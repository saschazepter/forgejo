// Copyright 2017 The Gitea Authors. All rights reserved.
// Copyright 2024 The Forgejo Authors c/o Codeberg e.V.. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	project_model "forgejo.org/models/project"
	repo_model "forgejo.org/models/repo"
	unit_model "forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/indexer/issues"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/references"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	"forgejo.org/modules/translation"
	files_service "forgejo.org/services/repository/files"
	user_service "forgejo.org/services/user"
	"forgejo.org/tests"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getIssuesSelection(t testing.TB, htmlDoc *HTMLDoc) *goquery.Selection {
	issueList := htmlDoc.doc.Find("#issue-list")
	assert.Equal(t, 1, issueList.Length())
	return issueList.Find(".flex-item").Find(".issue-title")
}

func getIssue(t *testing.T, repoID int64, issueSelection *goquery.Selection) *issues_model.Issue {
	href, exists := issueSelection.Attr("href")
	assert.True(t, exists)
	indexStr := href[strings.LastIndexByte(href, '/')+1:]
	index, err := strconv.Atoi(indexStr)
	require.NoError(t, err, "Invalid issue href: %s", href)
	return unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: repoID, Index: int64(index)})
}

func assertMatch(t testing.TB, issue *issues_model.Issue, keyword string) {
	matches := strings.Contains(strings.ToLower(issue.Title), keyword) ||
		strings.Contains(strings.ToLower(issue.Content), keyword)
	for _, comment := range issue.Comments {
		matches = matches || strings.Contains(
			strings.ToLower(comment.Content),
			keyword,
		)
	}
	assert.True(t, matches)
}

func TestNoLoginViewIssues(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	req := NewRequest(t, "GET", "/user2/repo1/issues")
	MakeRequest(t, req, http.StatusOK)
}

func TestViewIssues(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	req := NewRequest(t, "GET", "/user2/repo1/issues")
	resp := MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	search := htmlDoc.doc.Find(".list-header-search > .search > .input > input")
	placeholder, _ := search.Attr("placeholder")
	assert.Equal(t, "Search issues…", placeholder)
}

func TestViewIssuesSortByType(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	session := loginUser(t, user.Name)
	req := NewRequest(t, "GET", repo.Link()+"/issues?type=created_by")
	resp := session.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	issuesSelection := getIssuesSelection(t, htmlDoc)
	expectedNumIssues := unittest.GetCount(t,
		&issues_model.Issue{RepoID: repo.ID, PosterID: user.ID},
		unittest.Cond("is_closed=?", false),
		unittest.Cond("is_pull=?", false),
	)
	if expectedNumIssues > setting.UI.IssuePagingNum {
		expectedNumIssues = setting.UI.IssuePagingNum
	}
	assert.Equal(t, expectedNumIssues, issuesSelection.Length())

	issuesSelection.Each(func(_ int, selection *goquery.Selection) {
		issue := getIssue(t, repo.ID, selection)
		assert.Equal(t, user.ID, issue.PosterID)
	})
}

func TestViewIssuesKeyword(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{
		RepoID: repo.ID,
		Index:  1,
	})
	issues.UpdateIssueIndexer(t.Context(), issue.ID)

	const keyword = "first"
	req := NewRequestf(t, "GET", "%s/issues?q=%s", repo.Link(), keyword)
	resp := MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	issuesSelection := getIssuesSelection(t, htmlDoc)
	assert.Equal(t, 1, issuesSelection.Length())
	issuesSelection.Each(func(_ int, selection *goquery.Selection) {
		issue := getIssue(t, repo.ID, selection)
		assert.False(t, issue.IsClosed)
		assert.False(t, issue.IsPull)
		assertMatch(t, issue, keyword)
	})

	// keyword: 'firstt'
	// should not match when using phrase search
	req = NewRequestf(t, "GET", "%s/issues?q=\"%st\"", repo.Link(), keyword)
	resp = MakeRequest(t, req, http.StatusOK)
	htmlDoc = NewHTMLParser(t, resp.Body)
	issuesSelection = getIssuesSelection(t, htmlDoc)
	assert.Equal(t, 0, issuesSelection.Length())

	// should match as 'first' when using a standard query
	req = NewRequestf(t, "GET", "%s/issues?q=%st", repo.Link(), keyword)
	resp = MakeRequest(t, req, http.StatusOK)
	htmlDoc = NewHTMLParser(t, resp.Body)
	issuesSelection = getIssuesSelection(t, htmlDoc)
	assert.Equal(t, 1, issuesSelection.Length())
	issuesSelection.Each(func(_ int, selection *goquery.Selection) {
		issue := getIssue(t, repo.ID, selection)
		assert.False(t, issue.IsClosed)
		assert.False(t, issue.IsPull)
		assertMatch(t, issue, keyword)
	})
}

func TestViewIssuesSearchOptions(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	// there are two issues in repo1, both bound to a project. Add one
	// that is not bound to any project.
	_, issueNoProject := testIssueWithBean(t, "user2", 1, "Title", "Description")

	t.Run("All issues", func(t *testing.T) {
		req := NewRequestf(t, "GET", "%s/issues?state=all", repo.Link())
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)
		issuesSelection := getIssuesSelection(t, htmlDoc)
		assert.Equal(t, 3, issuesSelection.Length())
	})

	t.Run("Issues with no project", func(t *testing.T) {
		req := NewRequestf(t, "GET", "%s/issues?state=all&project=-1", repo.Link())
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)
		issuesSelection := getIssuesSelection(t, htmlDoc)
		assert.Equal(t, 1, issuesSelection.Length())
		issuesSelection.Each(func(_ int, selection *goquery.Selection) {
			issue := getIssue(t, repo.ID, selection)
			assert.Equal(t, issueNoProject.ID, issue.ID)
		})
	})

	t.Run("Issues with a specific project", func(t *testing.T) {
		project := unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: 1})

		req := NewRequestf(t, "GET", "%s/issues?state=all&project=%d", repo.Link(), project.ID)
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)
		issuesSelection := getIssuesSelection(t, htmlDoc)
		assert.Equal(t, 2, issuesSelection.Length())
		found := map[int64]bool{
			1: false,
			5: false,
		}
		issuesSelection.Each(func(_ int, selection *goquery.Selection) {
			issue := getIssue(t, repo.ID, selection)
			found[issue.ID] = true
		})
		assert.Len(t, found, 2)
		assert.True(t, found[1])
		assert.True(t, found[5])
	})
}

func TestNoLoginViewIssue(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	req := NewRequest(t, "GET", "/user2/repo1/issues/1")
	MakeRequest(t, req, http.StatusOK)
}

func TestViewIssueCommentBox(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	locale := translation.NewLocale("en-US")

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	collaborator := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	viewer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	ownerSession := loginUser(t, owner.Name)
	collaboratorSession := loginUser(t, collaborator.Name)
	viewerSession := loginUser(t, viewer.Name)

	openIssueURL, _ := testIssueWithBean(t, owner.Name, repo.ID, "Open", "Description")

	warningSignin := string(locale.Tr("repo.issues.sign_in_require_desc", fmt.Sprintf("/user/login?redirect_to=%s", url.QueryEscape(openIssueURL))))
	warningLocked := locale.TrString("discussion.locked")
	warningBlocked := locale.TrString("repo.comment.blocked_by_user")

	assertCommentForm := func(session *TestSession, url string, shouldExist bool) *HTMLDoc {
		var resp *httptest.ResponseRecorder

		req := NewRequest(t, "GET", url)
		if session != nil {
			resp = session.MakeRequest(t, req, http.StatusOK)
		} else {
			resp = MakeRequest(t, req, http.StatusOK)
		}
		htmlDoc := NewHTMLParser(t, resp.Body)
		htmlDoc.AssertElement(t, ".issue-content #comment-form", shouldExist)

		return htmlDoc
	}

	assertWarning := func(session *TestSession, url, warning string) {
		htmlDoc := assertCommentForm(session, url, false)

		timeline := htmlDoc.doc.Find(".issue-content .ui.timeline").Children()
		assert.Positivef(t, timeline.Length(), "There should be at least one entry in the timeline")

		html, _ := timeline.Last().Html()
		assert.Equal(t, strings.TrimSpace(html), warning)
	}

	t.Run("Owner can see comment box", func(t *testing.T) {
		assertCommentForm(ownerSession, openIssueURL, true)
	})

	t.Run("Collaborator can see comment box", func(t *testing.T) {
		assertCommentForm(collaboratorSession, openIssueURL, true)
	})

	t.Run("Viewer can see comment box", func(t *testing.T) {
		assertCommentForm(viewerSession, openIssueURL, true)
	})

	t.Run("Anonymous will see singin warning", func(t *testing.T) {
		assertWarning(nil, openIssueURL, warningSignin)
	})

	lockedIssueURL, lockedIssue := testIssueWithBean(t, owner.Name, repo.ID, "Locked", "Description")

	// Lock the issue
	req := NewRequest(t, "GET", lockedIssueURL)
	resp := ownerSession.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	req = NewRequestWithValues(t, "POST", fmt.Sprintf("%s/lock", lockedIssueURL), map[string]string{
		"_csrf":  htmlDoc.GetCSRF(),
		"reason": "Too heated",
	})
	_ = ownerSession.MakeRequest(t, req, http.StatusOK)

	lockedIssue = unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: lockedIssue.ID})
	assert.True(t, lockedIssue.IsLocked)

	t.Run("Owner can see comment box in locked issue", func(t *testing.T) {
		assertCommentForm(ownerSession, lockedIssueURL, true)
	})

	t.Run("Collaborator can see comment box in locked issue", func(t *testing.T) {
		assertCommentForm(collaboratorSession, lockedIssueURL, true)
	})

	t.Run("Viewer will see warning in locked issue", func(t *testing.T) {
		assertWarning(viewerSession, lockedIssueURL, warningLocked)
	})

	t.Run("Anonymous will see warning in locked issue", func(t *testing.T) {
		assertWarning(nil, lockedIssueURL, warningLocked)
	})

	err := user_service.BlockUser(db.DefaultContext, owner.ID, viewer.ID)
	require.NoError(t, err)

	t.Run("Blocked viewer will see warning", func(t *testing.T) {
		assertWarning(viewerSession, lockedIssueURL, warningBlocked)
		assertWarning(viewerSession, openIssueURL, warningBlocked)
	})
}

func testNewIssue(t *testing.T, session *TestSession, user, repo, title, content string) string {
	req := NewRequest(t, "GET", path.Join(user, repo, "issues", "new"))
	resp := session.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	link, exists := htmlDoc.doc.Find("form.ui.form").Attr("action")
	assert.True(t, exists, "The template has changed")
	req = NewRequestWithValues(t, "POST", link, map[string]string{
		"_csrf":   htmlDoc.GetCSRF(),
		"title":   title,
		"content": content,
	})
	resp = session.MakeRequest(t, req, http.StatusOK)

	issueURL := test.RedirectURL(resp)
	req = NewRequest(t, "GET", issueURL)
	resp = session.MakeRequest(t, req, http.StatusOK)

	htmlDoc = NewHTMLParser(t, resp.Body)
	val := htmlDoc.doc.Find("#issue-title-display").Text()
	assert.Contains(t, val, title)
	// test for first line only and if it contains only letters and spaces
	contentFirstLine := strings.Split(content, "\n")[0]
	patNotLetterOrSpace := regexp.MustCompile(`[^\p{L}\s]`)
	if len(contentFirstLine) != 0 && !patNotLetterOrSpace.MatchString(contentFirstLine) {
		val = htmlDoc.doc.Find(".comment .render-content p").First().Text()
		assert.Equal(t, contentFirstLine, val)
	}
	return issueURL
}

func testIssueAddComment(t *testing.T, session *TestSession, issueURL, content, status string) int64 {
	req := NewRequest(t, "GET", issueURL)
	resp := session.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	link, exists := htmlDoc.doc.Find("#comment-form").Attr("action")
	assert.True(t, exists, "The template has changed")

	commentCount := htmlDoc.doc.Find(".comment-list .comment .render-content").Length()

	req = NewRequestWithValues(t, "POST", link, map[string]string{
		"_csrf":   htmlDoc.GetCSRF(),
		"content": content,
		"status":  status,
	})
	resp = session.MakeRequest(t, req, http.StatusOK)

	req = NewRequest(t, "GET", test.RedirectURL(resp))
	resp = session.MakeRequest(t, req, http.StatusOK)

	htmlDoc = NewHTMLParser(t, resp.Body)

	val := htmlDoc.doc.Find(".comment-list .comment .render-content p").Eq(commentCount).Text()
	assert.Equal(t, content, val)

	idAttr, has := htmlDoc.doc.Find(".comment-list .comment").Eq(commentCount).Attr("id")
	idStr := idAttr[strings.LastIndexByte(idAttr, '-')+1:]
	assert.True(t, has)
	id, err := strconv.Atoi(idStr)
	require.NoError(t, err)
	return int64(id)
}

func TestNewIssue(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user2")
	testNewIssue(t, session, "user2", "repo1", "Title", "Description")
}

func TestIssueCheckboxes(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user2")
	issueURL := testNewIssue(t, session, "user2", "repo1", "Title", `- [x] small x
- [X] capital X
- [ ] empty
  - [x]x without gap
  - [ ]empty without gap
- [x]
x on new line
- [ ]
empty on new line
	-	[	]	tabs instead of spaces
Description`)
	req := NewRequest(t, "GET", issueURL)
	resp := session.MakeRequest(t, req, http.StatusOK)
	issueContent := NewHTMLParser(t, resp.Body).doc.Find(".comment .render-content").First()
	isCheckBox := func(i int, s *goquery.Selection) bool {
		typeVal, typeExists := s.Attr("type")
		return typeExists && typeVal == "checkbox"
	}
	isChecked := func(i int, s *goquery.Selection) bool {
		_, checkedExists := s.Attr("checked")
		return checkedExists
	}
	checkBoxes := issueContent.Find("input").FilterFunction(isCheckBox)
	assert.Equal(t, 8, checkBoxes.Length())
	assert.Equal(t, 4, checkBoxes.FilterFunction(isChecked).Length())

	// Issues list should show the correct numbers of checked and total checkboxes
	repo, err := repo_model.GetRepositoryByOwnerAndName(db.DefaultContext, "user2", "repo1")
	require.NoError(t, err)
	req = NewRequestf(t, "GET", "%s/issues", repo.Link())
	resp = MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	issuesSelection := htmlDoc.Find("#issue-list .flex-item")
	assert.Equal(t, "4 / 8", strings.TrimSpace(issuesSelection.Find(".checklist").Text()))
	value, _ := issuesSelection.Find("progress").Attr("value")
	vmax, _ := issuesSelection.Find("progress").Attr("max")
	assert.Equal(t, "4", value)
	assert.Equal(t, "8", vmax)
}

func TestIssueDependencies(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	session := loginUser(t, owner.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteIssue)

	repo, _, f := tests.CreateDeclarativeRepoWithOptions(t, owner, tests.DeclarativeRepoOptions{})
	defer f()

	createIssue := func(t *testing.T, title string) api.Issue {
		t.Helper()

		urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/issues", owner.Name, repo.Name)
		req := NewRequestWithJSON(t, "POST", urlStr, &api.CreateIssueOption{
			Body:  "",
			Title: title,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusCreated)

		var apiIssue api.Issue
		DecodeJSON(t, resp, &apiIssue)

		return apiIssue
	}
	addDependency := func(t *testing.T, issue, dependency api.Issue) {
		t.Helper()

		urlStr := fmt.Sprintf("/%s/%s/issues/%d/dependency/add", owner.Name, repo.Name, issue.Index)
		req := NewRequestWithValues(t, "POST", urlStr, map[string]string{
			"_csrf":         GetCSRF(t, session, fmt.Sprintf("/%s/%s/issues/%d", owner.Name, repo.Name, issue.Index)),
			"newDependency": fmt.Sprintf("%d", dependency.Index),
		})
		session.MakeRequest(t, req, http.StatusSeeOther)
	}
	removeDependency := func(t *testing.T, issue, dependency api.Issue) {
		t.Helper()

		urlStr := fmt.Sprintf("/%s/%s/issues/%d/dependency/delete", owner.Name, repo.Name, issue.Index)
		req := NewRequestWithValues(t, "POST", urlStr, map[string]string{
			"_csrf":              GetCSRF(t, session, fmt.Sprintf("/%s/%s/issues/%d", owner.Name, repo.Name, issue.Index)),
			"removeDependencyID": fmt.Sprintf("%d", dependency.Index),
			"dependencyType":     "blockedBy",
		})
		session.MakeRequest(t, req, http.StatusSeeOther)
	}

	assertHasDependency := func(t *testing.T, issueID, dependencyID int64, hasDependency bool) {
		t.Helper()

		urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/dependencies", owner.Name, repo.Name, issueID)
		req := NewRequest(t, "GET", urlStr)
		resp := MakeRequest(t, req, http.StatusOK)

		var issues []api.Issue
		DecodeJSON(t, resp, &issues)

		if hasDependency {
			assert.NotEmpty(t, issues)
			assert.Equal(t, issues[0].Index, dependencyID)
		} else {
			assert.Empty(t, issues)
		}
	}

	t.Run("Add dependency", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		issue1 := createIssue(t, "issue #1")
		issue2 := createIssue(t, "issue #2")
		addDependency(t, issue1, issue2)

		assertHasDependency(t, issue1.Index, issue2.Index, true)
	})

	t.Run("Remove dependency", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		issue1 := createIssue(t, "issue #1")
		issue2 := createIssue(t, "issue #2")
		addDependency(t, issue1, issue2)
		removeDependency(t, issue1, issue2)

		assertHasDependency(t, issue1.Index, issue2.Index, false)
	})
}

func TestEditIssue(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user2")
	issueURL := testNewIssue(t, session, "user2", "repo1", "Title", "Description")

	req := NewRequestWithValues(t, "POST", fmt.Sprintf("%s/content", issueURL), map[string]string{
		"_csrf":   GetCSRF(t, session, issueURL),
		"content": "modified content",
		"context": fmt.Sprintf("/%s/%s", "user2", "repo1"),
	})
	session.MakeRequest(t, req, http.StatusOK)

	req = NewRequestWithValues(t, "POST", fmt.Sprintf("%s/content", issueURL), map[string]string{
		"_csrf":   GetCSRF(t, session, issueURL),
		"content": "modified content",
		"context": fmt.Sprintf("/%s/%s", "user2", "repo1"),
	})
	session.MakeRequest(t, req, http.StatusBadRequest)

	req = NewRequestWithValues(t, "POST", fmt.Sprintf("%s/content", issueURL), map[string]string{
		"_csrf":           GetCSRF(t, session, issueURL),
		"content":         "modified content",
		"content_version": "1",
		"context":         fmt.Sprintf("/%s/%s", "user2", "repo1"),
	})
	session.MakeRequest(t, req, http.StatusOK)
}

func TestIssueCommentClose(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user2")
	issueURL := testNewIssue(t, session, "user2", "repo1", "Title", "Description")
	testIssueAddComment(t, session, issueURL, "Test comment 1", "")
	testIssueAddComment(t, session, issueURL, "Test comment 2", "")
	testIssueAddComment(t, session, issueURL, "Test comment 3", "close")

	// Validate that issue content has not been updated
	req := NewRequest(t, "GET", issueURL)
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)
	val := htmlDoc.doc.Find(".comment-list .comment .render-content p").First().Text()
	assert.Equal(t, "Description", val)
}

func TestIssueCommentDelete(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user2")
	issueURL := testNewIssue(t, session, "user2", "repo1", "Title", "Description")
	comment1 := "Test comment 1"
	commentID := testIssueAddComment(t, session, issueURL, comment1, "")
	comment := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: commentID})
	assert.Equal(t, comment1, comment.Content)

	// Using the ID of a comment that does not belong to the repository must fail
	req := NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/%s/comments/%d/delete", "user5", "repo4", commentID), map[string]string{
		"_csrf": GetCSRF(t, session, issueURL),
	})
	session.MakeRequest(t, req, http.StatusNotFound)
	req = NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/%s/comments/%d/delete", "user2", "repo1", commentID), map[string]string{
		"_csrf": GetCSRF(t, session, issueURL),
	})
	session.MakeRequest(t, req, http.StatusOK)
	unittest.AssertNotExistsBean(t, &issues_model.Comment{ID: commentID})
}

func TestIssueCommentAttachment(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	const repoURL = "user2/repo1"
	const content = "Test comment 4"
	const status = ""
	session := loginUser(t, "user2")
	issueURL := testNewIssue(t, session, "user2", "repo1", "Title", "Description")

	req := NewRequest(t, "GET", issueURL)
	resp := session.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	link, exists := htmlDoc.doc.Find("#comment-form").Attr("action")
	assert.True(t, exists, "The template has changed")

	uuid := createAttachment(t, session, GetCSRF(t, session, repoURL), repoURL, "image.png", generateImg(), http.StatusOK)

	commentCount := htmlDoc.doc.Find(".comment-list .comment .render-content").Length()

	req = NewRequestWithValues(t, "POST", link, map[string]string{
		"_csrf":   htmlDoc.GetCSRF(),
		"content": content,
		"status":  status,
		"files":   uuid,
	})
	resp = session.MakeRequest(t, req, http.StatusOK)

	req = NewRequest(t, "GET", test.RedirectURL(resp))
	resp = session.MakeRequest(t, req, http.StatusOK)

	htmlDoc = NewHTMLParser(t, resp.Body)

	val := htmlDoc.doc.Find(".comment-list .comment .render-content p").Eq(commentCount).Text()
	assert.Equal(t, content, val)

	idAttr, has := htmlDoc.doc.Find(".comment-list .comment").Eq(commentCount).Attr("id")
	idStr := idAttr[strings.LastIndexByte(idAttr, '-')+1:]
	assert.True(t, has)
	id, err := strconv.Atoi(idStr)
	require.NoError(t, err)
	assert.NotEqual(t, 0, id)

	req = NewRequest(t, "GET", fmt.Sprintf("/%s/%s/comments/%d/attachments", "user2", "repo1", id))
	session.MakeRequest(t, req, http.StatusOK)

	// Using the ID of a comment that does not belong to the repository must fail
	req = NewRequest(t, "GET", fmt.Sprintf("/%s/%s/comments/%d/attachments", "user5", "repo4", id))
	session.MakeRequest(t, req, http.StatusNotFound)
}

func TestIssueCommentUpdate(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user2")
	issueURL := testNewIssue(t, session, "user2", "repo1", "Title", "Description")
	comment1 := "Test comment 1"
	commentID := testIssueAddComment(t, session, issueURL, comment1, "")

	comment := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: commentID})
	assert.Equal(t, comment1, comment.Content)

	modifiedContent := comment.Content + "MODIFIED"

	// Using the ID of a comment that does not belong to the repository must fail
	req := NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/%s/comments/%d", "user5", "repo4", commentID), map[string]string{
		"_csrf":   GetCSRF(t, session, issueURL),
		"content": modifiedContent,
	})
	session.MakeRequest(t, req, http.StatusNotFound)

	req = NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/%s/comments/%d", "user2", "repo1", commentID), map[string]string{
		"_csrf":   GetCSRF(t, session, issueURL),
		"content": modifiedContent,
	})
	session.MakeRequest(t, req, http.StatusOK)

	comment = unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: commentID})
	assert.Equal(t, modifiedContent, comment.Content)

	// make the comment empty
	req = NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/%s/comments/%d", "user2", "repo1", commentID), map[string]string{
		"_csrf":           GetCSRF(t, session, issueURL),
		"content":         "",
		"content_version": fmt.Sprintf("%d", comment.ContentVersion),
	})
	session.MakeRequest(t, req, http.StatusOK)

	comment = unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: commentID})
	assert.Empty(t, comment.Content)
}

func TestIssueCommentUpdateSimultaneously(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user2")
	issueURL := testNewIssue(t, session, "user2", "repo1", "Title", "Description")
	comment1 := "Test comment 1"
	commentID := testIssueAddComment(t, session, issueURL, comment1, "")

	comment := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: commentID})
	assert.Equal(t, comment1, comment.Content)

	modifiedContent := comment.Content + "MODIFIED"

	req := NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/%s/comments/%d", "user2", "repo1", commentID), map[string]string{
		"_csrf":   GetCSRF(t, session, issueURL),
		"content": modifiedContent,
	})
	session.MakeRequest(t, req, http.StatusOK)

	modifiedContent = comment.Content + "2"

	req = NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/%s/comments/%d", "user2", "repo1", commentID), map[string]string{
		"_csrf":   GetCSRF(t, session, issueURL),
		"content": modifiedContent,
	})
	session.MakeRequest(t, req, http.StatusBadRequest)

	req = NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/%s/comments/%d", "user2", "repo1", commentID), map[string]string{
		"_csrf":           GetCSRF(t, session, issueURL),
		"content":         modifiedContent,
		"content_version": "1",
	})
	session.MakeRequest(t, req, http.StatusOK)

	comment = unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: commentID})
	assert.Equal(t, modifiedContent, comment.Content)
	assert.Equal(t, 2, comment.ContentVersion)
}

func TestIssueReaction(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user2")
	issueURL := testNewIssue(t, session, "user2", "repo1", "Title", "Description")

	req := NewRequest(t, "GET", issueURL)
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	req = NewRequestWithValues(t, "POST", path.Join(issueURL, "/reactions/react"), map[string]string{
		"_csrf":   htmlDoc.GetCSRF(),
		"content": "8ball",
	})
	session.MakeRequest(t, req, http.StatusInternalServerError)
	req = NewRequestWithValues(t, "POST", path.Join(issueURL, "/reactions/react"), map[string]string{
		"_csrf":   htmlDoc.GetCSRF(),
		"content": "eyes",
	})
	session.MakeRequest(t, req, http.StatusOK)
	req = NewRequestWithValues(t, "POST", path.Join(issueURL, "/reactions/unreact"), map[string]string{
		"_csrf":   htmlDoc.GetCSRF(),
		"content": "eyes",
	})
	session.MakeRequest(t, req, http.StatusOK)
}

func TestIssueCrossReference(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	// Issue that will be referenced
	_, issueBase := testIssueWithBean(t, "user2", 1, "Title", "Description")

	// Ref from issue title
	issueRefURL, issueRef := testIssueWithBean(t, "user2", 1, fmt.Sprintf("Title ref #%d", issueBase.Index), "Description")
	unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{
		IssueID:      issueBase.ID,
		RefRepoID:    1,
		RefIssueID:   issueRef.ID,
		RefCommentID: 0,
		RefIsPull:    false,
		RefAction:    references.XRefActionNone,
	})

	// Edit title, neuter ref
	testIssueChangeInfo(t, "user2", issueRefURL, "title", "Title no ref")
	unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{
		IssueID:      issueBase.ID,
		RefRepoID:    1,
		RefIssueID:   issueRef.ID,
		RefCommentID: 0,
		RefIsPull:    false,
		RefAction:    references.XRefActionNeutered,
	})

	// Ref from issue content
	issueRefURL, issueRef = testIssueWithBean(t, "user2", 1, "TitleXRef", fmt.Sprintf("Description ref #%d", issueBase.Index))
	unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{
		IssueID:      issueBase.ID,
		RefRepoID:    1,
		RefIssueID:   issueRef.ID,
		RefCommentID: 0,
		RefIsPull:    false,
		RefAction:    references.XRefActionNone,
	})

	// Edit content, neuter ref
	testIssueChangeInfo(t, "user2", issueRefURL, "content", "Description no ref")
	unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{
		IssueID:      issueBase.ID,
		RefRepoID:    1,
		RefIssueID:   issueRef.ID,
		RefCommentID: 0,
		RefIsPull:    false,
		RefAction:    references.XRefActionNeutered,
	})

	// Ref from a comment
	session := loginUser(t, "user2")
	commentID := testIssueAddComment(t, session, issueRefURL, fmt.Sprintf("Adding ref from comment #%d", issueBase.Index), "")
	comment := &issues_model.Comment{
		IssueID:      issueBase.ID,
		RefRepoID:    1,
		RefIssueID:   issueRef.ID,
		RefCommentID: commentID,
		RefIsPull:    false,
		RefAction:    references.XRefActionNone,
	}
	unittest.AssertExistsAndLoadBean(t, comment)

	// Ref from a different repository
	_, issueRef = testIssueWithBean(t, "user12", 10, "TitleXRef", fmt.Sprintf("Description ref user2/repo1#%d", issueBase.Index))
	unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{
		IssueID:      issueBase.ID,
		RefRepoID:    10,
		RefIssueID:   issueRef.ID,
		RefCommentID: 0,
		RefIsPull:    false,
		RefAction:    references.XRefActionNone,
	})
}

func testIssueWithBean(t *testing.T, user string, repoID int64, title, content string) (string, *issues_model.Issue) {
	session := loginUser(t, user)
	issueURL := testNewIssue(t, session, user, fmt.Sprintf("repo%d", repoID), title, content)
	indexStr := issueURL[strings.LastIndexByte(issueURL, '/')+1:]
	index, err := strconv.Atoi(indexStr)
	require.NoError(t, err, "Invalid issue href: %s", issueURL)
	issue := &issues_model.Issue{RepoID: repoID, Index: int64(index)}
	unittest.AssertExistsAndLoadBean(t, issue)
	return issueURL, issue
}

func testIssueChangeInfo(t *testing.T, user, issueURL, info, value string) {
	session := loginUser(t, user)

	req := NewRequest(t, "GET", issueURL)
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	req = NewRequestWithValues(t, "POST", path.Join(issueURL, info), map[string]string{
		"_csrf": htmlDoc.GetCSRF(),
		info:    value,
	})
	_ = session.MakeRequest(t, req, http.StatusOK)
}

func TestIssueRedirect(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user2")

	// Test external tracker where style not set (shall default numeric)
	req := NewRequest(t, "GET", path.Join("org26", "repo_external_tracker", "issues", "1"))
	resp := session.MakeRequest(t, req, http.StatusSeeOther)
	assert.Equal(t, "https://tracker.com/org26/repo_external_tracker/issues/1", test.RedirectURL(resp))

	// Test external tracker with numeric style
	req = NewRequest(t, "GET", path.Join("org26", "repo_external_tracker_numeric", "issues", "1"))
	resp = session.MakeRequest(t, req, http.StatusSeeOther)
	assert.Equal(t, "https://tracker.com/org26/repo_external_tracker_numeric/issues/1", test.RedirectURL(resp))

	// Test external tracker with alphanumeric style (for a pull request)
	req = NewRequest(t, "GET", path.Join("org26", "repo_external_tracker_alpha", "issues", "1"))
	resp = session.MakeRequest(t, req, http.StatusSeeOther)
	assert.Equal(t, "/"+path.Join("org26", "repo_external_tracker_alpha", "pulls", "1"), test.RedirectURL(resp))
}

func TestSearchIssues(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	expectedIssueCount := 20 // from the fixtures
	if expectedIssueCount > setting.UI.IssuePagingNum {
		expectedIssueCount = setting.UI.IssuePagingNum
	}

	link, _ := url.Parse("/issues/search")
	req := NewRequest(t, "GET", link.String())
	resp := session.MakeRequest(t, req, http.StatusOK)
	var apiIssues []*api.Issue
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, expectedIssueCount)

	since := "2000-01-01T00:50:01+00:00" // 946687801
	before := time.Unix(999307200, 0).Format(time.RFC3339)
	query := url.Values{}
	query.Add("since", since)
	query.Add("before", before)
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String())
	resp = session.MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 11)
	query.Del("since")
	query.Del("before")

	query.Add("state", "closed")
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String())
	resp = session.MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 2)

	query.Set("state", "all")
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String())
	resp = session.MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Equal(t, "22", resp.Header().Get("X-Total-Count"))
	assert.Len(t, apiIssues, 20)

	query.Add("limit", "5")
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String())
	resp = session.MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Equal(t, "22", resp.Header().Get("X-Total-Count"))
	assert.Len(t, apiIssues, 5)

	query = url.Values{"assigned": {"true"}, "state": {"all"}}
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String())
	resp = session.MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 2)

	query = url.Values{"milestones": {"milestone1"}, "state": {"all"}}
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String())
	resp = session.MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 1)

	query = url.Values{"milestones": {"milestone1,milestone3"}, "state": {"all"}}
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String())
	resp = session.MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 2)

	query = url.Values{"owner": {"user2"}} // user
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String())
	resp = session.MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 8)

	query = url.Values{"owner": {"org3"}} // organization
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String())
	resp = session.MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 5)

	query = url.Values{"owner": {"org3"}, "team": {"team1"}} // organization + team
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String())
	resp = session.MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 2)
}

func TestSearchIssuesWithLabels(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	expectedIssueCount := 20 // from the fixtures
	if expectedIssueCount > setting.UI.IssuePagingNum {
		expectedIssueCount = setting.UI.IssuePagingNum
	}

	session := loginUser(t, "user1")
	link, _ := url.Parse("/issues/search")
	query := url.Values{}
	var apiIssues []*api.Issue

	link.RawQuery = query.Encode()
	req := NewRequest(t, "GET", link.String())
	resp := session.MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, expectedIssueCount)

	query.Add("labels", "label1")
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String())
	resp = session.MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 2)

	// multiple labels
	query.Set("labels", "label1,label2")
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String())
	resp = session.MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 2)

	// an org label
	query.Set("labels", "orglabel4")
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String())
	resp = session.MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 1)

	// org and repo label
	query.Set("labels", "label2,orglabel4")
	query.Add("state", "all")
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String())
	resp = session.MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 2)

	// org and repo label which share the same issue
	query.Set("labels", "label1,orglabel4")
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String())
	resp = session.MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 2)
}

func TestGetIssueInfo(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 10})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	require.NoError(t, issue.LoadAttributes(db.DefaultContext))
	assert.Equal(t, int64(1019307200), int64(issue.DeadlineUnix))
	assert.Equal(t, api.StateOpen, issue.State())

	session := loginUser(t, owner.Name)

	urlStr := fmt.Sprintf("/%s/%s/issues/%d/info", owner.Name, repo.Name, issue.Index)
	req := NewRequest(t, "GET", urlStr)
	resp := session.MakeRequest(t, req, http.StatusOK)
	var apiIssue api.Issue
	DecodeJSON(t, resp, &apiIssue)

	assert.Equal(t, issue.ID, apiIssue.ID)
}

func TestIssuePinMove(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user2")
	issueURL, issue := testIssueWithBean(t, "user2", 1, "Title", "Content")
	assert.Equal(t, 0, issue.PinOrder)

	req := NewRequestWithValues(t, "POST", fmt.Sprintf("%s/pin", issueURL), map[string]string{
		"_csrf": GetCSRF(t, session, issueURL),
	})
	session.MakeRequest(t, req, http.StatusOK)
	issue = unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: issue.ID})

	position := 1
	assert.Equal(t, position, issue.PinOrder)

	newPosition := 2

	// Using the ID of an issue that does not belong to the repository must fail
	{
		session5 := loginUser(t, "user5")
		movePinURL := "/user5/repo4/issues/move_pin?_csrf=" + GetCSRF(t, session5, issueURL)
		req = NewRequestWithJSON(t, "POST", movePinURL, map[string]any{
			"id":       issue.ID,
			"position": newPosition,
		})
		session5.MakeRequest(t, req, http.StatusNotFound)

		issue = unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: issue.ID})
		assert.Equal(t, position, issue.PinOrder)
	}

	movePinURL := issueURL[:strings.LastIndexByte(issueURL, '/')] + "/move_pin?_csrf=" + GetCSRF(t, session, issueURL)
	req = NewRequestWithJSON(t, "POST", movePinURL, map[string]any{
		"id":       issue.ID,
		"position": newPosition,
	})
	session.MakeRequest(t, req, http.StatusNoContent)

	issue = unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: issue.ID})
	assert.Equal(t, newPosition, issue.PinOrder)
}

func TestUpdateIssueDeadline(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	issueBefore := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 10})
	repoBefore := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issueBefore.RepoID})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repoBefore.OwnerID})
	require.NoError(t, issueBefore.LoadAttributes(db.DefaultContext))
	assert.Equal(t, int64(1019307200), int64(issueBefore.DeadlineUnix))
	assert.Equal(t, api.StateOpen, issueBefore.State())

	session := loginUser(t, owner.Name)

	issueURL := fmt.Sprintf("%s/%s/issues/%d", owner.Name, repoBefore.Name, issueBefore.Index)
	req := NewRequest(t, "GET", issueURL)
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	urlStr := issueURL + "/deadline?_csrf=" + htmlDoc.GetCSRF()
	req = NewRequestWithJSON(t, "POST", urlStr, map[string]string{
		"due_date": "2022-04-06T00:00:00.000Z",
	})

	resp = session.MakeRequest(t, req, http.StatusCreated)
	var apiIssue api.IssueDeadline
	DecodeJSON(t, resp, &apiIssue)

	assert.Equal(t, "2022-04-06", apiIssue.Deadline.Format("2006-01-02"))
}

func TestUpdateIssueTitle(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	issueBefore := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issueBefore.RepoID})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	require.NoError(t, issueBefore.LoadAttributes(db.DefaultContext))
	assert.Equal(t, "issue1", issueBefore.Title)

	issueTitleUpdateTests := []struct {
		title            string
		expectedHTTPCode int
	}{
		{
			title:            "normal-title",
			expectedHTTPCode: http.StatusOK,
		},
		{
			title:            "extra-long-title-with-exactly-255-chars-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			expectedHTTPCode: http.StatusOK,
		},
		{
			title:            "",
			expectedHTTPCode: http.StatusBadRequest,
		},
		{
			title:            " ",
			expectedHTTPCode: http.StatusBadRequest,
		},
		{
			title:            "extra-long-title-over-255-chars-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			expectedHTTPCode: http.StatusBadRequest,
		},
	}

	session := loginUser(t, owner.Name)
	issueURL := fmt.Sprintf("%s/%s/issues/%d", owner.Name, repo.Name, issueBefore.Index)
	urlStr := issueURL + "/title"

	for _, issueTitleUpdateTest := range issueTitleUpdateTests {
		req := NewRequestWithValues(t, "POST", urlStr, map[string]string{
			"title": issueTitleUpdateTest.title,
			"_csrf": GetCSRF(t, session, issueURL),
		})

		resp := session.MakeRequest(t, req, issueTitleUpdateTest.expectedHTTPCode)

		// JSON data is received only if the request succeeds
		if issueTitleUpdateTest.expectedHTTPCode == http.StatusOK {
			issueAfter := struct {
				Title string `json:"title"`
			}{}

			DecodeJSON(t, resp, &issueAfter)
			assert.Equal(t, issueTitleUpdateTest.title, issueAfter.Title)
		}
	}
}

func TestIssueReferenceURL(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user2")

	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})

	req := NewRequest(t, "GET", fmt.Sprintf("%s/issues/%d", repo.FullName(), issue.Index))
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	// the "reference" uses relative URLs, then JS code will convert them to absolute URLs for current origin, in case users are using multiple domains
	ref, _ := htmlDoc.Find(`.timeline-item.comment.first .reference-issue`).Attr("data-reference")
	assert.Equal(t, "/user2/repo1/issues/1#issue-1", ref)

	ref, _ = htmlDoc.Find(`.timeline-item.comment:not(.first) .reference-issue`).Attr("data-reference")
	assert.Equal(t, "/user2/repo1/issues/1#issuecomment-2", ref)
}

func TestGetContentHistory(t *testing.T) {
	defer unittest.OverrideFixtures("tests/integration/fixtures/TestGetContentHistory")()
	defer tests.PrepareTestEnv(t)()

	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})
	issueURL := fmt.Sprintf("%s/issues/%d", repo.FullName(), issue.Index)
	contentHistory := unittest.AssertExistsAndLoadBean(t, &issues_model.ContentHistory{ID: 2, IssueID: issue.ID})
	contentHistoryURL := fmt.Sprintf("%s/issues/%d/content-history/detail?comment_id=%d&history_id=%d", repo.FullName(), issue.Index, contentHistory.CommentID, contentHistory.ID)

	type contentHistoryResp struct {
		CanSoftDelete bool `json:"canSoftDelete"`
		HistoryID     int  `json:"historyId"`
		PrevHistoryID int  `json:"prevHistoryId"`
	}

	testCase := func(t *testing.T, session *TestSession, canDelete bool) {
		t.Helper()
		contentHistoryURL := contentHistoryURL + "&_csrf=" + GetCSRF(t, session, issueURL)

		req := NewRequest(t, "GET", contentHistoryURL)
		resp := session.MakeRequest(t, req, http.StatusOK)

		var respJSON contentHistoryResp
		DecodeJSON(t, resp, &respJSON)

		assert.Equal(t, canDelete, respJSON.CanSoftDelete)
		assert.EqualValues(t, contentHistory.ID, respJSON.HistoryID)
		assert.EqualValues(t, contentHistory.ID-1, respJSON.PrevHistoryID)
	}

	t.Run("Anonymous", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		testCase(t, emptyTestSession(t), false)
	})

	t.Run("Another user", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		testCase(t, loginUser(t, "user8"), false)
	})

	t.Run("Repo owner", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		testCase(t, loginUser(t, "user2"), true)
	})

	t.Run("Poster", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		testCase(t, loginUser(t, "user5"), true)
	})
}

func TestCommitRefComment(t *testing.T) {
	defer unittest.OverrideFixtures("tests/integration/fixtures/TestCommitRefComment")()
	defer tests.PrepareTestEnv(t)()

	t.Run("Pull request", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/pulls/2")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		event := htmlDoc.Find("#issuecomment-1000 .text").Text()
		assert.Contains(t, event, "referenced this pull request")
	})

	t.Run("Issue", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/issues/1")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		event := htmlDoc.Find("#issuecomment-1001 .text").Text()
		assert.Contains(t, event, "referenced this issue")
	})
}

func TestIssueFilterNoFollow(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	// Check that every link in the filter list has rel="nofollow".
	t.Run("Issue lists", func(t *testing.T) {
		req := NewRequest(t, "GET", "/user2/repo1/issues")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		filterLinks := htmlDoc.Find(".issue-list-toolbar-right a[href*=\"?q=\"], .labels-list a[href*=\"?q=\"]")
		assert.Positive(t, filterLinks.Length())
		filterLinks.Each(func(i int, link *goquery.Selection) {
			rel, has := link.Attr("rel")
			assert.True(t, has)
			assert.Equal(t, "nofollow", rel)
		})
	})

	t.Run("Issue page", func(t *testing.T) {
		req := NewRequest(t, "GET", "/user2/repo1/issues/1")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		filterLinks := htmlDoc.Find(".timeline .labels-list a[href*=\"?labels=\"], .issue-content-right .labels-list a[href*=\"?labels=\"]")
		assert.Positive(t, filterLinks.Length())
		filterLinks.Each(func(i int, link *goquery.Selection) {
			rel, has := link.Attr("rel")
			assert.True(t, has)
			assert.Equal(t, "nofollow", rel)
		})
	})
}

func TestIssueForm(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		session := loginUser(t, user2.Name)
		repo, _, f := tests.CreateDeclarativeRepo(t, user2, "",
			[]unit_model.Type{unit_model.TypeCode, unit_model.TypeIssues}, nil,
			[]*files_service.ChangeRepoFile{
				{
					Operation: "create",
					TreePath:  ".forgejo/issue_template/test.yaml",
					ContentReader: strings.NewReader(`name: Test
about: Hello World
body:
  - type: checkboxes
    id: test
    attributes:
      label: Test
      options:
        - label: This is a label
`),
				},
			},
		)
		defer f()

		t.Run("Choose list", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", repo.Link()+"/issues/new/choose")
			resp := session.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			htmlDoc.AssertElement(t, "a[href$='/issues/new?template=.forgejo%2fissue_template%2ftest.yaml']", true)
		})

		t.Run("Issue template", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", repo.Link()+"/issues/new?template=.forgejo%2fissue_template%2ftest.yaml")
			resp := session.MakeRequest(t, req, http.StatusOK)
			htmlDoc := NewHTMLParser(t, resp.Body)

			htmlDoc.AssertElement(t, "#new-issue .field .ui.checkbox input[name='form-field-test-0']", true)
			checkboxLabel := htmlDoc.Find("#new-issue .field .ui.checkbox label").Text()
			assert.Contains(t, checkboxLabel, "This is a label")
		})
	})
}

func TestIssueUnsubscription(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, u *url.URL) {
		defer tests.PrepareTestEnv(t)()

		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
		repo, _, f := tests.CreateDeclarativeRepoWithOptions(t, user, tests.DeclarativeRepoOptions{
			AutoInit: optional.Some(false),
		})
		defer f()
		session := loginUser(t, user.Name)

		issueURL := testNewIssue(t, session, user.Name, repo.Name, "Issue title", "Description")
		req := NewRequestWithValues(t, "POST", fmt.Sprintf("%s/watch", issueURL), map[string]string{
			"_csrf": GetCSRF(t, session, issueURL),
			"watch": "0",
		})
		session.MakeRequest(t, req, http.StatusOK)
	})
}

func TestIssueLabelList(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	// The label list should always be present. When no labels are selected, .no-select is visible, otherwise hidden.
	labelListSelector := ".labels.list .labels-list"
	hiddenClass := "tw-hidden"

	t.Run("Test label list", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/issues/1")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		htmlDoc.AssertElement(t, labelListSelector, true)
		htmlDoc.AssertElement(t, ".labels.list .no-select."+hiddenClass, true)
	})
}

func TestIssueUserDashboard(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	session := loginUser(t, user.Name)

	// assert 'created_by' is the default filter
	const sel = ".dashboard .ui.list-header.dropdown .ui.menu a.active.item[href^='?type=created_by']"

	for _, path := range []string{"/issues", "/pulls"} {
		req := NewRequest(t, "GET", path)
		resp := session.MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)
		htmlDoc.AssertElement(t, sel, true)
	}
}

func TestIssueOrgDashboard(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	session := loginUser(t, user.Name)

	// assert 'your_repositories' is the default filter for org dashboards
	const sel = ".dashboard .ui.list-header.dropdown .ui.menu a.active.item[href^='?type=your_repositories']"

	for _, path := range []string{"/org/org3/issues", "/org/org3/pulls"} {
		req := NewRequest(t, "GET", path)
		resp := session.MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)
		htmlDoc.AssertElement(t, sel, true)
	}
}

func TestIssueCount(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	req := NewRequest(t, "GET", "/user2/repo1/issues")
	resp := MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)

	openCount := htmlDoc.doc.Find("a[data-test-name='open-issue-count']").Text()
	assert.Contains(t, openCount, "1\u00a0Open")

	closedCount := htmlDoc.doc.Find("a[data-test-name='closed-issue-count']").Text()
	assert.Contains(t, closedCount, "1\u00a0Closed")

	allCount := htmlDoc.doc.Find("a[data-test-name='all-issue-count']").Text()
	assert.Contains(t, allCount, "2\u00a0All")
}

func TestIssueDefaultValues(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	links := []string{"/user2/repo1/issues", "/user2/repo1/pulls"}
	values := []url.Values{
		{
			"type": {"created_by"},
		},
		{
			"poster": {"1"},
		},
		{
			"sort": {"latest"},
		},
		{
			"type":   {"all"},
			"sort":   {"latest"},
			"poster": {"1"},
		},
		{
			"type":   {"assigned"},
			"sort":   {"oldest"},
			"poster": {"1"},
		},
	}

	for _, link := range links {
		t.Run(link[13:], func(t *testing.T) {
			for _, value := range values {
				req := NewRequestf(t, "GET", "%s?%s", link, value.Encode())
				resp := MakeRequest(t, req, http.StatusOK)

				htmlDoc := NewHTMLParser(t, resp.Body)
				for name := range value {
					assert.Equal(t, value.Get(name), htmlDoc.GetInputValueByName(name))
				}
			}
		})
	}
}

func TestIssuePostersSearch(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	type userSearchInfo struct {
		UserID   int64  `json:"user_id"`
		UserName string `json:"username"`
	}

	type userSearchResponse struct {
		Results []*userSearchInfo `json:"results"`
	}

	t.Run("Name search", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		defer test.MockVariableValue(&setting.UI.DefaultShowFullName, false)()

		req := NewRequest(t, "GET", "/user2/repo1/issues/posters?q=USer2")
		resp := MakeRequest(t, req, http.StatusOK)

		var data userSearchResponse
		DecodeJSON(t, resp, &data)

		assert.Len(t, data.Results, 1)
		assert.Equal(t, "user2", data.Results[0].UserName)
		assert.EqualValues(t, 2, data.Results[0].UserID)
	})

	t.Run("Full name search", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		defer test.MockVariableValue(&setting.UI.DefaultShowFullName, true)()

		req := NewRequest(t, "GET", "/user2/repo1/issues/posters?q=OnE")
		resp := MakeRequest(t, req, http.StatusOK)

		var data userSearchResponse
		DecodeJSON(t, resp, &data)

		assert.Len(t, data.Results, 1)
		assert.Equal(t, "user1", data.Results[0].UserName)
		assert.EqualValues(t, 1, data.Results[0].UserID)
	})
}
