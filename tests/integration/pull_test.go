// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"testing"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/json"
	"forgejo.org/tests"

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
