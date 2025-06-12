// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	unit_model "forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/indexer/stats"
	files_service "forgejo.org/services/repository/files"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// first entry represents filename
// the following entries define the full file content over time
type FileChanges struct {
	Filename  string
	CommitMsg string
	Versions  []string
}

// put your Git repo declarations in here
// feel free to amend the helper function below or use the raw variant directly
func DeclareGitRepos(t *testing.T) func() {
	cleanupFunctions := []func(){
		newRepo(t, 2, "diff-test", []FileChanges{{
			Filename: "testfile",
			Versions: []string{"hello", "hallo", "hola", "native", "ubuntu-latest", "- runs-on: ubuntu-latest", "- runs-on: debian-latest"},
		}}),
		newRepo(t, 2, "language-stats-test", []FileChanges{{
			Filename: "main.rs",
			Versions: []string{"fn main() {", "println!(\"Hello World!\");", "}"},
		}}),
		newRepo(t, 2, "mentions-highlighted", []FileChanges{
			{
				Filename:  "history1.md",
				Versions:  []string{""},
				CommitMsg: "A commit message which mentions @user2 in the title\nand has some additional text which mentions @user1",
			},
			{
				Filename:  "history2.md",
				Versions:  []string{""},
				CommitMsg: "Another commit which mentions @user1 in the title\nand @user2 in the text",
			},
		}),
		newRepo(t, 2, "unicode-escaping", []FileChanges{{
			Filename: "a-file",
			Versions: []string{"{a}{а}"},
		}}),
		// add your repo declarations here
	}

	return func() {
		for _, cleanup := range cleanupFunctions {
			cleanup()
		}
	}
}

func newRepo(t *testing.T, userID int64, repoName string, fileChanges []FileChanges) func() {
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: userID})
	somerepo, _, cleanupFunc := tests.CreateDeclarativeRepo(t, user, repoName,
		[]unit_model.Type{unit_model.TypeCode, unit_model.TypeIssues}, nil,
		nil,
	)

	var lastCommitID string
	for _, file := range fileChanges {
		for i, version := range file.Versions {
			operation := "update"
			if i == 0 {
				operation = "create"
			}

			// default to unique commit messages
			commitMsg := file.CommitMsg
			if commitMsg == "" {
				commitMsg = fmt.Sprintf("Patch: %s-%d", file.Filename, i+1)
			}

			resp, err := files_service.ChangeRepoFiles(git.DefaultContext, somerepo, user, &files_service.ChangeRepoFilesOptions{
				Files: []*files_service.ChangeRepoFile{{
					Operation:     operation,
					TreePath:      file.Filename,
					ContentReader: strings.NewReader(version),
				}},
				Message:   commitMsg,
				OldBranch: "main",
				NewBranch: "main",
				Author: &files_service.IdentityOptions{
					Name:  user.Name,
					Email: user.Email,
				},
				Committer: &files_service.IdentityOptions{
					Name:  user.Name,
					Email: user.Email,
				},
				Dates: &files_service.CommitDateOptions{
					Author:    time.Now(),
					Committer: time.Now(),
				},
				LastCommitID: lastCommitID,
			})
			require.NoError(t, err)
			assert.NotEmpty(t, resp)

			lastCommitID = resp.Commit.SHA
		}
	}

	err := stats.UpdateRepoIndexer(somerepo)
	require.NoError(t, err)

	return cleanupFunc
}
