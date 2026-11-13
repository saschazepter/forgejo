// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"fmt"
	"net/http"
	"testing"

	actions_model "forgejo.org/models/actions"
	auth_model "forgejo.org/models/auth"
	api "forgejo.org/modules/structs"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIOrgVariablesCreateOrganizationVariable(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	owner := forgery.CreateUser(t, nil)
	org := forgery.CreateOrganisation(t, owner, nil)
	session := loginUser(t, owner.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteOrganization)

	cases := []struct {
		Name           string
		ExpectedStatus int
	}{
		{
			Name:           "-",
			ExpectedStatus: http.StatusBadRequest,
		},
		{
			Name:           "_",
			ExpectedStatus: http.StatusNoContent,
		},
		{
			Name:           "TEST_VAR",
			ExpectedStatus: http.StatusNoContent,
		},
		{
			Name:           "test_var",
			ExpectedStatus: http.StatusConflict,
		},
		{
			Name:           "ci",
			ExpectedStatus: http.StatusBadRequest,
		},
		{
			Name:           "123var",
			ExpectedStatus: http.StatusBadRequest,
		},
		{
			Name:           "var@test",
			ExpectedStatus: http.StatusBadRequest,
		},
		{
			Name:           "forgejo_var",
			ExpectedStatus: http.StatusBadRequest,
		},
		{
			Name:           "github_var",
			ExpectedStatus: http.StatusBadRequest,
		},
		{
			Name:           "gitea_var",
			ExpectedStatus: http.StatusBadRequest,
		},
	}

	for _, c := range cases {
		requestURL := fmt.Sprintf("/api/v1/orgs/%s/actions/variables/%s", org.Name, c.Name)

		request := NewRequestWithJSON(t, "POST", requestURL, api.CreateVariableOption{
			Value: "  \tvalüé\r\n" + c.Name + "  \r\n",
		})
		request.AddTokenAuth(token)
		MakeRequest(t, request, c.ExpectedStatus)

		if c.ExpectedStatus < 300 {
			request = NewRequest(t, "GET", requestURL)
			request.AddTokenAuth(token)
			res := MakeRequest(t, request, http.StatusOK)

			variable := api.ActionVariable{}
			DecodeJSON(t, res, &variable)

			assert.Equal(t, variable.Name, c.Name)
			assert.Equal(t, variable.Data, "  \tvalüé\n"+c.Name+"  \n")
		}
	}
}

func TestAPIOrgVariablesUpdateOrganizationVariable(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	owner := forgery.CreateUser(t, nil)
	org := forgery.CreateOrganisation(t, owner, nil)
	session := loginUser(t, owner.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteOrganization)

	variableName := "test_update_var"

	url := fmt.Sprintf("/api/v1/orgs/%s/actions/variables/%s", org.Name, variableName)

	request := NewRequestWithJSON(t, "POST", url, api.CreateVariableOption{Value: "initial_val"})
	request.AddTokenAuth(token)

	MakeRequest(t, request, http.StatusNoContent)

	t.Run("Accepts only valid names", func(t *testing.T) {
		cases := []struct {
			Name           string
			UpdateName     string
			ExpectedStatus int
		}{
			{
				Name:           "not_found_var",
				ExpectedStatus: http.StatusNotFound,
			},
			{
				Name:           variableName,
				UpdateName:     "1invalid",
				ExpectedStatus: http.StatusBadRequest,
			},
			{
				Name:           variableName,
				UpdateName:     "invalid@name",
				ExpectedStatus: http.StatusBadRequest,
			},
			{
				Name:           variableName,
				UpdateName:     "ci",
				ExpectedStatus: http.StatusBadRequest,
			},
			{
				Name:           variableName,
				UpdateName:     "forgejo_foo",
				ExpectedStatus: http.StatusBadRequest,
			},
			{
				Name:           variableName,
				UpdateName:     "updated_var_name",
				ExpectedStatus: http.StatusNoContent,
			},
			{
				Name:           variableName,
				ExpectedStatus: http.StatusNotFound,
			},
			{
				Name:           "updated_var_name",
				ExpectedStatus: http.StatusNoContent,
			},
		}

		for _, c := range cases {
			url := fmt.Sprintf("/api/v1/orgs/%s/actions/variables/%s", org.Name, c.Name)
			request := NewRequestWithJSON(t, "PUT", url, api.UpdateVariableOption{Name: c.UpdateName, Value: "updated_val"})
			request.AddTokenAuth(token)
			MakeRequest(t, request, c.ExpectedStatus)
		}
	})

	t.Run("Retains special characters", func(t *testing.T) {
		variableName := "special_characters"
		url := fmt.Sprintf("/api/v1/orgs/%s/actions/variables/%s", org.Name, variableName)

		req := NewRequestWithJSON(t, "POST", url, api.CreateVariableOption{Value: "initial_value"})
		req.AddTokenAuth(token)
		MakeRequest(t, req, http.StatusNoContent)

		requestData := api.UpdateVariableOption{
			Value: "\r\n    \tüpdåtéd\r\n   \r\n",
		}
		req = NewRequestWithJSON(t, "PUT", url, requestData)
		req.AddTokenAuth(token)
		MakeRequest(t, req, http.StatusNoContent)

		req = NewRequest(t, "GET", url)
		req.AddTokenAuth(token)
		res := MakeRequest(t, req, http.StatusOK)

		variable := api.ActionVariable{}
		DecodeJSON(t, res, &variable)

		assert.Equal(t, "SPECIAL_CHARACTERS", variable.Name)
		assert.Equal(t, "\n    \tüpdåtéd\n   \n", variable.Data)
	})
}

func TestAPIOrgVariablesDeleteOrganizationVariable(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	owner := forgery.CreateUser(t, nil)
	org := forgery.CreateOrganisation(t, owner, nil)

	variable, err := actions_model.InsertVariable(t.Context(), org.ID, 0, "FORGEJO_FORBIDDEN", "illegal")
	require.NoError(t, err)
	session := loginUser(t, owner.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteOrganization)

	variableName := "test_delete_var"

	url := fmt.Sprintf("/api/v1/orgs/%s/actions/variables/%s", org.Name, variableName)

	request := NewRequestWithJSON(t, "POST", url, api.CreateVariableOption{Value: "initial_val"})
	request.AddTokenAuth(token)
	MakeRequest(t, request, http.StatusNoContent)

	request = NewRequest(t, "DELETE", url).AddTokenAuth(token)
	MakeRequest(t, request, http.StatusNoContent)

	request = NewRequest(t, "DELETE", url).AddTokenAuth(token)
	MakeRequest(t, request, http.StatusNotFound)

	// deleting of forbidden names should still be possible
	url = fmt.Sprintf("/api/v1/orgs/%s/actions/variables/%s", org.Name, variable.Name)
	request = NewRequest(t, "DELETE", url).AddTokenAuth(token)
	MakeRequest(t, request, http.StatusNoContent)

	request = NewRequest(t, "DELETE", url).AddTokenAuth(token)
	MakeRequest(t, request, http.StatusNotFound)
}

func TestAPIOrgVariablesGetSingleOrganizationVariable(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	owner := forgery.CreateUser(t, nil)
	org := forgery.CreateOrganisation(t, owner, nil)
	session := loginUser(t, owner.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteOrganization)

	name := "some_variable"
	value := "false"

	createURL := fmt.Sprintf("/api/v1/orgs/%s/actions/variables/%s", org.Name, name)

	createRequest := NewRequestWithJSON(t, "POST", createURL, api.CreateVariableOption{Value: value})
	createRequest.AddTokenAuth(token)
	MakeRequest(t, createRequest, http.StatusNoContent)

	getURL := fmt.Sprintf("/api/v1/orgs/%s/actions/variables/%s", org.Name, name)

	getRequest := NewRequest(t, "GET", getURL)
	getRequest.AddTokenAuth(token)
	getResponse := MakeRequest(t, getRequest, http.StatusOK)

	var actionVariable api.ActionVariable
	DecodeJSON(t, getResponse, &actionVariable)

	assert.NotNil(t, actionVariable)
	assert.Equal(t, org.ID, actionVariable.OwnerID)
	assert.Equal(t, int64(0), actionVariable.RepoID)
	assert.Equal(t, "SOME_VARIABLE", actionVariable.Name)
	assert.Equal(t, value, actionVariable.Data)
}

func TestAPIOrgVariablesGetAllOrganizationVariables(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	owner := forgery.CreateUser(t, nil)
	org := forgery.CreateOrganisation(t, owner, nil)
	session := loginUser(t, owner.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteOrganization)

	variables := map[string]string{"second": "Dolor sit amet", "first": "Lorem ipsum"}
	for name, value := range variables {
		createURL := fmt.Sprintf("/api/v1/orgs/%s/actions/variables/%s", org.Name, name)

		createRequest := NewRequestWithJSON(t, "POST", createURL, api.CreateVariableOption{Value: value})
		createRequest.AddTokenAuth(token)

		MakeRequest(t, createRequest, http.StatusNoContent)
	}

	getURL := fmt.Sprintf("/api/v1/orgs/%s/actions/variables", org.Name)

	getRequest := NewRequest(t, "GET", getURL)
	getRequest.AddTokenAuth(token)
	getResponse := MakeRequest(t, getRequest, http.StatusOK)

	var actionVariables []api.ActionVariable
	DecodeJSON(t, getResponse, &actionVariables)

	assert.Len(t, actionVariables, len(variables))

	assert.Equal(t, org.ID, actionVariables[0].OwnerID)
	assert.Equal(t, int64(0), actionVariables[0].RepoID)
	assert.Equal(t, "FIRST", actionVariables[0].Name)
	assert.Equal(t, "Lorem ipsum", actionVariables[0].Data)

	assert.Equal(t, org.ID, actionVariables[1].OwnerID)
	assert.Equal(t, int64(0), actionVariables[1].RepoID)
	assert.Equal(t, "SECOND", actionVariables[1].Name)
	assert.Equal(t, "Dolor sit amet", actionVariables[1].Data)
}
