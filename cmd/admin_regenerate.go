// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"

	asymkey_model "forgejo.org/models/asymkey"
	"forgejo.org/modules/graceful"
	repo_service "forgejo.org/services/repository"

	"github.com/urfave/cli/v3"
)

var (
	microcmdRegenHooks = &cli.Command{
		Name:   "hooks",
		Usage:  "Regenerate git-hooks",
		Action: runRegenerateHooks,
	}

	microcmdRegenKeys = &cli.Command{
		Name:   "keys",
		Usage:  "Regenerate authorized_keys file",
		Action: runRegenerateKeys,
	}
)

func runRegenerateHooks(ctx context.Context, _ *cli.Command) error {
	ctx, cancel := installSignals(ctx)
	defer cancel()

	if err := initDB(ctx); err != nil {
		return err
	}
	return repo_service.SyncRepositoryHooks(graceful.GetManager().ShutdownContext())
}

func runRegenerateKeys(ctx context.Context, _ *cli.Command) error {
	ctx, cancel := installSignals(ctx)
	defer cancel()

	if err := initDB(ctx); err != nil {
		return err
	}
	return asymkey_model.RewriteAllPublicKeys(ctx)
}
