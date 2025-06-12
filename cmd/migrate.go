// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"

	"forgejo.org/models/db"
	"forgejo.org/models/migrations"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"

	"github.com/urfave/cli/v3"
)

// CmdMigrate represents the available migrate sub-command.
func cmdMigrate() *cli.Command {
	return &cli.Command{
		Name:        "migrate",
		Usage:       "Migrate the database",
		Description: "This is a command for migrating the database, so that you can run 'forgejo admin user create' before starting the server.",
		Action:      runMigrate,
	}
}

func runMigrate(stdCtx context.Context, ctx *cli.Command) error {
	stdCtx, cancel := installSignals(stdCtx)
	defer cancel()

	if err := initDB(stdCtx); err != nil {
		return err
	}

	log.Info("AppPath: %s", setting.AppPath)
	log.Info("AppWorkPath: %s", setting.AppWorkPath)
	log.Info("Custom path: %s", setting.CustomPath)
	log.Info("Log path: %s", setting.Log.RootPath)
	log.Info("Configuration file: %s", setting.CustomConf)

	if err := db.InitEngineWithMigration(context.Background(), func(dbEngine db.Engine) error {
		masterEngine, err := db.GetMasterEngine(dbEngine)
		if err != nil {
			return err
		}
		return migrations.Migrate(masterEngine)
	}); err != nil {
		log.Fatal("Failed to initialize ORM engine: %v", err)
		return err
	}

	return nil
}
