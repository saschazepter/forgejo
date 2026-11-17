package actions

import (
	"fmt"
	"testing"
	"time"

	actions_model "forgejo.org/models/actions"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	"forgejo.org/models/user"
	"forgejo.org/modules/actions"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/modules/timeutil"
	notify_service "forgejo.org/services/notify"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGenerateTaskContext(t *testing.T) {
	workflowFormat := `
name: Pull Request
on: pull_request
enable-openid-connect: %s
jobs:
  wf1-job:
    runs-on: ubuntu-latest
    steps:
      - run: echo 'test the pull'
`
	testUser := &user.User{
		ID:   1,
		Name: "testuser",
	}

	testRepo := &repo_model.Repository{
		ID:        1,
		OwnerName: "testowner",
		Name:      "testrepo",
	}

	createTask := func(workflowPayload string, isFork bool, triggerEvent string) *actions_model.ActionTask {
		return &actions_model.ActionTask{
			ID: 47,
			Job: &actions_model.ActionRunJob{
				ID:    2,
				RunID: 1,
				Run: &actions_model.ActionRun{
					ID:                1,
					Index:             42,
					TriggerUser:       testUser,
					Repo:              testRepo,
					TriggerEvent:      triggerEvent,
					Ref:               "refs/heads/main",
					CommitSHA:         "abc123def456",
					WorkflowID:        "test-workflow.yaml",
					WorkflowDirectory: ".forgejo/workflows",
					EventPayload:      `{"repository": {"name": "testrepo"}}`,
					IsForkPullRequest: isFork,
				},
				WorkflowPayload: []byte(workflowPayload),
			},
		}
	}

	t.Run("openid connect enabled", func(t *testing.T) {
		task := createTask(fmt.Sprintf(workflowFormat, "true"), false, "push")

		taskContext, err := generateTaskContext(task, &repo_model.ActionsConfig{})
		require.NoError(t, err)
		require.NotEmpty(t, taskContext.Fields["forgejo_actions_id_token_request_token"].GetStringValue())
		require.NotEmpty(t, taskContext.Fields["forgejo_actions_id_token_request_url"].GetStringValue())
		require.NotEmpty(t, taskContext.Fields["gitea_runtime_token"].GetStringValue())
		require.NotEmpty(t, taskContext.Fields["forgejo_runtime_token"].GetStringValue())
	})

	t.Run("openid connect enabled from fork with pull_request_target event", func(t *testing.T) {
		task := createTask(fmt.Sprintf(workflowFormat, "true"), true, "pull_request_target")

		taskContext, err := generateTaskContext(task, &repo_model.ActionsConfig{})
		require.NoError(t, err)
		require.NotEmpty(t, taskContext.Fields["forgejo_actions_id_token_request_token"].GetStringValue())
		require.NotEmpty(t, taskContext.Fields["forgejo_actions_id_token_request_url"].GetStringValue())
		require.NotEmpty(t, taskContext.Fields["gitea_runtime_token"].GetStringValue())
		require.NotEmpty(t, taskContext.Fields["forgejo_runtime_token"].GetStringValue())
	})

	t.Run("openid connect enabled from fork with pull_request event", func(t *testing.T) {
		task := createTask(fmt.Sprintf(workflowFormat, "true"), true, "pull_request")

		taskContext, err := generateTaskContext(task, &repo_model.ActionsConfig{})
		require.NoError(t, err)
		require.Empty(t, taskContext.Fields["forgejo_actions_id_token_request_token"].GetStringValue())
		require.Empty(t, taskContext.Fields["forgejo_actions_id_token_request_url"].GetStringValue())
		require.NotEmpty(t, taskContext.Fields["gitea_runtime_token"].GetStringValue())
		require.NotEmpty(t, taskContext.Fields["forgejo_runtime_token"].GetStringValue())
	})

	t.Run("openid connect disabled", func(t *testing.T) {
		task := createTask(fmt.Sprintf(workflowFormat, "false"), false, "push")

		taskContext, err := generateTaskContext(task, &repo_model.ActionsConfig{})
		require.NoError(t, err)
		require.Empty(t, taskContext.Fields["forgejo_actions_id_token_request_token"].GetStringValue())
		require.Empty(t, taskContext.Fields["forgejo_actions_id_token_request_url"].GetStringValue())
		require.NotEmpty(t, taskContext.Fields["gitea_runtime_token"].GetStringValue())
		require.NotEmpty(t, taskContext.Fields["forgejo_runtime_token"].GetStringValue())
	})

	t.Run("openid connect id token request url", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			appURL    string
			appSubURL string
			expected  string
		}{
			{
				name:      "without sub-path",
				appURL:    "https://forgejo.example.com/",
				appSubURL: "",
				expected:  "https://forgejo.example.com/api/actions/_apis/pipelines/workflows/1/idtoken?placeholder=true",
			},
			{
				name:      "with sub-path",
				appURL:    "https://example.com/sub/path/forgejo/",
				appSubURL: "/sub/path/forgejo",
				expected:  "https://example.com/sub/path/forgejo/api/actions/_apis/pipelines/workflows/1/idtoken?placeholder=true",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				defer test.MockVariableValue(&setting.AppURL, tc.appURL)()
				defer test.MockVariableValue(&setting.AppSubURL, tc.appSubURL)()

				task := createTask(fmt.Sprintf(workflowFormat, "true"), false, "push")

				taskContext, err := generateTaskContext(task, &repo_model.ActionsConfig{})
				require.NoError(t, err)
				assert.Equal(t, tc.expected, taskContext.Fields["forgejo_actions_id_token_request_url"].GetStringValue())
			})
		}
	})
}

func TestDeleteTask(t *testing.T) {
	t.Run("Task removed with logs and ephemeral runner", func(t *testing.T) {
		defer unittest.OverrideFixtures("services/actions/TestDeleteTask")()
		require.NoError(t, unittest.PrepareTestDatabase())

		task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 87601})
		runner := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunner{ID: 41601})
		unittest.AssertCount(t, &actions_model.ActionTaskOutput{TaskID: task.ID}, 2)
		unittest.AssertCount(t, &actions_model.ActionTaskStep{TaskID: task.ID}, 1)
		unittest.AssertCount(t, &actions_model.ActionTaskStepSummary{TaskID: task.ID}, 1)

		_, err := actions.WriteLogs(t.Context(), task.LogFilename, 0, []*runnerv1.LogRow{{Content: "OK"}})
		require.NoError(t, err)

		logExists, err := actions.ExistsLogs(t.Context(), task.LogFilename)
		require.NoError(t, err)
		assert.True(t, logExists)

		require.NoError(t, deleteTask(t.Context(), task.ID))

		logExists, err = actions.ExistsLogs(t.Context(), task.LogFilename)
		require.NoError(t, err)
		assert.False(t, logExists)

		unittest.AssertNotExistsBean(t, &actions_model.ActionTask{ID: task.ID})
		unittest.AssertCount(t, &actions_model.ActionTaskOutput{TaskID: task.ID}, 0)
		unittest.AssertCount(t, &actions_model.ActionTaskStep{TaskID: task.ID}, 0)
		unittest.AssertCount(t, &actions_model.ActionTaskStepSummary{TaskID: task.ID}, 0)
		unittest.AssertNotExistsBean(t, &actions_model.ActionRunner{ID: runner.ID})

		// Verify that other tasks have been left alone.
		otherTask := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 87602})
		unittest.AssertCount(t, &actions_model.ActionTaskOutput{TaskID: otherTask.ID}, 1)
		unittest.AssertCount(t, &actions_model.ActionTaskStep{TaskID: otherTask.ID}, 1)
	})

	t.Run("Task removed and persistent runner kept", func(t *testing.T) {
		defer unittest.OverrideFixtures("services/actions/TestDeleteTask")()
		require.NoError(t, unittest.PrepareTestDatabase())

		task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 87603})
		runner := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunner{ID: 41602})
		unittest.AssertCount(t, &actions_model.ActionTaskOutput{TaskID: task.ID}, 0)
		unittest.AssertCount(t, &actions_model.ActionTaskStep{TaskID: task.ID}, 1)

		_, err := actions.WriteLogs(t.Context(), task.LogFilename, 0, []*runnerv1.LogRow{{Content: "OK"}})
		require.NoError(t, err)

		logExists, err := actions.ExistsLogs(t.Context(), task.LogFilename)
		require.NoError(t, err)
		assert.True(t, logExists)

		require.NoError(t, deleteTask(t.Context(), task.ID))

		logExists, err = actions.ExistsLogs(t.Context(), task.LogFilename)
		require.NoError(t, err)
		assert.False(t, logExists)

		unittest.AssertNotExistsBean(t, &actions_model.ActionTask{ID: task.ID})
		unittest.AssertCount(t, &actions_model.ActionTaskOutput{TaskID: task.ID}, 0)
		unittest.AssertCount(t, &actions_model.ActionTaskStep{TaskID: task.ID}, 0)
		unittest.AssertCount(t, &actions_model.ActionTaskStepSummary{TaskID: task.ID}, 0)
		unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunner{ID: runner.ID})
	})

	t.Run("Task without logs removed", func(t *testing.T) {
		defer unittest.OverrideFixtures("services/actions/TestDeleteTask")()
		require.NoError(t, unittest.PrepareTestDatabase())

		task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 87604})
		assert.Empty(t, task.LogFilename)

		require.NoError(t, deleteTask(t.Context(), task.ID))

		unittest.AssertNotExistsBean(t, &actions_model.ActionTask{ID: task.ID})
	})

	t.Run("No error if task does not exist", func(t *testing.T) {
		require.NoError(t, unittest.PrepareTestDatabase())

		unittest.AssertNotExistsBean(t, &actions_model.ActionTask{ID: 87601})
		require.NoError(t, deleteTask(t.Context(), 87601))
	})

	t.Run("Error if task is not done", func(t *testing.T) {
		defer unittest.OverrideFixtures("services/actions/TestDeleteTask")()
		require.NoError(t, unittest.PrepareTestDatabase())

		task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 87602})
		unittest.AssertCount(t, &actions_model.ActionTaskOutput{TaskID: task.ID}, 1)
		unittest.AssertCount(t, &actions_model.ActionTaskStep{TaskID: task.ID}, 1)

		err := deleteTask(t.Context(), task.ID)
		require.ErrorContains(t, err, "unable to remove task 87602 because it has not completed yet")

		// Verify nothing has been deleted.
		unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: task.ID})
		unittest.AssertCount(t, &actions_model.ActionTaskOutput{TaskID: task.ID}, 1)
		unittest.AssertCount(t, &actions_model.ActionTaskStep{TaskID: task.ID}, 1)
	})
}

func TestStopTask(t *testing.T) {
	t.Run("Invalid status", func(t *testing.T) {
		defer unittest.OverrideFixtures("services/actions/TestStopTask")()
		require.NoError(t, unittest.PrepareTestDatabase())

		notifier := notify_service.NewMockNotifier(t)
		notifier.On("Run").Return().Maybe()

		notify_service.RegisterNotifier(notifier)
		defer notify_service.UnregisterNotifier(notifier)

		task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 87601})

		err := StopTask(t.Context(), task.ID, actions_model.StatusRunning)
		require.ErrorContains(t, err, "new task status running is not acceptable")
	})

	t.Run("Completed task", func(t *testing.T) {
		defer unittest.OverrideFixtures("services/actions/TestStopTask")()
		require.NoError(t, unittest.PrepareTestDatabase())

		notifier := notify_service.NewMockNotifier(t)
		notifier.On("Run").Return().Maybe()

		notify_service.RegisterNotifier(notifier)
		defer notify_service.UnregisterNotifier(notifier)

		task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 87601})
		assert.Equal(t, actions_model.StatusFailure, task.Status)

		require.NoError(t, StopTask(t.Context(), task.ID, actions_model.StatusCancelled))

		task = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 87601})
		assert.Equal(t, actions_model.StatusFailure, task.Status)
	})

	t.Run("Task stopped and runner removed", func(t *testing.T) {
		defer unittest.OverrideFixtures("services/actions/TestStopTask")()
		require.NoError(t, unittest.PrepareTestDatabase())

		notifier := notify_service.NewMockNotifier(t)
		notifier.On("Run").Return().Maybe()
		notifier.On("WorkflowJobCompleted", mock.Anything, mock.Anything, mock.Anything).Return()
		notifier.On("WorkflowRunCompleted", mock.Anything, mock.Anything, mock.Anything).Return()

		notify_service.RegisterNotifier(notifier)
		defer notify_service.UnregisterNotifier(notifier)

		task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 87602})
		job := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: task.JobID})
		runner := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunner{ID: task.RunnerID})

		require.NoError(t, StopTask(t.Context(), task.ID, actions_model.StatusCancelled))

		unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: job.RunID, Status: actions_model.StatusCancelled})
		unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: job.ID, Status: actions_model.StatusCancelled})
		unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: task.ID, Status: actions_model.StatusCancelled})
		unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTaskStep{TaskID: task.ID, Status: actions_model.StatusCancelled})
		unittest.AssertNotExistsBean(t, &actions_model.ActionRunner{ID: runner.ID})

		notifier.AssertNumberOfCalls(t, "WorkflowJobCompleted", 1)
		notifier.AssertNumberOfCalls(t, "WorkflowRunCompleted", 1)
		notifier.AssertCalled(
			t, "WorkflowJobCompleted", mock.Anything,
			mock.MatchedBy(func(job *actions_model.ActionRunJob) bool {
				return job.ID == task.JobID && job.Status == actions_model.StatusCancelled
			}),
			actions_model.StatusWaiting,
		)
		notifier.AssertCalled(
			t, "WorkflowRunCompleted", mock.Anything,
			mock.MatchedBy(func(run *actions_model.ActionRun) bool {
				return run.ID == 34902 && run.Status == actions_model.StatusCancelled
			}),
			actions_model.StatusWaiting,
		)
	})

	t.Run("Task stopped and runner retained", func(t *testing.T) {
		defer unittest.OverrideFixtures("services/actions/TestStopTask")()
		require.NoError(t, unittest.PrepareTestDatabase())

		notifier := notify_service.NewMockNotifier(t)
		notifier.On("Run").Return().Maybe()
		notifier.On("WorkflowJobCompleted", mock.Anything, mock.Anything, mock.Anything).Return()
		notifier.On("WorkflowRunCompleted", mock.Anything, mock.Anything, mock.Anything).Return()

		notify_service.RegisterNotifier(notifier)
		defer notify_service.UnregisterNotifier(notifier)

		task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 87603})
		job := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: task.JobID})
		runner := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunner{ID: task.RunnerID})

		require.NoError(t, StopTask(t.Context(), task.ID, actions_model.StatusCancelled))

		unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: job.RunID, Status: actions_model.StatusCancelled})
		unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: job.ID, Status: actions_model.StatusCancelled})
		unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: task.ID, Status: actions_model.StatusCancelled})
		unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTaskStep{TaskID: task.ID, Status: actions_model.StatusCancelled})
		unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunner{ID: runner.ID})

		notifier.AssertNumberOfCalls(t, "WorkflowJobCompleted", 1)
		notifier.AssertNumberOfCalls(t, "WorkflowRunCompleted", 1)
		notifier.AssertCalled(
			t, "WorkflowJobCompleted", mock.Anything,
			mock.MatchedBy(func(job *actions_model.ActionRunJob) bool {
				return job.ID == task.JobID && job.Status == actions_model.StatusCancelled
			}),
			actions_model.StatusRunning,
		)
		notifier.AssertCalled(
			t, "WorkflowRunCompleted", mock.Anything,
			mock.MatchedBy(func(run *actions_model.ActionRun) bool {
				return run.ID == job.RunID && run.Status == actions_model.StatusCancelled
			}),
			actions_model.StatusRunning,
		)
	})
}

func TestCreateTaskForRunner(t *testing.T) {
	t.Run("Triggers notifications", func(t *testing.T) {
		defer unittest.OverrideFixtures("services/actions/TestCreateTaskForRunner")()
		require.NoError(t, unittest.PrepareTestDatabase())

		notifier := notify_service.NewMockNotifier(t)
		notifier.On("Run").Return().Maybe()
		notifier.On("WorkflowJobStatusChanged", mock.Anything, mock.Anything, mock.Anything).Return()
		notifier.On("WorkflowRunStatusChanged", mock.Anything, mock.Anything, mock.Anything).Return()

		notify_service.RegisterNotifier(notifier)
		defer notify_service.UnregisterNotifier(notifier)

		user2 := unittest.AssertExistsAndLoadBean(t, &user.User{ID: 2})
		repo62 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 62, OwnerID: user2.ID})
		runnerOne := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunner{ID: 41601, OwnerID: user2.ID})
		jobOne := unittest.AssertExistsAndLoadBean(t,
			&actions_model.ActionRunJob{ID: 47301, OwnerID: user2.ID, RepoID: repo62.ID})

		requestKey := "9ac1fc24-5fd1-4a75-a85f-727d324dd963"

		task, err := CreateTaskForRunner(t.Context(), runnerOne, &requestKey, nil)
		require.NoError(t, err)

		assert.Equal(t, jobOne.ID, task.JobID)
		assert.Equal(t, runnerOne.ID, task.RunnerID)
		assert.Equal(t, actions_model.StatusRunning, task.Status)
		assert.Equal(t, requestKey, task.RunnerRequestKey)

		notifier.AssertNumberOfCalls(t, "WorkflowJobStatusChanged", 1)
		notifier.AssertNumberOfCalls(t, "WorkflowRunStatusChanged", 1)
		notifier.AssertCalled(
			t, "WorkflowJobStatusChanged", mock.Anything,
			mock.MatchedBy(func(eventJob *actions_model.ActionRunJob) bool {
				return eventJob.ID == jobOne.ID && eventJob.Status == actions_model.StatusRunning
			}),
			actions_model.StatusWaiting,
		)
		notifier.AssertCalled(
			t, "WorkflowRunStatusChanged", mock.Anything,
			mock.MatchedBy(func(eventRun *actions_model.ActionRun) bool {
				return eventRun.ID == jobOne.RunID && eventRun.Status == actions_model.StatusRunning
			}),
			actions_model.StatusWaiting,
		)
	})

	t.Run("Sets started date of run", func(t *testing.T) {
		defer unittest.OverrideFixtures("services/actions/TestCreateTaskForRunner")()
		require.NoError(t, unittest.PrepareTestDatabase())

		fixedDate := time.Date(2026, 9, 20, 15, 38, 23, 0, time.UTC)

		timeutil.MockSet(fixedDate)
		defer timeutil.MockUnset()

		user2 := unittest.AssertExistsAndLoadBean(t, &user.User{ID: 2})
		runnerOne := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunner{ID: 41601, OwnerID: user2.ID})
		runOne := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: 34901})

		assert.Zero(t, runOne.Started)

		_, err := CreateTaskForRunner(t.Context(), runnerOne, new("b44425a1-79a0-46ec-bd1b-d20f9417e86b"), nil)
		require.NoError(t, err)

		runOne = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runOne.ID})
		assert.Equal(t, fixedDate, runOne.Started.AsTime().UTC())
	})

	t.Run("Successive jobs do not override the start date", func(t *testing.T) {
		defer unittest.OverrideFixtures("services/actions/TestCreateTaskForRunner")()
		require.NoError(t, unittest.PrepareTestDatabase())

		user2 := unittest.AssertExistsAndLoadBean(t, &user.User{ID: 2})
		runnerOne := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunner{ID: 41601, OwnerID: user2.ID})
		runOne := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: 34901})

		assert.Zero(t, runOne.Started)

		originalStartDate := timeutil.TimeStampNow() - 7
		runOne.Started = originalStartDate

		require.NoError(t, actions_model.UpdateRun(t.Context(), runOne))

		_, err := CreateTaskForRunner(t.Context(), runnerOne, new("b44425a1-79a0-46ec-bd1b-d20f9417e86b"), nil)
		require.NoError(t, err)

		runOne = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runOne.ID})
		assert.Equal(t, originalStartDate, runOne.Started)
	})
}
