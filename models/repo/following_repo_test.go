// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"testing"

	"forgejo.org/modules/validation"
)

func Test_FollowingRepoValidation(t *testing.T) {
	sut := FollowingRepo{
		RepoID:           12,
		ExternalID:       "12",
		FederationHostID: 1,
		URI:              "http://localhost:3000/api/v1/activitypub/repo-id/1",
	}
	if res, err := validation.IsValid(sut); !res {
		t.Errorf("sut should be valid but was %q", err)
	}

	sut = FollowingRepo{
		ExternalID:       "12",
		FederationHostID: 1,
		URI:              "http://localhost:3000/api/v1/activitypub/repo-id/1",
	}
	if res, _ := validation.IsValid(sut); res {
		t.Error("sut should be invalid")
	}
}
