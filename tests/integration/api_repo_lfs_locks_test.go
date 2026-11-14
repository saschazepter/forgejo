// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/lfs"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
)

func TestAPILFSLocksNotStarted(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	defer test.MockVariableValue(&setting.LFS.StartServer, false)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	req := NewRequestf(t, "GET", "/%s/%s.git/info/lfs/locks", user.Name, repo.Name)
	MakeRequest(t, req, http.StatusNotFound)
	req = NewRequestf(t, "POST", "/%s/%s.git/info/lfs/locks", user.Name, repo.Name)
	MakeRequest(t, req, http.StatusNotFound)
	req = NewRequestf(t, "GET", "/%s/%s.git/info/lfs/locks/verify", user.Name, repo.Name)
	MakeRequest(t, req, http.StatusNotFound)
	req = NewRequestf(t, "GET", "/%s/%s.git/info/lfs/locks/10/unlock", user.Name, repo.Name)
	MakeRequest(t, req, http.StatusNotFound)
}

func TestAPILFSLocksNoAccess(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	defer test.MockVariableValue(&setting.LFS.StartServer, true)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	// Tries to create lock, but shall never succeed.
	//
	// noLoginCase: true -> no authentication, false -> it's the nonexistent repo case
	createLockHelper := func(t *testing.T, noLoginCase bool) {
		var repoName string
		if noLoginCase {
			repoName = repo.Name
		} else {
			repoName = "nonexistentrepo"
		}
		req := NewRequestf(t, "POST", "/%s/%s.git/info/lfs/locks", user.Name, repoName)
		if !noLoginCase {
			req.AddBasicAuth(user.Name)
		}
		req.Header.Set("Accept", lfs.AcceptHeader)
		req.Header.Set("Content-Type", lfs.MediaType)
		resp := MakeRequest(t, req, http.StatusForbidden)
		assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
		var lfsLockError api.LFSLockError
		DecodeJSON(t, resp, &lfsLockError)
		assert.Equal(t, "Forbidden: Push access required for lock creation", lfsLockError.Message)
		assert.Equal(t, "https://codeberg.org/forgejo/forgejo/issues", lfsLockError.DocumentationURL)
	}

	// Tries to list locks, but shall never succeed.
	//
	// noLoginCase: true -> no authentication, false -> it's the nonexistent repo case
	listLocksHelper := func(t *testing.T, noLoginCase bool) {
		var repoName string
		if noLoginCase {
			repoName = repo.Name
		} else {
			repoName = "nonexistentrepo"
		}
		req := NewRequestf(t, "GET", "/%s/%s.git/info/lfs/locks", user.Name, repoName)
		if !noLoginCase {
			req.AddBasicAuth(user.Name)
		}
		req.Header.Set("Accept", lfs.AcceptHeader)
		resp := MakeRequest(t, req, http.StatusForbidden)
		assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
		var lfsLockError lfs.ErrorResponse
		DecodeJSON(t, resp, &lfsLockError)
		assert.Equal(t, "Forbidden: Pull access required for listing locks", lfsLockError.Message)
		assert.Equal(t, "https://codeberg.org/forgejo/forgejo/issues", lfsLockError.DocumentationURL)
	}

	// Tries to list locks, but shall never succeed.
	//
	// noLoginCase: true -> no authentication, false -> it's the nonexistent repo case
	removeLockHelper := func(t *testing.T, noLoginCase bool) {
		var repoName string
		if noLoginCase {
			repoName = repo.Name
		} else {
			repoName = "nonexistentrepo"
		}
		req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/%s/%s.git/info/lfs/locks/%s/unlock", user.Name, repoName, "0"), map[string]string{})
		if !noLoginCase {
			req.AddBasicAuth(user.Name)
		}
		req.Header.Set("Accept", lfs.AcceptHeader)
		req.Header.Set("Content-Type", lfs.MediaType)
		resp := MakeRequest(t, req, http.StatusForbidden)
		assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
		var lfsUnlockError lfs.ErrorResponse
		DecodeJSON(t, resp, &lfsUnlockError)
		assert.Equal(t, "Forbidden: Push access required for lock deletion", lfsUnlockError.Message)
		assert.Equal(t, "https://codeberg.org/forgejo/forgejo/issues", lfsUnlockError.DocumentationURL)
	}

	t.Run("CreateLock", func(t *testing.T) {
		t.Run("NoLogin", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			createLockHelper(t, true)
		})

		t.Run("NonexistentRepo", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			createLockHelper(t, false)
		})
	})

	t.Run("ListLocks", func(t *testing.T) {
		t.Run("NoLogin", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			listLocksHelper(t, true)
		})

		t.Run("NonexistentRepo", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			listLocksHelper(t, false)
		})
	})

	t.Run("RemoveLock", func(t *testing.T) {
		t.Run("NoLogin", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			removeLockHelper(t, true)
		})

		t.Run("NonexistentRepo", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			removeLockHelper(t, false)
		})
	})
}

func TestAPILFSLocksLogged(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	defer test.MockVariableValue(&setting.LFS.StartServer, true)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2}) // in org 3
	user4 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4}) // in org 3

	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	repo3 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3}) // own by org 3

	testcases := []struct {
		user       *user_model.User
		repo       *repo_model.Repository
		path       string
		httpResult int
		addTime    []int
	}{
		{user: user2, repo: repo1, path: "foo/bar.zip", httpResult: http.StatusCreated, addTime: []int{0}},
		{user: user2, repo: repo1, path: "path/test", httpResult: http.StatusCreated, addTime: []int{0}},
		{user: user2, repo: repo1, path: "path/test", httpResult: http.StatusConflict},
		{user: user2, repo: repo1, path: "Foo/BaR.zip", httpResult: http.StatusConflict},
		{user: user2, repo: repo1, path: "Foo/Test/../subFOlder/../Relative/../BaR.zip", httpResult: http.StatusConflict},
		{user: user4, repo: repo1, path: "FoO/BaR.zip", httpResult: http.StatusForbidden},
		{user: user4, repo: repo1, path: "path/test-user4", httpResult: http.StatusForbidden},
		{user: user2, repo: repo1, path: "patH/Test-user4", httpResult: http.StatusCreated, addTime: []int{0}},
		{user: user2, repo: repo1, path: "some/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/long/path", httpResult: http.StatusCreated, addTime: []int{0}},

		{user: user2, repo: repo3, path: "test/foo/bar.zip", httpResult: http.StatusCreated, addTime: []int{1, 2}},
		{user: user4, repo: repo3, path: "test/foo/bar.zip", httpResult: http.StatusConflict},
		{user: user4, repo: repo3, path: "test/foo/bar.bin", httpResult: http.StatusCreated, addTime: []int{1, 2}},
	}

	resultsTests := []struct {
		user        *user_model.User
		repo        *repo_model.Repository
		totalCount  int
		oursCount   int
		theirsCount int
		locksOwners []*user_model.User
		locksTimes  []time.Time
	}{
		{user: user2, repo: repo1, totalCount: 4, oursCount: 4, theirsCount: 0, locksOwners: []*user_model.User{user2, user2, user2, user2}, locksTimes: []time.Time{}},
		{user: user2, repo: repo3, totalCount: 2, oursCount: 1, theirsCount: 1, locksOwners: []*user_model.User{user2, user4}, locksTimes: []time.Time{}},
		{user: user4, repo: repo3, totalCount: 2, oursCount: 1, theirsCount: 1, locksOwners: []*user_model.User{user2, user4}, locksTimes: []time.Time{}},
	}

	deleteTests := []struct {
		user   *user_model.User
		repo   *repo_model.Repository
		lockID string
	}{}

	t.Run("CreateLocks", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		for _, test := range testcases {
			req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/%s.git/info/lfs/locks", test.repo.FullName()), map[string]string{"path": test.path})
			req.AddBasicAuth(test.user.Name)
			req.Header.Set("Accept", lfs.AcceptHeader)
			req.Header.Set("Content-Type", lfs.MediaType)
			resp := MakeRequest(t, req, test.httpResult)
			if len(test.addTime) > 0 {
				assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
				var lfsLock api.LFSLockResponse
				DecodeJSON(t, resp, &lfsLock)
				assert.Equal(t, test.user.Name, lfsLock.Lock.Owner.Name)
				assert.Equal(t, lfsLock.Lock.LockedAt.Format(time.RFC3339), lfsLock.Lock.LockedAt.Format(time.RFC3339Nano)) // locked at should be rounded to second
				for _, id := range test.addTime {
					resultsTests[id].locksTimes = append(resultsTests[id].locksTimes, time.Now())
				}
			}
			if test.httpResult == http.StatusForbidden || test.httpResult == http.StatusConflict {
				assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
				var lfsLockError api.LFSLockError
				DecodeJSON(t, resp, &lfsLockError)
				assert.Equal(t, "https://codeberg.org/forgejo/forgejo/issues", lfsLockError.DocumentationURL)
			}
		}
	})

	t.Run("CheckCreation", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		for _, test := range resultsTests {
			req := NewRequestf(t, "GET", "/%s.git/info/lfs/locks", test.repo.FullName())
			req.AddBasicAuth(test.user.Name)
			req.Header.Set("Accept", lfs.AcceptHeader)
			resp := MakeRequest(t, req, http.StatusOK)
			assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
			var lfsLocks api.LFSLockList
			DecodeJSON(t, resp, &lfsLocks)
			assert.Len(t, lfsLocks.Locks, test.totalCount)
			for i, lock := range lfsLocks.Locks {
				assert.Equal(t, test.locksOwners[i].Name, lock.Owner.Name)
				assert.WithinDuration(t, test.locksTimes[i], lock.LockedAt, 10*time.Second)
				assert.Equal(t, lock.LockedAt.Format(time.RFC3339), lock.LockedAt.Format(time.RFC3339Nano)) // locked at should be rounded to second
			}

			req = NewRequestWithJSON(t, "POST", fmt.Sprintf("/%s.git/info/lfs/locks/verify", test.repo.FullName()), map[string]string{})
			req.AddBasicAuth(test.user.Name)
			req.Header.Set("Accept", lfs.AcceptHeader)
			req.Header.Set("Content-Type", lfs.MediaType)
			resp = MakeRequest(t, req, http.StatusOK)
			assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
			var lfsLocksVerify api.LFSLockListVerify
			DecodeJSON(t, resp, &lfsLocksVerify)
			assert.Len(t, lfsLocksVerify.Ours, test.oursCount)
			assert.Len(t, lfsLocksVerify.Theirs, test.theirsCount)
			for _, lock := range lfsLocksVerify.Ours {
				assert.Equal(t, test.user.Name, lock.Owner.Name)
				deleteTests = append(deleteTests, struct {
					user   *user_model.User
					repo   *repo_model.Repository
					lockID string
				}{test.user, test.repo, lock.ID})
			}
			for _, lock := range lfsLocksVerify.Theirs {
				assert.NotEqual(t, test.user.DisplayName(), lock.Owner.Name)
			}
		}
	})

	t.Run("RemoveLocks", func(t *testing.T) {
		t.Run("Normal", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			for _, test := range deleteTests {
				req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/%s.git/info/lfs/locks/%s/unlock", test.repo.FullName(), test.lockID), map[string]string{})
				req.AddBasicAuth(test.user.Name)
				req.Header.Set("Accept", lfs.AcceptHeader)
				req.Header.Set("Content-Type", lfs.MediaType)
				resp := MakeRequest(t, req, http.StatusOK)
				assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
				var lfsLockRep api.LFSLockResponse
				DecodeJSON(t, resp, &lfsLockRep)
				assert.Equal(t, test.lockID, lfsLockRep.Lock.ID)
				assert.Equal(t, test.user.Name, lfsLockRep.Lock.Owner.Name)
			}

			// check that we don't have any lock
			for _, test := range resultsTests {
				req := NewRequestf(t, "GET", "/%s.git/info/lfs/locks", test.repo.FullName())
				req.AddBasicAuth(test.user.Name)
				req.Header.Set("Accept", lfs.AcceptHeader)
				resp := MakeRequest(t, req, http.StatusOK)
				assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
				var lfsLocks api.LFSLockList
				DecodeJSON(t, resp, &lfsLocks)
				assert.Empty(t, lfsLocks.Locks)
			}
		})

		t.Run("TryRemovingAlreadyRemovedLocks", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			for _, test := range deleteTests {
				req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/%s.git/info/lfs/locks/%s/unlock", test.repo.FullName(), test.lockID), map[string]string{})
				req.AddBasicAuth(test.user.Name)
				req.Header.Set("Accept", lfs.AcceptHeader)
				req.Header.Set("Content-Type", lfs.MediaType)
				resp := MakeRequest(t, req, http.StatusInternalServerError)
				assert.Equal(t, lfs.MediaType, resp.Header().Get("Content-Type"))
				var lfsUnlockError lfs.ErrorResponse
				DecodeJSON(t, resp, &lfsUnlockError)
				assert.Equal(t, "Internal Server Error: Could not delete lock", lfsUnlockError.Message)
				assert.Equal(t, "https://codeberg.org/forgejo/forgejo/issues", lfsUnlockError.DocumentationURL)
			}
		})
	})
}
