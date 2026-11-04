// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package context

import (
	go_ctx "context"
	"errors"
	"fmt"
	"net/http"

	"forgejo.org/models/organization"
	"forgejo.org/models/perm"
	project_model "forgejo.org/models/project"
	unit_model "forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/log"
	project_module "forgejo.org/modules/project"
)

// API interface for projects
type ProjectAPI interface {
	// CreateProject creates a project for the user/org/repo
	CreateProject(ctx *APIContext)
	// ListProjects gets a list of projects according to query parameters
	ListProjects(ctx *APIContext)
	// GetProject gets a project by id
	GetProject(ctx *APIContext)
	// UpdateProject updates a project by id
	UpdateProject(ctx *APIContext)
	// DeleteProject deletes a project by ID
	DeleteProject(ctx *APIContext)
}

type Project struct {
	ProjectID       int64
	ProjectColumnID int64
	ProjectIssueID  int64
	APIOwnerType    project_module.APIOwnerType
}

// ReqProjectIDAssignableToIssueAndSetData expects a repository to be set in context
func ReqProjectIDAssignableToIssueAndSetData(ctx *Context, projectID int64) {
	project := getProjectAndEnsureOwner(ctx, projectID)
	if ctx.Written() {
		return
	}
	reqPermissionToAssignProjectToIssue(ctx, project.Type)
	if ctx.Written() {
		return
	}
	ctx.Data["Project"] = project
	ctx.Data["project_id"] = project.ID
}

// ReqProjectIDAssignableToIssue expects a repository to be set in context
func ReqProjectIDAssignableToIssue(ctx *Context, projectID int64) {
	project := getProjectAndEnsureOwner(ctx, projectID)
	if ctx.Written() {
		return
	}
	reqPermissionToAssignProjectToIssue(ctx, project.Type)
}

// getProjectAndEnsureOwner relies on the fact, that ctx.Repo.Repository is always set
func getProjectAndEnsureOwner(ctx *Context, projectID int64) *project_model.Project {
	project, err := project_model.GetProjectByID(ctx, projectID)
	if err != nil {
		if project_model.IsErrProjectNotExist(err) {
			ctx.NotFound(fmt.Sprintf("project %d is not found", projectID), nil)
			return nil
		}
		log.Error("project_model.GetProjectByID(%d): %v", projectID, err)
		ctx.ServerError(fmt.Sprintf("project_model.GetProjectByID(%d)", projectID), err)
		return nil
	}
	ownerID := ctx.Repo.Repository.OwnerID
	if project.Type == project_module.TypeRepository {
		ownerID = ctx.Repo.Repository.ID
	}
	if !project.CanBeAccessedByOwnerRepo(ownerID, ctx.Repo.Repository) {
		log.Error("project_model.GetProjectByID(%d): %v", projectID, err)
		ctx.NotFound(fmt.Sprintf("project %d did not belong to owner %d", projectID, ownerID), nil)
	}
	return project
}

func reqPermissionToAssignProjectToIssue(ctx *Context, ownerType project_module.OwnerType) {
	switch ownerType {
	case project_module.TypeRepository:
		if !ctx.Repo.Repository.UnitEnabled(ctx, unit_model.TypeProjects) {
			ctx.NotFound("repository projects are disabled", nil)
			return
		}
		if !ctx.Repo.CanRead(unit_model.TypeProjects) {
			ctx.Error(http.StatusForbidden, "doesn't have permissions to read repository projects")
			return
		}
		if !ctx.Repo.CanWrite(unit_model.TypeIssues) {
			ctx.Error(http.StatusForbidden, "doesn't have permissions to write repository issues")
			return
		}
	case project_module.TypeOrganization:
		if !ctx.Org.CanReadUnit(ctx, unit_model.TypeProjects) {
			ctx.Error(http.StatusForbidden, "doesn't have permissions to read the owner projects")
			return
		}
		if !ctx.Org.CanWriteUnit(ctx, unit_model.TypeIssues) {
			ctx.Error(http.StatusForbidden, "doesn't have permissions to write the repository issues and set the project")
			return
		}
	case project_module.TypeIndividual:
		if !ctx.Repo.CanWrite(unit_model.TypeIssues) {
			ctx.Error(http.StatusForbidden, "doesn't have permissions to write the repository issues and set the project")
			return
		}
	default:
		ctx.ServerError(fmt.Sprintf("unexpected owner type %v", ownerType), nil)
	}
}

// ProjectAssignment sets project data in the context
func ProjectAssignment(ctx *APIContext) {
	if _, projectAssignmentOnce := ctx.Data["projectAssignmentExecuted"]; projectAssignmentOnce {
		log.Trace("ProjectAssignment was exec already, skipping second call ...")
		return
	}
	ctx.Data["projectAssignmentExecuted"] = true

	// get path parameters
	projectID := ctx.ParamsInt64("project_id")
	if projectID == 0 {
		ctx.Error(http.StatusUnprocessableEntity, "Invalid arguments", "Project ID must not be zero")
		return
	}
	columnID := ctx.ParamsInt64("column_id")
	issueID := ctx.ParamsInt64("issue_id")
	var ownerType project_module.APIOwnerType

	if ctx.Project() != nil {
		ownerType = ctx.Project().APIOwnerType
	}
	ctx.SetProject(&Project{
		ProjectID:       projectID,
		ProjectColumnID: columnID,
		ProjectIssueID:  issueID,
		APIOwnerType:    ownerType,
	})
}

func ReqProjectReadPermissions(ctx *APIContext) {
	hasRead, err := hasReadProjectPermission(ctx,
		ctx.Project().APIOwnerType, ctx.Doer(), ctx.User(), ctx.Repo(),
		ctx.IsUserSiteAdmin())
	if err != nil {
		ctx.ServerError("hasReadPerms", err)
		return
	}
	if !hasRead {
		// forbidden
		ctx.Error(http.StatusForbidden, "ProjectHasRead", "The user did not have sufficient permissions")
		return
	}
}

func ReqProjectWritePermissions(ctx *APIContext) {
	hasWrite, err := hasWriteProjectPermission(ctx,
		ctx.Project().APIOwnerType, ctx.Doer(), ctx.User(), ctx.Repo(),
		ctx.IsUserSiteAdmin())
	if err != nil {
		ctx.ServerError("hasWritePerms", err)
		return
	}
	if !hasWrite {
		// forbidden
		ctx.Error(http.StatusForbidden, "ProjectHasWrite", "The user did not have sufficient permissions")
		return
	}
}

func hasWriteProjectPermission(ctx go_ctx.Context, ownerType any, doer,
	contextUser *user_model.User, repo *Repository, isUserSiteAdmin bool,
) (bool, error) {
	return hasProjectPermission(ctx, ownerType, doer, contextUser, repo, perm.AccessModeWrite, isUserSiteAdmin)
}

func hasReadProjectPermission(ctx go_ctx.Context, ownerType any, doer,
	contextUser *user_model.User, repo *Repository, isUserSiteAdmin bool,
) (bool, error) {
	return hasProjectPermission(ctx, ownerType, doer, contextUser, repo, perm.AccessModeRead, isUserSiteAdmin)
}

func hasProjectPermission(ctx go_ctx.Context, ownerType any, doer,
	contextUser *user_model.User, repo *Repository, p perm.AccessMode, isUserSiteAdmin bool,
) (bool, error) {
	if isUserSiteAdmin {
		return true, nil
	}
	var hasPerm bool
	switch ownerType {
	// If creation target is org, doer must be org owner, org admin, team member with valid team perms or site admin
	case project_module.APIOwnerTypeOrganization:
		org := Organization{Organization: (*organization.Organization)(contextUser)}
		hasPerm = org.Organization.UnitPermission(ctx, doer, unit_model.TypeProjects) >= p
	// If creation target is user, doer and context user must be identical or site admin
	case project_module.APIOwnerTypeIndividual:
		hasPerm = contextUser.ID == doer.ID // write
		if p == perm.AccessModeRead {
			hasPerm = contextUser.ID == doer.ID || contextUser.Visibility.IsPublic()
		}
	case project_module.APIOwnerTypeRepository:
		if repo.Repository.UnitEnabled(ctx, unit_model.TypeProjects) {
			repoWriter := repo.CanWrite(unit_model.TypeProjects)
			if p == perm.AccessModeWrite && repoWriter {
				hasPerm = true
			}
			if p != perm.AccessModeWrite {
				repoReader := repo.CanRead(unit_model.TypeProjects)
				hasPerm = repoReader || repoWriter
			}
		} else {
			return false, errors.New("repo projects not enabled")
		}
	}
	return hasPerm, nil
}
