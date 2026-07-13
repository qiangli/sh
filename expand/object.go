// Copyright (c) 2026, the bash++ authors
// See LICENSE for licensing information

package expand

import (
	"encoding/json"
	"fmt"

	"mvdan.cc/sh/v3/syntax"
)

// Objects are the bash++ dialect's one value kind which is not a string. The
// shell itself remains string-typed: an [Object] only stays an object while it
// is held by a variable, and the moment it is used as a word — interpolated,
// compared, or passed as an argument — it is coerced by [ObjectString].
//
// The coercion is JSON. That single choice is what makes objects safe to add to
// a POSIX shell: every consumer that predates them, from `echo` to an external
// binary, keeps seeing a plain string, and the only thing that changes is that
// the string is now structured. Serializing on the way out is what the OS
// boundary does; see [mvdan.cc/sh/v3/interp] and its exec environment.

// ObjectString renders val as the string the shell should see for it, which is
// its JSON encoding. Values which cannot be marshaled as JSON — channels,
// functions, cyclic structures — fall back to Go's default formatting rather
// than failing, so that the coercion is total and no shell expansion can error
// out on it. [ValidObject] rejects such values up front, so in practice the
// fallback is unreachable for objects the interpreter itself created.
//
// The encoding is deterministic: map keys are sorted by encoding/json, and
// struct fields keep their declaration order.
func ObjectString(val any) string {
	if val == nil {
		return "null"
	}
	b, err := json.Marshal(val)
	if err != nil {
		return fmt.Sprint(val)
	}
	return string(b)
}

// ValidObject reports whether val may be stored in an [Object] variable, which
// requires that it round-trips through JSON. Callers should use this to reject
// a bad value at the point of assignment, where an error can still be reported,
// rather than at expansion time where it cannot.
func ValidObject(val any) error {
	if val == nil {
		return nil // encodes as JSON null
	}
	if _, err := json.Marshal(val); err != nil {
		return fmt.Errorf("value cannot be used as an object: %w", err)
	}
	return nil
}

// NewObject builds an [Object] variable holding val.
func NewObject(val any) Variable {
	return Variable{Set: true, Kind: Object, Obj: val}
}

// ObjectsEnabled reports whether object-valued variables are available, which
// is true only under the bash++ dialect ([syntax.LangBashPP]). Every other
// variant, including the zero value [syntax.LangBash], returns false.
//
// This is the single source of truth for the gate. The interpreter consults it
// on the assignment path and refuses to create an [Object] when it is false, so
// that under any other dialect an object simply cannot come into existence and
// the rest of expansion never has to account for one.
func (cfg *Config) ObjectsEnabled() bool {
	return cfg.Lang == syntax.LangBashPP
}
