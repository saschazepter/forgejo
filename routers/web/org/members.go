// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2020 The Gitea Authors.
// SPDX-License-Identifier: MIT

package org

import (
	"fmt"
	"net/http"

	"forgejo.org/models/organization"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/base"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	shared_user "forgejo.org/routers/web/shared/user"
	"forgejo.org/services/context"
	org_service "forgejo.org/services/org"
)

const (
	// tplMembers template for organization members page
	tplMembers base.TplName = "org/member/members"
)

// Members render organization users page
func Members(ctx *context.Context) {
	org := ctx.Org.Organization
	ctx.Data["Title"] = org.FullName
	ctx.Data["PageIsOrgMembers"] = true

	page := max(ctx.FormInt("page"), 1)

	opts := &organization.FindOrgMembersOpts{
		Doer:  ctx.Doer,
		OrgID: org.ID,
	}

	if ctx.Doer != nil {
		isMember, err := ctx.Org.Organization.IsOrgMember(ctx, ctx.Doer.ID)
		if err != nil {
			ctx.Error(http.StatusInternalServerError, "IsOrgMember")
			return
		}
		opts.IsDoerMember = isMember
	}
	ctx.Data["PublicOnly"] = opts.PublicOnly()

	total, err := organization.CountOrgMembers(ctx, opts)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "CountOrgMembers")
		return
	}

	err = shared_user.LoadHeaderCount(ctx)
	if err != nil {
		ctx.ServerError("LoadHeaderCount", err)
		return
	}

	pager := context.NewPagination(int(total), setting.UI.MembersPagingNum, page, 5)
	opts.Page = page
	opts.PageSize = setting.UI.MembersPagingNum
	members, membersIsPublic, err := organization.FindOrgMembers(ctx, opts)
	if err != nil {
		ctx.ServerError("GetMembers", err)
		return
	}
	teams, err := organization.FindOrgTeams(ctx, org.ID)
	if err != nil {
		ctx.ServerError("GetTeams", err)
		return
	}
	ctx.Data["Page"] = pager
	ctx.Data["Members"] = members
	ctx.Data["Teams"] = teams
	ctx.Data["MembersIsPublicMember"] = membersIsPublic
	ctx.Data["MembersIsUserOrgOwner"] = organization.IsUserOrgOwner(ctx, members, org.ID)
	ctx.Data["MembersTwoFaStatus"] = members.GetTwoFaStatus(ctx)
	ctx.Data["AddMembersByInvitations"] = setting.Service.AddMembersByInvitations

	ctx.HTML(http.StatusOK, tplMembers)
}

// MembersAction response for operation to a member of organization
func MembersAction(ctx *context.Context) {
	uid := ctx.FormInt64("uid")
	if uid == 0 {
		ctx.Redirect(ctx.Org.OrgLink + "/members")
		return
	}

	org := ctx.Org.Organization
	var err error
	switch ctx.Params(":action") {
	case "private":
		if ctx.Doer.ID != uid && !ctx.Org.IsOwner {
			ctx.Error(http.StatusNotFound)
			return
		}
		err = organization.ChangeOrgUserStatus(ctx, org.ID, uid, false)
	case "public":
		if ctx.Doer.ID != uid && !ctx.Org.IsOwner {
			ctx.Error(http.StatusNotFound)
			return
		}
		err = organization.ChangeOrgUserStatus(ctx, org.ID, uid, true)
	case "remove":
		if !ctx.Org.IsOwner {
			ctx.Error(http.StatusNotFound)
			return
		}
		err = org_service.RemoveOrgUser(ctx, org.ID, uid)
		if organization.IsErrLastOrgOwner(err) {
			ctx.Flash.Error(ctx.Tr("form.last_org_owner"))
			ctx.JSONRedirect(ctx.Org.OrgLink + "/members")
			return
		}
	case "add":
		if !ctx.Org.IsOwner {
			ctx.Error(http.StatusNotFound)
			return
		}
		uname := ctx.FormString("uname")
		var u *user_model.User
		u, err = user_model.GetUserByName(ctx, uname)
		if err != nil {
			ctx.Flash.Error(ctx.Tr("form.user_not_exist"))
			ctx.Redirect(ctx.Org.OrgLink + "/members")
			return
		}

		if u.IsOrganization() {
			ctx.Flash.Error(ctx.Tr("form.cannot_add_org_to_team"))
			ctx.Redirect(ctx.Org.OrgLink + "/members")
			return
		}

		alreadyMember, err := ctx.Org.Organization.IsOrgMember(ctx, u.ID)
		if err != nil {
			ctx.ServerError("IsOrgMember", err)
			return
		}
		if alreadyMember {
			ctx.Flash.Error(ctx.Tr("members.user_already_member"))
			ctx.Redirect(ctx.Org.OrgLink + "/members")
			return
		}
		if setting.Service.AddMembersByInvitations {
			alreadyInvited, err := ctx.Org.Organization.IsInvitedToOrg(ctx, u.ID)
			if err != nil {
				ctx.ServerError("IsInvitedToOrg", err)
				return
			}
			if alreadyInvited {
				ctx.Flash.Error(ctx.Tr("members.user_already_invited_to_org"))
				ctx.Redirect(ctx.Org.OrgLink + "/members")
				return
			}
		}

		teams, err := organization.FindOrgTeams(ctx, org.ID)
		if err != nil {
			ctx.ServerError("GetTeams", err)
			return
		}
		addedToTeam := false
		for _, team := range teams {
			addToTeam := ctx.FormBool(fmt.Sprintf("team_%d", team.ID))
			if addToTeam {
				err = org_service.InviteOrAddTeamMember(ctx, ctx.Doer, u, team)
				if err != nil {
					ctx.ServerError("InviteOrAddTeamMember", err)
					return
				}
				addedToTeam = true
			}
		}

		if !addedToTeam {
			ctx.Flash.Error(ctx.Tr("members.no_team_selected"))
		} else if setting.Service.AddMembersByInvitations {
			ctx.Flash.Info(ctx.Tr("members.successfully_invited"))
		}
		ctx.Redirect(ctx.Org.OrgLink + "/members")
		return
	case "leave":
		err = org_service.RemoveOrgUser(ctx, org.ID, ctx.Doer.ID)
		if err == nil {
			ctx.Flash.Success(ctx.Tr("form.organization_leave_success", org.DisplayName()))
			ctx.JSON(http.StatusOK, map[string]any{
				"redirect": "", // keep the user stay on current page, in case they want to do other operations.
			})
		} else if organization.IsErrLastOrgOwner(err) {
			ctx.Flash.Error(ctx.Tr("form.last_org_owner"))
			ctx.JSONRedirect(ctx.Org.OrgLink + "/members")
		} else {
			log.Error("RemoveOrgUser(%d,%d): %v", org.ID, ctx.Doer.ID, err)
		}
		return
	}

	if err != nil {
		log.Error("Action(%s): %v", ctx.Params(":action"), err)
		ctx.JSON(http.StatusOK, map[string]any{
			"ok":  false,
			"err": err.Error(),
		})
		return
	}

	redirect := ctx.Org.OrgLink + "/members"
	if ctx.Params(":action") == "leave" {
		redirect = setting.AppSubURL + "/"
	}

	ctx.JSONRedirect(redirect)
}
