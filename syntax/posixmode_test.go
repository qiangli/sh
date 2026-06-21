// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package syntax

import (
	"strings"
	"testing"
)

// PosixMode must apply POSIX behavioral parse rules (single quotes literal
// inside ${...}) ON TOP OF LangBash, WITHOUT dropping bash grammar extensions
// like ${a:off:len} substrings — mirroring `bash --posix`.
func TestPosixModeKeepsBashExtensions(t *testing.T) {
	t.Parallel()
	// A bash extension that LangPOSIX rejects: substring expansion.
	const src = `a=hello; echo "${a:1:3}"`
	mustParse := func(opts ...ParserOption) {
		t.Helper()
		if _, err := NewParser(opts...).Parse(strings.NewReader(src), ""); err != nil {
			t.Fatalf("parse failed for %q: %v", src, err)
		}
	}
	mustReject := func(opts ...ParserOption) {
		t.Helper()
		if _, err := NewParser(opts...).Parse(strings.NewReader(src), ""); err == nil {
			t.Fatalf("expected parse error for %q under strict POSIX", src)
		}
	}
	mustParse(Variant(LangBash))                  // bash: ok
	mustParse(Variant(LangBash), PosixMode(true)) // bash --posix: STILL ok (the fix)
	mustReject(Variant(LangPOSIX))                // strict posix grammar: rejected
}

// bash --posix keeps the bash extension allowing non-POSIX function names
// (hyphens, dots): `test-hyphen() {}` runs under `bash --posix`. Only the pure
// LangPOSIX grammar restricts function names to POSIX "names".
func TestPosixModeAllowsNonPosixFuncNames(t *testing.T) {
	t.Parallel()
	const src = `test-hyphen() { :; }`
	parse := func(opts ...ParserOption) error {
		_, err := NewParser(opts...).Parse(strings.NewReader(src), "")
		return err
	}
	if err := parse(Variant(LangBash)); err != nil {
		t.Fatalf("bash: %v", err)
	}
	if err := parse(Variant(LangBash), PosixMode(true)); err != nil {
		t.Fatalf("bash --posix should accept hyphen func names: %v", err)
	}
	if err := parse(Variant(LangPOSIX)); err == nil {
		t.Fatal("LangPOSIX should reject a hyphen func name")
	}
}
