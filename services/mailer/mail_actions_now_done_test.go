// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package mailer

import (
	"testing"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	notify_service "forgejo.org/services/notify"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getActionsNowDoneTestUsers(t *testing.T) []*user_model.User {
	t.Helper()
	newTriggerUser := new(user_model.User)
	newTriggerUser.Name = "new_trigger_user"
	newTriggerUser.Language = "en_US"
	newTriggerUser.IsAdmin = false
	newTriggerUser.Email = "new_trigger_user@example.com"
	newTriggerUser.LastLoginUnix = 1693648327
	newTriggerUser.CreatedUnix = 1693648027
	newTriggerUser.EmailNotificationsPreference = user_model.EmailNotificationsEnabled
	require.NoError(t, user_model.CreateUser(db.DefaultContext, newTriggerUser))

	newOwner := new(user_model.User)
	newOwner.Name = "new_owner"
	newOwner.Language = "en_US"
	newOwner.IsAdmin = false
	newOwner.Email = "new_owner@example.com"
	newOwner.LastLoginUnix = 1693648329
	newOwner.CreatedUnix = 1693648029
	newOwner.EmailNotificationsPreference = user_model.EmailNotificationsEnabled
	require.NoError(t, user_model.CreateUser(db.DefaultContext, newOwner))

	return []*user_model.User{newTriggerUser, newOwner}
}

func assertTranslatedLocaleMailActionsNowDone(t *testing.T, msgBody string) {
	AssertTranslatedLocale(t, msgBody, "mail.actions.successful_run_after_failure", "mail.actions.not_successful_run", "mail.actions.run_info_cur_status", "mail.actions.run_info_ref", "mail.actions.run_info_previous_status", "mail.actions.run_info_trigger", "mail.view_it_on")
}

func TestActionRunNowDoneNotificationMail(t *testing.T) {
	ctx := t.Context()

	users := getActionsNowDoneTestUsers(t)
	defer CleanUpUsers(ctx, users)
	triggerUser := users[0]
	ownerUser := users[1]

	repo := repo_model.Repository{
		Name:        "some repo",
		Description: "rockets are cool",
		Owner:       ownerUser,
		OwnerID:     ownerUser.ID,
	}

	// Do some funky stuff with the action run's ids:
	// The run with the larger ID finished first.
	// This is odd but something that must work.
	run1 := &actions_model.ActionRun{ID: 2, Repo: &repo, RepoID: repo.ID, Title: "some workflow", TriggerUser: triggerUser, TriggerUserID: triggerUser.ID, Status: actions_model.StatusFailure, Stopped: 1745821796, TriggerEvent: "workflow_dispatch", EnableMailNotifications: true}
	run2 := &actions_model.ActionRun{ID: 1, Repo: &repo, RepoID: repo.ID, Title: "some workflow", TriggerUser: triggerUser, TriggerUserID: triggerUser.ID, Status: actions_model.StatusSuccess, Stopped: 1745822796, TriggerEvent: "push", EnableMailNotifications: true}
	run3 := &actions_model.ActionRun{ID: 1, Repo: &repo, RepoID: repo.ID, Title: "some workflow", TriggerUser: triggerUser, TriggerUserID: triggerUser.ID, Status: actions_model.StatusSuccess, Stopped: 1745822796, TriggerEvent: "push", EnableMailNotifications: false}

	notify_service.RegisterNotifier(NewNotifier())

	t.Run("DontSendNotificationEmailOnFirstActionSuccess", func(t *testing.T) {
		defer MockMailSettings(func(msgs ...*Message) {
			assert.Fail(t, "no mail should be sent")
		})()
		notify_service.ActionRunNowDone(ctx, run2, actions_model.StatusRunning, nil)
	})

	t.Run("SendNotificationEmailOnActionRunFailed", func(t *testing.T) {
		mailSentToOwner := false
		mailSentToTriggerUser := false
		defer MockMailSettings(func(msgs ...*Message) {
			assert.LessOrEqual(t, len(msgs), 2)
			for _, msg := range msgs {
				switch msg.To {
				case triggerUser.EmailTo():
					assert.False(t, mailSentToTriggerUser, "sent mail twice")
					mailSentToTriggerUser = true
				case ownerUser.EmailTo():
					assert.False(t, mailSentToOwner, "sent mail twice")
					mailSentToOwner = true
				default:
					assert.Fail(t, "sent mail to unknown sender", msg.To)
				}
				assert.Contains(t, msg.Body, triggerUser.HTMLURL())
				assert.Contains(t, msg.Body, triggerUser.Name)
				// what happened
				assert.Contains(t, msg.Body, "failed")
				// new status of run
				assert.Contains(t, msg.Body, "failure")
				// prior status of this run
				assert.Contains(t, msg.Body, "waiting")
				assertTranslatedLocaleMailActionsNowDone(t, msg.Body)
			}
		})()
		notify_service.ActionRunNowDone(ctx, run1, actions_model.StatusWaiting, nil)
		assert.True(t, mailSentToOwner)
		assert.True(t, mailSentToTriggerUser)
	})

	t.Run("SendNotificationEmailOnActionRunRecovered", func(t *testing.T) {
		mailSentToOwner := false
		mailSentToTriggerUser := false
		defer MockMailSettings(func(msgs ...*Message) {
			assert.LessOrEqual(t, len(msgs), 2)
			for _, msg := range msgs {
				switch msg.To {
				case triggerUser.EmailTo():
					assert.False(t, mailSentToTriggerUser, "sent mail twice")
					mailSentToTriggerUser = true
				case ownerUser.EmailTo():
					assert.False(t, mailSentToOwner, "sent mail twice")
					mailSentToOwner = true
				default:
					assert.Fail(t, "sent mail to unknown sender", msg.To)
				}
				assert.Contains(t, msg.Body, triggerUser.HTMLURL())
				assert.Contains(t, msg.Body, triggerUser.Name)
				// what happened
				assert.Contains(t, msg.Body, "recovered")
				// old status of run
				assert.Contains(t, msg.Body, "failure")
				// new status of run
				assert.Contains(t, msg.Body, "success")
				// prior status of this run
				assert.Contains(t, msg.Body, "running")
				assertTranslatedLocaleMailActionsNowDone(t, msg.Body)
			}
		})()
		assert.NotNil(t, setting.MailService)

		notify_service.ActionRunNowDone(ctx, run2, actions_model.StatusRunning, run1)
		assert.True(t, mailSentToOwner)
		assert.True(t, mailSentToTriggerUser)
	})

	t.Run("SendNotificationEmailOnActionRunRecoveredWithDisabledMail", func(t *testing.T) {
		defer MockMailSettings(func(msgs ...*Message) {
			assert.Len(t, msgs, 0)
		})()
		assert.NotNil(t, setting.MailService)

		notify_service.ActionRunNowDone(ctx, run3, actions_model.StatusRunning, run1)
	})
}
