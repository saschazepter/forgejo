// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/url"

	webhook_model "forgejo.org/models/webhook"
	"forgejo.org/modules/json"
	"forgejo.org/modules/log"
	webhook_module "forgejo.org/modules/webhook"
	"forgejo.org/services/forms"
	"forgejo.org/services/webhook/shared"
)

type packagistHandler struct{}

func (packagistHandler) Available(webhook_module.Scope) bool { return true }
func (packagistHandler) Type() webhook_module.HookType       { return webhook_module.PACKAGIST }
func (packagistHandler) Icon(size int) template.HTML         { return shared.ImgIcon("packagist.png", size) }

func (packagistHandler) UnmarshalForm(bind func(any)) forms.WebhookForm {
	var form struct {
		forms.WebhookCoreForm
		Username   string `binding:"Required"`
		APIToken   string `binding:"Required"`
		PackageURL string `binding:"Required;ValidUrl"`
	}
	bind(&form)

	return forms.WebhookForm{
		WebhookCoreForm: form.WebhookCoreForm,
		URL:             fmt.Sprintf("https://packagist.org/api/update-package?username=%s&apiToken=%s", url.QueryEscape(form.Username), url.QueryEscape(form.APIToken)),
		ContentType:     webhook_model.ContentTypeJSON,
		Secret:          "",
		HTTPMethod:      http.MethodPost,
		Metadata: &PackagistMeta{
			Username:   form.Username,
			APIToken:   form.APIToken,
			PackageURL: form.PackageURL,
		},
	}
}

type (
	// PackagistPayload represents a packagist payload
	// as expected by https://packagist.org/about
	PackagistPayload struct {
		PackagistRepository struct {
			URL string `json:"url"`
		} `json:"repository"`
	}

	// PackagistMeta contains the metadata for the webhook
	PackagistMeta struct {
		Username   string `json:"username"`
		APIToken   string `json:"api_token"`
		PackageURL string `json:"package_url"`
	}
)

// Metadata returns packagist metadata
func (packagistHandler) Metadata(w *webhook_model.Webhook) any {
	s := &PackagistMeta{}
	if err := json.Unmarshal([]byte(w.Meta), s); err != nil {
		log.Error("packagistHandler.Metadata(%d): %v", w.ID, err)
	}
	return s
}

// newPackagistRequest creates a request with the [PackagistPayload] for packagist (same payload for all events).
func (packagistHandler) NewRequest(ctx context.Context, w *webhook_model.Webhook, t *webhook_model.HookTask) (*http.Request, []byte, error) {
	meta := &PackagistMeta{}
	if err := json.Unmarshal([]byte(w.Meta), meta); err != nil {
		return nil, nil, fmt.Errorf("packagistHandler.NewRequest meta json: %w", err)
	}

	payload := PackagistPayload{
		PackagistRepository: struct {
			URL string `json:"url"`
		}{
			URL: meta.PackageURL,
		},
	}
	return shared.NewJSONRequestWithPayload(payload, w, t, false)
}
