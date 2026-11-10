// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issue

import (
	"slices"
	"testing"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRefEndNamesAndURLs(t *testing.T) {
	issues := []*issues_model.Issue{
		{ID: 1, Ref: "refs/heads/branch1"},
		{ID: 2, Ref: "refs/tags/tag1"},
		{ID: 3, Ref: "c0ffee"},
	}
	repoLink := "/foo/bar"

	endNames, urls := GetRefEndNamesAndURLs(issues, repoLink)
	assert.Equal(t, map[int64]string{1: "branch1", 2: "tag1", 3: "c0ffee"}, endNames)
	assert.Equal(t, map[int64]string{
		1: repoLink + "/src/branch/branch1",
		2: repoLink + "/src/tag/tag1",
		3: repoLink + "/src/commit/c0ffee",
	}, urls)
}

func TestIssue_DeleteIssue(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	issueIDs, err := issues_model.GetIssueIDsByRepoID(db.DefaultContext, 1)
	require.NoError(t, err)
	assert.Len(t, issueIDs, 5)

	issue := &issues_model.Issue{
		RepoID: 1,
		ID:     issueIDs[2],
	}

	err = deleteIssue(db.DefaultContext, issue)
	require.NoError(t, err)
	issueIDs, err = issues_model.GetIssueIDsByRepoID(db.DefaultContext, 1)
	require.NoError(t, err)
	assert.Len(t, issueIDs, 4)

	// check attachment removal
	attachments, err := repo_model.GetAttachmentsByIssueID(db.DefaultContext, 4)
	require.NoError(t, err)
	issue, err = issues_model.GetIssueByID(db.DefaultContext, 4)
	require.NoError(t, err)
	err = deleteIssue(db.DefaultContext, issue)
	require.NoError(t, err)
	assert.Len(t, attachments, 2)
	for i := range attachments {
		attachment, err := repo_model.GetAttachmentByUUID(db.DefaultContext, attachments[i].UUID)
		require.Error(t, err)
		assert.True(t, repo_model.IsErrAttachmentNotExist(err))
		assert.Nil(t, attachment)
	}

	// check issue dependencies
	user, err := user_model.GetUserByID(db.DefaultContext, 1)
	require.NoError(t, err)
	issue1, err := issues_model.GetIssueByID(db.DefaultContext, 1)
	require.NoError(t, err)
	issue2, err := issues_model.GetIssueByID(db.DefaultContext, 2)
	require.NoError(t, err)
	err = issues_model.CreateIssueDependency(db.DefaultContext, user, issue1, issue2)
	require.NoError(t, err)
	left, err := issues_model.IssueNoDependenciesLeft(db.DefaultContext, issue1)
	require.NoError(t, err)
	assert.False(t, left)

	err = deleteIssue(db.DefaultContext, issue2)
	require.NoError(t, err)
	left, err = issues_model.IssueNoDependenciesLeft(db.DefaultContext, issue1)
	require.NoError(t, err)
	assert.True(t, left)
}

func TestIssue_DeleteIssue_PullRequestRefs(t *testing.T) {
	unittest.PrepareTestEnv(t)

	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	// Pull request #2 in user2/repo1
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 2})
	require.True(t, issue.IsPull)
	require.NoError(t, issue.LoadRepo(t.Context()))

	gitRepo, err := gitrepo.OpenRepository(t.Context(), issue.Repo)
	require.NoError(t, err)
	defer gitRepo.Close()

	const commitID = "4a357436d925b5c974181ff12a994538ddc5a269"

	// refs belonging to PR #2 which should be removed
	removedRefs := []string{
		git.PullPrefix + "2/head",
		git.PullRequestForcePush + "2/comment/100/old",
		git.PullRequestForcePush + "2/comment/100/new",
		git.PullRequestForcePush + "2/comment/101/old",
		git.PullRequestForcePush + "2/comment/101/new",
	}
	// refs belonging to other PRs which must be retained -- including PR #20 & #200, which share a prefix with PR #2
	retainedRefs := []string{
		git.PullPrefix + "3/head",
		git.PullPrefix + "5/head",
		git.PullPrefix + "20/head",
		git.PullRequestForcePush + "20/comment/100/old",
		git.PullPrefix + "200/head",
	}
	for _, ref := range append(slices.Clone(removedRefs), retainedRefs...) {
		require.NoError(t, gitRepo.SetReference(ref, commitID))
	}

	require.NoError(t, DeleteIssue(t.Context(), doer, gitRepo, issue))

	refs, err := gitRepo.GetRefs()
	require.NoError(t, err)
	refNames := make([]string, 0, len(refs))
	for _, ref := range refs {
		refNames = append(refNames, ref.Name)
	}

	for _, ref := range removedRefs {
		assert.NotContains(t, refNames, ref)
	}
	for _, ref := range retainedRefs {
		assert.Contains(t, refNames, ref)
	}

	unittest.AssertNotExistsBean(t, &issues_model.Issue{ID: 2})
}
