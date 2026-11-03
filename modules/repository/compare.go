// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPLv3-or-later

package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"forgejo.org/modules/git"
	"forgejo.org/modules/log"
	"forgejo.org/modules/optional"
)

// Information from comparing two references in two repositories.
type CrossRepositoryCompare struct {
	headRepo *git.Repository // always non-nil
	baseRepo *git.Repository // may be nil if the two repositories are actually the same underlying repository path

	// When performing a cross-repository comparison, a temporary repository is created which contains no objects, but
	// references baseRepo and headRepo through git alternates.  This repository and path must be closed and cleaned up.
	compareRepo *git.Repository // may be nil if the two repositories are actually the same underlying repository path
	comparePath optional.Option[string]

	MergeBase    string
	BaseCommitID string
	HeadCommitID string
	Commits      []*git.Commit
	NumFiles     int
}

// Closes all the resources of the cross-repository comparison.  Safe to invoke multiple times.
func (ci *CrossRepositoryCompare) Close() {
	if ci.headRepo != nil {
		if err := ci.headRepo.Close(); err != nil {
			log.Error("Error while closing head repository: %v", err)
		}
		ci.headRepo = nil
	}
	if ci.baseRepo != nil {
		if err := ci.baseRepo.Close(); err != nil {
			log.Error("Error while closing base repository: %v", err)
		}
		ci.baseRepo = nil
	}
	if ci.compareRepo != nil {
		if err := ci.compareRepo.Close(); err != nil {
			log.Error("Error while closing compare repository: %v", err)
		}
		ci.compareRepo = nil
	}
	if hasTmp, tmp := ci.comparePath.Get(); hasTmp {
		if err := RemoveTemporaryPath(tmp); err != nil {
			log.Error("Error while removing removing temporary repo: %v", err)
		}
	}
}

// The head repository of the comparison.  Note that this repository can only be used to resolve references in the head
// repository, and not for comparison operations.
func (ci *CrossRepositoryCompare) HeadRepo() *git.Repository {
	return ci.headRepo
}

// The base repository of the comparison.  Note that this repository can only be used to resolve references in the base
// repository, and not for comparison operations.
func (ci *CrossRepositoryCompare) BaseRepo() *git.Repository {
	if ci.baseRepo != nil {
		return ci.baseRepo
	}
	// When both repositories are the same path, NewCrossRepositoryCompare will initialize only headRepo in order to
	// allow the cached subprocesses to be reused more efficiently:
	return ci.headRepo
}

// The comparison repository for the compare operation.  This repository may be a repository that only has access to
// objects from the head & base repo, and not references.  Therefore, it can be used to perform compare operations, but
// only if those comparisons reference objects by their ID and not `heads/refs/...` for example.
func (ci *CrossRepositoryCompare) CompareRepo() *git.Repository {
	if ci.compareRepo != nil {
		return ci.compareRepo
	}
	// When both repositories are the same path, NewCrossRepositoryCompare will initialize only headRepo in order to
	// allow the cached subprocesses to be reused more efficiently:
	return ci.headRepo
}

// When comparing two distinct repositories, then we create a temp repo to support the comparisons.  This will be a git
// repo with alternates set-up that reference both the head and base repo.  Within this temp repo, we'll be able to
// perform `git merge-base`, `git log`, `git diff`, etc. commands.  We'll need to use object IDs for those commands --
// the temp repository won't have any refs.
func makeCompareRepo(ctx context.Context, baseRepo, headRepo *git.Repository) (comparePath string, compareRepo *git.Repository, err error) {
	returnSuccessfully := false

	// Check for equal object formats -- git alternates won't work on incompatible repos
	headObjectFormat, err := headRepo.GetObjectFormat()
	if err != nil {
		return "", nil, fmt.Errorf("failed to get head object format for compare: %w", err)
	}
	baseObjectFormat, err := baseRepo.GetObjectFormat()
	if err != nil {
		return "", nil, fmt.Errorf("failed to get base object format for compare: %w", err)
	}
	if headObjectFormat.Name() != baseObjectFormat.Name() {
		return "", nil, fmt.Errorf("unable to compare two repositories with different object formats; head was %q, base was %q", headObjectFormat.Name(), baseObjectFormat.Name())
	}

	tmpComparePath, err := CreateTemporaryPath("compare")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temporary path for compare: %w", err)
	}
	// Cleanup the temp repo if we don't return it to the caller:
	defer func() {
		if !returnSuccessfully {
			if err := RemoveTemporaryPath(tmpComparePath); err != nil {
				log.Error("Error whilst removing removing temporary repo: %v", err)
			}
		}
	}()

	if err := git.InitRepository(ctx, tmpComparePath, false, headObjectFormat.Name()); err != nil {
		return "", nil, fmt.Errorf("failed to init temp repo for compare: %w", err)
	}

	p := filepath.Join(tmpComparePath, ".git", "objects", "info", "alternates")
	headObjectsPath := filepath.Join(headRepo.Path, "objects")
	baseObjectsPath := filepath.Join(baseRepo.Path, "objects")
	if err := os.WriteFile(p, fmt.Appendf(nil, "%s\n%s\n", headObjectsPath, baseObjectsPath), 0o600); err != nil {
		return "", nil, fmt.Errorf("could not write to .git/objects/info/alternates file: %w", err)
	}

	compareRepo, err = git.OpenRepository(ctx, tmpComparePath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to open compare repo: %w", err)
	}

	returnSuccessfully = true
	return tmpComparePath, compareRepo, nil
}

// Compares the contents of two references from two repositories.  The repositories may have no commits in common; it is
// not necessary for one to be a fork of the other.  The `baseRepo` and `headRepo` parameters will be closed when the
// returned comparison object is closed (or immediately if an error occurs), and should not be closed by the caller. The
// returned CrossRepositoryCompare object must be closed by the caller.
func NewCrossRepositoryCompare(ctx context.Context, baseRepo, headRepo *git.Repository, baseRef, headRef string, directComparison, fileOnly bool) (*CrossRepositoryCompare, error) {
	var err error

	returnSuccessfully := false
	ci := new(CrossRepositoryCompare)

	// We're responsible for closing the repo objects, either by returning in a CrossRepositoryCompare, or directly if
	// we return an error:
	defer func() {
		if !returnSuccessfully {
			baseRepo.Close()
			headRepo.Close() // if head/base are the same object, repository.Close() is safe for to invoke multiple times
		}
	}()

	ci.headRepo = headRepo
	ci.baseRepo = baseRepo
	if headRepo.Path != baseRepo.Path {
		comparePath, compareRepo, err := makeCompareRepo(ctx, baseRepo, headRepo)
		if err != nil {
			return nil, fmt.Errorf("failed to create compare repository: %w", err)
		}
		// Cleanup the temp repo if we don't return it in compareInfo:
		defer func() {
			if !returnSuccessfully {
				if err := RemoveTemporaryPath(comparePath); err != nil {
					log.Error("Error whilst removing removing temporary repo: %v", err)
				}
				compareRepo.Close()
			}
		}()
		ci.comparePath = optional.Some(comparePath)
		ci.compareRepo = compareRepo
	} else {
		if ci.baseRepo != ci.headRepo { // different pointers, same underlying repo
			ci.baseRepo.Close() // don't leak subprocess of baseRepo
		}
		ci.baseRepo = nil // don't keep both repositories around with their own subprocesses, as they go to the same underlying repo
	}

	headCommitID, err := ci.HeadRepo().GetRefCommitID(headRef)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit ID from head repo for ref %q: %w", headRef, err)
	}

	baseCommitID, err := ci.BaseRepo().GetRefCommitID(baseRef)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit ID from base repo for ref %q: %w", baseRef, err)
	}

	innerCi, err := ci.CompareRepo().GetCompareInfo(baseCommitID, headCommitID, directComparison, fileOnly)
	if err != nil {
		return nil, fmt.Errorf("failed to get compare info: %w", err)
	}

	ci.MergeBase = innerCi.MergeBase
	ci.BaseCommitID = innerCi.BaseCommitID
	ci.HeadCommitID = innerCi.HeadCommitID
	ci.Commits = innerCi.Commits
	ci.NumFiles = innerCi.NumFiles

	returnSuccessfully = true // disarm the `defer` cleanup routines
	return ci, nil
}
