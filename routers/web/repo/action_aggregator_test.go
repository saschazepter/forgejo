// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"strings"
	"testing"

	issue_model "forgejo.org/models/issues"
	"forgejo.org/models/organization"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/timeutil"

	"github.com/stretchr/testify/assert"
)

// *************** Helper functions for the tests ***************

func testComment(t int64) *issue_model.Comment {
	return &issue_model.Comment{PosterID: 1, CreatedUnix: timeutil.TimeStamp(t)}
}

func nameToID(name string) int64 {
	var id int64
	for c, letter := range name {
		id += int64((c+1)*1000) * int64(letter)
	}
	return id
}

func createReqReviewTarget(name string) issue_model.RequestReviewTarget {
	if strings.HasSuffix(name, "-team") {
		team := createTeam(name)
		return issue_model.RequestReviewTarget{Team: &team}
	}
	user := createUser(name)
	return issue_model.RequestReviewTarget{User: &user}
}

func createUser(name string) user_model.User {
	return user_model.User{Name: name, ID: nameToID(name)}
}

func createTeam(name string) organization.Team {
	return organization.Team{Name: name, ID: nameToID(name)}
}

func createLabel(name string) issue_model.Label {
	return issue_model.Label{Name: name, ID: nameToID(name)}
}

func addLabel(t int64, name string) *issue_model.Comment {
	c := testComment(t)
	c.Type = issue_model.CommentTypeLabel
	c.Content = "1"
	lbl := createLabel(name)
	c.Label = &lbl
	c.AddedLabels = []*issue_model.Label{&lbl}
	return c
}

func delLabel(t int64, name string) *issue_model.Comment {
	c := addLabel(t, name)
	c.Content = ""
	c.RemovedLabels = c.AddedLabels
	c.AddedLabels = nil
	return c
}

func openOrClose(t int64, close bool) *issue_model.Comment {
	c := testComment(t)
	if close {
		c.Type = issue_model.CommentTypeClose
	} else {
		c.Type = issue_model.CommentTypeReopen
	}
	return c
}

func reqReview(t int64, name string, delReq bool) *issue_model.Comment {
	c := testComment(t)
	c.Type = issue_model.CommentTypeReviewRequest
	if strings.HasSuffix(name, "-team") {
		team := createTeam(name)
		c.AssigneeTeam = &team
		c.AssigneeTeamID = team.ID
	} else {
		user := createUser(name)
		c.Assignee = &user
		c.AssigneeID = user.ID
	}
	c.RemovedAssignee = delReq
	return c
}

func ghostReqReview(t, id int64) *issue_model.Comment {
	c := testComment(t)
	c.Type = issue_model.CommentTypeReviewRequest
	c.AssigneeTeam = organization.NewGhostTeam()
	c.AssigneeTeamID = id
	return c
}

func reqReviewList(t int64, del bool, names ...string) *issue_model.Comment {
	req := []issue_model.RequestReviewTarget{}
	for _, name := range names {
		req = append(req, createReqReviewTarget(name))
	}
	cmnt := testComment(t)
	cmnt.Type = issue_model.CommentTypeReviewRequest
	if del {
		cmnt.RemovedRequestReview = req
	} else {
		cmnt.AddedRequestReview = req
	}
	return cmnt
}

func aggregatedComment(t int64,
	closed bool,
	addLabels []*issue_model.Label,
	delLabels []*issue_model.Label,
	addReqReview []issue_model.RequestReviewTarget,
	delReqReview []issue_model.RequestReviewTarget,
) *issue_model.Comment {
	cmnt := testComment(t)
	cmnt.Type = issue_model.CommentTypeAggregator
	cmnt.Aggregator = &issue_model.ActionAggregator{
		IsClosed:             closed,
		AddedLabels:          addLabels,
		RemovedLabels:        delLabels,
		AddedRequestReview:   addReqReview,
		RemovedRequestReview: delReqReview,
	}
	if len(addLabels) > 0 {
		cmnt.AddedLabels = addLabels
	}
	if len(delLabels) > 0 {
		cmnt.RemovedLabels = delLabels
	}
	if len(addReqReview) > 0 {
		cmnt.AddedRequestReview = addReqReview
	}
	if len(delReqReview) > 0 {
		cmnt.RemovedRequestReview = delReqReview
	}
	return cmnt
}

func commentText(t int64, text string) *issue_model.Comment {
	c := testComment(t)
	c.Type = issue_model.CommentTypeComment
	c.Content = text
	return c
}

// ****************************************************************

type testCase struct {
	name                 string
	beforeCombined       []*issue_model.Comment
	afterCombined        []*issue_model.Comment
	sameAfter            bool
	timestampCombination int64
}

func (kase *testCase) doTest(t *testing.T) {
	issue := issue_model.Issue{Comments: kase.beforeCombined}

	var now int64 = -9223372036854775808
	for c := 0; c < len(kase.beforeCombined); c++ {
		assert.Greater(t, int64(kase.beforeCombined[c].CreatedUnix), now)
		now = int64(kase.beforeCombined[c].CreatedUnix)
	}

	if kase.timestampCombination != 0 {
		now = kase.timestampCombination
	}

	issue_model.CombineCommentsHistory(&issue, now)

	after := kase.afterCombined
	if kase.sameAfter {
		after = kase.beforeCombined
	}

	if len(after) != len(issue.Comments) {
		t.Logf("Expected %v comments, got %v", len(after), len(issue.Comments))
		t.Log("Comments got after combination:")
		for c := 0; c < len(issue.Comments); c++ {
			cmt := issue.Comments[c]
			t.Logf("%v %v %v\n", cmt.Type, cmt.CreatedUnix, cmt.Content)
		}
		assert.Len(t, issue.Comments, len(after))
		t.Fail()
		return
	}

	for c := 0; c < len(after); c++ {
		l := (after)[c]
		r := issue.Comments[c]

		// Ignore some inner data of the aggregator to facilitate testing
		if l.Type == issue_model.CommentTypeAggregator {
			r.Aggregator.StartUnix = 0
			r.Aggregator.PrevClosed = false
			r.Aggregator.PosterID = 0
			r.Aggregator.StartInd = 0
			r.Aggregator.EndInd = 0
			r.Aggregator.AggAge = 0
		}

		// We can safely ignore this if the rest matches
		if l.Type == issue_model.CommentTypeLabel {
			l.Label = nil
			l.Content = ""
		} else if l.Type == issue_model.CommentTypeReviewRequest {
			l.Assignee = nil
			l.AssigneeID = 0
			l.AssigneeTeam = nil
			l.AssigneeTeamID = 0
		}

		assert.Equal(t, (after)[c], issue.Comments[c],
			"Comment %v is not equal", c,
		)
	}
}

// **************** Start of the tests ******************

func TestCombineLabelComments(t *testing.T) {
	var tmon int64 = 60 * 60 * 24 * 30
	var tday int64 = 60 * 60 * 24
	var thour int64 = 60 * 60
	kases := []testCase{
		// ADD single = normal label comment
		{
			name: "add_single_label",
			beforeCombined: []*issue_model.Comment{
				addLabel(0, "a"),
				commentText(10, "I'm a salmon"),
			},
			sameAfter: true,
		},

		// ADD then REMOVE = Nothing
		{
			name: "add_label_then_remove",
			beforeCombined: []*issue_model.Comment{
				addLabel(0, "a"),
				delLabel(1, "a"),
				commentText(65, "I'm a salmon"),
			},
			afterCombined: []*issue_model.Comment{
				commentText(65, "I'm a salmon"),
			},
		},

		// ADD 1 then comment then REMOVE = separate comments
		{
			name: "add_label_then_comment_then_remove",
			beforeCombined: []*issue_model.Comment{
				addLabel(0, "a"),
				commentText(10, "I'm a salmon"),
				delLabel(20, "a"),
			},
			sameAfter: true,
		},

		// ADD 2 = Combined labels
		{
			name: "combine_labels",
			beforeCombined: []*issue_model.Comment{
				addLabel(0, "a"),
				addLabel(10, "b"),
				commentText(20, "I'm a salmon"),
				addLabel(30, "c"),
				addLabel(80, "d"),
				addLabel(85, "e"),
				delLabel(90, "c"),
			},
			afterCombined: []*issue_model.Comment{
				{
					PosterID:    1,
					Type:        issue_model.CommentTypeLabel,
					CreatedUnix: timeutil.TimeStamp(0),
					AddedLabels: []*issue_model.Label{
						{Name: "a", ID: nameToID("a")},
						{Name: "b", ID: nameToID("b")},
					},
				},
				commentText(20, "I'm a salmon"),
				{
					PosterID:    1,
					Type:        issue_model.CommentTypeLabel,
					CreatedUnix: timeutil.TimeStamp(30),
					AddedLabels: []*issue_model.Label{
						{Name: "d", ID: nameToID("d")},
						{Name: "e", ID: nameToID("e")},
					},
				},
			},
		},

		// ADD 1, then 1 later = 2 separate comments
		{
			name: "add_then_later_label",
			beforeCombined: []*issue_model.Comment{
				addLabel(0, "a"),
				addLabel(60, "b"),
				addLabel(121, "c"),
			},
			afterCombined: []*issue_model.Comment{
				{
					PosterID:    1,
					Type:        issue_model.CommentTypeLabel,
					CreatedUnix: timeutil.TimeStamp(0),
					AddedLabels: []*issue_model.Label{
						{Name: "a", ID: nameToID("a")},
						{Name: "b", ID: nameToID("b")},
					},
				},
				addLabel(121, "c"),
			},
		},

		// ADD 2 then REMOVE 1 = label
		{
			name: "add_2_remove_1",
			beforeCombined: []*issue_model.Comment{
				addLabel(0, "a"),
				addLabel(10, "b"),
				delLabel(20, "a"),
			},
			afterCombined: []*issue_model.Comment{
				// The timestamp will be the one of the first aggregated comment
				addLabel(0, "b"),
			},
		},

		// ADD then REMOVE multiple = nothing
		{
			name: "add_multiple_remove_all",
			beforeCombined: []*issue_model.Comment{
				addLabel(0, "a"),
				addLabel(1, "b"),
				addLabel(2, "c"),
				addLabel(3, "d"),
				addLabel(4, "e"),
				delLabel(5, "d"),
				delLabel(6, "a"),
				delLabel(7, "e"),
				delLabel(8, "c"),
				delLabel(9, "b"),
			},
			afterCombined: nil,
		},

		// ADD 2, wait, REMOVE 2 = +2 then -2 comments
		{
			name: "add2_wait_rm2_labels",
			beforeCombined: []*issue_model.Comment{
				addLabel(0, "a"),
				addLabel(1, "b"),
				delLabel(120, "a"),
				delLabel(121, "b"),
			},
			afterCombined: []*issue_model.Comment{
				{
					PosterID:    1,
					Type:        issue_model.CommentTypeLabel,
					CreatedUnix: timeutil.TimeStamp(0),
					AddedLabels: []*issue_model.Label{
						{Name: "a", ID: nameToID("a")},
						{Name: "b", ID: nameToID("b")},
					},
				},
				{
					PosterID:    1,
					Type:        issue_model.CommentTypeLabel,
					CreatedUnix: timeutil.TimeStamp(120),
					RemovedLabels: []*issue_model.Label{
						{Name: "a", ID: nameToID("a")},
						{Name: "b", ID: nameToID("b")},
					},
				},
			},
		},

		// Regression check on edge case
		{
			name: "regression_edgecase_finalagg",
			beforeCombined: []*issue_model.Comment{
				commentText(0, "hey"),
				commentText(1, "ho"),
				addLabel(2, "a"),
				addLabel(3, "b"),
				delLabel(4, "a"),
				delLabel(5, "b"),

				addLabel(120, "a"),

				addLabel(220, "c"),
				addLabel(221, "d"),
				addLabel(222, "e"),
				delLabel(223, "d"),

				delLabel(400, "a"),
			},
			afterCombined: []*issue_model.Comment{
				commentText(0, "hey"),
				commentText(1, "ho"),
				addLabel(120, "a"),
				{
					PosterID:    1,
					Type:        issue_model.CommentTypeLabel,
					CreatedUnix: timeutil.TimeStamp(220),
					AddedLabels: []*issue_model.Label{
						{Name: "c", ID: nameToID("c")},
						{Name: "e", ID: nameToID("e")},
					},
				},
				delLabel(400, "a"),
			},
		},

		{
			name:                 "combine_label_high_timestamp_separated",
			timestampCombination: tmon + 1,
			beforeCombined: []*issue_model.Comment{
				// 1 month old, comments separated by 1 Day + 1 sec (not agg)
				addLabel(0, "d"),
				delLabel(tday+1, "d"),

				// 1 day old, comments separated by 1 hour + 1 sec (not agg)
				addLabel((tmon-tday)-thour, "c"),
				delLabel((tmon-tday)+1, "c"),

				// 1 hour old, comments separated by 10 mins + 1 sec (not agg)
				addLabel(tmon-thour, "b"),
				delLabel((tmon-(50*60))+1, "b"),

				// Else, aggregate by minute
				addLabel(tmon-61, "a"),
				delLabel(tmon, "a"),
			},
			sameAfter: true,
		},

		// Test higher timestamp diff
		{
			name:                 "combine_label_high_timestamp_merged",
			timestampCombination: tmon + 1,
			beforeCombined: []*issue_model.Comment{
				// 1 month old, comments separated by 1 Day (aggregated)
				addLabel(0, "d"),
				delLabel(tday, "d"),

				// 1 day old, comments separated by 1 hour (aggregated)
				addLabel((tmon-tday)-thour, "c"),
				delLabel(tmon-tday, "c"),

				// 1 hour old, comments separated by 10 mins (aggregated)
				addLabel(tmon-thour, "b"),
				delLabel(tmon-(50*60), "b"),

				addLabel(tmon-60, "a"),
				delLabel(tmon, "a"),
			},
		},
	}

	for _, kase := range kases {
		t.Run(kase.name, kase.doTest)
	}
}

func TestCombineReviewRequests(t *testing.T) {
	kases := []testCase{
		// ADD single = normal request review comment
		{
			name: "add_single_review",
			beforeCombined: []*issue_model.Comment{
				reqReview(0, "toto", false),
				commentText(10, "I'm a salmon"),
				reqReview(20, "toto-team", false),
			},
			sameAfter: true,
		},

		// ADD then REMOVE = Nothing
		{
			name: "add_then_remove_review",
			beforeCombined: []*issue_model.Comment{
				reqReview(0, "toto", false),
				reqReview(5, "toto", true),
				commentText(10, "I'm a salmon"),
			},
			afterCombined: []*issue_model.Comment{
				commentText(10, "I'm a salmon"),
			},
		},

		// ADD 1 then comment then REMOVE = separate comments
		{
			name: "add_comment_del_review",
			beforeCombined: []*issue_model.Comment{
				reqReview(0, "toto", false),
				commentText(5, "I'm a salmon"),
				reqReview(10, "toto", true),
			},
			sameAfter: true,
		},

		// ADD 2 = Combined request reviews
		{
			name: "combine_reviews",
			beforeCombined: []*issue_model.Comment{
				reqReview(0, "toto", false),
				reqReview(10, "tutu-team", false),
				commentText(20, "I'm a salmon"),
				reqReview(30, "titi", false),
				reqReview(80, "tata", false),
				reqReview(85, "tyty-team", false),
				reqReview(90, "titi", true),
			},
			afterCombined: []*issue_model.Comment{
				reqReviewList(0, false, "toto", "tutu-team"),
				commentText(20, "I'm a salmon"),
				reqReviewList(30, false, "tata", "tyty-team"),
			},
		},

		// ADD 1, then 1 later = 2 separate comments
		{
			name: "add_then_later_review",
			beforeCombined: []*issue_model.Comment{
				reqReview(0, "titi", false),
				reqReview(60, "toto-team", false),
				reqReview(121, "tutu", false),
			},
			afterCombined: []*issue_model.Comment{
				reqReviewList(0, false, "titi", "toto-team"),
				reqReviewList(121, false, "tutu"),
			},
		},

		// ADD 2 then REMOVE 1 = single request review
		{
			name: "add_2_then_remove_review",
			beforeCombined: []*issue_model.Comment{
				reqReview(0, "titi-team", false),
				reqReview(59, "toto", false),
				reqReview(60, "titi-team", true),
			},
			afterCombined: []*issue_model.Comment{
				reqReviewList(0, false, "toto"),
			},
		},

		// ADD then REMOVE multiple = nothing
		{
			name: "add_multiple_then_remove_all_review",
			beforeCombined: []*issue_model.Comment{
				reqReview(0, "titi0-team", false),
				reqReview(1, "toto1", false),
				reqReview(2, "titi2", false),
				reqReview(3, "titi3-team", false),
				reqReview(4, "titi4", false),
				reqReview(5, "titi5", false),
				reqReview(6, "titi6-team", false),
				reqReview(10, "titi0-team", true),
				reqReview(11, "toto1", true),
				reqReview(12, "titi2", true),
				reqReview(13, "titi3-team", true),
				reqReview(14, "titi4", true),
				reqReview(15, "titi5", true),
				reqReview(16, "titi6-team", true),
			},
			afterCombined: nil,
		},

		// ADD 2, wait, REMOVE 2 = +2 then -2 comments
		{
			name: "add2_wait_rm2_requests",
			beforeCombined: []*issue_model.Comment{
				reqReview(1, "titi", false),
				reqReview(2, "toto-team", false),
				reqReview(121, "titi", true),
				reqReview(122, "toto-team", true),
			},
			afterCombined: []*issue_model.Comment{
				reqReviewList(1, false, "titi", "toto-team"),
				reqReviewList(121, true, "titi", "toto-team"),
			},
		},

		// Ghost.
		{
			name: "ghost reviews",
			beforeCombined: []*issue_model.Comment{
				reqReview(1, "titi", false),
				ghostReqReview(2, 50),
				ghostReqReview(3, 51),
				ghostReqReview(4, 50),
			},
			afterCombined: []*issue_model.Comment{
				{
					PosterID:    1,
					Type:        issue_model.CommentTypeReviewRequest,
					CreatedUnix: timeutil.TimeStamp(1),
					AddedRequestReview: []issue_model.RequestReviewTarget{
						createReqReviewTarget("titi"), {Team: organization.NewGhostTeam()},
					},
				},
			},
		},
	}

	for _, kase := range kases {
		t.Run(kase.name, kase.doTest)
	}
}

func TestCombineOpenClose(t *testing.T) {
	kases := []testCase{
		// Close then open = nullified
		{
			name: "close_open_nullified",
			beforeCombined: []*issue_model.Comment{
				openOrClose(0, true),
				openOrClose(10, false),
			},
			afterCombined: nil,
		},

		// Close then open later = separate comments
		{
			name: "close_open_later",
			beforeCombined: []*issue_model.Comment{
				openOrClose(0, true),
				openOrClose(61, false),
			},
			sameAfter: true,
		},

		// Close then comment then open = separate comments
		{
			name: "close_comment_open",
			beforeCombined: []*issue_model.Comment{
				openOrClose(0, true),
				commentText(1, "I'm a salmon"),
				openOrClose(2, false),
			},
			sameAfter: true,
		},
	}

	for _, kase := range kases {
		t.Run(kase.name, kase.doTest)
	}
}

func TestCombineMultipleDifferentComments(t *testing.T) {
	lblA := createLabel("a")
	kases := []testCase{
		// Add Label + Close + ReqReview = Combined
		{
			name: "label_close_reqreview_combined",
			beforeCombined: []*issue_model.Comment{
				reqReview(1, "toto", false),
				addLabel(2, "a"),
				openOrClose(3, true),

				reqReview(101, "toto", true),
				openOrClose(102, false),
				delLabel(103, "a"),
			},
			afterCombined: []*issue_model.Comment{
				aggregatedComment(1,
					true,
					[]*issue_model.Label{&lblA},
					[]*issue_model.Label{},
					[]issue_model.RequestReviewTarget{createReqReviewTarget("toto")},
					[]issue_model.RequestReviewTarget{},
				),
				aggregatedComment(101,
					false,
					[]*issue_model.Label{},
					[]*issue_model.Label{&lblA},
					[]issue_model.RequestReviewTarget{},
					[]issue_model.RequestReviewTarget{createReqReviewTarget("toto")},
				),
			},
		},

		// Add Req + Add Label + Close + Del Req + Del Label = Close only
		{
			name: "req_label_close_dellabel_delreq",
			beforeCombined: []*issue_model.Comment{
				addLabel(2, "a"),
				reqReview(3, "titi", false),
				openOrClose(4, true),
				delLabel(5, "a"),
				reqReview(6, "titi", true),
			},
			afterCombined: []*issue_model.Comment{
				openOrClose(2, true),
			},
		},

		// Close + Add Req + Add Label + Del Req + Open = Label only
		{
			name: "close_req_label_open_delreq",
			beforeCombined: []*issue_model.Comment{
				openOrClose(2, true),
				reqReview(4, "titi", false),
				addLabel(5, "a"),
				reqReview(6, "titi", true),
				openOrClose(8, false),
			},
			afterCombined: []*issue_model.Comment{
				addLabel(2, "a"),
			},
		},

		// Add Label + Close + Add ReqReview + Del Label + Open = ReqReview only
		{
			name: "label_close_req_dellabel_open",
			beforeCombined: []*issue_model.Comment{
				addLabel(1, "a"),
				openOrClose(2, true),
				reqReview(4, "titi", false),
				openOrClose(7, false),
				delLabel(8, "a"),
			},
			afterCombined: []*issue_model.Comment{
				reqReviewList(1, false, "titi"),
			},
		},

		// Add Label + Close + ReqReview, then delete everything = nothing
		{
			name: "add_multiple_delete_everything",
			beforeCombined: []*issue_model.Comment{
				addLabel(1, "a"),
				openOrClose(2, true),
				reqReview(4, "titi", false),
				openOrClose(7, false),
				delLabel(8, "a"),
				reqReview(10, "titi", true),
			},
			afterCombined: nil,
		},

		// Add multiple, then comment, then delete everything = separate aggregation
		{
			name: "add_multiple_comment_delete_everything",
			beforeCombined: []*issue_model.Comment{
				addLabel(1, "a"),
				openOrClose(2, true),
				reqReview(4, "titi", false),

				commentText(6, "I'm a salmon"),

				openOrClose(7, false),
				delLabel(8, "a"),
				reqReview(10, "titi", true),
			},
			afterCombined: []*issue_model.Comment{
				aggregatedComment(1,
					true,
					[]*issue_model.Label{&lblA},
					[]*issue_model.Label{},
					[]issue_model.RequestReviewTarget{createReqReviewTarget("titi")},
					[]issue_model.RequestReviewTarget{},
				),
				commentText(6, "I'm a salmon"),
				aggregatedComment(7,
					false,
					[]*issue_model.Label{},
					[]*issue_model.Label{&lblA},
					[]issue_model.RequestReviewTarget{},
					[]issue_model.RequestReviewTarget{createReqReviewTarget("titi")},
				),
			},
		},

		{
			name: "regression_edgecase_finalagg",
			beforeCombined: []*issue_model.Comment{
				commentText(0, "hey"),
				commentText(1, "ho"),
				addLabel(2, "a"),
				reqReview(3, "titi", false),
				delLabel(4, "a"),
				reqReview(5, "titi", true),

				addLabel(120, "a"),

				openOrClose(220, true),
				addLabel(221, "d"),
				reqReview(222, "toto-team", false),
				delLabel(223, "d"),

				delLabel(400, "a"),
			},
			afterCombined: []*issue_model.Comment{
				commentText(0, "hey"),
				commentText(1, "ho"),
				addLabel(120, "a"),
				aggregatedComment(220,
					true,
					[]*issue_model.Label{},
					[]*issue_model.Label{},
					[]issue_model.RequestReviewTarget{createReqReviewTarget("toto-team")},
					[]issue_model.RequestReviewTarget{},
				),
				delLabel(400, "a"),
			},
		},
	}

	for _, kase := range kases {
		t.Run(kase.name, kase.doTest)
	}
}
