package context

import (
	"testing"

	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	project_module "forgejo.org/modules/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}

type runOpts struct {
	name        string
	doer        *user_model.User
	contextUser *user_model.User
	repo        *Repository
	ownerType   project_module.APIOwnerType
	isSiteAdmin bool
	expect      bool
}

func TestHasProjectPermission(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	// user1 is site admin
	user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	// user2 is writer in org3 and owner of repo1, not owner of repo3
	// user2 is member of org17 and in team with no project perms
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	// user2 is in owner team of org3
	org3 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3})
	org17 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 17})
	// user11 is not member of any org
	user11 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 11})
	// repo3 is private, owned by org3
	r3 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	repo3 := &Repository{
		Owner:      org3,
		Repository: r3,
	}

	t.Run("write", func(t *testing.T) {
		for _, tt := range []runOpts{
			{"user2 -> user2", user2, user2, nil, project_module.APIOwnerTypeIndividual, false, true},
			{"Site Admin (user1) -> org3", user1, org3, nil, project_module.APIOwnerTypeOrganization, true, true},
			{"Org Writer (user2) -> org3", user2, org3, nil, project_module.APIOwnerTypeOrganization, false, true},
			{"User11 -> org3 (not member)", user11, org3, nil, project_module.APIOwnerTypeOrganization, false, false},
			{"Team Member with low perms (user2) -> org (low permissions)", user2, org17, nil, project_module.APIOwnerTypeOrganization, false, false},
		} {
			t.Run(tt.name, func(t *testing.T) {
				hasPerm, err := hasWriteProjectPermission(t.Context(),
					tt.ownerType, tt.doer, tt.contextUser, tt.repo,
					tt.isSiteAdmin)
				require.NoError(t, err)
				assert.Equal(t, tt.expect, hasPerm)
			})
		}
	})

	t.Run("read", func(t *testing.T) {
		for _, tt := range []runOpts{
			{"user2 -> user2", user2, user2, nil, project_module.APIOwnerTypeIndividual, false, true},
			{"Site Admin (user1) -> org3", user1, org3, nil, project_module.APIOwnerTypeOrganization, true, true},
			{"Org Writer (user2) -> org3", user2, org3, nil, project_module.APIOwnerTypeOrganization, false, true},
			{"user11 -> org3 (not member, public org)", user11, org3, nil, project_module.APIOwnerTypeOrganization, false, true},
			{"Team Member with low perms (user2) -> org (low permissions)", user2, org17, nil, project_module.APIOwnerTypeOrganization, false, false},
			{"user11 -> repo3 (private repo)", user11, user11, repo3, project_module.APIOwnerTypeRepository, false, false},
		} {
			t.Run(tt.name, func(t *testing.T) {
				hasPerm, err := hasReadProjectPermission(t.Context(),
					tt.ownerType, tt.doer, tt.contextUser, tt.repo,
					tt.isSiteAdmin)
				require.NoError(t, err)
				assert.Equal(t, tt.expect, hasPerm)
			})
		}
	})
}
