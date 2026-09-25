//go:build full

package interp_test

import (
	"strings"
	"testing"
)

// The formatter may walk a copied slice only when an original value method
// has no side-effecting work hidden in its returned formatting expression.
func TestS275FmtPureValueProofRefusesEffects(t *testing.T) {
	for name, src := range map[string]string{
		"named field method": `package main
import "fmt"
type Tag int
func (Tag) String() string { println("effect"); return "tag" }
type item struct{ n Tag }
func (i item) String() string { return fmt.Sprint("item", i.n) }
func main() { fmt.Println([]item{{1}}); println("after") }`,
		"nested call": `package main
import "fmt"
type item struct{ n int }
func effect() int { println("effect"); return 1 }
func (i item) String() string { return fmt.Sprint("item", effect()) }
func main() { fmt.Println([]item{{1}}); println("after") }`,
	} {
		t.Run(name, func(t *testing.T) {
			out, stderr, err := runGoSource(t, "s275-fmt-value-proof", src)
			if err == nil || !strings.Contains(err.Error()+stderr, "original callback with copied slice references is unsupported") || strings.Contains(out+stderr, "effect") || strings.Contains(out+stderr, "after") {
				t.Fatalf("impure method crossed fmt copy: out=%q stderr=%q err=%v", out, stderr, err)
			}
		})
	}
}
