// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth_test

import (
	"slices"
	"testing"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	"forgejo.org/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOAuth2Application_GenerateClientSecret(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	app := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{ID: 1})
	secret, err := app.GenerateClientSecret(db.DefaultContext)
	require.NoError(t, err)
	assert.NotEmpty(t, secret)
	unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{ID: 1, ClientSecret: app.ClientSecret})
}

func BenchmarkOAuth2Application_GenerateClientSecret(b *testing.B) {
	require.NoError(b, unittest.PrepareTestDatabase())
	app := unittest.AssertExistsAndLoadBean(b, &auth_model.OAuth2Application{ID: 1})
	for i := 0; i < b.N; i++ {
		_, _ = app.GenerateClientSecret(db.DefaultContext)
	}
}

func TestOAuth2Application_ContainsRedirectURI(t *testing.T) {
	app := &auth_model.OAuth2Application{
		RedirectURIs: []string{"a", "b", "c"},
	}
	assert.True(t, app.ContainsRedirectURI("a"))
	assert.True(t, app.ContainsRedirectURI("b"))
	assert.True(t, app.ContainsRedirectURI("c"))
	assert.False(t, app.ContainsRedirectURI("d"))
}

func TestOAuth2Application_ContainsRedirectURI_WithPort(t *testing.T) {
	app := &auth_model.OAuth2Application{
		RedirectURIs:       []string{"http://127.0.0.1/", "http://::1/", "http://192.168.0.1/", "http://intranet/", "https://127.0.0.1/"},
		ConfidentialClient: false,
	}

	// http loopback uris should ignore port
	// https://datatracker.ietf.org/doc/html/rfc8252#section-7.3
	assert.True(t, app.ContainsRedirectURI("http://127.0.0.1:3456/"))
	assert.True(t, app.ContainsRedirectURI("http://127.0.0.1/"))
	assert.True(t, app.ContainsRedirectURI("http://[::1]:3456/"))

	// not http
	assert.False(t, app.ContainsRedirectURI("https://127.0.0.1:3456/"))
	// not loopback
	assert.False(t, app.ContainsRedirectURI("http://192.168.0.1:9954/"))
	assert.False(t, app.ContainsRedirectURI("http://intranet:3456/"))
	// unparsable
	assert.False(t, app.ContainsRedirectURI(":"))
}

func TestOAuth2Application_ContainsRedirect_Slash(t *testing.T) {
	app := &auth_model.OAuth2Application{RedirectURIs: []string{"http://127.0.0.1"}}
	assert.True(t, app.ContainsRedirectURI("http://127.0.0.1"))
	assert.True(t, app.ContainsRedirectURI("http://127.0.0.1/"))
	assert.False(t, app.ContainsRedirectURI("http://127.0.0.1/other"))

	app = &auth_model.OAuth2Application{RedirectURIs: []string{"http://127.0.0.1/"}}
	assert.True(t, app.ContainsRedirectURI("http://127.0.0.1"))
	assert.True(t, app.ContainsRedirectURI("http://127.0.0.1/"))
	assert.False(t, app.ContainsRedirectURI("http://127.0.0.1/other"))
}

func TestOAuth2Application_ContainsRedirect_Normalization(t *testing.T) {
	app := &auth_model.OAuth2Application{RedirectURIs: []string{"https://website.com"}}
	assert.True(t, app.ContainsRedirectURI("https://website.com"))
	assert.True(t, app.ContainsRedirectURI("https://webSITE.com"))  // ascii uppercase I
	assert.False(t, app.ContainsRedirectURI("https://websİte.com")) // U+0130 as I, Latin Capital Letter I with Dot Above
}

func TestOAuth2Application_ValidateClientSecret(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	app := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{ID: 1})
	secret, err := app.GenerateClientSecret(db.DefaultContext)
	require.NoError(t, err)
	assert.True(t, app.ValidateClientSecret([]byte(secret)))
	assert.False(t, app.ValidateClientSecret([]byte("fewijfowejgfiowjeoifew")))
}

func TestGetOAuth2ApplicationByClientID(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	app, err := auth_model.GetOAuth2ApplicationByClientID(db.DefaultContext, "da7da3ba-9a13-4167-856f-3899de0b0138")
	require.NoError(t, err)
	assert.Equal(t, "da7da3ba-9a13-4167-856f-3899de0b0138", app.ClientID)

	app, err = auth_model.GetOAuth2ApplicationByClientID(db.DefaultContext, "invalid client id")
	require.Error(t, err)
	assert.Nil(t, app)
}

func TestCreateOAuth2Application(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	app, err := auth_model.CreateOAuth2Application(db.DefaultContext, auth_model.CreateOAuth2ApplicationOptions{Name: "newapp", UserID: 1})
	require.NoError(t, err)
	assert.Equal(t, "newapp", app.Name)
	assert.Len(t, app.ClientID, 36)
	unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{Name: "newapp"})
}

func TestOAuth2Application_TableName(t *testing.T) {
	assert.Equal(t, "oauth2_application", new(auth_model.OAuth2Application).TableName())
}

func TestOAuth2Application_GetGrantByUserID(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	app := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{ID: 1})
	grant, err := app.GetGrantByUserID(db.DefaultContext, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), grant.UserID)

	grant, err = app.GetGrantByUserID(db.DefaultContext, 34923458)
	require.NoError(t, err)
	assert.Nil(t, grant)
}

func TestOAuth2Application_CreateGrant(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	app := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{ID: 1})
	grant, err := app.CreateGrant(db.DefaultContext, 2, "")
	require.NoError(t, err)
	assert.NotNil(t, grant)
	assert.Equal(t, int64(2), grant.UserID)
	assert.Equal(t, int64(1), grant.ApplicationID)
	assert.Empty(t, grant.Scope)
}

//////////////////// Grant

func TestGetOAuth2GrantByID(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	grant, err := auth_model.GetOAuth2GrantByID(db.DefaultContext, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), grant.ID)

	grant, err = auth_model.GetOAuth2GrantByID(db.DefaultContext, 34923458)
	require.NoError(t, err)
	assert.Nil(t, grant)
}

func TestOAuth2Grant_IncreaseCounter(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	grant := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: 1, Counter: 1})

	// First increment succeeds
	require.NoError(t, grant.IncreaseCounter(db.DefaultContext))
	assert.Equal(t, int64(2), grant.Counter)
	unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: 1, Counter: 2})

	// Simulate a stale grant (counter still 1): must fail (concurrent replay)
	grant.Counter = 1
	require.Error(t, grant.IncreaseCounter(db.DefaultContext), "stale counter must be rejected")

	// Counter in DB should be unchanged
	unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: 1, Counter: 2})
}

func TestOAuth2Grant_ScopeContains(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	grant := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: 1, Scope: "openid profile"})
	assert.True(t, grant.ScopeContains("openid"))
	assert.True(t, grant.ScopeContains("profile"))
	assert.False(t, grant.ScopeContains("profil"))
	assert.False(t, grant.ScopeContains("profile2"))
}

func TestOAuth2Grant_GenerateNewAuthorizationCode(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	grant := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: 1})
	code, err := grant.GenerateNewAuthorizationCode(db.DefaultContext, "https://example2.com/callback", "CjvyTLSdR47G5zYenDA-eDWW4lRrO8yvjcWwbD_deOg", "S256")
	require.NoError(t, err)
	assert.NotNil(t, code)
	assert.Greater(t, len(code.Code), 32) // secret length > 32
}

func TestOAuth2Grant_TableName(t *testing.T) {
	assert.Equal(t, "oauth2_grant", new(auth_model.OAuth2Grant).TableName())
}

func TestGetOAuth2GrantsByUserID(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	result, err := auth_model.GetOAuth2GrantsByUserID(db.DefaultContext, 1)
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, int64(1), result[0].ID)
	assert.Equal(t, result[0].ApplicationID, result[0].Application.ID)

	result, err = auth_model.GetOAuth2GrantsByUserID(db.DefaultContext, 34134)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestRevokeOAuth2Grant(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, auth_model.RevokeOAuth2Grant(db.DefaultContext, 1, 1))
	unittest.AssertNotExistsBean(t, &auth_model.OAuth2Grant{ID: 1, UserID: 1})
}

//////////////////// Authorization Code

func TestGetOAuth2AuthorizationByCode(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	code, err := auth_model.GetOAuth2AuthorizationByCode(db.DefaultContext, "authcode")
	require.NoError(t, err)
	assert.NotNil(t, code)
	assert.Equal(t, "authcode", code.Code)
	assert.Equal(t, int64(1), code.ID)

	code, err = auth_model.GetOAuth2AuthorizationByCode(db.DefaultContext, "does not exist")
	require.NoError(t, err)
	assert.Nil(t, code)
}

func TestOAuth2AuthorizationCode_ValidateCodeChallenge(t *testing.T) {
	// test plain
	code := &auth_model.OAuth2AuthorizationCode{
		CodeChallengeMethod: "plain",
		CodeChallenge:       "test123",
	}
	assert.True(t, code.ValidateCodeChallenge("test123"))
	assert.False(t, code.ValidateCodeChallenge("ierwgjoergjio"))

	// test S256
	code = &auth_model.OAuth2AuthorizationCode{
		CodeChallengeMethod: "S256",
		CodeChallenge:       "CjvyTLSdR47G5zYenDA-eDWW4lRrO8yvjcWwbD_deOg",
	}
	assert.True(t, code.ValidateCodeChallenge("N1Zo9-8Rfwhkt68r1r29ty8YwIraXR8eh_1Qwxg7yQXsonBt"))
	assert.False(t, code.ValidateCodeChallenge("wiogjerogorewngoenrgoiuenorg"))

	// test unknown
	code = &auth_model.OAuth2AuthorizationCode{
		CodeChallengeMethod: "monkey",
		CodeChallenge:       "foiwgjioriogeiogjerger",
	}
	assert.False(t, code.ValidateCodeChallenge("foiwgjioriogeiogjerger"))

	// test no PKCE at all (no challenge set, no verifier needed)
	code = &auth_model.OAuth2AuthorizationCode{
		CodeChallengeMethod: "",
		CodeChallenge:       "",
	}
	assert.True(t, code.ValidateCodeChallenge(""))

	// test PKCE required: challenge was set but verifier is empty
	code = &auth_model.OAuth2AuthorizationCode{
		CodeChallengeMethod: "S256",
		CodeChallenge:       "CjvyTLSdR47G5zYenDA-eDWW4lRrO8yvjcWwbD_deOg",
	}
	assert.False(t, code.ValidateCodeChallenge(""), "PKCE required: S256 challenge set but empty verifier must be rejected")

	code = &auth_model.OAuth2AuthorizationCode{
		CodeChallengeMethod: "plain",
		CodeChallenge:       "test123",
	}
	assert.False(t, code.ValidateCodeChallenge(""), "PKCE required: plain challenge set but empty verifier must be rejected")

	// test challenge stored but method empty (malformed: should reject)
	code = &auth_model.OAuth2AuthorizationCode{
		CodeChallengeMethod: "",
		CodeChallenge:       "foierjiogerogerg",
	}
	assert.False(t, code.ValidateCodeChallenge(""), "challenge present with empty method must be rejected")
	assert.False(t, code.ValidateCodeChallenge("foierjiogerogerg"), "challenge present with empty method must be rejected even with matching verifier")
}

func TestOAuth2AuthorizationCode_GenerateRedirectURI(t *testing.T) {
	code := &auth_model.OAuth2AuthorizationCode{
		RedirectURI: "https://example.com/callback",
		Code:        "thecode",
	}

	redirect, err := code.GenerateRedirectURI("thestate")
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/callback?code=thecode&state=thestate", redirect.String())

	redirect, err = code.GenerateRedirectURI("")
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/callback?code=thecode", redirect.String())
}

func TestOAuth2AuthorizationCode_Invalidate(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	code := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2AuthorizationCode{Code: "authcode"})
	require.NoError(t, code.Invalidate(db.DefaultContext))
	unittest.AssertNotExistsBean(t, &auth_model.OAuth2AuthorizationCode{Code: "authcode"})
}

func TestOAuth2AuthorizationCode_Invalidate_DoubleUse(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	code := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2AuthorizationCode{Code: "authcode"})

	// First invalidation should succeed
	require.NoError(t, code.Invalidate(db.DefaultContext))
	unittest.AssertNotExistsBean(t, &auth_model.OAuth2AuthorizationCode{Code: "authcode"})

	// Second invalidation of the same code must fail (replay prevention)
	err := code.Invalidate(db.DefaultContext)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authorization code already used")
}

func TestOAuth2AuthorizationCode_TableName(t *testing.T) {
	assert.Equal(t, "oauth2_authorization_code", new(auth_model.OAuth2AuthorizationCode).TableName())
}

func TestBuiltinApplicationsClientIDs(t *testing.T) {
	clientIDs := auth_model.BuiltinApplicationsClientIDs()
	slices.Sort(clientIDs)
	assert.Equal(t, []string{"9c072a20-61c7-4c4e-b245-4551afd67696", "a4792ccc-144e-407e-86c9-5e7d8d9c3269", "d57cb8c4-630c-4168-8324-ec79935e18d4", "e90ee53c-94e2-48ac-9358-a874fb9e0662"}, clientIDs)
}

func TestOrphanedOAuth2Applications(t *testing.T) {
	defer unittest.OverrideFixtures("models/auth/TestOrphanedOAuth2Applications")()
	require.NoError(t, unittest.PrepareTestDatabase())

	count, err := auth_model.CountOrphanedOAuth2Applications(db.DefaultContext)
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)
	unittest.AssertExistsIf(t, true, &auth_model.OAuth2Application{ID: 1002})

	_, err = auth_model.DeleteOrphanedOAuth2Applications(db.DefaultContext)
	require.NoError(t, err)

	count, err = auth_model.CountOrphanedOAuth2Applications(db.DefaultContext)
	require.NoError(t, err)
	assert.EqualValues(t, 0, count)
	unittest.AssertExistsIf(t, false, &auth_model.OAuth2Application{ID: 1002})
	unittest.AssertExistsIf(t, true, &auth_model.OAuth2Application{ID: 1003})
}
