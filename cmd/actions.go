// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"fmt"

	"forgejo.org/modules/private"
	"forgejo.org/modules/setting"

	"github.com/urfave/cli/v3"
)

// CmdActions represents the available actions sub-commands.
func cmdActions() *cli.Command {
	return &cli.Command{
		Name:  "actions",
		Usage: "Manage Forgejo Actions",
		Commands: []*cli.Command{
			subcmdActionsGenRunnerToken(),
		},
	}
}

func subcmdActionsGenRunnerToken() *cli.Command {
	return &cli.Command{
		Name:    "generate-runner-token",
		Usage:   "Generate a new token for a runner to use to register with the server",
		Action:  runGenerateActionsRunnerToken,
		Aliases: []string{"grt"},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "scope",
				Aliases: []string{"s"},
				Value:   "",
				Usage:   "{owner}[/{repo}] - leave empty for a global runner",
			},
		},
	}
}

func runGenerateActionsRunnerToken(ctx context.Context, c *cli.Command) error {
	ctx, cancel := installSignals(ctx)
	defer cancel()

	setting.MustInstalled()

	scope := c.String("scope")

	respText, extra := private.GenerateActionsRunnerToken(ctx, scope)
	if extra.HasError() {
		return handleCliResponseExtra(extra)
	}
	_, _ = fmt.Printf("%s\n", respText.Text)
	return nil
}
