// Copyright 2019 The Gitea Authors. All rights reserved.
// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strconv"
	"strings"
	"testing"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	repo_module "forgejo.org/modules/repository"
	"forgejo.org/modules/test"
	issue_service "forgejo.org/services/issue"
	"forgejo.org/services/mailer"
	repo_service "forgejo.org/services/repository"
	files_service "forgejo.org/services/repository/files"
	"forgejo.org/tests"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPullView_ReviewerMissed(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user1")

	req := NewRequest(t, "GET", "/pulls")
	resp := session.MakeRequest(t, req, http.StatusOK)
	assert.True(t, test.IsNormalPageCompleted(resp.Body.String()))

	req = NewRequest(t, "GET", "/user2/repo1/pulls/3")
	resp = session.MakeRequest(t, req, http.StatusOK)
	assert.True(t, test.IsNormalPageCompleted(resp.Body.String()))

	// if some reviews are missing, the page shouldn't fail
	reviews, err := issues_model.FindReviews(db.DefaultContext, issues_model.FindReviewOptions{
		IssueID: 2,
	})
	require.NoError(t, err)
	for _, r := range reviews {
		require.NoError(t, issues_model.DeleteReview(db.DefaultContext, r))
	}
	req = NewRequest(t, "GET", "/user2/repo1/pulls/2")
	resp = session.MakeRequest(t, req, http.StatusOK)
	assert.True(t, test.IsNormalPageCompleted(resp.Body.String()))
}

func loadComment(t *testing.T, commentID string) *issues_model.Comment {
	t.Helper()
	id, err := strconv.ParseInt(commentID, 10, 64)
	require.NoError(t, err)
	return unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: id})
}

func TestPullView_SelfReviewNotification(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, giteaURL *url.URL) {
		user1Session := loginUser(t, "user1")
		user2Session := loginUser(t, "user2")

		user1csrf := GetCSRF(t, user1Session, "/")
		oldUser1NotificationCount := getUserNotificationCount(t, user1Session, user1csrf)

		user2csrf := GetCSRF(t, user2Session, "/")
		oldUser2NotificationCount := getUserNotificationCount(t, user2Session, user2csrf)

		user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

		repo, _, f := tests.CreateDeclarativeRepo(t, user2, "test_reviewer", nil, nil, []*files_service.ChangeRepoFile{
			{
				Operation:     "create",
				TreePath:      "CODEOWNERS",
				ContentReader: strings.NewReader("README.md @user5\n"),
			},
		})
		defer f()

		// we need to add user1 as collaborator so it can be added as reviewer
		err := repo_module.AddCollaborator(db.DefaultContext, repo, user1)
		require.NoError(t, err)

		// create a new branch to prepare for pull request
		err = updateFileInBranch(user2, repo, "README.md", "codeowner-basebranch",
			strings.NewReader("# This is a new project\n"),
		)
		require.NoError(t, err)

		// Create a pull request.
		resp := testPullCreate(t, user2Session, "user2", "test_reviewer", false, repo.DefaultBranch, "codeowner-basebranch", "Test Pull Request")
		prURL := test.RedirectURL(resp)
		elem := strings.Split(prURL, "/")
		assert.Equal(t, "pulls", elem[3])

		req := NewRequest(t, http.MethodGet, prURL)
		resp = MakeRequest(t, req, http.StatusOK)
		doc := NewHTMLParser(t, resp.Body)
		attributeFilter := fmt.Sprintf("[data-update-url='/%s/%s/issues/request_review']", user2.Name, repo.Name)
		issueID, ok := doc.Find(attributeFilter).Attr("data-issue-id")
		assert.True(t, ok, "doc must contain data-issue-id")

		user1csrf = GetCSRF(t, user1Session, "/")
		testAssignReviewer(t, user1Session, user1csrf, user2.Name, repo.Name, issueID, "1", http.StatusOK)

		// both user notification should keep the same notification count since
		// user2 added itself as reviewer.
		user1csrf = GetCSRF(t, user1Session, "/")
		notificationCount := getUserNotificationCount(t, user1Session, user1csrf)
		assert.Equal(t, oldUser1NotificationCount, notificationCount)

		user2csrf = GetCSRF(t, user2Session, "/")
		notificationCount = getUserNotificationCount(t, user2Session, user2csrf)
		assert.Equal(t, oldUser2NotificationCount, notificationCount)
	})
}

func TestPullView_ResolveInvalidatedReviewComment(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user1")

	req := NewRequest(t, "GET", "/user2/repo1/pulls/3/files")
	session.MakeRequest(t, req, http.StatusOK)

	t.Run("single outdated review (line 1)", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/user2/repo1/pulls/3/files/reviews/new_comment")
		resp := session.MakeRequest(t, req, http.StatusOK)
		doc := NewHTMLParser(t, resp.Body)
		req = NewRequestWithValues(t, "POST", "/user2/repo1/pulls/3/files/reviews/comments", map[string]string{
			"_csrf":            doc.GetInputValueByName("_csrf"),
			"origin":           doc.GetInputValueByName("origin"),
			"latest_commit_id": doc.GetInputValueByName("latest_commit_id"),
			"side":             "proposed",
			"line":             "1",
			"path":             "iso-8859-1.txt",
			"diff_start_cid":   doc.GetInputValueByName("diff_start_cid"),
			"diff_end_cid":     doc.GetInputValueByName("diff_end_cid"),
			"diff_base_cid":    doc.GetInputValueByName("diff_base_cid"),
			"content":          "nitpicking comment",
			"pending_review":   "",
		})
		session.MakeRequest(t, req, http.StatusOK)

		req = NewRequestWithValues(t, "POST", "/user2/repo1/pulls/3/files/reviews/submit", map[string]string{
			"_csrf":     doc.GetInputValueByName("_csrf"),
			"commit_id": doc.GetInputValueByName("latest_commit_id"),
			"content":   "looks good",
			"type":      "comment",
		})
		session.MakeRequest(t, req, http.StatusOK)

		// retrieve comment_id by reloading the comment page
		req = NewRequest(t, "GET", "/user2/repo1/pulls/3")
		resp = session.MakeRequest(t, req, http.StatusOK)
		doc = NewHTMLParser(t, resp.Body)
		commentID, ok := doc.Find(`[data-action="Resolve"]`).Attr("data-comment-id")
		assert.True(t, ok)

		// adjust the database to mark the comment as invalidated
		// (to invalidate it properly, one should push a commit which should trigger this logic,
		// in the meantime, use this quick-and-dirty trick)
		comment := loadComment(t, commentID)
		require.NoError(t, issues_model.UpdateCommentInvalidate(t.Context(), &issues_model.Comment{
			ID:          comment.ID,
			Invalidated: true,
		}))

		req = NewRequestWithValues(t, "POST", "/user2/repo1/issues/resolve_conversation", map[string]string{
			"_csrf":      doc.GetInputValueByName("_csrf"),
			"origin":     "timeline",
			"action":     "Resolve",
			"comment_id": commentID,
		})
		resp = session.MakeRequest(t, req, http.StatusOK)

		// even on template error, the page returns HTTP 200
		// count the comments to ensure success.
		doc = NewHTMLParser(t, resp.Body)
		assert.Len(t, doc.Find(`.comments > .comment`).Nodes, 1)
	})

	t.Run("outdated and newer review (line 2)", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/user2/repo1/pulls/3/files/reviews/new_comment")
		resp := session.MakeRequest(t, req, http.StatusOK)
		doc := NewHTMLParser(t, resp.Body)

		var firstReviewID int64
		{
			// first (outdated) review
			req = NewRequestWithValues(t, "POST", "/user2/repo1/pulls/3/files/reviews/comments", map[string]string{
				"_csrf":            doc.GetInputValueByName("_csrf"),
				"origin":           doc.GetInputValueByName("origin"),
				"latest_commit_id": doc.GetInputValueByName("latest_commit_id"),
				"side":             "proposed",
				"line":             "2",
				"path":             "iso-8859-1.txt",
				"diff_start_cid":   doc.GetInputValueByName("diff_start_cid"),
				"diff_end_cid":     doc.GetInputValueByName("diff_end_cid"),
				"diff_base_cid":    doc.GetInputValueByName("diff_base_cid"),
				"content":          "nitpicking comment",
				"pending_review":   "",
			})
			session.MakeRequest(t, req, http.StatusOK)

			req = NewRequestWithValues(t, "POST", "/user2/repo1/pulls/3/files/reviews/submit", map[string]string{
				"_csrf":     doc.GetInputValueByName("_csrf"),
				"commit_id": doc.GetInputValueByName("latest_commit_id"),
				"content":   "looks good",
				"type":      "comment",
			})
			session.MakeRequest(t, req, http.StatusOK)

			// retrieve comment_id by reloading the comment page
			req = NewRequest(t, "GET", "/user2/repo1/pulls/3")
			resp = session.MakeRequest(t, req, http.StatusOK)
			doc = NewHTMLParser(t, resp.Body)
			commentID, ok := doc.Find(`[data-action="Resolve"]`).Attr("data-comment-id")
			assert.True(t, ok)

			// adjust the database to mark the comment as invalidated
			// (to invalidate it properly, one should push a commit which should trigger this logic,
			// in the meantime, use this quick-and-dirty trick)
			comment := loadComment(t, commentID)
			require.NoError(t, issues_model.UpdateCommentInvalidate(t.Context(), &issues_model.Comment{
				ID:          comment.ID,
				Invalidated: true,
			}))
			firstReviewID = comment.ReviewID
			assert.NotZero(t, firstReviewID)
		}

		// ID of the first comment for the second (up-to-date) review
		var commentID string

		{
			// second (up-to-date) review on the same line
			// make a second review
			req = NewRequestWithValues(t, "POST", "/user2/repo1/pulls/3/files/reviews/comments", map[string]string{
				"_csrf":            doc.GetInputValueByName("_csrf"),
				"origin":           doc.GetInputValueByName("origin"),
				"latest_commit_id": doc.GetInputValueByName("latest_commit_id"),
				"side":             "proposed",
				"line":             "2",
				"path":             "iso-8859-1.txt",
				"diff_start_cid":   doc.GetInputValueByName("diff_start_cid"),
				"diff_end_cid":     doc.GetInputValueByName("diff_end_cid"),
				"diff_base_cid":    doc.GetInputValueByName("diff_base_cid"),
				"content":          "nitpicking comment",
				"pending_review":   "",
			})
			session.MakeRequest(t, req, http.StatusOK)

			req = NewRequestWithValues(t, "POST", "/user2/repo1/pulls/3/files/reviews/submit", map[string]string{
				"_csrf":     doc.GetInputValueByName("_csrf"),
				"commit_id": doc.GetInputValueByName("latest_commit_id"),
				"content":   "looks better",
				"type":      "comment",
			})
			session.MakeRequest(t, req, http.StatusOK)

			// retrieve comment_id by reloading the comment page
			req = NewRequest(t, "GET", "/user2/repo1/pulls/3")
			resp = session.MakeRequest(t, req, http.StatusOK)
			doc = NewHTMLParser(t, resp.Body)

			commentIDs := doc.Find(`[data-action="Resolve"]`).Map(func(i int, elt *goquery.Selection) string {
				v, _ := elt.Attr("data-comment-id")
				return v
			})
			assert.Len(t, commentIDs, 2) // 1 for the outdated review, 1 for the current review

			// check that the first comment is for the previous review
			comment := loadComment(t, commentIDs[0])
			assert.Equal(t, comment.ReviewID, firstReviewID)

			// check that the second comment is for a different review
			comment = loadComment(t, commentIDs[1])
			assert.NotZero(t, comment.ReviewID)
			assert.NotEqual(t, comment.ReviewID, firstReviewID)

			commentID = commentIDs[1] // save commentID for later
		}

		req = NewRequestWithValues(t, "POST", "/user2/repo1/issues/resolve_conversation", map[string]string{
			"_csrf":      doc.GetInputValueByName("_csrf"),
			"origin":     "timeline",
			"action":     "Resolve",
			"comment_id": commentID,
		})
		resp = session.MakeRequest(t, req, http.StatusOK)

		// even on template error, the page returns HTTP 200
		// count the comments to ensure success.
		doc = NewHTMLParser(t, resp.Body)
		comments := doc.Find(`.comments > .comment`)
		assert.Len(t, comments.Nodes, 1) // the outdated comment belongs to another review and should not be shown
	})

	t.Run("Files Changed tab", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		for _, c := range []struct {
			style, outdated string
			expectedCount   int
		}{
			{"unified", "true", 3},  // 1 comment on line 1 + 2 comments on line 3
			{"unified", "false", 1}, // 1 comment on line 3 is not outdated
			{"split", "true", 3},    // 1 comment on line 1 + 2 comments on line 3
			{"split", "false", 1},   // 1 comment on line 3 is not outdated
		} {
			t.Run(c.style+"+"+c.outdated, func(t *testing.T) {
				req := NewRequest(t, "GET", "/user2/repo1/pulls/3/files?style="+c.style+"&show-outdated="+c.outdated)
				resp := session.MakeRequest(t, req, http.StatusOK)

				doc := NewHTMLParser(t, resp.Body)
				comments := doc.Find(`.comments > .comment`)
				assert.Len(t, comments.Nodes, c.expectedCount)
			})
		}
	})

	t.Run("Conversation tab", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/user2/repo1/pulls/3")
		resp := session.MakeRequest(t, req, http.StatusOK)

		doc := NewHTMLParser(t, resp.Body)
		comments := doc.Find(`.comments > .comment`)
		assert.Len(t, comments.Nodes, 3) // 1 comment on line 1 + 2 comments on line 3
	})
}

func TestPullView_CodeOwner(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

		repo, _, f := tests.CreateDeclarativeRepo(t, user2, "test_codeowner", nil, nil, []*files_service.ChangeRepoFile{
			{
				Operation:     "create",
				TreePath:      "CODEOWNERS",
				ContentReader: strings.NewReader("README.md @user5\n"),
			},
		})
		defer f()

		t.Run("First Pull Request", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// create a new branch to prepare for pull request
			err := updateFileInBranch(user2, repo, "README.md", "codeowner-basebranch",
				strings.NewReader("# This is a new project\n"),
			)
			require.NoError(t, err)

			// Create a pull request.
			session := loginUser(t, "user2")
			testPullCreate(t, session, "user2", "test_codeowner", false, repo.DefaultBranch, "codeowner-basebranch", "Test Pull Request")

			pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{BaseRepoID: repo.ID, HeadRepoID: repo.ID, HeadBranch: "codeowner-basebranch"})
			unittest.AssertExistsIf(t, true, &issues_model.Review{IssueID: pr.IssueID, Type: issues_model.ReviewTypeRequest, ReviewerID: 5})
			require.NoError(t, pr.LoadIssue(db.DefaultContext))

			err = issue_service.ChangeTitle(db.DefaultContext, pr.Issue, user2, "[WIP] Test Pull Request")
			require.NoError(t, err)
			prUpdated1 := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: pr.ID})
			require.NoError(t, prUpdated1.LoadIssue(db.DefaultContext))
			assert.Equal(t, "[WIP] Test Pull Request", prUpdated1.Issue.Title)

			err = issue_service.ChangeTitle(db.DefaultContext, prUpdated1.Issue, user2, "Test Pull Request2")
			require.NoError(t, err)
			prUpdated2 := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: pr.ID})
			require.NoError(t, prUpdated2.LoadIssue(db.DefaultContext))
			assert.Equal(t, "Test Pull Request2", prUpdated2.Issue.Title)
		})

		// change the default branch CODEOWNERS file to change README.md's codeowner
		err := updateFileInBranch(user2, repo, "CODEOWNERS", "",
			strings.NewReader("README.md @user8\n"),
		)
		require.NoError(t, err)

		t.Run("Second Pull Request", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// create a new branch to prepare for pull request
			err := updateFileInBranch(user2, repo, "README.md", "codeowner-basebranch2",
				strings.NewReader("# This is a new project2\n"),
			)
			require.NoError(t, err)

			// Create a pull request.
			session := loginUser(t, "user2")
			testPullCreate(t, session, "user2", "test_codeowner", false, repo.DefaultBranch, "codeowner-basebranch2", "Test Pull Request2")

			pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{BaseRepoID: repo.ID, HeadBranch: "codeowner-basebranch2"})
			unittest.AssertExistsIf(t, true, &issues_model.Review{IssueID: pr.IssueID, Type: issues_model.ReviewTypeRequest, ReviewerID: 8})
		})

		t.Run("Forked Repo Pull Request", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			user5 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
			forkedRepo, err := repo_service.ForkRepositoryAndUpdates(db.DefaultContext, user2, user5, repo_service.ForkRepoOptions{
				BaseRepo: repo,
				Name:     "test_codeowner_fork",
			})
			require.NoError(t, err)

			// create a new branch to prepare for pull request
			err = updateFileInBranch(user5, forkedRepo, "README.md", "codeowner-basebranch-forked",
				strings.NewReader("# This is a new forked project\n"),
			)
			require.NoError(t, err)

			session := loginUser(t, "user5")

			// create a pull request on the forked repository, code reviewers should not be mentioned
			testPullCreateDirectly(t, session, "user5", "test_codeowner_fork", forkedRepo.DefaultBranch, "", "", "codeowner-basebranch-forked", "Test Pull Request on Forked Repository")

			pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{BaseRepoID: forkedRepo.ID, HeadBranch: "codeowner-basebranch-forked"})
			unittest.AssertExistsIf(t, false, &issues_model.Review{IssueID: pr.IssueID, Type: issues_model.ReviewTypeRequest, ReviewerID: 8})

			// create a pull request to base repository, code reviewers should be mentioned
			testPullCreateDirectly(t, session, repo.OwnerName, repo.Name, repo.DefaultBranch, forkedRepo.OwnerName, forkedRepo.Name, "codeowner-basebranch-forked", "Test Pull Request3")

			pr = unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{BaseRepoID: repo.ID, HeadRepoID: forkedRepo.ID, HeadBranch: "codeowner-basebranch-forked"})
			unittest.AssertExistsIf(t, true, &issues_model.Review{IssueID: pr.IssueID, Type: issues_model.ReviewTypeRequest, ReviewerID: 8})
		})
	})
}

func TestPullView_GivenApproveOrRejectReviewOnClosedPR(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, giteaURL *url.URL) {
		user1Session := loginUser(t, "user1")
		user2Session := loginUser(t, "user2")

		// Have user1 create a fork of repo1.
		testRepoFork(t, user1Session, "user2", "repo1", "user1", "repo1")

		baseRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerName: "user2", Name: "repo1"})
		forkedRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerName: "user1", Name: "repo1"})
		baseGitRepo, err := gitrepo.OpenRepository(db.DefaultContext, baseRepo)
		require.NoError(t, err)
		defer baseGitRepo.Close()

		t.Run("Submit approve/reject review on merged PR", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Create a merged PR (made by user1) in the upstream repo1.
			testEditFile(t, user1Session, "user1", "repo1", "master", "README.md", "Hello, World (Edited)\n")
			resp := testPullCreate(t, user1Session, "user1", "repo1", false, "master", "master", "This is a pull title")
			elem := strings.Split(test.RedirectURL(resp), "/")
			assert.Equal(t, "pulls", elem[3])
			testPullMerge(t, user1Session, elem[1], elem[2], elem[4], repo_model.MergeStyleMerge, false)

			// Get the commit SHA
			pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{
				BaseRepoID: baseRepo.ID,
				BaseBranch: "master",
				HeadRepoID: forkedRepo.ID,
				HeadBranch: "master",
			})
			sha, err := baseGitRepo.GetRefCommitID(pr.GetGitRefName())
			require.NoError(t, err)

			// Grab the CSRF token.
			req := NewRequest(t, "GET", path.Join(elem[1], elem[2], "pulls", elem[4]))
			resp = user2Session.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			// Submit an approve review on the PR.
			testSubmitReview(t, user2Session, htmlDoc.GetCSRF(), "user2", "repo1", elem[4], sha, "approve", http.StatusOK)

			// Submit a reject review on the PR.
			testSubmitReview(t, user2Session, htmlDoc.GetCSRF(), "user2", "repo1", elem[4], sha, "reject", http.StatusOK)
		})

		t.Run("Submit approve/reject review on closed PR", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Created a closed PR (made by user1) in the upstream repo1.
			testEditFileToNewBranch(t, user1Session, "user1", "repo1", "master", "a-test-branch", "README.md", "Hello, World (Edited...again)\n")
			resp := testPullCreate(t, user1Session, "user1", "repo1", false, "master", "a-test-branch", "This is a pull title")
			elem := strings.Split(test.RedirectURL(resp), "/")
			assert.Equal(t, "pulls", elem[3])
			testIssueClose(t, user1Session, elem[1], elem[2], elem[4], true)

			// Get the commit SHA
			pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{
				BaseRepoID: baseRepo.ID,
				BaseBranch: "master",
				HeadRepoID: forkedRepo.ID,
				HeadBranch: "a-test-branch",
			})
			sha, err := baseGitRepo.GetRefCommitID(pr.GetGitRefName())
			require.NoError(t, err)

			// Grab the CSRF token.
			req := NewRequest(t, "GET", path.Join(elem[1], elem[2], "pulls", elem[4]))
			resp = user2Session.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			// Submit an approve review on the PR.
			testSubmitReview(t, user2Session, htmlDoc.GetCSRF(), "user2", "repo1", elem[4], sha, "approve", http.StatusOK)

			// Submit a reject review on the PR.
			testSubmitReview(t, user2Session, htmlDoc.GetCSRF(), "user2", "repo1", elem[4], sha, "reject", http.StatusOK)
		})
	})
}

func TestPullReviewInArchivedRepo(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, giteaURL *url.URL) {
		session := loginUser(t, "user2")

		// Open a PR
		testEditFileToNewBranch(t, session, "user2", "repo1", "master", "for-pr", "README.md", "Hi!\n")
		resp := testPullCreate(t, session, "user2", "repo1", true, "master", "for-pr", "PR title")
		elem := strings.Split(test.RedirectURL(resp), "/")

		t.Run("Review box normally", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// The "Finish review button" must be available
			resp = session.MakeRequest(t, NewRequest(t, "GET", path.Join(elem[1], elem[2], "pulls", elem[4], "files")), http.StatusOK)
			button := NewHTMLParser(t, resp.Body).Find("#review-box button")
			assert.False(t, button.HasClass("disabled"))
		})

		t.Run("Review box in archived repo", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Archive the repo
			resp = session.MakeRequest(t, NewRequestWithValues(t, "POST", path.Join(elem[1], elem[2], "settings"), map[string]string{
				"_csrf":  GetCSRF(t, session, path.Join(elem[1], elem[2], "settings")),
				"action": "archive",
			}), http.StatusSeeOther)

			// The "Finish review button" must be disabled
			resp = session.MakeRequest(t, NewRequest(t, "GET", path.Join(elem[1], elem[2], "pulls", elem[4], "files")), http.StatusOK)
			button := NewHTMLParser(t, resp.Body).Find("#review-box button")
			assert.True(t, button.HasClass("disabled"))
		})
	})
}

func testNofiticationCount(t *testing.T, session *TestSession, csrf string, expectedSubmitStatus int) *httptest.ResponseRecorder {
	options := map[string]string{
		"_csrf": csrf,
	}

	req := NewRequestWithValues(t, "GET", "/", options)
	return session.MakeRequest(t, req, expectedSubmitStatus)
}

func testAssignReviewer(t *testing.T, session *TestSession, csrf, owner, repo, pullID, reviewer string, expectedSubmitStatus int) *httptest.ResponseRecorder {
	options := map[string]string{
		"_csrf":     csrf,
		"action":    "attach",
		"issue_ids": pullID,
		"id":        reviewer,
	}

	submitURL := path.Join(owner, repo, "issues", "request_review")
	req := NewRequestWithValues(t, "POST", submitURL, options)
	return session.MakeRequest(t, req, expectedSubmitStatus)
}

func testSubmitReview(t *testing.T, session *TestSession, csrf, owner, repo, pullNumber, commitID, reviewType string, expectedSubmitStatus int) *httptest.ResponseRecorder {
	options := map[string]string{
		"_csrf":     csrf,
		"commit_id": commitID,
		"content":   "test",
		"type":      reviewType,
	}

	submitURL := path.Join(owner, repo, "pulls", pullNumber, "files", "reviews", "submit")
	req := NewRequestWithValues(t, "POST", submitURL, options)
	return session.MakeRequest(t, req, expectedSubmitStatus)
}

func testIssueClose(t *testing.T, session *TestSession, owner, repo, issueNumber string, isPull bool) *httptest.ResponseRecorder {
	issueType := "issues"
	if isPull {
		issueType = "pulls"
	}
	req := NewRequest(t, "GET", path.Join(owner, repo, issueType, issueNumber))
	resp := session.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	closeURL := path.Join(owner, repo, "issues", issueNumber, "comments")

	options := map[string]string{
		"_csrf":  htmlDoc.GetCSRF(),
		"status": "close",
	}

	req = NewRequestWithValues(t, "POST", closeURL, options)
	return session.MakeRequest(t, req, http.StatusOK)
}

func getUserNotificationCount(t *testing.T, session *TestSession, csrf string) string {
	resp := testNofiticationCount(t, session, csrf, http.StatusOK)
	doc := NewHTMLParser(t, resp.Body)
	return doc.Find(`.notification_count`).Text()
}

func TestPullRequestReplyMail(t *testing.T) {
	defer unittest.OverrideFixtures("tests/integration/fixtures/TestPullRequestReplyMail")()
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	session := loginUser(t, user.Name)

	t.Run("Reply to pending review comment", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		called := false
		defer test.MockVariableValue(&mailer.SendAsync, func(...*mailer.Message) {
			called = true
		})()

		review := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 1002}, "type = 0")

		req := NewRequestWithValues(t, "POST", "/user2/repo1/pulls/2/files/reviews/comments", map[string]string{
			"_csrf":   GetCSRF(t, session, "/user2/repo1/pulls/2"),
			"origin":  "diff",
			"content": "Just a comment!",
			"side":    "proposed",
			"line":    "4",
			"path":    "README.md",
			"reply":   strconv.FormatInt(review.ID, 10),
		})
		session.MakeRequest(t, req, http.StatusOK)

		assert.False(t, called)
		unittest.AssertExistsIf(t, true, &issues_model.Comment{Content: "Just a comment!", ReviewID: review.ID, IssueID: 2})
	})

	t.Run("Start a review", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		called := false
		defer test.MockVariableValue(&mailer.SendAsync, func(msgs ...*mailer.Message) {
			called = true
		})()

		req := NewRequestWithValues(t, "POST", "/user2/repo1/pulls/2/files/reviews/comments", map[string]string{
			"_csrf":   GetCSRF(t, session, "/user2/repo1/pulls/2"),
			"origin":  "diff",
			"content": "Notification time 2!",
			"side":    "proposed",
			"line":    "2",
			"path":    "README.md",
		})
		session.MakeRequest(t, req, http.StatusOK)

		assert.False(t, called)
		unittest.AssertExistsIf(t, true, &issues_model.Comment{Content: "Notification time 2!", IssueID: 2})
	})

	t.Run("Create a single comment", func(t *testing.T) {
		t.Run("As a reply", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			called := false
			defer test.MockVariableValue(&mailer.SendAsync, func(msgs ...*mailer.Message) {
				assert.Len(t, msgs, 2)
				assert.Equal(t, "user1@example.com", msgs[0].To)
				assert.Equal(t, "Re: [user2/repo1] issue2 (PR #2)", msgs[0].Subject)
				assert.Contains(t, msgs[0].Body, "Notification time!")
				called = true
			})()

			review := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 1001, Type: issues_model.ReviewTypeComment})

			req := NewRequestWithValues(t, "POST", "/user2/repo1/pulls/2/files/reviews/comments", map[string]string{
				"_csrf":   GetCSRF(t, session, "/user2/repo1/pulls/2"),
				"origin":  "diff",
				"content": "Notification time!",
				"side":    "proposed",
				"line":    "3",
				"path":    "README.md",
				"reply":   strconv.FormatInt(review.ID, 10),
			})
			session.MakeRequest(t, req, http.StatusOK)

			assert.True(t, called)
			unittest.AssertExistsIf(t, true, &issues_model.Comment{Content: "Notification time!", ReviewID: review.ID, IssueID: 2})
		})
		t.Run("On a new line", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			called := false
			defer test.MockVariableValue(&mailer.SendAsync, func(msgs ...*mailer.Message) {
				assert.Len(t, msgs, 2)
				assert.Equal(t, "user1@example.com", msgs[0].To)
				assert.Equal(t, "Re: [user2/repo1] issue2 (PR #2)", msgs[0].Subject)
				assert.Contains(t, msgs[0].Body, "Notification time 2!")
				called = true
			})()

			req := NewRequestWithValues(t, "POST", "/user2/repo1/pulls/2/files/reviews/comments", map[string]string{
				"_csrf":         GetCSRF(t, session, "/user2/repo1/pulls/2"),
				"origin":        "diff",
				"content":       "Notification time 2!",
				"side":          "proposed",
				"line":          "5",
				"path":          "README.md",
				"single_review": "true",
			})
			session.MakeRequest(t, req, http.StatusOK)

			assert.True(t, called)
			unittest.AssertExistsIf(t, true, &issues_model.Comment{Content: "Notification time 2!", IssueID: 2})
		})
	})
}

func updateFileInBranch(user *user_model.User, repo *repo_model.Repository, treePath, newBranch string, content io.ReadSeeker) error {
	oldBranch, err := gitrepo.GetDefaultBranch(git.DefaultContext, repo)
	if err != nil {
		return err
	}

	commitID, err := gitrepo.GetBranchCommitID(git.DefaultContext, repo, oldBranch)
	if err != nil {
		return err
	}

	opts := &files_service.ChangeRepoFilesOptions{
		Files: []*files_service.ChangeRepoFile{
			{
				Operation:     "update",
				TreePath:      treePath,
				ContentReader: content,
			},
		},
		OldBranch:    oldBranch,
		NewBranch:    newBranch,
		Author:       nil,
		Committer:    nil,
		LastCommitID: commitID,
	}
	_, err = files_service.ChangeRepoFiles(git.DefaultContext, repo, user, opts)
	return err
}
