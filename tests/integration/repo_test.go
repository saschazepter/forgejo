// Copyright 2017 The Gitea Authors. All rights reserved.
// Copyright 2023 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"
	"time"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	"forgejo.org/models/organization"
	repo_model "forgejo.org/models/repo"
	unit_model "forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	"forgejo.org/modules/translation"
	repo_service "forgejo.org/services/repository"
	files_service "forgejo.org/services/repository/files"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestViewRepo(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	req := NewRequest(t, "GET", "/user2/repo1")
	resp := session.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	noDescription := htmlDoc.doc.Find("#repo-desc").Children()
	repoTopics := htmlDoc.doc.Find("#repo-topics").Children()
	repoSummary := htmlDoc.doc.Find(".repository-summary").Children()

	assert.True(t, noDescription.HasClass("no-description"))
	assert.True(t, repoTopics.HasClass("repo-topic"))
	assert.True(t, repoSummary.HasClass("repository-menu"))

	req = NewRequest(t, "GET", "/org3/repo3")
	MakeRequest(t, req, http.StatusNotFound)

	session = loginUser(t, "user1")
	session.MakeRequest(t, req, http.StatusNotFound)
}

func testViewRepo(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	req := NewRequest(t, "GET", "/org3/repo3")
	session := loginUser(t, "user2")
	resp := session.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	files := htmlDoc.doc.Find("#repo-files-table  > TBODY > TR")

	type file struct {
		fileName   string
		commitID   string
		commitMsg  string
		commitTime string
	}

	var items []file

	files.Each(func(i int, s *goquery.Selection) {
		tds := s.Find("td")
		var f file
		tds.Each(func(i int, s *goquery.Selection) {
			if i == 0 {
				f.fileName = strings.TrimSpace(s.Text())
			} else if i == 1 {
				a := s.Find("a")
				f.commitMsg = strings.TrimSpace(a.Text())
				l, _ := a.Attr("href")
				f.commitID = path.Base(l)
			}
		})

		// convert "2017-06-14 21:54:21 +0800" to "Wed, 14 Jun 2017 13:54:21 UTC"
		htmlTimeString, _ := s.Find("relative-time").Attr("datetime")
		htmlTime, _ := time.Parse(time.RFC3339, htmlTimeString)
		f.commitTime = htmlTime.In(time.Local).Format(time.RFC1123)
		items = append(items, f)
	})

	commitT := time.Date(2017, time.June, 14, 13, 54, 21, 0, time.UTC).In(time.Local).Format(time.RFC1123)
	assert.Equal(t, []file{
		{
			fileName:   "doc",
			commitID:   "2a47ca4b614a9f5a43abbd5ad851a54a616ffee6",
			commitMsg:  "init project",
			commitTime: commitT,
		},
		{
			fileName:   "README.md",
			commitID:   "2a47ca4b614a9f5a43abbd5ad851a54a616ffee6",
			commitMsg:  "init project",
			commitTime: commitT,
		},
	}, items)
}

func TestViewRepo2(t *testing.T) {
	// no last commit cache
	testViewRepo(t)

	// enable last commit cache for all repositories
	oldCommitsCount := setting.CacheService.LastCommit.CommitsCount
	setting.CacheService.LastCommit.CommitsCount = 0
	// first view will not hit the cache
	testViewRepo(t)
	// second view will hit the cache
	testViewRepo(t)
	setting.CacheService.LastCommit.CommitsCount = oldCommitsCount
}

func TestViewRepo3(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	req := NewRequest(t, "GET", "/org3/repo3")
	session := loginUser(t, "user4")
	session.MakeRequest(t, req, http.StatusOK)
}

func TestViewRepo1CloneLinkAnonymous(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	req := NewRequest(t, "GET", "/user2/repo1")
	resp := MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	link, exists := htmlDoc.doc.Find("#repo-clone-https").Attr("data-link")
	assert.True(t, exists, "The template has changed")
	assert.Equal(t, setting.AppURL+"user2/repo1.git", link)
	_, exists = htmlDoc.doc.Find("#repo-clone-ssh").Attr("data-link")
	assert.False(t, exists)
}

func TestViewRepo1CloneLinkAuthorized(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	req := NewRequest(t, "GET", "/user2/repo1")
	resp := session.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	link, exists := htmlDoc.doc.Find("#repo-clone-https").Attr("data-link")
	assert.True(t, exists, "The template has changed")
	assert.Equal(t, setting.AppURL+"user2/repo1.git", link)
	link, exists = htmlDoc.doc.Find("#repo-clone-ssh").Attr("data-link")
	assert.True(t, exists, "The template has changed")
	sshURL := fmt.Sprintf("ssh://%s@%s:%d/user2/repo1.git", setting.SSH.User, setting.SSH.Domain, setting.SSH.Port)
	assert.Equal(t, sshURL, link)
}

func TestViewRepoWithSymlinks(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	req := NewRequest(t, "GET", "/user2/repo20.git")
	resp := session.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	files := htmlDoc.doc.Find("#repo-files-table > TBODY > TR > TD.name > SPAN.truncate")
	items := files.Map(func(i int, s *goquery.Selection) string {
		cls, _ := s.Find("SVG").Attr("class")
		file := strings.Trim(s.Find("A").Text(), " \t\n")
		return fmt.Sprintf("%s: %s", file, cls)
	})
	assert.Len(t, items, 5)
	assert.Equal(t, "a: tw-mr-2 svg octicon-file-directory-fill", items[0])
	assert.Equal(t, "link_b: tw-mr-2 svg octicon-file-directory-symlink", items[1])
	assert.Equal(t, "link_d: tw-mr-2 svg octicon-file-symlink-file", items[2])
	assert.Equal(t, "link_hi: tw-mr-2 svg octicon-file-symlink-file", items[3])
	assert.Equal(t, "link_link: tw-mr-2 svg octicon-file-symlink-file", items[4])
}

// TestViewAsRepoAdmin tests PR #2167
func TestViewAsRepoAdmin(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	for _, user := range []string{"user2", "user4"} {
		session := loginUser(t, user)

		req := NewRequest(t, "GET", "/user2/repo1.git")
		resp := session.MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)
		noDescription := htmlDoc.doc.Find("#repo-desc").Children()
		repoTopics := htmlDoc.doc.Find("#repo-topics").Children()
		repoSummary := htmlDoc.doc.Find(".repository-summary").Children()

		assert.True(t, noDescription.HasClass("no-description"))
		assert.True(t, repoTopics.HasClass("repo-topic"))
		assert.True(t, repoSummary.HasClass("repository-menu"))
	}
}

func TestRepoHTMLTitle(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	t.Run("Repository homepage", func(t *testing.T) {
		t.Run("Without description", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			htmlTitle := GetHTMLTitle(t, nil, "/user2/repo1")
			assert.Equal(t, "user2/repo1 - Forgejo: Beyond coding. We Forge.", htmlTitle)
		})
		t.Run("With description", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			htmlTitle := GetHTMLTitle(t, nil, "/user27/repo49")
			assert.Equal(t, "user27/repo49: A wonderful repository with more than just a README.md - Forgejo: Beyond coding. We Forge.", htmlTitle)
		})
	})

	t.Run("Code view", func(t *testing.T) {
		t.Run("Directory", func(t *testing.T) {
			t.Run("Default branch", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				htmlTitle := GetHTMLTitle(t, nil, "/user2/repo59/src/branch/master/deep/nesting")
				assert.Equal(t, "repo59/deep/nesting at master - user2/repo59 - Forgejo: Beyond coding. We Forge.", htmlTitle)
			})
			t.Run("Non-default branch", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				htmlTitle := GetHTMLTitle(t, nil, "/user2/repo59/src/branch/cake-recipe/deep/nesting")
				assert.Equal(t, "repo59/deep/nesting at cake-recipe - user2/repo59 - Forgejo: Beyond coding. We Forge.", htmlTitle)
			})
			t.Run("Commit", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				htmlTitle := GetHTMLTitle(t, nil, "/user2/repo59/src/commit/d8f53dfb33f6ccf4169c34970b5e747511c18beb/deep/nesting/")
				assert.Equal(t, "repo59/deep/nesting at d8f53dfb33f6ccf4169c34970b5e747511c18beb - user2/repo59 - Forgejo: Beyond coding. We Forge.", htmlTitle)
			})
			t.Run("Tag", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				htmlTitle := GetHTMLTitle(t, nil, "/user2/repo59/src/tag/v1.0/deep/nesting/")
				assert.Equal(t, "repo59/deep/nesting at v1.0 - user2/repo59 - Forgejo: Beyond coding. We Forge.", htmlTitle)
			})
		})
		t.Run("File", func(t *testing.T) {
			t.Run("Default branch", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				htmlTitle := GetHTMLTitle(t, nil, "/user2/repo59/src/branch/master/deep/nesting/folder/secret_sauce_recipe.txt")
				assert.Equal(t, "repo59/deep/nesting/folder/secret_sauce_recipe.txt at master - user2/repo59 - Forgejo: Beyond coding. We Forge.", htmlTitle)
			})
			t.Run("Non-default branch", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				htmlTitle := GetHTMLTitle(t, nil, "/user2/repo59/src/branch/cake-recipe/deep/nesting/folder/secret_sauce_recipe.txt")
				assert.Equal(t, "repo59/deep/nesting/folder/secret_sauce_recipe.txt at cake-recipe - user2/repo59 - Forgejo: Beyond coding. We Forge.", htmlTitle)
			})
			t.Run("Commit", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				htmlTitle := GetHTMLTitle(t, nil, "/user2/repo59/src/commit/d8f53dfb33f6ccf4169c34970b5e747511c18beb/deep/nesting/folder/secret_sauce_recipe.txt")
				assert.Equal(t, "repo59/deep/nesting/folder/secret_sauce_recipe.txt at d8f53dfb33f6ccf4169c34970b5e747511c18beb - user2/repo59 - Forgejo: Beyond coding. We Forge.", htmlTitle)
			})
			t.Run("Tag", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				htmlTitle := GetHTMLTitle(t, nil, "/user2/repo59/src/tag/v1.0/deep/nesting/folder/secret_sauce_recipe.txt")
				assert.Equal(t, "repo59/deep/nesting/folder/secret_sauce_recipe.txt at v1.0 - user2/repo59 - Forgejo: Beyond coding. We Forge.", htmlTitle)
			})
		})
	})

	t.Run("Issues view", func(t *testing.T) {
		t.Run("Overview page", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			htmlTitle := GetHTMLTitle(t, nil, "/user2/repo1/issues")
			assert.Equal(t, "Issues - user2/repo1 - Forgejo: Beyond coding. We Forge.", htmlTitle)
		})
		t.Run("View issue page", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			htmlTitle := GetHTMLTitle(t, nil, "/user2/repo1/issues/1")
			assert.Equal(t, "#1 - issue1 - user2/repo1 - Forgejo: Beyond coding. We Forge.", htmlTitle)
		})
	})

	t.Run("Pull requests view", func(t *testing.T) {
		t.Run("Overview page", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			htmlTitle := GetHTMLTitle(t, nil, "/user2/repo1/pulls")
			assert.Equal(t, "Pull requests - user2/repo1 - Forgejo: Beyond coding. We Forge.", htmlTitle)
		})
		t.Run("View pull request", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			htmlTitle := GetHTMLTitle(t, nil, "/user2/repo1/pulls/2")
			assert.Equal(t, "#2 - issue2 - user2/repo1 - Forgejo: Beyond coding. We Forge.", htmlTitle)
		})
	})
}

// TestViewFileInRepo repo description, topics and summary should not be displayed when viewing a file
func TestViewFileInRepo(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	req := NewRequest(t, "GET", "/user2/repo1/src/branch/master/README.md")
	resp := session.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	description := htmlDoc.doc.Find("#repo-desc")
	repoTopics := htmlDoc.doc.Find("#repo-topics")
	repoSummary := htmlDoc.doc.Find(".repository-summary")

	assert.Equal(t, 0, description.Length())
	assert.Equal(t, 0, repoTopics.Length())
	assert.Equal(t, 0, repoSummary.Length())
}

func TestViewFileInRepoRSSFeed(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	hasFileRSSFeed := func(t *testing.T, ref string) bool {
		t.Helper()

		req := NewRequestf(t, "GET", "/user2/repo1/src/%s/README.md", ref)
		resp := MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)
		fileFeed := htmlDoc.doc.Find(`a[href*="/user2/repo1/rss/"]`)

		return fileFeed.Length() != 0
	}

	t.Run("branch", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		assert.True(t, hasFileRSSFeed(t, "branch/master"))
	})

	t.Run("tag", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		assert.False(t, hasFileRSSFeed(t, "tag/v1.1"))
	})

	t.Run("commit", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		assert.False(t, hasFileRSSFeed(t, "commit/65f1bf27bc3bf70f64657658635e66094edbcb4d"))
	})
}

// TestBlameFileInRepo repo description, topics and summary should not be displayed when running blame on a file
func TestBlameFileInRepo(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	t.Run("Assert", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/blame/branch/master/README.md")
		resp := session.MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)
		description := htmlDoc.doc.Find("#repo-desc")
		repoTopics := htmlDoc.doc.Find("#repo-topics")
		repoSummary := htmlDoc.doc.Find(".repository-summary")

		assert.Equal(t, 0, description.Length())
		assert.Equal(t, 0, repoTopics.Length())
		assert.Equal(t, 0, repoSummary.Length())
	})

	t.Run("File size", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		gitRepo, err := git.OpenRepository(git.DefaultContext, repo.RepoPath())
		require.NoError(t, err)
		defer gitRepo.Close()

		commit, err := gitRepo.GetCommit("HEAD")
		require.NoError(t, err)

		blob, err := commit.GetBlobByPath("README.md")
		require.NoError(t, err)

		fileSize := blob.Size()
		require.NotZero(t, fileSize)

		t.Run("Above maximum", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			defer test.MockVariableValue(&setting.UI.MaxDisplayFileSize, fileSize)()

			req := NewRequest(t, "GET", "/user2/repo1/blame/branch/master/README.md")
			resp := session.MakeRequest(t, req, http.StatusOK)

			htmlDoc := NewHTMLParser(t, resp.Body)
			assert.Contains(t, htmlDoc.Find(".code-view").Text(), translation.NewLocale("en-US").Tr("repo.file_too_large"))
		})

		t.Run("Under maximum", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			defer test.MockVariableValue(&setting.UI.MaxDisplayFileSize, fileSize+1)()

			req := NewRequest(t, "GET", "/user2/repo1/blame/branch/master/README.md")
			resp := session.MakeRequest(t, req, http.StatusOK)

			htmlDoc := NewHTMLParser(t, resp.Body)
			assert.NotContains(t, htmlDoc.Find(".code-view").Text(), translation.NewLocale("en-US").Tr("repo.file_too_large"))
		})
	})
}

// TestViewRepoDirectory repo description, topics and summary should not be displayed when within a directory
func TestViewRepoDirectory(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	req := NewRequest(t, "GET", "/user2/repo20/src/branch/master/a")
	resp := session.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	description := htmlDoc.doc.Find("#repo-desc")
	repoTopics := htmlDoc.doc.Find("#repo-topics")
	repoSummary := htmlDoc.doc.Find(".repository-summary")

	repoFilesTable := htmlDoc.doc.Find("#repo-files-table")
	assert.NotEmpty(t, repoFilesTable.Nodes)

	assert.Zero(t, description.Length())
	assert.Zero(t, repoTopics.Length())
	assert.Zero(t, repoSummary.Length())
}

// ensure that the all the different ways to find and render a README work
func TestViewRepoDirectoryReadme(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	// there are many combinations:
	// - READMEs can be .md, .txt, or have no extension
	// - READMEs can be tagged with a language and even a country code
	// - READMEs can be stored in docs/, .gitea/, or .github/
	// - READMEs can be symlinks to other files
	// - READMEs can be broken symlinks which should not render
	//
	// this doesn't cover all possible cases, just the major branches of the code

	session := loginUser(t, "user2")

	check := func(name, url, expectedFilename, expectedReadmeType, expectedContent string) {
		t.Run(name, func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", url)
			resp := session.MakeRequest(t, req, http.StatusOK)

			htmlDoc := NewHTMLParser(t, resp.Body)
			readmeName := htmlDoc.doc.Find("h4.file-header")
			readmeContent := htmlDoc.doc.Find(".file-view") // TODO: add a id="readme" to the output to make this test more precise
			readmeType, _ := readmeContent.Attr("class")

			assert.Equal(t, expectedFilename, strings.TrimSpace(readmeName.Text()))
			assert.Contains(t, readmeType, expectedReadmeType)
			assert.Contains(t, readmeContent.Text(), expectedContent)
		})
	}

	// viewing the top level
	check("Home", "/user2/readme-test/", "README.md", "markdown", "The cake is a lie.")

	// viewing different file extensions
	check("md", "/user2/readme-test/src/branch/master/", "README.md", "markdown", "The cake is a lie.")
	check("txt", "/user2/readme-test/src/branch/txt/", "README.txt", "plain-text", "My spoon is too big.")
	check("plain", "/user2/readme-test/src/branch/plain/", "README", "plain-text", "Birken my stocks gee howdy")
	check("i18n", "/user2/readme-test/src/branch/i18n/", "README.zh.md", "markdown", "蛋糕是一个谎言")

	// using HEAD ref
	check("branch-HEAD", "/user2/readme-test/src/branch/HEAD/", "README.md", "markdown", "The cake is a lie.")
	check("commit-HEAD", "/user2/readme-test/src/commit/HEAD/", "README.md", "markdown", "The cake is a lie.")

	// viewing different subdirectories
	check("subdir", "/user2/readme-test/src/branch/subdir/libcake", "README.md", "markdown", "Four pints of sugar.")
	check("docs-direct", "/user2/readme-test/src/branch/special-subdir-docs/docs/", "README.md", "markdown", "This is in docs/")
	check("docs", "/user2/readme-test/src/branch/special-subdir-docs/", "docs/README.md", "markdown", "This is in docs/")
	check(".gitea", "/user2/readme-test/src/branch/special-subdir-.gitea/", ".gitea/README.md", "markdown", "This is in .gitea/")
	check(".github", "/user2/readme-test/src/branch/special-subdir-.github/", ".github/README.md", "markdown", "This is in .github/")

	// symlinks
	// symlinks are subtle:
	// - they should be able to handle going a reasonable number of times up and down in the tree
	// - they shouldn't get stuck on link cycles
	// - they should determine the filetype based on the name of the link, not the target
	check("symlink", "/user2/readme-test/src/branch/symlink/", "README.md", "markdown", "This is in some/other/path")
	check("symlink-multiple", "/user2/readme-test/src/branch/symlink/some/", "README.txt", "plain-text", "This is in some/other/path")
	check("symlink-up-and-down", "/user2/readme-test/src/branch/symlink/up/back/down/down", "README.md", "markdown", "It's a me, mario")

	// testing fallback rules
	// READMEs are searched in this order:
	// - [README.zh-cn.md, README.zh_cn.md, README.zh.md, README_zh.md, README.md, README.txt, README,
	//     docs/README.zh-cn.md, docs/README.zh_cn.md, docs/README.zh.md, docs/README_zh.md, docs/README.md, docs/README.txt, docs/README,
	//    .gitea/README.zh-cn.md, .gitea/README.zh_cn.md, .gitea/README.zh.md, .gitea/README_zh.md, .gitea/README.md, .gitea/README.txt, .gitea/README,

	//     .github/README.zh-cn.md, .github/README.zh_cn.md, .github/README.zh.md, .github/README_zh.md, .github/README.md, .github/README.txt, .github/README]
	// and a broken/looped symlink counts as not existing at all and should be skipped.
	// again, this doesn't cover all cases, but it covers a few
	check("fallback/top", "/user2/readme-test/src/branch/fallbacks/", "README.en.md", "markdown", "This is README.en.md")
	check("fallback/2", "/user2/readme-test/src/branch/fallbacks2/", "README.md", "markdown", "This is README.md")
	check("fallback/3", "/user2/readme-test/src/branch/fallbacks3/", "README", "plain-text", "This is README")
	check("fallback/4", "/user2/readme-test/src/branch/fallbacks4/", "docs/README.en.md", "markdown", "This is docs/README.en.md")
	check("fallback/5", "/user2/readme-test/src/branch/fallbacks5/", "docs/README.md", "markdown", "This is docs/README.md")
	check("fallback/6", "/user2/readme-test/src/branch/fallbacks6/", "docs/README", "plain-text", "This is docs/README")
	check("fallback/7", "/user2/readme-test/src/branch/fallbacks7/", ".gitea/README.en.md", "markdown", "This is .gitea/README.en.md")
	check("fallback/8", "/user2/readme-test/src/branch/fallbacks8/", ".gitea/README.md", "markdown", "This is .gitea/README.md")
	check("fallback/9", "/user2/readme-test/src/branch/fallbacks9/", ".gitea/README", "plain-text", "This is .gitea/README")

	// this case tests that broken symlinks count as missing files, instead of rendering their contents
	check("fallbacks-broken-symlinks", "/user2/readme-test/src/branch/fallbacks-broken-symlinks/", "docs/README", "plain-text", "This is docs/README")

	// some cases that should NOT render a README
	// - /readme
	// - /.github/docs/README.md
	// - a symlink loop

	missing := func(name, url string) {
		t.Run("missing/"+name, func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", url)
			resp := session.MakeRequest(t, req, http.StatusOK)

			htmlDoc := NewHTMLParser(t, resp.Body)
			_, exists := htmlDoc.doc.Find(".file-view").Attr("class")

			assert.False(t, exists, "README should not have rendered")
		})
	}
	missing("sp-ace", "/user2/readme-test/src/branch/sp-ace/")
	missing("nested-special", "/user2/readme-test/src/branch/special-subdir-nested/subproject") // the special subdirs should only trigger on the repo root
	missing("special-subdir-nested", "/user2/readme-test/src/branch/special-subdir-nested/")
	missing("symlink-loop", "/user2/readme-test/src/branch/symlink-loop/")
}

func TestRenamedFileHistory(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		t.Run("Renamed file", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", "/user2/repo59/commits/branch/master/license")
			resp := MakeRequest(t, req, http.StatusOK)

			htmlDoc := NewHTMLParser(t, resp.Body)

			renameNotice := htmlDoc.doc.Find(".ui.bottom.attached.header")
			assert.Equal(t, 1, renameNotice.Length())
			assert.Contains(t, renameNotice.Text(), "Renamed from licnse (Browse further)")

			oldFileHistoryLink, ok := renameNotice.Find("a").Attr("href")
			assert.True(t, ok)
			assert.Equal(t, "/user2/repo59/commits/commit/80b83c5c8220c3aa3906e081f202a2a7563ec879/licnse", oldFileHistoryLink)
		})

		t.Run("Renamed file, pagination", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			defer test.MockVariableValue(&setting.Git.CommitsRangeSize, 1)() // Limit commits displayed on the page to one

			resp := MakeRequest(t, NewRequest(t, "GET", "/user2/repo59/commits/branch/master/license"), http.StatusOK)
			page1 := NewHTMLParser(t, resp.Body)

			resp = MakeRequest(t, NewRequest(t, "GET", "/user2/repo59/commits/branch/master/license?page=2"), http.StatusOK)
			page2 := NewHTMLParser(t, resp.Body)

			// Browse further is only shown on 2nd page
			browseFurtherSel := ".ui.bottom.attached.header a[href='/user2/repo59/commits/commit/80b83c5c8220c3aa3906e081f202a2a7563ec879/licnse']"
			page1.AssertElement(t, browseFurtherSel, false)
			page2.AssertElement(t, browseFurtherSel, true)

			// Pagination goes after Browser further
			afterBrowseFurther := page2.Find(browseFurtherSel).Parent().Parent().NextAll()
			assert.Equal(t, 1, afterBrowseFurther.Find(".pagination.menu").Length())
		})

		t.Run("Non renamed file", func(t *testing.T) {
			req := NewRequest(t, "GET", "/user2/repo59/commits/branch/master/README.md")
			resp := MakeRequest(t, req, http.StatusOK)

			htmlDoc := NewHTMLParser(t, resp.Body)

			htmlDoc.AssertElement(t, ".ui.bottom.attached.header", false)
		})

		t.Run("Renamed file (with escaped name)", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			user2 := forgery.CreateUser(t, nil)

			var commitID string
			repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
				Files: forgery.MapFS{
					"#beep": forgery.MapFile("ping pong"),
				},
				LatestSha: &commitID,
			})

			files, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, user2, &files_service.ChangeRepoFilesOptions{
				Files: []*files_service.ChangeRepoFile{
					{
						Operation:     "update",
						TreePath:      "beep",
						FromTreePath:  "#beep",
						ContentReader: strings.NewReader("ping pong"),
					},
				},
				Message:   "rename",
				OldBranch: "main",
				NewBranch: "main",
				Author: &files_service.IdentityOptions{
					Name:  user2.Name,
					Email: user2.Email,
				},
				Committer: &files_service.IdentityOptions{
					Name:  user2.Name,
					Email: user2.Email,
				},
				Dates: &files_service.CommitDateOptions{
					Author:    time.Now(),
					Committer: time.Now(),
				},
				LastCommitID: commitID,
			})
			require.NoError(t, err)

			req := NewRequestf(t, "GET", "/%s/commits/branch/main/beep", repo.FullName())
			resp := MakeRequest(t, req, http.StatusOK)

			htmlDoc := NewHTMLParser(t, resp.Body)

			renameNotice := htmlDoc.doc.Find(".ui.bottom.attached.header")
			assert.Equal(t, 1, renameNotice.Length())
			assert.Contains(t, renameNotice.Text(), "Renamed from #beep (Browse further)")

			oldFileHistoryLink, ok := renameNotice.Find("a").Attr("href")
			assert.True(t, ok)
			assert.Equal(t, fmt.Sprintf("/%s/commits/commit/%s/%%23beep", repo.FullName(), files.Commit.SHA), oldFileHistoryLink)
		})
	})
}

func TestMarkDownReadmeImage(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	req := NewRequest(t, "GET", "/user2/repo1/src/branch/home-md-img-check")
	resp := session.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	src, exists := htmlDoc.doc.Find(`.markdown img`).Attr("src")
	assert.True(t, exists, "Image not found in README")
	assert.Equal(t, "/user2/repo1/media/branch/home-md-img-check/test-fake-img.jpg", src)

	req = NewRequest(t, "GET", "/user2/repo1/src/branch/home-md-img-check/README.md")
	resp = session.MakeRequest(t, req, http.StatusOK)

	htmlDoc = NewHTMLParser(t, resp.Body)
	src, exists = htmlDoc.doc.Find(`.markdown img`).Attr("src")
	assert.True(t, exists, "Image not found in markdown file")
	assert.Equal(t, "/user2/repo1/media/branch/home-md-img-check/test-fake-img.jpg", src)
}

func TestMarkDownReadmeImageSubfolder(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	// this branch has the README in the special docs/README.md location
	req := NewRequest(t, "GET", "/user2/repo1/src/branch/sub-home-md-img-check")
	resp := session.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	src, exists := htmlDoc.doc.Find(`.markdown img`).Attr("src")
	assert.True(t, exists, "Image not found in README")
	assert.Equal(t, "/user2/repo1/media/branch/sub-home-md-img-check/docs/test-fake-img.jpg", src)

	req = NewRequest(t, "GET", "/user2/repo1/src/branch/sub-home-md-img-check/docs/README.md")
	resp = session.MakeRequest(t, req, http.StatusOK)

	htmlDoc = NewHTMLParser(t, resp.Body)
	src, exists = htmlDoc.doc.Find(`.markdown img`).Attr("src")
	assert.True(t, exists, "Image not found in markdown file")
	assert.Equal(t, "/user2/repo1/media/branch/sub-home-md-img-check/docs/test-fake-img.jpg", src)
}

func TestGeneratedSourceLink(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	t.Run("Rendered file", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/user2/repo1/src/branch/master/README.md?display=source")
		resp := MakeRequest(t, req, http.StatusOK)
		doc := NewHTMLParser(t, resp.Body)

		dataURL, exists := doc.doc.Find(".copy-line-permalink").Attr("data-url")
		assert.True(t, exists)
		assert.Equal(t, "/user2/repo1/src/commit/65f1bf27bc3bf70f64657658635e66094edbcb4d/README.md?display=source", dataURL)

		dataURL, exists = doc.doc.Find(".ref-in-new-issue").Attr("data-url-param-body-link")
		assert.True(t, exists)
		assert.Equal(t, "/user2/repo1/src/commit/65f1bf27bc3bf70f64657658635e66094edbcb4d/README.md?display=source", dataURL)
	})

	t.Run("Non-Rendered file", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		session := loginUser(t, "user27")
		req := NewRequest(t, "GET", "/user27/repo49/src/branch/master/test/test.txt")
		resp := session.MakeRequest(t, req, http.StatusOK)
		doc := NewHTMLParser(t, resp.Body)

		dataURL, exists := doc.doc.Find(".copy-line-permalink").Attr("data-url")
		assert.True(t, exists)
		assert.Equal(t, "/user27/repo49/src/commit/aacbdfe9e1c4b47f60abe81849045fa4e96f1d75/test/test.txt", dataURL)

		dataURL, exists = doc.doc.Find(".ref-in-new-issue").Attr("data-url-param-body-link")
		assert.True(t, exists)
		assert.Equal(t, "/user27/repo49/src/commit/aacbdfe9e1c4b47f60abe81849045fa4e96f1d75/test/test.txt", dataURL)
	})
}

func TestViewCommit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	req := NewRequest(t, "GET", "/user2/repo1/commit/0123456789012345678901234567890123456789")
	req.Header.Add("Accept", "text/html")
	resp := MakeRequest(t, req, http.StatusNotFound)
	assert.True(t, test.IsNormalPageCompleted(resp.Body.String()), "non-existing commit should render 404 page")
}

func TestViewCommitSignature(t *testing.T) {
	t.Cleanup(func() {
		// Cannot use t.Context(), it is in the done state.
		require.NoError(t, git.InitFull(context.Background()))
	})

	defer test.MockVariableValue(&setting.Repository.Signing.SigningName, "UwU")()
	defer test.MockVariableValue(&setting.Repository.Signing.SigningEmail, "fox@example.com")()
	defer test.MockVariableValue(&setting.Repository.Signing.CRUDActions, []string{"always"})()
	defer test.MockVariableValue(&setting.Repository.Signing.InitialCommit, []string{"always"})()

	filePath := "signed.txt"
	fromBranch := "master"
	toBranch := "branch-signed"

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		// Use a new GNUPGPHOME to avoid messing with the existing GPG keyring.
		tmpDir := t.TempDir()
		require.NoError(t, os.Chmod(tmpDir, 0o700))
		t.Setenv("GNUPGHOME", tmpDir)

		rootKeyPair, err := importTestingKey()
		require.NoError(t, err)
		defer test.MockVariableValue(&setting.Repository.Signing.SigningKey, rootKeyPair.PrimaryKey.KeyIdShortString())()
		defer test.MockVariableValue(&setting.Repository.Signing.Format, "openpgp")()

		// Ensure the git config is updated with the new signing format.
		require.NoError(t, git.InitFull(t.Context()))

		user := forgery.CreateUser(t, nil)
		testCtx := NewAPITestContext(t, user.Name, "commit-header-signed", auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)
		u.Path = testCtx.GitPath()

		t.Run("Create repository", doAPICreateRepository(testCtx, nil, git.Sha1ObjectFormat))

		t.Run("Create commit", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			options := &api.CreateFileOptions{
				FileOptions: api.FileOptions{
					BranchName:    fromBranch,
					NewBranchName: toBranch,
					Message:       fmt.Sprintf("from:%s to:%s path:%s", fromBranch, toBranch, filePath),
					Author: api.Identity{
						Name:  user.FullName,
						Email: user.Email,
					},
					Committer: api.Identity{
						Name:  user.FullName,
						Email: user.Email,
					},
				},
				ContentBase64: base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "This is new text for %s", filePath)),
			}

			req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/%s/contents/%s", testCtx.Username, testCtx.Reponame, filePath), &options).
				AddTokenAuth(testCtx.Token)
			resp := testCtx.Session.MakeRequest(t, req, http.StatusCreated)

			var contents api.FileResponse
			DecodeJSON(t, resp, &contents)

			assert.True(t, contents.Verification.Verified)

			req = NewRequest(t, "GET", fmt.Sprintf("/%s/%s/commit/%s", testCtx.Username, testCtx.Reponame, contents.Commit.SHA))
			resp = testCtx.Session.MakeRequest(t, req, http.StatusOK)

			htmlDoc := NewHTMLParser(t, resp.Body)
			htmlDoc.AssertElement(t, ".signature-row.message.isSigned.isVerified", true)
		})
	})
}

func TestCommitView(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	t.Run("Non-existent commit", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/commit/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		req.SetHeader("Accept", "text/html")
		resp := MakeRequest(t, req, http.StatusNotFound)

		// Really ensure that 404 is being sent back.
		doc := NewHTMLParser(t, resp.Body)
		doc.AssertElement(t, `[aria-label="Page not found"]`, true)
	})

	t.Run("Too short commit ID", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/commit/65f")
		MakeRequest(t, req, http.StatusNotFound)
	})

	t.Run("Short commit ID", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/commit/65f1")
		resp := MakeRequest(t, req, http.StatusOK)

		doc := NewHTMLParser(t, resp.Body)
		commitTitle := doc.Find(".commit-summary").Text()
		assert.Contains(t, commitTitle, "Initial commit")

		req = NewRequest(t, "GET", "/user2/repo1/src/commit/65f1")
		resp = MakeRequest(t, req, http.StatusOK)

		doc = NewHTMLParser(t, resp.Body)
		commitTitle = doc.Find(".shortsha").Text()
		assert.Contains(t, commitTitle, "65f1bf27bc")
	})

	t.Run("Full commit ID", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/commit/65f1bf27bc3bf70f64657658635e66094edbcb4d")
		resp := MakeRequest(t, req, http.StatusOK)

		doc := NewHTMLParser(t, resp.Body)
		commitTitle := doc.Find(".commit-summary").Text()
		assert.Contains(t, commitTitle, "Initial commit")
	})
}

func TestRepoHomeViewRedirect(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	t.Run("Code", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1")
		resp := MakeRequest(t, req, http.StatusOK)

		doc := NewHTMLParser(t, resp.Body)
		l := doc.Find("#repo-desc").Length()
		assert.Equal(t, 1, l)
	})

	t.Run("No Code redirects to Issues", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		// Disable the Code unit
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		err := repo_service.UpdateRepositoryUnits(db.DefaultContext, repo, nil, []unit_model.Type{
			unit_model.TypeCode,
		})
		require.NoError(t, err)

		// The repo home should redirect to the built-in issue tracker
		req := NewRequest(t, "GET", "/user2/repo1")
		resp := MakeRequest(t, req, http.StatusSeeOther)
		redir := resp.Header().Get("Location")

		assert.Equal(t, "/user2/repo1/issues", redir)
	})

	t.Run("No Code and ExternalTracker redirects to Pulls", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		// Replace the internal tracker with an external one
		// Disable Code, Projects, Packages, and Actions
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		err := repo_service.UpdateRepositoryUnits(db.DefaultContext, repo, []repo_model.RepoUnit{{
			RepoID: repo.ID,
			Type:   unit_model.TypeExternalTracker,
			Config: &repo_model.ExternalTrackerConfig{
				ExternalTrackerURL: "https://example.com",
			},
		}}, []unit_model.Type{
			unit_model.TypeCode,
			unit_model.TypeIssues,
			unit_model.TypeProjects,
			unit_model.TypePackages,
			unit_model.TypeActions,
		})
		require.NoError(t, err)

		// The repo home should redirect to pull requests
		req := NewRequest(t, "GET", "/user2/repo1")
		resp := MakeRequest(t, req, http.StatusSeeOther)
		redir := resp.Header().Get("Location")

		assert.Equal(t, "/user2/repo1/pulls", redir)
	})

	t.Run("Only external wiki results in 404", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		// Replace the internal wiki with an external, and disable everything
		// else.
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		err := repo_service.UpdateRepositoryUnits(db.DefaultContext, repo, []repo_model.RepoUnit{{
			RepoID: repo.ID,
			Type:   unit_model.TypeExternalWiki,
			Config: &repo_model.ExternalWikiConfig{
				ExternalWikiURL: "https://example.com",
			},
		}}, []unit_model.Type{
			unit_model.TypeCode,
			unit_model.TypeIssues,
			unit_model.TypeExternalTracker,
			unit_model.TypeProjects,
			unit_model.TypePackages,
			unit_model.TypeActions,
			unit_model.TypePullRequests,
			unit_model.TypeReleases,
			unit_model.TypeWiki,
		})
		require.NoError(t, err)

		// The repo home ends up being 404
		req := NewRequest(t, "GET", "/user2/repo1")
		req.Header.Set("Accept", "text/html")
		resp := MakeRequest(t, req, http.StatusNotFound)

		// The external wiki is linked to from the 404 page
		doc := NewHTMLParser(t, resp.Body)
		txt := strings.TrimSpace(doc.Find(`a[href="https://example.com"]`).Text())
		assert.Equal(t, "Wiki", txt)
	})
}

func TestRepoFilesList(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

		// create the repo
		repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Files: forgery.MapFS{
				"zEta":           forgery.MapFile("zeta"),
				"licensa":        forgery.MapFile("licensa"),
				"LICENSE":        forgery.MapFile("LICENSE"),
				"licensz":        forgery.MapFile("licensz"),
				"delta":          forgery.MapFile("delta"),
				"Charlie/aa.txt": forgery.MapFile("charlie"),
				"Beta":           forgery.MapFile("beta"),
				"alpha":          forgery.MapFile("alpha"),
				"README.md":      forgery.MapFile("README.md"),
			},
		})

		req := NewRequest(t, "GET", "/"+repo.FullName())
		resp := MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)
		filesList := htmlDoc.Find("#repo-files-table tbody tr").Map(func(_ int, s *goquery.Selection) string {
			return s.AttrOr("data-entryname", "")
		})

		assert.Equal(t, []string{"Charlie", "alpha", "Beta", "delta", "licensa", "LICENSE", "licensz", "README.md", "zEta"}, filesList)
	})
}

func TestRepoFollowSymlink(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user2")

	assertCase := func(t *testing.T, url, expectedSymlinkURL string, shouldExist bool) {
		t.Helper()

		req := NewRequest(t, "GET", url)
		resp := session.MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)
		symlinkURL, ok := htmlDoc.Find(".file-actions .button[data-kind='follow-symlink']").Attr("href")
		if shouldExist {
			assert.True(t, ok)
			assert.Equal(t, expectedSymlinkURL, symlinkURL)
		} else {
			assert.False(t, ok)
		}
	}

	t.Run("Normal", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		assertCase(t, "/user2/readme-test/src/branch/symlink/README.md?display=source", "/user2/readme-test/src/branch/symlink/some/other/path/awefulcake.txt", true)
	})

	t.Run("Normal", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		assertCase(t, "/user2/readme-test/src/branch/symlink/some/README.txt", "/user2/readme-test/src/branch/symlink/some/other/path/awefulcake.txt", true)
	})

	t.Run("Normal", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		assertCase(t, "/user2/readme-test/src/branch/symlink/up/back/down/down/README.md", "/user2/readme-test/src/branch/symlink/down/side/../left/right/../reelmein", true)
	})

	t.Run("Broken symlink", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		assertCase(t, "/user2/readme-test/src/branch/fallbacks-broken-symlinks/docs/README", "", false)
	})

	t.Run("Loop symlink", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		assertCase(t, "/user2/readme-test/src/branch/symlink-loop/README.md", "", false)
	})

	t.Run("Not a symlink", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		assertCase(t, "/user2/readme-test/src/branch/master/README.md", "", false)
	})
}

func TestViewRepoOpenWith(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	getOpenWith := func() []string {
		req := NewRequest(t, "GET", "/user2/repo1")
		resp := MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)
		openWithHTML := htmlDoc.doc.Find(".js-clone-url-editor")

		var methods []string
		openWithHTML.Each(func(i int, s *goquery.Selection) {
			a, _ := s.Attr("data-href-template")
			methods = append(methods, a)
		})

		return methods
	}

	testOpenWith := func(expected []string) {
		methods := getOpenWith()

		assert.Len(t, methods, len(expected))
		for i, expectedMethod := range expected {
			assert.True(t, strings.HasPrefix(methods[i], expectedMethod))
		}
	}

	t.Run("Defaults", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		testOpenWith([]string{"vscode://", "vscodium://", "jetbrains://"})
	})

	t.Run("Customised", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		// Change the methods via the admin settings
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{IsAdmin: true})
		session := loginUser(t, user.Name)

		setEditorApps := func(t *testing.T, apps string) {
			t.Helper()

			req := NewRequestWithValues(t, "POST", "/admin/config?key=repository.open-with.editor-apps", map[string]string{
				"value": apps,
			})
			session.MakeRequest(t, req, http.StatusOK)
		}

		defer func() {
			setEditorApps(t, "")
		}()

		setEditorApps(t, "test = test://?url={url}")

		testOpenWith([]string{"test://"})
	})
}

func TestRepoCodeSearchForm(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	testSearchForm := func(t *testing.T, indexer bool) {
		defer test.MockVariableValue(&setting.Indexer.RepoIndexerEnabled, indexer)()
		req := NewRequest(t, "GET", "/user2/repo1/src/branch/master")
		resp := MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)
		formEl := htmlDoc.doc.Find("form[data-test-tag=codesearch]")

		action, exists := formEl.Attr("action")
		assert.True(t, exists)
		branchSubURL := "/branch/master"
		if indexer {
			assert.NotContains(t, action, branchSubURL)
		} else {
			assert.Contains(t, action, branchSubURL)
		}

		filepath, exists := formEl.Find("input[name=path]").Attr("value")
		assert.True(t, exists)
		assert.Empty(t, filepath)

		req = NewRequest(t, "GET", "/user2/glob/src/branch/master/x/y")
		resp = MakeRequest(t, req, http.StatusOK)

		filepath, exists = NewHTMLParser(t, resp.Body).doc.
			Find("form[data-test-tag=codesearch] input[name=path]").
			Attr("value")
		assert.True(t, exists)
		assert.Equal(t, "x/y", filepath)
	}

	t.Run("indexer disabled", func(t *testing.T) {
		testSearchForm(t, false)
	})

	t.Run("indexer enabled", func(t *testing.T) {
		testSearchForm(t, true)
	})
}

func TestFileHistoryPager(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	t.Run("Normal page number", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/commits/branch/master/README.md?page=1")
		MakeRequest(t, req, http.StatusOK)
	})

	t.Run("Too high page number", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/commits/branch/master/README.md?page=9999")
		MakeRequest(t, req, http.StatusNotFound)
	})
}

func TestRepoSubmoduleView(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		t.Run("FromGit", func(t *testing.T) {
			repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
				Files: forgery.MapFS{
					"README.md": forgery.MapFile("some explanation"), // file is expected in doGitClone
				},
			})

			// Clone the repository, add a submodule and push it.
			dstPath := t.TempDir()

			uClone := *u
			uClone.Path = repo.FullName()
			uClone.User = url.UserPassword(user2.Name, userPassword)

			t.Run("Clone", doGitClone(dstPath, &uClone))

			_, _, err := git.NewCommand(git.DefaultContext, "submodule", "add").AddDynamicArguments(u.JoinPath("/user2/repo1").String()).RunStdString(&git.RunOpts{Dir: dstPath})
			require.NoError(t, err)

			_, _, err = git.NewCommand(git.DefaultContext, "add", "repo1", ".gitmodules").RunStdString(&git.RunOpts{Dir: dstPath})
			require.NoError(t, err)

			_, _, err = git.NewCommand(git.DefaultContext, "commit", "-m", "add submodule").RunStdString(&git.RunOpts{Dir: dstPath})
			require.NoError(t, err)

			_, _, err = git.NewCommand(git.DefaultContext, "push").RunStdString(&git.RunOpts{Dir: dstPath})
			require.NoError(t, err)

			// Check that the submodule entry exist and the link is correct.
			req := NewRequest(t, "GET", "/"+repo.FullName())
			resp := MakeRequest(t, req, http.StatusOK)

			htmlDoc := NewHTMLParser(t, resp.Body)
			htmlDoc.AssertElement(t, fmt.Sprintf(`tr[data-entryname="repo1"] a[href="%s"]`, u.JoinPath("/user2/repo1").String()), true)

			// Check that a link to the submodule returns a redirect and that the redirect link is correct.
			req = NewRequest(t, "GET", "/"+repo.FullName()+"/src/branch/"+repo.DefaultBranch+"/repo1")
			resp = MakeRequest(t, req, http.StatusSeeOther)

			assert.Equal(t, u.JoinPath("/user2/repo1").String(), resp.Header().Get("Location"))
		})

		t.Run("Declarative", func(t *testing.T) {
			repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
				Files: forgery.MapFS{
					".gitmodules": forgery.MapFile(`[submodule "relative-module"]
  path = relative-module
  url = https://git.example.org/submodule.git
`),
					"relative-module": forgery.MapSubmodule("95601d16476a"),
				},
			})

			// Check that the submodule entry exist and the link is correct.
			req := NewRequest(t, "GET", "/"+repo.FullName())
			resp := MakeRequest(t, req, http.StatusOK)

			expectedDst := "https://git.example.org/submodule"
			htmlDoc := NewHTMLParser(t, resp.Body)

			href, ok := htmlDoc.Find(`tr[data-entryname="relative-module"] a`).Attr("href")
			assert.True(t, ok, "could not find entry 'relative-module' in file list")
			assert.Equal(t, expectedDst, href)

			// Check that a link to the submodule returns a redirect and that the redirect link is correct.
			req = NewRequest(t, "GET", "/"+repo.FullName()+"/src/branch/"+repo.DefaultBranch+"/relative-module")
			resp = MakeRequest(t, req, http.StatusSeeOther)

			assert.Equal(t, expectedDst, resp.Header().Get("Location"))
		})

		t.Run("SubmodulesFileTooBig", func(t *testing.T) {
			repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
				Files: forgery.MapFS{
					".gitmodules": forgery.MapFile(strings.Repeat("#", git.MaxGitmodulesFileSize-5) + // ensure that the partial read is invalid
						`
[submodule "relative-module"]
  path = relative-module
  url = https://git.example.org/submodule.git
`),
					"relative-module": forgery.MapSubmodule("95601d16476a"),
				},
			})

			// Check that the submodule entry exist and the link is correct.
			req := NewRequest(t, "GET", "/"+repo.FullName())
			resp := MakeRequest(t, req, http.StatusOK)

			htmlDoc := NewHTMLParser(t, resp.Body)

			_, ok := htmlDoc.Find(`tr[data-entryname="relative-module"] td.name a`).Attr("href")
			assert.False(t, ok, "should not find a link to 'relative-module' in file list")

			// Check that a link to the submodule returns a redirect and that the redirect link is correct.
			req = NewRequest(t, "GET", "/"+repo.FullName()+"/src/branch/"+repo.DefaultBranch+"/relative-module")
			resp = MakeRequest(t, req, http.StatusSeeOther)

			assert.Equal(t, "/"+repo.FullName()+"/src/branch/"+repo.DefaultBranch+"/", resp.Header().Get("Location"))

			// Check that a warning is present
			req = NewRequest(t, "GET", "/"+repo.FullName()+"/src/branch/"+repo.DefaultBranch+"/.gitmodules")
			resp = MakeRequest(t, req, http.StatusOK)

			htmlDoc = NewHTMLParser(t, resp.Body)

			warn, err := htmlDoc.Find(`.non-diff-file-content .warning`).Html()
			require.NoError(t, err)
			assert.NotEmpty(t, warn)
		})
	})
}

func TestBlameDirectory(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	// Ensure directory exists.
	req := NewRequest(t, "GET", "/user2/repo59/src/branch/master/deep")
	MakeRequest(t, req, http.StatusOK)

	// Blame is not allowed
	req = NewRequest(t, "GET", "/user2/repo59/blame/branch/master/deep")
	MakeRequest(t, req, http.StatusNotFound)
}

func TestInitInstructions(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	session := loginUser(t, user.Name)

	forEachObjectFormat(t, func(t *testing.T, objectFormat git.ObjectFormat) {
		defer tests.PrintCurrentTest(t)()

		var init string
		if objectFormat == git.Sha1ObjectFormat {
			init = "git init"
		} else {
			init = fmt.Sprintf("git init --object-format=%s", objectFormat.Name())
		}

		repo := forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{
			ObjectFormat: objectFormat,
		})

		portMatcher := regexp.MustCompile(`localhost:\d+`)
		resp := session.MakeRequest(t, NewRequest(t, "GET", "/"+repo.FullName()), http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)
		assert.Equal(t, fmt.Sprintf(`touch README.md
%s
git switch -c main
git add README.md
git commit -m "first commit"
git remote add origin http://localhost/user2/%s.git
git push -u origin main`, init, repo.Name), portMatcher.ReplaceAllString(htmlDoc.Find(".empty-repo-guide code").First().Text(), "localhost"))
	})
}

func TestRepoFindFilesRequiresCodeRead(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	// user15 is a member of the team test_team (id=7), which has only write-issue access in the org org3.  This team
	// has limited access to repos, so we need to add a private repo to its access list:
	unittest.AssertSuccessfulInsert(t, &organization.TeamRepo{OrgID: 3, TeamID: 7, RepoID: 3})
	session := loginUser(t, "user15")

	// Verify that we can access the private repo's issues page, as the team has issue access:
	session.MakeRequest(t, NewRequest(t, "GET", "/org3/repo3/issues"), http.StatusOK)

	// Verify that we can't access /find and the related /tree-list endpoints because the team doesn't have code access:
	session.MakeRequest(t, NewRequest(t, "GET", "/org3/repo3/find/branch/master"), http.StatusNotFound)
	session.MakeRequest(t, NewRequest(t, "GET", "/org3/repo3/tree-list/branch/master"), http.StatusNotFound)
}
