# Sprint 275 GNU Bash confirm decisions

## Case 1971: empty assignment in xtrace

The reported Linux GNU Bash 5.3.0 output has a blank line where `+ null=`
would normally appear. On this macOS host, `/opt/homebrew/bin/bash` identifies
itself as GNU Bash 5.3.15 and prints `+ null=` for the exact script:

```sh
set -x; var=42; (( $var )); null=; (( $null )); set +x
```

The interpreter also prints `+ null=`. No interpreter change or expectation
change is justified by the available GNU Bash comparator here. Recheck against
GNU Bash 5.3.0 on Linux before changing the xtrace implementation.
