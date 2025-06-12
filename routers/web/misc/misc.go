// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package misc

import (
	"net/http"
	"path"

	"forgejo.org/modules/git"
	"forgejo.org/modules/httpcache"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/util"
)

func SSHInfo(rw http.ResponseWriter, req *http.Request) {
	if !git.SupportProcReceive {
		rw.WriteHeader(http.StatusNotFound)
		return
	}
	rw.Header().Set("content-type", "text/json;charset=UTF-8")
	_, err := rw.Write([]byte(`{"type":"agit","version":1}`))
	if err != nil {
		log.Error("fail to write result: err: %v", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}
	rw.WriteHeader(http.StatusOK)
}

func DummyOK(w http.ResponseWriter, req *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func StaticRedirect(target string) func(w http.ResponseWriter, req *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, path.Join(setting.StaticURLPrefix, target), http.StatusMovedPermanently)
	}
}

var defaultRobotsTxt = []byte(`# The default Forgejo robots.txt
# For more information: https://forgejo.org/docs/latest/admin/search-engines-indexation/

User-agent: *
Disallow: /api/
Disallow: /avatars/
Disallow: /user/
Disallow: /swagger.*.json
Disallow: /explore/*?*

Disallow: /repo/create
Disallow: /repo/migrate
Disallow: /org/create
Disallow: /*/*/fork

Disallow: /*/*/watchers
Disallow: /*/*/stargazers
Disallow: /*/*/forks

Disallow: /*/*/src/
Disallow: /*/*/blame/
Disallow: /*/*/commit/
Disallow: /*/*/commits/
Disallow: /*/*/raw/
Disallow: /*/*/media/
Disallow: /*/*/tags
Disallow: /*/*/graph
Disallow: /*/*/branches
Disallow: /*/*/compare
Disallow: /*/*/lastcommit/
Disallow: /*/*/rss/branch/
Disallow: /*/*/atom/branch/

Disallow: /*/*/activity
Disallow: /*/*/activity_author_data

Disallow: /*/*/actions
Disallow: /*/*/projects
Disallow: /*/*/labels
Disallow: /*/*/milestones

Disallow: /*/*/find/
Disallow: /*/*/tree-list/
Disallow: /*/*/search/
Disallow: /*/-/code

Disallow: /*/*/issues/new
Disallow: /*/*/pulls/*/files
Disallow: /*/*/pulls/*/commits

Disallow: /attachments/
Disallow: /*/*/attachments/
Disallow: /*/*/issues/*/attachments/
Disallow: /*/*/pulls/*/attachments/
Disallow: /*/*/releases/attachments
Disallow: /*/*/releases/download

Disallow: /*/*/archive/
Disallow: /*.bundle$
Disallow: /*.patch$
Disallow: /*.diff$
Disallow: /*.atom$
Disallow: /*.rss$

Disallow: /*lang=*
Disallow: /*redirect_to=*
Disallow: /*tab=*
Disallow: /*q=*
Disallow: /*sort=*
Disallow: /*repo-search-archived=*
`)

func RobotsTxt(w http.ResponseWriter, req *http.Request) {
	httpcache.SetCacheControlInHeader(w.Header(), setting.StaticCacheTime)
	w.Header().Set("Content-Type", "text/plain")

	robotsTxt := util.FilePathJoinAbs(setting.CustomPath, "public/robots.txt")
	if ok, _ := util.IsExist(robotsTxt); ok {
		http.ServeFile(w, req, robotsTxt)
		return
	}

	_, err := w.Write(defaultRobotsTxt)
	if err != nil {
		log.Error("failed to write robots.txt: %v", err)
	}
}
