// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package method

import (
	"net/http"
	"net/http/httptest"
	"testing"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/timeutil"
	"forgejo.org/services/auth"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mapSessionStore map[any]any

func (m mapSessionStore) Get(k any) any      { return m[k] }
func (m mapSessionStore) Set(k, v any) error { m[k] = v; return nil }
func (m mapSessionStore) Delete(k any) error { delete(m, k); return nil }

func TestSessionVerifyAndRevoke(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	otherUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})

	_, _, err := auth_model.GenerateAuthToken(db.DefaultContext, user.ID, optional.None[int64](), timeutil.TimeStampNow().Add(3600), auth_model.LongTermAuthorization)
	require.NoError(t, err)
	_, _, err = auth_model.GenerateAuthToken(db.DefaultContext, otherUser.ID, optional.None[int64](), timeutil.TimeStampNow().Add(3600), auth_model.LongTermAuthorization)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp := httptest.NewRecorder()
	sessAuth := &Session{}

	sess := mapSessionStore{
		"uid":               user.ID,
		"sessionGeneration": user.SessionGeneration,
	}

	output := sessAuth.Verify(req, resp, sess)
	success, ok := output.(*auth.AuthenticationSuccess)
	require.True(t, ok)
	assert.Equal(t, user.ID, success.Result.User().ID)

	// Revoking sessions bumps the user's generation and deletes their auth tokens.
	require.NoError(t, user_model.RevokeUserSessions(db.DefaultContext, user))
	assert.EqualValues(t, 1, user.SessionGeneration)
	unittest.AssertNotExistsBean(t, &auth_model.AuthorizationToken{UID: user.ID})
	unittest.AssertExistsAndLoadBean(t, &auth_model.AuthorizationToken{UID: otherUser.ID})

	// The old session generation is no longer accepted.
	output = sessAuth.Verify(req, resp, sess)
	notAttempted, ok := output.(*auth.AuthenticationNotAttempted)
	require.True(t, ok)
	assert.True(t, notAttempted.InteractiveReauthenticationPossible)

	// A session with the updated generation succeeds.
	sess["sessionGeneration"] = user.SessionGeneration
	output = sessAuth.Verify(req, resp, sess)
	success, ok = output.(*auth.AuthenticationSuccess)
	require.True(t, ok)
	assert.Equal(t, user.ID, success.Result.User().ID)
}
