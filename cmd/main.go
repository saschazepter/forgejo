// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"forgejo.org/cmd/forgejo"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"

	"github.com/urfave/cli/v3"
)

// cmdHelp is our own help subcommand with more information
// Keep in mind that the "./gitea help"(subcommand) is different from "./gitea --help"(flag), the flag doesn't parse the config or output "DEFAULT CONFIGURATION:" information
func cmdHelp() *cli.Command {
	c := &cli.Command{
		Name:      "help",
		Aliases:   []string{"h"},
		Usage:     "Shows a list of commands or help for one command",
		ArgsUsage: "[command]",
		Action: func(ctx context.Context, c *cli.Command) (err error) {
			lineage := c.Lineage() // The order is from child to parent: help, doctor, Forgejo
			targetCmdIdx := 0
			if c.Name == "help" {
				targetCmdIdx = 1
			}
			if targetCmdIdx+1 < len(lineage) {
				err = cli.ShowCommandHelp(ctx, lineage[targetCmdIdx+1], lineage[targetCmdIdx].Name)
			} else {
				err = cli.ShowAppHelp(c)
			}
			_, _ = fmt.Fprintf(c.Root().Writer, `
DEFAULT CONFIGURATION:
   AppPath:    %s
   WorkPath:   %s
   CustomPath: %s
   ConfigFile: %s

`, setting.AppPath, setting.AppWorkPath, setting.CustomPath, setting.CustomConf)
			return err
		},
	}
	return c
}

func appGlobalFlags() []cli.Flag {
	return []cli.Flag{
		// make the builtin flags at the top
		cli.HelpFlag,

		// shared configuration flags, they are for global and for each sub-command at the same time
		// eg: such command is valid: "./gitea --config /tmp/app.ini web --config /tmp/app.ini", while it's discouraged indeed
		// keep in mind that the short flags like "-C", "-c" and "-w" are globally polluted, they can't be used for sub-commands anymore.
		&cli.StringFlag{
			Name:    "custom-path",
			Aliases: []string{"C"},
			Usage:   "Set custom path (defaults to '{WorkPath}/custom')",
		},
		&cli.StringFlag{
			Name:    "config",
			Aliases: []string{"c"},
			Value:   setting.CustomConf,
			Usage:   "Set custom config file (defaults to '{WorkPath}/custom/conf/app.ini')",
		},
		&cli.StringFlag{
			Name:    "work-path",
			Aliases: []string{"w"},
			Usage:   "Set Forgejo's working path (defaults to the directory of the Forgejo binary)",
		},
	}
}

func prepareSubcommandWithConfig(command *cli.Command, globalFlags func() []cli.Flag) {
	command.Flags = append(globalFlags(), command.Flags...)
	command.Action = prepareWorkPathAndCustomConf(command.Action)
	command.HideHelp = true
	if command.Name != "help" {
		command.Commands = append(command.Commands, cmdHelp())
	}
	for i := range command.Commands {
		prepareSubcommandWithConfig(command.Commands[i], globalFlags)
	}
}

// prepareWorkPathAndCustomConf wraps the Action to prepare the work path and custom config
// It can't use "Before", because each level's sub-command's Before will be called one by one, so the "init" would be done multiple times
func prepareWorkPathAndCustomConf(action cli.ActionFunc) func(_ context.Context, _ *cli.Command) error {
	return func(ctx context.Context, cli *cli.Command) error {
		var args setting.ArgWorkPathAndCustomConf
		// from children to parent, check the global flags
		for _, curCtx := range cli.Lineage() {
			if curCtx.IsSet("work-path") && args.WorkPath == "" {
				args.WorkPath = curCtx.String("work-path")
			}
			if curCtx.IsSet("custom-path") && args.CustomPath == "" {
				args.CustomPath = curCtx.String("custom-path")
			}
			if curCtx.IsSet("config") && args.CustomConf == "" {
				args.CustomConf = curCtx.String("config")
			}
		}
		setting.InitWorkPathAndCommonConfig(os.Getenv, args)
		if cli.Bool("help") || action == nil {
			// the default behavior of "urfave/cli": "nil action" means "show help"
			return cmdHelp().Action(ctx, cli)
		}
		return action(ctx, cli)
	}
}

func NewMainApp(version, versionExtra string) *cli.Command {
	path, err := os.Executable()
	if err != nil {
		panic(err)
	}
	executable := filepath.Base(path)

	subCmdsStandalone := make([]*cli.Command, 0, 10)
	subCmdWithConfig := make([]*cli.Command, 0, 10)
	globalFlags := func() []cli.Flag { return []cli.Flag{} }

	//
	// If the executable is forgejo-cli, provide a Forgejo specific CLI
	// that is NOT compatible with Gitea.
	//
	if executable == "forgejo-cli" {
		subCmdsStandalone = append(subCmdsStandalone, forgejo.CmdActions(context.Background()))
		subCmdWithConfig = append(subCmdWithConfig, forgejo.CmdF3(context.Background()))
		globalFlags = func() []cli.Flag {
			return []cli.Flag{
				&cli.BoolFlag{
					Name: "quiet",
				},
				&cli.BoolFlag{
					Name: "verbose",
				},
			}
		}
	} else {
		//
		// Otherwise provide a Gitea compatible CLI which includes Forgejo
		// specific additions under the forgejo-cli subcommand. It allows
		// admins to migration from Gitea to Forgejo by replacing the gitea
		// binary and rename it to forgejo if they want.
		//
		subCmdsStandalone = append(subCmdsStandalone, forgejo.CmdForgejo(context.Background()))
		subCmdWithConfig = append(subCmdWithConfig, cmdActions())
	}

	return innerNewMainApp(version, versionExtra, subCmdsStandalone, subCmdWithConfig, globalFlags)
}

func innerNewMainApp(version, versionExtra string, subCmdsStandaloneArgs, subCmdWithConfigArgs []*cli.Command, globalFlagsArgs func() []cli.Flag) *cli.Command {
	app := &cli.Command{}
	app.Name = "forgejo"
	app.Usage = "Beyond coding. We forge."
	app.Description = `By default, forgejo will start serving using the web-server with no argument, which can alternatively be run by running the subcommand "web".`
	app.Version = version + versionExtra
	app.EnableShellCompletion = true

	// these sub-commands need to use config file
	subCmdWithConfig := []*cli.Command{
		cmdHelp(), // the "help" sub-command was used to show the more information for "work path" and "custom config"
		cmdWeb(),
		cmdServ(),
		cmdHook(),
		cmdKeys(),
		cmdDump(),
		cmdAdmin(),
		cmdMigrate(),
		cmdDoctor(),
		cmdManager(),
		cmdEmbedded(),
		cmdMigrateStorage(),
		cmdDumpRepository(),
		cmdRestoreRepository(),
	}

	subCmdWithConfig = append(subCmdWithConfig, subCmdWithConfigArgs...)

	// these sub-commands do not need the config file, and they do not depend on any path or environment variable.
	subCmdStandalone := []*cli.Command{
		cmdCert(),
		cmdGenerate(),
	}
	subCmdStandalone = append(subCmdStandalone, subCmdsStandaloneArgs...)

	app.DefaultCommand = cmdWeb().Name

	globalFlags := func() []cli.Flag {
		return append(appGlobalFlags(), globalFlagsArgs()...)
	}
	app.Flags = append(app.Flags, cli.VersionFlag)
	app.Flags = append(app.Flags, globalFlags()...)
	app.HideHelp = true // use our own help action to show helps (with more information like default config)
	app.Before = PrepareConsoleLoggerLevel(log.INFO)
	for i := range subCmdWithConfig {
		prepareSubcommandWithConfig(subCmdWithConfig[i], globalFlags)
	}
	app.Commands = append(app.Commands, subCmdWithConfig...)
	app.Commands = append(app.Commands, subCmdStandalone...)

	setting.InitGiteaEnvVars()
	return app
}

func RunMainApp(app *cli.Command, args ...string) error {
	err := app.Run(context.Background(), args)
	if err == nil {
		return nil
	}
	if strings.HasPrefix(err.Error(), "flag provided but not defined:") {
		// the cli package should already have output the error message, so just exit
		cli.OsExiter(1)
		return err
	}
	_, _ = fmt.Fprintf(app.Root().ErrWriter, "Command error: %v\n", err)
	cli.OsExiter(1)
	return err
}
