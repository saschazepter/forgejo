// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package webhook

import (
	"fmt"
	"testing"

	webhook_model "forgejo.org/models/webhook"
	"forgejo.org/modules/json"
	"forgejo.org/modules/setting"
	webhook_module "forgejo.org/modules/webhook"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitPagesPayload(t *testing.T) {
	for _, x := range []struct {
		Name       string
		HookURL    string
		HookMeta   string
		HookAuthz  string
		ReqURLHost string
		ReqURLPath string
		ReqHdrHost string
		ReqAuthz   string
	}{
		{
			Name:       "Without Server",
			HookURL:    "https://whitequark.codeberg.page/project",
			HookMeta:   `{"server":""}`, // text
			ReqURLHost: "whitequark.codeberg.page",
			ReqHdrHost: "whitequark.codeberg.page",
			ReqURLPath: "/project",
		},
		{
			Name:       "With Server",
			HookURL:    "https://whitequark.codeberg.page/project",
			HookMeta:   `{"server":"codeberg.page:3000"}`, // text
			ReqURLHost: "codeberg.page:3000",
			ReqHdrHost: "whitequark.codeberg.page",
			ReqURLPath: "/project",
		},
		{
			Name:       "With Password",
			HookURL:    "https://example.org/",
			HookMeta:   `{"server":""}`, // text
			HookAuthz:  "Pages hunter2",
			ReqURLHost: "example.org",
			ReqHdrHost: "example.org",
			ReqAuthz:   "Pages hunter2",
			ReqURLPath: "/",
		},
	} {
		t.Run(x.Name, func(t *testing.T) {
			p := pushTestPayload()
			p.Ref = "refs/heads/pages"
			data, err := p.JSONPayload()
			require.NoError(t, err)

			hook := &webhook_model.Webhook{
				RepoID:   3,
				IsActive: true,
				Type:     webhook_module.GIT_PAGES,
				URL:      x.HookURL,
				Meta:     x.HookMeta,
			}
			hook.SetHeaderAuthorization(x.HookAuthz)
			task := &webhook_model.HookTask{
				HookID:         hook.ID,
				EventType:      webhook_module.HookEventPush,
				PayloadContent: string(data),
				PayloadVersion: 2,
			}

			req, reqBody, err := gitPagesHandler{}.NewRequest(t.Context(), hook, task)
			require.NotNil(t, req)
			require.NotNil(t, reqBody)
			require.NoError(t, err)

			assert.Equal(t, "POST", req.Method)
			assert.Equal(t, x.ReqURLHost, req.URL.Host)
			assert.Equal(t, x.ReqHdrHost, req.Host)
			assert.Equal(t, x.ReqURLPath, req.URL.Path)
			assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
			assert.Equal(t, "push", req.Header.Get("X-Forgejo-Event"))
			assert.Equal(t, x.ReqAuthz, req.Header.Get("Authorization"))
			assert.Equal(t, fmt.Sprint(setting.Webhook.DeliverTimeout), req.Header.Get("Deliver-Timeout"))
			var body struct {
				// These are the only bits that git-pages cares about.
				Ref        string `json:"ref"`
				Repository struct {
					CloneURL string `json:"clone_url"`
				} `json:"repository"`
			}
			err = json.NewDecoder(req.Body).Decode(&body)
			require.NoError(t, err)
			assert.Equal(t, "refs/heads/pages", body.Ref)
			assert.Equal(t, "http://localhost:3000/test/repo.git", body.Repository.CloneURL)
		})
	}
}
