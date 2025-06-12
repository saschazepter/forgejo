// Copyright 2015 The Gogs Authors. All rights reserved.
// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"

	"forgejo.org/models/db"
	"forgejo.org/models/perm"
	access_model "forgejo.org/models/perm/access"
	user_model "forgejo.org/models/user"
	"forgejo.org/models/webhook"
	"forgejo.org/modules/base"
	"forgejo.org/modules/git"
	"forgejo.org/modules/json"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/web/middleware"
	webhook_module "forgejo.org/modules/webhook"
	"forgejo.org/services/context"
	"forgejo.org/services/convert"
	"forgejo.org/services/forms"
	webhook_service "forgejo.org/services/webhook"

	"code.forgejo.org/go-chi/binding"
)

const (
	tplHooks        base.TplName = "repo/settings/webhook/base"
	tplHookNew      base.TplName = "repo/settings/webhook/new"
	tplOrgHookNew   base.TplName = "org/settings/hook_new"
	tplUserHookNew  base.TplName = "user/settings/hook_new"
	tplAdminHookNew base.TplName = "admin/hook_new"
)

// WebhookList render web hooks list page
func WebhookList(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.settings.hooks")
	ctx.Data["PageIsSettingsHooks"] = true
	ctx.Data["BaseLink"] = ctx.Repo.RepoLink + "/settings/hooks"
	ctx.Data["BaseLinkNew"] = ctx.Repo.RepoLink + "/settings/hooks"
	ctx.Data["WebhookList"] = webhook_service.List()
	ctx.Data["Description"] = ctx.Tr("repo.settings.hooks_desc", "https://forgejo.org/docs/latest/user/webhooks/")

	ws, err := db.Find[webhook.Webhook](ctx, webhook.ListWebhookOptions{RepoID: ctx.Repo.Repository.ID})
	if err != nil {
		ctx.ServerError("GetWebhooksByRepoID", err)
		return
	}
	ctx.Data["Webhooks"] = ws

	ctx.HTML(http.StatusOK, tplHooks)
}

type ownerRepoCtx struct {
	OwnerID         int64
	RepoID          int64
	IsAdmin         bool
	IsSystemWebhook bool
	Link            string
	LinkNew         string
	NewTemplate     base.TplName
}

// getOwnerRepoCtx determines whether this is a repo, owner, or admin (both default and system) context.
func getOwnerRepoCtx(ctx *context.Context) (*ownerRepoCtx, error) {
	if ctx.Data["PageIsRepoSettings"] == true {
		return &ownerRepoCtx{
			RepoID:      ctx.Repo.Repository.ID,
			Link:        path.Join(ctx.Repo.RepoLink, "settings/hooks"),
			LinkNew:     path.Join(ctx.Repo.RepoLink, "settings/hooks"),
			NewTemplate: tplHookNew,
		}, nil
	}

	if ctx.Data["PageIsOrgSettings"] == true {
		return &ownerRepoCtx{
			OwnerID:     ctx.ContextUser.ID,
			Link:        path.Join(ctx.Org.OrgLink, "settings/hooks"),
			LinkNew:     path.Join(ctx.Org.OrgLink, "settings/hooks"),
			NewTemplate: tplOrgHookNew,
		}, nil
	}

	if ctx.Data["PageIsUserSettings"] == true {
		return &ownerRepoCtx{
			OwnerID:     ctx.Doer.ID,
			Link:        path.Join(setting.AppSubURL, "/user/settings/hooks"),
			LinkNew:     path.Join(setting.AppSubURL, "/user/settings/hooks"),
			NewTemplate: tplUserHookNew,
		}, nil
	}

	if ctx.Data["PageIsAdmin"] == true {
		return &ownerRepoCtx{
			IsAdmin:         true,
			IsSystemWebhook: ctx.Params(":configType") == "system-hooks",
			Link:            path.Join(setting.AppSubURL, "/admin/hooks"),
			LinkNew:         path.Join(setting.AppSubURL, "/admin/", ctx.Params(":configType")),
			NewTemplate:     tplAdminHookNew,
		}, nil
	}

	return nil, errors.New("unable to set OwnerRepo context")
}

// WebhookNew render creating webhook page
func WebhookNew(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.settings.add_webhook")
	ctx.Data["Webhook"] = webhook.Webhook{HookEvent: &webhook_module.HookEvent{}}

	orCtx, err := getOwnerRepoCtx(ctx)
	if err != nil {
		ctx.ServerError("getOwnerRepoCtx", err)
		return
	}

	if orCtx.IsAdmin && orCtx.IsSystemWebhook {
		ctx.Data["PageIsAdminSystemHooks"] = true
		ctx.Data["PageIsAdminSystemHooksNew"] = true
	} else if orCtx.IsAdmin {
		ctx.Data["PageIsAdminDefaultHooks"] = true
		ctx.Data["PageIsAdminDefaultHooksNew"] = true
	} else {
		ctx.Data["PageIsSettingsHooks"] = true
		ctx.Data["PageIsSettingsHooksNew"] = true
	}

	hookType := ctx.Params(":type")
	handler := webhook_service.GetWebhookHandler(hookType)
	if handler == nil {
		ctx.NotFound("GetWebhookHandler", nil)
		return
	}
	ctx.Data["HookType"] = hookType
	ctx.Data["WebhookHandler"] = handler
	ctx.Data["BaseLink"] = orCtx.LinkNew
	ctx.Data["BaseLinkNew"] = orCtx.LinkNew
	ctx.Data["WebhookList"] = webhook_service.List()

	ctx.HTML(http.StatusOK, orCtx.NewTemplate)
}

// ParseHookEvent convert web form content to webhook.HookEvent
func ParseHookEvent(form forms.WebhookCoreForm) *webhook_module.HookEvent {
	return &webhook_module.HookEvent{
		PushOnly:       form.PushOnly(),
		SendEverything: form.SendEverything(),
		ChooseEvents:   form.ChooseEvents(),
		HookEvents: webhook_module.HookEvents{
			Create:                   form.Create,
			Delete:                   form.Delete,
			Fork:                     form.Fork,
			Issues:                   form.Issues,
			IssueAssign:              form.IssueAssign,
			IssueLabel:               form.IssueLabel,
			IssueMilestone:           form.IssueMilestone,
			IssueComment:             form.IssueComment,
			Release:                  form.Release,
			Push:                     form.Push,
			PullRequest:              form.PullRequest,
			PullRequestAssign:        form.PullRequestAssign,
			PullRequestLabel:         form.PullRequestLabel,
			PullRequestMilestone:     form.PullRequestMilestone,
			PullRequestComment:       form.PullRequestComment,
			PullRequestReview:        form.PullRequestReview,
			PullRequestSync:          form.PullRequestSync,
			PullRequestReviewRequest: form.PullRequestReviewRequest,
			Wiki:                     form.Wiki,
			Repository:               form.Repository,
			Package:                  form.Package,
			ActionRunFailure:         form.ActionFailure,
			ActionRunRecover:         form.ActionRecover,
			ActionRunSuccess:         form.ActionSuccess,
		},
		BranchFilter: form.BranchFilter,
	}
}

func WebhookCreate(ctx *context.Context) {
	hookType := ctx.Params(":type")
	handler := webhook_service.GetWebhookHandler(hookType)
	if handler == nil {
		ctx.NotFound("GetWebhookHandler", nil)
		return
	}

	fields := handler.UnmarshalForm(func(form any) {
		errs := binding.Bind(ctx.Req, form)
		middleware.Validate(errs, ctx.Data, form, ctx.Locale) // error checked below in ctx.HasError
	})

	ctx.Data["Title"] = ctx.Tr("repo.settings.add_webhook")
	ctx.Data["PageIsSettingsHooks"] = true
	ctx.Data["PageIsSettingsHooksNew"] = true
	ctx.Data["Webhook"] = webhook.Webhook{HookEvent: &webhook_module.HookEvent{}}
	ctx.Data["HookType"] = hookType
	ctx.Data["WebhookHandler"] = handler

	orCtx, err := getOwnerRepoCtx(ctx)
	if err != nil {
		ctx.ServerError("getOwnerRepoCtx", err)
		return
	}
	ctx.Data["BaseLink"] = orCtx.LinkNew
	ctx.Data["BaseLinkNew"] = orCtx.LinkNew
	ctx.Data["WebhookList"] = webhook_service.List()

	if ctx.HasError() {
		// pre-fill the form with the submitted data
		var w webhook.Webhook
		w.URL = fields.URL
		w.ContentType = fields.ContentType
		w.Secret = fields.Secret
		w.HookEvent = ParseHookEvent(fields.WebhookCoreForm)
		w.IsActive = fields.Active
		w.HTTPMethod = fields.HTTPMethod
		err := w.SetHeaderAuthorization(fields.AuthorizationHeader)
		if err != nil {
			ctx.ServerError("SetHeaderAuthorization", err)
			return
		}
		ctx.Data["Webhook"] = w
		ctx.Data["HookMetadata"] = fields.Metadata

		ctx.HTML(http.StatusUnprocessableEntity, orCtx.NewTemplate)
		return
	}

	var meta []byte
	if fields.Metadata != nil {
		meta, err = json.Marshal(fields.Metadata)
		if err != nil {
			ctx.ServerError("Marshal", err)
			return
		}
	}

	w := &webhook.Webhook{
		RepoID:          orCtx.RepoID,
		URL:             fields.URL,
		HTTPMethod:      fields.HTTPMethod,
		ContentType:     fields.ContentType,
		Secret:          fields.Secret,
		HookEvent:       ParseHookEvent(fields.WebhookCoreForm),
		IsActive:        fields.Active,
		Type:            hookType,
		Meta:            string(meta),
		OwnerID:         orCtx.OwnerID,
		IsSystemWebhook: orCtx.IsSystemWebhook,
	}
	err = w.SetHeaderAuthorization(fields.AuthorizationHeader)
	if err != nil {
		ctx.ServerError("SetHeaderAuthorization", err)
		return
	}
	if err := w.UpdateEvent(); err != nil {
		ctx.ServerError("UpdateEvent", err)
		return
	} else if err := webhook.CreateWebhook(ctx, w); err != nil {
		ctx.ServerError("CreateWebhook", err)
		return
	}

	ctx.Flash.Success(ctx.Tr("repo.settings.add_hook_success"))
	ctx.Redirect(orCtx.Link)
}

func WebhookUpdate(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.settings.update_webhook")
	ctx.Data["PageIsSettingsHooks"] = true
	ctx.Data["PageIsSettingsHooksEdit"] = true

	orCtx, w := checkWebhook(ctx)
	if ctx.Written() {
		return
	}
	ctx.Data["Webhook"] = w

	handler := webhook_service.GetWebhookHandler(w.Type)
	if handler == nil {
		ctx.NotFound("GetWebhookHandler", nil)
		return
	}

	fields := handler.UnmarshalForm(func(form any) {
		errs := binding.Bind(ctx.Req, form)
		middleware.Validate(errs, ctx.Data, form, ctx.Locale) // error checked below in ctx.HasError
	})

	// pre-fill the form with the submitted data
	w.URL = fields.URL
	w.ContentType = fields.ContentType
	w.Secret = fields.Secret
	w.HookEvent = ParseHookEvent(fields.WebhookCoreForm)
	w.IsActive = fields.Active
	w.HTTPMethod = fields.HTTPMethod

	err := w.SetHeaderAuthorization(fields.AuthorizationHeader)
	if err != nil {
		ctx.ServerError("SetHeaderAuthorization", err)
		return
	}

	if ctx.HasError() {
		ctx.Data["HookMetadata"] = fields.Metadata
		ctx.HTML(http.StatusUnprocessableEntity, orCtx.NewTemplate)
		return
	}

	var meta []byte
	if fields.Metadata != nil {
		meta, err = json.Marshal(fields.Metadata)
		if err != nil {
			ctx.ServerError("Marshal", err)
			return
		}
	}

	w.Meta = string(meta)

	if err := w.UpdateEvent(); err != nil {
		ctx.ServerError("UpdateEvent", err)
		return
	} else if err := webhook.UpdateWebhook(ctx, w); err != nil {
		ctx.ServerError("UpdateWebhook", err)
		return
	}

	ctx.Flash.Success(ctx.Tr("repo.settings.update_hook_success"))
	ctx.Redirect(fmt.Sprintf("%s/%d", orCtx.Link, w.ID))
}

func checkWebhook(ctx *context.Context) (*ownerRepoCtx, *webhook.Webhook) {
	orCtx, err := getOwnerRepoCtx(ctx)
	if err != nil {
		ctx.ServerError("getOwnerRepoCtx", err)
		return nil, nil
	}
	ctx.Data["BaseLink"] = orCtx.Link
	ctx.Data["BaseLinkNew"] = orCtx.LinkNew
	ctx.Data["WebhookList"] = webhook_service.List()

	var w *webhook.Webhook
	if orCtx.RepoID > 0 {
		w, err = webhook.GetWebhookByRepoID(ctx, orCtx.RepoID, ctx.ParamsInt64(":id"))
	} else if orCtx.OwnerID > 0 {
		w, err = webhook.GetWebhookByOwnerID(ctx, orCtx.OwnerID, ctx.ParamsInt64(":id"))
	} else if orCtx.IsAdmin {
		w, err = webhook.GetSystemOrDefaultWebhook(ctx, ctx.ParamsInt64(":id"))
	}
	if err != nil || w == nil {
		if webhook.IsErrWebhookNotExist(err) {
			ctx.NotFound("GetWebhookByID", nil)
		} else {
			ctx.ServerError("GetWebhookByID", err)
		}
		return nil, nil
	}

	ctx.Data["HookType"] = w.Type

	if handler := webhook_service.GetWebhookHandler(w.Type); handler != nil {
		ctx.Data["HookMetadata"] = handler.Metadata(w)
		ctx.Data["WebhookHandler"] = handler
	}

	ctx.Data["History"], err = w.History(ctx, 1)
	if err != nil {
		ctx.ServerError("History", err)
	}
	return orCtx, w
}

// WebhookEdit render editing web hook page
func WebhookEdit(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.settings.update_webhook")
	ctx.Data["PageIsSettingsHooks"] = true
	ctx.Data["PageIsSettingsHooksEdit"] = true

	orCtx, w := checkWebhook(ctx)
	if ctx.Written() {
		return
	}
	ctx.Data["Webhook"] = w

	ctx.HTML(http.StatusOK, orCtx.NewTemplate)
}

// WebhookTest test if web hook is work fine
func WebhookTest(ctx *context.Context) {
	hookID := ctx.ParamsInt64(":id")
	w, err := webhook.GetWebhookByRepoID(ctx, ctx.Repo.Repository.ID, hookID)
	if err != nil {
		ctx.Flash.Error("GetWebhookByRepoID: " + err.Error())
		ctx.Status(http.StatusInternalServerError)
		return
	}

	// Grab latest commit or fake one if it's empty repository.
	commit := ctx.Repo.Commit
	if commit == nil {
		ghost := user_model.NewGhostUser()
		objectFormat := git.ObjectFormatFromName(ctx.Repo.Repository.ObjectFormatName)
		commit = &git.Commit{
			ID:            objectFormat.EmptyObjectID(),
			Author:        ghost.NewGitSig(),
			Committer:     ghost.NewGitSig(),
			CommitMessage: "This is a fake commit",
		}
	}

	apiUser := convert.ToUserWithAccessMode(ctx, ctx.Doer, perm.AccessModeNone)

	apiCommit := &api.PayloadCommit{
		ID:      commit.ID.String(),
		Message: commit.Message(),
		URL:     ctx.Repo.Repository.HTMLURL() + "/commit/" + url.PathEscape(commit.ID.String()),
		Author: &api.PayloadUser{
			Name:  commit.Author.Name,
			Email: commit.Author.Email,
		},
		Committer: &api.PayloadUser{
			Name:  commit.Committer.Name,
			Email: commit.Committer.Email,
		},
	}

	commitID := commit.ID.String()
	p := &api.PushPayload{
		Ref:          git.BranchPrefix + ctx.Repo.Repository.DefaultBranch,
		Before:       commitID,
		After:        commitID,
		CompareURL:   setting.AppURL + ctx.Repo.Repository.ComposeCompareURL(commitID, commitID),
		Commits:      []*api.PayloadCommit{apiCommit},
		TotalCommits: 1,
		HeadCommit:   apiCommit,
		Repo:         convert.ToRepo(ctx, ctx.Repo.Repository, access_model.Permission{AccessMode: perm.AccessModeNone}),
		Pusher:       apiUser,
		Sender:       apiUser,
	}
	if err := webhook_service.PrepareWebhook(ctx, w, webhook_module.HookEventPush, p); err != nil {
		ctx.Flash.Error("PrepareWebhook: " + err.Error())
		ctx.Status(http.StatusInternalServerError)
	} else {
		ctx.Flash.Info(ctx.Tr("repo.settings.webhook.delivery.success"))
		ctx.Status(http.StatusOK)
	}
}

// WebhookReplay replays a webhook
func WebhookReplay(ctx *context.Context) {
	hookTaskUUID := ctx.Params(":uuid")

	orCtx, w := checkWebhook(ctx)
	if ctx.Written() {
		return
	}

	if err := webhook_service.ReplayHookTask(ctx, w, hookTaskUUID); err != nil {
		if webhook.IsErrHookTaskNotExist(err) {
			ctx.NotFound("ReplayHookTask", nil)
		} else {
			ctx.ServerError("ReplayHookTask", err)
		}
		return
	}

	ctx.Flash.Success(ctx.Tr("repo.settings.webhook.delivery.success"))
	ctx.Redirect(fmt.Sprintf("%s/%d", orCtx.Link, w.ID))
}

// WebhookDelete delete a webhook
func WebhookDelete(ctx *context.Context) {
	if err := webhook.DeleteWebhookByRepoID(ctx, ctx.Repo.Repository.ID, ctx.FormInt64("id")); err != nil {
		ctx.Flash.Error("DeleteWebhookByRepoID: " + err.Error())
	} else {
		ctx.Flash.Success(ctx.Tr("repo.settings.webhook_deletion_success"))
	}

	ctx.JSONRedirect(ctx.Repo.RepoLink + "/settings/hooks")
}
