// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issue

import (
	"context"
	"errors"
	"fmt"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	user_model "forgejo.org/models/user"
	notify_service "forgejo.org/services/notify"
)

func updateMilestoneCounters(ctx context.Context, issue *issues_model.Issue, id int64) error {
	if issue.NoAutoTime {
		// We set the milestone's update date to the max of the
		// milestone and issue update dates.
		// Note: we can not call UpdateMilestoneCounters() if the
		// milestone's update date is to be kept, because that function
		// auto-updates the dates.
		milestone, err := issues_model.GetMilestoneByRepoID(ctx, issue.RepoID, id)
		if err != nil {
			return fmt.Errorf("GetMilestoneByRepoID: %w", err)
		}
		updatedUnix := milestone.UpdatedUnix
		if issue.UpdatedUnix > updatedUnix {
			updatedUnix = issue.UpdatedUnix
		}
		if err := issues_model.UpdateMilestoneCountersWithDate(ctx, id, updatedUnix); err != nil {
			return err
		}
	} else {
		if err := issues_model.UpdateMilestoneCounters(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func changeMilestoneAssign(ctx context.Context, doer *user_model.User, issue *issues_model.Issue, oldMilestoneID int64) error {
	// Only check if milestone exists if we don't remove it.
	if issue.MilestoneID > 0 {
		has, err := issues_model.HasMilestoneByRepoID(ctx, issue.RepoID, issue.MilestoneID)
		if err != nil {
			return fmt.Errorf("HasMilestoneByRepoID: %w", err)
		}
		if !has {
			return errors.New("HasMilestoneByRepoID: issue doesn't exist")
		}
	}

	if err := issues_model.UpdateIssueCols(ctx, issue, "milestone_id"); err != nil {
		return err
	}

	if oldMilestoneID > 0 {
		if err := updateMilestoneCounters(ctx, issue, oldMilestoneID); err != nil {
			return err
		}
	}

	if issue.MilestoneID > 0 {
		if err := updateMilestoneCounters(ctx, issue, issue.MilestoneID); err != nil {
			return err
		}
	}

	if oldMilestoneID > 0 || issue.MilestoneID > 0 {
		if err := issue.LoadRepo(ctx); err != nil {
			return err
		}

		opts := &issues_model.CreateCommentOptions{
			Type:           issues_model.CommentTypeMilestone,
			Doer:           doer,
			Repo:           issue.Repo,
			Issue:          issue,
			OldMilestoneID: oldMilestoneID,
			MilestoneID:    issue.MilestoneID,
		}
		if _, err := issues_model.CreateComment(ctx, opts); err != nil {
			return err
		}
	}

	if issue.MilestoneID == 0 {
		issue.Milestone = nil
	}

	return nil
}

// ChangeMilestoneAssign changes assignment of milestone for issue.
func ChangeMilestoneAssign(ctx context.Context, issue *issues_model.Issue, doer *user_model.User, oldMilestoneID int64) (err error) {
	dbCtx, committer, err := db.TxContext(ctx)
	if err != nil {
		return err
	}
	defer committer.Close()

	if err = changeMilestoneAssign(dbCtx, doer, issue, oldMilestoneID); err != nil {
		return err
	}

	if err = committer.Commit(); err != nil {
		return fmt.Errorf("Commit: %w", err)
	}

	notify_service.IssueChangeMilestone(ctx, doer, issue, oldMilestoneID)

	return nil
}
