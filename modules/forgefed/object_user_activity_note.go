// Copyright 2024, 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package forgefed

import (
	"time"

	user_model "forgejo.org/models/user"
	"forgejo.org/modules/validation"

	ap "github.com/go-ap/activitypub"
)

// ForgeFollow activity data type
// swagger:model
type ForgeUserActivityNote struct {
	// swagger.ignore
	ap.Object
}

func NewForgeUserActivityNoteFromAp(item ap.Item) (ForgeUserActivityNote, error) {
	result := ForgeUserActivityNote{}
	object := item.(*ap.Object)
	result.Object = *object
	if valid, err := validation.IsValid(result); !valid {
		return ForgeUserActivityNote{}, err
	}
	return result, nil
}

// TODO: Unused - might be removed
func newNote(doer *user_model.User, content, id string, published time.Time) (ForgeUserActivityNote, error) {
	note := ForgeUserActivityNote{}
	note.Type = ap.NoteType
	note.AttributedTo = ap.IRI(doer.APActorID())
	note.Content = ap.NaturalLanguageValues{
		{
			Ref:   ap.NilLangRef,
			Value: ap.Content(content),
		},
	}
	note.ID = ap.IRI(id)
	note.Published = published
	note.URL = ap.IRI(id)
	note.To = ap.ItemCollection{
		ap.IRI("https://www.w3.org/ns/activitystreams#Public"),
	}
	note.CC = ap.ItemCollection{
		ap.IRI(doer.APActorID() + "/followers"),
	}

	if valid, err := validation.IsValid(note); !valid {
		return ForgeUserActivityNote{}, err
	}

	return note, nil
}

func (note ForgeUserActivityNote) Validate() []string {
	var result []string
	result = append(result, validation.ValidateNotEmpty(string(note.Type), "type")...)
	result = append(result, validation.ValidateOneOf(string(note.Type), []any{"Note"}, "type")...)
	result = append(result, validation.ValidateNotEmpty(note.Content.String(), "content")...)
	result = append(result, validation.ValidateIDExists(note.URL, "url")...)

	return result
}
