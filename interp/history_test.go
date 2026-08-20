package interp

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/expand"
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
	groups, _, _ := buildHistTimeline([]byte(src))
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

func TestInteractiveHistoryFeedsFc(t *testing.T) {
	histReset()
	t.Cleanup(histReset)

	var out bytes.Buffer
	r, err := New(
		Env(expand.ListEnviron("HISTFILE=/dev/null", "HOME="+t.TempDir())),
		StdIO(nil, &out, &out),
		Interactive(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	r.EnableInteractiveHistory()
	runLine := func(src string) {
		t.Helper()
		r.RecordInteractiveHistory(src)
		file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), "")
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Run(context.Background(), file); err != nil {
			t.Fatal(err)
		}
	}

	runLine("echo marker")
	runLine("fc -l")
	want := "marker\n1\t echo marker\n"
	if out.String() != want {
		t.Fatalf("interactive fc -l:\n got: %q\nwant: %q", out.String(), want)
	}
}

func TestFcPosixEditorAndArgs(t *testing.T) {
	run := func(t *testing.T, src string, editors *[]string) string {
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
		editorHandler := func(next ExecHandlerFunc) ExecHandlerFunc {
			return func(ctx context.Context, args []string) error {
				switch args[0] {
				case "ed", "vi":
					*editors = append(*editors, args[0])
					return nil
				}
				return next(ctx, args)
			}
		}
		var buf bytes.Buffer
		r, err := New(Dir(dir), StdIO(nil, &buf, &buf), ExecHandlers(editorHandler))
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Run(context.Background(), file); err != nil {
			t.Logf("run error: %v", err)
		}
		return buf.String()
	}

	for _, tt := range []struct {
		name string
		src  string
		want string
	}{
		{
			name: "non-posix editor fallback",
			src: `HISTFILE=/dev/null
unset FCEDIT EDITOR
set -o history
echo selected
fc
`,
			want: "vi",
		},
		{
			name: "posix editor fallback",
			src: `HISTFILE=/dev/null
unset FCEDIT EDITOR
set -o history
set -o posix
echo selected
fc
`,
			want: "ed",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var editors []string
			run(t, tt.src, &editors)
			if len(editors) != 1 || editors[0] != tt.want {
				t.Fatalf("editors = %v, want [%s]", editors, tt.want)
			}
		})
	}

	var editors []string
	out := run(t, `HISTFILE=/dev/null
set -o history
echo base
fc -s echo extra
printf 'status:%s\n' "$?"
`, &editors)
	want := "base\necho base\nbase\nstatus:0\n"
	if out != want {
		t.Errorf("non-posix fc -s extra args:\n got: %q\nwant: %q", out, want)
	}

	editors = nil
	out = run(t, `HISTFILE=/dev/null
set -o history
set -o posix
echo base
fc -s echo extra
printf 'status:%s\n' "$?"
`, &editors)
	want = "base\nfc: too many arguments\nstatus:1\n"
	if out != want {
		t.Errorf("posix fc -s extra args:\n got: %q\nwant: %q", out, want)
	}

	editors = nil
	out = run(t, `HISTFILE=/dev/null
set -o history
set -o posix
echo base
fc -l echo echo extra
printf 'status:%s\n' "$?"
`, &editors)
	if !strings.Contains(out, "\techo base\n") || !strings.HasSuffix(out, "status:0\n") {
		t.Errorf("posix fc -l extra args:\n got: %q", out)
	}
}

func TestFcReverseEditRange(t *testing.T) {
	histReset()
	t.Cleanup(histReset)
	dir := t.TempDir()
	const src = `HISTFILE=/dev/null
set -o history
: one
: two
: three
fc -e capture -1 -3
`
	path := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(path, []byte(src), 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), path)
	if err != nil {
		t.Fatal(err)
	}
	var edited string
	captureEditor := func(next ExecHandlerFunc) ExecHandlerFunc {
		return func(ctx context.Context, args []string) error {
			if args[0] != "capture" {
				return next(ctx, args)
			}
			if len(args) != 2 {
				t.Fatalf("capture args = %q, want editor and file", args)
			}
			data, err := os.ReadFile(args[1])
			if err != nil {
				return err
			}
			edited = string(data)
			return nil
		}
	}
	r, err := New(Dir(dir), ExecHandlers(captureEditor))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background(), file); err != nil {
		t.Fatal(err)
	}
	if want := ": three\n: two\n: one\n"; edited != want {
		t.Fatalf("edited commands = %q, want %q", edited, want)
	}
}

func TestFcListPosixOmitsModifiedMarker(t *testing.T) {
	out := runHistScript(t, `HISTFILE=/dev/null
HISTIGNORE='fc*'
set -o posix
set -o history
: one
fc -l -1 -1
fc -nl -1 -1
`)
	if want := "1\t: one\n\t: one\n"; out != want {
		t.Fatalf("fc listing = %q, want %q", out, want)
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
