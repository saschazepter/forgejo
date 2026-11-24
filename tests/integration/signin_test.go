// Copyright 2017 The Gitea Authors. All rights reserved.
// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"forgejo.org/models/auth"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/modules/translation"
	"forgejo.org/services/forms"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
)

func testLoginFailed(t *testing.T, username, password, message string) {
	session := emptyTestSession(t)
	req := NewRequestWithValues(t, "POST", "/user/login", map[string]string{
		"user_name": username,
		"password":  password,
	})
	resp := session.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	resultMsg := htmlDoc.doc.Find(".ui.message>p").Text()

	assert.Equal(t, message, resultMsg)
}

func TestSignin(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	// add new user with user2's email
	user.Name = "testuser"
	user.LowerName = strings.ToLower(user.Name)
	user.ID = 0
	unittest.AssertSuccessfulInsert(t, user)

	samples := []struct {
		username string
		password string
		message  string
	}{
		{username: "wrongUsername", password: "wrongPassword", message: translation.NewLocale("en-US").TrString("form.username_password_incorrect")},
		{username: "wrongUsername", password: "password", message: translation.NewLocale("en-US").TrString("form.username_password_incorrect")},
		{username: "user15", password: "wrongPassword", message: translation.NewLocale("en-US").TrString("form.username_password_incorrect")},
		{username: "user1@example.com", password: "wrongPassword", message: translation.NewLocale("en-US").TrString("form.username_password_incorrect")},
	}

	for _, s := range samples {
		testLoginFailed(t, s.username, s.password, s.message)
	}
}

func TestSigninWithRememberMe(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	baseURL, _ := url.Parse(setting.AppURL)

	session := emptyTestSession(t)
	req := NewRequestWithValues(t, "POST", "/user/login", map[string]string{
		"user_name": user.Name,
		"password":  userPassword,
		"remember":  "on",
	})
	session.MakeRequest(t, req, http.StatusSeeOther)

	c := session.GetCookie(setting.CookieRememberName)
	assert.NotNil(t, c)

	session = emptyTestSession(t)

	// Without session the settings page should not be reachable
	req = NewRequest(t, "GET", "/user/settings")
	session.MakeRequest(t, req, http.StatusSeeOther)

	req = NewRequest(t, "GET", "/user/login")
	// Set the remember me cookie for the login GET request
	session.jar.SetCookies(baseURL, []*http.Cookie{c})
	session.MakeRequest(t, req, http.StatusSeeOther)

	// With session the settings page should be reachable
	req = NewRequest(t, "GET", "/user/settings")
	session.MakeRequest(t, req, http.StatusOK)
}

func TestProviderDisplayNameIsPathEscaped(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	testCases := []string{
		"sla/shed",
		"per%cent",
		"que?ry=string",
		"ha#sh",
		"spa ce",     // doesn't break the path
		"pl+us",      // unchanged by url.PathEscape
		"amper&sand", // unchanged by url.PathEscape
	}

	for _, testCase := range testCases {
		// GitLab is only used here for convenience.
		addAuthSource(t, authSourcePayloadGitLabCustom(testCase))
	}

	request := NewRequest(t, "GET", "/user/login")
	response := MakeRequest(t, request, http.StatusOK)
	htmlDoc := NewHTMLParser(t, response.Body)

	for _, testCase := range testCases {
		t.Run(testCase, func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			htmlDoc.AssertElement(t, fmt.Sprintf("a.oauth-login-link[href='/user/oauth2/%s']", url.PathEscape(testCase)), true)
		})
	}
}

func TestDisableSignin(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	// Mock alternative auth ways as enabled
	defer test.MockVariableValue(&setting.Service.EnableOpenIDSignIn, true)()
	defer test.MockVariableValue(&setting.Service.EnableOpenIDSignUp, true)()
	t.Run("Disabled", func(t *testing.T) {
		defer test.MockVariableValue(&setting.Service.EnableInternalSignIn, false)()

		t.Run("UI", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", "/user/login")
			resp := MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)
			htmlDoc.AssertElement(t, "form[action='/user/login']", false)
			htmlDoc.AssertElement(t, ".divider-text", false)
		})

		t.Run("Signin", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			req := NewRequest(t, "POST", "/user/login")
			MakeRequest(t, req, http.StatusForbidden)
		})
	})

	t.Run("Enabled", func(t *testing.T) {
		defer test.MockVariableValue(&setting.Service.EnableInternalSignIn, true)()

		t.Run("UI", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", "/user/login")
			resp := MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)
			htmlDoc.AssertElement(t, "form[action='/user/login']", true)
			htmlDoc.AssertElement(t, ".divider-text", true)
		})

		t.Run("Signin", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			req := NewRequest(t, "POST", "/user/login")
			MakeRequest(t, req, http.StatusOK)
		})
	})
}

func TestGlobalTwoFactorRequirement(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	locale := translation.NewLocale("en-US")

	adminUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	normalUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	restrictedUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 29})

	runTest := func(t *testing.T, user *user_model.User, useTOTP, loginAllowed bool) {
		t.Helper()
		defer unittest.AssertSuccessfulDelete(t, &auth.TwoFactor{UID: user.ID})

		session := loginUserMaybeTOTP(t, user, useTOTP)

		req := NewRequest(t, "GET", fmt.Sprintf("/%s", user.Name))

		if loginAllowed {
			session.MakeRequest(t, req, http.StatusOK)

			// not found page
			req = NewRequest(t, "GET", "/absolutly/not/found")
			req.Header.Add("Accept", "text/html")
			resp := session.MakeRequest(t, req, http.StatusNotFound)
			htmlDoc := NewHTMLParser(t, resp.Body)
			assert.Greater(t, htmlDoc.Find(".navbar-left > a.item").Length(), 1) // show the Logo, and other links
			assert.Greater(t, htmlDoc.Find(".navbar-right .dialog-dropdown a").Length(), 1)

			// demo pages are using ignSignIn and are expected to be accessible with loginAllowed
			reset := enableDemoPages()
			req = NewRequest(t, "GET", "/-/demo/error/500")
			req.Header.Add("Accept", "text/html")
			resp = session.MakeRequest(t, req, http.StatusInternalServerError)
			htmlDoc = NewHTMLParser(t, resp.Body)
			assert.Equal(t, 1, htmlDoc.Find(".navbar-left > a.item").Length())
			htmlDoc.AssertElement(t, ".navbar-right", false)
			reset()
		} else {
			resp := session.MakeRequest(t, req, http.StatusSeeOther)
			assert.Equal(t, "/user/settings/security", resp.Header().Get("Location"))

			// not found page
			req = NewRequest(t, "GET", "/absolutly/not/found")
			req.Header.Add("Accept", "text/html")
			resp = session.MakeRequest(t, req, http.StatusNotFound)
			htmlDoc := NewHTMLParser(t, resp.Body)
			assert.Equal(t, 1, htmlDoc.Find(".navbar-left > a.item").Length()) // only show the Logo, no other links

			userLinks := htmlDoc.Find(".navbar-right .dialog-dropdown button[form='logout-user-action']")
			assert.Equal(t, 1, userLinks.Length()) // only logout link
			assert.Equal(t, "Sign out", strings.TrimSpace(userLinks.Text()))

			// demo pages are using ignSignIn and should redirect like any other pages if 2FA is required but missing
			reset := enableDemoPages()
			req = NewRequest(t, "GET", "/-/demo/error/500")
			req.Header.Add("Accept", "text/html")
			resp = session.MakeRequest(t, req, http.StatusSeeOther)
			assert.Equal(t, "/user/settings/security", resp.Header().Get("Location"))
			reset()

			// 2fa page
			req = NewRequest(t, "GET", "/user/settings/security")
			resp = session.MakeRequest(t, req, http.StatusOK)
			htmlDoc = NewHTMLParser(t, resp.Body)
			assert.Equal(t, locale.TrString("settings.must_enable_2fa"), htmlDoc.Find(".ui.red.message").Text())
			assert.Equal(t, 1, htmlDoc.Find(".navbar-left > a.item").Length()) // only show the Logo, no other links

			userLinks = htmlDoc.Find(".navbar-right .dialog-dropdown button[form='logout-user-action']")
			assert.Equal(t, 1, userLinks.Length()) // only logout link
			assert.Equal(t, "Sign out", strings.TrimSpace(userLinks.Text()))

			assert.Equal(t, 0, htmlDoc.FindByText("a", locale.TrString("settings.twofa_reenroll")).Length())

			headings := htmlDoc.Find(".user-setting-content h4.attached.header")
			assert.Equal(t, 3, headings.Length())
			assert.Equal(t, locale.TrString("settings.twofa"), strings.TrimSpace(headings.Eq(0).Text()))
			assert.Equal(t, locale.TrString("settings.webauthn"), strings.TrimSpace(headings.Eq(1).Text()))
			assert.Equal(t, locale.TrString("settings.sessions"), strings.TrimSpace(headings.Eq(2).Text()))
		}
	}

	t.Run("NoneTwoFactorRequirement", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		t.Run("no 2fa", func(t *testing.T) {
			runTest(t, adminUser, false, true)
			runTest(t, normalUser, false, true)
			runTest(t, restrictedUser, false, true)
		})

		t.Run("enabled 2fa", func(t *testing.T) {
			runTest(t, adminUser, true, true)
			runTest(t, normalUser, true, true)
			runTest(t, restrictedUser, true, true)
		})
	})

	t.Run("AllTwoFactorRequirement", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		defer test.MockVariableValue(&setting.GlobalTwoFactorRequirement, setting.AllTwoFactorRequirement)()

		t.Run("no 2fa", func(t *testing.T) {
			runTest(t, adminUser, false, false)
			runTest(t, normalUser, false, false)
			runTest(t, restrictedUser, false, false)
		})

		t.Run("enabled 2fa", func(t *testing.T) {
			runTest(t, adminUser, true, true)
			runTest(t, normalUser, true, true)
			runTest(t, restrictedUser, true, true)
		})
	})

	t.Run("AdminTwoFactorRequirement", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		defer test.MockVariableValue(&setting.GlobalTwoFactorRequirement, setting.AdminTwoFactorRequirement)()

		t.Run("no 2fa", func(t *testing.T) {
			runTest(t, adminUser, false, false)
			runTest(t, normalUser, false, true)
			runTest(t, restrictedUser, false, true)
		})

		t.Run("enabled 2fa", func(t *testing.T) {
			runTest(t, adminUser, true, true)
			runTest(t, normalUser, true, true)
			runTest(t, restrictedUser, true, true)
		})
	})
}

func TestTwoFactorWithPasswordChange(t *testing.T) {
	defer unittest.OverrideFixtures("models/fixtures/TestTwoFactorWithPasswordChange")()

	normalUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	changePasswordUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{MustChangePassword: true})

	runTest := func(t *testing.T, user *user_model.User, requireTOTP bool) {
		t.Helper()
		defer unittest.AssertSuccessfulDelete(t, &auth.TwoFactor{UID: user.ID})

		session := loginUser(t, user.Name)

		if user.MustChangePassword {
			req := NewRequest(t, "GET", fmt.Sprintf("/%s", user.Name))
			resp := session.MakeRequest(t, req, http.StatusSeeOther)
			assert.Equal(t, "/user/settings/change_password", resp.Header().Get("Location"))

			req = NewRequest(t, "GET", "/user/settings/security")
			resp = session.MakeRequest(t, req, http.StatusSeeOther)
			assert.Equal(t, "/user/settings/change_password", resp.Header().Get("Location"))

			req = NewRequestWithJSON(t, "POST", "/user/settings/change_password", forms.MustChangePasswordForm{
				Password: "password",
				Retype:   "password",
			})
			resp = session.MakeRequest(t, req, http.StatusOK)
			assert.Equal(t, "/user/settings/security", resp.Header().Get("Location"))
		}

		if requireTOTP {
			req := NewRequest(t, "GET", fmt.Sprintf("/%s", user.Name))
			resp := session.MakeRequest(t, req, http.StatusSeeOther)
			assert.Equal(t, "/user/settings/security", resp.Header().Get("Location"))

			req = NewRequest(t, "GET", "/user/settings/change_password")
			resp = session.MakeRequest(t, req, http.StatusSeeOther)
			assert.Equal(t, "/user/settings/security", resp.Header().Get("Location"))

			session.EnrollTOTP(t)
		}

		req := NewRequest(t, "GET", fmt.Sprintf("/%s", user.Name))
		session.MakeRequest(t, req, http.StatusOK)
	}

	t.Run("Don't require TwoFactor", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		runTest(t, normalUser, false)
		runTest(t, changePasswordUser, false)
	})

	t.Run("Require TwoFactor", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		defer test.MockVariableValue(&setting.GlobalTwoFactorRequirement, setting.AllTwoFactorRequirement)()

		runTest(t, normalUser, true)
		runTest(t, changePasswordUser, true)
	})
}
