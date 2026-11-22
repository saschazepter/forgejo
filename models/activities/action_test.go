// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package activities_test

import (
	"fmt"
	"path"
	"testing"

	activities_model "forgejo.org/models/activities"
	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAction_GetRepoPath(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	action := &activities_model.Action{RepoID: repo.ID}
	assert.Equal(t, path.Join(owner.Name, repo.Name), action.GetRepoPath(db.DefaultContext))
}

func TestAction_GetRepoLink(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	comment := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: 2})
	action := &activities_model.Action{RepoID: repo.ID, CommentID: comment.ID}
	setting.AppSubURL = "/suburl"
	expected := path.Join(setting.AppSubURL, owner.Name, repo.Name)
	assert.Equal(t, expected, action.GetRepoLink(db.DefaultContext))
	assert.Equal(t, repo.HTMLURL(), action.GetRepoAbsoluteLink(db.DefaultContext))
	assert.Equal(t, comment.HTMLURL(db.DefaultContext), action.GetCommentHTMLURL(db.DefaultContext))
}

func TestGetFeeds(t *testing.T) {
	// test with an individual user
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	actions, count, err := activities_model.GetFeeds(db.DefaultContext, activities_model.GetFeedsOptions{
		RequestedUser:   user,
		Actor:           user,
		IncludePrivate:  true,
		OnlyPerformedBy: false,
	})
	require.NoError(t, err)
	if assert.Len(t, actions, 1) {
		assert.EqualValues(t, 1, actions[0].ID)
		assert.Equal(t, user.ID, actions[0].UserID)
	}
	assert.Equal(t, int64(1), count)

	actions, count, err = activities_model.GetFeeds(db.DefaultContext, activities_model.GetFeedsOptions{
		RequestedUser:   user,
		Actor:           user,
		IncludePrivate:  false,
		OnlyPerformedBy: false,
	})
	require.NoError(t, err)
	assert.Empty(t, actions)
	assert.Equal(t, int64(0), count)
}

func TestGetFeedsForRepos(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	privRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	pubRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 8})

	// private repo & no login
	actions, count, err := activities_model.GetFeeds(db.DefaultContext, activities_model.GetFeedsOptions{
		RequestedRepo:  privRepo,
		IncludePrivate: true,
	})
	require.NoError(t, err)
	assert.Empty(t, actions)
	assert.Equal(t, int64(0), count)

	// public repo & no login
	actions, count, err = activities_model.GetFeeds(db.DefaultContext, activities_model.GetFeedsOptions{
		RequestedRepo:  pubRepo,
		IncludePrivate: true,
	})
	require.NoError(t, err)
	assert.Len(t, actions, 1)
	assert.Equal(t, int64(1), count)

	// private repo and login
	actions, count, err = activities_model.GetFeeds(db.DefaultContext, activities_model.GetFeedsOptions{
		RequestedRepo:  privRepo,
		IncludePrivate: true,
		Actor:          user,
	})
	require.NoError(t, err)
	assert.Len(t, actions, 1)
	assert.Equal(t, int64(1), count)

	// public repo & login
	actions, count, err = activities_model.GetFeeds(db.DefaultContext, activities_model.GetFeedsOptions{
		RequestedRepo:  pubRepo,
		IncludePrivate: true,
		Actor:          user,
	})
	require.NoError(t, err)
	assert.Len(t, actions, 1)
	assert.Equal(t, int64(1), count)
}

func TestGetFeeds2(t *testing.T) {
	// test with an organization user
	require.NoError(t, unittest.PrepareTestDatabase())
	org := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	actions, count, err := activities_model.GetFeeds(db.DefaultContext, activities_model.GetFeedsOptions{
		RequestedUser:   org,
		Actor:           user,
		IncludePrivate:  true,
		OnlyPerformedBy: false,
	})
	require.NoError(t, err)
	assert.Len(t, actions, 1)
	if assert.Len(t, actions, 1) {
		assert.EqualValues(t, 2, actions[0].ID)
		assert.Equal(t, org.ID, actions[0].UserID)
	}
	assert.Equal(t, int64(1), count)

	actions, count, err = activities_model.GetFeeds(db.DefaultContext, activities_model.GetFeedsOptions{
		RequestedUser:   org,
		Actor:           user,
		IncludePrivate:  false,
		OnlyPerformedBy: false,
	})
	require.NoError(t, err)
	assert.Empty(t, actions)
	assert.Equal(t, int64(0), count)
}

func TestActivityReadable(t *testing.T) {
	tt := []struct {
		desc   string
		user   *user_model.User
		doer   *user_model.User
		result bool
	}{{
		desc:   "user should see own activity",
		user:   &user_model.User{ID: 1},
		doer:   &user_model.User{ID: 1},
		result: true,
	}, {
		desc:   "anon should see activity if public",
		user:   &user_model.User{ID: 1},
		result: true,
	}, {
		desc:   "anon should NOT see activity",
		user:   &user_model.User{ID: 1, KeepActivityPrivate: true},
		result: false,
	}, {
		desc:   "user should see own activity if private too",
		user:   &user_model.User{ID: 1, KeepActivityPrivate: true},
		doer:   &user_model.User{ID: 1},
		result: true,
	}, {
		desc:   "other user should NOT see activity",
		user:   &user_model.User{ID: 1, KeepActivityPrivate: true},
		doer:   &user_model.User{ID: 2},
		result: false,
	}, {
		desc:   "admin should see activity",
		user:   &user_model.User{ID: 1, KeepActivityPrivate: true},
		doer:   &user_model.User{ID: 2, IsAdmin: true},
		result: true,
	}}
	for _, test := range tt {
		assert.Equal(t, test.result, activities_model.ActivityReadable(test.user, test.doer), test.desc)
	}
}

func TestNotifyWatchers(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	action := &activities_model.Action{
		ActUserID: 8,
		RepoID:    1,
		OpType:    activities_model.ActionStarRepo,
	}
	_, err := activities_model.NotifyWatchers(db.DefaultContext, action)
	require.NoError(t, err)

	// One watchers are inactive, thus action is only created for user 8, 1, 4, 11
	unittest.AssertExistsAndLoadBean(t, &activities_model.Action{
		ActUserID: action.ActUserID,
		UserID:    8,
		RepoID:    action.RepoID,
		OpType:    action.OpType,
	})
	unittest.AssertExistsAndLoadBean(t, &activities_model.Action{
		ActUserID: action.ActUserID,
		UserID:    1,
		RepoID:    action.RepoID,
		OpType:    action.OpType,
	})
	unittest.AssertExistsAndLoadBean(t, &activities_model.Action{
		ActUserID: action.ActUserID,
		UserID:    4,
		RepoID:    action.RepoID,
		OpType:    action.OpType,
	})
	unittest.AssertExistsAndLoadBean(t, &activities_model.Action{
		ActUserID: action.ActUserID,
		UserID:    11,
		RepoID:    action.RepoID,
		OpType:    action.OpType,
	})
}

func TestNotifySelectWatchers(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	action := &activities_model.Action{
		ActUserID: 8,
		RepoID:    5,
		OpType:    activities_model.ActionPullReviewDismissed,
	}
	_, err := activities_model.NotifyWatchers(db.DefaultContext, action)
	require.NoError(t, err)

	unittest.AssertExistsAndLoadBean(t, &activities_model.Action{
		ActUserID: action.ActUserID,
		UserID:    5,
		RepoID:    action.RepoID,
		OpType:    action.OpType,
	})
	// Notice that user 9 isn't here because they don't watch PRs on this repo.
	unittest.AssertNotExistsBean(t, &activities_model.Action{
		ActUserID: action.ActUserID,
		UserID:    4,
		RepoID:    action.RepoID,
		OpType:    action.OpType,
	})
}

func TestGetFeedsCorrupted(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	unittest.AssertExistsAndLoadBean(t, &activities_model.Action{
		ID:     8,
		RepoID: 1700,
	})

	actions, count, err := activities_model.GetFeeds(db.DefaultContext, activities_model.GetFeedsOptions{
		RequestedUser:  user,
		Actor:          user,
		IncludePrivate: true,
	})
	require.NoError(t, err)
	assert.Empty(t, actions)
	assert.Equal(t, int64(0), count)
}

func TestConsistencyUpdateAction(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		t.Skip("Test is only for SQLite database.")
	}
	require.NoError(t, unittest.PrepareTestDatabase())
	id := 8
	unittest.AssertExistsAndLoadBean(t, &activities_model.Action{
		ID: int64(id),
	})
	_, err := db.GetEngine(db.DefaultContext).Exec(`UPDATE action SET created_unix = "" WHERE id = ?`, id)
	require.NoError(t, err)
	actions := make([]*activities_model.Action, 0, 1)
	//
	// XORM returns an error when created_unix is a string
	//
	err = db.GetEngine(db.DefaultContext).Where("id = ?", id).Find(&actions)
	require.ErrorContains(t, err, "type string to a int64: invalid syntax")

	//
	// Get rid of incorrectly set created_unix
	//
	count, err := activities_model.CountActionCreatedUnixString(db.DefaultContext)
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)
	count, err = activities_model.FixActionCreatedUnixString(db.DefaultContext)
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)

	count, err = activities_model.CountActionCreatedUnixString(db.DefaultContext)
	require.NoError(t, err)
	assert.EqualValues(t, 0, count)
	count, err = activities_model.FixActionCreatedUnixString(db.DefaultContext)
	require.NoError(t, err)
	assert.EqualValues(t, 0, count)

	//
	// XORM must be happy now
	//
	require.NoError(t, db.GetEngine(db.DefaultContext).Where("id = ?", id).Find(&actions))
	unittest.CheckConsistencyFor(t, &activities_model.Action{})
}

func TestDeleteIssueActions(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	// load an issue
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 4})
	assert.NotEqual(t, issue.ID, issue.Index) // it needs to use different ID/Index to test the DeleteIssueActions to delete some actions by IssueIndex

	// insert a comment
	err := db.Insert(db.DefaultContext, &issues_model.Comment{Type: issues_model.CommentTypeComment, IssueID: issue.ID})
	require.NoError(t, err)
	comment := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{Type: issues_model.CommentTypeComment, IssueID: issue.ID})

	// truncate action table and insert some actions
	err = db.TruncateBeans(db.DefaultContext, &activities_model.Action{})
	require.NoError(t, err)
	err = db.Insert(db.DefaultContext, &activities_model.Action{
		OpType:    activities_model.ActionCommentIssue,
		CommentID: comment.ID,
	})
	require.NoError(t, err)
	err = db.Insert(db.DefaultContext, &activities_model.Action{
		OpType: activities_model.ActionCreateIssue,
		RepoID: issue.RepoID,
		// Older Content format...
		Content: fmt.Sprintf("%d|content...", issue.Index),
	})
	require.NoError(t, err)
	err = db.Insert(db.DefaultContext, &activities_model.Action{
		OpType: activities_model.ActionCreateIssue,
		RepoID: issue.RepoID,
		// JSON-encoded Content format...
		Content: fmt.Sprintf("[\"%d\",\"content...\"]", issue.Index),
	})
	require.NoError(t, err)

	// assert that the actions exist, then delete them
	unittest.AssertCount(t, &activities_model.Action{}, 3)
	require.NoError(t, activities_model.DeleteIssueActions(db.DefaultContext, issue.RepoID, issue.ID, issue.Index))
	unittest.AssertCount(t, &activities_model.Action{}, 0)
}

func TestGetIssueInfos(t *testing.T) {
	tt := []struct {
		content string
		field1  string
		field2  string
		field3  string
	}{
		{
			content: "4|",
			field1:  "4",
		},
		{
			content: "2|docs: Add README w/ template sections",
			field1:  "2",
			field2:  "docs: Add README w/ template sections",
		},
		{
			content: "2|docs: Add README w/ template sections|Some comment...",
			field1:  "2",
			field2:  "docs: Add README w/ template sections",
			field3:  "Some comment...",
		},
		{
			content: "[\"4\"]",
			field1:  "4",
		},
		{
			content: "[\"2\", \"docs: Add README w/ | template sections\"]",
			field1:  "2",
			field2:  "docs: Add README w/ | template sections",
		},
		{
			content: "[\"2\", \"docs: Add README w/ | template sections\", \"Some | comment...\"]",
			field1:  "2",
			field2:  "docs: Add README w/ | template sections",
			field3:  "Some | comment...",
		},
	}
	for _, test := range tt {
		action := &activities_model.Action{Content: test.content}
		issueInfos := action.GetIssueInfos()
		assert.Equal(t, test.field1, issueInfos[0])
		assert.Equal(t, test.field2, issueInfos[1])
		assert.Equal(t, test.field3, issueInfos[2])
	}
}

func TestIsPrivate(t *testing.T) {
	defer unittest.OverrideFixtures("models/activities/fixtures/TestIsPrivate")()
	require.NoError(t, unittest.PrepareTestDatabase())

	tt := []struct {
		activityID int64
		private    bool
	}{
		{1, true},  // private repo
		{3, false}, // public activities, public repo
		{11, true}, // private activities
	}

	for _, test := range tt {
		ctx := t.Context()
		action, err := activities_model.GetActivityByID(ctx, test.activityID)
		require.NoError(t, err)

		private, err := action.IsActionPrivate(ctx)
		require.NoError(t, err)

		assert.Equal(t, test.private, private, "action ID: %d", test.activityID)
	}
}

func TestLoadActUserSystemUsers(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	t.Run("Single", func(t *testing.T) {
		action := &activities_model.Action{ActUserID: user_model.ActionsUserID}
		action.LoadActUser(t.Context())
		assert.True(t, action.ActUser.IsActions())

		deleted := &activities_model.Action{ActUserID: 9999}
		deleted.LoadActUser(t.Context())
		assert.True(t, deleted.ActUser.IsGhost())
	})

	t.Run("List", func(t *testing.T) {
		actions := activities_model.ActionList{
			{ActUserID: user_model.ActionsUserID},
			{ActUserID: 2},
		}
		_, err := actions.LoadActUsers(t.Context())
		require.NoError(t, err)
		assert.True(t, actions[0].ActUser.IsActions())
		assert.Equal(t, "user2", actions[1].ActUser.Name)
	})
}
