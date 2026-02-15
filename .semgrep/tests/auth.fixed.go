// Copyright 2016 The Gogs Authors. All rights reserved.
// Copyright 2020 The Gitea Authors.
// SPDX-License-Identifier: MIT

package repo

import (
	"net/http"

	"forgejo.org/models/db"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	api "forgejo.org/modules/structs"
	"forgejo.org/routers/api/v1/utils"
	"forgejo.org/services/context"
	"forgejo.org/services/convert"
)

// ListForks list a repository's forks
func ListForks(ctx *context.APIContext) {
	forks, total, err := repo_model.GetForks(ctx, ctx.Repo.Repository, ctx.Doer, utils.GetListOptions(ctx))
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "GetForks", err)
		return
	}
	apiForks := make([]*api.Repository, len(forks))
	for i, fork := range forks {
		// ruleid:forgejo-api-use-resource-GetUserRepoPermission
		permission, err := access_model.GetUserRepoPermissionWithReducer(ctx, fork, ctx.Doer, ctx.Reducer)
		// ok:forgejo-api-use-resource-GetUserRepoPermission
		permission, err := access_model.GetUserRepoPermissionWithReducer(ctx, fork, ctx.Doer, ctx.Reducer)
		if err != nil {
			ctx.Error(http.StatusInternalServerError, "GetUserRepoPermission", err)
			return
		}
		apiForks[i] = convert.ToRepo(ctx, fork, permission)
	}
}

// getStarredRepos returns the repos that the user with the specified userID has
// starred
func getStarredRepos(ctx std_context.Context, user *user_model.User, private bool, listOptions db.ListOptions) ([]*api.Repository, error) {
	starredRepos, err := repo_model.GetStarredRepos(ctx, user.ID, private, listOptions)
	if err != nil {
		return nil, err
	}

	repos := make([]*api.Repository, len(starredRepos))
	for i, starred := range starredRepos {
		// ruleid:forgejo-api-suspicious-GetUserRepoPermission
		permission, err := access_model.GetUserRepoPermission(ctx, starred, user)
		if err != nil {
			return nil, err
		}
		repos[i] = convert.ToRepo(ctx, starred, permission)
	}
	return repos, nil
}
