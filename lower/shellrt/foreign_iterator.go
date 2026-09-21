package shellrt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mvdan.cc/sh/v3/polyglot"
	"sync"
)

// IteratorProcess is implemented by the single B14 line-process substrate.
type IteratorProcess interface {
	Lines() <-chan string
	Wait() (int, error)
	Close() error
	Stderr() string
}

type ForeignIterator struct {
	mu      sync.Mutex
	Start   func(context.Context) (IteratorProcess, error)
	Element string
	Decode  func(context.Context, string, string) (polyglot.CallResult, error)
	used    bool
}

// IteratorSequence owns the producer for exactly this range. A break invokes
// Close before returning, so generated code never leaves an infinite producer.
func IteratorSequence(ctx context.Context, value any) func(func(any) bool) {
	return func(yield func(any) bool) {
		iterator, ok := value.(*ForeignIterator)
		if !ok {
			panic(ValueAbort{Err: fmt.Errorf("%T is not a foreign iterator", value)})
		}
		iterator.mu.Lock()
		used := iterator.used
		iterator.used = true
		iterator.mu.Unlock()
		if used {
			return
		}
		process, err := iterator.Start(ctx)
		if err != nil {
			panic(ValueAbort{Err: err})
		}
		defer func() {
			err := process.Close()
			_, _ = io.WriteString(Stderr, process.Stderr())
			if err != nil && !errors.Is(err, context.Canceled) {
				panic(ValueAbort{Err: err})
			}
		}()
		for line := range process.Lines() {
			frame, err := iterator.Decode(ctx, line, iterator.Element)
			if err != nil {
				panic(ValueAbort{Err: err})
			}
			fmt.Fprint(Stdout, frame.Stdout)
			fmt.Fprint(Stderr, frame.Stderr)
			value := frame.Value
			if !yield(value) {
				return
			}
		}
		status, err := process.Wait()
		if err != nil {
			panic(ValueAbort{Err: err})
		}
		if status != 0 {
			Status = status
			panic(ValueAbort{Err: fmt.Errorf("foreign iterator exited with status %d", status)})
		}
	}
}
