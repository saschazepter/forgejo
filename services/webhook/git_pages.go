// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package webhook

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	webhook_model "forgejo.org/models/webhook"
	"forgejo.org/modules/json"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/svg"
	"forgejo.org/modules/templates"
	webhook_module "forgejo.org/modules/webhook"
	app_context "forgejo.org/services/context"
	"forgejo.org/services/forms"
	"forgejo.org/services/webhook/shared"

	"code.forgejo.org/go-chi/binding"
)

const GitPagesBranchName string = "pages"

type gitPagesHandler struct{}

func (gitPagesHandler) Available(scope webhook_module.Scope) bool {
	return scope == webhook_module.ScopeRepo
}
func (gitPagesHandler) Type() webhook_module.HookType { return webhook_module.GIT_PAGES }

// used in git_pages.tmpl
func (gitPagesHandler) SettingGitPagesWebhook() any { return setting.GitPagesWebhook }

func (gitPagesHandler) Icon(size int) template.HTML {
	return svg.RenderHTML("git-pages", size, "img")
}

type gitPagesForm struct {
	Active   bool
	SiteURL  string `binding:"Required;ValidUrl"`
	Server   string
	Password string
}

// tryDialGitPagesSite attempts to connect to the site URL with a short timeout to verify that
// the site has a valid TLS certificate; it does not send any HTTP data
func tryDialGitPagesSite(ctx context.Context, siteURL string) error {
	parsedURL, err := url.Parse(siteURL)
	if err != nil {
		return err
	}

	if parsedURL.Scheme != "https" {
		return nil
	}

	connectHost, connectPort := parsedURL.Hostname(), parsedURL.Port()
	if connectPort == "" {
		connectPort = "443"
	}
	connectAddr := net.JoinHostPort(connectHost, connectPort)

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	// Use the webhook HTTP client so that SKIP_TLS_VERIFY and ALLOWED_HOST_LIST are respected.
	httpTransport := webhookHTTPClient.Transport.(*http.Transport)
	rawConn, err := httpTransport.DialContext(ctx, "tcp", connectAddr)
	if err != nil {
		return err
	}

	conn := tls.Client(rawConn, &tls.Config{ServerName: connectHost})
	if err = conn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
	}

	if err != nil {
		log.Debug("tryDialGitPagesSite(%s): %v", siteURL, err)
	} else {
		log.Debug("tryDialGitPagesSite(%s): OK", siteURL)
	}
	return err
}

// isTLSAlertError verifies that err describes the specific TLS error that is returned by ACME
// capable frontend servers when they do not have the authority to issue an on-demand certificate
func isTLSAlertError(err error) bool {
	// Due to a Go issue, it is not currently possible to use a better method such as `errors.Is()``:
	// https://github.com/golang/go/issues/71500
	return err != nil && strings.HasSuffix(err.Error(), "tls: internal error")
}

var errGitPagesSiteURLAndServerDNSMismatch = errors.New("site URL and server DNS mismatch")

// dnsCompareGitPagesSiteURLAgainstServer checks whether the site URL resolves to the same or a subset
// of A/AAAA records as the server has
func dnsCompareGitPagesSiteURLAgainstServer(ctx context.Context, siteURL, server string) error {
	parsedURL, err := url.Parse(siteURL)
	if err != nil {
		return err
	}

	serverHost, _, err := net.SplitHostPort(server)
	if err != nil {
		serverHost = server
	}

	siteIPs, err := net.DefaultResolver.LookupHost(ctx, parsedURL.Hostname())
	if err != nil {
		return err
	}
	log.Debug("dnsCompareGitPagesSiteURLAgainstServer(%s): %v", parsedURL.Hostname(), siteIPs)

	serverIPs, err := net.DefaultResolver.LookupHost(ctx, serverHost)
	if err != nil {
		return err
	}
	log.Debug("dnsCompareGitPagesSiteURLAgainstServer(%s): %v", serverHost, serverIPs)

	serverIPMap := map[string]struct{}{}
	for _, serverIP := range serverIPs {
		serverIPMap[serverIP] = struct{}{}
	}
	for _, siteIP := range siteIPs {
		if _, found := serverIPMap[siteIP]; !found {
			log.Debug("dnsCompareGitPagesSiteURLAgainstServer(%s, %s): A/AAAA records do not match", siteURL, server)
			return errGitPagesSiteURLAndServerDNSMismatch
		}
	}
	return nil
}

func renderCodeTag(code string) any {
	return templates.HTMLFormat("<code>%s</code>", code)
}

func (f *gitPagesForm) Validate(req *http.Request, errs binding.Errors) binding.Errors {
	ctx := app_context.GetWebContext(req)

	// The "online" (involving network access) validation checks are looking for a specific kind of
	// common error. There is a number of other problems that can happen while performing validation,
	// e.g. malformed URLs or other network errors. This is not a general-purpose diagnostic tool, so
	// we intentionally ignore all of these other problems.
	if f.Active {
		if f.Server == "" {
			// Diagnose a common source of problems: attempting to deploy to a custom domain for the first
			// time using an `https://` site URL.
			if err := tryDialGitPagesSite(req.Context(), f.SiteURL); isTLSAlertError(err) {
				errs = append(errs, binding.Error{
					FieldNames: []string{"Server"},
					Message: ctx.Locale.TrString("webhooks.git_pages.server.required_for_this_site",
						renderCodeTag("https://")),
				})
			}
		} else {
			// Diagnose a hard to understand source of problems: when the server is pre-filled but
			// the site URL is changed to point to a custom domain that resolves to a different git-pages
			// instance, the deploy will appear to succeed but nothing will be available on the custom
			// domain. Check that the A/AAAA records for the site URL hostname are a subset of A/AAAA
			// records for the server (provided both of them resolve).
			err := dnsCompareGitPagesSiteURLAgainstServer(req.Context(), f.SiteURL, f.Server)
			if errors.Is(err, errGitPagesSiteURLAndServerDNSMismatch) {
				ctx.Flash.Warning(ctx.Tr("webhooks.git_pages.site_url_and_server_dns_mismatch",
					renderCodeTag(f.SiteURL), renderCodeTag(f.Server)))
			}
		}
	}

	return errs
}

func (gitPagesHandler) UnmarshalForm(bind func(any)) forms.WebhookForm {
	var form gitPagesForm
	bind(&form)

	authorization := ""
	if form.Password != "" {
		authorization = "Pages " + strings.TrimSpace(form.Password)
	}

	return forms.WebhookForm{
		WebhookCoreForm: forms.WebhookCoreForm{
			Active:              form.Active,
			Events:              "push_only",
			BranchFilter:        "pages",
			AuthorizationHeader: authorization,
		},
		URL: form.SiteURL,
		Metadata: &GitPagesMeta{
			Server: form.Server,
		},
	}
}

type GitPagesMeta struct {
	Server string `json:"server"`
}

func (gitPagesHandler) Metadata(w *webhook_model.Webhook) any {
	s := &GitPagesMeta{}
	if err := json.Unmarshal([]byte(w.Meta), s); err != nil {
		log.Error("gitPagesHandler.Metadata(%d): %v", w.ID, err)
	}
	return s
}

func (gitPagesHandler) NewRequest(ctx context.Context, w *webhook_model.Webhook, t *webhook_model.HookTask) (*http.Request, []byte, error) {
	meta := &GitPagesMeta{}
	if err := json.Unmarshal([]byte(w.Meta), meta); err != nil {
		return nil, nil, fmt.Errorf("gitPagesHandler.NewRequest meta json: %w", err)
	}

	// Send the request to the `meta.Server` host, but set the `Host:` header to the site host.
	// This allows first-time publishing to proceed without the git-pages server yet having
	// a TLS certificate for the site host (which has a circular dependency on completion of
	// first-time publishing).
	requestURL, _ := url.Parse(w.URL)
	requestHost := requestURL.Host
	if host, _, err := net.SplitHostPort(requestHost); err == nil {
		requestHost = host
	}
	if meta.Server != "" {
		requestURL.Host = meta.Server
	}

	body := []byte(t.PayloadContent)
	req, err := http.NewRequest(http.MethodPost, requestURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("http.NewRequest: %w", err)
	}
	req.Host = requestHost

	authz, err := w.HeaderAuthorization()
	if err != nil {
		return nil, nil, fmt.Errorf("Webhook.HeaderAuthorization: %w", err)
	}
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Deliver-Timeout", fmt.Sprint(setting.Webhook.DeliverTimeout))
	return req, body, shared.AddDefaultHeaders(req, []byte(w.Secret), t, body)
}
