// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package org

import (
	"testing"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	"forgejo.org/models/organization"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"

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
