// Copyright 2015 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"strings"
	"time"
)

// CommitTreeOpts represents the possible options to CommitTree
type CommitTreeOpts struct {
	Parents    []string
	Message    string
	KeyID      string
	NoGPGSign  bool
	AlwaysSign bool
}

// CommitTree creates a commit from a given tree id for the user with provided message
func (repo *Repository) CommitTree(author, committer *Signature, tree *Tree, opts CommitTreeOpts) (ObjectID, error) {
	commitTimeStr := time.Now().Format(time.RFC3339)

	// Because this may call hooks we should pass in the environment
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME="+author.Name,
		"GIT_AUTHOR_EMAIL="+author.Email,
		"GIT_AUTHOR_DATE="+commitTimeStr,
		"GIT_COMMITTER_NAME="+committer.Name,
		"GIT_COMMITTER_EMAIL="+committer.Email,
		"GIT_COMMITTER_DATE="+commitTimeStr,
	)
	cmd := NewCommand(repo.Ctx, "commit-tree").AddDynamicArguments(tree.ID.String())

	for _, parent := range opts.Parents {
		cmd.AddArguments("-p").AddDynamicArguments(parent)
	}

	messageBytes := new(bytes.Buffer)
	_, _ = messageBytes.WriteString(opts.Message)
	_, _ = messageBytes.WriteString("\n")

	if opts.KeyID != "" || opts.AlwaysSign {
		cmd.AddOptionFormat("-S%s", opts.KeyID)
	}

	if opts.NoGPGSign {
		cmd.AddArguments("--no-gpg-sign")
	}

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	err := cmd.Run(&RunOpts{
		Env:    env,
		Dir:    repo.Path,
		Stdin:  messageBytes,
		Stdout: stdout,
		Stderr: stderr,
	})
	if err != nil {
		return nil, ConcatenateError(err, stderr.String())
	}
	return NewIDFromString(strings.TrimSpace(stdout.String()))
}

func (repo *Repository) getTree(id ObjectID) (*Tree, error) {
	var tree *Tree
	if err := repo.WithCatFileBatch(repo.Ctx, func(wr io.Writer, rd *bufio.Reader) error {
		if _, err := wr.Write([]byte(id.String() + "\n")); err != nil {
			return err
		}

		// ignore the SHA
		_, typ, size, err := ReadBatchLine(rd)
		if err != nil {
			return err
		}

		switch typ {
		case "tag":
			resolvedID := id
			data, err := io.ReadAll(io.LimitReader(rd, size))
			if err != nil {
				return err
			}
			tag, err := parseTagData(id.Type(), data)
			if err != nil {
				return err
			}
			commit, err := tag.Commit(repo)
			if err != nil {
				return err
			}
			commit.ResolvedID = resolvedID
			tree = &commit.Tree
			return nil
		case "commit":
			commit, err := CommitFromReader(repo, id, io.LimitReader(rd, size))
			if err != nil {
				return err
			}
			if _, err := rd.Discard(1); err != nil {
				return err
			}
			commit.ResolvedID = commit.ID
			tree = &commit.Tree
			return nil
		case "tree":
			tree = NewTree(repo, id)
			tree.ResolvedID = id
			objectFormat, err := repo.GetObjectFormat()
			if err != nil {
				return err
			}
			tree.entries, err = catBatchParseTreeEntries(objectFormat, tree, rd, size)
			if err != nil {
				return err
			}
			tree.entriesParsed = true
			return nil
		default:
			if err := DiscardFull(rd, size+1); err != nil {
				return err
			}
			return ErrNotExist{
				ID: id.String(),
			}
		}
	}); err != nil {
		return nil, err
	}
	return tree, nil
}

// GetTree find the tree object in the repository.
func (repo *Repository) GetTree(idStr string) (*Tree, error) {
	objectFormat, err := repo.GetObjectFormat()
	if err != nil {
		return nil, err
	}
	if !objectFormat.IsValid(idStr) {
		res, err := repo.GetRefCommitID(idStr)
		if err != nil {
			return nil, err
		}
		if len(res) > 0 {
			idStr = res
		}
	}
	id, err := NewIDFromString(idStr)
	if err != nil {
		return nil, err
	}

	return repo.getTree(id)
}
