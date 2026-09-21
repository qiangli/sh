package polyglot

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

// CPython 23116f998f6789d8c2fbe5ed5b8146854c8c2a4f (PSF-2.0),
// Lib/test/test_generators.py: close executes finally, exhaustion and exceptions
// terminate independently from already-yielded values. These are independent
// bridge fixtures, not copies of the upstream test harness.
func TestIteratorPythonLifecycle(t *testing.T) {
	plan := pythonPlanWithRuntime(t, `def state(n: int = -1) -> int:
    if n >= 0: state.n=n
    return getattr(state,'n',0)
def values() -> Iterator[int]:
    try:
        yield state()
        yield state()+1
    finally:
        state(99)
        print('closed')
def bad() -> Iterator[int]:
    yield 7
    raise ValueError('partial')
`, Python{})
	m := Start(plan, Python{})
	defer m.Close()
	ctx := t.Context()
	m.Call(ctx, "state", int64(8))
	it, _, err := m.OpenIterator(ctx, "values", nil)
	if err != nil {
		t.Fatal(err)
	}
	value, more, err := it.Next(ctx)
	if err != nil || !more || value.Value != int64(8) {
		t.Fatalf("%+v %v %v", value, more, err)
	}
	closeResult, err := it.Close(ctx)
	if err != nil || closeResult.Stdout != "closed\n" {
		t.Fatalf("%+v %v", closeResult, err)
	}
	state, err := m.Call(ctx, "state")
	if err != nil || state.Value != int64(99) {
		t.Fatalf("%+v %v", state, err)
	}
	if _, more, err = it.Next(ctx); err != nil || more {
		t.Fatalf("closed iterator: %v %v", more, err)
	}
	it, _, err = m.OpenIterator(ctx, "bad", nil)
	if err != nil {
		t.Fatal(err)
	}
	if value, more, err = it.Next(ctx); err != nil || !more || value.Value != int64(7) {
		t.Fatalf("%+v %v %v", value, more, err)
	}
	if _, more, err = it.Next(ctx); err == nil || more || !strings.Contains(err.Error(), "partial") {
		t.Fatalf("%v %v", more, err)
	}
	it.Close(ctx)
}

func TestIteratorFilterInputCancel(t *testing.T) {
	plan := pythonPlanWithRuntime(t, "def upper(stdin: TextIO) -> Iterator[str]:\n    for line in stdin: yield line.upper()\n", Python{})
	m := Start(plan, Python{})
	defer m.Close()
	read, write := io.Pipe()
	defer read.Close()
	defer write.Close()
	it, _, err := m.OpenIterator(t.Context(), "upper", []any{FilterInput(read)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, _, err = it.Next(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	it.Close(t.Context())
	if len(m.callbackTable) != 0 {
		t.Fatal("expired filter callback retained")
	}
}

func TestIteratorCallbackOpenFailure(t *testing.T) {
	plan := pythonPlanWithRuntime(t, "def f() -> int: return 1\n", Python{})
	m := Start(plan, Python{})
	defer m.Close()
	_, _, err := m.OpenIterator(t.Context(), "missing", []any{FilterInput(strings.NewReader(""))})
	if err == nil {
		t.Fatal("missing callable accepted")
	}
	if len(m.callbackTable) != 0 {
		t.Fatal("failed open retained callback authority")
	}
}

func TestIteratorObjectCodec(t *testing.T) {
	plan := pythonPlanWithRuntime(t, `def values() -> Iterator[Any]:
    yield {'nested':[b'abc',7,True]}
    yield object()
`, Python{})
	m := Start(plan, Python{})
	defer m.Close()
	it, _, err := m.OpenIterator(t.Context(), "values", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close(t.Context())
	value, more, err := it.Next(t.Context())
	if err != nil || !more {
		t.Fatalf("%v %v", more, err)
	}
	encoded, err := m.EncodeIteratorFrame(t.Context(), value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := m.DecodeIteratorFrame(t.Context(), string(encoded), "any")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Value, value.Value) {
		t.Fatalf("codec changed value: %#v -> %#v", value.Value, decoded.Value)
	}
	value, more, err = it.Next(t.Context())
	if err != nil || !more {
		t.Fatalf("%v %v", more, err)
	}
	encoded, err = m.EncodeIteratorFrame(t.Context(), value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = m.DecodeIteratorFrame(t.Context(), string(encoded), "any")
	if err != nil {
		t.Fatal(err)
	}
	original := value.Value.(*Handle)
	handle := decoded.Value.(*Handle)
	if handle.Type != original.Type || handle.ID != original.ID || handle.module != m || handle.generation != original.generation {
		t.Fatal("handle ownership changed")
	}
}

// The worker's text stream is portable LF, but explicit binary output remains
// bytes. In particular, normalizing the final capture would corrupt raw CRLF.
func TestPythonWorkerTextAndRawNewlines(t *testing.T) {
	plan := pythonPlanWithRuntime(t, `def output() -> int:
    import os,sys
    print('text',flush=True)
    os.write(1,b'raw\r\n')
    print('warning',file=sys.stderr,flush=True)
    os.write(2,b'raw-warning\r\n')
    return 0
`, Python{})
	module := Start(plan, Python{})
	defer module.Close()
	got, err := module.Call(t.Context(), "output")
	if err != nil || got.Stdout != "text\nraw\r\n" || got.Stderr != "warning\nraw-warning\r\n" {
		t.Fatalf("%+v %v", got, err)
	}
}
