// Copyright 2019 The Gitea Authors. All rights reserved.
// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"fmt"
	gotemplate "html/template"
	"net/http"
	"net/url"
	"strings"

	user_model "forgejo.org/models/user"
	"forgejo.org/modules/charset"
	"forgejo.org/modules/git"
	"forgejo.org/modules/highlight"
	lfs_reader "forgejo.org/modules/lfs/reader"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/templates"
	"forgejo.org/modules/util"
	"forgejo.org/services/context"
	files_service "forgejo.org/services/repository/files"
)

type blameRow struct {
	RowNumber      int
	Avatar         gotemplate.HTML
	RepoLink       string
	PartSha        string
	PreviousSha    string
	PreviousShaURL string
	IsFirstCommit  bool
	CommitURL      string
	CommitMessage  string
	CommitSince    gotemplate.HTML
	Code           gotemplate.HTML
	EscapeStatus   *charset.EscapeStatus
}

// RefBlame render blame page
func RefBlame(ctx *context.Context) {
	if ctx.Repo.TreePath == "" {
		ctx.NotFound("No file specified", nil)
		return
	}

	paths := make([]string, 0, 5)
	treeNames := strings.Split(ctx.Repo.TreePath, "/")
	for i := range treeNames {
		paths = append(paths, strings.Join(treeNames[:i+1], "/"))
	}

	// Get current entry user currently looking at.
	entry, err := ctx.Repo.Commit.GetTreeEntryByPath(ctx.Repo.TreePath)
	if err != nil {
		HandleGitError(ctx, "Repo.Commit.GetTreeEntryByPath", err)
		return
	}
	if entry.IsDir() {
		ctx.NotFound("Cannot blame directory", nil)
		return
	}

	blob := entry.Blob()

	ctx.Data["PageIsViewCode"] = true
	ctx.Data["IsBlame"] = true

	ctx.Data["BranchLink"] = ctx.Repo.RepoLink + "/src/" + ctx.Repo.BranchNameSubURL()
	// Blame doesn't "support" Git LFS; it makes sense to show the "raw" version of
	// the content that is otherwise visible as-is to the user.
	ctx.Data["RawFileLink"] = ctx.Repo.RepoLink + "/raw/" + ctx.Repo.BranchNameSubURL() + "/" + util.PathEscapeSegments(ctx.Repo.TreePath)
	ctx.Data["Paths"] = paths
	ctx.Data["TreeNames"] = treeNames

	ctx.Data["FileSize"] = blob.Size()
	ctx.Data["FileName"] = blob.Name()

	if setting.LFS.StartServer {
		dataRc, fileInfo, err := lfs_reader.OpenReaderFromMaybeLFSBlob(ctx.Req.Context(), ctx.Repo.Repository.ID, blob)
		if err != nil {
			ctx.ServerError("OpenReaderFromMaybeLFSBlob", err)
			return
		}
		ctx.Data["IsLFSFile"] = fileInfo.IsLFSFile
		dataRc.Close()
	}

	// Do not display a blame view if the size of the file is
	// larger than what is configured as the maximum.
	if blob.Size() >= setting.UI.MaxDisplayFileSize {
		ctx.Data["IsFileTooLarge"] = true
		ctx.HTML(http.StatusOK, tplRepoHome)
		return
	}

	result, err := performBlame(ctx, ctx.Repo.Commit, ctx.Repo.TreePath, ctx.FormBool("bypass-blame-ignore"))
	if err != nil {
		ctx.ServerError("performBlame", err)
		return
	}

	ctx.Data["NumLinesSet"] = true
	numLines := 0
	for _, p := range result.Parts {
		numLines += len(p.Lines)
	}
	ctx.Data["NumLines"] = numLines

	ctx.Data["UsesIgnoreRevs"] = result.UsesIgnoreRevs
	ctx.Data["FaultyIgnoreRevsFile"] = result.FaultyIgnoreRevsFile

	commitNames := processBlameParts(ctx, result.Parts)
	if ctx.Written() {
		return
	}

	renderBlame(ctx, result.Parts, commitNames)

	ctx.HTML(http.StatusOK, tplRepoHome)
}

type blameResult struct {
	Parts                []*git.BlamePart
	UsesIgnoreRevs       bool
	FaultyIgnoreRevsFile bool
}

func performBlame(ctx *context.Context, commit *git.Commit, file string, bypassBlameIgnore bool) (*blameResult, error) {
	repoPath := ctx.Repo.Repository.RepoPath()
	objectFormat := ctx.Repo.GetObjectFormat()

	blameReader, err := git.CreateBlameReader(ctx, objectFormat, repoPath, commit, file, bypassBlameIgnore)
	if err != nil {
		return nil, err
	}

	r := &blameResult{}
	if err := fillBlameResult(blameReader, r); err != nil {
		_ = blameReader.Close()
		return nil, err
	}

	err = blameReader.Close()
	if err != nil {
		if len(r.Parts) == 0 && r.UsesIgnoreRevs {
			// try again without ignored revs

			blameReader, err = git.CreateBlameReader(ctx, objectFormat, repoPath, commit, file, true)
			if err != nil {
				return nil, err
			}

			r := &blameResult{
				FaultyIgnoreRevsFile: true,
			}
			if err := fillBlameResult(blameReader, r); err != nil {
				_ = blameReader.Close()
				return nil, err
			}

			return r, blameReader.Close()
		}
		return nil, err
	}
	return r, nil
}

func fillBlameResult(br *git.BlameReader, r *blameResult) error {
	r.UsesIgnoreRevs = br.UsesIgnoreRevs()

	previousHelper := make(map[string]*git.BlamePart)

	r.Parts = make([]*git.BlamePart, 0, 5)
	for {
		blamePart, err := br.NextPart()
		if err != nil {
			return fmt.Errorf("BlameReader.NextPart failed: %w", err)
		}
		if blamePart == nil {
			break
		}

		if prev, ok := previousHelper[blamePart.Sha]; ok {
			if blamePart.PreviousSha == "" {
				blamePart.PreviousSha = prev.PreviousSha
				blamePart.PreviousPath = prev.PreviousPath
			}
		} else {
			previousHelper[blamePart.Sha] = blamePart
		}

		r.Parts = append(r.Parts, blamePart)
	}

	return nil
}

func processBlameParts(ctx *context.Context, blameParts []*git.BlamePart) map[string]*user_model.UserCommit {
	// store commit data by SHA to look up avatar info etc
	commitNames := make(map[string]*user_model.UserCommit)
	// and as blameParts can reference the same commits multiple
	// times, we cache the lookup work locally
	commits := make([]*git.Commit, 0, len(blameParts))
	commitCache := map[string]*git.Commit{}
	commitCache[ctx.Repo.Commit.ID.String()] = ctx.Repo.Commit

	for _, part := range blameParts {
		sha := part.Sha
		if _, ok := commitNames[sha]; ok {
			continue
		}

		// find the blamePart commit, to look up parent & email address for avatars
		commit, ok := commitCache[sha]
		var err error
		if !ok {
			commit, err = ctx.Repo.GitRepo.GetCommit(sha)
			if err != nil {
				if git.IsErrNotExist(err) {
					ctx.NotFound("Repo.GitRepo.GetCommit", err)
				} else {
					ctx.ServerError("Repo.GitRepo.GetCommit", err)
				}
				return nil
			}
			commitCache[sha] = commit
		}

		commits = append(commits, commit)
	}

	// populate commit email addresses to later look up avatars.
	for _, c := range user_model.ValidateCommitsWithEmails(ctx, commits) {
		commitNames[c.ID.String()] = c
	}

	return commitNames
}

func renderBlame(ctx *context.Context, blameParts []*git.BlamePart, commitNames map[string]*user_model.UserCommit) {
	repoLink := ctx.Repo.RepoLink

	language, err := files_service.TryGetContentLanguage(ctx.Repo.GitRepo, ctx.Repo.CommitID, ctx.Repo.TreePath)
	if err != nil {
		log.Error("Unable to get file language for %-v:%s. Error: %v", ctx.Repo.Repository, ctx.Repo.TreePath, err)
	}

	lines := make([]string, 0)
	rows := make([]*blameRow, 0)
	escapeStatus := &charset.EscapeStatus{}

	var lexerName string

	avatarUtils := templates.NewAvatarUtils(ctx)
	i := 0
	commitCnt := 0
	for _, part := range blameParts {
		for index, line := range part.Lines {
			i++
			lines = append(lines, line)

			br := &blameRow{
				RowNumber: i,
			}

			commit := commitNames[part.Sha]
			if index == 0 {
				// Count commit number
				commitCnt++

				// User avatar image
				commitSince := templates.TimeSince(commit.Author.When)

				var avatar string
				if commit.User != nil {
					avatar = string(avatarUtils.Avatar(commit.User, 18))
				} else {
					avatar = string(avatarUtils.AvatarByEmail(commit.Author.Email, commit.Author.Name, 18, "tw-mr-2"))
				}

				br.Avatar = gotemplate.HTML(avatar)
				br.RepoLink = repoLink
				br.PartSha = part.Sha
				br.PreviousSha = part.PreviousSha
				br.PreviousShaURL = fmt.Sprintf("%s/blame/commit/%s/%s", repoLink, url.PathEscape(part.PreviousSha), util.PathEscapeSegments(part.PreviousPath))
				br.CommitURL = fmt.Sprintf("%s/commit/%s", repoLink, url.PathEscape(part.Sha))
				br.CommitMessage = commit.CommitMessage
				br.CommitSince = commitSince
			}

			if i != len(lines)-1 {
				line += "\n"
			}
			fileName := fmt.Sprintf("%v", ctx.Data["FileName"])
			line, lexerNameForLine := highlight.Code(fileName, language, line)

			// set lexer name to the first detected lexer. this is certainly suboptimal and
			// we should instead highlight the whole file at once
			if lexerName == "" {
				lexerName = lexerNameForLine
			}

			br.EscapeStatus, br.Code = charset.EscapeControlHTML(line, ctx.Locale, charset.FileviewContext)
			rows = append(rows, br)
			escapeStatus = escapeStatus.Or(br.EscapeStatus)
		}
	}

	ctx.Data["EscapeStatus"] = escapeStatus
	ctx.Data["BlameRows"] = rows
	ctx.Data["CommitCnt"] = commitCnt
	ctx.Data["LexerName"] = lexerName
}
