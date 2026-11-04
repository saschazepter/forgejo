// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package tests

// See README.md for a documentation of the test logic

import (
	"cmp"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/perm"
	"forgejo.org/models/unit"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/web/routing"
)

// Helpers to keep track of the ordered sequence of permissions middleware used by
// each REST API endpoint and use them for testing.
//
// When the routes are built, permission middleware are expected to call
// the `RecordSignature` function so that it is added to the sequence of
// permissions functions that will be called each time the API endpoint is
// used.
//
// When all the permission functions have been accumulated in a
// sequence for a given API endpoint, the
// `CollectPermissionsMiddlewares` function is called to store it
// in the `methodsAndPatternToSignatures` dictionary.
//
// The `Group` route function uses the `RestorePermissionsSequence`
// function to save the permission sequence as it exists when entering the
// `Group` route function and restore it when it returns.
//
// The `Combo` route function uses the `GetSignatures` function to
// save the permission sequence as it exists when it creates the
// object `Combo` object. It then uses the `SetSignatures` function to
// restore it after delegating to the `Get`, `Delete`, etc. method.
//
// The `Get`, `Delete`, etc. route functions use the
// `RestoreLastPermissionsSequence` to discard the permission sequence
// specific to them (from the middleware list they were given in argument).
//
// A middleware can call `FollowedBy` to require that a permission check
// registered with `RecordSignature` appears after it in the sequence. It
// will fail if it appears before it or if it is not found.
//
// `Reset()` is called before building the routes to initialize all
// global variables. They are all to be used when setting up the routes
// and if a test directly calls `routers.NormalRoutes()`, all routes need
// to be re-evaluated. This may be useful for instance when a setting
// is modified and enables routes that were not enabled before.

type Sequence struct {
	signatures [][]any
	followedBy []string
}

func (o Sequence) clone() Sequence {
	return Sequence{
		signatures: slices.Clone(o.signatures),
		followedBy: slices.Clone(o.followedBy),
	}
}

var (
	signatureStringToSignature    map[string][]any
	methodsAndPatternToSignatures map[string][][]any

	// used as temporary storage while building the routes
	sequence      Sequence
	sequenceStack []Sequence

	mutex = sync.Mutex{}
)

// See above for the documentation
func RecordSignature(signature ...any) {
	if !setting.IsInTesting {
		return
	}
	mutex.Lock()
	defer mutex.Unlock()

	sequence.signatures = append(sequence.signatures, signature)
	signatureStringToSignature[SignatureToString(signature)] = signature
}

// See above for the documentation
func FollowedBy(fun, followedBy any) {
	if !setting.IsInTesting {
		return
	}
	mutex.Lock()
	defer mutex.Unlock()

	followedByName := routing.GetFuncShortName(followedBy)
	funName := routing.GetFuncShortName(fun)
	for _, signature := range sequence.signatures {
		if routing.GetFuncShortName(signature[0]) == followedByName {
			panic(fmt.Errorf("%s must follow %s but precedes it", followedByName, funName))
		}
	}
	sequence.followedBy = append(sequence.followedBy, followedByName)
}

// See above for the documentation
func CollectPermissionsMiddlewares(endpoint any, methods, pattern string) {
	if !setting.IsInTesting {
		panic("must only be called if setting.IsInTesting is true")
	}
	mutex.Lock()
	defer mutex.Unlock()

	if len(sequence.signatures) == 0 {
		return
	}
	signaturesString := SignaturesToString(sequence.signatures)
	functionName := routing.GetFuncShortName(endpoint)
	methodsAndPattern := methods + " " + pattern
	followedByValidation(methodsAndPattern)
	if existingSignatures, has := methodsAndPatternToSignatures[methodsAndPattern]; has && SignaturesToString(existingSignatures) != signaturesString {
		panic(fmt.Errorf("function %s is invoked for %s with different permissions %s != %s", functionName, methodsAndPattern, SignaturesToString(existingSignatures), signaturesString))
	}
	methodsAndPatternToSignatures[methodsAndPattern] = slices.Clone(sequence.signatures)
	log.Debug("%s: permissions checks %v for function %v", methodsAndPattern, signaturesString, functionName)
}

// See above for the documentation
func RestorePermissionsSequence() func() {
	if !setting.IsInTesting {
		panic("must only be called if setting.IsInTesting is true")
	}
	mutex.Lock()
	defer mutex.Unlock()

	saved := sequence.clone()
	savedPermissionsSequenceStack := sequenceStack
	sequenceStack = append(sequenceStack, saved)
	return func() {
		sequenceStack = savedPermissionsSequenceStack
		if len(sequenceStack) > 0 {
			sequence = sequenceStack[len(sequenceStack)-1]
		} else {
			sequence = Sequence{}
		}
	}
}

// See above for the documentation
func GetSignatures() Sequence {
	if !setting.IsInTesting {
		return Sequence{}
	}
	mutex.Lock()
	defer mutex.Unlock()

	return sequence.clone()
}

// See above for the documentation
func SetSignatures(s Sequence) {
	if !setting.IsInTesting {
		return
	}
	mutex.Lock()
	defer mutex.Unlock()

	sequence = s
}

// See above for the documentation
func RestoreLastPermissionsSequence() {
	if !setting.IsInTesting {
		panic("must only be called if setting.IsInTesting is true")
	}
	mutex.Lock()
	defer mutex.Unlock()

	if len(sequenceStack) > 0 {
		sequence = sequenceStack[len(sequenceStack)-1]
	}
}

// See above for the documentation
func Reset() {
	if !setting.IsInTesting {
		panic("must only be called if setting.IsInTesting is true")
	}
	mutex.Lock()
	defer mutex.Unlock()

	signatureStringToSignature = make(map[string][]any, 200)
	methodsAndPatternToSignatures = make(map[string][][]any, 200)
	sequence = Sequence{}
	sequenceStack = nil
}

func followedByValidation(methodsAndPattern string) {
	signaturesString := SignaturesToString(sequence.signatures)
	followedBy := slices.Clone(sequence.followedBy)
	for _, signature := range sequence.signatures {
		if len(followedBy) == 0 {
			break
		}
		functionName := routing.GetFuncShortName(signature[0])
		if slices.Contains(followedBy, functionName) {
			followedBy = slices.DeleteFunc(followedBy, func(e string) bool {
				return e == functionName
			})
		}
	}
	if len(followedBy) > 0 {
		panic(fmt.Errorf("%s: %s does not contain the required permissions check %v", methodsAndPattern, signaturesString, followedBy))
	}
}

func SignatureToString(signature []any) string {
	fn := reflect.ValueOf(signature[0])
	if fn.Type().Kind() != reflect.Func {
		panic(fmt.Sprintf("handler must be a function, but got %s", fn.Type()))
	}
	function := strings.TrimPrefix(routing.GetFuncShortName(signature[0]), "permissions.")
	argStrings := []string{function}
	for _, arg := range signature[1:] {
		switch typedArg := arg.(type) {
		case []auth_model.AccessTokenScopeCategory:
			argStrings = append(argStrings, " "+RequiredScopesToString(typedArg...))
		case []unit.Type:
			slices.Sort(typedArg)
			for _, unitType := range typedArg {
				argStrings = append(argStrings, " "+unitType.String())
			}
		case unit.Type:
			argStrings = append(argStrings, " "+typedArg.String())
		case perm.AccessMode:
			argStrings = append(argStrings, " "+typedArg.String())
		default:
			panic(fmt.Errorf("unsupported type %T", arg))
		}
	}
	return strings.Join(argStrings, "")
}

func SignaturesToString(signatures [][]any) string {
	var signatureStrings []string
	for _, s := range signatures {
		signatureStrings = append(signatureStrings, SignatureToString(s))
	}
	return strings.Join(signatureStrings, ",")
}

func GetSignatureStringToSignature() map[string][]any {
	return signatureStringToSignature
}

func GetUniquePermissionsSequences() [][][]any {
	signaturesStringToSignatures := make(map[string][][]any, 200)
	for _, signatures := range methodsAndPatternToSignatures {
		signaturesStringToSignatures[SignaturesToString(signatures)] = signatures
	}
	sequences := slices.Collect(maps.Values(signaturesStringToSignatures))
	slices.SortFunc(sequences, func(a, b [][]any) int {
		return cmp.Compare(SignaturesToString(a), SignaturesToString(b))
	})
	return sequences
}

func GetShortestPermissionSequenceForEachSignature() [][][]any {
	signaturesStringToShortest := make(map[string][][]any, 200)
	for _, signatures := range methodsAndPatternToSignatures {
		for len(signatures) > 0 {
			last := signatures[len(signatures)-1]
			lastString := SignatureToString(last)
			if existingSignatures, has := signaturesStringToShortest[lastString]; has {
				if len(signatures) < len(existingSignatures) {
					signaturesStringToShortest[lastString] = signatures
				}
			} else {
				signaturesStringToShortest[lastString] = signatures
			}
			signatures = signatures[:len(signatures)-1]
		}
	}
	sequences := slices.Collect(maps.Values(signaturesStringToShortest))
	slices.SortFunc(sequences, func(a, b [][]any) int {
		return cmp.Compare(SignaturesToString(a), SignaturesToString(b))
	})
	return sequences
}

func RequiredScopesToString(scopeCategories ...auth_model.AccessTokenScopeCategory) string {
	var categories []string
	for _, category := range scopeCategories {
		switch category {
		case auth_model.AccessTokenScopeCategoryActivityPub:
			categories = append(categories, "ActivityPub")
		case auth_model.AccessTokenScopeCategoryAdmin:
			categories = append(categories, "Admin")
		case auth_model.AccessTokenScopeCategoryMisc:
			categories = append(categories, "Misc")
		case auth_model.AccessTokenScopeCategoryNotification:
			categories = append(categories, "Notification")
		case auth_model.AccessTokenScopeCategoryOrganization:
			categories = append(categories, "Organization")
		case auth_model.AccessTokenScopeCategoryPackage:
			categories = append(categories, "Package")
		case auth_model.AccessTokenScopeCategoryIssue:
			categories = append(categories, "Issue")
		case auth_model.AccessTokenScopeCategoryRepository:
			categories = append(categories, "Repository")
		case auth_model.AccessTokenScopeCategoryUser:
			categories = append(categories, "User")
		case auth_model.AccessTokenScopeCategoryProject:
			categories = append(categories, "Project")
		default:
			panic(fmt.Errorf("unkwnon scope category %v", category))
		}
	}
	slices.Sort(categories)
	return strings.Join(categories, "")
}
