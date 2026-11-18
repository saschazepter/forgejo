// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package authz

import (
	"context"
	"fmt"

	"forgejo.org/models/perm"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/modules/structs"

	"xorm.io/builder"
)

// Grants access only to public repositories.  Does not change the level of access for any public repos.
type PublicReposAuthorizationReducer struct{}

func (*PublicReposAuthorizationReducer) ReduceRepoAccess(ctx context.Context, repo *repo_model.Repository, accessMode perm.AccessMode) (perm.AccessMode, error) {
	if err := repo.LoadOwner(ctx); err != nil {
		return 0, fmt.Errorf("failed to LoadOwner during ReduceRepoAccess: %w", err)
	}

	// Fine-grained access tokens remove access to any private repositories, or repository owned by non-public users,
	// that aren't listed in their resource list.
	if !repo.Owner.Visibility.IsPublic() || repo.IsPrivate {
		return perm.AccessModeNone, nil
	}

	return accessMode, nil
}

func (*PublicReposAuthorizationReducer) RepoReadAccessFilter() builder.Cond {
	// Allow access only to non-private repositories, that aren't in a private or limited organization.
	return builder.And(
		builder.Eq{"`repository`.is_private": false},
		builder.NotIn("owner_id", builder.Select("id").From("`user`").Where(
			builder.Or(builder.Eq{"visibility": structs.VisibleTypeLimited}, builder.Eq{"visibility": structs.VisibleTypePrivate}),
		)))
}

func (*PublicReposAuthorizationReducer) AllowAdminOverride() bool {
	return false
}
