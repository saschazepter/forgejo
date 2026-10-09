// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	actions_model "forgejo.org/models/actions"
	auth_model "forgejo.org/models/auth"
	repo_model "forgejo.org/models/repo"
	unit_model "forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/json"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/webhook"
	actions_service "forgejo.org/services/actions"
	notify_service "forgejo.org/services/notify"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"connectrpc.com/connect"
	gouuid "github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestActionsJobWithNeeds(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		t.Skip()
	}
	testCases := []struct {
		treePath         string
		fileContent      string
		outcomes         map[string]*mockTaskOutcome
		expectedStatuses map[string]string
	}{
		{
			treePath: ".gitea/workflows/job-with-needs.yml",
			fileContent: `name: job-with-needs
on:
  push:
    paths:
      - '.gitea/workflows/job-with-needs.yml'
jobs:
  job1:
    runs-on: ubuntu-latest
    steps:
      - run: echo job1
  job2:
    runs-on: ubuntu-latest
    needs: [job1]
    steps:
      - run: echo job2
`,
			outcomes: map[string]*mockTaskOutcome{
				"job1": {
					result: runnerv1.Result_RESULT_SUCCESS,
				},
				"job2": {
					result: runnerv1.Result_RESULT_SUCCESS,
				},
			},
			expectedStatuses: map[string]string{
				"job1": actions_model.StatusSuccess.String(),
				"job2": actions_model.StatusSuccess.String(),
			},
		},
		{
			treePath: ".gitea/workflows/job-with-needs-fail.yml",
			fileContent: `name: job-with-needs-fail
on:
  push:
    paths:
      - '.gitea/workflows/job-with-needs-fail.yml'
jobs:
  job1:
    runs-on: ubuntu-latest
    steps:
      - run: echo job1
  job2:
    runs-on: ubuntu-latest
    needs: [job1]
    steps:
      - run: echo job2
`,
			outcomes: map[string]*mockTaskOutcome{
				"job1": {
					result: runnerv1.Result_RESULT_FAILURE,
				},
			},
			expectedStatuses: map[string]string{
				"job1": actions_model.StatusFailure.String(),
				"job2": actions_model.StatusSkipped.String(),
			},
		},
		{
			treePath: ".gitea/workflows/job-with-needs-fail-if.yml",
			fileContent: `name: job-with-needs-fail-if
on:
  push:
    paths:
      - '.gitea/workflows/job-with-needs-fail-if.yml'
jobs:
  job1:
    runs-on: ubuntu-latest
    steps:
      - run: echo job1
  job2:
    runs-on: ubuntu-latest
    if: ${{ always() }}
    needs: [job1]
    steps:
      - run: echo job2
`,
			outcomes: map[string]*mockTaskOutcome{
				"job1": {
					result: runnerv1.Result_RESULT_FAILURE,
				},
				"job2": {
					result: runnerv1.Result_RESULT_SUCCESS,
				},
			},
			expectedStatuses: map[string]string{
				"job1": actions_model.StatusFailure.String(),
				"job2": actions_model.StatusSuccess.String(),
			},
		},
	}
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		session := loginUser(t, user2.Name)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)

		apiRepo := createActionsTestRepo(t, token, "actions-jobs-with-needs", false)
		runner := newMockRunner()
		runner.registerAsRepoRunner(t, user2.Name, apiRepo.Name, "mock-runner", []string{"ubuntu-latest"})

		for _, tc := range testCases {
			t.Run(fmt.Sprintf("test %s", tc.treePath), func(t *testing.T) {
				// create the workflow file
				opts := getWorkflowCreateFileOptions(user2, apiRepo.DefaultBranch, fmt.Sprintf("create %s", tc.treePath), tc.fileContent)
				fileResp := createWorkflowFile(t, token, user2.Name, apiRepo.Name, tc.treePath, opts)

				// fetch and execute task
				for i := 0; i < len(tc.outcomes); i++ {
					task := runner.fetchTask(t)
					jobName := getTaskJobNameByTaskID(t, token, user2.Name, apiRepo.Name, task.Id)
					outcome := tc.outcomes[jobName]
					assert.NotNil(t, outcome)
					runner.execTask(t, task, outcome)
				}

				// check result
				req := NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/%s/%s/actions/tasks", user2.Name, apiRepo.Name)).
					AddTokenAuth(token)
				resp := MakeRequest(t, req, http.StatusOK)
				var actionTaskRespAfter api.ActionTaskResponse
				DecodeJSON(t, resp, &actionTaskRespAfter)
				for _, apiTask := range actionTaskRespAfter.Entries {
					if apiTask.HeadSHA != fileResp.Commit.SHA {
						continue
					}
					status := apiTask.Status
					assert.Equal(t, status, tc.expectedStatuses[apiTask.Name])
				}
			})
		}

		httpContext := NewAPITestContext(t, user2.Name, apiRepo.Name, auth_model.AccessTokenScopeWriteUser)
		doAPIDeleteRepository(httpContext)(t)
	})
}

func TestActionsJobNeedsMatrix(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		t.Skip()
	}
	testCases := []struct {
		treePath          string
		fileContent       string
		outcomes          map[string]*mockTaskOutcome
		expectedTaskNeeds map[string]*runnerv1.TaskNeed // jobID => TaskNeed
	}{
		{
			treePath: ".gitea/workflows/jobs-outputs-with-matrix.yml",
			fileContent: `name: jobs-outputs-with-matrix
on:
  push:
    paths:
      - '.gitea/workflows/jobs-outputs-with-matrix.yml'
jobs:
  job1:
    runs-on: ubuntu-latest
    outputs:
      output_1: ${{ steps.gen_output.outputs.output_1 }}
      output_2: ${{ steps.gen_output.outputs.output_2 }}
      output_3: ${{ steps.gen_output.outputs.output_3 }}
    strategy:
      matrix:
        version: [1, 2, 3]
    steps:
      - name: Generate output
        id: gen_output
        run: |
          version="${{ matrix.version }}"
          echo "output_${version}=${version}" >> "$GITHUB_OUTPUT"
  job2:
    runs-on: ubuntu-latest
    needs: [job1]
    steps:
      - run: echo '${{ toJSON(needs.job1.outputs) }}'
`,
			outcomes: map[string]*mockTaskOutcome{
				"job1 (1)": {
					result: runnerv1.Result_RESULT_SUCCESS,
					outputs: map[string]string{
						"output_1": "1",
						"output_2": "",
						"output_3": "",
					},
				},
				"job1 (2)": {
					result: runnerv1.Result_RESULT_SUCCESS,
					outputs: map[string]string{
						"output_1": "",
						"output_2": "2",
						"output_3": "",
					},
				},
				"job1 (3)": {
					result: runnerv1.Result_RESULT_SUCCESS,
					outputs: map[string]string{
						"output_1": "",
						"output_2": "",
						"output_3": "3",
					},
				},
			},
			expectedTaskNeeds: map[string]*runnerv1.TaskNeed{
				"job1": {
					Result: runnerv1.Result_RESULT_SUCCESS,
					Outputs: map[string]string{
						"output_1": "1",
						"output_2": "2",
						"output_3": "3",
					},
				},
			},
		},
		{
			treePath: ".gitea/workflows/jobs-outputs-with-matrix-failure.yml",
			fileContent: `name: jobs-outputs-with-matrix-failure
on:
  push:
    paths:
      - '.gitea/workflows/jobs-outputs-with-matrix-failure.yml'
jobs:
  job1:
    runs-on: ubuntu-latest
    outputs:
      output_1: ${{ steps.gen_output.outputs.output_1 }}
      output_2: ${{ steps.gen_output.outputs.output_2 }}
      output_3: ${{ steps.gen_output.outputs.output_3 }}
    strategy:
      matrix:
        version: [1, 2, 3]
    steps:
      - name: Generate output
        id: gen_output
        run: |
          version="${{ matrix.version }}"
          echo "output_${version}=${version}" >> "$GITHUB_OUTPUT"
  job2:
    runs-on: ubuntu-latest
    if: ${{ always() }}
    needs: [job1]
    steps:
      - run: echo '${{ toJSON(needs.job1.outputs) }}'
`,
			outcomes: map[string]*mockTaskOutcome{
				"job1 (1)": {
					result: runnerv1.Result_RESULT_SUCCESS,
					outputs: map[string]string{
						"output_1": "1",
						"output_2": "",
						"output_3": "",
					},
				},
				"job1 (2)": {
					result: runnerv1.Result_RESULT_FAILURE,
					outputs: map[string]string{
						"output_1": "",
						"output_2": "",
						"output_3": "",
					},
				},
				"job1 (3)": {
					result: runnerv1.Result_RESULT_SUCCESS,
					outputs: map[string]string{
						"output_1": "",
						"output_2": "",
						"output_3": "3",
					},
				},
			},
			expectedTaskNeeds: map[string]*runnerv1.TaskNeed{
				"job1": {
					Result: runnerv1.Result_RESULT_FAILURE,
					Outputs: map[string]string{
						"output_1": "1",
						"output_2": "",
						"output_3": "3",
					},
				},
			},
		},
	}
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		session := loginUser(t, user2.Name)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)

		apiRepo := createActionsTestRepo(t, token, "actions-jobs-outputs-with-matrix", false)
		runner := newMockRunner()
		runner.registerAsRepoRunner(t, user2.Name, apiRepo.Name, "mock-runner", []string{"ubuntu-latest"})

		for _, tc := range testCases {
			t.Run(fmt.Sprintf("test %s", tc.treePath), func(t *testing.T) {
				opts := getWorkflowCreateFileOptions(user2, apiRepo.DefaultBranch, fmt.Sprintf("create %s", tc.treePath), tc.fileContent)
				createWorkflowFile(t, token, user2.Name, apiRepo.Name, tc.treePath, opts)

				for i := 0; i < len(tc.outcomes); i++ {
					task := runner.fetchTask(t)
					jobName := getTaskJobNameByTaskID(t, token, user2.Name, apiRepo.Name, task.Id)
					outcome := tc.outcomes[jobName]
					assert.NotNil(t, outcome)
					runner.execTask(t, task, outcome)
				}

				task := runner.fetchTask(t)
				actualTaskNeeds := task.Needs
				assert.Len(t, actualTaskNeeds, len(tc.expectedTaskNeeds))
				for jobID, tn := range tc.expectedTaskNeeds {
					actualNeed := actualTaskNeeds[jobID]
					assert.Equal(t, tn.Result, actualNeed.Result)
					assert.Len(t, actualNeed.Outputs, len(tn.Outputs))
					for outputKey, outputValue := range tn.Outputs {
						assert.Equal(t, outputValue, actualNeed.Outputs[outputKey])
					}
				}
			})
		}

		httpContext := NewAPITestContext(t, user2.Name, apiRepo.Name, auth_model.AccessTokenScopeWriteUser)
		doAPIDeleteRepository(httpContext)(t)
	})
}

func TestActionsGiteaContext(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		t.Skip()
	}

	testCases := []struct {
		name                string
		treePath            string
		fileContent         string
		enableOpenIDConnect bool
	}{
		{
			name:     "openid_connect_disabled",
			treePath: ".gitea/workflows/pull.yml",
			fileContent: `name: Pull Request
on: pull_request
jobs:
  wf1-job:
    runs-on: ubuntu-latest
    steps:
      - run: echo 'test the pull'
`,
			enableOpenIDConnect: false,
		},
		{
			name:     "openid_connect_enabled",
			treePath: ".gitea/workflows/pull-enabled.yml",
			fileContent: `name: Pull Request
on: pull_request
jobs:
  wf1-job:
    enable-openid-connect: true
    runs-on: ubuntu-latest
    steps:
      - run: echo 'test the pull'
`,
			enableOpenIDConnect: true,
		},
	}

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		user2Session := loginUser(t, user2.Name)
		user2Token := getTokenForLoggedInUser(t, user2Session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)

		apiBaseRepo := createActionsTestRepo(t, user2Token, "actions-gitea-context", false)
		baseRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: apiBaseRepo.ID})
		user2APICtx := NewAPITestContext(t, baseRepo.OwnerName, baseRepo.Name, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)

		runner := newMockRunner()
		runner.registerAsRepoRunner(t, baseRepo.OwnerName, baseRepo.Name, "mock-runner", []string{"ubuntu-latest"})

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				opts := getWorkflowCreateFileOptions(user2, baseRepo.DefaultBranch, fmt.Sprintf("create %s", tc.treePath), tc.fileContent)
				createWorkflowFile(t, user2Token, baseRepo.OwnerName, baseRepo.Name, tc.treePath, opts)
				// user2 creates a pull request
				doAPICreateFile(user2APICtx, "user2-patch.txt", &api.CreateFileOptions{
					FileOptions: api.FileOptions{
						NewBranchName: tc.name,
						Message:       "create user2-patch.txt",
						Author: api.Identity{
							Name:  user2.Name,
							Email: user2.Email,
						},
						Committer: api.Identity{
							Name:  user2.Name,
							Email: user2.Email,
						},
						Dates: api.CommitDateOptions{
							Author:    time.Now(),
							Committer: time.Now(),
						},
					},
					ContentBase64: base64.StdEncoding.EncodeToString([]byte("user2-fix")),
				})(t)
				apiPull, err := doAPICreatePullRequest(user2APICtx, baseRepo.OwnerName, baseRepo.Name, baseRepo.DefaultBranch, tc.name)(t)
				require.NoError(t, err)
				task := runner.fetchTask(t)
				gtCtx := task.Context.GetFields()
				actionTask := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: task.Id})
				actionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: actionTask.JobID})
				actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: actionRunJob.RunID})
				require.NoError(t, actionRun.LoadAttributes(t.Context()))

				assert.Equal(t, user2.Name, gtCtx["actor"].GetStringValue())
				assert.Equal(t, setting.AppURL+"api/v1", gtCtx["api_url"].GetStringValue())
				assert.Equal(t, apiPull.Base.Ref, gtCtx["base_ref"].GetStringValue())
				runEvent := map[string]any{}
				require.NoError(t, json.Unmarshal([]byte(actionRun.EventPayload), &runEvent))
				assert.True(t, reflect.DeepEqual(gtCtx["event"].GetStructValue().AsMap(), runEvent))
				assert.Equal(t, actionRun.TriggerEvent, gtCtx["event_name"].GetStringValue())
				assert.Equal(t, apiPull.Head.Ref, gtCtx["head_ref"].GetStringValue())
				assert.Equal(t, string(actionRunJob.JobID), gtCtx["job"].GetStringValue())
				assert.Equal(t, actionRun.Ref, gtCtx["ref"].GetStringValue())
				assert.Equal(t, git.RefName(actionRun.Ref).ShortName(), gtCtx["ref_name"].GetStringValue())
				assert.False(t, gtCtx["ref_protected"].GetBoolValue())
				assert.Equal(t, git.RefName(actionRun.Ref).RefType(), gtCtx["ref_type"].GetStringValue())
				assert.Equal(t, actionRun.Repo.OwnerName+"/"+actionRun.Repo.Name, gtCtx["repository"].GetStringValue())
				assert.Equal(t, actionRun.Repo.OwnerName, gtCtx["repository_owner"].GetStringValue())
				assert.Equal(t, actionRun.Repo.HTMLURL(), gtCtx["repositoryUrl"].GetStringValue())
				assert.Equal(t, fmt.Sprint(actionRunJob.RunID), gtCtx["run_id"].GetStringValue())
				assert.Equal(t, fmt.Sprint(actionRun.Index), gtCtx["run_number"].GetStringValue())
				assert.Equal(t, fmt.Sprint(actionRunJob.Attempt), gtCtx["run_attempt"].GetStringValue())
				assert.Equal(t, "Actions", gtCtx["secret_source"].GetStringValue())
				assert.Equal(t, setting.AppURL, gtCtx["server_url"].GetStringValue())
				assert.Equal(t, actionRun.CommitSHA, gtCtx["sha"].GetStringValue())
				assert.Equal(t, actionRun.WorkflowID, gtCtx["workflow"].GetStringValue())
				assert.Contains(t, gtCtx["workflow_ref"].GetStringValue(), fmt.Sprintf("user2/actions-gitea-context/%s@refs/pull", tc.treePath))
				assert.Equal(t, setting.Actions.DefaultActionsURL.URL(), gtCtx["gitea_default_actions_url"].GetStringValue())
				assert.Equal(t, setting.Actions.DefaultActionsURL.URL(), gtCtx["forgejo_default_actions_url"].GetStringValue())
				assert.Equal(t, setting.AppVer, gtCtx["forgejo_server_version"].GetStringValue())
				token := gtCtx["token"].GetStringValue()
				assert.Equal(t, actionTask.TokenLastEight, token[len(token)-8:])
				assert.NotEmpty(t, gtCtx["gitea_runtime_token"].GetStringValue())
				assert.NotEmpty(t, gtCtx["forgejo_runtime_token"].GetStringValue())
				if tc.enableOpenIDConnect {
					assert.NotEmpty(t, gtCtx["forgejo_actions_id_token_request_token"].GetStringValue())
					assert.Equal(t,
						fmt.Sprintf("%sapi/actions/_apis/pipelines/workflows/%d/idtoken?placeholder=true",
							setting.AppURL, actionRunJob.RunID), gtCtx["forgejo_actions_id_token_request_url"].GetStringValue(),
					)
				} else {
					assert.Empty(t, gtCtx["forgejo_actions_id_token_request_token"].GetStringValue())
					assert.Empty(t, gtCtx["forgejo_actions_id_token_request_url"].GetStringValue())
				}
			})
		}

		doAPIDeleteRepository(user2APICtx)(t)
	})
}

func TestActionsRunsOnInputsWorkflowDispatch(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		t.Skip()
	}

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		session := loginUser(t, user2.Name)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)

		testRepository := createActionsTestRepo(t, token, "actions-runs-on-inputs-workflow-dispatch", false)

		ubuntuRunner := newMockRunner()
		ubuntuRunner.registerAsRepoRunner(t, user2.Name, testRepository.Name, "ubuntu-runner", []string{"ubuntu"})

		windowsRunner := newMockRunner()
		windowsRunner.registerAsRepoRunner(t, user2.Name, testRepository.Name, "windows-runner", []string{"windows"})

		workflowPath := ".gitea/workflows/pull.yaml"
		workflow := `name: Test runs-on with inputs
on:
  workflow_dispatch:
    inputs:
      image:
        required: true
        type: string

jobs:
  test:
    runs-on: ${{ inputs.image }}
    steps:
      - run: echo "Running on ${{ inputs.image }}"
`

		options := getWorkflowCreateFileOptions(user2, testRepository.DefaultBranch, fmt.Sprintf("create %s", workflowPath), workflow)
		createWorkflowFile(t, token, user2.Name, testRepository.Name, workflowPath, options)

		url := fmt.Sprintf("/%s/%s/actions/manual", user2.Name, testRepository.Name)
		request := NewRequestWithValues(t, "POST", url, map[string]string{
			"inputs[image]": "windows",
			"ref":           testRepository.DefaultBranch,
			"workflow":      "pull.yaml",
			"actor":         strconv.FormatInt(user2.ID, 10),
		})
		session.MakeRequest(t, request, http.StatusSeeOther)

		assert.Nil(t, ubuntuRunner.maybeFetchTask(t))

		task := windowsRunner.fetchTask(t)
		actionTask := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: task.Id})
		actionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: actionTask.JobID})
		run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: actionRunJob.RunID})

		assert.Equal(t, "Test runs-on with inputs", run.Title)
	})
}

func TestActionsRunsOnVars(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		t.Skip()
	}

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		session := loginUser(t, user2.Name)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)

		testRepository := createActionsTestRepo(t, token, "actions-runs-on-vars", false)

		ubuntuRunner := newMockRunner()
		ubuntuRunner.registerAsRepoRunner(t, user2.Name, testRepository.Name, "ubuntu-runner", []string{"ubuntu"})

		windowsRunner := newMockRunner()
		windowsRunner.registerAsRepoRunner(t, user2.Name, testRepository.Name, "windows-runner", []string{"windows"})

		workflowPath := ".gitea/workflows/pull.yaml"
		workflow := `name: Test runs-on with vars
on:
  workflow_dispatch:

jobs:
  test:
    runs-on: ${{ vars.runner }}
    steps:
      - run: echo "Running on ${{ vars.runner }}"
`

		options := getWorkflowCreateFileOptions(user2, testRepository.DefaultBranch, fmt.Sprintf("create %s", workflowPath), workflow)
		createWorkflowFile(t, token, user2.Name, testRepository.Name, workflowPath, options)

		varCreationURL := fmt.Sprintf("/%s/%s/settings/actions/variables/new", user2.Name, testRepository.Name)
		varCreationRequest := NewRequestWithValues(t, "POST", varCreationURL, map[string]string{
			"name": "runner",
			"data": "ubuntu",
		})
		session.MakeRequest(t, varCreationRequest, http.StatusOK)

		dispatchURL := fmt.Sprintf("/%s/%s/actions/manual", user2.Name, testRepository.Name)
		dispatchRequest := NewRequestWithValues(t, "POST", dispatchURL, map[string]string{
			"ref":      testRepository.DefaultBranch,
			"workflow": "pull.yaml",
			"actor":    strconv.FormatInt(user2.ID, 10),
		})
		session.MakeRequest(t, dispatchRequest, http.StatusSeeOther)

		assert.Nil(t, windowsRunner.maybeFetchTask(t))

		task := ubuntuRunner.fetchTask(t)
		actionTask := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: task.Id})
		actionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: actionTask.JobID})
		run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: actionRunJob.RunID})

		assert.Equal(t, "Test runs-on with vars", run.Title)
	})
}

func TestActionsEphemeral(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		t.Skip()
	}

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		user2Session := loginUser(t, user2.Name)
		user2Token := getTokenForLoggedInUser(t, user2Session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)

		apiBaseRepo := createActionsTestRepo(t, user2Token, "actions-gitea-context", false)
		baseRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: apiBaseRepo.ID})
		user2APICtx := NewAPITestContext(t, baseRepo.OwnerName, baseRepo.Name, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)

		runner := newMockRunner()
		runner.registerAsEphemeralRepoRunner(t, baseRepo.OwnerName, baseRepo.Name, "mock-runner", []string{"ubuntu-latest"})

		// verify CleanupEphemeralRunners does not remove this runner
		err := actions_service.CleanupEphemeralRunners(t.Context())
		require.NoError(t, err)

		// init the workflow
		wfTreePath := ".gitea/workflows/pull.yml"
		wfFileContent := `name: Pull Request
on: pull_request
jobs:
  wf1-job:
    runs-on: ubuntu-latest
    steps:
      - run: echo 'test the pull'
  wf2-job:
    runs-on: ubuntu-latest
    steps:
      - run: echo 'test the pull'
`
		opts := getWorkflowCreateFileOptions(user2, baseRepo.DefaultBranch, fmt.Sprintf("create %s", wfTreePath), wfFileContent)
		createWorkflowFile(t, user2Token, baseRepo.OwnerName, baseRepo.Name, wfTreePath, opts)
		// user2 creates a pull request
		doAPICreateFile(user2APICtx, "user2-patch.txt", &api.CreateFileOptions{
			FileOptions: api.FileOptions{
				NewBranchName: "user2/patch-1",
				Message:       "create user2-patch.txt",
				Author: api.Identity{
					Name:  user2.Name,
					Email: user2.Email,
				},
				Committer: api.Identity{
					Name:  user2.Name,
					Email: user2.Email,
				},
				Dates: api.CommitDateOptions{
					Author:    time.Now(),
					Committer: time.Now(),
				},
			},
			ContentBase64: base64.StdEncoding.EncodeToString([]byte("user2-fix")),
		})(t)
		_, err = doAPICreatePullRequest(user2APICtx, baseRepo.OwnerName, baseRepo.Name, baseRepo.DefaultBranch, "user2/patch-1")(t)
		require.NoError(t, err)
		task := runner.fetchTask(t)
		actionTask := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: task.Id})
		actionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: actionTask.JobID})
		actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: actionRunJob.RunID})
		require.NoError(t, actionRun.LoadAttributes(t.Context()))

		runEvent := map[string]any{}
		require.NoError(t, json.Unmarshal([]byte(actionRun.EventPayload), &runEvent))

		// verify CleanupEphemeralRunners does not remove this runner
		err = actions_service.CleanupEphemeralRunners(t.Context())
		require.NoError(t, err)

		resp, err := runner.client.runnerServiceClient.FetchTask(t.Context(), connect.NewRequest(&runnerv1.FetchTaskRequest{
			TasksVersion: 0,
		}))
		require.NoError(t, err)
		assert.Nil(t, resp.Msg.Task)

		// verify CleanupEphemeralRunners does not remove this runner
		err = actions_service.CleanupEphemeralRunners(t.Context())
		require.NoError(t, err)

		runner.client.runnerServiceClient.UpdateTask(t.Context(), connect.NewRequest(&runnerv1.UpdateTaskRequest{
			State: &runnerv1.TaskState{
				Id:     actionTask.ID,
				Result: runnerv1.Result_RESULT_SUCCESS,
			},
		}))
		resp, err = runner.client.runnerServiceClient.FetchTask(t.Context(), connect.NewRequest(&runnerv1.FetchTaskRequest{
			TasksVersion: 0,
		}))
		require.Error(t, err)
		assert.Nil(t, resp)

		// create an runner that picks a job and get force cancelled
		runnerToBeRemoved := newMockRunner()
		runnerToBeRemoved.registerAsEphemeralRepoRunner(t, baseRepo.OwnerName, baseRepo.Name, "mock-runner-to-be-removed", []string{"ubuntu-latest"})

		taskToStopAPIObj := runnerToBeRemoved.fetchTask(t)

		taskToStop := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: taskToStopAPIObj.Id})

		// verify CleanupEphemeralRunners does not remove the custom crafted runner
		err = actions_service.CleanupEphemeralRunners(t.Context())
		require.NoError(t, err)

		runnerToRemove := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunner{ID: taskToStop.RunnerID})

		err = actions_service.StopTask(t.Context(), taskToStop.ID, actions_model.StatusFailure)
		require.NoError(t, err)

		// verify CleanupEphemeralRunners does remove the custom crafted runner
		err = actions_service.CleanupEphemeralRunners(t.Context())
		require.NoError(t, err)

		unittest.AssertNotExistsBean(t, &actions_model.ActionRunner{ID: runnerToRemove.ID})

		// this cleanup is required to allow further tests to pass
		doAPIDeleteRepository(user2APICtx)(t)
	})
}

func createActionsTestRepo(t *testing.T, authToken, repoName string, isPrivate bool) *api.Repository {
	req := NewRequestWithJSON(t, "POST", "/api/v1/user/repos", &api.CreateRepoOption{
		Name:          repoName,
		Private:       isPrivate,
		Readme:        "Default",
		AutoInit:      true,
		DefaultBranch: "main",
	}).AddTokenAuth(authToken)
	resp := MakeRequest(t, req, http.StatusCreated)
	var apiRepo api.Repository
	DecodeJSON(t, resp, &apiRepo)
	return &apiRepo
}

func getWorkflowCreateFileOptions(u *user_model.User, branch, msg, content string) *api.CreateFileOptions {
	return &api.CreateFileOptions{
		FileOptions: api.FileOptions{
			BranchName: branch,
			Message:    msg,
			Author: api.Identity{
				Name:  u.Name,
				Email: u.Email,
			},
			Committer: api.Identity{
				Name:  u.Name,
				Email: u.Email,
			},
			Dates: api.CommitDateOptions{
				Author:    time.Now(),
				Committer: time.Now(),
			},
		},
		ContentBase64: base64.StdEncoding.EncodeToString([]byte(content)),
	}
}

func createWorkflowFile(t *testing.T, authToken, ownerName, repoName, treePath string, opts *api.CreateFileOptions) *api.FileResponse {
	req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/%s/contents/%s", ownerName, repoName, treePath), opts).
		AddTokenAuth(authToken)
	resp := MakeRequest(t, req, http.StatusCreated)
	var fileResponse api.FileResponse
	DecodeJSON(t, resp, &fileResponse)
	return &fileResponse
}

// getTaskJobNameByTaskID get the job name of the task by task ID
// there is currently not an API for querying a task by ID so we have to list all the tasks
func getTaskJobNameByTaskID(t *testing.T, authToken, ownerName, repoName string, taskID int64) string {
	// FIXME: we may need to query several pages
	req := NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/%s/%s/actions/tasks", ownerName, repoName)).
		AddTokenAuth(authToken)
	resp := MakeRequest(t, req, http.StatusOK)
	var taskRespBefore api.ActionTaskResponse
	DecodeJSON(t, resp, &taskRespBefore)
	for _, apiTask := range taskRespBefore.Entries {
		if apiTask.ID == taskID {
			return apiTask.Name
		}
	}
	return ""
}

func TestActionsRunsEvaluateIf(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		t.Skip()
	}

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		t.Run("skip all jobs instantly", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			runID := arif.dispatchSingleJob("${{ 'abc' == 'def' }}").ID
			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			arif.assertNoRunnableJobs()
			assert.Equal(t, actions_model.StatusSkipped, run.Status)
			job := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID})
			assert.Equal(t, actions_model.StatusSkipped, job.Status)
		})

		t.Run("skip entire run fires notifier", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)

			notifier := notify_service.NewMockNotifier(t)
			notifier.On("Run").Return().Maybe()
			notifier.On("PushCommits", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
			notifier.On("NewWorkflowJobAttempt", mock.Anything, mock.Anything).Return()
			notifier.On("NewWorkflowRunAttempt", mock.Anything, mock.Anything).Return()
			notifier.On("WorkflowRunCompleted", mock.Anything, mock.Anything, mock.Anything).Return()

			notify_service.RegisterNotifier(notifier)
			defer notify_service.UnregisterNotifier(notifier)

			arif.dispatchSingleJob("${{ 'abc' == 'def' }}")
			arif.assertNoRunnableJobs()

			notifier.AssertNumberOfCalls(t, "NewWorkflowJobAttempt", 1)
			notifier.AssertNumberOfCalls(t, "NewWorkflowRunAttempt", 1)
			notifier.AssertNumberOfCalls(t, "WorkflowRunCompleted", 1)
			notifier.AssertCalled(
				t, "NewWorkflowJobAttempt", mock.Anything,
				mock.MatchedBy(func(job *actions_model.ActionRunJob) bool {
					return job.Status == actions_model.StatusSkipped
				}),
			)
			notifier.AssertCalled(t,
				"NewWorkflowRunAttempt",
				mock.MatchedBy(func(ctx context.Context) bool { return ctx != nil }),
				mock.MatchedBy(func(run *actions_model.ActionRun) bool {
					return run.Title == ".forgejo/workflows/serverside_if.yml" &&
						run.Status == actions_model.StatusWaiting
				}))
			notifier.AssertCalled(t, "WorkflowRunCompleted",
				mock.MatchedBy(func(ctx context.Context) bool { return ctx != nil }),
				mock.MatchedBy(func(run *actions_model.ActionRun) bool {
					return run.Title == ".forgejo/workflows/serverside_if.yml" &&
						run.Status == actions_model.StatusSkipped
				}),
				actions_model.StatusWaiting,
			)
		})

		t.Run("skip single job instantly", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			arif.dispatchMultipleJobs("${{ 'abc' == 'abc' }}", "${{ 'abc' == 'def' }}")

			task := arif.mockRunTask()
			arif.assertNoRunnableJobs() // just one runnable job

			actionTask := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: task.Id})
			actionRunJob1 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: actionTask.JobID})
			assert.Equal(t, "test-1", actionRunJob1.Name)
			assert.Equal(t, actions_model.StatusSuccess, actionRunJob1.Status)
			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: actionRunJob1.RunID})
			assert.Equal(t, actions_model.StatusSuccess, run.Status)

			actionRunJob2 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-2"})
			assert.Equal(t, actions_model.StatusSkipped, actionRunJob2.Status)
		})

		t.Run("if clause needs another job, then is skipped", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			runID := arif.dispatchDependentJob().ID

			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusWaiting, run.Status)
			job1 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-1"})
			assert.Equal(t, actions_model.StatusWaiting, job1.Status)
			job2 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-2"})
			assert.Equal(t, actions_model.StatusBlocked, job2.Status) // `if` clause contains `needs`, can't be evaluated at schedule-time, still gets blocked

			arif.mockRunTask()

			job1 = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-1"})
			assert.Equal(t, actions_model.StatusSuccess, job1.Status)
			job2 = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-2"})
			assert.Equal(t, actions_model.StatusSkipped, job2.Status) // `if` clause contains `needs`, `if` is false, it is skipped

			arif.assertNoRunnableJobs()
		})

		t.Run("if clause needs another job, then is run", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			runID := arif.dispatchDependentJob().ID

			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusWaiting, run.Status)
			job1 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-1"})
			assert.Equal(t, actions_model.StatusWaiting, job1.Status)
			job2 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-2"})
			assert.Equal(t, actions_model.StatusBlocked, job2.Status) // `if` clause contains `needs`, can't be evaluated at schedule-time, still gets blocked

			arif.mockRunTaskAndFail()

			job1 = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-1"})
			assert.Equal(t, actions_model.StatusFailure, job1.Status)
			job2 = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-2"})
			assert.Equal(t, actions_model.StatusWaiting, job2.Status) // `if` clause contains `needs`, 'if' is true, goes to waiting

			arif.mockRunTask()
		})

		t.Run("if clause default / success() skips automatically when needed job fails", func(t *testing.T) {
			test := func(t *testing.T, dispatch func(arif *ActionsRunIfTester) int64) {
				defer tests.PrintCurrentTest(t)()

				arif := newActionsRunIfTester(t)
				runID := dispatch(arif)

				run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
				assert.Equal(t, actions_model.StatusWaiting, run.Status)
				job1 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-1"})
				assert.Equal(t, actions_model.StatusWaiting, job1.Status)
				job2 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-2"})
				assert.Equal(t, actions_model.StatusBlocked, job2.Status) // job is blocked by 'needs'

				arif.mockRunTaskAndFail()

				job1 = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-1"})
				assert.Equal(t, actions_model.StatusFailure, job1.Status)
				job2 = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-2"})
				assert.Equal(t, actions_model.StatusSkipped, job2.Status) // `if` defaults to success(), now gets skipped due to failure

				arif.assertNoRunnableJobs()
			}
			t.Run("default", func(t *testing.T) {
				test(t, func(arif *ActionsRunIfTester) int64 {
					return arif.dispatchDependentJobDefaultIf().ID
				})
			})
			t.Run("explicit success()", func(t *testing.T) {
				test(t, func(arif *ActionsRunIfTester) int64 {
					return arif.dispatchDependentJobIfSuccess().ID
				})
			})
		})

		t.Run("if failure() on successful dependency is skipped", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			runID := arif.dispatchIfFailure().ID

			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusWaiting, run.Status)
			job1 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-1"})
			assert.Equal(t, actions_model.StatusWaiting, job1.Status)
			job2 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-2"})
			assert.Equal(t, actions_model.StatusBlocked, job2.Status) // job is blocked by 'needs'

			arif.mockRunTask()

			job1 = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-1"})
			assert.Equal(t, actions_model.StatusSuccess, job1.Status)
			job2 = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-2"})
			assert.Equal(t, actions_model.StatusSkipped, job2.Status) // skipped because dependent job didn't fail

			arif.assertNoRunnableJobs()
		})

		t.Run("if failure() on failed dependency is executed", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			runID := arif.dispatchIfFailure().ID

			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusWaiting, run.Status)
			job1 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-1"})
			assert.Equal(t, actions_model.StatusWaiting, job1.Status)
			job2 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-2"})
			assert.Equal(t, actions_model.StatusBlocked, job2.Status) // job is blocked by 'needs'

			arif.mockRunTaskAndFail()

			job1 = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-1"})
			assert.Equal(t, actions_model.StatusFailure, job1.Status)
			job2 = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-2"})
			assert.Equal(t, actions_model.StatusWaiting, job2.Status) // waiting, dependent job failed

			arif.mockRunTask()

			job2 = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-2"})
			assert.Equal(t, actions_model.StatusSuccess, job2.Status)
		})

		t.Run("if always()", func(t *testing.T) {
			test := func(t *testing.T, runTask func(arif *ActionsRunIfTester)) {
				defer tests.PrintCurrentTest(t)()

				arif := newActionsRunIfTester(t)
				runID := arif.dispatchIfAlways().ID

				run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
				assert.Equal(t, actions_model.StatusWaiting, run.Status)
				job1 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-1"})
				assert.Equal(t, actions_model.StatusWaiting, job1.Status)
				job2 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-2"})
				assert.Equal(t, actions_model.StatusBlocked, job2.Status) // job is blocked by 'needs'

				runTask(arif)

				job2 = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-2"})
				assert.Equal(t, actions_model.StatusWaiting, job2.Status) // job can now be run regardless of status of test-1
				arif.mockRunTask()
			}
			t.Run("runs on success", func(t *testing.T) {
				test(t, func(arif *ActionsRunIfTester) {
					arif.mockRunTask()
				})
			})
			t.Run("runs on failure", func(t *testing.T) {
				test(t, func(arif *ActionsRunIfTester) {
					arif.mockRunTaskAndFail()
				})
			})
		})

		t.Run("if references env", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			runID := arif.dispatchSingleJob("${{ env.abc == 'def' }}").ID

			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusWaiting, run.Status)
			job1 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test"})
			assert.Equal(t, actions_model.StatusWaiting, job1.Status) // accessing env can't be evaluated server-side, so is set to waiting
		})

		t.Run("if references secrets", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			runID := arif.dispatchSingleJob("${{ secrets.abc == 'def' }}").ID

			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusWaiting, run.Status)
			job1 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test"})
			assert.Equal(t, actions_model.StatusWaiting, job1.Status) // accessing secrets isn't evaluated server-side, so is set to waiting
		})

		t.Run("jobs that need other jobs have their if clauses evaluated if unblocked", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			runID := arif.dispatchIfFalseChain().ID

			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusSkipped, run.Status)
			job1 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-1"})
			assert.Equal(t, actions_model.StatusSkipped, job1.Status)
			job2 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-2"})
			assert.Equal(t, actions_model.StatusSkipped, job2.Status)
			job3 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID, Name: "test-3"})
			assert.Equal(t, actions_model.StatusSkipped, job3.Status)
		})

		t.Run("access var during evaluation", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			runID := arif.dispatchForgejoTesting().ID

			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusWaiting.String(), run.Status.String())

			// Theoretically these are the exact right order for these to be executed, but it's possible they get
			// inserted into the database with identical creation times and therefore could have indeterminate sorting.
			// So the test case here is order-flexible.
			expectedJobs := []string{"backend-checks", "frontend-checks", "test-unit", "test-pgsql", "test-sqlite", "security-check", "semgrep"}
			for len(expectedJobs) > 0 {
				task := arif.mockRunTask()
				dbTask := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: task.Id})
				require.NoError(t, dbTask.LoadJob(t.Context()))

				idx := slices.Index(expectedJobs, dbTask.Job.Name)
				require.NotEqual(t, -1, idx, "could not find job %s in expectedJobs", dbTask.Job.Name)
				expectedJobs = append(expectedJobs[:idx], expectedJobs[idx+1:]...)
			}

			run = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusSuccess.String(), run.Status.String())
		})

		t.Run("access input during evaluation", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			runID := arif.dispatchInputConditional().ID

			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusWaiting.String(), run.Status.String())

			for _, expected := range []string{"test-1", "test-2"} {
				task := arif.mockRunTask()
				dbTask := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: task.Id})
				require.NoError(t, dbTask.LoadJob(t.Context()))
				assert.Equal(t, expected, dbTask.Job.Name)
			}

			run = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusSuccess.String(), run.Status.String())
		})

		t.Run("access forgejo context during workflow_dispatch evaluation", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			runID := arif.dispatchForgejoConditional().ID

			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusWaiting.String(), run.Status.String())

			for _, expected := range []string{"test-1", "test-2"} {
				task := arif.mockRunTask()
				dbTask := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: task.Id})
				require.NoError(t, dbTask.LoadJob(t.Context()))
				assert.Equal(t, expected, dbTask.Job.Name)
			}

			run = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusSuccess.String(), run.Status.String())
		})

		t.Run("access forgejo context during scheduled evaluation", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			runID := arif.scheduleForgejoConditional().ID

			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusWaiting.String(), run.Status.String())

			for _, expected := range []string{"test-1", "test-2"} {
				task := arif.mockRunTask()
				dbTask := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: task.Id})
				require.NoError(t, dbTask.LoadJob(t.Context()))
				assert.Equal(t, expected, dbTask.Job.Name)
			}

			run = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusSuccess.String(), run.Status.String())
		})

		t.Run("access forgejo context during event evaluation", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			runID := arif.eventForgejoConditional().ID

			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusWaiting.String(), run.Status.String())

			for _, expected := range []string{"test-1", "test-2"} {
				task := arif.mockRunTask()
				dbTask := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: task.Id})
				require.NoError(t, dbTask.LoadJob(t.Context()))
				assert.Equal(t, expected, dbTask.Job.Name)
			}

			run = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusSuccess.String(), run.Status.String())
		})

		t.Run("matrix requires skipped job", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			runID := arif.dispatchIncompleteMatrix().ID

			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusSkipped.String(), run.Status.String())
		})

		t.Run("runs-on requires skipped job", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			runID := arif.dispatchIncompleteRunsOn().ID

			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusSkipped.String(), run.Status.String())
		})

		t.Run("with requires skipped job", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			runID := arif.dispatchIncompleteWith().ID

			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusSkipped.String(), run.Status.String())
		})

		// If a `strategy.matrix` is used to expand a dependant job into multiple jobs, and, that expansion also
		// requires an input from another job, and, that other job has a server-side evaluated `if` that returns
		// false...
		t.Run("matrix expansion and incomplete job simultaneously job", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			runID := arif.dispatchIncompleteRunsOnWithMatrix().ID

			// Provide three values to the 'dim1' output which will become a matrix dimension.
			task := arif.mockRunTaskWithOutputs(map[string]string{"dim1": "[\"abc\",\"def\",\"ghj\"]"})
			dbTask := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: task.Id})
			require.NoError(t, dbTask.LoadJob(t.Context()))
			assert.Equal(t, "matrix-output", dbTask.Job.Name)

			// runs-on-output will be server-side skipped, and dependent-job will have a cascade skip because the
			// 'runs-on' value refers to a skipped job.
			arif.assertNoRunnableJobs()

			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusSuccess.String(), run.Status.String())

			jobs, err := actions_model.GetRunJobsByRunID(t.Context(), run.ID)
			require.NoError(t, err)
			// Currently this behaviour doesn't expand the jobs, it goes straight to skipping them.  That's reasonable
			// behaviour at the moment.  In the future, it would also be reasonable for it to expand-then-skip -- both
			// choices have the same functional outputs.  The expand-then-skip option may become the behaviour if we add
			// `matrix` context access to the `if` block in the future, since the evaluation order would have to change
			// a bit.
			expected := []string{"matrix-output", "runs-on-output", "dependent-job (incomplete matrix)"}
			for _, j := range jobs {
				if assert.Contains(t, expected, j.Name) {
					expected = slices.DeleteFunc(expected, func(s string) bool { return s == j.Name })
				}
			}
		})

		t.Run("sends job that fails server-side if evaluation to runner", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			runID := arif.dispatchSingleJob("${{ fromJSON('abc') }}").ID // invalid JSON
			arif.mockRunTaskAndFail()                                    // mock failure to evaluate the if from the runner
			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusFailure, run.Status)
			job := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID})
			assert.Equal(t, actions_model.StatusFailure, job.Status)
		})

		t.Run("sends unblocked job that fails server-side if evaluation to runner", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			arif := newActionsRunIfTester(t)
			runID := arif.dispatchMultipleJobs("${{ 'abc' == 'abc' }}", "${{ fromJSON('abc') }}").ID // invalid JSON
			task1 := arif.mockRunTask()
			task2 := arif.mockRunTaskAndFail() // mock failure to evaluate the if from the runner

			actionTask1 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: task1.Id})
			actionRunJob1 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: actionTask1.JobID})
			assert.Equal(t, actions_model.StatusSuccess, actionRunJob1.Status)

			actionTask2 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: task2.Id})
			actionRunJob2 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: actionTask2.JobID})
			assert.Equal(t, actions_model.StatusFailure, actionRunJob2.Status)

			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runID})
			assert.Equal(t, actions_model.StatusFailure, run.Status)
		})
	})
}

type ActionsRunIfTester struct {
	t        *testing.T
	user     *user_model.User
	session  *TestSession
	apiToken string
	apiRepo  *api.Repository
	runner   *mockRunner
}

func newActionsRunIfTester(t *testing.T) *ActionsRunIfTester {
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	session := loginUser(t, user2.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)

	testRepository := createActionsTestRepo(t, token, fmt.Sprintf("actions-runs-on-vars-%s", gouuid.New().String()), false)

	MakeRequest(t,
		NewRequestWithJSON(t, "POST",
			fmt.Sprintf("/api/v1/repos/%s/%s/actions/variables/ROLE", user2.Name, testRepository.Name),
			api.CreateVariableOption{Value: "forgejo-coding"}).AddTokenAuth(token),
		http.StatusNoContent)

	runner := newMockRunner()
	runner.registerAsRepoRunner(t, user2.Name, testRepository.Name, "ubuntu-runner", []string{"ubuntu"})

	return &ActionsRunIfTester{
		t:        t,
		user:     user2,
		session:  session,
		apiToken: token,
		apiRepo:  testRepository,
		runner:   runner,
	}
}

func (tester *ActionsRunIfTester) addWorkflowFile(filename, workflow string) *api.FileResponse {
	options := getWorkflowCreateFileOptions(tester.user, tester.apiRepo.DefaultBranch, fmt.Sprintf("create %s", filename), workflow)
	return createWorkflowFile(tester.t, tester.apiToken, tester.user.Name, tester.apiRepo.Name, filename, options)
}

func (tester *ActionsRunIfTester) dispatch(workflow string) *api.DispatchWorkflowRun {
	workflowPath := ".forgejo/workflows/serverside_if.yml"
	tester.addWorkflowFile(workflowPath, workflow)

	dispatchRequest := NewRequestWithJSON(tester.t, "POST",
		fmt.Sprintf("/api/v1/repos/%s/%s/actions/workflows/serverside_if.yml/dispatches", tester.user.Name, tester.apiRepo.Name),
		&api.DispatchWorkflowOption{
			Ref:           tester.apiRepo.DefaultBranch,
			ReturnRunInfo: true,
			Inputs: map[string]string{
				"input_1": "input_1 value",
			},
		}).
		AddTokenAuth(tester.apiToken)
	resp := MakeRequest(tester.t, dispatchRequest, http.StatusCreated)
	run := &api.DispatchWorkflowRun{}
	DecodeJSON(tester.t, resp, run)
	return run
}

func (tester *ActionsRunIfTester) pushEvent(workflow string) *actions_model.ActionRun {
	workflowPath := ".forgejo/workflows/serverside_if.yml"
	resp := tester.addWorkflowFile(workflowPath, workflow)
	return unittest.AssertExistsAndLoadBean(tester.t, &actions_model.ActionRun{CommitSHA: resp.Commit.SHA})
}

func (tester *ActionsRunIfTester) forceSchedule(workflow string) *actions_model.ActionRun {
	workflowPath := ".forgejo/workflows/serverside_if.yml"
	resp := tester.addWorkflowFile(workflowPath, workflow)
	payload := &api.SchedulePayload{
		Action: api.HookScheduleCreated,
	}
	p, err := json.Marshal(payload)
	require.NoError(tester.t, err)
	require.NoError(tester.t, actions_service.CreateScheduleTask(tester.t.Context(),
		&actions_model.ActionSchedule{
			RepoID:            tester.apiRepo.ID,
			OwnerID:           tester.user.ID,
			WorkflowID:        "serverside_if.yml",
			WorkflowDirectory: ".forgejo/workflows",
			TriggerUserID:     tester.user.ID,
			Ref:               "refs/heads/main",
			CommitSHA:         resp.Commit.SHA,
			Event:             webhook.HookEventSchedule,
			EventPayload:      string(p),
			Content:           []byte(workflow),
		}))
	return unittest.AssertExistsAndLoadBean(tester.t, &actions_model.ActionRun{CommitSHA: resp.Commit.SHA})
}

func (tester *ActionsRunIfTester) dispatchSingleJob(ifClause string) *api.DispatchWorkflowRun {
	return tester.dispatch(fmt.Sprintf(`
on:
  workflow_dispatch:

jobs:
  test:
    runs-on: ubuntu
    if: %s
    steps:
      - run: echo "Job contents go here."
`, ifClause))
}

func (tester *ActionsRunIfTester) dispatchMultipleJobs(ifClause1, ifClause2 string) *api.DispatchWorkflowRun {
	return tester.dispatch(fmt.Sprintf(`
on:
  workflow_dispatch:

jobs:
  test-1:
    runs-on: ubuntu
    if: %s
    steps:
      - run: echo "Job contents go here."
  test-2:
    runs-on: ubuntu
    if: %s
    steps:
      - run: echo "Job contents go here."
`, ifClause1, ifClause2))
}

func (tester *ActionsRunIfTester) dispatchDependentJob() *api.DispatchWorkflowRun {
	return tester.dispatch(`
on:
  workflow_dispatch:

jobs:
  test-1:
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
  test-2:
    needs: [test-1]
    runs-on: ubuntu
    if: ${{ needs.test-1.result == 'failure' }}
    steps:
      - run: echo "Job contents go here."
`)
}

func (tester *ActionsRunIfTester) dispatchDependentJobDefaultIf() *api.DispatchWorkflowRun {
	return tester.dispatch(`
on:
  workflow_dispatch:

jobs:
  test-1:
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
  test-2:
    needs: [test-1]
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
`)
}

func (tester *ActionsRunIfTester) dispatchDependentJobIfSuccess() *api.DispatchWorkflowRun {
	return tester.dispatch(`
on:
  workflow_dispatch:

jobs:
  test-1:
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
  test-2:
    needs: [test-1]
    if: success()
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
`)
}

func (tester *ActionsRunIfTester) dispatchIfFailure() *api.DispatchWorkflowRun {
	return tester.dispatch(`
on:
  workflow_dispatch:

jobs:
  test-1:
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
  test-2:
    needs: [test-1]
    if: failure()
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
`)
}

func (tester *ActionsRunIfTester) dispatchIfAlways() *api.DispatchWorkflowRun {
	return tester.dispatch(`
on:
  workflow_dispatch:

jobs:
  test-1:
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
  test-2:
    needs: [test-1]
    if: always()
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
`)
}

func (tester *ActionsRunIfTester) dispatchIfFalseChain() *api.DispatchWorkflowRun {
	return tester.dispatch(`
on:
  workflow_dispatch:

jobs:
  test-1:
    if: false
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
  test-2:
    needs: [test-1]
    if: false
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
  test-3:
    needs: [test-2]
    if: false
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
`)
}

func (tester *ActionsRunIfTester) dispatchForgejoTesting() *api.DispatchWorkflowRun {
	// Trimmed down copy of `.forgejo/workflows/testing.yml` to create a more complex & realistic `needs` tree with `if`
	// conditions on every job:
	return tester.dispatch(`
name: testing
enable-email-notifications: true
on:
  workflow_dispatch:
jobs:
  backend-checks:
    if: vars.ROLE == 'forgejo-coding' || vars.ROLE == 'forgejo-testing'
    runs-on: ubuntu
    steps:
      - run: echo "backend-checks job"
  frontend-checks:
    if: vars.ROLE == 'forgejo-coding' || vars.ROLE == 'forgejo-testing'
    runs-on: ubuntu
    steps:
      - run: echo "frontend-checks job"
  test-unit:
    if: vars.ROLE == 'forgejo-coding' || vars.ROLE == 'forgejo-testing'
    runs-on: ubuntu
    needs: [backend-checks, frontend-checks]
    steps:
      - run: echo "test-unit job"
  test-pgsql:
    if: vars.ROLE == 'forgejo-coding' || vars.ROLE == 'forgejo-testing'
    runs-on: ubuntu
    needs: [backend-checks, frontend-checks]
    steps:
      - run: echo "test-pgsql job"
  test-sqlite:
    if: vars.ROLE == 'forgejo-coding' || vars.ROLE == 'forgejo-testing'
    runs-on: ubuntu
    needs: [backend-checks, frontend-checks]
    steps:
      - run: echo "test-sqlite job"
  security-check:
    if: vars.ROLE == 'forgejo-coding' || vars.ROLE == 'forgejo-testing'
    runs-on: ubuntu
    needs:
      - test-sqlite
      - test-unit
    steps:
      - run: echo "security-check job"
  semgrep:
    if: vars.ROLE == 'forgejo-coding' || vars.ROLE == 'forgejo-testing'
    runs-on: ubuntu
    steps:
      - run: echo "semgrep job"
`)
}

func (tester *ActionsRunIfTester) dispatchInputConditional() *api.DispatchWorkflowRun {
	return tester.dispatch(`
on:
  workflow_dispatch:
    inputs:
      input_1:
        type: string
jobs:
  test-1:
    if: ${{ inputs.input_1 == 'input_1 value' }}
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
  test-2:
    needs: [test-1]
    if: ${{ inputs.input_1 == 'input_1 value' }}
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
`)
}

func (tester *ActionsRunIfTester) dispatchForgejoConditional() *api.DispatchWorkflowRun {
	return tester.dispatch(`
on:
  workflow_dispatch:
jobs:
  test-1:
    if: ${{ forgejo.event_name == 'workflow_dispatch' }}
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
  test-2:
    needs: [test-1]
    if: ${{ forgejo.event_name == 'workflow_dispatch' }}
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
`)
}

func (tester *ActionsRunIfTester) eventForgejoConditional() *actions_model.ActionRun {
	return tester.pushEvent(`
on:
  push:
jobs:
  test-1:
    if: ${{ forgejo.event_name == 'push' }}
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
  test-2:
    needs: [test-1]
    if: ${{ forgejo.event_name == 'push' }}
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
`)
}

func (tester *ActionsRunIfTester) scheduleForgejoConditional() *actions_model.ActionRun {
	return tester.forceSchedule(`
on:
  schedule:
jobs:
  test-1:
    if: ${{ forgejo.repository_owner == 'user2' }}
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
  test-2:
    needs: [test-1]
    if: ${{ forgejo.repository_owner == 'user2' }}
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
`)
}

func (tester *ActionsRunIfTester) dispatchIncompleteMatrix() *api.DispatchWorkflowRun {
	return tester.dispatch(`
on:
  workflow_dispatch:
jobs:
  test-1:
    if: ${{ '123' == 'abc' }}
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
  test-2:
    needs: [test-1]
    strategy:
      matrix:
        dim1: ${{ needs.test-1.outputs.output-not-existing }}
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
`)
}

func (tester *ActionsRunIfTester) dispatchIncompleteRunsOn() *api.DispatchWorkflowRun {
	return tester.dispatch(`
on:
  workflow_dispatch:
jobs:
  test-1:
    if: ${{ '123' == 'abc' }}
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
  test-2:
    needs: [test-1]
    runs-on: ${{ needs.test-1.outputs.output-not-existing }}
    steps:
      - run: echo "Job contents go here."
`)
}

func (tester *ActionsRunIfTester) dispatchIncompleteRunsOnWithMatrix() *api.DispatchWorkflowRun {
	return tester.dispatch(`
on:
  workflow_dispatch:
jobs:
  # This job will provide an output...
  matrix-output:
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here." # (output would be here, but runner will be mocked)
  # This job will be relied upon for an output, but will be skipped.
  runs-on-output:
    if: ${{ '123' == 'abc' }}
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here." # (output would be here, but runner will be mocked)
  dependent-job:
    needs: [matrix-output, runs-on-output]
    strategy:
      matrix:
        dim1: ${{ fromJSON(needs.matrix-output.outputs.dim1) }}
    runs-on: ${{ needs.runs-on-output.outputs.output-not-existing }}
    steps:
      - run: echo "Job contents go here."
`)
}

func (tester *ActionsRunIfTester) dispatchIncompleteWith() *api.DispatchWorkflowRun {
	tester.addWorkflowFile(".forgejo/workflows/reusable.yml", `
on:
  workflow_call:
    inputs:
      my_input:
        type: string
jobs:
  inner-job:
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
`)

	return tester.dispatch(`
on:
  workflow_dispatch:
jobs:
  test-1:
    if: ${{ '123' == 'abc' }}
    runs-on: ubuntu
    steps:
      - run: echo "Job contents go here."
  test-2:
    needs: [test-1]
    uses: ./.forgejo/workflows/reusable.yml
    with:
      my_input: ${{ needs.test-1.outputs.output-not-existing }}
`)
}

func (tester *ActionsRunIfTester) assertNoRunnableJobs() {
	assert.Nil(tester.t, tester.runner.maybeFetchTask(tester.t))
}

func (tester *ActionsRunIfTester) mockRunTask() *runnerv1.Task {
	return tester.mockRunTaskWithOutputs(nil)
}

func (tester *ActionsRunIfTester) mockRunTaskWithOutputs(outputs map[string]string) *runnerv1.Task {
	task := tester.runner.fetchTask(tester.t)
	tester.runner.execTask(tester.t, task, &mockTaskOutcome{
		result:  runnerv1.Result_RESULT_SUCCESS,
		outputs: outputs,
	})
	return task
}

func (tester *ActionsRunIfTester) mockRunTaskAndFail() *runnerv1.Task {
	task := tester.runner.fetchTask(tester.t)
	tester.runner.execTask(tester.t, task, &mockTaskOutcome{
		result: runnerv1.Result_RESULT_FAILURE,
	})
	return task
}

func TestActionsJobReusableWorkflowWithMatrix(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		t.Skip()
	}
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		// The entrypoint to this workflow will have a starting job (entry-pre-req), a reusable workflow with a matrix
		// strategy, and an ending job (entry-follow-up). Within the reusable workflow, two jobs (inner-job-1,
		// inner-job-2) will be run in two independent sequences.

		entry := `name: job-with-needs
on:
  push:
jobs:
  entry-pre-req:
    runs-on: docker
    steps: []
  entry:
    needs: entry-pre-req
    strategy:
      matrix:
        dimension: [value1, value2]
    uses: ./.forgejo/workflows/reusable.yml
    with:
      test-dimension: ${{ matrix.dimension }}
  entry-follow-up:
    needs: entry
    runs-on: docker
    steps: []
`

		reusable := `
on:
  workflow_call:
    inputs:
      test-dimension:
    outputs:
      job1-output:
        value: ${{ jobs.inner-job-1.outputs.job-output }}
      job2-output:
        value: ${{ jobs.inner-job-2.outputs.job-output }}
jobs:
  inner-job-1:
    runs-on: docker
    steps: []
  inner-job-2:
    needs: inner-job-1
    runs-on: docker
    steps: []
`

		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Files: forgery.MapFS{
				".forgejo/workflows/entry.yml":    forgery.MapFile(entry),
				".forgejo/workflows/reusable.yml": forgery.MapFile(reusable),
			},
		})
		forgery.EnableRepoUnit(t, repo, unit_model.TypeActions, nil)

		runner := newMockRunner()
		runner.registerAsRepoRunner(t, user2.Name, repo.Name, "mock-runner", []string{"docker"})

		entryPreReqTask := runner.fetchTask(t)
		entryPreReqJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{TaskID: entryPreReqTask.GetId()})
		assert.Equal(t, actions_model.JobIdentifier("entry-pre-req"), entryPreReqJob.JobID)
		// inner-job-1 (x2) must not start until entry-pre-req is complete.
		require.Nil(t, runner.maybeFetchTask(t))
		runner.succeedAtTask(t, entryPreReqTask)

		// There are two inner-job-1's, and two inner-job-2's, created with different inputs from the entry job's
		// matrix.  Tested by fetching the two jobs and validating they have the same identifiers, but independent
		// namespaces:
		matrix1InnerJob1Task := runner.fetchTask(t)
		matrix1InnerJob1 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{TaskID: matrix1InnerJob1Task.GetId()})
		assert.Equal(t, actions_model.JobIdentifier("inner-job-1"), matrix1InnerJob1.JobID)
		matrix2InnerJob1Task := runner.fetchTask(t)
		matrix2InnerJob1 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{TaskID: matrix2InnerJob1Task.GetId()})
		assert.Equal(t, actions_model.JobIdentifier("inner-job-1"), matrix2InnerJob1.JobID)
		assert.NotEqual(t, matrix1InnerJob1.JobNamespace, matrix2InnerJob1.JobNamespace)
		// Decode the workflow tasks, validating that each has the corect and distinct input:
		var matrix1InnerJob1Workflow map[string]any
		var matrix2InnerJob1Workflow map[string]any
		require.NoError(t, yaml.Unmarshal(matrix1InnerJob1Task.WorkflowPayload, &matrix1InnerJob1Workflow))
		require.NoError(t, yaml.Unmarshal(matrix2InnerJob1Task.WorkflowPayload, &matrix2InnerJob1Workflow))
		assert.Equal(t, "value1", matrix1InnerJob1Workflow["on"].(map[string]any)["workflow_call"].(map[string]any)["inputs"].(map[string]any)["test-dimension"].(map[string]any)["default"])
		assert.Equal(t, "value2", matrix2InnerJob1Workflow["on"].(map[string]any)["workflow_call"].(map[string]any)["inputs"].(map[string]any)["test-dimension"].(map[string]any)["default"])

		// These form independent chains of jobs; when inner-job-1{value1} is complete, inner-job-2{value1} can be
		// started, but neither has an impact on the jobs from the value2 matrix.  Tested by finishing
		// inner-job-1{value1}, ensuring inner-job-2{value1} can be started, and no other job can be started.
		runner.execTask(t, matrix1InnerJob1Task, &mockTaskOutcome{
			outputs: map[string]string{"job-output": "output from matrix1InnerJob1Task"},
			result:  runnerv1.Result_RESULT_SUCCESS,
		})
		matrix1InnerJob2Task := runner.fetchTask(t)
		matrix1InnerJob2 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{TaskID: matrix1InnerJob2Task.GetId()})
		assert.Equal(t, actions_model.JobIdentifier("inner-job-2"), matrix1InnerJob2.JobID)
		assert.Equal(t, matrix1InnerJob1.JobNamespace, matrix1InnerJob2.JobNamespace)
		assert.Nil(t, runner.maybeFetchTask(t)) // no other job available yet

		// Outputs provided by inner-job-1 can be read from inner-job-2, but only from within the same independent chain
		// of jobs, and not across the matrix.  Tested by validating the inputs to inner-job-2 from both matrix jobs,
		// which requires finishing inner-job-1:
		matrix1InnerJob2Needs := matrix1InnerJob2Task.GetNeeds()
		require.Contains(t, matrix1InnerJob2Needs, "inner-job-1")
		assert.Equal(t, map[string]string{"job-output": "output from matrix1InnerJob1Task"}, matrix1InnerJob2Needs["inner-job-1"].Outputs)
		// Finish the other inner-job-1 with it's own unique output so we can confirm the outputs aren't mixed:
		runner.execTask(t, matrix2InnerJob1Task, &mockTaskOutcome{
			outputs: map[string]string{"job-output": "the other output from matrix2InnerJob1Task"},
			result:  runnerv1.Result_RESULT_SUCCESS,
		})
		matrix2InnerJob2Task := runner.fetchTask(t)
		matrix2InnerJob2 := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{TaskID: matrix2InnerJob2Task.GetId()})
		assert.Equal(t, actions_model.JobIdentifier("inner-job-2"), matrix2InnerJob2.JobID)
		assert.Equal(t, matrix2InnerJob1.JobNamespace, matrix2InnerJob2.JobNamespace)
		matrix2InnerJob2Needs := matrix2InnerJob2Task.GetNeeds()
		require.Contains(t, matrix2InnerJob2Needs, "inner-job-1")
		assert.Equal(t, map[string]string{"job-output": "the other output from matrix2InnerJob1Task"}, matrix2InnerJob2Needs["inner-job-1"].Outputs)

		// All of the matrix jobs from entry will need to be completed before entry-follow-up can start.  Tested by
		// confirming that no jobs are available yet, and then finishing the two outstanding inner-job-2 instances one
		// by one.
		assert.Nil(t, runner.maybeFetchTask(t))
		runner.succeedAtTask(t, matrix1InnerJob2Task)
		assert.Nil(t, runner.maybeFetchTask(t))
		runner.succeedAtTask(t, matrix2InnerJob2Task)
		entryFollowUpTask := runner.fetchTask(t)
		entryFollowUpJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{TaskID: entryFollowUpTask.GetId()})
		assert.Equal(t, actions_model.JobIdentifier("entry-follow-up"), entryFollowUpJob.JobID)
		assert.Empty(t, entryFollowUpJob.JobNamespace)

		// The entry will have outputs which come the inner jobs.  As with all matrix jobs, the outputs from each job
		// are combined... but a reusable workflow can't have optional outputs like a job can.  Therefore the resulting
		// outputs of the entry job, which can be read from the entry-follow-up job, are just the outputs of the last
		// job to complete.
		entryFollowUpNeeds := entryFollowUpTask.GetNeeds()
		require.Contains(t, entryFollowUpNeeds, "entry")
		assert.Equal(t, map[string]string{
			"job1-output": "the other output from matrix2InnerJob1Task",
			"job2-output": "",
		}, entryFollowUpNeeds["entry"].Outputs)
	})
}
