// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

// Package execbudget defines Bashy's argument and environment budget.
package execbudget

// BashyArgMax is Bashy's combined argument and environment byte limit across
// platforms. Stricter native host limits, such as the Windows command-line
// limit or Linux's per-string limit, still apply to external programs.
const BashyArgMax = 1 << 20

// OverBashyArgMax reports whether the explicit argv and environment exceed
// BashyArgMax. Each UTF-8 string contributes its bytes and a terminating NUL,
// as in the POSIX argument/environment accounting model. The caller must pass
// the final environment that it will give to the child.
func OverBashyArgMax(args, env []string) bool {
	remaining := BashyArgMax
	for _, value := range args {
		if len(value) >= remaining {
			return true
		}
		remaining -= len(value) + 1
	}
	for _, value := range env {
		if len(value) >= remaining {
			return true
		}
		remaining -= len(value) + 1
	}
	return false
}
