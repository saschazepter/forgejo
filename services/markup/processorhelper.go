// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package markup

import (
	"context"
	"errors"

	"forgejo.org/models/perm/access"
	"forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/log"
	"forgejo.org/modules/markup"
	gitea_context "forgejo.org/services/context"
	file_service "forgejo.org/services/repository/files"
)

func ProcessorHelper() *markup.ProcessorHelper {
	return &markup.ProcessorHelper{
		ElementDir: "auto", // set dir="auto" for necessary (eg: <p>, <h?>, etc) tags
		IsUsernameMentionable: func(ctx context.Context, username string) bool {
			mentionedUser, err := user.GetUserByName(ctx, username)
			if err != nil {
				return false
			}

			giteaCtx, ok := ctx.(*gitea_context.Context)
			if !ok {
				// when using general context, use user's visibility to check
				return mentionedUser.Visibility.IsPublic()
			}

			// when using gitea context (web context), use user's visibility and user's permission to check
			return user.IsUserVisibleToViewer(giteaCtx, mentionedUser, giteaCtx.Doer)
		},
		GetRepoFileBlob: func(ctx context.Context, ownerName, repoName, commitSha, filePath string, language *string) (*git.Blob, error) {
			repo, err := repo.GetRepositoryByOwnerAndName(ctx, ownerName, repoName)
			if err != nil {
				return nil, err
			}

			var user *user.User

			giteaCtx, ok := ctx.(*gitea_context.Context)
			if ok {
				user = giteaCtx.Doer
			}

			perms, err := access.GetUserRepoPermission(ctx, repo, user)
			if err != nil {
				return nil, err
			}
			if !perms.CanRead(unit.TypeCode) {
				return nil, errors.New("cannot access repository code")
			}

			gitRepo, err := gitrepo.OpenRepository(ctx, repo)
			if err != nil {
				return nil, err
			}
			defer gitRepo.Close()

			commit, err := gitRepo.GetCommit(commitSha)
			if err != nil {
				return nil, err
			}

			if language != nil {
				*language, err = file_service.TryGetContentLanguage(gitRepo, commitSha, filePath)
				if err != nil {
					log.Error("Unable to get file language for %-v:%s. Error: %v", repo, filePath, err)
				}
			}

			blob, err := commit.GetBlobByPath(filePath)
			if err != nil {
				return nil, err
			}

			return blob, nil
		},
	}
}
