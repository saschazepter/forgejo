// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"forgejo.org/models/db"
	"forgejo.org/models/organization"
	packages_model "forgejo.org/models/packages"
	project_model "forgejo.org/models/project"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"
	project_module "forgejo.org/modules/project"
	"forgejo.org/tests"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type userCountTest struct {
	doer         *user_model.User
	user         *user_model.User
	session      *TestSession
	repoCount    int64
	projectCount int64
	packageCount int64
	memberCount  int64
	teamCount    int64
}

func (countTest *userCountTest) Init(t *testing.T, doerID, userID int64) {
	countTest.doer = unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: doerID})
	countTest.user = unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: userID})
	countTest.session = loginUser(t, countTest.doer.Name)

	var err error

	countTest.repoCount, err = repo_model.CountRepository(db.DefaultContext, &repo_model.SearchRepoOptions{
		Actor:       countTest.doer,
		OwnerID:     countTest.user.ID,
		Private:     true,
		Collaborate: optional.Some(false),
	})
	require.NoError(t, err)

	var ownerType project_module.OwnerType
	if countTest.user.IsOrganization() {
		ownerType = project_module.TypeOrganization
	} else {
		ownerType = project_module.TypeIndividual
	}
	countTest.projectCount, err = db.Count[project_model.Project](db.DefaultContext, &project_model.SearchOptions{
		OwnerID:  countTest.user.ID,
		IsClosed: optional.Some(false),
		Type:     ownerType,
	})
	require.NoError(t, err)
	countTest.packageCount, err = packages_model.CountOwnerPackages(db.DefaultContext, countTest.user.ID)
	require.NoError(t, err)

	if !countTest.user.IsOrganization() {
		return
	}

	org := (*organization.Organization)(countTest.user)

	isMember, err := org.IsOrgMember(db.DefaultContext, countTest.doer.ID)
	require.NoError(t, err)

	countTest.memberCount, err = organization.CountOrgMembers(db.DefaultContext, &organization.FindOrgMembersOpts{
		Doer:         countTest.doer,
		OrgID:        org.ID,
		IsDoerMember: isMember,
	})
	require.NoError(t, err)

	teams, err := org.LoadTeams(db.DefaultContext)
	require.NoError(t, err)

	countTest.teamCount = int64(len(teams))
}

func (countTest *userCountTest) getCount(doc *goquery.Document, name string) (int64, error) {
	selection := doc.Find(fmt.Sprintf("[test-name=\"%s\"]", name))

	if selection.Length() != 1 {
		return 0, fmt.Errorf("%s was not found", name)
	}

	return strconv.ParseInt(selection.Text(), 10, 64)
}

func (countTest *userCountTest) TestPage(t *testing.T, page string, orgLink bool) {
	t.Run(page, func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		var userLink string

		if orgLink {
			userLink = countTest.user.OrganisationLink()
		} else {
			userLink = countTest.user.HomeLink()
		}

		req := NewRequestf(t, "GET", "%s/%s", userLink, page)
		resp := countTest.session.MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		repoCount, err := countTest.getCount(htmlDoc.doc, "repository-count")
		require.NoError(t, err)
		assert.Equal(t, countTest.repoCount, repoCount)

		projectCount, err := countTest.getCount(htmlDoc.doc, "project-count")
		require.NoError(t, err)
		assert.Equal(t, countTest.projectCount, projectCount)

		packageCount, err := countTest.getCount(htmlDoc.doc, "package-count")
		require.NoError(t, err)
		assert.Equal(t, countTest.packageCount, packageCount)

		if !countTest.user.IsOrganization() {
			return
		}

		memberCount, err := countTest.getCount(htmlDoc.doc, "member-count")
		require.NoError(t, err)
		assert.Equal(t, countTest.memberCount, memberCount)

		teamCount, err := countTest.getCount(htmlDoc.doc, "team-count")
		require.NoError(t, err)
		assert.Equal(t, countTest.teamCount, teamCount)
	})
}

func TestFrontendHeaderCountUser(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	countTest := new(userCountTest)
	countTest.Init(t, 2, 2)

	countTest.TestPage(t, "", false)
	countTest.TestPage(t, "?tab=repositories", false)
	countTest.TestPage(t, "-/projects", false)
	countTest.TestPage(t, "-/packages", false)
	countTest.TestPage(t, "?tab=activity", false)
	countTest.TestPage(t, "?tab=stars", false)
}

func TestFrontendHeaderCountOrg(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	countTest := new(userCountTest)
	countTest.Init(t, 15, 17)

	countTest.TestPage(t, "", false)
	countTest.TestPage(t, "-/projects", false)
	countTest.TestPage(t, "-/packages", false)
	countTest.TestPage(t, "members", true)
	countTest.TestPage(t, "teams", true)

	countTest.TestPage(t, "settings", true)
	countTest.TestPage(t, "settings/hooks", true)
	countTest.TestPage(t, "settings/labels", true)
	countTest.TestPage(t, "settings/applications", true)
	countTest.TestPage(t, "settings/packages", true)
	countTest.TestPage(t, "settings/actions/runners", true)
	countTest.TestPage(t, "settings/actions/secrets", true)
	countTest.TestPage(t, "settings/actions/variables", true)
	countTest.TestPage(t, "settings/blocked_users", true)
	countTest.TestPage(t, "settings/delete", true)
}
