// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package internal

import (
	"io"
	"strconv"
	"strings"
)

type BoolOpt int

const (
	BoolOptMust BoolOpt = iota
	BoolOptShould
	BoolOptNot
)

type Token struct {
	Term  string
	Kind  BoolOpt
	Fuzzy bool
}

func (tk *Token) ParseIssueReference() (int64, error) {
	term := tk.Term
	if term[0] == '#' || term[0] == '!' {
		term = term[1:]
	}
	return strconv.ParseInt(term, 10, 64)
}

type Tokenizer struct {
	in *strings.Reader
}

func (t *Tokenizer) next() (tk Token, err error) {
	var (
		sb strings.Builder
		r  rune
	)
	tk.Kind = BoolOptShould
	tk.Fuzzy = true

	// skip all leading white space
	for {
		if r, _, err = t.in.ReadRune(); err == nil && r == ' ' {
			//nolint:staticcheck,wastedassign // SA4006 the variable is used after the loop
			r, _, err = t.in.ReadRune()
			continue
		}
		break
	}
	if err != nil {
		return tk, err
	}

	// check for +/- op, increment to the next rune in both cases
	switch r {
	case '+':
		tk.Kind = BoolOptMust
		r, _, err = t.in.ReadRune()
	case '-':
		tk.Kind = BoolOptNot
		r, _, err = t.in.ReadRune()
	}
	if err != nil {
		return tk, err
	}

	// parse the string, escaping special characters
	for esc := false; err == nil; r, _, err = t.in.ReadRune() {
		if esc {
			if !strings.ContainsRune("+-\\\"", r) {
				sb.WriteRune('\\')
			}
			sb.WriteRune(r)
			esc = false
			continue
		}
		switch r {
		case '\\':
			esc = true
		case '"':
			if !tk.Fuzzy {
				goto nextEnd
			}
			tk.Fuzzy = false
		case ' ', '\t':
			if tk.Fuzzy {
				goto nextEnd
			}
			sb.WriteRune(r)
		default:
			sb.WriteRune(r)
		}
	}
nextEnd:

	tk.Term = sb.String()
	if err == io.EOF {
		err = nil
	} // do not consider EOF as an error at the end
	return tk, err
}

// Tokenize the keyword
func (o *SearchOptions) Tokens() (tokens []Token, err error) {
	in := strings.NewReader(o.Keyword)
	it := Tokenizer{in: in}

	for token, err := it.next(); err == nil; token, err = it.next() {
		tokens = append(tokens, token)
	}
	if err != nil && err != io.EOF {
		return nil, err
	}

	return tokens, nil
}
