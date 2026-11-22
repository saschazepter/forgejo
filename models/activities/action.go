// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package activities

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	"forgejo.org/models/organization"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/base"
	"forgejo.org/modules/container"
	"forgejo.org/modules/git"
	"forgejo.org/modules/json"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/structs"
	"forgejo.org/modules/timeutil"

	"code.forgejo.org/xorm/xorm/schemas"
	"xorm.io/builder"
)

// ActionType represents the type of an action.
type ActionType int

// Possible action types.
const (
	ActionCreateRepo                ActionType = iota + 1 // 1
	ActionRenameRepo                                      // 2
	ActionStarRepo                                        // 3
	ActionWatchRepo                                       // 4
	ActionCommitRepo                                      // 5
	ActionCreateIssue                                     // 6
	ActionCreatePullRequest                               // 7
	ActionTransferRepo                                    // 8
	ActionPushTag                                         // 9
	ActionCommentIssue                                    // 10
	ActionMergePullRequest                                // 11
	ActionCloseIssue                                      // 12
	ActionReopenIssue                                     // 13
	ActionClosePullRequest                                // 14
	ActionReopenPullRequest                               // 15
	ActionDeleteTag                                       // 16
	ActionDeleteBranch                                    // 17
	ActionMirrorSyncPush                                  // 18
	ActionMirrorSyncCreate                                // 19
	ActionMirrorSyncDelete                                // 20
	ActionApprovePullRequest                              // 21
	ActionRejectPullRequest                               // 22
	ActionCommentPull                                     // 23
	ActionPublishRelease                                  // 24
	ActionPullReviewDismissed                             // 25
	ActionPullRequestReadyForReview                       // 26
	ActionAutoMergePullRequest                            // 27
)

func (at ActionType) String() string {
	switch at {
	case ActionCreateRepo:
		return "create_repo"
	case ActionRenameRepo:
		return "rename_repo"
	case ActionStarRepo:
		return "star_repo"
	case ActionWatchRepo:
		return "watch_repo"
	case ActionCommitRepo:
		return "commit_repo"
	case ActionCreateIssue:
		return "create_issue"
	case ActionCreatePullRequest:
		return "create_pull_request"
	case ActionTransferRepo:
		return "transfer_repo"
	case ActionPushTag:
		return "push_tag"
	case ActionCommentIssue:
		return "comment_issue"
	case ActionMergePullRequest:
		return "merge_pull_request"
	case ActionCloseIssue:
		return "close_issue"
	case ActionReopenIssue:
		return "reopen_issue"
	case ActionClosePullRequest:
		return "close_pull_request"
	case ActionReopenPullRequest:
		return "reopen_pull_request"
	case ActionDeleteTag:
		return "delete_tag"
	case ActionDeleteBranch:
		return "delete_branch"
	case ActionMirrorSyncPush:
		return "mirror_sync_push"
	case ActionMirrorSyncCreate:
		return "mirror_sync_create"
	case ActionMirrorSyncDelete:
		return "mirror_sync_delete"
	case ActionApprovePullRequest:
		return "approve_pull_request"
	case ActionRejectPullRequest:
		return "reject_pull_request"
	case ActionCommentPull:
		return "comment_pull"
	case ActionPublishRelease:
		return "publish_release"
	case ActionPullReviewDismissed:
		return "pull_review_dismissed"
	case ActionPullRequestReadyForReview:
		return "pull_request_ready_for_review"
	case ActionAutoMergePullRequest:
		return "auto_merge_pull_request"
	default:
		return "action-" + strconv.Itoa(int(at))
	}
}

func (at ActionType) WatchSelection() repo_model.WatchSelection {
	// WatchAllSelection generally means that there is no granular enough setting for this.
	switch at {
	case ActionCreateRepo:
		return repo_model.WatchAllSelection
	case ActionRenameRepo:
		return repo_model.WatchAllSelection
	case ActionStarRepo:
		return repo_model.WatchAllSelection
	case ActionWatchRepo:
		return repo_model.WatchAllSelection
	case ActionCommitRepo:
		return repo_model.WatchAllSelection
	case ActionCreateIssue:
		return repo_model.WatchSelection{Issues: true, PullRequests: false, Releases: false}
	case ActionCreatePullRequest:
		return repo_model.WatchSelection{Issues: false, PullRequests: true, Releases: false}
	case ActionTransferRepo:
		return repo_model.WatchAllSelection
	case ActionPushTag:
		return repo_model.WatchAllSelection
	case ActionCommentIssue:
		return repo_model.WatchSelection{Issues: true, PullRequests: false, Releases: false}
	case ActionMergePullRequest:
		return repo_model.WatchSelection{Issues: false, PullRequests: true, Releases: false}
	case ActionCloseIssue:
		return repo_model.WatchSelection{Issues: true, PullRequests: false, Releases: false}
	case ActionReopenIssue:
		return repo_model.WatchSelection{Issues: true, PullRequests: false, Releases: false}
	case ActionClosePullRequest:
		return repo_model.WatchSelection{Issues: false, PullRequests: true, Releases: false}
	case ActionReopenPullRequest:
		return repo_model.WatchSelection{Issues: false, PullRequests: true, Releases: false}
	case ActionDeleteTag:
		return repo_model.WatchAllSelection
	case ActionDeleteBranch:
		return repo_model.WatchAllSelection
	case ActionMirrorSyncPush:
		return repo_model.WatchAllSelection
	case ActionMirrorSyncCreate:
		return repo_model.WatchAllSelection
	case ActionMirrorSyncDelete:
		return repo_model.WatchAllSelection
	case ActionApprovePullRequest:
		return repo_model.WatchSelection{Issues: false, PullRequests: true, Releases: false}
	case ActionRejectPullRequest:
		return repo_model.WatchSelection{Issues: false, PullRequests: true, Releases: false}
	case ActionCommentPull:
		return repo_model.WatchSelection{Issues: false, PullRequests: true, Releases: false}
	case ActionPublishRelease:
		return repo_model.WatchSelection{Issues: false, PullRequests: false, Releases: true}
	case ActionPullReviewDismissed:
		return repo_model.WatchSelection{Issues: false, PullRequests: true, Releases: false}
	case ActionPullRequestReadyForReview:
		return repo_model.WatchSelection{Issues: false, PullRequests: true, Releases: false}
	case ActionAutoMergePullRequest:
		return repo_model.WatchSelection{Issues: false, PullRequests: true, Releases: false}
	default:
		return repo_model.WatchAllSelection
	}
}

func (at ActionType) InActions(actions ...string) bool {
	return slices.Contains(actions, at.String())
}

// Action represents user operation type and other information to
// repository. It implemented interface base.Actioner so that can be
// used in template render.
type Action struct {
	ID          int64 `xorm:"pk autoincr"`
	UserID      int64 // Receiver user id.
	OpType      ActionType
	ActUserID   int64            // Action user id.
	ActUser     *user_model.User `xorm:"-"`
	RepoID      int64
	Repo        *repo_model.Repository `xorm:"-"`
	CommentID   int64                  `xorm:"INDEX"` // indexed to support `DeleteIssueActions`
	Comment     *issues_model.Comment  `xorm:"-"`
	Issue       *issues_model.Issue    `xorm:"-"` // get the issue id from content
	RefName     string
	IsPrivate   bool               `xorm:"NOT NULL DEFAULT false"`
	Content     string             `xorm:"TEXT"`
	CreatedUnix timeutil.TimeStamp `xorm:"created INDEX"` // indexed to support `DeleteOldActions`
}

func init() {
	db.RegisterModel(new(Action))
}

// TableIndices implements xorm's TableIndices interface.  It is used here to ensure indexes with specified column order
// are created, which can't be created through xorm tags on the struct.
func (a *Action) TableIndices() []*schemas.Index {
	// Index to support getUserHeatmapData, which searches for data that is visible-to (user_id) and performed-by
	// (act_user_id) a user, but only includes visible repos (repo_id).
	actUserIndex := schemas.NewIndex("au_r_c_u", schemas.IndexType)
	actUserIndex.AddColumn("act_user_id", "repo_id", "created_unix", "user_id")

	// GetFeeds is a common access point to Action and requires that all action feeds be queried based upon one of
	// user_id (opts.RequestedUser), repo_id (opts.RequestedTeam... kinda), and/or repo_id (opts.RequestedRepo), and
	// then the results are ordered by created_unix and paginated.  The most efficient indexes to support those queries
	// are:
	requestedUser := schemas.NewIndex("user_id_created_unix", schemas.IndexType)
	requestedUser.AddColumn("user_id", "created_unix")
	requestedRepo := schemas.NewIndex("repo_id_created_unix", schemas.IndexType)
	requestedRepo.AddColumn("repo_id", "created_unix")

	// To support `DeleteIssueActions` search for createissue / createpullrequest actions; this isn't a great search
	// because `DeleteIssueActions` searches by `content` as well, but it should be sufficient performance-wise for
	// infrequent deleting of issues.
	repoOpType := schemas.NewIndex("repo_id_op_type", schemas.IndexType)
	repoOpType.AddColumn("repo_id", "op_type")

	indices := []*schemas.Index{actUserIndex, requestedUser, requestedRepo, repoOpType}

	return indices
}

// GetOpType gets the ActionType of this action.
func (a *Action) GetOpType() ActionType {
	return a.OpType
}

// LoadActUser loads a.ActUser
func (a *Action) LoadActUser(ctx context.Context) {
	if a.ActUser != nil {
		return
	}
	var err error
	a.ActUser, err = user_model.GetPossibleUserByID(ctx, a.ActUserID)
	if err == nil {
		return
	} else if user_model.IsErrUserNotExist(err) {
		a.ActUser = user_model.NewGhostUser()
	} else {
		log.Error("GetPossibleUserByID(%d): %v", a.ActUserID, err)
	}
}

func (a *Action) LoadRepo(ctx context.Context) {
	if a.Repo != nil {
		return
	}
	var err error
	a.Repo, err = repo_model.GetRepositoryByID(ctx, a.RepoID)
	if err != nil {
		log.Error("repo_model.GetRepositoryByID(%d): %v", a.RepoID, err)
	}
}

// GetActFullName gets the action's user full name.
func (a *Action) GetActFullName(ctx context.Context) string {
	a.LoadActUser(ctx)
	return a.ActUser.FullName
}

// GetActUserName gets the action's user name.
func (a *Action) GetActUserName(ctx context.Context) string {
	a.LoadActUser(ctx)
	return a.ActUser.Name
}

// ShortActUserName gets the action's user name trimmed to max 20
// chars.
func (a *Action) ShortActUserName(ctx context.Context) string {
	return base.EllipsisString(a.GetActUserName(ctx), 20)
}

// GetActDisplayName gets the action's display name based on DEFAULT_SHOW_FULL_NAME, or falls back to the username if it is blank.
func (a *Action) GetActDisplayName(ctx context.Context) string {
	if setting.UI.DefaultShowFullName {
		trimmedFullName := strings.TrimSpace(a.GetActFullName(ctx))
		if len(trimmedFullName) > 0 {
			return trimmedFullName
		}
	}
	return a.ShortActUserName(ctx)
}

// GetActDisplayNameTitle gets the action's display name used for the title (tooltip) based on DEFAULT_SHOW_FULL_NAME
func (a *Action) GetActDisplayNameTitle(ctx context.Context) string {
	if setting.UI.DefaultShowFullName {
		return a.ShortActUserName(ctx)
	}
	return a.GetActFullName(ctx)
}

// GetRepo returns the repository of the action.
func (a *Action) GetRepo(ctx context.Context) *repo_model.Repository {
	a.LoadRepo(ctx)
	return a.Repo
}

// GetRepoUserName returns the name of the action repository owner.
func (a *Action) GetRepoUserName(ctx context.Context) string {
	a.LoadRepo(ctx)
	if a.Repo == nil {
		return "(non-existing-repo)"
	}
	return a.Repo.OwnerName
}

// ShortRepoUserName returns the name of the action repository owner
// trimmed to max 20 chars.
func (a *Action) ShortRepoUserName(ctx context.Context) string {
	return base.EllipsisString(a.GetRepoUserName(ctx), 20)
}

// GetRepoName returns the name of the action repository.
func (a *Action) GetRepoName(ctx context.Context) string {
	a.LoadRepo(ctx)
	if a.Repo == nil {
		return "(non-existing-repo)"
	}
	return a.Repo.Name
}

// ShortRepoName returns the name of the action repository
// trimmed to max 33 chars.
func (a *Action) ShortRepoName(ctx context.Context) string {
	return base.EllipsisString(a.GetRepoName(ctx), 33)
}

// GetRepoPath returns the virtual path to the action repository.
func (a *Action) GetRepoPath(ctx context.Context) string {
	return path.Join(a.GetRepoUserName(ctx), a.GetRepoName(ctx))
}

// ShortRepoPath returns the virtual path to the action repository
// trimmed to max 20 + 1 + 33 chars.
func (a *Action) ShortRepoPath(ctx context.Context) string {
	return path.Join(a.ShortRepoUserName(ctx), a.ShortRepoName(ctx))
}

// GetRepoLink returns relative link to action repository.
func (a *Action) GetRepoLink(ctx context.Context) string {
	// path.Join will skip empty strings
	return path.Join(setting.AppSubURL, "/", url.PathEscape(a.GetRepoUserName(ctx)), url.PathEscape(a.GetRepoName(ctx)))
}

// GetRepoAbsoluteLink returns the absolute link to action repository.
func (a *Action) GetRepoAbsoluteLink(ctx context.Context) string {
	return setting.AppURL + url.PathEscape(a.GetRepoUserName(ctx)) + "/" + url.PathEscape(a.GetRepoName(ctx))
}

func (a *Action) LoadComment(ctx context.Context) (err error) {
	if a.CommentID == 0 || a.Comment != nil {
		return nil
	}
	a.Comment, err = issues_model.GetCommentByID(ctx, a.CommentID)
	return err
}

// GetCommentHTMLURL returns link to action comment.
func (a *Action) GetCommentHTMLURL(ctx context.Context) string {
	if a == nil {
		return "#"
	}
	_ = a.LoadComment(ctx)
	if a.Comment != nil {
		return a.Comment.HTMLURL(ctx)
	}

	if err := a.LoadIssue(ctx); err != nil || a.Issue == nil {
		return "#"
	}
	if err := a.Issue.LoadRepo(ctx); err != nil {
		return "#"
	}

	return a.Issue.HTMLURL()
}

// GetCommentLink returns link to action comment.
func (a *Action) GetCommentLink(ctx context.Context) string {
	if a == nil {
		return "#"
	}
	_ = a.LoadComment(ctx)
	if a.Comment != nil {
		return a.Comment.Link(ctx)
	}

	if err := a.LoadIssue(ctx); err != nil || a.Issue == nil {
		return "#"
	}
	if err := a.Issue.LoadRepo(ctx); err != nil {
		return "#"
	}

	return a.Issue.Link()
}

// GetBranch returns the action's repository branch.
func (a *Action) GetBranch() string {
	return strings.TrimPrefix(a.RefName, git.BranchPrefix)
}

// GetRefLink returns the action's ref link.
func (a *Action) GetRefLink(ctx context.Context) string {
	return git.RefURL(a.GetRepoLink(ctx), a.RefName)
}

// GetTag returns the action's repository tag.
func (a *Action) GetTag() string {
	return strings.TrimPrefix(a.RefName, git.TagPrefix)
}

// GetContent returns the action's content.
func (a *Action) GetContent() string {
	return a.Content
}

// GetCreate returns the action creation time.
func (a *Action) GetCreate() time.Time {
	return a.CreatedUnix.AsTime()
}

func (a *Action) IsIssueEvent() bool {
	return a.OpType.InActions("comment_issue", "approve_pull_request", "reject_pull_request", "comment_pull", "merge_pull_request")
}

// GetIssueInfos returns a list of associated information with the action.
func (a *Action) GetIssueInfos() []string {
	// Previously multiple pieces of data used to be encoded into a.Content by pipe-separating them, but this doesn't
	// work well if some of the user-entered pieces of content (issue titles, comments, etc.) contain pipes.  The newer
	// storage format is to json-encode a string array, which we check for and prefer... then fallback to assuming old.
	var ret []string
	if strings.HasPrefix(a.Content, "[") && strings.HasSuffix(a.Content, "]") {
		ret = make([]string, 0, 3)
		err := json.Unmarshal([]byte(a.Content), &ret)
		if err != nil {
			log.Error("GetIssueInfos json decoding error: %v", err)
		}
	} else {
		ret = strings.SplitN(a.Content, "|", 3)
	}

	// make sure it always returns 3 elements, because there are some access to the a[1] and a[2] without checking the length
	for len(ret) < 3 {
		ret = append(ret, "")
	}
	return ret
}

func (a *Action) getIssueIndex() int64 {
	infos := a.GetIssueInfos()
	if len(infos) == 0 {
		return 0
	}
	index, _ := strconv.ParseInt(infos[0], 10, 64)
	return index
}

func (a *Action) LoadIssue(ctx context.Context) error {
	if a.Issue != nil {
		return nil
	}
	if index := a.getIssueIndex(); index > 0 {
		issue, err := issues_model.GetIssueByIndex(ctx, a.RepoID, index)
		if err != nil {
			return err
		}
		a.Issue = issue
		a.Issue.Repo = a.Repo
	}
	return nil
}

// GetIssueTitle returns the title of first issue associated with the action.
func (a *Action) GetIssueTitle(ctx context.Context) string {
	if err := a.LoadIssue(ctx); err != nil {
		log.Error("LoadIssue: %v", err)
		return "<500 when get issue>"
	}
	if a.Issue == nil {
		return "<Issue not found>"
	}
	return a.Issue.Title
}

// GetIssueContent returns the content of first issue associated with this action.
func (a *Action) GetIssueContent(ctx context.Context) string {
	if err := a.LoadIssue(ctx); err != nil {
		log.Error("LoadIssue: %v", err)
		return "<500 when get issue>"
	}
	if a.Issue == nil {
		return "<Content not found>"
	}
	return a.Issue.Content
}

func GetActivityByID(ctx context.Context, id int64) (*Action, error) {
	var act Action
	_, err := db.GetEngine(ctx).ID(id).Get(&act)
	return &act, err
}

// GetFeedsOptions options for retrieving feeds
type GetFeedsOptions struct {
	db.ListOptions
	RequestedUser        *user_model.User       // the user we want activity for
	RequestedTeam        *organization.Team     // the team we want activity for
	RequestedRepo        *repo_model.Repository // the repo we want activity for
	RequestedIssue       *issues_model.Issue    // the issue we want activity for
	Actor                *user_model.User       // the user viewing the activity
	IncludePrivate       bool                   // include private actions
	OnlyPerformedBy      bool                   // only actions performed by requested user
	OnlyPerformedByActor bool                   // only actions performed by the original actor
	Date                 string                 // the day we want activity for: YYYY-MM-DD

	// Filters repositories based upon optional authorization restrictions.
	AuthorizationReducer     repo_model.RepositoryAuthorizationReducer
	repositoryTableAvailable bool // when using the authorization reducer, is `repository` already present in query?
}

// GetFeeds returns actions according to the provided options
func GetFeeds(ctx context.Context, opts GetFeedsOptions) (ActionList, int64, error) {
	if opts.RequestedUser == nil && opts.RequestedTeam == nil && opts.RequestedRepo == nil && opts.RequestedIssue == nil {
		return nil, 0, errors.New("need at least one of these filters: RequestedUser, RequestedTeam, RequestedRepo, RequestedIssue")
	}

	opts.repositoryTableAvailable = true // join for `repository` occurs here, so no need for a subquery
	cond, err := activityQueryCondition(ctx, opts)
	if err != nil {
		return nil, 0, err
	}

	sess := db.GetEngine(ctx).Where(cond).
		Select("`action`.*"). // this line will avoid select other joined table's columns
		Join("INNER", "repository", "`repository`.id = `action`.repo_id")

	opts.SetDefaultValues()
	sess = db.SetSessionPagination(sess, &opts)

	actions := make([]*Action, 0, opts.PageSize)
	count, err := sess.Desc("`action`.created_unix").FindAndCount(&actions)
	if err != nil {
		return nil, 0, fmt.Errorf("FindAndCount: %w", err)
	}

	if err := ActionList(actions).LoadAttributes(ctx); err != nil {
		return nil, 0, fmt.Errorf("LoadAttributes: %w", err)
	}

	return actions, count, nil
}

// ActivityReadable return whether doer can read activities of user
func ActivityReadable(user, doer *user_model.User) bool {
	return !user.KeepActivityPrivate ||
		doer != nil && (doer.IsAdmin || user.ID == doer.ID)
}

func activityQueryCondition(ctx context.Context, opts GetFeedsOptions) (builder.Cond, error) {
	cond := builder.NewCond()

	if opts.AuthorizationReducer != nil {
		if opts.repositoryTableAvailable {
			cond = cond.And(opts.AuthorizationReducer.RepoReadAccessFilter())
		} else {
			cond = cond.And(
				builder.In(
					"`action`.repo_id",
					builder.Select("id").From("repository").Where(opts.AuthorizationReducer.RepoReadAccessFilter()),
				),
			)
		}
	}

	if opts.OnlyPerformedByActor {
		cond = cond.And(builder.Expr("`action`.user_id = `action`.act_user_id"))
	}

	if opts.RequestedTeam != nil && opts.RequestedUser == nil {
		org, err := user_model.GetUserByID(ctx, opts.RequestedTeam.OrgID)
		if err != nil {
			return nil, err
		}
		opts.RequestedUser = org
	}

	// check activity visibility for actor ( similar to activityReadable() )
	if opts.Actor == nil {
		cond = cond.And(builder.In("act_user_id",
			builder.Select("`user`.id").Where(
				builder.Eq{"keep_activity_private": false, "visibility": structs.VisibleTypePublic},
			).From("`user`"),
		))
	} else if !opts.Actor.IsAdmin {
		uidCond := builder.Select("`user`.id").From("`user`").Where(
			builder.Eq{"keep_activity_private": false}.
				And(builder.In("visibility", structs.VisibleTypePublic, structs.VisibleTypeLimited)),
		).
			Or(builder.Eq{"id": opts.Actor.ID})

		if opts.RequestedUser != nil {
			if opts.RequestedUser.IsOrganization() {
				// An organization can always see the activities whose `act_user_id` is the same as its id.
				uidCond = uidCond.Or(builder.Eq{"id": opts.RequestedUser.ID})
			} else {
				// A user can always see the activities of the organizations to which the user belongs.
				uidCond = uidCond.Or(
					builder.Eq{"type": user_model.UserTypeOrganization}.
						And(builder.In("`user`.id", builder.Select("org_id").
							Where(builder.Eq{"uid": opts.RequestedUser.ID}).
							From("team_user"))),
				)
			}
		}

		cond = cond.And(builder.In("act_user_id", uidCond))
	}

	// check readable repositories by doer/actor
	if opts.Actor == nil || !opts.Actor.IsAdmin {
		cond = cond.And(builder.In("repo_id", repo_model.AccessibleRepoIDsQuery(opts.Actor)))
	}

	if opts.RequestedRepo != nil {
		cond = cond.And(builder.Eq{"repo_id": opts.RequestedRepo.ID})
	}

	if opts.RequestedTeam != nil {
		env := organization.OrgFromUser(opts.RequestedUser).AccessibleTeamReposEnv(ctx, opts.RequestedTeam)
		teamRepoIDs, err := env.RepoIDs(1, opts.RequestedUser.NumRepos)
		if err != nil {
			return nil, fmt.Errorf("GetTeamRepositories: %w", err)
		}
		cond = cond.And(builder.In("repo_id", teamRepoIDs))
	}

	if opts.RequestedUser != nil {
		cond = cond.And(builder.Eq{"user_id": opts.RequestedUser.ID})

		if opts.OnlyPerformedBy {
			cond = cond.And(builder.Eq{"act_user_id": opts.RequestedUser.ID})
		}
	}

	if opts.RequestedIssue != nil {
		if opts.RequestedRepo != nil && opts.RequestedRepo.ID != opts.RequestedIssue.RepoID {
			return nil, errors.New("requested repository id does not match requested issue repository id")
		}

		cond = cond.And(
			builder.Eq{"repo_id": opts.RequestedIssue.RepoID},
			builder.Or(
				builder.Like{"content", "[\"" + strconv.FormatInt(opts.RequestedIssue.Index, 10) + "\"%"}, // JSON, ["IssueIndex"...
				builder.Like{"content", strconv.FormatInt(opts.RequestedIssue.Index, 10) + "|%"},          // "IssueIndex|content..."
			),
		)
	}

	if !opts.IncludePrivate {
		cond = cond.And(builder.Eq{"`action`.is_private": false})
	}

	if opts.Date != "" {
		dateLow, err := time.ParseInLocation("2006-01-02", opts.Date, setting.DefaultUILocation)
		if err != nil {
			log.Warn("Unable to parse %s, filter not applied: %v", opts.Date, err)
		} else {
			dateHigh := dateLow.Add(86399000000000) // 23h59m59s

			cond = cond.And(builder.Gte{"`action`.created_unix": dateLow.Unix()})
			cond = cond.And(builder.Lte{"`action`.created_unix": dateHigh.Unix()})
		}
	}

	return cond, nil
}

// DeleteOldActions deletes all old actions from database.
func DeleteOldActions(ctx context.Context, olderThan time.Duration) (err error) {
	if olderThan <= 0 {
		return nil
	}

	_, err = db.GetEngine(ctx).Where("created_unix < ?", time.Now().Add(-olderThan).Unix()).Delete(&Action{})
	return err
}

// NotifyWatchers creates batch of actions for every watcher.
func NotifyWatchers(ctx context.Context, actions ...*Action) ([]Action, error) {
	var watchers []*repo_model.Watch
	var repo *repo_model.Repository
	var err error
	var permCode []bool
	var permIssue []bool
	var permPR []bool
	var out []Action

	e := db.GetEngine(ctx)

	for _, act := range actions {
		repoChanged := repo == nil || repo.ID != act.RepoID

		if repoChanged {
			// Add feeds for user self and all watchers.
			watchers, err = repo_model.GetSelectWatchers(ctx, act.RepoID, act.GetOpType().WatchSelection())
			if err != nil {
				return nil, fmt.Errorf("get watchers: %w", err)
			}

			// Be aware that optimizing this correctly into the `GetWatchers` SQL
			// query is for most cases less performant than doing this.
			blockedDoerUserIDs, err := user_model.ListBlockedByUsersID(ctx, act.ActUserID)
			if err != nil {
				return nil, fmt.Errorf("user_model.ListBlockedByUsersID: %w", err)
			}

			if len(blockedDoerUserIDs) > 0 {
				excludeWatcherIDs := make(container.Set[int64], len(blockedDoerUserIDs))
				excludeWatcherIDs.AddMultiple(blockedDoerUserIDs...)
				watchers = slices.DeleteFunc(watchers, func(v *repo_model.Watch) bool {
					return excludeWatcherIDs.Contains(v.UserID)
				})
			}
		}

		// Add feed for actioner.
		act.UserID = act.ActUserID
		if _, err = e.Insert(act); err != nil {
			return nil, fmt.Errorf("insert new actioner: %w", err)
		}
		out = append(out, *act)

		if repoChanged {
			act.LoadRepo(ctx)
			if act.Repo == nil {
				return nil, repo_model.ErrRepoNotExist{}
			}

			repo = act.Repo

			// check repo owner exist.
			if err := act.Repo.LoadOwner(ctx); err != nil {
				return nil, fmt.Errorf("can't get repo owner: %w", err)
			}
		} else if act.Repo == nil {
			act.Repo = repo
		}

		// Add feed for organization
		if act.Repo.Owner.IsOrganization() && act.ActUserID != act.Repo.Owner.ID {
			act.ID = 0
			act.UserID = act.Repo.Owner.ID
			if err = db.Insert(ctx, act); err != nil {
				return nil, fmt.Errorf("insert new actioner: %w", err)
			}
		}

		if repoChanged {
			permCode = make([]bool, len(watchers))
			permIssue = make([]bool, len(watchers))
			permPR = make([]bool, len(watchers))
			for i, watcher := range watchers {
				user, err := user_model.GetUserByID(ctx, watcher.UserID)
				if err != nil {
					permCode[i] = false
					permIssue[i] = false
					permPR[i] = false
					continue
				}
				perm, err := access_model.GetUserRepoPermission(ctx, repo, user)
				if err != nil {
					permCode[i] = false
					permIssue[i] = false
					permPR[i] = false
					continue
				}
				permCode[i] = perm.CanRead(unit.TypeCode)
				permIssue[i] = perm.CanRead(unit.TypeIssues)
				permPR[i] = perm.CanRead(unit.TypePullRequests)
			}
		}

		for i, watcher := range watchers {
			if act.ActUserID == watcher.UserID {
				continue
			}
			act.ID = 0
			act.UserID = watcher.UserID
			act.Repo.Units = nil

			switch act.OpType {
			case ActionCommitRepo, ActionPushTag, ActionDeleteTag, ActionPublishRelease, ActionDeleteBranch:
				if !permCode[i] {
					continue
				}
			case ActionCreateIssue, ActionCommentIssue, ActionCloseIssue, ActionReopenIssue:
				if !permIssue[i] {
					continue
				}
			case ActionCreatePullRequest, ActionCommentPull, ActionMergePullRequest, ActionClosePullRequest, ActionReopenPullRequest, ActionAutoMergePullRequest:
				if !permPR[i] {
					continue
				}
			}

			if err = db.Insert(ctx, act); err != nil {
				return nil, fmt.Errorf("insert new action: %w", err)
			}
		}
	}
	return out, nil
}

// NotifyWatchersActions creates batch of actions for every watcher.
func NotifyWatchersActions(ctx context.Context, acts []*Action) ([]Action, error) {
	ctx, committer, err := db.TxContext(ctx)
	if err != nil {
		return nil, err
	}
	defer committer.Close()
	var out []Action
	for _, act := range acts {
		as, err := NotifyWatchers(ctx, act)
		if err != nil {
			return nil, err
		}
		out = append(out, as...)
	}
	return out, committer.Commit()
}

// DeleteIssueActions delete all actions related with issueID
func DeleteIssueActions(ctx context.Context, repoID, issueID, issueIndex int64) error {
	// delete actions assigned to this issue
	e := db.GetEngine(ctx)

	// MariaDB has a performance bug: https://jira.mariadb.org/browse/MDEV-16289
	// so here it uses "DELETE ... WHERE IN" with pre-queried IDs.
	var lastCommentID int64
	commentIDs := make([]int64, 0, db.DefaultMaxInSize)
	for {
		commentIDs = commentIDs[:0]
		err := e.Select("`id`").Table(&issues_model.Comment{}).
			Where(builder.Eq{"issue_id": issueID}).And("`id` > ?", lastCommentID).
			OrderBy("`id`").Limit(db.DefaultMaxInSize).
			Find(&commentIDs)
		if err != nil {
			return err
		} else if len(commentIDs) == 0 {
			break
		} else if _, err = db.GetEngine(ctx).In("comment_id", commentIDs).Delete(&Action{}); err != nil {
			return err
		}
		lastCommentID = commentIDs[len(commentIDs)-1]
	}

	_, err := e.Where("repo_id = ?", repoID).
		In("op_type", ActionCreateIssue, ActionCreatePullRequest).
		Where(builder.Or(
			builder.Like{"content", strconv.FormatInt(issueIndex, 10) + "|%"}, // "IssueIndex|content..."
			builder.Like{"content", "[\"" + strconv.FormatInt(issueIndex, 10) + "\"%"},
		)). // JSON, ["IssueIndex"...
		Delete(&Action{})
	return err
}

// CountActionCreatedUnixString count actions where created_unix is an empty string
func CountActionCreatedUnixString(ctx context.Context) (int64, error) {
	if setting.Database.Type.IsSQLite3() {
		return db.GetEngine(ctx).Where(`created_unix = ""`).Count(new(Action))
	}
	return 0, nil
}

// FixActionCreatedUnixString set created_unix to zero if it is an empty string
func FixActionCreatedUnixString(ctx context.Context) (int64, error) {
	if setting.Database.Type.IsSQLite3() {
		res, err := db.GetEngine(ctx).Exec(`UPDATE action SET created_unix = 0 WHERE created_unix = ""`)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	}
	return 0, nil
}

func (a *Action) IsActionPrivate(ctx context.Context) (bool, error) {
	if a.IsPrivate {
		return true, nil
	}

	a.LoadRepo(ctx)
	if a.Repo == nil {
		return true, repo_model.ErrRepoNotExist{}
	}

	repo := a.Repo
	err := repo.LoadOwner(ctx)
	if err != nil {
		return true, err
	}

	if repo.IsPrivate || repo.Owner.KeepActivityPrivate || repo.Owner.Visibility != structs.VisibleTypePublic {
		return true, nil
	}

	a.LoadActUser(ctx)
	if a.ActUser == nil {
		return true, user_model.ErrUserNotExist{}
	}

	user := a.ActUser
	if user.KeepActivityPrivate || user.Visibility != structs.VisibleTypePublic {
		return true, nil
	}

	return false, nil
}
