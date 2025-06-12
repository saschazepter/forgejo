// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package container

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetResponseHeaders(t *testing.T) {
	t.Run("Content-Length for empty content", func(t *testing.T) {
		recorder := httptest.NewRecorder()

		setResponseHeaders(recorder, &containerHeaders{
			Status:        http.StatusOK,
			ContentLength: 0, // Empty blob
			ContentDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		})

		assert.Equal(t, "0", recorder.Header().Get("Content-Length"))
		assert.Equal(t, "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", recorder.Header().Get("Docker-Content-Digest"))
		assert.Equal(t, "registry/2.0", recorder.Header().Get("Docker-Distribution-Api-Version"))
		assert.Equal(t, http.StatusOK, recorder.Code)
	})

	t.Run("Content-Length for non-empty content", func(t *testing.T) {
		recorder := httptest.NewRecorder()

		setResponseHeaders(recorder, &containerHeaders{
			Status:        http.StatusOK,
			ContentLength: 1024,
			ContentDigest: "sha256:abcd1234",
		})

		assert.Equal(t, "1024", recorder.Header().Get("Content-Length"))
		assert.Equal(t, "sha256:abcd1234", recorder.Header().Get("Docker-Content-Digest"))
	})

	t.Run("All headers set correctly", func(t *testing.T) {
		recorder := httptest.NewRecorder()

		setResponseHeaders(recorder, &containerHeaders{
			Status:        http.StatusAccepted,
			ContentLength: 512,
			ContentDigest: "sha256:test123",
			ContentType:   "application/vnd.oci.image.manifest.v1+json",
			Location:      "/v2/test/repo/blobs/uploads/uuid123",
			Range:         "0-511",
			UploadUUID:    "uuid123",
		})

		assert.Equal(t, "512", recorder.Header().Get("Content-Length"))
		assert.Equal(t, "sha256:test123", recorder.Header().Get("Docker-Content-Digest"))
		assert.Equal(t, "application/vnd.oci.image.manifest.v1+json", recorder.Header().Get("Content-Type"))
		assert.Equal(t, "/v2/test/repo/blobs/uploads/uuid123", recorder.Header().Get("Location"))
		assert.Equal(t, "0-511", recorder.Header().Get("Range"))
		assert.Equal(t, "uuid123", recorder.Header().Get("Docker-Upload-Uuid"))
		assert.Equal(t, "registry/2.0", recorder.Header().Get("Docker-Distribution-Api-Version"))
		assert.Equal(t, `"sha256:test123"`, recorder.Header().Get("ETag"))
		assert.Equal(t, http.StatusAccepted, recorder.Code)
	})
}

// TestResponseHeadersForEmptyBlobs tests the core fix for ORAS empty blob support
func TestResponseHeadersForEmptyBlobs(t *testing.T) {
	t.Run("Content-Length set for empty blob", func(t *testing.T) {
		recorder := httptest.NewRecorder()

		// This tests the main fix: empty blobs should have Content-Length: 0
		setResponseHeaders(recorder, &containerHeaders{
			Status:        http.StatusOK,
			ContentLength: 0, // Empty blob (like empty config in ORAS artifacts)
			ContentDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		})

		// The key fix: Content-Length should be set even for 0-byte blobs
		assert.Equal(t, "0", recorder.Header().Get("Content-Length"))
		assert.Equal(t, "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", recorder.Header().Get("Docker-Content-Digest"))
		assert.Equal(t, "registry/2.0", recorder.Header().Get("Docker-Distribution-Api-Version"))
		assert.Equal(t, `"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"`, recorder.Header().Get("ETag"))
		assert.Equal(t, http.StatusOK, recorder.Code)
	})

	t.Run("Content-Length set for regular blob", func(t *testing.T) {
		recorder := httptest.NewRecorder()

		setResponseHeaders(recorder, &containerHeaders{
			Status:        http.StatusOK,
			ContentLength: 1024,
			ContentDigest: "sha256:abcd1234",
		})

		assert.Equal(t, "1024", recorder.Header().Get("Content-Length"))
		assert.Equal(t, "sha256:abcd1234", recorder.Header().Get("Docker-Content-Digest"))
	})

	t.Run("All headers set correctly", func(t *testing.T) {
		recorder := httptest.NewRecorder()

		setResponseHeaders(recorder, &containerHeaders{
			Status:        http.StatusAccepted,
			ContentLength: 512,
			ContentDigest: "sha256:test123",
			ContentType:   "application/vnd.oci.image.manifest.v1+json",
			Location:      "/v2/test/repo/blobs/uploads/uuid123",
			Range:         "0-511",
			UploadUUID:    "uuid123",
		})

		assert.Equal(t, "512", recorder.Header().Get("Content-Length"))
		assert.Equal(t, "sha256:test123", recorder.Header().Get("Docker-Content-Digest"))
		assert.Equal(t, "application/vnd.oci.image.manifest.v1+json", recorder.Header().Get("Content-Type"))
		assert.Equal(t, "/v2/test/repo/blobs/uploads/uuid123", recorder.Header().Get("Location"))
		assert.Equal(t, "0-511", recorder.Header().Get("Range"))
		assert.Equal(t, "uuid123", recorder.Header().Get("Docker-Upload-Uuid"))
		assert.Equal(t, "registry/2.0", recorder.Header().Get("Docker-Distribution-Api-Version"))
		assert.Equal(t, `"sha256:test123"`, recorder.Header().Get("ETag"))
		assert.Equal(t, http.StatusAccepted, recorder.Code)
	})
}
