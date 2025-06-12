// Copyright 2023, 2024, 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package forgefed

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"forgejo.org/modules/validation"
)

// ----------------------------- ActorID --------------------------------------------
type ActorID struct {
	ID                 string
	Source             string
	HostSchema         string
	Path               string
	Host               string
	HostPort           uint16
	UnvalidatedInput   string
	IsPortSupplemented bool
}

// Factory function for ActorID. Created struct is asserted to be valid
func NewActorID(uri string) (ActorID, error) {
	result, err := newActorID(uri)
	if err != nil {
		return ActorID{}, err
	}

	if valid, err := validation.IsValid(result); !valid {
		return ActorID{}, err
	}

	return result, nil
}

func (id ActorID) AsURI() string {
	var result, path string

	if id.Path == "" {
		path = id.ID
	} else {
		path = fmt.Sprintf("%s/%s", id.Path, id.ID)
	}

	if id.IsPortSupplemented {
		result = fmt.Sprintf("%s://%s/%s", id.HostSchema, id.Host, path)
	} else {
		result = fmt.Sprintf("%s://%s:%d/%s", id.HostSchema, id.Host, id.HostPort, path)
	}

	return result
}

func (id ActorID) Validate() []string {
	var result []string
	result = append(result, validation.ValidateNotEmpty(id.ID, "ID")...)
	result = append(result, validation.ValidateNotEmpty(id.Host, "host")...)
	result = append(result, validation.ValidateNotEmpty(id.HostPort, "hostPort")...)
	result = append(result, validation.ValidateNotEmpty(id.HostSchema, "hostSchema")...)
	result = append(result, validation.ValidateNotEmpty(id.UnvalidatedInput, "unvalidatedInput")...)

	if id.UnvalidatedInput != id.AsURI() {
		result = append(result, fmt.Sprintf("not all input was parsed, \nUnvalidated Input:%q \nParsed URI: %q", id.UnvalidatedInput, id.AsURI()))
	}

	return result
}

func newActorID(uri string) (ActorID, error) {
	validatedURI, err := url.ParseRequestURI(uri)
	if err != nil {
		return ActorID{}, err
	}
	pathWithActorID := strings.Split(validatedURI.Path, "/")
	if containsEmptyString(pathWithActorID) {
		pathWithActorID = removeEmptyStrings(pathWithActorID)
	}
	length := len(pathWithActorID)
	pathWithoutActorID := strings.Join(pathWithActorID[0:length-1], "/")
	id := strings.ToLower(pathWithActorID[length-1])

	result := ActorID{}
	result.ID = id
	result.HostSchema = strings.ToLower(validatedURI.Scheme)
	result.Host = strings.ToLower(validatedURI.Hostname())
	result.Path = strings.ToLower(pathWithoutActorID)

	if validatedURI.Port() == "" && result.HostSchema == "https" {
		result.IsPortSupplemented = true
		result.HostPort = 443
	} else if validatedURI.Port() == "" && result.HostSchema == "http" {
		result.IsPortSupplemented = true
		result.HostPort = 80
	} else {
		numPort, _ := strconv.ParseUint(validatedURI.Port(), 10, 16)
		result.HostPort = uint16(numPort)
	}

	result.UnvalidatedInput = strings.ToLower(uri)

	return result, nil
}

func containsEmptyString(ar []string) bool {
	for _, elem := range ar {
		if elem == "" {
			return true
		}
	}
	return false
}

func removeEmptyStrings(ls []string) []string {
	var rs []string
	for _, str := range ls {
		if str != "" {
			rs = append(rs, str)
		}
	}
	return rs
}
