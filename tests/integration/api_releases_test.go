// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	auth_model "forgejo.org/models/auth"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	api "forgejo.org/modules/structs"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIListReleases(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	token := getUserToken(t, user2.LowerName, auth_model.AccessTokenScopeReadRepository)

	link, _ := url.Parse(fmt.Sprintf("/api/v1/repos/%s/%s/releases", user2.Name, repo.Name))
	resp := MakeRequest(t, NewRequest(t, "GET", link.String()).AddTokenAuth(token), http.StatusOK)
	var apiReleases []*api.Release
	DecodeJSON(t, resp, &apiReleases)
	if assert.Len(t, apiReleases, 3) {
		for _, release := range apiReleases {
			switch release.ID {
			case 1:
				assert.False(t, release.IsDraft)
				assert.False(t, release.IsPrerelease)
				assert.True(t, strings.HasSuffix(release.UploadURL, "/api/v1/repos/user2/repo1/releases/1/assets"), release.UploadURL)
			case 4:
				assert.True(t, release.IsDraft)
				assert.False(t, release.IsPrerelease)
				assert.True(t, strings.HasSuffix(release.UploadURL, "/api/v1/repos/user2/repo1/releases/4/assets"), release.UploadURL)
			case 5:
				assert.False(t, release.IsDraft)
				assert.True(t, release.IsPrerelease)
				assert.True(t, strings.HasSuffix(release.UploadURL, "/api/v1/repos/user2/repo1/releases/5/assets"), release.UploadURL)
			default:
				require.NoError(t, fmt.Errorf("unexpected release: %v", release))
			}
		}
	}

	// test filter
	testFilterByLen := func(auth bool, query url.Values, expectedLength int, msgAndArgs ...string) {
		link.RawQuery = query.Encode()
		req := NewRequest(t, "GET", link.String())
		if auth {
			req.AddTokenAuth(token)
		}
		resp = MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &apiReleases)
		assert.Len(t, apiReleases, expectedLength, msgAndArgs)
	}

	testFilterByLen(false, url.Values{"draft": {"true"}}, 0, "anon should not see drafts")
	testFilterByLen(true, url.Values{"draft": {"true"}}, 1, "repo owner should see drafts")
	testFilterByLen(true, url.Values{"draft": {"false"}}, 2, "exclude drafts")
	testFilterByLen(true, url.Values{"draft": {"false"}, "pre-release": {"false"}}, 1, "exclude drafts and pre-releases")
	testFilterByLen(true, url.Values{"pre-release": {"true"}}, 1, "only get pre-release")
	testFilterByLen(true, url.Values{"draft": {"true"}, "pre-release": {"true"}}, 0, "there is no pre-release draft")
	testFilterByLen(true, url.Values{"q": {"release"}}, 3, "keyword")
}

func createNewReleaseUsingAPI(t *testing.T, token string, owner *user_model.User, repo *repo_model.Repository, name, target, title, desc string) *api.Release {
	urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/releases", owner.Name, repo.Name)
	req := NewRequestWithJSON(t, "POST", urlStr, &api.CreateReleaseOption{
		TagName:      name,
		Title:        title,
		Note:         desc,
		IsDraft:      false,
		IsPrerelease: false,
		Target:       target,
	}).AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusCreated)

	var newRelease api.Release
	DecodeJSON(t, resp, &newRelease)
	rel := &repo_model.Release{
		ID:      newRelease.ID,
		TagName: newRelease.TagName,
		Title:   newRelease.Title,
	}
	unittest.AssertExistsAndLoadBean(t, rel)
	assert.Equal(t, newRelease.Note, rel.Note)

	return &newRelease
}

func TestAPICreateAndUpdateRelease(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	session := loginUser(t, owner.LowerName)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

	gitRepo, err := gitrepo.OpenRepository(git.DefaultContext, repo)
	require.NoError(t, err)
	defer gitRepo.Close()

	err = gitRepo.CreateTag("v0.0.1", "master")
	require.NoError(t, err)

	target, err := gitRepo.GetTagCommitID("v0.0.1")
	require.NoError(t, err)

	newRelease := createNewReleaseUsingAPI(t, token, owner, repo, "v0.0.1", target, "v0.0.1", "test")

	urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/releases/%d", owner.Name, repo.Name, newRelease.ID)
	req := NewRequest(t, "GET", urlStr).
		AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)

	var release api.Release
	DecodeJSON(t, resp, &release)

	assert.Equal(t, newRelease.TagName, release.TagName)
	assert.Equal(t, newRelease.Title, release.Title)
	assert.Equal(t, newRelease.Note, release.Note)
	assert.False(t, newRelease.HideArchiveLinks)

	hideArchiveLinks := true

	req = NewRequestWithJSON(t, "PATCH", urlStr, &api.EditReleaseOption{
		TagName:          release.TagName,
		Title:            release.Title,
		Note:             "updated",
		IsDraft:          &release.IsDraft,
		IsPrerelease:     &release.IsPrerelease,
		Target:           release.Target,
		HideArchiveLinks: &hideArchiveLinks,
	}).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)

	DecodeJSON(t, resp, &newRelease)
	rel := &repo_model.Release{
		ID:      newRelease.ID,
		TagName: newRelease.TagName,
		Title:   newRelease.Title,
	}
	unittest.AssertExistsAndLoadBean(t, rel)
	assert.Equal(t, rel.Note, newRelease.Note)
	assert.True(t, newRelease.HideArchiveLinks)
}

func TestAPICreateProtectedTagRelease(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
	writer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	session := loginUser(t, writer.LowerName)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

	gitRepo, err := gitrepo.OpenRepository(git.DefaultContext, repo)
	require.NoError(t, err)
	defer gitRepo.Close()

	commit, err := gitRepo.GetBranchCommit("master")
	require.NoError(t, err)

	req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/%s/releases", repo.OwnerName, repo.Name), &api.CreateReleaseOption{
		TagName:      "v0.0.1",
		Title:        "v0.0.1",
		IsDraft:      false,
		IsPrerelease: false,
		Target:       commit.ID.String(),
	}).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusUnprocessableEntity)
}

func TestAPICreateReleaseToDefaultBranch(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	session := loginUser(t, owner.LowerName)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

	createNewReleaseUsingAPI(t, token, owner, repo, "v0.0.1", "", "v0.0.1", "test")
}

func TestAPICreateReleaseToDefaultBranchOnExistingTag(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	session := loginUser(t, owner.LowerName)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

	gitRepo, err := gitrepo.OpenRepository(git.DefaultContext, repo)
	require.NoError(t, err)
	defer gitRepo.Close()

	err = gitRepo.CreateTag("v0.0.1", "master")
	require.NoError(t, err)

	createNewReleaseUsingAPI(t, token, owner, repo, "v0.0.1", "", "v0.0.1", "test")
}

func TestAPICreateReleaseGivenInvalidTarget(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	session := loginUser(t, owner.LowerName)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

	urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/releases", owner.Name, repo.Name)
	req := NewRequestWithJSON(t, "POST", urlStr, &api.CreateReleaseOption{
		TagName: "i-point-to-an-invalid-target",
		Title:   "Invalid Target",
		Target:  "invalid-target",
	}).AddTokenAuth(token)

	MakeRequest(t, req, http.StatusNotFound)
}

func TestAPIGetLatestRelease(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	req := NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/%s/%s/releases/latest", owner.Name, repo.Name))
	resp := MakeRequest(t, req, http.StatusOK)

	var release *api.Release
	DecodeJSON(t, resp, &release)

	assert.Equal(t, "testing-release", release.Title)
}

func TestAPIGetReleaseByTag(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	tag := "v1.1"

	req := NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/%s/%s/releases/tags/%s", owner.Name, repo.Name, tag))
	resp := MakeRequest(t, req, http.StatusOK)

	var release *api.Release
	DecodeJSON(t, resp, &release)

	assert.Equal(t, "testing-release", release.Title)

	nonexistingtag := "nonexistingtag"

	req = NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/%s/%s/releases/tags/%s", owner.Name, repo.Name, nonexistingtag))
	resp = MakeRequest(t, req, http.StatusNotFound)

	var err *api.APIError
	DecodeJSON(t, resp, &err)
	assert.NotEmpty(t, err.Message)
}

func TestAPIDeleteReleaseByTagName(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	session := loginUser(t, owner.LowerName)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

	createNewReleaseUsingAPI(t, token, owner, repo, "release-tag", "", "Release Tag", "test")

	// delete release
	req := NewRequestf(t, http.MethodDelete, "/api/v1/repos/%s/%s/releases/tags/release-tag", owner.Name, repo.Name).
		AddTokenAuth(token)
	_ = MakeRequest(t, req, http.StatusNoContent)

	// make sure release is deleted
	req = NewRequestf(t, http.MethodDelete, "/api/v1/repos/%s/%s/releases/tags/release-tag", owner.Name, repo.Name).
		AddTokenAuth(token)
	_ = MakeRequest(t, req, http.StatusNotFound)

	// delete release tag too
	req = NewRequestf(t, http.MethodDelete, "/api/v1/repos/%s/%s/tags/release-tag", owner.Name, repo.Name).
		AddTokenAuth(token)
	_ = MakeRequest(t, req, http.StatusNoContent)
}

func TestAPIUploadAssetRelease(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	session := loginUser(t, owner.LowerName)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

	r := createNewReleaseUsingAPI(t, token, owner, repo, "release-tag", "", "Release Tag", "test")

	filename := "image.png"
	buff := generateImg()

	assetURL := fmt.Sprintf("/api/v1/repos/%s/%s/releases/%d/assets", owner.Name, repo.Name, r.ID)

	t.Run("multipart/form-data", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		body := &bytes.Buffer{}

		writer := multipart.NewWriter(body)
		part, err := writer.CreateFormFile("attachment", filename)
		require.NoError(t, err)
		_, err = io.Copy(part, bytes.NewReader(buff.Bytes()))
		require.NoError(t, err)
		err = writer.Close()
		require.NoError(t, err)

		req := NewRequestWithBody(t, http.MethodPost, assetURL, bytes.NewReader(body.Bytes())).
			AddTokenAuth(token).
			SetHeader("Content-Type", writer.FormDataContentType())
		resp := MakeRequest(t, req, http.StatusCreated)

		var attachment *api.Attachment
		DecodeJSON(t, resp, &attachment)

		assert.Equal(t, filename, attachment.Name)
		assert.EqualValues(t, 104, attachment.Size)

		req = NewRequestWithBody(t, http.MethodPost, assetURL+"?name=test-asset", bytes.NewReader(body.Bytes())).
			AddTokenAuth(token).
			SetHeader("Content-Type", writer.FormDataContentType())
		resp = MakeRequest(t, req, http.StatusCreated)

		var attachment2 *api.Attachment
		DecodeJSON(t, resp, &attachment2)

		assert.Equal(t, "test-asset", attachment2.Name)
		assert.EqualValues(t, 104, attachment2.Size)
	})

	t.Run("application/octet-stream", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequestWithBody(t, http.MethodPost, assetURL, bytes.NewReader(buff.Bytes())).
			AddTokenAuth(token)
		MakeRequest(t, req, http.StatusBadRequest)

		req = NewRequestWithBody(t, http.MethodPost, assetURL+"?name=stream.bin", bytes.NewReader(buff.Bytes())).
			AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusCreated)

		var attachment *api.Attachment
		DecodeJSON(t, resp, &attachment)

		assert.Equal(t, "stream.bin", attachment.Name)
		assert.EqualValues(t, 104, attachment.Size)
		assert.Equal(t, "attachment", attachment.Type)
	})
}

func TestAPIGetReleaseArchiveDownloadCount(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	session := loginUser(t, owner.LowerName)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

	name := "ReleaseDownloadCount"

	createNewReleaseUsingAPI(t, token, owner, repo, name, "", name, "test")

	urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/releases/tags/%s", owner.Name, repo.Name, name)

	req := NewRequest(t, "GET", urlStr)
	resp := MakeRequest(t, req, http.StatusOK)

	var release *api.Release
	DecodeJSON(t, resp, &release)

	// Check if everything defaults to 0
	assert.Equal(t, int64(0), release.ArchiveDownloadCount.TarGz)
	assert.Equal(t, int64(0), release.ArchiveDownloadCount.Zip)

	// Download the tarball to increase the count
	MakeRequest(t, NewRequest(t, "GET", release.TarURL), http.StatusOK)

	// Check if the count has increased
	resp = MakeRequest(t, req, http.StatusOK)

	DecodeJSON(t, resp, &release)

	assert.Equal(t, int64(1), release.ArchiveDownloadCount.TarGz)
	assert.Equal(t, int64(0), release.ArchiveDownloadCount.Zip)
}

func TestAPIExternalAssetRelease(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	session := loginUser(t, owner.LowerName)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

	r := createNewReleaseUsingAPI(t, token, owner, repo, "release-tag", "", "Release Tag", "test")

	req := NewRequest(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/releases/%d/assets?name=test-asset&external_url=https%%3A%%2F%%2Fforgejo.org%%2F", owner.Name, repo.Name, r.ID)).
		AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusCreated)

	var attachment *api.Attachment
	DecodeJSON(t, resp, &attachment)

	assert.Equal(t, "test-asset", attachment.Name)
	assert.EqualValues(t, 0, attachment.Size)
	assert.Equal(t, "https://forgejo.org/", attachment.DownloadURL)
	assert.Equal(t, "external", attachment.Type)
}

func TestAPIAllowedAPIURLInRelease(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	session := loginUser(t, owner.LowerName)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

	r := createNewReleaseUsingAPI(t, token, owner, repo, "release-tag", "", "Release Tag", "test")
	internalURL := "https://localhost:3003/api/packages/owner/generic/test/1.0.0/test.txt"

	req := NewRequest(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/releases/%d/assets?name=test-asset&external_url=%s", owner.Name, repo.Name, r.ID, url.QueryEscape(internalURL))).
		AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusCreated)

	var attachment *api.Attachment
	DecodeJSON(t, resp, &attachment)

	assert.Equal(t, "test-asset", attachment.Name)
	assert.EqualValues(t, 0, attachment.Size)
	assert.Equal(t, internalURL, attachment.DownloadURL)
	assert.Equal(t, "external", attachment.Type)
}

func TestAPIDuplicateAssetRelease(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	session := loginUser(t, owner.LowerName)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

	r := createNewReleaseUsingAPI(t, token, owner, repo, "release-tag", "", "Release Tag", "test")

	filename := "image.png"
	buff := generateImg()
	body := &bytes.Buffer{}

	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("attachment", filename)
	require.NoError(t, err)
	_, err = io.Copy(part, &buff)
	require.NoError(t, err)
	err = writer.Close()
	require.NoError(t, err)

	req := NewRequestWithBody(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/releases/%d/assets?name=test-asset&external_url=https%%3A%%2F%%2Fforgejo.org%%2F", owner.Name, repo.Name, r.ID), body).
		AddTokenAuth(token)
	req.Header.Add("Content-Type", writer.FormDataContentType())
	MakeRequest(t, req, http.StatusBadRequest)
}

func TestAPIMissingAssetRelease(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	session := loginUser(t, owner.LowerName)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

	r := createNewReleaseUsingAPI(t, token, owner, repo, "release-tag", "", "Release Tag", "test")

	req := NewRequest(t, http.MethodPost, fmt.Sprintf("/api/v1/repos/%s/%s/releases/%d/assets?name=test-asset", owner.Name, repo.Name, r.ID)).
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusBadRequest)
}
