// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"testing"

	"forgejo.org/models"
	"forgejo.org/models/auth"
	"forgejo.org/models/db"
	org_model "forgejo.org/models/organization"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"

	"github.com/stretchr/testify/require"
)

func TestDeleteSourceCleansUpMembershipProvenance(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	loginSource := auth.Source{ID: 1, Name: "Keycloak"}
	require.NoError(t, auth.CreateSource(db.DefaultContext, &loginSource))
	user28 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 28})
	team1 := unittest.AssertExistsAndLoadBean(t, &org_model.Team{ID: 1})

	_, err := models.InsertTeamMember(db.DefaultContext, team1, user28.ID, org_model.MembershipReasonAddedByAuthProvider, optional.None[int64](), optional.Some(loginSource.ID))
	require.NoError(t, err)

	require.NoError(t, DeleteSource(db.DefaultContext, &loginSource))

	unittest.AssertExistsAndLoadBean(t, &org_model.TeamUser{UID: 28, TeamID: 1, Reason: org_model.MembershipReasonAddedByAuthProvider, CreatedByLoginSourceID: optional.None[int64]()})
}
