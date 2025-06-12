// Copyright The Forgejo Authors.
// SPDX-License-Identifier: MIT

package forgejo

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"forgejo.org/models/db"
	"forgejo.org/modules/log"
	"forgejo.org/modules/private"
	"forgejo.org/modules/setting"

	"github.com/urfave/cli/v3"
)

type key int

const (
	noInitKey key = iota + 1
	noExitKey
	stdoutKey
	stderrKey
	stdinKey
)

func CmdForgejo(ctx context.Context) *cli.Command {
	return &cli.Command{
		Name:  "forgejo-cli",
		Usage: "Forgejo CLI",
		Flags: []cli.Flag{},
		Commands: []*cli.Command{
			CmdActions(ctx),
			CmdF3(ctx),
		},
	}
}

func ContextSetNoInit(ctx context.Context, value bool) context.Context {
	return context.WithValue(ctx, noInitKey, value)
}

func ContextGetNoInit(ctx context.Context) bool {
	value, ok := ctx.Value(noInitKey).(bool)
	return ok && value
}

func ContextSetNoExit(ctx context.Context, value bool) context.Context {
	return context.WithValue(ctx, noExitKey, value)
}

func ContextGetNoExit(ctx context.Context) bool {
	value, ok := ctx.Value(noExitKey).(bool)
	return ok && value
}

func ContextSetStderr(ctx context.Context, value io.Writer) context.Context {
	return context.WithValue(ctx, stderrKey, value)
}

func ContextGetStderr(ctx context.Context) io.Writer {
	value, ok := ctx.Value(stderrKey).(io.Writer)
	if !ok {
		return os.Stderr
	}
	return value
}

func ContextSetStdout(ctx context.Context, value io.Writer) context.Context {
	return context.WithValue(ctx, stdoutKey, value)
}

func ContextGetStdout(ctx context.Context) io.Writer {
	value, ok := ctx.Value(stderrKey).(io.Writer)
	if !ok {
		return os.Stdout
	}
	return value
}

func ContextSetStdin(ctx context.Context, value io.Reader) context.Context {
	return context.WithValue(ctx, stdinKey, value)
}

func ContextGetStdin(ctx context.Context) io.Reader {
	value, ok := ctx.Value(stdinKey).(io.Reader)
	if !ok {
		return os.Stdin
	}
	return value
}

// copied from ../cmd.go
func initDB(ctx context.Context) error {
	setting.MustInstalled()
	setting.LoadDBSetting()
	setting.InitSQLLoggersForCli(log.INFO)

	if setting.Database.Type == "" {
		log.Fatal(`Database settings are missing from the configuration file: %q.
Ensure you are running in the correct environment or set the correct configuration file with -c.
If this is the intended configuration file complete the [database] section.`, setting.CustomConf)
	}
	if err := db.InitEngine(ctx); err != nil {
		return fmt.Errorf("unable to initialize the database using the configuration in %q. Error: %w", setting.CustomConf, err)
	}
	return nil
}

// copied from ../cmd.go
func installSignals(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		// install notify
		signalChannel := make(chan os.Signal, 1)

		signal.Notify(
			signalChannel,
			syscall.SIGINT,
			syscall.SIGTERM,
		)
		select {
		case <-signalChannel:
		case <-ctx.Done():
		}
		cancel()
		signal.Reset()
	}()

	return ctx, cancel
}

func handleCliResponseExtra(ctx context.Context, extra private.ResponseExtra) error {
	if false && extra.UserMsg != "" {
		if _, err := fmt.Fprintf(ContextGetStdout(ctx), "%s", extra.UserMsg); err != nil {
			panic(err)
		}
	}
	if ContextGetNoExit(ctx) {
		return extra.Error
	}
	return cli.Exit(extra.Error, 1)
}

func prepareWorkPathAndCustomConf(ctx context.Context) func(ctx context.Context, cli *cli.Command) (context.Context, error) {
	return func(ctx context.Context, cli *cli.Command) (context.Context, error) {
		if !ContextGetNoInit(ctx) {
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
		}
		return ctx, nil
	}
}
