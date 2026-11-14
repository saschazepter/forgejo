// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forgejo.org/models/auth"
	"forgejo.org/models/db"
	git_model "forgejo.org/models/git"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	"forgejo.org/modules/json"
	"forgejo.org/modules/lfs"
	"forgejo.org/modules/setting"
	"forgejo.org/tests"

	"github.com/klauspost/compress/gzhttp"
	gzipp "github.com/klauspost/compress/gzip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func storeObjectInRepo(t *testing.T, repositoryID int64, content *[]byte) string {
	pointer, err := lfs.GeneratePointer(bytes.NewReader(*content))
	require.NoError(t, err)

	_, err = git_model.NewLFSMetaObject(db.DefaultContext, repositoryID, *pointer)
	require.NoError(t, err)
	contentStore := lfs.NewContentStore()
	exist, err := contentStore.Exists(*pointer)
	require.NoError(t, err)
	if !exist {
		err := contentStore.Put(*pointer, bytes.NewReader(*content))
		require.NoError(t, err)
	}
	return pointer.Oid
}

func makeStoreRequest(t *testing.T, content *[]byte, extraHeader *http.Header) *RequestWrapper {
	repo, err := repo_model.GetRepositoryByOwnerAndName(db.DefaultContext, "user2", "repo1")
	require.NoError(t, err)
	oid := storeObjectInRepo(t, repo.ID, content)
	t.Cleanup(func() {
		git_model.RemoveLFSMetaObjectByOid(db.DefaultContext, repo.ID, oid)
	})

	// Request OID
	req := NewRequest(t, "GET", "/user2/repo1.git/info/lfs/objects/"+oid+"/test")
	req.Header.Set("Accept-Encoding", "gzip")
	if extraHeader != nil {
		for key, values := range *extraHeader {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	}

	return req
}

func storeAndGetLfsToken(t *testing.T, content *[]byte, extraHeader *http.Header, expectedStatus int, ts ...auth.AccessTokenScope) *httptest.ResponseRecorder {
	req := makeStoreRequest(t, content, extraHeader)
	token := getUserToken(t, "user2", ts...)
	req.SetBasicAuth("user2", token)
	resp := MakeRequest(t, req, expectedStatus)
	return resp
}

type oauth2AuthMode string

const (
	oauth2ViaBearer oauth2AuthMode = "bearer"
	oauth2ViaBasic  oauth2AuthMode = "basic"
)

func storeAndGetLfsOAuthToken(t *testing.T, content *[]byte, extraHeader *http.Header, authMode oauth2AuthMode, expectedStatus int, ts ...auth.AccessTokenScope) *httptest.ResponseRecorder {
	// Update the grant to the requested scopes `ts`
	oauthGrant := unittest.AssertExistsAndLoadBean(t, &auth.OAuth2Grant{ID: 1})
	scopes := make([]string, len(ts))
	for i, s := range ts {
		scopes[i] = string(s)
	}
	oauthGrant.Scope = fmt.Sprintf("openid profile %s", strings.Join(scopes, " "))
	_, err := db.GetEngine(t.Context()).ID(oauthGrant.ID).Update(oauthGrant)
	require.NoError(t, err)

	accessTokenReq := NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     "da7da3ba-9a13-4167-856f-3899de0b0138",
		"client_secret": "4MK8Na6R55smdCY0WuCCumZ6hjRPnGY5saWVRHHjJiA=",
		"redirect_uri":  "a",
		"code":          "authcode",
		"code_verifier": "N1Zo9-8Rfwhkt68r1r29ty8YwIraXR8eh_1Qwxg7yQXsonBt",
	})
	accessTokenResp := MakeRequest(t, accessTokenReq, http.StatusOK)
	type response struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	parsed := new(response)
	require.NoError(t, json.Unmarshal(accessTokenResp.Body.Bytes(), parsed))

	req := makeStoreRequest(t, content, extraHeader)
	switch authMode {
	case oauth2ViaBearer:
		req.SetHeader("Authorization", fmt.Sprintf("Bearer %s", parsed.AccessToken))
	case oauth2ViaBasic:
		req.SetBasicAuth("user2", parsed.AccessToken)
	default:
		t.Fail()
	}
	resp := MakeRequest(t, req, expectedStatus)
	return resp
}

func storeAndGetLfs(t *testing.T, content *[]byte, extraHeader *http.Header, expectedStatus int) *httptest.ResponseRecorder {
	req := makeStoreRequest(t, content, extraHeader)
	session := loginUser(t, "user2")
	resp := session.MakeRequest(t, req, expectedStatus)
	return resp
}

func checkResponseTestContentEncoding(t *testing.T, content *[]byte, resp *httptest.ResponseRecorder, expectGzip bool) {
	contentEncoding := resp.Header().Get("Content-Encoding")
	if !expectGzip || !setting.EnableGzip {
		assert.NotContains(t, contentEncoding, "gzip")

		result := resp.Body.Bytes()
		assert.Equal(t, *content, result)
	} else {
		assert.Contains(t, contentEncoding, "gzip")
		gzippReader, err := gzipp.NewReader(resp.Body)
		require.NoError(t, err)
		result, err := io.ReadAll(gzippReader)
		require.NoError(t, err)
		assert.Equal(t, *content, result)
	}
}

func TestLFSGetSmall(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	content := []byte("A very small file\n")

	resp := storeAndGetLfs(t, &content, nil, http.StatusOK)
	checkResponseTestContentEncoding(t, &content, resp, false)
}

func TestLFSGetSmallToken(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	content := []byte("A very small file\n")

	resp := storeAndGetLfsToken(t, &content, nil, http.StatusOK, auth.AccessTokenScopePublicOnly, auth.AccessTokenScopeReadRepository)
	checkResponseTestContentEncoding(t, &content, resp, false)
}

func TestLFSGetSmallTokenFail(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	content := []byte("A very small file\n")

	storeAndGetLfsToken(t, &content, nil, http.StatusForbidden, auth.AccessTokenScopeReadNotification)
}

func TestLFSGetSmallOauthToken(t *testing.T) {
	for _, a := range []oauth2AuthMode{oauth2ViaBasic, oauth2ViaBearer} {
		t.Run(string(a), func(t *testing.T) {
			defer tests.PrepareTestEnv(t)()
			content := []byte("A very small file\n")

			resp := storeAndGetLfsOAuthToken(t, &content, nil, a, http.StatusOK, auth.AccessTokenScopePublicOnly, auth.AccessTokenScopeReadRepository)
			checkResponseTestContentEncoding(t, &content, resp, false)
		})
	}
}

func TestLFSGetSmallOAuthTokenFail(t *testing.T) {
	for _, a := range []oauth2AuthMode{oauth2ViaBasic, oauth2ViaBearer} {
		t.Run(string(a), func(t *testing.T) {
			defer tests.PrepareTestEnv(t)()
			content := []byte("A very small file\n")

			storeAndGetLfsOAuthToken(t, &content, nil, a, http.StatusForbidden, auth.AccessTokenScopeReadNotification)
		})
	}
}

func TestLFSGetLarge(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	content := make([]byte, gzhttp.DefaultMinSize*10)
	for i := range content {
		content[i] = byte(i % 256)
	}

	resp := storeAndGetLfs(t, &content, nil, http.StatusOK)
	checkResponseTestContentEncoding(t, &content, resp, true)
}

func TestLFSGetGzip(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	b := make([]byte, gzhttp.DefaultMinSize*10)
	for i := range b {
		b[i] = byte(i % 256)
	}
	outputBuffer := bytes.NewBuffer([]byte{})
	gzippWriter := gzipp.NewWriter(outputBuffer)
	gzippWriter.Write(b)
	gzippWriter.Close()
	content := outputBuffer.Bytes()

	resp := storeAndGetLfs(t, &content, nil, http.StatusOK)
	checkResponseTestContentEncoding(t, &content, resp, false)
}

func TestLFSGetZip(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	b := make([]byte, gzhttp.DefaultMinSize*10)
	for i := range b {
		b[i] = byte(i % 256)
	}
	outputBuffer := bytes.NewBuffer([]byte{})
	zipWriter := zip.NewWriter(outputBuffer)
	fileWriter, err := zipWriter.Create("default")
	require.NoError(t, err)
	fileWriter.Write(b)
	zipWriter.Close()
	content := outputBuffer.Bytes()

	resp := storeAndGetLfs(t, &content, nil, http.StatusOK)
	checkResponseTestContentEncoding(t, &content, resp, false)
}

func TestLFSGetRangeNo(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	content := []byte("123456789\n")

	resp := storeAndGetLfs(t, &content, nil, http.StatusOK)
	assert.Equal(t, content, resp.Body.Bytes())
}

func TestLFSGetRange(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	content := []byte("123456789\n")

	tests := []struct {
		in     string
		out    string
		status int
	}{
		{"bytes=0-0", "1", http.StatusPartialContent},
		{"bytes=0-1", "12", http.StatusPartialContent},
		{"bytes=1-1", "2", http.StatusPartialContent},
		{"bytes=1-3", "234", http.StatusPartialContent},
		{"bytes=1-", "23456789\n", http.StatusPartialContent},
		// end-range smaller than start-range is ignored
		{"bytes=1-0", "23456789\n", http.StatusPartialContent},
		{"bytes=0-10", "123456789\n", http.StatusPartialContent},
		// end-range bigger than length-1 is ignored
		{"bytes=0-11", "123456789\n", http.StatusPartialContent},
		{"bytes=11-", "Requested Range Not Satisfiable", http.StatusRequestedRangeNotSatisfiable},
		// incorrect header value cause whole header to be ignored
		{"bytes=-", "123456789\n", http.StatusOK},
		{"foobar", "123456789\n", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			h := http.Header{
				"Range": []string{tt.in},
			}
			resp := storeAndGetLfs(t, &content, &h, tt.status)
			if tt.status == http.StatusPartialContent || tt.status == http.StatusOK {
				assert.Equal(t, tt.out, resp.Body.String())
			} else {
				var er lfs.ErrorResponse
				err := json.Unmarshal(resp.Body.Bytes(), &er)
				require.NoError(t, err)
				assert.Equal(t, "https://codeberg.org/forgejo/forgejo/issues", er.DocumentationURL)
				assert.Equal(t, tt.out, er.Message)
			}
		})
	}
}
