// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package project

import (
	"testing"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	project_model "forgejo.org/models/project"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"
	project_module "forgejo.org/modules/project"
	project_structs "forgejo.org/modules/structs"
	"forgejo.org/modules/util"
	"forgejo.org/modules/validation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	ownerID            = int64(2)
	orgOwnerID         = int64(3)
	repoID             = int64(2)
	isShowClosed       = false
	sortType           = ""
	templateType       = project_module.TemplateTypeNone
	cardType           = project_module.CardTypeTextOnly
	keyword            = "Title"
	projectTitle       = "Project"
	projectDescription = "Description"
	projectTypeOrg     = project_module.APIOwnerTypeOrganization
	projectTypeUser    = project_module.TypeIndividual
	projectTypeRepo    = project_module.APIOwnerTypeRepository
	columnTitle1       = "Title 1"
	columnTitle2       = "Title 2"
	columnColor        = "#23adff"
	page               = 1
	pageSize           = 10
	notExistStr        = "not exist"
	invalidStr         = "Validation Error"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}

func TestGetOwnerType(t *testing.T) {
	pT := GetAPIOwnerType(false, false)
	assert.Equal(t, project_module.APIOwnerTypeIndividual, pT)

	pT = GetAPIOwnerType(true, false)
	assert.Equal(t, project_module.APIOwnerTypeOrganization, pT)

	pT = GetAPIOwnerType(false, true)
	assert.Equal(t, project_module.APIOwnerTypeRepository, pT)

	pT = GetAPIOwnerType(true, true)
	assert.Equal(t, project_module.APIOwnerTypeOrganization, pT)
}

func TestGetSearchOpts(t *testing.T) {
	opts := GetSearchOpts(
		ownerID,
		isShowClosed,
		sortType,
		keyword,
		projectTypeOrg,
		page,
		pageSize,
	)
	require.NotNil(t, opts)
	assert.Equal(t, ownerID, opts.OwnerID)
	assert.Equal(t, optional.Some(isShowClosed), opts.IsClosed)
	assert.Equal(t, keyword, opts.Title)
	assert.Equal(t, projectTypeOrg, opts.Type.ToAPIOwnerType())
	assert.NotNil(t, opts.ListOptions)

	opts = GetSearchOpts(
		repoID,
		isShowClosed,
		sortType,
		keyword,
		projectTypeRepo,
		page,
		pageSize,
	)
	require.NotNil(t, opts)
	assert.Equal(t, repoID, opts.RepoID)
	assert.Equal(t, optional.Some(isShowClosed), opts.IsClosed)
	assert.Equal(t, keyword, opts.Title)
	assert.Equal(t, projectTypeRepo, opts.Type.ToAPIOwnerType())
	assert.NotNil(t, opts.ListOptions)

	opts = GetSearchOpts(
		repoID,
		!isShowClosed,
		"",
		"",
		projectTypeRepo,
	)
	require.NotNil(t, opts)
	assert.Equal(t, repoID, opts.RepoID)
	assert.Equal(t, optional.Some(!isShowClosed), opts.IsClosed)
	assert.Equal(t, db.SearchOrderByNewest, opts.OrderBy)
	assert.Empty(t, opts.Title)
	assert.Equal(t, projectTypeRepo, opts.Type.ToAPIOwnerType())
}

func TestListProjectByOptions(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	opts := &project_model.SearchOptions{
		OwnerID: 2,
		Type:    projectTypeUser,
	}
	projects, err := ListProjectsByOptions(t.Context(), opts)
	require.NoError(t, err)
	assert.EqualValues(t, 4, projects[0].ID)
}

func TestGetIssues(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	nonExistingIssueID := int64(999)

	// Case 1: All issues in the list exist
	issue1 := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})
	issue2 := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 2})
	issue3 := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 3})
	issueIDs := []int64{issue1.ID, issue2.ID, issue3.ID}
	issueList, complete, err := GetIssues(t.Context(), issueIDs)
	require.NoError(t, err)
	assert.Len(t, issueList, 3)
	assert.True(t, complete)

	// Case 2: An issueID does not exist as issue in DB
	issueIDs = append(issueIDs, nonExistingIssueID)
	_, complete, err = GetIssues(t.Context(), issueIDs)
	require.NoError(t, err)
	assert.False(t, complete)
}

func TestCountProjectsByOptions(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	opts := &project_model.SearchOptions{
		OwnerID: 2,
		Type:    projectTypeUser,
	}
	count, err := CountProjectsByOptions(t.Context(), opts)
	require.NoError(t, err)
	assert.EqualValues(t, 3, count)
}

func TestNewProject(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	org3 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3})
	repo2 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	var nilRepo *repo_model.Repository

	t.Run("individual", func(t *testing.T) {
		opts := project_structs.CreateOrUpdateProjectOptions{
			Title:        "Test",
			Description:  "Test",
			TemplateType: project_module.APITemplateTypeNone.String(),
			CardType:     project_module.APICardTypeTextOnly.String(),
		}

		proj, err := NewProject(&opts, user2, nilRepo, project_module.APIOwnerTypeIndividual)
		require.NoError(t, err)

		assert.Equal(t, opts.Title, proj.Title)
		assert.Equal(t, opts.Description, proj.Description)

		assert.Equal(t, project_module.TypeIndividual, proj.Type)
		assert.Equal(t, user2.ID, proj.OwnerID)
		assert.Equal(t, user2, proj.Owner)
		assert.Zero(t, proj.RepoID)
		assert.Nil(t, proj.Repo)
	})

	t.Run("individual, owner is org", func(t *testing.T) {
		opts := project_structs.CreateOrUpdateProjectOptions{
			Title:        "Test",
			Description:  "Test",
			TemplateType: project_module.APITemplateTypeNone.String(),
			CardType:     project_module.APICardTypeTextOnly.String(),
		}

		_, err := NewProject(&opts, org3, nilRepo, project_module.APIOwnerTypeIndividual)
		require.Error(t, err)
		assert.ErrorContains(t, err, "Type was TypeIndividual, but owner was org")
	})

	t.Run("individual, repo given", func(t *testing.T) {
		opts := project_structs.CreateOrUpdateProjectOptions{
			Title:        "Test",
			Description:  "Test",
			TemplateType: project_module.APITemplateTypeNone.String(),
			CardType:     project_module.APICardTypeTextOnly.String(),
		}

		_, err := NewProject(&opts, user2, repo2, project_module.APIOwnerTypeIndividual)
		require.Error(t, err)
		assert.ErrorContains(t, err, "Type was TypeIndividual, repo was given")
	})

	t.Run("organization", func(t *testing.T) {
		opts := project_structs.CreateOrUpdateProjectOptions{
			Title:        "Test",
			Description:  "Test",
			TemplateType: project_module.APITemplateTypeNone.String(),
			CardType:     project_module.APICardTypeTextOnly.String(),
		}

		proj, err := NewProject(&opts, org3, nilRepo, project_module.APIOwnerTypeOrganization)
		require.NoError(t, err)

		assert.Equal(t, opts.Title, proj.Title)
		assert.Equal(t, opts.Description, proj.Description)

		assert.Equal(t, project_module.TypeOrganization, proj.Type)
		assert.Equal(t, org3.ID, proj.OwnerID)
		assert.Equal(t, org3, proj.Owner)
		assert.Zero(t, proj.RepoID)
		assert.Nil(t, proj.Repo)
	})

	t.Run("organization, owner is individual", func(t *testing.T) {
		opts := project_structs.CreateOrUpdateProjectOptions{
			Title:        "Test",
			Description:  "Test",
			TemplateType: project_module.APITemplateTypeNone.String(),
			CardType:     project_module.APICardTypeTextOnly.String(),
		}

		_, err := NewProject(&opts, user2, nilRepo, project_module.APIOwnerTypeOrganization)
		require.Error(t, err)
		assert.ErrorContains(t, err, "Type was TypeOrganization, but owner was individual")
	})

	t.Run("organization, repo given", func(t *testing.T) {
		opts := project_structs.CreateOrUpdateProjectOptions{
			Title:        "Test",
			Description:  "Test",
			TemplateType: project_module.APITemplateTypeNone.String(),
			CardType:     project_module.APICardTypeTextOnly.String(),
		}

		_, err := NewProject(&opts, org3, repo2, project_module.APIOwnerTypeOrganization)
		require.Error(t, err)
		assert.ErrorContains(t, err, "Type was TypeOrganization, repo was given")
	})

	t.Run("repository", func(t *testing.T) {
		opts := project_structs.CreateOrUpdateProjectOptions{
			Title:        "Test",
			Description:  "Test",
			TemplateType: project_module.APITemplateTypeNone.String(),
			CardType:     project_module.APICardTypeTextOnly.String(),
		}

		proj, err := NewProject(&opts, user2, repo2, project_module.APIOwnerTypeRepository)
		require.NoError(t, err)

		assert.Equal(t, opts.Title, proj.Title)
		assert.Equal(t, opts.Description, proj.Description)

		assert.Equal(t, project_module.TypeRepository, proj.Type)
		assert.Zero(t, proj.OwnerID)
		assert.Nil(t, proj.Owner)
		assert.Equal(t, repo2.ID, proj.RepoID)
		assert.Equal(t, repo2, proj.Repo)
	})

	t.Run("repository, empty repo", func(t *testing.T) {
		opts := project_structs.CreateOrUpdateProjectOptions{
			Title:        "Test",
			Description:  "Test",
			TemplateType: project_module.APITemplateTypeNone.String(),
			CardType:     project_module.APICardTypeTextOnly.String(),
		}

		_, err := NewProject(&opts, user2, nilRepo, project_module.APIOwnerTypeRepository)
		require.Error(t, err)
		assert.ErrorContains(t, err, "Repo type given, but repo struct was empty")
	})

	t.Run("invalid card type", func(t *testing.T) {
		invalidCardType := project_module.APICardType("invalid")
		opts := project_structs.CreateOrUpdateProjectOptions{
			Title:        "Test",
			Description:  "Test",
			TemplateType: project_module.APITemplateTypeNone.String(),
			CardType:     invalidCardType.String(),
		}

		_, err := NewProject(&opts, user2, nilRepo, project_module.APIOwnerTypeRepository)
		require.Error(t, err)
		assert.ErrorContains(t, err, "Field APICardType")
	})

	t.Run("invalid template type", func(t *testing.T) {
		invalidTemplateType := project_module.APITemplateType("invalid")
		opts := project_structs.CreateOrUpdateProjectOptions{
			Title:        "Test",
			Description:  "Test",
			TemplateType: invalidTemplateType.String(),
			CardType:     project_module.APICardTypeTextOnly.String(),
		}

		_, err := NewProject(&opts, user2, nilRepo, project_module.APIOwnerTypeRepository)
		require.Error(t, err)
		assert.ErrorContains(t, err, "Field APITemplateType")
	})

	t.Run("invalid owner type", func(t *testing.T) {
		invalidOwnerType := project_module.APIOwnerType("99")
		opts := project_structs.CreateOrUpdateProjectOptions{
			Title:        "Test",
			Description:  "Test",
			TemplateType: project_module.APITemplateTypeNone.String(),
			CardType:     project_module.APICardTypeTextOnly.String(),
		}

		_, err := NewProject(&opts, user2, nilRepo, invalidOwnerType)
		require.Error(t, err)
		assert.ErrorContains(t, err, "Field APIOwnerType")
	})
}

func TestGetValidProjectColumnByID(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	validProjectID := int64(1)

	t.Run("Valid Column ID", func(t *testing.T) {
		validColumnID := int64(1)
		_, err := GetValidProjectColumnByID(t.Context(), validProjectID, validColumnID)
		require.NoError(t, err)
	})
	t.Run("Empty Column ID", func(t *testing.T) {
		emptyColumnID := int64(0)
		_, err := GetValidProjectColumnByID(t.Context(), validProjectID, emptyColumnID)
		assert.True(t, validation.IsErrNotValid(err))
	})
	t.Run("Not Existing Column ID", func(t *testing.T) {
		nonExistingColumnID := int64(99999)
		_, err := GetValidProjectColumnByID(t.Context(), validProjectID, nonExistingColumnID)
		assert.ErrorContains(t, err, notExistStr)
	})
	t.Run("Column Does Not Belong To Project", func(t *testing.T) {
		differentColID := int64(4)
		_, err := GetValidProjectColumnByID(t.Context(), validProjectID, differentColID)
		assert.ErrorIs(t, err, util.ErrInvalidArgument)
	})
}

func TestCreateColumnInProjectError(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	column1 := &project_model.Column{
		Title:     "Some title",
		ProjectID: 1,
		Color:     "Invalid Color",
	}

	err := CreateColumnInProject(t.Context(), column1)
	require.Error(t, err)
	assert.ErrorContains(t, err, "bad color code")
}

func TestEditColumnInProjectError(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	column1 := &project_model.Column{
		ID:        1,
		Title:     "Some title",
		ProjectID: 1,
		Color:     "Invalid Color",
	}

	err := EditColumnInProject(t.Context(), column1)
	require.Error(t, err)
	assert.ErrorContains(t, err, "bad color code")
}

func TestSetDefaultColumn(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	projectID := int64(1)
	columnID := int64(2)

	// column is not default
	column := unittest.AssertExistsAndLoadBean(t, &project_model.Column{ID: columnID})
	assert.False(t, column.Default)

	// set column as new default
	err := SetDefaultColumn(t.Context(), projectID, columnID)
	require.NoError(t, err)

	// column is default
	column = unittest.AssertExistsAndLoadBean(t, &project_model.Column{ID: columnID})
	assert.True(t, column.Default)
}

func TestValidIssueID(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	issue1 := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})
	issue2 := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 2})
	issue3 := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 3})

	list := issues_model.IssueList{issue1, issue2, issue3}

	// valid owner ID
	require.NoError(t, ValidIssueIDs(t.Context(), 2, list))

	// invalid owner ID
	require.Error(t, ValidIssueIDs(t.Context(), 1234567890, list))
}

func TestMoveIssuesOnProjectColumnErrors(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	column := unittest.AssertExistsAndLoadBean(t, &project_model.Column{ID: 1})

	for _, tt := range []struct {
		name          string
		projectIssues []project_structs.MoveProjectIssue
		expectError   string
	}{
		{
			name: "Duplicate Issue IDs",
			projectIssues: []project_structs.MoveProjectIssue{
				{IssueID: int64(1), Sorting: int64(2)},
				{IssueID: int64(1), Sorting: int64(1)},
			},
			expectError: "duplicate issue IDs",
		},
		{
			name: "Invalid Issue ID",
			projectIssues: []project_structs.MoveProjectIssue{
				{IssueID: int64(1234567890), Sorting: int64(2)},
				{IssueID: int64(1), Sorting: int64(1)},
			},
			expectError: "all issues must belong to the specified project",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pIs := &project_structs.MoveIssuesOption{
				ProjectIssues: tt.projectIssues,
			}
			err := MoveIssuesOnProjectColumn(t.Context(), column, pIs)
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.expectError)
		})
	}
}

func TestGetProjectByIDForOwner(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	// prepare individual, repository, organization project
	project := &project_model.Project{
		OwnerID:      ownerID,
		Title:        projectTitle,
		Type:         projectTypeUser,
		Description:  projectDescription,
		CreatorID:    ownerID,
		TemplateType: templateType,
		CardType:     cardType,
	}
	repoProject := &project_model.Project{
		RepoID:       repoID,
		Title:        projectTitle,
		Type:         projectTypeRepo.ToOwnerType(),
		Description:  projectDescription,
		CreatorID:    ownerID,
		TemplateType: templateType,
		CardType:     cardType,
	}
	orgProject := &project_model.Project{
		OwnerID:      orgOwnerID,
		Title:        projectTitle,
		Type:         projectTypeOrg.ToOwnerType(),
		Description:  projectDescription,
		CreatorID:    ownerID,
		TemplateType: templateType,
		CardType:     cardType,
	}
	require.NoError(t, CreateProject(t.Context(), project))
	require.NoError(t, CreateProject(t.Context(), repoProject))
	require.NoError(t, CreateProject(t.Context(), orgProject))

	t.Run("valid project", func(t *testing.T) {
		wantProject, err := GetProjectByIDForOwner(t.Context(), project.ID, ownerID, nil)
		require.NoError(t, err)
		assert.Equal(t, wantProject.Title, projectTitle)
	})

	t.Run("individual, wrong owner", func(t *testing.T) {
		_, err := GetProjectByIDForOwner(t.Context(), project.ID, 99, nil)
		assert.ErrorIs(t, err, util.ErrInvalidArgument)
	})

	t.Run("repository, wrong owner", func(t *testing.T) {
		_, err := GetProjectByIDForOwner(t.Context(), repoProject.ID, 0, &repo_model.Repository{ID: 99})
		assert.ErrorIs(t, err, util.ErrInvalidArgument)
	})

	t.Run("organization, wrong owner", func(t *testing.T) {
		_, err := GetProjectByIDForOwner(t.Context(), orgProject.ID, 99, nil)
		assert.ErrorIs(t, err, util.ErrInvalidArgument)
	})

	t.Run("user, not repository", func(t *testing.T) {
		_, err := GetProjectByIDForOwner(t.Context(), project.ID, 0, &repo_model.Repository{ID: ownerID})
		require.ErrorIs(t, err, util.ErrInvalidArgument)

		_, err = GetProjectByIDForOwner(t.Context(), orgProject.ID, 0, &repo_model.Repository{ID: ownerID})
		require.ErrorIs(t, err, util.ErrInvalidArgument)
	})

	t.Run("repo, not user", func(t *testing.T) {
		_, err := GetProjectByIDForOwner(t.Context(), repoProject.ID, repoID, nil)
		assert.ErrorIs(t, err, util.ErrInvalidArgument)
	})

	t.Run("not existing project", func(t *testing.T) {
		_, err := GetProjectByIDForOwner(t.Context(), 1234567890, 99, nil)
		assert.ErrorIs(t, err, util.ErrNotExist)
	})
}

func TestUpdateProject(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	// prepare project
	project := &project_model.Project{
		OwnerID:      ownerID,
		Title:        projectTitle,
		Type:         projectTypeUser,
		Description:  projectDescription,
		CreatorID:    ownerID,
		TemplateType: templateType,
		CardType:     cardType,
	}
	require.NoError(t, CreateProject(t.Context(), project))

	t.Run("invalid card type", func(t *testing.T) {
		updated := &project_structs.CreateOrUpdateProjectOptions{
			CardType: "does not exist",
		}
		err := UpdateProject(t.Context(), project, updated)
		require.Error(t, err)
		assert.ErrorContains(t, err, "Field APICardType")
	})

	t.Run("invalid status", func(t *testing.T) {
		updated := &project_structs.CreateOrUpdateProjectOptions{
			Status: "does not exist",
		}
		err := UpdateProject(t.Context(), project, updated)
		require.Error(t, err)
		assert.ErrorContains(t, err, "Field APIStatus")
	})

	t.Run("change status", func(t *testing.T) {
		// close
		updated := &project_structs.CreateOrUpdateProjectOptions{
			Status: "closed",
		}
		assert.False(t, project.IsClosed)
		err := UpdateProject(t.Context(), project, updated)
		require.NoError(t, err)

		project = unittest.AssertExistsAndLoadBean(t, project)
		assert.True(t, project.IsClosed)

		// open
		updated = &project_structs.CreateOrUpdateProjectOptions{
			Status: "open",
		}
		err = UpdateProject(t.Context(), project, updated)
		require.NoError(t, err)

		project = unittest.AssertExistsAndLoadBean(t, project)
		assert.False(t, project.IsClosed)
	})
}

func TestCUDProject(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	project := &project_model.Project{
		OwnerID:      ownerID,
		Title:        projectTitle,
		Type:         projectTypeUser,
		Description:  projectDescription,
		CreatorID:    ownerID,
		TemplateType: templateType,
		CardType:     cardType,
	}
	newTitle := "Updated Title"
	newDescription := "Updated Description"

	// create project
	err := CreateProject(t.Context(), project)
	require.NoError(t, err)

	wantProject, err := GetProjectByIDForOwner(t.Context(), project.ID, ownerID, nil)
	require.NoError(t, err)
	assert.Equal(t, wantProject.Title, projectTitle)

	// update project
	updated := &project_structs.CreateOrUpdateProjectOptions{
		Title:       newTitle,
		Description: newDescription,
	}
	err = UpdateProject(t.Context(), wantProject, updated)
	require.NoError(t, err)
	assert.Equal(t, wantProject.Title, newTitle)
	assert.Equal(t, wantProject.Description, newDescription)

	t.Run("TestCRUDColumn", func(t *testing.T) {
		column1 := &project_model.Column{
			Title:     columnTitle1,
			ProjectID: project.ID,
		}

		column2 := &project_model.Column{
			Title:     columnTitle2,
			ProjectID: project.ID,
			Color:     columnColor,
		}

		// create column
		err := CreateColumnInProject(t.Context(), column1)
		require.NoError(t, err)
		assert.True(t, column1.Default)

		wantCol1, _ := GetValidProjectColumnByID(t.Context(), project.ID, column1.ID)
		assert.Equal(t, wantCol1.Title, columnTitle1)

		// create another column
		err = CreateColumnInProject(t.Context(), column2)
		require.NoError(t, err)

		wantCol2, err := GetValidProjectColumnByID(t.Context(), project.ID, column2.ID)
		require.NoError(t, err)
		assert.Equal(t, wantCol2.Title, columnTitle2)
		assert.False(t, wantCol2.Default)

		// try deleting default column
		err = DeleteColumnInProject(t.Context(), column1.ID)
		require.Error(t, err) // Can not delete default col
		require.ErrorContains(t, err, "cannot delete default column")

		// delete other column
		err = DeleteColumnInProject(t.Context(), column2.ID)
		require.NoError(t, err)
		unittest.AssertNotExistsBean(t, &project_model.Column{ID: column2.ID})
	})

	t.Run("TestCRUDProjectIssues", func(t *testing.T) {
		// The yml's in models/fixtures provide information about the test DB
		require.NoError(t, unittest.PrepareTestDatabase())
		column1 := unittest.AssertExistsAndLoadBean(t, &project_model.Column{ID: 1})
		project := unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: 1})
		pI1 := unittest.AssertExistsAndLoadBean(t, &project_model.ProjectIssue{ID: 1})
		pI2 := unittest.AssertExistsAndLoadBean(t, &project_model.ProjectIssue{ID: 2})

		// move/sort issues
		pIs := &project_structs.MoveIssuesOption{
			ProjectIssues: []project_structs.MoveProjectIssue{
				{
					IssueID: pI1.IssueID,
					Sorting: int64(2),
				},
				{
					IssueID: pI2.IssueID,
					Sorting: int64(1),
				},
			},
		}
		err = MoveIssuesOnProjectColumn(t.Context(), column1, pIs)
		require.NoError(t, err)

		pI1 = unittest.AssertExistsAndLoadBean(t, &project_model.ProjectIssue{ID: 1})
		pI2 = unittest.AssertExistsAndLoadBean(t, &project_model.ProjectIssue{ID: 2})
		assert.EqualValues(t, 2, pI1.Sorting)
		assert.EqualValues(t, 1, pI2.Sorting)
		assert.EqualValues(t, 1, pI1.ProjectColumnID)
		assert.EqualValues(t, 1, pI2.ProjectColumnID)

		// get issues in default column
		defaultCol, err := project.GetDefaultColumn(t.Context())
		require.NoError(t, err)

		defaultIssues, err := db.Find[project_model.ProjectIssue](t.Context(),
			project_model.FindProjectIssueOptions{
				ListOptions:     db.ListOptionsAll,
				ProjectID:       defaultCol.ProjectID,
				ProjectColumnID: defaultCol.ID,
			})
		require.NoError(t, err)

		assert.Contains(t, defaultIssues, pI1)
		assert.Contains(t, defaultIssues, pI2)
	})

	// delete project
	err = DeleteProjectByID(t.Context(), wantProject.ID, optional.None[int64]())
	require.NoError(t, err)
}
