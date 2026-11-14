// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package lfs

import (
	"net/http"
	"strconv"
	"strings"

	auth_model "forgejo.org/models/auth"
	git_model "forgejo.org/models/git"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/modules/json"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/services/context"
	"forgejo.org/services/convert"
)

// Handles simple cases of presenting lock lists (which can contain zero, one or
// more lists) without pagination.
func handleSimpleLockListOut(ctx *context.Context, list *git_model.LFSLockList, err error) {
	if err != nil {
		if git_model.IsErrLFSLockNotExist(err) {
			writeStatusMessageUsingJSON(ctx, http.StatusOK, &api.LFSLockList{})
			return
		}
		writeStatusMessage(ctx, http.StatusInternalServerError, "Internal Server Error: Could not list locks")
		return
	}

	writeStatusMessageUsingJSON(ctx, http.StatusOK, convert.ToLFSLockList(ctx, list, 0, 0))
}

// GetListLockHandler list locks
func GetListLockHandler(ctx *context.Context) {
	rv := getRequestContext(ctx)

	repository, err := repo_model.GetRepositoryByOwnerAndName(ctx, rv.User, rv.Repo)
	if err != nil {
		log.Debug("Could not find repository: %s/%s - %s", rv.User, rv.Repo, err)
		writeStatusMessage(ctx, http.StatusForbidden, "Pull access required for listing locks")
		return
	}
	repository.MustOwner(ctx)

	context.CheckRepoScopedToken(ctx, repository, auth_model.Read)
	if ctx.Written() {
		return
	}

	authenticated := authenticate(ctx, repository, true, false)
	if !authenticated {
		writeStatusMessage(ctx, http.StatusForbidden, "Pull access required for listing locks")
		return
	}

	cursor := max(ctx.FormInt("cursor"), 0)
	limit := ctx.FormInt("limit")
	if limit > setting.LFS.LocksPagingNum && setting.LFS.LocksPagingNum > 0 {
		limit = setting.LFS.LocksPagingNum
	} else if limit < 0 {
		limit = 0
	}
	id := ctx.FormString("id")
	if id != "" { // Case where we request a specific id
		v, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			writeStatusMessage(ctx, http.StatusBadRequest, err.Error())
			return
		}
		lock, err := git_model.GetLFSLockByIDAndRepo(ctx, v, repository.ID)
		if err != nil && !git_model.IsErrLFSLockNotExist(err) {
			log.Error("Unable to get lock with ID[%s]: Error: %v", v, err)
		}
		handleSimpleLockListOut(ctx, lock.AsLockList(), err)
		return
	}

	path := ctx.FormString("path")
	if path != "" { // Case where we request a specific id
		lock, err := git_model.GetLFSLock(ctx, repository, path)
		if err != nil && !git_model.IsErrLFSLockNotExist(err) {
			log.Error("Unable to get lock for repository %-v with path %s: Error: %v", repository, path, err)
		}
		handleSimpleLockListOut(ctx, lock.AsLockList(), err)
		return
	}

	// If no query params path or id
	lockList, err := git_model.GetLFSLockListByRepoID(ctx, repository.ID, cursor, limit)
	if err != nil {
		log.Error("Unable to list locks for repository ID[%d]: Error: %v", repository.ID, err)
		handleSimpleLockListOut(ctx, &lockList, err)
		return
	}
	writeStatusMessageUsingJSON(
		ctx,
		http.StatusOK,
		convert.ToLFSLockList(ctx, &lockList, cursor, limit),
	)
}

// PostLockHandler create lock
func PostLockHandler(ctx *context.Context) {
	userName := ctx.Params("username")
	repoName := strings.TrimSuffix(ctx.Params("reponame"), ".git")

	repository, err := repo_model.GetRepositoryByOwnerAndName(ctx, userName, repoName)
	if err != nil {
		log.Error("Unable to get repository: %s/%s Error: %v", userName, repoName, err)
		writeStatusMessage(ctx, http.StatusForbidden, "Push access required for lock creation")
		return
	}
	repository.MustOwner(ctx)

	context.CheckRepoScopedToken(ctx, repository, auth_model.Write)
	if ctx.Written() {
		return
	}

	authenticated := authenticate(ctx, repository, true, true)
	if !authenticated {
		writeStatusMessage(ctx, http.StatusForbidden, "Push access required for lock creation")
		return
	}

	bodyReader := ctx.Req.Body
	defer bodyReader.Close()

	var req api.LFSLockRequest
	dec := json.NewDecoder(bodyReader)
	if err := dec.Decode(&req); err != nil {
		log.Warn("Failed to decode lock request as json. Error: %v", err)
		writeStatus(ctx, http.StatusBadRequest)
		return
	}

	lock, err := git_model.CreateLFSLock(ctx, repository, &git_model.LFSLock{
		Path:    req.Path,
		OwnerID: ctx.Doer.ID,
	})
	if err != nil {
		if git_model.IsErrLFSLockAlreadyExist(err) {
			writeStatusMessageUsingJSON(ctx, http.StatusConflict, &api.LFSLockError{
				Lock:    convert.ToLFSLock(ctx, lock),
				Message: "Lock already exists",
			})
			return
		}
		if git_model.IsErrLFSUnauthorizedAction(err) {
			writeStatusMessage(ctx, http.StatusForbidden, "Push access required for lock creation: "+err.Error())
			return
		}
		log.Error("Unable to CreateLFSLock in repository %-v at %s for user %-v: Error: %v", repository, req.Path, ctx.Doer, err)
		writeStatusMessageUsingJSON(ctx, http.StatusInternalServerError, &api.LFSLockError{
			Message: "Could not create lock",
		})
		return
	}
	writeStatusMessageUsingJSON(ctx, http.StatusCreated, &api.LFSLockResponse{
		Lock: convert.ToLFSLock(ctx, lock),
	})
}

// VerifyLockHandler list locks for verification
func VerifyLockHandler(ctx *context.Context) {
	userName := ctx.Params("username")
	repoName := strings.TrimSuffix(ctx.Params("reponame"), ".git")

	repository, err := repo_model.GetRepositoryByOwnerAndName(ctx, userName, repoName)
	if err != nil {
		log.Error("Unable to get repository: %s/%s Error: %v", userName, repoName, err)
		writeStatusMessage(ctx, http.StatusForbidden, "Push access required for lock verification")
		return
	}
	repository.MustOwner(ctx)

	context.CheckRepoScopedToken(ctx, repository, auth_model.Read)
	if ctx.Written() {
		return
	}

	authenticated := authenticate(ctx, repository, true, true)
	if !authenticated {
		writeStatusMessage(ctx, http.StatusForbidden, "Push access required for lock verification")
		return
	}

	cursor := max(ctx.FormInt("cursor"), 0)
	limit := ctx.FormInt("limit")
	if limit > setting.LFS.LocksPagingNum && setting.LFS.LocksPagingNum > 0 {
		limit = setting.LFS.LocksPagingNum
	} else if limit < 0 {
		limit = 0
	}

	// TODO: This is very similar to the logic of ToLFSLockList in convert.go, but
	// api.LFSLockList isn't used.
	lockList, err := git_model.GetLFSLockListByRepoID(ctx, repository.ID, cursor, limit)
	if err != nil {
		log.Error("Unable to list locks for repository ID[%d]: Error: %v", repository.ID, err)
		writeStatusMessage(ctx, http.StatusInternalServerError, "Internal Server Error: Could not list locks")
		return
	}
	next := ""
	if limit > 0 && len(lockList) == limit {
		next = strconv.Itoa(cursor + 1)
	}
	lockOursListAPI := make([]*api.LFSLock, 0, len(lockList))
	lockTheirsListAPI := make([]*api.LFSLock, 0, len(lockList))
	for _, l := range lockList {
		if l.OwnerID == ctx.Doer.ID {
			lockOursListAPI = append(lockOursListAPI, convert.ToLFSLock(ctx, l))
		} else {
			lockTheirsListAPI = append(lockTheirsListAPI, convert.ToLFSLock(ctx, l))
		}
	}
	writeStatusMessageUsingJSON(ctx, http.StatusOK, &api.LFSLockListVerify{
		Ours:   lockOursListAPI,
		Theirs: lockTheirsListAPI,
		Next:   next,
	})
}

// UnLockHandler delete locks
func UnLockHandler(ctx *context.Context) {
	userName := ctx.Params("username")
	repoName := strings.TrimSuffix(ctx.Params("reponame"), ".git")

	repository, err := repo_model.GetRepositoryByOwnerAndName(ctx, userName, repoName)
	if err != nil {
		log.Error("Unable to get repository: %s/%s Error: %v", userName, repoName, err)
		writeStatusMessage(ctx, http.StatusForbidden, "Push access required for lock deletion")
		return
	}
	repository.MustOwner(ctx)

	context.CheckRepoScopedToken(ctx, repository, auth_model.Write)
	if ctx.Written() {
		return
	}

	authenticated := authenticate(ctx, repository, true, true)
	if !authenticated {
		writeStatusMessage(ctx, http.StatusForbidden, "Push access required for lock deletion")
		return
	}

	var req api.LFSLockDeleteRequest
	bodyReader := ctx.Req.Body
	defer bodyReader.Close()

	dec := json.NewDecoder(bodyReader)
	if err := dec.Decode(&req); err != nil {
		log.Warn("Failed to decode lock request as json. Error: %v", err)
		writeStatus(ctx, http.StatusBadRequest)
		return
	}

	lock, err := git_model.DeleteLFSLockByID(ctx, ctx.ParamsInt64("lid"), repository, ctx.Doer, req.Force)
	if err != nil {
		if git_model.IsErrLFSUnauthorizedAction(err) {
			writeStatusMessage(ctx, http.StatusForbidden, "Push access required for lock deletion")
			return
		}
		log.Error("Unable to DeleteLFSLockByID[%d] by user %-v with force %t: Error: %v", ctx.ParamsInt64("lid"), ctx.Doer, req.Force, err)

		writeStatusMessage(ctx, http.StatusInternalServerError, "Could not delete lock")
		return
	}
	writeStatusMessageUsingJSON(ctx, http.StatusOK, &api.LFSLockResponse{Lock: convert.ToLFSLock(ctx, lock)})
}
