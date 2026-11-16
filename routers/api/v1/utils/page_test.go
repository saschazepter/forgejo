// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package utils

import (
	"fmt"
	"testing"

	"forgejo.org/models/db"
	"forgejo.org/modules/setting"
	"forgejo.org/services/contexttest"

	"github.com/stretchr/testify/assert"
)

func TestGetListOptionsDefaults(t *testing.T) {
	ctx, _ := contexttest.MockAPIContext(t, "/")
	assert.Equal(t, db.ListOptions{
		Page:     1,
		PageSize: setting.API.DefaultPagingNum,
	}, GetListOptions(ctx))
}

func TestGetListOptionsLimit(t *testing.T) {
	ctx, _ := contexttest.MockAPIContext(t, "/?limit=1")
	assert.Equal(t, db.ListOptions{
		Page:     1,
		PageSize: 1,
	}, GetListOptions(ctx))
}

func TestGetListOptionsLimitWithPage(t *testing.T) {
	ctx, _ := contexttest.MockAPIContext(t, "/?page=2&limit=10")
	assert.Equal(t, db.ListOptions{
		Page:     2,
		PageSize: 10,
	}, GetListOptions(ctx))
}

func TestGetListOptionsLimitBeyondMax(t *testing.T) {
	ctx, _ := contexttest.MockAPIContext(t, fmt.Sprintf("/?limit=%d", setting.API.MaxResponseItems+1))
	assert.Equal(t, db.ListOptions{
		Page:     1,
		PageSize: setting.API.MaxResponseItems,
	}, GetListOptions(ctx))
}
