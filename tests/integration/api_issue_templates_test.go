// Copyright 2024 The Forgejo Authors c/o Codeberg e.V.. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	api "forgejo.org/modules/structs"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIIssueTemplateList(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, u *url.URL) {
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

		t.Run("no templates", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/%s/issue_templates", repo.FullName()))
			resp := MakeRequest(t, req, http.StatusOK)
			var issueTemplates []*api.IssueTemplate
			DecodeJSON(t, resp, &issueTemplates)
			assert.Empty(t, issueTemplates)
		})

		t.Run("existing template", func(t *testing.T) {
			templateCandidates := []string{
				".forgejo/ISSUE_TEMPLATE/test.md",
				".forgejo/issue_template/test.md",
				".gitea/ISSUE_TEMPLATE/test.md",
				".gitea/issue_template/test.md",
				".github/ISSUE_TEMPLATE/test.md",
				".github/issue_template/test.md",
			}

			for _, template := range templateCandidates {
				t.Run(template, func(t *testing.T) {
					defer tests.PrintCurrentTest(t)()
					defer deleteFileInBranch(user, repo, template, repo.DefaultBranch)

					err := createOrReplaceFileInBranch(user, repo, template, repo.DefaultBranch,
						`---
name: 'Template Name'
about: 'This template is for testing!'
title: '[TEST] '
ref: 'main'
---

This is the template!`)
					require.NoError(t, err)

					req := NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/%s/issue_templates", repo.FullName()))
					resp := MakeRequest(t, req, http.StatusOK)
					var issueTemplates []*api.IssueTemplate
					DecodeJSON(t, resp, &issueTemplates)
					assert.Len(t, issueTemplates, 1)
					assert.Equal(t, "Template Name", issueTemplates[0].Name)
					assert.Equal(t, "This template is for testing!", issueTemplates[0].About)
					assert.Equal(t, "refs/heads/main", issueTemplates[0].Ref)
					assert.Equal(t, template, issueTemplates[0].FileName)
				})
			}
		})

		t.Run("multiple templates", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			templatePriority := []string{
				".forgejo/issue_template/test.md",
				".gitea/issue_template/test.md",
				".github/issue_template/test.md",
			}
			defer func() {
				for _, template := range templatePriority {
					deleteFileInBranch(user, repo, template, repo.DefaultBranch)
				}
			}()

			for _, template := range templatePriority {
				err := createOrReplaceFileInBranch(user, repo, template, repo.DefaultBranch,
					`---
name: 'Template Name'
about: 'This template is for testing!'
title: '[TEST] '
ref: 'main'
---

This is the template!`)
				require.NoError(t, err)
			}

			req := NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/%s/issue_templates", repo.FullName()))
			resp := MakeRequest(t, req, http.StatusOK)
			var issueTemplates []*api.IssueTemplate
			DecodeJSON(t, resp, &issueTemplates)

			// If templates have the same filename and content, but in different
			// directories, they count as different templates, and all are
			// considered.
			assert.Len(t, issueTemplates, 3)
		})
	})
}
