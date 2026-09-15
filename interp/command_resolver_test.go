package interp_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// runWithResolver runs src on a fresh runner whose CommandResolver knows the
// two names an embedder might dispatch itself: `hi` (no path — a script
// body the embedder re-enters) and `gl` (a path — an exec record).
func runWithResolver(t *testing.T, src string, opts ...interp.RunnerOption) (string, error) {
	t.Helper()
	resolve := func(name string) (interp.ResolvedCommand, bool) {
		switch name {
		case "hi":
			return interp.ResolvedCommand{Desc: "hi is a registered command (script)"}, true
		case "gl":
			return interp.ResolvedCommand{Desc: "gl is a registered command (exec)", Path: "/opt/bin/gl"}, true
		}
		return interp.ResolvedCommand{}, false
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(src), "")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	opts = append([]interp.RunnerOption{interp.StdIO(nil, &out, &out), interp.CommandResolver(resolve)}, opts...)
	r, err := interp.New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
	defer cancel()
	err = r.Run(ctx, file)
	return out.String(), err
}

func TestCommandResolverIntrospection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		src  string
		want string
	}{
		// type: the resolver's description, and -t the file kind.
		{`type hi`, "hi is a registered command (script)\n"},
		{`type -t hi`, "file\n"},
		{`type gl`, "gl is a registered command (exec)\n"},
		// command -v: the path when there is one, else the bare name — the
		// builtin/function shape, since the name runs when invoked.
		{`command -v hi`, "hi\n"},
		{`command -v gl`, "/opt/bin/gl\n"},
		{`command -V hi`, "hi is a registered command (script)\n"},
		// -p prints the file that would run: a path, or nothing.
		{`type -p gl`, "/opt/bin/gl\n"},
		{`type -p hi; echo "rc=$?"`, "rc=0\n"},
		// The shell's own names still outrank the resolver …
		{`hi() { echo fn; }; type -t hi`, "function\n"},
		{`alias hi=echo; type hi`, "hi is aliased to `echo'\n"},
		// … and a name the resolver does not know is unchanged.
		{`type nosuchthing 2>&1; echo "rc=$?"`, "type: nosuchthing: not found\nrc=1\n"},
		{`command -v nosuchthing; echo "rc=$?"`, "rc=1\n"},
		// A word with a slash is a path, never a resolver question.
		{`command -v ./hi; echo "rc=$?"`, "rc=1\n"},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			t.Parallel()
			got, _ := runWithResolver(t, tc.src, interp.Params("-O", "expand_aliases"))
			if got != tc.want {
				t.Fatalf("%s:\n got %q\nwant %q", tc.src, got, tc.want)
			}
		})
	}
}

// TestCommandResolverPATHStaysPATH pins the two questions that are about
// FILES and therefore ignore the resolver: `type -P` and `hash`.
func TestCommandResolverPATHStaysPATH(t *testing.T) {
	t.Parallel()
	got, _ := runWithResolver(t, `type -P hi; echo "rc=$?"; hash hi 2>&1; echo "rc=$?"`, interp.Env(nil))
	want := "rc=1\nhash: hi: not found\nrc=1\n"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

// TestCommandResolverSubshell pins that the resolver survives the subshell
// copy, like every other embedder hook.
func TestCommandResolverSubshell(t *testing.T) {
	t.Parallel()
	got, _ := runWithResolver(t, `(type -t hi); echo "$(command -v gl)"`)
	if want := "file\n/opt/bin/gl\n"; got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

// TestNoCommandResolver pins the default: without a resolver the runner
// resolves exactly as bash does.
func TestNoCommandResolver(t *testing.T) {
	t.Parallel()
	file, _ := syntax.NewParser().Parse(strings.NewReader(`type -t hi; echo "rc=$?"`), "")
	var out bytes.Buffer
	r, _ := interp.New(interp.StdIO(nil, &out, &out))
	if err := r.Run(context.Background(), file); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "rc=1\n" {
		t.Fatalf("got %q", got)
	}
}
