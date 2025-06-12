// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package forgefed

import (
	"testing"

	"forgejo.org/modules/validation"

	ap "github.com/go-ap/activitypub"
)

func Test_UserActivityNoteValidation(t *testing.T) {
	sut := ForgeUserActivityNote{}
	sut.Type = "Note"
	sut.Content = ap.NaturalLanguageValues{
		{
			Ref:   ap.NilLangRef,
			Value: ap.Content("Any Content!"),
		},
	}
	sut.URL = ap.IRI("example.org/user-id/57")

	if res, _ := validation.IsValid(sut); !res {
		t.Errorf("sut expected to be valid: %v\n", sut.Validate())
	}
}
