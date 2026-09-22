// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/pathconv"
	"mvdan.cc/sh/v3/syntax"
)

// Sprint 245, story 682 (procsub): the Windows process-substitution pipe is
// created as \\.\pipe\sh-np-<hex> but spelled //./pipe/sh-np-<hex> on the
// command line, so `eval cat <(echo x)` does not lose its backslashes to
// the re-read. The shell recognises both spellings for its own open and
// stat of the path.

func TestStory682ProcSubstPipeNames(t *testing.T) {
	t.Parallel()
	native, shell := windowsProcSubstPipeNames("4a373c478eda0e4b")
	if native != `\\.\pipe\sh-np-4a373c478eda0e4b` {
		t.Errorf("native spelling = %q", native)
	}
	if shell != `//./pipe/sh-np-4a373c478eda0e4b` {
		t.Errorf("shell spelling = %q", shell)
	}
	// The command-line spelling survives being read again as shell input
	// unquoted, which is what eval does with the expanded word.
	// eval re-reads the expanded word as shell input: the //./ spelling comes
	// back unchanged, the native one loses its backslashes to escaping.
	reread := func(spelling string) string {
		// Single quotes keep the first read from touching the word, as the
		// procsub expansion does not: only eval's own re-read applies.
		file, err := syntax.NewParser().Parse(strings.NewReader("eval echo '"+spelling+"'"), "")
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		r, err := New(StdIO(nil, &out, &out))
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Run(context.Background(), file); err != nil {
			t.Fatal(err)
		}
		return strings.TrimSuffix(out.String(), "\n")
	}
	if got := reread(shell); got != shell {
		t.Errorf("eval re-read of %q = %q", shell, got)
	}
	if got := reread(native); got != `\.pipesh-np-4a373c478eda0e4b` {
		t.Errorf("eval re-read of %q = %q; expected the backslashes to be eaten", native, got)
	}
}

func TestStory682ProcSubstPipePathRecognised(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path    string
		windows bool
		want    bool
	}{
		{`//./pipe/sh-np-4a37`, true, true},
		{`\\.\pipe\sh-np-4a37`, true, true},
		{`//./pipe/sh-np-4a37`, false, false}, // not a Unix spelling
		{`//./pipe/other`, true, false},
		{`//./pipe/sh-np-4a37/x`, true, false},
		{`/tmp/sh-np-4a37`, true, false},
		{`//./pipe/`, true, false},
		{``, true, false},
	}
	for _, c := range cases {
		if got := isProcSubstPipePathMode(c.path, c.windows); got != c.want {
			t.Errorf("isProcSubstPipePathMode(%q, windows=%v) = %v, want %v", c.path, c.windows, got, c.want)
		}
	}
	info, ok := procSubstPipeStatMode(`//./pipe/sh-np-4a37`, true)
	if !ok {
		t.Fatal("no synthetic stat for the shell spelling")
	}
	if info.Mode()&fs.ModeNamedPipe == 0 || info.Mode()&0o600 != 0o600 || info.IsDir() || info.Name() != "sh-np-4a37" {
		t.Errorf("synthetic stat = %v %q", info.Mode(), info.Name())
	}
	if info, ok := procSubstPipeStatMode(`\\.\pipe\sh-np-4a37`, true); !ok || info.Name() != "sh-np-4a37" {
		t.Errorf("native spelling stat = %v, %v", info, ok)
	}
	if _, ok := procSubstPipeStatMode(`//./pipe/sh-np-4a37`, false); ok {
		t.Error("a Unix host has no Windows pipes to stat")
	}
}

// The path must reach CreateFile intact: the mount table passes a leading //
// through, absolute joining leaves it alone, and the share-delete open
// (which would CreateFile it with file-oriented flags) declines it so the
// plain os.OpenFile path is taken.
func TestStory682ProcSubstPipePathPassesThroughPathconv(t *testing.T) {
	t.Parallel()
	const p = `//./pipe/sh-np-4a37`
	m := pathconv.NewMounts(`C:\msys64`, nil, `C:\Temp`)
	if got := pathconv.ToOSMountsMode(m, `C:\work`, p, true); got != p {
		t.Errorf("ToOSMountsMode = %q, want passthrough", got)
	}
	if got := pathconv.ToOSMode(`/c/work`, p, true); got != p {
		t.Errorf("ToOSMode = %q, want passthrough", got)
	}
	if got := shellPathJoinAbsMode(`/c/work`, p, true); got != p {
		t.Errorf("shellPathJoinAbsMode = %q, want passthrough", got)
	}
	if windowsShareDeleteEligible(p) || windowsShareDeleteEligible(`\\.\pipe\sh-np-4a37`) {
		t.Error("a device path must not take the share-delete CreateFile")
	}
}

// The replay window behind a second open (procsubst_body.go). It is
// platform-neutral so the Windows serving logic's core is proven on any
// host; the pipe itself is exercised by TestStory682ProcSubstPipeTwoOpens.

// TestStory682ProcSubstBodyReplaysToEveryReader is the `diff <(t1) <(t2)`
// case: a consumer opens the substituted path a second time and must see
// the same bytes, from the start.
func TestStory682ProcSubstBodyReplaysToEveryReader(t *testing.T) {
	t.Parallel()

	body := []byte("line one\nline two\nline three\n")
	b := newProcSubstBody(procSubstReplayCap)
	if _, err := b.Write(body); err != nil {
		t.Fatal(err)
	}
	b.close()
	for i := range 3 {
		r := b.newReader()
		got, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatalf("reader %d: %v", i, err)
		}
		if !bytes.Equal(got, body) {
			t.Errorf("reader %d read %q, want %q", i, got, body)
		}
	}
}

// A reader that opens while the substitution is still writing is replayed
// what it missed and then follows the live stream to EOF.
func TestStory682ProcSubstBodyLateReaderFollowsLive(t *testing.T) {
	t.Parallel()

	b := newProcSubstBody(procSubstReplayCap)
	first := b.newReader()
	defer first.Close()
	if _, err := b.Write([]byte("head ")); err != nil {
		t.Fatal(err)
	}
	// The first reader drains what is there; the second has not opened yet.
	buf := make([]byte, 5)
	if n, err := first.Read(buf); err != nil || string(buf[:n]) != "head " {
		t.Fatalf("first read = %q, %v", buf[:n], err)
	}
	late := b.newReader()
	defer late.Close()
	go func() {
		b.Write([]byte("tail"))
		b.close()
	}()
	got, err := io.ReadAll(late)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "head tail" {
		t.Errorf("late reader read %q, want the whole body", got)
	}
}

// Past the replay window the body is no longer retained from its first
// byte: the writer is throttled by the slowest live reader, memory stays
// bounded, and a reader that opens late sees only what is still held.
func TestStory682ProcSubstBodyBoundsTheWindow(t *testing.T) {
	t.Parallel()

	const capBytes = 64
	b := newProcSubstBody(capBytes)
	live := b.newReader()
	defer live.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 8 {
			if _, err := b.Write(bytes.Repeat([]byte("x"), capBytes)); err != nil {
				return
			}
		}
		b.close()
	}()
	n, err := io.Copy(io.Discard, live)
	if err != nil {
		t.Fatal(err)
	}
	if n != 8*capBytes {
		t.Errorf("live reader read %d bytes, want %d", n, 8*capBytes)
	}
	<-done
	if b.start == 0 {
		t.Error("the window never advanced, so it was never bounded")
	}
	if len(b.buf) > capBytes {
		t.Errorf("retained %d bytes, want at most %d", len(b.buf), capBytes)
	}
	// A reader opening now starts at the oldest byte still held, not at
	// byte zero: the replay guarantee ends with the window.
	late := b.newReader()
	defer late.Close()
	rest, err := io.ReadAll(late)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(rest)) != b.end-b.start {
		t.Errorf("late reader read %d bytes, want the %d still held", len(rest), b.end-b.start)
	}
}

// abort is the teardown path: a substitution still writing fails as it
// would on a broken pipe, and the readers stop where they are.
func TestStory682ProcSubstBodyAbort(t *testing.T) {
	t.Parallel()

	b := newProcSubstBody(procSubstReplayCap)
	r := b.newReader()
	defer r.Close()
	if _, err := b.Write([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	b.abort()
	if _, err := b.Write([]byte("more")); err == nil {
		t.Error("a write after abort must fail")
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "partial" {
		t.Errorf("read %q, want the bytes written before the abort", got)
	}
}
