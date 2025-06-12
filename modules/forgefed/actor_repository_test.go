// Copyright 2023, 2024, 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package forgefed

import (
	"testing"

	"forgejo.org/modules/setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRepositoryId(t *testing.T) {
	var sut, expected RepositoryID
	var err error
	setting.AppURL = "http://localhost:3000/"

	expected = RepositoryID{}
	expected.ID = "1"
	expected.Source = "forgejo"
	expected.HostSchema = "http"
	expected.Path = ""
	expected.Host = "localhost"
	expected.HostPort = 3000
	expected.IsPortSupplemented = false
	expected.UnvalidatedInput = "http://localhost:3000/1"

	_, err = NewRepositoryID("https://an.other.host/api/v1/activitypub/user-id/1", "forgejo")
	require.EqualError(t, err, "Validation Error: forgefed.RepositoryID: path: \"api/v1/activitypub/user-id\" has to be a repo specific api path")

	expected = RepositoryID{}
	expected.ID = "1"
	expected.Source = "forgejo"
	expected.HostSchema = "http"
	expected.Path = "api/activitypub/repository-id"
	expected.Host = "localhost"
	expected.HostPort = 3000
	expected.IsPortSupplemented = false
	expected.UnvalidatedInput = "http://localhost:3000/api/activitypub/repository-id/1"
	sut, err = NewRepositoryID("http://localhost:3000/api/activitypub/repository-id/1", "forgejo")
	require.NoError(t, err)
	assert.Equal(t, expected, sut)
}
