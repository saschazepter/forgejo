// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/json"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	"forgejo.org/routers/web/auth"
	forgejo_context "forgejo.org/services/context"
	"forgejo.org/tests"

	"github.com/markbates/goth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthorizeNoClientID(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	req := NewRequest(t, "GET", "/login/oauth/authorize")
	ctx := loginUser(t, "user2")
	resp := ctx.MakeRequest(t, req, http.StatusBadRequest)
	assert.Contains(t, resp.Body.String(), "Client ID not registered")
}

func TestAuthorizeUnregisteredRedirect(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	req := NewRequest(t, "GET", "/login/oauth/authorize?client_id=da7da3ba-9a13-4167-856f-3899de0b0138&redirect_uri=UNREGISTERED&response_type=code&state=thestate")
	ctx := loginUser(t, "user1")
	resp := ctx.MakeRequest(t, req, http.StatusBadRequest)
	assert.Contains(t, resp.Body.String(), "Unregistered Redirect URI")
}

func TestAuthorizeUnsupportedResponseType(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	req := NewRequest(t, "GET", "/login/oauth/authorize?client_id=da7da3ba-9a13-4167-856f-3899de0b0138&redirect_uri=a&response_type=UNEXPECTED&state=thestate")
	ctx := loginUser(t, "user1")
	resp := ctx.MakeRequest(t, req, http.StatusSeeOther)
	u, err := resp.Result().Location()
	require.NoError(t, err)
	assert.Equal(t, "unsupported_response_type", u.Query().Get("error"))
	assert.Equal(t, "Only code response type is supported.", u.Query().Get("error_description"))
}

func TestAuthorizeUnsupportedCodeChallengeMethod(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	req := NewRequest(t, "GET", "/login/oauth/authorize?client_id=da7da3ba-9a13-4167-856f-3899de0b0138&redirect_uri=a&response_type=code&state=thestate&code_challenge_method=UNEXPECTED")
	ctx := loginUser(t, "user1")
	resp := ctx.MakeRequest(t, req, http.StatusSeeOther)
	u, err := resp.Result().Location()
	require.NoError(t, err)
	assert.Equal(t, "invalid_request", u.Query().Get("error"))
	assert.Equal(t, "unsupported code challenge method", u.Query().Get("error_description"))
}

func TestAuthorizeLoginRedirect(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	req := NewRequest(t, "GET", "/login/oauth/authorize")
	assert.Contains(t, MakeRequest(t, req, http.StatusSeeOther).Body.String(), "/user/login")
}

func TestAuthorizeShow(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	req := NewRequest(t, "GET", "/login/oauth/authorize?client_id=da7da3ba-9a13-4167-856f-3899de0b0138&redirect_uri=a&response_type=code&state=thestate")
	ctx := loginUser(t, "user4")
	resp := ctx.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	htmlDoc.AssertElement(t, "#authorize-app", true)
	htmlDoc.GetCSRF()
}

func TestOAuth_AuthorizeConfidentialTwice(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	// da7da3ba-9a13-4167-856f-3899de0b0138 a confidential client in models/fixtures/oauth2_application.yml

	// request authorization for the first time shows the grant page ...
	authorizeURL := "/login/oauth/authorize?client_id=da7da3ba-9a13-4167-856f-3899de0b0138&redirect_uri=a&response_type=code&state=thestate"
	req := NewRequest(t, "GET", authorizeURL)
	ctx := loginUser(t, "user4")
	resp := ctx.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	htmlDoc.AssertElement(t, "#authorize-app", true)

	// ... and the user grants the authorization
	req = NewRequestWithValues(t, "POST", "/login/oauth/grant", map[string]string{
		"_csrf":        htmlDoc.GetCSRF(),
		"client_id":    "da7da3ba-9a13-4167-856f-3899de0b0138",
		"redirect_uri": "a",
		"state":        "thestate",
		"granted":      "true",
	})
	resp = ctx.MakeRequest(t, req, http.StatusSeeOther)
	assert.Contains(t, test.RedirectURL(resp), "code=")

	// request authorization the second time and the grant page is not shown again, redirection happens immediately
	req = NewRequest(t, "GET", authorizeURL)
	resp = ctx.MakeRequest(t, req, http.StatusSeeOther)
	assert.Contains(t, test.RedirectURL(resp), "code=")
}

func TestOAuth_AuthorizePublicTwice(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	// ce5a1322-42a7-11ed-b878-0242ac120002 is a public client in models/fixtures/oauth2_application.yml
	authorizeURL := "/login/oauth/authorize?client_id=ce5a1322-42a7-11ed-b878-0242ac120002&redirect_uri=b&response_type=code&code_challenge_method=plain&code_challenge=CODE&state=thestate"
	ctx := loginUser(t, "user4")
	// a public client must be authorized every time
	for _, name := range []string{"First", "Second"} {
		t.Run(name, func(t *testing.T) {
			req := NewRequest(t, "GET", authorizeURL)
			resp := ctx.MakeRequest(t, req, http.StatusOK)

			htmlDoc := NewHTMLParser(t, resp.Body)
			htmlDoc.AssertElement(t, "#authorize-app", true)

			req = NewRequestWithValues(t, "POST", "/login/oauth/grant", map[string]string{
				"_csrf":        htmlDoc.GetCSRF(),
				"client_id":    "ce5a1322-42a7-11ed-b878-0242ac120002",
				"redirect_uri": "b",
				"state":        "thestate",
				"granted":      "true",
			})
			resp = ctx.MakeRequest(t, req, http.StatusSeeOther)
			assert.Contains(t, test.RedirectURL(resp), "code=")
		})
	}
}

func TestAuthorizeRedirectWithExistingGrant(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	req := NewRequest(t, "GET", "/login/oauth/authorize?client_id=da7da3ba-9a13-4167-856f-3899de0b0138&redirect_uri=https%3A%2F%2Fexample.com%2Fxyzzy&response_type=code&state=thestate")
	ctx := loginUser(t, "user1")
	resp := ctx.MakeRequest(t, req, http.StatusSeeOther)
	u, err := resp.Result().Location()
	require.NoError(t, err)
	assert.Equal(t, "thestate", u.Query().Get("state"))
	assert.Greaterf(t, len(u.Query().Get("code")), 30, "authorization code '%s' should be longer then 30", u.Query().Get("code"))
	u.RawQuery = ""
	assert.Equal(t, "https://example.com/xyzzy", u.String())
}

func TestAuthorizePKCERequiredForPublicClient(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	req := NewRequest(t, "GET", "/login/oauth/authorize?client_id=ce5a1322-42a7-11ed-b878-0242ac120002&redirect_uri=http%3A%2F%2F127.0.0.1&response_type=code&state=thestate")
	ctx := loginUser(t, "user1")
	resp := ctx.MakeRequest(t, req, http.StatusSeeOther)
	u, err := resp.Result().Location()
	require.NoError(t, err)
	assert.Equal(t, "invalid_request", u.Query().Get("error"))
	assert.Equal(t, "PKCE is required for public clients", u.Query().Get("error_description"))
}

func TestAccessTokenExchange(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	req := NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     "da7da3ba-9a13-4167-856f-3899de0b0138",
		"client_secret": "4MK8Na6R55smdCY0WuCCumZ6hjRPnGY5saWVRHHjJiA=",
		"redirect_uri":  "a",
		"code":          "authcode",
		"code_verifier": "N1Zo9-8Rfwhkt68r1r29ty8YwIraXR8eh_1Qwxg7yQXsonBt",
	})
	resp := MakeRequest(t, req, http.StatusOK)
	type response struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	parsed := new(response)

	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), parsed))
	assert.Greater(t, len(parsed.AccessToken), 10)
	assert.Greater(t, len(parsed.RefreshToken), 10)
}

func TestAccessTokenExchangeWithPublicClient(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	req := NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     "ce5a1322-42a7-11ed-b878-0242ac120002",
		"redirect_uri":  "http://127.0.0.1",
		"code":          "authcodepublic",
		"code_verifier": "N1Zo9-8Rfwhkt68r1r29ty8YwIraXR8eh_1Qwxg7yQXsonBt",
	})
	resp := MakeRequest(t, req, http.StatusOK)
	type response struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	parsed := new(response)

	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), parsed))
	assert.Greater(t, len(parsed.AccessToken), 10)
	assert.Greater(t, len(parsed.RefreshToken), 10)
}

func TestAccessTokenExchangeJSON(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	req := NewRequestWithJSON(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     "da7da3ba-9a13-4167-856f-3899de0b0138",
		"client_secret": "4MK8Na6R55smdCY0WuCCumZ6hjRPnGY5saWVRHHjJiA=",
		"redirect_uri":  "a",
		"code":          "authcode",
		"code_verifier": "N1Zo9-8Rfwhkt68r1r29ty8YwIraXR8eh_1Qwxg7yQXsonBt",
	})
	resp := MakeRequest(t, req, http.StatusOK)
	type response struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	parsed := new(response)

	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), parsed))
	assert.Greater(t, len(parsed.AccessToken), 10)
	assert.Greater(t, len(parsed.RefreshToken), 10)
}

func TestAccessTokenExchangeWithoutPKCE(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	req := NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     "da7da3ba-9a13-4167-856f-3899de0b0138",
		"client_secret": "4MK8Na6R55smdCY0WuCCumZ6hjRPnGY5saWVRHHjJiA=",
		"redirect_uri":  "a",
		"code":          "authcode",
	})
	resp := MakeRequest(t, req, http.StatusBadRequest)
	parsedError := new(auth.AccessTokenError)
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), parsedError))
	assert.Equal(t, "unauthorized_client", string(parsedError.ErrorCode))
	assert.Equal(t, "failed PKCE code challenge", parsedError.ErrorDescription)
}

func TestAccessTokenExchangeWithInvalidCredentials(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	// invalid client id
	req := NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     "???",
		"client_secret": "4MK8Na6R55smdCY0WuCCumZ6hjRPnGY5saWVRHHjJiA=",
		"redirect_uri":  "a",
		"code":          "authcode",
		"code_verifier": "N1Zo9-8Rfwhkt68r1r29ty8YwIraXR8eh_1Qwxg7yQXsonBt",
	})
	resp := MakeRequest(t, req, http.StatusBadRequest)
	parsedError := new(auth.AccessTokenError)
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), parsedError))
	assert.Equal(t, "invalid_client", string(parsedError.ErrorCode))
	assert.Equal(t, "cannot load client with client id: '???'", parsedError.ErrorDescription)

	// invalid client secret
	req = NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     "da7da3ba-9a13-4167-856f-3899de0b0138",
		"client_secret": "???",
		"redirect_uri":  "a",
		"code":          "authcode",
		"code_verifier": "N1Zo9-8Rfwhkt68r1r29ty8YwIraXR8eh_1Qwxg7yQXsonBt",
	})
	resp = MakeRequest(t, req, http.StatusBadRequest)
	parsedError = new(auth.AccessTokenError)
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), parsedError))
	assert.Equal(t, "unauthorized_client", string(parsedError.ErrorCode))
	assert.Equal(t, "invalid client secret", parsedError.ErrorDescription)

	// invalid redirect uri
	req = NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     "da7da3ba-9a13-4167-856f-3899de0b0138",
		"client_secret": "4MK8Na6R55smdCY0WuCCumZ6hjRPnGY5saWVRHHjJiA=",
		"redirect_uri":  "???",
		"code":          "authcode",
		"code_verifier": "N1Zo9-8Rfwhkt68r1r29ty8YwIraXR8eh_1Qwxg7yQXsonBt",
	})
	resp = MakeRequest(t, req, http.StatusBadRequest)
	parsedError = new(auth.AccessTokenError)
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), parsedError))
	assert.Equal(t, "unauthorized_client", string(parsedError.ErrorCode))
	assert.Equal(t, "unexpected redirect URI", parsedError.ErrorDescription)

	// invalid authorization code
	req = NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     "da7da3ba-9a13-4167-856f-3899de0b0138",
		"client_secret": "4MK8Na6R55smdCY0WuCCumZ6hjRPnGY5saWVRHHjJiA=",
		"redirect_uri":  "a",
		"code":          "???",
		"code_verifier": "N1Zo9-8Rfwhkt68r1r29ty8YwIraXR8eh_1Qwxg7yQXsonBt",
	})
	resp = MakeRequest(t, req, http.StatusBadRequest)
	parsedError = new(auth.AccessTokenError)
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), parsedError))
	assert.Equal(t, "unauthorized_client", string(parsedError.ErrorCode))
	assert.Equal(t, "client is not authorized", parsedError.ErrorDescription)

	// invalid grant_type
	req = NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type":    "???",
		"client_id":     "da7da3ba-9a13-4167-856f-3899de0b0138",
		"client_secret": "4MK8Na6R55smdCY0WuCCumZ6hjRPnGY5saWVRHHjJiA=",
		"redirect_uri":  "a",
		"code":          "authcode",
		"code_verifier": "N1Zo9-8Rfwhkt68r1r29ty8YwIraXR8eh_1Qwxg7yQXsonBt",
	})
	resp = MakeRequest(t, req, http.StatusBadRequest)
	parsedError = new(auth.AccessTokenError)
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), parsedError))
	assert.Equal(t, "unsupported_grant_type", string(parsedError.ErrorCode))
	assert.Equal(t, "Only refresh_token or authorization_code grant type is supported", parsedError.ErrorDescription)
}

func TestAccessTokenExchangeWithBasicAuth(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	req := NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type":    "authorization_code",
		"redirect_uri":  "a",
		"code":          "authcode",
		"code_verifier": "N1Zo9-8Rfwhkt68r1r29ty8YwIraXR8eh_1Qwxg7yQXsonBt",
	})
	req.Header.Add("Authorization", "Basic ZGE3ZGEzYmEtOWExMy00MTY3LTg1NmYtMzg5OWRlMGIwMTM4OjRNSzhOYTZSNTVzbWRDWTBXdUNDdW1aNmhqUlBuR1k1c2FXVlJISGpKaUE9")
	resp := MakeRequest(t, req, http.StatusOK)
	type response struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	parsed := new(response)

	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), parsed))
	assert.Greater(t, len(parsed.AccessToken), 10)
	assert.Greater(t, len(parsed.RefreshToken), 10)

	// use wrong client_secret
	req = NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type":    "authorization_code",
		"redirect_uri":  "a",
		"code":          "authcode",
		"code_verifier": "N1Zo9-8Rfwhkt68r1r29ty8YwIraXR8eh_1Qwxg7yQXsonBt",
	})
	req.Header.Add("Authorization", "Basic ZGE3ZGEzYmEtOWExMy00MTY3LTg1NmYtMzg5OWRlMGIwMTM4OmJsYWJsYQ==")
	resp = MakeRequest(t, req, http.StatusBadRequest)
	parsedError := new(auth.AccessTokenError)
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), parsedError))
	assert.Equal(t, "unauthorized_client", string(parsedError.ErrorCode))
	assert.Equal(t, "invalid client secret", parsedError.ErrorDescription)

	// missing header
	req = NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type":    "authorization_code",
		"redirect_uri":  "a",
		"code":          "authcode",
		"code_verifier": "N1Zo9-8Rfwhkt68r1r29ty8YwIraXR8eh_1Qwxg7yQXsonBt",
	})
	resp = MakeRequest(t, req, http.StatusBadRequest)
	parsedError = new(auth.AccessTokenError)
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), parsedError))
	assert.Equal(t, "invalid_client", string(parsedError.ErrorCode))
	assert.Equal(t, "cannot load client with client id: ''", parsedError.ErrorDescription)

	// client_id inconsistent with Authorization header
	req = NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type":   "authorization_code",
		"redirect_uri": "a",
		"code":         "authcode",
		"client_id":    "inconsistent",
	})
	req.Header.Add("Authorization", "Basic ZGE3ZGEzYmEtOWExMy00MTY3LTg1NmYtMzg5OWRlMGIwMTM4OjRNSzhOYTZSNTVzbWRDWTBXdUNDdW1aNmhqUlBuR1k1c2FXVlJISGpKaUE9")
	resp = MakeRequest(t, req, http.StatusBadRequest)
	parsedError = new(auth.AccessTokenError)
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), parsedError))
	assert.Equal(t, "invalid_request", string(parsedError.ErrorCode))
	assert.Equal(t, "client_id in request body inconsistent with Authorization header", parsedError.ErrorDescription)

	// client_secret inconsistent with Authorization header
	req = NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type":    "authorization_code",
		"redirect_uri":  "a",
		"code":          "authcode",
		"client_secret": "inconsistent",
	})
	req.Header.Add("Authorization", "Basic ZGE3ZGEzYmEtOWExMy00MTY3LTg1NmYtMzg5OWRlMGIwMTM4OjRNSzhOYTZSNTVzbWRDWTBXdUNDdW1aNmhqUlBuR1k1c2FXVlJISGpKaUE9")
	resp = MakeRequest(t, req, http.StatusBadRequest)
	parsedError = new(auth.AccessTokenError)
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), parsedError))
	assert.Equal(t, "invalid_request", string(parsedError.ErrorCode))
	assert.Equal(t, "client_secret in request body inconsistent with Authorization header", parsedError.ErrorDescription)
}

func TestRefreshTokenInvalidation(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	req := NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     "da7da3ba-9a13-4167-856f-3899de0b0138",
		"client_secret": "4MK8Na6R55smdCY0WuCCumZ6hjRPnGY5saWVRHHjJiA=",
		"redirect_uri":  "a",
		"code":          "authcode",
		"code_verifier": "N1Zo9-8Rfwhkt68r1r29ty8YwIraXR8eh_1Qwxg7yQXsonBt",
	})
	resp := MakeRequest(t, req, http.StatusOK)
	type response struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	parsed := new(response)

	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), parsed))

	// test without invalidation
	setting.OAuth2.InvalidateRefreshTokens = false

	req = NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type": "refresh_token",
		"client_id":  "da7da3ba-9a13-4167-856f-3899de0b0138",
		// omit secret
		"redirect_uri":  "a",
		"refresh_token": parsed.RefreshToken,
	})
	resp = MakeRequest(t, req, http.StatusBadRequest)
	parsedError := new(auth.AccessTokenError)
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), parsedError))
	assert.Equal(t, "invalid_client", string(parsedError.ErrorCode))
	assert.Equal(t, "invalid empty client secret", parsedError.ErrorDescription)

	req = NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     "da7da3ba-9a13-4167-856f-3899de0b0138",
		"client_secret": "4MK8Na6R55smdCY0WuCCumZ6hjRPnGY5saWVRHHjJiA=",
		"redirect_uri":  "a",
		"refresh_token": "UNEXPECTED",
	})
	resp = MakeRequest(t, req, http.StatusBadRequest)
	parsedError = new(auth.AccessTokenError)
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), parsedError))
	assert.Equal(t, "unauthorized_client", string(parsedError.ErrorCode))
	assert.Equal(t, "unable to parse refresh token", parsedError.ErrorDescription)

	req = NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     "da7da3ba-9a13-4167-856f-3899de0b0138",
		"client_secret": "4MK8Na6R55smdCY0WuCCumZ6hjRPnGY5saWVRHHjJiA=",
		"redirect_uri":  "a",
		"refresh_token": parsed.RefreshToken,
	})

	bs, err := io.ReadAll(req.Body)
	require.NoError(t, err)

	req.Body = io.NopCloser(bytes.NewReader(bs))
	MakeRequest(t, req, http.StatusOK)

	req.Body = io.NopCloser(bytes.NewReader(bs))
	MakeRequest(t, req, http.StatusOK)

	// test with invalidation
	setting.OAuth2.InvalidateRefreshTokens = true
	req.Body = io.NopCloser(bytes.NewReader(bs))
	MakeRequest(t, req, http.StatusOK)

	// repeat request should fail
	req.Body = io.NopCloser(bytes.NewReader(bs))
	resp = MakeRequest(t, req, http.StatusBadRequest)
	parsedError = new(auth.AccessTokenError)
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), parsedError))
	assert.Equal(t, "unauthorized_client", string(parsedError.ErrorCode))
	assert.Equal(t, "token was already used", parsedError.ErrorDescription)
}

func TestSignInOAuthCallbackSignIn(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	//
	// OAuth2 authentication source GitLab
	//
	gitlabName := "gitlab"
	gitlab := addAuthSource(t, authSourcePayloadGitLabCustom(gitlabName))

	//
	// Create a user as if it had been previously been created by the GitLab
	// authentication source.
	//
	userGitLabUserID := "5678"
	userGitLab := &user_model.User{
		Name:        "gitlabuser",
		Email:       "gitlabuser@example.com",
		Passwd:      "gitlabuserpassword",
		Type:        user_model.UserTypeIndividual,
		LoginType:   auth_model.OAuth2,
		LoginSource: gitlab.ID,
		LoginName:   userGitLabUserID,
	}
	defer createUser(t.Context(), t, userGitLab)()

	//
	// A request for user information sent to Goth will return a
	// goth.User exactly matching the user created above.
	//
	defer mockCompleteUserAuth(func(res http.ResponseWriter, req *http.Request) (goth.User, error) {
		return goth.User{
			Provider: gitlabName,
			UserID:   userGitLabUserID,
			Email:    userGitLab.Email,
		}, nil
	})()
	req := NewRequest(t, "GET", fmt.Sprintf("/user/oauth2/%s/callback?code=XYZ&state=XYZ", gitlabName))
	resp := MakeRequest(t, req, http.StatusSeeOther)
	assert.Equal(t, "/", test.RedirectURL(resp))
	userAfterLogin := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: userGitLab.ID})
	assert.Greater(t, userAfterLogin.LastLoginUnix, userGitLab.LastLoginUnix)
}

func TestSignInOAuthCallbackWithoutPKCEWhenUnsupported(t *testing.T) {
	// https://codeberg.org/forgejo/forgejo/issues/4033
	defer tests.PrepareTestEnv(t)()

	// Setup authentication source
	gitlabName := "gitlab"
	gitlab := addAuthSource(t, authSourcePayloadGitLabCustom(gitlabName))
	// Create a user as if it had been previously been created by the authentication source.
	userGitLabUserID := "5678"
	userGitLab := &user_model.User{
		Name:        "gitlabuser",
		Email:       "gitlabuser@example.com",
		Passwd:      "gitlabuserpassword",
		Type:        user_model.UserTypeIndividual,
		LoginType:   auth_model.OAuth2,
		LoginSource: gitlab.ID,
		LoginName:   userGitLabUserID,
	}
	defer createUser(t.Context(), t, userGitLab)()

	// initial redirection (to generate the code_challenge)
	session := emptyTestSession(t)
	req := NewRequest(t, "GET", fmt.Sprintf("/user/oauth2/%s", gitlabName))
	resp := session.MakeRequest(t, req, http.StatusTemporaryRedirect)
	dest, err := url.Parse(resp.Header().Get("Location"))
	require.NoError(t, err)
	assert.Empty(t, dest.Query().Get("code_challenge_method"))
	assert.Empty(t, dest.Query().Get("code_challenge"))

	// callback (to check the initial code_challenge)
	defer mockCompleteUserAuth(func(res http.ResponseWriter, req *http.Request) (goth.User, error) {
		assert.Empty(t, req.URL.Query().Get("code_verifier"))
		return goth.User{
			Provider: gitlabName,
			UserID:   userGitLabUserID,
			Email:    userGitLab.Email,
		}, nil
	})()
	req = NewRequest(t, "GET", fmt.Sprintf("/user/oauth2/%s/callback?code=XYZ&state=XYZ", gitlabName))
	resp = session.MakeRequest(t, req, http.StatusSeeOther)
	assert.Equal(t, "/", test.RedirectURL(resp))
	unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: userGitLab.ID})
}

func TestSignInOAuthCallbackPKCE(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, u *url.URL) {
		// Setup authentication source
		sourceName := "oidc"
		authSource := addAuthSource(t, authSourcePayloadOpenIDConnect(sourceName, u.String()))
		// Create a user as if it had been previously been created by the authentication source.
		userID := "5678"
		user := &user_model.User{
			Name:        "oidc.user",
			Email:       "oidc.user@example.com",
			Passwd:      "oidc.userpassword",
			Type:        user_model.UserTypeIndividual,
			LoginType:   auth_model.OAuth2,
			LoginSource: authSource.ID,
			LoginName:   userID,
		}
		defer createUser(t.Context(), t, user)()

		// initial redirection (to generate the code_challenge)
		session := emptyTestSession(t)
		req := NewRequest(t, "GET", fmt.Sprintf("/user/oauth2/%s", sourceName))
		resp := session.MakeRequest(t, req, http.StatusTemporaryRedirect)
		dest, err := url.Parse(resp.Header().Get("Location"))
		require.NoError(t, err)
		assert.Equal(t, "S256", dest.Query().Get("code_challenge_method"))
		codeChallenge := dest.Query().Get("code_challenge")
		assert.NotEmpty(t, codeChallenge)

		// callback (to check the initial code_challenge)
		defer mockCompleteUserAuth(func(res http.ResponseWriter, req *http.Request) (goth.User, error) {
			codeVerifier := req.URL.Query().Get("code_verifier")
			assert.NotEmpty(t, codeVerifier)
			assert.Greater(t, len(codeVerifier), 40, codeVerifier)

			sha2 := sha256.New()
			io.WriteString(sha2, codeVerifier)
			assert.Equal(t, codeChallenge, base64.RawURLEncoding.EncodeToString(sha2.Sum(nil)))

			return goth.User{
				Provider: sourceName,
				UserID:   userID,
				Email:    user.Email,
			}, nil
		})()
		req = NewRequest(t, "GET", fmt.Sprintf("/user/oauth2/%s/callback?code=XYZ&state=XYZ", sourceName))
		resp = session.MakeRequest(t, req, http.StatusSeeOther)
		assert.Equal(t, "/", test.RedirectURL(resp))
		unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: user.ID})
	})
}

func TestWellKnownDocumentIssuerDoesNotEndWithASlash(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	req := NewRequest(t, "GET", "/.well-known/openid-configuration")
	resp := MakeRequest(t, req, http.StatusOK)
	type response struct {
		Issuer string `json:"issuer"`
	}
	parsed := new(response)

	DecodeJSON(t, resp, parsed)
	assert.Equal(t, strings.TrimSuffix(setting.AppURL, "/"), parsed.Issuer)
}

func TestSignInOAuthCallbackRedirectToEscaping(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	//
	// OAuth2 authentication source GitLab
	//
	gitlabName := "gitlab"
	gitlab := addAuthSource(t, authSourcePayloadGitLabCustom(gitlabName))

	//
	// Create a user as if it had been previously created by the GitLab
	// authentication source.
	//
	userGitLabUserID := "5678"
	userGitLab := &user_model.User{
		Name:        "gitlabuser",
		Email:       "gitlabuser@example.com",
		Passwd:      "gitlabuserpassword",
		Type:        user_model.UserTypeIndividual,
		LoginType:   auth_model.OAuth2,
		LoginSource: gitlab.ID,
		LoginName:   userGitLabUserID,
	}
	defer createUser(t.Context(), t, userGitLab)()

	//
	// A request for user information sent to Goth will return a
	// goth.User exactly matching the user created above.
	//
	defer mockCompleteUserAuth(func(res http.ResponseWriter, req *http.Request) (goth.User, error) {
		return goth.User{
			Provider: gitlabName,
			UserID:   userGitLabUserID,
			Email:    userGitLab.Email,
		}, nil
	})()
	req := NewRequest(t, "GET", fmt.Sprintf("/user/oauth2/%s/callback?code=XYZ&state=XYZ", gitlabName))
	req.AddCookie(&http.Cookie{
		Name:  "redirect_to",
		Value: "/login/oauth/authorize?redirect_uri=https%3A%2F%2Ftranslate.example.org",
		Path:  "/",
	})
	resp := MakeRequest(t, req, http.StatusSeeOther)

	hasNewSessionCookie := false
	sessionCookieName := setting.SessionConfig.CookieName
	for _, c := range resp.Result().Cookies() {
		if c.Name == sessionCookieName {
			hasNewSessionCookie = true
			break
		}
		t.Log("Got cookie", c.Name)
	}

	assert.True(t, hasNewSessionCookie, "Session cookie %q is missing", sessionCookieName)
	assert.Equal(t, "/login/oauth/authorize?redirect_uri=https://translate.example.org", test.RedirectURL(resp))
}

func setupMockOIDCServer() *httptest.Server {
	var mockServer *httptest.Server
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"issuer": "` + strings.TrimSuffix(mockServer.URL, "/") + `",
				"authorization_endpoint": "` + mockServer.URL + `/authorize",
				"token_endpoint": "` + mockServer.URL + `/token",
				"userinfo_endpoint": "` + mockServer.URL + `/userinfo"
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	return mockServer
}

func TestSignInOauthCallbackSyncSSHKeys(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	mockServer := setupMockOIDCServer()
	defer mockServer.Close()

	sourceName := "oidc"
	authPayload := authSourcePayloadOpenIDConnect(sourceName, mockServer.URL+"/")
	authPayload["oauth2_attribute_ssh_public_key"] = "sshpubkey"
	authSource := addAuthSource(t, authPayload)

	userID := "5678"
	user := &user_model.User{
		Name:        "oidc.user",
		Email:       "oidc.user@example.com",
		Passwd:      "oidc.userpassword",
		Type:        user_model.UserTypeIndividual,
		LoginType:   auth_model.OAuth2,
		LoginSource: authSource.ID,
		LoginName:   userID,
		IsActive:    true,
	}
	defer createUser(t.Context(), t, user)()

	for _, tt := range []struct {
		name          string
		rawData       map[string]any
		parsedKeySets []string
	}{
		{
			name: "Add keys",
			rawData: map[string]any{
				"sshpubkey": []any{
					"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINDRDoephkaFELacrNNe2fqAwedhRB1MKOpLEHlPuczO nocomment",
				},
			},
			parsedKeySets: []string{
				"SHA256:X/mW7JUQ8J8yhrKBbZ/pJni8qx7zPA1DTFsi8ftpDwg",
			},
		},
		{
			name: "Update keys",
			rawData: map[string]any{
				"sshpubkey": []any{
					"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMLLMOLFMouSJmzOASKKv178d+7op4utSxcugF9tVVch nocomment",
					"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGyDh9sg1IGQGa0U363wcGXrDlGBhZI3UHvS7we/0d+T nocomment",
				},
			},
			parsedKeySets: []string{
				"SHA256:gsyG4JNmY5XoLBK5lSzuwD3EXcaDBiDKBkqDkpQTH6Q",
				"SHA256:bbEKB1Qpumgk6QrgiN6t/kIvtUZvIQ8rqQBz8yYPzYw",
			},
		},
		{
			name: "Remove keys",
			rawData: map[string]any{
				"sshpubkey": []any{},
			},
			parsedKeySets: []string{},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer mockCompleteUserAuth(func(res http.ResponseWriter, req *http.Request) (goth.User, error) {
				return goth.User{
					Provider: sourceName,
					UserID:   userID,
					Email:    user.Email,
					RawData:  tt.rawData,
				}, nil
			})()

			session := emptyTestSession(t)

			req := NewRequest(t, "GET", fmt.Sprintf("/user/oauth2/%s/callback?code=XYZ&state=XYZ", sourceName))
			resp := session.MakeRequest(t, req, http.StatusSeeOther)
			assert.Equal(t, "/", test.RedirectURL(resp))

			req = NewRequest(t, "GET", "/user/settings/keys")
			resp = session.MakeRequest(t, req, http.StatusOK)

			htmlDoc := NewHTMLParser(t, resp.Body)
			divs := htmlDoc.doc.Find("#keys-ssh .flex-item .flex-item-body:not(:last-child)")

			syncedKeys := make([]string, divs.Length())
			for i := 0; i < divs.Length(); i++ {
				syncedKeys[i] = strings.TrimSpace(divs.Eq(i).Text())
			}

			assert.ElementsMatch(t, tt.parsedKeySets, syncedKeys, "Unequal number of keys")
		})
	}
}

func TestSignUpViaOAuthWithMissingFields(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	// enable auto-creation of accounts via OAuth2
	enableAutoRegistration := setting.OAuth2Client.EnableAutoRegistration
	setting.OAuth2Client.EnableAutoRegistration = true
	defer func() {
		setting.OAuth2Client.EnableAutoRegistration = enableAutoRegistration
	}()

	// OAuth2 authentication source GitLab
	gitlabName := "gitlab"
	addAuthSource(t, authSourcePayloadGitLabCustom(gitlabName))
	userGitLabUserID := "5678"

	// The Goth User returned by the oauth2 integration is missing
	// an email address, so we won't be able to automatically create a local account for it.
	defer mockCompleteUserAuth(func(res http.ResponseWriter, req *http.Request) (goth.User, error) {
		return goth.User{
			Provider: gitlabName,
			UserID:   userGitLabUserID,
		}, nil
	})()
	req := NewRequest(t, "GET", fmt.Sprintf("/user/oauth2/%s/callback?code=XYZ&state=XYZ", gitlabName))
	resp := MakeRequest(t, req, http.StatusSeeOther)
	assert.Equal(t, "/user/link_account", test.RedirectURL(resp))
}

func TestOAuth_GrantApplicationOAuth(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	req := NewRequest(t, "GET", "/login/oauth/authorize?client_id=da7da3ba-9a13-4167-856f-3899de0b0138&redirect_uri=a&response_type=code&state=thestate")
	ctx := loginUser(t, "user4")
	resp := ctx.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	htmlDoc.AssertElement(t, "#authorize-app", true)

	req = NewRequestWithValues(t, "POST", "/login/oauth/grant", map[string]string{
		"_csrf":        htmlDoc.GetCSRF(),
		"client_id":    "da7da3ba-9a13-4167-856f-3899de0b0138",
		"redirect_uri": "a",
		"state":        "thestate",
		"granted":      "false",
	})
	resp = ctx.MakeRequest(t, req, http.StatusSeeOther)
	assert.Contains(t, test.RedirectURL(resp), "error=access_denied&error_description=the+request+is+denied")
}

func TestOAuthIntrospection(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	req := NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     "da7da3ba-9a13-4167-856f-3899de0b0138",
		"client_secret": "4MK8Na6R55smdCY0WuCCumZ6hjRPnGY5saWVRHHjJiA=",
		"redirect_uri":  "a",
		"code":          "authcode",
		"code_verifier": "N1Zo9-8Rfwhkt68r1r29ty8YwIraXR8eh_1Qwxg7yQXsonBt",
	})
	resp := MakeRequest(t, req, http.StatusOK)
	type response struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	parsed := new(response)

	DecodeJSON(t, resp, parsed)
	assert.Greater(t, len(parsed.AccessToken), 10)
	assert.Greater(t, len(parsed.RefreshToken), 10)

	type introspectResponse struct {
		Active   bool   `json:"active"`
		Scope    string `json:"scope,omitempty"`
		Username string `json:"username"`
	}

	// successful request with a valid client_id/client_secret and a valid token
	t.Run("successful request with valid token", func(t *testing.T) {
		req := NewRequestWithValues(t, "POST", "/login/oauth/introspect", map[string]string{
			"token": parsed.AccessToken,
		})
		req.Header.Add("Authorization", "Basic ZGE3ZGEzYmEtOWExMy00MTY3LTg1NmYtMzg5OWRlMGIwMTM4OjRNSzhOYTZSNTVzbWRDWTBXdUNDdW1aNmhqUlBuR1k1c2FXVlJISGpKaUE9")
		resp := MakeRequest(t, req, http.StatusOK)

		introspectParsed := new(introspectResponse)
		DecodeJSON(t, resp, introspectParsed)
		assert.True(t, introspectParsed.Active)
		assert.Equal(t, "user1", introspectParsed.Username)
	})

	// successful request with a valid client_id/client_secret, but an invalid token
	t.Run("successful request with invalid token", func(t *testing.T) {
		req := NewRequestWithValues(t, "POST", "/login/oauth/introspect", map[string]string{
			"token": "xyzzy",
		})
		req.Header.Add("Authorization", "Basic ZGE3ZGEzYmEtOWExMy00MTY3LTg1NmYtMzg5OWRlMGIwMTM4OjRNSzhOYTZSNTVzbWRDWTBXdUNDdW1aNmhqUlBuR1k1c2FXVlJISGpKaUE9")
		resp := MakeRequest(t, req, http.StatusOK)
		introspectParsed := new(introspectResponse)
		DecodeJSON(t, resp, introspectParsed)
		assert.False(t, introspectParsed.Active)
	})

	// unsuccessful request with an invalid client_id/client_secret
	t.Run("unsuccessful request due to invalid basic auth", func(t *testing.T) {
		req := NewRequestWithValues(t, "POST", "/login/oauth/introspect", map[string]string{
			"token": parsed.AccessToken,
		})
		req.Header.Add("Authorization", "Basic ZGE3ZGEzYmEtOWExMy00MTY3LTg1NmYtMzg5OWRlMGIwMTM4OjRNSzhOYTZSNTVzbWRDWTBXdUNDdW1aNmhqUlBuR1k1c2FXVlJISGpK")
		resp := MakeRequest(t, req, http.StatusUnauthorized)
		assert.Contains(t, resp.Body.String(), "no valid authorization")
	})
}

func requireCookieCSRF(t *testing.T, resp http.ResponseWriter) string {
	for _, c := range resp.(*httptest.ResponseRecorder).Result().Cookies() {
		if c.Name == "_csrf" {
			return c.Value
		}
	}
	require.Fail(t, "_csrf not found in cookies")
	return ""
}

func TestOAuth_GrantScopesReadUser(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	appBody := api.CreateOAuth2ApplicationOptions{
		Name: "oauth-provider-scopes-test",
		RedirectURIs: []string{
			"a",
		},
		ConfidentialClient: true,
	}

	req := NewRequestWithJSON(t, "POST", "/api/v1/user/applications/oauth2", &appBody).
		AddBasicAuth(user.Name)
	resp := MakeRequest(t, req, http.StatusCreated)

	var app *api.OAuth2Application
	DecodeJSON(t, resp, &app)

	grant := &auth_model.OAuth2Grant{
		ApplicationID: app.ID,
		UserID:        user.ID,
		Scope:         "openid profile email read:user",
	}

	err := db.Insert(db.DefaultContext, grant)
	require.NoError(t, err)

	assert.Contains(t, grant.Scope, "openid profile email read:user")

	ctx := loginUserWithPasswordRemember(t, user.Name, "password", true)

	authorizeURL := fmt.Sprintf("/login/oauth/authorize?client_id=%s&redirect_uri=a&response_type=code&state=thestate", app.ClientID)
	authorizeReq := NewRequest(t, "GET", authorizeURL)
	authorizeResp := ctx.MakeRequest(t, authorizeReq, http.StatusSeeOther)

	authcode := strings.Split(strings.Split(authorizeResp.Body.String(), "?code=")[1], "&amp")[0]
	grantReq := NewRequestWithValues(t, "POST", "/login/oauth/grant", map[string]string{
		"_csrf":        requireCookieCSRF(t, authorizeResp),
		"client_id":    app.ClientID,
		"redirect_uri": "a",
		"state":        "thestate",
		"granted":      "true",
	})
	grantResp := ctx.MakeRequest(t, grantReq, http.StatusBadRequest)
	assert.NotContains(t, grantResp.Body.String(), forgejo_context.CsrfErrorString)

	accessTokenReq := NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"_csrf":         requireCookieCSRF(t, authorizeResp),
		"grant_type":    "authorization_code",
		"client_id":     app.ClientID,
		"client_secret": app.ClientSecret,
		"redirect_uri":  "a",
		"code":          authcode,
	})
	accessTokenResp := ctx.MakeRequest(t, accessTokenReq, 200)
	type response struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	parsed := new(response)

	require.NoError(t, json.Unmarshal(accessTokenResp.Body.Bytes(), parsed))
	userReq := NewRequest(t, "GET", "/api/v1/user")
	userReq.SetHeader("Authorization", "Bearer "+parsed.AccessToken)
	userResp := MakeRequest(t, userReq, http.StatusOK)

	// assert.Contains(t, string(userResp.Body.Bytes()), "blah")
	type userResponse struct {
		Login string `json:"login"`
		Email string `json:"email"`
	}

	userParsed := new(userResponse)
	require.NoError(t, json.Unmarshal(userResp.Body.Bytes(), userParsed))
	assert.Contains(t, userParsed.Email, "user2@example.com")
}

func TestOAuth_GrantScopesFailReadRepository(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	appBody := api.CreateOAuth2ApplicationOptions{
		Name: "oauth-provider-scopes-test",
		RedirectURIs: []string{
			"a",
		},
		ConfidentialClient: true,
	}

	req := NewRequestWithJSON(t, "POST", "/api/v1/user/applications/oauth2", &appBody).
		AddBasicAuth(user.Name)
	resp := MakeRequest(t, req, http.StatusCreated)

	var app *api.OAuth2Application
	DecodeJSON(t, resp, &app)

	grant := &auth_model.OAuth2Grant{
		ApplicationID: app.ID,
		UserID:        user.ID,
		Scope:         "openid profile email read:user",
	}

	err := db.Insert(db.DefaultContext, grant)
	require.NoError(t, err)

	assert.Contains(t, grant.Scope, "openid profile email read:user")

	ctx := loginUserWithPasswordRemember(t, user.Name, "password", true)

	authorizeURL := fmt.Sprintf("/login/oauth/authorize?client_id=%s&redirect_uri=a&response_type=code&state=thestate", app.ClientID)
	authorizeReq := NewRequest(t, "GET", authorizeURL)
	authorizeResp := ctx.MakeRequest(t, authorizeReq, http.StatusSeeOther)

	authcode := strings.Split(strings.Split(authorizeResp.Body.String(), "?code=")[1], "&amp")[0]
	grantReq := NewRequestWithValues(t, "POST", "/login/oauth/grant", map[string]string{
		"_csrf":        requireCookieCSRF(t, authorizeResp),
		"client_id":    app.ClientID,
		"redirect_uri": "a",
		"state":        "thestate",
		"granted":      "true",
	})
	grantResp := ctx.MakeRequest(t, grantReq, http.StatusBadRequest)
	assert.NotContains(t, grantResp.Body.String(), forgejo_context.CsrfErrorString)

	accessTokenReq := NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"_csrf":         requireCookieCSRF(t, authorizeResp),
		"grant_type":    "authorization_code",
		"client_id":     app.ClientID,
		"client_secret": app.ClientSecret,
		"redirect_uri":  "a",
		"code":          authcode,
	})
	accessTokenResp := ctx.MakeRequest(t, accessTokenReq, http.StatusOK)
	type response struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	parsed := new(response)

	require.NoError(t, json.Unmarshal(accessTokenResp.Body.Bytes(), parsed))
	userReq := NewRequest(t, "GET", "/api/v1/users/user2/repos")
	userReq.SetHeader("Authorization", "Bearer "+parsed.AccessToken)
	userResp := MakeRequest(t, userReq, http.StatusForbidden)

	type userResponse struct {
		Message string `json:"message"`
	}

	userParsed := new(userResponse)
	require.NoError(t, json.Unmarshal(userResp.Body.Bytes(), userParsed))
	assert.Contains(t, userParsed.Message, "token does not have at least one of required scope(s): [read:repository]")
}

func TestOAuth_GrantScopesReadRepository(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	appBody := api.CreateOAuth2ApplicationOptions{
		Name: "oauth-provider-scopes-test",
		RedirectURIs: []string{
			"a",
		},
		ConfidentialClient: true,
	}

	req := NewRequestWithJSON(t, "POST", "/api/v1/user/applications/oauth2", &appBody).
		AddBasicAuth(user.Name)
	resp := MakeRequest(t, req, http.StatusCreated)

	var app *api.OAuth2Application
	DecodeJSON(t, resp, &app)

	grant := &auth_model.OAuth2Grant{
		ApplicationID: app.ID,
		UserID:        user.ID,
		Scope:         "openid profile email read:user read:repository",
	}

	err := db.Insert(db.DefaultContext, grant)
	require.NoError(t, err)

	assert.Contains(t, grant.Scope, "openid profile email read:user read:repository")

	ctx := loginUserWithPasswordRemember(t, user.Name, "password", true)

	authorizeURL := fmt.Sprintf("/login/oauth/authorize?client_id=%s&redirect_uri=a&response_type=code&state=thestate", app.ClientID)
	authorizeReq := NewRequest(t, "GET", authorizeURL)
	authorizeResp := ctx.MakeRequest(t, authorizeReq, http.StatusSeeOther)

	authcode := strings.Split(strings.Split(authorizeResp.Body.String(), "?code=")[1], "&amp")[0]
	grantReq := NewRequestWithValues(t, "POST", "/login/oauth/grant", map[string]string{
		"_csrf":        requireCookieCSRF(t, authorizeResp),
		"client_id":    app.ClientID,
		"redirect_uri": "a",
		"state":        "thestate",
		"granted":      "true",
	})
	grantResp := ctx.MakeRequest(t, grantReq, http.StatusBadRequest)
	assert.NotContains(t, grantResp.Body.String(), forgejo_context.CsrfErrorString)

	accessTokenReq := NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"_csrf":         requireCookieCSRF(t, authorizeResp),
		"grant_type":    "authorization_code",
		"client_id":     app.ClientID,
		"client_secret": app.ClientSecret,
		"redirect_uri":  "a",
		"code":          authcode,
	})
	accessTokenResp := ctx.MakeRequest(t, accessTokenReq, http.StatusOK)
	type response struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	parsed := new(response)

	require.NoError(t, json.Unmarshal(accessTokenResp.Body.Bytes(), parsed))
	userReq := NewRequest(t, "GET", "/api/v1/users/user2/repos")
	userReq.SetHeader("Authorization", "Bearer "+parsed.AccessToken)
	userResp := MakeRequest(t, userReq, http.StatusOK)

	type repos struct {
		FullRepoName string `json:"full_name"`
	}
	var userResponse []*repos
	require.NoError(t, json.Unmarshal(userResp.Body.Bytes(), &userResponse))
	if assert.NotEmpty(t, userResponse) {
		assert.Contains(t, userResponse[0].FullRepoName, "user2/repo1")
	}
}

func TestOAuth_GrantScopesReadPrivateGroups(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	// setting.OAuth2.EnableAdditionalGrantScopes = true
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user5"})

	appBody := api.CreateOAuth2ApplicationOptions{
		Name: "oauth-provider-scopes-test",
		RedirectURIs: []string{
			"a",
		},
		ConfidentialClient: true,
	}

	appReq := NewRequestWithJSON(t, "POST", "/api/v1/user/applications/oauth2", &appBody).
		AddBasicAuth(user.Name)
	appResp := MakeRequest(t, appReq, http.StatusCreated)

	var app *api.OAuth2Application
	DecodeJSON(t, appResp, &app)

	grant := &auth_model.OAuth2Grant{
		ApplicationID: app.ID,
		UserID:        user.ID,
		Scope:         "openid profile email groups read:user",
	}

	err := db.Insert(db.DefaultContext, grant)
	require.NoError(t, err)

	assert.Contains(t, grant.Scope, "openid profile email groups read:user")

	ctx := loginUserWithPasswordRemember(t, user.Name, "password", true)

	authorizeURL := fmt.Sprintf("/login/oauth/authorize?client_id=%s&redirect_uri=a&response_type=code&state=thestate", app.ClientID)
	authorizeReq := NewRequest(t, "GET", authorizeURL)
	authorizeResp := ctx.MakeRequest(t, authorizeReq, http.StatusSeeOther)

	authcode := strings.Split(strings.Split(authorizeResp.Body.String(), "?code=")[1], "&amp")[0]
	grantReq := NewRequestWithValues(t, "POST", "/login/oauth/grant", map[string]string{
		"_csrf":        requireCookieCSRF(t, authorizeResp),
		"client_id":    app.ClientID,
		"redirect_uri": "a",
		"state":        "thestate",
		"granted":      "true",
	})
	grantResp := ctx.MakeRequest(t, grantReq, http.StatusBadRequest)
	assert.NotContains(t, grantResp.Body.String(), forgejo_context.CsrfErrorString)

	accessTokenReq := NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"_csrf":         requireCookieCSRF(t, authorizeResp),
		"grant_type":    "authorization_code",
		"client_id":     app.ClientID,
		"client_secret": app.ClientSecret,
		"redirect_uri":  "a",
		"code":          authcode,
	})
	accessTokenResp := ctx.MakeRequest(t, accessTokenReq, http.StatusOK)
	type response struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token,omitempty"`
	}
	parsed := new(response)
	require.NoError(t, json.Unmarshal(accessTokenResp.Body.Bytes(), parsed))
	parts := strings.Split(parsed.IDToken, ".")

	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	type IDTokenClaims struct {
		Groups []string `json:"groups"`
	}

	claims := new(IDTokenClaims)
	require.NoError(t, json.Unmarshal(payload, claims))
	for _, group := range []string{"limited_org36", "limited_org36:team20writepackage", "org6", "org6:owners", "org7", "org7:owners", "privated_org", "privated_org:team14writeauth"} {
		assert.Contains(t, claims.Groups, group)
	}
}

func TestOAuth_GrantScopesReadOnlyPublicGroups(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	setting.OAuth2.EnableAdditionalGrantScopes = true
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user5"})

	appBody := api.CreateOAuth2ApplicationOptions{
		Name: "oauth-provider-scopes-test",
		RedirectURIs: []string{
			"a",
		},
		ConfidentialClient: true,
	}

	appReq := NewRequestWithJSON(t, "POST", "/api/v1/user/applications/oauth2", &appBody).
		AddBasicAuth(user.Name)
	appResp := MakeRequest(t, appReq, http.StatusCreated)

	var app *api.OAuth2Application
	DecodeJSON(t, appResp, &app)

	grant := &auth_model.OAuth2Grant{
		ApplicationID: app.ID,
		UserID:        user.ID,
		Scope:         "openid profile email groups read:user",
	}

	err := db.Insert(db.DefaultContext, grant)
	require.NoError(t, err)

	assert.Contains(t, grant.Scope, "openid profile email groups read:user")

	ctx := loginUserWithPasswordRemember(t, user.Name, "password", true)

	authorizeURL := fmt.Sprintf("/login/oauth/authorize?client_id=%s&redirect_uri=a&response_type=code&state=thestate", app.ClientID)
	authorizeReq := NewRequest(t, "GET", authorizeURL)
	authorizeResp := ctx.MakeRequest(t, authorizeReq, http.StatusSeeOther)

	authcode := strings.Split(strings.Split(authorizeResp.Body.String(), "?code=")[1], "&amp")[0]
	grantReq := NewRequestWithValues(t, "POST", "/login/oauth/grant", map[string]string{
		"_csrf":        requireCookieCSRF(t, authorizeResp),
		"client_id":    app.ClientID,
		"redirect_uri": "a",
		"state":        "thestate",
		"granted":      "true",
	})
	grantResp := ctx.MakeRequest(t, grantReq, http.StatusBadRequest)
	assert.NotContains(t, grantResp.Body.String(), forgejo_context.CsrfErrorString)

	accessTokenReq := NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"_csrf":         requireCookieCSRF(t, authorizeResp),
		"grant_type":    "authorization_code",
		"client_id":     app.ClientID,
		"client_secret": app.ClientSecret,
		"redirect_uri":  "a",
		"code":          authcode,
	})
	accessTokenResp := ctx.MakeRequest(t, accessTokenReq, http.StatusOK)
	type response struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token,omitempty"`
	}
	parsed := new(response)
	require.NoError(t, json.Unmarshal(accessTokenResp.Body.Bytes(), parsed))
	parts := strings.Split(parsed.IDToken, ".")

	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	type IDTokenClaims struct {
		Groups []string `json:"groups"`
	}

	claims := new(IDTokenClaims)
	require.NoError(t, json.Unmarshal(payload, claims))
	for _, privOrg := range []string{"org7", "org7:owners", "privated_org", "privated_org:team14writeauth"} {
		assert.NotContains(t, claims.Groups, privOrg)
	}

	userReq := NewRequest(t, "GET", "/login/oauth/userinfo")
	userReq.SetHeader("Authorization", "Bearer "+parsed.AccessToken)
	userResp := MakeRequest(t, userReq, http.StatusOK)

	type userinfo struct {
		Groups []string `json:"groups"`
	}
	parsedUserInfo := new(userinfo)
	require.NoError(t, json.Unmarshal(userResp.Body.Bytes(), parsedUserInfo))

	for _, privOrg := range []string{"org7", "org7:owners", "privated_org", "privated_org:team14writeauth"} {
		assert.NotContains(t, parsedUserInfo.Groups, privOrg)
	}
}

func TestOAuth_GrantScopesReadPublicGroupsWithTheReadScope(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	setting.OAuth2.EnableAdditionalGrantScopes = true
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user5"})

	appBody := api.CreateOAuth2ApplicationOptions{
		Name: "oauth-provider-scopes-test",
		RedirectURIs: []string{
			"a",
		},
		ConfidentialClient: true,
	}

	appReq := NewRequestWithJSON(t, "POST", "/api/v1/user/applications/oauth2", &appBody).
		AddBasicAuth(user.Name)
	appResp := MakeRequest(t, appReq, http.StatusCreated)

	var app *api.OAuth2Application
	DecodeJSON(t, appResp, &app)

	grant := &auth_model.OAuth2Grant{
		ApplicationID: app.ID,
		UserID:        user.ID,
		Scope:         "openid profile email groups read:user read:organization",
	}

	err := db.Insert(db.DefaultContext, grant)
	require.NoError(t, err)

	assert.Contains(t, grant.Scope, "openid profile email groups read:user read:organization")

	ctx := loginUserWithPasswordRemember(t, user.Name, "password", true)

	authorizeURL := fmt.Sprintf("/login/oauth/authorize?client_id=%s&redirect_uri=a&response_type=code&state=thestate", app.ClientID)
	authorizeReq := NewRequest(t, "GET", authorizeURL)
	authorizeResp := ctx.MakeRequest(t, authorizeReq, http.StatusSeeOther)

	authcode := strings.Split(strings.Split(authorizeResp.Body.String(), "?code=")[1], "&amp")[0]
	grantReq := NewRequestWithValues(t, "POST", "/login/oauth/grant", map[string]string{
		"_csrf":        requireCookieCSRF(t, authorizeResp),
		"client_id":    app.ClientID,
		"redirect_uri": "a",
		"state":        "thestate",
		"granted":      "true",
	})
	grantResp := ctx.MakeRequest(t, grantReq, http.StatusBadRequest)
	assert.NotContains(t, grantResp.Body.String(), forgejo_context.CsrfErrorString)

	accessTokenReq := NewRequestWithValues(t, "POST", "/login/oauth/access_token", map[string]string{
		"_csrf":         requireCookieCSRF(t, authorizeResp),
		"grant_type":    "authorization_code",
		"client_id":     app.ClientID,
		"client_secret": app.ClientSecret,
		"redirect_uri":  "a",
		"code":          authcode,
	})
	accessTokenResp := ctx.MakeRequest(t, accessTokenReq, http.StatusOK)
	type response struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token,omitempty"`
	}
	parsed := new(response)
	require.NoError(t, json.Unmarshal(accessTokenResp.Body.Bytes(), parsed))
	parts := strings.Split(parsed.IDToken, ".")

	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	type IDTokenClaims struct {
		Groups []string `json:"groups"`
	}

	claims := new(IDTokenClaims)
	require.NoError(t, json.Unmarshal(payload, claims))
	for _, privOrg := range []string{"org7", "org7:owners", "privated_org", "privated_org:team14writeauth"} {
		assert.Contains(t, claims.Groups, privOrg)
	}

	userReq := NewRequest(t, "GET", "/login/oauth/userinfo")
	userReq.SetHeader("Authorization", "Bearer "+parsed.AccessToken)
	userResp := MakeRequest(t, userReq, http.StatusOK)

	type userinfo struct {
		Groups []string `json:"groups"`
	}
	parsedUserInfo := new(userinfo)
	require.NoError(t, json.Unmarshal(userResp.Body.Bytes(), parsedUserInfo))
	for _, privOrg := range []string{"org7", "org7:owners", "privated_org", "privated_org:team14writeauth"} {
		assert.Contains(t, parsedUserInfo.Groups, privOrg)
	}
}

func TestSignUpViaOAuthDefaultRestricted(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&setting.OAuth2Client.EnableAutoRegistration, true)()
	defer test.MockVariableValue(&setting.Service.DefaultUserIsRestricted, true)()

	gitlabName := "gitlab"
	addAuthSource(t, authSourcePayloadGitLabCustom(gitlabName))
	userGitLabUserID := "BB(5)=47176870"

	defer mockCompleteUserAuth(func(res http.ResponseWriter, req *http.Request) (goth.User, error) {
		return goth.User{
			Provider: gitlabName,
			UserID:   userGitLabUserID,
			Name:     "gitlab-user",
			NickName: "gitlab-user",
			Email:    "gitlab@example.com",
		}, nil
	})()
	req := NewRequest(t, "GET", fmt.Sprintf("/user/oauth2/%s/callback?code=XYZ&state=XYZ", gitlabName))
	resp := MakeRequest(t, req, http.StatusSeeOther)
	assert.Equal(t, "/", test.RedirectURL(resp))

	unittest.AssertExistsIf(t, true, &user_model.User{Name: "gitlab-user"}, "is_restricted = true")
}

func TestSignUpViaOAuthLinking2FA(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&setting.OAuth2Client.EnableAutoRegistration, true)()
	defer test.MockVariableValue(&setting.OAuth2Client.AccountLinking, setting.OAuth2AccountLinkingAuto)()

	// Fake that user 2 is enrolled into WebAuthn.
	t.Cleanup(func() {
		unittest.AssertSuccessfulDelete(t, &auth_model.WebAuthnCredential{UserID: 2})
	})
	unittest.AssertSuccessfulInsert(t, &auth_model.WebAuthnCredential{UserID: 2})

	gitlabName := "gitlab"
	addAuthSource(t, authSourcePayloadGitLabCustom(gitlabName))
	userGitLabUserID := "BB(4)=107"

	defer mockCompleteUserAuth(func(res http.ResponseWriter, req *http.Request) (goth.User, error) {
		return goth.User{
			Provider: gitlabName,
			UserID:   userGitLabUserID,
			NickName: "user2",
			Email:    "user2@example.com",
		}, nil
	})()
	req := NewRequest(t, "GET", fmt.Sprintf("/user/oauth2/%s/callback?code=XYZ&state=XYZ", gitlabName))
	resp := MakeRequest(t, req, http.StatusSeeOther)

	// Make sure the user has to go through 2FA after linking.
	assert.Equal(t, "/user/webauthn", test.RedirectURL(resp))
}

func TestSignUpViaOAuth2FA(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&setting.OAuth2Client.EnableAutoRegistration, true)()
	defer test.MockVariableValue(&setting.OAuth2Client.AccountLinking, setting.OAuth2AccountLinkingAuto)()

	gitlabName := "gitlab"
	addAuthSource(t, authSourcePayloadGitLabCustom(gitlabName))
	userGitLabUserID := "BB(3)=21"

	defer mockCompleteUserAuth(func(res http.ResponseWriter, req *http.Request) (goth.User, error) {
		return goth.User{
			Provider: gitlabName,
			UserID:   userGitLabUserID,
			NickName: "user2",
			Email:    "user2@example.com",
		}, nil
	})()
	req := NewRequest(t, "GET", fmt.Sprintf("/user/oauth2/%s/callback?code=XYZ&state=XYZ", gitlabName))
	resp := MakeRequest(t, req, http.StatusSeeOther)

	// Make sure the user can login normally and is linked.
	assert.Equal(t, "/", test.RedirectURL(resp))

	// Fake that user 2 is enrolled into WebAuthn.
	t.Cleanup(func() {
		unittest.AssertSuccessfulDelete(t, &auth_model.WebAuthnCredential{UserID: 2})
	})
	unittest.AssertSuccessfulInsert(t, &auth_model.WebAuthnCredential{UserID: 2})

	req = NewRequest(t, "GET", fmt.Sprintf("/user/oauth2/%s/callback?code=XYZ&state=XYZ", gitlabName))
	resp = MakeRequest(t, req, http.StatusSeeOther)

	// Make sure user has to go through 2FA.
	assert.Equal(t, "/user/webauthn", test.RedirectURL(resp))
}
