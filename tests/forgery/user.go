// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package forgery

import (
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"testing"

	org_model "forgejo.org/models/organization"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/structs"

	"github.com/stretchr/testify/require"
)

var nameCleaner = regexp.MustCompile(`[^a-zA-Z0-9-]+`) // exclude "_", to prevent multiple consecutive dashes

// uniqueSafeName replaces specials chars with _ and appends a random hex suffix
func uniqueSafeName(testName string) string {
	return strings.Trim(nameCleaner.ReplaceAllLiteralString(testName, "_"), "-_") + "-" + strconv.FormatUint(uint64(rand.Uint32()), 16)
}

type CreateUserOptions struct {
	IsAdmin                      bool
	EmailNotificationsPreference string
}

const userPassword = "password"

func CreateUser(t testing.TB, opts *CreateUserOptions) *user_model.User {
	t.Helper()

	var overwrite *user_model.CreateUserOverwriteOptions
	if opts == nil {
		opts = &CreateUserOptions{}
		overwrite = &user_model.CreateUserOverwriteOptions{}
	} else {
		overwrite = &user_model.CreateUserOverwriteOptions{
			EmailNotificationsPreference: &opts.EmailNotificationsPreference,
		}
	}
	u := &user_model.User{}

	name := "user-" + uniqueSafeName(t.Name())

	u.Name = name
	u.Email = name + "@test.forgejo.org"
	u.Passwd = userPassword
	u.IsAdmin = opts.IsAdmin

	err := user_model.CreateUser(t.Context(), u, overwrite)
	require.NoError(t, err)
	return u
}

type CreateOrganisationOptions struct {
	Visibility structs.VisibleType
}

func CreateOrganisation(t testing.TB, owner *user_model.User, opts *CreateOrganisationOptions) *org_model.Organization {
	t.Helper()

	if opts == nil {
		opts = &CreateOrganisationOptions{}
	}
	if owner == nil {
		owner = CreateUser(t, nil) // if specific options are needed, create the owner manually
	}
	o := &org_model.Organization{
		Visibility: opts.Visibility,
	}

	name := "org-" + uniqueSafeName(t.Name())

	o.Name = name

	err := org_model.CreateOrganization(t.Context(), o, owner)
	require.NoError(t, err)
	return o
}
