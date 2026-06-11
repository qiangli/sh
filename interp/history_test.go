package interp

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func TestBuildHistTimeline(t *testing.T) {
	src := `echo one
# a comment line
for x in one two three
do
	:
done
cat <<!
body1
body2
!
echo a; echo b
echo "(left
mid
right)"
`
	groups := buildHistTimeline([]byte(src))
	want := []string{
		"echo one",
		"# a comment line",
		"for x in one two three; do \t:; done",
		"cat <<!\nbody1\nbody2\n!\n",
		"echo a; echo b",
		"echo \"(left\nmid\nright)\"",
	}
	if len(groups) != len(want) {
		t.Fatalf("got %d groups, want %d: %#v", len(groups), len(want), groups)
	}
	for i, w := range want {
		if groups[i].text != w {
			t.Errorf("group %d:\n got: %q\nwant: %q", i, groups[i].text, w)
		}
	}
}

func TestReadHistFileTimestamps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "histfile")
	content := "#1\necho below zero\n#2\n\n\necho one\n#3\necho two\n\n\n#4\necho three\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, _, err := readHistFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"echo below zero",
		"echo one",
		"echo two\n\n",
		"echo three",
	}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %#v", len(entries), len(want), entries)
	}
	for i, w := range want {
		if entries[i] != w {
			t.Errorf("entry %d:\n got: %q\nwant: %q", i, entries[i], w)
		}
	}

	// Without timestamps, every line is one entry.
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("ls\necho hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, nlines, err := readHistFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0] != "ls" || entries[1] != "echo hi" || nlines != 2 {
		t.Fatalf("plain file: got %#v (lines=%d)", entries, nlines)
	}
}

// runHistScript writes src to a script file and interprets it the way
// cmd/bashy would, returning combined output.
func runHistScript(t *testing.T, src string) string {
	t.Helper()
	histReset()
	t.Cleanup(histReset)
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(path, []byte(src), 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), path)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	r, err := New(Dir(dir), StdIO(nil, &buf, &buf))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background(), file); err != nil {
		t.Logf("run error: %v", err)
	}
	return buf.String()
}

func TestHistoryBuiltinRecording(t *testing.T) {
	out := runHistScript(t, `HISTFILE=/dev/null
set -o history
echo start
echo next
history
`)
	want := "start\nnext\n    1  echo start\n    2  echo next\n    3  history\n"
	if out != want {
		t.Errorf("recording:\n got: %q\nwant: %q", out, want)
	}
}

func TestHistoryBuiltinDeleteAndStifle(t *testing.T) {
	out := runHistScript(t, `HISTFILE=/dev/null
HISTSIZE=4
set -o history
echo 1
echo 2
echo 3
echo 4
echo 5
history -d -1
history
history -d 99
`)
	// Note: `history -d -1` is itself recorded first, so it deletes its
	// own entry (matching bash), and the later `history` line is
	// recorded before it lists.
	want := "1\n2\n3\n4\n5\n" +
		"    3  echo 3\n    4  echo 4\n    5  echo 5\n" +
		"    6  history\nhistory: 99: history position out of range\n"
	if out != want {
		t.Errorf("delete/stifle:\n got: %q\nwant: %q", out, want)
	}
}

func TestHistoryBuiltinIgnoreAndFc(t *testing.T) {
	out := runHistScript(t, `HISTFILE=/dev/null
HISTIGNORE='&:history*:fc*'
set -o history
echo aa bb
fc -l
fc -nl -1
fc -s aa=cc
`)
	want := "aa bb\n" +
		"1\t echo aa bb\n" +
		"\t echo aa bb\n" +
		"echo cc bb\ncc bb\n"
	if out != want {
		t.Errorf("histignore/fc:\n got: %q\nwant: %q", out, want)
	}
}

func TestHistoryExpansionDesignators(t *testing.T) {
	out := runHistScript(t, `HISTFILE=/dev/null
set -o history
echo line one
set -H
!!
!e
`)
	// `!!` repeats the previous command (`set -H`); `!e` repeats the
	// last command starting with `e`. Each expansion is echoed to
	// stderr before running, and the designator must not fall through
	// to command lookup (`!!: command not found`).
	want := "line one\nset -H\necho line one\nline one\n"
	if out != want {
		t.Errorf("history expansion:\n got: %q\nwant: %q", out, want)
	}
}

func TestHistorySAndW(t *testing.T) {
	dir := t.TempDir()
	hf := filepath.Join(dir, "histfile")
	out := runHistScript(t, `HISTFILE=/dev/null
set -o history
history -s "echo stored"
history -p '!!'
history -w `+hf+`
`)
	want := "echo stored\n"
	if out != want {
		t.Errorf("history -s/-p:\n got: %q\nwant: %q", out, want)
	}
	data, err := os.ReadFile(hf)
	if err != nil {
		t.Fatal(err)
	}
	// The history/set lines themselves are recorded too; the stored
	// entry must be present.
	if !strings.Contains(string(data), "echo stored\n") {
		t.Errorf("history -w file missing entry: %q", string(data))
	}
}
