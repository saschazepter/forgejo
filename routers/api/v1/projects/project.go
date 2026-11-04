// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package projects

import (
	"net/http"

	"forgejo.org/models/db"
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
		project.Repo = repo
	} else {
		project, err = project_service.GetProjectByIDForOwner(ctx, ctx.Project().ProjectID, owner.ID, nil)
		project.Owner = owner
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
	err := project_service.DeleteProjectByID(ctx, ctx.Project().ProjectID, optional.None[int64]())
	if err != nil {
		ctx.ServerError("Delete Project", err)
		return
	}

	ctx.Status(http.StatusNoContent)
}
