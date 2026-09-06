// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mailer

import (
	"context"
	"testing"

	"forgejo.org/models/unittest"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/templates"
	"forgejo.org/modules/test"
	"forgejo.org/modules/translation"

	_ "forgejo.org/modules/testimport"

	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}

func AssertTranslatedLocale(t *testing.T, message string, prefixes ...string) {
	t.Helper()
	for _, prefix := range prefixes {
		assert.NotContains(t, message, prefix, "there is an untranslated locale prefix")
	}
}

func MockMailSettings(send func(msgs ...*Message)) func() {
	translation.InitLocales(context.Background())
	subjectTemplates, bodyTemplates = templates.Mailer(context.Background())
	mailService := setting.Mailer{
		From: "test@gitea.com",
	}
	cleanups := []func(){
		test.MockVariableValue(&setting.MailService, &mailService),
		test.MockVariableValue(&setting.Domain, "localhost"),
		test.MockVariableValue(&SendAsync, send),
	}
	return func() {
		for _, cleanup := range cleanups {
			cleanup()
		}
	}
}
