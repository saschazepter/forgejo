// Copyright 2026 The Forgejo Authors.
// SPDX-License-Identifier: GPL-3.0-or-later

package actions

import (
	"context"
	"fmt"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/db"
	"forgejo.org/modules/log"
	"forgejo.org/modules/util"
	notify_service "forgejo.org/services/notify"

	"code.forgejo.org/forgejo/runner/v13/act/jobparser"
	gouuid "github.com/google/uuid"
	"xorm.io/builder"
)

func InitiateNextJobAttempt(ctx context.Context, job *actions_model.ActionRunJob, initialStatus actions_model.Status) error {
	oldStatus := job.Status

	if err := job.PrepareNextAttempt(initialStatus); err != nil {
		return err
	}

	// The columns have to be specified here to work around a xorm quirk: It won't update columns that are set to their
	// zero value without AllCols().
	if _, err := actions_model.UpdateRunJobWithoutNotification(ctx, job, builder.Eq{"status": oldStatus}, "handle", "attempt", "task_id", "status", "started", "stopped"); err != nil {
		return err
	}

	if err := PropagateNextJobAttempt(ctx, job.ID); err != nil {
		return err
	}

	CreateCommitStatus(ctx, job)

	return nil
}

// PropagateNextJobAttempt notifies observers that a new execution attempt of the job with the given
// ID has started. Therefore, it is also suitable for newly created jobs. PropagateNextJobAttempt
// expects that the job has been persisted right before it is being invoked. Otherwise, subscribers
// might receive a copy referencing outdated data. PropagateNextJobAttempt will not trigger
// notifications for jobs that are incomplete.
func PropagateNextJobAttempt(ctx context.Context, jobID int64) error {
	// Fetch a new copy from the database. That is necessary to ensure that we have a deep copy of
	// the job that cannot be altered by the code that invoked this function.
	job, err := actions_model.GetRunJobByID(ctx, jobID)
	if err != nil {
		return fmt.Errorf("could not load job %d: %w", jobID, err)
	}

	// Suppress notifications for jobs that are incomplete, because they will be replaced by 0 or
	// more different jobs. Sending notifications for them would be confusing.
	if incomplete, err := job.IsIncomplete(); err != nil {
		return fmt.Errorf("failed to determine whether job %d is incomplete: %w", job.ID, err)
	} else if incomplete {
		log.Debug("Suppressing job notification for job %d because it is incomplete", job.ID)
		return nil
	}

	// Notifications expect an ActionRunJob with all its attributes loaded.
	if err := job.LoadAttributes(ctx); err != nil {
		return fmt.Errorf("failed to load attributes of job %d: %w", job.ID, err)
	}

	notify_service.NewWorkflowJobAttempt(ctx, job)

	return nil
}

// PropagateJobStatus notifies observers that the status of the job with the given ID has changed.
// It will not do anything if priorStatus and the job's status are the same. PropagateJobStatus
// expects that the job has been persisted right before it is being invoked. Otherwise, subscribers
// might receive a copy referencing outdated data. PropagateJobStatus will not trigger
// notifications for jobs that are incomplete.
func PropagateJobStatus(ctx context.Context, jobID int64, priorStatus actions_model.Status) error {
	// Fetch a new copy from the database. That is necessary to ensure that we have a deep copy of
	// the job that cannot be altered by the code that invoked this function.
	job, err := actions_model.GetRunJobByID(ctx, jobID)
	if err != nil {
		return fmt.Errorf("could not load job %d: %w", jobID, err)
	}

	if job.Status == priorStatus {
		return nil
	}

	// Suppress notifications for jobs that are incomplete, because they will be replaced by 0 or
	// more different jobs. Sending notifications for them would be confusing.
	if incomplete, err := job.IsIncomplete(); err != nil {
		return fmt.Errorf("failed to determine whether job %d is incomplete: %w", job.ID, err)
	} else if incomplete {
		log.Debug("Suppressing job notification for job %d because it is incomplete", job.ID)
		return nil
	}

	// Notifications expect an ActionRunJob with all its attributes loaded.
	if err := job.LoadAttributes(ctx); err != nil {
		return fmt.Errorf("failed to load attributes of job %d: %w", job.ID, err)
	}

	if !job.Status.IsDone() {
		notify_service.WorkflowJobStatusChanged(ctx, job, priorStatus)
	} else {
		notify_service.WorkflowJobCompleted(ctx, job, priorStatus)
	}

	return nil
}

// deleteJobsOfRun removes all jobs that belong to the given run, including its associated tasks. Each job has to be
// completed for the operation to succeed.
func deleteJobsOfRun(ctx context.Context, runID int64) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		jobs, err := actions_model.GetRunJobsByRunID(ctx, runID)
		if err != nil {
			return fmt.Errorf("unable to load jobs of run %d: %w", runID, err)
		}

		for _, job := range jobs {
			if !job.Status.IsDone() {
				return fmt.Errorf("unable to delete job %d because it has not completed yet", job.ID)
			}

			tasks, err := actions_model.GetTasksOfJob(ctx, job.ID)
			if err != nil {
				return err
			}
			for _, task := range tasks {
				err = deleteTask(ctx, task.ID)
				if err != nil {
					return err
				}
			}

			err = actions_model.DeleteJob(ctx, job.ID)
			if err != nil {
				return fmt.Errorf("unable to delete job %d of run %d: %w", job.ID, job.RunID, err)
			}
		}

		return nil
	})
}

func convertSingleWorkflowToJobs(run *actions_model.ActionRun, jobs []*jobparser.SingleWorkflow) ([]*actions_model.ActionRunJob, error) {
	runJobs := make([]*actions_model.ActionRunJob, 0, len(jobs))
	for _, v := range jobs {
		id, job := v.Job()
		status := actions_model.StatusFailure
		payload := []byte{}
		needs := []string{}
		name := run.Title
		runsOn := []string{}

		if job != nil {
			needs = job.Needs()
			if err := v.SetJob(id, job.EraseNeeds()); err != nil {
				return nil, err
			}
			payload, _ = v.Marshal()

			if len(needs) > 0 || run.NeedApproval || v.IncompleteMatrix || v.IncompleteRunsOn || v.IncompleteWith {
				status = actions_model.StatusBlocked
			} else if ifPassed, err := job.EvaluateIf(); err == nil && !ifPassed {
				log.Trace("job %q skipped by server-side 'if' evaluation", id)
				status = actions_model.StatusSkipped
			} else {
				if err != nil {
					// `err != nil` case from EvaluateIf intentionally falls through to here, where we do nothing but
					// log the error.  There are two classes of possible error: ErrCannotEvaluateInJobParser, in which
					// we know we can't evaluate if server-side and must dispatch to the runner, and, all other errors
					// which occur because the user made a mistake in their workflow.  As the first class of error
					// requires us to set the job as waiting and send it to a runner, and, we don't have a convenient
					// way to report the second class of errors to the user, we just dispatch all of them to a runner by
					// setting the state to waiting.
					log.Trace("server-side evaluation of 'if' failed with an error; the job can be picked up by a runner: %s", err.Error())
				}
				status = actions_model.StatusWaiting
			}

			name = job.Name
			runsOn = job.RunsOn()
		}

		runJob := &actions_model.ActionRunJob{
			RunID:             run.ID,
			Run:               run,
			RepoID:            run.RepoID,
			OwnerID:           run.OwnerID,
			CommitSHA:         run.CommitSHA,
			IsForkPullRequest: run.IsForkPullRequest,
			Name:              name,
			WorkflowPayload:   payload,
			JobID:             actions_model.JobIdentifier(id),
			JobNamespace:      actions_model.JobNamespace(v.Metadata.Namespace),
			Needs:             util.ConvertSlice[string, actions_model.LocalJobIdentifier](needs),
			RunsOn:            runsOn,
			Status:            status,
			Attempt:           1,
			Handle:            gouuid.New().String(),
		}

		runJobs = append(runJobs, runJob)
	}

	return runJobs, nil
}
