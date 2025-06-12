// Copyright Earl Warren <contact@earl-warren.org>
// Copyright Loïc Dachary <loic@dachary.org>
// SPDX-License-Identifier: MIT

package driver

import (
	"context"

	repo_model "forgejo.org/models/repo"

	"code.forgejo.org/f3/gof3/v3/f3"
	helpers_repository "code.forgejo.org/f3/gof3/v3/forges/helpers/repository"
	f3_id "code.forgejo.org/f3/gof3/v3/id"
	f3_tree "code.forgejo.org/f3/gof3/v3/tree/f3"
	"code.forgejo.org/f3/gof3/v3/tree/generic"
)

var _ f3_tree.ForgeDriverInterface = &repository{}

type repository struct {
	common

	name string
	h    helpers_repository.Interface

	f *f3.Repository
}

func (o *repository) SetNative(repository any) {
	o.name = repository.(string)
}

func (o *repository) GetNativeID() string {
	return o.name
}

func (o *repository) NewFormat() f3.Interface {
	return &f3.Repository{}
}

func (o *repository) ToFormat() f3.Interface {
	return &f3.Repository{
		Common:    f3.NewCommon(o.GetNativeID()),
		Name:      o.GetNativeID(),
		FetchFunc: o.f.FetchFunc,
	}
}

func (o *repository) FromFormat(content f3.Interface) {
	f := content.Clone().(*f3.Repository)
	o.f = f
	o.f.SetID(f.Name)
	o.name = f.Name
}

func (o *repository) Get(ctx context.Context) bool {
	return o.h.Get(ctx)
}

func (o *repository) Put(ctx context.Context) f3_id.NodeID {
	return o.upsert(ctx)
}

func (o *repository) Patch(ctx context.Context) {
	o.upsert(ctx)
}

func (o *repository) upsert(ctx context.Context) f3_id.NodeID {
	o.Trace("%s", o.GetNativeID())
	o.h.Upsert(ctx, o.f)
	return f3_id.NewNodeID(o.f.Name)
}

func (o *repository) SetFetchFunc(fetchFunc func(ctx context.Context, destination, internalRef string)) {
	o.f.FetchFunc = fetchFunc
}

func (o *repository) getURL() string {
	owner := f3_tree.GetOwnerName(o.GetNode())
	repoName := f3_tree.GetProjectName(o.GetNode())
	if o.f.GetID() == f3.RepositoryNameWiki {
		repoName += ".wiki"
	}
	return repo_model.RepoPath(owner, repoName)
}

func (o *repository) GetRepositoryURL() string {
	return o.getURL()
}

func (o *repository) GetRepositoryPushURL() string {
	return o.getURL()
}

func (o *repository) GetRepositoryInternalRef() string {
	return ""
}

func (o *repository) GetPullRequestBranch(pr *f3.PullRequestBranch) *f3.PullRequestBranch {
	panic("")
}
func (o *repository) CreatePullRequestBranch(pr *f3.PullRequestBranch) {}
func (o *repository) DeletePullRequestBranch(pr *f3.PullRequestBranch) {}

func newRepository(_ context.Context) generic.NodeDriverInterface {
	r := &repository{
		f: &f3.Repository{},
	}
	r.h = helpers_repository.NewHelper(r)
	return r
}
