// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"go/constant"
	"go/token"
	"os"
	"path/filepath"
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

func TestSprint165StringToUint64NegativeSet(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "sprint165", "string-to-uint64", "negative.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			t.Fatalf("bad negative row %q", line)
		}
		name, sourceType, text, want := fields[0], fields[1], fields[2], fields[3]
		t.Run(name, func(t *testing.T) {
			got, handled, err := (&Runner{bashPPGoSource: true}).bashPPConvertGoSourceStringToUint64("uint64", bashPPScalar{
				value:   constant.MakeString(text),
				typ:     sourceType,
				runtime: true,
			})
			if want == "unhandled" {
				if handled || err != nil || got.value != nil {
					t.Fatalf("handled=%v value=%v err=%v, want unhandled", handled, got.value, err)
				}
				return
			}
			if !handled || err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("handled=%v err=%v, want %q", handled, err, want)
			}
		})
	}
}
