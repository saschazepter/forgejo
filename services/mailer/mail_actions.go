// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package mailer

import (
	"bytes"
	"context"
	"fmt"

	actions_model "forgejo.org/models/actions"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/base"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/translation"
)

const (
	tplActionJobFailure base.TplName = "actions/job_failure"
)

var sendActionRunJobFailureNotification = func(ctx context.Context, job *actions_model.ActionRunJob) error {
	if !job.Status.IsFailure() {
		return nil
	}

	if setting.MailService == nil {
		// No mail service configured
		return nil
	}

	if !job.Run.NotifyEmail {
		return nil
	}

	user := job.Run.TriggerUser
	// this happens e.g. when this is a scheduled run
	if user.IsSystem() {
		user = job.Run.Repo.Owner
	}
	if user.IsSystem() || user.Email == "" {
		return nil
	}

	if user.EmailNotificationsPreference == user_model.EmailNotificationsDisabled {
		return nil
	}

	jobLink, err := job.HTMLURL(ctx)
	if err != nil {
		return fmt.Errorf("could not generate link to job results: %w", err)
	}

	locale := translation.NewLocale(user.Language)
	data := map[string]any{
		"locale":   locale,
		"Language": locale.Language(),
		"Link":     jobLink,
		"Job":      job,
	}

	var content bytes.Buffer
	if err := bodyTemplates.ExecuteTemplate(&content, string(tplActionJobFailure), data); err != nil {
		return err
	}

	subject := fmt.Sprintf("[%[1]s] %s", job.Run.Repo.FullName(),
		locale.TrString("mail.actions.job_failure_subject", job.Name))
	msg := NewMessage(user.EmailTo(), subject, content.String())
	msg.Info = subject
	SendAsync(msg)

	return nil
}
