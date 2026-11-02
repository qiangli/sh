#!/usr/bin/env bash
# Provision the pinned compiler used by authored differential fixtures before
# their runtime deadlines and exact stdout/stderr comparisons begin.
set -euo pipefail
export GOTOOLCHAIN=go1.27.1
export GOWORK=off
version=$(go env GOVERSION)
if [ "$version" != go1.27.1 ]; then
	echo "ERROR: test oracle resolved $version; expected go1.27.1" >&2
	exit 1
fi
compiler=$(go tool compile -V)
if [ "$compiler" != 'compile version go1.27.1' ]; then
	echo "ERROR: test oracle compiler identity mismatch: $compiler" >&2
	exit 1
fi
printf 'oracle_version\t%s\noracle_compiler\t%s\noracle_root\t%s\n' \
	"$version" "$compiler" "$(go env GOROOT)"
