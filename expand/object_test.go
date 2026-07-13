// Copyright (c) 2026, the bash++ authors
// See LICENSE for licensing information

package expand_test

import (
	"testing"

	"github.com/go-quicktest/qt"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

func TestObjectsEnabled(t *testing.T) {
	t.Parallel()

	// The gate is the dialect, and only bash++ opens it. In particular the
	// zero value — which is what every existing caller has — is closed.
	var zero expand.Config
	qt.Assert(t, qt.IsFalse(zero.ObjectsEnabled()))

	for _, lang := range []syntax.LangVariant{
		syntax.LangBash, syntax.LangPOSIX, syntax.LangMirBSDKorn,
		syntax.LangBats, syntax.LangZsh,
	} {
		cfg := expand.Config{Lang: lang}
		qt.Check(t, qt.IsFalse(cfg.ObjectsEnabled()), qt.Commentf("lang %v", lang))
	}

	cfg := expand.Config{Lang: syntax.LangBashPP}
	qt.Assert(t, qt.IsTrue(cfg.ObjectsEnabled()))
}

func TestObjectString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		val  any
		want string
	}{
		{"nil", nil, "null"},
		{"string", "hi", `"hi"`},
		{"int", 3, "3"},
		{"bool", true, "true"},
		{"slice", []int{1, 2}, "[1,2]"},
		{"map sorted", map[string]any{"b": 2, "a": 1}, `{"a":1,"b":2}`},
		{"nested", map[string]any{"x": []any{1, "two"}}, `{"x":[1,"two"]}`},
		{"struct", struct {
			Name string `json:"name"`
			N    int    `json:"n"`
		}{"gopher", 7}, `{"name":"gopher","n":7}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			qt.Assert(t, qt.Equals(expand.ObjectString(tc.val), tc.want))
			// A variable holding it coerces to the same string, which is
			// what the shell sees wherever a string is required.
			vr := expand.NewObject(tc.val)
			qt.Assert(t, qt.Equals(vr.Kind, expand.Object))
			qt.Assert(t, qt.IsTrue(vr.IsSet()))
			qt.Assert(t, qt.Equals(vr.String(), tc.want))
		})
	}
}

// TestObjectRoundTrip is the round-trip gate at the expand layer: a Go value
// put into an Object variable comes back out as the same Go value, undamaged,
// while still coercing to a string for anything that wants one.
func TestObjectRoundTrip(t *testing.T) {
	t.Parallel()

	type point struct {
		X, Y int
	}
	orig := point{1, 2}

	vr := expand.NewObject(orig)
	got, ok := vr.Obj.(point)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.Equals(got, orig))
	qt.Assert(t, qt.Equals(vr.String(), `{"X":1,"Y":2}`))

	// Through a WriteEnviron, as the interpreter stores it: the Go value is
	// carried, not flattened to its string form on the way in.
	var we mapEnviron = map[string]expand.Variable{}
	qt.Assert(t, qt.IsNil(we.Set("P", vr)))
	back := we.Get("P")
	qt.Assert(t, qt.Equals(back.Kind, expand.Object))
	roundTripped, ok := back.Obj.(point)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.Equals(roundTripped, orig))
	qt.Assert(t, qt.Equals(back.String(), `{"X":1,"Y":2}`))
}

// mapEnviron is a minimal [expand.WriteEnviron] for the round-trip test.
type mapEnviron map[string]expand.Variable

func (m mapEnviron) Get(name string) expand.Variable { return m[name] }

func (m mapEnviron) Each(fn func(string, expand.Variable) bool) {
	for name, vr := range m {
		if !fn(name, vr) {
			return
		}
	}
}

func (m mapEnviron) Set(name string, vr expand.Variable) error {
	if !vr.IsSet() {
		delete(m, name)
		return nil
	}
	m[name] = vr
	return nil
}

func TestValidObject(t *testing.T) {
	t.Parallel()

	qt.Assert(t, qt.IsNil(expand.ValidObject(nil)))
	qt.Assert(t, qt.IsNil(expand.ValidObject(map[string]any{"a": 1})))

	// Values which cannot become JSON are rejected up front, so that the
	// string coercion later on can never fail at a point where the shell has
	// no way to report it.
	qt.Assert(t, qt.IsNotNil(expand.ValidObject(make(chan int))))
	qt.Assert(t, qt.IsNotNil(expand.ValidObject(func() {})))
}
