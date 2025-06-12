// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package structs

import (
	"errors"
	"strings"
	"time"

	"forgejo.org/modules/json"
)

// ErrInvalidReceiveHook FIXME
var ErrInvalidReceiveHook = errors.New("Invalid JSON payload received over webhook")

// Hook a hook is a web hook when one repository changed
type Hook struct {
	ID           int64  `json:"id"`
	Type         string `json:"type"`
	BranchFilter string `json:"branch_filter"`
	URL          string `json:"url"`

	// Deprecated: use Metadata instead
	Config              map[string]string `json:"config"`
	Events              []string          `json:"events"`
	AuthorizationHeader string            `json:"authorization_header"`
	ContentType         string            `json:"content_type"`
	Metadata            any               `json:"metadata"`
	Active              bool              `json:"active"`
	// swagger:strfmt date-time
	Updated time.Time `json:"updated_at"`
	// swagger:strfmt date-time
	Created time.Time `json:"created_at"`
}

// HookList represents a list of API hook.
type HookList []*Hook

// CreateHookOptionConfig has all config options in it
// required are "content_type" and "url" Required
type CreateHookOptionConfig map[string]string

// CreateHookOption options when create a hook
type CreateHookOption struct {
	// required: true
	// enum: ["forgejo", "dingtalk", "discord", "gitea", "gogs", "msteams", "slack", "telegram", "feishu", "wechatwork", "packagist"]
	Type string `json:"type" binding:"Required"`
	// required: true
	Config              CreateHookOptionConfig `json:"config" binding:"Required"`
	Events              []string               `json:"events"`
	BranchFilter        string                 `json:"branch_filter" binding:"GlobPattern"`
	AuthorizationHeader string                 `json:"authorization_header"`
	// default: false
	Active          bool `json:"active"`
	IsSystemWebhook bool `json:"is_system_webhook"`
}

// EditHookOption options when modify one hook
type EditHookOption struct {
	Config              map[string]string `json:"config"`
	Events              []string          `json:"events"`
	BranchFilter        string            `json:"branch_filter" binding:"GlobPattern"`
	AuthorizationHeader string            `json:"authorization_header"`
	Active              *bool             `json:"active"`
}

// Payloader payload is some part of one hook
type Payloader interface {
	JSONPayload() ([]byte, error)
}

// PayloadUser represents the author or committer of a commit
type PayloadUser struct {
	// Full name of the commit author
	Name string `json:"name"`
	// swagger:strfmt email
	Email    string `json:"email"`
	UserName string `json:"username"`
}

// FIXME: consider using same format as API when commits API are added.
//        applies to PayloadCommit and PayloadCommitVerification

// PayloadCommit represents a commit
type PayloadCommit struct {
	// sha1 hash of the commit
	ID           string                     `json:"id"`
	Message      string                     `json:"message"`
	URL          string                     `json:"url"`
	Author       *PayloadUser               `json:"author"`
	Committer    *PayloadUser               `json:"committer"`
	Verification *PayloadCommitVerification `json:"verification"`
	// swagger:strfmt date-time
	Timestamp time.Time `json:"timestamp"`
	Added     []string  `json:"added"`
	Removed   []string  `json:"removed"`
	Modified  []string  `json:"modified"`
}

// PayloadCommitVerification represents the GPG verification of a commit
type PayloadCommitVerification struct {
	Verified  bool         `json:"verified"`
	Reason    string       `json:"reason"`
	Signature string       `json:"signature"`
	Signer    *PayloadUser `json:"signer"`
	Payload   string       `json:"payload"`
}

var (
	_ Payloader = &CreatePayload{}
	_ Payloader = &DeletePayload{}
	_ Payloader = &ForkPayload{}
	_ Payloader = &PushPayload{}
	_ Payloader = &IssuePayload{}
	_ Payloader = &IssueCommentPayload{}
	_ Payloader = &PullRequestPayload{}
	_ Payloader = &RepositoryPayload{}
	_ Payloader = &ReleasePayload{}
	_ Payloader = &PackagePayload{}
	_ Payloader = &ActionPayload{}
)

// _________                        __
// \_   ___ \_______   ____ _____ _/  |_  ____
// /    \  \/\_  __ \_/ __ \\__  \\   __\/ __ \
// \     \____|  | \/\  ___/ / __ \|  | \  ___/
//  \______  /|__|    \___  >____  /__|  \___  >
//         \/             \/     \/          \/

// CreatePayload FIXME
type CreatePayload struct {
	Sha     string      `json:"sha"`
	Ref     string      `json:"ref"`
	RefType string      `json:"ref_type"`
	Repo    *Repository `json:"repository"`
	Sender  *User       `json:"sender"`
}

// JSONPayload return payload information
func (p *CreatePayload) JSONPayload() ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}

// ________         .__          __
// \______ \   ____ |  |   _____/  |_  ____
//  |    |  \_/ __ \|  | _/ __ \   __\/ __ \
//  |    `   \  ___/|  |_\  ___/|  | \  ___/
// /_______  /\___  >____/\___  >__|  \___  >
//         \/     \/          \/          \/

// PusherType define the type to push
type PusherType string

// describe all the PusherTypes
const (
	PusherTypeUser PusherType = "user"
)

// DeletePayload represents delete payload
type DeletePayload struct {
	Ref        string      `json:"ref"`
	RefType    string      `json:"ref_type"`
	PusherType PusherType  `json:"pusher_type"`
	Repo       *Repository `json:"repository"`
	Sender     *User       `json:"sender"`
}

// JSONPayload implements Payload
func (p *DeletePayload) JSONPayload() ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}

// ___________           __
// \_   _____/__________|  | __
//  |    __)/  _ \_  __ \  |/ /
//  |     \(  <_> )  | \/    <
//  \___  / \____/|__|  |__|_ \
//      \/                   \/

// ForkPayload represents fork payload
type ForkPayload struct {
	Forkee *Repository `json:"forkee"`
	Repo   *Repository `json:"repository"`
	Sender *User       `json:"sender"`
}

// JSONPayload implements Payload
func (p *ForkPayload) JSONPayload() ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}

// HookIssueCommentAction defines hook issue comment action
type HookIssueCommentAction string

// all issue comment actions
const (
	HookIssueCommentCreated HookIssueCommentAction = "created"
	HookIssueCommentEdited  HookIssueCommentAction = "edited"
	HookIssueCommentDeleted HookIssueCommentAction = "deleted"
)

// IssueCommentPayload represents a payload information of issue comment event.
type IssueCommentPayload struct {
	Action      HookIssueCommentAction `json:"action"`
	Issue       *Issue                 `json:"issue"`
	PullRequest *PullRequest           `json:"pull_request,omitempty"`
	Comment     *Comment               `json:"comment"`
	Changes     *ChangesPayload        `json:"changes,omitempty"`
	Repository  *Repository            `json:"repository"`
	Sender      *User                  `json:"sender"`
	IsPull      bool                   `json:"is_pull"`
}

// JSONPayload implements Payload
func (p *IssueCommentPayload) JSONPayload() ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}

// __________       .__
// \______   \ ____ |  |   ____ _____    ______ ____
//  |       _// __ \|  | _/ __ \\__  \  /  ___// __ \
//  |    |   \  ___/|  |_\  ___/ / __ \_\___ \\  ___/
//  |____|_  /\___  >____/\___  >____  /____  >\___  >
//         \/     \/          \/     \/     \/     \/

// HookReleaseAction defines hook release action type
type HookReleaseAction string

// all release actions
const (
	HookReleasePublished HookReleaseAction = "published"
	HookReleaseUpdated   HookReleaseAction = "updated"
	HookReleaseDeleted   HookReleaseAction = "deleted"
)

// ReleasePayload represents a payload information of release event.
type ReleasePayload struct {
	Action     HookReleaseAction `json:"action"`
	Release    *Release          `json:"release"`
	Repository *Repository       `json:"repository"`
	Sender     *User             `json:"sender"`
}

// JSONPayload implements Payload
func (p *ReleasePayload) JSONPayload() ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}

// __________             .__
// \______   \__ __  _____|  |__
//  |     ___/  |  \/  ___/  |  \
//  |    |   |  |  /\___ \|   Y  \
//  |____|   |____//____  >___|  /
//                      \/     \/

// PushPayload represents a payload information of push event.
type PushPayload struct {
	Ref          string           `json:"ref"`
	Before       string           `json:"before"`
	After        string           `json:"after"`
	CompareURL   string           `json:"compare_url"`
	Commits      []*PayloadCommit `json:"commits"`
	TotalCommits int              `json:"total_commits"`
	HeadCommit   *PayloadCommit   `json:"head_commit"`
	Repo         *Repository      `json:"repository"`
	Pusher       *User            `json:"pusher"`
	Sender       *User            `json:"sender"`
}

// JSONPayload FIXME
func (p *PushPayload) JSONPayload() ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}

// Branch returns branch name from a payload
func (p *PushPayload) Branch() string {
	return strings.ReplaceAll(p.Ref, "refs/heads/", "")
}

// .___
// |   | ______ ________ __   ____
// |   |/  ___//  ___/  |  \_/ __ \
// |   |\___ \ \___ \|  |  /\  ___/
// |___/____  >____  >____/  \___  >
//          \/     \/            \/

// HookIssueAction FIXME
type HookIssueAction string

const (
	// HookIssueOpened opened
	HookIssueOpened HookIssueAction = "opened"
	// HookIssueClosed closed
	HookIssueClosed HookIssueAction = "closed"
	// HookIssueReOpened reopened
	HookIssueReOpened HookIssueAction = "reopened"
	// HookIssueEdited edited
	HookIssueEdited HookIssueAction = "edited"
	// HookIssueAssigned assigned
	HookIssueAssigned HookIssueAction = "assigned"
	// HookIssueUnassigned unassigned
	HookIssueUnassigned HookIssueAction = "unassigned"
	// HookIssueLabelUpdated label_updated
	HookIssueLabelUpdated HookIssueAction = "label_updated"
	// HookIssueLabelCleared label_cleared
	HookIssueLabelCleared HookIssueAction = "label_cleared"
	// HookIssueSynchronized synchronized
	HookIssueSynchronized HookIssueAction = "synchronized"
	// HookIssueMilestoned is an issue action for when a milestone is set on an issue.
	HookIssueMilestoned HookIssueAction = "milestoned"
	// HookIssueDemilestoned is an issue action for when a milestone is cleared on an issue.
	HookIssueDemilestoned HookIssueAction = "demilestoned"
	// HookIssueReviewed is an issue action for when a pull request is reviewed
	HookIssueReviewed HookIssueAction = "reviewed"
	// HookIssueReviewRequested is an issue action for when a reviewer is requested for a pull request.
	HookIssueReviewRequested HookIssueAction = "review_requested"
	// HookIssueReviewRequestRemoved is an issue action for removing a review request to someone on a pull request.
	HookIssueReviewRequestRemoved HookIssueAction = "review_request_removed"
)

// IssuePayload represents the payload information that is sent along with an issue event.
type IssuePayload struct {
	Action     HookIssueAction `json:"action"`
	Index      int64           `json:"number"`
	Changes    *ChangesPayload `json:"changes,omitempty"`
	Issue      *Issue          `json:"issue"`
	Repository *Repository     `json:"repository"`
	Sender     *User           `json:"sender"`
	CommitID   string          `json:"commit_id"`
	Label      *Label          `json:"label,omitempty"`
}

// JSONPayload encodes the IssuePayload to JSON, with an indentation of two spaces.
func (p *IssuePayload) JSONPayload() ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}

// ChangesFromPayload FIXME
type ChangesFromPayload struct {
	From string `json:"from"`
}

// ChangesPayload represents the payload information of issue change
type ChangesPayload struct {
	Title *ChangesFromPayload `json:"title,omitempty"`
	Body  *ChangesFromPayload `json:"body,omitempty"`
	Ref   *ChangesFromPayload `json:"ref,omitempty"`
}

// __________      .__  .__    __________                                     __
// \______   \__ __|  | |  |   \______   \ ____  ________ __   ____   _______/  |_
//  |     ___/  |  \  | |  |    |       _// __ \/ ____/  |  \_/ __ \ /  ___/\   __\
//  |    |   |  |  /  |_|  |__  |    |   \  ___< <_|  |  |  /\  ___/ \___ \  |  |
//  |____|   |____/|____/____/  |____|_  /\___  >__   |____/  \___  >____  > |__|
//                                     \/     \/   |__|           \/     \/

// PullRequestPayload represents a payload information of pull request event.
type PullRequestPayload struct {
	Action            HookIssueAction `json:"action"`
	Index             int64           `json:"number"`
	Changes           *ChangesPayload `json:"changes,omitempty"`
	PullRequest       *PullRequest    `json:"pull_request"`
	RequestedReviewer *User           `json:"requested_reviewer"`
	Repository        *Repository     `json:"repository"`
	Sender            *User           `json:"sender"`
	CommitID          string          `json:"commit_id"`
	Review            *ReviewPayload  `json:"review"`
	Label             *Label          `json:"label,omitempty"`
}

// JSONPayload FIXME
func (p *PullRequestPayload) JSONPayload() ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}

type HookScheduleAction string

const (
	HookScheduleCreated HookScheduleAction = "schedule"
)

type SchedulePayload struct {
	Action HookScheduleAction `json:"action"`
}

type WorkflowDispatchPayload struct {
	Inputs     map[string]string `json:"inputs"`
	Ref        string            `json:"ref"`
	Repository *Repository       `json:"repository"`
	Sender     *User             `json:"sender"`
	Workflow   string            `json:"workflow"`
}

// ReviewPayload FIXME
type ReviewPayload struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

//  __      __.__ __   .__
// /  \    /  \__|  | _|__|
// \   \/\/   /  |  |/ /  |
//  \        /|  |    <|  |
//   \__/\  / |__|__|_ \__|
//        \/          \/

// HookWikiAction an action that happens to a wiki page
type HookWikiAction string

const (
	// HookWikiCreated created
	HookWikiCreated HookWikiAction = "created"
	// HookWikiEdited edited
	HookWikiEdited HookWikiAction = "edited"
	// HookWikiDeleted deleted
	HookWikiDeleted HookWikiAction = "deleted"
)

// WikiPayload payload for repository webhooks
type WikiPayload struct {
	Action     HookWikiAction `json:"action"`
	Repository *Repository    `json:"repository"`
	Sender     *User          `json:"sender"`
	Page       string         `json:"page"`
	Comment    string         `json:"comment"`
}

// JSONPayload JSON representation of the payload
func (p *WikiPayload) JSONPayload() ([]byte, error) {
	return json.MarshalIndent(p, "", " ")
}

//__________                           .__  __
//\______   \ ____ ______   ____  _____|__|/  |_  ___________ ___.__.
// |       _// __ \\____ \ /  _ \/  ___/  \   __\/  _ \_  __ <   |  |
// |    |   \  ___/|  |_> >  <_> )___ \|  ||  | (  <_> )  | \/\___  |
// |____|_  /\___  >   __/ \____/____  >__||__|  \____/|__|   / ____|
//        \/     \/|__|              \/                       \/

// HookRepoAction an action that happens to a repo
type HookRepoAction string

const (
	// HookRepoCreated created
	HookRepoCreated HookRepoAction = "created"
	// HookRepoDeleted deleted
	HookRepoDeleted HookRepoAction = "deleted"
)

// RepositoryPayload payload for repository webhooks
type RepositoryPayload struct {
	Action       HookRepoAction `json:"action"`
	Repository   *Repository    `json:"repository"`
	Organization *User          `json:"organization"`
	Sender       *User          `json:"sender"`
}

// JSONPayload JSON representation of the payload
func (p *RepositoryPayload) JSONPayload() ([]byte, error) {
	return json.MarshalIndent(p, "", " ")
}

// HookPackageAction an action that happens to a package
type HookPackageAction string

const (
	// HookPackageCreated created
	HookPackageCreated HookPackageAction = "created"
	// HookPackageDeleted deleted
	HookPackageDeleted HookPackageAction = "deleted"
)

// PackagePayload represents a package payload
type PackagePayload struct {
	Action       HookPackageAction `json:"action"`
	Repository   *Repository       `json:"repository"`
	Package      *Package          `json:"package"`
	Organization *User             `json:"organization"`
	Sender       *User             `json:"sender"`
}

// JSONPayload implements Payload
func (p *PackagePayload) JSONPayload() ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}

//     _        _   _
//    / \   ___| |_(_) ___  _ __
//   / _ \ / __| __| |/ _ \| '_ \
//  / ___ \ (__| |_| | (_) | | | |
// /_/   \_\___|\__|_|\___/|_| |_|

// this name is ridiculous, yes
// it's the sub-type of hook that has something to do with Forgejo Actions
type HookActionAction string

const (
	HookActionFailure HookActionAction = "failure"
	HookActionRecover HookActionAction = "recover"
	HookActionSuccess HookActionAction = "success"
)

// ActionPayload payload for action webhooks
type ActionPayload struct {
	Action HookActionAction `json:"action"`
	Run    *ActionRun       `json:"run"`
	// the status of this run before it completed
	// this must be a not done status
	PriorStatus string `json:"prior_status"`
	// the last run for the same workflow
	// could be nil when Run is the first for it's workflow
	LastRun *ActionRun `json:"last_run,omitempty"`
}

// JSONPayload return payload information
func (p *ActionPayload) JSONPayload() ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}
