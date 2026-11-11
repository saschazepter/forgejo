// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"testing"
	"time"

	issues_model "forgejo.org/models/issues"
	unit_model "forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	"forgejo.org/modules/git"
	"forgejo.org/modules/json"
	"forgejo.org/modules/setting"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPullDiff_CompletePRDiff(t *testing.T) {
	doTestPRDiff(t, "/user2/commitsonpr/pulls/1/files", []string{"test1.txt", "test10.txt", "test2.txt", "test3.txt", "test4.txt", "test5.txt", "test6.txt", "test7.txt", "test8.txt", "test9.txt"}, true)
}

func TestPullDiff_CompletePRDiffRename(t *testing.T) {
	defer unittest.OverrideFixtures("tests/integration/fixtures/TestPullDiff_CompletePRDiffRename")()
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	for _, url := range []string{"/user2/commitsonpr/pulls/2/files", "user2/commitsonpr/commit/19dad474fe6ea2df3584d62ab570b6bd55e8cb36"} {
		req := NewRequest(t, "GET", url)
		session.MakeRequest(t, req, http.StatusOK)

		resp := session.MakeRequest(t, req, http.StatusOK)
		assert.Contains(t, resp.Body.String(), "test1.txt → test0.txt")
	}

	req := NewRequest(t, "GET", "/api/v1/repos/user2/commitsonpr/pulls/2/files")
	resp := session.MakeRequest(t, req, http.StatusOK)
	var jsonMap []struct {
		Status           string `json:"status"`
		Filename         string `json:"filename"`
		PreviousFilename string `json:"previous_filename"`
	}

	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &jsonMap))

	assert.Equal(t, "renamed", jsonMap[0].Status)
	assert.Equal(t, "test0.txt", jsonMap[0].Filename)
	assert.Equal(t, "test1.txt", jsonMap[0].PreviousFilename)
}

func TestPullDiff_PaginatesPRDiff(t *testing.T) {
	defer func(old int) {
		setting.UI.DiffPagingNum = old
	}(setting.UI.DiffPagingNum)

	setting.UI.DiffPagingNum = 5

	doc := doTestPRDiff(t, "/user2/commitsonpr/pulls/1/files", []string{"test1.txt", "test10.txt", "test2.txt", "test3.txt", "test4.txt"}, true)

	diffShowMoreFiles := doc.doc.Find("#diff-show-more-files")
	assert.Equal(t, 1, diffShowMoreFiles.Length())

	loadMoreFilesHref, exists := diffShowMoreFiles.First().Attr("data-href")
	assert.True(t, exists)

	doc = doTestPRDiff(t, "/user2/commitsonpr/pulls/1/files"+loadMoreFilesHref, []string{"test5.txt", "test6.txt", "test7.txt", "test8.txt", "test9.txt"}, true)
	diffShowMoreFiles = doc.doc.Find("#diff-show-more-files")
	assert.Equal(t, 0, diffShowMoreFiles.Length())
}

func TestPullDiff_SingleCommitPRDiff(t *testing.T) {
	doTestPRDiff(t, "/user2/commitsonpr/pulls/1/commits/c5626fc9eff57eb1bb7b796b01d4d0f2f3f792a2", []string{"test3.txt"}, true)
}

func TestPullDiff_CommitRangePRDiff(t *testing.T) {
	doTestPRDiff(t, "/user2/commitsonpr/pulls/1/files/4ca8bcaf27e28504df7bf996819665986b01c847..23576dd018294e476c06e569b6b0f170d0558705", []string{"test2.txt", "test3.txt", "test4.txt"}, true)
}

func TestPullDiff_StartingFromBaseToCommitPRDiff(t *testing.T) {
	doTestPRDiff(t, "/user2/commitsonpr/pulls/1/files/c5626fc9eff57eb1bb7b796b01d4d0f2f3f792a2", []string{"test1.txt", "test2.txt", "test3.txt"}, true)
}

func doTestPRDiff(t *testing.T, prDiffURL string, expectedFilenames []string, editable bool) *HTMLDoc {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	req := NewRequest(t, "GET", "/user2/commitsonpr/pulls")
	session.MakeRequest(t, req, http.StatusOK)

	// Get the given PR diff url
	req = NewRequest(t, "GET", prDiffURL)
	resp := session.MakeRequest(t, req, http.StatusOK)
	doc := NewHTMLParser(t, resp.Body)

	// Assert all files are visible, only search inside the diff-file-box
	// otherwise the "load more" btn will be selected aswell
	fileContents := doc.doc.Find("#diff-file-boxes").Find(".file-content")

	numberOfFiles := fileContents.Length()

	assert.Equal(t, len(expectedFilenames), numberOfFiles)

	fileContents.Each(func(i int, s *goquery.Selection) {
		filename, _ := s.Attr("data-old-filename")
		assert.Equal(t, expectedFilenames[i], filename)
		doc.AssertElement(t, "h4.diff-file-header a.button[href=\"/user2/commitsonpr/_edit/branch1/"+filename+"\"]", editable)
	})

	return doc
}

func TestPullDiff_AGitNotEditable(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		// Create temporary repository.
		repo := forgery.CreateRepository(t, nil, &forgery.CreateRepositoryOptions{
			Files: forgery.FilesInit{},
		})
		forgery.EnableRepoUnits(t, repo, unit_model.TypePullRequests)

		user := repo.Owner
		session := loginUser(t, user.Name)

		clone := func(t *testing.T, clone string) string {
			t.Helper()

			dstPath := t.TempDir()
			cloneURL, _ := url.Parse(clone)
			cloneURL.User = url.UserPassword(user.Name, userPassword)
			require.NoError(t, git.CloneWithArgs(t.Context(), nil, cloneURL.String(), dstPath, git.CloneRepoOptions{}))
			doGitSetRemoteURL(dstPath, "origin", cloneURL)(t)

			return dstPath
		}

		firstCommit := func(t *testing.T, dstPath string) {
			t.Helper()

			require.NoError(t, os.WriteFile(path.Join(dstPath, "README.md"), []byte("## test content"), 0o600))
			require.NoError(t, git.AddChanges(dstPath, true))
			require.NoError(t, git.CommitChanges(dstPath, git.CommitChangesOptions{
				Committer: &git.Signature{
					Email: "user2@example.com",
					Name:  "user2",
					When:  time.Now(),
				},
				Author: &git.Signature{
					Email: "user2@example.com",
					Name:  "user2",
					When:  time.Now(),
				},
				Message: "Add README.",
			}))
		}
		dstPath := clone(t, fmt.Sprintf("%s%s.git", u.String(), repo.FullName()))

		// Create first commit.
		firstCommit(t, dstPath)

		// Create agit PR.
		require.NoError(t, git.NewCommand(t.Context(), "push", "origin", "HEAD:refs/for/main", "-o", "topic=agit-pr").Run(&git.RunOpts{Dir: dstPath}))

		pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{Index: 1, BaseRepoID: repo.ID})
		assert.Equal(t, issues_model.PullRequestFlowAGit, pr.Flow)

		resp := session.MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/%s/pulls/%d/files", repo.FullName(), pr.Index)), http.StatusOK)

		doc := NewHTMLParser(t, resp.Body)
		// There is no edit button on any changed file
		doc.AssertElement(t, "h4.diff-file-header a.button[href^=\"/"+repo.FullName()+"/_edit\"]", false)
	})
}
