// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package reader

import (
	"bytes"
	"context"
	"io"

	git_model "forgejo.org/models/git"
	"forgejo.org/modules/git"
	"forgejo.org/modules/lfs"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/typesniffer"
)

type FileInfo struct {
	IsTextFile  bool
	IsLFSFile   bool
	FileSize    int64
	LfsMeta     *lfs.Pointer
	SniffedType typesniffer.SniffedType
}

func OpenReaderFromMaybeLFSBlob(ctx context.Context, repoID int64, blob *git.Blob) (io.ReadCloser, *FileInfo, error) {
	dataRc, err := blob.DataAsync()
	if err != nil {
		return nil, nil, err
	}

	buf, err := io.ReadAll(io.LimitReader(dataRc, lfs.BlobSizeCutoff))
	if err != nil {
		dataRc.Close()
		return nil, nil, err
	}
	st := typesniffer.DetectContentType(buf, blob.Name())
	isTextFile := st.IsText()

	// FIXME: what happens when README file is an image?
	if !isTextFile || !setting.LFS.StartServer {
		return MultiReadCloser(bytes.NewReader(buf), dataRc), &FileInfo{isTextFile, false, blob.Size(), nil, st}, nil
	}

	pointer, err := lfs.ReadPointerFromBuffer(buf)
	if err != nil { // fallback to plain file
		return MultiReadCloser(bytes.NewReader(buf), dataRc), &FileInfo{isTextFile, false, blob.Size(), nil, st}, nil
	}

	meta, err := git_model.GetLFSMetaObjectByOid(ctx, repoID, pointer.Oid)
	if err != nil { // fallback to plain file
		log.Warn("Unable to access LFS pointer %s in repo %d: %v", pointer.Oid, repoID, err)
		return MultiReadCloser(bytes.NewReader(buf), dataRc), &FileInfo{isTextFile, false, blob.Size(), nil, st}, nil
	}

	dataRc.Close()

	dataRc, err = lfs.ReadMetaObject(*pointer)
	if err != nil {
		return nil, nil, err
	}

	buf, err = io.ReadAll(io.LimitReader(dataRc, lfs.BlobSizeCutoff))
	if err != nil {
		dataRc.Close()
		return nil, nil, err
	}

	st = typesniffer.DetectContentType(buf, blob.Name())

	return MultiReadCloser(bytes.NewReader(buf), dataRc), &FileInfo{st.IsText(), true, meta.Size, &meta.Pointer, st}, nil
}

type LFSMultiReadCloser struct {
	maybeReadClosers []io.Reader
	multiReader      io.Reader
}

func MultiReadCloser(maybeReadClosers ...io.Reader) LFSMultiReadCloser {
	r := make([]io.Reader, len(maybeReadClosers))
	copy(r, maybeReadClosers)

	return LFSMultiReadCloser{r, io.MultiReader(r...)}
}

func (rc LFSMultiReadCloser) Close() error {
	var err error
	for _, maybeReadCloser := range rc.maybeReadClosers {
		if readCloser, ok := any(maybeReadCloser).(io.Closer); ok {
			internalErr := readCloser.Close()
			if err == nil {
				err = internalErr
			}
		}
	}
	return err
}

func (rc LFSMultiReadCloser) Read(slice []byte) (int, error) {
	return rc.multiReader.Read(slice)
}
