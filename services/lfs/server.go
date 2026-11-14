// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package lfs

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	actions_model "forgejo.org/models/actions"
	auth_model "forgejo.org/models/auth"
	git_model "forgejo.org/models/git"
	"forgejo.org/models/perm"
	access_model "forgejo.org/models/perm/access"
	quota_model "forgejo.org/models/quota"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/modules/json"
	lfs_module "forgejo.org/modules/lfs"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/storage"
	api "forgejo.org/modules/structs"
	"forgejo.org/services/context"
)

// requestContext contain variables from the HTTP request.
type requestContext struct {
	User          string
	Repo          string
	Authorization string
}

// DownloadLink builds a URL to download the object.
func (rc *requestContext) DownloadLink(p lfs_module.Pointer) string {
	return setting.AppURL + path.Join(url.PathEscape(rc.User), url.PathEscape(rc.Repo+".git"), "info/lfs/objects", url.PathEscape(p.Oid))
}

// UploadLink builds a URL to upload the object.
func (rc *requestContext) UploadLink(p lfs_module.Pointer) string {
	return setting.AppURL + path.Join(url.PathEscape(rc.User), url.PathEscape(rc.Repo+".git"), "info/lfs/objects", url.PathEscape(p.Oid), strconv.FormatInt(p.Size, 10))
}

// VerifyLink builds a URL for verifying the object.
func (rc *requestContext) VerifyLink(p lfs_module.Pointer) string {
	return setting.AppURL + path.Join(url.PathEscape(rc.User), url.PathEscape(rc.Repo+".git"), "info/lfs/verify")
}

// CheckAcceptMediaType checks if the client accepts the LFS media type.
func CheckAcceptMediaType(ctx *context.Context) {
	mediaParts := strings.Split(ctx.Req.Header.Get("Accept"), ";")

	if mediaParts[0] != lfs_module.MediaType {
		log.Trace("Calling a LFS method without accepting the correct media type: %s", lfs_module.MediaType)
		writeStatus(ctx, http.StatusUnsupportedMediaType)
		return
	}
}

var rangeHeaderRegexp = regexp.MustCompile(`bytes=(\d+)\-(\d*).*`)

// DownloadHandler gets the content from the content store
func DownloadHandler(ctx *context.Context) {
	rc := getRequestContext(ctx)
	p := lfs_module.Pointer{Oid: ctx.Params("oid")}

	meta := getAuthenticatedMeta(ctx, rc, p, false)
	if meta == nil {
		return
	}

	// Support resume download using Range header
	var fromByte, toByte int64
	toByte = meta.Size - 1
	statusCode := http.StatusOK
	if rangeHdr := ctx.Req.Header.Get("Range"); rangeHdr != "" {
		match := rangeHeaderRegexp.FindStringSubmatch(rangeHdr)
		if len(match) > 1 {
			statusCode = http.StatusPartialContent
			fromByte, _ = strconv.ParseInt(match[1], 10, 32)

			if fromByte >= meta.Size {
				writeStatus(ctx, http.StatusRequestedRangeNotSatisfiable)
				return
			}

			if match[2] != "" {
				_toByte, _ := strconv.ParseInt(match[2], 10, 32)
				if _toByte >= fromByte && _toByte < toByte {
					toByte = _toByte
				}
			}

			ctx.Resp.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", fromByte, toByte, meta.Size-fromByte))
			ctx.Resp.Header().Set("Access-Control-Expose-Headers", "Content-Range")
		}
	}

	contentStore := lfs_module.NewContentStore()
	content, err := contentStore.Get(meta.Pointer)
	if err != nil {
		writeStatus(ctx, http.StatusNotFound)
		return
	}
	defer content.Close()

	if fromByte > 0 {
		_, err = content.Seek(fromByte, io.SeekStart)
		if err != nil {
			log.Error("Whilst trying to read LFS OID[%s]: Unable to seek to %d Error: %v", meta.Oid, fromByte, err)
			writeStatus(ctx, http.StatusInternalServerError)
			return
		}
	}

	contentLength := toByte + 1 - fromByte
	ctx.Resp.Header().Set("Content-Length", strconv.FormatInt(contentLength, 10))
	ctx.Resp.Header().Set("Content-Type", "application/octet-stream")

	filename := ctx.Params("filename")
	if len(filename) > 0 {
		decodedFilename, err := base64.RawURLEncoding.DecodeString(filename)
		if err == nil {
			ctx.Resp.Header().Set("Content-Disposition", "attachment; filename=\""+string(decodedFilename)+"\"")
			ctx.Resp.Header().Set("Access-Control-Expose-Headers", "Content-Disposition")
		}
	}

	ctx.Resp.WriteHeader(statusCode)
	if written, err := io.CopyN(ctx.Resp, content, contentLength); err != nil {
		log.Error("Error whilst copying LFS OID[%s] to the response after %d bytes. Error: %v", meta.Oid, written, err)
	}
}

// BatchHandler provides the batch api
func BatchHandler(ctx *context.Context) {
	var br lfs_module.BatchRequest
	if err := decodeJSON(ctx.Req, &br); err != nil {
		log.Trace("Unable to decode BATCH request vars: Error: %v", err)
		writeStatus(ctx, http.StatusBadRequest)
		return
	}

	var isUpload bool
	switch br.Operation {
	case "upload":
		isUpload = true
	case "download":
		isUpload = false
	default:
		log.Trace("Attempt to BATCH with invalid operation: %s", br.Operation)
		writeStatus(ctx, http.StatusBadRequest)
		return
	}

	rc := getRequestContext(ctx)

	repository := getAuthenticatedRepository(ctx, rc, isUpload)
	if repository == nil {
		return
	}

	if isUpload {
		ok, err := quota_model.EvaluateForUser(ctx, repository.OwnerID, quota_model.LimitSubjectSizeGitLFS)
		if err != nil {
			log.Error("quota_model.EvaluateForUser: %v", err)
			writeStatus(ctx, http.StatusInternalServerError)
			return
		}
		if !ok {
			writeStatusMessage(ctx, http.StatusRequestEntityTooLarge, "quota exceeded")
			return
		}
	}

	if setting.LFS.MaxBatchSize != 0 && len(br.Objects) > setting.LFS.MaxBatchSize {
		writeStatus(ctx, http.StatusRequestEntityTooLarge)
		return
	}

	contentStore := lfs_module.NewContentStore()

	var responseObjects []*lfs_module.ObjectResponse

	for _, p := range br.Objects {
		if err := p.Validate(); err != nil {
			responseObjects = append(responseObjects, buildObjectResponse(rc, p, false, false, &lfs_module.ObjectError{
				Code:    http.StatusUnprocessableEntity,
				Message: fmt.Sprintf("Oid or size are invalid (oid: %s, size: %d)", p.Oid, p.Size),
			}))
			continue
		}

		exists, err := contentStore.Exists(p)
		if err != nil {
			log.Error("Unable to check if LFS OID[%s] exist. Error: %v", p.Oid, rc.User, rc.Repo, err)
			writeStatus(ctx, http.StatusInternalServerError)
			return
		}

		meta, err := git_model.GetLFSMetaObjectByOid(ctx, repository.ID, p.Oid)
		if err != nil && !(err == git_model.ErrLFSObjectNotExist || err == lfs_module.ErrInvalidOIDFormat) {
			log.Error("Unable to get LFS MetaObject [%s] for %s/%s. Error: %v", p.Oid, rc.User, rc.Repo, err)
			writeStatus(ctx, http.StatusInternalServerError)
			return
		}

		if meta != nil && p.Size != meta.Size {
			responseObjects = append(responseObjects, buildObjectResponse(rc, p, false, false, &lfs_module.ObjectError{
				Code:    http.StatusUnprocessableEntity,
				Message: fmt.Sprintf("Object %s is not %d bytes", p.Oid, p.Size),
			}))
			continue
		}

		var responseObject *lfs_module.ObjectResponse
		if isUpload {
			var err *lfs_module.ObjectError
			if !exists && setting.LFS.MaxFileSize > 0 && p.Size > setting.LFS.MaxFileSize {
				err = &lfs_module.ObjectError{
					Code:    http.StatusUnprocessableEntity,
					Message: fmt.Sprintf("Size must be less than or equal to %d", setting.LFS.MaxFileSize),
				}
			}

			if exists && meta == nil {
				accessible, err := git_model.LFSObjectAccessible(ctx, ctx.Doer, p.Oid)
				if err != nil {
					log.Error("Unable to check if LFS MetaObject [%s] is accessible. Error: %v", p.Oid, err)
					writeStatus(ctx, http.StatusInternalServerError)
					return
				}
				if accessible {
					_, err := git_model.NewLFSMetaObject(ctx, repository.ID, p)
					if err != nil {
						log.Error("Unable to create LFS MetaObject [%s] for %s/%s. Error: %v", p.Oid, rc.User, rc.Repo, err)
						writeStatus(ctx, http.StatusInternalServerError)
						return
					}
				} else {
					exists = false
				}
			}

			responseObject = buildObjectResponse(rc, p, false, !exists, err)
		} else {
			var err *lfs_module.ObjectError
			if !exists || meta == nil {
				err = &lfs_module.ObjectError{
					Code:    http.StatusNotFound,
					Message: http.StatusText(http.StatusNotFound),
				}
			}

			responseObject = buildObjectResponse(rc, p, true, false, err)
		}
		responseObjects = append(responseObjects, responseObject)
	}

	respobj := &lfs_module.BatchResponse{Objects: responseObjects}

	ctx.Resp.Header().Set("Content-Type", lfs_module.MediaType)

	enc := json.NewEncoder(ctx.Resp)
	if err := enc.Encode(respobj); err != nil {
		log.Error("Failed to encode representation as json. Error: %v", err)
	}
}

// UploadHandler receives data from the client and puts it into the content store
func UploadHandler(ctx *context.Context) {
	rc := getRequestContext(ctx)

	p := lfs_module.Pointer{Oid: ctx.Params("oid")}
	var err error
	if p.Size, err = strconv.ParseInt(ctx.Params("size"), 10, 64); err != nil {
		writeStatusMessage(ctx, http.StatusUnprocessableEntity, err.Error())
	}

	if err = p.Validate(); err != nil {
		log.Trace("Attempt to access invalid LFS OID[%s] in %s/%s", p.Oid, rc.User, rc.Repo)
		writeStatusMessage(ctx, http.StatusUnprocessableEntity, "Oid or size are invalid")
		return
	}

	repository := getAuthenticatedRepository(ctx, rc, true)
	if repository == nil {
		return
	}

	contentStore := lfs_module.NewContentStore()
	exists, err := contentStore.Exists(p)
	if err != nil {
		log.Error("Unable to check if LFS OID[%s] exist. Error: %v", p.Oid, err)
		writeStatus(ctx, http.StatusInternalServerError)
		return
	}

	if !exists {
		ok, err := quota_model.EvaluateForUser(ctx, repository.OwnerID, quota_model.LimitSubjectSizeGitLFS)
		if err != nil {
			log.Error("quota_model.EvaluateForUser: %v", err)
			writeStatus(ctx, http.StatusInternalServerError)
			return
		}
		if !ok {
			writeStatusMessage(ctx, http.StatusRequestEntityTooLarge, "quota exceeded")
			return
		}
	}

	uploadOrVerify := func() error {
		if exists {
			accessible, err := git_model.LFSObjectAccessible(ctx, ctx.Doer, p.Oid)
			if err != nil {
				log.Error("Unable to check if LFS MetaObject [%s] is accessible. Error: %v", p.Oid, err)
				return err
			}
			if !accessible {
				// The file exists but the user has no access to it.
				// The upload gets verified by hashing and size comparison to prove access to it.
				hash := sha256.New()
				written, err := io.Copy(hash, ctx.Req.Body)
				if err != nil {
					log.Error("Error creating hash. Error: %v", err)
					return err
				}

				if written != p.Size {
					return lfs_module.ErrSizeMismatch
				}
				if hex.EncodeToString(hash.Sum(nil)) != p.Oid {
					return lfs_module.ErrHashMismatch
				}
			}
		} else if err := contentStore.Put(p, ctx.Req.Body); err != nil {
			log.Error("Error putting LFS MetaObject [%s] into content store. Error: %v", p.Oid, err)
			return err
		}
		_, err := git_model.NewLFSMetaObject(ctx, repository.ID, p)
		return err
	}

	defer ctx.Req.Body.Close()
	if err := uploadOrVerify(); err != nil {
		if errors.Is(err, lfs_module.ErrSizeMismatch) || errors.Is(err, lfs_module.ErrHashMismatch) {
			log.Error("Upload does not match LFS MetaObject [%s]. Error: %v", p.Oid, err)
			writeStatusMessage(ctx, http.StatusUnprocessableEntity, err.Error())
		} else {
			log.Error("Error whilst uploadOrVerify LFS OID[%s]: %v", p.Oid, err)
			writeStatus(ctx, http.StatusInternalServerError)
		}
		if _, err = git_model.RemoveLFSMetaObjectByOid(ctx, repository.ID, p.Oid); err != nil {
			log.Error("Error whilst removing MetaObject for LFS OID[%s]: %v", p.Oid, err)
		}
		return
	}

	writeStatus(ctx, http.StatusOK)
}

// VerifyHandler verify oid and its size from the content store
func VerifyHandler(ctx *context.Context) {
	var p lfs_module.Pointer
	if err := decodeJSON(ctx.Req, &p); err != nil {
		writeStatus(ctx, http.StatusUnprocessableEntity)
		return
	}

	rc := getRequestContext(ctx)

	// Only catches invalid OIDs, but the size case is covered later.
	meta := getAuthenticatedMeta(ctx, rc, p, true)
	if meta == nil {
		return
	}

	contentStore := lfs_module.NewContentStore()
	// Implicitly catches pointers with invalid sizes (i.e. negative size, as the
	// size of a file in the content store should never be equal to zero). Unlike
	// in other functions, we don't care about reporting back whether the error
	// was caused by a malformed oid or a wrong size; it's only "not found".
	ok, err := contentStore.Verify(meta.Pointer)

	status := http.StatusOK
	if err != nil {
		log.Error("Error whilst verifying LFS OID[%s]: %v", p.Oid, err)
		status = http.StatusInternalServerError
	} else if !ok {
		status = http.StatusNotFound
	}
	writeStatus(ctx, status)
}

func decodeJSON(req *http.Request, v any) error {
	defer req.Body.Close()

	dec := json.NewDecoder(req.Body)
	return dec.Decode(v)
}

func getRequestContext(ctx *context.Context) *requestContext {
	return &requestContext{
		User:          ctx.Params("username"),
		Repo:          strings.TrimSuffix(ctx.Params("reponame"), ".git"),
		Authorization: ctx.Req.Header.Get("Authorization"),
	}
}

func getAuthenticatedMeta(ctx *context.Context, rc *requestContext, p lfs_module.Pointer, requireWrite bool) *git_model.LFSMetaObject {
	// NOTE: p.Size is not necessarily valid, as the passed pointer object can simply contain the OID.
	if !p.IsOIDValid() {
		log.Info("Attempt to access invalid LFS OID[%s] in %s/%s", p.Oid, rc.User, rc.Repo)
		writeStatusMessage(ctx, http.StatusUnprocessableEntity, lfs_module.ErrInvalidOIDFormat.Error())
		return nil
	}

	repository := getAuthenticatedRepository(ctx, rc, requireWrite)
	if repository == nil {
		return nil
	}

	meta, err := git_model.GetLFSMetaObjectByOid(ctx, repository.ID, p.Oid)
	if err != nil {
		log.Error("Unable to get LFS OID[%s] in %s/%s. Error: %v", p.Oid, rc.User, rc.Repo, err)
		writeStatus(ctx, http.StatusNotFound)
		return nil
	}

	return meta
}

func getAuthenticatedRepository(ctx *context.Context, rc *requestContext, requireWrite bool) *repo_model.Repository {
	repository, err := repo_model.GetRepositoryByOwnerAndName(ctx, rc.User, rc.Repo)
	if err != nil {
		log.Error("Unable to get repository: %s/%s. Error: %v", rc.User, rc.Repo, err)
		writeStatus(ctx, http.StatusNotFound)
		return nil
	}

	if !authenticate(ctx, repository, false, requireWrite) {
		writeStatus(ctx, http.StatusUnauthorized)
		return nil
	}

	if requireWrite {
		context.CheckRepoScopedToken(ctx, repository, auth_model.Write)
	} else {
		context.CheckRepoScopedToken(ctx, repository, auth_model.Read)
	}

	if ctx.Written() {
		return nil
	}

	return repository
}

func buildObjectResponse(rc *requestContext, pointer lfs_module.Pointer, download, upload bool, err *lfs_module.ObjectError) *lfs_module.ObjectResponse {
	rep := &lfs_module.ObjectResponse{Pointer: pointer}
	if err != nil {
		rep.Error = err
	} else {
		rep.Actions = make(map[string]*lfs_module.Link)

		header := make(map[string]string)

		if len(rc.Authorization) > 0 {
			header["Authorization"] = rc.Authorization
		}

		if download {
			var link *lfs_module.Link
			if setting.LFS.Storage.MinioConfig.ServeDirect {
				// If we have a signed url (S3, object storage), redirect to this directly.
				u, err := storage.LFS.URL(pointer.RelativePath(), pointer.Oid, nil)
				if u != nil && err == nil {
					// Presigned url does not need the Authorization header
					// https://github.com/go-gitea/gitea/issues/21525
					delete(header, "Authorization")
					link = &lfs_module.Link{Href: u.String(), Header: header}
				}
			}
			if link == nil {
				link = &lfs_module.Link{Href: rc.DownloadLink(pointer), Header: header}
			}
			rep.Actions["download"] = link
		}
		if upload {
			rep.Actions["upload"] = &lfs_module.Link{Href: rc.UploadLink(pointer), Header: header}

			verifyHeader := make(map[string]string)
			maps.Copy(verifyHeader, header)

			// This is only needed to workaround https://github.com/git-lfs/git-lfs/issues/3662
			verifyHeader["Accept"] = lfs_module.AcceptHeader

			rep.Actions["verify"] = &lfs_module.Link{Href: rc.VerifyLink(pointer), Header: verifyHeader}
		}
	}
	return rep
}

func writeStatusMessageUsingJSON[
	JSON *api.LFSLockList |
		*api.LFSLockError |
		*api.LFSLockResponse |
		*api.LFSLockListVerify |
		*lfs_module.ErrorResponse](ctx *context.Context, statusCode int, input JSON) {
	// See: https://github.com/git-lfs/git-lfs/blob/0043a645047926f4bd7f7091299095528253d575/docs/api/batch.md?plain=1#L256-L259
	if statusCode == http.StatusUnauthorized {
		ctx.Resp.Header().Set("LFS-Authenticate", "Basic realm=forgejo-lfs")
	}

	if statusCode != http.StatusOK {
		const documentationURL = "https://codeberg.org/forgejo/forgejo/issues"
		if errorResponse, ok := any(input).(*lfs_module.ErrorResponse); ok {
			errorResponse.DocumentationURL = documentationURL
		} else if lockError, ok := any(input).(*api.LFSLockError); ok {
			lockError.DocumentationURL = documentationURL
		}
	}

	ctx.JSONWithContentType(statusCode, lfs_module.MediaType, input)
}

// Creates an [lfs_module.ErrorResponse] from the status and the message.
// The resulting message always starts with http.StatusText(status); if the
// message is an empty string, then the final JSON should only contain the
// http.StatusText(status) (e.g. "Unauthorized"). Otherwise, the string's
// structure should look something like "Unauthorized: Contents of message var".
func writeStatusMessage(ctx *context.Context, statusCode int, message string) {
	var messageForErrorResponse string
	if len(message) == 0 {
		messageForErrorResponse = http.StatusText(statusCode)
	} else {
		messageForErrorResponse = fmt.Sprintf("%s: %s", http.StatusText(statusCode), message)
	}
	inputJSON := lfs_module.ErrorResponse{Message: messageForErrorResponse}

	writeStatusMessageUsingJSON(ctx, statusCode, &inputJSON)
}

func writeStatus(ctx *context.Context, statusCode int) {
	writeStatusMessage(ctx, statusCode, "")
}

// authenticate uses the authorization string to determine whether
// or not to proceed. This server assumes an HTTP Basic auth format.
func authenticate(ctx *context.Context, repository *repo_model.Repository, requireSigned, requireWrite bool) bool {
	accessMode := perm.AccessModeRead
	if requireWrite {
		accessMode = perm.AccessModeWrite
	}

	if hasTaskID, taskID := ctx.Authentication.ActionsTaskID().Get(); hasTaskID {
		task, err := actions_model.GetTaskByID(ctx, taskID)
		if err != nil {
			log.Error("Unable to GetTaskByID for task[%d] Error: %v", taskID, err)
			return false
		}
		if task.RepoID != repository.ID {
			return false
		}

		if task.IsForkPullRequest {
			return accessMode <= perm.AccessModeRead
		}
		return accessMode <= perm.AccessModeWrite
	}

	// ctx.IsSigned is unnecessary here, this will be checked in perm.CanAccess. Usage of ctx.Authentication.Reducer()
	// or .Scope() is also unnecessary here in `authenticate` -- context.CheckRepoScopedToken is used after
	// authentication to provide authorization checks.
	repoPerm, err := access_model.GetUserRepoPermission(ctx, repository, ctx.Doer)
	if err != nil {
		log.Error("Unable to GetUserRepoPermission for user %-v in repo %-v Error: %v", ctx.Doer, repository, err)
		return false
	}

	canRead := repoPerm.CanAccess(accessMode, unit.TypeCode)
	if canRead && (!requireSigned || ctx.IsSigned) {
		return true
	}

	return false
}
