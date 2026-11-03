// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package forgery

import (
	"cmp"
	"io/fs"
	"testing"

	repo_model "forgejo.org/models/repo"
	unit_model "forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	repo_service "forgejo.org/services/repository"
	wiki_service "forgejo.org/services/wiki"

	"code.forgejo.org/xorm/xorm/convert"
	"github.com/stretchr/testify/require"
)

type CreateRepositoryOptions struct {
	Name string // if nil a unique name (derived from the test name) will be generated

	// Content of the initial commit, if nil the git repo will be left uninitialized.
	// Use [MapFS] or [FilesInit] to setup the initial files.
	Files fs.FS

	ObjectFormat  git.ObjectFormat // If nil, SHA1
	IsTemplate    bool
	IsPrivate     bool
	DefaultBranch string

	LatestSha   *string // if not nil, the commit sha after initializing the repo with the Files will be written to this ref
	SkipCleanup bool    // if true the repo will not be deleted at the end of the test (can be useful to debug locally)
}

// FilesInit specifies the templates to use upon repository initialization.
type FilesInit struct {
	Readme     string
	Gitignores string
	License    string
}

func (FilesInit) Open(name string) (fs.File, error) {
	panic("FilesInit is only a sentinel value")
}

// CreateRepository returns the repo, owner and opts can be nil
func CreateRepository(t testing.TB, owner *user_model.User, opts *CreateRepositoryOptions) *repo_model.Repository {
	t.Helper()

	if owner == nil {
		owner = CreateUser(t, nil) // if specific options are needed, create the owner manually
	}
	if opts == nil {
		opts = &CreateRepositoryOptions{}
	}

	repoName := opts.Name
	if repoName == "" {
		repoName = "repo-" + uniqueSafeName(t.Name())
	}

	gitFormat := cmp.Or(opts.ObjectFormat, git.Sha1ObjectFormat)

	defaultBranch := "main"
	if opts.DefaultBranch != "" {
		defaultBranch = opts.DefaultBranch
	}

	// Create the repository
	createOptions := repo_service.CreateRepoOptions{
		Name:             repoName,
		Description:      "Test Repo",
		DefaultBranch:    defaultBranch,
		IsTemplate:       opts.IsTemplate,
		ObjectFormatName: gitFormat.Name(),
		IsPrivate:        opts.IsPrivate,
	}
	if fi, ok := opts.Files.(FilesInit); ok {
		createOptions.AutoInit = true
		createOptions.Readme = cmp.Or(fi.Readme, "Default")
		createOptions.Gitignores = fi.Gitignores
		createOptions.License = fi.License
	}
	repo, err := repo_service.CreateRepositoryDirectly(t.Context(), owner, owner, createOptions)
	require.NoError(t, err)
	if !opts.SkipCleanup {
		t.Cleanup(func() {
			_ = repo_service.DeleteRepository(t.Context(), owner, repo, false)
		})
	}
	require.NotEmpty(t, repo)

	if !createOptions.AutoInit && opts.Files != nil {
		sha, err := initRepo(owner, repo, gitFormat, opts.Files, "init")
		require.NoError(t, err, "Make sure the Forgejo HTTP server is running (or use %T instead of %T for the Files field)", FilesInit{}, opts.Files)
		if opts.LatestSha != nil {
			*opts.LatestSha = sha
		}

		// reload the repo since pushing a commit might update the model via the push_update queue (IsEmpty for instance)
		repo, err = repo_model.GetRepositoryByID(t.Context(), repo.ID)
		require.NoError(t, err)
	} else if opts.LatestSha != nil {
		require.False(t, repo.IsEmpty, "Without Files, no LatestSha can be retrieved")

		gitRepo, err := git.OpenRepository(git.DefaultContext, repo.RepoPath())
		require.NoError(t, err)
		defer gitRepo.Close()

		*opts.LatestSha, err = gitRepo.GetBranchCommitID(defaultBranch)
		require.NoError(t, err)
	}
	repo.Owner = owner

	return repo
}

func InitWiki(t testing.TB, repo *repo_model.Repository, branch string) {
	// Set the wiki branch in the database first
	repo.WikiBranch = branch
	err := repo_model.UpdateRepositoryCols(t.Context(), repo, "wiki_branch")
	require.NoError(t, err)

	// Initialize the wiki
	err = wiki_service.InitWiki(t.Context(), repo)
	require.NoError(t, err)

	// Add a new wiki page
	err = wiki_service.AddWikiPage(t.Context(), repo.Owner, repo, "Home", "Welcome to the wiki!", "Add a Home page")
	require.NoError(t, err)
}

func EnableRepoUnit(t testing.TB, repo *repo_model.Repository, unit unit_model.Type, config convert.Conversion) {
	t.Helper()

	err := repo_service.UpdateRepositoryUnits(t.Context(), repo, []repo_model.RepoUnit{{
		RepoID: repo.ID,
		Type:   unit,
		Config: config,
	}}, nil)
	require.NoError(t, err)
}

// to specify a non-default config, call [EnableRepoUnit] instead
func EnableRepoUnits(t testing.TB, repo *repo_model.Repository, units ...unit_model.Type) {
	t.Helper()

	ru := make([]repo_model.RepoUnit, 0, len(units))
	for _, u := range units {
		var config convert.Conversion
		if u == unit_model.TypePullRequests { // pull request config is needed (otherwise no merge allowed by default)
			config = &repo_model.PullRequestsConfig{
				AllowMerge:           true,
				AllowRebase:          true,
				AllowRebaseMerge:     true,
				AllowSquash:          true,
				AllowFastForwardOnly: true,
				AllowManualMerge:     true,
				AllowRebaseUpdate:    true,
				DefaultMergeStyle:    repo_model.MergeStyleMerge,
				DefaultUpdateStyle:   repo_model.UpdateStyleMerge,
			}
		}
		ru = append(ru, repo_model.RepoUnit{
			RepoID: repo.ID,
			Type:   u,
			Config: config,
		})
	}
	err := repo_service.UpdateRepositoryUnits(t.Context(), repo, ru, nil)
	require.NoError(t, err)
}

func DisableRepoUnits(t testing.TB, repo *repo_model.Repository, units ...unit_model.Type) {
	t.Helper()

	err := repo_service.UpdateRepositoryUnits(t.Context(), repo, nil, units)
	require.NoError(t, err)
}
