// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package files

import (
	"testing"

	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	"forgejo.org/modules/json"
	"forgejo.org/services/context"
	"forgejo.org/services/contexttest"
	"forgejo.org/services/gitdiff"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Checks whether the diff preview is obtained successfully and as we expect it.
func diffPreviewTestHelper(ctx context.Context, t *testing.T, expectedDiff *gitdiff.Diff, baseBranch, treePath, content string) {
	diff, err := GetDiffPreview(ctx, ctx.Repo.Repository, baseBranch, treePath, content)
	require.NoError(t, err)
	expectedBs, err := json.Marshal(expectedDiff)
	require.NoError(t, err)
	bs, err := json.Marshal(diff)
	require.NoError(t, err)
	assert.Equal(t, expectedBs, bs)
}

// Checks normal README file.
func TestGetDiffPreview(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx, _ := contexttest.MockContext(t, "user2/repo1")
	ctx.SetParams(":id", "1")
	contexttest.LoadRepo(t, ctx, 1)
	contexttest.LoadUser(t, ctx, 2)
	contexttest.LoadGitRepo(t, ctx)
	defer ctx.Repo.GitRepo.Close()

	branch := ctx.Repo.Repository.DefaultBranch
	treePath := "README.md"
	nameHash := "8ec9a00bfd09b3190ac6b22251dbb1aa95a0579d"
	content := "# repo1\n\nDescription for repo1\nthis is a new line"

	expectedDiff := &gitdiff.Diff{
		TotalAddition: 2,
		TotalDeletion: 1,
		Files: []*gitdiff.DiffFile{
			{
				Name:     treePath,
				OldName:  treePath,
				NameHash: nameHash,
				Index:    1,
				Addition: 2,
				Deletion: 1,
				Type:     2,
				Sections: []*gitdiff.DiffSection{
					{
						FileName: treePath,
						Name:     "",
						Lines: []*gitdiff.DiffLine{
							{
								LeftIdx:       0,
								RightIdx:      0,
								Type:          4,
								Content:       "@@ -1,3 +1,4 @@",
								Conversations: nil,
								SectionInfo: &gitdiff.DiffLineSectionInfo{
									Path:          treePath,
									LastLeftIdx:   0,
									LastRightIdx:  0,
									LeftIdx:       1,
									RightIdx:      1,
									LeftHunkSize:  3,
									RightHunkSize: 4,
								},
							},
							{
								LeftIdx:       1,
								RightIdx:      1,
								Type:          1,
								Content:       " # repo1",
								Conversations: nil,
							},
							{
								LeftIdx:       2,
								RightIdx:      2,
								Type:          1,
								Content:       " ",
								Conversations: nil,
							},
							{
								LeftIdx:       3,
								RightIdx:      0,
								Match:         4,
								Type:          3,
								Content:       "-Description for repo1",
								Conversations: nil,
							},
							{
								LeftIdx:       0,
								RightIdx:      3,
								Match:         3,
								Type:          2,
								Content:       "+Description for repo1",
								Conversations: nil,
							},
							{
								LeftIdx:       0,
								RightIdx:      4,
								Match:         -1,
								Type:          2,
								Content:       "+this is a new line",
								Conversations: nil,
							},
						},
					},
				},
			},
		},
	}

	t.Run("TestGetDiffPreview", func(t *testing.T) {
		diffPreviewTestHelper(*ctx, t, expectedDiff, branch, treePath, content)
	})
}

// Checks LFS pointer file (both for when it's created and when it is modified).
func TestGetDiffPreviewLFSPointer(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx, _ := contexttest.MockContext(t, "user2/lfs")
	contexttest.LoadRepo(t, ctx, 54)
	contexttest.LoadUser(t, ctx, 2)
	contexttest.LoadGitRepo(t, ctx)
	defer ctx.Repo.GitRepo.Close()

	treePath := "pixel.jpg"
	nameHash := "286fed4479ff3a120d4a02a4239e453808e46b5b"

	expectedDiff1 := &gitdiff.Diff{
		TotalAddition: 3,
		TotalDeletion: 0,
		Files: []*gitdiff.DiffFile{
			{
				Name:      treePath,
				OldName:   treePath,
				NameHash:  nameHash,
				Index:     1,
				Addition:  3,
				Deletion:  0,
				Type:      1,
				IsCreated: true,
				IsLFSFile: false,
				Sections: []*gitdiff.DiffSection{
					{
						FileName: treePath,
						Name:     "",
						Lines: []*gitdiff.DiffLine{
							{
								LeftIdx:       0,
								RightIdx:      0,
								Match:         0,
								Type:          4,
								Content:       "@@ -0,0 +1,3 @@",
								Conversations: nil,
								SectionInfo: &gitdiff.DiffLineSectionInfo{
									Path:          treePath,
									LastLeftIdx:   0,
									LastRightIdx:  0,
									LeftIdx:       0,
									RightIdx:      1,
									LeftHunkSize:  0,
									RightHunkSize: 3,
								},
							},
							{
								LeftIdx:       0,
								RightIdx:      1,
								Match:         -1,
								Type:          2,
								Content:       "+version https://git-lfs.github.com/spec/v1",
								Conversations: nil,
							},
							{
								LeftIdx:       0,
								RightIdx:      2,
								Match:         -1,
								Type:          2,
								Content:       "+oid sha256:fb120a8de3b12793515dec8667db380383f1c20429a25f800a3a247212f1577f",
								Conversations: nil,
							},
							{
								LeftIdx:       0,
								RightIdx:      3,
								Match:         -1,
								Type:          2,
								Content:       "+size 283",
								Conversations: nil,
							},
						},
					},
				},
				Mode: "100644",
			},
		},
	}

	expectedDiff2 := &gitdiff.Diff{
		TotalAddition: 2,
		TotalDeletion: 2,
		Files: []*gitdiff.DiffFile{
			{
				Name:        treePath,
				OldName:     treePath,
				NameHash:    nameHash,
				Index:       1,
				Addition:    2,
				Deletion:    2,
				Type:        2,
				IsCreated:   false,
				IsDeleted:   false,
				IsBin:       false,
				IsLFSFile:   false,
				IsRenamed:   false,
				IsSubmodule: false,
				Sections: []*gitdiff.DiffSection{
					{
						FileName: treePath,
						Name:     "",
						Lines: []*gitdiff.DiffLine{
							{
								LeftIdx:       0,
								RightIdx:      0,
								Match:         0,
								Type:          4,
								Content:       "@@ -1,3 +1,3 @@",
								Conversations: nil,
								SectionInfo: &gitdiff.DiffLineSectionInfo{
									Path:          treePath,
									LastLeftIdx:   0,
									LastRightIdx:  0,
									LeftIdx:       1,
									RightIdx:      1,
									LeftHunkSize:  3,
									RightHunkSize: 3,
								},
							},
							{
								LeftIdx:       1,
								RightIdx:      1,
								Match:         0,
								Type:          1,
								Content:       " version https://git-lfs.github.com/spec/v1",
								Conversations: nil,
							},
							{
								LeftIdx:       2,
								RightIdx:      0,
								Match:         4,
								Type:          3,
								Content:       "-oid sha256:fb120a8de3b12793515dec8667db380383f1c20429a25f800a3a247212f1577f",
								Conversations: nil,
							},
							{
								LeftIdx:       3,
								RightIdx:      0,
								Match:         5,
								Type:          3,
								Content:       "-size 283",
								Conversations: nil,
							},
							{
								LeftIdx:       0,
								RightIdx:      2,
								Match:         2,
								Type:          2,
								Content:       "+oid sha256:1f98407553d3c43b4a9eea4982c1a673b41eccecfe574bb76d533553fdd723f0",
								Conversations: nil,
							},
							{
								LeftIdx:       0,
								RightIdx:      3,
								Match:         3,
								Type:          2,
								Content:       "+size 283",
								Conversations: nil,
							},
						},
					},
				},
			},
		},
	}

	// The diffs of the tests here cannot have "IsLFSFile" be true, as the LFS
	// file's OID must be that of an LFS file that is associated to the Git
	// repository. In the context of a unit test, this association cannot be
	// established. The testcases in 'image-diff.test.e2e.ts' make up for this.
	t.Run("TestGetDiffPreviewLFSPointer", func(t *testing.T) {
		t.Run("NewLFSPointerFile", func(t *testing.T) {
			content := "version https://git-lfs.github.com/spec/v1\noid sha256:fb120a8de3b12793515dec8667db380383f1c20429a25f800a3a247212f1577f\nsize 283"
			baseBranch := ctx.Repo.Repository.DefaultBranch
			ctx.SetParams(":id", "13")
			diffPreviewTestHelper(*ctx, t, expectedDiff1, baseBranch, treePath, content)
		})

		t.Run("ModifiedLFSPointerFile", func(t *testing.T) {
			content := "version https://git-lfs.github.com/spec/v1\noid sha256:1f98407553d3c43b4a9eea4982c1a673b41eccecfe574bb76d533553fdd723f0\nsize 283"
			baseBranch := "branch-with-new-image"
			ctx.SetParams(":id", "14")
			diffPreviewTestHelper(*ctx, t, expectedDiff2, baseBranch, treePath, content)
		})
	})
}

func TestGetDiffPreviewErrors(t *testing.T) {
	unittest.PrepareTestEnv(t)
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	branch := repo.DefaultBranch
	treePath := "README.md"
	content := "# repo1\n\nDescription for repo1\nthis is a new line"

	t.Run("empty repo", func(t *testing.T) {
		diff, err := GetDiffPreview(db.DefaultContext, &repo_model.Repository{}, branch, treePath, content)
		assert.Nil(t, diff)
		assert.EqualError(t, err, "repository does not exist [id: 0, uid: 0, owner_name: , name: ]")
	})

	t.Run("bad branch", func(t *testing.T) {
		badBranch := "bad_branch"
		diff, err := GetDiffPreview(db.DefaultContext, repo, badBranch, treePath, content)
		assert.Nil(t, diff)
		assert.EqualError(t, err, "branch does not exist [name: "+badBranch+"]")
	})

	t.Run("empty treePath", func(t *testing.T) {
		diff, err := GetDiffPreview(db.DefaultContext, repo, branch, "", content)
		assert.Nil(t, diff)
		assert.EqualError(t, err, "path is invalid [path: ]")
	})
}
