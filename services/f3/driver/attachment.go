// Copyright Earl Warren <contact@earl-warren.org>
// Copyright Loïc Dachary <loic@dachary.org>
// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/storage"
	"forgejo.org/modules/timeutil"
	forgejo_attachment "forgejo.org/services/attachment"

	"code.forgejo.org/f3/gof3/v3/f3"
	f3_id "code.forgejo.org/f3/gof3/v3/id"
	f3_tree "code.forgejo.org/f3/gof3/v3/tree/f3"
	"code.forgejo.org/f3/gof3/v3/tree/generic"
	f3_util "code.forgejo.org/f3/gof3/v3/util"
	"github.com/google/uuid"
)

var _ f3_tree.ForgeDriverInterface = &issue{}

type attachment struct {
	common

	forgejoAttachment *repo_model.Attachment
	sha               string
	contentType       string
	downloadFunc      f3.DownloadFuncType
}

func (o *attachment) SetNative(attachment any) {
	o.forgejoAttachment = attachment.(*repo_model.Attachment)
}

func (o *attachment) GetNativeID() string {
	return fmt.Sprintf("%d", o.forgejoAttachment.ID)
}

func (o *attachment) NewFormat() f3.Interface {
	node := o.GetNode()
	return node.GetTree().(f3_tree.TreeInterface).NewFormat(node.GetKind())
}

func (o *attachment) ToFormat() f3.Interface {
	if o.forgejoAttachment == nil {
		return o.NewFormat()
	}

	return &f3.Attachment{
		Common:        f3.NewCommon(o.GetNativeID()),
		Name:          o.forgejoAttachment.Name,
		ContentType:   o.contentType,
		Size:          o.forgejoAttachment.Size,
		DownloadCount: o.forgejoAttachment.DownloadCount,
		Created:       o.forgejoAttachment.CreatedUnix.AsTime(),
		SHA256:        o.sha,
		DownloadURL:   o.forgejoAttachment.DownloadURL(),
		DownloadFunc:  o.downloadFunc,
	}
}

func (o *attachment) FromFormat(content f3.Interface) {
	attachment := content.(*f3.Attachment)
	o.forgejoAttachment = &repo_model.Attachment{
		ID:                f3_util.ParseInt(attachment.GetID()),
		Name:              attachment.Name,
		Size:              attachment.Size,
		DownloadCount:     attachment.DownloadCount,
		CreatedUnix:       timeutil.TimeStamp(attachment.Created.Unix()),
		CustomDownloadURL: attachment.DownloadURL,
	}
	o.contentType = attachment.ContentType
	o.sha = attachment.SHA256
	o.downloadFunc = attachment.DownloadFunc
}

func (o *attachment) Get(ctx context.Context) bool {
	node := o.GetNode()
	o.Trace("%s", node.GetID())

	id := node.GetID().Int64()

	attachment, err := repo_model.GetAttachmentByID(ctx, id)
	if repo_model.IsErrAttachmentNotExist(err) {
		return false
	}
	if err != nil {
		panic(fmt.Errorf("attachment %v %w", id, err))
	}

	o.forgejoAttachment = attachment

	path := o.forgejoAttachment.RelativePath()

	{
		f, err := storage.Attachments.Open(path)
		if err != nil {
			panic(err)
		}
		hasher := sha256.New()
		if _, err := io.Copy(hasher, f); err != nil {
			panic(fmt.Errorf("io.Copy to hasher: %v", err))
		}
		o.sha = hex.EncodeToString(hasher.Sum(nil))
	}

	o.downloadFunc = func() io.ReadCloser {
		o.Trace("download %s from copy stored in temporary file %s", o.forgejoAttachment.DownloadURL, path)
		f, err := os.Open(path)
		if err != nil {
			panic(err)
		}
		return f
	}
	return true
}

func (o *attachment) Patch(ctx context.Context) {
	o.Trace("%d", o.forgejoAttachment.ID)
	if _, err := db.GetEngine(ctx).ID(o.forgejoAttachment.ID).Cols("name").Update(o.forgejoAttachment); err != nil {
		panic(fmt.Errorf("UpdateAttachmentCols: %v %v", o.forgejoAttachment, err))
	}
}

func (o *attachment) Put(ctx context.Context) f3_id.NodeID {
	node := o.GetNode()
	o.Trace("%s", node.GetID())

	uploader, err := user_model.GetAdminUser(ctx)
	if err != nil {
		panic(fmt.Errorf("GetAdminUser %w", err))
	}

	attachable := f3_tree.GetAttachable(o.GetNode())
	attachableID := f3_tree.GetAttachableID(o.GetNode())

	switch attachable.GetKind() {
	case f3_tree.KindRelease:
		o.forgejoAttachment.ReleaseID = attachableID
	case f3_tree.KindComment:
		o.forgejoAttachment.CommentID = attachableID
	case f3_tree.KindIssue, f3_tree.KindPullRequest:
		o.forgejoAttachment.IssueID = attachableID
	default:
		panic(fmt.Errorf("unexpected type %s", attachable.GetKind()))
	}

	o.forgejoAttachment.UploaderID = uploader.ID
	o.forgejoAttachment.RepoID = f3_tree.GetProjectID(o.GetNode())
	o.forgejoAttachment.UUID = uuid.New().String()

	download := o.downloadFunc()
	defer download.Close()

	_, err = forgejo_attachment.NewAttachment(ctx, o.forgejoAttachment, download, o.forgejoAttachment.Size)
	if err != nil {
		panic(err)
	}

	o.Trace("attachment created %d", o.forgejoAttachment.ID)
	return f3_id.NewNodeID(o.forgejoAttachment.ID)
}

func (o *attachment) Delete(ctx context.Context) {
	node := o.GetNode()
	o.Trace("%s", node.GetID())

	if err := repo_model.DeleteAttachment(ctx, o.forgejoAttachment, true); err != nil {
		panic(err)
	}
}

func newAttachment() generic.NodeDriverInterface {
	return &attachment{}
}
