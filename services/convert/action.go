// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package convert

import (
	"context"

	actions_model "forgejo.org/models/actions"
	access_model "forgejo.org/models/perm/access"
	api "forgejo.org/modules/structs"
)

// ToActionRun convert actions_model.User to api.ActionRun
// the run needs all attributes loaded
func ToActionRun(ctx context.Context, run *actions_model.ActionRun) *api.ActionRun {
	if run == nil {
		return nil
	}

	// The doer is the one whose perspective is used to view this ActionRun.
	// In the best case we use the user that created the webhook.
	// Unfortunately we don't know who that was.
	// So instead we use the repo owner, who is able to create webhooks and allow others to do so by making them repo admins.
	// This is pretty close to perfect.
	doer := run.Repo.Owner
	permissionInRepo, _ := access_model.GetUserRepoPermission(ctx, run.Repo, doer)

	return &api.ActionRun{
		ID:                run.ID,
		Title:             run.Title,
		Repo:              ToRepo(ctx, run.Repo, permissionInRepo),
		WorkflowID:        run.WorkflowID,
		Index:             run.Index,
		TriggerUser:       ToUser(ctx, run.TriggerUser, doer),
		ScheduleID:        run.ScheduleID,
		PrettyRef:         run.PrettyRef(),
		IsRefDeleted:      run.IsRefDeleted,
		CommitSHA:         run.CommitSHA,
		IsForkPullRequest: run.IsForkPullRequest,
		NeedApproval:      run.NeedApproval,
		ApprovedBy:        run.ApprovedBy,
		Event:             run.Event.Event(),
		EventPayload:      run.EventPayload,
		TriggerEvent:      run.TriggerEvent,
		Status:            run.Status.String(),
		Started:           run.Started.AsTime(),
		Stopped:           run.Stopped.AsTime(),
		Created:           run.Created.AsTime(),
		Updated:           run.Updated.AsTime(),
		Duration:          run.Duration(),
		HTMLURL:           run.HTMLURL(),
	}
}
