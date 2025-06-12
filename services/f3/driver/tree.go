// Copyright Earl Warren <contact@earl-warren.org>
// Copyright Loïc Dachary <loic@dachary.org>
// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"fmt"

	forgejo_options "forgejo.org/services/f3/driver/options"

	f3_kind "code.forgejo.org/f3/gof3/v3/kind"
	f3_tree "code.forgejo.org/f3/gof3/v3/tree/f3"
	"code.forgejo.org/f3/gof3/v3/tree/generic"
)

type treeDriver struct {
	generic.NullTreeDriver

	options *forgejo_options.Options
}

func (o *treeDriver) Init() {
	o.NullTreeDriver.Init()
}

func (o *treeDriver) Factory(ctx context.Context, kind f3_kind.Kind) generic.NodeDriverInterface {
	switch kind {
	case f3_tree.KindForge:
		return newForge()
	case f3_tree.KindOrganizations:
		return newOrganizations()
	case f3_tree.KindOrganization:
		return newOrganization()
	case f3_tree.KindUsers:
		return newUsers()
	case f3_tree.KindUser:
		return newUser()
	case f3_tree.KindProjects:
		return newProjects()
	case f3_tree.KindProject:
		return newProject()
	case f3_tree.KindIssues:
		return newIssues()
	case f3_tree.KindIssue:
		return newIssue()
	case f3_tree.KindComments:
		return newComments()
	case f3_tree.KindComment:
		return newComment()
	case f3_tree.KindAttachments:
		return newAttachments()
	case f3_tree.KindAttachment:
		return newAttachment()
	case f3_tree.KindLabels:
		return newLabels()
	case f3_tree.KindLabel:
		return newLabel()
	case f3_tree.KindReactions:
		return newReactions()
	case f3_tree.KindReaction:
		return newReaction()
	case f3_tree.KindReviews:
		return newReviews()
	case f3_tree.KindReview:
		return newReview()
	case f3_tree.KindReviewComments:
		return newReviewComments()
	case f3_tree.KindReviewComment:
		return newReviewComment()
	case f3_tree.KindMilestones:
		return newMilestones()
	case f3_tree.KindMilestone:
		return newMilestone()
	case f3_tree.KindPullRequests:
		return newPullRequests()
	case f3_tree.KindPullRequest:
		return newPullRequest()
	case f3_tree.KindReleases:
		return newReleases()
	case f3_tree.KindRelease:
		return newRelease()
	case f3_tree.KindTopics:
		return newTopics()
	case f3_tree.KindTopic:
		return newTopic()
	case f3_tree.KindRepositories:
		return newRepositories()
	case f3_tree.KindRepository:
		return newRepository(ctx)
	case f3_kind.KindRoot:
		return newRoot(o.GetTree().(f3_tree.TreeInterface).NewFormat(kind))
	default:
		panic(fmt.Errorf("unexpected kind %s", kind))
	}
}

func newTreeDriver(tree generic.TreeInterface, anyOptions any) generic.TreeDriverInterface {
	driver := &treeDriver{
		options: anyOptions.(*forgejo_options.Options),
	}
	driver.Init()
	return driver
}
