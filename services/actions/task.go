// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"
	"fmt"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/db"
	actions_module "forgejo.org/modules/actions"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func PickTask(ctx context.Context, runner *actions_model.ActionRunner, requestKey, handle *string) (*runnerv1.Task, bool, error) {
	var (
		task *runnerv1.Task
		job  *actions_model.ActionRunJob
	)

	if runner.Ephemeral {
		hasRunnerAssignedTask, err := actions_model.HasTaskForRunner(ctx, runner.ID)
		// Let the runner retry the request, do not allow to proceed
		if err != nil {
			return nil, false, err
		}

		// if runner has task, dont assign new task
		if hasRunnerAssignedTask {
			return nil, false, nil
		}
	}

	if err := db.WithTx(ctx, func(ctx context.Context) error {
		t, ok, err := actions_model.CreateTaskForRunner(ctx, runner, requestKey, handle)
		if err != nil {
			return fmt.Errorf("CreateTaskForRunner: %w", err)
		}
		if !ok {
			return nil
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

		taskContext, err := generateTaskContext(t)
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
		return nil, false, err
	}

	if task == nil {
		return nil, false, nil
	}

	CreateCommitStatus(ctx, job)

	return task, true, nil
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

			taskContext, err := generateTaskContext(t)
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

func generateTaskContext(t *actions_model.ActionTask) (*structpb.Struct, error) {
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

	giteaRuntimeToken, err := CreateAuthorizationToken(t, gitCtx, enableOpenIDConnect)
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
		ret[jobID] = &runnerv1.TaskNeed{
			Outputs: taskNeed.Outputs,
			Result:  runnerv1.Result(taskNeed.Result),
		}
	}
	return ret, nil
}

func StopTask(ctx context.Context, taskID int64, status actions_model.Status) error {
	if !status.IsDone() {
		return fmt.Errorf("cannot stop task with status %v", status)
	}
	e := db.GetEngine(ctx)

	task := &actions_model.ActionTask{}
	if has, err := e.ID(taskID).Get(task); err != nil {
		return err
	} else if !has {
		return util.ErrNotExist
	}
	if task.Status.IsDone() {
		return nil
	}

	now := timeutil.TimeStampNow()
	task.Status = status
	task.Stopped = now
	if _, err := UpdateRunJob(ctx, &actions_model.ActionRunJob{
		ID:      task.JobID,
		Status:  task.Status,
		Stopped: task.Stopped,
	}, nil); err != nil {
		return err
	}

	if err := actions_model.UpdateTask(ctx, task, "status", "stopped"); err != nil {
		return err
	}

	runner := &actions_model.ActionRunner{}
	if _, err := e.ID(task.RunnerID).Get(runner); err != nil {
		return fmt.Errorf("failed to find runner assigned to task")
	}

	if runner.Ephemeral {
		err := actions_model.DeleteRunner(ctx, runner)
		if err != nil {
			return fmt.Errorf("failed to remove ephemeral runner from stopped task: %w", err)
		}
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
		if _, err := e.ID(step.ID).Update(step); err != nil {
			return err
		}
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

	task := &actions_model.ActionTask{}
	if has, err := e.ID(state.Id).Get(task); err != nil {
		return nil, err
	} else if !has {
		return nil, util.ErrNotExist
	} else if runnerID != task.RunnerID {
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
			return nil, err
		}
		if _, err := UpdateRunJob(ctx, &actions_model.ActionRunJob{
			ID:      task.JobID,
			Status:  task.Status,
			Stopped: task.Stopped,
		}, nil); err != nil {
			return nil, err
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
