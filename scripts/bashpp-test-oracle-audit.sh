#!/usr/bin/env bash
set -euo pipefail

files=()
while IFS= read -r file; do
	files+=("$file")
done < <(git ls-files '*_test.go' | sort)

if ((${#files[@]} == 0)); then
	echo "bashpp oracle audit: no tracked test files found" >&2
	exit 1
fi

fail=0
for file in "${files[@]}"; do
	# Audit every named concurrency/lifecycle family, plus files that opt in
	# explicitly. Do not infer concurrency from generic words in comments:
	# ordinary parallel tests often use a private buffer safely.
	case "$file" in
		*/concurr*_test.go|*/carrier*_test.go|*/lifecycle*_test.go|*/racedetector*_test.go|*/race_*_test.go|*/tp714*_test.go)
			;;
		*)
			if ! grep -q 'bashpp-racegate:audit' "$file"; then
				continue
			fi
			;;
	esac
	if ! grep -qE 'bytes\.Buffer|JobCarrier|go func|t\.Parallel' "$file"; then
		continue
	fi
	if grep -nE 'var .*bytes\.Buffer|new\(bytes\.Buffer\)|&bytes\.Buffer\{\}' "$file" |
		grep -v 'bashpp-racegate:allow-plain-buffer' >&2; then
		echo "bashpp oracle audit: $file uses a plain bytes.Buffer in a concurrency/race test" >&2
		echo "use lockedBuffer/concBuffer, a channel observer, atomics, or an immutable snapshot" >&2
		fail=1
	fi
done

exit "$fail"
