//go:build full

package interp_test

import (
	"sync"
	"time"
)

// startPhaseDiagnostic samples a slow phase once without changing its deadline.
// The returned stop function cancels the timer and joins any in-flight snapshot
// before exposing its result, so callers never race a diagnostic writer.
func startPhaseDiagnostic(delay time.Duration, snapshot func() string) func() string {
	stop, done := make(chan struct{}), make(chan struct{})
	var captured string
	go func() {
		defer close(done)
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-stop:
			return
		case <-timer.C:
			captured = snapshot()
		}
	}()
	var once sync.Once
	return func() string { once.Do(func() { close(stop) }); <-done; return captured }
}
