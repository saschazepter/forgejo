// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package activities

import (
	"testing"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	"forgejo.org/modules/optional"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadAttributes(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	release := unittest.AssertExistsAndLoadBean(t, &repo_model.Release{ID: 1})
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})

	require.NoError(t, CreateOrUpdateIssueNotifications(db.DefaultContext, issue.ID, 0, 2, 0))
	require.NoError(t, CreateOrUpdateReleaseNotifications(db.DefaultContext, release.ID))

	notifications, err := db.Find[Notification](t.Context(), FindNotificationOptions{RepoID: release.RepoID})
	require.NoError(t, err)
	assert.Len(t, notifications, 12)

	err = NotificationList(notifications).LoadAttributes(t.Context())
	require.NoError(t, err)
	for _, notification := range notifications {
		assert.Equal(t, notification.UserID, notification.User.ID)

		if notification.ReleaseID.Has() {
			assert.Equal(t, notification.ReleaseID.ValueOrZeroValue(), notification.Release.ID)
		} else {
			assert.Nil(t, notification.Release)
		}

		if notification.IssueID != 0 {
			assert.Equal(t, notification.IssueID, notification.Issue.ID)
		} else {
			assert.Nil(t, notification.Issue)
		}
	}
}

func TestLoadReleases(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	release := unittest.AssertExistsAndLoadBean(t, &repo_model.Release{ID: 1})
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})

	require.NoError(t, CreateOrUpdateIssueNotifications(db.DefaultContext, issue.ID, 0, 2, 0))
	require.NoError(t, CreateOrUpdateReleaseNotifications(db.DefaultContext, release.ID))

	notifications, err := db.Find[Notification](t.Context(), FindNotificationOptions{RepoID: release.RepoID})
	require.NoError(t, err)
	assert.Len(t, notifications, 12)

	// insert an erroneous release id in the list of notifications. How mean!
	notifications[0].ReleaseID = optional.Some(int64(1234567))

	failures, err := NotificationList(notifications).LoadReleases(t.Context())
	require.NoError(t, err)

	assert.Len(t, failures, 1)
	assert.Equal(t, 0, failures[0])

	for i, notification := range notifications {
		if notification.ReleaseID.Has() && i != 0 {
			assert.Equal(t, notification.ReleaseID.ValueOrZeroValue(), notification.Release.ID)
		} else {
			assert.Nil(t, notification.Release)
		}
	}
}
