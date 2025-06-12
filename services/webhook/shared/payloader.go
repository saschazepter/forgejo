// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package shared

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"

	webhook_model "forgejo.org/models/webhook"
	"forgejo.org/modules/json"
	api "forgejo.org/modules/structs"
	webhook_module "forgejo.org/modules/webhook"
)

var ErrPayloadTypeNotSupported = errors.New("unsupported webhook event")

// PayloadConvertor defines the interface to convert system payload to webhook payload
type PayloadConvertor[T any] interface {
	Create(*api.CreatePayload) (T, error)
	Delete(*api.DeletePayload) (T, error)
	Fork(*api.ForkPayload) (T, error)
	Issue(*api.IssuePayload) (T, error)
	IssueComment(*api.IssueCommentPayload) (T, error)
	Push(*api.PushPayload) (T, error)
	PullRequest(*api.PullRequestPayload) (T, error)
	Review(*api.PullRequestPayload, webhook_module.HookEventType) (T, error)
	Repository(*api.RepositoryPayload) (T, error)
	Release(*api.ReleasePayload) (T, error)
	Wiki(*api.WikiPayload) (T, error)
	Package(*api.PackagePayload) (T, error)
	Action(*api.ActionPayload) (T, error)
}

func convertUnmarshalledJSON[T, P any](convert func(P) (T, error), data []byte) (T, error) {
	var p P
	if err := json.Unmarshal(data, &p); err != nil {
		var t T
		return t, fmt.Errorf("could not unmarshal payload: %w", err)
	}
	return convert(p)
}

func NewPayload[T any](rc PayloadConvertor[T], data []byte, event webhook_module.HookEventType) (T, error) {
	switch event {
	case webhook_module.HookEventCreate:
		return convertUnmarshalledJSON(rc.Create, data)
	case webhook_module.HookEventDelete:
		return convertUnmarshalledJSON(rc.Delete, data)
	case webhook_module.HookEventFork:
		return convertUnmarshalledJSON(rc.Fork, data)
	case webhook_module.HookEventIssues, webhook_module.HookEventIssueAssign, webhook_module.HookEventIssueLabel, webhook_module.HookEventIssueMilestone:
		return convertUnmarshalledJSON(rc.Issue, data)
	case webhook_module.HookEventIssueComment, webhook_module.HookEventPullRequestComment:
		// previous code sometimes sent s.PullRequest(p.(*api.PullRequestPayload))
		// however I couldn't find in notifier.go such a payload with an HookEvent***Comment event

		// History (most recent first):
		//  - refactored in https://github.com/go-gitea/gitea/pull/12310
		//  - assertion added in https://github.com/go-gitea/gitea/pull/12046
		//  - issue raised in https://github.com/go-gitea/gitea/issues/11940#issuecomment-645713996
		//    > That's because for HookEventPullRequestComment event, some places use IssueCommentPayload and others use PullRequestPayload

		// In modules/actions/workflows.go:183 the type assertion is always payload.(*api.IssueCommentPayload)
		return convertUnmarshalledJSON(rc.IssueComment, data)
	case webhook_module.HookEventPush:
		return convertUnmarshalledJSON(rc.Push, data)
	case webhook_module.HookEventPullRequest, webhook_module.HookEventPullRequestAssign, webhook_module.HookEventPullRequestLabel,
		webhook_module.HookEventPullRequestMilestone, webhook_module.HookEventPullRequestSync, webhook_module.HookEventPullRequestReviewRequest:
		return convertUnmarshalledJSON(rc.PullRequest, data)
	case webhook_module.HookEventPullRequestReviewApproved, webhook_module.HookEventPullRequestReviewRejected, webhook_module.HookEventPullRequestReviewComment:
		return convertUnmarshalledJSON(func(p *api.PullRequestPayload) (T, error) {
			return rc.Review(p, event)
		}, data)
	case webhook_module.HookEventRepository:
		return convertUnmarshalledJSON(rc.Repository, data)
	case webhook_module.HookEventRelease:
		return convertUnmarshalledJSON(rc.Release, data)
	case webhook_module.HookEventWiki:
		return convertUnmarshalledJSON(rc.Wiki, data)
	case webhook_module.HookEventPackage:
		return convertUnmarshalledJSON(rc.Package, data)
	case webhook_module.HookEventActionRunFailure, webhook_module.HookEventActionRunRecover, webhook_module.HookEventActionRunSuccess:
		return convertUnmarshalledJSON(rc.Action, data)
	}
	var t T
	return t, fmt.Errorf("newPayload unsupported event: %s", event)
}

func NewJSONRequest[T any](pc PayloadConvertor[T], w *webhook_model.Webhook, t *webhook_model.HookTask, withDefaultHeaders bool) (*http.Request, []byte, error) {
	payload, err := NewPayload(pc, []byte(t.PayloadContent), t.EventType)
	if err != nil {
		return nil, nil, err
	}
	return NewJSONRequestWithPayload(payload, w, t, withDefaultHeaders)
}

func NewJSONRequestWithPayload(payload any, w *webhook_model.Webhook, t *webhook_model.HookTask, withDefaultHeaders bool) (*http.Request, []byte, error) {
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, nil, err
	}

	method := w.HTTPMethod
	if method == "" {
		method = http.MethodPost
	}

	req, err := http.NewRequest(method, w.URL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	if withDefaultHeaders {
		return req, body, AddDefaultHeaders(req, []byte(w.Secret), t, body)
	}
	return req, body, nil
}

// AddDefaultHeaders adds the X-Forgejo, X-Gitea, X-Gogs, X-Hub, X-GitHub headers to the given request
func AddDefaultHeaders(req *http.Request, secret []byte, t *webhook_model.HookTask, payloadContent []byte) error {
	var signatureSHA1 string
	var signatureSHA256 string
	if len(secret) > 0 {
		sig1 := hmac.New(sha1.New, secret)
		sig256 := hmac.New(sha256.New, secret)
		_, err := io.MultiWriter(sig1, sig256).Write(payloadContent)
		if err != nil {
			// this error should never happen, since the hashes are writing to []byte and always return a nil error.
			return fmt.Errorf("prepareWebhooks.sigWrite: %w", err)
		}
		signatureSHA1 = hex.EncodeToString(sig1.Sum(nil))
		signatureSHA256 = hex.EncodeToString(sig256.Sum(nil))
	}

	event := t.EventType.Event()
	eventType := string(t.EventType)
	req.Header.Add("X-Forgejo-Delivery", t.UUID)
	req.Header.Add("X-Forgejo-Event", event)
	req.Header.Add("X-Forgejo-Event-Type", eventType)
	req.Header.Add("X-Forgejo-Signature", signatureSHA256)
	req.Header.Add("X-Gitea-Delivery", t.UUID)
	req.Header.Add("X-Gitea-Event", event)
	req.Header.Add("X-Gitea-Event-Type", eventType)
	req.Header.Add("X-Gitea-Signature", signatureSHA256)
	req.Header.Add("X-Gogs-Delivery", t.UUID)
	req.Header.Add("X-Gogs-Event", event)
	req.Header.Add("X-Gogs-Event-Type", eventType)
	req.Header.Add("X-Gogs-Signature", signatureSHA256)
	req.Header.Add("X-Hub-Signature", "sha1="+signatureSHA1)
	req.Header.Add("X-Hub-Signature-256", "sha256="+signatureSHA256)
	req.Header["X-GitHub-Delivery"] = []string{t.UUID}
	req.Header["X-GitHub-Event"] = []string{event}
	req.Header["X-GitHub-Event-Type"] = []string{eventType}
	return nil
}
