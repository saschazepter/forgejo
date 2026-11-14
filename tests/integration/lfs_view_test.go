// Copyright 2022 The Gitea Authors. All rights reserved.
// Copyright 2026 The Forgejo Authors. All right reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/lfs"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	"forgejo.org/modules/translation"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// check that files stored in LFS render properly in the web UI of the
// repository settings
func TestLFSFileSettingsRender(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&setting.LFS.StartServer, true)()

	session := loginUser(t, "user2")
	locale := translation.NewLocale("en-US")

	// for a repository without LFS files, check that the correct 'not found'
	// message is actually shown
	t.Run("WithoutLFSFiles", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/settings/lfs")
		resp := session.MakeRequest(t, req, http.StatusOK)

		filesTable := NewHTMLParser(t, resp.Body).doc.Find("#lfs-files-table")
		assert.Contains(t, filesTable.Text(), locale.TrString("repo.settings.lfs_no_lfs_files"))
	})

	// now check that it isn't
	t.Run("WithLFSFiles", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/lfs/settings/lfs")
		resp := session.MakeRequest(t, req, http.StatusOK)

		filesTable := NewHTMLParser(t, resp.Body).doc.Find("#lfs-files-table")
		assert.NotContains(t, filesTable.Text(), locale.TrString("repo.settings.lfs_no_lfs_files"))
	})
}

// Check that files render correctly for the different types of web endpoints
// that can display information/contents of files (e.g. diffs, blame, etc.)
func TestLFSFileRenderInRepo(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")
	locale := translation.NewLocale("en-US")

	/* Helper functions */

	// Gets the doc corresponding to the urlStr, ensures that the HTTP status
	// is OK.
	testGetDocOK := func(t *testing.T, session TestSession, urlStr string) *goquery.Document {
		req := NewRequest(t, "GET", urlStr)
		resp := session.MakeRequest(t, req, http.StatusOK)

		doc := NewHTMLParser(t, resp.Body).doc
		return doc
	}

	// Checks whether the label is present for a single-file view of a given doc.
	testStoredInGitLFSShown := func(t *testing.T, doc *goquery.Document, newFileName string, contains bool) {
		t.Run("Label", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			gitLfsLabel := doc.Find("div[data-new-filename='" + newFileName + "'] .diff-file-name > span.file > span")
			gitLfsLabelTooltip, exists := gitLfsLabel.Attr("data-tooltip-content")
			if contains {
				assert.Contains(t, gitLfsLabel.Text(), locale.TrString("quota.sizes.git.lfs"))
				assert.True(t, exists)
				assert.Contains(t, gitLfsLabelTooltip, locale.TrString("repo.diff.lfs.tooltip"))
			} else {
				assert.NotContains(t, gitLfsLabel.Text(), locale.TrString("quota.sizes.git.lfs"))
				assert.False(t, exists)
				assert.NotContains(t, gitLfsLabelTooltip, locale.TrString("repo.diff.lfs.tooltip"))
			}
		})
	}

	// Spots the diff of a file and checks whether the Git LFS pointer label is
	// present, e.g. for when many files are shown on a single page and we cannot
	// assert whether said files are accessible in our store; only that the shown
	// files represent valid pointers.
	testGitLFSPointerInfoShown := func(t *testing.T, doc *goquery.Document, contains bool) {
		t.Run("Label", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			fileInfo := doc.Find("div.file-info").Text()
			if contains {
				// TODO: Hover
				assert.Contains(t, fileInfo, locale.TrString("repo.stored_lfs"))
			} else {
				assert.NotContains(t, fileInfo, locale.TrString("repo.stored_lfs"))
			}
		})
	}

	// Test the endpoint's rendering for a Git LFS file.
	//
	// - t: Test session
	// - isLFSAccessible: Is the LFS server enabled & pointer of a valid object
	// - showLFSLabel: Should the label be shown (if the server is enabled)
	// - storedWithGitLFS label: false -> [testGitLFSPointerInfoShown], true -> [testStoredInGitLFSShown]
	// - urlStr: The URL whose 'rendering' is being tested
	// - contentSelector: Selector for finding the content
	// - contentStr: The content that we expect to find
	// - rawLinkSelector: Selector for finding the 'Raw'/'View file' button
	// - replaceBefore: Part of the urlStr that should be replaced
	// - replaceAfter: What said replaceBefore should be replaced with.
	testFileRender := func(t *testing.T, isLFSAccessible, showLFSLabel, storedWithGitLFSLabel bool, filenameStr, urlStr, contentSelector, contentStr, rawLinkSelector, replaceBefore, replaceAfter string) {
		doc := testGetDocOK(t, *session, urlStr)

		// 'Stored in Git LFS' is shown in the file info
		if storedWithGitLFSLabel {
			testStoredInGitLFSShown(t, doc, filenameStr, isLFSAccessible || showLFSLabel)
		} else {
			testGitLFSPointerInfoShown(t, doc, isLFSAccessible || showLFSLabel)
		}

		expectedRawLink := strings.Replace(urlStr, replaceBefore, replaceAfter, 1)

		t.Run("Content", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			content := doc.Find(contentSelector)
			assert.Contains(t, content.Text(), contentStr)
			// Special case for 'Binary': Check correct raw link rendering.
			if isLFSAccessible && filenameStr == "crypt.bin" && contentStr == locale.TrString("repo.file_view_raw") {
				t.Run("Binary link", func(t *testing.T) {
					// Sanity check that should always work
					assert.Equal(t, locale.TrString("repo.file_view_raw"), contentStr)
					rawLink, exists := doc.Find("div.file-view > div.view-raw > a").Attr("href")
					assert.True(t, exists, "Download link should render instead of content because this is a binary file")
					assert.Equal(t, expectedRawLink, rawLink, "The download link should use the proper /media link because it's in LFS")
				})
			}
		})

		t.Run("Raw button", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			rawLink, _ := doc.Find(rawLinkSelector).First().Attr("href")
			assert.Equal(t,
				expectedRawLink,
				rawLink,
			)
		})
	}

	// Checks whether the README renders for a given URL that shows the repository files.
	testReadmeUsingURL := func(t *testing.T, isLFS bool, urlStr string) {
		t.Run("Readme", func(t *testing.T) {
			t.Run("Content", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				doc := testGetDocOK(t, *session, urlStr+"/subdir")
				content := doc.Find("div#readme").Text()
				if isLFS {
					assert.Contains(t, content, "Testing READMEs in LFS")
					assert.NotContains(t, content, "oid sha256:9d172e5c64b4f0024b9901ec6afe9ea052f3c9b6ff9f4b07956d8c48c86fca82")
				} else {
					assert.NotContains(t, content, "Testing READMEs in LFS")
					assert.Contains(t, content, "oid sha256:9d172e5c64b4f0024b9901ec6afe9ea052f3c9b6ff9f4b07956d8c48c86fca82")
				}
			})
		})
	}

	/* Endpoint tests (we run them twice depending on LFS enabled) */

	// Tests /blame endpoint with different Git LFS scenarios.
	testBlame := func(t *testing.T, isLFSEnabled bool) {
		t.Run("Blame", func(t *testing.T) {
			t.Run("Markup", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				testFileRender(t,
					isLFSEnabled,
					isLFSEnabled,
					false,
					"CONTRIBUTING.md",
					"/user2/lfs/blame/commit/e9c32647bab825977942598c0efa415de300304b/CONTRIBUTING.md",
					".file-view > table:nth-child(1) > tbody:nth-child(1) > tr:nth-child(2)",
					"oid sha256:7b6b2c88dba9f760a1a58469b67fee2b698ef7e9399c4ca4f34a14ccbe39f623",
					"div.file-header-right > div > a",
					"blame",
					"raw",
				)
			})

			t.Run("Binary", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				testFileRender(t,
					isLFSEnabled,
					isLFSEnabled,
					false,
					"crypt.bin",
					"/user2/lfs/blame/commit/e9c32647bab825977942598c0efa415de300304b/crypt.bin",
					".file-view > table:nth-child(1) > tbody:nth-child(1) > tr:nth-child(2)",
					"oid sha256:2eccdb43825d2a49d99d542daa20075cff1d97d9d2349a8977efe9c03661737c",
					"div.file-header-right > div > a",
					"blame",
					"raw",
				)
			})

			t.Run("Invalid", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				testFileRender(t,
					false,
					false,
					false,
					"invalid",
					"/user2/lfs/blame/commit/e9c32647bab825977942598c0efa415de300304b/invalid",
					".file-view > table:nth-child(1) > tbody:nth-child(1) > tr:nth-child(2)",
					"oid sha256:9d178b5f15046343fd32f451df93acc2bdd9e6373be478b968e4cad6b6647351",
					"div.file-header-right > div > a",
					"blame",
					"raw",
				)
			})
		})
	}

	// Tests /commit endpoint with different Git LFS scenarios
	testCommit := func(t *testing.T, isLFSEnabled bool) {
		t.Run("Commit", func(t *testing.T) {
			t.Run("Markup", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				testFileRender(t,
					isLFSEnabled,
					true,
					true,
					"CONTRIBUTING.md",
					"/user2/lfs/commit/73cf03db6ece34e12bf91e8853dc58f678f2f82d",
					"div[data-new-filename='CONTRIBUTING.md'] .diff-file-body",
					"oid sha256:7b6b2c88dba9f760a1a58469b67fee2b698ef7e9399c4ca4f34a14ccbe39f623",
					"div[data-new-filename='CONTRIBUTING.md'] .diff-file-header-actions > a",
					"commit/73cf03db6ece34e12bf91e8853dc58f678f2f82d",
					"src/commit/73cf03db6ece34e12bf91e8853dc58f678f2f82d/CONTRIBUTING.md",
				)
			})

			t.Run("Binary", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				testFileRender(t,
					isLFSEnabled,
					true,
					true,
					"crypt.bin",
					"/user2/lfs/commit/73cf03db6ece34e12bf91e8853dc58f678f2f82d",
					"div[data-new-filename='crypt.bin'] .diff-file-body",
					"oid sha256:2eccdb43825d2a49d99d542daa20075cff1d97d9d2349a8977efe9c03661737c",
					"div[data-new-filename='crypt.bin'] .diff-file-header-actions > a",
					"commit/73cf03db6ece34e12bf91e8853dc58f678f2f82d",
					"src/commit/73cf03db6ece34e12bf91e8853dc58f678f2f82d/crypt.bin",
				)
			})

			t.Run("Invalid", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				testFileRender(t,
					false,
					false,
					false,
					"invalid",
					"/user2/lfs/commit/e9c32647bab825977942598c0efa415de300304b",
					"div[data-new-filename='invalid'] .diff-file-body",
					"oid sha256:9d178b5f15046343fd32f451df93acc2bdd9e6373be478b968e4cad6b6647351",
					"div[data-new-filename='invalid'] .diff-file-header-actions > a",
					"commit/e9c32647bab825977942598c0efa415de300304b",
					"src/commit/e9c32647bab825977942598c0efa415de300304b/invalid",
				)
			})
		})
	}

	// Tests /src/branch endpoint with different Git LFS scenarios.
	testSrcBranch := func(t *testing.T, isLFSEnabled bool) {
		t.Run("SrcBranch", func(t *testing.T) {
			var replaceAfter string
			if isLFSEnabled {
				replaceAfter = "media"
			} else {
				replaceAfter = "raw"
			}

			t.Run("Markup", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				var contentStr string
				if isLFSEnabled {
					contentStr = "Testing documents in LFS"
				} else {
					contentStr = "oid sha256:7b6b2c88dba9f760a1a58469b67fee2b698ef7e9399c4ca4f34a14ccbe39f623"
				}

				testFileRender(t,
					isLFSEnabled,
					false,
					false,
					"CONTRIBUTING.md",
					"/user2/lfs/src/branch/master/CONTRIBUTING.md",
					"div.file-view",
					contentStr,
					"div.file-header-right > div.buttons:nth-child(2) > a",
					"src",
					replaceAfter,
				)
			})

			t.Run("Binary", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				var contentStr string
				if isLFSEnabled {
					contentStr = locale.TrString("repo.file_view_raw")
				} else {
					contentStr = "oid sha256:2eccdb43825d2a49d99d542daa20075cff1d97d9d2349a8977efe9c03661737c"
				}

				testFileRender(t,
					isLFSEnabled,
					false,
					false,
					"crypt.bin",
					"/user2/lfs/src/branch/master/crypt.bin",
					"div.file-view",
					contentStr,
					"div.file-header-right > div > a",
					"src",
					replaceAfter,
				)
			})

			t.Run("Invalid", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				testFileRender(t,
					false,
					false,
					false,
					"invalid",
					"/user2/lfs/src/branch/master/invalid",
					"div.file-view",
					"oid sha256:9d178b5f15046343fd32f451df93acc2bdd9e6373be478b968e4cad6b6647351",
					"div.file-header-right > div > a",
					"src",
					"raw",
				)
			})

			testReadmeUsingURL(t, isLFSEnabled, "/user2/lfs/src/branch/master")
		})
	}

	// Tests /src/commit endpoint with different Git LFS scenarios.
	testSrcCommit := func(t *testing.T, isLFSEnabled bool) {
		t.Run("SrcCommit", func(t *testing.T) {
			var replaceAfter string
			if isLFSEnabled {
				replaceAfter = "media"
			} else {
				replaceAfter = "raw"
			}

			t.Run("Markup", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				var contentStr string
				if isLFSEnabled {
					contentStr = "Testing documents in LFS"
				} else {
					contentStr = "oid sha256:7b6b2c88dba9f760a1a58469b67fee2b698ef7e9399c4ca4f34a14ccbe39f623"
				}

				testFileRender(t,
					isLFSEnabled,
					false,
					false,
					"CONTRIBUTING.md",
					"/user2/lfs/src/commit/e9c32647bab825977942598c0efa415de300304b/CONTRIBUTING.md",
					"div.file-view",
					contentStr,
					"div.file-header-right > div.buttons:nth-child(2) > a",
					"src",
					replaceAfter,
				)
			})

			t.Run("Binary", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				var contentStr string
				if isLFSEnabled {
					contentStr = locale.TrString("repo.file_view_raw")
				} else {
					contentStr = "oid sha256:2eccdb43825d2a49d99d542daa20075cff1d97d9d2349a8977efe9c03661737c"
				}

				testFileRender(t,
					isLFSEnabled,
					false,
					false,
					"crypt.bin",
					"/user2/lfs/src/commit/e9c32647bab825977942598c0efa415de300304b/crypt.bin",
					"div.file-view",
					contentStr,
					"div.file-header-right > div > a",
					"src",
					replaceAfter,
				)
			})

			t.Run("Invalid", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				testFileRender(t,
					false,
					false,
					false,
					"invalid",
					"/user2/lfs/src/commit/e9c32647bab825977942598c0efa415de300304b/invalid",
					"div.file-view",
					"oid sha256:9d178b5f15046343fd32f451df93acc2bdd9e6373be478b968e4cad6b6647351",
					"div.file-header-right > div > a",
					"src",
					"raw",
				)
			})

			testReadmeUsingURL(t, isLFSEnabled, "/user2/lfs/src/commit/73cf03db6ece34e12bf91e8853dc58f678f2f82d")
		})
	}

	t.Run("WithoutLFSServer", func(t *testing.T) {
		defer test.MockVariableValue(&setting.LFS.StartServer, false)()

		testBlame(t, false)
		testCommit(t, false)
		testSrcBranch(t, false)
		testSrcCommit(t, false)
	})

	t.Run("WithLFSServer", func(t *testing.T) {
		defer test.MockVariableValue(&setting.LFS.StartServer, true)()

		testBlame(t, true)
		testCommit(t, true)
		testSrcBranch(t, true)
		testSrcCommit(t, true)
	})
}

// TestLFSLockView tests the LFS lock view on settings page of repositories
func TestLFSLockView(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})       // in org 3
	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1}) // owned by user 2
	repo3 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3}) // own by org 3
	session := loginUser(t, user2.Name)
	locale := translation.NewLocale("en-US")

	// create a lock
	lockPath := "test_lfs_lock_view.zip"
	lockID := ""
	{
		req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/%s.git/info/lfs/locks", repo3.FullName()), map[string]string{"path": lockPath})
		req.AddBasicAuth(user2.Name)
		req.Header.Set("Accept", lfs.AcceptHeader)
		req.Header.Set("Content-Type", lfs.MediaType)
		resp := MakeRequest(t, req, http.StatusCreated)
		assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
		lockResp := &api.LFSLockResponse{}
		DecodeJSON(t, resp, lockResp)
		lockID = lockResp.Lock.ID
	}
	defer func() {
		// release the lock
		req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/%s.git/info/lfs/locks/%s/unlock", repo3.FullName(), lockID), map[string]string{})
		req.AddBasicAuth(user2.Name)
		req.Header.Set("Accept", lfs.AcceptHeader)
		req.Header.Set("Content-Type", lfs.MediaType)
		resp := MakeRequest(t, req, http.StatusOK)
		assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
	}()

	t.Run("no locks message", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", fmt.Sprintf("/%s/settings/lfs/locks", repo1.FullName()))
		resp := session.MakeRequest(t, req, http.StatusOK)

		filesTable := NewHTMLParser(t, resp.Body).doc.Find("#lfs-files-locks-table")
		assert.Contains(t, filesTable.Text(), locale.TrString("repo.settings.lfs_locks_no_locks"))
	})

	t.Run("owner name", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		// make sure the display names are different, or the test is meaningless
		require.NoError(t, repo3.LoadOwner(t.Context()))
		require.NotEqual(t, user2.DisplayName(), repo3.Owner.DisplayName())

		req := NewRequest(t, "GET", fmt.Sprintf("/%s/settings/lfs/locks", repo3.FullName()))
		req.AddBasicAuth(user2.Name)
		resp := session.MakeRequest(t, req, http.StatusOK)

		doc := NewHTMLParser(t, resp.Body).doc

		tr := doc.Find("table#lfs-files-locks-table tbody tr")
		require.Equal(t, 1, tr.Length())

		td := tr.First().Find("td")
		require.Equal(t, 4, td.Length())

		// path
		assert.Equal(t, lockPath, strings.TrimSpace(td.Eq(0).Text()))
		// owner name
		assert.Equal(t, user2.DisplayName(), strings.TrimSpace(td.Eq(1).Text()))
	})
}

// Description of what can be found in a row of a Pointer view's table
type LFSPointerTestcase struct {
	blobHash   string
	oid        string
	inRepo     bool
	inStore    bool
	accessible bool
}

// Some cells contain a check or an X (as an octicon SVG). Based on the
// [LFSPointerTestcase]'s bool, we check whether the icon is as expected.
func checkOcticonIconBasedOnBoolean(t *testing.T, showCheck bool, selection *goquery.Selection) {
	classOfSelection, _ := selection.Attr("class")
	if showCheck {
		assert.Equal(t, "svg octicon-check", classOfSelection)
	} else {
		assert.Equal(t, "svg octicon-x", classOfSelection)
	}
}

// Iterates over the columns of a Pointer view table's row; rowNumber is also
// used as an index for the [LFSPointerTestcase] array.
//
// [LFSPointerTestcase.blobHash] can be left empty if not known a priori.
func iterateAndComparePointerTableCells(t *testing.T, rowNumber int, rowSelection *goquery.Selection, testcases []LFSPointerTestcase) {
	tableRowColumns := rowSelection.Find("td")
	assert.Equal(t, 7, tableRowColumns.Length())

	t.Run("OID_"+testcases[rowNumber].oid[0:9], func(t *testing.T) {
		// Important for the JavaScript form
		selectionCheckbox := rowSelection.Find("td:nth-child(1) input")
		selectionCheckboxName, _ := selectionCheckbox.Attr("name")
		assert.Contains(t, selectionCheckboxName, "oid")
		selectionCheckboxType, _ := selectionCheckbox.Attr("value")
		assert.Contains(t, selectionCheckboxType, testcases[rowNumber].oid)
		_, isSelectionCheckboxDisabled := selectionCheckbox.Attr("disabled")
		assert.Equal(t, !testcases[rowNumber].inRepo, isSelectionCheckboxDisabled)

		blobHashButton := rowSelection.Find("td:nth-child(2) a")
		blobHashButtonTitle, blobHashButtonExists := blobHashButton.Attr("title")
		assert.True(t, blobHashButtonExists)
		if testcases[rowNumber].blobHash != "" {
			assert.Contains(t, blobHashButton.Text(), testcases[rowNumber].blobHash[0:9])
			assert.Contains(t, blobHashButtonTitle, testcases[rowNumber].blobHash)
		}

		OIDButton := rowSelection.Find("td:nth-child(3) a")
		assert.Contains(t, OIDButton.Text(), testcases[rowNumber].oid[0:9])
		OIDButtonTitle, _ := OIDButton.Attr("title")
		assert.Contains(t, OIDButtonTitle, testcases[rowNumber].oid)

		inRepoStatusSVG := rowSelection.Find("td:nth-child(4) svg")
		checkOcticonIconBasedOnBoolean(t, testcases[rowNumber].inRepo, inRepoStatusSVG)
		inStoreStatusSVG := rowSelection.Find("td:nth-child(5) svg")
		checkOcticonIconBasedOnBoolean(t, testcases[rowNumber].inStore, inStoreStatusSVG)
		accessibleStatusSVG := rowSelection.Find("td:nth-child(6) svg")
		checkOcticonIconBasedOnBoolean(t, testcases[rowNumber].accessible, accessibleStatusSVG)

		// 'Find commit' button tested elsewhere.
	})
}

// Checks the Pointer view (and, as a bonus, the 'Find commits' view) of the
// lfs.git repository in the fixtures.
func TestLFSPointerViewSimple(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")
	locale := translation.NewLocale("en-US")

	// visit /user2/repo1/settings/lfs/pointer, without find commit check
	// and check if not found message is shown
	t.Run("Without LFS", func(t *testing.T) {
		t.Run("Pointer View", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", "/user2/repo1/settings/lfs/pointers")
			resp := session.MakeRequest(t, req, http.StatusOK)

			filesTable := NewHTMLParser(t, resp.Body).doc.Find("#lfs-files-table")
			assert.Contains(t, filesTable.Text(), locale.TrString("repo.settings.lfs.no_pointers"))
		})

		t.Run("Find Commit View (Valid OID)", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", "/user2/repo1/settings/lfs/find?oid=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&size=1")
			resp := session.MakeRequest(t, req, http.StatusOK)

			filesTable := NewHTMLParser(t, resp.Body).doc.Find(".user-main-content")
			assert.Contains(t, filesTable.Text(), locale.TrString("repo.settings.lfs_lfs_file_no_commits"))
			// While we're at it, why not include this as well?
			assert.Contains(t, filesTable.Text(), "LFS / aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		})

		t.Run("Find Commit View (Invalid OID)", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", "/user2/repo1/settings/lfs/find?oid=invalidoid&size=1")
			session.MakeRequest(t, req, http.StatusNotFound)
		})
	})

	// visit /user2/lfs/settings/lfs/pointer, with find commit check
	t.Run("With LFS", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		locale := translation.NewLocale("en-US")

		req := NewRequest(t, "GET", "/user2/lfs/settings/lfs/pointers")
		resp := session.MakeRequest(t, req, http.StatusOK)

		doc := NewHTMLParser(t, resp.Body).doc

		t.Run("Pointer View", func(t *testing.T) {
			t.Run("Modal", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				modal := doc.Find("dialog#lfs-files-delete.modal")
				header := modal.Find("article > header").Text()
				assert.Contains(t, header, locale.TrString("repo.settings.lfs.pointers.delete.modal.header"))
				content := modal.Find("article > div.content").Text()
				assert.Contains(t, content, locale.TrString("repo.settings.lfs.pointers.delete.modal.description"))
				cancelButton := modal.Find("article > div.actions > button.cancel").Text()
				assert.Contains(t, cancelButton, locale.TrString("settings.cancel"))
				redButton := modal.Find("article > div.actions > button.red").Text()
				assert.Contains(t, redButton, locale.TrString("org.settings.confirm_delete_account"))
			})

			t.Run("Table Header", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				tableHead := doc.Find("table#lfs-files-table > thead > tr")

				tableHeadColumns := tableHead.Find("th")
				assert.Equal(t, 7, tableHeadColumns.Length())

				checkboxClass, _ := tableHeadColumns.Find("input").First().Attr("class")
				assert.Equal(t, "select-all", checkboxClass)
				checkboxType, _ := tableHeadColumns.Find("input").First().Attr("type")
				assert.Equal(t, "checkbox", checkboxType)

				blobHashHeader := tableHead.Find("th:nth-child(2)").Text()
				assert.Equal(t, locale.TrString("repo.settings.lfs_pointers.sha"), blobHashHeader)
				oidHeader := tableHead.Find("th:nth-child(3)").Text()
				assert.Equal(t, locale.TrString("repo.settings.lfs_pointers.oid"), oidHeader)
				inRepoHeader := tableHead.Find("th:nth-child(4)").Text()
				assert.Equal(t, locale.TrString("repo.settings.lfs_pointers.inRepo"), inRepoHeader)
				inStoreHeader := tableHead.Find("th:nth-child(5)").Text()
				assert.Equal(t, locale.TrString("repo.settings.lfs_pointers.exists"), inStoreHeader)
				accessibleHeader := tableHead.Find("th:nth-child(6)").Text()
				assert.Equal(t, locale.TrString("repo.settings.lfs_pointers.accessible"), accessibleHeader)
				findCommitHeader := tableHead.Find("th:nth-child(7)").Text()
				assert.Empty(t, findCommitHeader)
			})

			t.Run("Table Contents", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				// The Pointers page shows all blobs (which are pointers) and their
				// corresponding OIDs & "LFS status". These are the items we expect to
				// find for user2's 'lfs.git' repository.
				lfsRepoTestcases := []LFSPointerTestcase{
					{
						blobHash:   "2310e4a07f9314a1a92fdfbdcd3d2884f01e96ab",
						oid:        "9d172e5c64b4f0024b9901ec6afe9ea052f3c9b6ff9f4b07956d8c48c86fca82",
						inRepo:     true,
						inStore:    true,
						accessible: true,
					},
					{
						blobHash:   "2b6c6c4eaefa24b22f2092c3d54b263ff26feb58",
						oid:        "7b6b2c88dba9f760a1a58469b67fee2b698ef7e9399c4ca4f34a14ccbe39f623",
						inRepo:     true,
						inStore:    true,
						accessible: true,
					},
					{
						blobHash:   "561a4b3072e14c9d3c26233bd8bdd9b9a5407abc",
						oid:        "1f98407553d3c43b4a9eea4982c1a673b41eccecfe574bb76d533553fdd723f0",
						inRepo:     false, // tests/e2e/fixtures/image-diff/branch.yml
						inStore:    true,
						accessible: true,
					},
					{
						blobHash:   "7421a018a7e3f15ee5691f162d0ed87dc19882f0",
						oid:        "0b8d8b5f15046343fd32f451df93acc2bdd9e6373be478b968e4cad6b6647351",
						inRepo:     true,
						inStore:    true,
						accessible: true,
					},
					{
						blobHash:   "b089e97ee59224e8c5676673c096ee4b6a8b9342",
						oid:        "9d178b5f15046343fd32f451df93acc2bdd9e6373be478b968e4cad6b6647351",
						inRepo:     false,
						inStore:    false,
						accessible: false,
					},
					{
						blobHash:   "d3472e823ad4a61a5ba8a73ab0f13669bdb92d2c",
						oid:        "fb120a8de3b12793515dec8667db380383f1c20429a25f800a3a247212f1577f",
						inRepo:     false, // tests/e2e/fixtures/image-diff/branch.yml
						inStore:    true,
						accessible: true,
					},
					{
						blobHash:   "d4a41a0d4db4949e129bd22f871171ea988103ef",
						oid:        "2eccdb43825d2a49d99d542daa20075cff1d97d9d2349a8977efe9c03661737c",
						inRepo:     true,
						inStore:    true,
						accessible: true,
					},
				}

				tableRows := doc.Find("table#lfs-files-table > tbody > tr")
				assert.Equal(t, 7, tableRows.Length())
				tableRowsIter := tableRows.EachIter()

				for rowNumber, rowSelection := range tableRowsIter {
					iterateAndComparePointerTableCells(t, rowNumber, rowSelection, lfsRepoTestcases)
				}
			})
		})

		t.Run("Find Commit View", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// follow the first link to /user2/lfs/settings/lfs/find?oid=....
			// (to get to the 'Find commits' view)
			filesTable := doc.Find("#lfs-files-table")
			assert.Contains(t, filesTable.Text(), locale.TrString("repo.settings.lfs_findcommits"))
			lfsFind := filesTable.Find(`.primary.button[href^="/user2"]`)
			assert.Positive(t, lfsFind.Length())
			lfsFindPath, exists := lfsFind.First().Attr("href")
			assert.True(t, exists)

			assert.Contains(t, lfsFindPath, "oid=")
			findCommitReq := NewRequest(t, "GET", lfsFindPath)
			findCommitResp := session.MakeRequest(t, findCommitReq, http.StatusOK)
			findCommitDoc := NewHTMLParser(t, findCommitResp.Body).doc

			lfsFilename := findCommitDoc.Find(`a[href^="/user2/lfs/src/commit/73cf03db6ece34e12bf91e8853dc58f678f2f82d/subdir/README.md"]`).Text()
			assert.NotNil(t, lfsFilename, "could not find file link")
			assert.Equal(t, "subdir/README.md", lfsFilename, "incorrect file name")

			branchString := findCommitDoc.Find(`a[href^="/user2/lfs/src/branch/master"]`).Text()
			assert.NotNil(t, branchString, "could not find branch link")
			assert.Equal(t, "master", branchString, "incorrect branch")

			shortShaString := findCommitDoc.Find(`a[href^="/user2/lfs/commit/73cf03db6ece34e12bf91e8853dc58f678f2f82d"]`).Text()
			assert.NotNil(t, shortShaString, "could not find sha link")
			assert.Equal(t, "73cf03db6e", shortShaString, "incorrect sha")

			assert.Equal(t, "2022-12-21", findCommitDoc.Find(`td[data-test-name="date"]`).Text())
		})
	})
}

// Like [TestLFSPointerViewSimple], but a new repository is initialized with a
// pointer to a (initially nonexistent object), then the object is stored in
// the LFS content store, then the object is deleted. At each step, we check the
// pointer view (but we don't check the blob hash).
func TestLFSPointerViewCreateAndDelete(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		session := loginUser(t, "user2")

		content := []byte("A very small file\n")
		pointer, err := lfs.GeneratePointer(bytes.NewReader(content))
		require.NoError(t, err)

		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

		strayPointer := fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", pointer.Oid, pointer.Size)
		repo := forgery.CreateRepository(
			t,
			user2,
			&forgery.CreateRepositoryOptions{
				Name: "lfs-addfile-repo",
				Files: forgery.MapFS{
					"lfs_pointer": forgery.MapFile(strayPointer),
				},
			},
		)

		pointersURL := fmt.Sprintf("/%s/%s/settings/lfs/pointers", repo.OwnerName, repo.Name)
		getTableRows := func() *goquery.Selection {
			req := NewRequest(t, "GET", pointersURL)
			resp := session.MakeRequest(t, req, http.StatusOK)

			tableRows := NewHTMLParser(t, resp.Body).doc.Find("table#lfs-files-table > tbody > tr")
			assert.Equal(t, 1, tableRows.Length())

			return tableRows
		}

		t.Run("InvalidPointer", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			tableRows := getTableRows()

			testcase := []LFSPointerTestcase{{
				blobHash:   "",
				oid:        pointer.Oid,
				inRepo:     false,
				inStore:    false,
				accessible: false,
			}}

			iterateAndComparePointerTableCells(t, 0, tableRows, testcase)
		})

		t.Run("AddFileToStoreThenCheckView", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			oid := storeObjectInRepo(t, repo.ID, &content)
			require.Equal(t, pointer.Oid, oid)

			tableRows := getTableRows()

			testcase := []LFSPointerTestcase{{
				blobHash:   "",
				oid:        pointer.Oid,
				inRepo:     true,
				inStore:    true,
				accessible: true,
			}}

			iterateAndComparePointerTableCells(t, 0, tableRows, testcase)
		})

		t.Run("DeleteLFSFileAndCheckAgain", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			deleteOidURL := fmt.Sprintf("/%s/%s/settings/lfs/delete/%s", repo.OwnerName, repo.Name, pointer.Oid)
			deleteRequest := NewRequest(t, "POST", deleteOidURL)

			payload := url.Values{"oid": {pointer.Oid}}
			deleteRequest.PostForm = payload
			session.MakeRequest(t, deleteRequest, http.StatusSeeOther)

			tableRows := getTableRows()

			testcase := []LFSPointerTestcase{{
				blobHash:   "",
				oid:        pointer.Oid,
				inRepo:     false,
				inStore:    false,
				accessible: false,
			}}

			iterateAndComparePointerTableCells(t, 0, tableRows, testcase)
		})

		t.Run("CasesWhereDeleteShouldReturn404NotFound", func(t *testing.T) {
			// Before performing a delete, our backend must perform a pointer validity
			// check.
			t.Run("DeleteInvalidPointer", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				invalidOid := "forgejofish"
				deleteOidURL := fmt.Sprintf("/%s/%s/settings/lfs/delete/%s", repo.OwnerName, repo.Name, invalidOid)
				deleteRequest := NewRequest(t, "POST", deleteOidURL)

				payload := url.Values{"oid": {invalidOid}}
				deleteRequest.PostForm = payload
				session.MakeRequest(t, deleteRequest, http.StatusNotFound)
			})

			// Even if the OID is an alphanumeric string with a length of 64, a
			// 'database association' between the OID and the repository must exist.
			//
			// Otherwise, 404.
			t.Run("DeleteUnassociatedPointer", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				unassociatedOid := "123412341234123412341234123412341234123412341234123412341234123"
				deleteOidURL := fmt.Sprintf("/%s/%s/settings/lfs/delete/%s", repo.OwnerName, repo.Name, unassociatedOid)
				deleteRequest := NewRequest(t, "POST", deleteOidURL)

				payload := url.Values{"oid": {unassociatedOid}}
				deleteRequest.PostForm = payload
				session.MakeRequest(t, deleteRequest, http.StatusNotFound)
			})
		})
	})
}
