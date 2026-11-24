// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"fmt"
	"net/http"
	"testing"

	repo_model "forgejo.org/models/repo"
	secret_model "forgejo.org/models/secret"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	app_context "forgejo.org/services/context"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActionsSecretsCreateSecret(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1, OwnerID: user2.ID})
	org3 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3, Type: user_model.UserTypeOrganization})

	sess := loginUser(t, user2.Name)

	testCases := []struct {
		name    string
		url     string
		ownerID int64
		repoID  int64
	}{
		{
			name:    "User",
			url:     "/user/settings/actions/secrets",
			ownerID: user2.ID,
			repoID:  0,
		},
		{
			name:    "Repository",
			url:     "/" + repo1.FullName() + "/settings/actions/secrets",
			ownerID: 0,
			repoID:  repo1.ID,
		},
		{
			name:    "Organization",
			url:     "/org/" + org3.Name + "/settings/actions/secrets",
			ownerID: org3.ID,
			repoID:  0,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req := NewRequestWithValues(t, "POST", testCase.url, map[string]string{
				"name": "my_secret",
				"data": "   \r\n\tSecrët dåtä\\   \r\n",
			})
			sess.MakeRequest(t, req, http.StatusOK)

			flashCookie := sess.GetCookie(app_context.CookieNameFlash)
			assert.NotNil(t, flashCookie)
			assert.Equal(t, "success%3DThe%2Bsecret%2B%2522MY_SECRET%2522%2Bhas%2Bbeen%2Badded.", flashCookie.Value)

			secret := unittest.AssertExistsAndLoadBean(t, &secret_model.Secret{OwnerID: testCase.ownerID, RepoID: testCase.repoID, Name: "MY_SECRET"})
			assert.Equal(t, "MY_SECRET", secret.Name)

			value, err := secret.GetDecryptedData()
			require.NoError(t, err)
			assert.Equal(t, "   \n\tSecrët dåtä\\   \n", value)
		})
	}
}

func TestActionsSecretsCreateSecretRejectsNameMatchingExistingSecret(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1, OwnerID: user2.ID})
	org3 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3, Type: user_model.UserTypeOrganization})

	sess := loginUser(t, user2.Name)

	testCases := []struct {
		name    string
		url     string
		ownerID int64
		repoID  int64
	}{
		{
			name:    "User",
			url:     "/user/settings/actions/secrets",
			ownerID: user2.ID,
			repoID:  0,
		},
		{
			name:    "Repository",
			url:     "/" + repo1.FullName() + "/settings/actions/secrets",
			ownerID: 0,
			repoID:  repo1.ID,
		},
		{
			name:    "Organization",
			url:     "/org/" + org3.Name + "/settings/actions/secrets",
			ownerID: org3.ID,
			repoID:  0,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req := NewRequestWithValues(t, "POST", testCase.url, map[string]string{
				"name": "my_secret",
				"data": "original value",
			})
			sess.MakeRequest(t, req, http.StatusOK)

			flashCookie := sess.GetCookie(app_context.CookieNameFlash)
			assert.NotNil(t, flashCookie)
			assert.Equal(t, "success%3DThe%2Bsecret%2B%2522MY_SECRET%2522%2Bhas%2Bbeen%2Badded.", flashCookie.Value)

			secret := unittest.AssertExistsAndLoadBean(t, &secret_model.Secret{OwnerID: testCase.ownerID, RepoID: testCase.repoID, Name: "MY_SECRET"})
			assert.Equal(t, "MY_SECRET", secret.Name)

			value, err := secret.GetDecryptedData()
			require.NoError(t, err)
			assert.Equal(t, "original value", value)

			// Try to create a new secret with the name but another value.
			req = NewRequestWithValues(t, "POST", testCase.url, map[string]string{
				"name": "my_secret",
				"data": "changed value",
			})
			sess.MakeRequest(t, req, http.StatusBadRequest)

			// Verify that the original secret has not been changed.
			secret = unittest.AssertExistsAndLoadBean(t, &secret_model.Secret{OwnerID: testCase.ownerID, RepoID: testCase.repoID, Name: "MY_SECRET"})
			value, err = secret.GetDecryptedData()
			require.NoError(t, err)

			assert.Equal(t, "MY_SECRET", secret.Name)
			assert.Equal(t, "original value", value)
		})
	}
}

func TestActionsSecretsEditSecret(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1, OwnerID: user2.ID})
	org3 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3, Type: user_model.UserTypeOrganization})

	sess := loginUser(t, user2.Name)

	testCases := []struct {
		name    string
		url     string
		ownerID int64
		repoID  int64
	}{
		{
			name:    "User",
			url:     "/user/settings/actions/secrets",
			ownerID: user2.ID,
			repoID:  0,
		},
		{
			name:    "Repository",
			url:     "/" + repo1.FullName() + "/settings/actions/secrets",
			ownerID: 0,
			repoID:  repo1.ID,
		},
		{
			name:    "Organization",
			url:     "/org/" + org3.Name + "/settings/actions/secrets",
			ownerID: org3.ID,
			repoID:  0,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req := NewRequestWithValues(t, "POST", testCase.url, map[string]string{
				"name": "TEST_SECRET",
				"data": "value",
			})
			sess.MakeRequest(t, req, http.StatusOK)

			flashCookie := sess.GetCookie(app_context.CookieNameFlash)
			assert.NotNil(t, flashCookie)
			assert.Equal(t, "success%3DThe%2Bsecret%2B%2522TEST_SECRET%2522%2Bhas%2Bbeen%2Badded.", flashCookie.Value)

			secret := unittest.AssertExistsAndLoadBean(t, &secret_model.Secret{OwnerID: testCase.ownerID, RepoID: testCase.repoID, Name: "TEST_SECRET"})

			req = NewRequestWithValues(t, "POST", fmt.Sprintf("%s/%d/edit", testCase.url, secret.ID), map[string]string{
				"name": secret.Name,
				"data": "   \r\n\tSecrët dåtä\\   \r\n",
			})
			sess.MakeRequest(t, req, http.StatusOK)

			flashCookie = sess.GetCookie(app_context.CookieNameFlash)
			assert.NotNil(t, flashCookie)
			assert.Equal(t, "success%3DThe%2Bsecret%2B%2522TEST_SECRET%2522%2Bhas%2Bbeen%2Bupdated.", flashCookie.Value)

			updatedSecret := unittest.AssertExistsAndLoadBean(t, &secret_model.Secret{ID: secret.ID})
			decryptedValue, err := updatedSecret.GetDecryptedData()
			require.NoError(t, err)

			assert.Equal(t, secret.Name, updatedSecret.Name)
			assert.Equal(t, "   \n\tSecrët dåtä\\   \n", decryptedValue)
		})
	}
}

func TestActionsSecretsEditSecretWithoutChangingItsValue(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1, OwnerID: user2.ID})
	org3 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3, Type: user_model.UserTypeOrganization})

	sess := loginUser(t, user2.Name)

	testCases := []struct {
		name    string
		url     string
		ownerID int64
		repoID  int64
	}{
		{
			name:    "User",
			url:     "/user/settings/actions/secrets",
			ownerID: user2.ID,
			repoID:  0,
		},
		{
			name:    "Repository",
			url:     "/" + repo1.FullName() + "/settings/actions/secrets",
			ownerID: 0,
			repoID:  repo1.ID,
		},
		{
			name:    "Organization",
			url:     "/org/" + org3.Name + "/settings/actions/secrets",
			ownerID: org3.ID,
			repoID:  0,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req := NewRequestWithValues(t, "POST", testCase.url, map[string]string{
				"name": "TEST_SECRET",
				"data": "   \r\n\tSecrët dåtä\\   \r\n",
			})
			sess.MakeRequest(t, req, http.StatusOK)

			flashCookie := sess.GetCookie(app_context.CookieNameFlash)
			assert.NotNil(t, flashCookie)
			assert.Equal(t, "success%3DThe%2Bsecret%2B%2522TEST_SECRET%2522%2Bhas%2Bbeen%2Badded.", flashCookie.Value)

			secret := unittest.AssertExistsAndLoadBean(t, &secret_model.Secret{OwnerID: testCase.ownerID, RepoID: testCase.repoID, Name: "TEST_SECRET"})

			req = NewRequestWithValues(t, "POST", fmt.Sprintf("%s/%d/edit", testCase.url, secret.ID), map[string]string{
				"name": "changed_secret",
				"data": "",
			})
			sess.MakeRequest(t, req, http.StatusOK)

			flashCookie = sess.GetCookie(app_context.CookieNameFlash)
			assert.NotNil(t, flashCookie)
			assert.Equal(t, "success%3DThe%2Bsecret%2B%2522CHANGED_SECRET%2522%2Bhas%2Bbeen%2Bupdated.", flashCookie.Value)

			updatedSecret := unittest.AssertExistsAndLoadBean(t, &secret_model.Secret{ID: secret.ID})
			decryptedValue, err := updatedSecret.GetDecryptedData()
			require.NoError(t, err)

			assert.Equal(t, "CHANGED_SECRET", updatedSecret.Name)
			assert.Equal(t, "   \n\tSecrët dåtä\\   \n", decryptedValue)
		})
	}
}

func TestActionsSecretsEditSecretRejectsInvalidName(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1, OwnerID: user2.ID})
	org3 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3, Type: user_model.UserTypeOrganization})

	sess := loginUser(t, user2.Name)

	testCases := []struct {
		name    string
		url     string
		ownerID int64
		repoID  int64
	}{
		{
			name:    "User",
			url:     "/user/settings/actions/secrets",
			ownerID: user2.ID,
			repoID:  0,
		},
		{
			name:    "Repository",
			url:     "/" + repo1.FullName() + "/settings/actions/secrets",
			ownerID: 0,
			repoID:  repo1.ID,
		},
		{
			name:    "Organization",
			url:     "/org/" + org3.Name + "/settings/actions/secrets",
			ownerID: org3.ID,
			repoID:  0,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req := NewRequestWithValues(t, "POST", testCase.url, map[string]string{
				"name": "TEST_SECRET",
				"data": "value",
			})
			sess.MakeRequest(t, req, http.StatusOK)

			flashCookie := sess.GetCookie(app_context.CookieNameFlash)
			assert.NotNil(t, flashCookie)
			assert.Equal(t, "success%3DThe%2Bsecret%2B%2522TEST_SECRET%2522%2Bhas%2Bbeen%2Badded.", flashCookie.Value)

			secret := unittest.AssertExistsAndLoadBean(t, &secret_model.Secret{OwnerID: testCase.ownerID, RepoID: testCase.repoID, Name: "TEST_SECRET"})

			req = NewRequestWithValues(t, "POST", fmt.Sprintf("%s/%d/edit", testCase.url, secret.ID), map[string]string{
				"name": "FORGEJO_IS_INVALID",
				"data": "",
			})
			sess.MakeRequest(t, req, http.StatusBadRequest)
		})
	}
}

func TestActionsSecretsRemoveSecret(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1, OwnerID: user2.ID})
	org3 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3, Type: user_model.UserTypeOrganization})

	sess := loginUser(t, user2.Name)

	testCases := []struct {
		name    string
		url     string
		ownerID int64
		repoID  int64
	}{
		{
			name:    "User",
			url:     "/user/settings/actions/secrets",
			ownerID: user2.ID,
			repoID:  0,
		},
		{
			name:    "Repository",
			url:     "/" + repo1.FullName() + "/settings/actions/secrets",
			ownerID: 0,
			repoID:  repo1.ID,
		},
		{
			name:    "Organization",
			url:     "/org/" + org3.Name + "/settings/actions/secrets",
			ownerID: org3.ID,
			repoID:  0,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req := NewRequestWithValues(t, "POST", testCase.url, map[string]string{
				"name": "TEST_SECRET",
				"data": "value",
			})
			sess.MakeRequest(t, req, http.StatusOK)

			flashCookie := sess.GetCookie(app_context.CookieNameFlash)
			assert.NotNil(t, flashCookie)
			assert.Equal(t, "success%3DThe%2Bsecret%2B%2522TEST_SECRET%2522%2Bhas%2Bbeen%2Badded.", flashCookie.Value)

			secret := unittest.AssertExistsAndLoadBean(t, &secret_model.Secret{OwnerID: testCase.ownerID, RepoID: testCase.repoID, Name: "TEST_SECRET"})

			req = NewRequest(t, "POST", fmt.Sprintf("%s/%d/delete", testCase.url, secret.ID))
			sess.MakeRequest(t, req, http.StatusOK)

			flashCookie = sess.GetCookie(app_context.CookieNameFlash)
			assert.NotNil(t, flashCookie)
			assert.Equal(t, "success%3DThe%2Bsecret%2Bhas%2Bbeen%2Bremoved.", flashCookie.Value)

			unittest.AssertNotExistsBean(t, secret)
		})
	}
}

func TestActionsSecretsRepoListsInehritedSecrets(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1, OwnerID: user2.ID})

	_, err := secret_model.InsertEncryptedSecret(t.Context(), user2.ID, 0, "OWNER_ONLY", "x")
	require.NoError(t, err)
	_, err = secret_model.InsertEncryptedSecret(t.Context(), user2.ID, 0, "SHARED", "x")
	require.NoError(t, err)
	_, err = secret_model.InsertEncryptedSecret(t.Context(), 0, repo1.ID, "SHARED", "y")
	require.NoError(t, err)

	sess := loginUser(t, user2.Name)
	resp := sess.MakeRequest(t, NewRequest(t, "GET", "/"+repo1.FullName()+"/settings/actions/secrets"), http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	lists := htmlDoc.Find(".flex-list")
	require.Equal(t, 2, lists.Length())
	inehrited := lists.Eq(1).Find(".flex-item-title")
	require.Equal(t, 2, inehrited.Length())
	assert.Contains(t, inehrited.Eq(0).Text(), "OWNER_ONLY")
	assert.Contains(t, inehrited.Eq(1).Text(), "SHARED")
	htmlDoc.AssertElement(t, `h4 a[href="/user/settings/actions/secrets"]`, true)

	resp = loginUser(t, "user1").MakeRequest(t, NewRequest(t, "GET", "/"+repo1.FullName()+"/settings/actions/secrets"), http.StatusOK)
	htmlDoc = NewHTMLParser(t, resp.Body)
	assert.Equal(t, 2, htmlDoc.Find(".flex-list").Length())
	htmlDoc.AssertElement(t, `h4 a[href="/user/settings/actions/secrets"]`, false)
}
