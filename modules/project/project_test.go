// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package project

import (
	"testing"

	val "forgejo.org/modules/validation"

	"github.com/stretchr/testify/assert"
)

func TestIsTemplateTypeValid(t *testing.T) {
	const TemplateTypeUnknown TemplateType = 15

	cases := []struct {
		typ   TemplateType
		valid bool
	}{
		{TemplateTypeNone, true},
		{TemplateTypeBasicKanban, true},
		{TemplateTypeBugTriage, true},
		{TemplateTypeUnknown, false},
	}

	for _, v := range cases {
		valid, _ := val.IsValid(v.typ)
		assert.Equal(t, v.valid, valid)
	}
}

func TestIsCardTypeValid(t *testing.T) {
	const UnknownType CardType = 15

	cases := []struct {
		typ   CardType
		valid bool
	}{
		{CardTypeTextOnly, true},
		{CardTypeImagesAndText, true},
		{UnknownType, false},
	}

	for _, v := range cases {
		valid, _ := val.IsValid(v.typ)
		assert.Equal(t, v.valid, valid)
	}
}

func TestIsOwnerTypeValid(t *testing.T) {
	const UnknownType OwnerType = 15

	cases := []struct {
		typ   OwnerType
		valid bool
	}{
		{TypeIndividual, true},
		{TypeRepository, true},
		{TypeOrganization, true},
		{UnknownType, false},
	}

	for _, v := range cases {
		valid, _ := val.IsValid(v.typ)
		assert.Equal(t, v.valid, valid)
	}
}

func TestIsProjectAPITypeValid(t *testing.T) {
	const UnknownType APIOwnerType = ""

	cases := []struct {
		typ   APIOwnerType
		valid bool
	}{
		{APIOwnerTypeIndividual, true},
		{APIOwnerTypeRepository, true},
		{APIOwnerTypeOrganization, true},
		{UnknownType, false},
	}

	for _, v := range cases {
		valid, _ := val.IsValid(v.typ)
		assert.Equal(t, v.valid, valid)
	}
}

func TestIsProjectTemplateTypeValid(t *testing.T) {
	const UnknownType APITemplateType = ""

	cases := []struct {
		typ   APITemplateType
		valid bool
	}{
		{APITemplateTypeNone, true},
		{APITemplateTypeBasicKanban, true},
		{APITemplateTypeBugTriage, true},
		{UnknownType, false},
	}

	for _, v := range cases {
		valid, _ := val.IsValid(v.typ)
		assert.Equal(t, v.valid, valid)
	}
}

func TestIsProjectCardTypeValid(t *testing.T) {
	const UnknownType APICardType = ""

	cases := []struct {
		typ   APICardType
		valid bool
	}{
		{APICardTypeTextOnly, true},
		{APICardTypeImagesAndText, true},
		{UnknownType, false},
	}

	for _, v := range cases {
		valid, _ := val.IsValid(v.typ)
		assert.Equal(t, v.valid, valid)
	}
}

func TestIsProjectStatusValid(t *testing.T) {
	const UnknownType APIStatus = ""

	cases := []struct {
		typ   APIStatus
		valid bool
	}{
		{APIStatusOpen, true},
		{APIStatusClosed, true},
		{UnknownType, false},
	}

	for _, v := range cases {
		valid, _ := val.IsValid(v.typ)
		assert.Equal(t, v.valid, valid)
	}
}

func TestTemplateTypeToAPITemplateType(t *testing.T) {
	for _, v := range []struct {
		template TemplateType
		expect   APITemplateType
	}{
		{TemplateTypeNone, APITemplateTypeNone},
		{TemplateTypeBasicKanban, APITemplateTypeBasicKanban},
		{TemplateTypeBugTriage, APITemplateTypeBugTriage},
		{TemplateType(255), APITemplateTypeNone},
	} {
		assert.Equal(t, v.expect, v.template.ToAPITemplateType())
	}
}

func TestAPITemplateTypeToTemplateType(t *testing.T) {
	for _, v := range []struct {
		template APITemplateType
		expect   TemplateType
	}{
		{APITemplateTypeNone, TemplateTypeNone},
		{APITemplateTypeBasicKanban, TemplateTypeBasicKanban},
		{APITemplateTypeBugTriage, TemplateTypeBugTriage},
		{APITemplateType("does not exist"), TemplateTypeNone},
	} {
		assert.Equal(t, v.expect, v.template.ToTemplateType())
	}
}

func TestCardTypeToAPICardType(t *testing.T) {
	for _, v := range []struct {
		template CardType
		expect   APICardType
	}{
		{CardTypeTextOnly, APICardTypeTextOnly},
		{CardTypeImagesAndText, APICardTypeImagesAndText},
		{CardType(255), APICardTypeTextOnly},
	} {
		assert.Equal(t, v.expect, v.template.ToAPICardType())
	}
}

func TestAPICardTypeToCardType(t *testing.T) {
	for _, v := range []struct {
		template APICardType
		expect   CardType
	}{
		{APICardTypeTextOnly, CardTypeTextOnly},
		{APICardTypeImagesAndText, CardTypeImagesAndText},
		{APICardType("does not exist"), CardTypeTextOnly},
	} {
		assert.Equal(t, v.expect, v.template.ToCardType())
	}
}

func TestOwnerTypeToAPIOwnerType(t *testing.T) {
	for _, v := range []struct {
		template OwnerType
		expect   APIOwnerType
	}{
		{TypeIndividual, APIOwnerTypeIndividual},
		{TypeRepository, APIOwnerTypeRepository},
		{OwnerType(255), APIOwnerTypeOrganization},
	} {
		assert.Equal(t, v.expect, v.template.ToAPIOwnerType())
	}
}

func TestAPIOwnerTypeToOwnerType(t *testing.T) {
	for _, v := range []struct {
		template APIOwnerType
		expect   OwnerType
	}{
		{APIOwnerTypeIndividual, TypeIndividual},
		{APIOwnerTypeRepository, TypeRepository},
		{APIOwnerType("does not exist"), TypeOrganization},
	} {
		assert.Equal(t, v.expect, v.template.ToOwnerType())
	}
}
