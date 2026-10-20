// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	actions_model "forgejo.org/models/actions"
	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	git_model "forgejo.org/models/git"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	unit_model "forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	actions_module "forgejo.org/modules/actions"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	webhook_module "forgejo.org/modules/webhook"
	actions_service "forgejo.org/services/actions"
	issue_service "forgejo.org/services/issue"
	pull_service "forgejo.org/services/pull"
	release_service "forgejo.org/services/release"
	repo_service "forgejo.org/services/repository"
	files_service "forgejo.org/services/repository/files"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActionsPullRequestCommitStatus(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2}) // owner of the base repo
		session := loginUser(t, "user2")
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteIssue)

		// prepare the repository
		baseRepo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Name:  "repo-pull-request",
			Files: forgery.FilesInit{},
		})
		forgery.EnableRepoUnit(t, baseRepo, unit_model.TypeActions, nil)

		files := make([]*files_service.ChangeRepoFile, 0, 10)
		for _, onType := range []string{
			"opened",
			"synchronize",
			"labeled",
			"unlabeled",
			"assigned",
			"unassigned",
			"milestoned",
			"demilestoned",
			"closed",
			"reopened",
		} {
			files = append(files, &files_service.ChangeRepoFile{
				Operation: "create",
				TreePath:  fmt.Sprintf(".forgejo/workflows/%s.yml", onType),
				ContentReader: strings.NewReader(fmt.Sprintf(`name: %[1]s
on:
  pull_request:
    types:
      - %[1]s
jobs:
  %[1]s:
    runs-on: docker
    steps:
      - run: true
`, onType)),
			})
		}

		_, err := files_service.ChangeRepoFiles(git.DefaultContext, baseRepo, user2, &files_service.ChangeRepoFilesOptions{
			Files: files,
		})
		require.NoError(t, err)

		baseGitRepo, err := gitrepo.OpenRepository(db.DefaultContext, baseRepo)
		require.NoError(t, err)
		defer func() {
			baseGitRepo.Close()
		}()

		// prepare the repository labels
		labelStr := "/api/v1/repos/user2/repo-pull-request/labels"
		labelsCount := 2
		labels := make([]*api.Label, labelsCount)
		for i := range labelsCount {
			color := "abcdef"
			req := NewRequestWithJSON(t, "POST", labelStr, &api.CreateLabelOption{
				Name:  fmt.Sprintf("label%d", i),
				Color: color,
			}).AddTokenAuth(token)
			resp := MakeRequest(t, req, http.StatusCreated)
			labels[i] = new(api.Label)
			DecodeJSON(t, resp, &labels[i])
			assert.Equal(t, color, labels[i].Color)
		}

		// create the pull request
		testEditFileToNewBranch(t, session, "user2", "repo-pull-request", "main", "wip-something", "README.md", "Hello, world 1")
		testPullCreate(t, session, "user2", "repo-pull-request", true, "main", "wip-something", "Commit status PR")
		pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{BaseRepoID: baseRepo.ID})
		require.NoError(t, pr.LoadIssue(db.DefaultContext))

		// prepare the assignees
		issueURL := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%s", "user2", "repo-pull-request", fmt.Sprintf("%d", pr.Issue.Index))

		// prepare the labels
		labelURL := fmt.Sprintf("%s/labels", issueURL)

		// prepare the milestone
		milestoneStr := "/api/v1/repos/user2/repo-pull-request/milestones"
		req := NewRequestWithJSON(t, "POST", milestoneStr, &api.CreateMilestoneOption{
			Title: "mymilestone",
			State: "open",
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusCreated)
		milestone := new(api.Milestone)
		DecodeJSON(t, resp, &milestone)

		// check that one of the status associated with the commit sha matches both
		// context & state
		checkCommitStatus := func(sha, context string, state api.CommitStatusState) bool {
			commitStatuses, _, err := git_model.GetLatestCommitStatus(db.DefaultContext, pr.BaseRepoID, sha, db.ListOptionsAll)
			require.NoError(t, err)
			for _, commitStatus := range commitStatuses {
				if state == commitStatus.State && context == commitStatus.Context {
					return true
				}
			}
			return false
		}

		assertActionRun := func(t *testing.T, sha, onType string, action api.HookIssueAction, actionRun *actions_model.ActionRun) {
			assert.Equal(t, fmt.Sprintf("%s.yml", onType), actionRun.WorkflowID)
			assert.Equal(t, sha, actionRun.CommitSHA)
			assert.Equal(t, actions_module.GithubEventPullRequest, actionRun.TriggerEvent)
			event, err := actionRun.GetPullRequestEventPayload()
			require.NoError(t, err)
			assert.Equal(t, action, event.Action)
		}

		type assertType func(t *testing.T, sha, onType string, action api.HookIssueAction, actionRuns []*actions_model.ActionRun)
		assertActionRuns := func(t *testing.T, sha, onType string, action api.HookIssueAction, actionRuns []*actions_model.ActionRun) {
			require.Len(t, actionRuns, 1)
			assertActionRun(t, sha, onType, action, actionRuns[0])
		}

		for _, testCase := range []struct {
			onType         string
			jobID          string
			doSomething    func()
			actionRunCount int
			action         api.HookIssueAction
			assert         assertType
		}{
			{
				onType:         "opened",
				doSomething:    func() {},
				actionRunCount: 1,
				action:         api.HookIssueOpened,
				assert:         assertActionRuns,
			},
			{
				onType: "synchronize",
				doSomething: func() {
					testEditFile(t, session, "user2", "repo-pull-request", "wip-something", "README.md", "Hello, world 2")
				},
				actionRunCount: 1,
				action:         api.HookIssueSynchronized,
				assert:         assertActionRuns,
			},
			{
				onType: "labeled",
				doSomething: func() {
					req := NewRequestWithJSON(t, "POST", labelURL, &api.IssueLabelsOption{
						Labels: []any{labels[0].ID, labels[1].ID},
					}).AddTokenAuth(token)
					MakeRequest(t, req, http.StatusOK)
				},
				actionRunCount: 2,
				action:         api.HookIssueLabelUpdated,
				assert: func(t *testing.T, sha, onType string, action api.HookIssueAction, actionRuns []*actions_model.ActionRun) {
					assertActionRun(t, sha, onType, api.HookIssueLabelUpdated, actionRuns[0])
					assertActionRun(t, sha, onType, api.HookIssueLabelUpdated, actionRuns[1])
				},
			},
			{
				onType: "unlabeled",
				doSomething: func() {
					req := NewRequestWithJSON(t, "PUT", labelURL, &api.IssueLabelsOption{
						Labels: []any{labels[0].ID},
					}).AddTokenAuth(token)
					MakeRequest(t, req, http.StatusOK)
				},
				actionRunCount: 3,
				action:         api.HookIssueLabelCleared,
				assert: func(t *testing.T, sha, onType string, action api.HookIssueAction, actionRuns []*actions_model.ActionRun) {
					foundPayloadWithLabels := false
					knownLabels := []string{"label0", "label1"}
					for _, actionRun := range actionRuns {
						assert.Equal(t, sha, actionRun.CommitSHA)
						assert.Equal(t, actions_module.GithubEventPullRequest, actionRun.TriggerEvent)
						event, err := actionRun.GetPullRequestEventPayload()
						require.NoError(t, err)
						switch event.Action {
						case api.HookIssueLabelUpdated:
							assert.Equal(t, "labeled.yml", actionRun.WorkflowID)
							assert.Equal(t, "label0", event.Label.Name)
							require.Len(t, event.PullRequest.Labels, 1)
							assert.Contains(t, "label0", event.PullRequest.Labels[0].Name)
						case api.HookIssueLabelCleared:
							assert.Equal(t, "unlabeled.yml", actionRun.WorkflowID)
							assert.Contains(t, knownLabels, event.Label.Name)
							if len(event.PullRequest.Labels) > 0 {
								foundPayloadWithLabels = true
								assert.Contains(t, knownLabels, event.PullRequest.Labels[0].Name)
							}
						default:
							require.Fail(t, fmt.Sprintf("unexpected action '%s'", event.Action))
						}
					}
					assert.True(t, foundPayloadWithLabels, "expected at least one clear label payload with non empty labels")
				},
			},
			{
				onType: "assigned",
				doSomething: func() {
					req := NewRequestWithJSON(t, "PATCH", issueURL, &api.EditIssueOption{
						Assignees: []string{"user2"},
					}).AddTokenAuth(token)
					MakeRequest(t, req, http.StatusCreated)
				},
				actionRunCount: 1,
				action:         api.HookIssueAssigned,
				assert:         assertActionRuns,
			},
			{
				onType: "unassigned",
				doSomething: func() {
					req := NewRequestWithJSON(t, "PATCH", issueURL, &api.EditIssueOption{
						Assignees: []string{},
					}).AddTokenAuth(token)
					MakeRequest(t, req, http.StatusCreated)
				},
				actionRunCount: 1,
				action:         api.HookIssueUnassigned,
				assert:         assertActionRuns,
			},
			{
				onType: "milestoned",
				doSomething: func() {
					req := NewRequestWithJSON(t, "PATCH", issueURL, &api.EditIssueOption{
						Milestone: &milestone.ID,
					}).AddTokenAuth(token)
					MakeRequest(t, req, http.StatusCreated)
				},
				actionRunCount: 1,
				action:         api.HookIssueMilestoned,
				assert:         assertActionRuns,
			},
			{
				onType: "demilestoned",
				doSomething: func() {
					var zero int64
					req := NewRequestWithJSON(t, "PATCH", issueURL, &api.EditIssueOption{
						Milestone: &zero,
					}).AddTokenAuth(token)
					MakeRequest(t, req, http.StatusCreated)
				},
				actionRunCount: 1,
				action:         api.HookIssueDemilestoned,
				assert:         assertActionRuns,
			},
			{
				onType: "closed",
				doSomething: func() {
					sha, err := baseGitRepo.GetRefCommitID(pr.GetGitRefName())
					require.NoError(t, err)
					err = issue_service.ChangeStatus(db.DefaultContext, pr.Issue, user2, &issues_model.PRNotificationInfo{MergedCommitID: sha}, true)
					require.NoError(t, err)
				},
				actionRunCount: 1,
				action:         api.HookIssueClosed,
				assert:         assertActionRuns,
			},
			{
				onType: "reopened",
				doSomething: func() {
					sha, err := baseGitRepo.GetRefCommitID(pr.GetGitRefName())
					require.NoError(t, err)
					err = issue_service.ChangeStatus(db.DefaultContext, pr.Issue, user2, &issues_model.PRNotificationInfo{MergedCommitID: sha}, false)
					require.NoError(t, err)
				},
				actionRunCount: 1,
				action:         api.HookIssueReOpened,
				assert:         assertActionRuns,
			},
		} {
			t.Run(testCase.onType, func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				defer func() {
					// cleanup leftovers, start from scratch
					unittest.AssertSuccessfulDelete(t, &actions_model.ActionRun{RepoID: baseRepo.ID})
					unittest.AssertSuccessfulDelete(t, &actions_model.ActionRunJob{RepoID: baseRepo.ID})
				}()

				// trigger the onType event
				testCase.doSomething()
				count := testCase.actionRunCount
				context := fmt.Sprintf("%[1]s / %[1]s (pull_request)", testCase.onType)

				var actionRuns []*actions_model.ActionRun

				// wait for ActionRun(s) to be created
				require.Eventually(t, func() bool {
					actionRuns = make([]*actions_model.ActionRun, 0)
					require.NoError(t, db.GetEngine(db.DefaultContext).Where("repo_id=?", baseRepo.ID).Find(&actionRuns))
					return len(actionRuns) == count
				}, 30*time.Second, 1*time.Second)

				// verify the expected  ActionRuns were created
				sha, err := baseGitRepo.GetRefCommitID(pr.GetGitRefName())
				require.NoError(t, err)
				// verify the commit status changes to CommitStatusSuccess when the job changes to StatusSuccess
				require.Eventually(t, func() bool {
					return checkCommitStatus(sha, context, api.CommitStatusPending)
				}, 30*time.Second, 1*time.Second)
				for _, actionRun := range actionRuns {
					// verify the expected  ActionRunJob was created and is StatusWaiting
					job := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, CommitSHA: sha})
					assert.Equal(t, actions_model.StatusWaiting, job.Status)

					// change the state of the job to success
					job.Status = actions_model.StatusSuccess
					actions_service.CreateCommitStatus(db.DefaultContext, job)
				}
				// verify the commit status changed to CommitStatusSuccess because the job(s) changed to StatusSuccess
				require.Eventually(t, func() bool {
					return checkCommitStatus(sha, context, api.CommitStatusSuccess)
				}, 30*time.Second, 1*time.Second)

				testCase.assert(t, sha, testCase.onType, testCase.action, actionRuns)
			})
		}
	})
}

func TestActionsPullRequestWithInvalidWorkflow(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2}) // owner of the base repo
		session := loginUser(t, "user2")

		// prepare the repository
		baseRepo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Name:  "repo-pull-request",
			Files: forgery.FilesInit{},
		})
		forgery.EnableRepoUnit(t, baseRepo, unit_model.TypeActions, nil)

		_, err := files_service.ChangeRepoFiles(git.DefaultContext, baseRepo, user2, &files_service.ChangeRepoFilesOptions{
			Files: []*files_service.ChangeRepoFile{
				{
					Operation: "create",
					TreePath:  ".forgejo/workflows/broken.yml",
					ContentReader: strings.NewReader(`name: broken
on:
pull_request:
types:
	- opened
jobs:
test:
runs-on: docker
	- run: true
`),
				},
			},
		})
		require.NoError(t, err)

		baseGitRepo, err := gitrepo.OpenRepository(t.Context(), baseRepo)
		require.NoError(t, err)
		defer func() {
			baseGitRepo.Close()
		}()

		// create the pull request
		testEditFileToNewBranch(t, session, "user2", "repo-pull-request", "main", "wip-something", "README.md", "Hello, world 1")
		testPullCreate(t, session, "user2", "repo-pull-request", true, "main", "wip-something", "Commit status PR")
		pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{BaseRepoID: baseRepo.ID})
		require.NoError(t, pr.LoadIssue(t.Context()))

		// check that one of the status associated with the commit sha matches both
		// context & state
		checkCommitStatus := func(sha, context string, state api.CommitStatusState) bool {
			commitStatuses, _, err := git_model.GetLatestCommitStatus(t.Context(), pr.BaseRepoID, sha, db.ListOptionsAll)
			require.NoError(t, err)
			for _, commitStatus := range commitStatuses {
				if state == commitStatus.State && context == commitStatus.Context {
					return true
				}
			}
			return false
		}

		var actionRuns []*actions_model.ActionRun

		// wait for ActionRun(s) to be created
		require.Eventually(t, func() bool {
			actionRuns = make([]*actions_model.ActionRun, 0)
			require.NoError(t, db.GetEngine(t.Context()).Where("event=? AND status=? AND repo_id=?", "pull_request", actions_model.StatusFailure, baseRepo.ID).Find(&actionRuns))
			return len(actionRuns) == 1
		}, 30*time.Second, 1*time.Second)

		// verify the expected  ActionRuns were created
		sha, err := baseGitRepo.GetRefCommitID(pr.GetGitRefName())
		require.NoError(t, err)

		// verify the commit status changes to CommitStatusFailure
		require.Eventually(t, func() bool {
			return checkCommitStatus(sha, "broken.yml / Update README.md (pull_request)", api.CommitStatusFailure)
		}, 30*time.Second, 1*time.Second)

		require.Len(t, actionRuns, 1)
		actionRun := actionRuns[0]
		// verify the expected  ActionRunJob was created and is StatusFailure
		job := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, CommitSHA: sha})
		assert.Equal(t, actions_model.StatusFailure, job.Status)
		assert.Equal(t, "broken.yml", actionRun.WorkflowID)
		assert.Equal(t, sha, actionRun.CommitSHA)
		assert.Equal(t, actions_module.GithubEventPullRequest, actionRun.TriggerEvent)
		event, err := actionRun.GetPullRequestEventPayload()
		require.NoError(t, err)
		assert.Equal(t, api.HookIssueOpened, event.Action)
	})
}

func TestActionsPullRequestTargetEvent(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2}) // owner of the base repo
		org3 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3})  // owner of the forked repo

		// create the base repo
		baseRepo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{Name: "repo-pull-request-target"})
		forgery.EnableRepoUnit(t, baseRepo, unit_model.TypeActions, nil)

		// create the forked repo
		forkedRepo, err := repo_service.ForkRepositoryAndUpdates(git.DefaultContext, user2, org3, repo_service.ForkRepoOptions{
			BaseRepo:    baseRepo,
			Name:        "forked-repo-pull-request-target",
			Description: "test pull-request-target event",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, forkedRepo)

		// add workflow file to the base repo
		addWorkflowToBaseResp, err := files_service.ChangeRepoFiles(git.DefaultContext, baseRepo, user2, &files_service.ChangeRepoFilesOptions{
			Files: []*files_service.ChangeRepoFile{
				{
					Operation: "create",
					TreePath:  ".gitea/workflows/pr.yml",
					ContentReader: strings.NewReader(`
name: test
on:
  pull_request_target:
    paths:
      - 'file_*.txt'
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: echo helloworld
`),
				},
			},
			Message:   "add workflow",
			OldBranch: "main",
			NewBranch: "main",
			Author: &files_service.IdentityOptions{
				Name:  user2.Name,
				Email: user2.Email,
			},
			Committer: &files_service.IdentityOptions{
				Name:  user2.Name,
				Email: user2.Email,
			},
			Dates: &files_service.CommitDateOptions{
				Author:    time.Now(),
				Committer: time.Now(),
			},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, addWorkflowToBaseResp)

		// add a new file to the forked repo
		addFileToForkedResp, err := files_service.ChangeRepoFiles(git.DefaultContext, forkedRepo, org3, &files_service.ChangeRepoFilesOptions{
			Files: []*files_service.ChangeRepoFile{
				{
					Operation:     "create",
					TreePath:      "file_1.txt",
					ContentReader: strings.NewReader("file1"),
				},
			},
			Message:   "add file1",
			OldBranch: "main",
			NewBranch: "fork-branch-1",
			Author: &files_service.IdentityOptions{
				Name:  org3.Name,
				Email: org3.Email,
			},
			Committer: &files_service.IdentityOptions{
				Name:  org3.Name,
				Email: org3.Email,
			},
			Dates: &files_service.CommitDateOptions{
				Author:    time.Now(),
				Committer: time.Now(),
			},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, addFileToForkedResp)

		// create Pull
		pullIssue := &issues_model.Issue{
			RepoID:   baseRepo.ID,
			Title:    "Test pull-request-target-event",
			PosterID: org3.ID,
			Poster:   org3,
			IsPull:   true,
		}
		pullRequest := &issues_model.PullRequest{
			HeadRepoID: forkedRepo.ID,
			BaseRepoID: baseRepo.ID,
			HeadBranch: "fork-branch-1",
			BaseBranch: "main",
			HeadRepo:   forkedRepo,
			BaseRepo:   baseRepo,
			Type:       issues_model.PullRequestGitea,
		}
		err = pull_service.NewPullRequest(git.DefaultContext, baseRepo, pullIssue, nil, nil, pullRequest, nil)
		require.NoError(t, err)
		// if a PR "synchronized" event races the "opened" event by having the same SHA, it must be skipped. See https://codeberg.org/forgejo/forgejo/issues/2009.
		assert.True(t, actions_service.SkipPullRequestEvent(git.DefaultContext, webhook_module.HookEventPullRequestSync, baseRepo.ID, addFileToForkedResp.Commit.SHA))

		// load and compare ActionRun
		assert.Equal(t, 1, unittest.GetCount(t, &actions_model.ActionRun{RepoID: baseRepo.ID}))
		actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID})
		assert.Equal(t, addFileToForkedResp.Commit.SHA, actionRun.CommitSHA)
		assert.Equal(t, actions_module.GithubEventPullRequestTarget, actionRun.TriggerEvent)

		// add another file whose name cannot match the specified path
		addFileToForkedResp, err = files_service.ChangeRepoFiles(git.DefaultContext, forkedRepo, org3, &files_service.ChangeRepoFilesOptions{
			Files: []*files_service.ChangeRepoFile{
				{
					Operation:     "create",
					TreePath:      "foo.txt",
					ContentReader: strings.NewReader("foo"),
				},
			},
			Message:   "add foo.txt",
			OldBranch: "main",
			NewBranch: "fork-branch-2",
			Author: &files_service.IdentityOptions{
				Name:  org3.Name,
				Email: org3.Email,
			},
			Committer: &files_service.IdentityOptions{
				Name:  org3.Name,
				Email: org3.Email,
			},
			Dates: &files_service.CommitDateOptions{
				Author:    time.Now(),
				Committer: time.Now(),
			},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, addFileToForkedResp)

		// create Pull
		pullIssue = &issues_model.Issue{
			RepoID:   baseRepo.ID,
			Title:    "A mismatched path cannot trigger pull-request-target-event",
			PosterID: org3.ID,
			Poster:   org3,
			IsPull:   true,
		}
		pullRequest = &issues_model.PullRequest{
			HeadRepoID: forkedRepo.ID,
			BaseRepoID: baseRepo.ID,
			HeadBranch: "fork-branch-2",
			BaseBranch: "main",
			HeadRepo:   forkedRepo,
			BaseRepo:   baseRepo,
			Type:       issues_model.PullRequestGitea,
		}
		err = pull_service.NewPullRequest(git.DefaultContext, baseRepo, pullIssue, nil, nil, pullRequest, nil)
		require.NoError(t, err)

		// the new pull request cannot trigger actions, so there is still only 1 record
		assert.Equal(t, 1, unittest.GetCount(t, &actions_model.ActionRun{RepoID: baseRepo.ID}))
	})
}

func TestActionsPullRequestTargetEventLocalReusable(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		t.Run("local reusable workflow is resolved from base when job is first queued", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2}) // owner of the base repo
			org3 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3})  // owner of the forked repo

			// create the base repo
			baseRepo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{Name: "repo-pull-request-target"})
			forgery.EnableRepoUnit(t, baseRepo, unit_model.TypeActions, nil)

			// create the forked repo
			forkedRepo, err := repo_service.ForkRepositoryAndUpdates(git.DefaultContext, user2, org3, repo_service.ForkRepoOptions{
				BaseRepo:    baseRepo,
				Name:        "forked-repo-pull-request-target",
				Description: "test pull-request-target event",
			})
			require.NoError(t, err)
			assert.NotEmpty(t, forkedRepo)

			// add workflow file to the base repo
			addWorkflowToBaseResp, err := files_service.ChangeRepoFiles(git.DefaultContext, baseRepo, user2, &files_service.ChangeRepoFilesOptions{
				Files: []*files_service.ChangeRepoFile{
					{
						Operation: "create",
						TreePath:  ".forgejo/workflows/pr.yml",
						ContentReader: strings.NewReader(`
name: test
on:
  pull_request_target:
jobs:
  outer-job:
    uses: ./.forgejo/workflows/local-reusable.yml
`),
					},
					{
						Operation: "create",
						TreePath:  ".forgejo/workflows/local-reusable.yml",
						ContentReader: strings.NewReader(`
name: reusable
on:
  workflow_call:
jobs:
  job-from-base-repo:
    runs-on: docker
    steps:
      - run: echo 0
`),
					},
				},
				Message:   "add workflow",
				OldBranch: "main",
				NewBranch: "main",
				Author: &files_service.IdentityOptions{
					Name:  user2.Name,
					Email: user2.Email,
				},
				Committer: &files_service.IdentityOptions{
					Name:  user2.Name,
					Email: user2.Email,
				},
				Dates: &files_service.CommitDateOptions{
					Author:    time.Now(),
					Committer: time.Now(),
				},
			})
			require.NoError(t, err)
			assert.NotEmpty(t, addWorkflowToBaseResp)

			// add a new file to the forked repo, as a subtle replacement for `local-reusable.yml`
			addFileToForkedResp, err := files_service.ChangeRepoFiles(git.DefaultContext, forkedRepo, org3, &files_service.ChangeRepoFilesOptions{
				Files: []*files_service.ChangeRepoFile{
					{
						Operation: "create",
						TreePath:  ".forgejo/workflows/local-reusable.yml",
						ContentReader: strings.NewReader(`
name: reusable
on:
  workflow_call:
jobs:
  job-from-head-repo:
    runs-on: docker
    steps:
      - run: echo 0
`),
					},
				},
				Message:   "add file1",
				OldBranch: "main",
				NewBranch: "fork-branch-1",
				Author: &files_service.IdentityOptions{
					Name:  org3.Name,
					Email: org3.Email,
				},
				Committer: &files_service.IdentityOptions{
					Name:  org3.Name,
					Email: org3.Email,
				},
				Dates: &files_service.CommitDateOptions{
					Author:    time.Now(),
					Committer: time.Now(),
				},
			})
			require.NoError(t, err)
			assert.NotEmpty(t, addFileToForkedResp)

			// create Pull
			pullIssue := &issues_model.Issue{
				RepoID:   baseRepo.ID,
				Title:    "Test pull-request-target-event",
				PosterID: org3.ID,
				Poster:   org3,
				IsPull:   true,
			}
			pullRequest := &issues_model.PullRequest{
				HeadRepoID: forkedRepo.ID,
				BaseRepoID: baseRepo.ID,
				HeadBranch: "fork-branch-1",
				BaseBranch: "main",
				HeadRepo:   forkedRepo,
				BaseRepo:   baseRepo,
				Type:       issues_model.PullRequestGitea,
			}
			err = pull_service.NewPullRequest(git.DefaultContext, baseRepo, pullIssue, nil, nil, pullRequest, nil)
			require.NoError(t, err)

			// load and compare ActionRun
			assert.Equal(t, 1, unittest.GetCount(t, &actions_model.ActionRun{RepoID: baseRepo.ID}))
			actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID})
			assert.Equal(t, addFileToForkedResp.Commit.SHA, actionRun.CommitSHA)
			assert.Equal(t, optional.Some(addWorkflowToBaseResp.Commit.SHA), actionRun.WorkflowSourceCommit)
			assert.Equal(t, actions_module.GithubEventPullRequestTarget, actionRun.TriggerEvent)

			unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, Name: "outer-job"})
			unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, Name: "job-from-base-repo"})
		})

		t.Run("local reusable workflow is resolved from base when job is later queued by dynamic expansion", func(t *testing.T) {
			if !setting.Database.Type.IsSQLite3() {
				// mockRunner only supported with sqlite
				t.Skip()
			}
			defer tests.PrintCurrentTest(t)()
			user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2}) // owner of the base repo
			org3 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3})  // owner of the forked repo

			// create the base repo
			baseRepo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{Name: "repo-pull-request-target-2"})
			forgery.EnableRepoUnit(t, baseRepo, unit_model.TypeActions, nil)

			// create the forked repo
			forkedRepo, err := repo_service.ForkRepositoryAndUpdates(git.DefaultContext, user2, org3, repo_service.ForkRepoOptions{
				BaseRepo:    baseRepo,
				Name:        "forked-repo-pull-request-target-2",
				Description: "test pull-request-target event",
			})
			require.NoError(t, err)
			assert.NotEmpty(t, forkedRepo)

			// add workflow file to the base repo
			addWorkflowToBaseResp, err := files_service.ChangeRepoFiles(git.DefaultContext, baseRepo, user2, &files_service.ChangeRepoFilesOptions{
				Files: []*files_service.ChangeRepoFile{
					{
						Operation: "create",
						TreePath:  ".forgejo/workflows/pr.yml",
						ContentReader: strings.NewReader(`
name: test
on:
  pull_request_target:
jobs:
  outer-job-prereq:
    runs-on: docker
    steps:
      - run: echo "Job contents go here." # we'll mock this having an output
  outer-job:
    needs: [outer-job-prereq]
    strategy:
      matrix:
        dim1: ${{ needs.outer-job-prereq.outputs.fake-output }}
    uses: ./.forgejo/workflows/local-reusable.yml
`),
					},
					{
						Operation: "create",
						TreePath:  ".forgejo/workflows/local-reusable.yml",
						ContentReader: strings.NewReader(`
name: reusable
on:
  workflow_call:
jobs:
  job-from-base-repo:
    runs-on: docker
    steps:
      - run: echo 0
`),
					},
				},
				Message:   "add workflow",
				OldBranch: "main",
				NewBranch: "main",
				Author: &files_service.IdentityOptions{
					Name:  user2.Name,
					Email: user2.Email,
				},
				Committer: &files_service.IdentityOptions{
					Name:  user2.Name,
					Email: user2.Email,
				},
				Dates: &files_service.CommitDateOptions{
					Author:    time.Now(),
					Committer: time.Now(),
				},
			})
			require.NoError(t, err)
			assert.NotEmpty(t, addWorkflowToBaseResp)

			// add a new file to the forked repo, as a subtle replacement for `local-reusable.yml`
			addFileToForkedResp, err := files_service.ChangeRepoFiles(git.DefaultContext, forkedRepo, org3, &files_service.ChangeRepoFilesOptions{
				Files: []*files_service.ChangeRepoFile{
					{
						Operation: "create",
						TreePath:  ".forgejo/workflows/local-reusable.yml",
						ContentReader: strings.NewReader(`
name: reusable
on:
  workflow_call:
jobs:
  job-from-head-repo:
    runs-on: docker
    steps:
      - run: echo 0
`),
					},
				},
				Message:   "add file1",
				OldBranch: "main",
				NewBranch: "fork-branch-1",
				Author: &files_service.IdentityOptions{
					Name:  org3.Name,
					Email: org3.Email,
				},
				Committer: &files_service.IdentityOptions{
					Name:  org3.Name,
					Email: org3.Email,
				},
				Dates: &files_service.CommitDateOptions{
					Author:    time.Now(),
					Committer: time.Now(),
				},
			})
			require.NoError(t, err)
			assert.NotEmpty(t, addFileToForkedResp)

			// create Pull
			pullIssue := &issues_model.Issue{
				RepoID:   baseRepo.ID,
				Title:    "Test pull-request-target-event",
				PosterID: org3.ID,
				Poster:   org3,
				IsPull:   true,
			}
			pullRequest := &issues_model.PullRequest{
				HeadRepoID: forkedRepo.ID,
				BaseRepoID: baseRepo.ID,
				HeadBranch: "fork-branch-1",
				BaseBranch: "main",
				HeadRepo:   forkedRepo,
				BaseRepo:   baseRepo,
				Type:       issues_model.PullRequestGitea,
			}
			err = pull_service.NewPullRequest(git.DefaultContext, baseRepo, pullIssue, nil, nil, pullRequest, nil)
			require.NoError(t, err)

			runner := newMockRunner()
			runner.registerAsRepoRunner(t, user2.Name, baseRepo.Name, "mock-runner", []string{"docker"})
			task := runner.fetchTask(t)
			runner.execTask(t, task, &mockTaskOutcome{
				result:  runnerv1.Result_RESULT_SUCCESS,
				outputs: map[string]string{"fake-output": "matrix value"},
			})

			assert.Equal(t, 1, unittest.GetCount(t, &actions_model.ActionRun{RepoID: baseRepo.ID}))
			actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID})
			unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, Name: "outer-job"})
			// if job-from-base-repo is here, then we know the expanded job came from the base repo -- it would be a
			// security problem if job-from-head-repo was created instead.
			unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, Name: "job-from-base-repo"})
		})
	})
}

func TestActionsSkipCI(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		session := loginUser(t, "user2")
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

		repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Name: "skip-ci",
			Files: forgery.MapFS{
				".gitea/workflows/pr.yml": forgery.MapFile("name: test\non:\n  push:\n    branches: [main]\n  pull_request:\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo helloworld\n"),
			},
		})
		forgery.EnableRepoUnit(t, repo, unit_model.TypeActions, nil)

		// a run has been created
		assert.Equal(t, 1, unittest.GetCount(t, &actions_model.ActionRun{RepoID: repo.ID}))

		// add a file with a configured skip-ci string in commit message
		addFileResp, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, user2, &files_service.ChangeRepoFilesOptions{
			Files: []*files_service.ChangeRepoFile{
				{
					Operation:     "create",
					TreePath:      "bar.txt",
					ContentReader: strings.NewReader("bar"),
				},
			},
			Message:   fmt.Sprintf("%s add bar", setting.Actions.SkipWorkflowStrings[0]),
			OldBranch: "main",
			NewBranch: "main",
			Author: &files_service.IdentityOptions{
				Name:  user2.Name,
				Email: user2.Email,
			},
			Committer: &files_service.IdentityOptions{
				Name:  user2.Name,
				Email: user2.Email,
			},
			Dates: &files_service.CommitDateOptions{
				Author:    time.Now(),
				Committer: time.Now(),
			},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, addFileResp)

		// the commit message contains a configured skip-ci string, so there is still only 1 record
		assert.Equal(t, 1, unittest.GetCount(t, &actions_model.ActionRun{RepoID: repo.ID}))

		// add file to new branch
		addFileToBranchResp, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, user2, &files_service.ChangeRepoFilesOptions{
			Files: []*files_service.ChangeRepoFile{
				{
					Operation:     "create",
					TreePath:      "test-skip-ci",
					ContentReader: strings.NewReader("test-skip-ci"),
				},
			},
			Message:   "add test file",
			OldBranch: "main",
			NewBranch: "test-skip-ci",
			Author: &files_service.IdentityOptions{
				Name:  user2.Name,
				Email: user2.Email,
			},
			Committer: &files_service.IdentityOptions{
				Name:  user2.Name,
				Email: user2.Email,
			},
			Dates: &files_service.CommitDateOptions{
				Author:    time.Now(),
				Committer: time.Now(),
			},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, addFileToBranchResp)

		resp := testPullCreate(t, session, "user2", "skip-ci", true, "main", "test-skip-ci", "[skip ci] test-skip-ci")

		// check the redirected URL
		url := test.RedirectURL(resp)
		assert.Regexp(t, "^/user2/skip-ci/pulls/[0-9]*$", url)

		// the pr title contains a configured skip-ci string, so there is still only 1 record
		assert.Equal(t, 1, unittest.GetCount(t, &actions_model.ActionRun{RepoID: repo.ID}))
	})
}

func TestActionsCreateDeleteRefEvent(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

		// create the repo
		repo, err := repo_service.CreateRepository(db.DefaultContext, user2, user2, repo_service.CreateRepoOptions{
			Name:          "create-delete-ref-event",
			Description:   "test create delete ref ci event",
			AutoInit:      true,
			Gitignores:    "Go",
			License:       "MIT",
			Readme:        "Default",
			DefaultBranch: "main",
			IsPrivate:     false,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, repo)

		// enable actions
		err = repo_service.UpdateRepositoryUnits(db.DefaultContext, repo, []repo_model.RepoUnit{{
			RepoID: repo.ID,
			Type:   unit_model.TypeActions,
		}}, nil)
		require.NoError(t, err)

		// add workflow file to the repo
		addWorkflowToBaseResp, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, user2, &files_service.ChangeRepoFilesOptions{
			Files: []*files_service.ChangeRepoFile{
				{
					Operation:     "create",
					TreePath:      ".gitea/workflows/createdelete.yml",
					ContentReader: strings.NewReader("name: test\non:\n  [create,delete]\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo helloworld\n"),
				},
			},
			Message:   "add workflow",
			OldBranch: "main",
			NewBranch: "main",
			Author: &files_service.IdentityOptions{
				Name:  user2.Name,
				Email: user2.Email,
			},
			Committer: &files_service.IdentityOptions{
				Name:  user2.Name,
				Email: user2.Email,
			},
			Dates: &files_service.CommitDateOptions{
				Author:    time.Now(),
				Committer: time.Now(),
			},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, addWorkflowToBaseResp)

		// Get the commit ID of the default branch
		gitRepo, err := gitrepo.OpenRepository(git.DefaultContext, repo)
		require.NoError(t, err)
		defer gitRepo.Close()
		branch, err := git_model.GetBranch(db.DefaultContext, repo.ID, repo.DefaultBranch)
		require.NoError(t, err)

		// create a branch
		err = repo_service.CreateNewBranchFromCommit(db.DefaultContext, user2, repo, gitRepo, branch.CommitID, "test-create-branch")
		require.NoError(t, err)
		run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{
			Title:      "add workflow",
			RepoID:     repo.ID,
			Event:      "create",
			Ref:        "refs/heads/test-create-branch",
			WorkflowID: "createdelete.yml",
			CommitSHA:  branch.CommitID,
		})
		assert.NotNil(t, run)

		// create a tag
		err = release_service.CreateNewTag(db.DefaultContext, user2, repo, branch.CommitID, "test-create-tag", "test create tag event")
		require.NoError(t, err)
		run = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{
			Title:      "add workflow",
			RepoID:     repo.ID,
			Event:      "create",
			Ref:        "refs/tags/test-create-tag",
			WorkflowID: "createdelete.yml",
			CommitSHA:  branch.CommitID,
		})
		assert.NotNil(t, run)

		// delete the branch
		err = repo_service.DeleteBranch(db.DefaultContext, user2, repo, gitRepo, "test-create-branch")
		require.NoError(t, err)
		run = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{
			Title:      "add workflow",
			RepoID:     repo.ID,
			Event:      "delete",
			Ref:        "refs/heads/main",
			WorkflowID: "createdelete.yml",
			CommitSHA:  branch.CommitID,
		})
		assert.NotNil(t, run)

		// delete the tag
		tag, err := repo_model.GetRelease(db.DefaultContext, repo.ID, "test-create-tag")
		require.NoError(t, err)
		err = release_service.DeleteReleaseByID(db.DefaultContext, repo, tag, user2, true)
		require.NoError(t, err)
		run = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{
			Title:      "add workflow",
			RepoID:     repo.ID,
			Event:      "delete",
			Ref:        "refs/heads/main",
			WorkflowID: "createdelete.yml",
			CommitSHA:  branch.CommitID,
		})
		assert.NotNil(t, run)
	})
}

func TestActionsWorkflowDispatch(t *testing.T) {
	testCases := []struct {
		name              string
		workflowID        string
		workflowDirectory string
	}{
		{
			name:              "GitHub",
			workflowID:        "dispatch.yml",
			workflowDirectory: ".github/workflows",
		},
		{
			name:              "Gitea",
			workflowID:        "test.yml",
			workflowDirectory: ".gitea/workflows",
		},
		{
			name:              "Forgejo",
			workflowID:        "build.yml",
			workflowDirectory: ".forgejo/workflows",
		},
	}
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		for _, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

				// create the repo
				var sha string
				repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
					Name:      "repo-workflow-dispatch",
					LatestSha: &sha,
					Files: forgery.MapFS{
						fmt.Sprintf("%s/%s", testCase.workflowDirectory, testCase.workflowID): forgery.MapFile(
							"name: test\n" +
								"on: [workflow_dispatch]\n" +
								"jobs:\n" +
								"  test:\n" +
								"    runs-on: ubuntu-latest\n" +
								"    steps:\n" +
								"      - run: echo helloworld\n",
						),
					},
				})
				forgery.EnableRepoUnit(t, repo, unit_model.TypeActions, nil)

				gitRepo, err := gitrepo.OpenRepository(db.DefaultContext, repo)
				require.NoError(t, err)
				defer gitRepo.Close()

				workflow, err := actions_service.GetWorkflowFromCommit(gitRepo, "main", testCase.workflowID)
				require.NoError(t, err)
				assert.Equal(t, "refs/heads/main", workflow.Ref)
				assert.Equal(t, sha, workflow.Commit.ID.String())

				inputGetter := func(key string) string {
					return ""
				}

				var r *actions_model.ActionRun
				var j []string
				r, j, err = workflow.Dispatch(db.DefaultContext, inputGetter, repo, user2)
				require.NoError(t, err)

				assert.Equal(t, 1, unittest.GetCount(t, &actions_model.ActionRun{RepoID: repo.ID}))

				assert.Equal(t, "test", r.Title)
				assert.Equal(t, testCase.workflowID, r.WorkflowID)
				assert.Equal(t, testCase.workflowDirectory, r.WorkflowDirectory)
				assert.Equal(t, sha, r.CommitSHA)
				assert.Equal(t, actions_module.GithubEventWorkflowDispatch, r.TriggerEvent)
				assert.Len(t, j, 1)
				assert.Equal(t, "test", j[0])
			})
		}
	})
}

func TestActionsWorkflowDispatchRejectsInputsThatExceedLimit(t *testing.T) {
	workflow := `
name: test
on:
  workflow_dispatch:
    inputs:
      boolean:
        description: 'Boolean'
        type: boolean
      number:
        description: 'Number'
        default: '100'
        type: number
      string:
        description: 'String'
        type: string
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: echo "OK"
`

	defer test.MockVariableValue(&setting.Actions.LimitDispatchInputs, 2)()

	testCases := []struct {
		name          string
		inputs        map[string]string
		expectedError string
	}{
		{
			name:   "below-limit",
			inputs: map[string]string{"boolean": "true", "number": "10"},
		},
		{
			name:          "beyond-limit",
			inputs:        map[string]string{"boolean": "true", "number": "10", "string": "my input"},
			expectedError: "too many inputs",
		},
	}

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

		var sha string
		repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Name:      "repo-workflow-dispatch",
			LatestSha: &sha,
			Files: forgery.MapFS{
				".forgejo/workflows/dispatch.yaml": forgery.MapFile(workflow),
			},
		})
		forgery.EnableRepoUnit(t, repo, unit_model.TypeActions, nil)

		gitRepo, err := gitrepo.OpenRepository(db.DefaultContext, repo)
		require.NoError(t, err)
		defer gitRepo.Close()

		workflow, err := actions_service.GetWorkflowFromCommit(gitRepo, "main", "dispatch.yaml")
		require.NoError(t, err)
		assert.Equal(t, "refs/heads/main", workflow.Ref)
		assert.Equal(t, sha, workflow.Commit.ID.String())

		for _, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				inputGetter := func(key string) string {
					return testCase.inputs[key]
				}

				_, _, err = workflow.Dispatch(db.DefaultContext, inputGetter, repo, user2)
				if testCase.expectedError == "" {
					require.NoError(t, err)
				} else {
					assert.EqualError(t, err, testCase.expectedError)
				}
			})
		}
	})
}

func TestActionsWorkflowDispatchDynamicMatrix(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

		// create the repo
		var sha string
		repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Name:      "repo-workflow-dispatch",
			LatestSha: &sha,
			Files: forgery.MapFS{
				".forgejo/workflows/dispatch.yml": forgery.MapFile(`
name: test
on: [workflow_dispatch]
jobs:
  test:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        dim1: "${{ fromJSON(needs.other-job.outputs.some-output) }}"
    steps:
      - run: echo helloworld
`,
				),
			},
		})
		forgery.EnableRepoUnit(t, repo, unit_model.TypeActions, nil)

		gitRepo, err := gitrepo.OpenRepository(db.DefaultContext, repo)
		require.NoError(t, err)
		defer gitRepo.Close()

		workflow, err := actions_service.GetWorkflowFromCommit(gitRepo, "main", "dispatch.yml")
		require.NoError(t, err)
		assert.Equal(t, "refs/heads/main", workflow.Ref)
		assert.Equal(t, sha, workflow.Commit.ID.String())

		inputGetter := func(key string) string {
			return ""
		}

		run, _, err := workflow.Dispatch(db.DefaultContext, inputGetter, repo, user2)
		require.NoError(t, err)

		job := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID})
		assert.Contains(t, string(job.WorkflowPayload), "incomplete_matrix: true")
	})
}

// Early in the job parsing, dynamic matrices may need to access workflow inputs.  Boolean inputs need special handling
// which is what this test case covers.
func TestActionsWorkflowDispatchDynamicMatrixBooleanInput(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

		// create the repo
		var sha string
		repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Name:      "repo-workflow-dispatch",
			LatestSha: &sha,
			Files: forgery.MapFS{
				".forgejo/workflows/dispatch.yml": forgery.MapFile(`
name: test
on:
  workflow_dispatch:
    inputs:
      win32:
        description: 'Boolean'
        required: false
        type: boolean
jobs:
  test:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        runner: ${{ fromJSON(inputs.win32 && '["win32", "win64"]' || '["win64"]') }}
    steps:
      - run: echo helloworld
`,
				),
			},
		})
		forgery.EnableRepoUnit(t, repo, unit_model.TypeActions, nil)

		gitRepo, err := gitrepo.OpenRepository(db.DefaultContext, repo)
		require.NoError(t, err)
		defer gitRepo.Close()

		workflow, err := actions_service.GetWorkflowFromCommit(gitRepo, "main", "dispatch.yml")
		require.NoError(t, err)
		assert.Equal(t, "refs/heads/main", workflow.Ref)
		assert.Equal(t, sha, workflow.Commit.ID.String())

		inputGetter := func(key string) string {
			return "false"
		}

		run, _, err := workflow.Dispatch(db.DefaultContext, inputGetter, repo, user2)
		require.NoError(t, err)

		jobs, err := actions_model.GetRunJobsByRunID(t.Context(), run.ID)
		require.NoError(t, err)
		assert.Len(t, jobs, 1)
	})
}

func TestActionsWorkflowDispatchReusableWorkflow(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

		// create the repo
		var sha string
		repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Name:      "repo-workflow-dispatch",
			LatestSha: &sha,
			Files: forgery.MapFS{
				".forgejo/workflows/dispatch.yml": forgery.MapFile(
					"name: test\n" +
						"on: [workflow_dispatch]\n" +
						"jobs:\n" +
						"  test:\n" +
						"    uses: ./.forgejo/workflows/reusable.yml\n",
				),
				".forgejo/workflows/reusable.yml": forgery.MapFile(
					"name: test\n" +
						"on: [workflow_call]\n" +
						"jobs:\n" +
						"  inner:\n" +
						"    runs-on: ubuntu-latest\n" +
						"    steps:\n" +
						"      - run: echo helloworld\n",
				),
			},
		})
		forgery.EnableRepoUnit(t, repo, unit_model.TypeActions, nil)

		gitRepo, err := gitrepo.OpenRepository(db.DefaultContext, repo)
		require.NoError(t, err)
		defer gitRepo.Close()

		workflow, err := actions_service.GetWorkflowFromCommit(gitRepo, "main", "dispatch.yml")
		require.NoError(t, err)
		assert.Equal(t, "refs/heads/main", workflow.Ref)
		assert.Equal(t, sha, workflow.Commit.ID.String())

		inputGetter := func(key string) string {
			return ""
		}

		run, _, err := workflow.Dispatch(db.DefaultContext, inputGetter, repo, user2)
		require.NoError(t, err)

		var runJobs []*actions_model.ActionRunJob
		db.GetEngine(t.Context()).Where("run_id=?", run.ID).Find(&runJobs)
		assert.Len(t, runJobs, 2)

		var parentJob *actions_model.ActionRunJob
		var childJob *actions_model.ActionRunJob
		for _, j := range runJobs {
			switch j.JobID {
			case "test":
				parentJob = j
			case "inner":
				childJob = j
			}
		}
		assert.NotNil(t, parentJob, "parentJob")
		assert.NotNil(t, childJob, "childJob")
	})
}

func TestActionsWorkflowDispatchConcurrencyGroup(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

		// create the repo
		var sha string
		repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Name:      "repo-workflow-dispatch",
			LatestSha: &sha,
			Files: forgery.MapFS{
				".forgejo/workflows/dispatch.yml": forgery.MapFile(
					"name: test\n" +
						"on: [workflow_dispatch]\n" +
						"jobs:\n" +
						"  test:\n" +
						"    runs-on: ubuntu-latest\n" +
						"    steps:\n" +
						"      - run: echo helloworld\n" +
						"concurrency:\n" +
						"  group: workflow-magic-group\n" +
						"  cancel-in-progress: true\n",
				),
			},
		})
		forgery.EnableRepoUnit(t, repo, unit_model.TypeActions, nil)

		gitRepo, err := gitrepo.OpenRepository(db.DefaultContext, repo)
		require.NoError(t, err)
		defer gitRepo.Close()

		workflow, err := actions_service.GetWorkflowFromCommit(gitRepo, "main", "dispatch.yml")
		require.NoError(t, err)
		assert.Equal(t, "refs/heads/main", workflow.Ref)
		assert.Equal(t, sha, workflow.Commit.ID.String())

		inputGetter := func(key string) string {
			return ""
		}

		firstRun, _, err := workflow.Dispatch(db.DefaultContext, inputGetter, repo, user2)
		require.NoError(t, err)
		assert.Equal(t, 1, unittest.GetCount(t, &actions_model.ActionRun{RepoID: repo.ID}))
		assert.Equal(t, "workflow-magic-group", firstRun.ConcurrencyGroup)
		assert.Equal(t, actions_model.CancelInProgress, firstRun.ConcurrencyType)

		// Dispatch again and verify previous run was cancelled:
		secondRun, _, err := workflow.Dispatch(db.DefaultContext, inputGetter, repo, user2)
		require.NoError(t, err)
		assert.Equal(t, 2, unittest.GetCount(t, &actions_model.ActionRun{RepoID: repo.ID}))
		assert.Equal(t, "workflow-magic-group", secondRun.ConcurrencyGroup)
		assert.Equal(t, actions_model.CancelInProgress, secondRun.ConcurrencyType)
		firstRunReload := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: firstRun.ID})
		assert.Equal(t, actions_model.StatusCancelled, firstRunReload.Status)
	})
}

// When a workflow dispatch has an incomplete job that needs to be evaluated based upon the output from another job,
// ensure that it is still able to access a boolean workflow dispatch input correctly.
func TestActionsWorkflowDispatchIncompleteJobBooleanInput(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		// mockRunner only supported with sqlite
		t.Skip()
	}

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

		// create the repo
		var sha string
		repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Name:      "repo-workflow-dispatch",
			LatestSha: &sha,
			Files: forgery.MapFS{
				".forgejo/workflows/dispatch.yml": forgery.MapFile(`
name: test
on:
  workflow_dispatch:
    inputs:
      win32:
        description: 'Boolean'
        type: boolean
jobs:
  pre-req:
    runs-on: ubuntu-latest
    steps:
      - run: echo helloworld # theoretically we provide an output from this job, but it is mocked
  target-job:
    needs: pre-req
    runs-on: ${{ needs.pre-req.outputs.runs-on }} # makes this job incomplete until pre-req is complete
    if: ${{ inputs.win32 }} # we'll provide a 'false' bool here so this job is skipped
    steps:
      - run: echo helloworld
`,
				),
			},
		})
		forgery.EnableRepoUnit(t, repo, unit_model.TypeActions, nil)

		gitRepo, err := gitrepo.OpenRepository(db.DefaultContext, repo)
		require.NoError(t, err)
		defer gitRepo.Close()

		workflow, err := actions_service.GetWorkflowFromCommit(gitRepo, "main", "dispatch.yml")
		require.NoError(t, err)

		inputGetter := func(key string) string {
			return "false" // the string false is interpreted as a boolean false because `type: boolean` on the input
		}
		run, _, err := workflow.Dispatch(db.DefaultContext, inputGetter, repo, user2)
		require.NoError(t, err)

		runner := newMockRunner()
		runner.registerAsRepoRunner(t, user2.Name, repo.Name, "mock-runner", []string{"ubuntu-latest"})

		preReqTask := runner.fetchTask(t)
		unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{TaskID: preReqTask.GetId()})
		runner.execTask(t, preReqTask, &mockTaskOutcome{
			result:  runnerv1.Result_RESULT_SUCCESS,
			outputs: map[string]string{"runs-on": "ubunut-latest"},
		})

		// The input "false" should be considered the boolean value false and prevent the target-job from becoming
		// available to run.  It should instead become skipped:
		assert.Nil(t, runner.maybeFetchTask(t))
		targetJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{
			RunID: run.ID,
			Name:  "target-job",
		})
		assert.Equal(t, actions_model.StatusSkipped.String(), targetJob.Status.String())
	})
}

func TestActionsPullRequestWithPathsFilter(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		session := loginUser(t, "user2")

		testCases := []struct {
			name     string
			workflow string
			runTitle string
		}{
			{
				name: "paths",
				workflow: `
on:
  pull_request:
    types: [closed]
    paths:
      - test.txt
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: echo OK
`,
				runTitle: "Update test.txt",
			},
			{
				name: "paths-ignore",
				workflow: `
on:
  pull_request:
    types: [closed]
    paths-ignore:
      - test.txt
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: echo OK
`,
				runTitle: "Update README.md",
			},
		}

		for _, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				baseRepo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
					Name: "repo-pull-request",
					Files: forgery.MapFS{
						".forgejo/workflows/test.yaml": forgery.MapFile(testCase.workflow),
						"README.md":                    forgery.MapFile("Hello"),
						"test.txt":                     forgery.MapFile("one"),
					},
				})
				forgery.EnableRepoUnit(t, baseRepo, unit_model.TypeActions, nil)

				baseGitRepo, err := gitrepo.OpenRepository(db.DefaultContext, baseRepo)
				require.NoError(t, err)
				defer baseGitRepo.Close()

				// Create a pull request for README.md and merge it.
				testEditFileToNewBranch(t, session, "user2", "repo-pull-request", "main", "change-readme", "README.md", "Hello there!")
				testPullCreate(t, session, "user2", "repo-pull-request", true, "main", "change-readme", "Update README.md")

				readmePR := unittest.AssertExistsAndLoadBean(t,
					&issues_model.PullRequest{BaseRepoID: baseRepo.ID, HeadBranch: "change-readme"})

				testPullMerge(t, session, "user2", "repo-pull-request", strconv.FormatInt(readmePR.Index, 10),
					repo_model.MergeStyleFastForwardOnly, true)

				// Create a pull request for test.txt and merge it.
				testEditFileToNewBranch(t, session, "user2", "repo-pull-request", "main", "change-test", "test.txt", "two")
				testPullCreate(t, session, "user2", "repo-pull-request", true, "main", "change-test", "Update test.txt")

				testPR := unittest.AssertExistsAndLoadBean(t,
					&issues_model.PullRequest{BaseRepoID: baseRepo.ID, HeadBranch: "change-test"})

				testPullMerge(t, session, "user2", "repo-pull-request", strconv.FormatInt(testPR.Index, 10),
					repo_model.MergeStyleFastForwardOnly, true)

				unittest.AssertCount(t, &actions_model.ActionRun{RepoID: baseRepo.ID}, 1)

				run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID})
				assert.Equal(t, webhook_module.HookEventPullRequest, run.Event)
				assert.Equal(t, actions_module.GithubEventPullRequest, run.TriggerEvent)
				assert.Contains(t, run.Title, testCase.runTitle)
			})
		}
	})
}

func TestActionWorkflowTitle(t *testing.T) {
	runName := "awesome title"
	workflow := fmt.Sprintf(`
run-name: "%s"
on:
  push:
  schedule:
    - cron: "* * * * *"
  workflow_dispatch:

jobs:
  test:
    runs-on: ubuntu-latest
    steps: []
`, runName)

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		for _, tc := range []struct {
			name string
			fn   func(t testing.TB, owner *user_model.User, repo *repo_model.Repository) *actions_model.ActionRun
		}{
			{
				name: "push",
				fn: func(t testing.TB, owner *user_model.User, repo *repo_model.Repository) *actions_model.ActionRun {
					return unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{
						RepoID: repo.ID,
						Event:  webhook_module.HookEventPush,
					})
				},
			},
			{
				name: "schedule",
				fn: func(t testing.TB, _ *user_model.User, repo *repo_model.Repository) *actions_model.ActionRun {
					schedules, err := db.Find[actions_model.ActionSchedule](t.Context(), actions_model.FindScheduleOptions{RepoID: repo.ID})
					require.NoError(t, err)

					require.NoError(t, actions_service.CreateScheduleTask(t.Context(), schedules[0]))

					return unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{
						RepoID:     repo.ID,
						ScheduleID: schedules[0].ID,
					})
				},
			},

			{
				name: "workflow_dispatch",
				fn: func(t testing.TB, owner *user_model.User, repo *repo_model.Repository) *actions_model.ActionRun {
					gitRepo, err := gitrepo.OpenRepository(t.Context(), repo)
					require.NoError(t, err)
					defer gitRepo.Close()

					workflow, err := actions_service.GetWorkflowFromCommit(gitRepo, repo.DefaultBranch, "workflow.yml")
					require.NoError(t, err)

					run, _, err := workflow.Dispatch(t.Context(), nil, repo, owner)
					require.NoError(t, err)

					return run
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
				repo := forgery.CreateRepository(t, owner, &forgery.CreateRepositoryOptions{
					Files: forgery.MapFS{
						".forgejo/workflows/workflow.yml": forgery.MapFile(workflow),
					},
				})

				assert.Equal(t, runName, tc.fn(t, owner, repo).Title)
			})
		}
	})
}

func TestActionsWorkflowsAreTriggeredForOriginalCommit(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		t.Run("push", func(t *testing.T) {
			workflow := `
on:
  push:
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: echo OK
`

			oldNotify := actions_service.Notify
			defer func() {
				actions_service.Notify = oldNotify
			}()

			// Test that workflow runs are triggered for the original commit. That means that if commit A is pushed,
			// immediately followed by commit B while triggers for A are still running, that one run is triggered for A
			// and one for B, not two for either A or B. That is simulated by collecting all notifications and
			// dispatching them all at once after A and B have been pushed.
			receivedInput := make([]*actions_service.NotifyInput, 0)
			actions_service.Notify = func(ctx context.Context, input *actions_service.NotifyInput) error {
				receivedInput = append(receivedInput, input)
				return nil
			}

			user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
			repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
				Files: forgery.MapFS{
					".forgejo/workflows/workflow.yaml": forgery.MapFile(workflow),
				},
			})

			opts := files_service.ChangeRepoFilesOptions{
				Files: []*files_service.ChangeRepoFile{
					{
						Operation:     "create",
						TreePath:      "README.md",
						ContentReader: strings.NewReader("Hello world!"),
					},
				},
				Message: "add workflow",
			}
			_, err := files_service.ChangeRepoFiles(t.Context(), repo, user2, &opts)
			require.NoError(t, err)

			// Verify that the expected notifications have been generated and queued.
			assert.Len(t, receivedInput, 3)
			assert.Equal(t, webhook_module.HookEventCreate, receivedInput[0].Event)
			assert.Equal(t, git.RefName("refs/heads/main"), receivedInput[0].Ref)
			assert.Empty(t, receivedInput[0].Commit)
			assert.Equal(t, webhook_module.HookEventPush, receivedInput[1].Event)
			assert.Equal(t, git.RefName("refs/heads/main"), receivedInput[1].Ref)
			assert.NotEmpty(t, receivedInput[1].Commit)
			assert.Equal(t, webhook_module.HookEventPush, receivedInput[2].Event)
			assert.Equal(t, git.RefName("refs/heads/main"), receivedInput[2].Ref)
			assert.NotEmpty(t, receivedInput[2].Commit)

			// Dispatch the buffered inputs.
			for _, input := range receivedInput {
				require.NoError(t, oldNotify(t.Context(), input))
			}

			runs, err := db.Find[actions_model.ActionRun](t.Context(), actions_model.FindRunOptions{RepoID: repo.ID})
			require.NoError(t, err)

			slices.SortFunc(runs, func(a, b *actions_model.ActionRun) int {
				return cmp.Compare(a.ID, b.ID)
			})

			assert.Len(t, runs, 2)
			assert.Equal(t, receivedInput[1].Commit, runs[0].CommitSHA)
			assert.Equal(t, webhook_module.HookEventPush, runs[0].Event)
			assert.Equal(t, receivedInput[2].Commit, runs[1].CommitSHA)
			assert.Equal(t, webhook_module.HookEventPush, runs[1].Event)
		})

		t.Run("pull_request", func(t *testing.T) {
			workflow := `
on:
  pull_request:
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: echo OK
`

			user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
			session := loginUser(t, "user2")

			repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
				Files: forgery.MapFS{
					".forgejo/workflows/workflow.yaml": forgery.MapFile(workflow),
					"README.md":                        forgery.MapFile(""),
				},
			})

			// Capture all future notifications.
			oldNotify := actions_service.Notify
			defer func() {
				actions_service.Notify = oldNotify
			}()

			inputMutex := sync.Mutex{}

			receivedInput := make([]*actions_service.NotifyInput, 0)
			actions_service.Notify = func(ctx context.Context, input *actions_service.NotifyInput) error {
				inputMutex.Lock()
				defer inputMutex.Unlock()
				receivedInput = append(receivedInput, input)
				return nil
			}

			// Create a pull request for README.md.
			testEditFileToNewBranch(t, session, repo.OwnerName, repo.Name, repo.DefaultBranch, "change-readme", "README.md", "Hello!")
			testPullCreate(t, session, repo.OwnerName, repo.Name, true, repo.DefaultBranch, "change-readme", "Update README.md")

			// Two distinct commits should result in two action runs.
			testEditFile(t, session, repo.OwnerName, repo.Name, "change-readme", "README.md", "One!")
			testEditFile(t, session, repo.OwnerName, repo.Name, "change-readme", "README.md", "Two!")

			// Wait until all expected notifications have been generated and queued.
			allInputsReceived := func() bool {
				inputMutex.Lock()
				defer inputMutex.Unlock()

				return len(receivedInput) == 7
			}
			require.Eventually(t, allInputsReceived, 5*time.Second, 50*time.Millisecond)

			assert.Equal(t, webhook_module.HookEventPullRequest, receivedInput[2].Event)
			assert.Equal(t, git.RefName("refs/pull/1/head"), receivedInput[2].Ref)
			assert.NotEmpty(t, receivedInput[2].Commit)
			assert.Equal(t, webhook_module.HookEventPullRequestSync, receivedInput[4].Event)
			assert.Equal(t, git.RefName("refs/pull/1/head"), receivedInput[4].Ref)
			assert.NotEmpty(t, receivedInput[4].Commit)
			assert.Equal(t, webhook_module.HookEventPullRequestSync, receivedInput[6].Event)
			assert.Equal(t, git.RefName("refs/pull/1/head"), receivedInput[6].Ref)
			assert.NotEmpty(t, receivedInput[6].Commit)

			// Dispatch the buffered inputs.
			for _, input := range receivedInput {
				require.NoError(t, oldNotify(t.Context(), input))
			}

			runs, err := db.Find[actions_model.ActionRun](t.Context(), actions_model.FindRunOptions{RepoID: repo.ID})
			require.NoError(t, err)

			slices.SortFunc(runs, func(a, b *actions_model.ActionRun) int {
				return cmp.Compare(a.ID, b.ID)
			})

			assert.Len(t, runs, 3)
			assert.Equal(t, receivedInput[2].Commit, runs[0].CommitSHA)
			assert.Equal(t, webhook_module.HookEventPullRequest, runs[0].Event)
			assert.Equal(t, receivedInput[4].Commit, runs[1].CommitSHA)
			assert.Equal(t, webhook_module.HookEventPullRequestSync, runs[1].Event)
			assert.Equal(t, receivedInput[6].Commit, runs[2].CommitSHA)
			assert.Equal(t, webhook_module.HookEventPullRequestSync, runs[2].Event)
		})
	})
}

func TestActionsForkPullRequest(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		// mockRunner only supported with sqlite
		t.Skip()
	}

	// All of the actions performs here are performed as the owner of the base repo, a typical scenario.  This should
	// *not* cause events to fire in the pull request as if the PR was trusted.  For example, if someone created a PR
	// that was going to do bad things in a workflow, I wouldn't want rejecting the PR, reviewing the PR, labeling the
	// PR, closing the PR, etc. to trigger the workflow to run just because I'm acting as an authorized user; those are
	// all actions that would reasonably be taken to get rid of the bad workflow, not trigger it.
	editPR := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string, opts api.EditIssueOption) {
		url := fmt.Sprintf("/api/v1/repos/%s/issues/%d", baseRepo.FullName(), pr.Index)
		req := NewRequestWithJSON(t, "PATCH", url, opts)
		req.AddTokenAuth(token) // owner of base repo
		MakeRequest(t, req, http.StatusCreated)
	}
	assignPR := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) {
		editPR(t, baseRepo, pr, token, api.EditIssueOption{Assignees: []string{baseRepo.OwnerName}})
	}
	unassignPR := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) {
		assignPR(t, baseRepo, pr, token)
		editPR(t, baseRepo, pr, token, api.EditIssueOption{Assignees: []string{}})
	}
	addLabel := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) int64 {
		url := fmt.Sprintf("/api/v1/repos/%s/labels", baseRepo.FullName())
		req := NewRequestWithJSON(t, "POST", url, api.CreateLabelOption{
			Name:  "test-label",
			Color: "#ff0000",
		})
		req.AddTokenAuth(token) // owner of base repo
		resp := MakeRequest(t, req, http.StatusCreated)
		label := &api.Label{}
		DecodeJSON(t, resp, &label)

		url = fmt.Sprintf("/api/v1/repos/%s/issues/%d/labels", baseRepo.FullName(), pr.Index)
		req = NewRequestWithJSON(t, "POST", url, api.IssueLabelsOption{
			Labels: []any{label.ID},
		})
		req.AddTokenAuth(token) // owner of base repo
		MakeRequest(t, req, http.StatusOK)
		return label.ID
	}
	labelPR := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) {
		addLabel(t, baseRepo, pr, token)
	}
	unlabelPR := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) {
		labelID := addLabel(t, baseRepo, pr, token)
		url := fmt.Sprintf("/api/v1/repos/%s/issues/%d/labels/%d", baseRepo.FullName(), pr.Index, labelID)
		req := NewRequest(t, "DELETE", url)
		req.AddTokenAuth(token) // owner of base repo
		MakeRequest(t, req, http.StatusNoContent)
	}
	milestonePR := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) {
		url := fmt.Sprintf("/api/v1/repos/%s/milestones", baseRepo.FullName())
		req := NewRequestWithJSON(t, "POST", url, api.CreateMilestoneOption{
			Title: "test-milestone",
		})
		req.AddTokenAuth(token) // owner of base repo
		resp := MakeRequest(t, req, http.StatusCreated)
		milestone := &api.Milestone{}
		DecodeJSON(t, resp, &milestone)

		editPR(t, baseRepo, pr, token, api.EditIssueOption{Milestone: &milestone.ID})
	}
	demilestonePR := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) {
		milestonePR(t, baseRepo, pr, token)
		editPR(t, baseRepo, pr, token, api.EditIssueOption{Milestone: new(int64(0))})
	}
	editPRTitle := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) {
		editPR(t, baseRepo, pr, token, api.EditIssueOption{Title: "Edited title"})
	}
	closePR := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) {
		closed := "closed"
		editPR(t, baseRepo, pr, token, api.EditIssueOption{State: &closed})
	}
	reopenPR := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) {
		closePR(t, baseRepo, pr, token)
		open := "open"
		editPR(t, baseRepo, pr, token, api.EditIssueOption{State: &open})
	}
	synchronizePR := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) {
		// Push a new commit to the PR's head branch in the fork, as the fork's owner.
		forkOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: pr.HeadRepo.OwnerID})
		resp, err := files_service.ChangeRepoFiles(git.DefaultContext, pr.HeadRepo, forkOwner, &files_service.ChangeRepoFilesOptions{
			Files: []*files_service.ChangeRepoFile{
				{
					Operation:     "create",
					TreePath:      "file-in-fork.txt",
					ContentReader: strings.NewReader("content"),
				},
			},
			Message:   "add file-in-fork.txt",
			OldBranch: "fork-branch-1",
			NewBranch: "fork-branch-1",
		})
		require.NoError(t, err)
		// the pull request is synchronized asynchronously after the push
		require.Eventually(t, func() bool {
			return unittest.BeanExists(t, &actions_model.ActionRun{RepoID: baseRepo.ID, CommitSHA: resp.Commit.SHA})
		}, 60*time.Second, time.Millisecond*100)
	}
	requestReview := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) {
		// Simplest thing to do would be request ourselves as a reviewer, but that doesn't send notifications and
		// therefore doesn't trigger `review_requested` event (seems like a bug in that context).  Add another user as a
		// repo collaborator to request their review:
		reviewer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
		actionsTrustSetCollaborator(t, token, baseRepo, reviewer, "read")

		url := fmt.Sprintf("/api/v1/repos/%s/pulls/%d/requested_reviewers", baseRepo.FullName(), pr.Index)
		req := NewRequestWithJSON(t, "POST", url, api.PullReviewRequestOptions{
			Reviewers: []string{"user4"},
		})
		req.AddTokenAuth(token) // owner of base repo
		MakeRequest(t, req, http.StatusCreated)
	}
	removeReviewRequest := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) {
		requestReview(t, baseRepo, pr, token)
		// reviewer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})

		url := fmt.Sprintf("/api/v1/repos/%s/pulls/%d/requested_reviewers", baseRepo.FullName(), pr.Index)
		req := NewRequestWithJSON(t, "DELETE", url, api.PullReviewRequestOptions{
			Reviewers: []string{"user4"},
		})
		req.AddTokenAuth(token) // owner of base repo
		MakeRequest(t, req, http.StatusNoContent)
	}
	createPRCommentAndReturn := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) *api.Comment {
		url := fmt.Sprintf("/api/v1/repos/%s/issues/%d/comments", baseRepo.FullName(), pr.Index)
		req := NewRequestWithJSON(t, "POST", url, api.CreateIssueCommentOption{
			Body: "Comment body.",
		})
		req.AddTokenAuth(token) // owner of base repo
		resp := MakeRequest(t, req, http.StatusCreated)
		comment := &api.Comment{}
		DecodeJSON(t, resp, &comment)
		return comment
	}
	createPRComment := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) {
		createPRCommentAndReturn(t, baseRepo, pr, token)
	}
	editPRComment := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) {
		comment := createPRCommentAndReturn(t, baseRepo, pr, token)
		url := fmt.Sprintf("/api/v1/repos/%s/issues/%d/comments/%d", baseRepo.FullName(), pr.Index, comment.ID)
		req := NewRequestWithJSON(t, "PATCH", url, api.EditIssueCommentOption{
			Body: "Comment body modified!",
		})
		req.AddTokenAuth(token) // owner of base repo
		MakeRequest(t, req, http.StatusOK)
	}
	deletePRComment := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) {
		comment := createPRCommentAndReturn(t, baseRepo, pr, token)
		url := fmt.Sprintf("/api/v1/repos/%s/issues/%d/comments/%d", baseRepo.FullName(), pr.Index, comment.ID)
		req := NewRequest(t, "DELETE", url)
		req.AddTokenAuth(token) // owner of base repo
		MakeRequest(t, req, http.StatusNoContent)
	}
	createPRReview := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) {
		url := fmt.Sprintf("/api/v1/repos/%s/pulls/%d/reviews", baseRepo.FullName(), pr.Index)
		req := NewRequestWithJSON(t, "POST", url, api.CreatePullReviewOptions{
			Event: api.ReviewStateRequestChanges,
			Body:  "Review body.  This work isn't good enough!",
		})
		req.AddTokenAuth(token) // owner of base repo
		MakeRequest(t, req, http.StatusOK)
	}
	createPRReviewComment := func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) {
		url := fmt.Sprintf("/api/v1/repos/%s/pulls/%d/reviews", baseRepo.FullName(), pr.Index)
		req := NewRequestWithJSON(t, "POST", url, api.CreatePullReviewOptions{
			Event: api.ReviewStateComment,
			Body:  "Review comment.",
		})
		req.AddTokenAuth(token) // owner of base repo
		MakeRequest(t, req, http.StatusOK)
	}
	writeToRepoCode := func(t *testing.T, baseRepo *repo_model.Repository, token string, expectedStatus int) {
		req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/tags", baseRepo.FullName()),
			&api.CreateTagOption{
				TagName: "test-tag",
				Message: "This is a test tag.",
				Target:  "main",
			}).AddTokenAuth(token)
		MakeRequest(t, req, expectedStatus)
	}

	type expectedResult int
	const (
		// Workflow defined in the fork will be run, which is untrusted code.  Therefore it will require approval, and
		// be provided no secrets.
		runFromFork expectedResult = iota
		// Workflow defined in the PR base branch will be run, which is trusted code.  Therefore it will not require
		// approval, and will be provided secrets.
		runFromBase
		// Workflow will be run without recognition that it is a pull request; for example when making a comment on a PR
		// and triggering the issue_comment event.
		runFromBaseWithoutPullRequest
	)
	type testCase struct {
		name             string // must be safe to use in a repo name
		workflowPreamble string
		triggerEvent     func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string)
		expectedResult   expectedResult
	}

	testCases := []testCase{
		{
			name:             "pull_request-opened",
			workflowPreamble: "on: { pull_request: { types: [opened] } }\n",
			triggerEvent:     func(t *testing.T, baseRepo *repo_model.Repository, pr *issues_model.PullRequest, token string) {}, // no-op, triggered by creating the PR
			expectedResult:   runFromFork,
		},
		{
			name:             "pull_request-assigned",
			workflowPreamble: "on: { pull_request: { types: [assigned] } }\n",
			triggerEvent:     assignPR,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request-unassigned",
			workflowPreamble: "on: { pull_request: { types: [unassigned] } }\n",
			triggerEvent:     unassignPR,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request-labeled",
			workflowPreamble: "on: { pull_request: { types: [labeled] } }\n",
			triggerEvent:     labelPR,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request-unlabeled",
			workflowPreamble: "on: { pull_request: { types: [unlabeled] } }\n",
			triggerEvent:     unlabelPR,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request-edited",
			workflowPreamble: "on: { pull_request: { types: [edited] } }\n",
			triggerEvent:     editPRTitle,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request-closed",
			workflowPreamble: "on: { pull_request: { types: [closed] } }\n",
			triggerEvent:     closePR,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request-reopened",
			workflowPreamble: "on: { pull_request: { types: [reopened] } }\n",
			triggerEvent:     reopenPR,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request-synchronize",
			workflowPreamble: "on: { pull_request: { types: [synchronize] } }\n",
			triggerEvent:     synchronizePR,
			expectedResult:   runFromFork,
		},
		{
			name:             "pull_request-milestoned",
			workflowPreamble: "on: { pull_request: { types: [milestoned] } }\n",
			triggerEvent:     milestonePR,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request-demilestoned",
			workflowPreamble: "on: { pull_request: { types: [demilestoned] } }\n",
			triggerEvent:     demilestonePR,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request-review_requested",
			workflowPreamble: "on: { pull_request: { types: [review_requested] } }\n",
			triggerEvent:     requestReview,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request-review_request_removed",
			workflowPreamble: "on: { pull_request: { types: [review_request_removed] } }\n",
			triggerEvent:     removeReviewRequest,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request_target-assigned",
			workflowPreamble: "on: { pull_request_target: { types: [assigned] } }\n",
			triggerEvent:     assignPR,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request_target-unassigned",
			workflowPreamble: "on: { pull_request_target: { types: [unassigned] } }\n",
			triggerEvent:     unassignPR,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request_target-labeled",
			workflowPreamble: "on: { pull_request_target: { types: [labeled] } }\n",
			triggerEvent:     labelPR,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request_target-unlabeled",
			workflowPreamble: "on: { pull_request_target: { types: [unlabeled] } }\n",
			triggerEvent:     unlabelPR,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request_target-edited",
			workflowPreamble: "on: { pull_request_target: { types: [edited] } }\n",
			triggerEvent:     editPRTitle,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request_target-closed",
			workflowPreamble: "on: { pull_request_target: { types: [closed] } }\n",
			triggerEvent:     closePR,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request_target-reopened",
			workflowPreamble: "on: { pull_request_target: { types: [reopened] } }\n",
			triggerEvent:     reopenPR,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request_target-synchronize",
			workflowPreamble: "on: { pull_request_target: { types: [synchronize] } }\n",
			triggerEvent:     synchronizePR,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request_target-milestoned",
			workflowPreamble: "on: { pull_request_target: { types: [milestoned] } }\n",
			triggerEvent:     milestonePR,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request_target-demilestoned",
			workflowPreamble: "on: { pull_request_target: { types: [demilestoned] } }\n",
			triggerEvent:     demilestonePR,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request_target-review_requested",
			workflowPreamble: "on: { pull_request_target: { types: [review_requested] } }\n",
			triggerEvent:     requestReview,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request_target-review_request_removed",
			workflowPreamble: "on: { pull_request_target: { types: [review_request_removed] } }\n",
			triggerEvent:     removeReviewRequest,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request_review",
			workflowPreamble: "on: { pull_request_review: { } }",
			triggerEvent:     createPRReview,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request_review-submitted",
			workflowPreamble: "on: { pull_request_review: { types: [submitted] } }",
			triggerEvent:     createPRReview,
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request_review-edited",
			workflowPreamble: "on: { pull_request_review: { types: [edited] } }",
			triggerEvent:     createPRReview, // no API provided for editing, but editing & submitting are treated the same internally
			expectedResult:   runFromBase,
		},
		{
			name:             "pull_request_review_comment-created",
			workflowPreamble: "on: { pull_request_review_comment: { types: [created] } }",
			triggerEvent:     createPRReviewComment,
			expectedResult:   runFromBase,
		},
		{
			name:             "issue_comment-created",
			workflowPreamble: "on: { issue_comment: { types: [created] } }",
			triggerEvent:     createPRComment,
			expectedResult:   runFromBaseWithoutPullRequest,
		},
		{
			name:             "issue_comment-edited",
			workflowPreamble: "on: { issue_comment: { types: [edited] } }",
			triggerEvent:     editPRComment,
			expectedResult:   runFromBaseWithoutPullRequest,
		},
		{
			name:             "issue_comment-deleted",
			workflowPreamble: "on: { issue_comment: { types: [deleted] } }",
			triggerEvent:     deletePRComment,
			expectedResult:   runFromBaseWithoutPullRequest,
		},
	}

	makeWorkflowForEvent := func(preamble, variation string) string {
		return fmt.Sprintf(`%s
jobs:
  job-%s:
    runs-on: docker
    steps:
      - run: echo "Job contents go here."
`, preamble, variation)
	}

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2}) // owner of the base repo
		org3 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3})  // owner of the forked repo
		session := loginUser(t, user2.Name)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteIssue)

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				// create the base repo
				baseRepo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
					Name: fmt.Sprintf("repo-pull-request-%s", tc.name),
					Files: forgery.MapFS{
						".forgejo/workflows/pr.yml": forgery.MapFile(makeWorkflowForEvent(tc.workflowPreamble, "base")),
					},
				})
				forgery.EnableRepoUnit(t, baseRepo, unit_model.TypeActions, nil)

				// Create a secret in the base repo which shouldn't be exposed to fork repo PRs
				url := fmt.Sprintf("/api/v1/repos/%s/actions/secrets/%s", baseRepo.FullName(), "MY_SECRET")
				req := NewRequestWithJSON(t, "PUT", url, api.CreateOrUpdateSecretOption{
					Data: "this is a secret value that must never, ever, be disclosed",
				})
				req.AddTokenAuth(token)
				MakeRequest(t, req, http.StatusCreated)

				// create the forked repo
				forkedRepo, err := repo_service.ForkRepositoryAndUpdates(git.DefaultContext, user2, org3, repo_service.ForkRepoOptions{
					BaseRepo:    baseRepo,
					Name:        fmt.Sprintf("forked-repo-pull-request-%s", tc.name),
					Description: "test pull-request-target event",
				})
				require.NoError(t, err)
				assert.NotEmpty(t, forkedRepo)

				// add a new file to the forked repo, replacing the base's pr.yml, to check which workflow is executed:
				addFileToForkedResp, err := files_service.ChangeRepoFiles(git.DefaultContext, forkedRepo, org3, &files_service.ChangeRepoFilesOptions{
					Files: []*files_service.ChangeRepoFile{
						{
							Operation:     "update",
							TreePath:      ".forgejo/workflows/pr.yml",
							ContentReader: strings.NewReader(makeWorkflowForEvent(tc.workflowPreamble, "fork")),
						},
					},
					Message:   "add file1",
					OldBranch: "main",
					NewBranch: "fork-branch-1",
				})
				require.NoError(t, err)
				assert.NotEmpty(t, addFileToForkedResp)

				// create Pull
				pullIssue := &issues_model.Issue{
					RepoID:   baseRepo.ID,
					Title:    "Test pull-request-target-event",
					PosterID: org3.ID,
					Poster:   org3,
					IsPull:   true,
				}
				pullRequest := &issues_model.PullRequest{
					HeadRepoID: forkedRepo.ID,
					BaseRepoID: baseRepo.ID,
					HeadBranch: "fork-branch-1",
					BaseBranch: "main",
					HeadRepo:   forkedRepo,
					BaseRepo:   baseRepo,
					Type:       issues_model.PullRequestGitea,
				}
				err = pull_service.NewPullRequest(git.DefaultContext, baseRepo, pullIssue, nil, nil, pullRequest, nil)
				require.NoError(t, err)

				// Trigger specific behaviour required for the event to fire...
				tc.triggerEvent(t, baseRepo, pullRequest, token)

				runner := newMockRunner()
				runner.registerAsRepoRunner(t, user2.Name, baseRepo.Name, "mock-runner", []string{"docker"})

				switch tc.expectedResult {
				case runFromFork:
					assert.Nil(t, runner.maybeFetchTask(t), "runFromFork requires job is pending approval, but was ready to execute")
					// Approve the PR to execute, if it is currently waiting for approval:
					actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID})
					assert.True(t, actionRun.IsForkPullRequest, "run must be marked as IsForkPullRequest")
					assert.True(t, actionRun.NeedApproval, "run must be marked as NeedApproval")
					require.NoError(t, actions_service.UpdateTrustedWithPullRequest(t.Context(), user2.ID, pullRequest, actions_service.UserTrustedOnce))
				case runFromBase:
					actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID})
					assert.True(t, actionRun.IsForkPullRequest, "run must be !IsForkPullRequest")
					assert.False(t, actionRun.NeedApproval, "run must be !NeedApproval")
				case runFromBaseWithoutPullRequest:
					actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID})
					assert.False(t, actionRun.IsForkPullRequest, "run must be !IsForkPullRequest")
					assert.False(t, actionRun.NeedApproval, "run must be !NeedApproval")
				}
				if t.Failed() {
					return // faster test exit rather than waiting for fetchTask to fail, as it runs a check loop
				}

				// Now we should be able to fetch the task:
				task := runner.fetchTask(t)

				assert.Contains(t, task.Secrets, "FORGEJO_TOKEN")
				assert.Contains(t, task.Secrets, "GITEA_TOKEN")
				assert.Contains(t, task.Secrets, "GITHUB_TOKEN")
				switch tc.expectedResult {
				case runFromFork:
					assert.Contains(t, string(task.GetWorkflowPayload()), "job-fork:") // running the workflow from the fork, not the base
					assert.Len(t, task.Secrets, 3)                                     // no other secrets, especially MY_SECRET defined in the base repo
					// runFromFork should give us a token that only has read permissions to the repo's units, so a write
					// to the repo should fail:
					writeToRepoCode(t, baseRepo, task.Secrets["FORGEJO_TOKEN"], http.StatusForbidden)
				case runFromBase:
					assert.Contains(t, string(task.GetWorkflowPayload()), "job-base:") // running the workflow from the base branch, not the fork, which is why it can be trusted
					assert.Contains(t, task.Secrets, "MY_SECRET")
					assert.Len(t, task.Secrets, 4)
					// runFromBase... SHOULD give us a token that has write permissions to the repo's units, and which
					// would return http.StatusOK to this request, but it doesn't.  Every one of these cases ends up
					// marked as `IsForkPullRequest` (which is technically correct), and then the token only gets
					// read-only permissions (which is a surprise).  This is a bug; StatusCreated is correct:
					writeToRepoCode(t, baseRepo, task.Secrets["FORGEJO_TOKEN"], http.StatusForbidden)
				case runFromBaseWithoutPullRequest:
					assert.Contains(t, string(task.GetWorkflowPayload()), "job-base:") // running the workflow from the base branch, not the fork, which is why it can be trusted
					assert.Contains(t, task.Secrets, "MY_SECRET")
					assert.Len(t, task.Secrets, 4)
					writeToRepoCode(t, baseRepo, task.Secrets["FORGEJO_TOKEN"], http.StatusCreated)
				}

				runner.execTask(t, task, &mockTaskOutcome{
					result:  runnerv1.Result_RESULT_SUCCESS,
					outputs: map[string]string{},
				})
			})
		}
	})
}
