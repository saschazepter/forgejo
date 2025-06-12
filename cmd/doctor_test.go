// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"testing"

	"forgejo.org/modules/log"
	"forgejo.org/services/doctor"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestDoctorRun(t *testing.T) {
	doctor.Register(&doctor.Check{
		Title: "Test Check",
		Name:  "test-check",
		Run:   func(ctx context.Context, logger log.Logger, autofix bool) error { return nil },

		SkipDatabaseInitialization: true,
	})
	app := cli.Command{}
	app.Commands = []*cli.Command{cmdDoctorCheck()}
	err := app.Run(t.Context(), []string{"./gitea", "check", "--run", "test-check"})
	require.NoError(t, err)
	err = app.Run(t.Context(), []string{"./gitea", "check", "--run", "no-such"})
	require.ErrorContains(t, err, `unknown checks: "no-such"`)
	err = app.Run(t.Context(), []string{"./gitea", "check", "--run", "test-check,no-such"})
	require.ErrorContains(t, err, `unknown checks: "no-such"`)
}
