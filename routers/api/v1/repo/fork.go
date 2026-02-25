// Copyright 2016 The Gogs Authors. All rights reserved.
// Copyright 2020 The Gitea Authors.
// SPDX-License-Identifier: MIT

package repo

import (
	"errors"
	"fmt"
	"net/http"

	"forgejo.org/models/organization"
	"forgejo.org/models/perm"
	access_model "forgejo.org/models/perm/access"
	quota_model "forgejo.org/models/quota"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/util"
	"forgejo.org/modules/web"
	"forgejo.org/routers/api/v1/utils"
	"forgejo.org/services/context"
	"forgejo.org/services/convert"
	repo_service "forgejo.org/services/repository"
)

// ListForks list a repository's forks
func ListForks(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/forks repository listForks
	// ---
	// summary: List a repository's forks
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repo
	//   type: string
	//   required: true
	// - name: page
	//   in: query
	//   description: page number of results to return (1-based)
	//   type: integer
	// - name: limit
	//   in: query
	//   description: page size of results
	//   type: integer
	// responses:
	//   "200":
	//     "$ref": "#/responses/RepositoryList"
	//   "404":
	//     "$ref": "#/responses/notFound"

	forks, total, err := repo_model.GetForks(ctx, ctx.Repo.Repository, ctx.Doer, utils.GetListOptions(ctx))
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "GetForks", err)
		return
	}
	apiForks := make([]*api.Repository, len(forks))
	for i, fork := range forks {
		// GetUserRepoPermission is used which doesn't take into account fine-grained access token's permissions. A fork
		// cannot have a different visibility than the original repo. It makes sense that if you can view a `ctx.Repo`
		// (guaranteed by the `repoAssignment` middleware used on the API endpoint), you can view the forks of that
		// repo.
		//
		// nosemgrep: forgejo-api-use-resource-GetUserRepoPermission
		permission, err := access_model.GetUserRepoPermission(ctx, fork, ctx.Doer)
		if err != nil {
			ctx.Error(http.StatusInternalServerError, "GetUserRepoPermission", err)
			return
		}
		apiForks[i] = convert.ToRepo(ctx, fork, permission)
	}

	ctx.SetTotalCountHeader(total)
	ctx.JSON(http.StatusOK, apiForks)
}

// CreateFork create a fork of a repo
func CreateFork(ctx *context.APIContext) {
	// swagger:operation POST /repos/{owner}/{repo}/forks repository createFork
	// ---
	// summary: Fork a repository
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo to fork
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repo to fork
	//   type: string
	//   required: true
	// - name: body
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/CreateForkOption"
	// responses:
	//   "202":
	//     "$ref": "#/responses/Repository"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "409":
	//     description: The repository with the same name already exists.
	//   "413":
	//     "$ref": "#/responses/quotaExceeded"
	//   "422":
	//     "$ref": "#/responses/validationError"

	form := web.GetForm(ctx).(*api.CreateForkOption)
	repo := ctx.Repo.Repository
	var forker *user_model.User // user/org that will own the fork
	if form.Organization == nil {
		forker = ctx.Doer
	} else {
		org, err := organization.GetOrgByName(ctx, *form.Organization)
		if err != nil {
			if organization.IsErrOrgNotExist(err) {
				ctx.Error(http.StatusUnprocessableEntity, "", err)
			} else {
				ctx.Error(http.StatusInternalServerError, "GetOrgByName", err)
			}
			return
		}
		isMember, err := org.IsOrgMember(ctx, ctx.Doer.ID)
		if err != nil {
			ctx.Error(http.StatusInternalServerError, "IsOrgMember", err)
			return
		} else if !isMember {
			ctx.Error(http.StatusForbidden, "isMemberNot", fmt.Sprintf("User is no Member of Organisation '%s'", org.Name))
			return
		}
		forker = org.AsUser()
	}

	if !ctx.CheckQuota(quota_model.LimitSubjectSizeReposAll, forker.ID, forker.Name) {
		return
	}

	var name string
	if form.Name == nil {
		name = repo.Name
	} else {
		name = *form.Name
	}

	fork, err := repo_service.ForkRepositoryAndUpdates(ctx, ctx.Doer, forker, repo_service.ForkRepoOptions{
		BaseRepo:    repo,
		Name:        name,
		Description: repo.Description,
	})
	if err != nil {
		if errors.Is(err, util.ErrAlreadyExist) || repo_model.IsErrReachLimitOfRepo(err) {
			ctx.Error(http.StatusConflict, "ForkRepositoryAndUpdates", err)
		} else {
			ctx.Error(http.StatusInternalServerError, "ForkRepositoryAndUpdates", err)
		}
		return
	}

	// TODO change back to 201
	ctx.JSON(http.StatusAccepted, convert.ToRepo(ctx, fork, access_model.Permission{AccessMode: perm.AccessModeOwner}))
}
