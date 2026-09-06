#!/bin/bash

set -e
#set -x
PS4='${BASH_SOURCE[0]}:$LINENO: ${FUNCNAME[0]}:  '

#
# Those must be explicitly required and are excluded from the full list of packages because they
# would interfere with the testing fixtures.
#
excluded+='forgejo.org/models/gitea_migrations|'          # must be run before database specific tests
excluded+='forgejo.org/models/forgejo_migrations|'        # must be run before database specific tests
excluded+='forgejo.org/models/forgejo_migrations_legacy|' # must be run before database specific tests
excluded+='forgejo.org/tests/integration/migration-test|' # must be run before database specific tests
excluded+='forgejo.org/tests|'                            # only tests, no coverage to get there
excluded+='forgejo.org/tests/e2e|'                        # JavaScript is not in scope here and if it adds coverage it should not be counted
excluded+='FAKETERMINATOR'                                # do not modify

: ${COVERAGEDIR:=$(pwd)/coverage/data}
: ${COVERAGE_TEST_NAME:=unit}
: ${GO:=$(go env GOROOT)/bin/go}

DEFAULT_TEST_PACKAGES=$($GO list ./... | grep -E -v "$excluded")

COVERED_PACKAGES=forgejo.org/cmd/...,forgejo.org/models/...,forgejo.org/modules/...,forgejo.org/routers/...,forgejo.org/services/...

function run_verbose() {
  echo "$@"
  "$@"
}

function run_test() {
  local package="$1"

  local coverage="$COVERAGEDIR/$COVERAGE_TEST_NAME"
  rm -fr $coverage
  mkdir -p $coverage

  set -o pipefail
  run_verbose $GO test -timeout=120m -tags='sqlite sqlite_unlock_notify' -covermode atomic -cover $package -coverpkg $COVERED_PACKAGES $COVERAGE_TEST_ARGS -args -test.gocoverdir=$coverage |& grep --text -v 'warning: no packages being tested depend on matches for pattern'
  set +o pipefail
}

function test_packages() {
  for package in "${@:-$DEFAULT_TEST_PACKAGES}"; do
    run_test "$package"
  done
}

"$@"
