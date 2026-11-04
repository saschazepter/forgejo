// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package structs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMovedIssuesOptionGetIssueIDs(t *testing.T) {
	m := &MoveIssuesOption{
		ProjectIssues: []MoveProjectIssue{
			{1, 1},
			{2, 2},
			{3, 3},
		},
	}

	ids := m.GetIssueIDs()
	assert.Len(t, m.ProjectIssues, len(ids))
	for _, v := range m.ProjectIssues {
		assert.Contains(t, ids, v.IssueID)
	}
}

func TestMovedIssuesOptionGetSortingMap(t *testing.T) {
	m := &MoveIssuesOption{
		ProjectIssues: []MoveProjectIssue{
			{1, 1},
			{2, 2},
			{3, 3},
		},
	}

	sortings := m.GetSortingsMap()
	assert.Len(t, m.ProjectIssues, len(sortings))
	for _, v := range m.ProjectIssues {
		assert.Contains(t, sortings, v.Sorting)
		assert.Equal(t, v.IssueID, sortings[v.Sorting])
	}
}
