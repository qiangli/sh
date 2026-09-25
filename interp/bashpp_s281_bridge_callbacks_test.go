//go:build full

package interp_test

import "testing"

// Sprint 281 Story 809: one bridge mechanism for synchronous dependency
// calls that invoke original method callbacks, plus result-owned retained
// function callbacks such as sync.OnceValue.
func TestS281SynchronousOriginalMethodCallbacks(t *testing.T) {
	for name, source := range map[string]string{
		"fmt Fprint invokes original Write": `package main
import (
	"fmt"
	"strings"
)
type sink struct{ parts []string }
func (s *sink) Write(p []byte) (int, error) {
	s.parts = append(s.parts, string(p))
	return len(p), nil
}
func main() {
	var s sink
	fmt.Fprint(&s, "node", 7)
	fmt.Println(strings.Join(s.parts, "|"))
}`,
		"dependency handle method formats original value": `package main
import (
	"bytes"
	"fmt"
	"log"
)
type tag struct{ n int }
func (t tag) String() string { return fmt.Sprint("tag", t.n) }
func main() {
	var buf bytes.Buffer
logger := log.New(&buf, "", 0)
logger.Printf("saw %v", tag{3})
	fmt.Print(buf.String())
}`,
		"original writer receives a copied variadic slice": `package main
import (
	"bufio"
	"fmt"
	"os"
)
type writer struct{ w *bufio.Writer }
func (w *writer) Write(p []byte) (int, error) { return w.w.Write(p) }
type node struct{ name string }
var values []any
func (n node) String() string {
	values[1] = "changed"
	return "<" + n.name + ">"
}
func (w *writer) printf(format string, args ...any) {
	fmt.Fprintf(w, format, args...)
}
func main() {
	w := &writer{w: bufio.NewWriter(os.Stdout)}
	values = []any{node{name: "seven"}, "before"}
	w.printf("node=%v text=%s\\n", values...)
	w.w.Flush()
}`,
	} {
		t.Run(name, func(t *testing.T) {
			differGoSource(t, source, nil, "")
		})
	}
}

func TestS281ResultOwnedOnceValueCallback(t *testing.T) {
	differGoSource(t, `package main
import (
	"fmt"
	"sync"
)
var calls int
func main() {
	once := sync.OnceValue(func() string {
		calls++
		return fmt.Sprint("value", calls)
	})
	fmt.Println(once(), once(), calls)
}`, nil, "")
}

func TestS281GenericBridgeTypeRegistration(t *testing.T) {
	differGoSource(t, `package main
import (
	"fmt"
	"hash/maphash"
)

type Thing struct{ Name string }
type ThingHasher struct{}

func (ThingHasher) Hash(h *maphash.Hash, v Thing) { h.WriteString(v.Name) }
func (ThingHasher) Equal(x, y Thing) bool { return x.Name == y.Name }

func main() {
	ch := make(chan maphash.Hasher[Thing], 1)
	fmt.Println(cap(ch), ThingHasher{}.Equal(Thing{"same"}, Thing{"other"}))
}`, nil, "")
}
