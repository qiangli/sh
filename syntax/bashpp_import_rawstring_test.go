// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

import (
	"strings"
	"testing"
)

// Raw-string imports: an explicit compatibility decision, NOT an accident.
//
// Go spells an import path with either an interpreted string ("fmt") or a raw
// string (`fmt`). Bash++ claims ONLY the interpreted form, and this file is
// the decision rather than a description of whatever the parser happens to do.
//
// The reason is that a backquote is already shell command substitution. In
// stock bash, `import ` + "`fmt`" runs the command `fmt` and passes its output
// to `import`, so the shape is Class E with a REAL and common meaning. Claiming
// it would silently change what such a line does, which is the one thing the
// superset rule forbids. Single-quoted and $'...' forms fall to shell for the
// same reason: they are ordinary shell quoting, not Go syntax.
//
// The cost of the decision is that `import ` + "`fmt`" is not a Bash++ import.
// That is accepted: a user who wants one writes the interpreted form, which is
// also what gofmt produces.
func TestBashPPImportClaimsOnlyInterpretedStrings(t *testing.T) {
	for _, test := range []struct {
		src    string
		claim  bool
		reason string
	}{
		{"import \"fmt\"\n", true, "interpreted string is the claimed Go form"},
		{"import `fmt`\n", false, "backquote is command substitution in shell"},
		{"import 'fmt'\n", false, "single quotes are shell quoting"},
		{"import $'fmt'\n", false, "ANSI-C quoting is shell quoting"},
		{"import \"fm\"\"t\"\n", false, "adjacent concatenation is not a Go import path"},
		{"import fmt\n", false, "a bare word is the documented near miss"},
		{"import neturl `net/url`\n", false, "an aliased raw string is not claimed either"},
	} {
		t.Run(test.reason, func(t *testing.T) {
			f, err := NewParser(Variant(LangBashPP)).Parse(strings.NewReader(test.src), "t")
			if err != nil {
				t.Fatalf("parse %q: %v", test.src, err)
			}
			_, claimed := f.Stmts[0].Cmd.(*BashPPImport)
			if claimed != test.claim {
				t.Fatalf("%q claimed=%v, want %v (%s)", test.src, claimed, test.claim, test.reason)
			}
		})
	}
}

// A raw string inside a grouped import must not merely be unclaimed: the
// resulting error has to be byte-identical to Classic Bash's, or Bash++ would
// have changed what a broken script reports.
func TestBashPPGroupedRawStringMatchesClassicBash(t *testing.T) {
	for _, src := range []string{
		"import (\n\t`fmt`\n)\n",
		"import (\n\t'fmt'\n)\n",
	} {
		bashErr := parseErrText(t, LangBash, src)
		bashPPErr := parseErrText(t, LangBashPP, src)
		if bashErr == "" {
			t.Fatalf("%q: expected Classic Bash to reject the shape", src)
		}
		if bashErr != bashPPErr {
			t.Fatalf("%q: bash %q != bash++ %q", src, bashErr, bashPPErr)
		}
	}
	// The interpreted form is the one Bash++ claims, and Classic still rejects it.
	const claimed = "import (\n\t\"fmt\"\n)\n"
	if got := parseErrText(t, LangBash, claimed); got == "" {
		t.Fatal("Classic Bash unexpectedly accepted a grouped import")
	}
	if got := parseErrText(t, LangBashPP, claimed); got != "" {
		t.Fatalf("Bash++ rejected its own grouped import: %v", got)
	}
}

func parseErrText(t *testing.T, lang LangVariant, src string) string {
	t.Helper()
	_, err := NewParser(Variant(lang)).Parse(strings.NewReader(src), "t")
	if err == nil {
		return ""
	}
	return err.Error()
}
