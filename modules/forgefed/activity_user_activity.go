// Copyright 2024, 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package forgefed

import (
	"fmt"
	"time"

	user_model "forgejo.org/models/user"
	"forgejo.org/modules/validation"

	ap "github.com/go-ap/activitypub"
)

// ForgeFollow activity data type
// swagger:model
type ForgeUserActivity struct {
	ap.Activity
	Note ForgeUserActivityNote
}

func NewForgeUserActivityFromAp(activity ap.Activity) (ForgeUserActivity, error) {
	result := ForgeUserActivity{}
	result.Activity = activity
	note, err := NewForgeUserActivityNoteFromAp(activity.Object)
	if err != nil {
		return ForgeUserActivity{}, err
	}
	result.Note = note
	if valid, err := validation.IsValid(result); !valid {
		return ForgeUserActivity{}, err
	}
	return result, nil
}

func NewForgeUserActivity(doer *user_model.User, actionID int64, content string) (ForgeUserActivity, error) {
	id := fmt.Sprintf("%s/activities/%d", doer.APActorID(), actionID)
	published := time.Now()

	result := ForgeUserActivity{}
	result.ID = ap.IRI(id + "/activity")
	result.Type = ap.CreateType
	result.Actor = ap.IRI(doer.APActorID())
	result.Published = published
	result.To = ap.ItemCollection{
		ap.IRI("https://www.w3.org/ns/activitystreams#Public"),
	}
	result.CC = ap.ItemCollection{
		ap.IRI(doer.APActorID() + "/followers"),
	}
	note, err := newNote(doer, content, id, published)
	if err != nil {
		return ForgeUserActivity{}, err
	}
	result.Object = note

	return result, nil
}

func (userActivity ForgeUserActivity) Validate() []string {
	var result []string
	result = append(result, validation.ValidateNotEmpty(string(userActivity.Type), "type")...)
	result = append(result, validation.ValidateOneOf(string(userActivity.Type), []any{"Create"}, "type")...)
	result = append(result, validation.ValidateIDExists(userActivity.Actor, "actor")...)

	if len(userActivity.To) == 0 {
		result = append(result, "Missing to")
	}
	if len(userActivity.CC) == 0 {
		result = append(result, "Missing cc")
	}

	result = append(result, userActivity.Note.Validate()...)

	return result
}
