// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package migrations

import (
	"net/http"
	"os"
	"testing"
	"time"

	base "forgejo.org/modules/migration"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGogsDownloadRepo(t *testing.T) {
	// Skip tests if Gogs token is not found
	gogsPersonalAccessToken := os.Getenv("GOGS_READ_TOKEN")
	if len(gogsPersonalAccessToken) == 0 {
		t.Skip("skipped test because GOGS_READ_TOKEN was not in the environment")
	}

	resp, err := http.Get("https://try.gogs.io/lunnytest/TESTREPO")
	if err != nil || resp.StatusCode/100 != 2 {
		// skip and don't run test
		t.Skip("visit test repo failed, ignored")
		return
	}

	downloader := NewGogsDownloader(t.Context(), "https://try.gogs.io", "", "", gogsPersonalAccessToken, "lunnytest", "TESTREPO")
	repo, err := downloader.GetRepoInfo()
	require.NoError(t, err)

	assertRepositoryEqual(t, &base.Repository{
		Name:          "TESTREPO",
		Owner:         "lunnytest",
		Description:   "",
		CloneURL:      "https://try.gogs.io/lunnytest/TESTREPO.git",
		OriginalURL:   "https://try.gogs.io/lunnytest/TESTREPO",
		DefaultBranch: "master",
	}, repo)

	milestones, err := downloader.GetMilestones()
	require.NoError(t, err)
	assertMilestonesEqual(t, []*base.Milestone{
		{
			Title: "1.0",
			State: "open",
		},
	}, milestones)

	labels, err := downloader.GetLabels()
	require.NoError(t, err)
	assertLabelsEqual(t, []*base.Label{
		{
			Name:  "bug",
			Color: "ee0701",
		},
		{
			Name:  "duplicate",
			Color: "cccccc",
		},
		{
			Name:  "enhancement",
			Color: "84b6eb",
		},
		{
			Name:  "help wanted",
			Color: "128a0c",
		},
		{
			Name:  "invalid",
			Color: "e6e6e6",
		},
		{
			Name:  "question",
			Color: "cc317c",
		},
		{
			Name:  "wontfix",
			Color: "ffffff",
		},
	}, labels)

	// downloader.GetIssues()
	issues, isEnd, err := downloader.GetIssues(1, 8)
	require.NoError(t, err)
	assert.False(t, isEnd)
	assertIssuesEqual(t, []*base.Issue{
		{
			Number:      1,
			PosterID:    5331,
			PosterName:  "lunny",
			PosterEmail: "xiaolunwen@gmail.com",
			Title:       "test",
			Content:     "test",
			Milestone:   "",
			State:       "open",
			Created:     time.Date(2019, 6, 11, 8, 16, 44, 0, time.UTC),
			Updated:     time.Date(2019, 10, 26, 11, 7, 2, 0, time.UTC),
			Labels: []*base.Label{
				{
					Name:  "bug",
					Color: "ee0701",
				},
			},
		},
	}, issues)

	// downloader.GetComments()
	comments, _, err := downloader.GetComments(&base.Issue{Number: 1, ForeignIndex: 1})
	require.NoError(t, err)
	assertCommentsEqual(t, []*base.Comment{
		{
			IssueIndex:  1,
			PosterID:    5331,
			PosterName:  "lunny",
			PosterEmail: "xiaolunwen@gmail.com",
			Created:     time.Date(2019, 6, 11, 8, 19, 50, 0, time.UTC),
			Updated:     time.Date(2019, 6, 11, 8, 19, 50, 0, time.UTC),
			Content:     "1111",
		},
		{
			IssueIndex:  1,
			PosterID:    15822,
			PosterName:  "clacplouf",
			PosterEmail: "test1234@dbn.re",
			Created:     time.Date(2019, 10, 26, 11, 7, 2, 0, time.UTC),
			Updated:     time.Date(2019, 10, 26, 11, 7, 2, 0, time.UTC),
			Content:     "88888888",
		},
	}, comments)

	// downloader.GetPullRequests()
	_, _, err = downloader.GetPullRequests(1, 3)
	require.Error(t, err)
}

func TestGogsDownloaderFactory_New(t *testing.T) {
	tests := []struct {
		name      string
		args      base.MigrateOptions
		baseURL   string
		repoOwner string
		repoName  string
		wantErr   bool
	}{
		{
			name: "Gogs_at_root",
			args: base.MigrateOptions{
				CloneAddr:    "https://git.example.com/user/repo.git",
				AuthUsername: "username",
				AuthPassword: "password",
				AuthToken:    "authtoken",
			},
			baseURL:   "https://git.example.com/",
			repoOwner: "user",
			repoName:  "repo",
			wantErr:   false,
		},
		{
			name: "Gogs_at_sub_path",
			args: base.MigrateOptions{
				CloneAddr:    "https://git.example.com/subpath/user/repo.git",
				AuthUsername: "username",
				AuthPassword: "password",
				AuthToken:    "authtoken",
			},
			baseURL:   "https://git.example.com/subpath",
			repoOwner: "user",
			repoName:  "repo",
			wantErr:   false,
		},
		{
			name: "Gogs_at_2nd_sub_path",
			args: base.MigrateOptions{
				CloneAddr:    "https://git.example.com/sub1/sub2/user/repo.git",
				AuthUsername: "username",
				AuthPassword: "password",
				AuthToken:    "authtoken",
			},
			baseURL:   "https://git.example.com/sub1/sub2",
			repoOwner: "user",
			repoName:  "repo",
			wantErr:   false,
		},
		{
			name: "Gogs_URL_too_short",
			args: base.MigrateOptions{
				CloneAddr:    "https://git.example.com/repo.git",
				AuthUsername: "username",
				AuthPassword: "password",
				AuthToken:    "authtoken",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &GogsDownloaderFactory{}
			opts := base.MigrateOptions{
				CloneAddr:    tt.args.CloneAddr,
				AuthUsername: tt.args.AuthUsername,
				AuthPassword: tt.args.AuthPassword,
				AuthToken:    tt.args.AuthToken,
			}
			got, err := f.New(t.Context(), opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("GogsDownloaderFactory.New() error = %v, wantErr %v", err, tt.wantErr)
				return
			} else if err != nil {
				return
			}

			assert.IsType(t, &GogsDownloader{}, got)
			assert.Equal(t, tt.baseURL, got.(*GogsDownloader).baseURL)
			assert.Equal(t, tt.repoOwner, got.(*GogsDownloader).repoOwner)
			assert.Equal(t, tt.repoName, got.(*GogsDownloader).repoName)
		})
	}
}
