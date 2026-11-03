// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPLv3-or-later

package repository_test

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"forgejo.org/models/db"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	repo_module "forgejo.org/modules/repository"
	repo_service "forgejo.org/services/repository"
	files_service "forgejo.org/services/repository/files"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCrossRepositoryCompare(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	t.Run("same repo, different branches", func(t *testing.T) {
		for _, exactSameRepoObject := range []bool{true, false} {
			t.Run(fmt.Sprintf("exactSameRepoObject=%v", exactSameRepoObject), func(t *testing.T) {
				baseRepo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
					Files: forgery.MapFS{
						"README.md": forgery.MapFile("base repo, default branch, README.md"),
					},
				})
				baseGitRepo, err := git.OpenRepository(t.Context(), baseRepo.RepoPath())
				require.NoError(t, err)
				baseGitRepo.LastCommitCache = &git.LastCommitCache{} // not used directly in this test, but initialized to later verify repo is closed

				// Create a second branch for the comparison
				branch := "new-branch"
				resp, err := files_service.ChangeRepoFiles(db.DefaultContext, baseRepo, user2, &files_service.ChangeRepoFilesOptions{
					Files: []*files_service.ChangeRepoFile{
						{
							Operation:     "update",
							TreePath:      "README.md",
							ContentReader: bytes.NewReader([]byte("base repo, new-branch, README.md")),
						},
					},
					OldBranch: baseRepo.DefaultBranch,
					NewBranch: "new-branch",
				})
				require.NoError(t, err)

				headGitRepo := baseGitRepo
				if !exactSameRepoObject {
					// For this test case, use two distinct *git.Repository objects, but the same underlying repository
					headGitRepo, err = git.OpenRepository(t.Context(), baseRepo.RepoPath())
					require.NoError(t, err)
					headGitRepo.LastCommitCache = &git.LastCommitCache{} // not used directly in this test, but initialized to later verify repo is closed
				}

				// verify both repos are marked with a LastCommitCache for the test to check if they're closed later
				assert.NotNil(t, baseGitRepo.LastCommitCache)
				assert.NotNil(t, headGitRepo.LastCommitCache)

				compare, err := repo_module.NewCrossRepositoryCompare(t.Context(), baseGitRepo, headGitRepo, baseRepo.DefaultBranch, branch, false, false)
				require.NoError(t, err)
				t.Cleanup(compare.Close)

				assert.Equal(t, resp.Commit.Parents[0].SHA, compare.MergeBase)
				assert.Equal(t, resp.Commit.Parents[0].SHA, compare.BaseCommitID)
				assert.Equal(t, resp.Commit.SHA, compare.HeadCommitID)
				require.Len(t, compare.Commits, 1)
				assert.Equal(t, resp.Commit.SHA, compare.Commits[0].ID.String())
				assert.Equal(t, 1, compare.NumFiles)

				// All repos accessible through compare should be the same *git.Repository object because only a single
				// repo is being compared in this test case:
				assert.Equal(t, headGitRepo, compare.HeadRepo())
				assert.Equal(t, headGitRepo, compare.BaseRepo())
				assert.Equal(t, headGitRepo, compare.CompareRepo())

				// Check repos are closed when compare is cleaned up
				compare.Close()
				assert.Nil(t, baseGitRepo.LastCommitCache)
				assert.Nil(t, headGitRepo.LastCommitCache)
			})
		}
	})

	t.Run("different repos, common ancestry", func(t *testing.T) {
		baseRepo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Files: forgery.MapFS{
				"README.md": forgery.MapFile("base repo, default branch, README.md"),
			},
		})
		baseGitRepo, err := git.OpenRepository(t.Context(), baseRepo.RepoPath())
		require.NoError(t, err)
		baseGitRepo.LastCommitCache = &git.LastCommitCache{} // not used directly in this test, but initialized to later verify repo is closed

		baseCommitID, err := baseGitRepo.GetBranchCommitID(baseRepo.DefaultBranch)
		require.NoError(t, err)

		// Fork the repository and modify the default branch
		headRepo, err := repo_service.ForkRepositoryIfNotExists(t.Context(), user2, user2, repo_service.ForkRepoOptions{
			BaseRepo: baseRepo,
			Name:     "fork",
		})
		require.NoError(t, err)
		resp, err := files_service.ChangeRepoFiles(db.DefaultContext, headRepo, user2, &files_service.ChangeRepoFilesOptions{
			Files: []*files_service.ChangeRepoFile{
				{
					Operation:     "update",
					TreePath:      "README.md",
					ContentReader: bytes.NewReader([]byte("head repo, default branch, README.md")),
				},
			},
			OldBranch:    headRepo.DefaultBranch,
			NewBranch:    headRepo.DefaultBranch,
			LastCommitID: baseCommitID,
		})
		require.NoError(t, err)

		headGitRepo, err := git.OpenRepository(t.Context(), headRepo.RepoPath())
		require.NoError(t, err)
		headGitRepo.LastCommitCache = &git.LastCommitCache{} // not used directly in this test, but initialized to later verify repo is closed

		// verify both repos are marked with a LastCommitCache for the test to check if they're closed later
		assert.NotNil(t, baseGitRepo.LastCommitCache)
		assert.NotNil(t, headGitRepo.LastCommitCache)

		compare, err := repo_module.NewCrossRepositoryCompare(t.Context(), baseGitRepo, headGitRepo, baseRepo.DefaultBranch, baseRepo.DefaultBranch, false, false)
		require.NoError(t, err)
		t.Cleanup(compare.Close)

		assert.Equal(t, resp.Commit.Parents[0].SHA, compare.MergeBase)
		assert.Equal(t, resp.Commit.Parents[0].SHA, compare.BaseCommitID)
		assert.Equal(t, resp.Commit.SHA, compare.HeadCommitID)
		require.Len(t, compare.Commits, 1)
		assert.Equal(t, resp.Commit.SHA, compare.Commits[0].ID.String())
		assert.Equal(t, 1, compare.NumFiles)

		assert.Equal(t, headGitRepo, compare.HeadRepo())
		assert.Equal(t, baseGitRepo, compare.BaseRepo())
		assert.NotNil(t, compare.CompareRepo())
		assert.NotEqual(t, headGitRepo.Path, compare.CompareRepo().Path)
		assert.NotEqual(t, baseGitRepo.Path, compare.CompareRepo().Path)

		// Ensure that references from headRepo didn't end up accessible in baseRepo; baseRepo's commits already exist
		// in headRepo because it's a fork
		_, err = baseGitRepo.GetCommit(compare.HeadCommitID)
		assert.True(t, git.IsErrNotExist(err))

		compareRepoPath := compare.CompareRepo().Path
		_, err = os.ReadDir(compareRepoPath) // verify compareRepoPath really exists before we close
		require.NoError(t, err)

		// Check repos are closed when compare is cleaned up
		compare.Close()
		assert.Nil(t, baseGitRepo.LastCommitCache)
		assert.Nil(t, headGitRepo.LastCommitCache)
		_, err = os.ReadDir(compareRepoPath) // verify compareRepoPath was removed from disk
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("different repos, no common ancestry", func(t *testing.T) {
		baseRepo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Files: forgery.MapFS{
				"README.md": forgery.MapFile("base repo, default branch, README.md"),
			},
		})
		baseGitRepo, err := git.OpenRepository(t.Context(), baseRepo.RepoPath())
		require.NoError(t, err)
		baseGitRepo.LastCommitCache = &git.LastCommitCache{} // not used directly in this test, but initialized to later verify repo is closed

		headRepo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Files: forgery.MapFS{
				"README.md": forgery.MapFile("head repo, default branch, README.md"),
			},
		})
		headGitRepo, err := git.OpenRepository(t.Context(), headRepo.RepoPath())
		require.NoError(t, err)
		headGitRepo.LastCommitCache = &git.LastCommitCache{} // not used directly in this test, but initialized to later verify repo is closed

		// verify both repos are marked with a LastCommitCache for the test to check if they're closed later
		assert.NotNil(t, baseGitRepo.LastCommitCache)
		assert.NotNil(t, headGitRepo.LastCommitCache)

		compare, err := repo_module.NewCrossRepositoryCompare(t.Context(), baseGitRepo, headGitRepo, baseRepo.DefaultBranch, headRepo.DefaultBranch, false, false)
		require.NoError(t, err)
		t.Cleanup(compare.Close)

		baseCommitID, err := baseGitRepo.GetBranchCommitID(baseRepo.DefaultBranch)
		require.NoError(t, err)
		headCommitID, err := headGitRepo.GetBranchCommitID(headRepo.DefaultBranch)
		require.NoError(t, err)

		assert.Equal(t, baseCommitID, compare.MergeBase)
		assert.Equal(t, baseCommitID, compare.BaseCommitID)
		assert.Equal(t, headCommitID, compare.HeadCommitID)
		assert.Empty(t, compare.Commits) // with no common ancestry, no commit list is possible
		assert.Equal(t, 1, compare.NumFiles)

		assert.Equal(t, headGitRepo, compare.HeadRepo())
		assert.Equal(t, baseGitRepo, compare.BaseRepo())
		assert.NotNil(t, compare.CompareRepo())
		assert.NotEqual(t, headGitRepo.Path, compare.CompareRepo().Path)
		assert.NotEqual(t, baseGitRepo.Path, compare.CompareRepo().Path)

		// Ensure that references from baseRepo didn't end up accessible in headRepo, and vice-versa:
		_, err = headGitRepo.GetCommit(compare.BaseCommitID)
		assert.True(t, git.IsErrNotExist(err))
		_, err = baseGitRepo.GetCommit(compare.HeadCommitID)
		assert.True(t, git.IsErrNotExist(err))

		compareRepoPath := compare.CompareRepo().Path
		_, err = os.ReadDir(compareRepoPath) // verify compareRepoPath really exists before we close
		require.NoError(t, err)

		// Check repos are closed when compare is cleaned up
		compare.Close()
		assert.Nil(t, baseGitRepo.LastCommitCache)
		assert.Nil(t, headGitRepo.LastCommitCache)
		_, err = os.ReadDir(compareRepoPath) // verify compareRepoPath was removed from disk
		assert.True(t, os.IsNotExist(err))
	})
}
