// Copyright 2026 The Forgejo Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRangeDiff(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		session := loginUser(t, user2.LoginName)

		t.Run("Success", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			urls := []string{
				"/user2/commitsonpr/range-diff/cbff181af4c9c7fee3cf6c106699e07d9a3f54e6..1978192d98bb1b65e11c2cf37da854fbf94bffd6/cbff181af4c9c7fee3cf6c106699e07d9a3f54e6..9b93963cf6de4dc33f915bb67f192d099c301f43",
				"/user2/commitsonpr/range-diff/cbff181..1978192/cbff181..9b93963", // short refs should still be resolved and render full-length links
			}
			for _, u := range urls {
				req := NewRequest(t, "GET", u)
				resp := session.MakeRequest(t, req, http.StatusOK)
				doc := NewHTMLParser(t, resp.Body)

				// "Comparing commit set from a..b against commit set from c..d"; check that links exist:
				doc.AssertElement(t, ".range-diff-description a:nth-child(1)[href='/user2/commitsonpr/commit/cbff181af4c9c7fee3cf6c106699e07d9a3f54e6']", true)
				doc.AssertElement(t, ".range-diff-description a:nth-child(2)[href='/user2/commitsonpr/commit/1978192d98bb1b65e11c2cf37da854fbf94bffd6']", true)
				doc.AssertElement(t, ".range-diff-description a:nth-child(3)[href='/user2/commitsonpr/commit/cbff181af4c9c7fee3cf6c106699e07d9a3f54e6']", true)
				doc.AssertElement(t, ".range-diff-description a:nth-child(4)[href='/user2/commitsonpr/commit/9b93963cf6de4dc33f915bb67f192d099c301f43']", true)
			}
		})

		t.Run("Malformed URL", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			// "a..b" missing two components in either commit set:
			session.MakeRequest(t, NewRequest(t, "GET", "/user2/commitsonpr/range-diff/cbff181/cbff181..9b93963"), http.StatusBadRequest)
			session.MakeRequest(t, NewRequest(t, "GET", "/user2/commitsonpr/range-diff/cbff181..1978192/cbff181"), http.StatusBadRequest)
		})

		t.Run("Missing Ref", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			session.MakeRequest(t, NewRequest(t, "GET", "/user2/commitsonpr/range-diff/bad..1978192/cbff181..9b93963"), http.StatusNotFound)
			session.MakeRequest(t, NewRequest(t, "GET", "/user2/commitsonpr/range-diff/cbff181..bad/cbff181..9b93963"), http.StatusNotFound)
			session.MakeRequest(t, NewRequest(t, "GET", "/user2/commitsonpr/range-diff/cbff181..1978192/bad..9b93963"), http.StatusNotFound)
			session.MakeRequest(t, NewRequest(t, "GET", "/user2/commitsonpr/range-diff/cbff181..1978192/cbff181..bad"), http.StatusNotFound)
		})

		t.Run("Display Each Commit Modification Type", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
				Files: forgery.MapFS{
					"README.md": forgery.MapFile("This is the base README file.\n"),
				},
			})

			// Clone to create a working directory for editing.
			repoURL := fmt.Sprintf("%s%s.git", u.String(), repo.FullName())
			clonePath := forcePushRefClone(t, repoURL, user2.Name)
			leftBaseCommitID := forcePushRefRevParse(t, clonePath, "main")

			// From the base branch, we want to create two series of commits that are similar, but exercise each of the
			// commit differences: equal, added, removed, and changed.
			leftBranch, rightBranch := "left-branch", "right-branch"
			branches := []string{leftBranch, rightBranch}

			// Left branch creation:
			// "Add CONTRIBUTING.md" commit; this one will remain equal between the range-diff
			require.NoError(t, git.NewCommand(t.Context(), "switch", "-c").AddDynamicArguments(leftBranch, "main").Run(&git.RunOpts{Dir: clonePath}))
			require.NoError(t, os.WriteFile(filepath.Join(clonePath, "CONTRIBUTING.md"), []byte("This is CONTRIBUTING.md.\n"), 0o600))
			require.NoError(t, git.NewCommand(t.Context(), "add", "CONTRIBUTING.md").Run(&git.RunOpts{Dir: clonePath}))
			require.NoError(t, git.NewCommand(t.Context(), "commit", "-m", "Add CONTRIBUTING.md").Run(&git.RunOpts{Dir: clonePath}))
			equalCommitLeftID := forcePushRefRevParse(t, clonePath, leftBranch)
			// "Update LICENSE.txt" commit; this commit will be present in the left commit series, but absent in the right commit series.
			require.NoError(t, git.NewCommand(t.Context(), "switch").AddDynamicArguments(leftBranch).Run(&git.RunOpts{Dir: clonePath}))
			require.NoError(t, os.WriteFile(filepath.Join(clonePath, "LICENSE.txt"), []byte("This is LICENSE.txt.\n"), 0o600))
			require.NoError(t, git.NewCommand(t.Context(), "add", "LICENSE.txt").Run(&git.RunOpts{Dir: clonePath}))
			require.NoError(t, git.NewCommand(t.Context(), "commit", "-m", "Update LICENSE.txt").Run(&git.RunOpts{Dir: clonePath}))
			absentCommitLeftID := forcePushRefRevParse(t, clonePath, leftBranch)
			// "Add unit tests" commit; this commit will be present in both branches but modified.
			require.NoError(t, git.NewCommand(t.Context(), "switch").AddDynamicArguments(leftBranch).Run(&git.RunOpts{Dir: clonePath}))
			require.NoError(t, os.WriteFile(filepath.Join(clonePath, "main_test.py"), []byte("Line 1\nLine 2\nLine 3\nLine 4\nLine 5\n"), 0o600))
			require.NoError(t, git.NewCommand(t.Context(), "add", "main_test.py").Run(&git.RunOpts{Dir: clonePath}))
			require.NoError(t, git.NewCommand(t.Context(), "commit", "-m", "Add unit tests").Run(&git.RunOpts{Dir: clonePath}))
			modifiedCommitLeftID := forcePushRefRevParse(t, clonePath, leftBranch)

			// Create a new base commit in `main`, so that range-diff has different bases between the two commit ranges:
			require.NoError(t, git.NewCommand(t.Context(), "switch", "main").Run(&git.RunOpts{Dir: clonePath}))
			require.NoError(t, git.NewCommand(t.Context(), "commit", "--allow-empty", "-m", "empty main commit").Run(&git.RunOpts{Dir: clonePath}))
			rightBaseCommitID := forcePushRefRevParse(t, clonePath, "main")

			// Right branch creation:
			require.NoError(t, git.NewCommand(t.Context(), "switch", "-c").AddDynamicArguments(rightBranch, "main").Run(&git.RunOpts{Dir: clonePath}))
			// Import equal commit
			require.NoError(t, git.NewCommand(t.Context(), "cherry-pick").AddDynamicArguments(equalCommitLeftID).Run(&git.RunOpts{Dir: clonePath}))
			equalCommitRightID := forcePushRefRevParse(t, clonePath, rightBranch)
			// Create a new commit that is unique to the right branch
			require.NoError(t, os.WriteFile(filepath.Join(clonePath, "RELEASE-NOTES.md"), []byte("This is RELEASE-NOTES.md.\n"), 0o600))
			require.NoError(t, git.NewCommand(t.Context(), "add", "RELEASE-NOTES.md").Run(&git.RunOpts{Dir: clonePath}))
			require.NoError(t, git.NewCommand(t.Context(), "commit", "-m", "Create RELEASE-NOTES.md").Run(&git.RunOpts{Dir: clonePath}))
			addedCommitRightID := forcePushRefRevParse(t, clonePath, rightBranch)
			// Import and tweak the "Add unit tests" commit:
			require.NoError(t, git.NewCommand(t.Context(), "cherry-pick").AddDynamicArguments(modifiedCommitLeftID).Run(&git.RunOpts{Dir: clonePath}))
			require.NoError(t, git.NewCommand(t.Context(), "commit", "--amend", "-m", "Add unit tests (in Python)").Run(&git.RunOpts{Dir: clonePath}))
			modifiedCommitRightID := forcePushRefRevParse(t, clonePath, rightBranch)

			leftHeadCommitID := forcePushRefRevParse(t, clonePath, leftBranch)
			rightHeadCommitID := forcePushRefRevParse(t, clonePath, rightBranch)
			for _, branch := range branches {
				require.NoError(t, git.NewCommand(t.Context(), "push", "origin").AddDynamicArguments(branch).Run(&git.RunOpts{Dir: clonePath}))
			}

			req := NewRequestf(t, "GET", "/user2/%s/range-diff/%s..%s/%s..%s", repo.Name, leftBaseCommitID, leftHeadCommitID, rightBaseCommitID, rightHeadCommitID)
			resp := session.MakeRequest(t, req, http.StatusOK)
			doc := NewHTMLParser(t, resp.Body)
			doc.AssertElement(t, "#clean-rebase", false)

			doc.AssertElementPredicate(t, "#range-diff-list .flex-item:nth-child(1)", func(listItem *goquery.Selection) {
				title := listItem.Find(".flex-item-title")
				require.Equal(t, 1, title.Length())
				assert.Equal(t, "Add CONTRIBUTING.md", title.Text())

				body := listItem.Find(".flex-item-body")
				require.Equal(t, 1, body.Length())
				assert.Contains(t, body.Text(), "has the same content as commit")
				assert.Equal(t, 1, body.Find(fmt.Sprintf("a[href='/user2/%s/commit/%s']", repo.Name, equalCommitLeftID)).Length())
				assert.Equal(t, 1, body.Find(fmt.Sprintf("a[href='/user2/%s/commit/%s']", repo.Name, equalCommitRightID)).Length())
			})

			doc.AssertElementPredicate(t, "#range-diff-list .flex-item:nth-child(2)", func(listItem *goquery.Selection) {
				title := listItem.Find(".flex-item-title")
				require.Equal(t, 1, title.Length())
				assert.Equal(t, "Update LICENSE.txt", title.Text())

				body := listItem.Find(".flex-item-body")
				require.Equal(t, 1, body.Length())
				assert.Contains(t, body.Text(), "was removed from the commit set")
				assert.Equal(t, 1, body.Find(fmt.Sprintf("a[href='/user2/%s/commit/%s']", repo.Name, absentCommitLeftID)).Length())
			})

			doc.AssertElementPredicate(t, "#range-diff-list .flex-item:nth-child(3)", func(listItem *goquery.Selection) {
				title := listItem.Find(".flex-item-title")
				require.Equal(t, 1, title.Length())
				assert.Equal(t, "Create RELEASE-NOTES.md", title.Text())

				body := listItem.Find(".flex-item-body")
				require.Equal(t, 1, body.Length())
				assert.Contains(t, body.Text(), "was added to the commit set")
				assert.Equal(t, 1, body.Find(fmt.Sprintf("a[href='/user2/%s/commit/%s']", repo.Name, addedCommitRightID)).Length())
			})

			doc.AssertElementPredicate(t, "#range-diff-list .flex-item:nth-child(4)", func(listItem *goquery.Selection) {
				title := listItem.Find(".flex-item-title")
				require.Equal(t, 1, title.Length())
				assert.Equal(t, "Add unit tests", title.Text())

				body := listItem.Find(".flex-item-body")
				require.Equal(t, 1, body.Length())
				assert.Contains(t, body.Text(), "was modified into the commit")
				assert.Equal(t, 1, body.Find(fmt.Sprintf("a[href='/user2/%s/commit/%s']", repo.Name, modifiedCommitLeftID)).Length())
				assert.Equal(t, 1, body.Find(fmt.Sprintf("a[href='/user2/%s/commit/%s']", repo.Name, modifiedCommitRightID)).Length())
			})
		})

		t.Run("Limit Range-Diff Size", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
				Files: forgery.MapFS{
					"README.md": forgery.MapFile("This is the base README file.\n"),
				},
			})

			// Clone to create a working directory for editing.
			repoURL := fmt.Sprintf("%s%s.git", u.String(), repo.FullName())
			clonePath := forcePushRefClone(t, repoURL, user2.Name)
			baseCommitID := forcePushRefRevParse(t, clonePath, "main")

			// Create a branch with > 100 commits on it:
			require.NoError(t, git.NewCommand(t.Context(), "switch", "-c", "branch", "main").Run(&git.RunOpts{Dir: clonePath}))
			for i := range 102 {
				require.NoError(t, git.NewCommand(t.Context(), "commit", "--allow-empty", "-m").AddDynamicArguments(fmt.Sprintf("commit %d", i)).Run(&git.RunOpts{Dir: clonePath}))
			}
			headCommitID := forcePushRefRevParse(t, clonePath, "branch")
			require.NoError(t, git.NewCommand(t.Context(), "push", "origin", "branch").Run(&git.RunOpts{Dir: clonePath}))

			session.MakeRequest(t,
				NewRequestf(t, "GET", "/user2/%s/range-diff/%s..%s/%s..%s", repo.Name, "main", "main", baseCommitID, headCommitID),
				http.StatusBadRequest)
			session.MakeRequest(t,
				NewRequestf(t, "GET", "/user2/%s/range-diff/%s..%s/%s..%s", repo.Name, baseCommitID, headCommitID, "main", "main"),
				http.StatusBadRequest)
		})

		t.Run("Clean Rebase", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
				Files: forgery.MapFS{
					"README.md": forgery.MapFile("This is the base README file.\n"),
				},
			})

			// Clone to create a working directory for editing.
			repoURL := fmt.Sprintf("%s%s.git", u.String(), repo.FullName())
			clonePath := forcePushRefClone(t, repoURL, user2.Name)
			leftBaseCommitID := forcePushRefRevParse(t, clonePath, "main")

			// From the base branch, we want to create two series of commits that are similar, but exercise each of the
			// commit differences: equal, added, removed, and changed.
			leftBranch, rightBranch := "left-branch", "right-branch"
			branches := []string{leftBranch, rightBranch}

			// Left branch creation:
			require.NoError(t, git.NewCommand(t.Context(), "switch", "-c").AddDynamicArguments(leftBranch, "main").Run(&git.RunOpts{Dir: clonePath}))
			require.NoError(t, os.WriteFile(filepath.Join(clonePath, "CONTRIBUTING.md"), []byte("This is CONTRIBUTING.md.\n"), 0o600))
			require.NoError(t, git.NewCommand(t.Context(), "add", "CONTRIBUTING.md").Run(&git.RunOpts{Dir: clonePath}))
			require.NoError(t, git.NewCommand(t.Context(), "commit", "-m", "Add CONTRIBUTING.md").Run(&git.RunOpts{Dir: clonePath}))
			equalCommitLeftID := forcePushRefRevParse(t, clonePath, leftBranch)

			// Create a new base commit in `main`, so that range-diff has different bases between the two commit ranges:
			require.NoError(t, git.NewCommand(t.Context(), "switch", "main").Run(&git.RunOpts{Dir: clonePath}))
			require.NoError(t, git.NewCommand(t.Context(), "commit", "--allow-empty", "-m", "empty main commit").Run(&git.RunOpts{Dir: clonePath}))
			rightBaseCommitID := forcePushRefRevParse(t, clonePath, "main")

			// Right branch creation:
			require.NoError(t, git.NewCommand(t.Context(), "switch", "-c").AddDynamicArguments(rightBranch, "main").Run(&git.RunOpts{Dir: clonePath}))
			require.NoError(t, git.NewCommand(t.Context(), "cherry-pick").AddDynamicArguments(equalCommitLeftID).Run(&git.RunOpts{Dir: clonePath}))

			leftHeadCommitID := forcePushRefRevParse(t, clonePath, leftBranch)
			rightHeadCommitID := forcePushRefRevParse(t, clonePath, rightBranch)
			for _, branch := range branches {
				require.NoError(t, git.NewCommand(t.Context(), "push", "origin").AddDynamicArguments(branch).Run(&git.RunOpts{Dir: clonePath}))
			}

			req := NewRequestf(t, "GET", "/user2/%s/range-diff/%s..%s/%s..%s", repo.Name, leftBaseCommitID, leftHeadCommitID, rightBaseCommitID, rightHeadCommitID)
			resp := session.MakeRequest(t, req, http.StatusOK)
			doc := NewHTMLParser(t, resp.Body)

			doc.AssertElement(t, "#clean-rebase", true)
		})
	})
}
