// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package project

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"forgejo.org/models/db"
	"forgejo.org/modules/container"
	project_module "forgejo.org/modules/project"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"

	"xorm.io/builder"
)

type (
	// ColumnList is a list of all project columns in a repository
	ColumnList []*Column
)

// ColumnColorPattern is a regexp witch can validate ColumnColor
var ColumnColorPattern = regexp.MustCompile("^#[0-9a-fA-F]{6}$")

// Column is used to represent column on a project
type Column struct {
	ID      int64 `xorm:"pk autoincr"`
	Title   string
	Default bool   `xorm:"NOT NULL DEFAULT false"` // issues not assigned to a specific column will be assigned to this column
	Sorting int8   `xorm:"NOT NULL DEFAULT 0 unique(project_sorting)"`
	Color   string `xorm:"VARCHAR(7)"`

	ProjectID int64 `xorm:"INDEX NOT NULL unique(project_sorting)"`
	CreatorID int64 `xorm:"NOT NULL"`

	CreatedUnix timeutil.TimeStamp `xorm:"INDEX created"`
	UpdatedUnix timeutil.TimeStamp `xorm:"INDEX updated"`
}

// TableName return the real table name
func (Column) TableName() string {
	return "project_board" // TODO: the legacy table name should be project_column
}

// FindColumnOptions contains options to find columns.
type FindColumnOptions struct {
	db.ListOptions
	ProjectID int64
}

func (opts FindColumnOptions) ToConds() builder.Cond {
	cond := builder.NewCond()
	if opts.ProjectID != 0 {
		cond = cond.And(builder.Eq{"project_id": opts.ProjectID})
	}
	return cond
}

func (opts FindColumnOptions) ToOrders() string {
	return "sorting, id"
}

func init() {
	db.RegisterModel(new(Column))
}

// ErrProjectColumnNotExist represents a "ErrProjectColumnNotExist" kind of error.
type ErrProjectColumnNotExist struct {
	ColumnID int64
}

// IsErrProjectColumnNotExist checks if an error is a ErrProjectColumnNotExist
func IsErrProjectColumnNotExist(err error) bool {
	_, ok := errors.AsType[ErrProjectColumnNotExist](err)
	return ok
}

func (err ErrProjectColumnNotExist) Error() string {
	return fmt.Sprintf("project column does not exist [id: %d]", err.ColumnID)
}

func (err ErrProjectColumnNotExist) Unwrap() error {
	return util.ErrNotExist
}

func createDefaultColumnsForProject(ctx context.Context, project *Project) error {
	var items []string

	switch project.TemplateType {
	case project_module.TemplateTypeBugTriage:
		items = setting.Project.ProjectBoardBugTriageType
	case project_module.TemplateTypeBasicKanban:
		items = setting.Project.ProjectBoardBasicKanbanType
	case project_module.TemplateTypeNone:
		fallthrough
	default:
		return nil
	}

	return db.WithTx(ctx, func(ctx context.Context) error {
		column := Column{
			CreatedUnix: timeutil.TimeStampNow(),
			CreatorID:   project.CreatorID,
			Title:       "Backlog",
			ProjectID:   project.ID,
			Default:     true,
			Sorting:     0,
		}
		if err := db.Insert(ctx, &column); err != nil {
			return err
		}

		if len(items) == 0 {
			return nil
		}

		columns := make([]Column, 0, len(items))
		for i, v := range items {
			columns = append(columns, Column{
				CreatedUnix: timeutil.TimeStampNow(),
				CreatorID:   project.CreatorID,
				Title:       v,
				ProjectID:   project.ID,
				Sorting:     int8(i + 1),
			})
		}

		return db.Insert(ctx, columns)
	})
}

// maxProjectColumns max columns allowed in a project, this should not bigger than 127
// because sorting is int8 in database
const maxProjectColumns = 20

// CreateColumn adds a new project column to a given project
func CreateColumn(ctx context.Context, column *Column) error {
	if len(column.Color) != 0 && !ColumnColorPattern.MatchString(column.Color) {
		return fmt.Errorf("bad color code: %s", column.Color)
	}
	res := struct {
		MaxSorting  int64
		ColumnCount int64
	}{}
	if _, err := db.GetEngine(ctx).Select("max(sorting) as max_sorting, count(*) as column_count").Table("project_board").
		Where("project_id=?", column.ProjectID).Get(&res); err != nil {
		return err
	}
	if res.ColumnCount >= maxProjectColumns {
		return errors.New("maximum number of columns reached")
	}
	// This is the first column in this project to be created, so we make it default
	if res.ColumnCount == 0 {
		column.Default = true
	}
	column.Sorting = int8(util.Iif(res.ColumnCount > 0, res.MaxSorting+1, 0))
	_, err := db.GetEngine(ctx).Insert(column)
	return err
}

// DeleteColumnByID removes all issues references to the project column.
func DeleteColumnByID(ctx context.Context, columnID int64) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		return deleteColumnByID(ctx, columnID)
	})
}

func deleteColumnByID(ctx context.Context, columnID int64) error {
	column, err := GetColumn(ctx, columnID)
	if err != nil {
		if IsErrProjectColumnNotExist(err) {
			return nil
		}

		return err
	}

	if column.Default {
		return errors.New("deleteColumnByID: cannot delete default column")
	}

	// move all issues to the default column
	project, err := GetProjectByID(ctx, column.ProjectID)
	if err != nil {
		return err
	}
	defaultColumn, err := project.GetDefaultColumn(ctx)
	if err != nil {
		return err
	}

	if err = column.moveIssuesToAnotherColumn(ctx, defaultColumn); err != nil {
		return err
	}

	if _, err := db.GetEngine(ctx).ID(column.ID).NoAutoCondition().Delete(column); err != nil {
		return err
	}
	return nil
}

func deleteColumnByProjectID(ctx context.Context, projectID int64) error {
	_, err := db.GetEngine(ctx).Where("project_id=?", projectID).Delete(&Column{})
	return err
}

// GetColumn fetches the current column of a project
func GetColumn(ctx context.Context, columnID int64) (*Column, error) {
	column := new(Column)
	has, err := db.GetEngine(ctx).ID(columnID).Get(column)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, ErrProjectColumnNotExist{ColumnID: columnID}
	}

	return column, nil
}

// UpdateColumn updates a project column
func UpdateColumn(ctx context.Context, column *Column) error {
	var fieldToUpdate []string

	fieldToUpdate = append(fieldToUpdate, "sorting")

	if column.Title != "" {
		fieldToUpdate = append(fieldToUpdate, "title")
	}

	if len(column.Color) != 0 && !ColumnColorPattern.MatchString(column.Color) {
		return fmt.Errorf("bad color code: %s", column.Color)
	}
	fieldToUpdate = append(fieldToUpdate, "color")

	_, err := db.GetEngine(ctx).ID(column.ID).Cols(fieldToUpdate...).Update(column)

	return err
}

// GetDefaultColumn return default column and ensure only one exists
func (p *Project) GetDefaultColumn(ctx context.Context) (*Column, error) {
	var column Column
	has, err := db.GetEngine(ctx).
		Where("project_id=? AND `default` = ?", p.ID, true).
		Desc("id").Get(&column)
	if err != nil {
		return nil, err
	}

	if has {
		return &column, nil
	}

	// create a default column if none is found, using the next available sorting value
	res := struct {
		MaxSorting  int64
		ColumnCount int64
	}{}
	if _, err := db.GetEngine(ctx).Select("max(sorting) as max_sorting, count(*) as column_count").
		Table("project_board").Where("project_id=?", p.ID).Get(&res); err != nil {
		return nil, err
	}

	column = Column{
		ProjectID: p.ID,
		Default:   true,
		Title:     "Uncategorized",
		CreatorID: p.CreatorID,
		Sorting:   int8(util.Iif(res.ColumnCount > 0, res.MaxSorting+1, 0)),
	}
	if _, err := db.GetEngine(ctx).Insert(&column); err != nil {
		return nil, err
	}
	return &column, nil
}

// SetDefaultColumn represents a column for issues not assigned to one
func SetDefaultColumn(ctx context.Context, projectID, columnID int64) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := GetColumn(ctx, columnID); err != nil {
			return err
		}

		if _, err := db.GetEngine(ctx).Where(builder.Eq{
			"project_id": projectID,
			"`default`":  true,
		}).Cols("`default`").Update(&Column{Default: false}); err != nil {
			return err
		}

		_, err := db.GetEngine(ctx).ID(columnID).
			Where(builder.Eq{"project_id": projectID}).
			Cols("`default`").Update(&Column{Default: true})
		return err
	})
}

func GetColumnsByIDs(ctx context.Context, projectID int64, columnsIDs []int64) (ColumnList, error) {
	columns := make([]*Column, 0, 5)
	if len(columnsIDs) == 0 {
		return columns, nil
	}
	if err := db.GetEngine(ctx).
		Where("project_id =?", projectID).
		In("id", columnsIDs).
		OrderBy("sorting").Find(&columns); err != nil {
		return nil, err
	}
	return columns, nil
}

// MoveColumnsOnProject sorts columns in a project using a two-phase approach
// to avoid unique constraint collisions during swap operations.
// All columns in the project must be included in the sortedColumnIDs map.
func MoveColumnsOnProject(ctx context.Context, projectID int64, sortedColumnIDs map[int64]int64) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		sess := db.GetEngine(ctx)

		// Validate no duplicate column IDs in map values
		columnIDSet := make(container.Set[int64], len(sortedColumnIDs))
		for _, columnID := range sortedColumnIDs {
			if !columnIDSet.Add(columnID) {
				return errors.New("duplicate column ID in reorder request")
			}
		}

		// Validate all columns exist and belong to this project
		allColumns, err := db.Find[Column](ctx, FindColumnOptions{
			ListOptions: db.ListOptionsAll, ProjectID: projectID,
		})
		if err != nil {
			return err
		}
		if len(allColumns) != len(sortedColumnIDs) {
			return errors.New("all columns in the project must be included in the reorder request")
		}

		columnIDs := util.ValuesOfMap(sortedColumnIDs)
		movedColumns, err := GetColumnsByIDs(ctx, projectID, columnIDs)
		if err != nil {
			return err
		}
		if len(movedColumns) != len(sortedColumnIDs) {
			return errors.New("some columns do not exist in this project")
		}

		// Build reverse map: columnID → target sorting
		targetSortingByColumn := make(map[int64]int64, len(sortedColumnIDs))
		for sorting, columnID := range sortedColumnIDs {
			targetSortingByColumn[columnID] = sorting
		}

		// Phase 1: negate using target sorting values (guaranteed unique since
		// they are map keys) to avoid unique constraint collisions during swap
		for _, column := range movedColumns {
			targetSorting := targetSortingByColumn[column.ID]
			if _, err := sess.Exec("UPDATE `project_board` SET sorting=? WHERE id=?",
				-(targetSorting + 1), column.ID); err != nil {
				return err
			}
		}

		// Phase 2: set final values
		for sorting, columnID := range sortedColumnIDs {
			if _, err := sess.Exec("UPDATE `project_board` SET sorting=? WHERE id=?",
				sorting, columnID); err != nil {
				return err
			}
		}
		return nil
	})
}
