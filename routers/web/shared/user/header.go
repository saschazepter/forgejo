// Copyright 2022 The Gitea Authors. All rights reserved.
// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package user

import (
	"net/url"

	"forgejo.org/models/db"
	"forgejo.org/models/organization"
	packages_model "forgejo.org/models/packages"
	access_model "forgejo.org/models/perm/access"
	project_model "forgejo.org/models/project"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/log"
	"forgejo.org/modules/markup"
	"forgejo.org/modules/markup/markdown"
	"forgejo.org/modules/optional"
	project_module "forgejo.org/modules/project"
	"forgejo.org/modules/setting"
	"forgejo.org/routers/web/repo"
	"forgejo.org/services/context"
	funding_service "forgejo.org/services/funding"
)

// prepareContextForCommonProfile store some common data into context data for user's profile related pages (including the nav menu)
// It is designed to be fast and safe to be called multiple times in one request
func prepareContextForCommonProfile(ctx *context.Context) {
	ctx.Data["IsPackageEnabled"] = setting.Packages.Enabled
	ctx.Data["IsRepoIndexerEnabled"] = setting.Indexer.RepoIndexerEnabled
	ctx.Data["EnableFeed"] = setting.Other.EnableFeed
	ctx.Data["FeedURL"] = ctx.ContextUser.HomeLink()
}

// PrepareContextForProfileBigAvatar set the context for big avatar view on the profile page
func PrepareContextForProfileBigAvatar(ctx *context.Context) {
	prepareContextForCommonProfile(ctx)

	ctx.Data["IsModerationEnabled"] = setting.Moderation.Enabled
	ctx.Data["IsBlocked"] = ctx.Doer != nil && user_model.IsBlocked(ctx, ctx.Doer.ID, ctx.ContextUser.ID)
	ctx.Data["IsFollowing"] = ctx.Doer != nil && user_model.IsFollowing(ctx, ctx.Doer.ID, ctx.ContextUser.ID)
	ctx.Data["ShowUserEmail"] = setting.UI.ShowUserEmail && ctx.ContextUser.Email != "" && ctx.IsSigned && !ctx.ContextUser.KeepEmailPrivate
	if setting.Service.UserLocationMapURL != "" {
		ctx.Data["ContextUserLocationMapURL"] = setting.Service.UserLocationMapURL + url.QueryEscape(ctx.ContextUser.Location)
	}
	// Show OpenID URIs
	openIDs, err := user_model.GetUserOpenIDs(ctx, ctx.ContextUser.ID)
	if err != nil {
		ctx.ServerError("GetUserOpenIDs", err)
		return
	}
	ctx.Data["OpenIDs"] = openIDs
	if len(ctx.ContextUser.Description) != 0 {
		content, err := markdown.RenderString(&markup.RenderContext{
			Metas: map[string]string{"mode": "document"},
			Ctx:   ctx,
		}, ctx.ContextUser.Description)
		if err != nil {
			ctx.ServerError("RenderString", err)
			return
		}
		ctx.Data["RenderedDescription"] = content
	}

	showPrivate := ctx.IsSigned && (ctx.Doer.IsAdmin || ctx.Doer.ID == ctx.ContextUser.ID)
	orgs, err := db.Find[organization.Organization](ctx, organization.FindOrgOptions{
		UserID:         ctx.ContextUser.ID,
		IncludeLimited: ctx.IsSigned,
		IncludePrivate: showPrivate,
	})
	if err != nil {
		ctx.ServerError("FindOrgs", err)
		return
	}
	ctx.Data["Orgs"] = orgs
	ctx.Data["HasOrgsVisible"] = organization.HasOrgsVisible(ctx, orgs, ctx.Doer)

	badges, _, err := user_model.GetUserBadges(ctx, ctx.ContextUser)
	if err != nil {
		ctx.ServerError("GetUserBadges", err)
		return
	}
	ctx.Data["Badges"] = badges

	// in case the numbers are already provided by other functions, no need to query again (which is slow)
	if _, ok := ctx.Data["NumFollowers"]; !ok {
		_, ctx.Data["NumFollowers"], _ = user_model.GetUserFollowers(ctx, ctx.ContextUser, ctx.Doer, db.ListOptions{PageSize: 1, Page: 1})
	}
	if _, ok := ctx.Data["NumFollowing"]; !ok {
		_, ctx.Data["NumFollowing"], _ = user_model.GetUserFollowing(ctx, ctx.ContextUser, ctx.Doer, db.ListOptions{PageSize: 1, Page: 1})
	}
}

func FindUserProfileReadme(ctx *context.Context, doer *user_model.User) (profileDbRepo *repo_model.Repository, profileGitRepo *git.Repository, profileReadmeBlob *git.Blob, profileFunding *funding_service.RepoFunding, profileClose func()) {
	profileDbRepo, err := repo_model.GetRepositoryByName(ctx, ctx.ContextUser.ID, context.OwnerProfileRepositoryName)
	if err == nil {
		// Don't show profile content if .profile repository is a fork or private
		if profileDbRepo.IsFork || profileDbRepo.IsPrivate {
			return nil, nil, nil, nil, func() {}
		}
		perm, err := access_model.GetUserRepoPermission(ctx, profileDbRepo, doer)
		if err == nil && !profileDbRepo.IsEmpty && perm.CanRead(unit.TypeCode) {
			if profileGitRepo, err = gitrepo.OpenRepository(ctx, profileDbRepo); err != nil {
				log.Error("FindUserProfileReadme failed to OpenRepository: %v", err)
			} else {
				if commit, err := profileGitRepo.GetBranchCommit(profileDbRepo.DefaultBranch); err != nil {
					log.Error("FindUserProfileReadme failed to GetBranchCommit: %v", err)
				} else {
					profileFunding, err = funding_service.GetFundingFromCommit(profileDbRepo, commit)
					if err != nil && !funding_service.IsNotExistError(err) {
						log.Error("FindUserProfileReadme failed to GetFundingFromCommit: %v", err)
					}
					tree, err := commit.SubTree("")
					if err != nil {
						log.Error("FindUserProfileReadme failed to get SubTree: %v", err)
					} else {
						entries, err := tree.ListEntries()
						if err != nil {
							log.Error("FindUserProfileReadme failed to list entries: %v", err)
						} else {
							_, readmeEntry, err := repo.FindReadmeFileInEntries(ctx, entries, true)
							if err != nil {
								log.Error("FindUserProfileReadme failed to find readme in entries: %v", err)
							} else if readmeEntry != nil {
								profileReadmeBlob = readmeEntry.Blob()
							}
						}
					}
				}
			}
		}
	} else if !repo_model.IsErrRepoNotExist(err) {
		log.Error("FindUserProfileReadme failed to GetRepositoryByName: %v", err)
	}
	return profileDbRepo, profileGitRepo, profileReadmeBlob, profileFunding, func() {
		if profileGitRepo != nil {
			_ = profileGitRepo.Close()
		}
	}
}

func RenderUserHeader(ctx *context.Context) {
	prepareContextForCommonProfile(ctx)

	_, _, profileReadmeBlob, _, profileClose := FindUserProfileReadme(ctx, ctx.Doer)
	defer profileClose()
	ctx.Data["HasProfileReadme"] = profileReadmeBlob != nil
}

func LoadHeaderCount(ctx *context.Context) error {
	prepareContextForCommonProfile(ctx)

	var err error

	ctx.Data["RepoCount"], err = repo_model.CountRepository(ctx, &repo_model.SearchRepoOptions{
		Actor:              ctx.Doer,
		OwnerID:            ctx.ContextUser.ID,
		Private:            ctx.IsSigned,
		Collaborate:        optional.Some(false),
		IncludeDescription: setting.UI.SearchRepoDescription,
	})
	if err != nil {
		return err
	}

	var ownerType project_module.OwnerType
	if ctx.ContextUser.IsOrganization() {
		ownerType = project_module.TypeOrganization
	} else {
		ownerType = project_module.TypeIndividual
	}
	ctx.Data["ProjectCount"], err = db.Count[project_model.Project](ctx, project_model.SearchOptions{
		OwnerID:  ctx.ContextUser.ID,
		IsClosed: optional.Some(false),
		Type:     ownerType,
	})
	if err != nil {
		return err
	}
	ctx.Data["PackageCount"], err = packages_model.CountOwnerPackages(ctx, ctx.ContextUser.ID)
	if err != nil {
		return err
	}

	return nil
}
