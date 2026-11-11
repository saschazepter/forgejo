// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package gitdiff

import (
	"strconv"
	"strings"
	"testing"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	pull_model "forgejo.org/models/pull"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/paginator"
	"forgejo.org/modules/setting"

	dmp "github.com/sergi/go-diff/diffmatchpatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiffToHTML(t *testing.T) {
	assert.Equal(t, "foo <span class=\"added-code\">bar</span> biz", diffToHTML(nil, []dmp.Diff{
		{Type: dmp.DiffEqual, Text: "foo "},
		{Type: dmp.DiffInsert, Text: "bar"},
		{Type: dmp.DiffDelete, Text: " baz"},
		{Type: dmp.DiffEqual, Text: " biz"},
	}, DiffLineAdd))

	assert.Equal(t, "foo <span class=\"removed-code\">bar</span> biz", diffToHTML(nil, []dmp.Diff{
		{Type: dmp.DiffEqual, Text: "foo "},
		{Type: dmp.DiffDelete, Text: "bar"},
		{Type: dmp.DiffInsert, Text: " baz"},
		{Type: dmp.DiffEqual, Text: " biz"},
	}, DiffLineDel))
}

func TestParsePatch_singlefile(t *testing.T) {
	type testcase struct {
		name        string
		gitdiff     string
		wantErr     bool
		addition    int
		deletion    int
		oldFilename string
		filename    string
	}

	tests := []testcase{
		{
			name: "readme.md2readme.md",
			gitdiff: `diff --git "\\a/README.md" "\\b/README.md"
--- "\\a/README.md"
+++ "\\b/README.md"
@@ -1,3 +1,6 @@
 # gitea-github-migrator
+
+ Build Status
- Latest Release
 Docker Pulls
+ cut off
+ cut off
`,
			addition:    4,
			deletion:    1,
			filename:    "README.md",
			oldFilename: "README.md",
		},
		{
			name: "A \\ B",
			gitdiff: `diff --git "a/A \\ B" "b/A \\ B"
--- "a/A \\ B"
+++ "b/A \\ B"
@@ -1,3 +1,6 @@
 # gitea-github-migrator
+
+ Build Status
- Latest Release
 Docker Pulls
+ cut off
+ cut off`,
			addition:    4,
			deletion:    1,
			filename:    "A \\ B",
			oldFilename: "A \\ B",
		},
		{
			name: "really weird filename",
			gitdiff: `diff --git "\\a/a b/file b/a a/file" "\\b/a b/file b/a a/file"
index d2186f1..f5c8ed2 100644
--- "\\a/a b/file b/a a/file"	` + `
+++ "\\b/a b/file b/a a/file"	` + `
@@ -1,3 +1,2 @@
 Create a weird file.
 ` + `
-and what does diff do here?
\ No newline at end of file`,
			addition:    0,
			deletion:    1,
			filename:    "a b/file b/a a/file",
			oldFilename: "a b/file b/a a/file",
		},
		{
			name: "delete file with blanks",
			gitdiff: `diff --git "\\a/file with blanks" "\\b/file with blanks"
deleted file mode 100644
index 898651a..0000000
--- "\\a/file with blanks" ` + `
+++ /dev/null
@@ -1,5 +0,0 @@
-a blank file
-
-has a couple o line
-
-the 5th line is the last
`,
			addition:    0,
			deletion:    5,
			filename:    "file with blanks",
			oldFilename: "file with blanks",
		},
		{
			name: "rename a—as",
			gitdiff: `diff --git "a/\360\243\220\265b\342\200\240vs" "b/a\342\200\224as"
similarity index 100%
rename from "\360\243\220\265b\342\200\240vs"
rename to "a\342\200\224as"
`,
			addition:    0,
			deletion:    0,
			oldFilename: "𣐵b†vs",
			filename:    "a—as",
		},
		{
			name: "rename with spaces",
			gitdiff: `diff --git "\\a/a b/file b/a a/file" "\\b/a b/a a/file b/b file"
similarity index 100%
rename from a b/file b/a a/file
rename to a b/a a/file b/b file
`,
			oldFilename: "a b/file b/a a/file",
			filename:    "a b/a a/file b/b file",
		},
		{
			name: "ambiguous deleted",
			gitdiff: `diff --git a/b b/b b/b b/b
deleted file mode 100644
index 92e798b..0000000
--- a/b b/b` + "\t" + `
+++ /dev/null
@@ -1 +0,0 @@
-b b/b
`,
			oldFilename: "b b/b",
			filename:    "b b/b",
			addition:    0,
			deletion:    1,
		},
		{
			name: "ambiguous addition",
			gitdiff: `diff --git a/b b/b b/b b/b
new file mode 100644
index 0000000..92e798b
--- /dev/null
+++ b/b b/b` + "\t" + `
@@ -0,0 +1 @@
+b b/b
`,
			oldFilename: "b b/b",
			filename:    "b b/b",
			addition:    1,
			deletion:    0,
		},
		{
			name: "rename",
			gitdiff: `diff --git a/b b/b b/b b/b b/b b/b
similarity index 100%
rename from b b/b b/b b/b b/b
rename to b
`,
			oldFilename: "b b/b b/b b/b b/b",
			filename:    "b",
		},
		{
			name: "ambiguous 1",
			gitdiff: `diff --git a/b b/b b/b b/b b/b b/b
similarity index 100%
rename from b b/b b/b b/b b/b
rename to b
`,
			oldFilename: "b b/b b/b b/b b/b",
			filename:    "b",
		},
		{
			name: "ambiguous 2",
			gitdiff: `diff --git a/b b/b b/b b/b b/b b/b
similarity index 100%
rename from b b/b b/b b/b
rename to b b/b
`,
			oldFilename: "b b/b b/b b/b",
			filename:    "b b/b",
		},
		{
			name: "minuses-and-pluses",
			gitdiff: `diff --git a/minuses-and-pluses b/minuses-and-pluses
index 6961180..9ba1a00 100644
--- a/minuses-and-pluses
+++ b/minuses-and-pluses
@@ -1,4 +1,4 @@
--- 1st line
-++ 2nd line
--- 3rd line
-++ 4th line
+++ 1st line
+-- 2nd line
+++ 3rd line
+-- 4th line
`,
			oldFilename: "minuses-and-pluses",
			filename:    "minuses-and-pluses",
			addition:    4,
			deletion:    4,
		},
	}

	for _, testcase := range tests {
		t.Run(testcase.name, func(t *testing.T) {
			got, err := ParsePatch(db.DefaultContext, setting.Git.MaxGitDiffLines, setting.Git.MaxGitDiffLineCharacters, strings.NewReader(testcase.gitdiff))
			if (err != nil) != testcase.wantErr {
				t.Errorf("ParsePatch(%q) error = %v, wantErr %v", testcase.name, err, testcase.wantErr)
				return
			}

			if got.TotalAddition != testcase.addition {
				t.Errorf("ParsePath(%q) does not have correct totalAddition %d, wanted %d", testcase.name, got.TotalAddition, testcase.addition)
			}
			if got.TotalDeletion != testcase.deletion {
				t.Errorf("ParsePath(%q) did not have correct totalDeletion %d, wanted %d", testcase.name, got.TotalDeletion, testcase.deletion)
			}
			file := got.Files[0]
			if file.Addition != testcase.addition {
				t.Errorf("ParsePath(%q) does not have correct file addition %d, wanted %d", testcase.name, file.Addition, testcase.addition)
			}
			if file.Deletion != testcase.deletion {
				t.Errorf("ParsePath(%q) did not have correct file deletion %d, wanted %d", testcase.name, file.Deletion, testcase.deletion)
			}
			if file.OldName != testcase.oldFilename {
				t.Errorf("ParsePath(%q) did not have correct OldName %q, wanted %q", testcase.name, file.OldName, testcase.oldFilename)
			}
			if file.Name != testcase.filename {
				t.Errorf("ParsePath(%q) did not have correct Name %q, wanted %q", testcase.name, file.Name, testcase.filename)
			}
		})
	}

	// Test max lines
	diffBuilder := &strings.Builder{}

	diff := `diff --git a/newfile2 b/newfile2
new file mode 100644
index 0000000..6bb8f39
--- /dev/null
+++ b/newfile2
@@ -0,0 +1,35 @@
`
	diffBuilder.WriteString(diff)

	for i := range 35 {
		diffBuilder.WriteString("+line" + strconv.Itoa(i) + "\n")
	}
	diff = diffBuilder.String()
	_, err := ParsePatch(db.DefaultContext, 20, setting.Git.MaxGitDiffLineCharacters, strings.NewReader(diff))
	if err != nil {
		t.Errorf("There should not be an error: %v", err)
	}
	_, err = ParsePatch(db.DefaultContext, 40, setting.Git.MaxGitDiffLineCharacters, strings.NewReader(diff))
	if err != nil {
		t.Errorf("There should not be an error: %v", err)
	}
	_, err = ParsePatch(db.DefaultContext, 40, 5, strings.NewReader(diff))
	if err != nil {
		t.Errorf("There should not be an error: %v", err)
	}

	// Test max characters
	diff = `diff --git a/newfile2 b/newfile2
new file mode 100644
index 0000000..6bb8f39
--- /dev/null
+++ b/newfile2
@@ -0,0 +1,35 @@
`
	diffBuilder.Reset()
	diffBuilder.WriteString(diff)

	for i := range 33 {
		diffBuilder.WriteString("+line" + strconv.Itoa(i) + "\n")
	}
	diffBuilder.WriteString("+line33")
	for range 512 {
		diffBuilder.WriteString("0123456789ABCDEF")
	}
	diffBuilder.WriteByte('\n')
	diffBuilder.WriteString("+line" + strconv.Itoa(34) + "\n")
	diffBuilder.WriteString("+line" + strconv.Itoa(35) + "\n")
	diff = diffBuilder.String()

	_, err = ParsePatch(db.DefaultContext, 20, 4096, strings.NewReader(diff))
	if err != nil {
		t.Errorf("There should not be an error: %v", err)
	}
	_, err = ParsePatch(db.DefaultContext, 40, 4096, strings.NewReader(diff))
	if err != nil {
		t.Errorf("There should not be an error: %v", err)
	}

	diff = `diff --git "a/README.md" "b/README.md"
--- a/README.md
+++ b/README.md
@@ -1,3 +1,6 @@
 # gitea-github-migrator
+
+ Build Status
- Latest Release
 Docker Pulls
+ cut off
+ cut off`
	_, err = ParsePatch(db.DefaultContext, setting.Git.MaxGitDiffLines, setting.Git.MaxGitDiffLineCharacters, strings.NewReader(diff))
	if err != nil {
		t.Errorf("ParsePatch failed: %s", err)
	}

	diff2 := `diff --git "a/A \\ B" "b/A \\ B"
--- "a/A \\ B"
+++ "b/A \\ B"
@@ -1,3 +1,6 @@
 # gitea-github-migrator
+
+ Build Status
- Latest Release
 Docker Pulls
+ cut off
+ cut off`
	_, err = ParsePatch(db.DefaultContext, setting.Git.MaxGitDiffLines, setting.Git.MaxGitDiffLineCharacters, strings.NewReader(diff2))
	if err != nil {
		t.Errorf("ParsePatch failed: %s", err)
	}

	diff2a := `diff --git "a/A \\ B" b/A/B
--- "a/A \\ B"
+++ b/A/B
@@ -1,3 +1,6 @@
 # gitea-github-migrator
+
+ Build Status
- Latest Release
 Docker Pulls
+ cut off
+ cut off`
	_, err = ParsePatch(db.DefaultContext, setting.Git.MaxGitDiffLines, setting.Git.MaxGitDiffLineCharacters, strings.NewReader(diff2a))
	if err != nil {
		t.Errorf("ParsePatch failed: %s", err)
	}

	diff3 := `diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1,3 +1,6 @@
 # gitea-github-migrator
+
+ Build Status
- Latest Release
 Docker Pulls
+ cut off
+ cut off`
	_, err = ParsePatch(db.DefaultContext, setting.Git.MaxGitDiffLines, setting.Git.MaxGitDiffLineCharacters, strings.NewReader(diff3))
	if err != nil {
		t.Errorf("ParsePatch failed: %s", err)
	}
}

func setupDefaultDiff() *Diff {
	return &Diff{
		Files: []*DiffFile{
			{
				Name: "README.md",
				Sections: []*DiffSection{
					{
						Lines: []*DiffLine{
							{
								LeftIdx:  4,
								RightIdx: 4,
							},
						},
					},
				},
			},
		},
	}
}

func TestDiff_LoadCommentsNoOutdated(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 2})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	diff := setupDefaultDiff()
	require.NoError(t, diff.LoadComments(db.DefaultContext, issue, user, false, ""))
	assert.Len(t, diff.Files[0].Sections[0].Lines[0].Conversations, 2)
}

func TestDiff_LoadCommentsWithOutdated(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 2})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	diff := setupDefaultDiff()
	require.NoError(t, diff.LoadComments(db.DefaultContext, issue, user, true, ""))
	assert.Len(t, diff.Files[0].Sections[0].Lines[0].Conversations, 2)
	assert.Len(t, diff.Files[0].Sections[0].Lines[0].Conversations[0], 2)
	assert.Len(t, diff.Files[0].Sections[0].Lines[0].Conversations[1], 1)
}

func TestDiffLine_CanComment(t *testing.T) {
	assert.False(t, (&DiffLine{Type: DiffLineSection}).CanComment())
	assert.False(t, (&DiffLine{Type: DiffLineAdd, Conversations: []issues_model.CodeConversation{{{Content: "bla"}}}}).CanComment())
	assert.True(t, (&DiffLine{Type: DiffLineAdd}).CanComment())
	assert.True(t, (&DiffLine{Type: DiffLineDel}).CanComment())
	assert.True(t, (&DiffLine{Type: DiffLinePlain}).CanComment())
}

func TestDiffLine_GetCommentSide(t *testing.T) {
	assert.Equal(t, "previous", (&DiffLine{Conversations: []issues_model.CodeConversation{{{Line: -3}}}}).GetCommentSide())
	assert.Equal(t, "proposed", (&DiffLine{Conversations: []issues_model.CodeConversation{{{Line: 3}}}}).GetCommentSide())
}

func TestGetDiffRangeWithWhitespaceBehavior(t *testing.T) {
	gitRepo, err := git.OpenRepository(git.DefaultContext, "./testdata/academic-module")
	require.NoError(t, err)

	defer gitRepo.Close()
	for _, behavior := range []git.TrustedCmdArgs{{"-w"}, {"--ignore-space-at-eol"}, {"-b"}, nil} {
		diffs, _, err := GetDiffSimple(db.DefaultContext, gitRepo,
			&DiffOptions{
				AfterCommitID:      "bd7063cc7c04689c4d082183d32a604ed27a24f9",
				BeforeCommitID:     "559c156f8e0178b71cb44355428f24001b08fc68",
				MaxLines:           setting.Git.MaxGitDiffLines,
				MaxLineCharacters:  setting.Git.MaxGitDiffLineCharacters,
				WhitespaceBehavior: behavior,
			})
		require.NoError(t, err, "Error when diff with %s", behavior)
		for _, f := range diffs.Files {
			assert.NotEmpty(t, f.Sections, "%s should have sections", f.Name)
		}
	}
}

func TestGetDiffFull(t *testing.T) {
	gitRepo, err := git.OpenRepository(git.DefaultContext, "./../../modules/git/tests/repos/language_stats_repo")
	require.NoError(t, err)

	defer gitRepo.Close()

	t.Run("Initial commit", func(t *testing.T) {
		diff, err := GetDiffFull(db.DefaultContext, gitRepo,
			&DiffOptions{
				AfterCommitID:     "8fee858da5796dfb37704761701bb8e800ad9ef3",
				MaxLines:          setting.Git.MaxGitDiffLines,
				MaxLineCharacters: setting.Git.MaxGitDiffLineCharacters,
			})
		require.NoError(t, err)

		assert.Empty(t, diff.Start)
		assert.Empty(t, diff.End)
		assert.Equal(t, 23, diff.TotalAddition)
		assert.Len(t, diff.Files, 5)

		assert.True(t, diff.Files[0].IsVendored)
		assert.Equal(t, ".gitattributes", diff.Files[0].Name)
		assert.Equal(t, "24139dae656713ba861751fb2c2ac38839349a7a", diff.Files[0].NameHash)

		assert.Equal(t, "Python", diff.Files[1].Language)
		assert.Equal(t, "i-am-a-python.p", diff.Files[1].Name)
		assert.Equal(t, "32154957b043de62cbcdbe254a53ec4c3e00c5a0", diff.Files[1].NameHash)

		assert.Equal(t, "java-hello/main.java", diff.Files[2].Name)
		assert.Equal(t, "ef9f6a406a4cde7bb5480ba7b027bdc8cd6fa11d", diff.Files[2].NameHash)

		assert.Equal(t, "main.vendor.java", diff.Files[3].Name)
		assert.Equal(t, "c94fd7272f109d4d21d6df2b637c864a5ab63f46", diff.Files[3].NameHash)

		assert.Equal(t, "python-hello/hello.py", diff.Files[4].Name)
		assert.Equal(t, "021705ba8b98778dc4e277d3a6ea1b8c6122a7f9", diff.Files[4].NameHash)
	})

	t.Run("Normal diff", func(t *testing.T) {
		diff, err := GetDiffFull(db.DefaultContext, gitRepo,
			&DiffOptions{
				AfterCommitID:     "341fca5b5ea3de596dc483e54c2db28633cd2f97",
				BeforeCommitID:    "8fee858da5796dfb37704761701bb8e800ad9ef3",
				MaxLines:          setting.Git.MaxGitDiffLines,
				MaxLineCharacters: setting.Git.MaxGitDiffLineCharacters,
			})
		require.NoError(t, err)

		assert.Empty(t, diff.Start)
		assert.Empty(t, diff.End)
		assert.Equal(t, 1, diff.TotalAddition)
		assert.Len(t, diff.Files, 1)

		assert.Equal(t, ".gitattributes", diff.Files[0].Name)
		assert.Equal(t, "24139dae656713ba861751fb2c2ac38839349a7a", diff.Files[0].NameHash)
		assert.Len(t, diff.Files[0].Sections, 2)
		assert.Equal(t, 4, diff.Files[0].Sections[1].Lines[0].SectionInfo.LeftIdx)
	})
}

func TestGetDiffNameStatus_GetsAllChangedFiles(t *testing.T) {
	gitRepo, err := git.OpenRepository(git.DefaultContext, "./../../modules/git/tests/repos/language_stats_repo")
	require.NoError(t, err)

	files, _ := GetDiffNameStatus(db.DefaultContext, gitRepo, "8fee858da5796dfb37704761701bb8e800ad9ef3", "5684d0c8cfdfb17fcd59101826efc9ff54b80df4", 1)

	assert.Len(t, files, 3)
	assert.ElementsMatch(t, files, []*DiffFileMetadata{
		{
			Name:     ".gitattributes",
			NameHash: "24139dae656713ba861751fb2c2ac38839349a7a",
			Type:     DiffFileChange,
			OnPage:   1,
		},
		{
			Name:     "README.md",
			NameHash: "8ec9a00bfd09b3190ac6b22251dbb1aa95a0579d",
			Type:     DiffFileAdd,
			OnPage:   2,
		},
		{
			Name:     "data/schematics/testfile.mts",
			NameHash: "5bf400e6197d3c0d5bf4aec2345abf4161d1c07a",
			Type:     DiffFileAdd,
			OnPage:   3,
		},
	})

	gitRepo.Close()
	gitRepo, err = git.OpenRepository(git.DefaultContext, "./../../modules/git/tests/repos/renames")
	require.NoError(t, err)

	defer gitRepo.Close()
	files, _ = GetDiffNameStatus(db.DefaultContext, gitRepo, "bc40f00489096a7d4090a609a6572f528e1acb76", "f667f3a24223414e3bfbe01ab6e445c703ab8e25", 1)

	assert.Len(t, files, 1)
	assert.ElementsMatch(t, files, []*DiffFileMetadata{
		{
			Name:     "README.md",
			OldName:  optional.Some("REAMDE"),
			NameHash: "8ec9a00bfd09b3190ac6b22251dbb1aa95a0579d",
			Type:     DiffFileRename,
			OnPage:   1,
		},
	})
}

func TestDiffLine_GetExpandDirection(t *testing.T) {
	tests := []struct {
		name           string
		diffLine       *DiffLine
		expectedResult DiffLineExpandDirection
	}{
		{
			name: "non-section line - no expansion",
			diffLine: &DiffLine{
				Type:        DiffLineAdd,
				SectionInfo: &DiffLineSectionInfo{},
			},
			expectedResult: DiffLineExpandNone,
		},
		{
			name: "nil section info - no expansion",
			diffLine: &DiffLine{
				Type:        DiffLineSection,
				SectionInfo: nil,
			},
			expectedResult: DiffLineExpandNone,
		},
		{
			name: "no lines between",
			diffLine: &DiffLine{
				Type: DiffLineSection,
				SectionInfo: &DiffLineSectionInfo{
					// Previous section of the diff displayed up to line 530...
					LastRightIdx: 530,
					LastLeftIdx:  530,
					// This section of the diff starts at line 531...
					RightIdx: 531,
					LeftIdx:  531,
				},
			},
			// There are zero lines between 530 and 531, so we should have nothing to expand.
			expectedResult: DiffLineExpandNone,
		},
		{
			name: "first diff section is the start of the file",
			diffLine: &DiffLine{
				Type: DiffLineSection,
				SectionInfo: &DiffLineSectionInfo{
					// Last[...]Idx is set to zero when it's the first section in the file (and not 1, which would be
					// the first section -is- the first line of the file).
					LastRightIdx: 0,
					LastLeftIdx:  0,
					// The diff section is showing line 1, the top of th efile.
					RightIdx: 1,
					LeftIdx:  1,
				},
			},
			// We're at the top of the file; no expansion.
			expectedResult: DiffLineExpandNone,
		},
		{
			name: "first diff section doesn't start at the top of the file",
			diffLine: &DiffLine{
				Type: DiffLineSection,
				SectionInfo: &DiffLineSectionInfo{
					// Last[...]Idx is set to zero when it's the first section in the file (and not 1, which would be
					// the first section -is- the first line of the file).
					LastRightIdx: 0,
					LastLeftIdx:  0,
					RightIdx:     531,
					LeftIdx:      531,
				},
			},
			// We're at the top of the diff but there's content above, so can only expand up.
			expectedResult: DiffLineExpandUp,
		},
		{
			name: "middle of the file with single expansion",
			diffLine: &DiffLine{
				Type: DiffLineSection,
				SectionInfo: &DiffLineSectionInfo{
					// Previous section ended at ~500...
					LastRightIdx: 500,
					LastLeftIdx:  500,
					// Next section starts one line away...
					RightIdx: 502,
					LeftIdx:  502,
					// The next block has content (> 0)
					RightHunkSize: 50,
					LeftHunkSize:  50,
				},
			},
			// Can be expanded in a single direction, displaying the missing line (501).
			expectedResult: DiffLineExpandSingle,
		},
		{
			name: "middle of the file with multi line expansion",
			diffLine: &DiffLine{
				Type: DiffLineSection,
				SectionInfo: &DiffLineSectionInfo{
					// Previous section ended at ~500...
					LastRightIdx: 500,
					LastLeftIdx:  500,
					// Lines 501-520 are hidden, exactly 20 lines, matching BlobExcerptChunkSize (20)...
					RightIdx: 521,
					LeftIdx:  521,
					// The next block has content (> 0)
					RightHunkSize: 50,
					LeftHunkSize:  50,
				},
			},
			// Can be expanded in a single direction, displaying all the hidden 20 lines.
			expectedResult: DiffLineExpandSingle,
		},
		{
			name: "middle of the file with multi direction expansion",
			diffLine: &DiffLine{
				Type: DiffLineSection,
				SectionInfo: &DiffLineSectionInfo{
					// Previous section ended at ~500...
					LastRightIdx: 500,
					LastLeftIdx:  500,
					// Lines 501-521 are hidden, exactly 21 lines, exceeding BlobExcerptChunkSize (20)...
					RightIdx: 522,
					LeftIdx:  522,
					// The next block has content (> 0)
					RightHunkSize: 50,
					LeftHunkSize:  50,
				},
			},
			// Now can be expanded down to display from 501-520 (521 remains hidden), or up to display 502-521 (501
			// remains hidden).
			expectedResult: DiffLineExpandUpDown,
		},
		{
			name: "end of the diff but still file content to display",
			diffLine: &DiffLine{
				Type: DiffLineSection,
				SectionInfo: &DiffLineSectionInfo{
					// We had a previous diff section, of any size/location...
					LastRightIdx: 200,
					LastLeftIdx:  200,
					RightIdx:     531,
					LeftIdx:      531,
					// Hunk size size 0 is a placeholder value for the end or beginning of a file...
					RightHunkSize: 0,
					LeftHunkSize:  0,
				},
			},
			// Combination of conditions says we're at the end of the last diff section, can only expand down.
			expectedResult: DiffLineExpandDown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.diffLine.GetExpandDirection()
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestNoCrashes(t *testing.T) {
	type testcase struct {
		gitdiff string
	}

	tests := []testcase{
		{
			gitdiff: "diff --git \n--- a\t\n",
		},
		{
			gitdiff: "diff --git \"0\n",
		},
	}
	for _, testcase := range tests {
		// It shouldn't crash, so don't care about the output.
		ParsePatch(db.DefaultContext, setting.Git.MaxGitDiffLines, setting.Git.MaxGitDiffLineCharacters, strings.NewReader(testcase.gitdiff))
	}
}

func TestEnrichWithReview(t *testing.T) {
	tests := []struct {
		name        string
		reviewState *pull_model.ReviewState
		diffFiles   []*DiffFileMetadata
		want        []*DiffFileMetadata
	}{
		{
			name:        "nil review state returns files unchanged",
			reviewState: nil,
			diffFiles: []*DiffFileMetadata{
				{Name: "a.go"},
				{Name: "b.go"},
			},
			want: []*DiffFileMetadata{
				{Name: "a.go"},
				{Name: "b.go"},
			},
		},
		{
			name: "nil UpdatedFiles returns files unchanged",
			reviewState: &pull_model.ReviewState{
				UpdatedFiles: nil,
			},
			diffFiles: []*DiffFileMetadata{
				{Name: "a.go"},
			},
			want: []*DiffFileMetadata{
				{Name: "a.go"},
			},
		},
		{
			name: "marks viewed files",
			reviewState: &pull_model.ReviewState{
				UpdatedFiles: map[string]pull_model.ViewedState{
					"a.go": pull_model.Viewed,
					"b.go": pull_model.Unviewed,
				},
			},
			diffFiles: []*DiffFileMetadata{
				{Name: "a.go"},
				{Name: "b.go"},
				{Name: "c.go"},
			},
			want: []*DiffFileMetadata{
				{Name: "a.go", IsViewed: true},
				{Name: "b.go", IsViewed: false},
				{Name: "c.go", IsViewed: false},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EnrichWithReview(tt.reviewState, tt.diffFiles...)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetFileNames(t *testing.T) {
	files := []*DiffFileMetadata{
		{Name: "a.go"},
		{Name: "b/c.go"},
		{Name: "d.go"},
		{Name: "README.md", OldName: optional.Some("README")},
	}

	got := GetFileNames(files)
	want := []string{"a.go", "b/c.go", "d.go", "README.md", "README"}
	assert.Equal(t, want, got)
}

func TestGetDiffMetadata(t *testing.T) {
	tests := []struct {
		name               string
		review             *pull_model.ReviewState
		paginator          *paginator.Paginator
		totalNumberOfFiles int
		want               *DiffMetadata
	}{
		{
			name:               "no review",
			review:             nil,
			paginator:          paginator.New(100, 20, 1, 5),
			totalNumberOfFiles: 100,
			want: &DiffMetadata{
				HasNext:            true,
				CurrentPage:        1,
				TotalNumberOfFiles: 100,
			},
		},
		{
			name: "review with viewed files",
			review: &pull_model.ReviewState{
				UpdatedFiles: map[string]pull_model.ViewedState{
					"a.go": pull_model.Viewed,
					"b.go": pull_model.Viewed,
					"c.go": pull_model.Unviewed,
				},
			},
			paginator:          paginator.New(50, 20, 2, 5),
			totalNumberOfFiles: 50,
			want: &DiffMetadata{
				HasNext:             true,
				CurrentPage:         2,
				TotalNumberOfFiles:  50,
				NumberOfViewedFiles: 2,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetDiffMetadata(tt.review, tt.paginator, tt.totalNumberOfFiles)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetDiffFilePage(t *testing.T) {
	metadata := []*DiffFileMetadata{
		{Name: "a.go"},
		{Name: "b.go"},
		{Name: "c.go"},
		{Name: "d.go"},
		{Name: "e.go"},
	}

	tests := []struct {
		name       string
		page       int
		pageSize   int
		totalFiles int
		want       []string
	}{
		{
			name:       "first page",
			page:       1,
			pageSize:   2,
			totalFiles: len(metadata),
			want:       []string{"a.go", "b.go"},
		},
		{
			name:       "second page",
			page:       2,
			pageSize:   2,
			totalFiles: len(metadata),
			want:       []string{"c.go", "d.go"},
		},
		{
			name:       "last partial page",
			page:       3,
			pageSize:   2,
			totalFiles: len(metadata),
			want:       []string{"e.go"},
		},
		{
			name:       "page beyond end",
			page:       10,
			pageSize:   2,
			totalFiles: len(metadata),
			want:       []string{},
		},
		{
			name:       "negative page normalizes to 1",
			page:       -1,
			pageSize:   2,
			totalFiles: len(metadata),
			want:       []string{"a.go", "b.go"},
		},
		{
			name:       "zero pageSize normalizes to 1",
			page:       1,
			pageSize:   0,
			totalFiles: len(metadata),
			want:       []string{"a.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetDiffFilePage(metadata, tt.page, tt.pageSize, tt.totalFiles)
			assert.Equal(t, tt.want, got)
		})
	}
}
