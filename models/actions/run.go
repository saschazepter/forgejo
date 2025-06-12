// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/json"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"
	webhook_module "forgejo.org/modules/webhook"

	"github.com/nektos/act/pkg/jobparser"
	"xorm.io/builder"
)

// ActionRun represents a run of a workflow file
type ActionRun struct {
	ID                int64
	Title             string
	RepoID            int64                  `xorm:"index unique(repo_index)"`
	Repo              *repo_model.Repository `xorm:"-"`
	OwnerID           int64                  `xorm:"index"`
	WorkflowID        string                 `xorm:"index"`                    // the name of workflow file
	Index             int64                  `xorm:"index unique(repo_index)"` // a unique number for each run of a repository
	TriggerUserID     int64                  `xorm:"index"`
	TriggerUser       *user_model.User       `xorm:"-"`
	ScheduleID        int64
	Ref               string `xorm:"index"` // the commit/tag/… that caused the run
	IsRefDeleted      bool   `xorm:"-"`
	CommitSHA         string
	IsForkPullRequest bool                         // If this is triggered by a PR from a forked repository or an untrusted user, we need to check if it is approved and limit permissions when running the workflow.
	NeedApproval      bool                         // may need approval if it's a fork pull request
	ApprovedBy        int64                        `xorm:"index"` // who approved
	Event             webhook_module.HookEventType // the webhook event that causes the workflow to run
	EventPayload      string                       `xorm:"LONGTEXT"`
	TriggerEvent      string                       // the trigger event defined in the `on` configuration of the triggered workflow
	Status            Status                       `xorm:"index"`
	Version           int                          `xorm:"version default 0"` // Status could be updated concomitantly, so an optimistic lock is needed
	// Started and Stopped is used for recording last run time, if rerun happened, they will be reset to 0
	Started timeutil.TimeStamp
	Stopped timeutil.TimeStamp
	// PreviousDuration is used for recording previous duration
	PreviousDuration time.Duration
	Created          timeutil.TimeStamp `xorm:"created"`
	Updated          timeutil.TimeStamp `xorm:"updated"`
}

func init() {
	db.RegisterModel(new(ActionRun))
	db.RegisterModel(new(ActionRunIndex))
}

func (run *ActionRun) HTMLURL() string {
	if run.Repo == nil {
		return ""
	}
	return fmt.Sprintf("%s/actions/runs/%d", run.Repo.HTMLURL(), run.Index)
}

func (run *ActionRun) Link() string {
	if run.Repo == nil {
		return ""
	}
	return fmt.Sprintf("%s/actions/runs/%d", run.Repo.Link(), run.Index)
}

// RefLink return the url of run's ref
func (run *ActionRun) RefLink() string {
	refName := git.RefName(run.Ref)
	if refName.IsPull() {
		return run.Repo.Link() + "/pulls/" + refName.ShortName()
	}
	return git.RefURL(run.Repo.Link(), run.Ref)
}

// PrettyRef return #id for pull ref or ShortName for others
func (run *ActionRun) PrettyRef() string {
	refName := git.RefName(run.Ref)
	if refName.IsPull() {
		return "#" + strings.TrimSuffix(strings.TrimPrefix(run.Ref, git.PullPrefix), "/head")
	}
	return refName.ShortName()
}

// LoadAttributes load Repo TriggerUser if not loaded
func (run *ActionRun) LoadAttributes(ctx context.Context) error {
	if run == nil {
		return nil
	}

	if err := run.LoadRepo(ctx); err != nil {
		return err
	}

	if err := run.Repo.LoadAttributes(ctx); err != nil {
		return err
	}

	if run.TriggerUser == nil {
		u, err := user_model.GetPossibleUserByID(ctx, run.TriggerUserID)
		if err != nil {
			return err
		}
		run.TriggerUser = u
	}

	return nil
}

func (run *ActionRun) LoadRepo(ctx context.Context) error {
	if run == nil || run.Repo != nil {
		return nil
	}

	repo, err := repo_model.GetRepositoryByID(ctx, run.RepoID)
	if err != nil {
		return err
	}
	run.Repo = repo
	return nil
}

func (run *ActionRun) Duration() time.Duration {
	return calculateDuration(run.Started, run.Stopped, run.Status) + run.PreviousDuration
}

func (run *ActionRun) GetPushEventPayload() (*api.PushPayload, error) {
	if run.Event == webhook_module.HookEventPush {
		var payload api.PushPayload
		if err := json.Unmarshal([]byte(run.EventPayload), &payload); err != nil {
			return nil, err
		}
		return &payload, nil
	}
	return nil, fmt.Errorf("event %s is not a push event", run.Event)
}

func (run *ActionRun) GetPullRequestEventPayload() (*api.PullRequestPayload, error) {
	if run.Event == webhook_module.HookEventPullRequest ||
		run.Event == webhook_module.HookEventPullRequestSync ||
		run.Event == webhook_module.HookEventPullRequestAssign ||
		run.Event == webhook_module.HookEventPullRequestMilestone ||
		run.Event == webhook_module.HookEventPullRequestLabel {
		var payload api.PullRequestPayload
		if err := json.Unmarshal([]byte(run.EventPayload), &payload); err != nil {
			return nil, err
		}
		return &payload, nil
	}
	return nil, fmt.Errorf("event %s is not a pull request event", run.Event)
}

func updateRepoRunsNumbers(ctx context.Context, repo *repo_model.Repository) error {
	_, err := db.GetEngine(ctx).ID(repo.ID).
		SetExpr("num_action_runs",
			builder.Select("count(*)").From("action_run").
				Where(builder.Eq{"repo_id": repo.ID}),
		).
		SetExpr("num_closed_action_runs",
			builder.Select("count(*)").From("action_run").
				Where(builder.Eq{
					"repo_id": repo.ID,
				}.And(
					builder.In("status",
						StatusSuccess,
						StatusFailure,
						StatusCancelled,
						StatusSkipped,
					),
				),
				),
		).
		Update(repo)
	return err
}

// InsertRun inserts a run
// The title will be cut off at 255 characters if it's longer than 255 characters.
// We don't have to send the ActionRunNowDone notification here because there are no runs that start in a not done status.
func InsertRun(ctx context.Context, run *ActionRun, jobs []*jobparser.SingleWorkflow) error {
	ctx, commiter, err := db.TxContext(ctx)
	if err != nil {
		return err
	}
	defer commiter.Close()

	index, err := db.GetNextResourceIndex(ctx, "action_run_index", run.RepoID)
	if err != nil {
		return err
	}
	run.Index = index
	run.Title, _ = util.SplitStringAtByteN(run.Title, 255)

	if err := db.Insert(ctx, run); err != nil {
		return err
	}

	if run.Repo == nil {
		repo, err := repo_model.GetRepositoryByID(ctx, run.RepoID)
		if err != nil {
			return err
		}
		run.Repo = repo
	}

	if err := updateRepoRunsNumbers(ctx, run.Repo); err != nil {
		return err
	}

	runJobs := make([]*ActionRunJob, 0, len(jobs))
	var hasWaiting bool
	for _, v := range jobs {
		id, job := v.Job()
		status := StatusFailure
		payload := []byte{}
		needs := []string{}
		name := run.Title
		runsOn := []string{}
		if job != nil {
			needs = job.Needs()
			if err := v.SetJob(id, job.EraseNeeds()); err != nil {
				return err
			}
			payload, _ = v.Marshal()

			if len(needs) > 0 || run.NeedApproval {
				status = StatusBlocked
			} else {
				status = StatusWaiting
				hasWaiting = true
			}
			name, _ = util.SplitStringAtByteN(job.Name, 255)
			runsOn = job.RunsOn()
		}
		runJobs = append(runJobs, &ActionRunJob{
			RunID:             run.ID,
			RepoID:            run.RepoID,
			OwnerID:           run.OwnerID,
			CommitSHA:         run.CommitSHA,
			IsForkPullRequest: run.IsForkPullRequest,
			Name:              name,
			WorkflowPayload:   payload,
			JobID:             id,
			Needs:             needs,
			RunsOn:            runsOn,
			Status:            status,
		})
	}
	if err := db.Insert(ctx, runJobs); err != nil {
		return err
	}

	// if there is a job in the waiting status, increase tasks version.
	if hasWaiting {
		if err := IncreaseTaskVersion(ctx, run.OwnerID, run.RepoID); err != nil {
			return err
		}
	}

	return commiter.Commit()
}

func GetLatestRun(ctx context.Context, repoID int64) (*ActionRun, error) {
	var run ActionRun
	has, err := db.GetEngine(ctx).Where("repo_id=?", repoID).OrderBy("id DESC").Limit(1).Get(&run)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, fmt.Errorf("latest run: %w", util.ErrNotExist)
	}
	return &run, nil
}

// GetRunBefore returns the last run that completed a given timestamp (not inclusive).
func GetRunBefore(ctx context.Context, repoID int64, timestamp timeutil.TimeStamp) (*ActionRun, error) {
	var run ActionRun
	has, err := db.GetEngine(ctx).Where("repo_id=? AND stopped IS NOT NULL AND stopped<?", repoID, timestamp).OrderBy("stopped DESC").Limit(1).Get(&run)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, fmt.Errorf("run before: %w", util.ErrNotExist)
	}
	return &run, nil
}

func GetLatestRunForBranchAndWorkflow(ctx context.Context, repoID int64, branch, workflowFile, event string) (*ActionRun, error) {
	var run ActionRun
	q := db.GetEngine(ctx).Where("repo_id=?", repoID).And("workflow_id=?", workflowFile)
	if event != "" {
		q = q.And("event=?", event)
	}
	if branch != "" {
		q = q.And("ref=?", branch)
	}
	has, err := q.Desc("id").Get(&run)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, util.NewNotExistErrorf("run with repo_id %d, ref %s, event %s, workflow_id %s", repoID, branch, event, workflowFile)
	}
	return &run, nil
}

func GetRunByID(ctx context.Context, id int64) (*ActionRun, error) {
	var run ActionRun
	has, err := db.GetEngine(ctx).Where("id=?", id).Get(&run)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, fmt.Errorf("run with id %d: %w", id, util.ErrNotExist)
	}

	return &run, nil
}

func GetRunByIndex(ctx context.Context, repoID, index int64) (*ActionRun, error) {
	run := &ActionRun{
		RepoID: repoID,
		Index:  index,
	}
	has, err := db.GetEngine(ctx).Get(run)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, fmt.Errorf("run with index %d %d: %w", repoID, index, util.ErrNotExist)
	}

	return run, nil
}

// UpdateRun updates a run.
// It requires the inputted run has Version set.
// It will return error if the version is not matched (it means the run has been changed after loaded).
// All calls to UpdateRunWithoutNotification that change run.Status from a not done status to a done status must call the ActionRunNowDone notification channel.
// Use the wrapper function UpdateRun instead.
func UpdateRunWithoutNotification(ctx context.Context, run *ActionRun, cols ...string) error {
	sess := db.GetEngine(ctx).ID(run.ID)
	if len(cols) > 0 {
		sess.Cols(cols...)
	}
	run.Title, _ = util.SplitStringAtByteN(run.Title, 255)
	affected, err := sess.Update(run)
	if err != nil {
		return err
	}
	if affected == 0 {
		return errors.New("run has changed")
		// It's impossible that the run is not found, since Gitea never deletes runs.
	}

	if run.Status != 0 || slices.Contains(cols, "status") {
		if run.RepoID == 0 {
			run, err = GetRunByID(ctx, run.ID)
			if err != nil {
				return err
			}
		}
		if run.Repo == nil {
			repo, err := repo_model.GetRepositoryByID(ctx, run.RepoID)
			if err != nil {
				return err
			}
			run.Repo = repo
		}
		if err := updateRepoRunsNumbers(ctx, run.Repo); err != nil {
			return err
		}
	}

	return nil
}

type ActionRunIndex db.ResourceIndex
