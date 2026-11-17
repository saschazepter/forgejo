// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package activities

import (
	"context"
	"fmt"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/container"
	"forgejo.org/modules/log"

	"xorm.io/builder"
)

// FindNotificationOptions represent the filters for notifications. If an ID is 0 it will be ignored.
type FindNotificationOptions struct {
	db.ListOptions
	UserID            int64
	RepoID            int64
	IssueID           int64
	ReleaseID         int64
	Status            []NotificationStatus
	Source            []NotificationSource
	UpdatedAfterUnix  int64
	UpdatedBeforeUnix int64
}

// ToCond will convert each condition into a xorm-Cond
func (opts FindNotificationOptions) ToConds() builder.Cond {
	cond := builder.NewCond()
	if opts.UserID != 0 {
		cond = cond.And(builder.Eq{"notification.user_id": opts.UserID})
	}
	if opts.RepoID != 0 {
		cond = cond.And(builder.Eq{"notification.repo_id": opts.RepoID})
	}
	if opts.IssueID != 0 {
		cond = cond.And(builder.Eq{"notification.issue_id": opts.IssueID})
	}
	if opts.ReleaseID != 0 {
		cond = cond.And(builder.Eq{"notification.release_id": opts.ReleaseID})
	}
	if len(opts.Status) > 0 {
		if len(opts.Status) == 1 {
			cond = cond.And(builder.Eq{"notification.status": opts.Status[0]})
		} else {
			cond = cond.And(builder.In("notification.status", opts.Status))
		}
	}
	if len(opts.Source) > 0 {
		cond = cond.And(builder.In("notification.source", opts.Source))
	}
	if opts.UpdatedAfterUnix != 0 {
		cond = cond.And(builder.Gte{"notification.updated_unix": opts.UpdatedAfterUnix})
	}
	if opts.UpdatedBeforeUnix != 0 {
		cond = cond.And(builder.Lte{"notification.updated_unix": opts.UpdatedBeforeUnix})
	}
	return cond
}

func (opts FindNotificationOptions) ToOrders() string {
	return "notification.updated_unix DESC"
}

// CreateOrUpdateIssueNotifications creates an issue notification
// for each watcher, or updates it if already exists
// receiverID > 0 just send to receiver, else send to all watcher
func CreateOrUpdateIssueNotifications(ctx context.Context, issueID, commentID, notificationAuthorID, receiverID int64) error {
	ctx, committer, err := db.TxContext(ctx)
	if err != nil {
		return err
	}
	defer committer.Close()

	if err := createOrUpdateIssueNotifications(ctx, issueID, commentID, notificationAuthorID, receiverID); err != nil {
		return err
	}

	return committer.Commit()
}

func createOrUpdateIssueNotifications(ctx context.Context, issueID, commentID, notificationAuthorID, receiverID int64) error {
	// init
	var toNotify container.Set[int64]
	notifications, err := db.Find[Notification](ctx, FindNotificationOptions{
		IssueID: issueID,
	})
	if err != nil {
		return err
	}

	issue, err := issues_model.GetIssueByID(ctx, issueID)
	if err != nil {
		return err
	}

	if receiverID > 0 {
		toNotify = make(container.Set[int64], 1)
		toNotify.Add(receiverID)
	} else {
		toNotify = make(container.Set[int64], 32)
		issueWatches, err := issues_model.GetIssueWatchersIDs(ctx, issueID, true)
		if err != nil {
			return err
		}
		toNotify.AddMultiple(issueWatches...)
		if !issue.IsPull || !issues_model.HasWorkInProgressPrefix(issue.Title) {
			repoWatches, err := repo_model.GetSelectWatcherIDs(ctx, issue.RepoID, repo_model.WatchSelection{Issues: !issue.IsPull, PullRequests: issue.IsPull, Releases: false})
			if err != nil {
				return err
			}
			toNotify.AddMultiple(repoWatches...)
		}
		issueParticipants, err := issue.GetParticipantIDsByIssue(ctx)
		if err != nil {
			return err
		}
		toNotify.AddMultiple(issueParticipants...)

		// dont notify user who cause notification
		delete(toNotify, notificationAuthorID)
		// explicit unwatch on issue
		issueUnWatches, err := issues_model.GetIssueWatchersIDs(ctx, issueID, false)
		if err != nil {
			return err
		}
		for _, id := range issueUnWatches {
			toNotify.Remove(id)
		}
		// Remove users who have the notification author blocked.
		blockedAuthorIDs, err := user_model.ListBlockedByUsersID(ctx, notificationAuthorID)
		if err != nil {
			return err
		}
		for _, id := range blockedAuthorIDs {
			toNotify.Remove(id)
		}
	}

	err = issue.LoadRepo(ctx)
	if err != nil {
		return err
	}

	// notify
	for userID := range toNotify {
		issue.Repo.Units = nil
		user, err := user_model.GetUserByID(ctx, userID)
		if err != nil {
			if user_model.IsErrUserNotExist(err) {
				continue
			}

			return err
		}
		if issue.IsPull && !access_model.CheckRepoUnitUser(ctx, issue.Repo, user, unit.TypePullRequests) {
			continue
		}
		if !issue.IsPull && !access_model.CheckRepoUnitUser(ctx, issue.Repo, user, unit.TypeIssues) {
			continue
		}

		if notificationExists(notifications, issue.ID, userID) {
			if err = updateIssueNotification(ctx, userID, issue.ID, commentID); err != nil {
				return err
			}
			continue
		}
		if err = createIssueNotification(ctx, userID, issue, commentID); err != nil {
			return err
		}
	}
	return nil
}

func CreateOrUpdateReleaseNotifications(ctx context.Context, releaseID int64) error {
	release, err := repo_model.GetReleaseByID(ctx, releaseID)
	if err != nil {
		return fmt.Errorf("GetReleaseByID: %w", err)
	}

	err = release.LoadRepo(ctx)
	if err != nil {
		return fmt.Errorf("LoadRepo: %w", err)
	}

	watcherIDList, err := repo_model.GetSelectWatcherIDs(ctx, release.RepoID, repo_model.WatchSelection{Issues: false, PullRequests: false, Releases: true})
	if err != nil {
		return fmt.Errorf("GetSelectWatcherIDs: %w", err)
	}

	return db.WithTx(ctx, func(ctx context.Context) error {
		for _, userID := range watcherIDList {
			if userID != release.PublisherID {
				err := createOrUpdateReleaseNotificationForUser(ctx, userID, release)
				if err != nil {
					log.Error("Can't update notification for release %d for user %s: %v", release.ID, userID, err)
				}
			}
		}

		return nil
	})
}

// NotificationList contains a list of notifications
type NotificationList []*Notification

// LoadAttributes load Repo Issue User and Comment if not loaded
func (nl NotificationList) LoadAttributes(ctx context.Context) error {
	if _, _, err := nl.LoadRepos(ctx); err != nil {
		return err
	}
	if _, err := nl.LoadIssues(ctx); err != nil {
		return err
	}
	if _, err := nl.LoadUsers(ctx); err != nil {
		return err
	}
	if _, err := nl.LoadComments(ctx); err != nil {
		return err
	}
	if _, err := nl.LoadReleases(ctx); err != nil {
		return fmt.Errorf("LoadReleases: %w", err)
	}
	return nil
}

// getRepoIDsToLoad returns all the repositoty ids which haven't been loaded
func (nl NotificationList) getRepoIDsToLoad() []int64 {
	return container.FilterSlice(nl, func(n *Notification) (int64, bool) {
		return n.RepoID, n.Repository == nil
	})
}

// LoadRepos loads repositories from database
func (nl NotificationList) LoadRepos(ctx context.Context) (repo_model.RepositoryList, []int, error) {
	if len(nl) == 0 {
		return repo_model.RepositoryList{}, []int{}, nil
	}

	repoIDs := nl.getRepoIDsToLoad()
	repos, err := db.GetByIDs(ctx, "id", repoIDs, &repo_model.Repository{})
	if err != nil {
		return nil, nil, err
	}

	failed := []int{}

	reposList := make(repo_model.RepositoryList, 0, len(repoIDs))
	for i, notification := range nl {
		if notification.Repository == nil {
			notification.Repository = repos[notification.RepoID]
		}
		if notification.Repository == nil {
			log.Error("Notification[%d]: RepoID: %d not found", notification.ID, notification.RepoID)
			failed = append(failed, i)
			continue
		}
		var found bool
		for _, r := range reposList {
			if r.ID == notification.RepoID {
				found = true
				break
			}
		}
		if !found {
			reposList = append(reposList, notification.Repository)
		}
	}
	return reposList, failed, nil
}

func (nl NotificationList) getIssueIDsToLoad() []int64 {
	ids := make(container.Set[int64], len(nl))
	for _, notification := range nl {
		if notification.Issue != nil {
			continue
		}
		ids.Add(notification.IssueID)
	}
	return ids.Values()
}

// LoadIssues loads issues from database
func (nl NotificationList) LoadIssues(ctx context.Context) ([]int, error) {
	if len(nl) == 0 {
		return []int{}, nil
	}

	issueIDs := nl.getIssueIDsToLoad()
	issues, err := db.GetByIDs(ctx, "id", issueIDs, &issues_model.Issue{})
	if err != nil {
		return nil, err
	}

	failures := []int{}

	for i, notification := range nl {
		if notification.Issue == nil {
			notification.Issue = issues[notification.IssueID]
			if notification.Issue == nil {
				if notification.IssueID != 0 {
					log.Error("Notification[%d]: IssueID: %d Not Found", notification.ID, notification.IssueID)
					failures = append(failures, i)
				}
				continue
			}
			notification.Issue.Repo = notification.Repository
		}
	}
	return failures, nil
}

// Without returns the notification list without the failures
func (nl NotificationList) Without(failures []int) NotificationList {
	if len(failures) == 0 {
		return nl
	}
	remaining := make([]*Notification, 0, len(nl))
	last := -1
	var i int
	for _, i = range failures {
		remaining = append(remaining, nl[last+1:i]...)
		last = i
	}
	if len(nl) > i {
		remaining = append(remaining, nl[i+1:]...)
	}
	return remaining
}

func (nl NotificationList) getCommentIDsToLoad() []int64 {
	ids := make(container.Set[int64], len(nl))
	for _, notification := range nl {
		if notification.CommentID == 0 || notification.Comment != nil {
			continue
		}
		ids.Add(notification.CommentID)
	}
	return ids.Values()
}

func (nl NotificationList) getUserIDs() []int64 {
	ids := make(container.Set[int64], len(nl))
	for _, notification := range nl {
		if notification.UserID == 0 || notification.User != nil {
			continue
		}
		ids.Add(notification.UserID)
	}
	return ids.Values()
}

// LoadUsers loads users from database
func (nl NotificationList) LoadUsers(ctx context.Context) ([]int, error) {
	if len(nl) == 0 {
		return []int{}, nil
	}

	userIDs := nl.getUserIDs()
	users, err := db.GetByIDs(ctx, "id", userIDs, &user_model.User{})
	if err != nil {
		return nil, err
	}

	failures := []int{}
	for i, notification := range nl {
		if notification.UserID > 0 && notification.User == nil && users[notification.UserID] != nil {
			notification.User = users[notification.UserID]
			if notification.User == nil {
				log.Error("Notification[%d]: UserID[%d] failed to load", notification.ID, notification.UserID)
				failures = append(failures, i)
				continue
			}
		}
	}
	return failures, nil
}

// LoadComments loads comments from database
func (nl NotificationList) LoadComments(ctx context.Context) ([]int, error) {
	if len(nl) == 0 {
		return []int{}, nil
	}

	commentIDs := nl.getCommentIDsToLoad()
	comments, err := db.GetByIDs(ctx, "id", commentIDs, &issues_model.Comment{})
	if err != nil {
		return nil, err
	}

	failures := []int{}
	for i, notification := range nl {
		if notification.CommentID > 0 && notification.Comment == nil && comments[notification.CommentID] != nil {
			notification.Comment = comments[notification.CommentID]
			if notification.Comment == nil {
				log.Error("Notification[%d]: CommentID[%d] failed to load", notification.ID, notification.CommentID)
				failures = append(failures, i)
				continue
			}
			notification.Comment.Issue = notification.Issue
		}
	}
	return failures, nil
}

func (nl NotificationList) getReleaseIDsToLoad() []int64 {
	ids := make(container.Set[int64], len(nl))
	for _, notification := range nl {
		if !notification.ReleaseID.Has() || notification.Release != nil {
			continue
		}
		ids.Add(notification.ReleaseID.ValueOrZeroValue())
	}
	return ids.Values()
}

// LoadReleases loads releases from database
func (nl NotificationList) LoadReleases(ctx context.Context) ([]int, error) {
	if len(nl) == 0 {
		return []int{}, nil
	}

	releaseIDs := nl.getReleaseIDsToLoad()
	releases, err := db.GetByIDs(ctx, "id", releaseIDs, &repo_model.Release{})
	if err != nil {
		return nil, fmt.Errorf("GetByIDs[Release]: %w", err)
	}

	failures := []int{}

	for i, notification := range nl {
		if notification.Release == nil && notification.ReleaseID.Has() {
			releaseID := notification.ReleaseID.ValueOrZeroValue()
			notification.Release = releases[releaseID]
			if notification.Release == nil {
				log.Error("Notification[%d]: ReleaseID: %d Not Found", notification.ID, notification.ReleaseID)
				failures = append(failures, i)
				continue
			}
			notification.Release.Repo = notification.Repository
		}
	}
	return failures, nil
}
