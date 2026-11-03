//go:build darwin

package interp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

func TestDarwinProcSubstDescriptorLifetime(t *testing.T) {
	reference, err := sourceAliasBashReference()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ name, src, want string }{
		{"nested external readers", `f(){ saved=$1; /usr/bin/true; cat "$1"; cat "$1"; test -e "$1" && echo inside-live; }; f <(printf 'hello\n'); test -e "$saved" || echo after-gone`, "hello\ninside-live\nafter-gone\n"},
		{"read numeric descriptor", `f(){ n=${1##*/}; read -r -u "$n" x; echo "$x"; }; f <(echo hi)`, "hi\n"},
		{"close input descriptor", `f(){ saved=$1; n=${1##*/}; eval "exec $n<&-"; cat "$1" 2>/dev/null; echo status=$?; }; f <(echo hi); test -e "$saved" || echo closed`, "status=1\nclosed\n"},
		{"close output before wait", `f(){ n=${1##*/}; eval "exec $n>&-"; wait; }; f >(cat) 2>/dev/null`, ""},
		{"close output descriptor", `f(){ n=${1##*/}; eval "exec $n>&-"; echo hi > "$1" 2>/dev/null; echo status=$?; }; f >(cat) 2>/dev/null; wait`, "status=1\n"},
		{"nested temporary close", `g(){ :; }; f(){ n=${1##*/}; eval "g $n<&-"; cat "$1"; }; f <(echo kept)`, "kept\n"},
		{"temporary descriptor close", `f(){ n=${1##*/}; eval ": $n<&-"; cat "$1"; }; f <(echo kept)`, "kept\n"},
		{"retained numeric duplicate", `f(){ n=${1##*/}; eval "exec 9<&$n"; }; f <(echo retained); read -r -u9 x; echo "$x"; exec 9<&-`, "retained\n"},
		{"low logical limit", `ulimit -n 32; cat <(echo low)`, "low\n"},
		{"explicit replacement", `f(){ n=${1##*/}; eval "exec $n</dev/null"; cat "$1"; echo replaced; }; f <(echo old)`, "replaced\n"},
		{"failed external cleanup", `f(){ saved=$1; /no-such-s557-command "$1" 2>/dev/null; }; f <(echo unused); test -e "$saved" || echo closed`, "closed\n"},
		{"empty input", `cat <(:); echo done`, "done\n"},
		{"empty output", `: > >(cat); wait; echo done`, "done\n"},
		{"external output", `printf 'hello\n' | tee >(cat) >/dev/null; wait`, "hello\n"},
		{"multiple output opens", `f(){ echo one > "$1"; echo two > "$1"; }; f >(cat); wait`, "one\ntwo\n"},
		{"pre-read side effect", `echo payload > >(echo ready; cat); wait`, "ready\npayload\n"},
		{"persistent named redirect", `exec {p}< <(echo retained); read -r -u "$p" value; echo "$value"; exec {p}<&-`, "retained\n"},
		{"nested substitution", `cat <(cat <(echo nested))`, "nested\n"},
		{"producer background descendant", `cat <( { cat <(echo descendant); } & )`, "descendant\n"},
		{"background inherited", `f(){ cat "$1" & }; f <(echo background); wait`, "background\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var out syncBuffer
			r, err := New(StdIO(nil, &out, &out))
			if err != nil {
				t.Fatal(err)
			}
			file, err := syntax.NewParser().Parse(strings.NewReader(tc.src), "")
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Run(ctx, file); err != nil {
				t.Fatalf("run: %v output=%q", err, out.String())
			}
			if got := out.String(); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			if len(r.procSubstDescriptors) != 0 {
				t.Fatalf("statement retained %d published endpoints", len(r.procSubstDescriptors))
			}
			// An authored differential, not a third-party suite or fixture.
			{
				got, err := exec.CommandContext(ctx, reference, "-c", tc.src).CombinedOutput()
				if err != nil {
					t.Fatalf("reference: %v %s", err, got)
				}
				if string(got) != tc.want {
					t.Fatalf("reference %q want %q", got, tc.want)
				}
			}
		})
	}
}

func TestDarwinProcSubstResetOwnership(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatal(err)
	}
	r.Reset()
	pipe, err := r.newProcSubstPipe(true)
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.cleanup()
	parent := r.procSubstDescriptor(pipe.path()).file
	child := r.Subshell()
	cloned := child.procSubstDescriptor(pipe.path()).file
	r.Reset()
	if fileOpen(parent) || !fileOpen(cloned) {
		t.Fatal("Reset did not isolate parent and child ownership")
	}
	child.Reset()
	if fileOpen(cloned) {
		t.Fatal("child Reset retained descriptor")
	}
}
func TestDarwinProcSubstConcurrentFork(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				case <-ctx.Done():
					return
				default:
				}
				_ = exec.CommandContext(ctx, "/usr/bin/true").Run()
			}
		}()
	}
	defer func() { close(stop); cancel(); wg.Wait() }()
	for i := 0; i < 1000; i++ {
		var out syncBuffer
		r, err := New(StdIO(nil, &out, &out))
		if err != nil {
			t.Fatal(err)
		}
		file, err := syntax.NewParser().Parse(strings.NewReader(". <(echo 'echo sourced')"), "")
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Run(ctx, file); err != nil {
			t.Fatal(err)
		}
		if out.String() != "sourced\n" {
			t.Fatalf("iteration%d output=%q", i, out.String())
		}
	}
}
func TestDarwinProcSubstExecFallback(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "read-substitution")
	if err := os.WriteFile(script, []byte("cat \"$1\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var out syncBuffer
	r, err := New(StdIO(nil, &out, &out))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, execStartOverrideCtxKey{}, func(cmd *exec.Cmd) error {
		calls++
		if calls == 1 {
			return syscall.ENOEXEC
		}
		if calls != 2 {
			return errors.New("unexpected additional fallback")
		}
		path := cmd.Args[len(cmd.Args)-1]
		// Exercise the fallback's actual ExtraFiles with a bounded native reader;
		// re-executing the package testbinary as a shell would recurse TestMain.
		cmd.Path = "/bin/cat"
		cmd.Args = []string{"cat", path}
		return cmd.Start()
	})
	file, err := syntax.NewParser().Parse(strings.NewReader(script+" <(echo fallback)"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Run(ctx, file); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || out.String() != "fallback\n" {
		t.Fatalf("calls%d output%q", calls, out.String())
	}
}
func TestDarwinProcSubstManyDescriptors(t *testing.T) {
	src := "cat" + strings.Repeat(" <(printf x)", 80)
	if got := runProcSubstScript(t, src); got != strings.Repeat("x", 80) {
		t.Fatalf("got %q", got)
	}
	reference, err := sourceAliasBashReference()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := exec.CommandContext(ctx, reference, "-c", src).CombinedOutput()
	if err != nil || string(got) != strings.Repeat("x", 80) {
		t.Fatalf("reference %v %q", err, got)
	}
}
func TestDarwinProcSubstCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out syncBuffer
	r, err := New(StdIO(nil, &out, &out), CallHandler(func(_ context.Context, args []string) ([]string, error) {
		if len(args) > 0 && args[0] == "cancelnow" {
			cancel()
			return []string{":"}, nil
		}
		return args, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	file, err := syntax.NewParser().Parse(strings.NewReader("cat <(cancelnow; while :; do :; done)"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Run(ctx, file); !errors.Is(err, context.Canceled) {
		t.Fatalf("run=%v output=%q", err, out.String())
	}
	if len(r.procSubstDescriptors) != 0 {
		t.Fatal("cancellation retained published descriptors")
	}
	for _, bg := range r.bgProcs {
		select {
		case <-bg.done:
		case <-time.After(time.Second):
			t.Fatal("producer did not terminate")
		}
	}
}

func TestDarwinProcSubstChildMetadata(t *testing.T) {
	for _, declared := range []bool{false, true} {
		t.Run(fmt.Sprint(declared), func(t *testing.T) {
			r, err := New()
			if err != nil {
				t.Fatal(err)
			}
			r.Reset()
			defer r.Reset()
			pipe, err := r.newProcSubstPipe(true)
			if err != nil {
				t.Fatal(err)
			}
			defer pipe.cleanup()
			fd, ok := r.ownedProcSubstDescriptorFD(pipe.path())
			if !ok {
				t.Fatal("not owned")
			}
			for _, closed := range []bool{false, true} {
				if closed {
					r.fdClosedTable = map[int]bool{fd: true}
				}
				cmd := exec.Command("/usr/bin/true", "unchanged")
				cmd.Env = []string{"KEEP=first", "KEEP=exact"}
				if declared {
					cmd.Env = append(cmd.Env, BashyInheritedFdsEnv+"=9,"+strconv.Itoa(fd))
				}
				cleanup, err := r.prepareOwnedChildDescriptors(cmd)
				if err != nil {
					t.Fatal(err)
				}
				want := []string{"KEEP=first", "KEEP=exact"}
				if declared {
					value := "9"
					if !closed {
						value += "," + strconv.Itoa(fd)
					}
					want = append(want, BashyInheritedFdsEnv+"="+value)
				}
				if !slices.Equal(cmd.Env, want) {
					t.Fatalf("environment %q want %q", cmd.Env, want)
				}
				if cmd.Args[1] != "unchanged" {
					t.Fatal("arguments changed")
				}
				if fileOpen(cmd.ExtraFiles[fd-3]) == closed {
					t.Fatal("wrong owned-slot state")
				}
				cleanup()
			}
		})
	}
}
