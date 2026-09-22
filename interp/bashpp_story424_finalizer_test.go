//go:build full

package interp_test

// Sprint: #243; Story: #424; Story-ID: ffabc6c1c44a

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestS243RetainedFinalizerOrderedMapRefusal pins the exact unchanged upstream
// acceptance root while retained-finalizer lifetime is unsupported. In
// particular, the root must stop at SetFinalizer registration rather than hang
// in its channel protocol or silently turn the finalizer into a no-op.
func TestS243RetainedFinalizerOrderedMapRefusal(t *testing.T) {
	path := filepath.Join(runtime.GOROOT(), "test", "typeparam", "orderedmap.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := runGoSourceRunnerError(t, string(source))
	if !strings.Contains(got, "original callback signature requires value-semantics parameters") {
		t.Fatalf("unchanged orderedmap.go did not preserve the retained-finalizer refusal: %q", got)
	}
}

// TestS243RetainedFinalizerLivenessRefusal locks both sides of the lifetime
// contract. The first original keeps p reachable across a collection, so a
// child mirror must not finalize it early. The second drops p and requires an
// actual callback, so pinning the mirror forever or treating SetFinalizer as a
// no-op is equally invalid. Until the bridge has an explicit retention/release
// protocol tied to interpreter reachability, both programs must fail closed at
// registration.
func TestS243RetainedFinalizerLivenessRefusal(t *testing.T) {
	for name, source := range map[string]string{
		"no_early_finalization": `package main
import "runtime"
func main() {
	done := make(chan bool, 1)
	p := new(int)
	runtime.SetFinalizer(p, func(*int) { done <- true })
	runtime.GC()
	select { case <-done: panic("finalized reachable object"); default: }
	runtime.KeepAlive(p)
}`,
		"eventual_release": `package main
import "runtime"
func main() {
	done := make(chan bool, 1)
	func() {
		p := new(int)
		runtime.SetFinalizer(p, func(*int) { done <- true })
	}()
	for i := 0; i < 20; i++ {
		runtime.GC()
		select { case <-done: return; default: }
	}
	panic("finalizer did not run")
}`,
	} {
		t.Run(name, func(t *testing.T) {
			got := runGoSourceRunnerError(t, source)
			if !strings.Contains(got, "original callback signature requires value-semantics parameters") {
				t.Fatalf("retained finalizer did not fail closed: %q", got)
			}
		})
	}
}
