// Copyright 2026 The Forgejo Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"net/http"
	"testing"

	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/tests"
)

func TestBasicAuth(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	t.Run("BasicAuthenticationSuccess", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/api/v1/user").
			AddBasicAuth(user.Name)
		MakeRequest(t, req, http.StatusOK)
	})

	t.Run("NonExistentEmail", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/api/v1/user").
			AddBasicAuth("email@example.com")
		MakeRequest(t, req, http.StatusUnauthorized)
	})
}
