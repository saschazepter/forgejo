// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"forgejo.org/routers/api/v1/projects"
	"forgejo.org/services/context"
)

// CreateProject creates a project for the user/org/repo
func (Project) CreateProject(ctx *context.APIContext) {
	// swagger:operation POST /repos/{username}/{reponame}/projects repository repoCreateProject
	// ---
	// summary: Create a project for a user
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: username
	//   in: path
	//   description: name of the user
	//   type: string
	//   required: true
	// - name: reponame
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	// 		 "$ref": "#/definitions/CreateOrUpdateProjectOptions"
	// responses:
	//   "201":
	//     "$ref": "#/responses/Project"
	//   "422":
	//     "$ref": "#/responses/validationError"

	projects.CreateProject(ctx)
}

// ListProjects gets a list of projects according to query parameters
func (Project) ListProjects(ctx *context.APIContext) {
	// swagger:operation GET /repos/{username}/{reponame}/projects repository repoListProjects
	// ---
	// summary: Get a list of projects
	// produces:
	// - application/json
	// parameters:
	// - name: username
	//   in: path
	//   description: name of the user
	//   type: string
	//   required: true
	// - name: reponame
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/ProjectList"
	//   "404":
	//     "$ref": "#/responses/notFound"

	projects.ListProjects(ctx)
}

// GetProject gets a project by id
func (Project) GetProject(ctx *context.APIContext) {
	// swagger:operation GET /repos/{username}/{reponame}/projects/{project_id} repository repoGetProject
	// ---
	// summary: Get a project
	// produces:
	// - application/json
	// parameters:
	// - name: username
	//   in: path
	//   description: name of the user
	//   type: string
	//   required: true
	// - name: reponame
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: project_id
	//   in: path
	//   description: the project id
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/Project"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "422":
	//     "$ref": "#/responses/validationError"

	projects.GetProject(ctx)
}

// UpdateProject updates a project by id
func (Project) UpdateProject(ctx *context.APIContext) {
	// swagger:operation PATCH /repos/{username}/{reponame}/projects/{project_id} repository repoUpdateProject
	// ---
	// summary: Update a project
	// consumes:
	// - application/json
	// parameters:
	// - name: username
	//   in: path
	//   description: name of the user
	//   type: string
	//   required: true
	// - name: reponame
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: project_id
	//   in: path
	//   description: the project id
	//   type: string
	//   required: true
	// - name: project
	//   in: body
	//   required: true
	//   schema:
	// 		"$ref": "#/definitions/CreateOrUpdateProjectOptions"
	// responses:
	//   "204":
	//     "description": "Successful update returns 204 No Content"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "422":
	//     "$ref": "#/responses/validationError"

	projects.UpdateProject(ctx)
}

// DeleteProject deletes a project by ID
func (Project) DeleteProject(ctx *context.APIContext) {
	// swagger:operation DELETE /repos/{username}/{reponame}/projects/{project_id} repository repoDeleteProject
	// ---
	// summary: Delete a project
	// parameters:
	// - name: username
	//   in: path
	//   description: name of the user
	//   type: string
	//   required: true
	// - name: reponame
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: project_id
	//   in: path
	//   description: the project id
	//   type: string
	//   required: true
	// responses:
	//   "204":
	//     "description": "Successful delete returns 204 No Content"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "422":
	//     "$ref": "#/responses/validationError"

	projects.DeleteProject(ctx)
}

var _ context.ProjectAPI = new(Project)

// Project implements context.ProjectAPI
type Project struct{}

// NewProject creates a new ProjectAPI service
func NewProjectAPI() context.ProjectAPI {
	return Project{}
}
