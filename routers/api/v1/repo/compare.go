// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"net/http"
	"strings"

	user_model "forgejo.org/models/user"
	api "forgejo.org/modules/structs"
	"forgejo.org/services/context"
	"forgejo.org/services/convert"
)

// CompareDiff compare two branches or commits
func CompareDiff(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/compare/{basehead} repository repoCompareDiff
	// ---
	// summary: Get commit comparison information
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repo
	//   type: string
	//   required: true
	// - name: basehead
	//   in: path
	//   description: compare two branches or commits
	//   type: string
	//   required: true
	// - name: verification
	//   in: query
	//   description: include verification status for each commit
	//   type: boolean
	//   default: true
	// - name: files
	//   in: query
	//   description: include which files changed by a commit
	//   type: boolean
	//   default: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/Compare"
	//   "404":
	//     "$ref": "#/responses/notFound"

	infoPath := ctx.Params("*")
	infos := [2]string{ctx.Repo().Repository.DefaultBranch, ctx.Repo().Repository.DefaultBranch}
	if base, head, ok := strings.Cut(infoPath, "..."); ok {
		infos[0], infos[1] = base, head
	} else if base, head, ok := strings.Cut(infoPath, ".."); ok {
		infos[0], infos[1] = base, head
	} else if infoPath != "" {
		infos[1] = infoPath
	}

	headRepository, ci, _, _ := parseCompareInfo(ctx, api.CreatePullRequestOption{
		Base: infos[0],
		Head: infos[1],
	})
	if ctx.Written() {
		return
	}
	defer ci.Close()

	verification := ctx.FormString("verification") == "" || ctx.FormBool("verification")
	files := ctx.FormString("files") == "" || ctx.FormBool("files")

	apiCommits := make([]*api.Commit, 0, len(ci.Commits))
	apiFiles := []*api.CommitAffectedFiles{}
	userCache := make(map[string]*user_model.User)
	for i := 0; i < len(ci.Commits); i++ {
		apiCommit, err := convert.ToCommit(ctx, headRepository, ci.CompareRepo(), ci.Commits[i], userCache,
			convert.ToCommitOptions{
				Stat:         true,
				Verification: verification,
				Files:        files,
			})
		if err != nil {
			ctx.ServerError("toCommit", err)
			return
		}
		apiCommits = append(apiCommits, apiCommit)
		apiFiles = append(apiFiles, apiCommit.Files...)
	}

	ctx.JSON(http.StatusOK, &api.Compare{
		TotalCommits: len(ci.Commits),
		Commits:      apiCommits,
		Files:        apiFiles,
	})
}
