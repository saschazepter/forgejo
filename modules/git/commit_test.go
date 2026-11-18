// Copyright 2017 The Gitea Authors. All rights reserved.
// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommitsCount(t *testing.T) {
	bareRepo1Path := filepath.Join(testReposDir, "repo1_bare")

	commitsCount, err := CommitsCount(DefaultContext,
		CommitsCountOptions{
			RepoPath: bareRepo1Path,
			Revision: []string{"8006ff9adbf0cb94da7dad9e537e53817f9fa5c0"},
		})

	require.NoError(t, err)
	assert.Equal(t, int64(3), commitsCount)
}

func TestCommitsCountWithoutBase(t *testing.T) {
	bareRepo1Path := filepath.Join(testReposDir, "repo1_bare")

	commitsCount, err := CommitsCount(DefaultContext,
		CommitsCountOptions{
			RepoPath: bareRepo1Path,
			Not:      "master",
			Revision: []string{"branch1"},
		})

	require.NoError(t, err)
	assert.Equal(t, int64(2), commitsCount)
}

func TestGetFullCommitID(t *testing.T) {
	bareRepo1Path := filepath.Join(testReposDir, "repo1_bare")

	id, err := GetFullCommitID(DefaultContext, bareRepo1Path, "8006ff9a")
	require.NoError(t, err)
	assert.Equal(t, "8006ff9adbf0cb94da7dad9e537e53817f9fa5c0", id)
}

func TestGetFullCommitIDError(t *testing.T) {
	bareRepo1Path := filepath.Join(testReposDir, "repo1_bare")

	id, err := GetFullCommitID(DefaultContext, bareRepo1Path, "unknown")
	assert.Empty(t, id)
	if assert.Error(t, err) {
		assert.EqualError(t, err, "object does not exist [id: unknown, rel_path: ]")
	}
}

func TestCommitFromReader(t *testing.T) {
	commitString := `tree f1a6cb52b2d16773290cefe49ad0684b50a4f930
parent 37991dec2c8e592043f47155ce4808d4580f9123
author silverwind <me@silverwind.io> 1563741793 +0200
committer silverwind <me@silverwind.io> 1563741793 +0200
gpgsig -----BEGIN PGP SIGNATURE-----
` + " " + `
 iQIzBAABCAAdFiEEWPb2jX6FS2mqyJRQLmK0HJOGlEMFAl00zmEACgkQLmK0HJOG
 lEMDFBAAhQKKqLD1VICygJMEB8t1gBmNLgvziOLfpX4KPWdPtBk3v/QJ7OrfMrVK
 xlC4ZZyx6yMm1Q7GzmuWykmZQJ9HMaHJ49KAbh5MMjjV/+OoQw9coIdo8nagRUld
 vX8QHzNZ6Agx77xHuDJZgdHKpQK3TrMDsxzoYYMvlqoLJIDXE1Sp7KYNy12nhdRg
 R6NXNmW8oMZuxglkmUwayMiPS+N4zNYqv0CXYzlEqCOgq9MJUcAMHt+KpiST+sm6
 FWkJ9D+biNPyQ9QKf1AE4BdZia4lHfPYU/C/DEL/a5xQuuop/zMQZoGaIA4p2zGQ
 /maqYxEIM/yRBQpT1jlODKPJrMEgx7SgY2hRU47YZ4fj6350fb6fNBtiiMAfJbjL
 S3Gh85E9fm3hJaNSPKAaJFYL1Ya2svuWfgHj677C56UcmYis7fhiiy1aJuYdHnSm
 sD53z/f0J+We4VZjY+pidvA9BGZPFVdR3wd3xGs8/oH6UWaLJAMGkLG6dDb3qDLm
 1LFZwsX8sdD32i1SiWanYQYSYMyFWr0awi4xdoMtYCL7uKBYtwtPyvq3cj4IrJlb
 mfeFhT57UbE4qukTDIQ0Y0WM40UYRTakRaDY7ubhXgLgx09Cnp9XTVMsHgT6j9/i
 1pxsB104XLWjQHTjr1JtiaBQEwFh9r2OKTcpvaLcbNtYpo7CzOs=
 =FRsO
 -----END PGP SIGNATURE-----

empty commit`

	sha := &Sha1Hash{0xfe, 0xaf, 0x4b, 0xa6, 0xbc, 0x63, 0x5f, 0xec, 0x44, 0x2f, 0x46, 0xdd, 0xd4, 0x51, 0x24, 0x16, 0xec, 0x43, 0xc2, 0xc2}
	gitRepo, err := openRepositoryWithDefaultContext(filepath.Join(testReposDir, "repo1_bare"))
	require.NoError(t, err)
	assert.NotNil(t, gitRepo)
	defer gitRepo.Close()

	commitFromReader, err := CommitFromReader(gitRepo, sha, strings.NewReader(commitString))
	require.NoError(t, err)
	require.NotNil(t, commitFromReader)
	assert.EqualValues(t, sha, commitFromReader.ID)
	assert.Equal(t, `-----BEGIN PGP SIGNATURE-----

iQIzBAABCAAdFiEEWPb2jX6FS2mqyJRQLmK0HJOGlEMFAl00zmEACgkQLmK0HJOG
lEMDFBAAhQKKqLD1VICygJMEB8t1gBmNLgvziOLfpX4KPWdPtBk3v/QJ7OrfMrVK
xlC4ZZyx6yMm1Q7GzmuWykmZQJ9HMaHJ49KAbh5MMjjV/+OoQw9coIdo8nagRUld
vX8QHzNZ6Agx77xHuDJZgdHKpQK3TrMDsxzoYYMvlqoLJIDXE1Sp7KYNy12nhdRg
R6NXNmW8oMZuxglkmUwayMiPS+N4zNYqv0CXYzlEqCOgq9MJUcAMHt+KpiST+sm6
FWkJ9D+biNPyQ9QKf1AE4BdZia4lHfPYU/C/DEL/a5xQuuop/zMQZoGaIA4p2zGQ
/maqYxEIM/yRBQpT1jlODKPJrMEgx7SgY2hRU47YZ4fj6350fb6fNBtiiMAfJbjL
S3Gh85E9fm3hJaNSPKAaJFYL1Ya2svuWfgHj677C56UcmYis7fhiiy1aJuYdHnSm
sD53z/f0J+We4VZjY+pidvA9BGZPFVdR3wd3xGs8/oH6UWaLJAMGkLG6dDb3qDLm
1LFZwsX8sdD32i1SiWanYQYSYMyFWr0awi4xdoMtYCL7uKBYtwtPyvq3cj4IrJlb
mfeFhT57UbE4qukTDIQ0Y0WM40UYRTakRaDY7ubhXgLgx09Cnp9XTVMsHgT6j9/i
1pxsB104XLWjQHTjr1JtiaBQEwFh9r2OKTcpvaLcbNtYpo7CzOs=
=FRsO
-----END PGP SIGNATURE-----
`, commitFromReader.Signature.Signature)
	assert.Equal(t, `tree f1a6cb52b2d16773290cefe49ad0684b50a4f930
parent 37991dec2c8e592043f47155ce4808d4580f9123
author silverwind <me@silverwind.io> 1563741793 +0200
committer silverwind <me@silverwind.io> 1563741793 +0200

empty commit`, commitFromReader.Signature.Payload)
	assert.Equal(t, "silverwind <me@silverwind.io>", commitFromReader.Author.String())

	commitFromReader2, err := CommitFromReader(gitRepo, sha, strings.NewReader(commitString+"\n\n"))
	require.NoError(t, err)
	commitFromReader.CommitMessage += "\n\n"
	commitFromReader.Signature.Payload += "\n\n"
	assert.Equal(t, commitFromReader, commitFromReader2)
}

func TestCommitWithEncodingFromReader(t *testing.T) {
	commitString := `tree ca3fad42080dd1a6d291b75acdfc46e5b9b307e5
parent 47b24e7ab977ed31c5a39989d570847d6d0052af
author KN4CK3R <admin@oldschoolhack.me> 1711702962 +0100
committer KN4CK3R <admin@oldschoolhack.me> 1711702962 +0100
encoding ISO-8859-1
gpgsig -----BEGIN PGP SIGNATURE-----
` + " " + `
 iQGzBAABCgAdFiEE9HRrbqvYxPT8PXbefPSEkrowAa8FAmYGg7IACgkQfPSEkrow
 Aa9olwv+P0HhtCM6CRvlUmPaqswRsDPNR4i66xyXGiSxdI9V5oJL7HLiQIM7KrFR
 gizKa2COiGtugv8fE+TKqXKaJx6uJUJEjaBd8E9Af9PrAzjWj+A84lU6/PgPS8hq
 zOfZraLOEWRH4tZcS+u2yFLu3ez2Wqh1xW5LNy7xqEedMXEFD1HwSJ0+pjacNkzr
 frp6Asyt7xRI6YmgFJZJoRsS3Ktr6rtKeRL2IErSQQyorOqj6gKrglhrhfG/114j
 FKB1v4or0WZ1DE8iP2SJZ3n+/K1IuWAINh7MVdb7PndfBPEa+IL+ucNk5uzEE8Jd
 G8smGxXUeFEt2cP1dj2W8EgAxuA9sTnH9dqI5aRqy5ifDjuya7Emm8sdOUvtGdmn
 SONRzusmu5n3DgV956REL7x62h7JuqmBz/12HZkr0z0zgXkcZ04q08pSJATX5N1F
 yN+tWxTsWg+zhDk96d5Esdo9JMjcFvPv0eioo30GAERaz1hoD7zCMT4jgUFTQwgz
 jw4YcO5u
 =r3UU
 -----END PGP SIGNATURE-----

ISO-8859-1`

	sha := &Sha1Hash{0xfe, 0xaf, 0x4b, 0xa6, 0xbc, 0x63, 0x5f, 0xec, 0x44, 0x2f, 0x46, 0xdd, 0xd4, 0x51, 0x24, 0x16, 0xec, 0x43, 0xc2, 0xc2}
	gitRepo, err := openRepositoryWithDefaultContext(filepath.Join(testReposDir, "repo1_bare"))
	require.NoError(t, err)
	assert.NotNil(t, gitRepo)
	defer gitRepo.Close()

	commitFromReader, err := CommitFromReader(gitRepo, sha, strings.NewReader(commitString))
	require.NoError(t, err)
	require.NotNil(t, commitFromReader)
	assert.EqualValues(t, sha, commitFromReader.ID)
	assert.Equal(t, `-----BEGIN PGP SIGNATURE-----

iQGzBAABCgAdFiEE9HRrbqvYxPT8PXbefPSEkrowAa8FAmYGg7IACgkQfPSEkrow
Aa9olwv+P0HhtCM6CRvlUmPaqswRsDPNR4i66xyXGiSxdI9V5oJL7HLiQIM7KrFR
gizKa2COiGtugv8fE+TKqXKaJx6uJUJEjaBd8E9Af9PrAzjWj+A84lU6/PgPS8hq
zOfZraLOEWRH4tZcS+u2yFLu3ez2Wqh1xW5LNy7xqEedMXEFD1HwSJ0+pjacNkzr
frp6Asyt7xRI6YmgFJZJoRsS3Ktr6rtKeRL2IErSQQyorOqj6gKrglhrhfG/114j
FKB1v4or0WZ1DE8iP2SJZ3n+/K1IuWAINh7MVdb7PndfBPEa+IL+ucNk5uzEE8Jd
G8smGxXUeFEt2cP1dj2W8EgAxuA9sTnH9dqI5aRqy5ifDjuya7Emm8sdOUvtGdmn
SONRzusmu5n3DgV956REL7x62h7JuqmBz/12HZkr0z0zgXkcZ04q08pSJATX5N1F
yN+tWxTsWg+zhDk96d5Esdo9JMjcFvPv0eioo30GAERaz1hoD7zCMT4jgUFTQwgz
jw4YcO5u
=r3UU
-----END PGP SIGNATURE-----
`, commitFromReader.Signature.Signature)
	assert.Equal(t, `tree ca3fad42080dd1a6d291b75acdfc46e5b9b307e5
parent 47b24e7ab977ed31c5a39989d570847d6d0052af
author KN4CK3R <admin@oldschoolhack.me> 1711702962 +0100
committer KN4CK3R <admin@oldschoolhack.me> 1711702962 +0100
encoding ISO-8859-1

ISO-8859-1`, commitFromReader.Signature.Payload)
	assert.Equal(t, "KN4CK3R <admin@oldschoolhack.me>", commitFromReader.Author.String())

	commitFromReader2, err := CommitFromReader(gitRepo, sha, strings.NewReader(commitString+"\n\n"))
	require.NoError(t, err)
	commitFromReader.CommitMessage += "\n\n"
	commitFromReader.Signature.Payload += "\n\n"
	assert.Equal(t, commitFromReader, commitFromReader2)
}

func TestCommitWithChangeIDFromReader(t *testing.T) {
	commitString := `tree efd3cbedfc360ce9f60e5f92d51221be5afb4bf0
author Nicole Patricia Mazzuca <nicole@strega-nil.co> 1746965490 +0200
committer Nicole Patricia Mazzuca <nicole@strega-nil.co> 1746965630 +0200
change-id psyxzzozmuvvwrwnpqpvmtwntqsnwzpu
gpgsig -----BEGIN PGP SIGNATURE-----
` + " " + `
 iHUEABYKAB0WIQT/T2ISZ7rMF2EbKVdDm0tNAL/2MgUCaCCUfgAKCRBDm0tNAL/2
 Mmu/AQC0OWWHsSlfDKIArdALjDLgd00OQVbP+6iYVE9e+rorFwEA5qYVAXD60EHB
 +7UVcfwZ2jKajkk3q01VyT/CDo3LLQE=
 =yq2Y
 -----END PGP SIGNATURE-----

views: first commit!

includes a basic month view, and prints a nice view of an imaginary
January where the year starts on a Monday :)`

	sha := &Sha1Hash{0xe6, 0x69, 0x11, 0x91, 0x44, 0x14, 0xb0, 0xda, 0xa8, 0x5d, 0x4a, 0x42, 0x8c, 0x8d, 0x60, 0x7b, 0x9b, 0x24, 0x9a, 0x2c}
	gitRepo, err := openRepositoryWithDefaultContext(filepath.Join(testReposDir, "repo1_bare"))
	require.NoError(t, err)
	assert.NotNil(t, gitRepo)
	defer gitRepo.Close()

	commitFromReader, err := CommitFromReader(gitRepo, sha, strings.NewReader(commitString))
	require.NoError(t, err)
	require.NotNil(t, commitFromReader)
	assert.EqualValues(t, sha, commitFromReader.ID)
	assert.Equal(t, `-----BEGIN PGP SIGNATURE-----

iHUEABYKAB0WIQT/T2ISZ7rMF2EbKVdDm0tNAL/2MgUCaCCUfgAKCRBDm0tNAL/2
Mmu/AQC0OWWHsSlfDKIArdALjDLgd00OQVbP+6iYVE9e+rorFwEA5qYVAXD60EHB
+7UVcfwZ2jKajkk3q01VyT/CDo3LLQE=
=yq2Y
-----END PGP SIGNATURE-----
`, commitFromReader.Signature.Signature)
	assert.Equal(t, `tree efd3cbedfc360ce9f60e5f92d51221be5afb4bf0
author Nicole Patricia Mazzuca <nicole@strega-nil.co> 1746965490 +0200
committer Nicole Patricia Mazzuca <nicole@strega-nil.co> 1746965630 +0200
change-id psyxzzozmuvvwrwnpqpvmtwntqsnwzpu

views: first commit!

includes a basic month view, and prints a nice view of an imaginary
January where the year starts on a Monday :)`, commitFromReader.Signature.Payload)
	assert.Equal(t, "Nicole Patricia Mazzuca <nicole@strega-nil.co>", commitFromReader.Author.String())
}

func TestGitbutlerCustomHeaderFields(t *testing.T) {
	// example from: https://github.com/go-gitea/gitea/issues/34529#issuecomment-2908481092
	commitString := `tree a29321bf9e3ec433ed9e47b1cbbac6906c71fc60
parent c0d83043ade7fa3ca10659608799477e9daa670b
author Sebastian Thiel <sebastian.thiel@icloud.com> 1747920681 +0200
committer Sebastian Thiel <sebastian.thiel@icloud.com> 1748010747 +0200
gitbutler-headers-version 2
gitbutler-change-id 1063f7ea-d841-43b3-903a-01747681c40d
gpgsig -----BEGIN PGP SIGNATURE-----
 
 iQIzBAABCAAdFiEE6vnM/NCHZAjyl8YKnLXueJXoJosFAmgwhvsACgkQnLXueJXo
 JovXUxAAq0WKJILCUAxyhwh5tRdxJTB2NjiCLf+ggLfjyrWPtMWPi/YUt7iGPB2H
 Wbv9U7l5t+54fPX8TQtBKZ79YaDMfYdjlfDSijmPruf8/MXB4G0rAaIajtCr0usZ
 kJDOgmmYS7bVMybDe6guwFZappiuSS2dCEYgeJun+q7Y6IYsfvdAluJmGubQIkPT
 rrEffqoQz3URmDYnAKW3sTRUVwCkYIJDxpl/W0Rvc0jmELdkHu7JYX7XvZBYSUDq
 FWgzCPjyErtkKk8AqoeWtTCpL+9ozzNIXNRKjGCOL2LG4H/uuNFdM46HB+KW/7+B
 wMGcpZk8T/zN9Cf348M+y+o09QX1OWavDS6LgvWJaDtG/swgxV96KKR5lEtdd1IU
 JHuXfPUfGp4r378FIrbPK+Thu5bn9Yq8qGvdZOpTqDxHPU9/o9wLpJghcWJZ5O3X
 MpK4HdN+bME2zgBd08QsOjANogbJIz9MVaMGRFlCO5iOiz2DxG+v2KkO8IRwGXaO
 OKKQ7BD04fS2wFma862BaTtB9M9f9UTWV4e2mgRpSDJWTQKrj+HkJ63gAFQYFnfp
 ppgqZLkmzH1Ta2U56JSMMfOoKVFgjuxRx1d+tzdC+TpQyo06NI1KkNMepK1rhFBW
 p8hej6n/7Bl9LL/W+DKsNqW9jQbTYu66JqKs3Kg7xga6w/ss0iw=
 =VG6I
 -----END PGP SIGNATURE-----

asdf
asdf
asdf
`

	sha := &Sha1Hash{0xe6, 0x69, 0x11, 0x91, 0x44, 0x14, 0xb0, 0xda, 0xa8, 0x5d, 0x4a, 0x42, 0x8c, 0x8d, 0x60, 0x7b, 0x9b, 0x24, 0x9a, 0x2c}
	gitRepo, err := openRepositoryWithDefaultContext(filepath.Join(testReposDir, "repo1_bare"))
	require.NoError(t, err)
	assert.NotNil(t, gitRepo)
	defer gitRepo.Close()

	commitFromReader, err := CommitFromReader(gitRepo, sha, strings.NewReader(commitString))
	require.NoError(t, err)
	require.NotNil(t, commitFromReader)
	assert.EqualValues(t, sha, commitFromReader.ID)
	assert.Equal(t, `-----BEGIN PGP SIGNATURE-----

iQIzBAABCAAdFiEE6vnM/NCHZAjyl8YKnLXueJXoJosFAmgwhvsACgkQnLXueJXo
JovXUxAAq0WKJILCUAxyhwh5tRdxJTB2NjiCLf+ggLfjyrWPtMWPi/YUt7iGPB2H
Wbv9U7l5t+54fPX8TQtBKZ79YaDMfYdjlfDSijmPruf8/MXB4G0rAaIajtCr0usZ
kJDOgmmYS7bVMybDe6guwFZappiuSS2dCEYgeJun+q7Y6IYsfvdAluJmGubQIkPT
rrEffqoQz3URmDYnAKW3sTRUVwCkYIJDxpl/W0Rvc0jmELdkHu7JYX7XvZBYSUDq
FWgzCPjyErtkKk8AqoeWtTCpL+9ozzNIXNRKjGCOL2LG4H/uuNFdM46HB+KW/7+B
wMGcpZk8T/zN9Cf348M+y+o09QX1OWavDS6LgvWJaDtG/swgxV96KKR5lEtdd1IU
JHuXfPUfGp4r378FIrbPK+Thu5bn9Yq8qGvdZOpTqDxHPU9/o9wLpJghcWJZ5O3X
MpK4HdN+bME2zgBd08QsOjANogbJIz9MVaMGRFlCO5iOiz2DxG+v2KkO8IRwGXaO
OKKQ7BD04fS2wFma862BaTtB9M9f9UTWV4e2mgRpSDJWTQKrj+HkJ63gAFQYFnfp
ppgqZLkmzH1Ta2U56JSMMfOoKVFgjuxRx1d+tzdC+TpQyo06NI1KkNMepK1rhFBW
p8hej6n/7Bl9LL/W+DKsNqW9jQbTYu66JqKs3Kg7xga6w/ss0iw=
=VG6I
-----END PGP SIGNATURE-----
`, commitFromReader.Signature.Signature)
	assert.Equal(t, `tree a29321bf9e3ec433ed9e47b1cbbac6906c71fc60
parent c0d83043ade7fa3ca10659608799477e9daa670b
author Sebastian Thiel <sebastian.thiel@icloud.com> 1747920681 +0200
committer Sebastian Thiel <sebastian.thiel@icloud.com> 1748010747 +0200
gitbutler-headers-version 2
gitbutler-change-id 1063f7ea-d841-43b3-903a-01747681c40d

asdf
asdf
asdf
`, commitFromReader.Signature.Payload)
	assert.Equal(t, "Sebastian Thiel <sebastian.thiel@icloud.com>", commitFromReader.Author.String())
}

func TestHasPreviousCommit(t *testing.T) {
	bareRepo1Path := filepath.Join(testReposDir, "repo1_bare")

	repo, err := openRepositoryWithDefaultContext(bareRepo1Path)
	require.NoError(t, err)
	defer repo.Close()

	commit, err := repo.GetCommit("8006ff9adbf0cb94da7dad9e537e53817f9fa5c0")
	require.NoError(t, err)

	parentSHA := MustIDFromString("8d92fc957a4d7cfd98bc375f0b7bb189a0d6c9f2")
	notParentSHA := MustIDFromString("2839944139e0de9737a044f78b0e4b40d989a9e3")

	haz, err := commit.HasPreviousCommit(parentSHA)
	require.NoError(t, err)
	assert.True(t, haz)

	hazNot, err := commit.HasPreviousCommit(notParentSHA)
	require.NoError(t, err)
	assert.False(t, hazNot)

	selfNot, err := commit.HasPreviousCommit(commit.ID)
	require.NoError(t, err)
	assert.False(t, selfNot)
}

func TestParseCommitFileStatus(t *testing.T) {
	type testcase struct {
		output   string
		added    []string
		removed  []string
		modified []string
	}

	kases := []testcase{
		{
			// Merge commit
			output: "MM\x00options/locale/locale_en-US.ini\x00",
			modified: []string{
				"options/locale/locale_en-US.ini",
			},
			added:   []string{},
			removed: []string{},
		},
		{
			// Spaces commit
			output: "D\x00b\x00D\x00b b/b\x00A\x00b b/b b/b b/b\x00A\x00b b/b b/b b/b b/b\x00",
			removed: []string{
				"b",
				"b b/b",
			},
			modified: []string{},
			added: []string{
				"b b/b b/b b/b",
				"b b/b b/b b/b b/b",
			},
		},
		{
			// larger commit
			output: "M\x00go.mod\x00M\x00go.sum\x00M\x00modules/ssh/ssh.go\x00M\x00vendor/github.com/gliderlabs/ssh/circle.yml\x00M\x00vendor/github.com/gliderlabs/ssh/context.go\x00A\x00vendor/github.com/gliderlabs/ssh/go.mod\x00A\x00vendor/github.com/gliderlabs/ssh/go.sum\x00M\x00vendor/github.com/gliderlabs/ssh/server.go\x00M\x00vendor/github.com/gliderlabs/ssh/session.go\x00M\x00vendor/github.com/gliderlabs/ssh/ssh.go\x00M\x00vendor/golang.org/x/sys/unix/mkerrors.sh\x00M\x00vendor/golang.org/x/sys/unix/syscall_darwin.go\x00M\x00vendor/golang.org/x/sys/unix/zerrors_darwin_amd64.go\x00M\x00vendor/golang.org/x/sys/unix/zerrors_darwin_arm64.go\x00M\x00vendor/golang.org/x/sys/unix/zerrors_freebsd_386.go\x00M\x00vendor/golang.org/x/sys/unix/zerrors_freebsd_amd64.go\x00M\x00vendor/golang.org/x/sys/unix/zerrors_freebsd_arm.go\x00M\x00vendor/golang.org/x/sys/unix/zerrors_freebsd_arm64.go\x00M\x00vendor/golang.org/x/sys/unix/zerrors_linux.go\x00M\x00vendor/golang.org/x/sys/unix/ztypes_darwin_amd64.go\x00M\x00vendor/golang.org/x/sys/unix/ztypes_darwin_arm64.go\x00M\x00vendor/golang.org/x/sys/unix/ztypes_dragonfly_amd64.go\x00M\x00vendor/golang.org/x/sys/unix/ztypes_freebsd_386.go\x00M\x00vendor/golang.org/x/sys/unix/ztypes_freebsd_amd64.go\x00M\x00vendor/golang.org/x/sys/unix/ztypes_freebsd_arm.go\x00M\x00vendor/golang.org/x/sys/unix/ztypes_freebsd_arm64.go\x00M\x00vendor/golang.org/x/sys/unix/ztypes_netbsd_386.go\x00M\x00vendor/golang.org/x/sys/unix/ztypes_netbsd_amd64.go\x00M\x00vendor/golang.org/x/sys/unix/ztypes_netbsd_arm.go\x00M\x00vendor/golang.org/x/sys/unix/ztypes_netbsd_arm64.go\x00M\x00vendor/modules.txt\x00",
			modified: []string{
				"go.mod",
				"go.sum",
				"modules/ssh/ssh.go",
				"vendor/github.com/gliderlabs/ssh/circle.yml",
				"vendor/github.com/gliderlabs/ssh/context.go",
				"vendor/github.com/gliderlabs/ssh/server.go",
				"vendor/github.com/gliderlabs/ssh/session.go",
				"vendor/github.com/gliderlabs/ssh/ssh.go",
				"vendor/golang.org/x/sys/unix/mkerrors.sh",
				"vendor/golang.org/x/sys/unix/syscall_darwin.go",
				"vendor/golang.org/x/sys/unix/zerrors_darwin_amd64.go",
				"vendor/golang.org/x/sys/unix/zerrors_darwin_arm64.go",
				"vendor/golang.org/x/sys/unix/zerrors_freebsd_386.go",
				"vendor/golang.org/x/sys/unix/zerrors_freebsd_amd64.go",
				"vendor/golang.org/x/sys/unix/zerrors_freebsd_arm.go",
				"vendor/golang.org/x/sys/unix/zerrors_freebsd_arm64.go",
				"vendor/golang.org/x/sys/unix/zerrors_linux.go",
				"vendor/golang.org/x/sys/unix/ztypes_darwin_amd64.go",
				"vendor/golang.org/x/sys/unix/ztypes_darwin_arm64.go",
				"vendor/golang.org/x/sys/unix/ztypes_dragonfly_amd64.go",
				"vendor/golang.org/x/sys/unix/ztypes_freebsd_386.go",
				"vendor/golang.org/x/sys/unix/ztypes_freebsd_amd64.go",
				"vendor/golang.org/x/sys/unix/ztypes_freebsd_arm.go",
				"vendor/golang.org/x/sys/unix/ztypes_freebsd_arm64.go",
				"vendor/golang.org/x/sys/unix/ztypes_netbsd_386.go",
				"vendor/golang.org/x/sys/unix/ztypes_netbsd_amd64.go",
				"vendor/golang.org/x/sys/unix/ztypes_netbsd_arm.go",
				"vendor/golang.org/x/sys/unix/ztypes_netbsd_arm64.go",
				"vendor/modules.txt",
			},
			added: []string{
				"vendor/github.com/gliderlabs/ssh/go.mod",
				"vendor/github.com/gliderlabs/ssh/go.sum",
			},
			removed: []string{},
		},
		{
			// git 1.7.2 adds an unnecessary \x00 on merge commit
			output: "\x00MM\x00options/locale/locale_en-US.ini\x00",
			modified: []string{
				"options/locale/locale_en-US.ini",
			},
			added:   []string{},
			removed: []string{},
		},
		{
			// git 1.7.2 adds an unnecessary \n on normal commit
			output: "\nD\x00b\x00D\x00b b/b\x00A\x00b b/b b/b b/b\x00A\x00b b/b b/b b/b b/b\x00",
			removed: []string{
				"b",
				"b b/b",
			},
			modified: []string{},
			added: []string{
				"b b/b b/b b/b",
				"b b/b b/b b/b b/b",
			},
		},
	}

	for _, kase := range kases {
		fileStatus := NewCommitFileStatus()
		parseCommitFileStatus(fileStatus, strings.NewReader(kase.output))

		assert.Equal(t, kase.added, fileStatus.Added)
		assert.Equal(t, kase.removed, fileStatus.Removed)
		assert.Equal(t, kase.modified, fileStatus.Modified)
	}
}

func TestGetCommitFileStatusMerges(t *testing.T) {
	bareRepo1Path := filepath.Join(testReposDir, "repo6_merge")

	commitFileStatus, err := GetCommitFileStatus(DefaultContext, bareRepo1Path, "022f4ce6214973e018f02bf363bf8a2e3691f699")
	require.NoError(t, err)

	expected := CommitFileStatus{
		[]string{
			"add_file.txt",
		},
		[]string{
			"to_remove.txt",
		},
		[]string{
			"to_modify.txt",
		},
	}

	assert.Equal(t, expected.Added, commitFileStatus.Added)
	assert.Equal(t, expected.Removed, commitFileStatus.Removed)
	assert.Equal(t, expected.Modified, commitFileStatus.Modified)
}

func TestParseCommitRenames(t *testing.T) {
	testcases := []struct {
		output  string
		renames [][2]string
	}{
		{
			output:  "R090\x00renamed.txt\x00history.txt\x00",
			renames: [][2]string{{"renamed.txt", "history.txt"}},
		},
		{
			output:  "R090\x00renamed.txt\x00history.txt\x00R000\x00corruptedstdouthere",
			renames: [][2]string{{"renamed.txt", "history.txt"}},
		},
		{
			output:  "R100\x00renamed.txt\x00history.txt\x00R001\x00readme.md\x00README.md\x00",
			renames: [][2]string{{"renamed.txt", "history.txt"}, {"readme.md", "README.md"}},
		},
	}

	for _, testcase := range testcases {
		renames := [][2]string{}
		parseCommitRenames(&renames, strings.NewReader(testcase.output))

		assert.Equal(t, testcase.renames, renames)
	}
}

func TestGetAllBranches(t *testing.T) {
	bareRepo1Path := filepath.Join(testReposDir, "repo1_bare")

	bareRepo1, err := openRepositoryWithDefaultContext(bareRepo1Path)
	require.NoError(t, err)

	commit, err := bareRepo1.GetCommit("95bb4d39648ee7e325106df01a621c530863a653")
	require.NoError(t, err)

	branches, err := commit.GetAllBranches()
	require.NoError(t, err)

	slices.Sort(branches)

	assert.Equal(t, []string{"branch1", "branch2", "master"}, branches)
}

func TestCommitFromReaderMisplacedHeaders(t *testing.T) {
	commitString := `tree f1a6cb52b2d16773290cefe49ad0684b50a4f930
parent 37991dec2c8e592043f47155ce4808d4580f9123
parent 37991dec2c8e592043f47155ce4808d4580f9124
author silverwind <me@silverwind.io> 1563741793 +0200
committer silverwind <me@silverwind.io> 1563741793 +0200
change-id psyxzzozmuvvwrwnpqpvmtwntqsnwzpu
tree 039f4a05b4860da94efec09237761d2b25bc6a1f
parent 37991dec2c8e592043f47155ce4808d4580f9125
author fake-author <me@silverwind.io> 1563741793 +0200
committer fake-committer <me@silverwind.io> 1563741793 +0200

empty commit`

	sha := &Sha1Hash{0xfe, 0xaf, 0x4b, 0xa6, 0xbc, 0x63, 0x5f, 0xec, 0x44, 0x2f, 0x46, 0xdd, 0xd4, 0x51, 0x24, 0x16, 0xec, 0x43, 0xc2, 0xc2}
	gitRepo, err := openRepositoryWithDefaultContext(filepath.Join(testReposDir, "repo1_bare"))
	require.NoError(t, err)
	assert.NotNil(t, gitRepo)
	defer gitRepo.Close()

	commitFromReader, err := CommitFromReader(gitRepo, sha, strings.NewReader(commitString))
	require.NoError(t, err)
	require.NotNil(t, commitFromReader)
	assert.EqualValues(t, sha, commitFromReader.ID)
	assert.Equal(t, "f1a6cb52b2d16773290cefe49ad0684b50a4f930", commitFromReader.Tree.ID.String())
	assert.Len(t, commitFromReader.Parents, 2) // ...123, ...124, but ...125 should not be included
	assert.Equal(t, "37991dec2c8e592043f47155ce4808d4580f9123", commitFromReader.Parents[0].String())
	assert.Equal(t, "37991dec2c8e592043f47155ce4808d4580f9124", commitFromReader.Parents[1].String())
	assert.Equal(t, "silverwind", commitFromReader.Author.Name)
	assert.Equal(t, "silverwind", commitFromReader.Committer.Name)
}
