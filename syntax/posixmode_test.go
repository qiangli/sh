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
