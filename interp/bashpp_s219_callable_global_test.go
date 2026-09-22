//go:build full

// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp_test

import (
	"testing"

	"github.com/go-quicktest/qt"
)

// Sprint: #219; Story: #462; Story-ID: e348d2c13248
func TestS219CallableGlobal(t *testing.T) {
	const src = `package main
import "fmt"

var trace string

func callable(tag string, start int) func() int {
	trace += tag
	n := start
	return func() int { n++; return n }
}

func nilCallable() func() { trace += "N"; return nil }
func ordinary() int { trace += "V"; return 7 }

var g = callable("G", 10)
var nilg = nilCallable()
var value = ordinary()
var h = g

func main() {
	fmt.Println(trace, value, nilg == nil)
	fmt.Println(g(), h(), g())
}
`
	out, stderr, err := runGoSource(t, "s219callableglobal", src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(out, "GNV 7 true\n11 12 13\n"))
}
