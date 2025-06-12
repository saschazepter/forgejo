// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package admin

import (
	"errors"
	"net/http"

	quota_model "forgejo.org/models/quota"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/web"
	"forgejo.org/services/context"
	"forgejo.org/services/convert"
)

func toLimitSubjects(subjStrings []string) (*quota_model.LimitSubjects, error) {
	subjects := make(quota_model.LimitSubjects, len(subjStrings))
	for i := range len(subjStrings) {
		subj, err := quota_model.ParseLimitSubject(subjStrings[i])
		if err != nil {
			return nil, err
		}
		subjects[i] = subj
	}

	return &subjects, nil
}

// ListQuotaRules lists all the quota rules
func ListQuotaRules(ctx *context.APIContext) {
	// swagger:operation GET /admin/quota/rules admin adminListQuotaRules
	// ---
	// summary: List the available quota rules
	// produces:
	// - application/json
	// responses:
	//   "200":
	//     "$ref": "#/responses/QuotaRuleInfoList"
	//   "403":
	//     "$ref": "#/responses/forbidden"

	rules, err := quota_model.ListRules(ctx)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "quota_model.ListQuotaRules", err)
		return
	}

	result := make([]api.QuotaRuleInfo, len(rules))
	for i := range len(rules) {
		result[i] = convert.ToQuotaRuleInfo(rules[i], true)
	}

	ctx.JSON(http.StatusOK, result)
}

// CreateQuotaRule creates a new quota rule
func CreateQuotaRule(ctx *context.APIContext) {
	// swagger:operation POST /admin/quota/rules admin adminCreateQuotaRule
	// ---
	// summary: Create a new quota rule
	// produces:
	// - application/json
	// parameters:
	// - name: rule
	//   in: body
	//   description: Definition of the quota rule
	//   schema:
	//     "$ref": "#/definitions/CreateQuotaRuleOptions"
	//   required: true
	// responses:
	//   "201":
	//     "$ref": "#/responses/QuotaRuleInfo"
	//   "400":
	//     "$ref": "#/responses/error"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "409":
	//     "$ref": "#/responses/error"
	//   "422":
	//     "$ref": "#/responses/validationError"

	form := web.GetForm(ctx).(*api.CreateQuotaRuleOptions)

	if form.Limit == nil {
		ctx.Error(http.StatusUnprocessableEntity, "quota_model.ParseLimitSubject", errors.New("[Limit]: Required"))
		return
	}

	subjects, err := toLimitSubjects(form.Subjects)
	if err != nil {
		ctx.Error(http.StatusUnprocessableEntity, "quota_model.ParseLimitSubject", err)
		return
	}

	rule, err := quota_model.CreateRule(ctx, form.Name, *form.Limit, *subjects)
	if err != nil {
		if quota_model.IsErrRuleAlreadyExists(err) {
			ctx.Error(http.StatusConflict, "", err)
		} else {
			ctx.Error(http.StatusInternalServerError, "quota_model.CreateRule", err)
		}
		return
	}
	ctx.JSON(http.StatusCreated, convert.ToQuotaRuleInfo(*rule, true))
}

// GetQuotaRule returns information about the specified quota rule
func GetQuotaRule(ctx *context.APIContext) {
	// swagger:operation GET /admin/quota/rules/{quotarule} admin adminGetQuotaRule
	// ---
	// summary: Get information about a quota rule
	// produces:
	// - application/json
	// parameters:
	// - name: quotarule
	//   in: path
	//   description: quota rule to query
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/QuotaRuleInfo"
	//   "400":
	//     "$ref": "#/responses/error"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "404":
	//     "$ref": "#/responses/notFound"

	ctx.JSON(http.StatusOK, convert.ToQuotaRuleInfo(*ctx.QuotaRule, true))
}

// EditQuotaRule changes an existing quota rule
func EditQuotaRule(ctx *context.APIContext) {
	// swagger:operation PATCH /admin/quota/rules/{quotarule} admin adminEditQuotaRule
	// ---
	// summary: Change an existing quota rule
	// produces:
	// - application/json
	// parameters:
	// - name: quotarule
	//   in: path
	//   description: Quota rule to change
	//   type: string
	//   required: true
	// - name: rule
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/EditQuotaRuleOptions"
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/QuotaRuleInfo"
	//   "400":
	//     "$ref": "#/responses/error"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "422":
	//     "$ref": "#/responses/validationError"

	form := web.GetForm(ctx).(*api.EditQuotaRuleOptions)

	var subjects *quota_model.LimitSubjects
	if form.Subjects != nil {
		subjs := make(quota_model.LimitSubjects, len(*form.Subjects))
		for i := range len(*form.Subjects) {
			subj, err := quota_model.ParseLimitSubject((*form.Subjects)[i])
			if err != nil {
				ctx.Error(http.StatusUnprocessableEntity, "quota_model.ParseLimitSubject", err)
				return
			}
			subjs[i] = subj
		}
		subjects = &subjs
	}

	rule, err := ctx.QuotaRule.Edit(ctx, form.Limit, subjects)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "quota_model.rule.Edit", err)
		return
	}

	ctx.JSON(http.StatusOK, convert.ToQuotaRuleInfo(*rule, true))
}

// DeleteQuotaRule deletes a quota rule
func DeleteQuotaRule(ctx *context.APIContext) {
	// swagger:operation DELETE /admin/quota/rules/{quotarule} admin adminDEleteQuotaRule
	// ---
	// summary: Deletes a quota rule
	// produces:
	// - application/json
	// parameters:
	// - name: quotarule
	//   in: path
	//   description: quota rule to delete
	//   type: string
	//   required: true
	// responses:
	//   "204":
	//     "$ref": "#/responses/empty"
	//   "400":
	//     "$ref": "#/responses/error"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "404":
	//     "$ref": "#/responses/notFound"

	err := quota_model.DeleteRuleByName(ctx, ctx.QuotaRule.Name)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "quota_model.DeleteRuleByName", err)
		return
	}

	ctx.Status(http.StatusNoContent)
}
