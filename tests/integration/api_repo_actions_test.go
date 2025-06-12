// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	actions_model "forgejo.org/models/actions"
	auth_model "forgejo.org/models/auth"
	repo_model "forgejo.org/models/repo"
	unit_model "forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	api "forgejo.org/modules/structs"
	files_service "forgejo.org/services/repository/files"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
)

func TestAPISearchActionJobs_RepoRunner(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	token := getUserToken(t, user2.LowerName, auth_model.AccessTokenScopeWriteRepository)
	job := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: 393})

	req := NewRequestf(
		t,
		"GET",
		"/api/v1/repos/%s/%s/actions/runners/jobs?labels=%s",
		repo.OwnerName, repo.Name,
		"ubuntu-latest",
	).AddTokenAuth(token)
	res := MakeRequest(t, req, http.StatusOK)

	var jobs []*api.ActionRunJob
	DecodeJSON(t, res, &jobs)

	assert.Len(t, jobs, 1)
	assert.Equal(t, job.ID, jobs[0].ID)
}

func TestAPIWorkflowDispatchReturnInfo(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, u *url.URL) {
		workflowName := "dispatch.yml"
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		token := getUserToken(t, user2.LowerName, auth_model.AccessTokenScopeWriteRepository)

		// create the repo
		repo, _, f := tests.CreateDeclarativeRepo(t, user2, "api-repo-workflow-dispatch",
			[]unit_model.Type{unit_model.TypeActions}, nil,
			[]*files_service.ChangeRepoFile{
				{
					Operation: "create",
					TreePath:  fmt.Sprintf(".forgejo/workflows/%s", workflowName),
					ContentReader: strings.NewReader(`name: WD
on: [workflow-dispatch]
jobs:
  t1:
    runs-on: docker
    steps:
      - run: echo "test 1"
  t2:
    runs-on: docker
    steps:
      - run: echo "test 2"
`,
					),
				},
			},
		)
		defer f()

		req := NewRequestWithJSON(
			t,
			http.MethodPost,
			fmt.Sprintf(
				"/api/v1/repos/%s/%s/actions/workflows/%s/dispatches",
				repo.OwnerName, repo.Name, workflowName,
			),
			&api.DispatchWorkflowOption{
				Ref:           repo.DefaultBranch,
				ReturnRunInfo: true,
			},
		)
		req.AddTokenAuth(token)

		res := MakeRequest(t, req, http.StatusCreated)
		run := new(api.DispatchWorkflowRun)
		DecodeJSON(t, res, run)

		assert.NotZero(t, run.ID)
		assert.NotZero(t, run.RunNumber)
		assert.Len(t, run.Jobs, 2)
	})
}

func TestAPIGetListActionRun(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	var (
		runIDs = []int64{892, 893, 894}
		dbRuns = make(map[int64]*actions_model.ActionRun, 3)
	)

	for _, id := range runIDs {
		dbRuns[id] = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: id})
	}

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: dbRuns[runIDs[0]].RepoID})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	token := getUserToken(t, user.LowerName, auth_model.AccessTokenScopeWriteRepository)

	testqueries := []struct {
		name        string
		query       string
		expectedIDs []int64
	}{
		{
			name:        "No query parameters",
			query:       "",
			expectedIDs: runIDs,
		},
		{
			name:        "Search for workflow_dispatch events",
			query:       "?event=workflow_dispatch",
			expectedIDs: []int64{894},
		},
		{
			name:        "Search for multiple events",
			query:       "?event=workflow_dispatch&event=push",
			expectedIDs: []int64{892, 894},
		},
		{
			name:        "Search for failed status",
			query:       "?status=failure",
			expectedIDs: []int64{893},
		},
		{
			name:        "Search for multiple statuses",
			query:       "?status=failure&status=running",
			expectedIDs: []int64{893, 894},
		},
		{
			name:        "Search for num_nr",
			query:       "?run_number=1",
			expectedIDs: []int64{892},
		},
		{
			name:        "Search for sha",
			query:       "?head_sha=97f29ee599c373c729132a5c46a046978311e0ee",
			expectedIDs: []int64{892, 894},
		},
	}

	for _, tt := range testqueries {
		t.Run(tt.name, func(t *testing.T) {
			req := NewRequest(t, http.MethodGet,
				fmt.Sprintf("/api/v1/repos/%s/%s/actions/runs%s",
					repo.OwnerName, repo.Name, tt.query,
				),
			)
			req.AddTokenAuth(token)

			res := MakeRequest(t, req, http.StatusOK)
			apiRuns := new(api.ListRepoActionRunResponse)
			DecodeJSON(t, res, apiRuns)

			assert.Equal(t, int64(len(tt.expectedIDs)), apiRuns.TotalCount)
			assert.Len(t, apiRuns.Entries, len(tt.expectedIDs))

			resultIDs := make([]int64, apiRuns.TotalCount)
			for i, run := range apiRuns.Entries {
				resultIDs[i] = run.ID
			}

			assert.ElementsMatch(t, tt.expectedIDs, resultIDs)
		})
	}
}

func TestAPIGetActionRun(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 63})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	token := getUserToken(t, user.LowerName, auth_model.AccessTokenScopeWriteRepository)

	testqueries := []struct {
		name           string
		runID          int64
		expectedStatus int
	}{
		{
			name:           "existing return ok",
			runID:          892,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "non existing run",
			runID:          9876543210, // I hope this run will not exists, else just change it to another.
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "existing run but wrong repo should not be found",
			runID:          891,
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range testqueries {
		t.Run(tt.name, func(t *testing.T) {
			req := NewRequest(t, http.MethodGet,
				fmt.Sprintf("/api/v1/repos/%s/%s/actions/runs/%d",
					repo.OwnerName, repo.Name, tt.runID,
				),
			)
			req.AddTokenAuth(token)

			res := MakeRequest(t, req, tt.expectedStatus)

			// Only interested in the data if 200 OK
			if tt.expectedStatus != http.StatusOK {
				return
			}

			dbRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: tt.runID})
			apiRun := new(api.RepoActionRun)
			DecodeJSON(t, res, apiRun)

			assert.Equal(t, dbRun.Index, apiRun.RunNumber)
			assert.Equal(t, dbRun.Status.String(), apiRun.Status)
			assert.Equal(t, dbRun.CommitSHA, apiRun.HeadSHA)
			assert.Equal(t, dbRun.TriggerUserID, apiRun.TriggeringActor.ID)
		})
	}
}
