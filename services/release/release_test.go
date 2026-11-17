// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package release

import (
	"strings"
	"testing"

	activities_model "forgejo.org/models/activities"
	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/test"
	"forgejo.org/services/attachment"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}

func TestRelease_Create(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	gitRepo, err := gitrepo.OpenRepository(git.DefaultContext, repo)
	require.NoError(t, err)
	defer gitRepo.Close()

	require.NoError(t, CreateRelease(gitRepo, &repo_model.Release{
		RepoID:       repo.ID,
		Repo:         repo,
		PublisherID:  user.ID,
		Publisher:    user,
		TagName:      "v0.1",
		Target:       "master",
		Title:        "v0.1 is released",
		Note:         "v0.1 is released",
		IsDraft:      false,
		IsPrerelease: false,
		IsTag:        false,
	}, "", []*AttachmentChange{}))

	require.NoError(t, CreateRelease(gitRepo, &repo_model.Release{
		RepoID:       repo.ID,
		Repo:         repo,
		PublisherID:  user.ID,
		Publisher:    user,
		TagName:      "v0.1.1",
		Target:       "65f1bf27bc3bf70f64657658635e66094edbcb4d",
		Title:        "v0.1.1 is released",
		Note:         "v0.1.1 is released",
		IsDraft:      false,
		IsPrerelease: false,
		IsTag:        false,
	}, "", []*AttachmentChange{}))

	require.NoError(t, CreateRelease(gitRepo, &repo_model.Release{
		RepoID:       repo.ID,
		Repo:         repo,
		PublisherID:  user.ID,
		Publisher:    user,
		TagName:      "v0.1.2",
		Target:       "65f1bf2",
		Title:        "v0.1.2 is released",
		Note:         "v0.1.2 is released",
		IsDraft:      false,
		IsPrerelease: false,
		IsTag:        false,
	}, "", []*AttachmentChange{}))

	require.NoError(t, CreateRelease(gitRepo, &repo_model.Release{
		RepoID:       repo.ID,
		Repo:         repo,
		PublisherID:  user.ID,
		Publisher:    user,
		TagName:      "v0.1.3",
		Target:       "65f1bf2",
		Title:        "v0.1.3 is released",
		Note:         "v0.1.3 is released",
		IsDraft:      true,
		IsPrerelease: false,
		IsTag:        false,
	}, "", []*AttachmentChange{}))

	require.NoError(t, CreateRelease(gitRepo, &repo_model.Release{
		RepoID:       repo.ID,
		Repo:         repo,
		PublisherID:  user.ID,
		Publisher:    user,
		TagName:      "v0.1.4",
		Target:       "65f1bf2",
		Title:        "v0.1.4 is released",
		Note:         "v0.1.4 is released",
		IsDraft:      false,
		IsPrerelease: true,
		IsTag:        false,
	}, "", []*AttachmentChange{}))

	testPlayload := "testtest"

	attach, err := attachment.NewAttachment(db.DefaultContext, &repo_model.Attachment{
		RepoID:     repo.ID,
		UploaderID: user.ID,
		Name:       "test.txt",
	}, strings.NewReader(testPlayload), int64(len([]byte(testPlayload))))
	require.NoError(t, err)

	release := repo_model.Release{
		RepoID:       repo.ID,
		Repo:         repo,
		PublisherID:  user.ID,
		Publisher:    user,
		TagName:      "v0.1.5",
		Target:       "65f1bf2",
		Title:        "v0.1.5 is released",
		Note:         "v0.1.5 is released",
		IsDraft:      false,
		IsPrerelease: false,
		IsTag:        true,
	}
	require.NoError(t, CreateRelease(gitRepo, &release, "test", []*AttachmentChange{
		{
			Action: "add",
			Type:   "attachment",
			UUID:   attach.UUID,
		},
	}))
	assert.NoError(t, repo_model.GetReleaseAttachments(db.DefaultContext, &release))
	assert.Len(t, release.Attachments, 1)
	assert.Equal(t, attach.UUID, release.Attachments[0].UUID)
	assert.Equal(t, attach.Name, release.Attachments[0].Name)
	assert.Equal(t, attach.ExternalURL, release.Attachments[0].ExternalURL)

	release = repo_model.Release{
		RepoID:       repo.ID,
		Repo:         repo,
		PublisherID:  user.ID,
		Publisher:    user,
		TagName:      "v0.1.6",
		Target:       "65f1bf2",
		Title:        "v0.1.6 is released",
		Note:         "v0.1.6 is released",
		IsDraft:      false,
		IsPrerelease: false,
		IsTag:        true,
	}
	assert.NoError(t, CreateRelease(gitRepo, &release, "", []*AttachmentChange{
		{
			Action:      "add",
			Type:        "external",
			Name:        "test",
			ExternalURL: "https://forgejo.org/",
		},
	}))
	assert.NoError(t, repo_model.GetReleaseAttachments(db.DefaultContext, &release))
	assert.Len(t, release.Attachments, 1)
	assert.Equal(t, "test", release.Attachments[0].Name)
	assert.Equal(t, "https://forgejo.org/", release.Attachments[0].ExternalURL)

	release = repo_model.Release{
		RepoID:       repo.ID,
		Repo:         repo,
		PublisherID:  user.ID,
		Publisher:    user,
		TagName:      "v0.1.7",
		Target:       "65f1bf2",
		Title:        "v0.1.7 is released",
		Note:         "v0.1.7 is released",
		IsDraft:      false,
		IsPrerelease: false,
		IsTag:        true,
	}
	assert.Error(t, CreateRelease(gitRepo, &repo_model.Release{}, "", []*AttachmentChange{
		{
			Action: "add",
			Type:   "external",
			Name:   "Click me",
			// Invalid URL (API URL of current instance), this should result in an error
			ExternalURL: "https://try.gitea.io/api/v1/user/follow",
		},
	}))
}

func TestRelease_Update(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	gitRepo, err := gitrepo.OpenRepository(git.DefaultContext, repo)
	require.NoError(t, err)
	defer gitRepo.Close()

	// Test a changed release
	require.NoError(t, CreateRelease(gitRepo, &repo_model.Release{
		RepoID:       repo.ID,
		Repo:         repo,
		PublisherID:  user.ID,
		Publisher:    user,
		TagName:      "v1.1.1",
		Target:       "master",
		Title:        "v1.1.1 is released",
		Note:         "v1.1.1 is released",
		IsDraft:      false,
		IsPrerelease: false,
		IsTag:        false,
	}, "", []*AttachmentChange{}))
	release, err := repo_model.GetRelease(db.DefaultContext, repo.ID, "v1.1.1")
	require.NoError(t, err)
	releaseCreatedUnix := release.CreatedUnix
	test.SleepTillNextSecond()
	release.Note = "Changed note"
	require.NoError(t, UpdateRelease(db.DefaultContext, user, gitRepo, release, false, []*AttachmentChange{}))
	release, err = repo_model.GetReleaseByID(db.DefaultContext, release.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(releaseCreatedUnix), int64(release.CreatedUnix))

	// Test a changed draft
	require.NoError(t, CreateRelease(gitRepo, &repo_model.Release{
		RepoID:       repo.ID,
		Repo:         repo,
		PublisherID:  user.ID,
		Publisher:    user,
		TagName:      "v1.2.1",
		Target:       "65f1bf2",
		Title:        "v1.2.1 is draft",
		Note:         "v1.2.1 is draft",
		IsDraft:      true,
		IsPrerelease: false,
		IsTag:        false,
	}, "", []*AttachmentChange{}))
	release, err = repo_model.GetRelease(db.DefaultContext, repo.ID, "v1.2.1")
	require.NoError(t, err)
	releaseCreatedUnix = release.CreatedUnix
	test.SleepTillNextSecond()
	release.Title = "Changed title"
	require.NoError(t, UpdateRelease(db.DefaultContext, user, gitRepo, release, false, []*AttachmentChange{}))
	release, err = repo_model.GetReleaseByID(db.DefaultContext, release.ID)
	require.NoError(t, err)
	assert.Less(t, int64(releaseCreatedUnix), int64(release.CreatedUnix))

	// Test a changed pre-release
	require.NoError(t, CreateRelease(gitRepo, &repo_model.Release{
		RepoID:       repo.ID,
		Repo:         repo,
		PublisherID:  user.ID,
		Publisher:    user,
		TagName:      "v1.3.1",
		Target:       "65f1bf2",
		Title:        "v1.3.1 is pre-released",
		Note:         "v1.3.1 is pre-released",
		IsDraft:      false,
		IsPrerelease: true,
		IsTag:        false,
	}, "", []*AttachmentChange{}))
	release, err = repo_model.GetRelease(db.DefaultContext, repo.ID, "v1.3.1")
	require.NoError(t, err)
	releaseCreatedUnix = release.CreatedUnix
	test.SleepTillNextSecond()
	release.Title = "Changed title"
	release.Note = "Changed note"
	require.NoError(t, UpdateRelease(db.DefaultContext, user, gitRepo, release, false, []*AttachmentChange{}))
	release, err = repo_model.GetReleaseByID(db.DefaultContext, release.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(releaseCreatedUnix), int64(release.CreatedUnix))

	// Test create release
	release = &repo_model.Release{
		RepoID:       repo.ID,
		Repo:         repo,
		PublisherID:  user.ID,
		Publisher:    user,
		TagName:      "v1.1.2",
		Target:       "master",
		Title:        "v1.1.2 is released",
		Note:         "v1.1.2 is released",
		IsDraft:      true,
		IsPrerelease: false,
		IsTag:        false,
	}
	require.NoError(t, CreateRelease(gitRepo, release, "", []*AttachmentChange{}))
	assert.Positive(t, release.ID)

	release.IsDraft = false
	tagName := release.TagName

	require.NoError(t, UpdateRelease(db.DefaultContext, user, gitRepo, release, false, []*AttachmentChange{}))
	release, err = repo_model.GetReleaseByID(db.DefaultContext, release.ID)
	require.NoError(t, err)
	assert.Equal(t, tagName, release.TagName)

	// Add new attachments
	samplePayload := "testtest"
	attach, err := attachment.NewAttachment(db.DefaultContext, &repo_model.Attachment{
		RepoID:     repo.ID,
		UploaderID: user.ID,
		Name:       "test.txt",
	}, strings.NewReader(samplePayload), int64(len([]byte(samplePayload))))
	require.NoError(t, err)

	require.NoError(t, UpdateRelease(db.DefaultContext, user, gitRepo, release, false, []*AttachmentChange{
		{
			Action: "add",
			Type:   "attachment",
			UUID:   attach.UUID,
		},
	}))
	require.NoError(t, repo_model.GetReleaseAttachments(db.DefaultContext, release))
	assert.Len(t, release.Attachments, 1)
	assert.Equal(t, attach.UUID, release.Attachments[0].UUID)
	assert.Equal(t, release.ID, release.Attachments[0].ReleaseID)
	assert.Equal(t, attach.Name, release.Attachments[0].Name)
	assert.Equal(t, attach.ExternalURL, release.Attachments[0].ExternalURL)

	// update the attachment name
	require.NoError(t, UpdateRelease(db.DefaultContext, user, gitRepo, release, false, []*AttachmentChange{
		{
			Action: "update",
			Name:   "test2.txt",
			UUID:   attach.UUID,
		},
	}))
	release.Attachments = nil
	require.NoError(t, repo_model.GetReleaseAttachments(db.DefaultContext, release))
	assert.Len(t, release.Attachments, 1)
	assert.Equal(t, attach.UUID, release.Attachments[0].UUID)
	assert.Equal(t, release.ID, release.Attachments[0].ReleaseID)
	assert.Equal(t, "test2.txt", release.Attachments[0].Name)
	assert.Equal(t, attach.ExternalURL, release.Attachments[0].ExternalURL)

	// delete the attachment
	require.NoError(t, UpdateRelease(db.DefaultContext, user, gitRepo, release, false, []*AttachmentChange{
		{
			Action: "delete",
			UUID:   attach.UUID,
		},
	}))
	release.Attachments = nil
	assert.NoError(t, repo_model.GetReleaseAttachments(db.DefaultContext, release))
	assert.Empty(t, release.Attachments)

	// Add new external attachment
	assert.NoError(t, UpdateRelease(db.DefaultContext, user, gitRepo, release, false, []*AttachmentChange{
		{
			Action:      "add",
			Type:        "external",
			Name:        "test",
			ExternalURL: "https://forgejo.org/",
		},
	}))
	assert.NoError(t, repo_model.GetReleaseAttachments(db.DefaultContext, release))
	assert.Len(t, release.Attachments, 1)
	assert.Equal(t, release.ID, release.Attachments[0].ReleaseID)
	assert.Equal(t, "test", release.Attachments[0].Name)
	assert.Equal(t, "https://forgejo.org/", release.Attachments[0].ExternalURL)
	externalAttachmentUUID := release.Attachments[0].UUID

	// update the attachment name
	assert.NoError(t, UpdateRelease(db.DefaultContext, user, gitRepo, release, false, []*AttachmentChange{
		{
			Action:      "update",
			Name:        "test2",
			UUID:        externalAttachmentUUID,
			ExternalURL: "https://about.gitea.com/",
		},
	}))
	release.Attachments = nil
	assert.NoError(t, repo_model.GetReleaseAttachments(db.DefaultContext, release))
	assert.Len(t, release.Attachments, 1)
	assert.Equal(t, externalAttachmentUUID, release.Attachments[0].UUID)
	assert.Equal(t, release.ID, release.Attachments[0].ReleaseID)
	assert.Equal(t, "test2", release.Attachments[0].Name)
	assert.Equal(t, "https://about.gitea.com/", release.Attachments[0].ExternalURL)

	// delete the attachment
	require.NoError(t, UpdateRelease(db.DefaultContext, user, gitRepo, release, false, []*AttachmentChange{
		{
			Action: "delete",
			UUID:   externalAttachmentUUID,
		},
	}))
	release.Attachments = nil
	require.NoError(t, repo_model.GetReleaseAttachments(db.DefaultContext, release))
	assert.Empty(t, release.Attachments)

	t.Run("Permission denied", func(t *testing.T) {
		require.NoError(t, UpdateRelease(t.Context(), user, gitRepo, release, false, []*AttachmentChange{
			{
				Action: "add",
				Type:   "attachment",
				UUID:   "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a13",
			},
		}))
		require.NoError(t, repo_model.GetReleaseAttachments(t.Context(), release))
		assert.Empty(t, release.Attachments)

		require.NoError(t, UpdateRelease(t.Context(), user, gitRepo, release, false, []*AttachmentChange{
			{
				Action: "update",
				Name:   "test2.txt",
				UUID:   "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a13",
			},
		}))
		require.NoError(t, repo_model.GetReleaseAttachments(t.Context(), release))
		assert.Empty(t, release.Attachments)
	})

	t.Run("Delete and edit", func(t *testing.T) {
		// Add a attachment.
		attach, err := attachment.NewAttachment(t.Context(), &repo_model.Attachment{
			RepoID:     repo.ID,
			UploaderID: user.ID,
			Name:       "test.txt",
		}, strings.NewReader("beep"), 4)
		require.NoError(t, err)

		require.NoError(t, UpdateRelease(t.Context(), user, gitRepo, release, false, []*AttachmentChange{
			{
				Action: "add",
				Type:   "attachment",
				UUID:   attach.UUID,
			},
		}))
		require.NoError(t, repo_model.GetReleaseAttachments(t.Context(), release))
		assert.Len(t, release.Attachments, 1)

		// Delete and edit at the same time, this is identical how the browser would delete a attachment.
		require.NoError(t, UpdateRelease(t.Context(), user, gitRepo, release, false, []*AttachmentChange{
			{
				Action: "update",
				Name:   "test.txt",
				UUID:   attach.UUID,
			},
			{
				Action: "delete",
				UUID:   attach.UUID,
			},
		}))
		require.NoError(t, repo_model.GetReleaseAttachments(t.Context(), release))
		assert.Empty(t, release.Attachments)
	})
}

func TestRelease_createTag(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	gitRepo, err := gitrepo.OpenRepository(git.DefaultContext, repo)
	require.NoError(t, err)
	defer gitRepo.Close()

	// Test a changed release
	release := &repo_model.Release{
		RepoID:       repo.ID,
		Repo:         repo,
		PublisherID:  user.ID,
		Publisher:    user,
		TagName:      "v2.1.1",
		Target:       "master",
		Title:        "v2.1.1 is released",
		Note:         "v2.1.1 is released",
		IsDraft:      false,
		IsPrerelease: false,
		IsTag:        false,
	}
	_, err = createTag(db.DefaultContext, gitRepo, release, "")
	require.NoError(t, err)
	assert.NotEmpty(t, release.CreatedUnix)
	releaseCreatedUnix := release.CreatedUnix
	test.SleepTillNextSecond()
	release.Note = "Changed note"
	_, err = createTag(db.DefaultContext, gitRepo, release, "")
	require.NoError(t, err)
	assert.Equal(t, int64(releaseCreatedUnix), int64(release.CreatedUnix))

	// Test a changed draft
	release = &repo_model.Release{
		RepoID:       repo.ID,
		Repo:         repo,
		PublisherID:  user.ID,
		Publisher:    user,
		TagName:      "v2.2.1",
		Target:       "65f1bf2",
		Title:        "v2.2.1 is draft",
		Note:         "v2.2.1 is draft",
		IsDraft:      true,
		IsPrerelease: false,
		IsTag:        false,
	}
	_, err = createTag(db.DefaultContext, gitRepo, release, "")
	require.NoError(t, err)
	releaseCreatedUnix = release.CreatedUnix
	test.SleepTillNextSecond()
	release.Title = "Changed title"
	_, err = createTag(db.DefaultContext, gitRepo, release, "")
	require.NoError(t, err)
	assert.Less(t, int64(releaseCreatedUnix), int64(release.CreatedUnix))

	// Test a changed pre-release
	release = &repo_model.Release{
		RepoID:       repo.ID,
		Repo:         repo,
		PublisherID:  user.ID,
		Publisher:    user,
		TagName:      "v2.3.1",
		Target:       "65f1bf2",
		Title:        "v2.3.1 is pre-released",
		Note:         "v2.3.1 is pre-released",
		IsDraft:      false,
		IsPrerelease: true,
		IsTag:        false,
	}
	_, err = createTag(db.DefaultContext, gitRepo, release, "")
	require.NoError(t, err)
	releaseCreatedUnix = release.CreatedUnix
	test.SleepTillNextSecond()
	release.Title = "Changed title"
	release.Note = "Changed note"
	_, err = createTag(db.DefaultContext, gitRepo, release, "")
	require.NoError(t, err)
	assert.Equal(t, int64(releaseCreatedUnix), int64(release.CreatedUnix))
}

func TestCreateNewTag(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	require.NoError(t, CreateNewTag(git.DefaultContext, user, repo, "master", "v2.0",
		"v2.0 is released \n\n BUGFIX: .... \n\n 123"))
}

func TestRelease_delete(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	gitRepo, err := gitrepo.OpenRepository(git.DefaultContext, repo)
	require.NoError(t, err)
	defer gitRepo.Close()

	release := repo_model.Release{
		RepoID:       repo.ID,
		Repo:         repo,
		PublisherID:  user.ID,
		Publisher:    user,
		TagName:      "v1.1.1",
		Target:       "master",
		Title:        "v1.1.1 is released",
		Note:         "v1.1.1 is released",
		IsDraft:      false,
		IsPrerelease: false,
		IsTag:        false,
	}
	require.NoError(t, CreateRelease(gitRepo, &release, "", []*AttachmentChange{}))

	// create notifications for this release
	require.NoError(t, activities_model.CreateOrUpdateReleaseNotifications(db.DefaultContext, release.ID))

	// user1 has a notification for the release
	notif, found, err := activities_model.GetReleaseNotification(t.Context(), 1, release.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, activities_model.NotificationStatusUnread, notif.Status)

	// delete the release
	require.NoError(t, DeleteReleaseByID(t.Context(), repo, &release, user, false))

	// user1 no longer has a notification for the release
	notif, found, err = activities_model.GetReleaseNotification(t.Context(), 1, release.ID)
	require.NoError(t, err)
	require.False(t, found)
	assert.Nil(t, notif)
}
