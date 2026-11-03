// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2018 The Gitea Authors. All rights reserved.
// Copyright 2024 The Forgejo Authors. All rights reserved.
// All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	stdCtx "context"
	"errors"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"forgejo.org/models"
	actions_model "forgejo.org/models/actions"
	activities_model "forgejo.org/models/activities"
	asymkey_model "forgejo.org/models/asymkey"
	"forgejo.org/models/db"
	git_model "forgejo.org/models/git"
	issues_model "forgejo.org/models/issues"
	"forgejo.org/models/organization"
	access_model "forgejo.org/models/perm/access"
	pull_model "forgejo.org/models/pull"
	quota_model "forgejo.org/models/quota"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/base"
	"forgejo.org/modules/charset"
	"forgejo.org/modules/emoji"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	issue_template "forgejo.org/modules/issue/template"
	"forgejo.org/modules/log"
	"forgejo.org/modules/markup"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/structs"
	"forgejo.org/modules/util"
	"forgejo.org/modules/web"
	"forgejo.org/routers/utils"
	actions_service "forgejo.org/services/actions"
	asymkey_service "forgejo.org/services/asymkey"
	"forgejo.org/services/automerge"
	"forgejo.org/services/context"
	"forgejo.org/services/context/upload"
	"forgejo.org/services/forms"
	"forgejo.org/services/gitdiff"
	notify_service "forgejo.org/services/notify"
	pull_service "forgejo.org/services/pull"
	repo_service "forgejo.org/services/repository"

	"github.com/gobwas/glob"
)

const (
	tplFork        base.TplName = "repo/pulls/fork"
	tplCompareDiff base.TplName = "repo/diff/compare"
	tplPullCommits base.TplName = "repo/pulls/commits"
	tplPullFiles   base.TplName = "repo/pulls/files"

	pullRequestTemplateKey = "PullRequestTemplate"
)

// generatePullRequestTemplateLocations generates all the file paths where we
// look for a pull request template, e.g. ".forgejo/PULL_REQUEST_TEMPLATE.md".
func generatePullRequestTemplateLocations() []string {
	var result []string
	prefixes := []string{"", ".forgejo/", ".gitea/", ".github/", "docs/"}
	filenames := []string{"PULL_REQUEST_TEMPLATE", "pull_request_template"}
	extensions := []string{".md", ".yaml", ".yml"}

	for _, prefix := range prefixes {
		for _, filename := range filenames {
			for _, extension := range extensions {
				result = append(result, prefix+filename+extension)
			}
		}
	}

	return result
}

var pullRequestTemplateCandidates = generatePullRequestTemplateLocations()

func getRepository(ctx *context.Context, repoID int64) *repo_model.Repository {
	repo, err := repo_model.GetRepositoryByID(ctx, repoID)
	if err != nil {
		if repo_model.IsErrRepoNotExist(err) {
			ctx.NotFound("GetRepositoryByID", nil)
		} else {
			ctx.ServerError("GetRepositoryByID", err)
		}
		return nil
	}

	perm, err := access_model.GetUserRepoPermission(ctx, repo, ctx.Doer)
	if err != nil {
		ctx.ServerError("GetUserRepoPermission", err)
		return nil
	}

	if !perm.CanRead(unit.TypeCode) {
		log.Trace("Permission Denied: User %-v cannot read %-v of repo %-v\n"+
			"User in repo has Permissions: %-+v",
			ctx.Doer,
			unit.TypeCode,
			ctx.Repo,
			perm)
		ctx.NotFound("getRepository", nil)
		return nil
	}
	return repo
}

func updateForkRepositoryInContext(ctx *context.Context, forkRepo *repo_model.Repository) bool {
	if forkRepo == nil {
		ctx.NotFound("No repository in context", nil)
		return false
	}

	if forkRepo.IsEmpty {
		log.Trace("Empty repository %-v", forkRepo)
		ctx.NotFound("updateForkRepositoryInContext", nil)
		return false
	}

	if err := forkRepo.LoadOwner(ctx); err != nil {
		ctx.ServerError("LoadOwner", err)
		return false
	}

	ctx.Data["repo_name"] = forkRepo.Name
	ctx.Data["description"] = forkRepo.Description
	ctx.Data["IsPrivate"] = forkRepo.IsPrivate || forkRepo.Owner.Visibility == structs.VisibleTypePrivate
	canForkToUser := forkRepo.OwnerID != ctx.Doer.ID && !repo_model.HasForkedRepo(ctx, ctx.Doer.ID, forkRepo.ID)

	ctx.Data["ForkRepo"] = forkRepo

	ownedOrgs, err := organization.GetOrgsCanCreateRepoByUserID(ctx, ctx.Doer.ID)
	if err != nil {
		ctx.ServerError("GetOrgsCanCreateRepoByUserID", err)
		return false
	}
	var orgs []*organization.Organization
	for _, org := range ownedOrgs {
		if forkRepo.OwnerID != org.ID && !repo_model.HasForkedRepo(ctx, org.ID, forkRepo.ID) {
			orgs = append(orgs, org)
		}
	}

	traverseParentRepo := forkRepo
	for {
		if ctx.Doer.ID == traverseParentRepo.OwnerID {
			canForkToUser = false
		} else {
			for i, org := range orgs {
				if org.ID == traverseParentRepo.OwnerID {
					orgs = append(orgs[:i], orgs[i+1:]...)
					break
				}
			}
		}

		if !traverseParentRepo.IsFork {
			break
		}
		traverseParentRepo, err = repo_model.GetRepositoryByID(ctx, traverseParentRepo.ForkID)
		if err != nil {
			ctx.ServerError("GetRepositoryByID", err)
			return false
		}
	}

	ctx.Data["CanForkToUser"] = canForkToUser
	ctx.Data["Orgs"] = orgs

	if canForkToUser {
		ctx.Data["ContextUser"] = ctx.Doer
	} else if len(orgs) > 0 {
		ctx.Data["ContextUser"] = orgs[0]
	} else {
		ctx.Data["CanForkRepo"] = false
		ctx.RenderWithErr(ctx.Tr("repo.fork_no_valid_owners"), tplFork, nil)
		return false
	}

	branches, err := git_model.FindBranchNames(ctx, git_model.FindBranchOptions{
		RepoID: ctx.Repo.Repository.ID,
		ListOptions: db.ListOptions{
			ListAll: true,
		},
		IsDeletedBranch: optional.Some(false),
		// Add it as the first option
		ExcludeBranchNames: []string{ctx.Repo.Repository.DefaultBranch},
	})
	if err != nil {
		ctx.ServerError("FindBranchNames", err)
		return false
	}
	ctx.Data["Branches"] = append([]string{ctx.Repo.Repository.DefaultBranch}, branches...)

	return true
}

// ForkByID redirects (with 301 Moved Permanently) to the repository's `/fork` page
func ForkByID(ctx *context.Context) {
	ctx.Redirect(ctx.Repo.Repository.Link()+"/fork", http.StatusMovedPermanently)
}

// Fork renders the repository fork page
func Fork(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("new_fork")

	if ctx.Doer.CanForkRepo() {
		ctx.Data["CanForkRepo"] = true
	} else {
		maxCreationLimit := ctx.Doer.MaxCreationLimit()
		msg := ctx.TrN(maxCreationLimit, "repo.form.reach_limit_of_creation_1", "repo.form.reach_limit_of_creation_n", maxCreationLimit)
		ctx.Flash.Error(msg, true)
	}

	if !updateForkRepositoryInContext(ctx, ctx.Repo.Repository) {
		return
	}

	ctx.HTML(http.StatusOK, tplFork)
}

// ForkPost response for forking a repository
func ForkPost(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.CreateRepoForm)
	ctx.Data["Title"] = ctx.Tr("new_fork")
	ctx.Data["CanForkRepo"] = true

	ctxUser := checkContextUser(ctx, form.UID)
	if ctx.Written() {
		return
	}

	forkRepo := ctx.Repo.Repository
	if !updateForkRepositoryInContext(ctx, forkRepo) {
		return
	}

	ctx.Data["ContextUser"] = ctxUser

	if !ctx.CheckQuota(quota_model.LimitSubjectSizeReposAll, ctxUser.ID, ctxUser.Name) {
		return
	}

	if ctx.HasError() {
		ctx.HTML(http.StatusOK, tplFork)
		return
	}

	var err error
	traverseParentRepo := forkRepo
	for {
		if ctxUser.ID == traverseParentRepo.OwnerID {
			ctx.RenderWithErr(ctx.Tr("repo.settings.new_owner_has_same_repo"), tplFork, &form)
			return
		}
		repo := repo_model.GetForkedRepo(ctx, ctxUser.ID, traverseParentRepo.ID)
		if repo != nil {
			ctx.Redirect(ctxUser.HomeLink() + "/" + url.PathEscape(repo.Name))
			return
		}
		if !traverseParentRepo.IsFork {
			break
		}
		traverseParentRepo, err = repo_model.GetRepositoryByID(ctx, traverseParentRepo.ForkID)
		if err != nil {
			ctx.ServerError("GetRepositoryByID", err)
			return
		}
	}

	// Check if user is allowed to create repo's on the organization.
	if ctxUser.IsOrganization() {
		isAllowedToFork, err := organization.OrgFromUser(ctxUser).CanCreateOrgRepo(ctx, ctx.Doer.ID)
		if err != nil {
			ctx.ServerError("CanCreateOrgRepo", err)
			return
		} else if !isAllowedToFork {
			ctx.Error(http.StatusForbidden)
			return
		}
	}

	repo, err := repo_service.ForkRepositoryAndUpdates(ctx, ctx.Doer, ctxUser, repo_service.ForkRepoOptions{
		BaseRepo:     forkRepo,
		Name:         form.RepoName,
		Description:  form.Description,
		SingleBranch: form.ForkSingleBranch,
	})
	if err != nil {
		ctx.Data["Err_RepoName"] = true
		switch {
		case repo_model.IsErrReachLimitOfRepo(err):
			maxCreationLimit := ctxUser.MaxCreationLimit()
			msg := ctx.TrN(maxCreationLimit, "repo.form.reach_limit_of_creation_1", "repo.form.reach_limit_of_creation_n", maxCreationLimit)
			ctx.RenderWithErr(msg, tplFork, &form)
		case repo_model.IsErrRepoAlreadyExist(err):
			ctx.RenderWithErr(ctx.Tr("repo.settings.new_owner_has_same_repo"), tplFork, &form)
		case repo_model.IsErrRepoFilesAlreadyExist(err):
			switch {
			case ctx.IsUserSiteAdmin() || (setting.Repository.AllowAdoptionOfUnadoptedRepositories && setting.Repository.AllowDeleteOfUnadoptedRepositories):
				ctx.RenderWithErr(ctx.Tr("form.repository_files_already_exist.adopt_or_delete"), tplFork, form)
			case setting.Repository.AllowAdoptionOfUnadoptedRepositories:
				ctx.RenderWithErr(ctx.Tr("form.repository_files_already_exist.adopt"), tplFork, form)
			case setting.Repository.AllowDeleteOfUnadoptedRepositories:
				ctx.RenderWithErr(ctx.Tr("form.repository_files_already_exist.delete"), tplFork, form)
			default:
				ctx.RenderWithErr(ctx.Tr("form.repository_files_already_exist"), tplFork, form)
			}
		case db.IsErrNameReserved(err):
			ctx.RenderWithErr(ctx.Tr("repo.form.name_reserved", err.(db.ErrNameReserved).Name), tplFork, &form)
		case db.IsErrNamePatternNotAllowed(err):
			ctx.RenderWithErr(ctx.Tr("repo.form.name_pattern_not_allowed", err.(db.ErrNamePatternNotAllowed).Pattern), tplFork, &form)
		default:
			ctx.ServerError("ForkPost", err)
		}
		return
	}

	log.Trace("Repository forked[%d]: %s/%s", forkRepo.ID, ctxUser.Name, repo.Name)
	ctx.Redirect(ctxUser.HomeLink() + "/" + url.PathEscape(repo.Name))
}

func getPullInfo(ctx *context.Context) (issue *issues_model.Issue, ok bool) {
	issue, err := issues_model.GetIssueByIndex(ctx, ctx.Repo.Repository.ID, ctx.ParamsInt64(":index"))
	if err != nil {
		if issues_model.IsErrIssueNotExist(err) {
			ctx.NotFound("GetIssueByIndex", err)
		} else {
			ctx.ServerError("GetIssueByIndex", err)
		}
		return nil, false
	}
	if err = issue.LoadPoster(ctx); err != nil {
		ctx.ServerError("LoadPoster", err)
		return nil, false
	}
	if err := issue.LoadRepo(ctx); err != nil {
		ctx.ServerError("LoadRepo", err)
		return nil, false
	}
	ctx.Data["Title"] = fmt.Sprintf("#%d - %s", issue.Index, emoji.ReplaceAliases(issue.Title))
	ctx.Data["Issue"] = issue

	if setting.Other.EnableFeed {
		ctx.Data["EnableFeed"] = true
		ctx.Data["FeedURL"] = issue.HTMLURL()
	}

	if !issue.IsPull {
		ctx.Redirect(issue.Link())
		return nil, false
	}

	if err = issue.LoadPullRequest(ctx); err != nil {
		ctx.ServerError("LoadPullRequest", err)
		return nil, false
	}

	if err = issue.PullRequest.LoadHeadRepo(ctx); err != nil {
		ctx.ServerError("LoadHeadRepo", err)
		return nil, false
	}

	if ctx.IsSigned {
		// Update issue-user.
		if err = activities_model.SetIssueReadBy(ctx, issue.ID, ctx.Doer.ID); err != nil {
			ctx.ServerError("ReadBy", err)
			return nil, false
		}
	}

	return issue, true
}

func setMergeTarget(ctx *context.Context, pull *issues_model.PullRequest) {
	if ctx.Repo.Owner.Name == pull.MustHeadUserName(ctx) {
		ctx.Data["HeadTarget"] = pull.HeadBranch
	} else if pull.HeadRepo == nil {
		ctx.Data["HeadTarget"] = pull.MustHeadUserName(ctx) + ":" + pull.HeadBranch
	} else {
		ctx.Data["HeadTarget"] = pull.MustHeadUserName(ctx) + "/" + pull.HeadRepo.Name + ":" + pull.HeadBranch
	}

	if pull.Flow == issues_model.PullRequestFlowAGit {
		ctx.Data["MadeUsingAGit"] = true
	}

	ctx.Data["BaseTarget"] = pull.BaseBranch
	ctx.Data["HeadBranchLink"] = pull.GetHeadBranchLink(ctx)
	ctx.Data["BaseBranchLink"] = pull.GetBaseBranchLink(ctx)
}

// GetPullDiffStats get Pull Requests diff stats
func GetPullDiffStats(ctx *context.Context) {
	// FIXME: this getPullInfo seems to be a duplicate call with other route handlers
	issue, ok := getPullInfo(ctx)
	if !ok {
		return
	}
	pull := issue.PullRequest

	mergeBaseCommitID := GetMergedBaseCommitID(ctx, issue)
	if mergeBaseCommitID == "" {
		ctx.NotFound("PullFiles", nil)
		return
	}

	// do not report 500 server error to end users if error occurs, otherwise a PR missing ref won't be able to view.
	headCommitID, err := ctx.Repo.GitRepo.GetRefCommitID(pull.GetGitRefName())
	if err != nil {
		log.Error("Failed to GetRefCommitID: %v, repo: %v", err, ctx.Repo.Repository.FullName())
		return
	}

	diffOptions := &gitdiff.DiffOptions{
		BeforeCommitID:     mergeBaseCommitID,
		AfterCommitID:      headCommitID,
		MaxLines:           setting.Git.MaxGitDiffLines,
		MaxLineCharacters:  setting.Git.MaxGitDiffLineCharacters,
		WhitespaceBehavior: gitdiff.GetWhitespaceFlag(ctx.Data["WhitespaceBehavior"].(string)),
	}

	diff, err := gitdiff.GetPullDiffStats(ctx.Repo.GitRepo, diffOptions)
	if err != nil {
		ctx.ServerError("GetPullDiffStats", err)
		return
	}

	ctx.Data["Diff"] = diff
}

func GetMergedBaseCommitID(ctx *context.Context, issue *issues_model.Issue) string {
	pull := issue.PullRequest

	var baseCommit string
	// Some migrated PR won't have any Base SHA and lose history, try to get one
	if pull.MergeBase == "" {
		var commitSHA, parentCommit string
		// If there is a head or a patch file, and it is readable, grab info
		commitSHA, err := ctx.Repo.GitRepo.GetRefCommitID(pull.GetGitRefName())
		if err != nil {
			// Head File does not exist, try the patch
			commitSHA, err = ctx.Repo.GitRepo.ReadPatchCommit(pull.Index)
			if err == nil {
				// Recreate pull head in files for next time
				if err := ctx.Repo.GitRepo.SetReference(pull.GetGitRefName(), commitSHA); err != nil {
					log.Error("Could not write head file", err)
				}
			} else {
				// There is no history available
				log.Trace("No history file available for PR %d", pull.Index)
			}
		}
		if commitSHA != "" {
			// Get immediate parent of the first commit in the patch, grab history back
			parentCommit, _, err = git.NewCommand(ctx, "rev-list", "-1", "--skip=1").AddDynamicArguments(commitSHA).RunStdString(&git.RunOpts{Dir: ctx.Repo.GitRepo.Path})
			if err == nil {
				parentCommit = strings.TrimSpace(parentCommit)
			}
			// Special case on Git < 2.25 that doesn't fail on immediate empty history
			if err != nil || parentCommit == "" {
				log.Info("No known parent commit for PR %d, error: %v", pull.Index, err)
				// bring at least partial history if it can work
				parentCommit = commitSHA
			}
		}
		baseCommit = parentCommit
	} else {
		// Keep an empty history or original commit
		baseCommit = pull.MergeBase
	}

	return baseCommit
}

// PrepareMergedViewPullInfo show meta information for a merged pull request view page
func PrepareMergedViewPullInfo(ctx *context.Context, issue *issues_model.Issue) *git.CompareInfo {
	pull := issue.PullRequest

	setMergeTarget(ctx, pull)
	ctx.Data["HasMerged"] = true

	baseCommit := GetMergedBaseCommitID(ctx, issue)

	compareInfo, err := ctx.Repo.GitRepo.GetCompareInfo(baseCommit, pull.GetGitRefName(), false, false)
	if err != nil {
		if strings.Contains(err.Error(), "fatal: Not a valid object name") || strings.Contains(err.Error(), "unknown revision or path not in the working tree") {
			ctx.Data["IsPullRequestBroken"] = true
			ctx.Data["BaseTarget"] = pull.BaseBranch
			ctx.Data["NumCommits"] = 0
			ctx.Data["CommitIDs"] = map[string]bool{}
			ctx.Data["NumFiles"] = 0
			return nil
		}

		ctx.ServerError("GetCompareInfo", err)
		return nil
	}
	ctx.Data["NumCommits"] = len(compareInfo.Commits)
	ctx.Data["NumFiles"] = compareInfo.NumFiles

	commitIDs := map[string]bool{}
	for _, commit := range compareInfo.Commits {
		commitIDs[commit.ID.String()] = true
	}
	ctx.Data["CommitIDs"] = commitIDs

	if len(compareInfo.Commits) != 0 {
		sha := compareInfo.Commits[0].ID.String()
		commitStatuses, _, err := git_model.GetLatestCommitStatus(ctx, ctx.Repo.Repository.ID, sha, db.ListOptionsAll)
		if err != nil {
			ctx.ServerError("GetLatestCommitStatus", err)
			return nil
		}

		if len(commitStatuses) != 0 {
			ctx.Data["LatestCommitStatuses"] = commitStatuses
			ctx.Data["LatestCommitStatus"] = git_model.CalcCommitStatus(commitStatuses)
		}
	}

	PrepareViewPullInfoActions(ctx, pull)
	if ctx.Written() {
		return nil
	}

	return compareInfo
}

// PrepareViewPullInfo show meta information for a pull request preview page
func PrepareViewPullInfo(ctx *context.Context, issue *issues_model.Issue) *git.CompareInfo {
	ctx.Data["PullRequestWorkInProgressPrefixes"] = setting.Repository.PullRequest.WorkInProgressPrefixes

	repo := ctx.Repo.Repository
	pull := issue.PullRequest

	if err := pull.LoadHeadRepo(ctx); err != nil {
		ctx.ServerError("LoadHeadRepo", err)
		return nil
	}

	if err := pull.LoadBaseRepo(ctx); err != nil {
		ctx.ServerError("LoadBaseRepo", err)
		return nil
	}

	setMergeTarget(ctx, pull)

	pb, err := git_model.GetFirstMatchProtectedBranchRule(ctx, repo.ID, pull.BaseBranch)
	if err != nil {
		ctx.ServerError("LoadProtectedBranch", err)
		return nil
	}
	ctx.Data["EnableStatusCheck"] = pb != nil && pb.EnableStatusCheck

	var baseGitRepo *git.Repository
	if pull.BaseRepoID == ctx.Repo.Repository.ID && ctx.Repo.GitRepo != nil {
		baseGitRepo = ctx.Repo.GitRepo
	} else {
		baseGitRepo, err := gitrepo.OpenRepository(ctx, pull.BaseRepo)
		if err != nil {
			ctx.ServerError("OpenRepository", err)
			return nil
		}
		defer baseGitRepo.Close()
	}

	if !baseGitRepo.IsBranchExist(pull.BaseBranch) {
		ctx.Data["IsPullRequestBroken"] = true
		ctx.Data["BaseTarget"] = pull.BaseBranch
		ctx.Data["HeadTarget"] = pull.HeadBranch

		sha, err := baseGitRepo.GetRefCommitID(pull.GetGitRefName())
		if err != nil {
			ctx.ServerError(fmt.Sprintf("GetRefCommitID(%s)", pull.GetGitRefName()), err)
			return nil
		}
		commitStatuses, _, err := git_model.GetLatestCommitStatus(ctx, repo.ID, sha, db.ListOptionsAll)
		if err != nil {
			ctx.ServerError("GetLatestCommitStatus", err)
			return nil
		}

		if len(commitStatuses) > 0 {
			ctx.Data["LatestCommitStatuses"] = commitStatuses
			ctx.Data["LatestCommitStatus"] = git_model.CalcCommitStatus(commitStatuses)
		}

		compareInfo, err := baseGitRepo.GetCompareInfo(pull.MergeBase, pull.GetGitRefName(), false, false)
		if err != nil {
			if strings.Contains(err.Error(), "fatal: Not a valid object name") {
				ctx.Data["IsPullRequestBroken"] = true
				ctx.Data["BaseTarget"] = pull.BaseBranch
				ctx.Data["NumCommits"] = 0
				ctx.Data["CommitIDs"] = map[string]bool{}
				ctx.Data["NumFiles"] = 0
				return nil
			}

			ctx.ServerError("GetCompareInfo", err)
			return nil
		}

		ctx.Data["NumCommits"] = len(compareInfo.Commits)
		ctx.Data["NumFiles"] = compareInfo.NumFiles

		commitIDs := map[string]bool{}
		for _, commit := range compareInfo.Commits {
			commitIDs[commit.ID.String()] = true
		}
		ctx.Data["CommitIDs"] = commitIDs

		return compareInfo
	}

	var headBranchExist bool
	var headBranchSha string
	// HeadRepo may be missing
	if pull.HeadRepo != nil {
		headGitRepo, closer, err := gitrepo.RepositoryFromContextOrOpen(ctx, pull.HeadRepo)
		if err != nil {
			ctx.ServerError("RepositoryFromContextOrOpen", err)
			return nil
		}
		defer closer.Close()

		if pull.Flow == issues_model.PullRequestFlowGithub {
			headBranchExist = headGitRepo.IsBranchExist(pull.HeadBranch)
		} else {
			headBranchExist = baseGitRepo.IsReferenceExist(pull.GetGitRefName())
		}

		if headBranchExist {
			if pull.Flow != issues_model.PullRequestFlowGithub {
				headBranchSha, err = baseGitRepo.GetRefCommitID(pull.GetGitRefName())
			} else {
				headBranchSha, err = headGitRepo.GetBranchCommitID(pull.HeadBranch)
			}
			if err != nil {
				ctx.ServerError("GetBranchCommitID", err)
				return nil
			}
		}
	}

	if headBranchExist {
		headRepoPerm, err := access_model.GetUserRepoPermission(ctx, pull.HeadRepo, ctx.Doer)
		if err != nil {
			ctx.ServerError("GetUserRepoPermission head", err)
			return nil
		}
		baseRepoPerm, err := access_model.GetUserRepoPermission(ctx, pull.BaseRepo, ctx.Doer)
		if err != nil {
			ctx.ServerError("GetUserRepoPermission base", err)
			return nil
		}

		ctx.Data["UpdateAllowed"], ctx.Data["UpdateByRebaseAllowed"], err = pull_service.IsUserAllowedToUpdate(ctx, pull, ctx.Doer, headRepoPerm, baseRepoPerm)
		if err != nil {
			ctx.ServerError("IsUserAllowedToUpdate", err)
			return nil
		}
		ctx.Data["GetCommitMessages"] = pull_service.GetSquashMergeCommitMessages(ctx, pull)
	} else {
		ctx.Data["GetCommitMessages"] = ""
	}

	sha, err := baseGitRepo.GetRefCommitID(pull.GetGitRefName())
	if err != nil {
		if git.IsErrNotExist(err) {
			ctx.Data["IsPullRequestBroken"] = true
			if pull.IsSameRepo() {
				ctx.Data["HeadTarget"] = pull.HeadBranch
			} else if pull.HeadRepo == nil {
				ctx.Data["HeadTarget"] = ctx.Locale.Tr("repo.pull.deleted_branch", pull.HeadBranch)
			} else {
				ctx.Data["HeadTarget"] = pull.HeadRepo.OwnerName + ":" + pull.HeadBranch
			}
			ctx.Data["BaseTarget"] = pull.BaseBranch
			ctx.Data["NumCommits"] = 0
			ctx.Data["CommitIDs"] = map[string]bool{}
			ctx.Data["NumFiles"] = 0
			return nil
		}
		ctx.ServerError(fmt.Sprintf("GetRefCommitID(%s)", pull.GetGitRefName()), err)
		return nil
	}

	commitStatuses, _, err := git_model.GetLatestCommitStatus(ctx, repo.ID, sha, db.ListOptionsAll)
	if err != nil {
		ctx.ServerError("GetLatestCommitStatus", err)
		return nil
	}

	if len(commitStatuses) > 0 {
		ctx.Data["LatestCommitStatuses"] = commitStatuses
		ctx.Data["LatestCommitStatus"] = git_model.CalcCommitStatus(commitStatuses)
	}

	if pb != nil && pb.EnableStatusCheck {
		var missingRequiredChecks []string
		for _, requiredContext := range pb.StatusCheckContexts {
			contextFound := false
			matchesRequiredContext := createRequiredContextMatcher(requiredContext)
			for _, presentStatus := range commitStatuses {
				if matchesRequiredContext(presentStatus.Context) {
					contextFound = true
					break
				}
			}

			if !contextFound {
				missingRequiredChecks = append(missingRequiredChecks, requiredContext)
			}
		}
		ctx.Data["MissingRequiredChecks"] = missingRequiredChecks

		ctx.Data["is_context_required"] = func(context string) bool {
			for _, c := range pb.StatusCheckContexts {
				if c == context {
					return true
				}
				if gp, err := glob.Compile(c); err != nil {
					// All newly created status_check_contexts are checked to ensure they are valid glob expressions before being stored in the database.
					// But some old status_check_context created before glob was introduced may be invalid glob expressions.
					// So log the error here for debugging.
					log.Error("compile glob %q: %v", c, err)
				} else if gp.Match(context) {
					return true
				}
			}
			return false
		}
		ctx.Data["RequiredStatusCheckState"] = pull_service.MergeRequiredContextsCommitStatus(commitStatuses, pb.StatusCheckContexts)
	}

	ctx.Data["HeadBranchMovedOn"] = headBranchSha != sha
	ctx.Data["HeadBranchCommitID"] = headBranchSha
	ctx.Data["PullHeadCommitID"] = sha

	if pull.HeadRepo == nil || !headBranchExist || (!pull.Issue.IsClosed && !pull.IsChecking() && (headBranchSha != sha)) {
		ctx.Data["IsPullRequestBroken"] = true
		if pull.IsSameRepo() {
			ctx.Data["HeadTarget"] = pull.HeadBranch
		} else if pull.HeadRepo == nil {
			ctx.Data["HeadTarget"] = ctx.Locale.Tr("repo.pull.deleted_branch", pull.HeadBranch)
		} else {
			ctx.Data["HeadTarget"] = pull.HeadRepo.OwnerName + ":" + pull.HeadBranch
		}
	}

	compareInfo, err := baseGitRepo.GetCompareInfo(git.BranchPrefix+pull.BaseBranch, pull.GetGitRefName(), false, false)
	if err != nil {
		if strings.Contains(err.Error(), "fatal: Not a valid object name") {
			ctx.Data["IsPullRequestBroken"] = true
			ctx.Data["BaseTarget"] = pull.BaseBranch
			ctx.Data["NumCommits"] = 0
			ctx.Data["CommitIDs"] = map[string]bool{}
			ctx.Data["NumFiles"] = 0
			return nil
		}

		ctx.ServerError("GetCompareInfo", err)
		return nil
	}

	if compareInfo.HeadCommitID == compareInfo.MergeBase {
		ctx.Data["IsNothingToCompare"] = true
	}

	PrepareViewPullInfoActions(ctx, pull)
	if ctx.Written() {
		return nil
	}

	if pull.IsWorkInProgress(ctx) {
		ctx.Data["IsPullWorkInProgress"] = true
		ctx.Data["WorkInProgressPrefix"] = pull.GetWorkInProgressPrefix(ctx)
	}

	if pull.IsFilesConflicted() {
		ctx.Data["IsPullFilesConflicted"] = true
		ctx.Data["ConflictedFiles"] = pull.ConflictedFiles
	}

	ctx.Data["NumCommits"] = len(compareInfo.Commits)
	ctx.Data["NumFiles"] = compareInfo.NumFiles

	commitIDs := map[string]bool{}
	for _, commit := range compareInfo.Commits {
		commitIDs[commit.ID.String()] = true
	}
	ctx.Data["CommitIDs"] = commitIDs

	return compareInfo
}

func createRequiredContextMatcher(requiredContext string) func(string) bool {
	if gp, err := glob.Compile(requiredContext); err == nil {
		return func(contextToCheck string) bool {
			return gp.Match(contextToCheck)
		}
	}

	return func(contextToCheck string) bool {
		return requiredContext == contextToCheck
	}
}

type pullCommitList struct {
	Commits             []pull_service.CommitInfo `json:"commits"`
	LastReviewCommitSha string                    `json:"last_review_commit_sha"`
	Locale              map[string]any            `json:"locale"`
}

// GetPullCommits get all commits for given pull request
func GetPullCommits(ctx *context.Context) {
	issue, ok := getPullInfo(ctx)
	if !ok {
		return
	}
	resp := &pullCommitList{}

	commits, lastReviewCommitSha, err := pull_service.GetPullCommits(ctx, issue)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, err)
		return
	}

	// Get the needed locale
	resp.Locale = map[string]any{
		"lang":                                ctx.Locale.Language(),
		"show_all_commits":                    ctx.Tr("repo.pulls.show_all_commits"),
		"stats_num_commits":                   ctx.TrN(len(commits), "repo.activity.git_stats_commit_1", "repo.activity.git_stats_commit_n", len(commits)),
		"show_changes_since_your_last_review": ctx.Tr("repo.pulls.show_changes_since_your_last_review"),
		"select_commit_hold_shift_for_range":  ctx.Tr("repo.pulls.select_commit_hold_shift_for_range"),
	}

	resp.Commits = commits
	resp.LastReviewCommitSha = lastReviewCommitSha

	ctx.JSON(http.StatusOK, resp)
}

// ViewPullCommits show commits for a pull request
func ViewPullCommits(ctx *context.Context) {
	ctx.Data["PageIsPullList"] = true
	ctx.Data["PageIsPullCommits"] = true

	issue, ok := getPullInfo(ctx)
	if !ok {
		return
	}
	pull := issue.PullRequest

	var prInfo *git.CompareInfo
	if pull.HasMerged {
		prInfo = PrepareMergedViewPullInfo(ctx, issue)
	} else {
		prInfo = PrepareViewPullInfo(ctx, issue)
	}

	if ctx.Written() {
		return
	} else if prInfo == nil {
		ctx.NotFound("ViewPullCommits", nil)
		return
	}

	ctx.Data["Username"] = ctx.Repo.Owner.Name
	ctx.Data["Reponame"] = ctx.Repo.Repository.Name

	commits := git_model.ParseCommitsWithStatus(ctx, prInfo.Commits, ctx.Repo.Repository)
	ctx.Data["Commits"] = commits
	ctx.Data["CommitCount"] = len(commits)

	ctx.Data["HasIssuesOrPullsWritePermission"] = ctx.Repo.CanWriteIssuesOrPulls(issue.IsPull)
	ctx.Data["IsIssuePoster"] = ctx.IsSigned && issue.IsPoster(ctx.Doer.ID)

	// For PR commits page
	PrepareBranchList(ctx)
	if ctx.Written() {
		return
	}
	getBranchData(ctx, issue)
	ctx.HTML(http.StatusOK, tplPullCommits)
}

// ViewPullFiles render pull request changed files list page
func viewPullFiles(ctx *context.Context, specifiedStartCommit, specifiedEndCommit string, willShowSpecifiedCommitRange, willShowSpecifiedCommit bool) {
	ctx.Data["PageIsPullList"] = true
	ctx.Data["PageIsPullFiles"] = true

	issue, ok := getPullInfo(ctx)
	if !ok {
		return
	}
	pull := issue.PullRequest

	var (
		startCommitID string
		endCommitID   string
		gitRepo       = ctx.Repo.GitRepo
	)

	var prInfo *git.CompareInfo
	if pull.HasMerged {
		prInfo = PrepareMergedViewPullInfo(ctx, issue)
	} else {
		prInfo = PrepareViewPullInfo(ctx, issue)
	}

	// Validate the given commit sha to show (if any passed)
	if willShowSpecifiedCommit || willShowSpecifiedCommitRange {
		foundStartCommit := len(specifiedStartCommit) == 0
		foundEndCommit := len(specifiedEndCommit) == 0

		if !foundStartCommit || !foundEndCommit {
			for _, commit := range prInfo.Commits {
				if commit.ID.String() == specifiedStartCommit {
					foundStartCommit = true
				}
				if commit.ID.String() == specifiedEndCommit {
					foundEndCommit = true
				}

				if foundStartCommit && foundEndCommit {
					break
				}
			}
		}

		if !foundStartCommit || !foundEndCommit {
			ctx.NotFound("Given SHA1 not found for this PR", nil)
			return
		}
	}

	if ctx.Written() {
		return
	} else if prInfo == nil {
		ctx.NotFound("ViewPullFiles", nil)
		return
	}

	headCommitID, err := gitRepo.GetRefCommitID(pull.GetGitRefName())
	if err != nil {
		ctx.ServerError("GetRefCommitID", err)
		return
	}

	ctx.Data["IsShowingOnlySingleCommit"] = willShowSpecifiedCommit

	if willShowSpecifiedCommit {
		commitID := specifiedEndCommit

		ctx.Data["CommitID"] = commitID

		var prevCommit, curCommit, nextCommit *git.Commit

		// Iterate in reverse to properly map "previous" and "next" buttons
		for _, commit := range slices.Backward(prInfo.Commits) {
			if curCommit != nil {
				nextCommit = commit
				break
			}

			if commit.ID.String() == commitID {
				curCommit = commit
			} else {
				prevCommit = commit
			}
		}

		if curCommit == nil {
			ctx.ServerError("Repo.GitRepo.viewPullFiles", git.ErrNotExist{ID: commitID})
			return
		}

		ctx.Data["Commit"] = curCommit
		if prevCommit != nil {
			ctx.Data["PrevCommitLink"] = path.Join(ctx.Repo.RepoLink, "pulls", strconv.FormatInt(issue.Index, 10), "commits", prevCommit.ID.String())
		}
		if nextCommit != nil {
			ctx.Data["NextCommitLink"] = path.Join(ctx.Repo.RepoLink, "pulls", strconv.FormatInt(issue.Index, 10), "commits", nextCommit.ID.String())
		}

		statuses, _, err := git_model.GetLatestCommitStatus(ctx, ctx.Repo.Repository.ID, commitID, db.ListOptionsAll)
		if err != nil {
			log.Error("GetLatestCommitStatus: %v", err)
		}

		ctx.Data["CommitStatus"] = git_model.CalcCommitStatus(statuses)
		ctx.Data["CommitStatuses"] = statuses

		verification := asymkey_model.ParseCommitWithSignature(ctx, curCommit)
		ctx.Data["Verification"] = verification
		ctx.Data["Author"] = user_model.ValidateCommitWithEmail(ctx, curCommit)

		if err := asymkey_model.CalculateTrustStatus(verification, ctx.Repo.Repository.GetTrustModel(), func(user *user_model.User) (bool, error) {
			return repo_model.IsOwnerMemberCollaborator(ctx, ctx.Repo.Repository, user.ID)
		}, nil); err != nil {
			ctx.ServerError("CalculateTrustStatus", err)
			return
		}

		note, err := git.GetNote(ctx, ctx.Repo.GitRepo, specifiedEndCommit)
		if err == nil {
			ctx.Data["NoteCommit"] = note.Commit
			ctx.Data["NoteAuthor"] = user_model.ValidateCommitWithEmail(ctx, note.Commit)
			ctx.Data["NoteRaw"] = string(charset.ToUTF8WithFallback(note.Message, charset.ConvertOpts{}))
			ctx.Data["NoteRendered"], err = markup.RenderCommitMessage(&markup.RenderContext{
				Links: markup.Links{
					Base:       ctx.Repo.RepoLink,
					BranchPath: path.Join("commit", util.PathEscapeSegments(commitID)),
				},
				Metas:   ctx.Repo.Repository.ComposeMetas(ctx),
				GitRepo: ctx.Repo.GitRepo,
				Ctx:     ctx,
			}, template.HTMLEscapeString(string(charset.ToUTF8WithFallback(note.Message, charset.ConvertOpts{}))))
			if err != nil {
				ctx.ServerError("RenderCommitMessage", err)
				return
			}
		}

		endCommitID = commitID
		if prevCommit != nil {
			startCommitID = prevCommit.ID.String()
		} else {
			startCommitID = prInfo.MergeBase
		}
		ctx.Data["IsShowingAllCommits"] = false
	} else if willShowSpecifiedCommitRange {
		if len(specifiedEndCommit) > 0 {
			endCommitID = specifiedEndCommit
		} else {
			endCommitID = headCommitID
		}
		if len(specifiedStartCommit) > 0 {
			startCommitID = specifiedStartCommit
		} else {
			startCommitID = prInfo.MergeBase
		}

		ctx.Data["IsShowingAllCommits"] = false
	} else {
		endCommitID = headCommitID
		startCommitID = prInfo.MergeBase
		ctx.Data["IsShowingAllCommits"] = true
	}

	ctx.Data["AfterCommitID"] = endCommitID
	ctx.Data["BeforeCommitID"] = startCommitID
	ctx.Data["Username"] = ctx.Repo.Owner.Name
	ctx.Data["Reponame"] = ctx.Repo.Repository.Name

	fileOnly := ctx.FormBool("file-only")
	files := ctx.FormStrings("files")
	ctx.Data["FileSelection"] = files

	maxLines := setting.Git.MaxGitDiffLines

	var diff *gitdiff.Diff

	var diffFileMetadata []*gitdiff.DiffFileMetadata
	if willShowSpecifiedCommit {
		diffFileMetadata, err = gitdiff.GetDiffNameStatus(ctx, gitRepo, "", endCommitID, setting.UI.DiffPagingNum, files...)
	} else {
		diffFileMetadata, err = gitdiff.GetDiffNameStatus(ctx, gitRepo, startCommitID, endCommitID, setting.UI.DiffPagingNum, files...)
	}
	if err != nil {
		ctx.ServerError("GetDiffNameStatus", err)
		return
	}

	pager, pagedFiles := utils.PaginateDiffFiles(ctx, diffFileMetadata)

	if fileOnly && (len(diffFileMetadata) == 2 || len(diffFileMetadata) == 1) {
		maxLines = -1
	}

	diffOptions := &gitdiff.DiffOptions{
		AfterCommitID:      endCommitID,
		MaxLines:           maxLines,
		MaxLineCharacters:  setting.Git.MaxGitDiffLineCharacters,
		WhitespaceBehavior: gitdiff.GetWhitespaceFlag(ctx.Data["WhitespaceBehavior"].(string)),
		FileOnly:           fileOnly,
	}

	if !willShowSpecifiedCommit {
		diffOptions.BeforeCommitID = startCommitID
	}

	// if we're not logged in or only a single commit (or commit range) is shown we
	// have to load only the diff and not get the viewed information
	// as the viewed information is designed to be loaded only on latest PR
	// diff and if you're signed in.
	var methodWithError string
	if !ctx.IsSigned || willShowSpecifiedCommit || willShowSpecifiedCommitRange {
		diff, err = gitdiff.GetDiffFull(ctx, gitRepo, diffOptions, pagedFiles...)
		methodWithError = "GetDiff"
	} else {
		diff, err = gitdiff.SyncAndGetUserSpecificDiff(ctx, ctx.Doer.ID, pull, gitRepo, diffOptions, pagedFiles...)
		methodWithError = "SyncAndGetUserSpecificDiff"
	}
	if err != nil {
		ctx.ServerError(methodWithError, err)
		return
	}

	var reviewState *pull_model.ReviewState
	if ctx.IsSigned && !willShowSpecifiedCommit && !willShowSpecifiedCommitRange {
		reviewState, err = pull_model.GetNewestReviewState(ctx, ctx.Doer.ID, pull.ID)
		if err != nil {
			ctx.ServerError("GetNewestReviewState", err)
			return
		}
		diffFileMetadata = gitdiff.EnrichWithReview(reviewState, diffFileMetadata...)
	}
	diffMetadata, err := gitdiff.GetDiffMetadata(reviewState, pager.Paginater, len(diffFileMetadata))
	if err != nil {
		ctx.ServerError("GetDiffMetadata", err)
		return
	}

	ctx.Data["DiffFileMetadata"] = diffFileMetadata
	ctx.Data["DiffMetadata"] = diffMetadata

	ctx.PageData["prReview"] = map[string]any{
		"numberOfFiles":       diffMetadata.TotalNumberOfFiles,
		"numberOfViewedFiles": diffMetadata.NumberOfViewedFiles,
	}

	if err = diff.LoadComments(ctx, issue, ctx.Doer, ctx.Data["ShowOutdatedComments"].(bool), endCommitID); err != nil {
		ctx.ServerError("LoadComments", err)
		return
	}

	pb, err := git_model.GetFirstMatchProtectedBranchRule(ctx, pull.BaseRepoID, pull.BaseBranch)
	if err != nil {
		ctx.ServerError("LoadProtectedBranch", err)
		return
	}

	if pb != nil {
		glob := pb.GetProtectedFilePatterns()
		if len(glob) != 0 {
			for _, file := range diff.Files {
				file.IsProtected = pb.IsProtectedFile(glob, file.Name)
			}
		}
	}

	ctx.Data["Diff"] = diff
	ctx.Data["DiffNotAvailable"] = len(diff.Files) == 0

	baseCommit, err := ctx.Repo.GitRepo.GetCommit(startCommitID)
	if err != nil {
		ctx.ServerError("GetCommit", err)
		return
	}
	headCommit, err := gitRepo.GetCommit(endCommitID)
	if err != nil {
		ctx.ServerError("GetCommit", err)
		return
	}

	// determine if the user viewing the pull request can edit the head branch
	if ctx.Doer != nil && pull.HeadRepo != nil && !pull.HasMerged {
		editable, err := pull_service.CanEditHeadBranch(ctx, ctx.Doer, pull)
		if err != nil {
			ctx.ServerError("CanEditHeadBranch", err)
			return
		}
		ctx.Data["HeadBranchIsEditable"] = editable
		ctx.Data["SourceRepoLink"] = pull.HeadRepo.Link()
		ctx.Data["HeadBranch"] = pull.HeadBranch
	}

	if ctx.IsSigned && ctx.Doer != nil {
		if ctx.Data["CanMarkConversation"], err = issues_model.CanMarkConversation(ctx, issue, ctx.Doer); err != nil {
			ctx.ServerError("CanMarkConversation", err)
			return
		}
	}

	setCompareContext(ctx, baseCommit, headCommit, ctx.Repo.Owner.Name, ctx.Repo.Repository.Name)

	assigneeUsers, err := repo_model.GetRepoAssignees(ctx, ctx.Repo.Repository)
	if err != nil {
		ctx.ServerError("GetRepoAssignees", err)
		return
	}
	ctx.Data["Assignees"] = MakeSelfOnTop(ctx.Doer, assigneeUsers)

	participants := getIssueParticipants(ctx, issue)
	if ctx.Written() {
		return
	}
	ctx.Data["Participants"] = participants
	ctx.Data["NumParticipants"] = len(participants)

	handleTeamMentions(ctx)
	if ctx.Written() {
		return
	}

	currentReview, err := issues_model.GetCurrentReview(ctx, ctx.Doer, issue)
	if err != nil && !issues_model.IsErrReviewNotExist(err) {
		ctx.ServerError("GetCurrentReview", err)
		return
	}
	numPendingCodeComments := int64(0)
	if currentReview != nil {
		numPendingCodeComments, err = issues_model.CountComments(ctx, &issues_model.FindCommentsOptions{
			Type:     issues_model.CommentTypeCode,
			ReviewID: currentReview.ID,
			IssueID:  issue.ID,
		})
		if err != nil {
			ctx.ServerError("CountComments", err)
			return
		}
	}
	ctx.Data["CurrentReview"] = currentReview
	ctx.Data["PendingCodeCommentNumber"] = numPendingCodeComments

	getBranchData(ctx, issue)
	ctx.Data["IsIssuePoster"] = ctx.IsSigned && issue.IsPoster(ctx.Doer.ID)
	ctx.Data["HasIssuesOrPullsWritePermission"] = ctx.Repo.CanWriteIssuesOrPulls(issue.IsPull)

	ctx.Data["IsAttachmentEnabled"] = setting.Attachment.Enabled
	// For files changed page
	PrepareBranchList(ctx)
	if ctx.Written() {
		return
	}
	upload.AddUploadContext(ctx, "comment")

	ctx.HTML(http.StatusOK, tplPullFiles)
}

func ViewPullFilesForSingleCommit(ctx *context.Context) {
	viewPullFiles(ctx, "", ctx.Params("sha"), true, true)
}

func ViewPullFilesForRange(ctx *context.Context) {
	viewPullFiles(ctx, ctx.Params("shaFrom"), ctx.Params("shaTo"), true, false)
}

func ViewPullFilesStartingFromCommit(ctx *context.Context) {
	viewPullFiles(ctx, "", ctx.Params("sha"), true, false)
}

func ViewPullFilesForAllCommitsOfPr(ctx *context.Context) {
	viewPullFiles(ctx, "", "", false, false)
}

// UpdatePullRequest merge PR's baseBranch into headBranch
func UpdatePullRequest(ctx *context.Context) {
	issue, ok := getPullInfo(ctx)
	if !ok {
		return
	}
	if issue.IsClosed {
		ctx.NotFound("MergePullRequest", nil)
		return
	}
	if issue.PullRequest.HasMerged {
		ctx.NotFound("MergePullRequest", nil)
		return
	}

	rebase := ctx.FormString("style") == "rebase"

	if err := issue.PullRequest.LoadBaseRepo(ctx); err != nil {
		ctx.ServerError("LoadBaseRepo", err)
		return
	}
	if err := issue.PullRequest.LoadHeadRepo(ctx); err != nil {
		ctx.ServerError("LoadHeadRepo", err)
		return
	}

	headRepoPerm, err := access_model.GetUserRepoPermission(ctx, issue.PullRequest.HeadRepo, ctx.Doer)
	if err != nil {
		ctx.ServerError("GetUserRepoPermission head", err)
		return
	}
	baseRepoPerm, err := access_model.GetUserRepoPermission(ctx, issue.PullRequest.BaseRepo, ctx.Doer)
	if err != nil {
		ctx.ServerError("GetUserRepoPermission base", err)
		return
	}

	allowedUpdateByMerge, allowedUpdateByRebase, err := pull_service.IsUserAllowedToUpdate(ctx, issue.PullRequest, ctx.Doer, headRepoPerm, baseRepoPerm)
	if err != nil {
		ctx.ServerError("IsUserAllowedToMerge", err)
		return
	}

	// ToDo: add check if maintainers are allowed to change branch ... (need migration & co)
	if (!allowedUpdateByMerge && !rebase) || (rebase && !allowedUpdateByRebase) {
		ctx.Flash.Error(ctx.Tr("repo.pulls.update_not_allowed"))
		ctx.Redirect(issue.Link())
		return
	}

	// default merge commit message
	message := fmt.Sprintf("Merge branch '%s' into %s", issue.PullRequest.BaseBranch, issue.PullRequest.HeadBranch)

	if err = pull_service.Update(ctx, issue.PullRequest, ctx.Doer, message, rebase); err != nil {
		if models.IsErrMergeConflicts(err) {
			conflictError := err.(models.ErrMergeConflicts)
			flashError, err := ctx.RenderToHTML(tplAlertDetails, map[string]any{
				"Message": ctx.Tr("repo.pulls.merge_conflict"),
				"Summary": ctx.Tr("repo.pulls.merge_conflict_summary"),
				"Details": utils.SanitizeFlashErrorString(conflictError.StdErr) + "<br>" + utils.SanitizeFlashErrorString(conflictError.StdOut),
			})
			if err != nil {
				ctx.ServerError("UpdatePullRequest.HTMLString", err)
				return
			}
			ctx.Flash.Error(flashError)
			ctx.Redirect(issue.Link())
			return
		} else if models.IsErrRebaseConflicts(err) {
			conflictError := err.(models.ErrRebaseConflicts)
			flashError, err := ctx.RenderToHTML(tplAlertDetails, map[string]any{
				"Message": ctx.Tr("repo.pulls.rebase_conflict", utils.SanitizeFlashErrorString(conflictError.CommitSHA)),
				"Summary": ctx.Tr("repo.pulls.rebase_conflict_summary"),
				"Details": utils.SanitizeFlashErrorString(conflictError.StdErr) + "<br>" + utils.SanitizeFlashErrorString(conflictError.StdOut),
			})
			if err != nil {
				ctx.ServerError("UpdatePullRequest.HTMLString", err)
				return
			}
			ctx.Flash.Error(flashError)
			ctx.Redirect(issue.Link())
			return
		}
		ctx.Flash.Error(err.Error())
		ctx.Redirect(issue.Link())
		return
	}

	ctx.Flash.Success(ctx.Tr("repo.pulls.update_branch_success"))
	ctx.Redirect(issue.Link())
}

// MergePullRequest response for merging pull request
func MergePullRequest(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.MergePullRequestForm)
	issue, ok := getPullInfo(ctx)
	if !ok {
		return
	}

	pr := issue.PullRequest
	pr.Issue = issue
	pr.Issue.Repo = ctx.Repo.Repository

	mergeStyle := repo_model.MergeStyle(form.Do)
	manuallyMerged := mergeStyle == repo_model.MergeStyleManuallyMerged

	mergeCheckType := pull_service.MergeCheckTypeGeneral
	if form.MergeWhenChecksSucceed {
		mergeCheckType = pull_service.MergeCheckTypeAuto
	}
	if manuallyMerged {
		mergeCheckType = pull_service.MergeCheckTypeManually
	}

	// start with merging by checking
	if err := pull_service.CheckPullMergeable(ctx, ctx.Doer, &ctx.Repo.Permission, pr, mergeCheckType, form.ForceMerge, mergeStyle); err != nil {
		switch {
		case errors.Is(err, pull_service.ErrIsClosed):
			if issue.IsPull {
				ctx.JSONError(ctx.Tr("repo.pulls.is_closed"))
			} else {
				ctx.JSONError(ctx.Tr("repo.issues.closed_title"))
			}
		case errors.Is(err, pull_service.ErrUserNotAllowedToMerge):
			ctx.JSONError(ctx.Tr("repo.pulls.update_not_allowed"))
		case errors.Is(err, pull_service.ErrHasMerged):
			ctx.JSONError(ctx.Tr("repo.pulls.has_merged"))
		case errors.Is(err, pull_service.ErrIsWorkInProgress):
			ctx.JSONError(ctx.Tr("repo.pulls.no_merge_wip"))
		case errors.Is(err, pull_service.ErrNotMergeableState):
			ctx.JSONError(ctx.Tr("repo.pulls.no_merge_not_ready"))
		case models.IsErrDisallowedToMerge(err):
			ctx.JSONError(ctx.Tr("repo.pulls.no_merge_not_ready"))
		case asymkey_service.IsErrWontSign(err):
			ctx.JSONError(err.Error()) // has no translation ...
		case errors.Is(err, pull_service.ErrDependenciesLeft):
			ctx.JSONError(ctx.Tr("repo.issues.dependency.pr_close_blocked"))
		default:
			ctx.ServerError("WebCheck", err)
		}

		return
	}

	// handle manually-merged mark
	if manuallyMerged {
		if err := pull_service.MergedManually(ctx, pr, ctx.Doer, ctx.Repo.GitRepo, form.MergeCommitID); err != nil {
			switch {
			case models.IsErrInvalidMergeStyle(err):
				ctx.JSONError(ctx.Tr("repo.pulls.invalid_merge_option"))
			case strings.Contains(err.Error(), "Wrong commit ID"):
				ctx.JSONError(ctx.Tr("repo.pulls.wrong_commit_id"))
			default:
				ctx.ServerError("MergedManually", err)
			}

			return
		}

		ctx.JSONRedirect(issue.Link())
		return
	}

	message := strings.TrimSpace(form.MergeTitleField)
	if len(message) == 0 {
		var err error
		message, _, err = pull_service.GetDefaultMergeMessage(ctx, ctx.Repo.GitRepo, pr, repo_model.MergeStyle(form.Do))
		if err != nil {
			ctx.ServerError("GetDefaultMergeMessage", err)
			return
		}
	}

	form.MergeMessageField = strings.TrimSpace(form.MergeMessageField)
	if len(form.MergeMessageField) > 0 {
		message += "\n\n" + form.MergeMessageField
	}

	if form.MergeWhenChecksSucceed {
		// delete all scheduled auto merges
		_ = pull_model.DeleteScheduledAutoMerge(ctx, pr.ID)
		// schedule auto merge
		scheduled, err := automerge.ScheduleAutoMerge(ctx, ctx.Doer, pr, repo_model.MergeStyle(form.Do), message, form.DeleteBranchAfterMerge)
		if err != nil {
			ctx.ServerError("ScheduleAutoMerge", err)
			return
		} else if scheduled {
			// nothing more to do ...
			ctx.Flash.Success(ctx.Tr("repo.pulls.auto_merge_newly_scheduled"))
			ctx.JSONRedirect(fmt.Sprintf("%s/pulls/%d", ctx.Repo.RepoLink, pr.Index))
			return
		}
	}

	// If the HTTP request is cancelled by the user agent, don't stop work. We've started a merge and need to finish all
	// the related work. All usage of `ctx` throughout the rest of this function should be only for error handling or UI
	// interactions, and all effective work should use `workCtx` instead.
	workCtx, cancelWorkCtx := stdCtx.WithTimeout(
		stdCtx.WithoutCancel(ctx),
		time.Duration(setting.Git.Timeout.Default)*time.Second,
	)
	defer cancelWorkCtx()

	if err := pull_service.Merge(workCtx, pr, ctx.Doer, ctx.Repo.GitRepo, repo_model.MergeStyle(form.Do), form.HeadCommitID, message, false); err != nil {
		if models.IsErrInvalidMergeStyle(err) {
			ctx.JSONError(ctx.Tr("repo.pulls.invalid_merge_option"))
		} else if models.IsErrMergeConflicts(err) {
			conflictError := err.(models.ErrMergeConflicts)
			flashError, err := ctx.RenderToHTML(tplAlertDetails, map[string]any{
				"Message": ctx.Tr("repo.pulls.merge_conflict"),
				"Summary": ctx.Tr("repo.pulls.merge_conflict_summary"),
				"Details": utils.SanitizeFlashErrorString(conflictError.StdErr) + "<br>" + utils.SanitizeFlashErrorString(conflictError.StdOut),
			})
			if err != nil {
				ctx.ServerError("MergePullRequest.HTMLString", err)
				return
			}
			ctx.Flash.Error(flashError)
			ctx.JSONRedirect(issue.Link())
		} else if models.IsErrRebaseConflicts(err) {
			conflictError := err.(models.ErrRebaseConflicts)
			flashError, err := ctx.RenderToHTML(tplAlertDetails, map[string]any{
				"Message": ctx.Tr("repo.pulls.rebase_conflict", utils.SanitizeFlashErrorString(conflictError.CommitSHA)),
				"Summary": ctx.Tr("repo.pulls.rebase_conflict_summary"),
				"Details": utils.SanitizeFlashErrorString(conflictError.StdErr) + "<br>" + utils.SanitizeFlashErrorString(conflictError.StdOut),
			})
			if err != nil {
				ctx.ServerError("MergePullRequest.HTMLString", err)
				return
			}
			ctx.Flash.Error(flashError)
			ctx.JSONRedirect(issue.Link())
		} else if models.IsErrMergeUnrelatedHistories(err) {
			log.Debug("MergeUnrelatedHistories error: %v", err)
			ctx.Flash.Error(ctx.Tr("repo.pulls.unrelated_histories"))
			ctx.JSONRedirect(issue.Link())
		} else if models.IsErrPullRequestHasMerged(err) {
			log.Debug("MergePullRequestHasMerged error: %v", err)
			ctx.Flash.Error(ctx.Tr("repo.pulls.already_merged"))
			ctx.JSONRedirect(issue.Link())
		} else if git.IsErrPushOutOfDate(err) {
			log.Debug("MergePushOutOfDate error: %v", err)
			ctx.Flash.Error(ctx.Tr("repo.pulls.merge_out_of_date"))
			ctx.JSONRedirect(issue.Link())
		} else if models.IsErrSHADoesNotMatch(err) {
			log.Debug("MergeHeadOutOfDate error: %v", err)
			ctx.Flash.Error(ctx.Tr("repo.pulls.head_out_of_date"))
			ctx.JSONRedirect(issue.Link())
		} else if git.IsErrPushRejected(err) {
			log.Debug("MergePushRejected error: %v", err)
			pushrejErr := err.(*git.ErrPushRejected)
			message := pushrejErr.Message
			if len(message) == 0 {
				ctx.Flash.Error(ctx.Tr("repo.pulls.push_rejected_no_message"))
			} else {
				flashError, err := ctx.RenderToHTML(tplAlertDetails, map[string]any{
					"Message": ctx.Tr("repo.pulls.push_rejected"),
					"Summary": ctx.Tr("repo.pulls.push_rejected_summary"),
					"Details": utils.SanitizeFlashErrorString(pushrejErr.Message),
				})
				if err != nil {
					ctx.ServerError("MergePullRequest.HTMLString", err)
					return
				}
				ctx.Flash.Error(flashError)
			}
			ctx.JSONRedirect(issue.Link())
		} else {
			ctx.ServerError("Merge", err)
		}
		return
	}
	log.Trace("Pull request merged: %d", pr.ID)

	if err := stopTimerIfAvailable(workCtx, ctx.Doer, issue); err != nil {
		ctx.ServerError("stopTimerIfAvailable", err)
		return
	}

	log.Trace("Pull request merged: %d", pr.ID)

	if form.DeleteBranchAfterMerge {
		var headRepo *git.Repository
		if ctx.Repo != nil && ctx.Repo.Repository != nil && pr.HeadRepoID == ctx.Repo.Repository.ID && ctx.Repo.GitRepo != nil {
			headRepo = ctx.Repo.GitRepo
		} else {
			var err error
			headRepo, err = gitrepo.OpenRepository(workCtx, pr.HeadRepo)
			if err != nil {
				ctx.ServerError(fmt.Sprintf("OpenRepository[%s]", pr.HeadRepo.FullName()), err)
				return
			}
			defer headRepo.Close()
		}

		if err := repo_service.DeleteBranchAfterMerge(workCtx, ctx.Doer, pr, headRepo); err != nil {
			switch {
			case errors.Is(err, repo_service.ErrBranchIsDefault):
				ctx.Flash.Error(ctx.Tr("repo.pulls.delete_after_merge.head_branch.is_default"))
			case errors.Is(err, git_model.ErrBranchIsProtected):
				ctx.Flash.Error(ctx.Tr("repo.pulls.delete_after_merge.head_branch.is_protected"))
			case errors.Is(err, util.ErrPermissionDenied):
				ctx.Flash.Error(ctx.Tr("repo.pulls.delete_after_merge.head_branch.insufficient_branch"))
			default:
				ctx.ServerError("DeleteBranchAfterMerge", err)
				return
			}

			ctx.JSONRedirect(issue.Link())
			return
		}
	}

	ctx.JSONRedirect(issue.Link())
}

// CancelAutoMergePullRequest cancels a scheduled pr
func CancelAutoMergePullRequest(ctx *context.Context) {
	issue, ok := getPullInfo(ctx)
	if !ok {
		return
	}

	if err := automerge.RemoveScheduledAutoMerge(ctx, ctx.Doer, issue.PullRequest, ctx.Repo.Permission); err != nil {
		switch {
		case errors.Is(err, util.ErrPermissionDenied):
			ctx.Flash.Error(ctx.Tr("repo.pulls.auto_merge.no_permission"))
			ctx.Redirect(issue.HTMLURL())
		case db.IsErrNotExist(err):
			ctx.Flash.Error(ctx.Tr("repo.pulls.auto_merge_not_scheduled"))
			ctx.Redirect(issue.HTMLURL())
		default:
			ctx.ServerError("RemoveScheduledAutoMerge", err)
		}
		return
	}

	ctx.Flash.Success(ctx.Tr("repo.pulls.auto_merge_canceled_schedule"))
	ctx.Redirect(issue.HTMLURL())
}

func stopTimerIfAvailable(ctx stdCtx.Context, user *user_model.User, issue *issues_model.Issue) error {
	if issues_model.StopwatchExists(ctx, user.ID, issue.ID) {
		if err := issues_model.CreateOrStopIssueStopwatch(ctx, user, issue); err != nil {
			return err
		}
	}

	return nil
}

// CompareAndPullRequestPost response for creating pull request
func CompareAndPullRequestPost(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.CreateIssueForm)
	ctx.Data["Title"] = ctx.Tr("repo.pulls.compare_changes")
	ctx.Data["PageIsComparePull"] = true
	ctx.Data["IsDiffCompare"] = true
	ctx.Data["PullRequestWorkInProgressPrefixes"] = setting.Repository.PullRequest.WorkInProgressPrefixes
	ctx.Data["IsAttachmentEnabled"] = setting.Attachment.Enabled
	upload.AddUploadContext(ctx, "comment")
	ctx.Data["HasIssuesOrPullsWritePermission"] = ctx.Repo.CanWrite(unit.TypePullRequests)

	var (
		repo        = ctx.Repo.Repository
		attachments []string
	)

	ci := ParseCompareInfo(ctx)
	if ci != nil {
		defer ci.Close()
	}
	if ctx.Written() {
		return
	}

	labelIDs, assigneeIDs, milestoneID, projectID := ValidateRepoMetas(ctx, *form, true)
	if ctx.Written() {
		return
	}

	if setting.Attachment.Enabled {
		attachments = form.Files
	}

	if ctx.HasError() {
		ctx.JSONError(ctx.GetErrMsg())
		return
	}

	if util.IsEmptyString(form.Title) {
		ctx.JSONError(ctx.Tr("repo.issues.new.title_empty"))
		return
	}

	content := form.Content
	if filename := ctx.Req.Form.Get("template-file"); filename != "" {
		if template, err := issue_template.UnmarshalFromRepo(ctx.Repo.GitRepo, ctx.Repo.Repository.DefaultBranch, filename); err == nil {
			content = issue_template.RenderToMarkdown(template, ctx.Req.Form)
		}
	}

	pullIssue := &issues_model.Issue{
		RepoID:      repo.ID,
		Repo:        repo,
		Title:       form.Title,
		PosterID:    ctx.Doer.ID,
		Poster:      ctx.Doer,
		MilestoneID: milestoneID,
		IsPull:      true,
		Content:     content,
	}
	pullRequest := &issues_model.PullRequest{
		HeadRepoID:          ci.HeadRepo.ID,
		BaseRepoID:          repo.ID,
		HeadBranch:          ci.HeadBranch,
		BaseBranch:          ci.BaseBranch,
		HeadRepo:            ci.HeadRepo,
		BaseRepo:            repo,
		Type:                issues_model.PullRequestGitea,
		AllowMaintainerEdit: form.AllowMaintainerEdit,
	}
	// FIXME: check error in the case two people send pull request at almost same time, give nice error prompt
	// instead of 500.

	if err := pull_service.NewPullRequest(ctx, repo, pullIssue, labelIDs, attachments, pullRequest, assigneeIDs); err != nil {
		switch {
		case errors.Is(err, user_model.ErrBlockedByUser):
			ctx.JSONError(ctx.Tr("repo.pulls.blocked_by_user"))
		case repo_model.IsErrUserDoesNotHaveAccessToRepo(err):
			ctx.Error(http.StatusBadRequest, "UserDoesNotHaveAccessToRepo", err.Error())
		case git.IsErrPushRejected(err):
			pushrejErr := err.(*git.ErrPushRejected)
			message := pushrejErr.Message
			if len(message) == 0 {
				ctx.JSONError(ctx.Tr("repo.pulls.push_rejected_no_message"))
				return
			}
			flashError, err := ctx.RenderToHTML(tplAlertDetails, map[string]any{
				"Message": ctx.Tr("repo.pulls.push_rejected"),
				"Summary": ctx.Tr("repo.pulls.push_rejected_summary"),
				"Details": utils.SanitizeFlashErrorString(pushrejErr.Message),
			})
			if err != nil {
				ctx.ServerError("CompareAndPullRequest.HTMLString", err)
				return
			}
			ctx.JSONError(flashError)
		default:
			// It's an unexpected error.
			// If it happens, we should add another case to handle it.
			log.Error("Unexpected error of NewPullRequest: %T %s", err, err)
			ctx.ServerError("CompareAndPullRequest", err)
		}
		return
	}

	if projectID > 0 {
		updateIssueProject(ctx, pullIssue, projectID)
		if ctx.Written() {
			return
		}
	}

	log.Trace("Pull request created: %d/%d", repo.ID, pullIssue.ID)
	ctx.JSONRedirect(pullIssue.Link())
}

// CleanUpPullRequest responses for delete merged branch when PR has been merged
func CleanUpPullRequest(ctx *context.Context) {
	issue, ok := getPullInfo(ctx)
	if !ok {
		return
	}

	pr := issue.PullRequest

	// Don't cleanup unmerged and unclosed PRs
	if !pr.HasMerged && !issue.IsClosed {
		ctx.NotFound("CleanUpPullRequest", nil)
		return
	}

	// Don't cleanup when there are other PR's that use this branch as head branch.
	exist, err := issues_model.HasUnmergedPullRequestsByHeadInfo(ctx, pr.HeadRepoID, pr.HeadBranch)
	if err != nil {
		ctx.ServerError("HasUnmergedPullRequestsByHeadInfo", err)
		return
	}
	if exist {
		ctx.NotFound("CleanUpPullRequest", nil)
		return
	}

	if err := pr.LoadHeadRepo(ctx); err != nil {
		ctx.ServerError("LoadHeadRepo", err)
		return
	} else if pr.HeadRepo == nil {
		// Forked repository has already been deleted
		ctx.NotFound("CleanUpPullRequest", nil)
		return
	} else if err = pr.LoadBaseRepo(ctx); err != nil {
		ctx.ServerError("LoadBaseRepo", err)
		return
	} else if err = pr.HeadRepo.LoadOwner(ctx); err != nil {
		ctx.ServerError("HeadRepo.LoadOwner", err)
		return
	}

	perm, err := access_model.GetUserRepoPermission(ctx, pr.HeadRepo, ctx.Doer)
	if err != nil {
		ctx.ServerError("GetUserRepoPermission", err)
		return
	}
	if !perm.CanWrite(unit.TypeCode) {
		ctx.NotFound("CleanUpPullRequest", nil)
		return
	}

	fullBranchName := pr.HeadRepo.Owner.Name + "/" + pr.HeadBranch

	var gitBaseRepo *git.Repository

	// Assume that the base repo is the current context (almost certainly)
	if ctx.Repo != nil && ctx.Repo.Repository != nil && ctx.Repo.Repository.ID == pr.BaseRepoID && ctx.Repo.GitRepo != nil {
		gitBaseRepo = ctx.Repo.GitRepo
	} else {
		// If not just open it
		gitBaseRepo, err = gitrepo.OpenRepository(ctx, pr.BaseRepo)
		if err != nil {
			ctx.ServerError(fmt.Sprintf("OpenRepository[%s]", pr.BaseRepo.FullName()), err)
			return
		}
		defer gitBaseRepo.Close()
	}

	// Now assume that the head repo is the same as the base repo (reasonable chance)
	gitRepo := gitBaseRepo
	// But if not: is it the same as the context?
	if pr.BaseRepoID != pr.HeadRepoID && ctx.Repo != nil && ctx.Repo.Repository != nil && ctx.Repo.Repository.ID == pr.HeadRepoID && ctx.Repo.GitRepo != nil {
		gitRepo = ctx.Repo.GitRepo
	} else if pr.BaseRepoID != pr.HeadRepoID {
		// Otherwise just load it up
		gitRepo, err = gitrepo.OpenRepository(ctx, pr.HeadRepo)
		if err != nil {
			ctx.ServerError(fmt.Sprintf("OpenRepository[%s]", pr.HeadRepo.FullName()), err)
			return
		}
		defer gitRepo.Close()
	}

	defer func() {
		ctx.JSONRedirect(issue.Link())
	}()

	// Check if branch has no new commits
	headCommitID, err := gitBaseRepo.GetRefCommitID(pr.GetGitRefName())
	if err != nil {
		log.Error("GetRefCommitID: %v", err)
		ctx.Flash.Error(ctx.Tr("repo.branch.deletion_failed", fullBranchName))
		return
	}
	branchCommitID, err := gitRepo.GetBranchCommitID(pr.HeadBranch)
	if err != nil {
		log.Error("GetBranchCommitID: %v", err)
		ctx.Flash.Error(ctx.Tr("repo.branch.deletion_failed", fullBranchName))
		return
	}
	if headCommitID != branchCommitID {
		ctx.Flash.Error(ctx.Tr("repo.branch.delete_branch_has_new_commits", fullBranchName))
		return
	}

	deleteBranch(ctx, pr, gitRepo)
}

func deleteBranch(ctx *context.Context, pr *issues_model.PullRequest, gitRepo *git.Repository) {
	fullBranchName := pr.HeadRepo.FullName() + ":" + pr.HeadBranch

	if err := pull_service.RetargetChildrenOnMerge(ctx, ctx.Doer, pr); err != nil {
		ctx.Flash.Error(ctx.Tr("repo.branch.deletion_failed", fullBranchName))
		return
	}

	if err := repo_service.DeleteBranch(ctx, ctx.Doer, pr.HeadRepo, gitRepo, pr.HeadBranch); err != nil {
		switch {
		case git.IsErrBranchNotExist(err):
			ctx.Flash.Error(ctx.Tr("repo.branch.deletion_failed", fullBranchName))
		case errors.Is(err, repo_service.ErrBranchIsDefault):
			ctx.Flash.Error(ctx.Tr("repo.branch.deletion_failed", fullBranchName))
		case errors.Is(err, git_model.ErrBranchIsProtected):
			ctx.Flash.Error(ctx.Tr("repo.branch.deletion_failed", fullBranchName))
		default:
			log.Error("DeleteBranch: %v", err)
			ctx.Flash.Error(ctx.Tr("repo.branch.deletion_failed", fullBranchName))
		}
		return
	}

	if err := issues_model.AddDeletePRBranchComment(ctx, ctx.Doer, pr.BaseRepo, pr.IssueID, pr.HeadBranch); err != nil {
		// Do not fail here as branch has already been deleted
		log.Error("DeleteBranch: %v", err)
	}

	ctx.Flash.Success(ctx.Tr("repo.branch.deletion_success", fullBranchName))
}

// DownloadPullDiff render a pull's raw diff
func DownloadPullDiff(ctx *context.Context) {
	DownloadPullDiffOrPatch(ctx, false)
}

// DownloadPullPatch render a pull's raw patch
func DownloadPullPatch(ctx *context.Context) {
	DownloadPullDiffOrPatch(ctx, true)
}

// DownloadPullDiffOrPatch render a pull's raw diff or patch
func DownloadPullDiffOrPatch(ctx *context.Context, patch bool) {
	pr, err := issues_model.GetPullRequestByIndex(ctx, ctx.Repo.Repository.ID, ctx.ParamsInt64(":index"))
	if err != nil {
		if issues_model.IsErrPullRequestNotExist(err) {
			ctx.NotFound("GetPullRequestByIndex", err)
		} else {
			ctx.ServerError("GetPullRequestByIndex", err)
		}
		return
	}

	binary := ctx.FormBool("binary")

	if err := pull_service.DownloadDiffOrPatch(ctx, pr, ctx, patch, binary); err != nil {
		ctx.ServerError("DownloadDiffOrPatch", err)
		return
	}
}

// UpdatePullRequestTarget change pull request's target branch
func UpdatePullRequestTarget(ctx *context.Context) {
	issue := GetActionIssue(ctx)
	if ctx.Written() {
		return
	}
	pr := issue.PullRequest
	if !issue.IsPull {
		ctx.Error(http.StatusNotFound)
		return
	}

	if !ctx.IsSigned || (!issue.IsPoster(ctx.Doer.ID) && !ctx.Repo.CanWriteIssuesOrPulls(issue.IsPull)) {
		ctx.Error(http.StatusForbidden)
		return
	}

	targetBranch := ctx.FormTrim("target_branch")
	if len(targetBranch) == 0 {
		ctx.Error(http.StatusNoContent)
		return
	}

	if err := pull_service.ChangeTargetBranch(ctx, pr, ctx.Doer, targetBranch); err != nil {
		if issues_model.IsErrPullRequestAlreadyExists(err) {
			err := err.(issues_model.ErrPullRequestAlreadyExists)

			RepoRelPath := ctx.Repo.Owner.Name + "/" + ctx.Repo.Repository.Name
			errorMessage := ctx.Tr("repo.pulls.has_pull_request", html.EscapeString(ctx.Repo.RepoLink+"/pulls/"+strconv.FormatInt(err.IssueID, 10)), html.EscapeString(RepoRelPath), err.IssueID) // FIXME: Creates url inside locale string

			ctx.Flash.Error(errorMessage)
			ctx.JSON(http.StatusConflict, map[string]any{
				"error":      err.Error(),
				"user_error": errorMessage,
			})
		} else if issues_model.IsErrIssueIsClosed(err) {
			errorMessage := ctx.Tr("repo.pulls.is_closed")

			ctx.Flash.Error(errorMessage)
			ctx.JSON(http.StatusConflict, map[string]any{
				"error":      err.Error(),
				"user_error": errorMessage,
			})
		} else if models.IsErrPullRequestHasMerged(err) {
			errorMessage := ctx.Tr("repo.pulls.has_merged")

			ctx.Flash.Error(errorMessage)
			ctx.JSON(http.StatusConflict, map[string]any{
				"error":      err.Error(),
				"user_error": errorMessage,
			})
		} else if git_model.IsErrBranchesEqual(err) {
			errorMessage := ctx.Tr("repo.pulls.nothing_to_compare")

			ctx.Flash.Error(errorMessage)
			ctx.JSON(http.StatusBadRequest, map[string]any{
				"error":      err.Error(),
				"user_error": errorMessage,
			})
		} else {
			ctx.ServerError("UpdatePullRequestTarget", err)
		}
		return
	}
	notify_service.PullRequestChangeTargetBranch(ctx, ctx.Doer, pr, targetBranch)

	ctx.JSON(http.StatusOK, map[string]any{
		"base_branch": pr.BaseBranch,
	})
}

// SetAllowEdits allow edits from maintainers to PRs
func SetAllowEdits(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.UpdateAllowEditsForm)

	pr, err := issues_model.GetPullRequestByIndex(ctx, ctx.Repo.Repository.ID, ctx.ParamsInt64(":index"))
	if err != nil {
		if issues_model.IsErrPullRequestNotExist(err) {
			ctx.NotFound("GetPullRequestByIndex", err)
		} else {
			ctx.ServerError("GetPullRequestByIndex", err)
		}
		return
	}

	if err := pull_service.SetAllowEdits(ctx, ctx.Doer, pr, form.AllowMaintainerEdit); err != nil {
		if errors.Is(err, pull_service.ErrUserHasNoPermissionForAction) {
			ctx.Error(http.StatusForbidden)
			return
		}
		ctx.ServerError("SetAllowEdits", err)
		return
	}

	ctx.JSON(http.StatusOK, map[string]any{
		"allow_maintainer_edit": pr.AllowMaintainerEdit,
	})
}

func PrepareViewPullInfoActions(ctx *context.Context, pull *issues_model.PullRequest) {
	canReadUnitActions := ctx.Repo.CanRead(unit.TypeActions)
	ctx.Data["CanReadUnitActions"] = canReadUnitActions

	if !canReadUnitActions {
		return
	}

	PrepareViewPullInfoActionsTrust(ctx, pull)
	if ctx.Written() {
		return
	}
}

func PrepareViewPullInfoActionsTrust(ctx *context.Context, pull *issues_model.PullRequest) {
	trusted, err := actions_service.GetPullRequestPosterIsTrustedWithActions(ctx, pull)
	if err != nil {
		ctx.ServerError("GetPullRequestUserIsTrustedWithActions", err)
		return
	}
	ctx.Data["PullRequestPosterIsNotTrustedWithActions"] = trusted == actions_service.UserIsNotTrustedWithActions
	ctx.Data["PullRequestPosterIsExplicitlyTrustedWithActions"] = trusted == actions_service.UserIsExplicitlyTrustedWithActions
	ctx.Data["PullRequestPosterIsImplicitlyTrustedWithActions"] = trusted == actions_service.UserIsImplicitlyTrustedWithActions

	someRunsNeedApproval, err := actions_model.HasRunThatNeedApproval(ctx, pull.Issue.RepoID, pull.ID)
	if err != nil {
		ctx.ServerError("HasRunThatNeedApproval", err)
		return
	}
	ctx.Data["SomePullRequestRunsNeedApproval"] = someRunsNeedApproval

	ctx.Data["UserCanDelegateTrustWithPullRequest"] = context.CheckRepoDelegateActionTrust(ctx)
}

func UpdateTrustWithPullRequestActions(ctx *context.Context) {
	pr, err := issues_model.GetPullRequestByIndex(ctx, ctx.Repo.Repository.ID, ctx.ParamsInt64(":index"))
	if err != nil {
		if issues_model.IsErrPullRequestNotExist(err) {
			ctx.NotFound("GetPullRequestByIndex", err)
		} else {
			ctx.ServerError("GetPullRequestByIndex", err)
		}
		return
	}

	trust := ctx.FormString("trust")

	if err := actions_service.UpdateTrustedWithPullRequest(ctx, ctx.Doer.ID, pr, actions_service.TrustUpdate(trust)); err != nil {
		ctx.Error(http.StatusInternalServerError, err.Error())
		return
	}

	if err := pr.LoadIssue(ctx); err != nil {
		ctx.Error(http.StatusInternalServerError, err.Error())
		return
	}

	if err := pr.Issue.LoadRepo(ctx); err != nil {
		ctx.Error(http.StatusInternalServerError, err.Error())
		return
	}

	ctx.Redirect(fmt.Sprintf("%s#pull-request-trust-panel", pr.Issue.Link()))
}

func SetCommitNotesPullRequest(ctx *context.Context) {
	setCommitNotes(ctx, fmt.Sprintf("%s/pulls/%s/commits/%s", ctx.Repo.Repository.Link(), ctx.Params(":index"), ctx.Params(":sha")))
}

func RemoveCommitNotesPullRequest(ctx *context.Context) {
	removeCommitNotes(ctx, fmt.Sprintf("%s/pulls/%s/commits/%s", ctx.Repo.Repository.Link(), ctx.Params(":index"), ctx.Params(":sha")))
}
