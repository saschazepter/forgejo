// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"net/url"
	"testing"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/services/actions"
	"forgejo.org/services/automerge"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActionsAutomerge(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		defer test.MockVariableValue(&setting.Actions.Enabled, true)()

		ctx := db.DefaultContext

		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
		pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: 2})
		job := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: 292})

		assert.False(t, pr.HasMerged, "PR should not be merged")
		assert.Equal(t, issues_model.PullRequestStatusMergeable, pr.Status, "PR should be mergeable")

		scheduled, err := automerge.ScheduleAutoMerge(ctx, user, pr, repo_model.MergeStyleMerge, "Dummy", false)

		require.NoError(t, err, "PR should be scheduled for automerge")
		assert.True(t, scheduled, "PR should be scheduled for automerge")

		actions.CreateCommitStatus(ctx, job)

		pr = unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: 2})

		assert.True(t, pr.HasMerged, "PR should be merged")
	},
	)
}

func TestActionsForcePushCommitStatus(t *testing.T) {
	defer unittest.OverrideFixtures("tests/integration/fixtures/TestForcePushCommitStatus/")()
	defer tests.PrepareTestEnv(t)()

	req := NewRequest(t, "GET", "/user2/commitsonpr/pulls/1")
	resp := MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	htmlDoc.AssertElement(t, ".error-code", false)

	htmlDoc.AssertElement(t, "#issuecomment-17 .forced-push [data-tippy='commit-statuses']:nth-of-type(3) svg.commit-status.octicon-dot-fill", true)
	htmlDoc.AssertElement(t, "#issuecomment-17 .forced-push [data-tippy='commit-statuses']:nth-of-type(5) svg.commit-status.octicon-check", true)

	htmlDoc.AssertElement(t, "#issuecomment-1001 .forced-push [data-tippy='commit-statuses']:nth-of-type(3) svg.commit-status.octicon-check", true)
	htmlDoc.AssertElement(t, "#issuecomment-1001 .forced-push [data-tippy='commit-statuses']:nth-of-type(4) svg.commit-status.octicon-check", true)
}
