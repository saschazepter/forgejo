// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// This is primarily coped from /tests/integration/integration_test.go
//   TODO: Move common functions to shared file

//nolint:forbidigo
package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"forgejo.org/models/unittest"
	"forgejo.org/modules/graceful"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/modules/testlogger"
	"forgejo.org/modules/util"
	"forgejo.org/modules/web"
	"forgejo.org/routers"
	"forgejo.org/tests"
	"forgejo.org/tests/internaltest"
)

var testE2eWebRoutes *web.Route

func TestMain(m *testing.M) {
	tests.DelegateToMainApp()

	defer log.GetManager().Close()

	managerCtx, cancel := context.WithCancel(context.Background())
	graceful.InitManager(managerCtx)
	defer cancel()

	tests.InitTest()
	initChangedFiles()

	fixtureOptions := unittest.FixturesOptions{
		Dir:  filepath.Join(setting.AppWorkPath, "models/fixtures/"),
		Base: setting.AppWorkPath,
		Dirs: []string{"tests/e2e/fixtures/"},
	}

	err := unittest.InitFixtures(fixtureOptions)
	if err != nil {
		fmt.Printf("Error initializing test database: %v\n", err)
		os.Exit(1)
	}

	testE2eWebRoutes = routers.NormalRoutes()
	testE2eWebRoutes.R.Patch("/_e2e/fixtures/reload", func(_ http.ResponseWriter, _ *http.Request) {
		err := unittest.LoadFixtures()
		if err != nil {
			fmt.Printf("Error reloading fixtures: %s\n", err)
			os.Exit(1)
		}
	})

	exitVal := m.Run()

	if err := testlogger.WriterCloser.Reset(); err != nil {
		fmt.Printf("testlogger.WriterCloser.Reset: error ignored: %v\n", err)
	}
	if err = util.RemoveAll(setting.Indexer.IssuePath); err != nil {
		fmt.Printf("util.RemoveAll: %v\n", err)
		os.Exit(1)
	}
	if err = util.RemoveAll(setting.Indexer.RepoPath); err != nil {
		fmt.Printf("Unable to remove repo indexer: %v\n", err)
		os.Exit(1)
	}

	os.Exit(exitVal)
}

// TestE2e should be the only test e2e necessary. It will collect all "*.test.e2e.ts" files in this directory and build a test for each.
func TestE2e(t *testing.T) {
	internaltest.NewInternalTestServer(t, nil)

	// Find the paths of all e2e test files in test directory.
	searchGlob := filepath.Join(setting.AppWorkPath, "tests", "e2e", "*.test.e2e.ts")
	paths, err := filepath.Glob(searchGlob)
	if err != nil {
		t.Fatal(err)
	} else if len(paths) == 0 {
		t.Fatal(fmt.Errorf("No e2e tests found in %s", searchGlob))
	}

	runArgs := []string{"npx", "playwright", "test"}

	_, testVisual := os.LookupEnv("VISUAL_TEST")
	// To update snapshot outputs
	if _, set := os.LookupEnv("ACCEPT_VISUAL"); set {
		runArgs = append(runArgs, "--update-snapshots")
	}
	if project := os.Getenv("PLAYWRIGHT_PROJECT"); project != "" {
		runArgs = append(runArgs, "--project="+project)
	}

	// Create new test for each input file
	for _, path := range paths {
		_, filename := filepath.Split(path)
		testname := filename[:len(filename)-len(filepath.Ext(path))]

		if canSkipTest(path) {
			fmt.Printf("No related changes for test, skipping: %s\n", filename)
			continue
		}

		t.Run(testname, func(t *testing.T) {
			// Add test-specific fixures or config options.
			// Note: this breaks test execution through Playwright Extension for VSCode
			// Note: these tests only work properly via `make test-e2e-sqlite#filename`
			if testname == "buttons.test.e2e" || testname == "dropdown.test.e2e" || testname == "modal.test.e2e" || testname == "hashbox.test.e2e" {
				// Allow access to /-/demo/
				defer test.MockVariableValue(&setting.IsProd, false)()
				defer test.MockVariableValue(&testE2eWebRoutes, routers.NormalRoutes())()
			}
			if testname == "codemirror.test.e2e" {
				defer test.MockVariableValue(&setting.DisableGitHooks, false)()
			}
			if testname == "user-settings.test.e2e" {
				defer test.MockVariableValue(&setting.Quota.Enabled, true)()
				defer test.MockVariableValue(&testE2eWebRoutes, routers.NormalRoutes())()
			}
			if testname == "runner-management.test.e2e" {
				defer unittest.OverrideFixtures("tests/e2e/fixtures/runner-management")()
			}
			if testname == "image-diff.test.e2e" {
				defer unittest.OverrideFixtures("tests/e2e/fixtures/image-diff")()
			}

			// Default 2 minute timeout
			onForgejoRun(t, func(*testing.T, *url.URL) {
				DeclareGitRepos(t)
				thisTest := runArgs
				// when all tests are run, use unique artifacts directories per test to preserve artifacts from other tests
				if testVisual {
					thisTest = append(thisTest, "--output=tests/e2e/test-artifacts/"+testname)
				}
				thisTest = append(thisTest, path)
				cmd := exec.Command(runArgs[0], thisTest...)
				cmd.Env = os.Environ()
				cmd.Env = append(cmd.Env, fmt.Sprintf("GITEA_URL=%s", setting.AppURL))
				cmd.Dir = setting.AppWorkPath

				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr

				err := cmd.Run()
				if err != nil && !testVisual {
					log.Fatal("Playwright Failed: %s", err)
				}
			})
		})
	}
}
