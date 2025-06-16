// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"net/http"
	"strings"
	"testing"

	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
)

func TestIssueCommentChangeMilestone(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	req := NewRequest(t, "GET", "/user2/repo1/issues/1")
	resp := MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	event := htmlDoc.Find("#issuecomment-2000 .text")
	links := event.Find("a")
	assert.Contains(t, event.Text(), "added this to the milestone1 milestone")
	assert.Equal(t, "/user1", links.Eq(0).AttrOr("href", ""))
	assert.Equal(t, "/user2/repo1/milestones/1", links.Eq(1).AttrOr("href", ""))

	event = htmlDoc.Find("#issuecomment-2001 .text")
	links = event.Find("a")
	assert.Contains(t, event.Text(), "modified the milestone from milestone1 to milestone2")
	assert.Equal(t, "/user1", links.Eq(0).AttrOr("href", ""))
	assert.Equal(t, "/user2/repo1/milestones/1", links.Eq(1).AttrOr("href", ""))
	assert.Equal(t, "/user2/repo1/milestones/2", links.Eq(2).AttrOr("href", ""))

	event = htmlDoc.Find("#issuecomment-2002 .text")
	links = event.Find("a")
	assert.Contains(t, event.Text(), "removed this from the milestone2 milestone")
	assert.Equal(t, "/user1", links.Eq(0).AttrOr("href", ""), "/user1")
	assert.Equal(t, "/user2/repo1/milestones/2", links.Eq(1).AttrOr("href", ""))

	event = htmlDoc.Find("#issuecomment-2003 .text")
	links = event.Find("a")
	assert.Contains(t, event.Text(), "added this to the (deleted) milestone")
	assert.Equal(t, "/user1", links.Eq(0).AttrOr("href", ""))
}

func TestIssueCommentChangeProject(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	req := NewRequest(t, "GET", "/user2/repo1/issues/1")
	resp := MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	event := htmlDoc.Find("#issuecomment-2010 .text")
	links := event.Find("a")
	assert.Contains(t, event.Text(), "added this to the First project project")
	assert.Equal(t, "/user1", links.Eq(0).AttrOr("href", ""))
	assert.Equal(t, "/user2/repo1/projects/1", links.Eq(1).AttrOr("href", ""))

	event = htmlDoc.Find("#issuecomment-2011 .text")
	links = event.Find("a")
	assert.Contains(t, event.Text(), "modified the project from First project to second project")
	assert.Equal(t, "/user1", links.Eq(0).AttrOr("href", ""), "/user1")
	assert.Equal(t, "/user2/repo1/projects/1", links.Eq(1).AttrOr("href", ""))
	assert.Equal(t, "/user2/repo1/projects/2", links.Eq(2).AttrOr("href", ""))

	event = htmlDoc.Find("#issuecomment-2012 .text")
	links = event.Find("a")
	assert.Contains(t, event.Text(), "removed this from the second project project")
	assert.Equal(t, "/user1", links.Eq(0).AttrOr("href", ""))
	assert.Equal(t, "/user2/repo1/projects/2", links.Eq(1).AttrOr("href", ""))

	event = htmlDoc.Find("#issuecomment-2013 .text")
	links = event.Find("a")
	assert.Contains(t, event.Text(), "added this to the (deleted) project")
	assert.Equal(t, "/user1", links.Eq(0).AttrOr("href", ""))
}

func TestIssueCommentChangeLabel(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	req := NewRequest(t, "GET", "/user2/repo1/issues/1")
	resp := MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	// Add multiple labels
	event := htmlDoc.Find("#issuecomment-2020 .text")
	links := event.Find("a")
	assert.Contains(t, strings.Join(strings.Fields(event.Text()), " "), "user1 added the label1 label2 labels ")
	assert.Equal(t, "/user1", links.Eq(0).AttrOr("href", ""))
	assert.Equal(t, "/user2/repo1/issues?labels=1", links.Eq(1).AttrOr("href", ""))
	assert.Equal(t, "/user2/repo1/issues?labels=2", links.Eq(2).AttrOr("href", ""))
	assert.Empty(t, htmlDoc.Find("#issuecomment-2021 .text").Text())

	// Remove single label
	event = htmlDoc.Find("#issuecomment-2022 .text")
	links = event.Find("a")
	assert.Contains(t, strings.Join(strings.Fields(event.Text()), " "), "user2 removed the label1 label ")
	assert.Equal(t, "/user2", links.Eq(0).AttrOr("href", ""))
	assert.Equal(t, "/user2/repo1/issues?labels=1", links.Eq(1).AttrOr("href", ""))

	// Modify labels (add and remove)
	event = htmlDoc.Find("#issuecomment-2023 .text")
	links = event.Find("a")
	assert.Contains(t, strings.Join(strings.Fields(event.Text()), " "), "user1 added label1 and removed label2 labels ")
	assert.Equal(t, "/user1", links.Eq(0).AttrOr("href", ""))
	assert.Equal(t, "/user2/repo1/issues?labels=1", links.Eq(1).AttrOr("href", ""))
	assert.Equal(t, "/user2/repo1/issues?labels=2", links.Eq(2).AttrOr("href", ""))
	assert.Empty(t, htmlDoc.Find("#issuecomment-2024 .text").Text())

	// Add single label
	event = htmlDoc.Find("#issuecomment-2025 .text")
	links = event.Find("a")
	assert.Contains(t, strings.Join(strings.Fields(event.Text()), " "), "user2 added the label2 label ")
	assert.Equal(t, "/user2", links.Eq(0).AttrOr("href", ""))
	assert.Equal(t, "/user2/repo1/issues?labels=2", links.Eq(1).AttrOr("href", ""))

	// Remove multiple labels
	event = htmlDoc.Find("#issuecomment-2026 .text")
	links = event.Find("a")
	assert.Contains(t, strings.Join(strings.Fields(event.Text()), " "), "user1 removed the label1 label2 labels ")
	assert.Equal(t, "/user1", links.Eq(0).AttrOr("href", ""))
	assert.Equal(t, "/user2/repo1/issues?labels=1", links.Eq(1).AttrOr("href", ""))
	assert.Equal(t, "/user2/repo1/issues?labels=2", links.Eq(2).AttrOr("href", ""))
	assert.Empty(t, htmlDoc.Find("#issuecomment-2027 .text").Text())
}

func TestIssueCommentChangeAssignee(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	req := NewRequest(t, "GET", "/user2/repo1/issues/1")
	resp := MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	// self-assign
	event := htmlDoc.Find("#issuecomment-2040 .text")
	links := event.Find("a")
	assert.Contains(t, strings.Join(strings.Fields(event.Text()), " "), "user1 self-assigned this")
	assert.Equal(t, "/user1", links.Eq(0).AttrOr("href", ""))

	// remove other
	event = htmlDoc.Find("#issuecomment-2041 .text")
	links = event.Find("a")
	assert.Contains(t, strings.Join(strings.Fields(event.Text()), " "), "user1 was unassigned by user2")
	assert.Equal(t, "/user1", links.Eq(0).AttrOr("href", ""))
	// assert.Equal(t, "/user2", links.Eq(1).AttrOr("href", ""))

	// add other
	event = htmlDoc.Find("#issuecomment-2042 .text")
	links = event.Find("a")
	assert.Contains(t, strings.Join(strings.Fields(event.Text()), " "), "user2 was assigned by user1")
	assert.Equal(t, "/user2", links.Eq(0).AttrOr("href", ""))
	// assert.Equal(t, "/user1", links.Eq(1).AttrOr("href", ""))

	// self-remove
	event = htmlDoc.Find("#issuecomment-2043 .text")
	links = event.Find("a")
	assert.Contains(t, strings.Join(strings.Fields(event.Text()), " "), "user2 removed their assignment")
	assert.Equal(t, "/user2", links.Eq(0).AttrOr("href", ""))
}
