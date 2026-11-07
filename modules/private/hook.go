// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package private

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"forgejo.org/modules/git"
	"forgejo.org/modules/git/pushoptions"
	"forgejo.org/modules/log"
	"forgejo.org/modules/repository"
)

// Git environment variables
const (
	GitAlternativeObjectDirectories = "GIT_ALTERNATE_OBJECT_DIRECTORIES"
	GitObjectDirectory              = "GIT_OBJECT_DIRECTORY"
	GitQuarantinePath               = "GIT_QUARANTINE_PATH"
)

// HookOptions represents the options for the Hook calls
type HookOptions struct {
	OldCommitIDs                    []string
	NewCommitIDs                    []string
	RefFullNames                    []git.RefName
	UserID                          int64
	UserName                        string
	GitObjectDirectory              string
	GitAlternativeObjectDirectories string
	GitQuarantinePath               string
	GitPushOptions                  map[string]string
	PullRequestID                   int64
	PushTrigger                     repository.PushTrigger
	DeployKeyID                     int64 // if the pusher is a DeployKey, then UserID is the repo's org user.
	IsWiki                          bool
	ActionPerm                      int
}

func (o *HookOptions) GetGitPushOptions() pushoptions.Interface {
	return pushoptions.NewFromMap(&o.GitPushOptions)
}

// SSHLogOption ssh log options
type SSHLogOption struct {
	Level   log.Level
	Message string
}

// HookPostReceiveResult represents an individual result from PostReceive
type HookPostReceiveResult struct {
	Messages     []HookPostReceiveMessage
	RepoWasEmpty bool
	Err          string
}

// HookPostReceiveMessage represents an individual branch result from PostReceive.
// Only for display purposes on 'git push'.
type HookPostReceiveMessage struct {
	PullURLS []string
	Branch   string
	// Set after a push to a PR-less branch on a fork repo
	CreateURL string
	// Set after an agit PR creation
	AgitRef string
}

// HookProcReceiveResult represents an individual result from ProcReceive
type HookProcReceiveResult struct {
	Results []HookProcReceiveRefResult
	Err     string
}

// HookProcReceiveRefResult represents an individual result from ProcReceive
type HookProcReceiveRefResult struct {
	OldOID       string
	NewOID       string
	Ref          string
	OriginalRef  git.RefName
	IsForcePush  bool
	IsNotMatched bool
	Err          string
}

// HookPreReceive check whether the provided commits are allowed
func HookPreReceive(ctx context.Context, ownerName, repoName string, opts HookOptions) ResponseExtra {
	reqURL := fmt.Sprintf("/api/internal/hook/pre-receive/%s/%s", url.PathEscape(ownerName), url.PathEscape(repoName))
	req := newInternalRequest(ctx, reqURL, "POST", opts)
	req.SetReadWriteTimeout(time.Duration(60+len(opts.OldCommitIDs)) * time.Second)
	_, extra := requestJSONResp(req, &ResponseText{})
	return extra
}

// HookPostReceive updates services and users
func HookPostReceive(ctx context.Context, ownerName, repoName string, opts HookOptions) (*HookPostReceiveResult, ResponseExtra) {
	reqURL := fmt.Sprintf("/api/internal/hook/post-receive/%s/%s", url.PathEscape(ownerName), url.PathEscape(repoName))
	req := newInternalRequest(ctx, reqURL, "POST", opts)
	req.SetReadWriteTimeout(time.Duration(60+len(opts.OldCommitIDs)) * time.Second)
	return requestJSONResp(req, &HookPostReceiveResult{})
}

// HookProcReceive proc-receive hook
func HookProcReceive(ctx context.Context, ownerName, repoName string, opts HookOptions) (*HookProcReceiveResult, ResponseExtra) {
	reqURL := fmt.Sprintf("/api/internal/hook/proc-receive/%s/%s", url.PathEscape(ownerName), url.PathEscape(repoName))

	req := newInternalRequest(ctx, reqURL, "POST", opts)
	req.SetReadWriteTimeout(time.Duration(60+len(opts.OldCommitIDs)) * time.Second)
	return requestJSONResp(req, &HookProcReceiveResult{})
}

// SetDefaultBranch will set the default branch to the provided branch for the provided repository
func SetDefaultBranch(ctx context.Context, ownerName, repoName, branch string) ResponseExtra {
	reqURL := fmt.Sprintf("/api/internal/hook/set-default-branch/%s/%s/%s",
		url.PathEscape(ownerName),
		url.PathEscape(repoName),
		url.PathEscape(branch),
	)
	req := newInternalRequest(ctx, reqURL, "POST")
	_, extra := requestJSONResp(req, &ResponseText{})
	return extra
}

// SSHLog sends ssh error log response
func SSHLog(ctx context.Context, level log.Level, msg string) error {
	req := newInternalRequest(ctx, "/api/internal/ssh/log", "POST", &SSHLogOption{Level: level, Message: msg})
	_, extra := requestJSONResp(req, &ResponseText{})
	return extra.Error
}
