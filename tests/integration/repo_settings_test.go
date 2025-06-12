// Copyright 2024 The Forgejo Authors c/o Codeberg e.V.. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"forgejo.org/models/db"
	"forgejo.org/models/forgefed"
	git_model "forgejo.org/models/git"
	repo_model "forgejo.org/models/repo"
	unit_model "forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	fm "forgejo.org/modules/forgefed"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/modules/validation"
	gitea_context "forgejo.org/services/context"
	repo_service "forgejo.org/services/repository"
	user_service "forgejo.org/services/user"
	"forgejo.org/tests"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepoSettingsUnits(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user2"})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerID: user.ID, Name: "repo1"})
	session := loginUser(t, user.Name)

	req := NewRequest(t, "GET", fmt.Sprintf("%s/settings/units", repo.Link()))
	session.MakeRequest(t, req, http.StatusOK)
}

func TestRepoSettingsAdminOptions(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user2"})
	admin := unittest.AssertExistsAndLoadBean(t, &user_model.User{IsAdmin: true})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerID: user.ID, Name: "repo1"})
	link := repo.Link()

	hasAdminOpts := func(t *testing.T, doer string, admin bool) {
		session := loginUser(t, doer)

		req := NewRequest(t, "GET", fmt.Sprintf("%s/settings", link))
		resp := session.MakeRequest(t, req, http.StatusOK)
		html := NewHTMLParser(t, resp.Body)

		elems := html.doc.Find("button[name=request_reindex_type]")
		if !admin {
			assert.Empty(t, elems.Nodes)
			return
		}

		values := []string{"code", "issues", "stats"}
		if !setting.Indexer.RepoIndexerEnabled {
			values = values[1:]
		}
		elems.Each(func(i int, s *goquery.Selection) {
			attr, exists := s.Attr("value")
			require.True(t, exists)
			assert.Equal(t, values[i], attr)
		})
	}

	t.Run("guest", func(t *testing.T) {
		hasAdminOpts(t, user.Name, false)
	})

	t.Run("admin", func(t *testing.T) {
		hasAdminOpts(t, admin.Name, true)
	})
}

func TestRepoAddMoreUnitsHighlighting(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user2"})
	session := loginUser(t, user.Name)

	// Make sure there are no disabled repos in the settings!
	setting.Repository.DisabledRepoUnits = []string{}
	unit_model.LoadUnitConfig()

	// Create a known-good repo, with some units disabled.
	repo, _, f := tests.CreateDeclarativeRepo(t, user, "", []unit_model.Type{
		unit_model.TypeCode,
		unit_model.TypePullRequests,
		unit_model.TypeProjects,
		unit_model.TypeActions,
		unit_model.TypeIssues,
		unit_model.TypeWiki,
	}, []unit_model.Type{unit_model.TypePackages}, nil)
	defer f()

	setUserHints := func(t *testing.T, hints bool) func() {
		saved := user.EnableRepoUnitHints

		require.NoError(t, user_service.UpdateUser(db.DefaultContext, user, &user_service.UpdateOptions{
			EnableRepoUnitHints: optional.Some(hints),
		}))

		return func() {
			require.NoError(t, user_service.UpdateUser(db.DefaultContext, user, &user_service.UpdateOptions{
				EnableRepoUnitHints: optional.Some(saved),
			}))
		}
	}

	assertHighlight := func(t *testing.T, page, uri string, highlighted bool) {
		t.Helper()

		req := NewRequest(t, "GET", fmt.Sprintf("%s/settings%s", repo.Link(), page))
		resp := session.MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		htmlDoc.AssertElement(t, fmt.Sprintf(".overflow-menu-items a[href='%s'].active", fmt.Sprintf("%s/settings%s", repo.Link(), uri)), highlighted)
	}

	t.Run("hints enabled", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		defer setUserHints(t, true)()

		t.Run("settings", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Visiting the /settings page, "Settings" is highlighted
			assertHighlight(t, "", "", true)
			// ...but "Add more" isn't.
			assertHighlight(t, "", "/units", false)
		})

		t.Run("units", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Visiting the /settings/units page, "Add more" is highlighted
			assertHighlight(t, "/units", "/units", true)
			// ...but "Settings" isn't.
			assertHighlight(t, "/units", "", false)
		})
	})

	t.Run("hints disabled", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		defer setUserHints(t, false)()

		t.Run("settings", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Visiting the /settings page, "Settings" is highlighted
			assertHighlight(t, "", "", true)
			// ...but "Add more" isn't (it doesn't exist).
			assertHighlight(t, "", "/units", false)
		})

		t.Run("units", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Visiting the /settings/units page, "Settings" is highlighted
			assertHighlight(t, "/units", "", true)
			// ...but "Add more" isn't (it doesn't exist)
			assertHighlight(t, "/units", "/units", false)
		})
	})
}

func TestRepoAddMoreUnits(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user2"})
	session := loginUser(t, user.Name)

	// Make sure there are no disabled repos in the settings!
	setting.Repository.DisabledRepoUnits = []string{}
	unit_model.LoadUnitConfig()

	// Create a known-good repo, with all units enabled.
	repo, _, f := tests.CreateDeclarativeRepo(t, user, "", []unit_model.Type{
		unit_model.TypeCode,
		unit_model.TypePullRequests,
		unit_model.TypeProjects,
		unit_model.TypePackages,
		unit_model.TypeActions,
		unit_model.TypeIssues,
		unit_model.TypeWiki,
	}, nil, nil)
	defer f()

	assertAddMore := func(t *testing.T, present bool) {
		t.Helper()

		req := NewRequest(t, "GET", repo.Link())
		resp := session.MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)
		htmlDoc.AssertElement(t, fmt.Sprintf("a[href='%s/settings/units']", repo.Link()), present)
	}

	t.Run("no add more with all units enabled", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		assertAddMore(t, false)
	})

	t.Run("add more if units can be enabled", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		defer func() {
			repo_service.UpdateRepositoryUnits(db.DefaultContext, repo, []repo_model.RepoUnit{{
				RepoID: repo.ID,
				Type:   unit_model.TypePackages,
			}}, nil)
		}()

		// Disable the Packages unit
		err := repo_service.UpdateRepositoryUnits(db.DefaultContext, repo, nil, []unit_model.Type{unit_model.TypePackages})
		require.NoError(t, err)

		assertAddMore(t, true)
	})

	t.Run("no add more if unit is globally disabled", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		defer func() {
			repo_service.UpdateRepositoryUnits(db.DefaultContext, repo, []repo_model.RepoUnit{{
				RepoID: repo.ID,
				Type:   unit_model.TypePackages,
			}}, nil)
			setting.Repository.DisabledRepoUnits = []string{}
			unit_model.LoadUnitConfig()
		}()

		// Disable the Packages unit globally
		setting.Repository.DisabledRepoUnits = []string{"repo.packages"}
		unit_model.LoadUnitConfig()

		// Disable the Packages unit
		err := repo_service.UpdateRepositoryUnits(db.DefaultContext, repo, nil, []unit_model.Type{unit_model.TypePackages})
		require.NoError(t, err)

		// The "Add more" link appears no more
		assertAddMore(t, false)
	})

	t.Run("issues & ext tracker globally disabled", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		defer func() {
			repo_service.UpdateRepositoryUnits(db.DefaultContext, repo, []repo_model.RepoUnit{{
				RepoID: repo.ID,
				Type:   unit_model.TypeIssues,
			}}, nil)
			setting.Repository.DisabledRepoUnits = []string{}
			unit_model.LoadUnitConfig()
		}()

		// Disable both Issues and ExternalTracker units globally
		setting.Repository.DisabledRepoUnits = []string{"repo.issues", "repo.ext_issues"}
		unit_model.LoadUnitConfig()

		// Disable the Issues unit
		err := repo_service.UpdateRepositoryUnits(db.DefaultContext, repo, nil, []unit_model.Type{unit_model.TypeIssues})
		require.NoError(t, err)

		// The "Add more" link appears no more
		assertAddMore(t, false)
	})
}

func TestProtectedBranch(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1, OwnerID: user.ID})
	session := loginUser(t, user.Name)

	t.Run("Add", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		link := fmt.Sprintf("/%s/settings/branches/edit", repo.FullName())

		req := NewRequestWithValues(t, "POST", link, map[string]string{
			"_csrf":       GetCSRF(t, session, link),
			"rule_name":   "master",
			"enable_push": "true",
		})
		session.MakeRequest(t, req, http.StatusSeeOther)

		// Verify it was added.
		unittest.AssertExistsIf(t, true, &git_model.ProtectedBranch{RuleName: "master", RepoID: repo.ID})
	})

	t.Run("Add duplicate", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		link := fmt.Sprintf("/%s/settings/branches/edit", repo.FullName())

		req := NewRequestWithValues(t, "POST", link, map[string]string{
			"_csrf":           GetCSRF(t, session, link),
			"rule_name":       "master",
			"require_signed_": "true",
		})
		session.MakeRequest(t, req, http.StatusSeeOther)
		flashCookie := session.GetCookie(gitea_context.CookieNameFlash)
		assert.NotNil(t, flashCookie)
		assert.Equal(t, "error%3DThere%2Bis%2Balready%2Ba%2Brule%2Bfor%2Bthis%2Bset%2Bof%2Bbranches", flashCookie.Value)

		// Verify it wasn't added.
		unittest.AssertCount(t, &git_model.ProtectedBranch{RuleName: "master", RepoID: repo.ID}, 1)
	})
}

func TestRepoFollowing(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&setting.Federation.Enabled, true)()

	mock := test.NewFederationServerMock()
	federatedSrv := mock.DistantServer(t)
	defer federatedSrv.Close()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1, OwnerID: user.ID})
	session := loginUser(t, user.Name)

	t.Run("Add a following repo", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		link := fmt.Sprintf("/%s/settings", repo.FullName())

		req := NewRequestWithValues(t, "POST", link, map[string]string{
			"_csrf":           GetCSRF(t, session, link),
			"action":          "federation",
			"following_repos": fmt.Sprintf("%s/api/v1/activitypub/repository-id/1", federatedSrv.URL),
		})
		session.MakeRequest(t, req, http.StatusSeeOther)

		// Verify it was added.
		federationHost := unittest.AssertExistsAndLoadBean(t, &forgefed.FederationHost{HostFqdn: "127.0.0.1"})
		unittest.AssertExistsAndLoadBean(t, &repo_model.FollowingRepo{
			ExternalID:       "1",
			FederationHostID: federationHost.ID,
		})
	})

	t.Run("Star a repo having a following repo", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		repoLink := fmt.Sprintf("/%s", repo.FullName())
		link := fmt.Sprintf("%s/action/star", repoLink)
		req := NewRequestWithValues(t, "POST", link, map[string]string{
			"_csrf": GetCSRF(t, session, repoLink),
		})

		session.MakeRequest(t, req, http.StatusOK)

		// Verify distant server received a like activity
		like := fm.ForgeLike{}
		err := like.UnmarshalJSON([]byte(mock.LastPost))
		if err != nil {
			t.Errorf("Error unmarshalling ForgeLike: %q", err)
		}
		if isValid, err := validation.IsValid(like); !isValid {
			t.Errorf("ForgeLike is not valid: %q", err)
		}
		activityType := like.Type
		object := like.Object.GetLink().String()
		isLikeType := activityType == "Like"
		isCorrectObject := strings.HasSuffix(object, "/api/v1/activitypub/repository-id/1")
		if !isLikeType || !isCorrectObject {
			t.Error("Activity is not a like for this repo")
		}
	})
}
