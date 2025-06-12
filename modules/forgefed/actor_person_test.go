// Copyright 2023, 2024, 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package forgefed

import (
	"reflect"
	"strings"
	"testing"

	"forgejo.org/modules/validation"

	ap "github.com/go-ap/activitypub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPersonIdFromModel(t *testing.T) {
	expected := PersonID{}
	expected.ID = "1"
	expected.Source = "forgejo"
	expected.HostSchema = "https"
	expected.Path = "api/v1/activitypub/user-id"
	expected.Host = "an.other.host"
	expected.HostPort = 443
	expected.IsPortSupplemented = false
	expected.UnvalidatedInput = "https://an.other.host:443/api/v1/activitypub/user-id/1"

	sut, _ := NewPersonIDFromModel("an.other.host", "https", 443, "forgejo", "1")
	assert.Equal(t, expected, sut)
}

func TestNewPersonId(t *testing.T) {
	var sut, expected PersonID
	var err error

	expected = PersonID{}
	expected.ID = "1"
	expected.Source = "forgejo"
	expected.HostSchema = "https"
	expected.Path = "api/v1/activitypub/user-id"
	expected.Host = "an.other.host"
	expected.HostPort = 443
	expected.IsPortSupplemented = true
	expected.UnvalidatedInput = "https://an.other.host/api/v1/activitypub/user-id/1"

	sut, err = NewPersonID("https://an.other.host/api/v1/activitypub/user-id/1", "forgejo")
	require.NoError(t, err)
	assert.Equal(t, expected, sut)

	expected = PersonID{}
	expected.ID = "1"
	expected.Source = "forgejo"
	expected.HostSchema = "https"
	expected.Path = "api/v1/activitypub/user-id"
	expected.Host = "an.other.host"
	expected.HostPort = 443
	expected.IsPortSupplemented = false
	expected.UnvalidatedInput = "https://an.other.host:443/api/v1/activitypub/user-id/1"

	sut, _ = NewPersonID("https://an.other.host:443/api/v1/activitypub/user-id/1", "forgejo")
	assert.Equal(t, expected, sut)

	expected = PersonID{}
	expected.ID = "1"
	expected.Source = "forgejo"
	expected.HostSchema = "http"
	expected.Path = "api/v1/activitypub/user-id"
	expected.Host = "an.other.host"
	expected.HostPort = 80
	expected.IsPortSupplemented = false
	expected.UnvalidatedInput = "http://an.other.host:80/api/v1/activitypub/user-id/1"

	sut, _ = NewPersonID("http://an.other.host:80/api/v1/activitypub/user-id/1", "forgejo")
	assert.Equal(t, expected, sut)

	expected = PersonID{}
	expected.ID = "1"
	expected.Source = "forgejo"
	expected.HostSchema = "https"
	expected.Path = "api/v1/activitypub/user-id"
	expected.Host = "an.other.host"
	expected.HostPort = 443
	expected.IsPortSupplemented = false
	expected.UnvalidatedInput = "https://an.other.host:443/api/v1/activitypub/user-id/1"

	sut, _ = NewPersonID("HTTPS://an.other.host:443/api/v1/activitypub/user-id/1", "forgejo")
	assert.Equal(t, expected, sut)

	expected = PersonID{}
	expected.ID = "@me"
	expected.Source = "gotosocial"
	expected.HostSchema = "https"
	expected.Path = ""
	expected.Host = "an.other.host"
	expected.HostPort = 443
	expected.IsPortSupplemented = true
	expected.UnvalidatedInput = "https://an.other.host/@me"

	sut, err = NewPersonID("https://an.other.host/@me", "gotosocial")
	require.NoError(t, err)
	assert.Equal(t, expected, sut)
}

func TestPersonIdValidation(t *testing.T) {
	sut := PersonID{}
	sut.ID = "1"
	sut.Source = "forgejo"
	sut.HostSchema = "https"
	sut.Path = ""
	sut.Host = "an.other.host"
	sut.HostPort = 443
	sut.IsPortSupplemented = true
	sut.UnvalidatedInput = "https://an.other.host/1"

	result, err := validation.IsValid(sut)
	assert.False(t, result)
	require.EqualError(t, err, "Validation Error: forgefed.PersonID: path should not be empty\npath: \"\" has to be a person specific api path")

	sut = PersonID{}
	sut.ID = "1"
	sut.Source = "mastodon"
	sut.HostSchema = "https"
	sut.Path = ""
	sut.Host = "an.other.host"
	sut.HostPort = 443
	sut.IsPortSupplemented = true
	sut.UnvalidatedInput = "https://an.other.host/1"

	result, err = validation.IsValid(sut)
	assert.True(t, result)
	require.NoError(t, err)

	sut = PersonID{}
	sut.ID = "1"
	sut.Source = "forgejo"
	sut.HostSchema = "https"
	sut.Path = "path"
	sut.Host = "an.other.host"
	sut.HostPort = 443
	sut.IsPortSupplemented = true
	sut.UnvalidatedInput = "https://an.other.host/path/1"

	result, err = validation.IsValid(sut)
	assert.False(t, result)
	require.EqualError(t, err, "Validation Error: forgefed.PersonID: path: \"path\" has to be a person specific api path")

	sut = PersonID{}
	sut.ID = "1"
	sut.Source = "forgejox"
	sut.HostSchema = "https"
	sut.Path = "api/v1/activitypub/user-id"
	sut.Host = "an.other.host"
	sut.HostPort = 443
	sut.IsPortSupplemented = true
	sut.UnvalidatedInput = "https://an.other.host/api/v1/activitypub/user-id/1"

	result, err = validation.IsValid(sut)
	assert.False(t, result)
	require.EqualError(t, err, "Validation Error: forgefed.PersonID: Field Source contains the value forgejox, which is not in allowed subset [forgejo gitea mastodon gotosocial]")
}

func TestWebfingerId(t *testing.T) {
	sut, _ := NewPersonID("https://codeberg.org/api/v1/activitypub/user-id/12345", "forgejo")
	assert.Equal(t, "@12345@codeberg.org", sut.AsWebfinger())
}

func TestShouldThrowErrorOnInvalidInput(t *testing.T) {
	var err any
	_, err = NewPersonID("", "forgejo")
	if err == nil {
		t.Errorf("empty input should be invalid.")
	}
	_, err = NewPersonID("http://localhost:3000/api/v1/something", "forgejo")
	if err == nil {
		t.Errorf("localhost uris are not external")
	}
	_, err = NewPersonID("./api/v1/something", "forgejo")
	if err == nil {
		t.Errorf("relative uris are not allowed")
	}
	_, err = NewPersonID("http://1.2.3.4/api/v1/something", "forgejo")
	if err == nil {
		t.Errorf("uri may not be ip-4 based")
	}
	_, err = NewPersonID("http:///[fe80::1ff:fe23:4567:890a%25eth0]/api/v1/something", "forgejo")
	if err == nil {
		t.Errorf("uri may not be ip-6 based")
	}
	_, err = NewPersonID("https://codeberg.org/api/v1/activitypub/../activitypub/user-id/12345", "forgejo")
	if err == nil {
		t.Errorf("uri may not contain relative path elements")
	}
	_, err = NewPersonID("https://myuser@an.other.host/api/v1/activitypub/user-id/1", "forgejo")
	if err == nil {
		t.Errorf("uri may not contain unparsed elements")
	}
	_, err = NewPersonID("https://an.other.host/api/v1/activitypub/user-id/1", "forgejo")
	if err != nil {
		t.Errorf("this uri should be valid but was: %v", err)
	}
}

func Test_PersonMarshalJSON(t *testing.T) {
	sut := ForgePerson{}
	sut.Type = "Person"
	sut.PreferredUsername = ap.NaturalLanguageValuesNew()
	sut.PreferredUsername.Set("en", ap.Content("MaxMuster"))
	result, _ := sut.MarshalJSON()
	assert.JSONEq(t, `{"type":"Person","preferredUsername":"MaxMuster"}`, string(result), "Expected string is not equal")
}

func Test_PersonUnmarshalJSON(t *testing.T) {
	expected := &ForgePerson{
		Actor: ap.Actor{
			Type: "Person",
			PreferredUsername: ap.NaturalLanguageValues{
				ap.LangRefValue{Ref: "en", Value: []byte("MaxMuster")},
			},
		},
	}
	sut := new(ForgePerson)
	err := sut.UnmarshalJSON([]byte(`{"type":"Person","preferredUsername":"MaxMuster"}`))
	if err != nil {
		t.Errorf("UnmarshalJSON() unexpected error: %v", err)
	}
	x, _ := expected.MarshalJSON()
	y, _ := sut.MarshalJSON()
	if !reflect.DeepEqual(x, y) {
		t.Errorf("UnmarshalJSON() expected: %q got: %q", x, y)
	}

	expectedStr := strings.ReplaceAll(strings.ReplaceAll(`{
		"id":"https://federated-repo.prod.meissa.de/api/v1/activitypub/user-id/10",
		"type":"Person",
		"icon":{"type":"Image","mediaType":"image/png","url":"https://federated-repo.prod.meissa.de/avatar/fa7f9c4af2a64f41b1bef292bf872614"},
		"url":"https://federated-repo.prod.meissa.de/stargoose9",
		"inbox":"https://federated-repo.prod.meissa.de/api/v1/activitypub/user-id/10/inbox",
		"outbox":"https://federated-repo.prod.meissa.de/api/v1/activitypub/user-id/10/outbox",
		"preferredUsername":"stargoose9",
		"publicKey":{"id":"https://federated-repo.prod.meissa.de/api/v1/activitypub/user-id/10#main-key",
			"owner":"https://federated-repo.prod.meissa.de/api/v1/activitypub/user-id/10",
			"publicKeyPem":"-----BEGIN PUBLIC KEY-----\nMIIBoj...XAgMBAAE=\n-----END PUBLIC KEY-----\n"}}`,
		"\n", ""),
		"\t", "")
	err = sut.UnmarshalJSON([]byte(expectedStr))
	if err != nil {
		t.Errorf("UnmarshalJSON() unexpected error: %v", err)
	}
	result, _ := sut.MarshalJSON()
	assert.JSONEq(t, expectedStr, string(result), "Expected string is not equal")
}

func TestForgePersonValidation(t *testing.T) {
	sut := new(ForgePerson)
	sut.UnmarshalJSON([]byte(`{"type":"Person","preferredUsername":"MaxMuster"}`))
	if res, _ := validation.IsValid(sut); !res {
		t.Errorf("sut expected to be valid: %v\n", sut.Validate())
	}
}

func TestAsloginName(t *testing.T) {
	sut, _ := NewPersonID("https://codeberg.org/api/v1/activitypub/user-id/12345", "forgejo")
	assert.Equal(t, "12345-codeberg.org", sut.AsLoginName())

	sut, _ = NewPersonID("https://codeberg.org:443/api/v1/activitypub/user-id/12345", "forgejo")
	assert.Equal(t, "12345-codeberg.org-443", sut.AsLoginName())
}
