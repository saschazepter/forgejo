;;; Copyright 2025 The Forgejo Authors. All rights reserved.
;;; SPDX-License-Identifier: MIT
;;;
;;; Commentary:
;;;
;;; This is a GNU Guix manifest that can be used to create a
;;; development environment to build and test Forgejo.
;;;
;;; The following is a usage example to create a containerized
;;; environment, with HOME shared for the Go cache and the network
;;; made available to fetch required Go and Node dependencies.
;;;
#|
guix shell -CNF --share=$HOME -m manifest.scm
export GOTOOLCHAIN=local     # to use the Go binary from Guix
export CC=gcc CGO_ENABLED=1
export TAGS="timetzdata sqlite sqlite_unlock_notify"
make clean
make -j$(nproc)
make test -j$(nproc)         # run unit tests
make test-sqlite -j$(nproc)  # run integration tests
make watch                   # run an instance/rebuild on changes
|#
(specifications->manifest
 (list "bash-minimal"
       "coreutils"
       "findutils"
       "gcc-toolchain"
       "git"                            ;libpcre support is required
       "git-lfs"
       "gnupg"
       "go"
       "grep"
       "make"
       "node"
       "nss-certs"
       "openssh"
       "sed"))
