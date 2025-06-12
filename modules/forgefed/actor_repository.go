// Copyright 2023, 2024, 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package forgefed

import (
	"fmt"
	"strings"

	"forgejo.org/modules/validation"
)

// ----------------------------- RepositoryID --------------------------------------------

type RepositoryID struct {
	ActorID
}

const (
	repositoryIDapiPathV1       = "api/v1/activitypub/repository-id"
	repositoryIDapiPathV1Latest = "api/activitypub/repository-id"
)

// Factory function for RepositoryID. Created struct is asserted to be valid.
func NewRepositoryID(uri, source string) (RepositoryID, error) {
	result, err := newActorID(uri)
	if err != nil {
		return RepositoryID{}, err
	}
	result.Source = source

	// validate Person specific
	repoID := RepositoryID{result}
	if valid, err := validation.IsValid(repoID); !valid {
		return RepositoryID{}, err
	}

	return repoID, nil
}

func (id RepositoryID) Validate() []string {
	result := id.ActorID.Validate()
	result = append(result, validation.ValidateNotEmpty(id.Source, "source")...)
	result = append(result, validation.ValidateOneOf(id.Source, []any{"forgejo", "gitea"}, "Source")...)
	if id.Source == "forgejo" {
		result = append(result, validation.ValidateNotEmpty(id.Path, "path")...)
		if strings.ToLower(id.Path) != repositoryIDapiPathV1 && strings.ToLower(id.Path) != repositoryIDapiPathV1Latest {
			result = append(result, fmt.Sprintf("path: %q has to be a repo specific api path", id.Path))
		}
	}
	return result
}
