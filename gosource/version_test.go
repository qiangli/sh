package gosource

import (
	"strings"
	"testing"
)

func TestGoSourceLanguageVersion(t *testing.T) {
	source := "package p\nvar _ = 0b101\n"
	for _, tc := range []struct{ version, want string }{
		{"go1.12", "binary literal requires go1.13 or later"},
		{"go1.13", ""},
		{"", ""},
		{"invalid", "invalid Go version"},
		{"go1.999", "requires newer Go version"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			program, err := Parse(strings.NewReader(source), "original.go", Options{GoVersion: tc.version})
			if tc.want == "" {
				if err != nil || program == nil {
					t.Fatalf("version %q rejected: %v", tc.version, err)
				}
			} else if err == nil || program != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("version %q: program=%v err=%v want %q", tc.version, program, err, tc.want)
			}
		})
	}
}

func TestGoSourceLanguageVersionDoesNotLeakAcrossLoads(t *testing.T) {
	source := "package p\nvar _ = 0b101\n"
	for _, v := range []string{"go1.12", "go1.13", "go1.12", ""} {
		_, err := Parse(strings.NewReader(source), "original.go", Options{GoVersion: v})
		if (err != nil) != (v == "go1.12") {
			t.Fatalf("version leaked into %q: %v", v, err)
		}
	}
}
