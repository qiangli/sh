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
