// Copyright 2021 The Gitea Authors. All rights reserved.
// Copyright 2024 The Forgejo Authors c/o Codeberg e.V.. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	unit_model "forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	repo_service "forgejo.org/services/repository"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompareTag(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")
	req := NewRequest(t, "GET", "/user2/repo1/compare/v1.1...master")
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)
	selection := htmlDoc.doc.Find(".choose.branch .filter.dropdown")
	// A dropdown for both base and head.
	assert.Lenf(t, selection.Nodes, 2, "The template has changed")

	req = NewRequest(t, "GET", "/user2/repo1/compare/invalid")
	resp = session.MakeRequest(t, req, http.StatusNotFound)
	assert.NotContains(t, resp.Body.String(), ">500<", "expect 404 page not 500")
}

// Compare with inferred default branch (master)
func TestCompareDefault(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")
	req := NewRequest(t, "GET", "/user2/repo1/compare/v1.1")
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)
	selection := htmlDoc.doc.Find(".choose.branch .filter.dropdown")
	assert.Lenf(t, selection.Nodes, 2, "The template has changed")
}

// Ensure the comparison matches what we expect
func inspectCompare(t *testing.T, htmlDoc *HTMLDoc, diffCount int, diffChanges []string) {
	selection := htmlDoc.doc.Find("#diff-file-boxes").Children()

	assert.Lenf(t, selection.Nodes, diffCount, "Expected %v diffed files, found: %v", diffCount, len(selection.Nodes))

	for _, diffChange := range diffChanges {
		selection = htmlDoc.doc.Find(fmt.Sprintf("[data-new-filename=\"%s\"]", diffChange))
		assert.Lenf(t, selection.Nodes, 1, "Expected 1 match for [data-new-filename=\"%s\"], found: %v", diffChange, len(selection.Nodes))
	}
}

func TestComparePatchAndDiffMenuEntries(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")
	req := NewRequest(t, "GET", "/user2/repo-release/compare/v1.0...v2.0")
	expectedCompare := "a8a700e8c6...user2/repo-release:7197b56fdc"
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)
	downloadOptions := htmlDoc.doc.Find("a.item[download]")
	var patchDownloadEntryPresent bool
	var diffDownloadEntryPresent bool
	downloadOptions.Each(func(idx int, c *goquery.Selection) {
		value, exists := c.Attr("download")
		if exists && strings.HasSuffix(value, ".patch") {
			assert.Equal(t, fmt.Sprintf("%s.patch", expectedCompare), value)
			patchDownloadEntryPresent = true
		}

		if exists && strings.HasSuffix(value, ".diff") {
			assert.Equal(t, fmt.Sprintf("%s.diff", expectedCompare), value)
			diffDownloadEntryPresent = true
		}
	})

	assert.True(t, patchDownloadEntryPresent, "Patch file download entry should be present")
	assert.True(t, diffDownloadEntryPresent, "Diff file download entry should be present")

	t.Run("PatchDownload", func(t *testing.T) {
		req := NewRequestf(t, "GET", "/user2/repo-release/compare/%s.patch", expectedCompare)
		attendedResponse := `From 4380f99290b2b3922733ff82c57afad915ace907 Mon Sep 17 00:00:00 2001
From: user1 <address1@example.com>
Date: Mon, 17 Apr 2023 14:39:35 +0200
Subject: [PATCH 1/3] feature v2

---
 feature | 0
 1 file changed, 0 insertions(+), 0 deletions(-)
 create mode 100644 feature

diff --git a/feature b/feature
new file mode 100644
index 0000000..e69de29

From 79f9d88f1b054d650f88da0bd658e21f7b0cf6ec Mon Sep 17 00:00:00 2001
From: user1 <address1@example.com>
Date: Mon, 17 Apr 2023 14:38:53 +0200
Subject: [PATCH 2/3] bugfix

---
 bugfix | 0
 1 file changed, 0 insertions(+), 0 deletions(-)
 create mode 100644 bugfix

diff --git a/bugfix b/bugfix
new file mode 100644
index 0000000..e69de29

From 7197b56fdc75b453f47c9110938cb46a303579fd Mon Sep 17 00:00:00 2001
From: user1 <address1@example.com>
Date: Mon, 17 Apr 2023 14:42:34 +0200
Subject: [PATCH 3/3] readme: v2

---
 README.md | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)

diff --git a/README.md b/README.md
index 6dfe48a..bc7068d 100644
--- a/README.md
+++ b/README.md
@@ -1,3 +1,3 @@
 # Releases test repo
 
-With a v1.0
+With a v1.0 and a v2.0
`

		resp := session.MakeRequest(t, req, http.StatusOK)
		assert.Equal(t, attendedResponse, resp.Body.String())
	})

	t.Run("DiffDownload", func(t *testing.T) {
		req := NewRequestf(t, "GET", "/user2/repo-release/compare/%s.diff", expectedCompare)
		attendedResponse := `diff --git a/README.md b/README.md
index 6dfe48a..bc7068d 100644
--- a/README.md
+++ b/README.md
@@ -1,3 +1,3 @@
 # Releases test repo
 
-With a v1.0
+With a v1.0 and a v2.0
diff --git a/bugfix b/bugfix
new file mode 100644
index 0000000..e69de29
diff --git a/feature b/feature
new file mode 100644
index 0000000..e69de29
`

		resp := session.MakeRequest(t, req, http.StatusOK)
		assert.Equal(t, attendedResponse, resp.Body.String())
	})
}

func TestComparePatchAndDiffMenuEntriesCrossRepository(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")
	req := NewRequest(t, "GET", "/user2/repo-release/compare/v1.0...user2/repo1:v1.1")
	expectedCompare := "a8a700e8c6...user2/repo1:65f1bf27bc"
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)
	downloadOptions := htmlDoc.doc.Find("a.item[download]")
	var patchDownloadEntryPresent bool
	var diffDownloadEntryPresent bool
	downloadOptions.Each(func(idx int, c *goquery.Selection) {
		value, exists := c.Attr("download")
		if exists && strings.HasSuffix(value, ".patch") {
			assert.Equal(t, fmt.Sprintf("%s.patch", expectedCompare), value)
			patchDownloadEntryPresent = true
		}

		if exists && strings.HasSuffix(value, ".diff") {
			assert.Equal(t, fmt.Sprintf("%s.diff", expectedCompare), value)
			diffDownloadEntryPresent = true
		}
	})

	assert.True(t, patchDownloadEntryPresent, "Patch file download entry should be present")
	assert.True(t, diffDownloadEntryPresent, "Diff file download entry should be present")

	t.Run("PatchDownload", func(t *testing.T) {
		req := NewRequestf(t, "GET", "/user2/repo-release/compare/%s.patch", expectedCompare)
		resp := session.MakeRequest(t, req, http.StatusOK)
		respPatch := resp.Body.String()
		assert.True(t, strings.HasPrefix(respPatch, "From 65f1bf27bc3bf70f64657658635e66094edbcb4d Mon Sep 17 00:00:00 2001"), "must have .patch file prefix for commit 65f1bf27bc")
		assert.Contains(t, respPatch, "Subject: [PATCH 1/4] Initial commit\n\n")
		assert.Contains(t, respPatch, "Subject: [PATCH 4/4] readme: v1\n\n")
	})

	t.Run("DiffDownload", func(t *testing.T) {
		req := NewRequestf(t, "GET", "/user2/repo-release/compare/%s.diff", expectedCompare)
		resp := session.MakeRequest(t, req, http.StatusOK)
		respDiff := resp.Body.String()
		assert.True(t, strings.HasPrefix(respDiff, "diff --git a/README.md b/README.md\n"), "must have .diff file prefix")
		assert.Contains(t, respDiff, "\n--- a/README.md\n")
		assert.Contains(t, respDiff, "\n+++ b/README.md\n")
		assert.Contains(t, respDiff, "\n\\ No newline at end of file\n")
	})
}

// Git commit graph for repo20
// * 8babce9 (origin/remove-files-b) Add a dummy file
// * b67e43a Delete test.csv and link_hi
// | * cfe3b3c (origin/remove-files-a) Delete test.csv and link_hi
// |/
// * c8e31bc (origin/add-csv) Add test csv file
// * 808038d (HEAD -> master, origin/master, origin/HEAD) Added test links

func TestCompareBranches(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	// Indirect compare remove-files-b (head) with add-csv (base) branch
	//
	//	'link_hi' and 'test.csv' are deleted, 'test.txt' is added
	req := NewRequest(t, "GET", "/user2/repo20/compare/add-csv...remove-files-b")
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	diffCount := 3
	diffChanges := []string{"link_hi", "test.csv", "test.txt"}

	inspectCompare(t, htmlDoc, diffCount, diffChanges)

	// Indirect compare remove-files-b (head) with remove-files-a (base) branch
	//
	//	'link_hi' and 'test.csv' are deleted, 'test.txt' is added

	req = NewRequest(t, "GET", "/user2/repo20/compare/remove-files-a...remove-files-b")
	resp = session.MakeRequest(t, req, http.StatusOK)
	htmlDoc = NewHTMLParser(t, resp.Body)

	diffCount = 3
	diffChanges = []string{"link_hi", "test.csv", "test.txt"}

	inspectCompare(t, htmlDoc, diffCount, diffChanges)

	// Indirect compare remove-files-a (head) with remove-files-b (base) branch
	//
	//	'link_hi' and 'test.csv' are deleted

	req = NewRequest(t, "GET", "/user2/repo20/compare/remove-files-b...remove-files-a")
	resp = session.MakeRequest(t, req, http.StatusOK)
	htmlDoc = NewHTMLParser(t, resp.Body)

	diffCount = 2
	diffChanges = []string{"link_hi", "test.csv"}

	inspectCompare(t, htmlDoc, diffCount, diffChanges)

	// Direct compare remove-files-b (head) with remove-files-a (base) branch
	//
	//	'test.txt' is deleted

	req = NewRequest(t, "GET", "/user2/repo20/compare/remove-files-b..remove-files-a")
	resp = session.MakeRequest(t, req, http.StatusOK)
	htmlDoc = NewHTMLParser(t, resp.Body)

	diffCount = 1
	diffChanges = []string{"test.txt"}

	inspectCompare(t, htmlDoc, diffCount, diffChanges)

	// Branches with name 'diff' or 'patch'
	req = NewRequest(t, "GET", "/user2/repo16/compare/master..diff")
	session.MakeRequest(t, req, http.StatusOK)
	req = NewRequest(t, "GET", "/user2/repo16/compare/master..diff.patch")
	session.MakeRequest(t, req, http.StatusOK)
	req = NewRequest(t, "GET", "/user2/repo16/compare/master..patch")
	session.MakeRequest(t, req, http.StatusOK)
	req = NewRequest(t, "GET", "/user2/repo16/compare/master..patch.diff")
	session.MakeRequest(t, req, http.StatusOK)
}

func TestCompareBranchesPaginatesDiff(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer func(old int) {
		setting.UI.DiffPagingNum = old
	}(setting.UI.DiffPagingNum)

	setting.UI.DiffPagingNum = 2

	session := loginUser(t, "user2")

	// Indirect compare remove-files-b (head) with add-csv (base) branch
	//
	//	'link_hi' and 'test.csv' are deleted, 'test.txt' is added
	req := NewRequest(t, "GET", "/user2/repo20/compare/add-csv...remove-files-b")
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	// PageSize is set to 2, so only the first 2 changes should be visble initially
	diffCount := 2
	diffChanges := []string{"link_hi", "test.csv"}

	inspectCompare(t, htmlDoc, diffCount, diffChanges)

	diffShowMoreFiles := htmlDoc.doc.Find("#diff-show-more-files")
	assert.Equal(t, 1, diffShowMoreFiles.Length())

	loadMoreFilesHref, exists := diffShowMoreFiles.First().Attr("data-href")
	assert.True(t, exists)

	req = NewRequest(t, "GET", "/user2/repo20/compare/add-csv...remove-files-b"+loadMoreFilesHref)
	resp = session.MakeRequest(t, req, http.StatusOK)
	htmlDoc = NewHTMLParser(t, resp.Body)

	// When triggering the "load more request" we should only receive the missing (1) changed file
	diffCount = 1
	diffChanges = []string{"test.txt"}

	inspectCompare(t, htmlDoc, diffCount, diffChanges)

	diffShowMoreFiles = htmlDoc.doc.Find("#diff-show-more-files")
	assert.Equal(t, 0, diffShowMoreFiles.Length())
}

func TestCompareWithPRsDisabled(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		session := loginUser(t, "user1")
		testRepoFork(t, session, "user2", "repo1", "user1", "repo1")
		testCreateBranch(t, session, "user1", "repo1", "branch/master", "recent-push", http.StatusSeeOther)
		testEditFile(t, session, "user1", "repo1", "recent-push", "README.md", "Hello recently!\n")

		repo, err := repo_model.GetRepositoryByOwnerAndName(db.DefaultContext, "user1", "repo1")
		require.NoError(t, err)

		defer func() {
			// Re-enable PRs on the repo
			err := repo_service.UpdateRepositoryUnits(db.DefaultContext, repo,
				[]repo_model.RepoUnit{{
					RepoID: repo.ID,
					Type:   unit_model.TypePullRequests,
				}},
				nil)
			require.NoError(t, err)
		}()

		// Disable PRs on the repo
		err = repo_service.UpdateRepositoryUnits(db.DefaultContext, repo, nil,
			[]unit_model.Type{unit_model.TypePullRequests})
		require.NoError(t, err)

		t.Run("branch view offer comparing branches", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", "/user1/repo1/branches")
			resp := session.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)
			compareLink := htmlDoc.Find("a[href='/user1/repo1/compare/master...recent-push']")
			assert.Equal(t, "Compare branches", strings.TrimSpace(compareLink.Text()))
		})

		t.Run("compare doesn't offer local branches", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", "/user2/repo1/compare/master...user1/repo1:recent-push")
			resp := session.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)
			branches := htmlDoc.Find(".choose.branch .menu .reference-list-menu.base-branch-list .item, .choose.branch .menu .reference-list-menu.base-tag-list .item")

			expectedPrefix := "user2:"
			for i := 0; i < len(branches.Nodes); i++ {
				assert.True(t, strings.HasPrefix(branches.Eq(i).Text(), expectedPrefix))
			}
		})

		t.Run("comparing against a disabled-PR repo", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", "/user1/repo1/compare/master...recent-push")
			resp := session.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)
			assert.Equal(t, "Compare branches", strings.TrimSpace(htmlDoc.Find("h2.header").Text()))
		})
	})
}

func TestCompareCrossRepo(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		session := loginUser(t, "user1")
		testRepoFork(t, session, "user2", "repo1", "user1", "repo1-copy")
		testCreateBranch(t, session, "user1", "repo1-copy", "branch/master", "recent-push", http.StatusSeeOther)
		testEditFile(t, session, "user1", "repo1-copy", "recent-push", "README.md", "Hello recently!\n")

		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerName: "user1", Name: "repo1-copy"})

		gitRepo, err := gitrepo.OpenRepository(db.DefaultContext, repo)
		require.NoError(t, err)
		defer gitRepo.Close()

		lastCommit, err := gitRepo.GetBranchCommitID("recent-push")
		require.NoError(t, err)
		assert.NotEmpty(t, lastCommit)

		t.Run("view file button links to correct file in fork", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", "/user2/repo1/compare/master...user1/repo1-copy:recent-push")
			resp := session.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)
			htmlDoc.AssertElement(t, "a[href='/user1/repo1-copy/src/commit/"+lastCommit+"/README.md']", true)
			htmlDoc.AssertElement(t, "a[href='/user1/repo1/src/commit/"+lastCommit+"/README.md']", false)
		})
	})
}

func TestCompareCodeExpand(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		// Create a new repository, with a file that has many lines
		repo := forgery.CreateRepository(t, nil, &forgery.CreateRepositoryOptions{
			Files: forgery.MapFS{
				"docs.md": forgery.MapFile("01\n02\n03\n04\n05\n06\n07\n08\n09\n0a\n0b\n0c\n0d\n0e\n0f\n10\n11\n12\n12\n13\n14\n15\n16\n17\n18\n19\n1a\n1b\n1c\n1d\n1e\n1f\n20\n"),
			},
		})
		owner := repo.Owner

		// Fork the repository
		forker := forgery.CreateUser(t, nil)
		session := loginUser(t, forker.Name)
		testRepoFork(t, session, owner.Name, repo.Name, forker.Name, repo.Name+"-copy")
		testCreateBranch(t, session, forker.Name, repo.Name+"-copy", "branch/main", "code-expand", http.StatusSeeOther)

		// Edit the file, insert a line somewhere in the middle
		testEditFile(t, session, forker.Name, repo.Name+"-copy", "code-expand", "docs.md",
			"01\n02\n03\n04\n05\n06\n07\n08\n09\n0a\n0b\n0c\n0d\n0e\n0f\n10\n11\nHELLO WORLD!\n12\n12\n13\n14\n15\n16\n17\n18\n19\n1a\n1b\n1c\n1d\n1e\n1f\n20\n",
		)

		t.Run("code expander targets the fork", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequestf(t, "GET", "%s/%s/compare/main...%s/%s:code-expand",
				owner.Name, repo.Name, forker.Name, repo.Name+"-copy")
			resp := session.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			els := htmlDoc.Find(`button.code-expander-button[hx-get]`)

			// all the links in the comparison should be to the forked repo&branch
			assert.NotZero(t, els.Length())
			expectedPrefix := fmt.Sprintf("/%s/%s/blob_excerpt/", forker.Name, repo.Name+"-copy")
			for i := 0; i < els.Length(); i++ {
				link := els.Eq(i).AttrOr("hx-get", "")
				assert.True(t, strings.HasPrefix(link, expectedPrefix))
			}
		})

		t.Run("code expander targets the repo in a PR", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Create a pullrequest
			resp := testPullCreate(t, session, forker.Name, repo.Name+"-copy", false, "main", "code-expand", "This is a pull title")

			// Grab the URL for the PR
			url := test.RedirectURL(resp) + "/files"

			// Visit the PR's diff
			req := NewRequest(t, "GET", url)
			resp = session.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			els := htmlDoc.Find(`button.code-expander-button[hx-get]`)

			// all the links in the comparison should be to the original repo&branch
			assert.NotZero(t, els.Length())
			expectedPrefix := fmt.Sprintf("/%s/%s/blob_excerpt/", owner.Name, repo.Name)
			for i := 0; i < els.Length(); i++ {
				link := els.Eq(i).AttrOr("hx-get", "")
				assert.True(t, strings.HasPrefix(link, expectedPrefix))
			}
		})
	})
}

func TestCompareSignedIn(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		// Setup the test with a connected user
		session := loginUser(t, "user1")
		testRepoFork(t, session, "user2", "repo1", "user1", "repo1")
		testCreateBranch(t, session, "user1", "repo1", "branch/master", "recent-push", http.StatusSeeOther)
		testEditFile(t, session, "user1", "repo1", "recent-push", "README.md", "Hello recently!\n")

		newPrSelector := "button.ui.button.primary.show-form"

		t.Run("PR creation button displayed if logged in", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", "/user1/repo1/compare/master...recent-push")
			resp := session.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			// Check that the "Sign in" button doesn't show up
			htmlDoc.AssertElement(t, "a[href='/user/login?redirect_to=%2Fuser1%2Frepo1%2Fcompare%2Fmaster...recent-push']", false)

			// Check that the "New pull request" button shows up
			htmlDoc.AssertElement(t, newPrSelector, true)
		})

		t.Run("no PR creation button but display warning", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", "/user1/repo1/compare/master...recent-push")
			resp := MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			// Check that the "Sign in" button shows up
			htmlDoc.AssertElement(t, "a[href='/user/login?redirect_to=%2Fuser1%2Frepo1%2Fcompare%2Fmaster...recent-push']", true)

			// Check that the "New pull request" button doesn't show up
			htmlDoc.AssertElement(t, newPrSelector, false)
		})
	})
}
