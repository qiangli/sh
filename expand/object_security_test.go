// Copyright (c) 2026, the bash++ authors
// See LICENSE for licensing information

package expand_test

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"mvdan.cc/sh/v3/expand"
)

func TestObjectMapCycleAndOversizeCoercionFailClosed(t *testing.T) {
	t.Parallel()

	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	qt.Assert(t, qt.IsNotNil(expand.ValidObject(cyclic)))
	qt.Assert(t, qt.Equals(expand.ObjectString(cyclic), expand.ObjectString(make(chan int))))

	large := strings.Repeat("x", 65<<20)
	qt.Assert(t, qt.IsNotNil(expand.ValidObject(large)))
	qt.Assert(t, qt.Equals(expand.ObjectString(large), expand.ObjectString(make(chan int))))
}

func TestObjectLargePayloadWithinLimit(t *testing.T) {
	t.Parallel()

	large := strings.Repeat("x", 36<<20)
	qt.Assert(t, qt.IsNil(expand.ValidObject(large)))
	qt.Assert(t, qt.Equals(len(expand.ObjectString(large)), len(large)+2))
}

func TestObjectPreflightRejectsRepeatedAliasBeforeMarshal(t *testing.T) {
	t.Parallel()

	chunk := strings.Repeat("x", 1<<20)
	aliases := make([]string, 1024)
	for i := range aliases {
		aliases[i] = chunk
	}
	qt.Assert(t, qt.IsNotNil(expand.ValidObject(aliases)))
	qt.Assert(t, qt.Equals(expand.ObjectString(aliases), expand.ObjectString(make(chan int))))
}

func TestObjectPreflightAccountsForStringEscaping(t *testing.T) {
	t.Parallel()

	// encoding/json expands each '<' to six bytes as \\u003c, so this source
	// string is small enough in memory but too large in its encoded form.
	escapeHeavy := strings.Repeat("<", 12<<20)
	qt.Assert(t, qt.IsNotNil(expand.ValidObject(escapeHeavy)))
	qt.Assert(t, qt.Equals(expand.ObjectString(escapeHeavy), expand.ObjectString(make(chan int))))
}
