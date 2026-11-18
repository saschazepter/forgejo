// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0

package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestKeys(t *testing.T) {
	defer test.MockVariableValue(&setting.InstallLock, true)()
	defer test.MockVariableValue(&setting.InternalToken, "abc123")()

	// Setup the server that processes the request.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/internal/ssh/authorized_keys", r.URL.Path)
		require.NoError(t, r.ParseForm())
		if fp := r.FormValue("content"); fp == "SHA256:cribavuRiVCKotErwXB99ChAJNVt9TqFfeTcldrxQ3I" {
			io.WriteString(w, "# Some authorized key command will be returned here")
		}
	}))
	defer ts.Close()
	defer test.MockVariableValue(&setting.LocalURL, ts.URL+"/")()

	stdout := &bytes.Buffer{}

	app := cli.Command{}
	app.Commands = []*cli.Command{cmdKeys()}
	app.Writer = stdout

	t.Run("Required", func(t *testing.T) {
		t.Cleanup(stdout.Reset)

		err := app.Run(t.Context(), []string{"./forgejo", "keys"})
		require.ErrorContains(t, err, `Required flags "username, type, content" not set`)
	})

	t.Run("Invalid base64", func(t *testing.T) {
		t.Cleanup(stdout.Reset)

		err := app.Run(t.Context(), []string{"./forgejo", "keys", "-u", "git", "-k", "Is base56 contant-time?", "-t", "ssh-unknown-type"})
		require.ErrorContains(t, err, "is not valid base64 encoded content:")
	})

	t.Run("Invalid SSH key input", func(t *testing.T) {
		t.Cleanup(stdout.Reset)

		err := app.Run(t.Context(), []string{"./forgejo", "keys", "-u", "git", "-k", "c3NoLWVkMjU1MTkgICAgICAgZw==", "-t", "ssh-unknown-type"})
		require.ErrorContains(t, err, "key content cannot be parsed as public SSH key: ssh: short read")
	})

	t.Run("Mismatched type", func(t *testing.T) {
		t.Cleanup(stdout.Reset)

		err := app.Run(t.Context(), []string{"./forgejo", "keys", "-u", "git", "-k", "AAAAC3NzaC1lZDI1NTE5AAAAIPdJ4yqPcq36whvYjhwj9OhHbGY/M4RCEDK9c96m0lNb", "-t", "ssh-unknown-type"})
		require.ErrorContains(t, err, `authorized keys key type mismatch: given type "ssh-unknown-type", encoded type "ssh-ed25519"`)

		assert.Empty(t, stdout.String())
	})

	t.Run("Mismatched username", func(t *testing.T) {
		t.Cleanup(stdout.Reset)

		err := app.Run(t.Context(), []string{"./forgejo", "keys", "-u", "frog", "-k", "AAAAC3NzaC1lZDI1NTE5AAAAIPdJ4yqPcq36whvYjhwj9OhHbGY/M4RCEDK9c96m0lNb", "-t", "ssh-ed25519"})
		require.NoError(t, err)

		assert.Empty(t, stdout.String())
	})

	t.Run("Normal", func(t *testing.T) {
		t.Cleanup(stdout.Reset)

		err := app.Run(t.Context(), []string{"./forgejo", "keys", "-u", "git", "-k", "AAAAC3NzaC1lZDI1NTE5AAAAIPdJ4yqPcq36whvYjhwj9OhHbGY/M4RCEDK9c96m0lNb", "-t", "ssh-ed25519"})
		require.NoError(t, err)

		assert.Equal(t, "# Some authorized key command will be returned here\n", stdout.String())
	})
}
