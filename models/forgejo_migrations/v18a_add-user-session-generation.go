// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package forgejo_migrations

import (
	"code.forgejo.org/xorm/xorm"
)

func init() {
	registerMigration(&Migration{
		Description: "add session_generation column to user table",
		Upgrade:     addUserSessionGeneration,
	})
}

func addUserSessionGeneration(x *xorm.Engine) error {
	type User struct {
		ID                int64 `xorm:"pk autoincr"`
		SessionGeneration int64 `xorm:"NOT NULL DEFAULT 0"`
	}

	_, err := x.SyncWithOptions(xorm.SyncOptions{IgnoreDropIndices: true}, new(User))
	return err
}
