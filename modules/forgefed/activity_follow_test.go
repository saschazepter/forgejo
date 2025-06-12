// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package forgefed

import (
	"testing"

	"forgejo.org/modules/validation"

	ap "github.com/go-ap/activitypub"
)

func Test_NewForgeFollowValidation(t *testing.T) {
	sut := ForgeFollow{}
	sut.Type = "Follow"
	sut.Actor = ap.IRI("example.org/alice")
	sut.Object = ap.IRI("example.org/bob")

	if err, _ := validation.IsValid(sut); !err {
		t.Errorf("sut is invalid: %v\n", err)
	}

	sut = ForgeFollow{}
	sut.Actor = ap.IRI("example.org/alice")
	sut.Object = ap.IRI("example.org/bob")

	if err, _ := validation.IsValid(sut); err {
		t.Errorf("sut is valid: %v\n", err)
	}
}
