// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mailer

import (
	"strconv"
	"testing"

	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminNotificationMail_test(t *testing.T) {
	t.Run("SendNotificationEmailOnNewUser_true", func(t *testing.T) {
		require.NoError(t, unittest.PrepareTestDatabase())

		newUser := new(user_model.User)
		newUser.Name = "new_user"
		newUser.Language = "en_US"
		newUser.IsAdmin = false
		newUser.Email = "new_user@example.com"
		newUser.LastLoginUnix = 1693648327
		newUser.CreatedUnix = 1693648027
		require.NoError(t, user_model.CreateUser(t.Context(), newUser))

		defer test.MockVariableValue(&setting.Admin.SendNotificationEmailOnNewUser, true)()

		called := false
		defer MockMailSettings(func(msgs ...*Message) {
			assert.Len(t, msgs, 1, "Test provides only one admin user, so only one email must be sent")
			assert.Equal(t, "user1@example.com", msgs[0].To, "checks if the recipient is the admin of the instance")
			manageUserURL := setting.AppURL + "admin/users/" + strconv.FormatInt(newUser.ID, 10)
			assert.Contains(t, msgs[0].Body, manageUserURL)
			assert.Contains(t, msgs[0].Body, newUser.HTMLURL())
			assert.Contains(t, msgs[0].Body, newUser.Name, "user name of the newly created user")
			AssertTranslatedLocale(t, msgs[0].Body, "mail.admin", "admin.users")
			called = true
		})()

		MailNewUser(t.Context(), newUser)

		assert.True(t, called)
	})

	t.Run("SendNotificationEmailOnNewUser_false", func(t *testing.T) {
		require.NoError(t, unittest.PrepareTestDatabase())

		newUser := new(user_model.User)
		newUser.Name = "new_user"
		newUser.Language = "en_US"
		newUser.IsAdmin = false
		newUser.Email = "new_user@example.com"
		newUser.LastLoginUnix = 1693648327
		newUser.CreatedUnix = 1693648027
		require.NoError(t, user_model.CreateUser(t.Context(), newUser))

		defer test.MockVariableValue(&setting.Admin.SendNotificationEmailOnNewUser, false)()
		defer MockMailSettings(func(msgs ...*Message) {
			require.Fail(t, "no email should have been sent")
		})()

		MailNewUser(t.Context(), newUser)
	})
}
