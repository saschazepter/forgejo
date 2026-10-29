// Copyright 2017 The Gitea Authors. All rights reserved.
// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	unit_model "forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	"forgejo.org/modules/translation"
	app_context "forgejo.org/services/context"
	"forgejo.org/services/mailer"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/PuerkitoBio/goquery"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/html"
)

func TestViewUser(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	req := NewRequest(t, "GET", "/user2")
	MakeRequest(t, req, http.StatusOK)

	t.Run("REQUIRE_SIGNIN_VIEW", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		defer test.MockVariableValue(&setting.Service.RequireSignInView, true)()
		req := NewRequest(t, "GET", "/user2")
		resp := MakeRequest(t, req, http.StatusSeeOther)
		assert.Equal(t, "/user/login", resp.Header().Get("Location"))
	})
}

func TestRenameUsername(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")
	req := NewRequestWithValues(t, "POST", "/user/settings", map[string]string{
		"name":     "newUsername",
		"email":    "user2@example.com",
		"language": "en-US",
	})
	session.MakeRequest(t, req, http.StatusSeeOther)

	unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "newUsername"})
	unittest.AssertNotExistsBean(t, &user_model.User{Name: "user2"})
}

func TestRenameInvalidUsername(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	invalidUsernames := []string{
		"%2f*",
		"%2f.",
		"%2f..",
		"%00",
		"thisHas ASpace",
		"p<A>tho>lo<gical",
		".",
		"..",
		".well-known",
		".abc",
		"abc.",
		"a..bc",
		"a...bc",
		"a.-bc",
		"a._bc",
		"a_-bc",
		"a/bc",
		"☁️",
		"-",
		"--diff",
		"-im-here",
		"a space",
	}

	session := loginUser(t, "user2")
	for _, invalidUsername := range invalidUsernames {
		t.Logf("Testing username %s", invalidUsername)

		req := NewRequestWithValues(t, "POST", "/user/settings", map[string]string{
			"name":  invalidUsername,
			"email": "user2@example.com",
		})
		resp := session.MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)
		assert.Contains(t,
			htmlDoc.doc.Find(".ui.negative.message").Text(),
			translation.NewLocale("en-US").TrString("form.username_error"),
		)

		unittest.AssertNotExistsBean(t, &user_model.User{Name: invalidUsername})
	}
}

func TestRenameReservedUsername(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	reservedUsernames := []string{
		// ".", "..", ".well-known", // The names are not only reserved but also invalid
		"admin",
		"api",
		"assets",
		"attachments",
		"avatar",
		"avatars",
		"captcha",
		"explore",
		"favicon.ico",
		"ghost",
		"issues",
		"login",
		"manifest.json",
		"metrics",
		"milestones",
		"notifications",
		"org",
		"pulls",
		"repo",
		"repo-avatars",
		"robots.txt",
		"ssh_info",
		"swagger.v1.json",
		"user",
		"v2",
	}

	session := loginUser(t, "user2")
	for _, reservedUsername := range reservedUsernames {
		t.Logf("Testing username %s", reservedUsername)
		req := NewRequestWithValues(t, "POST", "/user/settings", map[string]string{
			"name":     reservedUsername,
			"email":    "user2@example.com",
			"language": "en-US",
		})
		resp := session.MakeRequest(t, req, http.StatusSeeOther)

		req = NewRequest(t, "GET", test.RedirectURL(resp))
		resp = session.MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)
		assert.Contains(t,
			htmlDoc.doc.Find(".ui.negative.message").Text(),
			translation.NewLocale("en-US").TrString("user.form.name_reserved", reservedUsername),
		)

		unittest.AssertNotExistsBean(t, &user_model.User{Name: reservedUsername})
	}
}

func TestExportUserGPGKeys(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	// Export empty key list
	testExportUserGPGKeys(t, "user1", `-----BEGIN PGP PUBLIC KEY BLOCK-----
Note: This user hasn't uploaded any GPG keys.


=twTO
-----END PGP PUBLIC KEY BLOCK-----`)
	// Import key
	// User1 <user1@example.com>
	session := loginUser(t, "user1")
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteUser)
	testCreateGPGKey(t, session.MakeRequest, token, http.StatusCreated, `-----BEGIN PGP PUBLIC KEY BLOCK-----

mQENBFyy/VUBCADJ7zbM20Z1RWmFoVgp5WkQfI2rU1Vj9cQHes9i42wVLLtcbPeo
QzubgzvMPITDy7nfWxgSf83E23DoHQ1ACFbQh/6eFSRrjsusp3YQ/08NSfPPbcu8
0M5G+VGwSfzS5uEcwBVQmHyKdcOZIERTNMtYZx1C3bjLD1XVJHvWz9D72Uq4qeO3
8SR+lzp5n6ppUakcmRnxt3nGRBj1+hEGkdgzyPo93iy+WioegY2lwCA9xMEo5dah
BmYxWx51zyiXYlReTaxlyb3/nuSUt8IcW3Q8zjdtJj4Nu8U1SpV8EdaA1I9IPbHW
510OSLmD3XhqHH5m6mIxL1YoWxk3V7gpDROtABEBAAG0GVVzZXIxIDx1c2VyMUBl
eGFtcGxlLmNvbT6JAU4EEwEIADgWIQTQEbrYxmXsp1z3j7z9+v0I6RSEHwUCXLL9
VQIbAwULCQgHAgYVCgkICwIEFgIDAQIeAQIXgAAKCRD9+v0I6RSEH22YCACFqL5+
6M0m18AMC/pumcpnnmvAS1GrrKTF8nOROA1augZwp1WCNuKw2R6uOJIHANrYECSn
u7+j6GBP2gbIW8mSAzS6HWCs7GGiPpVtT4wcu8wljUI6BxjpyZtoEkriyBjt6HfK
rkegbkuySoJvjq4IcO5D1LB1JWgsUjMYQJj/ZpBIzVtjG9QtFSOiT1Hct4PoZHdC
nsdSgyCkwRZXG+u3kT/wP9F663ba4o16vYlz3dCGo66lF2tyoG3qcyZ1OUzUrnuv
96ytAzT6XIhrE0nVoBprMxFF5zExotJD3bHjcGBFNLf944bhjKee3U6t9+OsfJVC
l7N5xxIawCuTQdbfuQENBFyy/VUBCADe61yGEoTwKfsOKIhxLaNoRmD883O0tiWt
soO/HPj9dPQLTOiwXgSgSCd8C+LNxGKct87wgFozpah4tDLC6c0nALuHJ0SLbkfz
55aRhLeOOcrAydatDp72GroXzqpZ0xZBk5wjIWdgEol2GmVRM8QGbeuakU/HVz5y
lPzxUUocgdbSi3GE3zbzijQzVJdyL/kw/KP7pKT/PPKKJ2C5NQDLy0XGKEHddXGR
EWKkVlRalxq/TjfaMR0bi3MpezBsQmp99ATPO/d7trayZUxQHRtXzGFiOXfDHATr
qN730sODjqvU+mpc/SHCRwh9qWDjZRHSuKU5YDBjb5jIQJivZsQ/ABEBAAGJATYE
GAEIACAWIQTQEbrYxmXsp1z3j7z9+v0I6RSEHwUCXLL9VQIbDAAKCRD9+v0I6RSE
H7WoB/4tXl+97rQ6owPCGSVp1Xbwt2521V7COgsOFRVTRTryEWxRW8mm0S7wQvax
C0TLXKur6NVYQMn01iyL+FZzRpEWNuYF3f9QeeLJ/+l2DafESNhNTy17+RPmacK6
21dccpqchByVw/UMDeHSyjQLiG2lxzt8Gfx2gHmSbrq3aWovTGyz6JTffZvfy/n2
0Hm437OBPazO0gZyXhdV2PE5RSUfvAgm44235tcV5EV0d32TJDfv61+Vr2GUbah6
7XhJ1v6JYuh8kaYaEz8OpZDeh7f6Ho6PzJrsy/TKTKhGgZNINj1iaPFyOkQgKR5M
GrE0MHOxUbc9tbtyk0F1SuzREUBH
=DDXw
-----END PGP PUBLIC KEY BLOCK-----`)
	// Export new key
	testExportUserGPGKeys(t, "user1", `-----BEGIN PGP PUBLIC KEY BLOCK-----

xsBNBFyy/VUBCADJ7zbM20Z1RWmFoVgp5WkQfI2rU1Vj9cQHes9i42wVLLtcbPeo
QzubgzvMPITDy7nfWxgSf83E23DoHQ1ACFbQh/6eFSRrjsusp3YQ/08NSfPPbcu8
0M5G+VGwSfzS5uEcwBVQmHyKdcOZIERTNMtYZx1C3bjLD1XVJHvWz9D72Uq4qeO3
8SR+lzp5n6ppUakcmRnxt3nGRBj1+hEGkdgzyPo93iy+WioegY2lwCA9xMEo5dah
BmYxWx51zyiXYlReTaxlyb3/nuSUt8IcW3Q8zjdtJj4Nu8U1SpV8EdaA1I9IPbHW
510OSLmD3XhqHH5m6mIxL1YoWxk3V7gpDROtABEBAAHNGVVzZXIxIDx1c2VyMUBl
eGFtcGxlLmNvbT7CwI4EEwEIADgWIQTQEbrYxmXsp1z3j7z9+v0I6RSEHwUCXLL9
VQIbAwULCQgHAgYVCgkICwIEFgIDAQIeAQIXgAAKCRD9+v0I6RSEH22YCACFqL5+
6M0m18AMC/pumcpnnmvAS1GrrKTF8nOROA1augZwp1WCNuKw2R6uOJIHANrYECSn
u7+j6GBP2gbIW8mSAzS6HWCs7GGiPpVtT4wcu8wljUI6BxjpyZtoEkriyBjt6HfK
rkegbkuySoJvjq4IcO5D1LB1JWgsUjMYQJj/ZpBIzVtjG9QtFSOiT1Hct4PoZHdC
nsdSgyCkwRZXG+u3kT/wP9F663ba4o16vYlz3dCGo66lF2tyoG3qcyZ1OUzUrnuv
96ytAzT6XIhrE0nVoBprMxFF5zExotJD3bHjcGBFNLf944bhjKee3U6t9+OsfJVC
l7N5xxIawCuTQdbfzsBNBFyy/VUBCADe61yGEoTwKfsOKIhxLaNoRmD883O0tiWt
soO/HPj9dPQLTOiwXgSgSCd8C+LNxGKct87wgFozpah4tDLC6c0nALuHJ0SLbkfz
55aRhLeOOcrAydatDp72GroXzqpZ0xZBk5wjIWdgEol2GmVRM8QGbeuakU/HVz5y
lPzxUUocgdbSi3GE3zbzijQzVJdyL/kw/KP7pKT/PPKKJ2C5NQDLy0XGKEHddXGR
EWKkVlRalxq/TjfaMR0bi3MpezBsQmp99ATPO/d7trayZUxQHRtXzGFiOXfDHATr
qN730sODjqvU+mpc/SHCRwh9qWDjZRHSuKU5YDBjb5jIQJivZsQ/ABEBAAHCwHYE
GAEIACAWIQTQEbrYxmXsp1z3j7z9+v0I6RSEHwUCXLL9VQIbDAAKCRD9+v0I6RSE
H7WoB/4tXl+97rQ6owPCGSVp1Xbwt2521V7COgsOFRVTRTryEWxRW8mm0S7wQvax
C0TLXKur6NVYQMn01iyL+FZzRpEWNuYF3f9QeeLJ/+l2DafESNhNTy17+RPmacK6
21dccpqchByVw/UMDeHSyjQLiG2lxzt8Gfx2gHmSbrq3aWovTGyz6JTffZvfy/n2
0Hm437OBPazO0gZyXhdV2PE5RSUfvAgm44235tcV5EV0d32TJDfv61+Vr2GUbah6
7XhJ1v6JYuh8kaYaEz8OpZDeh7f6Ho6PzJrsy/TKTKhGgZNINj1iaPFyOkQgKR5M
GrE0MHOxUbc9tbtyk0F1SuzREUBH
=WFf5
-----END PGP PUBLIC KEY BLOCK-----`)
}

func testExportUserGPGKeys(t *testing.T, user, expected string) {
	session := loginUser(t, user)
	t.Logf("Testing username %s export gpg keys", user)
	req := NewRequest(t, "GET", "/"+user+".gpg")
	resp := session.MakeRequest(t, req, http.StatusOK)
	// t.Log(resp.Body.String())
	assert.Equal(t, expected, resp.Body.String())
}

func TestAccessTokenRegenerate(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user1")
	prevLatestTokenName, prevLatestTokenID := findLatestTokenID(t, session)

	createApplicationSettingsToken(t, session, "TestAccessToken", auth_model.AccessTokenScopeWriteUser)
	oldToken := assertAccessToken(t, session)
	oldTokenName, oldTokenID := findLatestTokenID(t, session)

	assert.Equal(t, "TestAccessToken", oldTokenName)

	req := NewRequestWithValues(t, "POST", "/user/settings/applications/tokens/regenerate", map[string]string{
		"id": strconv.Itoa(oldTokenID),
	})
	session.MakeRequest(t, req, http.StatusOK)

	newToken := assertAccessToken(t, session)
	newTokenName, newTokenID := findLatestTokenID(t, session)

	assert.NotEqual(t, oldToken, newToken)
	assert.Equal(t, oldTokenID, newTokenID)
	assert.Equal(t, "TestAccessToken", newTokenName)

	req = NewRequestWithValues(t, "POST", "/user/settings/applications/tokens/delete", map[string]string{
		"id": strconv.Itoa(newTokenID),
	})
	session.MakeRequest(t, req, http.StatusOK)

	latestTokenName, latestTokenID := findLatestTokenID(t, session)

	assert.Less(t, latestTokenID, oldTokenID)
	assert.Equal(t, latestTokenID, prevLatestTokenID)
	assert.Equal(t, latestTokenName, prevLatestTokenName)
	assert.NotEqual(t, "TestAccessToken", latestTokenName)
}

func TestAccessTokenResourceRepos(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	locale := translation.NewLocale("en-US")
	repoAccess := locale.TrString("settings.specific_repo_access") + ":"

	session := loginUser(t, "user2")

	// Before creating a repo-specific access token, we shouldn't have the "Repository access:" list in the personal
	// access token page:
	req := NewRequest(t, "GET", "/user/settings/applications")
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)
	htmlDoc.AssertSelection(t, htmlDoc.FindByText(".user-setting-content p", repoAccess), false)

	// Then we create a repo-specific access token.  We give it access to two repos, user2/repo2, but also user30/empty,
	// a private repo owned by someone else...  We'll pretend user2 used to be a collaborator on this repo and
	// previously had access to view it, but doesn't anymore.
	createFineGrainedRepoAccessToken(t, "user2",
		[]auth_model.AccessTokenScope{auth_model.AccessTokenScopeReadUser},
		[]int64{2, 52},
	)

	// Now we have "Repository access:"...
	req = NewRequest(t, "GET", "/user/settings/applications")
	resp = session.MakeRequest(t, req, http.StatusOK)
	htmlDoc = NewHTMLParser(t, resp.Body)
	htmlDoc.AssertSelection(t, htmlDoc.FindByText(".user-setting-content p", repoAccess), true)
	htmlDoc.AssertSelection(t, htmlDoc.FindByText(".user-setting-content a", "user2/repo2"), true)   // link to repo
	htmlDoc.AssertSelection(t, htmlDoc.FindByText(".user-setting-content a", "user30/empty"), false) // missing - user2 has no visibility
}

func findLatestTokenID(t *testing.T, session *TestSession) (string, int) {
	req := NewRequest(t, "GET", "/user/settings/applications")
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)
	latestTokenName := ""
	latestTokenID := 0
	htmlDoc.Find(".delete-button").Each(func(i int, s *goquery.Selection) {
		tokenID, exists := s.Attr("data-id")

		if !exists || tokenID == "" {
			return
		}

		id, err := strconv.Atoi(tokenID)
		require.NoError(t, err)
		if id > latestTokenID {
			latestTokenName = s.Parent().Parent().Find(".flex-item-title").Text()
			latestTokenID = id
		}
	})

	return latestTokenName, latestTokenID
}

func TestGetUserRss(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	t.Run("Normal", func(t *testing.T) {
		user34 := "the_34-user.with.all.allowedChars"
		req := NewRequestf(t, "GET", "/%s.rss", user34)
		resp := MakeRequest(t, req, http.StatusOK)
		if assert.Equal(t, "application/rss+xml;charset=utf-8", resp.Header().Get("Content-Type")) {
			rssDoc := NewHTMLParser(t, resp.Body).Find("channel")
			title, _ := rssDoc.ChildrenFiltered("title").Html()
			assert.Equal(t, "Feed of &#34;the_1-user.with.all.allowedChars&#34;", title)
			description, _ := rssDoc.ChildrenFiltered("description").Html()
			assert.Equal(t, "&lt;p dir=&#34;auto&#34;&gt;some &lt;a href=&#34;https://commonmark.org/&#34; rel=&#34;nofollow&#34;&gt;commonmark&lt;/a&gt;!&lt;/p&gt;\n", description)
		}
	})
	t.Run("Non-existent user", func(t *testing.T) {
		session := loginUser(t, "user2")
		req := NewRequestf(t, "GET", "/non-existent-user.rss")
		session.MakeRequest(t, req, http.StatusNotFound)
	})
}

func TestListStopWatches(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	session := loginUser(t, owner.Name)
	req := NewRequest(t, "GET", "/user/stopwatches")
	resp := session.MakeRequest(t, req, http.StatusOK)
	var apiWatches []*api.StopWatch
	DecodeJSON(t, resp, &apiWatches)
	stopwatch := unittest.AssertExistsAndLoadBean(t, &issues_model.Stopwatch{UserID: owner.ID})
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: stopwatch.IssueID})
	if assert.Len(t, apiWatches, 1) {
		assert.Equal(t, stopwatch.CreatedUnix.AsTime().Unix(), apiWatches[0].Created.Unix())
		assert.Equal(t, issue.Index, apiWatches[0].IssueIndex)
		assert.Equal(t, issue.Title, apiWatches[0].IssueTitle)
		assert.Equal(t, repo.Name, apiWatches[0].RepoName)
		assert.Equal(t, repo.OwnerName, apiWatches[0].RepoOwnerName)
		assert.Positive(t, apiWatches[0].Seconds)
	}
}

func TestUserLocationMapLink(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&setting.Service.UserLocationMapURL, "https://example/foo/")()

	session := loginUser(t, "user2")
	req := NewRequestWithValues(t, "POST", "/user/settings", map[string]string{
		"name":     "user2",
		"email":    "user@example.com",
		"language": "en-US",
		"location": "A/b",
	})
	session.MakeRequest(t, req, http.StatusSeeOther)

	req = NewRequest(t, "GET", "/user2/")
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)
	htmlDoc.AssertElement(t, `a[href="https://example/foo/A%2Fb"]`, true)
}

func TestUserHints(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user2"})
	session := loginUser(t, user.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteUser)

	// Create a known-good repo, with only one unit enabled
	repo := forgery.CreateRepository(t, user, nil)
	forgery.EnableRepoUnits(t, repo, unit_model.TypeCode)
	forgery.DisableRepoUnits(t, repo,
		unit_model.TypePullRequests,
		unit_model.TypeProjects,
		unit_model.TypePackages,
		unit_model.TypeActions,
		unit_model.TypeIssues,
		unit_model.TypeWiki,
	)

	ensureRepoUnitHints := func(t *testing.T, hints bool) {
		t.Helper()

		req := NewRequestWithJSON(t, "PATCH", "/api/v1/user/settings", &api.UserSettingsOptions{
			EnableRepoUnitHints: &hints,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusOK)

		var userSettings api.UserSettings
		DecodeJSON(t, resp, &userSettings)
		assert.Equal(t, hints, userSettings.EnableRepoUnitHints)
	}

	t.Run("API", func(t *testing.T) {
		t.Run("setting hints on and off", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			ensureRepoUnitHints(t, true)
			ensureRepoUnitHints(t, false)
		})

		t.Run("retrieving settings", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			for _, v := range []bool{true, false} {
				ensureRepoUnitHints(t, v)

				req := NewRequest(t, "GET", "/api/v1/user/settings").AddTokenAuth(token)
				resp := MakeRequest(t, req, http.StatusOK)

				var userSettings api.UserSettings
				DecodeJSON(t, resp, &userSettings)
				assert.Equal(t, v, userSettings.EnableRepoUnitHints)
			}
		})
	})

	t.Run("user settings", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		// Set a known-good state, that isn't the default
		ensureRepoUnitHints(t, false)

		assertHintState := func(t *testing.T, enabled bool) {
			t.Helper()

			req := NewRequest(t, "GET", "/user/settings/appearance")
			resp := session.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			_, hintChecked := htmlDoc.Find(`input[name="enable_repo_unit_hints"]`).Attr("checked")
			assert.Equal(t, enabled, hintChecked)

			link, _ := htmlDoc.Find("form[action='/user/settings/appearance/language'] a").Attr("href")
			assert.Equal(t, "https://forgejo.org/docs/next/contributor/localization/", link) // NOT derived from setting.AppDocsVer (contrib docs aren't tied to the present version!)
		}

		t.Run("view", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			assertHintState(t, false)
		})

		t.Run("change", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequestWithValues(t, "POST", "/user/settings/appearance/hints", map[string]string{
				"enable_repo_unit_hints": "true",
			})
			session.MakeRequest(t, req, http.StatusSeeOther)

			assertHintState(t, true)
		})
	})

	t.Run("repo view", func(t *testing.T) {
		assertAddMore := func(t *testing.T, present bool) {
			t.Helper()

			// check if the tab is present
			req := NewRequest(t, "GET", repo.Link())
			resp := session.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			htmlDoc.AssertElement(t, fmt.Sprintf("a[href='%s/settings/units']", repo.Link()), present)

			// check if the user settings hint is present
			req = NewRequest(t, "GET", repo.Link()+"/settings/units")
			resp = session.MakeRequest(t, req, http.StatusOK)
			htmlDoc = NewHTMLParser(t, resp.Body)

			htmlDoc.AssertElement(t, ".user-main-content a[href='/user/settings/appearance#hints']", present)
		}

		t.Run("hints enabled", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			ensureRepoUnitHints(t, true)
			assertAddMore(t, true)
		})

		t.Run("hints disabled", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			ensureRepoUnitHints(t, false)
			assertAddMore(t, false)
		})
	})
}

func TestUserPronouns(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	// user1 is admin, using user2 and user10 respectively instead.
	// This is explicitly mentioned here because of the unconventional
	// variable naming scheme.
	firstUserSession := loginUser(t, "user2")
	firstUserToken := getTokenForLoggedInUser(t, firstUserSession, auth_model.AccessTokenScopeWriteUser)

	// This user has the HidePronouns setting enabled.
	// Check the fixture!
	secondUserSession := loginUser(t, "user10")
	secondUserToken := getTokenForLoggedInUser(t, secondUserSession, auth_model.AccessTokenScopeWriteUser)

	adminUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{IsAdmin: true})
	adminSession := loginUser(t, adminUser.Name)
	adminToken := getTokenForLoggedInUser(t, adminSession, auth_model.AccessTokenScopeWriteAdmin)

	t.Run("API", func(t *testing.T) {
		t.Run("user", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// secondUserToken was chosen arbitrarily and should have no impact.
			// See next comment.
			req := NewRequest(t, "GET", "/api/v1/user").AddTokenAuth(secondUserToken)
			resp := firstUserSession.MakeRequest(t, req, http.StatusOK)

			// We check the raw JSON, because we want to test the response, not
			// what it decodes into. Contents doesn't matter, we're testing the
			// presence only.
			assert.Contains(t, resp.Body.String(), `"pronouns":`)
		})

		t.Run("users/{username}", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", "/api/v1/users/user2")
			resp := MakeRequest(t, req, http.StatusOK)

			// We check the raw JSON, because we want to test the response, not
			// what it decodes into. Contents doesn't matter, we're testing the
			// presence only.
			assert.Contains(t, resp.Body.String(), `"pronouns":`)

			req = NewRequest(t, "GET", "/api/v1/users/user10")
			resp = MakeRequest(t, req, http.StatusOK)

			// Same deal here.
			assert.Contains(t, resp.Body.String(), `"pronouns":`)
		})

		t.Run("user/settings", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Set pronouns first for user2
			pronouns := "they/them"
			req := NewRequestWithJSON(t, "PATCH", "/api/v1/user/settings", &api.UserSettingsOptions{
				Pronouns: &pronouns,
			}).AddTokenAuth(firstUserToken)
			resp := MakeRequest(t, req, http.StatusOK)

			// Verify the response
			var user *api.UserSettings
			DecodeJSON(t, resp, &user)
			assert.Equal(t, pronouns, user.Pronouns)

			// Verify retrieving the settings again
			req = NewRequest(t, "GET", "/api/v1/user/settings").AddTokenAuth(firstUserToken)
			resp = MakeRequest(t, req, http.StatusOK)

			DecodeJSON(t, resp, &user)
			assert.Equal(t, pronouns, user.Pronouns)
		})

		t.Run("admin/users/{username}", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Set the pronouns for user2
			pronouns := "he/him"
			req := NewRequestWithJSON(t, "PATCH", "/api/v1/admin/users/user2", &api.EditUserOption{
				Pronouns: &pronouns,
			}).AddTokenAuth(adminToken)
			resp := MakeRequest(t, req, http.StatusOK)

			// Verify the API response
			var user2 *api.User
			DecodeJSON(t, resp, &user2)
			assert.Equal(t, pronouns, user2.Pronouns)

			// Verify via user2
			req = NewRequest(t, "GET", "/api/v1/user").AddTokenAuth(firstUserToken)
			resp = MakeRequest(t, req, http.StatusOK)
			DecodeJSON(t, resp, &user2)
			assert.Equal(t, pronouns, user2.Pronouns) // TODO: This fails for some reason

			// Set the pronouns for user10
			pronouns = "he/him"
			req = NewRequestWithJSON(t, "PATCH", "/api/v1/admin/users/user10", &api.EditUserOption{
				Pronouns: &pronouns,
			}).AddTokenAuth(adminToken)
			resp = MakeRequest(t, req, http.StatusOK)

			// Verify the API response
			var user10 *api.User
			DecodeJSON(t, resp, &user10)
			assert.Equal(t, pronouns, user10.Pronouns)

			// Verify via user10
			req = NewRequest(t, "GET", "/api/v1/user").AddTokenAuth(secondUserToken)
			resp = MakeRequest(t, req, http.StatusOK)
			DecodeJSON(t, resp, &user10)
			assert.Equal(t, pronouns, user10.Pronouns)
		})
	})

	t.Run("UI", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		// Set the pronouns to a known state via the API
		pronouns := "they/them"
		req := NewRequestWithJSON(t, "PATCH", "/api/v1/user/settings", &api.UserSettingsOptions{
			Pronouns: &pronouns,
		}).AddTokenAuth(firstUserToken)
		MakeRequest(t, req, http.StatusOK)

		t.Run("profile view", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", "/user2")
			resp := MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			userNameAndPronouns := strings.TrimSpace(htmlDoc.Find(".profile-avatar-name .username").Text())
			assert.NotContains(t, userNameAndPronouns, pronouns)
		})

		t.Run("settings", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", "/user/settings")
			resp := firstUserSession.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			// Check that the field is present
			pronounField, has := htmlDoc.Find(`input[name="pronouns"]`).Attr("value")
			assert.True(t, has)
			assert.Equal(t, pronouns, pronounField)

			// Check that updating the field works
			newPronouns := "she/her"
			req = NewRequestWithValues(t, "POST", "/user/settings", map[string]string{
				"pronouns": newPronouns,
			})
			firstUserSession.MakeRequest(t, req, http.StatusSeeOther)

			user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user2"})
			assert.Equal(t, newPronouns, user2.Pronouns)
		})

		t.Run("admin settings", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user2"})

			req := NewRequestf(t, "GET", "/admin/users/%d/edit", user2.ID)
			resp := adminSession.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			// Check that the pronouns field is present
			pronounField, has := htmlDoc.Find(`input[name="pronouns"]`).Attr("value")
			assert.True(t, has)
			assert.NotEmpty(t, pronounField)

			// Check that updating the field works
			newPronouns := "it/its"
			editURI := fmt.Sprintf("/admin/users/%d/edit", user2.ID)
			req = NewRequestWithValues(t, "POST", editURI, map[string]string{
				"login_type": "0-0",
				"login_name": user2.LoginName,
				"email":      user2.Email,
				"pronouns":   newPronouns,
			})
			adminSession.MakeRequest(t, req, http.StatusSeeOther)

			user2New := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user2"})
			assert.Equal(t, newPronouns, user2New.Pronouns)
		})
	})

	t.Run("unspecified", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		// Set the pronouns to Unspecified (an empty string) via the API
		pronouns := ""
		req := NewRequestWithJSON(t, "PATCH", "/api/v1/admin/users/user2", &api.EditUserOption{
			Pronouns: &pronouns,
		}).AddTokenAuth(adminToken)
		MakeRequest(t, req, http.StatusOK)

		// Verify that the profile page does not display any pronouns, nor the separator
		req = NewRequest(t, "GET", "/user2")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		userName := strings.TrimSpace(htmlDoc.Find(".profile-avatar-name .username").Text())
		assert.Equal(t, "user2", userName)
	})
}

func TestUserEditWebsite(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := forgery.CreateUser(t, nil)
	urlStr := "/api/v1/user/settings"
	session := loginUser(t, user.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteUser)

	t.Run("an HTTPS website under default schemes", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		// changing website should work
		website := "https://codeberg.org"
		req := NewRequestWithJSON(t, "PATCH", urlStr, &api.UserSettingsOptions{
			Website: &website,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusOK)

		var apiUser api.UserSettings
		DecodeJSON(t, resp, &apiUser)

		assert.Equal(t, website, apiUser.Website)
	})

	t.Run("an H3 website under default schemes", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		// changing website should not work
		website := "h3://codeberg.org"
		req := NewRequestWithJSON(t, "PATCH", urlStr, &api.UserSettingsOptions{
			Website: &website,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusUnprocessableEntity)

		var apiErr api.APIError
		DecodeJSON(t, resp, &apiErr)

		assert.Equal(t, "[Website]: Url", apiErr.Message)
	})

	t.Run("an H3 website under custom schemes", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		defer test.MockProtect(&setting.Service.ValidSiteURLSchemes)()
		setting.Service.ValidSiteURLSchemes = append(setting.Service.ValidSiteURLSchemes, "h3")

		// changing website should work
		website := "h3://codeberg.org"
		req := NewRequestWithJSON(t, "PATCH", urlStr, &api.UserSettingsOptions{
			Website: &website,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusOK)

		var apiUser api.UserSettings
		DecodeJSON(t, resp, &apiUser)

		assert.Equal(t, website, apiUser.Website)
	})
}

func TestUserTOTPMail(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	session := loginUser(t, user.Name)

	t.Run("No security keys", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		called := false
		defer test.MockVariableValue(&mailer.SendAsync, func(msgs ...*mailer.Message) {
			assert.Len(t, msgs, 1)
			assert.Equal(t, user.EmailTo(), msgs[0].To)
			assert.EqualValues(t, translation.NewLocale("en-US").Tr("mail.totp_disabled.subject"), msgs[0].Subject)
			assert.Contains(t, msgs[0].Body, translation.NewLocale("en-US").Tr("mail.totp_disabled.no_2fa"))
			called = true
		})()

		unittest.AssertSuccessfulInsert(t, &auth_model.TwoFactor{UID: user.ID})
		req := NewRequest(t, "POST", "/user/settings/security/two_factor/disable")
		session.MakeRequest(t, req, http.StatusSeeOther)

		assert.True(t, called)
		unittest.AssertExistsIf(t, false, &auth_model.TwoFactor{UID: user.ID})
	})

	t.Run("with security keys", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		called := false
		defer test.MockVariableValue(&mailer.SendAsync, func(msgs ...*mailer.Message) {
			assert.Len(t, msgs, 1)
			assert.Equal(t, user.EmailTo(), msgs[0].To)
			assert.EqualValues(t, translation.NewLocale("en-US").Tr("mail.totp_disabled.subject"), msgs[0].Subject)
			assert.NotContains(t, msgs[0].Body, translation.NewLocale("en-US").Tr("mail.totp_disabled.no_2fa"))
			called = true
		})()

		unittest.AssertSuccessfulInsert(t, &auth_model.TwoFactor{UID: user.ID})
		unittest.AssertSuccessfulInsert(t, &auth_model.WebAuthnCredential{UserID: user.ID})
		req := NewRequest(t, "POST", "/user/settings/security/two_factor/disable")
		session.MakeRequest(t, req, http.StatusSeeOther)

		assert.True(t, called)
		unittest.AssertExistsIf(t, false, &auth_model.TwoFactor{UID: user.ID})
	})
}

func TestUserSecurityKeyMail(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	session := loginUser(t, user.Name)

	t.Run("Normal", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		called := false
		defer test.MockVariableValue(&mailer.SendAsync, func(msgs ...*mailer.Message) {
			assert.Len(t, msgs, 1)
			assert.Equal(t, user.EmailTo(), msgs[0].To)
			assert.EqualValues(t, translation.NewLocale("en-US").Tr("mail.removed_security_key.subject"), msgs[0].Subject)
			assert.Contains(t, msgs[0].Body, translation.NewLocale("en-US").Tr("mail.removed_security_key.no_2fa"))
			assert.Contains(t, msgs[0].Body, "Little Bobby Tables&#39;s primary key")
			called = true
		})()

		unittest.AssertSuccessfulInsert(t, &auth_model.WebAuthnCredential{UserID: user.ID, Name: "Little Bobby Tables's primary key"})
		id := unittest.AssertExistsAndLoadBean(t, &auth_model.WebAuthnCredential{UserID: user.ID}).ID
		req := NewRequestWithValues(t, "POST", "/user/settings/security/webauthn/delete", map[string]string{
			"id": strconv.FormatInt(id, 10),
		})
		session.MakeRequest(t, req, http.StatusOK)

		assert.True(t, called)
		unittest.AssertExistsIf(t, false, &auth_model.WebAuthnCredential{UserID: user.ID})
	})

	t.Run("With TOTP", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		called := false
		defer test.MockVariableValue(&mailer.SendAsync, func(msgs ...*mailer.Message) {
			assert.Len(t, msgs, 1)
			assert.Equal(t, user.EmailTo(), msgs[0].To)
			assert.EqualValues(t, translation.NewLocale("en-US").Tr("mail.removed_security_key.subject"), msgs[0].Subject)
			assert.NotContains(t, msgs[0].Body, translation.NewLocale("en-US").Tr("mail.removed_security_key.no_2fa"))
			assert.Contains(t, msgs[0].Body, "Little Bobby Tables&#39;s primary key")
			called = true
		})()

		unittest.AssertSuccessfulInsert(t, &auth_model.WebAuthnCredential{UserID: user.ID, Name: "Little Bobby Tables's primary key"})
		id := unittest.AssertExistsAndLoadBean(t, &auth_model.WebAuthnCredential{UserID: user.ID}).ID
		unittest.AssertSuccessfulInsert(t, &auth_model.TwoFactor{UID: user.ID})
		req := NewRequestWithValues(t, "POST", "/user/settings/security/webauthn/delete", map[string]string{
			"id": strconv.FormatInt(id, 10),
		})
		session.MakeRequest(t, req, http.StatusOK)

		assert.True(t, called)
		unittest.AssertExistsIf(t, false, &auth_model.WebAuthnCredential{UserID: user.ID})
	})

	t.Run("Two security keys", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		called := false
		defer test.MockVariableValue(&mailer.SendAsync, func(msgs ...*mailer.Message) {
			assert.Len(t, msgs, 1)
			assert.Equal(t, user.EmailTo(), msgs[0].To)
			assert.EqualValues(t, translation.NewLocale("en-US").Tr("mail.removed_security_key.subject"), msgs[0].Subject)
			assert.NotContains(t, msgs[0].Body, translation.NewLocale("en-US").Tr("mail.removed_security_key.no_2fa"))
			assert.Contains(t, msgs[0].Body, "Little Bobby Tables&#39;s primary key")
			called = true
		})()

		unittest.AssertSuccessfulInsert(t, &auth_model.WebAuthnCredential{UserID: user.ID, Name: "Little Bobby Tables's primary key"})
		id := unittest.AssertExistsAndLoadBean(t, &auth_model.WebAuthnCredential{UserID: user.ID}).ID
		unittest.AssertSuccessfulInsert(t, &auth_model.WebAuthnCredential{UserID: user.ID, Name: "Little Bobby Tables's evil key"})
		req := NewRequestWithValues(t, "POST", "/user/settings/security/webauthn/delete", map[string]string{
			"id": strconv.FormatInt(id, 10),
		})
		session.MakeRequest(t, req, http.StatusOK)

		assert.True(t, called)
		unittest.AssertExistsIf(t, false, &auth_model.WebAuthnCredential{UserID: user.ID, Name: "Little Bobby Tables's primary key"})
		unittest.AssertExistsIf(t, true, &auth_model.WebAuthnCredential{UserID: user.ID, Name: "Little Bobby Tables's evil key"})
	})
}

func TestUserTOTPEnrolled(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	session := loginUser(t, user.Name)

	t.Run("No WebAuthn enabled", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		called := false
		defer test.MockVariableValue(&mailer.SendAsync, func(msgs ...*mailer.Message) {
			assert.Len(t, msgs, 1)
			assert.Equal(t, user.EmailTo(), msgs[0].To)
			assert.EqualValues(t, translation.NewLocale("en-US").Tr("mail.totp_enrolled.subject"), msgs[0].Subject)
			assert.Contains(t, msgs[0].Body, translation.NewLocale("en-US").Tr("mail.totp_enrolled.text_1.no_webauthn"))
			called = true
		})()

		session.EnrollTOTP(t)
		unittest.AssertSuccessfulDelete(t, &auth_model.TwoFactor{UID: user.ID})

		assert.True(t, called)
	})

	t.Run("With WebAuthn enabled", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		called := false
		defer test.MockVariableValue(&mailer.SendAsync, func(msgs ...*mailer.Message) {
			assert.Len(t, msgs, 1)
			assert.Equal(t, user.EmailTo(), msgs[0].To)
			assert.EqualValues(t, translation.NewLocale("en-US").Tr("mail.totp_enrolled.subject"), msgs[0].Subject)
			assert.Contains(t, msgs[0].Body, translation.NewLocale("en-US").Tr("mail.totp_enrolled.text_1.has_webauthn"))
			called = true
		})()

		unittest.AssertSuccessfulInsert(t, &auth_model.WebAuthnCredential{UserID: user.ID, Name: "Cueball's primary key"})
		session.EnrollTOTP(t)
		unittest.AssertSuccessfulDelete(t, &auth_model.TwoFactor{UID: user.ID})

		assert.True(t, called)
	})
}

func TestUserTOTPReenroll(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	session := loginUser(t, user.Name)

	resp := session.MakeRequest(t, NewRequest(t, "GET", "/user/settings/security/two_factor/reenroll"), http.StatusSeeOther)
	assert.Equal(t, "/user/settings/security", resp.Header().Get("Location"))

	session.EnrollTOTP(t)

	resp = session.MakeRequest(t, NewRequest(t, "GET", "/user/settings/security/two_factor/reenroll"), http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	totpSecretKey, has := htmlDoc.Find(".twofa img[src^='data:image/png;base64']").Attr("alt")
	assert.True(t, has)

	currentTOTP, err := totp.GenerateCode(totpSecretKey, time.Now())
	require.NoError(t, err)

	req := NewRequestWithValues(t, "POST", "/user/settings/security/two_factor/reenroll", map[string]string{
		"passcode": currentTOTP,
	})
	session.MakeRequest(t, req, http.StatusSeeOther)

	flashCookie := session.GetCookie(app_context.CookieNameFlash)
	assert.NotNil(t, flashCookie)
	assert.Contains(t, flashCookie.Value, "success%3DYour%2Baccount%2Bhas%2Bbeen%2Bsuccessfully%2Benrolled.")
}

func TestUserTOTPDisable(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	runTest := func(t *testing.T, user *user_model.User, useTOTP, disableAllowed bool, status int, flashMessage string) {
		t.Helper()
		defer unittest.AssertSuccessfulDelete(t, &auth_model.TwoFactor{UID: user.ID})

		session := loginUserMaybeTOTP(t, user, useTOTP)

		resp := session.MakeRequest(t, NewRequest(t, "GET", "user/settings/security"), http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)
		htmlDoc.AssertElement(t, "#disable-form", disableAllowed)

		req := NewRequest(t, "POST", "user/settings/security/two_factor/disable")
		if status == http.StatusSeeOther {
			resp := session.MakeRequest(t, req, http.StatusSeeOther)
			assert.Equal(t, "/user/settings/security", resp.Header().Get("Location"))
		} else {
			session.MakeRequest(t, req, status)
		}
		if flashMessage != "" {
			flashCookie := session.GetCookie(app_context.CookieNameFlash)
			assert.NotNil(t, flashCookie)
			if disableAllowed {
				assert.Contains(t, flashCookie.Value, fmt.Sprintf("success%%3D%s", flashMessage))
			} else {
				assert.Contains(t, flashCookie.Value, fmt.Sprintf("error%%3D%s", flashMessage))
			}
		}
	}

	adminUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	normalUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	restrictedUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 29})

	const twofaNotEnrolled = "Your%2Baccount%2Bis%2Bnot%2Bcurrently%2Benrolled%2Bin%2Btwo-factor%2Bauthentication."
	const twofaDisabled = "Two-factor%2Bauthentication%2Bhas%2Bbeen%2Bdisabled."

	t.Run("NoneTwoFactorRequirement", func(t *testing.T) {
		t.Run("no 2fa", func(t *testing.T) {
			runTest(t, adminUser, false, false, http.StatusSeeOther, twofaNotEnrolled)
			runTest(t, normalUser, false, false, http.StatusSeeOther, twofaNotEnrolled)
			runTest(t, restrictedUser, false, false, http.StatusSeeOther, twofaNotEnrolled)
		})

		t.Run("enabled 2fa", func(t *testing.T) {
			runTest(t, adminUser, true, true, http.StatusSeeOther, twofaDisabled)
			runTest(t, normalUser, true, true, http.StatusSeeOther, twofaDisabled)
			runTest(t, restrictedUser, true, true, http.StatusSeeOther, twofaDisabled)
		})
	})

	t.Run("AllTwoFactorRequirement", func(t *testing.T) {
		defer test.MockVariableValue(&setting.GlobalTwoFactorRequirement, setting.AllTwoFactorRequirement)()

		runTest(t, adminUser, true, false, http.StatusNotFound, "")
		runTest(t, normalUser, true, false, http.StatusNotFound, "")
		runTest(t, restrictedUser, true, false, http.StatusNotFound, "")
	})

	t.Run("AdminTwoFactorRequirement", func(t *testing.T) {
		defer test.MockVariableValue(&setting.GlobalTwoFactorRequirement, setting.AdminTwoFactorRequirement)()

		t.Run("no 2fa", func(t *testing.T) {
			runTest(t, normalUser, false, false, http.StatusSeeOther, twofaNotEnrolled)
			runTest(t, restrictedUser, false, false, http.StatusSeeOther, twofaNotEnrolled)
		})

		t.Run("enabled 2fa", func(t *testing.T) {
			runTest(t, adminUser, true, false, http.StatusNotFound, "")
			runTest(t, normalUser, true, true, http.StatusSeeOther, twofaDisabled)
			runTest(t, restrictedUser, true, true, http.StatusSeeOther, twofaDisabled)
		})
	})
}

func TestUserRepos(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	cases := map[string][]string{
		"alphabetically":        {"repo6", "repo7", "repo8"},
		"recentupdate":          {"repo7", "repo8", "repo6"},
		"reversealphabetically": {"repo8", "repo7", "repo6"},
	}

	session := loginUser(t, "user10")
	for sortBy, repos := range cases {
		req := NewRequest(t, "GET", "/user10?sort="+sortBy)
		resp := session.MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)

		sel := htmlDoc.doc.Find("a.name")
		assert.Len(t, repos, len(sel.Nodes))
		for i := range repos {
			assert.Equal(t, repos[i], strings.TrimSpace(sel.Eq(i).Text()))
		}
	}
}

func TestUserActivate(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&setting.Service.RegisterEmailConfirm, true)()

	called := false
	code := ""
	defer test.MockVariableValue(&mailer.SendAsync, func(msgs ...*mailer.Message) {
		called = true
		assert.Len(t, msgs, 1)
		assert.Equal(t, `"doesnotexist" <doesnotexist@example.com>`, msgs[0].To)
		assert.EqualValues(t, translation.NewLocale("en-US").Tr("mail.activate_account"), msgs[0].Subject)

		messageDoc := NewHTMLParser(t, bytes.NewBuffer([]byte(msgs[0].Body)))
		link, ok := messageDoc.Find("a").Attr("href")
		assert.True(t, ok)
		u, err := url.Parse(link)
		require.NoError(t, err)
		code = u.Query()["code"][0]
	})()

	session := emptyTestSession(t)
	req := NewRequestWithValues(t, "POST", "/user/sign_up", map[string]string{
		"user_name": "doesnotexist",
		"email":     "doesnotexist@example.com",
		"password":  "examplePassword!1",
		"retype":    "examplePassword!1",
	})
	session.MakeRequest(t, req, http.StatusOK)
	assert.True(t, called)

	queryCode, err := url.QueryUnescape(code)
	require.NoError(t, err)

	lookupKey, validator, ok := strings.Cut(queryCode, ":")
	assert.True(t, ok)

	rawValidator, err := hex.DecodeString(validator)
	require.NoError(t, err)

	authToken, err := auth_model.FindAuthToken(db.DefaultContext, lookupKey, auth_model.UserActivation)
	require.NoError(t, err)
	assert.False(t, authToken.IsExpired())
	assert.Equal(t, authToken.HashedValidator, auth_model.HashValidator(rawValidator))

	t.Run("No password", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req = NewRequest(t, "POST", "/user/activate?code="+code)
		session.MakeRequest(t, req, http.StatusOK)

		unittest.AssertExistsIf(t, true, &auth_model.AuthorizationToken{ID: authToken.ID})
		unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "doesnotexist"}, "is_active = false")
	})

	t.Run("With password", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req = NewRequestWithValues(t, "POST", "/user/activate?code="+code, map[string]string{
			"password": "examplePassword!1",
		})
		session.MakeRequest(t, req, http.StatusSeeOther)

		unittest.AssertExistsIf(t, false, &auth_model.AuthorizationToken{ID: authToken.ID})
		unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "doesnotexist"}, "is_active = true")
	})
}

func parseMailHelper(t *testing.T, expectedTo, expectedSubject string) (cleanup func(), codeRes *string, calledRes *bool) {
	t.Helper()

	called := false
	code := ""

	cleanup = test.MockVariableValue(&mailer.SendAsync, func(msgs ...*mailer.Message) {
		if called {
			return
		}
		called = true

		assert.Len(t, msgs, 1)
		assert.Equal(t, expectedTo, msgs[0].To)
		assert.Equal(t, expectedSubject, msgs[0].Subject)

		messageDoc := NewHTMLParser(t, bytes.NewBuffer([]byte(msgs[0].Body)))
		link, ok := messageDoc.Find("a").Attr("href")
		assert.True(t, ok)
		u, err := url.Parse(link)
		require.NoError(t, err)
		code = u.Query()["code"][0]
	})

	return cleanup, &code, &called
}

func TestUserPasswordReset(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	cleanup, code, called := parseMailHelper(t, user2.EmailTo(), string(translation.NewLocale("en-US").Tr("mail.reset_password")))
	defer cleanup()

	session := emptyTestSession(t)
	req := NewRequestWithValues(t, "POST", "/user/forgot_password", map[string]string{
		"email": user2.Email,
	})
	session.MakeRequest(t, req, http.StatusOK)
	assert.True(t, *called)

	queryCode, err := url.QueryUnescape(*code)
	require.NoError(t, err)

	lookupKey, validator, ok := strings.Cut(queryCode, ":")
	assert.True(t, ok)

	rawValidator, err := hex.DecodeString(validator)
	require.NoError(t, err)

	authToken, err := auth_model.FindAuthToken(db.DefaultContext, lookupKey, auth_model.PasswordReset)
	require.NoError(t, err)
	assert.False(t, authToken.IsExpired())
	assert.Equal(t, authToken.HashedValidator, auth_model.HashValidator(rawValidator))

	req = NewRequestWithValues(t, "POST", "/user/recover_account", map[string]string{
		"code":     *code,
		"password": "new_password",
	})
	session.MakeRequest(t, req, http.StatusSeeOther)

	unittest.AssertNotExistsBean(t, &auth_model.AuthorizationToken{ID: authToken.ID})
	assert.True(t, unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2}).ValidatePassword(t.Context(), "new_password"))
}

func TestUserPasswordResetOAuth2(t *testing.T) {
	defer unittest.OverrideFixtures("tests/integration/fixtures/TestUserPasswordResetOAuth2")()
	defer tests.PrepareTestEnv(t)()

	t.Run("OAuth2 user without password", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1001})
		assert.True(t, user.IsOAuth2())
		assert.False(t, user.IsPasswordSet())
		assert.False(t, user.IsLocal())

		session := emptyTestSession(t)
		req := NewRequestWithValues(t, "POST", "/user/forgot_password", map[string]string{
			"email": user.Email,
		})
		resp := session.MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)
		assert.Contains(t,
			htmlDoc.doc.Find(".ui.negative.message").Text(),
			translation.NewLocale("en-US").TrString("auth.non_local_account"),
		)
	})

	t.Run("OAuth2 user with password", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1000})
		assert.True(t, user.IsOAuth2())
		assert.True(t, user.IsPasswordSet())
		assert.False(t, user.IsLocal())

		cleanup, code, called := parseMailHelper(t, user.EmailTo(), string(translation.NewLocale("en-US").Tr("mail.reset_password")))
		defer cleanup()

		session := emptyTestSession(t)
		req := NewRequestWithValues(t, "POST", "/user/forgot_password", map[string]string{
			"email": user.Email,
		})
		session.MakeRequest(t, req, http.StatusOK)
		assert.True(t, *called)

		user.Passwd = ""
		err := user_model.UpdateUserCols(db.DefaultContext, user, "passwd")
		require.NoError(t, err)

		req = NewRequestWithValues(t, "POST", "/user/recover_account", map[string]string{
			"code":     *code,
			"password": "new_password",
		})
		resp := session.MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)
		assert.Contains(t,
			htmlDoc.doc.Find(".ui.negative.message").Text(),
			translation.NewLocale("en-US").TrString("auth.non_local_account"),
		)
	})
}

func TestMakeEmailAddressPrimary(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	session := loginUser(t, user2.Name)

	t.Run("Not activated", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		email := unittest.AssertExistsAndLoadBean(t, &user_model.EmailAddress{ID: 35, UID: user2.ID}, "is_activated = false")
		req := NewRequestWithValues(t, "POST", "/user/settings/account/email", map[string]string{
			"_method": "PRIMARY",
			"id":      strconv.FormatInt(email.ID, 10),
		})
		session.MakeRequest(t, req, http.StatusSeeOther)

		unittest.AssertExistsIf(t, true, &user_model.User{ID: user2.ID, Email: user2.Email})
		unittest.AssertExistsIf(t, true, &user_model.EmailAddress{ID: email.ID}, "is_primary = false")
	})

	t.Run("Nonexistent", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequestWithValues(t, "POST", "/user/settings/account/email", map[string]string{
			"_method": "PRIMARY",
			"id":      strconv.FormatInt(unittest.NonexistentID, 10),
		})
		session.MakeRequest(t, req, http.StatusSeeOther)

		unittest.AssertExistsIf(t, true, &user_model.User{ID: user2.ID, Email: user2.Email})
	})
}

func TestActivateEmailAddress(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&setting.Service.RegisterEmailConfirm, true)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	cleanup, code, called := parseMailHelper(t, "newemail@example.org", string(translation.NewLocale("en-US").Tr("mail.activate_email")))
	defer cleanup()

	session := loginUser(t, user2.Name)
	req := NewRequestWithValues(t, "POST", "/user/settings/account/email", map[string]string{
		"email": "newemail@example.org",
	})
	session.MakeRequest(t, req, http.StatusSeeOther)
	assert.True(t, *called)

	queryCode, err := url.QueryUnescape(*code)
	require.NoError(t, err)

	lookupKey, validator, ok := strings.Cut(queryCode, ":")
	assert.True(t, ok)

	rawValidator, err := hex.DecodeString(validator)
	require.NoError(t, err)

	authToken, err := auth_model.FindAuthToken(db.DefaultContext, lookupKey, auth_model.EmailActivation("newemail@example.org"))
	require.NoError(t, err)
	assert.False(t, authToken.IsExpired())
	assert.Equal(t, authToken.HashedValidator, auth_model.HashValidator(rawValidator))

	req = NewRequestWithValues(t, "POST", "/user/activate_email", map[string]string{
		"code":  *code,
		"email": "newemail@example.org",
	})
	session.MakeRequest(t, req, http.StatusSeeOther)

	unittest.AssertNotExistsBean(t, &auth_model.AuthorizationToken{ID: authToken.ID})
	unittest.AssertExistsAndLoadBean(t, &user_model.EmailAddress{UID: user2.ID, IsActivated: true, Email: "newemail@example.org"})
}

func TestExportUserSSHKeys(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	t.Run("No exported keys", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		resp := MakeRequest(t, NewRequest(t, "GET", "/user1.keys"), http.StatusOK)

		assert.Equal(t, "# Note: This user hasn't uploaded any SSH keys.\n", resp.Body.String())
	})

	t.Run("Exported key", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		resp := MakeRequest(t, NewRequest(t, "GET", "/user2.keys"), http.StatusOK)

		assert.Equal(t, "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQDWVj0fQ5N8wNc0LVNA41wDLYJ89ZIbejrPfg/avyj3u/ZohAKsQclxG4Ju0VirduBFF9EOiuxoiFBRr3xRpqzpsZtnMPkWVWb+akZwBFAx8p+jKdy4QXR/SZqbVobrGwip2UjSrri1CtBxpJikojRIZfCnDaMOyd9Jp6KkujvniFzUWdLmCPxUE9zhTaPu0JsEP7MW0m6yx7ZUhHyfss+NtqmFTaDO+QlMR7L2QkDliN2Jl3Xa3PhuWnKJfWhdAq1Cw4oraKUOmIgXLkuiuxVQ6mD3AiFupkmfqdHq6h+uHHmyQqv3gU+/sD8GbGAhf6ftqhTsXjnv1Aj4R8NoDf9BS6KRkzkeun5UisSzgtfQzjOMEiJtmrep2ZQrMGahrXa+q4VKr0aKJfm+KlLfwm/JztfsBcqQWNcTURiCFqz+fgZw0Ey/de0eyMzldYTdXXNRYCKjs9bvBK+6SSXRM7AhftfQ0ZuoW5+gtinPrnmoOaSCEJbAiEiTO/BzOHgowiM=\n", resp.Body.String())
	})

	t.Run("No exported keys and SSH principal", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		resp := MakeRequest(t, NewRequest(t, "GET", "/user5.keys"), http.StatusOK)

		assert.Equal(t, "# Note: This user hasn't uploaded any SSH keys.\n", resp.Body.String())
	})

	t.Run("Exported key and SSH principal", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		resp := MakeRequest(t, NewRequest(t, "GET", "/user9.keys"), http.StatusOK)

		assert.Equal(t, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDN7KuFUnlztx/UM6PUTyiBAq5SeIqr+qSVFC6JzLQAh\n", resp.Body.String())
	})
}

func TestAuthorizedIntegrationList(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	locale := translation.NewLocale("en-US")
	topDescription := locale.TrString("settings.authorized_integration.desc")
	noAI := locale.TrString("settings.authorized_integration.none")

	session := loginUser(t, "user2")

	// Load page with no authorized integrations:
	req := NewRequest(t, "GET", "/user/settings/authorized-integrations")
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)
	htmlDoc.AssertSelection(t, htmlDoc.FindByTextTrim("div.flex-item", topDescription), true)
	htmlDoc.AssertSelection(t, htmlDoc.FindByTextTrim("div.flex-item-body p", noAI), true)

	ait := newAITester(t)
	defer ait.close()

	// Load page which should now have a generic authorized integration:
	req = NewRequest(t, "GET", "/user/settings/authorized-integrations")
	resp = session.MakeRequest(t, req, http.StatusOK)
	htmlDoc = NewHTMLParser(t, resp.Body)
	htmlDoc.AssertSelection(t, htmlDoc.FindByTextTrim("div.flex-item", topDescription), true)
	htmlDoc.AssertSelection(t, htmlDoc.FindByTextTrim("div.flex-item-body p", noAI), false) // "no ... configured" no longer present
	htmlDoc.AssertSelection(t, htmlDoc.FindByTextTrim("div.flex-item span.flex-item-title", "AI TestAuthorizedIntegrationList"), true)
}

func TestAuthorizedIntegrationView(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	locale := translation.NewLocale("en-US")
	topDescription := locale.TrString("settings.authorized_integration.desc")
	noAI := locale.TrString("settings.authorized_integration.none")

	session := loginUser(t, "user2")

	// Create an Authorized Integration
	ait := newAITester(t)
	defer ait.close()

	// Load page which should now have a generic authorized integration:
	req := NewRequest(t, "GET", "/user/settings/authorized-integrations")
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)
	htmlDoc.AssertSelection(t, htmlDoc.FindByTextTrim("div.flex-item", topDescription), true)
	htmlDoc.AssertSelection(t, htmlDoc.FindByTextTrim("div.flex-item-body p", noAI), false) // "no ... configured" no longer present
	htmlDoc.AssertSelection(t, htmlDoc.FindByTextTrim("div.flex-item span.flex-item-title", "AI TestAuthorizedIntegrationView"), true)

	// Find button to view/edit and verify it's URL
	viewButtonSelector := htmlDoc.Find("div.flex-item-trailing a.primary.button")
	require.Equal(t, 1, viewButtonSelector.Length())
	viewButton := viewButtonSelector.Get(0)
	hrefIndex := slices.IndexFunc(viewButton.Attr, func(attr html.Attribute) bool {
		return attr.Key == "href"
	})
	require.NotEqual(t, -1, hrefIndex)
	href := viewButton.Attr[hrefIndex].Val
	assert.Equal(t, fmt.Sprintf("/user/settings/authorized-integrations/generic/%d", ait.authorizedIntegration.ID), href)

	// Load the view/edit page
	req = NewRequest(t, "GET", href)
	resp = session.MakeRequest(t, req, http.StatusOK)
	htmlDoc = NewHTMLParser(t, resp.Body)

	// Assert contents of all the fields
	htmlDoc.AssertAttrEqual(t, "#name", "value", "AI TestAuthorizedIntegrationView")
	assert.Equal(t, "An Authorized Integration created for the test case TestAuthorizedIntegrationView.\nIt's pretty neat.", htmlDoc.doc.Find("textarea[name='description']").Text())
	htmlDoc.AssertAttrEqual(t, "#audience", "value", ait.authorizedIntegration.Audience)
	htmlDoc.AssertAttrEqual(t, "#issuer", "value", ait.authorizedIntegration.Issuer)
	assert.Equal(t, "{\n  \"rules\": [\n    {\n      \"claim\": \"custom-claim\",\n      \"compare\": \"eq\",\n      \"value\": \"custom-claim-value\"\n    }\n  ]\n}", htmlDoc.doc.Find("textarea[id='claim_rules']").Text()) //nolint:testifylint // this isn't a JSON comparison; the formatting here should be exact as it represents the auto-indentation generated by the server
	htmlDoc.AssertElementChecked(t, "#resource-all")
	htmlDoc.AssertElementSelected(t, "#scope-activitypub option[value='write:activitypub']")
	assert.Equal(t, 0, htmlDoc.Find("#scope-admin").Length()) // not an admin user
	htmlDoc.AssertElementSelected(t, "#scope-issue option[value='write:issue']")
	htmlDoc.AssertElementSelected(t, "#scope-misc option[value='write:misc']")
	htmlDoc.AssertElementSelected(t, "#scope-notification option[value='write:notification']")
	htmlDoc.AssertElementSelected(t, "#scope-organization option[value='write:organization']")
	htmlDoc.AssertElementSelected(t, "#scope-package option[value='write:package']")
	htmlDoc.AssertElementSelected(t, "#scope-repository option[value='write:repository']")
	htmlDoc.AssertElementSelected(t, "#scope-user option[value='write:user']")
}
