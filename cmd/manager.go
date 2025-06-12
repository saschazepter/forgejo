// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"os"
	"time"

	"forgejo.org/modules/private"

	"github.com/urfave/cli/v3"
)

// CmdManager represents the manager command
func cmdManager() *cli.Command {
	return &cli.Command{
		Name:        "manager",
		Usage:       "Manage the running forgejo process",
		Description: "This is a command for managing the running forgejo process",
		Commands: []*cli.Command{
			subcmdShutdown(),
			subcmdRestart(),
			subcmdReloadTemplates(),
			subcmdFlushQueues(),
			subcmdLogging(),
			subCmdProcesses(),
		},
	}
}

func subcmdShutdown() *cli.Command {
	return &cli.Command{
		Name:  "shutdown",
		Usage: "Gracefully shutdown the running process",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name: "debug",
			},
		},
		Action: runShutdown,
	}
}

func subcmdRestart() *cli.Command {
	return &cli.Command{
		Name:  "restart",
		Usage: "Gracefully restart the running process - (not implemented for windows servers)",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name: "debug",
			},
		},
		Action: runRestart,
	}
}

func subcmdReloadTemplates() *cli.Command {
	return &cli.Command{
		Name:  "reload-templates",
		Usage: "Reload template files in the running process",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name: "debug",
			},
		},
		Action: runReloadTemplates,
	}
}

func subcmdFlushQueues() *cli.Command {
	return &cli.Command{
		Name:   "flush-queues",
		Usage:  "Flush queues in the running process",
		Action: runFlushQueues,
		Flags: []cli.Flag{
			&cli.DurationFlag{
				Name:  "timeout",
				Value: 60 * time.Second,
				Usage: "Timeout for the flushing process",
			},
			&cli.BoolFlag{
				Name:  "non-blocking",
				Usage: "Set to true to not wait for flush to complete before returning",
			},
			&cli.BoolFlag{
				Name: "debug",
			},
		},
	}
}

func subCmdProcesses() *cli.Command {
	return &cli.Command{
		Name:   "processes",
		Usage:  "Display running processes within the current process",
		Action: runProcesses,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name: "debug",
			},
			&cli.BoolFlag{
				Name:  "flat",
				Usage: "Show processes as flat table rather than as tree",
			},
			&cli.BoolFlag{
				Name:  "no-system",
				Usage: "Do not show system processes",
			},
			&cli.BoolFlag{
				Name:  "stacktraces",
				Usage: "Show stacktraces",
			},
			&cli.BoolFlag{
				Name:  "json",
				Usage: "Output as json",
			},
			&cli.StringFlag{
				Name:  "cancel",
				Usage: "Process PID to cancel. (Only available for non-system processes.)",
			},
		},
	}
}

func runShutdown(ctx context.Context, c *cli.Command) error {
	ctx, cancel := installSignals(ctx)
	defer cancel()

	setup(ctx, c.Bool("debug"), false)
	extra := private.Shutdown(ctx)
	return handleCliResponseExtra(extra)
}

func runRestart(ctx context.Context, c *cli.Command) error {
	ctx, cancel := installSignals(ctx)
	defer cancel()

	setup(ctx, c.Bool("debug"), false)
	extra := private.Restart(ctx)
	return handleCliResponseExtra(extra)
}

func runReloadTemplates(ctx context.Context, c *cli.Command) error {
	ctx, cancel := installSignals(ctx)
	defer cancel()

	setup(ctx, c.Bool("debug"), false)
	extra := private.ReloadTemplates(ctx)
	return handleCliResponseExtra(extra)
}

func runFlushQueues(ctx context.Context, c *cli.Command) error {
	ctx, cancel := installSignals(ctx)
	defer cancel()

	setup(ctx, c.Bool("debug"), false)
	extra := private.FlushQueues(ctx, c.Duration("timeout"), c.Bool("non-blocking"))
	return handleCliResponseExtra(extra)
}

func runProcesses(ctx context.Context, c *cli.Command) error {
	ctx, cancel := installSignals(ctx)
	defer cancel()

	setup(ctx, c.Bool("debug"), false)
	extra := private.Processes(ctx, os.Stdout, c.Bool("flat"), c.Bool("no-system"), c.Bool("stacktraces"), c.Bool("json"), c.String("cancel"))
	return handleCliResponseExtra(extra)
}
