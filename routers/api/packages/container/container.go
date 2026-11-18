// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package container

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"

	packages_model "forgejo.org/models/packages"
	container_model "forgejo.org/models/packages/container"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/json"
	"forgejo.org/modules/log"
	packages_module "forgejo.org/modules/packages"
	container_module "forgejo.org/modules/packages/container"
	"forgejo.org/modules/setting"
	"forgejo.org/routers/api/packages/helper"
	"forgejo.org/services/context"
	packages_service "forgejo.org/services/packages"
	container_service "forgejo.org/services/packages/container"

	digest "github.com/opencontainers/go-digest"
)

// https://github.com/opencontainers/distribution-spec/blob/main/spec.md#pulling-manifests
var imageNamePattern = regexp.MustCompile(`^[a-z0-9]+((\.|_|__|-+)[a-z0-9]+)*(\/[a-z0-9]+((\.|_|__|-+)[a-z0-9]+)*)*$`)

type containerHeaders struct {
	Status        int
	ContentDigest string
	UploadUUID    string
	Range         string
	Location      string
	ContentType   string
	ContentLength int64
}

// https://github.com/opencontainers/distribution-spec/blob/main/spec.md#legacy-docker-support-http-headers
func setResponseHeaders(resp http.ResponseWriter, h *containerHeaders) {
	if h.Location != "" {
		resp.Header().Set("Location", h.Location)
	}
	if h.Range != "" {
		resp.Header().Set("Range", h.Range)
	}
	if h.ContentType != "" {
		resp.Header().Set("Content-Type", h.ContentType)
	}
	if h.UploadUUID != "" {
		resp.Header().Set("Docker-Upload-Uuid", h.UploadUUID)
	}
	if h.ContentDigest != "" {
		resp.Header().Set("Docker-Content-Digest", h.ContentDigest)
		resp.Header().Set("ETag", fmt.Sprintf(`"%s"`, h.ContentDigest))
	}
	if h.ContentLength >= 0 {
		resp.Header().Set("Content-Length", strconv.FormatInt(h.ContentLength, 10))
	}
	resp.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
	resp.WriteHeader(h.Status)
}

func jsonResponse(ctx *context.Context, status int, obj any) {
	// Buffer the JSON content first to calculate correct Content-Length
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(obj); err != nil {
		log.Error("JSON encode: %v", err)
		return
	}

	setResponseHeaders(ctx.Resp, &containerHeaders{
		Status:        status,
		ContentType:   "application/json",
		ContentLength: int64(buf.Len()),
	})

	if _, err := buf.WriteTo(ctx.Resp); err != nil {
		log.Error("JSON write: %v", err)
	}
}

func apiError(ctx *context.Context, status int, err error) {
	helper.LogAndProcessError(ctx, status, err, func(message string) {
		setResponseHeaders(ctx.Resp, &containerHeaders{
			Status: status,
		})
	})
}

// https://github.com/opencontainers/distribution-spec/blob/main/spec.md#error-codes
func apiErrorDefined(ctx *context.Context, err *container_service.NamedError) {
	type ContainerError struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}

	type ContainerErrors struct {
		Errors []ContainerError `json:"errors"`
	}

	jsonResponse(ctx, err.StatusCode, ContainerErrors{
		Errors: []ContainerError{
			{
				Code:    err.Code,
				Message: err.Message,
			},
		},
	})
}

func APIUnauthorizedError(ctx *context.Context) {
	// Do not include more than one challenge in the same header field. That breaks clients even though the HTTP RFC
	// allows it.
	ctx.Resp.Header().Set("WWW-Authenticate", `Bearer realm="`+setting.AppURL+`v2/token",service="container_registry",scope="*"`)
	apiErrorDefined(ctx, container_service.ErrUnauthorized)
}

// ReqContainerAccess is a middleware which checks the current user valid (real user or ghost if anonymous access is enabled)
func ReqContainerAccess(ctx *context.Context) {
	if ctx.Doer == nil || (setting.Service.RequireSignInView && ctx.Doer.IsGhost()) {
		APIUnauthorizedError(ctx)
	}
}

// VerifyImageName is a middleware which checks if the image name is allowed
func VerifyImageName(ctx *context.Context) {
	if !imageNamePattern.MatchString(ctx.Params("image")) {
		apiErrorDefined(ctx, container_service.ErrNameInvalid)
	}
}

// DetermineSupport is used to test if the registry supports OCI
// https://github.com/opencontainers/distribution-spec/blob/main/spec.md#determining-support
func DetermineSupport(ctx *context.Context) {
	setResponseHeaders(ctx.Resp, &containerHeaders{
		Status: http.StatusOK,
	})
}

// Authenticate creates a token for the current user
// If the current user is anonymous, the ghost user is used unless RequireSignInView is enabled.
func Authenticate(ctx *context.Context) {
	u := ctx.Doer
	if u == nil {
		if setting.Service.RequireSignInView {
			APIUnauthorizedError(ctx)
			return
		}

		u = user_model.NewGhostUser()
	}

	// If there's an API scope, ensure it propagates.
	scope := ctx.Authentication.Scope().ValueOrZeroValue()

	token, err := packages_service.CreateAuthorizationToken(u, scope)
	if err != nil {
		apiError(ctx, http.StatusInternalServerError, err)
		return
	}

	ctx.JSON(http.StatusOK, map[string]string{
		"token": token,
	})
}

// https://distribution.github.io/distribution/spec/auth/oauth/
func AuthenticateNotImplemented(ctx *context.Context) {
	// This optional endpoint can be used to authenticate a client.
	// It must implement the specification described in:
	// https://datatracker.ietf.org/doc/html/rfc6749
	// https://distribution.github.io/distribution/spec/auth/oauth/
	// Purpose of this stub is to respond with 404 Not Found instead of 405 Method Not Allowed.

	ctx.Status(http.StatusNotFound)
}

// https://docs.docker.com/registry/spec/api/#listing-repositories
func GetRepositoryList(ctx *context.Context) {
	n := ctx.FormInt("n")
	if n <= 0 || n > 100 {
		n = 100
	}
	last := ctx.FormTrim("last")

	repositories, err := container_model.GetRepositories(ctx, ctx.Doer, n, last)
	if err != nil {
		apiError(ctx, http.StatusInternalServerError, err)
		return
	}

	type RepositoryList struct {
		Repositories []string `json:"repositories"`
	}

	if len(repositories) == n {
		v := url.Values{}
		if n > 0 {
			v.Add("n", strconv.Itoa(n))
		}
		v.Add("last", repositories[len(repositories)-1])

		ctx.Resp.Header().Set("Link", fmt.Sprintf(`</v2/_catalog?%s>; rel="next"`, v.Encode()))
	}

	jsonResponse(ctx, http.StatusOK, RepositoryList{
		Repositories: repositories,
	})
}

// https://github.com/opencontainers/distribution-spec/blob/main/spec.md#mounting-a-blob-from-another-repository
// https://github.com/opencontainers/distribution-spec/blob/main/spec.md#single-post
// https://github.com/opencontainers/distribution-spec/blob/main/spec.md#pushing-a-blob-in-chunks
func InitiateUploadBlob(ctx *context.Context) {
	image := ctx.Params("image")

	mount := ctx.FormTrim("mount")
	from := ctx.FormTrim("from")
	if mount != "" {
		blob, _ := container_service.WorkaroundGetContainerBlob(ctx, &container_model.BlobSearchOptions{
			Repository: from,
			Digest:     mount,
		})
		if blob != nil {
			accessible, err := packages_model.IsBlobAccessibleForUser(ctx, blob.Blob.ID, ctx.Doer)
			if err != nil {
				apiError(ctx, http.StatusInternalServerError, err)
				return
			}

			if accessible {
				if err := container_service.MountBlob(ctx, &packages_service.PackageInfo{Owner: ctx.Package.Owner, Name: image}, blob.Blob); err != nil {
					apiError(ctx, http.StatusInternalServerError, err)
					return
				}

				setResponseHeaders(ctx.Resp, &containerHeaders{
					Location:      fmt.Sprintf("/v2/%s/%s/blobs/%s", ctx.Package.Owner.LowerName, image, mount),
					ContentDigest: mount,
					Status:        http.StatusCreated,
				})
				return
			}
		}
	}

	digest := ctx.FormTrim("digest")
	if digest != "" {
		buf, err := packages_module.CreateHashedBufferFromReader(ctx.Req.Body)
		if err != nil {
			apiError(ctx, http.StatusInternalServerError, err)
			return
		}
		defer buf.Close()

		if digest != container_service.DigestFromHashSummer(buf) {
			apiErrorDefined(ctx, container_service.ErrDigestInvalid)
			return
		}

		if _, err := container_service.SaveAsPackageBlob(ctx,
			buf,
			&packages_service.PackageCreationInfo{
				PackageInfo: packages_service.PackageInfo{
					Owner: ctx.Package.Owner,
					Name:  image,
				},
				Creator: ctx.Doer,
			},
		); err != nil {
			switch err {
			case packages_service.ErrQuotaTotalCount, packages_service.ErrQuotaTypeSize, packages_service.ErrQuotaTotalSize:
				apiError(ctx, http.StatusForbidden, err)
			default:
				apiError(ctx, http.StatusInternalServerError, err)
			}
			return
		}

		setResponseHeaders(ctx.Resp, &containerHeaders{
			Location:      fmt.Sprintf("/v2/%s/%s/blobs/%s", ctx.Package.Owner.LowerName, image, digest),
			ContentDigest: digest,
			Status:        http.StatusCreated,
		})
		return
	}

	upload, err := packages_model.CreateBlobUpload(ctx)
	if err != nil {
		apiError(ctx, http.StatusInternalServerError, err)
		return
	}

	setResponseHeaders(ctx.Resp, &containerHeaders{
		Location:   fmt.Sprintf("/v2/%s/%s/blobs/uploads/%s", ctx.Package.Owner.LowerName, image, upload.ID),
		Range:      "0-0",
		UploadUUID: upload.ID,
		Status:     http.StatusAccepted,
	})
}

// https://docs.docker.com/registry/spec/api/#get-blob-upload
func GetUploadBlob(ctx *context.Context) {
	uuid := ctx.Params("uuid")

	upload, err := packages_model.GetBlobUploadByID(ctx, uuid)
	if err != nil {
		if err == packages_model.ErrPackageBlobUploadNotExist {
			apiErrorDefined(ctx, container_service.ErrBlobUploadUnknown)
		} else {
			apiError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	setResponseHeaders(ctx.Resp, &containerHeaders{
		Range:      fmt.Sprintf("0-%d", upload.BytesReceived),
		UploadUUID: upload.ID,
		Status:     http.StatusNoContent,
	})
}

// https://github.com/opencontainers/distribution-spec/blob/main/spec.md#pushing-a-blob-in-chunks
func UploadBlob(ctx *context.Context) {
	image := ctx.Params("image")

	uploader, err := container_service.NewBlobUploader(ctx, ctx.Params("uuid"))
	if err != nil {
		if err == packages_model.ErrPackageBlobUploadNotExist {
			apiErrorDefined(ctx, container_service.ErrBlobUploadUnknown)
		} else {
			apiError(ctx, http.StatusInternalServerError, err)
		}
		return
	}
	defer uploader.Close()

	contentRange := ctx.Req.Header.Get("Content-Range")
	if contentRange != "" {
		start, end := 0, 0
		if _, err := fmt.Sscanf(contentRange, "%d-%d", &start, &end); err != nil {
			apiErrorDefined(ctx, container_service.ErrBlobUploadInvalid)
			return
		}

		if int64(start) != uploader.Size() {
			apiErrorDefined(ctx, container_service.ErrBlobUploadInvalid.WithStatusCode(http.StatusRequestedRangeNotSatisfiable))
			return
		}
	} else if uploader.Size() != 0 {
		apiErrorDefined(ctx, container_service.ErrBlobUploadInvalid.WithMessage("Stream uploads after first write are not allowed"))
		return
	}

	if err := uploader.Append(ctx, ctx.Req.Body); err != nil {
		apiError(ctx, http.StatusInternalServerError, err)
		return
	}

	setResponseHeaders(ctx.Resp, &containerHeaders{
		Location:   fmt.Sprintf("/v2/%s/%s/blobs/uploads/%s", ctx.Package.Owner.LowerName, image, uploader.ID),
		Range:      fmt.Sprintf("0-%d", uploader.Size()-1),
		UploadUUID: uploader.ID,
		Status:     http.StatusAccepted,
	})
}

// https://github.com/opencontainers/distribution-spec/blob/main/spec.md#pushing-a-blob-in-chunks
func EndUploadBlob(ctx *context.Context) {
	image := ctx.Params("image")

	digest := ctx.FormTrim("digest")
	if digest == "" {
		apiErrorDefined(ctx, container_service.ErrDigestInvalid)
		return
	}

	uploader, err := container_service.NewBlobUploader(ctx, ctx.Params("uuid"))
	if err != nil {
		if err == packages_model.ErrPackageBlobUploadNotExist {
			apiErrorDefined(ctx, container_service.ErrBlobUploadUnknown)
		} else {
			apiError(ctx, http.StatusInternalServerError, err)
		}
		return
	}
	defer uploader.Close()

	if ctx.Req.Body != nil {
		if err := uploader.Append(ctx, ctx.Req.Body); err != nil {
			apiError(ctx, http.StatusInternalServerError, err)
			return
		}
	}

	if digest != container_service.DigestFromHashSummer(uploader) {
		apiErrorDefined(ctx, container_service.ErrDigestInvalid)
		return
	}

	if _, err := container_service.SaveAsPackageBlob(ctx,
		uploader,
		&packages_service.PackageCreationInfo{
			PackageInfo: packages_service.PackageInfo{
				Owner: ctx.Package.Owner,
				Name:  image,
			},
			Creator: ctx.Doer,
		},
	); err != nil {
		switch err {
		case packages_service.ErrQuotaTotalCount, packages_service.ErrQuotaTypeSize, packages_service.ErrQuotaTotalSize:
			apiError(ctx, http.StatusForbidden, err)
		default:
			apiError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	if err := container_service.RemoveBlobUploadByID(ctx, uploader.ID); err != nil {
		apiError(ctx, http.StatusInternalServerError, err)
		return
	}

	setResponseHeaders(ctx.Resp, &containerHeaders{
		Location:      fmt.Sprintf("/v2/%s/%s/blobs/%s", ctx.Package.Owner.LowerName, image, digest),
		ContentDigest: digest,
		Status:        http.StatusCreated,
	})
}

// https://docs.docker.com/registry/spec/api/#delete-blob-upload
func CancelUploadBlob(ctx *context.Context) {
	uuid := ctx.Params("uuid")

	_, err := packages_model.GetBlobUploadByID(ctx, uuid)
	if err != nil {
		if err == packages_model.ErrPackageBlobUploadNotExist {
			apiErrorDefined(ctx, container_service.ErrBlobUploadUnknown)
		} else {
			apiError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	if err := container_service.RemoveBlobUploadByID(ctx, uuid); err != nil {
		apiError(ctx, http.StatusInternalServerError, err)
		return
	}

	setResponseHeaders(ctx.Resp, &containerHeaders{
		Status: http.StatusNoContent,
	})
}

// https://github.com/opencontainers/distribution-spec/blob/main/spec.md#checking-if-content-exists-in-the-registry
func HeadBlob(ctx *context.Context) {
	blob, err := container_service.GetLocalBlob(ctx, ctx.Package.Owner.ID, ctx.Params("digest"), ctx.Params("image"))
	if err != nil {
		if errors.Is(err, container_model.ErrContainerBlobNotExist) {
			apiErrorDefined(ctx, container_service.ErrBlobUnknown)
		} else {
			apiError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	setResponseHeaders(ctx.Resp, &containerHeaders{
		ContentDigest: blob.Properties.GetByName(container_module.PropertyDigest),
		ContentLength: blob.Blob.Size,
		Status:        http.StatusOK,
	})
}

// https://github.com/opencontainers/distribution-spec/blob/main/spec.md#pulling-blobs
func GetBlob(ctx *context.Context) {
	blob, err := container_service.GetLocalBlob(ctx, ctx.Package.Owner.ID, ctx.Params("digest"), ctx.Params("image"))
	if err != nil {
		if err == container_model.ErrContainerBlobNotExist {
			apiErrorDefined(ctx, container_service.ErrBlobUnknown)
		} else {
			apiError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	serveBlob(ctx, blob)
}

// https://github.com/opencontainers/distribution-spec/blob/main/spec.md#deleting-blobs
func DeleteBlob(ctx *context.Context) {
	d := ctx.Params("digest")

	if digest.Digest(d).Validate() != nil {
		apiErrorDefined(ctx, container_service.ErrBlobUnknown)
		return
	}

	if err := container_service.DeleteBlob(ctx, ctx.Package.Owner.ID, ctx.Params("image"), d); err != nil {
		apiError(ctx, http.StatusInternalServerError, err)
		return
	}

	setResponseHeaders(ctx.Resp, &containerHeaders{
		Status: http.StatusAccepted,
	})
}

// https://github.com/opencontainers/distribution-spec/blob/main/spec.md#pushing-manifests
func UploadManifest(ctx *context.Context) {
	mci, err := container_service.NewManifestCreationInfo(
		ctx.Package.Owner,
		ctx.Doer,
		ctx.Req.Header.Get("Content-Type"),
		ctx.Params("image"),
		ctx.Params("reference"),
	)
	if err != nil {
		apiErrorDefined(ctx, container_service.ErrManifestInvalid.WithMessage(err.Error()))
		return
	}

	maxSize := container_service.MaxManifestSize + 1
	buf, err := packages_module.CreateHashedBufferFromReaderWithSize(&io.LimitedReader{R: ctx.Req.Body, N: int64(maxSize)}, maxSize)
	if err != nil {
		apiError(ctx, http.StatusInternalServerError, err)
		return
	}
	defer buf.Close()

	if buf.Size() > container_service.MaxManifestSize {
		apiErrorDefined(ctx, container_service.ErrManifestInvalid.WithMessage("Manifest exceeds maximum size").WithStatusCode(http.StatusRequestEntityTooLarge))
		return
	}

	digest, err := container_service.ProcessManifest(ctx, *mci, buf)
	if err != nil {
		var namedError *container_service.NamedError
		if errors.As(err, &namedError) {
			apiErrorDefined(ctx, namedError)
		} else if errors.Is(err, container_model.ErrContainerBlobNotExist) {
			apiErrorDefined(ctx, container_service.ErrBlobUnknown)
		} else {
			switch err {
			case packages_service.ErrQuotaTotalCount, packages_service.ErrQuotaTypeSize, packages_service.ErrQuotaTotalSize:
				apiError(ctx, http.StatusForbidden, err)
			default:
				apiError(ctx, http.StatusInternalServerError, err)
			}
		}
		return
	}

	setResponseHeaders(ctx.Resp, &containerHeaders{
		Location:      fmt.Sprintf("/v2/%s/%s/manifests/%s", ctx.Package.Owner.LowerName, mci.Image, mci.Reference),
		ContentDigest: digest,
		Status:        http.StatusCreated,
	})
}

// https://github.com/opencontainers/distribution-spec/blob/main/spec.md#checking-if-content-exists-in-the-registry
func HeadManifest(ctx *context.Context) {
	manifest, err := container_service.GetLocalManifest(ctx, ctx.Package.Owner.ID, ctx.Params("image"), ctx.Params("reference"))
	if err != nil {
		if err == container_model.ErrContainerBlobNotExist {
			apiErrorDefined(ctx, container_service.ErrManifestUnknown)
		} else {
			apiError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	setResponseHeaders(ctx.Resp, &containerHeaders{
		ContentDigest: manifest.Properties.GetByName(container_module.PropertyDigest),
		ContentType:   manifest.Properties.GetByName(container_module.PropertyMediaType),
		ContentLength: manifest.Blob.Size,
		Status:        http.StatusOK,
	})
}

// https://github.com/opencontainers/distribution-spec/blob/main/spec.md#pulling-manifests
func GetManifest(ctx *context.Context) {
	manifest, err := container_service.GetLocalManifest(ctx, ctx.Package.Owner.ID, ctx.Params("image"), ctx.Params("reference"))
	if err != nil {
		if err == container_model.ErrContainerBlobNotExist {
			apiErrorDefined(ctx, container_service.ErrManifestUnknown)
		} else {
			apiError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	serveBlob(ctx, manifest)
}

// https://github.com/opencontainers/distribution-spec/blob/main/spec.md#deleting-tags
// https://github.com/opencontainers/distribution-spec/blob/main/spec.md#deleting-manifests
func DeleteManifest(ctx *context.Context) {
	opts, err := container_service.GetManifestSearchOptions(
		ctx.Package.Owner.ID,
		ctx.Params("image"),
		ctx.Params("reference"),
	)
	if err != nil {
		apiErrorDefined(ctx, container_service.ErrManifestUnknown)
		return
	}

	pvs, err := container_model.GetManifestVersions(ctx, opts)
	if err != nil {
		apiError(ctx, http.StatusInternalServerError, err)
		return
	}

	if len(pvs) == 0 {
		apiErrorDefined(ctx, container_service.ErrManifestUnknown)
		return
	}

	for _, pv := range pvs {
		if err := packages_service.RemovePackageVersion(ctx, ctx.Doer, pv); err != nil {
			apiError(ctx, http.StatusInternalServerError, err)
			return
		}
	}

	setResponseHeaders(ctx.Resp, &containerHeaders{
		Status: http.StatusAccepted,
	})
}

func serveBlob(ctx *context.Context, pfd *packages_model.PackageFileDescriptor) {
	serveDirectReqParams := make(url.Values)
	serveDirectReqParams.Set("response-content-type", pfd.Properties.GetByName(container_module.PropertyMediaType))
	s, u, pf, err := packages_service.GetPackageBlobStream(ctx, pfd.File, pfd.Blob, serveDirectReqParams)
	if err != nil {
		if errors.Is(err, packages_model.ErrPackageFileNotExist) {
			apiError(ctx, http.StatusNotFound, err)
			return
		}
		apiError(ctx, http.StatusInternalServerError, err)
		return
	}

	opts := &context.ServeHeaderOptions{
		ContentType:        pfd.Properties.GetByName(container_module.PropertyMediaType),
		RedirectStatusCode: http.StatusTemporaryRedirect,
		AdditionalHeaders: map[string][]string{
			"Docker-Distribution-Api-Version": {"registry/2.0"},
		},
	}

	if d := pfd.Properties.GetByName(container_module.PropertyDigest); d != "" {
		opts.AdditionalHeaders["Docker-Content-Digest"] = []string{d}
		opts.AdditionalHeaders["ETag"] = []string{fmt.Sprintf(`"%s"`, d)}
	}

	helper.ServePackageFile(ctx, s, u, pf, opts)
}

// https://github.com/opencontainers/distribution-spec/blob/main/spec.md#content-discovery
func GetTagList(ctx *context.Context) {
	image := ctx.Params("image")

	if _, err := packages_model.GetPackageByName(ctx, ctx.Package.Owner.ID, packages_model.TypeContainer, image); err != nil {
		if errors.Is(err, packages_model.ErrPackageNotExist) {
			apiErrorDefined(ctx, container_service.ErrNameUnknown)
		} else {
			apiError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	n := -1
	if ctx.FormTrim("n") != "" {
		n = ctx.FormInt("n")
	}
	last := ctx.FormTrim("last")

	tagList, vals, err := container_service.GetLocalTagList(ctx,
		ctx.Package.Owner.LowerName,
		image,
		last,
		n,
		ctx.Package.Owner.ID)

	if errors.Is(err, packages_model.ErrPackageNotExist) {
		apiErrorDefined(ctx, container_service.ErrNameUnknown)
		return
	} else if err != nil {
		apiError(ctx, http.StatusInternalServerError, err)
		return
	}

	if len(tagList.Tags) > 0 {
		ctx.Resp.Header().Set("Link", fmt.Sprintf(`</v2/%s/%s/tags/list?%s>; rel="next"`, ctx.Package.Owner.LowerName, image, vals.Encode()))
	}

	jsonResponse(ctx, http.StatusOK, tagList)
}
