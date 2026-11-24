// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/eventsource"
	"forgejo.org/modules/setting"
	"forgejo.org/routers/web/auth"
	"forgejo.org/services/context"
)

// SignOutSessions signs the doer out of all browsers and revokes their long-term authorization tokens.
func SignOutSessions(ctx *context.Context) {
	if err := user_model.RevokeUserSessions(ctx, ctx.Doer); err != nil {
		ctx.ServerError("RevokeUserSessions", err)
		return
	}

	eventsource.GetManager().SendMessage(ctx.Doer.ID, &eventsource.Event{
		Name: "logout",
		Data: ctx.Session.ID(),
	})
	auth.HandleSignOut(ctx)
	ctx.JSONRedirect(setting.AppSubURL + "/")
}
