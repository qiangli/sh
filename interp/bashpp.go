// Copyright (c) 2026, the bash++ authors
// See LICENSE for licensing information

package interp

import (
	"errors"
	"fmt"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// ErrObjectsUnsupported is returned when an object-valued variable is created
// on a runner which is not in the bash++ dialect, or which is in POSIX mode.
var ErrObjectsUnsupported = errors.New("objects require the bash++ dialect (interp.Lang(syntax.LangBashPP)) and are unavailable under `set -o posix`")

// Lang sets the shell language variant the runner implements, which is the
// interp-side half of the dialect seam: before it, only the parser knew about
// [syntax.LangVariant], so there was nowhere to gate a behavioral extension.
//
// The default is [syntax.LangBash], and every variant other than
// [syntax.LangBashPP] behaves exactly as it did before this option existed —
// the gate is opt-in, so existing consumers are unaffected.
//
// [syntax.LangBashPP] selects bash++, a strict superset of Bash. Its grammar is
// Bash's, unchanged; what it adds is object-valued variables (see
// [Runner.SetObject]). Pass the same variant to [syntax.Variant] when
// constructing the parser.
func Lang(l syntax.LangVariant) RunnerOption {
	return func(r *Runner) error {
		switch l {
		case syntax.LangBash, syntax.LangPOSIX, syntax.LangMirBSDKorn,
			syntax.LangBats, syntax.LangZsh, syntax.LangBashPP:
		case syntax.LangAuto:
			return fmt.Errorf("interp.Lang: LangAuto must be resolved to a concrete variant first")
		default:
			return fmt.Errorf("interp.Lang: unknown shell language variant: %#b", l)
		}
		r.dialect = l
		if r.ecfg != nil {
			r.ecfg.Lang = l
		}
		return nil
	}
}

// Dialect reports the shell language variant the runner implements, as set by
// [Lang]. The zero value is reported as [syntax.LangBash].
func (r *Runner) Dialect() syntax.LangVariant {
	if r.dialect == 0 {
		return syntax.LangBash
	}
	return r.dialect
}

// objectsEnabled reports whether object-valued variables are live on this
// runner. It requires the bash++ dialect, and it is off under `set -o posix`:
// POSIX mode is the shell promising to be nothing but a POSIX shell, so the
// extensions have to go, exactly as bash drops its own under --posix.
func (r *Runner) objectsEnabled() bool {
	if r.opts[optPosix] {
		return false
	}
	if r.ecfg != nil {
		return r.ecfg.ObjectsEnabled()
	}
	return r.Dialect() == syntax.LangBashPP
}

// SetObject binds name to an arbitrary Go value, as an object-valued variable.
//
// Objects are how a value crosses between Go and the shell without a lossy trip
// through a string. Inside the shell the variable still behaves like a string
// wherever a string is required — `$name` interpolates as JSON, and so does the
// value an external binary is handed (see [expand.Object]) — but a Go caller
// can retrieve the original value, undamaged, with [Runner.Object].
//
// It returns [ErrObjectsUnsupported] unless the runner is in the bash++ dialect
// and out of POSIX mode, and it rejects values which cannot be encoded as JSON,
// since such a value could not be coerced to a string later on, at a point where
// the shell has no way to report the failure.
func (r *Runner) SetObject(name string, val any) error {
	// The variable scopes only exist once the runner has been reset, and a
	// caller will typically seed objects before the first Run. Reset lazily,
	// as Run itself does, so that seeding does not wipe them afterwards.
	if !r.didReset {
		r.Reset()
	}
	if !r.objectsEnabled() {
		return ErrObjectsUnsupported
	}
	if !syntax.ValidName(name) {
		return fmt.Errorf("invalid variable name: %q", name)
	}
	if err := expand.ValidObject(val); err != nil {
		return err
	}
	r.setVar(name, expand.NewObject(val))
	return nil
}

// Object retrieves the Go value bound to name by [Runner.SetObject]. The second
// result is false if name is unset or is not an object.
func (r *Runner) Object(name string) (any, bool) {
	if !r.didReset {
		r.Reset()
	}
	vr := r.lookupVar(name)
	if vr.Kind != expand.Object {
		return nil, false
	}
	return vr.Obj, true
}
