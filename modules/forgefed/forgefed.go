// Copyright 2023, 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package forgefed

import (
	ap "github.com/go-ap/activitypub"
	"github.com/valyala/fastjson"
)

const ForgeFedNamespaceURI = "https://forgefed.org/ns"

// GetItemByType instantiates a new ForgeFed object if the type matches
// otherwise it defaults to existing activitypub package typer function.
func GetItemByType(typ ap.ActivityVocabularyType) (ap.Item, error) {
	switch typ {
	case RepositoryType:
		return RepositoryNew(""), nil
	default:
		return ap.GetItemByType(typ)
	}
}

// JSONUnmarshalerFn is the function that will load the data from a fastjson.Value into an Item
// that the go-ap/activitypub package doesn't know about.
func JSONUnmarshalerFn(typ ap.ActivityVocabularyType, val *fastjson.Value, i ap.Item) error {
	switch typ {
	case RepositoryType:
		return OnRepository(i, func(r *Repository) error {
			return JSONLoadRepository(val, r)
		})
	default:
		return nil
	}
}

// NotEmpty is the function that checks if an object is empty
func NotEmpty(i ap.Item) bool {
	if ap.IsNil(i) {
		return false
	}
	switch i.GetType() {
	case RepositoryType:
		r, err := ToRepository(i)
		if err != nil {
			return false
		}
		return ap.NotEmpty(r.Actor)
	default:
		return ap.NotEmpty(i)
	}
}
