// Copyright Earl Warren <contact@earl-warren.org>
// Copyright Loïc Dachary <loic@dachary.org>
// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"fmt"

	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"

	f3_tree "code.forgejo.org/f3/gof3/v3/tree/f3"
	"code.forgejo.org/f3/gof3/v3/tree/generic"
)

type attachments struct {
	container
}

func (o *attachments) ListPage(ctx context.Context, page int) generic.ChildrenSlice {
	if page > 1 {
		return generic.NewChildrenSlice(0)
	}

	attachable := f3_tree.GetAttachable(o.GetNode())
	attachableID := f3_tree.GetAttachableID(o.GetNode())

	var attachments []*repo_model.Attachment

	switch attachable.GetKind() {
	case f3_tree.KindRelease:
		release, err := repo_model.GetReleaseByID(ctx, attachableID)
		if err != nil {
			panic(fmt.Errorf("GetReleaseByID %v %w", attachableID, err))
		}

		if err := release.LoadAttributes(ctx); err != nil {
			panic(fmt.Errorf("error while listing attachments: %v", err))
		}

		attachments = release.Attachments

	case f3_tree.KindComment:
		comment, err := issues_model.GetCommentByID(ctx, attachableID)
		if err != nil {
			panic(fmt.Errorf("GetCommentByID %v %w", attachableID, err))
		}

		if err := comment.LoadAttachments(ctx); err != nil {
			panic(fmt.Errorf("error while listing attachments: %v", err))
		}

		attachments = comment.Attachments

	case f3_tree.KindIssue, f3_tree.KindPullRequest:
		repoID := f3_tree.GetProjectID(o.GetNode())
		issue, err := issues_model.GetIssueByIndex(ctx, repoID, attachableID)
		if err != nil {
			panic(fmt.Errorf("GetIssueByID %v %w", attachableID, err))
		}

		if err := issue.LoadAttachments(ctx); err != nil {
			panic(fmt.Errorf("error while listing attachments: %v", err))
		}

		attachments = issue.Attachments

	default:
		panic(fmt.Errorf("unexpected type %s", attachable.GetKind()))
	}

	return f3_tree.ConvertListed(ctx, o.GetNode(), f3_tree.ConvertToAny(attachments...)...)
}

func newAttachments() generic.NodeDriverInterface {
	return &attachments{}
}
