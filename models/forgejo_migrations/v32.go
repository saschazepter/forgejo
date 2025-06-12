// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package forgejo_migrations //nolint:revive

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"forgejo.org/models/packages"
	"forgejo.org/modules/json"
	"forgejo.org/modules/log"
	"forgejo.org/modules/packages/maven"
	packages_service "forgejo.org/services/packages"

	"golang.org/x/net/context"
	"xorm.io/xorm"
)

var getPackage = packages_service.GetPackageFileStream

type Snapshot struct {
	baseVersion string
	date        string
	time        string
	build       int
}

type Metadata struct {
	XMLName      xml.Name `xml:"metadata"`
	ModelVersion string   `xml:"modelVersion,attr"`
	GroupID      string   `xml:"groupId"`
	ArtifactID   string   `xml:"artifactId"`
	Version      string   `xml:"version"`
}

type mavenPackageResult struct {
	PackageFile    *packages.PackageFile    `xorm:"extends"`
	PackageVersion *packages.PackageVersion `xorm:"extends"`
	Package        *packages.Package        `xorm:"extends"`
	PackageName    string                   `xorm:"-"`
	Snapshot       *Snapshot                `xorm:"-"`
	GroupID        string                   `xorm:"-"`
	ArtifactID     string                   `xorm:"-"`
}

// ChangeMavenArtifactConcatenation resolves old dash-concatenated Maven coordinates and regenerates metadata.
// Note: runs per-owner in a single transaction; failures roll back all owners.
func ChangeMavenArtifactConcatenation(x *xorm.Engine) error {
	sess := x.NewSession()
	defer sess.Close()

	if err := sess.Begin(); err != nil {
		return err
	}

	// get unique owner IDs of Maven packages
	var ownerIDs []*int64
	if err := sess.
		Table("package").
		Select("package.owner_id").
		Where("package.type = 'maven'").
		GroupBy("package.owner_id").
		OrderBy("package.owner_id DESC").
		Find(&ownerIDs); err != nil {
		return err
	}

	for _, id := range ownerIDs {
		if err := fixMavenArtifactPerOwner(sess, id); err != nil {
			log.Error("owner %d migration failed: %v", id, err)
			return err // rollback all
		}
	}

	return sess.Commit()
}

func fixMavenArtifactPerOwner(sess *xorm.Session, ownerID *int64) error {
	results, err := getMavenPackageResultsToUpdate(sess, ownerID)
	if err != nil {
		return err
	}

	if err = resolvePackageCollisions(results, sess); err != nil {
		return err
	}

	if err = processPackageVersions(results, sess); err != nil {
		return err
	}

	return processPackageFiles(results, sess)
}

// processPackageFiles updates Maven package files and versions in the database
// Returns an error if any database or processing operation fails.
func processPackageFiles(results []*mavenPackageResult, sess *xorm.Session) error {
	processedVersion := make(map[string][]*mavenPackageResult)

	for _, r := range results {
		if r.Snapshot != nil {
			key := fmt.Sprintf("%s:%s", r.PackageName, r.PackageVersion.LowerVersion)
			processedVersion[key] = append(processedVersion[key], r)
		}

		// Only update version_id when it differs
		if r.PackageVersion.ID != r.PackageFile.VersionID {
			pattern := strings.TrimSuffix(r.PackageFile.Name, ".pom") + "%"
			// Per routers/api/packages/maven/maven.go:338, POM files already have the `IsLead`, so no update needed for this prop
			if _, err := sess.Exec("UPDATE package_file SET version_id = ? WHERE version_id = ? and name like ?", r.PackageVersion.ID, r.PackageFile.VersionID, pattern); err != nil {
				return err
			}
		}
	}

	// If maven-metadata.xml is missing (snapshot path collision), skip regeneration
	// Without this metadata, Maven cannot resolve snapshot details
	for _, packageResults := range processedVersion {
		sort.Slice(packageResults, func(i, j int) bool {
			return packageResults[i].Snapshot.build > packageResults[j].Snapshot.build
		})

		rs := packageResults[0]

		pf, md, err := parseMetadata(sess, rs)
		if err != nil {
			return err
		}

		if pf != nil && md != nil && md.GroupID == rs.GroupID && md.ArtifactID == rs.ArtifactID {
			if pf.VersionID != rs.PackageFile.VersionID {
				if _, err := sess.ID(pf.ID).Cols("version_id").Update(pf); err != nil {
					return err
				}
			}
			continue
		}

		log.Warn("no maven-metadata.xml found for (id: %d) [%s:%s]", rs.PackageVersion.ID, rs.PackageName, rs.PackageVersion.Version)
	}

	return nil
}

// parseMetadata retrieves metadata for a Maven package file from the database and decodes it into a Metadata object.
// Returns the associated PackageFile, Metadata, and any error encountered during processing.
func parseMetadata(sess *xorm.Session, snapshot *mavenPackageResult) (*packages.PackageFile, *Metadata, error) {
	ctx := context.Background()

	var pf packages.PackageFile
	found, err := sess.Table(pf).
		Where("version_id = ?", snapshot.PackageFile.VersionID). // still the old id
		And("lower_name = ?", "maven-metadata.xml").
		Get(&pf)
	if err != nil {
		return nil, nil, err
	}

	if !found {
		return nil, nil, nil
	}

	s, _, _, err := getPackage(ctx, &pf)
	if err != nil {
		return nil, nil, err
	}

	defer s.Close()
	dec := xml.NewDecoder(s)
	var m Metadata
	if err := dec.Decode(&m); err != nil {
		return nil, nil, err
	}

	return &pf, &m, nil
}

// processPackageVersions processes Maven package versions by updating metadata or inserting new records as necessary.
// It avoids redundant updates by tracking already processed versions using a map. Returns an error on failure.
func processPackageVersions(results []*mavenPackageResult, sess *xorm.Session) error {
	processedVersion := make(map[string]int64)

	for _, r := range results {
		key := fmt.Sprintf("%s:%s", r.PackageName, r.PackageVersion.Version)

		if id, ok := processedVersion[key]; ok {
			r.PackageVersion.ID = id
			continue
		}

		// for non collisions, just update the metadata
		if r.PackageVersion.PackageID == r.Package.ID {
			if _, err := sess.ID(r.PackageVersion.ID).Cols("metadata_json").Update(r.PackageVersion); err != nil {
				return err
			}
		} else {
			log.Info("Create new maven package version for %s:%s", r.PackageName, r.PackageVersion.Version)
			r.PackageVersion.ID = 0
			r.PackageVersion.PackageID = r.Package.ID
			if _, err := sess.Insert(r.PackageVersion); err != nil {
				return err
			}
		}

		processedVersion[key] = r.PackageVersion.ID
	}

	return nil
}

// getMavenPackageResultsToUpdate retrieves Maven package results that need updates based on the owner ID.
// It processes POM metadata, fixes package inconsistencies, and filters corrupted package versions.
func getMavenPackageResultsToUpdate(sess *xorm.Session, ownerID *int64) ([]*mavenPackageResult, error) {
	ctx := context.Background()
	var candidates []*mavenPackageResult
	if err := sess.
		Table("package_file").
		Select("package_file.*, package_version.*, package.*").
		Join("INNER", "package_version", "package_version.id = package_file.version_id").
		Join("INNER", "package", "package.id = package_version.package_id").
		Where("package_file.lower_name LIKE ?", "%.pom").
		And("package.type = ?", "maven").
		And("package.owner_id = ?", ownerID).
		OrderBy("package_version.id DESC, package_file.id DESC").
		Find(&candidates); err != nil {
		return nil, err
	}

	var results []*mavenPackageResult
	var corruptedVersionIDs []int64

	// fetch actual metadata from blob as all packages needs to be fixed following the new string concatenation
	for _, r := range candidates {
		if err := processPomMetadata(ctx, r); err != nil {
			// Skip corrupted versions; admin intervention may be needed to repair these files.
			log.Warn("Failed to process package file [id: %d] ignoring package version[%d]: %v", r.PackageFile.ID, r.PackageVersion.ID, err)

			corruptedVersionIDs = append(corruptedVersionIDs, r.PackageVersion.ID)

			continue
		}

		results = append(results, r)
		log.Debug("Resolved id [%d] from [%s:%s] to [%s:%s] [Snapshot: %v]", r.Package.ID, r.Package.Name, r.PackageVersion.Version, r.PackageName, r.PackageVersion.Version, r.Snapshot)
	}

	for _, corruptedVersionID := range corruptedVersionIDs {
		for i := 0; i < len(results); {
			if corruptedVersionID == results[i].PackageVersion.ID {
				results = append(results[:i], results[i+1:]...)
			} else {
				i++
			}
		}
	}

	return results, nil
}

// resolvePackageCollisions handles name collisions by keeping the first existing record and inserting new Package records for subsequent collisions.
// Returns a map from PackageName to its resolved Package.ID.
func resolvePackageCollisions(results []*mavenPackageResult, sess *xorm.Session) error {
	// Group new names by lowerName
	collisions := make(map[string][]string)
	for _, r := range results {
		names := collisions[r.Package.LowerName]
		if !slices.Contains(names, r.PackageName) {
			collisions[r.Package.LowerName] = append(names, r.PackageName)
		}
	}

	pkgIDByName := make(map[string]int64)
	var err error

	for _, r := range results {
		list := collisions[r.Package.LowerName]

		// update to the upcoming package name which is colon separated
		r.Package.Name = r.PackageName
		r.Package.LowerName = r.PackageName

		// exiting entry
		if id, ok := pkgIDByName[r.PackageName]; ok {
			r.Package.ID = id
			// first package kept the current id
		} else if list[0] == r.PackageName {
			pkgIDByName[r.PackageName] = r.Package.ID

			if _, err = sess.ID(r.Package.ID).Cols("name", "lower_name").Update(r.Package); err != nil {
				return err
			}
			// create a new entry
		} else {
			log.Info("Create new maven package for %s", r.Package.Name)

			r.Package.ID = 0
			if _, err = sess.Insert(r.Package); err != nil {
				return err
			}

			pkgIDByName[r.PackageName] = r.Package.ID
		}
	}

	return nil
}

// processPomMetadata processes a Maven package file, parses its POM metadata, and updates PackageVersion information.
func processPomMetadata(ctx context.Context, mpr *mavenPackageResult) error {
	s, _, _, err := getPackage(ctx, mpr.PackageFile)
	if err != nil {
		return fmt.Errorf("unable to get package stream: %v", err)
	}
	defer s.Close()

	actualPom, err := maven.ParsePackageMetaData(s)
	if err != nil {
		return fmt.Errorf("failed to parse POM metadata: %v", err)
	}

	raw, err := json.Marshal(actualPom)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %v", err)
	}

	var currentPom *maven.Metadata
	if err = json.Unmarshal([]byte(mpr.PackageVersion.MetadataJSON), &currentPom); err != nil {
		return fmt.Errorf("failed to unmarshal metadata: %v", err)
	}

	// since the rest api can also be (ab)used to upload artifacts wrong, just ignore them
	if isInvalidMatch(currentPom, actualPom) {
		return fmt.Errorf("artifact mismatch: actual [%s] expected [%s]", actualPom.ArtifactID, currentPom.ArtifactID)
	}

	// this will also fix packages that missed its groupID
	// Ref: https://codeberg.org/forgejo/forgejo/pulls/6329
	mpr.PackageVersion.MetadataJSON = string(raw)

	// Since Maven packages are case-sensitive, avoid potential clashes and clean-ups
	// by enforcing consistent case handling similar to RPM packages.
	mpr.PackageName = fmt.Sprintf("%s:%s", actualPom.GroupID, actualPom.ArtifactID)

	mpr.GroupID = actualPom.GroupID
	mpr.ArtifactID = actualPom.ArtifactID

	if strings.HasSuffix(mpr.PackageVersion.Version, "-SNAPSHOT") {
		snap, err := extraSnapshotDetails(currentPom, actualPom, mpr)
		if err != nil {
			return err
		}
		mpr.Snapshot = snap
	} else {
		// only snapshots are affected but kept in case of not complete fixtures
		expectedFileName := fmt.Sprintf("%s-%s.pom", actualPom.ArtifactID, mpr.PackageVersion.Version)
		if mpr.PackageFile.Name != expectedFileName {
			log.Warn("invalid package file name - this is a collision which needs to be resolved expected [%s], actual [%s]", expectedFileName, mpr.PackageFile.Name)
		}
	}

	return nil
}

// extraSnapshotDetails extracts detailed snapshot information
// Returns a Snapshot object encapsulating the extracted details or an error if the filename is invalid or parsing fails.
func extraSnapshotDetails(currentPom, actualPom *maven.Metadata, mpr *mavenPackageResult) (*Snapshot, error) {
	pattern := `^%s-` +
		`(?P<baseVersion>[\d\.]+)-` +
		`(?P<date>\d{8})\.` +
		`(?P<time>\d{6})-` +
		`(?P<build>\d+)\.pom$`
	re := regexp.MustCompile(fmt.Sprintf(pattern, regexp.QuoteMeta(currentPom.ArtifactID)))

	if re.FindStringSubmatch(mpr.PackageFile.Name) == nil {
		log.Warn("invalid package file name - this is a collision which needs to be resolved %s", mpr.PackageFile.Name)
	}

	re = regexp.MustCompile(fmt.Sprintf(pattern, regexp.QuoteMeta(actualPom.ArtifactID)))
	match := re.FindStringSubmatch(mpr.PackageFile.Name)

	if match == nil {
		return nil, fmt.Errorf("invalid snapshot filename: %s", mpr.PackageFile.Name)
	}

	baseIdx := re.SubexpIndex("baseVersion")
	dateIdx := re.SubexpIndex("date")
	timeIdx := re.SubexpIndex("time")
	buildIdx := re.SubexpIndex("build")

	buildNum, _ := strconv.Atoi(match[buildIdx])

	return &Snapshot{
		baseVersion: match[baseIdx],
		date:        match[dateIdx],
		time:        match[timeIdx],
		build:       buildNum,
	}, nil
}

// isInvalidMatch returns true if the stored metadata’s groupID:artifactID
// differs from actual values—accounting for an earlier bug that sometimes omitted the groupID.
func isInvalidMatch(current, actual *maven.Metadata) bool {
	bare := fmt.Sprintf("-%s", actual.ArtifactID)
	full := fmt.Sprintf("%s-%s", actual.GroupID, actual.ArtifactID)
	currentID := fmt.Sprintf("%s-%s", current.GroupID, current.ArtifactID)

	return currentID != full && currentID != bare
}
