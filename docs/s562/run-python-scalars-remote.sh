#!/usr/bin/env bash
# Manager-only execution after review of the staged immutable bundle.
set -euo pipefail
if [[ $# != 4 ]]; then
  echo 'usage: run-python-scalars-remote.sh STAGE_DIR GO_BIN PYTHON_BIN CORPUS_ROOT' >&2
  exit 2
fi
stage=$(cd "$1" && pwd)
go=$2
python=$3
corpus=$4
for path in "$go" "$python" "$corpus"; do
  [[ "$path" = /* ]] || { echo 'tool and corpus paths must be absolute' >&2; exit 2; }
done
cd "$stage"
sha256sum -c SHA256SUMS
mkdir source bin cache tmp
git clone --quiet worker.bundle source
test "$(git -C source rev-parse HEAD)" = "$(cat COMMIT)"
test -x "$go"
test -x "$python"
ln -s "$go" bin/go
ln -s "$python" bin/python3
export PATH="$stage/bin:/usr/bin:/bin"
export BASHPP_PYTHON="$python"
export TMPDIR="$stage/tmp" GOCACHE="$stage/cache/go" GOPATH="$stage/cache/gopath"
export GOWORK=off
bash source/docs/s562/verify-python-scalars.sh "$stage/results" "$corpus"
