// Copyright 2023, 2024, 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package forgefed

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActorNew(t *testing.T) {
	sut, err := NewActorID("https://an.other.forgejo.host/api/v1/activitypub/user-id/5")
	require.NoError(t, err)
	assert.Equal(t, ActorID{
		ID:                 "5",
		HostSchema:         "https",
		Path:               "api/v1/activitypub/user-id",
		Host:               "an.other.forgejo.host",
		HostPort:           443,
		UnvalidatedInput:   "https://an.other.forgejo.host/api/v1/activitypub/user-id/5",
		IsPortSupplemented: true,
	}, sut)

	sut, err = NewActorID("https://an.other.forgejo.host/api/v1/activitypub/actor")
	require.NoError(t, err)
	assert.Equal(t, ActorID{
		ID:                 "actor",
		HostSchema:         "https",
		Path:               "api/v1/activitypub",
		Host:               "an.other.forgejo.host",
		HostPort:           443,
		UnvalidatedInput:   "https://an.other.forgejo.host/api/v1/activitypub/actor",
		IsPortSupplemented: true,
	}, sut)

	sut, err = NewActorID("https://an.other.gts.host/users/me")
	require.NoError(t, err)
	assert.Equal(t, ActorID{
		ID:                 "me",
		HostSchema:         "https",
		Path:               "users",
		Host:               "an.other.gts.host",
		HostPort:           443,
		UnvalidatedInput:   "https://an.other.gts.host/users/me",
		IsPortSupplemented: true,
	}, sut)
}

func TestActorIdValidation(t *testing.T) {
	sut := ActorID{}
	sut.HostSchema = "https"
	sut.Path = "api/v1/activitypub/user-id"
	sut.Host = "an.other.host"
	sut.HostPort = 443
	sut.IsPortSupplemented = true
	sut.UnvalidatedInput = "https://an.other.host/api/v1/activitypub/user-id/"
	result := sut.Validate()
	assert.Len(t, result, 1)
	assert.Equal(t, "ID should not be empty", result[0])

	sut = ActorID{}
	sut.ID = "1"
	sut.HostSchema = "https"
	sut.Path = "api/v1/activitypub/user-id"
	sut.Host = "an.other.host"
	sut.HostPort = 443
	sut.IsPortSupplemented = true
	sut.UnvalidatedInput = "https://an.other.host/api/v1/activitypub/user-id/1?illegal=action"
	result = sut.Validate()
	assert.Len(t, result, 1)
	assert.Equal(t, "not all input was parsed, \nUnvalidated Input:\"https://an.other.host/api/v1/activitypub/user-id/1?illegal=action\" \nParsed URI: \"https://an.other.host/api/v1/activitypub/user-id/1\"", result[0])
}
