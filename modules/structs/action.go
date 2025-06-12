// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package structs

import (
	"time"
)

// ActionRunJob represents a job of a run
// swagger:model
type ActionRunJob struct {
	// the action run job id
	ID int64 `json:"id"`
	// the repository id
	RepoID int64 `json:"repo_id"`
	// the owner id
	OwnerID int64 `json:"owner_id"`
	// the action run job name
	Name string `json:"name"`
	// the action run job needed ids
	Needs []string `json:"needs"`
	// the action run job labels to run on
	RunsOn []string `json:"runs_on"`
	// the action run job latest task id
	TaskID int64 `json:"task_id"`
	// the action run job status
	Status string `json:"status"`
}

// ActionRun represents an action run
// swagger:model
type ActionRun struct {
	// the action run id
	ID int64 `json:"id"`
	// the action run's title
	Title string `json:"title"`
	// the repo this action is part of
	Repo *Repository `json:"repository"`
	// the name of workflow file
	WorkflowID string `json:"workflow_id"`
	// a unique number for each run of a repository
	Index int64 `json:"index_in_repo"`
	// the user that triggered this action run
	TriggerUser *User `json:"trigger_user"`
	// the cron id for the schedule trigger
	ScheduleID int64
	// the commit/tag/… the action run ran on
	PrettyRef string `json:"prettyref"`
	// has the commit/tag/… the action run ran on been deleted
	IsRefDeleted bool `json:"is_ref_deleted"`
	// the commit sha the action run ran on
	CommitSHA string `json:"commit_sha"`
	// If this is triggered by a PR from a forked repository or an untrusted user, we need to check if it is approved and limit permissions when running the workflow.
	IsForkPullRequest bool `json:"is_fork_pull_request"`
	// may need approval if it's a fork pull request
	NeedApproval bool `json:"need_approval"`
	// who approved this action run
	ApprovedBy int64 `json:"approved_by"`
	// the webhook event that causes the workflow to run
	Event string `json:"event"`
	// the payload of the webhook event that causes the workflow to run
	EventPayload string `json:"event_payload"`
	// the trigger event defined in the `on` configuration of the triggered workflow
	TriggerEvent string `json:"trigger_event"`
	// the current status of this run
	Status string `json:"status"`
	// when the action run was started
	Started time.Time `json:"started,omitempty"`
	// when the action run was stopped
	Stopped time.Time `json:"stopped,omitempty"`
	// when the action run was created
	Created time.Time `json:"created,omitempty"`
	// when the action run was last updated
	Updated time.Time `json:"updated,omitempty"`
	// how long the action run ran for
	Duration time.Duration `json:"duration,omitempty"`
	// the url of this action run
	HTMLURL string `json:"html_url"`
}
