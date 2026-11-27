// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issues

import (
	"context"

	"forgejo.org/models/db"
	org_model "forgejo.org/models/organization"
	project_model "forgejo.org/models/project"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/util"
)

// LoadProject load the project the issue was assigned to
func (issue *Issue) LoadProject(ctx context.Context) (err error) {
	if issue.Project == nil {
		var p project_model.Project
		has, err := db.GetEngine(ctx).Table("project").
			Join("INNER", "project_issue", "project.id=project_issue.project_id").
			Where("project_issue.issue_id = ?", issue.ID).Get(&p)
		if err != nil {
			return err
		} else if has {
			issue.Project = &p
		}
	}
	return err
}

func (issue *Issue) projectID(ctx context.Context) int64 {
	var ip project_model.ProjectIssue
	has, err := db.GetEngine(ctx).Where("issue_id=?", issue.ID).Get(&ip)
	if err != nil || !has {
		return 0
	}
	return ip.ProjectID
}

// ProjectColumnID return project column id if issue was assigned to one
func (issue *Issue) ProjectColumnID(ctx context.Context) int64 {
	var ip project_model.ProjectIssue
	has, err := db.GetEngine(ctx).Where("issue_id=?", issue.ID).Get(&ip)
	if err != nil || !has {
		return 0
	}
	return ip.ProjectColumnID
}

// LoadIssuesFromColumn load issues assigned to this column
func LoadIssuesFromColumn(ctx context.Context, b *project_model.Column, doer *user_model.User, org *org_model.Organization, isClosed optional.Option[bool]) (IssueList, error) {
	issueOpts := &IssuesOptions{
		ProjectColumnID: b.ID,
		ProjectID:       b.ProjectID,
		SortType:        "project-column-sorting",
		IsClosed:        isClosed,
		AllPublic:       true,
	}
	if doer != nil {
		issueOpts.User = doer
		issueOpts.Org = org
	}

	issueList, err := Issues(ctx, issueOpts)
	if err != nil {
		return nil, err
	}

	if b.Default {
		issueOpts.ProjectColumnID = db.NoConditionID

		issues, err := Issues(ctx, issueOpts)
		if err != nil {
			return nil, err
		}
		issueList = append(issueList, issues...)
	}

	if err := issueList.LoadComments(ctx); err != nil {
		return nil, err
	}

	return issueList, nil
}

// LoadIssuesFromColumnList load issues assigned to the columns
func LoadIssuesFromColumnList(ctx context.Context, bs project_model.ColumnList, doer *user_model.User, org *org_model.Organization, isClosed optional.Option[bool]) (map[int64]IssueList, error) {
	issuesMap := make(map[int64]IssueList, len(bs))
	for i := range bs {
		il, err := LoadIssuesFromColumn(ctx, bs[i], doer, org, isClosed)
		if err != nil {
			return nil, err
		}
		issuesMap[bs[i].ID] = il
	}
	return issuesMap, nil
}

// IssueAssignOrRemoveProject changes the project associated with an issue.
// If projectIssue.ProjectID is 0, the issue is removed from the project.
// If projectIssue.ProjectColumnID is 0, the issue is assigned to the default
// column of the project.
func IssueAssignOrRemoveProject(
	ctx context.Context,
	issue *Issue,
	doer *user_model.User,
	projectIssue *project_model.ProjectIssue,
) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		oldProjectID := issue.projectID(ctx)

		if err := issue.LoadRepo(ctx); err != nil {
			return err
		}

		// Only check if we add a new project and not remove it.
		if projectIssue.ProjectID > 0 {
			newProject, err := project_model.GetProjectByID(ctx, projectIssue.ProjectID)
			if err != nil {
				return err
			}
			if !newProject.CanBeAccessedByOwnerRepo(issue.Repo.OwnerID, issue.Repo) {
				return util.NewPermissionDeniedErrorf("issue %d can't be accessed by project %d", issue.ID, newProject.ID)
			}
			if projectIssue.ProjectColumnID == 0 {
				newDefaultColumn, err := newProject.GetDefaultColumn(ctx)
				if err != nil {
					return err
				}
				projectIssue.ProjectColumnID = newDefaultColumn.ID
			}
		}

		if _, err := db.GetEngine(ctx).Where("project_issue.issue_id=?", issue.ID).Delete(&project_model.ProjectIssue{}); err != nil {
			return err
		}

		if oldProjectID > 0 || projectIssue.ProjectID > 0 {
			if _, err := CreateComment(ctx, &CreateCommentOptions{
				Type:         CommentTypeProject,
				Doer:         doer,
				Repo:         issue.Repo,
				Issue:        issue,
				OldProjectID: oldProjectID,
				ProjectID:    projectIssue.ProjectID,
			}); err != nil {
				return err
			}
		}
		if projectIssue.ProjectID == 0 {
			return nil
		}
		if projectIssue.ProjectColumnID == 0 {
			panic("newColumnID must not be zero") // shouldn't happen
		}

		res := struct {
			MaxSorting int64
			IssueCount int64
		}{}
		if _, err := db.GetEngine(ctx).Select("max(sorting) as max_sorting, count(*) as issue_count").Table("project_issue").
			Where("project_id=?", projectIssue.ProjectID).
			And("project_board_id=?", projectIssue.ProjectColumnID).
			Get(&res); err != nil {
			return err
		}
		newSorting := util.Iif(res.IssueCount > 0, res.MaxSorting+1, 0)
		projectIssue.IssueID = issue.ID
		projectIssue.Sorting = newSorting
		return db.Insert(ctx, projectIssue)
	})
}

// NumIssuesInProjects returns the amount of issues assigned to one of the project
// in the list which the doer can access.
func NumIssuesInProjects(ctx context.Context, pl []*project_model.Project, doer *user_model.User, org *org_model.Organization, isClosed optional.Option[bool]) (map[int64]int, error) {
	numMap := make(map[int64]int, len(pl))
	for _, p := range pl {
		num, err := NumIssuesInProject(ctx, p, doer, org, isClosed)
		if err != nil {
			return nil, err
		}
		numMap[p.ID] = num
	}

	return numMap, nil
}

// NumIssuesInProject returns the amount of issues assigned to the project which
// the doer can access.
func NumIssuesInProject(ctx context.Context, p *project_model.Project, doer *user_model.User, org *org_model.Organization, isClosed optional.Option[bool]) (int, error) {
	numIssuesInProject := int(0)
	bs, err := db.Find[project_model.Column](ctx, project_model.FindColumnOptions{
		ListOptions: db.ListOptionsAll, ProjectID: p.ID,
	})
	if err != nil {
		return 0, err
	}
	im, err := LoadIssuesFromColumnList(ctx, bs, doer, org, isClosed)
	if err != nil {
		return 0, err
	}
	for _, il := range im {
		numIssuesInProject += len(il)
	}
	return numIssuesInProject, nil
}
