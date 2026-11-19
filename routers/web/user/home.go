// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package user

import (
	"bytes"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	activities_model "forgejo.org/models/activities"
	asymkey_model "forgejo.org/models/asymkey"
	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	"forgejo.org/models/organization"
	project_model "forgejo.org/models/project"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/base"
	"forgejo.org/modules/container"
	issue_indexer "forgejo.org/modules/indexer/issues"
	"forgejo.org/modules/log"
	"forgejo.org/modules/markup"
	"forgejo.org/modules/markup/markdown"
	"forgejo.org/modules/optional"
	project_module "forgejo.org/modules/project"
	"forgejo.org/modules/setting"
	"forgejo.org/routers/web/feed"
	"forgejo.org/services/context"
	issue_service "forgejo.org/services/issue"
	pull_service "forgejo.org/services/pull"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"xorm.io/builder"
)

const (
	tplDashboard  base.TplName = "user/dashboard/dashboard"
	tplIssues     base.TplName = "user/dashboard/issues"
	tplMilestones base.TplName = "user/dashboard/milestones"
	tplProfile    base.TplName = "user/profile"
)

// getDashboardContextUser finds out which context user dashboard is being viewed as .
func getDashboardContextUser(ctx *context.Context) *user_model.User {
	ctxUser := ctx.Doer
	orgName := ctx.Params(":org")
	if len(orgName) > 0 {
		ctxUser = ctx.Org.Organization.AsUser()
		ctx.Data["Teams"] = ctx.Org.Teams
	}
	ctx.Data["ContextUser"] = ctxUser

	orgs, err := organization.GetUserOrgsList(ctx, ctx.Doer)
	if err != nil {
		ctx.ServerError("GetUserOrgsList", err)
		return nil
	}
	ctx.Data["Orgs"] = orgs

	return ctxUser
}

// Dashboard render the dashboard page
func Dashboard(ctx *context.Context) {
	ctxUser := getDashboardContextUser(ctx)
	if ctx.Written() {
		return
	}

	var (
		date = ctx.FormString("date")
		page = ctx.FormInt("page")
	)

	// Make sure page number is at least 1. Will be posted to ctx.Data.
	if page <= 1 {
		page = 1
	}

	ctx.Data["Title"] = ctxUser.DisplayName() + " - " + ctx.Locale.TrString("dashboard")
	ctx.Data["PageIsDashboard"] = true
	ctx.Data["PageIsNews"] = true
	cnt, _ := organization.GetOrganizationCount(ctx, ctxUser)
	ctx.Data["UserOrgsCount"] = cnt
	ctx.Data["MirrorsEnabled"] = setting.Mirror.Enabled
	ctx.Data["UsersPageIsDisabled"] = setting.Service.Explore.DisableUsersPage
	ctx.Data["OrganizationsPageIsDisabled"] = setting.Service.Explore.DisableOrganizationsPage
	ctx.Data["Date"] = date

	var uid int64
	if ctxUser != nil {
		uid = ctxUser.ID
	}

	ctx.PageData["dashboardRepoList"] = map[string]any{
		"searchLimit": setting.UI.User.RepoPagingNum,
		"uid":         uid,
	}

	if setting.Service.EnableUserHeatmap {
		data, err := activities_model.GetUserHeatmapDataByUserTeam(ctx, ctxUser, ctx.Org.Team, ctx.Doer)
		if err != nil {
			ctx.ServerError("GetUserHeatmapDataByUserTeam", err)
			return
		}
		ctx.Data["HeatmapData"] = data
		ctx.Data["HeatmapTotalContributions"] = activities_model.GetTotalContributionsInHeatmap(data)
	}

	feeds, count, err := activities_model.GetFeeds(ctx, activities_model.GetFeedsOptions{
		RequestedUser:   ctxUser,
		RequestedTeam:   ctx.Org.Team,
		Actor:           ctx.Doer,
		IncludePrivate:  true,
		OnlyPerformedBy: false,
		Date:            ctx.FormString("date"),
		ListOptions: db.ListOptions{
			Page:     page,
			PageSize: setting.UI.FeedPagingNum,
		},
	})
	if err != nil {
		ctx.ServerError("GetFeeds", err)
		return
	}

	ctx.Data["Feeds"] = feeds

	pager := context.NewPagination(int(count), setting.UI.FeedPagingNum, page, 5)
	pager.AddParam(ctx, "date", "Date")
	ctx.Data["Page"] = pager

	ctx.HTML(http.StatusOK, tplDashboard)
}

// Milestones render the user milestones page
func Milestones(ctx *context.Context) {
	if unit.TypeIssues.UnitGlobalDisabled() && unit.TypePullRequests.UnitGlobalDisabled() {
		log.Debug("Milestones overview page not available as both issues and pull requests are globally disabled")
		ctx.Status(http.StatusNotFound)
		return
	}

	ctx.Data["Title"] = ctx.Tr("milestones")
	ctx.Data["PageIsMilestonesDashboard"] = true

	ctxUser := getDashboardContextUser(ctx)
	if ctx.Written() {
		return
	}

	repoOpts := repo_model.SearchRepoOptions{
		Actor:         ctx.Doer,
		OwnerID:       ctxUser.ID,
		Private:       true,
		AllPublic:     false, // Include also all public repositories of users and public organisations
		AllLimited:    false, // Include also all public repositories of limited organisations
		Archived:      optional.Some(false),
		HasMilestones: optional.Some(true), // Just needs display repos has milestones
	}

	if ctxUser.IsOrganization() && ctx.Org.Team != nil {
		repoOpts.TeamID = ctx.Org.Team.ID
	}

	var (
		userRepoCond = repo_model.SearchRepositoryCondition(&repoOpts) // all repo condition user could visit
		repoCond     = userRepoCond
		repoIDs      []int64

		reposQuery   = ctx.FormString("repos")
		isShowClosed = ctx.FormString("state") == "closed"
		sortType     = ctx.FormString("sort")
		page         = ctx.FormInt("page")
		keyword      = ctx.FormTrim("q")
	)

	if page <= 1 {
		page = 1
	}

	if len(reposQuery) != 0 {
		if issueReposQueryPattern.MatchString(reposQuery) {
			// remove "[" and "]" from string
			reposQuery = reposQuery[1 : len(reposQuery)-1]
			// for each ID (delimiter ",") add to int to repoIDs

			for rID := range strings.SplitSeq(reposQuery, ",") {
				// Ensure nonempty string entries
				if rID != "" && rID != "0" {
					rIDint64, err := strconv.ParseInt(rID, 10, 64)
					// If the repo id specified by query is not parseable or not accessible by user, just ignore it.
					if err == nil {
						repoIDs = append(repoIDs, rIDint64)
					}
				}
			}
			if len(repoIDs) > 0 {
				// Don't just let repoCond = builder.In("id", repoIDs) because user may has no permission on repoIDs
				// But the original repoCond has a limitation
				repoCond = repoCond.And(builder.In("`repository`.id", repoIDs))
			}
		} else {
			log.Warn("issueReposQueryPattern not match with query")
		}
	}

	counts, err := issues_model.CountMilestonesMap(ctx, issues_model.FindMilestoneOptions{
		RepoCond: userRepoCond,
		Name:     keyword,
		IsClosed: optional.Some(isShowClosed),
	})
	if err != nil {
		ctx.ServerError("CountMilestonesByRepoIDs", err)
		return
	}

	milestones, err := db.Find[issues_model.Milestone](ctx, issues_model.FindMilestoneOptions{
		ListOptions: db.ListOptions{
			Page:     page,
			PageSize: setting.UI.IssuePagingNum,
		},
		RepoCond: repoCond,
		IsClosed: optional.Some(isShowClosed),
		SortType: sortType,
		Name:     keyword,
	})
	if err != nil {
		ctx.ServerError("SearchMilestones", err)
		return
	}

	showRepos, _, err := repo_model.SearchRepositoryByCondition(ctx, &repoOpts, userRepoCond, false)
	if err != nil {
		ctx.ServerError("SearchRepositoryByCondition", err)
		return
	}
	slices.SortFunc(showRepos, func(a, b *repo_model.Repository) int {
		return strings.Compare(a.FullName(), b.FullName())
	})

	for i := 0; i < len(milestones); {
		for _, repo := range showRepos {
			if milestones[i].RepoID == repo.ID {
				milestones[i].Repo = repo
				break
			}
		}
		if milestones[i].Repo == nil {
			log.Warn("Cannot find milestone %d 's repository %d", milestones[i].ID, milestones[i].RepoID)
			milestones = append(milestones[:i], milestones[i+1:]...)
			continue
		}

		milestones[i].RenderedContent, err = markdown.RenderString(&markup.RenderContext{
			Links: markup.Links{
				Base: milestones[i].Repo.Link(),
			},
			Metas: milestones[i].Repo.ComposeMetas(ctx),
			Ctx:   ctx,
		}, milestones[i].Content)
		if err != nil {
			ctx.ServerError("RenderString", err)
			return
		}

		if milestones[i].Repo.IsTimetrackerEnabled(ctx) {
			err := milestones[i].LoadTotalTrackedTime(ctx)
			if err != nil {
				ctx.ServerError("LoadTotalTrackedTime", err)
				return
			}
		}
		i++
	}

	milestoneStats, err := issues_model.GetMilestonesStatsByRepoCondAndKw(ctx, repoCond, keyword)
	if err != nil {
		ctx.ServerError("GetMilestoneStats", err)
		return
	}

	var totalMilestoneStats *issues_model.MilestonesStats
	if len(repoIDs) == 0 {
		totalMilestoneStats = milestoneStats
	} else {
		totalMilestoneStats, err = issues_model.GetMilestonesStatsByRepoCondAndKw(ctx, userRepoCond, keyword)
		if err != nil {
			ctx.ServerError("GetMilestoneStats", err)
			return
		}
	}

	showRepoIDs := make(container.Set[int64], len(showRepos))
	for _, repo := range showRepos {
		if repo.ID > 0 {
			showRepoIDs.Add(repo.ID)
		}
	}
	if len(repoIDs) == 0 {
		repoIDs = showRepoIDs.Values()
	}
	repoIDs = slices.DeleteFunc(repoIDs, func(v int64) bool {
		return !showRepoIDs.Contains(v)
	})

	var pagerCount int
	if isShowClosed {
		ctx.Data["State"] = "closed"
		ctx.Data["Total"] = totalMilestoneStats.ClosedCount
		pagerCount = int(milestoneStats.ClosedCount)
	} else {
		ctx.Data["State"] = "open"
		ctx.Data["Total"] = totalMilestoneStats.OpenCount
		pagerCount = int(milestoneStats.OpenCount)
	}

	ctx.Data["Milestones"] = milestones
	ctx.Data["Repos"] = showRepos
	ctx.Data["Counts"] = counts
	ctx.Data["MilestoneStats"] = milestoneStats
	ctx.Data["SortType"] = sortType
	ctx.Data["Keyword"] = keyword
	ctx.Data["RepoIDs"] = repoIDs
	ctx.Data["IsShowClosed"] = isShowClosed

	pager := context.NewPagination(pagerCount, setting.UI.IssuePagingNum, page, 5)
	pager.AddParam(ctx, "q", "Keyword")
	pager.AddParam(ctx, "repos", "RepoIDs")
	pager.AddParam(ctx, "sort", "SortType")
	pager.AddParam(ctx, "state", "State")
	ctx.Data["Page"] = pager

	ctx.HTML(http.StatusOK, tplMilestones)
}

// Pulls renders the user's pull request overview page
func Pulls(ctx *context.Context) {
	if unit.TypePullRequests.UnitGlobalDisabled() {
		log.Debug("Pull request overview page not available as it is globally disabled.")
		ctx.Status(http.StatusNotFound)
		return
	}

	ctx.Data["Title"] = ctx.Tr("pull_requests")
	ctx.Data["PageIsPulls"] = true
	buildIssueOverview(ctx, unit.TypePullRequests)
}

// Issues renders the user's issues overview page
func Issues(ctx *context.Context) {
	if unit.TypeIssues.UnitGlobalDisabled() {
		log.Debug("Issues overview page not available as it is globally disabled.")
		ctx.Status(http.StatusNotFound)
		return
	}

	ctx.Data["Title"] = ctx.Tr("issues")
	ctx.Data["PageIsIssues"] = true
	buildIssueOverview(ctx, unit.TypeIssues)
}

// Regexp for repos query
var issueReposQueryPattern = regexp.MustCompile(`^\[\d+(,\d+)*,?\]$`)

func buildIssueOverview(ctx *context.Context, unitType unit.Type) {
	// ----------------------------------------------------
	// Determine user; can be either user or organization.
	// Return with NotFound or ServerError if unsuccessful.
	// ----------------------------------------------------

	ctxUser := getDashboardContextUser(ctx)
	if ctx.Written() {
		return
	}

	var (
		viewType          string
		sortType          = ctx.FormString("sort")
		filterMode        int
		defaultFilterMode int
		defaultViewType   string
	)

	// Default to recently updated, unlike repository issues list
	if sortType == "" {
		sortType = "recentupdate"
	}

	// --------------------------------------------------------------------------------
	// Distinguish User from Organization.
	// Org:
	// - Remember pre-determined viewType string for later. Will be posted to ctx.Data.
	//   Organization does not have view type and filter mode.
	// User:
	// - Use ctx.FormString("type") to determine filterMode.
	//  The type is set when clicking for example "assigned to me" on the overview page.
	// - Remember either this or a fallback. Will be posted to ctx.Data.
	// --------------------------------------------------------------------------------

	// TODO: distinguish during routing

	// Default to created_by on /pulls and /issues
	// because it is most relevant to the user in the global context
	if ctx.Org == nil || ctx.Org.Organization == nil {
		defaultFilterMode = issues_model.FilterModeCreate
		defaultViewType = "created_by"
	} else {
		// Default to your_repositories on /org/*/pulls and /org/*/issues
		// because it is the most relevant to the user in the context of an org
		defaultFilterMode = issues_model.FilterModeYourRepositories
		defaultViewType = "your_repositories"
	}

	viewType = ctx.FormString("type")
	switch viewType {
	case "assigned":
		filterMode = issues_model.FilterModeAssign
	case "mentioned":
		filterMode = issues_model.FilterModeMention
	case "review_requested":
		filterMode = issues_model.FilterModeReviewRequested
	case "reviewed_by":
		filterMode = issues_model.FilterModeReviewed
	case "your_repositories":
		filterMode = issues_model.FilterModeYourRepositories
	case "created_by":
		fallthrough
	default:
		filterMode = defaultFilterMode
		viewType = defaultViewType
	}

	// --------------------------------------------------------------------------
	// Build opts (IssuesOptions), which contains filter information.
	// Will eventually be used to retrieve issues relevant for the overview page.
	// Note: Non-final states of opts are used in-between, namely for:
	//       - Keyword search
	//       - Count Issues by repo
	// --------------------------------------------------------------------------

	// Get repository IDs where User/Org/Team has access.
	var team *organization.Team
	var org *organization.Organization
	if ctx.Org != nil {
		org = ctx.Org.Organization
		team = ctx.Org.Team
	}

	isPullList := unitType == unit.TypePullRequests
	opts := &issues_model.IssuesOptions{
		IsPull:     optional.Some(isPullList),
		SortType:   sortType,
		IsArchived: optional.Some(false),
		Org:        org,
		Team:       team,
		User:       ctx.Doer,
	}

	// Search all repositories which
	//
	// As user:
	// - Owns the repository.
	// - Have collaborator permissions in repository.
	//
	// As org:
	// - Owns the repository.
	//
	// As team:
	// - Team org's owns the repository.
	// - Team has read permission to repository.
	repoOpts := &repo_model.SearchRepoOptions{
		Actor:       ctx.Doer,
		OwnerID:     ctxUser.ID,
		Private:     true,
		AllPublic:   false,
		AllLimited:  false,
		Collaborate: optional.None[bool](),
		UnitType:    unitType,
		Archived:    optional.Some(false),
		EnabledUnit: optional.Some(unitType),
	}
	if team != nil {
		repoOpts.TeamID = team.ID
	}
	accessibleRepos := container.Set[int64]{}
	{
		ids, _, err := repo_model.SearchRepositoryIDs(ctx, repoOpts)
		if err != nil {
			ctx.ServerError("SearchRepositoryIDs", err)
			return
		}
		accessibleRepos.AddMultiple(ids...)
		opts.RepoIDs = ids
		if len(opts.RepoIDs) == 0 {
			// no repos found, don't let the indexer return all repos
			opts.RepoIDs = []int64{0}
		}
	}
	if ctx.Doer.ID == ctxUser.ID && filterMode != issues_model.FilterModeYourRepositories {
		// If the doer is the same as the context user, which means the doer is viewing his own dashboard,
		// it's not enough to show the repos that the doer owns or has been explicitly granted access to,
		// because the doer may create issues or be mentioned in any public repo.
		// So we need search issues in all public repos.
		opts.AllPublic = true
	}

	switch filterMode {
	case issues_model.FilterModeAssign:
		opts.AssigneeID = ctx.Doer.ID
	case issues_model.FilterModeCreate:
		opts.PosterID = ctx.Doer.ID
	case issues_model.FilterModeMention:
		opts.MentionedID = ctx.Doer.ID
	case issues_model.FilterModeReviewRequested:
		opts.ReviewRequestedID = ctx.Doer.ID
	case issues_model.FilterModeReviewed:
		opts.ReviewedID = ctx.Doer.ID
	}

	// keyword holds the search term entered into the search field.
	keyword := strings.Trim(ctx.FormString("q"), " ")
	ctx.Data["Keyword"] = keyword

	// Educated guess: Do or don't show closed issues.
	isShowClosed := ctx.FormString("state") == "closed"
	opts.IsClosed = optional.Some(isShowClosed)

	// Make sure page number is at least 1. Will be posted to ctx.Data.
	page := max(ctx.FormInt("page"), 1)
	opts.Paginator = &db.ListOptions{
		Page:     page,
		PageSize: setting.UI.IssuePagingNum,
	}

	// Projects
	//
	// We do not consider listing projects for the individual repos.
	// Instead, we only support listing the projects under the scope of an individual/organisation.
	// However, this limitation does not affect filtering with a project ID.
	{
		projOpts := project_model.SearchOptions{
			ListOptions: db.ListOptionsAll,
			OwnerID:     ctxUser.ID,
			IsClosed:    optional.None[bool](),
			Type:        project_module.TypeIndividual,
		}
		if org != nil {
			projOpts.OwnerID = org.ID
			projOpts.Type = project_module.TypeOrganization
		}

		projects, err := db.Find[project_model.Project](ctx, projOpts)
		if err != nil {
			ctx.ServerError("GetProjects", err)
			return
		}

		if projectID := ctx.FormInt64("project"); projectID != 0 {
			opts.ProjectID = projectID
			ctx.Data["ProjectID"] = projectID
		}

		ctx.Data["Projects"] = projects
	}

	// Get IDs for labels (a filter option for issues/pulls).
	// Required for IssuesOptions.
	selectedLabels := ctx.FormString("labels")
	if len(selectedLabels) > 0 {
		var err error
		opts.LabelIDs, err = base.StringsToInt64s(strings.Split(selectedLabels, ","))
		if err != nil {
			ctx.Flash.Error(ctx.Tr("invalid_data", selectedLabels), true)
		}
		if slices.Contains(opts.LabelIDs, 0) {
			opts.LabelIDs = []int64{0}
			ctx.Data["NoLabel"] = true
		}
	}
	if len(opts.LabelIDs) == 0 {
		ctx.Data["AllLabels"] = true
	}

	if org != nil {
		// Get Org Labels
		labels, err := issues_model.GetLabelsByOrgID(ctx, ctx.Org.Organization.ID, ctx.FormString("sort"), db.ListOptions{})
		if err != nil {
			ctx.ServerError("GetLabelsByOrgID", err)
			return
		}

		// Get the exclusive scope for every label ID
		labelExclusiveScopes := make([]string, 0, len(opts.LabelIDs))
		for _, labelID := range opts.LabelIDs {
			foundExclusiveScope := false
			for _, label := range labels {
				if label.ID == labelID || label.ID == -labelID {
					labelExclusiveScopes = append(labelExclusiveScopes, label.ExclusiveScope())
					foundExclusiveScope = true
					break
				}
			}
			if !foundExclusiveScope {
				labelExclusiveScopes = append(labelExclusiveScopes, "")
			}
		}

		for _, l := range labels {
			l.LoadSelectedLabelsAfterClick(opts.LabelIDs, labelExclusiveScopes)
		}
		ctx.Data["Labels"] = labels
	}

	// ------------------------------
	// Get issues as defined by opts.
	// ------------------------------

	// Slice of Issues that will be displayed on the overview page
	// USING FINAL STATE OF opts FOR A QUERY.
	var issues issues_model.IssueList
	{
		issueIDs, _, err := issue_indexer.SearchIssues(ctx, issue_indexer.ToSearchOptions(ctx, keyword, opts))
		if err != nil {
			ctx.ServerError("issueIDsFromSearch", err)
			return
		}
		issues, err = issues_model.GetIssuesByIDs(ctx, issueIDs, true)
		if err != nil {
			ctx.ServerError("GetIssuesByIDs", err)
			return
		}
	}

	commitStatuses, lastStatus, err := pull_service.GetIssuesAllCommitStatus(ctx, issues)
	if err != nil {
		ctx.ServerError("GetIssuesAllCommitStatus", err)
		return
	}

	// -------------------------------
	// Fill stats to post to ctx.Data.
	// -------------------------------
	searchOpts := issue_indexer.ToSearchOptions(ctx, keyword, opts)
	issueStats, err := getUserIssueStats(ctx, ctxUser, filterMode, searchOpts)
	if err != nil {
		ctx.ServerError("getUserIssueStats", err)
		return
	}
	isShowClosed = searchOpts.IsClosed.ValueOrDefault(isShowClosed)
	sortType = searchOpts.SortBy.ToIssueSort()

	// Will be posted to ctx.Data.
	var shownIssues int
	if !isShowClosed {
		shownIssues = int(issueStats.OpenCount)
	} else {
		shownIssues = int(issueStats.ClosedCount)
	}

	ctx.Data["IsShowClosed"] = isShowClosed

	ctx.Data["IssueRefEndNames"], ctx.Data["IssueRefURLs"] = issue_service.GetRefEndNamesAndURLs(issues, ctx.FormString("RepoLink"))

	if err := issues.LoadAttributes(ctx); err != nil {
		ctx.ServerError("issues.LoadAttributes", err)
		return
	}
	ctx.Data["Issues"] = issues

	approvalCounts, err := issues.GetApprovalCounts(ctx)
	if err != nil {
		ctx.ServerError("ApprovalCounts", err)
		return
	}
	ctx.Data["ApprovalCounts"] = func(issueID int64, typ string) int64 {
		counts, ok := approvalCounts[issueID]
		if !ok || len(counts) == 0 {
			return 0
		}
		reviewTyp := issues_model.ReviewTypeApprove
		switch typ {
		case "reject":
			reviewTyp = issues_model.ReviewTypeReject
		case "waiting":
			reviewTyp = issues_model.ReviewTypeRequest
		}
		for _, count := range counts {
			if count.Type == reviewTyp {
				return count.Count
			}
		}
		return 0
	}
	ctx.Data["CommitLastStatus"] = lastStatus
	ctx.Data["CommitStatuses"] = commitStatuses
	ctx.Data["IssueStats"] = issueStats
	ctx.Data["ViewType"] = viewType
	ctx.Data["SortType"] = sortType
	ctx.Data["IsShowClosed"] = isShowClosed
	ctx.Data["SelectLabels"] = selectedLabels
	ctx.Data["PageIsOrgIssues"] = org != nil

	state := "open"
	if isShowClosed {
		state = "closed"
	}
	ctx.Data["State"] = state

	ctx.SetFormString("state", state)
	if has, value := searchOpts.AssigneeID.Get(); has {
		id := strconv.FormatInt(value, 10)
		ctx.SetFormString("assignee", id)
	}

	pager := context.NewPagination(shownIssues, setting.UI.IssuePagingNum, page, 5)
	pager.AddParam(ctx, "q", "Keyword")
	pager.AddParam(ctx, "type", "ViewType")
	pager.AddParam(ctx, "sort", "SortType")
	pager.AddParam(ctx, "state", "State")
	pager.AddParam(ctx, "labels", "SelectLabels")
	pager.AddParam(ctx, "milestone", "MilestoneID")
	pager.AddParam(ctx, "assignee", "AssigneeID")
	pager.AddParam(ctx, "project", "ProjectID")
	ctx.Data["Page"] = pager

	ctx.HTML(http.StatusOK, tplIssues)
}

// ShowSSHKeys output all the ssh keys of user by uid
func ShowSSHKeys(ctx *context.Context) {
	keys, err := db.Find[asymkey_model.PublicKey](ctx, asymkey_model.FindPublicKeyOptions{
		OwnerID: ctx.ContextUser.ID,
	})
	if err != nil {
		ctx.ServerError("ListPublicKeys", err)
		return
	}

	var buf bytes.Buffer
	for i := range keys {
		if keys[i].Type == asymkey_model.KeyTypePrincipal {
			continue // Don't display SSH principals, only public keys
		}
		buf.WriteString(keys[i].OmitEmail())
		buf.WriteString("\n")
	}

	if buf.Len() == 0 {
		buf.WriteString("# Note: This user hasn't uploaded any SSH keys.\n")
	}

	ctx.PlainTextBytes(http.StatusOK, buf.Bytes())
}

// ShowGPGKeys output all the public GPG keys of user by uid
func ShowGPGKeys(ctx *context.Context) {
	keys, err := db.Find[asymkey_model.GPGKey](ctx, asymkey_model.FindGPGKeyOptions{
		ListOptions: db.ListOptionsAll,
		OwnerID:     ctx.ContextUser.ID,
	})
	if err != nil {
		ctx.ServerError("ListGPGKeys", err)
		return
	}

	entities := make([]*openpgp.Entity, 0)
	failedEntitiesID := make([]string, 0)
	for _, k := range keys {
		e, err := asymkey_model.GPGKeyToEntity(ctx, k)
		if err != nil {
			if asymkey_model.IsErrGPGKeyImportNotExist(err) {
				failedEntitiesID = append(failedEntitiesID, k.KeyID)
				continue // Skip previous import without backup of imported armored key
			}
			ctx.ServerError("ShowGPGKeys", err)
			return
		}
		entities = append(entities, e)
	}
	var buf bytes.Buffer

	headers := make(map[string]string)
	if len(failedEntitiesID) > 0 { // If some key need re-import to be exported
		headers["Note"] = fmt.Sprintf("The keys with the following IDs couldn't be exported and need to be reuploaded %s", strings.Join(failedEntitiesID, ", "))
	} else if len(entities) == 0 {
		headers["Note"] = "This user hasn't uploaded any GPG keys."
	}
	writer, _ := armor.Encode(&buf, "PGP PUBLIC KEY BLOCK", headers)
	for _, e := range entities {
		err = e.Serialize(writer) // TODO find why key are exported with a different cipherTypeByte as original (should not be blocking but strange)
		if err != nil {
			ctx.ServerError("ShowGPGKeys", err)
			return
		}
	}
	writer.Close()
	ctx.PlainTextBytes(http.StatusOK, buf.Bytes())
}

func UsernameSubRoute(ctx *context.Context) {
	// WORKAROUND to support usernames with "." in it
	// https://github.com/go-chi/chi/issues/781
	username := ctx.Params("username")
	reloadParam := func(suffix string) (success bool) {
		ctx.SetParams("username", strings.TrimSuffix(username, suffix))
		context.UserAssignmentWeb()(ctx)
		if ctx.Written() {
			return false
		}
		// check view permissions
		if !user_model.IsUserVisibleToViewer(ctx, ctx.ContextUser, ctx.Doer) {
			ctx.NotFound("User not visible", nil)
			return false
		}
		return true
	}
	switch {
	case strings.HasSuffix(username, ".png"):
		if reloadParam(".png") {
			AvatarByUserName(ctx)
		}
	case strings.HasSuffix(username, ".keys"):
		if reloadParam(".keys") {
			ShowSSHKeys(ctx)
		}
	case strings.HasSuffix(username, ".gpg"):
		if reloadParam(".gpg") {
			ShowGPGKeys(ctx)
		}
	case strings.HasSuffix(username, ".rss"):
		if !setting.Other.EnableFeed {
			ctx.Error(http.StatusNotFound)
			return
		}
		if reloadParam(".rss") {
			feed.ShowUserFeedRSS(ctx)
		}
	case strings.HasSuffix(username, ".atom"):
		if !setting.Other.EnableFeed {
			ctx.Error(http.StatusNotFound)
			return
		}
		if reloadParam(".atom") {
			feed.ShowUserFeedAtom(ctx)
		}
	default:
		context.UserAssignmentWeb()(ctx)
		if !ctx.Written() {
			ctx.Data["EnableFeed"] = setting.Other.EnableFeed
			OwnerProfile(ctx)
		}
	}
}

func getUserIssueStats(ctx *context.Context, ctxUser *user_model.User, filterMode int, opts *issue_indexer.SearchOptions) (*issues_model.IssueStats, error) {
	doerID := ctx.Doer.ID

	opts = opts.Copy(func(o *issue_indexer.SearchOptions) {
		// If the doer is the same as the context user, which means the doer is viewing his own dashboard,
		// it's not enough to show the repos that the doer owns or has been explicitly granted access to,
		// because the doer may create issues or be mentioned in any public repo.
		// So we need search issues in all public repos.
		o.AllPublic = doerID == ctxUser.ID
		o.AssigneeID = nil
		o.PosterID = nil
		o.MentionID = nil
		o.ReviewRequestedID = nil
		o.ReviewedID = nil
	})

	var (
		err error
		ret = &issues_model.IssueStats{}
	)

	{
		openClosedOpts := opts.Copy()
		switch filterMode {
		case issues_model.FilterModeAll:
			break
		case issues_model.FilterModeYourRepositories:
			openClosedOpts.AllPublic = false
		case issues_model.FilterModeAssign:
			openClosedOpts.AssigneeID = optional.Some(doerID)
		case issues_model.FilterModeCreate:
			openClosedOpts.PosterID = optional.Some(doerID)
		case issues_model.FilterModeMention:
			openClosedOpts.MentionID = optional.Some(doerID)
		case issues_model.FilterModeReviewRequested:
			openClosedOpts.ReviewRequestedID = optional.Some(doerID)
		case issues_model.FilterModeReviewed:
			openClosedOpts.ReviewedID = optional.Some(doerID)
		}
		openClosedOpts.IsClosed = optional.Some(false)
		ret.OpenCount, err = issue_indexer.CountIssues(ctx, openClosedOpts)
		if err != nil {
			return nil, err
		}
		openClosedOpts.IsClosed = optional.Some(true)
		ret.ClosedCount, err = issue_indexer.CountIssues(ctx, openClosedOpts)
		if err != nil {
			return nil, err
		}
	}

	ret.YourRepositoriesCount, err = issue_indexer.CountIssues(ctx, opts.Copy(func(o *issue_indexer.SearchOptions) { o.AllPublic = false }))
	if err != nil {
		return nil, err
	}
	ret.AssignCount, err = issue_indexer.CountIssues(ctx, opts.Copy(func(o *issue_indexer.SearchOptions) { o.AssigneeID = optional.Some(doerID) }))
	if err != nil {
		return nil, err
	}
	ret.CreateCount, err = issue_indexer.CountIssues(ctx, opts.Copy(func(o *issue_indexer.SearchOptions) { o.PosterID = optional.Some(doerID) }))
	if err != nil {
		return nil, err
	}
	ret.MentionCount, err = issue_indexer.CountIssues(ctx, opts.Copy(func(o *issue_indexer.SearchOptions) { o.MentionID = optional.Some(doerID) }))
	if err != nil {
		return nil, err
	}
	ret.ReviewRequestedCount, err = issue_indexer.CountIssues(ctx, opts.Copy(func(o *issue_indexer.SearchOptions) { o.ReviewRequestedID = optional.Some(doerID) }))
	if err != nil {
		return nil, err
	}
	ret.ReviewedCount, err = issue_indexer.CountIssues(ctx, opts.Copy(func(o *issue_indexer.SearchOptions) { o.ReviewedID = optional.Some(doerID) }))
	if err != nil {
		return nil, err
	}
	return ret, nil
}
