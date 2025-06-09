// Copyright 2023 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package forgejo_migrations //nolint:revive

import (
	"context"
	"errors"
	"fmt"
	"os"

	"forgejo.org/models/forgejo/semver"
	forgejo_v1_20 "forgejo.org/models/forgejo_migrations/v1_20"
	forgejo_v1_22 "forgejo.org/models/forgejo_migrations/v1_22"
	"forgejo.org/modules/git"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"

	"xorm.io/xorm"
	"xorm.io/xorm/names"
)

// ForgejoVersion describes the Forgejo version table. Should have only one row with id = 1.
type ForgejoVersion struct {
	ID      int64 `xorm:"pk autoincr"`
	Version int64
}

type Migration struct {
	description string
	migrate     func(*xorm.Engine) error
}

// NewMigration creates a new migration.
func NewMigration(desc string, fn func(*xorm.Engine) error) *Migration {
	return &Migration{desc, fn}
}

// This is a sequence of additional Forgejo migrations.
// Add new migrations to the bottom of the list.
var migrations = []*Migration{
	// v0 -> v1
	NewMigration("Create the `forgejo_blocked_user` table", forgejo_v1_20.AddForgejoBlockedUser),
	// v1 -> v2
	NewMigration("Create the `forgejo_sem_ver` table", forgejo_v1_20.CreateSemVerTable),
	// v2 -> v3
	NewMigration("Create the `forgejo_auth_token` table", forgejo_v1_20.CreateAuthorizationTokenTable),
	// v3 -> v4
	NewMigration("Add the `default_permissions` column to the `repo_unit` table", forgejo_v1_22.AddDefaultPermissionsToRepoUnit),
	// v4 -> v5
	NewMigration("Create the `forgejo_repo_flag` table", forgejo_v1_22.CreateRepoFlagTable),
	// v5 -> v6
	NewMigration("Add the `wiki_branch` column to the `repository` table", forgejo_v1_22.AddWikiBranchToRepository),
	// v6 -> v7
	NewMigration("Add the `enable_repo_unit_hints` column to the `user` table", forgejo_v1_22.AddUserRepoUnitHintsSetting),
	// v7 -> v8
	NewMigration("Modify the `release`.`note` content to remove SSH signatures", forgejo_v1_22.RemoveSSHSignaturesFromReleaseNotes),
	// v8 -> v9
	NewMigration("Add the `apply_to_admins` column to the `protected_branch` table", forgejo_v1_22.AddApplyToAdminsSetting),
	// v9 -> v10
	NewMigration("Add pronouns to user", forgejo_v1_22.AddPronounsToUser),
	// v10 -> v11
	NewMigration("Add the `created` column to the `issue` table", forgejo_v1_22.AddCreatedToIssue),
	// v11 -> v12
	NewMigration("Add repo_archive_download_count table", forgejo_v1_22.AddRepoArchiveDownloadCount),
	// v12 -> v13
	NewMigration("Add `hide_archive_links` column to `release` table", AddHideArchiveLinksToRelease),
	// v13 -> v14
	NewMigration("Remove Gitea-specific columns from the repository and badge tables", RemoveGiteaSpecificColumnsFromRepositoryAndBadge),
	// v14 -> v15
	NewMigration("Create the `federation_host` table", CreateFederationHostTable),
	// v15 -> v16
	NewMigration("Create the `federated_user` table", CreateFederatedUserTable),
	// v16 -> v17
	NewMigration("Add `normalized_federated_uri` column to `user` table", AddNormalizedFederatedURIToUser),
	// v17 -> v18
	NewMigration("Create the `following_repo` table", CreateFollowingRepoTable),
	// v18 -> v19
	NewMigration("Add external_url to attachment table", AddExternalURLColumnToAttachmentTable),
	// v19 -> v20
	NewMigration("Creating Quota-related tables", CreateQuotaTables),
	// v20 -> v21
	NewMigration("Add SSH keypair to `pull_mirror` table", AddSSHKeypairToPushMirror),
	// v21 -> v22
	NewMigration("Add `legacy` to `web_authn_credential` table", AddLegacyToWebAuthnCredential),
	// v22 -> v23
	NewMigration("Add `delete_branch_after_merge` to `auto_merge` table", AddDeleteBranchAfterMergeToAutoMerge),
	// v23 -> v24
	NewMigration("Add `purpose` column to `forgejo_auth_token` table", AddPurposeToForgejoAuthToken),
	// v24 -> v25
	NewMigration("Migrate `secret` column to store keying material", MigrateTwoFactorToKeying),
	// v25 -> v26
	NewMigration("Add `hash_blake2b` column to `package_blob` table", AddHashBlake2bToPackageBlob),
	// v26 -> v27
	NewMigration("Add `created_unix` column to `user_redirect` table", AddCreatedUnixToRedirect),
	// v27 -> v28
	NewMigration("Add pronoun privacy settings to user", AddHidePronounsOptionToUser),
	// v28 -> v29
	NewMigration("Add public key information to `FederatedUser` and `FederationHost`", AddPublicKeyInformationForFederation),
	// v29 -> v30
	NewMigration("Migrate `User.NormalizedFederatedURI` column to extract port & schema into FederatedHost", MigrateNormalizedFederatedURI),
	// v30 -> v31
	NewMigration("Normalize repository.topics to empty slice instead of null", SetTopicsAsEmptySlice),
	// v31 -> v32
	NewMigration("Migrate maven package name concatenation", ChangeMavenArtifactConcatenation),
	// v32 -> v33
	NewMigration("Add is_code_indexer_enabled to repository", AddIsCodeIndexerEnabledToRepository),
}

// GetCurrentDBVersion returns the current Forgejo database version.
func GetCurrentDBVersion(x *xorm.Engine) (int64, error) {
	if err := x.Sync(new(ForgejoVersion)); err != nil {
		return -1, fmt.Errorf("sync: %w", err)
	}

	currentVersion := &ForgejoVersion{ID: 1}
	has, err := x.Get(currentVersion)
	if err != nil {
		return -1, fmt.Errorf("get: %w", err)
	}
	if !has {
		return -1, nil
	}
	return currentVersion.Version, nil
}

// ExpectedVersion returns the expected Forgejo database version.
func ExpectedVersion() int64 {
	return int64(len(migrations))
}

// EnsureUpToDate will check if the Forgejo database is at the correct version.
func EnsureUpToDate(x *xorm.Engine) error {
	currentDB, err := GetCurrentDBVersion(x)
	if err != nil {
		return err
	}

	if currentDB < 0 {
		return errors.New("database has not been initialized")
	}

	expected := ExpectedVersion()

	if currentDB != expected {
		return fmt.Errorf(`current Forgejo database version %d is not equal to the expected version %d. Please run "forgejo [--config /path/to/app.ini] migrate" to update the database version`, currentDB, expected)
	}

	return nil
}

// Migrate Forgejo database to current version.
func Migrate(x *xorm.Engine) error {
	// Set a new clean the default mapper to GonicMapper as that is the default for .
	x.SetMapper(names.GonicMapper{})
	if err := x.Sync(new(ForgejoVersion)); err != nil {
		return fmt.Errorf("sync: %w", err)
	}

	currentVersion := &ForgejoVersion{ID: 1}
	has, err := x.Get(currentVersion)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	} else if !has {
		// If the version record does not exist we think
		// it is a fresh installation and we can skip all migrations.
		currentVersion.ID = 0
		currentVersion.Version = ExpectedVersion()

		if _, err = x.InsertOne(currentVersion); err != nil {
			return fmt.Errorf("insert: %w", err)
		}
	}

	v := currentVersion.Version

	// Downgrading Forgejo's database version not supported
	if v > ExpectedVersion() {
		msg := fmt.Sprintf("Your Forgejo database (migration version: %d) is for a newer version of Forgejo, you cannot use the newer database for this old Forgejo release (%d).", v, ExpectedVersion())
		msg += "\nForgejo will exit to keep your database safe and unchanged. Please use the correct Forgejo release, do not change the migration version manually (incorrect manual operation may cause data loss)."
		if !setting.IsProd {
			msg += fmt.Sprintf("\nIf you are in development and really know what you're doing, you can force changing the migration version by executing: UPDATE forgejo_version SET version=%d WHERE id=1;", ExpectedVersion())
		}
		_, _ = fmt.Fprintln(os.Stderr, msg)
		log.Fatal(msg)
		return nil
	}

	// Some migration tasks depend on the git command
	if git.DefaultContext == nil {
		if err = git.InitSimple(context.Background()); err != nil {
			return err
		}
	}

	// Migrate
	for i, m := range migrations[v:] {
		log.Info("Migration[%d]: %s", v+int64(i), m.description)
		// Reset the mapper between each migration - migrations are not supposed to depend on each other
		x.SetMapper(names.GonicMapper{})
		if err = m.migrate(x); err != nil {
			return fmt.Errorf("migration[%d]: %s failed: %w", v+int64(i), m.description, err)
		}
		currentVersion.Version = v + int64(i) + 1
		if _, err = x.ID(1).Update(currentVersion); err != nil {
			return err
		}
	}

	if err := x.Sync(new(semver.ForgejoSemVer)); err != nil {
		return fmt.Errorf("sync: %w", err)
	}

	return semver.SetVersionStringWithEngine(x, setting.ForgejoVersion)
}
