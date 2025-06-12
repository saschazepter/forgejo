// Copyright The Forgejo Authors.
// SPDX-License-Identifier: MIT

package forgejo

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/modules/private"
	"forgejo.org/modules/setting"
	private_routers "forgejo.org/routers/private"

	"github.com/urfave/cli/v3"
)

func CmdActions(ctx context.Context) *cli.Command {
	return &cli.Command{
		Name:  "actions",
		Usage: "Commands for managing Forgejo Actions",
		Commands: []*cli.Command{
			SubcmdActionsGenerateRunnerToken(ctx),
			SubcmdActionsGenerateRunnerSecret(ctx),
			SubcmdActionsRegister(ctx),
		},
	}
}

func SubcmdActionsGenerateRunnerToken(ctx context.Context) *cli.Command {
	return &cli.Command{
		Name:   "generate-runner-token",
		Usage:  "Generate a new token for a runner to use to register with the server",
		Before: prepareWorkPathAndCustomConf(ctx),
		Action: RunGenerateActionsRunnerToken,
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

func SubcmdActionsGenerateRunnerSecret(ctx context.Context) *cli.Command {
	return &cli.Command{
		Name:   "generate-secret",
		Usage:  "Generate a secret suitable for input to the register subcommand",
		Action: RunGenerateSecret,
	}
}

func SubcmdActionsRegister(ctx context.Context) *cli.Command {
	return &cli.Command{
		Name:   "register",
		Usage:  "Idempotent registration of a runner using a shared secret",
		Before: prepareWorkPathAndCustomConf(ctx),
		Action: RunRegister,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "secret",
				Usage: "the secret the runner will use to connect as a 40 character hexadecimal string",
			},
			&cli.StringFlag{
				Name:  "secret-stdin",
				Usage: "the secret the runner will use to connect as a 40 character hexadecimal string, read from stdin",
			},
			&cli.StringFlag{
				Name:  "secret-file",
				Usage: "path to the file containing the secret the runner will use to connect as a 40 character hexadecimal string",
			},
			&cli.StringFlag{
				Name:    "scope",
				Aliases: []string{"s"},
				Value:   "",
				Usage:   "{owner}[/{repo}] - leave empty for a global runner",
			},
			&cli.StringFlag{
				Name:  "labels",
				Value: "",
				Usage: "comma separated list of labels supported by the runner (e.g. docker,ubuntu-latest,self-hosted)  (not required since v1.21)",
			},
			&cli.BoolFlag{
				Name:  "keep-labels",
				Value: false,
				Usage: "do not affect the labels when updating an existing runner",
			},
			&cli.StringFlag{
				Name:  "name",
				Value: "runner",
				Usage: "name of the runner (default runner)",
			},
			&cli.StringFlag{
				Name:  "version",
				Value: "",
				Usage: "version of the runner (not required since v1.21)",
			},
		},
	}
}

func readSecret(ctx context.Context, cli *cli.Command) (string, error) {
	if cli.IsSet("secret") {
		return cli.String("secret"), nil
	}
	if cli.IsSet("secret-stdin") {
		buf, err := io.ReadAll(ContextGetStdin(ctx))
		if err != nil {
			return "", err
		}
		return string(buf), nil
	}
	if cli.IsSet("secret-file") {
		path := cli.String("secret-file")
		buf, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(buf), nil
	}
	return "", errors.New("at least one of the --secret, --secret-stdin, --secret-file options is required")
}

func validateSecret(secret string) error {
	secretLen := len(secret)
	if secretLen != 40 {
		return fmt.Errorf("the secret must be exactly 40 characters long, not %d: generate-secret can provide a secret matching the requirements", secretLen)
	}
	if _, err := hex.DecodeString(secret); err != nil {
		return fmt.Errorf("the secret must be an hexadecimal string: %w", err)
	}
	return nil
}

func getLabels(cli *cli.Command) (*[]string, error) {
	if !cli.Bool("keep-labels") {
		lblValue := strings.Split(cli.String("labels"), ",")
		return &lblValue, nil
	}
	if cli.String("labels") != "" {
		return nil, errors.New("--labels and --keep-labels should not be used together")
	}
	return nil, nil
}

func RunRegister(ctx context.Context, cli *cli.Command) error {
	var cancel context.CancelFunc
	if !ContextGetNoInit(ctx) {
		ctx, cancel = installSignals(ctx)
		defer cancel()

		if err := initDB(ctx); err != nil {
			return err
		}
	}
	setting.MustInstalled()

	secret, err := readSecret(ctx, cli)
	if err != nil {
		return err
	}
	if err := validateSecret(secret); err != nil {
		return err
	}
	scope := cli.String("scope")
	name := cli.String("name")
	version := cli.String("version")
	labels, err := getLabels(cli)
	if err != nil {
		return err
	}

	//
	// There are two kinds of tokens
	//
	// - "registration token" only used when a runner interacts to
	//   register
	//
	// - "token" obtained after a successful registration and stored by
	//   the runner to authenticate
	//
	// The register subcommand does not need a "registration token", it
	// needs a "token". Using the same name is confusing and secret is
	// preferred for this reason in the cli.
	//
	// The ActionsRunnerRegister argument is token to be consistent with
	// the internal naming. It is still confusing to the developer but
	// not to the user.
	//
	owner, repo, err := private_routers.ParseScope(ctx, scope)
	if err != nil {
		return err
	}

	runner, err := actions_model.RegisterRunner(ctx, owner, repo, secret, labels, name, version)
	if err != nil {
		return fmt.Errorf("error while registering runner: %v", err)
	}

	if _, err := fmt.Fprintf(ContextGetStdout(ctx), "%s", runner.UUID); err != nil {
		panic(err)
	}
	return nil
}

func RunGenerateSecret(ctx context.Context, cli *cli.Command) error {
	runner := actions_model.ActionRunner{}
	if err := runner.GenerateToken(); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(ContextGetStdout(ctx), "%s", runner.Token); err != nil {
		panic(err)
	}
	return nil
}

func RunGenerateActionsRunnerToken(ctx context.Context, cli *cli.Command) error {
	if !ContextGetNoInit(ctx) {
		var cancel context.CancelFunc
		ctx, cancel = installSignals(ctx)
		defer cancel()
	}

	setting.MustInstalled()

	scope := cli.String("scope")

	respText, extra := private.GenerateActionsRunnerToken(ctx, scope)
	if extra.HasError() {
		return handleCliResponseExtra(ctx, extra)
	}
	if _, err := fmt.Fprintf(ContextGetStdout(ctx), "%s", respText.Text); err != nil {
		panic(err)
	}
	return nil
}
