// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package debian

import (
	"archive/tar"
	"bufio"
	"errors"
	"io"
	"net/mail"
	"regexp"
	"strings"

	"forgejo.org/modules/util"
	"forgejo.org/modules/validation"

	"pault.ag/go/debian/deb"
)

const (
	PropertyDistribution               = "debian.distribution"
	PropertyComponent                  = "debian.component"
	PropertyArchitecture               = "debian.architecture"
	PropertyControl                    = "debian.control"
	PropertyRepositoryIncludeInRelease = "debian.repository.include_in_release"

	SettingKeyPrivate = "debian.key.private"
	SettingKeyPublic  = "debian.key.public"

	RepositoryPackage = "_debian"
	RepositoryVersion = "_repository"

	controlTar = "control.tar"
)

var (
	ErrMissingControlFile     = util.NewInvalidArgumentErrorf("control file is missing")
	ErrUnsupportedCompression = util.NewInvalidArgumentErrorf("unsupported compression algorithm")
	ErrInvalidName            = util.NewInvalidArgumentErrorf("package name is invalid")
	ErrInvalidVersion         = util.NewInvalidArgumentErrorf("package version is invalid")
	ErrInvalidArchitecture    = util.NewInvalidArgumentErrorf("package architecture is invalid")

	// https://www.debian.org/doc/debian-policy/ch-controlfields.html#source
	namePattern = regexp.MustCompile(`\A[a-z0-9][a-z0-9+-.]+\z`)
	// https://www.debian.org/doc/debian-policy/ch-controlfields.html#version
	versionPattern = regexp.MustCompile(`\A(?:[1-9]?[0-9]:)?[a-zA-Z0-9.+~]+(?:-[a-zA-Z0-9.+-~]+)?\z`)
)

type Package struct {
	Name         string
	Version      string
	Architecture string
	Control      string
	Metadata     *Metadata
}

type Metadata struct {
	Maintainer   string   `json:"maintainer,omitempty"`
	ProjectURL   string   `json:"project_url,omitempty"`
	Description  string   `json:"description,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
}

// ParsePackage parses the Debian package file
// https://manpages.debian.org/bullseye/dpkg-dev/deb.5.en.html
func ParsePackage(r io.ReaderAt) (*Package, error) {
	// we could call deb.Load() directly but as of go-debian@v0.21.4 the result is
	// quite different from the hand-rolled ParseControlFile below
	arr, err := deb.LoadAr(r)
	if err != nil {
		return nil, util.NewInvalidArgumentErrorf("invalid ar file: %v", err)
	}

	for {
		hd, err := arr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		suffix, ok := strings.CutPrefix(hd.Name, controlTar)
		if !ok {
			continue
		}

		switch suffix {
		case "", ".gz", ".xz", ".zst":
			break
		default:
			// manually check the extension since hd.Tarfile() tries to read
			// unknown extensions as uncompressed tar
			return nil, ErrUnsupportedCompression
		}

		if p, err := findControlFile(hd); err == nil {
			// found and parsed the `control` entry
			return p, nil
		} else if !errors.Is(err, ErrMissingControlFile) {
			return nil, err
		}
	}

	return nil, ErrMissingControlFile
}

func findControlFile(hd *deb.ArEntry) (*Package, error) {
	tr, closer, err := hd.Tarfile()
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	for {
		hd, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		if hd.Typeflag != tar.TypeReg {
			continue
		}

		if hd.FileInfo().Name() == "control" {
			return ParseControlFile(tr)
		}
	}
	return nil, ErrMissingControlFile
}

// ParseControlFile parses a Debian control file to retrieve the metadata
func ParseControlFile(r io.Reader) (*Package, error) {
	p := &Package{
		Metadata: &Metadata{},
	}

	key := ""
	var depends strings.Builder
	var control strings.Builder

	s := bufio.NewScanner(io.TeeReader(r, &control))
	for s.Scan() {
		line := s.Text()

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if line[0] == ' ' || line[0] == '\t' {
			switch key {
			case "Description":
				p.Metadata.Description += line
			case "Depends":
				depends.WriteString(trimmed)
			}
		} else {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) < 2 {
				continue
			}

			key = parts[0]
			value := strings.TrimSpace(parts[1])
			switch key {
			case "Package":
				p.Name = value
			case "Version":
				p.Version = value
			case "Architecture":
				p.Architecture = value
			case "Maintainer":
				a, err := mail.ParseAddress(value)
				if err != nil || a.Name == "" {
					p.Metadata.Maintainer = value
				} else {
					p.Metadata.Maintainer = a.Name
				}
			case "Description":
				p.Metadata.Description = value
			case "Depends":
				depends.WriteString(value)
			case "Homepage":
				if validation.IsValidURL(value) {
					p.Metadata.ProjectURL = value
				}
			}
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}

	if !namePattern.MatchString(p.Name) {
		return nil, ErrInvalidName
	}
	if !versionPattern.MatchString(p.Version) {
		return nil, ErrInvalidVersion
	}
	if p.Architecture == "" {
		return nil, ErrInvalidArchitecture
	}

	dependencies := strings.Split(depends.String(), ",")
	for i := range dependencies {
		dependencies[i] = strings.TrimSpace(dependencies[i])
	}
	p.Metadata.Dependencies = dependencies

	p.Control = strings.TrimSpace(control.String())

	return p, nil
}
