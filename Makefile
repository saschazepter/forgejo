ifeq ($(USE_REPO_TEST_DIR),1)

# This rule replaces the whole Makefile when we're trying to use /tmp repository temporary files
location = $(CURDIR)/$(word $(words $(MAKEFILE_LIST)),$(MAKEFILE_LIST))
self := $(location)

%:
	@tmpdir=`mktemp --tmpdir -d` ; \
	echo Using temporary directory $$tmpdir for test repositories ; \
	USE_REPO_TEST_DIR= $(MAKE) -f $(self) --no-print-directory REPO_TEST_DIR=$$tmpdir/ $@ ; \
	STATUS=$$? ; rm -r "$$tmpdir" ; exit $$STATUS

else

# This is the "normal" part of the Makefile

DIST := dist
DIST_DIRS := $(DIST)/binaries $(DIST)/release
IMPORT := forgejo.org

GO ?= $(shell go env GOROOT)/bin/go
SHASUM ?= shasum -a 256
HAS_GO := $(shell hash $(GO) > /dev/null 2>&1 && echo yes)
COMMA := ,
DIFF ?= diff --unified

ifeq ($(USE_GOTESTSUM), yes)
	GOTEST ?= gotestsum --
	GOTESTCOMPILEDRUNPREFIX ?= gotestsum --raw-command -- go tool test2json -t
	GOTESTCOMPILEDRUNSUFFIX ?= -test.v=test2json
else
	GOTEST ?= $(GO) test
	GOTESTCOMPILEDRUNPREFIX ?=
	GOTESTCOMPILEDRUNSUFFIX ?=
endif

XGO_VERSION := go-1.21.x

AIR_PACKAGE ?= github.com/air-verse/air@v1 # renovate: datasource=go
EDITORCONFIG_CHECKER_PACKAGE ?= github.com/editorconfig-checker/editorconfig-checker/v3/cmd/editorconfig-checker@v3.3.0 # renovate: datasource=go
GOFUMPT_PACKAGE ?= mvdan.cc/gofumpt@v0.8.0 # renovate: datasource=go
GOLANGCI_LINT_PACKAGE ?= github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.1.6 # renovate: datasource=go
GXZ_PACKAGE ?= github.com/ulikunitz/xz/cmd/gxz@v0.5.11 # renovate: datasource=go
SWAGGER_PACKAGE ?= github.com/go-swagger/go-swagger/cmd/swagger@v0.31.0 # renovate: datasource=go
XGO_PACKAGE ?= src.techknowlogick.com/xgo@latest
GO_LICENSES_PACKAGE ?= github.com/google/go-licenses@v1.6.0 # renovate: datasource=go
GOVULNCHECK_PACKAGE ?= golang.org/x/vuln/cmd/govulncheck@v1 # renovate: datasource=go
DEADCODE_PACKAGE ?= golang.org/x/tools/cmd/deadcode@v0.34.0 # renovate: datasource=go
GOMOCK_PACKAGE ?= go.uber.org/mock/mockgen@v0.5.2 # renovate: datasource=go
GOPLS_PACKAGE ?= golang.org/x/tools/gopls@v0.18.1 # renovate: datasource=go
RENOVATE_NPM_PACKAGE ?= renovate@40.48.4 # renovate: datasource=docker packageName=data.forgejo.org/renovate/renovate

# https://github.com/disposable-email-domains/disposable-email-domains/commits/main/
DISPOSABLE_EMAILS_SHA ?= 0c27e671231d27cf66370034d7f6818037416989 # renovate: ...

ifeq ($(HAS_GO), yes)
	CGO_EXTRA_CFLAGS := -DSQLITE_MAX_VARIABLE_NUMBER=32766
	CGO_CFLAGS ?= $(shell $(GO) env CGO_CFLAGS) $(CGO_EXTRA_CFLAGS)
endif

GOFLAGS := -v
EXECUTABLE ?= gitea

ifeq ($(shell sed --version 2>/dev/null | grep -q GNU && echo gnu),gnu)
	SED_INPLACE := sed -i
else
	SED_INPLACE := sed -i ''
endif

EXTRA_GOFLAGS ?=

MAKE_VERSION := $(shell "$(MAKE)" -v | cat | head -n 1)
MAKE_EVIDENCE_DIR := .make_evidence

ifeq ($(RACE_ENABLED),true)
	GOFLAGS += -race
	GOTESTFLAGS += -race
endif

STORED_VERSION_FILE := VERSION
HUGO_VERSION ?= 0.111.3

GITEA_COMPATIBILITY ?= gitea-1.22.0

STORED_VERSION=$(shell cat $(STORED_VERSION_FILE) 2>/dev/null)
ifneq ($(STORED_VERSION),)
  FORGEJO_VERSION ?= $(STORED_VERSION)
else
  ifneq ($(GITEA_VERSION),)
    FORGEJO_VERSION ?= $(GITEA_VERSION)
    FORGEJO_VERSION_API ?= $(GITEA_VERSION)+${GITEA_COMPATIBILITY}
  else
    # drop the "g" prefix prepended by git describe to the commit hash
    FORGEJO_VERSION ?= $(shell git describe --exclude '*-test' --tags --always 2>/dev/null | sed 's/^v//' | sed 's/\-g/-/')
    ifneq ($(FORGEJO_VERSION),)
      ifeq ($(findstring $(GITEA_COMPATIBILITY),$(FORGEJO_VERSION)),)
        FORGEJO_VERSION := $(FORGEJO_VERSION)+$(GITEA_COMPATIBILITY)
      endif
    endif
  endif
endif
FORGEJO_VERSION_MAJOR=$(shell echo $(FORGEJO_VERSION) | sed -e 's/\..*//')
FORGEJO_VERSION_MINOR=$(shell echo $(FORGEJO_VERSION) | sed -E -e 's/^([0-9]+\.[0-9]+).*/\1/')

RELEASE_VERSION ?= ${FORGEJO_VERSION}
VERSION ?= ${RELEASE_VERSION}

FORGEJO_VERSION_API ?= ${FORGEJO_VERSION}

# Strip binaries by default to reduce size, allow overriding for debugging
STRIP ?= 1
ifeq ($(STRIP),1)
	LDFLAGS := $(LDFLAGS) -s -w
endif
LDFLAGS := $(LDFLAGS) -X "main.ReleaseVersion=$(RELEASE_VERSION)" -X "main.MakeVersion=$(MAKE_VERSION)" -X "main.Version=$(FORGEJO_VERSION)" -X "main.Tags=$(TAGS)" -X "main.ForgejoVersion=$(FORGEJO_VERSION_API)"

LINUX_ARCHS ?= linux/amd64,linux/386,linux/arm-5,linux/arm-6,linux/arm64

ifeq ($(HAS_GO), yes)
	GO_TEST_PACKAGES ?= $(filter-out $(shell $(GO) list forgejo.org/models/migrations/...) $(shell $(GO) list forgejo.org/models/forgejo_migrations/...) forgejo.org/tests/integration/migration-test forgejo.org/tests forgejo.org/tests/integration forgejo.org/tests/e2e,$(shell $(GO) list ./...))
endif
REMOTE_CACHER_MODULES ?= cache nosql session queue
GO_TEST_REMOTE_CACHER_PACKAGES ?= $(addprefix forgejo.org/modules/,$(REMOTE_CACHER_MODULES))

FOMANTIC_WORK_DIR := web_src/fomantic

WEBPACK_SOURCES := $(shell find web_src/js web_src/css -type f)
WEBPACK_CONFIGS := webpack.config.js tailwind.config.js
WEBPACK_DEST := public/assets/js/index.js public/assets/css/index.css
WEBPACK_DEST_ENTRIES := public/assets/js public/assets/css public/assets/fonts

BINDATA_DEST := modules/public/bindata.go modules/options/bindata.go modules/templates/bindata.go
BINDATA_HASH := $(addsuffix .hash,$(BINDATA_DEST))

GENERATED_GO_DEST := modules/charset/invisible_gen.go modules/charset/ambiguous_gen.go

SVG_DEST_DIR := public/assets/img/svg

AIR_TMP_DIR := .air

GO_LICENSE_TMP_DIR := .go-licenses
GO_LICENSE_FILE := assets/go-licenses.json

TAGS ?=
TAGS_SPLIT := $(subst $(COMMA), ,$(TAGS))
TAGS_EVIDENCE := $(MAKE_EVIDENCE_DIR)/tags

TEST_TAGS ?= sqlite sqlite_unlock_notify

TAR_EXCLUDES := .git data indexers queues log node_modules $(EXECUTABLE) $(FOMANTIC_WORK_DIR)/node_modules $(DIST) $(MAKE_EVIDENCE_DIR) $(AIR_TMP_DIR) $(GO_LICENSE_TMP_DIR)

GO_DIRS := build cmd models modules routers services tests
WEB_DIRS := web_src/js web_src/css

STYLELINT_FILES := web_src/css web_src/js/components/*.vue
SPELLCHECK_FILES := $(GO_DIRS) $(WEB_DIRS) docs/content templates options/locale/locale_en-US.ini .github $(wildcard *.go *.js *.ts *.vue *.md *.yml *.yaml)

GO_SOURCES := $(wildcard *.go)
GO_SOURCES += $(shell find $(GO_DIRS) -type f -name "*.go" ! -path modules/options/bindata.go ! -path modules/public/bindata.go ! -path modules/templates/bindata.go)
GO_SOURCES += $(GENERATED_GO_DEST)
GO_SOURCES_NO_BINDATA := $(GO_SOURCES)

ifeq ($(HAS_GO), yes)
	MIGRATION_PACKAGES := $(shell $(GO) list forgejo.org/models/migrations/... forgejo.org/models/forgejo_migrations/...)
endif

ifeq ($(filter $(TAGS_SPLIT),bindata),bindata)
	GO_SOURCES += $(BINDATA_DEST)
	GENERATED_GO_DEST += $(BINDATA_DEST)
endif

# Force installation of playwright dependencies by setting this flag
ifdef DEPS_PLAYWRIGHT
	PLAYWRIGHT_FLAGS += --with-deps
endif

FORGEJO_API_SPEC := public/assets/forgejo/api.v1.yml

SWAGGER_SPEC := templates/swagger/v1_json.tmpl
SWAGGER_SPEC_S_TMPL := s|"basePath": *"/api/v1"|"basePath": "{{AppSubUrl \| JSEscape}}/api/v1"|g
SWAGGER_SPEC_S_JSON := s|"basePath": *"{{AppSubUrl \| JSEscape}}/api/v1"|"basePath": "/api/v1"|g
SWAGGER_EXCLUDE := code.gitea.io/sdk
SWAGGER_NEWLINE_COMMAND := -e '$$a\'
SWAGGER_SPEC_BRANDING := s|Gitea API|Forgejo API|g
SWAGGER_SPEC_LICENSE := s|"name": "MIT"|"name": "This file is distributed under the MIT license for the purpose of interoperability"|

TEST_MYSQL_HOST ?= mysql:3306
TEST_MYSQL_DBNAME ?= testgitea?multiStatements=true
TEST_MYSQL_USERNAME ?= root
TEST_MYSQL_PASSWORD ?=
TEST_PGSQL_HOST ?= pgsql:5432
TEST_PGSQL_DBNAME ?= testgitea
TEST_PGSQL_USERNAME ?= postgres
TEST_PGSQL_PASSWORD ?= postgres
TEST_PGSQL_SCHEMA ?= gtestschema

.PHONY: all
all: build

.PHONY: help
help:
	@echo "Make Routines:"
	@echo " - \"\"                               equivalent to \"build\""
	@echo " - build                            build everything"
	@echo " - frontend                         build frontend files"
	@echo " - backend                          build backend files"
	@echo " - watch                            watch everything and continuously rebuild"
	@echo " - watch-frontend                   watch frontend files and continuously rebuild"
	@echo " - watch-backend                    watch backend files and continuously rebuild"
	@echo " - clean                            delete backend and integration files"
	@echo " - clean-all                        delete backend, frontend and integration files"
	@echo " - deps                             install dependencies"
	@echo " - deps-frontend                    install frontend dependencies"
	@echo " - deps-backend                     install backend dependencies"
	@echo " - deps-tools                       install tool dependencies"
	@echo " - lint                             lint everything"
	@echo " - lint-fix                         lint everything and fix issues"
	@echo " - lint-frontend                    lint frontend files"
	@echo " - lint-frontend-fix                lint frontend files and fix issues"
	@echo " - lint-backend                     lint backend files"
	@echo " - lint-backend-fix                 lint backend files and fix issues"
	@echo " - lint-go                          lint go files"
	@echo " - lint-go-fix                      lint go files and fix issues"
	@echo " - lint-go-vet                      lint go files with vet"
	@echo " - lint-go-gopls                    lint go files with gopls"
	@echo " - lint-js                          lint js files"
	@echo " - lint-js-fix                      lint js files and fix issues"
	@echo " - lint-css                         lint css files"
	@echo " - lint-css-fix                     lint css files and fix issues"
	@echo " - lint-md                          lint markdown files"
	@echo " - lint-swagger                     lint swagger files"
	@echo " - lint-renovate                    lint renovate files"
	@echo " - checks                           run various consistency checks"
	@echo " - checks-frontend                  check frontend files"
	@echo " - checks-backend                   check backend files"
	@echo " - test                             test everything"
	@echo " - show-version-full                show the same version as the API endpoint"
	@echo " - show-version-major               show major release number only"
	@echo " - test-frontend                    test frontend files"
	@echo " - test-frontend-coverage           test frontend files and display code coverage"
	@echo " - test-backend                     test backend files"
	@echo " - test-remote-cacher               test backend files that use a remote cache"
	@echo " - test-e2e-sqlite[\#name.test.e2e] test end to end using playwright and sqlite"
	@echo " - webpack                          build webpack files"
	@echo " - svg                              build svg files"
	@echo " - fomantic                         build fomantic files"
	@echo " - generate                         run \"go generate\""
	@echo " - fmt                              format the Go code"
	@echo " - generate-license                 update license files"
	@echo " - generate-gitignore               update gitignore files"
	@echo " - generate-manpage                 generate manpage"
	@echo " - generate-gomock                  generate gomock files"
	@echo " - generate-forgejo-api             generate the forgejo API from spec"
	@echo " - forgejo-api-validate             check if the forgejo API matches the specs"
	@echo " - generate-swagger                 generate the swagger spec from code comments"
	@echo " - swagger-validate                 check if the swagger spec is valid"
	@echo " - go-licenses                      regenerate go licenses"
	@echo " - tidy                             run go mod tidy"
	@echo " - test[\#TestSpecificName]         run unit test"
	@echo " - test-sqlite[\#TestSpecificName]  run integration test for sqlite"
	@echo " - reproduce-build\#version         build a reproducible binary for the specified release version"

.PHONY: verify-version
verify-version:
ifeq ($(FORGEJO_VERSION),)
	@echo "Error: Could not determine FORGEJO_VERSION; version file $(STORED_VERSION_FILE) not present and no suitable git tag found"
	@echo 'In most cases this likely means you forgot to fetch git tags, you can fix this by executing `git fetch --tags`. If this is not possible and this is part of a custom build process, then you can set a specific version by writing it to $(STORED_VERSION_FILE) (This must be a semver compatible version).'
	@false
endif

.PHONY: show-version-full
show-version-full: verify-version
	@echo ${FORGEJO_VERSION}

.PHONY: show-version-major
show-version-major: verify-version
	@echo ${FORGEJO_VERSION_MAJOR}

.PHONY: show-version-minor
show-version-minor: verify-version
	@echo ${FORGEJO_VERSION_MINOR}

.PHONY: show-version-api
show-version-api: verify-version
	@echo ${FORGEJO_VERSION_API}

###
# Check system and environment requirements
###

.PHONY: go-check
go-check:
	$(eval MIN_GO_VERSION_STR := $(shell grep -Eo '^go\s+[0-9]+\.[0-9]+' go.mod | cut -d' ' -f2))
	$(eval MIN_GO_VERSION := $(shell printf "%03d%03d" $(shell echo '$(MIN_GO_VERSION_STR)' | tr '.' ' ')))
	$(eval GO_VERSION := $(shell printf "%03d%03d" $(shell $(GO) version | grep -Eo '[0-9]+\.[0-9]+' | tr '.' ' ');))
	@if [ "$(GO_VERSION)" -lt "$(MIN_GO_VERSION)" ]; then \
		echo "Forgejo requires Go $(MIN_GO_VERSION_STR) or greater to build. You can get it at https://go.dev/dl/"; \
		exit 1; \
	fi

.PHONY: git-check
git-check:
	@if git lfs >/dev/null 2>&1 ; then : ; else \
		echo "Forgejo requires git with lfs support to run tests." ; \
		exit 1; \
	fi

.PHONY: node-check
node-check:
	$(eval MIN_NODE_VERSION_STR := $(shell grep -Eo '"node":.*[0-9.]+"' package.json | sed -n 's/.*[^0-9.]\([0-9.]*\)"/\1/p'))
	$(eval MIN_NODE_VERSION := $(shell printf "%03d%03d%03d" $(shell echo '$(MIN_NODE_VERSION_STR)' | tr '.' ' ')))
	$(eval NODE_VERSION := $(shell printf "%03d%03d%03d" $(shell node -v | cut -c2- | tr '.' ' ');))
	$(eval NPM_MISSING := $(shell hash npm > /dev/null 2>&1 || echo 1))
	@if [ "$(NODE_VERSION)" -lt "$(MIN_NODE_VERSION)" -o "$(NPM_MISSING)" = "1" ]; then \
		echo "Forgejo requires Node.js $(MIN_NODE_VERSION_STR) or greater and npm to build. You can get it at https://nodejs.org/en/download/"; \
		exit 1; \
	fi

###
# Basic maintenance, check and lint targets
###

.PHONY: clean-all
clean-all: clean
	rm -rf $(WEBPACK_DEST_ENTRIES) node_modules

.PHONY: clean
clean:
	rm -rf $(EXECUTABLE) $(DIST) $(BINDATA_DEST) $(BINDATA_HASH) \
		integrations*.test \
		e2e*.test \
		tests/integration/gitea-integration-* \
		tests/integration/indexers-* \
		tests/mysql.ini tests/pgsql.ini man/ \
		tests/e2e/gitea-e2e-*/ \
		tests/e2e/indexers-*/ \
		tests/e2e/reports/ tests/e2e/test-artifacts/ tests/e2e/test-snapshots/

.PHONY: fmt
fmt:
	@GOFUMPT_PACKAGE=$(GOFUMPT_PACKAGE) $(GO) run build/code-batch-process.go gitea-fmt -w '{file-list}'
	$(eval TEMPLATES := $(shell find templates -type f -name '*.tmpl'))
	@# strip whitespace after '{{' or '(' and before '}}' or ')' unless there is only
	@# whitespace before it
	@$(SED_INPLACE) \
		-e 's/{{[ 	]\{1,\}/{{/g' -e '/^[ 	]\{1,\}}}/! s/[ 	]\{1,\}}}/}}/g' \
	  -e 's/([ 	]\{1,\}/(/g' -e '/^[ 	]\{1,\})/! s/[ 	]\{1,\})/)/g' \
	  $(TEMPLATES)

.PHONY: fmt-check
fmt-check: fmt
	@git diff --exit-code --color=always $(GO_SOURCES) templates $(WEB_DIRS) \
	|| (code=$$?; echo "Please run 'make fmt' and commit the result"; exit $${code})

.PHONY: $(TAGS_EVIDENCE)
$(TAGS_EVIDENCE):
	@mkdir -p $(MAKE_EVIDENCE_DIR)
	@echo "$(TAGS)" > $(TAGS_EVIDENCE)

ifneq "$(TAGS)" "$(shell cat $(TAGS_EVIDENCE) 2>/dev/null)"
TAGS_PREREQ := $(TAGS_EVIDENCE)
endif

OAPI_CODEGEN_PACKAGE ?= github.com/deepmap/oapi-codegen/cmd/oapi-codegen@v1.12.4
KIN_OPENAPI_CODEGEN_PACKAGE ?= github.com/getkin/kin-openapi/cmd/validate@v0.114.0
FORGEJO_API_SERVER = routers/api/forgejo/v1/generated.go

.PHONY: generate-forgejo-api
generate-forgejo-api: $(FORGEJO_API_SPEC)
	$(GO) run $(OAPI_CODEGEN_PACKAGE) -package v1 -generate chi-server,types $< > $(FORGEJO_API_SERVER)

.PHONY: forgejo-api-check
forgejo-api-check: generate-forgejo-api
	@git diff --exit-code --color=always $(FORGEJO_API_SERVER) \
	|| (code=$$?; echo "Please run 'make generate-forgejo-api' and commit the result"; exit $${code})

.PHONY: forgejo-api-validate
forgejo-api-validate:
	$(GO) run $(KIN_OPENAPI_CODEGEN_PACKAGE) $(FORGEJO_API_SPEC)

.PHONY: generate-swagger
generate-swagger: $(SWAGGER_SPEC)

$(SWAGGER_SPEC): $(GO_SOURCES_NO_BINDATA)
	$(GO) run $(SWAGGER_PACKAGE) generate spec -x "$(SWAGGER_EXCLUDE)" -o './$(SWAGGER_SPEC)'
	$(SED_INPLACE) '$(SWAGGER_SPEC_S_TMPL)' './$(SWAGGER_SPEC)'
	$(SED_INPLACE) $(SWAGGER_NEWLINE_COMMAND) './$(SWAGGER_SPEC)'
	$(SED_INPLACE) '$(SWAGGER_SPEC_BRANDING)' './$(SWAGGER_SPEC)'
	$(SED_INPLACE) '$(SWAGGER_SPEC_LICENSE)' './$(SWAGGER_SPEC)'

.PHONY: swagger-check
swagger-check: generate-swagger
	@git diff --exit-code --color=always '$(SWAGGER_SPEC)' \
	|| (code=$$?; echo "Please run 'make generate-swagger' and commit the result"; exit $${code})

.PHONY: swagger-validate
swagger-validate:
	$(SED_INPLACE) '$(SWAGGER_SPEC_S_JSON)' './$(SWAGGER_SPEC)'
	$(GO) run $(SWAGGER_PACKAGE) validate './$(SWAGGER_SPEC)'
	$(SED_INPLACE) '$(SWAGGER_SPEC_S_TMPL)' './$(SWAGGER_SPEC)'

.PHONY: checks
checks: checks-frontend checks-backend

.PHONY: checks-frontend
checks-frontend: lockfile-check svg-check

.PHONY: checks-backend
checks-backend: tidy-check swagger-check fmt-check swagger-validate security-check

.PHONY: lint
lint: lint-frontend lint-backend

.PHONY: lint-fix
lint-fix: lint-frontend-fix lint-backend-fix

.PHONY: lint-frontend
lint-frontend: lint-js lint-css

.PHONY: lint-frontend-fix
lint-frontend-fix: lint-js-fix lint-css-fix

.PHONY: lint-backend
lint-backend: lint-go lint-go-vet lint-editorconfig lint-renovate lint-locale lint-locale-usage lint-disposable-emails

.PHONY: lint-backend-fix
lint-backend-fix: lint-go-fix lint-go-vet lint-editorconfig lint-disposable-emails-fix

.PHONY: lint-js
lint-js: node_modules
	npx eslint --color --max-warnings=0

.PHONY: lint-js-fix
lint-js-fix: node_modules
	npx eslint --color --max-warnings=0 --fix

.PHONY: lint-css
lint-css: node_modules
	npx stylelint --color --max-warnings=0 $(STYLELINT_FILES)

.PHONY: lint-css-fix
lint-css-fix: node_modules
	npx stylelint --color --max-warnings=0 $(STYLELINT_FILES) --fix

.PHONY: lint-swagger
lint-swagger: node_modules
	npx spectral lint -q -F hint $(SWAGGER_SPEC)

.PHONY: lint-renovate
lint-renovate: node_modules
	npx --yes --package $(RENOVATE_NPM_PACKAGE) -- renovate-config-validator > .lint-renovate 2>&1 || true
	@if grep --quiet --extended-regexp -e '^( ERROR:)' .lint-renovate ; then cat .lint-renovate ; rm .lint-renovate ; exit 1 ; fi
	@rm .lint-renovate

.PHONY: lint-locale
lint-locale:
	$(GO) run build/lint-locale/lint-locale.go

.PHONY: lint-locale-usage
lint-locale-usage:
	$(GO) run build/lint-locale-usage/lint-locale-usage.go

.PHONY: lint-md
lint-md: node_modules
	npx markdownlint docs *.md

RUN_DEADCODE = $(GO) run $(DEADCODE_PACKAGE) -generated=false -f='{{println .Path}}{{range .Funcs}}{{printf "\t%s\n" .Name}}{{end}}{{println}}' -test forgejo.org

.PHONY: lint-go
lint-go:
	$(GO) run $(GOLANGCI_LINT_PACKAGE) run $(GOLANGCI_LINT_ARGS)
	$(RUN_DEADCODE) > .cur-deadcode-out
	@$(DIFF) .deadcode-out .cur-deadcode-out \
	|| (code=$$?; echo "Please run 'make lint-go-fix' and commit the result"; exit $${code})

.PHONY: lint-go-fix
lint-go-fix:
	$(GO) run $(GOLANGCI_LINT_PACKAGE) run $(GOLANGCI_LINT_ARGS) --fix
	$(RUN_DEADCODE) > .deadcode-out

.PHONY: lint-go-vet
lint-go-vet:
	@echo "Running go vet..."
	@$(GO) vet ./...

.PHONY: lint-go-gopls
lint-go-gopls:
	@echo "Running gopls check..."
	@GO=$(GO) GOPLS_PACKAGE=$(GOPLS_PACKAGE) tools/lint-go-gopls.sh $(GO_SOURCES_NO_BINDATA)

.PHONY: lint-editorconfig
lint-editorconfig:
	$(GO) run $(EDITORCONFIG_CHECKER_PACKAGE) templates .forgejo/workflows

.PHONY: lint-disposable-emails
lint-disposable-emails:
	$(GO) run build/generate-disposable-email.go -check -r $(DISPOSABLE_EMAILS_SHA)

.PHONY: lint-disposable-emails-fix
lint-disposable-emails-fix:
	$(GO) run build/generate-disposable-email.go -r $(DISPOSABLE_EMAILS_SHA)

.PHONY: security-check
security-check:
	go run $(GOVULNCHECK_PACKAGE) -show color ./...

###
# Development and testing targets
###

.PHONY: watch
watch:
	@bash tools/watch.sh

.PHONY: watch-frontend
watch-frontend: node-check node_modules
	@rm -rf $(WEBPACK_DEST_ENTRIES)
	NODE_ENV=development npx webpack --watch --progress

.PHONY: watch-backend
watch-backend: go-check
	GITEA_RUN_MODE=dev $(GO) run $(AIR_PACKAGE) -c .air.toml

.PHONY: test
test: test-frontend test-backend

.PHONY: test-backend
test-backend:
	@echo "Running go test with $(GOTESTFLAGS) -tags '$(TEST_TAGS)'..."
	@$(GOTEST) $(GOTESTFLAGS) -tags='$(TEST_TAGS)' $(GO_TEST_PACKAGES)

.PHONY: test-remote-cacher
test-remote-cacher:
	@echo "Running go test with $(GOTESTFLAGS) -tags '$(TEST_TAGS)'..."
	@$(GOTEST) $(GOTESTFLAGS) -tags='$(TEST_TAGS)' $(GO_TEST_REMOTE_CACHER_PACKAGES)

.PHONY: test-frontend
test-frontend: node_modules
	npx vitest

.PHONY: test-frontend-coverage
test-frontend-coverage: node_modules
	npx vitest --coverage --coverage.include 'web_src/**'

.PHONY: test-check
test-check:
	@echo "Running test-check...";
	@diff=$$(git status -s); \
	if [ -n "$$diff" ]; then \
		echo "make test-backend has changed files in the source tree:"; \
		echo "$${diff}"; \
		echo "You should change the tests to create these files in a temporary directory."; \
		echo "Do not simply add these files to .gitignore"; \
		exit 1; \
	fi

.PHONY: test\#%
test\#%:
	@echo "Running go test with $(GOTESTFLAGS) -tags '$(TEST_TAGS)'..."
	@$(GOTEST) $(GOTESTFLAGS) -tags='$(TEST_TAGS)' -run $(subst .,/,$*) $(GO_TEST_PACKAGES)

.PHONY: coverage
coverage:
	grep '^\(mode: .*\)\|\(.*:[0-9]\+\.[0-9]\+,[0-9]\+\.[0-9]\+ [0-9]\+ [0-9]\+\)$$' coverage.out > coverage-bodged.out
	grep '^\(mode: .*\)\|\(.*:[0-9]\+\.[0-9]\+,[0-9]\+\.[0-9]\+ [0-9]\+ [0-9]\+\)$$' integration.coverage.out > integration.coverage-bodged.out
	$(GO) run build/gocovmerge.go integration.coverage-bodged.out coverage-bodged.out > coverage.all

.PHONY: unit-test-coverage
unit-test-coverage:
	@echo "Running unit-test-coverage $(GOTESTFLAGS) -tags '$(TEST_TAGS)'..."
	@$(GOTEST) $(GOTESTFLAGS) -timeout=20m -tags='$(TEST_TAGS)' -cover -coverprofile coverage.out $(GO_TEST_PACKAGES) && echo "\n==>\033[32m Ok\033[m\n" || exit 1

.PHONY: tidy
tidy:
	$(eval MIN_GO_VERSION := $(shell grep -Eo '^go\s+[0-9]+\.[0-9.]+' go.mod | cut -d' ' -f2))
	$(GO) mod tidy -compat=$(MIN_GO_VERSION)
	@$(MAKE) --no-print-directory $(GO_LICENSE_FILE)

vendor: go.mod go.sum
	$(GO) mod vendor
	@touch vendor

.PHONY: tidy-check
tidy-check: tidy
	@git diff --exit-code --color=always go.mod go.sum $(GO_LICENSE_FILE) \
	|| (code=$$?; echo "Please run 'make tidy' and commit the result"; exit $${code})

.PHONY: go-licenses
go-licenses: $(GO_LICENSE_FILE)

$(GO_LICENSE_FILE): go.mod go.sum
	-$(GO) run $(GO_LICENSES_PACKAGE) save . --force --ignore forgejo.org --save_path=$(GO_LICENSE_TMP_DIR) 2>/dev/null
	$(GO) run build/generate-go-licenses.go $(GO_LICENSE_TMP_DIR) $(GO_LICENSE_FILE)
	@rm -rf $(GO_LICENSE_TMP_DIR)

generate-ini-sqlite:
	sed -e 's|{{REPO_TEST_DIR}}|${REPO_TEST_DIR}|g' \
		-e 's|{{TEST_LOGGER}}|$(or $(TEST_LOGGER),test$(COMMA)file)|g' \
		-e 's|{{TEST_TYPE}}|$(or $(TEST_TYPE),integration)|g' \
			tests/sqlite.ini.tmpl > tests/sqlite.ini

.PHONY: test-sqlite
test-sqlite: integrations.sqlite.test generate-ini-sqlite
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/sqlite.ini $(GOTESTCOMPILEDRUNPREFIX) ./integrations.sqlite.test $(GOTESTCOMPILEDRUNSUFFIX)

.PHONY: test-sqlite\#%
test-sqlite\#%: integrations.sqlite.test generate-ini-sqlite
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/sqlite.ini $(GOTESTCOMPILEDRUNPREFIX) ./integrations.sqlite.test $(GOTESTCOMPILEDRUNSUFFIX) -test.run $(subst .,/,$*)

.PHONY: test-sqlite-migration
test-sqlite-migration:  migrations.sqlite.test migrations.individual.sqlite.test

generate-ini-mysql:
	sed -e 's|{{TEST_MYSQL_HOST}}|${TEST_MYSQL_HOST}|g' \
		-e 's|{{TEST_MYSQL_DBNAME}}|${TEST_MYSQL_DBNAME}|g' \
		-e 's|{{TEST_MYSQL_USERNAME}}|${TEST_MYSQL_USERNAME}|g' \
		-e 's|{{TEST_MYSQL_PASSWORD}}|${TEST_MYSQL_PASSWORD}|g' \
		-e 's|{{REPO_TEST_DIR}}|${REPO_TEST_DIR}|g' \
		-e 's|{{TEST_LOGGER}}|$(or $(TEST_LOGGER),test$(COMMA)file)|g' \
		-e 's|{{TEST_TYPE}}|$(or $(TEST_TYPE),integration)|g' \
			tests/mysql.ini.tmpl > tests/mysql.ini

.PHONY: test-mysql
test-mysql: integrations.mysql.test generate-ini-mysql
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/mysql.ini $(GOTESTCOMPILEDRUNPREFIX) ./integrations.mysql.test $(GOTESTCOMPILEDRUNSUFFIX)

.PHONY: test-mysql\#%
test-mysql\#%: integrations.mysql.test generate-ini-mysql
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/mysql.ini $(GOTESTCOMPILEDRUNPREFIX) ./integrations.mysql.test $(GOTESTCOMPILEDRUNSUFFIX) -test.run $(subst .,/,$*)

.PHONY: test-mysql-migration
test-mysql-migration: migrations.mysql.test migrations.individual.mysql.test

generate-ini-pgsql:
	sed -e 's|{{TEST_PGSQL_HOST}}|${TEST_PGSQL_HOST}|g' \
		-e 's|{{TEST_PGSQL_DBNAME}}|${TEST_PGSQL_DBNAME}|g' \
		-e 's|{{TEST_PGSQL_USERNAME}}|${TEST_PGSQL_USERNAME}|g' \
		-e 's|{{TEST_PGSQL_PASSWORD}}|${TEST_PGSQL_PASSWORD}|g' \
		-e 's|{{TEST_PGSQL_SCHEMA}}|${TEST_PGSQL_SCHEMA}|g' \
		-e 's|{{REPO_TEST_DIR}}|${REPO_TEST_DIR}|g' \
		-e 's|{{TEST_LOGGER}}|$(or $(TEST_LOGGER),test$(COMMA)file)|g' \
		-e 's|{{TEST_TYPE}}|$(or $(TEST_TYPE),integration)|g' \
		-e 's|{{TEST_STORAGE_TYPE}}|$(or $(TEST_STORAGE_TYPE),minio)|g' \
			tests/pgsql.ini.tmpl > tests/pgsql.ini

.PHONY: test-pgsql
test-pgsql: integrations.pgsql.test generate-ini-pgsql
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/pgsql.ini $(GOTESTCOMPILEDRUNPREFIX) ./integrations.pgsql.test $(GOTESTCOMPILEDRUNSUFFIX)

.PHONY: test-pgsql\#%
test-pgsql\#%: integrations.pgsql.test generate-ini-pgsql
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/pgsql.ini $(GOTESTCOMPILEDRUNPREFIX) ./integrations.pgsql.test $(GOTESTCOMPILEDRUNSUFFIX) -test.run $(subst .,/,$*)

.PHONY: test-pgsql-migration
test-pgsql-migration: migrations.pgsql.test migrations.individual.pgsql.test

.PHONY: playwright
playwright: deps-frontend
	npx playwright install $(PLAYWRIGHT_FLAGS)

.PHONY: test-e2e%
test-e2e%: TEST_TYPE ?= e2e
	# Clear display env variable. Otherwise, chromium tests can fail.
	DISPLAY=

.PHONY: test-e2e
test-e2e: test-e2e-sqlite

.PHONY: test-e2e-sqlite
test-e2e-sqlite: playwright e2e.sqlite.test generate-ini-sqlite
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/sqlite.ini $(GOTESTCOMPILEDRUNPREFIX) ./e2e.sqlite.test $(GOTESTCOMPILEDRUNSUFFIX) -test.run TestE2e

.PHONY: test-e2e-sqlite\#%
test-e2e-sqlite\#%: playwright e2e.sqlite.test generate-ini-sqlite
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/sqlite.ini $(GOTESTCOMPILEDRUNPREFIX) ./e2e.sqlite.test $(GOTESTCOMPILEDRUNSUFFIX) -test.run TestE2e/$*

.PHONY: test-e2e-sqlite-firefox\#%
test-e2e-sqlite-firefox\#%: playwright e2e.sqlite.test generate-ini-sqlite
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/sqlite.ini PLAYWRIGHT_PROJECT=firefox $(GOTESTCOMPILEDRUNPREFIX) ./e2e.sqlite.test $(GOTESTCOMPILEDRUNSUFFIX) -test.run TestE2e/$*

.PHONY: test-e2e-mysql
test-e2e-mysql: playwright e2e.mysql.test generate-ini-mysql
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/mysql.ini $(GOTESTCOMPILEDRUNPREFIX) ./e2e.mysql.test $(GOTESTCOMPILEDRUNSUFFIX) -test.run TestE2e

.PHONY: test-e2e-mysql\#%
test-e2e-mysql\#%: playwright e2e.mysql.test generate-ini-mysql
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/mysql.ini $(GOTESTCOMPILEDRUNPREFIX) ./e2e.mysql.test $(GOTESTCOMPILEDRUNSUFFIX) -test.run TestE2e/$*

.PHONY: test-e2e-pgsql
test-e2e-pgsql: playwright e2e.pgsql.test generate-ini-pgsql
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/pgsql.ini $(GOTESTCOMPILEDRUNPREFIX) ./e2e.pgsql.test $(GOTESTCOMPILEDRUNSUFFIX) -test.run TestE2e

.PHONY: test-e2e-pgsql\#%
test-e2e-pgsql\#%: playwright e2e.pgsql.test generate-ini-pgsql
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/pgsql.ini $(GOTESTCOMPILEDRUNPREFIX) ./e2e.pgsql.test $(GOTESTCOMPILEDRUNSUFFIX) -test.run TestE2e/$*

.PHONY: test-e2e-debugserver
test-e2e-debugserver: e2e.sqlite.test generate-ini-sqlite
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/sqlite.ini ./e2e.sqlite.test -test.run TestDebugserver -test.timeout 24h

.PHONY: bench-sqlite
bench-sqlite: integrations.sqlite.test generate-ini-sqlite
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/sqlite.ini ./integrations.sqlite.test -test.cpuprofile=cpu.out -test.run DontRunTests -test.bench .

.PHONY: bench-mysql
bench-mysql: integrations.mysql.test generate-ini-mysql
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/mysql.ini ./integrations.mysql.test -test.cpuprofile=cpu.out -test.run DontRunTests -test.bench .

.PHONY: bench-pgsql
bench-pgsql: integrations.pgsql.test generate-ini-pgsql
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/pgsql.ini ./integrations.pgsql.test -test.cpuprofile=cpu.out -test.run DontRunTests -test.bench .

.PHONY: integration-test-coverage
integration-test-coverage: integrations.cover.test generate-ini-mysql
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/mysql.ini ./integrations.cover.test -test.coverprofile=integration.coverage.out

.PHONY: integration-test-coverage-sqlite
integration-test-coverage-sqlite: integrations.cover.sqlite.test generate-ini-sqlite
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/sqlite.ini ./integrations.cover.sqlite.test -test.coverprofile=integration.coverage.out

integrations.mysql.test: git-check $(GO_SOURCES)
	$(GOTEST) $(GOTESTFLAGS) -c forgejo.org/tests/integration -o integrations.mysql.test

integrations.pgsql.test: git-check $(GO_SOURCES)
	$(GOTEST) $(GOTESTFLAGS) -c forgejo.org/tests/integration -o integrations.pgsql.test

integrations.sqlite.test: git-check $(GO_SOURCES)
	$(GOTEST) $(GOTESTFLAGS) -c forgejo.org/tests/integration -o integrations.sqlite.test -tags '$(TEST_TAGS)'

integrations.cover.test: git-check $(GO_SOURCES)
	$(GOTEST) $(GOTESTFLAGS) -c forgejo.org/tests/integration -coverpkg $(shell echo $(GO_TEST_PACKAGES) | tr ' ' ',') -o integrations.cover.test

integrations.cover.sqlite.test: git-check $(GO_SOURCES)
	$(GOTEST) $(GOTESTFLAGS) -c forgejo.org/tests/integration -coverpkg $(shell echo $(GO_TEST_PACKAGES) | tr ' ' ',') -o integrations.cover.sqlite.test -tags '$(TEST_TAGS)'

.PHONY: migrations.mysql.test
migrations.mysql.test: $(GO_SOURCES) generate-ini-mysql
	$(GOTEST) $(GOTESTFLAGS) -c forgejo.org/tests/integration/migration-test -o migrations.mysql.test
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/mysql.ini $(GOTESTCOMPILEDRUNPREFIX) ./migrations.mysql.test $(GOTESTCOMPILEDRUNSUFFIX)

.PHONY: migrations.pgsql.test
migrations.pgsql.test: $(GO_SOURCES) generate-ini-pgsql
	$(GOTEST) $(GOTESTFLAGS) -c forgejo.org/tests/integration/migration-test -o migrations.pgsql.test
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/pgsql.ini $(GOTESTCOMPILEDRUNPREFIX) ./migrations.pgsql.test $(GOTESTCOMPILEDRUNSUFFIX)

.PHONY: migrations.sqlite.test
migrations.sqlite.test: $(GO_SOURCES) generate-ini-sqlite
	$(GOTEST) $(GOTESTFLAGS) -c forgejo.org/tests/integration/migration-test -o migrations.sqlite.test -tags '$(TEST_TAGS)'
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/sqlite.ini $(GOTESTCOMPILEDRUNPREFIX) ./migrations.sqlite.test $(GOTESTCOMPILEDRUNSUFFIX)

.PHONY: migrations.individual.mysql.test
migrations.individual.mysql.test: $(GO_SOURCES)
	for pkg in $(MIGRATION_PACKAGES); do \
		GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/mysql.ini $(GOTEST) $(GOTESTFLAGS) -tags '$(TEST_TAGS)' $$pkg || exit 1; \
	done

.PHONY: migrations.individual.sqlite.test\#%
migrations.individual.sqlite.test\#%: $(GO_SOURCES) generate-ini-sqlite
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/sqlite.ini $(GOTEST) $(GOTESTFLAGS) -tags '$(TEST_TAGS)' forgejo.org/models/migrations/$*

.PHONY: migrations.individual.pgsql.test
migrations.individual.pgsql.test: $(GO_SOURCES)
	for pkg in $(MIGRATION_PACKAGES); do \
		GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/pgsql.ini $(GOTEST) $(GOTESTFLAGS) -tags '$(TEST_TAGS)' $$pkg || exit 1;\
	done

.PHONY: migrations.individual.pgsql.test\#%
migrations.individual.pgsql.test\#%: $(GO_SOURCES) generate-ini-pgsql
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/pgsql.ini $(GOTEST) $(GOTESTFLAGS) -tags '$(TEST_TAGS)' forgejo.org/models/migrations/$*

.PHONY: migrations.individual.sqlite.test
migrations.individual.sqlite.test: $(GO_SOURCES) generate-ini-sqlite
	for pkg in $(MIGRATION_PACKAGES); do \
		GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/sqlite.ini $(GOTEST) $(GOTESTFLAGS) -tags '$(TEST_TAGS)' $$pkg || exit 1; \
	done

.PHONY: migrations.individual.sqlite.test\#%
migrations.individual.sqlite.test\#%: $(GO_SOURCES) generate-ini-sqlite
	GITEA_ROOT="$(CURDIR)" GITEA_CONF=tests/sqlite.ini $(GOTEST) $(GOTESTFLAGS) -tags '$(TEST_TAGS)' forgejo.org/models/migrations/$*

e2e.mysql.test: $(GO_SOURCES)
	$(GOTEST) $(GOTESTFLAGS) -c forgejo.org/tests/e2e -o e2e.mysql.test

e2e.pgsql.test: $(GO_SOURCES)
	$(GOTEST) $(GOTESTFLAGS) -c forgejo.org/tests/e2e -o e2e.pgsql.test

e2e.sqlite.test: $(GO_SOURCES)
	$(GOTEST) $(GOTESTFLAGS) -c forgejo.org/tests/e2e -o e2e.sqlite.test -tags '$(TEST_TAGS)'

.PHONY: check
check: test

###
# Production / build targets
###

.PHONY: install $(TAGS_PREREQ)
install: $(wildcard *.go) | verify-version
	CGO_CFLAGS="$(CGO_CFLAGS)" $(GO) install -v -tags '$(TAGS)' -ldflags '$(LDFLAGS)'

.PHONY: build
build: frontend backend

.PHONY: frontend
frontend: $(WEBPACK_DEST)

.PHONY: backend
backend: go-check generate-backend $(EXECUTABLE)

# We generate the backend before the frontend in case we in future we want to generate things in the frontend from generated files in backend
.PHONY: generate
generate: generate-backend

.PHONY: generate-backend
generate-backend: $(TAGS_PREREQ) generate-go

.PHONY: generate-go
generate-go: $(TAGS_PREREQ)
	@echo "Running go generate..."
	@CC= GOOS= GOARCH= CGO_ENABLED=0 $(GO) generate -tags '$(TAGS)' ./...

.PHONY: merge-locales
merge-locales:
	@echo "NOT NEEDED: THIS IS A NOOP AS OF Forgejo 7.0 BUT KEPT FOR BACKWARD COMPATIBILITY"

$(EXECUTABLE): $(GO_SOURCES) $(TAGS_PREREQ) | verify-version
	CGO_CFLAGS="$(CGO_CFLAGS)" $(GO) build $(GOFLAGS) $(EXTRA_GOFLAGS) -tags '$(TAGS)' -ldflags '$(LDFLAGS)' -o $@

forgejo: $(EXECUTABLE)
	ln -f $(EXECUTABLE) forgejo

static-executable: $(GO_SOURCES) $(TAGS_PREREQ) | verify-version
	CGO_CFLAGS="$(CGO_CFLAGS)" $(GO) build $(GOFLAGS) $(EXTRA_GOFLAGS) -tags 'netgo osusergo $(TAGS)' -ldflags '-linkmode external -extldflags "-static" $(LDFLAGS)' -o $(EXECUTABLE)

.PHONY: release
release: frontend generate release-linux release-copy release-compress vendor release-sources release-check

# just the sources, with all assets builtin and frontend resources generated
sources-tarbal: frontend generate vendor release-sources release-check

$(DIST_DIRS):
	mkdir -p $(DIST_DIRS)

.PHONY: release-linux
release-linux: | $(DIST_DIRS) verify-version
	CGO_CFLAGS="$(CGO_CFLAGS)" $(GO) run $(XGO_PACKAGE) -go $(XGO_VERSION) -dest $(DIST)/binaries -tags 'netgo osusergo $(TAGS)' -ldflags '-linkmode external -extldflags "-static" $(LDFLAGS)' -targets '$(LINUX_ARCHS)' -out forgejo-$(VERSION) .
ifeq ($(CI),true)
	cp /build/* $(DIST)/binaries
endif

.PHONY: release-darwin
release-darwin: | $(DIST_DIRS) verify-version
	CGO_CFLAGS="$(CGO_CFLAGS)" $(GO) run $(XGO_PACKAGE) -go $(XGO_VERSION) -dest $(DIST)/binaries -tags 'netgo osusergo $(TAGS)' -ldflags '$(LDFLAGS)' -targets 'darwin-10.12/amd64,darwin-10.12/arm64' -out gitea-$(VERSION) .

.PHONY: release-freebsd
release-freebsd: | $(DIST_DIRS) verify-version
	CGO_CFLAGS="$(CGO_CFLAGS)" $(GO) run $(XGO_PACKAGE) -go $(XGO_VERSION) -dest $(DIST)/binaries -tags 'netgo osusergo $(TAGS)' -ldflags '$(LDFLAGS)' -targets 'freebsd/amd64' -out gitea-$(VERSION) .

.PHONY: release-copy
release-copy: | $(DIST_DIRS)
	cd $(DIST); for file in `find . -type f -name "*"`; do cp $${file} ./release/; done;

.PHONY: release-check
release-check: | $(DIST_DIRS)
	cd $(DIST)/release/; for file in `find . -type f -name "*"`; do echo "checksumming $${file}" && $(SHASUM) `echo $${file} | sed 's/^..//'` > $${file}.sha256; done;

.PHONY: release-compress
release-compress: | $(DIST_DIRS)
	cd $(DIST)/release/; for file in `find . -type f -name "*"`; do echo "compressing $${file}" && $(GO) run $(GXZ_PACKAGE) -k -9 $${file}; done;

.PHONY: release-sources
release-sources: | $(DIST_DIRS)
	echo $(VERSION) > $(STORED_VERSION_FILE)
# bsdtar needs a ^ to prevent matching subdirectories
	$(eval EXCL := --exclude=$(shell tar --help | grep -q bsdtar && echo "^")./)
# use transform to a add a release-folder prefix; in bsdtar the transform parameter equivalent is -s
	$(eval TRANSFORM := $(shell tar --help | grep -q bsdtar && echo "-s '/^./forgejo-src-$(VERSION)/'" || echo "--transform 's|^./|forgejo-src-$(VERSION)/|'"))
	tar $(addprefix $(EXCL),$(TAR_EXCLUDES)) $(TRANSFORM) -czf $(DIST)/release/forgejo-src-$(VERSION).tar.gz .
	rm -f $(STORED_VERSION_FILE)

.PHONY: release-docs
release-docs: | $(DIST_DIRS) docs
	tar -czf $(DIST)/release/gitea-docs-$(VERSION).tar.gz -C ./docs .

.PHONY: reproduce-build
reproduce-build:
# Start building the Dockerfile with the RELEASE_VERSION tag set. GOPROXY is set
# for convenience, because the default of the Dockerfile is `direct` which can be
# quite slow.
	@docker build --build-arg="RELEASE_VERSION=$(RELEASE_VERSION)" --build-arg="GOPROXY=$(shell $(GO) env GOPROXY)" --tag "forgejo-reproducibility" .
	@id=$$(docker create forgejo-reproducibility); \
	docker cp $$id:/app/gitea/gitea ./forgejo; \
	docker rm -v $$id; \
	docker image rm forgejo-reproducibility:latest

.PHONY: reproduce-build\#%
reproduce-build\#%:
	@git switch -d "$*"
# All the current variables are based on information before the git checkout happened.
# Call the makefile again, so these variables are correct and can be used for building
# a reproducible binary. Always execute git switch -, to go back to the previous branch.
	@make reproduce-build; \
	(code=$$?; git switch -; exit $${code})

###
# Dependency management
###

.PHONY: deps
deps: deps-frontend deps-backend deps-tools

.PHONY: deps-frontend
deps-frontend: node_modules

.PHONY: deps-backend
deps-backend:
	$(GO) mod download

.PHONY: deps-tools
deps-tools:
	$(GO) install $(AIR_PACKAGE)
	$(GO) install $(EDITORCONFIG_CHECKER_PACKAGE)
	$(GO) install $(GOFUMPT_PACKAGE)
	$(GO) install $(GOLANGCI_LINT_PACKAGE)
	$(GO) install $(GXZ_PACKAGE)
	$(GO) install $(SWAGGER_PACKAGE)
	$(GO) install $(XGO_PACKAGE)
	$(GO) install $(GO_LICENSES_PACKAGE)
	$(GO) install $(GOVULNCHECK_PACKAGE)
	$(GO) install $(GOMOCK_PACKAGE)
	$(GO) install $(GOPLS_PACKAGE)

node_modules: package-lock.json
	npm install --no-save
	@touch node_modules

.venv: poetry.lock
	poetry install
	@touch .venv

.PHONY: fomantic
fomantic:
	rm -rf $(FOMANTIC_WORK_DIR)/build
	cd $(FOMANTIC_WORK_DIR) && npm install --no-save
	cp -f $(FOMANTIC_WORK_DIR)/theme.config.less $(FOMANTIC_WORK_DIR)/node_modules/fomantic-ui/src/theme.config
	cp -rf $(FOMANTIC_WORK_DIR)/_site $(FOMANTIC_WORK_DIR)/node_modules/fomantic-ui/src/
	$(SED_INPLACE) -e 's/  overrideBrowserslist\r/  overrideBrowserslist: ["defaults"]\r/g' $(FOMANTIC_WORK_DIR)/node_modules/fomantic-ui/tasks/config/tasks.js
	cd $(FOMANTIC_WORK_DIR) && npx gulp -f node_modules/fomantic-ui/gulpfile.js build
	# fomantic uses "touchstart" as click event for some browsers, it's not ideal, so we force fomantic to always use "click" as click event
	$(SED_INPLACE) -e 's/clickEvent[ \t]*=/clickEvent = "click", unstableClickEvent =/g' $(FOMANTIC_WORK_DIR)/build/semantic.js
	$(SED_INPLACE) -e 's/\r//g' $(FOMANTIC_WORK_DIR)/build/semantic.css $(FOMANTIC_WORK_DIR)/build/semantic.js
	rm -f $(FOMANTIC_WORK_DIR)/build/*.min.*

.PHONY: webpack
webpack: $(WEBPACK_DEST)

$(WEBPACK_DEST): $(WEBPACK_SOURCES) $(WEBPACK_CONFIGS) package-lock.json
	@$(MAKE) -s node-check node_modules
	@rm -rf $(WEBPACK_DEST_ENTRIES)
	@echo "Running webpack..."
	@BROWSERSLIST_IGNORE_OLD_DATA=true npx webpack
	@touch $(WEBPACK_DEST)

.PHONY: svg
svg: node-check | node_modules
	rm -rf $(SVG_DEST_DIR)
	node tools/generate-svg.js

.PHONY: svg-check
svg-check: svg
	@git add $(SVG_DEST_DIR)
	@git diff --exit-code --color=always --cached $(SVG_DEST_DIR) \
	|| (code=$$?; echo "Please run 'make svg' and commit the result"; exit $${code})

.PHONY: lockfile-check
lockfile-check:
	npm install --package-lock-only
	@git diff --exit-code --color=always package-lock.json \
	|| (code=$$?; echo "Please run 'npm install --package-lock-only' and commit the result"; exit $${code})

.PHONY: generate-license
generate-license:
	$(GO) run build/generate-licenses.go

.PHONY: generate-gitignore
generate-gitignore:
	$(GO) run build/generate-gitignores.go

.PHONY: generate-gomock
generate-gomock:
	$(GO) run $(GOMOCK_PACKAGE) -package mock -destination ./modules/queue/mock/redisuniversalclient.go forgejo.org/modules/nosql RedisClient

.PHONY: generate-images
generate-images: | node_modules
	node tools/generate-images.js

.PHONY: generate-manpage
generate-manpage:
	@[ -f gitea ] || make backend
	@mkdir -p man/man1/ man/man5
	@./gitea docs --man > man/man1/gitea.1
	@gzip -9 man/man1/gitea.1 && echo man/man1/gitea.1.gz created
	@#TODO A small script that formats config-cheat-sheet.en-us.md nicely for use as a config man page

# This endif closes the if at the top of the file
endif

# Disable parallel execution because it would break some targets that don't
# specify exact dependencies like 'backend' which does currently not depend
# on 'frontend' to enable Node.js-less builds from source tarballs.
.NOTPARALLEL:
