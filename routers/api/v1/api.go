// Copyright 2015 The Gogs Authors. All rights reserved.
// Copyright 2016 The Gitea Authors. All rights reserved.
// Copyright 2023-2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package v1 Gitea API
//
// This documentation describes the Gitea API.
//
//	Schemes: https, http
//	BasePath: /api/v1
//	Version: {{AppVer | JSEscape}}
//	License: MIT http://opensource.org/licenses/MIT
//
//	Consumes:
//	- application/json
//	- text/plain
//
//	Produces:
//	- application/json
//	- text/html
//
//	Security:
//	- BasicAuth :
//	- AuthorizationHeaderToken :
//	- SudoParam :
//	- SudoHeader :
//	- TOTPHeader :
//
//	SecurityDefinitions:
//	BasicAuth:
//	     type: basic
//	AuthorizationHeaderToken:
//	     type: apiKey
//	     name: Authorization
//	     in: header
//	     description: API tokens must be prepended with "token" followed by a space.
//	SudoParam:
//	     type: apiKey
//	     name: sudo
//	     in: query
//	     description: Sudo API request as the user provided as the key. Admin privileges are required.
//	SudoHeader:
//	     type: apiKey
//	     name: Sudo
//	     in: header
//	     description: Sudo API request as the user provided as the key. Admin privileges are required.
//	TOTPHeader:
//	     type: apiKey
//	     name: X-FORGEJO-OTP
//	     in: header
//	     description: Must be used in combination with BasicAuth if two-factor authentication is enabled.
//
// swagger:meta
package v1

import (
	"fmt"
	"net/http"
	"strings"

	actions_model "forgejo.org/models/actions"
	auth_model "forgejo.org/models/auth"
	issues_model "forgejo.org/models/issues"
	"forgejo.org/models/organization"
	"forgejo.org/models/perm"
	access_model "forgejo.org/models/perm/access"
	quota_model "forgejo.org/models/quota"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/forgefed"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/web"
	"forgejo.org/routers/api/shared"
	"forgejo.org/routers/api/v1/activitypub"
	"forgejo.org/routers/api/v1/admin"
	"forgejo.org/routers/api/v1/misc"
	"forgejo.org/routers/api/v1/notify"
	"forgejo.org/routers/api/v1/org"
	"forgejo.org/routers/api/v1/packages"
	"forgejo.org/routers/api/v1/repo"
	"forgejo.org/routers/api/v1/settings"
	"forgejo.org/routers/api/v1/user"
	"forgejo.org/services/actions"
	"forgejo.org/services/auth"
	"forgejo.org/services/context"
	"forgejo.org/services/forms"

	_ "forgejo.org/routers/api/v1/swagger" // for swagger generation

	"code.forgejo.org/go-chi/binding"
)

func sudo() func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		sudo := ctx.FormString("sudo")
		if len(sudo) == 0 {
			sudo = ctx.Req.Header.Get("Sudo")
		}

		if len(sudo) > 0 {
			if ctx.IsSigned && ctx.Doer.IsAdmin {
				user, err := user_model.GetUserByName(ctx, sudo)
				if err != nil {
					if user_model.IsErrUserNotExist(err) {
						ctx.NotFound()
					} else {
						ctx.Error(http.StatusInternalServerError, "GetUserByName", err)
					}
					return
				}
				log.Trace("Sudo from (%s) to: %s", ctx.Doer.Name, user.Name)
				ctx.Doer = user
			} else {
				ctx.JSON(http.StatusForbidden, map[string]string{
					"message": "Only administrators allowed to sudo.",
				})
				return
			}
		}
	}
}

func repoAssignment() func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		userName := ctx.Params("username")
		repoName := ctx.Params("reponame")

		var (
			owner *user_model.User
			err   error
		)

		// Check if the user is the same as the repository owner.
		if ctx.IsSigned && ctx.Doer.LowerName == strings.ToLower(userName) {
			owner = ctx.Doer
		} else {
			owner, err = user_model.GetUserByName(ctx, userName)
			if err != nil {
				if user_model.IsErrUserNotExist(err) {
					if redirectUserID, err := user_model.LookupUserRedirect(ctx, userName); err == nil {
						context.RedirectToUser(ctx.Base, userName, redirectUserID)
					} else if user_model.IsErrUserRedirectNotExist(err) {
						ctx.NotFound("GetUserByName", err)
					} else {
						ctx.Error(http.StatusInternalServerError, "LookupUserRedirect", err)
					}
				} else {
					ctx.Error(http.StatusInternalServerError, "GetUserByName", err)
				}
				return
			}
		}
		ctx.Repo.Owner = owner
		ctx.ContextUser = owner

		// Get repository.
		repo, err := repo_model.GetRepositoryByName(ctx, owner.ID, repoName)
		if err != nil {
			if repo_model.IsErrRepoNotExist(err) {
				redirectRepoID, err := repo_model.LookupRedirect(ctx, owner.ID, repoName)
				if err == nil {
					context.RedirectToRepo(ctx.Base, redirectRepoID)
				} else if repo_model.IsErrRedirectNotExist(err) {
					ctx.NotFound()
				} else {
					ctx.Error(http.StatusInternalServerError, "LookupRepoRedirect", err)
				}
			} else {
				ctx.Error(http.StatusInternalServerError, "GetRepositoryByName", err)
			}
			return
		}

		repo.Owner = owner
		ctx.Repo.Repository = repo

		if ctx.Doer != nil && ctx.Doer.ID == user_model.ActionsUserID {
			taskID := ctx.Data["ActionsTaskID"].(int64)
			task, err := actions_model.GetTaskByID(ctx, taskID)
			if err != nil {
				ctx.Error(http.StatusInternalServerError, "actions_model.GetTaskByID", err)
				return
			}
			if task.RepoID != repo.ID {
				ctx.NotFound()
				return
			}

			if task.IsForkPullRequest {
				ctx.Repo.AccessMode = perm.AccessModeRead
			} else {
				ctx.Repo.AccessMode = perm.AccessModeWrite
			}

			if err := ctx.Repo.Repository.LoadUnits(ctx); err != nil {
				ctx.Error(http.StatusInternalServerError, "LoadUnits", err)
				return
			}
			ctx.Repo.Units = ctx.Repo.Repository.Units
			ctx.Repo.UnitsMode = make(map[unit.Type]perm.AccessMode)
			for _, u := range ctx.Repo.Repository.Units {
				ctx.Repo.UnitsMode[u.Type] = ctx.Repo.AccessMode
			}
		} else {
			ctx.Repo.Permission, err = access_model.GetUserRepoPermission(ctx, repo, ctx.Doer)
			if err != nil {
				ctx.Error(http.StatusInternalServerError, "GetUserRepoPermission", err)
				return
			}
		}

		if !ctx.Repo.HasAccess() {
			ctx.NotFound()
			return
		}
	}
}

// must be used within a group with a call to repoAssignment() to set ctx.Repo
func commentAssignment(idParam string) func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		comment, err := issues_model.GetCommentByID(ctx, ctx.ParamsInt64(idParam))
		if err != nil {
			if issues_model.IsErrCommentNotExist(err) {
				ctx.NotFound(err)
			} else {
				ctx.InternalServerError(err)
			}
			return
		}

		if err = comment.LoadIssue(ctx); err != nil {
			ctx.InternalServerError(err)
			return
		}
		if comment.Issue == nil || comment.Issue.RepoID != ctx.Repo.Repository.ID {
			ctx.NotFound()
			return
		}

		if !ctx.Repo.CanReadIssuesOrPulls(comment.Issue.IsPull) {
			ctx.NotFound()
			return
		}

		comment.Issue.Repo = ctx.Repo.Repository

		ctx.Comment = comment
	}
}

func reqPackageAccess(accessMode perm.AccessMode) func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		if ctx.Package.AccessMode < accessMode && !ctx.IsUserSiteAdmin() {
			ctx.Error(http.StatusForbidden, "reqPackageAccess", "user should have specific permission or be a site admin")
			return
		}
	}
}

func checkTokenPublicOnly() func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		if !ctx.PublicOnly {
			return
		}

		requiredScopeCategories, ok := ctx.Data["requiredScopeCategories"].([]auth_model.AccessTokenScopeCategory)
		if !ok || len(requiredScopeCategories) == 0 {
			return
		}

		// public Only permission check
		switch {
		case auth_model.ContainsCategory(requiredScopeCategories, auth_model.AccessTokenScopeCategoryRepository):
			if ctx.Repo.Repository != nil && ctx.Repo.Repository.IsPrivate {
				ctx.Error(http.StatusForbidden, "reqToken", "token scope is limited to public repos")
				return
			}
		case auth_model.ContainsCategory(requiredScopeCategories, auth_model.AccessTokenScopeCategoryIssue):
			if ctx.Repo.Repository != nil && ctx.Repo.Repository.IsPrivate {
				ctx.Error(http.StatusForbidden, "reqToken", "token scope is limited to public issues")
				return
			}
		case auth_model.ContainsCategory(requiredScopeCategories, auth_model.AccessTokenScopeCategoryOrganization):
			if ctx.Org.Organization != nil && ctx.Org.Organization.Visibility != api.VisibleTypePublic {
				ctx.Error(http.StatusForbidden, "reqToken", "token scope is limited to public orgs")
				return
			}
			if ctx.ContextUser != nil && ctx.ContextUser.IsOrganization() && ctx.ContextUser.Visibility != api.VisibleTypePublic {
				ctx.Error(http.StatusForbidden, "reqToken", "token scope is limited to public orgs")
				return
			}
		case auth_model.ContainsCategory(requiredScopeCategories, auth_model.AccessTokenScopeCategoryUser):
			if ctx.ContextUser != nil && ctx.ContextUser.IsUser() && ctx.ContextUser.Visibility != api.VisibleTypePublic {
				ctx.Error(http.StatusForbidden, "reqToken", "token scope is limited to public users")
				return
			}
		case auth_model.ContainsCategory(requiredScopeCategories, auth_model.AccessTokenScopeCategoryActivityPub):
			if ctx.ContextUser != nil && ctx.ContextUser.IsUser() && ctx.ContextUser.Visibility != api.VisibleTypePublic {
				ctx.Error(http.StatusForbidden, "reqToken", "token scope is limited to public activitypub")
				return
			}
		case auth_model.ContainsCategory(requiredScopeCategories, auth_model.AccessTokenScopeCategoryNotification):
			if ctx.Repo.Repository != nil && ctx.Repo.Repository.IsPrivate {
				ctx.Error(http.StatusForbidden, "reqToken", "token scope is limited to public notifications")
				return
			}
		case auth_model.ContainsCategory(requiredScopeCategories, auth_model.AccessTokenScopeCategoryPackage):
			if ctx.Package != nil && ctx.Package.Owner.Visibility.IsPrivate() {
				ctx.Error(http.StatusForbidden, "reqToken", "token scope is limited to public packages")
				return
			}
		}
	}
}

// if a token is being used for auth, we check that it contains the required scope
// if a token is not being used, reqToken will enforce other sign in methods
func tokenRequiresScopes(requiredScopeCategories ...auth_model.AccessTokenScopeCategory) func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		// no scope required
		if len(requiredScopeCategories) == 0 {
			return
		}

		// Need OAuth2 token to be present.
		scope, scopeExists := ctx.Data["ApiTokenScope"].(auth_model.AccessTokenScope)
		if ctx.Data["IsApiToken"] != true || !scopeExists {
			return
		}

		// use the http method to determine the access level
		requiredScopeLevel := auth_model.Read
		if ctx.Req.Method == "POST" || ctx.Req.Method == "PUT" || ctx.Req.Method == "PATCH" || ctx.Req.Method == "DELETE" {
			requiredScopeLevel = auth_model.Write
		}

		// get the required scope for the given access level and category
		requiredScopes := auth_model.GetRequiredScopes(requiredScopeLevel, requiredScopeCategories...)
		allow, err := scope.HasScope(requiredScopes...)
		if err != nil {
			ctx.Error(http.StatusForbidden, "tokenRequiresScope", "checking scope failed: "+err.Error())
			return
		}

		if !allow {
			ctx.Error(http.StatusForbidden, "tokenRequiresScope", fmt.Sprintf("token does not have at least one of required scope(s): %v", requiredScopes))
			return
		}

		ctx.Data["requiredScopeCategories"] = requiredScopeCategories

		// check if scope only applies to public resources
		publicOnly, err := scope.PublicOnly()
		if err != nil {
			ctx.Error(http.StatusForbidden, "tokenRequiresScope", "parsing public resource scope failed: "+err.Error())
			return
		}

		// assign to true so that those searching should only filter public repositories/users/organizations
		ctx.PublicOnly = publicOnly
	}
}

// Contexter middleware already checks token for user sign in process.
func reqToken() func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		// If actions token is present
		if true == ctx.Data["IsActionsToken"] {
			return
		}

		if ctx.IsSigned {
			return
		}
		ctx.Error(http.StatusUnauthorized, "reqToken", "token is required")
	}
}

func reqExploreSignIn() func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		if (setting.Service.RequireSignInView || setting.Service.Explore.RequireSigninView) && !ctx.IsSigned {
			ctx.Error(http.StatusUnauthorized, "reqExploreSignIn", "you must be signed in to search for users")
		}
	}
}

func reqUsersExploreEnabled() func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		if setting.Service.Explore.DisableUsersPage {
			ctx.NotFound()
		}
	}
}

func reqBasicOrRevProxyAuth() func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		if ctx.IsSigned && setting.Service.EnableReverseProxyAuthAPI && ctx.Data["AuthedMethod"].(string) == auth.ReverseProxyMethodName {
			return
		}
		if !ctx.IsBasicAuth {
			ctx.Error(http.StatusUnauthorized, "reqBasicAuth", "auth required")
			return
		}
	}
}

// reqSiteAdmin user should be the site admin
func reqSiteAdmin() func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		if !ctx.IsUserSiteAdmin() {
			ctx.Error(http.StatusForbidden, "reqSiteAdmin", "user should be the site admin")
			return
		}
	}
}

// reqOwner user should be the owner of the repo or site admin.
func reqOwner() func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		if !ctx.Repo.IsOwner() && !ctx.IsUserSiteAdmin() {
			ctx.Error(http.StatusForbidden, "reqOwner", "user should be the owner of the repo")
			return
		}
	}
}

// reqSelfOrAdmin doer should be the same as the contextUser or site admin
func reqSelfOrAdmin() func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		if !ctx.IsUserSiteAdmin() && ctx.ContextUser != ctx.Doer {
			ctx.Error(http.StatusForbidden, "reqSelfOrAdmin", "doer should be the site admin or be same as the contextUser")
			return
		}
	}
}

// reqAdmin user should be an owner or a collaborator with admin write of a repository, or site admin
func reqAdmin() func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		if !ctx.IsUserRepoAdmin() && !ctx.IsUserSiteAdmin() {
			ctx.Error(http.StatusForbidden, "reqAdmin", "user should be an owner or a collaborator with admin write of a repository")
			return
		}
	}
}

// reqRepoWriter user should have a permission to write to a repo, or be a site admin
func reqRepoWriter(unitTypes ...unit.Type) func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		if !ctx.IsUserRepoWriter(unitTypes) && !ctx.IsUserRepoAdmin() && !ctx.IsUserSiteAdmin() {
			ctx.Error(http.StatusForbidden, "reqRepoWriter", "user should have a permission to write to a repo")
			return
		}
	}
}

// reqRepoBranchWriter user should have a permission to write to a branch, or be a site admin
func reqRepoBranchWriter(ctx *context.APIContext) {
	options, ok := web.GetForm(ctx).(api.FileOptionInterface)
	if !ok || (!ctx.Repo.CanWriteToBranch(ctx, ctx.Doer, options.Branch()) && !ctx.IsUserSiteAdmin()) {
		ctx.Error(http.StatusForbidden, "reqRepoBranchWriter", "user should have a permission to write to this branch")
		return
	}
}

// reqRepoReader user should have specific read permission or be a repo admin or a site admin
func reqRepoReader(unitType unit.Type) func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		if !ctx.Repo.CanRead(unitType) && !ctx.IsUserRepoAdmin() && !ctx.IsUserSiteAdmin() {
			ctx.Error(http.StatusForbidden, "reqRepoReader", "user should have specific read permission or be a repo admin or a site admin")
			return
		}
	}
}

// reqAnyRepoReader user should have any permission to read repository or permissions of site admin
func reqAnyRepoReader() func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		if !ctx.Repo.HasAccess() && !ctx.IsUserSiteAdmin() {
			ctx.Error(http.StatusForbidden, "reqAnyRepoReader", "user should have any permission to read repository or permissions of site admin")
			return
		}
	}
}

// reqOrgOwnership user should be an organization owner, or a site admin
func reqOrgOwnership() func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		if ctx.IsUserSiteAdmin() {
			return
		}

		var orgID int64
		if ctx.Org.Organization != nil {
			orgID = ctx.Org.Organization.ID
		} else if ctx.Org.Team != nil {
			orgID = ctx.Org.Team.OrgID
		} else {
			ctx.Error(http.StatusInternalServerError, "", "reqOrgOwnership: unprepared context")
			return
		}

		isOwner, err := organization.IsOrganizationOwner(ctx, orgID, ctx.Doer.ID)
		if err != nil {
			ctx.Error(http.StatusInternalServerError, "IsOrganizationOwner", err)
			return
		} else if !isOwner {
			if ctx.Org.Organization != nil {
				ctx.Error(http.StatusForbidden, "", "Must be an organization owner")
			} else {
				ctx.NotFound()
			}
			return
		}
	}
}

// reqTeamMembership user should be an team member, or a site admin
func reqTeamMembership() func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		if ctx.IsUserSiteAdmin() {
			return
		}
		if ctx.Org.Team == nil {
			ctx.Error(http.StatusInternalServerError, "", "reqTeamMembership: unprepared context")
			return
		}

		orgID := ctx.Org.Team.OrgID
		isOwner, err := organization.IsOrganizationOwner(ctx, orgID, ctx.Doer.ID)
		if err != nil {
			ctx.Error(http.StatusInternalServerError, "IsOrganizationOwner", err)
			return
		} else if isOwner {
			return
		}

		if isTeamMember, err := organization.IsTeamMember(ctx, orgID, ctx.Org.Team.ID, ctx.Doer.ID); err != nil {
			ctx.Error(http.StatusInternalServerError, "IsTeamMember", err)
			return
		} else if !isTeamMember {
			isOrgMember, err := organization.IsOrganizationMember(ctx, orgID, ctx.Doer.ID)
			if err != nil {
				ctx.Error(http.StatusInternalServerError, "IsOrganizationMember", err)
			} else if isOrgMember {
				ctx.Error(http.StatusForbidden, "", "Must be a team member")
			} else {
				ctx.NotFound()
			}
			return
		}
	}
}

// reqOrgMembership user should be an organization member, or a site admin
func reqOrgMembership() func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		if ctx.IsUserSiteAdmin() {
			return
		}

		var orgID int64
		if ctx.Org.Organization != nil {
			orgID = ctx.Org.Organization.ID
		} else if ctx.Org.Team != nil {
			orgID = ctx.Org.Team.OrgID
		} else {
			ctx.Error(http.StatusInternalServerError, "", "reqOrgMembership: unprepared context")
			return
		}

		if isMember, err := organization.IsOrganizationMember(ctx, orgID, ctx.Doer.ID); err != nil {
			ctx.Error(http.StatusInternalServerError, "IsOrganizationMember", err)
			return
		} else if !isMember {
			if ctx.Org.Organization != nil {
				ctx.Error(http.StatusForbidden, "", "Must be an organization member")
			} else {
				ctx.NotFound()
			}
			return
		}
	}
}

func reqGitHook() func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		if !ctx.Doer.CanEditGitHook() {
			ctx.Error(http.StatusForbidden, "", "must be allowed to edit Git hooks")
			return
		}
	}
}

// reqWebhooksEnabled requires webhooks to be enabled by admin.
func reqWebhooksEnabled() func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		if setting.DisableWebhooks {
			ctx.Error(http.StatusForbidden, "", "webhooks disabled by administrator")
			return
		}
	}
}

func orgAssignment(args ...bool) func(ctx *context.APIContext) {
	var (
		assignOrg  bool
		assignTeam bool
	)
	if len(args) > 0 {
		assignOrg = args[0]
	}
	if len(args) > 1 {
		assignTeam = args[1]
	}
	return func(ctx *context.APIContext) {
		ctx.Org = new(context.APIOrganization)

		var err error
		if assignOrg {
			ctx.Org.Organization, err = organization.GetOrgByName(ctx, ctx.Params(":org"))
			if err != nil {
				if organization.IsErrOrgNotExist(err) {
					redirectUserID, err := user_model.LookupUserRedirect(ctx, ctx.Params(":org"))
					if err == nil {
						context.RedirectToUser(ctx.Base, ctx.Params(":org"), redirectUserID)
					} else if user_model.IsErrUserRedirectNotExist(err) {
						ctx.NotFound("GetOrgByName", err)
					} else {
						ctx.Error(http.StatusInternalServerError, "LookupUserRedirect", err)
					}
				} else {
					ctx.Error(http.StatusInternalServerError, "GetOrgByName", err)
				}
				return
			}
			ctx.ContextUser = ctx.Org.Organization.AsUser()
		}

		if assignTeam {
			ctx.Org.Team, err = organization.GetTeamByID(ctx, ctx.ParamsInt64(":teamid"))
			if err != nil {
				if organization.IsErrTeamNotExist(err) {
					ctx.NotFound()
				} else {
					ctx.Error(http.StatusInternalServerError, "GetTeamById", err)
				}
				return
			}
		}
	}
}

func mustEnableIssues(ctx *context.APIContext) {
	if !ctx.Repo.CanRead(unit.TypeIssues) {
		if log.IsTrace() {
			if ctx.IsSigned {
				log.Trace("Permission Denied: User %-v cannot read %-v in Repo %-v\n"+
					"User in Repo has Permissions: %-+v",
					ctx.Doer,
					unit.TypeIssues,
					ctx.Repo.Repository,
					ctx.Repo.Permission)
			} else {
				log.Trace("Permission Denied: Anonymous user cannot read %-v in Repo %-v\n"+
					"Anonymous user in Repo has Permissions: %-+v",
					unit.TypeIssues,
					ctx.Repo.Repository,
					ctx.Repo.Permission)
			}
		}
		ctx.NotFound()
		return
	}
}

func mustAllowPulls(ctx *context.APIContext) {
	if !ctx.Repo.Repository.CanEnablePulls() || !ctx.Repo.CanRead(unit.TypePullRequests) {
		if ctx.Repo.Repository.CanEnablePulls() && log.IsTrace() {
			if ctx.IsSigned {
				log.Trace("Permission Denied: User %-v cannot read %-v in Repo %-v\n"+
					"User in Repo has Permissions: %-+v",
					ctx.Doer,
					unit.TypePullRequests,
					ctx.Repo.Repository,
					ctx.Repo.Permission)
			} else {
				log.Trace("Permission Denied: Anonymous user cannot read %-v in Repo %-v\n"+
					"Anonymous user in Repo has Permissions: %-+v",
					unit.TypePullRequests,
					ctx.Repo.Repository,
					ctx.Repo.Permission)
			}
		}
		ctx.NotFound()
		return
	}
}

func mustEnableIssuesOrPulls(ctx *context.APIContext) {
	if !ctx.Repo.CanRead(unit.TypeIssues) &&
		(!ctx.Repo.Repository.CanEnablePulls() || !ctx.Repo.CanRead(unit.TypePullRequests)) {
		if ctx.Repo.Repository.CanEnablePulls() && log.IsTrace() {
			if ctx.IsSigned {
				log.Trace("Permission Denied: User %-v cannot read %-v and %-v in Repo %-v\n"+
					"User in Repo has Permissions: %-+v",
					ctx.Doer,
					unit.TypeIssues,
					unit.TypePullRequests,
					ctx.Repo.Repository,
					ctx.Repo.Permission)
			} else {
				log.Trace("Permission Denied: Anonymous user cannot read %-v and %-v in Repo %-v\n"+
					"Anonymous user in Repo has Permissions: %-+v",
					unit.TypeIssues,
					unit.TypePullRequests,
					ctx.Repo.Repository,
					ctx.Repo.Permission)
			}
		}
		ctx.NotFound()
		return
	}
}

func mustEnableWiki(ctx *context.APIContext) {
	if !(ctx.Repo.CanRead(unit.TypeWiki)) {
		ctx.NotFound()
		return
	}
}

func mustNotBeArchived(ctx *context.APIContext) {
	if ctx.Repo.Repository.IsArchived {
		ctx.Error(http.StatusLocked, "RepoArchived", fmt.Errorf("%s is archived", ctx.Repo.Repository.LogString()))
		return
	}
}

func mustEnableAttachments(ctx *context.APIContext) {
	if !setting.Attachment.Enabled {
		ctx.NotFound()
		return
	}
}

// bind binding an obj to a func(ctx *context.APIContext)
func bind[T any](_ T) any {
	return func(ctx *context.APIContext) {
		theObj := new(T) // create a new form obj for every request but not use obj directly
		errs := binding.Bind(ctx.Req, theObj)
		if len(errs) > 0 {
			ctx.Error(http.StatusUnprocessableEntity, "validationError", fmt.Sprintf("%s: %s", errs[0].FieldNames, errs[0].Error()))
			return
		}
		web.SetForm(ctx, theObj)
	}
}

func individualPermsChecker(ctx *context.APIContext) {
	// org permissions have been checked in context.OrgAssignment(), but individual permissions haven't been checked.
	if ctx.ContextUser.IsIndividual() {
		switch ctx.ContextUser.Visibility {
		case api.VisibleTypePrivate:
			if ctx.Doer == nil || (ctx.ContextUser.ID != ctx.Doer.ID && !ctx.Doer.IsAdmin) {
				ctx.NotFound("Visit Project", nil)
				return
			}
		case api.VisibleTypeLimited:
			if ctx.Doer == nil {
				ctx.NotFound("Visit Project", nil)
				return
			}
		}
	}
}

// Routes registers all v1 APIs routes to web application.
func Routes() *web.Route {
	m := web.NewRoute()

	m.Use(shared.Middlewares()...)

	addActionsRoutes := func(
		m *web.Route,
		reqChecker func(ctx *context.APIContext),
		act actions.API,
	) {
		m.Group("/actions", func() {
			m.Group("/secrets", func() {
				m.Get("", reqToken(), reqChecker, act.ListActionsSecrets)
				m.Combo("/{secretname}").
					Put(reqToken(), reqChecker, bind(api.CreateOrUpdateSecretOption{}), act.CreateOrUpdateSecret).
					Delete(reqToken(), reqChecker, act.DeleteSecret)
			})

			m.Group("/variables", func() {
				m.Get("", reqToken(), reqChecker, act.ListVariables)
				m.Combo("/{variablename}").
					Get(reqToken(), reqChecker, act.GetVariable).
					Delete(reqToken(), reqChecker, act.DeleteVariable).
					Post(reqToken(), reqChecker, bind(api.CreateVariableOption{}), act.CreateVariable).
					Put(reqToken(), reqChecker, bind(api.UpdateVariableOption{}), act.UpdateVariable)
			})

			m.Group("/runners", func() {
				m.Get("/registration-token", reqToken(), reqChecker, act.GetRegistrationToken)
				m.Get("/jobs", reqToken(), reqChecker, act.SearchActionRunJobs)
			})
		})
	}

	m.Group("", func() {
		// Miscellaneous (no scope required)
		if setting.API.EnableSwagger {
			m.Get("/swagger", func(ctx *context.APIContext) {
				ctx.Redirect(setting.AppSubURL + "/api/swagger")
			})
		}

		if setting.Federation.Enabled {
			m.Get("/nodeinfo", misc.NodeInfo)
			m.Group("/activitypub", func() {
				// deprecated, remove in 1.20, use /user-id/{user-id} instead
				m.Group("/user/{username}", func() {
					m.Get("", activitypub.ReqHTTPSignature(), activitypub.Person)
					m.Post("/inbox", activitypub.ReqHTTPSignature(), activitypub.PersonInbox)
				}, context.UserAssignmentAPI(), checkTokenPublicOnly())
				m.Group("/user-id/{user-id}", func() {
					m.Get("", activitypub.ReqHTTPSignature(), activitypub.Person)
					m.Post("/inbox", activitypub.ReqHTTPSignature(), activitypub.PersonInbox)
				}, context.UserIDAssignmentAPI(), checkTokenPublicOnly())
				m.Group("/actor", func() {
					m.Get("", activitypub.Actor)
					m.Post("/inbox", activitypub.ReqHTTPSignature(), activitypub.ActorInbox)
				})
				m.Group("/repository-id/{repository-id}", func() {
					m.Get("", activitypub.ReqHTTPSignature(), activitypub.Repository)
					m.Post("/inbox",
						bind(forgefed.ForgeLike{}),
						activitypub.ReqHTTPSignature(),
						activitypub.RepositoryInbox)
				}, context.RepositoryIDAssignmentAPI())
			}, tokenRequiresScopes(auth_model.AccessTokenScopeCategoryActivityPub))
		}

		// Misc (public accessible)
		m.Group("", func() {
			m.Get("/version", misc.Version)
			m.Get("/signing-key.gpg", misc.SigningKey)
			m.Get("/signing-key.ssh", misc.SSHSigningKey)
			m.Post("/markup", reqToken(), bind(api.MarkupOption{}), misc.Markup)
			m.Post("/markdown", reqToken(), bind(api.MarkdownOption{}), misc.Markdown)
			m.Post("/markdown/raw", reqToken(), misc.MarkdownRaw)
			m.Get("/gitignore/templates", misc.ListGitignoresTemplates)
			m.Get("/gitignore/templates/{name}", misc.GetGitignoreTemplateInfo)
			m.Get("/licenses", misc.ListLicenseTemplates)
			m.Get("/licenses/{name}", misc.GetLicenseTemplateInfo)
			m.Get("/label/templates", misc.ListLabelTemplates)
			m.Get("/label/templates/{name}", misc.GetLabelTemplate)

			m.Group("/settings", func() {
				m.Get("/ui", settings.GetGeneralUISettings)
				m.Get("/api", settings.GetGeneralAPISettings)
				m.Get("/attachment", settings.GetGeneralAttachmentSettings)
				m.Get("/repository", settings.GetGeneralRepoSettings)
			})
		})

		// Notifications (requires 'notifications' scope)
		m.Group("/notifications", func() {
			m.Combo("").
				Get(reqToken(), notify.ListNotifications).
				Put(reqToken(), notify.ReadNotifications)
			m.Get("/new", reqToken(), notify.NewAvailable)
			m.Combo("/threads/{id}").
				Get(reqToken(), notify.GetThread).
				Patch(reqToken(), notify.ReadThread)
		}, tokenRequiresScopes(auth_model.AccessTokenScopeCategoryNotification))

		// Users (requires user scope)
		m.Group("/users", func() {
			m.Get("/search", reqExploreSignIn(), reqUsersExploreEnabled(), user.Search)

			m.Group("/{username}", func() {
				m.Get("", reqExploreSignIn(), user.GetInfo)

				if setting.Service.EnableUserHeatmap {
					m.Get("/heatmap", user.GetUserHeatmapData)
				}

				m.Get("/repos", tokenRequiresScopes(auth_model.AccessTokenScopeCategoryRepository), reqExploreSignIn(), user.ListUserRepos)
				m.Group("/tokens", func() {
					m.Combo("").Get(user.ListAccessTokens).
						Post(bind(api.CreateAccessTokenOption{}), reqBasicOrRevProxyAuth(), reqToken(), user.CreateAccessToken)
					m.Combo("/{id}").Delete(reqBasicOrRevProxyAuth(), reqToken(), user.DeleteAccessToken)
				}, reqSelfOrAdmin())

				m.Get("/activities/feeds", user.ListUserActivityFeeds)
			}, context.UserAssignmentAPI(), checkTokenPublicOnly(), individualPermsChecker)
		}, tokenRequiresScopes(auth_model.AccessTokenScopeCategoryUser))

		// Users (requires user scope)
		m.Group("/users", func() {
			m.Group("/{username}", func() {
				m.Get("/keys", user.ListPublicKeys)
				m.Get("/gpg_keys", user.ListGPGKeys)

				m.Get("/followers", user.ListFollowers)
				m.Group("/following", func() {
					m.Get("", user.ListFollowing)
					m.Get("/{target}", user.CheckFollowing)
				})

				if !setting.Repository.DisableStars {
					m.Get("/starred", user.GetStarredRepos)
				}

				m.Get("/subscriptions", user.GetWatchedRepos)
			}, context.UserAssignmentAPI(), checkTokenPublicOnly())
		}, tokenRequiresScopes(auth_model.AccessTokenScopeCategoryUser), reqToken())

		// Users (requires user scope)
		m.Group("/user", func() {
			m.Get("", user.GetAuthenticatedUser)
			if setting.Quota.Enabled {
				m.Group("/quota", func() {
					m.Get("", user.GetQuota)
					m.Get("/check", user.CheckQuota)
					m.Get("/attachments", user.ListQuotaAttachments)
					m.Get("/packages", user.ListQuotaPackages)
					m.Get("/artifacts", user.ListQuotaArtifacts)
				})
			}
			m.Group("/settings", func() {
				m.Get("", user.GetUserSettings)
				m.Patch("", bind(api.UserSettingsOptions{}), user.UpdateUserSettings)
			}, reqToken())
			m.Combo("/emails").
				Get(user.ListEmails).
				Post(bind(api.CreateEmailOption{}), user.AddEmail).
				Delete(bind(api.DeleteEmailOption{}), user.DeleteEmail)

			// manage user-level actions features
			m.Group("/actions", func() {
				m.Group("/secrets", func() {
					m.Combo("/{secretname}").
						Put(bind(api.CreateOrUpdateSecretOption{}), user.CreateOrUpdateSecret).
						Delete(user.DeleteSecret)
				})

				m.Group("/variables", func() {
					m.Get("", user.ListVariables)
					m.Combo("/{variablename}").
						Get(user.GetVariable).
						Delete(user.DeleteVariable).
						Post(bind(api.CreateVariableOption{}), user.CreateVariable).
						Put(bind(api.UpdateVariableOption{}), user.UpdateVariable)
				})

				m.Group("/runners", func() {
					m.Get("/registration-token", reqToken(), user.GetRegistrationToken)
					m.Get("/jobs", reqToken(), user.SearchActionRunJobs)
				})
			})

			m.Get("/followers", user.ListMyFollowers)
			m.Group("/following", func() {
				m.Get("", user.ListMyFollowing)
				m.Group("/{username}", func() {
					m.Get("", user.CheckMyFollowing)
					m.Put("", user.Follow)
					m.Delete("", user.Unfollow)
				}, context.UserAssignmentAPI())
			})

			// (admin:public_key scope)
			m.Group("/keys", func() {
				m.Combo("").Get(user.ListMyPublicKeys).
					Post(bind(api.CreateKeyOption{}), user.CreatePublicKey)
				m.Combo("/{id}").Get(user.GetPublicKey).
					Delete(user.DeletePublicKey)
			})

			// (admin:application scope)
			m.Group("/applications", func() {
				m.Combo("/oauth2").
					Get(user.ListOauth2Applications).
					Post(bind(api.CreateOAuth2ApplicationOptions{}), user.CreateOauth2Application)
				m.Combo("/oauth2/{id}").
					Delete(user.DeleteOauth2Application).
					Patch(bind(api.CreateOAuth2ApplicationOptions{}), user.UpdateOauth2Application).
					Get(user.GetOauth2Application)
			})

			// (admin:gpg_key scope)
			m.Group("/gpg_keys", func() {
				m.Combo("").Get(user.ListMyGPGKeys).
					Post(bind(api.CreateGPGKeyOption{}), user.CreateGPGKey)
				m.Combo("/{id}").Get(user.GetGPGKey).
					Delete(user.DeleteGPGKey)
			})
			m.Get("/gpg_key_token", user.GetVerificationToken)
			m.Post("/gpg_key_verify", bind(api.VerifyGPGKeyOption{}), user.VerifyUserGPGKey)

			// (repo scope)
			m.Combo("/repos", tokenRequiresScopes(auth_model.AccessTokenScopeCategoryRepository)).Get(user.ListMyRepos).
				Post(bind(api.CreateRepoOption{}), context.EnforceQuotaAPI(quota_model.LimitSubjectSizeReposAll, context.QuotaTargetUser), repo.Create)

			// (repo scope)
			if !setting.Repository.DisableStars {
				m.Group("/starred", func() {
					m.Get("", user.GetMyStarredRepos)
					m.Group("/{username}/{reponame}", func() {
						m.Get("", user.IsStarring)
						m.Put("", user.Star)
						m.Delete("", user.Unstar)
					}, repoAssignment(), checkTokenPublicOnly())
				}, tokenRequiresScopes(auth_model.AccessTokenScopeCategoryRepository))
			}
			m.Get("/times", repo.ListMyTrackedTimes)
			m.Get("/stopwatches", repo.GetStopwatches)
			m.Get("/subscriptions", user.GetMyWatchedRepos)
			m.Get("/teams", org.ListUserTeams)
			m.Group("/hooks", func() {
				m.Combo("").Get(user.ListHooks).
					Post(bind(api.CreateHookOption{}), user.CreateHook)
				m.Combo("/{id}").Get(user.GetHook).
					Patch(bind(api.EditHookOption{}), user.EditHook).
					Delete(user.DeleteHook)
			}, reqWebhooksEnabled())

			m.Group("", func() {
				m.Get("/list_blocked", user.ListBlockedUsers)
				m.Group("", func() {
					m.Put("/block/{username}", user.BlockUser)
					m.Put("/unblock/{username}", user.UnblockUser)
				}, context.UserAssignmentAPI())
			})

			m.Group("/avatar", func() {
				m.Post("", bind(api.UpdateUserAvatarOption{}), user.UpdateAvatar)
				m.Delete("", user.DeleteAvatar)
			}, reqToken())
		}, tokenRequiresScopes(auth_model.AccessTokenScopeCategoryUser), reqToken())

		// Repositories (requires repo scope, org scope)
		m.Post("/org/{org}/repos",
			// FIXME: we need org in context
			tokenRequiresScopes(auth_model.AccessTokenScopeCategoryOrganization, auth_model.AccessTokenScopeCategoryRepository),
			reqToken(),
			bind(api.CreateRepoOption{}),
			repo.CreateOrgRepoDeprecated)

		// requires repo scope
		// FIXME: Don't expose repository id outside of the system
		m.Combo("/repositories/{id}", reqToken(), tokenRequiresScopes(auth_model.AccessTokenScopeCategoryRepository)).Get(repo.GetByID)

		// Repos (requires repo scope)
		m.Group("/repos", func() {
			m.Get("/search", repo.Search)

			// (repo scope)
			m.Post("/migrate", reqToken(), bind(api.MigrateRepoOptions{}), repo.Migrate)

			m.Group("/{username}/{reponame}", func() {
				m.Get("/compare/*", reqRepoReader(unit.TypeCode), repo.CompareDiff)

				m.Combo("").Get(reqAnyRepoReader(), repo.Get).
					Delete(reqToken(), reqOwner(), repo.Delete).
					Patch(reqToken(), reqAdmin(), bind(api.EditRepoOption{}), repo.Edit)
				m.Post("/generate", reqToken(), reqRepoReader(unit.TypeCode), bind(api.GenerateRepoOption{}), repo.Generate)
				m.Group("/transfer", func() {
					m.Post("", reqOwner(), bind(api.TransferRepoOption{}), repo.Transfer)
					m.Post("/accept", repo.AcceptTransfer)
					m.Post("/reject", repo.RejectTransfer)
				}, reqToken())
				addActionsRoutes(
					m,
					reqOwner(),
					repo.NewAction(),
				)
				m.Group("/hooks/git", func() {
					m.Combo("").Get(repo.ListGitHooks)
					m.Group("/{id}", func() {
						m.Combo("").Get(repo.GetGitHook).
							Patch(bind(api.EditGitHookOption{}), repo.EditGitHook).
							Delete(repo.DeleteGitHook)
					})
				}, reqToken(), reqAdmin(), reqGitHook(), context.ReferencesGitRepo(true))
				m.Group("/hooks", func() {
					m.Combo("").Get(repo.ListHooks).
						Post(bind(api.CreateHookOption{}), repo.CreateHook)
					m.Group("/{id}", func() {
						m.Combo("").Get(repo.GetHook).
							Patch(bind(api.EditHookOption{}), repo.EditHook).
							Delete(repo.DeleteHook)
						m.Post("/tests", context.ReferencesGitRepo(), context.RepoRefForAPI, repo.TestHook)
					})
				}, reqToken(), reqAdmin(), reqWebhooksEnabled())
				m.Group("/collaborators", func() {
					m.Get("", reqAnyRepoReader(), repo.ListCollaborators)
					m.Group("/{collaborator}", func() {
						m.Combo("").Get(reqAnyRepoReader(), repo.IsCollaborator).
							Put(reqAdmin(), bind(api.AddCollaboratorOption{}), repo.AddCollaborator).
							Delete(reqAdmin(), repo.DeleteCollaborator)
						m.Get("/permission", repo.GetRepoPermissions)
					})
				}, reqToken())
				if setting.Repository.EnableFlags {
					m.Group("/flags", func() {
						m.Combo("").Get(repo.ListFlags).
							Put(bind(api.ReplaceFlagsOption{}), repo.ReplaceAllFlags).
							Delete(repo.DeleteAllFlags)
						m.Group("/{flag}", func() {
							m.Combo("").Get(repo.HasFlag).
								Put(repo.AddFlag).
								Delete(repo.DeleteFlag)
						})
					}, tokenRequiresScopes(auth_model.AccessTokenScopeCategoryAdmin), reqToken(), reqSiteAdmin())
				}
				m.Get("/assignees", reqToken(), reqAnyRepoReader(), repo.GetAssignees)
				m.Get("/reviewers", reqToken(), reqAnyRepoReader(), repo.GetReviewers)
				m.Group("/teams", func() {
					m.Get("", reqAnyRepoReader(), repo.ListTeams)
					m.Combo("/{team}").Get(reqAnyRepoReader(), repo.IsTeam).
						Put(reqAdmin(), repo.AddTeam).
						Delete(reqAdmin(), repo.DeleteTeam)
				}, reqToken())
				m.Get("/raw/*", context.ReferencesGitRepo(), context.RepoRefForAPI, reqRepoReader(unit.TypeCode), repo.GetRawFile)
				m.Get("/media/*", context.ReferencesGitRepo(), context.RepoRefForAPI, reqRepoReader(unit.TypeCode), repo.GetRawFileOrLFS)
				m.Get("/archive/*", reqRepoReader(unit.TypeCode), repo.GetArchive)
				if !setting.Repository.DisableForks {
					m.Combo("/forks").Get(repo.ListForks).
						Post(reqToken(), reqRepoReader(unit.TypeCode), bind(api.CreateForkOption{}), repo.CreateFork)
				}
				m.Group("/branches", func() {
					m.Get("", repo.ListBranches)
					m.Get("/*", repo.GetBranch)
					m.Delete("/*", reqToken(), reqRepoWriter(unit.TypeCode), mustNotBeArchived, repo.DeleteBranch)
					m.Post("", reqToken(), reqRepoWriter(unit.TypeCode), mustNotBeArchived, bind(api.CreateBranchRepoOption{}), context.EnforceQuotaAPI(quota_model.LimitSubjectSizeGitAll, context.QuotaTargetRepo), repo.CreateBranch)
					m.Patch("/*", reqToken(), reqRepoWriter(unit.TypeCode), mustNotBeArchived, bind(api.UpdateBranchRepoOption{}), repo.UpdateBranch)
				}, context.ReferencesGitRepo(), reqRepoReader(unit.TypeCode))
				m.Group("/branch_protections", func() {
					m.Get("", repo.ListBranchProtections)
					m.Post("", bind(api.CreateBranchProtectionOption{}), mustNotBeArchived, repo.CreateBranchProtection)
					m.Group("/{name}", func() {
						m.Get("", repo.GetBranchProtection)
						m.Patch("", bind(api.EditBranchProtectionOption{}), mustNotBeArchived, repo.EditBranchProtection)
						m.Delete("", repo.DeleteBranchProtection)
					})
				}, reqToken(), reqAdmin())
				m.Group("/tags", func() {
					m.Get("", repo.ListTags)
					m.Get("/*", repo.GetTag)
					m.Post("", reqToken(), reqRepoWriter(unit.TypeCode), mustNotBeArchived, bind(api.CreateTagOption{}), context.EnforceQuotaAPI(quota_model.LimitSubjectSizeReposAll, context.QuotaTargetRepo), repo.CreateTag)
					m.Delete("/*", reqToken(), reqRepoWriter(unit.TypeCode), mustNotBeArchived, repo.DeleteTag)
				}, reqRepoReader(unit.TypeCode), context.ReferencesGitRepo(true))
				m.Group("/tag_protections", func() {
					m.Combo("").Get(repo.ListTagProtection).
						Post(bind(api.CreateTagProtectionOption{}), mustNotBeArchived, repo.CreateTagProtection)
					m.Group("/{id}", func() {
						m.Combo("").Get(repo.GetTagProtection).
							Patch(bind(api.EditTagProtectionOption{}), mustNotBeArchived, repo.EditTagProtection).
							Delete(repo.DeleteTagProtection)
					})
				}, reqToken(), reqAdmin())
				m.Group("/actions", func() {
					m.Get("/tasks", repo.ListActionTasks)
					m.Group("/runs", func() {
						m.Get("", repo.ListActionRuns)
						m.Get("/{run_id}", repo.GetActionRun)
					})

					m.Group("/workflows", func() {
						m.Group("/{workflowname}", func() {
							m.Post("/dispatches", reqToken(), reqRepoWriter(unit.TypeActions), mustNotBeArchived, bind(api.DispatchWorkflowOption{}), repo.DispatchWorkflow)
						})
					})
				}, reqRepoReader(unit.TypeActions), context.ReferencesGitRepo(true))
				m.Group("/keys", func() {
					m.Combo("").Get(repo.ListDeployKeys).
						Post(bind(api.CreateKeyOption{}), repo.CreateDeployKey)
					m.Combo("/{id}").Get(repo.GetDeployKey).
						Delete(repo.DeleteDeploykey)
				}, reqToken(), reqAdmin())
				m.Group("/times", func() {
					m.Combo("").Get(repo.ListTrackedTimesByRepository)
					m.Combo("/{timetrackingusername}").Get(repo.ListTrackedTimesByUser)
				}, mustEnableIssues, reqToken())
				m.Group("/wiki", func() {
					m.Combo("/page/{pageName}").
						Get(repo.GetWikiPage).
						Patch(mustNotBeArchived, reqToken(), reqRepoWriter(unit.TypeWiki), bind(api.CreateWikiPageOptions{}), context.EnforceQuotaAPI(quota_model.LimitSubjectSizeWiki, context.QuotaTargetRepo), repo.EditWikiPage).
						Delete(mustNotBeArchived, reqToken(), reqRepoWriter(unit.TypeWiki), repo.DeleteWikiPage)
					m.Get("/revisions/{pageName}", repo.ListPageRevisions)
					m.Post("/new", reqToken(), mustNotBeArchived, reqRepoWriter(unit.TypeWiki), bind(api.CreateWikiPageOptions{}), context.EnforceQuotaAPI(quota_model.LimitSubjectSizeWiki, context.QuotaTargetRepo), repo.NewWikiPage)
					m.Get("/pages", repo.ListWikiPages)
				}, mustEnableWiki)
				m.Post("/markup", reqToken(), bind(api.MarkupOption{}), misc.Markup)
				m.Post("/markdown", reqToken(), bind(api.MarkdownOption{}), misc.Markdown)
				m.Post("/markdown/raw", reqToken(), misc.MarkdownRaw)
				if !setting.Repository.DisableStars {
					m.Get("/stargazers", repo.ListStargazers)
				}
				m.Get("/subscribers", repo.ListSubscribers)
				m.Group("/subscription", func() {
					m.Get("", user.IsWatching)
					m.Put("", user.Watch)
					m.Delete("", user.Unwatch)
				}, reqToken())
				m.Group("/releases", func() {
					m.Combo("").Get(repo.ListReleases).
						Post(reqToken(), reqRepoWriter(unit.TypeReleases), context.ReferencesGitRepo(), bind(api.CreateReleaseOption{}), context.EnforceQuotaAPI(quota_model.LimitSubjectSizeReposAll, context.QuotaTargetRepo), repo.CreateRelease)
					m.Combo("/latest").Get(repo.GetLatestRelease)
					m.Group("/{id}", func() {
						m.Combo("").Get(repo.GetRelease).
							Patch(reqToken(), reqRepoWriter(unit.TypeReleases), context.ReferencesGitRepo(), bind(api.EditReleaseOption{}), context.EnforceQuotaAPI(quota_model.LimitSubjectSizeReposAll, context.QuotaTargetRepo), repo.EditRelease).
							Delete(reqToken(), reqRepoWriter(unit.TypeReleases), repo.DeleteRelease)
						m.Group("/assets", func() {
							m.Combo("").Get(repo.ListReleaseAttachments).
								Post(reqToken(), reqRepoWriter(unit.TypeReleases), context.EnforceQuotaAPI(quota_model.LimitSubjectSizeAssetsAttachmentsReleases, context.QuotaTargetRepo), repo.CreateReleaseAttachment)
							m.Combo("/{attachment_id}").Get(repo.GetReleaseAttachment).
								Patch(reqToken(), reqRepoWriter(unit.TypeReleases), bind(api.EditAttachmentOptions{}), repo.EditReleaseAttachment).
								Delete(reqToken(), reqRepoWriter(unit.TypeReleases), repo.DeleteReleaseAttachment)
						})
					})
					m.Group("/tags", func() {
						m.Combo("/{tag}").
							Get(repo.GetReleaseByTag).
							Delete(reqToken(), reqRepoWriter(unit.TypeReleases), repo.DeleteReleaseByTag)
					})
				}, reqRepoReader(unit.TypeReleases))
				m.Post("/mirror-sync", reqToken(), reqRepoWriter(unit.TypeCode), mustNotBeArchived, context.EnforceQuotaAPI(quota_model.LimitSubjectSizeGitAll, context.QuotaTargetRepo), repo.MirrorSync)
				m.Post("/push_mirrors-sync", reqAdmin(), reqToken(), mustNotBeArchived, repo.PushMirrorSync)
				m.Group("/push_mirrors", func() {
					m.Combo("").Get(repo.ListPushMirrors).
						Post(mustNotBeArchived, bind(api.CreatePushMirrorOption{}), repo.AddPushMirror)
					m.Combo("/{name}").
						Delete(mustNotBeArchived, repo.DeletePushMirrorByRemoteName).
						Get(repo.GetPushMirrorByName)
				}, reqAdmin(), reqToken())

				m.Get("/editorconfig/{filename}", context.ReferencesGitRepo(), context.RepoRefForAPI, reqRepoReader(unit.TypeCode), repo.GetEditorconfig)
				m.Group("/pulls", func() {
					m.Combo("").Get(repo.ListPullRequests).
						Post(reqToken(), mustNotBeArchived, bind(api.CreatePullRequestOption{}), repo.CreatePullRequest)
					m.Get("/pinned", repo.ListPinnedPullRequests)
					m.Group("/{index}", func() {
						m.Combo("").Get(repo.GetPullRequest).
							Patch(reqToken(), bind(api.EditPullRequestOption{}), repo.EditPullRequest)
						m.Get(".{diffType:diff|patch}", repo.DownloadPullDiffOrPatch)
						m.Post("/update", reqToken(), context.EnforceQuotaAPI(quota_model.LimitSubjectSizeGitAll, context.QuotaTargetRepo), repo.UpdatePullRequest)
						m.Get("/commits", repo.GetPullRequestCommits)
						m.Get("/files", repo.GetPullRequestFiles)
						m.Combo("/merge").Get(repo.IsPullRequestMerged).
							Post(reqToken(), mustNotBeArchived, bind(forms.MergePullRequestForm{}), context.EnforceQuotaAPI(quota_model.LimitSubjectSizeGitAll, context.QuotaTargetRepo), repo.MergePullRequest).
							Delete(reqToken(), mustNotBeArchived, repo.CancelScheduledAutoMerge)
						m.Group("/reviews", func() {
							m.Combo("").
								Get(repo.ListPullReviews).
								Post(reqToken(), bind(api.CreatePullReviewOptions{}), repo.CreatePullReview)
							m.Group("/{id}", func() {
								m.Combo("").
									Get(repo.GetPullReview).
									Delete(reqToken(), repo.DeletePullReview).
									Post(reqToken(), bind(api.SubmitPullReviewOptions{}), repo.SubmitPullReview)
								m.Group("/comments", func() {
									m.Combo("").
										Get(repo.GetPullReviewComments).
										Post(reqToken(), bind(api.CreatePullReviewCommentOptions{}), repo.CreatePullReviewComment)
									m.Group("/{comment}", func() {
										m.Combo("").
											Get(repo.GetPullReviewComment).
											Delete(reqToken(), repo.DeletePullReviewComment)
									}, commentAssignment("comment"))
								})
								m.Post("/dismissals", reqToken(), bind(api.DismissPullReviewOptions{}), repo.DismissPullReview)
								m.Post("/undismissals", reqToken(), repo.UnDismissPullReview)
							})
						})
						m.Combo("/requested_reviewers", reqToken()).
							Delete(bind(api.PullReviewRequestOptions{}), repo.DeleteReviewRequests).
							Post(bind(api.PullReviewRequestOptions{}), repo.CreateReviewRequests)
					})
					m.Get("/{base}/*", repo.GetPullRequestByBaseHead)
				}, mustAllowPulls, reqRepoReader(unit.TypeCode), context.ReferencesGitRepo())
				m.Group("/statuses", func() {
					m.Combo("/{sha}").Get(repo.GetCommitStatuses).
						Post(reqToken(), reqRepoWriter(unit.TypeCode), bind(api.CreateStatusOption{}), repo.NewCommitStatus)
				}, reqRepoReader(unit.TypeCode))
				m.Group("/commits", func() {
					m.Get("", context.ReferencesGitRepo(), repo.GetAllCommits)
					m.Group("/{ref}", func() {
						m.Get("/status", repo.GetCombinedCommitStatusByRef)
						m.Get("/statuses", repo.GetCommitStatusesByRef)
						m.Get("/pull", repo.GetCommitPullRequest)
					}, context.ReferencesGitRepo())
				}, reqRepoReader(unit.TypeCode))
				m.Group("/git", func() {
					m.Group("/commits", func() {
						m.Get("/{sha}", repo.GetSingleCommit)
						m.Get("/{sha}.{diffType:diff|patch}", repo.DownloadCommitDiffOrPatch)
					})
					m.Get("/refs", repo.GetGitAllRefs)
					m.Get("/refs/*", repo.GetGitRefs)
					m.Get("/trees/{sha}", repo.GetTree)
					m.Get("/blobs/{sha}", repo.GetBlob)
					m.Get("/tags/{sha}", repo.GetAnnotatedTag)
					m.Group("/notes/{sha}", func() {
						m.Get("", repo.GetNote)
						m.Post("", reqToken(), reqRepoWriter(unit.TypeCode), bind(api.NoteOptions{}), repo.SetNote)
						m.Delete("", reqToken(), reqRepoWriter(unit.TypeCode), repo.RemoveNote)
					})
				}, context.ReferencesGitRepo(true), reqRepoReader(unit.TypeCode))
				m.Post("/diffpatch", reqRepoWriter(unit.TypeCode), reqToken(), bind(api.ApplyDiffPatchFileOptions{}), mustNotBeArchived, context.EnforceQuotaAPI(quota_model.LimitSubjectSizeReposAll, context.QuotaTargetRepo), repo.ApplyDiffPatch)
				m.Group("/contents", func() {
					m.Get("", repo.GetContentsList)
					m.Post("", reqToken(), bind(api.ChangeFilesOptions{}), reqRepoBranchWriter, mustNotBeArchived, context.EnforceQuotaAPI(quota_model.LimitSubjectSizeReposAll, context.QuotaTargetRepo), repo.ChangeFiles)
					m.Get("/*", repo.GetContents)
					m.Group("/*", func() {
						m.Post("", bind(api.CreateFileOptions{}), reqRepoBranchWriter, mustNotBeArchived, context.EnforceQuotaAPI(quota_model.LimitSubjectSizeReposAll, context.QuotaTargetRepo), repo.CreateFile)
						m.Put("", bind(api.UpdateFileOptions{}), reqRepoBranchWriter, mustNotBeArchived, context.EnforceQuotaAPI(quota_model.LimitSubjectSizeReposAll, context.QuotaTargetRepo), repo.UpdateFile)
						m.Delete("", bind(api.DeleteFileOptions{}), reqRepoBranchWriter, mustNotBeArchived, context.EnforceQuotaAPI(quota_model.LimitSubjectSizeReposAll, context.QuotaTargetRepo), repo.DeleteFile)
					}, reqToken())
				}, reqRepoReader(unit.TypeCode))
				m.Get("/signing-key.gpg", misc.SigningKey)
				m.Group("/topics", func() {
					m.Combo("").Get(repo.ListTopics).
						Put(reqToken(), reqAdmin(), bind(api.RepoTopicOptions{}), repo.UpdateTopics)
					m.Group("/{topic}", func() {
						m.Combo("").Put(reqToken(), repo.AddTopic).
							Delete(reqToken(), repo.DeleteTopic)
					}, reqAdmin())
				}, reqAnyRepoReader())
				m.Get("/issue_templates", context.ReferencesGitRepo(), repo.GetIssueTemplates)
				m.Get("/issue_config", context.ReferencesGitRepo(), repo.GetIssueConfig)
				m.Get("/issue_config/validate", context.ReferencesGitRepo(), repo.ValidateIssueConfig)
				m.Get("/languages", reqRepoReader(unit.TypeCode), repo.GetLanguages)
				m.Get("/activities/feeds", repo.ListRepoActivityFeeds)
				m.Get("/new_pin_allowed", repo.AreNewIssuePinsAllowed)
				m.Group("/avatar", func() {
					m.Post("", bind(api.UpdateRepoAvatarOption{}), repo.UpdateAvatar)
					m.Delete("", repo.DeleteAvatar)
				}, reqAdmin(), reqToken())
				m.Group("/sync_fork", func() {
					m.Get("", reqRepoReader(unit.TypeCode), repo.SyncForkDefaultInfo)
					m.Post("", mustNotBeArchived, reqRepoWriter(unit.TypeCode), repo.SyncForkDefault)
					m.Get("/{branch}", reqRepoReader(unit.TypeCode), repo.SyncForkBranchInfo)
					m.Post("/{branch}", mustNotBeArchived, reqRepoWriter(unit.TypeCode), repo.SyncForkBranch)
				})

				m.Get("/{ball_type:tarball|zipball|bundle}/*", reqRepoReader(unit.TypeCode), repo.DownloadArchive)
			}, repoAssignment(), checkTokenPublicOnly())
		}, tokenRequiresScopes(auth_model.AccessTokenScopeCategoryRepository))

		// Notifications (requires notifications scope)
		m.Group("/repos", func() {
			m.Group("/{username}/{reponame}", func() {
				m.Combo("/notifications", reqToken()).
					Get(notify.ListRepoNotifications).
					Put(notify.ReadRepoNotifications)
			}, repoAssignment(), checkTokenPublicOnly())
		}, tokenRequiresScopes(auth_model.AccessTokenScopeCategoryNotification))

		// Issue (requires issue scope)
		m.Group("/repos", func() {
			m.Get("/issues/search", repo.SearchIssues)

			m.Group("/{username}/{reponame}", func() {
				m.Group("/issues", func() {
					m.Combo("").Get(repo.ListIssues).
						Post(reqToken(), mustNotBeArchived, bind(api.CreateIssueOption{}), reqRepoReader(unit.TypeIssues), repo.CreateIssue)
					m.Get("/pinned", reqRepoReader(unit.TypeIssues), repo.ListPinnedIssues)
					m.Group("/comments", func() {
						m.Get("", repo.ListRepoIssueComments)
						m.Group("/{id}", func() {
							m.Combo("").
								Get(repo.GetIssueComment).
								Patch(mustNotBeArchived, reqToken(), bind(api.EditIssueCommentOption{}), repo.EditIssueComment).
								Delete(reqToken(), repo.DeleteIssueComment)
							m.Combo("/reactions").
								Get(repo.GetIssueCommentReactions).
								Post(reqToken(), bind(api.EditReactionOption{}), repo.PostIssueCommentReaction).
								Delete(reqToken(), bind(api.EditReactionOption{}), repo.DeleteIssueCommentReaction)
							m.Group("/assets", func() {
								m.Combo("").
									Get(repo.ListIssueCommentAttachments).
									Post(reqToken(), mustNotBeArchived, context.EnforceQuotaAPI(quota_model.LimitSubjectSizeAssetsAttachmentsIssues, context.QuotaTargetRepo), repo.CreateIssueCommentAttachment)
								m.Combo("/{attachment_id}").
									Get(repo.GetIssueCommentAttachment).
									Patch(reqToken(), mustNotBeArchived, bind(api.EditAttachmentOptions{}), repo.EditIssueCommentAttachment).
									Delete(reqToken(), mustNotBeArchived, repo.DeleteIssueCommentAttachment)
							}, mustEnableAttachments)
						}, commentAssignment(":id"))
					})
					m.Group("/{index}", func() {
						m.Combo("").Get(repo.GetIssue).
							Patch(reqToken(), bind(api.EditIssueOption{}), repo.EditIssue).
							Delete(reqToken(), reqAdmin(), context.ReferencesGitRepo(), repo.DeleteIssue)
						m.Group("/comments", func() {
							m.Combo("").Get(repo.ListIssueComments).
								Post(reqToken(), mustNotBeArchived, bind(api.CreateIssueCommentOption{}), repo.CreateIssueComment)
							m.Combo("/{id}", reqToken()).Patch(bind(api.EditIssueCommentOption{}), repo.EditIssueCommentDeprecated).
								Delete(repo.DeleteIssueCommentDeprecated)
						})
						m.Get("/timeline", repo.ListIssueCommentsAndTimeline)
						m.Group("/labels", func() {
							m.Combo("").Get(repo.ListIssueLabels).
								Post(reqToken(), bind(api.IssueLabelsOption{}), repo.AddIssueLabels).
								Put(reqToken(), bind(api.IssueLabelsOption{}), repo.ReplaceIssueLabels).
								Delete(reqToken(), bind(api.DeleteLabelsOption{}), repo.ClearIssueLabels)
							m.Delete("/{id}", reqToken(), bind(api.DeleteLabelsOption{}), repo.DeleteIssueLabel)
						})
						m.Group("/times", func() {
							m.Combo("").
								Get(repo.ListTrackedTimes).
								Post(bind(api.AddTimeOption{}), repo.AddTime).
								Delete(repo.ResetIssueTime)
							m.Delete("/{id}", repo.DeleteTime)
						}, reqToken())
						m.Combo("/deadline").Post(reqToken(), bind(api.EditDeadlineOption{}), repo.UpdateIssueDeadline)
						m.Group("/stopwatch", func() {
							m.Post("/start", repo.StartIssueStopwatch)
							m.Post("/stop", repo.StopIssueStopwatch)
							m.Delete("/delete", repo.DeleteIssueStopwatch)
						}, reqToken())
						m.Group("/subscriptions", func() {
							m.Get("", repo.GetIssueSubscribers)
							m.Get("/check", reqToken(), repo.CheckIssueSubscription)
							m.Put("/{user}", reqToken(), repo.AddIssueSubscription)
							m.Delete("/{user}", reqToken(), repo.DelIssueSubscription)
						})
						m.Combo("/reactions").
							Get(repo.GetIssueReactions).
							Post(reqToken(), bind(api.EditReactionOption{}), repo.PostIssueReaction).
							Delete(reqToken(), bind(api.EditReactionOption{}), repo.DeleteIssueReaction)
						m.Group("/assets", func() {
							m.Combo("").
								Get(repo.ListIssueAttachments).
								Post(reqToken(), mustNotBeArchived, context.EnforceQuotaAPI(quota_model.LimitSubjectSizeAssetsAttachmentsIssues, context.QuotaTargetRepo), repo.CreateIssueAttachment)
							m.Combo("/{attachment_id}").
								Get(repo.GetIssueAttachment).
								Patch(reqToken(), mustNotBeArchived, bind(api.EditAttachmentOptions{}), repo.EditIssueAttachment).
								Delete(reqToken(), mustNotBeArchived, repo.DeleteIssueAttachment)
						}, mustEnableAttachments)
						m.Combo("/dependencies").
							Get(repo.GetIssueDependencies).
							Post(reqToken(), mustNotBeArchived, bind(api.IssueMeta{}), repo.CreateIssueDependency).
							Delete(reqToken(), mustNotBeArchived, bind(api.IssueMeta{}), repo.RemoveIssueDependency)
						m.Combo("/blocks").
							Get(repo.GetIssueBlocks).
							Post(reqToken(), bind(api.IssueMeta{}), repo.CreateIssueBlocking).
							Delete(reqToken(), bind(api.IssueMeta{}), repo.RemoveIssueBlocking)
						m.Group("/pin", func() {
							m.Combo("").
								Post(reqToken(), reqAdmin(), repo.PinIssue).
								Delete(reqToken(), reqAdmin(), repo.UnpinIssue)
							m.Patch("/{position}", reqToken(), reqAdmin(), repo.MoveIssuePin)
						})
					})
				}, mustEnableIssuesOrPulls)
				m.Group("/labels", func() {
					m.Combo("").Get(repo.ListLabels).
						Post(reqToken(), reqRepoWriter(unit.TypeIssues, unit.TypePullRequests), bind(api.CreateLabelOption{}), repo.CreateLabel)
					m.Combo("/{id}").Get(repo.GetLabel).
						Patch(reqToken(), reqRepoWriter(unit.TypeIssues, unit.TypePullRequests), bind(api.EditLabelOption{}), repo.EditLabel).
						Delete(reqToken(), reqRepoWriter(unit.TypeIssues, unit.TypePullRequests), repo.DeleteLabel)
				})
				m.Group("/milestones", func() {
					m.Combo("").Get(repo.ListMilestones).
						Post(reqToken(), reqRepoWriter(unit.TypeIssues, unit.TypePullRequests), bind(api.CreateMilestoneOption{}), repo.CreateMilestone)
					m.Combo("/{id}").Get(repo.GetMilestone).
						Patch(reqToken(), reqRepoWriter(unit.TypeIssues, unit.TypePullRequests), bind(api.EditMilestoneOption{}), repo.EditMilestone).
						Delete(reqToken(), reqRepoWriter(unit.TypeIssues, unit.TypePullRequests), repo.DeleteMilestone)
				})
			}, repoAssignment(), checkTokenPublicOnly())
		}, tokenRequiresScopes(auth_model.AccessTokenScopeCategoryIssue))

		// NOTE: these are Gitea package management API - see packages.CommonRoutes and packages.DockerContainerRoutes for endpoints that implement package manager APIs
		m.Group("/packages/{username}", func() {
			m.Group("/{type}/{name}", func() {
				m.Group("/{version}", func() {
					m.Get("", packages.GetPackage)
					m.Delete("", reqToken(), reqPackageAccess(perm.AccessModeWrite), packages.DeletePackage)
					m.Get("/files", packages.ListPackageFiles)
				})

				m.Post("/-/link/{repo_name}", reqToken(), reqPackageAccess(perm.AccessModeWrite), packages.LinkPackage)
				m.Post("/-/unlink", reqToken(), reqPackageAccess(perm.AccessModeWrite), packages.UnlinkPackage)
			})

			m.Get("/", packages.ListPackages)
		}, tokenRequiresScopes(auth_model.AccessTokenScopeCategoryPackage), context.UserAssignmentAPI(), context.PackageAssignmentAPI(), reqPackageAccess(perm.AccessModeRead), checkTokenPublicOnly())

		// Organizations
		m.Get("/user/orgs", reqToken(), tokenRequiresScopes(auth_model.AccessTokenScopeCategoryUser, auth_model.AccessTokenScopeCategoryOrganization), org.ListMyOrgs)
		m.Group("/users/{username}/orgs", func() {
			m.Get("", reqToken(), org.ListUserOrgs)
			m.Get("/{org}/permissions", reqToken(), org.GetUserOrgsPermissions)
		}, tokenRequiresScopes(auth_model.AccessTokenScopeCategoryUser, auth_model.AccessTokenScopeCategoryOrganization), context.UserAssignmentAPI(), checkTokenPublicOnly())
		m.Post("/orgs", tokenRequiresScopes(auth_model.AccessTokenScopeCategoryOrganization), reqToken(), bind(api.CreateOrgOption{}), org.Create)
		m.Get("/orgs", org.GetAll, tokenRequiresScopes(auth_model.AccessTokenScopeCategoryOrganization))
		m.Group("/orgs/{org}", func() {
			m.Combo("").Get(org.Get).
				Patch(reqToken(), reqOrgOwnership(), bind(api.EditOrgOption{}), org.Edit).
				Delete(reqToken(), reqOrgOwnership(), org.Delete)
			m.Post("/rename", reqToken(), reqOrgOwnership(), bind(api.RenameOrgOption{}), org.Rename)
			m.Combo("/repos").Get(user.ListOrgRepos).
				Post(reqToken(), bind(api.CreateRepoOption{}), context.EnforceQuotaAPI(quota_model.LimitSubjectSizeReposAll, context.QuotaTargetOrg), repo.CreateOrgRepo)
			m.Group("/members", func() {
				m.Get("", reqToken(), org.ListMembers)
				m.Combo("/{username}").Get(reqToken(), org.IsMember).
					Delete(reqToken(), reqOrgOwnership(), org.DeleteMember)
			})
			addActionsRoutes(
				m,
				reqOrgOwnership(),
				org.NewAction(),
			)
			m.Group("/public_members", func() {
				m.Get("", org.ListPublicMembers)
				m.Combo("/{username}").Get(org.IsPublicMember).
					Put(reqToken(), reqOrgMembership(), org.PublicizeMember).
					Delete(reqToken(), reqOrgMembership(), org.ConcealMember)
			})
			m.Group("/teams", func() {
				m.Get("", org.ListTeams)
				m.Post("", reqOrgOwnership(), bind(api.CreateTeamOption{}), org.CreateTeam)
				m.Get("/search", org.SearchTeam)
			}, reqToken(), reqOrgMembership())
			m.Group("/labels", func() {
				m.Get("", org.ListLabels)
				m.Post("", reqToken(), reqOrgOwnership(), bind(api.CreateLabelOption{}), org.CreateLabel)
				m.Combo("/{id}").Get(reqToken(), org.GetLabel).
					Patch(reqToken(), reqOrgOwnership(), bind(api.EditLabelOption{}), org.EditLabel).
					Delete(reqToken(), reqOrgOwnership(), org.DeleteLabel)
			})
			m.Group("/hooks", func() {
				m.Combo("").Get(org.ListHooks).
					Post(bind(api.CreateHookOption{}), org.CreateHook)
				m.Combo("/{id}").Get(org.GetHook).
					Patch(bind(api.EditHookOption{}), org.EditHook).
					Delete(org.DeleteHook)
			}, reqToken(), reqOrgOwnership(), reqWebhooksEnabled())
			m.Group("/avatar", func() {
				m.Post("", bind(api.UpdateUserAvatarOption{}), org.UpdateAvatar)
				m.Delete("", org.DeleteAvatar)
			}, reqToken(), reqOrgOwnership())
			m.Get("/activities/feeds", org.ListOrgActivityFeeds)

			if setting.Quota.Enabled {
				m.Group("/quota", func() {
					m.Get("", org.GetQuota)
					m.Get("/check", org.CheckQuota)
					m.Get("/attachments", org.ListQuotaAttachments)
					m.Get("/packages", org.ListQuotaPackages)
					m.Get("/artifacts", org.ListQuotaArtifacts)
				}, reqToken(), reqOrgOwnership())
			}

			m.Group("", func() {
				m.Get("/list_blocked", org.ListBlockedUsers)
				m.Group("", func() {
					m.Put("/block/{username}", org.BlockUser)
					m.Put("/unblock/{username}", org.UnblockUser)
				}, context.UserAssignmentAPI())
			}, reqToken(), reqOrgOwnership())
		}, tokenRequiresScopes(auth_model.AccessTokenScopeCategoryOrganization), orgAssignment(true), checkTokenPublicOnly())
		m.Group("/teams/{teamid}", func() {
			m.Combo("").Get(reqToken(), org.GetTeam).
				Patch(reqToken(), reqOrgOwnership(), bind(api.EditTeamOption{}), org.EditTeam).
				Delete(reqToken(), reqOrgOwnership(), org.DeleteTeam)
			m.Group("/members", func() {
				m.Get("", reqToken(), org.GetTeamMembers)
				m.Combo("/{username}").
					Get(reqToken(), org.GetTeamMember).
					Put(reqToken(), reqOrgOwnership(), org.AddTeamMember).
					Delete(reqToken(), reqOrgOwnership(), org.RemoveTeamMember)
			})
			m.Group("/repos", func() {
				m.Get("", reqToken(), org.GetTeamRepos)
				m.Combo("/{org}/{reponame}").
					Put(reqToken(), org.AddTeamRepository).
					Delete(reqToken(), org.RemoveTeamRepository).
					Get(reqToken(), org.GetTeamRepo)
			})
			m.Get("/activities/feeds", org.ListTeamActivityFeeds)
		}, tokenRequiresScopes(auth_model.AccessTokenScopeCategoryOrganization), orgAssignment(false, true), reqToken(), reqTeamMembership(), checkTokenPublicOnly())

		m.Group("/admin", func() {
			m.Group("/cron", func() {
				m.Get("", admin.ListCronTasks)
				m.Post("/{task}", admin.PostCronTask)
			})
			m.Get("/orgs", admin.GetAllOrgs)
			m.Group("/users", func() {
				m.Get("", admin.SearchUsers)
				m.Post("", bind(api.CreateUserOption{}), admin.CreateUser)
				m.Group("/{username}", func() {
					m.Combo("").Patch(bind(api.EditUserOption{}), admin.EditUser).
						Delete(admin.DeleteUser)
					m.Group("/keys", func() {
						m.Post("", bind(api.CreateKeyOption{}), admin.CreatePublicKey)
						m.Delete("/{id}", admin.DeleteUserPublicKey)
					})
					m.Get("/orgs", org.ListUserOrgs)
					m.Post("/orgs", bind(api.CreateOrgOption{}), admin.CreateOrg)
					m.Post("/repos", bind(api.CreateRepoOption{}), admin.CreateRepo)
					m.Post("/rename", bind(api.RenameUserOption{}), admin.RenameUser)
					if setting.Quota.Enabled {
						m.Group("/quota", func() {
							m.Get("", admin.GetUserQuota)
							m.Post("/groups", bind(api.SetUserQuotaGroupsOptions{}), admin.SetUserQuotaGroups)
						})
					}
				}, context.UserAssignmentAPI())
			})
			m.Group("/emails", func() {
				m.Get("", admin.GetAllEmails)
				m.Get("/search", admin.SearchEmail)
			})
			m.Group("/unadopted", func() {
				m.Get("", admin.ListUnadoptedRepositories)
				m.Post("/{username}/{reponame}", admin.AdoptRepository)
				m.Delete("/{username}/{reponame}", admin.DeleteUnadoptedRepository)
			})
			m.Group("/hooks", func() {
				m.Combo("").Get(admin.ListHooks).
					Post(bind(api.CreateHookOption{}), admin.CreateHook)
				m.Combo("/{id}").Get(admin.GetHook).
					Patch(bind(api.EditHookOption{}), admin.EditHook).
					Delete(admin.DeleteHook)
			})
			m.Group("/runners", func() {
				m.Get("/registration-token", admin.GetRegistrationToken)
				m.Get("/jobs", admin.SearchActionRunJobs)
			})
			if setting.Quota.Enabled {
				m.Group("/quota", func() {
					m.Group("/rules", func() {
						m.Combo("").Get(admin.ListQuotaRules).
							Post(bind(api.CreateQuotaRuleOptions{}), admin.CreateQuotaRule)
						m.Combo("/{quotarule}", context.QuotaRuleAssignmentAPI()).
							Get(admin.GetQuotaRule).
							Patch(bind(api.EditQuotaRuleOptions{}), admin.EditQuotaRule).
							Delete(admin.DeleteQuotaRule)
					})
					m.Group("/groups", func() {
						m.Combo("").Get(admin.ListQuotaGroups).
							Post(bind(api.CreateQuotaGroupOptions{}), admin.CreateQuotaGroup)
						m.Group("/{quotagroup}", func() {
							m.Combo("").Get(admin.GetQuotaGroup).
								Delete(admin.DeleteQuotaGroup)
							m.Group("/rules", func() {
								m.Combo("/{quotarule}", context.QuotaRuleAssignmentAPI()).
									Put(admin.AddRuleToQuotaGroup).
									Delete(admin.RemoveRuleFromQuotaGroup)
							})
							m.Group("/users", func() {
								m.Get("", admin.ListUsersInQuotaGroup)
								m.Combo("/{username}", context.UserAssignmentAPI()).
									Put(admin.AddUserToQuotaGroup).
									Delete(admin.RemoveUserFromQuotaGroup)
							})
						}, context.QuotaGroupAssignmentAPI())
					})
				})
			}
		}, tokenRequiresScopes(auth_model.AccessTokenScopeCategoryAdmin), reqToken(), reqSiteAdmin())

		m.Group("/topics", func() {
			m.Get("/search", repo.TopicSearch)
		}, tokenRequiresScopes(auth_model.AccessTokenScopeCategoryRepository))
	}, sudo())

	return m
}
