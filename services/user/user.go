// Copyright 2024 The Forgejo Authors. All rights reserved.
// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package user

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"forgejo.org/models"
	asymkey_model "forgejo.org/models/asymkey"
	"forgejo.org/models/auth"
	"forgejo.org/models/db"
	"forgejo.org/models/organization"
	packages_model "forgejo.org/models/packages"
	repo_model "forgejo.org/models/repo"
	system_model "forgejo.org/models/system"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/eventsource"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/storage"
	"forgejo.org/modules/util"
	"forgejo.org/services/agit"
	"forgejo.org/services/auth/source/oauth2"
	org_service "forgejo.org/services/org"
	"forgejo.org/services/packages"
	container_service "forgejo.org/services/packages/container"
	repo_service "forgejo.org/services/repository"
)

// RenameUser renames a user
func RenameUser(ctx context.Context, u *user_model.User, newUserName string) error {
	return renameUser(ctx, u, newUserName, false)
}

// AdminRenameUser renames a user as an admin.
func AdminRenameUser(ctx context.Context, u *user_model.User, newUserName string) error {
	return renameUser(ctx, u, newUserName, true)
}

// CanUserRename returns if the given user can be renamed.
//
// This is merely a precondition, you likely want to use [RenameUser] or [AdminRenameUser]
// which also takes into consideration username cooldown of a new username.
func CanUserRename(ctx context.Context, user *user_model.User) (bool, error) {
	// Non-local users are not allowed to change their username.
	// Organizations can always be renamed.
	if user.IsOrganization() || user.IsLocal() {
		return true, nil
	}

	// If the user's authentication source is OAuth2 and that source allows for
	// username changes then don't make a fuzz about it.
	if !user.IsOAuth2() {
		return false, nil
	}

	source, err := auth.GetSourceByID(ctx, user.LoginSource)
	if err != nil {
		return false, err
	}
	return source.Cfg.(*oauth2.Source).AllowUsernameChange, nil
}

func renameUser(ctx context.Context, u *user_model.User, newUserName string, doerIsAdmin bool) error {
	if newUserName == u.Name {
		return nil
	}

	// If the doer is an admin, then allow the rename - they know better.
	if !doerIsAdmin {
		canRenamed, err := CanUserRename(ctx, u)
		if err != nil {
			return err
		}
		if !canRenamed {
			return user_model.ErrUserIsNotLocal{
				UID:  u.ID,
				Name: u.Name,
			}
		}
	}

	if err := user_model.IsUsableUsername(newUserName); err != nil {
		return err
	}

	// Check if the new username can be claimed.
	if !doerIsAdmin {
		if ok, expireTime, err := user_model.CanClaimUsername(ctx, newUserName, u.ID); err != nil {
			return err
		} else if !ok {
			return user_model.ErrCooldownPeriod{
				ExpireTime: expireTime,
			}
		}
	}

	onlyCapitalization := strings.EqualFold(newUserName, u.Name)
	oldUserName := u.Name

	if onlyCapitalization {
		u.Name = newUserName
		if err := user_model.UpdateUserCols(ctx, u, "name"); err != nil {
			u.Name = oldUserName
			return err
		}
		return repo_model.UpdateRepositoryOwnerNames(ctx, u.ID, newUserName)
	}

	ctx, committer, err := db.TxContext(ctx)
	if err != nil {
		return err
	}
	defer committer.Close()

	isExist, err := user_model.IsUserExist(ctx, u.ID, newUserName)
	if err != nil {
		return err
	}
	if isExist {
		return user_model.ErrUserAlreadyExist{
			Name: newUserName,
		}
	}

	if err = repo_model.UpdateRepositoryOwnerName(ctx, oldUserName, newUserName); err != nil {
		return err
	}

	if err = user_model.NewUserRedirect(ctx, u.ID, oldUserName, newUserName); err != nil {
		return err
	}

	if setting.Service.MaxUserRedirects > 0 {
		if err := user_model.LimitUserRedirects(ctx, u.ID, setting.Service.MaxUserRedirects); err != nil {
			return err
		}
	}

	if err := agit.UserNameChanged(ctx, u, newUserName); err != nil {
		return err
	}
	if err := container_service.UpdateRepositoryNames(ctx, u, newUserName); err != nil {
		return err
	}

	u.Name = newUserName
	u.LowerName = strings.ToLower(newUserName)
	if err := user_model.UpdateUserCols(ctx, u, "name", "lower_name"); err != nil {
		u.Name = oldUserName
		u.LowerName = strings.ToLower(oldUserName)
		return err
	}

	// Do not fail if directory does not exist
	if err = util.Rename(user_model.UserPath(oldUserName), user_model.UserPath(newUserName)); err != nil && !os.IsNotExist(err) {
		u.Name = oldUserName
		u.LowerName = strings.ToLower(oldUserName)
		return fmt.Errorf("rename user directory: %w", err)
	}

	if err = committer.Commit(); err != nil {
		u.Name = oldUserName
		u.LowerName = strings.ToLower(oldUserName)
		if err2 := util.Rename(user_model.UserPath(newUserName), user_model.UserPath(oldUserName)); err2 != nil && !os.IsNotExist(err2) {
			log.Critical("Unable to rollback directory change during failed username change from: %s to: %s. DB Error: %v. Filesystem Error: %v", oldUserName, newUserName, err, err2)
			return fmt.Errorf("failed to rollback directory change during failed username change from: %s to: %s. DB Error: %w. Filesystem Error: %v", oldUserName, newUserName, err, err2)
		}
		return err
	}
	return nil
}

// DeleteUser completely and permanently deletes everything of a user,
// but issues/comments/pulls will be kept and shown as someone has been deleted,
// unless the user is younger than USER_DELETE_WITH_COMMENTS_MAX_DAYS.
func DeleteUser(ctx context.Context, u *user_model.User, purge bool) error {
	if u.IsOrganization() {
		return fmt.Errorf("%s is an organization not a user", u.Name)
	}

	if user_model.IsLastAdminUser(ctx, u) {
		return models.ErrDeleteLastAdminUser{UID: u.ID}
	}

	hasSSHKey, err := db.GetEngine(ctx).Where("owner_id = ? AND type != ?", u.ID, asymkey_model.KeyTypePrincipal).Table("public_key").Exist()
	if err != nil {
		return err
	}

	hasPrincipalSSHKey, err := db.GetEngine(ctx).Where("owner_id = ? AND type = ?", u.ID, asymkey_model.KeyTypePrincipal).Table("public_key").Exist()
	if err != nil {
		return err
	}

	if purge {
		// Disable the user first
		// NOTE: This is deliberately not within a transaction as it must disable the user immediately to prevent any further action by the user to be purged.
		if err := user_model.UpdateUserCols(ctx, &user_model.User{
			ID:              u.ID,
			IsActive:        false,
			IsRestricted:    true,
			IsAdmin:         false,
			ProhibitLogin:   true,
			Passwd:          "",
			Salt:            "",
			PasswdHashAlgo:  "",
			MaxRepoCreation: 0,
		}, "is_active", "is_restricted", "is_admin", "prohibit_login", "max_repo_creation", "passwd", "salt", "passwd_hash_algo"); err != nil {
			return fmt.Errorf("unable to disable user: %s[%d] prior to purge. UpdateUserCols: %w", u.Name, u.ID, err)
		}

		// Force any logged in sessions to log out
		// FIXME: We also need to tell the session manager to log them out too.
		eventsource.GetManager().SendMessage(u.ID, &eventsource.Event{
			Name: "logout",
		})

		// Delete all repos belonging to this user
		// Now this is not within a transaction because there are internal transactions within the DeleteRepository
		// BUT: the db will still be consistent even if a number of repos have already been deleted.
		// And in fact we want to capture any repositories that are being created in other transactions in the meantime
		//
		// An alternative option here would be write a DeleteAllRepositoriesForUserID function which would delete all of the repos
		// but such a function would likely get out of date
		err := repo_service.DeleteOwnerRepositoriesDirectly(ctx, u)
		if err != nil {
			return err
		}

		// Remove from Organizations and delete last owner organizations
		// Now this is not within a transaction because there are internal transactions within the DeleteOrganization
		// BUT: the db will still be consistent even if a number of organizations memberships and organizations have already been deleted
		// And in fact we want to capture any organization additions that are being created in other transactions in the meantime
		//
		// An alternative option here would be write a function which would delete all organizations but it seems
		// but such a function would likely get out of date
		for {
			orgs, err := db.Find[organization.Organization](ctx, organization.FindOrgOptions{
				ListOptions: db.ListOptions{
					PageSize: repo_model.RepositoryListDefaultPageSize,
					Page:     1,
				},
				UserID:         u.ID,
				IncludePrivate: true,
			})
			if err != nil {
				return fmt.Errorf("unable to find org list for %s[%d]. Error: %w", u.Name, u.ID, err)
			}
			if len(orgs) == 0 {
				break
			}
			for _, org := range orgs {
				if err := org_service.RemoveOrgUser(ctx, org.ID, u.ID); err != nil {
					if organization.IsErrLastOrgOwner(err) {
						err = org_service.DeleteOrganization(ctx, org, true)
						if err != nil {
							return fmt.Errorf("unable to delete organization %d: %w", org.ID, err)
						}
					}
					if err != nil {
						return fmt.Errorf("unable to remove user %s[%d] from org %s[%d]. Error: %w", u.Name, u.ID, org.Name, org.ID, err)
					}
				}
			}
		}

		// Delete Packages
		if setting.Packages.Enabled {
			if _, err := packages.RemoveAllPackages(ctx, u.ID); err != nil {
				return err
			}
		}

		// Delete Federated Users
		if setting.Federation.Enabled {
			if err := user_model.DeleteFederatedUser(ctx, u.ID); err != nil {
				return err
			}
		}
	}

	ctx, committer, err := db.TxContext(ctx)
	if err != nil {
		return err
	}
	defer committer.Close()

	// Note: A user owns any repository or belongs to any organization
	//	cannot perform delete operation. This causes a race with the purge above
	//  however consistency requires that we ensure that this is the case

	// Check ownership of repository.
	count, err := repo_model.CountRepositories(ctx, repo_model.CountRepositoryOptions{OwnerID: u.ID})
	if err != nil {
		return fmt.Errorf("GetRepositoryCount: %w", err)
	} else if count > 0 {
		return models.ErrUserOwnRepos{UID: u.ID}
	}

	// Check membership of organization.
	count, err = organization.GetOrganizationCount(ctx, u)
	if err != nil {
		return fmt.Errorf("GetOrganizationCount: %w", err)
	} else if count > 0 {
		return models.ErrUserHasOrgs{UID: u.ID}
	}

	// Check ownership of packages.
	if ownsPackages, err := packages_model.HasOwnerPackages(ctx, u.ID); err != nil {
		return fmt.Errorf("HasOwnerPackages: %w", err)
	} else if ownsPackages {
		return models.ErrUserOwnPackages{UID: u.ID}
	}

	if err := deleteUser(ctx, u, purge); err != nil {
		return fmt.Errorf("DeleteUser: %w", err)
	}

	if err := committer.Commit(); err != nil {
		return err
	}
	committer.Close()

	if hasSSHKey {
		if err = asymkey_model.RewriteAllPublicKeys(ctx); err != nil {
			return err
		}
	}

	if hasPrincipalSSHKey {
		if err = asymkey_model.RewriteAllPrincipalKeys(ctx); err != nil {
			return err
		}
	}

	// Note: There are something just cannot be roll back,
	//	so just keep error logs of those operations.
	path := user_model.UserPath(u.Name)
	if err := util.RemoveAll(path); err != nil {
		err = fmt.Errorf("Failed to RemoveAll %s: %w", path, err)
		_ = system_model.CreateNotice(ctx, system_model.NoticeTask, fmt.Sprintf("delete user '%s': %v", u.Name, err))
		return err
	}

	if u.Avatar != "" {
		avatarPath := u.CustomAvatarRelativePath()
		if err := storage.Avatars.Delete(avatarPath); err != nil {
			err = fmt.Errorf("Failed to remove %s: %w", avatarPath, err)
			_ = system_model.CreateNotice(ctx, system_model.NoticeTask, fmt.Sprintf("delete user '%s': %v", u.Name, err))
			return err
		}
	}

	return nil
}

// DeleteInactiveUsers deletes all inactive users and email addresses.
func DeleteInactiveUsers(ctx context.Context, olderThan time.Duration) error {
	users, err := user_model.GetInactiveUsers(ctx, olderThan)
	if err != nil {
		return err
	}

	// FIXME: should only update authorized_keys file once after all deletions.
	for _, u := range users {
		select {
		case <-ctx.Done():
			return db.ErrCancelledf("Before delete inactive user %s", u.Name)
		default:
		}
		if err := DeleteUser(ctx, u, false); err != nil {
			// Ignore users that were set inactive by admin.
			if models.IsErrUserOwnRepos(err) || models.IsErrUserHasOrgs(err) ||
				models.IsErrUserOwnPackages(err) || models.IsErrDeleteLastAdminUser(err) {
				log.Warn("Inactive user %q has repositories, organizations or packages, skipping deletion: %v", u.Name, err)
				continue
			}
			return err
		}
	}

	return nil
}
