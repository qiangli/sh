//go:build unix && full

package interp_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// A portable child helper makes process-group assertions independent of ps,
// sh, or host utility output. It is spawned by the actual default exec handler.
func TestStreamGroupChildHelper(t *testing.T) {
	mode := os.Getenv("BASHPP_STREAM_GROUP_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "identity":
		fmt.Printf("child=%d group=%d\n", os.Getpid(), syscall.Getpgrp())
	case "copy":
		fmt.Printf("child=%d group=%d\n", os.Getpid(), syscall.Getpgrp())
		_, _ = io.Copy(os.Stdout, os.Stdin)
	case "first":
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		fmt.Print(line)
	}
	os.Exit(0)
}

func streamGroupChild(t *testing.T, mode string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return "GOSH_PROG= GOSH_CMD= BASHPP_STREAM_GROUP_HELPER=" + mode + " " + strconv.Quote(exe) + " -test.run=^TestStreamGroupChildHelper$"
}

const streamGroupFixture = `~~~python as py
def report(stdin: TextIO) -> Iterator[str]:
    import os
    globals()['count'] = globals().get('count', 0) + 1
    yield 'worker=%d group=%d\n' % (os.getpid(), os.getpgrp())
    for line in stdin:
        yield line
def state() -> int:
    return globals().get('count', 0)
~~~
`

func requireStreamGroupPython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
}

func TestForeignStreamPipelineActualGroupAndPersistentState(t *testing.T) {
	requireStreamGroupPython(t)
	source := streamGroupFixture + streamGroupChild(t, "identity") + " | py.report | " + streamGroupChild(t, "copy") + "\n" + `n := py.state()
echo "state=$n"
`
	out, stderr, err := runStreamGroup(t, source)
	if err != nil || stderr != "" {
		t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
	}
	group := 0
	identities := 0
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !strings.HasPrefix(fields[1], "group=") {
			continue
		}
		id, err := strconv.Atoi(strings.TrimPrefix(fields[1], "group="))
		if err != nil {
			t.Fatal(err)
		}
		if group == 0 {
			group = id
		}
		if id != group {
			t.Fatalf("different pipeline groups: %q", out)
		}
		identities++
	}
	if identities != 3 || group == syscall.Getpgrp() || !strings.Contains(out, "state=1\n") {
		t.Fatalf("identities=%d group=%d output=%q", identities, group, out)
	}
}

func TestForeignStreamPipelineWrapperAndDynamicRefusal(t *testing.T) {
	requireStreamGroupPython(t)
	t.Run("wrapper", func(t *testing.T) {
		out, stderr, err := runStreamGroup(t, streamGroupFixture+"wrapped() { py.report; }\nprintf 'input\\n' | wrapped\n")
		if err != nil || stderr != "" || !strings.Contains(out, "input\n") {
			t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
		}
	})
	t.Run("sequential wrapper calls", func(t *testing.T) {
		out, stderr, err := runStreamGroup(t, streamGroupFixture+"wrapped() { py.report; py.report; }\nprintf 'input\\n' | wrapped\nn := py.state()\necho state=$n\n")
		if err != nil || stderr != "" || !strings.Contains(out, "state=2\n") {
			t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
		}
	})
	t.Run("alias", func(t *testing.T) {
		out, stderr, err := runStreamGroup(t, streamGroupFixture+"shopt -s expand_aliases\nalias wrapped=py.report\nprintf 'input\\n' | wrapped\n")
		if err != nil || stderr != "" || !strings.Contains(out, "input\n") {
			t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
		}
	})
	t.Run("dynamic", func(t *testing.T) {
		out, stderr, err := runStreamGroup(t, streamGroupFixture+"name=py.report\nprintf 'input\\n' | $name\n")
		if err == nil || !strings.Contains(stderr, "dynamic pipeline command") || strings.Contains(out, "worker=") {
			t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
		}
	})
	t.Run("same worker twice", func(t *testing.T) {
		out, stderr, err := runStreamGroup(t, streamGroupFixture+"py.report | py.report\n")
		if err == nil || !strings.Contains(stderr, "multiple pipeline stages") || out != "" {
			t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
		}
	})
}

func TestForeignStreamPipelineEarlyConsumerExit(t *testing.T) {
	requireStreamGroupPython(t)
	source := `~~~python as py
def rows() -> Iterator[str]:
    try:
        for i in range(1000000):
            yield 'row'
    finally:
        globals()['closed'] = globals().get('closed', 0) + 1
def state() -> int:
    return globals().get('closed', 0)
~~~
py.rows | ` + streamGroupChild(t, "first") + `
n := py.state()
echo "closed=$n"
`
	out, stderr, err := runStreamGroup(t, source)
	if err != nil || !strings.Contains(out, "closed=1\n") {
		t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
	}
}

type streamGroupBuffer struct {
	mu    sync.Mutex
	b     strings.Builder
	ready chan struct{}
	once  sync.Once
}

func (b *streamGroupBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.b.Write(p)
	if strings.Contains(b.b.String(), "worker=") {
		b.once.Do(func() { close(b.ready) })
	}
	return n, err
}
func (b *streamGroupBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }

func TestForeignStreamPipelineCancellationReapsGroup(t *testing.T) {
	requireStreamGroupPython(t)
	source := `~~~python as py
def rows() -> Iterator[str]:
    import os, time
    yield 'worker=%d group=%d' % (os.getpid(), os.getpgrp())
    time.sleep(60)
    yield 'late'
~~~
py.rows | ` + streamGroupChild(t, "copy") + "\n"
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(source), "stream-cancel.bpp")
	if err != nil {
		t.Fatal(err)
	}
	out := &streamGroupBuffer{ready: make(chan struct{})}
	stderr := &streamGroupBuffer{ready: make(chan struct{})}
	runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.StdIO(nil, out, stderr))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx, file) }()
	select {
	case <-out.ready:
	case err := <-done:
		t.Fatalf("exited before ready: %v/%q/%q", err, out.String(), stderr.String())
	case <-time.After(15 * time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not join pipeline")
	}
	for _, line := range strings.Split(out.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		id := strings.TrimPrefix(strings.TrimPrefix(fields[0], "worker="), "child=")
		pid, err := strconv.Atoi(id)
		if err != nil {
			continue
		}
		if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
			t.Errorf("process %d not reaped: %v; output=%q", pid, err, out.String())
		}
	}
}

func TestClassicPipelineHasNoForeignGroup(t *testing.T) {
	source := streamGroupChild(t, "identity") + " | " + streamGroupChild(t, "copy") + "\n"
	file, err := syntax.NewParser().Parse(strings.NewReader(source), "classic.sh")
	if err != nil {
		t.Fatal(err)
	}
	out := &streamGroupBuffer{ready: make(chan struct{})}
	stderr := &streamGroupBuffer{ready: make(chan struct{})}
	runner, err := interp.New(interp.StdIO(nil, out, stderr))
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.Run(t.Context(), file); err != nil {
		t.Fatalf("%v: %s", err, stderr.String())
	}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if !strings.HasSuffix(line, fmt.Sprintf("group=%d", syscall.Getpgrp())) {
			t.Fatalf("classic group changed: %q", out.String())
		}
	}
}

func runStreamGroup(t *testing.T, source string) (string, string, error) {
	t.Helper()
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(source), "stream-group.bpp")
	if err != nil {
		t.Fatal(err)
	}
	out := &streamGroupBuffer{ready: make(chan struct{})}
	stderr := &streamGroupBuffer{ready: make(chan struct{})}
	runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.StdIO(nil, out, stderr))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	err = runner.Run(ctx, file)
	return out.String(), stderr.String(), err
}

func TestForeignStreamPipelineBackgroundSignalReapsWorker(t *testing.T) {
	requireStreamGroupPython(t)
	marker := t.TempDir() + "/ready"
	source := fmt.Sprintf(`~~~python as py
def rows() -> Iterator[str]:
    import os, time
    with open(%q, 'w') as marker:
        marker.write(str(os.getpid()))
    yield 'ready'
    time.sleep(60)
    yield 'late'
~~~
py.rows | %s &
while [ ! -f %q ]; do sleep 0.01; done
kill -TERM %%1
wait
`, marker, streamGroupChild(t, "copy"), marker)
	out, stderr, err := runStreamGroup(t, source)
	if err != nil {
		t.Fatalf("background job did not finish after signal: %v; out=%q stderr=%q", err, out, stderr)
	}
	data, readErr := os.ReadFile(marker)
	if readErr != nil {
		t.Fatalf("marker: %v; out=%q stderr=%q run=%v", readErr, out, stderr, err)
	}
	pid, parseErr := strconv.Atoi(string(data))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	if status := syscall.Kill(pid, 0); status != syscall.ESRCH {
		t.Fatalf("background worker %d survived job signal: %v; out=%q stderr=%q run=%v", pid, status, out, stderr, err)
	}
	if strings.Contains(out, "late") {
		t.Fatalf("signal did not terminate worker: %q", out)
	}
}
