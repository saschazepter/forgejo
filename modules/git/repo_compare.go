// Copyright 2015 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// CompareInfo represents needed information for comparing references.
type CompareInfo struct {
	MergeBase    string
	BaseCommitID string
	HeadCommitID string
	Commits      []*Commit
	NumFiles     int
}

// GetMergeBaseSimple returns the merge base of base and head.
func (repo *Repository) GetMergeBaseSimple(base, head string) (string, error) {
	stdout, _, err := NewCommand(repo.Ctx, "merge-base").AddDashesAndList(base, head).RunStdString(&RunOpts{Dir: repo.Path})
	return strings.TrimSpace(stdout), err
}

// GetMergeBase checks and returns merge base of two branches and the reference used as base.
func (repo *Repository) GetMergeBase(base, head string) (string, error) {
	stdout, _, err := NewCommand(repo.Ctx, "merge-base").AddDashesAndList(base, head).RunStdString(&RunOpts{Dir: repo.Path})
	return strings.TrimSpace(stdout), err
}

// GetCompareInfo generates and returns compare information between base and head branches of repositories.
func (repo *Repository) GetCompareInfo(baseBranch, headBranch string, directComparison, fileOnly bool) (_ *CompareInfo, err error) {
	compareInfo := new(CompareInfo)

	compareInfo.HeadCommitID, err = repo.GetRefCommitID(headBranch)
	if err != nil {
		compareInfo.HeadCommitID = headBranch
	}

	compareInfo.MergeBase, err = repo.GetMergeBase(baseBranch, headBranch)
	if err == nil {
		compareInfo.BaseCommitID, err = repo.GetRefCommitID(baseBranch)
		if err != nil {
			compareInfo.BaseCommitID = baseBranch
		}
		separator := "..."
		baseCommitID := compareInfo.MergeBase
		if directComparison {
			separator = ".."
			baseCommitID = compareInfo.BaseCommitID
		}

		// We have a common base - therefore we know that ... should work
		if !fileOnly {
			// avoid: ambiguous argument 'refs/a...refs/b': unknown revision or path not in the working tree. Use '--': 'git <command> [<revision>...] -- [<file>...]'
			var logs []byte
			logs, _, err = NewCommand(repo.Ctx, "log").AddArguments(prettyLogFormat).
				AddDynamicArguments(baseCommitID + separator + headBranch).AddArguments("--").
				RunStdBytes(&RunOpts{Dir: repo.Path})
			if err != nil {
				return nil, err
			}
			compareInfo.Commits, err = repo.parsePrettyFormatLogToList(logs)
			if err != nil {
				return nil, fmt.Errorf("parsePrettyFormatLogToList: %w", err)
			}
		} else {
			compareInfo.Commits = []*Commit{}
		}
	} else {
		compareInfo.Commits = []*Commit{}
		compareInfo.MergeBase, err = repo.GetRefCommitID(baseBranch)
		if err != nil {
			compareInfo.MergeBase = baseBranch
		}
		compareInfo.BaseCommitID = compareInfo.MergeBase
	}

	// Count number of changed files.
	// This probably should be removed as we need to use shortstat elsewhere
	// Now there is git diff --shortstat but this appears to be slower than simply iterating with --nameonly
	compareInfo.NumFiles, err = repo.GetDiffNumChangedFiles(baseBranch, headBranch, directComparison)
	if err != nil {
		return nil, err
	}
	return compareInfo, nil
}

type lineCountWriter struct {
	numLines int
}

// Write counts the number of newlines in the provided bytestream
func (l *lineCountWriter) Write(p []byte) (n int, err error) {
	n = len(p)
	l.numLines += bytes.Count(p, []byte{'\000'})
	return n, err
}

// GetDiffNumChangedFiles counts the number of changed files
// This is substantially quicker than shortstat but...
func (repo *Repository) GetDiffNumChangedFiles(base, head string, directComparison bool) (int, error) {
	// Now there is git diff --shortstat but this appears to be slower than simply iterating with --nameonly
	w := &lineCountWriter{}
	stderr := new(bytes.Buffer)

	separator := "..."
	if directComparison {
		separator = ".."
	}

	// avoid: ambiguous argument 'refs/a...refs/b': unknown revision or path not in the working tree. Use '--': 'git <command> [<revision>...] -- [<file>...]'
	if err := NewCommand(repo.Ctx, "diff", "-z", "--name-only").AddDynamicArguments(base + separator + head).AddArguments("--").
		Run(&RunOpts{
			Dir:    repo.Path,
			Stdout: w,
			Stderr: stderr,
		}); err != nil {
		if strings.Contains(stderr.String(), "no merge base") {
			// git >= 2.28 now returns an error if base and head have become unrelated.
			// previously it would return the results of git diff -z --name-only base head so let's try that...
			w = &lineCountWriter{}
			stderr.Reset()
			if err = NewCommand(repo.Ctx, "diff", "-z", "--name-only").AddDynamicArguments(base, head).AddArguments("--").Run(&RunOpts{
				Dir:    repo.Path,
				Stdout: w,
				Stderr: stderr,
			}); err == nil {
				return w.numLines, nil
			}
		}
		return 0, fmt.Errorf("%w: Stderr: %s", err, stderr)
	}
	return w.numLines, nil
}

var (
	ErrNoMergebaseFound        = errors.New("no merge base found")
	ErrMultipleMergebasesFound = errors.New("multiple merge bases found")
)

// GetShortStat returns the number of changed files, additions and deletions. If
// `useMergebase` is specified then the merge base between `base` and `head` is
// used to compare against `head`.
func (repo *Repository) GetShortStat(base, head string, useMergebase bool) (numFiles, totalAdditions, totalDeletions int, err error) {
	cmd := NewCommand(repo.Ctx, "diff-tree", "--shortstat", "--find-renames")
	if useMergebase {
		cmd = cmd.AddArguments("--merge-base")
	}
	cmd.AddDynamicArguments(base, head)

	stdout, stderr, err := cmd.RunStdString(&RunOpts{Dir: repo.Path})
	if err != nil {
		switch stderr {
		case "fatal: no merge base found\n":
			return 0, 0, 0, ErrNoMergebaseFound
		case "fatal: multiple merge bases found\n":
			return 0, 0, 0, ErrMultipleMergebasesFound
		}
		return 0, 0, 0, err
	}

	return parseDiffStat(stdout)
}

// GetCommitShortStat returns the number of files, total additions and total deletions the commit has.
func (repo *Repository) GetCommitShortStat(commitID string) (numFiles, totalAdditions, totalDeletions int, err error) {
	cmd := NewCommand(repo.Ctx, "diff-tree", "--shortstat", "--no-commit-id", "--root", "--find-renames").AddDynamicArguments(commitID)
	stdout, _, err := cmd.RunStdString(&RunOpts{Dir: repo.Path})
	if err != nil {
		return 0, 0, 0, err
	}

	return parseDiffStat(stdout)
}

// GetIndexShortStat returns the number of files, total additions and total
// deletions the commit has.
//
// NOTE: It uses `git-diff-index`, should only be used when working with
// temporary repository. When working on bare repositories use `GetCommitShortStat`.
func GetIndexShortStat(ctx context.Context, repoPath, commitID string) (numFiles, totalAdditions, totalDeletions int, err error) {
	cmd := NewCommand(ctx, "diff-index", "--cached", "--shortstat").AddDynamicArguments(commitID)
	stdout, _, err := cmd.RunStdString(&RunOpts{Dir: repoPath})
	if err != nil {
		return 0, 0, 0, err
	}

	return parseDiffStat(stdout)
}

var shortStatFormat = regexp.MustCompile(
	`\s*(\d+) files? changed(?:, (\d+) insertions?\(\+\))?(?:, (\d+) deletions?\(-\))?`,
)

var patchCommits = regexp.MustCompile(`^From\s(\w+)\s`)

func parseDiffStat(stdout string) (numFiles, totalAdditions, totalDeletions int, err error) {
	if len(stdout) == 0 || stdout == "\n" {
		return 0, 0, 0, nil
	}
	groups := shortStatFormat.FindStringSubmatch(stdout)
	if len(groups) != 4 {
		return 0, 0, 0, fmt.Errorf("unable to parse shortstat: %s groups: %s", stdout, groups)
	}

	numFiles, err = strconv.Atoi(groups[1])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("unable to parse shortstat: %s. Error parsing NumFiles %w", stdout, err)
	}

	if len(groups[2]) != 0 {
		totalAdditions, err = strconv.Atoi(groups[2])
		if err != nil {
			return 0, 0, 0, fmt.Errorf("unable to parse shortstat: %s. Error parsing NumAdditions %w", stdout, err)
		}
	}

	if len(groups[3]) != 0 {
		totalDeletions, err = strconv.Atoi(groups[3])
		if err != nil {
			return 0, 0, 0, fmt.Errorf("unable to parse shortstat: %s. Error parsing NumDeletions %w", stdout, err)
		}
	}
	return numFiles, totalAdditions, totalDeletions, err
}

// GetDiffOrPatch generates either diff or formatted patch data between given revisions
func (repo *Repository) GetDiffOrPatch(base, head string, w io.Writer, patch, binary bool) error {
	if patch {
		return repo.GetPatch(base, head, w)
	}
	if binary {
		return repo.GetDiffBinary(base, head, w)
	}
	return repo.GetDiff(base, head, w)
}

// GetDiff generates and returns patch data between given revisions, optimized for human readability
func (repo *Repository) GetDiff(base, head string, w io.Writer) error {
	return NewCommand(repo.Ctx, "diff", "-p").AddDynamicArguments(base, head).Run(&RunOpts{
		Dir:    repo.Path,
		Stdout: w,
	})
}

// GetDiffBinary generates and returns patch data between given revisions, including binary diffs.
func (repo *Repository) GetDiffBinary(base, head string, w io.Writer) error {
	return NewCommand(repo.Ctx, "diff", "-p", "--binary", "--histogram").AddDynamicArguments(base, head).Run(&RunOpts{
		Dir:    repo.Path,
		Stdout: w,
	})
}

// GetPatch generates and returns format-patch data between given revisions, able to be used with `git apply`
func (repo *Repository) GetPatch(base, head string, w io.Writer) error {
	stderr := new(bytes.Buffer)
	err := NewCommand(repo.Ctx, "format-patch", "--binary", "--stdout").AddDynamicArguments(base + "..." + head).
		Run(&RunOpts{
			Dir:    repo.Path,
			Stdout: w,
			Stderr: stderr,
		})
	if err != nil && bytes.Contains(stderr.Bytes(), []byte("no merge base")) {
		return NewCommand(repo.Ctx, "format-patch", "--binary", "--stdout").AddDynamicArguments(base, head).
			Run(&RunOpts{
				Dir:    repo.Path,
				Stdout: w,
			})
	}
	return err
}

// GetFilesChangedBetween returns a list of all files that have been changed between the given commits
// If base is undefined empty SHA (zeros), it only returns the files changed in the head commit
// If base is the SHA of an empty tree (EmptyTreeSHA), it returns the files changes from the initial commit to the head commit
func (repo *Repository) GetFilesChangedBetween(base, head string) ([]string, error) {
	objectFormat, err := repo.GetObjectFormat()
	if err != nil {
		return nil, err
	}
	cmd := NewCommand(repo.Ctx, "diff-tree", "--name-only", "--root", "--no-commit-id", "-r", "-z")
	if base == objectFormat.EmptyObjectID().String() {
		cmd.AddDynamicArguments(head)
	} else {
		cmd.AddDynamicArguments(base, head)
	}
	stdout, _, err := cmd.RunStdString(&RunOpts{Dir: repo.Path})
	if err != nil {
		return nil, err
	}
	split := strings.Split(stdout, "\000")

	// Because Git will always emit filenames with a terminal NUL ignore the last entry in the split - which will always be empty.
	if len(split) > 0 {
		split = split[:len(split)-1]
	}

	return split, err
}

// GetDiffFromMergeBase generates and return patch data from merge base to head
func (repo *Repository) GetDiffFromMergeBase(base, head string, w io.Writer) error {
	stderr := new(bytes.Buffer)
	err := NewCommand(repo.Ctx, "diff", "-p", "--binary").AddDynamicArguments(base + "..." + head).
		Run(&RunOpts{
			Dir:    repo.Path,
			Stdout: w,
			Stderr: stderr,
		})
	if err != nil && bytes.Contains(stderr.Bytes(), []byte("no merge base")) {
		return repo.GetDiffBinary(base, head, w)
	}
	return err
}

// ReadPatchCommit will check if a diff patch exists and return stats
func (repo *Repository) ReadPatchCommit(prID int64) (commitSHA string, err error) {
	// Migrated repositories download patches to "pulls" location
	patchFile := fmt.Sprintf("pulls/%d.patch", prID)
	loadPatch, err := os.Open(filepath.Join(repo.Path, patchFile))
	if err != nil {
		return "", err
	}
	defer loadPatch.Close()
	// Read only the first line of the patch - usually it contains the first commit made in patch
	scanner := bufio.NewScanner(loadPatch)
	scanner.Scan()
	// Parse the Patch stats, sometimes Migration returns a 404 for the patch file
	commitSHAGroups := patchCommits.FindStringSubmatch(scanner.Text())
	if len(commitSHAGroups) != 0 {
		commitSHA = commitSHAGroups[1]
	} else {
		return "", errors.New("patch file doesn't contain valid commit ID")
	}
	return commitSHA, nil
}
