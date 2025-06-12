// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"fmt"

	"forgejo.org/models/db"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"

	"github.com/urfave/cli/v3"
)

// cmdDoctorConvert represents the available convert sub-command.
func cmdDoctorConvert() *cli.Command {
	return &cli.Command{
		Name:        "convert",
		Usage:       "Convert the database",
		Description: "A command to convert an existing MySQL database from utf8 to utf8mb4",
		Action:      runDoctorConvert,
	}
}

func runDoctorConvert(stdCtx context.Context, ctx *cli.Command) error {
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

	if setting.Database.Type.IsMySQL() {
		if err := db.ConvertDatabaseTable(); err != nil {
			log.Fatal("Failed to convert database & table: %v", err)
			return err
		}
		fmt.Println("Converted successfully, please confirm your database's character set is now utf8mb4")
	} else {
		fmt.Println("This command can only be used with a MySQL database")
	}

	return nil
}
