// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/url"
	"testing"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/tests"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Cmd_AdminUser(t *testing.T) {
	onGiteaRun(t, func(*testing.T, *url.URL) {
		for i, testCase := range []struct {
			name               string
			options            []string
			mustChangePassword bool
		}{
			{
				name:               "default",
				options:            []string{},
				mustChangePassword: true,
			},
			{
				name:               "--must-change-password=false",
				options:            []string{"--must-change-password=false"},
				mustChangePassword: false,
			},
			{
				name:               "--must-change-password=true",
				options:            []string{"--must-change-password=true"},
				mustChangePassword: true,
			},
			{
				name:               "--must-change-password",
				options:            []string{"--must-change-password"},
				mustChangePassword: true,
			},
		} {
			t.Run(testCase.name, func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				name := fmt.Sprintf("testuser%d", i)

				options := []string{"user", "create", "--username", name, "--password", "password", "--email", name + "@example.com"}
				options = append(options, testCase.options...)
				output, err := runMainApp("admin", options...)
				require.NoError(t, err)
				assert.Contains(t, output, "has been successfully created")
				user := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: name})
				assert.Equal(t, testCase.mustChangePassword, user.MustChangePassword)

				options = []string{"user", "change-password", "--username", name, "--password", "password"}
				options = append(options, testCase.options...)
				output, err = runMainApp("admin", options...)
				require.NoError(t, err)
				assert.Contains(t, output, "has been successfully updated")
				user = unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: name})
				assert.Equal(t, testCase.mustChangePassword, user.MustChangePassword)

				_, err = runMainApp("admin", "user", "delete", "--username", name)
				require.NoError(t, err)
				unittest.AssertNotExistsBean(t, &user_model.User{Name: name})
			})
		}
	})
}

func Test_Cmd_AdminFirstUser(t *testing.T) {
	onGiteaRun(t, func(*testing.T, *url.URL) {
		for _, testCase := range []struct {
			name               string
			options            []string
			mustChangePassword bool
			isAdmin            bool
		}{
			{
				name:               "default",
				options:            []string{},
				mustChangePassword: false,
				isAdmin:            false,
			},
			{
				name:               "--must-change-password=false",
				options:            []string{"--must-change-password=false"},
				mustChangePassword: false,
				isAdmin:            false,
			},
			{
				name:               "--must-change-password=true",
				options:            []string{"--must-change-password=true"},
				mustChangePassword: true,
				isAdmin:            false,
			},
			{
				name:               "--must-change-password",
				options:            []string{"--must-change-password"},
				mustChangePassword: true,
				isAdmin:            false,
			},
			{
				name:               "--admin default",
				options:            []string{"--admin"},
				mustChangePassword: false,
				isAdmin:            true,
			},
			{
				name:               "--admin --must-change-password=false",
				options:            []string{"--admin", "--must-change-password=false"},
				mustChangePassword: false,
				isAdmin:            true,
			},
			{
				name:               "--admin --must-change-password=true",
				options:            []string{"--admin", "--must-change-password=true"},
				mustChangePassword: true,
				isAdmin:            true,
			},
			{
				name:               "--admin --must-change-password",
				options:            []string{"--admin", "--must-change-password"},
				mustChangePassword: true,
				isAdmin:            true,
			},
		} {
			t.Run(testCase.name, func(t *testing.T) {
				db.GetEngine(db.DefaultContext).Exec("DELETE FROM `user`")
				db.GetEngine(db.DefaultContext).Exec("DELETE FROM `email_address`")
				assert.Equal(t, int64(0), user_model.CountUsers(db.DefaultContext, nil))
				name := "testuser"

				options := []string{"user", "create", "--username", name, "--password", "password", "--email", name + "@example.com"}
				options = append(options, testCase.options...)
				output, err := runMainApp("admin", options...)
				require.NoError(t, err)
				assert.Contains(t, output, "has been successfully created")
				user := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: name})
				assert.Equal(t, testCase.mustChangePassword, user.MustChangePassword)
				assert.Equal(t, testCase.isAdmin, user.IsAdmin)
			})
		}
	})
}

func Test_Cmd_AdminUserResetMFA(t *testing.T) {
	onGiteaRun(t, func(*testing.T, *url.URL) {
		name := "testuser"

		options := []string{"user", "create", "--username", name, "--password", "password", "--email", name + "@example.com"}
		output, err := runMainApp("admin", options...)
		require.NoError(t, err)
		assert.Contains(t, output, "has been successfully created")
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: name})

		twoFactor := &auth_model.TwoFactor{
			UID: user.ID,
		}
		token := twoFactor.GenerateScratchToken()
		require.NoError(t, auth_model.NewTwoFactor(t.Context(), twoFactor, token))
		twoFactor, err = auth_model.GetTwoFactorByUID(t.Context(), user.ID)
		require.NoError(t, err)
		require.NotNil(t, twoFactor)

		authn, err := auth_model.CreateCredential(t.Context(), user.ID, "test", &webauthn.Credential{})
		require.NoError(t, err)

		options = []string{"user", "reset-mfa", "--username", name}
		output, err = runMainApp("admin", options...)
		require.NoError(t, err)
		assert.Contains(t, output, "two-factor authentication settings have been removed")

		_, err = auth_model.GetTwoFactorByUID(t.Context(), user.ID)
		require.ErrorContains(t, err, "user not enrolled in 2FA")

		_, err = auth_model.GetWebAuthnCredentialByID(t.Context(), authn.ID)
		require.ErrorContains(t, err, "WebAuthn credential does not exist")

		_, err = runMainApp("admin", "user", "delete", "--username", name)
		require.NoError(t, err)
	})
}
