// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	unit_model "forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	repo_service "forgejo.org/services/repository"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIUserReposNotLogin(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	req := NewRequestf(t, "GET", "/api/v1/users/%s/repos", user.Name)
	resp := MakeRequest(t, req, http.StatusOK)

	var apiRepos []api.Repository
	DecodeJSON(t, resp, &apiRepos)
	expectedLen := unittest.GetCount(t, repo_model.Repository{OwnerID: user.ID},
		unittest.Cond("is_private = ?", false))
	assert.Len(t, apiRepos, expectedLen)
	for _, repo := range apiRepos {
		assert.Equal(t, user.ID, repo.Owner.ID)
		assert.False(t, repo.Private)
	}
}

func TestAPIUserReposWithWrongToken(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	wrongToken := fmt.Sprintf("Bearer %s", "wrong_token")
	req := NewRequestf(t, "GET", "/api/v1/users/%s/repos", user.Name).
		AddTokenAuth(wrongToken)
	resp := MakeRequest(t, req, http.StatusUnauthorized)

	assert.Contains(t, resp.Body.String(), "access token does not exist")
	assert.NotContains(t, resp.Body.String(), "wrong_token")
}

func TestAPIUserReposAccessTokenResources(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	var repos []api.Repository

	// Test cases repo1 (public), repo2 (private), repo16 (private).
	session := loginUser(t, "user2")

	find := func() (bool, bool, bool) {
		foundRepo1 := false  // public user2/repo1
		foundRepo2 := false  // private user2/repo2
		foundRepo16 := false // second private repo user2/repo16 used in fine-grain testing, included as baseline
		for _, repo := range repos {
			switch repo.Name {
			case "repo1":
				foundRepo1 = true
			case "repo2":
				foundRepo2 = true
			case "repo16":
				foundRepo16 = true
			}
		}
		return foundRepo1, foundRepo2, foundRepo16
	}

	t.Run("all access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		allToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadUser, auth_model.AccessTokenScopeReadRepository)

		req := NewRequest(t, "GET", "/api/v1/users/user2/repos").AddTokenAuth(allToken)
		resp := MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &repos)
		foundRepo1, foundRepo2, foundRepo16 := find()

		assert.True(t, foundRepo1)  // public user2/repo1
		assert.True(t, foundRepo2)  // private user2/repo2
		assert.True(t, foundRepo16) // private user2/repo16, used in fine-grain testing, included as baseline
	})

	t.Run("public-only access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		publicOnlyToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopePublicOnly, auth_model.AccessTokenScopeReadUser, auth_model.AccessTokenScopeReadRepository)

		req := NewRequest(t, "GET", "/api/v1/users/user2/repos").AddTokenAuth(publicOnlyToken)
		resp := MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &repos)
		foundRepo1, foundRepo2, foundRepo16 := find()

		assert.True(t, foundRepo1)   // public user2/repo1
		assert.False(t, foundRepo2)  // private user2/repo2
		assert.False(t, foundRepo16) // private user2/repo16
	})

	t.Run("specific repo access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		repo2OnlyToken := createFineGrainedRepoAccessToken(t, "user2",
			[]auth_model.AccessTokenScope{auth_model.AccessTokenScopeReadUser, auth_model.AccessTokenScopeReadRepository},
			[]int64{2},
		)

		req := NewRequest(t, "GET", "/api/v1/users/user2/repos").AddTokenAuth(repo2OnlyToken)
		resp := MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &repos)
		foundRepo1, foundRepo2, foundRepo16 := find()

		assert.True(t, foundRepo1)   // public user2/repo1, allowed as it's public and read-access only
		assert.True(t, foundRepo2)   // private user2/repo2, allowed inside fine-grain
		assert.False(t, foundRepo16) // private user2/repo16, denied outside fine-grain
	})
}

func TestAPISearchRepo(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	const keyword = "test"

	req := NewRequestf(t, "GET", "/api/v1/repos/search?q=%s", keyword)
	resp := MakeRequest(t, req, http.StatusOK)

	var body api.SearchResults
	DecodeJSON(t, resp, &body)
	assert.NotEmpty(t, body.Data)
	for _, repo := range body.Data {
		assert.Contains(t, repo.Name, keyword)
		assert.False(t, repo.Private)
	}

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 15})
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 16})
	org3 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 18})
	user4 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 20})
	orgUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 17})
	actionsUser := user_model.NewActionsUser()

	oldAPIDefaultNum := setting.API.DefaultPagingNum
	defer func() {
		setting.API.DefaultPagingNum = oldAPIDefaultNum
	}()
	setting.API.DefaultPagingNum = 10

	// Map of expected results, where key is user for login
	type expectedResults map[*user_model.User]struct {
		count           int
		repoOwnerID     int64
		repoName        string
		includesPrivate bool
	}

	testCases := []struct {
		name, requestURL string
		expectedResults
	}{
		{
			name: "RepositoriesMax50", requestURL: "/api/v1/repos/search?limit=50&private=false", expectedResults: expectedResults{
				nil:   {count: 39},
				user:  {count: 39},
				user2: {count: 39},
			},
		},
		{
			name: "RepositoriesMax10", requestURL: "/api/v1/repos/search?limit=10&private=false", expectedResults: expectedResults{
				nil:   {count: 10},
				user:  {count: 10},
				user2: {count: 10},
			},
		},
		{
			name: "RepositoriesDefault", requestURL: "/api/v1/repos/search?default&private=false", expectedResults: expectedResults{
				nil:   {count: 10},
				user:  {count: 10},
				user2: {count: 10},
			},
		},
		{
			name: "RepositoriesByName", requestURL: fmt.Sprintf("/api/v1/repos/search?q=%s&private=false", "big_test_"), expectedResults: expectedResults{
				nil:   {count: 7, repoName: "big_test_"},
				user:  {count: 7, repoName: "big_test_"},
				user2: {count: 7, repoName: "big_test_"},
			},
		},
		{
			name: "RepositoriesByName", requestURL: fmt.Sprintf("/api/v1/repos/search?q=%s&private=false", "user2/big_test_"), expectedResults: expectedResults{
				user2: {count: 2, repoName: "big_test_"},
			},
		},
		{
			name: "RepositoriesAccessibleAndRelatedToUser", requestURL: fmt.Sprintf("/api/v1/repos/search?uid=%d", user.ID), expectedResults: expectedResults{
				nil:   {count: 5},
				user:  {count: 9, includesPrivate: true},
				user2: {count: 6, includesPrivate: true},
			},
		},
		{
			name: "RepositoriesAccessibleAndRelatedToUser2", requestURL: fmt.Sprintf("/api/v1/repos/search?uid=%d", user2.ID), expectedResults: expectedResults{
				nil:   {count: 1},
				user:  {count: 2, includesPrivate: true},
				user2: {count: 2, includesPrivate: true},
				user4: {count: 1},
			},
		},
		{
			name: "RepositoriesAccessibleAndRelatedToUser3", requestURL: fmt.Sprintf("/api/v1/repos/search?uid=%d", org3.ID), expectedResults: expectedResults{
				nil:   {count: 1},
				user:  {count: 4, includesPrivate: true},
				user2: {count: 3, includesPrivate: true},
				org3:  {count: 4, includesPrivate: true},
			},
		},
		{
			name: "RepositoriesOwnedByOrganization", requestURL: fmt.Sprintf("/api/v1/repos/search?uid=%d", orgUser.ID), expectedResults: expectedResults{
				nil:   {count: 1, repoOwnerID: orgUser.ID},
				user:  {count: 2, repoOwnerID: orgUser.ID, includesPrivate: true},
				user2: {count: 1, repoOwnerID: orgUser.ID},
			},
		},
		{name: "RepositoriesAccessibleAndRelatedToUser4", requestURL: fmt.Sprintf("/api/v1/repos/search?uid=%d", user4.ID), expectedResults: expectedResults{
			nil:   {count: 3},
			user:  {count: 4, includesPrivate: true},
			user4: {count: 7, includesPrivate: true},
		}},
		{name: "RepositoriesAccessibleAndRelatedToUser4/SearchModeSource", requestURL: fmt.Sprintf("/api/v1/repos/search?uid=%d&mode=%s", user4.ID, "source"), expectedResults: expectedResults{
			nil:   {count: 0},
			user:  {count: 1, includesPrivate: true},
			user4: {count: 1, includesPrivate: true},
		}},
		{name: "RepositoriesAccessibleAndRelatedToUser4/SearchModeFork", requestURL: fmt.Sprintf("/api/v1/repos/search?uid=%d&mode=%s", user4.ID, "fork"), expectedResults: expectedResults{
			nil:   {count: 1},
			user:  {count: 1},
			user4: {count: 2, includesPrivate: true},
		}},
		{name: "RepositoriesAccessibleAndRelatedToUser4/SearchModeFork/Exclusive", requestURL: fmt.Sprintf("/api/v1/repos/search?uid=%d&mode=%s&exclusive=1", user4.ID, "fork"), expectedResults: expectedResults{
			nil:   {count: 1},
			user:  {count: 1},
			user4: {count: 2, includesPrivate: true},
		}},
		{name: "RepositoriesAccessibleAndRelatedToUser4/SearchModeMirror", requestURL: fmt.Sprintf("/api/v1/repos/search?uid=%d&mode=%s", user4.ID, "mirror"), expectedResults: expectedResults{
			nil:   {count: 2},
			user:  {count: 2},
			user4: {count: 4, includesPrivate: true},
		}},
		{name: "RepositoriesAccessibleAndRelatedToUser4/SearchModeMirror/Exclusive", requestURL: fmt.Sprintf("/api/v1/repos/search?uid=%d&mode=%s&exclusive=1", user4.ID, "mirror"), expectedResults: expectedResults{
			nil:   {count: 1},
			user:  {count: 1},
			user4: {count: 2, includesPrivate: true},
		}},
		{name: "RepositoriesAccessibleAndRelatedToUser4/SearchModeCollaborative", requestURL: fmt.Sprintf("/api/v1/repos/search?uid=%d&mode=%s", user4.ID, "collaborative"), expectedResults: expectedResults{
			nil:   {count: 0},
			user:  {count: 1, includesPrivate: true},
			user4: {count: 1, includesPrivate: true},
		}},
		{name: "ForgejoActionsUser/UID", requestURL: fmt.Sprintf("/api/v1/repos/search?uid=%d", actionsUser.ID), expectedResults: expectedResults{
			nil:         {count: 0},
			actionsUser: {count: 0},
			user:        {count: 0},
			user4:       {count: 0},
		}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			for userToLogin, expected := range testCase.expectedResults {
				var testName string
				var userID int64
				var token string
				if userToLogin != nil && userToLogin.ID > 0 {
					testName = fmt.Sprintf("LoggedUser%d", userToLogin.ID)
					session := loginUser(t, userToLogin.Name)
					token = getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadRepository)
					userID = userToLogin.ID
				} else {
					testName = "AnonymousUser"
					_ = emptyTestSession(t)
				}

				t.Run(testName, func(t *testing.T) {
					request := NewRequest(t, "GET", testCase.requestURL).
						AddTokenAuth(token)
					response := MakeRequest(t, request, http.StatusOK)

					var body api.SearchResults
					DecodeJSON(t, response, &body)

					repoNames := make([]string, 0, len(body.Data))
					for _, repo := range body.Data {
						repoNames = append(repoNames, fmt.Sprintf("%d:%s:%t", repo.ID, repo.FullName, repo.Private))
					}
					assert.Len(t, repoNames, expected.count)
					for _, repo := range body.Data {
						r := getRepo(t, repo.ID)
						hasAccess, err := access_model.HasAccess(db.DefaultContext, userID, r)
						require.NoError(t, err, "Error when checking if User: %d has access to %s: %v", userID, repo.FullName, err)
						assert.True(t, hasAccess, "User: %d does not have access to %s", userID, repo.FullName)

						assert.NotEmpty(t, repo.Name)
						assert.Equal(t, repo.Name, r.Name)

						if len(expected.repoName) > 0 {
							assert.Contains(t, repo.Name, expected.repoName)
						}

						if expected.repoOwnerID > 0 {
							assert.Equal(t, expected.repoOwnerID, repo.Owner.ID)
						}

						if !expected.includesPrivate {
							assert.False(t, repo.Private, "User: %d not expecting private repository: %s", userID, repo.FullName)
						}
					}
				})
			}
		})
	}
}

func TestAPISearchRepoAccessTokenResources(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	var searchResults *api.SearchResults

	// Test cases repo1 (public), repo2 (private), repo16 (private).
	session := loginUser(t, "user2")

	find := func() (bool, bool, bool) {
		foundRepo1 := false  // public user2/repo1
		foundRepo2 := false  // private user2/repo2
		foundRepo16 := false // second private repo user2/repo16 used in fine-grain testing, included as baseline
		for _, repo := range searchResults.Data {
			switch repo.Name {
			case "repo1":
				foundRepo1 = true
			case "repo2":
				foundRepo2 = true
			case "repo16":
				foundRepo16 = true
			}
		}
		return foundRepo1, foundRepo2, foundRepo16
	}

	t.Run("all access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		allToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadRepository)

		req := NewRequest(t, "GET", "/api/v1/repos/search").AddTokenAuth(allToken)
		resp := MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &searchResults)
		require.True(t, searchResults.OK)
		foundRepo1, foundRepo2, foundRepo16 := find()

		assert.True(t, foundRepo1)  // public user2/repo1
		assert.True(t, foundRepo2)  // private user2/repo2
		assert.True(t, foundRepo16) // private user2/repo16, used in fine-grain testing, included as baseline
	})

	t.Run("public-only access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		publicOnlyToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopePublicOnly, auth_model.AccessTokenScopeReadRepository)

		req := NewRequest(t, "GET", "/api/v1/repos/search").AddTokenAuth(publicOnlyToken)
		resp := MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &searchResults)
		require.True(t, searchResults.OK)
		foundRepo1, foundRepo2, foundRepo16 := find()

		assert.True(t, foundRepo1)   // public user2/repo1
		assert.False(t, foundRepo2)  // private user2/repo2
		assert.False(t, foundRepo16) // private user2/repo16
	})

	t.Run("specific repo access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		repo2OnlyToken := createFineGrainedRepoAccessToken(t, "user2",
			[]auth_model.AccessTokenScope{auth_model.AccessTokenScopeReadRepository},
			[]int64{2},
		)

		req := NewRequest(t, "GET", "/api/v1/repos/search").AddTokenAuth(repo2OnlyToken)
		resp := MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &searchResults)
		require.True(t, searchResults.OK)
		foundRepo1, foundRepo2, foundRepo16 := find()

		assert.True(t, foundRepo1)   // public user2/repo1, allowed as it's public and read-access only
		assert.True(t, foundRepo2)   // private user2/repo2, allowed inside fine-grain
		assert.False(t, foundRepo16) // private user2/repo16, denied outside fine-grain
	})
}

var repoCache = make(map[int64]*repo_model.Repository)

func getRepo(t *testing.T, repoID int64) *repo_model.Repository {
	if _, ok := repoCache[repoID]; !ok {
		repoCache[repoID] = unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repoID})
	}
	return repoCache[repoID]
}

func TestAPIViewRepo(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	var repo api.Repository

	req := NewRequest(t, "GET", "/api/v1/repos/user2/repo1")
	resp := MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &repo)
	assert.EqualValues(t, 1, repo.ID)
	assert.Equal(t, "repo1", repo.Name)
	assert.Equal(t, 2, repo.Releases)
	assert.Equal(t, 1, repo.OpenIssues)
	assert.Equal(t, 3, repo.OpenPulls)

	req = NewRequest(t, "GET", "/api/v1/repos/user12/repo10")
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &repo)
	assert.EqualValues(t, 10, repo.ID)
	assert.Equal(t, "repo10", repo.Name)
	assert.Equal(t, 1, repo.OpenPulls)
	assert.Equal(t, 1, repo.Forks)

	req = NewRequest(t, "GET", "/api/v1/repos/user5/repo4")
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &repo)
	assert.EqualValues(t, 4, repo.ID)
	assert.Equal(t, "repo4", repo.Name)
	assert.Equal(t, 1, repo.Stars)
}

// `/repos/{username}/{reponame}` uses repoAssignment() middleware -- this test runs that middleware through all
// variations of access token resource access.
func TestAPIViewRepoAccessTokenResources(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	var repo api.Repository

	t.Run("all access token", func(t *testing.T) {
		session := loginUser(t, "user2")
		allToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadRepository)

		t.Run("allowed public repo1", func(t *testing.T) {
			req := NewRequest(t, "GET", "/api/v1/repos/user2/repo1").AddTokenAuth(allToken)
			resp := MakeRequest(t, req, http.StatusOK)
			DecodeJSON(t, resp, &repo)
			assert.False(t, repo.Private)
		})
		t.Run("allowed private repo2", func(t *testing.T) {
			req := NewRequest(t, "GET", "/api/v1/repos/user2/repo2").AddTokenAuth(allToken)
			resp := MakeRequest(t, req, http.StatusOK)
			DecodeJSON(t, resp, &repo)
			assert.True(t, repo.Private)
		})
		// repo16 is a second repo used in fine-grain testing below, so we include it in other tests as a baseline
		t.Run("allowed private repo16", func(t *testing.T) {
			req := NewRequest(t, "GET", "/api/v1/repos/user2/repo16").AddTokenAuth(allToken)
			resp := MakeRequest(t, req, http.StatusOK)
			DecodeJSON(t, resp, &repo)
			assert.True(t, repo.Private)
		})
	})

	t.Run("public-only access token", func(t *testing.T) {
		session := loginUser(t, "user2")
		publicOnlyToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopePublicOnly, auth_model.AccessTokenScopeReadRepository)

		t.Run("allowed public repo1", func(t *testing.T) {
			req := NewRequest(t, "GET", "/api/v1/repos/user2/repo1").AddTokenAuth(publicOnlyToken)
			resp := MakeRequest(t, req, http.StatusOK)
			DecodeJSON(t, resp, &repo)
			assert.False(t, repo.Private)
		})
		t.Run("denied private repo2", func(t *testing.T) {
			req := NewRequest(t, "GET", "/api/v1/repos/user2/repo2").AddTokenAuth(publicOnlyToken)
			MakeRequest(t, req, http.StatusNotFound)
		})
		t.Run("denied private repo16", func(t *testing.T) {
			req := NewRequest(t, "GET", "/api/v1/repos/user2/repo16").AddTokenAuth(publicOnlyToken)
			MakeRequest(t, req, http.StatusNotFound)
		})
	})

	t.Run("specific repo access token", func(t *testing.T) {
		repo2OnlyToken := createFineGrainedRepoAccessToken(t, "user2",
			[]auth_model.AccessTokenScope{auth_model.AccessTokenScopeReadRepository},
			[]int64{2},
		)

		t.Run("allowed public repo1", func(t *testing.T) {
			req := NewRequest(t, "GET", "/api/v1/repos/user2/repo1").AddTokenAuth(repo2OnlyToken)
			resp := MakeRequest(t, req, http.StatusOK)
			DecodeJSON(t, resp, &repo)
			assert.False(t, repo.Private)
		})
		t.Run("allowed inside fine-grain repo2", func(t *testing.T) {
			req := NewRequest(t, "GET", "/api/v1/repos/user2/repo2").AddTokenAuth(repo2OnlyToken)
			resp := MakeRequest(t, req, http.StatusOK)
			DecodeJSON(t, resp, &repo)
			assert.True(t, repo.Private)
		})
		t.Run("denied private outside fine-grain repo16", func(t *testing.T) {
			req := NewRequest(t, "GET", "/api/v1/repos/user2/repo16").AddTokenAuth(repo2OnlyToken)
			MakeRequest(t, req, http.StatusNotFound)
		})
	})
}

// Validate that private information on the user profile isn't exposed by way of being an owner of a public repository.
func TestAPIViewRepoOwnerSettings(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	var repo api.Repository

	req := NewRequest(t, "GET", "/api/v1/repos/user2/repo1")
	resp := MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &repo)
	assert.EqualValues(t, 1, repo.ID)
	assert.Equal(t, "user2@noreply.example.org", repo.Owner.Email) // unauthed, always private
	assert.Empty(t, repo.Owner.Pronouns)                           // user2.keep_pronouns_private = true

	session := loginUser(t, "user2")
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadRepository)
	req = NewRequest(t, "GET", "/api/v1/repos/user2/repo1").AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &repo)
	assert.Equal(t, "user2@noreply.example.org", repo.Owner.Email) // user2.keep_email_private = true
	assert.Equal(t, "he/him", repo.Owner.Pronouns)                 // user2.keep_pronouns_private = true

	req = NewRequest(t, "GET", "/api/v1/repos/user12/repo10")
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &repo)
	assert.EqualValues(t, 10, repo.ID)
	assert.Equal(t, "user12@noreply.example.org", repo.Owner.Email) // unauthed, always private

	req = NewRequest(t, "GET", "/api/v1/repos/user12/repo10").AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &repo)
	assert.Equal(t, "user12@example.com", repo.Owner.Email) // user2.keep_email_private = false
}

func TestAPIOrgRepos(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	org3 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	// org3 is an Org. Check their repos.
	sourceOrg := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3})

	expectedResults := map[*user_model.User]struct {
		count           int
		includesPrivate bool
	}{
		user:  {count: 1},
		user:  {count: 3, includesPrivate: true},
		user2: {count: 3, includesPrivate: true},
		org3:  {count: 1},
	}

	for userToLogin, expected := range expectedResults {
		testName := fmt.Sprintf("LoggedUser%d", userToLogin.ID)
		session := loginUser(t, userToLogin.Name)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadOrganization)

		t.Run(testName, func(t *testing.T) {
			req := NewRequestf(t, "GET", "/api/v1/orgs/%s/repos", sourceOrg.Name).
				AddTokenAuth(token)
			resp := MakeRequest(t, req, http.StatusOK)

			var apiRepos []*api.Repository
			DecodeJSON(t, resp, &apiRepos)
			assert.Len(t, apiRepos, expected.count)
			for _, repo := range apiRepos {
				if !expected.includesPrivate {
					assert.False(t, repo.Private)
				}
			}
		})
	}
}

// See issue #28483. Tests to make sure we consider more than just code unit-enabled repositories.
func TestAPIOrgReposWithCodeUnitDisabled(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	repo21 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{Name: "repo21"})
	org3 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo21.OwnerID})

	// Disable code repository unit.
	var units []unit_model.Type
	units = append(units, unit_model.TypeCode)

	require.NoError(t, repo_service.UpdateRepositoryUnits(db.DefaultContext, repo21, nil, units))
	assert.False(t, repo21.UnitEnabled(db.DefaultContext, unit_model.TypeCode))

	session := loginUser(t, "user2")
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadOrganization)

	req := NewRequestf(t, "GET", "/api/v1/orgs/%s/repos", org3.Name).
		AddTokenAuth(token)

	resp := MakeRequest(t, req, http.StatusOK)
	var apiRepos []*api.Repository
	DecodeJSON(t, resp, &apiRepos)

	var repoNames []string
	for _, r := range apiRepos {
		repoNames = append(repoNames, r.Name)
	}

	assert.Contains(t, repoNames, repo21.Name)
}

func TestAPIGetRepoByIDUnauthorized(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	session := loginUser(t, user.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadRepository)
	req := NewRequest(t, "GET", "/api/v1/repositories/2").
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusNotFound)
}

func TestAPIGetRepoByIDAccessTokenResources(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	// Test targets:
	// id 1 - user2/repo1 - public repo
	// id 2 - user2/repo2 - private repo
	// id 16 - user2/repo16 - private repo
	testCase := func(t *testing.T, repoID int, token string, expectedStatus int) {
		req := NewRequest(t,
			"GET",
			fmt.Sprintf("/api/v1/repositories/%d", repoID)).
			AddTokenAuth(token)
		MakeRequest(t, req, expectedStatus)
	}

	t.Run("all access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		allToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadRepository)

		testCase(t, 1, allToken, http.StatusOK)  // public user2/repo1
		testCase(t, 2, allToken, http.StatusOK)  // private user2/repo2
		testCase(t, 16, allToken, http.StatusOK) // private org3/repo3
	})

	t.Run("public-only access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		publicOnlyToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopePublicOnly, auth_model.AccessTokenScopeReadRepository)

		testCase(t, 1, publicOnlyToken, http.StatusOK)        // public user2/repo1
		testCase(t, 2, publicOnlyToken, http.StatusNotFound)  // private user2/repo2
		testCase(t, 16, publicOnlyToken, http.StatusNotFound) // private org3/repo3
	})

	t.Run("specific repo access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		repo2OnlyToken := createFineGrainedRepoAccessToken(t, "user2",
			[]auth_model.AccessTokenScope{auth_model.AccessTokenScopeReadRepository},
			[]int64{2},
		)

		testCase(t, 1, repo2OnlyToken, http.StatusOK)        // public user2/repo1, read-only outside of the auth'd repos
		testCase(t, 2, repo2OnlyToken, http.StatusOK)        // private org3/repo3
		testCase(t, 16, repo2OnlyToken, http.StatusNotFound) // private user2/repo20, outside of fine-grain
	})
}

func TestAPIRepoMigrate(t *testing.T) {
	testCases := []struct {
		ctxUserID, userID  int64
		cloneURL, repoName string
		expectedStatus     int
	}{
		{ctxUserID: 1, userID: 2, cloneURL: "https://code.forgejo.org/forgejo/migration-test.git", repoName: "git-admin", expectedStatus: http.StatusCreated},
		{ctxUserID: 2, userID: 2, cloneURL: "https://code.forgejo.org/forgejo/migration-test.git", repoName: "git-own", expectedStatus: http.StatusCreated},
		{ctxUserID: 2, userID: 1, cloneURL: "https://code.forgejo.org/forgejo/migration-test.git", repoName: "git-bad", expectedStatus: http.StatusForbidden},
		{ctxUserID: 2, userID: 3, cloneURL: "https://code.forgejo.org/forgejo/migration-test.git", repoName: "git-org", expectedStatus: http.StatusCreated},
		{ctxUserID: 2, userID: 6, cloneURL: "https://code.forgejo.org/forgejo/migration-test.git", repoName: "git-bad-org", expectedStatus: http.StatusForbidden},
		{ctxUserID: 2, userID: 3, cloneURL: "https://localhost:3000/user/test_repo.git", repoName: "private-ip-localhost", expectedStatus: http.StatusUnprocessableEntity},
		{ctxUserID: 2, userID: 3, cloneURL: "https://10.0.0.1/user/test_repo.git", repoName: "private-ip", expectedStatus: http.StatusUnprocessableEntity},
	}

	defer tests.PrepareTestEnv(t)()
	for _, testCase := range testCases {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: testCase.ctxUserID})
		session := loginUser(t, user.Name)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
		req := NewRequestWithJSON(t, "POST", "/api/v1/repos/migrate", &api.MigrateRepoOptions{
			CloneAddr:   testCase.cloneURL,
			RepoOwnerID: testCase.userID,
			RepoName:    testCase.repoName,
			Wiki:        true,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, NoExpectedStatus)
		require.Equalf(t, testCase.expectedStatus, resp.Code, "unexpected status for %s (may be due to throttling): '%v' on url '%s'", testCase.repoName, resp.Body.String(), testCase.cloneURL)
	}
}

func TestAPIRepoMigrateConflict(t *testing.T) {
	onApplicationRun(t, testAPIRepoMigrateConflict)
}

func testAPIRepoMigrateConflict(t *testing.T, u *url.URL) {
	username := "user2"
	baseAPITestContext := NewAPITestContext(t, username, "repo1", auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)

	u.Path = baseAPITestContext.GitPath()

	t.Run("Existing", func(t *testing.T) {
		httpContext := baseAPITestContext

		httpContext.Reponame = "repo-tmp-17"
		t.Run("CreateRepo", doAPICreateRepository(httpContext, nil, git.Sha1ObjectFormat)) // FIXME: use forEachObjectFormat

		user, err := user_model.GetUserByName(db.DefaultContext, httpContext.Username)
		require.NoError(t, err)
		userID := user.ID

		cloneURL := "https://code.forgejo.org/forgejo/migration-test.git"

		req := NewRequestWithJSON(t, "POST", "/api/v1/repos/migrate",
			&api.MigrateRepoOptions{
				CloneAddr:   cloneURL,
				RepoOwnerID: userID,
				RepoName:    httpContext.Reponame,
			}).
			AddTokenAuth(httpContext.Token)
		resp := httpContext.Session.MakeRequest(t, req, http.StatusConflict)
		respJSON := map[string]string{}
		DecodeJSON(t, resp, &respJSON)
		assert.Equal(t, "The repository with the same name already exists.", respJSON["message"])
	})
}

// mirror-sync must fail with "400 (Bad Request)" when an attempt is made to
// sync a non-mirror repository.
func TestAPIMirrorSyncNonMirrorRepo(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

	var repo api.Repository
	req := NewRequest(t, "GET", "/api/v1/repos/user2/repo1")
	resp := MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &repo)
	assert.False(t, repo.Mirror)

	req = NewRequestf(t, "POST", "/api/v1/repos/user2/repo1/mirror-sync").
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusBadRequest)
	errRespJSON := map[string]string{}
	DecodeJSON(t, resp, &errRespJSON)
	assert.Equal(t, "Repository is not a mirror", errRespJSON["message"])
}

func TestAPIOrgRepoCreate(t *testing.T) {
	testCases := []struct {
		ctxUserID         int64
		orgName, repoName string
		expectedStatus    int
	}{
		{ctxUserID: 1, orgName: "org3", repoName: "repo-admin", expectedStatus: http.StatusCreated},
		{ctxUserID: 2, orgName: "org3", repoName: "repo-own", expectedStatus: http.StatusCreated},
		{ctxUserID: 2, orgName: "org6", repoName: "repo-bad-org", expectedStatus: http.StatusForbidden},
		{ctxUserID: 28, orgName: "org3", repoName: "repo-creator", expectedStatus: http.StatusCreated},
		{ctxUserID: 28, orgName: "org6", repoName: "repo-not-creator", expectedStatus: http.StatusForbidden},
	}

	defer tests.PrepareTestEnv(t)()
	for _, testCase := range testCases {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: testCase.ctxUserID})
		session := loginUser(t, user.Name)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteOrganization, auth_model.AccessTokenScopeWriteRepository)
		req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/org/%s/repos", testCase.orgName), &api.CreateRepoOption{
			Name: testCase.repoName,
		}).AddTokenAuth(token)
		MakeRequest(t, req, testCase.expectedStatus)
	}
}

func TestAPIRepoCreateConflict(t *testing.T) {
	onApplicationRun(t, testAPIRepoCreateConflict)
}

func testAPIRepoCreateConflict(t *testing.T, u *url.URL) {
	username := "user2"
	baseAPITestContext := NewAPITestContext(t, username, "repo1", auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)

	u.Path = baseAPITestContext.GitPath()

	t.Run("Existing", func(t *testing.T) {
		httpContext := baseAPITestContext

		httpContext.Reponame = "repo-tmp-17"
		t.Run("CreateRepo", doAPICreateRepository(httpContext, nil, git.Sha1ObjectFormat)) // FIXME: use forEachObjectFormat

		req := NewRequestWithJSON(t, "POST", "/api/v1/user/repos",
			&api.CreateRepoOption{
				Name: httpContext.Reponame,
			}).
			AddTokenAuth(httpContext.Token)
		resp := httpContext.Session.MakeRequest(t, req, http.StatusConflict)
		respJSON := map[string]string{}
		DecodeJSON(t, resp, &respJSON)
		assert.Equal(t, "The repository with the same name already exists.", respJSON["message"])
	})
}

func TestAPIRepoCreateDenied(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	// This test verifies that `write:repository` is not a sufficient scope to create a repository.  If it was, then
	// repo-specific access tokens would be able to create new repositories.
	session := loginUser(t, "user2")
	writeToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

	req := NewRequestWithJSON(t, "POST", "/api/v1/user/repos",
		&api.CreateRepoOption{
			Name: "my-new-repo",
		}).
		AddTokenAuth(writeToken)
	MakeRequest(t, req, http.StatusForbidden)
}

func TestAPIRepoDelete(t *testing.T) {
	t.Run("permitted to delete user repo w/ user scope", func(t *testing.T) {
		defer tests.PrepareTestEnv(t)()
		session := loginUser(t, "user2")
		writeToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteUser)
		req := NewRequest(t, "DELETE", "/api/v1/repos/user2/repo2").
			AddTokenAuth(writeToken)
		MakeRequest(t, req, http.StatusNoContent)
	})

	t.Run("denied to delete user repo w/ org scope", func(t *testing.T) {
		defer tests.PrepareTestEnv(t)()
		session := loginUser(t, "user2")
		writeToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteOrganization)
		req := NewRequest(t, "DELETE", "/api/v1/repos/user2/repo2").
			AddTokenAuth(writeToken)
		resp := MakeRequest(t, req, http.StatusForbidden)
		assert.Contains(t, resp.Body.String(), "token does not have at least one of required scope(s): [write:user]")
	})

	t.Run("permitted to delete org repo w/ org scope", func(t *testing.T) {
		defer tests.PrepareTestEnv(t)()
		session := loginUser(t, "user2")
		writeToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteOrganization)
		req := NewRequest(t, "DELETE", "/api/v1/repos/org3/repo3").
			AddTokenAuth(writeToken)
		MakeRequest(t, req, http.StatusNoContent)
	})

	t.Run("denied to delete org repo w/ user scope", func(t *testing.T) {
		defer tests.PrepareTestEnv(t)()
		session := loginUser(t, "user2")
		writeToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteUser)
		req := NewRequest(t, "DELETE", "/api/v1/repos/org3/repo3").
			AddTokenAuth(writeToken)
		resp := MakeRequest(t, req, http.StatusForbidden)
		assert.Contains(t, resp.Body.String(), "token does not have at least one of required scope(s): [write:organization]")
	})

	t.Run("denied with repo-specific", func(t *testing.T) {
		defer tests.PrepareTestEnv(t)()
		// limit ourselves to write:repository -- repo-specific access tokens can't be created with write:user
		repo2OnlyToken := createFineGrainedRepoAccessToken(t, "user2",
			[]auth_model.AccessTokenScope{auth_model.AccessTokenScopeWriteRepository},
			[]int64{2},
		)
		req := NewRequest(t, "DELETE", "/api/v1/repos/user2/repo2").
			AddTokenAuth(repo2OnlyToken)
		resp := MakeRequest(t, req, http.StatusForbidden)
		assert.Contains(t, resp.Body.String(), "token does not have at least one of required scope(s): [write:user]")
	})
}

func TestAPIRepoTransfer(t *testing.T) {
	testCases := []struct {
		ctxUserID      int64
		newOwner       string
		teams          *[]int64
		expectedStatus int
	}{
		// Disclaimer for test story: "user1" is an admin, "user2" is normal user and part of in owner team of org "org3"
		// Transfer to a user with teams in another org should fail
		{ctxUserID: 1, newOwner: "org3", teams: &[]int64{5}, expectedStatus: http.StatusForbidden},
		// Transfer to a user with non-existent team IDs should fail
		{ctxUserID: 1, newOwner: "user2", teams: &[]int64{2}, expectedStatus: http.StatusUnprocessableEntity},
		// Transfer should go through
		{ctxUserID: 1, newOwner: "org3", teams: &[]int64{2}, expectedStatus: http.StatusAccepted},
		// Let user transfer it back to himself
		{ctxUserID: 2, newOwner: "user2", expectedStatus: http.StatusAccepted},
		// And revert transfer
		{ctxUserID: 2, newOwner: "org3", teams: &[]int64{2}, expectedStatus: http.StatusAccepted},
		// Cannot start transfer to an existing repo
		{ctxUserID: 2, newOwner: "org3", teams: nil, expectedStatus: http.StatusUnprocessableEntity},
		// Start transfer, repo is now in pending transfer mode
		{ctxUserID: 2, newOwner: "org6", teams: nil, expectedStatus: http.StatusCreated},
	}

	defer tests.PrepareTestEnv(t)()

	// create repo to move
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	session := loginUser(t, user.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)
	repoName := "moveME"
	apiRepo := new(api.Repository)
	req := NewRequestWithJSON(t, "POST", "/api/v1/user/repos", &api.CreateRepoOption{
		Name:        repoName,
		Description: "repo move around",
		Private:     false,
		Readme:      "Default",
		AutoInit:    true,
	}).AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusCreated)
	DecodeJSON(t, resp, apiRepo)

	// start testing
	for _, testCase := range testCases {
		user = unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: testCase.ctxUserID})
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: apiRepo.ID})
		session = loginUser(t, user.Name)
		token = getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
		req = NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/%s/transfer", repo.OwnerName, repo.Name), &api.TransferRepoOption{
			NewOwner: testCase.newOwner,
			TeamIDs:  testCase.teams,
		}).AddTokenAuth(token)
		MakeRequest(t, req, testCase.expectedStatus)
	}

	// cleanup
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: apiRepo.ID})
	require.NoError(t, repo_service.DeleteRepositoryDirectly(db.DefaultContext, repo.ID, repo_service.DeleteRepositoryOpts{}))
}

// This test verifies that a repo-specific access token with `write:repository` scope is not a sufficient to transfer a
// repository to another user.
func TestAPIRepoTransferAccessTokenResources(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo2OnlyToken := createFineGrainedRepoAccessToken(t, "user2",
		[]auth_model.AccessTokenScope{auth_model.AccessTokenScopeWriteRepository},
		[]int64{2},
	)

	req := NewRequestWithJSON(t, "POST", "/api/v1/repos/user2/repo2/transfer", &api.TransferRepoOption{
		NewOwner: "org3",
	}).AddTokenAuth(repo2OnlyToken)
	resp := MakeRequest(t, req, http.StatusForbidden)
	assert.Contains(t, resp.Body.String(), "user should be an owner or a collaborator with admin write")
}

func transfer(t *testing.T) *repo_model.Repository {
	// create repo to move
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	session := loginUser(t, user.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)
	repoName := "moveME"
	apiRepo := new(api.Repository)
	req := NewRequestWithJSON(t, "POST", "/api/v1/user/repos", &api.CreateRepoOption{
		Name:        repoName,
		Description: "repo move around",
		Private:     false,
		Readme:      "Default",
		AutoInit:    true,
	}).AddTokenAuth(token)

	resp := MakeRequest(t, req, http.StatusCreated)
	DecodeJSON(t, resp, apiRepo)

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: apiRepo.ID})
	req = NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/%s/transfer", repo.OwnerName, repo.Name), &api.TransferRepoOption{
		NewOwner: "user4",
	}).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusCreated)

	return repo
}

func TestAPIAcceptTransfer(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := transfer(t)

	// try to accept with not authorized user
	session := loginUser(t, "user2")
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)
	req := NewRequest(t, "POST", fmt.Sprintf("/api/v1/repos/%s/%s/transfer/reject", repo.OwnerName, repo.Name)).
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusForbidden)

	// try to accept repo that's not marked as transferred
	req = NewRequest(t, "POST", fmt.Sprintf("/api/v1/repos/%s/%s/transfer/accept", "user2", "repo1")).
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusNotFound)

	// accept transfer
	session = loginUser(t, "user4")
	token = getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)

	req = NewRequest(t, "POST", fmt.Sprintf("/api/v1/repos/%s/%s/transfer/accept", repo.OwnerName, repo.Name)).
		AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusAccepted)
	apiRepo := new(api.Repository)
	DecodeJSON(t, resp, apiRepo)
	assert.Equal(t, "user4", apiRepo.Owner.UserName)
}

func TestAPIRejectTransfer(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := transfer(t)

	// try to reject with not authorized user
	session := loginUser(t, "user2")
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
	req := NewRequest(t, "POST", fmt.Sprintf("/api/v1/repos/%s/%s/transfer/reject", repo.OwnerName, repo.Name)).
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusForbidden)

	// try to reject repo that's not marked as transferred
	req = NewRequest(t, "POST", fmt.Sprintf("/api/v1/repos/%s/%s/transfer/reject", "user2", "repo1")).
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusNotFound)

	// reject transfer
	session = loginUser(t, "user4")
	token = getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

	req = NewRequest(t, "POST", fmt.Sprintf("/api/v1/repos/%s/%s/transfer/reject", repo.OwnerName, repo.Name)).
		AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	apiRepo := new(api.Repository)
	DecodeJSON(t, resp, apiRepo)
	assert.Equal(t, "user2", apiRepo.Owner.UserName)
}

func TestAPIGenerateRepo(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	templateRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 44})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	session := loginUser(t, user.Name)

	// write:repository scope is always required (logically, because we're writing inside the contents of a new
	// repository) but the need for write:user or write:organization depends on the target owner, so we'll test those
	// combinations.

	t.Run("permitted to generate into user with user scope", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteUser, auth_model.AccessTokenScopeWriteRepository)
		repo := new(api.Repository)
		req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/%s/generate", templateRepo.OwnerName, templateRepo.Name), &api.GenerateRepoOption{
			Owner:       user.Name,
			Name:        "new-repo",
			Description: "test generate repo",
			Private:     false,
			GitContent:  true,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusCreated)
		DecodeJSON(t, resp, repo)
		assert.Equal(t, "new-repo", repo.Name)
	})

	t.Run("denied to generate into user without user scope", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
		req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/%s/generate", templateRepo.OwnerName, templateRepo.Name), &api.GenerateRepoOption{
			Owner:       user.Name,
			Name:        "new-repo",
			Description: "test generate repo",
			Private:     false,
			GitContent:  true,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusForbidden)
		assert.Contains(t, resp.Body.String(), "token requires scope write:user to create a repository owned by a user")
	})

	t.Run("permitted to generate into org with org scope", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteOrganization, auth_model.AccessTokenScopeWriteRepository)
		repo := new(api.Repository)
		req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/%s/generate", templateRepo.OwnerName, templateRepo.Name), &api.GenerateRepoOption{
			Owner:       "org3",
			Name:        "new-repo",
			Description: "test generate repo",
			Private:     false,
			GitContent:  true,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusCreated)
		DecodeJSON(t, resp, repo)

		assert.Equal(t, "new-repo", repo.Name)
	})

	t.Run("denied to generate into org without org scope", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
		req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/%s/generate", templateRepo.OwnerName, templateRepo.Name), &api.GenerateRepoOption{
			Owner:       "org3",
			Name:        "new-repo",
			Description: "test generate repo",
			Private:     false,
			GitContent:  true,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusForbidden)
		assert.Contains(t, resp.Body.String(), "token requires scope write:organization to create a repository owned by a user")
	})

	t.Run("denied to generate without write:repository", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteUser)
		req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/%s/generate", templateRepo.OwnerName, templateRepo.Name), &api.GenerateRepoOption{
			Owner:       user.Name,
			Name:        "new-repo",
			Description: "test generate repo",
			Private:     false,
			GitContent:  true,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusForbidden)
		assert.Contains(t, resp.Body.String(), "token does not have at least one of required scope(s): [write:repository]")
	})
}

func TestAPIRepoGetReviewers(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	session := loginUser(t, user.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadRepository)
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	req := NewRequestf(t, "GET", "/api/v1/repos/%s/%s/reviewers", user.Name, repo.Name).
		AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	var reviewers []*api.User
	DecodeJSON(t, resp, &reviewers)
	if assert.Len(t, reviewers, 3) {
		assert.ElementsMatch(t, []int64{1, 4, 11}, []int64{reviewers[0].ID, reviewers[1].ID, reviewers[2].ID})
	}
}

func TestAPIRepoGetAssignees(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	session := loginUser(t, user.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadRepository)
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	req := NewRequestf(t, "GET", "/api/v1/repos/%s/%s/assignees", user.Name, repo.Name).
		AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	var assignees []*api.User
	DecodeJSON(t, resp, &assignees)
	assert.Len(t, assignees, 1)
}

func TestAPIViewRepoObjectFormat(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	var repo api.Repository

	req := NewRequest(t, "GET", "/api/v1/repos/user2/repo1")
	resp := MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &repo)
	assert.Equal(t, "sha1", repo.ObjectFormatName)
}

// TestAPIViewRepoWikiGitInfo tests wiki git information
func TestAPIViewRepoWikiGitInfo(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	for _, test := range []struct {
		name        string
		user        string
		repo        string
		hasWiki     bool
		hasContents bool
	}{
		{
			name:        "wiki enabled, wiki contents",
			user:        "user2",
			repo:        "repo1",
			hasWiki:     true,
			hasContents: true,
		},
		{
			name:        "wiki enabled, no wiki contents",
			user:        "user5",
			repo:        "repo4",
			hasWiki:     true,
			hasContents: false,
		},
		{
			name:        "wiki disabled, no wiki contents",
			user:        "user12",
			repo:        "repo10",
			hasWiki:     false,
			hasContents: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// get repo
			url := fmt.Sprintf("/api/v1/repos/%s/%s", test.user, test.repo)
			req := NewRequest(t, "GET", url)
			resp := MakeRequest(t, req, http.StatusOK)
			var repo api.Repository
			DecodeJSON(t, resp, &repo)

			// check repo
			sshURL := fmt.Sprintf("ssh://%s@%s:%d/%s/%s.wiki.git",
				setting.SSH.User, setting.SSH.Domain, setting.SSH.Port,
				test.user, test.repo)
			cloneURL := fmt.Sprintf("http://%s:%s/%s/%s.wiki.git",
				setting.Domain, setting.HTTPPort,
				test.user, test.repo)
			assert.Equal(t, test.hasWiki, repo.HasWiki)
			assert.Equal(t, test.hasContents, repo.HasWikiContents)
			assert.Equal(t, sshURL, repo.WikiSSHURL)
			assert.Equal(t, cloneURL, repo.WikiCloneURL)
		})
	}
}

func TestAPIRepoCommitPull(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	var pr api.PullRequest
	req := NewRequest(t, "GET", "/api/v1/repos/user2/repo1/commits/1a8823cd1a9549fde083f992f6b9b87a7ab74fb3/pull")
	resp := MakeRequest(t, req, http.StatusOK)

	DecodeJSON(t, resp, &pr)
	assert.EqualValues(t, 1, pr.ID)

	req = NewRequest(t, "GET", "/api/v1/repos/user2/repo1/commits/not-a-commit/pull")
	MakeRequest(t, req, http.StatusNotFound)
}

func TestAPIListOwnRepoSorting(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	session := loginUser(t, user.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadRepository, auth_model.AccessTokenScopeReadUser)

	t.Run("No sorting", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		MakeRequest(t, NewRequest(t, "GET", "/api/v1/user/repos").AddTokenAuth(token), http.StatusOK)
	})

	t.Run("ID sorting", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		var repos []api.Repository
		resp := MakeRequest(t, NewRequest(t, "GET", "/api/v1/user/repos?limit=2&order_by=id").AddTokenAuth(token), http.StatusOK)
		DecodeJSON(t, resp, &repos)

		assert.Len(t, repos, 2)
		assert.EqualValues(t, 1, repos[0].ID)
		assert.EqualValues(t, 2, repos[1].ID)
	})

	t.Run("Name sorting", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		var repos []api.Repository
		resp := MakeRequest(t, NewRequest(t, "GET", "/api/v1/user/repos?limit=2&order_by=name").AddTokenAuth(token), http.StatusOK)
		DecodeJSON(t, resp, &repos)

		assert.Len(t, repos, 2)
		assert.Equal(t, "big_test_private_4", repos[0].Name)
		// Postgres doesn't do ascii sorting.
		if setting.Database.Type.IsPostgreSQL() {
			assert.Equal(t, "commitsonpr", repos[1].Name)
		} else {
			assert.Equal(t, "commits_search_test", repos[1].Name)
		}
	})

	t.Run("Reverse alphabetic sorting", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		var repos []api.Repository
		resp := MakeRequest(t, NewRequest(t, "GET", "/api/v1/user/repos?limit=2&order_by=reversealphabetically").AddTokenAuth(token), http.StatusOK)
		DecodeJSON(t, resp, &repos)

		assert.Len(t, repos, 2)
		assert.Equal(t, "utf8", repos[0].Name)
		assert.Equal(t, "test_workflows", repos[1].Name)
	})
}

func TestAPIListOwnRepoAccessTokenResources(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	var repos []api.Repository

	// Test cases repo1 (public), repo2 (private), repo16 (private).
	session := loginUser(t, "user2")

	find := func() (bool, bool, bool) {
		foundRepo1 := false  // public user2/repo1
		foundRepo2 := false  // private user2/repo2
		foundRepo16 := false // second private repo user2/repo16 used in fine-grain testing, included as baseline
		for _, repo := range repos {
			switch repo.Name {
			case "repo1":
				foundRepo1 = true
			case "repo2":
				foundRepo2 = true
			case "repo16":
				foundRepo16 = true
			}
		}
		return foundRepo1, foundRepo2, foundRepo16
	}

	t.Run("all access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		allToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadUser, auth_model.AccessTokenScopeReadRepository)

		req := NewRequest(t, "GET", "/api/v1/user/repos").AddTokenAuth(allToken)
		resp := MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &repos)
		foundRepo1, foundRepo2, foundRepo16 := find()

		assert.True(t, foundRepo1)  // public user2/repo1
		assert.True(t, foundRepo2)  // private user2/repo2
		assert.True(t, foundRepo16) // private user2/repo16, used in fine-grain testing, included as baseline
	})

	t.Run("public-only access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		publicOnlyToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopePublicOnly, auth_model.AccessTokenScopeReadUser, auth_model.AccessTokenScopeReadRepository)

		req := NewRequest(t, "GET", "/api/v1/user/repos").AddTokenAuth(publicOnlyToken)
		resp := MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &repos)
		foundRepo1, foundRepo2, foundRepo16 := find()

		assert.True(t, foundRepo1)   // public user2/repo1
		assert.False(t, foundRepo2)  // private user2/repo2
		assert.False(t, foundRepo16) // private user2/repo16
	})

	t.Run("specific repo access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		repo2OnlyToken := createFineGrainedRepoAccessToken(t, "user2",
			[]auth_model.AccessTokenScope{auth_model.AccessTokenScopeReadUser, auth_model.AccessTokenScopeReadRepository},
			[]int64{2},
		)

		req := NewRequest(t, "GET", "/api/v1/user/repos").AddTokenAuth(repo2OnlyToken)
		resp := MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &repos)
		foundRepo1, foundRepo2, foundRepo16 := find()

		assert.True(t, foundRepo1)   // public user2/repo1, allowed as it's public and read-access only
		assert.True(t, foundRepo2)   // private user2/repo2, allowed inside fine-grain
		assert.False(t, foundRepo16) // private user2/repo16, denied outside fine-grain
	})
}
