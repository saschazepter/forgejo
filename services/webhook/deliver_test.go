// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package webhook

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"forgejo.org/models/db"
	"forgejo.org/models/unittest"
	webhook_model "forgejo.org/models/webhook"
	"forgejo.org/modules/hostmatcher"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	webhook_module "forgejo.org/modules/webhook"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhookProxy(t *testing.T) {
	defer test.MockProtect(&setting.Webhook)()
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")

	setting.Webhook.ProxyURL = "http://localhost:8080"
	setting.Webhook.ProxyURLFixed, _ = url.Parse(setting.Webhook.ProxyURL)
	setting.Webhook.ProxyHosts = []string{"*.discordapp.com", "discordapp.com"}

	allowedHostMatcher := hostmatcher.ParseHostMatchList("webhook.ALLOWED_HOST_LIST", "discordapp.com,s.discordapp.com")

	tests := []struct {
		req     string
		want    string
		wantErr bool
	}{
		{
			req:     "https://discordapp.com/api/webhooks/xxxxxxxxx/xxxxxxxxxxxxxxxxxxx",
			want:    "http://localhost:8080",
			wantErr: false,
		},
		{
			req:     "http://s.discordapp.com/assets/xxxxxx",
			want:    "http://localhost:8080",
			wantErr: false,
		},
		{
			req:     "http://github.com/a/b",
			want:    "",
			wantErr: false,
		},
		{
			req:     "http://www.discordapp.com/assets/xxxxxx",
			want:    "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.req, func(t *testing.T) {
			req, err := http.NewRequest("POST", tt.req, nil)
			require.NoError(t, err)

			u, err := webhookProxy(allowedHostMatcher)(req)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			got := ""
			if u != nil {
				got = u.String()
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestWebhookDeliverAuthorizationHeader(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	done := make(chan struct{}, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/webhook", r.URL.Path)
		assert.Equal(t, "Bearer s3cr3t-t0ken", r.Header.Get("Authorization"))
		w.WriteHeader(200)
		done <- struct{}{}
	}))
	t.Cleanup(s.Close)

	hook := &webhook_model.Webhook{
		RepoID:      3,
		URL:         s.URL + "/webhook",
		ContentType: webhook_model.ContentTypeJSON,
		IsActive:    true,
		Type:        webhook_module.GITEA,
	}
	require.NoError(t, webhook_model.CreateWebhook(t.Context(), hook, "Bearer s3cr3t-t0ken"))

	hookTask := &webhook_model.HookTask{
		HookID:         hook.ID,
		EventType:      webhook_module.HookEventPush,
		PayloadVersion: 2,
	}

	hookTask, err := webhook_model.CreateHookTask(db.DefaultContext, hookTask)
	require.NoError(t, err)
	assert.NotNil(t, hookTask)

	require.NoError(t, Deliver(t.Context(), hookTask))
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("waited to long for request to happen")
	}

	assert.True(t, hookTask.IsSucceed)
	assert.Equal(t, "Bearer ******", hookTask.RequestInfo.Headers["Authorization"])
}

func TestWebhookDeliverHookTask(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	done := make(chan struct{}, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PUT", r.Method)
		switch r.URL.Path {
		case "/webhook/JVatD-35P2O8I7ZhYFUkW1FyGdJ7LA0GcLRMC03r02s":
			// Version 1
			assert.Equal(t, "push", r.Header.Get("X-GitHub-Event"))
			assert.Empty(t, r.Header.Get("Content-Type"))
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			assert.Equal(t, `{"data": 42}`, string(body))

		case "/webhook/OWkz2tnGWTev7X-soFlJGDJYtqaL_or14nskUb5Ath0":
			// Version 2
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			assert.Len(t, body, 1909)

		default:
			w.WriteHeader(404)
			t.Fatalf("unexpected url path %s", r.URL.Path)
			return
		}
		w.WriteHeader(200)
		done <- struct{}{}
	}))
	t.Cleanup(s.Close)

	hook := &webhook_model.Webhook{
		RepoID:      3,
		IsActive:    true,
		Type:        webhook_module.MATRIX,
		URL:         s.URL + "/webhook",
		HTTPMethod:  "PUT",
		ContentType: webhook_model.ContentTypeJSON,
		Meta:        `{"message_type":0}`, // text
	}
	require.NoError(t, webhook_model.CreateWebhook(db.DefaultContext, hook, ""))

	t.Run("Version 1", func(t *testing.T) {
		hookTask := &webhook_model.HookTask{
			HookID:         hook.ID,
			EventType:      webhook_module.HookEventPush,
			PayloadContent: `{"data": 42}`,
			PayloadVersion: 1,
		}

		hookTask, err := webhook_model.CreateHookTask(db.DefaultContext, hookTask)
		require.NoError(t, err)
		assert.NotNil(t, hookTask)

		require.NoError(t, Deliver(t.Context(), hookTask))
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("waited to long for request to happen")
		}

		assert.True(t, hookTask.IsSucceed)
	})

	t.Run("Version 2", func(t *testing.T) {
		p := pushTestPayload()
		data, err := p.JSONPayload()
		require.NoError(t, err)

		hookTask := &webhook_model.HookTask{
			HookID:         hook.ID,
			EventType:      webhook_module.HookEventPush,
			PayloadContent: string(data),
			PayloadVersion: 2,
		}

		hookTask, err = webhook_model.CreateHookTask(db.DefaultContext, hookTask)
		require.NoError(t, err)
		assert.NotNil(t, hookTask)

		require.NoError(t, Deliver(t.Context(), hookTask))
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("waited to long for request to happen")
		}

		assert.True(t, hookTask.IsSucceed)
	})
}

func TestWebhookDeliverSpecificTypes(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	type hookCase struct {
		gotBody        chan []byte
		expectedMethod string
	}

	cases := map[string]hookCase{
		webhook_module.SLACK: {
			gotBody: make(chan []byte, 1),
		},
		webhook_module.DISCORD: {
			gotBody: make(chan []byte, 1),
		},
		webhook_module.DINGTALK: {
			gotBody: make(chan []byte, 1),
		},
		webhook_module.TELEGRAM: {
			gotBody: make(chan []byte, 1),
		},
		webhook_module.MSTEAMS: {
			gotBody: make(chan []byte, 1),
		},
		webhook_module.FEISHU: {
			gotBody: make(chan []byte, 1),
		},
		webhook_module.MATRIX: {
			gotBody:        make(chan []byte, 1),
			expectedMethod: "PUT",
		},
		webhook_module.WECHATWORK: {
			gotBody: make(chan []byte, 1),
		},
		webhook_module.PACKAGIST: {
			gotBody: make(chan []byte, 1),
		},
	}

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"), r.URL.Path)

		typ := strings.Split(r.URL.Path, "/")[1] // take first segment (after skipping leading slash)
		hc := cases[typ]

		if hc.expectedMethod != "" {
			assert.Equal(t, hc.expectedMethod, r.Method, r.URL.Path)
		} else {
			assert.Equal(t, "POST", r.Method, r.URL.Path)
		}

		require.NotNil(t, hc.gotBody, r.URL.Path)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		w.WriteHeader(200)
		hc.gotBody <- body
	}))
	t.Cleanup(s.Close)

	p := pushTestPayload()
	data, err := p.JSONPayload()
	require.NoError(t, err)

	for typ, hc := range cases {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			hook := &webhook_model.Webhook{
				RepoID:      3,
				IsActive:    true,
				Type:        typ,
				URL:         s.URL + "/" + typ,
				HTTPMethod:  "", // should fallback to POST, when left unset by the specific hook
				ContentType: 0,  // set to 0 so that falling back to default request fails with "invalid content type"
				Meta:        "{}",
			}
			require.NoError(t, webhook_model.CreateWebhook(db.DefaultContext, hook, ""))

			hookTask := &webhook_model.HookTask{
				HookID:         hook.ID,
				EventType:      webhook_module.HookEventPush,
				PayloadContent: string(data),
				PayloadVersion: 2,
			}

			hookTask, err := webhook_model.CreateHookTask(db.DefaultContext, hookTask)
			require.NoError(t, err)
			assert.NotNil(t, hookTask)

			require.NoError(t, Deliver(t.Context(), hookTask))
			select {
			case gotBody := <-hc.gotBody:
				assert.NotEqual(t, string(data), string(gotBody), "request body must be different from the event payload")
				assert.Equal(t, hookTask.RequestInfo.Body, string(gotBody), "request body was not saved")
			case <-time.After(5 * time.Second):
				t.Fatal("waited to long for request to happen")
			}

			assert.True(t, hookTask.IsSucceed)
		})
	}
}
