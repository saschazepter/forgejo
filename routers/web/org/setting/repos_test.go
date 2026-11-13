// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package setting

import (
	"testing"

	"forgejo.org/models/organization"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/services/context"
	"forgejo.org/services/contexttest"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createOwner creates a user allowed to create organizations: the
// DefaultAllowCreateOrganization setting is copied into the user row at
// creation time, so it only needs to be mocked while the user is created.
func createOwner(t *testing.T) *user_model.User {
	t.Helper()
	defer test.MockVariableValue(&setting.Service.DefaultAllowCreateOrganization, true)()
	return forgery.CreateUser(t, nil)
}

// createOrgWithRepos creates an organization owning three repositories (one of
// them private) on behalf of a freshly created owner.
func createOrgWithRepos(t *testing.T) (*organization.Organization, *user_model.User) {
	t.Helper()
	owner := createOwner(t)
	org := forgery.CreateOrganisation(t, owner, nil)
	forgery.CreateRepository(t, org.AsUser(), &forgery.CreateRepositoryOptions{Name: "repo-a"})
	forgery.CreateRepository(t, org.AsUser(), &forgery.CreateRepositoryOptions{Name: "repo-b", IsPrivate: true})
	forgery.CreateRepository(t, org.AsUser(), &forgery.CreateRepositoryOptions{Name: "repo-c"})
	return org, owner
}

// runRepos executes the handler for the given query string and returns the
// resulting context so the caller can assert on ctx.Data.
func runRepos(t *testing.T, query string, org *organization.Organization, doer *user_model.User) *context.Context {
	t.Helper()
	ctx, _ := contexttest.MockContext(t, "/org/"+org.Name+"/settings/repos"+query)
	ctx.Doer = doer
	ctx.Org = &context.Organization{Organization: org}
	Repos(ctx)
	return ctx
}

func repoNames(repos repo_model.RepositoryList) []string {
	names := make([]string, len(repos))
	for i, r := range repos {
		names[i] = r.LowerName
	}
	return names
}

// The default sort is "recentupdate", and any unknown value must fall back to
// it rather than being passed through to the query layer.
func TestReposSortType(t *testing.T) {
	unittest.PrepareTestEnv(t)
	org, owner := createOrgWithRepos(t)

	cases := []struct{ query, expected string }{
		{"", "recentupdate"},
		{"?sort=not_a_valid_sort", "recentupdate"},
		{"?sort=alphabetically", "alphabetically"},
		{"?sort=gitsize", "gitsize"},
		{"?sort=lfssize", "lfssize"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			ctx := runRepos(t, tc.query, org, owner)
			assert.Equal(t, tc.expected, ctx.Data["SortType"])
		})
	}
}

// Sorting must actually order the returned repositories, for every sortable
// column of the page.
func TestReposSortOrdersResults(t *testing.T) {
	unittest.PrepareTestEnv(t)
	org, owner := createOrgWithRepos(t)

	sorted := func(query string) []string {
		return repoNames(runRepos(t, query, org, owner).Data["Repos"].(repo_model.RepositoryList))
	}

	// Sizes are all 0 on freshly created repositories: alphabetical ordering.
	assert.Equal(t, []string{"repo-a", "repo-b", "repo-c"}, sorted("?sort=alphabetically"))
	assert.Equal(t, []string{"repo-c", "repo-b", "repo-a"}, sorted("?sort=reversealphabetically"))

	// Give the repositories sizes whose ordering differs from the alphabetical
	// one, so a silent fallback to name ordering would fail the assertions.
	for name, sizes := range map[string][2]int64{
		"repo-a": {300, 10},
		"repo-b": {100, 30},
		"repo-c": {200, 20},
	} {
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerID: org.ID, LowerName: name})
		require.NoError(t, repo_model.UpdateRepoSize(t.Context(), repo.ID, sizes[0], sizes[1]))
	}

	assert.Equal(t, []string{"repo-b", "repo-c", "repo-a"}, sorted("?sort=gitsize"))
	assert.Equal(t, []string{"repo-a", "repo-c", "repo-b"}, sorted("?sort=reversegitsize"))
	assert.Equal(t, []string{"repo-a", "repo-c", "repo-b"}, sorted("?sort=lfssize"))
	assert.Equal(t, []string{"repo-b", "repo-c", "repo-a"}, sorted("?sort=reverselfssize"))
}

// Only repositories owned by the organization must be listed: private ones
// included, and repositories of other owners excluded.
func TestReposOnlyOrgRepos(t *testing.T) {
	unittest.PrepareTestEnv(t)
	org, owner := createOrgWithRepos(t)
	// A repository owned by the owner personally must not appear in the
	// organization list.
	forgery.CreateRepository(t, owner, &forgery.CreateRepositoryOptions{Name: "personal-repo"})

	repos := runRepos(t, "", org, owner).Data["Repos"].(repo_model.RepositoryList)
	require.Len(t, repos, 3)
	hasPrivate := false
	for _, r := range repos {
		assert.Equal(t, org.ID, r.OwnerID)
		hasPrivate = hasPrivate || r.IsPrivate
	}
	assert.True(t, hasPrivate, "the owner must see the organization private repositories")
}

// Total must reflect the full repository count across all pages, and the pages
// must be disjoint; page indexes <= 0 must clamp to the first page.
func TestReposPagination(t *testing.T) {
	unittest.PrepareTestEnv(t)
	defer test.MockVariableValue(&setting.UI.Admin.RepoPagingNum, 2)()
	org, owner := createOrgWithRepos(t)

	ctx := runRepos(t, "?sort=alphabetically&page=1", org, owner)
	page1 := ctx.Data["Repos"].(repo_model.RepositoryList)
	total := ctx.Data["Total"].(int64)
	assert.Equal(t, []string{"repo-a", "repo-b"}, repoNames(page1), "the page must be capped at the configured page size")
	assert.Equal(t, int64(3), total, "Total must count every repository, not just the current page")

	page2 := runRepos(t, "?sort=alphabetically&page=2", org, owner).Data["Repos"].(repo_model.RepositoryList)
	assert.Equal(t, []string{"repo-c"}, repoNames(page2), "page 2 must hold the remaining repository")

	// page=0 and page=-1 must behave like page 1.
	for _, q := range []string{"?sort=alphabetically&page=0", "?sort=alphabetically&page=-1"} {
		clamped := runRepos(t, q, org, owner).Data["Repos"].(repo_model.RepositoryList)
		assert.Equal(t, repoNames(page1), repoNames(clamped), "%s must clamp to page 1", q)
	}
}

// An organization that owns no repositories must still render successfully with
// an empty list (Total == 0), exercising the {{else}} branch of the template.
func TestReposNoRepos(t *testing.T) {
	unittest.PrepareTestEnv(t)
	owner := createOwner(t)
	org := forgery.CreateOrganisation(t, owner, nil)

	ctx := runRepos(t, "", org, owner)
	assert.Empty(t, ctx.Data["Repos"].(repo_model.RepositoryList))
	assert.Equal(t, int64(0), ctx.Data["Total"].(int64))
}
