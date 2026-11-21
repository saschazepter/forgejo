// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/json"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	issue_service "forgejo.org/services/issue"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"xorm.io/builder"
)

func TestAPIPullReviewCreateDeleteComment(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	pullIssue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 3})
	require.NoError(t, pullIssue.LoadAttributes(db.DefaultContext))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: pullIssue.RepoID})

	username := "user2"
	session := loginUser(t, username)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

	// as of e522e774cae2240279fc48c349fc513c9d3353ee
	// There should be no reason for CreateComment to behave differently
	// depending on the event associated with the review. But the logic of the implementation
	// at this point in time is very involved and deserves these seemingly redundant
	// test.
	for _, event := range []api.ReviewStateType{
		api.ReviewStatePending,
		api.ReviewStateRequestChanges,
		api.ReviewStateApproved,
		api.ReviewStateComment,
	} {
		t.Run("Event_"+string(event), func(t *testing.T) {
			path := "README.md"
			var review api.PullReview
			var reviewLine int64 = 1

			// cleanup
			{
				session := loginUser(t, "user1")
				token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeAll)

				req := NewRequestf(t, http.MethodGet, "/api/v1/repos/%s/pulls/%d/reviews", repo.FullName(), pullIssue.Index).AddTokenAuth(token)
				resp := MakeRequest(t, req, http.StatusOK)
				var reviews []*api.PullReview
				DecodeJSON(t, resp, &reviews)
				for _, review := range reviews {
					if review.State == api.ReviewStateRequestReview {
						continue
					}
					req := NewRequestf(t, http.MethodDelete, "/api/v1/repos/%s/pulls/%d/reviews/%d", repo.FullName(), pullIssue.Index, review.ID).
						AddTokenAuth(token)
					MakeRequest(t, req, http.StatusNoContent)
				}
			}

			requireReviewCount := func(count int) {
				req := NewRequestf(t, http.MethodGet, "/api/v1/repos/%s/pulls/%d/reviews", repo.FullName(), pullIssue.Index).AddTokenAuth(token)
				resp := MakeRequest(t, req, http.StatusOK)
				var reviews []*api.PullReview
				DecodeJSON(t, resp, &reviews)
				require.Len(t, reviews, count)
			}

			{
				req := NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/pulls/%d/reviews", repo.FullName(), pullIssue.Index), &api.CreatePullReviewOptions{
					Body:  "body1",
					Event: event,
				}).AddTokenAuth(token)
				resp := MakeRequest(t, req, http.StatusOK)
				DecodeJSON(t, resp, &review)
				require.EqualValues(t, string(event), review.State)
				require.Equal(t, 0, review.CodeCommentsCount)
			}

			{
				req := NewRequestf(t, http.MethodGet, "/api/v1/repos/%s/pulls/%d/reviews/%d", repo.FullName(), pullIssue.Index, review.ID).
					AddTokenAuth(token)
				resp := MakeRequest(t, req, http.StatusOK)
				var getReview api.PullReview
				DecodeJSON(t, resp, &getReview)
				require.Equal(t, getReview, review)
			}
			requireReviewCount(2)

			newCommentBody := "first new line"
			var reviewComment api.PullReviewComment

			{
				req := NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/pulls/%d/reviews/%d/comments", repo.FullName(), pullIssue.Index, review.ID), &api.CreatePullReviewCommentOptions{
					Path:       path,
					Body:       newCommentBody,
					OldLineNum: reviewLine,
				}).AddTokenAuth(token)
				resp := MakeRequest(t, req, http.StatusOK)
				DecodeJSON(t, resp, &reviewComment)
				assert.Equal(t, review.ID, reviewComment.ReviewID)
				assert.Equal(t, newCommentBody, reviewComment.Body)
				// we sent OldLineNum: 1, but that line of code isn't modified in this PR, triggering the PR's logic
				// which standardizes comments on non-modified lines of code to be on the right-hand-side of the diff:
				assert.EqualValues(t, 0, reviewComment.OldLineNum)
				assert.EqualValues(t, reviewLine, reviewComment.LineNum)
				assert.Equal(t, path, reviewComment.Path)
			}

			{
				req := NewRequestf(t, http.MethodGet, "/api/v1/repos/%s/pulls/%d/reviews/%d/comments/%d", repo.FullName(), pullIssue.Index, review.ID, reviewComment.ID).
					AddTokenAuth(token)
				resp := MakeRequest(t, req, http.StatusOK)

				var comment api.PullReviewComment
				DecodeJSON(t, resp, &comment)
				assert.Equal(t, reviewComment, comment)
			}

			{
				req := NewRequestf(t, http.MethodDelete, "/api/v1/repos/%s/pulls/%d/reviews/%d/comments/%d", repo.FullName(), pullIssue.Index, review.ID, reviewComment.ID).
					AddTokenAuth(token)
				MakeRequest(t, req, http.StatusNoContent)
			}

			{
				req := NewRequestf(t, http.MethodGet, "/api/v1/repos/%s/pulls/%d/reviews/%d/comments/%d", repo.FullName(), pullIssue.Index, review.ID, reviewComment.ID).
					AddTokenAuth(token)
				MakeRequest(t, req, http.StatusNotFound)
			}

			{
				req := NewRequestf(t, http.MethodDelete, "/api/v1/repos/%s/pulls/%d/reviews/%d", repo.FullName(), pullIssue.Index, review.ID).
					AddTokenAuth(token)
				MakeRequest(t, req, http.StatusNoContent)
			}
			requireReviewCount(1)
		})
	}
}

func TestAPIPullReviewCommentReply(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	pullIssue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 3})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: pullIssue.RepoID})
	pullURL := fmt.Sprintf("/api/v1/repos/%s/pulls/%d", repo.FullName(), pullIssue.Index)

	session := loginUser(t, "user2")
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteIssue)

	createReview := func(t *testing.T, event api.ReviewStateType) (api.PullReview, *api.PullReviewComment) {
		t.Helper()
		var review api.PullReview
		options := &api.CreatePullReviewOptions{
			Body:     "review",
			Event:    event,
			Comments: []api.CreatePullReviewComment{{Path: "README.md", Body: "parent review comment", NewLineNum: 1}},
		}
		req := NewRequestWithJSON(t, http.MethodPost, pullURL+"/reviews", options).AddTokenAuth(token)
		DecodeJSON(t, MakeRequest(t, req, http.StatusOK), &review)

		var comments []*api.PullReviewComment
		req = NewRequestf(t, http.MethodGet, "%s/reviews/%d/comments", pullURL, review.ID).AddTokenAuth(token)
		DecodeJSON(t, MakeRequest(t, req, http.StatusOK), &comments)
		require.Len(t, comments, 1)
		return review, comments[0]
	}
	replyRequest := func(t *testing.T, url string, commentID int64, body string) *RequestWrapper {
		t.Helper()
		options := &api.CreatePullReviewCommentReplyOptions{Body: body}
		return NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("%s/comments/%d/replies", url, commentID), options)
	}

	review, parentComment := createReview(t, api.ReviewStateComment)

	t.Run("Reply joins the conversation", func(t *testing.T) {
		replyBody := "reply to parent review comment"
		var reply api.PullReviewComment
		req := replyRequest(t, pullURL, parentComment.ID, replyBody).AddTokenAuth(token)
		DecodeJSON(t, MakeRequest(t, req, http.StatusCreated), &reply)
		assert.Equal(t, replyBody, reply.Body)
		assert.Equal(t, parentComment.ReviewID, reply.ReviewID)
		assert.Equal(t, parentComment.Path, reply.Path)
		assert.Equal(t, parentComment.LineNum, reply.LineNum)
		assert.Equal(t, parentComment.OldLineNum, reply.OldLineNum)
		assert.Equal(t, parentComment.ExtraLinesCount, reply.ExtraLinesCount)

		var reviewComments []*api.PullReviewComment
		req = NewRequestf(t, http.MethodGet, "%s/reviews/%d/comments", pullURL, review.ID).AddTokenAuth(token)
		DecodeJSON(t, MakeRequest(t, req, http.StatusOK), &reviewComments)
		assert.Len(t, reviewComments, 2)
	})

	t.Run("Comment of another pull request", func(t *testing.T) {
		otherPullURL := fmt.Sprintf("/api/v1/repos/%s/pulls/%d", repo.FullName(), pullIssue.Index+1)
		req := replyRequest(t, otherPullURL, parentComment.ID, "reply").AddTokenAuth(token)
		MakeRequest(t, req, http.StatusNotFound)
	})

	t.Run("Not a review comment", func(t *testing.T) {
		var plainComment api.Comment
		options := &api.CreateIssueCommentOption{Body: "plain pull conversation comment"}
		issueCommentsURL := fmt.Sprintf("/api/v1/repos/%s/issues/%d/comments", repo.FullName(), pullIssue.Index)
		req := NewRequestWithJSON(t, http.MethodPost, issueCommentsURL, options).AddTokenAuth(token)
		DecodeJSON(t, MakeRequest(t, req, http.StatusCreated), &plainComment)

		req = replyRequest(t, pullURL, plainComment.ID, "reply").AddTokenAuth(token)
		MakeRequest(t, req, http.StatusBadRequest)
	})

	t.Run("Empty body", func(t *testing.T) {
		req := replyRequest(t, pullURL, parentComment.ID, "").AddTokenAuth(token)
		MakeRequest(t, req, http.StatusUnprocessableEntity)
	})

	t.Run("Pending review", func(t *testing.T) {
		_, pendingComment := createReview(t, api.ReviewStatePending)

		t.Run("Reviewer can reply", func(t *testing.T) {
			req := replyRequest(t, pullURL, pendingComment.ID, "note to self").AddTokenAuth(token)
			MakeRequest(t, req, http.StatusCreated)
		})

		t.Run("Site admin cannot reply", func(t *testing.T) {
			adminToken := getTokenForLoggedInUser(t, loginUser(t, "user1"), auth_model.AccessTokenScopeWriteRepository)
			req := replyRequest(t, pullURL, pendingComment.ID, "reply").AddTokenAuth(adminToken)
			MakeRequest(t, req, http.StatusNotFound)
		})
	})
}

func TestAPIPullReviewMultiLineComment(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	pullIssue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 3})
	require.NoError(t, pullIssue.LoadAttributes(db.DefaultContext))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: pullIssue.RepoID})

	session := loginUser(t, "user2")
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

	// Create a review with a multi-line comment (extra_lines_count > 0)
	var review api.PullReview
	req := NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews", repo.OwnerName, repo.Name, pullIssue.Index), &api.CreatePullReviewOptions{
		Body:  "multi-line review",
		Event: "COMMENT",
		Comments: []api.CreatePullReviewComment{
			{
				Path:            "README.md",
				Body:            "multi-line comment on lines 1-3",
				NewLineNum:      1,
				ExtraLinesCount: 2,
			},
		},
	}).AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &review)
	assert.EqualValues(t, "COMMENT", review.State)
	assert.Equal(t, 1, review.CodeCommentsCount)

	// Get the review comments and verify extra_lines_count is returned
	req = NewRequestf(t, http.MethodGet, "/api/v1/repos/%s/%s/pulls/%d/reviews/%d/comments", repo.OwnerName, repo.Name, pullIssue.Index, review.ID).
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	var reviewComments []*api.PullReviewComment
	DecodeJSON(t, resp, &reviewComments)
	require.Len(t, reviewComments, 1)
	assert.EqualValues(t, 2, reviewComments[0].ExtraLinesCount)
	assert.EqualValues(t, 1, reviewComments[0].LineNum)
	assert.Equal(t, "multi-line comment on lines 1-3", reviewComments[0].Body)

	// Create a review with a single-line comment (extra_lines_count = 0, backward compat)
	var review2 api.PullReview
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews", repo.OwnerName, repo.Name, pullIssue.Index), &api.CreatePullReviewOptions{
		Body:  "single-line review",
		Event: "COMMENT",
		Comments: []api.CreatePullReviewComment{
			{
				Path:       "README.md",
				Body:       "single-line comment",
				NewLineNum: 1,
			},
		},
	}).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &review2)

	// Get the review comments and verify extra_lines_count is 0
	req = NewRequestf(t, http.MethodGet, "/api/v1/repos/%s/%s/pulls/%d/reviews/%d/comments", repo.OwnerName, repo.Name, pullIssue.Index, review2.ID).
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	var singleComments []*api.PullReviewComment
	DecodeJSON(t, resp, &singleComments)
	require.Len(t, singleComments, 1)
	assert.EqualValues(t, 0, singleComments[0].ExtraLinesCount)

	// Create a review with negative extra_lines_count → should be rejected
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews", repo.OwnerName, repo.Name, pullIssue.Index), &api.CreatePullReviewOptions{
		Body:  "negative review",
		Event: "COMMENT",
		Comments: []api.CreatePullReviewComment{
			{
				Path:            "README.md",
				Body:            "negative extra_lines_count",
				NewLineNum:      1,
				ExtraLinesCount: -1,
			},
		},
	}).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusUnprocessableEntity)

	// Create a review with a range exceeding setting.UI.MaxCodeCommentLines → should be rejected
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews", repo.OwnerName, repo.Name, pullIssue.Index), &api.CreatePullReviewOptions{
		Body:  "too long review",
		Event: "COMMENT",
		Comments: []api.CreatePullReviewComment{
			{
				Path:            "README.md",
				Body:            "extra_lines_count over the limit",
				NewLineNum:      1,
				ExtraLinesCount: int64(setting.UI.MaxCodeCommentLines), // range spans MaxCodeCommentLines+1 lines
			},
		},
	}).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusUnprocessableEntity)

	// Cleanup
	req = NewRequestf(t, http.MethodDelete, "/api/v1/repos/%s/%s/pulls/%d/reviews/%d", repo.OwnerName, repo.Name, pullIssue.Index, review.ID).
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusNoContent)
	req = NewRequestf(t, http.MethodDelete, "/api/v1/repos/%s/%s/pulls/%d/reviews/%d", repo.OwnerName, repo.Name, pullIssue.Index, review2.ID).
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusNoContent)
}

func TestAPIPullReviewList(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	pullIssue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 3})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: pullIssue.RepoID})
	reviewsURL := fmt.Sprintf("/api/v1/repos/%s/pulls/%d/reviews", repo.FullName(), pullIssue.Index)

	// Review 6 belongs to user2 and is pending. It sorts between reviews 5 and 7.
	allReviews := []int64{5, 6, 7, 8, 9, 10, 21, 22}
	visibleReviews := []int64{5, 7, 8, 9, 10, 21, 22}
	checkReviews := func(t *testing.T, req *RequestWrapper, total int, wantIDs []int64) {
		t.Helper()
		resp := MakeRequest(t, req, http.StatusOK)
		assert.Equal(t, fmt.Sprint(total), resp.Header().Get("X-Total-Count"))

		var reviews []api.PullReview
		DecodeJSON(t, resp, &reviews)
		ids := make([]int64, 0, len(reviews))
		for _, review := range reviews {
			ids = append(ids, review.ID)
			if review.ID == 6 {
				assert.Equal(t, api.ReviewStatePending, review.State)
			}
		}
		assert.Equal(t, wantIDs, ids)
	}

	for _, test := range []struct {
		name     string
		username string
		wantIDs  []int64
	}{
		{"Reviewer", "user2", allReviews},
		{"Administrator", "user1", allReviews},
		{"Other user", "user4", visibleReviews},
		{"Anonymous", "", visibleReviews},
	} {
		t.Run(test.name, func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			req := NewRequest(t, http.MethodGet, reviewsURL)
			if test.username != "" {
				token := getTokenForLoggedInUser(t, loginUser(t, test.username), auth_model.AccessTokenScopeReadRepository)
				req.AddTokenAuth(token)
			}
			checkReviews(t, req, len(test.wantIDs), test.wantIDs)
		})
	}

	t.Run("Pagination", func(t *testing.T) {
		token := getTokenForLoggedInUser(t, loginUser(t, "user4"), auth_model.AccessTokenScopeReadRepository)
		for page, wantIDs := range [][]int64{{5, 7, 8}, {9, 10, 21}, {22}, {}} {
			req := NewRequestf(t, http.MethodGet, "%s?page=%d&limit=3", reviewsURL, page+1).AddTokenAuth(token)
			checkReviews(t, req, len(visibleReviews), wantIDs)
		}
	})
}

func TestAPIPullReview(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	pullIssue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 3})
	require.NoError(t, pullIssue.LoadAttributes(db.DefaultContext))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: pullIssue.RepoID})

	// test ListPullReviews
	session := loginUser(t, "user2")
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
	req := NewRequestf(t, http.MethodGet, "/api/v1/repos/%s/%s/pulls/%d/reviews", repo.OwnerName, repo.Name, pullIssue.Index).
		AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)

	var reviews []*api.PullReview
	DecodeJSON(t, resp, &reviews)
	if !assert.Len(t, reviews, 8) {
		return
	}
	for _, r := range reviews {
		assert.Equal(t, pullIssue.HTMLURL(), r.HTMLPullURL)
	}
	assert.EqualValues(t, 8, reviews[3].ID)
	assert.EqualValues(t, "APPROVED", reviews[3].State)
	assert.Equal(t, 0, reviews[3].CodeCommentsCount)
	assert.True(t, reviews[3].Stale)
	assert.False(t, reviews[3].Official)

	assert.EqualValues(t, 10, reviews[5].ID)
	assert.EqualValues(t, "REQUEST_CHANGES", reviews[5].State)
	assert.Equal(t, 1, reviews[5].CodeCommentsCount)
	assert.EqualValues(t, -1, reviews[5].Reviewer.ID) // ghost user
	assert.False(t, reviews[5].Stale)
	assert.True(t, reviews[5].Official)

	// test GetPullReview
	req = NewRequestf(t, http.MethodGet, "/api/v1/repos/%s/%s/pulls/%d/reviews/%d", repo.OwnerName, repo.Name, pullIssue.Index, reviews[3].ID).
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	var review api.PullReview
	DecodeJSON(t, resp, &review)
	assert.Equal(t, *reviews[3], review)

	req = NewRequestf(t, "GET", "/api/v1/repos/%s/%s/pulls/%d/reviews/%d", repo.OwnerName, repo.Name, pullIssue.Index, reviews[5].ID).
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &review)
	assert.Equal(t, *reviews[5], review)

	// test GetPullReviewComments
	comment := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: 7})
	req = NewRequestf(t, http.MethodGet, "/api/v1/repos/%s/%s/pulls/%d/reviews/%d/comments", repo.OwnerName, repo.Name, pullIssue.Index, 10).
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	var reviewComments []*api.PullReviewComment
	DecodeJSON(t, resp, &reviewComments)
	assert.Len(t, reviewComments, 1)
	assert.Equal(t, "Ghost", reviewComments[0].Poster.UserName)
	assert.Equal(t, "a review from a deleted user", reviewComments[0].Body)
	assert.Equal(t, comment.ID, reviewComments[0].ID)
	assert.EqualValues(t, comment.UpdatedUnix, reviewComments[0].Updated.Unix())
	assert.Equal(t, comment.HTMLURL(db.DefaultContext), reviewComments[0].HTMLURL)

	// test CreatePullReview
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews", repo.OwnerName, repo.Name, pullIssue.Index), &api.CreatePullReviewOptions{
		Body: "body1",
		// Event: "" # will result in PENDING
		Comments: []api.CreatePullReviewComment{
			{
				Path:       "README.md",
				Body:       "first new line",
				OldLineNum: 0,
				NewLineNum: 1,
			}, {
				Path:       "README.md",
				Body:       "first old line",
				OldLineNum: 1,
				NewLineNum: 0,
			}, {
				Path:       "iso-8859-1.txt",
				Body:       "this line contains a non-utf-8 character",
				OldLineNum: 0,
				NewLineNum: 1,
			},
		},
	}).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &review)
	assert.EqualValues(t, 6, review.ID)
	assert.EqualValues(t, "PENDING", review.State)
	assert.Equal(t, 3, review.CodeCommentsCount)

	// test SubmitPullReview
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews/%d", repo.OwnerName, repo.Name, pullIssue.Index, review.ID), &api.SubmitPullReviewOptions{
		Event: "APPROVED",
		Body:  "just two nits",
	}).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &review)
	assert.EqualValues(t, 6, review.ID)
	assert.EqualValues(t, "APPROVED", review.State)
	assert.Equal(t, 3, review.CodeCommentsCount)

	// test dismiss review
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews/%d/dismissals", repo.OwnerName, repo.Name, pullIssue.Index, review.ID), &api.DismissPullReviewOptions{
		Message: "test",
	}).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &review)
	assert.EqualValues(t, 6, review.ID)
	assert.True(t, review.Dismissed)

	// test dismiss review
	req = NewRequest(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews/%d/undismissals", repo.OwnerName, repo.Name, pullIssue.Index, review.ID)).
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &review)
	assert.EqualValues(t, 6, review.ID)
	assert.False(t, review.Dismissed)

	// test DeletePullReview
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews", repo.OwnerName, repo.Name, pullIssue.Index), &api.CreatePullReviewOptions{
		Body:  "just a comment",
		Event: "COMMENT",
	}).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &review)
	assert.EqualValues(t, "COMMENT", review.State)
	assert.Equal(t, 0, review.CodeCommentsCount)
	req = NewRequestf(t, http.MethodDelete, "/api/v1/repos/%s/%s/pulls/%d/reviews/%d", repo.OwnerName, repo.Name, pullIssue.Index, review.ID).
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusNoContent)

	// test CreatePullReview Comment without body but with comments
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews", repo.OwnerName, repo.Name, pullIssue.Index), &api.CreatePullReviewOptions{
		// Body:  "",
		Event: "COMMENT",
		Comments: []api.CreatePullReviewComment{
			{
				Path:       "README.md",
				Body:       "first new line",
				OldLineNum: 0,
				NewLineNum: 1,
			}, {
				Path:       "README.md",
				Body:       "first old line",
				OldLineNum: 1,
				NewLineNum: 0,
			},
		},
	}).AddTokenAuth(token)
	var commentReview api.PullReview

	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &commentReview)
	assert.EqualValues(t, "COMMENT", commentReview.State)
	assert.Equal(t, 2, commentReview.CodeCommentsCount)
	assert.Empty(t, commentReview.Body)
	assert.False(t, commentReview.Dismissed)

	// test CreatePullReview Comment with body but without comments
	commentBody := "This is a body of the comment."
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews", repo.OwnerName, repo.Name, pullIssue.Index), &api.CreatePullReviewOptions{
		Body:     commentBody,
		Event:    "COMMENT",
		Comments: []api.CreatePullReviewComment{},
	}).AddTokenAuth(token)

	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &commentReview)
	assert.EqualValues(t, "COMMENT", commentReview.State)
	assert.Equal(t, 0, commentReview.CodeCommentsCount)
	assert.Equal(t, commentBody, commentReview.Body)
	assert.False(t, commentReview.Dismissed)

	// test CreatePullReview Comment without body and no comments
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews", repo.OwnerName, repo.Name, pullIssue.Index), &api.CreatePullReviewOptions{
		Body:     "",
		Event:    "COMMENT",
		Comments: []api.CreatePullReviewComment{},
	}).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusUnprocessableEntity)
	errMap := make(map[string]any)
	json.Unmarshal(resp.Body.Bytes(), &errMap)
	assert.Equal(t, "review event COMMENT requires a body or a comment", errMap["message"].(string))

	// test get review requests
	// to make it simple, use same api with get review
	pullIssue12 := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 12})
	require.NoError(t, pullIssue12.LoadAttributes(db.DefaultContext))
	repo3 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: pullIssue12.RepoID})

	req = NewRequestf(t, http.MethodGet, "/api/v1/repos/%s/%s/pulls/%d/reviews", repo3.OwnerName, repo3.Name, pullIssue12.Index).
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &reviews)
	assert.EqualValues(t, 11, reviews[0].ID)
	assert.EqualValues(t, "REQUEST_REVIEW", reviews[0].State)
	assert.Equal(t, 0, reviews[0].CodeCommentsCount)
	assert.False(t, reviews[0].Stale)
	assert.True(t, reviews[0].Official)
	assert.Equal(t, "test_team", reviews[0].ReviewerTeam.Name)

	assert.EqualValues(t, 12, reviews[1].ID)
	assert.EqualValues(t, "REQUEST_REVIEW", reviews[1].State)
	assert.Equal(t, 0, reviews[0].CodeCommentsCount)
	assert.False(t, reviews[1].Stale)
	assert.True(t, reviews[1].Official)
	assert.EqualValues(t, 1, reviews[1].Reviewer.ID)
}

func TestAPIPullReviewRequest(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	pullIssue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 3})
	require.NoError(t, pullIssue.LoadAttributes(db.DefaultContext))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: pullIssue.RepoID})

	// Test add Review Request
	session := loginUser(t, "user2")
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
	req := NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", repo.OwnerName, repo.Name, pullIssue.Index), &api.PullReviewRequestOptions{
		Reviewers: []string{"user4@example.com", "user8"},
	}).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusCreated)

	// poster of pr can't be reviewer
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", repo.OwnerName, repo.Name, pullIssue.Index), &api.PullReviewRequestOptions{
		Reviewers: []string{"user1"},
	}).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusUnprocessableEntity)

	// test user not exist
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", repo.OwnerName, repo.Name, pullIssue.Index), &api.PullReviewRequestOptions{
		Reviewers: []string{"testOther"},
	}).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusNotFound)

	// Test Remove Review Request
	session2 := loginUser(t, "user4")
	token2 := getTokenForLoggedInUser(t, session2, auth_model.AccessTokenScopeWriteRepository)

	req = NewRequestWithJSON(t, http.MethodDelete, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", repo.OwnerName, repo.Name, pullIssue.Index), &api.PullReviewRequestOptions{
		Reviewers: []string{"user4"},
	}).AddTokenAuth(token2)
	MakeRequest(t, req, http.StatusNoContent)

	// doer is not admin
	req = NewRequestWithJSON(t, http.MethodDelete, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", repo.OwnerName, repo.Name, pullIssue.Index), &api.PullReviewRequestOptions{
		Reviewers: []string{"user8"},
	}).AddTokenAuth(token2)
	MakeRequest(t, req, http.StatusUnprocessableEntity)

	req = NewRequestWithJSON(t, http.MethodDelete, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", repo.OwnerName, repo.Name, pullIssue.Index), &api.PullReviewRequestOptions{
		Reviewers: []string{"user8"},
	}).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusNoContent)

	// a collaborator can add/remove a review request
	pullIssue21 := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 21})
	require.NoError(t, pullIssue21.LoadAttributes(db.DefaultContext))
	pull21Repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: pullIssue21.RepoID}) // repo60
	user38Session := loginUser(t, "user38")
	user38Token := getTokenForLoggedInUser(t, user38Session, auth_model.AccessTokenScopeWriteRepository)
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", pull21Repo.OwnerName, pull21Repo.Name, pullIssue21.Index), &api.PullReviewRequestOptions{
		Reviewers: []string{"user4@example.com"},
	}).AddTokenAuth(user38Token)
	MakeRequest(t, req, http.StatusCreated)

	req = NewRequestWithJSON(t, http.MethodDelete, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", pull21Repo.OwnerName, pull21Repo.Name, pullIssue21.Index), &api.PullReviewRequestOptions{
		Reviewers: []string{"user4@example.com"},
	}).AddTokenAuth(user38Token)
	MakeRequest(t, req, http.StatusNoContent)

	// the poster of the PR can add/remove a review request
	user39Session := loginUser(t, "user39")
	user39Token := getTokenForLoggedInUser(t, user39Session, auth_model.AccessTokenScopeWriteRepository)
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", pull21Repo.OwnerName, pull21Repo.Name, pullIssue21.Index), &api.PullReviewRequestOptions{
		Reviewers: []string{"user8"},
	}).AddTokenAuth(user39Token)
	MakeRequest(t, req, http.StatusCreated)

	req = NewRequestWithJSON(t, http.MethodDelete, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", pull21Repo.OwnerName, pull21Repo.Name, pullIssue21.Index), &api.PullReviewRequestOptions{
		Reviewers: []string{"user8"},
	}).AddTokenAuth(user39Token)
	MakeRequest(t, req, http.StatusNoContent)

	// user with read permission on pull requests unit can add/remove a review request
	pullIssue22 := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 22})
	require.NoError(t, pullIssue22.LoadAttributes(db.DefaultContext))
	pull22Repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: pullIssue22.RepoID}) // repo61
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", pull22Repo.OwnerName, pull22Repo.Name, pullIssue22.Index), &api.PullReviewRequestOptions{
		Reviewers: []string{"user38"},
	}).AddTokenAuth(user39Token) // user39 is from a team with read permission on pull requests unit
	MakeRequest(t, req, http.StatusCreated)

	req = NewRequestWithJSON(t, http.MethodDelete, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", pull22Repo.OwnerName, pull22Repo.Name, pullIssue22.Index), &api.PullReviewRequestOptions{
		Reviewers: []string{"user38"},
	}).AddTokenAuth(user39Token) // user39 is from a team with read permission on pull requests unit
	MakeRequest(t, req, http.StatusNoContent)

	// Test team review request
	pullIssue12 := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 12})
	require.NoError(t, pullIssue12.LoadAttributes(db.DefaultContext))
	repo3 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: pullIssue12.RepoID})

	// Test add Team Review Request
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", repo3.OwnerName, repo3.Name, pullIssue12.Index), &api.PullReviewRequestOptions{
		TeamReviewers: []string{"team1", "owners"},
	}).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusCreated)

	// Test add Team Review Request to not allowned
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", repo3.OwnerName, repo3.Name, pullIssue12.Index), &api.PullReviewRequestOptions{
		TeamReviewers: []string{"test_team"},
	}).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusUnprocessableEntity)

	// Test add Team Review Request to not exist
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", repo3.OwnerName, repo3.Name, pullIssue12.Index), &api.PullReviewRequestOptions{
		TeamReviewers: []string{"not_exist_team"},
	}).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusNotFound)

	// Test Remove team Review Request
	req = NewRequestWithJSON(t, http.MethodDelete, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", repo3.OwnerName, repo3.Name, pullIssue12.Index), &api.PullReviewRequestOptions{
		TeamReviewers: []string{"team1"},
	}).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusNoContent)

	// empty request test
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", repo3.OwnerName, repo3.Name, pullIssue12.Index), &api.PullReviewRequestOptions{}).
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusCreated)

	req = NewRequestWithJSON(t, http.MethodDelete, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", repo3.OwnerName, repo3.Name, pullIssue12.Index), &api.PullReviewRequestOptions{}).
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusNoContent)
}

func TestAPIPullReviewRequestAccessTokenResources(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		session := loginUser(t, "user2")
		writeToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteIssue, auth_model.AccessTokenScopeWriteRepository)

		for _, repo := range []string{"user2/repo1", "user2/repo2", "org3/repo3"} {
			// For our three target repos, we'll need to enable pull requests for this test case.
			trueBool := true
			req := NewRequestWithJSON(t, "PATCH", fmt.Sprintf("/api/v1/repos/%s", repo), &api.EditRepoOption{
				HasPullRequests: &trueBool,
			}).AddTokenAuth(writeToken)
			MakeRequest(t, req, http.StatusOK)

			// Add user `user5` as a collaborator on all the target repos as well, so that we can successfully request
			// review from them.
			write := "write"
			req = NewRequestWithJSON(t, "PUT", fmt.Sprintf("/api/v1/repos/%s/collaborators/user5", repo), &api.AddCollaboratorOption{
				Permission: &write,
			}).AddTokenAuth(writeToken)
			MakeRequest(t, req, http.StatusNoContent)
		}

		// Create a pull request on each of the target test repos.
		var repo1PullRequest, repo2PullRequest, repo3PullRequest api.PullRequest
		createPullRequest := func(repoFullname string, pullRequest *api.PullRequest) {
			req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/contents", repoFullname),
				&api.ChangeFilesOptions{
					FileOptions: api.FileOptions{
						NewBranchName: "prtest",
					},
					Files: []*api.ChangeFileOperation{},
				}).AddTokenAuth(writeToken)
			MakeRequest(t, req, http.StatusCreated)

			req = NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/pulls", repoFullname), &api.CreatePullRequestOption{
				Body:  "repo1 issue dependency",
				Title: "important dependency",
				Base:  "master",
				Head:  "prtest",
			}).AddTokenAuth(writeToken)
			resp := MakeRequest(t, req, http.StatusCreated)
			DecodeJSON(t, resp, pullRequest)
		}
		createPullRequest("user2/repo1", &repo1PullRequest)
		createPullRequest("user2/repo2", &repo2PullRequest)
		createPullRequest("org3/repo3", &repo3PullRequest)

		// The core of the test is to see whether we can add pull request reviewers to the repos, using access tokens
		// with different permission scopes (all, public-only, fine-grained access tokens).  Define the test:
		testCase := func(t *testing.T, repo string, pullRequest *api.PullRequest, token string, expectedStatus int) {
			req := NewRequestWithJSON(t,
				"POST",
				fmt.Sprintf("/api/v1/repos/%s/pulls/%d/requested_reviewers", repo, pullRequest.Index),
				&api.PullReviewRequestOptions{
					Reviewers: []string{"user5"},
				}).
				AddTokenAuth(token)
			MakeRequest(t, req, expectedStatus)
		}

		t.Run("all access token", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			allToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

			testCase(t, "user2/repo1", &repo1PullRequest, allToken, http.StatusCreated) // public user2/repo1
			testCase(t, "user2/repo2", &repo2PullRequest, allToken, http.StatusCreated) // private user2/repo2
			testCase(t, "org3/repo3", &repo3PullRequest, allToken, http.StatusCreated)  // private org3/repo3
		})

		t.Run("public-only access token", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			publicOnlyToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopePublicOnly, auth_model.AccessTokenScopeWriteRepository)

			testCase(t, "user2/repo1", &repo1PullRequest, publicOnlyToken, http.StatusCreated)  // public user2/repo1
			testCase(t, "user2/repo2", &repo2PullRequest, publicOnlyToken, http.StatusNotFound) // private user2/repo2
			testCase(t, "org3/repo3", &repo3PullRequest, publicOnlyToken, http.StatusNotFound)  // private org3/repo3
		})

		t.Run("specific repo access token", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			repo2OnlyToken := createFineGrainedRepoAccessToken(t, "user2",
				[]auth_model.AccessTokenScope{auth_model.AccessTokenScopeWriteRepository},
				[]int64{3},
			)

			testCase(t, "user2/repo1", &repo1PullRequest, repo2OnlyToken, http.StatusForbidden) // public user2/repo1, read-only outside of the auth'd repos
			testCase(t, "user2/repo2", &repo2PullRequest, repo2OnlyToken, http.StatusNotFound)  // private user2/repo2, outside of fine-grain
			testCase(t, "org3/repo3", &repo3PullRequest, repo2OnlyToken, http.StatusCreated)    // private org3/repo3
		})
	})
}

func TestAPIPullReviewStayDismissed(t *testing.T) {
	// This test against issue https://github.com/go-gitea/gitea/issues/28542
	// where old reviews surface after a review request got dismissed.
	defer tests.PrepareTestEnv(t)()
	pullIssue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 3})
	require.NoError(t, pullIssue.LoadAttributes(db.DefaultContext))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: pullIssue.RepoID})
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	session2 := loginUser(t, user2.LoginName)
	token2 := getTokenForLoggedInUser(t, session2, auth_model.AccessTokenScopeWriteRepository)
	user8 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 8})
	session8 := loginUser(t, user8.LoginName)
	token8 := getTokenForLoggedInUser(t, session8, auth_model.AccessTokenScopeWriteRepository)

	// user2 request user8
	req := NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", repo.OwnerName, repo.Name, pullIssue.Index), &api.PullReviewRequestOptions{
		Reviewers: []string{user8.LoginName},
	}).AddTokenAuth(token2)
	MakeRequest(t, req, http.StatusCreated)

	reviewsCountCheck(t,
		"check we have only one review request",
		pullIssue.ID, user8.ID, 0, 1, 1, false)

	// user2 request user8 again, it is expected to be ignored
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", repo.OwnerName, repo.Name, pullIssue.Index), &api.PullReviewRequestOptions{
		Reviewers: []string{user8.LoginName},
	}).AddTokenAuth(token2)
	MakeRequest(t, req, http.StatusCreated)

	reviewsCountCheck(t,
		"check we have only one review request, even after re-request it again",
		pullIssue.ID, user8.ID, 0, 1, 1, false)

	// user8 reviews it as accept
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews", repo.OwnerName, repo.Name, pullIssue.Index), &api.CreatePullReviewOptions{
		Event: "APPROVED",
		Body:  "lgtm",
	}).AddTokenAuth(token8)
	MakeRequest(t, req, http.StatusOK)

	reviewsCountCheck(t,
		"check we have one valid approval",
		pullIssue.ID, user8.ID, 0, 0, 1, true)

	// emulate of auto-dismiss lgtm on a protected branch that where a pull just got an update
	_, err := db.GetEngine(db.DefaultContext).Where("issue_id = ? AND reviewer_id = ?", pullIssue.ID, user8.ID).
		Cols("dismissed").Update(&issues_model.Review{Dismissed: true})
	require.NoError(t, err)

	// user2 request user8 again
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/requested_reviewers", repo.OwnerName, repo.Name, pullIssue.Index), &api.PullReviewRequestOptions{
		Reviewers: []string{user8.LoginName},
	}).AddTokenAuth(token2)
	MakeRequest(t, req, http.StatusCreated)

	reviewsCountCheck(t,
		"check we have no valid approval and one review request",
		pullIssue.ID, user8.ID, 1, 1, 2, false)

	// user8 dismiss review
	_, err = issue_service.ReviewRequest(db.DefaultContext, pullIssue, user8, user8, false)
	require.NoError(t, err)

	reviewsCountCheck(t,
		"check new review request is now dismissed",
		pullIssue.ID, user8.ID, 1, 0, 1, false)

	// add a new valid approval
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews", repo.OwnerName, repo.Name, pullIssue.Index), &api.CreatePullReviewOptions{
		Event: "APPROVED",
		Body:  "lgtm",
	}).AddTokenAuth(token8)
	MakeRequest(t, req, http.StatusOK)

	reviewsCountCheck(t,
		"check that old reviews requests are deleted",
		pullIssue.ID, user8.ID, 1, 0, 2, true)

	// now add a change request witch should dismiss the approval
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews", repo.OwnerName, repo.Name, pullIssue.Index), &api.CreatePullReviewOptions{
		Event: "REQUEST_CHANGES",
		Body:  "please change XYZ",
	}).AddTokenAuth(token8)
	MakeRequest(t, req, http.StatusOK)

	reviewsCountCheck(t,
		"check that old reviews are dismissed",
		pullIssue.ID, user8.ID, 2, 0, 3, false)
}

func reviewsCountCheck(t *testing.T, name string, issueID, reviewerID int64, expectedDismissed, expectedRequested, expectedTotal int, expectApproval bool) {
	t.Run(name, func(t *testing.T) {
		unittest.AssertCountByCond(t, "review", builder.Eq{
			"issue_id":    issueID,
			"reviewer_id": reviewerID,
			"dismissed":   true,
		}, expectedDismissed)

		unittest.AssertCountByCond(t, "review", builder.Eq{
			"issue_id":    issueID,
			"reviewer_id": reviewerID,
		}, expectedTotal)

		unittest.AssertCountByCond(t, "review", builder.Eq{
			"issue_id":    issueID,
			"reviewer_id": reviewerID,
			"type":        issues_model.ReviewTypeRequest,
		}, expectedRequested)

		approvalCount := 0
		if expectApproval {
			approvalCount = 1
		}
		unittest.AssertCountByCond(t, "review", builder.Eq{
			"issue_id":    issueID,
			"reviewer_id": reviewerID,
			"type":        issues_model.ReviewTypeApprove,
			"dismissed":   false,
		}, approvalCount)
	})
}

type reviewConversation struct {
	repo       *repo_model.Repository
	otherRepo  *repo_model.Repository
	pullIndex  int64
	token      string
	root       api.PullReviewComment
	reply      api.PullReviewComment
	getComment func(commentID int64) api.PullReviewComment
}

func prepareReviewConversation(t *testing.T) reviewConversation {
	t.Helper()
	pullIssue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 3})
	otherRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerName: "user2", LowerName: "repo20"})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: pullIssue.RepoID})

	token := getTokenForLoggedInUser(t, loginUser(t, "user2"), auth_model.AccessTokenScopeWriteRepository)

	var review api.PullReview
	req := NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/pulls/%d/reviews", repo.FullName(), pullIssue.Index), &api.CreatePullReviewOptions{
		Event:    api.ReviewStateComment,
		Comments: []api.CreatePullReviewComment{{Path: "README.md", Body: "root", NewLineNum: 1}},
	}).AddTokenAuth(token)
	DecodeJSON(t, MakeRequest(t, req, http.StatusOK), &review)

	var rootComments []*api.PullReviewComment
	req = NewRequestf(t, http.MethodGet, "/api/v1/repos/%s/pulls/%d/reviews/%d/comments", repo.FullName(), pullIssue.Index, review.ID).AddTokenAuth(token)
	DecodeJSON(t, MakeRequest(t, req, http.StatusOK), &rootComments)
	require.Len(t, rootComments, 1)

	var reply api.PullReviewComment
	req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/pulls/%d/reviews/%d/comments", repo.FullName(), pullIssue.Index, review.ID), &api.CreatePullReviewCommentOptions{
		Path:       "README.md",
		Body:       "reply",
		NewLineNum: 1,
	}).AddTokenAuth(token)
	DecodeJSON(t, MakeRequest(t, req, http.StatusOK), &reply)

	return reviewConversation{
		repo:      repo,
		otherRepo: otherRepo,
		pullIndex: pullIssue.Index,
		token:     token,
		root:      *rootComments[0],
		reply:     reply,
		getComment: func(commentID int64) api.PullReviewComment {
			var comment api.PullReviewComment
			req := NewRequestf(t, http.MethodGet, "/api/v1/repos/%s/pulls/%d/reviews/%d/comments/%d", repo.FullName(), pullIssue.Index, review.ID, commentID).AddTokenAuth(token)
			DecodeJSON(t, MakeRequest(t, req, http.StatusOK), &comment)
			return comment
		},
	}
}

func resolveConversationURL(repo *repo_model.Repository, commentID int64) string {
	return fmt.Sprintf("/api/v1/repos/%s/pulls/comments/%d/resolve", repo.FullName(), commentID)
}

func unresolveConversationURL(repo *repo_model.Repository, commentID int64) string {
	return fmt.Sprintf("/api/v1/repos/%s/pulls/comments/%d/unresolve", repo.FullName(), commentID)
}

func TestAPIPullReviewResolveConversation(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	c := prepareReviewConversation(t)

	t.Run("Without write access", func(t *testing.T) {
		otherToken := getTokenForLoggedInUser(t, loginUser(t, "user4"), auth_model.AccessTokenScopeWriteRepository)
		req := NewRequest(t, http.MethodPost, resolveConversationURL(c.repo, c.root.ID)).AddTokenAuth(otherToken)
		MakeRequest(t, req, http.StatusForbidden)
		assert.Nil(t, c.getComment(c.root.ID).Resolver)
	})

	t.Run("Read-only token", func(t *testing.T) {
		// A read token is not supposed to change anything, even if it's owned by the repository owner.
		readToken := getTokenForLoggedInUser(t, loginUser(t, "user2"), auth_model.AccessTokenScopeReadRepository)
		req := NewRequest(t, http.MethodPost, resolveConversationURL(c.repo, c.root.ID)).AddTokenAuth(readToken)
		MakeRequest(t, req, http.StatusForbidden)
		assert.Nil(t, c.getComment(c.root.ID).Resolver)
	})

	t.Run("Without token", func(t *testing.T) {
		req := NewRequest(t, http.MethodPost, resolveConversationURL(c.repo, c.root.ID))
		MakeRequest(t, req, http.StatusUnauthorized)
		assert.Nil(t, c.getComment(c.root.ID).Resolver)
	})

	t.Run("Comment of another repository", func(t *testing.T) {
		req := NewRequest(t, http.MethodPost, resolveConversationURL(c.otherRepo, c.root.ID)).AddTokenAuth(c.token)
		MakeRequest(t, req, http.StatusNotFound)
	})

	t.Run("Unknown comment", func(t *testing.T) {
		req := NewRequest(t, http.MethodPost, resolveConversationURL(c.repo, 999999)).AddTokenAuth(c.token)
		MakeRequest(t, req, http.StatusNotFound)
	})

	t.Run("Not a code comment", func(t *testing.T) {
		token := getTokenForLoggedInUser(t, loginUser(t, "user2"), auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteIssue)
		var issueComment api.Comment
		req := NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/issues/%d/comments", c.repo.FullName(), c.pullIndex), &api.CreateIssueCommentOption{
			Body: "plain comment",
		}).AddTokenAuth(token)
		DecodeJSON(t, MakeRequest(t, req, http.StatusCreated), &issueComment)

		req = NewRequest(t, http.MethodPost, resolveConversationURL(c.repo, issueComment.ID)).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusUnprocessableEntity)
	})

	t.Run("PR author without write access", func(t *testing.T) {
		baseRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 10})
		forkRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 11})
		author := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: forkRepo.OwnerID})

		authorToken := getTokenForLoggedInUser(t, loginUser(t, author.Name), auth_model.AccessTokenScopeWriteRepository)
		req := NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/contents/fork-change.md", forkRepo.FullName()), &api.CreateFileOptions{
			FileOptions:   api.FileOptions{BranchName: "master", NewBranchName: "fork-change"},
			ContentBase64: base64.StdEncoding.EncodeToString([]byte("fork change\n")),
		}).AddTokenAuth(authorToken)
		MakeRequest(t, req, http.StatusCreated)

		var pull api.PullRequest
		req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/pulls", baseRepo.FullName()), &api.CreatePullRequestOption{
			Head:  fmt.Sprintf("%s:fork-change", author.Name),
			Base:  "master",
			Title: "pull request from a fork",
		}).AddTokenAuth(authorToken)
		DecodeJSON(t, MakeRequest(t, req, http.StatusCreated), &pull)

		ownerToken := getTokenForLoggedInUser(t, loginUser(t, baseRepo.OwnerName), auth_model.AccessTokenScopeWriteRepository)
		var review api.PullReview
		req = NewRequestWithJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/pulls/%d/reviews", baseRepo.FullName(), pull.Index), &api.CreatePullReviewOptions{
			Event:    api.ReviewStateComment,
			Comments: []api.CreatePullReviewComment{{Path: "fork-change.md", Body: "root", NewLineNum: 1}},
		}).AddTokenAuth(ownerToken)
		DecodeJSON(t, MakeRequest(t, req, http.StatusOK), &review)

		var comments []*api.PullReviewComment
		req = NewRequestf(t, http.MethodGet, "/api/v1/repos/%s/pulls/%d/reviews/%d/comments", baseRepo.FullName(), pull.Index, review.ID).AddTokenAuth(ownerToken)
		DecodeJSON(t, MakeRequest(t, req, http.StatusOK), &comments)
		require.Len(t, comments, 1)

		var resolved api.PullReviewComment
		req = NewRequest(t, http.MethodPost, resolveConversationURL(baseRepo, comments[0].ID)).AddTokenAuth(authorToken)
		DecodeJSON(t, MakeRequest(t, req, http.StatusOK), &resolved)
		require.NotNil(t, resolved.Resolver)
		assert.Equal(t, author.Name, resolved.Resolver.UserName)
	})

	t.Run("Resolve via a reply resolves the conversation root", func(t *testing.T) {
		var resolved api.PullReviewComment
		req := NewRequest(t, http.MethodPost, resolveConversationURL(c.repo, c.reply.ID)).AddTokenAuth(c.token)
		DecodeJSON(t, MakeRequest(t, req, http.StatusOK), &resolved)
		assert.Equal(t, c.root.ID, resolved.ID)
		require.NotNil(t, resolved.Resolver)
		assert.Equal(t, "user2", resolved.Resolver.UserName)

		require.NotNil(t, c.getComment(c.root.ID).Resolver)
		assert.Nil(t, c.getComment(c.reply.ID).Resolver)
	})

	t.Run("Resolve is idempotent", func(t *testing.T) {
		var resolved api.PullReviewComment
		req := NewRequest(t, http.MethodPost, resolveConversationURL(c.repo, c.root.ID)).AddTokenAuth(c.token)
		DecodeJSON(t, MakeRequest(t, req, http.StatusOK), &resolved)
		require.NotNil(t, resolved.Resolver)
		assert.Equal(t, "user2", resolved.Resolver.UserName)
	})
}

func TestAPIPullReviewUnresolveConversation(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	c := prepareReviewConversation(t)

	req := NewRequest(t, http.MethodPost, resolveConversationURL(c.repo, c.root.ID)).AddTokenAuth(c.token)
	MakeRequest(t, req, http.StatusOK)

	t.Run("Without write access", func(t *testing.T) {
		otherToken := getTokenForLoggedInUser(t, loginUser(t, "user4"), auth_model.AccessTokenScopeWriteRepository)
		req := NewRequest(t, http.MethodPost, unresolveConversationURL(c.repo, c.root.ID)).AddTokenAuth(otherToken)
		MakeRequest(t, req, http.StatusForbidden)
		require.NotNil(t, c.getComment(c.root.ID).Resolver)
	})

	t.Run("Read-only token", func(t *testing.T) {
		// A read token is not supposed to change anything, even if it's owned by the repository owner.
		readToken := getTokenForLoggedInUser(t, loginUser(t, "user2"), auth_model.AccessTokenScopeReadRepository)
		req := NewRequest(t, http.MethodPost, unresolveConversationURL(c.repo, c.root.ID)).AddTokenAuth(readToken)
		MakeRequest(t, req, http.StatusForbidden)
		require.NotNil(t, c.getComment(c.root.ID).Resolver)
	})

	t.Run("Without token", func(t *testing.T) {
		req := NewRequest(t, http.MethodPost, unresolveConversationURL(c.repo, c.root.ID))
		MakeRequest(t, req, http.StatusUnauthorized)
		require.NotNil(t, c.getComment(c.root.ID).Resolver)
	})

	t.Run("Unresolve via a reply unresolves the conversation root", func(t *testing.T) {
		var unresolved api.PullReviewComment
		req := NewRequest(t, http.MethodPost, unresolveConversationURL(c.repo, c.reply.ID)).AddTokenAuth(c.token)
		DecodeJSON(t, MakeRequest(t, req, http.StatusOK), &unresolved)
		assert.Equal(t, c.root.ID, unresolved.ID)
		assert.Nil(t, unresolved.Resolver)
		assert.Nil(t, c.getComment(c.root.ID).Resolver)
	})

	t.Run("Unresolve is idempotent", func(t *testing.T) {
		var unresolved api.PullReviewComment
		req := NewRequest(t, http.MethodPost, unresolveConversationURL(c.repo, c.root.ID)).AddTokenAuth(c.token)
		DecodeJSON(t, MakeRequest(t, req, http.StatusOK), &unresolved)
		assert.Nil(t, unresolved.Resolver)
	})
}
