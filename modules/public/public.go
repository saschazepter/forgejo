// Copyright 2016 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package public

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"forgejo.org/modules/assetfs"
	"forgejo.org/modules/container"
	"forgejo.org/modules/httpcache"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/util"
)

func CustomAssets() *assetfs.Layer {
	return assetfs.Local("custom", setting.CustomPath, "public")
}

func AssetFS() *assetfs.LayeredFS {
	return assetfs.Layered(CustomAssets(), BuiltinAssets())
}

// FileHandlerFunc implements the static handler for serving files in "public" assets
func FileHandlerFunc() http.HandlerFunc {
	assetFS := AssetFS()
	return func(resp http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" && req.Method != "HEAD" {
			resp.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		handleRequest(resp, req, assetFS, req.URL.Path)
	}
}

// parseAcceptEncoding parse Accept-Encoding: deflate, gzip;q=1.0, *;q=0.5 as compress methods
func parseAcceptEncoding(val string) container.Set[string] {
	parts := strings.Split(val, ";")
	types := make(container.Set[string])
	for _, v := range strings.Split(parts[0], ",") {
		types.Add(strings.TrimSpace(v))
	}
	return types
}

// setWellKnownContentType will set the Content-Type if the file is a well-known type.
// See the comments of detectWellKnownMimeType
func setWellKnownContentType(w http.ResponseWriter, file string) {
	mimeType := detectWellKnownMimeType(path.Ext(file))
	if mimeType != "" {
		w.Header().Set("Content-Type", mimeType)
	}
}

func handleRequest(w http.ResponseWriter, req *http.Request, fs fs.FS, file string) {
	// actually, fs (http.FileSystem) is designed to be a safe interface, relative paths won't bypass its parent directory, it's also fine to do a clean here
	f, err := fs.Open(util.PathJoinRelX(file))
	if err != nil {
		if os.IsNotExist(err) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		log.Error("[Static] Open %q failed: %v", file, err)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Error("[Static] %q exists, but fails to open: %v", file, err)
		return
	}

	// need to serve index file? (no at the moment)
	if fi.IsDir() {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	serveContent(w, req, fi.Name(), fi.ModTime(), f.(io.ReadSeeker))
}

type ZstdBytesProvider interface {
	ZstdBytes() []byte
}

// serveContent serve http content
func serveContent(w http.ResponseWriter, req *http.Request, name string, modtime time.Time, content io.ReadSeeker) {
	setWellKnownContentType(w, name)

	encodings := parseAcceptEncoding(req.Header.Get("Accept-Encoding"))
	if encodings.Contains("zstd") {
		// If the file was compressed, use the bytes directly.
		if compressed, ok := content.(ZstdBytesProvider); ok {
			rdZstd := bytes.NewReader(compressed.ZstdBytes())
			if w.Header().Get("Content-Type") == "" {
				w.Header().Set("Content-Type", "application/octet-stream")
			}
			w.Header().Set("Content-Encoding", "zstd")
			httpcache.ServeContentWithCacheControl(w, req, name, modtime, rdZstd)
			return
		}
	}

	httpcache.ServeContentWithCacheControl(w, req, name, modtime, content)
	return
}
