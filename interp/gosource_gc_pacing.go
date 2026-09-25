// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

// Sprint: #270; Story: #757; Story-ID: 609c89bfa598
//
// Interpreter collection pacing for Go-source runs. The evaluator keeps a
// small live heap but allocates short-lived cells, frames and strings at a
// high rate, so at the default GOGC=100 the host process collects hundreds of
// times per second; the corpus profiles spent more time starting and stopping
// the world, preempting and re-committing scavenged spans than in the
// evaluator itself. While at least one Go-source program runs, the host's
// collector target is raised. This is the interpreter's own heap only: the
// program's runtime (runtime.GC, MemStats, SetGCPercent, finalizers observed
// through the dependency helper) is untouched, and an explicit GOGC in the
// host environment is always honoured.

import (
	"os"
	"runtime/debug"
	"sync"
)

const goSourceInterpreterGCPercent = 400

var goSourceGCPacing struct {
	mu     sync.Mutex
	active int
	saved  int
}

// goSourceRaiseGCPacing raises the host collector target for one Go-source
// run and returns the matching release.
func goSourceRaiseGCPacing() func() {
	if _, set := os.LookupEnv("GOGC"); set {
		return func() {}
	}
	p := &goSourceGCPacing
	p.mu.Lock()
	if p.active == 0 {
		p.saved = debug.SetGCPercent(goSourceInterpreterGCPercent)
		if p.saved < 0 || p.saved > goSourceInterpreterGCPercent {
			// Collection was already off or laxer; keep the host's choice.
			debug.SetGCPercent(p.saved)
		}
	}
	p.active++
	p.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			p.active--
			if p.active == 0 {
				debug.SetGCPercent(p.saved)
			}
			p.mu.Unlock()
		})
	}
}
