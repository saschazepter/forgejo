// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package org

import (
	"context"

	org_model "forgejo.org/models/organization"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/services/mailer"
)

// CreateTeamInviteByEmail makes a persistent invite in db for someone without an account and mails it to them.
func CreateTeamInviteByEmail(ctx context.Context, inviter *user_model.User, team *org_model.Team, uname string) error {
	invite, err := org_model.CreateTeamInviteByEmail(ctx, inviter, team, uname)
	if err != nil {
		return err
	}

	return mailer.MailTeamInvite(ctx, inviter, team, invite)
}

// CreateTeamInviteByUser makes a persistent invite in db for someone with an account already and mails it.
func CreateTeamInviteByUser(ctx context.Context, inviter, invited *user_model.User, team *org_model.Team) error {
	invite, err := org_model.CreateTeamInviteForUser(ctx, inviter, invited, team)
	if err != nil {
		return err
	}
	// TODO: instead of only sending an email, also create an in-app notification
	return mailer.MailTeamInvite(ctx, inviter, team, invite)
}

// InviteOrAddTeamMember invites the user to the team if all team changes should go through invites, or adds them directly otherwise.
func InviteOrAddTeamMember(ctx context.Context, inviter, invited *user_model.User, team *org_model.Team) error {
	if setting.Service.AddMembersByInvitations && inviter.ID != invited.ID {
		isAlreadyOrgMember, err := org_model.IsOrganizationMember(ctx, team.OrgID, invited.ID)
		if err != nil {
			return err
		}
		if isAlreadyOrgMember {
			// the user has already consented to being part of the org: we add them to the team directly
			return AddTeamMemberByCooptation(ctx, team, invited, inviter)
		}
		return CreateTeamInviteByUser(ctx, inviter, invited, team)
	}
	return AddTeamMemberByCooptation(ctx, team, invited, inviter)
}

// AcceptInvite adds the doer to the invited team, deleting the invite
func AcceptInvite(ctx context.Context, team *org_model.Team, invite *org_model.TeamInvite, doer *user_model.User, hideMembership bool) error {
	if team.ID != invite.TeamID {
		return org_model.ErrTeamInviteNotFound{}
	}
	linkedToUser, invitedUserID := invite.InvitedID.Get()
	if linkedToUser && invitedUserID != doer.ID {
		return org_model.ErrTeamInviteNotFound{}
	}
	if invite.IsExpired() {
		return org_model.ErrTeamInviteExpired{}
	}
	if err := invite.LoadInviterUser(ctx); err != nil {
		return err
	}
	if err := AddTeamMemberByCooptation(ctx, team, doer, invite.InviterUser); err != nil {
		return err
	}

	if err := org_model.ChangeOrgUserStatus(ctx, team.OrgID, doer.ID, !hideMembership); err != nil {
		return err
	}

	if err := org_model.RemoveInviteByID(ctx, invite.ID, team.ID); err != nil {
		log.Error("RemoveInviteByID: %v", err)
	}
	return nil
}

// DeclineInvite turns down an invitation to a team
func DeclineInvite(ctx context.Context, invite *org_model.TeamInvite) error {
	// TODO: notify the inviter here
	return org_model.RemoveInviteByID(ctx, invite.ID, invite.TeamID)
}
