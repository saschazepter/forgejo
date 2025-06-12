// Copyright 2019 The Gitea Authors. All rights reserved.
// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forgejo.org/models/asymkey"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	files_service "forgejo.org/services/repository/files"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getCreateRepoFilesOptions(repo *repo_model.Repository) *files_service.ChangeRepoFilesOptions {
	return &files_service.ChangeRepoFilesOptions{
		Files: []*files_service.ChangeRepoFile{
			{
				Operation:     "create",
				TreePath:      "new/file.txt",
				ContentReader: strings.NewReader("This is a NEW file"),
			},
		},
		OldBranch: repo.DefaultBranch,
		NewBranch: repo.DefaultBranch,
		Message:   "Creates new/file.txt",
		Author:    nil,
		Committer: nil,
	}
}

func getUpdateRepoFilesOptions(repo *repo_model.Repository) *files_service.ChangeRepoFilesOptions {
	return &files_service.ChangeRepoFilesOptions{
		Files: []*files_service.ChangeRepoFile{
			{
				Operation:     "update",
				TreePath:      "README.md",
				SHA:           "4b4851ad51df6a7d9f25c979345979eaeb5b349f",
				ContentReader: strings.NewReader("This is UPDATED content for the README file"),
			},
		},
		OldBranch: repo.DefaultBranch,
		NewBranch: repo.DefaultBranch,
		Message:   "Updates README.md",
		Author:    nil,
		Committer: nil,
	}
}

func getDeleteRepoFilesOptions(repo *repo_model.Repository) *files_service.ChangeRepoFilesOptions {
	return &files_service.ChangeRepoFilesOptions{
		Files: []*files_service.ChangeRepoFile{
			{
				Operation: "delete",
				TreePath:  "README_new.md",
				SHA:       "dbf8d00e022e05b7e5cf7e535de857de57925647",
			},
		},
		LastCommitID: "",
		OldBranch:    repo.DefaultBranch,
		NewBranch:    repo.DefaultBranch,
		Message:      "Deletes README.md",
		Author: &files_service.IdentityOptions{
			Name:  "Bob Smith",
			Email: "bob@smith.com",
		},
		Committer: nil,
	}
}

func getExpectedFileResponseForRepofilesDelete() *api.FileResponse {
	// Just returns fields that don't change, i.e. fields with commit SHAs and dates can't be determined
	return &api.FileResponse{
		Content: nil,
		Commit: &api.FileCommitResponse{
			Author: &api.CommitUser{
				Identity: api.Identity{
					Name:  "Bob Smith",
					Email: "bob@smith.com",
				},
			},
			Committer: &api.CommitUser{
				Identity: api.Identity{
					Name:  "Bob Smith",
					Email: "bob@smith.com",
				},
			},
			Message: "Deletes README.md\n",
		},
		Verification: &api.PayloadCommitVerification{
			Verified:  false,
			Reason:    asymkey.NotSigned,
			Signature: "",
			Payload:   "",
		},
	}
}

func getExpectedFileResponseForRepofilesCreate(commitID, lastCommitSHA string, lastCommitWhen time.Time) *api.FileResponse {
	treePath := "new/file.txt"
	encoding := "base64"
	content := "VGhpcyBpcyBhIE5FVyBmaWxl"
	selfURL := setting.AppURL + "api/v1/repos/user2/repo1/contents/" + treePath + "?ref=master"
	htmlURL := setting.AppURL + "user2/repo1/src/branch/master/" + treePath
	gitURL := setting.AppURL + "api/v1/repos/user2/repo1/git/blobs/103ff9234cefeee5ec5361d22b49fbb04d385885"
	downloadURL := setting.AppURL + "user2/repo1/raw/branch/master/" + treePath
	return &api.FileResponse{
		Content: &api.ContentsResponse{
			Name:           filepath.Base(treePath),
			Path:           treePath,
			SHA:            "103ff9234cefeee5ec5361d22b49fbb04d385885",
			LastCommitSHA:  lastCommitSHA,
			LastCommitWhen: lastCommitWhen,
			Type:           "file",
			Size:           18,
			Encoding:       &encoding,
			Content:        &content,
			URL:            &selfURL,
			HTMLURL:        &htmlURL,
			GitURL:         &gitURL,
			DownloadURL:    &downloadURL,
			Links: &api.FileLinksResponse{
				Self:    &selfURL,
				GitURL:  &gitURL,
				HTMLURL: &htmlURL,
			},
		},
		Commit: &api.FileCommitResponse{
			CommitMeta: api.CommitMeta{
				URL: setting.AppURL + "api/v1/repos/user2/repo1/git/commits/" + commitID,
				SHA: commitID,
			},
			HTMLURL: setting.AppURL + "user2/repo1/commit/" + commitID,
			Author: &api.CommitUser{
				Identity: api.Identity{
					Name:  "User Two",
					Email: "user2@noreply.example.org",
				},
				Date: time.Now().UTC().Format(time.RFC3339),
			},
			Committer: &api.CommitUser{
				Identity: api.Identity{
					Name:  "User Two",
					Email: "user2@noreply.example.org",
				},
				Date: time.Now().UTC().Format(time.RFC3339),
			},
			Parents: []*api.CommitMeta{
				{
					URL: setting.AppURL + "api/v1/repos/user2/repo1/git/commits/65f1bf27bc3bf70f64657658635e66094edbcb4d",
					SHA: "65f1bf27bc3bf70f64657658635e66094edbcb4d",
				},
			},
			Message: "Updates README.md\n",
			Tree: &api.CommitMeta{
				URL: setting.AppURL + "api/v1/repos/user2/repo1/git/trees/f93e3a1a1525fb5b91020da86e44810c87a2d7bc",
				SHA: "f93e3a1a1525fb5b91020git dda86e44810c87a2d7bc",
			},
		},
		Verification: &api.PayloadCommitVerification{
			Verified:  false,
			Reason:    asymkey.NotSigned,
			Signature: "",
			Payload:   "",
		},
	}
}

func getExpectedFileResponseForRepofilesUpdate(commitID, filename, lastCommitSHA string, lastCommitWhen time.Time) *api.FileResponse {
	encoding := "base64"
	content := "VGhpcyBpcyBVUERBVEVEIGNvbnRlbnQgZm9yIHRoZSBSRUFETUUgZmlsZQ=="
	selfURL := setting.AppURL + "api/v1/repos/user2/repo1/contents/" + filename + "?ref=master"
	htmlURL := setting.AppURL + "user2/repo1/src/branch/master/" + filename
	gitURL := setting.AppURL + "api/v1/repos/user2/repo1/git/blobs/dbf8d00e022e05b7e5cf7e535de857de57925647"
	downloadURL := setting.AppURL + "user2/repo1/raw/branch/master/" + filename
	return &api.FileResponse{
		Content: &api.ContentsResponse{
			Name:           filename,
			Path:           filename,
			SHA:            "dbf8d00e022e05b7e5cf7e535de857de57925647",
			LastCommitSHA:  lastCommitSHA,
			LastCommitWhen: lastCommitWhen,
			Type:           "file",
			Size:           43,
			Encoding:       &encoding,
			Content:        &content,
			URL:            &selfURL,
			HTMLURL:        &htmlURL,
			GitURL:         &gitURL,
			DownloadURL:    &downloadURL,
			Links: &api.FileLinksResponse{
				Self:    &selfURL,
				GitURL:  &gitURL,
				HTMLURL: &htmlURL,
			},
		},
		Commit: &api.FileCommitResponse{
			CommitMeta: api.CommitMeta{
				URL: setting.AppURL + "api/v1/repos/user2/repo1/git/commits/" + commitID,
				SHA: commitID,
			},
			HTMLURL: setting.AppURL + "user2/repo1/commit/" + commitID,
			Author: &api.CommitUser{
				Identity: api.Identity{
					Name:  "User Two",
					Email: "user2@noreply.example.org",
				},
				Date: time.Now().UTC().Format(time.RFC3339),
			},
			Committer: &api.CommitUser{
				Identity: api.Identity{
					Name:  "User Two",
					Email: "user2@noreply.example.org",
				},
				Date: time.Now().UTC().Format(time.RFC3339),
			},
			Parents: []*api.CommitMeta{
				{
					URL: setting.AppURL + "api/v1/repos/user2/repo1/git/commits/65f1bf27bc3bf70f64657658635e66094edbcb4d",
					SHA: "65f1bf27bc3bf70f64657658635e66094edbcb4d",
				},
			},
			Message: "Updates README.md\n",
			Tree: &api.CommitMeta{
				URL: setting.AppURL + "api/v1/repos/user2/repo1/git/trees/f93e3a1a1525fb5b91020da86e44810c87a2d7bc",
				SHA: "f93e3a1a1525fb5b91020da86e44810c87a2d7bc",
			},
		},
		Verification: &api.PayloadCommitVerification{
			Verified:  false,
			Reason:    asymkey.NotSigned,
			Signature: "",
			Payload:   "",
		},
	}
}

func TestChangeRepoFiles(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, u *url.URL) {
		doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

		gitRepo, err := gitrepo.OpenRepository(git.DefaultContext, repo)
		require.NoError(t, err)
		defer gitRepo.Close()

		t.Run("Create", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			opts := getCreateRepoFilesOptions(repo)
			filesResponse, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, doer, opts)
			require.NoError(t, err)

			commitID, err := gitRepo.GetBranchCommitID(opts.NewBranch)
			require.NoError(t, err)
			lastCommit, err := gitRepo.GetCommitByPath("new/file.txt")
			require.NoError(t, err)
			expectedFileResponse := getExpectedFileResponseForRepofilesCreate(commitID, lastCommit.ID.String(), lastCommit.Committer.When)
			assert.Equal(t, expectedFileResponse.Content, filesResponse.Files[0])
			assert.Equal(t, expectedFileResponse.Commit.SHA, filesResponse.Commit.SHA)
			assert.Equal(t, expectedFileResponse.Commit.HTMLURL, filesResponse.Commit.HTMLURL)
			assert.Equal(t, expectedFileResponse.Commit.Author.Email, filesResponse.Commit.Author.Email)
			assert.Equal(t, expectedFileResponse.Commit.Author.Name, filesResponse.Commit.Author.Name)
		})

		t.Run("Update", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			opts := getUpdateRepoFilesOptions(repo)
			filesResponse, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, doer, opts)
			require.NoError(t, err)

			commit, err := gitRepo.GetBranchCommit(opts.NewBranch)
			require.NoError(t, err)
			lastCommit, err := commit.GetCommitByPath(opts.Files[0].TreePath)
			require.NoError(t, err)
			expectedFileResponse := getExpectedFileResponseForRepofilesUpdate(commit.ID.String(), opts.Files[0].TreePath, lastCommit.ID.String(), lastCommit.Committer.When)
			assert.Equal(t, expectedFileResponse.Content, filesResponse.Files[0])
			assert.Equal(t, expectedFileResponse.Commit.SHA, filesResponse.Commit.SHA)
			assert.Equal(t, expectedFileResponse.Commit.HTMLURL, filesResponse.Commit.HTMLURL)
			assert.Equal(t, expectedFileResponse.Commit.Author.Email, filesResponse.Commit.Author.Email)
			assert.Equal(t, expectedFileResponse.Commit.Author.Name, filesResponse.Commit.Author.Name)
		})

		t.Run("Update with commit ID (without blob sha)", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			opts := getUpdateRepoFilesOptions(repo)

			commit, err := gitRepo.GetBranchCommit(opts.NewBranch)
			require.NoError(t, err)

			opts.Files[0].SHA = ""
			opts.LastCommitID = commit.ID.String()
			filesResponse, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, doer, opts)
			require.NoError(t, err)

			commit, err = gitRepo.GetBranchCommit(opts.NewBranch)
			require.NoError(t, err)
			lastCommit, err := commit.GetCommitByPath(opts.Files[0].TreePath)
			require.NoError(t, err)
			expectedFileResponse := getExpectedFileResponseForRepofilesUpdate(commit.ID.String(), opts.Files[0].TreePath, lastCommit.ID.String(), lastCommit.Committer.When)
			assert.Equal(t, expectedFileResponse.Content, filesResponse.Files[0])
			assert.Equal(t, expectedFileResponse.Commit.SHA, filesResponse.Commit.SHA)
			assert.Equal(t, expectedFileResponse.Commit.HTMLURL, filesResponse.Commit.HTMLURL)
			assert.Equal(t, expectedFileResponse.Commit.Author.Email, filesResponse.Commit.Author.Email)
			assert.Equal(t, expectedFileResponse.Commit.Author.Name, filesResponse.Commit.Author.Name)
		})

		t.Run("Update and move", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			opts := getUpdateRepoFilesOptions(repo)
			opts.Files[0].SHA = "dbf8d00e022e05b7e5cf7e535de857de57925647"
			opts.Files[0].FromTreePath = "README.md"
			opts.Files[0].TreePath = "README_new.md" // new file name, README_new.md
			filesResponse, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, doer, opts)
			require.NoError(t, err)

			commit, err := gitRepo.GetBranchCommit(opts.NewBranch)
			require.NoError(t, err)
			lastCommit, err := commit.GetCommitByPath(opts.Files[0].TreePath)
			require.NoError(t, err)
			expectedFileResponse := getExpectedFileResponseForRepofilesUpdate(commit.ID.String(), opts.Files[0].TreePath, lastCommit.ID.String(), lastCommit.Committer.When)

			// assert that the old file no longer exists in the last commit of the branch
			fromEntry, err := commit.GetTreeEntryByPath(opts.Files[0].FromTreePath)
			switch err.(type) {
			case git.ErrNotExist:
				// correct, continue
			default:
				t.Fatalf("expected git.ErrNotExist, got:%v", err)
			}
			toEntry, err := commit.GetTreeEntryByPath(opts.Files[0].TreePath)
			require.NoError(t, err)
			assert.Nil(t, fromEntry)  // Should no longer exist here
			assert.NotNil(t, toEntry) // Should exist here
			// assert SHA has remained the same but paths use the new file name
			assert.Equal(t, expectedFileResponse.Content.SHA, filesResponse.Files[0].SHA)
			assert.Equal(t, expectedFileResponse.Content.Name, filesResponse.Files[0].Name)
			assert.Equal(t, expectedFileResponse.Content.Path, filesResponse.Files[0].Path)
			assert.Equal(t, expectedFileResponse.Content.URL, filesResponse.Files[0].URL)
			assert.Equal(t, expectedFileResponse.Commit.SHA, filesResponse.Commit.SHA)
			assert.Equal(t, expectedFileResponse.Commit.HTMLURL, filesResponse.Commit.HTMLURL)
		})

		t.Run("Change without branch names", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			opts := getUpdateRepoFilesOptions(repo)
			opts.OldBranch = ""
			opts.NewBranch = ""
			opts.Files[0].TreePath = "README_new.md"
			opts.Files[0].SHA = "dbf8d00e022e05b7e5cf7e535de857de57925647"

			filesResponse, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, doer, opts)
			require.NoError(t, err)

			commit, _ := gitRepo.GetBranchCommit(repo.DefaultBranch)
			lastCommit, _ := commit.GetCommitByPath(opts.Files[0].TreePath)
			expectedFileResponse := getExpectedFileResponseForRepofilesUpdate(commit.ID.String(), opts.Files[0].TreePath, lastCommit.ID.String(), lastCommit.Committer.When)
			assert.Equal(t, expectedFileResponse.Content, filesResponse.Files[0])
		})

		t.Run("Delete files", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			opts := getDeleteRepoFilesOptions(repo)

			filesResponse, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, doer, opts)
			require.NoError(t, err)
			expectedFileResponse := getExpectedFileResponseForRepofilesDelete()
			assert.NotNil(t, filesResponse)
			assert.Nil(t, filesResponse.Files[0])
			assert.Equal(t, expectedFileResponse.Commit.Message, filesResponse.Commit.Message)
			assert.Equal(t, expectedFileResponse.Commit.Author.Identity, filesResponse.Commit.Author.Identity)
			assert.Equal(t, expectedFileResponse.Commit.Committer.Identity, filesResponse.Commit.Committer.Identity)
			assert.Equal(t, expectedFileResponse.Verification, filesResponse.Verification)

			filesResponse, err = files_service.ChangeRepoFiles(git.DefaultContext, repo, doer, opts)
			assert.Nil(t, filesResponse)
			expectedError := "repository file does not exist [path: " + opts.Files[0].TreePath + "]"
			assert.EqualError(t, err, expectedError)
		})

		t.Run("Delete without branch name", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			opts := getDeleteRepoFilesOptions(repo)
			opts.OldBranch = ""
			opts.NewBranch = ""
			opts.Files[0].SHA = "103ff9234cefeee5ec5361d22b49fbb04d385885"
			opts.Files[0].TreePath = "new/file.txt"

			filesResponse, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, doer, opts)
			require.NoError(t, err)
			expectedFileResponse := getExpectedFileResponseForRepofilesDelete()
			assert.NotNil(t, filesResponse)
			assert.Nil(t, filesResponse.Files[0])
			assert.Equal(t, expectedFileResponse.Commit.Message, filesResponse.Commit.Message)
			assert.Equal(t, expectedFileResponse.Commit.Author.Identity, filesResponse.Commit.Author.Identity)
			assert.Equal(t, expectedFileResponse.Commit.Committer.Identity, filesResponse.Commit.Committer.Identity)
			assert.Equal(t, expectedFileResponse.Verification, filesResponse.Verification)
		})
	})
}

func TestChangeRepoFilesErrors(t *testing.T) {
	// setup
	onGiteaRun(t, func(t *testing.T, u *url.URL) {
		doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

		t.Run("bad branch", func(t *testing.T) {
			opts := getUpdateRepoFilesOptions(repo)
			opts.OldBranch = "bad_branch"
			filesResponse, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, doer, opts)
			require.Error(t, err)
			assert.Nil(t, filesResponse)
			expectedError := "branch does not exist [name: " + opts.OldBranch + "]"
			assert.EqualError(t, err, expectedError)
		})

		t.Run("bad SHA", func(t *testing.T) {
			opts := getUpdateRepoFilesOptions(repo)
			origSHA := opts.Files[0].SHA
			opts.Files[0].SHA = "bad_sha"
			filesResponse, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, doer, opts)
			assert.Nil(t, filesResponse)
			require.Error(t, err)
			expectedError := "sha does not match [given: " + opts.Files[0].SHA + ", expected: " + origSHA + "]"
			assert.EqualError(t, err, expectedError)
		})

		t.Run("missing SHA", func(t *testing.T) {
			opts := getUpdateRepoFilesOptions(repo)
			opts.Files[0].SHA = ""
			filesResponse, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, doer, opts)
			assert.Nil(t, filesResponse)
			require.Error(t, err)
			expectedError := "a SHA or commit ID must be provided when updating a file"
			assert.EqualError(t, err, expectedError)
		})

		t.Run("bad last commit ID", func(t *testing.T) {
			opts := getUpdateRepoFilesOptions(repo)
			opts.LastCommitID = "bad"
			filesResponse, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, doer, opts)
			assert.Nil(t, filesResponse)
			require.Error(t, err)
			expectedError := "ConvertToSHA1: Invalid last commit ID: object does not exist [id: bad, rel_path: ]"
			assert.EqualError(t, err, expectedError)
		})

		t.Run("new branch already exists", func(t *testing.T) {
			opts := getUpdateRepoFilesOptions(repo)
			opts.NewBranch = "develop"
			filesResponse, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, doer, opts)
			assert.Nil(t, filesResponse)
			require.Error(t, err)
			expectedError := "branch already exists [name: " + opts.NewBranch + "]"
			assert.EqualError(t, err, expectedError)
		})

		t.Run("treePath is empty:", func(t *testing.T) {
			opts := getUpdateRepoFilesOptions(repo)
			opts.Files[0].TreePath = ""
			filesResponse, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, doer, opts)
			assert.Nil(t, filesResponse)
			require.Error(t, err)
			expectedError := "path contains a malformed path component [path: ]"
			assert.EqualError(t, err, expectedError)
		})

		t.Run("treePath is a git directory:", func(t *testing.T) {
			opts := getUpdateRepoFilesOptions(repo)
			opts.Files[0].TreePath = ".git"
			filesResponse, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, doer, opts)
			assert.Nil(t, filesResponse)
			require.Error(t, err)
			expectedError := "path contains a malformed path component [path: " + opts.Files[0].TreePath + "]"
			assert.EqualError(t, err, expectedError)
		})

		t.Run("create file that already exists", func(t *testing.T) {
			opts := getCreateRepoFilesOptions(repo)
			opts.Files[0].TreePath = "README.md" // already exists
			fileResponse, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, doer, opts)
			assert.Nil(t, fileResponse)
			require.Error(t, err)
			expectedError := "repository file already exists [path: " + opts.Files[0].TreePath + "]"
			assert.EqualError(t, err, expectedError)
		})
	})
}
