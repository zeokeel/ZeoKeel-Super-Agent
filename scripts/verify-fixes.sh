#!/bin/sh
# verify-fixes.sh — the one gate for "the repository is green".
#
# Contract (Wave 1):
#   * This script is the single source of truth for the mechanical gate.
#     CI calls it, `make verify` calls it, scripts/release-check.sh calls it.
#     Do not add checks to CI that are not here; do not add checks here that
#     CI cannot run headless.
#   * Product builds and tests are CGO_ENABLED=0. The ONLY cgo step is the
#     race detector, which runs with CGO_ENABLED=1 (the race runtime is C).
#     That exception is why this script sets the flag per command instead of
#     exporting CGO_ENABLED=1 globally.
#   * No display, no network, no real credentials: live-model tests are
#     skipped by name (`-skip Live`), and they self-skip when their env vars
#     are absent.
#
# Optional environment:
#   SKIP_RACE=1   skip the -race step (used by containers without gcc).
#                 The release gate MUST NOT set this.
set -u
cd "$(dirname "$0")/.."

PASS=0
FAIL=0
FAILED_NAMES=""

step() { # name, command...
	name=$1
	shift
	printf '\n\033[1m== %s\033[0m\n' "$name"
	if "$@"; then
		PASS=$((PASS + 1))
		printf '  \033[32mPASS\033[0m %s\n' "$name"
	else
		FAIL=$((FAIL + 1))
		FAILED_NAMES="$FAILED_NAMES $name"
		printf '  \033[31mFAIL\033[0m %s\n' "$name"
	fi
}

# 1. Zero-cgo product build.
step "CGO_ENABLED=0 go build ./..." env CGO_ENABLED=0 go build ./...

# 2. Zero-cgo tests, headless, live-model tests skipped by name.
step "CGO_ENABLED=0 go test ./... -skip Live" \
	env CGO_ENABLED=0 go test ./... -count=1 -skip 'Live'

# 3. Vet.
step "CGO_ENABLED=0 go vet ./..." env CGO_ENABLED=0 go vet ./...

# 4. The hard rule: nothing in the dependency graph may need cgo. This also
#    re-checks the CGO=1 third-party audit from the script itself.
step "scripts/check-nocgo.sh" sh scripts/check-nocgo.sh

# 5. The race detector. This is the one step that requires cgo; it is a test
#    instrument, not a product build. gcc must be installed (CI installs it).
if [ "${SKIP_RACE:-0}" = "1" ]; then
	printf '\n\033[33m== race detector: SKIPPED by SKIP_RACE=1 (release gate must not do this)\033[0m\n'
else
	step "CGO_ENABLED=1 go test -race ./... -skip Live" \
		env CGO_ENABLED=1 go test -race ./... -count=1 -skip 'Live'
fi

printf '\n\033[1mverify-fixes result\033[0m: %d passed, %d failed' "$PASS" "$FAIL"
if [ -n "$FAILED_NAMES" ]; then
	printf ' — failed:%s\n' "$FAILED_NAMES"
	exit 1
fi
printf '\n'
