// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"strings"
	"testing"
	"time"

	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	"forgejo.org/modules/git"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepoCommitHeader(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	t.Run("Verify commit info", func(t *testing.T) {
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		gitRepo, err := git.OpenRepository(git.DefaultContext, repo.RepoPath())
		require.NoError(t, err)
		defer gitRepo.Close()

		commit, err := gitRepo.GetCommit("65f1bf27bc3bf70f64657658635e66094edbcb4d")
		require.NoError(t, err)

		req := NewRequest(t, "GET", "/user2/repo1/commit/65f1bf27bc3bf70f64657658635e66094edbcb4d")
		resp := session.MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)

		summary := htmlDoc.Find(".commit-header h3")
		assert.Equal(t, commit.Summary(), strings.TrimSpace(summary.Text()))

		author := htmlDoc.Find(".commit-header-row .author strong").First()
		assert.Equal(t, commit.Author.Name, author.Text())

		committer := htmlDoc.Find(".commit-header-row .author strong").Last()
		assert.Equal(t, commit.Committer.Name, committer.Text())

		date, _ := htmlDoc.Find(".commit-header-row #authored-time relative-time").Attr("datetime")
		assert.Equal(t, commit.Author.When.Format(time.RFC3339), date)

		sha := htmlDoc.Find(".commit-header-row .sha.label")
		assert.Equal(t, commit.ID.String()[:10], sha.Find(".shortsha").Text())
	})

	t.Run("Verify parent commit ID", func(t *testing.T) {
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
		gitRepo, err := git.OpenRepository(git.DefaultContext, repo.RepoPath())
		require.NoError(t, err)
		defer gitRepo.Close()

		commit, err := gitRepo.GetCommit("205ac761f3326a7ebe416e8673760016450b5cec")
		require.NoError(t, err)

		req := NewRequest(t, "GET", "/user2/repo2/commit/205ac761f3326a7ebe416e8673760016450b5cec")
		resp := session.MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)

		shas := htmlDoc.Find(".commit-header-row .sha.label")
		assert.Equal(t, 2, shas.Length())

		parentSha := shas.First()
		parentHref, _ := parentSha.Attr("href")
		assert.Equal(t, "/user2/repo2/commit/2c54faec6c45d31c1abfaecdab471eac6633738a", parentHref)

		parentID, err := commit.ParentID(0)
		require.NoError(t, err)
		assert.Equal(t, parentID.String()[:10], parentSha.Find(".shortsha").Text())

		sha := shas.Last()
		assert.Equal(t, commit.ID.String()[:10], sha.Find(".shortsha").Text())
	})
}
