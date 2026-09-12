package gosource

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkerFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "sprint151", "checker", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

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

func TestGoSourceLeadingCheckerLanguageFlag(t *testing.T) {
	for _, name := range []string{
		"lang_typechecker.go.txt",
		"lang_errorcheck.go.txt",
	} {
		source := checkerFixture(t, name)
		_, err := Parse(strings.NewReader(source), name, Options{})
		if err == nil || !strings.Contains(err.Error(), "binary literal requires go1.13 or later") {
			t.Fatalf("%s: err = %v", name, err)
		}
	}

	// An API option is authoritative; a corpus recipe must not override it.
	source := checkerFixture(t, "lang_typechecker.go.txt")
	if _, err := Parse(strings.NewReader(source), "version.go", Options{GoVersion: "go1.13"}); err != nil {
		t.Fatalf("explicit GoVersion was overridden: %v", err)
	}
}

func TestGoSourceCheckerConfigurationDoesNotLeak(t *testing.T) {
	source := checkerFixture(t, "fake_import_c.go.txt")
	_, err := Parse(strings.NewReader(source), "fake.go", Options{})
	if err == nil || !strings.Contains(err.Error(), "undefined: missing") || strings.Contains(err.Error(), "could not import C") {
		t.Fatalf("FakeImportC load: %v", err)
	}
	_, err = Parse(strings.NewReader(strings.TrimPrefix(source, "// -fakeImportC\n")), "real.go", Options{})
	if err == nil || !strings.Contains(err.Error(), "could not import C") {
		t.Fatalf("FakeImportC leaked to later load: %v", err)
	}
}

func TestGoSourceCollectsAllCheckerDiagnostics(t *testing.T) {
	source := checkerFixture(t, "all_errors.go.txt")
	_, err := Parse(strings.NewReader(source), "all.go", Options{})
	if err == nil {
		t.Fatal("invalid program passed type checking")
	}
	for _, want := range []string{"cannot use \"a\"", "cannot use 1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("diagnostics %q do not contain %q", err, want)
		}
	}
}
