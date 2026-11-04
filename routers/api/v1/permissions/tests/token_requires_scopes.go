// Copyright 2026 The Forgejo Authors.
// SPDX-License-Identifier: GPLv3-or-later

package tests

import (
	"fmt"
	"strings"
	"testing"

	auth_model "forgejo.org/models/auth"
	apiv1_permissions "forgejo.org/routers/api/v1/permissions"
)

var _ = registerFunctionTestBuilder([]string{"TokenRequiresScopes "}, func(t *testing.T, signatureString string, signature []any) {
	t.Helper()
	categories := signature[1].([]auth_model.AccessTokenScopeCategory)
	var scopes []string
	for _, category := range categories {
		var scope auth_model.AccessTokenScope
		switch category {
		case auth_model.AccessTokenScopeCategoryActivityPub:
			scope = auth_model.AccessTokenScopeReadActivityPub
		case auth_model.AccessTokenScopeCategoryAdmin:
			scope = auth_model.AccessTokenScopeReadAdmin
		case auth_model.AccessTokenScopeCategoryNotification:
			scope = auth_model.AccessTokenScopeReadNotification
		case auth_model.AccessTokenScopeCategoryOrganization:
			scope = auth_model.AccessTokenScopeReadOrganization
		case auth_model.AccessTokenScopeCategoryPackage:
			scope = auth_model.AccessTokenScopeReadPackage
		case auth_model.AccessTokenScopeCategoryIssue:
			scope = auth_model.AccessTokenScopeReadIssue
		case auth_model.AccessTokenScopeCategoryRepository:
			scope = auth_model.AccessTokenScopeReadRepository
		case auth_model.AccessTokenScopeCategoryUser:
			scope = auth_model.AccessTokenScopeReadUser
		case auth_model.AccessTokenScopeCategoryProject:
			scope = auth_model.AccessTokenScopeReadProject
		default:
			panic(fmt.Errorf("unexpected category %v", category))
		}
		scopes = append(scopes, string(scope))
	}
	readscope := strings.Join(scopes, ",")
	t.Logf("%s scopes %s", signatureString, readscope)
	signatureStringToFunctionTest[signatureString] = functionTest{
		testCases: []*testCase{
			{
				// pass because the doer token has the required scope read
				// level
				data: newTestData(map[string]string{}, newSharedData().
					SetDoer().
					SetDoerScope(readscope).
					SetTokenLevel("read"),
				),
			},
			{
				// fail because the doer token does not have write level for
				// the required scope
				data: newTestData(map[string]string{}, newSharedData().
					SetDoer().
					SetDoerScope(readscope).
					SetTokenLevel("write"),
				),
				error: "token does not have at least one of required scope(s)",
			},
			{
				// fail because the doer token does not have the required
				// scope at all
				data: newTestData(map[string]string{}, newSharedData().
					SetDoer().
					SetDoerScope("read:misc").
					SetTokenLevel("write"),
				),
				error: "token does not have at least one of required scope(s)",
			},
		},
		fulfillNeeds: func(t *testing.T, data *testData) {
			t.Helper()
			data.shared.SetRepositoryDefault()
			data.shared.SetDoerDefault()
			if data.shared.HasDoerScope() {
				scope := data.shared.DoerScope()
				if !strings.Contains(scope, readscope) {
					writescope := strings.ReplaceAll(readscope, "read", "write")
					data.shared.SetDoerScope(strings.Join([]string{scope, writescope}, ","))
				}
			} else {
				data.shared.SetDoerScope(readscope)
			}

			data.shared.SetTokenLevelDefault("read")
		},
		interpret: func(t *testing.T, permissions *apiv1_permissions.Permissions, data *testData) {
			fixtureSetRepository(t, permissions, data.shared.RepositoryName(), data.shared.RepositoryInit(), data.shared.RepositoryPrivate(), data.shared.RepositoryArchived(), data.shared.RepositoryCollaborator())
		},
		staticArgs: 1,
		call: func(t *testing.T, ctx apiv1_permissions.Context, data *testData, args []any) {
			level := levelStringToLevel(data.shared.TokenLevel())
			categories := args[0].([]auth_model.AccessTokenScopeCategory)
			t.Logf("calling TokenRequiresScopes(ctx, %v, %v)", categories, level)
			apiv1_permissions.TokenRequiresScopes(ctx, categories, level)
		},
	}
})
