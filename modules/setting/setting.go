// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2017 The Gitea Authors. All rights reserved.
// Copyright 2025 The Forgejo Authors. All rights reserved
// SPDX-License-Identifier: MIT

package setting

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"forgejo.org/modules/log"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/user"
	"forgejo.org/modules/util"

	"github.com/hashicorp/go-version"
)

var ForgejoVersion = "1.0.0"

// settings
var (
	// AppVer is the version of the current build of Forgejo. It is set in main.go from main.Version.
	AppVer string
	// AppDocsVer returns the Forgejo Docs version string corresponding to the current build of Forgejo.
	AppDocsVer = sync.OnceValue(initialAppDocsVer)
	// AppBuiltWith represents a human-readable version go runtime build version and build tags. (See main.go formatBuiltWith().)
	AppBuiltWith string
	// AppStartTime stores the time at which Forgejo started.
	AppStartTime time.Time

	// Other global setting objects

	CfgProvider ConfigProvider
	RunMode     string
	RunUser     string
	IsProd      bool

	// IsInTesting indicates whether the testing is running. A lot of unreliable code causes a lot of nonsense error logs during testing
	// TODO: this is only a temporary solution, we should make the test code more reliable
	IsInTesting = false
)

func init() {
	if AppVer == "" {
		AppVer = "dev"
	}

	// By default set this logger at Info - we'll change it later, but we need to start with something.
	log.SetConsoleLogger(log.DEFAULT, "console", log.INFO)
}

// Parses AppVer as a semver string and returns a version string compatible with Forgejo Docs.
func initialAppDocsVer() string {
	result := "latest"

	// parse semver if we haven't yet
	ver, err := version.NewSemver(strings.TrimSpace(AppVer))
	if err != nil {
		log.Warn("Doclinks will fallback to version '%s' due to err: %v", result, err)
	} else {
		segments := ver.Segments()
		if len(segments) >= 2 {
			result = fmt.Sprintf("v%d.%d", segments[0], segments[1])
		}
	}

	return result
}

// Returns a URL string to Forgejo docs for the current app version.
func AppDocsURL(path string) string {
	return AppVersionDocsURL(AppDocsVer(), path)
}

// Returns a URL string to Forgejo docs for the given app version.
func AppVersionDocsURL(version, path string) string {
	path, hash, hasHash := strings.Cut(path, "#")
	version = util.PathEscapeSegments(version)
	path = util.PathEscapeSegments(path)
	if path == "" {
		path = "/"
	}
	url, err := url.JoinPath("https://forgejo.org/", "docs", version, path)
	if err != nil {
		// Not sure when or if this would ever happen, but let's log the error and continue with an empty `url` value
		log.Debug("AppVersionDocsURL failed to join version '%[1]s' to path '%[2]s': %[3]v", version, path, err)
	}
	if hasHash {
		return url + "#" + hash
	}
	return url
}

// IsRunUserMatchCurrentUser returns false if configured run user does not match
// actual user that runs the app. The first return value is the actual user name.
func IsRunUserMatchCurrentUser(runUser string) (string, bool) {
	if SSH.StartBuiltinServer {
		return "", true
	}

	currentUser := user.CurrentUsername()
	return currentUser, runUser == currentUser
}

// PrepareAppDataPath creates app data directory if necessary
func PrepareAppDataPath() error {
	// FIXME: There are too many calls to MkdirAll in old code. It is incorrect.
	// For example, if someDir=/mnt/vol1/gitea-home/data, if the mount point /mnt/vol1 is not mounted when Forgejo runs,
	// then Forgejo will make new empty directories in /mnt/vol1, all are stored in the root filesystem.
	// The correct behavior should be: creating parent directories is end users' duty. We only create sub-directories in existing parent directories.
	// For quickstart, the parent directories should be created automatically for first startup (eg: a flag or a check of INSTALL_LOCK).
	// Now we can take the first step to do correctly (using Mkdir) in other packages, and prepare the AppDataPath here, then make a refactor in future.

	st, err := os.Stat(AppDataPath)
	if os.IsNotExist(err) {
		err = os.MkdirAll(AppDataPath, os.ModePerm)
		if err != nil {
			return fmt.Errorf("unable to create the APP_DATA_PATH directory: %q, Error: %w", AppDataPath, err)
		}
		return nil
	}

	if err != nil {
		return fmt.Errorf("unable to use APP_DATA_PATH %q. Error: %w", AppDataPath, err)
	}

	if !st.IsDir() /* also works for symlink */ {
		return fmt.Errorf("the APP_DATA_PATH %q is not a directory (or symlink to a directory) and can't be used", AppDataPath)
	}

	return nil
}

func InitCfgProvider(file string) {
	var err error
	if CfgProvider, err = NewConfigProviderFromFile(file); err != nil {
		log.Fatal("Unable to init config provider from %q: %v", file, err)
	}
	CfgProvider.DisableSaving() // do not allow saving the CfgProvider into file, it will be polluted by the "MustXxx" calls
}

func MustInstalled() {
	if !InstallLock {
		log.Fatal(`
Unable to load a config file for an installed Forgejo instance.
If you have installed Forgejo already, you might need to provide the path to your config file via the "--config" option.
If you haven't installed Forgejo already, please run "forgejo web" command first to finish the installation.
`)
	}
}

func LoadCommonSettings() {
	if err := loadCommonSettingsFrom(CfgProvider); err != nil {
		log.Fatal("Unable to load settings from config: %v", err)
	}
}

// loadCommonSettingsFrom loads common configurations from a configuration provider.
func loadCommonSettingsFrom(cfg ConfigProvider) error {
	// WARNING: don't change the sequence except you know what you are doing.
	loadRunModeFrom(cfg)
	loadLogGlobalFrom(cfg)
	loadPWAFrom(cfg)
	loadServerFrom(cfg)
	loadSSHFrom(cfg)

	mustCurrentRunUserMatch(cfg) // it depends on the SSH config, only non-builtin SSH server requires this check

	loadSecurityFrom(cfg)
	loadOAuth2From(cfg)
	if err := loadAttachmentFrom(cfg); err != nil {
		return err
	}
	if err := loadLFSFrom(cfg); err != nil {
		return err
	}
	loadTimeFrom(cfg)
	if err := loadRepositoryFrom(cfg); err != nil {
		return err
	}
	if err := loadAvatarsFrom(cfg); err != nil {
		return err
	}
	if err := loadRepoAvatarFrom(cfg); err != nil {
		return err
	}
	if err := loadPackagesFrom(cfg); err != nil {
		return err
	}
	if err := loadActionsFrom(cfg); err != nil {
		return err
	}
	if err := loadModerationFrom(cfg); err != nil {
		return err
	}

	loadUIFrom(cfg)
	loadAdminFrom(cfg)
	loadAPIFrom(cfg)
	loadBadgesFrom(cfg)
	loadMetricsFrom(cfg)
	loadCamoFrom(cfg)
	loadI18nFrom(cfg)
	loadGitFrom(cfg)
	loadMirrorFrom(cfg)
	loadMarkupFrom(cfg)
	loadQuotaFrom(cfg)
	loadOtherFrom(cfg)
	loadCustomFundingProvidersFrom(cfg)
	return nil
}

func loadRunModeFrom(rootCfg ConfigProvider) {
	rootSec := rootCfg.Section("")
	RunUser = rootSec.Key("RUN_USER").MustString(user.CurrentUsername())

	// The following is a purposefully undocumented option. Please do not run Forgejo as root. It will only cause future headaches.
	// Please don't use root as a bandaid to "fix" something that is broken, instead the broken thing should instead be fixed properly.
	unsafeAllowRunAsRoot := ConfigSectionKeyBool(rootSec, "I_AM_BEING_UNSAFE_RUNNING_AS_ROOT")
	unsafeAllowRunAsRoot = unsafeAllowRunAsRoot || optional.ParseBool(os.Getenv("GITEA_I_AM_BEING_UNSAFE_RUNNING_AS_ROOT")).ValueOrDefault(false)
	RunMode = os.Getenv("GITEA_RUN_MODE")
	if RunMode == "" {
		RunMode = rootSec.Key("RUN_MODE").MustString("prod")
	}

	// non-dev mode is treated as prod mode, to protect users from accidentally running in dev mode if there is a typo in this value.
	RunMode = strings.ToLower(RunMode)
	if RunMode != "dev" {
		RunMode = "prod"
	}
	IsProd = RunMode != "dev"

	// check if we run as root
	if os.Getuid() == 0 {
		if !unsafeAllowRunAsRoot {
			// Special thanks to VLC which inspired the wording of this messaging.
			log.Fatal("Forgejo is not supposed to be run as root. Sorry. If you need to use privileged TCP ports please instead use setcap and the `cap_net_bind_service` permission")
		}
		log.Critical("You are running Forgejo using the root user, and have purposely chosen to skip built-in protections around this. You have been warned against this.")
	}
}

// HasInstallLock checks the install-lock in ConfigProvider directly, because sometimes the config file is not loaded into setting variables yet.
func HasInstallLock(rootCfg ConfigProvider) bool {
	return rootCfg.Section("security").Key("INSTALL_LOCK").MustBool(false)
}

func mustCurrentRunUserMatch(rootCfg ConfigProvider) {
	// Does not check run user when the "InstallLock" is off.
	if HasInstallLock(rootCfg) {
		currentUser, match := IsRunUserMatchCurrentUser(RunUser)
		if !match {
			log.Fatal("Expect user '%s' but current user is: %s", RunUser, currentUser)
		}
	}
}

// LoadSettings initializes the settings for normal start up
func LoadSettings() {
	initAllLoggers()

	loadDBSetting(CfgProvider)
	loadFederationFrom(CfgProvider)
	loadServiceFrom(CfgProvider)
	loadOAuth2ClientFrom(CfgProvider)
	loadCacheFrom(CfgProvider)
	loadSessionFrom(CfgProvider)
	loadCorsFrom(CfgProvider)
	loadMailsFrom(CfgProvider)
	loadProxyFrom(CfgProvider)
	loadWebhookFrom(CfgProvider)
	loadMigrationsFrom(CfgProvider)
	loadIndexerFrom(CfgProvider)
	loadTaskFrom(CfgProvider)
	LoadQueueSettings()
	loadProjectFrom(CfgProvider)
	loadMimeTypeMapFrom(CfgProvider)
	loadAuthorizedIntegrationFrom(CfgProvider)
}

// LoadSettingsForInstall initializes the settings for install
func LoadSettingsForInstall() {
	initAllLoggers()

	loadDBSetting(CfgProvider)
	loadServiceFrom(CfgProvider)
	loadMailerFrom(CfgProvider)
}
