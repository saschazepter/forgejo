// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package secrets

import (
	"errors"

	"forgejo.org/models/db"
	secret_model "forgejo.org/models/secret"
	"forgejo.org/modules/log"
	"forgejo.org/modules/util"
	"forgejo.org/modules/web"
	"forgejo.org/services/context"
	"forgejo.org/services/forms"
	secrets_service "forgejo.org/services/secrets"
)

func SetSecretsContext(ctx *context.Context, ownerID, repoID int64) []*secret_model.Secret {
	secrets, err := db.Find[secret_model.Secret](ctx, secret_model.FindSecretsOptions{OwnerID: ownerID, RepoID: repoID})
	if err != nil {
		ctx.ServerError("FindSecrets", err)
		return nil
	}

	ctx.Data["Secrets"] = secrets
	return secrets
}

func SetInheritedSecretsContext(ctx *context.Context, ownerID int64, repoSecrets []*secret_model.Secret) {
	inherited, err := db.Find[secret_model.Secret](ctx, secret_model.FindSecretsOptions{OwnerID: ownerID})
	if err != nil {
		ctx.ServerError("FindSecrets", err)
		return
	}
	overridden := make(map[string]bool, len(repoSecrets))
	for _, s := range repoSecrets {
		overridden[s.Name] = true
	}

	ctx.Data["InheritedSecrets"] = inherited
	ctx.Data["OverriddenSecrets"] = overridden
}

func CreateSecretPost(ctx *context.Context, ownerID, repoID int64, redirectURL string) {
	form := web.GetForm(ctx).(*forms.CreateSecretForm)

	normalizedData := util.ReserveLineBreakForTextarea(form.Data)
	secret, err := secret_model.InsertEncryptedSecret(ctx, ownerID, repoID, form.Name, normalizedData)
	if err != nil {
		log.Error("InsertEncryptedSecret failed: %v", err)
		ctx.JSONError(ctx.Tr("secrets.creation.failed"))
		return
	}

	ctx.Flash.Success(ctx.Tr("secrets.creation.success", secret.Name))
	ctx.JSONRedirect(redirectURL)
}

func EditSecretPost(ctx *context.Context, ownerID, repoID, id int64, redirectURL string) {
	form := web.GetForm(ctx).(*forms.EditSecretForm)

	secret, err := secret_model.GetSecretByID(ctx, ownerID, repoID, id)
	if errors.Is(err, util.ErrNotExist) {
		ctx.NotFound("GetSecretByID", err)
		return
	} else if err != nil {
		ctx.ServerError("GetSecretByID", err)
		return
	}

	secret.Name = form.Name
	if form.Data != "" {
		secret.SetData(util.ReserveLineBreakForTextarea(form.Data))
	}

	err = secret_model.UpdateSecret(ctx, secret)
	if err != nil {
		log.Error("UpdateSecret failed: %v", err)
		ctx.JSONError(ctx.Tr("actions.secrets.mutation.failure_message", secret.Name))
		return
	}

	ctx.Flash.Success(ctx.Tr("actions.secrets.mutation.success_message", secret.Name))
	ctx.JSONRedirect(redirectURL)
}

func DeleteSecretPost(ctx *context.Context, ownerID, repoID, id int64, redirectURL string) {
	err := secrets_service.DeleteSecretByID(ctx, ownerID, repoID, id)
	if err != nil {
		log.Error("DeleteSecretByID(%d) failed: %v", id, err)
		ctx.JSONError(ctx.Tr("secrets.deletion.failed"))
		return
	}

	ctx.Flash.Success(ctx.Tr("secrets.deletion.success"))
	ctx.JSONRedirect(redirectURL)
}
