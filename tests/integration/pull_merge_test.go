// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"maps"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"forgejo.org/models"
	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	git_model "forgejo.org/models/git"
	issues_model "forgejo.org/models/issues"
	pull_model "forgejo.org/models/pull"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/models/webhook"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/hostmatcher"
	"forgejo.org/modules/log"
	"forgejo.org/modules/queue"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	"forgejo.org/modules/translation"
	"forgejo.org/services/automerge"
	app_context "forgejo.org/services/context"
	"forgejo.org/services/forms"
	"forgejo.org/services/mailer"
	"forgejo.org/services/pull"
	commitstatus_service "forgejo.org/services/repository/commitstatus"
	webhook_service "forgejo.org/services/webhook"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/hashicorp/go-version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type optionsPullMerge map[string]string

func testPullMerge(t *testing.T, session *TestSession, user, repo, pullnum string, mergeStyle repo_model.MergeStyle, deleteBranch bool) *httptest.ResponseRecorder {
	options := optionsPullMerge{
		"do": string(mergeStyle),
	}
	if deleteBranch {
		options["delete_branch_after_merge"] = "on"
	}

	return testPullMergeForm(t, session, http.StatusOK, user, repo, pullnum, options)
}

func testPullMergeForm(t *testing.T, session *TestSession, expectedCode int, user, repo, pullnum string, addOptions optionsPullMerge) *httptest.ResponseRecorder {
	link := path.Join(user, repo, "pulls", pullnum, "merge")

	options := map[string]string{}
	maps.Copy(options, addOptions)

	req := NewRequestWithValues(t, "POST", link, options)
	resp := session.MakeRequest(t, req, expectedCode)

	if expectedCode == http.StatusOK {
		respJSON := struct {
			Redirect string
		}{}
		DecodeJSON(t, resp, &respJSON)

		assert.Equal(t, fmt.Sprintf("/%s/%s/pulls/%s", user, repo, pullnum), respJSON.Redirect)
	}

	return resp
}

func testPullCleanUp(t *testing.T, session *TestSession, user, repo, pullnum string) *httptest.ResponseRecorder {
	req := NewRequest(t, "GET", path.Join(user, repo, "pulls", pullnum))
	resp := session.MakeRequest(t, req, http.StatusOK)

	// Click the little button to create a pull
	htmlDoc := NewHTMLParser(t, resp.Body)
	link, exists := htmlDoc.doc.Find(".timeline-item .delete-button").Attr("data-url")
	assert.True(t, exists, "The template has changed, can not find delete button url")
	req = NewRequest(t, "POST", link)
	resp = session.MakeRequest(t, req, http.StatusOK)

	return resp
}

// returns the hook tasks, order by ID desc.
func retrieveHookTasks(t *testing.T, hookID int64, activateWebhook bool) []*webhook.HookTask {
	t.Helper()
	if activateWebhook {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(s.Close)
		updated, err := db.GetEngine(db.DefaultContext).ID(hookID).Cols("is_active", "url").Update(webhook.Webhook{
			IsActive: true,
			URL:      s.URL,
		})

		// allow webhook deliveries on localhost
		t.Cleanup(test.MockVariableValue(&setting.Webhook.AllowedHostList, hostmatcher.MatchBuiltinLoopback))
		webhook_service.Init()

		assert.Equal(t, int64(1), updated)
		require.NoError(t, err)
	}

	hookTasks, err := webhook.HookTasks(db.DefaultContext, hookID, 1)
	require.NoError(t, err)
	return hookTasks
}

func TestPullMerge(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		hookTasks := retrieveHookTasks(t, 1, true)
		hookTasksLenBefore := len(hookTasks)

		session := loginUser(t, "user1")
		testRepoFork(t, session, "user2", "repo1", "user1", "repo1")
		testEditFile(t, session, "user1", "repo1", "master", "README.md", "Hello, World (Edited)\n")

		resp := testPullCreate(t, session, "user1", "repo1", false, "master", "master", "This is a pull title")

		elem := strings.Split(test.RedirectURL(resp), "/")
		assert.Equal(t, "pulls", elem[3])
		testPullMerge(t, session, elem[1], elem[2], elem[4], repo_model.MergeStyleMerge, false)

		hookTasks = retrieveHookTasks(t, 1, false)
		assert.Len(t, hookTasks, hookTasksLenBefore+1)
	})
}

func TestPullRebase(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		hookTasks := retrieveHookTasks(t, 1, true)
		hookTasksLenBefore := len(hookTasks)

		session := loginUser(t, "user1")
		testRepoFork(t, session, "user2", "repo1", "user1", "repo1")
		testEditFile(t, session, "user1", "repo1", "master", "README.md", "Hello, World (Edited)\n")

		resp := testPullCreate(t, session, "user1", "repo1", false, "master", "master", "This is a pull title")

		elem := strings.Split(test.RedirectURL(resp), "/")
		assert.Equal(t, "pulls", elem[3])
		testPullMerge(t, session, elem[1], elem[2], elem[4], repo_model.MergeStyleRebase, false)

		hookTasks = retrieveHookTasks(t, 1, false)
		assert.Len(t, hookTasks, hookTasksLenBefore+1)
	})
}

func TestPullRebaseMerge(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		hookTasks := retrieveHookTasks(t, 1, true)
		hookTasksLenBefore := len(hookTasks)

		session := loginUser(t, "user1")
		testRepoFork(t, session, "user2", "repo1", "user1", "repo1")
		testEditFile(t, session, "user1", "repo1", "master", "README.md", "Hello, World (Edited)\n")

		resp := testPullCreate(t, session, "user1", "repo1", false, "master", "master", "This is a pull title")

		elem := strings.Split(test.RedirectURL(resp), "/")
		assert.Equal(t, "pulls", elem[3])
		testPullMerge(t, session, elem[1], elem[2], elem[4], repo_model.MergeStyleRebaseMerge, false)

		hookTasks = retrieveHookTasks(t, 1, false)
		assert.Len(t, hookTasks, hookTasksLenBefore+1)
	})
}

func TestPullSquash(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		hookTasks := retrieveHookTasks(t, 1, true)
		hookTasksLenBefore := len(hookTasks)

		session := loginUser(t, "user1")
		testRepoFork(t, session, "user2", "repo1", "user1", "repo1")
		testEditFile(t, session, "user1", "repo1", "master", "README.md", "Hello, World (Edited)\n")
		testEditFile(t, session, "user1", "repo1", "master", "README.md", "Hello, World (Edited!)\n")

		resp := testPullCreate(t, session, "user1", "repo1", false, "master", "master", "This is a pull title")

		elem := strings.Split(test.RedirectURL(resp), "/")
		assert.Equal(t, "pulls", elem[3])
		testPullMerge(t, session, elem[1], elem[2], elem[4], repo_model.MergeStyleSquash, false)

		hookTasks = retrieveHookTasks(t, 1, false)
		assert.Len(t, hookTasks, hookTasksLenBefore+1)
	})
}

func TestPullSquashLargeMessage(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		session := loginUser(t, "user1")
		testRepoFork(t, session, "user2", "repo1", "user1", "repo1")
		testEditFile(t, session, "user1", "repo1", "master", "README.md", "Hello, World (Edited)\n")

		resp := testPullCreate(t, session, "user1", "repo1", false, "master", "master", "This is a pull title")

		elem := strings.Split(test.RedirectURL(resp), "/")
		assert.Equal(t, "pulls", elem[3])

		largeMessage := strings.Repeat("a long changelog line for the squashed commit message\n", 5000)
		require.Greater(t, len(largeMessage), 128*1024)

		testPullMergeForm(t, session, http.StatusOK, elem[1], elem[2], elem[4], optionsPullMerge{
			"do":                  string(repo_model.MergeStyleSquash),
			"merge_message_field": largeMessage,
		})
	})
}

func TestPullCleanUpAfterMerge(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		session := loginUser(t, "user1")
		testRepoFork(t, session, "user2", "repo1", "user1", "repo1")
		testEditFileToNewBranch(t, session, "user1", "repo1", "master", "feature/test", "README.md", "Hello, World (Edited - TestPullCleanUpAfterMerge)\n")

		resp := testPullCreate(t, session, "user1", "repo1", false, "master", "feature/test", "This is a pull title")

		elem := strings.Split(test.RedirectURL(resp), "/")
		assert.Equal(t, "pulls", elem[3])
		testPullMerge(t, session, elem[1], elem[2], elem[4], repo_model.MergeStyleMerge, false)

		// Check PR branch deletion
		resp = testPullCleanUp(t, session, elem[1], elem[2], elem[4])
		respJSON := struct {
			Redirect string
		}{}
		DecodeJSON(t, resp, &respJSON)

		assert.NotEmpty(t, respJSON.Redirect, "Redirected URL is not found")

		elem = strings.Split(respJSON.Redirect, "/")
		assert.Equal(t, "pulls", elem[3])

		// Check branch deletion result
		req := NewRequest(t, "GET", respJSON.Redirect)
		resp = session.MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)
		resultMsg := htmlDoc.doc.Find(".ui.message>p").Text()

		assert.Equal(t, "Branch \"user1/repo1:feature/test\" has been deleted.", resultMsg)
	})
}

func TestCantMergeWorkInProgress(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		session := loginUser(t, "user1")
		testRepoFork(t, session, "user2", "repo1", "user1", "repo1")
		testEditFile(t, session, "user1", "repo1", "master", "README.md", "Hello, World (Edited)\n")

		resp := testPullCreate(t, session, "user1", "repo1", false, "master", "master", "[wip] This is a pull title")

		req := NewRequest(t, "GET", test.RedirectURL(resp))
		resp = session.MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)
		text := strings.TrimSpace(htmlDoc.doc.Find(".merge-section > .item").Last().Text())
		assert.NotEmpty(t, text, "Can't find WIP text")

		assert.Contains(t, text, translation.NewLocale("en-US").TrString("repo.pulls.cannot_merge_work_in_progress"), "Unable to find WIP text")
		assert.Contains(t, text, "[wip]", "Unable to find WIP text")
	})
}

func TestCantMergeConflict(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		session := loginUser(t, "user1")
		testRepoFork(t, session, "user2", "repo1", "user1", "repo1")
		testEditFileToNewBranch(t, session, "user1", "repo1", "master", "conflict", "README.md", "Hello, World (Edited Once)\n")
		testEditFileToNewBranch(t, session, "user1", "repo1", "master", "base", "README.md", "Hello, World (Edited Twice)\n")

		// Use API to create a conflicting pr
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
		req := NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls", "user1", "repo1"), &api.CreatePullRequestOption{
			Head:  "conflict",
			Base:  "base",
			Title: "create a conflicting pr",
		}).AddTokenAuth(token)
		session.MakeRequest(t, req, http.StatusCreated)

		// Now this PR will be marked conflict - or at least a race will do - so drop down to pure code at this point...
		user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{
			Name: "user1",
		})
		repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{
			OwnerID: user1.ID,
			Name:    "repo1",
		})

		pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{
			HeadRepoID: repo1.ID,
			BaseRepoID: repo1.ID,
			HeadBranch: "conflict",
			BaseBranch: "base",
		})

		gitRepo, err := gitrepo.OpenRepository(git.DefaultContext, repo1)
		require.NoError(t, err)

		t.Run("Rebase", func(t *testing.T) {
			t.Run("Git version without replay", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				oldVersion, err := version.NewVersion("2.43.0")
				require.NoError(t, err)
				defer test.MockVariableValue(&git.GitVersion, oldVersion)()

				err = pull.Merge(t.Context(), pr, user1, gitRepo, repo_model.MergeStyleRebase, "", "CONFLICT", false)
				require.Error(t, err, "Merge should return an error due to conflict")
				assert.True(t, models.IsErrRebaseConflicts(err), "Merge error is not a conflict error")
			})
			t.Run("Git version with replay", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				if git.CheckGitVersionAtLeast("2.54") != nil {
					t.SkipNow()
				}

				err = pull.Merge(t.Context(), pr, user1, gitRepo, repo_model.MergeStyleRebase, "", "CONFLICT", false)
				require.Error(t, err, "Merge should return an error due to conflict")
				assert.True(t, models.IsErrRebaseConflicts(err), "Merge error is not a conflict error")
			})
		})

		t.Run("Merge", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			err = pull.Merge(t.Context(), pr, user1, gitRepo, repo_model.MergeStyleMerge, "", "CONFLICT", false)
			require.Error(t, err, "Merge should return an error due to conflict")
			assert.True(t, models.IsErrMergeConflicts(err), "Merge error is not a conflict error")
		})

		gitRepo.Close()
	})
}

func TestRebaseOntoExistingCommit(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		session := loginUser(t, "user1")
		testRepoFork(t, session, "user2", "repo1", "user1", "repo1")
		testEditFileToNewBranch(t, session, "user1", "repo1", "master", "base", "README.md", "Hello, World\n")
		testNewFileToNewBranch(t, session, "user1", "repo1", "base", "target", "TEST.md", "Hello, Test\n")
		testEditFile(t, session, "user1", "repo1", "base", "README.md", "Hello, World (Edited)\n")
		testEditFile(t, session, "user1", "repo1", "target", "README.md", "Hello, World (Edited)\n")

		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
		req := NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls", "user1", "repo1"), &api.CreatePullRequestOption{
			Head:  "target",
			Base:  "base",
			Title: "create a pr with commit that exists in base",
		}).AddTokenAuth(token)
		session.MakeRequest(t, req, http.StatusCreated)

		user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{
			Name: "user1",
		})
		repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{
			OwnerID: user1.ID,
			Name:    "repo1",
		})

		pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{
			HeadRepoID: repo1.ID,
			BaseRepoID: repo1.ID,
			HeadBranch: "target",
			BaseBranch: "base",
		})

		gitRepo, err := gitrepo.OpenRepository(git.DefaultContext, repo1)
		require.NoError(t, err)

		t.Run("Rebase", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			err = pull.Merge(t.Context(), pr, user1, gitRepo, repo_model.MergeStyleRebase, "", "REBASE-EXISTING-COMMIT", false)
			require.NoError(t, err, "Merge should succeed without error")

			changedFiles, err := gitRepo.GetFilesChangedBetween("base~1", "base")
			require.NoError(t, err)
			assert.NotEmpty(t, changedFiles, "HEAD commit should not be empty after rebase")
			assert.Len(t, changedFiles, 1, "HEAD commit should modify exactly one file after rebase")
			assert.Contains(t, changedFiles, "TEST.md", "HEAD commit should modify TEST.md after rebase")
		})

		gitRepo.Close()
	})
}

func TestCantMergeUnrelated(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		session := loginUser(t, "user1")
		testRepoFork(t, session, "user2", "repo1", "user1", "repo1")
		testEditFileToNewBranch(t, session, "user1", "repo1", "master", "base", "README.md", "Hello, World (Edited Twice)\n")

		// Now we want to create a commit on a branch that is totally unrelated to our current head
		// Drop down to pure code at this point
		user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{
			Name: "user1",
		})
		repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{
			OwnerID: user1.ID,
			Name:    "repo1",
		})
		path := repo_model.RepoPath(user1.Name, repo1.Name)

		err := git.NewCommand(git.DefaultContext, "read-tree", "--empty").Run(&git.RunOpts{Dir: path})
		require.NoError(t, err)

		stdin := bytes.NewBufferString("Unrelated File")
		var stdout strings.Builder
		err = git.NewCommand(git.DefaultContext, "hash-object", "-w", "--stdin").Run(&git.RunOpts{
			Dir:    path,
			Stdin:  stdin,
			Stdout: &stdout,
		})

		require.NoError(t, err)
		sha := strings.TrimSpace(stdout.String())

		_, _, err = git.NewCommand(git.DefaultContext, "update-index", "--add", "--replace", "--cacheinfo").AddDynamicArguments("100644", sha, "somewhere-over-the-rainbow").RunStdString(&git.RunOpts{Dir: path})
		require.NoError(t, err)

		treeSha, _, err := git.NewCommand(git.DefaultContext, "write-tree").RunStdString(&git.RunOpts{Dir: path})
		require.NoError(t, err)
		treeSha = strings.TrimSpace(treeSha)

		commitTimeStr := time.Now().Format(time.RFC3339)
		doerSig := user1.NewGitSig()
		env := append(os.Environ(),
			"GIT_AUTHOR_NAME="+doerSig.Name,
			"GIT_AUTHOR_EMAIL="+doerSig.Email,
			"GIT_AUTHOR_DATE="+commitTimeStr,
			"GIT_COMMITTER_NAME="+doerSig.Name,
			"GIT_COMMITTER_EMAIL="+doerSig.Email,
			"GIT_COMMITTER_DATE="+commitTimeStr,
		)

		messageBytes := new(bytes.Buffer)
		_, _ = messageBytes.WriteString("Unrelated")
		_, _ = messageBytes.WriteString("\n")

		stdout.Reset()
		err = git.NewCommand(git.DefaultContext, "commit-tree").AddDynamicArguments(treeSha).
			Run(&git.RunOpts{
				Env:    env,
				Dir:    path,
				Stdin:  messageBytes,
				Stdout: &stdout,
			})
		require.NoError(t, err)
		commitSha := strings.TrimSpace(stdout.String())

		_, _, err = git.NewCommand(git.DefaultContext, "branch", "unrelated").AddDynamicArguments(commitSha).RunStdString(&git.RunOpts{Dir: path})
		require.NoError(t, err)

		testEditFileToNewBranch(t, session, "user1", "repo1", "master", "conflict", "README.md", "Hello, World (Edited Once)\n")

		// Use API to create a conflicting pr
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
		req := NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls", "user1", "repo1"), &api.CreatePullRequestOption{
			Head:  "unrelated",
			Base:  "base",
			Title: "create an unrelated pr",
		}).AddTokenAuth(token)
		session.MakeRequest(t, req, http.StatusCreated)

		// Now this PR could be marked conflict - or at least a race may occur - so drop down to pure code at this point...
		gitRepo, err := gitrepo.OpenRepository(git.DefaultContext, repo1)
		require.NoError(t, err)
		pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{
			HeadRepoID: repo1.ID,
			BaseRepoID: repo1.ID,
			HeadBranch: "unrelated",
			BaseBranch: "base",
		})

		err = pull.Merge(t.Context(), pr, user1, gitRepo, repo_model.MergeStyleMerge, "", "UNRELATED", false)
		require.Error(t, err, "Merge should return an error due to unrelated")
		assert.True(t, models.IsErrMergeUnrelatedHistories(err), "Merge error is not a unrelated histories error")
		gitRepo.Close()
	})
}

func TestFastForwardOnlyMerge(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		session := loginUser(t, "user1")
		testRepoFork(t, session, "user2", "repo1", "user1", "repo1")
		testEditFileToNewBranch(t, session, "user1", "repo1", "master", "update", "README.md", "Hello, World 2\n")

		// Use API to create a pr from update to master
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
		req := NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls", "user1", "repo1"), &api.CreatePullRequestOption{
			Head:  "update",
			Base:  "master",
			Title: "create a pr that can be fast-forward-only merged",
		}).AddTokenAuth(token)
		session.MakeRequest(t, req, http.StatusCreated)

		user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{
			Name: "user1",
		})
		repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{
			OwnerID: user1.ID,
			Name:    "repo1",
		})

		pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{
			HeadRepoID: repo1.ID,
			BaseRepoID: repo1.ID,
			HeadBranch: "update",
			BaseBranch: "master",
		})

		gitRepo, err := git.OpenRepository(git.DefaultContext, repo_model.RepoPath(user1.Name, repo1.Name))
		require.NoError(t, err)

		err = pull.Merge(t.Context(), pr, user1, gitRepo, repo_model.MergeStyleFastForwardOnly, "", "FAST-FORWARD-ONLY", false)

		require.NoError(t, err)

		gitRepo.Close()
	})
}

func TestCantFastForwardOnlyMergeDiverging(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		session := loginUser(t, "user1")
		testRepoFork(t, session, "user2", "repo1", "user1", "repo1")
		testEditFileToNewBranch(t, session, "user1", "repo1", "master", "diverging", "README.md", "Hello, World diverged\n")
		testEditFile(t, session, "user1", "repo1", "master", "README.md", "Hello, World 2\n")

		// Use API to create a pr from diverging to update
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
		req := NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls", "user1", "repo1"), &api.CreatePullRequestOption{
			Head:  "diverging",
			Base:  "master",
			Title: "create a pr from a diverging branch",
		}).AddTokenAuth(token)
		session.MakeRequest(t, req, http.StatusCreated)

		user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{
			Name: "user1",
		})
		repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{
			OwnerID: user1.ID,
			Name:    "repo1",
		})

		pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{
			HeadRepoID: repo1.ID,
			BaseRepoID: repo1.ID,
			HeadBranch: "diverging",
			BaseBranch: "master",
		})

		gitRepo, err := git.OpenRepository(git.DefaultContext, repo_model.RepoPath(user1.Name, repo1.Name))
		require.NoError(t, err)

		err = pull.Merge(t.Context(), pr, user1, gitRepo, repo_model.MergeStyleFastForwardOnly, "", "DIVERGING", false)

		require.Error(t, err, "Merge should return an error due to being for a diverging branch")
		assert.True(t, models.IsErrMergeDivergingFastForwardOnly(err), "Merge error is not a diverging fast-forward-only error")

		gitRepo.Close()
	})
}

func TestPullRetargetChildOnBranchDelete(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		session := loginUser(t, "user1")
		testEditFileToNewBranch(t, session, "user2", "repo1", "master", "base-pr", "README.md", "Hello, World\n(Edited - TestPullRetargetOnCleanup - base PR)\n")
		testRepoFork(t, session, "user2", "repo1", "user1", "repo1")
		testEditFileToNewBranch(t, session, "user1", "repo1", "base-pr", "child-pr", "README.md", "Hello, World\n(Edited - TestPullRetargetOnCleanup - base PR)\n(Edited - TestPullRetargetOnCleanup - child PR)")

		respBasePR := testPullCreate(t, session, "user2", "repo1", true, "master", "base-pr", "Base Pull Request")
		elemBasePR := strings.Split(test.RedirectURL(respBasePR), "/")
		assert.Equal(t, "pulls", elemBasePR[3])

		respChildPR := testPullCreate(t, session, "user1", "repo1", false, "base-pr", "child-pr", "Child Pull Request")
		elemChildPR := strings.Split(test.RedirectURL(respChildPR), "/")
		assert.Equal(t, "pulls", elemChildPR[3])

		testPullMerge(t, session, elemBasePR[1], elemBasePR[2], elemBasePR[4], repo_model.MergeStyleMerge, true)

		// Check child PR
		req := NewRequest(t, "GET", test.RedirectURL(respChildPR))
		resp := session.MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)
		targetBranch := htmlDoc.doc.Find("#branch_target>a").Text()
		prStatus := strings.TrimSpace(htmlDoc.doc.Find(".issue-title-meta>.issue-state-label").Text())

		assert.Equal(t, "master", targetBranch)
		assert.Equal(t, "Open", prStatus)
	})
}

func TestPullDontRetargetChildOnWrongRepo(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		session := loginUser(t, "user1")
		testRepoFork(t, session, "user2", "repo1", "user1", "repo1")
		testEditFileToNewBranch(t, session, "user1", "repo1", "master", "base-pr", "README.md", "Hello, World\n(Edited - TestPullDontRetargetChildOnWrongRepo - base PR)\n")
		testEditFileToNewBranch(t, session, "user1", "repo1", "base-pr", "child-pr", "README.md", "Hello, World\n(Edited - TestPullDontRetargetChildOnWrongRepo - base PR)\n(Edited - TestPullDontRetargetChildOnWrongRepo - child PR)")

		respBasePR := testPullCreate(t, session, "user1", "repo1", false, "master", "base-pr", "Base Pull Request")
		elemBasePR := strings.Split(test.RedirectURL(respBasePR), "/")
		assert.Equal(t, "pulls", elemBasePR[3])

		respChildPR := testPullCreate(t, session, "user1", "repo1", true, "base-pr", "child-pr", "Child Pull Request")
		elemChildPR := strings.Split(test.RedirectURL(respChildPR), "/")
		assert.Equal(t, "pulls", elemChildPR[3])

		testPullMerge(t, session, elemBasePR[1], elemBasePR[2], elemBasePR[4], repo_model.MergeStyleMerge, true)

		// Check child PR
		req := NewRequest(t, "GET", test.RedirectURL(respChildPR))
		resp := session.MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)
		targetBranch := htmlDoc.doc.Find("#branch_target>a").Text()
		prStatus := strings.TrimSpace(htmlDoc.doc.Find(".issue-title-meta>.issue-state-label").Text())

		assert.Equal(t, "base-pr", targetBranch)
		assert.Equal(t, "Closed", prStatus)
	})
}

func TestPullMergeIndexerNotifier(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		// create a pull request
		session := loginUser(t, "user1")
		testRepoFork(t, session, "user2", "repo1", "user1", "repo1")
		testEditFile(t, session, "user1", "repo1", "master", "README.md", "Hello, World (Edited)\n")
		createPullResp := testPullCreate(t, session, "user1", "repo1", false, "master", "master", "Indexer notifier test pull")

		require.NoError(t, queue.GetManager().FlushAll(t.Context(), 0))

		repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{
			OwnerName: "user2",
			Name:      "repo1",
		})
		issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{
			RepoID:   repo1.ID,
			Title:    "Indexer notifier test pull",
			IsPull:   true,
			IsClosed: false,
		})

		// build the request for searching issues
		link, _ := url.Parse("/api/v1/repos/issues/search")
		query := url.Values{}
		query.Add("state", "closed")
		query.Add("type", "pulls")
		query.Add("q", "notifier")
		link.RawQuery = query.Encode()

		// search issues
		searchIssuesResp := session.MakeRequest(t, NewRequest(t, "GET", link.String()), http.StatusOK)
		var apiIssuesBefore []*api.Issue
		DecodeJSON(t, searchIssuesResp, &apiIssuesBefore)
		assert.Empty(t, apiIssuesBefore)

		// merge the pull request
		elem := strings.Split(test.RedirectURL(createPullResp), "/")
		assert.Equal(t, "pulls", elem[3])
		testPullMerge(t, session, elem[1], elem[2], elem[4], repo_model.MergeStyleMerge, false)

		// check if the issue is closed
		issue = unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{
			ID: issue.ID,
		})
		assert.True(t, issue.IsClosed)

		require.NoError(t, queue.GetManager().FlushAll(t.Context(), 0))

		// search issues again
		searchIssuesResp = session.MakeRequest(t, NewRequest(t, "GET", link.String()), http.StatusOK)
		var apiIssuesAfter []*api.Issue
		DecodeJSON(t, searchIssuesResp, &apiIssuesAfter)
		if assert.Len(t, apiIssuesAfter, 1) {
			assert.Equal(t, issue.ID, apiIssuesAfter[0].ID)
		}
	})
}

func testResetRepo(t *testing.T, repoPath, branch, commitID string) {
	f, err := os.OpenFile(filepath.Join(repoPath, "refs", "heads", branch), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(commitID + "\n")
	require.NoError(t, err)
	f.Close()

	repo, err := git.OpenRepository(t.Context(), repoPath)
	require.NoError(t, err)
	defer repo.Close()
	id, err := repo.GetBranchCommitID(branch)
	require.NoError(t, err)
	assert.Equal(t, commitID, id)
}

func TestPullMergeBranchProtect(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		admin := "user1"
		owner := "user5"
		notOwner := "user4"
		repo := "repo4"

		dstPath := t.TempDir()

		u.Path = fmt.Sprintf("%s/%s.git", owner, repo)
		u.User = url.UserPassword(owner, userPassword)

		t.Run("Clone", doGitClone(dstPath, u))

		for _, testCase := range []struct {
			name          string
			doer          string
			expectedCode  map[string]int
			filename      string
			protectBranch parameterProtectBranch
		}{
			{
				name:         "SuccessAdminNotEnoughMergeRequiredApprovals",
				doer:         admin,
				expectedCode: map[string]int{"api": http.StatusOK, "web": http.StatusOK},
				filename:     "branch-data-file-",
				protectBranch: parameterProtectBranch{
					"required_approvals": "1",
					"apply_to_admins":    "true",
				},
			},
			{
				name:         "FailOwnerProtectedFile",
				doer:         owner,
				expectedCode: map[string]int{"api": http.StatusMethodNotAllowed, "web": http.StatusBadRequest},
				filename:     "protected-file-",
				protectBranch: parameterProtectBranch{
					"protected_file_patterns": "protected-file-*",
					"apply_to_admins":         "true",
				},
			},
			{
				name:         "OwnerProtectedFile",
				doer:         owner,
				expectedCode: map[string]int{"api": http.StatusOK, "web": http.StatusOK},
				filename:     "protected-file-",
				protectBranch: parameterProtectBranch{
					"protected_file_patterns": "protected-file-*",
					"apply_to_admins":         "false",
				},
			},
			{
				name:         "FailNotOwnerProtectedFile",
				doer:         notOwner,
				expectedCode: map[string]int{"api": http.StatusMethodNotAllowed, "web": http.StatusBadRequest},
				filename:     "protected-file-",
				protectBranch: parameterProtectBranch{
					"protected_file_patterns": "protected-file-*",
				},
			},
			{
				name:         "FailOwnerNotEnoughMergeRequiredApprovals",
				doer:         owner,
				expectedCode: map[string]int{"api": http.StatusMethodNotAllowed, "web": http.StatusBadRequest},
				filename:     "branch-data-file-",
				protectBranch: parameterProtectBranch{
					"required_approvals": "1",
					"apply_to_admins":    "true",
				},
			},
			{
				name:         "SuccessOwnerNotEnoughMergeRequiredApprovals",
				doer:         owner,
				expectedCode: map[string]int{"api": http.StatusOK, "web": http.StatusOK},
				filename:     "branch-data-file-",
				protectBranch: parameterProtectBranch{
					"required_approvals": "1",
					"apply_to_admins":    "false",
				},
			},
			{
				name:         "FailNotOwnerNotEnoughMergeRequiredApprovals",
				doer:         notOwner,
				expectedCode: map[string]int{"api": http.StatusMethodNotAllowed, "web": http.StatusBadRequest},
				filename:     "branch-data-file-",
				protectBranch: parameterProtectBranch{
					"required_approvals": "1",
					"apply_to_admins":    "false",
				},
			},
			{
				name:         "SuccessNotOwner",
				doer:         notOwner,
				expectedCode: map[string]int{"api": http.StatusOK, "web": http.StatusOK},
				filename:     "branch-data-file-",
				protectBranch: parameterProtectBranch{
					"required_approvals": "0",
				},
			},
		} {
			mergeWith := func(t *testing.T, ctx APITestContext, apiOrWeb string, expectedCode int, pr int64) {
				switch apiOrWeb {
				case "api":
					ctx.ExpectedCode = expectedCode
					doAPIMergePullRequestForm(t, ctx, owner, repo, pr,
						&forms.MergePullRequestForm{
							MergeMessageField: "doAPIMergePullRequest Merge",
							Do:                string(repo_model.MergeStyleMerge),
							ForceMerge:        true,
						})
					ctx.ExpectedCode = 0
				case "web":
					testPullMergeForm(t, ctx.Session, expectedCode, owner, repo, fmt.Sprintf("%d", pr), optionsPullMerge{
						"do":          string(repo_model.MergeStyleMerge),
						"force_merge": "true",
					})
				default:
					panic(apiOrWeb)
				}
			}
			for _, withAPIOrWeb := range []string{"api", "web"} {
				t.Run(testCase.name+" "+withAPIOrWeb, func(t *testing.T) {
					defer tests.PrintCurrentTest(t)()
					branch := testCase.name + "-" + withAPIOrWeb
					unprotected := branch + "-unprotected"
					doGitCheckoutBranch(dstPath, "master")(t)
					doGitCreateBranch(dstPath, branch)(t)
					doGitPushTestRepository(dstPath, "origin", branch)(t)

					ctx := NewAPITestContext(t, owner, repo, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)
					doProtectBranch(ctx, branch, testCase.protectBranch)(t)

					ctx = NewAPITestContext(t, testCase.doer, "not used", auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)
					ctx.Username = owner
					ctx.Reponame = repo
					generateCommitWithNewData(t, littleSize, dstPath, "user2@example.com", "User Two", testCase.filename)
					doGitPushTestRepository(dstPath, "origin", branch+":"+unprotected)(t)
					pr, err := doAPICreatePullRequest(ctx, owner, repo, branch, unprotected)(t)
					require.NoError(t, err)
					mergeWith(t, ctx, withAPIOrWeb, testCase.expectedCode[withAPIOrWeb], pr.Index)
				})
			}
		}
	})
}

func testPullAutoMergeAfterCommitStatusSucceed(t *testing.T, ctx APITestContext, forkName string, approval, deleteBranch, withAPI bool) {
	// prepare environment (fork repo, create user)
	user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	testRepoFork(t, ctx.Session, "user2", "repo1", "user1", forkName)
	defer func() {
		testDeleteRepository(t, ctx.Session, "user1", forkName)
	}()

	// create a pull request with some changes
	branchName := "master"
	if deleteBranch {
		branchName = "new_branch_1"
		testEditFileToNewBranch(t, ctx.Session, "user1", forkName, "master", branchName, "README.md", "Hello, World (Edited)\n")
	} else {
		testEditFile(t, ctx.Session, "user1", forkName, "master", "README.md", "Hello, World (Edited)\n")
	}
	testPullCreate(t, ctx.Session, "user1", forkName, false, "master", branchName, "Indexer notifier test pull")

	baseRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerName: "user2", Name: "repo1"})
	forkedRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerName: "user1", Name: forkName})
	pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{
		BaseRepoID: baseRepo.ID,
		BaseBranch: "master",
		HeadRepoID: forkedRepo.ID,
		HeadBranch: branchName,
	})

	if deleteBranch {
		// check if new branch exists
		forkedGitRepo, err := gitrepo.OpenRepository(db.DefaultContext, forkedRepo)
		require.NoError(t, err)
		newBranch, err := forkedGitRepo.GetBranch(branchName)
		require.NoError(t, err)
		assert.NotNil(t, newBranch)
		forkedGitRepo.Close()
	}

	// schedule pull request for automerge
	if withAPI {
		mergePullRequestForm := forms.MergePullRequestForm{
			MergeMessageField:      "auto merge test",
			Do:                     string(repo_model.MergeStyleMerge),
			MergeWhenChecksSucceed: true,
			DeleteBranchAfterMerge: deleteBranch,
		}

		// first time scheduling an automerge pull request, should return a 201
		ctx.ExpectedCode = http.StatusCreated
		doAPIMergePullRequestForm(t, ctx, "user2", "repo1", pr.Index, &mergePullRequestForm)

		// second time scheduling an automerge pull request, should return a 409
		ctx.ExpectedCode = http.StatusConflict
		doAPIMergePullRequestForm(t, ctx, "user2", "repo1", pr.Index, &mergePullRequestForm)
	} else {
		mergePullRequestForm := map[string]string{
			"merge_message_field":       "auto merge test",
			"do":                        string(repo_model.MergeStyleMerge),
			"merge_when_checks_succeed": "true",
			"delete_branch_after_merge": strconv.FormatBool(deleteBranch),
		}

		// first time scheduling an automerge pull request, should return a 200
		testPullMergeForm(t, ctx.Session, http.StatusOK, "user2", "repo1", strconv.FormatInt(pr.Index, 10), mergePullRequestForm)

		// second time scheduling an automerge pull request, should delete the previous scheduled automerge and return a 200 again
		testPullMergeForm(t, ctx.Session, http.StatusOK, "user2", "repo1", strconv.FormatInt(pr.Index, 10), mergePullRequestForm)
	}

	// reload PR again
	pr = unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: pr.ID})
	assert.False(t, pr.HasMerged)
	assert.Empty(t, pr.MergedCommitID)

	// update commit status to success, then it should be merged automatically
	baseGitRepo, err := gitrepo.OpenRepository(db.DefaultContext, baseRepo)
	require.NoError(t, err)
	sha, err := baseGitRepo.GetRefCommitID(pr.GetGitRefName())
	require.NoError(t, err)
	masterCommitID, err := baseGitRepo.GetBranchCommitID("master")
	require.NoError(t, err)

	branches, _, err := baseGitRepo.GetBranchNames(0, 100)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"sub-home-md-img-check", "home-md-img-check", "pr-to-update", "branch2", "DefaultBranch", "develop", "feature/1", "master"}, branches)
	baseGitRepo.Close()
	defer func() {
		testResetRepo(t, baseRepo.RepoPath(), "master", masterCommitID)
	}()

	err = commitstatus_service.CreateCommitStatus(db.DefaultContext, baseRepo, user1, sha, &git_model.CommitStatus{
		State:     api.CommitStatusSuccess,
		TargetURL: "https://gitea.com",
		Context:   "gitea/actions",
	})
	require.NoError(t, err)

	// approve PR if necessary
	if approval {
		// reload PR again
		pr = unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: pr.ID})
		assert.False(t, pr.HasMerged)
		assert.Empty(t, pr.MergedCommitID)

		// approve the PR from non-author
		approveSession := loginUser(t, "user2")
		testSubmitReview(t, approveSession, "user2", "repo1", strconv.Itoa(int(pr.Index)), sha, "approve", http.StatusOK)
	}

	// reload PR again
	pr = unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: pr.ID})
	assert.True(t, pr.HasMerged)
	assert.NotEmpty(t, pr.MergedCommitID)

	unittest.AssertNotExistsBean(t, &pull_model.AutoMerge{PullID: pr.ID})

	if deleteBranch {
		// check if new branch got removed
		forkedGitRepo, err := gitrepo.OpenRepository(db.DefaultContext, forkedRepo)
		require.NoError(t, err)
		_, err = forkedGitRepo.GetBranch(branchName)
		require.Error(t, err)
		assert.True(t, git.IsErrBranchNotExist(err))
		forkedGitRepo.Close()
	}
}

func TestPullAutoMergeAfterCommitStatusSucceed(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		for _, testCase := range []struct {
			name         string
			forkName     string
			approval     bool
			deleteBranch bool
		}{
			{
				name:         "TestPullAutoMergeAfterCommitStatusSucceed",
				forkName:     "repo1-1",
				approval:     false,
				deleteBranch: false,
			},
			{
				name:         "TestPullAutoMergeAfterCommitStatusSucceedWithBranchDeletion",
				forkName:     "repo1-2",
				approval:     false,
				deleteBranch: true,
			},
			{
				name:         "TestPullAutoMergeAfterCommitStatusSucceedAndApproval",
				forkName:     "repo1-3",
				approval:     true,
				deleteBranch: false,
			},
			{
				name:         "TestPullAutoMergeAfterCommitStatusSucceedAndApprovalWithBranchDeletion",
				forkName:     "repo1-4",
				approval:     true,
				deleteBranch: true,
			},
		} {
			// perform all tests with API and web routes
			for _, withAPI := range []bool{false, true} {
				t.Run(testCase.name, func(t *testing.T) {
					defer tests.PrintCurrentTest(t)()
					protectedBranch := parameterProtectBranch{
						"enable_push":           "true",
						"enable_status_check":   "true",
						"status_check_contexts": "gitea/actions",
					}
					if testCase.approval {
						protectedBranch["required_approvals"] = "1"
					}
					ctx := NewAPITestContext(t, "user2", "repo1", auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)
					doProtectBranch(ctx, "master", protectedBranch)(t)

					ctx = NewAPITestContext(t, "user1", "repo1", auth_model.AccessTokenScopeWriteRepository)
					testPullAutoMergeAfterCommitStatusSucceed(t, ctx, testCase.forkName, testCase.approval, testCase.deleteBranch, withAPI)
				})
			}
		}
	})
}

func TestPullAutoMergeAfterCommitStatusSucceedAndApprovalForAgitFlow(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		// create a pull request
		baseAPITestContext := NewAPITestContext(t, "user2", "repo1", auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)

		dstPath := t.TempDir()

		u.Path = baseAPITestContext.GitPath()
		u.User = url.UserPassword("user2", userPassword)

		t.Run("Clone", doGitClone(dstPath, u))

		err := os.WriteFile(path.Join(dstPath, "test_file"), []byte("## test content"), 0o666)
		require.NoError(t, err)

		err = git.AddChanges(dstPath, true)
		require.NoError(t, err)

		err = git.CommitChanges(dstPath, git.CommitChangesOptions{
			Committer: &git.Signature{
				Email: "user2@example.com",
				Name:  "user2",
				When:  time.Now(),
			},
			Author: &git.Signature{
				Email: "user2@example.com",
				Name:  "user2",
				When:  time.Now(),
			},
			Message: "Testing commit 1",
		})
		require.NoError(t, err)

		stderrBuf := &bytes.Buffer{}

		err = git.NewCommand(git.DefaultContext, "push", "origin", "HEAD:refs/for/master", "-o").
			AddDynamicArguments(`topic=test/head2`).
			AddArguments("-o").
			AddDynamicArguments(`title="create a test pull request with agit"`).
			AddArguments("-o").
			AddDynamicArguments(`description="This PR is a test pull request which created with agit"`).
			Run(&git.RunOpts{Dir: dstPath, Stderr: stderrBuf})
		require.NoError(t, err)

		assert.Contains(t, stderrBuf.String(), setting.AppURL+"user2/repo1/pulls/6")

		baseRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerName: "user2", Name: "repo1"})
		pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{
			Flow:       issues_model.PullRequestFlowAGit,
			BaseRepoID: baseRepo.ID,
			BaseBranch: "master",
			HeadRepoID: baseRepo.ID,
			HeadBranch: "user2/test/head2",
		})

		session := loginUser(t, "user1")
		// Change master branch to protected
		req := NewRequestWithValues(t, "POST", "/user2/repo1/settings/branches/edit", map[string]string{
			"rule_name":             "master",
			"enable_push":           "true",
			"enable_status_check":   "true",
			"status_check_contexts": "gitea/actions",
			"required_approvals":    "1",
		})
		session.MakeRequest(t, req, http.StatusSeeOther)

		user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
		// first time insert automerge record, return true
		scheduled, err := automerge.ScheduleAutoMerge(db.DefaultContext, user1, pr, repo_model.MergeStyleMerge, "auto merge test", false)
		require.NoError(t, err)
		assert.True(t, scheduled)

		// second time insert automerge record, return false because it does exist
		scheduled, err = automerge.ScheduleAutoMerge(db.DefaultContext, user1, pr, repo_model.MergeStyleMerge, "auto merge test", false)
		require.Error(t, err)
		assert.False(t, scheduled)

		// reload pr again
		pr = unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: pr.ID})
		assert.False(t, pr.HasMerged)
		assert.Empty(t, pr.MergedCommitID)

		// update commit status to success, then it should be merged automatically
		baseGitRepo, err := gitrepo.OpenRepository(db.DefaultContext, baseRepo)
		require.NoError(t, err)
		sha, err := baseGitRepo.GetRefCommitID(pr.GetGitRefName())
		require.NoError(t, err)
		masterCommitID, err := baseGitRepo.GetBranchCommitID("master")
		require.NoError(t, err)
		baseGitRepo.Close()
		defer func() {
			testResetRepo(t, baseRepo.RepoPath(), "master", masterCommitID)
		}()

		err = commitstatus_service.CreateCommitStatus(db.DefaultContext, baseRepo, user1, sha, &git_model.CommitStatus{
			State:     api.CommitStatusSuccess,
			TargetURL: "https://gitea.com",
			Context:   "gitea/actions",
		})
		require.NoError(t, err)

		// reload pr again
		pr = unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: pr.ID})
		assert.False(t, pr.HasMerged)
		assert.Empty(t, pr.MergedCommitID)

		// approve the PR from non-author
		approveSession := loginUser(t, "user1")
		testSubmitReview(t, approveSession, "user2", "repo1", strconv.Itoa(int(pr.Index)), sha, "approve", http.StatusOK)

		// reload pr again
		pr = unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: pr.ID})
		assert.True(t, pr.HasMerged)
		assert.NotEmpty(t, pr.MergedCommitID)

		unittest.AssertNotExistsBean(t, &pull_model.AutoMerge{PullID: pr.ID})
	})
}

func TestPullDeleteBranchPerms(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		user2Session := loginUser(t, "user2")
		user4Session := loginUser(t, "user4")
		testRepoFork(t, user4Session, "user2", "repo1", "user4", "repo1")
		testEditFileToNewBranch(t, user2Session, "user2", "repo1", "master", "base-pr", "README.md", "Hello, World\n(Edited - base PR)\n")

		req := NewRequestWithValues(t, "POST", "/user4/repo1/compare/master...user2/repo1:base-pr", map[string]string{
			"title": "Testing PR",
		})
		resp := user4Session.MakeRequest(t, req, http.StatusOK)
		elem := strings.Split(test.RedirectURL(resp), "/")

		req = NewRequestWithValues(t, "POST", "/user4/repo1/pulls/"+elem[4]+"/merge", map[string]string{
			"do":                        "merge",
			"delete_branch_after_merge": "on",
		})
		user4Session.MakeRequest(t, req, http.StatusOK)

		flashCookie := user4Session.GetCookie(app_context.CookieNameFlash)
		assert.NotNil(t, flashCookie)
		assert.Equal(t, "error%3DYou%2Bdon%2527t%2Bhave%2Bpermission%2Bto%2Bdelete%2Bthe%2Bhead%2Bbranch.", flashCookie.Value)

		// Check that the branch still exist.
		req = NewRequest(t, "GET", "/user2/repo1/src/branch/base-pr")
		user4Session.MakeRequest(t, req, http.StatusOK)
	})
}

// Test that rebasing only happens when its necessary.
func TestRebaseWhenNecessary(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		session := loginUser(t, "user1")
		testRepoFork(t, session, "user2", "repo1", "user1", "repo1")

		t.Run("No rebase needed", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			testEditFile(t, session, "user1", "repo1", "master", "README.md", "Hello, World (Edited)\n")

			resp := testPullCreate(t, session, "user1", "repo1", false, "master", "master", "This is a pull title")
			pullLink := test.RedirectURL(resp)

			resp = session.MakeRequest(t, NewRequest(t, "GET", test.RedirectURL(resp)+"/commits"), http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)
			commitLinkBefore, ok := htmlDoc.Find("a.sha").Attr("href")
			assert.True(t, ok)
			commitBefore := commitLinkBefore[strings.LastIndexByte(commitLinkBefore, '/'):]

			elem := strings.Split(pullLink, "/")
			testPullMerge(t, session, elem[1], elem[2], elem[4], repo_model.MergeStyleRebase, false)

			resp = session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1"), http.StatusOK)
			htmlDoc = NewHTMLParser(t, resp.Body)
			commitLinkAfter, ok := htmlDoc.Find(".latest-commit a.sha").Attr("href")
			assert.True(t, ok)
			commitAfter := commitLinkAfter[strings.LastIndexByte(commitLinkAfter, '/'):]

			assert.Equal(t, commitBefore, commitAfter)
		})

		t.Run("Rebase needed", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Make user2/repo1 ahead of user1/repo1
			testEditFile(t, session, "user2", "repo1", "master", "README.md", "Hello, World (Edited 2x)\n")

			// To avoid conflicts, create a new file on /user/repo1.
			session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user1/repo1/_new/master", map[string]string{
				"commit_choice":  "direct",
				"tree_path":      "test-file.md",
				"content":        "newly-added-test-file",
				"commit_mail_id": "-1",
			}), http.StatusSeeOther)

			resp := testPullCreate(t, session, "user1", "repo1", false, "master", "master", "This is another pull")
			pullLink := test.RedirectURL(resp)

			resp = session.MakeRequest(t, NewRequest(t, "GET", test.RedirectURL(resp)+"/commits"), http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)
			commitLinkBefore, ok := htmlDoc.Find("a.sha").Attr("href")
			assert.True(t, ok)
			commitBefore := commitLinkBefore[strings.LastIndexByte(commitLinkBefore, '/'):]

			elem := strings.Split(pullLink, "/")
			testPullMerge(t, session, elem[1], elem[2], elem[4], repo_model.MergeStyleRebase, false)

			resp = session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1"), http.StatusOK)
			htmlDoc = NewHTMLParser(t, resp.Body)
			commitLinkAfter, ok := htmlDoc.Find(".latest-commit a.sha").Attr("href")
			assert.True(t, ok)
			commitAfter := commitLinkAfter[strings.LastIndexByte(commitLinkAfter, '/'):]

			assert.NotEqual(t, commitBefore, commitAfter)
		})
	})
}

func shuffleSlice(slice []int64) {
	rand.Shuffle(len(slice), func(i, j int) {
		slice[i], slice[j] = slice[j], slice[i]
	})
}

func bulkCreatePRs(t *testing.T, prCount int, repo *repo_model.Repository, token string, labelIDs []int64, milestoneID int64) {
	var createAllPRs sync.WaitGroup
	var errorListMutex sync.Mutex
	var errorList []any
	for i := range prCount {
		createAllPRs.Add(1)
		go func(i int) {
			defer createAllPRs.Done()
			defer func() {
				if r := recover(); r != nil {
					errorListMutex.Lock()
					defer errorListMutex.Unlock()
					errorList = append(errorList, r)
				}
			}()

			// We're going to create two branches; a new target branch where the PR will merge *into*, and a new
			// head branch where the PR will merge *from*.  This test is about finding internal concurrency
			// conflicts within Forgejo that prevent merges, and, merging simultaneously into the *same branch*
			// would have natural conflicts that aren't what we're attempting to test.
			targetBranchName := fmt.Sprintf("target-branch-%d", i)
			req := NewRequestWithJSON(t,
				"POST",
				fmt.Sprintf("/api/v1/repos/%s/%s/branches", repo.OwnerName, repo.Name),
				&api.CreateBranchRepoOption{
					OldRefName: "main",
					BranchName: targetBranchName,
				}).AddTokenAuth(token)
			MakeRequest(t, req, http.StatusCreated)

			// Create the head branch that we'll be trying to merge from, with a file change:
			headBranchName := fmt.Sprintf("update-%d", i)
			req = NewRequestWithJSON(t,
				"POST",
				fmt.Sprintf("/api/v1/repos/%s/%s/contents/README-%d.md", repo.OwnerName, repo.Name, i),
				&api.CreateFileOptions{
					FileOptions: api.FileOptions{
						NewBranchName: headBranchName,
					},
					ContentBase64: base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "Hello, world %d!\n", i)),
				}).AddTokenAuth(token)
			MakeRequest(t, req, http.StatusCreated)

			// Create a PR for the branch
			myLabelIDs := slices.Clone(labelIDs)
			shuffleSlice(myLabelIDs) // use a random ordering for labels as it may cause deadlocks when their count of assigned issues is updated
			req = NewRequestWithJSON(t, http.MethodPost,
				fmt.Sprintf("/api/v1/repos/%s/%s/pulls", repo.OwnerName, repo.Name),
				&api.CreatePullRequestOption{
					Head:      headBranchName,
					Base:      targetBranchName,
					Title:     fmt.Sprintf("create PR from branch %s", headBranchName),
					Labels:    myLabelIDs,
					Milestone: milestoneID,
				}).AddTokenAuth(token)
			MakeRequest(t, req, http.StatusCreated)
		}(i)
	}
	createAllPRs.Wait()
	assert.Empty(t, errorList)
}

func TestMergeConcurrency(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		token := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteIssue)

		// How many PRs will be created and attempted to be merge concurrently?  5 was enough to reproduce deadlock
		// errors from label and repo stat updates ~80% of the time, and it keeps the test reasonably fast. Test has
		// been run up to 50 without errors.
		concurrentCount := 5

		repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Files: forgery.FilesInit{},
		})

		// Create labels for the PRs...
		labelNames := []string{"bug", "super-important", "tricky", "deadlock"}
		labelIDs := make([]int64, 0, len(labelNames))
		for _, label := range labelNames {
			req := NewRequestWithJSON(t,
				"POST",
				fmt.Sprintf("/api/v1/repos/%s/%s/labels", repo.OwnerName, repo.Name),
				&api.CreateLabelOption{
					Color: "#000000",
					Name:  label,
				}).AddTokenAuth(token)
			resp := MakeRequest(t, req, http.StatusCreated)

			var apiLabel api.Label
			DecodeJSON(t, resp, &apiLabel)
			labelIDs = append(labelIDs, apiLabel.ID)
		}

		// Create a milestone that all PRs will be associated to...
		req := NewRequestWithJSON(t,
			"POST",
			fmt.Sprintf("/api/v1/repos/%s/%s/milestones", repo.OwnerName, repo.Name),
			&api.CreateMilestoneOption{
				Title:       "Test Milestone",
				Description: "This is the test milestone.",
				State:       "open",
			}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusCreated)
		var apiMilestone api.Milestone
		DecodeJSON(t, resp, &apiMilestone)

		bulkCreatePRs(t, concurrentCount, repo, token, labelIDs, apiMilestone.ID)

		// All our PRs are created; now let's try to merge them concurrently.

		lc, logCleanup := test.NewLogChecker(log.DEFAULT, log.ERROR)
		lc.Filter(
			// Typical error when the test fails:
			"Failed to update PR to merged",

			// MySQL & PostgreSQL will both report 40001 for deadlocks
			"40001",
		)
		lc.StopMark("===End Test: TestMergeConcurrency===")
		defer logCleanup()

		{
			var mergeAllPRs sync.WaitGroup
			var errorListMutex sync.Mutex
			var errorList []any
			for i := range concurrentCount {
				mergeAllPRs.Add(1)
				go func(i int) {
					defer mergeAllPRs.Done()
					defer func() {
						if r := recover(); r != nil {
							errorListMutex.Lock()
							defer errorListMutex.Unlock()
							errorList = append(errorList, r)
						}
					}()

					targetBranchName := fmt.Sprintf("target-branch-%d", i)
					headBranchName := fmt.Sprintf("update-%d", i)
					pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{
						HeadRepoID: repo.ID,
						BaseRepoID: repo.ID,
						HeadBranch: headBranchName,
						BaseBranch: targetBranchName,
					})

					req := NewRequestWithJSON(t,
						"POST",
						fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/merge", repo.OwnerName, repo.Name, pr.Index),
						&forms.MergePullRequestForm{
							Do:                "merge",
							MergeMessageField: fmt.Sprintf("Merge %s", targetBranchName),
						}).AddTokenAuth(token)
					resp := MakeRequest(t, req, http.StatusOK)
					assert.Empty(t, resp.Body.String())
				}(i)
			}
			mergeAllPRs.Wait()
			assert.Empty(t, errorList)
		}

		// Verify that: Logs had no errors
		log.Error("===End Test: TestMergeConcurrency===")
		filtered, stopped := lc.Check(100 * time.Millisecond)
		assert.False(t, filtered[0], "received 'Failed to update PR to merged' error log messages")
		assert.False(t, filtered[1], "40001 deadlock errors were logged during test")
		assert.True(t, stopped)

		// Verify that: All PRs are merged on the repo
		for i := range concurrentCount {
			targetBranchName := fmt.Sprintf("target-branch-%d", i)
			headBranchName := fmt.Sprintf("update-%d", i)
			pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{
				HeadRepoID: repo.ID,
				BaseRepoID: repo.ID,
				HeadBranch: headBranchName,
				BaseBranch: targetBranchName,
			})
			assert.True(t, pr.HasMerged, "pr %s merged", headBranchName)
		}
	})
}

func TestMergeHTTPRequestCancellation(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		user2Session := loginUser(t, "user2")
		token := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteIssue)

		// The purpose of this test is to interrupt the HTTP request to "/%s/%s/pulls/%d/merge" by cancelling the
		// context at various times during the request, and ensuring that we don't get into any states where the request
		// has partially succeeded but then been cancelled -- for example, wrote the merge to the repo, but didn't
		// update Forgejo's database. To do this we're going to create a bunch of PRs, merge them, and cancel request
		// during merge -- evenly distributing the cancellation times like this:
		cancellationChecks := 5                                                       // number of pull requests to create and attempt to merge
		measuredMergeTime := 283 * time.Millisecond                                   // time measured on a test system for one POST /%s/%s/pulls/%d/merge
		cancellationDuration := measuredMergeTime / time.Duration(cancellationChecks) // cancel after (i+1) * cancellationDuration for each PR

		repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Files: forgery.FilesInit{},
		})

		bulkCreatePRs(t, cancellationChecks, repo, token, nil, 0)

		// All our PRs are created; now let's try to merge them concurrently. This technically doesn't have to be
		// concurrent, but `TestMergeConcurrency` already had all this logic for this test to copy, and it reduces the
		// test runtime:
		{
			var mergeAllPRs sync.WaitGroup
			var errorListMutex sync.Mutex
			var errorList []any
			for i := range cancellationChecks {
				mergeAllPRs.Add(1)
				go func(i int) {
					defer mergeAllPRs.Done()
					defer func() {
						if r := recover(); r != nil {
							errorListMutex.Lock()
							defer errorListMutex.Unlock()
							errorList = append(errorList, r)
						}
					}()

					targetBranchName := fmt.Sprintf("target-branch-%d", i)
					headBranchName := fmt.Sprintf("update-%d", i)
					pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{
						HeadRepoID: repo.ID,
						BaseRepoID: repo.ID,
						HeadBranch: headBranchName,
						BaseBranch: targetBranchName,
					})

					// Here's the major subject of this test: every merge request is fired with a different context
					// timeout, causing the HTTP request to be interrupted in different places throughout the request.
					reqCtx, cancel := context.WithTimeout(t.Context(), time.Duration(i+1)*cancellationDuration)
					defer cancel()

					req := NewRequestWithValues(t, "POST",
						fmt.Sprintf("/%s/%s/pulls/%d/merge", repo.OwnerName, repo.Name, pr.Index), map[string]string{
							"do":                        "merge",
							"delete_branch_after_merge": "on",
						})
					req.Request = req.WithContext(reqCtx)
					user2Session.MakeRequest(t, req, NoExpectedStatus)
				}(i)
			}
			mergeAllPRs.Wait()
			assert.Empty(t, errorList)
		}

		// Verify that all PRs are in a consistent state of merged or not (not a corrupt state):
		gitRepo, err := gitrepo.OpenRepository(t.Context(), repo)
		require.NoError(t, err)

		for i := range cancellationChecks {
			targetBranchName := fmt.Sprintf("target-branch-%d", i)
			headBranchName := fmt.Sprintf("update-%d", i)
			pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{
				HeadRepoID: repo.ID,
				BaseRepoID: repo.ID,
				HeadBranch: headBranchName,
				BaseBranch: targetBranchName,
			})
			targetBranchInDB := unittest.AssertExistsAndLoadBean(t, &git_model.Branch{
				RepoID: repo.ID,
				Name:   targetBranchName,
			})

			targetBranchCommitIDInRepo, err := gitRepo.GetBranchCommitID(targetBranchName)
			require.NoError(t, err)
			assert.Equal(t, targetBranchCommitIDInRepo, targetBranchInDB.CommitID, "real commit ID match for %s", targetBranchName)

			targetBranchCommitInRepo, err := gitRepo.GetCommit(targetBranchCommitIDInRepo)
			require.NoError(t, err)
			assert.Equal(t, strings.TrimSpace(targetBranchCommitInRepo.CommitMessage), strings.TrimSpace(targetBranchInDB.CommitMessage))

			if pr.HasMerged {
				assert.Equal(t,
					fmt.Sprintf("Merge pull request 'create PR from branch %[1]s' (#%[2]d) from %[1]s into %[3]s", headBranchName, pr.Index, targetBranchName),
					targetBranchInDB.CommitMessage)
			} else {
				assert.Equal(t, "Initial commit", targetBranchInDB.CommitMessage)
			}
		}
	})
}

func getRepoDefaultBranchCommitID(t *testing.T, repo *repo_model.Repository) string {
	gitRepoPR, err := gitrepo.OpenRepository(db.DefaultContext, repo)
	require.NoError(t, err)
	sha, err := gitRepoPR.GetRefCommitID(repo.DefaultBranch)
	require.NoError(t, err)
	gitRepoPR.Close()
	return sha
}

func TestMergeClosesIssueInOtherRepo(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		const testFileName = "README.md"
		const testFileContents = "test\n"
		const changeBranch = "change"
		const newFileName = "newFile"

		user := forgery.CreateUser(t, &forgery.CreateUserOptions{
			EmailNotificationsPreference: user_model.EmailNotificationsAndYourOwn,
		})
		org := forgery.CreateOrganisation(t, user, nil)

		// create repo where the PR that closes the issue will be
		repoPR := forgery.CreateRepository(t, org.AsUser(), &forgery.CreateRepositoryOptions{
			DefaultBranch: "main",
			Files:         forgery.MapFS{testFileName: forgery.MapFile(testFileContents)},
			Name:          "repo-pr",
		})

		// create repo that contains the issue to be closed
		repoIssue := forgery.CreateRepository(t, org.AsUser(), &forgery.CreateRepositoryOptions{
			Files: forgery.FilesInit{},
			Name:  "repo-issue",
		})

		// create the issue
		issue := createIssue(t, user, repoIssue, "test issue", "to be closed by merging PR in repoPR")

		// create a branch in repoPR
		dstPath := t.TempDir()
		giteaURL.Path = fmt.Sprintf("%s/%s.git", org.Name, repoPR.Name)
		giteaURL.User = url.UserPassword(user.Name, userPassword)
		doGitClone(dstPath, giteaURL)(t)
		doGitCreateBranch(dstPath, changeBranch)(t)

		// add a file, create a commit in repoPR, push it
		generateCommitWithNewData(t, littleSize, dstPath, user.Email, user.Name, newFileName)
		doGitPushTestRepository(dstPath, "origin", changeBranch)(t)

		// create a PR
		ctx := NewAPITestContext(t, user.Name, repoPR.Name, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)
		pr, err := doAPICreatePullRequest(ctx, org.Name, repoPR.Name, repoPR.DefaultBranch, changeBranch)(t)
		require.NoError(t, err)

		// merge PR with a comment that uses the close keyword
		messageText := ""
		defer test.MockVariableValue(&mailer.SendAsync, func(msgs ...*mailer.Message) {
			linkFragment := fmt.Sprintf("%s/%s/commit/", org.Name, repoPR.Name)
			if len(msgs) > 0 && strings.Contains(msgs[0].Body, linkFragment) {
				messageText = msgs[0].Body
			}
		})()
		doAPIMergePullRequestForm(t, ctx, org.Name, repoPR.Name, pr.Index,
			&forms.MergePullRequestForm{
				MergeMessageField: fmt.Sprintf("closes %s/%s#%d", org.Name, repoIssue.Name, issue.Index),
				Do:                string(repo_model.MergeStyleMerge),
				ForceMerge:        true,
			})

		// verify the issue is closed
		issue = unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: issue.ID})
		assert.True(t, issue.IsClosed)

		// verify link in email
		sha := getRepoDefaultBranchCommitID(t, repoPR)
		linkFragmentWithSha := fmt.Sprintf("%s/%s/commit/%s", org.Name, repoPR.Name, sha)
		assert.Contains(t, messageText, linkFragmentWithSha)
	})
}
