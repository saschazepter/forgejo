// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"path/filepath"
	"strings"
	"time"

	"forgejo.org/modules/log"
)

// Git settings
var Git = struct {
	Path                 string
	HomePath             string
	DisableDiffHighlight bool

	MaxGitDiffLines          int
	MaxGitDiffLineCharacters int
	MaxGitRangeDiffCommits   int
	CommitsRangeSize         int // CommitsRangeSize the default commits range size
	BranchesRangeSize        int // BranchesRangeSize the default branches range size
	VerbosePush              bool
	VerbosePushDelay         time.Duration
	GCArgs                   []string `ini:"GC_ARGS" delim:" "`
	PullRequestPushMessage   bool
	DisablePartialClone      bool
	Timeout                  struct {
		Default int
		Migrate int
		Mirror  int
		Clone   int
		Pull    int
		GC      int `ini:"GC"`
		Grep    int
	} `ini:"git.timeout"`
}{
	DisableDiffHighlight:     false,
	MaxGitDiffLines:          1000,
	MaxGitDiffLineCharacters: 5000,
	MaxGitRangeDiffCommits:   100,
	CommitsRangeSize:         50,
	BranchesRangeSize:        20,
	VerbosePush:              true,
	VerbosePushDelay:         5 * time.Second,
	GCArgs:                   []string{},
	PullRequestPushMessage:   true,
	DisablePartialClone:      false,
	Timeout: struct {
		Default int
		Migrate int
		Mirror  int
		Clone   int
		Pull    int
		GC      int `ini:"GC"`
		Grep    int
	}{
		Default: 360,
		Migrate: 600,
		Mirror:  300,
		Clone:   300,
		Pull:    300,
		GC:      60,
		Grep:    2,
	},
}

type GitConfigType struct {
	Options map[string]string // git config key is case-insensitive, always use lower-case
}

func (c *GitConfigType) SetOption(key, val string) {
	c.Options[strings.ToLower(key)] = val
}

func (c *GitConfigType) GetOption(key string) string {
	return c.Options[strings.ToLower(key)]
}

var GitConfig = GitConfigType{
	Options: make(map[string]string),
}

func loadGitFrom(rootCfg ConfigProvider) {
	sec := rootCfg.Section("git")
	deprecatedSettingWarning(rootCfg, "git", "MAX_GIT_DIFF_FILES", "ui", "DIFF_PAGING_NUM")
	if err := sec.MapTo(&Git); err != nil {
		log.Fatal("Failed to map Git settings: %v", err)
	}

	secGitConfig := rootCfg.Section("git.config")
	GitConfig.Options = make(map[string]string)
	GitConfig.SetOption("diff.algorithm", "histogram")
	GitConfig.SetOption("core.logAllRefUpdates", "true")
	GitConfig.SetOption("gc.reflogExpire", "90")

	GitConfig.SetOption("transfer.fsckObjects", "true")
	// To ignore specific warnings they have to be set for all of the three
	// scenarios. Per git-config(1): "To uniformly configure the same fsck
	// settings in different circumstances, all three of them must be set to the
	// same values."
	for _, prefix := range []string{"fsck.", "fetch.fsck.", "receive.fsck."} {
		GitConfig.SetOption(prefix+"badTimezone", "ignore")
		GitConfig.SetOption(prefix+"missingSpaceBeforeDate", "ignore")
		GitConfig.SetOption(prefix+"zeroPaddedFilemode", "ignore")
	}

	for _, key := range secGitConfig.Keys() {
		GitConfig.SetOption(key.Name(), key.String())
	}

	Git.HomePath = sec.Key("HOME_PATH").MustString("home")
	if !filepath.IsAbs(Git.HomePath) {
		Git.HomePath = filepath.Join(AppDataPath, Git.HomePath)
	} else {
		Git.HomePath = filepath.Clean(Git.HomePath)
	}
}
