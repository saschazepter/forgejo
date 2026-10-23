// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"forgejo.org/models/db"
	git_model "forgejo.org/models/git"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	base "forgejo.org/modules/migration"
	repo_module "forgejo.org/modules/repository"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/routers/private"
	"forgejo.org/services/migrations/allowlist"
	pull_service "forgejo.org/services/pull"
	repo_service "forgejo.org/services/repository"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

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
	onApplicationRun(t, testGitPush)
}

func testGitPush(t *testing.T, u *url.URL) {
	forEachObjectFormat(t, func(t *testing.T, objectFormat git.ObjectFormat) {
		t.Run("Push branches at once", func(t *testing.T) {
			runTestGitPush(t, u, objectFormat, func(t *testing.T, gitPath string) (pushed, deleted []string) {
				for i := range 10 {
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
				for i := range 10 {
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
				for i := range 5 {
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
				for i := range 10 {
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

				for i := range 10 {
					branchName := fmt.Sprintf("branch-%d", i)
					pushed = append(pushed, branchName)
					doGitCreateBranch(gitPath, branchName)(t)
				}
				doGitPushTestRepository(gitPath, "origin", "--all")(t)

				for i := range 10 {
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

	require.NoError(t, repo_service.DeleteRepositoryDirectly(db.DefaultContext, repo.ID, repo_service.DeleteRepositoryOpts{}))
}

func TestOptionsGitPush(t *testing.T) {
	onApplicationRun(t, testOptionsGitPush)
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

		// create a collaborator user
		collaborator := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
		u.User = url.UserPassword(collaborator.LowerName, userPassword)
		doGitAddRemote(gitPath, "collaborator", u)(t)

		t.Run("User without write access is not allowed to push", func(t *testing.T) {
			defer test.MockVariableValue(&setting.AppDocsVer, func() string { return "v0.0" })()
			branchName := "branch3"
			doGitCreateBranch(gitPath, branchName)(t)
			stderr := doGitPushTestRepositoryFail(t, gitPath, "collaborator", branchName)

			assert.Contains(t, stderr, `remote: Forgejo: User 'user5' is not allowed to push to branch 'branch3' in 'user2/repo-to-push'.`)
			assert.Contains(t, stderr, `remote: If you instead wanted to create a pull request to the branch 'branch3', please use:`)
			assert.Contains(t, stderr, `remote: git push origin HEAD:refs/for/branch3/choose-a-descriptor`)
			assert.Contains(t, stderr, `remote: You might want to replace 'origin' with the name of your Git remote if it is different from origin. You can freely choose the descriptor to set it to a topic.`)
			assert.Contains(t, stderr, `remote: You can learn about creating pull requests with AGit in the documentation: https://forgejo.org/docs/v0.0/user/git-cli/agit-support/`) // derived from setting.AppDocsVer
		})

		// give write access to the collaborator
		require.NoError(t, repo_module.AddCollaborator(db.DefaultContext, repo, collaborator))

		t.Run("Collaborator with write access is allowed to push", func(t *testing.T) {
			branchName := "branch4"
			doGitCreateBranch(gitPath, branchName)(t)
			doGitPushTestRepository(gitPath, "collaborator", branchName)(t)
		})

		t.Run("Collaborator with write access fails to change private & template via push options", func(t *testing.T) {
			branchName := "branch5"
			doGitCreateBranch(gitPath, branchName)(t)
			stderr := doGitPushTestRepositoryFail(t, gitPath, "collaborator", branchName, "-o", "repo.private=true", "-o", "repo.template=true")
			assert.Contains(t, stderr, "Forgejo: options validation failed: permission denied for changing repo settings")

			repo, err = repo_model.GetRepositoryByOwnerAndName(db.DefaultContext, user.Name, "repo-to-push")
			require.NoError(t, err)
			require.False(t, repo.IsPrivate)
			require.False(t, repo.IsTemplate)
		})

		require.NoError(t, repo_service.DeleteRepositoryDirectly(db.DefaultContext, repo.ID, repo_service.DeleteRepositoryOpts{}))
	})
}

func TestGitPushAllowMaintainerEditRestrictedHead(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		baseRepoOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})

		// Create a base repository as a target for the pull request
		baseRepo := forgery.CreateRepository(t, baseRepoOwner, &forgery.CreateRepositoryOptions{DefaultBranch: "master"})
		baseRepoPath := t.TempDir()
		doGitInitTestRepository(baseRepoPath, git.Sha1ObjectFormat)(t)
		u.Path = baseRepo.FullName() + ".git"
		u.User = url.UserPassword(baseRepoOwner.LowerName, userPassword)
		doGitAddRemote(baseRepoPath, "origin", u)(t)
		doGitPushTestRepository(baseRepoPath, "origin", baseRepo.DefaultBranch)(t)

		// Fork the base repo
		forkUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		forkRepo, err := repo_service.ForkRepositoryAndUpdates(t.Context(), forkUser, forkUser, repo_service.ForkRepoOptions{
			BaseRepo:    baseRepo,
			Name:        "repo-pr-update",
			Description: "desc",
		})
		require.NoError(t, err)
		forkRepoPath := t.TempDir()
		u.Path = forkRepo.FullName() + ".git"
		u.User = url.UserPassword(forkUser.LowerName, userPassword)
		doGitClone(forkRepoPath, u)(t)

		// Make a modification in the fork repo
		branchName := "my-branch-for-pr"
		doGitCreateBranch(forkRepoPath, branchName)(t)
		doGitAddSomeCommits(forkRepoPath, branchName)(t)
		doGitPushTestRepository(forkRepoPath, "origin", branchName)(t)

		// Create a pull request in the base repo to incorporate the fork's modification
		pullIssue := &issues_model.Issue{
			RepoID:   baseRepo.ID,
			Title:    "Test Pull Request from Fork",
			PosterID: forkUser.ID,
			Poster:   forkUser,
			IsPull:   true,
		}
		pullRequest := &issues_model.PullRequest{
			HeadRepo:            forkRepo,
			HeadRepoID:          forkRepo.ID,
			HeadBranch:          branchName,
			BaseRepo:            baseRepo,
			BaseRepoID:          baseRepo.ID,
			BaseBranch:          baseRepo.DefaultBranch,
			Type:                issues_model.PullRequestGitea,
			AllowMaintainerEdit: true,
		}
		err = pull_service.NewPullRequest(git.DefaultContext, baseRepo, pullIssue, nil, nil, pullRequest, nil)
		require.NoError(t, err)

		// The existence of the pull request allows maintainers of the base repo (baseRepoOwner) to write to the fork
		// repo, but *only* to the branch for the pull request.  Set up for editing as the baseRepoOwner...
		u.Path = forkRepo.FullName() + ".git"
		u.User = url.UserPassword(baseRepoOwner.LowerName, userPassword)
		doGitAddRemote(baseRepoPath, "fork", u)(t)
		doGitFetch(baseRepoPath, "fork")(t)
		doGitCheckoutBranch(baseRepoPath, branchName)(t)

		// Test writing to the PR branch, should succeed:
		doGitAddSomeCommits(baseRepoPath, branchName)(t)
		doGitPushTestRepository(baseRepoPath, "fork", branchName)(t)

		// We're allowed to write to the PR branch, but not to another branch:
		doGitCreateBranch(baseRepoPath, "another-branch")(t)
		doGitAddSomeCommits(baseRepoPath, "another-branch")(t)
		doGitPushTestRepositoryFail(t, baseRepoPath, "fork", "another-branch")

		// Verify that each branch being pushed is checked independently -- pushing to a branch we're permitted to, and
		// then a branch that we're not, does not allow the push:
		doGitAddSomeCommits(baseRepoPath, branchName)(t)       // Ensure we have new commits ready to push
		doGitAddSomeCommits(baseRepoPath, "another-branch")(t) // Ensure we have new commits ready to push
		doGitPushTestRepositoryFail(t, baseRepoPath, "fork", branchName, "another-branch")
	})
}

func TestGitPushMirror(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		owner := forgery.CreateUser(t, nil)
		repo := forgery.CreateRepository(t, owner, &forgery.CreateRepositoryOptions{
			Files: forgery.FilesInit{},
		})

		repoPath := t.TempDir()
		u.Path = repo.FullName() + ".git"
		u.User = url.UserPassword(owner.LowerName, userPassword)
		doGitClone(repoPath, u)(t)

		branchName := "local-branch"
		doGitCreateBranch(repoPath, branchName)(t)
		doGitAddSomeCommits(repoPath, branchName)(t)

		// create an agit PR (1)
		err := git.NewCommand(git.DefaultContext, "push", "origin").
			AddDynamicArguments(fmt.Sprintf("%s:refs/for/main/first-pr", branchName)).Run(&git.RunOpts{Dir: repoPath})
		require.NoError(t, err)

		// create a branch PR (2)
		err = git.NewCommand(git.DefaultContext, "push", "origin").
			AddDynamicArguments(fmt.Sprintf("%s:remote-branch", branchName)).Run(&git.RunOpts{Dir: repoPath})
		require.NoError(t, err)
		pullIssue := &issues_model.Issue{
			RepoID:   repo.ID,
			Title:    "Branch Pull Request",
			PosterID: owner.ID,
			Poster:   owner,
			IsPull:   true,
		}
		pullRequest := &issues_model.PullRequest{
			HeadRepo:            repo,
			HeadRepoID:          repo.ID,
			HeadBranch:          "remote-branch",
			BaseRepo:            repo,
			BaseRepoID:          repo.ID,
			BaseBranch:          repo.DefaultBranch,
			Type:                issues_model.PullRequestGitea,
			AllowMaintainerEdit: true,
		}
		err = pull_service.NewPullRequest(git.DefaultContext, repo, pullIssue, nil, nil, pullRequest, nil)
		require.NoError(t, err)

		// push --mirror
		var stderr bytes.Buffer
		err = git.NewCommand(git.DefaultContext, "push", "--mirror").Run(&git.RunOpts{Dir: repoPath, Stderr: &stderr})
		assert.Contains(t, stderr.String(), "(Forgejo ignored PR deletion attempt)")
		require.NoError(t, err, stderr.String())

		// ensure both PRs can still be fetched
		err = git.NewCommand(git.DefaultContext, "fetch", "origin").
			AddDynamicArguments(fmt.Sprintf("refs/pull/%d/head:%s", 1, "first-agit-pr")).
			Run(&git.RunOpts{Dir: repoPath})
		require.NoError(t, err)
		err = git.NewCommand(git.DefaultContext, "fetch", "origin").
			AddDynamicArguments(fmt.Sprintf("refs/pull/%d/head:%s", 2, "second-branch-pr")).
			Run(&git.RunOpts{Dir: repoPath})
		require.NoError(t, err)

		// Create a mirror (via a migration) and convert it to a pushed mirror
		// https://codeberg.org/forgejo/forgejo/pulls/14885
		t.Run("CloneMirror", func(t *testing.T) {
			defer allowlist.Init() // reset allowlist to not disturb the other tests

			defer test.MockVariableValue(&setting.Migrations.AllowUnencrypted, true)()
			defer test.MockVariableValue(&setting.Migrations.AllowLocalNetworks, true)()
			allowlist.Init() // reads the variables defined above

			mirror := forgery.CreateRepository(t, owner, &forgery.CreateRepositoryOptions{})
			mirror, err = repo_service.MigrateRepositoryGitData(t.Context(), mirror.Owner, mirror, base.MigrateOptions{
				RepoName:  mirror.Name,
				CloneAddr: repo.HTMLURL(),
			}, http.DefaultTransport)
			require.NoError(t, err)

			// check that the refs got mirrored (shouldn't happen, but would invalidate the rest of the test)
			// https://codeberg.org/forgejo/forgejo/pulls/14885
			err = git.NewCommand(git.DefaultContext, "fetch").
				AddDynamicArguments(
					mirror.HTMLURL(),
					fmt.Sprintf("refs/pull/%d/head:%s", 2, "second-branch-pr"),
				).
				Run(&git.RunOpts{Dir: repoPath})
			require.NoError(t, err, "if exit code = 128, check if the rest of this test is still relevant")

			// setup push mirror
			session := loginUser(t, owner.Name)
			session.MakeRequest(t, NewRequestWithValues(t, "POST", repo.HTMLURL()+"/settings", map[string]string{
				"action":                     "push-mirror-add",
				"push_mirror_address":        mirror.HTMLURL(),
				"push_mirror_username":       owner.Name,
				"push_mirror_password":       userPassword,
				"push_mirror_sync_on_commit": "on",
				"push_mirror_interval":       "8h0m0s",
			}), http.StatusSeeOther)

			// check initialSha of mirror
			gitMirror, err := git.OpenRepository(git.DefaultContext, mirror.RepoPath())
			require.NoError(t, err)
			defer gitMirror.Close()

			initialSha, err := gitMirror.GetBranchCommitID(mirror.DefaultBranch)
			require.NoError(t, err)

			// push some commits to original repo
			err = git.NewCommand(git.DefaultContext, "push", "origin").
				AddDynamicArguments(fmt.Sprintf("%s:%s", branchName, repo.DefaultBranch)).
				Run(&git.RunOpts{Dir: repoPath})
			require.NoError(t, err)

			// wait for new commit to arrive to the mirror
			start := time.Now()
			for {
				updatedSha, err := gitMirror.GetBranchCommitID(mirror.DefaultBranch)
				require.NoError(t, err)
				if updatedSha != initialSha {
					break
				}
				const maxDelay = 5 * time.Second
				if time.Since(start) >= maxDelay {
					t.Errorf("mirror not updated within %s", maxDelay)
					break
				}
				time.Sleep(time.Second)
			}

			// check that the refs got deleted from the mirror and cannot be fetched anymore
			err = git.NewCommand(git.DefaultContext, "fetch").
				AddDynamicArguments(
					mirror.HTMLURL(),
					fmt.Sprintf("refs/pull/%d/head:%s", 2, "second-branch-pr"),
				).
				Run(&git.RunOpts{Dir: repoPath})
			require.Error(t, err)
			require.True(t, git.IsErrorExitCode(err, 128), err)
		})
	})
}

// Two pushes to the same branch that are processed concurrently must not roll back the branch's commit ID in the
// database.  This is tricky to test; `TestSeamPostReceiveSync` is used in the post-receive hook to cause a process to
// stall to replicate the uncertain concurrency that occurs in reality.
func TestGitPushConcurrentBranchUpdate(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		owner := forgery.CreateUser(t, &forgery.CreateUserOptions{})

		repo := forgery.CreateRepository(t, owner, &forgery.CreateRepositoryOptions{
			Files: forgery.FilesInit{},
		})
		branchName := repo.DefaultBranch

		repoPath := t.TempDir()
		u.Path = repo.FullName() + ".git"
		u.User = url.UserPassword(owner.LowerName, userPassword)
		doGitClone(repoPath, u)(t)

		revParseHead := func() string {
			stdout, _, err := git.NewCommand(git.DefaultContext, "rev-parse", "HEAD").RunStdString(&git.RunOpts{Dir: repoPath})
			require.NoError(t, err)
			return strings.TrimSpace(stdout)
		}
		initialCommitID := revParseHead()
		doGitAddSomeCommits(repoPath, branchName)(t)
		firstCommitID := revParseHead()
		doGitAddSomeCommits(repoPath, branchName)(t)
		secondCommitID := revParseHead()
		t.Logf("initialCommitID = %q", initialCommitID)
		t.Logf("firstCommitID = %q", firstCommitID)
		t.Logf("secondCommitID = %q", secondCommitID)

		pushCommit := func(commitID string) error {
			_, stderr, err := git.NewCommand(git.DefaultContext, "push", "origin").
				AddDynamicArguments(fmt.Sprintf("%s:refs/heads/%s", commitID, branchName)).
				RunStdString(&git.RunOpts{Dir: repoPath})
			if err != nil {
				return fmt.Errorf("%s: %w", stderr, err)
			}
			return nil
		}

		assertBranchCommitInDB := func(expectedCommitID string) {
			t.Helper()
			branch, err := git_model.GetBranch(t.Context(), repo.ID, branchName)
			require.NoError(t, err)
			assert.Equal(t, expectedCommitID, branch.CommitID)
		}

		// Inject into the post-receive handler a function which recognizes when we're pushing the first commit, and
		// freezes the goroutine until we want it to continue.
		firstPostReceiveHookRunning := make(chan any, 1)
		unblockFirstPostReceiveHook := make(chan any, 1)
		defer test.MockVariableValue(&private.TestSeamPostReceiveSync, func(puo *repo_module.PushUpdateOptions) {
			if puo.NewCommitID == firstCommitID {
				close(firstPostReceiveHookRunning)
				<-unblockFirstPostReceiveHook
			}
		})()

		// Begin the first push of `firstCommitID`; runs in a goroutine because it will be blocked by the
		// TestSeamPostReceiveSync implementation.
		firstPushDone := make(chan error, 1)
		go func() {
			firstPushDone <- pushCommit(firstCommitID)
		}()
		firstPushReleased := false
		finishFirstPush := func() {
			if !firstPushReleased {
				firstPushReleased = true
				close(unblockFirstPostReceiveHook)
				assert.NoError(t, <-firstPushDone)
			}
		}
		defer finishFirstPush() // if test fails/exits, still try to cleanup the running goroutine

		// Wait until the first post-receive hook is running, identified by firstPostReceiveHookRunning:
		select {
		case <-firstPostReceiveHookRunning:
			// Expected - first push is now waiting on the post-receive hook to complete
		case <-time.NewTimer(30 * time.Second).C:
			assert.FailNow(t, "first push's post-receive hook never started")
		case <-t.Context().Done():
		}

		// Test pre-condition: the first push, which should be stalled right now, should not have yet updated the
		// database to the first commit ID.  If it has, something has gone wrong and the test scenario isn't valid.
		assertBranchCommitInDB(initialCommitID)

		// Second push: runs to completion while the first push is stalled.
		require.NoError(t, pushCommit(secondCommitID))
		assertBranchCommitInDB(secondCommitID)

		// Allow the first push's post-receive to complete; it must not revert the branch to firstCommitID.
		finishFirstPush()
		assertBranchCommitInDB(secondCommitID)
	})
}

func TestGitPushAGit(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		owner := forgery.CreateUser(t, nil)
		repo := forgery.CreateRepository(t, owner, &forgery.CreateRepositoryOptions{
			Files: forgery.FilesInit{},
		})

		prRegex := regexp.MustCompile(`refs/pull/(\d+)/head`)

		newAgitPR := func(repoPath string) (string, error) {
			branchName := "agit-pr"
			doGitCreateBranch(repoPath, branchName)(t)
			doGitAddSomeCommits(repoPath, branchName)(t)
			_, stdErr, err := git.NewCommand(git.DefaultContext, "push", "origin").
				AddDynamicArguments(fmt.Sprintf("%s:refs/for/main/%s", branchName, branchName)).RunStdString(&git.RunOpts{Dir: repoPath})
			if err != nil {
				return "", err
			}
			require.NoError(t, err)
			return prRegex.FindStringSubmatch(stdErr)[1], nil
		}
		fetchAgitPR := func(repoPath, branchName, prIndex string) error {
			return git.NewCommand(git.DefaultContext, "fetch", "origin").
				AddDynamicArguments(fmt.Sprintf("refs/pull/%s/head:%s", prIndex, branchName)).
				Run(&git.RunOpts{Dir: repoPath})
		}
		pushAgitPR := func(repoPath, branchName, prIndex string) error {
			_, stdErr, err := git.NewCommand(git.DefaultContext, "push", "origin").
				AddDynamicArguments(fmt.Sprintf("%s:refs/pull/%s/head", branchName, prIndex)).
				RunStdString(&git.RunOpts{Dir: repoPath})
			if err != nil {
				return fmt.Errorf("%s: %w", stdErr, err)
			}
			return nil
		}

		ownerPath := t.TempDir()
		u.Path = repo.FullName() + ".git"
		u.User = url.UserPassword(owner.LowerName, userPassword)
		doGitClone(ownerPath, u)(t)

		ownerPR, err := newAgitPR(ownerPath)
		require.NoError(t, err)

		// Fork the base repo
		contributor := forgery.CreateUser(t, nil)
		contributorPath := t.TempDir()
		u.Path = repo.FullName() + ".git"
		u.User = url.UserPassword(contributor.LowerName, userPassword)
		doGitClone(contributorPath, u)(t)

		// contributor PR
		contributorPR, err := newAgitPR(contributorPath)
		require.NoError(t, err)

		t.Run("owner", func(t *testing.T) {
			// pushing to any agit PR is fine
			require.NoError(t, fetchAgitPR(ownerPath, "owner-pr", ownerPR))
			doGitAddSomeCommits(ownerPath, "owner-pr")(t)
			require.NoError(t, pushAgitPR(ownerPath, "owner-pr", ownerPR))

			require.NoError(t, fetchAgitPR(ownerPath, "contributor-pr", contributorPR))
			doGitAddSomeCommits(ownerPath, "contributor-pr")(t)
			require.NoError(t, pushAgitPR(ownerPath, "contributor-pr", contributorPR))
		})
		t.Run("contributor", func(t *testing.T) {
			// pushing to other agit PR fails
			require.NoError(t, fetchAgitPR(contributorPath, "owner-pr", ownerPR))
			doGitAddSomeCommits(contributorPath, "owner-pr")(t)
			require.ErrorContains(t, pushAgitPR(contributorPath, "owner-pr", ownerPR), "not allowed to push to pull request")

			// pushing to own PR is fine
			require.NoError(t, fetchAgitPR(contributorPath, "contributor-pr", contributorPR))
			doGitAddSomeCommits(contributorPath, "contributor-pr")(t)
			require.NoError(t, pushAgitPR(contributorPath, "contributor-pr", contributorPR))
		})
	})
}
