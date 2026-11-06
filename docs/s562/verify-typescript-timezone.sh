#!/bin/sh
# Run from the exact sh snapshot, on the manager's claimed test host.
# GO names the authenticated SDK executable; evidence must be a NEW directory.
set -eu
phase=${1:?red or green required}
evidence=${2:?absolute evidence directory required}
case "$phase" in red|green) ;; *) exit 2 ;; esac
case "$evidence" in /*) ;; *) echo 'absolute evidence directory required' >&2; exit 2 ;; esac
: "${GO:?authenticated Go executable required}"
mkdir "$evidence"
rc=0
"$GO" test -count=1 ./polyglot -run '^TestTypeScriptExplicitTimezoneEnvironment$' \
    >"$evidence/stdout" 2>"$evidence/stderr" || rc=$?
printf '%s\n' "$rc" >"$evidence/exit-status"
cat "$evidence/stdout"
cat "$evidence/stderr" >&2
case "$phase" in
    red)
        test "$rc" -ne 0
        grep -q -- 'FAIL: TestTypeScriptExplicitTimezoneEnvironment' "$evidence/stdout"
        grep -q -- 'TZ = ("", false), want ("UTC", true)' "$evidence/stdout"
        grep -q -- 'share a fingerprint' "$evidence/stdout"
        ;;
    green)
        test "$rc" -eq 0
        grep -q '^ok[[:space:]]' "$evidence/stdout"
        ;;
esac
