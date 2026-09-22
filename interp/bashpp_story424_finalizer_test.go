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

// TestS243RetainedFinalizerRefusal pins the two exact unchanged SDK roots while
// retained-finalizer lifetime is unsupported. test/abi/map.go requires its *T
// map-key temporary to stay live across V's three collections; orderedmap.go
// registers a retained method callback. Both must stop at registration rather
// than hang, finalize early, or silently turn the finalizer into a no-op.
//
// This is only a fail-closed regression guard. It does not satisfy Story 424's
// semantic acceptance: doing so requires a collector-visible allocation whose
// retention and release follow interpreter reachability while preserving the
// original pointer identity delivered to the callback.
func TestS243RetainedFinalizerRefusal(t *testing.T) {
	for _, rel := range []string{
		filepath.Join("abi", "map.go"),
		filepath.Join("typeparam", "orderedmap.go"),
	} {
		t.Run(filepath.ToSlash(rel), func(t *testing.T) {
			path := filepath.Join(runtime.GOROOT(), "test", rel)
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got := runGoSourceRunnerError(t, string(source))
			if !strings.Contains(got, "original callback signature requires value-semantics parameters") {
				t.Fatalf("unchanged %s did not preserve the retained-finalizer refusal: %q", filepath.ToSlash(rel), got)
			}
		})
	}
}
