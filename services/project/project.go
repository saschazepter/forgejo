// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package project

import (
	"context"
	"fmt"

	"forgejo.org/models/db"
	project_model "forgejo.org/models/project"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"
	project_module "forgejo.org/modules/project"
	project_structs "forgejo.org/modules/structs"
	validation "forgejo.org/modules/validation"
)

func getBasicSearchOpts(isShowClosed bool, sortType, keyword string, ownerType project_module.APIOwnerType, pageOpts ...int) *project_model.SearchOptions {
	opts := &project_model.SearchOptions{
		IsClosed: optional.Some(isShowClosed),
		Type:     ownerType.ToOwnerType(),
	}
	opts.OrderBy = project_model.GetSearchOrderBySortType(sortType)
	if keyword != "" {
		opts.Title = keyword
	}
	if len(pageOpts) > 1 {
		opts.ListOptions = db.ListOptions{
			Page:     pageOpts[0],
			PageSize: pageOpts[1],
		}
	}
	return opts
}

// GetAPIOwnerType Returns the owner type of the project
func GetAPIOwnerType(isOrg, isRepo bool) project_module.APIOwnerType {
	var t project_module.APIOwnerType
	if isOrg {
		t = project_module.APIOwnerTypeOrganization
	} else if isRepo {
		t = project_module.APIOwnerTypeRepository
	} else {
		t = project_module.APIOwnerTypeIndividual
	}
	return t
}

// Returns a valid project or fails with validation error if invalid
func NewProject(
	form *project_structs.CreateOrUpdateProjectOptions,
	owner *user_model.User,
	repo *repo_model.Repository,
	ownerType project_module.APIOwnerType,
) (*project_model.Project, error) {
	var err error

	projectTemplateType := project_module.APITemplateType(form.TemplateType)
	if projectTemplateType == "" {
		// treat empty string as template type "none"
		projectTemplateType = project_module.APITemplateTypeNone
	}
	valid, err := validation.IsValid(projectTemplateType)
	if !valid {
		return nil, err
	}

	projectCardType := project_module.APICardType(form.CardType)
	valid, err = validation.IsValid(projectCardType)
	if !valid {
		return nil, err
	}

	valid, err = validation.IsValid(ownerType)
	if !valid {
		return nil, err
	}

	res := &project_model.Project{
		Title:        form.Title,
		Description:  form.Description,
		TemplateType: projectTemplateType.ToTemplateType(),
		CardType:     projectCardType.ToCardType(),
		Type:         ownerType.ToOwnerType(),
	}

	errNotValid := validation.ErrNotValid{}
	switch ownerType {
	case project_module.APIOwnerTypeIndividual:
		if owner.IsOrganization() {
			errNotValid.Message = "Type was TypeIndividual, but owner was org"
		} else if repo != nil {
			errNotValid.Message = "Type was TypeIndividual, repo was given"
		} else {
			res.Owner = owner
			res.OwnerID = owner.ID
		}
	case project_module.APIOwnerTypeOrganization:
		if owner.IsIndividual() {
			errNotValid.Message = "Type was TypeOrganization, but owner was individual"
		} else if repo != nil {
			errNotValid.Message = "Type was TypeOrganization, repo was given"
		} else {
			res.Owner = owner
			res.OwnerID = owner.ID
		}
	case project_module.APIOwnerTypeRepository:
		if repo != nil {
			res.Repo = repo
			res.RepoID = repo.ID
		} else {
			errNotValid.Message = "Repo type given, but repo struct was empty"
		}
	}

	if errNotValid.Message != "" {
		return nil, errNotValid
	}

	return res, nil
}

// GetSearchOpts returns search options for user, org or repo depending on the ownerType
func GetSearchOpts(id int64, isShowClosed bool, sortType, keyword string, ownerType project_module.APIOwnerType, pageOpts ...int) *project_model.SearchOptions {
	opts := getBasicSearchOpts(isShowClosed, sortType, keyword, ownerType, pageOpts...)
	if ownerType == project_module.APIOwnerTypeRepository {
		opts.RepoID = id
	} else {
		opts.OwnerID = id
	}
	return opts
}

// GetProjectByIDForOwner Fetches a Project by its ID from the DB
// and checks if it belongs to the given owner
func GetProjectByIDForOwner(ctx context.Context, projectID, ownerID int64, repo *repo_model.Repository) (*project_model.Project, error) {
	project, err := project_model.GetProjectByID(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("could not get project %d for owner %d: %w", projectID, ownerID, err)
	}
	if !project.CanBeAccessedByOwnerRepo(ownerID, repo) {
		return nil, project_module.ErrMismatchedOwner{Message: "Project did not belong to given owner"}
	}
	return project, nil
}

// ListProjectsByOptions Lists Projects by search options
func ListProjectsByOptions(ctx context.Context, opts *project_model.SearchOptions) ([]*project_model.Project, error) {
	projects, err := db.Find[project_model.Project](ctx, opts)
	return projects, err
}

// ListProjectsByOptions Counts Projects by search options
func CountProjectsByOptions(ctx context.Context, opts *project_model.SearchOptions) (int64, error) {
	count, err := db.Count[project_model.Project](ctx, opts)
	return count, err
}

// Write

// CreateProject Expects a valid project (as provided by NewProject for example) and creates it in DB
func CreateProject(ctx context.Context, project *project_model.Project) error {
	err := project_model.CreateProject(ctx, project)
	if err != nil {
		return fmt.Errorf("could not create project %d: %w", project.ID, err)
	}
	return nil
}

// UpdateProject Updates a Project in DB
func UpdateProject(ctx context.Context, project *project_model.Project, updated *project_structs.CreateOrUpdateProjectOptions) error {
	if updated.Title != "" {
		project.Title = updated.Title
	}

	if updated.Description != "" {
		project.Description = updated.Description
	}

	if updated.CardType != "" {
		projectCardType := project_module.APICardType(updated.CardType)
		valid, err := validation.IsValid(projectCardType)
		if !valid {
			return err
		}
		project.CardType = projectCardType.ToCardType()
	}

	if err := project_model.UpdateProject(ctx, project); err != nil {
		return fmt.Errorf("Could not update project %d: %w", project.ID, err)
	}

	if updated.Status != "" {
		projectStatus := project_module.APIStatus(updated.Status)
		valid, err := validation.IsValid(projectStatus)
		if !valid {
			return err
		}
		if project.IsClosed != projectStatus.IsClosed() {
			if err := ChangeProjectStatus(ctx, project, projectStatus.IsClosed()); err != nil {
				return err
			}
		}
	}
	return nil
}

// DeleteProjectByID Deletes Project from DB
func DeleteProjectByID(ctx context.Context, projectID int64, repoID optional.Option[int64]) error {
	err := project_model.DeleteProjectByID(ctx, projectID, repoID)
	if err != nil {
		return fmt.Errorf("could not delete project %d: %w", projectID, err)
	}
	return nil
}

// ChangeProjectStatus Changes the status of a Project to closed or open
func ChangeProjectStatus(ctx context.Context, project *project_model.Project, close bool) error {
	return project_model.ChangeProjectStatus(ctx, project, close)
}
