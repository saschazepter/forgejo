// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package private

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"forgejo.org/models/db"
	git_model "forgejo.org/models/git"
	issues_model "forgejo.org/models/issues"
	pull_model "forgejo.org/models/pull"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/cache"
	"forgejo.org/modules/git"
	"forgejo.org/modules/git/pushoptions"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/log"
	"forgejo.org/modules/private"
	repo_module "forgejo.org/modules/repository"
	"forgejo.org/modules/setting"
	timeutil "forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"
	"forgejo.org/modules/web"
	app_context "forgejo.org/services/context"
	repo_service "forgejo.org/services/repository"
)

// TestSeamPostReceiveSync is a test seam (a place where you can alter behaviour without editing the code) that is
// invoked when post-receive hook is just about to sync branches to the database, but hasn't yet.  This should only be
// used in test code.
var TestSeamPostReceiveSync func(*repo_module.PushUpdateOptions)

// HookPostReceive updates services and users
// https://git-scm.com/docs/githooks#post-receive
// This hook does not affect the outcome of git receive-pack, as it is called after the real work is done.
func HookPostReceive(ctx *app_context.PrivateContext) {
	opts := web.GetForm(ctx).(*private.HookOptions)

	// We don't rely on RepoAssignment here because:
	// a) we don't need the git repo in this function
	//    OUT OF DATE: we do need the git repo to sync the branch to the db now.
	// b) our update function will likely change the repository in the db so we will need to refresh it
	// c) we don't always need the repo

	ownerName := ctx.Params(":owner")
	repoName := ctx.Params(":repo")

	// defer getting the repository at this point - as we should only retrieve it if we're going to call update
	var repo *repo_model.Repository

	updates := make([]*repo_module.PushUpdateOptions, 0, len(opts.OldCommitIDs))
	wasEmpty := false

	for i := range opts.OldCommitIDs {
		refFullName := opts.RefFullNames[i]

		// Only trigger activity updates for changes to branches or
		// tags.  Updates to other refs (eg, refs/notes, refs/changes,
		// or other less-standard refs spaces are ignored since there
		// may be a very large number of them).
		if refFullName.IsBranch() || refFullName.IsTag() {
			if repo == nil {
				repo = loadRepository(ctx, ownerName, repoName)
				if ctx.Written() {
					// Error handled in loadRepository
					return
				}
				wasEmpty = repo.IsEmpty
			}

			option := &repo_module.PushUpdateOptions{
				RefFullName:  refFullName,
				OldCommitID:  opts.OldCommitIDs[i],
				NewCommitID:  opts.NewCommitIDs[i],
				PusherID:     opts.UserID,
				PusherName:   opts.UserName,
				RepoUserName: ownerName,
				RepoName:     repoName,
				TimeNano:     time.Now().UnixNano(),
			}
			updates = append(updates, option)
			if repo.IsEmpty && (refFullName.BranchName() == "master" || refFullName.BranchName() == "main") {
				// put the master/main branch first
				// FIXME: It doesn't always work, since the master/main branch may not be the first batch of updates.
				//        If the user pushes many branches at once, the Git hook will call the internal API in batches, rather than all at once.
				//        See https://github.com/go-gitea/gitea/blob/cb52b17f92e2d2293f7c003649743464492bca48/cmd/hook.go#L27
				//        If the user executes `git push origin --all` and pushes more than 30 branches, the master/main may not be the default branch.
				copy(updates[1:], updates)
				updates[0] = option
			}
		}
	}

	if repo != nil && len(updates) > 0 {
		branchesToSync := make([]*repo_module.PushUpdateOptions, 0, len(updates))
		for _, update := range updates {
			if !update.RefFullName.IsBranch() {
				continue
			}
			if repo == nil {
				repo = loadRepository(ctx, ownerName, repoName)
				if ctx.Written() {
					return
				}
				wasEmpty = repo.IsEmpty
			}

			if update.IsDelRef() {
				if err := git_model.AddDeletedBranch(ctx, repo.ID, update.RefFullName.BranchName(), update.PusherID); err != nil {
					log.Error("Failed to add deleted branch: %s/%s Error: %v", ownerName, repoName, err)
					ctx.JSON(http.StatusInternalServerError, private.HookPostReceiveResult{
						Err: fmt.Sprintf("Failed to add deleted branch: %s/%s Error: %v", ownerName, repoName, err),
					})
					return
				}
			} else {
				branchesToSync = append(branchesToSync, update)
			}
		}
		if len(branchesToSync) > 0 {
			gitRepo, err := gitrepo.OpenRepository(ctx, repo)
			if err != nil {
				log.Error("Failed to open repository: %s/%s Error: %v", ownerName, repoName, err)
				ctx.JSON(http.StatusInternalServerError, private.HookPostReceiveResult{
					Err: fmt.Sprintf("Failed to open repository: %s/%s Error: %v", ownerName, repoName, err),
				})
				return
			}

			branchNames := make([]string, 0, len(branchesToSync))
			for _, update := range branchesToSync {
				branchNames = append(branchNames, update.RefFullName.BranchName())
				if TestSeamPostReceiveSync != nil && testing.Testing() {
					TestSeamPostReceiveSync(update)
				}
			}

			err = repo_service.SyncBranchesToDB(ctx, repo.ID, opts.UserID, branchNames, gitRepo.GetBranchCommit)
			gitRepo.Close()
			if err != nil {
				ctx.JSON(http.StatusInternalServerError, private.HookPostReceiveResult{
					Err: fmt.Sprintf("Failed to sync branch to DB in repository: %s/%s Error: %v", ownerName, repoName, err),
				})
				return
			}
		}

		if err := repo_service.PushUpdates(updates); err != nil {
			log.Error("Failed to Update: %s/%s Total Updates: %d", ownerName, repoName, len(updates))
			for i, update := range updates {
				log.Error("Failed to Update: %s/%s Update: %d/%d: Branch: %s", ownerName, repoName, i, len(updates), update.RefFullName.BranchName())
			}
			log.Error("Failed to Update: %s/%s Error: %v", ownerName, repoName, err)

			ctx.JSON(http.StatusInternalServerError, private.HookPostReceiveResult{
				Err: fmt.Sprintf("Failed to Update: %s/%s Error: %v", ownerName, repoName, err),
			})
			return
		}
	}

	// handle pull request merging, a pull request action should push at least 1 commit
	if opts.PushTrigger == repo_module.PushTriggerPRMergeToBase {
		handlePullRequestMerging(ctx, opts, ownerName, repoName, updates)
		if ctx.Written() {
			return
		}
	}

	// Handle Push Options
	if !opts.GetGitPushOptions().Empty() {
		// load the repository
		if repo == nil {
			repo = loadRepository(ctx, ownerName, repoName)
			if ctx.Written() {
				// Error handled in loadRepository
				return
			}
			wasEmpty = repo.IsEmpty
		}

		repo.IsPrivate = opts.GetGitPushOptions().GetBool(pushoptions.RepoPrivate, repo.IsPrivate)
		repo.IsTemplate = opts.GetGitPushOptions().GetBool(pushoptions.RepoTemplate, repo.IsTemplate)
		if err := repo_model.UpdateRepositoryCols(ctx, repo, "is_private", "is_template"); err != nil {
			log.Error("Failed to Update: %s/%s Error: %v", ownerName, repoName, err)
			ctx.JSON(http.StatusInternalServerError, private.HookPostReceiveResult{
				Err: fmt.Sprintf("Failed to Update: %s/%s Error: %v", ownerName, repoName, err),
			})
			return
		}
	}

	if !setting.Git.PullRequestPushMessage {
		ctx.JSON(http.StatusOK, private.HookPostReceiveResult{
			RepoWasEmpty: wasEmpty,
		})
		return
	}

	messages := make([]private.HookPostReceiveMessage, 0, len(opts.OldCommitIDs))

	// We have to reload the repo in case its state is changed above
	repo = nil
	var baseRepo *repo_model.Repository

	// Now handle the pull request push messages
	for i := range opts.OldCommitIDs {
		refFullName := opts.RefFullNames[i]
		newCommitID := opts.NewCommitIDs[i]

		// agit ref
		if refFullName.IsPull() {
			if repo == nil {
				repo = loadRepository(ctx, ownerName, repoName)
				if ctx.Written() {
					return
				}
			}

			pullIndex, _ := strconv.ParseInt(refFullName.PullName(), 10, 64)

			// existing agit PR
			if !git.IsEmptyCommitID(opts.OldCommitIDs[i], nil) {
				messages = append(messages, private.HookPostReceiveMessage{
					PullURLS:  []string{fmt.Sprintf("%s/pulls/%d", repo.HTMLURL(), pullIndex)},
					Branch:    "",
					CreateURL: "",
					AgitRef:   "",
				})
				continue
			}

			// freshly created agit PR, display: git config set branch...
			pr, err := issues_model.GetPullRequestByIndex(ctx, repo.ID, pullIndex)
			if err != nil {
				log.Error("Failed to get PR by index %v Error: %v", pullIndex, err)
				continue
			}

			_, branch, _ := strings.Cut(pr.HeadBranch, "/")
			branch = git.SanitizeRefPattern(branch)

			messages = append(messages, private.HookPostReceiveMessage{
				PullURLS:  []string{fmt.Sprintf("%s/pulls/%d", repo.HTMLURL(), pullIndex)},
				Branch:    branch,
				CreateURL: "",
				AgitRef:   refFullName.String(),
			})
			continue
		}

		// If we've pushed a branch (and not deleted it)
		if !git.IsEmptyCommitID(newCommitID, nil) && refFullName.IsBranch() {
			// First ensure we have the repository loaded, we're allowed pulls requests and we can get the base repo
			if repo == nil {
				repo = loadRepository(ctx, ownerName, repoName)
				if ctx.Written() {
					return
				}

				baseRepo = repo

				if repo.IsFork {
					if err := repo.GetBaseRepo(ctx); err != nil {
						log.Error("Failed to get Base Repository of Forked repository: %-v Error: %v", repo, err)
						ctx.JSON(http.StatusInternalServerError, private.HookPostReceiveResult{
							Err:          fmt.Sprintf("Failed to get Base Repository of Forked repository: %-v Error: %v", repo, err),
							RepoWasEmpty: wasEmpty,
						})
						return
					}
					if repo.BaseRepo.AllowsPulls(ctx) {
						baseRepo = repo.BaseRepo
					}
				}

				if !baseRepo.AllowsPulls(ctx) {
					// We can stop there's no need to go any further
					ctx.JSON(http.StatusOK, private.HookPostReceiveResult{
						RepoWasEmpty: wasEmpty,
					})
					return
				}
			}

			branch := refFullName.BranchName()

			// If our branch is the default branch of an unforked repo - there's no PR to create or refer to
			if !repo.IsFork && branch == baseRepo.DefaultBranch {
				continue
			}

			// Check if there is an existing pull request for this branch.
			prList, err := issues_model.GetUnmergedPullRequestsAnyTarget(ctx, repo.ID, baseRepo.ID, branch, issues_model.PullRequestFlowGithub)
			if err != nil {
				log.Error("Failed to get active PR in: %-v Branch: %s to: %-v Error: %v", repo, branch, baseRepo, err)
				ctx.JSON(http.StatusInternalServerError, private.HookPostReceiveResult{
					Err: fmt.Sprintf(
						"Failed to get active PR in: %-v Branch: %s to: %-v Error: %v", repo, branch, baseRepo, err,
					),
					RepoWasEmpty: wasEmpty,
				})
				return
			}
			err = prList.LoadRepositories(ctx)
			if err != nil {
				log.Error("Failed to load repositories for PullRequestList: %s", err)
				ctx.JSON(http.StatusInternalServerError, private.HookPostReceiveResult{
					Err:          fmt.Sprintf("Failed to load repositories for PullRequestList: %s", err),
					RepoWasEmpty: wasEmpty,
				})
				return
			}

			if repo.IsFork {
				branch = fmt.Sprintf("%s:%s", repo.OwnerName, branch)
			}
			createURL := fmt.Sprintf("%s/compare/%s...%s", baseRepo.HTMLURL(), util.PathEscapeSegments(baseRepo.DefaultBranch), util.PathEscapeSegments(branch))
			var urls []string
			foundDefaultBranch := false
			for _, pr := range prList {
				var baseBranchDisplay string
				if pr.HeadRepoID == pr.BaseRepoID {
					// Inside the same repository: just show base branch name
					baseBranchDisplay = pr.BaseBranch
				} else {
					// We are merging this into another repo: display user/repo:branch
					baseBranchDisplay = fmt.Sprintf("%s:%s", pr.BaseRepo.FullName(), pr.BaseBranch)
				}
				urls = append(urls, fmt.Sprintf("%s/pulls/%d merges into %s", baseRepo.HTMLURL(), pr.Index, baseBranchDisplay))
				if pr.BaseBranch == baseRepo.DefaultBranch {
					foundDefaultBranch = true
				}
			}
			if foundDefaultBranch {
				createURL = ""
			}
			messages = append(messages, private.HookPostReceiveMessage{
				PullURLS:  urls,
				Branch:    branch,
				CreateURL: createURL,
				AgitRef:   "",
			})
		}
	}
	ctx.JSON(http.StatusOK, private.HookPostReceiveResult{
		Messages:     messages,
		RepoWasEmpty: wasEmpty,
	})
}

func loadContextCacheUser(ctx context.Context, id int64) (*user_model.User, error) {
	return cache.GetWithContextCache(ctx, "hook_post_receive_user", id, func() (*user_model.User, error) {
		return user_model.GetUserByID(ctx, id)
	})
}

// handlePullRequestMerging handle pull request merging, a pull request action should push at least 1 commit
func handlePullRequestMerging(ctx *app_context.PrivateContext, opts *private.HookOptions, ownerName, repoName string, updates []*repo_module.PushUpdateOptions) {
	if len(updates) == 0 {
		ctx.JSON(http.StatusInternalServerError, private.HookPostReceiveResult{
			Err: fmt.Sprintf("Pushing a merged PR (pr:%d) no commits pushed ", opts.PullRequestID),
		})
		return
	}

	pr, err := issues_model.GetPullRequestByID(ctx, opts.PullRequestID)
	if err != nil {
		log.Error("GetPullRequestByID[%d]: %v", opts.PullRequestID, err)
		ctx.JSON(http.StatusInternalServerError, private.HookPostReceiveResult{Err: "GetPullRequestByID failed"})
		return
	}

	pusher, err := loadContextCacheUser(ctx, opts.UserID)
	if err != nil {
		log.Error("Failed to Update: %s/%s Error: %v", ownerName, repoName, err)
		ctx.JSON(http.StatusInternalServerError, private.HookPostReceiveResult{Err: "Load pusher user failed"})
		return
	}

	pr.MergedCommitID = updates[len(updates)-1].NewCommitID
	pr.MergedUnix = timeutil.TimeStampNow()
	pr.Merger = pusher
	pr.MergerID = pusher.ID
	err = db.WithTx(ctx, func(ctx context.Context) error {
		// Removing an auto merge pull and ignore if not exist
		if err := pull_model.DeleteScheduledAutoMerge(ctx, pr.ID); err != nil && !db.IsErrNotExist(err) {
			return fmt.Errorf("DeleteScheduledAutoMerge[%d]: %v", opts.PullRequestID, err)
		}
		if _, err := pr.SetMerged(ctx); err != nil {
			return fmt.Errorf("SetMerged failed: %s/%s Error: %v", ownerName, repoName, err)
		}
		return nil
	})
	if err != nil {
		log.Error("Failed to update PR to merged: %v", err)
		ctx.JSON(http.StatusInternalServerError, private.HookPostReceiveResult{Err: "Failed to update PR to merged"})
	}
}
