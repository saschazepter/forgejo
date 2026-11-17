// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"sync"
	"testing"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	git_model "forgejo.org/models/git"
	"forgejo.org/modules/json"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepoCommits(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	// Request repository commits page
	req := NewRequest(t, "GET", "/user2/repo1/commits/branch/master")
	resp := session.MakeRequest(t, req, http.StatusOK)

	doc := NewHTMLParser(t, resp.Body)
	commitURL, exists := doc.doc.Find("#commits-table tbody tr td.sha a").Attr("href")
	assert.True(t, exists)
	assert.NotEmpty(t, commitURL)
	doc.AssertElement(t, ".repo-path", false)
}

func doTestRepoCommitWithStatus(t *testing.T, state string, classes ...string) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	// Request repository commits page
	req := NewRequest(t, "GET", "/user2/repo1/commits/branch/master")
	resp := session.MakeRequest(t, req, http.StatusOK)

	doc := NewHTMLParser(t, resp.Body)
	// Get first commit URL
	commitURL, exists := doc.doc.Find("#commits-table tbody tr td.sha a").Attr("href")
	assert.True(t, exists)
	assert.NotEmpty(t, commitURL)

	// Call API to add status for commit
	ctx := NewAPITestContext(t, "user2", "repo1", auth_model.AccessTokenScopeWriteRepository)
	t.Run("CreateStatus", doAPICreateCommitStatus(ctx, path.Base(commitURL), api.CreateStatusOption{
		State:       api.CommitStatusState(state),
		TargetURL:   "http://test.ci/",
		Description: "",
		Context:     "testci",
	}))

	req = NewRequest(t, "GET", "/user2/repo1/commits/branch/master")
	resp = session.MakeRequest(t, req, http.StatusOK)

	doc = NewHTMLParser(t, resp.Body)
	// Check if commit status is displayed in message column (.tippy-target to ignore the tippy trigger)
	sel := doc.doc.Find("#commits-table tbody tr td.message .tippy-target .commit-status")
	assert.Equal(t, 1, sel.Length())
	for _, class := range classes {
		assert.True(t, sel.HasClass(class))
	}

	// By SHA
	req = NewRequest(t, "GET", "/api/v1/repos/user2/repo1/commits/"+path.Base(commitURL)+"/statuses")
	reqOne := NewRequest(t, "GET", "/api/v1/repos/user2/repo1/commits/"+path.Base(commitURL)+"/status")
	testRepoCommitsWithStatus(t, session.MakeRequest(t, req, http.StatusOK), session.MakeRequest(t, reqOne, http.StatusOK), state)

	// By short SHA
	req = NewRequest(t, "GET", "/api/v1/repos/user2/repo1/commits/"+path.Base(commitURL)[:10]+"/statuses")
	reqOne = NewRequest(t, "GET", "/api/v1/repos/user2/repo1/commits/"+path.Base(commitURL)[:10]+"/status")
	testRepoCommitsWithStatus(t, session.MakeRequest(t, req, http.StatusOK), session.MakeRequest(t, reqOne, http.StatusOK), state)

	// By Ref
	req = NewRequest(t, "GET", "/api/v1/repos/user2/repo1/commits/master/statuses")
	reqOne = NewRequest(t, "GET", "/api/v1/repos/user2/repo1/commits/master/status")
	testRepoCommitsWithStatus(t, session.MakeRequest(t, req, http.StatusOK), session.MakeRequest(t, reqOne, http.StatusOK), state)
	req = NewRequest(t, "GET", "/api/v1/repos/user2/repo1/commits/v1.1/statuses")
	reqOne = NewRequest(t, "GET", "/api/v1/repos/user2/repo1/commits/v1.1/status")
	testRepoCommitsWithStatus(t, session.MakeRequest(t, req, http.StatusOK), session.MakeRequest(t, reqOne, http.StatusOK), state)
}

func testRepoCommitsWithStatus(t *testing.T, resp, respOne *httptest.ResponseRecorder, state string) {
	var statuses []*api.CommitStatus
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &statuses))
	var status api.CombinedStatus
	require.NoError(t, json.Unmarshal(respOne.Body.Bytes(), &status))
	assert.NotNil(t, status)

	if assert.Len(t, statuses, 1) {
		assert.Equal(t, api.CommitStatusState(state), statuses[0].State)
		assert.Equal(t, setting.AppURL+"api/v1/repos/user2/repo1/statuses/65f1bf27bc3bf70f64657658635e66094edbcb4d", statuses[0].URL)
		assert.Equal(t, "http://test.ci/", statuses[0].TargetURL)
		assert.Empty(t, statuses[0].Description)
		assert.Equal(t, "testci", statuses[0].Context)

		assert.Len(t, status.Statuses, 1)
		assert.Equal(t, statuses[0], status.Statuses[0])
		assert.Equal(t, "65f1bf27bc3bf70f64657658635e66094edbcb4d", status.SHA)
	}
}

func TestCombinedStatusReturnsPendingWhenCommitHasNoStatuses(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")
	session.MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/user2/repo1/git/commits/65f1bf27bc3bf70f64657658635e66094edbcb4d"), http.StatusOK)
	resp := session.MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/user2/repo1/commits/65f1bf27bc3bf70f64657658635e66094edbcb4d/status"), http.StatusOK)
	assert.Equal(t, "0", resp.Header().Get("X-Total-Count"))

	var status api.CombinedStatus
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &status))
	assert.Equal(t, api.CommitStatusPending, status.State)
	assert.Equal(t, []*api.CommitStatus{}, status.Statuses)
}

func TestCombinedStatusReturnsPendingEvenWhenCommitDoesNotExist(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user2")
	for _, ref := range []string{"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "branch-not-exist"} {
		t.Run(ref, func(t *testing.T) {
			session.MakeRequest(t, NewRequestf(t, "GET", "/api/v1/repos/user2/repo1/git/commits/%s", ref), http.StatusNotFound)
			resp := session.MakeRequest(t, NewRequestf(t, "GET", "/api/v1/repos/user2/repo1/commits/%s/status", ref), http.StatusOK)
			assert.Equal(t, "0", resp.Header().Get("X-Total-Count"))
			var status api.CombinedStatus
			require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &status))
			assert.Equal(t, api.CommitStatusPending, status.State)
			assert.Equal(t, []*api.CommitStatus{}, status.Statuses)
		})
	}
}

func TestCombinedStatusReturnsEmptyObjectWhenPageIsBeyondLastStatus(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	const sha = "65f1bf27bc3bf70f64657658635e66094edbcb4d"
	session := loginUser(t, "user2")
	ctx := NewAPITestContext(t, "user2", "repo1", auth_model.AccessTokenScopeWriteRepository)
	for _, name := range []string{"build", "lint"} {
		require.True(t, t.Run(name, doAPICreateCommitStatus(ctx, sha, api.CreateStatusOption{
			State:   api.CommitStatusSuccess,
			Context: name,
		})))
	}
	statuses, count, err := git_model.GetLatestCommitStatus(db.DefaultContext, 1, sha, db.ListOptions{Page: 3, PageSize: 1})
	require.NoError(t, err)
	assert.EqualValues(t, 2, count)
	assert.Empty(t, statuses)
	for page := 1; page <= 2; page++ {
		resp := session.MakeRequest(t, NewRequestf(t, "GET", "/api/v1/repos/user2/repo1/commits/%s/status?limit=1&page=%d", sha, page), http.StatusOK)
		assert.Equal(t, "2", resp.Header().Get("X-Total-Count"))
		var status api.CombinedStatus
		require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &status))
		assert.Len(t, status.Statuses, 1)
	}
	resp := session.MakeRequest(t, NewRequestf(t, "GET", "/api/v1/repos/user2/repo1/commits/%s/status?limit=1&page=3", sha), http.StatusOK)
	assert.Empty(t, resp.Header().Get("X-Total-Count"))
	var status *api.CombinedStatus
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &status))
	require.NotNil(t, status)
	assert.Empty(t, status.State)
	assert.Nil(t, status.Statuses)
}

func TestRepoCommitsWithStatusPending(t *testing.T) {
	doTestRepoCommitWithStatus(t, "pending", "octicon-dot-fill", "yellow")
}

func TestRepoCommitsWithStatusSuccess(t *testing.T) {
	doTestRepoCommitWithStatus(t, "success", "octicon-check", "green")
}

func TestRepoCommitsWithStatusError(t *testing.T) {
	doTestRepoCommitWithStatus(t, "error", "gitea-exclamation", "red")
}

func TestRepoCommitsWithStatusFailure(t *testing.T) {
	doTestRepoCommitWithStatus(t, "failure", "octicon-x", "red")
}

func TestRepoCommitsWithStatusWarning(t *testing.T) {
	doTestRepoCommitWithStatus(t, "warning", "gitea-exclamation", "yellow")
}

func TestRepoCommitsStatusParallel(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	// Request repository commits page
	req := NewRequest(t, "GET", "/user2/repo1/commits/branch/master")
	resp := session.MakeRequest(t, req, http.StatusOK)

	doc := NewHTMLParser(t, resp.Body)
	// Get first commit URL
	commitURL, exists := doc.doc.Find("#commits-table tbody tr td.sha a").Attr("href")
	assert.True(t, exists)
	assert.NotEmpty(t, commitURL)

	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func(parentT *testing.T, i int) {
			parentT.Run(fmt.Sprintf("ParallelCreateStatus_%d", i), func(t *testing.T) {
				ctx := NewAPITestContext(t, "user2", "repo1", auth_model.AccessTokenScopeWriteRepository)
				runBody := doAPICreateCommitStatus(ctx, path.Base(commitURL), api.CreateStatusOption{
					State:       api.CommitStatusPending,
					TargetURL:   "http://test.ci/",
					Description: "",
					Context:     "testci",
				})
				runBody(t)
				wg.Done()
			})
		}(t, i)
	}
	wg.Wait()
}

func TestRepoCommitsStatusMultiple(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	// Request repository commits page
	req := NewRequest(t, "GET", "/user2/repo1/commits/branch/master")
	resp := session.MakeRequest(t, req, http.StatusOK)

	doc := NewHTMLParser(t, resp.Body)
	// Get first commit URL
	commitURL, exists := doc.doc.Find("#commits-table tbody tr td.sha a").Attr("href")
	assert.True(t, exists)
	assert.NotEmpty(t, commitURL)

	// Call API to add status for commit
	ctx := NewAPITestContext(t, "user2", "repo1", auth_model.AccessTokenScopeWriteRepository)
	t.Run("CreateStatus", doAPICreateCommitStatus(ctx, path.Base(commitURL), api.CreateStatusOption{
		State:       api.CommitStatusSuccess,
		TargetURL:   "http://test.ci/",
		Description: "",
		Context:     "testci",
	}))

	t.Run("CreateStatus", doAPICreateCommitStatus(ctx, path.Base(commitURL), api.CreateStatusOption{
		State:       api.CommitStatusSuccess,
		TargetURL:   "http://test.ci/",
		Description: "",
		Context:     "other_context",
	}))

	req = NewRequest(t, "GET", "/user2/repo1/commits/branch/master")
	resp = session.MakeRequest(t, req, http.StatusOK)

	doc = NewHTMLParser(t, resp.Body)
	// Check that the data-tippy="commit-statuses" (for trigger) and commit-status (svg) are present
	sel := doc.doc.Find("#commits-table tbody tr td.message [data-tippy=\"commit-statuses\"] .commit-status")
	assert.Equal(t, 1, sel.Length())
}

func TestRepoCommitStatusesLimit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	// Request repository commits page
	req := NewRequest(t, "GET", "/user2/repo1/commits/branch/master")
	resp := session.MakeRequest(t, req, http.StatusOK)

	doc := NewHTMLParser(t, resp.Body)
	// Get first commit URL
	commitURL, exists := doc.doc.Find("#commits-table tbody tr td.sha a").Attr("href")
	assert.True(t, exists)
	assert.NotEmpty(t, commitURL)

	// Call API to add status for commit
	ctx := NewAPITestContext(t, "user2", "repo1", auth_model.AccessTokenScopeWriteRepository)
	t.Run("CreateStatus", doAPICreateCommitStatus(ctx, path.Base(commitURL), api.CreateStatusOption{
		State:       api.CommitStatusState("failure"),
		TargetURL:   "http://test.ci/",
		Description: "",
		Context:     "testci",
	}))
	t.Run("CreateStatus", doAPICreateCommitStatus(ctx, path.Base(commitURL), api.CreateStatusOption{
		State:       api.CommitStatusState("warning"),
		TargetURL:   "http://test.ci/",
		Description: "",
		Context:     "other_context",
	}))

	// By SHA with limit #9783
	req = NewRequest(t, "GET", "/api/v1/repos/user2/repo1/commits/"+path.Base(commitURL)+"/statuses?limit=1")
	resp = session.MakeRequest(t, req, http.StatusOK)

	var statusesSha []*api.CommitStatus
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &statusesSha))

	assert.Len(t, statusesSha, 1)
	assert.Equal(t, "2", resp.Header().Get("X-Total-Count"))
}

func TestRepoStatusesBySHALimit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	// Request repository commits page
	req := NewRequest(t, "GET", "/user2/repo1/commits/branch/master")
	resp := session.MakeRequest(t, req, http.StatusOK)

	doc := NewHTMLParser(t, resp.Body)
	// Get first commit URL
	commitURL, exists := doc.doc.Find("#commits-table tbody tr td.sha a").Attr("href")
	assert.True(t, exists)
	assert.NotEmpty(t, commitURL)

	// Call API to add status for commit
	ctx := NewAPITestContext(t, "user2", "repo1", auth_model.AccessTokenScopeWriteRepository)
	t.Run("CreateStatus", doAPICreateCommitStatus(ctx, path.Base(commitURL), api.CreateStatusOption{
		State:       api.CommitStatusState("failure"),
		TargetURL:   "http://test.ci/",
		Description: "",
		Context:     "testci",
	}))
	t.Run("CreateStatus", doAPICreateCommitStatus(ctx, path.Base(commitURL), api.CreateStatusOption{
		State:       api.CommitStatusState("warning"),
		TargetURL:   "http://test.ci/",
		Description: "",
		Context:     "other_context",
	}))

	// Statuses endpoint
	req = NewRequest(t, "GET", "/api/v1/repos/user2/repo1/statuses/"+path.Base(commitURL)+"?limit=1")
	resp = session.MakeRequest(t, req, http.StatusOK)

	var statuses []*api.CommitStatus
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &statuses))

	assert.Len(t, statuses, 1)
	assert.Equal(t, "2", resp.Header().Get("X-Total-Count"))
}

func TestCreateCommitStatusNonExistingSHA(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	ctx := NewAPITestContext(t, "user2", "repo1", auth_model.AccessTokenScopeWriteRepository)
	ctx.ExpectedCode = http.StatusNotFound

	t.Run("NonExistingSHA", doAPICreateCommitStatus(ctx, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", api.CreateStatusOption{
		State:       api.CommitStatusPending,
		TargetURL:   "http://test.ci/",
		Description: "",
		Context:     "testci",
	}))
}
