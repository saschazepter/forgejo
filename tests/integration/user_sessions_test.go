// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"net/http"
	"testing"

	"forgejo.org/models/auth"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserSignOutSessions(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user2"})
	otherUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user4"})

	assertSignedIn := func(t *testing.T, session *TestSession, signedIn bool) {
		t.Helper()
		expected := http.StatusSeeOther
		if signedIn {
			expected = http.StatusOK
		}
		session.MakeRequest(t, NewRequest(t, "GET", "/user/settings/security"), expected)
	}

	first := loginUser(t, user.Name)
	second := loginUserWithPasswordRemember(t, user.Name, userPassword, true)
	rememberCookie := second.GetCookie(setting.CookieRememberName)
	require.NotNil(t, rememberCookie)
	otherUserSession := loginUser(t, otherUser.Name)

	doc := getHTMLDoc(t, first, "/user/settings/security", http.StatusOK)
	doc.AssertElement(t, "button[data-url$='/sessions/sign_out']", true)

	// Sign out all browsers for user2.
	first.MakeRequest(t, NewRequest(t, "POST", "/user/settings/security/sessions/sign_out"), http.StatusOK)

	// Both active sessions of user2 are now signed out.
	assertSignedIn(t, first, false)
	assertSignedIn(t, second, false)

	// The remember-me (LTA) token of user2 is also invalidated.
	unittest.AssertNotExistsBean(t, &auth.AuthorizationToken{UID: user.ID})
	ltaSession := GetSessionForLTACookie(t, rememberCookie)
	ltaSession.MakeRequest(t, NewRequest(t, "GET", "/user/login"), http.StatusOK)

	// Other users are unaffected.
	assertSignedIn(t, otherUserSession, true)

	// Signing in again works with the bumped session generation.
	relogged := loginUser(t, user.Name)
	assertSignedIn(t, relogged, true)
	updatedUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: user.ID})
	assert.EqualValues(t, 1, updatedUser.SessionGeneration)
}
