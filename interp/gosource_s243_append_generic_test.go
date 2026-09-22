//go:build full

package interp_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Sprint: #243; Story: #671; Story-ID: 56d156f9118e

func TestS243OriginalTypeparamAppendRoot(t *testing.T) {
	for _, tc := range []struct {
		name, path, digest string
	}{
		{"typeparam_append", filepath.Join("typeparam", "append.go"), "0dd711bc7400560e3a3a7f5e0421ba4a9ae753e6661d20713df80c205c559516"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "test", tc.path))
			if err != nil {
				t.Fatal(err)
			}
			if tc.digest != "" && fmt.Sprintf("%x", sha256.Sum256(source)) != tc.digest {
				t.Fatalf("original %s bytes changed", tc.path)
			}
			differGoSource(t, string(source), nil, "")
		})
	}
}

func TestS243AppendGenericBindingPerInstantiation(t *testing.T) {
	typedSendThreeModes(t, `package main
import "fmt"
type I int
type S string
type Recv <-chan int
type Slice[E any] []E
func add[T any](dst Slice[T], value func() T) Slice[T] { return append(dst, value()) }
var calls int
func once[T any](v T) func() T { return func() T { calls++; return v } }
func main() {
 is := add(Slice[I]{1}, once(I(2)))
 ss := add(Slice[S]{"a"}, once(S("b")))
 ch := make(Recv)
 cs := add(Slice[Recv]{ch}, once(ch))
 fmt.Println(is, ss, len(cs), cs[0] == ch, cs[1] == ch, calls)
}`)
}
