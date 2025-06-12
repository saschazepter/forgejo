// Copyright 2023, 2024, 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package forgefed

import (
	"fmt"
	"strings"

	"forgejo.org/modules/validation"

	ap "github.com/go-ap/activitypub"
)

// ----------------------------- PersonID --------------------------------------------
type PersonID struct {
	ActorID
}

const (
	personIDapiPathV1       = "api/v1/activitypub/user-id"
	personIDapiPathV1Latest = "api/activitypub/user-id"
)

// Factory function for PersonID. Created struct is asserted to be valid
func NewPersonID(uri, source string) (PersonID, error) {
	result, err := newActorID(uri)
	if err != nil {
		return PersonID{}, err
	}
	result.Source = source

	// validate Person specific path
	personID := PersonID{result}
	if valid, err := validation.IsValid(personID); !valid {
		return PersonID{}, err
	}

	return personID, nil
}

func NewPersonIDFromModel(host, schema string, port uint16, softwareName, id string) (PersonID, error) {
	result := PersonID{}
	result.ID = id
	result.Source = softwareName
	result.Host = host
	result.HostSchema = schema
	result.HostPort = port
	result.IsPortSupplemented = false

	if softwareName == "forgejo" {
		result.Path = personIDapiPathV1
	}
	result.UnvalidatedInput = result.AsURI()

	// validate Person specific path
	if valid, err := validation.IsValid(result); !valid {
		return PersonID{}, err
	}

	return result, nil
}

func (id PersonID) AsWebfinger() string {
	result := fmt.Sprintf("@%s@%s", strings.ToLower(id.ID), strings.ToLower(id.Host))
	return result
}

func (id PersonID) AsLoginName() string {
	result := fmt.Sprintf("%s%s", strings.ToLower(id.ID), id.HostSuffix())
	return result
}

func (id PersonID) HostSuffix() string {
	var result string
	if !id.IsPortSupplemented {
		result = fmt.Sprintf("-%s-%d", strings.ToLower(id.Host), id.HostPort)
	} else {
		result = fmt.Sprintf("-%s", strings.ToLower(id.Host))
	}
	return result
}

func (id PersonID) Validate() []string {
	result := id.ActorID.Validate()
	result = append(result, validation.ValidateNotEmpty(id.Source, "source")...)
	result = append(result, validation.ValidateOneOf(id.Source, []any{"forgejo", "gitea", "mastodon", "gotosocial"}, "Source")...)
	if id.Source == "forgejo" {
		result = append(result, validation.ValidateNotEmpty(id.Path, "path")...)
		if strings.ToLower(id.Path) != personIDapiPathV1 && strings.ToLower(id.Path) != personIDapiPathV1Latest {
			result = append(result, fmt.Sprintf("path: %q has to be a person specific api path", id.Path))
		}
	}

	return result
}

// ----------------------------- ForgePerson -------------------------------------

// ForgePerson activity data type
// swagger:model
type ForgePerson struct {
	// swagger:ignore
	ap.Actor
}

func (s ForgePerson) MarshalJSON() ([]byte, error) {
	return s.Actor.MarshalJSON()
}

func (s *ForgePerson) UnmarshalJSON(data []byte) error {
	return s.Actor.UnmarshalJSON(data)
}

func (s ForgePerson) Validate() []string {
	var result []string
	result = append(result, validation.ValidateNotEmpty(string(s.Type), "Type")...)
	result = append(result, validation.ValidateOneOf(string(s.Type), []any{string(ap.PersonType)}, "Type")...)
	result = append(result, validation.ValidateNotEmpty(s.PreferredUsername.String(), "PreferredUsername")...)

	return result
}
