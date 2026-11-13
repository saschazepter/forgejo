// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package org

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"

	"forgejo.org/models"
	"forgejo.org/models/db"
	org_model "forgejo.org/models/organization"
	"forgejo.org/models/perm"
	repo_model "forgejo.org/models/repo"
	unit_model "forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/base"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/validation"
	"forgejo.org/modules/web"
	shared_user "forgejo.org/routers/web/shared/user"
	"forgejo.org/services/context"
	"forgejo.org/services/convert"
	"forgejo.org/services/forms"
	org_service "forgejo.org/services/org"
	repo_service "forgejo.org/services/repository"
)

const (
	// tplTeams template path for teams list page
	tplTeams base.TplName = "org/team/teams"
	// tplTeamNew template path for create new team page
	tplTeamNew base.TplName = "org/team/new"
	// tplTeamMembers template path for showing team members page
	tplTeamMembers base.TplName = "org/team/members"
	// tplTeamRepositories template path for showing team repositories page
	tplTeamRepositories base.TplName = "org/team/repositories"
	// tplTeamInvite template path for team invites page
	tplTeamInvite base.TplName = "org/team/invite"
)

// Teams render teams list page
func Teams(ctx *context.Context) {
	org := ctx.Org.Organization
	ctx.Data["Title"] = org.FullName
	ctx.Data["PageIsOrgTeams"] = true

	doerIsMember := make(map[int64]bool)
	for _, t := range ctx.Org.Teams {
		if err := t.LoadMembers(ctx); err != nil {
			ctx.ServerError("GetMembers", err)
			return
		}
		// since LoadMembers above isn't paginated, we can compute whether
		// the user is a member simply by looking for them in the returned members.
		// TODO: compute this information via org.GetUserTeams once we limit the number of loaded users, for https://codeberg.org/forgejo/forgejo/issues/12448
		doerIsMember[t.ID] = slices.IndexFunc(t.Members, func(u *user_model.User) bool { return u.ID == ctx.Doer.ID }) != -1
	}
	ctx.Data["Teams"] = ctx.Org.Teams
	ctx.Data["DoerIsMember"] = doerIsMember

	err := shared_user.LoadHeaderCount(ctx)
	if err != nil {
		ctx.ServerError("LoadHeaderCount", err)
		return
	}

	ctx.HTML(http.StatusOK, tplTeams)
}

// TeamsAction response for join, leave, remove, add operations to team
func TeamsAction(ctx *context.Context) {
	page := ctx.FormString("page")
	var err error
	switch ctx.Params(":action") {
	case "join":
		if !ctx.Org.IsOwner {
			ctx.Error(http.StatusNotFound)
			return
		}
		err = org_service.AddTeamMemberByCooptation(ctx, ctx.Org.Team, ctx.Doer, ctx.Doer)
	case "leave":
		err = models.RemoveTeamMember(ctx, ctx.Org.Team, ctx.Doer.ID)
		if err != nil {
			if org_model.IsErrLastOrgOwner(err) {
				ctx.Flash.Error(ctx.Tr("form.last_org_owner"))
			} else {
				log.Error("Action(%s): %v", ctx.Params(":action"), err)
				ctx.JSON(http.StatusOK, map[string]any{
					"ok":  false,
					"err": err.Error(),
				})
				return
			}
		}
		checkIsOrgMemberAndRedirect(ctx, ctx.Org.OrgLink+"/teams/")
		return
	case "remove":
		if !ctx.Org.IsOwner {
			ctx.Error(http.StatusNotFound)
			return
		}

		uid := ctx.FormInt64("uid")
		if uid == 0 {
			ctx.Redirect(ctx.Org.OrgLink + "/teams")
			return
		}

		err = models.RemoveTeamMember(ctx, ctx.Org.Team, uid)
		if err != nil {
			if org_model.IsErrLastOrgOwner(err) {
				ctx.Flash.Error(ctx.Tr("form.last_org_owner"))
			} else {
				log.Error("Action(%s): %v", ctx.Params(":action"), err)
				ctx.JSON(http.StatusOK, map[string]any{
					"ok":  false,
					"err": err.Error(),
				})
				return
			}
		}
		checkIsOrgMemberAndRedirect(ctx, ctx.Org.OrgLink+"/teams/"+url.PathEscape(ctx.Org.Team.LowerName))
		return
	case "add":
		if !ctx.Org.IsOwner {
			ctx.Error(http.StatusNotFound)
			return
		}
		uname := strings.ToLower(ctx.FormString("uname"))
		var u *user_model.User
		u, err = user_model.GetUserByName(ctx, uname)
		if err != nil {
			if user_model.IsErrUserNotExist(err) {
				if setting.MailService != nil && validation.ValidateEmail(uname) == nil {
					if err := org_service.CreateTeamInviteByEmail(ctx, ctx.Doer, ctx.Org.Team, uname); err != nil {
						if org_model.IsErrTeamInviteAlreadyExist(err) {
							ctx.Flash.Error(ctx.Tr("form.duplicate_invite_to_team"))
						} else if org_model.IsErrUserEmailAlreadyAdded(err) {
							ctx.Flash.Error(ctx.Tr("org.teams.add_duplicate_users"))
						} else {
							ctx.ServerError("CreateTeamInvite", err)
							return
						}
					}
				} else {
					ctx.Flash.Error(ctx.Tr("form.user_not_exist"))
				}
				ctx.Redirect(ctx.Org.OrgLink + "/teams/" + url.PathEscape(ctx.Org.Team.LowerName))
			} else {
				ctx.ServerError("GetUserByName", err)
			}
			return
		}

		if u.IsOrganization() {
			ctx.Flash.Error(ctx.Tr("form.cannot_add_org_to_team"))
			ctx.Redirect(ctx.Org.OrgLink + "/teams/" + url.PathEscape(ctx.Org.Team.LowerName))
			return
		}

		if ctx.Org.Team.IsMember(ctx, u.ID) {
			ctx.Flash.Error(ctx.Tr("org.teams.add_duplicate_users"))
		} else {
			err = org_service.InviteOrAddTeamMember(ctx, ctx.Doer, u, ctx.Org.Team)
			if org_model.IsErrTeamInviteAlreadyExist(err) {
				ctx.Flash.Error(ctx.Tr("members.user_already_invited_to_team"))
				ctx.Redirect(ctx.Org.OrgLink + "/teams/" + url.PathEscape(ctx.Org.Team.LowerName))
				return
			} else if err != nil {
				ctx.ServerError("InviteOrAddTeamMember", err)
				return
			}
		}

		page = "team"
	case "remove_invite":
		if !ctx.Org.IsOwner {
			ctx.Error(http.StatusNotFound)
			return
		}

		iid := ctx.FormInt64("iid")
		if iid == 0 {
			ctx.Redirect(ctx.Org.OrgLink + "/teams/" + url.PathEscape(ctx.Org.Team.LowerName))
			return
		}

		if err := org_model.RemoveInviteByID(ctx, iid, ctx.Org.Team.ID); err != nil {
			log.Error("Action(%s): %v", ctx.Params(":action"), err)
			ctx.ServerError("RemoveInviteByID", err)
			return
		}

		page = "team"
	}

	if err != nil {
		if org_model.IsErrLastOrgOwner(err) {
			ctx.Flash.Error(ctx.Tr("form.last_org_owner"))
		} else {
			log.Error("Action(%s): %v", ctx.Params(":action"), err)
			ctx.JSON(http.StatusOK, map[string]any{
				"ok":  false,
				"err": err.Error(),
			})
			return
		}
	}

	switch page {
	case "team":
		ctx.Redirect(ctx.Org.OrgLink + "/teams/" + url.PathEscape(ctx.Org.Team.LowerName))
	case "home":
		ctx.Redirect(ctx.Org.Organization.AsUser().HomeLink())
	default:
		ctx.Redirect(ctx.Org.OrgLink + "/teams")
	}
}

func checkIsOrgMemberAndRedirect(ctx *context.Context, defaultRedirect string) {
	if isOrgMember, err := org_model.IsOrganizationMember(ctx, ctx.Org.Organization.ID, ctx.Doer.ID); err != nil {
		ctx.ServerError("IsOrganizationMember", err)
		return
	} else if !isOrgMember {
		if ctx.Org.Organization.Visibility.IsPrivate() {
			defaultRedirect = setting.AppSubURL + "/"
		} else {
			defaultRedirect = ctx.Org.Organization.HomeLink()
		}
	}
	ctx.JSONRedirect(defaultRedirect)
}

// TeamsRepoAction operate team's repository
func TeamsRepoAction(ctx *context.Context) {
	if !ctx.Org.IsOwner {
		ctx.Error(http.StatusNotFound)
		return
	}

	var err error
	action := ctx.Params(":action")
	switch action {
	case "add":
		repoName := path.Base(ctx.FormString("repo_name"))
		var repo *repo_model.Repository
		repo, err = repo_model.GetRepositoryByName(ctx, ctx.Org.Organization.ID, repoName)
		if err != nil {
			if repo_model.IsErrRepoNotExist(err) {
				ctx.Flash.Error(ctx.Tr("org.teams.add_nonexistent_repo"))
				ctx.Redirect(ctx.Org.OrgLink + "/teams/" + url.PathEscape(ctx.Org.Team.LowerName) + "/repositories")
				return
			}
			ctx.ServerError("GetRepositoryByName", err)
			return
		}
		err = org_service.TeamAddRepository(ctx, ctx.Org.Team, repo)
	case "remove":
		err = repo_service.RemoveRepositoryFromTeam(ctx, ctx.Org.Team, ctx.FormInt64("repoid"))
	case "addall":
		err = models.AddAllRepositories(ctx, ctx.Org.Team)
	case "removeall":
		err = models.RemoveAllRepositories(ctx, ctx.Org.Team)
	}

	if err != nil {
		log.Error("Action(%s): '%s' %v", ctx.Params(":action"), ctx.Org.Team.Name, err)
		ctx.ServerError("TeamsRepoAction", err)
		return
	}

	ctx.Redirect(ctx.Org.OrgLink + "/teams/" + url.PathEscape(ctx.Org.Team.LowerName) + "/repositories")
}

// NewTeam render create new team page
func NewTeam(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Org.Organization.FullName
	ctx.Data["PageIsOrgTeams"] = true
	ctx.Data["PageIsOrgTeamsNew"] = true
	ctx.Data["Team"] = &org_model.Team{}
	ctx.Data["Units"] = unit_model.Units
	if err := shared_user.LoadHeaderCount(ctx); err != nil {
		ctx.ServerError("LoadHeaderCount", err)
		return
	}
	ctx.HTML(http.StatusOK, tplTeamNew)
}

func getUnitPerms(forms url.Values, teamPermission perm.AccessMode) map[unit_model.Type]perm.AccessMode {
	unitPerms := make(map[unit_model.Type]perm.AccessMode)
	for _, ut := range unit_model.AllRepoUnitTypes {
		// Default accessmode is none
		unitPerms[ut] = perm.AccessModeNone

		v, ok := forms[fmt.Sprintf("unit_%d", ut)]
		if ok {
			vv, _ := strconv.Atoi(v[0])
			if teamPermission >= perm.AccessModeAdmin {
				unitPerms[ut] = teamPermission
				// Don't allow `TypeExternal{Tracker,Wiki}` to influence this as they can only be set to READ perms.
				if ut == unit_model.TypeExternalTracker || ut == unit_model.TypeExternalWiki {
					unitPerms[ut] = perm.AccessModeRead
				}
			} else {
				unitPerms[ut] = perm.AccessMode(vv)
				if unitPerms[ut] >= perm.AccessModeAdmin {
					unitPerms[ut] = perm.AccessModeWrite
				}
			}
		}
	}
	return unitPerms
}

// NewTeamPost response for create new team
func NewTeamPost(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.CreateTeamForm)
	includesAllRepositories := form.RepoAccess == "all"
	p := perm.ParseAccessMode(form.Permission)
	unitPerms := getUnitPerms(ctx.Req.Form, p)
	if p < perm.AccessModeAdmin {
		// if p is less than admin accessmode, then it should be general accessmode,
		// so we should calculate the minimal accessmode from units accessmodes.
		p = unit_model.MinUnitAccessMode(unitPerms)
	}

	t := &org_model.Team{
		OrgID:                   ctx.Org.Organization.ID,
		Name:                    form.TeamName,
		Description:             form.Description,
		AccessMode:              p,
		IncludesAllRepositories: includesAllRepositories,
		CanCreateOrgRepo:        form.CanCreateOrgRepo,
	}

	units := make([]*org_model.TeamUnit, 0, len(unitPerms))
	for tp, perm := range unitPerms {
		units = append(units, &org_model.TeamUnit{
			OrgID:      ctx.Org.Organization.ID,
			Type:       tp,
			AccessMode: perm,
		})
	}
	t.Units = units

	ctx.Data["Title"] = ctx.Org.Organization.FullName
	ctx.Data["PageIsOrgTeams"] = true
	ctx.Data["PageIsOrgTeamsNew"] = true
	ctx.Data["Units"] = unit_model.Units
	ctx.Data["Team"] = t

	if ctx.HasError() {
		ctx.HTML(http.StatusOK, tplTeamNew)
		return
	}

	if t.AccessMode < perm.AccessModeAdmin && len(unitPerms) == 0 {
		ctx.RenderWithErr(ctx.Tr("form.team_no_units_error"), tplTeamNew, &form)
		return
	}

	if err := models.NewTeam(ctx, t); err != nil {
		ctx.Data["Err_TeamName"] = true
		switch {
		case org_model.IsErrTeamAlreadyExist(err):
			ctx.RenderWithErr(ctx.Tr("form.team_name_been_taken"), tplTeamNew, &form)
		default:
			ctx.ServerError("NewTeam", err)
		}
		return
	}
	log.Trace("Team created: %s/%s", ctx.Org.Organization.Name, t.Name)
	ctx.Redirect(ctx.Org.OrgLink + "/teams/" + url.PathEscape(t.LowerName))
}

// TeamMembers render team members page
func TeamMembers(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Org.Team.Name
	ctx.Data["PageIsOrgTeams"] = true
	ctx.Data["PageIsOrgTeamMembers"] = true
	ctx.Data["AddMembersByInvitations"] = setting.Service.AddMembersByInvitations

	if err := shared_user.LoadHeaderCount(ctx); err != nil {
		ctx.ServerError("LoadHeaderCount", err)
		return
	}

	page := max(ctx.FormInt("page"), 1)
	total := ctx.Org.Team.NumMembers
	pager := context.NewPagination(total, setting.UI.MembersPagingNum, page, 5)
	opts := db.ListOptions{}
	opts.Page = page
	opts.PageSize = setting.UI.MembersPagingNum

	memberships, err := org_model.GetTeamMemberships(ctx, &org_model.SearchMembersOptions{
		ListOptions: opts,
		TeamID:      ctx.Org.Team.ID,
	})
	if err != nil {
		ctx.ServerError("GetTeamMemberships", err)
		return
	}
	ctx.Data["Memberships"] = memberships
	ctx.Data["Page"] = pager
	ctx.Data["Units"] = unit_model.Units

	invites, err := org_model.GetInvitesByTeamID(ctx, ctx.Org.Team.ID)
	if err != nil {
		ctx.ServerError("GetInvitesByTeamID", err)
		return
	}
	pendingInvites := make([]*org_model.TeamInvite, 0, len(invites))
	expiredInvites := make([]*org_model.TeamInvite, 0, len(invites))
	for _, invite := range invites {
		if invite.LoadInvitedUser(ctx) != nil {
			ctx.ServerError("LoadInvitedUser", err)
			return
		}
		if invite.IsExpired() {
			expiredInvites = append(expiredInvites, invite)
		} else {
			pendingInvites = append(pendingInvites, invite)
		}
	}
	ctx.Data["PendingInvites"] = pendingInvites
	ctx.Data["ExpiredInvites"] = expiredInvites
	ctx.Data["IsEmailInviteEnabled"] = setting.MailService != nil

	ctx.HTML(http.StatusOK, tplTeamMembers)
}

// TeamRepositories show the repositories of team
func TeamRepositories(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Org.Team.Name
	ctx.Data["PageIsOrgTeams"] = true
	ctx.Data["PageIsOrgTeamRepos"] = true

	if err := shared_user.LoadHeaderCount(ctx); err != nil {
		ctx.ServerError("LoadHeaderCount", err)
		return
	}

	page := max(ctx.FormInt("page"), 1)
	total := ctx.Org.Team.NumRepos
	pager := context.NewPagination(total, setting.UI.User.RepoPagingNum, page, 5)
	opts := db.ListOptions{}
	opts.Page = page
	opts.PageSize = setting.UI.User.RepoPagingNum

	if err := ctx.Org.Team.LoadPaginatedRepositories(ctx, opts); err != nil {
		ctx.ServerError("GetRepositories", err)
		return
	}
	ctx.Data["Page"] = pager
	ctx.Data["Units"] = unit_model.Units
	ctx.HTML(http.StatusOK, tplTeamRepositories)
}

// SearchTeam api for searching teams
func SearchTeam(ctx *context.Context) {
	listOptions := db.ListOptions{
		Page:     ctx.FormInt("page"),
		PageSize: convert.ToCorrectPageSize(ctx.FormInt("limit")),
	}

	opts := &org_model.SearchTeamOptions{
		// UserID is not set because the router already requires the doer to be an org admin. Thus, we don't need to restrict to teams that the user belongs in
		Keyword:     ctx.FormTrim("q"),
		OrgID:       ctx.Org.Organization.ID,
		IncludeDesc: ctx.FormString("include_desc") == "" || ctx.FormBool("include_desc"),
		ListOptions: listOptions,
	}

	teams, maxResults, err := org_model.SearchTeam(ctx, opts)
	if err != nil {
		log.Error("SearchTeam failed: %v", err)
		ctx.JSON(http.StatusInternalServerError, map[string]any{
			"ok":    false,
			"error": "SearchTeam internal failure",
		})
		return
	}

	apiTeams, err := convert.ToTeams(ctx, teams, false)
	if err != nil {
		log.Error("convert ToTeams failed: %v", err)
		ctx.JSON(http.StatusInternalServerError, map[string]any{
			"ok":    false,
			"error": "SearchTeam failed to get units",
		})
		return
	}

	ctx.SetTotalCountHeader(maxResults)
	ctx.JSON(http.StatusOK, map[string]any{
		"ok":   true,
		"data": apiTeams,
	})
}

// EditTeam render team edit page
func EditTeam(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Org.Organization.FullName
	ctx.Data["PageIsOrgTeams"] = true
	if err := ctx.Org.Team.LoadUnits(ctx); err != nil {
		ctx.ServerError("LoadUnits", err)
		return
	}
	if err := shared_user.LoadHeaderCount(ctx); err != nil {
		ctx.ServerError("LoadHeaderCount", err)
		return
	}
	ctx.Data["Team"] = ctx.Org.Team
	ctx.Data["Units"] = unit_model.Units
	ctx.HTML(http.StatusOK, tplTeamNew)
}

// EditTeamPost response for modify team information
func EditTeamPost(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.CreateTeamForm)
	t := ctx.Org.Team
	newAccessMode := perm.ParseAccessMode(form.Permission)
	unitPerms := getUnitPerms(ctx.Req.Form, newAccessMode)
	if newAccessMode < perm.AccessModeAdmin {
		// if newAccessMode is less than admin accessmode, then it should be general accessmode,
		// so we should calculate the minimal accessmode from units accessmodes.
		newAccessMode = unit_model.MinUnitAccessMode(unitPerms)
	}
	isAuthChanged := false
	isIncludeAllChanged := false
	includesAllRepositories := form.RepoAccess == "all"

	ctx.Data["Title"] = ctx.Org.Organization.FullName
	ctx.Data["PageIsOrgTeams"] = true
	ctx.Data["Team"] = t
	ctx.Data["Units"] = unit_model.Units

	if !t.IsOwnerTeam() {
		t.Name = form.TeamName
		if t.AccessMode != newAccessMode {
			isAuthChanged = true
			t.AccessMode = newAccessMode
		}

		if t.IncludesAllRepositories != includesAllRepositories {
			isIncludeAllChanged = true
			t.IncludesAllRepositories = includesAllRepositories
		}
		t.CanCreateOrgRepo = form.CanCreateOrgRepo

		units := make([]*org_model.TeamUnit, 0, len(unitPerms))
		for tp, perm := range unitPerms {
			units = append(units, &org_model.TeamUnit{
				OrgID:      t.OrgID,
				TeamID:     t.ID,
				Type:       tp,
				AccessMode: perm,
			})
		}
		t.Units = units
	} else {
		t.CanCreateOrgRepo = true
	}

	t.Description = form.Description

	if ctx.HasError() {
		ctx.HTML(http.StatusOK, tplTeamNew)
		return
	}

	if t.AccessMode < perm.AccessModeAdmin && len(unitPerms) == 0 {
		ctx.RenderWithErr(ctx.Tr("form.team_no_units_error"), tplTeamNew, &form)
		return
	}

	if err := models.UpdateTeam(ctx, t, isAuthChanged, isIncludeAllChanged); err != nil {
		ctx.Data["Err_TeamName"] = true
		switch {
		case org_model.IsErrTeamAlreadyExist(err):
			ctx.RenderWithErr(ctx.Tr("form.team_name_been_taken"), tplTeamNew, &form)
		default:
			ctx.ServerError("UpdateTeam", err)
		}
		return
	}
	ctx.Redirect(ctx.Org.OrgLink + "/teams/" + url.PathEscape(t.LowerName))
}

// DeleteTeam response for the delete team request
func DeleteTeam(ctx *context.Context) {
	if err := models.DeleteTeam(ctx, ctx.Org.Team); err != nil {
		ctx.Flash.Error("DeleteTeam: " + err.Error())
	} else {
		ctx.Flash.Success(ctx.Tr("org.teams.delete_team_success"))
	}

	ctx.JSONRedirect(ctx.Org.OrgLink + "/teams")
}

// TeamInvite renders the team invite page
func TeamInvite(ctx *context.Context) {
	invite, org, team, inviter, err := getTeamInviteFromContext(ctx)
	if err != nil {
		if org_model.IsErrTeamInviteNotFound(err) || org_model.IsErrTeamInviteExpired(err) {
			ctx.NotFound("ErrTeamInviteNotFound", err)
		} else {
			ctx.ServerError("getTeamInviteFromContext", err)
		}
		return
	}

	linkedToUser, invitedUserID := invite.InvitedID.Get()
	if linkedToUser && invitedUserID != ctx.Doer.ID {
		ctx.NotFound("ErrTeamInviteNotFound", nil)
		return
	}

	hiddenMembership, err := org_model.IsPrivateMembership(ctx, org.ID, ctx.Doer.ID)
	if err != nil {
		ctx.ServerError("IsPrivateMembership", err)
		return
	}

	ctx.Data["Title"] = ctx.Tr("org.teams.invite_team_member", team.Name)
	ctx.Data["Invite"] = invite
	ctx.Data["Organization"] = org
	ctx.Data["Team"] = team
	ctx.Data["Inviter"] = inviter
	ctx.Data["HiddenMembership"] = hiddenMembership
	ctx.Data["DeclineURL"] = fmt.Sprintf("/org/invite/%s/decline", invite.Token)

	ctx.HTML(http.StatusOK, tplTeamInvite)
}

// TeamInvitePost accepts the team invitation
func TeamInvitePost(ctx *context.Context) {
	invite, org, team, _, err := getTeamInviteFromContext(ctx)
	if err != nil {
		if org_model.IsErrTeamInviteNotFound(err) || org_model.IsErrTeamInviteExpired(err) {
			ctx.NotFound("ErrTeamInviteNotFound", err)
		} else {
			ctx.ServerError("getTeamInviteFromContext", err)
		}
		return
	}
	hideMembership := ctx.FormBool("hide_membership")

	if err := org_service.AcceptInvite(ctx, team, invite, ctx.Doer, hideMembership); err != nil {
		if org_model.IsErrTeamInviteNotFound(err) || org_model.IsErrTeamInviteExpired(err) {
			ctx.NotFound("ErrTeamInviteNotFound", err)
		} else {
			ctx.ServerError("AcceptInvite", err)
		}
		return
	}

	linkedToUser, invitedUserID := invite.InvitedID.Get()
	// if there is another invite in the same org for the same user, redirect them to that
	if linkedToUser {
		nextInvite, err := org_model.GetInviteByOrgAndUser(ctx, org.ID, invitedUserID)
		if err != nil && !org_model.IsErrTeamInviteNotFound(err) {
			log.Error("GetInviteByOrgAndUser: %v", err)
		}
		if nextInvite != nil {
			ctx.Redirect("/org/invite/" + nextInvite.Token)
			return
		}
	}
	// otherwise redirect them to the page of the team they joined
	ctx.Redirect(org.OrganisationLink() + "/teams/" + url.PathEscape(team.LowerName))
}

// DeclineTeamInvite declines an invitation to a team
func DeclineTeamInvite(ctx *context.Context) {
	invite, org, _, _, err := getTeamInviteFromContext(ctx)
	if err != nil {
		if org_model.IsErrTeamInviteNotFound(err) || org_model.IsErrTeamInviteExpired(err) {
			ctx.NotFound("ErrTeamInviteNotFound", err)
		} else {
			ctx.ServerError("getTeamInviteFromContext", err)
		}
		return
	}

	linkedToUser, invitedUserID := invite.InvitedID.Get()
	if linkedToUser && invitedUserID != ctx.Doer.ID {
		ctx.NotFound("ErrTeamInviteNotFound", nil)
		return
	}

	if err := org_service.DeclineInvite(ctx, invite); err != nil {
		ctx.ServerError("DeclineInvite", err)
		return
	}
	ctx.Flash.Success(ctx.Tr("org.teams.invitation_declined"))

	// if there is another invite in the same org for the same user, redirect them to that
	if linkedToUser {
		nextInvite, err := org_model.GetInviteByOrgAndUser(ctx, org.ID, invitedUserID)
		if err != nil && !org_model.IsErrTeamInviteNotFound(err) {
			log.Error("GetInviteByOrgAndUser: %v", err)
		}
		if nextInvite != nil {
			ctx.Redirect("/org/invite/" + nextInvite.Token)
			return
		}
	}
	// otherwise redirect them to their dashboard (they might not be able to view the org)
	ctx.Redirect(ctx.Doer.DashboardLink())
}

func getTeamInviteFromContext(ctx *context.Context) (*org_model.TeamInvite, *org_model.Organization, *org_model.Team, *user_model.User, error) {
	invite, err := org_model.GetInviteByToken(ctx, ctx.Params("token"))
	if err != nil {
		return nil, nil, nil, nil, err
	}

	if invite.IsExpired() {
		return nil, nil, nil, nil, org_model.ErrTeamInviteExpired{Token: invite.Token}
	}

	inviter, err := user_model.GetUserByID(ctx, invite.InviterID)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	team, err := org_model.GetTeamByID(ctx, invite.TeamID)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	org, err := user_model.GetUserByID(ctx, team.OrgID)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	return invite, org_model.OrgFromUser(org), team, inviter, nil
}
