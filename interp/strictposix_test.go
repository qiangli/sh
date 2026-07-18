// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

package interp

import "testing"

// The strictPosix flag is the foundation for the strict-sh conformance
// behaviors (yash -p): it must survive both Reset and Subshell, since the
// yash cases exercise strict semantics inside subshells and across runs.
func TestStrictPosixPropagation(t *testing.T) {
	r, err := New(WithStrictPosix(true))
	if err != nil {
		t.Fatal(err)
	}
	if !r.strictPosix {
		t.Fatal("WithStrictPosix(true) did not set strictPosix")
	}
	r.Reset()
	if !r.strictPosix {
		t.Fatal("Reset dropped strictPosix")
	}
	sub := r.Subshell()
	if !sub.strictPosix {
		t.Fatal("Subshell dropped strictPosix")
	}
}
