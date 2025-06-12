// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package container

import (
	"io"
	"strings"
	"testing"

	"forgejo.org/modules/packages/container/helm"

	oci "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseImageConfig(t *testing.T) {
	description := "Image Description"
	author := "Gitea"
	license := "MIT"
	projectURL := "https://gitea.com"
	repositoryURL := "https://gitea.com/gitea"
	documentationURL := "https://docs.gitea.com"

	configOCI := `{"config": {"labels": {"` + labelAuthors + `": "` + author + `", "` + labelLicenses + `": "` + license + `", "` + labelURL + `": "` + projectURL + `", "` + labelSource + `": "` + repositoryURL + `", "` + labelDocumentation + `": "` + documentationURL + `", "` + labelDescription + `": "` + description + `"}}, "history": [{"created_by": "do it 1"}, {"created_by": "dummy #(nop) do it 2"}]}`

	metadata, err := ParseImageConfig(oci.MediaTypeImageManifest, strings.NewReader(configOCI))
	require.NoError(t, err)

	assert.Equal(t, TypeOCI, metadata.Type)
	assert.Equal(t, description, metadata.Description)
	assert.ElementsMatch(t, []string{author}, metadata.Authors)
	assert.Equal(t, license, metadata.Licenses)
	assert.Equal(t, projectURL, metadata.ProjectURL)
	assert.Equal(t, repositoryURL, metadata.RepositoryURL)
	assert.Equal(t, documentationURL, metadata.DocumentationURL)
	assert.ElementsMatch(t, []string{"do it 1", "do it 2"}, metadata.ImageLayers)
	assert.Equal(
		t,
		map[string]string{
			labelAuthors:       author,
			labelLicenses:      license,
			labelURL:           projectURL,
			labelSource:        repositoryURL,
			labelDocumentation: documentationURL,
			labelDescription:   description,
		},
		metadata.Labels,
	)
	assert.Empty(t, metadata.Manifests)

	configHelm := `{"description":"` + description + `", "home": "` + projectURL + `", "sources": ["` + repositoryURL + `"], "maintainers":[{"name":"` + author + `"}]}`

	metadata, err = ParseImageConfig(helm.ConfigMediaType, strings.NewReader(configHelm))
	require.NoError(t, err)

	assert.Equal(t, TypeHelm, metadata.Type)
	assert.Equal(t, description, metadata.Description)
	assert.ElementsMatch(t, []string{author}, metadata.Authors)
	assert.Equal(t, projectURL, metadata.ProjectURL)
	assert.Equal(t, repositoryURL, metadata.RepositoryURL)
}

func TestParseImageConfigEmptyBlob(t *testing.T) {
	t.Run("Empty config blob (EOF)", func(t *testing.T) {
		// Test empty reader (simulates empty config blob common in OCI artifacts)
		metadata, err := ParseImageConfig(oci.MediaTypeImageManifest, strings.NewReader(""))
		require.NoError(t, err)

		assert.Equal(t, TypeOCI, metadata.Type)
		assert.Equal(t, DefaultPlatform, metadata.Platform)
		assert.Empty(t, metadata.Description)
		assert.Empty(t, metadata.Authors)
		assert.Empty(t, metadata.Labels)
		assert.Empty(t, metadata.Manifests)
	})

	t.Run("Empty JSON object", func(t *testing.T) {
		// Test minimal valid JSON config
		metadata, err := ParseImageConfig(oci.MediaTypeImageManifest, strings.NewReader("{}"))
		require.NoError(t, err)

		assert.Equal(t, TypeOCI, metadata.Type)
		assert.Equal(t, DefaultPlatform, metadata.Platform)
		assert.Empty(t, metadata.Description)
		assert.Empty(t, metadata.Authors)
	})

	t.Run("Invalid JSON still returns error", func(t *testing.T) {
		// Test that actual JSON errors (not EOF) are still returned
		_, err := ParseImageConfig(oci.MediaTypeImageManifest, strings.NewReader("{invalid json"))
		require.Error(t, err)
		assert.NotEqual(t, io.EOF, err)
	})

	t.Run("OCI artifact with empty config", func(t *testing.T) {
		// Test OCI artifact scenario with minimal config
		configOCI := `{"config": {}}`
		metadata, err := ParseImageConfig(oci.MediaTypeImageManifest, strings.NewReader(configOCI))
		require.NoError(t, err)

		assert.Equal(t, TypeOCI, metadata.Type)
		assert.Equal(t, DefaultPlatform, metadata.Platform)
		assert.Empty(t, metadata.Description)
		assert.Empty(t, metadata.Authors)
		assert.Empty(t, metadata.ImageLayers)
	})
}
