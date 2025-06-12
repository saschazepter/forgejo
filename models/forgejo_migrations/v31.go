// Copyright 2025 The Forgejo Authors.
// SPDX-License-Identifier: GPL-3.0-or-later

package forgejo_migrations //nolint:revive

import (
	"xorm.io/xorm"
	"xorm.io/xorm/schemas"
)

func SetTopicsAsEmptySlice(x *xorm.Engine) error {
	var err error
	switch x.Dialect().URI().DBType {
	case schemas.MYSQL:
		_, err = x.Exec("UPDATE `repository` SET topics = '[]' WHERE topics IS NULL OR topics = 'null'")
	case schemas.SQLITE:
		_, err = x.Exec("UPDATE `repository` SET topics = '[]' WHERE topics IS NULL OR topics = 'null'")
	case schemas.POSTGRES:
		_, err = x.Exec("UPDATE `repository` SET topics = '[]' WHERE topics IS NULL OR topics::text = 'null'")
	}

	if err != nil {
		return err
	}

	if x.Dialect().URI().DBType == schemas.SQLITE {
		sessMigration := x.NewSession()
		defer sessMigration.Close()
		if err := sessMigration.Begin(); err != nil {
			return err
		}
		_, err = sessMigration.Exec("ALTER TABLE `repository` RENAME COLUMN `topics` TO `topics_backup`")
		if err != nil {
			return err
		}
		_, err = sessMigration.Exec("ALTER TABLE `repository` ADD COLUMN `topics` TEXT NOT NULL DEFAULT '[]'")
		if err != nil {
			return err
		}
		_, err = sessMigration.Exec("UPDATE `repository` SET `topics` = `topics_backup`")
		if err != nil {
			return err
		}
		_, err = sessMigration.Exec("ALTER TABLE `repository` DROP COLUMN `topics_backup`")
		if err != nil {
			return err
		}

		return sessMigration.Commit()
	}

	type Repository struct {
		ID     int64    `xorm:"pk autoincr"`
		Topics []string `xorm:"TEXT JSON NOT NULL"`
	}

	return x.Sync(new(Repository))
}
