// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/modules/util"

	"github.com/hashicorp/go-version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRun(m *testing.M) error {
	gitHomePath, err := os.MkdirTemp(os.TempDir(), "git-home")
	if err != nil {
		return fmt.Errorf("unable to create temp dir: %w", err)
	}
	defer util.RemoveAll(gitHomePath)
	setting.Git.HomePath = gitHomePath

	if err = InitFull(context.Background()); err != nil {
		return fmt.Errorf("failed to call Init: %w", err)
	}

	exitCode := m.Run()
	if exitCode != 0 {
		return fmt.Errorf("run test failed, ExitCode=%d", exitCode)
	}
	return nil
}

func TestMain(m *testing.M) {
	if err := testRun(m); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Test failed: %v", err)
		os.Exit(1)
	}
}

func gitConfigContains(sub string) bool {
	if b, err := os.ReadFile(HomeDir() + "/.gitconfig"); err == nil {
		return strings.Contains(string(b), sub)
	}
	return false
}

func TestGitConfig(t *testing.T) {
	assert.False(t, gitConfigContains("key-a"))

	require.NoError(t, configSetNonExist("test.key-a", "val-a"))
	assert.True(t, gitConfigContains("key-a = val-a"))

	require.NoError(t, configSetNonExist("test.key-a", "val-a-changed"))
	assert.False(t, gitConfigContains("key-a = val-a-changed"))

	require.NoError(t, configSet("test.key-a", "val-a-changed"))
	assert.True(t, gitConfigContains("key-a = val-a-changed"))

	require.NoError(t, configAddNonExist("test.key-b", "val-b"))
	assert.True(t, gitConfigContains("key-b = val-b"))

	require.NoError(t, configAddNonExist("test.key-b", "val-2b"))
	assert.True(t, gitConfigContains("key-b = val-b"))
	assert.True(t, gitConfigContains("key-b = val-2b"))

	require.NoError(t, configUnsetAll("test.key-b", "val-b"))
	assert.False(t, gitConfigContains("key-b = val-b"))
	assert.True(t, gitConfigContains("key-b = val-2b"))

	require.NoError(t, configUnsetAll("test.key-b", "val-2b"))
	assert.False(t, gitConfigContains("key-b = val-2b"))

	require.NoError(t, configSet("test.key-x", "*"))
	assert.True(t, gitConfigContains("key-x = *"))
	require.NoError(t, configSetNonExist("test.key-x", "*"))
	require.NoError(t, configUnsetAll("test.key-x", "*"))
	assert.False(t, gitConfigContains("key-x = *"))
}

func TestSyncConfig(t *testing.T) {
	defer test.MockProtect(&setting.GitConfig)()

	setting.GitConfig.Options["sync-test.cfg-key-a"] = "CfgValA"
	require.NoError(t, syncGitConfig())
	assert.True(t, gitConfigContains("[sync-test]"))
	assert.True(t, gitConfigContains("cfg-key-a = CfgValA"))
}

func TestSyncConfigGPGFormat(t *testing.T) {
	defer test.MockProtect(&setting.GitConfig)()

	t.Run("No format", func(t *testing.T) {
		defer test.MockVariableValue(&setting.Repository.Signing.Format, "")()
		require.NoError(t, syncGitConfig())
		assert.True(t, gitConfigContains("[gpg]"))
		assert.True(t, gitConfigContains("format = openpgp"))
	})

	t.Run("SSH format", func(t *testing.T) {
		if CheckGitVersionAtLeast("2.34.0") != nil {
			t.SkipNow()
		}

		r, err := os.OpenRoot(t.TempDir())
		require.NoError(t, err)
		f, err := r.OpenFile("ssh-keygen", os.O_CREATE|os.O_TRUNC, 0o700)
		require.NoError(t, f.Close())
		require.NoError(t, err)
		t.Setenv("PATH", r.Name())
		defer test.MockVariableValue(&setting.Repository.Signing.Format, "ssh")()

		require.NoError(t, syncGitConfig())
		assert.True(t, gitConfigContains("[gpg]"))
		assert.True(t, gitConfigContains("format = ssh"))

		t.Run("Old version", func(t *testing.T) {
			oldVersion, err := version.NewVersion("2.33.0")
			require.NoError(t, err)
			defer test.MockVariableValue(&GitVersion, oldVersion)()
			require.ErrorContains(t, syncGitConfig(), "ssh signing requires Git >= 2.34.0")
		})

		t.Run("No ssh-keygen binary", func(t *testing.T) {
			require.NoError(t, r.Remove("ssh-keygen"))
			require.ErrorContains(t, syncGitConfig(), "git signing requires a ssh-keygen binary")
		})

		t.Run("Dynamic ssh-keygen binary location", func(t *testing.T) {
			f, err := r.OpenFile("ssh-keygen-2", os.O_CREATE|os.O_TRUNC, 0o700)
			require.NoError(t, f.Close())
			require.NoError(t, err)
			defer test.MockVariableValue(&setting.GitConfig.Options, map[string]string{
				"gpg.ssh.program": "ssh-keygen-2",
			})()
			require.NoError(t, syncGitConfig())
		})
	})

	t.Run("OpenPGP format", func(t *testing.T) {
		defer test.MockVariableValue(&setting.Repository.Signing.Format, "openpgp")()
		require.NoError(t, syncGitConfig())
		assert.True(t, gitConfigContains("[gpg]"))
		assert.True(t, gitConfigContains("format = openpgp"))
	})
}
