// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2018 The Gitea Authors. All rights reserved.
// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package setting

import (
	stdCtx "context"
	"fmt"
	"html/template"
	"net/http"
	"slices"
	"strings"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/modules/base"
	"forgejo.org/modules/log"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/web"
	"forgejo.org/routers/web/shared/user"
	"forgejo.org/services/authz"
	"forgejo.org/services/context"
	"forgejo.org/services/forms"

	"xorm.io/builder"
)

const (
	tplAccessTokenEdit base.TplName = "user/settings/access_token_edit"
)

func getSelectedRepos(ctx *context.Context, selectedReposRaw []string) ([]*repo_model.Repository, error) {
	ownerAndName := make([][2]string, len(selectedReposRaw))
	for i, selected := range selectedReposRaw {
		split := strings.SplitN(selected, "/", 2) // ownername/reponame
		if len(split) != 2 {
			return nil, fmt.Errorf("invalid selected_repo: %s", selected)
		}
		ownerAndName[i] = [2]string{split[0], split[1]}
	}

	repoSearch := &repo_model.SearchRepoOptions{
		OwnerAndName: ownerAndName,
		OrderBy:      db.SearchOrderByAlphabetically, // match sorting in loadAccessTokenCreateData for consistency
		Private:      true,
	}

	cond := repo_model.SearchRepositoryCondition(repoSearch)
	repos, _, err := repo_model.SearchRepositoryByCondition(ctx, repoSearch, cond, false)
	if err != nil {
		return nil, err
	} else if len(repos) != len(selectedReposRaw) {
		// One or more of the repositories couldn't be found by search by owner & name.
		// Keep error in sync w/ ~permission.HasAccess below to avoid data existence probing.
		return nil, fmt.Errorf("one or more of the repositories couldn't be found by owner & name")
	}

	selectedRepos := make([]*repo_model.Repository, len(selectedReposRaw))
	for i, repo := range repos {
		permission, err := access_model.GetUserRepoPermission(ctx, repo, ctx.Doer)
		if err != nil {
			return nil, err
		} else if !permission.HasAccess() {
			// Prevent data existence probing -- ensure this error is the exact same as the (len(repos) !=
			// len(selectedReposRaw)) case above
			return nil, fmt.Errorf("one or more of the repositories couldn't be found by owner & name")
		}
		selectedRepos[i] = repo
	}
	return selectedRepos, nil
}

func loadAccessTokenCreateData(ctx *context.Context) {
	ctx.Data["AccessTokenScopePublicOnly"] = string(auth_model.AccessTokenScopePublicOnly) // note: SliceUtils.Contains won't work in the template if this is a `auth_model.AccessTokenScope`, so it's cast to a string here

	categories := []string{
		"activitypub",
		"issue",
		"misc",
		"notification",
		"organization",
		"package",
		"project",
		"repository",
		"user",
	}
	if ctx.Doer.IsAdmin {
		categories = append(categories, "admin")
	}
	slices.Sort(categories)
	ctx.Data["Categories"] = categories

	// Awkward -- GET and POST use different form bindings for the reasons explained on NewAccessTokenGetForm -- and
	// this method can be called in both situations, on all GETs, and on some POSTs when validation errors occur.  So
	// both forms need to be handled here.
	getForm, isGet := web.GetForm(ctx).(*forms.NewAccessTokenGetForm)
	postForm, isPost := web.GetForm(ctx).(*forms.NewAccessTokenPostForm)

	if isGet {
		// Manage the result of adding or removing a repository before we do anything with `form.SelectedRepo`...
		changed := false
		if getForm.AddSelectedRepo != "" {
			getForm.SelectedRepo = append(getForm.SelectedRepo, getForm.AddSelectedRepo)
			changed = true
		}
		if getForm.RemoveSelectedRepo != "" {
			getForm.SelectedRepo = slices.DeleteFunc(
				getForm.SelectedRepo,
				func(r string) bool { return r == getForm.RemoveSelectedRepo },
			)
			changed = true
		}
		if changed {
			// We've changed getForm.SelectedRepo, but a reference to this slice was already present in `ctx.Data` (the
			// Bind middleware invokes AssignForm to put getForm values into `ctx.Data`).  Replace the reference:
			ctx.Data["selected_repo"] = getForm.SelectedRepo
		}
	}

	repoSearchText := ""
	if isGet {
		repoSearchText = getForm.RepoSearch
	}

	selectedReposRaw := []string{}
	if isGet {
		selectedReposRaw = getForm.SelectedRepo
	} else if isPost {
		selectedReposRaw = postForm.SelectedRepo
	}
	selectedRepos, err := getSelectedRepos(ctx, selectedReposRaw)
	if err != nil {
		ctx.Error(http.StatusBadRequest, "getSelectedRepos")
		return
	}
	ctx.Data["SelectedRepos"] = selectedRepos

	page := 1
	if isGet {
		// Pagination on the repo search has form submit buttons that send the `set_page` param.  It's then encoded into
		// the page in the hidden input `page` which we fall back to, if anything else causes a form get (eg. adding or
		// removing a repo).
		if getForm.SetPage > 0 {
			page = getForm.SetPage
		} else if getForm.Page > 0 {
			page = getForm.Page
		}
	}
	pageSize := 10
	repoSearch := &repo_model.SearchRepoOptions{
		Actor:    ctx.Doer,
		Keyword:  repoSearchText,
		Private:  true,
		Archived: optional.Some(false),

		// Restrict repositories to those owned by, or collaborated with, by the user.  Repo-specific access tokens
		// could theoretically be created on any public repository as well, but there wouldn't be much point to that and
		// it would really balloon the search results to an impractical number of repos.
		OwnerID: ctx.Doer.ID,

		ListOptions: db.ListOptions{
			Page:     page,
			PageSize: pageSize,
		},
		OrderBy: db.SearchOrderByAlphabetically, // match sorting in getSelectedRepos for consistency
	}
	cond := repo_model.SearchRepositoryCondition(repoSearch)
	// Exclude all the repos that are currently in `form.SelectedRepo` from the search, by omitting them from the search
	// condition.  This prevents the UI from displaying the same repo on the left and right, and maintains the repo
	// search and page-size correctly.
	for _, selected := range selectedRepos {
		cond = cond.And(builder.Neq{"id": selected.ID})
	}
	repos, count, err := repo_model.SearchRepositoryByCondition(ctx, repoSearch, cond, false)
	if err != nil {
		log.Error("SearchRepository: %v", err)
		ctx.JSON(http.StatusInternalServerError, nil)
		return
	}
	ctx.Data["Repos"] = repos

	pager := context.NewPagination(int(count), pageSize, page, 3)
	pager.SetDefaultParams(ctx)
	ctx.Data["Page"] = pager

	autofocus := ""
	if isGet {
		switch {
		// Token name will be autofocused the first time the page is loaded -- if form.Scope is empty then that would be
		// a good sign it's the first load.
		case len(getForm.Scope) == 0:
			autofocus = "name"
		// After submitting a search, refocus the search text box.  Search invokes set_page=1 to reset the pagination
		// which we'll use to detect this case.
		case getForm.SetPage == 1:
			autofocus = "search"
		}
	}
	ctx.Data["Autofocus"] = autofocus
}

// Applications render manage access token page
func AccessTokenCreate(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("settings.applications")
	ctx.Data["PageIsSettingsApplications"] = true

	loadAccessTokenCreateData(ctx)
	if ctx.Written() {
		return
	}

	ctx.HTML(http.StatusOK, tplAccessTokenEdit)
}

// ApplicationsPost response for add user's access token
func AccessTokenCreatePost(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.NewAccessTokenPostForm)
	ctx.Data["Title"] = ctx.Tr("settings")
	ctx.Data["PageIsSettingsApplications"] = true

	renderWithError := func(msg template.HTML) {
		loadAccessTokenCreateData(ctx)
		if ctx.Written() {
			return
		}
		ctx.RenderWithErr(msg, tplAccessTokenEdit, form)
	}

	if ctx.HasError() {
		loadAccessTokenCreateData(ctx)
		if ctx.Written() {
			return
		}
		ctx.HTML(http.StatusOK, tplAccessTokenEdit)
		return
	}

	scope, err := form.GetScope()
	if err != nil {
		ctx.ServerError("GetScope", err)
		return
	}
	if !scope.HasPermissionScope() {
		renderWithError(ctx.Tr("settings.at_least_one_permission"))
		return
	}

	t := &auth_model.AccessToken{
		UID:   ctx.Doer.ID,
		Name:  form.Name,
		Scope: scope,
	}

	var resourceRepos []*auth_model.AccessTokenResourceRepo
	switch form.Resource {
	case "all":
		t.ResourceAllRepos = true
	case "public-only":
		t.ResourceAllRepos = true
		newScopeUnnormalized := fmt.Sprintf("%s,%s", scope, auth_model.AccessTokenScopePublicOnly)
		newScope, err := auth_model.AccessTokenScope(newScopeUnnormalized).Normalize()
		if err != nil {
			ctx.ServerError("AccessTokenScope.Normalize", err)
			return
		}
		t.Scope = newScope
	case "repo-specific":
		t.ResourceAllRepos = false
		selectedRepos, err := getSelectedRepos(ctx, form.SelectedRepo)
		if err != nil {
			ctx.Error(http.StatusBadRequest, "getSelectedRepos")
			return
		}
		for _, repo := range selectedRepos {
			resourceRepos = append(resourceRepos, &auth_model.AccessTokenResourceRepo{RepoID: repo.ID})
		}
	}

	exist, err := auth_model.AccessTokenByNameExists(ctx, t)
	if err != nil {
		ctx.ServerError("AccessTokenByNameExists", err)
		return
	} else if exist {
		renderWithError(ctx.Tr("settings.generate_token_name_duplicate", t.Name))
		return
	}

	if err := authz.ValidateAccessToken(t, resourceRepos); err != nil {
		s := user.TranslateAccessTokenValidationError(ctx.Base, err)
		if has, str := s.Get(); has {
			renderWithError(template.HTML(template.HTMLEscapeString(str)))
			return
		}
		ctx.ServerError("ValidateAccessToken", err)
		return
	}

	err = db.WithTx(ctx, func(ctx stdCtx.Context) error {
		if err := auth_model.NewAccessToken(ctx, t); err != nil {
			return err
		}
		return auth_model.InsertAccessTokenResourceRepos(ctx, t.ID, resourceRepos)
	})
	if err != nil {
		ctx.ServerError("NewAccessToken", err)
		return
	}

	ctx.Flash.Success(ctx.Tr("settings.generate_token_success"))
	ctx.Flash.Info(t.Token)

	ctx.Redirect(setting.AppSubURL + "/user/settings/applications")
}

// DeleteAccessToken response for delete user access token
func DeleteAccessToken(ctx *context.Context) {
	if err := auth_model.DeleteAccessTokenByID(ctx, ctx.FormInt64("id"), ctx.Doer.ID); err != nil {
		ctx.Flash.Error("DeleteAccessTokenByID: " + err.Error())
	} else {
		ctx.Flash.Success(ctx.Tr("settings.delete_token_success"))
	}

	ctx.JSONRedirect(setting.AppSubURL + "/user/settings/applications")
}

// RegenerateAccessToken response for regenerating user access token
func RegenerateAccessToken(ctx *context.Context) {
	if t, err := auth_model.RegenerateAccessTokenByID(ctx, ctx.FormInt64("id"), ctx.Doer.ID); err != nil {
		if auth_model.IsErrAccessTokenNotExist(err) {
			ctx.Flash.Error(ctx.Tr("error.not_found"))
		} else {
			ctx.Flash.Error(ctx.Tr("error.server_internal"))
			log.Error("DeleteAccessTokenByID", err)
		}
	} else {
		ctx.Flash.Success(ctx.Tr("settings.regenerate_token_success"))
		ctx.Flash.Info(t.Token)
	}

	ctx.JSONRedirect(setting.AppSubURL + "/user/settings/applications")
}
