// Copyright 2021 The Gitea Authors. All rights reserved.
// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package lfs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const (
	// LFS pointer files must be less than 1024 bytes
	//
	// See: https://github.com/git-lfs/git-lfs/blob/f0bffc4fe998fe5cb004dbca9e8951ea662ff66b/docs/spec.md?plain=1#L22-L23
	BlobSizeCutoff = 1024

	// MetaFileIdentifier is the string appearing at the first line of LFS pointer files.
	// https://github.com/git-lfs/git-lfs/blob/master/docs/spec.md
	//
	// FIXME: There can be multiple different identifiers, not just that of the example.
	// Please also fix services/gitdiff/gitdiff.go
	MetaFileIdentifier = "version https://git-lfs.github.com/spec/v1"

	// MetaFileOidPrefix appears in LFS pointer files on a line before the sha256 hash.
	MetaFileOidPrefix = "oid sha256:"
)

var (
	// ErrEmptyPointer occurs when the LFS pointer is empty
	// (which is a valid pointer, but likely not what one expects)
	ErrEmptyPointer = errors.New("LFS Pointer is empty")

	// ErrMissingPrefix occurs if the content lacks the LFS prefix
	ErrMissingPrefix = errors.New("content lacks the LFS prefix")

	// ErrInvalidStructure occurs if the content has an invalid structure
	ErrInvalidStructure = errors.New("content has an invalid structure")

	// ErrInvalidOIDFormat occurs if the oid has an invalid format
	ErrInvalidOIDFormat = errors.New("OID has an invalid format")

	// ErrInvalidPointerTargetSize occurs if the size is negative (e.g. -1)
	ErrInvalidPointerTargetSize = errors.New("Pointer contains a negative size")
)

var oidPattern = regexp.MustCompile(`^[a-f\d]{64}$`)

// IsOIDValid only checks whether the pointer's OID format is correct; this
// is only useful when we want to "distinguish" the reason as to why the pointer
// can be invalid so that we can return [ErrInvalidOIDFormat]s.
//
// This may be the case when given an OID to perform an LFS operation, e.g. a
// lookup or a deletion, instead of parsing a file representing an LFS pointer.
func (p Pointer) IsOIDValid() bool {
	return oidPattern.MatchString(p.Oid)
}

// Validate checks if the pointer has a valid structure.
// It doesn't check if the pointed-to-content exists.
//
// Note that in certain cases, it is completely reasonable to create a Pointer
// object that contains a provided OID as an identifier but not a valid size,
// as the "missing pieces" are to be obtained by the database. This is primarily
// intended for LFS pointer files.
//
// TODO: Refactor and fix the additional checks of ReadPointerFromBuffer here
// or someplace else.
func (p *Pointer) Validate() error {
	// p == nil represents the "empty pointer"
	if p == nil {
		return nil
	}
	if !p.IsOIDValid() {
		return ErrInvalidOIDFormat
	}
	if p.Size < 0 {
		return ErrInvalidPointerTargetSize
	}
	if p.Size == 0 {
		return ErrEmptyPointer
	}
	return nil
}

// Reads a Git LFS pointer from a provided string.
// Usually called by [ReadPointerFromBuffer], but the function in itself is
// primarily useful for diffs.
//
// Will return a pointer if the provided byte slice is a pointer file or an
// error otherwise.
func ReadPointerFromString(pointerString string) (*Pointer, error) {
	var p Pointer
	var err error

	if len(pointerString) == 0 {
		return nil, ErrEmptyPointer
	}

	splitLines := strings.Split(pointerString, "\n")
	// FIXME: There can be multiple different identifiers, not just that of the example.
	if !strings.HasPrefix(splitLines[0], MetaFileIdentifier) {
		return nil, ErrMissingPrefix
	}

	if len(splitLines) < 3 {
		return nil, ErrInvalidStructure
	}

	// FIXME: The second line is not necessarily that of the OID.
	// See: https://github.com/git-lfs/git-lfs/blob/f0bffc4fe998fe5cb004dbca9e8951ea662ff66b/lfs/pointer_test.go#L189-L193
	p.Oid = strings.TrimPrefix(splitLines[1], MetaFileOidPrefix)

	p.Size, err = strconv.ParseInt(strings.TrimPrefix(splitLines[2], "size "), 10, 64)
	if err != nil {
		return nil, err
	}

	if err = p.Validate(); err != nil {
		return nil, err
	}

	return &p, nil
}

// ReadPointerFromBuffer will return a pointer if the provided byte slice is
// a pointer file or an error otherwise.
func ReadPointerFromBuffer(buf []byte) (*Pointer, error) {
	return ReadPointerFromString(string(buf))
}

// ReadPointer tries to read LFS pointer data from the reader
func ReadPointer(reader io.Reader) (*Pointer, error) {
	buf := make([]byte, BlobSizeCutoff)
	n, err := io.ReadFull(reader, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	buf = buf[:n]

	return ReadPointerFromBuffer(buf)
}

// StringContent returns the string representation of the pointer
// https://github.com/git-lfs/git-lfs/blob/main/docs/spec.md#the-pointer
func (p *Pointer) StringContent() string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf("%s\n%s%s\nsize %d\n", MetaFileIdentifier, MetaFileOidPrefix, p.Oid, p.Size)
}

// RelativePath returns the relative storage path of the pointer
func (p Pointer) RelativePath() string {
	if len(p.Oid) < 5 {
		return p.Oid
	}

	return path.Join(p.Oid[0:2], p.Oid[2:4], p.Oid[4:])
}

func (p *Pointer) LogString() string {
	if p == nil {
		return "<LFSPointer empty>"
	}
	return fmt.Sprintf("<LFSPointer %s:%d>", p.Oid, p.Size)
}

// GeneratePointer generates a pointer for arbitrary content
func GeneratePointer(content io.Reader) (*Pointer, error) {
	h := sha256.New()
	c, err := io.Copy(h, content)
	if err != nil {
		return nil, err
	}
	sum := h.Sum(nil)
	if c == 0 {
		return nil, ErrEmptyPointer
	}
	return &Pointer{Oid: hex.EncodeToString(sum), Size: c}, nil
}
