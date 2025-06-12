// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	gitea_context "forgejo.org/services/context"
	"forgejo.org/services/webhook"
	"forgejo.org/tests"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
)

func TestNewWebHookLink(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user2")

	webhooksLen := len(webhook.List())
	baseurl := "/user2/repo1/settings/hooks"
	tests := []string{
		// webhook list page
		baseurl,
		// new webhook page
		baseurl + "/gitea/new",
		// edit webhook page
		baseurl + "/1",
	}

	var csrfToken string
	for _, url := range tests {
		resp := session.MakeRequest(t, NewRequest(t, "GET", url), http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)
		assert.Equal(t,
			webhooksLen,
			htmlDoc.Find(`a[href^="`+baseurl+`/"][href$="/new"]`).Length(),
			"not all webhooks are listed in the 'new' dropdown")

		csrfToken = htmlDoc.GetCSRF()
	}

	// ensure that the "failure" pages has the full dropdown as well
	resp := session.MakeRequest(t, NewRequestWithValues(t, "POST", baseurl+"/gitea/new", map[string]string{"_csrf": csrfToken}), http.StatusUnprocessableEntity)
	htmlDoc := NewHTMLParser(t, resp.Body)
	assert.Equal(t,
		webhooksLen,
		htmlDoc.Find(`a[href^="`+baseurl+`/"][href$="/new"]`).Length(),
		"not all webhooks are listed in the 'new' dropdown on failure")

	resp = session.MakeRequest(t, NewRequestWithValues(t, "POST", baseurl+"/1", map[string]string{"_csrf": csrfToken}), http.StatusUnprocessableEntity)
	htmlDoc = NewHTMLParser(t, resp.Body)
	assert.Equal(t,
		webhooksLen,
		htmlDoc.Find(`a[href^="`+baseurl+`/"][href$="/new"]`).Length(),
		"not all webhooks are listed in the 'new' dropdown on failure")

	adminSession := loginUser(t, "user1")
	t.Run("org3", func(t *testing.T) {
		baseurl := "/org/org3/settings/hooks"
		resp := adminSession.MakeRequest(t, NewRequest(t, "GET", baseurl), http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)
		assert.Equal(t,
			webhooksLen,
			htmlDoc.Find(`a[href^="`+baseurl+`/"][href$="/new"]`).Length(),
			"not all webhooks are listed in the 'new' dropdown")
	})
	t.Run("admin", func(t *testing.T) {
		baseurl := "/admin/hooks"
		resp := adminSession.MakeRequest(t, NewRequest(t, "GET", baseurl), http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)
		assert.Equal(t,
			webhooksLen,
			htmlDoc.Find(`a[href^="/admin/default-hooks/"][href$="/new"]`).Length(),
			"not all webhooks are listed in the 'new' dropdown for default-hooks")
		assert.Equal(t,
			webhooksLen,
			htmlDoc.Find(`a[href^="/admin/system-hooks/"][href$="/new"]`).Length(),
			"not all webhooks are listed in the 'new' dropdown for system-hooks")
	})
}

func TestWebhookForms(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user1")

	t.Run("forgejo/required", testWebhookForms("forgejo", session, map[string]string{
		"payload_url":  "https://forgejo.example.com",
		"http_method":  "POST",
		"content_type": "1", // json
	}, map[string]string{
		"payload_url": "",
	}, map[string]string{
		"http_method": "",
	}, map[string]string{
		"content_type": "",
	}, map[string]string{
		"payload_url": "invalid_url",
	}, map[string]string{
		"http_method": "INVALID",
	}))
	t.Run("forgejo/optional", testWebhookForms("forgejo", session, map[string]string{
		"payload_url":  "https://forgejo.example.com",
		"http_method":  "POST",
		"content_type": "1", // json
		"secret":       "s3cr3t",

		"branch_filter":        "forgejo/*",
		"authorization_header": "Bearer 123456",
	}))

	t.Run("gitea/required", testWebhookForms("gitea", session, map[string]string{
		"payload_url":  "https://gitea.example.com",
		"http_method":  "POST",
		"content_type": "1", // json
	}, map[string]string{
		"payload_url": "",
	}, map[string]string{
		"http_method": "",
	}, map[string]string{
		"content_type": "",
	}, map[string]string{
		"payload_url": "invalid_url",
	}, map[string]string{
		"http_method": "INVALID",
	}))
	t.Run("gitea/optional", testWebhookForms("gitea", session, map[string]string{
		"payload_url":  "https://gitea.example.com",
		"http_method":  "POST",
		"content_type": "1", // json
		"secret":       "s3cr3t",

		"branch_filter":        "gitea/*",
		"authorization_header": "Bearer 123456",
	}))

	t.Run("gogs/required", testWebhookForms("gogs", session, map[string]string{
		"payload_url":  "https://gogs.example.com",
		"content_type": "1", // json
	}))
	t.Run("gogs/optional", testWebhookForms("gogs", session, map[string]string{
		"payload_url":  "https://gogs.example.com",
		"content_type": "1", // json
		"secret":       "s3cr3t",

		"branch_filter":        "gogs/*",
		"authorization_header": "Bearer 123456",
	}))

	t.Run("slack/required", testWebhookForms("slack", session, map[string]string{
		"payload_url": "https://slack.example.com",
		"channel":     "general",
	}, map[string]string{
		"channel": "",
	}, map[string]string{
		"channel": "invalid channel name",
	}))
	t.Run("slack/optional", testWebhookForms("slack", session, map[string]string{
		"payload_url": "https://slack.example.com",
		"channel":     "#general",
		"username":    "john",
		"icon_url":    "https://slack.example.com/icon.png",
		"color":       "#dd4b39",

		"branch_filter":        "slack/*",
		"authorization_header": "Bearer 123456",
	}))

	t.Run("discord/required", testWebhookForms("discord", session, map[string]string{
		"username":    "john",
		"payload_url": "https://discord.example.com",
	}))
	t.Run("discord/optional", testWebhookForms("discord", session, map[string]string{
		"payload_url": "https://discord.example.com",
		"username":    "john",
		"icon_url":    "https://discord.example.com/icon.png",

		"branch_filter":        "discord/*",
		"authorization_header": "Bearer 123456",
	}))

	t.Run("dingtalk/required", testWebhookForms("dingtalk", session, map[string]string{
		"payload_url": "https://dingtalk.example.com",
	}))
	t.Run("dingtalk/optional", testWebhookForms("dingtalk", session, map[string]string{
		"payload_url": "https://dingtalk.example.com",

		"branch_filter":        "discord/*",
		"authorization_header": "Bearer 123456",
	}))

	t.Run("telegram/required", testWebhookForms("telegram", session, map[string]string{
		"bot_token": "123456",
		"chat_id":   "789",
	}))
	t.Run("telegram/optional", testWebhookForms("telegram", session, map[string]string{
		"bot_token": "123456",
		"chat_id":   "789",
		"thread_id": "abc",

		"branch_filter":        "telegram/*",
		"authorization_header": "Bearer 123456",
	}))

	t.Run("msteams/required", testWebhookForms("msteams", session, map[string]string{
		"payload_url": "https://msteams.example.com",
	}))
	t.Run("msteams/optional", testWebhookForms("msteams", session, map[string]string{
		"payload_url": "https://msteams.example.com",

		"branch_filter":        "msteams/*",
		"authorization_header": "Bearer 123456",
	}))

	t.Run("feishu/required", testWebhookForms("feishu", session, map[string]string{
		"payload_url": "https://feishu.example.com",
	}))
	t.Run("feishu/optional", testWebhookForms("feishu", session, map[string]string{
		"payload_url": "https://feishu.example.com",

		"branch_filter":        "feishu/*",
		"authorization_header": "Bearer 123456",
	}))

	t.Run("matrix/required", testWebhookForms("matrix", session, map[string]string{
		"homeserver_url": "https://matrix.example.com",
		"access_token":   "123456",
		"room_id":        "123",
	}, map[string]string{
		"access_token": "",
	}))
	t.Run("matrix/optional", testWebhookForms("matrix", session, map[string]string{
		"homeserver_url": "https://matrix.example.com",
		"access_token":   "123456",
		"room_id":        "123",
		"message_type":   "1", // m.text

		"branch_filter": "matrix/*",
	}))

	t.Run("wechatwork/required", testWebhookForms("wechatwork", session, map[string]string{
		"payload_url": "https://wechatwork.example.com",
	}))
	t.Run("wechatwork/optional", testWebhookForms("wechatwork", session, map[string]string{
		"payload_url": "https://wechatwork.example.com",

		"branch_filter":        "wechatwork/*",
		"authorization_header": "Bearer 123456",
	}))

	t.Run("packagist/required", testWebhookForms("packagist", session, map[string]string{
		"username":    "john",
		"api_token":   "secret",
		"package_url": "https://packagist.org/packages/example/framework",
	}))
	t.Run("packagist/optional", testWebhookForms("packagist", session, map[string]string{
		"username":    "john",
		"api_token":   "secret",
		"package_url": "https://packagist.org/packages/example/framework",

		"branch_filter":        "packagist/*",
		"authorization_header": "Bearer 123456",
	}))

	t.Run("sourcehut_builds/required", testWebhookForms("sourcehut_builds", session, map[string]string{
		"payload_url":   "https://sourcehut_builds.example.com",
		"manifest_path": ".build.yml",
		"visibility":    "PRIVATE",
		"access_token":  "123456",
	}, map[string]string{
		"access_token": "",
	}, map[string]string{
		"manifest_path": "",
	}, map[string]string{
		"manifest_path": "/absolute",
	}, map[string]string{
		"visibility": "",
	}, map[string]string{
		"visibility": "INVALID",
	}))
	t.Run("sourcehut_builds/optional", testWebhookForms("sourcehut_builds", session, map[string]string{
		"payload_url":   "https://sourcehut_builds.example.com",
		"manifest_path": ".build.yml",
		"visibility":    "PRIVATE",
		"secrets":       "on",
		"access_token":  "123456",

		"branch_filter": "srht/*",
	}))
}

func assertInput(t testing.TB, form *goquery.Selection, name string) string {
	t.Helper()
	input := form.Find(`input[name="` + name + `"]`)
	if input.Length() != 1 {
		form.Find("input").Each(func(i int, s *goquery.Selection) {
			t.Logf("found <input name=%q />", s.AttrOr("name", ""))
		})
		t.Errorf("field <input name=%q /> found %d times, expected once", name, input.Length())
	}
	switch input.AttrOr("type", "") {
	case "checkbox":
		if _, checked := input.Attr("checked"); checked {
			return "on"
		}
		return ""
	default:
		return input.AttrOr("value", "")
	}
}

func testWebhookForms(name string, session *TestSession, validFields map[string]string, invalidPatches ...map[string]string) func(t *testing.T) {
	return func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		t.Run("repo1", func(t *testing.T) {
			testWebhookFormsShared(t, "/user2/repo1/settings/hooks", name, session, validFields, invalidPatches...)
		})
		t.Run("org3", func(t *testing.T) {
			testWebhookFormsShared(t, "/org/org3/settings/hooks", name, session, validFields, invalidPatches...)
		})
		t.Run("system", func(t *testing.T) {
			testWebhookFormsShared(t, "/admin/system-hooks", name, session, validFields, invalidPatches...)
		})
		t.Run("default", func(t *testing.T) {
			testWebhookFormsShared(t, "/admin/default-hooks", name, session, validFields, invalidPatches...)
		})
	}
}

func testWebhookFormsShared(t *testing.T, endpoint, name string, session *TestSession, validFields map[string]string, invalidPatches ...map[string]string) {
	// new webhook form
	resp := session.MakeRequest(t, NewRequest(t, "GET", endpoint+"/"+name+"/new"), http.StatusOK)
	htmlForm := NewHTMLParser(t, resp.Body).Find(`form[action^="` + endpoint + `/"]`)

	testWebhookFormsSharedChooseEvents(t, htmlForm)

	// fill the form
	payload := map[string]string{
		"_csrf":  htmlForm.Find(`input[name="_csrf"]`).AttrOr("value", ""),
		"events": "send_everything",
	}
	for k, v := range validFields {
		assertInput(t, htmlForm, k)
		payload[k] = v
	}
	if t.Failed() {
		t.FailNow() // prevent further execution if the form could not be filled properly
	}

	// create the webhook (this redirects back to the hook list)
	resp = session.MakeRequest(t, NewRequestWithValues(t, "POST", endpoint+"/"+name+"/new", payload), http.StatusSeeOther)
	assertHasFlashMessages(t, resp, "success")
	listEndpoint := resp.Header().Get("Location")
	updateEndpoint := endpoint + "/"
	if endpoint == "/admin/system-hooks" || endpoint == "/admin/default-hooks" {
		updateEndpoint = "/admin/hooks/"
	}

	// find last created hook in the hook list
	// (a bit hacky, but the list should be sorted)
	resp = session.MakeRequest(t, NewRequest(t, "GET", listEndpoint), http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)
	selector := `a[href^="` + updateEndpoint + `"]`
	if endpoint == "/admin/system-hooks" {
		// system-hooks and default-hooks are listed on the same page
		// add a specifier to select the latest system-hooks
		// (the default-hooks are at the end, so no further specifier needed)
		selector = `.admin-setting-content > div:first-of-type ` + selector
	}
	editFormURL := htmlDoc.Find(selector).Last().AttrOr("href", "")
	assert.NotEmpty(t, editFormURL)

	// edit webhook form
	resp = session.MakeRequest(t, NewRequest(t, "GET", editFormURL), http.StatusOK)
	htmlForm = NewHTMLParser(t, resp.Body).Find(`form[action^="` + updateEndpoint + `"]`)
	editPostURL := htmlForm.AttrOr("action", "")
	assert.NotEmpty(t, editPostURL)

	// fill the form
	payload = map[string]string{
		"_csrf":  htmlForm.Find(`input[name="_csrf"]`).AttrOr("value", ""),
		"events": "push_only",
	}
	for k, v := range validFields {
		assert.Equal(t, v, assertInput(t, htmlForm, k), "input %q did not contain value %q", k, v)
		payload[k] = v
	}

	// update the webhook
	resp = session.MakeRequest(t, NewRequestWithValues(t, "POST", editPostURL, payload), http.StatusSeeOther)
	assertHasFlashMessages(t, resp, "success")

	// check the updated webhook
	resp = session.MakeRequest(t, NewRequest(t, "GET", editFormURL), http.StatusOK)
	htmlForm = NewHTMLParser(t, resp.Body).Find(`form[action^="` + updateEndpoint + `"]`)
	for k, v := range validFields {
		assert.Equal(t, v, assertInput(t, htmlForm, k), "input %q did not contain value %q", k, v)
	}

	if len(invalidPatches) > 0 {
		// check that invalid fields are rejected
		resp := session.MakeRequest(t, NewRequest(t, "GET", endpoint+"/"+name+"/new"), http.StatusOK)
		htmlForm := NewHTMLParser(t, resp.Body).Find(`form[action^="` + endpoint + `/"]`)

		for _, invalidPatch := range invalidPatches {
			t.Run("invalid", func(t *testing.T) {
				// fill the form
				payload := map[string]string{
					"_csrf":  htmlForm.Find(`input[name="_csrf"]`).AttrOr("value", ""),
					"events": "send_everything",
				}
				for k, v := range validFields {
					payload[k] = v
				}
				for k, v := range invalidPatch {
					if v == "" {
						delete(payload, k)
					} else {
						payload[k] = v
					}
				}

				resp := session.MakeRequest(t, NewRequestWithValues(t, "POST", endpoint+"/"+name+"/new", payload), http.StatusUnprocessableEntity)
				// check that the invalid form is pre-filled
				htmlForm = NewHTMLParser(t, resp.Body).Find(`form[action^="` + endpoint + `/"]`)
				for k, v := range payload {
					if k == "_csrf" || k == "events" || v == "" {
						// the 'events' is a radio input, which is buggy below
						continue
					}
					assert.Equal(t, v, assertInput(t, htmlForm, k), "input %q did not contain value %q", k, v)
				}
				if t.Failed() {
					t.Log(invalidPatch)
				}
			})
		}
	}
}

func assertHasFlashMessages(t *testing.T, resp *httptest.ResponseRecorder, expectedKeys ...string) {
	seenKeys := make(map[string][]string, len(expectedKeys))

	for _, cookie := range resp.Result().Cookies() {
		if cookie.Name != gitea_context.CookieNameFlash {
			continue
		}
		flash, _ := url.ParseQuery(cookie.Value)
		for key, value := range flash {
			// the key is itself url-encoded
			if flash, err := url.ParseQuery(key); err == nil {
				for key, value := range flash {
					seenKeys[key] = value
				}
			} else {
				seenKeys[key] = value
			}
		}
	}

	for _, k := range expectedKeys {
		if len(seenKeys[k]) == 0 {
			t.Errorf("missing expected flash message %q", k)
		}
		delete(seenKeys, k)
	}

	for k, v := range seenKeys {
		t.Errorf("unexpected flash message %q: %q", k, v)
	}
}

func testWebhookFormsSharedChooseEvents(t *testing.T, htmlForm *goquery.Selection) {
	webhookTypes := []string{
		"create",
		"delete",
		"fork",
		"push",
		"repository",
		"release",
		"package",
		"wiki",
		"issues",
		"issue_assign",
		"issue_label",
		"issue_milestone",
		"issue_comment",
		"pull_request",
		"pull_request_assign",
		"pull_request_label",
		"pull_request_milestone",
		"pull_request_comment",
		"pull_request_review",
		"pull_request_sync",
		"pull_request_review_request",
		"action_failure",
		"action_recover",
		"action_success",
	}

	// check all types of webhooks are present in the form
	for _, webhookType := range webhookTypes {
		assertInput(t, htmlForm, webhookType)
	}
}
