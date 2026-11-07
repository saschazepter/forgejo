// Copyright 2021 The Gitea Authors. All rights reserved.
// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	asymkey_model "forgejo.org/models/asymkey"
	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	"forgejo.org/modules/translation"
	app_context "forgejo.org/services/context"
	doctor "forgejo.org/services/doctor"
	migrations_allowlist "forgejo.org/services/migrations/allowlist"
	mirror_service "forgejo.org/services/mirror"
	repo_service "forgejo.org/services/repository"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPushMirrorRedactCredential(t *testing.T) {
	defer test.MockVariableValue(&setting.Mirror.Enabled, true)()
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")
	cloneAddr := "https://:TOKEN@example.com/example/example.git"

	t.Run("Web route", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		resp := session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user2/repo1/settings", map[string]string{
			"action":               "push-mirror-add",
			"push_mirror_address":  cloneAddr,
			"push_mirror_interval": "0",
		}), http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)
		assert.Contains(t,
			htmlDoc.doc.Find(".ui.negative.message").Text(),
			translation.NewLocale("en-US").Tr("migrate.form.error.url_credentials"),
		)
	})

	t.Run("API route", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
		resp := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/user2/repo1/push_mirrors", &api.CreatePushMirrorOption{
			RemoteAddress: cloneAddr,
			Interval:      "0",
		}).AddTokenAuth(token), http.StatusBadRequest)

		var respBody map[string]any
		DecodeJSON(t, resp, &respBody)

		assert.Equal(t, "The URL contains credentials", respBody["message"])
	})
}

func TestMirrorPush(t *testing.T) {
	onApplicationRun(t, testMirrorPush)
}

func testMirrorPush(t *testing.T, u *url.URL) {
	defer test.MockVariableValue(&setting.Migrations.AllowLocalNetworks, true)()
	defer test.MockVariableValue(&setting.Migrations.AllowUnencrypted, true)()

	require.NoError(t, migrations_allowlist.Init())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	srcRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	mirrorRepo, err := repo_service.CreateRepositoryDirectly(db.DefaultContext, user, user, repo_service.CreateRepoOptions{
		Name: "test-push-mirror",
	})
	require.NoError(t, err)

	ctx := NewAPITestContext(t, user.LowerName, srcRepo.Name, auth_model.AccessTokenScopeReadRepository)

	doCreatePushMirror(ctx, fmt.Sprintf("%s%s/%s", u.String(), url.PathEscape(ctx.Username), url.PathEscape(mirrorRepo.Name)), user.LowerName, userPassword)(t)
	doCreatePushMirror(ctx, fmt.Sprintf("%s%s/%s", u.String(), url.PathEscape(ctx.Username), url.PathEscape("does-not-matter")), user.LowerName, userPassword)(t)

	mirrors, _, err := repo_model.GetPushMirrorsByRepoID(db.DefaultContext, srcRepo.ID, db.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, mirrors, 2)

	_, _, err = git.NewCommand(t.Context(), "config", "--get-all").AddDynamicArguments("remote." + mirrors[0].RemoteName + ".fetch").RunStdString(&git.RunOpts{Dir: srcRepo.RepoPath()})
	require.True(t, git.IsErrorExitCode(err, 1), "push mirror remote must not have a fetch refspec")

	_, _, err = git.NewCommand(t.Context(), "config", "--add").AddDynamicArguments("remote."+mirrors[0].RemoteName+".fetch", "+refs/*:refs/*").RunStdString(&git.RunOpts{Dir: srcRepo.RepoPath()})
	require.NoError(t, err)

	ok := mirror_service.SyncPushMirror(t.Context(), mirrors[0].ID)
	assert.True(t, ok)
	_, _, err = git.NewCommand(t.Context(), "config", "--get-all").AddDynamicArguments("remote." + mirrors[0].RemoteName + ".fetch").RunStdString(&git.RunOpts{Dir: srcRepo.RepoPath()})
	require.True(t, git.IsErrorExitCode(err, 1), "sync must remove legacy fetch refspecs")

	srcGitRepo, err := gitrepo.OpenRepository(git.DefaultContext, srcRepo)
	require.NoError(t, err)
	defer srcGitRepo.Close()

	srcCommit, err := srcGitRepo.GetBranchCommit("master")
	require.NoError(t, err)

	mirrorGitRepo, err := gitrepo.OpenRepository(git.DefaultContext, mirrorRepo)
	require.NoError(t, err)
	defer mirrorGitRepo.Close()

	mirrorCommit, err := mirrorGitRepo.GetBranchCommit("master")
	require.NoError(t, err)

	assert.Equal(t, srcCommit.ID, mirrorCommit.ID)

	// Test that we can "repair" push mirrors where the remote doesn't exist in git's state.
	// To do that, we artificially remove the remote...
	cmd := git.NewCommand(db.DefaultContext, "remote", "rm").AddDynamicArguments(mirrors[0].RemoteName)
	_, _, err = cmd.RunStdString(&git.RunOpts{Dir: srcRepo.RepoPath()})
	require.NoError(t, err)

	// ...then ensure that trying to get its remote address fails
	_, err = repo_model.GetPushMirrorRemoteAddress(srcRepo.OwnerName, srcRepo.Name, mirrors[0].RemoteName)
	require.Error(t, err)

	// ...and that we can fix it.
	err = doctor.FixPushMirrorsWithoutGitRemote(db.DefaultContext, nil, true)
	require.NoError(t, err)

	// ...and after fixing, we only have one remote
	mirrors, _, err = repo_model.GetPushMirrorsByRepoID(db.DefaultContext, srcRepo.ID, db.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, mirrors, 1)

	// ...one we can get the address of, and it's not the one we removed
	remoteAddress, err := repo_model.GetPushMirrorRemoteAddress(srcRepo.OwnerName, srcRepo.Name, mirrors[0].RemoteName)
	require.NoError(t, err)
	assert.Contains(t, remoteAddress, "does-not-matter")

	// Cleanup
	doRemovePushMirror(ctx, fmt.Sprintf("%s%s/%s", u.String(), url.PathEscape(ctx.Username), url.PathEscape(mirrorRepo.Name)), user.LowerName, userPassword, int(mirrors[0].ID))(t)
	mirrors, _, err = repo_model.GetPushMirrorsByRepoID(db.DefaultContext, srcRepo.ID, db.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, mirrors)
}

func doCreatePushMirror(ctx APITestContext, address, username, password string) func(t *testing.T) {
	return func(t *testing.T) {
		req := NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/%s/settings", url.PathEscape(ctx.Username), url.PathEscape(ctx.Reponame)), map[string]string{
			"action":               "push-mirror-add",
			"push_mirror_address":  address,
			"push_mirror_username": username,
			"push_mirror_password": password,
			"push_mirror_interval": "0",
		})
		ctx.Session.MakeRequest(t, req, http.StatusSeeOther)

		flashCookie := ctx.Session.GetCookie(app_context.CookieNameFlash)
		assert.NotNil(t, flashCookie)
		assert.Contains(t, flashCookie.Value, "success")
	}
}

func doCreatePushMirrorWithBranchFilter(ctx APITestContext, address, username, password, branchFilter string) func(t *testing.T) {
	return func(t *testing.T) {
		req := NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/%s/settings", url.PathEscape(ctx.Username), url.PathEscape(ctx.Reponame)), map[string]string{
			"action":                    "push-mirror-add",
			"push_mirror_address":       address,
			"push_mirror_username":      username,
			"push_mirror_password":      password,
			"push_mirror_interval":      "0",
			"push_mirror_branch_filter": branchFilter,
		})
		ctx.Session.MakeRequest(t, req, http.StatusSeeOther)

		flashCookie := ctx.Session.GetCookie(app_context.CookieNameFlash)
		assert.NotNil(t, flashCookie)
		assert.Contains(t, flashCookie.Value, "success")
	}
}

func doRemovePushMirror(ctx APITestContext, address, username, password string, pushMirrorID int) func(t *testing.T) {
	return func(t *testing.T) {
		req := NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/%s/settings", url.PathEscape(ctx.Username), url.PathEscape(ctx.Reponame)), map[string]string{
			"action":               "push-mirror-remove",
			"push_mirror_id":       strconv.Itoa(pushMirrorID),
			"push_mirror_address":  address,
			"push_mirror_username": username,
			"push_mirror_password": password,
			"push_mirror_interval": "0",
		})
		ctx.Session.MakeRequest(t, req, http.StatusSeeOther)

		flashCookie := ctx.Session.GetCookie(app_context.CookieNameFlash)
		assert.NotNil(t, flashCookie)
		assert.Contains(t, flashCookie.Value, "success")
	}
}

func TestSSHPushMirror(t *testing.T) {
	_, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("SSH executable not present")
	}

	onApplicationRun(t, func(t *testing.T, _ *url.URL) {
		defer test.MockVariableValue(&setting.Migrations.AllowLocalNetworks, true)()
		defer test.MockVariableValue(&setting.Mirror.Enabled, true)()
		defer test.MockVariableValue(&setting.SSH.RootPath, t.TempDir())()
		require.NoError(t, migrations_allowlist.Init())

		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		srcRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
		assert.False(t, srcRepo.HasWiki())
		sess := loginUser(t, user.Name)

		pushToRepo := forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{})
		sshURL := fmt.Sprintf("ssh://%s@%s/%s.git", setting.SSH.User, net.JoinHostPort(setting.SSH.ListenHost, strconv.Itoa(setting.SSH.ListenPort)), pushToRepo.FullName())

		t.Run("Mutual exclusive", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/settings", srcRepo.FullName()), map[string]string{
				"action":               "push-mirror-add",
				"push_mirror_address":  sshURL,
				"push_mirror_username": "username",
				"push_mirror_password": "password",
				"push_mirror_use_ssh":  "true",
				"push_mirror_interval": "0",
			})
			resp := sess.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			errMsg := htmlDoc.Find(".ui.negative.message").Text()
			assert.Contains(t, errMsg, "Cannot use public key and password based authentication in combination.")
		})

		inputSelector := `input[id="push_mirror_use_ssh"]`

		t.Run("SSH not available", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			defer test.MockVariableValue(&git.HasSSHExecutable, false)()

			req := NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/settings", srcRepo.FullName()), map[string]string{
				"action":               "push-mirror-add",
				"push_mirror_address":  sshURL,
				"push_mirror_use_ssh":  "true",
				"push_mirror_interval": "0",
			})
			resp := sess.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			errMsg := htmlDoc.Find(".ui.negative.message").Text()
			assert.Contains(t, errMsg, "SSH authentication isn't available.")

			htmlDoc.AssertElement(t, inputSelector, false)
		})

		t.Run("SSH available", func(t *testing.T) {
			req := NewRequest(t, "GET", fmt.Sprintf("/%s/settings", srcRepo.FullName()))
			resp := sess.MakeRequest(t, req, http.StatusOK)

			htmlDoc := NewHTMLParser(t, resp.Body)
			htmlDoc.AssertElement(t, inputSelector, true)
		})

		testMirrorPush := func(t *testing.T, srcRepo *repo_model.Repository, expectedSHA string) {
			t.Helper()

			pushToRepo := forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{})
			sshURL := fmt.Sprintf("ssh://%s@%s/%s.git", setting.SSH.User, net.JoinHostPort(setting.SSH.ListenHost, strconv.Itoa(setting.SSH.ListenPort)), pushToRepo.FullName())

			var pushMirror *repo_model.PushMirror
			t.Run("Adding", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				req := NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/settings", srcRepo.FullName()), map[string]string{
					"action":               "push-mirror-add",
					"push_mirror_address":  sshURL,
					"push_mirror_use_ssh":  "true",
					"push_mirror_interval": "0",
				})
				sess.MakeRequest(t, req, http.StatusSeeOther)

				flashCookie := sess.GetCookie(app_context.CookieNameFlash)
				assert.NotNil(t, flashCookie)
				assert.Contains(t, flashCookie.Value, "success")

				pushMirror = unittest.AssertExistsAndLoadBean(t, &repo_model.PushMirror{RepoID: srcRepo.ID})
				assert.NotEmpty(t, pushMirror.PrivateKey)
				assert.NotEmpty(t, pushMirror.PublicKey)
			})

			publickey := ""
			t.Run("Publickey", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				req := NewRequest(t, "GET", fmt.Sprintf("/%s/settings", srcRepo.FullName()))
				resp := sess.MakeRequest(t, req, http.StatusOK)
				htmlDoc := NewHTMLParser(t, resp.Body)

				publickey = htmlDoc.Find(".ui.table td a[data-clipboard-text]").AttrOr("data-clipboard-text", "")
				assert.Equal(t, publickey, pushMirror.GetPublicKey())
			})

			t.Run("Add deploy key", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				req := NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/settings/keys", pushToRepo.FullName()), map[string]string{
					"title":       "push mirror key",
					"content":     publickey,
					"is_writable": "true",
				})
				sess.MakeRequest(t, req, http.StatusSeeOther)

				unittest.AssertExistsAndLoadBean(t, &asymkey_model.DeployKey{Name: "push mirror key", RepoID: pushToRepo.ID})
			})

			t.Run("Synchronize", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				req := NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/settings", srcRepo.FullName()), map[string]string{
					"action":         "push-mirror-sync",
					"push_mirror_id": strconv.FormatInt(pushMirror.ID, 10),
				})
				sess.MakeRequest(t, req, http.StatusSeeOther)
			})

			t.Run("Check mirrored content", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				req := NewRequest(t, "GET", fmt.Sprintf("/%s", srcRepo.FullName()))
				resp := sess.MakeRequest(t, req, http.StatusOK)
				htmlDoc := NewHTMLParser(t, resp.Body)

				assert.Contains(t, htmlDoc.Find(".shortsha").Text(), expectedSHA)
				srcBranch := strings.TrimSpace(htmlDoc.Find(".js-branch-tag-selector > div > button > span").Text())

				assert.Eventually(t, func() bool {
					pushToRepoPath := fmt.Sprintf("/%s", pushToRepo.FullName())
					if srcBranch != "" {
						pushToRepoPath = fmt.Sprintf("/%s/src/branch/%s", pushToRepo.FullName(), srcBranch)
					}
					req = NewRequest(t, "GET", pushToRepoPath)
					resp = sess.MakeRequest(t, req, NoExpectedStatus)
					htmlDoc = NewHTMLParser(t, resp.Body)

					return resp.Code == http.StatusOK && htmlDoc.Find(".shortsha").Text() == expectedSHA
				}, time.Second*30, time.Second)
			})

			t.Run("Check known host keys", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				knownHosts, err := os.ReadFile(filepath.Join(setting.SSH.RootPath, "known_hosts"))
				require.NoError(t, err)

				publicKey, err := os.ReadFile(setting.SSH.ServerHostKeys[0] + ".pub")
				require.NoError(t, err)

				assert.Contains(t, string(knownHosts), string(publicKey))
			})
		}

		t.Run("Normal", func(t *testing.T) {
			testMirrorPush(t, srcRepo, "1032bbf17f")
		})

		t.Run("LFS", func(t *testing.T) {
			defer test.MockVariableValue(&setting.LFS.StartServer, true)()

			srcRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 54})
			testMirrorPush(t, srcRepo, "e9c32647ba")
		})
	})
}

func TestPushMirrorBranchFilterWebUI(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		defer test.MockVariableValue(&setting.Migrations.AllowLocalNetworks, true)()
		defer test.MockVariableValue(&setting.Migrations.AllowUnencrypted, true)()
		defer test.MockVariableValue(&setting.Mirror.Enabled, true)()
		require.NoError(t, migrations_allowlist.Init())

		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		srcRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		sess := loginUser(t, user.Name)

		mirrorRepo := forgery.CreateRepository(t, user, nil)

		ctx := NewAPITestContext(t, user.LowerName, srcRepo.Name, auth_model.AccessTokenScopeReadRepository)
		ctx.Session = sess
		remoteAddress := fmt.Sprintf("%s%s/%s", u.String(), url.PathEscape(user.Name), url.PathEscape(mirrorRepo.Name))

		t.Run("Create push mirror with branch filter via web UI", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			doCreatePushMirrorWithBranchFilter(ctx, remoteAddress, user.LowerName, userPassword, "main,develop")(t)

			mirrors, _, err := repo_model.GetPushMirrorsByRepoID(db.DefaultContext, srcRepo.ID, db.ListOptions{})
			require.NoError(t, err)
			require.Len(t, mirrors, 1)
			assert.Equal(t, "main,develop", mirrors[0].BranchFilter)
			assert.Equal(t, remoteAddress, mirrors[0].RemoteAddress)
		})

		t.Run("Create push mirror with empty branch filter via web UI", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			remoteAddress2 := fmt.Sprintf("%s%s/%s", u.String(), url.PathEscape(user.Name), url.PathEscape("foo"))
			doCreatePushMirrorWithBranchFilter(ctx, remoteAddress2, user.LowerName, userPassword, "")(t)

			mirrors, _, err := repo_model.GetPushMirrorsByRepoID(db.DefaultContext, srcRepo.ID, db.ListOptions{})
			require.NoError(t, err)
			require.Len(t, mirrors, 2)

			var emptyMirror *repo_model.PushMirror
			for _, mirror := range mirrors {
				if mirror.RemoteAddress == remoteAddress2 {
					emptyMirror = mirror
					break
				}
			}
			require.NotNil(t, emptyMirror)
			assert.Empty(t, emptyMirror.BranchFilter)
		})

		t.Run("Verify branch filter field is visible in settings page", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", fmt.Sprintf("/%s/%s/settings", url.PathEscape(ctx.Username), url.PathEscape(ctx.Reponame)))
			resp := sess.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			htmlDoc.AssertElement(t, "input#push_mirror_branch_filter", true)
		})

		t.Run("Verify existing branch filter values are pre-populated", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", fmt.Sprintf("/%s/%s/settings", url.PathEscape(ctx.Username), url.PathEscape(ctx.Reponame)))
			resp := sess.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			// Find all edit buttons for push mirrors
			editButtons := htmlDoc.Find("button[data-modal='#push-mirror-edit-modal']")
			assert.Equal(t, 2, editButtons.Length(), "Should have 2 push mirror edit buttons")

			// Get the created mirrors from database to match with UI elements
			mirrors, _, err := repo_model.GetPushMirrorsByRepoID(db.DefaultContext, srcRepo.ID, db.ListOptions{})
			require.NoError(t, err)
			require.Len(t, mirrors, 2)

			// Create a map of remote address to branch filter for easy lookup
			expectedFilters := make(map[string]string)
			for _, mirror := range mirrors {
				expectedFilters[mirror.RemoteAddress] = mirror.BranchFilter
			}

			// Check each edit button has the correct branch filter data attribute
			editButtons.Each(func(i int, s *goquery.Selection) {
				remoteAddress, exists := s.Attr("data-modal-push-mirror-edit-address")
				assert.True(t, exists, "Edit button should have remote address data attribute")

				branchFilter, exists := s.Attr("data-modal-push-mirror-edit-branch-filter")
				assert.True(t, exists, "Edit button should have branch filter data attribute")

				expectedFilter, found := expectedFilters[remoteAddress]
				assert.True(t, found, "Remote address should match one of the created mirrors")
				assert.Equal(t, expectedFilter, branchFilter, "Branch filter should match the expected value for remote %s", remoteAddress)
			})

			// Verify the edit modal has the correct input field for branch filter editing
			htmlDoc.AssertElement(t, "#push-mirror-edit-modal input[name='push_mirror_branch_filter']", true)
		})
	})
}

func TestPushMirrorBranchFilterIntegration(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		defer test.MockVariableValue(&setting.Migrations.AllowLocalNetworks, true)()
		defer test.MockVariableValue(&setting.Migrations.AllowUnencrypted, true)()
		defer test.MockVariableValue(&setting.Mirror.Enabled, true)()
		require.NoError(t, migrations_allowlist.Init())

		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		srcRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		sess := loginUser(t, user.Name)
		token := getTokenForLoggedInUser(t, sess, auth_model.AccessTokenScopeAll)

		ctx := NewAPITestContext(t, user.LowerName, srcRepo.Name, auth_model.AccessTokenScopeReadRepository)
		ctx.Session = sess
		remoteAddress := fmt.Sprintf("%s%s/%s", u.String(), url.PathEscape(user.Name), url.PathEscape("foo"))
		urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/push_mirrors", user.LowerName, srcRepo.Name)

		t.Run("Web UI to API integration", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Create push mirror with branch filter via web UI
			doCreatePushMirrorWithBranchFilter(ctx, remoteAddress, user.LowerName, userPassword, "main,develop")(t)

			// Verify it appears in API responses
			req := NewRequest(t, "GET", urlStr).AddTokenAuth(token)
			resp := MakeRequest(t, req, http.StatusOK)
			var pushMirrors []*api.PushMirror
			DecodeJSON(t, resp, &pushMirrors)
			require.Len(t, pushMirrors, 1)
			assert.Equal(t, "main,develop", pushMirrors[0].BranchFilter)
			assert.Equal(t, remoteAddress, pushMirrors[0].RemoteAddress)

			// Verify it's stored correctly in database
			mirrors, _, err := repo_model.GetPushMirrorsByRepoID(db.DefaultContext, srcRepo.ID, db.ListOptions{})
			require.NoError(t, err)
			require.Len(t, mirrors, 1)
			assert.Equal(t, "main,develop", mirrors[0].BranchFilter)
		})

		t.Run("API to Web UI integration", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Create another mirror repo for this test
			mirrorRepo2, err := repo_service.CreateRepositoryDirectly(db.DefaultContext, user, user, repo_service.CreateRepoOptions{
				Name: "test-api-to-ui",
			})
			require.NoError(t, err)
			remoteAddress2 := fmt.Sprintf("%s%s/%s", u.String(), url.PathEscape(user.Name), url.PathEscape(mirrorRepo2.Name))

			// Create push mirror with branch filter via API
			req := NewRequestWithJSON(t, "POST", urlStr, &api.CreatePushMirrorOption{
				RemoteAddress: remoteAddress2,
				Interval:      "8h",
				BranchFilter:  "feature-*,hotfix-*",
			}).AddTokenAuth(token)
			MakeRequest(t, req, http.StatusOK)

			// Verify it's stored in database
			mirrors, _, err := repo_model.GetPushMirrorsByRepoID(db.DefaultContext, srcRepo.ID, db.ListOptions{})
			require.NoError(t, err)
			require.Len(t, mirrors, 2) // Should have 2 mirrors now

			// Find the mirror created via API
			var apiMirror *repo_model.PushMirror
			for _, mirror := range mirrors {
				if mirror.RemoteAddress == remoteAddress2 {
					apiMirror = mirror
					break
				}
			}
			require.NotNil(t, apiMirror)
			assert.Equal(t, "feature-*,hotfix-*", apiMirror.BranchFilter)

			// Verify it appears in web UI with correct branch filter data
			req = NewRequest(t, "GET", fmt.Sprintf("/%s/%s/settings", url.PathEscape(ctx.Username), url.PathEscape(ctx.Reponame)))
			resp := sess.MakeRequest(t, req, http.StatusOK)
			assert.Equal(t, http.StatusOK, resp.Code)

			// Check that the edit button has the correct data attributes for branch filter
			doc := NewHTMLParser(t, resp.Body)
			editButton := doc.Find(fmt.Sprintf(`button[data-modal-push-mirror-edit-address="%s"]`, remoteAddress2))
			require.Equal(t, 1, editButton.Length(), "Should find exactly one edit button for the API-created mirror")

			branchFilterAttr, exists := editButton.Attr("data-modal-push-mirror-edit-branch-filter")
			require.True(t, exists, "Edit button should have branch filter data attribute")
			assert.Equal(t, "feature-*,hotfix-*", branchFilterAttr, "Branch filter data attribute should match what was set via API")
		})
	})
}

func TestPushMirrorSettings(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		defer test.MockVariableValue(&setting.Migrations.AllowLocalNetworks, true)()
		defer test.MockVariableValue(&setting.Migrations.AllowUnencrypted, true)()
		defer test.MockVariableValue(&setting.Mirror.Enabled, true)()
		require.NoError(t, migrations_allowlist.Init())

		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		srcRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
		srcRepo2 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
		assert.False(t, srcRepo.HasWiki())
		sess := loginUser(t, user.Name)

		pushToRepo := forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{})

		t.Run("Adding", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/settings", srcRepo2.FullName()), map[string]string{
				"action":               "push-mirror-add",
				"push_mirror_address":  u.String() + pushToRepo.FullName(),
				"push_mirror_interval": "0",
			})
			sess.MakeRequest(t, req, http.StatusSeeOther)

			req = NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/settings", srcRepo.FullName()), map[string]string{
				"action":               "push-mirror-add",
				"push_mirror_address":  u.String() + pushToRepo.FullName(),
				"push_mirror_interval": "0",
			})
			sess.MakeRequest(t, req, http.StatusSeeOther)

			flashCookie := sess.GetCookie(app_context.CookieNameFlash)
			assert.NotNil(t, flashCookie)
			assert.Contains(t, flashCookie.Value, "success")
		})

		mirrors, _, err := repo_model.GetPushMirrorsByRepoID(db.DefaultContext, srcRepo.ID, db.ListOptions{})
		require.NoError(t, err)
		assert.Len(t, mirrors, 1)
		mirrorID := mirrors[0].ID

		mirrors, _, err = repo_model.GetPushMirrorsByRepoID(db.DefaultContext, srcRepo2.ID, db.ListOptions{})
		require.NoError(t, err)
		assert.Len(t, mirrors, 1)

		t.Run("Interval", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			unittest.AssertExistsAndLoadBean(t, &repo_model.PushMirror{ID: mirrorID - 1})

			req := NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/settings", srcRepo.FullName()), map[string]string{
				"action":               "push-mirror-update",
				"push_mirror_id":       strconv.FormatInt(mirrorID-1, 10),
				"push_mirror_interval": "10m0s",
			})
			sess.MakeRequest(t, req, http.StatusNotFound)

			req = NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/settings", srcRepo.FullName()), map[string]string{
				"action":               "push-mirror-update",
				"push_mirror_id":       strconv.FormatInt(mirrorID, 10),
				"push_mirror_interval": "10m0s",
			})
			sess.MakeRequest(t, req, http.StatusSeeOther)

			flashCookie := sess.GetCookie(app_context.CookieNameFlash)
			assert.NotNil(t, flashCookie)
			assert.Contains(t, flashCookie.Value, "success")
		})
	})
}

func TestPushMirrorBranchFilterSyncOperations(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		defer test.MockVariableValue(&setting.Migrations.AllowLocalNetworks, true)()
		defer test.MockVariableValue(&setting.Migrations.AllowUnencrypted, true)()
		defer test.MockVariableValue(&setting.Mirror.Enabled, true)()
		require.NoError(t, migrations_allowlist.Init())

		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		srcRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		sess := loginUser(t, user.Name)

		// Create test repository with multiple branches
		testRepoPath := srcRepo.RepoPath()

		// Create additional branches in source repository
		_, _, err := git.NewCommand(git.DefaultContext, "update-ref", "refs/heads/develop", "refs/heads/master").RunStdString(&git.RunOpts{Dir: testRepoPath})
		require.NoError(t, err)
		_, _, err = git.NewCommand(git.DefaultContext, "update-ref", "refs/heads/feature-auth", "refs/heads/master").RunStdString(&git.RunOpts{Dir: testRepoPath})
		require.NoError(t, err)
		_, _, err = git.NewCommand(git.DefaultContext, "update-ref", "refs/heads/feature-ui", "refs/heads/master").RunStdString(&git.RunOpts{Dir: testRepoPath})
		require.NoError(t, err)
		_, _, err = git.NewCommand(git.DefaultContext, "update-ref", "refs/heads/hotfix-123", "refs/heads/master").RunStdString(&git.RunOpts{Dir: testRepoPath})
		require.NoError(t, err)

		ctx := NewAPITestContext(t, user.LowerName, srcRepo.Name, auth_model.AccessTokenScopeReadRepository)
		ctx.Session = sess

		t.Run("Create push mirror with branch filter and trigger sync", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Create mirror repository
			mirrorRepo, err := repo_service.CreateRepositoryDirectly(db.DefaultContext, user, user, repo_service.CreateRepoOptions{
				Name: "test-sync-branch-filter",
			})
			require.NoError(t, err)
			remoteAddress := fmt.Sprintf("%s%s/%s", u.String(), url.PathEscape(user.Name), url.PathEscape(mirrorRepo.Name))

			// Create push mirror with specific branch filter via web UI
			branchFilter := "master,develop,feature-auth"
			doCreatePushMirrorWithBranchFilter(ctx, remoteAddress, user.LowerName, userPassword, branchFilter)(t)

			// Verify the push mirror was created with branch filter
			mirrors, _, err := repo_model.GetPushMirrorsByRepoID(db.DefaultContext, srcRepo.ID, db.ListOptions{})
			require.NoError(t, err)
			require.Len(t, mirrors, 1)
			assert.Equal(t, branchFilter, mirrors[0].BranchFilter)

			// Verify git remote configuration includes correct refspecs for filtered branches
			output, _, err := git.NewCommand(git.DefaultContext, "config", "--get-all").AddDynamicArguments(fmt.Sprintf("remote.%s.push", mirrors[0].RemoteName)).RunStdString(&git.RunOpts{Dir: testRepoPath})
			require.NoError(t, err)
			assert.Contains(t, output, "+refs/heads/master:refs/heads/master")
			assert.Contains(t, output, "+refs/heads/develop:refs/heads/develop")
			assert.Contains(t, output, "+refs/heads/feature-auth:refs/heads/feature-auth")
			assert.NotContains(t, output, "+refs/heads/feature-ui:refs/heads/feature-ui")
			assert.NotContains(t, output, "+refs/heads/hotfix-123:refs/heads/hotfix-123")
			assert.Contains(t, output, "+refs/tags/*:refs/tags/*") // Tags should always be pushed

			// Trigger sync operation
			ok := mirror_service.SyncPushMirror(db.DefaultContext, mirrors[0].ID)
			assert.True(t, ok)

			// Verify only filtered branches were pushed to mirror
			mirrorGitRepo, err := gitrepo.OpenRepository(git.DefaultContext, mirrorRepo)
			require.NoError(t, err)
			defer mirrorGitRepo.Close()

			// Check that filtered branches exist in mirror
			_, err = mirrorGitRepo.GetBranchCommit("master")
			require.NoError(t, err, "master branch should exist in mirror")
			_, err = mirrorGitRepo.GetBranchCommit("develop")
			require.NoError(t, err, "develop branch should exist in mirror")
			_, err = mirrorGitRepo.GetBranchCommit("feature-auth")
			require.NoError(t, err, "feature-auth branch should exist in mirror")

			// Check that non-filtered branches don't exist in mirror
			_, err = mirrorGitRepo.GetBranchCommit("feature-ui")
			require.Error(t, err, "feature-ui branch should not exist in mirror")
			_, err = mirrorGitRepo.GetBranchCommit("hotfix-123")
			require.Error(t, err, "hotfix-123 branch should not exist in mirror")
		})

		t.Run("Update branch filter and verify git remote settings are updated", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Get the existing mirror
			mirrors, _, err := repo_model.GetPushMirrorsByRepoID(db.DefaultContext, srcRepo.ID, db.ListOptions{})
			require.NoError(t, err)
			require.Len(t, mirrors, 1)
			mirror := mirrors[0]
			mirror.Repo = srcRepo

			// Update branch filter to include different branches
			mirror.BranchFilter = "master,feature-ui,hotfix-123"
			err = repo_model.UpdatePushMirror(db.DefaultContext, mirror)
			require.NoError(t, err)

			// Update git remote configuration
			err = mirror_service.UpdatePushMirrorBranchFilter(db.DefaultContext, mirror)
			require.NoError(t, err)

			// Verify git remote configuration was updated
			output, _, err := git.NewCommand(git.DefaultContext, "config", "--get-all").AddDynamicArguments(fmt.Sprintf("remote.%s.push", mirror.RemoteName)).RunStdString(&git.RunOpts{Dir: testRepoPath})
			require.NoError(t, err)
			assert.Contains(t, output, "+refs/heads/master:refs/heads/master")
			assert.Contains(t, output, "+refs/heads/feature-ui:refs/heads/feature-ui")
			assert.Contains(t, output, "+refs/heads/hotfix-123:refs/heads/hotfix-123")
			assert.NotContains(t, output, "+refs/heads/develop:refs/heads/develop")
			assert.NotContains(t, output, "+refs/heads/feature-auth:refs/heads/feature-auth")
			assert.Contains(t, output, "+refs/tags/*:refs/tags/*") // Tags should always be pushed

			// Trigger sync operation with updated filter
			ok := mirror_service.SyncPushMirror(db.DefaultContext, mirror.ID)
			assert.True(t, ok)
		})

		t.Run("Test empty branch filter pushes all branches", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Create another mirror repository for this test
			mirrorRepo2, err := repo_service.CreateRepositoryDirectly(db.DefaultContext, user, user, repo_service.CreateRepoOptions{
				Name: "test-sync-empty-filter",
			})
			require.NoError(t, err)
			remoteAddress2 := fmt.Sprintf("%s%s/%s", u.String(), url.PathEscape(user.Name), url.PathEscape(mirrorRepo2.Name))

			// Create push mirror with empty branch filter
			doCreatePushMirrorWithBranchFilter(ctx, remoteAddress2, user.LowerName, userPassword, "")(t)

			// Get the new mirror
			mirrors, _, err := repo_model.GetPushMirrorsByRepoID(db.DefaultContext, srcRepo.ID, db.ListOptions{})
			require.NoError(t, err)
			require.Len(t, mirrors, 2) // Should have 2 mirrors now

			var emptyFilterMirror *repo_model.PushMirror
			for _, mirror := range mirrors {
				if mirror.RemoteAddress == remoteAddress2 {
					emptyFilterMirror = mirror
					break
				}
			}
			require.NotNil(t, emptyFilterMirror)
			assert.Empty(t, emptyFilterMirror.BranchFilter)

			// Verify git remote configuration for empty filter (should mirror all branches)
			output, _, err := git.NewCommand(git.DefaultContext, "config", "--get-all").AddDynamicArguments(fmt.Sprintf("remote.%s.push", emptyFilterMirror.RemoteName)).RunStdString(&git.RunOpts{Dir: testRepoPath})
			require.NoError(t, err)
			assert.Contains(t, output, "+refs/heads/*:refs/heads/*") // Should mirror all branches
			assert.Contains(t, output, "+refs/tags/*:refs/tags/*")

			// Trigger sync operation
			ok := mirror_service.SyncPushMirror(db.DefaultContext, emptyFilterMirror.ID)
			assert.True(t, ok)

			// Verify all branches were pushed to mirror
			mirrorGitRepo2, err := gitrepo.OpenRepository(git.DefaultContext, mirrorRepo2)
			require.NoError(t, err)
			defer mirrorGitRepo2.Close()

			// Check that all branches exist in mirror
			_, err = mirrorGitRepo2.GetBranchCommit("master")
			require.NoError(t, err, "master branch should exist in mirror")
			_, err = mirrorGitRepo2.GetBranchCommit("develop")
			require.NoError(t, err, "develop branch should exist in mirror")
			_, err = mirrorGitRepo2.GetBranchCommit("feature-auth")
			require.NoError(t, err, "feature-auth branch should exist in mirror")
			_, err = mirrorGitRepo2.GetBranchCommit("feature-ui")
			require.NoError(t, err, "feature-ui branch should exist in mirror")
			_, err = mirrorGitRepo2.GetBranchCommit("hotfix-123")
			require.NoError(t, err, "hotfix-123 branch should exist in mirror")
		})

		t.Run("Test glob pattern branch filter in sync operations", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Create another mirror repository for this test
			mirrorRepo3, err := repo_service.CreateRepositoryDirectly(db.DefaultContext, user, user, repo_service.CreateRepoOptions{
				Name: "test-sync-glob-filter",
			})
			require.NoError(t, err)
			remoteAddress3 := fmt.Sprintf("%s%s/%s", u.String(), url.PathEscape(user.Name), url.PathEscape(mirrorRepo3.Name))

			// Create push mirror with glob pattern branch filter
			globFilter := "master,feature-*"
			doCreatePushMirrorWithBranchFilter(ctx, remoteAddress3, user.LowerName, userPassword, globFilter)(t)

			// Get the new mirror
			mirrors, _, err := repo_model.GetPushMirrorsByRepoID(db.DefaultContext, srcRepo.ID, db.ListOptions{})
			require.NoError(t, err)
			require.Len(t, mirrors, 3) // Should have 3 mirrors now

			var globMirror *repo_model.PushMirror
			for _, mirror := range mirrors {
				if mirror.RemoteAddress == remoteAddress3 {
					globMirror = mirror
					break
				}
			}
			require.NotNil(t, globMirror)
			assert.Equal(t, globFilter, globMirror.BranchFilter)

			// Verify git remote configuration includes glob pattern branches
			output, _, err := git.NewCommand(git.DefaultContext, "config", "--get-all").AddDynamicArguments(fmt.Sprintf("remote.%s.push", globMirror.RemoteName)).RunStdString(&git.RunOpts{Dir: testRepoPath})
			require.NoError(t, err)
			assert.Contains(t, output, "+refs/heads/master:refs/heads/master")
			assert.Contains(t, output, "+refs/heads/feature-*:refs/heads/feature-*")
			assert.NotContains(t, output, "+refs/heads/develop:refs/heads/develop")
			assert.NotContains(t, output, "+refs/heads/hotfix-123:refs/heads/hotfix-123")
			assert.Contains(t, output, "+refs/tags/*:refs/tags/*")

			// Trigger sync operation
			ok := mirror_service.SyncPushMirror(db.DefaultContext, globMirror.ID)
			assert.True(t, ok)

			// Verify only matching branches were pushed to mirror
			mirrorGitRepo3, err := gitrepo.OpenRepository(git.DefaultContext, mirrorRepo3)
			require.NoError(t, err)
			defer mirrorGitRepo3.Close()

			// Check that matching branches exist in mirror
			_, err = mirrorGitRepo3.GetBranchCommit("master")
			require.NoError(t, err, "master branch should exist in mirror")
			_, err = mirrorGitRepo3.GetBranchCommit("feature-auth")
			require.NoError(t, err, "feature-auth branch should exist in mirror")
			_, err = mirrorGitRepo3.GetBranchCommit("feature-ui")
			require.NoError(t, err, "feature-ui branch should exist in mirror")

			// Check that non-matching branches don't exist in mirror
			_, err = mirrorGitRepo3.GetBranchCommit("develop")
			require.Error(t, err, "develop branch should not exist in mirror")
			_, err = mirrorGitRepo3.GetBranchCommit("hotfix-123")
			require.Error(t, err, "hotfix-123 branch should not exist in mirror")
		})
	})
}

func TestPushMirrorWebUIToAPIIntegration(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		defer test.MockVariableValue(&setting.Migrations.AllowLocalNetworks, true)()
		defer test.MockVariableValue(&setting.Migrations.AllowUnencrypted, true)()
		defer test.MockVariableValue(&setting.Mirror.Enabled, true)()
		require.NoError(t, migrations_allowlist.Init())

		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		srcRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		session := loginUser(t, user.Name)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeAll)

		mirrorRepo := forgery.CreateRepository(t, user, nil)

		ctx := NewAPITestContext(t, user.LowerName, srcRepo.Name, auth_model.AccessTokenScopeReadRepository)
		ctx.Session = session
		remoteAddress := fmt.Sprintf("%s%s/%s", u.String(), url.PathEscape(user.Name), url.PathEscape(mirrorRepo.Name))
		urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/push_mirrors", user.Name, srcRepo.Name)

		t.Run("Set branch filter via web UI and verify in API response", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Create push mirror with branch filter via web UI
			branchFilter := "main,develop,feature-*"
			doCreatePushMirrorWithBranchFilter(ctx, remoteAddress, user.LowerName, userPassword, branchFilter)(t)

			// Verify via API that branch filter is set correctly
			req := NewRequest(t, "GET", urlStr).AddTokenAuth(token)
			resp := MakeRequest(t, req, http.StatusOK)
			var pushMirrors []*api.PushMirror
			DecodeJSON(t, resp, &pushMirrors)

			require.Len(t, pushMirrors, 1)
			assert.Equal(t, branchFilter, pushMirrors[0].BranchFilter, "Branch filter set via web UI should appear in API response")
			assert.Equal(t, remoteAddress, pushMirrors[0].RemoteAddress)

			// Store mirror info for cleanup
			mirrorRemoteName := pushMirrors[0].RemoteName

			// Cleanup
			req = NewRequest(t, "DELETE", fmt.Sprintf("%s/%s", urlStr, mirrorRemoteName)).AddTokenAuth(token)
			MakeRequest(t, req, http.StatusNoContent)
		})

		t.Run("Set empty branch filter via web UI and verify in API response", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Create another mirror repo for this test
			mirrorRepo2 := forgery.CreateRepository(t, user, nil)
			remoteAddress2 := fmt.Sprintf("%s%s/%s", u.String(), url.PathEscape(user.Name), url.PathEscape(mirrorRepo2.Name))

			// Create push mirror with empty branch filter via web UI
			doCreatePushMirrorWithBranchFilter(ctx, remoteAddress2, user.LowerName, userPassword, "")(t)

			// Verify via API that branch filter is empty
			req := NewRequest(t, "GET", urlStr).AddTokenAuth(token)
			resp := MakeRequest(t, req, http.StatusOK)
			var pushMirrors []*api.PushMirror
			DecodeJSON(t, resp, &pushMirrors)

			require.Len(t, pushMirrors, 1)
			assert.Empty(t, pushMirrors[0].BranchFilter, "Empty branch filter set via web UI should appear as empty in API response")
			assert.Equal(t, remoteAddress2, pushMirrors[0].RemoteAddress)

			// Cleanup
			req = NewRequest(t, "DELETE", fmt.Sprintf("%s/%s", urlStr, pushMirrors[0].RemoteName)).AddTokenAuth(token)
			MakeRequest(t, req, http.StatusNoContent)
		})

		t.Run("Set complex branch filter via web UI and verify in API response", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Create another mirror repo for this test
			mirrorRepo3 := forgery.CreateRepository(t, user, nil)
			remoteAddress3 := fmt.Sprintf("%s%s/%s", u.String(), url.PathEscape(user.Name), url.PathEscape(mirrorRepo3.Name))

			// Create push mirror with complex branch filter via web UI
			complexFilter := "main,release/v*,hotfix-*,feature-auth,feature-ui"
			doCreatePushMirrorWithBranchFilter(ctx, remoteAddress3, user.LowerName, userPassword, complexFilter)(t)

			// Verify via API that complex branch filter is preserved
			req := NewRequest(t, "GET", urlStr).AddTokenAuth(token)
			resp := MakeRequest(t, req, http.StatusOK)
			var pushMirrors []*api.PushMirror
			DecodeJSON(t, resp, &pushMirrors)

			require.Len(t, pushMirrors, 1)
			assert.Equal(t, complexFilter, pushMirrors[0].BranchFilter, "Complex branch filter set via web UI should be preserved in API response")
			assert.Equal(t, remoteAddress3, pushMirrors[0].RemoteAddress)

			// Cleanup
			req = NewRequest(t, "DELETE", fmt.Sprintf("%s/%s", urlStr, pushMirrors[0].RemoteName)).AddTokenAuth(token)
			MakeRequest(t, req, http.StatusNoContent)
		})

		t.Run("Update branch filter via API and verify in web UI", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Create another mirror repo for this test
			mirrorRepo4 := forgery.CreateRepository(t, user, nil)
			remoteAddress4 := fmt.Sprintf("%s%s/%s", u.String(), url.PathEscape(user.Name), url.PathEscape(mirrorRepo4.Name))

			// First create a push mirror via API with initial branch filter
			initialFilter := "main"
			req := NewRequestWithJSON(t, "POST", urlStr, &api.CreatePushMirrorOption{
				RemoteAddress: remoteAddress4,
				Interval:      "8h",
				BranchFilter:  initialFilter,
			}).AddTokenAuth(token)
			MakeRequest(t, req, http.StatusOK)

			// Get the created mirror info
			req = NewRequest(t, "GET", urlStr).AddTokenAuth(token)
			resp := MakeRequest(t, req, http.StatusOK)
			var pushMirrors []*api.PushMirror
			DecodeJSON(t, resp, &pushMirrors)
			require.Len(t, pushMirrors, 1)
			assert.Equal(t, initialFilter, pushMirrors[0].BranchFilter)
			mirrorRemoteName := pushMirrors[0].RemoteName

			// Get the actual mirror from database to get the ID
			dbMirrors, _, err := repo_model.GetPushMirrorsByRepoID(db.DefaultContext, srcRepo.ID, db.ListOptions{})
			require.NoError(t, err)
			require.Len(t, dbMirrors, 1)

			// Update branch filter via web form (using existing repo settings endpoint)
			updatedFilter := "main,develop,feature-*"
			req = NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/%s/settings", url.PathEscape(user.Name), url.PathEscape(srcRepo.Name)), map[string]string{
				"action":                    "push-mirror-update",
				"push_mirror_id":            fmt.Sprintf("%d", dbMirrors[0].ID),
				"push_mirror_interval":      "8h",
				"push_mirror_branch_filter": updatedFilter,
			})
			session.MakeRequest(t, req, http.StatusSeeOther)

			// Verify the branch filter was updated via API
			req = NewRequest(t, "GET", urlStr).AddTokenAuth(token)
			resp = MakeRequest(t, req, http.StatusOK)
			DecodeJSON(t, resp, &pushMirrors)
			require.Len(t, pushMirrors, 1)
			assert.Equal(t, updatedFilter, pushMirrors[0].BranchFilter, "Branch filter should be updated via web form")

			// Verify the branch filter is visible in the web UI settings page
			req = NewRequest(t, "GET", fmt.Sprintf("/%s/%s/settings", url.PathEscape(user.Name), url.PathEscape(srcRepo.Name)))
			resp = session.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)
			editButton := htmlDoc.Find(fmt.Sprintf(`button[data-modal-push-mirror-edit-address="%s"]`, remoteAddress4))
			require.Equal(t, 1, editButton.Length(), "Should find exactly one edit button for the updated mirror")

			branchFilterAttr, exists := editButton.Attr("data-modal-push-mirror-edit-branch-filter")
			require.True(t, exists, "Edit button should have branch filter data attribute")
			assert.Equal(t, updatedFilter, branchFilterAttr, "Branch filter data attribute should match the updated value")

			// Cleanup
			req = NewRequest(t, "DELETE", fmt.Sprintf("%s/%s", urlStr, mirrorRemoteName)).AddTokenAuth(token)
			MakeRequest(t, req, http.StatusNoContent)
		})

		t.Run("Multiple mirrors with different branch filters - UI to API consistency", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Create multiple mirror repos
			testCases := []struct {
				name         string
				branchFilter string
			}{
				{"multi-test-1", "main,develop"},
				{"multi-test-2", "feature-*,hotfix-*"},
				{"multi-test-3", ""},
			}
			for _, tc := range testCases {
				remoteAddress := fmt.Sprintf("%s%s/%s", u.String(), url.PathEscape(user.Name), url.PathEscape(tc.name))
				req := NewRequestWithJSON(t, "POST", urlStr, &api.CreatePushMirrorOption{
					RemoteAddress: remoteAddress,
					Interval:      "8h",
					BranchFilter:  tc.branchFilter,
				}).AddTokenAuth(token)
				MakeRequest(t, req, http.StatusOK)
			}

			// Verify all mirrors and their branch filters via API
			req := NewRequest(t, "GET", urlStr).AddTokenAuth(token)
			resp := MakeRequest(t, req, http.StatusOK)
			var pushMirrors []*api.PushMirror
			DecodeJSON(t, resp, &pushMirrors)
			require.Len(t, pushMirrors, 3)

			// Create a map for easier verification
			filterMap := make(map[string]string)
			for _, mirror := range pushMirrors {
				for _, tc := range testCases {
					if strings.Contains(mirror.RemoteAddress, tc.name) {
						filterMap[tc.name] = mirror.BranchFilter
						// createdMirrors = append(createdMirrors, mirror.RemoteName)
						break
					}
				}
			}

			// Verify each branch filter is correctly preserved
			assert.Equal(t, "main,develop", filterMap["multi-test-1"], "First mirror branch filter should match")
			assert.Equal(t, "feature-*,hotfix-*", filterMap["multi-test-2"], "Second mirror branch filter should match")
			assert.Empty(t, filterMap["multi-test-3"], "Third mirror branch filter should be empty")
		})

		t.Run("Verify branch filter field exists in web UI form", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Access the push mirror settings page
			req := NewRequest(t, "GET", fmt.Sprintf("/%s/%s/settings", url.PathEscape(user.Name), url.PathEscape(srcRepo.Name)))
			resp := session.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			htmlDoc.AssertElement(t, "#push_mirror_branch_filter", true)
		})
	})
}

func TestMirrorPushFailOnRedirect(t *testing.T) {
	defer test.MockVariableValue(&setting.Migrations.AllowUnencrypted, true)()

	// Not using MockVariableValue due to need to undo `migrations_allowlist.Init()`
	prev := setting.Migrations.AllowedDomains
	setting.Migrations.AllowedDomains = "127.0.0.1"
	migrations_allowlist.Init()
	defer func() {
		setting.Migrations.AllowedDomains = prev
		migrations_allowlist.Init()
	}()

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		srcRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

		// Target repo that the mirror may go into...
		mirrorRepo, err := repo_service.CreateRepositoryDirectly(db.DefaultContext, user, user, repo_service.CreateRepoOptions{
			Name: "test-push-mirror-address-check",
		})
		require.NoError(t, err)

		// Actual HTTP server that the mirror will go into, which will attempt to redirect to the mirrorRepo
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// w.Header().Set("Location", "https://example.com/some-user/some-repo.git")
			targetLocation := fmt.Sprintf("%s%s/%s%s", u.String(), mirrorRepo.OwnerName, mirrorRepo.Name, r.RequestURI)
			t.Logf("targetLocation = %q", targetLocation)
			w.Header().Set("Location", targetLocation)
			w.WriteHeader(301)
		}))
		t.Cleanup(s.Close)

		ctx := NewAPITestContext(t, user.LowerName, srcRepo.Name, auth_model.AccessTokenScopeReadRepository)
		doCreatePushMirror(ctx, s.URL, user.LowerName, userPassword)(t)

		mirrors, _, err := repo_model.GetPushMirrorsByRepoID(db.DefaultContext, srcRepo.ID, db.ListOptions{})
		require.NoError(t, err)
		require.Len(t, mirrors, 1)

		// Regardless of whether http.followRedirects is set or not, this sync will fail -- auth doesn't get sent to the
		// new redirected URL (great!) and would fail with a 401 -- so to ensure that we're hitting the
		// http.followRedirects=false case, check the error log output and ensure it has the expected git error.
		lc, cleanup := test.NewLogChecker(log.DEFAULT, log.ERROR)
		lc.Filter("The requested URL returned error: 301") // expected git error
		lc.StopMark("SyncPushMirror")
		defer cleanup()

		ok := mirror_service.SyncPushMirror(t.Context(), mirrors[0].ID)
		assert.False(t, ok, "expected push mirror sync to fail due to redirect")

		logFiltered, _ := lc.Check(5 * time.Second)
		assert.True(t, logFiltered[0], "expected migration error output")
	})
}

// Verifies that a push mirror which was created while the remote address was permitted will fail to sync if the
// AllowedDomains configuration later changes such that the remote URL is no longer permitted.
func TestMirrorPushAddressCheck(t *testing.T) {
	defer test.MockVariableValue(&setting.Migrations.AllowUnencrypted, true)()

	// Not using MockVariableValue due to need to undo `migrations_allowlist.Init()`
	prev := setting.Migrations.AllowedDomains
	setting.Migrations.AllowedDomains = "127.0.0.1"
	migrations_allowlist.Init()
	defer func() {
		setting.Migrations.AllowedDomains = prev
		migrations_allowlist.Init()
	}()

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		srcRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

		mirrorRepo, err := repo_service.CreateRepositoryDirectly(db.DefaultContext, user, user, repo_service.CreateRepoOptions{
			Name: "test-push-mirror-address-check",
		})
		require.NoError(t, err)

		ctx := NewAPITestContext(t, user.LowerName, srcRepo.Name, auth_model.AccessTokenScopeReadRepository)

		// Create the push mirror while localhost is still an allowed migration domain.
		doCreatePushMirror(ctx, fmt.Sprintf("%s%s/%s", u.String(), url.PathEscape(ctx.Username), url.PathEscape(mirrorRepo.Name)), user.LowerName, userPassword)(t)

		mirrors, _, err := repo_model.GetPushMirrorsByRepoID(db.DefaultContext, srcRepo.ID, db.ListOptions{})
		require.NoError(t, err)
		require.Len(t, mirrors, 1)

		// The push should succeed while localhost is permitted.
		ok := mirror_service.SyncPushMirror(t.Context(), mirrors[0].ID)
		assert.True(t, ok, "expected push mirror sync to succeed while the remote URL is permitted")

		// Reset the allowed domains to the default, which does not permit localhost.
		setting.Migrations.AllowedDomains = prev
		migrations_allowlist.Init()

		// Re-triggering the push mirror should now fail because the remote URL is no longer permitted.
		ok = mirror_service.SyncPushMirror(t.Context(), mirrors[0].ID)
		assert.False(t, ok, "expected push mirror sync to fail because the remote URL is no longer permitted")
	})
}
