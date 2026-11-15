// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package org

import (
	"context"
	"fmt"

	"forgejo.org/models"
	"forgejo.org/models/db"
	git_model "forgejo.org/models/git"
	issues_model "forgejo.org/models/issues"
	"forgejo.org/models/organization"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"

	"xorm.io/builder"
)

// AddTeamMemberByCooptation adds a user to a team upon invitation of another user
func AddTeamMemberByCooptation(ctx context.Context, team *organization.Team, user, inviter *user_model.User) error {
	_, err := models.InsertTeamMember(ctx, team, user.ID, organization.MembershipReasonAddedByUser, optional.Some(inviter.ID), optional.None[int64]())
	return err
}

// AddTeamMemberByLoginSource adds a user to a team based on some metadata offered by a login source
func AddTeamMemberByLoginSource(ctx context.Context, team *organization.Team, userID, loginSourceID int64) error {
	_, err := models.InsertTeamMember(ctx, team, userID, organization.MembershipReasonAddedByAuthProvider, optional.None[int64](), optional.Some(loginSourceID))
	return err
}

// RemoveTeamMember removes member from given team of given organization.
func RemoveTeamMember(ctx context.Context, team *organization.Team, oldMember, doer *user_model.User) error {
	ctx, committer, err := db.TxContext(ctx)
	if err != nil {
		return err
	}
	defer committer.Close()
	if err := removeTeamMember(ctx, team, oldMember.ID); err != nil {
		return err
	}
	return committer.Commit()
}

func removeTeamMember(ctx context.Context, team *organization.Team, userID int64) error {
	e := db.GetEngine(ctx)
	isMember, err := organization.IsTeamMember(ctx, team.OrgID, team.ID, userID)
	if err != nil || !isMember {
		return err
	}

	// Check if the user to delete is the last member in owner team.
	if team.IsOwnerTeam() && team.NumMembers == 1 {
		return organization.ErrLastOrgOwner{UID: userID}
	}

	team.NumMembers--

	if err := team.LoadRepositories(ctx); err != nil {
		return err
	}

	if _, err := e.Delete(&organization.TeamUser{
		UID:    userID,
		OrgID:  team.OrgID,
		TeamID: team.ID,
	}); err != nil {
		return err
	} else if _, err = e.
		ID(team.ID).
		Cols("num_members").
		Update(team); err != nil {
		return err
	}

	// Delete access to team repositories.
	for _, repo := range team.Repos {
		if err := access_model.RecalculateUserAccess(ctx, repo, userID); err != nil {
			return err
		}

		// Remove watches from now inaccessible
		if err := models.ReconsiderWatches(ctx, repo, userID); err != nil {
			return err
		}

		// Remove issue assignments from now inaccessible
		if err := models.ReconsiderRepoIssuesAssignee(ctx, repo, userID); err != nil {
			return err
		}
	}

	return removeInvalidOrgUser(ctx, userID, team.OrgID)
}

func removeInvalidOrgUser(ctx context.Context, userID, orgID int64) error {
	// Check if the user is a member of any team in the organization.
	if count, err := db.GetEngine(ctx).Count(&organization.TeamUser{
		UID:   userID,
		OrgID: orgID,
	}); err != nil {
		return err
	} else if count == 0 {
		return RemoveOrgUser(ctx, orgID, userID)
	}
	return nil
}

// DeleteTeam deletes given team.
// It's caller's responsibility to assign organization ID.
func DeleteTeam(ctx context.Context, t *organization.Team) error {
	ctx, committer, err := db.TxContext(ctx)
	if err != nil {
		return err
	}
	defer committer.Close()

	if err := t.LoadRepositories(ctx); err != nil {
		return err
	}

	if err := t.LoadMembers(ctx); err != nil {
		return err
	}

	// update branch protections
	{
		protections := make([]*git_model.ProtectedBranch, 0, 10)
		err := db.GetEngine(ctx).In("repo_id",
			builder.Select("id").From("repository").Where(builder.Eq{"owner_id": t.OrgID})).
			Find(&protections)
		if err != nil {
			return fmt.Errorf("findProtectedBranches: %w", err)
		}
		for _, p := range protections {
			if err := git_model.RemoveTeamIDFromProtectedBranch(ctx, p, t.ID); err != nil {
				return err
			}
		}
	}

	if err := removeAllRepositories(ctx, t); err != nil {
		return err
	}

	if err := db.DeleteBeans(ctx,
		&organization.Team{ID: t.ID},
		&organization.TeamUser{OrgID: t.OrgID, TeamID: t.ID},
		&organization.TeamUnit{TeamID: t.ID},
		&organization.TeamInvite{TeamID: t.ID},
		&issues_model.Review{Type: issues_model.ReviewTypeRequest, ReviewerTeamID: t.ID}, // batch delete the binding relationship between team and PR (request review from team)
	); err != nil {
		return err
	}

	for _, tm := range t.Members {
		if err := removeInvalidOrgUser(ctx, tm.ID, t.OrgID); err != nil {
			return err
		}
	}

	// Update organization number of teams.
	if _, err := db.Exec(ctx, "UPDATE `user` SET num_teams=num_teams-1 WHERE id=?", t.OrgID); err != nil {
		return err
	}

	return committer.Commit()
}

// RemoveAllRepositories removes all repositories from team and recalculates access
func RemoveAllRepositories(ctx context.Context, t *organization.Team) (err error) {
	if t.IncludesAllRepositories {
		return nil
	}

	ctx, committer, err := db.TxContext(ctx)
	if err != nil {
		return err
	}
	defer committer.Close()

	if err = removeAllRepositories(ctx, t); err != nil {
		return err
	}

	return committer.Commit()
}

// removeAllRepositories removes all repositories from team and recalculates access
// Note: Shall not be called if team includes all repositories
func removeAllRepositories(ctx context.Context, t *organization.Team) (err error) {
	e := db.GetEngine(ctx)

	if err := t.LoadRepositories(ctx); err != nil {
		return fmt.Errorf("load repositories failed: %w", err)
	}
	if err := t.LoadMembers(ctx); err != nil {
		return fmt.Errorf("load repositories failed: %w", err)
	}

	// Delete all accesses.
	for _, repo := range t.Repos {
		if err := access_model.RecalculateTeamAccesses(ctx, repo, t.ID); err != nil {
			return err
		}

		// Remove watches from all users and now inaccessible repos
		for _, user := range t.Members {
			has, err := access_model.HasAccess(ctx, user.ID, repo)
			if err != nil {
				return err
			} else if has {
				continue
			}

			if err = repo_model.WatchRepoExplicitly(ctx, user.ID, repo.ID, repo_model.WatchNoneSelection); err != nil {
				return err
			}

			// Remove all IssueWatches a user has subscribed to in the repositories
			if err = issues_model.RemoveIssueWatchersByRepoID(ctx, user.ID, repo.ID); err != nil {
				return err
			}
		}
	}

	// Delete team-repo
	if _, err := e.
		Where("team_id=?", t.ID).
		Delete(new(organization.TeamRepo)); err != nil {
		return err
	}

	t.NumRepos = 0
	if _, err = e.ID(t.ID).Cols("num_repos").Update(t); err != nil {
		return err
	}

	return nil
}
