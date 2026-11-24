// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issue

import (
	"testing"

	activities_model "forgejo.org/models/activities"
	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/repository"
	"forgejo.org/modules/setting"

	"github.com/stretchr/testify/require"
)

func TestUpdateIssuesCommit(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	pushCommits := []*repository.PushCommit{
		{
			Sha1:           "abcdef1",
			CommitterEmail: "user2@example.com",
			CommitterName:  "User Two",
			AuthorEmail:    "user4@example.com",
			AuthorName:     "User Four",
			Message:        "start working on #FST-1, #1",
		},
		{
			Sha1:           "abcdef2",
			CommitterEmail: "user2@example.com",
			CommitterName:  "User Two",
			AuthorEmail:    "user2@example.com",
			AuthorName:     "User Two",
			Message:        "a plain message",
		},
		{
			Sha1:           "abcdef2",
			CommitterEmail: "user2@example.com",
			CommitterName:  "User Two",
			AuthorEmail:    "user2@example.com",
			AuthorName:     "User Two",
			Message:        "close #2",
		},
	}

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	repo.Owner = user

	commentBean := &issues_model.Comment{
		Type:      issues_model.CommentTypeCommitRef,
		CommitSHA: "abcdef1",
		PosterID:  user.ID,
		IssueID:   1,
	}
	issueBean := &issues_model.Issue{RepoID: repo.ID, Index: 4}

	unittest.AssertNotExistsBean(t, commentBean)
	unittest.AssertNotExistsBean(t, &issues_model.Issue{RepoID: repo.ID, Index: 2}, unittest.Cond("is_closed = ?", true))
	require.NoError(t, UpdateIssuesCommit(db.DefaultContext, user, repo, pushCommits, repo.DefaultBranch))
	unittest.AssertExistsAndLoadBean(t, commentBean)
	unittest.AssertExistsAndLoadBean(t, issueBean, unittest.Cond("is_closed = ?", true))
	unittest.CheckConsistencyFor(t, &activities_model.Action{})

	// Test that push to a non-default branch closes no issue.
	pushCommits = []*repository.PushCommit{
		{
			Sha1:           "abcdef1",
			CommitterEmail: "user2@example.com",
			CommitterName:  "User Two",
			AuthorEmail:    "user4@example.com",
			AuthorName:     "User Four",
			Message:        "close #1",
		},
	}
	repo = unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	commentBean = &issues_model.Comment{
		Type:      issues_model.CommentTypeCommitRef,
		CommitSHA: "abcdef1",
		PosterID:  user.ID,
		IssueID:   6,
	}
	issueBean = &issues_model.Issue{RepoID: repo.ID, Index: 1}

	unittest.AssertNotExistsBean(t, commentBean)
	unittest.AssertNotExistsBean(t, &issues_model.Issue{RepoID: repo.ID, Index: 1}, unittest.Cond("is_closed = ?", true))
	require.NoError(t, UpdateIssuesCommit(db.DefaultContext, user, repo, pushCommits, "non-existing-branch"))
	unittest.AssertExistsAndLoadBean(t, commentBean)
	unittest.AssertNotExistsBean(t, issueBean, unittest.Cond("is_closed = ?", true))
	unittest.CheckConsistencyFor(t, &activities_model.Action{})

	pushCommits = []*repository.PushCommit{
		{
			Sha1:           "abcdef3",
			CommitterEmail: "user2@example.com",
			CommitterName:  "User Two",
			AuthorEmail:    "user2@example.com",
			AuthorName:     "User Two",
			Message:        "close " + setting.AppURL + repo.FullName() + "/pulls/1",
		},
	}
	repo = unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	commentBean = &issues_model.Comment{
		Type:      issues_model.CommentTypeCommitRef,
		CommitSHA: "abcdef3",
		PosterID:  user.ID,
		IssueID:   6,
	}
	issueBean = &issues_model.Issue{RepoID: repo.ID, Index: 1}

	unittest.AssertNotExistsBean(t, commentBean)
	unittest.AssertNotExistsBean(t, &issues_model.Issue{RepoID: repo.ID, Index: 1}, unittest.Cond("is_closed = ?", true))
	require.NoError(t, UpdateIssuesCommit(db.DefaultContext, user, repo, pushCommits, repo.DefaultBranch))
	unittest.AssertExistsAndLoadBean(t, commentBean)
	unittest.AssertExistsAndLoadBean(t, issueBean, unittest.Cond("is_closed = ?", true))
	unittest.CheckConsistencyFor(t, &activities_model.Action{})
}

func TestUpdateIssuesCommit_Colon(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	pushCommits := []*repository.PushCommit{
		{
			Sha1:           "abcdef2",
			CommitterEmail: "user2@example.com",
			CommitterName:  "User Two",
			AuthorEmail:    "user2@example.com",
			AuthorName:     "User Two",
			Message:        "close: #2",
		},
	}

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	repo.Owner = user

	issueBean := &issues_model.Issue{RepoID: repo.ID, Index: 4}

	unittest.AssertNotExistsBean(t, &issues_model.Issue{RepoID: repo.ID, Index: 2}, unittest.Cond("is_closed = ?", true))
	require.NoError(t, UpdateIssuesCommit(db.DefaultContext, user, repo, pushCommits, repo.DefaultBranch))
	unittest.AssertExistsAndLoadBean(t, issueBean, unittest.Cond("is_closed = ?", true))
	unittest.CheckConsistencyFor(t, &activities_model.Action{})
}

func TestUpdateIssuesCommit_Issue5957(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	// Test that push to a non-default branch closes an issue.
	pushCommits := []*repository.PushCommit{
		{
			Sha1:           "abcdef1",
			CommitterEmail: "user2@example.com",
			CommitterName:  "User Two",
			AuthorEmail:    "user4@example.com",
			AuthorName:     "User Four",
			Message:        "close #2",
		},
	}

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	commentBean := &issues_model.Comment{
		Type:      issues_model.CommentTypeCommitRef,
		CommitSHA: "abcdef1",
		PosterID:  user.ID,
		IssueID:   7,
	}

	issueBean := &issues_model.Issue{RepoID: repo.ID, Index: 2, ID: 7}

	unittest.AssertNotExistsBean(t, commentBean)
	unittest.AssertNotExistsBean(t, issueBean, unittest.Cond("is_closed = ?", true))
	require.NoError(t, UpdateIssuesCommit(db.DefaultContext, user, repo, pushCommits, "non-existing-branch"))
	unittest.AssertExistsAndLoadBean(t, commentBean)
	unittest.AssertExistsAndLoadBean(t, issueBean, unittest.Cond("is_closed = ?", true))
	unittest.CheckConsistencyFor(t, &activities_model.Action{})
}

func TestUpdateIssuesCommit_AnotherRepo(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	// Test that a push to default branch closes issue in another repo
	// If the user also has push permissions to that repo
	pushCommits := []*repository.PushCommit{
		{
			Sha1:           "abcdef1",
			CommitterEmail: "user2@example.com",
			CommitterName:  "User Two",
			AuthorEmail:    "user2@example.com",
			AuthorName:     "User Two",
			Message:        "close user2/repo1#1",
		},
	}

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	commentBean := &issues_model.Comment{
		Type:      issues_model.CommentTypeCommitRef,
		CommitSHA: "abcdef1",
		PosterID:  user.ID,
		IssueID:   1,
	}

	issueBean := &issues_model.Issue{RepoID: 1, Index: 1, ID: 1}

	unittest.AssertNotExistsBean(t, commentBean)
	unittest.AssertNotExistsBean(t, issueBean, unittest.Cond("is_closed = ?", true))
	require.NoError(t, UpdateIssuesCommit(db.DefaultContext, user, repo, pushCommits, repo.DefaultBranch))
	unittest.AssertExistsAndLoadBean(t, commentBean)
	unittest.AssertExistsAndLoadBean(t, issueBean, unittest.Cond("is_closed = ?", true))
	unittest.CheckConsistencyFor(t, &activities_model.Action{})
}

func TestUpdateIssuesCommit_AnotherRepo_FullAddress(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	// Test that a push to default branch closes issue in another repo
	// If the user also has push permissions to that repo
	pushCommits := []*repository.PushCommit{
		{
			Sha1:           "abcdef1",
			CommitterEmail: "user2@example.com",
			CommitterName:  "User Two",
			AuthorEmail:    "user2@example.com",
			AuthorName:     "User Two",
			Message:        "close " + setting.AppURL + "user2/repo1/issues/1",
		},
	}

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	commentBean := &issues_model.Comment{
		Type:      issues_model.CommentTypeCommitRef,
		CommitSHA: "abcdef1",
		PosterID:  user.ID,
		IssueID:   1,
	}

	issueBean := &issues_model.Issue{RepoID: 1, Index: 1, ID: 1}

	unittest.AssertNotExistsBean(t, commentBean)
	unittest.AssertNotExistsBean(t, issueBean, unittest.Cond("is_closed = ?", true))
	require.NoError(t, UpdateIssuesCommit(db.DefaultContext, user, repo, pushCommits, repo.DefaultBranch))
	unittest.AssertExistsAndLoadBean(t, commentBean)
	unittest.AssertExistsAndLoadBean(t, issueBean, unittest.Cond("is_closed = ?", true))
	unittest.CheckConsistencyFor(t, &activities_model.Action{})
}

func TestUpdateIssuesCommit_AnotherRepoNoPermission(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 10})

	// Test that a push with close reference *can not* close issue
	// If the committer doesn't have push rights in that repo
	pushCommits := []*repository.PushCommit{
		{
			Sha1:           "abcdef3",
			CommitterEmail: "user10@example.com",
			CommitterName:  "User Ten",
			AuthorEmail:    "user10@example.com",
			AuthorName:     "User Ten",
			Message:        "close org3/repo3#1",
		},
		{
			Sha1:           "abcdef4",
			CommitterEmail: "user10@example.com",
			CommitterName:  "User Ten",
			AuthorEmail:    "user10@example.com",
			AuthorName:     "User Ten",
			Message:        "close " + setting.AppURL + "org3/repo3/issues/1",
		},
	}

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 6})
	commentBean := &issues_model.Comment{
		Type:      issues_model.CommentTypeCommitRef,
		CommitSHA: "abcdef3",
		PosterID:  user.ID,
		IssueID:   6,
	}
	commentBean2 := &issues_model.Comment{
		Type:      issues_model.CommentTypeCommitRef,
		CommitSHA: "abcdef4",
		PosterID:  user.ID,
		IssueID:   6,
	}

	issueBean := &issues_model.Issue{RepoID: 3, Index: 1, ID: 6}

	unittest.AssertNotExistsBean(t, commentBean)
	unittest.AssertNotExistsBean(t, commentBean2)
	unittest.AssertNotExistsBean(t, issueBean, unittest.Cond("is_closed = ?", true))
	require.NoError(t, UpdateIssuesCommit(db.DefaultContext, user, repo, pushCommits, repo.DefaultBranch))
	unittest.AssertNotExistsBean(t, commentBean)
	unittest.AssertNotExistsBean(t, commentBean2)
	unittest.AssertNotExistsBean(t, issueBean, unittest.Cond("is_closed = ?", true))
	unittest.CheckConsistencyFor(t, &activities_model.Action{})
}

func TestUpdateIssuesCommit_Fork(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		message   string
		issueID   int64
		wasClosed bool
		closed    bool
	}{
		{name: "parent issue stays open", message: "closes user2/repo1#1", issueID: 1},
		{name: "parent issue stays closed", message: "reopens user2/repo1#4", issueID: 5, wasClosed: true, closed: true},
		{name: "fork issue closes", message: "closes #2", issueID: 7, closed: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
			repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
			repo.IsFork = true
			repo.ForkID = 1

			commit := &repository.PushCommit{Sha1: "abcdef1", Message: testCase.message}
			issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: testCase.issueID})
			require.Equal(t, testCase.wasClosed, issue.IsClosed)

			require.NoError(t, UpdateIssuesCommit(db.DefaultContext, user, repo, []*repository.PushCommit{commit}, repo.DefaultBranch))

			issue = unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: testCase.issueID})
			require.Equal(t, testCase.closed, issue.IsClosed)
			unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{
				Type:      issues_model.CommentTypeCommitRef,
				CommitSHA: commit.Sha1,
				PosterID:  user.ID,
				IssueID:   issue.ID,
			})
		})
	}
}
