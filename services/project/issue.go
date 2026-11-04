// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package project

import (
	"context"
	"errors"
	"fmt"

	issues_model "forgejo.org/models/issues"
	project_model "forgejo.org/models/project"
	project_types "forgejo.org/modules/structs"
)

// ValidIssueIDs checks if the IDs of the given issue list are valid
func ValidIssueIDs(ctx context.Context, ownerID int64, issues issues_model.IssueList) error {
	if _, err := issues.LoadRepositories(ctx); err != nil {
		return fmt.Errorf("Could not load issue repos: %w", err)
	}
	for _, issue := range issues {
		if issue.Repo.OwnerID != ownerID {
			return errors.New("some issue's ownerID is not equal to project's ownerID")
		}
	}
	return nil
}

// GetIssues Gets an issue list by IssueIDs and checks for completeness, returns false if not complete
func GetIssues(ctx context.Context, issueIDs []int64) (issues_model.IssueList, bool, error) {
	issues, err := issues_model.GetIssuesByIDs(ctx, issueIDs, true)
	if err != nil {
		return nil, false, fmt.Errorf("could not get issues: %w", err)
	}
	complete := len(issues) == len(issueIDs)
	return issues, complete, nil
}

// MoveIssuesOnProjectColumn Allows moving Issues between Columns or to change the sorting within Columns
func MoveIssuesOnProjectColumn(ctx context.Context, column *project_model.Column, projectIssues *project_types.MoveIssuesOption) error {
	sortedIssueIDs := projectIssues.GetSortingsMap()
	err := project_model.MoveIssuesOnProjectColumn(ctx, column, sortedIssueIDs)
	if err != nil {
		return fmt.Errorf("could not sort or move issue on column %d: %w", column.ID, err)
	}
	return nil
}
