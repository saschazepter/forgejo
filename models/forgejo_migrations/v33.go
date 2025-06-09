// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package forgejo_migrations //nolint:revive

import "xorm.io/xorm"

func AddIsCodeIndexerEnabledToRepository(x *xorm.Engine) error {
	type Repository struct {
		ID                   int64 `xorm:"pk autoincr"`
		IsCodeIndexerEnabled bool  `xorm:"NOT NULL DEFAULT true"`
	}

	return x.Sync(&Repository{})
}
