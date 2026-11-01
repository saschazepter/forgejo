// Copyright 2024  The Forgejo Authors c/o Codeberg e.V.. All rights reserved.
// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	webhook_model "forgejo.org/models/webhook"
	"forgejo.org/modules/git"
	"forgejo.org/modules/json"
	"forgejo.org/modules/log"
	"forgejo.org/modules/svg"
	webhook_module "forgejo.org/modules/webhook"
	"forgejo.org/services/forms"
	"forgejo.org/services/webhook/shared"
)

var _ Handler = defaultHandler{}

type defaultHandler struct {
	forgejo bool
}

func (defaultHandler) Available(webhook_module.Scope) bool { return true }

func (dh defaultHandler) Type() webhook_module.HookType {
	if dh.forgejo {
		return webhook_module.FORGEJO
	}
	return webhook_module.GITEA
}

func (dh defaultHandler) Icon(size int) template.HTML {
	if dh.forgejo {
		return svg.RenderHTML("gitea-forgejo", size, "img")
	}
	return svg.RenderHTML("gitea-gitea", size, "img")
}

func (defaultHandler) Metadata(*webhook_model.Webhook) any { return nil }

func (defaultHandler) UnmarshalForm(bind func(any)) forms.WebhookForm {
	var form struct {
		forms.WebhookCoreForm
		PayloadURL  string `binding:"Required;ValidUrl"`
		HTTPMethod  string `binding:"Required;In(POST,GET)"`
		ContentType int    `binding:"Required"`
		Secret      string
	}
	bind(&form)

	contentType := webhook_model.ContentTypeJSON
	if webhook_model.HookContentType(form.ContentType) == webhook_model.ContentTypeForm {
		contentType = webhook_model.ContentTypeForm
	}
	return forms.WebhookForm{
		WebhookCoreForm: form.WebhookCoreForm,
		URL:             form.PayloadURL,
		ContentType:     contentType,
		Secret:          form.Secret,
		HTTPMethod:      form.HTTPMethod,
		Metadata:        nil,
	}
}

func (defaultHandler) NewRequest(ctx context.Context, w *webhook_model.Webhook, t *webhook_model.HookTask) (req *http.Request, body []byte, err error) {
	payloadContent := t.PayloadContent
	if w.Type == webhook_module.GITEA &&
		(t.EventType == webhook_module.HookEventCreate || t.EventType == webhook_module.HookEventDelete) {
		// Woodpecker expects the ref to be short on tag creation only
		// https://github.com/woodpecker-ci/woodpecker/blob/00ccec078cdced80cf309cd4da460a5041d7991a/server/forge/gitea/helper.go#L134
		// see https://codeberg.org/codeberg/community/issues/1556
		payloadContent, err = substituteRefShortName(payloadContent)
		if err != nil {
			return nil, nil, fmt.Errorf("could not substitute ref: %w", err)
		}
	}

	switch w.HTTPMethod {
	case "":
		log.Info("HTTP Method for %s webhook %s [ID: %d] is not set, defaulting to POST", w.Type, w.URL, w.ID)
		fallthrough
	case http.MethodPost:
		switch w.ContentType {
		case webhook_model.ContentTypeJSON:
			req, err = http.NewRequest("POST", w.URL, strings.NewReader(payloadContent))
			if err != nil {
				return nil, nil, err
			}

			req.Header.Set("Content-Type", "application/json")
		case webhook_model.ContentTypeForm:
			forms := url.Values{
				"payload": []string{payloadContent},
			}

			req, err = http.NewRequest("POST", w.URL, strings.NewReader(forms.Encode()))
			if err != nil {
				return nil, nil, err
			}

			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		default:
			return nil, nil, fmt.Errorf("invalid content type: %v", w.ContentType)
		}
	case http.MethodGet:
		u, err := url.Parse(w.URL)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid URL: %w", err)
		}
		vals := u.Query()
		vals["payload"] = []string{payloadContent}
		u.RawQuery = vals.Encode()
		req, err = http.NewRequest("GET", u.String(), nil)
		if err != nil {
			return nil, nil, err
		}
	case http.MethodPut:
		switch w.Type {
		case webhook_module.MATRIX: // used when t.Version == 1
			stateKey := getMatrixStateKey(t)
			url := fmt.Sprintf("%s/%s", w.URL, stateKey)
			req, err = http.NewRequest("PUT", url, strings.NewReader(payloadContent))
			if err != nil {
				return nil, nil, err
			}
		default:
			return nil, nil, fmt.Errorf("invalid http method: %v", w.HTTPMethod)
		}
	default:
		return nil, nil, fmt.Errorf("invalid http method: %v", w.HTTPMethod)
	}

	body = []byte(payloadContent)
	return req, body, shared.AddDefaultHeaders(req, []byte(w.Secret), t, body)
}

func substituteRefShortName(body string) (string, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return body, err
	}
	ref, ok := m["ref"].(string)
	if !ok {
		return body, fmt.Errorf("expected string 'ref', got %T", m["ref"])
	}

	m["ref"] = git.RefName(ref).ShortName()

	buf, err := json.Marshal(m)
	return string(buf), err
}
