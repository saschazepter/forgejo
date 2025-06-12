// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package bleve

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	repo_model "forgejo.org/models/repo"
	"forgejo.org/modules/analyze"
	"forgejo.org/modules/charset"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	tokenizer_hierarchy "forgejo.org/modules/indexer/code/bleve/tokenizer/hierarchy"
	"forgejo.org/modules/indexer/code/internal"
	indexer_internal "forgejo.org/modules/indexer/internal"
	inner_bleve "forgejo.org/modules/indexer/internal/bleve"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/typesniffer"

	"github.com/blevesearch/bleve/v2"
	analyzer_custom "github.com/blevesearch/bleve/v2/analysis/analyzer/custom"
	analyzer_keyword "github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	"github.com/blevesearch/bleve/v2/analysis/token/camelcase"
	"github.com/blevesearch/bleve/v2/analysis/token/lowercase"
	"github.com/blevesearch/bleve/v2/analysis/token/unicodenorm"
	"github.com/blevesearch/bleve/v2/analysis/tokenizer/unicode"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/go-enry/go-enry/v2"
)

const (
	unicodeNormalizeName = "unicodeNormalize"
	maxBatchSize         = 16
)

func addUnicodeNormalizeTokenFilter(m *mapping.IndexMappingImpl) error {
	return m.AddCustomTokenFilter(unicodeNormalizeName, map[string]any{
		"type": unicodenorm.Name,
		"form": unicodenorm.NFC,
	})
}

// RepoIndexerData data stored in the repo indexer
type RepoIndexerData struct {
	RepoID    int64
	CommitID  string
	Filename  string
	Content   string
	Language  string
	UpdatedAt time.Time
}

// Type returns the document type, for bleve's mapping.Classifier interface.
func (d *RepoIndexerData) Type() string {
	return repoIndexerDocType
}

const (
	repoIndexerAnalyzer      = "repoIndexerAnalyzer"
	repoIndexerDocType       = "repoIndexerDocType"
	pathHierarchyAnalyzer    = "pathHierarchyAnalyzer"
	repoIndexerLatestVersion = 7
)

// generateBleveIndexMapping generates a bleve index mapping for the repo indexer
func generateBleveIndexMapping() (mapping.IndexMapping, error) {
	docMapping := bleve.NewDocumentMapping()
	numericFieldMapping := bleve.NewNumericFieldMapping()
	numericFieldMapping.IncludeInAll = false
	docMapping.AddFieldMappingsAt("RepoID", numericFieldMapping)

	textFieldMapping := bleve.NewTextFieldMapping()
	textFieldMapping.IncludeInAll = false
	docMapping.AddFieldMappingsAt("Content", textFieldMapping)

	termFieldMapping := bleve.NewTextFieldMapping()
	termFieldMapping.IncludeInAll = false
	termFieldMapping.Analyzer = analyzer_keyword.Name
	docMapping.AddFieldMappingsAt("Language", termFieldMapping)
	docMapping.AddFieldMappingsAt("CommitID", termFieldMapping)

	pathFieldMapping := bleve.NewTextFieldMapping()
	pathFieldMapping.IncludeInAll = false
	pathFieldMapping.Analyzer = pathHierarchyAnalyzer
	docMapping.AddFieldMappingsAt("Filename", pathFieldMapping)

	timeFieldMapping := bleve.NewDateTimeFieldMapping()
	timeFieldMapping.IncludeInAll = false
	docMapping.AddFieldMappingsAt("UpdatedAt", timeFieldMapping)

	mapping := bleve.NewIndexMapping()
	if err := addUnicodeNormalizeTokenFilter(mapping); err != nil {
		return nil, err
	} else if err := mapping.AddCustomAnalyzer(repoIndexerAnalyzer, map[string]any{
		"type":          analyzer_custom.Name,
		"char_filters":  []string{},
		"tokenizer":     unicode.Name,
		"token_filters": []string{unicodeNormalizeName, camelcase.Name, lowercase.Name},
	}); err != nil {
		return nil, err
	} else if err := mapping.AddCustomAnalyzer(pathHierarchyAnalyzer, map[string]any{
		"type":          analyzer_custom.Name,
		"char_filters":  []string{},
		"tokenizer":     tokenizer_hierarchy.Name,
		"token_filters": []string{unicodeNormalizeName},
	}); err != nil {
		return nil, err
	}
	mapping.DefaultAnalyzer = repoIndexerAnalyzer
	mapping.AddDocumentMapping(repoIndexerDocType, docMapping)
	mapping.AddDocumentMapping("_all", bleve.NewDocumentDisabledMapping())

	return mapping, nil
}

var _ internal.Indexer = &Indexer{}

// Indexer represents a bleve indexer implementation
type Indexer struct {
	inner                    *inner_bleve.Indexer
	indexer_internal.Indexer // do not composite inner_bleve.Indexer directly to avoid exposing too much
}

// NewIndexer creates a new bleve local indexer
func NewIndexer(indexDir string) *Indexer {
	inner := inner_bleve.NewIndexer(indexDir, repoIndexerLatestVersion, generateBleveIndexMapping)
	return &Indexer{
		Indexer: inner,
		inner:   inner,
	}
}

func (b *Indexer) addUpdate(ctx context.Context, batchWriter git.WriteCloserError, batchReader *bufio.Reader, commitSha string,
	update internal.FileUpdate, repo *repo_model.Repository, batch *inner_bleve.FlushingBatch,
) error {
	// Ignore vendored files in code search
	if setting.Indexer.ExcludeVendored && analyze.IsVendor(update.Filename) {
		return nil
	}

	size := update.Size

	var err error
	if !update.Sized {
		var stdout string
		stdout, _, err = git.NewCommand(ctx, "cat-file", "-s").AddDynamicArguments(update.BlobSha).RunStdString(&git.RunOpts{Dir: repo.RepoPath()})
		if err != nil {
			return err
		}
		if size, err = strconv.ParseInt(strings.TrimSpace(stdout), 10, 64); err != nil {
			return fmt.Errorf("misformatted git cat-file output: %w", err)
		}
	}

	if size > setting.Indexer.MaxIndexerFileSize {
		return b.addDelete(update.Filename, repo, batch)
	}

	if _, err := batchWriter.Write([]byte(update.BlobSha + "\n")); err != nil {
		return err
	}

	_, _, size, err = git.ReadBatchLine(batchReader)
	if err != nil {
		return err
	}

	fileContents, err := io.ReadAll(io.LimitReader(batchReader, size))
	if err != nil {
		return err
	} else if !typesniffer.DetectContentType(fileContents).IsText() {
		// FIXME: UTF-16 files will probably fail here
		// Even if the file is not recognized as a "text file", we could still put its name into the indexers to make the filename become searchable, while leave the content to empty.
		fileContents = nil
	}

	if _, err = batchReader.Discard(1); err != nil {
		return err
	}
	id := internal.FilenameIndexerID(repo.ID, update.Filename)
	return batch.Index(id, &RepoIndexerData{
		RepoID:    repo.ID,
		CommitID:  commitSha,
		Filename:  update.Filename,
		Content:   string(charset.ToUTF8DropErrors(fileContents, charset.ConvertOpts{})),
		Language:  analyze.GetCodeLanguage(update.Filename, fileContents),
		UpdatedAt: time.Now().UTC(),
	})
}

func (b *Indexer) addDelete(filename string, repo *repo_model.Repository, batch *inner_bleve.FlushingBatch) error {
	id := internal.FilenameIndexerID(repo.ID, filename)
	return batch.Delete(id)
}

// Index indexes the data
func (b *Indexer) Index(ctx context.Context, repo *repo_model.Repository, sha string, changes *internal.RepoChanges) error {
	batch := inner_bleve.NewFlushingBatch(b.inner.Indexer, maxBatchSize)
	if len(changes.Updates) > 0 {
		r, err := gitrepo.OpenRepository(ctx, repo)
		if err != nil {
			return err
		}
		defer r.Close()
		gitBatch, err := r.NewBatch(ctx)
		if err != nil {
			return err
		}
		defer gitBatch.Close()

		for _, update := range changes.Updates {
			if err := b.addUpdate(ctx, gitBatch.Writer, gitBatch.Reader, sha, update, repo, batch); err != nil {
				return err
			}
		}
		gitBatch.Close()
	}
	for _, filename := range changes.RemovedFilenames {
		if err := b.addDelete(filename, repo, batch); err != nil {
			return err
		}
	}
	return batch.Flush()
}

// Delete deletes indexes by ids
func (b *Indexer) Delete(_ context.Context, repoID int64) error {
	query := inner_bleve.NumericEqualityQuery(repoID, "RepoID")
	searchRequest := bleve.NewSearchRequestOptions(query, 2147483647, 0, false)
	result, err := b.inner.Indexer.Search(searchRequest)
	if err != nil {
		return err
	}
	batch := inner_bleve.NewFlushingBatch(b.inner.Indexer, maxBatchSize)
	for _, hit := range result.Hits {
		if err = batch.Delete(hit.ID); err != nil {
			return err
		}
	}
	return batch.Flush()
}

// Search searches for files in the specified repo.
// Returns the matching file-paths
func (b *Indexer) Search(ctx context.Context, opts *internal.SearchOptions) (int64, []*internal.SearchResult, []*internal.SearchResultLanguages, error) {
	var (
		indexerQuery query.Query
		keywordQuery query.Query
	)

	if opts.Mode == internal.CodeSearchModeUnion {
		query := bleve.NewDisjunctionQuery()
		for _, field := range strings.Fields(opts.Keyword) {
			query.AddQuery(inner_bleve.MatchPhraseQuery(field, "Content", repoIndexerAnalyzer, false, 1.0))
		}
		keywordQuery = query
	} else {
		keywordQuery = inner_bleve.MatchPhraseQuery(opts.Keyword, "Content", repoIndexerAnalyzer, false, 1.0)
	}

	if len(opts.RepoIDs) > 0 {
		repoQueries := make([]query.Query, 0, len(opts.RepoIDs))
		for _, repoID := range opts.RepoIDs {
			repoQueries = append(repoQueries, inner_bleve.NumericEqualityQuery(repoID, "RepoID"))
		}

		indexerQuery = bleve.NewConjunctionQuery(
			bleve.NewDisjunctionQuery(repoQueries...),
			keywordQuery,
		)
	} else {
		indexerQuery = keywordQuery
	}

	opts.Filename = strings.Trim(opts.Filename, "/")
	if len(opts.Filename) > 0 {
		// we use a keyword analyzer for the query than path hierarchy analyzer
		// to match only the exact path
		// eg, a query for modules/indexer/code
		// should not provide results for modules/ nor modules/indexer
		indexerQuery = bleve.NewConjunctionQuery(
			indexerQuery,
			inner_bleve.MatchQuery(opts.Filename, "Filename", analyzer_keyword.Name, 0),
		)
	}

	// Save for reuse without language filter
	facetQuery := indexerQuery
	if len(opts.Language) > 0 {
		indexerQuery = bleve.NewConjunctionQuery(
			indexerQuery,
			inner_bleve.MatchQuery(opts.Language, "Language", analyzer_keyword.Name, 0),
		)
	}

	from, pageSize := opts.GetSkipTake()
	searchRequest := bleve.NewSearchRequestOptions(indexerQuery, pageSize, from, false)
	searchRequest.Fields = []string{"Content", "RepoID", "Filename", "Language", "CommitID", "UpdatedAt"}
	searchRequest.IncludeLocations = true

	if len(opts.Language) == 0 {
		searchRequest.AddFacet("languages", bleve.NewFacetRequest("Language", 10))
	}

	searchRequest.SortBy([]string{"-_score", "UpdatedAt"})

	result, err := b.inner.Indexer.SearchInContext(ctx, searchRequest)
	if err != nil {
		return 0, nil, nil, err
	}

	total := int64(result.Total)

	searchResults := make([]*internal.SearchResult, len(result.Hits))
	for i, hit := range result.Hits {
		startIndex, endIndex := -1, -1
		for _, locations := range hit.Locations["Content"] {
			if startIndex != -1 && endIndex != -1 {
				break
			}
			location := locations[0]
			locationStart := int(location.Start)
			locationEnd := int(location.End)
			if startIndex < 0 || locationStart < startIndex {
				startIndex = locationStart
			}
			if endIndex < 0 && locationEnd > endIndex {
				endIndex = locationEnd
			}
		}
		language := hit.Fields["Language"].(string)
		var updatedUnix timeutil.TimeStamp
		if t, err := time.Parse(time.RFC3339, hit.Fields["UpdatedAt"].(string)); err == nil {
			updatedUnix = timeutil.TimeStamp(t.Unix())
		}
		searchResults[i] = &internal.SearchResult{
			RepoID:      int64(hit.Fields["RepoID"].(float64)),
			StartIndex:  startIndex,
			EndIndex:    endIndex,
			Filename:    hit.Fields["Filename"].(string),
			Content:     hit.Fields["Content"].(string),
			CommitID:    hit.Fields["CommitID"].(string),
			UpdatedUnix: updatedUnix,
			Language:    language,
			Color:       enry.GetColor(language),
		}
	}

	searchResultLanguages := make([]*internal.SearchResultLanguages, 0, 10)
	if len(opts.Language) > 0 {
		// Use separate query to go get all language counts
		facetRequest := bleve.NewSearchRequestOptions(facetQuery, 1, 0, false)
		facetRequest.Fields = []string{"Content", "RepoID", "Filename", "Language", "CommitID", "UpdatedAt"}
		facetRequest.IncludeLocations = true
		facetRequest.AddFacet("languages", bleve.NewFacetRequest("Language", 10))

		if result, err = b.inner.Indexer.Search(facetRequest); err != nil {
			return 0, nil, nil, err
		}
	}
	languagesFacet := result.Facets["languages"]
	for _, term := range languagesFacet.Terms.Terms() {
		if len(term.Term) == 0 {
			continue
		}
		searchResultLanguages = append(searchResultLanguages, &internal.SearchResultLanguages{
			Language: term.Term,
			Color:    enry.GetColor(term.Term),
			Count:    term.Count,
		})
	}
	return total, searchResults, searchResultLanguages, nil
}
