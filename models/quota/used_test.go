// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package quota

import (
	"testing"

	"forgejo.org/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetUsedForUser(t *testing.T) {
	defer unittest.OverrideFixtures("models/fixtures/TestGetUsedForUser/")()
	require.NoError(t, unittest.PrepareTestDatabase())

	used, err := GetUsedForUser(t.Context(), 5)
	require.NoError(t, err)

	assert.EqualValues(t, 4096, used.Size.Assets.Artifacts)
}
