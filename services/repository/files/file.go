// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package files

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/util"
)

func GetFilesResponseFromCommit(ctx context.Context, repo *repo_model.Repository, commit *git.Commit, branch string, treeNames []string) (*api.FilesResponse, error) {
	files := []*api.ContentsResponse{}
	for _, file := range treeNames {
		fileContents, _ := GetContents(ctx, repo, file, branch, false) // ok if fails, then will be nil
		files = append(files, fileContents)
	}
	fileCommitResponse, _ := GetFileCommitResponse(repo, commit) // ok if fails, then will be nil
	verification := GetPayloadCommitVerification(ctx, commit)
	filesResponse := &api.FilesResponse{
		Files:        files,
		Commit:       fileCommitResponse,
		Verification: verification,
	}
	return filesResponse, nil
}

// constructs a FileResponse with the file at the index from FilesResponse
func GetFileResponseFromFilesResponse(filesResponse *api.FilesResponse, index int) *api.FileResponse {
	content := &api.ContentsResponse{}
	if len(filesResponse.Files) > index {
		content = filesResponse.Files[index]
	}
	fileResponse := &api.FileResponse{
		Content:      content,
		Commit:       filesResponse.Commit,
		Verification: filesResponse.Verification,
	}
	return fileResponse
}

// GetFileCommitResponse Constructs a FileCommitResponse from a Commit object
func GetFileCommitResponse(repo *repo_model.Repository, commit *git.Commit) (*api.FileCommitResponse, error) {
	if repo == nil {
		return nil, errors.New("repo cannot be nil")
	}
	if commit == nil {
		return nil, errors.New("commit cannot be nil")
	}
	commitURL, _ := url.Parse(repo.APIURL() + "/git/commits/" + url.PathEscape(commit.ID.String()))
	commitTreeURL, _ := url.Parse(repo.APIURL() + "/git/trees/" + url.PathEscape(commit.Tree.ID.String()))
	parents := make([]*api.CommitMeta, commit.ParentCount())
	for i := 0; i <= commit.ParentCount(); i++ {
		if parent, err := commit.Parent(i); err == nil && parent != nil {
			parentCommitURL, _ := url.Parse(repo.APIURL() + "/git/commits/" + url.PathEscape(parent.ID.String()))
			parents[i] = &api.CommitMeta{
				SHA: parent.ID.String(),
				URL: parentCommitURL.String(),
			}
		}
	}
	commitHTMLURL, _ := url.Parse(repo.HTMLURL() + "/commit/" + url.PathEscape(commit.ID.String()))
	fileCommit := &api.FileCommitResponse{
		CommitMeta: api.CommitMeta{
			SHA: commit.ID.String(),
			URL: commitURL.String(),
		},
		HTMLURL: commitHTMLURL.String(),
		Author: &api.CommitUser{
			Identity: api.Identity{
				Name:  commit.Author.Name,
				Email: commit.Author.Email,
			},
			Date: commit.Author.When.UTC().Format(time.RFC3339),
		},
		Committer: &api.CommitUser{
			Identity: api.Identity{
				Name:  commit.Committer.Name,
				Email: commit.Committer.Email,
			},
			Date: commit.Committer.When.UTC().Format(time.RFC3339),
		},
		Message: commit.Message(),
		Tree: &api.CommitMeta{
			URL: commitTreeURL.String(),
			SHA: commit.Tree.ID.String(),
		},
		Parents: parents,
	}
	return fileCommit, nil
}

// GetAuthorAndCommitterUsers Gets the author and committer user objects from the IdentityOptions
func GetAuthorAndCommitterUsers(author, committer *IdentityOptions, doer *user_model.User) (authorUser, committerUser *user_model.User) {
	// Committer and author are optional. If they are not the doer (not same email address)
	// then we use bogus User objects for them to store their FullName and Email.
	// If only one of the two are provided, we set both of them to it.
	// If neither are provided, both are the doer.
	if committer != nil && committer.Email != "" {
		if doer != nil && strings.EqualFold(doer.Email, committer.Email) {
			committerUser = doer // the committer is the doer, so will use their user object
			if committer.Name != "" {
				committerUser.FullName = committer.Name
			}
			// Use the provided email and not revert to placeholder mail.
			committerUser.KeepEmailPrivate = false
		} else {
			committerUser = &user_model.User{
				FullName: committer.Name,
				Email:    committer.Email,
			}
		}
	}
	if author != nil && author.Email != "" {
		if doer != nil && strings.EqualFold(doer.Email, author.Email) {
			authorUser = doer // the author is the doer, so will use their user object
			if authorUser.Name != "" {
				authorUser.FullName = author.Name
			}
			// Use the provided email and not revert to placeholder mail.
			authorUser.KeepEmailPrivate = false
		} else {
			authorUser = &user_model.User{
				FullName: author.Name,
				Email:    author.Email,
			}
		}
	}
	if authorUser == nil {
		if committerUser != nil {
			authorUser = committerUser // No valid author was given so use the committer
		} else if doer != nil {
			authorUser = doer // No valid author was given and no valid committer so use the doer
		}
	}
	if committerUser == nil {
		committerUser = authorUser // No valid committer so use the author as the committer (was set to a valid user above)
	}
	return authorUser, committerUser
}

// CleanUploadFileName Trims a filename and returns empty string if it is a .git directory
func CleanUploadFileName(name string) string {
	// Rebase the filename
	name = util.PathJoinRel(name)
	// Git disallows any filenames to have a .git directory in them.
	for _, part := range strings.Split(name, "/") {
		if strings.ToLower(part) == ".git" {
			return ""
		}
	}
	return name
}
