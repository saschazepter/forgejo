// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"fmt"
	"io"
	"strconv"

	"forgejo.org/modules/optional"

	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
)

type RangeDiffCommitStatus string

const (
	CommitEqual   RangeDiffCommitStatus = "="
	CommitChanged RangeDiffCommitStatus = "!"
	CommitAdded   RangeDiffCommitStatus = ">"
	CommitRemoved RangeDiffCommitStatus = "<"
)

// RangeDiffCommit identifies a single commit in the range-diff output.
type RangeDiffCommit struct {
	Index int    // index of the commit in the commit set, starting at 1
	OID   string // commit ID (may be abbreviated if `--abbref=64` was not provided to range-diff command)
}

// RangeDiffCommitPair represents range-diff outputing two commits have been compared with each other.  Note that when
// Status=(CommitEqual|CommitChanged), the commits may have different indexes indicating that they differ by their order
// in the two commit sets.
type RangeDiffCommitPair struct {
	Old     optional.Option[RangeDiffCommit] // Absent when Status = RangeDiffCommitAdded
	New     optional.Option[RangeDiffCommit] // Absent when Status = RangeDiffCommitRemoved
	Status  RangeDiffCommitStatus
	Subject string
}

// Internal participle structure for parsing the entire range-diff output
type rangeDiffDocument struct {
	Pairs []*rangeDiffPairNode `parser:"@@*"`
}

// Internal participle structure for parsing a single line of the range-diff output
type rangeDiffPairNode struct {
	Pos       lexer.Position
	OldIndex  string `parser:"Space* @(Number | '-') ':' Space+"`
	OldCommit string `parser:"@((Number | Hex)+ | '-'+) Space+"`
	Status    string `parser:"@('=' | '!' | '<' | '>') Space+"`
	NewIndex  string `parser:"Space* @(Number | '-') ':' Space+"`
	NewCommit string `parser:"@((Number | Hex)+ | '-'+) Space+"`
	Subject   string `parser:"@(~Newline)* (Newline | EOF)"`
}

var rangeDiffLexer = lexer.MustSimple([]lexer.SimpleRule{
	{Name: "Space", Pattern: ` `},
	{Name: "Newline", Pattern: `\r?\n`},
	{Name: "Number", Pattern: `[0-9]+`},
	{Name: "Hex", Pattern: `[a-fA-F]+`},
	{Name: "Marker", Pattern: `[:=!<>@+\-]`},
	{Name: "Text", Pattern: `[^ \r\n\x1b:=!<>@+\-]+`},
})
var rangeDiffParser = participle.MustBuild[rangeDiffDocument](participle.Lexer(rangeDiffLexer))

// ParseRangeDiff accepts standalone git range-diff output which must be provided with `--no-patch` (listing commits
// only), and `--no-color` (no ANSI output).  Recommended to provide `--abbrev=64` as well to get full commit ID length
// (for both sha1 & sha256 repos).
func ParseRangeDiff(input io.Reader) ([]RangeDiffCommitPair, error) {
	// Parse the input:
	parsed, err := rangeDiffParser.Parse("", input)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	// Parser cannot handle `optional.Option[N]`, and puts both "-" and numerics in the indexes, so we do a post-process
	// to reformat the optional data:
	pairs := make([]RangeDiffCommitPair, len(parsed.Pairs))
	for i, p := range parsed.Pairs {
		pairs[i].Status = RangeDiffCommitStatus(p.Status)
		pairs[i].Subject = p.Subject
		if p.OldIndex != "-" {
			index, err := strconv.Atoi(p.OldIndex)
			if err != nil {
				return nil, fmt.Errorf("unable to convert %q into a numeric value: %w", p.OldIndex, err)
			}
			pairs[i].Old = optional.Some[RangeDiffCommit](RangeDiffCommit{
				Index: index,
				OID:   p.OldCommit,
			})
		}
		if p.NewIndex != "-" {
			index, err := strconv.Atoi(p.NewIndex)
			if err != nil {
				return nil, fmt.Errorf("unable to convert %q into a numeric value: %w", p.NewIndex, err)
			}
			pairs[i].New = optional.Some[RangeDiffCommit](RangeDiffCommit{
				Index: index,
				OID:   p.NewCommit,
			})
		}
	}

	return pairs, nil
}
