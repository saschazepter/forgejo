// Copyright 2015 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// Copyright 2023 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package user

import (
	"errors"
	"fmt"
	gotemplate "html/template"
	"io"
	"net/http"
	"path"
	"strings"

	activities_model "forgejo.org/models/activities"
	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/base"
	"forgejo.org/modules/git"
	"forgejo.org/modules/log"
	"forgejo.org/modules/markup"
	"forgejo.org/modules/markup/markdown"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/util"
	"forgejo.org/routers/web/feed"
	"forgejo.org/routers/web/org"
	shared_user "forgejo.org/routers/web/shared/user"
	"forgejo.org/services/context"
	user_service "forgejo.org/services/user"
)

const (
	tplProfileBigAvatar base.TplName = "shared/user/profile_big_avatar"
	tplFollowUnfollow   base.TplName = "org/follow_unfollow"
)

// OwnerProfile render profile page for a user or a organization (aka, repo owner)
func OwnerProfile(ctx *context.Context) {
	if strings.Contains(ctx.Req.Header.Get("Accept"), "application/rss+xml") {
		feed.ShowUserFeedRSS(ctx)
		return
	}
	if strings.Contains(ctx.Req.Header.Get("Accept"), "application/atom+xml") {
		feed.ShowUserFeedAtom(ctx)
		return
	}

	if ctx.ContextUser.IsOrganization() {
		org.Home(ctx)
	} else {
		userProfile(ctx)
	}
}

func userProfile(ctx *context.Context) {
	// check view permissions
	if !user_model.IsUserVisibleToViewer(ctx, ctx.ContextUser, ctx.Doer) {
		ctx.NotFound("User not visible", nil)
		return
	}

	ctx.Data["Title"] = ctx.ContextUser.DisplayName()
	ctx.Data["PageIsUserProfile"] = true

	ctx.Data["OpenGraphTitle"] = ctx.ContextUser.DisplayName()
	ctx.Data["OpenGraphType"] = "profile"
	ctx.Data["OpenGraphImageURL"] = ctx.ContextUser.AvatarLink(ctx)
	ctx.Data["OpenGraphURL"] = ctx.ContextUser.HTMLURL()
	ctx.Data["OpenGraphDescription"] = ctx.ContextUser.Description

	profileDbRepo, profileGitRepo, profileReadmeBlob, profileClose := shared_user.FindUserProfileReadme(ctx, ctx.Doer)
	defer profileClose()

	showPrivate := ctx.IsSigned && (ctx.Doer.IsAdmin || ctx.Doer.ID == ctx.ContextUser.ID)
	prepareUserProfileTabData(ctx, showPrivate, profileDbRepo, profileGitRepo, profileReadmeBlob)
	// call PrepareContextForProfileBigAvatar later to avoid re-querying the NumFollowers & NumFollowing
	shared_user.PrepareContextForProfileBigAvatar(ctx)
	ctx.HTML(http.StatusOK, tplProfile)
}

func prepareUserProfileTabData(ctx *context.Context, showPrivate bool, profileDbRepo *repo_model.Repository, profileGitRepo *git.Repository, profileReadme *git.Blob) {
	// if there is a profile readme, default to "overview" page, otherwise, default to "repositories" page
	// if there is not a profile readme, the overview tab should be treated as the repositories tab
	tab := ctx.FormString("tab")
	if tab == "" || tab == "overview" {
		if profileReadme != nil {
			tab = "overview"
		} else {
			tab = "repositories"
		}
	}
	ctx.Data["TabName"] = tab
	ctx.Data["HasProfileReadme"] = profileReadme != nil

	page := ctx.FormInt("page")
	if page <= 0 {
		page = 1
	}

	pagingNum := setting.UI.User.RepoPagingNum
	topicOnly := ctx.FormBool("topic")
	var (
		repos   []*repo_model.Repository
		count   int64
		total   int
		orderBy db.SearchOrderBy
	)

	sortOrder := ctx.FormString("sort")
	if _, ok := repo_model.OrderByFlatMap[sortOrder]; !ok {
		sortOrder = setting.UI.ExploreDefaultSort // TODO: add new default sort order for user home?
	}
	ctx.Data["SortType"] = sortOrder
	orderBy = repo_model.OrderByFlatMap[sortOrder]

	keyword := ctx.FormTrim("q")
	ctx.Data["Keyword"] = keyword

	language := ctx.FormTrim("language")
	ctx.Data["Language"] = language

	followers, numFollowers, err := user_model.GetUserFollowers(ctx, ctx.ContextUser, ctx.Doer, db.ListOptions{
		PageSize: pagingNum,
		Page:     page,
	})
	if err != nil {
		ctx.ServerError("GetUserFollowers", err)
		return
	}
	ctx.Data["NumFollowers"] = numFollowers
	following, numFollowing, err := user_model.GetUserFollowing(ctx, ctx.ContextUser, ctx.Doer, db.ListOptions{
		PageSize: pagingNum,
		Page:     page,
	})
	if err != nil {
		ctx.ServerError("GetUserFollowing", err)
		return
	}
	ctx.Data["NumFollowing"] = numFollowing

	archived := ctx.FormOptionalBool("archived")
	ctx.Data["IsArchived"] = archived

	fork := ctx.FormOptionalBool("fork")
	ctx.Data["IsFork"] = fork

	mirror := ctx.FormOptionalBool("mirror")
	ctx.Data["IsMirror"] = mirror

	template := ctx.FormOptionalBool("template")
	ctx.Data["IsTemplate"] = template

	private := ctx.FormOptionalBool("private")
	ctx.Data["IsPrivate"] = private

	switch tab {
	case "followers":
		ctx.Data["Cards"] = followers
		total = int(numFollowers)
		ctx.Data["CardsTitle"] = ctx.TrN(total, "user.followers.title.one", "user.followers.title.few")
		if ctx.IsSigned && ctx.ContextUser.ID == ctx.Doer.ID {
			ctx.Data["CardsNoneMsg"] = ctx.Tr("followers.incoming.list.self.none")
		} else {
			ctx.Data["CardsNoneMsg"] = ctx.Tr("followers.incoming.list.none")
		}
	case "following":
		ctx.Data["Cards"] = following
		total = int(numFollowing)
		ctx.Data["CardsTitle"] = ctx.TrN(total, "user.following.title.one", "user.following.title.few")
		if ctx.IsSigned && ctx.ContextUser.ID == ctx.Doer.ID {
			ctx.Data["CardsNoneMsg"] = ctx.Tr("followers.outgoing.list.self.none")
		} else {
			ctx.Data["CardsNoneMsg"] = ctx.Tr("followers.outgoing.list.none", ctx.ContextUser.Name)
		}
	case "activity":
		// prepare heatmap data
		if setting.Service.EnableUserHeatmap {
			data, err := activities_model.GetUserHeatmapDataByUser(ctx, ctx.ContextUser, ctx.Doer, nil)
			if err != nil {
				ctx.ServerError("GetUserHeatmapDataByUser", err)
				return
			}
			ctx.Data["HeatmapData"] = data
			ctx.Data["HeatmapTotalContributions"] = activities_model.GetTotalContributionsInHeatmap(data)
		}

		date := ctx.FormString("date")
		pagingNum = setting.UI.FeedPagingNum
		items, count, err := activities_model.GetFeeds(ctx, activities_model.GetFeedsOptions{
			RequestedUser:   ctx.ContextUser,
			Actor:           ctx.Doer,
			IncludePrivate:  showPrivate,
			OnlyPerformedBy: true,
			Date:            date,
			ListOptions: db.ListOptions{
				PageSize: pagingNum,
				Page:     page,
			},
		})
		if err != nil {
			ctx.ServerError("GetFeeds", err)
			return
		}
		ctx.Data["Feeds"] = items
		ctx.Data["Date"] = date

		total = int(count)
	case "stars":
		ctx.Data["PageIsProfileStarList"] = true
		repos, count, err = repo_model.SearchRepository(ctx, &repo_model.SearchRepoOptions{
			ListOptions: db.ListOptions{
				PageSize: pagingNum,
				Page:     page,
			},
			Actor:              ctx.Doer,
			Keyword:            keyword,
			OrderBy:            orderBy,
			Private:            ctx.IsSigned,
			StarredByID:        ctx.ContextUser.ID,
			Collaborate:        optional.Some(false),
			TopicOnly:          topicOnly,
			Language:           language,
			IncludeDescription: setting.UI.SearchRepoDescription,
			Archived:           archived,
			Fork:               fork,
			Mirror:             mirror,
			Template:           template,
			IsPrivate:          private,
		})
		if err != nil {
			ctx.ServerError("SearchRepository", err)
			return
		}

		total = int(count)
	case "watching":
		repos, count, err = repo_model.SearchRepository(ctx, &repo_model.SearchRepoOptions{
			ListOptions: db.ListOptions{
				PageSize: pagingNum,
				Page:     page,
			},
			Actor:              ctx.Doer,
			Keyword:            keyword,
			OrderBy:            orderBy,
			Private:            ctx.IsSigned,
			WatchedByID:        ctx.ContextUser.ID,
			Collaborate:        optional.Some(false),
			TopicOnly:          topicOnly,
			Language:           language,
			IncludeDescription: setting.UI.SearchRepoDescription,
			Archived:           archived,
			Fork:               fork,
			Mirror:             mirror,
			Template:           template,
			IsPrivate:          private,
		})
		if err != nil {
			ctx.ServerError("SearchRepository", err)
			return
		}

		total = int(count)
	case "overview":
		if rc, _, err := profileReadme.NewTruncatedReader(setting.UI.MaxDisplayFileSize); err != nil {
			log.Error("failed to NewTruncatedReader: %v", err)
		} else {
			defer rc.Close()

			if markupType := markup.Type(profileReadme.Name()); markupType != "" {
				if profileContent, err := markdown.RenderReader(&markup.RenderContext{
					Ctx:     ctx,
					Type:    markupType,
					GitRepo: profileGitRepo,
					Links: markup.Links{
						// Give the repo link to the markdown render for the full link of media element.
						// the media link usually be like /[user]/[repoName]/media/branch/[branchName],
						// 	Eg. /Tom/.profile/media/branch/main
						// The branch shown on the profile page is the default branch, this need to be in sync with doc, see:
						//	https://docs.gitea.com/usage/profile-readme
						Base:       profileDbRepo.Link(),
						BranchPath: path.Join("branch", util.PathEscapeSegments(profileDbRepo.DefaultBranch)),
					},
					Metas: map[string]string{"mode": "document"},
				}, rc); err != nil {
					log.Error("failed to RenderString: %v", err)
				} else {
					ctx.Data["ProfileReadme"] = profileContent
				}
			} else {
				content, err := io.ReadAll(rc)
				if err != nil {
					log.Error("Read readme content failed: %v", err)
				}
				ctx.Data["ProfileReadme"] = gotemplate.HTMLEscapeString(util.UnsafeBytesToString(content))
				ctx.Data["IsProfileReadmePlain"] = true
			}
		}
	default: // default to "repositories"
		repos, count, err = repo_model.SearchRepository(ctx, &repo_model.SearchRepoOptions{
			ListOptions: db.ListOptions{
				PageSize: pagingNum,
				Page:     page,
			},
			Actor:              ctx.Doer,
			Keyword:            keyword,
			OwnerID:            ctx.ContextUser.ID,
			OrderBy:            orderBy,
			Private:            ctx.IsSigned,
			Collaborate:        optional.Some(false),
			TopicOnly:          topicOnly,
			Language:           language,
			IncludeDescription: setting.UI.SearchRepoDescription,
			Archived:           archived,
			Fork:               fork,
			Mirror:             mirror,
			Template:           template,
			IsPrivate:          private,
		})
		if err != nil {
			ctx.ServerError("SearchRepository", err)
			return
		}

		total = int(count)
	}
	ctx.Data["Repos"] = repos
	ctx.Data["Total"] = total

	err = shared_user.LoadHeaderCount(ctx)
	if err != nil {
		ctx.ServerError("LoadHeaderCount", err)
		return
	}

	pager := context.NewPagination(total, pagingNum, page, 5)
	pager.SetDefaultParams(ctx)
	pager.AddParam(ctx, "tab", "TabName")
	if tab != "followers" && tab != "following" && tab != "activity" && tab != "projects" {
		pager.AddParam(ctx, "language", "Language")
	}
	if tab == "activity" {
		pager.AddParam(ctx, "date", "Date")
	}
	if has, value := archived.Get(); has {
		pager.AddParamString("archived", fmt.Sprint(value))
	}
	if has, value := fork.Get(); has {
		pager.AddParamString("fork", fmt.Sprint(value))
	}
	if has, value := mirror.Get(); has {
		pager.AddParamString("mirror", fmt.Sprint(value))
	}
	if has, value := template.Get(); has {
		pager.AddParamString("template", fmt.Sprint(value))
	}
	if has, value := private.Get(); has {
		pager.AddParamString("private", fmt.Sprint(value))
	}
	ctx.Data["Page"] = pager
}

// Action response for follow/unfollow user request
func Action(ctx *context.Context) {
	var err error
	action := ctx.FormString("action")

	if ctx.ContextUser.IsOrganization() && (action == "block" || action == "unblock") {
		log.Error("Cannot perform this action on an organization %q", ctx.FormString("action"))
		ctx.JSONError(fmt.Sprintf("Action %q failed", ctx.FormString("action")))
		return
	}

	switch action {
	case "follow":
		err = user_model.FollowUser(ctx, ctx.Doer.ID, ctx.ContextUser.ID)
	case "unfollow":
		err = user_model.UnfollowUser(ctx, ctx.Doer.ID, ctx.ContextUser.ID)
	case "block":
		err = user_service.BlockUser(ctx, ctx.Doer.ID, ctx.ContextUser.ID)
	case "unblock":
		err = user_model.UnblockUser(ctx, ctx.Doer.ID, ctx.ContextUser.ID)
	}

	if err != nil {
		if !errors.Is(err, user_model.ErrBlockedByUser) {
			log.Error("Failed to apply action %q: %v", ctx.FormString("action"), err)
			ctx.Error(http.StatusBadRequest, fmt.Sprintf("Action %q failed", ctx.FormString("action")))
			return
		}

		if ctx.ContextUser.IsOrganization() {
			ctx.Flash.Error(ctx.Tr("org.follow_blocked_user"), true)
		} else {
			ctx.Flash.Error(ctx.Tr("user.follow_blocked_user"), true)
		}
	}

	if ctx.ContextUser.IsIndividual() {
		shared_user.PrepareContextForProfileBigAvatar(ctx)
		ctx.Data["IsHTMX"] = true
		ctx.HTML(http.StatusOK, tplProfileBigAvatar)
		return
	} else if ctx.ContextUser.IsOrganization() {
		ctx.Data["Org"] = ctx.ContextUser
		ctx.Data["IsFollowing"] = ctx.Doer != nil && user_model.IsFollowing(ctx, ctx.Doer.ID, ctx.ContextUser.ID)
		ctx.HTML(http.StatusOK, tplFollowUnfollow)
		return
	}
	log.Error("Failed to apply action %q: unsupported context user type: %s", ctx.FormString("action"), ctx.ContextUser.Type)
	ctx.Error(http.StatusBadRequest, fmt.Sprintf("Action %q failed", ctx.FormString("action")))
}
