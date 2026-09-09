// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp_test

// Sprint: #118; Story: #3; Story-ID: fa07603b71dc
//
// bashPPInvoke binds a variadic parameter as an indexed shell variable, with
// no collection metadata attached, so a two-variable `for i, v := range`
// over it fell back to the scalar range path and iterated len(args) times
// with v left unset instead of walking the arguments themselves. Both cases
// below run the unchanged original source and compare stdout, stderr and
// exit status against a real Go build of the same file, so what is pinned is
// that a two-variable range over a variadic parameter behaves exactly as Go
// requires, not an interpreter-only expectation.
import "testing"

func TestGoSourceVariadicTwoVariableRange(t *testing.T) {
	cases := map[string]string{
		"multiple_arguments": `package main

import "fmt"

func sum(nums ...int) int {
	total := 0
	for i, v := range nums {
		fmt.Println(i, v)
		total += v
	}
	return total
}

func main() {
	fmt.Println(sum(10, 20, 30))
}
`,
		"zero_arguments": `package main

import "fmt"

func sum(nums ...int) int {
	total := 0
	for i, v := range nums {
		fmt.Println(i, v)
		total += v
	}
	return total
}

func main() {
	fmt.Println(sum())
}
`,
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			differGoSource(t, source, nil, "")
		})
	}
}
