// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/json"
	"forgejo.org/modules/setting"
	auth_service "forgejo.org/services/auth"

	"github.com/urfave/cli/v3"
)

func microcmdUserCreateAuthorizedIntegration() *cli.Command {
	return &cli.Command{
		Name: "create-authorized-integration",
		Description: `Creates an authorized integration. Authorized integrations allow Forgejo to
receive JWTs from external sources, validate their claims against
user-defined rules, and grant access to Forgejo's API on behalf of a user.

The issuer may be set to "urn:forgejo:authorized-integrations:actions"
to support JWTs from the local instance's Forgejo Actions, utilizing the
enable-openid-connect flag in a workflow.`,

		// `--claim-in sub=v1,v2,v3` needs to be parsed as a single parameter so that we can comma-split the value into
		// an array.  To accomplish this, we disable urfave 's slice flag separator, which would cause this to be
		// treated as "sub=v1", "v2=?", and "v3=?", resulting in an error of missing values.
		DisableSliceFlagSeparator: true,

		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "username",
				Aliases:  []string{"u"},
				Usage:    "Username",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "name",
				Usage:    "Name of the authorized integration for later identification",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "description",
				Usage: "Optional description for the authorized integration",
			},

			// JWT validation:
			&cli.StringFlag{
				Name:     "issuer",
				Usage:    `JWT issuer ('iss' claim), example: https://forgejo.example.org/api/actions`,
				Required: true,
			},
			&cli.StringMapFlag{
				Name:  "claim-eq",
				Value: map[string]string{},
				Usage: `Zero-or-more claim equality checks, formatted as claim=value, example: "actor=someuser"`,
			},
			&cli.StringMapFlag{
				Name:  "claim-in",
				Value: map[string]string{},
				Usage: `Zero-or-more claim equality in list checks, formatted as claim=value1,value2,... example: "actor=user1,user2"`,
			},
			&cli.StringMapFlag{
				Name:  "claim-glob",
				Value: map[string]string{},
				Usage: `Zero-or-more claim glob checks, formatted as claim=value, example: "sub=repo:forgejo/*:pull_request"`,
			},
			&cli.StringMapFlag{
				Name:  "claim-glob-in",
				Value: map[string]string{},
				Usage: `Zero-or-more claim glob in list checks, formatted as claim=va*ue1,va*ue2,... example: "sub=repo:*/*:pull_request,repo:*/*:refs:*"`,
			},
			// nested claim support omitted for now -- pretty complex for a CLI

			// Permissions available on successful auth:
			&cli.StringSliceFlag{
				Name:  "scope",
				Value: []string{"all"},
				Usage: `One-or-more scopes to apply to access token, examples: "all", "read:issue", "write:repository"`,
			},
			&cli.StringSliceFlag{
				Name:  "repo",
				Value: []string{"all"},
				Usage: `Zero-or-more specific repositories that can be accessed, or "all" to allow access to all repositories, example: "owner1/repo1"`,
			},
		},
		Before: noDanglingArgs,
		Action: runCreateAuthorizedIntegration,
	}
}

func runCreateAuthorizedIntegration(ctx context.Context, c *cli.Command) error {
	// this command should load settings since config such as authorized_integration.ALLOW_LOCALNETWORKS can affect behavior of this command.
	// this also makes behavior match experience from configuring this using web ui.
	setting.LoadSettings()

	if !c.IsSet("username") {
		return errors.New("you must provide a username to generate a token for")
	}

	ctx, cancel := installSignals(ctx)
	defer cancel()

	if err := initDB(ctx); err != nil {
		return err
	}

	user, err := user_model.GetUserByName(ctx, c.String("username"))
	if err != nil {
		return err
	}

	ai := &auth_model.AuthorizedIntegration{
		UserID:      user.ID,
		Name:        c.String("name"),
		Description: c.String("description"),
		UI:          auth_model.AuthorizedIntegrationUIGeneric,
	}

	var rules []auth_model.ClaimRule
	ai.Issuer = c.String("issuer")
	for claim, value := range c.StringMap("claim-eq") {
		rules = append(rules, auth_model.ClaimRule{
			Claim:      claim,
			Comparison: auth_model.ClaimEqual,
			Value:      value,
		})
	}
	for claim, value := range c.StringMap("claim-in") {
		values := []string{}
		for s := range strings.SplitSeq(value, ",") {
			values = append(values, strings.TrimSpace(s))
		}
		rules = append(rules, auth_model.ClaimRule{
			Claim:      claim,
			Comparison: auth_model.ClaimIn,
			Values:     values,
		})
	}
	for claim, value := range c.StringMap("claim-glob") {
		rules = append(rules, auth_model.ClaimRule{
			Claim:      claim,
			Comparison: auth_model.ClaimGlob,
			Value:      value,
		})
	}
	for claim, value := range c.StringMap("claim-glob-in") {
		values := []string{}
		for s := range strings.SplitSeq(value, ",") {
			values = append(values, strings.TrimSpace(s))
		}
		rules = append(rules, auth_model.ClaimRule{
			Claim:      claim,
			Comparison: auth_model.ClaimGlobIn,
			Values:     values,
		})
	}
	ai.ClaimRules = &auth_model.ClaimRules{Rules: rules}

	scopes := strings.Join(c.StringSlice("scope"), ",")
	accessTokenScope, err := auth_model.AccessTokenScope(scopes).Normalize()
	if err != nil {
		return fmt.Errorf("invalid access token scope provided: %w", err)
	}
	ai.Scope = accessTokenScope

	allRepos := false
	repos := []*repo.Repository{}
	for _, repoName := range c.StringSlice("repo") {
		if repoName == "all" {
			allRepos = true
		} else {
			split := strings.Split(repoName, "/")
			if len(split) != 2 {
				return fmt.Errorf("invalid repo name: %q", split)
			}
			owner := split[0]
			name := split[1]
			repo, err := repo.GetRepositoryByOwnerAndName(ctx, owner, name)
			if err != nil {
				return err
			}
			repos = append(repos, repo)
		}
	}
	ai.ResourceAllRepos = allRepos

	rr := make([]*auth_model.AuthorizedIntegResourceRepo, len(repos))
	for i := range repos {
		rr[i] = &auth_model.AuthorizedIntegResourceRepo{RepoID: repos[i].ID}
	}

	if err := auth_service.InsertAuthorizedIntegration(ctx, ai, rr); err != nil {
		return err
	}

	type ClaimRuleDescription struct {
		Description string                     `json:"description"`
		Claim       string                     `json:"claim"`
		Comparison  auth_model.ClaimComparison `json:"compare"`
		Value       string                     `json:"value,omitempty"`
		Values      []string                   `json:"values,omitempty"`
	}
	output := struct {
		Message     string                 `json:"message"`
		Name        string                 `json:"name"`
		Description string                 `json:"description,omitempty"`
		Issuer      string                 `json:"issuer"`
		Audience    string                 `json:"audience"`
		ClaimRules  []ClaimRuleDescription `json:"claim_rules"`
	}{
		Message:     "Authorized integration was successfully created.",
		Name:        ai.Name,
		Description: ai.Description,
		Issuer:      ai.Issuer,
		Audience:    ai.Audience,
	}
	for _, cr := range ai.ClaimRules.Rules {
		var description string
		switch cr.Comparison {
		case auth_model.ClaimEqual:
			description = fmt.Sprintf("%q = %q", cr.Claim, cr.Value)
		case auth_model.ClaimIn:
			description = fmt.Sprintf("%q in %q", cr.Claim, cr.Values)
		case auth_model.ClaimGlob:
			description = fmt.Sprintf("%q matches %q", cr.Claim, cr.Value)
		case auth_model.ClaimGlobIn:
			description = fmt.Sprintf("%q matches in %q", cr.Claim, cr.Values)
		}
		output.ClaimRules = append(output.ClaimRules, ClaimRuleDescription{
			Description: description,
			Claim:       cr.Claim,
			Comparison:  cr.Comparison,
			Value:       cr.Value,
			Values:      cr.Values,
		})
	}

	raw, err := json.Marshal(output)
	if err != nil {
		return err
	}
	var indent bytes.Buffer
	if err := json.Indent(&indent, raw, "", "  "); err != nil {
		return err
	}
	os.Stdout.Write(indent.Bytes())

	return nil
}
