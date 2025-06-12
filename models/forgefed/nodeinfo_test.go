// Copyright 2023 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package forgefed

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"forgejo.org/modules/validation"
)

func Test_NodeInfoWellKnownUnmarshalJSON(t *testing.T) {
	type testPair struct {
		item    []byte
		want    NodeInfoWellKnown
		wantErr error
	}

	tests := map[string]testPair{
		"with href": {
			item: []byte(`{"links":[{"href":"https://federated-repo.prod.meissa.de/api/v1/nodeinfo","rel":"http://nodeinfo.diaspora.software/ns/schema/2.1"}]}`),
			want: NodeInfoWellKnown{
				Href: "https://federated-repo.prod.meissa.de/api/v1/nodeinfo",
			},
		},
		"empty": {
			item:    []byte(``),
			wantErr: errors.New("cannot parse JSON: cannot parse empty string; unparsed tail: \"\""),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := NodeInfoWellKnownUnmarshalJSON(tt.item)
			if (err != nil || tt.wantErr != nil) && tt.wantErr.Error() != err.Error() {
				t.Errorf("UnmarshalJSON() error = \"%v\", wantErr \"%v\"", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("UnmarshalJSON() got = %q, want %q", got, tt.want)
			}
		})
	}
}

func Test_NodeInfoWellKnownValidate(t *testing.T) {
	sut := NodeInfoWellKnown{Href: "https://federated-repo.prod.meissa.de/api/v1/nodeinfo"}
	if b, err := validation.IsValid(sut); !b {
		t.Errorf("sut should be valid, %v, %v", sut, err)
	}

	sut = NodeInfoWellKnown{Href: "./federated-repo.prod.meissa.de/api/v1/nodeinfo"}
	_, err := validation.IsValid(sut)
	if !validation.IsErrNotValid(err) && strings.Contains(err.Error(), "Href has to be absolute\nValue  is not contained in allowed values [http https]") {
		t.Errorf("validation error expected but was: %v\n", err)
	}

	sut = NodeInfoWellKnown{Href: "https://federated-repo.prod.meissa.de/api/v1/nodeinfo?alert=1"}
	_, err = validation.IsValid(sut)
	if !validation.IsErrNotValid(err) && strings.Contains(err.Error(), "Href has to be absolute\nValue  is not contained in allowed values [http https]") {
		t.Errorf("sut should be valid, %v, %v", sut, err)
	}
}

func Test_NewNodeInfoWellKnown(t *testing.T) {
	sut, _ := NewNodeInfoWellKnown([]byte(`{"links":[{"href":"https://federated-repo.prod.meissa.de/api/v1/nodeinfo","rel":"http://nodeinfo.diaspora.software/ns/schema/2.1"}]}`))
	expected := NodeInfoWellKnown{Href: "https://federated-repo.prod.meissa.de/api/v1/nodeinfo"}
	if sut != expected {
		t.Errorf("expected was: %v but was: %v", expected, sut)
	}

	_, err := NewNodeInfoWellKnown([]byte(`invalid`))
	if err == nil {
		t.Error("error was expected here")
	}
}

func Test_NewNodeInfo(t *testing.T) {
	sut, _ := NewNodeInfo([]byte(`{"version":"2.1","software":{"name":"gitea","version":"1.20.0+dev-2539-g5840cc6d3","repository":"https://github.com/go-gitea/gitea.git","homepage":"https://gitea.io/"},"protocols":["activitypub"],"services":{"inbound":[],"outbound":["rss2.0"]},"openRegistrations":true,"usage":{"users":{"total":13,"activeHalfyear":1,"activeMonth":1}},"metadata":{}}`))
	expected := NodeInfo{SoftwareName: "gitea"}
	if sut != expected {
		t.Errorf("expected was: %v but was: %v", expected, sut)
	}

	_, err := NewNodeInfo([]byte(`invalid`))
	if err == nil {
		t.Error("error was expected here")
	}
}
