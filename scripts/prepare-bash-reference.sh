#!/usr/bin/env bash
# Select or provision the GNU Bash 5.3 oracle; never substitute Bashy or an
# older system Bash for a Bash 5.3 semantic comparison. Build only, no suites.
set -euo pipefail
is_reference() {
	local version
	version=$("$1" --version 2>/dev/null) || return 1
	[[ "$version" != *bashy* ]] && [[ "$version" =~ ^GNU\ bash,\ version\ 5\.3\. ]]
}
if [ -n "${SH_TEST_BASH_REFERENCE:-}" ]; then
	if ! is_reference "$SH_TEST_BASH_REFERENCE"; then
		echo "ERROR: SH_TEST_BASH_REFERENCE must identify GNU Bash 5.3" >&2
		exit 1
	fi
	resolved=$(command -v "$SH_TEST_BASH_REFERENCE")
	printf '%s/%s\n' "$(cd "$(dirname "$resolved")" && pwd)" "$(basename "$resolved")"
	exit 0
fi
for candidate in /opt/homebrew/bin/bash /usr/local/bin/bash "$(command -v bash)"; do
	if is_reference "$candidate"; then printf '%s\n' "$candidate"; exit 0; fi
done
cache=${XDG_CACHE_HOME:-"$HOME/.cache"}/sh-test-oracles
binary=$cache/bash-5.3-$(uname -s)-$(uname -m)
if [ -x "$binary" ] && is_reference "$binary"; then printf '%s\n' "$binary"; exit 0; fi
stage=$(mktemp -d "${TMPDIR:-/tmp}/sh-bash-reference.XXXXXX")
trap 'rm -rf "$stage"' EXIT
expected=0d5cd86965f869a26cf64f4b71be7b96f90a3ba8b3d74e27e8e9d9d5550f31ba
verified=0
for url in https://mirrors.kernel.org/gnu/bash/bash-5.3.tar.gz https://ftpmirror.gnu.org/bash/bash-5.3.tar.gz https://ftp.gnu.org/gnu/bash/bash-5.3.tar.gz; do
	if curl --fail --location --retry 2 --connect-timeout 15 --max-time 120 "$url" -o "$stage/bash.tar.gz"; then
		actual=$(shasum -a 256 "$stage/bash.tar.gz")
		if [ "${actual%% *}" = "$expected" ]; then verified=1; break; fi
		echo "ERROR: GNU Bash archive checksum mismatch from $url" >&2
	fi
done
if [ "$verified" != 1 ]; then echo 'ERROR: no verified GNU Bash 5.3 archive' >&2; exit 1; fi
tar -xzf "$stage/bash.tar.gz" -C "$stage"
(cd "$stage/bash-5.3" && ./configure --without-bash-malloc && make -j2) >&2
if ! is_reference "$stage/bash-5.3/bash"; then echo 'ERROR: built oracle is not GNU Bash 5.3' >&2; exit 1; fi
mkdir -p "$cache"
# Publish a complete executable atomically; concurrent preparations never see
# a partial binary. The source is checksum-verified before any build command.
published=$(mktemp "$cache/.bash-5.3.XXXXXX")
cp "$stage/bash-5.3/bash" "$published"
chmod 755 "$published"
mv -f "$published" "$binary"
printf '%s\n' "$binary"
