// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package mailer_test

import (
	"testing"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	issue_service "forgejo.org/services/issue"
	"forgejo.org/services/mailer"
	notify_service "forgejo.org/services/notify"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloseIssue(t *testing.T) {
	defer unittest.OverrideFixtures("services/mailer/fixtures/TestCloseIssue")()
	require.NoError(t, unittest.PrepareTestDatabase())

	called := false
	defer mailer.MockMailSettings(func(msgs ...*mailer.Message) {
		require.Len(t, msgs, 3)
		msg := msgs[0]
		assert.Equal(t, "Re: [user2/repo1] issue1 (Issue #1)", msg.Subject)
		mailer.AssertTranslatedLocale(t, msg.Body, "mail.issue.action.close")
		assert.Contains(t, msg.Body, "closed #1.")
		called = true
	})()

	notifier := mailer.NewNotifier()
	notify_service.RegisterNotifier(notifier)
	defer notify_service.UnregisterNotifier(notifier)

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})
	err := issue_service.ChangeStatus(db.DefaultContext, issue, user, &issues_model.PRNotificationInfo{MergedCommitID: ""}, true)
	require.NoError(t, err)
	assert.True(t, called)
}

func TestCloseIssueByCommit(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	called := false
	defer mailer.MockMailSettings(func(msgs ...*mailer.Message) {
		require.Len(t, msgs, 3)
		msg := msgs[0]
		assert.Equal(t, "Re: [user2/repo1] issue1 (Issue #1)", msg.Subject)
		mailer.AssertTranslatedLocale(t, msg.Body, "mail.issue.action.close_by_commit")
		assert.Contains(t, msg.Body, "closed")
		assert.Contains(t, msg.Body, "#1")
		assert.Contains(t, msg.Body, "in commit")
		assert.Contains(t, msg.Body, "abc123def")
		called = true
	})()

	notifier := mailer.NewNotifier()
	notify_service.RegisterNotifier(notifier)
	defer notify_service.UnregisterNotifier(notifier)

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})
	err := issue_service.ChangeStatus(db.DefaultContext, issue, user, &issues_model.PRNotificationInfo{MergedCommitID: "abc123def"}, true)
	require.NoError(t, err)
	assert.True(t, called)
}
