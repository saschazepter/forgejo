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
	"strings"
	"sync/atomic"

	"forgejo.org/modules/optional"
)

var LinguistAttributes = []string{"linguist-vendored", "linguist-generated", "linguist-language", "gitlab-language", "linguist-documentation", "linguist-detectable"}

// newCheckAttrStdoutReader parses the nul-byte separated output of git check-attr on each call of
// the returned function. The first reading error will stop the reading and be returned on all
// subsequent calls.
func newCheckAttrStdoutReader(r io.Reader, count int) func() (map[string]GitAttribute, error) {
	scanner := bufio.NewScanner(r)

	// adapted from bufio.ScanLines to split on nul-byte \x00
	scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := bytes.IndexByte(data, '\x00'); i >= 0 {
			// We have a full nul-terminated line.
			return i + 1, data[0:i], nil
		}
		// If we're at EOF, we have a final, non-terminated line. Return it.
		if atEOF {
			return len(data), data, nil
		}
		// Request more data.
		return 0, nil, nil
	})

	var err error
	nextText := func() string {
		if err != nil {
			return ""
		}
		if !scanner.Scan() {
			err = scanner.Err()
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			return ""
		}
		return scanner.Text()
	}
	nextAttribute := func() (string, GitAttribute, error) {
		nextText() // discard filename
		key := nextText()
		value := GitAttribute(nextText())
		return key, value, err
	}
	return func() (map[string]GitAttribute, error) {
		values := make(map[string]GitAttribute, count)
		for range count {
			k, v, err := nextAttribute()
			if err != nil {
				return values, err
			}
			values[k] = v
		}
		return values, scanner.Err()
	}
}

// GitAttribute exposes an attribute from the .gitattribute file
type GitAttribute string //nolint:revive

// IsSpecified returns true if the gitattribute is set and not empty
func (ca GitAttribute) IsSpecified() bool {
	return ca != "" && ca != "unspecified"
}

// String returns the value of the attribute or "" if unspecified
func (ca GitAttribute) String() string {
	if !ca.IsSpecified() {
		return ""
	}
	return string(ca)
}

// Prefix returns the value of the attribute before any question mark '?'
//
// sometimes used within gitlab-language: https://docs.gitlab.com/ee/user/project/highlighting.html#override-syntax-highlighting-for-a-file-type
func (ca GitAttribute) Prefix() string {
	s := ca.String()
	if i := strings.IndexByte(s, '?'); i >= 0 {
		return s[:i]
	}
	return s
}

// Bool returns true if "set"/"true", false if "unset"/"false", none otherwise
func (ca GitAttribute) Bool() optional.Option[bool] {
	switch ca {
	case "set", "true":
		return optional.Some(true)
	case "unset", "false":
		return optional.Some(false)
	}
	return optional.None[bool]()
}

// gitCheckAttrCommand prepares the "git check-attr" command for later use as one-shot or streaming
// instantiation.
func (repo *Repository) gitCheckAttrCommand(treeish string, attributes ...string) (*Command, *RunOpts, context.CancelFunc, error) {
	if len(attributes) == 0 {
		return nil, nil, nil, errors.New("no provided attributes to check-attr")
	}

	env := os.Environ()
	var removeTempFiles context.CancelFunc = func() {}

	// git < 2.40 cannot run check-attr on bare repo, but needs INDEX + WORK_TREE
	hasIndex := treeish == ""
	if !hasIndex && !SupportCheckAttrOnBare {
		indexFilename, worktree, cancel, err := repo.ReadTreeToTemporaryIndex(treeish)
		if err != nil {
			return nil, nil, nil, err
		}
		removeTempFiles = cancel

		env = append(env, "GIT_INDEX_FILE="+indexFilename, "GIT_WORK_TREE="+worktree)

		hasIndex = true

		// clear treeish to read from provided index/work_tree
		treeish = ""
	}

	cmd := NewCommand(repo.Ctx, "check-attr", "-z")

	if hasIndex {
		cmd.AddArguments("--cached")
	}

	if len(treeish) > 0 {
		cmd.AddArguments("--source")
		cmd.AddDynamicArguments(treeish)
	}
	cmd.AddDynamicArguments(attributes...)

	// Version 2.43.1 has a bug where the behavior of `GIT_FLUSH` is flipped.
	// Ref: https://lore.kernel.org/git/CABn0oJvg3M_kBW-u=j3QhKnO=6QOzk-YFTgonYw_UvFS1NTX4g@mail.gmail.com
	if InvertedGitFlushEnv {
		env = append(env, "GIT_FLUSH=0")
	} else {
		env = append(env, "GIT_FLUSH=1")
	}

	return cmd, &RunOpts{
		Env: env,
		Dir: repo.Path,
	}, removeTempFiles, nil
}

// GitAttributeFirst returns the first specified attribute of the given filename.
//
// If treeish is empty, the gitattribute will be read from the current repo (which MUST be a working directory and NOT bare).
func (repo *Repository) GitAttributeFirst(treeish, filename string, attributes ...string) (GitAttribute, error) {
	values, err := repo.GitAttributes(treeish, filename, attributes...)
	if err != nil {
		return "", err
	}
	for _, a := range attributes {
		if values[a].IsSpecified() {
			return values[a], nil
		}
	}
	return "", nil
}

// GitAttributes returns the gitattribute of the given filename.
//
// If treeish is empty, the gitattribute will be read from the current repo (which MUST be a working directory and NOT bare).
func (repo *Repository) GitAttributes(treeish, filename string, attributes ...string) (map[string]GitAttribute, error) {
	cmd, runOpts, removeTempFiles, err := repo.gitCheckAttrCommand(treeish, attributes...)
	if err != nil {
		return nil, err
	}
	defer removeTempFiles()

	stdOut := new(bytes.Buffer)
	runOpts.Stdout = stdOut

	stdErr := new(bytes.Buffer)
	runOpts.Stderr = stdErr

	cmd.AddDashesAndList(filename)

	if err := cmd.Run(runOpts); err != nil {
		return nil, fmt.Errorf("failed to run check-attr: %w\n%s\n%s", err, stdOut.String(), stdErr.String())
	}

	return newCheckAttrStdoutReader(stdOut, len(attributes))()
}

// GitAttributeChecker creates an AttributeChecker for the given repository and provided commit ID
// to retrieve the attributes of multiple files. The AttributeChecker must be closed after use.
//
// If treeish is empty, the gitattribute will be read from the current repo (which MUST be a working directory and NOT bare).
func (repo *Repository) GitAttributeChecker(treeish string, attributes ...string) (AttributeChecker, error) {
	cmd, runOpts, removeTempFiles, err := repo.gitCheckAttrCommand(treeish, attributes...)
	if err != nil {
		return AttributeChecker{}, err
	}

	cmd.AddArguments("--stdin")

	// os.Pipe is needed (and not io.Pipe), otherwise cmd.Wait will wait for the stdinReader
	// to be closed before returning (which would require another goroutine)
	// https://go.dev/issue/23019
	stdinReader, stdinWriter, err := os.Pipe() // reader closed in goroutine / writer closed on ac.Close
	if err != nil {
		return AttributeChecker{}, err
	}
	stdoutReader, stdoutWriter := io.Pipe() // closed in goroutine

	ac := AttributeChecker{
		removeTempFiles: removeTempFiles, // called on ac.Close
		stdinWriter:     stdinWriter,
		readStdout:      newCheckAttrStdoutReader(stdoutReader, len(attributes)),
		err:             &atomic.Value{},
	}

	go func() {
		defer stdinReader.Close()
		defer stdoutWriter.Close() // in case of a panic (no-op if already closed by CloseWithError at the end)

		stdErr := new(bytes.Buffer)
		runOpts.Stdin = stdinReader
		runOpts.Stdout = stdoutWriter
		runOpts.Stderr = stdErr

		err := cmd.Run(runOpts)

		// if the context was cancelled, Run error is irrelevant
		if e := cmd.parentContext.Err(); e != nil {
			err = e
		}

		if err != nil && !IsErrCanceledOrKilled(err) { // decorate the returned error
			err = fmt.Errorf("git check-attr (stderr: %q): %w", strings.TrimSpace(stdErr.String()), err)
			ac.err.Store(err)
		}
		stdoutWriter.CloseWithError(err)
	}()

	return ac, nil
}

type AttributeChecker struct {
	removeTempFiles context.CancelFunc
	stdinWriter     io.WriteCloser
	readStdout      func() (map[string]GitAttribute, error)
	err             *atomic.Value
}

func (ac AttributeChecker) CheckPath(path string) (map[string]GitAttribute, error) {
	if _, err := ac.stdinWriter.Write([]byte(path + "\x00")); err != nil {
		// try to return the Run error if available, since it is likely more helpful
		// than just "broken pipe"
		if aerr, _ := ac.err.Load().(error); aerr != nil {
			return nil, aerr
		}
		return nil, fmt.Errorf("git check-attr: %w", err)
	}

	return ac.readStdout()
}

func (ac AttributeChecker) Close() error {
	ac.removeTempFiles()
	return ac.stdinWriter.Close()
}
