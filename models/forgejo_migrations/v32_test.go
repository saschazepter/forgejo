// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package forgejo_migrations //nolint:revive

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"strings"
	"testing"

	migration_tests "forgejo.org/models/migrations/test"
	"forgejo.org/models/packages"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type readSeekCloser struct {
	*bytes.Reader
}

func (rsc readSeekCloser) Close() error {
	// No resources to close, so we simply provide a no-op implementation.
	return nil
}

func StringToReadSeekCloser(s string) io.ReadSeekCloser {
	return readSeekCloser{Reader: bytes.NewReader([]byte(s))}
}

func Test_ChangeMavenArtifactConcatenation(t *testing.T) {
	getPackage = func(ctx context.Context, pf *packages.PackageFile) (io.ReadSeekCloser, *url.URL, *packages.PackageFile, error) {
		var data string

		switch pf.BlobID {
		case 1:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>com.example</groupId><artifactId>parent-project</artifactId><version>1.0-SNAPSHOT</version></project>`
		case 3:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging></packaging><parent><groupId>com.example</groupId><artifactId>parent-project</artifactId></parent><groupId></groupId><artifactId>sub-module</artifactId><version>1.0-SNAPSHOT</version></project>`
		case 6:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>com.example</groupId><artifactId>parent-project</artifactId><version>7.0.0</version></project>`
		case 7:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging></packaging><parent><groupId>com.example</groupId><artifactId>parent-project</artifactId></parent><groupId></groupId><artifactId>sub-module</artifactId><version>7.0.0</version></project>`
		case 9:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>com.example</groupId><artifactId>parent-project</artifactId><version>7.0.0</version></project>`
		case 11:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>foo-</groupId><artifactId>bar</artifactId><version>1.0-SNAPSHOT</version></project>`
		case 13:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>foo-</groupId><artifactId>bar</artifactId><version>7.0.0</version></project>`
		case 14:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>foo</groupId><artifactId>-bar</artifactId><version>1.0-SNAPSHOT</version></project>`
		case 16:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>foo</groupId><artifactId>-bar</artifactId><version>7.0.0</version></project>`
		case 20:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>com.example</groupId><artifactId>parent-project</artifactId><version>8.0.0</version></project>`
		case 21:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging></packaging><parent><groupId>com.example</groupId><artifactId>parent-project</artifactId></parent><groupId></groupId><artifactId>sub-module</artifactId><version>8.0.0</version></project>`
		case 23:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>com.example</groupId><artifactId>parent-project</artifactId><version>8.0.0</version></project>`
		case 26:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>foo-</groupId><artifactId>bar</artifactId><version>8.0.0</version></project>`
		case 28:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>foo</groupId><artifactId>-bar</artifactId><version>8.0.0</version></project>`
		case 32:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>com.example</groupId><artifactId>parent-project</artifactId><version>9.0.0</version></project>`
		case 33:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging></packaging><parent><groupId>com.example</groupId><artifactId>parent-project</artifactId></parent><groupId></groupId><artifactId>sub-module</artifactId><version>9.0.0</version></project>`
		case 35:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>com.example</groupId><artifactId>parent-project</artifactId><version>9.0.0</version></project>`
		case 38:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>foo-</groupId><artifactId>bar</artifactId><version>9.0.0</version></project>`
		case 40:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>foo</groupId><artifactId>-bar</artifactId><version>9.0.0</version></project>`
		case 44:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>com.example</groupId><artifactId>parent-project</artifactId><version>10.0.0</version></project>`
		case 45:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging></packaging><parent><groupId>com.example</groupId><artifactId>parent-project</artifactId></parent><groupId></groupId><artifactId>sub-module</artifactId><version>10.0.0</version></project>`
		case 47:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>com.example</groupId><artifactId>parent-project</artifactId><version>10.0.0</version></project>`
		case 50:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>foo-</groupId><artifactId>bar</artifactId><version>10.0.0</version></project>`
		case 52:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>foo</groupId><artifactId>-bar</artifactId><version>10.0.0</version></project>`
		case 56:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>com.example</groupId><artifactId>parent-project</artifactId><version>11.0.0</version></project>`
		case 57:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging></packaging><parent><groupId>com.example</groupId><artifactId>parent-project</artifactId></parent><groupId></groupId><artifactId>sub-module</artifactId><version>11.0.0</version></project>`
		case 59:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>com.example</groupId><artifactId>parent-project</artifactId><version>11.0.0</version></project>`
		case 62:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>foo-</groupId><artifactId>bar</artifactId><version>11.0.0</version></project>`
		case 64:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>foo</groupId><artifactId>-bar</artifactId><version>11.0.0</version></project>`
		case 66:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging></packaging><parent><groupId>com.broken</groupId><artifactId>br-parent</artifactId></parent><groupId></groupId><artifactId>br-rest-webmvc</artifactId><version></version></project>`
		case 68:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging></packaging><parent><groupId>com.broken</groupId><artifactId>br-parent</artifactId></parent><groupId></groupId><artifactId>br-openapi-base</artifactId><version></version></project>`
		case 72:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><parent><groupId>de.loosetie</groupId><artifactId>lt-parent-kotlin</artifactId></parent><groupId>com.broken</groupId><artifactId>br-root</artifactId><version>1.2.4</version></project>`
		case 74:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging></packaging><parent><groupId>com.broken</groupId><artifactId>br-parent</artifactId></parent><groupId></groupId><artifactId>br-repo-jooq</artifactId><version></version></project>`
		case 76:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging></packaging><parent><groupId>com.broken</groupId><artifactId>br-parent</artifactId></parent><groupId></groupId><artifactId>br-repo-in-memory</artifactId><version></version></project>`
		case 78:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><parent><groupId>com.broken</groupId><artifactId>br-root</artifactId></parent><groupId></groupId><artifactId>br-parent</artifactId><version></version></project>`
		case 79:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>group</groupId><artifactId>bar-art</artifactId><version>11.0.0</version></project>`
		case 80:
			data = `<project><modelVersion>4.0.0</modelVersion><packaging>pom</packaging><groupId>group-bar</groupId><artifactId>art</artifactId><version>11.0.0</version></project>`
		case 55:
			data = `<?xml version="1.0" encoding="UTF-8"?><metadata modelVersion="1.1.0"><groupId>com.example</groupId><artifactId>sub-module</artifactId><version>1.0-SNAPSHOT</version></metadata>`
		case 53:
			data = `<?xml version="1.0" encoding="UTF-8"?><metadata modelVersion="1.1.0"><groupId>com.example</groupId><artifactId>parent-project</artifactId><version>1.0-SNAPSHOT</version></metadata>`
		case 63:
			data = `<?xml version="1.0" encoding="UTF-8"?><metadata modelVersion="1.1.0"><groupId>foo</groupId><artifactId>-bar</artifactId><version>1.0-SNAPSHOT</version></metadata>`
		default:
			t.Fatalf("Unknown package file type: %d", pf.BlobID)
		}

		return StringToReadSeekCloser(data), nil, nil, nil
	}

	x, deferable := migration_tests.PrepareTestEnv(t, 0, new(packages.Package), new(packages.PackageFile), new(packages.PackageVersion), new(packages.PackageBlob))
	defer deferable()
	if x == nil || t.Failed() {
		return
	}

	cnt, err := x.Table("package").Count()
	require.NoError(t, err)
	assert.EqualValues(t, 8, cnt)

	cnt, err = x.Table("package_file").Count()
	require.NoError(t, err)
	assert.EqualValues(t, 87, cnt)

	cnt, err = x.Table("package_version").Count()
	require.NoError(t, err)
	assert.EqualValues(t, 31, cnt)

	cnt, err = x.Table("package_blob").Count()
	require.NoError(t, err)
	assert.EqualValues(t, 80, cnt)

	require.NoError(t, ChangeMavenArtifactConcatenation(x))

	var pks []*packages.Package
	require.NoError(t, x.OrderBy("id").Find(&pks))
	validatePackages(t, pks)

	var pvs []*packages.PackageVersion
	require.NoError(t, x.OrderBy("id").Find(&pvs))
	validatePackageVersions(t, pvs)

	var pfs []*packages.PackageFile
	require.NoError(t, x.OrderBy("id").Find(&pfs))
	validatePackageFiles(t, pfs)
}

func validatePackages(t *testing.T, pbs []*packages.Package) {
	assertPackage := func(id, ownerID, repoID int64, name string) {
		pb := pbs[id-1]

		require.Equal(t, id, pb.ID)
		require.Equal(t, ownerID, pb.OwnerID)
		require.Equal(t, repoID, pb.RepoID)
		require.Equal(t, name, pb.Name)
		require.Equal(t, name, pb.LowerName)
		require.Equal(t, packages.TypeMaven, pb.Type)
	}

	require.Len(t, pbs, 10)

	assertPackage(1, 2, 0, "com.example:parent-project")
	assertPackage(2, 2, 0, "com.example:sub-module")
	assertPackage(3, 1, 0, "com.example:parent-project")
	assertPackage(4, 1, 0, "com.example:sub-module")
	assertPackage(5, 1, 0, "foo:-bar")
	assertPackage(6, 8, 54, "com.broken:br-rest-webmvc")
	// broken poms completely ignored as it is impossible to look up the correct metadata
	assertPackage(7, 8, 54, "com.broken-br-openapi-base")
	assertPackage(8, 1, 0, "group-bar:art")

	// new created entries
	assertPackage(9, 1, 0, "group:bar-art")
	assertPackage(10, 1, 0, "foo-:bar")
}

func validatePackageVersions(t *testing.T, pvs []*packages.PackageVersion) {
	require.Len(t, pvs, 38)

	assertPackageVersion := func(id, packageId, creatorId, createdUnix int64, version, metadata string) {
		pv := pvs[id-1]

		require.Equal(t, id, pv.ID)
		require.Equal(t, packageId, pv.PackageID)

		require.Equal(t, creatorId, pv.CreatorID)
		require.Equal(t, version, pv.Version)
		require.Equal(t, strings.ToLower(version), pv.LowerVersion)
		require.JSONEq(t, metadata, pv.MetadataJSON)
	}

	assertPackageVersion(1, 1, 1, 1746256357, "1.0-SNAPSHOT", `{"artifact_id":"parent-project","group_id":"com.example"}`)
	assertPackageVersion(2, 2, 1, 1746256358, "1.0-SNAPSHOT", `{"artifact_id":"sub-module","group_id":"com.example"}`)
	assertPackageVersion(3, 1, 1, 1746256360, "7.0.0", `{"artifact_id":"parent-project","group_id":"com.example"}`)
	// added groupId
	assertPackageVersion(4, 2, 1, 1746256361, "7.0.0", `{"artifact_id":"sub-module","group_id":"com.example"}`)
	assertPackageVersion(5, 3, 1, 1746256364, "7.0.0", `{"artifact_id":"parent-project","group_id":"com.example"}`)
	// added groupId
	assertPackageVersion(6, 4, 1, 1746256365, "7.0.0", `{"artifact_id":"sub-module","group_id":"com.example"}`)
	assertPackageVersion(7, 5, 1, 1746256367, "1.0-SNAPSHOT", `{"artifact_id":"-bar","group_id":"foo"}`)
	assertPackageVersion(8, 5, 1, 1746256370, "7.0.0", `{"artifact_id":"-bar","group_id":"foo"}`)
	assertPackageVersion(9, 1, 1, 1746256389, "8.0.0", `{"artifact_id":"parent-project","group_id":"com.example"}`)
	// added groupId
	assertPackageVersion(10, 2, 1, 1746256390, "8.0.0", `{"artifact_id":"sub-module","group_id":"com.example"}`)
	assertPackageVersion(11, 3, 1, 1746256393, "8.0.0", `{"artifact_id":"parent-project","group_id":"com.example"}`)
	// added groupId
	assertPackageVersion(12, 4, 1, 1746256394, "8.0.0", `{"artifact_id":"sub-module","group_id":"com.example"}`)
	assertPackageVersion(13, 5, 1, 1746256399, "8.0.0", `{"artifact_id":"-bar","group_id":"foo"}`)
	assertPackageVersion(14, 1, 1, 1746256419, "9.0.0", `{"artifact_id":"parent-project","group_id":"com.example"}`)
	// added groupId
	assertPackageVersion(15, 2, 1, 1746256420, "9.0.0", `{"artifact_id":"sub-module","group_id":"com.example"}`)
	assertPackageVersion(16, 3, 1, 1746256423, "9.0.0", `{"artifact_id":"parent-project","group_id":"com.example"}`)
	// added groupId
	assertPackageVersion(17, 4, 1, 1746256424, "9.0.0", `{"artifact_id":"sub-module","group_id":"com.example"}`)
	assertPackageVersion(18, 5, 1, 1746256429, "9.0.0", `{"artifact_id":"-bar","group_id":"foo"}`)
	assertPackageVersion(19, 1, 1, 1746256449, "10.0.0", `{"artifact_id":"parent-project","group_id":"com.example"}`)
	assertPackageVersion(20, 2, 1, 1746256450, "10.0.0", `{"artifact_id":"sub-module","group_id":"com.example"}`)
	assertPackageVersion(21, 3, 1, 1746256452, "10.0.0", `{"artifact_id":"parent-project","group_id":"com.example"}`)
	assertPackageVersion(22, 4, 1, 1746256453, "10.0.0", `{"artifact_id":"sub-module","group_id":"com.example"}`)
	assertPackageVersion(23, 5, 1, 1746256459, "10.0.0", `{"artifact_id":"-bar","group_id":"foo"}`)
	assertPackageVersion(24, 1, 1, 1746256478, "11.0.0", `{"artifact_id":"parent-project","group_id":"com.example"}`)
	assertPackageVersion(25, 2, 1, 1746256479, "11.0.0", `{"artifact_id":"sub-module","group_id":"com.example"}`)
	assertPackageVersion(26, 3, 1, 1746256482, "11.0.0", `{"artifact_id":"parent-project","group_id":"com.example"}`)
	assertPackageVersion(27, 4, 1, 1746256483, "11.0.0", `{"artifact_id":"sub-module","group_id":"com.example"}`)
	assertPackageVersion(28, 5, 1, 1746256488, "11.0.0", `{"artifact_id":"-bar","group_id":"foo"}`)
	// should be untouched at all. fixtures doesn't contain names
	assertPackageVersion(29, 7, 6, 1746256488, "1.2.4", `{"group_id":"com.broken","artifact_id":"br-root","name":"Foo"}`)
	// added group name
	assertPackageVersion(30, 6, 6, 1746256488, "1.2.4", `{"artifact_id":"br-rest-webmvc", "group_id":"com.broken"}`)
	assertPackageVersion(31, 8, 1, 1746256488, "11.0.0", `{"artifact_id":"art","group_id":"group-bar"}`)

	// new entries
	assertPackageVersion(32, 9, 1, 1746256488, "11.0.0", `{"artifact_id":"bar-art","group_id":"group"}`)
	assertPackageVersion(33, 10, 1, 1746256488, "11.0.0", `{"artifact_id":"bar","group_id":"foo-"}`)
	assertPackageVersion(34, 10, 1, 1746256488, "10.0.0", `{"artifact_id":"bar","group_id":"foo-"}`)
	assertPackageVersion(35, 10, 1, 1746256488, "9.0.0", `{"artifact_id":"bar","group_id":"foo-"}`)
	assertPackageVersion(36, 10, 1, 1746256488, "8.0.0", `{"artifact_id":"bar","group_id":"foo-"}`)
	assertPackageVersion(37, 10, 1, 1746256488, "7.0.0", `{"artifact_id":"bar","group_id":"foo-"}`)
	assertPackageVersion(38, 10, 1, 1746256488, "1.0-SNAPSHOT", `{"artifact_id":"bar","group_id":"foo-"}`)
}

func validatePackageFiles(t *testing.T, pfs []*packages.PackageFile) {
	assertPackageVersion := func(pos, id, versionId, blobId, createdUnix int64, name string, isLead bool) {
		pf := pfs[pos]

		require.Equal(t, id, pf.ID)
		require.Equal(t, versionId, pf.VersionID)
		require.Equal(t, blobId, pf.BlobID)
		require.Equal(t, name, pf.Name)
		require.Equal(t, strings.ToLower(name), pf.LowerName)
		require.Empty(t, pf.CompositeKey)
		require.Equal(t, isLead, pf.IsLead)
		require.EqualValues(t, createdUnix, pf.CreatedUnix)

		require.Empty(t, pf.CompositeKey)
	}
	assertPackageVersion(0, 1, 1, 1, 1746256357, "parent-project-1.0-20250503.071237-1.pom", true)
	assertPackageVersion(1, 3, 2, 3, 1746256358, "sub-module-1.0-20250503.071237-1.pom", true)
	assertPackageVersion(2, 4, 2, 4, 1746256358, "sub-module-1.0-20250503.071237-1.jar", false)
	assertPackageVersion(3, 6, 3, 6, 1746256360, "parent-project-7.0.0.pom", true)
	assertPackageVersion(4, 7, 4, 7, 1746256361, "sub-module-7.0.0.pom", true)
	assertPackageVersion(5, 8, 4, 8, 1746256361, "sub-module-7.0.0.jar", false)
	assertPackageVersion(6, 9, 5, 9, 1746256364, "parent-project-7.0.0.pom", true)
	assertPackageVersion(7, 10, 6, 7, 1746256365, "sub-module-7.0.0.pom", true)
	assertPackageVersion(8, 11, 6, 10, 1746256365, "sub-module-7.0.0.jar", false)
	// new versionId 7 -> 38
	assertPackageVersion(9, 12, 38, 11, 1746256367, "bar-1.0-20250503.071248-1.pom", true)
	// new versionId 37
	assertPackageVersion(10, 14, 37, 13, 1746256370, "bar-7.0.0.pom", true)
	assertPackageVersion(11, 15, 7, 14, 1746256373, "-bar-1.0-20250503.071253-2.pom", true)
	assertPackageVersion(12, 17, 8, 16, 1746256375, "-bar-7.0.0.pom", true)
	assertPackageVersion(13, 18, 1, 1, 1746256385, "parent-project-1.0-20250503.071306-2.pom", true)
	assertPackageVersion(14, 20, 2, 3, 1746256386, "sub-module-1.0-20250503.071306-2.pom", true)
	assertPackageVersion(15, 21, 2, 18, 1746256386, "sub-module-1.0-20250503.071306-2.jar", false)
	assertPackageVersion(16, 23, 9, 20, 1746256389, "parent-project-8.0.0.pom", true)
	assertPackageVersion(17, 24, 10, 21, 1746256390, "sub-module-8.0.0.pom", true)
	assertPackageVersion(18, 25, 10, 22, 1746256390, "sub-module-8.0.0.jar", false)
	assertPackageVersion(19, 26, 11, 23, 1746256393, "parent-project-8.0.0.pom", true)
	assertPackageVersion(20, 27, 12, 21, 1746256394, "sub-module-8.0.0.pom", true)
	assertPackageVersion(21, 28, 12, 24, 1746256394, "sub-module-8.0.0.jar", false)
	// new versionId 7 -> 38
	assertPackageVersion(22, 29, 38, 11, 1746256397, "bar-1.0-20250503.071317-3.pom", true)
	assertPackageVersion(23, 31, 36, 26, 1746256399, "bar-8.0.0.pom", true)
	assertPackageVersion(24, 32, 7, 14, 1746256402, "-bar-1.0-20250503.071323-4.pom", true)
	assertPackageVersion(25, 34, 13, 28, 1746256405, "-bar-8.0.0.pom", true)
	assertPackageVersion(26, 35, 1, 1, 1746256415, "parent-project-1.0-20250503.071335-3.pom", true)
	assertPackageVersion(27, 37, 2, 3, 1746256416, "sub-module-1.0-20250503.071335-3.pom", true)
	assertPackageVersion(28, 38, 2, 30, 1746256416, "sub-module-1.0-20250503.071335-3.jar", false)
	assertPackageVersion(29, 40, 14, 32, 1746256419, "parent-project-9.0.0.pom", true)
	assertPackageVersion(30, 41, 15, 33, 1746256420, "sub-module-9.0.0.pom", true)
	assertPackageVersion(31, 42, 15, 34, 1746256420, "sub-module-9.0.0.jar", false)
	assertPackageVersion(32, 43, 16, 35, 1746256423, "parent-project-9.0.0.pom", true)
	assertPackageVersion(33, 44, 17, 33, 1746256424, "sub-module-9.0.0.pom", true)
	assertPackageVersion(34, 45, 17, 36, 1746256424, "sub-module-9.0.0.jar", false)
	// new versionId 7 -> 38
	assertPackageVersion(35, 46, 38, 11, 1746256427, "bar-1.0-20250503.071347-5.pom", true)
	// new versionId 18 -> 35
	assertPackageVersion(36, 48, 35, 38, 1746256429, "bar-9.0.0.pom", true)
	assertPackageVersion(37, 49, 7, 14, 1746256432, "-bar-1.0-20250503.071353-6.pom", true)
	assertPackageVersion(38, 51, 18, 40, 1746256435, "-bar-9.0.0.pom", true)
	assertPackageVersion(39, 52, 1, 1, 1746256445, "parent-project-1.0-20250503.071405-4.pom", true)
	assertPackageVersion(40, 54, 2, 3, 1746256446, "sub-module-1.0-20250503.071405-4.pom", true)
	assertPackageVersion(41, 55, 2, 42, 1746256446, "sub-module-1.0-20250503.071405-4.jar", false)
	assertPackageVersion(42, 57, 19, 44, 1746256449, "parent-project-10.0.0.pom", true)
	assertPackageVersion(43, 58, 20, 45, 1746256450, "sub-module-10.0.0.pom", true)
	assertPackageVersion(44, 59, 20, 46, 1746256450, "sub-module-10.0.0.jar", false)
	assertPackageVersion(45, 60, 21, 47, 1746256452, "parent-project-10.0.0.pom", true)
	assertPackageVersion(46, 61, 22, 45, 1746256453, "sub-module-10.0.0.pom", true)
	assertPackageVersion(47, 62, 22, 48, 1746256453, "sub-module-10.0.0.jar", false)
	// new versionId 7 -> 38
	assertPackageVersion(48, 63, 38, 11, 1746256456, "bar-1.0-20250503.071416-7.pom", true)
	// new versionId 34
	assertPackageVersion(49, 65, 34, 50, 1746256459, "bar-10.0.0.pom", true)
	assertPackageVersion(50, 66, 7, 14, 1746256461, "-bar-1.0-20250503.071422-8.pom", true)
	assertPackageVersion(51, 68, 23, 52, 1746256464, "-bar-10.0.0.pom", true)
	assertPackageVersion(52, 69, 1, 1, 1746256474, "parent-project-1.0-20250503.071435-5.pom", true)
	assertPackageVersion(53, 70, 1, 53, 1746256474, "maven-metadata.xml", false)
	assertPackageVersion(54, 71, 2, 3, 1746256475, "sub-module-1.0-20250503.071435-5.pom", true)
	assertPackageVersion(55, 72, 2, 54, 1746256475, "sub-module-1.0-20250503.071435-5.jar", false)
	assertPackageVersion(56, 73, 2, 55, 1746256476, "maven-metadata.xml", false)
	assertPackageVersion(57, 74, 24, 56, 1746256478, "parent-project-11.0.0.pom", true)
	assertPackageVersion(58, 75, 25, 57, 1746256479, "sub-module-11.0.0.pom", true)
	assertPackageVersion(59, 76, 25, 58, 1746256479, "sub-module-11.0.0.jar", false)
	assertPackageVersion(60, 77, 26, 59, 1746256482, "parent-project-11.0.0.pom", true)
	assertPackageVersion(61, 78, 27, 57, 1746256483, "sub-module-11.0.0.pom", true)
	assertPackageVersion(62, 79, 27, 60, 1746256483, "sub-module-11.0.0.jar", false)
	// new versionId 7 -> 38
	assertPackageVersion(63, 80, 38, 11, 1746256486, "bar-1.0-20250503.071446-9.pom", true)
	// new versionId 33
	assertPackageVersion(64, 82, 33, 62, 1746256488, "bar-11.0.0.pom", true)
	assertPackageVersion(65, 83, 7, 14, 1746256491, "-bar-1.0-20250503.071451-10.pom", true)
	assertPackageVersion(66, 84, 7, 63, 1746256491, "maven-metadata.xml", false)
	assertPackageVersion(67, 85, 28, 64, 1746256494, "-bar-11.0.0.pom", true)
	assertPackageVersion(68, 86, 29, 75, 174625649444986, "br-repo-jooq-1.2.4-sources.jar", false)
	assertPackageVersion(69, 87, 29, 65, 174625649446161, "br-rest-webmvc-1.2.4.jar", false)
	assertPackageVersion(70, 88, 29, 68, 174625649444734, "br-openapi-base-1.2.4.pom", true)
	assertPackageVersion(71, 89, 29, 69, 174625649444746, "br-openapi-base-1.2.4.jar", false)
	assertPackageVersion(72, 90, 29, 70, 174625649444775, "br-openapi-base-1.2.4-sources.jar", false)
	assertPackageVersion(73, 91, 29, 78, 174625649444852, "br-parent-1.2.4.pom", true)
	assertPackageVersion(74, 92, 29, 76, 174625649444900, "br-repo-in-memory-1.2.4.pom", true)
	assertPackageVersion(75, 93, 29, 73, 174625649444911, "br-repo-in-memory-1.2.4.jar", false)
	assertPackageVersion(76, 94, 29, 77, 174625649444922, "br-repo-in-memory-1.2.4-sources.jar", false)
	assertPackageVersion(77, 95, 29, 74, 174625649444953, "br-repo-jooq-1.2.4.pom", true)
	assertPackageVersion(78, 96, 29, 67, 174625649444969, "br-repo-jooq-1.2.4.jar", false)
	assertPackageVersion(79, 97, 29, 71, 174625649446161, "br-rest-webmvc-1.2.4-sources.jar", false)
	assertPackageVersion(80, 98, 29, 66, 174625649446195, "br-rest-webmvc-1.2.4.pom", true)
	assertPackageVersion(81, 99, 29, 72, 174625649446217, "br-root-1.2.4.pom", true)
	assertPackageVersion(82, 100, 30, 66, 174625649446311, "br-rest-webmvc-1.2.4.pom", true)
	assertPackageVersion(83, 101, 30, 65, 174625649446312, "br-rest-webmvc-1.2.4.jar", false)
	assertPackageVersion(84, 102, 30, 71, 174625649446312, "br-rest-webmvc-1.2.4-sources.jar", false)
	// new versionId 31 -> 32
	assertPackageVersion(85, 103, 32, 79, 1746280832, "bar-art-11.0.0.pom", true)
	assertPackageVersion(86, 104, 31, 80, 1746280843, "art-11.0.0.pom", true)
}
