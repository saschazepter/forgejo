// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"net/http"
	"strings"
	"testing"

	repo_model "forgejo.org/models/repo"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOrgSettingsRepos verifies the end-to-end rendering of the organization
// settings repositories page: a sortable table listing the organization's
// repositories with their git and LFS size columns and their state labels,
// reachable only by owners.
func TestOrgSettingsRepos(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	owner := forgery.CreateUser(t, nil)
	org := forgery.CreateOrganisation(t, owner, nil)

	normal := forgery.CreateRepository(t, org.AsUser(), &forgery.CreateRepositoryOptions{Name: "repo-normal"})
	private := forgery.CreateRepository(t, org.AsUser(), &forgery.CreateRepositoryOptions{Name: "repo-private", IsPrivate: true})
	template := forgery.CreateRepository(t, org.AsUser(), &forgery.CreateRepositoryOptions{Name: "repo-template", IsTemplate: true})
	archived := forgery.CreateRepository(t, org.AsUser(), &forgery.CreateRepositoryOptions{Name: "repo-archived"})
	require.NoError(t, repo_model.SetArchiveRepoState(t.Context(), archived, true))

	session := loginUser(t, owner.Name)

	req := NewRequest(t, "GET", "/org/"+org.Name+"/settings/repos")
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	// The page must be linked from the settings navigation.
	assert.Positive(t, htmlDoc.doc.Find(".flex-container-nav a[href='/org/"+org.Name+"/settings/repos']").Length(),
		"the settings navigation must link the repositories page")

	// The table headers (name / size / lfs size) must be present.
	assert.Equal(t, 3, htmlDoc.doc.Find("table thead tr th").Length())

	// Each repository row must carry the name and the two size columns, plus
	// the label matching its state.
	cases := []struct {
		repo  *repo_model.Repository
		label string
	}{
		{normal, ""},
		{private, "Private"},
		{template, "Template"},
		{archived, "Archived"},
	}
	for _, tc := range cases {
		t.Run(tc.repo.Name, func(t *testing.T) {
			row := htmlDoc.doc.Find("table tbody tr td a[href='/" + org.Name + "/" + tc.repo.Name + "']").Closest("tr")
			require.Equal(t, 1, row.Length(), "%s must be listed", tc.repo.Name)
			assert.Equal(t, 3, row.Find("td").Length())

			labels := row.Find("td span.ui.basic.label")
			if tc.label == "" {
				assert.Equal(t, 0, labels.Length(), "%s must not carry any state label", tc.repo.Name)
			} else {
				require.Equal(t, 1, labels.Length(), "%s must carry exactly one state label", tc.repo.Name)
				assert.Equal(t, tc.label, strings.TrimSpace(labels.Text()))
			}
		})
	}

	// A user that is not an owner of the organization must be denied.
	stranger := forgery.CreateUser(t, nil)
	strangerSession := loginUser(t, stranger.Name)
	strangerSession.MakeRequest(t, NewRequest(t, "GET", "/org/"+org.Name+"/settings/repos"), http.StatusNotFound)
}

// TestOrgSettingsReposEmptyState verifies that an organization without any
// repository renders the empty-state message instead of a list of rows.
func TestOrgSettingsReposEmptyState(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	owner := forgery.CreateUser(t, nil)
	org := forgery.CreateOrganisation(t, owner, nil)

	session := loginUser(t, owner.Name)

	req := NewRequest(t, "GET", "/org/"+org.Name+"/settings/repos")
	resp := session.MakeRequest(t, req, http.StatusOK)

	assert.Contains(t, resp.Body.String(), "You do not own any repositories.")
}
