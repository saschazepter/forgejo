// Copyright 2021 Gitea. All rights reserved.
// SPDX-License-Identifier: MIT

package automerge

import (
	"context"
	"errors"
	"fmt"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	access_model "forgejo.org/models/perm/access"
	pull_model "forgejo.org/models/pull"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/graceful"
	"forgejo.org/modules/log"
	"forgejo.org/modules/process"
	"forgejo.org/modules/queue"
	notify_service "forgejo.org/services/notify"
	pull_service "forgejo.org/services/pull"
	repo_service "forgejo.org/services/repository"
	shared_automerge "forgejo.org/services/shared/automerge"
)

// Init runs the task queue to that handles auto merges
func Init() error {
	notify_service.RegisterNotifier(NewNotifier())

	shared_automerge.PRAutoMergeQueue = queue.CreateUniqueQueue(graceful.GetManager().ShutdownContext(), "pr_auto_merge", handler)
	if shared_automerge.PRAutoMergeQueue == nil {
		return errors.New("unable to create pr_auto_merge queue")
	}
	go graceful.GetManager().RunWithCancel(shared_automerge.PRAutoMergeQueue)
	return nil
}

// handle passed PR IDs and test the PRs
func handler(items ...string) []string {
	for _, s := range items {
		var id int64
		var sha string
		if _, err := fmt.Sscanf(s, "%d_%s", &id, &sha); err != nil {
			log.Error("could not parse data from pr_auto_merge queue (%v): %v", s, err)
			continue
		}
		handlePullRequestAutoMerge(id, sha)
	}
	return nil
}

// ScheduleAutoMerge if schedule is false and no error, pull can be merged directly
func ScheduleAutoMerge(ctx context.Context, doer *user_model.User, pull *issues_model.PullRequest, style repo_model.MergeStyle, message string, deleteBranch bool) (scheduled bool, err error) {
	err = db.WithTx(ctx, func(ctx context.Context) error {
		if err := pull_model.ScheduleAutoMerge(ctx, doer, pull.ID, style, message, deleteBranch); err != nil {
			return err
		}
		scheduled = true

		_, err = issues_model.CreateAutoMergeComment(ctx, issues_model.CommentTypePRScheduledToAutoMerge, pull, doer)
		return err
	})
	return scheduled, err
}

// RemoveScheduledAutoMerge cancels a previously scheduled pull request
func RemoveScheduledAutoMerge(ctx context.Context, doer *user_model.User, pull *issues_model.PullRequest) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		if err := pull_model.DeleteScheduledAutoMerge(ctx, pull.ID); err != nil {
			return err
		}

		_, err := issues_model.CreateAutoMergeComment(ctx, issues_model.CommentTypePRUnScheduledToAutoMerge, pull, doer)
		return err
	})
}

// StartPRCheckAndAutoMergeBySHA start an automerge check and auto merge task for all pull requests of repository and SHA
func StartPRCheckAndAutoMergeBySHA(ctx context.Context, sha string, repo *repo_model.Repository) error {
	return shared_automerge.StartPRCheckAndAutoMergeBySHA(ctx, sha, repo)
}

// StartPRCheckAndAutoMerge start an automerge check and auto merge task for a pull request
func StartPRCheckAndAutoMerge(ctx context.Context, pull *issues_model.PullRequest) {
	shared_automerge.StartPRCheckAndAutoMerge(ctx, pull)
}

// handlePullRequestAutoMerge merge the pull request if all checks are successful
func handlePullRequestAutoMerge(pullID int64, sha string) {
	ctx, _, finished := process.GetManager().AddContext(graceful.GetManager().HammerContext(),
		fmt.Sprintf("Handle AutoMerge of PR[%d] with sha[%s]", pullID, sha))
	defer finished()

	pr, err := issues_model.GetPullRequestByID(ctx, pullID)
	if err != nil {
		log.Error("GetPullRequestByID[%d]: %v", pullID, err)
		return
	}

	// Check if there is a scheduled pr in the db
	exists, scheduledPRM, err := pull_model.GetScheduledMergeByPullID(ctx, pr.ID)
	if err != nil {
		log.Error("%-v GetScheduledMergeByPullID: %v", pr, err)
		return
	}
	if !exists {
		return
	}

	if err = pr.LoadBaseRepo(ctx); err != nil {
		log.Error("%-v LoadBaseRepo: %v", pr, err)
		return
	}

	// check the sha is the same as pull request head commit id
	baseGitRepo, err := gitrepo.OpenRepository(ctx, pr.BaseRepo)
	if err != nil {
		log.Error("OpenRepository: %v", err)
		return
	}
	defer baseGitRepo.Close()

	headCommitID, err := baseGitRepo.GetRefCommitID(pr.GetGitRefName())
	if err != nil {
		log.Error("GetRefCommitID: %v", err)
		return
	}
	if headCommitID != sha {
		log.Warn("Head commit id of auto merge %-v does not match sha [%s], it may means the head branch has been updated. Just ignore this request because a new request expected in the queue", pr, sha)
		return
	}

	// Get all checks for this pr
	// We get the latest sha commit hash again to handle the case where the check of a previous push
	// did not succeed or was not finished yet.
	if err = pr.LoadHeadRepo(ctx); err != nil {
		log.Error("%-v LoadHeadRepo: %v", pr, err)
		return
	}

	var headGitRepo *git.Repository
	if pr.BaseRepoID == pr.HeadRepoID {
		headGitRepo = baseGitRepo
	} else {
		headGitRepo, err = gitrepo.OpenRepository(ctx, pr.HeadRepo)
		if err != nil {
			log.Error("OpenRepository %-v: %v", pr.HeadRepo, err)
			return
		}
		defer headGitRepo.Close()
	}

	switch pr.Flow {
	case issues_model.PullRequestFlowGithub:
		headBranchExist := headGitRepo.IsBranchExist(pr.HeadBranch)
		if pr.HeadRepo == nil || !headBranchExist {
			log.Warn("Head branch of auto merge %-v does not exist [HeadRepoID: %d, Branch: %s]", pr, pr.HeadRepoID, pr.HeadBranch)
			return
		}
	case issues_model.PullRequestFlowAGit:
		headBranchExist := git.IsReferenceExist(ctx, baseGitRepo.Path, pr.GetGitRefName())
		if !headBranchExist {
			log.Warn("Head branch of auto merge %-v does not exist [HeadRepoID: %d, Branch(Agit): %s]", pr, pr.HeadRepoID, pr.HeadBranch)
			return
		}
	default:
		log.Error("wrong flow type %d", pr.Flow)
		return
	}

	// Check if all checks succeeded
	pass, err := pull_service.IsPullCommitStatusPass(ctx, pr)
	if err != nil {
		log.Error("%-v IsPullCommitStatusPass: %v", pr, err)
		return
	}
	if !pass {
		log.Info("Scheduled auto merge %-v has unsuccessful status checks", pr)
		return
	}

	// Merge if all checks succeeded
	doer, err := user_model.GetUserByID(ctx, scheduledPRM.DoerID)
	if err != nil {
		log.Error("Unable to get scheduled User[%d]: %v", scheduledPRM.DoerID, err)
		return
	}

	perm, err := access_model.GetUserRepoPermission(ctx, pr.HeadRepo, doer)
	if err != nil {
		log.Error("GetUserRepoPermission %-v: %v", pr.HeadRepo, err)
		return
	}

	if err := pull_service.CheckPullMergeable(ctx, doer, &perm, pr, pull_service.MergeCheckTypeGeneral, false); err != nil {
		if errors.Is(err, pull_service.ErrUserNotAllowedToMerge) {
			log.Info("%-v was scheduled to automerge by an unauthorized user", pr)
			return
		}
		log.Error("%-v CheckPullMergeable: %v", pr, err)
		return
	}

	if err := pull_service.Merge(ctx, pr, doer, baseGitRepo, scheduledPRM.MergeStyle, "", scheduledPRM.Message, true); err != nil {
		log.Error("pull_service.Merge: %v", err)
		// FIXME: if merge failed, we should display some error message to the pull request page.
		// The resolution is add a new column on automerge table named `error_message` to store the error message and displayed
		// on the pull request page. But this should not be finished in a bug fix PR which will be backport to release branch.
		return
	}

	if scheduledPRM.DeleteBranchAfterMerge {
		err := repo_service.DeleteBranchAfterMerge(ctx, doer, pr, headGitRepo)
		if err != nil {
			log.Error("%d repo_service.DeleteBranchIfUnused: %v", pr.ID, err)
		}
	}
}
