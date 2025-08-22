#!/bin/bash

: ${COVERAGEDIR:=$(pwd)/coverage/data}
: ${GO:=$(go env GOROOT)/bin/go}
ALL_PACKAGES=$($GO list ./... | sed -e 's/$/,/' | tr -d '\n' | sed -e 's/,$//')
: ${COVERED_PACKAGES:=$ALL_PACKAGES}

function run_test() {
  local package="$1"
  local coverage="$COVERAGEDIR/$COVERAGE_TEST_DATABASE/$package"

  rm -f $coverage/*
  mkdir -p $coverage
  $GO test -timeout=20m -race -tags='sqlite sqlite_unlock_notify' -cover $package -coverpkg $COVERED_PACKAGES $COVERAGE_TEST_ARGS -args -test.gocoverdir=$coverage
}

function test_packages() {
  for package in "${@:-$ALL_PACKAGES}"; do
    run_test $package
  done
}

function main() {
  run "$@"
}

"$@"
