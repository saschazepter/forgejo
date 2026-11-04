// Copyright 2016 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package context

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	auth_model "forgejo.org/models/auth"
	issues_model "forgejo.org/models/issues"
	org_model "forgejo.org/models/organization"
	"forgejo.org/models/perm"
	access_model "forgejo.org/models/perm/access"
	quota_model "forgejo.org/models/quota"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
	mc "forgejo.org/modules/cache"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/httpcache"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/web"
	web_types "forgejo.org/modules/web/types"
	apiv1_permissions "forgejo.org/routers/api/v1/permissions"
	"forgejo.org/services/auth"
	"forgejo.org/services/authz"

	"code.forgejo.org/go-chi/cache"
)

var ghMediaRE = regexp.MustCompile(`application/vnd\.github(\.[^+]+)?\+json`)

// APIContext is a specific context for API service
type APIContext struct {
	*Base

	cache cache.Cache

	doer           *user_model.User // current signed-in user
	isSigned       bool
	authentication auth.AuthenticationResult

	user *user_model.User // the user which is being visited, in most cases it differs from Doer

	repo       *Repository
	issues     map[int64]*issues_model.Issue
	comments   map[int64]*issues_model.Comment
	org        *APIOrganization
	pkg        *Package
	project    *Project
	quotaGroup *quota_model.Group
	quotaRule  *quota_model.Rule
	publicOnly bool // Whether the request is for a public endpoint
	reducer    authz.AuthorizationReducer

	requiredScopeCategories []auth_model.AccessTokenScopeCategory
}

func init() {
	web.RegisterResponseStatusProvider[*APIContext](func(req *http.Request) web_types.ResponseStatusProvider {
		return req.Context().Value(apiContextKey).(*APIContext)
	})
}

// Currently, we have the following common fields in error response:
// * message: the message for end users (it shouldn't be used for error type detection)
//            if we need to indicate some errors, we should introduce some new fields like ErrorCode or ErrorType
// * url:     the swagger document URL

// APIError is error format response
// swagger:response error
type swaggerAPIError struct {
	// in:body
	Body api.APIError `json:"body"`
}

type APIValidationError struct {
	Message string `json:"message"`
	URL     string `json:"url"`
}

// APIValidationError is error format response related to input validation
// swagger:response validationError
type swaggerAPIValidationError struct {
	// in:body
	Body APIValidationError `json:"body"`
}

type APIInvalidTopicsError struct {
	Message       string   `json:"message"`
	InvalidTopics []string `json:"invalidTopics"`
}

// APIInvalidTopicsError is error format response to invalid topics
// swagger:response invalidTopicsError
type swaggerAPIInvalidTopicsError struct {
	// in:body
	Body APIInvalidTopicsError `json:"body"`
}

// APIEmpty is an empty response
// swagger:response empty
type APIEmpty struct{}

type APIUnauthorizedError struct {
	api.APIError
}

// APIUnauthorizedError is a unauthorized error response
// swagger:response unauthorized
type swaggerAPUnauthorizedError struct {
	// in:body
	Body APIUnauthorizedError `json:"body"`
}

type APIForbiddenError struct {
	api.APIError
}

// APIForbiddenError is a forbidden error response
// swagger:response forbidden
type swaggerAPIForbiddenError struct {
	// in:body
	Body APIForbiddenError `json:"body"`
}

type APINotFound struct {
	Message string   `json:"message"`
	URL     string   `json:"url"`
	Errors  []string `json:"errors"`
}

// APINotFound is a not found error response
// swagger:response notFound
type swaggerAPINotFound struct {
	// in:body
	Body APINotFound `json:"body"`
}

// APIConflict is a conflict empty response
// swagger:response conflict
type APIConflict struct{}

// APIRedirect is a redirect response
// swagger:response redirect
type APIRedirect struct{}

// APIString is a string response
// swagger:response string
type APIString string

type APIRepoArchivedError struct {
	api.APIError
}

// APIRepoArchivedError is an error that is raised when an archived repo should be modified
// swagger:response repoArchivedError
type swaggerAPIRepoArchivedError struct {
	// in:body
	Body APIRepoArchivedError `json:"body"`
}

type APIInternalServerError struct {
	api.APIError
}

// APIInternalServerError is an error that is raised when an internal server error occurs
// swagger:response internalServerError
type swaggerAPIInternalServerError struct {
	// in:body
	Body APIInternalServerError `json:"body"`
}

// ServerError responds with error message, status is 500
func (ctx *APIContext) ServerError(title string, err error) {
	ctx.Error(http.StatusInternalServerError, title, err)
}

func (ctx *APIContext) Context() context.Context {
	return ctx.originCtx
}

func (ctx *APIContext) Cache() cache.Cache {
	return ctx.cache
}

func (ctx *APIContext) Doer() *user_model.User {
	return ctx.doer
}

func (ctx *APIContext) SetDoer(doer *user_model.User) {
	ctx.doer = doer
}

func (ctx *APIContext) IsSigned() bool {
	return ctx.isSigned
}

func (ctx *APIContext) SetIsSigned(isSigned bool) {
	ctx.isSigned = isSigned
}

func (ctx *APIContext) Authentication() auth.AuthenticationResult {
	return ctx.authentication
}

func (ctx *APIContext) SetAuthentication(authentication auth.AuthenticationResult) {
	ctx.authentication = authentication
}

func (ctx *APIContext) User() *user_model.User {
	return ctx.user
}

func (ctx *APIContext) SetUser(user *user_model.User) {
	ctx.user = user
}

func (ctx *APIContext) Repo() *Repository {
	return ctx.repo
}

func (ctx *APIContext) SetRepo(repo *Repository) {
	ctx.repo = repo
}

func (ctx *APIContext) LoadIssue(indexParam string) *issues_model.Issue {
	id := ctx.ParamsInt64(indexParam)
	issue, ok := ctx.issues[id]
	if !ok {
		var err error
		issue, err = issues_model.GetIssueByIndex(ctx.Context(), ctx.Repository().ID, id)
		if err != nil {
			if issues_model.IsErrIssueNotExist(err) {
				ctx.NotFound("IsErrIssueNotExist", err)
				return nil
			}
			ctx.Error(http.StatusInternalServerError, "GetIssueByIndex", err)
			return nil
		}

		issue.Repo = ctx.Repo().Repository

		ctx.issues[id] = issue
	}
	return issue
}

func (ctx *APIContext) Repository() *repo_model.Repository {
	return ctx.Repo().Repository
}

func (ctx *APIContext) Permission() *access_model.Permission {
	return &ctx.Repo().Permission
}

func (ctx *APIContext) SetPermission(permission *access_model.Permission) {
	ctx.Repo().Permission = *permission
}

func (ctx *APIContext) Organization() *org_model.Organization {
	return ctx.Org().Organization
}

func (ctx *APIContext) Org() *APIOrganization {
	return ctx.org
}

func (ctx *APIContext) SetOrg(org *APIOrganization) {
	ctx.org = org
}

func (ctx *APIContext) Team() *org_model.Team {
	return ctx.Org().Team
}

func (ctx *APIContext) Package() *Package {
	return ctx.pkg
}

func (ctx *APIContext) PackageOwner() *user_model.User {
	if ctx.Package() == nil {
		return nil
	}
	return ctx.Package().Owner
}

func (ctx *APIContext) LoadComment(idParam string) *issues_model.Comment {
	id := ctx.ParamsInt64(idParam)
	comment, ok := ctx.comments[id]
	if !ok {
		var err error
		comment, err = issues_model.GetCommentByID(ctx, id)
		if err != nil {
			if issues_model.IsErrCommentNotExist(err) {
				ctx.NotFound(err)
			} else {
				ctx.InternalServerError(err)
			}
			return nil
		}

		if err = comment.LoadIssue(ctx); err != nil {
			ctx.InternalServerError(err)
			return nil
		}

		comment.Issue.Repo = ctx.Repo().Repository

		ctx.comments[id] = comment
	}
	return comment
}

func (ctx *APIContext) PackageAccessMode() perm.AccessMode {
	if ctx.Package() == nil {
		return perm.AccessModeNone
	}
	return ctx.Package().AccessMode
}

func (ctx *APIContext) QuotaGroup() *quota_model.Group {
	return ctx.quotaGroup
}

func (ctx *APIContext) QuotaRule() *quota_model.Rule {
	return ctx.quotaRule
}

func (ctx *APIContext) PublicOnly() bool {
	return ctx.publicOnly
}

func (ctx *APIContext) SetPublicOnly(publicOnly bool) {
	ctx.publicOnly = publicOnly
}

func (ctx *APIContext) Reducer() authz.AuthorizationReducer {
	return ctx.reducer
}

func (ctx *APIContext) SetReducer(reducer authz.AuthorizationReducer) {
	ctx.reducer = reducer
}

func (ctx *APIContext) RequiredScopeCategories() []auth_model.AccessTokenScopeCategory {
	return ctx.requiredScopeCategories
}

func (ctx *APIContext) SetRequiredScopeCategories(requiredScopeCategories []auth_model.AccessTokenScopeCategory) {
	ctx.requiredScopeCategories = requiredScopeCategories
}

// Error responds with an error message to client with given obj as the message.
// If status is 500, also it prints error to log.
func (ctx *APIContext) Error(status int, title string, obj any) {
	var message string
	if err, ok := obj.(error); ok {
		message = err.Error()
	} else {
		message = fmt.Sprintf("%s", obj)
	}

	if status == http.StatusInternalServerError {
		log.ErrorWithSkip(1, "%s: %s", title, message)

		if setting.IsProd && (ctx.Doer() == nil || !ctx.Doer().IsAdmin) {
			message = ""
		}
	}

	ctx.JSON(status, api.APIError{
		Message: message,
		URL:     setting.API.SwaggerURL,
	})
}

// InternalServerError responds with an error message to the client with the error as a message
// and the file and line of the caller.
func (ctx *APIContext) InternalServerError(err error) {
	log.ErrorWithSkip(1, "InternalServerError: %v", err)

	var message string
	if !setting.IsProd || (ctx.Doer() != nil && ctx.Doer().IsAdmin) {
		message = err.Error()
	}

	ctx.JSON(http.StatusInternalServerError, api.APIError{
		Message: message,
		URL:     setting.API.SwaggerURL,
	})
}

func (ctx *APIContext) GetError() error {
	return errors.New("unexpected call to APIContext.GetError")
}

func (ctx *APIContext) Project() *Project {
	return ctx.project
}

func (ctx *APIContext) SetProject(p *Project) {
	ctx.project = p
}

type apiContextKeyType struct{}

var apiContextKey = apiContextKeyType{}

// GetAPIContext returns a context for API routes
func GetAPIContext(req *http.Request) *APIContext {
	return req.Context().Value(apiContextKey).(*APIContext)
}

func genAPILinks(curURL *url.URL, total, pageSize, curPage int) []string {
	page := NewPagination(total, pageSize, curPage, 0)
	paginater := page.Paginater
	links := make([]string, 0, 4)

	if paginater.HasNext() {
		u := *curURL
		queries := u.Query()
		queries.Set("page", fmt.Sprintf("%d", paginater.Next()))
		u.RawQuery = queries.Encode()

		links = append(links, fmt.Sprintf("<%s%s>; rel=\"next\"", setting.AppURL, u.RequestURI()[1:]))
	}
	if !paginater.IsLast() {
		u := *curURL
		queries := u.Query()
		queries.Set("page", fmt.Sprintf("%d", paginater.TotalPages()))
		u.RawQuery = queries.Encode()

		links = append(links, fmt.Sprintf("<%s%s>; rel=\"last\"", setting.AppURL, u.RequestURI()[1:]))
	}
	if !paginater.IsFirst() {
		u := *curURL
		queries := u.Query()
		queries.Set("page", "1")
		u.RawQuery = queries.Encode()

		links = append(links, fmt.Sprintf("<%s%s>; rel=\"first\"", setting.AppURL, u.RequestURI()[1:]))
	}
	if paginater.HasPrevious() {
		u := *curURL
		queries := u.Query()
		queries.Set("page", fmt.Sprintf("%d", paginater.Previous()))
		u.RawQuery = queries.Encode()

		links = append(links, fmt.Sprintf("<%s%s>; rel=\"prev\"", setting.AppURL, u.RequestURI()[1:]))
	}
	return links
}

// SetLinkHeader sets pagination link header by given total number and page size.
func (ctx *APIContext) SetLinkHeader(total, pageSize int) {
	links := genAPILinks(ctx.Req.URL, total, pageSize, ctx.FormInt("page"))

	if len(links) > 0 {
		ctx.RespHeader().Set("Link", strings.Join(links, ","))
		ctx.AppendAccessControlExposeHeaders("Link")
	}
}

// APIContexter returns apicontext as middleware
func APIContexter() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			base, baseCleanUp := NewBaseContext(w, req)
			ctx := &APIContext{
				Base:     base,
				issues:   make(map[int64]*issues_model.Issue),
				comments: make(map[int64]*issues_model.Comment),
				cache:    mc.GetCache(),
				repo:     &Repository{PullRequest: &PullRequest{}},
				org:      &APIOrganization{},
			}
			defer baseCleanUp()

			ctx.AppendContextValue(apiContextKey, ctx)
			ctx.AppendContextValueFunc(gitrepo.RepositoryContextKey, func() any { return ctx.Repo().GitRepo })

			httpcache.SetCacheControlInHeader(ctx.Resp.Header(), 0)
			ctx.Resp.Header().Set(`X-Frame-Options`, setting.CORSConfig.XFrameOptions)

			next.ServeHTTP(ctx.Resp, ctx.Req)
		})
	}
}

// NotFound handles 404s for APIContext
// String will replace message, errors will be added to a slice
func (ctx *APIContext) NotFound(objs ...any) {
	message := ctx.Locale.TrString("error.not_found")
	errors := make([]string, 0)
	for _, obj := range objs {
		// Ignore nil
		if obj == nil {
			continue
		}

		if err, ok := obj.(error); ok {
			errors = append(errors, err.Error())
		} else {
			message = obj.(string)
		}
	}

	ctx.JSON(http.StatusNotFound, APINotFound{
		Message: message,
		URL:     setting.API.SwaggerURL,
		Errors:  errors,
	})
}

// ReferencesGitRepo injects the GitRepo into the Context
// you can optional skip the IsEmpty check
func ReferencesGitRepo(allowEmpty ...bool) func(ctx *APIContext) (cancel context.CancelFunc) {
	return func(ctx *APIContext) (cancel context.CancelFunc) {
		// Empty repository does not have reference information.
		if ctx.Repo().Repository.IsEmpty && (len(allowEmpty) == 0 || !allowEmpty[0]) {
			return nil
		}

		// For API calls.
		if ctx.Repo().GitRepo == nil {
			gitRepo, err := gitrepo.OpenRepository(ctx, ctx.Repo().Repository)
			if err != nil {
				ctx.Error(http.StatusInternalServerError, fmt.Sprintf("Open Repository %v failed", ctx.Repo().Repository.FullName()), err)
				return cancel
			}
			ctx.Repo().GitRepo = gitRepo
			// We opened it, we should close it
			return func() {
				// If it's been set to nil then assume someone else has closed it.
				if ctx.Repo().GitRepo != nil {
					_ = ctx.Repo().GitRepo.Close()
				}
			}
		}

		return cancel
	}
}

// RepoRefForAPI handles repository reference names when the ref name is not explicitly given
func RepoRefForAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := GetAPIContext(req)

		if ctx.Repo().Repository.IsEmpty {
			ctx.NotFound(errors.New("repository is empty"))
			return
		}

		if ctx.Repo().GitRepo == nil {
			ctx.InternalServerError(errors.New("no open git repo"))
			return
		}

		if ref := ctx.FormTrim("ref"); len(ref) > 0 {
			commit, err := ctx.Repo().GitRepo.GetCommit(ref)
			if err != nil {
				if git.IsErrNotExist(err) {
					ctx.NotFound()
				} else {
					ctx.Error(http.StatusInternalServerError, "GetCommit", err)
				}
				return
			}
			ctx.Repo().Commit = commit
			ctx.Repo().CommitID = ctx.Repo().Commit.ID.String()
			ctx.Repo().TreePath = ctx.Params("*")
			next.ServeHTTP(w, req)
			return
		}

		refName := getRefName(ctx.Base, ctx.Repo(), RepoRefAny)
		var err error

		if ctx.Repo().GitRepo.IsBranchExist(refName) {
			ctx.Repo().Commit, err = ctx.Repo().GitRepo.GetBranchCommit(refName)
			if err != nil {
				ctx.InternalServerError(err)
				return
			}
			ctx.Repo().CommitID = ctx.Repo().Commit.ID.String()
		} else if ctx.Repo().GitRepo.IsTagExist(refName) {
			ctx.Repo().Commit, err = ctx.Repo().GitRepo.GetTagCommit(refName)
			if err != nil {
				ctx.InternalServerError(err)
				return
			}
			ctx.Repo().CommitID = ctx.Repo().Commit.ID.String()
		} else if len(refName) == ctx.Repo().GetObjectFormat().FullLength() {
			ctx.Repo().CommitID = refName
			ctx.Repo().Commit, err = ctx.Repo().GitRepo.GetCommit(refName)
			if err != nil {
				ctx.NotFound("GetCommit", err)
				return
			}
		} else {
			ctx.NotFound(fmt.Errorf("not exist: '%s'", ctx.Params("*")))
			return
		}

		next.ServeHTTP(w, req)
	})
}

// HasAPIError returns true if error occurs in form validation.
func (ctx *APIContext) HasAPIError() bool {
	hasErr, ok := ctx.Data["HasError"]
	if !ok {
		return false
	}
	return hasErr.(bool)
}

// GetErrMsg returns error message in form validation.
func (ctx *APIContext) GetErrMsg() string {
	msg, _ := ctx.Data["ErrorMsg"].(string)
	if msg == "" {
		msg = "invalid form data"
	}
	return msg
}

// NotFoundOrServerError use error check function to determine if the error
// is about not found. It responds with 404 status code for not found error,
// or error context description for logging purpose of 500 server error.
func (ctx *APIContext) NotFoundOrServerError(logMsg string, errCheck func(error) bool, logErr error) {
	if errCheck(logErr) {
		ctx.JSON(http.StatusNotFound, nil)
		return
	}
	ctx.Error(http.StatusInternalServerError, "NotFoundOrServerError", logMsg)
}

// IsUserSiteAdmin returns true if current user is a site admin
func (ctx *APIContext) IsUserSiteAdmin() bool {
	return apiv1_permissions.IsUserSiteAdmin(ctx)
}

// IsUserRepoAdmin returns true if current user is admin in current repo
func (ctx *APIContext) IsUserRepoAdmin() bool {
	return apiv1_permissions.IsUserRepoAdmin(ctx)
}

// IsUserRepoWriter returns true if current user has write privilege in current repo
func (ctx *APIContext) IsUserRepoWriter(unitTypes []unit.Type) bool {
	return apiv1_permissions.IsUserRepoWriter(ctx, unitTypes)
}

// Returns true when the requests indicates that it accepts a Github response.
// This should be used to return information in the way that the Github API
// specifies it. Avoids breaking compatibility with non-Github API clients.
func (ctx *APIContext) AcceptsGithubResponse() bool {
	return ghMediaRE.MatchString(ctx.Req.Header.Get("Accept"))
}
