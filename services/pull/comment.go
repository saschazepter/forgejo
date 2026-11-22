// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pull

import (
	"context"
	"fmt"
	"slices"

	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/json"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
)

// getCommitIDsFromRepo get commit IDs from repo in between oldCommitID and newCommitID
// isForcePush will be true if oldCommit isn't on the branch
// Commit on baseBranch will skip
func getCommitIDsFromRepo(ctx context.Context, repo *repo_model.Repository, oldCommitID, newCommitID, baseBranch string) (commitIDs []string, isForcePush bool, err error) {
	gitRepo, closer, err := gitrepo.RepositoryFromContextOrOpen(ctx, repo)
	if err != nil {
		return nil, false, err
	}
	defer closer.Close()

	oldCommit, err := gitRepo.GetCommit(oldCommitID)
	if err != nil {
		return nil, false, err
	}

	newCommit, err := gitRepo.GetCommit(newCommitID)
	if err != nil {
		return nil, false, err
	}

	isForcePush, err = newCommit.IsForcePush(oldCommitID)
	if err != nil {
		return nil, false, err
	}

	if isForcePush {
		commitIDs = make([]string, 2)
		commitIDs[0] = oldCommitID
		commitIDs[1] = newCommitID

		return commitIDs, isForcePush, err
	}

	// Find commits between new and old commit excluding base branch commits
	commits, err := gitRepo.CommitsBetweenNotBase(newCommit, oldCommit, baseBranch)
	if err != nil {
		return nil, false, err
	}

	commitIDs = make([]string, 0, len(commits))
	for _, c := range slices.Backward(commits) {
		commitIDs = append(commitIDs, c.ID.String())
	}

	return commitIDs, isForcePush, err
}

// CreatePushPullComment create push code to pull base comment
func CreatePushPullComment(ctx context.Context, pusher *user_model.User, pr *issues_model.PullRequest, oldCommitID, newCommitID string) (comment *issues_model.Comment, err error) {
	if pr.HasMerged || oldCommitID == "" || newCommitID == "" {
		return nil, nil
	}

	ops := &issues_model.CreateCommentOptions{
		Type: issues_model.CommentTypePullRequestPush,
		Doer: pusher,
		Repo: pr.BaseRepo,
	}

	var data issues_model.PushActionContent

	data.CommitIDs, data.IsForcePush, err = getCommitIDsFromRepo(ctx, pr.BaseRepo, oldCommitID, newCommitID, pr.BaseBranch)
	if err != nil {
		return nil, err
	}

	// Record the current merge-base of the commits during a force push.  This allows us to accurately `git range-diff`
	// in the future to see the changes made during this force push.  Without the merge-base recorded now, if the base
	// branch of the PR changed, or the base branch was force pushed, or the new commit was merged into the base branch,
	// we wouldn't be able to compute the same merge-base and therefore provide the same `range-diff` command:
	if data.IsForcePush {
		gitRepo, closer, err := gitrepo.RepositoryFromContextOrOpen(ctx, pr.BaseRepo)
		if err != nil {
			return nil, fmt.Errorf("failed to open repository: %w", err)
		}

		// `git merge-base` cmds can fail if there is no merge base; this would probably be a mistake by the user force
		// pushing where they've messed up their PR.  We omit recording the merge base and emit a warning.
		oldMergeBase, err := gitRepo.GetMergeBase(pr.BaseBranch, data.CommitIDs[0])
		if err != nil {
			log.Warn("Force push to repo %s PR %d; failed to get old commit's merge-base: %s", pr.BaseRepo.FullName(), pr.Index, err)
		}
		newMergeBase, err := gitRepo.GetMergeBase(pr.BaseBranch, data.CommitIDs[1])
		if err != nil {
			log.Warn("Force push to repo %s PR %d; failed to get new commit's merge-base: %s", pr.BaseRepo.FullName(), pr.Index, err)
		}
		if oldMergeBase != "" || newMergeBase != "" {
			data.ForcePush = &issues_model.ForcePushInfo{
				OldMergeBase: oldMergeBase,
				NewMergeBase: newMergeBase,
			}
		}
		closer.Close()
	}

	if err := pr.LoadIssue(ctx); err != nil {
		return nil, err
	}

	ops.Issue = pr.Issue

	dataJSON, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	ops.Content = string(dataJSON)

	comment, err = issues_model.CreateComment(ctx, ops)

	if data.IsForcePush && setting.Repository.PullRequest.KeepForcePushRefs {
		comment.Issue = pr.Issue
		gitRepo, closer, err := gitrepo.RepositoryFromContextOrOpen(ctx, pr.BaseRepo)
		if err != nil {
			return nil, fmt.Errorf("failed to open repository: %w", err)
		}
		defer closer.Close()
		if err := gitRepo.SetReference(comment.ForcePushOldCommitGitRefName(), data.CommitIDs[0]); err != nil {
			return nil, fmt.Errorf("failed to update ref in repository for force-push comment: %w", err)
		}
		if err := gitRepo.SetReference(comment.ForcePushNewCommitGitRefName(), data.CommitIDs[1]); err != nil {
			return nil, fmt.Errorf("failed to update ref in repository for force-push comment: %w", err)
		}
	}

	return comment, err
}
