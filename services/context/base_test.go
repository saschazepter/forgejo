// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package context

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"

	"github.com/stretchr/testify/assert"
)

func TestRedirect(t *testing.T) {
	defer test.MockVariableValue(&setting.AppURL, "http://localhost:3000/")()
	req, _ := http.NewRequest("GET", "/", nil)

	cases := []struct {
		url  string
		keep bool
	}{
		{"http://test", false},
		{"https://test", false},
		{"//test", false},
		{"/://test", true},
		{"/test", true},
	}
	for _, c := range cases {
		resp := httptest.NewRecorder()
		b, cleanup := NewBaseContext(resp, req)
		resp.Header().Add("Set-Cookie", (&http.Cookie{Name: setting.SessionConfig.CookieName, Value: "dummy"}).String())
		b.Redirect(c.url)
		cleanup()
		has := resp.Header().Get("Set-Cookie") == "i_like_gitea=dummy"
		assert.Equal(t, c.keep, has, "url = %q", c.url)
		assert.Equal(t, http.StatusSeeOther, resp.Code)
	}

	req, _ = http.NewRequest("GET", "/", nil)
	resp := httptest.NewRecorder()
	req.Header.Add("HX-Request", "true")
	b, cleanup := NewBaseContext(resp, req)
	b.Redirect("/other")
	cleanup()
	assert.Equal(t, "/other", resp.Header().Get("HX-Redirect"))
	assert.Equal(t, http.StatusNoContent, resp.Code)
}

func TestRedirectOptionalStatus(t *testing.T) {
	defer test.MockVariableValue(&setting.AppURL, "http://localhost:3000/")()
	req, _ := http.NewRequest("GET", "/", nil)

	cases := []struct {
		expected int
		actual   int
	}{
		{expected: 303},
		{http.StatusTemporaryRedirect, 307},
		{http.StatusPermanentRedirect, 308},
	}
	for _, c := range cases {
		resp := httptest.NewRecorder()
		b, cleanup := NewBaseContext(resp, req)
		b.Redirect("/", c.actual)
		cleanup()
		assert.Equal(t, c.expected, resp.Code)
	}
}
