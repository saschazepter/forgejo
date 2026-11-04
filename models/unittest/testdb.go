// Copyright 2026 The Forgejo Authors. All rights reserved.
// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package unittest

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forgejo.org/models/db"
	"forgejo.org/models/system"
	"forgejo.org/modules/auth/password/hash"
	"forgejo.org/modules/base"
	"forgejo.org/modules/git"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/setting/config"
	"forgejo.org/modules/storage"
	"forgejo.org/modules/test"
	"forgejo.org/modules/util"
	"forgejo.org/services/stats"

	"code.forgejo.org/xorm/xorm"
	"code.forgejo.org/xorm/xorm/names"
	"github.com/stretchr/testify/require"
)

// projectRoot is the path to the repository root
var (
	projectRoot string
	fixturesDir string
)

// FixturesDir returns the fixture directory
func FixturesDir() string {
	return fixturesDir
}

func fatalTestError(fmtStr string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, fmtStr, args...)
	os.Exit(1)
}

// InitSettings initializes config provider and load common settings for tests
func InitSettings() {
	InitCustomSettings("unittest.ini")
}

func InitCustomSettings(confFileName string) {
	root := base.SetupProjectRoot()
	if root == "" {
		fatalTestError("Environment variable $PROJECT_ROOT not set")
	}
	if setting.CustomConf == "" {
		templateFile := confFileName + ".tmpl"
		content, err := os.ReadFile(filepath.Join(root, "tests", templateFile))
		if err != nil {
			log.Fatalf("couldn't read config template: %s", templateFile)
		}
		err = os.WriteFile(filepath.Join(root, "tests", confFileName), content, 0o644)
		if err != nil {
			log.Fatalf("couldn't write config: %s", confFileName)
		}
		setting.CustomConf = filepath.Join(root, "tests", confFileName)
	}
	os.Setenv("PROJECT_CONF", setting.CustomConf)

	setting.InitCfgProvider(setting.CustomConf)
	setting.LoadCommonSettings()

	if err := setting.PrepareAppDataPath(); err != nil {
		log.Fatalf("Can not prepare APP_DATA_PATH: %v", err)
	}
	// register the dummy hash algorithm function used in the test fixtures
	_ = hash.Register("dummy", hash.NewDummyHasher)

	setting.PasswordHashAlgo, _ = hash.SetDefaultPasswordHashAlgorithm("dummy")

	// Avoid loading the git's system config.
	// On macOS, system config sets the osxkeychain credential helper, which will cause tests to freeze with a dialog.
	// But we do not set it in production at the moment, because it might be a "breaking" change,
	// more details are in "modules/git.commonBaseEnvs".
	_ = os.Setenv("GIT_CONFIG_NOSYSTEM", "true")
}

// TestOptions represents test options
type TestOptions struct {
	FixtureFiles    []string
	SetUp           func() error // SetUp will be executed before all tests in this package
	TearDown        func() error // TearDown will be executed after all tests in this package
	IniFileOverride string
}

// MainTest a reusable TestMain(..) function for unit tests that need to use a
// test database. Creates the test database, and sets necessary settings.
func MainTest(m *testing.M, testOpts ...*TestOptions) {
	if _, ok := os.LookupEnv("GIT_DIR"); ok {
		// The wiki tests require perform git operations.
		// It worked before dropping the need for the gitea binary because in case of wiki push,
		// the git hooks do not perform http requests (access permission is checked before git invocation).
		log.Println("Fake git hook which accepts everything (GIT_DIR is set).")
		log.Println("Forgejo with proper http hooks is available in integration tests.")
		os.Exit(0)
	}

	searchDir, _ := os.Getwd()
	for searchDir != "" {
		if _, err := os.Stat(filepath.Join(searchDir, "go.mod")); err == nil {
			break // The "go.mod" should be the one for source repository
		}
		if dir := filepath.Dir(searchDir); dir == searchDir {
			searchDir = "" // reaches the root of filesystem
		} else {
			searchDir = dir
		}
	}
	if searchDir == "" {
		panic("The tests should run in the source repository, there should be a 'go.mod' in the root")
	}

	projectRoot = searchDir
	setting.CustomPath = filepath.Join(projectRoot, "custom")
	if len(testOpts) == 0 || testOpts[0].IniFileOverride == "" {
		InitSettings()
	} else {
		InitCustomSettings(testOpts[0].IniFileOverride)
	}

	fixturesDir = filepath.Join(projectRoot, "models", "fixtures")
	var opts FixturesOptions
	if len(testOpts) == 0 || len(testOpts[0].FixtureFiles) == 0 {
		opts.Dir = fixturesDir
	} else {
		for _, f := range testOpts[0].FixtureFiles {
			if len(f) != 0 {
				opts.Files = append(opts.Files, filepath.Join(fixturesDir, f))
			}
		}
	}

	if err := CreateTestEngine(opts); err != nil {
		fatalTestError("Error creating test engine: %v\n", err)
	}

	setting.AppURL = "https://try.gitea.io/"
	setting.RunUser = "runuser"
	setting.SSH.User = "sshuser"
	setting.SSH.BuiltinServerUser = "builtinuser"
	setting.SSH.Port = 3000
	setting.SSH.Domain = "try.gitea.io"
	setting.Database.Type = "sqlite3"
	setting.Repository.DefaultBranch = "master" // many test code still assume that default branch is called "master"
	repoRootPath, err := os.MkdirTemp(os.TempDir(), "repos")
	if err != nil {
		fatalTestError("TempDir: %v\n", err)
	}
	setting.RepoRootPath = repoRootPath
	appDataPath, err := os.MkdirTemp(os.TempDir(), "appdata")
	if err != nil {
		fatalTestError("TempDir: %v\n", err)
	}
	setting.AppDataPath = appDataPath
	setting.AppWorkPath = projectRoot
	setting.StaticRootPath = projectRoot
	setting.GravatarSource = "https://secure.gravatar.com/avatar/"

	setting.Attachment.Storage.Path = filepath.Join(setting.AppDataPath, "attachments")

	setting.LFS.Storage.Path = filepath.Join(setting.AppDataPath, "lfs")

	setting.Avatar.Storage.Path = filepath.Join(setting.AppDataPath, "avatars")

	setting.RepoAvatar.Storage.Path = filepath.Join(setting.AppDataPath, "repo-avatars")

	setting.RepoArchive.Storage.Path = filepath.Join(setting.AppDataPath, "repo-archive")

	setting.Packages.Storage.Path = filepath.Join(setting.AppDataPath, "packages")

	setting.Actions.LogStorage.Path = filepath.Join(setting.AppDataPath, "actions_log")

	setting.Git.HomePath = filepath.Join(setting.AppDataPath, "home")

	setting.IncomingEmail.ReplyToAddress = "incoming+%{token}@localhost"

	config.SetDynGetter(system.NewDatabaseDynKeyGetter())

	if err = storage.Init(); err != nil {
		fatalTestError("storage.Init: %v\n", err)
	}
	initStats()
	if err = util.RemoveAll(repoRootPath); err != nil {
		fatalTestError("util.RemoveAll: %v\n", err)
	}
	if err = CopyDir(filepath.Join(projectRoot, "tests", "gitea-repositories-meta"), setting.RepoRootPath); err != nil {
		fatalTestError("util.CopyDir: %v\n", err)
	}

	if err = git.InitFull(context.Background()); err != nil {
		fatalTestError("git.Init: %v\n", err)
	}
	ownerDirs, err := os.ReadDir(setting.RepoRootPath)
	if err != nil {
		fatalTestError("unable to read the new repo root: %v\n", err)
	}
	for _, ownerDir := range ownerDirs {
		if !ownerDir.Type().IsDir() {
			continue
		}
		repoDirs, err := os.ReadDir(filepath.Join(setting.RepoRootPath, ownerDir.Name()))
		if err != nil {
			fatalTestError("unable to read the new repo root: %v\n", err)
		}
		for _, repoDir := range repoDirs {
			_ = os.MkdirAll(filepath.Join(setting.RepoRootPath, ownerDir.Name(), repoDir.Name(), "objects", "pack"), 0o755)
			_ = os.MkdirAll(filepath.Join(setting.RepoRootPath, ownerDir.Name(), repoDir.Name(), "objects", "info"), 0o755)
			_ = os.MkdirAll(filepath.Join(setting.RepoRootPath, ownerDir.Name(), repoDir.Name(), "refs", "heads"), 0o755)
			_ = os.MkdirAll(filepath.Join(setting.RepoRootPath, ownerDir.Name(), repoDir.Name(), "refs", "tag"), 0o755)
		}
	}

	if len(testOpts) > 0 && testOpts[0].SetUp != nil {
		if err := testOpts[0].SetUp(); err != nil {
			fatalTestError("set up failed: %v\n", err)
		}
	}

	exitStatus := m.Run()

	if len(testOpts) > 0 && testOpts[0].TearDown != nil {
		if err := testOpts[0].TearDown(); err != nil {
			fatalTestError("tear down failed: %v\n", err)
		}
	}

	if err = util.RemoveAll(repoRootPath); err != nil {
		fatalTestError("util.RemoveAll: %v\n", err)
	}
	if err = util.RemoveAll(appDataPath); err != nil {
		fatalTestError("util.RemoveAll: %v\n", err)
	}
	os.Exit(exitStatus)
}

func initStats() {
	// Use an in-memory queue for the `stats` module during testing.  This queue will collect requests for recalc during
	// tests which can be performed by invoking `unittest.FlushAsyncCalcs(t)`.
	cfg, err := setting.NewConfigProviderFromData(`
[queue.stats_recalc]
TYPE = channel
`)
	if err != nil {
		fatalTestError("NewConfigProviderFromData: %v\n", err)
	}
	defer test.MockVariableValue(&setting.CfgProvider, cfg)()
	if err := stats.Init(); err != nil {
		fatalTestError("stats.Init: %v\n", err)
	}
}

// FixturesOptions fixtures needs to be loaded options
type FixturesOptions struct {
	Dir   string
	Files []string
	Dirs  []string
	Base  string
	// By default all registered models are cleaned, even if they do not have fixture. When OnlyAffectModels is not-nil,
	// cleaning registered models will be skipped and only these models with fixtures are considered.
	OnlyAffectModels []any
}

// CreateTestEngine creates a memory database and loads the fixture data from fixturesDir
func CreateTestEngine(opts FixturesOptions) error {
	x, err := xorm.NewEngine("sqlite3", "file::memory:?cache=shared&_txlock=immediate")
	if err != nil {
		if strings.Contains(err.Error(), "unknown driver") {
			return fmt.Errorf(`sqlite3 requires: import _ "github.com/mattn/go-sqlite3" or -tags sqlite,sqlite_unlock_notify%s%w`, "\n", err)
		}
		return err
	}
	x.SetMapper(names.GonicMapper{})
	x.AddHook(faultInjectorHook{})
	db.SetDefaultEngine(context.Background(), x)

	if err = db.SyncAllTables(); err != nil {
		return err
	}
	switch os.Getenv("GITEA_UNIT_TESTS_LOG_SQL") {
	case "true", "1":
		x.ShowSQL(true)
	}

	return InitFixtures(opts)
}

// PrepareTestDatabase load test fixtures into test database
func PrepareTestDatabase() error {
	return LoadFixtures()
}

// PrepareTestEnv prepares the environment for unit tests. Can only be called
// by tests that use the above MainTest(..) function.
func PrepareTestEnv(t testing.TB) {
	require.NoError(t, PrepareTestDatabase())
	require.NoError(t, util.RemoveAll(setting.RepoRootPath))
	projectRoot = base.SetupProjectRoot() // Makes sure PROJECT_ROOT is set
	metaPath := filepath.Join(projectRoot, "tests", "gitea-repositories-meta")
	require.NoError(t, CopyDir(metaPath, setting.RepoRootPath))
	ownerDirs, err := os.ReadDir(setting.RepoRootPath)
	require.NoError(t, err)
	for _, ownerDir := range ownerDirs {
		if !ownerDir.Type().IsDir() {
			continue
		}
		repoDirs, err := os.ReadDir(filepath.Join(setting.RepoRootPath, ownerDir.Name()))
		require.NoError(t, err)
		for _, repoDir := range repoDirs {
			_ = os.MkdirAll(filepath.Join(setting.RepoRootPath, ownerDir.Name(), repoDir.Name(), "objects", "pack"), 0o755)
			_ = os.MkdirAll(filepath.Join(setting.RepoRootPath, ownerDir.Name(), repoDir.Name(), "objects", "info"), 0o755)
			_ = os.MkdirAll(filepath.Join(setting.RepoRootPath, ownerDir.Name(), repoDir.Name(), "refs", "heads"), 0o755)
			_ = os.MkdirAll(filepath.Join(setting.RepoRootPath, ownerDir.Name(), repoDir.Name(), "refs", "tag"), 0o755)
		}
	}
}
