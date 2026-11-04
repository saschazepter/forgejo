// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// This artifact server is inspired by https://github.com/nektos/act/blob/master/pkg/artifacts/server.go.
// It updates url setting and uses ObjectStore to handle artifacts persistence.

package actions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"forgejo.org/models/db"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"

	"xorm.io/builder"
)

// This is a pointless change but it is in models/actions.

// ArtifactStatus is the status of an artifact, uploading, expired or need-delete
type ArtifactStatus int64

const (
	ArtifactStatusUploadPending   ArtifactStatus = iota + 1 // 1， ArtifactStatusUploadPending is the status of an artifact upload that is pending
	ArtifactStatusUploadConfirmed                           // 2， ArtifactStatusUploadConfirmed is the status of an artifact upload that is confirmed
	ArtifactStatusUploadError                               // 3， ArtifactStatusUploadError is the status of an artifact upload that is errored
	ArtifactStatusExpired                                   // 4, ArtifactStatusExpired is the status of an artifact that is expired
	ArtifactStatusPendingDeletion                           // 5, ArtifactStatusPendingDeletion is the status of an artifact that is pending deletion
	ArtifactStatusDeleted                                   // 6, ArtifactStatusDeleted is the status of an artifact that is deleted
)

func init() {
	db.RegisterModel(new(ActionArtifact))
}

// ActionArtifact is a file that is stored in the artifact storage.
type ActionArtifact struct {
	ID                 int64 `xorm:"pk autoincr"`
	RunID              int64 `xorm:"index unique(runid_name_path)"` // The run id of the artifact
	RunnerID           int64
	RepoID             int64 `xorm:"index"`
	OwnerID            int64
	CommitSHA          string
	StoragePath        string             // The path to the artifact in the storage
	FileSize           int64              // The size of the artifact in bytes
	FileCompressedSize int64              // The size of the artifact in bytes after gzip compression
	ContentEncoding    string             // The content encoding of the artifact
	ArtifactPath       string             `xorm:"index unique(runid_name_path)"` // The path to the artifact when runner uploads it
	ArtifactName       string             `xorm:"index unique(runid_name_path)"` // The name of the artifact when runner uploads it
	Status             int64              `xorm:"index"`                         // The status of the artifact, uploading, expired or need-delete
	CreatedUnix        timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix        timeutil.TimeStamp `xorm:"updated index"`
	ExpiredUnix        timeutil.TimeStamp `xorm:"index"` // The time when the artifact will be expired
}

func CreateArtifact(ctx context.Context, t *ActionTask, artifactName, artifactPath string, expiredDays int64) (*ActionArtifact, error) {
	if err := t.LoadJob(ctx); err != nil {
		return nil, err
	}
	artifact, err := getArtifactByNameAndPath(ctx, t.Job.RunID, artifactName, artifactPath)
	if errors.Is(err, util.ErrNotExist) {
		artifact := &ActionArtifact{
			ArtifactName: artifactName,
			ArtifactPath: artifactPath,
			RunID:        t.Job.RunID,
			RunnerID:     t.RunnerID,
			RepoID:       t.RepoID,
			OwnerID:      t.OwnerID,
			CommitSHA:    t.CommitSHA,
			Status:       int64(ArtifactStatusUploadPending),
			ExpiredUnix:  timeutil.TimeStamp(time.Now().Unix() + timeutil.Day*expiredDays),
		}
		if _, err := db.GetEngine(ctx).Insert(artifact); err != nil {
			return nil, err
		}
		return artifact, nil
	} else if err != nil {
		return nil, err
	}

	if _, err := db.GetEngine(ctx).ID(artifact.ID).Cols("expired_unix").Update(&ActionArtifact{
		ExpiredUnix: timeutil.TimeStamp(time.Now().Unix() + timeutil.Day*expiredDays),
	}); err != nil {
		return nil, err
	}

	return artifact, nil
}

// IsV4 reports whether the artifact was uploaded via the v4 backend.
// The v4 backend stores the whole artifact as a single zip file;
// v1-v3 stores each file as a separate row.
func (a *ActionArtifact) IsV4() bool {
	return a.ArtifactName+".zip" == a.ArtifactPath && a.ContentEncoding == "application/zip"
}

func getArtifactByNameAndPath(ctx context.Context, runID int64, name, fpath string) (*ActionArtifact, error) {
	var art ActionArtifact
	has, err := db.GetEngine(ctx).Where("run_id = ? AND artifact_name = ? AND artifact_path = ?", runID, name, fpath).Get(&art)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, util.ErrNotExist
	}
	return &art, nil
}

// UpdateArtifactByID updates an artifact by id
func UpdateArtifactByID(ctx context.Context, id int64, art *ActionArtifact) error {
	art.ID = id
	_, err := db.GetEngine(ctx).ID(id).AllCols().Update(art)
	return err
}

type FindArtifactsOptions struct {
	db.ListOptions
	ID           int64
	RepoID       int64
	RunID        int64
	ArtifactName string
	Status       int
}

func (opts FindArtifactsOptions) ToConds() builder.Cond {
	cond := builder.NewCond()
	if opts.ID > 0 {
		cond = cond.And(builder.Eq{"id": opts.ID})
	}
	if opts.RepoID > 0 {
		cond = cond.And(builder.Eq{"repo_id": opts.RepoID})
	}
	if opts.RunID > 0 {
		cond = cond.And(builder.Eq{"run_id": opts.RunID})
	}
	if opts.ArtifactName != "" {
		cond = cond.And(builder.Eq{"artifact_name": opts.ArtifactName})
	}
	if opts.Status > 0 {
		cond = cond.And(builder.Eq{"status": opts.Status})
	}

	return cond
}

var _ db.FindOptionsOrder = FindArtifactsOptions{}

// ToOrders implements db.FindOptionsOrder, to have a stable order
func (opts FindArtifactsOptions) ToOrders() string {
	return "id"
}

// ActionArtifactMeta is the meta data of an artifact
type ActionArtifactMeta struct {
	ArtifactName string
	FileSize     int64
	Status       ArtifactStatus
}

// AggregatedArtifact is the aggregated view of a logical artifact
// (one or more rows sharing the same run_id + artifact_name), used by the
// public API to represent a single artifact to clients.
type AggregatedArtifact struct {
	ID           int64              `xorm:"id"`
	RunID        int64              `xorm:"run_id"`
	RepoID       int64              `xorm:"-"`
	ArtifactName string             `xorm:"artifact_name"`
	FileSize     int64              `xorm:"file_size"`
	Status       ArtifactStatus     `xorm:"status"`
	CreatedUnix  timeutil.TimeStamp `xorm:"created_unix"`
	UpdatedUnix  timeutil.TimeStamp `xorm:"updated_unix"`
	ExpiredUnix  timeutil.TimeStamp `xorm:"expired_unix"`
}

// APIDownloadURL returns the download URL for this artifact under the given
// repository API URL prefix (e.g. "https://host/api/v1/repos/owner/name").
func (a *AggregatedArtifact) APIDownloadURL(repoAPIURL string) string {
	return fmt.Sprintf("%s/actions/artifacts/%d/zip", repoAPIURL, a.ID)
}

// ListUploadedArtifactsMeta returns all uploaded artifacts meta of a run
func ListUploadedArtifactsMeta(ctx context.Context, runID int64) ([]*ActionArtifactMeta, error) {
	arts := make([]*ActionArtifactMeta, 0, 10)
	return arts, db.GetEngine(ctx).Table("action_artifact").
		Where(builder.Eq{"run_id": runID}.And(builder.In("status", ArtifactStatusUploadConfirmed, ArtifactStatusExpired))).
		GroupBy("artifact_name").
		Select("artifact_name, sum(file_size) as file_size, max(status) as status").
		Find(&arts)
}

// ListNeedExpiredArtifacts returns all need expired artifacts but not deleted
func ListNeedExpiredArtifacts(ctx context.Context) ([]*ActionArtifact, error) {
	arts := make([]*ActionArtifact, 0, 10)
	return arts, db.GetEngine(ctx).
		Where("expired_unix < ? AND status = ?", timeutil.TimeStamp(time.Now().Unix()), ArtifactStatusUploadConfirmed).Find(&arts)
}

// ListPendingDeleteArtifacts returns all artifacts in pending-delete status.
// limit is the max number of artifacts to return.
func ListPendingDeleteArtifacts(ctx context.Context, limit int) ([]*ActionArtifact, error) {
	arts := make([]*ActionArtifact, 0, limit)
	return arts, db.GetEngine(ctx).
		Where("status = ?", ArtifactStatusPendingDeletion).Limit(limit).Find(&arts)
}

// SetArtifactExpired sets an artifact to expired
func SetArtifactExpired(ctx context.Context, artifactID int64) error {
	_, err := db.GetEngine(ctx).Where("id=? AND status = ?", artifactID, ArtifactStatusUploadConfirmed).Cols("status").Update(&ActionArtifact{Status: int64(ArtifactStatusExpired)})
	return err
}

// SetArtifactNeedDelete sets an artifact to need-delete, cron job will delete it
func SetArtifactNeedDelete(ctx context.Context, runID int64, name string) error {
	_, err := db.GetEngine(ctx).Where("run_id=? AND artifact_name=? AND status = ?", runID, name, ArtifactStatusUploadConfirmed).Cols("status").Update(&ActionArtifact{Status: int64(ArtifactStatusPendingDeletion)})
	return err
}

// SetArtifactDeleted sets an artifact to deleted
func SetArtifactDeleted(ctx context.Context, artifactID int64) error {
	_, err := db.GetEngine(ctx).ID(artifactID).Cols("status").Update(&ActionArtifact{Status: int64(ArtifactStatusDeleted)})
	return err
}

// SetArtifactsOfRunDeleted marks all artifacts of the given run as deleted.
func SetArtifactsOfRunDeleted(ctx context.Context, runID int64) error {
	_, err := db.GetEngine(ctx).
		Where("run_id=?", runID).
		Cols("status").
		Update(&ActionArtifact{Status: int64(ArtifactStatusPendingDeletion)})
	return err
}

// aggregatedArtifactConds returns the common WHERE clause used by aggregated
// artifact queries: restrict to visible statuses and apply the caller's filters.
// The Status field on opts is ignored — visibility is fixed to UploadConfirmed/Expired.
func aggregatedArtifactConds(opts FindArtifactsOptions) builder.Cond {
	opts.Status = 0
	return opts.ToConds().And(builder.In("status", ArtifactStatusUploadConfirmed, ArtifactStatusExpired))
}

const aggregatedArtifactSelect = "min(id) as id, run_id, artifact_name, sum(file_size) as file_size, max(status) as status, min(created_unix) as created_unix, max(updated_unix) as updated_unix, max(expired_unix) as expired_unix"

// ListAggregatedArtifacts returns paginated aggregated artifacts.
// Each result represents one logical artifact: a (run_id, artifact_name) group,
// with ID = MIN(id), FileSize = SUM(file_size), Status = MAX(status), and
// timestamps aggregated accordingly. Status filter in opts is ignored; results
// are always restricted to UploadConfirmed and Expired statuses.
func ListAggregatedArtifacts(ctx context.Context, opts FindArtifactsOptions) ([]*AggregatedArtifact, int64, error) {
	cond := aggregatedArtifactConds(opts)

	var countKeys []struct {
		ID int64 `xorm:"id"`
	}
	if err := db.GetEngine(ctx).Table("action_artifact").
		Where(cond).
		GroupBy("run_id, artifact_name").
		Select("min(id) as id").
		Find(&countKeys); err != nil {
		return nil, 0, err
	}
	total := int64(len(countKeys))

	sess := db.GetEngine(ctx).Table("action_artifact").
		Where(cond).
		GroupBy("run_id, artifact_name").
		Select(aggregatedArtifactSelect).
		OrderBy("id DESC")

	capacity := 10
	if opts.PageSize > 0 {
		sess = sess.Limit(opts.PageSize, (opts.Page-1)*opts.PageSize)
		capacity = opts.PageSize
	}

	arts := make([]*AggregatedArtifact, 0, capacity)
	return arts, total, sess.Find(&arts)
}

// GetAggregatedArtifactByID returns the aggregated artifact by its canonical ID
// (MIN(id) of the group), scoped to the given repository. Returns util.ErrNotExist
// when the ID does not exist, is not canonical for its group, or does not belong to repoID.
// The repoID scoping is performed in the query so callers don't need a follow-up check.
func GetAggregatedArtifactByID(ctx context.Context, repoID, artifactID int64) (*AggregatedArtifact, error) {
	var art ActionArtifact
	has, err := db.GetEngine(ctx).Where(builder.Eq{"id": artifactID, "repo_id": repoID}).Get(&art)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, util.ErrNotExist
	}

	cond := aggregatedArtifactConds(FindArtifactsOptions{
		RunID:        art.RunID,
		ArtifactName: art.ArtifactName,
	})

	meta := new(AggregatedArtifact)
	has, err = db.GetEngine(ctx).Table("action_artifact").
		Where(cond).
		GroupBy("run_id, artifact_name").
		Select(aggregatedArtifactSelect).
		Get(meta)
	if err != nil {
		return nil, err
	}
	if !has || meta.ID != artifactID {
		return nil, util.ErrNotExist
	}

	meta.RepoID = art.RepoID
	return meta, nil
}
