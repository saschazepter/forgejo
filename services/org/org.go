// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package org

import (
	"context"
	"fmt"

	"forgejo.org/models"
	"forgejo.org/models/db"
	org_model "forgejo.org/models/organization"
	packages_model "forgejo.org/models/packages"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/storage"
	"forgejo.org/modules/util"
	repo_service "forgejo.org/services/repository"
)

// RemoveOrgUser removes user from given organization.
func RemoveOrgUser(ctx context.Context, orgID, userID int64) error {
	ou := new(org_model.OrgUser)

	has, err := db.GetEngine(ctx).
		Where("uid=?", userID).
		And("org_id=?", orgID).
		Get(ou)
	if err != nil {
		return fmt.Errorf("get org-user: %w", err)
	} else if !has {
		return nil
	}

	org, err := org_model.GetOrgByID(ctx, orgID)
	if err != nil {
		return fmt.Errorf("GetUserByID [%d]: %w", orgID, err)
	}

	// Check if the user to delete is the last member in owner team.
	if isOwner, err := org_model.IsOrganizationOwner(ctx, orgID, userID); err != nil {
		return err
	} else if isOwner {
		t, err := org_model.GetOwnerTeam(ctx, org.ID)
		if err != nil {
			return err
		}
		if t.NumMembers == 1 {
			if err := t.LoadMembers(ctx); err != nil {
				return err
			}
			if t.Members[0].ID == userID {
				return org_model.ErrLastOrgOwner{UID: userID}
			}
		}
	}

	ctx, committer, err := db.TxContext(ctx)
	if err != nil {
		return err
	}
	defer committer.Close()

	if _, err := db.DeleteByID[org_model.OrgUser](ctx, ou.ID); err != nil {
		return err
	} else if _, err = db.Exec(ctx, "UPDATE `user` SET num_members=num_members-1 WHERE id=?", orgID); err != nil {
		return err
	}

	// Delete all repository accesses and unwatch them.
	env, err := org_model.AccessibleReposEnv(ctx, org, userID)
	if err != nil {
		return fmt.Errorf("AccessibleReposEnv: %w", err)
	}
	repoIDs, err := env.RepoIDs(1, org.NumRepos)
	if err != nil {
		return fmt.Errorf("GetUserRepositories [%d]: %w", userID, err)
	}
	for _, repoID := range repoIDs {
		if err = repo_model.WatchRepoExplicitly(ctx, userID, repoID, repo_model.WatchNoneSelection); err != nil {
			return err
		}
	}

	if len(repoIDs) > 0 {
		if _, err = db.GetEngine(ctx).
			Where("user_id = ?", userID).
			In("repo_id", repoIDs).
			Delete(new(access_model.Access)); err != nil {
			return err
		}
	}

	// Delete member in their teams.
	teams, err := org_model.GetUserOrgTeams(ctx, org.ID, userID)
	if err != nil {
		return err
	}
	for _, t := range teams {
		if err = removeTeamMember(ctx, t, userID); err != nil {
			return err
		}
	}

	return committer.Commit()
}

// DeleteOrganization completely and permanently deletes everything of organization.
func DeleteOrganization(ctx context.Context, org *org_model.Organization, purge bool) error {
	if purge {
		err := repo_service.DeleteOwnerRepositoriesDirectly(ctx, org.AsUser())
		if err != nil {
			return err
		}
	}

	ctx, committer, err := db.TxContext(ctx)
	if err != nil {
		return err
	}
	defer committer.Close()

	// Check ownership of repository.
	count, err := repo_model.CountRepositories(ctx, repo_model.CountRepositoryOptions{OwnerID: org.ID})
	if err != nil {
		return fmt.Errorf("GetRepositoryCount: %w", err)
	} else if count > 0 {
		return models.ErrUserOwnRepos{UID: org.ID}
	}

	// Check ownership of packages.
	if ownsPackages, err := packages_model.HasOwnerPackages(ctx, org.ID); err != nil {
		return fmt.Errorf("HasOwnerPackages: %w", err)
	} else if ownsPackages {
		return models.ErrUserOwnPackages{UID: org.ID}
	}

	if err := org_model.DeleteOrganization(ctx, org); err != nil {
		return fmt.Errorf("DeleteOrganization: %w", err)
	}

	if err := committer.Commit(); err != nil {
		return err
	}

	// FIXME: system notice
	// Note: There are something just cannot be roll back,
	//	so just keep error logs of those operations.
	path := user_model.UserPath(org.Name)

	if err := util.RemoveAll(path); err != nil {
		return fmt.Errorf("failed to RemoveAll %s: %w", path, err)
	}

	if len(org.Avatar) > 0 {
		avatarPath := org.CustomAvatarRelativePath()
		if err := storage.Avatars.Delete(avatarPath); err != nil {
			return fmt.Errorf("failed to remove %s: %w", avatarPath, err)
		}
	}

	return nil
}
