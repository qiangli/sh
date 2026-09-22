//go:build full

package interp_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Sprint: #243; Story: #672; Story-ID: fa5b3cf5a929

// Two local generic declarations with the same source name are distinct
// dynamic types. Their public reflection spelling remains the declared name.
func TestS243LocalGenericInterfaceIdentity(t *testing.T) {
	differGoSource(t, `package main
import (
 "fmt"
 "reflect"
)
func one() any { type T[_ any] int; return T[int](0) }
func two() any { type T[_ any] int; return T[int](0) }
func main() {
 p, q := one(), two()
 fmt.Println(p == q)
 fmt.Println(reflect.TypeOf(p).String(), reflect.TypeOf(q).String())
}
`, nil, "")
}

// A package declaration and a same-spelled local declaration remain distinct;
// both retain the public Go spelling rather than a registry-private name.
func TestS243PackageAndLocalGenericInterfaceIdentity(t *testing.T) {
	differGoSource(t, `package main
import (
 "fmt"
 "reflect"
)
type T[_ any] int
func packageValue() any { return T[int](0) }
func localValue() any { type T[_ any] int; return T[int](0) }
func main() {
 p, q := packageValue(), localValue()
 fmt.Println(p == q)
 fmt.Println(reflect.TypeOf(p).String(), reflect.TypeOf(q).String())
}
`, nil, "")
}

func TestS243OriginalIssue54456GenericIdentity(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "test", "typeparam", "issue54456.go"))
	if err != nil {
		t.Fatal(err)
	}
	typedSendThreeModes(t, string(source))
}
