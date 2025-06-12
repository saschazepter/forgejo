// Copyright 2017 The Gitea Authors. All rights reserved.
// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package user_test

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"forgejo.org/models/auth"
	"forgejo.org/models/db"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/auth/password/hash"
	"forgejo.org/modules/container"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"
	"forgejo.org/modules/validation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOAuth2Application_LoadUser(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	app := unittest.AssertExistsAndLoadBean(t, &auth.OAuth2Application{ID: 1})
	user, err := user_model.GetUserByID(db.DefaultContext, app.UID)
	require.NoError(t, err)
	assert.NotNil(t, user)
}

func TestIsValidUserID(t *testing.T) {
	assert.False(t, user_model.IsValidUserID(-30))
	assert.False(t, user_model.IsValidUserID(0))
	assert.True(t, user_model.IsValidUserID(user_model.GhostUserID))
	assert.True(t, user_model.IsValidUserID(user_model.ActionsUserID))
	assert.True(t, user_model.IsValidUserID(200))
}

func TestGetUserFromMap(t *testing.T) {
	id := int64(200)
	idMap := map[int64]*user_model.User{
		id: {ID: id},
	}

	ghostID := int64(user_model.GhostUserID)
	actionsID := int64(user_model.ActionsUserID)
	actualID, actualUser := user_model.GetUserFromMap(-20, idMap)
	assert.Equal(t, ghostID, actualID)
	assert.Equal(t, ghostID, actualUser.ID)

	actualID, actualUser = user_model.GetUserFromMap(0, idMap)
	assert.Equal(t, ghostID, actualID)
	assert.Equal(t, ghostID, actualUser.ID)

	actualID, actualUser = user_model.GetUserFromMap(ghostID, idMap)
	assert.Equal(t, ghostID, actualID)
	assert.Equal(t, ghostID, actualUser.ID)

	actualID, actualUser = user_model.GetUserFromMap(actionsID, idMap)
	assert.Equal(t, actionsID, actualID)
	assert.Equal(t, actionsID, actualUser.ID)
}

func TestGetUserByName(t *testing.T) {
	defer unittest.OverrideFixtures("models/user/fixtures")()
	require.NoError(t, unittest.PrepareTestDatabase())

	{
		_, err := user_model.GetUserByName(db.DefaultContext, "")
		assert.True(t, user_model.IsErrUserNotExist(err), err)
	}
	{
		_, err := user_model.GetUserByName(db.DefaultContext, "UNKNOWN")
		assert.True(t, user_model.IsErrUserNotExist(err), err)
	}
	{
		user, err := user_model.GetUserByName(db.DefaultContext, "USER2")
		require.NoError(t, err)
		assert.Equal(t, "user2", user.Name)
	}
	{
		user, err := user_model.GetUserByName(db.DefaultContext, "org3")
		require.NoError(t, err)
		assert.Equal(t, "org3", user.Name)
	}
	{
		user, err := user_model.GetUserByName(db.DefaultContext, "remote01")
		require.NoError(t, err)
		assert.Equal(t, "remote01", user.Name)
	}
}

func TestCanCreateOrganization(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	admin := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	assert.True(t, admin.CanCreateOrganization())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	assert.True(t, user.CanCreateOrganization())
	// Disable user create organization permission.
	user.AllowCreateOrganization = false
	assert.False(t, user.CanCreateOrganization())

	setting.Admin.DisableRegularOrgCreation = true
	user.AllowCreateOrganization = true
	assert.True(t, admin.CanCreateOrganization())
	assert.False(t, user.CanCreateOrganization())
}

func TestGetAllUsers(t *testing.T) {
	defer unittest.OverrideFixtures("models/user/fixtures")()
	require.NoError(t, unittest.PrepareTestDatabase())

	users, err := user_model.GetAllUsers(db.DefaultContext)
	require.NoError(t, err)

	found := make(map[user_model.UserType]bool, 0)
	for _, user := range users {
		found[user.Type] = true
	}
	assert.True(t, found[user_model.UserTypeIndividual], users)
	assert.True(t, found[user_model.UserTypeRemoteUser], users)
	assert.False(t, found[user_model.UserTypeOrganization], users)
}

func TestAPActorID(t *testing.T) {
	user := user_model.User{ID: 1}
	url := user.APActorID()
	expected := "https://try.gitea.io/api/v1/activitypub/user-id/1"
	assert.Equal(t, expected, url)
}

func TestAPActorID_APActorID(t *testing.T) {
	user := user_model.User{ID: user_model.APServerActorUserID}
	url := user.APActorID()
	expected := "https://try.gitea.io/api/v1/activitypub/actor"
	assert.Equal(t, expected, url)
}

func TestAPActorKeyID(t *testing.T) {
	user := user_model.User{ID: 1}
	url := user.APActorKeyID()
	expected := "https://try.gitea.io/api/v1/activitypub/user-id/1#main-key"
	assert.Equal(t, expected, url)
}

func TestSearchUsers(t *testing.T) {
	defer unittest.OverrideFixtures("models/user/fixtures")()
	require.NoError(t, unittest.PrepareTestDatabase())
	testSuccess := func(opts *user_model.SearchUserOptions, expectedUserOrOrgIDs []int64) {
		users, _, err := user_model.SearchUsers(db.DefaultContext, opts)
		require.NoError(t, err)
		cassText := fmt.Sprintf("ids: %v, opts: %v", expectedUserOrOrgIDs, opts)
		if assert.Len(t, users, len(expectedUserOrOrgIDs), "case: %s", cassText) {
			for i, expectedID := range expectedUserOrOrgIDs {
				assert.Equal(t, expectedID, users[i].ID, "case: %s", cassText)
			}
		}
	}

	// test orgs
	testOrgSuccess := func(opts *user_model.SearchUserOptions, expectedOrgIDs []int64) {
		opts.Type = user_model.UserTypeOrganization
		testSuccess(opts, expectedOrgIDs)
	}

	testOrgSuccess(&user_model.SearchUserOptions{OrderBy: "id ASC", ListOptions: db.ListOptions{Page: 1, PageSize: 2}},
		[]int64{3, 6})

	testOrgSuccess(&user_model.SearchUserOptions{OrderBy: "id ASC", ListOptions: db.ListOptions{Page: 2, PageSize: 2}},
		[]int64{7, 17})

	testOrgSuccess(&user_model.SearchUserOptions{OrderBy: "id ASC", ListOptions: db.ListOptions{Page: 3, PageSize: 2}},
		[]int64{19, 25})

	testOrgSuccess(&user_model.SearchUserOptions{OrderBy: "id ASC", ListOptions: db.ListOptions{Page: 4, PageSize: 2}},
		[]int64{26, 41})

	testOrgSuccess(&user_model.SearchUserOptions{ListOptions: db.ListOptions{Page: 5, PageSize: 2}},
		[]int64{})

	// test users
	testUserSuccess := func(opts *user_model.SearchUserOptions, expectedUserIDs []int64) {
		opts.Type = user_model.UserTypeIndividual
		testSuccess(opts, expectedUserIDs)
	}

	testUserSuccess(&user_model.SearchUserOptions{OrderBy: "id ASC", ListOptions: db.ListOptions{Page: 1}},
		[]int64{1, 2, 4, 5, 8, 9, 10, 11, 12, 13, 14, 15, 16, 18, 20, 21, 24, 27, 28, 29, 30, 32, 34, 37, 38, 39, 40, 1041})

	testUserSuccess(&user_model.SearchUserOptions{ListOptions: db.ListOptions{Page: 1}, IsActive: optional.Some(false)},
		[]int64{9})

	testUserSuccess(&user_model.SearchUserOptions{OrderBy: "id ASC", ListOptions: db.ListOptions{Page: 1}, IsActive: optional.Some(true)},
		[]int64{1, 2, 4, 5, 8, 10, 11, 12, 13, 14, 15, 16, 18, 20, 21, 24, 27, 28, 29, 30, 32, 34, 37, 38, 39, 40, 1041})

	testUserSuccess(&user_model.SearchUserOptions{Keyword: "user1", OrderBy: "id ASC", ListOptions: db.ListOptions{Page: 1}, IsActive: optional.Some(true)},
		[]int64{1, 10, 11, 12, 13, 14, 15, 16, 18})

	// order by name asc default
	testUserSuccess(&user_model.SearchUserOptions{Keyword: "user1", ListOptions: db.ListOptions{Page: 1}, IsActive: optional.Some(true)},
		[]int64{1, 10, 11, 12, 13, 14, 15, 16, 18})

	testUserSuccess(&user_model.SearchUserOptions{ListOptions: db.ListOptions{Page: 1}, IsAdmin: optional.Some(true)},
		[]int64{1})

	testUserSuccess(&user_model.SearchUserOptions{ListOptions: db.ListOptions{Page: 1}, IsRestricted: optional.Some(true)},
		[]int64{29})

	testUserSuccess(&user_model.SearchUserOptions{ListOptions: db.ListOptions{Page: 1}, IsProhibitLogin: optional.Some(true)},
		[]int64{1041, 37})

	testUserSuccess(&user_model.SearchUserOptions{ListOptions: db.ListOptions{Page: 1}, IsTwoFactorEnabled: optional.Some(true)},
		[]int64{24, 32})
}

func TestEmailNotificationPreferences(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	for _, test := range []struct {
		expected string
		userID   int64
	}{
		{user_model.EmailNotificationsEnabled, 1},
		{user_model.EmailNotificationsEnabled, 2},
		{user_model.EmailNotificationsOnMention, 3},
		{user_model.EmailNotificationsOnMention, 4},
		{user_model.EmailNotificationsEnabled, 5},
		{user_model.EmailNotificationsEnabled, 6},
		{user_model.EmailNotificationsDisabled, 7},
		{user_model.EmailNotificationsEnabled, 8},
		{user_model.EmailNotificationsOnMention, 9},
	} {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: test.userID})
		assert.Equal(t, test.expected, user.EmailNotificationsPreference)
	}
}

func TestHashPasswordDeterministic(t *testing.T) {
	b := make([]byte, 16)
	u := &user_model.User{}
	algos := hash.RecommendedHashAlgorithms
	for j := 0; j < len(algos); j++ {
		u.PasswdHashAlgo = algos[j]
		for i := 0; i < 50; i++ {
			// generate a random password
			rand.Read(b)
			pass := string(b)

			// save the current password in the user - hash it and store the result
			u.SetPassword(pass)
			r1 := u.Passwd

			// run again
			u.SetPassword(pass)
			r2 := u.Passwd

			assert.NotEqual(t, r1, r2)
			assert.True(t, u.ValidatePassword(t.Context(), pass))
		}
	}
}

func BenchmarkHashPassword(b *testing.B) {
	// BenchmarkHashPassword ensures that it takes a reasonable amount of time
	// to hash a password - in order to protect from brute-force attacks.
	pass := "password1337"
	u := &user_model.User{Passwd: pass}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		u.SetPassword(pass)
	}
}

func TestNewGitSig(t *testing.T) {
	users := make([]*user_model.User, 0, 20)
	err := db.GetEngine(db.DefaultContext).Find(&users)
	require.NoError(t, err)

	for _, user := range users {
		sig := user.NewGitSig()
		assert.NotContains(t, sig.Name, "<")
		assert.NotContains(t, sig.Name, ">")
		assert.NotContains(t, sig.Name, "\n")
		assert.NotEmpty(t, strings.TrimSpace(sig.Name))
	}
}

func TestDisplayName(t *testing.T) {
	users := make([]*user_model.User, 0, 20)
	err := db.GetEngine(db.DefaultContext).Find(&users)
	require.NoError(t, err)

	for _, user := range users {
		displayName := user.DisplayName()
		assert.Equal(t, strings.TrimSpace(displayName), displayName)
		if len(strings.TrimSpace(user.FullName)) == 0 {
			assert.Equal(t, user.Name, displayName)
		}
		assert.NotEmpty(t, strings.TrimSpace(displayName))
	}
}

func TestCreateUserInvalidEmail(t *testing.T) {
	user := &user_model.User{
		Name:               "GiteaBot",
		Email:              "GiteaBot@gitea.io\r\n",
		Passwd:             ";p['////..-++']",
		IsAdmin:            false,
		Theme:              setting.UI.DefaultTheme,
		MustChangePassword: false,
	}

	err := user_model.CreateUser(db.DefaultContext, user)
	require.Error(t, err)
	assert.True(t, validation.IsErrEmailInvalid(err))
}

func TestCreateUserEmailAlreadyUsed(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	// add new user with user2's email
	user.Name = "testuser"
	user.LowerName = strings.ToLower(user.Name)
	user.ID = 0
	err := user_model.CreateUser(db.DefaultContext, user)
	require.Error(t, err)
	assert.True(t, user_model.IsErrEmailAlreadyUsed(err))
}

func TestCreateUserCustomTimestamps(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	// Add new user with a custom creation timestamp.
	var creationTimestamp timeutil.TimeStamp = 12345
	user.Name = "testuser"
	user.LowerName = strings.ToLower(user.Name)
	user.ID = 0
	user.Email = "unique@example.com"
	user.CreatedUnix = creationTimestamp
	err := user_model.CreateUser(db.DefaultContext, user)
	require.NoError(t, err)

	fetched, err := user_model.GetUserByID(t.Context(), user.ID)
	require.NoError(t, err)
	assert.Equal(t, creationTimestamp, fetched.CreatedUnix)
	assert.Equal(t, creationTimestamp, fetched.UpdatedUnix)
}

func TestCreateUserWithoutCustomTimestamps(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	// There is no way to use a mocked time for the XORM auto-time functionality,
	// so use the real clock to approximate the expected timestamp.
	timestampStart := time.Now().Unix()

	// Add new user without a custom creation timestamp.
	user.Name = "Testuser"
	user.LowerName = strings.ToLower(user.Name)
	user.ID = 0
	user.Email = "unique@example.com"
	user.CreatedUnix = 0
	user.UpdatedUnix = 0
	err := user_model.CreateUser(db.DefaultContext, user)
	require.NoError(t, err)

	timestampEnd := time.Now().Unix()

	fetched, err := user_model.GetUserByID(t.Context(), user.ID)
	require.NoError(t, err)

	assert.LessOrEqual(t, timestampStart, fetched.CreatedUnix)
	assert.LessOrEqual(t, fetched.CreatedUnix, timestampEnd)

	assert.LessOrEqual(t, timestampStart, fetched.UpdatedUnix)
	assert.LessOrEqual(t, fetched.UpdatedUnix, timestampEnd)
}

func TestCreateUserClaimingUsername(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	defer test.MockVariableValue(&setting.Service.UsernameCooldownPeriod, 1)()

	_, err := db.GetEngine(db.DefaultContext).NoAutoTime().Insert(&user_model.Redirect{RedirectUserID: 1, LowerName: "redirecting", CreatedUnix: timeutil.TimeStampNow()})
	require.NoError(t, err)

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	user.Name = "redirecting"
	user.LowerName = strings.ToLower(user.Name)
	user.ID = 0
	user.Email = "unique@example.com"

	t.Run("Normal creation", func(t *testing.T) {
		err = user_model.CreateUser(db.DefaultContext, user)
		assert.True(t, user_model.IsErrCooldownPeriod(err))
	})

	t.Run("Creation as admin", func(t *testing.T) {
		err = user_model.AdminCreateUser(db.DefaultContext, user)
		require.NoError(t, err)
	})
}

func TestGetUserIDsByNames(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	// ignore non existing
	IDs, err := user_model.GetUserIDsByNames(db.DefaultContext, []string{"user1", "user2", "none_existing_user"}, true)
	require.NoError(t, err)
	assert.Equal(t, []int64{1, 2}, IDs)

	// ignore non existing
	IDs, err = user_model.GetUserIDsByNames(db.DefaultContext, []string{"user1", "do_not_exist"}, false)
	require.Error(t, err)
	assert.Equal(t, []int64(nil), IDs)
}

func TestGetMaileableUsersByIDs(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	results, err := user_model.GetMaileableUsersByIDs(db.DefaultContext, []int64{1, 4}, false)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	if len(results) > 1 {
		assert.Equal(t, 1, results[0].ID)
	}

	results, err = user_model.GetMaileableUsersByIDs(db.DefaultContext, []int64{1, 4}, true)
	require.NoError(t, err)
	assert.Len(t, results, 2)
	if len(results) > 2 {
		assert.Equal(t, 1, results[0].ID)
		assert.Equal(t, 4, results[1].ID)
	}
}

func TestNewUserRedirect(t *testing.T) {
	// redirect to a completely new name
	require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	require.NoError(t, user_model.NewUserRedirect(db.DefaultContext, user.ID, user.Name, "newusername"))

	unittest.AssertExistsAndLoadBean(t, &user_model.Redirect{
		LowerName:      user.LowerName,
		RedirectUserID: user.ID,
	})
	unittest.AssertExistsAndLoadBean(t, &user_model.Redirect{
		LowerName:      "olduser1",
		RedirectUserID: user.ID,
	})
}

func TestNewUserRedirect2(t *testing.T) {
	// redirect to previously used name
	require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	require.NoError(t, user_model.NewUserRedirect(db.DefaultContext, user.ID, user.Name, "olduser1"))

	unittest.AssertExistsAndLoadBean(t, &user_model.Redirect{
		LowerName:      user.LowerName,
		RedirectUserID: user.ID,
	})
	unittest.AssertNotExistsBean(t, &user_model.Redirect{
		LowerName:      "olduser1",
		RedirectUserID: user.ID,
	})
}

func TestNewUserRedirect3(t *testing.T) {
	// redirect for a previously-unredirected user
	require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	require.NoError(t, user_model.NewUserRedirect(db.DefaultContext, user.ID, user.Name, "newusername"))

	unittest.AssertExistsAndLoadBean(t, &user_model.Redirect{
		LowerName:      user.LowerName,
		RedirectUserID: user.ID,
	})
}

func TestGetUserByOpenID(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	_, err := user_model.GetUserByOpenID(db.DefaultContext, "https://unknown")
	if assert.Error(t, err) {
		assert.True(t, user_model.IsErrUserNotExist(err))
	}

	user, err := user_model.GetUserByOpenID(db.DefaultContext, "https://user1.domain1.tld")
	require.NoError(t, err)

	assert.Equal(t, int64(1), user.ID)

	user, err = user_model.GetUserByOpenID(db.DefaultContext, "https://domain1.tld/user2/")
	require.NoError(t, err)

	assert.Equal(t, int64(2), user.ID)
}

func TestFollowUser(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	testSuccess := func(followerID, followedID int64) {
		require.NoError(t, user_model.FollowUser(db.DefaultContext, followerID, followedID))
		unittest.AssertExistsAndLoadBean(t, &user_model.Follow{UserID: followerID, FollowID: followedID})
	}
	testSuccess(4, 2)
	testSuccess(5, 2)

	require.NoError(t, user_model.FollowUser(db.DefaultContext, 2, 2))

	// Blocked user.
	require.ErrorIs(t, user_model.ErrBlockedByUser, user_model.FollowUser(db.DefaultContext, 1, 4))
	require.ErrorIs(t, user_model.ErrBlockedByUser, user_model.FollowUser(db.DefaultContext, 4, 1))
	unittest.AssertNotExistsBean(t, &user_model.Follow{UserID: 1, FollowID: 4})
	unittest.AssertNotExistsBean(t, &user_model.Follow{UserID: 4, FollowID: 1})

	unittest.CheckConsistencyFor(t, &user_model.User{})
}

func TestUnfollowUser(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	testSuccess := func(followerID, followedID int64) {
		require.NoError(t, user_model.UnfollowUser(db.DefaultContext, followerID, followedID))
		unittest.AssertNotExistsBean(t, &user_model.Follow{UserID: followerID, FollowID: followedID})
	}
	testSuccess(4, 2)
	testSuccess(5, 2)
	testSuccess(2, 2)

	unittest.CheckConsistencyFor(t, &user_model.User{})
}

func TestIsUserVisibleToViewer(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})   // admin, public
	user4 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})   // normal, public
	user20 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 20}) // public, same team as user31
	user29 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 29}) // public, is restricted
	user31 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 31}) // private, same team as user20
	user33 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 33}) // limited, follows 31

	test := func(u, viewer *user_model.User, expected bool) {
		name := func(u *user_model.User) string {
			if u == nil {
				return "<nil>"
			}
			return u.Name
		}
		assert.Equal(t, expected, user_model.IsUserVisibleToViewer(db.DefaultContext, u, viewer), "user %v should be visible to viewer %v: %v", name(u), name(viewer), expected)
	}

	// admin viewer
	test(user1, user1, true)
	test(user20, user1, true)
	test(user31, user1, true)
	test(user33, user1, true)

	// non admin viewer
	test(user4, user4, true)
	test(user20, user4, true)
	test(user31, user4, false)
	test(user33, user4, true)
	test(user4, nil, true)

	// public user
	test(user4, user20, true)
	test(user4, user31, true)
	test(user4, user33, true)

	// limited user
	test(user33, user33, true)
	test(user33, user4, true)
	test(user33, user29, false)
	test(user33, nil, false)

	// private user
	test(user31, user31, true)
	test(user31, user4, false)
	test(user31, user20, true)
	test(user31, user29, false)
	test(user31, user33, true)
	test(user31, nil, false)
}

func TestGetAllAdmins(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	admins, err := user_model.GetAllAdmins(db.DefaultContext)
	require.NoError(t, err)

	assert.Len(t, admins, 1)
	assert.Equal(t, int64(1), admins[0].ID)
}

func Test_ValidateUser(t *testing.T) {
	defer test.MockVariableValue(&setting.Service.AllowedUserVisibilityModesSlice, []bool{true, false, true})()

	kases := map[*user_model.User]bool{
		{ID: 1, Visibility: structs.VisibleTypePublic}:  true,
		{ID: 2, Visibility: structs.VisibleTypeLimited}: false,
		{ID: 2, Visibility: structs.VisibleTypePrivate}: true,
	}
	for kase, expected := range kases {
		assert.Equal(t, expected, nil == user_model.ValidateUser(kase))
	}
}

func Test_NormalizeUserFromEmail(t *testing.T) {
	defer test.MockVariableValue(&setting.Service.AllowDotsInUsernames, true)()

	testCases := []struct {
		Input             string
		Expected          string
		IsNormalizedValid bool
	}{
		{"test", "test", true},
		{"Sinéad.O'Connor", "Sinead.OConnor", true},
		{"Æsir", "AEsir", true},
		{"Flußpferd", "Flusspferd", true},
		// \u00e9\u0065\u0301
		{"éé", "ee", true},
		{"Awareness Hub", "Awareness-Hub", true},
		{"double__underscore", "double__underscore", false}, // We should consider squashing double non-alpha characters
		{".bad.", ".bad.", false},
		{"new😀user", "new😀user", false}, // No plans to support
	}
	for _, testCase := range testCases {
		normalizedName, err := user_model.NormalizeUserName(testCase.Input)
		require.NoError(t, err)
		assert.Equal(t, testCase.Expected, normalizedName)
		if testCase.IsNormalizedValid {
			require.NoError(t, user_model.IsUsableUsername(normalizedName))
		} else {
			require.Error(t, user_model.IsUsableUsername(normalizedName))
		}
	}
}

func TestEmailTo(t *testing.T) {
	testCases := []struct {
		fullName string
		mail     string
		result   string
	}{
		{"Awareness Hub", "awareness@hub.net", `"Awareness Hub" <awareness@hub.net>`},
		{"name@example.com", "name@example.com", "name@example.com"},
		{"Hi Its <Mee>", "ee@mail.box", `"Hi Its Mee" <ee@mail.box>`},
		{"Sinéad.O'Connor", "sinead.oconnor@gmail.com", "=?utf-8?b?U2luw6lhZC5PJ0Nvbm5vcg==?= <sinead.oconnor@gmail.com>"},
		{"Æsir", "aesir@gmx.de", "=?utf-8?q?=C3=86sir?= <aesir@gmx.de>"},
		{"new😀user", "new.user@alo.com", "=?utf-8?q?new=F0=9F=98=80user?= <new.user@alo.com>"}, // codespell:ignore
		{`"quoted"`, "quoted@test.com", `"quoted" <quoted@test.com>`},
		{`gusted`, "gusted@test.com", `"gusted" <gusted@test.com>`},
		{`Joe Q. Public`, "john.q.public@example.com", `"Joe Q. Public" <john.q.public@example.com>`},
		{`Who?`, "one@y.test", `"Who?" <one@y.test>`},
	}

	for _, testCase := range testCases {
		t.Run(testCase.result, func(t *testing.T) {
			testUser := &user_model.User{FullName: testCase.fullName, Email: testCase.mail}
			assert.Equal(t, testCase.result, testUser.EmailTo())
		})
	}

	t.Run("Override user's email", func(t *testing.T) {
		testUser := &user_model.User{FullName: "Christine Jorgensen", Email: "christine@test.com"}
		assert.Equal(t, `"Christine Jorgensen" <christine@example.org>`, testUser.EmailTo("christine@example.org"))
	})
}

func TestDisabledUserFeatures(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	testValues := container.SetOf(setting.UserFeatureDeletion,
		setting.UserFeatureManageSSHKeys,
		setting.UserFeatureManageGPGKeys)
	defer test.MockVariableValue(&setting.Admin.ExternalUserDisableFeatures, testValues)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})

	assert.Empty(t, setting.Admin.UserDisabledFeatures.Values())

	// no features should be disabled with a plain login type
	assert.LessOrEqual(t, user.LoginType, auth.Plain)
	assert.Empty(t, user_model.DisabledFeaturesWithLoginType(user).Values())
	for f := range testValues.Seq() {
		assert.False(t, user_model.IsFeatureDisabledWithLoginType(user, f))
	}

	// check disabled features with external login type
	user.LoginType = auth.OAuth2

	// all features should be disabled
	assert.NotEmpty(t, user_model.DisabledFeaturesWithLoginType(user).Values())
	for f := range testValues.Seq() {
		assert.True(t, user_model.IsFeatureDisabledWithLoginType(user, f))
	}
}

func TestGenerateEmailAuthorizationCode(t *testing.T) {
	defer test.MockVariableValue(&setting.Service.ActiveCodeLives, 2)()
	require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	code, err := user.GenerateEmailAuthorizationCode(db.DefaultContext, auth.UserActivation)
	require.NoError(t, err)

	lookupKey, validator, ok := strings.Cut(code, ":")
	assert.True(t, ok)

	rawValidator, err := hex.DecodeString(validator)
	require.NoError(t, err)

	authToken, err := auth.FindAuthToken(db.DefaultContext, lookupKey, auth.UserActivation)
	require.NoError(t, err)
	assert.False(t, authToken.IsExpired())
	assert.Equal(t, authToken.HashedValidator, auth.HashValidator(rawValidator))

	authToken.Expiry = authToken.Expiry.Add(-int64(setting.Service.ActiveCodeLives) * 60)
	assert.True(t, authToken.IsExpired())
}

func TestVerifyUserAuthorizationToken(t *testing.T) {
	defer test.MockVariableValue(&setting.Service.ActiveCodeLives, 2)()
	require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	code, err := user.GenerateEmailAuthorizationCode(db.DefaultContext, auth.UserActivation)
	require.NoError(t, err)

	lookupKey, _, ok := strings.Cut(code, ":")
	assert.True(t, ok)

	t.Run("Wrong purpose", func(t *testing.T) {
		u, _, err := user_model.VerifyUserAuthorizationToken(db.DefaultContext, code, auth.PasswordReset)
		require.NoError(t, err)
		assert.Nil(t, u)
	})

	t.Run("No delete", func(t *testing.T) {
		u, _, err := user_model.VerifyUserAuthorizationToken(db.DefaultContext, code, auth.UserActivation)
		require.NoError(t, err)
		assert.Equal(t, user.ID, u.ID)

		authToken, err := auth.FindAuthToken(db.DefaultContext, lookupKey, auth.UserActivation)
		require.NoError(t, err)
		assert.NotNil(t, authToken)
	})

	t.Run("Delete", func(t *testing.T) {
		u, deleteToken, err := user_model.VerifyUserAuthorizationToken(db.DefaultContext, code, auth.UserActivation)
		require.NoError(t, err)
		assert.Equal(t, user.ID, u.ID)
		require.NoError(t, deleteToken())

		authToken, err := auth.FindAuthToken(db.DefaultContext, lookupKey, auth.UserActivation)
		require.ErrorIs(t, err, util.ErrNotExist)
		assert.Nil(t, authToken)
	})
}

func TestGetInactiveUsers(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	// all inactive users
	// user1's createdunix is 1672578000
	users, err := user_model.GetInactiveUsers(db.DefaultContext, 0)
	require.NoError(t, err)
	assert.Len(t, users, 1)
	interval := time.Now().Unix() - 1672578000 + 3600*24
	users, err = user_model.GetInactiveUsers(db.DefaultContext, time.Duration(interval*int64(time.Second)))
	require.NoError(t, err)
	require.Empty(t, users)
}

func TestPronounsPrivacy(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	t.Run("EmptyPronounsIfNoneSet", func(t *testing.T) {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
		user.Pronouns = ""
		user.KeepPronounsPrivate = false

		assert.Empty(t, user.GetPronouns(false))
	})
	t.Run("EmptyPronounsIfSetButPrivateAndNotLoggedIn", func(t *testing.T) {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
		user.Pronouns = "any"
		user.KeepPronounsPrivate = true

		assert.Empty(t, user.GetPronouns(false))
	})
	t.Run("ReturnPronounsIfSetAndNotPrivateAndNotLoggedIn", func(t *testing.T) {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
		user.Pronouns = "any"
		user.KeepPronounsPrivate = false

		assert.Equal(t, "any", user.GetPronouns(false))
	})
	t.Run("ReturnPronounsIfSetAndPrivateAndLoggedIn", func(t *testing.T) {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
		user.Pronouns = "any"
		user.KeepPronounsPrivate = false

		assert.Equal(t, "any", user.GetPronouns(true))
	})
	t.Run("ReturnPronounsIfSetAndNotPrivateAndLoggedIn", func(t *testing.T) {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
		user.Pronouns = "any"
		user.KeepPronounsPrivate = true

		assert.Equal(t, "any", user.GetPronouns(true))
	})
}
