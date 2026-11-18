// Copyright 2018 The Gitea Authors. All rights reserved.
// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"forgejo.org/modules/log"
	"forgejo.org/modules/private"

	"github.com/urfave/cli/v3"
	"golang.org/x/crypto/ssh"
)

// CmdKeys represents the available keys sub-command
func cmdKeys() *cli.Command {
	return &cli.Command{
		Name:        "keys",
		Usage:       "(internal) Should only be called by SSH server",
		Description: "Queries the Forgejo database to get the authorized command for a given ssh key fingerprint",
		Before:      multipleBefore(noDanglingArgs, PrepareConsoleLoggerLevel(log.FATAL)),
		Action:      runKeys,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "expected",
				Aliases: []string{"e"},
				Value:   "git",
				Usage:   "Expected user for whom provide key commands",
			},
			&cli.StringFlag{
				Name:     "username",
				Aliases:  []string{"u"},
				Value:    "",
				Usage:    "Username trying to log in by SSH",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "type",
				Aliases:  []string{"t"},
				Value:    "",
				Usage:    "Type of the SSH key provided to the SSH Server",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "content",
				Aliases:  []string{"k"},
				Value:    "",
				Usage:    "Base64 encoded content of the SSH key provided to the SSH Server",
				Required: true,
			},
		},
	}
}

func runKeys(ctx context.Context, c *cli.Command) error {
	// Check username matches the expected username
	if strings.TrimSpace(c.String("username")) != strings.TrimSpace(c.String("expected")) {
		return nil
	}

	// Decode content and parse it a SSH public key, verify the type is what was
	// given to us.
	key, err := base64.StdEncoding.DecodeString(c.String("content"))
	if err != nil {
		return fmt.Errorf("is not valid base64 encoded content: %w", err)
	}
	publicKey, err := ssh.ParsePublicKey(key)
	if err != nil {
		return fmt.Errorf("key content cannot be parsed as public SSH key: %w", err)
	}
	if publicKey.Type() != c.String("type") {
		return fmt.Errorf("authorized keys key type mismatch: given type %q, encoded type %q", c.String("type"), publicKey.Type())
	}

	ctx, cancel := installSignals(ctx)
	defer cancel()

	setup(ctx, c.Bool("debug"), true)

	authorizedString, extra := private.AuthorizedPublicKeyByFingerprint(ctx, ssh.FingerprintSHA256(publicKey))
	// do not use handleCliResponseExtra or cli.NewExitError, if it exists immediately, it breaks some tests like Test_CmdKeys
	if extra.Error != nil {
		return extra.Error
	}
	_, _ = fmt.Fprintln(c.Root().Writer, strings.TrimSpace(authorizedString.Text))
	return nil
}
