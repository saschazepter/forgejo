// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	activities_model "forgejo.org/models/activities"
	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	"forgejo.org/modules/timeutil"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIUserHeatmap(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	adminUsername := "user1"
	normalUsername := "user2"
	token := getUserToken(t, adminUsername, auth_model.AccessTokenScopeReadUser)

	fakeNow := time.Date(2011, 10, 20, 0, 0, 0, 0, time.Local)
	timeutil.MockSet(fakeNow)
	defer timeutil.MockUnset()

	req := NewRequest(t, "GET", fmt.Sprintf("/api/v1/users/%s/heatmap", normalUsername)).
		AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	var heatmap []*activities_model.UserHeatmapData
	DecodeJSON(t, resp, &heatmap)
	var dummyheatmap []*activities_model.UserHeatmapData
	dummyheatmap = append(dummyheatmap, &activities_model.UserHeatmapData{Timestamp: 1603227600, Contributions: 1})

	assert.Equal(t, dummyheatmap, heatmap)
}

func TestAPIUserHeatmapAccessReducer(t *testing.T) {
	for _, test := range []struct {
		name           string
		publicOnly     bool
		repoIDs        []int64
		includePrivate bool
	}{
		{name: "PublicOnly", publicOnly: true, includePrivate: false},
		{name: "PublicRepoOnly", repoIDs: []int64{1}, includePrivate: false},
		{name: "PrivateRepoAccess", repoIDs: []int64{2}, includePrivate: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			defer tests.PrepareTestEnv(t)()

			timeutil.MockSet(time.Date(2020, 10, 21, 0, 0, 0, 0, time.UTC))
			defer timeutil.MockUnset()

			// The fixtures contain one contribution to private user2/repo2.
			// Add a public contribution in a separate heatmap bucket.
			_, err := db.GetEngine(db.DefaultContext).NoAutoTime().Insert(&activities_model.Action{
				UserID: 2, ActUserID: 2, RepoID: 1,
				OpType:      activities_model.ActionCreateRepo,
				CreatedUnix: 1603231200,
			})
			require.NoError(t, err)

			scopes := []auth_model.AccessTokenScope{auth_model.AccessTokenScopeReadUser, auth_model.AccessTokenScopeReadRepository}
			unrestrictedToken := getUserToken(t, "user2", scopes...)
			req := NewRequest(t, "GET", "/api/v1/users/user2/heatmap").AddTokenAuth(unrestrictedToken)
			resp := MakeRequest(t, req, http.StatusOK)
			var heatmap []*activities_model.UserHeatmapData
			DecodeJSON(t, resp, &heatmap)
			assert.Equal(t, []*activities_model.UserHeatmapData{
				{Timestamp: 1603227600, Contributions: 1},
				{Timestamp: 1603231200, Contributions: 1},
			}, heatmap)

			var token string
			if test.publicOnly {
				token = getTokenForLoggedInUser(t, loginUser(t, "user2"), append(scopes, auth_model.AccessTokenScopePublicOnly)...)
			} else {
				token = createFineGrainedRepoAccessToken(t, "user2", scopes, test.repoIDs)
			}

			req = NewRequest(t, "GET", "/api/v1/users/user2/heatmap").AddTokenAuth(token)
			resp = MakeRequest(t, req, http.StatusOK)
			DecodeJSON(t, resp, &heatmap)
			expected := []*activities_model.UserHeatmapData{{Timestamp: 1603231200, Contributions: 1}}
			if test.includePrivate {
				// Repo-specific tokens retain access to public repositories.
				expected = append([]*activities_model.UserHeatmapData{{Timestamp: 1603227600, Contributions: 1}}, expected...)
			}
			assert.Equal(t, expected, heatmap)
		})
	}
}
