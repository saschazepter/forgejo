// Copyright 2017 The Gitea Authors. All rights reserved.
// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package activities

import (
	"context"
	"testing"
	"time"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/timeutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateOrUpdateIssueNotifications(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})

	require.NoError(t, CreateOrUpdateIssueNotifications(db.DefaultContext, issue.ID, 0, 2, 0))

	// User 9 is inactive, thus notifications for user 1 and 4 are created
	notf := unittest.AssertExistsAndLoadBean(t, &Notification{UserID: 1, IssueID: issue.ID})
	assert.Equal(t, NotificationStatusUnread, notf.Status)
	unittest.CheckConsistencyFor(t, &issues_model.Issue{ID: issue.ID})

	notf = unittest.AssertExistsAndLoadBean(t, &Notification{UserID: 4, IssueID: issue.ID})
	assert.Equal(t, NotificationStatusUnread, notf.Status)
}

func TestCreateOrUpdateReleaseNotifications(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	release := unittest.AssertExistsAndLoadBean(t, &repo_model.Release{ID: 1})

	// user8 only watches issues and PRs, not releases
	updated, err := db.GetEngine(t.Context()).Table("watch").Update(
		&repo_model.Watch{WatchSelectionIssues: true, WatchSelectionPullRequests: true, WatchSelectionReleases: false},
		&repo_model.Watch{UserID: 8, RepoID: 1},
	)
	require.NoError(t, err)
	assert.Equal(t, int64(1), updated)

	require.NoError(t, CreateOrUpdateReleaseNotifications(db.DefaultContext, release.ID))

	notif, found, err := GetReleaseNotification(t.Context(), 1, release.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, NotificationStatusUnread, notif.Status)
	notif, found, err = GetReleaseNotification(t.Context(), 4, release.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, NotificationStatusUnread, notif.Status)
	notif, found, err = GetReleaseNotification(t.Context(), 8, release.ID)
	require.NoError(t, err)
	require.False(t, found)
	assert.Nil(t, notif)
}

func TestNotificationsForUser(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	notfs, err := db.Find[Notification](db.DefaultContext, FindNotificationOptions{
		UserID: user.ID,
		Status: []NotificationStatus{
			NotificationStatusRead,
			NotificationStatusUnread,
		},
	})
	require.NoError(t, err)
	if assert.Len(t, notfs, 3) {
		assert.EqualValues(t, 5, notfs[0].ID)
		assert.Equal(t, user.ID, notfs[0].UserID)
		assert.EqualValues(t, 4, notfs[1].ID)
		assert.Equal(t, user.ID, notfs[1].UserID)
		assert.EqualValues(t, 2, notfs[2].ID)
		assert.Equal(t, user.ID, notfs[2].UserID)
	}
}

func TestNotification_GetRepo(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	notf := unittest.AssertExistsAndLoadBean(t, &Notification{RepoID: 1})
	repo, err := notf.GetRepo(db.DefaultContext)
	require.NoError(t, err)
	assert.Equal(t, repo, notf.Repository)
	assert.Equal(t, notf.RepoID, repo.ID)
}

func TestNotification_GetIssue(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	notf := unittest.AssertExistsAndLoadBean(t, &Notification{RepoID: 1})
	issue, err := notf.GetIssue(db.DefaultContext)
	require.NoError(t, err)
	assert.Equal(t, issue, notf.Issue)
	assert.Equal(t, notf.IssueID, issue.ID)
}

func TestGetNotificationCount(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	cnt, err := db.Count[Notification](db.DefaultContext, FindNotificationOptions{
		UserID: user.ID,
		Status: []NotificationStatus{
			NotificationStatusRead,
		},
	})
	require.NoError(t, err)
	assert.EqualValues(t, 0, cnt)

	cnt, err = db.Count[Notification](db.DefaultContext, FindNotificationOptions{
		UserID: user.ID,
		Status: []NotificationStatus{
			NotificationStatusUnread,
		},
	})
	require.NoError(t, err)
	assert.EqualValues(t, 1, cnt)
}

func TestSetNotificationStatus(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	notf := unittest.AssertExistsAndLoadBean(t,
		&Notification{UserID: user.ID, Status: NotificationStatusRead})
	_, err := SetNotificationStatus(db.DefaultContext, notf.ID, user, NotificationStatusPinned)
	require.NoError(t, err)
	unittest.AssertExistsAndLoadBean(t,
		&Notification{ID: notf.ID, Status: NotificationStatusPinned})

	_, err = SetNotificationStatus(db.DefaultContext, 1, user, NotificationStatusRead)
	require.Error(t, err)
	_, err = SetNotificationStatus(db.DefaultContext, unittest.NonexistentID, user, NotificationStatusRead)
	require.Error(t, err)
}

func TestUpdateNotificationStatuses(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	notfUnread := unittest.AssertExistsAndLoadBean(t,
		&Notification{UserID: user.ID, Status: NotificationStatusUnread})
	notfRead := unittest.AssertExistsAndLoadBean(t,
		&Notification{UserID: user.ID, Status: NotificationStatusRead})
	notfPinned := unittest.AssertExistsAndLoadBean(t,
		&Notification{UserID: user.ID, Status: NotificationStatusPinned})
	require.NoError(t, UpdateNotificationStatuses(db.DefaultContext, user, NotificationStatusUnread, NotificationStatusRead))
	unittest.AssertExistsAndLoadBean(t,
		&Notification{ID: notfUnread.ID, Status: NotificationStatusRead})
	unittest.AssertExistsAndLoadBean(t,
		&Notification{ID: notfRead.ID, Status: NotificationStatusRead})
	unittest.AssertExistsAndLoadBean(t,
		&Notification{ID: notfPinned.ID, Status: NotificationStatusPinned})
}

func TestSetIssueReadBy(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})
	require.NoError(t, db.WithTx(db.DefaultContext, func(ctx context.Context) error {
		return SetIssueReadBy(ctx, issue.ID, user.ID)
	}))

	nt, err := GetIssueNotification(db.DefaultContext, user.ID, issue.ID)
	require.NoError(t, err)
	assert.Equal(t, NotificationStatusRead, nt.Status)
}

func TestUpdateIssueNotification(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	timeutil.MockSet(now)
	defer timeutil.MockUnset()

	t.Run("Read notification", func(t *testing.T) {
		require.NoError(t, updateIssueNotification(t.Context(), 1, 1, 1001))

		notification := unittest.AssertExistsAndLoadBean(t, &Notification{UserID: 1, IssueID: 1})
		assert.Equal(t, NotificationStatusUnread, notification.Status)
		assert.EqualValues(t, 0, notification.CommentID)
		assert.Equal(t, timeutil.TimeStamp(now.Unix()), notification.UpdatedUnix)
	})
	t.Run("Unread notification", func(t *testing.T) {
		beforeUpdateUnix := unittest.AssertExistsAndLoadBean(t, &Notification{UserID: 2, IssueID: 2}).UpdatedUnix
		require.NoError(t, updateIssueNotification(t.Context(), 2, 2, 1001))

		notification := unittest.AssertExistsAndLoadBean(t, &Notification{UserID: 2, IssueID: 2})
		assert.Equal(t, NotificationStatusUnread, notification.Status)
		assert.EqualValues(t, 1001, notification.CommentID)
		assert.NotEqual(t, beforeUpdateUnix, notification.UpdatedUnix)
	})

	t.Run("Pinned notification", func(t *testing.T) {
		require.NoError(t, updateIssueNotification(t.Context(), 1, 1, 1001))

		notification := unittest.AssertExistsAndLoadBean(t, &Notification{UserID: 1, IssueID: 1})
		assert.Equal(t, NotificationStatusUnread, notification.Status)
		assert.EqualValues(t, 0, notification.CommentID)
		assert.Equal(t, timeutil.TimeStamp(now.Unix()), notification.UpdatedUnix)
	})
}

func TestSetReleaseReadBy(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	user20 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 20})
	release := unittest.AssertExistsAndLoadBean(t, &repo_model.Release{ID: 1})

	require.NoError(t, CreateOrUpdateReleaseNotifications(db.DefaultContext, release.ID))

	// user1 is notified of a release
	notif, found, err := GetReleaseNotification(t.Context(), user1.ID, release.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, NotificationStatusUnread, notif.Status)
	// user20 isn't notified
	notif, found, err = GetReleaseNotification(t.Context(), user20.ID, release.ID)
	require.NoError(t, err)
	require.False(t, found)
	assert.Nil(t, notif)

	// user1 visits the release page
	err = SetReleaseReadBy(t.Context(), release.ID, user1.ID)
	require.NoError(t, err)

	// it marks the release as read for them
	notif, found, err = GetReleaseNotification(t.Context(), user1.ID, release.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, NotificationStatusRead, notif.Status)

	// user20 visits the release page
	err = SetReleaseReadBy(t.Context(), release.ID, user20.ID)
	require.NoError(t, err)

	// they still aren't notified
	notif, found, err = GetReleaseNotification(t.Context(), user20.ID, release.ID)
	require.NoError(t, err)
	require.False(t, found)
	assert.Nil(t, notif)
}
