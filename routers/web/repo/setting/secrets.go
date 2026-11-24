// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"errors"
	"net/http"

	org_model "forgejo.org/models/organization"
	"forgejo.org/modules/base"
	"forgejo.org/modules/setting"
	shared "forgejo.org/routers/web/shared/secrets"
	shared_user "forgejo.org/routers/web/shared/user"
	"forgejo.org/services/context"
)

const (
	// TODO: Separate secrets from runners when layout is ready
	tplRepoSecrets base.TplName = "repo/settings/actions"
	tplOrgSecrets  base.TplName = "org/settings/actions"
	tplUserSecrets base.TplName = "user/settings/actions"
)

type secretsCtx struct {
	OwnerID         int64
	RepoID          int64
	IsRepo          bool
	IsOrg           bool
	IsUser          bool
	SecretsTemplate base.TplName
	RedirectLink    string
}

func getSecretsCtx(ctx *context.Context) (*secretsCtx, error) {
	if ctx.Data["PageIsRepoSettings"] == true {
		return &secretsCtx{
			OwnerID:         0,
			RepoID:          ctx.Repo.Repository.ID,
			IsRepo:          true,
			SecretsTemplate: tplRepoSecrets,
			RedirectLink:    ctx.Repo.RepoLink + "/settings/actions/secrets",
		}, nil
	}

	if ctx.Data["PageIsOrgSettings"] == true {
		err := shared_user.LoadHeaderCount(ctx)
		if err != nil {
			ctx.ServerError("LoadHeaderCount", err)
			return nil, nil
		}
		return &secretsCtx{
			OwnerID:         ctx.ContextUser.ID,
			RepoID:          0,
			IsOrg:           true,
			SecretsTemplate: tplOrgSecrets,
			RedirectLink:    ctx.Org.OrgLink + "/settings/actions/secrets",
		}, nil
	}

	if ctx.Data["PageIsUserSettings"] == true {
		return &secretsCtx{
			OwnerID:         ctx.Doer.ID,
			RepoID:          0,
			IsUser:          true,
			SecretsTemplate: tplUserSecrets,
			RedirectLink:    setting.AppSubURL + "/user/settings/actions/secrets",
		}, nil
	}

	return nil, errors.New("unable to set Secrets context")
}

func Secrets(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("actions.actions")
	ctx.Data["PageType"] = "secrets"
	ctx.Data["PageIsSharedSettingsSecrets"] = true

	sCtx, err := getSecretsCtx(ctx)
	if err != nil {
		ctx.ServerError("getSecretsCtx", err)
		return
	}

	if sCtx.IsRepo {
		ctx.Data["DisableSSH"] = setting.SSH.Disabled
	}

	secrets := shared.SetSecretsContext(ctx, sCtx.OwnerID, sCtx.RepoID)
	if ctx.Written() {
		return
	}
	if sCtx.IsRepo {
		shared.SetInheritedSecretsContext(ctx, ctx.Repo.Repository.OwnerID, secrets)
		if ctx.Written() {
			return
		}

		// Link only if we could open the org's secret page
		owner := ctx.Repo.Owner
		if owner.IsOrganization() {
			isOwner, err := org_model.IsOrganizationOwner(ctx, owner.ID, ctx.Doer.ID)
			if err != nil {
				ctx.ServerError("IsOrganizationOwner", err)
				return
			}
			if isOwner || ctx.Doer.IsAdmin {
				ctx.Data["InheritedSecretsLink"] = owner.OrganisationLink() + "/settings/actions/secrets"
			}
		} else if owner.ID == ctx.Doer.ID {
			ctx.Data["InheritedSecretsLink"] = setting.AppSubURL + "/user/settings/actions/secrets"
		}
	}
	ctx.HTML(http.StatusOK, sCtx.SecretsTemplate)
}

func SecretsCreatePost(ctx *context.Context) {
	sCtx, err := getSecretsCtx(ctx)
	if err != nil {
		ctx.ServerError("getSecretsCtx", err)
		return
	}

	if ctx.HasError() {
		ctx.JSONError(ctx.GetErrMsg())
		return
	}

	shared.CreateSecretPost(
		ctx,
		sCtx.OwnerID,
		sCtx.RepoID,
		sCtx.RedirectLink,
	)
}

func SecretsEditPost(ctx *context.Context) {
	sCtx, err := getSecretsCtx(ctx)
	if err != nil {
		ctx.ServerError("getSecretsCtx", err)
		return
	}

	if ctx.HasError() {
		ctx.JSONError(ctx.GetErrMsg())
		return
	}

	shared.EditSecretPost(ctx, sCtx.OwnerID, sCtx.RepoID, ctx.ParamsInt64(":secret_id"), sCtx.RedirectLink)
}

func SecretsDeletePost(ctx *context.Context) {
	sCtx, err := getSecretsCtx(ctx)
	if err != nil {
		ctx.ServerError("getSecretsCtx", err)
		return
	}
	shared.DeleteSecretPost(
		ctx,
		sCtx.OwnerID,
		sCtx.RepoID,
		ctx.ParamsInt64(":secret_id"),
		sCtx.RedirectLink,
	)
}
