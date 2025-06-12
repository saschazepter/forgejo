// Copyright 2016 The Gogs Authors. All rights reserved.
// Copyright 2016 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"fmt"
	"os"

	"forgejo.org/modules/generate"

	"github.com/mattn/go-isatty"
	"github.com/urfave/cli/v3"
)

// CmdGenerate represents the available generate sub-command.
func cmdGenerate() *cli.Command {
	return &cli.Command{
		Name:  "generate",
		Usage: "Generate Gitea's secrets/keys/tokens",
		Commands: []*cli.Command{
			subcmdSecret(),
		},
	}
}

func subcmdSecret() *cli.Command {
	return &cli.Command{
		Name:  "secret",
		Usage: "Generate a secret token",
		Commands: []*cli.Command{
			microcmdGenerateInternalToken(),
			microcmdGenerateLfsJwtSecret(),
			microcmdGenerateSecretKey(),
		},
	}
}

func microcmdGenerateInternalToken() *cli.Command {
	return &cli.Command{
		Name:   "INTERNAL_TOKEN",
		Usage:  "Generate a new INTERNAL_TOKEN",
		Action: runGenerateInternalToken,
	}
}

func microcmdGenerateLfsJwtSecret() *cli.Command {
	return &cli.Command{
		Name:    "JWT_SECRET",
		Aliases: []string{"LFS_JWT_SECRET"},
		Usage:   "Generate a new JWT_SECRET",
		Action:  runGenerateLfsJwtSecret,
	}
}

func microcmdGenerateSecretKey() *cli.Command {
	return &cli.Command{
		Name:   "SECRET_KEY",
		Usage:  "Generate a new SECRET_KEY",
		Action: runGenerateSecretKey,
	}
}

func runGenerateInternalToken(ctx context.Context, c *cli.Command) error {
	internalToken, err := generate.NewInternalToken()
	if err != nil {
		return err
	}

	fmt.Printf("%s", internalToken)

	if isatty.IsTerminal(os.Stdout.Fd()) {
		fmt.Println()
	}

	return nil
}

func runGenerateLfsJwtSecret(ctx context.Context, c *cli.Command) error {
	_, jwtSecretBase64 := generate.NewJwtSecret()

	fmt.Printf("%s", jwtSecretBase64)

	if isatty.IsTerminal(os.Stdout.Fd()) {
		fmt.Print("\n")
	}

	return nil
}

func runGenerateSecretKey(ctx context.Context, c *cli.Command) error {
	secretKey, err := generate.NewSecretKey()
	if err != nil {
		return err
	}

	fmt.Printf("%s", secretKey)

	if isatty.IsTerminal(os.Stdout.Fd()) {
		fmt.Print("\n")
	}

	return nil
}
