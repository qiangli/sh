#!/usr/bin/env bash
# The quick tier — the push gate: build, then unit and mock tests only.
#
# Two mechanisms, one meaning:
#   * `go test -short` skips what calls an external shell or sleeps;
#   * the Bash++ evaluator test FILES that build or run Go programs with the
#     reviewed toolchain (and the compiled-mode `lower` tests, which always do)
#     carry `//go:build full` and are not even compiled here.
# `make test-full` (= `-tags full`, run on tags and by hand) is the complete
# suite. Membership of the full tier was measured on 2026-09-17: every test file
# whose slowest test took more than 1s in `go test -json ./gosource ./interp
# ./lower`, minus the shell lifecycle/signal files — see docs in CLAUDE.md.
# CI also runs `go vet -tags full ./...` so the full tier cannot rot uncompiled.
set -euo pipefail
cd "$(dirname "$0")/.."
timeout=${TEST_TIMEOUT:-10m}

go build ./...

# Darwin signal dispositions are process-global. Isolate the interp suite into
# bounded processes so signal conformance cases cannot leave runtime receiver
# state that affects hundreds of later tests. Every test still runs; the
# regexes partition Test* names and examples run last.
if [ "$(uname -s)" = Darwin ]; then
	go list ./... | grep -v '/interp$' | xargs go test -short -timeout="$timeout"
	for group in 'A-E' 'F-J' 'K-O' 'P-T' 'U-Z'; do
		go test -short -timeout="$timeout" -run "^Test[$group]" ./interp
	done
	go test -short -timeout="$timeout" -run '^Example' ./interp
else
	go test -short -timeout="$timeout" ./...
fi
