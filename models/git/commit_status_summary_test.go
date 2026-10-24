package git

import (
	"testing"

	"forgejo.org/models/db"
	"forgejo.org/models/unittest"
	api "forgejo.org/modules/structs"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetLatestCommitStatusForRepoAndSHAs(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	sha0 := "0000000000000000000000000000000000000000"
	shaA := "85478e978a441311de4d07f0b93b9ca1761fb5bf"
	shaB := "d58505ebb556f11608afa5d09b31a0d425ade84c"

	unittest.AssertSuccessfulInsert(t,
		&CommitStatusSummary{
			RepoID: 1,
			SHA:    shaA,
			State:  api.CommitStatusSuccess,
		},
		&CommitStatusSummary{
			RepoID: 2,
			SHA:    shaB,
			State:  api.CommitStatusFailure,
		},
	)

	ctx := db.DefaultContext

	t.Run("nil", func(t *testing.T) {
		statuses, err := GetLatestCommitStatusForRepoAndSHAs(ctx, nil)
		require.NoError(t, err)
		assert.Nil(t, statuses)
	})

	t.Run("multiple RepoSHAs", func(t *testing.T) {
		statuses, err := GetLatestCommitStatusForRepoAndSHAs(ctx, []RepoSHA{
			{RepoID: 1, SHA: shaA},
			{RepoID: 2, SHA: shaB},
		})
		require.NoError(t, err)
		assert.ElementsMatch(t, []*CommitStatus{
			{
				RepoID: 1,
				SHA:    shaA,
				State:  api.CommitStatusSuccess,
			},
			{
				RepoID: 2,
				SHA:    shaB,
				State:  api.CommitStatusFailure,
			},
		}, statuses)
	})

	t.Run("unknown repo", func(t *testing.T) {
		statuses, err := GetLatestCommitStatusForRepoAndSHAs(ctx, []RepoSHA{{RepoID: 3, SHA: shaA}})
		require.NoError(t, err)
		assert.Empty(t, statuses)
	})

	t.Run("unknown SHA", func(t *testing.T) {
		statuses, err := GetLatestCommitStatusForRepoAndSHAs(ctx, []RepoSHA{{RepoID: 1, SHA: sha0}})
		require.NoError(t, err)
		assert.Empty(t, statuses)
	})

	t.Run("db error", func(t *testing.T) {
		defer unittest.SetFaultInjector(0)()

		statuses, err := GetLatestCommitStatusForRepoAndSHAs(ctx, []RepoSHA{{RepoID: 1, SHA: shaA}})
		require.ErrorIs(t, err, unittest.ErrFaultInjected)
		assert.Nil(t, statuses)
	})
}
