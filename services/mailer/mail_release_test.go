// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package mailer_test

import (
	"testing"

	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/services/mailer"
	notify_service "forgejo.org/services/notify"
	release_service "forgejo.org/services/release"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMailNewRelease(t *testing.T) {
	defer unittest.OverrideFixtures("services/mailer/fixtures/TestMailNewRelease")()
	defer require.NoError(t, unittest.PrepareTestDatabase())

	user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	user11 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 11})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	gitRepo, err := gitrepo.OpenRepository(t.Context(), repo)
	require.NoError(t, err)
	defer gitRepo.Close()

	t.Run("Normal", func(t *testing.T) {
		called := false

		defer mailer.MockMailSettings(func(msgs ...*mailer.Message) {
			assert.Len(t, msgs, 2)
			if user1.EmailTo() == msgs[0].To {
				assert.Equal(t, user11.EmailTo(), msgs[1].To)
			} else {
				assert.Equal(t, user11.EmailTo(), msgs[0].To)
				assert.Equal(t, user1.EmailTo(), msgs[1].To)
			}

			assert.Equal(t, "v0.1 in user2/repo1 released", msgs[0].Subject)
			called = true
		})()

		notifier := mailer.NewNotifier()
		notify_service.RegisterNotifier(notifier)
		defer notify_service.UnregisterNotifier(notifier)

		require.NoError(t, release_service.CreateRelease(gitRepo, &repo_model.Release{
			RepoID:      repo.ID,
			Repo:        repo,
			PublisherID: user2.ID,
			Publisher:   user2,
			TagName:     "v0.1",
			Target:      "master",
			Title:       "v0.1 is released",
			Note:        "v0.1 is released",
		}, "", []*release_service.AttachmentChange{}))

		assert.True(t, called)
	})

	t.Run("Non-active user", func(t *testing.T) {
		_, err := db.GetEngine(db.DefaultContext).Exec("UPDATE user SET is_active=false WHERE id=11")
		require.NoError(t, err)

		t.Cleanup(func() {
			_, err := db.GetEngine(db.DefaultContext).Exec("UPDATE user SET is_active=true WHERE id=11")
			require.NoError(t, err)
		})
		called := false

		defer mailer.MockMailSettings(func(msgs ...*mailer.Message) {
			assert.Len(t, msgs, 1)
			assert.Equal(t, user1.EmailTo(), msgs[0].To)

			assert.Equal(t, "v0.2 in user2/repo1 released", msgs[0].Subject)
			called = true
		})()

		notifier := mailer.NewNotifier()
		notify_service.RegisterNotifier(notifier)
		defer notify_service.UnregisterNotifier(notifier)

		require.NoError(t, release_service.CreateRelease(gitRepo, &repo_model.Release{
			RepoID:      repo.ID,
			Repo:        repo,
			PublisherID: user2.ID,
			Publisher:   user2,
			TagName:     "v0.2",
			Target:      "master",
			Title:       "v0.2 is released",
			Note:        "v0.2 is released",
		}, "", []*release_service.AttachmentChange{}))

		assert.True(t, called)
	})

	t.Run("No permissions for releases", func(t *testing.T) {
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 41})

		gitRepo, err := gitrepo.OpenRepository(t.Context(), repo)
		require.NoError(t, err)
		defer gitRepo.Close()

		called := false

		defer mailer.MockMailSettings(func(msgs ...*mailer.Message) {
			called = true
		})()

		notifier := mailer.NewNotifier()
		notify_service.RegisterNotifier(notifier)
		defer notify_service.UnregisterNotifier(notifier)

		require.NoError(t, release_service.CreateRelease(gitRepo, &repo_model.Release{
			RepoID:      repo.ID,
			Repo:        repo,
			PublisherID: user2.ID,
			Publisher:   user2,
			TagName:     "v0.2",
			Target:      "master",
			Title:       "v0.2 is released",
			Note:        "v0.2 is released",
		}, "", []*release_service.AttachmentChange{}))

		assert.False(t, called)
	})
}
