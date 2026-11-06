#!/usr/bin/env bash
# Execute only by the manager after reviewing the staged bundle and checksums.
set -euo pipefail
if [[ $# != 4 ]]; then
  echo 'usage: run-c-atexit-remote.sh STAGE_DIR ZIG_BIN GO_BIN CORPUS_ROOT' >&2
  exit 2
fi
stage=$(cd "$1" && pwd)
zig=$2
go=$3
corpus=$4
# Tool and corpus paths must remain valid after changing into the stage.
for path in "$zig" "$go" "$corpus"; do
  [[ "$path" = /* ]] || { echo 'tool and corpus paths must be absolute' >&2; exit 2; }
done
cd "$stage"
sha256sum -c SHA256SUMS
mkdir source bin cache tmp
# The bundle includes the baseline commit needed for the red overlay.
git clone --quiet worker.bundle source
test -x "$zig"
test -x "$go"
for pair in 'clang cc' 'clang++ c++'; do
  read -r name mode <<< "$pair"
  printf '#!/bin/sh\nexec "%s" %s "$@"\n' "$zig" "$mode" > "bin/$name"
  chmod 700 "bin/$name"
done
ln -s "$go" bin/go
export PATH="$stage/bin:/usr/bin:/bin"
export TMPDIR="$stage/tmp"
export ZIG_GLOBAL_CACHE_DIR="$stage/cache/zig" GOCACHE="$stage/cache/go" GOPATH="$stage/cache/gopath"
export GOWORK=off
bash source/docs/s562/verify-c-atexit.sh "$stage/results" \
 "$corpus"
