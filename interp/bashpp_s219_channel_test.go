//go:build full

package interp_test

import (
	"mvdan.cc/sh/v3/gosource"
	"strings"
	"testing"
)

// TestS219ChannelIdentity keeps channel values on the typed value path across
// the argument, result, assignment, and nil-operation boundaries exercised by
// the Sprint 219 corpus roots. These are deliberately smaller programs than
// the corpus fixtures, but retain the exact failing expression shapes.
func TestS219ChannelIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, source, want string
	}{
		{
			name: "nil_second_channel_argument",
			source: `package main
func send(a, b chan uint) { a <- 1; if b != nil { b <- 2 } }
func main() { a := make(chan uint, 1); send(a, nil); println(<-a) }
`,
			want: "1\n",
		},
		{
			name: "returned_channel",
			source: `package main
func odds() chan int { out := make(chan int); go func() { out <- 5 }(); return out }
func main() { a := odds(); println(<-a) }
`,
			want: "5\n",
		},
		{
			name: "package_defined_element_assignment",
			source: `package main
type block [7]byte
var c chan block
var capacity = 1
func main() { c = make(chan block, capacity); println(cap(c)) }
`,
			want: "1\n",
		},
		{
			name: "defined_large_element_failed_make",
			source: `package main
type block [1<<16-1]byte
var c chan block
var capacity = -1
func shouldPanic(f func()) { defer func() { if recover() == nil { panic("did not panic") } }(); f() }
func main() { shouldPanic(func() { c = make(chan block, capacity) }); println("ok") }
`,
			want: "ok\n",
		},
		{
			name: "defined_channel_conversion_in_func_literal",
			source: `package main
type XChan chan int
var makers = []func(chan int) XChan { func(c chan int) XChan { return XChan(c) } }
func main() { c := make(chan int, 1); c <- 9; println(<-makers[0](c)) }
`,
			want: "9\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, got, err := bashPPRunGoSource(t, t.TempDir(), "outside-corpus.go", tc.source)
			if err != nil || stdout != "" || got != tc.want {
				t.Fatalf("run=%v stdout=%q stderr=%q; want stderr=%q", err, stdout, got, tc.want)
			}
		})
	}
}

func TestS219ChannelDirectionGuards(t *testing.T) {
	for _, tc := range []struct {
		name, source string
	}{
		{
			name: "argument_widening",
			source: `package main
func wide(chan int) {}
func pass(c <-chan int) { wide(c) }
func main() { pass(make(chan int)) }
`,
		},
		{
			name: "result_widening",
			source: `package main
func wide(c <-chan int) chan int { return c }
func main() { _ = wide(make(chan int)) }
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program, err := gosource.Parse(strings.NewReader(tc.source), "outside-corpus-negative.go", gosource.Options{RunMain: true})
			if err == nil || program != nil {
				t.Fatal("invalid directional channel conversion succeeded")
			}
		})
	}
}
