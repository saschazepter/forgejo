// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package setting

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestSettingRepositoryMapping(t *testing.T) {
	t.Run("Default", func(t *testing.T) {
		cfg, err := NewConfigProviderFromData("")
		require.NoError(t, err)
		require.NoError(t, loadRepositoryFrom(cfg))
		assert.False(t, strings.HasSuffix(RepoRootPath, "git-repos"))
		assert.Equal(t, "bash", ScriptType)
		assert.False(t, Repository.ForcePrivate)
		assert.Equal(t, []string{".txt", ".md", ".markdown", ".mdown", ".mkd", ".livemd", ""}, Repository.Editor.LineWrapExtensions)
		assert.True(t, Repository.Upload.Enabled)
		assert.Equal(t, "tmp/local-repo", Repository.Local.LocalCopyPath)
		assert.Equal(t, []string{"WIP:", "[WIP]"}, Repository.PullRequest.WorkInProgressPrefixes)
		assert.Equal(t, 3, Repository.Issue.MaxPinned)
		assert.Empty(t, Repository.Release.AllowedTypes)
		assert.Equal(t, "default", Repository.Signing.SigningKey)
	})
	t.Run("Specified", func(t *testing.T) {
		cfg, err := NewConfigProviderFromData(`
[repository]
ROOT = git-repos
SCRIPT_TYPE = sh
FORCE_PRIVATE = true
[repository.editor]
LINE_WRAP_EXTENSIONS = .txt
[repository.upload]
ENABLED = false
[repository.local]
LOCAL_COPY_PATH = tmp/repo-local
[repository.pull-request]
WORK_IN_PROGRESS_PREFIXES = wip:,draft:
[repository.issue]
MAX_PINNED = 5
[repository.release]
ALLOWED_TYPES = .zip,text/plain
[repository.signing]
SIGNING_KEY = none
`)
		require.NoError(t, err)
		require.NoError(t, loadRepositoryFrom(cfg))
		assert.True(t, strings.HasSuffix(RepoRootPath, "git-repos"))
		assert.Equal(t, "sh", ScriptType)
		assert.True(t, Repository.ForcePrivate)
		assert.Equal(t, []string{".txt"}, Repository.Editor.LineWrapExtensions)
		assert.False(t, Repository.Upload.Enabled)
		assert.Equal(t, "tmp/repo-local", Repository.Local.LocalCopyPath)
		assert.Equal(t, []string{"wip:", "draft:"}, Repository.PullRequest.WorkInProgressPrefixes)
		assert.Equal(t, 5, Repository.Issue.MaxPinned)
		assert.Equal(t, ".zip,text/plain", Repository.Release.AllowedTypes)
		assert.Equal(t, "none", Repository.Signing.SigningKey)
	})
}

func TestSSHInstanceKey(t *testing.T) {
	t.Run("Default value", func(t *testing.T) {
		cfg, err := NewConfigProviderFromData(`
[repository.signing]
FORMAT = ssh
`)
		require.NoError(t, err)
		require.ErrorContains(t, loadRepositoryFrom(cfg), "missing [repository.signing].SIGNING_KEY for signing format ssh")
	})

	t.Run("No value", func(t *testing.T) {
		cfg, err := NewConfigProviderFromData(`
[repository.signing]
FORMAT = ssh
SIGNING_KEY =
`)
		require.NoError(t, err)
		require.ErrorContains(t, loadRepositoryFrom(cfg), "missing [repository.signing].SIGNING_KEY for signing format ssh")
	})

	t.Run("None value", func(t *testing.T) {
		cfg, err := NewConfigProviderFromData(`
[repository.signing]
FORMAT = ssh
SIGNING_KEY = none
`)
		require.NoError(t, err)
		require.ErrorContains(t, loadRepositoryFrom(cfg), "missing [repository.signing].SIGNING_KEY for signing format ssh")
	})

	t.Run("Key does not exist", func(t *testing.T) {
		cfg, err := NewConfigProviderFromData(`
[repository.signing]
FORMAT = ssh
SIGNING_KEY = /file/does/not/exist
`)
		require.NoError(t, err)
		require.ErrorContains(t, loadRepositoryFrom(cfg), "reading SSH signing key from [repository.signing].SIGNING_KEY failed:")
	})

	t.Run("Not a public SSH key", func(t *testing.T) {
		sshSigningKeyPath, err := filepath.Abs("../../tests/integration/ssh-signing-key")
		require.NoError(t, err)
		iniStr := fmt.Sprintf(`
[repository.signing]
FORMAT = ssh
SIGNING_KEY = %s
`, sshSigningKeyPath)
		cfg, err := NewConfigProviderFromData(iniStr)
		require.NoError(t, err)
		require.ErrorContains(t, loadRepositoryFrom(cfg), "parsing SSH signing key from [repository.signing].SIGNING_KEY failed:")
	})

	t.Run("Normal", func(t *testing.T) {
		sshSigningKeyPath, err := filepath.Abs("../../tests/integration/ssh-signing-key.pub")
		require.NoError(t, err)
		iniStr := fmt.Sprintf(`
[repository.signing]
FORMAT = ssh
SIGNING_KEY = %s
`, sshSigningKeyPath)
		cfg, err := NewConfigProviderFromData(iniStr)
		require.NoError(t, err)
		require.NoError(t, loadRepositoryFrom(cfg))

		assert.NotNil(t, SSHInstanceKey)
		assert.Equal(t, "ssh-ed25519", SSHInstanceKey.Type())
		assert.EqualValues(t, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFeRC8GfFyXtiy0f1E7hLv77BXW7e68tFvIcs8/29YqH\n", ssh.MarshalAuthorizedKey(SSHInstanceKey))
	})
}
