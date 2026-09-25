//go:build full

package interp_test

import "testing"

// Sprint: #275; Story: #758; Story-ID: 2577207b59b5

// Reused local generic names are distinct per declaration, and each
// instance keeps gc's public spelling: package-qualified, with type
// arguments separated by a bare comma.
func TestS275ScopedLocalGenericInstances(t *testing.T) {
	differGoSource(t, `package main
import (
 "fmt"
 "reflect"
)
func one() any { type T[_ any] int; return T[*[]map[string][2]float64](0) }
func two() any { type T[_ any] int; return T[*[]map[string][2]float64](0) }
func three() any { type T[_, _ any] int; return T[int, uint8](3) }
func main() {
 a, b, c := one(), two(), three()
 fmt.Println(reflect.TypeOf(a).String(), reflect.TypeOf(b).String(), reflect.TypeOf(c).String())
 fmt.Println(a == b, a == a)
}
`, nil, "")
}
