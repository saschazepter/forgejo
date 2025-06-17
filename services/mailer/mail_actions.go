// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later
package mailer

import (
	"bytes"

	actions_model "forgejo.org/models/actions"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/base"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/translation"
)

const (
	tplActionNowDone base.TplName = "actions/now_done"
)

// requires !run.Status.IsSuccess() or !lastRun.Status.IsSuccess()
func MailActionRun(run *actions_model.ActionRun, priorStatus actions_model.Status, lastRun *actions_model.ActionRun) error {
	if setting.MailService == nil {
		// No mail service configured
		return nil
	}

	if !run.EnableMailNotifications {
		return nil
	}

	if run.TriggerUser.Email != "" && run.TriggerUser.EmailNotificationsPreference != user_model.EmailNotificationsDisabled {
		if err := sendMailActionRun(run.TriggerUser, run, priorStatus, lastRun); err != nil {
			return err
		}
	}

	if run.Repo.Owner.Email != "" && run.Repo.Owner.Email != run.TriggerUser.Email && run.Repo.Owner.EmailNotificationsPreference != user_model.EmailNotificationsDisabled {
		if err := sendMailActionRun(run.Repo.Owner, run, priorStatus, lastRun); err != nil {
			return err
		}
	}

	return nil
}

func sendMailActionRun(to *user_model.User, run *actions_model.ActionRun, priorStatus actions_model.Status, lastRun *actions_model.ActionRun) error {
	var (
		locale  = translation.NewLocale(to.Language)
		content bytes.Buffer
	)

	var subject string
	if run.Status.IsSuccess() {
		subject = locale.TrString("mail.actions.successful_run_after_failure_subject", run.Title, run.Repo.FullName())
	} else {
		subject = locale.TrString("mail.actions.not_successful_run", run.Title, run.Repo.FullName())
	}

	commitSHA := run.CommitSHA
	if len(commitSHA) > 7 {
		commitSHA = commitSHA[:7]
	}
	branch := run.PrettyRef()

	data := map[string]any{
		"locale":          locale,
		"Link":            run.HTMLURL(),
		"Subject":         subject,
		"Language":        locale.Language(),
		"RepoFullName":    run.Repo.FullName(),
		"Run":             run,
		"TriggerUserLink": run.TriggerUser.HTMLURL(),
		"LastRun":         lastRun,
		"PriorStatus":     priorStatus,
		"CommitSHA":       commitSHA,
		"Branch":          branch,
		"IsSuccess":       run.Status.IsSuccess(),
	}

	if err := bodyTemplates.ExecuteTemplate(&content, string(tplActionNowDone), data); err != nil {
		return err
	}

	msg := NewMessage(to.EmailTo(), subject, content.String())
	msg.Info = subject
	SendAsync(msg)

	return nil
}
