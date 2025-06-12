// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package quota

import (
	"context"

	action_model "forgejo.org/models/actions"
	"forgejo.org/models/db"
	package_model "forgejo.org/models/packages"
	repo_model "forgejo.org/models/repo"

	"xorm.io/builder"
)

type Used struct {
	Size UsedSize
}

type UsedSize struct {
	Repos  UsedSizeRepos
	Git    UsedSizeGit
	Assets UsedSizeAssets
}

func (u UsedSize) All() int64 {
	return u.Repos.All() + u.Git.All(u.Repos) + u.Assets.All()
}

type UsedSizeRepos struct {
	Public  int64
	Private int64
}

func (u UsedSizeRepos) All() int64 {
	return u.Public + u.Private
}

type UsedSizeGit struct {
	LFS int64
}

func (u UsedSizeGit) All(r UsedSizeRepos) int64 {
	return u.LFS + r.All()
}

type UsedSizeAssets struct {
	Attachments UsedSizeAssetsAttachments
	Artifacts   int64
	Packages    UsedSizeAssetsPackages
}

func (u UsedSizeAssets) All() int64 {
	return u.Attachments.All() + u.Artifacts + u.Packages.All
}

type UsedSizeAssetsAttachments struct {
	Issues   int64
	Releases int64
}

func (u UsedSizeAssetsAttachments) All() int64 {
	return u.Issues + u.Releases
}

type UsedSizeAssetsPackages struct {
	All int64
}

func (u Used) CalculateFor(subject LimitSubject) int64 {
	switch subject {
	case LimitSubjectNone:
		return 0
	case LimitSubjectSizeAll:
		return u.Size.All()
	case LimitSubjectSizeReposAll:
		return u.Size.Repos.All()
	case LimitSubjectSizeReposPublic:
		return u.Size.Repos.Public
	case LimitSubjectSizeReposPrivate:
		return u.Size.Repos.Private
	case LimitSubjectSizeGitAll:
		return u.Size.Git.All(u.Size.Repos)
	case LimitSubjectSizeGitLFS:
		return u.Size.Git.LFS
	case LimitSubjectSizeAssetsAll:
		return u.Size.Assets.All()
	case LimitSubjectSizeAssetsAttachmentsAll:
		return u.Size.Assets.Attachments.All()
	case LimitSubjectSizeAssetsAttachmentsIssues:
		return u.Size.Assets.Attachments.Issues
	case LimitSubjectSizeAssetsAttachmentsReleases:
		return u.Size.Assets.Attachments.Releases
	case LimitSubjectSizeAssetsArtifacts:
		return u.Size.Assets.Artifacts
	case LimitSubjectSizeAssetsPackagesAll:
		return u.Size.Assets.Packages.All
	case LimitSubjectSizeWiki:
		return 0
	}
	return 0
}

func makeUserOwnedCondition(q string, userID int64) builder.Cond {
	switch q {
	case "repositories", "attachments", "artifacts":
		return builder.Eq{"`repository`.owner_id": userID}
	case "packages":
		return builder.Or(
			builder.Eq{"`repository`.owner_id": userID},
			builder.And(
				builder.Eq{"`package`.repo_id": 0},
				builder.Eq{"`package`.owner_id": userID},
			),
		)
	}
	return builder.NewCond()
}

func createQueryFor(ctx context.Context, userID int64, q string) db.Engine {
	session := db.GetEngine(ctx)

	switch q {
	case "repositories":
		session = session.Table("repository")
	case "attachments":
		session = session.
			Table("attachment").
			Join("INNER", "`repository`", "`attachment`.repo_id = `repository`.id")
	case "artifacts":
		session = session.
			Table("action_artifact").
			Join("INNER", "`repository`", "`action_artifact`.repo_id = `repository`.id").
			Where("`action_artifact`.status != ?", action_model.ArtifactStatusExpired)
	case "packages":
		session = session.
			Table("package_version").
			Join("INNER", "`package_file`", "`package_file`.version_id = `package_version`.id").
			Join("INNER", "`package_blob`", "`package_file`.blob_id = `package_blob`.id").
			Join("INNER", "`package`", "`package_version`.package_id = `package`.id").
			Join("LEFT OUTER", "`repository`", "`package`.repo_id = `repository`.id")
	}

	return session.Where(makeUserOwnedCondition(q, userID))
}

func GetQuotaAttachmentsForUser(ctx context.Context, userID int64, opts db.ListOptions) (int64, *[]*repo_model.Attachment, error) {
	var attachments []*repo_model.Attachment

	sess := createQueryFor(ctx, userID, "attachments").
		OrderBy("`attachment`.size DESC")
	if opts.PageSize > 0 {
		sess = sess.Limit(opts.PageSize, (opts.Page-1)*opts.PageSize)
	}
	count, err := sess.FindAndCount(&attachments)
	if err != nil {
		return 0, nil, err
	}

	return count, &attachments, nil
}

func GetQuotaPackagesForUser(ctx context.Context, userID int64, opts db.ListOptions) (int64, *[]*package_model.PackageVersion, error) {
	var pkgs []*package_model.PackageVersion

	sess := createQueryFor(ctx, userID, "packages").
		OrderBy("`package_blob`.size DESC")
	if opts.PageSize > 0 {
		sess = sess.Limit(opts.PageSize, (opts.Page-1)*opts.PageSize)
	}
	count, err := sess.FindAndCount(&pkgs)
	if err != nil {
		return 0, nil, err
	}

	return count, &pkgs, nil
}

func GetQuotaArtifactsForUser(ctx context.Context, userID int64, opts db.ListOptions) (int64, *[]*action_model.ActionArtifact, error) {
	var artifacts []*action_model.ActionArtifact

	sess := createQueryFor(ctx, userID, "artifacts").
		OrderBy("`action_artifact`.file_compressed_size DESC")
	if opts.PageSize > 0 {
		sess = sess.Limit(opts.PageSize, (opts.Page-1)*opts.PageSize)
	}
	count, err := sess.FindAndCount(&artifacts)
	if err != nil {
		return 0, nil, err
	}

	return count, &artifacts, nil
}

func GetUsedForUser(ctx context.Context, userID int64) (*Used, error) {
	var used Used

	_, err := createQueryFor(ctx, userID, "repositories").
		Where("`repository`.is_private = ?", true).
		Select("SUM(git_size) AS code").
		Get(&used.Size.Repos.Private)
	if err != nil {
		return nil, err
	}

	_, err = createQueryFor(ctx, userID, "repositories").
		Where("`repository`.is_private = ?", false).
		Select("SUM(git_size) AS code").
		Get(&used.Size.Repos.Public)
	if err != nil {
		return nil, err
	}

	_, err = createQueryFor(ctx, userID, "repositories").
		Select("SUM(lfs_size) AS lfs").
		Get(&used.Size.Git.LFS)
	if err != nil {
		return nil, err
	}

	_, err = createQueryFor(ctx, userID, "attachments").
		Select("SUM(`attachment`.size) AS size").
		Where("`attachment`.release_id != 0").
		Get(&used.Size.Assets.Attachments.Releases)
	if err != nil {
		return nil, err
	}

	_, err = createQueryFor(ctx, userID, "attachments").
		Select("SUM(`attachment`.size) AS size").
		Where("`attachment`.release_id = 0").
		Get(&used.Size.Assets.Attachments.Issues)
	if err != nil {
		return nil, err
	}

	_, err = createQueryFor(ctx, userID, "artifacts").
		Select("SUM(file_compressed_size) AS size").
		Get(&used.Size.Assets.Artifacts)
	if err != nil {
		return nil, err
	}

	_, err = createQueryFor(ctx, userID, "packages").
		Select("SUM(package_blob.size) AS size").
		Get(&used.Size.Assets.Packages.All)
	if err != nil {
		return nil, err
	}

	return &used, nil
}
