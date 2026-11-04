// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package project

import (
	"errors"
	"fmt"

	"forgejo.org/modules/util"
	"forgejo.org/modules/validation"
)

// ErrMismatchedOwner represents an error that
// occurs when ownerID and project.OwnerID mismatch
type ErrMismatchedOwner struct {
	Message string
}

func (err ErrMismatchedOwner) Error() string {
	return err.Message
}

func (err ErrMismatchedOwner) Unwrap() error {
	return util.ErrInvalidArgument
}

func IsErrMismatchedOwner(err error) bool {
	_, ok := errors.AsType[ErrMismatchedOwner](err)
	return ok
}

// ErrMismatchedID represents an error that
// occurs when projectID, columnID and/or projectIssueID mismatch
type ErrMismatchedID struct {
	Message string
}

func (err ErrMismatchedID) Error() string {
	return err.Message
}

func (err ErrMismatchedID) Unwrap() error {
	return util.ErrInvalidArgument
}

// Model Types

type TemplateType uint8

const (
	// TemplateTypeNone is a project template type that has no predefined columns
	TemplateTypeNone TemplateType = iota
	// TemplateTypeBasicKanban is a project template type that has basic predefined columns typically used in a kanban board
	TemplateTypeBasicKanban
	// TemplateTypeBugTriage is a project template type that has predefined columns suited to hunting down bugs
	TemplateTypeBugTriage
)

func (tt TemplateType) ToAPITemplateType() APITemplateType {
	switch tt {
	case TemplateTypeBasicKanban:
		return APITemplateTypeBasicKanban
	case TemplateTypeBugTriage:
		return APITemplateTypeBugTriage
	default:
		return APITemplateTypeNone
	}
}

func (tt TemplateType) Validate() []string {
	types := []any{
		TemplateTypeBasicKanban,
		TemplateTypeBugTriage,
		TemplateTypeNone,
	}
	return validation.ValidateOneOf(tt, types, "TemplateType")
}

type CardType uint8

const (
	// CardTypeTextOnly is a project column card type that is text only
	CardTypeTextOnly CardType = iota
	// CardTypeImagesAndText is a project column card type that has images and text
	CardTypeImagesAndText
)

func (ct CardType) ToAPICardType() APICardType {
	switch ct {
	case CardTypeTextOnly:
		return APICardTypeTextOnly
	case CardTypeImagesAndText:
		return APICardTypeImagesAndText
	default:
		return APICardTypeTextOnly
	}
}

func (ct CardType) Validate() []string {
	types := []any{
		CardTypeTextOnly,
		CardTypeImagesAndText,
	}
	return validation.ValidateOneOf(ct, types, "CardType")
}

type OwnerType uint8

const (
	// TypeIndividual is a type of project that is owned by an individual
	TypeIndividual OwnerType = iota + 1
	// TypeRepository is a project that is tied to a repository
	TypeRepository
	// TypeOrganization is a project that is tied to an organisation
	TypeOrganization
)

func (pt OwnerType) ToAPIOwnerType() APIOwnerType {
	switch pt {
	case TypeIndividual:
		return APIOwnerTypeIndividual
	case TypeRepository:
		return APIOwnerTypeRepository
	default:
		return APIOwnerTypeOrganization
	}
}

func (pt OwnerType) Validate() []string {
	types := []any{
		TypeIndividual,
		TypeRepository,
		TypeOrganization,
	}
	return validation.ValidateOneOf(pt, types, "OwnerType")
}

// API Types

type APIOwnerType string

const (
	APIOwnerTypeIndividual   APIOwnerType = "individual"
	APIOwnerTypeRepository   APIOwnerType = "repository"
	APIOwnerTypeOrganization APIOwnerType = "organization"
)

func (pt APIOwnerType) ToOwnerType() OwnerType {
	switch pt {
	case APIOwnerTypeIndividual:
		return TypeIndividual
	case APIOwnerTypeRepository:
		return TypeRepository
	default:
		return TypeOrganization
	}
}

func (pt APIOwnerType) Validate() []string {
	types := []any{
		APIOwnerTypeIndividual,
		APIOwnerTypeRepository,
		APIOwnerTypeOrganization,
	}
	return validation.ValidateOneOf(pt, types, "APIOwnerType")
}

func (pt APIOwnerType) String() string {
	return string(pt)
}

type (
	APITemplateType string
	// APITemplateConfig is used to identify the template type of project that is being created
	APITemplateConfig struct {
		TemplateType APITemplateType
		Translation  string
	}
)

const (
	APITemplateTypeNone        APITemplateType = "none"
	APITemplateTypeBasicKanban APITemplateType = "basic_kanban"
	APITemplateTypeBugTriage   APITemplateType = "bug_triage"
)

func (p APITemplateType) ToTemplateType() TemplateType {
	switch p {
	case APITemplateTypeBasicKanban:
		return TemplateTypeBasicKanban
	case APITemplateTypeBugTriage:
		return TemplateTypeBugTriage
	default:
		return TemplateTypeNone
	}
}

func (p APITemplateType) Validate() []string {
	types := []any{
		APITemplateTypeNone,
		APITemplateTypeBasicKanban,
		APITemplateTypeBugTriage,
	}
	return validation.ValidateOneOf(p, types, "APITemplateType")
}

func (p APITemplateType) String() string {
	return string(p)
}

//llu:returnsTrKeyWeak
func GetAPITemplateConfigs() []APITemplateConfig {
	return []APITemplateConfig{
		{APITemplateTypeNone, "repo.projects.type.none"},
		{APITemplateTypeBasicKanban, "repo.projects.type.basic_kanban"},
		{APITemplateTypeBugTriage, "repo.projects.type.bug_triage"},
	}
}

type (
	APICardType string
	// APICardConfig is used to identify the type of column card that is being used
	APICardConfig struct {
		CardType    APICardType
		Translation string
	}
)

const (
	APICardTypeTextOnly      APICardType = "text_only"
	APICardTypeImagesAndText APICardType = "images_and_text"
)

func (p APICardType) ToCardType() CardType {
	switch p {
	case APICardTypeTextOnly:
		return CardTypeTextOnly
	case APICardTypeImagesAndText:
		return CardTypeImagesAndText
	default:
		return CardTypeTextOnly
	}
}

func (p APICardType) Validate() []string {
	types := []any{
		APICardTypeTextOnly,
		APICardTypeImagesAndText,
	}
	return validation.ValidateOneOf(p, types, "APICardType")
}

func (p APICardType) String() string {
	return string(p)
}

// APIGetCardConfig retrieves the types of configurations project column cards could have
//
//llu:returnsTrKeyWeak
func GetAPICardConfig() []APICardConfig {
	return []APICardConfig{
		{APICardTypeTextOnly, "repo.projects.card_type.text_only"},
		{APICardTypeImagesAndText, "repo.projects.card_type.images_and_text"},
	}
}

// APIStatus is the project status.
type APIStatus string

const (
	// APIStatusOpen is the project status of an open project.
	APIStatusOpen APIStatus = "open"
	// APIStatusClosed is the project status of a closed project.
	APIStatusClosed APIStatus = "closed"
)

// Validate checks if project status is valid.
func (p APIStatus) Validate() []string {
	types := []any{
		APIStatusOpen,
		APIStatusClosed,
	}
	return validation.ValidateOneOf(p, types, "APIStatus")
}

func (p APIStatus) String() string {
	return string(p)
}

// IsClosed returns whether project status is closed.
func (p APIStatus) IsClosed() bool {
	return p == APIStatusClosed
}

func ProjectLinkForOrg(org string, projectID int64) string { //nolint
	return fmt.Sprintf("%s/-/projects/%d", org, projectID)
}

func ProjectLinkForRepo(repo string, projectID int64) string { //nolint
	return fmt.Sprintf("%s/projects/%d", repo, projectID)
}
