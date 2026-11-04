// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"cmp"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"

	auth_model "forgejo.org/models/auth"
	project_model "forgejo.org/models/project"
	"forgejo.org/models/unittest"
	project_module "forgejo.org/modules/project"
	api "forgejo.org/modules/structs"
	"forgejo.org/services/convert"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runOpts struct {
	token     string
	owner     string
	repo      string
	ownerType project_module.APIOwnerType
}

func getProjectAPIBaseString(opts *runOpts) string {
	var projectAPIBaseString string
	switch opts.ownerType {
	case project_module.APIOwnerTypeIndividual:
		projectAPIBaseString = "/api/v1/users/" + opts.owner
	case project_module.APIOwnerTypeOrganization:
		projectAPIBaseString = "/api/v1/orgs/" + opts.owner
	case project_module.APIOwnerTypeRepository:
		projectAPIBaseString = "/api/v1/repos/" + opts.owner + "/" + opts.repo
	}
	return projectAPIBaseString
}

// createProject creates a project.
func createProject(t *testing.T, runOpts *runOpts, projectName string) api.Project {
	var project api.Project
	endpoint := getProjectAPIBaseString(runOpts) + "/projects"
	resp := jsonRequestWithAuth(t, runOpts.token, "POST", endpoint, http.StatusCreated,
		&api.CreateOrUpdateProjectOptions{
			Title:        projectName,
			Description:  projectName,
			TemplateType: project_module.APITemplateTypeNone.String(),
			CardType:     project_module.APICardTypeTextOnly.String(),
		},
	)
	DecodeJSON(t, resp, &project)
	return project
}

func getProject(t *testing.T, token, projectAPIBaseString string, pID int64, status int) *httptest.ResponseRecorder {
	return projectsIDEndpoint(t, token, "GET", projectAPIBaseString, pID, status)
}

func projectsIDEndpoint(t *testing.T, token, method, projectAPIBaseString string, pID int64, status int) *httptest.ResponseRecorder {
	projectAPIString := fmt.Sprintf("%v/projects/%v", projectAPIBaseString, pID)
	return requestWithAuth(t, token, method, projectAPIString, status)
}

func requestWithAuth(
	t *testing.T,
	token, method, endpoint string,
	status int,
) *httptest.ResponseRecorder {
	req := NewRequest(t, method, endpoint).AddTokenAuth(token)
	return MakeRequest(t, req, status)
}

func jsonRequestWithAuth(t *testing.T, token, method, endpoint string, statusCode int, opts any) *httptest.ResponseRecorder {
	req := NewRequestWithJSON(
		t, method,
		endpoint,
		&opts,
	).AddTokenAuth(token)
	resp := MakeRequest(t, req, statusCode)
	return resp
}

// TestProjectAPIListProjectsPagination tests ListProjects in the Project API
// with pagination.
func TestProjectAPIListProjectsPagination(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	err := unittest.PrepareTestDatabase()
	require.NoError(t, err)

	// user and token, repo
	user := forgery.CreateUser(t, nil)
	session := loginUser(t, user.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteProject)
	repo := forgery.CreateRepository(t, user, nil)

	// create projects
	numProjects := 100
	projects := []*api.Project{}
	for range numProjects {
		projects = append(projects, convert.ToAPIProject(forgery.CreateProject(t, repo, nil)))
	}

	// list projects
	limit := 10   // maximum number of entries in api call response
	numCalls := 0 // number of performed api calls
	gotProjects := []*api.Project{}
	for i := range numProjects {
		var projResp []*api.Project
		resp := requestWithAuth(
			t, token, "GET",
			fmt.Sprintf(
				"/api/v1/repos/%v/%v/projects?page=%d&limit=%d",
				user.Name,
				repo.Name,
				i+1,
				limit,
			),
			http.StatusOK,
		)
		DecodeJSON(t, resp, &projResp)
		numCalls++

		assert.Equal(t, strconv.Itoa(numProjects), resp.Result().Header.Get("X-Total-Count"))
		assert.NotEmpty(t, resp.Result().Header.Get("Link"))
		gotProjects = append(gotProjects, projResp...)

		if len(gotProjects) == numProjects {
			break
		}
	}
	assert.Len(t, gotProjects, numProjects)
	projSort := func(a, b *api.Project) int {
		return cmp.Compare(a.ID, b.ID)
	}
	slices.SortFunc(projects, projSort)
	slices.SortFunc(gotProjects, projSort)
	assert.Equal(t, projects, gotProjects)
	assert.Equal(t, numProjects/limit, numCalls)
}

// Test the use cases
func TestProjectAPICRUD(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	err := unittest.PrepareTestDatabase()
	require.NoError(t, err)

	// user and token, org, repos
	user := forgery.CreateUser(t, nil)
	session := loginUser(t, user.Name)
	writeToken := getTokenForLoggedInUser(t, session,
		auth_model.AccessTokenScopeWriteProject,
		auth_model.AccessTokenScopeWriteIssue,
	)
	readToken := getTokenForLoggedInUser(t, session,
		auth_model.AccessTokenScopeReadProject,
	)
	org := forgery.CreateOrganisation(t, user)
	userRepo := forgery.CreateRepository(t, user, nil)
	orgRepo := forgery.CreateRepository(t, org.AsUser(), nil)

	isClosed := func(s string) bool {
		if s == "closed" {
			return true
		}
		return false
	}

	for _, tt := range []struct {
		name      string
		owner     string
		ownerType project_module.APIOwnerType
		repo      string
		baseURL   string
	}{
		{
			name:      "User",
			owner:     user.Name,
			ownerType: project_module.APIOwnerTypeIndividual,
			repo:      userRepo.Name,
			baseURL:   fmt.Sprintf("/api/v1/users/%s", user.Name),
		},
		{
			name:      "Org",
			owner:     org.Name,
			ownerType: project_module.APIOwnerTypeOrganization,
			repo:      orgRepo.Name,
			baseURL:   fmt.Sprintf("/api/v1/orgs/%s", org.Name),
		},
		{
			name:      "User Repo",
			owner:     user.Name,
			ownerType: project_module.APIOwnerTypeRepository,
			repo:      userRepo.Name,
			baseURL:   fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
		},
		{
			name:      "Org Repo",
			owner:     org.Name,
			ownerType: project_module.APIOwnerTypeRepository,
			repo:      orgRepo.Name,
			baseURL:   fmt.Sprintf("/api/v1/repos/%s/%s", org.Name, orgRepo.Name),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Create, Get project for an owner
			project := createProject(t, &runOpts{
				token:     writeToken,
				owner:     tt.owner,
				repo:      tt.repo,
				ownerType: tt.ownerType,
			}, "Project 1")

			assert.NotZero(t, project.ID)
			assert.Equal(t, "Project 1", project.Title)
			assert.Equal(t, "Project 1", project.Description)
			if tt.ownerType == project_module.APIOwnerTypeRepository {
				assert.Empty(t, project.OwnerName)
				assert.Equal(t, tt.repo, project.RepoName)
			} else {
				assert.Equal(t, tt.owner, project.OwnerName)
				assert.Empty(t, project.RepoName)
			}
			assert.Equal(t, "open", project.Status)
			assert.Equal(t, project_module.APITemplateTypeNone.String(), project.TemplateType)
			assert.Equal(t, project_module.APICardTypeTextOnly.String(), project.CardType)

			resp := getProject(t, readToken, tt.baseURL, project.ID, http.StatusOK)
			var projResp api.Project
			DecodeJSON(t, resp, &projResp)

			assert.Equal(t, project.ID, projResp.ID)
			assert.Equal(t, project.Title, projResp.Title)
			assert.Equal(t, project.Description, projResp.Description)
			assert.Equal(t, project.OwnerName, projResp.OwnerName)
			assert.Equal(t, project.RepoName, projResp.RepoName)
			assert.Equal(t, project.Status, projResp.Status)
			assert.Equal(t, project.OwnerType, projResp.OwnerType)
			assert.Equal(t, project.TemplateType, projResp.TemplateType)
			assert.Equal(t, project.CardType, projResp.CardType)

			// Update properties of a project
			t.Run("Update properties of project", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				projOpts := api.CreateOrUpdateProjectOptions{
					Title:       "Project 15",
					Description: "ABC",
					CardType:    project_module.APICardTypeImagesAndText.String(),
					Status:      project.Status,
				}
				endpoint := fmt.Sprintf("%s/projects/%d", tt.baseURL, project.ID)
				jsonRequestWithAuth(t, writeToken, "PATCH", endpoint, http.StatusNoContent, projOpts)
				p := unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: project.ID})

				assert.Equal(t, projOpts.Title, p.Title)
				assert.Equal(t, projOpts.Description, p.Description)
				assert.Equal(t, projOpts.CardType, p.CardType.ToAPICardType().String())
				assert.Equal(t, isClosed(projOpts.Status), p.IsClosed)
			})

			// Change status of a project (open, closed)
			t.Run("Change status of project", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				// close project
				projOpts := api.CreateOrUpdateProjectOptions{
					Title:       "Project 15",
					Description: "ABC",
					CardType:    project_module.APICardTypeImagesAndText.String(),
					Status:      project_module.APIStatusClosed.String(),
				}
				endpoint := fmt.Sprintf("%s/projects/%d", tt.baseURL, project.ID)
				jsonRequestWithAuth(t, writeToken, "PATCH", endpoint, http.StatusNoContent, projOpts)

				p := unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: project.ID})
				assert.Equal(t, isClosed(projOpts.Status), p.IsClosed)

				resp := getProject(t, readToken, tt.baseURL, project.ID, http.StatusOK)
				var projResp api.Project
				DecodeJSON(t, resp, &projResp)
				assert.EqualValues(t, project_module.APIStatusClosed, projResp.Status)

				// re-open project
				projOpts.Status = project_module.APIStatusOpen.String()
				jsonRequestWithAuth(t, writeToken, "PATCH", endpoint, http.StatusNoContent, projOpts)

				p = unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: project.ID})
				assert.Equal(t, isClosed(projOpts.Status), p.IsClosed)
			})

			// List projects
			t.Run("List projects", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				var projResp []*api.Project
				endpoint := tt.baseURL + "/projects"
				resp := requestWithAuth(t, readToken, "GET", endpoint, http.StatusOK)
				DecodeJSON(t, resp, &projResp)

				// there are already some projects in the db for user2
				if !slices.ContainsFunc(projResp, func(p *api.Project) bool {
					return p.ID == project.ID
				}) {
					t.Error("project not in project list")
				}
			})

			// Remove project
			t.Run("Remove project", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				endpoint := fmt.Sprintf("%s/projects/%v",
					tt.baseURL, project.ID)
				requestWithAuth(t, writeToken, "DELETE", endpoint, http.StatusNoContent)

				unittest.AssertNotExistsBean(t, &project_model.Project{
					ID: project.ID,
				})
			})
		})
	}
}
