package shellrt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
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
		defer func() { _ = process.Close(); _, _ = io.WriteString(Stderr, process.Stderr()) }()
		for line := range process.Lines() {
			var value any
			decoder := json.NewDecoder(strings.NewReader(line))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				panic(ValueAbort{Err: err})
			}
			if number, ok := value.(json.Number); ok {
				if iterator.Element == "int" {
					value, err = number.Int64()
				} else {
					value, err = number.Float64()
				}
				if err != nil {
					panic(ValueAbort{Err: err})
				}
			}
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
