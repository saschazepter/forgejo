// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package migrations

import (
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"forgejo.org/models/unittest"
	base "forgejo.org/modules/migration"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/services/migrations/allowlist"

	gitea_sdk "code.gitea.io/sdk/gitea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGiteaDownloadRepo(t *testing.T) {
	defer test.MockVariableValueWithReset(&setting.Migrations.AllowLocalNetworks, true, func() { require.NoError(t, allowlist.Init()) })()

	giteaToken := os.Getenv("GITEA_TOKEN")

	fixturePath := "./testdata/gitea/full_download"
	server := unittest.NewMockWebServer(t, "https://gitea.com", fixturePath, giteaToken != "")
	defer server.Close()

	giteaClient, err := gitea_sdk.NewClient(
		server.URL,
		gitea_sdk.SetToken(giteaToken),
		gitea_sdk.SetBasicAuth("", ""),
		gitea_sdk.SetContext(t.Context()),
		gitea_sdk.SetHTTPClient(allowlist.NewMigrationHTTPClient()),
	)
	require.NoError(t, err, "Could not create Client")
	downloader, err := NewGiteaDownloader(t.Context(), giteaClient, server.URL, "gitea/test_repo")
	if downloader == nil {
		t.Fatal("NewGitlabDownloader is nil")
	}
	require.NoError(t, err, "NewGitlabDownloader error occur")

	repo, err := downloader.GetRepoInfo()
	require.NoError(t, err)
	assertRepositoryEqual(t, &base.Repository{
		Name:          "test_repo",
		Owner:         "gitea",
		IsPrivate:     false,
		Description:   "Test repository for testing migration from gitea to gitea",
		CloneURL:      server.URL + "/gitea/test_repo.git",
		OriginalURL:   server.URL + "/gitea/test_repo",
		DefaultBranch: "master",
		Website:       "https://codeberg.org/forgejo/forgejo/",
		AvatarURL:     "",
	}, repo)

	topics, err := downloader.GetTopics()
	require.NoError(t, err)
	sort.Strings(topics)
	assert.Equal(t, []string{"ci", "gitea", "migration", "test"}, topics)

	labels, err := downloader.GetLabels()
	require.NoError(t, err)
	assertLabelsEqual(t, []*base.Label{
		{
			Name:  "Bug",
			Color: "e11d21",
		},
		{
			Name:  "Enhancement",
			Color: "207de5",
		},
		{
			Name:        "Feature",
			Color:       "0052cc",
			Description: "a feature request",
		},
		{
			Name:  "Invalid",
			Color: "d4c5f9",
		},
		{
			Name:  "Question",
			Color: "fbca04",
		},
		{
			Name:  "Valid",
			Color: "53e917",
		},
	}, labels)

	milestones, err := downloader.GetMilestones()
	require.NoError(t, err)
	assertMilestonesEqual(t, []*base.Milestone{
		{
			Title:    "V2 Finalize",
			Created:  time.Unix(0, 0),
			Deadline: new(time.Unix(1599263999, 0)),
			Updated:  new(time.Date(2022, 11, 13, 5, 29, 15, 0, time.UTC)),
			State:    "open",
		},
		{
			Title:       "V1",
			Description: "Generate Content",
			Created:     time.Unix(0, 0),
			Updated:     new(time.Unix(0, 0)),
			Closed:      new(time.Unix(1598985406, 0)),
			State:       "closed",
		},
	}, milestones)

	releases, err := downloader.GetReleases()
	require.NoError(t, err)
	assertReleasesEqual(t, []*base.Release{
		{
			Name:            "Second Release",
			TagName:         "v2-rc1",
			TargetCommitish: "master",
			Body:            "this repo has:\r\n* reactions\r\n* wiki\r\n* issues  (open/closed)\r\n* pulls (open/closed/merged) (external/internal)\r\n* pull reviews\r\n* projects\r\n* milestones\r\n* labels\r\n* releases\r\n\r\nto test migration against",
			Draft:           false,
			Prerelease:      true,
			Created:         time.Date(2020, 9, 1, 18, 2, 43, 0, time.UTC),
			Published:       time.Date(2020, 9, 1, 18, 2, 43, 0, time.UTC),
			PublisherID:     689,
			PublisherName:   "6543",
			PublisherEmail:  "6543@noreply.gitea.com",
		},
		{
			Name:            "First Release",
			TagName:         "V1",
			TargetCommitish: "master",
			Body:            "as title",
			Draft:           false,
			Prerelease:      false,
			Created:         time.Date(2020, 9, 1, 17, 30, 32, 0, time.UTC),
			Published:       time.Date(2020, 9, 1, 17, 30, 32, 0, time.UTC),
			PublisherID:     689,
			PublisherName:   "6543",
			PublisherEmail:  "6543@noreply.gitea.com",
		},
	}, releases)

	issues, isEnd, err := downloader.GetIssues(1, 50)
	require.NoError(t, err)
	assert.True(t, isEnd)
	assert.Len(t, issues, 7)
	assert.Equal(t, "open", issues[0].State)

	issues, isEnd, err = downloader.GetIssues(3, 2)
	require.NoError(t, err)
	assert.False(t, isEnd)

	assertIssuesEqual(t, []*base.Issue{
		{
			Number:      4,
			Title:       "what is this repo about?",
			Content:     "",
			Milestone:   "V1",
			PosterID:    -1,
			PosterName:  "Ghost",
			PosterEmail: "",
			State:       "closed",
			IsLocked:    true,
			Created:     time.Unix(1598975321, 0),
			Updated:     time.Unix(1598975400, 0),
			Labels: []*base.Label{{
				Name:        "Question",
				Color:       "fbca04",
				Description: "",
			}},
			Reactions: []*base.Reaction{
				{
					UserID:   689,
					UserName: "6543",
					Content:  "gitea",
				},
				{
					UserID:   689,
					UserName: "6543",
					Content:  "laugh",
				},
			},
			Closed: new(time.Date(2020, 9, 1, 15, 49, 34, 0, time.UTC)),
		},
		{
			Number:      2,
			Title:       "Spam",
			Content:     ":(",
			Milestone:   "",
			PosterID:    689,
			PosterName:  "6543",
			PosterEmail: "6543@obermui.de",
			State:       "closed",
			IsLocked:    false,
			Created:     time.Unix(1598919780, 0),
			Updated:     time.Unix(1598969497, 0),
			Labels: []*base.Label{{
				Name:        "Invalid",
				Color:       "d4c5f9",
				Description: "",
			}},
			Closed: new(time.Unix(1598969497, 0)),
		},
	}, issues)

	comments, _, err := downloader.GetComments(&base.Issue{Number: 4, ForeignIndex: 4})
	require.NoError(t, err)
	assertCommentsEqual(t, []*base.Comment{
		{
			IssueIndex:  4,
			PosterID:    689,
			PosterName:  "6543",
			PosterEmail: "6543@noreply.gitea.com",
			Created:     time.Unix(1598975370, 0),
			Updated:     time.Unix(1599070865, 0),
			Content:     "a really good question!\n\nIt is the used as TESTSET for gitea2gitea repo migration function",
		},
		{
			IssueIndex:  4,
			PosterID:    -1,
			PosterName:  "Ghost",
			PosterEmail: "ghost@noreply.gitea.com",
			Created:     time.Unix(1598975393, 0),
			Updated:     time.Unix(1598975393, 0),
			Content:     "Oh!",
		},
	}, comments)

	prs, isEnd, err := downloader.GetPullRequests(1, 50)
	require.NoError(t, err)
	assert.True(t, isEnd)
	assert.Len(t, prs, 6)
	prs, isEnd, err = downloader.GetPullRequests(1, 3)
	require.NoError(t, err)
	assert.False(t, isEnd)
	assert.Len(t, prs, 3)
	assertPullRequestEqual(t, &base.PullRequest{
		Number:      12,
		PosterID:    689,
		PosterName:  "6543",
		PosterEmail: "6543@obermui.de",
		Title:       "Dont Touch",
		Content:     "\r\nadd dont touch note",
		Milestone:   "V2 Finalize",
		State:       "closed",
		IsLocked:    false,
		Created:     time.Unix(1598982759, 0),
		Updated:     time.Unix(1599023425, 0),
		Closed:      new(time.Date(2020, 9, 1, 17, 55, 33, 0, time.UTC)),
		Assignees:   []string{"techknowlogick"},
		Base: base.PullRequestBranch{
			CloneURL:  "",
			Ref:       "master",
			SHA:       "827aa28a907853e5ddfa40c8f9bc52471a2685fd",
			RepoName:  "test_repo",
			OwnerName: "gitea",
		},
		Head: base.PullRequestBranch{
			CloneURL:  server.URL + "/6543-forks/test_repo.git",
			Ref:       "refs/pull/12/head",
			SHA:       "b6ab5d9ae000b579a5fff03f92c486da4ddf48b6",
			RepoName:  "test_repo",
			OwnerName: "6543-forks",
		},
		Merged:         true,
		MergedTime:     new(time.Unix(1598982934, 0)),
		MergeCommitSHA: "827aa28a907853e5ddfa40c8f9bc52471a2685fd",
	}, prs[1])

	reviews, err := downloader.GetReviews(&base.Issue{Number: 7, ForeignIndex: 7})
	require.NoError(t, err)
	assertReviewsEqual(t, []*base.Review{
		{
			ID:           1770,
			IssueIndex:   7,
			ReviewerID:   689,
			ReviewerName: "6543",
			CommitID:     "187ece0cb6631e2858a6872e5733433bb3ca3b03",
			CreatedAt:    time.Date(2020, 9, 1, 16, 12, 58, 0, time.UTC),
			State:        "COMMENT", // TODO
			Comments: []*base.ReviewComment{
				{
					ID:        116561,
					InReplyTo: 0,
					Content:   "is one `\\newline` to less?",
					TreePath:  "README.md",
					DiffHunk:  "@@ -2,3 +2,3 @@\n \n-Test repository for testing migration from gitea 2 gitea\n\\ No newline at end of file\n+Test repository for testing migration from gitea 2 gitea",
					Position:  0,
					Line:      4,
					CommitID:  "187ece0cb6631e2858a6872e5733433bb3ca3b03",
					PosterID:  689,
					Reactions: nil,
					CreatedAt: time.Date(2020, 9, 1, 16, 12, 58, 0, time.UTC),
					UpdatedAt: time.Date(2024, 6, 3, 1, 18, 36, 0, time.UTC),
				},
			},
		},
		{
			ID:           1771,
			IssueIndex:   7,
			ReviewerID:   9,
			ReviewerName: "techknowlogick",
			CommitID:     "187ece0cb6631e2858a6872e5733433bb3ca3b03",
			CreatedAt:    time.Date(2020, 9, 1, 17, 6, 47, 0, time.UTC),
			State:        "REQUEST_CHANGES", // TODO
			Content:      "I think this needs some changes",
		},
		{
			ID:           1772,
			IssueIndex:   7,
			ReviewerID:   9,
			ReviewerName: "techknowlogick",
			CommitID:     "187ece0cb6631e2858a6872e5733433bb3ca3b03",
			CreatedAt:    time.Date(2020, 9, 1, 17, 19, 51, 0, time.UTC),
			State:        base.ReviewStateApproved,
			Official:     true,
			Content:      "looks good",
		},
	}, reviews)
}

func TestForgejoDownloadRepo(t *testing.T) {
	defer test.MockVariableValueWithReset(&setting.Migrations.AllowLocalNetworks, true, func() { require.NoError(t, allowlist.Init()) })()
	token := os.Getenv("CODE_FORGEJO_TOKEN")

	fixturePath := "./testdata/code-forgejo-org/full_download"
	server := unittest.NewMockWebServer(t, "https://code.forgejo.org", fixturePath, token != "")
	defer server.Close()

	giteaClient, err := gitea_sdk.NewClient(
		server.URL,
		gitea_sdk.SetToken(token),
		gitea_sdk.SetBasicAuth("", ""),
		gitea_sdk.SetContext(t.Context()),
		gitea_sdk.SetHTTPClient(allowlist.NewMigrationHTTPClient()),
	)
	require.NoError(t, err, "Clould not create Client")
	downloader, err := NewGiteaDownloader(t.Context(), giteaClient, server.URL, "Gusted/agit-test")
	require.NoError(t, err)
	require.NotNil(t, downloader)

	prs, _, err := downloader.GetPullRequests(1, 50)
	require.NoError(t, err)
	assert.Len(t, prs, 1)

	assertPullRequestEqual(t, &base.PullRequest{
		Number:      1,
		PosterID:    63,
		PosterName:  "Gusted",
		PosterEmail: "gusted@noreply.code.forgejo.org",
		Title:       "Add extra information",
		State:       "open",
		Created:     time.Date(2025, time.April, 1, 20, 28, 45, 0, time.UTC),
		Updated:     time.Date(2025, time.April, 1, 20, 28, 45, 0, time.UTC),
		Base: base.PullRequestBranch{
			CloneURL:  "",
			Ref:       "main",
			SHA:       "79ebb873a6497c8847141ba9706b3f757196a1e6",
			RepoName:  "agit-test",
			OwnerName: "Gusted",
		},
		Head: base.PullRequestBranch{
			CloneURL:  server.URL + "/Gusted/agit-test.git",
			Ref:       "refs/pull/1/head",
			SHA:       "667e9317ec37b977e6d3d7d43e3440636970563c",
			RepoName:  "agit-test",
			OwnerName: "Gusted",
		},
		Reactions: []*base.Reaction{{
			UserID:   89,
			UserName: "oliverpool",
			Content:  "+1",
		}},
		Flow: 1,
	}, prs[0])
}

func createGiteaIssueComments(number int) []*gitea_sdk.Comment {
	giteaComments := make([]*gitea_sdk.Comment, 0, number)
	giteaUser := &gitea_sdk.User{
		ID:       1,
		UserName: "rando",
	}
	for i := 1; i <= number; i++ {
		giteaComment := gitea_sdk.Comment{
			ID:      1,
			Poster:  giteaUser,
			Created: time.Date(2025, time.August, 7, 13, i, 25, 0, time.UTC),
			Updated: time.Date(2025, time.August, 7, 13, i+2, 25, 0, time.UTC),
			Body:    strconv.Itoa(i),
		}
		giteaComments = append(giteaComments, &giteaComment)
	}
	return giteaComments
}

func createForgejoIssueComments(comments []*gitea_sdk.Comment) []*base.Comment {
	forgejoComments := make([]*base.Comment, 0, len(comments))
	for _, comment := range comments {
		forgejoComments = append(forgejoComments, &base.Comment{
			IssueIndex:  1, // commentable.GetLocalIndex()
			Index:       comment.ID,
			PosterID:    comment.Poster.ID,
			PosterName:  comment.Poster.UserName,
			PosterEmail: comment.Poster.Email,
			Content:     comment.Body,
			Created:     comment.Created,
			Updated:     comment.Updated,
			Reactions:   []*base.Reaction{},
		})
	}
	return forgejoComments
}

func TestBreakConditions(t *testing.T) {
	defer test.MockVariableValueWithReset(&setting.Migrations.AllowLocalNetworks, true, func() { require.NoError(t, allowlist.Init()) })()
	giteaToken := os.Getenv("GITEA_TOKEN")

	fixturePath := "./testdata/gitea/breaking_conditions"
	server := unittest.NewMockWebServer(t, "https://gitea.com", fixturePath, giteaToken != "")
	defer server.Close()

	// Client
	giteaClient, err := gitea_sdk.NewClient(
		server.URL,
		gitea_sdk.SetToken(""),
		gitea_sdk.SetBasicAuth("", ""),
		gitea_sdk.SetContext(t.Context()),
		gitea_sdk.SetHTTPClient(allowlist.NewMigrationHTTPClient()),
	)
	require.NoError(t, err, "Clould not create Client")

	// Downloader
	downloader, err := NewGiteaDownloader(t.Context(), giteaClient, server.URL, "gitea/test_repo")
	if downloader == nil {
		t.Fatal("NewGiteaDownloader is nil")
	}
	require.NoError(t, err, "Could not create Gitea Downloader")

	pageSize := 20
	buggyPageSize := 25
	smallerPageSize := 15
	downloader.maxPerPage = pageSize

	bugResponse := createGiteaIssueComments(buggyPageSize)
	commentsWithBug := createForgejoIssueComments(bugResponse)

	shorterListResponse := createGiteaIssueComments(smallerPageSize)
	commentsShortList := createForgejoIssueComments(shorterListResponse)

	fullListResponse := createGiteaIssueComments(pageSize)
	differentFullListResponse := createGiteaIssueComments(pageSize)
	differentFullListResponse[0].Body = "Different String"
	differentFullListResponse[len(differentFullListResponse)-1].Body = "Different String"
	commentsFullList := createForgejoIssueComments(fullListResponse)

	assert.True(t, downloader.isSinglePage(commentsWithBug))
	assert.True(t, downloader.isSinglePage(commentsShortList))
	assert.True(t, downloader.isLastPage(commentsShortList, shorterListResponse))
	assert.True(t, downloader.isLastPage(commentsWithBug, bugResponse))
	assert.False(t, downloader.isSinglePage(commentsFullList))
	assert.False(t, downloader.isLastPage(commentsFullList, differentFullListResponse))
}

func TestGiteaDownloaderAvatarURL(t *testing.T) {
	defer test.MockVariableValueWithReset(&setting.Migrations.AllowLocalNetworks, true, func() { require.NoError(t, allowlist.Init()) })()
	GithubLimitRateRemaining = 3 // Wait at 3 remaining since we could have 3 CI in //

	token := os.Getenv("GITEA_READ_TOKEN")
	liveMode := token != ""

	fixturePath := "./testdata/gitea/avatar"
	server := unittest.NewMockWebServer(t, "https://code.forgejo.org", fixturePath, liveMode)
	defer server.Close()

	giteaClient, err := gitea_sdk.NewClient(
		server.URL,
		gitea_sdk.SetToken(token),
		gitea_sdk.SetBasicAuth("", ""),
		gitea_sdk.SetContext(t.Context()),
		gitea_sdk.SetHTTPClient(allowlist.NewMigrationHTTPClient()),
	)
	require.NoError(t, err, "Could not create Client")

	downloader, err := NewGiteaDownloader(t.Context(), giteaClient, server.URL, "birdgoose/forgejo-12921")
	require.NoError(t, err, "Could not create Gitea Downloader")
	require.NotNil(t, downloader, "Could not create Gitea Downloader")

	repo, err := downloader.GetRepoInfo()
	require.NoError(t, err)
	require.NotNil(t, repo)

	assertRepositoryEqual(t, &base.Repository{
		Name:          "forgejo-12921",
		Owner:         "birdgoose",
		Description:   "An example repo for testing Forgejo migrations.",
		CloneURL:      server.URL + "/birdgoose/forgejo-12921.git",
		OriginalURL:   server.URL + "/birdgoose/forgejo-12921",
		DefaultBranch: "main",
		AvatarURL:     server.URL + "/repo-avatars/8b50ec8eccaf97b0a1bed6d9e7bd771173d69b213122e82c98d7d301e3fa60f0",
	}, repo)
}
