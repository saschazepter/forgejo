// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package access_test

import (
	"slices"
	"testing"

	"forgejo.org/models/db"
	perm_model "forgejo.org/models/perm"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccessLevel(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	user5 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	user29 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 29})
	// A public repository owned by User 2
	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	assert.False(t, repo1.IsPrivate)
	// A private repository owned by Org 3
	repo3 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	assert.True(t, repo3.IsPrivate)

	// Another public repository
	repo4 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
	assert.False(t, repo4.IsPrivate)
	// org. owned private repo
	repo24 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 24})

	level, err := access_model.AccessLevel(db.DefaultContext, user2, repo1)
	require.NoError(t, err)
	assert.Equal(t, perm_model.AccessModeOwner, level)

	level, err = access_model.AccessLevel(db.DefaultContext, user2, repo3)
	require.NoError(t, err)
	assert.Equal(t, perm_model.AccessModeOwner, level)

	level, err = access_model.AccessLevel(db.DefaultContext, user5, repo1)
	require.NoError(t, err)
	assert.Equal(t, perm_model.AccessModeRead, level)

	level, err = access_model.AccessLevel(db.DefaultContext, user5, repo3)
	require.NoError(t, err)
	assert.Equal(t, perm_model.AccessModeNone, level)

	// restricted user has no access to a public repo
	level, err = access_model.AccessLevel(db.DefaultContext, user29, repo1)
	require.NoError(t, err)
	assert.Equal(t, perm_model.AccessModeNone, level)

	// ... unless he's a collaborator
	level, err = access_model.AccessLevel(db.DefaultContext, user29, repo4)
	require.NoError(t, err)
	assert.Equal(t, perm_model.AccessModeWrite, level)

	// ... or a team member
	level, err = access_model.AccessLevel(db.DefaultContext, user29, repo24)
	require.NoError(t, err)
	assert.Equal(t, perm_model.AccessModeRead, level)
}

func TestHasAccess(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	// A public repository owned by User 2
	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	assert.False(t, repo1.IsPrivate)
	// A private repository owned by Org 3
	repo2 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	assert.True(t, repo2.IsPrivate)

	has, err := access_model.HasAccess(db.DefaultContext, user1.ID, repo1)
	require.NoError(t, err)
	assert.True(t, has)

	_, err = access_model.HasAccess(db.DefaultContext, user1.ID, repo2)
	require.NoError(t, err)

	_, err = access_model.HasAccess(db.DefaultContext, user2.ID, repo1)
	require.NoError(t, err)

	_, err = access_model.HasAccess(db.DefaultContext, user2.ID, repo2)
	require.NoError(t, err)
}

func TestRepository_RecalculateAccesses(t *testing.T) {
	// test with organization repo
	require.NoError(t, unittest.PrepareTestDatabase())
	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	require.NoError(t, repo1.LoadOwner(db.DefaultContext))

	_, err := db.GetEngine(db.DefaultContext).Delete(&repo_model.Collaboration{UserID: 2, RepoID: 3})
	require.NoError(t, err)
	require.NoError(t, access_model.RecalculateAccesses(db.DefaultContext, repo1))

	access := &access_model.Access{UserID: 2, RepoID: 3}
	has, err := db.GetEngine(db.DefaultContext).Get(access)
	require.NoError(t, err)
	assert.True(t, has)
	assert.Equal(t, perm_model.AccessModeOwner, access.Mode)
}

func TestRepository_RecalculateAccesses2(t *testing.T) {
	// test with non-organization repo
	require.NoError(t, unittest.PrepareTestDatabase())
	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
	require.NoError(t, repo1.LoadOwner(db.DefaultContext))

	_, err := db.GetEngine(db.DefaultContext).Delete(&repo_model.Collaboration{UserID: 4, RepoID: 4})
	require.NoError(t, err)
	require.NoError(t, access_model.RecalculateAccesses(db.DefaultContext, repo1))

	has, err := db.GetEngine(db.DefaultContext).Get(&access_model.Access{UserID: 4, RepoID: 4})
	require.NoError(t, err)
	assert.False(t, has)
}

func TestRepository_RecalculateAccessRestrictedUser(t *testing.T) {
	defer unittest.OverrideFixtures("models/perm/access/TestRepository_RecalculateAccessRestrictedUser")()
	require.NoError(t, unittest.PrepareTestDatabase())

	// User 29 is a restricted user in the fixture data that will be used in this test:
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 29})
	assert.True(t, user.IsRestricted)

	// user5/repo4, public repo; user29 is a write collaborator on it
	repo4 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})

	// org17/big_test_private_4, private repo in org17, team.id=9 (reivew_team) gives read access
	repo24 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 24})

	// user10/repo8, public repo; user29 is a read collaborator on it.  Public repos typically don't get AccessModeRead
	// entries in the Access table because they're already public, but, for restricted users they must be present.
	repo8 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 8})

	assertAccess := func(t *testing.T) {
		access4 := unittest.AssertExistsAndLoadBean(t, &access_model.Access{UserID: user.ID, RepoID: repo4.ID})
		assert.Equal(t, perm_model.AccessModeWrite, access4.Mode)
		access24 := unittest.AssertExistsAndLoadBean(t, &access_model.Access{UserID: user.ID, RepoID: repo24.ID})
		assert.Equal(t, perm_model.AccessModeRead, access24.Mode)
		access8 := unittest.AssertExistsAndLoadBean(t, &access_model.Access{UserID: user.ID, RepoID: repo8.ID})
		assert.Equal(t, perm_model.AccessModeRead, access8.Mode)
	}

	_, err := db.GetEngine(t.Context()).Delete(&access_model.Access{UserID: user.ID})
	require.NoError(t, err)

	// Recalculate via RecalculateAccesses:
	require.NoError(t, access_model.RecalculateAccesses(t.Context(), repo4))
	require.NoError(t, access_model.RecalculateAccesses(t.Context(), repo24))
	require.NoError(t, access_model.RecalculateAccesses(t.Context(), repo8))
	assertAccess(t)

	_, err = db.GetEngine(t.Context()).Delete(&access_model.Access{UserID: user.ID})
	require.NoError(t, err)

	// Recalculate via RecalculateUserAccess:
	require.NoError(t, access_model.RecalculateUserAccess(t.Context(), repo4, user.ID))
	require.NoError(t, access_model.RecalculateUserAccess(t.Context(), repo24, user.ID))
	require.NoError(t, access_model.RecalculateUserAccess(t.Context(), repo8, user.ID))
	assertAccess(t)
}

func TestRepository_RecalculateAccessPublicRepoReader(t *testing.T) {
	defer unittest.OverrideFixtures("models/perm/access/TestRepository_RecalculateAccessPublicRepoReader")()
	require.NoError(t, unittest.PrepareTestDatabase())

	// Even though a repository may be public and readable to anyone, an explicit grant of read access (from a team
	// membership or a real-level collaborator) should populate the access table, allowing that user to appear in
	// `GetRepoReaders`, and in the repo search on the user dashboard.

	// org41/repo61, public repo in a public org; team.id=22 (team1) gives read access to user38 & user39
	repo61 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 61})
	assert.False(t, repo61.IsPrivate)
	user38 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 38})
	assert.False(t, user38.IsRestricted)
	user39 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 39})
	assert.False(t, user39.IsRestricted)

	// user2/repo1, public repo; user4 is a read collaborator on it
	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	assert.False(t, repo1.IsPrivate)
	user4 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	assert.False(t, user4.IsRestricted)

	type grant struct {
		repo *repo_model.Repository
		user *user_model.User
	}
	grants := []grant{{repo61, user38}, {repo61, user39}, {repo1, user4}}

	for _, g := range grants {
		_, err := db.GetEngine(t.Context()).Delete(&access_model.Access{UserID: g.user.ID, RepoID: g.repo.ID})
		require.NoError(t, err)
	}

	require.NoError(t, access_model.RecalculateAccesses(t.Context(), repo61))
	require.NoError(t, access_model.RecalculateAccesses(t.Context(), repo1))

	for _, g := range grants {
		access := unittest.AssertExistsAndLoadBean(t, &access_model.Access{UserID: g.user.ID, RepoID: g.repo.ID})
		assert.Equal(t, perm_model.AccessModeRead, access.Mode)

		isReader, err := access_model.IsRepoReader(t.Context(), g.repo, g.user.ID)
		require.NoError(t, err)
		assert.True(t, isReader)

		readers, err := access_model.GetRepoReaders(t.Context(), g.repo)
		require.NoError(t, err)
		assert.True(t, slices.ContainsFunc(readers, func(u *user_model.User) bool { return u.ID == g.user.ID }))
	}
}
