// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2018 The Gitea Authors. All rights reserved.
// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"forgejo.org/models"
	activities_model "forgejo.org/models/activities"
	"forgejo.org/models/asymkey"
	"forgejo.org/models/db"
	git_model "forgejo.org/models/git"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/base"
	"forgejo.org/modules/container"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/log"
	"forgejo.org/modules/markup"
	"forgejo.org/modules/markup/markdown"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/util"
	"forgejo.org/modules/web"
	"forgejo.org/routers/web/feed"
	"forgejo.org/services/context"
	"forgejo.org/services/context/upload"
	"forgejo.org/services/forms"
	release_service "forgejo.org/services/release"
)

const (
	tplReleasesList base.TplName = "repo/release/list"
	tplReleaseNew   base.TplName = "repo/release/new"
	tplTagsList     base.TplName = "repo/tag/list"
)

// calReleaseNumCommitsBehind calculates given release has how many commits behind release target.
func calReleaseNumCommitsBehind(repoCtx *context.Repository, release *repo_model.Release, countCache map[string]int64) error {
	target := release.Target
	if target == "" {
		target = repoCtx.Repository.DefaultBranch
	}
	// Get count if not cached
	if _, ok := countCache[target]; !ok {
		commit, err := repoCtx.GitRepo.GetBranchCommit(target)
		if err != nil {
			var errNotExist git.ErrNotExist
			if target == repoCtx.Repository.DefaultBranch || !errors.As(err, &errNotExist) {
				return fmt.Errorf("GetBranchCommit: %w", err)
			}
			// fallback to default branch
			target = repoCtx.Repository.DefaultBranch
			commit, err = repoCtx.GitRepo.GetBranchCommit(target)
			if err != nil {
				return fmt.Errorf("GetBranchCommit(DefaultBranch): %w", err)
			}
		}
		countCache[target], err = commit.CommitsCount()
		if err != nil {
			return fmt.Errorf("CommitsCount: %w", err)
		}
	}
	release.NumCommitsBehind = countCache[target] - release.NumCommits
	release.TargetBehind = target
	return nil
}

type ReleaseInfo struct {
	Release        *repo_model.Release
	CommitStatus   *git_model.CommitStatus
	CommitStatuses []*git_model.CommitStatus
}

func getReleaseInfos(ctx *context.Context, opts *repo_model.FindReleasesOptions) ([]*ReleaseInfo, int64, error) {
	releases, err := db.Find[repo_model.Release](ctx, opts)
	if err != nil {
		return nil, 0, err
	}

	counts := int64(len(releases))
	if !opts.IsListAll() {
		counts, err = db.Count[repo_model.Release](ctx, opts)
		if err != nil {
			return nil, 0, err
		}
	}

	for _, release := range releases {
		release.Repo = ctx.Repo.Repository
	}

	if err = repo_model.GetReleaseAttachments(ctx, releases...); err != nil {
		return nil, 0, err
	}

	// Temporary cache commits count of used branches to speed up.
	countCache := make(map[string]int64)
	cacheUsers := make(map[int64]*user_model.User)
	if ctx.Doer != nil {
		cacheUsers[ctx.Doer.ID] = ctx.Doer
	}
	var ok bool

	canReadActions := ctx.Repo.CanRead(unit.TypeActions)

	releaseInfos := make([]*ReleaseInfo, 0, len(releases))
	for _, r := range releases {
		if r.Publisher, ok = cacheUsers[r.PublisherID]; !ok {
			r.Publisher, err = user_model.GetUserByID(ctx, r.PublisherID)
			if err != nil {
				if user_model.IsErrUserNotExist(err) {
					r.Publisher = user_model.NewGhostUser()
				} else {
					return nil, 0, err
				}
			}
			cacheUsers[r.PublisherID] = r.Publisher
		}

		r.RenderedNote, err = markdown.RenderString(&markup.RenderContext{
			Links: markup.Links{
				Base: ctx.Repo.RepoLink,
			},
			Metas:   ctx.Repo.Repository.ComposeMetas(ctx),
			GitRepo: ctx.Repo.GitRepo,
			Ctx:     ctx,
		}, r.Note)
		if err != nil {
			return nil, 0, err
		}

		err = r.LoadArchiveDownloadCount(ctx)
		if err != nil {
			return nil, 0, err
		}

		if !r.IsDraft {
			if err := calReleaseNumCommitsBehind(ctx.Repo, r, countCache); err != nil {
				return nil, 0, err
			}
		}

		info := &ReleaseInfo{
			Release: r,
		}

		if canReadActions {
			statuses, _, err := git_model.GetLatestCommitStatus(ctx, r.Repo.ID, r.Sha1, db.ListOptionsAll)
			if err != nil {
				return nil, 0, err
			}

			info.CommitStatus = git_model.CalcCommitStatus(statuses)
			info.CommitStatuses = statuses
		}

		releaseInfos = append(releaseInfos, info)
	}

	return releaseInfos, counts, nil
}

// Releases render releases list page
func Releases(ctx *context.Context) {
	ctx.Data["PageIsReleaseList"] = true
	ctx.Data["Title"] = ctx.Tr("repo.release.releases")
	ctx.Data["IsViewBranch"] = false
	ctx.Data["IsViewTag"] = true
	// Disable the showCreateNewBranch form in the dropdown on this page.
	ctx.Data["CanCreateBranch"] = false
	ctx.Data["HideBranchesInDropdown"] = true
	ctx.Data["ShowReleaseSearch"] = true

	keyword := ctx.FormTrim("q")
	ctx.Data["Keyword"] = keyword

	listOptions := db.ListOptions{
		Page:     ctx.FormInt("page"),
		PageSize: ctx.FormInt("limit"),
	}
	if listOptions.PageSize == 0 {
		listOptions.PageSize = setting.Repository.Release.DefaultPagingNum
	}
	if listOptions.PageSize > setting.API.MaxResponseItems {
		listOptions.PageSize = setting.API.MaxResponseItems
	}

	writeAccess := ctx.Repo.CanWrite(unit.TypeReleases)
	ctx.Data["CanCreateRelease"] = writeAccess && !ctx.Repo.Repository.IsArchived

	releases, releasesCount, err := getReleaseInfos(ctx, &repo_model.FindReleasesOptions{
		ListOptions: listOptions,
		// only show draft releases for users who can write, read-only users shouldn't see draft releases.
		IncludeDrafts: writeAccess,
		RepoID:        ctx.Repo.Repository.ID,
		Keyword:       keyword,
	})
	if err != nil {
		ctx.ServerError("getReleaseInfos", err)
		return
	}
	for _, rel := range releases {
		if rel.Release.IsTag && rel.Release.Title == "" {
			rel.Release.Title = rel.Release.TagName
		}
		rel.Release.Repo = ctx.Repo.Repository // perf: avoid r.LoadRepo, tag-by-tag, in verifyTagSignature
	}

	ctx.Data["Releases"] = releases
	addVerifyTagToContext(ctx)

	pager := context.NewPagination(int(releasesCount), listOptions.PageSize, listOptions.Page, 5)
	pager.SetDefaultParams(ctx)
	ctx.Data["Page"] = pager

	ctx.HTML(http.StatusOK, tplReleasesList)
}

func verifyTagSignature(ctx *context.Context, r *repo_model.Release) (*asymkey.ObjectVerification, error) {
	if err := r.LoadRepo(ctx); err != nil {
		return nil, err
	}

	gitRepo, closer, err := gitrepo.RepositoryFromContextOrOpen(ctx, r.Repo)
	if err != nil {
		return nil, err
	}
	defer closer.Close()

	tag, err := gitRepo.GetTag(r.TagName)
	if err != nil {
		return nil, err
	}
	if tag.Signature == nil {
		return nil, nil
	}

	verification := asymkey.ParseTagWithSignature(ctx, gitRepo, tag)
	return verification, nil
}

func addVerifyTagToContext(ctx *context.Context) {
	ctx.Data["VerifyTag"] = func(r *repo_model.Release) *asymkey.ObjectVerification {
		v, err := verifyTagSignature(ctx, r)
		if err != nil {
			return nil
		}
		return v
	}
	ctx.Data["HasSignature"] = func(verification *asymkey.ObjectVerification) bool {
		if verification == nil {
			return false
		}
		return verification.Reason != asymkey.NotSigned
	}
}

// TagsList render tags list page
func TagsList(ctx *context.Context) {
	ctx.Data["PageIsTagList"] = true
	ctx.Data["Title"] = ctx.Tr("repo.release.tags")
	ctx.Data["IsViewBranch"] = false
	ctx.Data["IsViewTag"] = true
	// Disable the showCreateNewBranch form in the dropdown on this page.
	ctx.Data["CanCreateBranch"] = false
	ctx.Data["HideBranchesInDropdown"] = true
	ctx.Data["CanCreateRelease"] = ctx.Repo.CanWrite(unit.TypeReleases) && !ctx.Repo.Repository.IsArchived
	ctx.Data["ShowReleaseSearch"] = true

	keyword := ctx.FormTrim("q")
	ctx.Data["Keyword"] = keyword

	listOptions := db.ListOptions{
		Page:     ctx.FormInt("page"),
		PageSize: ctx.FormInt("limit"),
	}
	if listOptions.PageSize == 0 {
		listOptions.PageSize = setting.Repository.Release.DefaultPagingNum
	}
	if listOptions.PageSize > setting.API.MaxResponseItems {
		listOptions.PageSize = setting.API.MaxResponseItems
	}

	opts := repo_model.FindReleasesOptions{
		ListOptions: listOptions,
		// for the tags list page, show all releases with real tags (having real commit-id),
		// the drafts should also be included because a real tag might be used as a draft.
		IncludeDrafts: true,
		IncludeTags:   true,
		HasSha1:       optional.Some(true),
		RepoID:        ctx.Repo.Repository.ID,
		Keyword:       keyword,
	}

	releases, err := db.Find[repo_model.Release](ctx, opts)
	if err != nil {
		ctx.ServerError("GetReleasesByRepoID", err)
		return
	}

	for _, rel := range releases {
		rel.Repo = ctx.Repo.Repository // perf: avoid r.LoadRepo, tag-by-tag, in verifyTagSignature
	}

	ctx.Data["Releases"] = releases
	addVerifyTagToContext(ctx)

	numTags := ctx.Data["NumTags"].(int64)
	pager := context.NewPagination(int(numTags), opts.PageSize, opts.Page, 5)
	pager.SetDefaultParams(ctx)
	ctx.Data["Page"] = pager

	ctx.Data["PageIsViewCode"] = !ctx.Repo.Repository.UnitEnabled(ctx, unit.TypeReleases)
	ctx.HTML(http.StatusOK, tplTagsList)
}

// ReleasesFeedRSS get feeds for releases in RSS format
func ReleasesFeedRSS(ctx *context.Context) {
	releasesOrTagsFeed(ctx, true, "rss")
}

// TagsListFeedRSS get feeds for tags in RSS format
func TagsListFeedRSS(ctx *context.Context) {
	releasesOrTagsFeed(ctx, false, "rss")
}

// ReleasesFeedAtom get feeds for releases in Atom format
func ReleasesFeedAtom(ctx *context.Context) {
	releasesOrTagsFeed(ctx, true, "atom")
}

// TagsListFeedAtom get feeds for tags in RSS format
func TagsListFeedAtom(ctx *context.Context) {
	releasesOrTagsFeed(ctx, false, "atom")
}

func releasesOrTagsFeed(ctx *context.Context, isReleasesOnly bool, formatType string) {
	feed.ShowReleaseFeed(ctx, ctx.Repo.Repository, isReleasesOnly, formatType)
}

// SingleRelease renders a single release's page
func SingleRelease(ctx *context.Context) {
	ctx.Data["PageIsReleaseList"] = true
	ctx.Data["DefaultBranch"] = ctx.Repo.Repository.DefaultBranch

	writeAccess := ctx.Repo.CanWrite(unit.TypeReleases)
	ctx.Data["CanCreateRelease"] = writeAccess && !ctx.Repo.Repository.IsArchived

	releases, _, err := getReleaseInfos(ctx, &repo_model.FindReleasesOptions{
		ListOptions: db.ListOptions{Page: 1, PageSize: 1},
		RepoID:      ctx.Repo.Repository.ID,
		// Include tags in the search too.
		IncludeTags: true,
		TagNames:    []string{ctx.Params("*")},
		// only show draft releases for users who can write, read-only users shouldn't see draft releases.
		IncludeDrafts: writeAccess,
	})
	if err != nil {
		ctx.ServerError("getReleaseInfos", err)
		return
	}
	if len(releases) != 1 {
		ctx.NotFound("SingleRelease", err)
		return
	}

	release := releases[0].Release
	if release.IsTag && release.Title == "" {
		release.Title = release.TagName
	}
	addVerifyTagToContext(ctx)

	ctx.Data["PageIsSingleTag"] = release.IsTag
	ctx.Data["Title"] = release.DisplayName()

	err = release.LoadArchiveDownloadCount(ctx)
	if err != nil {
		ctx.ServerError("LoadArchiveDownloadCount", err)
		return
	}

	if ctx.IsSigned && !release.IsTag {
		err = activities_model.SetReleaseReadBy(ctx, release.ID, ctx.Doer.ID)
		if err != nil {
			ctx.ServerError("SetReleaseReadBy", err)
			return
		}
	}

	ctx.Data["Releases"] = releases

	ctx.Data["OpenGraphTitle"] = fmt.Sprintf("%s - %s", release.DisplayName(), release.Repo.FullName())
	ctx.Data["OpenGraphDescription"] = base.EllipsisString(release.Note, 300)
	ctx.Data["OpenGraphURL"] = release.HTMLURL()
	ctx.Data["OpenGraphImageURL"] = release.SummaryCardURL()
	ctx.Data["OpenGraphImageAltText"] = ctx.Tr("repo.release.summary_card_alt", release.DisplayName(), release.Repo.FullName())

	ctx.HTML(http.StatusOK, tplReleasesList)
}

// LatestRelease redirects to the latest release
func LatestRelease(ctx *context.Context) {
	release, err := repo_model.GetLatestReleaseByRepoID(ctx, ctx.Repo.Repository.ID)
	if err != nil {
		if repo_model.IsErrReleaseNotExist(err) {
			ctx.NotFound("LatestRelease", err)
			return
		}
		ctx.ServerError("GetLatestReleaseByRepoID", err)
		return
	}

	if err := release.LoadAttributes(ctx); err != nil {
		ctx.ServerError("LoadAttributes", err)
		return
	}

	ctx.Redirect(release.Link())
}

// NewRelease render creating or edit release page
func NewRelease(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.release.new_release")
	ctx.Data["PageIsReleaseList"] = true
	ctx.Data["tag_target"] = ctx.Repo.Repository.DefaultBranch
	if tagName := ctx.FormString("tag"); len(tagName) > 0 {
		rel, err := repo_model.GetRelease(ctx, ctx.Repo.Repository.ID, tagName)
		if err != nil && !repo_model.IsErrReleaseNotExist(err) {
			ctx.ServerError("GetRelease", err)
			return
		}

		if rel != nil {
			rel.Repo = ctx.Repo.Repository
			if err := rel.LoadAttributes(ctx); err != nil {
				ctx.ServerError("LoadAttributes", err)
				return
			}

			ctx.Data["tag_name"] = rel.TagName
			if rel.Target != "" {
				ctx.Data["tag_target"] = rel.Target
			}
			ctx.Data["title"] = rel.Title
			ctx.Data["content"] = rel.Note
			ctx.Data["attachments"] = rel.Attachments
		}
	}
	ctx.Data["IsAttachmentEnabled"] = setting.Attachment.Enabled
	assigneeUsers, err := repo_model.GetRepoAssignees(ctx, ctx.Repo.Repository)
	if err != nil {
		ctx.ServerError("GetRepoAssignees", err)
		return
	}
	ctx.Data["Assignees"] = MakeSelfOnTop(ctx.Doer, assigneeUsers)

	upload.AddUploadContext(ctx, "release")

	// For New Release page
	PrepareBranchList(ctx)
	if ctx.Written() {
		return
	}

	tags, err := repo_model.GetTagNamesByRepoID(ctx, ctx.Repo.Repository.ID)
	if err != nil {
		ctx.ServerError("GetTagNamesByRepoID", err)
		return
	}
	ctx.Data["Tags"] = tags

	// We set the value of the hide_archive_link textbox depending on the latest release
	latestRelease, err := repo_model.GetLatestReleaseByRepoID(ctx, ctx.Repo.Repository.ID)
	if err != nil {
		if repo_model.IsErrReleaseNotExist(err) {
			ctx.Data["hide_archive_links"] = false
		} else {
			ctx.ServerError("GetLatestReleaseByRepoID", err)
			return
		}
	}
	if latestRelease != nil {
		ctx.Data["hide_archive_links"] = latestRelease.HideArchiveLinks
	}

	ctx.HTML(http.StatusOK, tplReleaseNew)
}

// NewReleasePost response for creating a release
func NewReleasePost(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.NewReleaseForm)
	ctx.Data["Title"] = ctx.Tr("repo.release.new_release")
	ctx.Data["PageIsReleaseList"] = true

	tags, err := repo_model.GetTagNamesByRepoID(ctx, ctx.Repo.Repository.ID)
	if err != nil {
		ctx.ServerError("GetTagNamesByRepoID", err)
		return
	}
	ctx.Data["Tags"] = tags

	if ctx.HasError() {
		ctx.HTML(http.StatusOK, tplReleaseNew)
		return
	}

	objectFormat, _ := ctx.Repo.GitRepo.GetObjectFormat()

	// form.Target can be a branch name or a full commitID.
	if !ctx.Repo.GitRepo.IsBranchExist(form.Target) &&
		len(form.Target) == objectFormat.FullLength() && !ctx.Repo.GitRepo.IsCommitExist(form.Target) {
		ctx.RenderWithErr(ctx.Tr("form.target_branch_not_exist"), tplReleaseNew, &form)
		return
	}

	// Title of release cannot be empty
	if len(form.TagOnly) == 0 && len(form.Title) == 0 {
		ctx.RenderWithErr(ctx.Tr("repo.release.title_empty"), tplReleaseNew, &form)
		return
	}

	attachmentChanges := make(container.Set[*release_service.AttachmentChange])
	attachmentChangesByID := make(map[string]*release_service.AttachmentChange)

	if setting.Attachment.Enabled {
		for _, uuid := range form.Files {
			attachmentChanges.Add(&release_service.AttachmentChange{
				Action: "add",
				Type:   "attachment",
				UUID:   uuid,
			})
		}

		const namePrefix = "attachment-new-name-"
		const exturlPrefix = "attachment-new-exturl-"
		for k, v := range ctx.Req.Form {
			isNewName := strings.HasPrefix(k, namePrefix)
			isNewExturl := strings.HasPrefix(k, exturlPrefix)
			if isNewName || isNewExturl {
				var id string
				if isNewName {
					id = k[len(namePrefix):]
				} else if isNewExturl {
					id = k[len(exturlPrefix):]
				}
				if _, ok := attachmentChangesByID[id]; !ok {
					attachmentChangesByID[id] = &release_service.AttachmentChange{
						Action: "add",
						Type:   "external",
					}
					attachmentChanges.Add(attachmentChangesByID[id])
				}
				if isNewName {
					attachmentChangesByID[id].Name = v[0]
				} else if isNewExturl {
					attachmentChangesByID[id].ExternalURL = v[0]
				}
			}
		}
	}

	rel, err := repo_model.GetRelease(ctx, ctx.Repo.Repository.ID, form.TagName)
	if err != nil {
		if !repo_model.IsErrReleaseNotExist(err) {
			ctx.ServerError("GetRelease", err)
			return
		}

		msg := ""
		if len(form.Title) > 0 && form.AddTagMsg {
			msg = form.Title + "\n\n" + form.Content
		}

		if len(form.TagOnly) > 0 {
			if err = release_service.CreateNewTag(ctx, ctx.Doer, ctx.Repo.Repository, form.Target, form.TagName, msg); err != nil {
				if models.IsErrTagAlreadyExists(err) {
					e := err.(models.ErrTagAlreadyExists)
					ctx.Flash.Error(ctx.Tr("repo.branch.tag_collision", e.TagName))
					ctx.Redirect(ctx.Repo.RepoLink + "/src/" + ctx.Repo.BranchNameSubURL())
					return
				}

				if models.IsErrInvalidTagName(err) {
					ctx.Flash.Error(ctx.Tr("repo.release.tag_name_invalid"))
					ctx.Redirect(ctx.Repo.RepoLink + "/src/" + ctx.Repo.BranchNameSubURL())
					return
				}

				if models.IsErrProtectedTagName(err) {
					ctx.Flash.Error(ctx.Tr("repo.release.tag_name_protected"))
					ctx.Redirect(ctx.Repo.RepoLink + "/src/" + ctx.Repo.BranchNameSubURL())
					return
				}

				ctx.ServerError("releaseservice.CreateNewTag", err)
				return
			}

			ctx.Flash.Success(ctx.Tr("repo.tag.create_success", form.TagName))
			ctx.Redirect(ctx.Repo.RepoLink + "/src/tag/" + util.PathEscapeSegments(form.TagName))
			return
		}

		rel = &repo_model.Release{
			RepoID:           ctx.Repo.Repository.ID,
			Repo:             ctx.Repo.Repository,
			PublisherID:      ctx.Doer.ID,
			Publisher:        ctx.Doer,
			Title:            form.Title,
			TagName:          form.TagName,
			Target:           form.Target,
			Note:             form.Content,
			IsDraft:          len(form.Draft) > 0,
			IsPrerelease:     form.Prerelease,
			HideArchiveLinks: form.HideArchiveLinks,
			IsTag:            false,
		}

		if err = release_service.CreateRelease(ctx.Repo.GitRepo, rel, msg, attachmentChanges.Values()); err != nil {
			ctx.Data["Err_TagName"] = true
			switch {
			case repo_model.IsErrReleaseAlreadyExist(err):
				ctx.RenderWithErr(ctx.Tr("repo.release.tag_name_already_exist"), tplReleaseNew, &form)
			case models.IsErrInvalidTagName(err):
				ctx.RenderWithErr(ctx.Tr("repo.release.tag_name_invalid"), tplReleaseNew, &form)
			case models.IsErrProtectedTagName(err):
				ctx.RenderWithErr(ctx.Tr("repo.release.tag_name_protected"), tplReleaseNew, &form)
			case repo_model.IsErrInvalidExternalURL(err):
				ctx.RenderWithErr(ctx.Tr("repo.release.invalid_external_url", err.(repo_model.ErrInvalidExternalURL).ExternalURL), tplReleaseNew, &form)
			default:
				ctx.ServerError("CreateRelease", err)
			}
			return
		}
	} else {
		if !rel.IsTag {
			ctx.Data["Err_TagName"] = true
			ctx.RenderWithErr(ctx.Tr("repo.release.tag_name_already_exist"), tplReleaseNew, &form)
			return
		}

		rel.Title = form.Title
		rel.Note = form.Content
		rel.Target = form.Target
		rel.IsDraft = len(form.Draft) > 0
		rel.IsPrerelease = form.Prerelease
		rel.PublisherID = ctx.Doer.ID
		rel.HideArchiveLinks = form.HideArchiveLinks
		rel.IsTag = false

		if err = release_service.UpdateRelease(ctx, ctx.Doer, ctx.Repo.GitRepo, rel, true, attachmentChanges.Values()); err != nil {
			ctx.Data["Err_TagName"] = true
			switch {
			case repo_model.IsErrInvalidExternalURL(err):
				ctx.RenderWithErr(ctx.Tr("repo.release.invalid_external_url", err.(repo_model.ErrInvalidExternalURL).ExternalURL), tplReleaseNew, &form)
			default:
				ctx.ServerError("UpdateRelease", err)
			}
			return
		}
	}
	log.Trace("Release created: %s/%s:%s", ctx.Doer.LowerName, ctx.Repo.Repository.Name, form.TagName)

	ctx.Redirect(ctx.Repo.RepoLink + "/releases")
}

// EditRelease render release edit page
func EditRelease(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.release.edit_release")
	ctx.Data["PageIsReleaseList"] = true
	ctx.Data["PageIsEditRelease"] = true
	ctx.Data["IsAttachmentEnabled"] = setting.Attachment.Enabled
	upload.AddUploadContext(ctx, "release")

	tagName := ctx.Params("*")
	rel, err := repo_model.GetRelease(ctx, ctx.Repo.Repository.ID, tagName)
	if err != nil {
		if repo_model.IsErrReleaseNotExist(err) {
			ctx.NotFound("GetRelease", err)
		} else {
			ctx.ServerError("GetRelease", err)
		}
		return
	}
	ctx.Data["ID"] = rel.ID
	ctx.Data["tag_name"] = rel.TagName
	ctx.Data["tag_target"] = rel.Target
	ctx.Data["title"] = rel.Title
	ctx.Data["content"] = rel.Note
	ctx.Data["prerelease"] = rel.IsPrerelease
	ctx.Data["hide_archive_links"] = rel.HideArchiveLinks
	ctx.Data["IsDraft"] = rel.IsDraft

	rel.Repo = ctx.Repo.Repository
	if err := rel.LoadAttributes(ctx); err != nil {
		ctx.ServerError("LoadAttributes", err)
		return
	}
	ctx.Data["attachments"] = rel.Attachments

	// Get assignees.
	assigneeUsers, err := repo_model.GetRepoAssignees(ctx, rel.Repo)
	if err != nil {
		ctx.ServerError("GetRepoAssignees", err)
		return
	}
	ctx.Data["Assignees"] = MakeSelfOnTop(ctx.Doer, assigneeUsers)

	ctx.HTML(http.StatusOK, tplReleaseNew)
}

// EditReleasePost response for edit release
func EditReleasePost(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.EditReleaseForm)
	ctx.Data["Title"] = ctx.Tr("repo.release.edit_release")
	ctx.Data["PageIsReleaseList"] = true
	ctx.Data["PageIsEditRelease"] = true

	tagName := ctx.Params("*")
	rel, err := repo_model.GetRelease(ctx, ctx.Repo.Repository.ID, tagName)
	if err != nil {
		if repo_model.IsErrReleaseNotExist(err) {
			ctx.NotFound("GetRelease", err)
		} else {
			ctx.ServerError("GetRelease", err)
		}
		return
	}
	if rel.IsTag {
		ctx.NotFound("GetRelease", err)
		return
	}
	ctx.Data["tag_name"] = rel.TagName
	ctx.Data["tag_target"] = rel.Target
	ctx.Data["title"] = rel.Title
	ctx.Data["content"] = rel.Note
	ctx.Data["prerelease"] = rel.IsPrerelease
	ctx.Data["hide_archive_links"] = rel.HideArchiveLinks

	rel.Repo = ctx.Repo.Repository
	if err := rel.LoadAttributes(ctx); err != nil {
		ctx.ServerError("LoadAttributes", err)
		return
	}
	// TODO: If an error occurs, do not forget the attachment edits the user made
	// when displaying the error message.
	ctx.Data["attachments"] = rel.Attachments

	if ctx.HasError() {
		ctx.HTML(http.StatusOK, tplReleaseNew)
		return
	}

	const delPrefix = "attachment-del-"
	const editPrefix = "attachment-edit-"
	const newPrefix = "attachment-new-"
	const namePrefix = "name-"
	const exturlPrefix = "exturl-"
	attachmentChanges := make(container.Set[*release_service.AttachmentChange])
	attachmentChangesByID := make(map[string]*release_service.AttachmentChange)

	if setting.Attachment.Enabled {
		for _, uuid := range form.Files {
			attachmentChanges.Add(&release_service.AttachmentChange{
				Action: "add",
				Type:   "attachment",
				UUID:   uuid,
			})
		}

		for k, v := range ctx.Req.Form {
			if strings.HasPrefix(k, delPrefix) && v[0] == "true" {
				attachmentChanges.Add(&release_service.AttachmentChange{
					Action: "delete",
					UUID:   k[len(delPrefix):],
				})
			} else {
				isUpdatedName := strings.HasPrefix(k, editPrefix+namePrefix)
				isUpdatedExturl := strings.HasPrefix(k, editPrefix+exturlPrefix)
				isNewName := strings.HasPrefix(k, newPrefix+namePrefix)
				isNewExturl := strings.HasPrefix(k, newPrefix+exturlPrefix)

				if isUpdatedName || isUpdatedExturl || isNewName || isNewExturl {
					var uuid string

					if isUpdatedName {
						uuid = k[len(editPrefix+namePrefix):]
					} else if isUpdatedExturl {
						uuid = k[len(editPrefix+exturlPrefix):]
					} else if isNewName {
						uuid = k[len(newPrefix+namePrefix):]
					} else if isNewExturl {
						uuid = k[len(newPrefix+exturlPrefix):]
					}

					if _, ok := attachmentChangesByID[uuid]; !ok {
						attachmentChangesByID[uuid] = &release_service.AttachmentChange{
							Type: "attachment",
							UUID: uuid,
						}
						attachmentChanges.Add(attachmentChangesByID[uuid])
					}

					if isUpdatedName || isUpdatedExturl {
						attachmentChangesByID[uuid].Action = "update"
					} else if isNewName || isNewExturl {
						attachmentChangesByID[uuid].Action = "add"
					}

					if isUpdatedName || isNewName {
						attachmentChangesByID[uuid].Name = v[0]
					} else if isUpdatedExturl || isNewExturl {
						attachmentChangesByID[uuid].ExternalURL = v[0]
						attachmentChangesByID[uuid].Type = "external"
					}
				}
			}
		}
	}

	rel.Title = form.Title
	rel.Note = form.Content
	rel.IsDraft = len(form.Draft) > 0
	rel.IsPrerelease = form.Prerelease
	rel.HideArchiveLinks = form.HideArchiveLinks
	if err = release_service.UpdateRelease(ctx, ctx.Doer, ctx.Repo.GitRepo, rel, false, attachmentChanges.Values()); err != nil {
		switch {
		case repo_model.IsErrInvalidExternalURL(err):
			ctx.RenderWithErr(ctx.Tr("repo.release.invalid_external_url", err.(repo_model.ErrInvalidExternalURL).ExternalURL), tplReleaseNew, &form)
		default:
			ctx.ServerError("UpdateRelease", err)
		}
		return
	}
	ctx.Redirect(ctx.Repo.RepoLink + "/releases")
}

// DeleteRelease deletes a release
func DeleteRelease(ctx *context.Context) {
	deleteReleaseOrTag(ctx, false)
}

// DeleteTag deletes a tag
func DeleteTag(ctx *context.Context) {
	deleteReleaseOrTag(ctx, true)
}

func deleteReleaseOrTag(ctx *context.Context, isDelTag bool) {
	redirect := func() {
		if isDelTag {
			ctx.JSONRedirect(ctx.Repo.RepoLink + "/tags")
			return
		}

		ctx.JSONRedirect(ctx.Repo.RepoLink + "/releases")
	}

	rel, err := repo_model.GetReleaseForRepoByID(ctx, ctx.Repo.Repository.ID, ctx.FormInt64("id"))
	if err != nil {
		if repo_model.IsErrReleaseNotExist(err) {
			ctx.NotFound("GetReleaseForRepoByID", err)
		} else {
			ctx.Flash.Error("DeleteReleaseByID: " + err.Error())
			redirect()
		}
		return
	}

	if err := release_service.DeleteReleaseByID(ctx, ctx.Repo.Repository, rel, ctx.Doer, isDelTag); err != nil {
		if models.IsErrProtectedTagName(err) {
			ctx.Flash.Error(ctx.Tr("repo.release.tag_name_protected"))
		} else {
			ctx.Flash.Error("DeleteReleaseByID: " + err.Error())
		}
	} else {
		if isDelTag {
			ctx.Flash.Success(ctx.Tr("repo.release.deletion_tag_success"))
		} else {
			ctx.Flash.Success(ctx.Tr("repo.release.deletion_success"))
		}
	}

	redirect()
}
