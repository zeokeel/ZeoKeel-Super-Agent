# ZeoKeel Super Agent — the only build entry points.
# Every product build is CGO_ENABLED=0; see design/zeokeel-go-mygo-design.md §9
# and design/ops-runbook.md. The single cgo exception is the -race step inside
# scripts/verify-fixes.sh (a test instrument, not a product build).

export CGO_ENABLED := 0
export GOPROXY ?= https://goproxy.cn,direct

GOFLAGS_RELEASE := -tags mygo_noinspector -ldflags "-s -w"

.PHONY: all build run test vet fmt verify check nocgo cross size tidy release dist package version

all: verify build

build: ## build the desktop binary
	go build -o bin/zeokeel ./cmd/zeokeel

run: ## run the desktop app (needs a display; use xvfb-run on a headless box)
	go run ./cmd/zeokeel

test: ## run every test; needs no display. Live-model tests self-skip.
	go test ./... -count=1 -skip 'Live'

vet: ## go vet plus mygo's own build-value check
	go vet ./...
	-@command -v go >/dev/null && go tool mygo vet ./... 2>/dev/null || true

fmt:
	gofmt -l -w .

# The canonical gate: exactly what CI and scripts/release-check.sh run.
verify: ## build + test + vet + zero-cgo guard + race (single source of truth)
	sh scripts/verify-fixes.sh

check: verify ## kept for compatibility; same as `make verify`

nocgo: ## the hard rule: nothing in the dependency graph may need cgo
	sh scripts/check-nocgo.sh

cross: ## every platform we ship
	GOOS=linux   GOARCH=amd64 go build ./...
	GOOS=linux   GOARCH=arm64 go build ./...
	GOOS=darwin  GOARCH=amd64 go build ./...
	GOOS=darwin  GOARCH=arm64 go build ./...
	GOOS=windows GOARCH=amd64 go build ./...
	GOOS=windows GOARCH=arm64 go build ./...

size: ## release size, must stay under 40 MB
	go build $(GOFLAGS_RELEASE) -o bin/zeokeel ./cmd/zeokeel
	@ls -lh bin/zeokeel | awk '{print "release binary:", $$5}'

tidy:
	go mod tidy

VERSION := $(shell cat VERSION)
LDFLAGS := -s -w -X main.version=$(VERSION)

version: ## print the version being built
	@echo $(VERSION)

dist: ## build the three shippable binaries into dist/
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -tags mygo_noinspector -ldflags "$(LDFLAGS)" -o dist/zeokeel-linux-amd64 ./cmd/zeokeel
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -tags mygo_noinspector -ldflags "$(LDFLAGS)" -o dist/zeokeel-darwin-arm64 ./cmd/zeokeel
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -tags mygo_noinspector -ldflags "$(LDFLAGS)" -o dist/zeokeel-windows-amd64.exe ./cmd/zeokeel
	@ls -lh dist/

package: ## build the three shippable archives into dist/
	sh scripts/package.sh

release: verify package ## release gate: canonical verify, then package, then release checks
	sh scripts/release-check.sh
	@echo "built $(VERSION). 发布前逐条对一遍 RELEASE.md；F 组阻塞项未清空时不要发 1.0.0。"
