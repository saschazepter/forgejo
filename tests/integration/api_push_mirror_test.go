// Copyright The Forgejo Authors
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
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
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	migrations_allowlist "forgejo.org/services/migrations/allowlist"
	mirror_service "forgejo.org/services/mirror"
	repo_service "forgejo.org/services/repository"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIPushMirror(t *testing.T) {
	onApplicationRun(t, testAPIPushMirror)
}

func testAPIPushMirror(t *testing.T, u *url.URL) {
	defer test.MockVariableValue(&setting.Migrations.AllowLocalNetworks, true)()
	defer test.MockVariableValue(&setting.Migrations.AllowUnencrypted, true)()
	defer test.MockVariableValue(&setting.Mirror.Enabled, true)()
	defer test.MockProtect(&mirror_service.AddPushMirrorRemote)()
	defer test.MockProtect(&repo_model.DeletePushMirrors)()

	require.NoError(t, migrations_allowlist.Init())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	srcRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: srcRepo.OwnerID})
	session := loginUser(t, user.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeAll)
	urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/push_mirrors", owner.Name, srcRepo.Name)

	mirrorRepo, err := repo_service.CreateRepositoryDirectly(db.DefaultContext, user, user, repo_service.CreateRepoOptions{
		Name: "test-push-mirror",
	})
	require.NoError(t, err)
	remoteAddress := fmt.Sprintf("%s%s/%s", u.String(), url.PathEscape(user.Name), url.PathEscape(mirrorRepo.Name))

	deletePushMirrors := repo_model.DeletePushMirrors
	deletePushMirrorsError := errors.New("deletePushMirrorsError")
	deletePushMirrorsFail := func(ctx context.Context, opts repo_model.PushMirrorOptions) error {
		return deletePushMirrorsError
	}

	addPushMirrorRemote := mirror_service.AddPushMirrorRemote
	addPushMirrorRemoteError := errors.New("addPushMirrorRemoteError")
	addPushMirrorRemoteFail := func(ctx context.Context, m *repo_model.PushMirror, addr string) error {
		return addPushMirrorRemoteError
	}

	for _, testCase := range []struct {
		name        string
		message     string
		status      int
		mirrorCount int
		setup       func()
	}{
		{
			name:        "success",
			status:      http.StatusOK,
			mirrorCount: 1,
			setup: func() {
				mirror_service.AddPushMirrorRemote = addPushMirrorRemote
				repo_model.DeletePushMirrors = deletePushMirrors
			},
		},
		{
			name:        "fail to add and delete",
			message:     deletePushMirrorsError.Error(),
			status:      http.StatusInternalServerError,
			mirrorCount: 1,
			setup: func() {
				mirror_service.AddPushMirrorRemote = addPushMirrorRemoteFail
				repo_model.DeletePushMirrors = deletePushMirrorsFail
			},
		},
		{
			name:        "fail to add",
			message:     addPushMirrorRemoteError.Error(),
			status:      http.StatusInternalServerError,
			mirrorCount: 0,
			setup: func() {
				mirror_service.AddPushMirrorRemote = addPushMirrorRemoteFail
				repo_model.DeletePushMirrors = deletePushMirrors
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.setup()
			req := NewRequestWithJSON(t, "POST", urlStr, &api.CreatePushMirrorOption{
				RemoteAddress: remoteAddress,
				Interval:      "8h",
			}).AddTokenAuth(token)

			resp := MakeRequest(t, req, testCase.status)
			if testCase.message != "" {
				err := api.APIError{}
				DecodeJSON(t, resp, &err)
				assert.Equal(t, testCase.message, err.Message)
			}

			req = NewRequest(t, "GET", urlStr).AddTokenAuth(token)
			resp = MakeRequest(t, req, http.StatusOK)
			var pushMirrors []*api.PushMirror
			DecodeJSON(t, resp, &pushMirrors)
			if assert.Len(t, pushMirrors, testCase.mirrorCount) && testCase.mirrorCount > 0 {
				pushMirror := pushMirrors[0]
				assert.Equal(t, remoteAddress, pushMirror.RemoteAddress)

				repo_model.DeletePushMirrors = deletePushMirrors
				req = NewRequest(t, "DELETE", fmt.Sprintf("%s/%s", urlStr, pushMirror.RemoteName)).AddTokenAuth(token)
				MakeRequest(t, req, http.StatusNoContent)

				_, _, err := git.NewCommand(t.Context(), "remote", "get-url").AddDynamicArguments(pushMirror.RemoteName).RunStdString(&git.RunOpts{Dir: srcRepo.RepoPath()})
				require.True(t, git.IsErrorExitCode(err, 2), "API deletion must remove the Git remote")
			}
		})
	}
}

func TestAPIPushMirrorBranchFilter(t *testing.T) {
	onApplicationRun(t, testAPIPushMirrorBranchFilter)
}

func testAPIPushMirrorBranchFilter(t *testing.T, u *url.URL) {
	defer test.MockVariableValue(&setting.Migrations.AllowLocalNetworks, true)()
	defer test.MockVariableValue(&setting.Migrations.AllowUnencrypted, true)()
	defer test.MockVariableValue(&setting.Mirror.Enabled, true)()
	defer test.MockProtect(&mirror_service.AddPushMirrorRemote)()
	defer test.MockProtect(&repo_model.DeletePushMirrors)()

	require.NoError(t, migrations_allowlist.Init())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	srcRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: srcRepo.OwnerID})
	session := loginUser(t, user.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeAll)
	urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/push_mirrors", owner.Name, srcRepo.Name)

	mirrorRepo := forgery.CreateRepository(t, user, nil)

	remoteAddress := fmt.Sprintf("%s%s/%s", u.String(), url.PathEscape(user.Name), url.PathEscape(mirrorRepo.Name))

	t.Run("Create push mirror with branch filter", func(t *testing.T) {
		req := NewRequestWithJSON(t, "POST", urlStr, &api.CreatePushMirrorOption{
			RemoteAddress: remoteAddress,
			Interval:      "8h",
			BranchFilter:  "main,develop",
		}).AddTokenAuth(token)

		MakeRequest(t, req, http.StatusOK)

		// Verify the push mirror was created with branch filter
		req = NewRequest(t, "GET", urlStr).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusOK)
		var pushMirrors []*api.PushMirror
		DecodeJSON(t, resp, &pushMirrors)
		require.Len(t, pushMirrors, 1)
		assert.Equal(t, "main,develop", pushMirrors[0].BranchFilter)

		// Cleanup
		req = NewRequest(t, "DELETE", fmt.Sprintf("%s/%s", urlStr, pushMirrors[0].RemoteName)).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusNoContent)
	})

	t.Run("Create push mirror with empty branch filter", func(t *testing.T) {
		req := NewRequestWithJSON(t, "POST", urlStr, &api.CreatePushMirrorOption{
			RemoteAddress: remoteAddress,
			Interval:      "8h",
			BranchFilter:  "",
		}).AddTokenAuth(token)

		MakeRequest(t, req, http.StatusOK)

		// Verify the push mirror was created with empty branch filter
		req = NewRequest(t, "GET", urlStr).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusOK)
		var pushMirrors []*api.PushMirror
		DecodeJSON(t, resp, &pushMirrors)
		require.Len(t, pushMirrors, 1)
		assert.Empty(t, pushMirrors[0].BranchFilter)

		// Cleanup
		req = NewRequest(t, "DELETE", fmt.Sprintf("%s/%s", urlStr, pushMirrors[0].RemoteName)).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusNoContent)
	})

	t.Run("Create push mirror without branch filter parameter", func(t *testing.T) {
		req := NewRequestWithJSON(t, "POST", urlStr, &api.CreatePushMirrorOption{
			RemoteAddress: remoteAddress,
			Interval:      "8h",
			// BranchFilter: ""
		}).AddTokenAuth(token)

		MakeRequest(t, req, http.StatusOK)

		// Verify the push mirror defaults to empty branch filter
		req = NewRequest(t, "GET", urlStr).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusOK)
		var pushMirrors []*api.PushMirror
		DecodeJSON(t, resp, &pushMirrors)
		require.Len(t, pushMirrors, 1)
		assert.Empty(t, pushMirrors[0].BranchFilter)

		// Cleanup
		req = NewRequest(t, "DELETE", fmt.Sprintf("%s/%s", urlStr, pushMirrors[0].RemoteName)).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusNoContent)
	})

	t.Run("Retrieve multiple push mirrors with different branch filters", func(t *testing.T) {
		// Create multiple push mirrors with different branch filters
		testCases := []struct {
			name         string
			branchFilter string
		}{
			{"mirror-1", "main"},
			{"mirror-2", "develop,feature-*"},
			{"mirror-3", ""},
		}

		// Create mirrors
		for _, tc := range testCases {
			mirrorRepo := forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{Name: tc.name})

			remoteAddr := fmt.Sprintf("%s%s/%s", u.String(), url.PathEscape(user.Name), url.PathEscape(mirrorRepo.Name))
			req := NewRequestWithJSON(t, "POST", urlStr, &api.CreatePushMirrorOption{
				RemoteAddress: remoteAddr,
				Interval:      "8h",
				BranchFilter:  tc.branchFilter,
			}).AddTokenAuth(token)

			MakeRequest(t, req, http.StatusOK)
		}

		// Retrieve all mirrors and verify branch filters
		req := NewRequest(t, "GET", urlStr).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusOK)
		var pushMirrors []*api.PushMirror
		DecodeJSON(t, resp, &pushMirrors)
		require.Len(t, pushMirrors, 3)

		// Create a map for easier verification
		filterMap := make(map[string]string)
		var createdMirrors []*api.PushMirror
		for _, mirror := range pushMirrors {
			for _, tc := range testCases {
				if strings.Contains(mirror.RemoteAddress, tc.name) {
					filterMap[tc.name] = mirror.BranchFilter
					createdMirrors = append(createdMirrors, mirror)
					break
				}
			}
		}

		assert.Equal(t, "main", filterMap["mirror-1"])
		assert.Equal(t, "develop,feature-*", filterMap["mirror-2"])
		assert.Empty(t, filterMap["mirror-3"])

		// Cleanup
		for _, mirror := range createdMirrors {
			req = NewRequest(t, "DELETE", fmt.Sprintf("%s/%s", urlStr, mirror.RemoteName)).AddTokenAuth(token)
			MakeRequest(t, req, http.StatusNoContent)
		}
	})
}

func TestAPIPushMirrorSSH(t *testing.T) {
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
		session := loginUser(t, user.Name)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
		pushToRepo := forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{})

		sshURL := fmt.Sprintf("ssh://%s@%s/%s.git", setting.SSH.User, net.JoinHostPort(setting.SSH.ListenHost, strconv.Itoa(setting.SSH.ListenPort)), pushToRepo.FullName())

		t.Run("Mutual exclusive", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/push_mirrors", srcRepo.FullName()), &api.CreatePushMirrorOption{
				RemoteAddress:  sshURL,
				Interval:       "8h",
				UseSSH:         true,
				RemoteUsername: "user",
				RemotePassword: "password",
			}).AddTokenAuth(token)
			resp := MakeRequest(t, req, http.StatusBadRequest)

			var apiError api.APIError
			DecodeJSON(t, resp, &apiError)
			assert.Equal(t, "'use_ssh' is mutually exclusive with 'remote_username' and 'remote_password'", apiError.Message)
		})

		t.Run("SSH not available", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			defer test.MockVariableValue(&git.HasSSHExecutable, false)()

			req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/push_mirrors", srcRepo.FullName()), &api.CreatePushMirrorOption{
				RemoteAddress: sshURL,
				Interval:      "8h",
				UseSSH:        true,
			}).AddTokenAuth(token)
			resp := MakeRequest(t, req, http.StatusBadRequest)

			var apiError api.APIError
			DecodeJSON(t, resp, &apiError)
			assert.Equal(t, "SSH authentication not available.", apiError.Message)
		})

		testMirrorPush := func(t *testing.T, srcRepo *repo_model.Repository, expectedSHA string) {
			t.Helper()

			pushToRepo := forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{})
			sshURL := fmt.Sprintf("ssh://%s@%s/%s.git", setting.SSH.User, net.JoinHostPort(setting.SSH.ListenHost, strconv.Itoa(setting.SSH.ListenPort)), pushToRepo.FullName())

			var pushMirror *repo_model.PushMirror
			t.Run("Adding", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/push_mirrors", srcRepo.FullName()), &api.CreatePushMirrorOption{
					RemoteAddress: sshURL,
					Interval:      "8h",
					UseSSH:        true,
				}).AddTokenAuth(token)
				MakeRequest(t, req, http.StatusOK)

				pushMirror = unittest.AssertExistsAndLoadBean(t, &repo_model.PushMirror{RepoID: srcRepo.ID})
				assert.NotEmpty(t, pushMirror.PrivateKey)
				assert.NotEmpty(t, pushMirror.PublicKey)
			})

			publickey := pushMirror.GetPublicKey()
			t.Run("Publickey", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				req := NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/%s/push_mirrors", srcRepo.FullName())).AddTokenAuth(token)
				resp := MakeRequest(t, req, http.StatusOK)

				var pushMirrors []*api.PushMirror
				DecodeJSON(t, resp, &pushMirrors)
				assert.Len(t, pushMirrors, 1)
				assert.Equal(t, publickey, pushMirrors[0].PublicKey)
			})

			t.Run("Add deploy key", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/keys", pushToRepo.FullName()), &api.CreateKeyOption{
					Title:    "push mirror key",
					Key:      publickey,
					ReadOnly: false,
				}).AddTokenAuth(token)
				MakeRequest(t, req, http.StatusCreated)

				unittest.AssertExistsAndLoadBean(t, &asymkey_model.DeployKey{Name: "push mirror key", RepoID: pushToRepo.ID})
			})

			t.Run("Synchronize", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				req := NewRequest(t, "POST", fmt.Sprintf("/api/v1/repos/%s/push_mirrors-sync", srcRepo.FullName())).AddTokenAuth(token)
				MakeRequest(t, req, http.StatusOK)
			})

			t.Run("Check mirrored content", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				req := NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/%s/commits?limit=1", srcRepo.FullName())).AddTokenAuth(token)
				resp := MakeRequest(t, req, http.StatusOK)

				var commitList []*api.Commit
				DecodeJSON(t, resp, &commitList)

				assert.Len(t, commitList, 1)
				assert.Equal(t, expectedSHA, commitList[0].SHA)

				assert.Eventually(t, func() bool {
					req := NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/%s/commits?limit=1", srcRepo.FullName())).AddTokenAuth(token)
					resp := MakeRequest(t, req, http.StatusOK)

					var commitList []*api.Commit
					DecodeJSON(t, resp, &commitList)

					return len(commitList) != 0 && commitList[0].SHA == expectedSHA
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
			testMirrorPush(t, srcRepo, "1032bbf17fbc0d9c95bb5418dabe8f8c99278700")
		})

		t.Run("LFS", func(t *testing.T) {
			defer test.MockVariableValue(&setting.LFS.StartServer, true)()

			srcRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 54})
			testMirrorPush(t, srcRepo, "e9c32647bab825977942598c0efa415de300304b")
		})
	})
}
