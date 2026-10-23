// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package private

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"forgejo.org/models"
	asymkey_model "forgejo.org/models/asymkey"
	git_model "forgejo.org/models/git"
	issues_model "forgejo.org/models/issues"
	perm_model "forgejo.org/models/perm"
	access_model "forgejo.org/models/perm/access"
	quota_model "forgejo.org/models/quota"
	"forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/log"
	"forgejo.org/modules/private"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/web"
	app_context "forgejo.org/services/context"
	pull_service "forgejo.org/services/pull"
)

type preReceiveContext struct {
	*app_context.PrivateContext

	// loadedPusher indicates that where the following information are loaded
	loadedPusher        bool
	user                *user_model.User // it's the org user if a DeployKey is used
	userPerm            access_model.Permission
	deployKeyAccessMode perm_model.AccessMode

	protectedTags    []*git_model.ProtectedTag
	gotProtectedTags bool

	env []string

	opts *private.HookOptions

	isOverQuota bool
}

// canWriteCodeToBranch returns true if pusher can write code to the specified branch name on the ctx repository.
// Permitted if the user has write access to the code unit of the repository, or if there is a PR that allows maintainer
// edit, or if a write deploy key is in-use.
func (ctx *preReceiveContext) canWriteCodeToBranch(branchName string) bool {
	if !ctx.loadPusherAndPermission() {
		return false
	}
	return issues_model.CanMaintainerWriteToBranch(ctx, ctx.userPerm, branchName, ctx.user, access_model.GetUserRepoPermission) || ctx.deployKeyAccessMode >= perm_model.AccessModeWrite
}

// assertCanWriteCodeToBranch verifies that the pusher can write code to the specified branch name on the ctx
// repository.  If the user cannot write, an error response is written to the HTTP context.
func (ctx *preReceiveContext) assertCanWriteCodeToBranch(branchName string) bool {
	if !ctx.canWriteCodeToBranch(branchName) {
		if ctx.Written() {
			return false
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "User '%s' is not allowed to push to branch '%s' in '%s/%s'.", ctx.user.Name, branchName, ctx.Repo.Repository.OwnerName, ctx.Repo.Repository.Name)

		if ctx.CanCreatePullRequest() {
			fmt.Fprintf(&sb, "\nIf you instead wanted to create a pull request to the branch '%s', please use:", branchName)
			fmt.Fprintf(&sb, "\ngit push origin HEAD:refs/for/%s/choose-a-descriptor", branchName)
			sb.WriteString("\nYou might want to replace 'origin' with the name of your Git remote if it is different from origin. You can freely choose the descriptor to set it to a topic.")
			sb.WriteString("\nYou can learn about creating pull requests with AGit in the documentation: ")
			sb.WriteString(setting.AppDocsURL("user/git-cli/agit-support/"))
		}
		ctx.JSON(http.StatusForbidden, private.Response{
			UserMsg: sb.String(),
		})
		return false
	}
	return true
}

// canWriteCodeToBranch returns true if pusher can write code to the ctx repository.  Use [canWriteCodeToBranch] instead
// if the write is to a named branch.  Permitted if the user has write access to the code unit, or if a write deploy key
// is in-use.
func (ctx *preReceiveContext) canWriteCode() bool {
	if !ctx.loadPusherAndPermission() {
		return false
	}
	return ctx.userPerm.CanWrite(unit.TypeCode) || ctx.deployKeyAccessMode >= perm_model.AccessModeWrite
}

// assertCanWriteCode verifies that the pusher can write code to the repository.  Use [assertCanWriteCodeToBranch]
// instead if the write is to a named branch.  If the user cannot write, an error response is written to the HTTP
// context.
func (ctx *preReceiveContext) assertCanWriteCode() bool {
	if !ctx.canWriteCode() {
		if ctx.Written() {
			return false
		}
		msg := fmt.Sprintf("User '%s' is not allowed to push to repository '%s/%s'.", ctx.user.Name, ctx.Repo.Repository.OwnerName, ctx.Repo.Repository.Name)
		ctx.JSON(http.StatusForbidden, private.Response{
			UserMsg: msg,
		})
		return false
	}
	return true
}

// CanCreatePullRequest returns true if pusher can create pull requests
func (ctx *preReceiveContext) CanCreatePullRequest() bool {
	if !ctx.loadPusherAndPermission() {
		return false
	}
	return ctx.userPerm.CanRead(unit.TypePullRequests)
}

// AssertCreatePullRequest returns true if can create pull requests
func (ctx *preReceiveContext) AssertCreatePullRequest() bool {
	if !ctx.CanCreatePullRequest() {
		if ctx.Written() {
			return false
		}
		ctx.JSON(http.StatusForbidden, private.Response{
			UserMsg: "User permission denied for creating pull-request.",
		})
		return false
	}
	return true
}

var errPermissionDenied = errors.New("permission denied for changing repo settings")

func (ctx *preReceiveContext) canChangeSettings() error {
	if !ctx.loadPusherAndPermission() {
		return errPermissionDenied
	}

	if !ctx.userPerm.IsOwner() && !ctx.userPerm.IsAdmin() {
		return errPermissionDenied
	}

	if ctx.Repo.Repository.IsFork {
		return errPermissionDenied
	}

	return nil
}

func (ctx *preReceiveContext) validatePushOptions() error {
	opts := web.GetForm(ctx).(*private.HookOptions)

	if opts.GetGitPushOptions().ChangeRepoSettings() {
		return ctx.canChangeSettings()
	}

	return nil
}

func (ctx *preReceiveContext) assertPushOptions() bool {
	if err := ctx.validatePushOptions(); err != nil {
		ctx.JSON(http.StatusForbidden, private.Response{
			UserMsg: fmt.Sprintf("options validation failed: %v", err),
		})
		return false
	}
	return true
}

func (ctx *preReceiveContext) checkQuota() error {
	if !setting.Quota.Enabled {
		ctx.isOverQuota = false
		return nil
	}

	if !ctx.loadPusherAndPermission() {
		ctx.isOverQuota = true
		return nil
	}

	ok, err := quota_model.EvaluateForUser(ctx, ctx.Repo.Repository.OwnerID, quota_model.LimitSubjectSizeReposAll)
	if err != nil {
		log.Error("quota_model.EvaluateForUser: %v", err)
		ctx.JSON(http.StatusInternalServerError, private.Response{
			UserMsg: "Error checking user quota",
		})
		return err
	}

	ctx.isOverQuota = !ok
	return nil
}

func (ctx *preReceiveContext) quotaExceeded() {
	ctx.JSON(http.StatusRequestEntityTooLarge, private.Response{
		UserMsg: "Quota exceeded",
	})
}

// HookPreReceive checks whether a individual commit is acceptable
func HookPreReceive(ctx *app_context.PrivateContext) {
	opts := web.GetForm(ctx).(*private.HookOptions)

	ourCtx := &preReceiveContext{
		PrivateContext: ctx,
		env:            generateGitEnv(opts), // Generate git environment for checking commits
		opts:           opts,
	}

	if !ourCtx.assertPushOptions() {
		log.Trace("Git push options validation failed")
		return
	}
	log.Trace("Git push options validation succeeded")

	if err := ourCtx.checkQuota(); err != nil {
		return
	}

	// Iterate across the provided old commit IDs
	for i := range opts.OldCommitIDs {
		oldCommitID := opts.OldCommitIDs[i]
		newCommitID := opts.NewCommitIDs[i]
		refFullName := opts.RefFullNames[i]

		switch {
		case refFullName.IsBranch():
			preReceiveBranch(ourCtx, oldCommitID, newCommitID, refFullName)
		case refFullName.IsTag():
			preReceiveTag(ourCtx, oldCommitID, newCommitID, refFullName)
		case refFullName.IsFor():
			preReceiveFor(ourCtx, oldCommitID, newCommitID, refFullName)
		case refFullName.IsPull():
			preReceivePull(ourCtx, oldCommitID, newCommitID, refFullName)
		default:
			if ourCtx.isOverQuota {
				ourCtx.quotaExceeded()
				return
			}
			ourCtx.assertCanWriteCode()
		}
		if ctx.Written() {
			return
		}
	}

	ctx.PlainText(http.StatusOK, "ok")
}

func preReceiveBranch(ctx *preReceiveContext, oldCommitID, newCommitID string, refFullName git.RefName) {
	branchName := refFullName.BranchName()
	if !ctx.assertCanWriteCodeToBranch(branchName) {
		return
	}

	repo := ctx.Repo.Repository
	gitRepo := ctx.Repo.GitRepo
	objectFormat := ctx.Repo.GetObjectFormat()

	if branchName == repo.DefaultBranch && newCommitID == objectFormat.EmptyObjectID().String() {
		log.Warn("Forbidden: Branch: %s is the default branch in %-v and cannot be deleted", branchName, repo)
		ctx.JSON(http.StatusForbidden, private.Response{
			UserMsg: fmt.Sprintf("branch %s is the default branch and cannot be deleted", branchName),
		})
		return
	}

	protectBranch, err := git_model.GetFirstMatchProtectedBranchRule(ctx, repo.ID, branchName)
	if err != nil {
		log.Error("Unable to get protected branch: %s in %-v Error: %v", branchName, repo, err)
		ctx.JSON(http.StatusInternalServerError, private.Response{
			Err: err.Error(),
		})
		return
	}

	// Allow pushes to non-protected branches
	if protectBranch == nil {
		// ...unless the user is over quota, and the operation is not a delete
		if newCommitID != objectFormat.EmptyObjectID().String() && ctx.isOverQuota {
			ctx.quotaExceeded()
		}

		return
	}
	protectBranch.Repo = repo

	// This ref is a protected branch.
	//
	// First of all we need to enforce absolutely:
	//
	// 1. Detect and prevent deletion of the branch
	if newCommitID == objectFormat.EmptyObjectID().String() {
		log.Warn("Forbidden: Branch: %s in %-v is protected from deletion", branchName, repo)
		ctx.JSON(http.StatusForbidden, private.Response{
			UserMsg: fmt.Sprintf("branch %s is protected from deletion", branchName),
		})
		return
	}

	// 2. Disallow force pushes to protected branches
	if oldCommitID != objectFormat.EmptyObjectID().String() {
		output, _, err := git.NewCommand(ctx, "rev-list", "--max-count=1").AddDynamicArguments(oldCommitID, "^"+newCommitID).RunStdString(&git.RunOpts{Dir: repo.RepoPath(), Env: ctx.env})
		if err != nil {
			log.Error("Unable to detect force push between: %s and %s in %-v Error: %v", oldCommitID, newCommitID, repo, err)
			ctx.JSON(http.StatusInternalServerError, private.Response{
				Err: fmt.Sprintf("Fail to detect force push: %v", err),
			})
			return
		} else if len(output) > 0 {
			log.Warn("Forbidden: Branch: %s in %-v is protected from force push", branchName, repo)
			ctx.JSON(http.StatusForbidden, private.Response{
				UserMsg: fmt.Sprintf("branch %s is protected from force push", branchName),
			})
			return
		}
	}

	// 3. Enforce require signed commits
	if protectBranch.RequireSignedCommits {
		err := verifyCommits(oldCommitID, newCommitID, gitRepo, ctx.env)
		if err != nil {
			if !isErrUnverifiedCommit(err) {
				log.Error("Unable to check commits from %s to %s in %-v: %v", oldCommitID, newCommitID, repo, err)
				ctx.JSON(http.StatusInternalServerError, private.Response{
					Err: fmt.Sprintf("Unable to check commits from %s to %s: %v", oldCommitID, newCommitID, err),
				})
				return
			}
			unverifiedCommit := err.(*errUnverifiedCommit).sha
			log.Warn("Forbidden: Branch: %s in %-v is protected from unverified commit %s", branchName, repo, unverifiedCommit)
			ctx.JSON(http.StatusForbidden, private.Response{
				UserMsg: fmt.Sprintf("branch %s is protected from unverified commit %s", branchName, unverifiedCommit),
			})
			return
		}
	}

	// Now there are several tests which can be overridden:
	//
	// 4. Check protected file patterns - this is overridable from the UI
	changedProtectedfiles := false
	protectedFilePath := ""

	globs := protectBranch.GetProtectedFilePatterns()
	if len(globs) > 0 {
		_, err := pull_service.CheckFileProtection(gitRepo, oldCommitID, newCommitID, globs, 1, ctx.env)
		if err != nil {
			if !models.IsErrFilePathProtected(err) {
				log.Error("Unable to check file protection for commits from %s to %s in %-v: %v", oldCommitID, newCommitID, repo, err)
				ctx.JSON(http.StatusInternalServerError, private.Response{
					Err: fmt.Sprintf("Unable to check file protection for commits from %s to %s: %v", oldCommitID, newCommitID, err),
				})
				return
			}

			changedProtectedfiles = true
			protectedFilePath = err.(models.ErrFilePathProtected).Path
		}
	}

	// 5. Check if the doer is allowed to push
	var canPush bool
	if ctx.opts.DeployKeyID != 0 {
		canPush = !changedProtectedfiles && protectBranch.CanPush && (!protectBranch.EnableWhitelist || protectBranch.WhitelistDeployKeys)
	} else {
		user, err := user_model.GetUserByID(ctx, ctx.opts.UserID)
		if err != nil {
			log.Error("Unable to GetUserByID for commits from %s to %s in %-v: %v", oldCommitID, newCommitID, repo, err)
			ctx.JSON(http.StatusInternalServerError, private.Response{
				Err: fmt.Sprintf("Unable to GetUserByID for commits from %s to %s: %v", oldCommitID, newCommitID, err),
			})
			return
		}
		canPush = !changedProtectedfiles && protectBranch.CanUserPush(ctx, user)
	}

	// 6. If we're not allowed to push directly
	if !canPush {
		// Is this is a merge from the UI/API?
		if ctx.opts.PullRequestID == 0 {
			// 6a. If we're not merging from the UI/API then there are two ways we got here:
			//
			// We are changing a protected file and we're not allowed to do that
			if changedProtectedfiles {
				log.Warn("Forbidden: Branch: %s in %-v is protected from changing file %s", branchName, repo, protectedFilePath)
				ctx.JSON(http.StatusForbidden, private.Response{
					UserMsg: fmt.Sprintf("branch %s is protected from changing file %s", branchName, protectedFilePath),
				})
				return
			}

			// Allow commits that only touch unprotected files
			globs := protectBranch.GetUnprotectedFilePatterns()
			if len(globs) > 0 {
				unprotectedFilesOnly, err := pull_service.CheckUnprotectedFiles(gitRepo, oldCommitID, newCommitID, globs, ctx.env)
				if err != nil {
					log.Error("Unable to check file protection for commits from %s to %s in %-v: %v", oldCommitID, newCommitID, repo, err)
					ctx.JSON(http.StatusInternalServerError, private.Response{
						Err: fmt.Sprintf("Unable to check file protection for commits from %s to %s: %v", oldCommitID, newCommitID, err),
					})
					return
				}
				if unprotectedFilesOnly {
					// Commit only touches unprotected files, this is allowed
					return
				}
			}

			// Or we're simply not able to push to this protected branch
			log.Warn("Forbidden: User %d is not allowed to push to protected branch: %s in %-v", ctx.opts.UserID, branchName, repo)
			ctx.JSON(http.StatusForbidden, private.Response{
				UserMsg: fmt.Sprintf("Not allowed to push to protected branch %s", branchName),
			})
			return
		}
		// 6b. Merge (from UI or API)

		// Get the PR, user and permissions for the user in the repository
		pr, err := issues_model.GetPullRequestByID(ctx, ctx.opts.PullRequestID)
		if err != nil {
			log.Error("Unable to get PullRequest %d Error: %v", ctx.opts.PullRequestID, err)
			ctx.JSON(http.StatusInternalServerError, private.Response{
				Err: fmt.Sprintf("Unable to get PullRequest %d Error: %v", ctx.opts.PullRequestID, err),
			})
			return
		}

		// although we should have called `loadPusherAndPermission` before, here we call it explicitly again because we need to access ctx.user below
		if !ctx.loadPusherAndPermission() {
			// if error occurs, loadPusherAndPermission had written the error response
			return
		}

		// Now check if the user is allowed to merge PRs for this repository
		// Note: we can use ctx.perm and ctx.user directly as they will have been loaded above
		allowedMerge, err := pull_service.IsUserAllowedToMerge(ctx, pr, ctx.userPerm, ctx.user)
		if err != nil {
			log.Error("Error calculating if allowed to merge: %v", err)
			ctx.JSON(http.StatusInternalServerError, private.Response{
				Err: fmt.Sprintf("Error calculating if allowed to merge: %v", err),
			})
			return
		}

		if !allowedMerge {
			log.Warn("Forbidden: User %d is not allowed to push to protected branch: %s in %-v and is not allowed to merge pr #%d", ctx.opts.UserID, branchName, repo, pr.Index)
			ctx.JSON(http.StatusForbidden, private.Response{
				UserMsg: fmt.Sprintf("Not allowed to push to protected branch %s", branchName),
			})
			return
		}

		// If we're an admin for the instance, we can ignore checks
		if ctx.user.IsAdmin {
			return
		}

		// It's not allowed t overwrite protected files. Unless if the user is an
		// admin and the protected branch rule doesn't apply to admins.
		if changedProtectedfiles && (!ctx.userPerm.IsAdmin() || protectBranch.ApplyToAdmins) {
			log.Warn("Forbidden: Branch: %s in %-v is protected from changing file %s", branchName, repo, protectedFilePath)
			ctx.JSON(http.StatusForbidden, private.Response{
				UserMsg: fmt.Sprintf("branch %s is protected from changing file %s", branchName, protectedFilePath),
			})
			return
		}

		// Check all status checks and reviews are ok
		if pb, err := pull_service.CheckPullBranchProtections(ctx, pr, true); err != nil {
			if models.IsErrDisallowedToMerge(err) {
				// Allow this if the rule doesn't apply to admins and the user is an admin.
				if ctx.userPerm.IsAdmin() && !pb.ApplyToAdmins {
					return
				}
				log.Warn("Forbidden: User %d is not allowed push to protected branch %s in %-v and pr #%d is not ready to be merged: %s", ctx.opts.UserID, branchName, repo, pr.Index, err.Error())
				ctx.JSON(http.StatusForbidden, private.Response{
					UserMsg: fmt.Sprintf("Not allowed to push to protected branch %s and pr #%d is not ready to be merged: %s", branchName, ctx.opts.PullRequestID, err.Error()),
				})
				return
			}
			log.Error("Unable to check if mergeable: protected branch %s in %-v and pr #%d. Error: %v", ctx.opts.UserID, branchName, repo, pr.Index, err)
			ctx.JSON(http.StatusInternalServerError, private.Response{
				Err: fmt.Sprintf("Unable to get status of pull request %d. Error: %v", ctx.opts.PullRequestID, err),
			})
			return
		}
	}
}

func preReceiveTag(ctx *preReceiveContext, oldCommitID, newCommitID string, refFullName git.RefName) { //nolint:unparam
	if !ctx.assertCanWriteCode() {
		return
	}

	tagName := refFullName.TagName()

	if !ctx.gotProtectedTags {
		var err error
		ctx.protectedTags, err = git_model.GetProtectedTags(ctx, ctx.Repo.Repository.ID)
		if err != nil {
			log.Error("Unable to get protected tags for %-v Error: %v", ctx.Repo.Repository, err)
			ctx.JSON(http.StatusInternalServerError, private.Response{
				Err: err.Error(),
			})
			return
		}
		ctx.gotProtectedTags = true
	}

	isAllowed, err := git_model.IsUserAllowedToControlTag(ctx, ctx.protectedTags, tagName, ctx.opts.UserID)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, private.Response{
			Err: err.Error(),
		})
		return
	}
	if !isAllowed {
		log.Warn("Forbidden: Tag %s in %-v is protected", tagName, ctx.Repo.Repository)
		ctx.JSON(http.StatusForbidden, private.Response{
			UserMsg: fmt.Sprintf("Tag %s is protected", tagName),
		})
		return
	}

	// If the user is over quota, and the push isn't a tag deletion, deny it
	if ctx.isOverQuota {
		objectFormat := ctx.Repo.GetObjectFormat()
		if newCommitID != objectFormat.EmptyObjectID().String() {
			ctx.quotaExceeded()
			return
		}
	}
}

func preReceiveFor(ctx *preReceiveContext, oldCommitID, newCommitID string, refFullName git.RefName) { //nolint:unparam
	if !ctx.AssertCreatePullRequest() {
		return
	}

	if ctx.Repo.Repository.IsEmpty {
		ctx.JSON(http.StatusForbidden, private.Response{
			UserMsg: "Can't create pull request for an empty repository.",
		})
		return
	}

	if ctx.opts.IsWiki {
		ctx.JSON(http.StatusForbidden, private.Response{
			UserMsg: "Pull requests are not supported on the wiki.",
		})
		return
	}

	baseBranchName := refFullName.ForBranchName()

	baseBranchExist := ctx.Repo.GitRepo.IsBranchExist(baseBranchName)

	if !baseBranchExist {
		for p, v := range baseBranchName {
			if v == '/' && ctx.Repo.GitRepo.IsBranchExist(baseBranchName[:p]) && p != len(baseBranchName)-1 {
				baseBranchExist = true
				break
			}
		}
	}

	if !baseBranchExist {
		ctx.JSON(http.StatusForbidden, private.Response{
			UserMsg: fmt.Sprintf("Unexpected ref: %s", refFullName),
		})
		return
	}
}

func preReceivePull(ctx *preReceiveContext, oldCommitID, newCommitID string, refFullName git.RefName) { //nolint:unparam
	pullIndex, _ := strconv.ParseInt(refFullName.PullName(), 10, 64)
	if pullIndex <= 0 {
		ctx.JSON(http.StatusForbidden, private.Response{
			UserMsg: fmt.Sprintf("Invalid ref: %s", refFullName),
		})
		return
	}

	// special case: when mirroring, refs/pull/... will get push-deleted
	// let pre-receive pass and ignore deletion in agit.ProcReceive
	// (returning an error here would prevent mirroring the other refs)
	//
	// check before fetching the pull request, since non-existing PRs may get deleted
	// https://codeberg.org/forgejo/forgejo/pulls/14885
	objectFormat := ctx.Repo.GetObjectFormat()
	if newCommitID == objectFormat.EmptyObjectID().String() {
		if ctx.canWriteCode() {
			// pass to agit.ProcReceive
			return
		}
		if ctx.Written() {
			return
		}
		// user has no write permission: hard-failure
		ctx.JSON(http.StatusForbidden, private.Response{
			UserMsg: "Pull-requests cannot be deleted via git push.",
		})
		return
	}

	pr, err := issues_model.GetPullRequestByIndex(ctx, ctx.Repo.Repository.ID, pullIndex)
	if err != nil {
		ctx.JSON(http.StatusForbidden, private.Response{
			UserMsg: fmt.Sprintf("Unable to get pull request %d", pullIndex),
		})
		return
	}
	if pr.Flow != issues_model.PullRequestFlowAGit {
		userMsg := fmt.Sprintf("Only AGit pull-requests can be pushed via %s", refFullName)

		if pr.HeadRepoID == ctx.Repo.Repository.ID {
			userMsg += fmt.Sprintf("\nPush to branch %q instead", pr.HeadBranch)
		} else if pr.LoadHeadRepo(ctx) == nil {
			userMsg += "\nPush to the fork repo instead:"

			cloneLink := pr.HeadRepo.CloneLink()
			if !setting.Repository.DisableHTTPGit {
				userMsg += fmt.Sprintf("\n  git push %s HEAD:%s", cloneLink.HTTPS, pr.HeadBranch)
			}
			if !setting.SSH.Disabled {
				userMsg += fmt.Sprintf("\n  git push %s HEAD:%s", cloneLink.SSH, pr.HeadBranch)
			}
		}
		ctx.JSON(http.StatusForbidden, private.Response{
			UserMsg: userMsg,
		})
		return
	}

	// user must have repo write rights or be the PR creator
	if !ctx.canWriteCode() {
		if ctx.Written() {
			return
		}
		if ctx.user.ID != pr.Issue.PosterID {
			msg := fmt.Sprintf("User '%s' is not allowed to push to pull request %d of repository '%s/%s'.", ctx.user.Name, pr.Index, ctx.Repo.Repository.OwnerName, ctx.Repo.Repository.Name)
			ctx.JSON(http.StatusForbidden, private.Response{
				UserMsg: msg,
			})
			return
		}
	}
}

func generateGitEnv(opts *private.HookOptions) (env []string) {
	env = os.Environ()
	if opts.GitAlternativeObjectDirectories != "" {
		env = append(env,
			private.GitAlternativeObjectDirectories+"="+opts.GitAlternativeObjectDirectories)
	}
	if opts.GitObjectDirectory != "" {
		env = append(env,
			private.GitObjectDirectory+"="+opts.GitObjectDirectory)
	}
	if opts.GitQuarantinePath != "" {
		env = append(env,
			private.GitQuarantinePath+"="+opts.GitQuarantinePath)
	}
	return env
}

// loadPusherAndPermission returns false if an error occurs, and it writes the error response
func (ctx *preReceiveContext) loadPusherAndPermission() bool {
	if ctx.loadedPusher {
		return true
	}

	if ctx.opts.UserID == user_model.ActionsUserID {
		ctx.user = user_model.NewActionsUser()
		ctx.userPerm.AccessMode = perm_model.AccessMode(ctx.opts.ActionPerm)
		if err := ctx.Repo.Repository.LoadUnits(ctx); err != nil {
			log.Error("Unable to get User id %d Error: %v", ctx.opts.UserID, err)
			ctx.JSON(http.StatusInternalServerError, private.Response{
				Err: fmt.Sprintf("Unable to get User id %d Error: %v", ctx.opts.UserID, err),
			})
			return false
		}
		ctx.userPerm.Units = ctx.Repo.Repository.Units
		ctx.userPerm.UnitsMode = make(map[unit.Type]perm_model.AccessMode)
		for _, u := range ctx.Repo.Repository.Units {
			ctx.userPerm.UnitsMode[u.Type] = ctx.userPerm.AccessMode
		}
	} else {
		user, err := user_model.GetUserByID(ctx, ctx.opts.UserID)
		if err != nil {
			log.Error("Unable to get User id %d Error: %v", ctx.opts.UserID, err)
			ctx.JSON(http.StatusInternalServerError, private.Response{
				Err: fmt.Sprintf("Unable to get User id %d Error: %v", ctx.opts.UserID, err),
			})
			return false
		}
		ctx.user = user
		userPerm, err := access_model.GetUserRepoPermission(ctx, ctx.Repo.Repository, user)
		if err != nil {
			log.Error("Unable to get Repo permission of repo %s/%s of User %s: %v", ctx.Repo.Repository.OwnerName, ctx.Repo.Repository.Name, user.Name, err)
			ctx.JSON(http.StatusInternalServerError, private.Response{
				Err: fmt.Sprintf("Unable to get Repo permission of repo %s/%s of User %s: %v", ctx.Repo.Repository.OwnerName, ctx.Repo.Repository.Name, user.Name, err),
			})
			return false
		}
		ctx.userPerm = userPerm
	}

	if ctx.opts.DeployKeyID != 0 {
		deployKey, err := asymkey_model.GetDeployKeyByID(ctx, ctx.opts.DeployKeyID)
		if err != nil {
			log.Error("Unable to get DeployKey id %d Error: %v", ctx.opts.DeployKeyID, err)
			ctx.JSON(http.StatusInternalServerError, private.Response{
				Err: fmt.Sprintf("Unable to get DeployKey id %d Error: %v", ctx.opts.DeployKeyID, err),
			})
			return false
		}
		ctx.deployKeyAccessMode = deployKey.Mode
	}

	ctx.loadedPusher = true
	return true
}
