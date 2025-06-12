// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"forgejo.org/modules/storage"
	"forgejo.org/routers/api/actions"
	actions_service "forgejo.org/services/actions"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func toProtoJSON(m protoreflect.ProtoMessage) io.Reader {
	resp, _ := protojson.Marshal(m)
	buf := bytes.Buffer{}
	buf.Write(resp)
	return &buf
}

func uploadArtifact(t *testing.T, body string) string {
	token, err := actions_service.CreateAuthorizationToken(48, 792, 193)
	require.NoError(t, err)

	// acquire artifact upload url
	req := NewRequestWithBody(t, "POST", "/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact", toProtoJSON(&actions.CreateArtifactRequest{
		Version:                 4,
		Name:                    "artifact",
		WorkflowRunBackendId:    "792",
		WorkflowJobRunBackendId: "193",
	})).AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	var uploadResp actions.CreateArtifactResponse
	protojson.Unmarshal(resp.Body.Bytes(), &uploadResp)
	assert.True(t, uploadResp.Ok)
	assert.Contains(t, uploadResp.SignedUploadUrl, "/twirp/github.actions.results.api.v1.ArtifactService/UploadArtifact")

	// get upload url
	idx := strings.Index(uploadResp.SignedUploadUrl, "/twirp/")
	url := uploadResp.SignedUploadUrl[idx:] + "&comp=block"

	// upload artifact chunk
	req = NewRequestWithBody(t, "PUT", url, strings.NewReader(body))
	MakeRequest(t, req, http.StatusCreated)

	t.Log("Create artifact confirm")

	sha := sha256.Sum256([]byte(body))

	// confirm artifact upload
	req = NewRequestWithBody(t, "POST", "/twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact", toProtoJSON(&actions.FinalizeArtifactRequest{
		Name:                    "artifact",
		Size:                    1024,
		Hash:                    wrapperspb.String("sha256:" + hex.EncodeToString(sha[:])),
		WorkflowRunBackendId:    "792",
		WorkflowJobRunBackendId: "193",
	})).
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	var finalizeResp actions.FinalizeArtifactResponse
	protojson.Unmarshal(resp.Body.Bytes(), &finalizeResp)
	assert.True(t, finalizeResp.Ok)
	return token
}

func TestActionsArtifactV4UploadSingleFile(t *testing.T) {
	defer prepareTestEnvActionsArtifacts(t)()
	body := strings.Repeat("A", 1024)
	uploadArtifact(t, body)
}

func TestActionsArtifactV4UploadSingleFileWrongChecksum(t *testing.T) {
	defer prepareTestEnvActionsArtifacts(t)()

	token, err := actions_service.CreateAuthorizationToken(48, 792, 193)
	require.NoError(t, err)

	// acquire artifact upload url
	req := NewRequestWithBody(t, "POST", "/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact", toProtoJSON(&actions.CreateArtifactRequest{
		Version:                 4,
		Name:                    "artifact-invalid-checksum",
		WorkflowRunBackendId:    "792",
		WorkflowJobRunBackendId: "193",
	})).AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	var uploadResp actions.CreateArtifactResponse
	protojson.Unmarshal(resp.Body.Bytes(), &uploadResp)
	assert.True(t, uploadResp.Ok)
	assert.Contains(t, uploadResp.SignedUploadUrl, "/twirp/github.actions.results.api.v1.ArtifactService/UploadArtifact")

	// get upload url
	idx := strings.Index(uploadResp.SignedUploadUrl, "/twirp/")
	url := uploadResp.SignedUploadUrl[idx:] + "&comp=block"

	// upload artifact chunk
	body := strings.Repeat("B", 1024)
	req = NewRequestWithBody(t, "PUT", url, strings.NewReader(body))
	MakeRequest(t, req, http.StatusCreated)

	t.Log("Create artifact confirm")

	sha := sha256.Sum256([]byte(strings.Repeat("A", 1024)))

	// confirm artifact upload
	req = NewRequestWithBody(t, "POST", "/twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact", toProtoJSON(&actions.FinalizeArtifactRequest{
		Name:                    "artifact-invalid-checksum",
		Size:                    1024,
		Hash:                    wrapperspb.String("sha256:" + hex.EncodeToString(sha[:])),
		WorkflowRunBackendId:    "792",
		WorkflowJobRunBackendId: "193",
	})).
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusInternalServerError)
}

func TestActionsArtifactV4UploadSingleFileWithRetentionDays(t *testing.T) {
	defer prepareTestEnvActionsArtifacts(t)()

	token, err := actions_service.CreateAuthorizationToken(48, 792, 193)
	require.NoError(t, err)

	// acquire artifact upload url
	req := NewRequestWithBody(t, "POST", "/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact", toProtoJSON(&actions.CreateArtifactRequest{
		Version:                 4,
		ExpiresAt:               timestamppb.New(time.Now().Add(5 * 24 * time.Hour)),
		Name:                    "artifactWithRetentionDays",
		WorkflowRunBackendId:    "792",
		WorkflowJobRunBackendId: "193",
	})).AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	var uploadResp actions.CreateArtifactResponse
	protojson.Unmarshal(resp.Body.Bytes(), &uploadResp)
	assert.True(t, uploadResp.Ok)
	assert.Contains(t, uploadResp.SignedUploadUrl, "/twirp/github.actions.results.api.v1.ArtifactService/UploadArtifact")

	// get upload url
	idx := strings.Index(uploadResp.SignedUploadUrl, "/twirp/")
	url := uploadResp.SignedUploadUrl[idx:] + "&comp=block"

	// upload artifact chunk
	body := strings.Repeat("A", 1024)
	req = NewRequestWithBody(t, "PUT", url, strings.NewReader(body))
	MakeRequest(t, req, http.StatusCreated)

	t.Log("Create artifact confirm")

	sha := sha256.Sum256([]byte(body))

	// confirm artifact upload
	req = NewRequestWithBody(t, "POST", "/twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact", toProtoJSON(&actions.FinalizeArtifactRequest{
		Name:                    "artifactWithRetentionDays",
		Size:                    1024,
		Hash:                    wrapperspb.String("sha256:" + hex.EncodeToString(sha[:])),
		WorkflowRunBackendId:    "792",
		WorkflowJobRunBackendId: "193",
	})).
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	var finalizeResp actions.FinalizeArtifactResponse
	protojson.Unmarshal(resp.Body.Bytes(), &finalizeResp)
	assert.True(t, finalizeResp.Ok)
}

func TestActionsArtifactV4UploadSingleFileWithPotentialHarmfulBlockID(t *testing.T) {
	defer prepareTestEnvActionsArtifacts(t)()

	token, err := actions_service.CreateAuthorizationToken(48, 792, 193)
	require.NoError(t, err)

	// acquire artifact upload url
	req := NewRequestWithBody(t, "POST", "/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact", toProtoJSON(&actions.CreateArtifactRequest{
		Version:                 4,
		Name:                    "artifactWithPotentialHarmfulBlockID",
		WorkflowRunBackendId:    "792",
		WorkflowJobRunBackendId: "193",
	})).AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	var uploadResp actions.CreateArtifactResponse
	protojson.Unmarshal(resp.Body.Bytes(), &uploadResp)
	assert.True(t, uploadResp.Ok)
	assert.Contains(t, uploadResp.SignedUploadUrl, "/twirp/github.actions.results.api.v1.ArtifactService/UploadArtifact")

	// get upload urls
	idx := strings.Index(uploadResp.SignedUploadUrl, "/twirp/")
	url := uploadResp.SignedUploadUrl[idx:] + "&comp=block&blockid=%2f..%2fmyfile"
	blockListURL := uploadResp.SignedUploadUrl[idx:] + "&comp=blocklist"

	// upload artifact chunk
	body := strings.Repeat("A", 1024)
	req = NewRequestWithBody(t, "PUT", url, strings.NewReader(body))
	MakeRequest(t, req, http.StatusCreated)

	// verify that the exploit didn't work
	_, err = storage.Actions.Stat("myfile")
	require.Error(t, err)

	// upload artifact blockList
	blockList := &actions.BlockList{
		Latest: []string{
			"/../myfile",
		},
	}
	rawBlockList, err := xml.Marshal(blockList)
	require.NoError(t, err)
	req = NewRequestWithBody(t, "PUT", blockListURL, bytes.NewReader(rawBlockList))
	MakeRequest(t, req, http.StatusCreated)

	t.Log("Create artifact confirm")

	sha := sha256.Sum256([]byte(body))

	// confirm artifact upload
	req = NewRequestWithBody(t, "POST", "/twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact", toProtoJSON(&actions.FinalizeArtifactRequest{
		Name:                    "artifactWithPotentialHarmfulBlockID",
		Size:                    1024,
		Hash:                    wrapperspb.String("sha256:" + hex.EncodeToString(sha[:])),
		WorkflowRunBackendId:    "792",
		WorkflowJobRunBackendId: "193",
	})).
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	var finalizeResp actions.FinalizeArtifactResponse
	protojson.Unmarshal(resp.Body.Bytes(), &finalizeResp)
	assert.True(t, finalizeResp.Ok)
}

func TestActionsArtifactV4UploadSingleFileWithChunksOutOfOrder(t *testing.T) {
	defer prepareTestEnvActionsArtifacts(t)()

	token, err := actions_service.CreateAuthorizationToken(48, 792, 193)
	require.NoError(t, err)

	// acquire artifact upload url
	req := NewRequestWithBody(t, "POST", "/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact", toProtoJSON(&actions.CreateArtifactRequest{
		Version:                 4,
		Name:                    "artifactWithChunksOutOfOrder",
		WorkflowRunBackendId:    "792",
		WorkflowJobRunBackendId: "193",
	})).AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	var uploadResp actions.CreateArtifactResponse
	protojson.Unmarshal(resp.Body.Bytes(), &uploadResp)
	assert.True(t, uploadResp.Ok)
	assert.Contains(t, uploadResp.SignedUploadUrl, "/twirp/github.actions.results.api.v1.ArtifactService/UploadArtifact")

	// get upload urls
	idx := strings.Index(uploadResp.SignedUploadUrl, "/twirp/")
	block1URL := uploadResp.SignedUploadUrl[idx:] + "&comp=block&blockid=block1"
	block2URL := uploadResp.SignedUploadUrl[idx:] + "&comp=block&blockid=block2"
	blockListURL := uploadResp.SignedUploadUrl[idx:] + "&comp=blocklist"

	// upload artifact chunks
	bodyb := strings.Repeat("B", 1024)
	req = NewRequestWithBody(t, "PUT", block2URL, strings.NewReader(bodyb))
	MakeRequest(t, req, http.StatusCreated)

	bodya := strings.Repeat("A", 1024)
	req = NewRequestWithBody(t, "PUT", block1URL, strings.NewReader(bodya))
	MakeRequest(t, req, http.StatusCreated)

	// upload artifact blockList
	blockList := &actions.BlockList{
		Latest: []string{
			"block1",
			"block2",
		},
	}
	rawBlockList, err := xml.Marshal(blockList)
	require.NoError(t, err)
	req = NewRequestWithBody(t, "PUT", blockListURL, bytes.NewReader(rawBlockList))
	MakeRequest(t, req, http.StatusCreated)

	t.Log("Create artifact confirm")

	sha := sha256.Sum256([]byte(bodya + bodyb))

	// confirm artifact upload
	req = NewRequestWithBody(t, "POST", "/twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact", toProtoJSON(&actions.FinalizeArtifactRequest{
		Name:                    "artifactWithChunksOutOfOrder",
		Size:                    2048,
		Hash:                    wrapperspb.String("sha256:" + hex.EncodeToString(sha[:])),
		WorkflowRunBackendId:    "792",
		WorkflowJobRunBackendId: "193",
	})).
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	var finalizeResp actions.FinalizeArtifactResponse
	protojson.Unmarshal(resp.Body.Bytes(), &finalizeResp)
	assert.True(t, finalizeResp.Ok)
}

func TestActionsArtifactV4DownloadSingle(t *testing.T) {
	defer prepareTestEnvActionsArtifacts(t)()

	token, err := actions_service.CreateAuthorizationToken(48, 792, 193)
	require.NoError(t, err)

	// acquire artifact upload url
	req := NewRequestWithBody(t, "POST", "/twirp/github.actions.results.api.v1.ArtifactService/ListArtifacts", toProtoJSON(&actions.ListArtifactsRequest{
		NameFilter:              wrapperspb.String("artifact-v4-download"),
		WorkflowRunBackendId:    "792",
		WorkflowJobRunBackendId: "193",
	})).AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	var listResp actions.ListArtifactsResponse
	protojson.Unmarshal(resp.Body.Bytes(), &listResp)
	assert.Len(t, listResp.Artifacts, 1)

	// confirm artifact upload
	req = NewRequestWithBody(t, "POST", "/twirp/github.actions.results.api.v1.ArtifactService/GetSignedArtifactURL", toProtoJSON(&actions.GetSignedArtifactURLRequest{
		Name:                    "artifact-v4-download",
		WorkflowRunBackendId:    "792",
		WorkflowJobRunBackendId: "193",
	})).
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	var finalizeResp actions.GetSignedArtifactURLResponse
	protojson.Unmarshal(resp.Body.Bytes(), &finalizeResp)
	assert.NotEmpty(t, finalizeResp.SignedUrl)

	req = NewRequest(t, "GET", finalizeResp.SignedUrl)
	resp = MakeRequest(t, req, http.StatusOK)
	body := strings.Repeat("D", 1024)
	assert.Equal(t, "bytes", resp.Header().Get("accept-ranges"))
	assert.Equal(t, body, resp.Body.String())

	// Download artifact via user-facing URL
	req = NewRequest(t, "GET", "/user5/repo4/actions/runs/188/artifacts/artifact-v4-download")
	resp = MakeRequest(t, req, http.StatusOK)
	assert.Equal(t, "bytes", resp.Header().Get("accept-ranges"))
	assert.Equal(t, body, resp.Body.String())

	// Partial artifact download
	req = NewRequest(t, "GET", "/user5/repo4/actions/runs/188/artifacts/artifact-v4-download").SetHeader("range", "bytes=0-99")
	resp = MakeRequest(t, req, http.StatusPartialContent)
	body = strings.Repeat("D", 100)
	assert.Equal(t, "bytes 0-99/1024", resp.Header().Get("content-range"))
	assert.Equal(t, body, resp.Body.String())
}

func TestActionsArtifactV4DownloadRange(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	bstr := strings.Repeat("D", 100)
	body := strings.Repeat("A", 100) + bstr
	token := uploadArtifact(t, body)

	// Download (Actions API)
	req := NewRequestWithBody(t, "POST", "/twirp/github.actions.results.api.v1.ArtifactService/GetSignedArtifactURL", toProtoJSON(&actions.GetSignedArtifactURLRequest{
		Name:                    "artifact-v4-download",
		WorkflowRunBackendId:    "792",
		WorkflowJobRunBackendId: "193",
	})).
		AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	var finalizeResp actions.GetSignedArtifactURLResponse
	protojson.Unmarshal(resp.Body.Bytes(), &finalizeResp)
	assert.NotEmpty(t, finalizeResp.SignedUrl)

	req = NewRequest(t, "GET", finalizeResp.SignedUrl).SetHeader("range", "bytes=100-199")
	resp = MakeRequest(t, req, http.StatusPartialContent)
	assert.Equal(t, "bytes 100-199/1024", resp.Header().Get("content-range"))
	assert.Equal(t, bstr, resp.Body.String())

	// Download (user-facing API)
	req = NewRequest(t, "GET", "/user5/repo4/actions/runs/188/artifacts/artifact-v4-download").SetHeader("range", "bytes=100-199")
	resp = MakeRequest(t, req, http.StatusPartialContent)
	assert.Equal(t, "bytes 100-199/1024", resp.Header().Get("content-range"))
	assert.Equal(t, bstr, resp.Body.String())
}

func TestActionsArtifactV4Delete(t *testing.T) {
	defer prepareTestEnvActionsArtifacts(t)()

	token, err := actions_service.CreateAuthorizationToken(48, 792, 193)
	require.NoError(t, err)

	// delete artifact by name
	req := NewRequestWithBody(t, "POST", "/twirp/github.actions.results.api.v1.ArtifactService/DeleteArtifact", toProtoJSON(&actions.DeleteArtifactRequest{
		Name:                    "artifact-v4-download",
		WorkflowRunBackendId:    "792",
		WorkflowJobRunBackendId: "193",
	})).AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	var deleteResp actions.DeleteArtifactResponse
	protojson.Unmarshal(resp.Body.Bytes(), &deleteResp)
	assert.True(t, deleteResp.Ok)
}
