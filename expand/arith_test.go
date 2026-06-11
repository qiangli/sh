// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"errors"
	"strings"
	"testing"
)

func TestExpandArithmText(t *testing.T) {
	env := ListEnviron("key=abc")
	cfg := &Config{Env: env}
	got, err := cfg.expandArithmText("assoc[$key]++")
	if err != nil {
		t.Fatal(err)
	}
	if want := "assoc[abc]++"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandedAssocSubscriptError(t *testing.T) {
	err := expandedAssocSubscriptError("assoc[x],b[$(echo uname >&2)]++")
	if err == nil {
		t.Fatal("expected error")
	}
	var arithErr *ArithmError
	if !errors.As(err, &arithErr) {
		t.Fatalf("got %T, want *ArithmError", err)
	}
	if got, want := arithErr.Text, `assoc[x],b[$(echo uname >&2)]++`; got != want {
		t.Fatalf("got text %q, want %q", got, want)
	}
	if got, want := err.Error(), `error token is "],b[$(echo uname >&2)"`; !strings.Contains(got, want) {
		t.Fatalf("got %q, want to contain %q", got, want)
	}

	err = expandedAssocSubscriptError("0],b[1")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.As(err, &arithErr) {
		t.Fatalf("got %T, want *ArithmError", err)
	}
	if got, want := arithErr.Text, `0\],b\[1`; got != want {
		t.Fatalf("got text %q, want %q", got, want)
	}
	if got, want := err.Error(), `error token is "\],b\[1"`; !strings.Contains(got, want) {
		t.Fatalf("got %q, want to contain %q", got, want)
	}
}
