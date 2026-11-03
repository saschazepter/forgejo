// Copyright 2019 The Gitea Authors. All rights reserved.
// Copyright 2018 Jonas Franz. All rights reserved.
// SPDX-License-Identifier: MIT

package migrations

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"forgejo.org/models"
	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/avatar"
	base_module "forgejo.org/modules/base"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/label"
	"forgejo.org/modules/log"
	base "forgejo.org/modules/migration"
	repo_module "forgejo.org/modules/repository"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/storage"
	"forgejo.org/modules/structs"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/uri"
	"forgejo.org/modules/util"
	"forgejo.org/services/migrations/allowlist"
	"forgejo.org/services/pull"
	repo_service "forgejo.org/services/repository"

	"github.com/google/uuid"
)

var _ base.Uploader = &GiteaLocalUploader{}

// GiteaLocalUploader implements an Uploader to gitea sites
type GiteaLocalUploader struct {
	ctx            context.Context
	doer           *user_model.User
	repoOwner      string
	repoName       string
	repo           *repo_model.Repository
	labels         map[string]*issues_model.Label
	milestones     map[string]int64
	issues         map[int64]*issues_model.Issue
	gitRepo        *git.Repository
	prHeadCache    map[string]string
	sameApp        bool
	userMap        map[int64]int64 // external user id mapping to user id
	prCache        map[int64]*issues_model.PullRequest
	commentMap     map[commentKey]migratedComment // foreign comment index to inserted comment, to resolve reply links
	gitServiceType structs.GitServiceType
}

// NewGiteaLocalUploader creates an gitea Uploader via gitea API v1
func NewGiteaLocalUploader(ctx context.Context, doer *user_model.User, repoOwner, repoName string) *GiteaLocalUploader {
	return &GiteaLocalUploader{
		ctx:         ctx,
		doer:        doer,
		repoOwner:   repoOwner,
		repoName:    repoName,
		labels:      make(map[string]*issues_model.Label),
		milestones:  make(map[string]int64),
		issues:      make(map[int64]*issues_model.Issue),
		prHeadCache: make(map[string]string),
		userMap:     make(map[int64]int64),
		prCache:     make(map[int64]*issues_model.PullRequest),
	}
}

// EnableCommentReplyTo allocates commentMap, enabling resolveReplyLinks: replies are only
// resolved when the downloader announces comments carrying Meta["ReplyTo"].
func (g *GiteaLocalUploader) EnableCommentReplyTo() {
	g.commentMap = make(map[commentKey]migratedComment)
}

// commentKey identifies a migrated comment by the issue it belongs to and its index on the
// source forge.
type commentKey struct {
	issueID      int64
	foreignIndex int64
}

// migratedComment retains what a reply needs to quote its parent comment.
type migratedComment struct {
	id             int64
	posterID       int64
	originalAuthor string
	firstLine      string
}

// MaxBatchInsertSize returns the table's max batch insert size
func (g *GiteaLocalUploader) MaxBatchInsertSize(tp string) int {
	switch tp {
	case "issue":
		return db.MaxBatchInsertSize(new(issues_model.Issue))
	case "comment":
		return db.MaxBatchInsertSize(new(issues_model.Comment))
	case "milestone":
		return db.MaxBatchInsertSize(new(issues_model.Milestone))
	case "label":
		return db.MaxBatchInsertSize(new(issues_model.Label))
	case "release":
		return db.MaxBatchInsertSize(new(repo_model.Release))
	case "pullrequest":
		return db.MaxBatchInsertSize(new(issues_model.PullRequest))
	}
	return 10
}

// CreateRepo creates a repository
func (g *GiteaLocalUploader) CreateRepo(repo *base.Repository, opts base.MigrateOptions) error {
	owner, err := user_model.GetUserByName(g.ctx, g.repoOwner)
	if err != nil {
		return err
	}

	var r *repo_model.Repository
	if opts.MigrateToRepoID <= 0 {
		r, err = repo_service.CreateRepositoryDirectly(g.ctx, g.doer, owner, repo_service.CreateRepoOptions{
			Name:           g.repoName,
			Description:    repo.Description,
			Website:        repo.Website,
			OriginalURL:    repo.OriginalURL,
			GitServiceType: opts.GitServiceType,
			IsPrivate:      opts.Private || setting.Repository.ForcePrivate,
			IsMirror:       opts.Mirror,
			Status:         repo_model.RepositoryBeingMigrated,
		})
	} else {
		r, err = repo_model.GetRepositoryByID(g.ctx, opts.MigrateToRepoID)
	}
	if err != nil {
		return err
	}
	r.DefaultBranch = repo.DefaultBranch
	r.Description = repo.Description
	r.Website = repo.Website

	r, err = repo_service.MigrateRepositoryGitData(g.ctx, owner, r, base.MigrateOptions{
		CloneAddr:      repo.CloneURL, // SECURITY: we will assume that this has already been checked
		LFS:            opts.LFS,
		LFSEndpoint:    opts.LFSEndpoint,
		Mirror:         repo.IsMirror,
		MirrorInterval: opts.MirrorInterval,
		Releases:       opts.Releases, // if didn't get releases, then sync them from tags
		RepoName:       g.repoName,
		Wiki:           opts.Wiki,
	}, allowlist.NewMigrationHTTPTransport())

	g.sameApp = strings.HasPrefix(repo.OriginalURL, setting.AppURL)
	g.repo = r
	if err != nil {
		return err
	}
	g.gitRepo, err = gitrepo.OpenRepository(g.ctx, g.repo)
	if err != nil {
		return err
	}

	// attempt to migrate the repository avatar in a best-effort manner
	if repo.AvatarURL != "" {
		if err = g.migrateRepositoryAvatar(repo.AvatarURL); err != nil {
			log.Warn("Migrating repo avatar failed: %v", err)
		}
	}

	// detect object format from git repository and update to database
	objectFormat, err := g.gitRepo.GetObjectFormat()
	if err != nil {
		return err
	}
	g.repo.ObjectFormatName = objectFormat.Name()
	return repo_model.UpdateRepositoryCols(g.ctx, g.repo, "object_format_name")
}

// Close closes this uploader
func (g *GiteaLocalUploader) Close() {
	if g.gitRepo != nil {
		g.gitRepo.Close()
	}
}

// CreateTopics creates topics
func (g *GiteaLocalUploader) CreateTopics(topics ...string) error {
	validTopics, invalidTopics := repo_model.SanitizeAndValidateTopics(topics)
	if len(invalidTopics) > 0 {
		log.Warn("Invalid topics: %v in migration to %s/%s", invalidTopics, g.repoOwner, g.repoName)
	}

	// Truncate if it exceeds the maximum number of topics per repo.
	if len(validTopics) > repo_model.MaxTopicsPerRepo {
		validTopics = validTopics[:repo_model.MaxTopicsPerRepo]
		log.Warn("More than %[0]d topics, truncated to first %[0]d topics in migration to %s/%s", repo_model.MaxTopicsPerRepo, g.repoOwner, g.repoName)
	}

	return repo_model.SaveTopics(g.ctx, g.repo.ID, validTopics...)
}

// CreateMilestones creates milestones
func (g *GiteaLocalUploader) CreateMilestones(milestones ...*base.Milestone) error {
	mss := make([]*issues_model.Milestone, 0, len(milestones))
	for _, milestone := range milestones {
		var deadline timeutil.TimeStamp
		if milestone.Deadline != nil {
			deadline = timeutil.TimeStamp(milestone.Deadline.Unix())
		}
		if deadline == 0 {
			deadline = timeutil.TimeStamp(time.Date(9999, 1, 1, 0, 0, 0, 0, setting.DefaultUILocation).Unix())
		}

		if milestone.Created.IsZero() {
			if milestone.Updated != nil {
				milestone.Created = *milestone.Updated
			} else if milestone.Deadline != nil {
				milestone.Created = *milestone.Deadline
			} else {
				milestone.Created = time.Now()
			}
		}
		if milestone.Updated == nil || milestone.Updated.IsZero() {
			milestone.Updated = &milestone.Created
		}

		ms := issues_model.Milestone{
			RepoID:       g.repo.ID,
			Name:         milestone.Title,
			Content:      milestone.Description,
			IsClosed:     milestone.State == "closed",
			CreatedUnix:  timeutil.TimeStamp(milestone.Created.Unix()),
			UpdatedUnix:  timeutil.TimeStamp(milestone.Updated.Unix()),
			DeadlineUnix: deadline,
		}
		if ms.IsClosed && milestone.Closed != nil {
			ms.ClosedDateUnix = timeutil.TimeStamp(milestone.Closed.Unix())
		}
		mss = append(mss, &ms)
	}

	err := issues_model.InsertMilestones(g.ctx, mss...)
	if err != nil {
		return err
	}

	for _, ms := range mss {
		g.milestones[ms.Name] = ms.ID
	}
	return nil
}

// CreateLabels creates labels
func (g *GiteaLocalUploader) CreateLabels(labels ...*base.Label) error {
	lbs := make([]*issues_model.Label, 0, len(labels))
	for _, l := range labels {
		if color, err := label.NormalizeColor(l.Color); err != nil {
			log.Warn("Invalid label color: #%s for label: %s in migration to %s/%s", l.Color, l.Name, g.repoOwner, g.repoName)
			l.Color = "#ffffff"
		} else {
			l.Color = color
		}

		lbs = append(lbs, &issues_model.Label{
			RepoID:      g.repo.ID,
			Name:        l.Name,
			Exclusive:   l.Exclusive,
			Description: l.Description,
			Color:       l.Color,
		})
	}

	err := issues_model.NewLabels(g.ctx, lbs...)
	if err != nil {
		return err
	}
	for _, lb := range lbs {
		g.labels[lb.Name] = lb
	}
	return nil
}

// CreateReleases creates releases
func (g *GiteaLocalUploader) CreateReleases(releases ...*base.Release) error {
	rels := make([]*repo_model.Release, 0, len(releases))
	for _, release := range releases {
		if release.Created.IsZero() {
			if !release.Published.IsZero() {
				release.Created = release.Published
			} else {
				release.Created = time.Now()
			}
		}

		// SECURITY: The TagName must be a valid git ref
		if release.TagName != "" && !git.IsValidRefPattern(release.TagName) {
			release.TagName = ""
		}

		// SECURITY: The TargetCommitish must be a valid git ref
		if release.TargetCommitish != "" && !git.IsValidRefPattern(release.TargetCommitish) {
			release.TargetCommitish = ""
		}

		rel := repo_model.Release{
			RepoID:       g.repo.ID,
			TagName:      release.TagName,
			LowerTagName: strings.ToLower(release.TagName),
			Target:       release.TargetCommitish,
			Title:        release.Name,
			Note:         release.Body,
			IsDraft:      release.Draft,
			IsPrerelease: release.Prerelease,
			IsTag:        false,
			CreatedUnix:  timeutil.TimeStamp(release.Created.Unix()),
		}

		if err := g.remapUser(release, &rel); err != nil {
			return err
		}

		// calc NumCommits if possible
		if rel.TagName != "" {
			commit, err := g.gitRepo.GetTagCommit(rel.TagName)
			if !git.IsErrNotExist(err) {
				if err != nil {
					return fmt.Errorf("GetTagCommit[%v]: %w", rel.TagName, err)
				}
				rel.Sha1 = commit.ID.String()
				rel.NumCommits, err = commit.CommitsCount()
				if err != nil {
					return fmt.Errorf("CommitsCount: %w", err)
				}
			}
		}

		for _, asset := range release.Assets {
			if asset.Created.IsZero() {
				if !asset.Updated.IsZero() {
					asset.Created = asset.Updated
				} else {
					asset.Created = release.Created
				}
			}
			attach := repo_model.Attachment{
				UUID:          uuid.New().String(),
				Name:          asset.Name,
				DownloadCount: int64(*asset.DownloadCount),
				Size:          int64(*asset.Size),
				CreatedUnix:   timeutil.TimeStamp(asset.Created.Unix()),
			}

			// SECURITY: We cannot check the DownloadURL and DownloadFunc are safe here
			// ... we must assume that they are safe and simply download the attachment
			err := func() error {
				// asset.DownloadURL maybe a local file
				var rc io.ReadCloser
				var err error
				if asset.DownloadFunc != nil {
					rc, err = asset.DownloadFunc()
					if err != nil {
						return err
					}
				} else if asset.DownloadURL != nil {
					rc, err = uri.Open(*asset.DownloadURL)
					if err != nil {
						return err
					}
				}
				if rc == nil {
					return nil
				}
				_, err = storage.Attachments.Save(attach.RelativePath(), rc, int64(*asset.Size))
				rc.Close()
				return err
			}()
			if err != nil {
				return err
			}

			rel.Attachments = append(rel.Attachments, &attach)
		}

		rels = append(rels, &rel)
	}

	return repo_model.InsertReleases(g.ctx, rels...)
}

// SyncTags syncs releases with tags in the database
func (g *GiteaLocalUploader) SyncTags() error {
	return repo_module.SyncReleasesWithTags(g.ctx, g.repo, g.gitRepo)
}

// CreateIssues creates issues
func (g *GiteaLocalUploader) CreateIssues(issues ...*base.Issue) error {
	iss := make([]*issues_model.Issue, 0, len(issues))
	for _, issue := range issues {
		var labels []*issues_model.Label
		for _, label := range issue.Labels {
			lb, ok := g.labels[label.Name]
			if ok {
				labels = append(labels, lb)
			}
		}

		milestoneID := g.milestones[issue.Milestone]

		if issue.Created.IsZero() {
			if issue.Closed != nil {
				issue.Created = *issue.Closed
			} else {
				issue.Created = time.Now()
			}
		}
		if issue.Updated.IsZero() {
			if issue.Closed != nil {
				issue.Updated = *issue.Closed
			} else {
				issue.Updated = time.Now()
			}
		}

		// SECURITY: issue.Ref needs to be a valid reference
		if !git.IsValidRefPattern(issue.Ref) {
			log.Warn("Invalid issue.Ref[%s] in issue #%d in %s/%s", issue.Ref, issue.Number, g.repoOwner, g.repoName)
			issue.Ref = ""
		}

		is := issues_model.Issue{
			RepoID:      g.repo.ID,
			Repo:        g.repo,
			Index:       issue.Number,
			Title:       base_module.TruncateString(issue.Title, 255),
			Content:     issue.Content,
			Ref:         issue.Ref,
			IsClosed:    issue.State == "closed",
			IsLocked:    issue.IsLocked,
			MilestoneID: milestoneID,
			Labels:      labels,
			CreatedUnix: timeutil.TimeStamp(issue.Created.Unix()),
			UpdatedUnix: timeutil.TimeStamp(issue.Updated.Unix()),
		}

		if err := g.remapUser(issue, &is); err != nil {
			return err
		}

		if issue.Closed != nil {
			is.ClosedUnix = timeutil.TimeStamp(issue.Closed.Unix())
		}
		// add reactions
		for _, reaction := range issue.Reactions {
			res := issues_model.Reaction{
				Type:        reaction.Content,
				CreatedUnix: timeutil.TimeStampNow(),
			}
			if err := g.remapUser(reaction, &res); err != nil {
				return err
			}
			is.Reactions = append(is.Reactions, &res)
		}
		iss = append(iss, &is)
	}

	if len(iss) > 0 {
		if err := issues_model.InsertIssues(g.ctx, iss...); err != nil {
			return err
		}

		for _, is := range iss {
			g.issues[is.Index] = is
		}
	}

	return nil
}

// CreateComments creates comments of issues
func (g *GiteaLocalUploader) CreateComments(comments ...*base.Comment) error {
	cms := make([]*issues_model.Comment, 0, len(comments))
	for _, comment := range comments {
		var issue *issues_model.Issue
		issue, ok := g.issues[comment.IssueIndex]
		if !ok {
			return fmt.Errorf("comment[index=%d,poster_id=%d] belongs to a issue[index=%d] that was not migrated", comment.Index, comment.PosterID, comment.IssueIndex)
		}

		if comment.Created.IsZero() {
			comment.Created = time.Unix(int64(issue.CreatedUnix), 0)
		}
		if comment.Updated.IsZero() {
			comment.Updated = comment.Created
		}
		if comment.CommentType == "" {
			// if type field is missing, then assume a normal comment
			comment.CommentType = issues_model.CommentTypeComment.String()
		}
		cm := issues_model.Comment{
			IssueID:     issue.ID,
			Type:        issues_model.AsCommentType(comment.CommentType),
			Content:     comment.Content,
			CreatedUnix: timeutil.TimeStamp(comment.Created.Unix()),
			UpdatedUnix: timeutil.TimeStamp(comment.Updated.Unix()),
		}

		switch cm.Type {
		case issues_model.CommentTypeReopen:
			cm.Content = ""
		case issues_model.CommentTypeClose:
			cm.Content = ""
		case issues_model.CommentTypeAssignees:
			if assigneeID, ok := comment.Meta["AssigneeID"].(int); ok {
				cm.AssigneeID = int64(assigneeID)
			}
			if comment.Meta["RemovedAssigneeID"] != nil {
				cm.RemovedAssignee = true
			}
		case issues_model.CommentTypeChangeTitle:
			if comment.Meta["OldTitle"] != nil {
				cm.OldTitle = fmt.Sprint(comment.Meta["OldTitle"])
			}
			if comment.Meta["NewTitle"] != nil {
				cm.NewTitle = fmt.Sprint(comment.Meta["NewTitle"])
			}
		case issues_model.CommentTypeChangeTargetBranch:
			if comment.Meta["OldRef"] != nil && comment.Meta["NewRef"] != nil {
				cm.OldRef = fmt.Sprint(comment.Meta["OldRef"])
				cm.NewRef = fmt.Sprint(comment.Meta["NewRef"])
				cm.Content = ""
			}
		case issues_model.CommentTypeMergePull:
			cm.Content = ""
		case issues_model.CommentTypePRScheduledToAutoMerge, issues_model.CommentTypePRUnScheduledToAutoMerge:
			cm.Content = ""
		default:
		}

		if err := g.remapUser(comment, &cm); err != nil {
			return err
		}

		// add reactions
		for _, reaction := range comment.Reactions {
			res := issues_model.Reaction{
				Type:        reaction.Content,
				CreatedUnix: timeutil.TimeStampNow(),
			}
			if err := g.remapUser(reaction, &res); err != nil {
				return err
			}
			cm.Reactions = append(cm.Reactions, &res)
		}

		cms = append(cms, &cm)
	}

	if len(cms) == 0 {
		return nil
	}
	if err := issues_model.InsertIssueComments(g.ctx, cms); err != nil {
		return err
	}
	return g.resolveReplyLinks(comments, cms)
}

// resolveReplyLinks prepends to each reply (Meta["ReplyTo"] = parent comment Index) the
// header the quote-reply button produces. Parent ids only exist once inserted, hence the
// content update after the fact; commentMap spans batches to find earlier-inserted parents.
func (g *GiteaLocalUploader) resolveReplyLinks(comments []*base.Comment, cms []*issues_model.Comment) error {
	if g.commentMap == nil {
		return nil
	}
	for i, cm := range cms {
		g.commentMap[commentKey{cm.IssueID, comments[i].Index}] = migratedComment{
			id:             cm.ID,
			posterID:       cm.PosterID,
			originalAuthor: cm.OriginalAuthor,
			firstLine:      strings.TrimSuffix(strings.SplitN(cm.Content, "\n", 2)[0], "\r"),
		}
	}
	for i, cm := range cms {
		// Check ReplyTo
		replyTo, ok := replyToIndex(comments[i].Meta)
		if !ok {
			continue
		}

		// Check parent
		parent, ok := g.commentMap[commentKey{cm.IssueID, replyTo}]
		if !ok {
			continue
		}

		// Resolve author
		author := parent.originalAuthor
		if author == "" {
			switch poster, err := user_model.GetUserByID(g.ctx, parent.posterID); {
			case err == nil:
				author = poster.Name
			case user_model.IsErrUserNotExist(err):
				author = user_model.GhostUserName
			default:
				return err
			}
		}

		// Build comment reply header
		issue := g.issues[comments[i].IssueIndex]
		path := "issues"
		if issue.IsPull {
			path = "pulls"
		}
		cm.Content = fmt.Sprintf("@%s wrote in %s/%s/%d#issuecomment-%d:\n> %s\n\n%s",
			author, g.repo.HTMLURL(), path, issue.Index, parent.id, parent.firstLine, cm.Content)
		if _, err := db.GetEngine(g.ctx).ID(cm.ID).Cols("content").Update(cm); err != nil {
			return err
		}
	}
	return nil
}

// replyToIndex extracts Meta["ReplyTo"], tolerating the integer types a yaml or json
// round-trip may produce.
func replyToIndex(meta map[string]any) (int64, bool) {
	switch v := meta["ReplyTo"].(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case uint64:
		return int64(v), true
	case float64:
		return int64(v), true
	}
	return 0, false
}

// CreatePullRequests creates pull requests
func (g *GiteaLocalUploader) CreatePullRequests(prs ...*base.PullRequest) error {
	gprs := make([]*issues_model.PullRequest, 0, len(prs))
	for _, pr := range prs {
		gpr, err := g.newPullRequest(pr)
		if err != nil {
			return err
		}

		if err := g.remapUser(pr, gpr.Issue); err != nil {
			return err
		}

		gprs = append(gprs, gpr)
	}
	if err := issues_model.InsertPullRequests(g.ctx, gprs...); err != nil {
		return err
	}
	for _, pr := range gprs {
		g.issues[pr.Issue.Index] = pr.Issue
		pull.AddToTaskQueue(g.ctx, pr)
	}
	return nil
}

func (g *GiteaLocalUploader) updateGitForPullRequest(pr *base.PullRequest) (head string, err error) {
	// SECURITY: this pr must have been must have been ensured safe
	if !pr.EnsuredSafe {
		log.Error("PR #%d in %s/%s has not been checked for safety.", pr.Number, g.repoOwner, g.repoName)
		return "", fmt.Errorf("pull request[index=%d] was not checked for safety", pr.Number)
	}

	head = "unknown repository"
	if pr.IsForkPullRequest() && pr.State != "closed" {
		// OK we want to fetch the current head as a branch from its CloneURL

		// 1. Is there a head clone URL available?
		// 2. Is there a head ref available?
		if pr.Head.CloneURL == "" || pr.Head.Ref == "" {
			return head, nil
		}

		// 3. We need to create a remote for this clone url
		// ... maybe we already have a name for this remote
		remote, ok := g.prHeadCache[pr.Head.CloneURL+":"]
		if !ok {
			// ... let's try ownername as a reasonable name
			remote = pr.Head.OwnerName
			if !git.IsValidRefPattern(remote) {
				// ... let's try something less nice
				remote = "head-pr-" + strconv.FormatInt(pr.Number, 10)
			}
			// ... now add the remote
			err := g.gitRepo.AddRemote(remote, pr.Head.CloneURL, true)
			if err != nil {
				log.Error("PR #%d in %s/%s AddRemote[%s] failed: %v", pr.Number, g.repoOwner, g.repoName, remote, err)
			} else {
				g.prHeadCache[pr.Head.CloneURL+":"] = remote
				ok = true
			}
		}
		if !ok {
			return head, nil
		}

		// 4. Check if we already have this ref?
		localRef, ok := g.prHeadCache[pr.Head.CloneURL+":"+pr.Head.Ref]
		if !ok {
			// ... We would normally name this migrated branch as <OwnerName>/<HeadRef> but we need to ensure that is safe
			localRef = git.SanitizeRefPattern(pr.Head.OwnerName + "/" + pr.Head.Ref)

			// ... Now we must assert that this does not exist
			if g.gitRepo.IsBranchExist(localRef) {
				localRef = "head-pr-" + strconv.FormatInt(pr.Number, 10) + "/" + localRef
				i := 0
				for g.gitRepo.IsBranchExist(localRef) {
					if i > 5 {
						// ... We tried, we really tried but this is just a seriously unfriendly repo
						return head, nil
					}
					// OK just try some uuids!
					localRef = git.SanitizeRefPattern("head-pr-" + strconv.FormatInt(pr.Number, 10) + uuid.New().String())
					i++
				}
			}

			fetchArg := pr.Head.Ref + ":" + git.BranchPrefix + localRef
			if strings.HasPrefix(fetchArg, "-") {
				fetchArg = git.BranchPrefix + fetchArg
			}

			_, _, err = git.NewCommand(g.ctx, "fetch", "--no-tags").AddDashesAndList(remote, fetchArg).RunStdString(&git.RunOpts{Dir: g.repo.RepoPath()})
			if err != nil {
				log.Error("Fetch branch from %s failed: %v", pr.Head.CloneURL, err)
				return head, nil
			}
			g.prHeadCache[pr.Head.CloneURL+":"+pr.Head.Ref] = localRef
			head = localRef
		}

		// 5. Now if pr.Head.SHA == "" we should recover this to the head of this branch
		if pr.Head.SHA == "" {
			headSha, err := g.gitRepo.GetBranchCommitID(localRef)
			if err != nil {
				log.Error("unable to get head SHA of local head for PR #%d from %s in %s/%s. Error: %v", pr.Number, pr.Head.Ref, g.repoOwner, g.repoName, err)
				return head, nil
			}
			pr.Head.SHA = headSha
		}

		_, _, err = git.NewCommand(g.ctx, "update-ref", "--no-deref").AddDynamicArguments(pr.GetGitRefName(), pr.Head.SHA).RunStdString(&git.RunOpts{Dir: g.repo.RepoPath()})
		if err != nil {
			return "", err
		}

		return head, nil
	}

	if pr.Head.Ref != "" {
		head = pr.Head.Ref
	}

	// Ensure the closed PR SHA still points to an existing ref
	if pr.Head.SHA == "" {
		// The SHA is empty
		log.Warn("Empty reference, no pull head for PR #%d in %s/%s", pr.Number, g.repoOwner, g.repoName)
	} else {
		_, _, err = git.NewCommand(g.ctx, "rev-list", "--quiet", "-1").AddDynamicArguments(pr.Head.SHA).RunStdString(&git.RunOpts{Dir: g.repo.RepoPath()})
		if err != nil {
			// Git update-ref remove bad references with a relative path
			log.Warn("Deprecated local head %s for PR #%d in %s/%s, removing  %s", pr.Head.SHA, pr.Number, g.repoOwner, g.repoName, pr.GetGitRefName())
		} else {
			// set head information
			_, _, err = git.NewCommand(g.ctx, "update-ref", "--no-deref").AddDynamicArguments(pr.GetGitRefName(), pr.Head.SHA).RunStdString(&git.RunOpts{Dir: g.repo.RepoPath()})
			if err != nil {
				log.Error("unable to set %s as the local head for PR #%d from %s in %s/%s. Error: %v", pr.Head.SHA, pr.Number, pr.Head.Ref, g.repoOwner, g.repoName, err)
			}
		}
	}

	return head, nil
}

func (g *GiteaLocalUploader) newPullRequest(pr *base.PullRequest) (*issues_model.PullRequest, error) {
	var labels []*issues_model.Label
	for _, label := range pr.Labels {
		lb, ok := g.labels[label.Name]
		if ok {
			labels = append(labels, lb)
		}
	}

	milestoneID := g.milestones[pr.Milestone]

	head, err := g.updateGitForPullRequest(pr)
	if err != nil {
		return nil, fmt.Errorf("updateGitForPullRequest: %w", err)
	}

	// Now we may need to fix the mergebase
	if pr.Base.SHA == "" {
		if pr.Base.Ref != "" && pr.Head.SHA != "" {
			// A PR against a tag base does not make sense - therefore pr.Base.Ref must be a branch
			// TODO: should we be checking for the refs/heads/ prefix on the pr.Base.Ref? (i.e. are these actually branches or refs)
			pr.Base.SHA, err = g.gitRepo.GetMergeBase(git.BranchPrefix+pr.Base.Ref, pr.Head.SHA)
			if err != nil {
				log.Error("Cannot determine the merge base for PR #%d in %s/%s. Error: %v", pr.Number, g.repoOwner, g.repoName, err)
			}
		} else {
			log.Error("Cannot determine the merge base for PR #%d in %s/%s. Not enough information", pr.Number, g.repoOwner, g.repoName)
		}
	}

	if pr.Created.IsZero() {
		if pr.Closed != nil {
			pr.Created = *pr.Closed
		} else if pr.MergedTime != nil {
			pr.Created = *pr.MergedTime
		} else {
			pr.Created = time.Now()
		}
	}
	if pr.Updated.IsZero() {
		pr.Updated = pr.Created
	}

	prTitle := pr.Title
	if pr.IsDraft && !issues_model.HasWorkInProgressPrefix(pr.Title) {
		prTitle = fmt.Sprintf("%s %s", setting.Repository.PullRequest.WorkInProgressPrefixes[0], pr.Title)
	}

	issue := issues_model.Issue{
		RepoID:      g.repo.ID,
		Repo:        g.repo,
		Title:       util.TruncateRunes(prTitle, 255),
		Index:       pr.Number,
		Content:     pr.Content,
		MilestoneID: milestoneID,
		IsPull:      true,
		IsClosed:    pr.State == "closed",
		IsLocked:    pr.IsLocked,
		Labels:      labels,
		CreatedUnix: timeutil.TimeStamp(pr.Created.Unix()),
		UpdatedUnix: timeutil.TimeStamp(pr.Updated.Unix()),
	}

	if err := g.remapUser(pr, &issue); err != nil {
		return nil, err
	}

	// add reactions
	for _, reaction := range pr.Reactions {
		res := issues_model.Reaction{
			Type:        reaction.Content,
			CreatedUnix: timeutil.TimeStampNow(),
		}
		if err := g.remapUser(reaction, &res); err != nil {
			return nil, err
		}
		issue.Reactions = append(issue.Reactions, &res)
	}

	pullRequest := issues_model.PullRequest{
		HeadRepoID: g.repo.ID,
		HeadBranch: head,
		BaseRepoID: g.repo.ID,
		BaseBranch: pr.Base.Ref,
		MergeBase:  pr.Base.SHA,
		Index:      pr.Number,
		HasMerged:  pr.Merged,
		Flow:       issues_model.PullRequestFlow(pr.Flow),

		Issue: &issue,
	}

	if pullRequest.Issue.IsClosed && pr.Closed != nil {
		pullRequest.Issue.ClosedUnix = timeutil.TimeStamp(pr.Closed.Unix())
	}
	if pullRequest.HasMerged && pr.MergedTime != nil {
		pullRequest.MergedUnix = timeutil.TimeStamp(pr.MergedTime.Unix())
		pullRequest.MergedCommitID = pr.MergeCommitSHA
		pullRequest.MergerID = g.doer.ID
	}

	// TODO: assignees

	return &pullRequest, nil
}

func convertReviewState(state string) issues_model.ReviewType {
	switch state {
	case base.ReviewStatePending:
		return issues_model.ReviewTypePending
	case base.ReviewStateApproved:
		return issues_model.ReviewTypeApprove
	case base.ReviewStateChangesRequested:
		return issues_model.ReviewTypeReject
	case base.ReviewStateCommented:
		return issues_model.ReviewTypeComment
	case base.ReviewStateRequestReview:
		return issues_model.ReviewTypeRequest
	default:
		return issues_model.ReviewTypePending
	}
}

// CreateReviews create pull request reviews of currently migrated issues
func (g *GiteaLocalUploader) CreateReviews(reviews ...*base.Review) error {
	cms := make([]*issues_model.Review, 0, len(reviews))
	for _, review := range reviews {
		var issue *issues_model.Issue
		issue, ok := g.issues[review.IssueIndex]
		if !ok {
			return fmt.Errorf("review[index=%d,reviewer_id=%d] belongs to a issue[index=%d] that was not migrated", review.ID, review.ReviewerID, review.IssueIndex)
		}
		if review.CreatedAt.IsZero() {
			review.CreatedAt = time.Unix(int64(issue.CreatedUnix), 0)
		}

		cm := issues_model.Review{
			Type:        convertReviewState(review.State),
			IssueID:     issue.ID,
			Content:     review.Content,
			Official:    review.Official,
			CreatedUnix: timeutil.TimeStamp(review.CreatedAt.Unix()),
			UpdatedUnix: timeutil.TimeStamp(review.CreatedAt.Unix()),
		}

		if err := g.remapUser(review, &cm); err != nil {
			return err
		}

		cms = append(cms, &cm)

		// get pr
		pr, ok := g.prCache[issue.ID]
		if !ok {
			var err error
			pr, err = issues_model.GetPullRequestByIssueIDWithNoAttributes(g.ctx, issue.ID)
			if err != nil {
				return err
			}
			g.prCache[issue.ID] = pr
		}
		if pr.MergeBase == "" {
			// No mergebase -> no basis for any patches
			log.Warn("PR #%d in %s/%s: does not have a merge base, all review comments will be ignored", pr.Index, g.repoOwner, g.repoName)
			continue
		}

		headCommitID, err := g.gitRepo.GetRefCommitID(pr.GetGitRefName())
		if err != nil {
			log.Warn("PR #%d GetRefCommitID[%s] in %s/%s: %v, all review comments will be ignored", pr.Index, pr.GetGitRefName(), g.repoOwner, g.repoName, err)
			continue
		}

		for _, comment := range review.Comments {
			// Skip code comment if it doesn't have a diff it is commenting on.
			if comment.DiffHunk == "" {
				continue
			}

			line := comment.Line
			if line != 0 {
				comment.Position = 1
			} else if comment.DiffHunk != "" {
				_, _, line, _ = git.ParseDiffHunkString(comment.DiffHunk)
			}

			// SECURITY: The TreePath must be cleaned! use relative path
			comment.TreePath = util.PathJoinRel(comment.TreePath)

			var patch string
			reader, writer := io.Pipe()
			defer func() {
				_ = reader.Close()
				_ = writer.Close()
			}()
			go func(comment *base.ReviewComment) {
				if err := git.GetRepoRawDiffForFile(g.gitRepo, pr.MergeBase, headCommitID, git.RawDiffNormal, comment.TreePath, writer); err != nil {
					// We should ignore the error since the commit maybe removed when force push to the pull request
					log.Warn("GetRepoRawDiffForFile failed when migrating [%s, %s, %s, %s]: %v", g.gitRepo.Path, pr.MergeBase, headCommitID, comment.TreePath, err)
				}
				_ = writer.Close()
			}(comment)

			// For multi-line comments, center the patch on the last line and expand context to include the full range
			cutLine := int64((&issues_model.Comment{Line: int64(line + comment.Position - 1), ExtraLinesCount: comment.ExtraLinesCount}).UnsignedDisplayLine())
			patch, _ = git.CutDiffAroundLine(reader, cutLine, line < 0, setting.UI.CodeCommentLines+int(comment.ExtraLinesCount))

			if comment.CreatedAt.IsZero() {
				comment.CreatedAt = review.CreatedAt
			}
			if comment.UpdatedAt.IsZero() {
				comment.UpdatedAt = comment.CreatedAt
			}

			objectFormat := git.ObjectFormatFromName(g.repo.ObjectFormatName)
			if !objectFormat.IsValid(comment.CommitID) {
				log.Warn("Invalid comment CommitID[%s] on comment[%d] in PR #%d of %s/%s replaced with %s", comment.CommitID, pr.Index, g.repoOwner, g.repoName, headCommitID)
				comment.CommitID = headCommitID
			}

			c := issues_model.Comment{
				Type:            issues_model.CommentTypeCode,
				IssueID:         issue.ID,
				Content:         comment.Content,
				Line:            int64(line + comment.Position - 1),
				ExtraLinesCount: comment.ExtraLinesCount,
				TreePath:        comment.TreePath,
				CommitSHA:       comment.CommitID,
				Patch:           patch,
				CreatedUnix:     timeutil.TimeStamp(comment.CreatedAt.Unix()),
				UpdatedUnix:     timeutil.TimeStamp(comment.UpdatedAt.Unix()),
			}

			// A comment without its own author (e.g. a reply in a thread) inherits the review author.
			if comment.PosterName == "" {
				comment.PosterName = review.ReviewerName
				comment.PosterID = review.ReviewerID
			}
			if err := g.remapUser(comment, &c); err != nil {
				return err
			}

			cm.Comments = append(cm.Comments, &c)
		}
	}

	return issues_model.InsertReviews(g.ctx, cms)
}

// Rollback when migrating failed, this will rollback all the changes.
func (g *GiteaLocalUploader) Rollback() error {
	if g.repo != nil && g.repo.ID > 0 {
		g.gitRepo.Close()

		// do not delete the repository, otherwise the end users won't be able to see the last error message
	}
	return nil
}

// Finish when migrating success, this will do some status update things.
func (g *GiteaLocalUploader) Finish() error {
	if g.repo == nil || g.repo.ID <= 0 {
		return ErrRepoNotCreated
	}

	// update issue_index
	if err := issues_model.RecalculateIssueIndexForRepo(g.ctx, g.repo.ID); err != nil {
		return err
	}

	if err := models.UpdateRepoStats(g.ctx, g.repo.ID); err != nil {
		return err
	}

	g.repo.Status = repo_model.RepositoryReady
	return repo_model.UpdateRepositoryCols(g.ctx, g.repo, "status")
}

func (g *GiteaLocalUploader) remapUser(source user_model.ExternalUserMigrated, target user_model.ExternalUserRemappable) error {
	var userID int64
	var err error
	if g.sameApp {
		userID, err = g.remapLocalUser(source)
	} else {
		userID, err = g.remapExternalUser(source)
	}
	if err != nil {
		return err
	}

	if userID > 0 {
		return target.RemapExternalUser("", 0, userID)
	}
	return target.RemapExternalUser(source.GetExternalName(), source.GetExternalID(), user_model.GhostUserID)
}

func (g *GiteaLocalUploader) remapLocalUser(source user_model.ExternalUserMigrated) (int64, error) {
	userid, ok := g.userMap[source.GetExternalID()]
	if !ok {
		name, err := user_model.GetUserNameByID(g.ctx, source.GetExternalID())
		if err != nil {
			return 0, err
		}
		// let's not reuse an ID when the user was deleted or has a different user name
		if name != source.GetExternalName() {
			userid = 0
		} else {
			userid = source.GetExternalID()
		}
		g.userMap[source.GetExternalID()] = userid
	}
	return userid, nil
}

func (g *GiteaLocalUploader) remapExternalUser(source user_model.ExternalUserMigrated) (userid int64, err error) {
	userid, ok := g.userMap[source.GetExternalID()]
	if !ok {
		userid, err = user_model.GetUserIDByExternalUserID(g.ctx, g.gitServiceType.Name(), fmt.Sprintf("%d", source.GetExternalID()))
		if err != nil {
			log.Error("GetUserIDByExternalUserID: %v", err)
			return 0, err
		}
		g.userMap[source.GetExternalID()] = userid
	}
	return userid, nil
}

func (g *GiteaLocalUploader) migrateRepositoryAvatar(avatarURL string) error {
	client := allowlist.NewMigrationHTTPClient()
	client.Timeout = setting.Migrations.AvatarFetchTimeout
	byteBuffer, err := avatar.FetchExternalImageData(avatarURL, client)
	if err != nil {
		return err
	}

	bytes, err := io.ReadAll(byteBuffer)
	if err != nil {
		return err
	}

	return repo_service.UploadAvatar(g.ctx, g.repo, bytes)
}
