// Copyright 2026 The Forgejo Authors.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"testing"

	auth_model "forgejo.org/models/auth"
	api "forgejo.org/modules/structs"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
)

// A token limited to public repo1 must not learn about private repo2.
// List endpoints already hide repo2. The activity feed still returns it.
func TestAPIActivityFeedUserRestrictedTokenPrivateRepo(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	token := createFineGrainedRepoAccessToken(t, "user2",
		[]auth_model.AccessTokenScope{auth_model.AccessTokenScopeReadUser, auth_model.AccessTokenScopeReadRepository},
		[]int64{1}, // user2/repo1, public
	)

	req := NewRequest(t, "GET", "/api/v1/repos/user2/repo2").AddTokenAuth(token)
	MakeRequest(t, req, http.StatusNotFound)

	req = NewRequest(t, "GET", "/api/v1/users/user2/repos").AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	var repos []*api.Repository
	DecodeJSON(t, resp, &repos)
	for _, repo := range repos {
		assert.NotEqual(t, "user2/repo2", repo.FullName)
	}

	req = NewRequest(t, "GET", "/api/v1/users/user2/activities/feeds").AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	var activities []api.Activity
	DecodeJSON(t, resp, &activities)

	var leaked *api.Activity
	for i := range activities {
		if activities[i].Repo != nil && activities[i].Repo.FullName == "user2/repo2" {
			leaked = &activities[i]
			break
		}
	}
	assert.Nil(t, leaked, "restricted token expected no user2/repo2 activity; feed ids=%v", activityRepoNames(activities))
}

func TestAPIActivityFeedUserPublicOnlyPrivateRepo(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")
	token := getTokenForLoggedInUser(t, session,
		auth_model.AccessTokenScopePublicOnly,
		auth_model.AccessTokenScopeReadUser,
		auth_model.AccessTokenScopeReadRepository,
	)

	req := NewRequest(t, "GET", "/api/v1/repos/user2/repo2").AddTokenAuth(token)
	MakeRequest(t, req, http.StatusNotFound)

	req = NewRequest(t, "GET", "/api/v1/users/user2/activities/feeds").AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	var activities []api.Activity
	DecodeJSON(t, resp, &activities)

	var leaked *api.Activity
	for i := range activities {
		if activities[i].Repo != nil && activities[i].Repo.FullName == "user2/repo2" {
			leaked = &activities[i]
			break
		}
	}
	assert.Nil(t, leaked, "public-only token expected no user2/repo2 activity; feed ids=%v", activityRepoNames(activities))
}

func TestAPIActivityFeedOrgPublicOnlyPrivateRepo(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")
	token := getTokenForLoggedInUser(t, session,
		auth_model.AccessTokenScopePublicOnly,
		auth_model.AccessTokenScopeReadOrganization,
		auth_model.AccessTokenScopeReadRepository,
	)

	req := NewRequest(t, "GET", "/api/v1/repos/org3/repo3").AddTokenAuth(token)
	MakeRequest(t, req, http.StatusNotFound)

	req = NewRequest(t, "GET", "/api/v1/orgs/org3/activities/feeds").AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	var activities []api.Activity
	DecodeJSON(t, resp, &activities)

	var leaked *api.Activity
	for i := range activities {
		if activities[i].Repo != nil && activities[i].Repo.FullName == "org3/repo3" {
			leaked = &activities[i]
			break
		}
	}
	assert.Nil(t, leaked, "public-only org token expected no org3/repo3 activity; feed ids=%v", activityRepoNames(activities))
}

func activityRepoNames(activities []api.Activity) []string {
	names := make([]string, 0, len(activities))
	for _, activity := range activities {
		if activity.Repo == nil {
			names = append(names, "")
			continue
		}
		names = append(names, activity.Repo.FullName)
	}
	return names
}
