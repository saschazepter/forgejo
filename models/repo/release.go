// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"context"
	"fmt"
	"html/template"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"forgejo.org/models/db"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/container"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/structs"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"

	"xorm.io/builder"
)

// ErrReleaseAlreadyExist represents a "ReleaseAlreadyExist" kind of error.
type ErrReleaseAlreadyExist struct {
	TagName string
}

// IsErrReleaseAlreadyExist checks if an error is a ErrReleaseAlreadyExist.
func IsErrReleaseAlreadyExist(err error) bool {
	_, ok := err.(ErrReleaseAlreadyExist)
	return ok
}

func (err ErrReleaseAlreadyExist) Error() string {
	return fmt.Sprintf("release tag already exist [tag_name: %s]", err.TagName)
}

func (err ErrReleaseAlreadyExist) Unwrap() error {
	return util.ErrAlreadyExist
}

// ErrReleaseNotExist represents a "ReleaseNotExist" kind of error.
type ErrReleaseNotExist struct {
	ID      int64
	TagName string
}

// IsErrReleaseNotExist checks if an error is a ErrReleaseNotExist.
func IsErrReleaseNotExist(err error) bool {
	_, ok := err.(ErrReleaseNotExist)
	return ok
}

func (err ErrReleaseNotExist) Error() string {
	return fmt.Sprintf("release tag does not exist [id: %d, tag_name: %s]", err.ID, err.TagName)
}

func (err ErrReleaseNotExist) Unwrap() error {
	return util.ErrNotExist
}

// Release represents a release of repository.
type Release struct {
	ID                   int64            `xorm:"pk autoincr"`
	RepoID               int64            `xorm:"INDEX UNIQUE(n)"`
	Repo                 *Repository      `xorm:"-"`
	PublisherID          int64            `xorm:"INDEX"`
	Publisher            *user_model.User `xorm:"-"`
	TagName              string           `xorm:"INDEX UNIQUE(n)"`
	OriginalAuthor       string
	OriginalAuthorID     int64 `xorm:"index"`
	LowerTagName         string
	Target               string
	TargetBehind         string `xorm:"-"` // to handle non-existing or empty target
	Title                string
	Sha1                 string `xorm:"INDEX VARCHAR(64)"`
	HideArchiveLinks     bool   `xorm:"NOT NULL DEFAULT false"`
	NumCommits           int64
	NumCommitsBehind     int64                            `xorm:"-"`
	Note                 string                           `xorm:"TEXT"`
	RenderedNote         template.HTML                    `xorm:"-"`
	IsDraft              bool                             `xorm:"NOT NULL DEFAULT false"`
	IsPrerelease         bool                             `xorm:"NOT NULL DEFAULT false"`
	IsTag                bool                             `xorm:"NOT NULL DEFAULT false"` // will be true only if the record is a tag and has no related releases
	Attachments          []*Attachment                    `xorm:"-"`
	CreatedUnix          timeutil.TimeStamp               `xorm:"INDEX"`
	ArchiveDownloadCount *structs.TagArchiveDownloadCount `xorm:"-"`
}

func init() {
	db.RegisterModel(new(Release))
}

// LoadAttributes load repo and publisher attributes for a release
func (r *Release) LoadAttributes(ctx context.Context) error {
	err := r.LoadRepo(ctx)
	if err != nil {
		return err
	}

	if r.Publisher == nil {
		r.Publisher, err = user_model.GetUserByID(ctx, r.PublisherID)
		if err != nil {
			if user_model.IsErrUserNotExist(err) {
				r.Publisher = user_model.NewGhostUser()
			} else {
				return err
			}
		}
	}

	// This function may be called on a release that's not yet in the database.
	// The next two attributes relies on the release struct already having been
	// assigned a ID. Return early if that's not the case.
	if r.ID == 0 {
		return nil
	}

	err = r.LoadArchiveDownloadCount(ctx)
	if err != nil {
		return err
	}

	return GetReleaseAttachments(ctx, r)
}

// LoadRepo load repo attribute for release
func (r *Release) LoadRepo(ctx context.Context) error {
	if r.Repo != nil {
		return nil
	}

	var err error
	r.Repo, err = GetRepositoryByID(ctx, r.RepoID)

	return err
}

// LoadArchiveDownloadCount loads the download count for the source archives
func (r *Release) LoadArchiveDownloadCount(ctx context.Context) error {
	var err error
	r.ArchiveDownloadCount, err = GetArchiveDownloadCount(ctx, r.RepoID, r.ID)
	return err
}

// GetTotalDownloadCount returns the summary of all download count of files attached to the release
func (r *Release) GetTotalDownloadCount(ctx context.Context) (int64, error) {
	var archiveCount int64
	if !r.HideArchiveLinks {
		_, err := db.GetEngine(ctx).SQL("SELECT SUM(count) FROM repo_archive_download_count WHERE release_id = ?", r.ID).Get(&archiveCount)
		if err != nil {
			return 0, err
		}
	}

	var attachmentCount int64
	_, err := db.GetEngine(ctx).SQL("SELECT SUM(download_count) FROM attachment WHERE release_id = ?", r.ID).Get(&attachmentCount)
	if err != nil {
		return 0, err
	}

	return archiveCount + attachmentCount, nil
}

// APIURL the api url for a release. release must have attributes loaded
func (r *Release) APIURL() string {
	return r.Repo.APIURL() + "/releases/" + strconv.FormatInt(r.ID, 10)
}

// ZipURL the zip url for a release. release must have attributes loaded
func (r *Release) ZipURL() string {
	return r.Repo.HTMLURL() + "/archive/" + util.PathEscapeSegments(r.TagName) + ".zip"
}

// TarURL the tar.gz url for a release. release must have attributes loaded
func (r *Release) TarURL() string {
	return r.Repo.HTMLURL() + "/archive/" + util.PathEscapeSegments(r.TagName) + ".tar.gz"
}

// HTMLURL the url for a release on the web UI. release must have attributes loaded
func (r *Release) HTMLURL() string {
	return r.Repo.HTMLURL() + "/releases/tag/" + util.PathEscapeSegments(r.TagName)
}

// APIUploadURL the api url to upload assets to a release. release must have attributes loaded
// If `githubFormat` is true, then `{?name,label}` is added to match the Github API.
func (r *Release) APIUploadURL(githubFormat bool) string {
	if githubFormat {
		return r.APIURL() + "/assets{?name,label}"
	}
	return r.APIURL() + "/assets"
}

// Link the relative url for a release on the web UI. release must have attributes loaded
func (r *Release) Link() string {
	return r.Repo.Link() + "/releases/tag/" + util.PathEscapeSegments(r.TagName)
}

// SummaryCardURL returns the absolute URL to an image providing a summary of the release
func (r *Release) SummaryCardURL() string {
	return fmt.Sprintf("%s/releases/summary-card/%s", r.Repo.HTMLURL(), util.PathEscapeSegments(r.TagName))
}

// DisplayName returns the name of the release
func (r *Release) DisplayName() string {
	if r.IsTag && r.Title == "" {
		return r.TagName
	}

	return r.Title
}

// IsReleaseExist returns true if release with given tag name already exists.
func IsReleaseExist(ctx context.Context, repoID int64, tagName string) (bool, error) {
	if len(tagName) == 0 {
		return false, nil
	}

	return db.GetEngine(ctx).Exist(&Release{RepoID: repoID, LowerTagName: strings.ToLower(tagName)})
}

// UpdateRelease updates all columns of a release
func UpdateRelease(ctx context.Context, rel *Release) error {
	rel.Title, _ = util.SplitStringAtByteN(rel.Title, 255)
	_, err := db.GetEngine(ctx).ID(rel.ID).AllCols().Update(rel)
	return err
}

// AddReleaseAttachments adds a release attachments
func AddReleaseAttachments(ctx context.Context, release *Release, attachmentUUIDs []string) (err error) {
	attachments, err := FindRepoAttachmentsByUUID(ctx, release.RepoID, attachmentUUIDs, FindAttachmentOptions{})
	if err != nil {
		return fmt.Errorf("FindRepoAttachmentsByUUID[uuids=%q,repoID=%d]: %w", attachmentUUIDs, release.RepoID, err)
	}

	for i := range attachments {
		attachments[i].ReleaseID = release.ID
		// No assign value could be 0, so ignore AllCols().
		if _, err = db.GetEngine(ctx).ID(attachments[i].ID).Update(attachments[i]); err != nil {
			return fmt.Errorf("update attachment [%d]: %w", attachments[i].ID, err)
		}
	}

	return err
}

// GetRelease returns release by given ID.
func GetRelease(ctx context.Context, repoID int64, tagName string) (*Release, error) {
	rel := &Release{RepoID: repoID, LowerTagName: strings.ToLower(tagName)}
	has, err := db.GetEngine(ctx).Get(rel)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, ErrReleaseNotExist{0, tagName}
	}
	return rel, nil
}

// GetReleaseByID returns release with given ID.
func GetReleaseByID(ctx context.Context, id int64) (*Release, error) {
	rel := new(Release)
	has, err := db.GetEngine(ctx).
		ID(id).
		Get(rel)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, ErrReleaseNotExist{id, ""}
	}

	return rel, nil
}

// GetReleaseForRepoByID returns release with given ID.
func GetReleaseForRepoByID(ctx context.Context, repoID, id int64) (*Release, error) {
	rel := new(Release)
	has, err := db.GetEngine(ctx).
		Where("id=? AND repo_id=?", id, repoID).
		Get(rel)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, ErrReleaseNotExist{id, ""}
	}

	return rel, nil
}

// FindReleasesOptions describes the conditions to Find releases
type FindReleasesOptions struct {
	db.ListOptions
	RepoID        int64
	IncludeDrafts bool
	IncludeTags   bool
	IsPreRelease  optional.Option[bool]
	IsDraft       optional.Option[bool]
	TagNames      []string
	HasSha1       optional.Option[bool] // useful to find draft releases which are created with existing tags
	Keyword       string
}

func (opts FindReleasesOptions) ToConds() builder.Cond {
	var cond builder.Cond = builder.Eq{"repo_id": opts.RepoID}

	if !opts.IncludeDrafts {
		cond = cond.And(builder.Eq{"is_draft": false})
	}
	if !opts.IncludeTags {
		cond = cond.And(builder.Eq{"is_tag": false})
	}
	if len(opts.TagNames) > 0 {
		cond = cond.And(builder.In("tag_name", opts.TagNames))
	}
	if has, value := opts.IsPreRelease.Get(); has {
		cond = cond.And(builder.Eq{"is_prerelease": value})
	}
	if has, value := opts.IsDraft.Get(); has {
		cond = cond.And(builder.Eq{"is_draft": value})
	}
	if has, value := opts.HasSha1.Get(); has {
		if value {
			cond = cond.And(builder.Neq{"sha1": ""})
		} else {
			cond = cond.And(builder.Eq{"sha1": ""})
		}
	}

	if opts.Keyword != "" {
		keywordCond := builder.NewCond()
		keywordCond = keywordCond.Or(builder.Like{"lower_tag_name", strings.ToLower(opts.Keyword)})
		keywordCond = keywordCond.Or(db.BuildCaseInsensitiveLike("title", opts.Keyword))
		keywordCond = keywordCond.Or(db.BuildCaseInsensitiveLike("note", opts.Keyword))
		cond = cond.And(keywordCond)
	}

	return cond
}

func (opts FindReleasesOptions) ToOrders() string {
	return "created_unix DESC, id DESC"
}

// GetTagNamesByRepoID returns a list of release tag names of repository.
func GetTagNamesByRepoID(ctx context.Context, repoID int64) ([]string, error) {
	listOptions := db.ListOptions{
		ListAll: true,
	}
	opts := FindReleasesOptions{
		ListOptions:   listOptions,
		IncludeDrafts: true,
		IncludeTags:   true,
		HasSha1:       optional.Some(true),
		RepoID:        repoID,
	}

	tags := make([]string, 0)
	sess := db.GetEngine(ctx).
		Table("release").
		Desc("created_unix", "id").
		Where(opts.ToConds()).
		Cols("tag_name")

	return tags, sess.Find(&tags)
}

// GetLatestReleaseByRepoID returns the latest release for a repository
func GetLatestReleaseByRepoID(ctx context.Context, repoID int64) (*Release, error) {
	cond := builder.NewCond().
		And(builder.Eq{"repo_id": repoID}).
		And(builder.Eq{"is_draft": false}).
		And(builder.Eq{"is_prerelease": false}).
		And(builder.Eq{"is_tag": false})

	rel := new(Release)
	has, err := db.GetEngine(ctx).
		Desc("created_unix", "id").
		Where(cond).
		Get(rel)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, ErrReleaseNotExist{0, "latest"}
	}

	return rel, nil
}

type releaseMetaSearch struct {
	ID  []int64
	Rel []*Release
}

func (s releaseMetaSearch) Len() int {
	return len(s.ID)
}

func (s releaseMetaSearch) Swap(i, j int) {
	s.ID[i], s.ID[j] = s.ID[j], s.ID[i]
	s.Rel[i], s.Rel[j] = s.Rel[j], s.Rel[i]
}

func (s releaseMetaSearch) Less(i, j int) bool {
	return s.ID[i] < s.ID[j]
}

func hasDuplicateName(attaches []*Attachment) bool {
	attachSet := container.Set[string]{}
	for _, attachment := range attaches {
		if attachSet.Contains(attachment.Name) {
			return true
		}
		attachSet.Add(attachment.Name)
	}
	return false
}

// GetReleaseAttachments retrieves the attachments for releases
func GetReleaseAttachments(ctx context.Context, rels ...*Release) (err error) {
	if len(rels) == 0 {
		return nil
	}

	// To keep this efficient as possible sort all releases by id,
	//    select attachments by release id,
	//    then merge join them

	// Sort
	sortedRels := releaseMetaSearch{ID: make([]int64, len(rels)), Rel: make([]*Release, len(rels))}
	var attachments []*Attachment
	for index, element := range rels {
		element.Attachments = []*Attachment{}
		sortedRels.ID[index] = element.ID
		sortedRels.Rel[index] = element
	}
	sort.Sort(sortedRels)

	// Select attachments
	err = db.GetEngine(ctx).
		Asc("release_id", "name").
		In("release_id", sortedRels.ID).
		Find(&attachments)
	if err != nil {
		return err
	}

	// merge join
	currentIndex := 0
	for _, attachment := range attachments {
		for sortedRels.ID[currentIndex] < attachment.ReleaseID {
			currentIndex++
		}
		sortedRels.Rel[currentIndex].Attachments = append(sortedRels.Rel[currentIndex].Attachments, attachment)
	}

	// Makes URL's predictable
	for _, release := range rels {
		// If we have no Repo, we don't need to execute this loop
		if release.Repo == nil {
			continue
		}

		// If the names unique, use the URL with the Name instead of the UUID
		if !hasDuplicateName(release.Attachments) {
			for _, attachment := range release.Attachments {
				attachment.CustomDownloadURL = release.Repo.HTMLURL() + "/releases/download/" + url.PathEscape(release.TagName) + "/" + url.PathEscape(attachment.Name)
			}
		}
	}

	return err
}

// UpdateReleasesMigrationsByType updates all migrated repositories' releases from gitServiceType to replace originalAuthorID to posterID
func UpdateReleasesMigrationsByType(ctx context.Context, gitServiceType structs.GitServiceType, originalAuthorID string, posterID int64) error {
	_, err := db.GetEngine(ctx).Table("release").
		Where("repo_id IN (SELECT id FROM repository WHERE original_service_type = ?)", gitServiceType).
		And("original_author_id = ?", originalAuthorID).
		Update(map[string]any{
			"publisher_id":       posterID,
			"original_author":    "",
			"original_author_id": 0,
		})
	return err
}

// PushUpdateDeleteTagsContext updates a number of delete tags with context
func PushUpdateDeleteTagsContext(ctx context.Context, repo *Repository, tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	lowerTags := make([]string, 0, len(tags))
	for _, tag := range tags {
		lowerTags = append(lowerTags, strings.ToLower(tag))
	}

	for _, tag := range tags {
		release, err := GetRelease(ctx, repo.ID, tag)
		if err != nil {
			return fmt.Errorf("GetRelease: %w", err)
		}

		err = DeleteArchiveDownloadCountForRelease(ctx, release.ID)
		if err != nil {
			return fmt.Errorf("DeleteTagArchiveDownloadCount: %w", err)
		}
	}

	if _, err := db.GetEngine(ctx).
		Where("repo_id = ? AND is_tag = ?", repo.ID, true).
		In("lower_tag_name", lowerTags).
		Delete(new(Release)); err != nil {
		return fmt.Errorf("Delete: %w", err)
	}

	if _, err := db.GetEngine(ctx).
		Where("repo_id = ? AND is_tag = ?", repo.ID, false).
		In("lower_tag_name", lowerTags).
		Cols("is_draft", "num_commits", "sha1").
		Update(&Release{
			IsDraft: true,
		}); err != nil {
		return fmt.Errorf("Update: %w", err)
	}

	return nil
}

// PushUpdateDeleteTag must be called for any push actions to delete tag
func PushUpdateDeleteTag(ctx context.Context, repo *Repository, tagName string) error {
	rel, err := GetRelease(ctx, repo.ID, tagName)
	if err != nil {
		if IsErrReleaseNotExist(err) {
			return nil
		}
		return fmt.Errorf("GetRelease: %w", err)
	}
	if rel.IsTag {
		if _, err = db.DeleteByID[Release](ctx, rel.ID); err != nil {
			return fmt.Errorf("Delete: %w", err)
		}
	} else {
		rel.IsDraft = true
		rel.NumCommits = 0
		rel.Sha1 = ""
		if _, err = db.GetEngine(ctx).ID(rel.ID).AllCols().Update(rel); err != nil {
			return fmt.Errorf("Update: %w", err)
		}
	}

	return nil
}

// SaveOrUpdateTag must be called for any push actions to add tag
func SaveOrUpdateTag(ctx context.Context, repo *Repository, newRel *Release) error {
	rel, err := GetRelease(ctx, repo.ID, newRel.TagName)
	if err != nil && !IsErrReleaseNotExist(err) {
		return fmt.Errorf("GetRelease: %w", err)
	}

	if rel == nil {
		rel = newRel
		if _, err = db.GetEngine(ctx).Insert(rel); err != nil {
			return fmt.Errorf("InsertOne: %w", err)
		}
	} else {
		rel.Sha1 = newRel.Sha1
		rel.CreatedUnix = newRel.CreatedUnix
		rel.NumCommits = newRel.NumCommits
		rel.IsDraft = false
		if rel.IsTag && newRel.PublisherID > 0 {
			rel.PublisherID = newRel.PublisherID
		}
		if _, err = db.GetEngine(ctx).ID(rel.ID).AllCols().Update(rel); err != nil {
			return fmt.Errorf("Update: %w", err)
		}
	}
	return nil
}

// RemapExternalUser ExternalUserRemappable interface
func (r *Release) RemapExternalUser(externalName string, externalID, userID int64) error {
	r.OriginalAuthor = externalName
	r.OriginalAuthorID = externalID
	r.PublisherID = userID
	return nil
}

// UserID ExternalUserRemappable interface
func (r *Release) GetUserID() int64 { return r.PublisherID }

// ExternalName ExternalUserRemappable interface
func (r *Release) GetExternalName() string { return r.OriginalAuthor }

// ExternalID ExternalUserRemappable interface
func (r *Release) GetExternalID() int64 { return r.OriginalAuthorID }

// InsertReleases migrates release
func InsertReleases(ctx context.Context, rels ...*Release) error {
	ctx, committer, err := db.TxContext(ctx)
	if err != nil {
		return err
	}
	defer committer.Close()
	sess := db.GetEngine(ctx)

	for _, rel := range rels {
		if _, err := sess.NoAutoTime().Insert(rel); err != nil {
			return err
		}

		if len(rel.Attachments) > 0 {
			for i := range rel.Attachments {
				rel.Attachments[i].ReleaseID = rel.ID
				rel.Attachments[i].RepoID = rel.RepoID
			}

			if _, err := sess.NoAutoTime().Insert(rel.Attachments); err != nil {
				return err
			}
		}
	}

	return committer.Commit()
}

func FindTagsByCommitIDs(ctx context.Context, repoID int64, commitIDs ...string) (map[string][]*Release, error) {
	releases := make([]*Release, 0, len(commitIDs))
	if err := db.GetEngine(ctx).Where("repo_id=?", repoID).
		In("sha1", commitIDs).
		Find(&releases); err != nil {
		return nil, err
	}
	res := make(map[string][]*Release, len(releases))
	for _, r := range releases {
		res[r.Sha1] = append(res[r.Sha1], r)
	}
	return res, nil
}
