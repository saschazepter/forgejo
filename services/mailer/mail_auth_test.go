// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mailer_test

import (
	"testing"

	"forgejo.org/models/db"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/translation"
	"forgejo.org/services/mailer"
	user_service "forgejo.org/services/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPasswordChangeMail(t *testing.T) {
	defer require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	called := false
	defer mailer.MockMailSettings(func(msgs ...*mailer.Message) {
		assert.Len(t, msgs, 1)
		assert.Equal(t, user.EmailTo(), msgs[0].To)
		assert.EqualValues(t, translation.NewLocale("en-US").Tr("mail.password_change.subject"), msgs[0].Subject)
		mailer.AssertTranslatedLocale(t, msgs[0].Body, "mail.password_change.text_1", "mail.password_change.text_2", "mail.password_change.text_3")
		called = true
	})()

	require.NoError(t, user_service.UpdateAuth(db.DefaultContext, user, &user_service.UpdateAuthOptions{Password: optional.Some("NewPasswordYolo!")}))
	assert.True(t, called)
}

func TestPrimaryMailChange(t *testing.T) {
	defer require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	firstEmail := unittest.AssertExistsAndLoadBean(t, &user_model.EmailAddress{ID: 3, UID: user.ID, IsPrimary: true})
	secondEmail := unittest.AssertExistsAndLoadBean(t, &user_model.EmailAddress{ID: 35, UID: user.ID}, "is_primary = false")
	require.NoError(t, user_model.ActivateEmail(db.DefaultContext, secondEmail))

	called := false
	defer mailer.MockMailSettings(func(msgs ...*mailer.Message) {
		assert.False(t, called)
		assert.Len(t, msgs, 1)
		assert.Equal(t, user.EmailTo(firstEmail.Email), msgs[0].To)
		assert.EqualValues(t, translation.NewLocale("en-US").Tr("mail.primary_mail_change.subject"), msgs[0].Subject)
		assert.Contains(t, msgs[0].Body, secondEmail.Email)
		assert.Contains(t, msgs[0].Body, setting.AppURL)
		mailer.AssertTranslatedLocale(t, msgs[0].Body, "mail.primary_mail_change.text_1", "mail.primary_mail_change.text_2", "mail.primary_mail_change.text_3")
		called = true
	})()

	require.NoError(t, user_service.MakeEmailAddressPrimary(db.DefaultContext, user, secondEmail, true))
	assert.True(t, called)

	require.NoError(t, user_service.MakeEmailAddressPrimary(db.DefaultContext, user, firstEmail, false))
}
