// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pull

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"forgejo.org/models"
	"forgejo.org/models/db"
	git_model "forgejo.org/models/git"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/base"
	"forgejo.org/modules/container"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/graceful"
	"forgejo.org/modules/json"
	"forgejo.org/modules/log"
	"forgejo.org/modules/process"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/sync"
	app_context "forgejo.org/services/context"
	issue_service "forgejo.org/services/issue"
	notify_service "forgejo.org/services/notify"
)

// TODO: use clustered lock (unique queue? or *abuse* cache)
var pullWorkingPool = sync.MutexMap{}

// NewPullRequest creates new pull request with labels for repository.
func NewPullRequest(ctx context.Context, repo *repo_model.Repository, issue *issues_model.Issue, labelIDs []int64, uuids []string, pr *issues_model.PullRequest, assigneeIDs []int64) error {
	// Check if the doer is not blocked by the repository's owner.
	if user_model.IsBlocked(ctx, repo.OwnerID, issue.PosterID) {
		return user_model.ErrBlockedByUser
	}

	testPatchCtx, err := testPatch(ctx, pr)
	defer testPatchCtx.close()
	if err != nil {
		return fmt.Errorf("testPatch: %w", err)
	}

	divergence, err := git.GetDivergingCommits(ctx, testPatchCtx.gitRepo.Path, testPatchCtx.baseRev, testPatchCtx.headRev, testPatchCtx.env)
	if err != nil {
		return fmt.Errorf("GetDivergingCommits: %w", err)
	}
	pr.CommitsAhead = divergence.Ahead
	pr.CommitsBehind = divergence.Behind

	assigneeCommentMap := make(map[int64]*issues_model.Comment)

	// add first push codes comment
	baseGitRepo, err := gitrepo.OpenRepository(ctx, pr.BaseRepo)
	if err != nil {
		return err
	}
	defer baseGitRepo.Close()

	var reviewNotifiers []*issue_service.ReviewRequestNotifier
	if err := db.WithTx(ctx, func(ctx context.Context) error {
		if err := issues_model.NewPullRequest(ctx, repo, issue, labelIDs, uuids, pr); err != nil {
			return err
		}

		for _, assigneeID := range assigneeIDs {
			comment, err := issue_service.AddAssigneeIfNotAssigned(ctx, issue, issue.Poster, assigneeID, false)
			if err != nil {
				return err
			}
			assigneeCommentMap[assigneeID] = comment
		}

		pr.Issue = issue
		issue.PullRequest = pr

		if pr.Flow == issues_model.PullRequestFlowGithub {
			err = PushToBaseRepo(ctx, pr)
		} else {
			err = UpdateRef(ctx, pr)
		}
		if err != nil {
			return err
		}

		compareInfo, err := baseGitRepo.GetCompareInfo(git.BranchPrefix+pr.BaseBranch, pr.GetGitRefName(), false, false)
		if err != nil {
			return err
		}
		if len(compareInfo.Commits) == 0 {
			return nil
		}

		data := issues_model.PushActionContent{IsForcePush: false}
		data.CommitIDs = make([]string, 0, len(compareInfo.Commits))
		for _, c := range slices.Backward(compareInfo.Commits) {
			data.CommitIDs = append(data.CommitIDs, c.ID.String())
		}

		dataJSON, err := json.Marshal(data)
		if err != nil {
			return err
		}

		ops := &issues_model.CreateCommentOptions{
			Type:        issues_model.CommentTypePullRequestPush,
			Doer:        issue.Poster,
			Repo:        repo,
			Issue:       pr.Issue,
			IsForcePush: false,
			Content:     string(dataJSON),
		}

		if _, err = issues_model.CreateComment(ctx, ops); err != nil {
			return err
		}

		if !pr.IsWorkInProgress(ctx) {
			reviewNotifiers, err = issue_service.PullRequestCodeOwnersReview(ctx, issue, pr)
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		// cleanup: this will only remove the reference, the real commit will be clean up when next GC
		if err1 := baseGitRepo.RemoveReference(pr.GetGitRefName()); err1 != nil {
			log.Error("RemoveReference: %v", err1)
		}
		return err
	}
	baseGitRepo.Close() // close immediately to avoid notifications will open the repository again

	issue_service.ReviewRequestNotify(ctx, issue, issue.Poster, reviewNotifiers)

	mentions, err := issues_model.FindAndUpdateIssueMentions(ctx, issue, issue.Poster, issue.Content)
	if err != nil {
		return err
	}
	notify_service.NewPullRequest(ctx, pr, mentions)
	if len(issue.Labels) > 0 {
		notify_service.IssueChangeLabels(ctx, issue.Poster, issue, issue.Labels, nil)
	}
	if issue.Milestone != nil {
		notify_service.IssueChangeMilestone(ctx, issue.Poster, issue, 0)
	}
	for _, assigneeID := range assigneeIDs {
		assignee, err := user_model.GetUserByID(ctx, assigneeID)
		if err != nil {
			return ErrDependenciesLeft
		}
		notify_service.IssueChangeAssignee(ctx, issue.Poster, issue, assignee, false, assigneeCommentMap[assigneeID])
	}

	return nil
}

// ChangeTargetBranch changes the target branch of this pull request, as the given user.
func ChangeTargetBranch(ctx context.Context, pr *issues_model.PullRequest, doer *user_model.User, targetBranch string) (err error) {
	defer pullWorkingPool.Lock(fmt.Sprint(pr.ID))()

	// Current target branch is already the same
	if pr.BaseBranch == targetBranch {
		return nil
	}

	if pr.Issue.IsClosed {
		return issues_model.ErrIssueIsClosed{
			ID:     pr.Issue.ID,
			RepoID: pr.Issue.RepoID,
			Index:  pr.Issue.Index,
		}
	}

	if pr.HasMerged {
		return models.ErrPullRequestHasMerged{
			ID:         pr.ID,
			IssueID:    pr.Index,
			HeadRepoID: pr.HeadRepoID,
			BaseRepoID: pr.BaseRepoID,
			HeadBranch: pr.HeadBranch,
			BaseBranch: pr.BaseBranch,
		}
	}

	// Check if branches are equal
	branchesEqual, err := IsHeadEqualWithBranch(ctx, pr, targetBranch)
	if err != nil {
		return err
	}
	if branchesEqual {
		return git_model.ErrBranchesEqual{
			HeadBranchName: pr.HeadBranch,
			BaseBranchName: targetBranch,
		}
	}

	// Check if pull request for the new target branch already exists
	existingPr, err := issues_model.GetUnmergedPullRequest(ctx, pr.HeadRepoID, pr.BaseRepoID, pr.HeadBranch, targetBranch, issues_model.PullRequestFlowGithub)
	if existingPr != nil {
		return issues_model.ErrPullRequestAlreadyExists{
			ID:         existingPr.ID,
			IssueID:    existingPr.Index,
			HeadRepoID: existingPr.HeadRepoID,
			BaseRepoID: existingPr.BaseRepoID,
			HeadBranch: existingPr.HeadBranch,
			BaseBranch: existingPr.BaseBranch,
		}
	}
	if err != nil && !issues_model.IsErrPullRequestNotExist(err) {
		return err
	}

	// Set new target branch
	oldBranch := pr.BaseBranch
	pr.BaseBranch = targetBranch

	// Refresh patch
	if err := TestPatch(pr); err != nil {
		return err
	}

	// Update target branch, PR diff and status
	// This is the same as checkAndUpdateStatus in check service, but also updates base_branch
	if pr.Status == issues_model.PullRequestStatusChecking {
		pr.Status = issues_model.PullRequestStatusMergeable
	}

	// Update Commit Divergence
	divergence, err := GetDiverging(ctx, pr)
	if err != nil {
		return err
	}
	pr.CommitsAhead = divergence.Ahead
	pr.CommitsBehind = divergence.Behind

	if err := pr.UpdateColsIfNotMerged(ctx, "merge_base", "status", "conflicted_files", "changed_protected_files", "base_branch", "commits_ahead", "commits_behind"); err != nil {
		return err
	}

	// Create comment
	options := &issues_model.CreateCommentOptions{
		Type:   issues_model.CommentTypeChangeTargetBranch,
		Doer:   doer,
		Repo:   pr.Issue.Repo,
		Issue:  pr.Issue,
		OldRef: oldBranch,
		NewRef: targetBranch,
	}
	if _, err = issues_model.CreateComment(ctx, options); err != nil {
		return fmt.Errorf("CreateChangeTargetBranchComment: %w", err)
	}

	return nil
}

func checkForInvalidation(ctx context.Context, requests issues_model.PullRequestList, repoID int64, doer *user_model.User, newCommitID string) error {
	repo, err := repo_model.GetRepositoryByID(ctx, repoID)
	if err != nil {
		return fmt.Errorf("GetRepositoryByIDCtx: %w", err)
	}
	go func() {
		// FIXME: graceful: We need to tell the manager we're doing something...
		err := InvalidateCodeComments(ctx, requests, doer, repo, newCommitID)
		if err != nil {
			log.Error("PullRequestList.InvalidateCodeComments: %v", err)
		}
	}()
	return nil
}

// AddTestPullRequestTask adds new test tasks by given head/base repository and head/base branch,
// and generate new patch for testing as needed.
func AddTestPullRequestTask(ctx context.Context, doer *user_model.User, repoID int64, branch string, isSync bool, oldCommitID, newCommitID string, timeNano int64) {
	description := fmt.Sprintf("AddTestPullRequestTask [head_repo_id: %d, head_branch: %s]: only pull requests created before nano time %d will be considered", repoID, branch, timeNano)
	log.Trace(description)
	go graceful.GetManager().RunWithShutdownContext(func(shutdownCtx context.Context) {
		// make it a process to allow for cancellation (especially during integration tests where no global shutdown happens)
		ctx, _, finished := process.GetManager().AddContext(shutdownCtx, description)
		defer finished()
		// There is no sensible way to shut this down ":-("
		// If you don't let it run all the way then you will lose data
		// TODO: graceful: TestPullRequest needs to become a queue!

		TestPullRequest(ctx, doer, repoID, timeNano, branch, isSync, oldCommitID, newCommitID)
	})
}

func TestPullRequest(ctx context.Context, doer *user_model.User, repoID, olderThan int64, branch string, isSync bool, oldCommitID, newCommitID string) {
	// Only consider PR that are older than olderThan, which is the time at
	// which the newCommitID was added to repoID.
	//
	// * commit C is pushed
	// * the git hook queues AddTestPullRequestTask for processing and returns with success
	// * TestPullRequest is not called yet
	// * a pull request P with commit C as the head is created
	// * TestPullRequest runs and ignores P because it was created after the commit was received
	//
	// In other words, a PR must not be updated based on events that happened before it existed
	prs, err := issues_model.GetUnmergedPullRequestsByHeadInfoMax(ctx, repoID, olderThan, branch)
	if err != nil {
		log.Error("Find pull requests [head_repo_id: %d, head_branch: %s]: %v", repoID, branch, err)
		return
	}

	for _, pr := range prs {
		log.Trace("Updating PR[id=%d,index=%d]: composing new test task", pr.ID, pr.Index)
		if pr.Flow == issues_model.PullRequestFlowGithub {
			if err := PushToBaseRepo(ctx, pr); err != nil {
				log.Error("PushToBaseRepo: %v", err)
				continue
			}
		} else {
			continue
		}

		AddToTaskQueue(ctx, pr)
		comment, err := CreatePushPullComment(ctx, doer, pr, oldCommitID, newCommitID)
		if err == nil && comment != nil {
			notify_service.PullRequestPushCommits(ctx, doer, pr, comment)
		}
	}

	if isSync {
		requests := issues_model.PullRequestList(prs)
		if err = requests.LoadAttributes(ctx); err != nil {
			log.Error("PullRequestList.LoadAttributes: %v", err)
		}
		if invalidationErr := checkForInvalidation(ctx, requests, repoID, doer, newCommitID); invalidationErr != nil {
			log.Error("checkForInvalidation: %v", invalidationErr)
		}
		if err == nil {
			for _, pr := range prs {
				ValidatePullRequest(ctx, pr, newCommitID, oldCommitID, doer)

				pr.HeadCommitID = newCommitID
				notify_service.PullRequestSynchronized(ctx, doer, pr)
			}
		}
	}

	log.Trace("TestPullRequest [base_repo_id: %d, base_branch: %s]: finding pull requests", repoID, branch)
	prs, err = issues_model.GetUnmergedPullRequestsByBaseInfo(ctx, repoID, branch)
	if err != nil {
		log.Error("Find pull requests [base_repo_id: %d, base_branch: %s]: %v", repoID, branch, err)
		return
	}
	for _, pr := range prs {
		divergence, err := GetDiverging(ctx, pr)
		if err != nil {
			if git_model.IsErrBranchNotExist(err) && !git.IsBranchExist(ctx, pr.HeadRepo.RepoPath(), pr.HeadBranch) {
				log.Warn("Cannot test PR %s/%d: head_branch %s no longer exists", pr.BaseRepo.Name, pr.IssueID, pr.HeadBranch)
			} else {
				log.Error("GetDiverging: %v", err)
			}
		} else {
			err = pr.UpdateCommitDivergence(ctx, divergence.Ahead, divergence.Behind)
			if err != nil {
				log.Error("UpdateCommitDivergence: %v", err)
			}
		}
		AddToTaskQueue(ctx, pr)
	}
}

// Mark old reviews as stale if diff to mergebase has changed.
// Dismiss all approval reviews if protected branch rule item enabled.
// Update commit divergence.
func ValidatePullRequest(ctx context.Context, pr *issues_model.PullRequest, newCommitID, oldCommitID string, doer *user_model.User) {
	objectFormat := git.ObjectFormatFromName(pr.BaseRepo.ObjectFormatName)
	if newCommitID == "" || newCommitID == objectFormat.EmptyObjectID().String() {
		return
	}

	testPatchCtx, err := getTestPatchCtx(ctx, pr, true)
	defer testPatchCtx.close()
	if err != nil {
		log.Error("testPatchCtx: %v", err)
		return
	}

	changed, err := testPatchCtx.gitRepo.CheckIfDiffDiffers(testPatchCtx.baseRev, oldCommitID, newCommitID, testPatchCtx.env)
	if err != nil {
		log.Error("CheckIfDiffDiffers: %v", err)
	}
	if changed {
		if err := issues_model.MarkReviewsAsStale(ctx, pr.IssueID); err != nil {
			log.Error("MarkReviewsAsStale: %v", err)
		}

		pb, err := git_model.GetFirstMatchProtectedBranchRule(ctx, pr.BaseRepoID, pr.BaseBranch)
		if err != nil {
			log.Error("GetFirstMatchProtectedBranchRule: %v", err)
		}
		if pb != nil && pb.DismissStaleApprovals {
			if err := DismissApprovalReviews(ctx, doer, pr); err != nil {
				log.Error("DismissApprovalReviews: %v", err)
			}
		}
	}
	if err := issues_model.MarkReviewsAsNotStale(ctx, pr.IssueID, newCommitID); err != nil {
		log.Error("MarkReviewsAsNotStale: %v", err)
	}

	divergence, err := git.GetDivergingCommits(ctx, testPatchCtx.gitRepo.Path, testPatchCtx.baseRev, testPatchCtx.headRev, testPatchCtx.env)
	if err != nil {
		log.Error("GetDivergingCommits: %v", err)
	} else {
		err = pr.UpdateCommitDivergence(ctx, divergence.Ahead, divergence.Behind)
		if err != nil {
			log.Error("UpdateCommitDivergence: %v", err)
		}
	}
}

// PushToBaseRepo pushes commits from branches of head repository to
// corresponding branches of base repository.
// FIXME: Only push branches that are actually updates?
func PushToBaseRepo(ctx context.Context, pr *issues_model.PullRequest) (err error) {
	log.Trace("PushToBaseRepo[%d]: pushing commits to base repo '%s'", pr.BaseRepoID, pr.GetGitRefName())

	if err := pr.LoadHeadRepo(ctx); err != nil {
		log.Error("Unable to load head repository for PR[%d] Error: %v", pr.ID, err)
		return err
	}
	headRepoPath := pr.HeadRepo.RepoPath()

	if err := pr.LoadBaseRepo(ctx); err != nil {
		log.Error("Unable to load base repository for PR[%d] Error: %v", pr.ID, err)
		return err
	}
	baseRepoPath := pr.BaseRepo.RepoPath()

	baseRepo, err := git.OpenRepository(ctx, baseRepoPath)
	if err != nil {
		return err
	}
	defer baseRepo.Close()

	fetchedCommitID, err := baseRepo.Fetch("file://"+headRepoPath, git.BranchPrefix+pr.HeadBranch)
	if err != nil {
		return err
	}

	return baseRepo.SetReference(pr.GetGitRefName(), fetchedCommitID)
}

// UpdateRef update refs/pull/id/head directly for agit flow pull request
func UpdateRef(ctx context.Context, pr *issues_model.PullRequest) (err error) {
	log.Trace("UpdateRef[%d]: upgate pull request ref in base repo '%s'", pr.ID, pr.GetGitRefName())
	if err := pr.LoadBaseRepo(ctx); err != nil {
		log.Error("Unable to load base repository for PR[%d] Error: %v", pr.ID, err)
		return err
	}

	_, _, err = git.NewCommand(ctx, "update-ref").AddDynamicArguments(pr.GetGitRefName(), pr.HeadCommitID).RunStdString(&git.RunOpts{Dir: pr.BaseRepo.RepoPath()})
	if err != nil {
		log.Error("Unable to update ref in base repository for PR[%d] Error: %v", pr.ID, err)
	}

	return err
}

type errlist []error

func (errs errlist) Error() string {
	if len(errs) > 0 {
		var buf strings.Builder
		for i, err := range errs {
			if i > 0 {
				buf.WriteString(", ")
			}
			buf.WriteString(err.Error())
		}
		return buf.String()
	}
	return ""
}

// RetargetChildrenOnMerge retarget children pull requests on merge if possible
func RetargetChildrenOnMerge(ctx context.Context, doer *user_model.User, pr *issues_model.PullRequest) error {
	if setting.Repository.PullRequest.RetargetChildrenOnMerge && pr.BaseRepoID == pr.HeadRepoID {
		return RetargetBranchPulls(ctx, doer, pr.HeadRepoID, pr.HeadBranch, pr.BaseBranch)
	}
	return nil
}

// RetargetBranchPulls change target branch for all pull requests whose base branch is the branch
// Both branch and targetBranch must be in the same repo (for security reasons)
func RetargetBranchPulls(ctx context.Context, doer *user_model.User, repoID int64, branch, targetBranch string) error {
	prs, err := issues_model.GetUnmergedPullRequestsByBaseInfo(ctx, repoID, branch)
	if err != nil {
		return err
	}

	if err := issues_model.PullRequestList(prs).LoadAttributes(ctx); err != nil {
		return err
	}

	var errs errlist
	for _, pr := range prs {
		if err = pr.Issue.LoadRepo(ctx); err != nil {
			errs = append(errs, err)
		} else if err = ChangeTargetBranch(ctx, pr, doer, targetBranch); err != nil &&
			!issues_model.IsErrIssueIsClosed(err) && !models.IsErrPullRequestHasMerged(err) &&
			!issues_model.IsErrPullRequestAlreadyExists(err) {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

// CloseBranchPulls close all the pull requests who's head branch is the branch
func CloseBranchPulls(ctx context.Context, doer *user_model.User, repoID int64, branch string) error {
	prs, err := issues_model.GetUnmergedPullRequestsByHeadInfo(ctx, repoID, branch)
	if err != nil {
		return err
	}

	prs2, err := issues_model.GetUnmergedPullRequestsByBaseInfo(ctx, repoID, branch)
	if err != nil {
		return err
	}

	prs = append(prs, prs2...)
	if err := prs.LoadAttributes(ctx); err != nil {
		return err
	}

	var errs errlist
	for _, pr := range prs {
		if err = issue_service.ChangeStatus(ctx, pr.Issue, doer, &issues_model.PRNotificationInfo{MergedCommitID: ""}, true); err != nil && !issues_model.IsErrPullWasClosed(err) && !issues_model.IsErrDependenciesLeft(err) {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

// CloseRepoBranchesPulls close all pull requests which head branches are in the given repository, but only whose base repo is not in the given repository
func CloseRepoBranchesPulls(ctx context.Context, doer *user_model.User, repo *repo_model.Repository) error {
	branches, _, err := gitrepo.GetBranchesByPath(ctx, repo, 0, 0)
	if err != nil {
		return err
	}

	var errs errlist
	for _, branch := range branches {
		prs, err := issues_model.GetUnmergedPullRequestsByHeadInfo(ctx, repo.ID, branch.Name)
		if err != nil {
			return err
		}

		if err = prs.LoadAttributes(ctx); err != nil {
			return err
		}

		for _, pr := range prs {
			// If the base repository for this pr is this repository there is no need to close it
			// as it is going to be deleted anyway
			if pr.BaseRepoID == repo.ID {
				continue
			}
			if err = issue_service.ChangeStatus(ctx, pr.Issue, doer, &issues_model.PRNotificationInfo{MergedCommitID: ""}, true); err != nil && !issues_model.IsErrPullWasClosed(err) {
				errs = append(errs, err)
			}
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

var commitMessageTrailersPattern = regexp.MustCompile(`(?:^|\n\n)(?:[\w-]+[ \t]*:[^\n]+\n*(?:[ \t]+[^\n]+\n*)*)+$`)

// GetSquashMergeCommitMessages returns the commit messages between head and merge base (if there is one)
func GetSquashMergeCommitMessages(ctx context.Context, pr *issues_model.PullRequest) string {
	if err := pr.LoadIssue(ctx); err != nil {
		log.Error("Cannot load issue %d for PR id %d: Error: %v", pr.IssueID, pr.ID, err)
		return ""
	}

	if err := pr.Issue.LoadPoster(ctx); err != nil {
		log.Error("Cannot load poster %d for pr id %d, index %d Error: %v", pr.Issue.PosterID, pr.ID, pr.Index, err)
		return ""
	}

	if pr.HeadRepo == nil {
		var err error
		pr.HeadRepo, err = repo_model.GetRepositoryByID(ctx, pr.HeadRepoID)
		if err != nil {
			log.Error("GetRepositoryByIdCtx[%d]: %v", pr.HeadRepoID, err)
			return ""
		}
	}

	gitRepo, closer, err := gitrepo.RepositoryFromContextOrOpen(ctx, pr.HeadRepo)
	if err != nil {
		log.Error("Unable to open head repository: Error: %v", err)
		return ""
	}
	defer closer.Close()

	var headCommit *git.Commit
	if pr.Flow == issues_model.PullRequestFlowGithub {
		headCommit, err = gitRepo.GetBranchCommit(pr.HeadBranch)
	} else {
		pr.HeadCommitID, err = gitRepo.GetRefCommitID(pr.GetGitRefName())
		if err != nil {
			log.Error("Unable to get head commit: %s Error: %v", pr.GetGitRefName(), err)
			return ""
		}
		headCommit, err = gitRepo.GetCommit(pr.HeadCommitID)
	}
	if err != nil {
		log.Error("Unable to get head commit: %s Error: %v", pr.HeadBranch, err)
		return ""
	}

	mergeBase, err := gitRepo.GetCommit(pr.MergeBase)
	if err != nil {
		log.Error("Unable to get merge base commit: %s Error: %v", pr.MergeBase, err)
		return ""
	}

	limit := setting.Repository.PullRequest.DefaultMergeMessageCommitsLimit

	commits, err := gitRepo.CommitsBetweenLimit(headCommit, mergeBase, limit, 0)
	if err != nil {
		log.Error("Unable to get commits between: %s %s Error: %v", pr.HeadBranch, pr.MergeBase, err)
		return ""
	}

	posterSig := pr.Issue.Poster.NewGitSig().String()

	uniqueAuthors := make(container.Set[string])
	authors := make([]string, 0, len(commits))
	stringBuilder := strings.Builder{}

	if !setting.Repository.PullRequest.PopulateSquashCommentWithCommitMessages {
		message := strings.TrimSpace(pr.Issue.Content)
		stringBuilder.WriteString(message)
		if stringBuilder.Len() > 0 {
			stringBuilder.WriteRune('\n')
			if !commitMessageTrailersPattern.MatchString(message) {
				stringBuilder.WriteRune('\n')
			}
		}
	}

	// commits list is in reverse chronological order
	first := true
	for _, commit := range slices.Backward(commits) {
		if setting.Repository.PullRequest.PopulateSquashCommentWithCommitMessages {
			maxSize := setting.Repository.PullRequest.DefaultMergeMessageSize
			if maxSize < 0 || stringBuilder.Len() < maxSize {
				var toWrite []byte
				if first {
					first = false
					toWrite = []byte(strings.TrimPrefix(commit.CommitMessage, pr.Issue.Title))
				} else {
					toWrite = []byte(commit.CommitMessage)
				}

				if len(toWrite) > maxSize-stringBuilder.Len() && maxSize > -1 {
					toWrite = append(toWrite[:maxSize-stringBuilder.Len()], "..."...)
				}
				if _, err := stringBuilder.Write(toWrite); err != nil {
					log.Error("Unable to write commit message Error: %v", err)
					return ""
				}

				if _, err := stringBuilder.WriteRune('\n'); err != nil {
					log.Error("Unable to write commit message Error: %v", err)
					return ""
				}
			}
		}

		authorString := commit.Author.String()
		if uniqueAuthors.Add(authorString) && authorString != posterSig {
			// Compare use account as well to avoid adding the same author multiple times
			// times when email addresses are private or multiple emails are used.
			commitUser, _ := user_model.GetUserByEmail(ctx, commit.Author.Email)
			if commitUser == nil || commitUser.ID != pr.Issue.Poster.ID {
				authors = append(authors, authorString)
			}
		}
	}

	// Consider collecting the remaining authors
	if limit >= 0 && setting.Repository.PullRequest.DefaultMergeMessageAllAuthors {
		skip := limit
		limit = 30
		for {
			commits, err := gitRepo.CommitsBetweenLimit(headCommit, mergeBase, limit, skip)
			if err != nil {
				log.Error("Unable to get commits between: %s %s Error: %v", pr.HeadBranch, pr.MergeBase, err)
				return ""
			}
			if len(commits) == 0 {
				break
			}
			for _, commit := range commits {
				authorString := commit.Author.String()
				if uniqueAuthors.Add(authorString) && authorString != posterSig {
					commitUser, _ := user_model.GetUserByEmail(ctx, commit.Author.Email)
					if commitUser == nil || commitUser.ID != pr.Issue.Poster.ID {
						authors = append(authors, authorString)
					}
				}
			}
			skip += limit
		}
	}

	for _, author := range authors {
		if _, err := stringBuilder.WriteString("Co-authored-by: "); err != nil {
			log.Error("Unable to write to string builder Error: %v", err)
			return ""
		}
		if _, err := stringBuilder.WriteString(author); err != nil {
			log.Error("Unable to write to string builder Error: %v", err)
			return ""
		}
		if _, err := stringBuilder.WriteRune('\n'); err != nil {
			log.Error("Unable to write to string builder Error: %v", err)
			return ""
		}
	}

	return stringBuilder.String()
}

// GetIssuesLastCommitStatus returns a map of issue ID to the most recent commit's latest status
func GetIssuesLastCommitStatus(ctx context.Context, issues issues_model.IssueList) (map[int64]*git_model.CommitStatus, error) {
	_, lastStatus, err := GetIssuesAllCommitStatus(ctx, issues)
	return lastStatus, err
}

// GetIssuesAllCommitStatus returns a map of issue ID to a list of all statuses for the most recent commit as well as a map of issue ID to only the commit's latest status
func GetIssuesAllCommitStatus(ctx context.Context, issues issues_model.IssueList) (map[int64][]*git_model.CommitStatus, map[int64]*git_model.CommitStatus, error) {
	if err := issues.LoadPullRequests(ctx); err != nil {
		return nil, nil, err
	}
	if _, err := issues.LoadRepositories(ctx); err != nil {
		return nil, nil, err
	}

	var (
		gitRepos = make(map[int64]*git.Repository)
		res      = make(map[int64][]*git_model.CommitStatus)
		lastRes  = make(map[int64]*git_model.CommitStatus)
		err      error
	)
	defer func() {
		for _, gitRepo := range gitRepos {
			gitRepo.Close()
		}
	}()

	for _, issue := range issues {
		if !issue.IsPull {
			continue
		}
		gitRepo, ok := gitRepos[issue.RepoID]
		if !ok {
			gitRepo, err = gitrepo.OpenRepository(ctx, issue.Repo)
			if err != nil {
				log.Error("Cannot open git repository %-v for issue #%d[%d]. Error: %v", issue.Repo, issue.Index, issue.ID, err)
				continue
			}
			gitRepos[issue.RepoID] = gitRepo
		}

		statuses, lastStatus, err := getAllCommitStatus(ctx, gitRepo, issue.PullRequest)
		if err != nil {
			log.Error("getAllCommitStatus: can't get commit statuses of pull [%d]: %v", issue.PullRequest.ID, err)
			continue
		}
		res[issue.PullRequest.ID] = statuses
		lastRes[issue.PullRequest.ID] = lastStatus
	}
	return res, lastRes, nil
}

// getAllCommitStatus get pr's commit statuses.
func getAllCommitStatus(ctx context.Context, gitRepo *git.Repository, pr *issues_model.PullRequest) (statuses []*git_model.CommitStatus, lastStatus *git_model.CommitStatus, err error) {
	sha, shaErr := gitRepo.GetRefCommitID(pr.GetGitRefName())
	if shaErr != nil {
		return nil, nil, shaErr
	}

	statuses, _, err = git_model.GetLatestCommitStatus(ctx, pr.BaseRepo.ID, sha, db.ListOptionsAll)
	lastStatus = git_model.CalcCommitStatus(statuses)
	return statuses, lastStatus, err
}

// IsHeadEqualWithBranch returns if the commits of branchName are available in pull request head
func IsHeadEqualWithBranch(ctx context.Context, pr *issues_model.PullRequest, branchName string) (bool, error) {
	var err error
	if err = pr.LoadBaseRepo(ctx); err != nil {
		return false, err
	}
	baseGitRepo, closer, err := gitrepo.RepositoryFromContextOrOpen(ctx, pr.BaseRepo)
	if err != nil {
		return false, err
	}
	defer closer.Close()

	baseCommit, err := baseGitRepo.GetBranchCommit(branchName)
	if err != nil {
		return false, err
	}

	if err = pr.LoadHeadRepo(ctx); err != nil {
		return false, err
	}
	var headGitRepo *git.Repository
	if pr.HeadRepoID == pr.BaseRepoID {
		headGitRepo = baseGitRepo
	} else {
		var closer io.Closer

		headGitRepo, closer, err = gitrepo.RepositoryFromContextOrOpen(ctx, pr.HeadRepo)
		if err != nil {
			return false, err
		}
		defer closer.Close()
	}

	var headCommit *git.Commit
	if pr.Flow == issues_model.PullRequestFlowGithub {
		headCommit, err = headGitRepo.GetBranchCommit(pr.HeadBranch)
		if err != nil {
			return false, err
		}
	} else {
		pr.HeadCommitID, err = baseGitRepo.GetRefCommitID(pr.GetGitRefName())
		if err != nil {
			return false, err
		}
		if headCommit, err = baseGitRepo.GetCommit(pr.HeadCommitID); err != nil {
			return false, err
		}
	}
	return baseCommit.HasPreviousCommit(headCommit.ID)
}

type CommitInfo struct {
	Summary               string `json:"summary"`
	CommitterOrAuthorName string `json:"committer_or_author_name"`
	ID                    string `json:"id"`
	ShortSha              string `json:"short_sha"`
	Time                  string `json:"time"`
}

// GetPullCommits returns all commits on given pull request and the last review commit sha
// Attention: The last review commit sha must be from the latest review whose commit id is not empty.
// So the type of the latest review cannot be "ReviewTypeRequest".
func GetPullCommits(ctx *app_context.Context, issue *issues_model.Issue) ([]CommitInfo, string, error) {
	pull := issue.PullRequest

	baseGitRepo := ctx.Repo.GitRepo

	if err := pull.LoadBaseRepo(ctx); err != nil {
		return nil, "", err
	}
	baseBranch := pull.BaseBranch
	if pull.HasMerged {
		baseBranch = pull.MergeBase
	}
	prInfo, err := baseGitRepo.GetCompareInfo(baseBranch, pull.GetGitRefName(), true, false)
	if err != nil {
		return nil, "", err
	}

	commits := make([]CommitInfo, 0, len(prInfo.Commits))

	for _, commit := range prInfo.Commits {
		var committerOrAuthorName string
		var commitTime time.Time
		if commit.Author != nil {
			committerOrAuthorName = commit.Author.Name
			commitTime = commit.Author.When
		} else {
			committerOrAuthorName = commit.Committer.Name
			commitTime = commit.Committer.When
		}

		commits = append(commits, CommitInfo{
			Summary:               commit.Summary(),
			CommitterOrAuthorName: committerOrAuthorName,
			ID:                    commit.ID.String(),
			ShortSha:              base.ShortSha(commit.ID.String()),
			Time:                  commitTime.Format(time.RFC3339),
		})
	}

	var lastReviewCommitID string
	if ctx.IsSigned {
		// get last review of current user and store information in context (if available)
		lastreview, err := issues_model.FindLatestReviews(ctx, issues_model.FindReviewOptions{
			IssueID:    issue.ID,
			ReviewerID: ctx.Doer.ID,
			Types: []issues_model.ReviewType{
				issues_model.ReviewTypeApprove,
				issues_model.ReviewTypeComment,
				issues_model.ReviewTypeReject,
			},
		})

		if err != nil && !issues_model.IsErrReviewNotExist(err) {
			return nil, "", err
		}
		if len(lastreview) > 0 {
			lastReviewCommitID = lastreview[0].CommitID
		}
	}

	return commits, lastReviewCommitID, nil
}
