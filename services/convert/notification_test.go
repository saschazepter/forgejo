// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package convert

import (
	"fmt"
	"strings"
	"testing"

	activities_model "forgejo.org/models/activities"
	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToNotificationThread(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	release := unittest.AssertExistsAndLoadBean(t, &repo_model.Release{ID: 1})
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})

	require.NoError(t, activities_model.CreateOrUpdateIssueNotifications(db.DefaultContext, issue.ID, 0, 2, 0))
	require.NoError(t, activities_model.CreateOrUpdateReleaseNotifications(db.DefaultContext, release.ID))

	notifications, err := db.Find[activities_model.Notification](t.Context(), activities_model.FindNotificationOptions{RepoID: release.RepoID})
	require.NoError(t, err)
	assert.Len(t, notifications, 12)
	err = activities_model.NotificationList(notifications).LoadAttributes(t.Context())
	require.NoError(t, err)

	for _, notification := range notifications {
		converted := ToNotificationThread(t.Context(), notification)
		assert.Equal(t, notification.RepoID, converted.Repository.ID)

		if notification.ReleaseID.Has() {
			assert.Equal(t, notification.ReleaseID.ValueOrZeroValue(), converted.Release.ID)
			assert.True(t, strings.HasSuffix(converted.Subject.URL, fmt.Sprintf("/api/v1/repos/user2/repo1/releases/%d", converted.Release.ID)))
		} else {
			assert.Nil(t, converted.Release)
		}
	}
}
