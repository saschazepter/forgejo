// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package access

import (
	"context"
	"fmt"

	"forgejo.org/models/db"
	"forgejo.org/models/perm"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"

	"xorm.io/builder"
)

// Access represents the highest access level of a user to the repository. The only access type
// that is not in this table is the real owner of a repository. In case of an organization
// repository, the members of the owners team are in this table.
type Access struct {
	ID     int64 `xorm:"pk autoincr"`
	UserID int64 `xorm:"UNIQUE(s) REFERENCES(user, id)"`
	RepoID int64 `xorm:"UNIQUE(s) REFERENCES(repository, id)"`
	Mode   perm.AccessMode
}

func init() {
	db.RegisterModel(new(Access))
}

func accessLevel(ctx context.Context, user *user_model.User, repo *repo_model.Repository) (perm.AccessMode, error) {
	mode := perm.AccessModeNone
	var userID int64
	restricted := false

	if user != nil {
		userID = user.ID
		restricted = user.IsRestricted
	}

	if !restricted && !repo.IsPrivate {
		mode = perm.AccessModeRead
	}

	if userID == 0 {
		return mode, nil
	}

	if userID == repo.OwnerID {
		return perm.AccessModeOwner, nil
	}

	a, exist, err := db.Get[Access](ctx, builder.Eq{"user_id": userID, "repo_id": repo.ID})
	if err != nil {
		return mode, err
	} else if !exist {
		return mode, nil
	}
	return a.Mode, nil
}

type recalcAccess struct {
	users      optional.Option[[]int64] // UserID
	repos      optional.Option[[]int64] // RepoID
	ignoreTeam optional.Option[int64]   // TeamID
}

type accessKey struct {
	userID int64
	repoID int64
}

func recalculateAccess(ctx context.Context, recalc recalcAccess) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		accessMap := make(map[accessKey]perm.AccessMode, 20)

		// Build user access map based upon collaborator records:
		if err := updateAccessMapByCollaborators(ctx, accessMap, recalc); err != nil {
			return fmt.Errorf("update access map by collaborators: %w", err)
		}

		// Update user access map based upon team membership:
		if err := updateAccessMapByTeamMembership(ctx, accessMap, recalc); err != nil {
			return fmt.Errorf("update access map by team membership: %w", err)
		}

		removeNoneAccess(accessMap)

		newAccesses := make([]Access, 0, len(accessMap))
		for key, accessMode := range accessMap {
			newAccesses = append(newAccesses, Access{
				UserID: key.userID,
				RepoID: key.repoID,
				Mode:   accessMode,
			})
		}

		// Delete existing Access records:
		accessCond := builder.NewCond()
		if hasUserList, userList := recalc.users.Get(); hasUserList {
			accessCond = accessCond.And(builder.In("user_id", userList))
		}
		if hasRepoList, repoList := recalc.repos.Get(); hasRepoList {
			accessCond = accessCond.And(builder.In("repo_id", repoList))
		}
		if _, err := db.GetEngine(ctx).Where(accessCond).Delete(new(Access)); err != nil {
			return fmt.Errorf("access batch delete error: %w", err)
		}

		// Insert newly created Access records:
		batchSize := db.MaxBatchInsertSize(new(Access))
		for len(newAccesses) > 0 {
			batch := newAccesses[:min(len(newAccesses), batchSize)]
			if err := db.Insert(ctx, batch); err != nil {
				return fmt.Errorf("access batch insert error: %w", err)
			}
			newAccesses = newAccesses[len(batch):]
		}

		return nil
	})
}

func updateAccessMapByCollaborators(ctx context.Context, accessMap map[accessKey]perm.AccessMode, recalc recalcAccess) error {
	collabCond := builder.NewCond()
	if hasUserList, userList := recalc.users.Get(); hasUserList {
		collabCond = collabCond.And(builder.In("user_id", userList))
	}
	if hasRepoList, repoList := recalc.repos.Get(); hasRepoList {
		collabCond = collabCond.And(builder.In("repo_id", repoList))
	}
	if err := db.Iterate(ctx, collabCond, func(ctx context.Context, c *repo_model.Collaboration) error {
		key := accessKey{userID: c.UserID, repoID: c.RepoID}
		updateUserAccess(accessMap, key, c.Mode)
		return nil
	}); err != nil {
		return fmt.Errorf("iterate collaboration: %w", err)
	}
	return nil
}

func updateAccessMapByTeamMembership(ctx context.Context, accessMap map[accessKey]perm.AccessMode, recalc recalcAccess) error {
	hasIgnoreTeam, ignoreTeam := recalc.ignoreTeam.Get()

	teamCond := builder.NewCond()
	if hasUserList, userList := recalc.users.Get(); hasUserList {
		teamCond = teamCond.And(builder.In("team_user.uid", userList))
	}
	if hasRepoList, repoList := recalc.repos.Get(); hasRepoList {
		teamCond = teamCond.And(builder.In("repository.id", repoList))
	}
	if hasIgnoreTeam {
		// During delete operations for a team, a recalc is completed with a team ignored
		teamCond = teamCond.And(builder.Neq{"team.id": ignoreTeam})
	}

	type teamMembershipAccess struct {
		UserID         int64 `xorm:"uid"`
		RepoID         int64
		TeamAccessMode perm.AccessMode `xorm:"authorize"`
	}
	var teams []*teamMembershipAccess
	if err := db.GetEngine(ctx).
		Select("team_user.uid, team_repo.repo_id, team.authorize").
		Table("team").
		Join("INNER", "team_repo", "team_repo.team_id = team.id").
		Join("INNER", "team_user", "team_user.team_id = team.id").
		Join("INNER", "repository", "repository.id = team_repo.repo_id").
		And(teamCond).
		Find(&teams); err != nil {
		return fmt.Errorf("team membership query: %w", err)
	}
	for _, team := range teams {
		key := accessKey{userID: team.UserID, repoID: team.RepoID}
		updateUserAccess(accessMap, key, team.TeamAccessMode)
	}

	return nil
}

func removeNoneAccess(accessMap map[accessKey]perm.AccessMode) {
	for key, value := range accessMap {
		if value < perm.AccessModeRead {
			delete(accessMap, key)
		}
	}
}

func maxAccessMode(modes ...perm.AccessMode) perm.AccessMode {
	max := perm.AccessModeNone
	for _, mode := range modes {
		if mode > max {
			max = mode
		}
	}
	return max
}

func updateUserAccess(accessMap map[accessKey]perm.AccessMode, key accessKey, mode perm.AccessMode) {
	if lastMode, ok := accessMap[key]; ok {
		accessMap[key] = maxAccessMode(lastMode, mode)
	} else {
		accessMap[key] = mode
	}
}

// RecalculateTeamAccesses recalculates new accesses for teams of an organization
// except the team whose ID is given. It is used to assign a team ID when
// remove repository from that team.
func RecalculateTeamAccesses(ctx context.Context, repo *repo_model.Repository, ignTeamID int64) (err error) {
	var ign optional.Option[int64]
	if ignTeamID == 0 {
		ign = optional.None[int64]()
	} else {
		ign = optional.Some(ignTeamID)
	}
	return recalculateAccess(ctx, recalcAccess{
		repos:      optional.Some([]int64{repo.ID}),
		ignoreTeam: ign,
	})
}

// RecalculateUserAccess recalculates new access for a single user
// Usable if we know access only affected one user
func RecalculateUserAccess(ctx context.Context, repo *repo_model.Repository, uid int64) (err error) {
	return recalculateAccess(ctx, recalcAccess{
		repos: optional.Some([]int64{repo.ID}),
		users: optional.Some([]int64{uid}),
	})
}

// RecalculateAccesses recalculates all accesses for repository.
func RecalculateAccesses(ctx context.Context, repo *repo_model.Repository) error {
	return recalculateAccess(ctx, recalcAccess{
		repos: optional.Some([]int64{repo.ID}),
	})
}

func RecalculateUserAccessForRepos(ctx context.Context, userID int64, repoIDs []int64) (err error) {
	return recalculateAccess(ctx, recalcAccess{
		repos: optional.Some(repoIDs),
		users: optional.Some([]int64{userID}),
	})
}
