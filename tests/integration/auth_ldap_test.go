// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"forgejo.org/models"
	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	"forgejo.org/models/organization"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/translation"
	"forgejo.org/services/auth"
	"forgejo.org/services/auth/source/ldap"
	org_service "forgejo.org/services/org"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ldapUser struct {
	UserName     string
	Password     string
	FullName     string
	Email        string
	OtherEmails  []string
	IsAdmin      bool
	IsRestricted bool
	SSHKeys      []string
}

var gitLDAPUsers = []ldapUser{
	{
		UserName:    "professor",
		Password:    "professor",
		FullName:    "Hubert Farnsworth",
		Email:       "professor@planetexpress.com",
		OtherEmails: []string{"hubert@planetexpress.com"},
		IsAdmin:     true,
	},
	{
		UserName: "hermes",
		Password: "hermes",
		FullName: "Conrad Hermes",
		Email:    "hermes@planetexpress.com",
		IsAdmin:  true,
	},
	{
		UserName: "fry",
		Password: "fry",
		FullName: "Philip Fry",
		Email:    "fry@planetexpress.com",
	},
	{
		UserName:     "leela",
		Password:     "leela",
		FullName:     "Leela Turanga",
		Email:        "leela@planetexpress.com",
		IsRestricted: true,
	},
	{
		UserName: "bender",
		Password: "bender",
		FullName: "Bender Rodriguez",
		Email:    "bender@planetexpress.com",
	},
	{
		UserName: "sshuser",
		Password: "sshuser",
		FullName: "SSH User",
		SSHKeys: []string{
			"SHA256:qLY06smKfHoW/92yXySpnxFR10QFrLdRjf/GNPvwcW8",
			"SHA256:QlVTuM5OssDatqidn2ffY+Lc4YA5Fs78U+0KOHI51jQ",
			"SHA256:DXdeUKYOJCSSmClZuwrb60hUq7367j4fA+udNC3FdRI",
		},
		Email: "sshuser@planetexpress.com",
	},
}

var otherLDAPUsers = []ldapUser{
	{
		UserName: "zoidberg",
		Password: "zoidberg",
		FullName: "John Zoidberg",
		Email:    "zoidberg@planetexpress.com",
	},
	{
		UserName: "amy",
		Password: "amy",
		FullName: "Amy Kroker",
		Email:    "amy@planetexpress.com",
	},
}

func skipLDAPTests() bool {
	return os.Getenv("TEST_LDAP") != "1"
}

func getLDAPServerHost() string {
	host := os.Getenv("TEST_LDAP_HOST")
	if len(host) == 0 {
		host = "ldap"
	}
	return host
}

func getLDAPServerPort() string {
	port := os.Getenv("TEST_LDAP_PORT")
	if len(port) == 0 {
		port = "10389"
	}
	return port
}

func buildAuthSourceLDAPPayload(sshKeyAttribute, mailKeyAttribute, defaultDomainName, groupFilter, groupTeamMap, groupTeamMapRemoval string) map[string]string {
	// Modify user filter to test group filter explicitly
	userFilter := "(&(objectClass=inetOrgPerson)(memberOf=cn=git,ou=people,dc=planetexpress,dc=com)(uid=%s))"
	if groupFilter != "" {
		userFilter = "(&(objectClass=inetOrgPerson)(uid=%s))"
	}

	if len(mailKeyAttribute) == 0 {
		mailKeyAttribute = "mail"
	}

	return map[string]string{
		"type":                     "2",
		"name":                     "ldap",
		"host":                     getLDAPServerHost(),
		"port":                     getLDAPServerPort(),
		"bind_dn":                  "uid=gitea,ou=service,dc=planetexpress,dc=com",
		"bind_password":            "password",
		"user_base":                "ou=people,dc=planetexpress,dc=com",
		"filter":                   userFilter,
		"admin_filter":             "(memberOf=cn=admin_staff,ou=people,dc=planetexpress,dc=com)",
		"restricted_filter":        "(uid=leela)",
		"attribute_username":       "uid",
		"attribute_name":           "givenName",
		"attribute_surname":        "sn",
		"attribute_mail":           mailKeyAttribute,
		"attribute_ssh_public_key": sshKeyAttribute,
		"default_domain_name":      defaultDomainName,
		"is_sync_enabled":          "on",
		"is_active":                "on",
		"groups_enabled":           "on",
		"group_dn":                 "ou=people,dc=planetexpress,dc=com",
		"group_member_uid":         "member",
		"group_filter":             groupFilter,
		"group_team_map":           groupTeamMap,
		"group_team_map_removal":   groupTeamMapRemoval,
		"user_uid":                 "DN",
	}
}

func addAuthSourceLDAP(t *testing.T, sshKeyAttribute, mailKeyAttribute, defaultDomainName, groupFilter string, groupMapParams ...string) {
	groupTeamMapRemoval := "off"
	groupTeamMap := ""
	if len(groupMapParams) == 2 {
		groupTeamMapRemoval = groupMapParams[0]
		groupTeamMap = groupMapParams[1]
	}
	session := loginUser(t, "user1")
	req := NewRequestWithValues(t, "POST", "/admin/auths/new", buildAuthSourceLDAPPayload(sshKeyAttribute, mailKeyAttribute, defaultDomainName, groupFilter, groupTeamMap, groupTeamMapRemoval))
	session.MakeRequest(t, req, http.StatusSeeOther)
}

func TestLDAPUserSignin(t *testing.T) {
	if skipLDAPTests() {
		t.Skip()
		return
	}
	defer tests.PrepareTestEnv(t)()
	addAuthSourceLDAP(t, "", "", "", "")

	u := gitLDAPUsers[0]

	session := loginUserWithPassword(t, u.UserName, u.Password)
	req := NewRequest(t, "GET", "/user/settings")
	resp := session.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)

	assert.Equal(t, u.UserName, htmlDoc.GetInputValueByName("name"))
	assert.Equal(t, u.FullName, htmlDoc.GetInputValueByName("full_name"))
}

func TestLDAPAuthChange(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	addAuthSourceLDAP(t, "", "", "", "")

	session := loginUser(t, "user1")
	req := NewRequest(t, "GET", "/admin/auths")
	resp := session.MakeRequest(t, req, http.StatusOK)
	doc := NewHTMLParser(t, resp.Body)
	href, exists := doc.Find("table.table td a").Attr("href")
	if !exists {
		assert.True(t, exists, "No authentication source found")
		return
	}

	req = NewRequest(t, "GET", href)
	resp = session.MakeRequest(t, req, http.StatusOK)
	doc = NewHTMLParser(t, resp.Body)
	host, _ := doc.Find(`input[name="host"]`).Attr("value")
	assert.Equal(t, host, getLDAPServerHost())
	binddn, _ := doc.Find(`input[name="bind_dn"]`).Attr("value")
	assert.Equal(t, "uid=gitea,ou=service,dc=planetexpress,dc=com", binddn)

	req = NewRequestWithValues(t, "POST", href, buildAuthSourceLDAPPayload("", "", "", "", "", "off"))
	session.MakeRequest(t, req, http.StatusSeeOther)

	req = NewRequest(t, "GET", href)
	resp = session.MakeRequest(t, req, http.StatusOK)
	doc = NewHTMLParser(t, resp.Body)
	host, _ = doc.Find(`input[name="host"]`).Attr("value")
	assert.Equal(t, host, getLDAPServerHost())
	binddn, _ = doc.Find(`input[name="bind_dn"]`).Attr("value")
	assert.Equal(t, "uid=gitea,ou=service,dc=planetexpress,dc=com", binddn)
	domainname, _ := doc.Find(`input[name="default_domain_name"]`).Attr("value")
	assert.Empty(t, domainname)

	req = NewRequestWithValues(t, "POST", href, buildAuthSourceLDAPPayload("", "", "test.org", "", "", "off"))
	session.MakeRequest(t, req, http.StatusSeeOther)

	req = NewRequest(t, "GET", href)
	resp = session.MakeRequest(t, req, http.StatusOK)
	doc = NewHTMLParser(t, resp.Body)
	host, _ = doc.Find(`input[name="host"]`).Attr("value")
	assert.Equal(t, host, getLDAPServerHost())
	binddn, _ = doc.Find(`input[name="bind_dn"]`).Attr("value")
	assert.Equal(t, "uid=gitea,ou=service,dc=planetexpress,dc=com", binddn)
	domainname, _ = doc.Find(`input[name="default_domain_name"]`).Attr("value")
	assert.Equal(t, "test.org", domainname)
}

func TestLDAPUserSync(t *testing.T) {
	if skipLDAPTests() {
		t.Skip()
		return
	}
	defer tests.PrepareTestEnv(t)()
	addAuthSourceLDAP(t, "", "", "", "")
	auth.SyncExternalUsers(t.Context(), true)

	// Check if users exists
	for _, gitLDAPUser := range gitLDAPUsers {
		dbUser, err := user_model.GetUserByName(db.DefaultContext, gitLDAPUser.UserName)
		require.NoError(t, err)
		assert.Equal(t, gitLDAPUser.UserName, dbUser.Name)
		assert.Equal(t, gitLDAPUser.Email, dbUser.Email)
		assert.Equal(t, gitLDAPUser.IsAdmin, dbUser.IsAdmin)
		assert.Equal(t, gitLDAPUser.IsRestricted, dbUser.IsRestricted)
	}

	// Check if no users exist
	for _, otherLDAPUser := range otherLDAPUsers {
		_, err := user_model.GetUserByName(db.DefaultContext, otherLDAPUser.UserName)
		assert.True(t, user_model.IsErrUserNotExist(err))
	}
}

func TestLDAPUserSyncWithEmptyUsernameAttribute(t *testing.T) {
	if skipLDAPTests() {
		t.Skip()
		return
	}
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user1")
	payload := buildAuthSourceLDAPPayload("", "", "", "", "", "")
	payload["attribute_username"] = ""
	req := NewRequestWithValues(t, "POST", "/admin/auths/new", payload)
	session.MakeRequest(t, req, http.StatusSeeOther)

	for _, u := range gitLDAPUsers {
		req := NewRequest(t, "GET", "/admin/users?q="+u.UserName)
		resp := session.MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)

		tr := htmlDoc.doc.Find("table.table tbody tr:not(.no-results-row)")
		assert.Equal(t, 0, tr.Length())
	}

	for _, u := range gitLDAPUsers {
		req := NewRequestWithValues(t, "POST", "/user/login", map[string]string{
			"user_name": u.UserName,
			"password":  u.Password,
		})
		MakeRequest(t, req, http.StatusSeeOther)
	}

	auth.SyncExternalUsers(t.Context(), true)

	authSource := unittest.AssertExistsAndLoadBean(t, &auth_model.Source{
		Name: payload["name"],
	})
	unittest.AssertCount(t, &user_model.User{
		LoginType:   auth_model.LDAP,
		LoginSource: authSource.ID,
	}, len(gitLDAPUsers))

	for _, u := range gitLDAPUsers {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{
			Name: u.UserName,
		})
		assert.True(t, user.IsActive)
	}
}

func TestLDAPUserSyncWithGroupFilter(t *testing.T) {
	if skipLDAPTests() {
		t.Skip()
		return
	}
	defer tests.PrepareTestEnv(t)()
	addAuthSourceLDAP(t, "", "", "", "(cn=git)")

	// Assert a user not a member of the LDAP group "cn=git" cannot login
	// This test may look like TestLDAPUserSigninFailed but it is not.
	// The later test uses user filter containing group membership filter (memberOf)
	// This test is for the case when LDAP user records may not be linked with
	// all groups the user is a member of, the user filter is modified accordingly inside
	// the addAuthSourceLDAP based on the value of the groupFilter
	u := otherLDAPUsers[0]
	testLoginFailed(t, u.UserName, u.Password, translation.NewLocale("en-US").TrString("form.username_password_incorrect"))

	auth.SyncExternalUsers(t.Context(), true)

	// Assert members of LDAP group "cn=git" are added
	for _, gitLDAPUser := range gitLDAPUsers {
		unittest.BeanExists(t, &user_model.User{
			Name: gitLDAPUser.UserName,
		})
	}

	// Assert everyone else is not added
	for _, gitLDAPUser := range otherLDAPUsers {
		unittest.AssertNotExistsBean(t, &user_model.User{
			Name: gitLDAPUser.UserName,
		})
	}

	ldapSource := unittest.AssertExistsAndLoadBean(t, &auth_model.Source{
		Name: "ldap",
	})
	ldapConfig := ldapSource.Cfg.(*ldap.Source)
	ldapConfig.GroupFilter = "(cn=ship_crew)"
	auth_model.UpdateSource(db.DefaultContext, ldapSource)

	auth.SyncExternalUsers(t.Context(), true)

	for _, gitLDAPUser := range gitLDAPUsers {
		if gitLDAPUser.UserName == "fry" || gitLDAPUser.UserName == "leela" || gitLDAPUser.UserName == "bender" {
			// Assert members of the LDAP group "cn-ship_crew" are still active
			user := unittest.AssertExistsAndLoadBean(t, &user_model.User{
				Name: gitLDAPUser.UserName,
			})
			assert.True(t, user.IsActive, "User %s should be active", gitLDAPUser.UserName)
		} else {
			// Assert everyone else is inactive
			user := unittest.AssertExistsAndLoadBean(t, &user_model.User{
				Name: gitLDAPUser.UserName,
			})
			assert.False(t, user.IsActive, "User %s should be inactive", gitLDAPUser.UserName)
		}
	}
}

func TestLDAPUserSigninFailed(t *testing.T) {
	if skipLDAPTests() {
		t.Skip()
		return
	}
	defer tests.PrepareTestEnv(t)()
	addAuthSourceLDAP(t, "", "", "", "")

	u := otherLDAPUsers[0]
	testLoginFailed(t, u.UserName, u.Password, translation.NewLocale("en-US").TrString("form.username_password_incorrect"))
}

func TestLDAPUserSSHKeySync(t *testing.T) {
	if skipLDAPTests() {
		t.Skip()
		return
	}
	defer tests.PrepareTestEnv(t)()
	addAuthSourceLDAP(t, "sshPublicKey", "", "", "")

	auth.SyncExternalUsers(t.Context(), true)

	// Check if users has SSH keys synced
	for _, u := range gitLDAPUsers {
		if len(u.SSHKeys) == 0 {
			continue
		}
		session := loginUserWithPassword(t, u.UserName, u.Password)

		req := NewRequest(t, "GET", "/user/settings/keys")
		resp := session.MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)

		divs := htmlDoc.doc.Find("#keys-ssh .flex-item .flex-item-body:not(:last-child)")

		syncedKeys := make([]string, divs.Length())
		for i := 0; i < divs.Length(); i++ {
			syncedKeys[i] = strings.TrimSpace(divs.Eq(i).Text())
		}

		assert.ElementsMatch(t, u.SSHKeys, syncedKeys, "Unequal number of keys synchronized for user: %s", u.UserName)
	}
}

func TestLDAPGroupTeamSyncAddMember(t *testing.T) {
	if skipLDAPTests() {
		t.Skip()
		return
	}
	defer tests.PrepareTestEnv(t)()
	addAuthSourceLDAP(t, "", "", "", "", "on", `{"cn=ship_crew,ou=people,dc=planetexpress,dc=com":{"org26": ["team11"]},"cn=admin_staff,ou=people,dc=planetexpress,dc=com": {"non-existent": ["non-existent"]}}`)
	org, err := organization.GetOrgByName(db.DefaultContext, "org26")
	require.NoError(t, err)
	team, err := organization.GetTeam(db.DefaultContext, org.ID, "team11")
	require.NoError(t, err)
	loginSource := unittest.AssertExistsAndLoadBean(t, &auth_model.Source{Name: "ldap"})

	auth.SyncExternalUsers(t.Context(), true)
	for _, gitLDAPUser := range gitLDAPUsers {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{
			Name: gitLDAPUser.UserName,
		})
		usersOrgs, err := db.Find[organization.Organization](db.DefaultContext, organization.FindOrgOptions{
			UserID:         user.ID,
			IncludePrivate: true,
		})
		require.NoError(t, err)
		allOrgTeams, err := organization.GetUserOrgTeams(db.DefaultContext, org.ID, user.ID)
		require.NoError(t, err)
		if user.Name == "fry" || user.Name == "leela" || user.Name == "bender" {
			// assert members of LDAP group "cn=ship_crew" are added to mapped teams
			assert.Len(t, usersOrgs, 1, "User [%s] should be member of one organization", user.Name)
			assert.Equal(t, "org26", usersOrgs[0].Name, "Membership should be added to the right organization")
			isMember, err := organization.IsTeamMember(db.DefaultContext, usersOrgs[0].ID, team.ID, user.ID)
			require.NoError(t, err)
			assert.True(t, isMember, "Membership should be added to the right team")
			// check that the login source is marked as reason for the team membership
			unittest.AssertExistsAndLoadBean(t, &organization.TeamUser{TeamID: team.ID, UID: user.ID, Reason: organization.MembershipReasonAddedByAuthProvider, CreatedByLoginSourceID: optional.Some(loginSource.ID)})
			err = models.RemoveTeamMember(db.DefaultContext, team, user.ID)
			require.NoError(t, err)
			err = models.RemoveOrgUser(db.DefaultContext, usersOrgs[0].ID, user.ID)
			require.NoError(t, err)
		} else {
			// assert members of LDAP group "cn=admin_staff" keep initial team membership since mapped team does not exist
			assert.Empty(t, usersOrgs, "User should be member of no organization")
			isMember, err := organization.IsTeamMember(db.DefaultContext, org.ID, team.ID, user.ID)
			require.NoError(t, err)
			assert.False(t, isMember, "User should no be added to this team")
			assert.Empty(t, allOrgTeams, "User should not be added to any team")
		}
	}
}

func TestLDAPGroupTeamSyncRemoveMember(t *testing.T) {
	if skipLDAPTests() {
		t.Skip()
		return
	}
	defer tests.PrepareTestEnv(t)()
	addAuthSourceLDAP(t, "", "", "", "", "on", `{"cn=dispatch,ou=people,dc=planetexpress,dc=com": {"org26": ["team11"]}}`)
	org, err := organization.GetOrgByName(db.DefaultContext, "org26")
	require.NoError(t, err)
	team, err := organization.GetTeam(db.DefaultContext, org.ID, "team11")
	require.NoError(t, err)
	loginUserWithPassword(t, gitLDAPUsers[0].UserName, gitLDAPUsers[0].Password)
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{
		Name: gitLDAPUsers[0].UserName,
	})
	err = organization.AddOrgUser(db.DefaultContext, org.ID, user.ID)
	require.NoError(t, err)
	inviterUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	err = org_service.AddTeamMemberByCooptation(db.DefaultContext, team, user, inviterUser)
	require.NoError(t, err)
	isMember, err := organization.IsOrganizationMember(db.DefaultContext, org.ID, user.ID)
	require.NoError(t, err)
	assert.True(t, isMember, "User should be member of this organization")
	isMember, err = organization.IsTeamMember(db.DefaultContext, org.ID, team.ID, user.ID)
	require.NoError(t, err)
	assert.True(t, isMember, "User should be member of this team")
	// assert team member "professor" gets removed from org26 team11
	loginUserWithPassword(t, gitLDAPUsers[0].UserName, gitLDAPUsers[0].Password)
	isMember, err = organization.IsOrganizationMember(db.DefaultContext, org.ID, user.ID)
	require.NoError(t, err)
	assert.False(t, isMember, "User membership should have been removed from organization")
	isMember, err = organization.IsTeamMember(db.DefaultContext, org.ID, team.ID, user.ID)
	require.NoError(t, err)
	assert.False(t, isMember, "User membership should have been removed from team")
}

func TestLDAPPreventInvalidGroupTeamMap(t *testing.T) {
	if skipLDAPTests() {
		t.Skip()
		return
	}
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user1")
	req := NewRequestWithValues(t, "POST", "/admin/auths/new", buildAuthSourceLDAPPayload("", "", "", "", `{"NOT_A_VALID_JSON"["MISSING_DOUBLE_POINT"]}`, "off"))
	session.MakeRequest(t, req, http.StatusOK) // StatusOK = failed, StatusSeeOther = ok
}

func TestLDAPUserSyncInvalidMail(t *testing.T) {
	if skipLDAPTests() {
		t.Skip()
		return
	}
	defer tests.PrepareTestEnv(t)()
	addAuthSourceLDAP(t, "", "nonexisting", "", "")
	auth.SyncExternalUsers(t.Context(), true)

	// Check if users exists
	for _, gitLDAPUser := range gitLDAPUsers {
		dbUser, err := user_model.GetUserByName(db.DefaultContext, gitLDAPUser.UserName)
		require.NoError(t, err)
		assert.Equal(t, gitLDAPUser.UserName, dbUser.Name)
		assert.Equal(t, gitLDAPUser.UserName+"@localhost.local", dbUser.Email)
		assert.Equal(t, gitLDAPUser.IsAdmin, dbUser.IsAdmin)
		assert.Equal(t, gitLDAPUser.IsRestricted, dbUser.IsRestricted)
	}

	// Check if no users exist
	for _, otherLDAPUser := range otherLDAPUsers {
		_, err := user_model.GetUserByName(db.DefaultContext, otherLDAPUser.UserName)
		assert.True(t, user_model.IsErrUserNotExist(err))
	}
}

func TestLDAPUserSyncInvalidMailDefaultDomain(t *testing.T) {
	if skipLDAPTests() {
		t.Skip()
		return
	}
	defer tests.PrepareTestEnv(t)()
	addAuthSourceLDAP(t, "", "nonexisting", "test.org", "")
	auth.SyncExternalUsers(t.Context(), true)

	// Check if users exists
	for _, gitLDAPUser := range gitLDAPUsers {
		dbUser, err := user_model.GetUserByName(db.DefaultContext, gitLDAPUser.UserName)
		require.NoError(t, err)
		assert.Equal(t, gitLDAPUser.UserName, dbUser.Name)
		assert.Equal(t, gitLDAPUser.UserName+"@test.org", dbUser.Email)
		assert.Equal(t, gitLDAPUser.IsAdmin, dbUser.IsAdmin)
		assert.Equal(t, gitLDAPUser.IsRestricted, dbUser.IsRestricted)
	}

	// Check if no users exist
	for _, otherLDAPUser := range otherLDAPUsers {
		_, err := user_model.GetUserByName(db.DefaultContext, otherLDAPUser.UserName)
		assert.True(t, user_model.IsErrUserNotExist(err))
	}
}
