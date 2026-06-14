NPM := $(shell command -v npm 2> /dev/null)
GH := $(shell command -v gh 2> /dev/null)

currentDir = $(shell pwd)
serverOutputDir = ${currentDir}/build/server
cliOutputDir = ${currentDir}/build/cli
cliHomebrewDir = ${currentDir}/../homebrew-lflow

## installation
install: install-go install-js
.PHONY: install

install-go:
	@echo "==> installing go dependencies"
	@go mod download
.PHONY: install-go

install-js:
ifndef NPM
	$(error npm is not installed)
endif

	@echo "==> installing js dependencies"

ifeq ($(CI), true)
	@(cd ${currentDir}/pkg/server/assets && npm ci --cache $(NPM_CACHE_DIR) --prefer-offline --unsafe-perm=true)
else
	@(cd ${currentDir}/pkg/server/assets && npm install)
endif
.PHONY: install-js

## test
test: generate-cli-schema
	@echo "==> running tests"
	@go test -tags fts5 ./...
.PHONY: test

# development
dev-server:
	@echo "==> running dev environment"
	@mkdir -p "${currentDir}/pkg/server/static"
	@cp "${currentDir}"/pkg/server/assets/static/* "${currentDir}/pkg/server/static"
	@(cd "${currentDir}/pkg/server/assets/" && ./styles/build.sh)
	@(cd "${currentDir}/pkg/server/assets/" && ./js/build.sh)
	@(cd "${currentDir}/pkg/server" && go run -ldflags "-X 'github.com/lflow/lflow/pkg/server/buildinfo.CSSFiles=main.css' -X 'github.com/lflow/lflow/pkg/server/buildinfo.JSFiles=main.js' -X 'github.com/lflow/lflow/pkg/server/buildinfo.Version=dev' -X 'github.com/lflow/lflow/pkg/server/buildinfo.Standalone=true'" --tags fts5 main.go start -port 3001)
.PHONY: dev-server

build-server:
ifndef version
	$(error version is required. Usage: make version=0.1.0 build-server)
endif

	@echo "==> building server assets"
	@(cd "${currentDir}/pkg/server/assets/" && ./styles/build.sh)
	@(cd "${currentDir}/pkg/server/assets/" && ./js/build.sh)

	@echo "==> building server"
	@mkdir -p "${serverOutputDir}"
	@go build -tags fts5 -ldflags "-X 'github.com/lflow/lflow/pkg/server/buildinfo.CSSFiles=main.css' -X 'github.com/lflow/lflow/pkg/server/buildinfo.JSFiles=main.js' -X 'github.com/lflow/lflow/pkg/server/buildinfo.Version=$(version)' -X 'github.com/lflow/lflow/pkg/server/buildinfo.Standalone=true'" -o "${serverOutputDir}/lflow-server" ./pkg/server
.PHONY: build-server

build-server-docker: build-server
ifndef version
	$(error version is required. Usage: make version=0.1.0 [platform=linux/amd64] build-server-docker)
endif

	@echo "==> building Docker image"
	@(cd ${currentDir}/host/docker && ./build.sh $(version) $(platform))
.PHONY: build-server-docker

generate-cli-schema:
	@echo "==> generating CLI database schema"
	@mkdir -p pkg/cli/database
	@touch pkg/cli/database/schema.sql
	@go run -tags fts5 ./pkg/cli/database/schema
.PHONY: generate-cli-schema

build-cli: generate-cli-schema
ifndef version
	$(error version is required. Usage: make version=0.1.0 build-cli)
endif

	@echo "==> building cli"
	@mkdir -p "${cliOutputDir}"
	@go build -tags fts5 -ldflags "-X main.apiEndpoint=http://127.0.0.1:3001/api -X main.versionTag=$(version)" -o "${cliOutputDir}/lflow" ./pkg/cli
.PHONY: build-cli

clean:
	@git clean -f
	@rm -rf build
.PHONY: clean
