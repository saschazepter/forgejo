// Copyright 2015 The Gogs Authors. All rights reserved.
// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"sync"

	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
)

// Repository represents a Git repository.
type Repository struct {
	Path string

	tagCache *ObjectCache

	gpgSettings *GPGSettings

	batchLock      sync.Mutex
	batchLockStack string
	batch          *Batch

	checkLock      sync.Mutex
	checkLockStack string
	check          *Batch

	closeOnce sync.Once

	Ctx             context.Context
	LastCommitCache *LastCommitCache

	objectFormat ObjectFormat
}

// openRepositoryWithDefaultContext opens the repository at the given path with DefaultContext.
func openRepositoryWithDefaultContext(repoPath string) (*Repository, error) {
	return OpenRepository(DefaultContext, repoPath)
}

// OpenRepository opens the repository at the given path with the provided context.
func OpenRepository(ctx context.Context, repoPath string) (*Repository, error) {
	repoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, err
	} else if !isDir(repoPath) {
		return nil, errors.New("no such file or directory")
	}

	return &Repository{
		Path:     repoPath,
		tagCache: newObjectCache(),
		Ctx:      ctx,
	}, nil
}

func (repo *Repository) internalCatFile(lock *sync.Mutex, lockStack *string, batch **Batch, makeBatch func() (*Batch, error)) (io.Writer, *bufio.Reader, func(error), error) {
	if lock.TryLock() {
		// Capturing stack traces is a bit expensive for recording this diagnostic information, so only do it in
		// dev/test.
		if !setting.IsProd {
			stack := make([]byte, 8192)
			n := runtime.Stack(stack, false)
			*lockStack = string(stack[:n])
		}

		if *batch == nil {
			newBatch, err := makeBatch()
			if err != nil {
				lock.Unlock()
				return nil, nil, nil, err
			}
			*batch = newBatch
		}

		// Close func wouldn't be safe to call twice without sync.Once. As it may be used in `defer cancel()` and then
		// stored, or, called in a specific code path, making it safe to call twice makes it easier to use correctly't
		// safe to call twice.
		var closeOnce sync.Once
		return (*batch).Writer, (*batch).Reader, func(err error) {
			closeOnce.Do(func() {
				if IsErrNotExist(err) {
					// Omit ErrNotExist error from closing and freeing the subprocess, as this error regularly occurs
					// from ReadBatchLine and the stream is still safe to continue use.
				} else if err != nil {
					log.Debug("internalCatFile closing subprocess as part of error handling for error: %#v", err)
					// error occurred; the `git cat-file --batch` cmd can't be reused because the pipe states are
					// unknown after the error
					(*batch).Close()
					*batch = nil
				}
				*lockStack = ""
				lock.Unlock()
			})
		}, nil
	}

	log.Debug("Opening temporary cat file batch for: %s", repo.Path)
	tempBatch, err := makeBatch()
	if err != nil {
		return nil, nil, nil, err
	}
	return tempBatch.Writer, tempBatch.Reader, func(err error) { tempBatch.Close() }, nil
}

// CatFileBatch begins a `git cat-file --batch` subprocess and returns pipes to the writer and reader for the process. A
// function is returned which must be invoked when using the writer and reader is complete.
//
// The subprocess will be reused between different calls to CatFileBatch, until the repository is closed.  To manage
// this subprocess safely, usage must:
//
// - ensure that the entire response from a request sent to the writer is read from the reader, leaving no leftover
// content in the pipe that the next usage might encounter
//
// - call the close method with an error if any error occurred while processing, signaling that the pipes to the
// subprocess may be in an unexpected state and cannot be reused anymore
func (repo *Repository) CatFileBatch(ctx context.Context) (io.Writer, *bufio.Reader, func(error), error) {
	return repo.internalCatFile(&repo.batchLock, &repo.batchLockStack, &repo.batch,
		func() (*Batch, error) { return repo.newBatch(ctx) })
}

// WithCatFileBatch performs the same work as CatFileBatch, but with a closure-based API which makes error handling a
// little easier.  It is still necessary to ensure that the entire response from any request sent to the writer is read
// from the reader, leaving no leftover content in the pipe that the next musage might encounter.
//
// Excluding git.ErrNotExist, any error returned from the closure will cause the reusable subprocess to close.  Be
// careful to only return errors that would indicate the subprocess reader/writer state is unclear.
func (repo *Repository) WithCatFileBatch(ctx context.Context, closure func(io.Writer, *bufio.Reader) error) error {
	wr, rd, close, err := repo.CatFileBatch(ctx)
	if err != nil {
		return err
	}
	err = closure(wr, rd)
	close(err)
	return err
}

// CatFileBatch begins a `git cat-file --batch-check` subprocess and returns pipes to the writer and reader for the
// process. A function is returned which must be invoked when using the writer and reader is complete.
//
// The subprocess will be reused between different calls to CatFileBatch, until the repository is closed.  To manage
// this subprocess safely, usage must:
//
// - ensure that the entire response from a request sent to the writer is read from the reader, leaving no leftover
// content in the pipe that the next usage might encounter
//
// - call the close method with an error if any error occurred while processing, signaling that the pipes to the
// subprocess may be in an unexpected state and cannot be reused anymore
func (repo *Repository) CatFileBatchCheck(ctx context.Context) (io.Writer, *bufio.Reader, func(error), error) {
	return repo.internalCatFile(&repo.checkLock, &repo.checkLockStack, &repo.check,
		func() (*Batch, error) { return repo.newBatchCheck(ctx) })
}

// WithCatFileBatch performs the same work as CatFileBatchCheck, but with a closure-based API which makes error handling
// a little easier.  It is still necessary to ensure that the entire response from any request sent to the writer is
// read from the reader, leaving no leftover content in the pipe that the next musage might encounter.
//
// Excluding git.ErrNotExist, any error returned from the closure will cause the reusable subprocess to close.  Be
// careful to only return errors that would indicate the subprocess reader/writer state is unclear.
func (repo *Repository) WithCatFileBatchCheck(ctx context.Context, closure func(io.Writer, *bufio.Reader) error) error {
	wr, rd, close, err := repo.CatFileBatchCheck(ctx)
	if err != nil {
		return err
	}
	err = closure(wr, rd)
	close(err)
	return err
}

func (repo *Repository) Close() error {
	if repo == nil {
		return nil
	}

	// Some code paths will execute `defer repo.Close()` and then `repo.Close()` later, causing double closure.  Holding
	// batchLock/checkLock indefinitely causes double closure to panic, so we prevent that by using a sync.Once.
	repo.closeOnce.Do(func() {
		// Intentionally leave repo.batchLock locked; never unlock it.  This repository is closed, but some codepaths
		// will still call `CatFileBatch` on the closed repository, particularly where references to `*git.Blob`, which
		// hold a pointer to a Repository, outlive the repository being closed.  By leaving the lock held, we'll force
		// new usages on the closed repository to go to the "temporary cat-file" code path, where the subprocess will be
		// closed immediately after one operation.
		if !repo.batchLock.TryLock() {
			msg := "attempting to close repository while `CatFileBatch` is in-progress"
			if repo.batchLockStack != "" {
				msg = fmt.Sprintf("%s -- CatFileBatch was started at stack trace %q", msg, repo.batchLockStack)
			}
			panic(msg)
		}
		if repo.batch != nil {
			repo.batch.Close()
			repo.batch = nil
		}
		// Lock also never released -- same as batchLock.
		if !repo.checkLock.TryLock() {
			msg := "attempting to close repository while `CatFileBatchCheck` is in-progress"
			if repo.checkLockStack != "" {
				msg = fmt.Sprintf("%s -- CatFileBatchCheck was started at stack trace %q", msg, repo.checkLockStack)
			}
			panic(msg)
		}
		if repo.check != nil {
			repo.check.Close()
			repo.check = nil
		}

		repo.LastCommitCache = nil
		repo.tagCache = nil
	})

	return nil
}
