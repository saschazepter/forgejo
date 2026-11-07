// Copyright 2022 The Gitea Authors. All rights reserved.
// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"io"
	"net/http"
	"path"

	"forgejo.org/modules/charset"
	"forgejo.org/modules/git"
	lfs_reader "forgejo.org/modules/lfs/reader"
	"forgejo.org/modules/log"
	"forgejo.org/modules/markup"
	"forgejo.org/services/context"
)

// RenderFile renders a file by repos path
func RenderFile(ctx *context.Context) {
	blob, err := ctx.Repo.Commit.GetBlobByPath(ctx.Repo.TreePath)
	if err != nil {
		if git.IsErrNotExist(err) {
			ctx.NotFound("GetBlobByPath", err)
		} else {
			ctx.ServerError("GetBlobByPath", err)
		}
		return
	}

	dataRc, fInfo, err := lfs_reader.OpenReaderFromMaybeLFSBlob(ctx, ctx.Repo.Repository.ID, blob)
	if err != nil {
		ctx.ServerError("OpenReaderFromMaybeLFSBlob", err)
		return
	}
	defer dataRc.Close()

	rd := charset.ToUTF8WithFallbackReader(dataRc, charset.ConvertOpts{})
	ctx.Resp.Header().Add("Content-Security-Policy", "frame-src 'self'; sandbox allow-scripts")

	if markupType := markup.Type(blob.Name()); markupType == "" {
		if fInfo.SniffedType.IsText() {
			_, _ = io.Copy(ctx.Resp, rd)
		} else {
			http.Error(ctx.Resp, "Unsupported file type render", http.StatusInternalServerError)
		}
		return
	}

	err = markup.Render(&markup.RenderContext{
		Ctx:          ctx,
		RelativePath: ctx.Repo.TreePath,
		Links: markup.Links{
			Base:       ctx.Repo.RepoLink,
			BranchPath: ctx.Repo.BranchNameSubURL(),
			TreePath:   path.Dir(ctx.Repo.TreePath),
		},
		Metas:            ctx.Repo.Repository.ComposeDocumentMetas(ctx),
		GitRepo:          ctx.Repo.GitRepo,
		InStandalonePage: true,
	}, rd, ctx.Resp)
	if err != nil {
		log.Error("Failed to render file %q: %v", ctx.Repo.TreePath, err)
		http.Error(ctx.Resp, "Failed to render file", http.StatusInternalServerError)
		return
	}
}
