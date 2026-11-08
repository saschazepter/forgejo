// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"bufio"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This unit test relies on the implementation detail of CatFileBatch.
func TestCatFileBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	repo, err := OpenRepository(ctx, "./tests/repos/repo1_bare")
	require.NoError(t, err)
	defer repo.Close()

	var wr io.Writer
	var r *bufio.Reader
	var cancel1 func(error)
	t.Run("Request cat file batch", func(t *testing.T) {
		assert.Nil(t, repo.batch)
		wr, r, cancel1, err = repo.CatFileBatch(ctx)
		require.NoError(t, err)
		assert.NotNil(t, repo.batch)
		assert.Equal(t, repo.batch.Writer, wr)
		require.False(t, repo.batchLock.TryLock()) // must not be able to lock -- should already *be* locked
	})

	t.Run("Request temporary cat file batch", func(t *testing.T) {
		wr, r, cancel, err := repo.CatFileBatch(ctx)
		require.NoError(t, err)
		assert.NotEqual(t, repo.batch.Writer, wr)

		t.Run("Check temporary cat file batch", func(t *testing.T) {
			_, err = wr.Write([]byte("95bb4d39648ee7e325106df01a621c530863a653" + "\n"))
			require.NoError(t, err)

			sha, typ, size, err := ReadBatchLine(r)
			require.NoError(t, err)
			assert.Equal(t, "commit", typ)
			assert.Equal(t, []byte("95bb4d39648ee7e325106df01a621c530863a653"), sha)
			assert.EqualValues(t, 144, size)
		})

		cancel(nil)
		require.False(t, repo.batchLock.TryLock()) // must not be able to lock -- should already *be* locked
	})

	t.Run("Check cached cat file batch", func(t *testing.T) {
		_, err = wr.Write([]byte("95bb4d39648ee7e325106df01a621c530863a653" + "\n"))
		require.NoError(t, err)

		sha, typ, size, err := ReadBatchLine(r)
		require.NoError(t, err)
		assert.Equal(t, "commit", typ)
		assert.Equal(t, []byte("95bb4d39648ee7e325106df01a621c530863a653"), sha)
		assert.EqualValues(t, 144, size)
	})

	t.Run("Cancel cached cat file batch", func(t *testing.T) {
		cancel1(nil)
		require.True(t, repo.batchLock.TryLock()) // must be able to lock as cancel1 should have released it
		repo.batchLock.Unlock()
		assert.NotNil(t, repo.batch)
	})

	t.Run("cancel can be invoked again after invocation", func(t *testing.T) {
		assert.NotPanics(t, func() {
			// already cancelled once in test case above
			cancel1(nil)
		})
	})

	t.Run("cancel with error prevents reuse", func(t *testing.T) {
		wr1, r1, cancel1, err := repo.CatFileBatch(ctx)
		require.NoError(t, err)
		cancel1(errors.New("something went wrong"))

		wr2, r2, cancel2, err := repo.CatFileBatch(ctx)
		require.NoError(t, err)
		assert.NotEqual(t, wr1, wr2)
		assert.NotEqual(t, r1, r2)
		cancel2(nil)
	})

	t.Run("cancel with ErrNotExist can still be reused", func(t *testing.T) {
		wr1, r1, cancel1, err := repo.CatFileBatch(ctx)
		require.NoError(t, err)
		cancel1(ErrNotExist{})

		wr2, r2, cancel2, err := repo.CatFileBatch(ctx)
		require.NoError(t, err)
		assert.Equal(t, wr1, wr2)
		assert.Equal(t, r1, r2)
		cancel2(nil)
	})

	t.Run("Request cached cat file batch", func(t *testing.T) {
		wr, _, cancel3, err := repo.CatFileBatch(ctx)
		require.NoError(t, err)
		assert.NotNil(t, repo.batch)
		assert.Equal(t, repo.batch.Writer, wr)
		require.False(t, repo.batchLock.TryLock()) // must not be able to lock -- should already *be* locked

		// attempting to close repository while `CatFileBatch` is in-progress
		assert.Panics(t, func() {
			repo.Close()
		})
		cancel3(nil)
	})

	t.Run("repo close again after invocation safely", func(t *testing.T) {
		// Create a new repo since we panic'd Close in the last test
		repo, err = OpenRepository(ctx, "./tests/repos/repo1_bare")
		require.NoError(t, err)

		// open subprocess which should be cached by the repo
		_, _, cancel, err := repo.CatFileBatch(ctx)
		require.NoError(t, err)
		assert.NotNil(t, repo.batch)
		cancel(nil)

		// close repo again after invocation
		assert.NotPanics(t, func() {
			repo.Close()
			repo.Close()
		})
	})

	t.Run("Start process after repo Close", func(t *testing.T) {
		repo, err = OpenRepository(ctx, "./tests/repos/repo1_bare")
		require.NoError(t, err)
		repo.Close()

		// open subprocess -- because the repo is closed, an action which is expected to occur only once, we do not
		// expect it to create a reusable subprocess since it presumably will never be cleaned up
		_, _, cancel, err := repo.CatFileBatch(ctx)
		require.NoError(t, err)
		assert.Nil(t, repo.check)
		cancel(nil)
	})
}

// This unit test relies on the implementation detail of CatFileBatchCheck.
func TestCatFileBatchCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	repo, err := OpenRepository(ctx, "./tests/repos/repo1_bare")
	require.NoError(t, err)
	defer repo.Close()

	var wr io.Writer
	var r *bufio.Reader
	var cancel1 func(error)
	t.Run("Request cat file batch check", func(t *testing.T) {
		assert.Nil(t, repo.check)
		wr, r, cancel1, err = repo.CatFileBatchCheck(ctx)
		require.NoError(t, err)
		assert.NotNil(t, repo.check)
		assert.Equal(t, repo.check.Writer, wr)
		require.False(t, repo.checkLock.TryLock()) // must not be able to lock -- should already *be* locked
	})

	t.Run("Request temporary cat file batch check", func(t *testing.T) {
		wr, r, cancel, err := repo.CatFileBatchCheck(ctx)
		require.NoError(t, err)
		assert.NotEqual(t, repo.check.Writer, wr)

		t.Run("Check temporary cat file batch check", func(t *testing.T) {
			_, err = wr.Write([]byte("test" + "\n"))
			require.NoError(t, err)

			sha, typ, size, err := ReadBatchLine(r)
			require.NoError(t, err)
			assert.Equal(t, "tag", typ)
			assert.Equal(t, []byte("3ad28a9149a2864384548f3d17ed7f38014c9e8a"), sha)
			assert.EqualValues(t, 807, size)
		})

		cancel(nil)
		require.False(t, repo.checkLock.TryLock()) // must not be able to lock -- should already *be* locked
	})

	t.Run("Check cached cat file batch check", func(t *testing.T) {
		_, err = wr.Write([]byte("test" + "\n"))
		require.NoError(t, err)

		sha, typ, size, err := ReadBatchLine(r)
		require.NoError(t, err)
		assert.Equal(t, "tag", typ)
		assert.Equal(t, []byte("3ad28a9149a2864384548f3d17ed7f38014c9e8a"), sha)
		assert.EqualValues(t, 807, size)
	})

	t.Run("Cancel cached cat file batch check", func(t *testing.T) {
		cancel1(nil)
		require.True(t, repo.checkLock.TryLock()) // must be able to lock as cancel1 should have released it
		repo.checkLock.Unlock()
		assert.NotNil(t, repo.check)
	})

	t.Run("cancel can be invoked again after invocation", func(t *testing.T) {
		assert.NotPanics(t, func() {
			// already cancelled once in test case above
			cancel1(nil)
		})
	})

	t.Run("cancel with error prevents reuse", func(t *testing.T) {
		wr1, r1, cancel1, err := repo.CatFileBatchCheck(ctx)
		require.NoError(t, err)
		cancel1(errors.New("something went wrong"))

		wr2, r2, cancel2, err := repo.CatFileBatchCheck(ctx)
		require.NoError(t, err)
		assert.NotEqual(t, wr1, wr2)
		assert.NotEqual(t, r1, r2)
		cancel2(nil)
	})

	t.Run("cancel with ErrNotExist can still be reused", func(t *testing.T) {
		wr1, r1, cancel1, err := repo.CatFileBatchCheck(ctx)
		require.NoError(t, err)
		cancel1(ErrNotExist{})

		wr2, r2, cancel2, err := repo.CatFileBatchCheck(ctx)
		require.NoError(t, err)
		assert.Equal(t, wr1, wr2)
		assert.Equal(t, r1, r2)
		cancel2(nil)
	})

	t.Run("Request cached cat file batch check", func(t *testing.T) {
		wr, _, cancel3, err := repo.CatFileBatchCheck(ctx)
		require.NoError(t, err)
		assert.NotNil(t, repo.check)
		assert.Equal(t, repo.check.Writer, wr)
		require.False(t, repo.checkLock.TryLock()) // must not be able to lock -- should already *be* locked

		// attempting to close repository while `CatFileBatchCheck` is in-progress
		assert.Panics(t, func() {
			repo.Close()
		})
		cancel3(nil)
	})

	t.Run("repo close again after invocation safely", func(t *testing.T) {
		// Create a new repo since we panic'd Close in the last test
		repo, err = OpenRepository(ctx, "./tests/repos/repo1_bare")
		require.NoError(t, err)

		// open subprocess which should be cached by the repo
		_, _, cancel, err := repo.CatFileBatchCheck(ctx)
		require.NoError(t, err)
		assert.NotNil(t, repo.check)
		cancel(nil)

		// close repo multiple times
		assert.NotPanics(t, func() {
			repo.Close()
			repo.Close()
		})
	})

	t.Run("Start process after repo Close", func(t *testing.T) {
		repo, err = OpenRepository(ctx, "./tests/repos/repo1_bare")
		require.NoError(t, err)
		repo.Close()

		// open subprocess -- because the repo is closed, an action which is expected to occur only once, we do not
		// expect it to create a reusable subprocess since it presumably will never be cleaned up
		_, _, cancel, err := repo.CatFileBatchCheck(ctx)
		require.NoError(t, err)
		assert.Nil(t, repo.check)
		cancel(nil)
	})
}
