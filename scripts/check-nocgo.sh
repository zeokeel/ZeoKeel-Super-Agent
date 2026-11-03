#!/bin/sh
# Zero-cgo guard: fails if anything in the dependency graph needs cgo.
# Usable as a pre-commit / CI step:  ./scripts/check-nocgo.sh [pkg-pattern]
set -eu

# The hard constraint: cgo must never be on, in any environment.
export CGO_ENABLED=0
if [ "$(go env CGO_ENABLED)" != "0" ]; then
	echo "check-nocgo: CGO_ENABLED is not 0" >&2
	exit 1
fi

pkgs="${1:-./...}"

echo "== 1. CGO_ENABLED=0 go build $pkgs"
go build $pkgs

echo "== 2. no cgo files may be compiled into the build graph"
# CgoFiles is empty for every package under CGO_ENABLED=0 exactly when nothing
# in the graph needs cgo; a dependency without a pure-Go fallback fails step 1.
cgo=$(CGO_ENABLED=0 go list -deps -f '{{if .CgoFiles}}{{.ImportPath}}: {{.CgoFiles}}{{end}}' $pkgs | grep -v '^$' || true)
if [ -n "$cgo" ]; then
	echo "check-nocgo: FAIL: cgo files in the build graph:" >&2
	echo "$cgo" >&2
	exit 1
fi

echo "== 3. no third-party package has cgo files (audit with CGO_ENABLED=1)"
# Under CGO_ENABLED=1 the standard library would compile optional cgo paths
# (net, os/user, runtime/cgo), and purego has an optional cgo fallback. These
# are allowed: the shipped build is CGO_ENABLED=0. Anything else is a real cgo
# dependency and fails the guard.
third=$(CGO_ENABLED=1 go list -deps -f '{{if .CgoFiles}}{{.ImportPath}}{{with .Module}}|{{.Path}}{{end}}{{end}}' $pkgs \
	| grep '|' \
	| grep -v '|github.com/ebitengine/purego$' \
	|| true)
if [ -n "$third" ]; then
	echo "check-nocgo: FAIL: third-party packages with cgo files:" >&2
	echo "$third" >&2
	exit 1
fi

echo "== 4. go vet"
go vet $pkgs

echo "check-nocgo: PASS (zero cgo)"
