// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"testing"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	git_model "forgejo.org/models/git"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/json"
	"forgejo.org/modules/lfs"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPILFSNotStarted(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	defer test.MockVariableValue(&setting.LFS.StartServer, false)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	req := NewRequestf(t, "POST", "/%s/%s.git/info/lfs/objects/batch", user.Name, repo.Name)
	MakeRequest(t, req, http.StatusNotFound)
	req = NewRequestf(t, "PUT", "/%s/%s.git/info/lfs/objects/oid/10", user.Name, repo.Name)
	MakeRequest(t, req, http.StatusNotFound)
	req = NewRequestf(t, "GET", "/%s/%s.git/info/lfs/objects/oid/name", user.Name, repo.Name)
	MakeRequest(t, req, http.StatusNotFound)
	req = NewRequestf(t, "GET", "/%s/%s.git/info/lfs/objects/oid", user.Name, repo.Name)
	MakeRequest(t, req, http.StatusNotFound)
	req = NewRequestf(t, "POST", "/%s/%s.git/info/lfs/verify", user.Name, repo.Name)
	MakeRequest(t, req, http.StatusNotFound)
}

func TestAPILFSMediaType(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	defer test.MockVariableValue(&setting.LFS.StartServer, true)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	req := NewRequestf(t, "POST", "/%s/%s.git/info/lfs/objects/batch", user.Name, repo.Name)
	resp := MakeRequest(t, req, http.StatusUnsupportedMediaType)
	assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
	req = NewRequestf(t, "POST", "/%s/%s.git/info/lfs/verify", user.Name, repo.Name)
	resp = MakeRequest(t, req, http.StatusUnsupportedMediaType)
	assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
}

func createLFSTestRepository(t *testing.T, name string) *repo_model.Repository {
	ctx := NewAPITestContext(t, "user2", "lfs-"+name+"-repo", auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)
	t.Run("CreateRepo", doAPICreateRepository(ctx, nil, git.Sha1ObjectFormat)) // FIXME: use forEachObjectFormat

	repo, err := repo_model.GetRepositoryByOwnerAndName(db.DefaultContext, "user2", "lfs-"+name+"-repo")
	require.NoError(t, err)

	return repo
}

func TestAPILFSBatch(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	defer test.MockVariableValue(&setting.LFS.StartServer, true)()

	repo := createLFSTestRepository(t, "batch")

	content := []byte("dummy1")
	oid := storeObjectInRepo(t, repo.ID, &content)
	defer git_model.RemoveLFSMetaObjectByOid(db.DefaultContext, repo.ID, oid)

	newRequest := func(t testing.TB, br *lfs.BatchRequest) *RequestWrapper {
		return NewRequestWithJSON(t, "POST", "/user2/lfs-batch-repo.git/info/lfs/objects/batch", br).
			SetHeader("Accept", lfs.AcceptHeader).
			SetHeader("Content-Type", lfs.MediaType)
	}
	decodeResponse := func(t *testing.T, b *bytes.Buffer) *lfs.BatchResponse {
		var br lfs.BatchResponse

		require.NoError(t, json.Unmarshal(b.Bytes(), &br))
		return &br
	}

	t.Run("InvalidJsonRequest", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := newRequest(t, nil)
		req.AddBasicAuth("user2")

		resp := MakeRequest(t, req, http.StatusBadRequest)
		assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
	})

	t.Run("InvalidOperation", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := newRequest(t, &lfs.BatchRequest{
			Operation: "dummy",
		})
		req.AddBasicAuth("user2")

		resp := MakeRequest(t, req, http.StatusBadRequest)
		assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
	})

	t.Run("InvalidPointer", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := newRequest(t, &lfs.BatchRequest{
			Operation: "download",
			Objects: []lfs.Pointer{
				{Oid: "dummy"},
				{Oid: oid, Size: -1},
			},
		})
		req.AddBasicAuth("user2")

		resp := MakeRequest(t, req, http.StatusOK)
		assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
		br := decodeResponse(t, resp.Body)
		assert.Len(t, br.Objects, 2)
		assert.Equal(t, "dummy", br.Objects[0].Oid)
		assert.Equal(t, oid, br.Objects[1].Oid)
		assert.Equal(t, int64(0), br.Objects[0].Size)
		assert.Equal(t, int64(-1), br.Objects[1].Size)
		assert.NotNil(t, br.Objects[0].Error)
		assert.NotNil(t, br.Objects[1].Error)
		assert.Equal(t, http.StatusUnprocessableEntity, br.Objects[0].Error.Code)
		assert.Equal(t, http.StatusUnprocessableEntity, br.Objects[1].Error.Code)
		assert.Contains(t, br.Objects[0].Error.Message, "Oid or size are invalid")
		assert.Contains(t, br.Objects[1].Error.Message, "Oid or size are invalid")
	})

	t.Run("PointerSizeMismatch", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := newRequest(t, &lfs.BatchRequest{
			Operation: "download",
			Objects: []lfs.Pointer{
				{Oid: oid, Size: 1},
			},
		})
		req.AddBasicAuth("user2")

		resp := MakeRequest(t, req, http.StatusOK)
		assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
		br := decodeResponse(t, resp.Body)
		assert.Len(t, br.Objects, 1)
		assert.NotNil(t, br.Objects[0].Error)
		assert.Equal(t, http.StatusUnprocessableEntity, br.Objects[0].Error.Code)
		assert.Equal(t, "Object "+oid+" is not 1 bytes", br.Objects[0].Error.Message)
	})

	t.Run("Download", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		t.Run("PointerNotInStore", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := newRequest(t, &lfs.BatchRequest{
				Operation: "download",
				Objects: []lfs.Pointer{
					{Oid: "fb8f7d8435968c4f82a726a92395be4d16f2f63116caf36c8ad35c60831ab042", Size: 6},
				},
			})
			req.AddBasicAuth("user2")

			resp := MakeRequest(t, req, http.StatusOK)
			assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
			br := decodeResponse(t, resp.Body)
			assert.Len(t, br.Objects, 1)
			assert.NotNil(t, br.Objects[0].Error)
			assert.Equal(t, http.StatusNotFound, br.Objects[0].Error.Code)
		})

		t.Run("MetaNotFound", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			p := lfs.Pointer{Oid: "05eeb4eb5be71f2dd291ca39157d6d9effd7d1ea19cbdc8a99411fe2a8f26a00", Size: 6}

			contentStore := lfs.NewContentStore()
			exist, err := contentStore.Exists(p)
			require.NoError(t, err)
			assert.False(t, exist)
			err = contentStore.Put(p, bytes.NewReader([]byte("dummy0")))
			require.NoError(t, err)

			req := newRequest(t, &lfs.BatchRequest{
				Operation: "download",
				Objects:   []lfs.Pointer{p},
			})
			req.AddBasicAuth("user2")

			resp := MakeRequest(t, req, http.StatusOK)
			assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
			br := decodeResponse(t, resp.Body)
			assert.Len(t, br.Objects, 1)
			assert.NotNil(t, br.Objects[0].Error)
			assert.Equal(t, http.StatusNotFound, br.Objects[0].Error.Code)
		})

		t.Run("Unauthorized", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := newRequest(t, &lfs.BatchRequest{
				Operation: "download",
				Objects: []lfs.Pointer{
					{Oid: oid, Size: 6},
				},
			})

			resp := MakeRequest(t, req, http.StatusUnauthorized)
			assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
			assert.Equal(t, "Basic realm=forgejo-lfs", resp.Header().Get("LFS-Authenticate"))
		})

		t.Run("Success", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := newRequest(t, &lfs.BatchRequest{
				Operation: "download",
				Objects: []lfs.Pointer{
					{Oid: oid, Size: 6},
				},
			})
			req.AddBasicAuth("user2")

			resp := MakeRequest(t, req, http.StatusOK)
			assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
			br := decodeResponse(t, resp.Body)
			assert.Len(t, br.Objects, 1)
			assert.Nil(t, br.Objects[0].Error)
			assert.Contains(t, br.Objects[0].Actions, "download")
			l := br.Objects[0].Actions["download"]
			assert.NotNil(t, l)
			assert.NotEmpty(t, l.Href)
		})
	})

	t.Run("Upload", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		t.Run("FileTooBig", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			defer test.MockVariableValue(&setting.LFS.MaxFileSize, 2)()

			req := newRequest(t, &lfs.BatchRequest{
				Operation: "upload",
				Objects: []lfs.Pointer{
					{Oid: "fb8f7d8435968c4f82a726a92395be4d16f2f63116caf36c8ad35c60831ab042", Size: 6},
				},
			})
			req.AddBasicAuth("user2")

			resp := MakeRequest(t, req, http.StatusOK)
			assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
			br := decodeResponse(t, resp.Body)
			assert.Len(t, br.Objects, 1)
			assert.NotNil(t, br.Objects[0].Error)
			assert.Equal(t, http.StatusUnprocessableEntity, br.Objects[0].Error.Code)
			assert.Equal(t, "Size must be less than or equal to 2", br.Objects[0].Error.Message)
		})

		t.Run("AddMeta", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			p := lfs.Pointer{Oid: "05eeb4eb5be71f2dd291ca39157d6d9effd7d1ea19cbdc8a99411fe2a8f26a00", Size: 6}

			contentStore := lfs.NewContentStore()
			exist, err := contentStore.Exists(p)
			require.NoError(t, err)
			assert.True(t, exist)

			repo2 := createLFSTestRepository(t, "batch2")
			content := []byte("dummy0")
			storeObjectInRepo(t, repo2.ID, &content)

			meta, err := git_model.GetLFSMetaObjectByOid(db.DefaultContext, repo.ID, p.Oid)
			assert.Nil(t, meta)
			assert.Equal(t, git_model.ErrLFSObjectNotExist, err)

			req := newRequest(t, &lfs.BatchRequest{
				Operation: "upload",
				Objects:   []lfs.Pointer{p},
			})
			req.AddBasicAuth("user2")

			resp := MakeRequest(t, req, http.StatusOK)
			assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
			br := decodeResponse(t, resp.Body)
			assert.Len(t, br.Objects, 1)
			assert.Nil(t, br.Objects[0].Error)
			assert.Empty(t, br.Objects[0].Actions)

			meta, err = git_model.GetLFSMetaObjectByOid(db.DefaultContext, repo.ID, p.Oid)
			require.NoError(t, err)
			assert.NotNil(t, meta)

			// Cleanup
			err = contentStore.Delete(p.RelativePath())
			require.NoError(t, err)
		})

		t.Run("AlreadyExists", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := newRequest(t, &lfs.BatchRequest{
				Operation: "upload",
				Objects: []lfs.Pointer{
					{Oid: oid, Size: 6},
				},
			})
			req.AddBasicAuth("user2")

			resp := MakeRequest(t, req, http.StatusOK)
			assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
			br := decodeResponse(t, resp.Body)
			assert.Len(t, br.Objects, 1)
			assert.Nil(t, br.Objects[0].Error)
			assert.Empty(t, br.Objects[0].Actions)
		})

		t.Run("NewFile", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := newRequest(t, &lfs.BatchRequest{
				Operation: "upload",
				Objects: []lfs.Pointer{
					{Oid: "d6f175817f886ec6fbbc1515326465fa96c3bfd54a4ea06cfd6dbbd8340e0153", Size: 1},
				},
			})
			req.AddBasicAuth("user2")

			resp := MakeRequest(t, req, http.StatusOK)
			assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
			br := decodeResponse(t, resp.Body)
			assert.Len(t, br.Objects, 1)
			assert.Nil(t, br.Objects[0].Error)
			assert.Contains(t, br.Objects[0].Actions, "upload")
			ul := br.Objects[0].Actions["upload"]
			assert.NotNil(t, ul)
			assert.NotEmpty(t, ul.Href)
			assert.Contains(t, br.Objects[0].Actions, "verify")
			vl := br.Objects[0].Actions["verify"]
			assert.NotNil(t, vl)
			assert.NotEmpty(t, vl.Href)
		})

		t.Run("InvalidOID", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			defer test.MockVariableValue(&setting.LFS.MaxFileSize, 2)()

			req := newRequest(t, &lfs.BatchRequest{
				Operation: "upload",
				Objects: []lfs.Pointer{
					{Oid: "dummy", Size: 6},
				},
			})
			req.AddBasicAuth("user2")

			resp := MakeRequest(t, req, http.StatusOK)
			assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
			br := decodeResponse(t, resp.Body)
			assert.Len(t, br.Objects, 1)
			assert.NotNil(t, br.Objects[0].Error)
			assert.Equal(t, http.StatusUnprocessableEntity, br.Objects[0].Error.Code)
			assert.Equal(t, "Oid or size are invalid (oid: dummy, size: 6)", br.Objects[0].Error.Message)
		})
	})
}

func TestAPILFSUpload(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	defer test.MockVariableValue(&setting.LFS.StartServer, true)()

	repo := createLFSTestRepository(t, "upload")

	content := []byte("dummy3")
	oid := storeObjectInRepo(t, repo.ID, &content)
	defer git_model.RemoveLFSMetaObjectByOid(db.DefaultContext, repo.ID, oid)

	newRequest := func(t testing.TB, p lfs.Pointer, content string) *RequestWrapper {
		return NewRequestWithBody(t, "PUT", path.Join("/user2/lfs-upload-repo.git/info/lfs/objects/", p.Oid, strconv.FormatInt(p.Size, 10)), strings.NewReader(content))
	}

	var lfsErrorResponse lfs.ErrorResponse

	t.Run("InvalidPointer", func(t *testing.T) {
		t.Run("InvalidOID", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := newRequest(t, lfs.Pointer{Oid: "dummy"}, "")
			req.AddBasicAuth("user2")

			resp := MakeRequest(t, req, http.StatusUnprocessableEntity)
			assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
			expected, err := json.Marshal(lfs.ErrorResponse{
				Message:          "Unprocessable Entity: Oid or size are invalid",
				DocumentationURL: "https://codeberg.org/forgejo/forgejo/issues",
			})
			require.NoError(t, err)
			assert.JSONEq(t, string(expected), resp.Body.String())
		})

		t.Run("NegativeSize", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := newRequest(t, lfs.Pointer{
				Oid:  "83de2e488b89a0aa1c97496b888120a28b0c1e15463a4adb8405578c540f36d4",
				Size: -1,
			}, "")
			req.AddBasicAuth("user2")

			resp := MakeRequest(t, req, http.StatusUnprocessableEntity)
			assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
			expected, err := json.Marshal(lfs.ErrorResponse{
				Message:          "Unprocessable Entity: Oid or size are invalid",
				DocumentationURL: "https://codeberg.org/forgejo/forgejo/issues",
			})
			require.NoError(t, err)
			assert.JSONEq(t, string(expected), resp.Body.String())
		})
	})

	t.Run("AlreadyExistsInStore", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		p := lfs.Pointer{Oid: "83de2e488b89a0aa1c97496b888120a28b0c1e15463a4adb8405578c540f36d4", Size: 6}

		contentStore := lfs.NewContentStore()
		exist, err := contentStore.Exists(p)
		require.NoError(t, err)
		assert.False(t, exist)
		err = contentStore.Put(p, bytes.NewReader([]byte("dummy5")))
		require.NoError(t, err)

		meta, err := git_model.GetLFSMetaObjectByOid(db.DefaultContext, repo.ID, p.Oid)
		assert.Nil(t, meta)
		assert.Equal(t, git_model.ErrLFSObjectNotExist, err)

		t.Run("InvalidAccess", func(t *testing.T) {
			req := newRequest(t, p, "invalid")
			req.AddBasicAuth("user2")

			resp := MakeRequest(t, req, http.StatusUnprocessableEntity)
			assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
			DecodeJSON(t, resp, &lfsErrorResponse)
			assert.Equal(t, "Unprocessable Entity: content size does not match", lfsErrorResponse.Message)
			assert.Equal(t, "https://codeberg.org/forgejo/forgejo/issues", lfsErrorResponse.DocumentationURL)
		})

		t.Run("ValidAccess", func(t *testing.T) {
			req := newRequest(t, p, "dummy5")
			req.AddBasicAuth("user2")

			resp := MakeRequest(t, req, http.StatusOK)
			assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
			meta, err = git_model.GetLFSMetaObjectByOid(db.DefaultContext, repo.ID, p.Oid)
			require.NoError(t, err)
			assert.NotNil(t, meta)
		})

		// Cleanup
		err = contentStore.Delete(p.RelativePath())
		require.NoError(t, err)
	})

	t.Run("MetaAlreadyExists", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := newRequest(t, lfs.Pointer{Oid: oid, Size: 6}, "")
		req.AddBasicAuth("user2")

		resp := MakeRequest(t, req, http.StatusOK)
		assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
	})

	t.Run("HashMismatch", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := newRequest(t, lfs.Pointer{Oid: "2581dd7bbc1fe44726de4b7dd806a087a978b9c5aec0a60481259e34be09b06a", Size: 1}, "a")
		req.AddBasicAuth("user2")

		resp := MakeRequest(t, req, http.StatusUnprocessableEntity)
		assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
		DecodeJSON(t, resp, &lfsErrorResponse)
		assert.Equal(t, "Unprocessable Entity: content hash does not match OID", lfsErrorResponse.Message)
		assert.Equal(t, "https://codeberg.org/forgejo/forgejo/issues", lfsErrorResponse.DocumentationURL)
	})

	t.Run("SizeMismatch", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := newRequest(t, lfs.Pointer{Oid: "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb", Size: 2}, "a")
		req.AddBasicAuth("user2")

		resp := MakeRequest(t, req, http.StatusUnprocessableEntity)
		assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
		DecodeJSON(t, resp, &lfsErrorResponse)
		assert.Equal(t, "Unprocessable Entity: content size does not match", lfsErrorResponse.Message)
		assert.Equal(t, "https://codeberg.org/forgejo/forgejo/issues", lfsErrorResponse.DocumentationURL)
	})

	t.Run("Unauthenticated", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		p := lfs.Pointer{Oid: "6ccce4863b70f258d691f59609d31b4502e1ba5199942d3bc5d35d17a4ce771d", Size: 5}

		req := newRequest(t, p, "gitea")

		resp := MakeRequest(t, req, http.StatusUnauthorized)
		assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
		assert.Equal(t, "Basic realm=forgejo-lfs", resp.Header().Get("LFS-Authenticate"))
		DecodeJSON(t, resp, &lfsErrorResponse)
		assert.Equal(t, "Unauthorized", lfsErrorResponse.Message)
		assert.Equal(t, "https://codeberg.org/forgejo/forgejo/issues", lfsErrorResponse.DocumentationURL)
	})

	t.Run("Success", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		p := lfs.Pointer{Oid: "6ccce4863b70f258d691f59609d31b4502e1ba5199942d3bc5d35d17a4ce771d", Size: 5}

		req := newRequest(t, p, "gitea")
		req.AddBasicAuth("user2")

		resp := MakeRequest(t, req, http.StatusOK)
		assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))

		contentStore := lfs.NewContentStore()
		exist, err := contentStore.Exists(p)
		require.NoError(t, err)
		assert.True(t, exist)

		meta, err := git_model.GetLFSMetaObjectByOid(db.DefaultContext, repo.ID, p.Oid)
		require.NoError(t, err)
		assert.NotNil(t, meta)
	})
}

// Beyond [lfs.VerifyHandler], this can also be used to test getAuthenticatedMeta.
func TestAPILFSVerify(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	defer test.MockVariableValue(&setting.LFS.StartServer, true)()

	repo := createLFSTestRepository(t, "verify")

	content := []byte("dummy3")
	oid := storeObjectInRepo(t, repo.ID, &content)
	defer git_model.RemoveLFSMetaObjectByOid(db.DefaultContext, repo.ID, oid)

	newRequest := func(t testing.TB, p *lfs.Pointer) *RequestWrapper {
		return NewRequestWithJSON(t, "POST", "/user2/lfs-verify-repo.git/info/lfs/verify", p).
			SetHeader("Accept", lfs.AcceptHeader).
			SetHeader("Content-Type", lfs.MediaType)
	}

	var lfsErrorResponse lfs.ErrorResponse

	t.Run("InvalidJsonRequest", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := newRequest(t, nil)
		req.AddBasicAuth("user2")

		resp := MakeRequest(t, req, http.StatusUnprocessableEntity)
		assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
		DecodeJSON(t, resp, &lfsErrorResponse)
		assert.Equal(t, "Unprocessable Entity: OID has an invalid format", lfsErrorResponse.Message)
		assert.Equal(t, "https://codeberg.org/forgejo/forgejo/issues", lfsErrorResponse.DocumentationURL)
	})

	t.Run("InvalidPointer", func(t *testing.T) {
		t.Run("InvalidOID", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := newRequest(t, &lfs.Pointer{})
			req.AddBasicAuth("user2")

			resp := MakeRequest(t, req, http.StatusUnprocessableEntity)
			assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
			expected, err := json.Marshal(lfs.ErrorResponse{
				Message:          "Unprocessable Entity: OID has an invalid format",
				DocumentationURL: "https://codeberg.org/forgejo/forgejo/issues",
			})
			require.NoError(t, err)
			assert.JSONEq(t, string(expected), resp.Body.String())
		})

		// Size mismatch leads to pointer not being found.
		t.Run("NegativeSize", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := newRequest(t, &lfs.Pointer{
				Oid:  "83de2e488b89a0aa1c97496b888120a28b0c1e15463a4adb8405578c540f36d4",
				Size: -1,
			})
			req.AddBasicAuth("user2")

			resp := MakeRequest(t, req, http.StatusNotFound)
			assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
		})
	})

	t.Run("PointerNotExisting", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := newRequest(t, &lfs.Pointer{Oid: "fb8f7d8435968c4f82a726a92395be4d16f2f63116caf36c8ad35c60831ab042", Size: 6})
		req.AddBasicAuth("user2")

		resp := MakeRequest(t, req, http.StatusNotFound)
		assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
		DecodeJSON(t, resp, &lfsErrorResponse)
		assert.Equal(t, "Not Found", lfsErrorResponse.Message)
		assert.Equal(t, "https://codeberg.org/forgejo/forgejo/issues", lfsErrorResponse.DocumentationURL)
	})

	t.Run("Unauthenticated", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := newRequest(t, &lfs.Pointer{Oid: oid, Size: 6})

		resp := MakeRequest(t, req, http.StatusUnauthorized)
		assert.Equal(t, "Basic realm=forgejo-lfs", resp.Header().Get("LFS-Authenticate"))
		assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
		DecodeJSON(t, resp, &lfsErrorResponse)
		assert.Equal(t, "Unauthorized", lfsErrorResponse.Message)
		assert.Equal(t, "https://codeberg.org/forgejo/forgejo/issues", lfsErrorResponse.DocumentationURL)
	})

	t.Run("Success", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := newRequest(t, &lfs.Pointer{Oid: oid, Size: 6})
		req.AddBasicAuth("user2")

		resp := MakeRequest(t, req, http.StatusOK)
		assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
	})
}

// Accessing git LFS resources uses CheckRepoScopedToken to validate a PAT; here we run that through all variations of
// access token resource access to ensure it is accurately applied for LFS access.
func TestAPILFSScopeAndResources(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	writeOperation := func(t *testing.T, repoFullName, token string, expectedStatus int) {
		oid := "83de2e488b89a0aa1c97496b888120a28b0c1e15463a4adb8405578c540f36d4"
		batch := &lfs.BatchRequest{
			Operation: "upload",
			Objects: []lfs.Pointer{
				{Oid: oid, Size: 6},
			},
		}
		req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/%s.git/info/lfs/objects/batch", repoFullName), batch).
			SetHeader("Accept", lfs.AcceptHeader).
			SetHeader("Content-Type", lfs.MediaType)
		req.Request.SetBasicAuth("any", token)
		MakeRequest(t, req, expectedStatus)
	}
	readOperation := func(t *testing.T, repoFullName, token string, expectedStatus int) {
		oid := "83de2e488b89a0aa1c97496b888120a28b0c1e15463a4adb8405578c540f36d4"
		batch := &lfs.BatchRequest{
			Operation: "download",
			Objects: []lfs.Pointer{
				{Oid: oid, Size: 6},
			},
		}
		req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/%s.git/info/lfs/objects/batch", repoFullName), batch).
			SetHeader("Accept", lfs.AcceptHeader).
			SetHeader("Content-Type", lfs.MediaType)
		req.Request.SetBasicAuth("any", token)
		MakeRequest(t, req, expectedStatus)
	}

	t.Run("read-write access token", func(t *testing.T) {
		session := loginUser(t, "user2")
		allToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

		t.Run("allowed public repo1", func(t *testing.T) {
			readOperation(t, "user2/repo1", allToken, http.StatusOK)
			writeOperation(t, "user2/repo1", allToken, http.StatusOK)
		})
		t.Run("allowed private repo2", func(t *testing.T) {
			readOperation(t, "user2/repo2", allToken, http.StatusOK)
			writeOperation(t, "user2/repo2", allToken, http.StatusOK)
		})
		// repo16 is a second repo used in fine-grain testing below, so we include it in other tests as a baseline
		t.Run("allowed private repo16", func(t *testing.T) {
			readOperation(t, "user2/repo16", allToken, http.StatusOK)
			writeOperation(t, "user2/repo16", allToken, http.StatusOK)
		})
	})

	t.Run("read-only access token", func(t *testing.T) {
		session := loginUser(t, "user2")
		allToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadRepository)

		t.Run("allowed public repo1", func(t *testing.T) {
			readOperation(t, "user2/repo1", allToken, http.StatusOK)
			writeOperation(t, "user2/repo1", allToken, http.StatusForbidden)
		})
		t.Run("allowed private repo2", func(t *testing.T) {
			readOperation(t, "user2/repo2", allToken, http.StatusOK)
			writeOperation(t, "user2/repo2", allToken, http.StatusForbidden)
		})
		// repo16 is a second repo used in fine-grain testing below, so we include it in other tests as a baseline
		t.Run("allowed private repo16", func(t *testing.T) {
			readOperation(t, "user2/repo16", allToken, http.StatusOK)
			writeOperation(t, "user2/repo16", allToken, http.StatusForbidden)
		})
	})

	t.Run("public-only access token", func(t *testing.T) {
		session := loginUser(t, "user2")
		publicOnlyToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopePublicOnly, auth_model.AccessTokenScopeReadRepository)

		t.Run("allowed public repo1", func(t *testing.T) {
			readOperation(t, "user2/repo1", publicOnlyToken, http.StatusOK)
		})
		t.Run("denied private repo2", func(t *testing.T) {
			readOperation(t, "user2/repo2", publicOnlyToken, http.StatusForbidden)
		})
		t.Run("denied private repo16", func(t *testing.T) {
			readOperation(t, "user2/repo16", publicOnlyToken, http.StatusForbidden)
		})
	})

	t.Run("specific repo access token", func(t *testing.T) {
		repo2OnlyToken := createFineGrainedRepoAccessToken(t, "user2",
			[]auth_model.AccessTokenScope{auth_model.AccessTokenScopeReadRepository},
			[]int64{2},
		)

		t.Run("allowed public repo1", func(t *testing.T) {
			readOperation(t, "user2/repo1", repo2OnlyToken, http.StatusOK)
		})
		t.Run("allowed inside fine-grain repo2", func(t *testing.T) {
			readOperation(t, "user2/repo2", repo2OnlyToken, http.StatusOK)
		})
		t.Run("denied private outside fine-grain repo16", func(t *testing.T) {
			readOperation(t, "user2/repo16", repo2OnlyToken, http.StatusForbidden)
		})
	})
}
