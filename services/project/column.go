// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package project

import (
	"context"
	"fmt"

	project_model "forgejo.org/models/project"
	project_module "forgejo.org/modules/project"
	"forgejo.org/modules/validation"
)

func getColumnByID(ctx context.Context, columnID int64) (*project_model.Column, error) {
	column, err := project_model.GetColumn(ctx, columnID)
	if err != nil {
		return nil, fmt.Errorf("failed to get column %d: %w", columnID, err)
	}
	return column, nil
}

// GetValidProjectColumnByID gets a Column by its ID, validate ID != 0 and check if projectIDs match
func GetValidProjectColumnByID(ctx context.Context, projectID, columnID int64) (*project_model.Column, error) {
	if columnID == int64(0) {
		return nil, validation.ErrNotValid{
			Message: "column ID must not be empty",
		}
	}
	c, err := getColumnByID(ctx, columnID)
	if err != nil {
		return nil, fmt.Errorf("could not get column for project %d: %w", projectID, err)
	}
	if c.ProjectID != projectID {
		return nil, project_module.ErrMismatchedID{
			Message: fmt.Sprintf("column %d did not belong to project %d", columnID, projectID),
		}
	}
	return c, nil
}

// CreateColumnInProject creates a ProjectColumn in a Project
func CreateColumnInProject(ctx context.Context, col *project_model.Column) error {
	err := project_model.CreateColumn(ctx, col)
	if err != nil {
		return fmt.Errorf("could not create column for project %d: %w", col.ProjectID, err)
	}
	return nil
}

// EditColumnInProject updates the title or color of a ProjectColumn
func EditColumnInProject(ctx context.Context, col *project_model.Column) error {
	err := project_model.UpdateColumn(ctx, col)
	if err != nil {
		return fmt.Errorf("could not edit column for project %d: %w", col.ProjectID, err)
	}
	return nil
}

// SetDefaultColumn sets the default Column of a Project, other Columns will then be set non default
func SetDefaultColumn(ctx context.Context, projectID, columnID int64) error {
	return project_model.SetDefaultColumn(ctx, projectID, columnID)
}

// DeleteColumnInProject deletes a Column from a Project
func DeleteColumnInProject(ctx context.Context, columnID int64) error {
	err := project_model.DeleteColumnByID(ctx, columnID)
	if err != nil {
		return fmt.Errorf("could not delete column %d: %w", columnID, err)
	}
	return nil
}
