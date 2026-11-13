// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package org

import (
	"context"

	"forgejo.org/models"
	"forgejo.org/models/organization"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"
)

// AddTeamMemberByCooptation adds a user to a team upon invitation of another user
func AddTeamMemberByCooptation(ctx context.Context, team *organization.Team, user, inviter *user_model.User) error {
	_, err := models.InsertTeamMember(ctx, team, user.ID, organization.MembershipReasonAddedByUser, optional.Some(inviter.ID), optional.None[int64]())
	return err
}

// AddTeamMemberByLoginSource adds a user to a team based on some metadata offered by a login source
func AddTeamMemberByLoginSource(ctx context.Context, team *organization.Team, userID, loginSourceID int64) error {
	_, err := models.InsertTeamMember(ctx, team, userID, organization.MembershipReasonAddedByAuthProvider, optional.None[int64](), optional.Some(loginSourceID))
	return err
}
