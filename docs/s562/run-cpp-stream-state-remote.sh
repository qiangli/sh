#!/usr/bin/env bash
# Manager executes only after reviewing the immutable staged source bundle.
set -euo pipefail
if [[ $# != 3 ]]; then
  echo 'usage: run-cpp-stream-state-remote.sh STAGE_DIR ZIG_BIN GO_BIN' >&2
  exit 2
fi
stage=$(cd "$1" && pwd)
zig=$2
go=$3
for path in "$zig" "$go"; do
  [[ "$path" = /* && -x "$path" ]] || { echo 'absolute executable paths required' >&2; exit 2; }
done
cd "$stage"
sha256sum -c SHA256SUMS
# Match the retained native triage SDK, not merely a version string.
echo "2317bbb91798556d9d0f38aabdac23db83f0979b25f767259ae474546724087c  $zig" | sha256sum -c -
mkdir source bin cache tmp
git clone --quiet worker.bundle source
test "$(git -C source rev-parse HEAD)" = "$(cat COMMIT)"
for pair in 'clang cc' 'clang++ c++'; do
  read -r name mode <<< "$pair"
  printf '#!/bin/sh\nexec "%s" %s "$@"\n' "$zig" "$mode" > "bin/$name"
  chmod 700 "bin/$name"
done
ln -s "$go" bin/go
export PATH="$stage/bin:/usr/bin:/bin" TMPDIR="$stage/tmp"
export ZIG_GLOBAL_CACHE_DIR="$stage/cache/zig" ZIG_LOCAL_CACHE_DIR="$stage/cache/zig-local"
export GOCACHE="$stage/cache/go" GOPATH="$stage/cache/gopath"
# Total wall bound includes Go dependency/build preparation; no retry.
timeout --kill-after=10s 600s bash -e -c '
  python3 source/docs/s562/prepare-cpp-stream-provider.py "$1/provider"
  bash source/docs/s562/verify-cpp-stream-state.sh "$1/results"
' bash "$stage"
