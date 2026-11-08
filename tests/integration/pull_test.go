// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/json"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestViewPulls(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	req := NewRequest(t, "GET", "/user2/repo1/pulls")
	resp := MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	search := htmlDoc.doc.Find(".list-header-search > .search > .input > input")
	placeholder, _ := search.Attr("placeholder")
	assert.Equal(t, "Search pull requests…", placeholder)
}

func TestViewPullsType(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	session := loginUser(t, user.Name)
	req := NewRequest(t, "GET", repo.Link()+"/pulls")
	resp := session.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	pullsType := htmlDoc.doc.Find(".list-header-type > .menu .item[href*=\"type=all\"]").First()
	assert.Equal(t, "All pull requests", pullsType.Text())
}

func TestPullViewConversation(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	req := NewRequest(t, "GET", "/user2/commitsonpr/pulls/1")
	resp := MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)

	t.Run("Commits", func(t *testing.T) {
		commitLists := htmlDoc.Find(".timeline-item.commits-list")
		assert.Equal(t, 4, commitLists.Length())

		commits := commitLists.Find(".singular-commit")
		assert.Equal(t, 10, commits.Length())

		// First one has not been affected by a force push, therefore it's still part of the
		// PR and should link to the PR-scoped review tab
		firstCommit := commits.Eq(0)
		firstCommitMessageHref, _ := firstCommit.Find("a.default-link").Attr("href")
		firstCommitShaHref, _ := firstCommit.Find("a.sha.label").Attr("href")
		assert.Equal(t, "/user2/commitsonpr/pulls/1/commits/4ca8bcaf27e28504df7bf996819665986b01c847", firstCommitMessageHref)
		assert.Equal(t, "/user2/commitsonpr/pulls/1/commits/4ca8bcaf27e28504df7bf996819665986b01c847", firstCommitShaHref)

		// The fifth commit has been overwritten by a force push.
		// Attempting to view the old one in the review tab won't work:
		req := NewRequest(t, "GET", "/user2/commitsonpr/pulls/1/commits/3e64625bd6eb5bcba69ac97de6c8f507402df861")
		MakeRequest(t, req, http.StatusNotFound)

		// Therefore, this commit should link to the non-PR commit view instead
		fifthCommit := commits.Eq(4)
		fifthCommitMessageHref, _ := fifthCommit.Find("a.default-link").Attr("href")
		fifthCommitShaHref, _ := fifthCommit.Find("a.sha.label").Attr("href")
		assert.Equal(t, "/user2/commitsonpr/commit/3e64625bd6eb5bcba69ac97de6c8f507402df861", fifthCommitMessageHref)
		assert.Equal(t, "/user2/commitsonpr/commit/3e64625bd6eb5bcba69ac97de6c8f507402df861", fifthCommitShaHref)
	})

	t.Run("ForcePush", func(t *testing.T) {
		forcePush := htmlDoc.Find("#issuecomment-17")
		require.Equal(t, 1, forcePush.Length())
		html, err := forcePush.Html()
		require.NoError(t, err)
		t.Logf("issuecomment-17 HTML = %s", html) // useful for figuring out errors in the assertions

		// match sections of the markup "user2 force-pushed branch 1 from 1978192d98 to 9b93963cf6 2025-06-...
		assert.Equal(t, 1, forcePush.Find("svg.octicon-repo-push").Length())
		assert.Equal(t, "user2", forcePush.Find("a.author").Text())
		assert.Contains(t, forcePush.Text(), "force-pushed branch1 from")
		assert.Equal(t, "1978192d98", forcePush.Find("a.ui.sha[href='/user2/commitsonpr/commit/1978192d98bb1b65e11c2cf37da854fbf94bffd6'] code").Text())
		assert.Equal(t, "9b93963cf6", forcePush.Find("a.ui.sha[href='/user2/commitsonpr/commit/9b93963cf6de4dc33f915bb67f192d099c301f43'] code").Text())
		assert.Contains(t, forcePush.Text(), "2025-06-")

		// followed by a Compare button:
		assert.Equal(t, 1, forcePush.Find("span.compare a[href='/user2/commitsonpr/compare/1978192d98bb1b65e11c2cf37da854fbf94bffd6..9b93963cf6de4dc33f915bb67f192d099c301f43']").Length())
	})

	t.Run("ForcePush GC'd Commits", func(t *testing.T) {
		// Adjust the content of comment #17 so that the old commit ID is one that doesn't exist in the repository.
		// Commits are stored in the `content` field in a JSON encoded structure matching PushActionContent, which needs
		// to be decoded, changed, reencoded, then updated in the DB:
		c17 := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: 17})
		var data issues_model.PushActionContent
		require.NoError(t, json.Unmarshal([]byte(c17.Content), &data))
		require.True(t, data.IsForcePush)
		require.Len(t, data.CommitIDs, 2)
		data.CommitIDs[0] = "0123456789012345678901234567890123456789" // invalid commit ID
		updated, err := json.Marshal(data)
		require.NoError(t, err)
		c17.Content = string(updated)
		_, err = db.GetEngine(t.Context()).ID(c17.ID).Cols("content").Update(c17)
		require.NoError(t, err)

		// Re-request the PR page after the modified comment:
		req := NewRequest(t, "GET", "/user2/commitsonpr/pulls/1")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		// Similar to the "ForcePush" test above, but the markup will vary due to the invalid commit:
		forcePush := htmlDoc.Find("#issuecomment-17")
		require.Equal(t, 1, forcePush.Length())
		html, err := forcePush.Html()
		require.NoError(t, err)
		t.Logf("issuecomment-17 HTML = %s", html) // useful for figuring out errors in the assertions

		// match sections of the markup "user2 force-pushed branch 1 from 1978192d98 to 9b93963cf6 2025-06-...
		assert.Equal(t, 1, forcePush.Find("svg.octicon-repo-push").Length())
		assert.Equal(t, "user2", forcePush.Find("a.author").Text())
		assert.Contains(t, forcePush.Text(), "force-pushed branch1 from")
		assert.Equal(t, "0123456789", forcePush.Find("span[data-tooltip-content] code").Text())
		assert.Contains(t, forcePush.Find("span[data-tooltip-content]").Text(), "(unavailable)")
		assert.Equal(t, "9b93963cf6", forcePush.Find("a.ui.sha[href='/user2/commitsonpr/commit/9b93963cf6de4dc33f915bb67f192d099c301f43'] code").Text())
		assert.Contains(t, forcePush.Text(), "2025-06-")
		assert.NotContains(t, forcePush.Text(), "Compare") // no compare button as one of the commits is invalid
	})
}

func TestPullManuallyMergeWarning(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	session := loginUser(t, user2.Name)

	warningMessage := `Warning: The "Autodetect manual merge" setting is not enabled for this repository, you will have to mark this pull request as manually merged afterwards.`
	t.Run("Autodetect disabled", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/pulls/3")
		resp := session.MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)
		mergeInstructions := htmlDoc.Find("#merge-instructions").Text()
		assert.Contains(t, mergeInstructions, warningMessage)
	})

	pullRequestUnit := unittest.AssertExistsAndLoadBean(t, &repo_model.RepoUnit{RepoID: 1, Type: unit.TypePullRequests})
	config := pullRequestUnit.PullRequestsConfig()
	config.AutodetectManualMerge = true
	_, err := db.GetEngine(db.DefaultContext).ID(pullRequestUnit.ID).Cols("config").Update(pullRequestUnit)
	require.NoError(t, err)

	t.Run("Autodetect enabled", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/pulls/3")
		resp := session.MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)
		mergeInstructions := htmlDoc.Find("#merge-instructions").Text()
		assert.NotContains(t, mergeInstructions, warningMessage)
	})
}

func TestPullCombinedReviewRequest(t *testing.T) {
	defer unittest.OverrideFixtures("tests/integration/fixtures/TestPullCombinedReviewRequest")()
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	helper := func(t *testing.T, action, userID, expectedText string) {
		t.Helper()

		req := NewRequestWithValues(t, "POST", "/user2/repo1/pulls/request_review", map[string]string{
			"issue_ids": "3",
			"action":    action,
			"id":        userID,
		})
		session.MakeRequest(t, req, http.StatusOK)

		req = NewRequest(t, "GET", "/user2/repo1/pulls/3")
		resp := session.MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		assert.Contains(t, htmlDoc.Find(".timeline-item:has(.review-request-list)").Last().Text(), expectedText)
	}

	helper(t, "detach", "2", "refused to review")
	helper(t, "attach", "4", "requested reviews from user4 and removed review requests for user2")
	helper(t, "attach", "9", "requested reviews from user4, user9 and removed review requests for user2")
	helper(t, "attach", "2", "requested reviews from user4, user9")
	helper(t, "detach", "4", "requested review from user9")
	helper(t, "detach", "11", "requested reviews from user9 and removed review requests for user11")
	helper(t, "detach", "9", "removed review request for user11")
	helper(t, "detach", "2", "removed review requests for user11, user2")
}

func TestShowMergeForManualMerge(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	// Only allow manual merge strategy for this repository.
	pullRepoUnit := unittest.AssertExistsAndLoadBean(t, &repo_model.RepoUnit{ID: 5, RepoID: 1, Type: unit.TypePullRequests})
	pullRepoUnit.Config = &repo_model.PullRequestsConfig{
		AllowManualMerge:  true,
		DefaultMergeStyle: repo_model.MergeStyleManuallyMerged,
	}
	repo_model.UpdateRepoUnit(t.Context(), pullRepoUnit)

	session := loginUser(t, "user2")

	req := NewRequest(t, "GET", "/user2/repo1/pulls/5")
	resp := session.MakeRequest(t, req, http.StatusOK)

	// Assert that the mergebox is shown.
	htmlDoc := NewHTMLParser(t, resp.Body)
	htmlDoc.AssertElement(t, "#pull-request-merge-form", true)
}

func TestPullUrlHandling(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	t.Run("Overview correct", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/user2/repo1/pulls")
		MakeRequest(t, req, http.StatusOK)
	})

	t.Run("Pull correct", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/user2/repo1/pulls/5")
		MakeRequest(t, req, http.StatusOK)
	})

	t.Run("Overview left-padded", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/user2/repo1/extra_text_pulls")
		MakeRequest(t, req, http.StatusNotFound)
	})

	t.Run("Overview right-padded", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/user2/repo1/pulls_extra_text")
		MakeRequest(t, req, http.StatusNotFound)
	})

	t.Run("Pull left-padded", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/user2/repo1/extra_text_pulls/5")
		MakeRequest(t, req, http.StatusNotFound)
	})

	t.Run("Pull right-padded", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/user2/repo1/pulls_extra_text/5")
		MakeRequest(t, req, http.StatusNotFound)
	})

	t.Run("POST Title correct", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		session := loginUser(t, "user2")

		req := NewRequestWithValues(t, "POST", "/user2/repo1/issues/5/title", map[string]string{"title": "test"})
		session.MakeRequest(t, req, http.StatusOK)
	})

	t.Run("POST Title padded", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		session := loginUser(t, "user2")

		req := NewRequestWithValues(t, "POST", "/user2/repo1/issues_extra_text/5/title", map[string]string{"title": "test"})
		session.MakeRequest(t, req, http.StatusNotFound)
	})

	t.Run("Pull content overview correct", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/user2/repo1/pulls/5/content-history/overview")
		MakeRequest(t, req, http.StatusOK)
	})

	t.Run("Pull content overview left-padded", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/user2/repo1/extra_text_pulls/5/content-history/overview")
		MakeRequest(t, req, http.StatusNotFound)
	})
}

// TestPullForcePushRef verifies that the refs/pull/N/comment/M/{old,new} refs, created by a force push to a PR, are
// created correctly.
func TestPullForcePushRef(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		session := loginUser(t, user2.Name)

		baseRepo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Files: forgery.MapFS{
				"FUNFACT": forgery.MapFile("Smithy was the runner up to be Forgejo's name"),
			},
		})
		forgery.EnableRepoUnits(t, baseRepo, unit.TypePullRequests)

		clone := func(t *testing.T, clone string) string {
			t.Helper()
			return forcePushRefClone(t, clone, "user2")
		}
		revParse := forcePushRefRevParse
		firstCommit := forcePushRefFirstCommit
		amendCommit := forcePushRefAmendCommit

		testCases := []struct {
			name                   string
			inputKeepForcePushRefs bool
			inputFork              bool
			inputAGit              bool
			outcomeRefsPresent     bool
		}{
			{
				name:                   "Inline PR",
				inputKeepForcePushRefs: true,
				outcomeRefsPresent:     true,
			},
			{
				name:                   "Disable Keep Refs",
				inputKeepForcePushRefs: false,
				outcomeRefsPresent:     false,
			},
			{
				name:                   "Fork PR",
				inputKeepForcePushRefs: true,
				inputFork:              true,
				outcomeRefsPresent:     true,
			},
			{
				name:                   "AGit PR",
				inputKeepForcePushRefs: true,
				inputAGit:              true,
				outcomeRefsPresent:     true,
			},
		}

		for i, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				defer test.MockVariableValue(&setting.Repository.PullRequest.KeepForcePushRefs, tc.inputKeepForcePushRefs)()

				repoName := baseRepo.FullName()
				if tc.inputFork {
					testRepoFork(t, session, "user2", baseRepo.Name, "org3", "forked-repo")
					repoName = fmt.Sprintf("org3/forked-repo")
				}

				// Local clone with a working directory:
				clonePath := clone(t, fmt.Sprintf("%s%s.git", u.String(), repoName))
				branch := fmt.Sprintf("branch-%d", i)
				require.NoError(t, git.NewCommand(t.Context(), "checkout", "-b").AddDynamicArguments(branch).Run(&git.RunOpts{Dir: clonePath}))

				// Create first commit in PR:
				firstCommitID := firstCommit(t, clonePath)
				t.Logf("firstCommitID = %s", firstCommitID)

				// Create PR:
				var pr *issues_model.PullRequest
				if !tc.inputAGit {
					require.NoError(t, git.NewCommand(t.Context(), "push", "origin").AddDynamicArguments(branch).Run(&git.RunOpts{Dir: clonePath}))
					resp := session.MakeRequest(t, NewRequestWithValues(t, "POST", fmt.Sprintf("%s/compare/main...%s:%s", baseRepo.FullName(), repoName, branch), map[string]string{
						"title": "pull request",
					}), http.StatusOK)
					jsonBody := make(map[string]string)
					require.NoError(t, json.NewDecoder(resp.Body).Decode(&jsonBody)) // {"redirect":"/user2/repo-TestPullForcePushRef-a16d140/pulls/2"}
					prIndex, err := strconv.Atoi(strings.Split(jsonBody["redirect"], "/")[4])
					require.NoError(t, err)
					pr = unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{Index: int64(prIndex), BaseRepoID: baseRepo.ID})
				} else {
					stdout := &bytes.Buffer{}
					stderr := &bytes.Buffer{}
					err := git.NewCommand(t.Context(), "push", "origin").
						AddDynamicArguments(fmt.Sprintf("%s:refs/for/main/agit-%d", branch, i)).
						Run(&git.RunOpts{Dir: clonePath, Stdout: stdout, Stderr: stderr})
					if err != nil {
						t.Logf("agit push stdout = %q", stdout.String())
						t.Logf("agit push stderr = %q", stderr.String())
						require.NoError(t, err)
					}
					// search for "remote:   http://localhost:3002/user2/repo-TestPullForcePushRef-5bab6c15/pulls/4
					stderrStr := stderr.String()
					prIndexStr := regexp.MustCompile("/pulls/([0-9]+) *\n").FindStringSubmatch(stderrStr)[1]
					prIndex, err := strconv.Atoi(prIndexStr)
					require.NoError(t, err)
					pr = unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{Index: int64(prIndex), BaseRepoID: baseRepo.ID})
				}

				// Change the first commit with an amend, and force push it:
				secondCommitID := amendCommit(t, clonePath)
				t.Logf("secondCommitID = %s", secondCommitID)
				if !tc.inputAGit {
					require.NoError(t, git.NewCommand(t.Context(), "push", "--force", "origin").AddDynamicArguments(branch).Run(&git.RunOpts{Dir: clonePath}))
				} else {
					stdout := &bytes.Buffer{}
					stderr := &bytes.Buffer{}
					err := git.NewCommand(t.Context(), "push", "--force", "--push-option=force-push=true", "origin").
						AddDynamicArguments(fmt.Sprintf("%s:refs/for/main/agit-%d", branch, i)).
						Run(&git.RunOpts{Dir: clonePath, Stdout: stdout, Stderr: stderr})
					if err != nil {
						t.Logf("agit force push stdout = %q", stdout.String())
						t.Logf("agit force push stderr = %q", stderr.String())
						require.NoError(t, err)
					}
				}

				// Find the internal PR comment marked IsForcePush created from the force push:
				forcePushComment := forcePushRefGetSingleComment(t, pr)
				assert.Equal(t, firstCommitID, forcePushComment.OldCommit)
				assert.Equal(t, secondCommitID, forcePushComment.NewCommit)

				if tc.outcomeRefsPresent {
					// firstCommitID is probably an orphan in the base repo, except that w/
					// `setting.Repository.PullRequest.KeepForcePushRefs` there will be an additional ref kept pointing at both
					// the old and new commits.  Verify the existence of those refs in the base repo:
					oldRefRevParse := revParse(t, baseRepo.RepoPath(), forcePushComment.ForcePushOldCommitGitRefName())
					assert.Equal(t, firstCommitID, oldRefRevParse)
					newRefRevParse := revParse(t, baseRepo.RepoPath(), forcePushComment.ForcePushNewCommitGitRefName())
					assert.Equal(t, secondCommitID, newRefRevParse)
				} else {
					assert.Empty(t, revParse(t, baseRepo.RepoPath(), forcePushComment.ForcePushOldCommitGitRefName()))
					assert.Empty(t, revParse(t, baseRepo.RepoPath(), forcePushComment.ForcePushNewCommitGitRefName()))
				}
			})
		}
	})
}

func forcePushRefClone(t *testing.T, clone, username string) string {
	t.Helper()

	dstPath := t.TempDir()
	cloneURL, _ := url.Parse(clone)
	cloneURL.User = url.UserPassword(username, userPassword)
	require.NoError(t, git.CloneWithArgs(t.Context(), nil, cloneURL.String(), dstPath, git.CloneRepoOptions{}))
	doGitSetRemoteURL(dstPath, "origin", cloneURL)(t)

	return dstPath
}

func forcePushRefRevParse(t *testing.T, dstPath, ref string) string {
	t.Helper()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	err := git.NewCommand(t.Context(), "rev-parse").AddDynamicArguments(ref).
		Run(&git.RunOpts{Dir: dstPath, Stdout: stdout, Stderr: stderr})
	if err != nil {
		require.Contains(t, stderr.String(), "unknown revision")
		return ""
	}
	return strings.TrimSpace(stdout.String())
}

func forcePushRefFirstCommit(t *testing.T, dstPath string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(path.Join(dstPath, "README.md"), []byte("## test content"), 0o600))
	require.NoError(t, git.AddChanges(dstPath, true))
	require.NoError(t, git.CommitChanges(dstPath, git.CommitChangesOptions{
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
		Message: "Add README.",
	}))
	return forcePushRefRevParse(t, dstPath, "HEAD")
}

func forcePushRefAmendCommit(t *testing.T, dstPath string) string {
	t.Helper()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	err := git.NewCommand(t.Context(), "commit", "--amend", "--message", "new message").
		Run(&git.RunOpts{Dir: dstPath, Stdout: stdout, Stderr: stderr})
	if err != nil {
		t.Logf("commit stdout = %q", stdout.String())
		t.Logf("commit stderr = %q", stderr.String())
	}
	require.NoError(t, err)
	return forcePushRefRevParse(t, dstPath, "HEAD")
}

// TestPullForcePushRefAccess verifies how the refs/pull/N/comment/M/{old,new} refs created by a force push to a PR can
// be accessed: they can be fetched by a git client, they cannot be modified by a git client, and they are not sent to a
// push mirror.
func TestPullForcePushRefAccess(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		defer test.MockVariableValue(&setting.Repository.PullRequest.KeepForcePushRefs, true)()

		// user1 is a site administrator; this gives the test the most privileged user possible for attempting to push to
		// the refs, and also allows a push mirror to be created to a local path.
		user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
		require.True(t, user1.IsAdmin)
		session := loginUser(t, user1.Name)

		baseRepo := forgery.CreateRepository(t, user1, &forgery.CreateRepositoryOptions{
			Files: forgery.MapFS{
				"FUNFACT": forgery.MapFile("Smithy was the runner up to be Forgejo's name"),
			},
		})
		forgery.EnableRepoUnits(t, baseRepo, unit.TypePullRequests)
		repoURL := fmt.Sprintf("%s%s.git", u.String(), baseRepo.FullName())

		// Create a PR, and then force-push to it to create the refs:
		clonePath := forcePushRefClone(t, repoURL, user1.Name)
		const branch = "force-push-branch"
		require.NoError(t, git.NewCommand(t.Context(), "checkout", "-b").AddDynamicArguments(branch).Run(&git.RunOpts{Dir: clonePath}))
		firstCommitID := forcePushRefFirstCommit(t, clonePath)
		require.NoError(t, git.NewCommand(t.Context(), "push", "origin").AddDynamicArguments(branch).Run(&git.RunOpts{Dir: clonePath}))

		resp := session.MakeRequest(t, NewRequestWithValues(t, "POST", fmt.Sprintf("%s/compare/main...%s", baseRepo.FullName(), branch), map[string]string{
			"title": "pull request",
		}), http.StatusOK)
		jsonBody := make(map[string]string)
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&jsonBody))
		prIndex, err := strconv.Atoi(strings.Split(jsonBody["redirect"], "/")[4])
		require.NoError(t, err)
		pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{Index: int64(prIndex), BaseRepoID: baseRepo.ID})

		secondCommitID := forcePushRefAmendCommit(t, clonePath)
		require.NoError(t, git.NewCommand(t.Context(), "push", "--force", "origin").AddDynamicArguments(branch).Run(&git.RunOpts{Dir: clonePath}))

		forcePushComment := forcePushRefGetSingleComment(t, pr)
		oldRef := forcePushComment.ForcePushOldCommitGitRefName()
		newRef := forcePushComment.ForcePushNewCommitGitRefName()
		commentRefPrefix := fmt.Sprintf("%s%d/comment/", git.PullPrefix, pr.Index)

		t.Run("Fetch", func(t *testing.T) {
			// A fresh clone doesn't fetch refs/pull/* by default, but they're advertised & can be fetched explicitly:
			fetchPath := forcePushRefClone(t, repoURL, user1.Name)

			// Make sure the refs/pull/*/comment/* can be read from the remote:
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			err := git.NewCommand(t.Context(), "ls-remote", "origin").AddDynamicArguments(commentRefPrefix + "*").
				Run(&git.RunOpts{Dir: fetchPath, Stdout: stdout, Stderr: stderr})
			if err != nil {
				t.Logf("ls-remote stdout = %q", stdout.String())
				t.Logf("ls-remote stderr = %q", stderr.String())
			}
			require.NoError(t, err)
			output := stdout.String()
			assert.Contains(t, output, fmt.Sprintf("%s\t%s", firstCommitID, oldRef))
			assert.Contains(t, output, fmt.Sprintf("%s\t%s", secondCommitID, newRef))

			// Verify that it can be fetched into the clone:
			err = git.NewCommand(t.Context(), "fetch", "origin").
				AddDynamicArguments(
					fmt.Sprintf("%s:refs/pinned/old", oldRef),
					fmt.Sprintf("%s:refs/pinned/new", newRef),
				).
				Run(&git.RunOpts{Dir: fetchPath})
			require.NoError(t, err)

			// Verify that the fetched refs are the correct commits:
			assert.Equal(t, firstCommitID, forcePushRefRevParse(t, fetchPath, "refs/pinned/old"))
			assert.Equal(t, secondCommitID, forcePushRefRevParse(t, fetchPath, "refs/pinned/new"))
		})

		t.Run("Push", func(t *testing.T) {
			// Create a new commit that we'll attempt to push into the comment refs:
			require.NoError(t, os.WriteFile(path.Join(clonePath, "EVIL.md"), []byte("## overwritten"), 0o600))
			require.NoError(t, git.AddChanges(clonePath, true))
			require.NoError(t, git.NewCommand(t.Context(), "commit", "--message", "attempt to modify pinned ref").Run(&git.RunOpts{Dir: clonePath}))
			thirdCommitID := forcePushRefRevParse(t, clonePath, "HEAD")

			pushCases := []struct {
				name    string
				refspec string
			}{
				{name: "Overwrite old ref", refspec: fmt.Sprintf("+%s:%s", thirdCommitID, oldRef)},
				{name: "Overwrite new ref", refspec: fmt.Sprintf("+%s:%s", thirdCommitID, newRef)},
				{name: "Delete old ref", refspec: ":" + oldRef},
				{name: "Delete new ref", refspec: ":" + newRef},
				{name: "Create new ref for existing comment", refspec: fmt.Sprintf("%s:%s%d/other", thirdCommitID, commentRefPrefix, forcePushComment.ID)},
				{name: "Create new ref for unknown comment", refspec: fmt.Sprintf("%s:%s%d/old", thirdCommitID, commentRefPrefix, forcePushComment.ID+1000)},
				{name: "Create new ref for unknown PR", refspec: fmt.Sprintf("%s:%s%d/comment/1/old", thirdCommitID, git.PullPrefix, pr.Index+1000)},
			}
			for _, pc := range pushCases {
				t.Run(pc.name, func(t *testing.T) {
					stdout := &bytes.Buffer{}
					stderr := &bytes.Buffer{}
					err := git.NewCommand(t.Context(), "push", "origin").
						AddDynamicArguments(pc.refspec).
						Run(&git.RunOpts{Dir: clonePath, Stdout: stdout, Stderr: stderr})
					require.Error(t, err)
					assert.Contains(t, stderr.String(), "Force push comment references (refs/pull/N/comment/D/{new,old}) are read-only, and cannot be written")
				})
			}

			// Verify that the refs have not changed in the base repo:
			assert.Equal(t, firstCommitID, forcePushRefRevParse(t, baseRepo.RepoPath(), oldRef))
			assert.Equal(t, secondCommitID, forcePushRefRevParse(t, baseRepo.RepoPath(), newRef))
		})
	})
}

func forcePushRefGetSingleComment(t *testing.T, pr *issues_model.PullRequest) *issues_model.Comment {
	var forcePushComment *issues_model.Comment
	// Fetch all comments on the PR and search for one, and only one, which is tagged with IsForcePush.  This is done in
	// a retry loop because TestPullRequest is an uncontrolled background goroutine (not a proper queue job, done
	// immediately during tests) from a push, and TestPullRequest will be creating these comments.
	ctx := t.Context()
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		var comments []*issues_model.Comment
		require.NoError(t, db.GetEngine(ctx).Where("issue_id = ?", pr.IssueID).Find(&comments))
		for _, c := range comments {
			require.NoError(t, c.LoadIssue(ctx))
			require.NoError(t, c.Issue.LoadRepo(ctx))
			require.NoError(t, c.LoadPushCommits(ctx))
			if c.IsForcePush {
				require.Nil(t, forcePushComment, "must be only one comment with a force push")
				forcePushComment = c
			}
		}
		require.NotNil(t, forcePushComment, "must have one comment flagged as a force push")
	}, 30*time.Second, 200*time.Millisecond)
	return forcePushComment
}
