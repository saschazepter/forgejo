// Copyright 2021 The Gitea Authors. All rights reserved.
// Copyright 2024 The Forgejo Authors c/o Codeberg e.V.. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"forgejo.org/models"
	auth_model "forgejo.org/models/auth"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	repo_module "forgejo.org/modules/repository"
	api "forgejo.org/modules/structs"
	"forgejo.org/services/release"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTagViewWithoutRelease(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	session := loginUser(t, owner.Name)

	err := release.CreateNewTag(git.DefaultContext, owner, repo, "master", "no-release", "release-less tag")
	require.NoError(t, err)

	// Test that the page loads
	req := NewRequestf(t, "GET", "/%s/releases/tag/no-release", repo.FullName())
	resp := session.MakeRequest(t, req, http.StatusOK)

	// Test that the tags sub-menu is active and has a counter
	htmlDoc := NewHTMLParser(t, resp.Body)
	tagsTab := htmlDoc.Find(".small-menu-items .active.item[href$='/tags']")
	assert.Contains(t, tagsTab.Text(), "4 tags")

	// Test that the release sub-menu isn't active
	releaseLink := htmlDoc.Find(".small-menu-items .item[href$='/releases']")
	assert.False(t, releaseLink.HasClass("active"))

	// Test that the title is displayed
	releaseTitle := strings.TrimSpace(htmlDoc.Find("h4.release-list-title > a").Text())
	assert.Equal(t, "no-release", releaseTitle)

	// Test that there is no "Stable" link
	htmlDoc.AssertElement(t, "h4.release-list-title > span.ui.green.label", false)

	// Ensure that there is no "Edit" button
	htmlDoc.AssertElement(t, ".detail a.muted > svg.octicon-pencil", false)

	// Test that the correct user is linked
	ownerLinkHref, _ := htmlDoc.Find("a.author").Attr("href")
	assert.Equal(t, "/user2", ownerLinkHref)

	t.Run("Ghost owner", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		ghost := user_model.NewGhostUser()
		err = release.CreateNewTag(git.DefaultContext, ghost, repo, "master", "ghost-tag", "a spooky tag")
		require.NoError(t, err)

		req := NewRequestf(t, "GET", "/%s/releases/tag/ghost-tag", repo.FullName())
		resp := session.MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)

		// Test that the Ghost user does not link anywhere
		ownerLink := htmlDoc.Find("a.author")
		_, ok := ownerLink.Attr("href")
		assert.Equal(t, 1, ownerLink.Length())
		assert.False(t, ok)
		assert.Equal(t, "Ghost", ownerLink.Text())
	})
}

func TestCreateNewTagProtected(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, u *url.URL) {
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

		t.Run("Code", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			err := release.CreateNewTag(git.DefaultContext, owner, repo, "master", "t-first", "first tag")
			require.NoError(t, err)

			err = release.CreateNewTag(git.DefaultContext, owner, repo, "master", "v-2", "second tag")
			require.Error(t, err)
			assert.True(t, models.IsErrProtectedTagName(err))

			err = release.CreateNewTag(git.DefaultContext, owner, repo, "master", "v-1.1", "third tag")
			require.NoError(t, err)
		})

		t.Run("Git", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			httpContext := NewAPITestContext(t, owner.Name, repo.Name)

			dstPath := t.TempDir()

			u.Path = httpContext.GitPath()
			u.User = url.UserPassword(owner.Name, userPassword)

			doGitClone(dstPath, u)(t)

			_, _, err := git.NewCommand(git.DefaultContext, "tag", "v-2").RunStdString(&git.RunOpts{Dir: dstPath})
			require.NoError(t, err)

			_, _, err = git.NewCommand(git.DefaultContext, "push", "--tags").RunStdString(&git.RunOpts{Dir: dstPath})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "Tag v-2 is protected")
		})

		t.Run("GitTagForce", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			httpContext := NewAPITestContext(t, owner.Name, repo.Name)

			dstPath := t.TempDir()

			u.Path = httpContext.GitPath()
			u.User = url.UserPassword(owner.Name, userPassword)

			doGitClone(dstPath, u)(t)

			_, _, err := git.NewCommand(git.DefaultContext, "tag", "v-1.1", "-m", "force update v2", "--force").RunStdString(&git.RunOpts{Dir: dstPath})
			require.NoError(t, err)

			_, _, err = git.NewCommand(git.DefaultContext, "push", "--tags").RunStdString(&git.RunOpts{Dir: dstPath})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "the tag already exists in the remote")

			_, _, err = git.NewCommand(git.DefaultContext, "push", "--tags", "--force").RunStdString(&git.RunOpts{Dir: dstPath})
			require.NoError(t, err)
			req := NewRequestf(t, "GET", "/%s/releases/tag/v-1.1", repo.FullName())
			resp := MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)
			tagsTab := htmlDoc.Find(".release-list-title")
			assert.Contains(t, tagsTab.Text(), "force update v2")
		})
	})
}

func TestSyncRepoTags(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, u *url.URL) {
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

		t.Run("Git", func(t *testing.T) {
			httpContext := NewAPITestContext(t, owner.Name, repo.Name)

			dstPath := t.TempDir()

			u.Path = httpContext.GitPath()
			u.User = url.UserPassword(owner.Name, userPassword)

			doGitClone(dstPath, u)(t)

			_, _, err := git.NewCommand(git.DefaultContext, "tag", "v2", "-m", "this is an annotated tag").RunStdString(&git.RunOpts{Dir: dstPath})
			require.NoError(t, err)

			_, _, err = git.NewCommand(git.DefaultContext, "push", "--tags").RunStdString(&git.RunOpts{Dir: dstPath})
			require.NoError(t, err)

			testTag := func(t *testing.T) {
				t.Helper()
				req := NewRequestf(t, "GET", "/%s/releases/tag/v2", repo.FullName())
				resp := MakeRequest(t, req, http.StatusOK)
				htmlDoc := NewHTMLParser(t, resp.Body)
				tagsTab := htmlDoc.Find(".release-list-title")
				assert.Contains(t, tagsTab.Text(), "this is an annotated tag")
			}

			// Make sure `SyncRepoTags` doesn't modify annotated tags.
			testTag(t)
			require.NoError(t, repo_module.SyncRepoTags(git.DefaultContext, repo.ID))
			testTag(t)
		})
	})
}

func TestRepushTag(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, u *url.URL) {
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
		session := loginUser(t, owner.LowerName)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

		httpContext := NewAPITestContext(t, owner.Name, repo.Name)

		dstPath := t.TempDir()

		u.Path = httpContext.GitPath()
		u.User = url.UserPassword(owner.Name, userPassword)

		doGitClone(dstPath, u)(t)

		// create and push a tag
		_, _, err := git.NewCommand(git.DefaultContext, "tag", "v2.0").RunStdString(&git.RunOpts{Dir: dstPath})
		require.NoError(t, err)
		_, _, err = git.NewCommand(git.DefaultContext, "push", "origin", "--tags", "v2.0").RunStdString(&git.RunOpts{Dir: dstPath})
		require.NoError(t, err)
		// create a release for the tag
		createdRelease := createNewReleaseUsingAPI(t, token, owner, repo, "v2.0", "", "Release of v2.0", "desc")
		assert.False(t, createdRelease.IsDraft)
		// delete the tag
		_, _, err = git.NewCommand(git.DefaultContext, "push", "origin", "--delete", "v2.0").RunStdString(&git.RunOpts{Dir: dstPath})
		require.NoError(t, err)
		// query the release by API and it should be a draft
		req := NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/%s/%s/releases/tags/%s", owner.Name, repo.Name, "v2.0"))
		resp := MakeRequest(t, req, http.StatusOK)
		var respRelease *api.Release
		DecodeJSON(t, resp, &respRelease)
		assert.True(t, respRelease.IsDraft)
		// re-push the tag
		_, _, err = git.NewCommand(git.DefaultContext, "push", "origin", "--tags", "v2.0").RunStdString(&git.RunOpts{Dir: dstPath})
		require.NoError(t, err)
		// query the release by API and it should not be a draft
		req = NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/%s/%s/releases/tags/%s", owner.Name, repo.Name, "v2.0"))
		resp = MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &respRelease)
		assert.False(t, respRelease.IsDraft)
	})
}
