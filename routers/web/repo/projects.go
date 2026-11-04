// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"errors"
	"net/http"
	"strings"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	"forgejo.org/models/perm"
	project_model "forgejo.org/models/project"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/modules/base"
	"forgejo.org/modules/json"
	"forgejo.org/modules/log"
	"forgejo.org/modules/markup"
	"forgejo.org/modules/markup/markdown"
	"forgejo.org/modules/optional"
	project_module "forgejo.org/modules/project"
	"forgejo.org/modules/setting"
	project_structs "forgejo.org/modules/structs"
	"forgejo.org/modules/util"
	"forgejo.org/modules/web"
	"forgejo.org/services/context"
	"forgejo.org/services/forms"
	project_service "forgejo.org/services/project"
)

const (
	tplProjects     base.TplName = "repo/projects/list"
	tplProjectsNew  base.TplName = "repo/projects/new"
	tplProjectsView base.TplName = "repo/projects/view"
)

// MustEnableProjects check if projects are enabled in settings
func MustEnableProjects(ctx *context.Context) {
	if unit.TypeProjects.UnitGlobalDisabled() {
		ctx.NotFound("EnableRepoProjects", nil)
		return
	}

	if ctx.Repo.Repository != nil {
		if !ctx.Repo.CanRead(unit.TypeProjects) {
			ctx.NotFound("MustEnableProjects", nil)
			return
		}
	}
}

func getAndCheckProjectByID(ctx *context.Context, projectID int64) *project_model.Project {
	project, err := project_service.GetProjectByIDForOwner(ctx, projectID, 0, ctx.Repo.Repository)
	if err != nil {
		ctx.NotFoundOrServerError("GetProjectByIDForOwner", func(err error) bool {
			return errors.Is(err, util.ErrInvalidArgument) || project_model.IsErrProjectNotExist(err)
		}, err)
		return nil
	}
	return project
}

// Projects renders the home page of projects
func Projects(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.projects")

	sortType := ctx.FormTrim("sort")
	ctx.Data["SortType"] = sortType

	showClosed := strings.EqualFold(ctx.FormTrim("state"), "closed")
	keyword := ctx.FormTrim("q")
	ctx.Data["Keyword"] = keyword
	repo := ctx.Repo.Repository
	page := max(ctx.FormInt("page"), 1)

	ctx.Data["OpenCount"] = repo.NumOpenProjects
	ctx.Data["ClosedCount"] = repo.NumClosedProjects

	var total int
	if !showClosed {
		total = repo.NumOpenProjects
	} else {
		total = repo.NumClosedProjects
	}

	opts := project_service.GetSearchOpts(
		repo.ID,
		showClosed,
		sortType,
		keyword,
		project_module.APIOwnerTypeRepository,
		page,
		setting.UI.IssuePagingNum,
	)
	log.Trace("Got project SearchOpts for repo %v and owner type %v", repo.Name, project_module.APIOwnerTypeRepository)
	projects, err := project_service.ListProjectsByOptions(ctx, opts)
	if err != nil {
		ctx.ServerError("ListProjectsByOptions", err)
		return
	}
	log.Trace("Found %v projects", len(projects))
	for i := range projects {
		projects[i].RenderedContent, err = markdown.RenderString(&markup.RenderContext{
			Links: markup.Links{
				Base: ctx.Repo.RepoLink,
			},
			Metas:   ctx.Repo.Repository.ComposeMetas(ctx),
			GitRepo: ctx.Repo.GitRepo,
			Ctx:     ctx,
		}, projects[i].Description)
		if err != nil {
			ctx.ServerError("RenderString", err)
			return
		}
	}
	log.Trace("Counted %v projects", total)
	ctx.Data["Projects"] = projects

	if showClosed {
		ctx.Data["State"] = "closed"
	} else {
		ctx.Data["State"] = "open"
	}

	pager := context.NewPagination(len(projects), setting.UI.IssuePagingNum, page, 5)
	pager.AddParam(ctx, "state", "State")
	pager.AddParam(ctx, "q", "Keyword")
	pager.AddParam(ctx, "sort", "SortType")
	ctx.Data["Page"] = pager

	ctx.Data["CanWriteProjects"] = ctx.Repo.CanWrite(unit.TypeProjects)
	ctx.Data["IsShowClosed"] = showClosed
	ctx.Data["IsProjectsPage"] = true

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

// RenderNewProject render creating a project page
func RenderNewProject(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.projects.new")
	ctx.Data["TemplateConfigs"] = project_module.GetAPITemplateConfigs()
	ctx.Data["CardTypes"] = project_module.GetAPICardConfig()
	ctx.Data["CanWriteProjects"] = ctx.Repo.CanWrite(unit.TypeProjects)
	ctx.Data["CancelLink"] = ctx.Repo.Repository.Link() + "/projects"
	ctx.HTML(http.StatusOK, tplProjectsNew)
}

// CreateProject creates a new project
func CreateProject(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.CreateProjectForm)
	ctx.Data["Title"] = ctx.Tr("repo.projects.new")

	if ctx.HasError() {
		RenderNewProject(ctx)
		return
	}

	opt := &project_structs.CreateOrUpdateProjectOptions{
		Title:        form.Title,
		Description:  form.Content,
		TemplateType: form.TemplateType,
		CardType:     form.CardType,
		Status:       "open",
	}
	project, err := project_service.NewProject(opt, ctx.ContextUser, ctx.Repo.Repository, project_module.APIOwnerTypeRepository)
	if err != nil {
		ctx.ServerError("NewProject", err)
		return
	}

	if err := project_service.CreateProject(ctx, project); err != nil {
		ctx.ServerError("CreateProject", err)
		return
	}
	log.Trace("Created project %v for repo %v", form.Title, ctx.Repo.Repository.Name)

	ctx.Flash.Success(ctx.Tr("repo.projects.create_success", form.Title))
	ctx.Redirect(ctx.Repo.RepoLink + "/projects")
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
		ctx.JSONRedirect(ctx.Repo.RepoLink + "/projects")
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
	ctx.JSONRedirect(project_module.ProjectLinkForRepo(ctx.Repo.Repository.Link(), ctx.ParamsInt64("id")))
}

// DeleteProject delete a project
func DeleteProject(ctx *context.Context) {
	project := getAndCheckProjectByID(ctx, ctx.ParamsInt64("id"))
	if ctx.Written() {
		return
	}

	repoID := optional.Option[int64]{ctx.Repo.Repository.ID}
	if err := project_service.DeleteProjectByID(ctx, project.ID, repoID); err != nil {
		ctx.ServerError("DeleteProjectByID", err)
		return
	}
	ctx.Flash.Success(ctx.Tr("repo.projects.deletion_success"))

	ctx.JSONRedirect(ctx.Repo.RepoLink + "/projects")
}

// RenderEditProject allows a project to be edited
func RenderEditProject(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.projects.edit")
	ctx.Data["PageIsEditProjects"] = true
	ctx.Data["CanWriteProjects"] = ctx.Repo.CanWrite(unit.TypeProjects)
	ctx.Data["CardTypes"] = project_module.GetAPICardConfig()

	project := getAndCheckProjectByID(ctx, ctx.ParamsInt64("id"))
	if ctx.Written() {
		return
	}

	ctx.Data["projectID"] = project.ID
	ctx.Data["title"] = project.Title
	ctx.Data["content"] = project.Description
	ctx.Data["card_type"] = project.CardType.ToAPICardType()
	ctx.Data["redirect"] = ctx.FormString("redirect")
	ctx.Data["CancelLink"] = project_module.ProjectLinkForRepo(ctx.Repo.Repository.Link(), project.ID)

	ctx.HTML(http.StatusOK, tplProjectsNew)
}

// EditProjectPost response for editing a project
func EditProjectPost(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.CreateProjectForm)
	projectID := ctx.ParamsInt64("id")

	ctx.Data["Title"] = ctx.Tr("repo.projects.edit")
	ctx.Data["PageIsEditProjects"] = true
	ctx.Data["CanWriteProjects"] = ctx.Repo.CanWrite(unit.TypeProjects)
	ctx.Data["CardTypes"] = project_module.GetAPICardConfig()
	ctx.Data["CancelLink"] = project_module.ProjectLinkForRepo(ctx.Repo.Repository.Link(), projectID)

	if ctx.HasError() {
		ctx.HTML(http.StatusOK, tplProjectsNew)
		return
	}

	project := getAndCheckProjectByID(ctx, ctx.ParamsInt64("id"))
	if ctx.Written() {
		return
	}

	opt := &project_structs.CreateOrUpdateProjectOptions{
		Title:       form.Title,
		Description: form.Content,
		CardType:    form.CardType,
	}

	if err := project_service.UpdateProject(ctx, project, opt); err != nil {
		ctx.ServerError("UpdateProjects", err)
		return
	}

	ctx.Flash.Success(ctx.Tr("repo.projects.edit_success", project.Title))
	if ctx.FormString("redirect") == "project" {
		ctx.Redirect(project.Link(ctx))
	} else {
		ctx.Redirect(ctx.Repo.RepoLink + "/projects")
	}
}

// ViewProject renders the project with board view
func ViewProject(ctx *context.Context) {
	project := getAndCheckProjectByID(ctx, ctx.ParamsInt64("id"))
	if ctx.Written() {
		return
	}

	columns, err := db.Find[project_model.Column](ctx, project_model.FindColumnOptions{
		ListOptions: db.ListOptionsAll, ProjectID: project.ID,
	})
	if err != nil {
		ctx.ServerError("ListProjectColumns", err)
		return
	}

	issuesMap, err := issues_model.LoadIssuesFromColumnList(ctx, columns, ctx.Doer, nil, optional.None[bool]())
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
	ctx.Data["LinkedPRs"] = linkedPrsMap

	project.RenderedContent, err = markdown.RenderString(&markup.RenderContext{
		Links: markup.Links{
			Base: ctx.Repo.RepoLink,
		},
		Metas:   ctx.Repo.Repository.ComposeMetas(ctx),
		GitRepo: ctx.Repo.GitRepo,
		Ctx:     ctx,
	}, project.Description)
	if err != nil {
		ctx.ServerError("RenderString", err)
		return
	}

	ctx.Data["Title"] = project.Title
	ctx.Data["IsProjectsPage"] = true
	ctx.Data["CanWriteProjects"] = ctx.Repo.CanWrite(unit.TypeProjects)
	ctx.Data["Project"] = project
	ctx.Data["IssuesMap"] = issuesMap
	ctx.Data["Columns"] = columns

	ctx.HTML(http.StatusOK, tplProjectsView)
}

// UpdateIssueProject change an issue's project
func UpdateIssueProject(ctx *context.Context) {
	issues := getActionIssues(ctx)
	if ctx.Written() {
		return
	}

	if err := issues.LoadProjects(ctx); err != nil {
		ctx.ServerError("LoadProjects", err)
		return
	}
	if _, err := issues.LoadRepositories(ctx); err != nil {
		ctx.ServerError("LoadRepositories", err)
		return
	}

	projectID := ctx.FormInt64("id")
	for _, issue := range issues {
		if issue.Project != nil && issue.Project.ID == projectID {
			continue
		}
		if err := issues_model.IssueAssignOrRemoveProject(ctx, issue, ctx.Doer, projectID, 0); err != nil {
			if errors.Is(err, util.ErrPermissionDenied) {
				continue
			}
			ctx.ServerError("IssueAssignOrRemoveProject", err)
			return
		}
	}

	ctx.JSONOK()
}

// DeleteProjectColumn allows for the deletion of a project column
func DeleteProjectColumn(ctx *context.Context) {
	if ctx.Doer == nil {
		ctx.JSON(http.StatusForbidden, map[string]string{
			"message": "Only signed in users are allowed to perform this action.",
		})
		return
	}

	if !ctx.Repo.IsOwner() && !ctx.Repo.IsAdmin() && !ctx.Repo.CanAccess(perm.AccessModeWrite, unit.TypeProjects) {
		ctx.JSON(http.StatusForbidden, map[string]string{
			"message": "Only authorized users are allowed to perform this action.",
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
	if !ctx.Repo.IsOwner() && !ctx.Repo.IsAdmin() && !ctx.Repo.CanAccess(perm.AccessModeWrite, unit.TypeProjects) {
		ctx.JSON(http.StatusForbidden, map[string]string{
			"message": "Only authorized users are allowed to perform this action.",
		})
		return
	}

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

func checkProjectColumnChangePermissions(ctx *context.Context) (*project_model.Project, *project_model.Column) {
	if ctx.Doer == nil {
		ctx.JSON(http.StatusForbidden, map[string]string{
			"message": "Only signed in users are allowed to perform this action.",
		})
		return nil, nil
	}

	if !ctx.Repo.IsOwner() && !ctx.Repo.IsAdmin() && !ctx.Repo.CanAccess(perm.AccessModeWrite, unit.TypeProjects) {
		ctx.JSON(http.StatusForbidden, map[string]string{
			"message": "Only authorized users are allowed to perform this action.",
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
	_, column := checkProjectColumnChangePermissions(ctx)
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
	project, column := checkProjectColumnChangePermissions(ctx)
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

	if !ctx.Repo.IsOwner() && !ctx.Repo.IsAdmin() && !ctx.Repo.CanAccess(perm.AccessModeWrite, unit.TypeProjects) {
		ctx.JSON(http.StatusForbidden, map[string]string{
			"message": "Only authorized users are allowed to perform this action.",
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

	for _, issue := range existingIssues {
		if issue.RepoID != project.RepoID {
			ctx.ServerError("Some issue's repoID is not equal to project's repoID", errors.New("Some issue's repoID is not equal to project's repoID"))
			return
		}
	}

	if err = project_service.MoveIssuesOnProjectColumn(ctx, column, form); err != nil {
		ctx.ServerError("MoveIssuesOnProjectColumn", err)
		return
	}

	ctx.JSONOK()
}
