//go:build full

package interp_test

// Sprint: #243; Story: #672; Story-ID: fa5b3cf5a929

import "testing"

func TestS243ScalarConstantThreeModes(t *testing.T) {
	cases := map[string]string{
		"exact_constant_comparisons": `package main

import "fmt"

const huge = 1 << 100
const hugeMinusOne = huge - 1
const third = 1.0 / 3.0

func main() {
	var rounded float64 = huge
	fmt.Println(huge > hugeMinusOne, hugeMinusOne+1 == huge)
	fmt.Println(rounded == hugeMinusOne, third*3 == 1)
}
`,
		"defined_array_comparisons": `package main

import "fmt"

type bytes1 [1]uint8

func main() {
	v := bytes1{1}
	fmt.Println(v == [1]uint8{1}, v != [1]uint8{2})
	var a [4]int
	b := [4]int{}
	fmt.Println(a == b, a != b)
}
`,
		"alias_struct_interface_comparisons": `package main

import "fmt"

type stringsAlias = struct{ F string }
type intsAlias = struct{ F int }

func zero() intsAlias { return intsAlias{} }

func main() {
	var want struct{ F int }
	var s stringsAlias
	var i intsAlias
	fmt.Println(any(want) == any(i), any(want) == any(zero()))
	fmt.Println(any(s) == any(struct{ F string }{}), any(s) == any(i))
}
`,
		"local_generic_type_identity": `package main

import "fmt"

func one() any { type T[_ any] int; return T[int](0) }
func two() any { type T[_ any] int; return T[int](0) }

func main() { fmt.Println(one() == two(), one() == one()) }
`,
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) { typedSendThreeModes(t, source) })
	}
}

func TestS243ScalarConstantNegativeControls(t *testing.T) {
	typedSendThreeModes(t, `package main

import "fmt"

type named int

func main() {
	var s string = "7"
	var n named = 7
	fmt.Println(s == "7", n == 7, n != 8)
}
`)
}
