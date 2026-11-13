// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"fmt"
	"testing"

	project_model "forgejo.org/models/project"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	"forgejo.org/services/contexttest"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
)

func TestNewIssueValidateProject(t *testing.T) {
	unittest.PrepareTestEnv(t)

	user := forgery.CreateUser(t, &forgery.CreateUserOptions{
		IsAdmin: true, // to allow creating organisation
	})

	chooseProject := func(t *testing.T, repo *repo_model.Repository, p *project_model.Project, isFound bool) {
		ctx, _ := contexttest.MockContext(
			t, fmt.Sprintf(
				"%s/issues/new?project=%d",
				repo.Link(),
				p.ID,
			),
		)
		contexttest.LoadUser(t, ctx, user.ID)
		contexttest.LoadRepo(t, ctx, repo.ID)
		contexttest.LoadGitRepo(t, ctx)
		if ctx.Repo.Owner.IsOrganization() {
			contexttest.LoadOrganization(t, ctx, ctx.Repo.Owner.ID)
		}

		NewIssue(ctx)

		if isFound {
			assert.Equal(t, p.ID, ctx.Data["project_id"])
			assert.NotNil(t, ctx.Data["Project"])
		} else {
			assert.Nil(t, ctx.Data["project_id"])
			assert.Nil(t, ctx.Data["Project"])
		}
	}

	userRepo := forgery.CreateRepository(t, user, nil)
	userOrg := forgery.CreateOrganisation(t, user, nil)
	orgRepo := forgery.CreateRepository(t, userOrg.AsUser(), nil)
	t.Run("Project belongs to repository", func(t *testing.T) {
		p := forgery.CreateProject(t, userRepo, nil)
		chooseProject(t, userRepo, p, true)
	})
	t.Run("Project belongs to user", func(t *testing.T) {
		p := forgery.CreateProject(t, user, nil)
		chooseProject(t, userRepo, p, true)
	})
	t.Run("Project belongs to org", func(t *testing.T) {
		p := forgery.CreateProject(t, userOrg, nil)
		chooseProject(t, orgRepo, p, true)
	})
	t.Run("Project neither belongs to repo nor the user", func(t *testing.T) {
		otherUser := forgery.CreateUser(t, nil)
		p := forgery.CreateProject(t, otherUser, nil)
		chooseProject(t, userRepo, p, false)
	})
}
