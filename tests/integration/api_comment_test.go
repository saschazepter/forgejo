// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/references"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/services/convert"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const IssueIDNotExist = 10000

func TestAPIListRepoComments(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	comment := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{},
		unittest.Cond("type = ?", issues_model.CommentTypeComment))
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: comment.IssueID})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})
	repoOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	link, _ := url.Parse(fmt.Sprintf("/api/v1/repos/%s/%s/issues/comments", repoOwner.Name, repo.Name))
	req := NewRequest(t, "GET", link.String())
	resp := MakeRequest(t, req, http.StatusOK)

	var apiComments []*api.Comment
	DecodeJSON(t, resp, &apiComments)
	assert.Len(t, apiComments, 3)
	for _, apiComment := range apiComments {
		c := &issues_model.Comment{ID: apiComment.ID}
		unittest.AssertExistsAndLoadBean(t, c,
			unittest.Cond("type = ?", issues_model.CommentTypeComment))
		unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: c.IssueID, RepoID: repo.ID})
	}

	// test before and since filters
	query := url.Values{}
	before := "2000-01-01T00:00:11+00:00" // unix: 946684811
	since := "2000-01-01T00:00:12+00:00"  // unix: 946684812
	query.Add("before", before)
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String())
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiComments)
	assert.Len(t, apiComments, 1)
	assert.EqualValues(t, 2, apiComments[0].ID)

	query.Del("before")
	query.Add("since", since)
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String())
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiComments)
	assert.Len(t, apiComments, 2)
	assert.EqualValues(t, 3, apiComments[0].ID)
}

func TestAPIListIssueComments(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	comment := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{},
		unittest.Cond("type = ?", issues_model.CommentTypeComment))
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: comment.IssueID})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})
	repoOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	token := getUserToken(t, repoOwner.Name, auth_model.AccessTokenScopeReadIssue)
	req := NewRequestf(t, "GET", "/api/v1/repos/%s/%s/issues/%d/comments", repoOwner.Name, repo.Name, issue.Index).
		AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)

	var comments []*api.Comment
	DecodeJSON(t, resp, &comments)
	expectedCount := unittest.GetCount(t, &issues_model.Comment{IssueID: issue.ID},
		unittest.Cond("type = ?", issues_model.CommentTypeComment))
	assert.Len(t, comments, expectedCount)

	req = NewRequestf(t, "GET", "/api/v1/repos/%s/%s/issues/%d/comments", repoOwner.Name, repo.Name, IssueIDNotExist).
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusNotFound)
}

func TestAPICreateComment(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	const commentBody = "Comment body"

	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})
	repoOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	token := getUserToken(t, repoOwner.Name, auth_model.AccessTokenScopeWriteIssue)
	urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/comments",
		repoOwner.Name, repo.Name, issue.Index)
	req := NewRequestWithValues(t, "POST", urlStr, map[string]string{
		"body": commentBody,
	}).AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusCreated)

	var updatedComment api.Comment
	DecodeJSON(t, resp, &updatedComment)
	assert.Equal(t, commentBody, updatedComment.Body)
	unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: updatedComment.ID, IssueID: issue.ID, Content: commentBody})

	urlStr = fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/comments",
		repoOwner.Name, repo.Name, IssueIDNotExist)
	req = NewRequestWithValues(t, "POST", urlStr, map[string]string{
		"body": commentBody,
	}).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusNotFound)
}

func TestAPICreateCommentAutoDate(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})
	repoOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	token := getUserToken(t, repoOwner.Name, auth_model.AccessTokenScopeWriteIssue)

	urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/comments",
		repoOwner.Name, repo.Name, issue.Index)
	const commentBody = "Comment body"

	t.Run("WithAutoDate", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequestWithValues(t, "POST", urlStr, map[string]string{
			"body": commentBody,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusCreated)
		var updatedComment api.Comment
		DecodeJSON(t, resp, &updatedComment)

		// the execution of the API call supposedly lasted less than one minute
		updatedSince := time.Since(updatedComment.Updated)
		assert.LessOrEqual(t, updatedSince, time.Minute)

		commentAfter := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: updatedComment.ID, IssueID: issue.ID, Content: commentBody})
		updatedSince = time.Since(commentAfter.UpdatedUnix.AsTime())
		assert.LessOrEqual(t, updatedSince, time.Minute)
	})

	t.Run("WithUpdateDate", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		updatedAt := time.Now().Add(-time.Hour).Truncate(time.Second)

		req := NewRequestWithJSON(t, "POST", urlStr, &api.CreateIssueCommentOption{
			Body:    commentBody,
			Updated: &updatedAt,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusCreated)
		var updatedComment api.Comment
		DecodeJSON(t, resp, &updatedComment)

		// dates will be converted into the same tz, in order to compare them
		utcTZ, _ := time.LoadLocation("UTC")
		assert.Equal(t, updatedAt.In(utcTZ), updatedComment.Updated.In(utcTZ))
		commentAfter := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: updatedComment.ID, IssueID: issue.ID, Content: commentBody})
		assert.Equal(t, updatedAt.In(utcTZ), commentAfter.UpdatedUnix.AsTime().In(utcTZ))
	})
}

func TestAPICommentXRefAutoDate(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})
	repoOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	token := getUserToken(t, repoOwner.Name, auth_model.AccessTokenScopeWriteIssue)

	t.Run("WithAutoDate", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		// Create a comment mentioning issue #2 and check that a xref comment was added
		// in issue #2
		urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/comments",
			repoOwner.Name, repo.Name, issue.Index)

		commentBody := "mention #2"
		req := NewRequestWithJSON(t, "POST", urlStr, &api.CreateIssueCommentOption{
			Body: commentBody,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusCreated)
		var createdComment api.Comment
		DecodeJSON(t, resp, &createdComment)

		ref := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{IssueID: 2, RefIssueID: 1, RefCommentID: createdComment.ID})
		assert.Equal(t, issues_model.CommentTypeCommentRef, ref.Type)
		assert.Equal(t, references.XRefActionNone, ref.RefAction)
		// the execution of the API call supposedly lasted less than one minute
		updatedSince := time.Since(ref.UpdatedUnix.AsTime())
		assert.LessOrEqual(t, updatedSince, time.Minute)

		// Remove the mention to issue #2 and check that the xref was neutered
		urlStr = fmt.Sprintf("/api/v1/repos/%s/%s/issues/comments/%d",
			repoOwner.Name, repo.Name, createdComment.ID)

		newCommentBody := "no mention"
		req = NewRequestWithJSON(t, "PATCH", urlStr, &api.EditIssueCommentOption{
			Body: newCommentBody,
		}).AddTokenAuth(token)
		resp = MakeRequest(t, req, http.StatusOK)
		var updatedComment api.Comment
		DecodeJSON(t, resp, &updatedComment)

		ref = unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{IssueID: 2, RefIssueID: 1, RefCommentID: updatedComment.ID})
		assert.Equal(t, issues_model.CommentTypeCommentRef, ref.Type)
		assert.Equal(t, references.XRefActionNeutered, ref.RefAction)
		// the execution of the API call supposedly lasted less than one minute
		updatedSince = time.Since(ref.UpdatedUnix.AsTime())
		assert.LessOrEqual(t, updatedSince, time.Minute)
	})

	t.Run("WithUpdateDate", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		// dates will be converted into the same tz, in order to compare them
		utcTZ, _ := time.LoadLocation("UTC")

		// Create a comment mentioning issue #2 and check that a xref comment was added
		// in issue #2
		urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/comments",
			repoOwner.Name, repo.Name, issue.Index)

		commentBody := "re-mention #2"
		updatedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
		req := NewRequestWithJSON(t, "POST", urlStr, &api.CreateIssueCommentOption{
			Body:    commentBody,
			Updated: &updatedAt,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusCreated)
		var createdComment api.Comment
		DecodeJSON(t, resp, &createdComment)

		ref := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{IssueID: 2, RefIssueID: 1, RefCommentID: createdComment.ID})
		assert.Equal(t, issues_model.CommentTypeCommentRef, ref.Type)
		assert.Equal(t, references.XRefActionNone, ref.RefAction)
		assert.Equal(t, updatedAt.In(utcTZ), ref.UpdatedUnix.AsTimeInLocation(utcTZ))

		// Remove the mention to issue #2 and check that the xref was neutered
		urlStr = fmt.Sprintf("/api/v1/repos/%s/%s/issues/comments/%d",
			repoOwner.Name, repo.Name, createdComment.ID)

		newCommentBody := "no mention"
		updatedAt = time.Now().Add(-time.Hour).Truncate(time.Second)
		req = NewRequestWithJSON(t, "PATCH", urlStr, &api.EditIssueCommentOption{
			Body:    newCommentBody,
			Updated: &updatedAt,
		}).AddTokenAuth(token)
		resp = MakeRequest(t, req, http.StatusOK)
		var updatedComment api.Comment
		DecodeJSON(t, resp, &updatedComment)

		ref = unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{IssueID: 2, RefIssueID: 1, RefCommentID: updatedComment.ID})
		assert.Equal(t, issues_model.CommentTypeCommentRef, ref.Type)
		assert.Equal(t, references.XRefActionNeutered, ref.RefAction)
		assert.Equal(t, updatedAt.In(utcTZ), ref.UpdatedUnix.AsTimeInLocation(utcTZ))
	})
}

func TestAPIGetComment(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	comment := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: 2})
	require.NoError(t, comment.LoadIssue(db.DefaultContext))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: comment.Issue.RepoID})
	repoOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	req := NewRequestf(t, "GET", "/api/v1/repos/%s/%s/issues/comments/%d", repoOwner.Name, repo.Name, comment.ID)

	t.Run("Poster is a known user", func(t *testing.T) {
		resp := MakeRequest(t, req, http.StatusOK)

		var apiComment api.Comment
		DecodeJSON(t, resp, &apiComment)

		require.NoError(t, comment.LoadPoster(db.DefaultContext))
		expect := convert.ToAPIComment(db.DefaultContext, repo, comment)

		assert.Equal(t, expect.ID, apiComment.ID)
		assert.Equal(t, expect.Poster.FullName, apiComment.Poster.FullName)
		assert.Equal(t, expect.Body, apiComment.Body)
		assert.Equal(t, expect.Created.Unix(), apiComment.Created.Unix())
	})

	t.Run("Poster is Ghost because the user was deleted", func(t *testing.T) {
		unittest.AssertSuccessfulDelete(t, &user_model.User{ID: comment.PosterID})
		resp := MakeRequest(t, req, http.StatusOK)
		var apiComment api.Comment
		DecodeJSON(t, resp, &apiComment)
		ghostUser := user_model.NewGhostUser()
		assert.Equal(t, ghostUser.ID, apiComment.Poster.ID)
	})
}

func TestAPIGetSystemUserComment(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})
	repoOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	for _, systemUser := range []*user_model.User{
		user_model.NewGhostUser(),
		user_model.NewActionsUser(),
	} {
		body := fmt.Sprintf("Hello %s", systemUser.Name)
		comment, err := issues_model.CreateComment(db.DefaultContext, &issues_model.CreateCommentOptions{
			Type:    issues_model.CommentTypeComment,
			Doer:    systemUser,
			Repo:    repo,
			Issue:   issue,
			Content: body,
		})
		require.NoError(t, err)

		req := NewRequestf(t, "GET", "/api/v1/repos/%s/%s/issues/comments/%d", repoOwner.Name, repo.Name, comment.ID)
		resp := MakeRequest(t, req, http.StatusOK)

		var apiComment api.Comment
		DecodeJSON(t, resp, &apiComment)

		if assert.NotNil(t, apiComment.Poster) {
			if assert.Equal(t, systemUser.ID, apiComment.Poster.ID) {
				require.NoError(t, comment.LoadPoster(db.DefaultContext))
				assert.Equal(t, systemUser.Name, apiComment.Poster.UserName)
			}
		}
		assert.Equal(t, body, apiComment.Body)
	}
}

func TestAPIEditComment(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	const newCommentBody = "This is the new comment body"

	comment := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: 8},
		unittest.Cond("type = ?", issues_model.CommentTypeComment))
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: comment.IssueID})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})
	repoOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	t.Run("UnrelatedCommentID", func(t *testing.T) {
		// Using the ID of a comment that does not belong to the repository must fail
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
		repoOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
		token := getUserToken(t, repoOwner.Name, auth_model.AccessTokenScopeWriteIssue)
		urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/issues/comments/%d",
			repoOwner.Name, repo.Name, comment.ID)
		req := NewRequestWithValues(t, "PATCH", urlStr, map[string]string{
			"body": newCommentBody,
		}).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusNotFound)
	})

	token := getUserToken(t, repoOwner.Name, auth_model.AccessTokenScopeWriteIssue)
	urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/issues/comments/%d",
		repoOwner.Name, repo.Name, comment.ID)
	req := NewRequestWithValues(t, "PATCH", urlStr, map[string]string{
		"body": newCommentBody,
	}).AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)

	var updatedComment api.Comment
	DecodeJSON(t, resp, &updatedComment)
	assert.Equal(t, comment.ID, updatedComment.ID)
	assert.Equal(t, newCommentBody, updatedComment.Body)
	unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: comment.ID, IssueID: issue.ID, Content: newCommentBody})
}

func TestAPIEditCommentWithDate(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	comment := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{},
		unittest.Cond("type = ?", issues_model.CommentTypeComment))
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: comment.IssueID})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})
	repoOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	token := getUserToken(t, repoOwner.Name, auth_model.AccessTokenScopeWriteIssue)

	urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/issues/comments/%d",
		repoOwner.Name, repo.Name, comment.ID)
	const newCommentBody = "This is the new comment body"

	t.Run("WithAutoDate", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequestWithValues(t, "PATCH", urlStr, map[string]string{
			"body": newCommentBody,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusOK)
		var updatedComment api.Comment
		DecodeJSON(t, resp, &updatedComment)

		// the execution of the API call supposedly lasted less than one minute
		updatedSince := time.Since(updatedComment.Updated)
		assert.LessOrEqual(t, updatedSince, time.Minute)

		commentAfter := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: comment.ID, IssueID: issue.ID, Content: newCommentBody})
		updatedSince = time.Since(commentAfter.UpdatedUnix.AsTime())
		assert.LessOrEqual(t, updatedSince, time.Minute)
	})

	t.Run("WithUpdateDate", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		updatedAt := time.Now().Add(-time.Hour).Truncate(time.Second)

		req := NewRequestWithJSON(t, "PATCH", urlStr, &api.EditIssueCommentOption{
			Body:    newCommentBody,
			Updated: &updatedAt,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusOK)
		var updatedComment api.Comment
		DecodeJSON(t, resp, &updatedComment)

		// dates will be converted into the same tz, in order to compare them
		utcTZ, _ := time.LoadLocation("UTC")
		assert.Equal(t, updatedAt.In(utcTZ), updatedComment.Updated.In(utcTZ))
		commentAfter := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: comment.ID, IssueID: issue.ID, Content: newCommentBody})
		assert.Equal(t, updatedAt.In(utcTZ), commentAfter.UpdatedUnix.AsTime().In(utcTZ))
	})
}

func TestAPIDeleteComment(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	comment := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: 8},
		unittest.Cond("type = ?", issues_model.CommentTypeComment))
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: comment.IssueID})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})
	repoOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	t.Run("UnrelatedCommentID", func(t *testing.T) {
		// Using the ID of a comment that does not belong to the repository must fail
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
		repoOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
		token := getUserToken(t, repoOwner.Name, auth_model.AccessTokenScopeWriteIssue)
		req := NewRequestf(t, "DELETE", "/api/v1/repos/%s/%s/issues/comments/%d", repoOwner.Name, repo.Name, comment.ID).
			AddTokenAuth(token)
		MakeRequest(t, req, http.StatusNotFound)
	})

	token := getUserToken(t, repoOwner.Name, auth_model.AccessTokenScopeWriteIssue)
	req := NewRequestf(t, "DELETE", "/api/v1/repos/%s/%s/issues/comments/%d", repoOwner.Name, repo.Name, comment.ID).
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusNoContent)

	unittest.AssertNotExistsBean(t, &issues_model.Comment{ID: comment.ID})
}

func TestAPIListIssueTimeline(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	// load comment
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})
	repoOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	// The default request returns the first page.
	req := NewRequestf(t, "GET", "/api/v1/repos/%s/%s/issues/%d/timeline", repoOwner.Name, repo.Name, issue.Index)
	resp := MakeRequest(t, req, http.StatusOK)

	var comments []*api.TimelineComment
	DecodeJSON(t, resp, &comments)
	expectedCount := unittest.GetCount(t, &issues_model.Comment{IssueID: issue.ID})
	require.Greater(t, expectedCount, setting.API.DefaultPagingNum)
	assert.Len(t, comments, setting.API.DefaultPagingNum)

	// The next page returns the remaining timeline entries.
	req = NewRequestf(t, "GET", "/api/v1/repos/%s/%s/issues/%d/timeline?page=2", repoOwner.Name, repo.Name, issue.Index)
	resp = MakeRequest(t, req, http.StatusOK)

	var commentsPageTwo []*api.TimelineComment
	DecodeJSON(t, resp, &commentsPageTwo)
	assert.Len(t, commentsPageTwo, expectedCount-setting.API.DefaultPagingNum)

	req = NewRequestf(t, "GET", "/api/v1/repos/%s/%s/issues/%d/timeline", repoOwner.Name, repo.Name, IssueIDNotExist)
	MakeRequest(t, req, http.StatusNotFound)
}

func TestAPIListIssueTimelineAccessTokenResources(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")
	writeToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteIssue)

	// Create an issue on a repo, repo1 -- call it issue1.
	req := NewRequestWithJSON(t, "POST", "/api/v1/repos/user2/repo1/issues", &api.CreateIssueOption{
		Body:  "issue body",
		Title: "issue title",
	}).AddTokenAuth(writeToken)
	resp := MakeRequest(t, req, http.StatusCreated)
	var issue1 api.Issue
	DecodeJSON(t, resp, &issue1)

	// On three other issues, on a public repo (repo1), on two private repos (repo2, org3/repo3), create
	// cross-references via comments to issue1. (typically repo16 is used in similar tests for a second private repo,
	// but can't be used here because it doesn't have the issue unit enabled)
	req = NewRequestWithJSON(t, "POST", "/api/v1/repos/user2/repo1/issues", &api.CreateIssueOption{
		Body:  "repo1 referencing issue",
		Title: fmt.Sprintf("I really like the idea of %s ...", issue1.HTMLURL),
	}).AddTokenAuth(writeToken)
	MakeRequest(t, req, http.StatusCreated)
	req = NewRequestWithJSON(t, "POST", "/api/v1/repos/user2/repo2/issues", &api.CreateIssueOption{
		Body:  "repo1 referencing issue",
		Title: fmt.Sprintf("I really like the idea of %s ...", issue1.HTMLURL),
	}).AddTokenAuth(writeToken)
	MakeRequest(t, req, http.StatusCreated)
	req = NewRequestWithJSON(t, "POST", "/api/v1/repos/org3/repo3/issues", &api.CreateIssueOption{
		Body:  "repo1 referencing issue",
		Title: fmt.Sprintf("I really like the idea of %s ...", issue1.HTMLURL),
	}).AddTokenAuth(writeToken)
	MakeRequest(t, req, http.StatusCreated)

	// The remainder of this test reads the timeline on issue1 with different access token resources and see if the
	// cross-references are visible or hidden.
	var comments []*api.TimelineComment
	find := func() (bool, bool, bool) {
		foundRepo1 := false // public repo1
		foundRepo2 := false // private repo2
		foundRepo3 := false // second public repo used in fine-grain testing, included as baseline
		for _, comment := range comments {
			// println(fmt.Sprintf("%#v", comment))
			if comment.RefIssue != nil && comment.RefIssue.Repo != nil {
				// println(fmt.Sprintf("RefIssue = %#v", comment.RefIssue))
				switch comment.RefIssue.Repo.Name {
				case "repo1":
					foundRepo1 = true
				case "repo2":
					foundRepo2 = true
				case "repo3":
					foundRepo3 = true
				}
			}
		}
		return foundRepo1, foundRepo2, foundRepo3
	}

	t.Run("all access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		allToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadIssue)

		req := NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/user2/repo1/issues/%d/timeline", issue1.Index)).AddTokenAuth(allToken)
		resp := MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &comments)
		foundRepo1, foundRepo2, foundRepo3 := find()

		assert.True(t, foundRepo1) // public repo1
		assert.True(t, foundRepo2) // private repo2
		assert.True(t, foundRepo3) // private org3/repo3, used in fine-grain testing, included as baseline
	})

	t.Run("public-only access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		publicOnlyToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopePublicOnly, auth_model.AccessTokenScopeReadIssue)

		req := NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/user2/repo1/issues/%d/timeline", issue1.Index)).AddTokenAuth(publicOnlyToken)
		resp := MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &comments)
		foundRepo1, foundRepo2, foundRepo3 := find()

		assert.True(t, foundRepo1)  // public repo1
		assert.False(t, foundRepo2) // private repo2
		assert.False(t, foundRepo3) // private org3/repo3
	})

	t.Run("specific repo access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		repo2OnlyToken := createFineGrainedRepoAccessToken(t, "user2",
			[]auth_model.AccessTokenScope{auth_model.AccessTokenScopeReadIssue},
			[]int64{2},
		)

		req := NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/user2/repo1/issues/%d/timeline", issue1.Index)).AddTokenAuth(repo2OnlyToken)
		resp := MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &comments)
		foundRepo1, foundRepo2, foundRepo3 := find()

		assert.True(t, foundRepo1)  // public repo1, allowed as it's public and read-access only
		assert.True(t, foundRepo2)  // private repo2, allowed inside fine-grain
		assert.False(t, foundRepo3) // private org3/repo3, denied outside fine-grain
	})
}
