// Copyright 2023 The Gitea Authors. All rights reserved.
// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package assetfs

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mkdir(t *testing.T, elems ...string) {
	require.NoError(t, os.MkdirAll(filepath.Join(elems...), 0o755))
}

func write(t *testing.T, content string, elems ...string) {
	require.NoError(t, os.WriteFile(filepath.Join(elems...), []byte(content), 0o644))
}

func TestLayered(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "assetfs-layers")
	dir1 := filepath.Join(dir, "l1")
	dir2 := filepath.Join(dir, "l2")

	// d1 & f1: only in "l1"; d2 & f2: only in "l2"
	// da & fa: in both "l1" and "l2"
	mkdir(t, dir1, "d1")
	mkdir(t, dir1, "da")
	mkdir(t, dir1, "da/sub1")

	mkdir(t, dir2, "d2")
	mkdir(t, dir2, "da")
	mkdir(t, dir2, "da/sub2")

	write(t, "dummy", dir1, ".DS_Store")
	write(t, "f1", dir1, "f1")
	write(t, "fa-1", dir1, "fa")
	write(t, "d1-f", dir1, "d1/f")
	write(t, "da-f-1", dir1, "da/f")

	write(t, "f2", dir2, "f2")
	write(t, "fa-2", dir2, "fa")
	write(t, "d2-f", dir2, "d2/f")
	write(t, "da-f-2", dir2, "da/f")

	assets := Layered(Local("l1", dir1), Local("l2", dir2))

	f, err := assets.Open("f1")
	require.NoError(t, err)
	bs, err := io.ReadAll(f)
	require.NoError(t, err)
	assert.Equal(t, "f1", string(bs))
	_ = f.Close()

	assertRead := func(expected string, expectedErr error, elems ...string) {
		bs, err := assets.ReadFile(elems...)
		if err != nil {
			require.ErrorIs(t, err, expectedErr)
		} else {
			require.NoError(t, err)
			assert.Equal(t, expected, string(bs))
		}
	}
	assertRead("f1", nil, "f1")
	assertRead("f2", nil, "f2")
	assertRead("fa-1", nil, "fa")

	assertRead("d1-f", nil, "d1/f")
	assertRead("d2-f", nil, "d2/f")
	assertRead("da-f-1", nil, "da/f")

	assertRead("", fs.ErrNotExist, "no-such")

	files, err := assets.ListFiles(".", true)
	require.NoError(t, err)
	assert.Equal(t, []string{"f1", "f2", "fa"}, files)

	files, err = assets.ListFiles(".", false)
	require.NoError(t, err)
	assert.Equal(t, []string{"d1", "d2", "da"}, files)

	files, err = assets.ListFiles(".")
	require.NoError(t, err)
	assert.Equal(t, []string{"d1", "d2", "da", "f1", "f2", "fa"}, files)

	files, err = assets.ListAllFiles(".", true)
	require.NoError(t, err)
	assert.Equal(t, []string{"d1/f", "d2/f", "da/f", "f1", "f2", "fa"}, files)

	files, err = assets.ListAllFiles(".", false)
	require.NoError(t, err)
	assert.Equal(t, []string{"d1", "d2", "da", "da/sub1", "da/sub2"}, files)

	files, err = assets.ListAllFiles(".")
	require.NoError(t, err)
	assert.Equal(t, []string{
		"d1", "d1/f",
		"d2", "d2/f",
		"da", "da/f", "da/sub1", "da/sub2",
		"f1", "f2", "fa",
	}, files)

	assert.Empty(t, assets.GetFileLayerName("no-such"))
	assert.Equal(t, "l1", assets.GetFileLayerName("f1"))
	assert.Equal(t, "l2", assets.GetFileLayerName("f2"))
}

// Allow layers to read symlink outside the layer root.
func TestLayeredSymlink(t *testing.T) {
	dir := t.TempDir()
	dirl1 := filepath.Join(dir, "l1")
	require.NoError(t, os.MkdirAll(dirl1, 0o755))

	// Open layer in dir/l1
	layer := Local("l1", dirl1)

	// Create a file in dir/outside
	fileContents := []byte("I am outside the layer")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "outside"), fileContents, 0o600))
	// Symlink dir/l1/outside to dir/outside
	require.NoError(t, os.Symlink(filepath.Join(dir, "outside"), filepath.Join(dirl1, "outside")))

	// Open dir/l1/outside.
	f, err := layer.Open("outside")
	require.NoError(t, err)
	defer f.Close()

	// Confirm it contains the output of dir/outside
	contents, err := io.ReadAll(f)
	require.NoError(t, err)

	assert.Equal(t, fileContents, contents)
}

func TestReadAllLayerFiles(t *testing.T) {
	// Folders with the same parent
	dir := filepath.Join(t.TempDir(), "assetfs-layers")
	dir1 := filepath.Join(dir, "l1")
	dir2 := filepath.Join(dir, "l2")
	dir3 := filepath.Join(dir, "l3")
	// dir4 and dir5 are empty
	dir4 := filepath.Join(dir, "l4")
	dir5 := filepath.Join(dir, "l5")

	mkdir(t, dir1)
	mkdir(t, dir2)
	mkdir(t, dir3)
	mkdir(t, dir4)
	mkdir(t, dir5)

	// Folders hold files of same name
	write(t, "A", dir1, "locale_test.json")
	write(t, "B", dir2, "locale_test.json")
	write(t, "C", dir3, "locale_test.json")

	layers := Layered(
		Local("l1", dir1),
		Local("l2", dir2),
		Local("l3", dir3),
		Local("l4", dir4),
		Local("l5", dir5),
	)

	files, err := layers.ReadAllLayerFiles("locale_test.json")
	require.NoError(t, err)

	assert.Len(t, files, 3)
	assert.Equal(t, "A", string(files[0]))
	assert.Equal(t, "B", string(files[1]))
	assert.Equal(t, "C", string(files[2]))

	empty := Layered(
		Local("l4", dir4),
		Local("l5", dir5),
	)
	_, err = empty.ReadAllLayerFiles("locale_test.json")
	assert.ErrorIs(t, err, fs.ErrNotExist)
}
