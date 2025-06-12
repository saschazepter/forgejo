// Copyright 2023, 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package forgefed

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"forgejo.org/modules/validation"

	ap "github.com/go-ap/activitypub"
)

func Test_NewForgeUndoLike(t *testing.T) {
	actorIRI := "https://repo.prod.meissa.de/api/v1/activitypub/user-id/1"
	objectIRI := "https://codeberg.org/api/v1/activitypub/repository-id/1"
	want := []byte(`{"type":"Undo","startTime":"2024-03-27T00:00:00Z",` +
		`"actor":"https://repo.prod.meissa.de/api/v1/activitypub/user-id/1",` +
		`"object":{` +
		`"type":"Like",` +
		`"actor":"https://repo.prod.meissa.de/api/v1/activitypub/user-id/1",` +
		`"object":"https://codeberg.org/api/v1/activitypub/repository-id/1"}}`)

	startTime, _ := time.Parse("2006-Jan-02", "2024-Mar-27")
	sut, err := NewForgeUndoLike(actorIRI, objectIRI, startTime)
	if err != nil {
		t.Errorf("unexpected error: %v\n", err)
	}
	if valid, _ := validation.IsValid(sut); !valid {
		t.Errorf("sut expected to be valid: %v\n", sut.Validate())
	}

	got, err := sut.MarshalJSON()
	if err != nil {
		t.Errorf("MarshalJSON() error = \"%v\"", err)
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MarshalJSON() got = %q, want %q", got, want)
	}
}

func Test_UndoLikeMarshalJSON(t *testing.T) {
	type testPair struct {
		item    ForgeUndoLike
		want    []byte
		wantErr error
	}

	startTime, _ := time.Parse("2006-Jan-02", "2024-Mar-27")
	like, _ := NewForgeLike("https://repo.prod.meissa.de/api/v1/activitypub/user-id/1", "https://codeberg.org/api/v1/activitypub/repository-id/1", startTime)
	tests := map[string]testPair{
		"empty": {
			item: ForgeUndoLike{},
			want: nil,
		},
		"valid": {
			item: ForgeUndoLike{
				Activity: ap.Activity{
					StartTime: startTime,
					Actor:     ap.IRI("https://repo.prod.meissa.de/api/v1/activitypub/user-id/1"),
					Type:      "Undo",
					Object:    like,
				},
			},
			want: []byte(`{"type":"Undo",` +
				`"startTime":"2024-03-27T00:00:00Z",` +
				`"actor":"https://repo.prod.meissa.de/api/v1/activitypub/user-id/1",` +
				`"object":{` +
				`"type":"Like",` +
				`"startTime":"2024-03-27T00:00:00Z",` +
				`"actor":"https://repo.prod.meissa.de/api/v1/activitypub/user-id/1",` +
				`"object":"https://codeberg.org/api/v1/activitypub/repository-id/1"}}`),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := tt.item.MarshalJSON()
			if (err != nil || tt.wantErr != nil) && tt.wantErr.Error() != err.Error() {
				t.Errorf("MarshalJSON() error = \"%v\", wantErr \"%v\"", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MarshalJSON() got = %q\nwant %q", got, tt.want)
			}
		})
	}
}

func Test_UndoLikeUnmarshalJSON(t *testing.T) {
	type testPair struct {
		item    []byte
		want    *ForgeUndoLike
		wantErr error
	}

	startTime, _ := time.Parse("2006-Jan-02", "2024-Mar-27")
	like, _ := NewForgeLike("https://repo.prod.meissa.de/api/v1/activitypub/user-id/1", "https://codeberg.org/api/v1/activitypub/repository-id/1", startTime)

	tests := map[string]testPair{
		"valid": {
			item: []byte(`{"type":"Undo",` +
				`"startTime":"2024-03-27T00:00:00Z",` +
				`"actor":"https://repo.prod.meissa.de/api/v1/activitypub/user-id/1",` +
				`"object":{` +
				`"type":"Like",` +
				`"startTime":"2024-03-27T00:00:00Z",` +
				`"actor":"https://repo.prod.meissa.de/api/v1/activitypub/user-id/1",` +
				`"object":"https://codeberg.org/api/v1/activitypub/repository-id/1"}}`),
			want: &ForgeUndoLike{
				Activity: ap.Activity{
					StartTime: startTime,
					Actor:     ap.IRI("https://repo.prod.meissa.de/api/v1/activitypub/user-id/1"),
					Type:      "Undo",
					Object:    like,
				},
			},
			wantErr: nil,
		},
		"invalid": {
			item:    []byte(`invalid JSON`),
			want:    nil,
			wantErr: errors.New("cannot parse JSON"),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got := new(ForgeUndoLike)
			err := got.UnmarshalJSON(test.item)
			if test.wantErr != nil {
				if err == nil {
					t.Errorf("UnmarshalJSON() error = nil, wantErr \"%v\"", test.wantErr)
				} else if !strings.Contains(err.Error(), test.wantErr.Error()) {
					t.Errorf("UnmarshalJSON() error = \"%v\", wantErr \"%v\"", err, test.wantErr)
				}
				return
			}
			remarshalledgot, _ := got.MarshalJSON()
			remarshalledwant, _ := test.want.MarshalJSON()
			if !reflect.DeepEqual(remarshalledgot, remarshalledwant) {
				t.Errorf("UnmarshalJSON() got = %#v\nwant %#v", got, test.want)
			}
		})
	}
}

func TestActivityValidationUndo(t *testing.T) {
	sut := new(ForgeUndoLike)

	_ = sut.UnmarshalJSON([]byte(`
		{"type":"Undo",
		 "startTime":"2024-03-27T00:00:00Z",
		 "actor":"https://repo.prod.meissa.de/api/v1/activitypub/user-id/1",
		 "object":{
		   "type":"Like",
		   "actor":"https://repo.prod.meissa.de/api/v1/activitypub/user-id/1",
		   "object":"https://codeberg.org/api/v1/activitypub/repository-id/1"}}`))
	if res, _ := validation.IsValid(sut); !res {
		t.Errorf("sut expected to be valid: %v\n", sut.Validate())
	}

	_ = sut.UnmarshalJSON([]byte(`
		{"startTime":"2024-03-27T00:00:00Z",
		"actor":"https://repo.prod.meissa.de/api/v1/activitypub/user-id/1",
		"object":{
		  "type":"Like",
		  "startTime":"2024-03-27T00:00:00Z",
		  "actor":"https://repo.prod.meissa.de/api/v1/activitypub/user-id/1",
		  "object":"https://codeberg.org/api/v1/activitypub/repository-id/1"}}`))
	if err := validateAndCheckError(sut, "type should not be empty"); err != nil {
		t.Error(*err)
	}

	_ = sut.UnmarshalJSON([]byte(`
		{"type":"Undo",
		 "startTime":"2024-03-27T00:00:00Z",
		 "object":{
		   "type":"Like",
		   "actor":"https://repo.prod.meissa.de/api/v1/activitypub/user-id/1",
		   "object":"https://codeberg.org/api/v1/activitypub/repository-id/1"}}`))
	if err := validateAndCheckError(sut, "Actor should not be nil."); err != nil {
		t.Error(*err)
	}

	_ = sut.UnmarshalJSON([]byte(`
		{"type":"Undo",
		"startTime":"2024-03-27T00:00:00Z",
		"actor":"string",
		"object":{
		"type":"Like",
			"actor":"https://repo.prod.meissa.de/api/v1/activitypub/user-id/1",
			"object":"https://codeberg.org/api/v1/activitypub/repository-id/1"}}`))
	if err := validateAndCheckError(sut, "Actor should not be nil."); err != nil {
		t.Error(*err)
	}

	_ = sut.UnmarshalJSON([]byte(`
		{"type":"Undo",
		"startTime":"2024-03-27T00:00:00Z",
		"actor":"https://repo.prod.meissa.de/api/v1/activitypub/user-id/1"
		}`))
	if err := validateAndCheckError(sut, "object should not be empty."); err != nil {
		t.Error(*err)
	}

	_ = sut.UnmarshalJSON([]byte(`
		{"type":"Undo",
		"startTime":"2024-03-27T00:00:00Z",
		"actor":"https://repo.prod.meissa.de/api/v1/activitypub/user-id/1",
		"object":{
		  "startTime":"2024-03-27T00:00:00Z",
		  "actor":"https://repo.prod.meissa.de/api/v1/activitypub/user-id/1",
		  "object":"https://codeberg.org/api/v1/activitypub/repository-id/1"}}`))
	if err := validateAndCheckError(sut, "object is not of type Activity"); err != nil {
		t.Error(*err)
	}

	_ = sut.UnmarshalJSON([]byte(`
		{"type":"Undo",
		"startTime":"2024-03-27T00:00:00Z",
		"actor":"https://repo.prod.meissa.de/api/v1/activitypub/user-id/1",
		"object":{
		  "type":"Like",
		  "object":""}}`))
	if err := validateAndCheckError(sut, "Object.Actor should not be nil."); err != nil {
		t.Error(*err)
	}

	_ = sut.UnmarshalJSON([]byte(`
		{"type":"Undo",
		"startTime":"2024-03-27T00:00:00Z",
		"actor":"https://repo.prod.meissa.de/api/v1/activitypub/user-id/1",
		"object":{
		  "type":"Like",
		  "startTime":"2024-03-27T00:00:00Z",
		  "actor":"https://repo.prod.meissa.de/api/v1/activitypub/user-id/1"}}`))
	if err := validateAndCheckError(sut, "Object.Object should not be nil."); err != nil {
		t.Error(*err)
	}
}
