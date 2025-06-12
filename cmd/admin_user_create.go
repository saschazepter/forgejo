// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	user_model "forgejo.org/models/user"
	pwd "forgejo.org/modules/auth/password"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/setting"

	"github.com/urfave/cli/v3"
)

func microcmdUserCreate() *cli.Command {
	return &cli.Command{
		Name:   "create",
		Usage:  "Create a new user in database",
		Action: runCreateUser,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "name",
				Usage: "Username. DEPRECATED: use username instead",
			},
			&cli.StringFlag{
				Name:  "username",
				Usage: "Username",
			},
			&cli.StringFlag{
				Name:  "password",
				Usage: "User password",
			},
			&cli.StringFlag{
				Name:  "email",
				Usage: "User email address",
			},
			&cli.BoolFlag{
				Name:  "admin",
				Usage: "User is an admin",
			},
			&cli.BoolFlag{
				Name:  "random-password",
				Usage: "Generate a random password for the user",
			},
			&cli.BoolFlag{
				Name:  "must-change-password",
				Usage: "Set this option to false to prevent forcing the user to change their password after initial login",
				Value: true,
			},
			&cli.IntFlag{
				Name:  "random-password-length",
				Usage: "Length of the random password to be generated",
				Value: 12,
			},
			&cli.BoolFlag{
				Name:  "access-token",
				Usage: "Generate access token for the user",
			},
			&cli.StringFlag{
				Name:  "access-token-name",
				Usage: `Name of the generated access token`,
				Value: "gitea-admin",
			},
			&cli.StringFlag{
				Name:  "access-token-scopes",
				Usage: `Scopes of the generated access token, comma separated. Examples: "all", "public-only,read:issue", "write:repository,write:user"`,
				Value: "all",
			},
			&cli.BoolFlag{
				Name:  "restricted",
				Usage: "Make a restricted user account",
			},
			&cli.StringFlag{
				Name:  "fullname",
				Usage: `The full, human-readable name of the user`,
			},
		},
	}
}

func runCreateUser(ctx context.Context, c *cli.Command) error {
	// this command highly depends on the many setting options (create org, visibility, etc.), so it must have a full setting load first
	// duplicate setting loading should be safe at the moment, but it should be refactored & improved in the future.
	setting.LoadSettings()

	if err := argsSet(c, "email"); err != nil {
		return err
	}

	if c.IsSet("name") && c.IsSet("username") {
		return errors.New("cannot set both --name and --username flags")
	}
	if !c.IsSet("name") && !c.IsSet("username") {
		return errors.New("one of --name or --username flags must be set")
	}

	if c.IsSet("password") && c.IsSet("random-password") {
		return errors.New("cannot set both -random-password and -password flags")
	}

	var username string
	if c.IsSet("username") {
		username = c.String("username")
	} else {
		username = c.String("name")
		_, _ = fmt.Fprint(c.Root().ErrWriter, "--name flag is deprecated. Use --username instead.\n")
	}

	ctx, cancel := installSignals(ctx)
	defer cancel()

	if err := initDB(ctx); err != nil {
		return err
	}

	var password string
	if c.IsSet("password") {
		password = c.String("password")
	} else if c.IsSet("random-password") {
		var err error
		password, err = pwd.Generate(c.Int("random-password-length"))
		if err != nil {
			return err
		}
		fmt.Printf("generated random password is '%s'\n", password)
	} else {
		return errors.New("must set either password or random-password flag")
	}

	isAdmin := c.Bool("admin")
	mustChangePassword := true // always default to true
	if c.IsSet("must-change-password") {
		// if the flag is set, use the value provided by the user
		mustChangePassword = c.Bool("must-change-password")
	} else {
		// check whether there are users in the database
		hasUserRecord, err := db.IsTableNotEmpty(&user_model.User{})
		if err != nil {
			return fmt.Errorf("IsTableNotEmpty: %w", err)
		}
		if !hasUserRecord {
			// if this is the first admin being created, don't force to change password (keep the old behavior)
			mustChangePassword = false
		}
	}

	restricted := optional.None[bool]()

	if c.IsSet("restricted") {
		restricted = optional.Some(c.Bool("restricted"))
	}

	// default user visibility in app.ini
	visibility := setting.Service.DefaultUserVisibilityMode

	u := &user_model.User{
		Name:               username,
		Email:              c.String("email"),
		Passwd:             password,
		IsAdmin:            isAdmin,
		MustChangePassword: mustChangePassword,
		Visibility:         visibility,
		FullName:           c.String("fullname"),
	}

	overwriteDefault := &user_model.CreateUserOverwriteOptions{
		IsActive:     optional.Some(true),
		IsRestricted: restricted,
	}

	var accessTokenName string
	var accessTokenScope auth_model.AccessTokenScope
	if c.IsSet("access-token") {
		accessTokenName = strings.TrimSpace(c.String("access-token-name"))
		if accessTokenName == "" {
			return errors.New("access-token-name cannot be empty")
		}
		var err error
		accessTokenScope, err = auth_model.AccessTokenScope(c.String("access-token-scopes")).Normalize()
		if err != nil {
			return fmt.Errorf("invalid access token scope provided: %w", err)
		}
		if !accessTokenScope.HasPermissionScope() {
			return errors.New("access token does not have any permission")
		}
	} else if c.IsSet("access-token-name") || c.IsSet("access-token-scopes") {
		return errors.New("access-token-name and access-token-scopes flags are only valid when access-token flag is set")
	}

	// arguments should be prepared before creating the user & access token, in case there is anything wrong

	// create the user
	if err := user_model.CreateUser(ctx, u, overwriteDefault); err != nil {
		return fmt.Errorf("CreateUser: %w", err)
	}
	fmt.Printf("New user '%s' has been successfully created!\n", username)

	// create the access token
	if accessTokenScope != "" {
		t := &auth_model.AccessToken{Name: accessTokenName, UID: u.ID, Scope: accessTokenScope}
		if err := auth_model.NewAccessToken(ctx, t); err != nil {
			return err
		}
		fmt.Printf("Access token was successfully created... %s\n", t.Token)
	}
	return nil
}
