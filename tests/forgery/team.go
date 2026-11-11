// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPLv3-or-later

package forgery

import (
	"testing"

	"forgejo.org/models"
	"forgejo.org/models/db"
	org_model "forgejo.org/models/organization"
	"forgejo.org/models/perm"
	unit_model "forgejo.org/models/unit"
	user_model "forgejo.org/models/user"

	"github.com/stretchr/testify/require"
)

type CreateTeamOptions struct {
	Name             string
	CanCreateOrgRepo bool

	Mode  perm.AccessMode
	Units []*org_model.TeamUnit

	Members []*user_model.User
}

func CreateTeam(t *testing.T, org *org_model.Organization, opts *CreateTeamOptions) *org_model.Team {
	t.Helper()

	if opts == nil {
		opts = &CreateTeamOptions{
			Mode: perm.AccessModeRead,
		}
	}

	if opts.Name == "" {
		opts.Name = "team-" + uniqueSafeName(t.Name())
	}

	team := &org_model.Team{
		OrgID:                   org.ID,
		Name:                    opts.Name,
		LowerName:               opts.Name,
		IncludesAllRepositories: true,
		AccessMode:              opts.Mode,
		Units:                   opts.Units,
		CanCreateOrgRepo:        opts.CanCreateOrgRepo,
	}
	require.NoError(t, db.Insert(t.Context(), team))

	units := make([]org_model.TeamUnit, 0, len(unit_model.AllRepoUnitTypes))
	for _, tp := range unit_model.AllRepoUnitTypes {
		up := opts.Mode
		if tp == unit_model.TypeExternalTracker || tp == unit_model.TypeExternalWiki {
			up = perm.AccessModeRead
		}
		units = append(units, org_model.TeamUnit{
			OrgID:      org.ID,
			TeamID:     team.ID,
			Type:       tp,
			AccessMode: up,
		})
	}

	require.NoError(t, db.Insert(t.Context(), &units))

	for _, user := range opts.Members {
		_, err := models.InsertTeamMember(t.Context(), team, user.ID)
		require.NoError(t, err)
	}

	return team
}
