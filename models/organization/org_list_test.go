// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package organization_test

import (
	"slices"
	"strings"
	"testing"

	"forgejo.org/models/db"
	"forgejo.org/models/organization"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCountOrganizations(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	expected, err := db.GetEngine(db.DefaultContext).Where("type=?", user_model.UserTypeOrganization).Count(&organization.Organization{})
	require.NoError(t, err)
	cnt, err := db.Count[organization.Organization](db.DefaultContext, organization.FindOrgOptions{IncludePrivate: true})
	require.NoError(t, err)
	assert.Equal(t, expected, cnt)
}

func TestFindOrgs(t *testing.T) {
	defer unittest.OverrideFixtures("models/organization/TestFindOrgs")()
	require.NoError(t, unittest.PrepareTestDatabase())

	orgs, err := db.Find[organization.Organization](db.DefaultContext, organization.FindOrgOptions{
		UserID:         4,
		IncludePrivate: true,
	})
	require.NoError(t, err)
	if assert.Len(t, orgs, 2) {
		if orgs[0].ID == 22 {
			assert.EqualValues(t, 22, orgs[0].ID)
			assert.EqualValues(t, 3, orgs[1].ID)
		} else {
			assert.EqualValues(t, 3, orgs[0].ID)
			assert.EqualValues(t, 22, orgs[1].ID)
		}
	}

	orgs, err = db.Find[organization.Organization](db.DefaultContext, organization.FindOrgOptions{
		UserID:         4,
		IncludePrivate: false,
	})
	require.NoError(t, err)
	assert.Empty(t, orgs)

	total, err := db.Count[organization.Organization](db.DefaultContext, organization.FindOrgOptions{
		UserID:         4,
		IncludePrivate: true,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)

	total, err = db.Count[organization.Organization](db.DefaultContext, organization.FindOrgOptions{
		UserID:         4,
		IncludePrivate: false,
		IncludeLimited: true,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
}

func TestGetOrgsCanCreateRepoByUserID(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	orgs, err := organization.GetOrgsCanCreateRepoByUserID(db.DefaultContext, 2)
	require.NoError(t, err)
	assert.Len(t, orgs, 1)
	assert.EqualValues(t, 3, orgs[0].ID)
	orgs, err = organization.GetOrgsCanCreateRepoByUserID(db.DefaultContext, 1)
	require.NoError(t, err)
	assert.Len(t, orgs, 2)
	assert.EqualValues(t, 36, orgs[0].ID)
	assert.EqualValues(t, 35, orgs[1].ID)
}

func TestGetUserOrgsList(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	orgs, err := organization.GetUserOrgsList(db.DefaultContext, &user_model.User{ID: 4})
	require.NoError(t, err)
	if assert.Len(t, orgs, 1) {
		assert.EqualValues(t, 3, orgs[0].ID)
		// repo_id: 3 is in the team, 32 is public, 5 is private with no team
		assert.Equal(t, 2, orgs[0].NumRepos)
	}
}

func TestGetUserOrgsListSorting(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	orgs, err := organization.GetUserOrgsList(db.DefaultContext, &user_model.User{ID: 1})
	require.NoError(t, err)

	isSorted := slices.IsSortedFunc(orgs, func(a, b *organization.MinimalOrg) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})

	assert.True(t, isSorted)
}
