// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

package interp_test

import "testing"

func TestConcurrencyLeakIdentity(t *testing.T) {
	baseline := cgParseGoroutines("goroutine 12324 [select, 10 minutes]:\nproduct.wait()\ncreated by product.start in goroutine 12254")
	d := &cgLeakDetector{baseline: map[string]bool{baseline[0].identity(): true}}
	for _, header := range []string{
		"goroutine 12324 [select, 11 minutes]:",
		"goroutine 12324 [runnable]:",
		"goroutine 12324 [chan receive]:",
	} {
		g := cgParseGoroutines(header + "\nproduct.wait()")[0]
		if !d.baseline[g.identity()] {
			t.Errorf("existing goroutine misidentified: %s", header)
		}
	}
	for _, header := range []string{
		"goroutine 12325 [select, 10 minutes]:",
		"goroutine 12325 [select, 11 minutes]:",
		"goroutine invalid [select]:",
		"goroutine 0 [select]:",
	} {
		g := cgParseGoroutines(header + "\nproduct.wait()")[0]
		if d.baseline[g.identity()] {
			t.Errorf("new goroutine hidden: %s", header)
		}
	}
}
