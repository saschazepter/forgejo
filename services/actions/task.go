// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"
	"fmt"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/db"
	secret_model "forgejo.org/models/secret"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"

	runnerv1 "code.gitea.io/actions-proto-go/runner/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func PickTask(ctx context.Context, runner *actions_model.ActionRunner) (*runnerv1.Task, bool, error) {
	var (
		task *runnerv1.Task
		job  *actions_model.ActionRunJob
	)

	if err := db.WithTx(ctx, func(ctx context.Context) error {
		t, ok, err := actions_model.CreateTaskForRunner(ctx, runner)
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

		secrets, err := secret_model.GetSecretsOfTask(ctx, t)
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

func generateTaskContext(t *actions_model.ActionTask) (*structpb.Struct, error) {
	giteaRuntimeToken, err := CreateAuthorizationToken(t.ID, t.Job.RunID, t.JobID)
	if err != nil {
		return nil, err
	}

	gitCtx := GenerateGiteaContext(t.Job.Run, t.Job)
	gitCtx["token"] = t.Token
	gitCtx["gitea_runtime_token"] = giteaRuntimeToken

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

	ctx, commiter, err := db.TxContext(ctx)
	if err != nil {
		return nil, err
	}
	defer commiter.Close()

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

	if err := commiter.Commit(); err != nil {
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
