// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	auth_model "forgejo.org/models/auth"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/queue"
	"forgejo.org/tests"
)

func TestNotification(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	session := loginUser(t, user2.Name)

	req := NewRequest(t, "GET", "/notifications")
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	// Unread and pinned notification.
	htmlDoc.AssertElement(t, ".notifications-link[href='/user2/repo1/pulls/3']", true)
	htmlDoc.AssertElement(t, ".notifications-link[href='/user2/repo1/issues/4']", true)
	htmlDoc.AssertElement(t, ".notifications-link[href='/user2/repo2/issues/1']", true)

	// Read notification.
	htmlDoc.AssertElement(t, ".notifications-link[href='/user2/repo2/pulls/2']", false)
}

func TestReleaseNotification(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	user5 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	sessionUser2 := loginUser(t, user2.Name)
	sessionUser5 := loginUser(t, user5.Name)

	tokenUser2 := getTokenForLoggedInUser(t, sessionUser2, auth_model.AccessTokenScopeWriteRepository)
	tokenUser5 := getTokenForLoggedInUser(t, sessionUser5, auth_model.AccessTokenScopeReadNotification, auth_model.AccessTokenScopeWriteRepository)

	// subscribe user5 to the releases on the repo
	req := NewRequest(t, "PUT", fmt.Sprintf("/api/v1/repos/%s/%s/subscription", repo.OwnerName, repo.Name)).
		AddTokenAuth(tokenUser5)
	MakeRequest(t, req, http.StatusOK)

	createNewReleaseUsingAPI(t, tokenUser2, user2, repo, "releaseName", "", "releaseTitle", "")

	queue.GetManager().FlushAll(t.Context(), 1*time.Second)

	// check the in-app notifications of user5
	req = NewRequest(t, "GET", "/notifications")
	resp := sessionUser5.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	// there is a notification about the release
	htmlDoc.AssertElement(t, ".notifications-link[href='/user2/repo1/releases/tag/releaseName']", true)

	// visit the release page as the notified user
	req = NewRequest(t, "GET", "/user2/repo1/releases/tag/releaseName")
	sessionUser5.MakeRequest(t, req, http.StatusOK)

	// check the in-app notifications of user5 again
	req = NewRequest(t, "GET", "/notifications")
	resp = sessionUser5.MakeRequest(t, req, http.StatusOK)
	htmlDoc = NewHTMLParser(t, resp.Body)

	// there is no longer a notification about the release
	htmlDoc.AssertElement(t, ".notifications-link[href='/user2/repo1/releases/tag/releaseName']", false)
}
