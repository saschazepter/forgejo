// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package common

import (
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	repo_module "forgejo.org/modules/repository"
)

// CompareInfo represents the collected results from ParseCompareInfo
type CompareInfo struct {
	HeadUser         *user_model.User
	HeadRepo         *repo_model.Repository
	CompareInfo      *repo_module.CrossRepositoryCompare
	BaseBranch       string
	HeadBranch       string
	DirectComparison bool
}

func (ci *CompareInfo) Close() {
	if ci.CompareInfo != nil {
		ci.CompareInfo.Close()
		ci.CompareInfo = nil
	}
}
