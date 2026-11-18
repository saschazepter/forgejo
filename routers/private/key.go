// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package private

import (
	"net/http"

	asymkey_model "forgejo.org/models/asymkey"
	"forgejo.org/models/db"
	"forgejo.org/modules/private"
	"forgejo.org/modules/timeutil"
	"forgejo.org/services/context"
)

// UpdatePublicKeyInRepo update public key and deploy key updates
func UpdatePublicKeyInRepo(ctx *context.PrivateContext) {
	keyID := ctx.ParamsInt64(":id")
	repoID := ctx.ParamsInt64(":repoid")
	if err := asymkey_model.UpdatePublicKeyUpdated(ctx, keyID); err != nil {
		ctx.JSON(http.StatusInternalServerError, private.Response{
			Err: err.Error(),
		})
		return
	}

	deployKey, err := asymkey_model.GetDeployKeyByRepo(ctx, keyID, repoID)
	if err != nil {
		if asymkey_model.IsErrDeployKeyNotExist(err) {
			ctx.PlainText(http.StatusOK, "success")
			return
		}
		ctx.JSON(http.StatusInternalServerError, private.Response{
			Err: err.Error(),
		})
		return
	}
	deployKey.UpdatedUnix = timeutil.TimeStampNow()
	if err = asymkey_model.UpdateDeployKeyCols(ctx, deployKey, "updated_unix"); err != nil {
		ctx.JSON(http.StatusInternalServerError, private.Response{
			Err: err.Error(),
		})
		return
	}

	ctx.PlainText(http.StatusOK, "success")
}

// AuthorizedPublicKeyByFingerprint finds a public key via its fingerprint.
// The output is compatible with "AUTHORIZED_KEYS FILE FORMAT" in sshd(8).
func AuthorizedPublicKeyByFingerprint(ctx *context.PrivateContext) {
	publicKey, exists, err := db.Get[asymkey_model.PublicKey](ctx, asymkey_model.FindPublicKeyOptions{
		Fingerprint: ctx.FormString("content"),
	}.ToConds())
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, private.Response{
			Err: err.Error(),
		})
		return
	}
	if !exists {
		ctx.PlainText(http.StatusOK, "# No key found")
		return
	}

	ctx.PlainText(http.StatusOK, publicKey.AuthorizedString())
}
