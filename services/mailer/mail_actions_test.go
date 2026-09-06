// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package mailer

import (
	"testing"

	actions_model "forgejo.org/models/actions"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_sendActionRunJobFailureNotification(t *testing.T) {
	t.Run("Skipped if not failed", func(t *testing.T) {
		require.NoError(t, unittest.PrepareTestDatabase())

		defer MockMailSettings(func(msgs ...*Message) { require.Fail(t, "an email was sent") })()

		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		repo62 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 62})

		run := &actions_model.ActionRun{Title: "ci.yaml", NotifyEmail: true, TriggerUser: user2, Repo: repo62}
		job := &actions_model.ActionRunJob{Name: "build", Status: actions_model.StatusCancelled, Run: run}

		unittest.AssertSuccessfulInsert(t, run, job)

		// Notifications expect a job with its attributes.
		require.NoError(t, job.LoadAttributes(t.Context()))

		require.NoError(t, sendActionRunJobFailureNotification(t.Context(), job))
	})

	t.Run("Skipped if mail disabled", func(t *testing.T) {
		require.NoError(t, unittest.PrepareTestDatabase())

		defer MockMailSettings(func(msgs ...*Message) { require.Fail(t, "an email was sent") })()
		defer test.MockVariableValue(&setting.MailService, nil)()

		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		repo62 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 62})

		run := &actions_model.ActionRun{Title: "ci.yaml", NotifyEmail: true, TriggerUser: user2, Repo: repo62}
		job := &actions_model.ActionRunJob{Name: "build", Status: actions_model.StatusFailure, Run: run}

		unittest.AssertSuccessfulInsert(t, run, job)

		// Notifications expect a job with its attributes.
		require.NoError(t, job.LoadAttributes(t.Context()))

		require.NoError(t, sendActionRunJobFailureNotification(t.Context(), job))
	})

	t.Run("Skipped if run notifications disabled", func(t *testing.T) {
		require.NoError(t, unittest.PrepareTestDatabase())

		defer MockMailSettings(func(msgs ...*Message) { require.Fail(t, "an email was sent") })()

		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		repo62 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 62})

		run := &actions_model.ActionRun{Title: "ci.yaml", NotifyEmail: false, TriggerUser: user2, Repo: repo62}
		job := &actions_model.ActionRunJob{Name: "build", Status: actions_model.StatusFailure, Run: run}

		unittest.AssertSuccessfulInsert(t, run, job)

		// Notifications expect a job with its attributes.
		require.NoError(t, job.LoadAttributes(t.Context()))

		require.NoError(t, sendActionRunJobFailureNotification(t.Context(), job))
	})

	t.Run("Skipped if user disabled notifications", func(t *testing.T) {
		require.NoError(t, unittest.PrepareTestDatabase())

		defer MockMailSettings(func(msgs ...*Message) { require.Fail(t, "an email was sent") })()

		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		repo62 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 62})

		user2.EmailNotificationsPreference = user_model.EmailNotificationsDisabled

		run := &actions_model.ActionRun{Title: "ci.yaml", NotifyEmail: true, TriggerUser: user2, Repo: repo62}
		job := &actions_model.ActionRunJob{Name: "build", Status: actions_model.StatusFailure, Run: run}

		unittest.AssertSuccessfulInsert(t, run, job)

		// Notifications expect a job with its attributes.
		require.NoError(t, job.LoadAttributes(t.Context()))

		require.NoError(t, sendActionRunJobFailureNotification(t.Context(), job))
	})

	t.Run("Skipped if user has no email", func(t *testing.T) {
		require.NoError(t, unittest.PrepareTestDatabase())

		defer MockMailSettings(func(msgs ...*Message) { require.Fail(t, "an email was sent") })()

		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		repo62 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 62})

		user2.Email = ""

		run := &actions_model.ActionRun{Title: "ci.yaml", NotifyEmail: true, TriggerUser: user2, Repo: repo62}
		job := &actions_model.ActionRunJob{Name: "build", Status: actions_model.StatusFailure, Run: run}

		unittest.AssertSuccessfulInsert(t, run, job)

		// Notifications expect a job with its attributes.
		require.NoError(t, job.LoadAttributes(t.Context()))

		require.NoError(t, sendActionRunJobFailureNotification(t.Context(), job))
	})

	t.Run("To repo owner if triggered by system", func(t *testing.T) {
		require.NoError(t, unittest.PrepareTestDatabase())

		var messages []*Message
		defer MockMailSettings(func(msgs ...*Message) {
			messages = msgs
		})()

		actionsUser := user_model.NewActionsUser()
		repo62 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 62})

		run := &actions_model.ActionRun{Title: "ci.yaml", NotifyEmail: true, TriggerUser: actionsUser, Repo: repo62}
		job := &actions_model.ActionRunJob{
			Name:    "build",
			Status:  actions_model.StatusFailure,
			Run:     run,
			Started: 1788443525,
			Stopped: 1788443590,
		}

		unittest.AssertSuccessfulInsert(t, run, job)

		// Notifications expect a job with its attributes.
		require.NoError(t, job.LoadAttributes(t.Context()))

		require.NoError(t, sendActionRunJobFailureNotification(t.Context(), job))

		assert.Len(t, messages, 1)
		assert.Equal(t, `"User Two" <user2@example.com>`, messages[0].To)
		assert.Equal(t, "[user2/test_workflows] Workflow job failed: build", messages[0].Subject)
		assert.Contains(t, messages[0].Body, `The workflow job "build" has failed.`)
		assert.Contains(t, messages[0].Body,
			`Repository: <a href="https://try.gitea.io/user2/test_workflows">user2/test_workflows</a>`)
		assert.Contains(t, messages[0].Body,
			`Run: <a href="https://try.gitea.io/user2/test_workflows/actions/runs/0">ci.yaml</a>`)
		assert.Contains(t, messages[0].Body,
			`Job: <a href="https://try.gitea.io/user2/test_workflows/actions/runs/0/jobs/0/attempt/1">build</a>`)
		assert.Contains(t, messages[0].Body, "Duration: 1m5s")
		assert.Contains(t, messages[0].Body, "Finished: 2026-09-03 13:53:10 +00:00")
		assert.Contains(t, messages[0].Body,
			`View results on https://try.gitea.io/user2/test_workflows/actions/runs/0/jobs/0/attempt/1.`)
	})

	t.Run("To triggering user", func(t *testing.T) {
		require.NoError(t, unittest.PrepareTestDatabase())

		var messages []*Message
		defer MockMailSettings(func(msgs ...*Message) {
			messages = msgs
		})()

		user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
		repo62 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 62})

		run := &actions_model.ActionRun{Title: "testing.yaml", NotifyEmail: true, TriggerUser: user1, Repo: repo62}
		job := &actions_model.ActionRunJob{
			Name:    "test",
			Status:  actions_model.StatusFailure,
			Run:     run,
			Started: 1788443525,
			Stopped: 1788443591,
		}

		unittest.AssertSuccessfulInsert(t, run, job)

		// Notifications expect a job with its attributes.
		require.NoError(t, job.LoadAttributes(t.Context()))

		require.NoError(t, sendActionRunJobFailureNotification(t.Context(), job))

		assert.Len(t, messages, 1)
		assert.Equal(t, `"User One" <user1@example.com>`, messages[0].To)
		assert.Equal(t, "[user2/test_workflows] Workflow job failed: test", messages[0].Subject)
		assert.Contains(t, messages[0].Body, `The workflow job "test" has failed.`)
		assert.Contains(t, messages[0].Body,
			`Repository: <a href="https://try.gitea.io/user2/test_workflows">user2/test_workflows</a>`)
		assert.Contains(t, messages[0].Body,
			`Run: <a href="https://try.gitea.io/user2/test_workflows/actions/runs/0">testing.yaml</a>`)
		assert.Contains(t, messages[0].Body,
			`Job: <a href="https://try.gitea.io/user2/test_workflows/actions/runs/0/jobs/0/attempt/1">test</a>`)
		assert.Contains(t, messages[0].Body, "Duration: 1m6s")
		assert.Contains(t, messages[0].Body, "Finished: 2026-09-03 13:53:11 +00:00")
		assert.Contains(t, messages[0].Body,
			`View results on https://try.gitea.io/user2/test_workflows/actions/runs/0/jobs/0/attempt/1.`)
	})
}
