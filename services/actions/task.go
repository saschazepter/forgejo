// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"
	"fmt"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	actions_module "forgejo.org/modules/actions"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"code.forgejo.org/forgejo/runner/v13/act/jobparser"
	"code.forgejo.org/xorm/xorm"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"xorm.io/builder"
)

var ErrEphemeralRunnerHasAssignedTask = errors.New("ephemeral runner already has an assigned task")

func PickTask(ctx context.Context, runner *actions_model.ActionRunner, requestKey, handle *string) (*runnerv1.Task, error) {
	var (
		task *runnerv1.Task
		job  *actions_model.ActionRunJob
	)

	if runner.Ephemeral {
		hasRunnerAssignedTask, err := actions_model.HasTaskForRunner(ctx, runner.ID)
		// Let the runner retry the request, do not allow to proceed
		if err != nil {
			return nil, err
		}

		// if runner has task, dont assign new task
		if hasRunnerAssignedTask {
			return nil, ErrEphemeralRunnerHasAssignedTask
		}
	}

	if err := db.WithTx(ctx, func(ctx context.Context) error {
		t, err := CreateTaskForRunner(ctx, runner, requestKey, handle)
		if err != nil {
			return fmt.Errorf("CreateTaskForRunner: %w", err)
		}

		if err := t.LoadAttributes(ctx); err != nil {
			return fmt.Errorf("task LoadAttributes: %w", err)
		}
		job = t.Job

		secrets, err := getSecretsOfTask(ctx, t)
		if err != nil {
			return fmt.Errorf("GetSecretsOfTask: %w", err)
		}

		vars, err := actions_model.GetVariablesOfRun(ctx, t.Job.Run)
		if err != nil {
			return fmt.Errorf("GetVariablesOfRun: %w", err)
		}

		needs, err := findTaskNeeds(ctx, job)
		if err != nil {
			return fmt.Errorf("findTaskNeeds: %w", err)
		}

		unit, err := t.Job.Run.Repo.GetUnit(ctx, unit.TypeActions)
		if err != nil {
			return fmt.Errorf("GetUnit: %w", err)
		}

		taskContext, err := generateTaskContext(t, unit.ActionsConfig())
		if err != nil {
			return fmt.Errorf("generateTaskContext: %w", err)
		}

		task = &runnerv1.Task{
			Id:              t.ID,
			WorkflowPayload: t.Job.WorkflowPayload,
			Context:         taskContext,
			Secrets:         secrets,
			Vars:            vars,
			Needs:           needs,
		}

		return nil
	}); err != nil {
		return nil, err
	}

	CreateCommitStatus(ctx, job)

	return task, nil
}

func IsNoTaskAvailable(err error) bool {
	return errors.Is(err, ErrEphemeralRunnerHasAssignedTask) ||
		errors.Is(err, actions_model.ErrNoMatchingJobFound) ||
		errors.Is(err, actions_model.ErrNoJobUpdated)
}

func RecoverTasks(ctx context.Context, tasks []*actions_model.ActionTask) ([]*runnerv1.Task, error) {
	retval := make([]*runnerv1.Task, len(tasks))

	err := db.WithTx(ctx, func(ctx context.Context) error {
		for i, t := range tasks {
			// `Token` is stored in the database w/ a one-way hash, so we can't recover it from the original.  Instead
			// we generate a new token to create usable runnerv1.Task objects.
			t.GenerateToken()
			if err := t.UpdateToken(ctx); err != nil {
				return fmt.Errorf("UpdateTask failed: %w", err)
			}

			if err := t.LoadAttributes(ctx); err != nil {
				return fmt.Errorf("task LoadAttributes: %w", err)
			}
			job := t.Job

			secrets, err := getSecretsOfTask(ctx, t)
			if err != nil {
				return fmt.Errorf("GetSecretsOfTask: %w", err)
			}

			vars, err := actions_model.GetVariablesOfRun(ctx, t.Job.Run)
			if err != nil {
				return fmt.Errorf("GetVariablesOfRun: %w", err)
			}

			needs, err := findTaskNeeds(ctx, job)
			if err != nil {
				return fmt.Errorf("findTaskNeeds: %w", err)
			}

			unit, err := t.Job.Run.Repo.GetUnit(ctx, unit.TypeActions)
			if err != nil {
				return fmt.Errorf("GetUnit: %w", err)
			}

			taskContext, err := generateTaskContext(t, unit.ActionsConfig())
			if err != nil {
				return fmt.Errorf("generateTaskContext: %w", err)
			}

			retval[i] = &runnerv1.Task{
				Id:              t.ID,
				WorkflowPayload: t.Job.WorkflowPayload,
				Context:         taskContext,
				Secrets:         secrets,
				Vars:            vars,
				Needs:           needs,
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return retval, nil
}

func generateTaskContext(t *actions_model.ActionTask, ac *repo_model.ActionsConfig) (*structpb.Struct, error) {
	run := t.Job.Run
	gitCtx, err := GenerateGiteaContext(run, t.Job)
	if err != nil {
		return nil, err
	}
	gitCtx["token"] = t.Token

	enableOpenIDConnect, err := t.Job.EnableOpenIDConnect()
	if err != nil {
		return nil, err
	}

	// Override the setting from the workflow is this is coming from a fork pull request
	// and this isn't a pull_request_target event.
	if run.IsForkPullRequest && run.TriggerEvent != actions_module.GithubEventPullRequestTarget {
		enableOpenIDConnect = false
	}

	giteaRuntimeToken, err := CreateAuthorizationToken(t, gitCtx, enableOpenIDConnect, ac)
	if err != nil {
		return nil, err
	}

	gitCtx["gitea_runtime_token"] = giteaRuntimeToken // Can be removed after Forgejo 19.
	gitCtx["forgejo_runtime_token"] = giteaRuntimeToken

	if enableOpenIDConnect {
		gitCtx["forgejo_actions_id_token_request_token"] = giteaRuntimeToken
		// The "placeholder=true" at the end of the URL is meaningless, but we need a param
		// here if we want to match the format used in GitHub actions examples (e.g., to ensure
		// that "ACTIONS_ID_TOKEN_REQUEST_URL&audience=..." will work as expected).
		gitCtx["forgejo_actions_id_token_request_url"] = setting.AppURL + fmt.Sprintf("api/actions/_apis/pipelines/workflows/%d/idtoken?placeholder=true", t.Job.RunID)
	}

	return structpb.NewStruct(gitCtx)
}

func findTaskNeeds(ctx context.Context, taskJob *actions_model.ActionRunJob) (map[string]*runnerv1.TaskNeed, error) {
	taskNeeds, err := FindTaskNeeds(ctx, taskJob)
	if err != nil {
		return nil, err
	}
	ret := make(map[string]*runnerv1.TaskNeed, len(taskNeeds))
	for jobID, taskNeed := range taskNeeds {
		ret[string(jobID.ToLocal(taskJob.JobNamespace))] = &runnerv1.TaskNeed{
			Outputs: taskNeed.Outputs,
			Result:  runnerv1.Result(taskNeed.Result),
		}
	}
	return ret, nil
}

func stopTask(ctx context.Context, taskID int64, status actions_model.Status) error {
	if !status.IsDone() {
		return fmt.Errorf("new task status %v is not acceptable", status)
	}

	task, err := actions_model.GetTaskByID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("failed to get task %d: %w", taskID, err)
	}
	if task.Status.IsDone() {
		return nil
	}

	now := timeutil.TimeStampNow()
	task.Status = status
	task.Stopped = now
	if err := actions_model.UpdateTask(ctx, task, "status", "stopped"); err != nil {
		return fmt.Errorf("failed to update task %d: %w", task.ID, err)
	}

	if err := task.LoadAttributes(ctx); err != nil {
		return err
	}

	for _, step := range task.Steps {
		if !step.Status.IsDone() {
			step.Status = status
			if step.Started == 0 {
				step.Started = now
			}
			step.Stopped = now
		}
		e := db.GetEngine(ctx)
		if _, err := e.ID(step.ID).Update(step); err != nil {
			return err
		}
	}

	if err = actions_model.DeleteEphemeralRunner(ctx, task.RunnerID); err != nil {
		return fmt.Errorf("failed to remove ephemeral runner %d after task %d was stopped: %w", task.RunnerID, task.ID, err)
	}

	return nil
}

func StopTask(ctx context.Context, taskID int64, status actions_model.Status) error {
	if !status.IsDone() {
		return fmt.Errorf("new task status %v is not acceptable", status)
	}

	if err := stopTask(ctx, taskID, status); err != nil {
		return fmt.Errorf("failed to stop task %d: %w", taskID, err)
	}

	task, err := actions_model.GetTaskByID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("failed to get task %d: %w", taskID, err)
	}

	job, err := actions_model.GetRunJobByID(ctx, task.JobID)
	if err != nil {
		return fmt.Errorf("could not load job %d: %w", task.JobID, err)
	}

	priorStatus := job.Status
	job.Status = task.Status
	job.Stopped = task.Stopped

	if _, err := actions_model.UpdateRunJobWithoutNotification(ctx, job, nil); err != nil {
		return fmt.Errorf("failed to update job %d: %w", job.ID, err)
	}

	if err = PropagateJobStatus(ctx, job.ID, priorStatus); err != nil {
		return fmt.Errorf("could not propagate changed status of job %d: %w", job.ID, err)
	}

	if err := RefreshAndPropagateRunStatus(ctx, job.RunID); err != nil {
		return fmt.Errorf("could not update status of run %d: %w", job.RunID, err)
	}

	return nil
}

// UpdateTaskByState updates the task by the state.
// It will always update the task if the state is not final, even there is no change.
// So it will update ActionTask.Updated to avoid the task being judged as a zombie task.
func UpdateTaskByState(ctx context.Context, runnerID int64, state *runnerv1.TaskState) (*actions_model.ActionTask, error) {
	stepStates := map[int64]*runnerv1.StepState{}
	for _, v := range state.Steps {
		stepStates[v.Id] = v
	}

	ctx, committer, err := db.TxContext(ctx)
	if err != nil {
		return nil, err
	}
	defer committer.Close()

	e := db.GetEngine(ctx)

	task, err := actions_model.GetTaskByID(ctx, state.Id)
	if err != nil {
		return nil, fmt.Errorf("failed to get task %d: %w", state.Id, err)
	}
	if runnerID != task.RunnerID {
		return nil, errors.New("invalid runner for task")
	}

	if task.Status.IsDone() {
		// the state is final, do nothing
		return task, nil
	}

	// state.Result is not unspecified means the task is finished
	if state.Result != runnerv1.Result_RESULT_UNSPECIFIED {
		task.Status = actions_model.Status(state.Result)
		task.Stopped = timeutil.TimeStamp(state.StoppedAt.AsTime().Unix())
		if err := actions_model.UpdateTask(ctx, task, "status", "stopped"); err != nil {
			return nil, fmt.Errorf("failed to update task %d: %w", task.ID, err)
		}

		job, err := actions_model.GetRunJobByID(ctx, task.JobID)
		if err != nil {
			return nil, fmt.Errorf("could not load job %d: %w", task.JobID, err)
		}

		priorStatus := job.Status
		job.Status = task.Status
		job.Stopped = task.Stopped

		if _, err := actions_model.UpdateRunJobWithoutNotification(ctx, job, nil); err != nil {
			return nil, fmt.Errorf("failed to update job %d: %w", job.ID, err)
		}

		if err = PropagateJobStatus(ctx, job.ID, priorStatus); err != nil {
			return nil, fmt.Errorf("could not propagate changed status of job %d: %w", job.ID, err)
		}

		if err := RefreshAndPropagateRunStatus(ctx, job.RunID); err != nil {
			return nil, fmt.Errorf("could not refresh and propagate status of run %d: %w", job.RunID, err)
		}
	} else {
		// Force update ActionTask.Updated to avoid the task being judged as a zombie task
		task.Updated = timeutil.TimeStampNow()
		if err := actions_model.UpdateTask(ctx, task, "updated"); err != nil {
			return nil, err
		}
	}

	if err := task.LoadAttributes(ctx); err != nil {
		return nil, err
	}

	for _, step := range task.Steps {
		var result runnerv1.Result
		if v, ok := stepStates[step.Index]; ok {
			result = v.Result
			step.LogIndex = v.LogIndex
			step.LogLength = v.LogLength
			step.Started = convertTimestamp(v.StartedAt)
			step.Stopped = convertTimestamp(v.StoppedAt)
		}
		if result != runnerv1.Result_RESULT_UNSPECIFIED {
			step.Status = actions_model.Status(result)
		} else if step.Started != 0 {
			step.Status = actions_model.StatusRunning
		}
		if _, err := e.ID(step.ID).Update(step); err != nil {
			return nil, err
		}
	}

	if err := committer.Commit(); err != nil {
		return nil, err
	}

	return task, nil
}

func convertTimestamp(timestamp *timestamppb.Timestamp) timeutil.TimeStamp {
	if timestamp.GetSeconds() == 0 && timestamp.GetNanos() == 0 {
		return timeutil.TimeStamp(0)
	}
	return timeutil.TimeStamp(timestamp.AsTime().Unix())
}

// deleteTask removes the given task with all associated steps, outputs, logs, and ephemeral runners, if any. For
// deleteTask to succeed, it must have completed. If it has not, an error is returned. If the given task does not exist,
// nothing happens.
func deleteTask(ctx context.Context, taskID int64) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		task, err := actions_model.GetTaskByID(ctx, taskID)
		if err != nil {
			if errors.Is(err, util.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("unable to load task %d: %w", taskID, err)
		}

		if !task.Status.IsDone() {
			return fmt.Errorf("unable to remove task %d because it has not completed yet", taskID)
		}

		if task.HasLogs() {
			err = actions_module.RemoveLogs(ctx, task.LogInStorage, task.LogFilename)
			if err != nil {
				return fmt.Errorf("unable to remove logs of task %d: %w", taskID, err)
			}
		}

		// Whether an ephemeral runner has been used is determined based on whether it is assigned to a task.
		// Consequently, ephemeral runners have to be cleaned up before any task can be removed.
		err = actions_model.DeleteEphemeralRunner(ctx, task.RunnerID)
		if err != nil {
			return fmt.Errorf("unable to cleanup ephemeral runners before removing task %d: %w", taskID, err)
		}
		err = actions_model.DeleteTask(ctx, task.ID)
		if err != nil {
			return fmt.Errorf("unable to remove task %d: %w", task.ID, err)
		}

		return nil
	})
}

func CreateTaskForRunner(ctx context.Context, runner *actions_model.ActionRunner, requestKey, handle *string) (*actions_model.ActionTask, error) {
	ctx, committer, err := db.TxContext(ctx)
	if err != nil {
		return nil, err
	}
	defer committer.Close()

	e := db.GetEngine(ctx)

	jobs, err := actions_model.GetAvailableJobsForRunner(e, runner)
	if err != nil {
		return nil, err
	}

	// TODO: a more efficient way to filter labels
	var job *actions_model.ActionRunJob
	log.Trace("runner labels: %v", runner.AgentLabels)
	for _, j := range jobs {
		if j.IsRequestedByRunner(handle) && j.ItRunsOn(runner.AgentLabels) {
			job = j
			break
		}
	}
	if job == nil {
		return nil, actions_model.ErrNoMatchingJobFound
	}
	if err := job.LoadAttributes(ctx); err != nil {
		return nil, err
	}

	priorStatus := job.Status

	now := timeutil.TimeStampNow()
	job.Started = now
	job.Status = actions_model.StatusRunning

	task := &actions_model.ActionTask{
		JobID:             job.ID,
		Attempt:           job.Attempt,
		RunnerID:          runner.ID,
		Started:           now,
		Status:            actions_model.StatusRunning,
		RepoID:            job.RepoID,
		OwnerID:           job.OwnerID,
		CommitSHA:         job.CommitSHA,
		IsForkPullRequest: job.IsForkPullRequest,
	}
	if requestKey != nil {
		task.RunnerRequestKey = *requestKey
	}
	task.GenerateToken()

	var workflowJob *jobparser.Job
	if gots, err := jobparser.Parse(job.WorkflowPayload, false); err != nil {
		return nil, fmt.Errorf("parse workflow of job %d: %w", job.ID, err)
	} else if len(gots) != 1 {
		return nil, fmt.Errorf("workflow of job %d: not single workflow", job.ID)
	} else { //nolint:revive
		_, workflowJob = gots[0].Job()
	}

	if _, err := e.Insert(task); err != nil {
		return nil, err
	}

	task.LogFilename = logFileName(job.Run.Repo, task.ID)
	if err := actions_model.UpdateTask(ctx, task, "log_filename"); err != nil {
		return nil, err
	}

	if len(workflowJob.Steps) > 0 {
		steps := make([]*actions_model.ActionTaskStep, len(workflowJob.Steps))
		for i, v := range workflowJob.Steps {
			name, _ := util.SplitStringAtByteN(v.String(), 255)
			steps[i] = &actions_model.ActionTaskStep{
				Name:   name,
				TaskID: task.ID,
				Index:  int64(i),
				RepoID: task.RepoID,
				Status: actions_model.StatusWaiting,
			}
		}
		if _, err := e.Insert(steps); err != nil {
			return nil, err
		}
		task.Steps = steps
	}

	job.TaskID = task.ID
	// We never have to send a notification here because the job is started with a not done status.
	//
	// ErrDeadlock can occur on MariaDB w/ `innodb_snapshot_isolation`, rather than returning 0 records -- we can treat
	// that just the same and return the `ErrNoJobUpdated` error code. An alternative would be to use READ COMMITTED
	// transaction isolation level, but models/db doesn't currently expose that, and it would cause transaction nesting
	// difficulties.
	if n, err := actions_model.UpdateRunJobWithoutNotification(ctx, job, builder.Eq{"task_id": 0}); err != nil && errors.Is(err, xorm.ErrDeadlock) {
		return nil, actions_model.ErrNoJobUpdated
	} else if err != nil {
		return nil, err
	} else if n != 1 {
		return nil, actions_model.ErrNoJobUpdated
	}

	if job.Run.Started.IsZero() {
		job.Run.Started = job.Started

		if err = actions_model.UpdateRun(ctx, job.Run); err != nil {
			return nil, fmt.Errorf("could not update run %d of job %d: %w", job.RunID, job.ID, err)
		}
	}

	if err = PropagateJobStatus(ctx, job.ID, priorStatus); err != nil {
		return nil, fmt.Errorf("could not propagate changed status of job %d: %w", job.ID, err)
	}
	if err = RefreshAndPropagateRunStatus(ctx, job.RunID); err != nil {
		return nil, fmt.Errorf("could not refresh and propagate the status of run %d: %w", job.RunID, err)
	}

	task.Job = job

	if err := committer.Commit(); err != nil {
		return nil, err
	}

	return task, nil
}

func logFileName(repo *repo_model.Repository, taskID int64) string {
	ret := fmt.Sprintf("%s/%02x/%d.log", repo.FullName(), taskID%256, taskID)

	if setting.Actions.LogCompression.IsZstd() {
		ret += ".zst"
	}

	return ret
}
