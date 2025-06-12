// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"forgejo.org/models/db"
	git_model "forgejo.org/models/git"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/log"
	repo_module "forgejo.org/modules/repository"
	"forgejo.org/modules/test"
	repo_service "forgejo.org/services/repository"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func forEachObjectFormat(t *testing.T, f func(t *testing.T, objectFormat git.ObjectFormat)) {
	for _, objectFormat := range []git.ObjectFormat{git.Sha256ObjectFormat, git.Sha1ObjectFormat} {
		if !git.SupportHashSha256 && objectFormat == git.Sha256ObjectFormat {
			continue
		}

		t.Run(objectFormat.Name(), func(t *testing.T) {
			f(t, objectFormat)
		})
	}
}

func TestGitPush(t *testing.T) {
	onGiteaRun(t, testGitPush)
}

func testGitPush(t *testing.T, u *url.URL) {
	forEachObjectFormat(t, func(t *testing.T, objectFormat git.ObjectFormat) {
		t.Run("Push branches at once", func(t *testing.T) {
			runTestGitPush(t, u, objectFormat, func(t *testing.T, gitPath string) (pushed, deleted []string) {
				for i := 0; i < 10; i++ {
					branchName := fmt.Sprintf("branch-%d", i)
					pushed = append(pushed, branchName)
					doGitCreateBranch(gitPath, branchName)(t)
				}
				pushed = append(pushed, "master")
				doGitPushTestRepository(gitPath, "origin", "--all")(t)
				return pushed, deleted
			})
		})

		t.Run("Push branches exists", func(t *testing.T) {
			runTestGitPush(t, u, objectFormat, func(t *testing.T, gitPath string) (pushed, deleted []string) {
				for i := 0; i < 10; i++ {
					branchName := fmt.Sprintf("branch-%d", i)
					if i < 5 {
						pushed = append(pushed, branchName)
					}
					doGitCreateBranch(gitPath, branchName)(t)
				}
				// only push master and the first 5 branches
				pushed = append(pushed, "master")
				args := append([]string{"origin"}, pushed...)
				doGitPushTestRepository(gitPath, args...)(t)

				pushed = pushed[:0]
				// do some changes for the first 5 branches created above
				for i := 0; i < 5; i++ {
					branchName := fmt.Sprintf("branch-%d", i)
					pushed = append(pushed, branchName)

					doGitAddSomeCommits(gitPath, branchName)(t)
				}

				for i := 5; i < 10; i++ {
					pushed = append(pushed, fmt.Sprintf("branch-%d", i))
				}
				pushed = append(pushed, "master")

				// push all, so that master are not changed
				doGitPushTestRepository(gitPath, "origin", "--all")(t)

				return pushed, deleted
			})
		})

		t.Run("Push branches one by one", func(t *testing.T) {
			runTestGitPush(t, u, objectFormat, func(t *testing.T, gitPath string) (pushed, deleted []string) {
				for i := 0; i < 10; i++ {
					branchName := fmt.Sprintf("branch-%d", i)
					doGitCreateBranch(gitPath, branchName)(t)
					doGitPushTestRepository(gitPath, "origin", branchName)(t)
					pushed = append(pushed, branchName)
				}
				return pushed, deleted
			})
		})

		t.Run("Delete branches", func(t *testing.T) {
			runTestGitPush(t, u, objectFormat, func(t *testing.T, gitPath string) (pushed, deleted []string) {
				doGitPushTestRepository(gitPath, "origin", "master")(t) // make sure master is the default branch instead of a branch we are going to delete
				pushed = append(pushed, "master")

				for i := 0; i < 10; i++ {
					branchName := fmt.Sprintf("branch-%d", i)
					pushed = append(pushed, branchName)
					doGitCreateBranch(gitPath, branchName)(t)
				}
				doGitPushTestRepository(gitPath, "origin", "--all")(t)

				for i := 0; i < 10; i++ {
					branchName := fmt.Sprintf("branch-%d", i)
					doGitPushTestRepository(gitPath, "origin", "--delete", branchName)(t)
					deleted = append(deleted, branchName)
				}
				return pushed, deleted
			})
		})

		t.Run("Push to deleted branch", func(t *testing.T) {
			runTestGitPush(t, u, objectFormat, func(t *testing.T, gitPath string) (pushed, deleted []string) {
				doGitPushTestRepository(gitPath, "origin", "master")(t) // make sure master is the default branch instead of a branch we are going to delete
				pushed = append(pushed, "master")

				doGitCreateBranch(gitPath, "branch-1")(t)
				doGitPushTestRepository(gitPath, "origin", "branch-1")(t)
				pushed = append(pushed, "branch-1")

				// delete and restore
				doGitPushTestRepository(gitPath, "origin", "--delete", "branch-1")(t)
				doGitPushTestRepository(gitPath, "origin", "branch-1")(t)

				return pushed, deleted
			})
		})
	})
}

func runTestGitPush(t *testing.T, u *url.URL, objectFormat git.ObjectFormat, gitOperation func(t *testing.T, gitPath string) (pushed, deleted []string)) {
	defer tests.PrintCurrentTest(t, 1)()
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo, err := repo_service.CreateRepository(db.DefaultContext, user, user, repo_service.CreateRepoOptions{
		Name:             "repo-to-push",
		Description:      "test git push",
		AutoInit:         false,
		DefaultBranch:    "main",
		IsPrivate:        false,
		ObjectFormatName: objectFormat.Name(),
	})
	require.NoError(t, err)
	require.NotEmpty(t, repo)

	gitPath := t.TempDir()

	doGitInitTestRepository(gitPath, objectFormat)(t)

	oldPath := u.Path
	oldUser := u.User
	defer func() {
		u.Path = oldPath
		u.User = oldUser
	}()
	u.Path = repo.FullName() + ".git"
	u.User = url.UserPassword(user.LowerName, userPassword)

	doGitAddRemote(gitPath, "origin", u)(t)

	gitRepo, err := git.OpenRepository(git.DefaultContext, gitPath)
	require.NoError(t, err)
	defer gitRepo.Close()

	pushedBranches, deletedBranches := gitOperation(t, gitPath)

	dbBranches := make([]*git_model.Branch, 0)
	require.NoError(t, db.GetEngine(db.DefaultContext).Where("repo_id=?", repo.ID).Find(&dbBranches))
	assert.Lenf(t, dbBranches, len(pushedBranches), "mismatched number of branches in db")
	dbBranchesMap := make(map[string]*git_model.Branch, len(dbBranches))
	for _, branch := range dbBranches {
		dbBranchesMap[branch.Name] = branch
	}

	deletedBranchesMap := make(map[string]bool, len(deletedBranches))
	for _, branchName := range deletedBranches {
		deletedBranchesMap[branchName] = true
	}

	for _, branchName := range pushedBranches {
		branch, ok := dbBranchesMap[branchName]
		deleted := deletedBranchesMap[branchName]
		assert.True(t, ok, "branch %s not found in database", branchName)
		assert.Equal(t, deleted, branch.IsDeleted, "IsDeleted of %s is %v, but it's expected to be %v", branchName, branch.IsDeleted, deleted)
		commitID, err := gitRepo.GetBranchCommitID(branchName)
		require.NoError(t, err)
		assert.Equal(t, commitID, branch.CommitID)
	}

	require.NoError(t, repo_service.DeleteRepositoryDirectly(db.DefaultContext, user, repo.ID))
}

func TestOptionsGitPush(t *testing.T) {
	onGiteaRun(t, testOptionsGitPush)
}

func testOptionsGitPush(t *testing.T, u *url.URL) {
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	forEachObjectFormat(t, func(t *testing.T, objectFormat git.ObjectFormat) {
		repo, err := repo_service.CreateRepository(db.DefaultContext, user, user, repo_service.CreateRepoOptions{
			Name:             "repo-to-push",
			Description:      "test git push",
			AutoInit:         false,
			DefaultBranch:    "main",
			IsPrivate:        false,
			ObjectFormatName: objectFormat.Name(),
		})
		require.NoError(t, err)
		require.NotEmpty(t, repo)

		gitPath := t.TempDir()

		doGitInitTestRepository(gitPath, objectFormat)(t)

		u.Path = repo.FullName() + ".git"
		u.User = url.UserPassword(user.LowerName, userPassword)
		doGitAddRemote(gitPath, "origin", u)(t)

		t.Run("Unknown push options are silently ignored", func(t *testing.T) {
			branchName := "branch0"
			doGitCreateBranch(gitPath, branchName)(t)
			doGitPushTestRepository(gitPath, "origin", branchName, "-o", "uknownoption=randomvalue", "-o", "repo.private=true")(t)
			repo, err := repo_model.GetRepositoryByOwnerAndName(db.DefaultContext, user.Name, "repo-to-push")
			require.NoError(t, err)
			require.True(t, repo.IsPrivate)
			require.False(t, repo.IsTemplate)
		})

		t.Run("Owner sets private & template to true via push options", func(t *testing.T) {
			branchName := "branch1"
			doGitCreateBranch(gitPath, branchName)(t)
			doGitPushTestRepository(gitPath, "origin", branchName, "-o", "repo.private=true", "-o", "repo.template=true")(t)
			repo, err := repo_model.GetRepositoryByOwnerAndName(db.DefaultContext, user.Name, "repo-to-push")
			require.NoError(t, err)
			require.True(t, repo.IsPrivate)
			require.True(t, repo.IsTemplate)
		})

		t.Run("Owner sets private & template to false via push options", func(t *testing.T) {
			branchName := "branch2"
			doGitCreateBranch(gitPath, branchName)(t)
			doGitPushTestRepository(gitPath, "origin", branchName, "-o", "repo.private=false", "-o", "repo.template=false")(t)
			repo, err = repo_model.GetRepositoryByOwnerAndName(db.DefaultContext, user.Name, "repo-to-push")
			require.NoError(t, err)
			require.False(t, repo.IsPrivate)
			require.False(t, repo.IsTemplate)
		})

		// create a collaborator with write access
		collaborator := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
		u.User = url.UserPassword(collaborator.LowerName, userPassword)
		doGitAddRemote(gitPath, "collaborator", u)(t)
		repo_module.AddCollaborator(db.DefaultContext, repo, collaborator)

		t.Run("Collaborator with write access is allowed to push", func(t *testing.T) {
			branchName := "branch3"
			doGitCreateBranch(gitPath, branchName)(t)
			doGitPushTestRepository(gitPath, "collaborator", branchName)(t)
		})

		t.Run("Collaborator with write access fails to change private & template via push options", func(t *testing.T) {
			logChecker, cleanup := test.NewLogChecker(log.DEFAULT, log.TRACE)
			logChecker.Filter("permission denied for changing repo settings").StopMark("Git push options validation")
			defer cleanup()
			branchName := "branch4"
			doGitCreateBranch(gitPath, branchName)(t)
			doGitPushTestRepositoryFail(gitPath, "collaborator", branchName, "-o", "repo.private=true", "-o", "repo.template=true")(t)
			repo, err = repo_model.GetRepositoryByOwnerAndName(db.DefaultContext, user.Name, "repo-to-push")
			require.NoError(t, err)
			require.False(t, repo.IsPrivate)
			require.False(t, repo.IsTemplate)
			logFiltered, logStopped := logChecker.Check(5 * time.Second)
			assert.True(t, logStopped)
			assert.True(t, logFiltered[0])
		})

		require.NoError(t, repo_service.DeleteRepositoryDirectly(db.DefaultContext, user, repo.ID))
	})
}
