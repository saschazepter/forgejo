// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package user

import (
	"testing"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	org_model "forgejo.org/models/organization"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/validation"

	"github.com/gobwas/glob"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminAddOrSetPrimaryEmailAddress(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 27})

	emails, err := user_model.GetEmailAddresses(db.DefaultContext, user.ID)
	require.NoError(t, err)
	assert.Len(t, emails, 1)

	primary, err := user_model.GetPrimaryEmailAddressOfUser(db.DefaultContext, user.ID)
	require.NoError(t, err)
	assert.NotEqual(t, "new-primary@example.com", primary.Email)
	assert.Equal(t, user.Email, primary.Email)

	require.NoError(t, AdminAddOrSetPrimaryEmailAddress(db.DefaultContext, user, "new-primary@example.com"))

	primary, err = user_model.GetPrimaryEmailAddressOfUser(db.DefaultContext, user.ID)
	require.NoError(t, err)
	assert.Equal(t, "new-primary@example.com", primary.Email)
	assert.Equal(t, user.Email, primary.Email)

	emails, err = user_model.GetEmailAddresses(db.DefaultContext, user.ID)
	require.NoError(t, err)
	assert.Len(t, emails, 2)

	setting.Service.EmailDomainAllowList = []glob.Glob{glob.MustCompile("example.org")}
	defer func() {
		setting.Service.EmailDomainAllowList = []glob.Glob{}
	}()

	require.NoError(t, AdminAddOrSetPrimaryEmailAddress(db.DefaultContext, user, "new-primary2@example2.com"))

	primary, err = user_model.GetPrimaryEmailAddressOfUser(db.DefaultContext, user.ID)
	require.NoError(t, err)
	assert.Equal(t, "new-primary2@example2.com", primary.Email)
	assert.Equal(t, user.Email, primary.Email)

	require.NoError(t, AdminAddOrSetPrimaryEmailAddress(db.DefaultContext, user, "user27@example.com"))

	primary, err = user_model.GetPrimaryEmailAddressOfUser(db.DefaultContext, user.ID)
	require.NoError(t, err)
	assert.Equal(t, "user27@example.com", primary.Email)
	assert.Equal(t, user.Email, primary.Email)

	emails, err = user_model.GetEmailAddresses(db.DefaultContext, user.ID)
	require.NoError(t, err)
	assert.Len(t, emails, 3)
}

func TestReplacePrimaryEmailAddress(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	t.Run("User", func(t *testing.T) {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 13})

		emails, err := user_model.GetEmailAddresses(db.DefaultContext, user.ID)
		require.NoError(t, err)
		assert.Len(t, emails, 1)

		primary, err := user_model.GetPrimaryEmailAddressOfUser(db.DefaultContext, user.ID)
		require.NoError(t, err)
		assert.NotEqual(t, "primary-13@example.com", primary.Email)
		assert.Equal(t, user.Email, primary.Email)

		require.NoError(t, ReplacePrimaryEmailAddress(db.DefaultContext, user, "primary-13@example.com"))

		primary, err = user_model.GetPrimaryEmailAddressOfUser(db.DefaultContext, user.ID)
		require.NoError(t, err)
		assert.Equal(t, "primary-13@example.com", primary.Email)
		assert.Equal(t, user.Email, primary.Email)

		emails, err = user_model.GetEmailAddresses(db.DefaultContext, user.ID)
		require.NoError(t, err)
		assert.Len(t, emails, 1)

		require.NoError(t, ReplacePrimaryEmailAddress(db.DefaultContext, user, "primary-13@example.com"))
	})

	t.Run("Organization", func(t *testing.T) {
		org := unittest.AssertExistsAndLoadBean(t, &org_model.Organization{ID: 3})

		assert.Equal(t, "org3@example.com", org.Email)

		require.NoError(t, ReplacePrimaryEmailAddress(db.DefaultContext, org.AsUser(), "primary-org@example.com"))

		assert.Equal(t, "primary-org@example.com", org.Email)
	})
}

func TestAddEmailAddresses(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	require.Error(t, AddEmailAddresses(db.DefaultContext, user, []string{" invalid email "}))

	emails := []string{"user1234@example.com", "user5678@example.com"}

	require.NoError(t, AddEmailAddresses(db.DefaultContext, user, emails))

	err := AddEmailAddresses(db.DefaultContext, user, emails)
	require.Error(t, err)
	assert.True(t, user_model.IsErrEmailAlreadyUsed(err))
}

func TestReplaceInactivePrimaryEmail(t *testing.T) {
	defer unittest.OverrideFixtures("services/user/TestReplaceInactivePrimaryEmail/")()
	require.NoError(t, unittest.PrepareTestDatabase())

	t.Run("User doesn't exist", func(t *testing.T) {
		email := &user_model.EmailAddress{
			Email: "user9999999@example.com",
			UID:   9999999,
		}
		err := ReplaceInactivePrimaryEmail(db.DefaultContext, "user10@example.com", email)
		require.Error(t, err)
		assert.True(t, user_model.IsErrUserNotExist(err))
	})

	t.Run("Normal", func(t *testing.T) {
		unittest.AssertExistsIf(t, true, &auth_model.AuthorizationToken{UID: 10})

		email := &user_model.EmailAddress{
			Email: "user201@example.com",
			UID:   10,
		}
		err := ReplaceInactivePrimaryEmail(db.DefaultContext, "user10@example.com", email)
		require.NoError(t, err)

		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 10})
		assert.Equal(t, "user201@example.com", user.Email)

		unittest.AssertExistsIf(t, false, &auth_model.AuthorizationToken{UID: 10})
	})
}

func TestDeleteEmailAddresses(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	emails := []string{"user2-2@example.com"}

	err := DeleteEmailAddresses(db.DefaultContext, user, emails)
	require.NoError(t, err)

	err = DeleteEmailAddresses(db.DefaultContext, user, emails)
	require.NoError(t, err)

	emails = []string{"user2@example.com"}

	err = DeleteEmailAddresses(db.DefaultContext, user, emails)
	require.Error(t, err)
	assert.True(t, user_model.IsErrPrimaryEmailCannotDelete(err))
}

func TestMakeEmailAddressPrimary(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	newPrimaryEmail := unittest.AssertExistsAndLoadBean(t, &user_model.EmailAddress{ID: 35, UID: user.ID}, "is_primary = false", "is_activated = false")

	t.Run("Not activated", func(t *testing.T) {
		require.ErrorIs(t, MakeEmailAddressPrimary(db.DefaultContext, user, newPrimaryEmail, false), validation.ErrEmailNotActivated)

		unittest.AssertExistsIf(t, true, &user_model.User{ID: 2, Email: "user2@example.com"})
		unittest.AssertExistsIf(t, true, &user_model.EmailAddress{ID: 3, UID: user.ID, IsPrimary: true})
		unittest.AssertExistsIf(t, true, &user_model.EmailAddress{ID: 35, UID: user.ID}, "is_primary = false")
	})

	t.Run("Activated", func(t *testing.T) {
		require.NoError(t, user_model.ActivateEmail(db.DefaultContext, newPrimaryEmail))
		require.NoError(t, MakeEmailAddressPrimary(db.DefaultContext, user, newPrimaryEmail, false))

		unittest.AssertExistsIf(t, true, &user_model.User{ID: 2, Email: newPrimaryEmail.Email})
		unittest.AssertExistsIf(t, true, &user_model.EmailAddress{ID: 3, UID: user.ID}, "is_primary = false")
		unittest.AssertExistsIf(t, true, &user_model.EmailAddress{ID: 35, UID: user.ID, IsPrimary: true})
	})
}
