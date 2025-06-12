// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"testing"

	"forgejo.org/models/auth"
	"forgejo.org/models/db"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/timeutil"
	"forgejo.org/services/auth/source/oauth2"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createAndParseToken(t *testing.T, grant *auth.OAuth2Grant) *oauth2.OIDCToken {
	signingKey, err := oauth2.CreateJWTSigningKey("HS256", make([]byte, 32))
	require.NoError(t, err)
	assert.NotNil(t, signingKey)

	response, terr := newAccessTokenResponse(db.DefaultContext, grant, signingKey, signingKey)
	assert.Nil(t, terr)
	assert.NotNil(t, response)

	parsedToken, err := jwt.ParseWithClaims(response.IDToken, &oauth2.OIDCToken{}, func(token *jwt.Token) (any, error) {
		assert.NotNil(t, token.Method)
		assert.Equal(t, signingKey.SigningMethod().Alg(), token.Method.Alg())
		return signingKey.VerifyKey(), nil
	})
	require.NoError(t, err)
	assert.True(t, parsedToken.Valid)

	oidcToken, ok := parsedToken.Claims.(*oauth2.OIDCToken)
	assert.True(t, ok)
	assert.NotNil(t, oidcToken)

	return oidcToken
}

func TestNewAccessTokenResponse_OIDCToken(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	grants, err := auth.GetOAuth2GrantsByUserID(db.DefaultContext, 3)
	require.NoError(t, err)
	assert.Len(t, grants, 1)

	// Scopes: openid
	oidcToken := createAndParseToken(t, grants[0])
	assert.Equal(t, "https://try.gitea.io", oidcToken.RegisteredClaims.Issuer)
	assert.Empty(t, oidcToken.Name)
	assert.Empty(t, oidcToken.PreferredUsername)
	assert.Empty(t, oidcToken.Profile)
	assert.Empty(t, oidcToken.Picture)
	assert.Empty(t, oidcToken.Website)
	assert.Empty(t, oidcToken.UpdatedAt)
	assert.Empty(t, oidcToken.Email)
	assert.False(t, oidcToken.EmailVerified)

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	grants, err = auth.GetOAuth2GrantsByUserID(db.DefaultContext, user.ID)
	require.NoError(t, err)
	assert.Len(t, grants, 1)

	// Scopes: openid profile email
	oidcToken = createAndParseToken(t, grants[0])
	assert.Equal(t, "https://try.gitea.io", oidcToken.RegisteredClaims.Issuer)
	assert.Equal(t, "User Five", oidcToken.Name)
	assert.Equal(t, "user5", oidcToken.PreferredUsername)
	assert.Equal(t, "https://try.gitea.io/user5", oidcToken.Profile)
	assert.Equal(t, "https://try.gitea.io/assets/img/avatar_default.png", oidcToken.Picture)
	assert.Empty(t, oidcToken.Website)
	assert.Equal(t, timeutil.TimeStamp(0), oidcToken.UpdatedAt)
	assert.Equal(t, "user5@example.com", oidcToken.Email)
	assert.True(t, oidcToken.EmailVerified)
}

func TestEncodeCodeChallenge(t *testing.T) {
	// test vector from https://datatracker.ietf.org/doc/html/rfc7636#page-18
	codeChallenge, err := encodeCodeChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
	require.NoError(t, err)
	assert.Equal(t, "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", codeChallenge)
}
