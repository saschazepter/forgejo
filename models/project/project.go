// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package project

import (
	"context"
	"errors"
	"fmt"
	"html/template"

	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/log"
	"forgejo.org/modules/optional"
	project_module "forgejo.org/modules/project"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"

	"xorm.io/builder"
)

// ErrProjectNotExist represents a "ProjectNotExist" kind of error.
type ErrProjectNotExist struct {
	ID     int64
	RepoID int64
}

// IsErrProjectNotExist checks if an error is a ErrProjectNotExist
func IsErrProjectNotExist(err error) bool {
	_, ok := errors.AsType[ErrProjectNotExist](err)
	return ok
}

func (err ErrProjectNotExist) Error() string {
	return fmt.Sprintf("project does not exist [id: %d]", err.ID)
}

func (err ErrProjectNotExist) Unwrap() error {
	return util.ErrNotExist
}

// Project represents a project
type Project struct {
	ID           int64                       `xorm:"pk autoincr"`
	Title        string                      `xorm:"INDEX NOT NULL"`
	Description  string                      `xorm:"TEXT"`
	OwnerID      int64                       `xorm:"INDEX"`
	Owner        *user_model.User            `xorm:"-"`
	RepoID       int64                       `xorm:"INDEX"`
	Repo         *repo_model.Repository      `xorm:"-"`
	CreatorID    int64                       `xorm:"NOT NULL"`
	IsClosed     bool                        `xorm:"INDEX"`
	TemplateType project_module.TemplateType `xorm:"'board_type'"` // TODO: rename the column to template_type
	CardType     project_module.CardType
	Type         project_module.OwnerType

	RenderedContent template.HTML `xorm:"-"`

	CreatedUnix    timeutil.TimeStamp `xorm:"INDEX created"`
	UpdatedUnix    timeutil.TimeStamp `xorm:"INDEX updated"`
	ClosedDateUnix timeutil.TimeStamp
}

// Ghost Project is a project which has been deleted
const GhostProjectID = -1

func (p *Project) IsGhost() bool {
	return p.ID == GhostProjectID
}

func (p *Project) LoadOwner(ctx context.Context) (err error) {
	if p.Owner != nil {
		return nil
	}
	p.Owner, err = user_model.GetUserByID(ctx, p.OwnerID)
	return err
}

func (p *Project) LoadRepo(ctx context.Context) (err error) {
	if p.RepoID == 0 || p.Repo != nil {
		return nil
	}
	p.Repo, err = repo_model.GetRepositoryByID(ctx, p.RepoID)
	return err
}

// Link returns the project's relative URL.
func (p *Project) Link(ctx context.Context) string {
	// nosemgrep: forgejo-logic-suspicious-OwnerID-check (system users are not stored in the database)
	if p.OwnerID > 0 {
		err := p.LoadOwner(ctx)
		if err != nil {
			log.Error("LoadOwner: %v", err)
			return ""
		}
		return project_module.ProjectLinkForOrg(p.Owner.HomeLink(), p.ID)
	}
	if p.RepoID > 0 {
		err := p.LoadRepo(ctx)
		if err != nil {
			log.Error("LoadRepo: %v", err)
			return ""
		}
		return project_module.ProjectLinkForRepo(p.Repo.Link(), p.ID)
	}
	return ""
}

func (p *Project) IconName() string {
	if p.IsRepositoryProject() {
		return "octicon-project"
	}
	return "octicon-project-symlink"
}

func (p *Project) IsOrganizationProject() bool {
	return p.Type == project_module.TypeOrganization
}

func (p *Project) IsRepositoryProject() bool {
	return p.Type == project_module.TypeRepository
}

func (p *Project) CanBeAccessedByOwnerRepo(ownerID int64, repo *repo_model.Repository) bool {
	if p.Type == project_module.TypeRepository {
		return repo != nil && p.RepoID == repo.ID // if a project belongs to a repository, then its OwnerID is 0 and can be ignored
	}
	return p.OwnerID == ownerID && p.RepoID == 0
}

func init() {
	db.RegisterModel(new(Project))
}

// SearchOptions are options for GetProjects
type SearchOptions struct {
	db.ListOptions
	OwnerID  int64
	RepoID   int64
	IsClosed optional.Option[bool]
	OrderBy  db.SearchOrderBy
	Type     project_module.OwnerType
	Title    string
}

func (opts SearchOptions) ToConds() builder.Cond {
	cond := builder.NewCond()
	if opts.RepoID > 0 {
		cond = cond.And(builder.Eq{"repo_id": opts.RepoID})
	}
	if has, value := opts.IsClosed.Get(); has {
		cond = cond.And(builder.Eq{"is_closed": value})
	}

	if opts.Type > 0 {
		cond = cond.And(builder.Eq{"type": opts.Type})
	}
	if opts.OwnerID != 0 {
		cond = cond.And(builder.Eq{"owner_id": opts.OwnerID})
	}

	if len(opts.Title) != 0 {
		cond = cond.And(db.BuildCaseInsensitiveLike("title", opts.Title))
	}
	return cond
}

func (opts SearchOptions) ToOrders() string {
	return opts.OrderBy.String()
}

func GetSearchOrderBySortType(sortType string) db.SearchOrderBy {
	switch sortType {
	case "oldest":
		return db.SearchOrderByOldest
	case "recentupdate":
		return db.SearchOrderByRecentUpdated
	case "leastupdate":
		return db.SearchOrderByLeastUpdated
	default:
		return db.SearchOrderByNewest
	}
}

// CreateProject Creates a new Project and expects a valid project which is generated in the respective service function.
// The title will be cut off at 255 characters if it's longer than 255 characters.
func CreateProject(ctx context.Context, p *Project) error {
	p.Title, _ = util.SplitStringAtByteN(p.Title, 255)

	return db.WithTx(ctx, func(ctx context.Context) error {
		if err := db.Insert(ctx, p); err != nil {
			return err
		}

		if p.RepoID > 0 {
			if _, err := db.Exec(ctx, "UPDATE `repository` SET num_projects = num_projects + 1 WHERE id = ?", p.RepoID); err != nil {
				return err
			}
		}

		return createDefaultColumnsForProject(ctx, p)
	})
}

// GetProjectByID Fetches a Project by it ID.
func GetProjectByID(ctx context.Context, id int64) (*Project, error) {
	p := new(Project)

	has, err := db.GetEngine(ctx).ID(id).Get(p)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, ErrProjectNotExist{ID: id}
	}

	return p, nil
}

// UpdateProject Updates only the following Project properties: Title, Description, CardType.
// Expects a valid project which is generated in the respective service function.
func UpdateProject(ctx context.Context, p *Project) error {
	p.Title, _ = util.SplitStringAtByteN(p.Title, 255)
	_, err := db.GetEngine(ctx).ID(p.ID).Cols(
		"title",
		"description",
		"card_type",
	).Update(p)
	return err
}

func updateRepositoryProjectCount(ctx context.Context, repoID int64) error {
	if _, err := db.GetEngine(ctx).Exec(builder.Update(
		builder.Eq{
			"`num_projects`": builder.Select("count(*)").From("`project`").
				Where(builder.Eq{"`project`.`repo_id`": repoID}.
					And(builder.Eq{"`project`.`type`": project_module.TypeRepository})),
		},
	).From("`repository`").Where(builder.Eq{"id": repoID})); err != nil {
		return err
	}

	if _, err := db.GetEngine(ctx).Exec(builder.Update(
		builder.Eq{
			"`num_closed_projects`": builder.Select("count(*)").From("`project`").
				Where(builder.Eq{"`project`.`repo_id`": repoID}.
					And(builder.Eq{"`project`.`type`": project_module.TypeRepository}).
					And(builder.Eq{"`project`.`is_closed`": true})),
		},
	).From("`repository`").Where(builder.Eq{"id": repoID})); err != nil {
		return err
	}
	return nil
}

// ChangeProjectStatus Changes the status of the specified project to the given state.
// Specified via the `isClosed` argument.
func ChangeProjectStatus(ctx context.Context, p *Project, isClosed bool) error {
	if p.IsClosed == isClosed {
		return nil
	}

	return db.WithTx(ctx, func(ctx context.Context) error {
		p.IsClosed = isClosed
		p.ClosedDateUnix = timeutil.TimeStampNow()
		count, err := db.GetEngine(ctx).ID(p.ID).Cols("is_closed", "closed_date_unix").Update(p)
		if err != nil {
			return err
		}
		if count < 1 {
			return nil
		}

		return updateRepositoryProjectCount(ctx, p.RepoID)
	})
}

// DeleteProjectByID Deletes a project by ID.
// If it's not in a database transaction, it will start a new database transaction.
// repoID is optional and only needed, when updating a Project that belongs to a repository.
// Leave empty if the OwnerType is not repository.
func DeleteProjectByID(ctx context.Context, id int64, repoID optional.Option[int64]) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		if err := deleteProjectIssuesByProjectID(ctx, id); err != nil {
			return err
		}

		if err := deleteColumnByProjectID(ctx, id); err != nil {
			return err
		}

		if _, err := db.GetEngine(ctx).ID(id).Delete(new(Project)); err != nil {
			return err
		}

		has, id := repoID.Get()
		if has {
			return updateRepositoryProjectCount(ctx, id)
		}
		return nil
	})
}

func DeleteProjectByRepoID(ctx context.Context, repoID int64) error {
	switch {
	case setting.Database.Type.IsSQLite3():
		if _, err := db.GetEngine(ctx).Exec("DELETE FROM project_issue WHERE project_issue.id IN (SELECT project_issue.id FROM project_issue INNER JOIN project WHERE project.id = project_issue.project_id AND project.repo_id = ?)", repoID); err != nil {
			return err
		}
		if _, err := db.GetEngine(ctx).Exec("DELETE FROM project_board WHERE project_board.id IN (SELECT project_board.id FROM project_board INNER JOIN project WHERE project.id = project_board.project_id AND project.repo_id = ?)", repoID); err != nil {
			return err
		}
		if _, err := db.GetEngine(ctx).Table("project").Where("repo_id = ? ", repoID).Delete(&Project{}); err != nil {
			return err
		}
	case setting.Database.Type.IsPostgreSQL():
		if _, err := db.GetEngine(ctx).Exec("DELETE FROM project_issue USING project WHERE project.id = project_issue.project_id AND project.repo_id = ? ", repoID); err != nil {
			return err
		}
		if _, err := db.GetEngine(ctx).Exec("DELETE FROM project_board USING project WHERE project.id = project_board.project_id AND project.repo_id = ? ", repoID); err != nil {
			return err
		}
		if _, err := db.GetEngine(ctx).Table("project").Where("repo_id = ? ", repoID).Delete(&Project{}); err != nil {
			return err
		}
	default:
		if _, err := db.GetEngine(ctx).Exec("DELETE project_issue FROM project_issue INNER JOIN project ON project.id = project_issue.project_id WHERE project.repo_id = ? ", repoID); err != nil {
			return err
		}
		if _, err := db.GetEngine(ctx).Exec("DELETE project_board FROM project_board INNER JOIN project ON project.id = project_board.project_id WHERE project.repo_id = ? ", repoID); err != nil {
			return err
		}
		if _, err := db.GetEngine(ctx).Table("project").Where("repo_id = ? ", repoID).Delete(&Project{}); err != nil {
			return err
		}
	}

	return updateRepositoryProjectCount(ctx, repoID)
}
