// Copyright 2025 The Forgejo Authors.
// SPDX-License-Identifier: GPL-3.0-or-later

package forgejo_migrations //nolint:revive

import (
	"testing"

	migration_tests "forgejo.org/models/migrations/test"

	"github.com/stretchr/testify/require"
)

func Test_SetTopicsAsEmptySlice(t *testing.T) {
	type Repository struct {
		ID     int64    `xorm:"pk autoincr"`
		Topics []string `xorm:"TEXT JSON"`
	}

	x, deferable := migration_tests.PrepareTestEnv(t, 0, new(Repository))
	defer deferable()
	if x == nil || t.Failed() {
		return
	}

	require.NoError(t, SetTopicsAsEmptySlice(x))

	var repos []Repository
	require.NoError(t, x.Find(&repos))

	for _, repo := range repos {
		if repo.ID == 2 {
			require.Equal(t, []string{"go", "dev"}, repo.Topics, "Valid topics should remain unchanged")
		} else {
			require.Equal(t, []string{}, repo.Topics, "NULL topics should be set to empty array")
		}
	}
}
