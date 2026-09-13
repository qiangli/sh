// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"go/constant"
	"go/token"
	"strings"
	"testing"
)

// The wide-integer fallback is confined to GoSource; Classic retains its
// signed scalar carrier and its established rejection of this conversion.
func TestSprint165WideIntegerClassicParityNegative(t *testing.T) {
	wide := constant.MakeFromLiteral("9223372036854775808", token.INT, 0)
	_, err := (&Runner{}).bashPPConvertScalar("string", bashPPScalar{value: wide})
	if err == nil || !strings.Contains(err.Error(), "cannot convert 9223372036854775808 to string") {
		t.Fatalf("Classic conversion changed: %v", err)
	}
}
