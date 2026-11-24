// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package method

import (
	"fmt"
	"net/http"

	user_model "forgejo.org/models/user"
	"forgejo.org/modules/log"
	"forgejo.org/services/auth"
)

// Ensure the struct implements the interface.
var (
	_ auth.Method = &Session{}
)

// Session checks if there is a user uid stored in the session and returns the user
// object for that uid.
type Session struct{}

// Verify checks if there is a user uid stored in the session and returns the user
// object for that uid.
// Returns nil if there is no user uid stored in the session.
func (s *Session) Verify(req *http.Request, w http.ResponseWriter, sess auth.SessionStore) auth.MethodOutput {
	if sess == nil {
		return &auth.AuthenticationNotAttempted{InteractiveReauthenticationPossible: true}
	}

	// Get user ID
	uid := sess.Get("uid")
	if uid == nil {
		return &auth.AuthenticationNotAttempted{InteractiveReauthenticationPossible: true}
	}
	log.Trace("Session Authorization: Found user[%d]", uid)

	id, ok := uid.(int64)
	if !ok {
		return &auth.AuthenticationNotAttempted{InteractiveReauthenticationPossible: true}
	}

	// Get user object
	user, err := user_model.GetUserByID(req.Context(), id)
	if err != nil {
		if !user_model.IsErrUserNotExist(err) {
			// Return the err as-is to keep current signed-in session, in case the err is something like context.Canceled. Otherwise non-existing user (nil, nil) will make the caller clear the signed-in session.
			return &auth.AuthenticationError{Error: fmt.Errorf("session auth GetUserByID: %w", err)}
		}
		return &auth.AuthenticationAttemptedIncorrectCredential{Error: err}
	}

	gen, _ := sess.Get("sessionGeneration").(int64)
	if gen != user.SessionGeneration {
		log.Trace("Session Authorization: Expired session generation for user %-v (got %d, want %d)", user, gen, user.SessionGeneration)
		return &auth.AuthenticationNotAttempted{InteractiveReauthenticationPossible: true}
	}

	log.Trace("Session Authorization: Logged in user %-v", user)
	return &auth.AuthenticationSuccess{Result: &sessionAuthenticationResult{user: user}}
}
