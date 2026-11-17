// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issues

import (
	"context"
	"errors"

	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/container"
	"forgejo.org/modules/log"
)

// CommentList defines a list of comments
type CommentList []*Comment

// LoadPosters loads posters
func (comments CommentList) LoadPosters(ctx context.Context) error {
	if len(comments) == 0 {
		return nil
	}

	posterIDs := container.FilterSlice(comments, func(c *Comment) (int64, bool) {
		return c.PosterID, c.Poster == nil && user_model.IsValidUserID(c.PosterID)
	})

	posterMaps, err := getPostersByIDs(ctx, posterIDs)
	if err != nil {
		return err
	}

	for _, comment := range comments {
		if comment.Poster == nil {
			comment.PosterID, comment.Poster = user_model.GetUserFromMap(comment.PosterID, posterMaps)
		}
	}
	return nil
}

func (comments CommentList) getLabelIDs() []int64 {
	return container.FilterSlice(comments, func(comment *Comment) (int64, bool) {
		return comment.LabelID, comment.LabelID > 0
	})
}

func (comments CommentList) loadLabels(ctx context.Context) error {
	if len(comments) == 0 {
		return nil
	}

	labelIDs := comments.getLabelIDs()
	commentLabels, err := db.GetByIDs(ctx, "id", labelIDs, &Label{})
	if err != nil {
		return err
	}

	for _, comment := range comments {
		comment.Label = commentLabels[comment.LabelID]
	}
	return nil
}

func (comments CommentList) getMilestoneIDs() []int64 {
	return container.FilterSlice(comments, func(comment *Comment) (int64, bool) {
		return comment.MilestoneID, comment.MilestoneID > 0
	})
}

func (comments CommentList) loadMilestones(ctx context.Context) error {
	if len(comments) == 0 {
		return nil
	}

	milestoneIDs := comments.getMilestoneIDs()
	if len(milestoneIDs) == 0 {
		return nil
	}

	milestones, err := db.GetByIDs(ctx, "id", milestoneIDs, &Milestone{})
	if err != nil {
		return err
	}

	for _, comment := range comments {
		comment.Milestone = milestones[comment.MilestoneID]
	}
	return nil
}

func (comments CommentList) getOldMilestoneIDs() []int64 {
	return container.FilterSlice(comments, func(comment *Comment) (int64, bool) {
		return comment.OldMilestoneID, comment.OldMilestoneID > 0
	})
}

func (comments CommentList) loadOldMilestones(ctx context.Context) error {
	if len(comments) == 0 {
		return nil
	}

	milestoneIDs := comments.getOldMilestoneIDs()
	if len(milestoneIDs) == 0 {
		return nil
	}

	milestones, err := db.GetByIDs(ctx, "id", milestoneIDs, &Milestone{})
	if err != nil {
		return err
	}

	for _, comment := range comments {
		comment.OldMilestone = milestones[comment.OldMilestoneID]
	}
	return nil
}

func (comments CommentList) getAssigneeIDs() []int64 {
	return container.FilterSlice(comments, func(comment *Comment) (int64, bool) {
		return comment.AssigneeID, user_model.IsValidUserID(comment.AssigneeID)
	})
}

func (comments CommentList) loadAssignees(ctx context.Context) error {
	if len(comments) == 0 {
		return nil
	}

	assigneeIDs := comments.getAssigneeIDs()
	assignees, err := db.GetByIDs(ctx, "id", assigneeIDs, &user_model.User{})
	if err != nil {
		return err
	}

	for _, comment := range comments {
		comment.AssigneeID, comment.Assignee = user_model.GetUserFromMap(comment.AssigneeID, assignees)
	}
	return nil
}

// getIssueIDs returns all the issue ids on this comment list which issue hasn't been loaded
func (comments CommentList) getIssueIDs() []int64 {
	return container.FilterSlice(comments, func(comment *Comment) (int64, bool) {
		return comment.IssueID, comment.Issue == nil
	})
}

// Issues returns all the issues of comments
func (comments CommentList) Issues() IssueList {
	issues := make(map[int64]*Issue, len(comments))
	for _, comment := range comments {
		if comment.Issue != nil {
			if _, ok := issues[comment.Issue.ID]; !ok {
				issues[comment.Issue.ID] = comment.Issue
			}
		}
	}

	issueList := make([]*Issue, 0, len(issues))
	for _, issue := range issues {
		issueList = append(issueList, issue)
	}
	return issueList
}

// LoadIssues loads issues of comments
func (comments CommentList) LoadIssues(ctx context.Context) error {
	if len(comments) == 0 {
		return nil
	}

	issueIDs := comments.getIssueIDs()
	issues, err := db.GetByIDs(ctx, "id", issueIDs, &Issue{})
	if err != nil {
		return err
	}

	for _, comment := range comments {
		if comment.Issue == nil {
			comment.Issue = issues[comment.IssueID]
		}
	}
	return nil
}

func (comments CommentList) getDependentIssueIDs() []int64 {
	return container.FilterSlice(comments, func(comment *Comment) (int64, bool) {
		if comment.DependentIssue != nil {
			return 0, false
		}
		return comment.DependentIssueID, comment.DependentIssueID > 0
	})
}

func (comments CommentList) loadDependentIssues(ctx context.Context) error {
	if len(comments) == 0 {
		return nil
	}

	issueIDs := comments.getDependentIssueIDs()
	issues, err := db.GetByIDs(ctx, "id", issueIDs, &Issue{})
	if err != nil {
		return err
	}

	for _, comment := range comments {
		if comment.DependentIssue == nil {
			comment.DependentIssue = issues[comment.DependentIssueID]
			if comment.DependentIssue != nil {
				if err := comment.DependentIssue.LoadRepo(ctx); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// getAttachmentCommentIDs only return the comment ids which possibly has attachments
func (comments CommentList) getAttachmentCommentIDs() []int64 {
	return container.FilterSlice(comments, func(comment *Comment) (int64, bool) {
		return comment.ID, comment.Type.HasAttachmentSupport()
	})
}

// LoadAttachmentsByIssue loads attachments by issue id
func (comments CommentList) LoadAttachmentsByIssue(ctx context.Context) error {
	if len(comments) == 0 {
		return nil
	}

	attachments := make([]*repo_model.Attachment, 0, len(comments)/2)
	if err := db.GetEngine(ctx).Where("issue_id=? AND comment_id>0", comments[0].IssueID).Find(&attachments); err != nil {
		return err
	}

	commentAttachmentsMap := make(map[int64][]*repo_model.Attachment, len(comments))
	for _, attach := range attachments {
		commentAttachmentsMap[attach.CommentID] = append(commentAttachmentsMap[attach.CommentID], attach)
	}

	for _, comment := range comments {
		comment.Attachments = commentAttachmentsMap[comment.ID]
	}
	return nil
}

// LoadAttachments loads attachments
func (comments CommentList) LoadAttachments(ctx context.Context) (err error) {
	if len(comments) == 0 {
		return nil
	}

	commentsIDs := comments.getAttachmentCommentIDs()
	attachments, err := db.GetByFieldIn(ctx, "comment_id", commentsIDs, &repo_model.Attachment{})
	if err != nil {
		return err
	}

	for _, comment := range comments {
		comment.Attachments = attachments[comment.ID]
	}
	return nil
}

func (comments CommentList) LoadResolveDoers(ctx context.Context) (err error) {
	relevant := func(c *Comment) bool {
		return c.ResolveDoerID != 0 && c.Type == CommentTypeCode
	}
	userIDs := make(container.Set[int64])
	for _, comment := range comments {
		if relevant(comment) {
			userIDs.Add(comment.ResolveDoerID)
		}
	}

	if len(userIDs) == 0 {
		return nil
	}

	userMap := make(map[int64]*user_model.User)
	users, err := user_model.GetUsersByIDs(ctx, userIDs.Slice())
	if err != nil {
		return err
	}
	for _, user := range users {
		userMap[user.ID] = user
	}

	for _, comment := range comments {
		if !relevant(comment) {
			continue
		}
		resolveDoer, ok := userMap[comment.ResolveDoerID]
		if !ok {
			comment.ResolveDoer = user_model.NewGhostUser()
		} else {
			comment.ResolveDoer = resolveDoer
		}
	}

	return nil
}

func (comments CommentList) LoadReactions(ctx context.Context, repo *repo_model.Repository) (err error) {
	loadIssueID := int64(0)
	loadCommentIDs := make([]int64, 0, len(comments))

	for _, comment := range comments {
		if loadIssueID == 0 {
			loadIssueID = comment.IssueID
		} else if loadIssueID != comment.IssueID {
			return errors.New("unable to load reactions from comments on different issues than each other")
		}
		if comment.Reactions == nil {
			loadCommentIDs = append(loadCommentIDs, comment.ID)
		}
	}

	if loadIssueID == 0 {
		return nil
	}

	reactions, err := getReactionsForComments(ctx, loadIssueID, loadCommentIDs)
	if err != nil {
		return err
	}

	allReactions := make(ReactionList, 0, len(reactions))
	for _, comment := range comments {
		if comment.Reactions == nil {
			comment.Reactions = reactions[comment.ID]
			allReactions = append(allReactions, comment.Reactions...)
		}
	}

	if _, err := allReactions.LoadUsers(ctx, repo); err != nil {
		return err
	}

	return nil
}

func (comments CommentList) getReviewIDs() []int64 {
	return container.FilterSlice(comments, func(comment *Comment) (int64, bool) {
		return comment.ReviewID, comment.ReviewID > 0
	})
}

func (comments CommentList) LoadReviews(ctx context.Context) error {
	if len(comments) == 0 {
		return nil
	}

	reviewIDs := comments.getReviewIDs()
	reviews := make(map[int64]*Review, len(reviewIDs))
	if err := db.GetEngine(ctx).In("id", reviewIDs).Find(&reviews); err != nil {
		return err
	}

	for _, comment := range comments {
		comment.Review = reviews[comment.ReviewID]
		if comment.Review == nil {
			// review request which has been replaced by actual reviews doesn't exist in database anymore, so don't log errors for them.
			if comment.ReviewID > 0 && comment.Type != CommentTypeReviewRequest {
				log.Error("comment with review id [%d] but has no review record", comment.ReviewID)
			}
			continue
		}

		// If the comment dismisses a review, we need to load the reviewer to show whose review has been dismissed.
		// Otherwise, the reviewer is the poster of the comment, so we don't need to load it.
		if comment.Type == CommentTypeDismissReview {
			if err := comment.Review.LoadReviewer(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// LoadAttributes loads attributes of the comments, except for attachments and
// comments
func (comments CommentList) LoadAttributes(ctx context.Context) (err error) {
	if err = comments.LoadPosters(ctx); err != nil {
		return err
	}

	if err = comments.loadLabels(ctx); err != nil {
		return err
	}

	if err = comments.loadMilestones(ctx); err != nil {
		return err
	}

	if err = comments.loadOldMilestones(ctx); err != nil {
		return err
	}

	if err = comments.loadAssignees(ctx); err != nil {
		return err
	}

	if err = comments.LoadAttachments(ctx); err != nil {
		return err
	}

	if err = comments.LoadReviews(ctx); err != nil {
		return err
	}

	if err = comments.LoadIssues(ctx); err != nil {
		return err
	}

	return comments.loadDependentIssues(ctx)
}
