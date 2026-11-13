// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package user

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forgejo.org/models"
	asymkey_model "forgejo.org/models/asymkey"
	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	"forgejo.org/models/git"
	"forgejo.org/models/issues"
	"forgejo.org/models/moderation"
	"forgejo.org/models/organization"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/json"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"
	"forgejo.org/services/auth/source/oauth2"
	redirect_service "forgejo.org/services/redirect"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}

func TestDeleteUser(t *testing.T) {
	// Note: an earlier revision of TestDeleteUser also tested for deleting user 2, but then failed to remove them from
	// all organizations because ErrLastOrgOwner and considered a successful test -- since that didn't actually test
	// DeleteUser in any way, that case has been removed from TestDeleteUser.

	testCases := []struct {
		userID          int64
		errUserOwnRepos bool
		errContains     string
	}{
		{
			userID:      3,
			errContains: "is an organization not a user",
		},
		{
			userID: 4,
		},
		{
			userID: 8,
		},
		{
			userID:          11,
			errUserOwnRepos: true,
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("delete %d", tc.userID), func(t *testing.T) {
			defer unittest.OverrideFixtures("services/user/TestDeleteUser")()
			require.NoError(t, unittest.PrepareTestDatabase())
			user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: tc.userID})

			// Remove user from all organizations first
			orgUsers := make([]*organization.OrgUser, 0, 10)
			require.NoError(t, db.GetEngine(db.DefaultContext).Find(&orgUsers, &organization.OrgUser{UID: tc.userID}))
			for _, orgUser := range orgUsers {
				err := models.RemoveOrgUser(db.DefaultContext, orgUser.OrgID, orgUser.UID)
				require.NoError(t, err)
			}

			err := DeleteUser(db.DefaultContext, user, false)
			if tc.errUserOwnRepos {
				require.Error(t, err)
				assert.True(t, models.IsErrUserOwnRepos(err), "IsErrUserOwnRepos: %v", err)
			} else if tc.errContains != "" {
				assert.ErrorContains(t, err, tc.errContains)
			} else {
				require.NoError(t, err)
				unittest.AssertNotExistsBean(t, &user_model.User{ID: tc.userID})
				unittest.CheckConsistencyFor(t, &user_model.User{}, &repo_model.Repository{})
			}
		})
	}
}

func TestDeleteUserRetainsTrackedTime(t *testing.T) {
	defer unittest.OverrideFixtures("services/user/TestDeleteUser")()
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 8})

	err := DeleteUser(db.DefaultContext, user, false)
	require.NoError(t, err)

	time := make([]*issues.TrackedTime, 0, 10)
	err = db.GetEngine(db.DefaultContext).Find(&time, &issues.TrackedTime{IssueID: 22})
	require.NoError(t, err)
	require.Len(t, time, 1)
	tt := time[0]
	assert.EqualValues(t, 0, tt.UserID)
	assert.EqualValues(t, 22, tt.IssueID)
	assert.EqualValues(t, 401, tt.Time)

	// Make sure other tracked time wasn't affected
	time = make([]*issues.TrackedTime, 0, 10)
	err = db.GetEngine(db.DefaultContext).Find(&time, &issues.TrackedTime{UserID: 2})
	require.NoError(t, err)
	assert.Len(t, time, 5)
}

func TestDeleteUserCleansUpBranchProtectionRules(t *testing.T) {
	defer unittest.OverrideFixtures("services/user/TestDeleteUserCleansUpBranchProtectionRules")()
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 8})

	err := DeleteUser(db.DefaultContext, user, false)
	require.NoError(t, err)

	protectedBranches := make([]*git.ProtectedBranch, 0, 10)
	err = db.GetEngine(db.DefaultContext).Find(&protectedBranches, &git.ProtectedBranch{})
	require.NoError(t, err)
	require.Len(t, protectedBranches, 3)

	for _, pb := range protectedBranches {
		assert.Equal(t, []int64{1}, pb.ApprovalsWhitelistUserIDs)
		assert.Equal(t, []int64{1}, pb.WhitelistUserIDs)
		assert.Equal(t, []int64{1}, pb.MergeWhitelistUserIDs)
	}
}

func TestDeleteUserCleansUpRepoSpecificAPIAccess(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 8})

	// Create access token with repo-specific access, owned by `user`
	accessToken := &auth_model.AccessToken{
		UID:              user.ID,
		Name:             util.CryptoRandomString(10),
		Scope:            auth_model.AccessTokenScopeReadRepository,
		ResourceAllRepos: false,
	}
	require.NoError(t, auth_model.NewAccessToken(t.Context(), accessToken))
	resRepo1 := &auth_model.AccessTokenResourceRepo{
		RepoID: 2,
	}
	require.NoError(t, auth_model.InsertAccessTokenResourceRepos(t.Context(), accessToken.ID, []*auth_model.AccessTokenResourceRepo{resRepo1}))

	// Create authorized integration with repo-specific access, owned by `user`
	ai := &auth_model.AuthorizedIntegration{
		UserID:           user.ID,
		Scope:            auth_model.AccessTokenScopeReadRepository,
		ResourceAllRepos: false,
		Issuer:           "https://example.org/",
		ClaimRules:       &auth_model.ClaimRules{},
	}
	require.NoError(t, auth_model.InsertAuthorizedIntegration(t.Context(), ai))
	aiResRepo1 := &auth_model.AuthorizedIntegResourceRepo{
		RepoID: 2,
	}
	require.NoError(t, auth_model.InsertAuthorizedIntegrationResourceRepos(t.Context(), ai.ID, []*auth_model.AuthorizedIntegResourceRepo{aiResRepo1}))

	// Delete user.  No follow-up assertions are required as foreign key violations will occur if the delete did not
	// cleanup the repo-specific access created above.
	require.NoError(t, DeleteUser(db.DefaultContext, user, false))
}

func TestPurgeUser(t *testing.T) {
	defer unittest.OverrideFixtures("services/user/TestPurgeUser")()
	require.NoError(t, unittest.PrepareTestDatabase())
	defer test.MockVariableValue(&setting.SSH.RootPath, t.TempDir())()
	defer test.MockVariableValue(&setting.SSH.CreateAuthorizedKeysFile, true)()
	defer test.MockVariableValue(&setting.SSH.CreateAuthorizedPrincipalsFile, true)()
	defer test.MockVariableValue(&setting.SSH.StartBuiltinServer, false)()
	require.NoError(t, asymkey_model.RewriteAllPublicKeys(db.DefaultContext))
	require.NoError(t, asymkey_model.RewriteAllPrincipalKeys(db.DefaultContext))

	test := func(userID int64, modifySSHKey bool) {
		require.NoError(t, unittest.PrepareTestDatabase())
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: userID})

		fAuthorizedKeys, err := os.Open(filepath.Join(setting.SSH.RootPath, "authorized_keys"))
		require.NoError(t, err)
		authorizedKeysStatBefore, err := fAuthorizedKeys.Stat()
		require.NoError(t, err)
		fAuthorizedPrincipals, err := os.Open(filepath.Join(setting.SSH.RootPath, "authorized_principals"))
		require.NoError(t, err)
		authorizedPrincipalsBefore, err := fAuthorizedPrincipals.Stat()
		require.NoError(t, err)

		require.NoError(t, DeleteUser(db.DefaultContext, user, true))

		unittest.AssertNotExistsBean(t, &user_model.User{ID: userID})
		unittest.CheckConsistencyFor(t, &user_model.User{}, &repo_model.Repository{})

		fAuthorizedKeys, err = os.Open(filepath.Join(setting.SSH.RootPath, "authorized_keys"))
		require.NoError(t, err)
		fAuthorizedPrincipals, err = os.Open(filepath.Join(setting.SSH.RootPath, "authorized_principals"))
		require.NoError(t, err)

		authorizedKeysStatAfter, err := fAuthorizedKeys.Stat()
		require.NoError(t, err)
		authorizedPrincipalsAfter, err := fAuthorizedPrincipals.Stat()
		require.NoError(t, err)

		if modifySSHKey {
			assert.Greater(t, authorizedKeysStatAfter.ModTime(), authorizedKeysStatBefore.ModTime())
			assert.Greater(t, authorizedPrincipalsAfter.ModTime(), authorizedPrincipalsBefore.ModTime())
		} else {
			assert.Equal(t, authorizedKeysStatAfter.ModTime(), authorizedKeysStatBefore.ModTime())
			assert.Equal(t, authorizedPrincipalsAfter.ModTime(), authorizedPrincipalsBefore.ModTime())
		}
	}
	test(2, true)
	test(4, false)
	test(8, false)
	test(11, false)

	org := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3})
	require.Error(t, DeleteUser(db.DefaultContext, org, false))
}

func TestCreateUser(t *testing.T) {
	user := &user_model.User{
		Name:               "GiteaBot",
		Email:              "GiteaBot@gitea.io",
		Passwd:             ";p['////..-++']",
		IsAdmin:            false,
		Theme:              setting.UI.DefaultTheme,
		MustChangePassword: false,
	}

	require.NoError(t, user_model.CreateUser(db.DefaultContext, user))

	require.NoError(t, DeleteUser(db.DefaultContext, user, false))
}

func TestRenameUser(t *testing.T) {
	defer unittest.OverrideFixtures("models/user/fixtures/")()
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 21})

	t.Run("Same username", func(t *testing.T) {
		require.NoError(t, RenameUser(db.DefaultContext, user, user.Name))
	})

	t.Run("Non usable username", func(t *testing.T) {
		usernames := []string{"--diff", ".well-known", "gitea-actions", "aaa.atom", "aa.png"}
		for _, username := range usernames {
			require.Error(t, user_model.IsUsableUsername(username), "non-usable username: %s", username)
			require.Error(t, RenameUser(db.DefaultContext, user, username), "non-usable username: %s", username)
		}
	})

	t.Run("Only capitalization", func(t *testing.T) {
		caps := strings.ToUpper(user.Name)
		unittest.AssertNotExistsBean(t, &user_model.User{ID: user.ID, Name: caps})
		unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerID: user.ID, OwnerName: user.Name})

		require.NoError(t, RenameUser(db.DefaultContext, user, caps))

		unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: user.ID, Name: caps})
		unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerID: user.ID, OwnerName: caps})
	})

	t.Run("Already exists", func(t *testing.T) {
		existUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})

		require.ErrorIs(t, RenameUser(db.DefaultContext, user, existUser.Name), user_model.ErrUserAlreadyExist{Name: existUser.Name})
		require.ErrorIs(t, RenameUser(db.DefaultContext, user, existUser.LowerName), user_model.ErrUserAlreadyExist{Name: existUser.LowerName})
		newUsername := fmt.Sprintf("uSEr%d", existUser.ID)
		require.ErrorIs(t, RenameUser(db.DefaultContext, user, newUsername), user_model.ErrUserAlreadyExist{Name: newUsername})
	})

	t.Run("Normal", func(t *testing.T) {
		oldUsername := user.Name
		newUsername := "User_Rename"

		require.NoError(t, RenameUser(db.DefaultContext, user, newUsername))
		unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: user.ID, Name: newUsername, LowerName: strings.ToLower(newUsername)})

		redirectUID, err := redirect_service.LookupUserRedirect(db.DefaultContext, user, oldUsername)
		require.NoError(t, err)
		assert.Equal(t, user.ID, redirectUID)

		unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerID: user.ID, OwnerName: user.Name})
	})

	t.Run("Keep N redirects", func(t *testing.T) {
		defer test.MockProtect(&setting.Service.MaxUserRedirects)()
		// Start clean
		unittest.AssertSuccessfulDelete(t, &user_model.Redirect{RedirectUserID: user.ID})

		setting.Service.MaxUserRedirects = 1

		require.NoError(t, RenameUser(db.DefaultContext, user, "redirect-1"))
		unittest.AssertExistsIf(t, true, &user_model.Redirect{LowerName: "user_rename"})

		// The granularity of created_unix is a second.
		test.SleepTillNextSecond()
		require.NoError(t, RenameUser(db.DefaultContext, user, "redirect-2"))
		unittest.AssertExistsIf(t, false, &user_model.Redirect{LowerName: "user_rename"})
		unittest.AssertExistsIf(t, true, &user_model.Redirect{LowerName: "redirect-1"})

		setting.Service.MaxUserRedirects = 2
		test.SleepTillNextSecond()
		require.NoError(t, RenameUser(db.DefaultContext, user, "redirect-3"))
		unittest.AssertExistsIf(t, true, &user_model.Redirect{LowerName: "redirect-1"})
		unittest.AssertExistsIf(t, true, &user_model.Redirect{LowerName: "redirect-2"})
	})

	t.Run("Non-local", func(t *testing.T) {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1041, LoginSource: 1001})
		authSource := unittest.AssertExistsAndLoadBean(t, &auth_model.Source{ID: user.LoginSource})
		assert.False(t, user.IsLocal())
		assert.True(t, user.IsOAuth2())

		t.Run("Allowed", func(t *testing.T) {
			require.NoError(t, RenameUser(t.Context(), user, "I-am-a-local-username"))
		})

		t.Run("Not allowed", func(t *testing.T) {
			authSourceCfg := authSource.Cfg.(*oauth2.Source)
			authSourceCfg.AllowUsernameChange = false
			authSource.Cfg = authSourceCfg
			_, err := db.GetEngine(t.Context()).Cols("cfg").ID(authSource.ID).Update(authSource)
			require.NoError(t, err)

			require.ErrorIs(t, RenameUser(t.Context(), user, "Another-username-change"), user_model.ErrUserIsNotLocal{UID: user.ID, Name: user.Name})
			t.Run("Admin", func(t *testing.T) {
				require.NoError(t, AdminRenameUser(t.Context(), user, "Another-username-change"))
			})
		})
	})
}

func TestCreateUser_Issue5882(t *testing.T) {
	// Init settings
	_ = setting.Admin

	passwd := ".//.;1;;//.,-=_"

	tt := []struct {
		user               *user_model.User
		disableOrgCreation bool
	}{
		{&user_model.User{Name: "GiteaBot", Email: "GiteaBot@gitea.io", Passwd: passwd, MustChangePassword: false}, false},
		{&user_model.User{Name: "GiteaBot2", Email: "GiteaBot2@gitea.io", Passwd: passwd, MustChangePassword: false}, true},
	}

	setting.Service.DefaultAllowCreateOrganization = true

	for _, v := range tt {
		setting.Admin.DisableRegularOrgCreation = v.disableOrgCreation

		require.NoError(t, user_model.CreateUser(db.DefaultContext, v.user))

		u, err := user_model.GetUserByEmail(db.DefaultContext, v.user.Email)
		require.NoError(t, err)

		assert.Equal(t, !u.AllowCreateOrganization, v.disableOrgCreation)

		require.NoError(t, DeleteUser(db.DefaultContext, v.user, false))
	}
}

func TestDeleteInactiveUsers(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	// Add an inactive user older than a minute, with an associated email_address record.
	oldUser := &user_model.User{Name: "OldInactive", LowerName: "oldinactive", Email: "old@example.com", CreatedUnix: timeutil.TimeStampNow().Add(-120)}
	_, err := db.GetEngine(db.DefaultContext).NoAutoTime().Insert(oldUser)
	require.NoError(t, err)
	oldEmail := &user_model.EmailAddress{UID: oldUser.ID, IsPrimary: true, Email: "old@example.com", LowerEmail: "old@example.com"}
	err = db.Insert(db.DefaultContext, oldEmail)
	require.NoError(t, err)

	// Add an inactive user that's not older than a minute, with an associated email_address record.
	newUser := &user_model.User{Name: "NewInactive", LowerName: "newinactive", Email: "new@example.com"}
	err = db.Insert(db.DefaultContext, newUser)
	require.NoError(t, err)
	newEmail := &user_model.EmailAddress{UID: newUser.ID, IsPrimary: true, Email: "new@example.com", LowerEmail: "new@example.com"}
	err = db.Insert(db.DefaultContext, newEmail)
	require.NoError(t, err)

	err = DeleteInactiveUsers(db.DefaultContext, time.Minute)
	require.NoError(t, err)

	// User older than a minute should be deleted along with their email address.
	unittest.AssertExistsIf(t, false, oldUser)
	unittest.AssertExistsIf(t, false, oldEmail)

	// User not older than a minute shouldn't be deleted and their email address should still exist.
	unittest.AssertExistsIf(t, true, newUser)
	unittest.AssertExistsIf(t, true, newEmail)
}

func TestCreateShadowCopyOnUserUpdate(t *testing.T) {
	defer unittest.OverrideFixtures("models/fixtures/ModerationFeatures")()
	require.NoError(t, unittest.PrepareTestDatabase())

	userAlexSmithID := int64(1002)
	abuseReportID := int64(2)     // submitted for @alexsmith
	newDummyValue := "[REDACTED]" // used for updating profile text fields

	// Retrieve the abusive user (@alexsmith) and the abuse report already created for this user.
	abuser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: userAlexSmithID})
	report := unittest.AssertExistsAndLoadBean(t, &moderation.AbuseReport{
		ID:          abuseReportID,
		ContentType: moderation.ReportedContentTypeUser,
		ContentID:   abuser.ID,
	})
	// The report should not already have a shadow copy linked.
	assert.False(t, report.ShadowCopyID.Valid)

	// Keep a copy of old field values before updating them.
	oldUserData := user_model.UserData{
		FullName:    abuser.FullName,
		Location:    abuser.Location,
		Website:     abuser.Website,
		Pronouns:    abuser.Pronouns,
		Description: abuser.Description,
	}

	// The abusive user is updating their profile.
	opts := &UpdateOptions{
		FullName:    optional.Some(newDummyValue),
		Location:    optional.Some(newDummyValue),
		Website:     optional.Some(newDummyValue),
		Pronouns:    optional.Some(newDummyValue),
		Description: optional.Some(newDummyValue),
	}
	require.NoError(t, UpdateUser(t.Context(), abuser, opts))

	// Reload the report.
	report = unittest.AssertExistsAndLoadBean(t, &moderation.AbuseReport{ID: report.ID})
	// A shadow copy should have been created and linked to our report.
	assert.True(t, report.ShadowCopyID.Valid)
	// Retrieve the newly created shadow copy and unmarshal the stored JSON so that we can check the values.
	shadowCopy := unittest.AssertExistsAndLoadBean(t, &moderation.AbuseReportShadowCopy{ID: report.ShadowCopyID.Int64})
	shadowCopyUserData := new(user_model.UserData)
	require.NoError(t, json.Unmarshal([]byte(shadowCopy.RawValue), &shadowCopyUserData))
	// Check to see if the initial field values of the user were stored within the shadow copy.
	assert.Equal(t, oldUserData.FullName, shadowCopyUserData.FullName)
	assert.Equal(t, oldUserData.Location, shadowCopyUserData.Location)
	assert.Equal(t, oldUserData.Website, shadowCopyUserData.Website)
	assert.Equal(t, oldUserData.Pronouns, shadowCopyUserData.Pronouns)
	assert.Equal(t, oldUserData.Description, shadowCopyUserData.Description)
}

func TestDeleteUserCleansUpMembershipProvenance(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	user28 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 28})
	team1 := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})

	_, err := models.InsertTeamMember(db.DefaultContext, team1, user28.ID, organization.MembershipReasonAddedByUser, optional.Some(user2.ID), optional.None[int64]())
	require.NoError(t, err)

	require.NoError(t, DeleteUser(db.DefaultContext, user2, true))

	unittest.AssertExistsAndLoadBean(t, &organization.TeamUser{UID: 28, TeamID: 1, Reason: organization.MembershipReasonAddedByUser, CreatedByUserID: optional.None[int64]()})
}
