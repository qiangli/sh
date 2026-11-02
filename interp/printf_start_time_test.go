package interp

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

func TestPrintfShellStartTimeStable(t *testing.T) {
	var out bytes.Buffer
	r, err := New(StdIO(nil, &out, &out))
	if err != nil {
		t.Fatal(err)
	}
	// A fixed past startup separates -2 from the current clock without sleeps.
	r.startTime = time.Unix(1700000000, 999999999)
	src := `printf '%(%s)T\n' -2; (printf '%(%s)T\n' -2); printf -v stamp '%(%s)T' -2; printf '%s\n' "$stamp"`
	f, err := syntax.NewParser().Parse(strings.NewReader(src), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if want := "1700000000\n1700000000\n1700000000\n"; out.String() != want {
		t.Fatalf("startup output %q, want %q", out.String(), want)
	}
}
