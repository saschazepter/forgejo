// Copyright 2016 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package activities

import (
	"context"
	"fmt"
	"strconv"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	"forgejo.org/models/organization"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"

	"xorm.io/builder"
)

type (
	// NotificationStatus is the status of the notification (read or unread)
	NotificationStatus uint8
	// NotificationSource is the source of the notification (issue, PR, commit, etc)
	NotificationSource uint8
)

const (
	// NotificationStatusUnread represents an unread notification
	NotificationStatusUnread NotificationStatus = iota + 1
	// NotificationStatusRead represents a read notification
	NotificationStatusRead
	// NotificationStatusPinned represents a pinned notification
	NotificationStatusPinned
)

const (
	// NotificationSourceIssue is a notification of an issue
	NotificationSourceIssue NotificationSource = iota + 1
	// NotificationSourcePullRequest is a notification of a pull request
	NotificationSourcePullRequest
	// NotificationSourceCommit is a notification of a commit
	NotificationSourceCommit
	// NotificationSourceRepository is a notification for a repository
	NotificationSourceRepository
	// NotificationSourceRelease is a notification for a release
	NotificationSourceRelease
)

// Notification represents a notification
type Notification struct {
	ID     int64 `xorm:"pk autoincr"`
	UserID int64 `xorm:"NOT NULL INDEX(s)"`
	RepoID int64 `xorm:"INDEX NOT NULL"`

	Status NotificationStatus `xorm:"SMALLINT NOT NULL INDEX(s)"`
	Source NotificationSource `xorm:"SMALLINT INDEX NOT NULL"`

	IssueID   int64 `xorm:"INDEX NOT NULL"`
	CommentID int64

	ReleaseID optional.Option[int64] `xorm:"INDEX REFERENCES(release, id)"`

	Issue      *issues_model.Issue    `xorm:"-"`
	Repository *repo_model.Repository `xorm:"-"`
	Comment    *issues_model.Comment  `xorm:"-"`
	User       *user_model.User       `xorm:"-"`
	Release    *repo_model.Release    `xorm:"-"`

	CreatedUnix timeutil.TimeStamp `xorm:"created NOT NULL"`
	UpdatedUnix timeutil.TimeStamp `xorm:"updated INDEX NOT NULL"`
}

func init() {
	db.RegisterModel(new(Notification))
}

// CreateRepoTransferNotification creates  notification for the user a repository was transferred to
func CreateRepoTransferNotification(ctx context.Context, doer, newOwner *user_model.User, repo *repo_model.Repository) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		var notify []*Notification

		if newOwner.IsOrganization() {
			users, err := organization.GetUsersWhoCanCreateOrgRepo(ctx, newOwner.ID)
			if err != nil || len(users) == 0 {
				return err
			}
			for i := range users {
				notify = append(notify, &Notification{
					UserID: i,
					RepoID: repo.ID,
					Status: NotificationStatusUnread,
					Source: NotificationSourceRepository,
				})
			}
		} else {
			notify = []*Notification{{
				UserID: newOwner.ID,
				RepoID: repo.ID,
				Status: NotificationStatusUnread,
				Source: NotificationSourceRepository,
			}}
		}

		return db.Insert(ctx, notify)
	})
}

func createIssueNotification(ctx context.Context, userID int64, issue *issues_model.Issue, commentID int64) error {
	notification := &Notification{
		UserID:    userID,
		RepoID:    issue.RepoID,
		Status:    NotificationStatusUnread,
		IssueID:   issue.ID,
		CommentID: commentID,
	}

	if issue.IsPull {
		notification.Source = NotificationSourcePullRequest
	} else {
		notification.Source = NotificationSourceIssue
	}

	return db.Insert(ctx, notification)
}

func updateIssueNotification(ctx context.Context, userID, issueID, commentID int64) error {
	notification, err := GetIssueNotification(ctx, userID, issueID)
	if err != nil {
		return err
	}

	// If the notification is not yet read, then only update the update_unix column,
	// so that the notification does not point to a newer comment.
	if notification.Status != NotificationStatusRead {
		notification.UpdatedUnix = timeutil.TimeStampNow()
		_, err = db.GetEngine(ctx).ID(notification.ID).Cols("updated_unix").NoAutoTime().Update(notification)
		return err
	}

	notification.Status = NotificationStatusUnread
	notification.CommentID = commentID
	_, err = db.GetEngine(ctx).ID(notification.ID).Cols("status", "comment_id").Update(notification)
	return err
}

// GetIssueNotification return the notification about an issue
func GetIssueNotification(ctx context.Context, userID, issueID int64) (*Notification, error) {
	notification := new(Notification)
	_, err := db.GetEngine(ctx).
		Where("user_id = ?", userID).
		And("issue_id = ?", issueID).
		Get(notification)
	return notification, err
}

func createOrUpdateReleaseNotificationForUser(ctx context.Context, userID int64, release *repo_model.Release) error {
	user, err := user_model.GetUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("GetUserByID [%d]: %w", userID, err)
	}

	if !access_model.CheckRepoUnitUser(ctx, release.Repo, user, unit.TypeReleases) {
		return nil
	}

	notification := new(Notification)
	has, err := db.GetEngine(ctx).
		Where("user_id = ?", userID).
		And("release_id = ?", release.ID).
		Get(notification)
	if err != nil {
		return fmt.Errorf("Could not check if a notification for this release already exists: %w", err)
	}

	if has {
		notification.Status = NotificationStatusUnread
		notification.UpdatedUnix = timeutil.TimeStampNow()
		_, err = db.GetEngine(ctx).ID(notification.ID).Cols("status", "updated_unix").NoAutoTime().Update(notification)
		return fmt.Errorf("Could not mark an existing release notification as unread: %w", err)
	}

	notification.UserID = userID
	notification.RepoID = release.RepoID
	notification.ReleaseID = optional.Some(release.ID)
	notification.Status = NotificationStatusUnread
	notification.Source = NotificationSourceRelease
	_, err = db.GetEngine(ctx).Insert(notification)
	if err != nil {
		return fmt.Errorf("Could not create a new notification for a release: %w", err)
	}
	return nil
}

// GetReleaseNotification return the notification about a release
func GetReleaseNotification(ctx context.Context, userID, releaseID int64) (*Notification, bool, error) {
	return db.Get[Notification](ctx, FindNotificationOptions{UserID: userID, ReleaseID: releaseID}.ToConds())
}

// LoadAttributes load Repo Issue User and Comment if not loaded
func (n *Notification) LoadAttributes(ctx context.Context) (err error) {
	if err = n.loadRepo(ctx); err != nil {
		return err
	}
	if err = n.loadIssue(ctx); err != nil {
		return err
	}
	if err = n.loadUser(ctx); err != nil {
		return err
	}
	if err = n.loadComment(ctx); err != nil {
		return err
	}
	if err = n.loadRelease(ctx); err != nil {
		return fmt.Errorf("loadRelease: %w", err)
	}
	return err
}

func (n *Notification) loadRepo(ctx context.Context) (err error) {
	if n.Repository == nil {
		n.Repository, err = repo_model.GetRepositoryByID(ctx, n.RepoID)
		if err != nil {
			return fmt.Errorf("getRepositoryByID [%d]: %w", n.RepoID, err)
		}
	}
	return nil
}

func (n *Notification) loadIssue(ctx context.Context) (err error) {
	if n.Issue == nil && n.IssueID != 0 {
		n.Issue, err = issues_model.GetIssueByID(ctx, n.IssueID)
		if err != nil {
			return fmt.Errorf("getIssueByID [%d]: %w", n.IssueID, err)
		}
		return n.Issue.LoadAttributes(ctx)
	}
	return nil
}

func (n *Notification) loadComment(ctx context.Context) (err error) {
	if n.Comment == nil && n.CommentID != 0 {
		n.Comment, err = issues_model.GetCommentByID(ctx, n.CommentID)
		if err != nil {
			if issues_model.IsErrCommentNotExist(err) {
				return issues_model.ErrCommentNotExist{
					ID:      n.CommentID,
					IssueID: n.IssueID,
				}
			}
			return err
		}
	}
	return nil
}

func (n *Notification) loadUser(ctx context.Context) (err error) {
	if n.User == nil {
		n.User, err = user_model.GetUserByID(ctx, n.UserID)
		if err != nil {
			return fmt.Errorf("getUserByID [%d]: %w", n.UserID, err)
		}
	}
	return nil
}

func (n *Notification) loadRelease(ctx context.Context) (err error) {
	releaseID := n.ReleaseID.ValueOrZeroValue()
	if n.Release == nil && releaseID != 0 {
		n.Release, err = repo_model.GetReleaseByID(ctx, releaseID)
		if err != nil {
			return fmt.Errorf("GetReleaseByID [%d]: %w", releaseID, err)
		}

		err = n.loadRepo(ctx)
		if err != nil {
			return fmt.Errorf("loadRepo: %w", err)
		}

		n.Release.Repo = n.Repository
	}
	return nil
}

// GetRepo returns the repo of the notification
func (n *Notification) GetRepo(ctx context.Context) (*repo_model.Repository, error) {
	return n.Repository, n.loadRepo(ctx)
}

// GetIssue returns the issue of the notification
func (n *Notification) GetIssue(ctx context.Context) (*issues_model.Issue, error) {
	return n.Issue, n.loadIssue(ctx)
}

// HTMLURL formats a URL-string to the notification
func (n *Notification) HTMLURL(ctx context.Context) string {
	switch n.Source {
	case NotificationSourceIssue, NotificationSourcePullRequest:
		if n.Comment != nil {
			return n.Comment.HTMLURL(ctx)
		}
		return n.Issue.HTMLURL()
	case NotificationSourceRepository:
		return n.Repository.HTMLURL()
	case NotificationSourceRelease:
		return n.Release.HTMLURL()
	}
	return ""
}

// Link formats a relative URL-string to the notification
func (n *Notification) Link(ctx context.Context) string {
	switch n.Source {
	case NotificationSourceIssue, NotificationSourcePullRequest:
		if n.Comment != nil {
			return n.Comment.Link(ctx)
		}
		return n.Issue.Link()
	case NotificationSourceRepository:
		return n.Repository.Link()
	case NotificationSourceRelease:
		return n.Release.Link()
	}
	return ""
}

// APIURL formats a URL-string to the notification
func (n *Notification) APIURL() string {
	return setting.AppURL + "api/v1/notifications/threads/" + strconv.FormatInt(n.ID, 10)
}

func notificationExists(notifications []*Notification, issueID, userID int64) bool {
	for _, notification := range notifications {
		if notification.IssueID == issueID && notification.UserID == userID {
			return true
		}
	}

	return false
}

// UserIDCount is a simple coalition of UserID and Count
type UserIDCount struct {
	UserID int64
	Count  int64
}

// GetUIDsAndNotificationCounts returns the unread counts for every user between the two provided times.
// It must return all user IDs which appear during the period, including count=0 for users who have read all.
func GetUIDsAndNotificationCounts(ctx context.Context, since, until timeutil.TimeStamp) ([]UserIDCount, error) {
	sql := `SELECT user_id, sum(case when status= ? then 1 else 0 end) AS count FROM notification ` +
		`WHERE user_id IN (SELECT user_id FROM notification WHERE updated_unix >= ? AND ` +
		`updated_unix < ?) GROUP BY user_id`
	var res []UserIDCount
	return res, db.GetEngine(ctx).SQL(sql, NotificationStatusUnread, since, until).Find(&res)
}

// SetIssueReadBy sets issue to be read by given user.
func SetIssueReadBy(ctx context.Context, issueID, userID int64) error {
	if err := issues_model.UpdateIssueUserByRead(ctx, userID, issueID); err != nil {
		return err
	}

	return setIssueNotificationStatusReadIfUnread(ctx, userID, issueID)
}

func setIssueNotificationStatusReadIfUnread(ctx context.Context, userID, issueID int64) error {
	notification, err := GetIssueNotification(ctx, userID, issueID)
	// ignore if not exists
	if err != nil {
		return nil
	}

	if notification.Status != NotificationStatusUnread {
		return nil
	}

	notification.Status = NotificationStatusRead

	_, err = db.GetEngine(ctx).ID(notification.ID).Cols("status").Update(notification)
	return err
}

// SetRepoReadBy sets repo to be visited by given user.
func SetRepoReadBy(ctx context.Context, userID, repoID int64) error {
	_, err := db.GetEngine(ctx).Where(builder.Eq{
		"user_id": userID,
		"status":  NotificationStatusUnread,
		"source":  NotificationSourceRepository,
		"repo_id": repoID,
	}).Cols("status").Update(&Notification{Status: NotificationStatusRead})
	return err
}

// SetReleaseReadBy sets release to be read by given user.
func SetReleaseReadBy(ctx context.Context, releaseID, userID int64) error {
	notification, found, err := GetReleaseNotification(ctx, userID, releaseID)
	if err != nil {
		return fmt.Errorf("GetReleaseNotification: %w", err)
	}

	if !found || notification.Status != NotificationStatusUnread {
		return nil
	}

	notification.Status = NotificationStatusRead

	_, err = db.GetEngine(ctx).ID(notification.ID).Cols("status").Update(notification)
	if err != nil {
		return fmt.Errorf("Could not update the notification status: %w", err)
	}
	return nil
}

// SetNotificationStatus change the notification status
func SetNotificationStatus(ctx context.Context, notificationID int64, user *user_model.User, status NotificationStatus) (*Notification, error) {
	notification, err := GetNotificationByID(ctx, notificationID)
	if err != nil {
		return notification, err
	}

	if notification.UserID != user.ID {
		return nil, fmt.Errorf("Can't change notification of another user: %d, %d", notification.UserID, user.ID)
	}

	notification.Status = status

	_, err = db.GetEngine(ctx).ID(notificationID).Update(notification)
	return notification, err
}

// GetNotificationByID return notification by ID
func GetNotificationByID(ctx context.Context, notificationID int64) (*Notification, error) {
	notification := new(Notification)
	ok, err := db.GetEngine(ctx).
		Where("id = ?", notificationID).
		Get(notification)
	if err != nil {
		return nil, err
	}

	if !ok {
		return nil, db.ErrNotExist{Resource: "notification", ID: notificationID}
	}

	return notification, nil
}

// UpdateNotificationStatuses updates the statuses of all of a user's notifications that are of the currentStatus type to the desiredStatus
func UpdateNotificationStatuses(ctx context.Context, user *user_model.User, currentStatus, desiredStatus NotificationStatus) error {
	n := &Notification{Status: desiredStatus}
	_, err := db.GetEngine(ctx).
		Where("user_id = ? AND status = ?", user.ID, currentStatus).
		Cols("status", "updated_unix").
		Update(n)
	return err
}

// LoadIssuePullRequests loads all issues' pull requests if possible
func (nl NotificationList) LoadIssuePullRequests(ctx context.Context) error {
	issues := make(map[int64]*issues_model.Issue, len(nl))
	for _, notification := range nl {
		if notification.Issue != nil && notification.Issue.IsPull && notification.Issue.PullRequest == nil {
			issues[notification.Issue.ID] = notification.Issue
		}
	}

	if len(issues) == 0 {
		return nil
	}

	pulls, err := issues_model.GetPullRequestByIssueIDs(ctx, util.KeysOfMap(issues))
	if err != nil {
		return err
	}

	for _, pull := range pulls {
		if issue := issues[pull.IssueID]; issue != nil {
			issue.PullRequest = pull
			issue.PullRequest.Issue = issue
		}
	}

	return nil
}
