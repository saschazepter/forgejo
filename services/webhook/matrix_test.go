// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package webhook

import (
	"testing"

	actions_model "forgejo.org/models/actions"
	webhook_model "forgejo.org/models/webhook"
	"forgejo.org/modules/json"
	api "forgejo.org/modules/structs"
	webhook_module "forgejo.org/modules/webhook"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatrixPayload(t *testing.T) {
	mc := matrixConvertor{
		MsgType: "m.text",
	}

	t.Run("Create", func(t *testing.T) {
		p := createTestPayload()

		pl, err := mc.Create(p)
		require.NoError(t, err)
		require.NotNil(t, pl)

		assert.Equal(t, "[test/repo:[test](http://localhost:3000/test/repo/src/branch/test)] branch created by user1", pl.Body)
		assert.Equal(t, `[test/repo:<a href="http://localhost:3000/test/repo/src/branch/test">test</a>] branch created by user1`, pl.FormattedBody)
	})

	t.Run("Delete", func(t *testing.T) {
		p := deleteTestPayload()

		pl, err := mc.Delete(p)
		require.NoError(t, err)
		require.NotNil(t, pl)

		assert.Equal(t, "[[test/repo](http://localhost:3000/test/repo):test] branch deleted by user1", pl.Body)
		assert.Equal(t, `[<a href="http://localhost:3000/test/repo">test/repo</a>:test] branch deleted by user1`, pl.FormattedBody)
	})

	t.Run("Fork", func(t *testing.T) {
		p := forkTestPayload()

		pl, err := mc.Fork(p)
		require.NoError(t, err)
		require.NotNil(t, pl)

		assert.Equal(t, "[test/repo2](http://localhost:3000/test/repo2) is forked to [test/repo](http://localhost:3000/test/repo)", pl.Body)
		assert.Equal(t, `<a href="http://localhost:3000/test/repo2">test/repo2</a> is forked to <a href="http://localhost:3000/test/repo">test/repo</a>`, pl.FormattedBody)
	})

	t.Run("Push", func(t *testing.T) {
		p := pushTestPayload()

		pl, err := mc.Push(p)
		require.NoError(t, err)
		require.NotNil(t, pl)

		assert.Equal(t, "[test/repo] user1 pushed 2 commits to test:\n[2020558](http://localhost:3000/test/repo/commit/2020558fe2e34debb818a514715839cabd25e778): commit message - user1\n[2020558](http://localhost:3000/test/repo/commit/2020558fe2e34debb818a514715839cabd25e778): commit message - user1", pl.Body)
		assert.Equal(t, `[test/repo] user1 pushed 2 commits to test:<br><a href="http://localhost:3000/test/repo/commit/2020558fe2e34debb818a514715839cabd25e778">2020558</a>: commit message - user1<br><a href="http://localhost:3000/test/repo/commit/2020558fe2e34debb818a514715839cabd25e778">2020558</a>: commit message - user1`, pl.FormattedBody)
	})

	t.Run("Issue", func(t *testing.T) {
		p := issueTestPayload()

		p.Action = api.HookIssueOpened
		pl, err := mc.Issue(p)
		require.NoError(t, err)
		require.NotNil(t, pl)

		assert.Equal(t, "[test/repo] Issue opened: [#2 crash](http://localhost:3000/test/repo/issues/2) by user1", pl.Body)
		assert.Equal(t, `[test/repo] Issue opened: <a href="http://localhost:3000/test/repo/issues/2">#2 crash</a> by user1`, pl.FormattedBody)

		p.Action = api.HookIssueClosed
		pl, err = mc.Issue(p)
		require.NoError(t, err)
		require.NotNil(t, pl)

		assert.Equal(t, "[test/repo] Issue closed: [#2 crash](http://localhost:3000/test/repo/issues/2) by user1", pl.Body)
		assert.Equal(t, `[test/repo] Issue closed: <a href="http://localhost:3000/test/repo/issues/2">#2 crash</a> by user1`, pl.FormattedBody)
	})

	t.Run("IssueComment", func(t *testing.T) {
		p := issueCommentTestPayload()

		pl, err := mc.IssueComment(p)
		require.NoError(t, err)
		require.NotNil(t, pl)

		assert.Equal(t, "[test/repo] New comment on issue [#2 crash](http://localhost:3000/test/repo/issues/2) by user1", pl.Body)
		assert.Equal(t, `[test/repo] New comment on issue <a href="http://localhost:3000/test/repo/issues/2">#2 crash</a> by user1`, pl.FormattedBody)
	})

	t.Run("PullRequest", func(t *testing.T) {
		p := pullRequestTestPayload()

		pl, err := mc.PullRequest(p)
		require.NoError(t, err)
		require.NotNil(t, pl)

		assert.Equal(t, "[test/repo] Pull request opened: [#12 Fix bug](http://localhost:3000/test/repo/pulls/12) by user1", pl.Body)
		assert.Equal(t, `[test/repo] Pull request opened: <a href="http://localhost:3000/test/repo/pulls/12">#12 Fix bug</a> by user1`, pl.FormattedBody)
	})

	t.Run("PullRequestComment", func(t *testing.T) {
		p := pullRequestCommentTestPayload()

		pl, err := mc.IssueComment(p)
		require.NoError(t, err)
		require.NotNil(t, pl)

		assert.Equal(t, "[test/repo] New comment on pull request [#12 Fix bug](http://localhost:3000/test/repo/pulls/12) by user1", pl.Body)
		assert.Equal(t, `[test/repo] New comment on pull request <a href="http://localhost:3000/test/repo/pulls/12">#12 Fix bug</a> by user1`, pl.FormattedBody)
	})

	t.Run("Review", func(t *testing.T) {
		p := pullRequestTestPayload()
		p.Action = api.HookIssueReviewed

		pl, err := mc.Review(p, webhook_module.HookEventPullRequestReviewApproved)
		require.NoError(t, err)
		require.NotNil(t, pl)

		assert.Equal(t, "[test/repo] Pull request review approved: [#12 Fix bug](http://localhost:3000/test/repo/pulls/12) by user1", pl.Body)
		assert.Equal(t, `[test/repo] Pull request review approved: <a href="http://localhost:3000/test/repo/pulls/12">#12 Fix bug</a> by user1`, pl.FormattedBody)
	})

	t.Run("Repository", func(t *testing.T) {
		p := repositoryTestPayload()

		pl, err := mc.Repository(p)
		require.NoError(t, err)
		require.NotNil(t, pl)

		assert.Equal(t, `[[test/repo](http://localhost:3000/test/repo)] Repository created by user1`, pl.Body)
		assert.Equal(t, `[<a href="http://localhost:3000/test/repo">test/repo</a>] Repository created by user1`, pl.FormattedBody)
	})

	t.Run("Package", func(t *testing.T) {
		p := packageTestPayload()

		pl, err := mc.Package(p)
		require.NoError(t, err)
		require.NotNil(t, pl)

		assert.Equal(t, `[[GiteaContainer](http://localhost:3000/user1/-/packages/container/GiteaContainer/latest)] Package published by user1`, pl.Body)
		assert.Equal(t, `[<a href="http://localhost:3000/user1/-/packages/container/GiteaContainer/latest">GiteaContainer</a>] Package published by user1`, pl.FormattedBody)
	})

	t.Run("Wiki", func(t *testing.T) {
		p := wikiTestPayload()

		p.Action = api.HookWikiCreated
		pl, err := mc.Wiki(p)
		require.NoError(t, err)
		require.NotNil(t, pl)

		assert.Equal(t, "[test/repo] New wiki page \"[index](http://localhost:3000/test/repo/wiki/index)\" (Wiki change comment) by user1", pl.Body)
		assert.Equal(t, `[test/repo] New wiki page "<a href="http://localhost:3000/test/repo/wiki/index">index</a>" (Wiki change comment) by user1`, pl.FormattedBody)

		p.Action = api.HookWikiEdited
		pl, err = mc.Wiki(p)
		require.NoError(t, err)
		require.NotNil(t, pl)

		assert.Equal(t, "[test/repo] Wiki page \"[index](http://localhost:3000/test/repo/wiki/index)\" edited (Wiki change comment) by user1", pl.Body)
		assert.Equal(t, `[test/repo] Wiki page "<a href="http://localhost:3000/test/repo/wiki/index">index</a>" edited (Wiki change comment) by user1`, pl.FormattedBody)

		p.Action = api.HookWikiDeleted
		pl, err = mc.Wiki(p)
		require.NoError(t, err)
		require.NotNil(t, pl)

		assert.Equal(t, "[test/repo] Wiki page \"[index](http://localhost:3000/test/repo/wiki/index)\" deleted by user1", pl.Body)
		assert.Equal(t, `[test/repo] Wiki page "<a href="http://localhost:3000/test/repo/wiki/index">index</a>" deleted by user1`, pl.FormattedBody)
	})

	t.Run("Release", func(t *testing.T) {
		p := pullReleaseTestPayload()

		pl, err := mc.Release(p)
		require.NoError(t, err)
		require.NotNil(t, pl)

		assert.Equal(t, "[test/repo] Release created: [v1.0](http://localhost:3000/test/repo/releases/tag/v1.0) by user1", pl.Body)
		assert.Equal(t, `[test/repo] Release created: <a href="http://localhost:3000/test/repo/releases/tag/v1.0">v1.0</a> by user1`, pl.FormattedBody)
	})

	t.Run("WorkflowRun", func(t *testing.T) {
		testCases := []struct {
			runStatus    actions_model.Status
			markdownBody string
			htmlBody     string
		}{
			{
				runStatus:    actions_model.StatusBlocked,
				markdownBody: `[acme/test] Workflow run "Update README.md" is blocked. [View details](https://example.com/acme/test/actions/runs/197719).`,
				htmlBody:     `[acme/test] Workflow run "Update README.md" is blocked. <a href="https://example.com/acme/test/actions/runs/197719">View details</a>.`,
			},
			{
				runStatus:    actions_model.StatusCancelled,
				markdownBody: `[acme/test] Workflow run "Update README.md" was cancelled. [View details](https://example.com/acme/test/actions/runs/197719).`,
				htmlBody:     `[acme/test] Workflow run "Update README.md" was cancelled. <a href="https://example.com/acme/test/actions/runs/197719">View details</a>.`,
			},
			{
				runStatus:    actions_model.StatusFailure,
				markdownBody: `[acme/test] Workflow run "Update README.md" has failed. [View details](https://example.com/acme/test/actions/runs/197719).`,
				htmlBody:     `[acme/test] Workflow run "Update README.md" has failed. <a href="https://example.com/acme/test/actions/runs/197719">View details</a>.`,
			},
			{
				runStatus:    actions_model.StatusRunning,
				markdownBody: `[acme/test] Workflow run "Update README.md" has started running. [View details](https://example.com/acme/test/actions/runs/197719).`,
				htmlBody:     `[acme/test] Workflow run "Update README.md" has started running. <a href="https://example.com/acme/test/actions/runs/197719">View details</a>.`,
			},
			{
				runStatus:    actions_model.StatusSkipped,
				markdownBody: `[acme/test] Workflow run "Update README.md" was skipped. [View details](https://example.com/acme/test/actions/runs/197719).`,
				htmlBody:     `[acme/test] Workflow run "Update README.md" was skipped. <a href="https://example.com/acme/test/actions/runs/197719">View details</a>.`,
			},
			{
				runStatus:    actions_model.StatusSuccess,
				markdownBody: `[acme/test] Workflow run "Update README.md" has completed successfully. [View details](https://example.com/acme/test/actions/runs/197719).`,
				htmlBody:     `[acme/test] Workflow run "Update README.md" has completed successfully. <a href="https://example.com/acme/test/actions/runs/197719">View details</a>.`,
			},
			{
				runStatus:    actions_model.StatusWaiting,
				markdownBody: `[acme/test] Workflow run "Update README.md" is waiting. [View details](https://example.com/acme/test/actions/runs/197719).`,
				htmlBody:     `[acme/test] Workflow run "Update README.md" is waiting. <a href="https://example.com/acme/test/actions/runs/197719">View details</a>.`,
			},
		}

		for _, testCase := range testCases {
			t.Run(testCase.runStatus.String(), func(t *testing.T) {
				inputPayload := &api.WorkflowRunPayload{
					Action: api.HookNewWorkflowRunAttempt,
					Run: &api.ActionRun{
						Title:   "Update README.md",
						Status:  testCase.runStatus.String(),
						HTMLURL: "https://example.com/acme/test/actions/runs/197719",
						Repo: &api.Repository{
							FullName: "acme/test",
						},
						TriggerUser: &api.User{
							UserName:  "jane",
							AvatarURL: "https://example.com/avatars/7dc9cf?size=64",
						},
					},
				}

				payload, err := mc.WorkflowRun(inputPayload)
				require.NoError(t, err)

				assert.Equal(t, testCase.markdownBody, payload.Body)
				assert.Equal(t, testCase.htmlBody, payload.FormattedBody)
			})
		}
	})

	t.Run("WorkflowJob", func(t *testing.T) {
		testCases := []struct {
			jobStatus    actions_model.Status
			markdownBody string
			htmlBody     string
		}{
			{
				jobStatus:    actions_model.StatusBlocked,
				markdownBody: `[acme/test] Workflow job "build-and-test" is blocked. [View details](https://example.com/acme/test/actions/runs/196540/jobs/3/attempt/1).`,
				htmlBody:     `[acme/test] Workflow job "build-and-test" is blocked. <a href="https://example.com/acme/test/actions/runs/196540/jobs/3/attempt/1">View details</a>.`,
			},
			{
				jobStatus:    actions_model.StatusCancelled,
				markdownBody: `[acme/test] Workflow job "build-and-test" was cancelled. [View details](https://example.com/acme/test/actions/runs/196540/jobs/3/attempt/1).`,
				htmlBody:     `[acme/test] Workflow job "build-and-test" was cancelled. <a href="https://example.com/acme/test/actions/runs/196540/jobs/3/attempt/1">View details</a>.`,
			},
			{
				jobStatus:    actions_model.StatusFailure,
				markdownBody: `[acme/test] Workflow job "build-and-test" has failed. [View details](https://example.com/acme/test/actions/runs/196540/jobs/3/attempt/1).`,
				htmlBody:     `[acme/test] Workflow job "build-and-test" has failed. <a href="https://example.com/acme/test/actions/runs/196540/jobs/3/attempt/1">View details</a>.`,
			},
			{
				jobStatus:    actions_model.StatusRunning,
				markdownBody: `[acme/test] Workflow job "build-and-test" has started running. [View details](https://example.com/acme/test/actions/runs/196540/jobs/3/attempt/1).`,
				htmlBody:     `[acme/test] Workflow job "build-and-test" has started running. <a href="https://example.com/acme/test/actions/runs/196540/jobs/3/attempt/1">View details</a>.`,
			},
			{
				jobStatus:    actions_model.StatusSkipped,
				markdownBody: `[acme/test] Workflow job "build-and-test" was skipped. [View details](https://example.com/acme/test/actions/runs/196540/jobs/3/attempt/1).`,
				htmlBody:     `[acme/test] Workflow job "build-and-test" was skipped. <a href="https://example.com/acme/test/actions/runs/196540/jobs/3/attempt/1">View details</a>.`,
			},
			{
				jobStatus:    actions_model.StatusSuccess,
				markdownBody: `[acme/test] Workflow job "build-and-test" has completed successfully. [View details](https://example.com/acme/test/actions/runs/196540/jobs/3/attempt/1).`,
				htmlBody:     `[acme/test] Workflow job "build-and-test" has completed successfully. <a href="https://example.com/acme/test/actions/runs/196540/jobs/3/attempt/1">View details</a>.`,
			},
			{
				jobStatus:    actions_model.StatusWaiting,
				markdownBody: `[acme/test] Workflow job "build-and-test" is waiting. [View details](https://example.com/acme/test/actions/runs/196540/jobs/3/attempt/1).`,
				htmlBody:     `[acme/test] Workflow job "build-and-test" is waiting. <a href="https://example.com/acme/test/actions/runs/196540/jobs/3/attempt/1">View details</a>.`,
			},
		}

		for _, testCase := range testCases {
			t.Run(testCase.jobStatus.String(), func(t *testing.T) {
				inputPayload := &api.WorkflowJobPayload{
					Action: api.HookNewWorkflowJobAttempt,
					Job: &api.ActionRunJob{
						Name:    "build-and-test",
						HTMLURL: "https://example.com/acme/test/actions/runs/196540/jobs/3/attempt/1",
						Status:  testCase.jobStatus.String(),
					},
					Run: &api.ActionRun{
						Title: "Update README.md",
						Repo: &api.Repository{
							FullName: "acme/test",
						},
						TriggerUser: &api.User{
							UserName:  "jane",
							AvatarURL: "https://example.com/avatars/7dc9cf?size=64",
						},
					},
				}

				payload, err := mc.WorkflowJob(inputPayload)
				require.NoError(t, err)

				assert.Equal(t, testCase.markdownBody, payload.Body)
				assert.Equal(t, testCase.htmlBody, payload.FormattedBody)
			})
		}
	})
}

func TestMatrixJSONPayload(t *testing.T) {
	p := pushTestPayload()
	data, err := p.JSONPayload()
	require.NoError(t, err)

	hook := &webhook_model.Webhook{
		RepoID:   3,
		IsActive: true,
		Type:     webhook_module.MATRIX,
		URL:      "https://matrix.example.com/_matrix/client/v3/rooms/ROOM_ID/send/m.room.message",
		Meta:     `{"message_type":0}`, // text
	}
	task := &webhook_model.HookTask{
		HookID:         hook.ID,
		EventType:      webhook_module.HookEventPush,
		PayloadContent: string(data),
		PayloadVersion: 2,
	}

	req, reqBody, err := matrixHandler{}.NewRequest(t.Context(), hook, task)
	require.NotNil(t, req)
	require.NotNil(t, reqBody)
	require.NoError(t, err)

	assert.Equal(t, "PUT", req.Method)
	assert.Equal(t, "/_matrix/client/v3/rooms/ROOM_ID/send/m.room.message/OWkz2tnGWTev7X-soFlJGDJYtqaL_or14nskUb5Ath0", req.URL.Path)
	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
	var body MatrixPayload
	err = json.NewDecoder(req.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "[test/repo] user1 pushed 2 commits to test:\n[2020558](http://localhost:3000/test/repo/commit/2020558fe2e34debb818a514715839cabd25e778): commit message - user1\n[2020558](http://localhost:3000/test/repo/commit/2020558fe2e34debb818a514715839cabd25e778): commit message - user1", body.Body)
}
