// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"
	"fmt"
	"time"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/modules/log"
	"forgejo.org/modules/timeutil"
	webhook_module "forgejo.org/modules/webhook"

	"github.com/nektos/act/pkg/jobparser"
	"xorm.io/builder"
)

// StartScheduleTasks start the task
func StartScheduleTasks(ctx context.Context) error {
	return startTasks(ctx)
}

// startTasks retrieves specifications in pages, creates a schedule task for each specification,
// and updates the specification's next run time and previous run time.
// The function returns an error if there's an issue with finding or updating the specifications.
func startTasks(ctx context.Context) error {
	// Set the page size
	pageSize := 50

	// Retrieve specs in pages until all specs have been retrieved
	now := time.Now()
	for page := 1; ; page++ {
		// Retrieve the specs for the current page
		specs, _, err := actions_model.FindSpecs(ctx, actions_model.FindSpecOptions{
			ListOptions: db.ListOptions{
				Page:     page,
				PageSize: pageSize,
			},
			Next: now.Unix(),
		})
		if err != nil {
			return fmt.Errorf("find specs: %w", err)
		}

		if err := specs.LoadRepos(ctx); err != nil {
			return fmt.Errorf("LoadRepos: %w", err)
		}

		// Loop through each spec and create a schedule task for it
		for _, row := range specs {
			// cancel running jobs if the event is push
			if row.Schedule.Event == webhook_module.HookEventPush {
				// cancel running jobs of the same workflow
				if err := CancelPreviousJobs(
					ctx,
					row.RepoID,
					row.Schedule.Ref,
					row.Schedule.WorkflowID,
					webhook_module.HookEventSchedule,
				); err != nil {
					log.Error("CancelPreviousJobs: %v", err)
				}
			}

			if row.Repo.IsArchived {
				// Skip if the repo is archived
				continue
			}

			cfg, err := row.Repo.GetUnit(ctx, unit.TypeActions)
			if err != nil {
				if repo_model.IsErrUnitTypeNotExist(err) {
					// Skip the actions unit of this repo is disabled.
					continue
				}
				return fmt.Errorf("GetUnit: %w", err)
			}
			if cfg.ActionsConfig().IsWorkflowDisabled(row.Schedule.WorkflowID) {
				continue
			}

			if err := CreateScheduleTask(ctx, row.Schedule); err != nil {
				log.Error("CreateScheduleTask: %v", err)
				return err
			}

			// Parse the spec
			schedule, err := row.Parse()
			if err != nil {
				log.Error("Parse: %v", err)
				return err
			}

			// Update the spec's next run time and previous run time
			row.Prev = row.Next
			row.Next = timeutil.TimeStamp(schedule.Next(now.Add(1 * time.Minute)).Unix())
			if err := actions_model.UpdateScheduleSpec(ctx, row, "prev", "next"); err != nil {
				log.Error("UpdateScheduleSpec: %v", err)
				return err
			}
		}

		// Stop if all specs have been retrieved
		if len(specs) < pageSize {
			break
		}
	}

	return nil
}

// CreateScheduleTask creates a scheduled task from a cron action schedule.
// It creates an action run based on the schedule, inserts it into the database, and creates commit statuses for each job.
func CreateScheduleTask(ctx context.Context, cron *actions_model.ActionSchedule) error {
	// Create a new action run based on the schedule
	run := &actions_model.ActionRun{
		Title:         cron.Title,
		RepoID:        cron.RepoID,
		OwnerID:       cron.OwnerID,
		WorkflowID:    cron.WorkflowID,
		TriggerUserID: cron.TriggerUserID,
		Ref:           cron.Ref,
		CommitSHA:     cron.CommitSHA,
		Event:         cron.Event,
		EventPayload:  cron.EventPayload,
		TriggerEvent:  string(webhook_module.HookEventSchedule),
		ScheduleID:    cron.ID,
		Status:        actions_model.StatusWaiting,
	}

	vars, err := actions_model.GetVariablesOfRun(ctx, run)
	if err != nil {
		log.Error("GetVariablesOfRun: %v", err)
		return err
	}

	// Parse the workflow specification from the cron schedule
	workflows, err := jobparser.Parse(cron.Content, jobparser.WithVars(vars))
	if err != nil {
		return err
	}

	// Insert the action run and its associated jobs into the database
	if err := actions_model.InsertRun(ctx, run, workflows); err != nil {
		return err
	}

	// Return nil if no errors occurred
	return nil
}

// CancelPreviousJobs cancels all previous jobs of the same repository, reference, workflow, and event.
// It's useful when a new run is triggered, and all previous runs needn't be continued anymore.
func CancelPreviousJobs(ctx context.Context, repoID int64, ref, workflowID string, event webhook_module.HookEventType) error {
	// Find all runs in the specified repository, reference, and workflow with non-final status
	runs, total, err := db.FindAndCount[actions_model.ActionRun](ctx, actions_model.FindRunOptions{
		RepoID:       repoID,
		Ref:          ref,
		WorkflowID:   workflowID,
		TriggerEvent: event,
		Status:       []actions_model.Status{actions_model.StatusRunning, actions_model.StatusWaiting, actions_model.StatusBlocked},
	})
	if err != nil {
		return err
	}

	// If there are no runs found, there's no need to proceed with cancellation, so return nil.
	if total == 0 {
		return nil
	}

	// Iterate over each found run and cancel its associated jobs.
	for _, run := range runs {
		// Find all jobs associated with the current run.
		jobs, err := db.Find[actions_model.ActionRunJob](ctx, actions_model.FindRunJobOptions{
			RunID: run.ID,
		})
		if err != nil {
			return err
		}

		// Iterate over each job and attempt to cancel it.
		for _, job := range jobs {
			// Skip jobs that are already in a terminal state (completed, cancelled, etc.).
			status := job.Status
			if status.IsDone() {
				continue
			}

			// If the job has no associated task (probably an error), set its status to 'Cancelled' and stop it.
			if job.TaskID == 0 {
				job.Status = actions_model.StatusCancelled
				job.Stopped = timeutil.TimeStampNow()

				// Update the job's status and stopped time in the database.
				n, err := UpdateRunJob(ctx, job, builder.Eq{"task_id": 0}, "status", "stopped")
				if err != nil {
					return err
				}

				// If the update affected 0 rows, it means the job has changed in the meantime, so we need to try again.
				if n == 0 {
					return errors.New("job has changed, try again")
				}

				// Continue with the next job.
				continue
			}

			// If the job has an associated task, try to stop the task, effectively cancelling the job.
			if err := StopTask(ctx, job.TaskID, actions_model.StatusCancelled); err != nil {
				return err
			}
		}
	}

	// Return nil to indicate successful cancellation of all running and waiting jobs.
	return nil
}

func CleanRepoScheduleTasks(ctx context.Context, repo *repo_model.Repository, cancelPreviousJobs bool) error {
	// If actions disabled when there is schedule task, this will remove the outdated schedule tasks
	// There is no other place we can do this because the app.ini will be changed manually
	if err := actions_model.DeleteScheduleTaskByRepo(ctx, repo.ID); err != nil {
		return fmt.Errorf("DeleteCronTaskByRepo: %v", err)
	}
	if cancelPreviousJobs {
		// cancel running cron jobs of this repository and delete old schedules
		if err := CancelPreviousJobs(
			ctx,
			repo.ID,
			repo.DefaultBranch,
			"",
			webhook_module.HookEventSchedule,
		); err != nil {
			return fmt.Errorf("CancelPreviousJobs: %v", err)
		}
	}
	return nil
}
