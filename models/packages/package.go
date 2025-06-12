// Copyright 2021 The Gitea Authors. All rights reserved.
// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package packages

import (
	"context"
	"fmt"
	"strings"

	"forgejo.org/models/db"
	"forgejo.org/modules/util"

	"xorm.io/builder"
	"xorm.io/xorm"
)

func init() {
	db.RegisterModel(new(Package))
}

var (
	// ErrDuplicatePackage indicates a duplicated package error
	ErrDuplicatePackage = util.NewAlreadyExistErrorf("package already exists")
	// ErrPackageNotExist indicates a package not exist error
	ErrPackageNotExist = util.NewNotExistErrorf("package does not exist")
)

// Type of a package
type Type string

// List of supported packages
const (
	TypeAlpine    Type = "alpine"
	TypeArch      Type = "arch"
	TypeCargo     Type = "cargo"
	TypeChef      Type = "chef"
	TypeComposer  Type = "composer"
	TypeConan     Type = "conan"
	TypeConda     Type = "conda"
	TypeContainer Type = "container"
	TypeCran      Type = "cran"
	TypeDebian    Type = "debian"
	TypeGeneric   Type = "generic"
	TypeGo        Type = "go"
	TypeHelm      Type = "helm"
	TypeMaven     Type = "maven"
	TypeNpm       Type = "npm"
	TypeNuGet     Type = "nuget"
	TypePub       Type = "pub"
	TypePyPI      Type = "pypi"
	TypeRpm       Type = "rpm"
	TypeAlt       Type = "alt"
	TypeRubyGems  Type = "rubygems"
	TypeSwift     Type = "swift"
	TypeVagrant   Type = "vagrant"
)

var TypeList = []Type{
	TypeAlpine,
	TypeArch,
	TypeCargo,
	TypeChef,
	TypeComposer,
	TypeConan,
	TypeConda,
	TypeContainer,
	TypeCran,
	TypeDebian,
	TypeGeneric,
	TypeGo,
	TypeHelm,
	TypeMaven,
	TypeNpm,
	TypeNuGet,
	TypePub,
	TypePyPI,
	TypeRpm,
	TypeAlt,
	TypeRubyGems,
	TypeSwift,
	TypeVagrant,
}

// Name gets the name of the package type
func (pt Type) Name() string {
	switch pt {
	case TypeAlpine:
		return "Alpine"
	case TypeArch:
		return "Arch"
	case TypeCargo:
		return "Cargo"
	case TypeChef:
		return "Chef"
	case TypeComposer:
		return "Composer"
	case TypeConan:
		return "Conan"
	case TypeConda:
		return "Conda"
	case TypeContainer:
		return "Container"
	case TypeCran:
		return "CRAN"
	case TypeDebian:
		return "Debian"
	case TypeGeneric:
		return "Generic"
	case TypeGo:
		return "Go"
	case TypeHelm:
		return "Helm"
	case TypeMaven:
		return "Maven"
	case TypeNpm:
		return "npm"
	case TypeNuGet:
		return "NuGet"
	case TypePub:
		return "Pub"
	case TypePyPI:
		return "PyPI"
	case TypeRpm:
		return "RPM"
	case TypeAlt:
		return "Alt"
	case TypeRubyGems:
		return "RubyGems"
	case TypeSwift:
		return "Swift"
	case TypeVagrant:
		return "Vagrant"
	}
	panic(fmt.Sprintf("unknown package type: %s", string(pt)))
}

// SVGName gets the name of the package type svg image
func (pt Type) SVGName() string {
	switch pt {
	case TypeAlpine:
		return "gitea-alpine"
	case TypeArch:
		return "gitea-arch"
	case TypeCargo:
		return "gitea-cargo"
	case TypeChef:
		return "gitea-chef"
	case TypeComposer:
		return "gitea-composer"
	case TypeConan:
		return "gitea-conan"
	case TypeConda:
		return "gitea-conda"
	case TypeContainer:
		return "octicon-container"
	case TypeCran:
		return "gitea-cran"
	case TypeDebian:
		return "gitea-debian"
	case TypeGeneric:
		return "octicon-package"
	case TypeGo:
		return "gitea-go"
	case TypeHelm:
		return "gitea-helm"
	case TypeMaven:
		return "gitea-maven"
	case TypeNpm:
		return "gitea-npm"
	case TypeNuGet:
		return "gitea-nuget"
	case TypePub:
		return "gitea-pub"
	case TypePyPI:
		return "gitea-python"
	case TypeRpm:
		return "gitea-rpm"
	case TypeAlt:
		return "gitea-alt"
	case TypeRubyGems:
		return "gitea-rubygems"
	case TypeSwift:
		return "gitea-swift"
	case TypeVagrant:
		return "gitea-vagrant"
	}
	panic(fmt.Sprintf("unknown package type: %s", string(pt)))
}

// Package represents a package
type Package struct {
	ID               int64  `xorm:"pk autoincr"`
	OwnerID          int64  `xorm:"UNIQUE(s) INDEX NOT NULL"`
	RepoID           int64  `xorm:"INDEX"`
	Type             Type   `xorm:"UNIQUE(s) INDEX NOT NULL"`
	Name             string `xorm:"NOT NULL"`
	LowerName        string `xorm:"UNIQUE(s) INDEX NOT NULL"`
	SemverCompatible bool   `xorm:"NOT NULL DEFAULT false"`
	IsInternal       bool   `xorm:"NOT NULL DEFAULT false"`
}

func ResolvePackageName(name string, t Type) string {
	switch t {
	case TypeMaven:
		return name
	default:
		return strings.ToLower(name)
	}
}

// TryInsertPackage inserts a package. If a package exists already, ErrDuplicatePackage is returned
func TryInsertPackage(ctx context.Context, p *Package) (*Package, error) {
	e := db.GetEngine(ctx)

	existing := &Package{}

	has, err := e.Where(builder.Eq{
		"owner_id":   p.OwnerID,
		"type":       p.Type,
		"lower_name": p.LowerName,
	}).Get(existing)
	if err != nil {
		return nil, err
	}
	if has {
		return existing, ErrDuplicatePackage
	}
	if _, err = e.Insert(p); err != nil {
		return nil, err
	}
	return p, nil
}

// DeletePackageByID deletes a package by id
func DeletePackageByID(ctx context.Context, packageID int64) error {
	n, err := db.GetEngine(ctx).ID(packageID).Delete(&Package{})
	if n == 0 && err == nil {
		return ErrPackageNotExist
	}
	return err
}

// SetRepositoryLink sets the linked repository
func SetRepositoryLink(ctx context.Context, packageID, repoID int64) error {
	n, err := db.GetEngine(ctx).ID(packageID).Cols("repo_id").Update(&Package{RepoID: repoID})
	if n == 0 && err == nil {
		return ErrPackageNotExist
	}
	return err
}

func UnlinkRepository(ctx context.Context, packageID int64) error {
	_, err := db.GetEngine(ctx).ID(packageID).Cols("repo_id").Update(&Package{RepoID: 0})
	return err
}

// UnlinkRepositoryFromAllPackages unlinks every package from the repository
func UnlinkRepositoryFromAllPackages(ctx context.Context, repoID int64) error {
	_, err := db.GetEngine(ctx).Where("repo_id = ?", repoID).Cols("repo_id").Update(&Package{})
	return err
}

// GetPackageByID gets a package by id
func GetPackageByID(ctx context.Context, packageID int64) (*Package, error) {
	p := &Package{}

	has, err := db.GetEngine(ctx).ID(packageID).Get(p)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, ErrPackageNotExist
	}
	return p, nil
}

// GetPackageByName gets a package by name
func GetPackageByName(ctx context.Context, ownerID int64, packageType Type, name string) (*Package, error) {
	var cond builder.Cond = builder.Eq{
		"package.owner_id":    ownerID,
		"package.type":        packageType,
		"package.lower_name":  ResolvePackageName(name, packageType),
		"package.is_internal": false,
	}

	p := &Package{}

	has, err := db.GetEngine(ctx).
		Where(cond).
		Get(p)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, ErrPackageNotExist
	}
	return p, nil
}

// GetPackagesByType gets all packages of a specific type
func GetPackagesByType(ctx context.Context, ownerID int64, packageType Type) ([]*Package, error) {
	var cond builder.Cond = builder.Eq{
		"package.owner_id":    ownerID,
		"package.type":        packageType,
		"package.is_internal": false,
	}

	ps := make([]*Package, 0, 10)
	return ps, db.GetEngine(ctx).
		Where(cond).
		Find(&ps)
}

// FindUnreferencedPackages gets all packages without associated versions
func FindUnreferencedPackages(ctx context.Context) ([]int64, error) {
	var pIDs []int64
	if err := db.GetEngine(ctx).
		Select("package.id").
		Table("package").
		Join("LEFT", "package_version", "package_version.package_id = package.id").
		Where("package_version.id IS NULL").
		Find(&pIDs); err != nil {
		return nil, err
	}
	return pIDs, nil
}

func getPackages(ctx context.Context) *xorm.Session {
	return db.GetEngine(ctx).
		Table("package_version").
		Join("INNER", "package", "package.id = package_version.package_id").
		Where("package_version.is_internal = ?", false)
}

func getOwnerPackages(ctx context.Context, ownerID int64) *xorm.Session {
	return getPackages(ctx).
		Where("package.owner_id = ?", ownerID)
}

// HasOwnerPackages tests if a user/org has accessible packages
func HasOwnerPackages(ctx context.Context, ownerID int64) (bool, error) {
	return getOwnerPackages(ctx, ownerID).
		Exist(&Package{})
}

// CountOwnerPackages counts user/org accessible packages
func CountOwnerPackages(ctx context.Context, ownerID int64) (int64, error) {
	return getOwnerPackages(ctx, ownerID).
		Distinct("package.id").
		Count(&Package{})
}

func getRepositoryPackages(ctx context.Context, repositoryID int64) *xorm.Session {
	return getPackages(ctx).
		Where("package.repo_id = ?", repositoryID)
}

// HasRepositoryPackages tests if a repository has packages
func HasRepositoryPackages(ctx context.Context, repositoryID int64) (bool, error) {
	return getRepositoryPackages(ctx, repositoryID).
		Exist(&PackageVersion{})
}

// CountRepositoryPackages counts packages of a repository
func CountRepositoryPackages(ctx context.Context, repositoryID int64) (int64, error) {
	return getRepositoryPackages(ctx, repositoryID).
		Distinct("package.id").
		Count(&Package{})
}
