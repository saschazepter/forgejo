// Copyright 2019 The Gitea Authors.
// All rights reserved.
// SPDX-License-Identifier: MIT

package pull

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"forgejo.org/models"
	git_model "forgejo.org/models/git"
	issues_model "forgejo.org/models/issues"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/graceful"
	"forgejo.org/modules/log"
	"forgejo.org/modules/process"
	"forgejo.org/modules/util"

	"github.com/gobwas/glob"
)

// DownloadDiffOrPatch will write the patch for the pr to the writer
func DownloadDiffOrPatch(ctx context.Context, pr *issues_model.PullRequest, w io.Writer, patch, binary bool) error {
	if err := pr.LoadBaseRepo(ctx); err != nil {
		log.Error("Unable to load base repository ID %d for pr #%d [%d]", pr.BaseRepoID, pr.Index, pr.ID)
		return err
	}

	gitRepo, closer, err := gitrepo.RepositoryFromContextOrOpen(ctx, pr.BaseRepo)
	if err != nil {
		return fmt.Errorf("OpenRepository: %w", err)
	}
	defer closer.Close()

	if err := gitRepo.GetDiffOrPatch(pr.MergeBase, pr.GetGitRefName(), w, patch, binary); err != nil {
		log.Error("Unable to get patch file from %s to %s in %s Error: %v", pr.MergeBase, pr.HeadBranch, pr.BaseRepo.FullName(), err)
		return fmt.Errorf("Unable to get patch file from %s to %s in %s Error: %w", pr.MergeBase, pr.HeadBranch, pr.BaseRepo.FullName(), err)
	}
	return nil
}

// TestPatch will test whether a simple patch will apply
func TestPatch(pr *issues_model.PullRequest) error {
	ctx, _, finished := process.GetManager().AddContext(graceful.GetManager().HammerContext(), fmt.Sprintf("TestPatch: %s", pr))
	defer finished()

	testPatchCtx, err := testPatch(ctx, pr)
	testPatchCtx.close()
	return err
}

type testPatchContext struct {
	headRev        string
	headIsCommitID bool
	baseRev        string
	env            []string
	gitRepo        *git.Repository
	close          func()
}

// LoadHeadRevision loads the necessary information to access the head revision.
func (t *testPatchContext) LoadHeadRevision(ctx context.Context, pr *issues_model.PullRequest) error {
	// If AGit, then use HeadCommitID if set (AGit flow creates pull request),
	// otherwise use the pull request reference.
	if pr.Flow == issues_model.PullRequestFlowAGit {
		if len(pr.HeadCommitID) > 0 {
			t.headRev = pr.HeadCommitID
			t.headIsCommitID = true
			return nil
		}
		t.headRev = pr.GetGitRefName()
		return nil
	}

	// If it is within the same repository, simply return the branch name.
	if pr.BaseRepoID == pr.HeadRepoID {
		t.headRev = pr.GetGitHeadBranchRefName()
		return nil
	}

	// We are in Github flow, head and base repository are different.
	// Resolve the head branch to a commitID and return a Git alternate
	// environment for the head repository.
	gitRepo, err := git.OpenRepository(ctx, pr.HeadRepo.RepoPath())
	if err != nil {
		return err
	}
	defer gitRepo.Close()

	headCommitID, err := gitRepo.GetRefCommitID(pr.GetGitHeadBranchRefName())
	if err != nil {
		return err
	}

	t.headRev = headCommitID
	t.headIsCommitID = true
	t.env = append(os.Environ(), `GIT_ALTERNATE_OBJECT_DIRECTORIES=`+pr.HeadRepo.RepoPath()+"/objects")
	return nil
}

// getTestPatchCtx constructs a new testpatch context for the given pull request.
// If `onBare` is true, then the context will use the base repository that does
// not contain a working tree. Otherwise a temporary repository is created that
// contains a working tree.
func getTestPatchCtx(ctx context.Context, pr *issues_model.PullRequest, onBare bool) (*testPatchContext, error) {
	testPatchCtx := &testPatchContext{
		close: func() {},
	}

	if onBare {
		if err := pr.LoadBaseRepo(ctx); err != nil {
			return testPatchCtx, fmt.Errorf("LoadBaseRepo: %w", err)
		}
		if err := pr.LoadHeadRepo(ctx); err != nil {
			return testPatchCtx, fmt.Errorf("LoadHeadRepo: %w", err)
		}

		if err := testPatchCtx.LoadHeadRevision(ctx, pr); err != nil {
			return testPatchCtx, fmt.Errorf("LoadHeadRevision: %w", err)
		}

		gitRepo, err := git.OpenRepository(ctx, pr.BaseRepo.RepoPath())
		if err != nil {
			return testPatchCtx, fmt.Errorf("OpenRepository: %w", err)
		}

		testPatchCtx.baseRev = git.BranchPrefix + pr.BaseBranch
		testPatchCtx.gitRepo = gitRepo
		testPatchCtx.close = func() {
			gitRepo.Close()
		}
	} else {
		prCtx, cancel, err := createTemporaryRepoForPR(ctx, pr)
		if err != nil {
			return testPatchCtx, fmt.Errorf("createTemporaryRepoForPR: %w", err)
		}
		testPatchCtx.close = cancel

		gitRepo, err := git.OpenRepository(ctx, prCtx.tmpBasePath)
		if err != nil {
			return testPatchCtx, fmt.Errorf("OpenRepository: %w", err)
		}

		testPatchCtx.baseRev = git.BranchPrefix + baseBranch
		testPatchCtx.headRev = git.BranchPrefix + trackingBranch
		testPatchCtx.gitRepo = gitRepo
		testPatchCtx.close = func() {
			cancel()
			gitRepo.Close()
		}
	}
	return testPatchCtx, nil
}

func testPatch(ctx context.Context, pr *issues_model.PullRequest) (*testPatchContext, error) {
	testPatchCtx, err := getTestPatchCtx(ctx, pr, git.SupportGitMergeTree)
	if err != nil {
		return testPatchCtx, fmt.Errorf("getTestPatchCtx: %w", err)
	}

	// 1. update merge base
	pr.MergeBase, _, err = git.NewCommand(ctx, "merge-base").AddDashesAndList(testPatchCtx.baseRev, testPatchCtx.headRev).RunStdString(&git.RunOpts{Dir: testPatchCtx.gitRepo.Path, Env: testPatchCtx.env})
	if err != nil {
		var err2 error
		pr.MergeBase, err2 = testPatchCtx.gitRepo.GetRefCommitID(testPatchCtx.baseRev)
		if err2 != nil {
			return testPatchCtx, fmt.Errorf("GetMergeBase: %v and can't find commit ID for base: %w", err, err2)
		}
	}
	pr.MergeBase = strings.TrimSpace(pr.MergeBase)

	if testPatchCtx.headIsCommitID {
		pr.HeadCommitID = testPatchCtx.headRev
	} else {
		if pr.HeadCommitID, err = testPatchCtx.gitRepo.GetRefCommitID(testPatchCtx.headRev); err != nil {
			return testPatchCtx, fmt.Errorf("GetRefCommitID: can't find commit ID for head: %w", err)
		}
	}

	// If the head commit is equal to the merge base it roughly means that the
	// head commit is a parent of the base commit.
	if pr.HeadCommitID == pr.MergeBase {
		pr.Status = issues_model.PullRequestStatusAncestor
		return testPatchCtx, nil
	}

	// 2. Check for conflicts
	if conflicts, err := checkConflicts(ctx, pr, testPatchCtx); err != nil || conflicts || pr.Status == issues_model.PullRequestStatusEmpty {
		if err != nil {
			return testPatchCtx, fmt.Errorf("checkConflicts: %w", err)
		}
		return testPatchCtx, nil
	}

	// 3. Check for protected files changes
	if err = checkPullFilesProtection(ctx, pr, testPatchCtx); err != nil {
		return testPatchCtx, fmt.Errorf("checkPullFilesProtection: %v", err)
	}

	if len(pr.ChangedProtectedFiles) > 0 {
		log.Trace("Found %d protected files changed", len(pr.ChangedProtectedFiles))
	}

	pr.Status = issues_model.PullRequestStatusMergeable

	return testPatchCtx, nil
}

type errMergeConflict struct {
	filename string
}

func (e *errMergeConflict) Error() string {
	return fmt.Sprintf("conflict detected at: %s", e.filename)
}

func attemptMerge(ctx context.Context, file *unmergedFile, tmpBasePath string, filesToRemove *[]string, filesToAdd *[]git.IndexObjectInfo) error {
	log.Trace("Attempt to merge:\n%v", file)

	switch {
	case file.stage1 != nil && (file.stage2 == nil || file.stage3 == nil):
		// 1. Deleted in one or both:
		//
		// Conflict <==> the stage1 !SameAs to the undeleted one
		if (file.stage2 != nil && !file.stage1.SameAs(file.stage2)) || (file.stage3 != nil && !file.stage1.SameAs(file.stage3)) {
			// Conflict!
			return &errMergeConflict{file.stage1.path}
		}

		// Not a genuine conflict and we can simply remove the file from the index
		*filesToRemove = append(*filesToRemove, file.stage1.path)
		return nil
	case file.stage1 == nil && file.stage2 != nil && (file.stage3 == nil || file.stage2.SameAs(file.stage3)):
		// 2. Added in ours but not in theirs or identical in both
		//
		// Not a genuine conflict just add to the index
		*filesToAdd = append(*filesToAdd, git.IndexObjectInfo{Mode: file.stage2.mode, Object: git.MustIDFromString(file.stage2.sha), Filename: file.stage2.path})
		return nil
	case file.stage1 == nil && file.stage2 != nil && file.stage3 != nil && file.stage2.sha == file.stage3.sha && file.stage2.mode != file.stage3.mode:
		// 3. Added in both with the same sha but the modes are different
		//
		// Conflict! (Not sure that this can actually happen but we should handle)
		return &errMergeConflict{file.stage2.path}
	case file.stage1 == nil && file.stage2 == nil && file.stage3 != nil:
		// 4. Added in theirs but not ours:
		//
		// Not a genuine conflict just add to the index
		*filesToAdd = append(*filesToAdd, git.IndexObjectInfo{Mode: file.stage3.mode, Object: git.MustIDFromString(file.stage3.sha), Filename: file.stage3.path})
		return nil
	case file.stage1 == nil:
		// 5. Created by new in both
		//
		// Conflict!
		return &errMergeConflict{file.stage2.path}
	case file.stage2 != nil && file.stage3 != nil:
		// 5. Modified in both - we should try to merge in the changes but first:
		//
		if file.stage2.mode == "120000" || file.stage3.mode == "120000" {
			// 5a. Conflicting symbolic link change
			return &errMergeConflict{file.stage2.path}
		}
		if file.stage2.mode == "160000" || file.stage3.mode == "160000" {
			// 5b. Conflicting submodule change
			return &errMergeConflict{file.stage2.path}
		}
		if file.stage2.mode != file.stage3.mode {
			// 5c. Conflicting mode change
			return &errMergeConflict{file.stage2.path}
		}

		// Need to get the objects from the object db to attempt to merge
		root, _, err := git.NewCommand(ctx, "unpack-file").AddDynamicArguments(file.stage1.sha).RunStdString(&git.RunOpts{Dir: tmpBasePath})
		if err != nil {
			return fmt.Errorf("unable to get root object: %s at path: %s for merging. Error: %w", file.stage1.sha, file.stage1.path, err)
		}
		root = strings.TrimSpace(root)
		defer func() {
			_ = util.Remove(filepath.Join(tmpBasePath, root))
		}()

		base, _, err := git.NewCommand(ctx, "unpack-file").AddDynamicArguments(file.stage2.sha).RunStdString(&git.RunOpts{Dir: tmpBasePath})
		if err != nil {
			return fmt.Errorf("unable to get base object: %s at path: %s for merging. Error: %w", file.stage2.sha, file.stage2.path, err)
		}
		base = strings.TrimSpace(filepath.Join(tmpBasePath, base))
		defer func() {
			_ = util.Remove(base)
		}()
		head, _, err := git.NewCommand(ctx, "unpack-file").AddDynamicArguments(file.stage3.sha).RunStdString(&git.RunOpts{Dir: tmpBasePath})
		if err != nil {
			return fmt.Errorf("unable to get head object:%s at path: %s for merging. Error: %w", file.stage3.sha, file.stage3.path, err)
		}
		head = strings.TrimSpace(head)
		defer func() {
			_ = util.Remove(filepath.Join(tmpBasePath, head))
		}()

		// now git merge-file annoyingly takes a different order to the merge-tree ...
		_, _, conflictErr := git.NewCommand(ctx, "merge-file").AddDynamicArguments(base, root, head).RunStdString(&git.RunOpts{Dir: tmpBasePath})
		if conflictErr != nil {
			return &errMergeConflict{file.stage2.path}
		}

		// base now contains the merged data
		hash, _, err := git.NewCommand(ctx, "hash-object", "-w", "--path").AddDynamicArguments(file.stage2.path, base).RunStdString(&git.RunOpts{Dir: tmpBasePath})
		if err != nil {
			return err
		}
		hash = strings.TrimSpace(hash)
		*filesToAdd = append(*filesToAdd, git.IndexObjectInfo{Mode: file.stage2.mode, Object: git.MustIDFromString(hash), Filename: file.stage2.path})
		return nil
	default:
		if file.stage1 != nil {
			return &errMergeConflict{file.stage1.path}
		} else if file.stage2 != nil {
			return &errMergeConflict{file.stage2.path}
		} else if file.stage3 != nil {
			return &errMergeConflict{file.stage3.path}
		}
	}
	return nil
}

// AttemptThreeWayMerge will attempt to three way merge using git read-tree and then follow the git merge-one-file algorithm to attempt to resolve basic conflicts
func AttemptThreeWayMerge(ctx context.Context, gitRepo *git.Repository, base, ours, theirs, description string) (bool, []string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// First we use read-tree to do a simple three-way merge
	if _, _, err := git.NewCommand(ctx, "read-tree", "-m").AddDynamicArguments(base, ours, theirs).RunStdString(&git.RunOpts{Dir: gitRepo.Path}); err != nil {
		log.Error("Unable to run read-tree -m! Error: %v", err)
		return false, nil, fmt.Errorf("unable to run read-tree -m! Error: %w", err)
	}

	var filesToRemove []string
	var filesToAdd []git.IndexObjectInfo

	// Then we use git ls-files -u to list the unmerged files and collate the triples in unmergedfiles
	unmerged := make(chan *unmergedFile)
	go unmergedFiles(ctx, gitRepo.Path, unmerged)

	defer func() {
		cancel()
		for range unmerged {
			// empty the unmerged channel
		}
	}()

	numberOfConflicts := 0
	conflict := false
	conflictedFiles := make([]string, 0, 5)

	for file := range unmerged {
		if file == nil {
			break
		}
		if file.err != nil {
			cancel()
			return false, nil, file.err
		}

		// OK now we have the unmerged file triplet attempt to merge it
		if err := attemptMerge(ctx, file, gitRepo.Path, &filesToRemove, &filesToAdd); err != nil {
			if conflictErr, ok := err.(*errMergeConflict); ok {
				log.Trace("Conflict: %s in %s", conflictErr.filename, description)
				conflict = true
				if numberOfConflicts < 10 {
					conflictedFiles = append(conflictedFiles, conflictErr.filename)
				}
				numberOfConflicts++
				continue
			}
			return false, nil, err
		}
	}

	// Add and remove files in one command, as this is slow with many files otherwise
	if err := gitRepo.RemoveFilesFromIndex(filesToRemove...); err != nil {
		return false, nil, err
	}
	if err := gitRepo.AddObjectsToIndex(filesToAdd...); err != nil {
		return false, nil, err
	}

	return conflict, conflictedFiles, nil
}

// MergeTree runs a 3-way merge between `ours` and `theirs` with
// `base` as the merge base.
//
// It uses git-merge-tree(1) to do this merge without requiring a work-tree and
// can run in a base repository. It returns the object ID of the merge tree, if
// there are any conflicts and conflicted files.
func MergeTree(ctx context.Context, gitRepo *git.Repository, base, ours, theirs string, env []string) (string, bool, []string, error) {
	cmd := git.NewCommand(ctx, "merge-tree", "--write-tree", "-z", "--name-only", "--no-messages")
	if git.CheckGitVersionAtLeast("2.40") == nil {
		cmd.AddOptionFormat("--merge-base=%s", base)
	}

	stdout := &bytes.Buffer{}
	gitErr := cmd.AddDynamicArguments(ours, theirs).Run(&git.RunOpts{Dir: gitRepo.Path, Stdout: stdout, Env: env})
	if gitErr != nil && !git.IsErrorExitCode(gitErr, 1) {
		log.Error("Unable to run merge-tree: %v", gitErr)
		return "", false, nil, fmt.Errorf("unable to run merge-tree: %w", gitErr)
	}

	// There are two situations that we consider for the output:
	// 1. Clean merge and the output is <OID of toplevel tree>NUL
	// 2. Merge conflict and the output is <OID of toplevel tree>NUL<Conflicted file info>NUL
	treeOID, conflictedFileInfo, _ := strings.Cut(stdout.String(), "\x00")
	if len(conflictedFileInfo) == 0 {
		return treeOID, git.IsErrorExitCode(gitErr, 1), nil, nil
	}

	// Remove last NULL-byte from conflicted file info, then split with NULL byte as separator.
	return treeOID, true, strings.Split(conflictedFileInfo[:len(conflictedFileInfo)-1], "\x00"), nil
}

// checkConflicts takes a pull request and checks if merging it would result in
// merge conflicts and checks if the diff is empty; the status is set accordingly.
func checkConflicts(ctx context.Context, pr *issues_model.PullRequest, testPatchCtx *testPatchContext) (bool, error) {
	// Resets the conflict status.
	pr.ConflictedFiles = nil

	if git.SupportGitMergeTree {
		// Check for conflicts via a merge-tree.
		treeHash, conflict, conflictFiles, err := MergeTree(ctx, testPatchCtx.gitRepo, pr.MergeBase, testPatchCtx.baseRev, testPatchCtx.headRev, testPatchCtx.env)
		if err != nil {
			return false, fmt.Errorf("MergeTree: %w", err)
		}

		if !conflict {
			// No conflicts were detected, we now check if the pull request contains
			// changed code. We do this by comparing the tree of the merged tree and
			// that of the base reference.
			baseTree, err := testPatchCtx.gitRepo.GetTree(testPatchCtx.baseRev)
			if err != nil {
				return false, fmt.Errorf("GetTree: %w", err)
			}

			if treeHash == baseTree.ID.String() {
				log.Debug("PullRequest[%d]: Patch is empty - ignoring", pr.ID)
				pr.Status = issues_model.PullRequestStatusEmpty
			}
			return false, nil
		}

		pr.Status = issues_model.PullRequestStatusConflict
		pr.ConflictedFiles = conflictFiles

		log.Trace("Found %d files conflicted: %v", len(pr.ConflictedFiles), pr.ConflictedFiles)
		return true, nil
	}

	// 2. AttemptThreeWayMerge first - this is much quicker than plain patch to base
	description := fmt.Sprintf("PR[%d] %s/%s#%d", pr.ID, pr.BaseRepo.OwnerName, pr.BaseRepo.Name, pr.Index)
	conflict, conflictFiles, err := AttemptThreeWayMerge(ctx, testPatchCtx.gitRepo, pr.MergeBase, testPatchCtx.baseRev, testPatchCtx.headRev, description)
	if err != nil {
		return false, err
	}

	if !conflict {
		// No conflicts detected so we need to check if the patch is empty...
		// a. Write the newly merged tree and check the new tree-hash
		var treeHash string
		treeHash, _, err = git.NewCommand(ctx, "write-tree").RunStdString(&git.RunOpts{Dir: testPatchCtx.gitRepo.Path})
		if err != nil {
			lsfiles, _, _ := git.NewCommand(ctx, "ls-files", "-u").RunStdString(&git.RunOpts{Dir: testPatchCtx.gitRepo.Path})
			return false, fmt.Errorf("unable to write unconflicted tree: %w\n`git ls-files -u`:\n%s", err, lsfiles)
		}
		treeHash = strings.TrimSpace(treeHash)
		baseTree, err := testPatchCtx.gitRepo.GetTree(testPatchCtx.baseRev)
		if err != nil {
			return false, err
		}

		// b. compare the new tree-hash with the base tree hash
		if treeHash == baseTree.ID.String() {
			log.Debug("PullRequest[%d]: Patch is empty - ignoring", pr.ID)
			pr.Status = issues_model.PullRequestStatusEmpty
		}

		return false, nil
	}

	pr.Status = issues_model.PullRequestStatusConflict
	pr.ConflictedFiles = conflictFiles

	log.Trace("Found %d files conflicted: %v", len(pr.ConflictedFiles), pr.ConflictedFiles)
	return true, nil
}

// CheckFileProtection check file Protection
func CheckFileProtection(repo *git.Repository, oldCommitID, newCommitID string, patterns []glob.Glob, limit int, env []string) ([]string, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	affectedFiles, err := git.GetAffectedFiles(repo, oldCommitID, newCommitID, env)
	if err != nil {
		return nil, err
	}
	changedProtectedFiles := make([]string, 0, limit)
	for _, affectedFile := range affectedFiles {
		lpath := strings.ToLower(affectedFile)
		for _, pat := range patterns {
			if pat.Match(lpath) {
				changedProtectedFiles = append(changedProtectedFiles, lpath)
				break
			}
		}
		if len(changedProtectedFiles) >= limit {
			break
		}
	}
	if len(changedProtectedFiles) > 0 {
		err = models.ErrFilePathProtected{
			Path: changedProtectedFiles[0],
		}
	}
	return changedProtectedFiles, err
}

// CheckUnprotectedFiles check if the commit only touches unprotected files
func CheckUnprotectedFiles(repo *git.Repository, oldCommitID, newCommitID string, patterns []glob.Glob, env []string) (bool, error) {
	if len(patterns) == 0 {
		return false, nil
	}
	affectedFiles, err := git.GetAffectedFiles(repo, oldCommitID, newCommitID, env)
	if err != nil {
		return false, err
	}
	for _, affectedFile := range affectedFiles {
		lpath := strings.ToLower(affectedFile)
		unprotected := false
		for _, pat := range patterns {
			if pat.Match(lpath) {
				unprotected = true
				break
			}
		}
		if !unprotected {
			return false, nil
		}
	}
	return true, nil
}

// checkPullFilesProtection check if pr changed protected files and save results
func checkPullFilesProtection(ctx context.Context, pr *issues_model.PullRequest, testPatchCtx *testPatchContext) error {
	if pr.Status == issues_model.PullRequestStatusEmpty {
		pr.ChangedProtectedFiles = nil
		return nil
	}

	pb, err := git_model.GetFirstMatchProtectedBranchRule(ctx, pr.BaseRepoID, pr.BaseBranch)
	if err != nil {
		return err
	}

	if pb == nil {
		pr.ChangedProtectedFiles = nil
		return nil
	}

	pr.ChangedProtectedFiles, err = CheckFileProtection(testPatchCtx.gitRepo, pr.MergeBase, testPatchCtx.headRev, pb.GetProtectedFilePatterns(), 10, testPatchCtx.env)
	if err != nil && !models.IsErrFilePathProtected(err) {
		return err
	}
	return nil
}
