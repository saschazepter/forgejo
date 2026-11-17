// Copyright 2024 The Forgejo Authors
// SPDX-License-Identifier: MIT

package issues

import (
	"testing"

	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommentListLoadUser(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	issue := unittest.AssertExistsAndLoadBean(t, &Issue{})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	for _, testCase := range []struct {
		poster   int64
		assignee int64
		user     *user_model.User
	}{
		{
			poster:   user_model.ActionsUserID,
			assignee: user_model.ActionsUserID,
			user:     user_model.NewActionsUser(),
		},
		{
			poster:   user_model.GhostUserID,
			assignee: user_model.GhostUserID,
			user:     user_model.NewGhostUser(),
		},
		{
			poster:   doer.ID,
			assignee: doer.ID,
			user:     doer,
		},
		{
			poster:   0,
			assignee: 0,
			user:     user_model.NewGhostUser(),
		},
		{
			poster:   -200,
			assignee: -200,
			user:     user_model.NewGhostUser(),
		},
		{
			poster:   200,
			assignee: 200,
			user:     user_model.NewGhostUser(),
		},
	} {
		t.Run(testCase.user.Name, func(t *testing.T) {
			comment, err := CreateComment(db.DefaultContext, &CreateCommentOptions{
				Type:    CommentTypeComment,
				Doer:    testCase.user,
				Repo:    repo,
				Issue:   issue,
				Content: "Hello",
			})
			assert.NoError(t, err)

			list := CommentList{comment}

			comment.PosterID = testCase.poster
			comment.Poster = nil
			assert.NoError(t, list.LoadPosters(db.DefaultContext))
			require.NotNil(t, comment.Poster)
			assert.Equal(t, testCase.user.ID, comment.Poster.ID)

			comment.AssigneeID = testCase.assignee
			comment.Assignee = nil
			require.NoError(t, list.loadAssignees(db.DefaultContext))
			require.NotNil(t, comment.Assignee)
			assert.Equal(t, testCase.user.ID, comment.Assignee.ID)
		})
	}
}

func TestCommentListLoadResolveDoers(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	issue := unittest.AssertExistsAndLoadBean(t, &Issue{})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	empty := CommentList{}
	require.NoError(t, empty.LoadResolveDoers(t.Context()))

	comment1, err := CreateComment(db.DefaultContext, &CreateCommentOptions{
		Type:    CommentTypeCode,
		Doer:    doer,
		Repo:    repo,
		Issue:   issue,
		Content: "Hello",
	})
	require.NoError(t, err)
	require.NoError(t, MarkConversation(t.Context(), comment1, doer, true))
	comment1 = unittest.AssertExistsAndLoadBean(t, &Comment{ID: comment1.ID}) // reload after change
	comment1List := CommentList{comment1}
	require.NoError(t, comment1List.LoadResolveDoers(t.Context()))
	require.NotNil(t, comment1.ResolveDoer)
	assert.Equal(t, doer.ID, comment1.ResolveDoer.ID)

	comment2, err := CreateComment(db.DefaultContext, &CreateCommentOptions{
		Type:    CommentTypeCode,
		Doer:    doer,
		Repo:    repo,
		Issue:   issue,
		Content: "Hello again",
	})
	require.NoError(t, err)
	require.NoError(t, MarkConversation(t.Context(), comment2, user_model.NewGhostUser(), true))

	// Reload for fresh objects
	comment1 = unittest.AssertExistsAndLoadBean(t, &Comment{ID: comment1.ID})
	comment2 = unittest.AssertExistsAndLoadBean(t, &Comment{ID: comment2.ID})

	comment2List := CommentList{comment1, comment2}
	require.NoError(t, comment2List.LoadResolveDoers(t.Context()))
	require.NotNil(t, comment1.ResolveDoer)
	assert.Equal(t, doer.ID, comment1.ResolveDoer.ID)
	require.NotNil(t, comment2.ResolveDoer)
	assert.EqualValues(t, -1, comment2.ResolveDoer.ID)
}

func TestCommentListLoadReactions(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	issue := unittest.AssertExistsAndLoadBean(t, &Issue{})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	empty := CommentList{}
	require.NoError(t, empty.LoadReactions(t.Context(), repo))

	comment1, err := CreateComment(db.DefaultContext, &CreateCommentOptions{
		Type:    CommentTypeCode,
		Doer:    doer,
		Repo:    repo,
		Issue:   issue,
		Content: "Hello",
	})
	require.NoError(t, err)
	_, err = CreateReaction(t.Context(), &ReactionOptions{
		Type:      "eyes",
		DoerID:    doer.ID,
		IssueID:   issue.ID,
		CommentID: comment1.ID,
	})
	require.NoError(t, err)

	comment1 = unittest.AssertExistsAndLoadBean(t, &Comment{ID: comment1.ID}) // reload after change
	comment1List := CommentList{comment1}
	require.NoError(t, comment1List.LoadReactions(t.Context(), repo))
	require.Len(t, comment1.Reactions, 1)
	assert.Equal(t, "eyes", comment1.Reactions[0].Type)
	assert.NotNil(t, comment1.Reactions[0].User)

	comment2, err := CreateComment(db.DefaultContext, &CreateCommentOptions{
		Type:    CommentTypeCode,
		Doer:    doer,
		Repo:    repo,
		Issue:   issue,
		Content: "Hello again",
	})
	require.NoError(t, err)
	_, err = CreateReaction(t.Context(), &ReactionOptions{
		Type:      "rocket",
		DoerID:    doer.ID,
		IssueID:   issue.ID,
		CommentID: comment2.ID,
	})
	require.NoError(t, err)

	// Reload for fresh objects
	comment1 = unittest.AssertExistsAndLoadBean(t, &Comment{ID: comment1.ID})
	comment2 = unittest.AssertExistsAndLoadBean(t, &Comment{ID: comment2.ID})

	comment2List := CommentList{comment1, comment2}
	require.NoError(t, comment2List.LoadReactions(t.Context(), repo))
	require.Len(t, comment1.Reactions, 1)
	require.Len(t, comment2.Reactions, 1)
	assert.Equal(t, "rocket", comment2.Reactions[0].Type)
	assert.NotNil(t, comment2.Reactions[0].User)
}

func TestCommentListLoadLabels(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	// Comments whose ID differ from their label ID, so a lookup by the wrong key
	// cannot pass by accident, and with different labels, so each comment must
	// get its own label.
	comment1 := unittest.AssertExistsAndLoadBean(t, &Comment{ID: 2020, LabelID: 1})
	comment2 := unittest.AssertExistsAndLoadBean(t, &Comment{ID: 2021, LabelID: 2})

	require.NoError(t, CommentList{comment1, comment2}.loadLabels(db.DefaultContext))

	require.NotNil(t, comment1.Label)
	assert.EqualValues(t, 1, comment1.Label.ID)
	require.NotNil(t, comment2.Label)
	assert.EqualValues(t, 2, comment2.Label.ID)
}
