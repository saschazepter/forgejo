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
	issues_model "forgejo.org/models/issues"
	"forgejo.org/models/organization"
	"forgejo.org/models/perm"
	project_model "forgejo.org/models/project"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	project_module "forgejo.org/modules/project"
	api "forgejo.org/modules/structs"
	"forgejo.org/services/convert"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runOpts struct {
	token         string
	owner         *user_model.User
	repo          *repo_model.Repository
	projectID     int64
	shouldSucceed bool
	ownerType     project_module.APIOwnerType
}

func getProjectAPIBaseString(opts *runOpts) string {
	var projectAPIBaseString string
	switch opts.ownerType {
	case project_module.APIOwnerTypeIndividual:
		projectAPIBaseString = "/api/v1/users/" + opts.owner.LowerName
	case project_module.APIOwnerTypeOrganization:
		projectAPIBaseString = "/api/v1/orgs/" + opts.owner.LowerName
	case project_module.APIOwnerTypeRepository:
		projectAPIBaseString = "/api/v1/repos/" + opts.owner.LowerName + "/" + opts.repo.LowerName
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

func deleteProject(t *testing.T, token, projectAPIBaseString string, pID int64, status int) *httptest.ResponseRecorder {
	return projectsIDEndpoint(t, token, "DELETE", projectAPIBaseString, pID, status)
}

func projectsIDEndpoint(t *testing.T, token, method, projectAPIBaseString string, pID int64, status int) *httptest.ResponseRecorder {
	projectAPIString := fmt.Sprintf("%v/projects/%v", projectAPIBaseString, pID)
	return requestWithAuth(t, token, method, projectAPIString, status)
}

// createProjectIssue creates an issue.
func createProjectIssue(t *testing.T, runOpts *runOpts, columnID int64, issueName string) api.ProjectIssue {
	// create issue
	issue := forgery.CreateIssue(t, runOpts.owner, runOpts.repo, issueName, issueName)

	// create project issue
	var projectIssue api.ProjectIssue
	endpoint := getProjectAPIBaseString(runOpts) + fmt.Sprintf("/projects/%d/columns/%d/issues", runOpts.projectID, columnID)
	if columnID == 0 {
		endpoint = getProjectAPIBaseString(runOpts) + fmt.Sprintf("/projects/%d/issues", runOpts.projectID)
	}
	resp := jsonRequestWithAuth(t, runOpts.token, "POST", endpoint,
		http.StatusCreated,
		&api.CreateProjectIssueOptions{
			IssueID: issue.ID,
		},
	)
	DecodeJSON(t, resp, &projectIssue)
	return projectIssue
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

func TestProjectAPIGetProject(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	err := unittest.PrepareTestDatabase()
	require.NoError(t, err)

	// user
	user := forgery.CreateUser(t, nil)
	userSession := loginUser(t, user.Name)
	userWriteToken := getTokenForLoggedInUser(t, userSession,
		auth_model.AccessTokenScopeWriteProject,
	)
	userReadToken := getTokenForLoggedInUser(t, userSession,
		auth_model.AccessTokenScopeReadProject,
	)
	userRepo := forgery.CreateRepository(t, user, nil)
	userRepo2 := forgery.CreateRepository(t, user, nil)
	userOrg := forgery.CreateOrganisation(t, user, nil)
	userOrgRepo := forgery.CreateRepository(t, userOrg.AsUser(), nil)

	// other user
	other := forgery.CreateUser(t, nil)
	otherSession := loginUser(t, other.Name)
	otherReadToken := getTokenForLoggedInUser(t, otherSession,
		auth_model.AccessTokenScopeWriteProject,
	)
	otherRepo := forgery.CreateRepository(t, other, nil)
	otherOrg := forgery.CreateOrganisation(t, other, nil)
	otherOrgRepo := forgery.CreateRepository(t, otherOrg.AsUser(), nil)

	// projects for user
	userProject := createProject(t, &runOpts{
		token:     userWriteToken,
		owner:     user,
		ownerType: project_module.APIOwnerTypeIndividual,
	}, "User Project")
	orgProject := createProject(t, &runOpts{
		token:     userWriteToken,
		owner:     userOrg.AsUser(),
		ownerType: project_module.APIOwnerTypeOrganization,
	}, "Org Project")
	repoProject := createProject(t, &runOpts{
		token:     userWriteToken,
		owner:     user,
		repo:      userRepo,
		ownerType: project_module.APIOwnerTypeRepository,
	}, "Repo Project")

	// test gets
	for _, tt := range []struct {
		name    string
		baseURL string
		token   string
		project api.Project
		status  int
	}{
		// user project, user
		{
			"user project, get by user",
			fmt.Sprintf("/api/v1/users/%s", user.Name),
			userReadToken,
			userProject,
			http.StatusOK,
		},
		{
			"user project, get by user, write token",
			fmt.Sprintf("/api/v1/users/%s", user.Name),
			userWriteToken,
			userProject,
			http.StatusOK,
		},
		{
			"user project, get by user, via org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", userOrg.Name),
			userReadToken,
			userProject,
			http.StatusNotFound,
		},
		{
			"user project, get by user, via repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
			userReadToken,
			userProject,
			http.StatusNotFound,
		},

		// user project, other
		{
			"user project, get by other",
			fmt.Sprintf("/api/v1/users/%s", user.Name),
			otherReadToken,
			userProject,
			http.StatusNotFound,
		},
		{
			"user project, get by other, via other's user endpoint",
			fmt.Sprintf("/api/v1/users/%s", other.Name),
			otherReadToken,
			userProject,
			http.StatusNotFound,
		},
		{
			"user project, get by other, via other's org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", otherOrg.Name),
			otherReadToken,
			userProject,
			http.StatusNotFound,
		},
		{
			"user project, get by other, via other's org repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", otherOrg.Name, otherOrgRepo.Name),
			otherReadToken,
			userProject,
			http.StatusNotFound,
		},

		// org project, user
		{
			"org project, get by user",
			fmt.Sprintf("/api/v1/orgs/%s", userOrg.Name),
			userReadToken,
			orgProject,
			http.StatusOK,
		},
		{
			"org project, get by user, via user endpoint",
			fmt.Sprintf("/api/v1/users/%s/%s", user.Name, userRepo.Name),
			userReadToken,
			orgProject,
			http.StatusNotFound,
		},
		{
			"org project, get by user, via repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
			userReadToken,
			orgProject,
			http.StatusNotFound,
		},

		// org project, other
		{
			"org project, get by other",
			fmt.Sprintf("/api/v1/orgs/%s", userOrg.Name),
			otherReadToken,
			orgProject,
			http.StatusOK,
		},
		{
			"org project, get by other, via other's user endpoint",
			fmt.Sprintf("/api/v1/users/%s", other.Name),
			otherReadToken,
			orgProject,
			http.StatusNotFound,
		},
		{
			"org project, get by other, via other's org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", otherOrg.Name),
			otherReadToken,
			orgProject,
			http.StatusNotFound,
		},
		{
			"org project, get by other, via other's org repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", otherOrg.Name, otherOrgRepo.Name),
			otherReadToken,
			orgProject,
			http.StatusNotFound,
		},

		// repo project, user
		{
			"repo project, get by user",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
			userReadToken,
			repoProject,
			http.StatusOK,
		},
		{
			"repo project, get by user, via wrong repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo2.Name),
			userReadToken,
			repoProject,
			http.StatusNotFound,
		},
		{
			"repo project, get by user, via user endpoint",
			fmt.Sprintf("/api/v1/users/%s", user.Name),
			userReadToken,
			repoProject,
			http.StatusNotFound,
		},
		{
			"repo project, get by user, via org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", userOrg.Name),
			userReadToken,
			repoProject,
			http.StatusNotFound,
		},
		{
			"repo project, get by user, via org repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", userOrg.Name, userOrgRepo.Name),
			userReadToken,
			repoProject,
			http.StatusNotFound,
		},

		// repo project, other
		{
			"repo project, get by other",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
			otherReadToken,
			repoProject,
			http.StatusOK,
		},
		{
			"repo project, get by other, via other's repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", other.Name, otherRepo.Name),
			otherReadToken,
			repoProject,
			http.StatusNotFound,
		},
		{
			"repo project, get by other, via other's user endpoint",
			fmt.Sprintf("/api/v1/users/%s", other.Name),
			otherReadToken,
			repoProject,
			http.StatusNotFound,
		},
		{
			"repo project, get by other, via other's org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", otherOrg.Name),
			otherReadToken,
			repoProject,
			http.StatusNotFound,
		},
		{
			"repo project, get by other, via other's org repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", otherOrg.Name, otherOrgRepo.Name),
			otherReadToken,
			repoProject,
			http.StatusNotFound,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// get project
			endpoint := fmt.Sprintf("%s/projects/%d",
				tt.baseURL, tt.project.ID)
			resp := requestWithAuth(t, tt.token, "GET", endpoint, tt.status)
			if tt.status == http.StatusOK {
				var proj api.Project
				DecodeJSON(t, resp, &proj)
				assert.Equal(t, tt.project, proj)
			}
		})
	}
}

func TestProjectAPIDeleteProject(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	err := unittest.PrepareTestDatabase()
	require.NoError(t, err)

	// user
	user := forgery.CreateUser(t, nil)
	userSession := loginUser(t, user.Name)
	userWriteToken := getTokenForLoggedInUser(t, userSession,
		auth_model.AccessTokenScopeWriteProject,
	)
	userReadToken := getTokenForLoggedInUser(t, userSession,
		auth_model.AccessTokenScopeReadProject,
	)
	userRepo := forgery.CreateRepository(t, user, nil)
	userRepo2 := forgery.CreateRepository(t, user, nil)
	userOrg := forgery.CreateOrganisation(t, user, nil)
	userOrgRepo := forgery.CreateRepository(t, userOrg.AsUser(), nil)

	// other user
	other := forgery.CreateUser(t, nil)
	otherSession := loginUser(t, other.Name)
	otherWriteToken := getTokenForLoggedInUser(t, otherSession,
		auth_model.AccessTokenScopeWriteProject,
	)
	otherRepo := forgery.CreateRepository(t, other, nil)
	otherOrg := forgery.CreateOrganisation(t, other, nil)
	otherOrgRepo := forgery.CreateRepository(t, otherOrg.AsUser(), nil)

	// projects for user
	userProject := createProject(t, &runOpts{
		token:     userWriteToken,
		owner:     user,
		ownerType: project_module.APIOwnerTypeIndividual,
	}, "User Project")
	orgProject := createProject(t, &runOpts{
		token:     userWriteToken,
		owner:     userOrg.AsUser(),
		ownerType: project_module.APIOwnerTypeOrganization,
	}, "User Project")
	repoProject := createProject(t, &runOpts{
		token:     userWriteToken,
		owner:     user,
		repo:      userRepo,
		ownerType: project_module.APIOwnerTypeRepository,
	}, "Repo Project")

	// test failing deletes
	for _, tt := range []struct {
		name    string
		baseURL string
		token   string
		project api.Project
		status  int
	}{
		// user project, right user
		{
			"user project, delete by user, read token",
			fmt.Sprintf("/api/v1/users/%s", user.Name),
			userReadToken,
			userProject,
			http.StatusForbidden,
		},
		{
			"user project, delete by user, via org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", userOrg.Name),
			userWriteToken,
			userProject,
			http.StatusNotFound,
		},
		{
			"user project, delete by user, via repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
			userWriteToken,
			userProject,
			http.StatusNotFound,
		},

		// user project, wrong user
		{
			"user project, delete by other",
			fmt.Sprintf("/api/v1/users/%s", user.Name),
			otherWriteToken,
			userProject,
			http.StatusForbidden,
		},
		{
			"user project, delete by other, via other's user endpoint",
			fmt.Sprintf("/api/v1/users/%s", other.Name),
			otherWriteToken,
			userProject,
			http.StatusNotFound,
		},
		{
			"user project, delete by other, via other's org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", otherOrg.Name),
			otherWriteToken,
			userProject,
			http.StatusNotFound,
		},
		{
			"user project, delete by other, via other's org repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", otherOrg.Name, otherOrgRepo.Name),
			otherWriteToken,
			userProject,
			http.StatusNotFound,
		},

		// org project, right user
		{
			"org project, delete by user, read token",
			fmt.Sprintf("/api/v1/orgs/%s", userOrg.Name),
			userReadToken,
			orgProject,
			http.StatusForbidden,
		},
		{
			"org project, delete by user, via user endpoint",
			fmt.Sprintf("/api/v1/users/%s/%s", user.Name, userRepo.Name),
			userWriteToken,
			orgProject,
			http.StatusNotFound,
		},
		{
			"org project, delete by user, via repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
			userWriteToken,
			orgProject,
			http.StatusNotFound,
		},

		// org project, wrong user
		{
			"org project, delete by other",
			fmt.Sprintf("/api/v1/orgs/%s", userOrg.Name),
			otherWriteToken,
			orgProject,
			http.StatusForbidden,
		},
		{
			"org project, delete by other, via other's user endpoint",
			fmt.Sprintf("/api/v1/users/%s", other.Name),
			otherWriteToken,
			orgProject,
			http.StatusNotFound,
		},
		{
			"org project, delete by other, via other's org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", otherOrg.Name),
			otherWriteToken,
			orgProject,
			http.StatusNotFound,
		},
		{
			"org project, delete by other, via other's org repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", otherOrg.Name, otherOrgRepo.Name),
			otherWriteToken,
			orgProject,
			http.StatusNotFound,
		},

		// repo project, right user
		{
			"repo project, delete by user, read token",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
			userReadToken,
			repoProject,
			http.StatusForbidden,
		},
		{
			"repo project, delete by user, via wrong repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo2.Name),
			userWriteToken,
			repoProject,
			http.StatusNotFound,
		},
		{
			"repo project, delete by user, via user endpoint",
			fmt.Sprintf("/api/v1/users/%s", user.Name),
			userWriteToken,
			repoProject,
			http.StatusNotFound,
		},
		{
			"repo project, delete by user, via org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", userOrg.Name),
			userWriteToken,
			repoProject,
			http.StatusNotFound,
		},
		{
			"repo project, delete by user, via org repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", userOrg.Name, userOrgRepo.Name),
			userWriteToken,
			repoProject,
			http.StatusNotFound,
		},

		// repo project, wrong user
		{
			"repo project, delete by other",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
			otherWriteToken,
			repoProject,
			http.StatusForbidden,
		},
		{
			"repo project, delete by other, via other's repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", other.Name, otherRepo.Name),
			otherWriteToken,
			repoProject,
			http.StatusNotFound,
		},
		{
			"repo project, delete by other, via other's user endpoint",
			fmt.Sprintf("/api/v1/users/%s", other.Name),
			otherWriteToken,
			repoProject,
			http.StatusNotFound,
		},
		{
			"repo project, delete by other, via other's org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", otherOrg.Name),
			otherWriteToken,
			repoProject,
			http.StatusNotFound,
		},
		{
			"repo project, delete by other, via other's org repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", otherOrg.Name, otherOrgRepo.Name),
			otherWriteToken,
			repoProject,
			http.StatusNotFound,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// delete project
			endpoint := fmt.Sprintf("%s/projects/%d", tt.baseURL, tt.project.ID)
			requestWithAuth(t, tt.token, "DELETE", endpoint, tt.status)
			unittest.AssertExistsIf(t, true, &project_model.Project{
				ID: tt.project.ID,
			})
		})
	}

	// test successful deletes
	for _, tt := range []struct {
		name    string
		baseURL string
		token   string
		project api.Project
	}{
		{
			"user project",
			fmt.Sprintf("/api/v1/users/%s", user.Name),
			userWriteToken,
			userProject,
		},
		{
			"org project",
			fmt.Sprintf("/api/v1/orgs/%s", userOrg.Name),
			userWriteToken,
			orgProject,
		},
		{
			"repo project",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
			userWriteToken,
			repoProject,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// delete project
			endpoint := fmt.Sprintf("%s/projects/%d", tt.baseURL, tt.project.ID)
			requestWithAuth(t, tt.token, "DELETE", endpoint, http.StatusNoContent)
			unittest.AssertExistsIf(t, false, &project_model.Project{
				ID: tt.project.ID,
			})
		})
	}
}

func TestProjectAPIListProjectIssues(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	err := unittest.PrepareTestDatabase()
	require.NoError(t, err)

	// user
	user := forgery.CreateUser(t, nil)
	userSession := loginUser(t, user.Name)
	userWriteToken := getTokenForLoggedInUser(t, userSession,
		auth_model.AccessTokenScopeWriteProject,
	)
	userReadToken := getTokenForLoggedInUser(t, userSession,
		auth_model.AccessTokenScopeReadProject,
	)
	userRepo := forgery.CreateRepository(t, user, nil)
	userRepo2 := forgery.CreateRepository(t, user, nil)
	userOrg := forgery.CreateOrganisation(t, user, nil)
	userOrgRepo := forgery.CreateRepository(t, userOrg.AsUser(), nil)

	// other user
	other := forgery.CreateUser(t, nil)
	otherSession := loginUser(t, other.Name)
	otherReadToken := getTokenForLoggedInUser(t, otherSession,
		auth_model.AccessTokenScopeReadProject,
	)
	otherRepo := forgery.CreateRepository(t, other, nil)
	otherOrg := forgery.CreateOrganisation(t, other, nil)
	otherOrgRepo := forgery.CreateRepository(t, otherOrg.AsUser(), nil)

	// projects for user
	userProject := createProject(t, &runOpts{
		token:     userWriteToken,
		owner:     user,
		ownerType: project_module.APIOwnerTypeIndividual,
	}, "User Project")
	orgProject := createProject(t, &runOpts{
		token:     userWriteToken,
		owner:     userOrg.AsUser(),
		ownerType: project_module.APIOwnerTypeOrganization,
	}, "User Project")
	repoProject := createProject(t, &runOpts{
		token:     userWriteToken,
		owner:     user,
		repo:      userRepo,
		ownerType: project_module.APIOwnerTypeRepository,
	}, "Repo Project")

	// test lists
	for _, tt := range []struct {
		name    string
		baseURL string
		token   string
		project api.Project
		status  int
	}{
		// user project, user
		{
			"user project, list by user, read token",
			fmt.Sprintf("/api/v1/users/%s", user.Name),
			userReadToken,
			userProject,
			http.StatusOK,
		},
		{
			"user project, list by user, write token",
			fmt.Sprintf("/api/v1/users/%s", user.Name),
			userWriteToken,
			userProject,
			http.StatusOK,
		},
		{
			"user project, list by user, via org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", userOrg.Name),
			userReadToken,
			userProject,
			http.StatusNotFound,
		},
		{
			"user project, list by user, via repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
			userReadToken,
			userProject,
			http.StatusNotFound,
		},

		// user project, other
		{
			"user project, list by other",
			fmt.Sprintf("/api/v1/users/%s", user.Name),
			otherReadToken,
			userProject,
			http.StatusNotFound,
		},
		{
			"user project, list by other, via other's user endpoint",
			fmt.Sprintf("/api/v1/users/%s", other.Name),
			otherReadToken,
			userProject,
			http.StatusNotFound,
		},
		{
			"user project, list by other, via other's org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", otherOrg.Name),
			otherReadToken,
			userProject,
			http.StatusNotFound,
		},
		{
			"user project, list by other, via other's org repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", otherOrg.Name, otherOrgRepo.Name),
			otherReadToken,
			userProject,
			http.StatusNotFound,
		},

		// org project, user
		{
			"org project, list by user, read token",
			fmt.Sprintf("/api/v1/orgs/%s", userOrg.Name),
			userReadToken,
			orgProject,
			http.StatusOK,
		},
		{
			"org project, list by user, write token",
			fmt.Sprintf("/api/v1/orgs/%s", userOrg.Name),
			userWriteToken,
			orgProject,
			http.StatusOK,
		},
		{
			"org project, list by user, via user endpoint",
			fmt.Sprintf("/api/v1/users/%s/%s", user.Name, userRepo.Name),
			userReadToken,
			orgProject,
			http.StatusNotFound,
		},
		{
			"org project, list by user, via repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
			userReadToken,
			orgProject,
			http.StatusNotFound,
		},

		// org project, other
		{
			"org project, list by other",
			fmt.Sprintf("/api/v1/orgs/%s", userOrg.Name),
			otherReadToken,
			orgProject,
			http.StatusOK,
		},
		{
			"org project, list by other, via other's user endpoint",
			fmt.Sprintf("/api/v1/users/%s", other.Name),
			otherReadToken,
			orgProject,
			http.StatusNotFound,
		},
		{
			"org project, list by other, via other's org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", otherOrg.Name),
			otherReadToken,
			orgProject,
			http.StatusNotFound,
		},
		{
			"org project, list by other, via other's org repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", otherOrg.Name, otherOrgRepo.Name),
			otherReadToken,
			orgProject,
			http.StatusNotFound,
		},

		// repo project, user
		{
			"repo project, list by user, read token",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
			userReadToken,
			repoProject,
			http.StatusOK,
		},
		{
			"repo project, list by user, write token",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
			userWriteToken,
			repoProject,
			http.StatusOK,
		},
		{
			"repo project, list by user, via wrong repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo2.Name),
			userReadToken,
			repoProject,
			http.StatusNotFound,
		},
		{
			"repo project, list by user, via user endpoint",
			fmt.Sprintf("/api/v1/users/%s", user.Name),
			userReadToken,
			repoProject,
			http.StatusNotFound,
		},
		{
			"repo project, list by user, via org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", userOrg.Name),
			userReadToken,
			repoProject,
			http.StatusNotFound,
		},
		{
			"repo project, list by user, via org repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", userOrg.Name, userOrgRepo.Name),
			userReadToken,
			repoProject,
			http.StatusNotFound,
		},

		// repo project, other
		{
			"repo project, list by other",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
			otherReadToken,
			repoProject,
			http.StatusOK,
		},
		{
			"repo project, list by other, via other's repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", other.Name, otherRepo.Name),
			otherReadToken,
			repoProject,
			http.StatusNotFound,
		},
		{
			"repo project, list by other, via other's user endpoint",
			fmt.Sprintf("/api/v1/users/%s", other.Name),
			otherReadToken,
			repoProject,
			http.StatusNotFound,
		},
		{
			"repo project, list by other, via other's org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", otherOrg.Name),
			otherReadToken,
			repoProject,
			http.StatusNotFound,
		},
		{
			"repo project, list by other, via other's org repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", otherOrg.Name, otherOrgRepo.Name),
			otherReadToken,
			repoProject,
			http.StatusNotFound,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// list project issues
			endpoint := fmt.Sprintf("%s/projects/%d/issues", tt.baseURL, tt.project.ID)
			requestWithAuth(t, tt.token, "GET", endpoint, tt.status)
		})
	}
}

func TestProjectAPICreateProjectIssue(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	err := unittest.PrepareTestDatabase()
	require.NoError(t, err)

	// user
	user := forgery.CreateUser(t, nil)
	userSession := loginUser(t, user.Name)
	userWriteToken := getTokenForLoggedInUser(t, userSession,
		auth_model.AccessTokenScopeWriteProject,
	)
	userReadToken := getTokenForLoggedInUser(t, userSession,
		auth_model.AccessTokenScopeReadProject,
	)
	userRepo := forgery.CreateRepository(t, user, nil)
	userRepo2 := forgery.CreateRepository(t, user, nil)
	userOrg := forgery.CreateOrganisation(t, user, nil)
	userOrgRepo := forgery.CreateRepository(t, userOrg.AsUser(), nil)

	// other user
	other := forgery.CreateUser(t, nil)
	otherSession := loginUser(t, other.Name)
	otherWriteToken := getTokenForLoggedInUser(t, otherSession,
		auth_model.AccessTokenScopeWriteProject,
	)
	otherRepo := forgery.CreateRepository(t, other, nil)
	otherOrg := forgery.CreateOrganisation(t, other, nil)
	otherOrgRepo := forgery.CreateRepository(t, otherOrg.AsUser(), nil)

	// projects for user
	userProject := createProject(t, &runOpts{
		token:     userWriteToken,
		owner:     user,
		ownerType: project_module.APIOwnerTypeIndividual,
	}, "User Project")
	orgProject := createProject(t, &runOpts{
		token:     userWriteToken,
		owner:     userOrg.AsUser(),
		ownerType: project_module.APIOwnerTypeOrganization,
	}, "User Project")
	repoProject := createProject(t, &runOpts{
		token:     userWriteToken,
		owner:     user,
		repo:      userRepo,
		ownerType: project_module.APIOwnerTypeRepository,
	}, "Repo Project")

	// issues for user
	repoIssue := forgery.CreateIssue(t, user, userRepo, "repo issue", "repo issue")
	orgRepoIssue := forgery.CreateIssue(t, userOrg.AsUser(), userOrgRepo, "org repo issue", "org repo issue")

	// test creates
	for _, tt := range []struct {
		name    string
		baseURL string
		token   string
		project api.Project
		issue   *issues_model.Issue
		status  int
	}{
		// user project, user
		{
			"user project, create by user, read token",
			fmt.Sprintf("/api/v1/users/%s", user.Name),
			userReadToken,
			userProject,
			repoIssue,
			http.StatusForbidden,
		},
		{
			"user project, create by user, via org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", userOrg.Name),
			userWriteToken,
			userProject,
			repoIssue,
			http.StatusNotFound,
		},
		{
			"user project, create by user, via repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
			userWriteToken,
			userProject,
			repoIssue,
			http.StatusNotFound,
		},

		// user project, other
		{
			"user project, create by other",
			fmt.Sprintf("/api/v1/users/%s", user.Name),
			otherWriteToken,
			userProject,
			repoIssue,
			http.StatusForbidden,
		},
		{
			"user project, create by other, via other's user endpoint",
			fmt.Sprintf("/api/v1/users/%s", other.Name),
			otherWriteToken,
			userProject,
			repoIssue,
			http.StatusNotFound,
		},
		{
			"user project, create by other, via other's org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", otherOrg.Name),
			otherWriteToken,
			userProject,
			repoIssue,
			http.StatusNotFound,
		},
		{
			"user project, create by other, via other's org repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", otherOrg.Name, otherOrgRepo.Name),
			otherWriteToken,
			userProject,
			repoIssue,
			http.StatusNotFound,
		},

		// org project, user
		{
			"org project, create by user, read token",
			fmt.Sprintf("/api/v1/orgs/%s", userOrg.Name),
			userReadToken,
			orgProject,
			orgRepoIssue,
			http.StatusForbidden,
		},
		{
			"org project, creste by user, via user endpoint",
			fmt.Sprintf("/api/v1/users/%s/%s", user.Name, userRepo.Name),
			userWriteToken,
			orgProject,
			orgRepoIssue,
			http.StatusNotFound,
		},
		{
			"org project, create by user, via repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
			userWriteToken,
			orgProject,
			orgRepoIssue,
			http.StatusNotFound,
		},

		// org project, other
		{
			"org project, create by other",
			fmt.Sprintf("/api/v1/orgs/%s", userOrg.Name),
			otherWriteToken,
			orgProject,
			orgRepoIssue,
			http.StatusForbidden,
		},
		{
			"org project, create by other, via other's user endpoint",
			fmt.Sprintf("/api/v1/users/%s", other.Name),
			otherWriteToken,
			orgProject,
			orgRepoIssue,
			http.StatusNotFound,
		},
		{
			"org project, create by other, via other's org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", otherOrg.Name),
			otherWriteToken,
			orgProject,
			orgRepoIssue,
			http.StatusNotFound,
		},
		{
			"org project, create by other, via other's org repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", otherOrg.Name, otherOrgRepo.Name),
			otherWriteToken,
			orgProject,
			orgRepoIssue,
			http.StatusNotFound,
		},

		// repo project, user
		{
			"repo project, create by user, read token",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
			userReadToken,
			repoProject,
			repoIssue,
			http.StatusForbidden,
		},
		{
			"repo project, create by user, via wrong repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo2.Name),
			userWriteToken,
			repoProject,
			repoIssue,
			http.StatusNotFound,
		},
		{
			"repo project, create by user, via user endpoint",
			fmt.Sprintf("/api/v1/users/%s", user.Name),
			userWriteToken,
			repoProject,
			repoIssue,
			http.StatusNotFound,
		},
		{
			"repo project, create by user, via org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", userOrg.Name),
			userWriteToken,
			repoProject,
			repoIssue,
			http.StatusNotFound,
		},
		{
			"repo project, create by user, via org repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", userOrg.Name, userOrgRepo.Name),
			userWriteToken,
			repoProject,
			repoIssue,
			http.StatusNotFound,
		},

		// repo project, other
		{
			"repo project, create by other",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
			otherWriteToken,
			repoProject,
			repoIssue,
			http.StatusForbidden,
		},
		{
			"repo project, create by other, via other's repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", other.Name, otherRepo.Name),
			otherWriteToken,
			repoProject,
			repoIssue,
			http.StatusNotFound,
		},
		{
			"repo project, create by other, via other's user endpoint",
			fmt.Sprintf("/api/v1/users/%s", other.Name),
			otherWriteToken,
			repoProject,
			repoIssue,
			http.StatusNotFound,
		},
		{
			"repo project, create by other, via other's org endpoint",
			fmt.Sprintf("/api/v1/orgs/%s", otherOrg.Name),
			otherWriteToken,
			repoProject,
			repoIssue,
			http.StatusNotFound,
		},
		{
			"repo project, create by other, via other's org repo endpoint",
			fmt.Sprintf("/api/v1/repos/%s/%s", otherOrg.Name, otherOrgRepo.Name),
			otherWriteToken,
			repoProject,
			repoIssue,
			http.StatusNotFound,
		},

		// successful creates
		{
			"user project, create by user, write token",
			fmt.Sprintf("/api/v1/users/%s", user.Name),
			userWriteToken,
			userProject,
			repoIssue,
			http.StatusCreated,
		},
		{
			"org project, create by user, write token",
			fmt.Sprintf("/api/v1/orgs/%s", userOrg.Name),
			userWriteToken,
			orgProject,
			orgRepoIssue,
			http.StatusCreated,
		},
		{
			"repo project, create by user, write token",
			fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
			userWriteToken,
			repoProject,
			repoIssue,
			http.StatusCreated,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// create project issue
			endpoint := fmt.Sprintf("%s/projects/%d/issues", tt.baseURL, tt.project.ID)
			jsonRequestWithAuth(t, tt.token, "POST", endpoint, tt.status,
				&api.CreateProjectIssueOptions{
					IssueID: tt.issue.ID,
				},
			)
		})
	}
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
	org := forgery.CreateOrganisation(t, user, nil)
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
		owner     *user_model.User
		ownerType project_module.APIOwnerType
		repo      *repo_model.Repository
		baseURL   string
	}{
		{
			name:      "User",
			owner:     user,
			ownerType: project_module.APIOwnerTypeIndividual,
			repo:      userRepo,
			baseURL:   fmt.Sprintf("/api/v1/users/%s", user.Name),
		},
		{
			name:      "Org",
			owner:     org.AsUser(),
			ownerType: project_module.APIOwnerTypeOrganization,
			repo:      orgRepo,
			baseURL:   fmt.Sprintf("/api/v1/orgs/%s", org.Name),
		},
		{
			name:      "User Repo",
			owner:     user,
			ownerType: project_module.APIOwnerTypeRepository,
			repo:      userRepo,
			baseURL:   fmt.Sprintf("/api/v1/repos/%s/%s", user.Name, userRepo.Name),
		},
		{
			name:      "Org Repo",
			owner:     org.AsUser(),
			ownerType: project_module.APIOwnerTypeRepository,
			repo:      orgRepo,
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
				assert.Equal(t, tt.repo.Name, project.RepoName)
			} else {
				assert.Equal(t, tt.owner.Name, project.OwnerName)
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

			// Add issue to a project, to the default column
			projectIssue1 := createProjectIssue(t,
				&runOpts{
					token:     writeToken,
					owner:     tt.owner,
					repo:      tt.repo,
					projectID: project.ID,
					ownerType: tt.ownerType,
				}, 0, "TestIssue")

			assert.NotZero(t, projectIssue1.ID)
			assert.Equal(t, project.ID, projectIssue1.ProjectID)
			assert.NotNil(t, projectIssue1.Sorting) // Sorting is zero by default, but "NOT NULL" according to DB model

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

			// List issues in a project
			t.Run("List issues in a a project", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				var issueResp []*api.ProjectIssue
				endpoint := fmt.Sprintf("%s/projects/%d/issues",
					tt.baseURL, project.ID)
				resp := requestWithAuth(t, readToken, "GET", endpoint, http.StatusOK)
				DecodeJSON(t, resp, &issueResp)

				if !slices.ContainsFunc(issueResp, func(p *api.ProjectIssue) bool {
					return p.ID == projectIssue1.ID
				}) {
					t.Error("project issue not in project issue list")
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

func runProjectWriteActions(t *testing.T, runOpts *runOpts, projectOpts *api.CreateOrUpdateProjectOptions) {
	projectAPIBaseString := getProjectAPIBaseString(runOpts)
	endpoint := projectAPIBaseString + "/projects"
	resp := jsonRequestWithAuth(t, runOpts.token, "POST", endpoint, NoExpectedStatus, projectOpts)
	var proj *api.Project
	if runOpts.shouldSucceed {
		require.Equal(t, http.StatusCreated, resp.Code)
		DecodeJSON(t, resp, &proj)
		assert.Equal(t, projectOpts.Title, proj.Title)
		deleteProject(t, runOpts.token, projectAPIBaseString, proj.ID, http.StatusNoContent)
	} else {
		assert.NotEqual(t, http.StatusCreated, resp.Code)
	}
}

func runProjectReadActions(t *testing.T, opts *runOpts) {
	projectAPIBaseString := getProjectAPIBaseString(opts)
	resp := getProject(t, opts.token, projectAPIBaseString, opts.projectID, NoExpectedStatus)
	if opts.shouldSucceed {
		var proj *api.Project
		assert.Equal(t, http.StatusOK, resp.Code)
		DecodeJSON(t, resp, &proj)
		assert.Equal(t, opts.projectID, proj.ID)
	} else {
		assert.NotEqual(t, http.StatusOK, resp.Code)
	}
}

func TestProjectAPIPermissionHandling(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	err := unittest.PrepareTestDatabase()
	require.NoError(t, err)

	// users and tokens
	user1 := forgery.CreateUser(t, nil)
	user2 := forgery.CreateUser(t, nil)

	user2Session := loginUser(t, user2.Name)
	user2WriteToken := getTokenForLoggedInUser(t,
		user2Session,
		auth_model.AccessTokenScopeWriteProject,
		auth_model.AccessTokenScopeWriteOrganization,
		auth_model.AccessTokenScopeWriteRepository,
		auth_model.AccessTokenScopeWriteUser,
		auth_model.AccessTokenScopeWriteIssue,
	)
	user2ReadToken := getTokenForLoggedInUser(t,
		user2Session,
		auth_model.AccessTokenScopeReadProject,
		auth_model.AccessTokenScopeReadOrganization,
		auth_model.AccessTokenScopeReadRepository,
		auth_model.AccessTokenScopeReadUser,
		auth_model.AccessTokenScopeReadIssue,
	)

	projectOpts := &api.CreateOrUpdateProjectOptions{
		Title:        "Test Project",
		Description:  "Test Project",
		TemplateType: "none",
		CardType:     "text_only",
		Status:       "open",
	}

	t.Run("Private Org where User2 is owner", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		privUser2Org := forgery.CreateOrganisation(t, user2,
			&forgery.CreateOrganisationOptions{
				Visibility: api.VisibleTypePrivate,
			})

		// Run actions
		runOpts := &runOpts{
			token:         user2WriteToken,
			owner:         privUser2Org.AsUser(),
			shouldSucceed: true,
			ownerType:     project_module.APIOwnerTypeOrganization,
		}
		runProjectWriteActions(t, runOpts, projectOpts)
	})

	t.Run("Repo where User2 is owner", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		user2Repo := forgery.CreateRepository(t, user2, nil)

		// Run actions
		runOpts := &runOpts{
			token:         user2WriteToken,
			owner:         user2,
			repo:          user2Repo,
			shouldSucceed: true,
			ownerType:     project_module.APIOwnerTypeRepository,
		}
		runProjectWriteActions(t, runOpts, projectOpts)
	})

	t.Run("Project where User2 is owner", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		// Run actions
		runOpts := &runOpts{
			token:         user2WriteToken,
			owner:         user2,
			shouldSucceed: true,
			ownerType:     project_module.APIOwnerTypeIndividual,
		}
		runProjectWriteActions(t, runOpts, projectOpts)
	})

	t.Run("Private Org where User2 team member with write access", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		// private user1 org with user2 write access
		privUser1Org := forgery.CreateOrganisation(t, user1,
			&forgery.CreateOrganisationOptions{
				Visibility: api.VisibleTypePrivate,
			})

		privOrgTeamOpts := &forgery.CreateTeamOptions{
			Name: "CanWriteProjects",
			Mode: perm.AccessModeWrite,
			Units: []*organization.TeamUnit{
				{
					OrgID:      privUser1Org.ID,
					Type:       unit.TypeProjects,
					AccessMode: perm.AccessModeWrite,
				},
			},
			Members: []*user_model.User{user2},
		}
		forgery.CreateTeam(t, privUser1Org, privOrgTeamOpts)

		// Run actions
		runOpts := &runOpts{
			token:         user2WriteToken,
			owner:         privUser1Org.AsUser(),
			shouldSucceed: true,
			ownerType:     project_module.APIOwnerTypeOrganization,
		}
		runProjectWriteActions(t, runOpts, projectOpts)
	})

	t.Run("Repo where User2 is collaborator with read/write access", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		user1Repo := forgery.CreateRepository(t, user1, &forgery.CreateRepositoryOptions{
			Collaborators: map[*user_model.User]perm.AccessMode{
				user2: perm.AccessModeWrite,
			},
		})

		// Run actions
		runOpts := &runOpts{
			token:         user2WriteToken,
			owner:         user1,
			repo:          user1Repo,
			shouldSucceed: true,
			ownerType:     project_module.APIOwnerTypeRepository,
		}
		runProjectWriteActions(t, runOpts, projectOpts)
	})

	t.Run("Repo where User2 is not owner", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		user1Repo := forgery.CreateRepository(t, user1, nil)
		runOpts := &runOpts{
			token:         user2WriteToken,
			owner:         user1,
			repo:          user1Repo,
			shouldSucceed: false,
			ownerType:     project_module.APIOwnerTypeRepository,
		}
		runProjectWriteActions(t, runOpts, projectOpts)
	})

	t.Run("Repo where User2 is collaborator with read access", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		user1Repo := forgery.CreateRepository(t, user1, &forgery.CreateRepositoryOptions{
			Collaborators: map[*user_model.User]perm.AccessMode{
				user2: perm.AccessModeRead,
			},
		})

		// Run actions
		runOpts := &runOpts{
			token:         user2WriteToken,
			owner:         user1,
			repo:          user1Repo,
			shouldSucceed: false,
			ownerType:     project_module.APIOwnerTypeRepository,
		}
		runProjectWriteActions(t, runOpts, projectOpts)

		user1Project := forgery.CreateProject(t, user1Repo, nil)
		runOpts.projectID = user1Project.ID
		runOpts.token = user2ReadToken
		runOpts.shouldSucceed = true
		runProjectReadActions(t, runOpts)
	})

	t.Run("Private Org where User2 is not member", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		privUser1Org := forgery.CreateOrganisation(t, user1,
			&forgery.CreateOrganisationOptions{
				Visibility: api.VisibleTypePrivate,
			})
		runOpts := &runOpts{
			token:         user2WriteToken,
			owner:         privUser1Org.AsUser(),
			shouldSucceed: false,
			ownerType:     project_module.APIOwnerTypeOrganization,
		}
		runProjectWriteActions(t, runOpts, projectOpts)
		user1Project := forgery.CreateProject(t, privUser1Org, nil)
		runOpts.token = user2ReadToken
		runOpts.projectID = user1Project.ID
		runProjectReadActions(t, runOpts)
	})

	t.Run("Project where User2 is not owner", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		privUser1Org := forgery.CreateOrganisation(t, user1,
			&forgery.CreateOrganisationOptions{
				Visibility: api.VisibleTypePrivate,
			})
		delProj := forgery.CreateProject(t, privUser1Org, nil)
		runOpts := &runOpts{
			token:         user2ReadToken,
			owner:         privUser1Org.AsUser(),
			shouldSucceed: false,
			ownerType:     project_module.APIOwnerTypeOrganization,
		}
		baseString := getProjectAPIBaseString(runOpts)
		deleteProject(t, user2WriteToken, baseString, delProj.ID, http.StatusForbidden)
	})
}
