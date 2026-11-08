// Copyright 2024 The Forgejo Authors c/o Codeberg e.V.. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIIssueTemplateList(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user := forgery.CreateUser(t, nil)
		repo := forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{
			Files: forgery.FilesInit{},
		})

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
				"docs/issue_template/test.md",
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
				"docs/issue_template/test.md",
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
			assert.Len(t, issueTemplates, 4)
		})

		t.Run("oversized templates are skipped", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			defer test.MockVariableValue(&setting.UI.MaxDisplayFileSize, 1000)()

			templates := []string{
				".forgejo/issue_template/small.md",
				".gitea/issue_template/big.md",
			}
			defer func() {
				for _, template := range templates {
					deleteFileInBranch(user, repo, template, repo.DefaultBranch)
				}
			}()

			for _, template := range templates {
				content := `---
				name: 'Template Name'
				about: 'This template is for testing!'
				title: '[TEST] '
				ref: 'main'
				---

				This is the template!`

				if strings.HasSuffix(template, "big.md") {
					content += strings.Repeat("lorem ipsum ", 100)
				}
				err := createOrReplaceFileInBranch(user, repo, template, repo.DefaultBranch, content)
				require.NoError(t, err)
			}

			req := NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/%s/issue_templates", repo.FullName()))
			resp := MakeRequest(t, req, http.StatusOK)
			var issueTemplates []*api.IssueTemplate
			DecodeJSON(t, resp, &issueTemplates)

			assert.Len(t, issueTemplates, 1)
			assert.Equal(t, ".forgejo/issue_template/small.md", issueTemplates[0].FileName)
		})
	})
}
