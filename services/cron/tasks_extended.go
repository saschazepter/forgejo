// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cron

import (
	"context"
	"time"

	activities_model "forgejo.org/models/activities"
	asymkey_model "forgejo.org/models/asymkey"
	"forgejo.org/models/system"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	issue_indexer "forgejo.org/modules/indexer/issues"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/updatechecker"
	moderation_service "forgejo.org/services/moderation"
	repo_service "forgejo.org/services/repository"
	archiver_service "forgejo.org/services/repository/archiver"
	user_service "forgejo.org/services/user"
)

func registerDeleteInactiveUsers() {
	RegisterTaskFatal("delete_inactive_accounts", &OlderThanConfig{
		BaseConfig: BaseConfig{
			Enabled:    false,
			RunAtStart: false,
			Schedule:   "@annually",
		},
		OlderThan: time.Minute * time.Duration(setting.Service.ActiveCodeLives),
	}, func(ctx context.Context, _ *user_model.User, config Config) error {
		olderThanConfig := config.(*OlderThanConfig)
		return user_service.DeleteInactiveUsers(ctx, olderThanConfig.OlderThan)
	})
}

func registerDeleteRepositoryArchives() {
	RegisterTaskFatal("delete_repo_archives", &BaseConfig{
		Enabled:    false,
		RunAtStart: false,
		Schedule:   "@annually",
	}, func(ctx context.Context, _ *user_model.User, _ Config) error {
		return archiver_service.DeleteRepositoryArchives(ctx)
	})
}

func registerGarbageCollectRepositories() {
	type RepoHealthCheckConfig struct {
		BaseConfig
		Timeout time.Duration
		Args    []string `delim:" "`
	}
	RegisterTaskFatal("git_gc_repos", &RepoHealthCheckConfig{
		BaseConfig: BaseConfig{
			Enabled:    false,
			RunAtStart: false,
			Schedule:   "@every 72h",
		},
		Timeout: time.Duration(setting.Git.Timeout.GC) * time.Second,
		Args:    setting.Git.GCArgs,
	}, func(ctx context.Context, _ *user_model.User, config Config) error {
		rhcConfig := config.(*RepoHealthCheckConfig)
		// the git args are set by config, they can be safe to be trusted
		return repo_service.GitGcRepos(ctx, rhcConfig.Timeout, git.ToTrustedCmdArgs(rhcConfig.Args))
	})
}

func registerRewriteAllPublicKeys() {
	RegisterTaskFatal("resync_all_sshkeys", &BaseConfig{
		Enabled:    false,
		RunAtStart: false,
		Schedule:   "@every 72h",
	}, func(ctx context.Context, _ *user_model.User, _ Config) error {
		return asymkey_model.RewriteAllPublicKeys(ctx)
	})
}

func registerRewriteAllPrincipalKeys() {
	RegisterTaskFatal("resync_all_sshprincipals", &BaseConfig{
		Enabled:    false,
		RunAtStart: false,
		Schedule:   "@every 72h",
	}, func(ctx context.Context, _ *user_model.User, _ Config) error {
		return asymkey_model.RewriteAllPrincipalKeys(ctx)
	})
}

func registerReinitMissingRepositories() {
	RegisterTaskFatal("reinit_missing_repos", &BaseConfig{
		Enabled:    false,
		RunAtStart: false,
		Schedule:   "@every 72h",
	}, func(ctx context.Context, _ *user_model.User, _ Config) error {
		return repo_service.ReinitMissingRepositories(ctx)
	})
}

func registerDeleteMissingRepositories() {
	RegisterTaskFatal("delete_missing_repos", &BaseConfig{
		Enabled:    false,
		RunAtStart: false,
		Schedule:   "@every 72h",
	}, func(ctx context.Context, _ *user_model.User, _ Config) error {
		return repo_service.DeleteMissingRepositories(ctx)
	})
}

func registerRemoveRandomAvatars() {
	RegisterTaskFatal("delete_generated_repository_avatars", &BaseConfig{
		Enabled:    false,
		RunAtStart: false,
		Schedule:   "@every 72h",
	}, func(ctx context.Context, _ *user_model.User, _ Config) error {
		return repo_service.RemoveRandomAvatars(ctx)
	})
}

func registerDeleteOldActions() {
	RegisterTaskFatal("delete_old_actions", &OlderThanConfig{
		BaseConfig: BaseConfig{
			Enabled:    false,
			RunAtStart: false,
			Schedule:   "@every 168h",
		},
		OlderThan: 365 * 24 * time.Hour,
	}, func(ctx context.Context, _ *user_model.User, config Config) error {
		olderThanConfig := config.(*OlderThanConfig)
		return activities_model.DeleteOldActions(ctx, olderThanConfig.OlderThan)
	})
}

func registerUpdateChecker() {
	type UpdateCheckerConfig struct {
		BaseConfig
		DomainEndpoint string
	}
	RegisterTaskFatal("update_checker", &UpdateCheckerConfig{
		BaseConfig: BaseConfig{
			Enabled:    true,
			RunAtStart: false,
			Schedule:   "@every 168h",
		},
		DomainEndpoint: "release.forgejo.org",
	}, func(ctx context.Context, _ *user_model.User, config Config) error {
		updateCheckerConfig := config.(*UpdateCheckerConfig)
		return updatechecker.UpdateChecker(updateCheckerConfig.DomainEndpoint)
	})
}

func registerDeleteOldSystemNotices() {
	RegisterTaskFatal("delete_old_system_notices", &OlderThanConfig{
		BaseConfig: BaseConfig{
			Enabled:    false,
			RunAtStart: false,
			Schedule:   "@every 168h",
		},
		OlderThan: 365 * 24 * time.Hour,
	}, func(ctx context.Context, _ *user_model.User, config Config) error {
		olderThanConfig := config.(*OlderThanConfig)
		return system.DeleteOldSystemNotices(ctx, olderThanConfig.OlderThan)
	})
}

type GCLFSConfig struct {
	BaseConfig
	OlderThan                time.Duration
	LastUpdatedMoreThanAgo   time.Duration
	NumberToCheckPerRepo     int64
	ProportionToCheckPerRepo float64
}

func registerGCLFS() {
	if !setting.LFS.StartServer {
		return
	}

	RegisterTaskFatal("gc_lfs", &GCLFSConfig{
		BaseConfig: BaseConfig{
			Enabled:    false,
			RunAtStart: false,
			Schedule:   "@every 24h",
		},
		// Only attempt to garbage collect lfs meta objects older than a week as the order of git lfs upload
		// and git object upload is not necessarily guaranteed. It's possible to imagine a situation whereby
		// an LFS object is uploaded but the git branch is not uploaded immediately, or there are some rapid
		// changes in new branches that might lead to lfs objects becoming temporarily unassociated with git
		// objects.
		//
		// It is likely that a week is potentially excessive but it should definitely be enough that any
		// unassociated LFS object is genuinely unassociated.
		OlderThan: 24 * time.Hour * 7,

		// Only GC things that haven't been looked at in the past 3 days
		LastUpdatedMoreThanAgo:   24 * time.Hour * 3,
		NumberToCheckPerRepo:     100,
		ProportionToCheckPerRepo: 0.6,
	}, func(ctx context.Context, _ *user_model.User, config Config) error {
		gcLFSConfig := config.(*GCLFSConfig)
		return repo_service.GarbageCollectLFSMetaObjects(ctx, repo_service.GarbageCollectLFSMetaObjectsOptions{
			AutoFix:                 true,
			OlderThan:               time.Now().Add(-gcLFSConfig.OlderThan),
			UpdatedLessRecentlyThan: time.Now().Add(-gcLFSConfig.LastUpdatedMoreThanAgo),
		})
	})
}

func registerRebuildIssueIndexer() {
	RegisterTaskFatal("rebuild_issue_indexer", &BaseConfig{
		Enabled:    false,
		RunAtStart: false,
		Schedule:   "@annually",
	}, func(ctx context.Context, _ *user_model.User, config Config) error {
		return issue_indexer.PopulateIssueIndexer(ctx)
	})
}

func registerRemoveResolvedReports() {
	type ReportConfig struct {
		BaseConfig
		ConfigKeepResolvedReportsFor time.Duration
	}
	RegisterTaskFatal("remove_resolved_reports", &ReportConfig{
		BaseConfig: BaseConfig{
			Enabled:    false,
			RunAtStart: false,
			Schedule:   "@every 24h",
		},
		ConfigKeepResolvedReportsFor: setting.Moderation.KeepResolvedReportsFor,
	}, func(ctx context.Context, _ *user_model.User, config Config) error {
		reportConfig := config.(*ReportConfig)
		return moderation_service.RemoveResolvedReports(ctx, reportConfig.ConfigKeepResolvedReportsFor)
	})
}

func initExtendedTasks() {
	registerDeleteInactiveUsers()
	registerDeleteRepositoryArchives()
	registerGarbageCollectRepositories()
	registerRewriteAllPublicKeys()
	registerRewriteAllPrincipalKeys()
	registerReinitMissingRepositories()
	registerDeleteMissingRepositories()
	registerRemoveRandomAvatars()
	registerDeleteOldActions()
	registerUpdateChecker()
	registerDeleteOldSystemNotices()
	registerGCLFS()
	registerRebuildIssueIndexer()
	if setting.Moderation.Enabled {
		registerRemoveResolvedReports()
	}
}
