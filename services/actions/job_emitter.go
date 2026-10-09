// Copyright 2022 The Gitea Authors. All rights reserved.
// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT AND GPL-3.0-or-later

package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/db"
	"forgejo.org/modules/container"
	"forgejo.org/modules/graceful"
	"forgejo.org/modules/json"
	"forgejo.org/modules/log"
	"forgejo.org/modules/queue"
	"forgejo.org/modules/structs"
	"forgejo.org/modules/util"

	"code.forgejo.org/forgejo/runner/v13/act/jobparser"
	"xorm.io/builder"
)

var (
	logger          = log.GetManager().GetLogger(log.DEFAULT)
	jobEmitterQueue *queue.WorkerPoolQueue[*jobUpdate]
)

type jobUpdate struct {
	RunID int64
}

func EmitJobsIfReady(runID int64) error {
	err := jobEmitterQueue.Push(&jobUpdate{
		RunID: runID,
	})
	if errors.Is(err, queue.ErrAlreadyInQueue) {
		return nil
	}
	return err
}

func jobEmitterQueueHandler(items ...*jobUpdate) []*jobUpdate {
	ctx := graceful.GetManager().ShutdownContext()
	var ret []*jobUpdate
	for _, update := range items {
		if err := checkJobsOfRun(ctx, update.RunID, 0); err != nil {
			logger.Error("checkJobsOfRun failed for RunID = %d: %v", update.RunID, err)
			ret = append(ret, update)
		}

		if err := RefreshAndPropagateRunStatus(ctx, update.RunID); err != nil {
			logger.Error("RefreshAndPropagateRunStatus failed for run %d: %v", update.RunID, err)
		}
	}
	return ret
}

func checkJobsOfRun(ctx context.Context, runID int64, recursionCount int) error {
	// Recursion happens if one job finishing causes another job to be evaluated so that it creates new jobs (eg.
	// dynamic matrix), those new jobs need to have their 'needs' re-evaluated. Safety check here against infinite
	// recursion -- no clear reason this should happen more than once in a check since after one recurse there aren't
	// any actual new jobs completed, but better safe than sorry.
	if recursionCount > 5 {
		return fmt.Errorf("checkJobsOfRun for runID %d hit recursion limit %d", runID, recursionCount)
	}

	var jobs actions_model.ActionJobList
	jobs, err := db.Find[actions_model.ActionRunJob](ctx, actions_model.FindRunJobOptions{RunID: runID})
	if err != nil {
		return err
	}

	var updates map[int64]actions_model.Status
	if err := db.WithTx(ctx, func(ctx context.Context) error {
		updates = newJobStatusResolver(jobs).Resolve()
		for _, job := range jobs {
			if status, ok := updates[job.ID]; ok {
				// Capture the current status of job which is required for emitting notifications.
				priorStatus := job.Status

				job.Status = status
				updateColumns := []string{"status"}

				if status.IsWaiting() {
					behaviour, err := prepareJobForEmitting(ctx, job, jobs)
					switch behaviour {
					case behaviourError:
						return fmt.Errorf("error in prepareJobForEmitting: %w", err)

					case behaviourExecuteJob:
						// Intentional blank case -- proceed with updating the status of the job to waiting.
						break

					case behaviourIgnoreJob:
						// Skip updating this job's status to waiting, continue with other jobs in the run.
						continue

					case behaviourIgnoreAllJobsInRun:
						// Stop processing any other jobs in this run.
						return nil

					case behaviorSkipJob:
						// "status" is already a column that we're updating, so we can just switch the target status to skipped.
						job.Status = actions_model.StatusSkipped
					}
				} else if status.IsSuccess() || status.IsFailure() || status.IsSkipped() {
					// Transition to these states can be triggered by workflow call outer jobs
					additionalColumns, err := tryHandleWorkflowCallOuterJob(ctx, job)
					if err != nil {
						return fmt.Errorf("error in tryHandleWorkflowCallOuterJob: %w", err)
					}
					updateColumns = append(updateColumns, additionalColumns...)
				}

				if n, err := actions_model.UpdateRunJobWithoutNotification(ctx, job, builder.Eq{"status": actions_model.StatusBlocked}, updateColumns...); err != nil {
					return err
				} else if n != 1 {
					return fmt.Errorf("no affected for updating blocked job %v", job.ID)
				}

				if err = PropagateJobStatus(ctx, job.ID, priorStatus); err != nil {
					return fmt.Errorf("could not propagate the changed status of job %d: %w", job.ID, err)
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}

	CreateCommitStatus(ctx, jobs...)

	// prepareJobForEmitting can create new jobs in this run which may initially be persisted in the DB as blocked
	// because they have non-empty `needs`. In that case, we need to recursively run the job emitter so that new jobs
	// are recognized as having their `needs` completed and be set as unblocked. Check if any new jobs were created and
	// rerun the job emitter if so. The same is necessary if updates completed jobs that unblocked other jobs.
	if hasNewJobs, err := actions_model.RunHasOtherJobs(ctx, runID, jobs); err != nil {
		return fmt.Errorf("RunHasOtherJobs error: %w", err)
	} else if hasNewJobs || len(updates) > 0 {
		return checkJobsOfRun(ctx, runID, recursionCount+1)
	}

	return nil
}

type jobStatusResolver struct {
	statuses map[int64]actions_model.Status
	needs    map[int64][]int64
	jobMap   map[int64]*actions_model.ActionRunJob
}

// unknownJobID stores the ID of an unknown job that might be referenced in the workflow. The ID can be any number as
// long it does not match the ID of an existing job.
var unknownJobID int64 = -1

func newJobStatusResolver(jobs actions_model.ActionJobList) *jobStatusResolver {
	idToJobs := make(map[actions_model.NamespacedJobIdentifier][]*actions_model.ActionRunJob, len(jobs))
	jobMap := make(map[int64]*actions_model.ActionRunJob)
	for _, job := range jobs {
		idToJobs[job.NamespacedJobID()] = append(idToJobs[job.NamespacedJobID()], job)
		jobMap[job.ID] = job
	}

	statuses := make(map[int64]actions_model.Status, len(jobs))
	needs := make(map[int64][]int64, len(jobs))
	for _, job := range jobs {
		statuses[job.ID] = job.Status
		for _, need := range job.NamespacedNeeds() {
			neededJobs, ok := idToJobs[need]
			if ok {
				for _, v := range neededJobs {
					needs[job.ID] = append(needs[job.ID], v.ID)
				}
			} else {
				// Handles the case of an unknown job being referenced in `needs`, for example, `needs: ["unknown"]`.
				needs[job.ID] = append(needs[job.ID], unknownJobID)
			}
		}
	}
	return &jobStatusResolver{
		statuses: statuses,
		needs:    needs,
		jobMap:   jobMap,
	}
}

func (r *jobStatusResolver) Resolve() map[int64]actions_model.Status {
	ret := map[int64]actions_model.Status{}
	for id, status := range r.statuses {
		if status != actions_model.StatusBlocked {
			continue
		}
		allDone, allSucceedOrSkip, allSkip := true, true, true
		for _, need := range r.needs[id] {
			needStatus := r.statuses[need]
			if !needStatus.IsDone() {
				allDone = false
			}
			if needStatus.In(actions_model.StatusFailure, actions_model.StatusCancelled) {
				allSucceedOrSkip = false
			}
			if !needStatus.IsSkipped() {
				allSkip = false
			}
		}
		if allDone {
			if isWorkflowCallOuterJob, _ := r.jobMap[id].IsWorkflowCallOuterJob(); isWorkflowCallOuterJob {
				// If the dependent job was a workflow call outer job, then options aren't waiting/skipped, but rather
				// success/skip/failure.  checkJobsOfRun will do additional work in these cases to "finish" the workflow
				// call job as well.
				if allSucceedOrSkip {
					isIncompleteMatrix, _, _ := r.jobMap[id].HasIncompleteMatrix()
					isIncompleteWith, _, _, _ := r.jobMap[id].HasIncompleteWith()
					if isIncompleteMatrix || isIncompleteWith {
						// The `needs` of this job are done.  For an outer workflow call, that usually means that the
						// inner jobs are done.  But if the job is incomplete, that means that the `needs` that were
						// required to define the job are done, and now the job can be expanded with the missing values
						// that come from `${{ needs... }}`.  By putting this job into `Waiting` state, it will go into
						// `prepareJobForEmitting` to be reparsed, replaced with a full job definition, with new `needs`
						// that contain its inner jobs:
						ret[id] = actions_model.StatusWaiting
					} else if allSkip {
						// All of the inner jobs are skipped -- this most likely occurs because an outer job's `if:`
						// condition was false, and that condition was populated to all the inner jobs.  Even if that's
						// not the case, it's effectively true that the reusable workflow was skipped if all the inner
						// jobs had their own `if:` conditions that were skipped.
						ret[id] = actions_model.StatusSkipped
					} else {
						// This job is done by virtue of its inner jobs being done successfully.
						ret[id] = actions_model.StatusSuccess
					}
				} else {
					ret[id] = actions_model.StatusFailure
				}
			} else {
				// All the `needs` of this job are done.  We could now go to waiting or skipped, dependent on the `if`
				// condition of the new job.  Don't evaluate that here -- set it to waiting, and `prepareJobForEmitting`
				// will perform a full evaluation of the if clause to determine if it should be skipped.
				ret[id] = actions_model.StatusWaiting
			}
		}
	}
	return ret
}

type behaviour int

const (
	// behaviourError is used to indicate that there's no relevant behaviour due to an internal server error.
	behaviourError behaviour = iota

	// behaviourExecuteJob indicates that the job is ready to be unblocked as normal.
	behaviourExecuteJob

	// behaviourIgnoreJob indicates that the job should not be unblocked, and should instead be ignored.
	behaviourIgnoreJob

	// behaviourIgnoreAllJobsInRun indicates that something went wrong and all jobs in the run should now be ignored.
	behaviourIgnoreAllJobsInRun

	// behaviorSkipJob indicates that the job should be marked as skipped as the 'if' evaluation returned false.
	behaviorSkipJob
)

// Invoked once a job has all its `needs` parameters met and is ready to transition to waiting. May expand the job's
// `strategy.matrix` into multiple new jobs, may compute a calculated `runs-on` field, may expand reusable workflows,
// and may evaluate `if` to see if the job should be skipped -- all things that could change based upon `${{ needs...
// }}` outputs and results, which are now available.
func prepareJobForEmitting(ctx context.Context, blockedJob *actions_model.ActionRunJob, jobsInRun []*actions_model.ActionRunJob) (behaviour, error) {
	incompleteMatrix, _, err := blockedJob.HasIncompleteMatrix()
	if err != nil {
		return behaviourError, fmt.Errorf("job HasIncompleteMatrix: %w", err)
	}

	incompleteRunsOn, _, _, err := blockedJob.HasIncompleteRunsOn()
	if err != nil {
		return behaviourError, fmt.Errorf("job HasIncompleteRunsOn: %w", err)
	}

	incompleteWith, _, _, err := blockedJob.HasIncompleteWith()
	if err != nil {
		return behaviourError, fmt.Errorf("job HasIncompleteWith: %w", err)
	}

	if err := blockedJob.LoadRun(ctx); err != nil {
		return behaviourError, fmt.Errorf("failure LoadRun in prepareJobForEmitting: %w", err)
	}

	// Compute jobOutputs for all the other jobs required as needed by this job:
	jobNeeds := []string{}
	jobOutputs := make(map[string]map[string]string, len(jobsInRun))
	jobResults := make(map[string]string, len(jobsInRun))
	blockedNamespacedNeeds := container.SetOf(blockedJob.NamespacedNeeds()...)
	for _, job := range jobsInRun {
		namespacedJobID := job.NamespacedJobID()
		if !blockedNamespacedNeeds.Contains(namespacedJobID) {
			// Only include jobs that are in the `needs` of the blocked job.  This filtering includes the namespace so
			// that we never look at the outputs or results of a similarly named job in a different namespace.
			continue
		} else if !job.Status.IsDone() {
			// Unexpected: `job` is needed by `blockedJob` but it isn't done; `jobStatusResolver` shouldn't be calling
			// `prepareJobForEmitting` in this case.
			return behaviourError, fmt.Errorf(
				"jobStatusResolver attempted to prepareJobForEmitting for a job (id=%d) with an incomplete 'needs' job (id=%d)", blockedJob.ID, job.ID,
			)
		}

		// Jobs in a different namespace may be part of `needs` because of runtime order dependencies; for example, if a
		// pre-req job runs before a reusable workflow, the reusable workflow's jobs will require the pre-req.  These
		// job identifiers need to be converted to the local namespace of this job for needed jobs prerequisites to be
		// considered complete, and for their statuses and outputs to be accessible.
		localJobID := string(namespacedJobID.ToLocal(blockedJob.JobNamespace))

		outputs, err := actions_model.FindTaskOutputByTaskID(ctx, job.TaskID)
		if err != nil {
			return behaviourError, fmt.Errorf("failed loading task outputs: %w", err)
		}
		outputsMap := make(map[string]string, len(outputs))
		for _, v := range outputs {
			outputsMap[v.OutputKey] = v.OutputValue
		}
		jobOutputs[localJobID] = outputsMap
		jobResults[localJobID] = job.Status.String()
		jobNeeds = append(jobNeeds, localJobID)
	}

	vars, err := actions_model.GetVariablesOfRun(ctx, blockedJob.Run)
	if err != nil {
		return behaviourError, fmt.Errorf("failure GetVariablesOfRun in prepareJobForEmitting: %w", err)
	}

	// Re-parse the blocked job, providing all the other completed jobs' outputs, to turn this incomplete job into
	// one-or-more new jobs:
	expandLocalReusableWorkflow, expandCleanup := lazyRepoExpandLocalReusableWorkflow(ctx, blockedJob.RepoID, blockedJob.Run.GetWorkflowSourceCommit())
	defer expandCleanup()
	newJobWorkflows, err := jobparser.Parse(blockedJob.WorkflowPayload, false,
		jobparser.WithJobOutputs(jobOutputs),
		jobparser.WithJobResults(jobResults),
		jobparser.WithWorkflowNeeds(jobNeeds),
		jobparser.SupportIncompleteRunsOn(),
		jobparser.ExpandLocalReusableWorkflows(expandLocalReusableWorkflow),
		jobparser.ExpandInstanceReusableWorkflows(expandInstanceReusableWorkflows(ctx)),
		jobparser.WithVars(vars),
		jobparser.WithInputs(getRunInputs(blockedJob.Run)),
		jobparser.WithGitContext(generateGiteaContextForRun(blockedJob.Run)),
		jobparser.EnableNamespaces(),
	)
	if err != nil {
		// Reparsing errors are quite rare here since we were already able to parse this workflow in the past to
		// generate `blockedJob`, but it would be possible with a remote reusable workflow if the reference disappears
		// from the remote repo -- eg. it was `@v1` and the `v1` tag was removed.
		if err := FailRunPreExecutionError(
			ctx,
			blockedJob.Run,
			actions_model.ErrorCodeJobParsingError,
			[]any{err.Error()},
		); err != nil {
			return behaviourError, fmt.Errorf("setting run into PreExecutionError state failed: %w", err)
		}
		// `FailRunPreExecutionError` will mark all the pending runs in the job failed; ignore all of them.
		return behaviourIgnoreAllJobsInRun, nil
	}

	if !incompleteMatrix && !incompleteRunsOn && !incompleteWith {
		// We did not need to reparse this job in order to expand it into new workflows.  But, as we've provided all the
		// necessary parsing context to the job, we can use this opportunity to perform an evaluation of the job's 'if'
		// clause within the server, and we might be able to skip this job.

		if len(newJobWorkflows) != 1 {
			// This case shouldn't happen, but we'll ignore our attempt to evaluate the if block:
			return behaviourExecuteJob, nil
		}

		swf := newJobWorkflows[0]
		_, job := swf.Job()

		ifPassed, err := job.EvaluateIf()
		if err != nil {
			// Fallback to sending the job to a runner.  There are two classes of possible error:
			// ErrCannotEvaluateInJobParser, in which we know we can't evaluate if server-side and must dispatch to the
			// runner, and, all other errors which occur because the user made a mistake in their workflow.  As we don't
			// have a convenient way to report the second class of errors to the user, we just dispatch all of them to a
			// runner.
			log.Trace("server-side evaluation of 'if' failed with an error; the job can be picked up by a runner: %s", err.Error())
			return behaviourExecuteJob, nil
		} else if !ifPassed {
			return behaviorSkipJob, nil
		}
		return behaviourExecuteJob, nil
	}

	// Even though every job in the `needs` list is done, perform a consistency check if the job was still unable to be
	// evaluated into a fully complete job with the correct matrix and runs-on values. Evaluation errors here need to be
	// reported back to the user for them to correct their workflow, so we slip this notification into
	// PreExecutionError.
	for _, swf := range newJobWorkflows {
		// If the re-evaluated job has the same job ID as the input job, and it's still incomplete, then we'll consider
		// it to be a "persistent incomplete" job with some error that needs to be reported to the user.  If the
		// re-evaluated job has a different job ID, then it's likely an expanded job -- such as from a reusable workflow
		// -- which could have it's own `needs` that allows it to expand into a correct job in the future.
		jobID, job := swf.Job()
		if actions_model.JobIdentifier(jobID) == blockedJob.JobID {
			if swf.IncompleteMatrix {
				if cascadeSkip(swf.IncompleteMatrixNeeds, jobResults) {
					// This job has an incomplete matrix.  It is incomplete because it has `${{ needs.x... }}` where x
					// is a skipped job.  Skip this job as well.
					//
					// Theoretically this job could have `if: always()` and expect to be executed... but that's
					// contradictory with having a matrix that relies on a needed job that was skipped.
					return behaviorSkipJob, nil
				}
				errorCode, errorDetails := persistentIncompleteMatrixError(blockedJob, swf.IncompleteMatrixNeeds)
				if err := FailRunPreExecutionError(ctx, blockedJob.Run, errorCode, errorDetails); err != nil {
					return behaviourError, fmt.Errorf("setting run into PreExecutionError state failed: %w", err)
				}
				// `FailRunPreExecutionError` will mark all the pending runs in the job failed; ignore all of them.
				return behaviourIgnoreAllJobsInRun, nil
			} else if swf.IncompleteRunsOn {
				if cascadeSkip(swf.IncompleteRunsOnNeeds, jobResults) {
					// This job has an incomplete runs-on.  It is incomplete because it has `${{ needs.x... }}` where x
					// is a skipped job.  Skip this job as well.
					return behaviorSkipJob, nil
				}
				errorCode, errorDetails := persistentIncompleteRunsOnError(blockedJob, swf.IncompleteRunsOnNeeds, swf.IncompleteRunsOnMatrix)
				if err := FailRunPreExecutionError(ctx, blockedJob.Run, errorCode, errorDetails); err != nil {
					return behaviourError, fmt.Errorf("setting run into PreExecutionError state failed: %w", err)
				}
				// `FailRunPreExecutionError` will mark all the pending runs in the job failed; ignore all of them.
				return behaviourIgnoreAllJobsInRun, nil
			} else if swf.IncompleteWith {
				if cascadeSkip(swf.IncompleteWithNeeds, jobResults) {
					// This job has an incomplete with.  It is incomplete because it has `${{ needs.x... }}` where x is
					// a skipped job.  Skip this job as well.
					return behaviorSkipJob, nil
				}
				errorCode, errorDetails := persistentIncompleteWithError(blockedJob, swf.IncompleteWithNeeds, swf.IncompleteWithMatrix)
				if err := FailRunPreExecutionError(ctx, blockedJob.Run, errorCode, errorDetails); err != nil {
					return behaviourError, fmt.Errorf("setting run into PreExecutionError state failed: %w", err)
				}
				// `FailRunPreExecutionError` will mark all the pending runs in the job failed; ignore all of them.
				return behaviourIgnoreAllJobsInRun, nil
			}
		}

		// Original job had a `needs: ...blockedJob.Needs...`.  Even though we've now expanded that job, which would
		// evaluate any ${{ needs.... }} reference that is required for expansion, this job could still have other
		// reasons to require acccess to those needs variables.  We need to reinsert those `needs` into the new job so
		// that those job's outputs and results are made available to this new job.
		newNeeds := job.Needs()
		for _, n := range blockedJob.NamespacedNeeds() {
			// Needs from the blocked job should be expanded to their namespace qualified names, if they're in a
			// different namespace than this job.
			newNeeds = append(newNeeds, string(n.ToLocal(actions_model.JobNamespace(swf.Metadata.Namespace))))
		}

		err := job.RawNeeds.Encode(newNeeds)
		if err != nil {
			return behaviourError, fmt.Errorf("failure to encode newNeeds: %w", err)
		}
		err = swf.SetJob(jobID, job)
		if err != nil {
			return behaviourError, fmt.Errorf("failure to reencode updated job: %w", err)
		}
	}

	err = db.WithTx(ctx, func(ctx context.Context) error {
		jobs, err := convertSingleWorkflowToJobs(blockedJob.Run, newJobWorkflows)
		if err != nil {
			return fmt.Errorf("failed to convert parsed workflows to jobs of run %d: %w", blockedJob.RunID, err)
		}
		if err := actions_model.InsertRunJobs(ctx, blockedJob.Run, jobs); err != nil {
			return fmt.Errorf("failure in InsertRunJobs: %w", err)
		}

		// Send notifications for the newly created jobs. But do not send notifications for
		// blockedJob, because it is a placeholder and all previous notification were suppressed.
		for _, job := range jobs {
			if err := PropagateNextJobAttempt(ctx, job.ID); err != nil {
				return fmt.Errorf("failed to propagate new attempt of job %d: %w", job.ID, err)
			}
		}

		// Delete the blocked job which has been expanded into `newJobWorkflows`.
		count, err := db.DeleteByID[actions_model.ActionRunJob](ctx, blockedJob.ID)
		if err != nil {
			return err
		} else if count != 1 {
			return fmt.Errorf("unexpected record count in delete incomplete_matrix=true job with ID %d; count = %d", blockedJob.ID, count)
		}

		// After manipulating jobs, update the status of the run to prevent it from being out of sync.
		if err := RefreshAndPropagateRunStatus(ctx, blockedJob.RunID); err != nil {
			return fmt.Errorf("could not refresh and propagate the status of run %d: %w", blockedJob.RunID, err)
		}

		return nil
	})
	if err != nil {
		return behaviourError, err
	}
	// job was deleted after it was replaced with one-or-more new jobs, so ignore it.
	return behaviourIgnoreJob, nil
}

func cascadeSkip(incompleteNeeds *jobparser.IncompleteNeeds, jobResults map[string]string) bool {
	if incompleteNeeds == nil {
		return false
	}
	requiredJob := incompleteNeeds.Job
	requiredJobResult, present := jobResults[requiredJob]
	if !present {
		// Can't find the status of the needed job.
		return false
	}
	if requiredJobResult != actions_model.StatusSkipped.String() {
		// The needed job wasn't skipped, so don't cascade the skip to the dependent job.
		return false
	}
	return true
}

func persistentIncompleteMatrixError(job *actions_model.ActionRunJob, incompleteNeeds *jobparser.IncompleteNeeds) (actions_model.PreExecutionError, []any) {
	var errorCode actions_model.PreExecutionError
	var errorDetails []any

	// `incompleteNeeds` tells us what part of a `${{ needs... }}` expression was missing
	if incompleteNeeds != nil {
		jobRef := incompleteNeeds.Job       // always provided
		outputRef := incompleteNeeds.Output // missing if the entire job wasn't present
		if outputRef != "" {
			errorCode = actions_model.ErrorCodeIncompleteMatrixMissingOutput
			errorDetails = []any{
				job.JobID,
				jobRef,
				outputRef,
			}
		} else {
			errorCode = actions_model.ErrorCodeIncompleteMatrixMissingJob
			errorDetails = []any{
				job.JobID,
				jobRef,
				strings.Join(util.ConvertSlice[actions_model.LocalJobIdentifier, string](job.Needs), ", "),
			}
		}
		return errorCode, errorDetails
	}

	// Not sure why we ended up in `IncompleteMatrix` when nothing was marked as incomplete
	errorCode = actions_model.ErrorCodeIncompleteMatrixUnknownCause
	errorDetails = []any{job.JobID}
	return errorCode, errorDetails
}

//nolint:dupl
func persistentIncompleteRunsOnError(job *actions_model.ActionRunJob, incompleteNeeds *jobparser.IncompleteNeeds, incompleteMatrix *jobparser.IncompleteMatrix) (actions_model.PreExecutionError, []any) {
	var errorCode actions_model.PreExecutionError
	var errorDetails []any

	// `incompleteMatrix` tells us which dimension of a matrix was accessed that was missing
	if incompleteMatrix != nil {
		dimension := incompleteMatrix.Dimension
		errorCode = actions_model.ErrorCodeIncompleteRunsOnMissingMatrixDimension
		errorDetails = []any{
			job.JobID,
			dimension,
		}
		return errorCode, errorDetails
	}

	// `incompleteNeeds` tells us what part of a `${{ needs... }}` expression was missing
	if incompleteNeeds != nil {
		jobRef := incompleteNeeds.Job       // always provided
		outputRef := incompleteNeeds.Output // missing if the entire job wasn't present
		if outputRef != "" {
			errorCode = actions_model.ErrorCodeIncompleteRunsOnMissingOutput
			errorDetails = []any{
				job.JobID,
				jobRef,
				outputRef,
			}
		} else {
			errorCode = actions_model.ErrorCodeIncompleteRunsOnMissingJob
			errorDetails = []any{
				job.JobID,
				jobRef,
				strings.Join(util.ConvertSlice[actions_model.LocalJobIdentifier, string](job.Needs), ", "),
			}
		}
		return errorCode, errorDetails
	}

	// Not sure why we ended up in `IncompleteRunsOn` when nothing was marked as incomplete
	errorCode = actions_model.ErrorCodeIncompleteRunsOnUnknownCause
	errorDetails = []any{job.JobID}
	return errorCode, errorDetails
}

//nolint:dupl
func persistentIncompleteWithError(job *actions_model.ActionRunJob, incompleteNeeds *jobparser.IncompleteNeeds, incompleteMatrix *jobparser.IncompleteMatrix) (actions_model.PreExecutionError, []any) {
	var errorCode actions_model.PreExecutionError
	var errorDetails []any

	// `incompleteMatrix` tells us which dimension of a matrix was accessed that was missing
	if incompleteMatrix != nil {
		dimension := incompleteMatrix.Dimension
		errorCode = actions_model.ErrorCodeIncompleteWithMissingMatrixDimension
		errorDetails = []any{
			job.JobID,
			dimension,
		}
		return errorCode, errorDetails
	}

	// `incompleteNeeds` tells us what part of a `${{ needs... }}` expression was missing
	if incompleteNeeds != nil {
		jobRef := incompleteNeeds.Job       // always provided
		outputRef := incompleteNeeds.Output // missing if the entire job wasn't present
		if outputRef != "" {
			errorCode = actions_model.ErrorCodeIncompleteWithMissingOutput
			errorDetails = []any{
				job.JobID,
				jobRef,
				outputRef,
			}
		} else {
			errorCode = actions_model.ErrorCodeIncompleteWithMissingJob
			errorDetails = []any{
				job.JobID,
				jobRef,
				strings.Join(util.ConvertSlice[actions_model.LocalJobIdentifier, string](job.Needs), ", "),
			}
		}
		return errorCode, errorDetails
	}

	// Not sure why we ended up in `IncompleteWith` when nothing was marked as incomplete
	errorCode = actions_model.ErrorCodeIncompleteWithUnknownCause
	errorDetails = []any{job.JobID}
	return errorCode, errorDetails
}

// When a workflow call outer job's dependencies are completed, `tryHandleWorkflowCallOuterJob` will complete the job
// without actually executing it. It will not be dispatched it to a runner. There's no job execution logic, but we need
// to update state of a few things -- particularly workflow outputs.
//
// A slice of additional columns for the caller to update on the passed-in `ActionRunJob` is returned, in addition to an
// error.
func tryHandleWorkflowCallOuterJob(ctx context.Context, job *actions_model.ActionRunJob) ([]string, error) {
	isWorkflowCallOuterJob, err := job.IsWorkflowCallOuterJob()
	if err != nil {
		return nil, fmt.Errorf("failure to identify workflow call outer job: %w", err)
	} else if !isWorkflowCallOuterJob {
		// Not an expected code path today, but if job status resolution changes in the future we might "try" to handle
		// a workflow call outer job while it's not really in that state.  No work required.
		return nil, nil
	}

	// Gather all the data that is needed to perform an expression evaluation of the job's outputs:
	singleWorkflow, err := job.DecodeWorkflowPayload()
	if err != nil {
		return nil, fmt.Errorf("failure to decode workflow payload: %w", err)
	}
	err = job.LoadRun(ctx)
	if err != nil {
		return nil, fmt.Errorf("failure to load job's run: %w", err)
	}
	err = job.Run.LoadRepo(ctx)
	if err != nil {
		return nil, fmt.Errorf("failure to load run's repo: %w", err)
	}
	githubContext := generateGiteaContextForRun(job.Run)
	taskNeeds, err := FindTaskNeeds(ctx, job)
	if err != nil {
		return nil, fmt.Errorf("failure to 'needs' for job: %w", err)
	}
	needs := make([]string, 0, len(taskNeeds))
	jobResults := make(map[string]string, len(taskNeeds))
	jobOutputs := make(map[string]map[string]string, len(taskNeeds))
	for jobID, n := range taskNeeds {
		qualified := string(jobID.ToLocal(job.JobNamespace))
		needs = append(needs, qualified)
		jobResults[qualified] = n.Result.String()
		jobOutputs[qualified] = n.Outputs
	}
	vars, err := actions_model.GetVariablesOfRun(ctx, job.Run)
	if err != nil {
		return nil, fmt.Errorf("failure to 'var' for run: %w", err)
	}

	// With all the required contexts, we can calculate the outputs.
	outputs := jobparser.EvaluateWorkflowCallOutputs(
		singleWorkflow,
		githubContext,
		vars,
		needs,
		jobResults,
		jobOutputs,
	)

	// Insert a placeholder task with all the computed outputs
	actionTask, err := actions_model.CreatePlaceholderTask(ctx, job, outputs)
	if err != nil {
		return nil, fmt.Errorf("failure to insert placeholder task: %w", err)
	}

	// Populate task_id and ask caller to update it in DB.
	// Update previously incremented attempt field as well.
	job.TaskID = actionTask.ID
	return []string{"task_id", "attempt"}, nil
}

func getRunInputs(run *actions_model.ActionRun) map[string]any {
	// workflow_dispatch inputs are stored in the event payload
	var dispatchPayload *structs.WorkflowDispatchPayload
	err := json.Unmarshal([]byte(run.EventPayload), &dispatchPayload)
	if err != nil {
		return nil
	}
	// transition from map[string]string to map[string]any...
	inputs := make(map[string]any, len(dispatchPayload.Inputs))
	for k, v := range dispatchPayload.Inputs {
		inputs[k] = v
	}
	return inputs
}
