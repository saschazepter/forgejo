// Copyright 2024, 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package validation

import (
	"testing"

	"forgejo.org/modules/timeutil"

	ap "github.com/go-ap/activitypub"
	"github.com/stretchr/testify/assert"
)

type Sut struct {
	valid bool
}

func (sut Sut) Validate() []string {
	if sut.valid {
		return []string{}
	}
	return []string{"invalid"}
}

func Test_IsValid(t *testing.T) {
	sut := Sut{valid: true}
	if res, _ := IsValid(sut); !res {
		t.Errorf("sut expected to be valid: %v\n", sut.Validate())
	}
	sut = Sut{valid: false}
	res, err := IsValid(sut)
	if res {
		t.Errorf("sut expected to be invalid: %v\n", sut.Validate())
	}
	if err == nil || !IsErrNotValid(err) || err.Error() != "Validation Error: validation.Sut: invalid" {
		t.Errorf("validation error expected, but was %v", err)
	}
}

func Test_ValidateNotEmpty_ForString(t *testing.T) {
	sut := ""
	res := ValidateNotEmpty(sut, "dummyField")
	assert.Len(t, res, 1)

	sut = "not empty"
	res = ValidateNotEmpty(sut, "dummyField")
	assert.Empty(t, res, 0)
}

func Test_ValidateNotEmpty_ForTimestamp(t *testing.T) {
	sut := timeutil.TimeStamp(0)
	res := ValidateNotEmpty(sut, "dummyField")
	assert.Len(t, res, 1)

	sut = timeutil.TimeStampNow()
	res = ValidateNotEmpty(sut, "dummyField")
	assert.Empty(t, res, 0)
}

func Test_ValidateIDExists_ForItem(t *testing.T) {
	sut := ap.Activity{
		Object: nil,
	}
	res := ValidateIDExists(sut.Object, "dummyField")
	assert.Len(t, res, 1)

	sut = ap.Activity{
		Object: ap.IRI(""),
	}
	res = ValidateIDExists(sut.Object, "dummyField")
	assert.Len(t, res, 1)

	sut = ap.Activity{
		Object: ap.IRI("https://dummy.link/id"),
	}
	res = ValidateIDExists(sut.Object, "dummyField")
	assert.Empty(t, res, 0)
}

func Test_ValidateMaxLen(t *testing.T) {
	sut := "0123456789"
	res := ValidateMaxLen(sut, 9, "dummyField")
	assert.Len(t, res, 1)

	sut = "0123456789"
	res = ValidateMaxLen(sut, 11, "dummyField")
	assert.Empty(t, res, 0)
}
