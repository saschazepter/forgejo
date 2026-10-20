// Copyright The Forgejo Authors.
// SPDX-License-Identifier: MIT

package actions

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/perm"
	"forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/user"
	"forgejo.org/modules/actions"
	"forgejo.org/modules/git"
	"forgejo.org/modules/json"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/structs"
	"forgejo.org/modules/util"
	"forgejo.org/modules/webhook"
	"forgejo.org/services/convert"

	"code.forgejo.org/forgejo/runner/v13/act/jobparser"
	act_model "code.forgejo.org/forgejo/runner/v13/act/model"
)

type InputRequiredErr struct {
	Name string
}

func (err InputRequiredErr) Error() string {
	return fmt.Sprintf("input required for '%s'", err.Name)
}

func IsInputRequiredErr(err error) bool {
	_, ok := err.(InputRequiredErr)
	return ok
}

type Workflow struct {
	WorkflowDirectory string
	WorkflowID        string
	Ref               string
	Commit            *git.Commit
	GitEntry          *git.TreeEntry
}

type InputValueGetter func(key string) string

var ErrSkipDispatchInput = errors.New("skip dispatching of input")

func resolveDispatchInput(key, value string, input act_model.WorkflowDispatchInput) (string, error) {
	if len(value) == 0 {
		value = input.Default
		if len(value) == 0 {
			if input.Required {
				name := input.Description
				if len(name) == 0 {
					name = key
				}
				return "", InputRequiredErr{Name: name}
			}
			return "", ErrSkipDispatchInput
		}
	} else if input.Type == "boolean" {
		// Temporary compatibility shim for people that upgrade to Forgejo 14. Can be removed with Forgejo 15.
		if value == "on" {
			value = "true"
		}
	}

	return value, nil
}

func (entry *Workflow) WorkflowPath() string {
	return entry.WorkflowDirectory + "/" + entry.WorkflowID
}

func (entry *Workflow) Dispatch(ctx context.Context, inputGetter InputValueGetter, repo *repo_model.Repository, doer *user.User) (r *actions_model.ActionRun, j []string, err error) {
	content, err := actions.GetContentFromEntry(entry.GitEntry)
	if err != nil {
		return nil, nil, err
	}

	wf, err := act_model.ReadWorkflow(bytes.NewReader(content), false)
	if err != nil {
		return nil, nil, err
	}

	fullWorkflowID := entry.WorkflowPath()

	title := wf.Name
	if len(title) < 1 {
		title = fullWorkflowID
	}

	// Runner expects a `map[string]string` for inputs in in the payload dispatch, but newer code in the Runner's
	// jobparser library takes a map[string]any which is more directly actionable for parsing:
	inputs := make(map[string]string)
	inputsAny := make(map[string]any)
	if workflowDispatch := wf.WorkflowDispatchConfig(); workflowDispatch != nil {
		for key, input := range workflowDispatch.Inputs {
			value, err := resolveDispatchInput(key, inputGetter(key), input)
			if err == ErrSkipDispatchInput {
				continue
			} else if err != nil {
				return nil, nil, err
			}
			inputs[key] = value
			inputsAny[key] = value
			// To match the behaviour of the runner when parsing map[string]string into map[string]any, check for
			// boolean type inputs and convert them to booleans for expression evaluation:
			// https://code.forgejo.org/forgejo/runner/src/commit/d5693e379c034a3afcb920087570d9a6e179e86e/act/runner/expression.go#L435-L439
			if input.Type == "boolean" {
				inputsAny[key] = value == "true"
			}
		}
	}

	if int64(len(inputs)) > setting.Actions.LimitDispatchInputs {
		return nil, nil, errors.New("too many inputs")
	}

	jobNames := util.KeysOfMap(wf.Jobs)

	payload := &structs.WorkflowDispatchPayload{
		Inputs:     inputsAny,
		Ref:        entry.Ref,
		Repository: convert.ToRepo(ctx, repo, access.Permission{AccessMode: perm.AccessModeNone}),
		Sender:     convert.ToUser(ctx, doer, nil),
		Workflow:   fullWorkflowID,
	}

	p, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}

	notifications, err := wf.Notifications()
	if err != nil {
		return nil, nil, err
	}

	run := &actions_model.ActionRun{
		Title:             title,
		RepoID:            repo.ID,
		Repo:              repo,
		OwnerID:           repo.OwnerID,
		WorkflowID:        entry.WorkflowID,
		WorkflowDirectory: entry.WorkflowDirectory,
		TriggerUserID:     doer.ID,
		TriggerUser:       doer,
		Ref:               entry.Ref,
		CommitSHA:         entry.Commit.ID.String(),
		Event:             webhook.HookEventWorkflowDispatch,
		EventPayload:      string(p),
		TriggerEvent:      string(webhook.HookEventWorkflowDispatch),
		Status:            actions_model.StatusWaiting,
		NotifyEmail:       notifications,
	}

	vars, err := actions_model.GetVariablesOfRun(ctx, run)
	if err != nil {
		return nil, nil, err
	}

	err = ConfigureActionRunConcurrency(wf, run, vars, inputsAny)
	if err != nil {
		return nil, nil, err
	}

	if run.ConcurrencyType == actions_model.CancelInProgress {
		if err := CancelPreviousWithConcurrencyGroup(
			ctx,
			run.RepoID,
			run.ConcurrencyGroup,
		); err != nil {
			return nil, nil, err
		}
	}

	jobs, err := actions.JobParser(content,
		jobparser.WithVars(vars),
		jobparser.WithInputs(inputsAny),
		// We don't have any job outputs yet, but `WithJobOutputs(...)` triggers JobParser to supporting its
		// `IncompleteMatrix` tagging for any jobs that require the inputs of other jobs.
		jobparser.WithJobOutputs(map[string]map[string]string{}),
		jobparser.SupportIncompleteRunsOn(),
		jobparser.ExpandLocalReusableWorkflows(expandLocalReusableWorkflows(entry.Commit)),
		jobparser.ExpandInstanceReusableWorkflows(expandInstanceReusableWorkflows(ctx)),
		jobparser.WithGitContext(generateGiteaContextForRun(run)),
		jobparser.EnableNamespaces(),
	)
	if err != nil {
		return nil, nil, err
	}

	if err := ConfigureActionRunTitle(jobs, run); err != nil {
		return nil, nil, err
	}

	if err := InsertRun(ctx, run, jobs); err != nil {
		return run, jobNames, err
	}

	return run, jobNames, consistencyCheckRun(ctx, run)
}

func GetWorkflowFromCommit(gitRepo *git.Repository, ref, workflowID string) (*Workflow, error) {
	ref, err := gitRepo.ExpandRef(ref)
	if err != nil {
		return nil, err
	}

	commit, err := gitRepo.GetCommit(ref)
	if err != nil {
		if errors.Is(err, util.ErrNotExist) {
			return nil, util.NewInvalidArgumentErrorf("reference '%s' does not exist", ref)
		}
		return nil, err
	}

	workflowDirectory, entries, err := actions.ListWorkflows(commit)
	if err != nil {
		return nil, err
	}

	var workflowEntry *git.TreeEntry
	for _, entry := range entries {
		if entry.Name() == workflowID {
			workflowEntry = entry
			break
		}
	}
	if workflowEntry == nil {
		return nil, util.NewNotExistErrorf("workflow not found")
	}

	return &Workflow{
		WorkflowDirectory: workflowDirectory,
		WorkflowID:        workflowID,
		Ref:               ref,
		Commit:            commit,
		GitEntry:          workflowEntry,
	}, nil
}

// Sets Title of a workflow run to the value of the workflow's `run-name` field, if present.
// Keeps the existing default title when run-name is absent or when run-name evaluates to "".
func ConfigureActionRunTitle(workflows []*jobparser.SingleWorkflow, run *actions_model.ActionRun) error {
	if len(workflows) == 0 {
		return nil
	}
	// run-name is workflow-level, so each job's SingleWorkflow has the same run-name.
	runName, err := workflows[0].EvaluateRunName()
	if err != nil {
		return fmt.Errorf("unable to evaluate workflow `run-name`: %w", err)
	}
	if runName != "" {
		run.Title = runName
	}
	return nil
}

// Sets the ConcurrencyGroup & ConcurrencyType on the provided ActionRun based upon the Workflow's `concurrency` data,
// or appropriate defaults if not present.
func ConfigureActionRunConcurrency(workflow *act_model.Workflow, run *actions_model.ActionRun, vars map[string]string, inputs map[string]any) error {
	concurrencyGroup, cancelInProgress, err := jobparser.EvaluateWorkflowConcurrency(
		workflow.RawConcurrency, generateGiteaContextForRun(run), vars, inputs,
	)
	if err != nil {
		return fmt.Errorf("unable to evaluate workflow `concurrency` block: %w", err)
	}
	if concurrencyGroup != "" {
		run.SetConcurrencyGroup(concurrencyGroup)
	} else {
		run.SetDefaultConcurrencyGroup()
	}
	if cancelInProgress == nil {
		// Maintain compatible behavior from before concurrency groups were implemented -- if `cancel-in-progress`
		// isn't defined in the workflow, cancel on push & PR sync events.
		if run.Event == webhook.HookEventPush || run.Event == webhook.HookEventPullRequestSync {
			run.ConcurrencyType = actions_model.CancelInProgress
		} else {
			run.ConcurrencyType = actions_model.UnlimitedConcurrency
		}
	} else if *cancelInProgress {
		run.ConcurrencyType = actions_model.CancelInProgress
	} else if concurrencyGroup == "" {
		// A workflow has explicitly listed `cancel-in-progress: false`, but has *not* provided a concurrency group.  In
		// this case we want to trigger a different concurrency behavior -- we won't cancel in-progress builds (we were
		// asked not to), we won't queue behind other builds (we weren't given a concurrency group so it's reasonable to
		// assume the user doesn't want a concurrency limit).
		run.ConcurrencyType = actions_model.UnlimitedConcurrency
	} else {
		run.ConcurrencyType = actions_model.QueueBehind
	}
	return nil
}
