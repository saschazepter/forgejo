// Copyright 2015 The Gogs Authors. All rights reserved.
// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"forgejo.org/modules/log"
)

// BranchPrefix base dir of the branch information file store on git
const BranchPrefix = "refs/heads/"

// IsReferenceExist returns true if given reference exists in the repository.
func IsReferenceExist(ctx context.Context, repoPath, name string) bool {
	_, _, err := NewCommand(ctx, "show-ref", "--verify").AddDashesAndList(name).RunStdString(&RunOpts{Dir: repoPath})
	return err == nil
}

// IsBranchExist returns true if given branch exists in the repository.
func IsBranchExist(ctx context.Context, repoPath, name string) bool {
	return IsReferenceExist(ctx, repoPath, BranchPrefix+name)
}

// Branch represents a Git branch.
type Branch struct {
	Name string
	Path string

	gitRepo *Repository
}

// GetHEADBranch returns corresponding branch of HEAD.
func (repo *Repository) GetHEADBranch() (*Branch, error) {
	if repo == nil {
		return nil, errors.New("nil repo")
	}
	stdout, _, err := NewCommand(repo.Ctx, "symbolic-ref", "HEAD").RunStdString(&RunOpts{Dir: repo.Path})
	if err != nil {
		return nil, err
	}
	stdout = strings.TrimSpace(stdout)

	if !strings.HasPrefix(stdout, BranchPrefix) {
		return nil, fmt.Errorf("invalid HEAD branch: %v", stdout)
	}

	return &Branch{
		Name:    stdout[len(BranchPrefix):],
		Path:    stdout,
		gitRepo: repo,
	}, nil
}

func GetDefaultBranch(ctx context.Context, repoPath string) (string, error) {
	stdout, _, err := NewCommand(ctx, "symbolic-ref", "HEAD").RunStdString(&RunOpts{Dir: repoPath})
	if err != nil {
		return "", err
	}
	stdout = strings.TrimSpace(stdout)
	if !strings.HasPrefix(stdout, BranchPrefix) {
		return "", errors.New("the HEAD is not a branch: " + stdout)
	}
	return strings.TrimPrefix(stdout, BranchPrefix), nil
}

// GetBranch returns a branch by it's name
func (repo *Repository) GetBranch(branch string) (*Branch, error) {
	if !repo.IsBranchExist(branch) {
		return nil, ErrBranchNotExist{branch}
	}
	return &Branch{
		Path:    repo.Path,
		Name:    branch,
		gitRepo: repo,
	}, nil
}

// GetBranches returns a slice of *git.Branch
func (repo *Repository) GetBranches(skip, limit int) ([]*Branch, int, error) {
	brs, countAll, err := repo.GetBranchNames(skip, limit)
	if err != nil {
		return nil, 0, err
	}

	branches := make([]*Branch, len(brs))
	for i := range brs {
		branches[i] = &Branch{
			Path:    repo.Path,
			Name:    brs[i],
			gitRepo: repo,
		}
	}

	return branches, countAll, nil
}

// DeleteBranchOptions Option(s) for delete branch
type DeleteBranchOptions struct {
	Force bool
}

// DeleteBranch delete a branch by name on repository.
func (repo *Repository) DeleteBranch(name string, opts DeleteBranchOptions) error {
	cmd := NewCommand(repo.Ctx, "branch")

	if opts.Force {
		cmd.AddArguments("-D")
	} else {
		cmd.AddArguments("-d")
	}

	cmd.AddDashesAndList(name)
	_, _, err := cmd.RunStdString(&RunOpts{Dir: repo.Path})

	return err
}

// CreateBranch create a new branch
func (repo *Repository) CreateBranch(branch, oldbranchOrCommit string) error {
	cmd := NewCommand(repo.Ctx, "branch")
	cmd.AddDashesAndList(branch, oldbranchOrCommit)

	_, _, err := cmd.RunStdString(&RunOpts{Dir: repo.Path})

	return err
}

// AddRemote adds a new remote to repository.
func (repo *Repository) AddRemote(name, url string, fetch bool) error {
	cmd := NewCommand(repo.Ctx, "remote", "add")
	if fetch {
		cmd.AddArguments("-f")
	}
	cmd.AddDynamicArguments(name, url)

	_, _, err := cmd.RunStdString(&RunOpts{Dir: repo.Path})
	return err
}

// RemoveRemote removes a remote from repository.
func (repo *Repository) RemoveRemote(name string) error {
	_, _, err := NewCommand(repo.Ctx, "remote", "rm").AddDynamicArguments(name).RunStdString(&RunOpts{Dir: repo.Path})
	return err
}

// GetCommit returns the head commit of a branch
func (branch *Branch) GetCommit() (*Commit, error) {
	return branch.gitRepo.GetBranchCommit(branch.Name)
}

// RenameBranch rename a branch
func (repo *Repository) RenameBranch(from, to string) error {
	_, _, err := NewCommand(repo.Ctx, "branch", "-m").AddDynamicArguments(from, to).RunStdString(&RunOpts{Dir: repo.Path})
	return err
}

// IsObjectExist returns true if given reference exists in the repository.
func (repo *Repository) IsObjectExist(name string) bool {
	if name == "" {
		return false
	}

	wr, rd, cancel, err := repo.CatFileBatchCheck(repo.Ctx)
	if err != nil {
		log.Debug("Error writing to CatFileBatchCheck %v", err)
		return false
	}
	defer cancel()
	_, err = wr.Write([]byte(name + "\n"))
	if err != nil {
		log.Debug("Error writing to CatFileBatchCheck %v", err)
		return false
	}
	sha, _, _, err := ReadBatchLine(rd)
	return err == nil && bytes.HasPrefix(sha, []byte(strings.TrimSpace(name)))
}

// IsReferenceExist returns true if given reference exists in the repository.
func (repo *Repository) IsReferenceExist(name string) bool {
	if name == "" {
		return false
	}

	wr, rd, cancel, err := repo.CatFileBatchCheck(repo.Ctx)
	if err != nil {
		log.Debug("Error writing to CatFileBatchCheck %v", err)
		return false
	}
	defer cancel()
	_, err = wr.Write([]byte(name + "\n"))
	if err != nil {
		log.Debug("Error writing to CatFileBatchCheck %v", err)
		return false
	}
	_, _, _, err = ReadBatchLine(rd)
	return err == nil
}

// IsBranchExist returns true if given branch exists in current repository.
func (repo *Repository) IsBranchExist(name string) bool {
	if repo == nil || name == "" {
		return false
	}

	return repo.IsReferenceExist(BranchPrefix + name)
}

// GetBranchNames returns branches from the repository, skipping "skip" initial branches and
// returning at most "limit" branches, or all branches if "limit" is 0.
func (repo *Repository) GetBranchNames(skip, limit int) ([]string, int, error) {
	return callShowRef(repo.Ctx, repo.Path, BranchPrefix, TrustedCmdArgs{BranchPrefix, "--sort=-committerdate"}, skip, limit)
}

// WalkReferences walks all the references from the repository
// refType should be empty, ObjectTag or ObjectBranch. All other values are equivalent to empty.
func (repo *Repository) WalkReferences(refType ObjectType, skip, limit int, walkfn func(sha1, refname string) error) (int, error) {
	var args TrustedCmdArgs
	switch refType {
	case ObjectTag:
		args = TrustedCmdArgs{TagPrefix, "--sort=-taggerdate"}
	case ObjectBranch:
		args = TrustedCmdArgs{BranchPrefix, "--sort=-committerdate"}
	}

	return WalkShowRef(repo.Ctx, repo.Path, args, skip, limit, walkfn)
}

// callShowRef return refs, if limit = 0 it will not limit
func callShowRef(ctx context.Context, repoPath, trimPrefix string, extraArgs TrustedCmdArgs, skip, limit int) (branchNames []string, countAll int, err error) {
	countAll, err = WalkShowRef(ctx, repoPath, extraArgs, skip, limit, func(_, branchName string) error {
		branchName = strings.TrimPrefix(branchName, trimPrefix)
		branchNames = append(branchNames, branchName)

		return nil
	})
	return branchNames, countAll, err
}

func WalkShowRef(ctx context.Context, repoPath string, extraArgs TrustedCmdArgs, skip, limit int, walkfn func(sha1, refname string) error) (countAll int, err error) {
	stdoutReader, stdoutWriter := io.Pipe()
	defer func() {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
	}()

	go func() {
		stderrBuilder := &strings.Builder{}
		args := TrustedCmdArgs{"for-each-ref", "--format=%(objectname) %(refname)"}
		args = append(args, extraArgs...)
		err := NewCommand(ctx, args...).Run(&RunOpts{
			Dir:    repoPath,
			Stdout: stdoutWriter,
			Stderr: stderrBuilder,
		})
		if err != nil {
			if stderrBuilder.Len() == 0 {
				_ = stdoutWriter.Close()
				return
			}
			_ = stdoutWriter.CloseWithError(ConcatenateError(err, stderrBuilder.String()))
		} else {
			_ = stdoutWriter.Close()
		}
	}()

	i := 0
	bufReader := bufio.NewReader(stdoutReader)
	for i < skip {
		_, isPrefix, err := bufReader.ReadLine()
		if err == io.EOF {
			return i, nil
		}
		if err != nil {
			return 0, err
		}
		if !isPrefix {
			i++
		}
	}
	for limit == 0 || i < skip+limit {
		// The output of show-ref is simply a list:
		// <sha> SP <ref> LF
		sha, err := bufReader.ReadString(' ')
		if err == io.EOF {
			return i, nil
		}
		if err != nil {
			return 0, err
		}

		branchName, err := bufReader.ReadString('\n')
		if err == io.EOF {
			// This shouldn't happen... but we'll tolerate it for the sake of peace
			return i, nil
		}
		if err != nil {
			return i, err
		}

		if len(branchName) > 0 {
			branchName = branchName[:len(branchName)-1]
		}

		if len(sha) > 0 {
			sha = sha[:len(sha)-1]
		}

		err = walkfn(sha, branchName)
		if err != nil {
			return i, err
		}
		i++
	}
	// count all refs
	for limit != 0 {
		_, isPrefix, err := bufReader.ReadLine()
		if err == io.EOF {
			return i, nil
		}
		if err != nil {
			return 0, err
		}
		if !isPrefix {
			i++
		}
	}
	return i, nil
}

// GetRefsBySha returns all references filtered with prefix that belong to a sha commit hash
func (repo *Repository) GetRefsBySha(sha, prefix string) ([]string, error) {
	var revList []string
	_, err := WalkShowRef(repo.Ctx, repo.Path, nil, 0, 0, func(walkSha, refname string) error {
		if walkSha == sha && strings.HasPrefix(refname, prefix) {
			revList = append(revList, refname)
		}
		return nil
	})
	return revList, err
}
