// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package user

import (
	"testing"

	"forgejo.org/modules/setting"
	"forgejo.org/modules/templates"
	"forgejo.org/services/contexttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPaginationIssueType(t *testing.T) {
	ctx, _ := contexttest.MockContext(t, "/", contexttest.MockContextOption{Render: templates.HTMLRenderer()})
	ctx.Data["IssueType"] = "issue"
	pager := notificationSubscriptionPager(ctx, 1, 2)
	setting.AppSubURL = "/SubPath"
	out, err := ctx.RenderToHTML("base/paginate", map[string]any{"Link": setting.AppSubURL, "Page": pager})
	require.NoError(t, err)
	assert.Contains(t, out, `<a class=" item navigation" href="/SubPath/?page=2&issueType=issue">`)
}
