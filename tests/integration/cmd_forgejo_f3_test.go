// Copyright Earl Warren <contact@earl-warren.org>
// Copyright Loïc Dachary <loic@dachary.org>
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"fmt"
	"testing"

	"forgejo.org/cmd/forgejo"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/services/f3/driver/options"
	"forgejo.org/tests"

	_ "forgejo.org/services/f3/driver"
	_ "forgejo.org/services/f3/driver/tests"

	f3_filesystem_options "code.forgejo.org/f3/gof3/v3/forges/filesystem/options"
	f3_logger "code.forgejo.org/f3/gof3/v3/logger"
	f3_options "code.forgejo.org/f3/gof3/v3/options"
	f3_generic "code.forgejo.org/f3/gof3/v3/tree/generic"
	f3_tests "code.forgejo.org/f3/gof3/v3/tree/tests/f3"
	f3_tests_forge "code.forgejo.org/f3/gof3/v3/tree/tests/f3/forge"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func runApp(ctx context.Context, args ...string) (string, error) {
	l := f3_logger.NewCaptureLogger()
	ctx = f3_logger.ContextSetLogger(ctx, l)
	ctx = forgejo.ContextSetNoInit(ctx, true)

	app := cli.Command{}

	app.Root().Writer = l.GetBuffer()
	app.Root().ErrWriter = l.GetBuffer()

	defer func() {
		if r := recover(); r != nil {
			fmt.Println(l.String())
			panic(r)
		}
	}()

	app.Commands = []*cli.Command{
		forgejo.SubcmdF3Mirror(ctx),
	}
	err := app.Run(ctx, args)

	fmt.Println(l.String())

	return l.String(), err
}

func TestF3_CmdMirror_LocalForgejo(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&setting.F3.Enabled, true)()

	ctx := t.Context()

	mirrorOptions := f3_tests_forge.GetFactory(options.Name)().NewOptions(t)
	mirrorTree := f3_generic.GetFactory("f3")(ctx, mirrorOptions)

	fixtureOptions := f3_tests_forge.GetFactory(f3_filesystem_options.Name)().NewOptions(t)
	fixtureTree := f3_generic.GetFactory("f3")(ctx, fixtureOptions)

	log := fixtureTree.GetLogger()
	creator := f3_tests.NewCreator(t, "CmdMirrorLocalForgejo", log)

	log.Trace("======= build fixture")

	var fromPath string
	{
		fixtureUserID := "userID01"
		fixtureProjectID := "projectID01"

		userFormat := creator.GenerateUser()
		userFormat.SetID(fixtureUserID)
		users := fixtureTree.MustFind(f3_generic.NewPathFromString("/forge/users"))
		user := users.CreateChild(ctx)
		user.FromFormat(userFormat)
		user.Upsert(ctx)
		require.Equal(t, user.GetID(), users.GetIDFromName(ctx, userFormat.UserName))

		projectFormat := creator.GenerateProject()
		projectFormat.SetID(fixtureProjectID)
		projects := user.MustFind(f3_generic.NewPathFromString("projects"))
		project := projects.CreateChild(ctx)
		project.FromFormat(projectFormat)
		project.Upsert(ctx)
		require.Equal(t, project.GetID(), projects.GetIDFromName(ctx, projectFormat.Name))

		fromPath = fmt.Sprintf("/forge/users/%s/projects/%s", userFormat.UserName, projectFormat.Name)
	}

	log.Trace("======= create mirror")

	var toPath string
	var projects f3_generic.NodeInterface
	{
		userFormat := creator.GenerateUser()
		users := mirrorTree.MustFind(f3_generic.NewPathFromString("/forge/users"))
		user := users.CreateChild(ctx)
		user.FromFormat(userFormat)
		user.Upsert(ctx)
		require.Equal(t, user.GetID(), users.GetIDFromName(ctx, userFormat.UserName))

		projectFormat := creator.GenerateProject()
		projects = user.MustFind(f3_generic.NewPathFromString("projects"))
		project := projects.CreateChild(ctx)
		project.FromFormat(projectFormat)
		project.Upsert(ctx)
		require.Equal(t, project.GetID(), projects.GetIDFromName(ctx, projectFormat.Name))

		toPath = fmt.Sprintf("/forge/users/%s/projects/%s", userFormat.UserName, projectFormat.Name)
	}

	log.Trace("======= mirror %s => %s", fromPath, toPath)
	output, err := runApp(ctx,
		"f3", "mirror",
		"--from-type", f3_filesystem_options.Name,
		"--from-path", fromPath,
		"--from-filesystem-directory", fixtureOptions.(f3_options.URLInterface).GetURL(),

		"--to-type", options.Name,
		"--to-path", toPath,
	)
	require.NoError(t, err)
	log.Trace("======= assert")
	require.Contains(t, output, fmt.Sprintf("mirror %s", fromPath))
	projects.List(ctx)
	require.NotEmpty(t, projects.GetChildren())
	log.Trace("======= project %s", projects.GetChildren()[0])
}
