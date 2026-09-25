// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp_test

import (
	"testing"
)

// TestBashPPInlineReceiveCompareEvaluatesOnce guards the Sprint 270 / Story 761
// fix: an inline `<-ch` compared with a scalar variable must receive exactly
// once. Before the fix the scalar operand yielded the comparable path's
// scalar-only sentinel after the receive had already run, making the caller
// retry the whole comparison and receive from the channel a second time — the
// value under test was compared against the wrong element and a following
// receive read past the drained buffer. Each program's output is fixed by the
// language, so the assertions are self-contained and need no Go toolchain.
func TestBashPPInlineReceiveCompareEvaluatesOnce(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			// The reported root: receive on the left, scalar on the right.
			// Only one value is consumed by the comparison, so the buffered 2
			// is still available for the second receive.
			name: "recv-left-scalar-right",
			src: `package main

import "fmt"

func main() {
	ch := make(chan int, 2)
	ch <- 1
	ch <- 2
	v := 1
	fmt.Println(<-ch == v)
	fmt.Println(<-ch)
}
`,
			want: "true\n2\n",
		},
		{
			// Parenthesised receive must be recognised through the paren.
			name: "recv-left-paren-scalar-right",
			src: `package main

import "fmt"

func main() {
	ch := make(chan int, 2)
	ch <- 7
	ch <- 8
	v := 7
	fmt.Println((<-ch) == v)
	fmt.Println(<-ch)
}
`,
			want: "true\n8\n",
		},
		{
			// Scalar on the left already fell back before the receive fired,
			// so this direction was correct before the fix; keep it covered so
			// the reorder does not regress it.
			name: "scalar-left-recv-right",
			src: `package main

import "fmt"

func main() {
	ch := make(chan int, 2)
	ch <- 1
	ch <- 2
	v := 1
	fmt.Println(v == <-ch)
	fmt.Println(<-ch)
}
`,
			want: "true\n2\n",
		},
		{
			// Two receives genuinely consume two values, in source order.
			name: "recv-both-sides",
			src: `package main

import "fmt"

func main() {
	ch := make(chan int, 2)
	ch <- 5
	ch <- 6
	fmt.Println(<-ch == <-ch)
}
`,
			want: "false\n",
		},
		{
			// A mismatching compare still leaves the sibling value intact.
			name: "recv-left-scalar-right-mismatch",
			src: `package main

import "fmt"

func main() {
	ch := make(chan int, 2)
	ch <- 3
	ch <- 4
	v := 9
	fmt.Println(<-ch == v)
	fmt.Println(<-ch)
}
`,
			want: "false\n4\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			out, errOut, err := bashPPRunGoSource(t, dir, dir+"/original.go", tc.src)
			if err != nil {
				t.Fatalf("Runner: %v; stdout=%q stderr=%q", err, out, errOut)
			}
			if out != tc.want {
				t.Fatalf("stdout=%q, want %q (stderr=%q)", out, tc.want, errOut)
			}
		})
	}
}
