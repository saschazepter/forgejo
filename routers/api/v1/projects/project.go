// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package projects

import (
	"net/http"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	access_model "forgejo.org/models/perm/access"
	project_model "forgejo.org/models/project"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"
	project_module "forgejo.org/modules/project"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/validation"
	"forgejo.org/modules/web"
	"forgejo.org/routers/api/v1/utils"
	"forgejo.org/services/context"
	"forgejo.org/services/convert"
	project_service "forgejo.org/services/project"
)

// getOwnerTypeAndOwnerAndRepoFromData returns the owner type, owner and
// repository from data in the context.
func getOwnerTypeAndOwnerAndRepoFromData(ctx *context.APIContext) (
	project_module.APIOwnerType, *user_model.User, *repo_model.Repository,
) {
	var ownerType project_module.APIOwnerType
	var owner *user_model.User
	var repo *repo_model.Repository

	switch ctx.Project().APIOwnerType {
	case project_module.APIOwnerTypeIndividual:
		ownerType = project_module.APIOwnerTypeIndividual
		owner = ctx.Doer()
	case project_module.APIOwnerTypeOrganization:
		ownerType = project_module.APIOwnerTypeOrganization
		owner = ctx.User()
	case project_module.APIOwnerTypeRepository:
		ownerType = project_module.APIOwnerTypeRepository
		repo = ctx.Repository()
	}
	return ownerType, owner, repo
}

// CreateProject creates a project for the user/org/repo
func CreateProject(ctx *context.APIContext) {
	form := web.GetForm(ctx).(*api.CreateOrUpdateProjectOptions)
	ownerType, owner, repo := getOwnerTypeAndOwnerAndRepoFromData(ctx)

	// create the project
	p, err := project_service.NewProject(form, owner, repo, ownerType)
	if err != nil {
		if validation.IsErrNotValid(err) {
			ctx.Error(http.StatusUnprocessableEntity, "Invalid Arguments", err)
		} else {
			ctx.ServerError("Create Project", err)
		}
		return
	}

	if err := project_service.CreateProject(ctx, p); err != nil {
		ctx.ServerError("Create Project", err)
		return
	}

	ctx.JSON(http.StatusCreated, convert.ToAPIProject(p))
}

// ListProjects gets a list of projects according to query parameters
func ListProjects(ctx *context.APIContext) {
	ownerType, owner, repo := getOwnerTypeAndOwnerAndRepoFromData(ctx)

	// get projects
	listOptions := utils.GetListOptions(ctx)
	opts := project_model.SearchOptions{
		ListOptions: listOptions,
		Type:        ownerType.ToOwnerType(),
	}
	if ownerType == project_module.APIOwnerTypeRepository {
		opts.RepoID = repo.ID
	} else {
		opts.OwnerID = owner.ID
	}
	projects, total, err := db.FindAndCount[project_model.Project](ctx, opts)
	if err != nil {
		ctx.ServerError("Get Projects", err)
		return
	}

	// set owner and repo in projects if set by caller
	for _, p := range projects {
		if ownerType == project_module.APIOwnerTypeRepository {
			p.Repo = repo
		} else {
			p.Owner = owner
		}
	}

	ctx.SetLinkHeader(int(total), listOptions.PageSize)
	ctx.SetTotalCountHeader(total)
	ctx.JSON(http.StatusOK, convert.ToAPIProjectList(projects))
}

// GetProject gets a project by id
func GetProject(ctx *context.APIContext) {
	ownerType, owner, repo := getOwnerTypeAndOwnerAndRepoFromData(ctx)
	var err error
	var project *project_model.Project
	if ownerType == project_module.APIOwnerTypeRepository {
		project, err = project_service.GetProjectByIDForOwner(ctx, ctx.Project().ProjectID, 0, repo)
		if project != nil {
			project.Repo = repo
		}
	} else {
		project, err = project_service.GetProjectByIDForOwner(ctx, ctx.Project().ProjectID, owner.ID, nil)
		if project != nil {
			project.Owner = owner
		}
	}
	if err != nil {
		if project_model.IsErrProjectNotExist(err) || project_module.IsErrMismatchedOwner(err) {
			ctx.NotFound("Get Project", err)
		} else {
			ctx.ServerError("Get Project", err)
		}
		return
	}

	ctx.JSON(http.StatusOK, convert.ToAPIProject(project))
}

// UpdateProject updates a project by id
func UpdateProject(ctx *context.APIContext) {
	form := web.GetForm(ctx).(*api.CreateOrUpdateProjectOptions)
	ownerType, owner, repo := getOwnerTypeAndOwnerAndRepoFromData(ctx)
	var err error
	var project *project_model.Project
	if ownerType == project_module.APIOwnerTypeRepository {
		project, err = project_service.GetProjectByIDForOwner(ctx, ctx.Project().ProjectID, 0, repo)
	} else {
		project, err = project_service.GetProjectByIDForOwner(ctx, ctx.Project().ProjectID, owner.ID, repo)
	}
	if err != nil {
		if project_model.IsErrProjectNotExist(err) || project_module.IsErrMismatchedOwner(err) {
			ctx.NotFound("Update Project", err)
		} else {
			ctx.ServerError("Update Project", err)
		}
		return
	}
	if err := project_service.UpdateProject(ctx, project, form); err != nil {
		if validation.IsErrNotValid(err) {
			ctx.Error(http.StatusUnprocessableEntity, "Invalid Arguments", err)
		} else {
			ctx.ServerError("Update Project", err)
		}
		return
	}
	ctx.Status(http.StatusNoContent)
}

// DeleteProject deletes a project by ID
func DeleteProject(ctx *context.APIContext) {
	ownerType, owner, repo := getOwnerTypeAndOwnerAndRepoFromData(ctx)
	var err error
	var project *project_model.Project
	if ownerType == project_module.APIOwnerTypeRepository {
		project, err = project_service.GetProjectByIDForOwner(ctx, ctx.Project().ProjectID, 0, repo)
	} else {
		project, err = project_service.GetProjectByIDForOwner(ctx, ctx.Project().ProjectID, owner.ID, repo)
	}
	if err != nil {
		if project_model.IsErrProjectNotExist(err) || project_module.IsErrMismatchedOwner(err) {
			ctx.NotFound("Delete Project", err)
		} else {
			ctx.ServerError("Delete Project", err)
		}
		return
	}
	err = project_service.DeleteProjectByID(ctx, project.ID, optional.None[int64]())
	if err != nil {
		ctx.ServerError("Delete Project", err)
		return
	}

	ctx.Status(http.StatusNoContent)
}

// ListProjectIssues lists project issues
func ListProjectIssues(ctx *context.APIContext) {
	ownerType, owner, repo := getOwnerTypeAndOwnerAndRepoFromData(ctx)
	var err error
	var project *project_model.Project
	if ownerType == project_module.APIOwnerTypeRepository {
		project, err = project_service.GetProjectByIDForOwner(ctx, ctx.Project().ProjectID, 0, repo)
	} else {
		project, err = project_service.GetProjectByIDForOwner(ctx, ctx.Project().ProjectID, owner.ID, repo)
	}
	if err != nil {
		if project_model.IsErrProjectNotExist(err) || project_module.IsErrMismatchedOwner(err) {
			ctx.NotFound("List Project Issues", err)
		} else {
			ctx.ServerError("List Project Issues", err)
		}
		return
	}

	// get project issues
	listOptions := utils.GetListOptions(ctx)
	projectIssues, total, err := db.FindAndCount[project_model.ProjectIssue](ctx, project_model.FindProjectIssueOptions{
		ListOptions: listOptions,
		ProjectID:   project.ID,
	})
	if err != nil {
		ctx.ServerError("List Project Issues", err)
		return
	}

	ctx.SetLinkHeader(int(total), listOptions.PageSize)
	ctx.SetTotalCountHeader(total)
	ctx.JSON(http.StatusOK, convert.ToProjectIssueList(projectIssues))
}

// CreateProjectIssue creates an issue in the default column of the project
func CreateProjectIssue(ctx *context.APIContext) {
	createProjectColumnIssue(ctx)
}

// createProjectColumnIssue creates an issue in a column of a project.
func createProjectColumnIssue(ctx *context.APIContext) {
	ownerType, owner, repo := getOwnerTypeAndOwnerAndRepoFromData(ctx)
	var err error
	var project *project_model.Project
	if ownerType == project_module.APIOwnerTypeRepository {
		project, err = project_service.GetProjectByIDForOwner(ctx, ctx.Project().ProjectID, 0, repo)
	} else {
		project, err = project_service.GetProjectByIDForOwner(ctx, ctx.Project().ProjectID, owner.ID, repo)
	}
	if err != nil {
		if project_model.IsErrProjectNotExist(err) || project_module.IsErrMismatchedOwner(err) {
			ctx.NotFound("Create Project Column Issue", err)
		} else {
			ctx.ServerError("Create Project Column Issue", err)
		}
		return
	}

	// get form parameters
	form := web.GetForm(ctx).(*api.CreateProjectIssueOptions)
	issueID := form.IssueID
	if issueID == 0 {
		ctx.Error(http.StatusUnprocessableEntity, "Invalid Arguments", "Issue ID must not be zero")
		return
	}

	// get issue
	issue, err := issues_model.GetIssueByID(ctx, issueID)
	if err != nil {
		if issues_model.IsErrIssueNotExist(err) {
			ctx.NotFound("Create Project Column Issue", err)
		} else {
			ctx.ServerError("CreateProjectColumnIssue", err)
		}
		return
	}

	// check issue permissions
	if err := issue.LoadRepo(ctx); err != nil {
		ctx.ServerError("CreateProjectColumnIssue", err)
		return
	}
	perm, err := access_model.GetUserRepoPermissionWithReducer(ctx, issue.Repo, ctx.Doer(), ctx.Reducer())
	if err != nil {
		ctx.ServerError("CreateProjectColumnIssue", err)
		return
	}
	if !perm.CanWriteIssuesOrPulls(issue.IsPull) {
		ctx.NotFound()
		return
	}

	// create project issue
	projIssue, err := project_service.CreateIssueInProject(ctx, issue, ctx.Doer(), project.ID, ctx.Project().ProjectColumnID)
	if err != nil {
		ctx.ServerError("CreateProjectColumnIssue", err)
		return
	}

	// return project issue
	ctx.JSON(http.StatusCreated, convert.ToProjectIssue(projIssue))
}
