// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package org

import (
	"errors"
	"net/http"
	"strings"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	project_model "forgejo.org/models/project"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/modules/base"
	"forgejo.org/modules/json"
	"forgejo.org/modules/log"
	"forgejo.org/modules/optional"
	project_module "forgejo.org/modules/project"
	"forgejo.org/modules/setting"
	project_structs "forgejo.org/modules/structs"
	"forgejo.org/modules/templates"
	"forgejo.org/modules/util"
	"forgejo.org/modules/web"
	shared_user "forgejo.org/routers/web/shared/user"
	"forgejo.org/services/context"
	"forgejo.org/services/forms"
	project_service "forgejo.org/services/project"
)

const (
	tplProjects     base.TplName = "org/projects/list"
	tplProjectsNew  base.TplName = "org/projects/new"
	tplProjectsView base.TplName = "org/projects/view"
)

func getAndCheckProjectByID(ctx *context.Context, projectID int64) *project_model.Project {
	project, err := project_service.GetProjectByIDForOwner(ctx, projectID, ctx.ContextUser.ID, nil)
	if err != nil {
		ctx.NotFoundOrServerError("GetProjectByIDForOwner", func(err error) bool {
			return errors.Is(err, util.ErrInvalidArgument) || project_model.IsErrProjectNotExist(err)
		}, err)
		return nil
	}
	return project
}

// MustEnableProjects check if projects are enabled in settings
func MustEnableProjects(ctx *context.Context) {
	if unit.TypeProjects.UnitGlobalDisabled() {
		ctx.NotFound("MustEnableProjects", nil)
		return
	}
}

// Projects renders the home page of projects
func Projects(ctx *context.Context) {
	shared_user.PrepareContextForProfileBigAvatar(ctx)
	ctx.Data["Title"] = ctx.Tr("repo.projects")

	sortType := ctx.FormTrim("sort")
	ctx.Data["SortType"] = sortType

	showClosed := strings.EqualFold(ctx.FormTrim("state"), "closed")
	keyword := ctx.FormTrim("q")
	ctx.Data["Keyword"] = keyword
	page := max(ctx.FormInt("page"), 1)

	ownerType := project_service.GetAPIOwnerType(ctx.ContextUser.IsOrganization(), false)
	opts := project_service.GetSearchOpts(
		ctx.ContextUser.ID,
		showClosed,
		sortType,
		keyword,
		ownerType,
		page,
		setting.UI.IssuePagingNum,
	)
	log.Trace("Got project SearchOpts for user %v and owner type %v", ctx.ContextUser.Name, ownerType)
	projects, err := project_service.ListProjectsByOptions(*ctx, opts)
	if err != nil {
		ctx.ServerError("ListProjectsByOptions", err)
		return
	}
	log.Trace("Found %v projects", len(projects))
	total, err := project_service.CountProjectsByOptions(*ctx, opts)
	if err != nil {
		ctx.ServerError("CountProjectsByOptions", err)
		return
	}
	log.Trace("Counted %v projects", total)
	countOpts := project_service.GetSearchOpts(ctx.ContextUser.ID, !showClosed, "", "", ownerType)
	opTotal, err := project_service.CountProjectsByOptions(*ctx, countOpts)
	if err != nil {
		ctx.ServerError("CountProjectsByOptions", err)
		return
	}

	if showClosed {
		ctx.Data["OpenCount"] = opTotal
		ctx.Data["ClosedCount"] = total
		ctx.Data["State"] = "closed"
	} else {
		ctx.Data["OpenCount"] = total
		ctx.Data["ClosedCount"] = opTotal
		ctx.Data["State"] = "open"
	}

	ctx.Data["Projects"] = projects
	shared_user.RenderUserHeader(ctx)

	for _, project := range projects {
		project.RenderedContent = templates.RenderMarkdownToHtml(ctx, project.Description)
	}

	err = shared_user.LoadHeaderCount(ctx)
	if err != nil {
		ctx.ServerError("LoadHeaderCount", err)
		return
	}

	pager := context.NewPagination(len(projects), setting.UI.IssuePagingNum, page, 5)
	pager.AddParam(ctx, "state", "State")
	pager.AddParam(ctx, "q", "Keyword")
	pager.AddParam(ctx, "sort", "SortType")
	ctx.Data["Page"] = pager

	ctx.Data["CanWriteProjects"] = canWriteProjects(ctx)
	ctx.Data["IsShowClosed"] = showClosed
	ctx.Data["PageIsViewProjects"] = true

	numOpenIssues, err := issues_model.NumIssuesInProjects(ctx, projects, ctx.Doer, ctx.Org.Organization, optional.Some(false))
	if err != nil {
		ctx.ServerError("NumIssuesInProjects", err)
		return
	}
	numClosedIssues, err := issues_model.NumIssuesInProjects(ctx, projects, ctx.Doer, ctx.Org.Organization, optional.Some(true))
	if err != nil {
		ctx.ServerError("NumIssuesInProjects", err)
		return
	}
	ctx.Data["NumOpenIssuesInProject"] = numOpenIssues
	ctx.Data["NumClosedIssuesInProject"] = numClosedIssues

	ctx.HTML(http.StatusOK, tplProjects)
}

func canWriteProjects(ctx *context.Context) bool {
	if ctx.ContextUser.IsOrganization() {
		return ctx.Org.CanWriteUnit(ctx, unit.TypeProjects)
	}
	return ctx.Doer != nil && ctx.ContextUser.ID == ctx.Doer.ID
}

// RenderNewProject render creating a project page
func RenderNewProject(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.projects.new")
	ctx.Data["TemplateConfigs"] = project_module.GetAPITemplateConfigs()
	ctx.Data["CardTypes"] = project_module.GetAPICardConfig()
	ctx.Data["CanWriteProjects"] = canWriteProjects(ctx)
	ctx.Data["PageIsViewProjects"] = true
	ctx.Data["HomeLink"] = ctx.ContextUser.HomeLink()
	ctx.Data["CancelLink"] = ctx.ContextUser.HomeLink() + "/-/projects"
	shared_user.RenderUserHeader(ctx)

	err := shared_user.LoadHeaderCount(ctx)
	if err != nil {
		ctx.ServerError("LoadHeaderCount", err)
		return
	}

	ctx.HTML(http.StatusOK, tplProjectsNew)
}

// CreateProject creates a new project
func CreateProject(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.CreateProjectForm)
	ctx.Data["Title"] = ctx.Tr("repo.projects.new")
	shared_user.RenderUserHeader(ctx)

	if ctx.HasError() {
		RenderNewProject(ctx)
		return
	}

	ownerType := project_service.GetAPIOwnerType(ctx.ContextUser.IsOrganization(), false)
	log.Trace("Got owner type %v", ownerType)

	opt := &project_structs.CreateOrUpdateProjectOptions{
		Title:        form.Title,
		Description:  form.Content,
		TemplateType: form.TemplateType,
		CardType:     form.CardType,
		Status:       "open",
	}
	project, err := project_service.NewProject(opt, ctx.ContextUser, nil, ownerType)
	if err != nil {
		ctx.ServerError("NewProject", err)
		return
	}

	if err := project_service.CreateProject(ctx, project); err != nil {
		ctx.ServerError("CreateProject", err)
		return
	}
	log.Trace("Created project with name %v", form.Title)

	ctx.Flash.Success(ctx.Tr("repo.projects.create_success", form.Title))
	ctx.Redirect(ctx.ContextUser.HomeLink() + "/-/projects")
}

// ChangeProjectStatus updates the status of a project between "open" and "close"
func ChangeProjectStatus(ctx *context.Context) {
	var toClose bool
	switch ctx.Params("action") {
	case "open":
		toClose = false
	case "close":
		toClose = true
	default:
		ctx.JSONRedirect(ctx.ContextUser.HomeLink() + "/-/projects")
		return
	}
	project := getAndCheckProjectByID(ctx, ctx.ParamsInt64("id"))
	if ctx.Written() {
		return
	}
	if err := project_service.ChangeProjectStatus(ctx, project, toClose); err != nil {
		ctx.ServerError("ChangeProjectStatus", err)
		return
	}
	ctx.JSONRedirect(project_module.ProjectLinkForOrg(ctx.ContextUser.HomeLink(), ctx.ParamsInt64("id")))
}

// DeleteProject delete a project
func DeleteProject(ctx *context.Context) {
	project := getAndCheckProjectByID(ctx, ctx.ParamsInt64("id"))
	if ctx.Written() {
		return
	}

	if err := project_service.DeleteProjectByID(ctx, project.ID, optional.None[int64]()); err != nil {
		ctx.ServerError("DeleteProjectByID", err)
		return
	}
	ctx.Flash.Success(ctx.Tr("repo.projects.deletion_success"))

	ctx.JSONRedirect(ctx.ContextUser.HomeLink() + "/-/projects")
}

// RenderEditProject allows a project to be edited
func RenderEditProject(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.projects.edit")
	ctx.Data["PageIsEditProjects"] = true
	ctx.Data["PageIsViewProjects"] = true
	ctx.Data["CanWriteProjects"] = canWriteProjects(ctx)
	ctx.Data["CardTypes"] = project_module.GetAPICardConfig()

	shared_user.RenderUserHeader(ctx)

	project := getAndCheckProjectByID(ctx, ctx.ParamsInt64("id"))
	if ctx.Written() {
		return
	}

	ctx.Data["projectID"] = project.ID
	ctx.Data["title"] = project.Title
	ctx.Data["content"] = project.Description
	ctx.Data["redirect"] = ctx.FormString("redirect")
	ctx.Data["HomeLink"] = ctx.ContextUser.HomeLink()
	ctx.Data["card_type"] = project.CardType.ToAPICardType()
	ctx.Data["CancelLink"] = project_module.ProjectLinkForOrg(ctx.ContextUser.HomeLink(), project.ID)

	ctx.HTML(http.StatusOK, tplProjectsNew)
}

// EditProjectPost response for editing a project
func EditProjectPost(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.CreateProjectForm)
	projectID := ctx.ParamsInt64("id")
	ctx.Data["Title"] = ctx.Tr("repo.projects.edit")
	ctx.Data["PageIsEditProjects"] = true
	ctx.Data["PageIsViewProjects"] = true
	ctx.Data["CanWriteProjects"] = canWriteProjects(ctx)
	ctx.Data["CardTypes"] = project_module.GetAPICardConfig()
	ctx.Data["CancelLink"] = project_module.ProjectLinkForOrg(ctx.ContextUser.HomeLink(), projectID)

	shared_user.RenderUserHeader(ctx)

	err := shared_user.LoadHeaderCount(ctx)
	if err != nil {
		ctx.ServerError("LoadHeaderCount", err)
		return
	}

	if ctx.HasError() {
		ctx.HTML(http.StatusOK, tplProjectsNew)
		return
	}

	project := getAndCheckProjectByID(ctx, ctx.ParamsInt64("id"))
	if ctx.Written() {
		return
	}

	updated := &project_structs.CreateOrUpdateProjectOptions{
		Title:        form.Title,
		Description:  form.Content,
		TemplateType: form.TemplateType,
		CardType:     form.CardType,
		Status:       "open",
	}
	if err = project_service.UpdateProject(ctx, project, updated); err != nil {
		ctx.ServerError("UpdateProjects", err)
		return
	}

	ctx.Flash.Success(ctx.Tr("repo.projects.edit_success", project.Title))
	if ctx.FormString("redirect") == "project" {
		ctx.Redirect(project.Link(ctx))
	} else {
		ctx.Redirect(ctx.ContextUser.HomeLink() + "/-/projects")
	}
}

// ViewProject renders the project with board view for a project
func ViewProject(ctx *context.Context) {
	project := getAndCheckProjectByID(ctx, ctx.ParamsInt64("id"))
	if ctx.Written() {
		return
	}

	columns, err := db.Find[project_model.Column](ctx, project_model.FindColumnOptions{
		ListOptions: db.ListOptionsAll, ProjectID: project.ID,
	})
	if err != nil {
		ctx.ServerError("GetColumns", err)
		return
	}

	issuesMap, err := issues_model.LoadIssuesFromColumnList(ctx, columns, ctx.Doer, ctx.Org.Organization, optional.None[bool]())
	if err != nil {
		ctx.ServerError("LoadIssuesFromColumnList", err)
		return
	}

	if project.CardType != project_module.CardTypeTextOnly {
		issuesAttachmentMap := make(map[int64][]*repo_model.Attachment)
		for _, issuesList := range issuesMap {
			for _, issue := range issuesList {
				if issueAttachment, err := repo_model.GetAttachmentsByIssueIDImagesLatest(ctx, issue.ID); err == nil {
					issuesAttachmentMap[issue.ID] = issueAttachment
				}
			}
		}
		ctx.Data["issuesAttachmentMap"] = issuesAttachmentMap
	}

	linkedPrsMap := make(map[int64][]*issues_model.Issue)
	for _, issuesList := range issuesMap {
		for _, issue := range issuesList {
			var referencedIDs []int64
			for _, comment := range issue.Comments {
				if comment.RefIssueID != 0 && comment.RefIsPull {
					referencedIDs = append(referencedIDs, comment.RefIssueID)
				}
			}

			if len(referencedIDs) > 0 {
				if linkedPrs, err := issues_model.Issues(ctx, &issues_model.IssuesOptions{
					IssueIDs:  referencedIDs,
					IsPull:    optional.Some(true),
					User:      ctx.Doer,
					AllPublic: !ctx.IsSigned,
				}); err == nil {
					linkedPrsMap[issue.ID] = linkedPrs
				}
			}
		}
	}

	project.RenderedContent = templates.RenderMarkdownToHtml(ctx, project.Description)
	ctx.Data["Title"] = project.Title
	ctx.Data["LinkedPRs"] = linkedPrsMap
	ctx.Data["PageIsViewProjects"] = true
	ctx.Data["CanWriteProjects"] = canWriteProjects(ctx)
	ctx.Data["Project"] = project
	ctx.Data["IssuesMap"] = issuesMap
	ctx.Data["Columns"] = columns
	shared_user.RenderUserHeader(ctx)

	err = shared_user.LoadHeaderCount(ctx)
	if err != nil {
		ctx.ServerError("LoadHeaderCount", err)
		return
	}

	ctx.HTML(http.StatusOK, tplProjectsView)
}

// DeleteProjectColumn allows for the deletion of a project column
func DeleteProjectColumn(ctx *context.Context) {
	if ctx.Doer == nil {
		ctx.JSON(http.StatusForbidden, map[string]string{
			"message": "Only signed in users are allowed to perform this action.",
		})
		return
	}

	project := getAndCheckProjectByID(ctx, ctx.ParamsInt64("id"))
	if ctx.Written() {
		return
	}

	_, err := project_service.GetValidProjectColumnByID(ctx, project.ID, ctx.ParamsInt64("columnID"))
	if err != nil {
		ctx.NotFoundOrServerError("GetValidProjectColumnByID", project_model.IsErrProjectColumnNotExist, err)
		return
	}

	if err := project_service.DeleteColumnInProject(ctx, ctx.ParamsInt64("columnID")); err != nil {
		ctx.ServerError("DeleteColumnInProject", err)
		return
	}

	ctx.JSONOK()
}

// CreateColumnInProject allows a new column to be added to a project.
func CreateColumnInProject(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.EditProjectColumnForm)

	project := getAndCheckProjectByID(ctx, ctx.ParamsInt64("id"))
	if ctx.Written() {
		return
	}

	if err := project_service.CreateColumnInProject(ctx, &project_model.Column{
		ProjectID: project.ID,
		Title:     form.Title,
		Color:     form.Color,
		CreatorID: ctx.Doer.ID,
	}); err != nil {
		ctx.ServerError("CreateColumnInProject", err)
		return
	}

	ctx.JSONOK()
}

// CheckProjectColumnChangePermissions check permission
func CheckProjectColumnChangePermissions(ctx *context.Context) (*project_model.Project, *project_model.Column) {
	if ctx.Doer == nil {
		ctx.JSON(http.StatusForbidden, map[string]string{
			"message": "Only signed in users are allowed to perform this action.",
		})
		return nil, nil
	}

	project := getAndCheckProjectByID(ctx, ctx.ParamsInt64("id"))
	if ctx.Written() {
		return nil, nil
	}

	column, err := project_service.GetValidProjectColumnByID(ctx, project.ID, ctx.ParamsInt64("columnID"))
	if err != nil {
		ctx.NotFoundOrServerError("GetValidProjectColumnByID", project_model.IsErrProjectColumnNotExist, err)
		return nil, nil
	}
	return project, column
}

// EditProjectColumn allows a project column's to be updated
func EditProjectColumn(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.EditProjectColumnForm)
	_, column := CheckProjectColumnChangePermissions(ctx)
	if ctx.Written() {
		return
	}

	if form.Title != "" {
		column.Title = form.Title
	}
	column.Color = form.Color
	if form.Sorting != 0 {
		column.Sorting = form.Sorting
	}

	if err := project_service.EditColumnInProject(ctx, column); err != nil {
		ctx.ServerError("EditColumnInProject", err)
		return
	}

	ctx.JSONOK()
}

// SetDefaultProjectColumn set default column for uncategorized issues/pulls
func SetDefaultProjectColumn(ctx *context.Context) {
	project, column := CheckProjectColumnChangePermissions(ctx)
	if ctx.Written() {
		return
	}

	if err := project_service.SetDefaultColumn(ctx, project.ID, column.ID); err != nil {
		ctx.ServerError("SetDefaultColumn", err)
		return
	}

	ctx.JSONOK()
}

// MoveIssues moves or keeps issues in a column and sorts them inside that column
func MoveIssues(ctx *context.Context) {
	if ctx.Doer == nil {
		ctx.JSON(http.StatusForbidden, map[string]string{
			"message": "Only signed in users are allowed to perform this action.",
		})
		return
	}

	project := getAndCheckProjectByID(ctx, ctx.ParamsInt64("id"))
	if ctx.Written() {
		return
	}

	column, err := project_service.GetValidProjectColumnByID(ctx, project.ID, ctx.ParamsInt64("columnID"))
	if err != nil {
		ctx.NotFoundOrServerError("GetValidProjectColumnByID", project_model.IsErrProjectColumnNotExist, err)
		return
	}

	form := &project_structs.MoveIssuesOption{}
	if err = json.NewDecoder(ctx.Req.Body).Decode(&form); err != nil {
		ctx.ServerError("DecodeMovedIssuesForm", err)
		return
	}

	existingIssues, complete, err := project_service.GetIssues(ctx, form.GetIssueIDs())
	if err != nil {
		ctx.ServerError("GetIssues", err)
		return
	}

	if !complete {
		ctx.Flash.Warning(ctx.Tr("project.missing_issues_in_list"), true)
	}

	if err = project_service.ValidIssueIDs(ctx, project.OwnerID, existingIssues); err != nil {
		ctx.ServerError("ValidIssueID", err)
		return
	}

	if err = project_service.MoveIssuesOnProjectColumn(ctx, column, form); err != nil {
		ctx.ServerError("MoveIssuesOnProjectColumn", err)
		return
	}

	ctx.JSONOK()
}
