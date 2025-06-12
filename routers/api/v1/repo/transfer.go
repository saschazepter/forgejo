// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"errors"
	"fmt"
	"net/http"

	"forgejo.org/models"
	"forgejo.org/models/organization"
	"forgejo.org/models/perm"
	access_model "forgejo.org/models/perm/access"
	quota_model "forgejo.org/models/quota"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/log"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/web"
	"forgejo.org/services/context"
	"forgejo.org/services/convert"
	repo_service "forgejo.org/services/repository"
)

// Transfer transfers the ownership of a repository
func Transfer(ctx *context.APIContext) {
	// swagger:operation POST /repos/{owner}/{repo}/transfer repository repoTransfer
	// ---
	// summary: Transfer a repo ownership
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo to transfer
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repo to transfer
	//   type: string
	//   required: true
	// - name: body
	//   in: body
	//   description: "Transfer Options"
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/TransferRepoOption"
	// responses:
	//   "202":
	//     "$ref": "#/responses/Repository"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "413":
	//     "$ref": "#/responses/quotaExceeded"
	//   "422":
	//     "$ref": "#/responses/validationError"

	opts := web.GetForm(ctx).(*api.TransferRepoOption)

	newOwner, err := user_model.GetUserByName(ctx, opts.NewOwner)
	if err != nil {
		if user_model.IsErrUserNotExist(err) {
			ctx.Error(http.StatusNotFound, "", "The new owner does not exist or cannot be found")
			return
		}
		ctx.InternalServerError(err)
		return
	}

	if newOwner.Type == user_model.UserTypeOrganization {
		if !ctx.Doer.IsAdmin && newOwner.Visibility == api.VisibleTypePrivate && !organization.OrgFromUser(newOwner).HasMemberWithUserID(ctx, ctx.Doer.ID) {
			// The user shouldn't know about this organization
			ctx.Error(http.StatusNotFound, "", "The new owner does not exist or cannot be found")
			return
		}
	}

	if !ctx.CheckQuota(quota_model.LimitSubjectSizeReposAll, newOwner.ID, newOwner.Name) {
		return
	}

	var teams []*organization.Team
	if opts.TeamIDs != nil {
		if !newOwner.IsOrganization() {
			ctx.Error(http.StatusUnprocessableEntity, "repoTransfer", "Teams can only be added to organization-owned repositories")
			return
		}

		org := convert.ToOrganization(ctx, organization.OrgFromUser(newOwner))
		for _, tID := range *opts.TeamIDs {
			team, err := organization.GetTeamByID(ctx, tID)
			if err != nil {
				ctx.Error(http.StatusUnprocessableEntity, "team", fmt.Errorf("team %d not found", tID))
				return
			}

			if team.OrgID != org.ID {
				ctx.Error(http.StatusForbidden, "team", fmt.Errorf("team %d belongs not to org %d", tID, org.ID))
				return
			}

			teams = append(teams, team)
		}
	}

	if ctx.Repo.GitRepo != nil {
		ctx.Repo.GitRepo.Close()
		ctx.Repo.GitRepo = nil
	}

	oldFullname := ctx.Repo.Repository.FullName()

	if err := repo_service.StartRepositoryTransfer(ctx, ctx.Doer, newOwner, ctx.Repo.Repository, teams); err != nil {
		if errors.Is(err, user_model.ErrBlockedByUser) {
			ctx.Error(http.StatusForbidden, "StartRepositoryTransfer", err)
			return
		}

		if models.IsErrRepoTransferInProgress(err) {
			ctx.Error(http.StatusConflict, "StartRepositoryTransfer", err)
			return
		}

		if repo_model.IsErrRepoAlreadyExist(err) {
			ctx.Error(http.StatusUnprocessableEntity, "StartRepositoryTransfer", err)
			return
		}

		ctx.InternalServerError(err)
		return
	}

	if ctx.Repo.Repository.Status == repo_model.RepositoryPendingTransfer {
		log.Trace("Repository transfer initiated: %s -> %s", oldFullname, ctx.Repo.Repository.FullName())
		ctx.JSON(http.StatusCreated, convert.ToRepo(ctx, ctx.Repo.Repository, access_model.Permission{AccessMode: perm.AccessModeAdmin}))
		return
	}

	log.Trace("Repository transferred: %s -> %s", oldFullname, ctx.Repo.Repository.FullName())
	ctx.JSON(http.StatusAccepted, convert.ToRepo(ctx, ctx.Repo.Repository, access_model.Permission{AccessMode: perm.AccessModeAdmin}))
}

// AcceptTransfer accept a repo transfer
func AcceptTransfer(ctx *context.APIContext) {
	// swagger:operation POST /repos/{owner}/{repo}/transfer/accept repository acceptRepoTransfer
	// ---
	// summary: Accept a repo transfer
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo to transfer
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repo to transfer
	//   type: string
	//   required: true
	// responses:
	//   "202":
	//     "$ref": "#/responses/Repository"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "413":
	//     "$ref": "#/responses/quotaExceeded"

	err := acceptOrRejectRepoTransfer(ctx, true)
	if ctx.Written() {
		return
	}
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "acceptOrRejectRepoTransfer", err)
		return
	}

	ctx.JSON(http.StatusAccepted, convert.ToRepo(ctx, ctx.Repo.Repository, ctx.Repo.Permission))
}

// RejectTransfer reject a repo transfer
func RejectTransfer(ctx *context.APIContext) {
	// swagger:operation POST /repos/{owner}/{repo}/transfer/reject repository rejectRepoTransfer
	// ---
	// summary: Reject a repo transfer
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo to transfer
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repo to transfer
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/Repository"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "404":
	//     "$ref": "#/responses/notFound"

	err := acceptOrRejectRepoTransfer(ctx, false)
	if ctx.Written() {
		return
	}
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "acceptOrRejectRepoTransfer", err)
		return
	}

	ctx.JSON(http.StatusOK, convert.ToRepo(ctx, ctx.Repo.Repository, ctx.Repo.Permission))
}

func acceptOrRejectRepoTransfer(ctx *context.APIContext, accept bool) error {
	repoTransfer, err := models.GetPendingRepositoryTransfer(ctx, ctx.Repo.Repository)
	if err != nil {
		if models.IsErrNoPendingTransfer(err) {
			ctx.NotFound()
			return nil
		}
		return err
	}

	if err := repoTransfer.LoadAttributes(ctx); err != nil {
		return err
	}

	if !repoTransfer.CanUserAcceptTransfer(ctx, ctx.Doer) {
		ctx.Error(http.StatusForbidden, "CanUserAcceptTransfer", nil)
		return errors.New("user does not have permissions to do this")
	}

	if accept {
		recipient := repoTransfer.Recipient
		if !ctx.CheckQuota(quota_model.LimitSubjectSizeReposAll, recipient.ID, recipient.Name) {
			return nil
		}

		return repo_service.TransferOwnership(ctx, repoTransfer.Doer, repoTransfer.Recipient, ctx.Repo.Repository, repoTransfer.Teams)
	}

	return repo_service.CancelRepositoryTransfer(ctx, ctx.Repo.Repository)
}
