// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"forgejo.org/models/db"
	git_model "forgejo.org/models/git"
	org_model "forgejo.org/models/organization"
	quota_model "forgejo.org/models/quota"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/lfs"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	"forgejo.org/routers"
	app_context "forgejo.org/services/context"
	repo_service "forgejo.org/services/repository"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebQuotaEnforcementRepoMigrate(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		env := createQuotaWebEnv(t)
		defer env.Cleanup()

		env.RunVisitAndPostToPageTests(t, "/repo/migrate", &Payload{
			"repo_name":  "migration-test",
			"clone_addr": env.Users.Limited.Repo.Link() + ".git",
			"service":    fmt.Sprintf("%d", api.ForgejoService),
		}, http.StatusOK)
	})
}

func TestWebQuotaEnforcementRepoCreate(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		env := createQuotaWebEnv(t)
		defer env.Cleanup()

		env.RunVisitAndPostToPageTests(t, "/repo/create", nil, http.StatusOK)
	})
}

func TestWebQuotaEnforcementRepoFork(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		env := createQuotaWebEnv(t)
		defer env.Cleanup()

		page := fmt.Sprintf("%s/fork", env.Users.Limited.Repo.Link())
		env.RunVisitAndPostToPageTests(t, page, &Payload{
			"repo_name": "fork-test",
		}, http.StatusSeeOther)
	})
}

func TestWebQuotaEnforcementIssueAttachment(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		env := createQuotaWebEnv(t)
		defer env.Cleanup()

		// Uploading to our repo => 413
		env.As(t, env.Users.Limited).
			With(Context{Repo: env.Users.Limited.Repo}).
			CreateIssueAttachment("test.txt").
			ExpectStatus(http.StatusRequestEntityTooLarge)

		// Uploading to the limited org repo => 413
		env.As(t, env.Users.Limited).
			With(Context{Repo: env.Orgs.Limited.Repo}).
			CreateIssueAttachment("test.txt").
			ExpectStatus(http.StatusRequestEntityTooLarge)

		// Uploading to the unlimited org repo => 200
		env.As(t, env.Users.Limited).
			With(Context{Repo: env.Orgs.Unlimited.Repo}).
			CreateIssueAttachment("test.txt").
			ExpectStatus(http.StatusOK)
	})
}

func TestWebQuotaEnforcementMirrorSync(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		env := createQuotaWebEnv(t)
		defer env.Cleanup()

		var mirrorRepo *repo_model.Repository

		env.As(t, env.Users.Limited).
			WithoutQuota(func(ctx *quotaWebEnvAsContext) {
				mirrorRepo = ctx.CreateMirror()
			}).
			With(Context{
				Repo:    mirrorRepo,
				Payload: &Payload{"action": "mirror-sync"},
			}).
			PostToPage(mirrorRepo.Link() + "/settings").
			ExpectStatus(http.StatusOK).
			ExpectFlashMessage("Quota exceeded, not pulling changes.")
	})
}

func TestWebQuotaEnforcementRepoContentEditing(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		env := createQuotaWebEnv(t)
		defer env.Cleanup()

		// We're only going to test the GET requests here, because the entire combo
		// is covered by a route check.

		// Lets create a helper!
		runCheck := func(t *testing.T, path string, successStatus int) {
			t.Run("#"+path, func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				// Uploading to a limited user's repo => 413
				env.As(t, env.Users.Limited).
					VisitPage(env.Users.Limited.Repo.Link() + path).
					ExpectStatus(http.StatusRequestEntityTooLarge)

				// Limited org => 413
				env.As(t, env.Users.Limited).
					VisitPage(env.Orgs.Limited.Repo.Link() + path).
					ExpectStatus(http.StatusRequestEntityTooLarge)

				// Unlimited org => 200
				env.As(t, env.Users.Limited).
					VisitPage(env.Orgs.Unlimited.Repo.Link() + path).
					ExpectStatus(successStatus)
			})
		}

		paths := []string{
			"/_new/main",
			"/_edit/main/README.md",
			"/_delete/main",
			"/_upload/main",
			"/_diffpatch/main",
		}

		for _, path := range paths {
			runCheck(t, path, http.StatusOK)
		}

		// Run another check for `_cherrypick`. It's cumbersome to dig out a valid
		// commit id, so we'll use a fake, and treat 404 as a success: it's not 413,
		// and that's all we care about for this test.
		runCheck(t, "/_cherrypick/92cfceb39d57d914ed8b14d0e37643de0797ae56/main", http.StatusNotFound)
	})
}

func TestWebQuotaEnforcementRepoBranches(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		env := createQuotaWebEnv(t)
		defer env.Cleanup()

		t.Run("create", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			runTest := func(t *testing.T, path string) {
				t.Run("#"+path, func(t *testing.T) {
					defer tests.PrintCurrentTest(t)()

					env.As(t, env.Users.Limited).
						With(Context{Payload: &Payload{"new_branch_name": "quota"}}).
						PostToRepoPage("/branches/_new" + path).
						ExpectStatus(http.StatusRequestEntityTooLarge)

					env.As(t, env.Users.Limited).
						With(Context{
							Payload: &Payload{"new_branch_name": "quota"},
							Repo:    env.Orgs.Limited.Repo,
						}).
						PostToRepoPage("/branches/_new" + path).
						ExpectStatus(http.StatusRequestEntityTooLarge)

					env.As(t, env.Users.Limited).
						With(Context{
							Payload: &Payload{"new_branch_name": "quota"},
							Repo:    env.Orgs.Unlimited.Repo,
						}).
						PostToRepoPage("/branches/_new" + path).
						ExpectStatus(http.StatusNotFound)
				})
			}

			// We're testing the first two against things that don't exist, so that
			// all three consistently return 404 if no quota enforcement happens.
			runTest(t, "/branch/no-such-branch")
			runTest(t, "/tag/no-such-tag")
			runTest(t, "/commit/92cfceb39d57d914ed8b14d0e37643de0797ae56")
		})

		t.Run("delete & restore", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			env.As(t, env.Users.Limited).
				WithoutQuota(func(ctx *quotaWebEnvAsContext) {
					ctx.With(Context{Payload: &Payload{"new_branch_name": "to-delete"}}).
						PostToRepoPage("/branches/_new/branch/main").
						ExpectStatus(http.StatusSeeOther)
				})

			env.As(t, env.Users.Limited).
				PostToRepoPage("/branches/delete?name=to-delete").
				ExpectStatus(http.StatusOK)

			env.As(t, env.Users.Limited).
				PostToRepoPage("/branches/restore?name=to-delete").
				ExpectStatus(http.StatusOK)
		})
	})
}

func TestWebQuotaEnforcementRepoReleases(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		env := createQuotaWebEnv(t)
		defer env.Cleanup()

		env.RunVisitAndPostToRepoPageTests(t, "/releases/new", &Payload{
			"tag_name":   "quota",
			"tag_target": "main",
			"title":      "test release",
		}, http.StatusSeeOther)

		t.Run("attachments", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Uploading to our repo => 413
			env.As(t, env.Users.Limited).
				With(Context{Repo: env.Users.Limited.Repo}).
				CreateReleaseAttachment("test.txt").
				ExpectStatus(http.StatusRequestEntityTooLarge)

			// Uploading to the limited org repo => 413
			env.As(t, env.Users.Limited).
				With(Context{Repo: env.Orgs.Limited.Repo}).
				CreateReleaseAttachment("test.txt").
				ExpectStatus(http.StatusRequestEntityTooLarge)

			// Uploading to the unlimited org repo => 200
			env.As(t, env.Users.Limited).
				With(Context{Repo: env.Orgs.Unlimited.Repo}).
				CreateReleaseAttachment("test.txt").
				ExpectStatus(http.StatusOK)
		})
	})
}

func TestWebQuotaEnforcementRepoPulls(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		env := createQuotaWebEnv(t)
		defer env.Cleanup()

		// To create a pull request, we first fork the two limited repos into the
		// unlimited org.
		env.As(t, env.Users.Limited).
			With(Context{Repo: env.Users.Limited.Repo}).
			ForkRepoInto(env.Orgs.Unlimited)
		env.As(t, env.Users.Limited).
			With(Context{Repo: env.Orgs.Limited.Repo}).
			ForkRepoInto(env.Orgs.Unlimited)

		// Then, create pull requests from the forks, back to the main repos
		env.As(t, env.Users.Limited).
			With(Context{Repo: env.Users.Limited.Repo}).
			CreatePullFrom(env.Orgs.Unlimited)
		env.As(t, env.Users.Limited).
			With(Context{Repo: env.Orgs.Limited.Repo}).
			CreatePullFrom(env.Orgs.Unlimited)

		// Trying to merge the pull request will fail for both, though, due to being
		// over quota.
		env.As(t, env.Users.Limited).
			With(Context{Repo: env.Users.Limited.Repo}).
			With(Context{Payload: &Payload{"do": "merge"}}).
			PostToRepoPage("/pulls/1/merge").
			ExpectStatus(http.StatusRequestEntityTooLarge)

		env.As(t, env.Users.Limited).
			With(Context{Repo: env.Orgs.Limited.Repo}).
			With(Context{Payload: &Payload{"do": "merge"}}).
			PostToRepoPage("/pulls/1/merge").
			ExpectStatus(http.StatusRequestEntityTooLarge)
	})
}

func TestWebQuotaEnforcementRepoTransfer(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		env := createQuotaWebEnv(t)
		defer env.Cleanup()

		t.Run("direct transfer", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Trying to transfer the repository to a limited organization fails.
			env.As(t, env.Users.Limited).
				With(Context{Repo: env.Users.Limited.Repo}).
				With(Context{Payload: &Payload{
					"action":         "transfer",
					"repo_name":      env.Users.Limited.Repo.FullName(),
					"new_owner_name": env.Orgs.Limited.Org.Name,
				}}).
				PostToRepoPage("/settings").
				ExpectStatus(http.StatusOK).
				ExpectFlashMessageContains("over quota", "The repository has not been transferred")

			// Trying to transfer to a different, also limited user, also fails.
			env.As(t, env.Users.Limited).
				With(Context{Repo: env.Users.Limited.Repo}).
				With(Context{Payload: &Payload{
					"action":         "transfer",
					"repo_name":      env.Users.Limited.Repo.FullName(),
					"new_owner_name": env.Users.Contributor.User.Name,
				}}).
				PostToRepoPage("/settings").
				ExpectStatus(http.StatusOK).
				ExpectFlashMessageContains("over quota", "The repository has not been transferred")
		})

		t.Run("accept & reject", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Trying to transfer to a different user, with quota lifted, starts the transfer
			env.As(t, env.Users.Contributor).
				WithoutQuota(func(ctx *quotaWebEnvAsContext) {
					env.As(ctx.t, env.Users.Limited).
						With(Context{Repo: env.Users.Limited.Repo}).
						With(Context{Payload: &Payload{
							"action":         "transfer",
							"repo_name":      env.Users.Limited.Repo.FullName(),
							"new_owner_name": env.Users.Contributor.User.Name,
						}}).
						PostToRepoPage("/settings").
						ExpectStatus(http.StatusSeeOther).
						ExpectFlashCookieContains("This repository has been marked for transfer and awaits confirmation")
				})

			// Trying to accept the transfer, with quota in effect, fails
			env.As(t, env.Users.Contributor).
				With(Context{Repo: env.Users.Limited.Repo}).
				PostToRepoPage("/action/accept_transfer").
				ExpectStatus(http.StatusRequestEntityTooLarge)

			// Rejecting the transfer, however, succeeds.
			env.As(t, env.Users.Contributor).
				With(Context{Repo: env.Users.Limited.Repo}).
				PostToRepoPage("/action/reject_transfer").
				ExpectStatus(http.StatusSeeOther)
		})
	})
}

func TestQuotaGitEnforcement(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		env := createQuotaWebEnv(t)
		defer env.Cleanup()

		// Lets create a little helper that runs a task for three of our repos: the
		// user's repo, the limited org repo, and the unlimited org's.
		//
		// We expect the last one to always work, and the expected status of the
		// other two is decided by the caller.
		runTestForAllRepos := func(t *testing.T, task func(t *testing.T, repo *repo_model.Repository) error, expectSuccess bool) {
			t.Helper()

			err := task(t, env.Users.Limited.Repo)
			if expectSuccess {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			err = task(t, env.Orgs.Limited.Repo)
			if expectSuccess {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			err = task(t, env.Orgs.Unlimited.Repo)
			require.NoError(t, err)
		}

		// Run tests with quotas disabled
		runTestForAllReposWithQuotaDisabled := func(t *testing.T, task func(t *testing.T, repo *repo_model.Repository) error) {
			t.Helper()

			t.Run("with quota disabled", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				defer test.MockVariableValue(&setting.Quota.Enabled, false)()
				defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()

				runTestForAllRepos(t, task, true)
			})
		}

		t.Run("push branch", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Pushing a new branch is denied if the user is over quota.
			runTestForAllRepos(t, func(t *testing.T, repo *repo_model.Repository) error {
				return env.As(t, env.Users.Limited).
					With(Context{Repo: repo}).
					LocalClone(u).
					Push("HEAD:new-branch")
			}, false)

			// Pushing a new branch is always allowed if quota is disabled
			runTestForAllReposWithQuotaDisabled(t, func(t *testing.T, repo *repo_model.Repository) error {
				return env.As(t, env.Users.Limited).
					With(Context{Repo: repo}).
					LocalClone(u).
					Push("HEAD:new-branch-wo-quota")
			})
		})

		t.Run("push tag", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Pushing a tag is denied if the user is over quota.
			runTestForAllRepos(t, func(t *testing.T, repo *repo_model.Repository) error {
				return env.As(t, env.Users.Limited).
					With(Context{Repo: repo}).
					LocalClone(u).
					Tag("new-tag").
					Push("new-tag")
			}, false)

			// ...but succeeds if the quota feature is disabled
			runTestForAllReposWithQuotaDisabled(t, func(t *testing.T, repo *repo_model.Repository) error {
				return env.As(t, env.Users.Limited).
					With(Context{Repo: repo}).
					LocalClone(u).
					Tag("new-tag-wo-quota").
					Push("new-tag-wo-quota")
			})
		})

		t.Run("Agit PR", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Opening an Agit PR is *always* accepted. At least for now.
			runTestForAllRepos(t, func(t *testing.T, repo *repo_model.Repository) error {
				return env.As(t, env.Users.Limited).
					With(Context{Repo: repo}).
					LocalClone(u).
					Push("HEAD:refs/for/main/agit-pr-branch")
			}, true)
		})

		t.Run("delete branch", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Deleting a branch is respected, and allowed.
			err := env.As(t, env.Users.Limited).
				WithoutQuota(func(ctx *quotaWebEnvAsContext) {
					err := ctx.
						LocalClone(u).
						Push("HEAD:branch-to-delete")
					require.NoError(ctx.t, err)
				}).
				Push(":branch-to-delete")
			require.NoError(t, err)
		})

		t.Run("delete tag", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Deleting a tag is always allowed.
			err := env.As(t, env.Users.Limited).
				WithoutQuota(func(ctx *quotaWebEnvAsContext) {
					err := ctx.
						LocalClone(u).
						Tag("tag-to-delete").
						Push("tag-to-delete")
					require.NoError(ctx.t, err)
				}).
				Push(":tag-to-delete")
			require.NoError(t, err)
		})

		t.Run("mixed push", func(t *testing.T) {
			t.Run("all deletes", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				// Pushing multiple deletes is allowed.
				err := env.As(t, env.Users.Limited).
					WithoutQuota(func(ctx *quotaWebEnvAsContext) {
						err := ctx.
							LocalClone(u).
							Tag("mixed-push-tag").
							Push("mixed-push-tag", "HEAD:mixed-push-branch")
						require.NoError(ctx.t, err)
					}).
					Push(":mixed-push-tag", ":mixed-push-branch")
				require.NoError(t, err)
			})

			t.Run("new & delete", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				// Pushing a mix of deletions & a new branch is rejected together.
				err := env.As(t, env.Users.Limited).
					WithoutQuota(func(ctx *quotaWebEnvAsContext) {
						err := ctx.
							LocalClone(u).
							Tag("mixed-push-tag").
							Push("mixed-push-tag", "HEAD:mixed-push-branch")
						require.NoError(ctx.t, err)
					}).
					Push(":mixed-push-tag", ":mixed-push-branch", "HEAD:mixed-push-branch-new")
				require.Error(t, err)

				// ...unless quota is disabled
				t.Run("with quota disabled", func(t *testing.T) {
					defer tests.PrintCurrentTest(t)()
					defer test.MockVariableValue(&setting.Quota.Enabled, false)()
					defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()

					err := env.As(t, env.Users.Limited).
						WithoutQuota(func(ctx *quotaWebEnvAsContext) {
							err := ctx.
								LocalClone(u).
								Tag("mixed-push-tag-2").
								Push("mixed-push-tag-2", "HEAD:mixed-push-branch-2")
							require.NoError(ctx.t, err)
						}).
						Push(":mixed-push-tag-2", ":mixed-push-branch-2", "HEAD:mixed-push-branch-new-2")
					require.NoError(t, err)
				})
			})
		})
	})
}

func TestQuotaGitLfsEnforcement(t *testing.T) {
	defer test.MockVariableValue(&setting.LFS.StartServer, true)()

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		env := createQuotaWebEnv(t)
		defer env.Cleanup()

		t.Run("UploadHandler", func(t *testing.T) {
			// Uploading to our repo => 413
			env.As(t, env.Users.Limited).
				With(Context{Repo: env.Users.Limited.Repo}).
				AuthBasic().
				PushLFSObject().
				ExpectStatus(http.StatusRequestEntityTooLarge)

			// Uploading to the limited org repo => 413
			env.As(t, env.Users.Limited).
				With(Context{Repo: env.Orgs.Limited.Repo}).
				AuthBasic().
				PushLFSObject().
				ExpectStatus(http.StatusRequestEntityTooLarge)

			// Uploading to the unlimited org repo => 200
			env.As(t, env.Users.Limited).
				With(Context{Repo: env.Orgs.Unlimited.Repo}).
				AuthBasic().
				PushLFSObject().
				ExpectStatus(http.StatusOK)
		})

		t.Run("BatchHandler", func(t *testing.T) {
			// Uploading to our repo => 413
			env.As(t, env.Users.Limited).
				With(Context{Repo: env.Users.Limited.Repo}).
				AuthBasic().
				BatchPushLFSObject().
				ExpectStatus(http.StatusRequestEntityTooLarge)

			// Uploading to the limited org repo => 413
			env.As(t, env.Users.Limited).
				With(Context{Repo: env.Orgs.Limited.Repo}).
				AuthBasic().
				BatchPushLFSObject().
				ExpectStatus(http.StatusRequestEntityTooLarge)

			// Uploading to the unlimited org repo => 200
			env.As(t, env.Users.Limited).
				With(Context{Repo: env.Orgs.Unlimited.Repo}).
				AuthBasic().
				BatchPushLFSObject().
				ExpectStatus(http.StatusOK)
		})
	})
}

func TestQuotaConfigDefault(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		env := createQuotaWebEnv(t)
		defer env.Cleanup()

		t.Run("with config-based default", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			defer test.MockVariableValue(&setting.Quota.Default.Total, 0)()

			env.As(t, env.Users.Ungrouped).
				With(Context{
					Payload: &Payload{
						"uid":       env.Users.Ungrouped.ID().AsString(),
						"repo_name": "quota-config-default",
					},
				}).
				PostToPage("/repo/create").
				ExpectStatus(http.StatusRequestEntityTooLarge)
		})

		t.Run("without config-based default", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			env.As(t, env.Users.Ungrouped).
				With(Context{
					Payload: &Payload{
						"uid":       env.Users.Ungrouped.ID().AsString(),
						"repo_name": "quota-config-default",
					},
				}).
				PostToPage("/repo/create").
				ExpectStatus(http.StatusSeeOther)
		})
	})
}

/**********************
 * Here be dragons!   *
 *                    *
 *      .             *
 *  .>   )\;`a__      *
 * (  _ _)/ /-." ~~   *
 *  `( )_ )/          *
 *  <_  <_ sb/dwb     *
 **********************/

type quotaWebEnv struct {
	Users quotaWebEnvUsers
	Orgs  quotaWebEnvOrgs

	cleaners []func()
}

type quotaWebEnvUsers struct {
	Limited     quotaWebEnvUser
	Contributor quotaWebEnvUser
	Ungrouped   quotaWebEnvUser
}

type quotaWebEnvOrgs struct {
	Limited   quotaWebEnvOrg
	Unlimited quotaWebEnvOrg
}

type quotaWebEnvOrg struct {
	Org *org_model.Organization

	Repo *repo_model.Repository

	QuotaGroup *quota_model.Group
	QuotaRule  *quota_model.Rule
}

type quotaWebEnvUser struct {
	User    *user_model.User
	Session *TestSession
	Repo    *repo_model.Repository

	QuotaGroup *quota_model.Group
	QuotaRule  *quota_model.Rule
}

type Payload map[string]string

type quotaWebEnvAsContext struct {
	t *testing.T

	Doer *quotaWebEnvUser
	Repo *repo_model.Repository

	Payload Payload

	gitPath string

	request   *RequestWrapper
	response  *httptest.ResponseRecorder
	authBasic bool
}

type Context struct {
	Repo    *repo_model.Repository
	Payload *Payload
}

func (ctx *quotaWebEnvAsContext) With(opts Context) *quotaWebEnvAsContext {
	if opts.Repo != nil {
		ctx.Repo = opts.Repo
	}
	if opts.Payload != nil {
		maps.Copy(ctx.Payload, *opts.Payload)
	}
	return ctx
}

func (ctx *quotaWebEnvAsContext) VisitPage(page string) *quotaWebEnvAsContext {
	ctx.t.Helper()

	ctx.request = NewRequest(ctx.t, "GET", page)

	return ctx
}

func (ctx *quotaWebEnvAsContext) VisitRepoPage(page string) *quotaWebEnvAsContext {
	ctx.t.Helper()

	return ctx.VisitPage(ctx.Repo.Link() + page)
}

func (ctx *quotaWebEnvAsContext) AuthBasic() *quotaWebEnvAsContext {
	ctx.t.Helper()
	ctx.authBasic = true
	return ctx
}

func (ctx *quotaWebEnvAsContext) ExpectStatus(status int) *quotaWebEnvAsContext {
	ctx.t.Helper()

	if ctx.authBasic {
		ctx.response = MakeRequest(ctx.t, ctx.request.AddBasicAuth(ctx.Doer.User.Name), status)
	} else {
		ctx.response = ctx.Doer.Session.MakeRequest(ctx.t, ctx.request, status)
	}

	return ctx
}

func (ctx *quotaWebEnvAsContext) ExpectFlashMessage(value string) {
	ctx.t.Helper()

	htmlDoc := NewHTMLParser(ctx.t, ctx.response.Body)
	flashMessage := strings.TrimSpace(htmlDoc.Find(`.flash-message`).Text())

	assert.Equal(ctx.t, value, flashMessage)
}

func (ctx *quotaWebEnvAsContext) ExpectFlashMessageContains(parts ...string) {
	ctx.t.Helper()

	htmlDoc := NewHTMLParser(ctx.t, ctx.response.Body)
	flashMessage := strings.TrimSpace(htmlDoc.Find(`.flash-message`).Text())

	for _, part := range parts {
		assert.Contains(ctx.t, flashMessage, part)
	}
}

func (ctx *quotaWebEnvAsContext) ExpectFlashCookieContains(parts ...string) {
	ctx.t.Helper()

	flashCookie := ctx.Doer.Session.GetCookie(app_context.CookieNameFlash)
	assert.NotNil(ctx.t, flashCookie)

	// Need to decode the cookie twice
	flashValue, err := url.QueryUnescape(flashCookie.Value)
	require.NoError(ctx.t, err)
	flashValue, err = url.QueryUnescape(flashValue)
	require.NoError(ctx.t, err)

	for _, part := range parts {
		assert.Contains(ctx.t, flashValue, part)
	}
}

func (ctx *quotaWebEnvAsContext) ForkRepoInto(org quotaWebEnvOrg) {
	ctx.t.Helper()

	ctx.
		With(Context{Payload: &Payload{
			"uid":       org.ID().AsString(),
			"repo_name": ctx.Repo.Name + "-fork",
		}}).
		PostToRepoPage("/fork").
		ExpectStatus(http.StatusSeeOther)
}

func (ctx *quotaWebEnvAsContext) CreatePullFrom(org quotaWebEnvOrg) {
	ctx.t.Helper()

	url := fmt.Sprintf("/compare/main...%s:main", org.Org.Name)
	ctx.
		With(Context{Payload: &Payload{
			"title": "PR test",
		}}).
		PostToRepoPage(url).
		ExpectStatus(http.StatusOK)
}

func (ctx *quotaWebEnvAsContext) PostToPage(page string) *quotaWebEnvAsContext {
	ctx.t.Helper()

	payload := ctx.Payload

	ctx.request = NewRequestWithValues(ctx.t, "POST", page, payload)

	return ctx
}

func (ctx *quotaWebEnvAsContext) PostToRepoPage(page string) *quotaWebEnvAsContext {
	ctx.t.Helper()

	return ctx.PostToPage(ctx.Repo.Link() + page)
}

func (ctx *quotaWebEnvAsContext) CreateAttachment(filename, attachmentType string) *quotaWebEnvAsContext {
	ctx.t.Helper()

	body := &bytes.Buffer{}
	image := generateImg()

	// Setup multi-part
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	require.NoError(ctx.t, err)
	_, err = io.Copy(part, &image)
	require.NoError(ctx.t, err)
	err = writer.Close()
	require.NoError(ctx.t, err)

	ctx.request = NewRequestWithBody(ctx.t, "POST", fmt.Sprintf("%s/%s/attachments", ctx.Repo.Link(), attachmentType), body)
	ctx.request.Header.Add("Content-Type", writer.FormDataContentType())

	return ctx
}

func (ctx *quotaWebEnvAsContext) CreateIssueAttachment(filename string) *quotaWebEnvAsContext {
	ctx.t.Helper()

	return ctx.CreateAttachment(filename, "issues")
}

func (ctx *quotaWebEnvAsContext) CreateReleaseAttachment(filename string) *quotaWebEnvAsContext {
	ctx.t.Helper()

	return ctx.CreateAttachment(filename, "releases")
}

func (ctx *quotaWebEnvAsContext) PushLFSObject() *quotaWebEnvAsContext {
	ctx.t.Helper()

	p := lfs.Pointer{Oid: "6ccce4863b70f258d691f59609d31b4502e1ba5199942d3bc5d35d17a4ce771d", Size: 5}
	ctx.request = NewRequestWithBody(ctx.t, "PUT",
		fmt.Sprintf("%s.git/info/lfs/objects/%s/%d",
			ctx.Repo.Link(), p.Oid, p.Size), strings.NewReader("gitea"))

	ctx.t.Cleanup(func() {
		git_model.RemoveLFSMetaObjectByOid(db.DefaultContext, ctx.Repo.ID, p.Oid)
	})

	return ctx
}

func (ctx *quotaWebEnvAsContext) BatchPushLFSObject() *quotaWebEnvAsContext {
	ctx.t.Helper()

	batch := &lfs.BatchRequest{
		Operation: "upload",
		Objects: []lfs.Pointer{
			{Oid: "d6f175817f886ec6fbbc1515326465fa96c3bfd54a4ea06cfd6dbbd8340e0153", Size: 1},
		},
	}
	ctx.request = NewRequestWithJSON(ctx.t, "POST",
		fmt.Sprintf("%s.git/info/lfs/objects/batch", ctx.Repo.Link()), batch).
		SetHeader("Accept", lfs.AcceptHeader).
		SetHeader("Content-Type", lfs.MediaType)

	ctx.t.Cleanup(func() {
		git_model.RemoveLFSMetaObjectByOid(db.DefaultContext, ctx.Repo.ID, batch.Objects[0].Oid)
	})

	return ctx
}

func (ctx *quotaWebEnvAsContext) WithoutQuota(task func(ctx *quotaWebEnvAsContext)) *quotaWebEnvAsContext {
	ctx.t.Helper()

	defer ctx.Doer.SetQuota(-1)()
	task(ctx)

	return ctx
}

func (ctx *quotaWebEnvAsContext) CreateMirror() *repo_model.Repository {
	ctx.t.Helper()

	doer := ctx.Doer.User

	repo, err := repo_service.CreateRepositoryDirectly(db.DefaultContext, doer, doer, repo_service.CreateRepoOptions{
		Name:     "test-mirror",
		IsMirror: true,
		Status:   repo_model.RepositoryBeingMigrated,
	})
	require.NoError(ctx.t, err)

	return repo
}

func (ctx *quotaWebEnvAsContext) LocalClone(u *url.URL) *quotaWebEnvAsContext {
	ctx.t.Helper()

	gitPath := ctx.t.TempDir()

	doGitInitTestRepository(gitPath, git.Sha1ObjectFormat)(ctx.t)

	oldPath := u.Path
	oldUser := u.User
	defer func() {
		u.Path = oldPath
		u.User = oldUser
	}()
	u.Path = ctx.Repo.FullName() + ".git"
	u.User = url.UserPassword(ctx.Doer.User.LowerName, userPassword)

	doGitAddRemote(gitPath, "origin", u)(ctx.t)

	ctx.gitPath = gitPath

	return ctx
}

func (ctx *quotaWebEnvAsContext) Push(params ...string) error {
	ctx.t.Helper()

	gitRepo, err := git.OpenRepository(git.DefaultContext, ctx.gitPath)
	require.NoError(ctx.t, err)
	defer gitRepo.Close()

	_, _, err = git.NewCommand(git.DefaultContext, "push", "origin").
		AddArguments(git.ToTrustedCmdArgs(params)...).
		RunStdString(&git.RunOpts{Dir: ctx.gitPath})

	return err
}

func (ctx *quotaWebEnvAsContext) Tag(tagName string) *quotaWebEnvAsContext {
	ctx.t.Helper()

	gitRepo, err := git.OpenRepository(git.DefaultContext, ctx.gitPath)
	require.NoError(ctx.t, err)
	defer gitRepo.Close()

	_, _, err = git.NewCommand(git.DefaultContext, "tag").
		AddArguments(git.ToTrustedCmdArgs([]string{tagName})...).
		RunStdString(&git.RunOpts{Dir: ctx.gitPath})
	require.NoError(ctx.t, err)

	return ctx
}

func (user *quotaWebEnvUser) SetQuota(limit int64) func() {
	previousLimit := user.QuotaRule.Limit

	user.QuotaRule.Limit = limit
	user.QuotaRule.Edit(db.DefaultContext, &limit, nil)

	return func() {
		user.QuotaRule.Limit = previousLimit
		user.QuotaRule.Edit(db.DefaultContext, &previousLimit, nil)
	}
}

func (user *quotaWebEnvUser) ID() convertAs {
	return convertAs{
		asString: fmt.Sprintf("%d", user.User.ID),
	}
}

func (org *quotaWebEnvOrg) ID() convertAs {
	return convertAs{
		asString: fmt.Sprintf("%d", org.Org.ID),
	}
}

type convertAs struct {
	asString string
}

func (cas convertAs) AsString() string {
	return cas.asString
}

func (env *quotaWebEnv) Cleanup() {
	for _, cleaner := range slices.Backward(env.cleaners) {
		cleaner()
	}
}

func (env *quotaWebEnv) As(t *testing.T, user quotaWebEnvUser) *quotaWebEnvAsContext {
	t.Helper()

	ctx := quotaWebEnvAsContext{
		t:    t,
		Doer: &user,
		Repo: user.Repo,

		Payload: Payload{},
	}
	return &ctx
}

func (env *quotaWebEnv) RunVisitAndPostToRepoPageTests(t *testing.T, page string, payload *Payload, successStatus int) {
	t.Helper()

	// Visiting the user's repo page fails due to being over quota.
	env.As(t, env.Users.Limited).
		With(Context{Repo: env.Users.Limited.Repo}).
		VisitRepoPage(page).
		ExpectStatus(http.StatusRequestEntityTooLarge)

	// Posting as the limited user, to the limited repo, fails due to being over
	// quota.
	env.As(t, env.Users.Limited).
		With(Context{
			Payload: payload,
			Repo:    env.Users.Limited.Repo,
		}).
		PostToRepoPage(page).
		ExpectStatus(http.StatusRequestEntityTooLarge)

	// Visiting the limited org's repo page fails due to being over quota.
	env.As(t, env.Users.Limited).
		With(Context{Repo: env.Orgs.Limited.Repo}).
		VisitRepoPage(page).
		ExpectStatus(http.StatusRequestEntityTooLarge)

	// Posting as the limited user, to a limited org's repo, fails for the same
	// reason.
	env.As(t, env.Users.Limited).
		With(Context{
			Payload: payload,
			Repo:    env.Orgs.Limited.Repo,
		}).
		PostToRepoPage(page).
		ExpectStatus(http.StatusRequestEntityTooLarge)

	// Visiting the repo page for the unlimited org succeeds.
	env.As(t, env.Users.Limited).
		With(Context{Repo: env.Orgs.Unlimited.Repo}).
		VisitRepoPage(page).
		ExpectStatus(http.StatusOK)

	// Posting as the limited user, to an unlimited org's repo, succeeds.
	env.As(t, env.Users.Limited).
		With(Context{
			Payload: payload,
			Repo:    env.Orgs.Unlimited.Repo,
		}).
		PostToRepoPage(page).
		ExpectStatus(successStatus)
}

func (env *quotaWebEnv) RunVisitAndPostToPageTests(t *testing.T, page string, payload *Payload, successStatus int) {
	t.Helper()

	// Visiting the page is always fine.
	env.As(t, env.Users.Limited).
		VisitPage(page).
		ExpectStatus(http.StatusOK)

	// Posting as the Limited user fails, because it is over quota.
	env.As(t, env.Users.Limited).
		With(Context{Payload: payload}).
		With(Context{
			Payload: &Payload{
				"uid": env.Users.Limited.ID().AsString(),
			},
		}).
		PostToPage(page).
		ExpectStatus(http.StatusRequestEntityTooLarge)

	// Posting to a limited org also fails, for the same reason.
	env.As(t, env.Users.Limited).
		With(Context{Payload: payload}).
		With(Context{
			Payload: &Payload{
				"uid": env.Orgs.Limited.ID().AsString(),
			},
		}).
		PostToPage(page).
		ExpectStatus(http.StatusRequestEntityTooLarge)

	// Posting to an unlimited repo works, however.
	env.As(t, env.Users.Limited).
		With(Context{Payload: payload}).
		With(Context{
			Payload: &Payload{
				"uid": env.Orgs.Unlimited.ID().AsString(),
			},
		}).
		PostToPage(page).
		ExpectStatus(successStatus)
}

func createQuotaWebEnv(t *testing.T) *quotaWebEnv {
	t.Helper()

	// *** helpers ***

	makeUngroupedUser := func(t *testing.T) quotaWebEnvUser {
		t.Helper()

		user := quotaWebEnvUser{}

		// Create the user
		user.User = forgery.CreateUser(t, nil)
		user.Session = loginUser(t, user.User.Name)

		// Create a repository for the user
		repo := forgery.CreateRepository(t, user.User, &forgery.CreateRepositoryOptions{
			Files: forgery.FilesInit{},
		})
		user.Repo = repo

		return user
	}

	// Create a user, its quota group & rule
	makeUser := func(t *testing.T, limit int64) quotaWebEnvUser {
		t.Helper()

		user := makeUngroupedUser(t)
		userName := user.User.Name

		// Create a quota group for them
		group, err := quota_model.CreateGroup(db.DefaultContext, userName)
		require.NoError(t, err)
		user.QuotaGroup = group

		// Create a rule
		rule, err := quota_model.CreateRule(db.DefaultContext, userName, limit, quota_model.LimitSubjects{quota_model.LimitSubjectSizeAll})
		require.NoError(t, err)
		user.QuotaRule = rule

		// Add the rule to the group
		err = group.AddRuleByName(db.DefaultContext, rule.Name)
		require.NoError(t, err)

		// Add the user to the group
		err = group.AddUserByID(db.DefaultContext, user.User.ID)
		require.NoError(t, err)

		return user
	}

	// Create a user, its quota group & rule
	makeOrg := func(t *testing.T, owner *user_model.User, limit int64) quotaWebEnvOrg {
		t.Helper()

		org := quotaWebEnvOrg{}

		// Create the org
		org.Org = forgery.CreateOrganisation(t, owner, nil)

		// Create a repository for the org
		repo := forgery.CreateRepository(t, org.Org.AsUser(), &forgery.CreateRepositoryOptions{
			Files: forgery.FilesInit{},
		})
		org.Repo = repo

		// Create a quota group for them
		group, err := quota_model.CreateGroup(db.DefaultContext, org.Org.Name)
		require.NoError(t, err)
		org.QuotaGroup = group

		// Create a rule
		rule, err := quota_model.CreateRule(db.DefaultContext, org.Org.Name, limit, quota_model.LimitSubjects{quota_model.LimitSubjectSizeAll})
		require.NoError(t, err)
		org.QuotaRule = rule

		// Add the rule to the group
		err = group.AddRuleByName(db.DefaultContext, rule.Name)
		require.NoError(t, err)

		// Add the org to the group
		err = group.AddUserByID(db.DefaultContext, org.Org.ID)
		require.NoError(t, err)

		return org
	}

	env := quotaWebEnv{}
	env.cleaners = []func(){
		test.MockVariableValue(&setting.Quota.Enabled, true),
		test.MockVariableValue(&testWebRoutes, routers.NormalRoutes()),
	}

	// Create the limited user and the various orgs, and a contributor who's not
	// in any of the orgs.
	env.Users.Limited = makeUser(t, int64(0))
	env.Users.Contributor = makeUser(t, int64(0))
	env.Orgs.Limited = makeOrg(t, env.Users.Limited.User, int64(0))
	env.Orgs.Unlimited = makeOrg(t, env.Users.Limited.User, int64(-1))

	env.Users.Ungrouped = makeUngroupedUser(t)

	return &env
}
