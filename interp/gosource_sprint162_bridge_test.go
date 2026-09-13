package interp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustReadSprint162Bridge(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "sprint162", "bridge", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestGoSourceSprint162GenericBridgeTypes(t *testing.T) {
	differGoSource(t, mustReadSprint162Bridge(t, "generic_types.go.txt"), nil, "")
}

func TestGoSourceSprint162GenericBridgeJSON(t *testing.T) {
	differGoSource(t, mustReadSprint162Bridge(t, "generic_json.go.txt"), nil, "")
}

func TestGoSourceSprint162GenericBridgePositiveControl(t *testing.T) {
	differGoSource(t, mustReadSprint162Bridge(t, "generic_types_ref.go.txt"), nil, "")
}

func TestGoSourceSprint162GenericBridgeNegativeReferenceReceiver(t *testing.T) {
	got := runGoSourceRunnerError(t, mustReadSprint162Bridge(t, "generic_types_negative.go.txt"))
	if !strings.Contains(got, "wrote through reference storage") {
		t.Fatalf("wrong refusal for reference-bearing generic receiver: %q", got)
	}
}
