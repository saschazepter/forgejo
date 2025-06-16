// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"net/http"
	"testing"
	"time"

	"forgejo.org/models/db"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/modules/timeutil"
	forgejo_context "forgejo.org/services/context"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserRedirect(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&setting.Service.UsernameCooldownPeriod, 1)()

	session := loginUser(t, "user2")

	t.Run("Rename user normally", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequestWithValues(t, "POST", "/user/settings", map[string]string{
			"_csrf": GetCSRF(t, session, "/user/settings"),
			"name":  "user2-new",
		})
		session.MakeRequest(t, req, http.StatusSeeOther)

		flashCookie := session.GetCookie(forgejo_context.CookieNameFlash)
		assert.NotNil(t, flashCookie)
		assert.Equal(t, "success%3DYour%2Bprofile%2Bhas%2Bbeen%2Bupdated.", flashCookie.Value)

		unittest.AssertExistsIf(t, true, &user_model.Redirect{LowerName: "user2", RedirectUserID: 2})
	})

	t.Run("Create new user", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequestWithValues(t, "POST", "/user/sign_up", map[string]string{
			"_csrf":     GetCSRF(t, emptyTestSession(t), "/user/sign_up"),
			"user_name": "user2",
			"email":     "doesnotexist@example.com",
			"password":  "examplePassword!1",
			"retype":    "examplePassword!1",
		})
		resp := MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)
		flashMessage := htmlDoc.Find(`.flash-message`).Text()
		assert.Contains(t, flashMessage, "The username cannot be claimed, because its cooldown period is not yet over. It can be claimed on")
	})

	t.Run("Rename another user", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		session := loginUser(t, "user4")
		req := NewRequestWithValues(t, "POST", "/user/settings", map[string]string{
			"_csrf": GetCSRF(t, session, "/user/settings"),
			"name":  "user2",
		})
		session.MakeRequest(t, req, http.StatusSeeOther)

		flashCookie := session.GetCookie(forgejo_context.CookieNameFlash)
		assert.NotNil(t, flashCookie)
		assert.Contains(t, flashCookie.Value, "error%3DThe%2Busername%2Bcannot%2Bbe%2Bclaimed%252C%2Bbecause%2Bits%2Bcooldown%2Bperiod%2Bis%2Bnot%2Byet%2Bover.%2BIt%2Bcan%2Bbe%2Bclaimed%2Bon")
	})

	t.Run("Admin rename user", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		session := loginUser(t, "user1")
		req := NewRequestWithValues(t, "POST", "/admin/users/4/edit", map[string]string{
			"_csrf":      GetCSRF(t, session, "/admin/users/4/edit"),
			"user_name":  "user2",
			"email":      "user4@example.com",
			"login_type": "0-0",
		})
		session.MakeRequest(t, req, http.StatusSeeOther)
		flashCookie := session.GetCookie(forgejo_context.CookieNameFlash)
		assert.NotNil(t, flashCookie)
		assert.Equal(t, "success%3DThe%2Buser%2Baccount%2Bhas%2Bbeen%2Bupdated.", flashCookie.Value)

		unittest.AssertExistsIf(t, true, &user_model.User{LowerName: "user2"})
		unittest.AssertExistsIf(t, false, &user_model.Redirect{LowerName: "user2", RedirectUserID: 2})
	})

	t.Run("Reclaim username", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequestWithValues(t, "POST", "/user/settings", map[string]string{
			"_csrf": GetCSRF(t, session, "/user/settings"),
			"name":  "user2-new-2",
		})
		session.MakeRequest(t, req, http.StatusSeeOther)

		flashCookie := session.GetCookie(forgejo_context.CookieNameFlash)
		assert.NotNil(t, flashCookie)
		assert.Equal(t, "success%3DYour%2Bprofile%2Bhas%2Bbeen%2Bupdated.", flashCookie.Value)

		unittest.AssertExistsIf(t, true, &user_model.Redirect{LowerName: "user2-new", RedirectUserID: 2})

		req = NewRequestWithValues(t, "POST", "/user/settings", map[string]string{
			"_csrf": GetCSRF(t, session, "/user/settings"),
			"name":  "user2-new",
		})
		session.MakeRequest(t, req, http.StatusSeeOther)

		flashCookie = session.GetCookie(forgejo_context.CookieNameFlash)
		assert.NotNil(t, flashCookie)
		assert.Equal(t, "success%3DYour%2Bprofile%2Bhas%2Bbeen%2Bupdated.", flashCookie.Value)

		unittest.AssertExistsIf(t, false, &user_model.Redirect{LowerName: "user2-new", RedirectUserID: 2})
		unittest.AssertExistsIf(t, true, &user_model.Redirect{LowerName: "user2-new-2", RedirectUserID: 2})
	})

	t.Run("Profile note", func(t *testing.T) {
		getPrompt := func(t *testing.T) string {
			req := NewRequest(t, "GET", "/user/settings")
			resp := session.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			return htmlDoc.Find("input[name='name'] + .help").Text()
		}

		t.Run("No cooldown", func(t *testing.T) {
			defer test.MockVariableValue(&setting.Service.UsernameCooldownPeriod, 0)()
			defer tests.PrintCurrentTest(t)()

			assert.Contains(t, getPrompt(t), "The old username will redirect until someone claims it.")
		})

		t.Run("With cooldown", func(t *testing.T) {
			defer test.MockVariableValue(&setting.Service.UsernameCooldownPeriod, 8)()
			defer tests.PrintCurrentTest(t)()

			assert.Contains(t, getPrompt(t), "The old username will be available to everyone after a cooldown period of 8 days. You can still reclaim the old username during the cooldown period.")
		})
	})

	t.Run("Org settings note", func(t *testing.T) {
		getPrompt := func(t *testing.T) string {
			req := NewRequest(t, "GET", "/org/org3/settings")
			resp := session.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			return htmlDoc.Find("#org_name + .help").Text()
		}

		t.Run("No cooldown", func(t *testing.T) {
			defer test.MockVariableValue(&setting.Service.UsernameCooldownPeriod, 0)()
			defer tests.PrintCurrentTest(t)()

			assert.Contains(t, getPrompt(t), "The old name will redirect until it is claimed.")
		})

		t.Run("With cooldown", func(t *testing.T) {
			defer test.MockVariableValue(&setting.Service.UsernameCooldownPeriod, 8)()
			defer tests.PrintCurrentTest(t)()

			assert.Contains(t, getPrompt(t), "The old organization name will be available to everyone after a cooldown period of 8 days, you can still reclaim the old name during the cooldown period.")
		})
	})
}

// NOTE: This is a unit test but written in the integration test to ensure this runs on all databases.
func TestLimitUserRedirects(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	_, err := db.GetEngine(db.DefaultContext).NoAutoTime().Insert(&user_model.Redirect{RedirectUserID: 1, LowerName: "legacy", CreatedUnix: 0},
		&user_model.Redirect{RedirectUserID: 1, LowerName: "past", CreatedUnix: timeutil.TimeStampNow().AddDuration(-48 * time.Hour)},
		&user_model.Redirect{RedirectUserID: 1, LowerName: "recent", CreatedUnix: timeutil.TimeStampNow().AddDuration(-12 * time.Hour)},
		&user_model.Redirect{RedirectUserID: 1, LowerName: "future", CreatedUnix: timeutil.TimeStampNow().AddDuration(time.Hour)})
	require.NoError(t, err)

	require.NoError(t, user_model.LimitUserRedirects(db.DefaultContext, 1, 3))

	unittest.AssertExistsIf(t, false, &user_model.Redirect{LowerName: "legacy"})
	unittest.AssertExistsIf(t, true, &user_model.Redirect{LowerName: "past"})
	unittest.AssertExistsIf(t, true, &user_model.Redirect{LowerName: "recent"})
	unittest.AssertExistsIf(t, true, &user_model.Redirect{LowerName: "future"})

	require.NoError(t, user_model.LimitUserRedirects(db.DefaultContext, 1, 1))

	unittest.AssertExistsIf(t, false, &user_model.Redirect{LowerName: "legacy"})
	unittest.AssertExistsIf(t, false, &user_model.Redirect{LowerName: "past"})
	unittest.AssertExistsIf(t, false, &user_model.Redirect{LowerName: "recent"})
	unittest.AssertExistsIf(t, true, &user_model.Redirect{LowerName: "future"})
}

// NOTE: This is a unit test but written in the integration test to ensure this runs on all databases.
func TestCanClaimUsername(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	_, err := db.GetEngine(db.DefaultContext).NoAutoTime().Insert(&user_model.Redirect{RedirectUserID: 1, LowerName: "legacy", CreatedUnix: 0},
		&user_model.Redirect{RedirectUserID: 1, LowerName: "past", CreatedUnix: timeutil.TimeStampNow().AddDuration(-48 * time.Hour)},
		&user_model.Redirect{RedirectUserID: 1, LowerName: "recent", CreatedUnix: timeutil.TimeStampNow().AddDuration(-12 * time.Hour)},
		&user_model.Redirect{RedirectUserID: 1, LowerName: "future", CreatedUnix: timeutil.TimeStampNow().AddDuration(time.Hour)},
		&user_model.Redirect{RedirectUserID: 3, LowerName: "recent-org", CreatedUnix: timeutil.TimeStampNow().AddDuration(-12 * time.Hour)})
	require.NoError(t, err)

	testCase := func(t *testing.T, legacy, past, recent, future bool, doerID int64) {
		t.Helper()

		ok, _, err := user_model.CanClaimUsername(db.DefaultContext, "legacy", doerID)
		require.NoError(t, err)
		assert.Equal(t, legacy, ok)

		ok, _, err = user_model.CanClaimUsername(db.DefaultContext, "past", doerID)
		require.NoError(t, err)
		assert.Equal(t, past, ok)

		ok, _, err = user_model.CanClaimUsername(db.DefaultContext, "recent", doerID)
		require.NoError(t, err)
		assert.Equal(t, recent, ok)

		ok, _, err = user_model.CanClaimUsername(db.DefaultContext, "future", doerID)
		require.NoError(t, err)
		assert.Equal(t, future, ok)
	}

	t.Run("No cooldown", func(t *testing.T) {
		defer test.MockVariableValue(&setting.Service.UsernameCooldownPeriod, 0)()

		testCase(t, true, true, true, true, -1)
	})

	t.Run("1 day cooldown", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		defer test.MockVariableValue(&setting.Service.UsernameCooldownPeriod, 1)()

		testCase(t, true, true, false, false, -1)
	})

	t.Run("1 week cooldown", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		defer test.MockVariableValue(&setting.Service.UsernameCooldownPeriod, 7)()

		testCase(t, true, false, false, false, -1)

		t.Run("Own username", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			defer test.MockVariableValue(&setting.Service.UsernameCooldownPeriod, 7)()

			testCase(t, true, true, true, true, 1)
		})
	})

	t.Run("Organisation", func(t *testing.T) {
		defer test.MockVariableValue(&setting.Service.UsernameCooldownPeriod, 1)()

		t.Run("Not owner", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			ok, _, err := user_model.CanClaimUsername(db.DefaultContext, "recent-org", -1)
			require.NoError(t, err)
			assert.False(t, ok)
		})
		t.Run("Owner", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			ok, _, err := user_model.CanClaimUsername(db.DefaultContext, "recent-org", 2)
			require.NoError(t, err)
			assert.True(t, ok)
		})
	})
}
