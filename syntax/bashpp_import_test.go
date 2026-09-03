package syntax

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestBashPPImportExactShapes(t *testing.T) {
	for _, src := range []string{
		`import "fmt"`, `import f "fmt"`, `import "encoding/json"`,
		`import _ "fmt"`, `import . "fmt"`,
		"import (\n\t\"fmt\"\n\tf \"log\"\n\t_ \"embed\"\n)",
		"import\n(\n\t\"fmt\"\n)", `import ()`, "import (\n\t\"fmt\";\n)",
		`import "net/http"`, `import "crypto/x509"`, `import "testing/fstest"`,
		`import "syscall/js"`, `import "unsafe"`, `import f "\x66mt"`,
	} {
		for _, chunk := range []int{0, 1} {
			var rd interface{ Read([]byte) (int, error) } = strings.NewReader(src)
			if chunk == 1 {
				rd = &oneByteReader{r: strings.NewReader(src)}
			}
			f, err := NewParser(Variant(LangBashPP)).Parse(rd, "")
			if err != nil {
				t.Fatalf("%q chunk=%d: %v", src, chunk, err)
			}
			if _, ok := f.Stmts[0].Cmd.(*BashPPImport); !ok {
				t.Fatalf("%q: got %T", src, f.Stmts[0].Cmd)
			}
		}
	}
}

type oneByteReader struct{ r *strings.Reader }

func (r *oneByteReader) Read(p []byte) (int, error) { return r.r.Read(p[:1]) }

func TestBashPPImportFallbackExact(t *testing.T) {
	shapes := []string{
		`import`, `"import" "fmt"`, `import fmt`, `import 'fmt'`, `import "$pkg"`,
		`import if "fmt"`, `import f "fmt" extra`,
		`X=1 import "fmt"`, `import "fmt" >out`, `import "bad path"`,
		`import ("fmt")`, "import \"fmt\\nlog\"", `import "fmt\\"`,
		`import "./fmt"`, `import "../fmt"`, `import "/tmp/x"`, `import "local/pkg"`,
		`import "fmt/"`, `import "fmt//x"`, `import "C:fmt"`,
		`import "cmd/go"`, `import "internal/abi"`, `import "net/http/internal"`,
		`import "vendor/golang.org/x/net/http2"`, `import "net/http/http_test"`,
		"import (\n\t\"fmt\"\n\t\"local/pkg\"\n)", "import (\n\t\"fmt\" extra\n)",
		`import (; "fmt")`, "import (\n\t\"fmt\";;\n)", "import (\n\t\"fmt\" \"log\"\n)",
	}
	for _, src := range shapes {
		assertImportFallbackExact(t, src, false)
		assertImportFallbackExact(t, src, true)
	}
}

func assertImportFallbackExact(t *testing.T, src string, oneByte bool) {
	t.Helper()
	parse := func(lang LangVariant) (*File, error) {
		var rd interface{ Read([]byte) (int, error) } = strings.NewReader(src)
		if oneByte {
			rd = &oneByteReader{r: strings.NewReader(src)}
		}
		return NewParser(Variant(lang)).Parse(rd, "fallback.sh")
	}
	bash, bashErr := parse(LangBash)
	pp, ppErr := parse(LangBashPP)
	if !reflect.DeepEqual(bash, pp) || fmt.Sprint(bashErr) != fmt.Sprint(ppErr) || reflect.TypeOf(bashErr) != reflect.TypeOf(ppErr) {
		t.Fatalf("fallback differs for %q oneByte=%v: bash=%#v err=%T %v; pp=%#v err=%T %v", src, oneByte, bash, bashErr, bashErr, pp, ppErr, ppErr)
	}
}

func TestGo127StdlibAllowlistProvenanceAndNearMisses(t *testing.T) {
	if go127StdlibSourceSHA256 != "636d109763f0fe3e45347b74e07a0e00a1ca6a90b6130564b09ed8a04804d942" {
		t.Fatalf("unreviewed Go 1.27 source inventory: %s", go127StdlibSourceSHA256)
	}
	if !slices.IsSorted(go127StdlibImports[:]) {
		t.Fatal("Go 1.27 standard-library allowlist is not sorted")
	}
	for i, path := range go127StdlibImports {
		if i > 0 && path == go127StdlibImports[i-1] {
			t.Fatalf("duplicate allowlist path %q", path)
		}
	}
	joined := strings.Join(go127StdlibImports[:], "\n") + "\n"
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(joined))); got != "de444f71390a90f274b5176d8da92480ab72e2992d8885823712cd927e289f6c" {
		t.Fatalf("unreviewed Go 1.27 import allowlist checksum: %s", got)
	}

	// Mutate every reviewed name across every excluded namespace class. This
	// makes additions and adjacent spellings fail closed, including under the
	// parser's most adversarial one-byte input schedule.
	for _, path := range go127StdlibImports {
		for _, near := range []string{
			"/" + path, "./" + path, "../" + path,
			"cmd/" + path, "internal/" + path, "vendor/" + path,
			path + "/internal", path + "/vendor", path + "_test", path + ".module",
		} {
			if isGo127StdlibImport(near) {
				t.Fatalf("near miss %q unexpectedly allowed (from %q)", near, path)
			}
			assertImportFallbackExact(t, `import "`+near+`"`, true)
		}
	}
}

func TestBashPPImportClassicAndPOSIXNeverClaim(t *testing.T) {
	for _, lang := range []LangVariant{LangBash, LangPOSIX} {
		for _, src := range []string{`import "fmt"`, `import f "fmt"`, "import (\n\t\"fmt\"\n)", "import\n(\n\t\"fmt\"\n)"} {
			want, wantErr := NewParser(Variant(lang)).Parse(strings.NewReader(src), "")
			got, gotErr := NewParser(Variant(lang)).Parse(&oneByteReader{r: strings.NewReader(src)}, "")
			if !reflect.DeepEqual(got, want) || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) ||
				reflect.TypeOf(gotErr) != reflect.TypeOf(wantErr) {
				t.Fatalf("%v one-byte parse differs for %q: normal=%#v err=%T %v; one-byte=%#v err=%T %v",
					lang, src, want, wantErr, wantErr, got, gotErr, gotErr)
			}
			for _, f := range []*File{want, got} {
				if len(f.Stmts) > 0 {
					if _, ok := f.Stmts[0].Cmd.(*BashPPImport); ok {
						t.Fatalf("%v claimed %q", lang, src)
					}
				}
			}
		}
	}
}

func TestBashPPGroupedImportCanonicalPrint(t *testing.T) {
	for _, test := range []struct {
		src, want string
	}{
		{"import\n(\n\t\"fmt\"\n)\n", "import (\n\t\"fmt\"\n)\n"},
		{"import ()\n", "import ()\n"},
		{"import (\n\t\"fmt\";\n)\n", "import (\n\t\"fmt\"\n)\n"},
	} {
		f, err := NewParser(Variant(LangBashPP)).Parse(strings.NewReader(test.src), "")
		if err != nil {
			t.Fatalf("parse %q: %v", test.src, err)
		}
		var out bytes.Buffer
		if err := NewPrinter().Print(&out, f); err != nil {
			t.Fatalf("print %q: %v", test.src, err)
		}
		if got := out.String(); got != test.want {
			t.Fatalf("print %q: got %q, want %q", test.src, got, test.want)
		}
	}
}

func TestBashPPImportPrintWalk(t *testing.T) {
	src := "import\n# between\n(\n# first\n\"fmt\" # inline\n# second\nf \"log\"\n# last\n)\n"
	f, err := NewParser(Variant(LangBashPP), KeepComments(true)).Parse(strings.NewReader(src), "")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	want := "import\n# between\n(\n\t# first\n\t\"fmt\" # inline\n\t# second\n\tf \"log\"\n\t# last\n)\n"
	if err := NewPrinter().Print(&out, f); err != nil || out.String() != want {
		t.Fatalf("%q %v", out.String(), err)
	}
	imp := f.Stmts[0].Cmd.(*BashPPImport)
	if len(imp.Comments) != 1 || len(imp.Specs) != 2 || len(imp.Specs[0].Comments) != 1 ||
		len(imp.Specs[1].Comments) != 2 || len(imp.Last) != 1 {
		t.Fatalf("comments were not retained in typed nodes: %#v", imp)
	}
	seen, specs, comments := false, 0, 0
	Walk(f, func(n Node) bool {
		if _, ok := n.(*BashPPImport); ok {
			seen = true
		}
		if _, ok := n.(*BashPPImportSpec); ok {
			specs++
		}
		if _, ok := n.(*Comment); ok {
			comments++
		}
		return true
	})
	if !seen || specs != 2 || comments != 5 {
		t.Fatal("walk missed BashPPImport")
	}
}
