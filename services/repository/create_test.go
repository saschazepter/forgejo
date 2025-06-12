// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"fmt"
	"testing"

	"forgejo.org/models"
	"forgejo.org/models/db"
	"forgejo.org/models/organization"
	"forgejo.org/models/perm"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/structs"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIncludesAllRepositoriesTeams(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	testTeamRepositories := func(teamID int64, repoIds []int64) {
		team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: teamID})
		require.NoError(t, team.LoadRepositories(db.DefaultContext), "%s: GetRepositories", team.Name)
		assert.Len(t, team.Repos, team.NumRepos, "%s: len repo", team.Name)
		assert.Len(t, team.Repos, len(repoIds), "%s: repo count", team.Name)
		for i, rid := range repoIds {
			if rid > 0 {
				assert.True(t, HasRepository(db.DefaultContext, team, rid), "%s: HasRepository(%d) %d", rid, i)
			}
		}
	}

	// Get an admin user.
	user, err := user_model.GetUserByID(db.DefaultContext, 1)
	require.NoError(t, err, "GetUserByID")

	// Create org.
	org := &organization.Organization{
		Name:       "All_repo",
		IsActive:   true,
		Type:       user_model.UserTypeOrganization,
		Visibility: structs.VisibleTypePublic,
	}
	require.NoError(t, organization.CreateOrganization(db.DefaultContext, org, user), "CreateOrganization")

	// Check Owner team.
	ownerTeam, err := org.GetOwnerTeam(db.DefaultContext)
	require.NoError(t, err, "GetOwnerTeam")
	assert.True(t, ownerTeam.IncludesAllRepositories, "Owner team includes all repositories")

	// Create repos.
	repoIDs := make([]int64, 0)
	for i := 0; i < 3; i++ {
		r, err := CreateRepositoryDirectly(db.DefaultContext, user, org.AsUser(), CreateRepoOptions{Name: fmt.Sprintf("repo-%d", i)})
		require.NoError(t, err, "CreateRepository %d", i)
		if r != nil {
			repoIDs = append(repoIDs, r.ID)
		}
	}
	// Get fresh copy of Owner team after creating repos.
	ownerTeam, err = org.GetOwnerTeam(db.DefaultContext)
	require.NoError(t, err, "GetOwnerTeam")

	// Create teams and check repositories.
	teams := []*organization.Team{
		ownerTeam,
		{
			OrgID:                   org.ID,
			Name:                    "team one",
			AccessMode:              perm.AccessModeRead,
			IncludesAllRepositories: true,
		},
		{
			OrgID:                   org.ID,
			Name:                    "team 2",
			AccessMode:              perm.AccessModeRead,
			IncludesAllRepositories: false,
		},
		{
			OrgID:                   org.ID,
			Name:                    "team three",
			AccessMode:              perm.AccessModeWrite,
			IncludesAllRepositories: true,
		},
		{
			OrgID:                   org.ID,
			Name:                    "team 4",
			AccessMode:              perm.AccessModeWrite,
			IncludesAllRepositories: false,
		},
	}
	teamRepos := [][]int64{
		repoIDs,
		repoIDs,
		{},
		repoIDs,
		{},
	}
	for i, team := range teams {
		if i > 0 { // first team is Owner.
			require.NoError(t, models.NewTeam(db.DefaultContext, team), "%s: NewTeam", team.Name)
		}
		testTeamRepositories(team.ID, teamRepos[i])
	}

	// Update teams and check repositories.
	teams[3].IncludesAllRepositories = false
	teams[4].IncludesAllRepositories = true
	teamRepos[4] = repoIDs
	for i, team := range teams {
		require.NoError(t, models.UpdateTeam(db.DefaultContext, team, false, true), "%s: UpdateTeam", team.Name)
		testTeamRepositories(team.ID, teamRepos[i])
	}

	// Create repo and check teams repositories.
	r, err := CreateRepositoryDirectly(db.DefaultContext, user, org.AsUser(), CreateRepoOptions{Name: "repo-last"})
	require.NoError(t, err, "CreateRepository last")
	if r != nil {
		repoIDs = append(repoIDs, r.ID)
	}
	teamRepos[0] = repoIDs
	teamRepos[1] = repoIDs
	teamRepos[4] = repoIDs
	for i, team := range teams {
		testTeamRepositories(team.ID, teamRepos[i])
	}

	// Remove repo and check teams repositories.
	require.NoError(t, DeleteRepositoryDirectly(db.DefaultContext, user, repoIDs[0]), "DeleteRepository")
	teamRepos[0] = repoIDs[1:]
	teamRepos[1] = repoIDs[1:]
	teamRepos[3] = repoIDs[1:3]
	teamRepos[4] = repoIDs[1:]
	for i, team := range teams {
		testTeamRepositories(team.ID, teamRepos[i])
	}

	// Wipe created items.
	for i, rid := range repoIDs {
		if i > 0 { // first repo already deleted.
			require.NoError(t, DeleteRepositoryDirectly(db.DefaultContext, user, rid), "DeleteRepository %d", i)
		}
	}
	require.NoError(t, organization.DeleteOrganization(db.DefaultContext, org), "DeleteOrganization")
}

func TestCreateRepository(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})

	r, err := CreateRepositoryDirectly(db.DefaultContext, user, user, CreateRepoOptions{Name: "repo-last"})
	require.NoError(t, err)
	require.NotNil(t, r.Topics)
	require.Empty(t, r.Topics)
}
