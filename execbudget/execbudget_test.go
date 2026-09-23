// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

package execbudget

import (
	"strings"
	"testing"
)

func TestBashyArgMaxBoundary(t *testing.T) {
	// Include both argv and env NUL bytes, and count UTF-8 bytes rather
	// than runes. Equality with the reported limit is permitted.
	args := []string{"x"}
	env := []string{"BIG=" + strings.Repeat("é", (BashyArgMax-2-5)/2) + "x"}
	if OverBashyArgMax(args, env) {
		t.Fatal("equal-size launch rejected")
	}
	env[0] += "x"
	if !OverBashyArgMax(args, env) {
		t.Fatal("over-limit launch accepted")
	}
}
