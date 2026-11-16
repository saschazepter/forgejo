// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2020 The Gitea Authors.
// SPDX-License-Identifier: MIT

package user

import (
	"fmt"
	"net/http"

	activities_model "forgejo.org/models/activities"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/structs"
	"forgejo.org/routers/api/v1/utils"
	"forgejo.org/services/context"
	"forgejo.org/services/convert"
)

// Search search users
func Search(ctx *context.APIContext) {
	// swagger:operation GET /users/search user userSearch
	// ---
	// summary: Search for users
	// produces:
	// - application/json
	// parameters:
	// - name: q
	//   in: query
	//   description: keyword
	//   type: string
	// - name: uid
	//   in: query
	//   description: ID of the user to search for
	//   type: integer
	//   format: int64
	// - name: sort
	//   in: query
	//   description: sort order of results
	//   type: string
	//   enum: [oldest, newest, alphabetically, reversealphabetically, recentupdate, leastupdate]
	// - name: page
	//   in: query
	//   description: page number of results to return (1-based)
	//   type: integer
	// - name: limit
	//   in: query
	//   description: page size of results
	//   type: integer
	// responses:
	//   "200":
	//     description: "SearchResults of a successful search"
	//     schema:
	//       type: object
	//       title: "UserSearchResults"
	//       properties:
	//         ok:
	//           type: boolean
	//         data:
	//           type: array
	//           items:
	//             "$ref": "#/definitions/User"

	listOptions := utils.GetListOptions(ctx)

	uid := ctx.FormInt64("uid")
	var users []*user_model.User
	var maxResults int64
	var err error

	switch uid {
	case user_model.GhostUserID:
		maxResults = 1
		users = []*user_model.User{user_model.NewGhostUser()}
	case user_model.ActionsUserID:
		maxResults = 1
		users = []*user_model.User{user_model.NewActionsUser()}
	default:
		var visible []structs.VisibleType
		if ctx.PublicOnly() {
			visible = []structs.VisibleType{structs.VisibleTypePublic}
		}
		users, maxResults, err = user_model.SearchUsers(ctx, &user_model.SearchUserOptions{
			Actor:         ctx.Doer(),
			Keyword:       ctx.FormTrim("q"),
			UID:           uid,
			Type:          user_model.UserTypeIndividual,
			SearchByEmail: true,
			Visible:       visible,
			ListOptions:   listOptions,
			OrderBy:       utils.GetDbSearchOrder(ctx),
		})
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, map[string]any{
				"ok":    false,
				"error": err.Error(),
			})
			return
		}
	}

	ctx.SetLinkHeader(int(maxResults), listOptions.PageSize)
	ctx.SetTotalCountHeader(maxResults)

	ctx.JSON(http.StatusOK, map[string]any{
		"ok":   true,
		"data": convert.ToUsers(ctx, ctx.Doer(), users),
	})
}

// GetInfo get user's information
func GetInfo(ctx *context.APIContext) {
	// swagger:operation GET /users/{username} user userGet
	// ---
	// summary: Get a user
	// produces:
	// - application/json
	// parameters:
	// - name: username
	//   in: path
	//   description: username of user to get
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/User"
	//   "404":
	//     "$ref": "#/responses/notFound"

	if !user_model.IsUserVisibleToViewer(ctx, ctx.User(), ctx.Doer()) {
		// fake ErrUserNotExist error message to not leak information about existence
		ctx.NotFound("GetUserByName", user_model.ErrUserNotExist{Name: ctx.Params(":username")})
		return
	}
	ctx.JSON(http.StatusOK, convert.ToUser(ctx, ctx.User(), ctx.Doer()))
}

// GetAuthenticatedUser get current user's information
func GetAuthenticatedUser(ctx *context.APIContext) {
	// swagger:operation GET /user user userGetCurrent
	// ---
	// summary: Get the authenticated user
	// produces:
	// - application/json
	// responses:
	//   "200":
	//     "$ref": "#/responses/User"
	//   "401":
	//     "$ref": "#/responses/unauthorized"
	//   "403":
	//     "$ref": "#/responses/forbidden"

	ctx.JSON(http.StatusOK, convert.ToUser(ctx, ctx.Doer(), ctx.Doer()))
}

// GetUserHeatmapData is the handler to get a users heatmap
func GetUserHeatmapData(ctx *context.APIContext) {
	// swagger:operation GET /users/{username}/heatmap user userGetHeatmapData
	// ---
	// summary: Get a user's heatmap
	// produces:
	// - application/json
	// parameters:
	// - name: username
	//   in: path
	//   description: username of user to get
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/UserHeatmapData"
	//   "404":
	//     "$ref": "#/responses/notFound"

	heatmap, err := activities_model.GetUserHeatmapDataByUser(ctx, ctx.User(), ctx.Doer(), ctx.Reducer())
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "GetUserHeatmapDataByUser", err)
		return
	}
	ctx.JSON(http.StatusOK, heatmap)
}

func ListUserActivityFeeds(ctx *context.APIContext) {
	// swagger:operation GET /users/{username}/activities/feeds user userListActivityFeeds
	// ---
	// summary: List a user's activity feeds
	// produces:
	// - application/json
	// parameters:
	// - name: username
	//   in: path
	//   description: username of user
	//   type: string
	//   required: true
	// - name: only-performed-by
	//   in: query
	//   description: if true, only show actions performed by the requested user
	//   type: boolean
	// - name: date
	//   in: query
	//   description: the date of the activities to be found
	//   type: string
	//   format: date
	// - name: page
	//   in: query
	//   description: page number of results to return (1-based)
	//   type: integer
	// - name: limit
	//   in: query
	//   description: page size of results
	//   type: integer
	// responses:
	//   "200":
	//     "$ref": "#/responses/ActivityFeedsList"
	//   "404":
	//     "$ref": "#/responses/notFound"

	includePrivate := ctx.IsSigned() && (ctx.IsUserSiteAdmin() || ctx.Doer().ID == ctx.User().ID)
	listOptions := utils.GetListOptions(ctx)

	opts := activities_model.GetFeedsOptions{
		RequestedUser:        ctx.User(),
		Actor:                ctx.Doer(),
		IncludePrivate:       includePrivate,
		OnlyPerformedBy:      ctx.FormBool("only-performed-by"),
		Date:                 ctx.FormString("date"),
		ListOptions:          listOptions,
		AuthorizationReducer: ctx.Reducer(),
	}

	feeds, count, err := activities_model.GetFeeds(ctx, opts)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "GetFeeds", err)
		return
	}
	ctx.SetTotalCountHeader(count)

	ctx.JSON(http.StatusOK, convert.ToActivities(ctx, feeds, ctx.Doer()))
}

// ListBlockedUsers list the authenticated user's blocked users.
func ListBlockedUsers(ctx *context.APIContext) {
	// swagger:operation GET /user/list_blocked user userListBlockedUsers
	// ---
	// summary: List the authenticated user's blocked users
	// produces:
	// - application/json
	// parameters:
	// - name: page
	//   in: query
	//   description: page number of results to return (1-based)
	//   type: integer
	// - name: limit
	//   in: query
	//   description: page size of results
	//   type: integer
	// responses:
	//   "200":
	//     "$ref": "#/responses/BlockedUserList"
	//   "401":
	//     "$ref": "#/responses/unauthorized"
	//   "403":
	//     "$ref": "#/responses/forbidden"

	utils.ListUserBlockedUsers(ctx, ctx.Doer())
}

// BlockUser blocks a user from the doer.
func BlockUser(ctx *context.APIContext) {
	// swagger:operation PUT /user/block/{username} user userBlockUser
	// ---
	// summary: Blocks a user from the doer
	// produces:
	// - application/json
	// parameters:
	// - name: username
	//   in: path
	//   description: username of the user
	//   type: string
	//   required: true
	// responses:
	//   "204":
	//     "$ref": "#/responses/empty"
	//   "401":
	//     "$ref": "#/responses/unauthorized"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "422":
	//     "$ref": "#/responses/validationError"

	if ctx.User().IsOrganization() {
		ctx.Error(http.StatusUnprocessableEntity, "", fmt.Errorf("%s is an organization not a user", ctx.User().Name))
		return
	}

	utils.BlockUser(ctx, ctx.Doer(), ctx.User())
}

// UnblockUser unblocks a user from the doer.
func UnblockUser(ctx *context.APIContext) {
	// swagger:operation PUT /user/unblock/{username} user userUnblockUser
	// ---
	// summary: Unblocks a user from the doer
	// produces:
	// - application/json
	// parameters:
	// - name: username
	//   in: path
	//   description: username of the user
	//   type: string
	//   required: true
	// responses:
	//   "204":
	//     "$ref": "#/responses/empty"
	//   "401":
	//     "$ref": "#/responses/unauthorized"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "422":
	//     "$ref": "#/responses/validationError"

	if ctx.User().IsOrganization() {
		ctx.Error(http.StatusUnprocessableEntity, "", fmt.Errorf("%s is an organization not a user", ctx.User().Name))
		return
	}

	utils.UnblockUser(ctx, ctx.Doer(), ctx.User())
}
