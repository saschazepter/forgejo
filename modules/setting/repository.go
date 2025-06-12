// Copyright 2019 The Gitea Authors. All rights reserved.
// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"forgejo.org/modules/log"

	"golang.org/x/crypto/ssh"
)

// enumerates all the policy repository creating
const (
	RepoCreatingLastUserVisibility = "last"
	RepoCreatingPrivate            = "private"
	RepoCreatingPublic             = "public"
)

// MaxUserCardsPerPage sets maximum amount of watchers and stargazers shown per page
// those pages use 2 or 3 column layout, so the value should be divisible by 2 and 3
var MaxUserCardsPerPage = 36

// MaxForksPerPage sets maximum amount of forks shown per page
var MaxForksPerPage = 40

var SSHInstanceKey ssh.PublicKey

// Repository settings
var (
	Repository = struct {
		DetectedCharsetsOrder                   []string
		DetectedCharsetScore                    map[string]int `ini:"-"`
		AnsiCharset                             string
		ForcePrivate                            bool
		DefaultPrivate                          string
		DefaultPushCreatePrivate                bool
		MaxCreationLimit                        int
		PreferredLicenses                       []string
		DisableHTTPGit                          bool
		AccessControlAllowOrigin                string
		UseCompatSSHURI                         bool
		GoGetCloneURLProtocol                   string
		DefaultCloseIssuesViaCommitsInAnyBranch bool
		EnablePushCreateUser                    bool
		EnablePushCreateOrg                     bool
		DisabledRepoUnits                       []string
		DefaultRepoUnits                        []string
		DefaultForkRepoUnits                    []string
		DefaultMirrorRepoUnits                  []string
		PrefixArchiveFiles                      bool
		DisableMigrations                       bool
		DisableStars                            bool
		DisableForks                            bool
		DefaultBranch                           string
		AllowAdoptionOfUnadoptedRepositories    bool
		AllowDeleteOfUnadoptedRepositories      bool
		DisableDownloadSourceArchives           bool
		AllowForkWithoutMaximumLimit            bool

		// Repository editor settings
		Editor struct {
			LineWrapExtensions []string
		} `ini:"-"`

		// Repository upload settings
		Upload struct {
			Enabled      bool
			TempPath     string
			AllowedTypes string
			FileMaxSize  int64
			MaxFiles     int
		} `ini:"-"`

		// Repository local settings
		Local struct {
			LocalCopyPath string
		} `ini:"-"`

		// Pull request settings
		PullRequest struct {
			WorkInProgressPrefixes                   []string
			CloseKeywords                            []string
			ReopenKeywords                           []string
			DefaultMergeStyle                        string
			DefaultMergeMessageCommitsLimit          int
			DefaultMergeMessageSize                  int
			DefaultMergeMessageAllAuthors            bool
			DefaultMergeMessageMaxApprovers          int
			DefaultMergeMessageOfficialApproversOnly bool
			DefaultUpdateStyle                       string
			PopulateSquashCommentWithCommitMessages  bool
			AddCoCommitterTrailers                   bool
			RetargetChildrenOnMerge                  bool
		} `ini:"repository.pull-request"`

		// Issue Setting
		Issue struct {
			LockReasons []string
			MaxPinned   int
		} `ini:"repository.issue"`

		Release struct {
			AllowedTypes     string
			DefaultPagingNum int
		} `ini:"repository.release"`

		Signing struct {
			SigningKey        string
			SigningName       string
			SigningEmail      string
			Format            string
			InitialCommit     []string
			CRUDActions       []string `ini:"CRUD_ACTIONS"`
			Merges            []string
			Wiki              []string
			DefaultTrustModel string
		} `ini:"repository.signing"`

		SettableFlags []string
		EnableFlags   bool
	}{
		DetectedCharsetsOrder: []string{
			"UTF-8",
			"UTF-16BE",
			"UTF-16LE",
			"UTF-32BE",
			"UTF-32LE",
			"ISO-8859-1",
			"windows-1252",
			"ISO-8859-2",
			"windows-1250",
			"ISO-8859-5",
			"ISO-8859-6",
			"ISO-8859-7",
			"windows-1253",
			"ISO-8859-8-I",
			"windows-1255",
			"ISO-8859-8",
			"windows-1251",
			"windows-1256",
			"KOI8-R",
			"ISO-8859-9",
			"windows-1254",
			"Shift_JIS",
			"GB18030",
			"EUC-JP",
			"EUC-KR",
			"Big5",
			"ISO-2022-JP",
			"ISO-2022-KR",
			"ISO-2022-CN",
			"IBM424_rtl",
			"IBM424_ltr",
			"IBM420_rtl",
			"IBM420_ltr",
		},
		DetectedCharsetScore:                    map[string]int{},
		AnsiCharset:                             "",
		ForcePrivate:                            false,
		DefaultPrivate:                          RepoCreatingLastUserVisibility,
		DefaultPushCreatePrivate:                true,
		MaxCreationLimit:                        -1,
		PreferredLicenses:                       []string{"Apache-2.0", "MIT"},
		DisableHTTPGit:                          false,
		AccessControlAllowOrigin:                "",
		UseCompatSSHURI:                         true,
		DefaultCloseIssuesViaCommitsInAnyBranch: false,
		EnablePushCreateUser:                    false,
		EnablePushCreateOrg:                     false,
		DisabledRepoUnits:                       []string{},
		DefaultRepoUnits:                        []string{},
		DefaultForkRepoUnits:                    []string{},
		DefaultMirrorRepoUnits:                  []string{},
		PrefixArchiveFiles:                      true,
		DisableMigrations:                       false,
		DisableStars:                            false,
		DisableForks:                            false,
		DefaultBranch:                           "main",
		AllowForkWithoutMaximumLimit:            true,

		// Repository editor settings
		Editor: struct {
			LineWrapExtensions []string
		}{
			LineWrapExtensions: strings.Split(".txt,.md,.markdown,.mdown,.mkd,.livemd,", ","),
		},

		// Repository upload settings
		Upload: struct {
			Enabled      bool
			TempPath     string
			AllowedTypes string
			FileMaxSize  int64
			MaxFiles     int
		}{
			Enabled:      true,
			TempPath:     "data/tmp/uploads",
			AllowedTypes: "",
			FileMaxSize:  50,
			MaxFiles:     5,
		},

		// Repository local settings
		Local: struct {
			LocalCopyPath string
		}{
			LocalCopyPath: "tmp/local-repo",
		},

		// Pull request settings
		PullRequest: struct {
			WorkInProgressPrefixes                   []string
			CloseKeywords                            []string
			ReopenKeywords                           []string
			DefaultMergeStyle                        string
			DefaultMergeMessageCommitsLimit          int
			DefaultMergeMessageSize                  int
			DefaultMergeMessageAllAuthors            bool
			DefaultMergeMessageMaxApprovers          int
			DefaultMergeMessageOfficialApproversOnly bool
			DefaultUpdateStyle                       string
			PopulateSquashCommentWithCommitMessages  bool
			AddCoCommitterTrailers                   bool
			RetargetChildrenOnMerge                  bool
		}{
			WorkInProgressPrefixes: []string{"WIP:", "[WIP]"},
			// Same as GitHub. See
			// https://help.github.com/articles/closing-issues-via-commit-messages
			CloseKeywords:                            strings.Split("close,closes,closed,fix,fixes,fixed,resolve,resolves,resolved", ","),
			ReopenKeywords:                           strings.Split("reopen,reopens,reopened", ","),
			DefaultMergeStyle:                        "merge",
			DefaultMergeMessageCommitsLimit:          50,
			DefaultMergeMessageSize:                  5 * 1024,
			DefaultMergeMessageAllAuthors:            false,
			DefaultMergeMessageMaxApprovers:          10,
			DefaultMergeMessageOfficialApproversOnly: true,
			DefaultUpdateStyle:                       "merge",
			PopulateSquashCommentWithCommitMessages:  false,
			AddCoCommitterTrailers:                   true,
			RetargetChildrenOnMerge:                  true,
		},

		// Issue settings
		Issue: struct {
			LockReasons []string
			MaxPinned   int
		}{
			LockReasons: strings.Split("Too heated,Off-topic,Spam,Resolved", ","),
			MaxPinned:   3,
		},

		Release: struct {
			AllowedTypes     string
			DefaultPagingNum int
		}{
			AllowedTypes:     "",
			DefaultPagingNum: 10,
		},

		// Signing settings
		Signing: struct {
			SigningKey        string
			SigningName       string
			SigningEmail      string
			Format            string
			InitialCommit     []string
			CRUDActions       []string `ini:"CRUD_ACTIONS"`
			Merges            []string
			Wiki              []string
			DefaultTrustModel string
		}{
			SigningKey:        "default",
			SigningName:       "",
			SigningEmail:      "",
			Format:            "openpgp",
			InitialCommit:     []string{"always"},
			CRUDActions:       []string{"pubkey", "twofa", "parentsigned"},
			Merges:            []string{"pubkey", "twofa", "basesigned", "commitssigned"},
			Wiki:              []string{"never"},
			DefaultTrustModel: "collaborator",
		},

		EnableFlags: false,
	}
	RepoRootPath string
	ScriptType   = "bash"
)

func loadRepositoryFrom(rootCfg ConfigProvider) {
	var err error
	// Determine and create root git repository path.
	sec := rootCfg.Section("repository")
	Repository.DisableHTTPGit = sec.Key("DISABLE_HTTP_GIT").MustBool()
	Repository.UseCompatSSHURI = sec.Key("USE_COMPAT_SSH_URI").MustBool(true)
	Repository.GoGetCloneURLProtocol = sec.Key("GO_GET_CLONE_URL_PROTOCOL").MustString("https")
	Repository.MaxCreationLimit = sec.Key("MAX_CREATION_LIMIT").MustInt(-1)
	Repository.DefaultBranch = sec.Key("DEFAULT_BRANCH").MustString(Repository.DefaultBranch)
	RepoRootPath = sec.Key("ROOT").MustString(path.Join(AppDataPath, "forgejo-repositories"))
	if !filepath.IsAbs(RepoRootPath) {
		RepoRootPath = filepath.Join(AppWorkPath, RepoRootPath)
	} else {
		RepoRootPath = filepath.Clean(RepoRootPath)
	}
	defaultDetectedCharsetsOrder := make([]string, 0, len(Repository.DetectedCharsetsOrder))
	for _, charset := range Repository.DetectedCharsetsOrder {
		defaultDetectedCharsetsOrder = append(defaultDetectedCharsetsOrder, strings.ToLower(strings.TrimSpace(charset)))
	}
	ScriptType = sec.Key("SCRIPT_TYPE").MustString("bash")

	if _, err := exec.LookPath(ScriptType); err != nil {
		log.Warn("SCRIPT_TYPE %q is not on the current PATH. Are you sure that this is the correct SCRIPT_TYPE?", ScriptType)
	}

	if err = sec.MapTo(&Repository); err != nil {
		log.Fatal("Failed to map Repository settings: %v", err)
	} else if err = rootCfg.Section("repository.editor").MapTo(&Repository.Editor); err != nil {
		log.Fatal("Failed to map Repository.Editor settings: %v", err)
	} else if err = rootCfg.Section("repository.upload").MapTo(&Repository.Upload); err != nil {
		log.Fatal("Failed to map Repository.Upload settings: %v", err)
	} else if err = rootCfg.Section("repository.local").MapTo(&Repository.Local); err != nil {
		log.Fatal("Failed to map Repository.Local settings: %v", err)
	} else if err = rootCfg.Section("repository.pull-request").MapTo(&Repository.PullRequest); err != nil {
		log.Fatal("Failed to map Repository.PullRequest settings: %v", err)
	}

	if !rootCfg.Section("packages").Key("ENABLED").MustBool(Packages.Enabled) {
		Repository.DisabledRepoUnits = append(Repository.DisabledRepoUnits, "repo.packages")
	}

	if !rootCfg.Section("actions").Key("ENABLED").MustBool(Actions.Enabled) {
		Repository.DisabledRepoUnits = append(Repository.DisabledRepoUnits, "repo.actions")
	}

	// Handle default trustmodel settings
	Repository.Signing.DefaultTrustModel = strings.ToLower(strings.TrimSpace(Repository.Signing.DefaultTrustModel))
	if Repository.Signing.DefaultTrustModel == "default" {
		Repository.Signing.DefaultTrustModel = "collaborator"
	}

	// Handle preferred charset orders
	preferred := make([]string, 0, len(Repository.DetectedCharsetsOrder))
	for _, charset := range Repository.DetectedCharsetsOrder {
		canonicalCharset := strings.ToLower(strings.TrimSpace(charset))
		preferred = append(preferred, canonicalCharset)
		// remove it from the defaults
		for i, charset := range defaultDetectedCharsetsOrder {
			if charset == canonicalCharset {
				defaultDetectedCharsetsOrder = append(defaultDetectedCharsetsOrder[:i], defaultDetectedCharsetsOrder[i+1:]...)
				break
			}
		}
	}

	i := 0
	for _, charset := range preferred {
		// Add the defaults
		if charset == "defaults" {
			for _, charset := range defaultDetectedCharsetsOrder {
				canonicalCharset := strings.ToLower(strings.TrimSpace(charset))
				if _, has := Repository.DetectedCharsetScore[canonicalCharset]; !has {
					Repository.DetectedCharsetScore[canonicalCharset] = i
					i++
				}
			}
			continue
		}
		if _, has := Repository.DetectedCharsetScore[charset]; !has {
			Repository.DetectedCharsetScore[charset] = i
			i++
		}
	}

	if !filepath.IsAbs(Repository.Upload.TempPath) {
		Repository.Upload.TempPath = path.Join(AppWorkPath, Repository.Upload.TempPath)
	}

	if err := loadRepoArchiveFrom(rootCfg); err != nil {
		log.Fatal("loadRepoArchiveFrom: %v", err)
	}
	Repository.EnableFlags = sec.Key("ENABLE_FLAGS").MustBool()

	if Repository.Signing.Format == "ssh" && Repository.Signing.SigningKey != "none" && Repository.Signing.SigningKey != "" {
		sshPublicKey, err := os.ReadFile(Repository.Signing.SigningKey)
		if err != nil {
			log.Fatal("Could not read repository signing key in %q: %v", Repository.Signing.SigningKey, err)
		}
		SSHInstanceKey, _, _, _, err = ssh.ParseAuthorizedKey(sshPublicKey)
		if err != nil {
			log.Fatal("Could not parse the SSH signing key %q: %v", sshPublicKey, err)
		}
	}
}
