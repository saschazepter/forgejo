// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package org

import (
	"testing"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	"forgejo.org/models/organization"
	"forgejo.org/models/perm"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddTeamMemberByCooptation(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	testCases := []struct {
		name      string
		teamID    int64
		userID    int64
		inviterID int64
	}{
		{"user already in team", 1, 2, 5},
		{"user not yet in team", 1, 4, 5},
		{"adding a user to a non-owners team", 3, 2, 5},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: testCase.teamID})
			invitedUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: testCase.userID})
			inviterUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: testCase.inviterID})
			require.NoError(t, AddTeamMemberByCooptation(db.DefaultContext, team, invitedUser, inviterUser))
			if testCase.userID == 4 {
				unittest.AssertExistsAndLoadBean(t, &organization.TeamUser{UID: testCase.userID, TeamID: testCase.teamID, Reason: organization.MembershipReasonAddedByUser, CreatedByUserID: optional.Some(testCase.inviterID)})
			} else {
				unittest.AssertExistsAndLoadBean(t, &organization.TeamUser{UID: testCase.userID, TeamID: testCase.teamID})
			}
			unittest.CheckConsistencyFor(t, &organization.Team{ID: testCase.teamID}, &user_model.User{ID: team.OrgID})
		})
	}
}

func TestAddTeamMemberByLoginSource(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	loginSource := auth_model.Source{ID: 1}
	_, err := db.GetEngine(db.DefaultContext).Insert(loginSource)
	require.NoError(t, err)

	testCases := []struct {
		name   string
		teamID int64
		userID int64
	}{
		{"user already in team", 1, 2},
		{"user not yet in team", 1, 4},
		{"adding a user to a non-owners team", 3, 2},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: testCase.teamID})
			require.NoError(t, AddTeamMemberByLoginSource(db.DefaultContext, team, testCase.userID, loginSource.ID))
			unittest.AssertExistsAndLoadBean(t, &organization.TeamUser{UID: testCase.userID, TeamID: testCase.teamID})
			unittest.CheckConsistencyFor(t, &organization.Team{ID: testCase.teamID}, &user_model.User{ID: team.OrgID})
		})
	}
}

func TestTeam_RemoveMember(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	testSuccess := func(teamID, userID int64) {
		team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: teamID})
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: userID})
		require.NoError(t, RemoveTeamMember(db.DefaultContext, team, user, user))
		unittest.AssertNotExistsBean(t, &organization.TeamUser{UID: userID, TeamID: teamID})
		unittest.CheckConsistencyFor(t, &organization.Team{ID: teamID})
	}
	testSuccess(1, 4)
	testSuccess(2, 2)
	testSuccess(3, 2)

	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	err := RemoveTeamMember(db.DefaultContext, team, user2, user2)
	assert.True(t, organization.IsErrLastOrgOwner(err))
}

func TestDeleteTeam(t *testing.T) {
	defer unittest.OverrideFixtures("models/fixtures/TestDeleteTeam")()
	require.NoError(t, unittest.PrepareTestDatabase())

	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 2})
	require.NoError(t, DeleteTeam(db.DefaultContext, team))
	unittest.AssertNotExistsBean(t, &organization.Team{ID: team.ID})
	unittest.AssertNotExistsBean(t, &organization.TeamRepo{TeamID: team.ID})
	unittest.AssertNotExistsBean(t, &organization.TeamUser{TeamID: team.ID})

	// check that team members don't have "leftover" access to repos
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	accessMode, err := access_model.AccessLevel(db.DefaultContext, user, repo)
	require.NoError(t, err)
	assert.Less(t, accessMode, perm.AccessModeWrite)

	// Delete a team that is set `includes_all_repositories: true`, which should still cleanup any access that was
	// granted by this team.  First assert that the permission we're expecting to cleanup exists:
	user = unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 33})
	repo = unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 61})
	unittest.AssertExistsAndLoadBean(t, &access_model.Access{UserID: user.ID, RepoID: repo.ID, Mode: perm.AccessModeWrite})
	// Delete team 100, which includes all repos:
	team = unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 100})
	require.NoError(t, DeleteTeam(db.DefaultContext, team))
	// Validate TeamRepo & Access are removed:
	unittest.AssertNotExistsBean(t, &organization.TeamRepo{TeamID: team.ID})
	unittest.AssertNotExistsBean(t, &access_model.Access{UserID: user.ID, RepoID: repo.ID, Mode: perm.AccessModeWrite})
}
