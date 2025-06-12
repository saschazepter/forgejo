// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package activitypub

import (
	"crypto"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"forgejo.org/models/db"
	"forgejo.org/models/forgefed"
	"forgejo.org/models/user"
	"forgejo.org/modules/activitypub"
	fm "forgejo.org/modules/forgefed"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	gitea_context "forgejo.org/services/context"
	"forgejo.org/services/federation"

	"github.com/42wim/httpsig"
	ap "github.com/go-ap/activitypub"
)

func decodePublicKeyPem(pubKeyPem string) ([]byte, error) {
	block, _ := pem.Decode([]byte(pubKeyPem))
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, errors.New("could not decode publicKeyPem to PUBLIC KEY pem block type")
	}

	return block.Bytes, nil
}

func getFederatedUser(ctx *gitea_context.APIContext, person *ap.Person, federationHost *forgefed.FederationHost) (*user.FederatedUser, error) {
	personID, err := fm.NewPersonID(person.ID.String(), string(federationHost.NodeInfo.SoftwareName))
	if err != nil {
		return nil, err
	}
	_, federatedUser, err := user.FindFederatedUser(ctx, personID.ID, federationHost.ID)
	if err != nil {
		return nil, err
	}

	if federatedUser != nil {
		return federatedUser, nil
	}

	_, newFederatedUser, err := federation.CreateUserFromAP(ctx, personID, federationHost.ID)
	if err != nil {
		return nil, err
	}

	return newFederatedUser, nil
}

func storePublicKey(ctx *gitea_context.APIContext, person *ap.Person, pubKeyBytes []byte) error {
	federationHost, err := federation.GetFederationHostForURI(ctx, person.ID.String())
	if err != nil {
		return err
	}

	if person.Type == ap.ActivityVocabularyType("Application") {
		federationHost.KeyID = sql.NullString{
			String: person.PublicKey.ID.String(),
			Valid:  true,
		}

		federationHost.PublicKey = sql.Null[sql.RawBytes]{
			V:     pubKeyBytes,
			Valid: true,
		}

		_, err = db.GetEngine(ctx).ID(federationHost.ID).Update(federationHost)
		if err != nil {
			return err
		}
	} else if person.Type == ap.ActivityVocabularyType("Person") {
		federatedUser, err := getFederatedUser(ctx, person, federationHost)
		if err != nil {
			return err
		}

		federatedUser.KeyID = sql.NullString{
			String: person.PublicKey.ID.String(),
			Valid:  true,
		}

		federatedUser.PublicKey = sql.Null[sql.RawBytes]{
			V:     pubKeyBytes,
			Valid: true,
		}

		_, err = db.GetEngine(ctx).ID(federatedUser.ID).Update(federatedUser)
		if err != nil {
			return err
		}
	}

	return nil
}

func getPublicKeyFromResponse(b []byte, keyID *url.URL) (person *ap.Person, pubKeyBytes []byte, p crypto.PublicKey, err error) {
	person = ap.PersonNew(ap.IRI(keyID.String()))
	err = person.UnmarshalJSON(b)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("ActivityStreams type cannot be converted to one known to have publicKey property: %w", err)
	}

	pubKey := person.PublicKey
	if pubKey.ID.String() != keyID.String() {
		return nil, nil, nil, fmt.Errorf("cannot find publicKey with id: %s in %s", keyID, string(b))
	}

	pubKeyBytes, err = decodePublicKeyPem(pubKey.PublicKeyPem)
	if err != nil {
		return nil, nil, nil, err
	}

	p, err = x509.ParsePKIXPublicKey(pubKeyBytes)
	if err != nil {
		return nil, nil, nil, err
	}

	return person, pubKeyBytes, p, err
}

func verifyHTTPSignatures(ctx *gitea_context.APIContext) (authenticated bool, err error) {
	if !setting.Federation.SignatureEnforced {
		return true, nil
	}

	r := ctx.Req

	// 1. Figure out what key we need to verify
	v, err := httpsig.NewVerifier(r)
	if err != nil {
		return false, err
	}

	ID := v.KeyId()
	idIRI, err := url.Parse(ID)
	if err != nil {
		return false, err
	}

	signatureAlgorithm := httpsig.Algorithm(setting.Federation.SignatureAlgorithms[0])

	// 2. Fetch the public key of the other actor
	// Try if the signing actor is an already known federated user
	_, federationUser, err := user.FindFederatedUserByKeyID(ctx, idIRI.String())
	if err != nil {
		return false, err
	}

	if federationUser != nil && federationUser.PublicKey.Valid {
		pubKey, err := x509.ParsePKIXPublicKey(federationUser.PublicKey.V)
		if err != nil {
			return false, err
		}

		authenticated = v.Verify(pubKey, signatureAlgorithm) == nil
		return authenticated, err
	}

	// Try if the signing actor is an already known federation host
	federationHost, err := forgefed.FindFederationHostByKeyID(ctx, idIRI.String())
	if err != nil {
		return false, err
	}

	if federationHost != nil && federationHost.PublicKey.Valid {
		pubKey, err := x509.ParsePKIXPublicKey(federationHost.PublicKey.V)
		if err != nil {
			return false, err
		}

		authenticated = v.Verify(pubKey, signatureAlgorithm) == nil
		return authenticated, err
	}

	// Fetch missing public key
	actionsUser := user.NewAPServerActor()
	clientFactory, err := activitypub.GetClientFactory(ctx)
	if err != nil {
		return false, err
	}

	apClient, err := clientFactory.WithKeys(ctx, actionsUser, actionsUser.APActorKeyID())
	if err != nil {
		return false, err
	}

	b, err := apClient.GetBody(idIRI.String())
	if err != nil {
		return false, err
	}

	person, pubKeyBytes, pubKey, err := getPublicKeyFromResponse(b, idIRI)
	if err != nil {
		return false, err
	}

	authenticated = v.Verify(pubKey, signatureAlgorithm) == nil
	if authenticated {
		err = storePublicKey(ctx, person, pubKeyBytes)
		if err != nil {
			return false, err
		}
	}

	return authenticated, err
}

// ReqHTTPSignature function
func ReqHTTPSignature() func(ctx *gitea_context.APIContext) {
	return func(ctx *gitea_context.APIContext) {
		if authenticated, err := verifyHTTPSignatures(ctx); err != nil {
			log.Warn("verifyHttpSignatures failed: %v", err)
			ctx.Error(http.StatusBadRequest, "reqSignature", "request signature verification failed")
		} else if !authenticated {
			ctx.Error(http.StatusForbidden, "reqSignature", "request signature verification failed")
		}
	}
}
