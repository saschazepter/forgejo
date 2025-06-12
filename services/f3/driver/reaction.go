// Copyright Earl Warren <contact@earl-warren.org>
// Copyright Loïc Dachary <loic@dachary.org>
// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"fmt"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	user_model "forgejo.org/models/user"

	"code.forgejo.org/f3/gof3/v3/f3"
	f3_id "code.forgejo.org/f3/gof3/v3/id"
	f3_tree "code.forgejo.org/f3/gof3/v3/tree/f3"
	"code.forgejo.org/f3/gof3/v3/tree/generic"
	f3_util "code.forgejo.org/f3/gof3/v3/util"
)

var _ f3_tree.ForgeDriverInterface = &reaction{}

type reaction struct {
	common

	forgejoReaction *issues_model.Reaction
}

func (o *reaction) SetNative(reaction any) {
	o.forgejoReaction = reaction.(*issues_model.Reaction)
}

func (o *reaction) GetNativeID() string {
	return fmt.Sprintf("%d", o.forgejoReaction.ID)
}

func (o *reaction) NewFormat() f3.Interface {
	node := o.GetNode()
	return node.GetTree().(f3_tree.TreeInterface).NewFormat(node.GetKind())
}

func (o *reaction) ToFormat() f3.Interface {
	if o.forgejoReaction == nil {
		return o.NewFormat()
	}
	return &f3.Reaction{
		Common:  f3.NewCommon(fmt.Sprintf("%d", o.forgejoReaction.ID)),
		UserID:  f3_tree.NewUserReference(o.forgejoReaction.User.ID),
		Content: o.forgejoReaction.Type,
	}
}

func (o *reaction) FromFormat(content f3.Interface) {
	reaction := content.(*f3.Reaction)

	o.forgejoReaction = &issues_model.Reaction{
		ID:     f3_util.ParseInt(reaction.GetID()),
		UserID: reaction.UserID.GetIDAsInt(),
		User: &user_model.User{
			ID: reaction.UserID.GetIDAsInt(),
		},
		Type: reaction.Content,
	}
}

func (o *reaction) Get(ctx context.Context) bool {
	node := o.GetNode()
	o.Trace("%s", node.GetID())

	id := node.GetID().Int64()

	if has, err := db.GetEngine(ctx).Where("ID = ?", id).Get(o.forgejoReaction); err != nil {
		panic(fmt.Errorf("reaction %v %w", id, err))
	} else if !has {
		return false
	}
	if _, err := o.forgejoReaction.LoadUser(ctx); err != nil {
		panic(fmt.Errorf("LoadUser %v %w", *o.forgejoReaction, err))
	}
	return true
}

func (o *reaction) Patch(ctx context.Context) {
	o.Trace("%d", o.forgejoReaction.ID)
	if _, err := db.GetEngine(ctx).ID(o.forgejoReaction.ID).Cols("type").Update(o.forgejoReaction); err != nil {
		panic(fmt.Errorf("UpdateReactionCols: %v %v", o.forgejoReaction, err))
	}
}

func (o *reaction) Put(ctx context.Context) f3_id.NodeID {
	o.Trace("%v", o.forgejoReaction.User)

	sess := db.GetEngine(ctx)

	reactionable := f3_tree.GetReactionable(o.GetNode())
	reactionableID := f3_tree.GetReactionableID(o.GetNode())

	switch reactionable.GetKind() {
	case f3_tree.KindIssue, f3_tree.KindPullRequest:
		project := f3_tree.GetProjectID(o.GetNode())
		issue, err := issues_model.GetIssueByIndex(ctx, project, reactionableID)
		if err != nil {
			panic(fmt.Errorf("GetIssueByIndex %v %w", reactionableID, err))
		}
		o.forgejoReaction.IssueID = issue.ID
	case f3_tree.KindComment:
		o.forgejoReaction.CommentID = reactionableID
	default:
		panic(fmt.Errorf("unexpected type %v", reactionable.GetKind()))
	}

	o.Trace("%v", o.forgejoReaction)

	if _, err := sess.Insert(o.forgejoReaction); err != nil {
		panic(err)
	}
	o.Trace("reaction created %d", o.forgejoReaction.ID)
	return f3_id.NewNodeID(o.forgejoReaction.ID)
}

func (o *reaction) Delete(ctx context.Context) {
	node := o.GetNode()
	o.Trace("%s", node.GetID())

	sess := db.GetEngine(ctx)
	if _, err := sess.Delete(o.forgejoReaction); err != nil {
		panic(err)
	}
}

func newReaction() generic.NodeDriverInterface {
	return &reaction{}
}
