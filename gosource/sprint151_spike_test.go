package gosource_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// TestSprint151Spike pins the converter's CURRENT verdict on one small
// out-of-corpus Go program per rejection mechanism catalogued in
// testdata/sprint151/FINDINGS-M2.md. Every program is valid Go (go vet clean,
// deterministic output), so it doubles as a regression test once a mechanism
// is implemented: the row's want must then flip to "" and the program should
// graduate into a run-and-compare table such as TestUnchangedGo.
func TestSprint151Spike(t *testing.T) {
	for _, tc := range []struct {
		mechanism, file, want string
	}{
		{"labeled-branch/for-break", "labeled_break_for.go", "labeled_break_for.go:8:1: gosource: unsupported LabeledStmt"},
		{"labeled-branch/for-continue", "labeled_continue_for.go", "labeled_continue_for.go:8:1: gosource: unsupported LabeledStmt"},
		{"labeled-branch/switch-break", "labeled_break_switch.go", "labeled_break_switch.go:9:1: gosource: unsupported LabeledStmt"},
		{"labeled-branch/select-break", "labeled_break_select.go", "labeled_break_select.go:13:1: gosource: unsupported LabeledStmt"},
		{"goto/backward", "goto_backward.go", "goto_backward.go:8:1: gosource: unsupported LabeledStmt"},
		{"goto/forward", "goto_forward.go", "goto_forward.go:10:3: gosource: unsupported labeled branch"},
		{"expression-statement/paren-call", "exprstmt_paren_call.go", ""},
		{"expression-statement/bare-type-switch", "exprstmt_bare_typeswitch.go", ""},
		{"expression-kind/IndexListExpr", "expr_indexlist_funcvalue.go", "expr_indexlist_funcvalue.go:10:7: gosource: unsupported expression *ast.IndexListExpr"},
		{"expression-kind/type-switch-composite-case", "expr_typeswitch_composite_case.go", "expr_typeswitch_composite_case.go:11:7: gosource: unsupported expression *ast.ArrayType"},
		{"type-kind/indexed-call", "type_indexed_call.go", ""},
		{"type-kind/indexed-call-ident", "type_indexed_call_ident.go", ""},
		{"range-target/index", "range_target_index.go", "range_target_index.go:8:6: gosource: unsupported range assignment target"},
		{"range-target/field", "range_target_field.go", "range_target_field.go:10:9: gosource: unsupported range assignment target"},
		{"range-target/deref", "range_target_deref.go", "range_target_deref.go:9:6: gosource: unsupported range assignment target"},
		{"compound-simple-statement/for-init", "forinit_compound.go", "forinit_compound.go:11:6: gosource: unsupported compound simple statement"},
		{"function-value-type/generic-selector", "funcvalue_generic_selector.go", "funcvalue_generic_selector.go:12:7: gosource: unsupported function value type"},
		{"function-value-type/named-func-type", "funcvalue_named_type.go", ""},
	} {
		t.Run(tc.mechanism, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "sprint151", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			_, err = gosource.Load([]gosource.Source{{Name: tc.file, Data: data}}, gosource.Options{RunMain: true})
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != tc.want {
				t.Fatalf("%s: converter verdict changed\n got: %q\nwant: %q\nupdate FINDINGS-M2.md and, if the mechanism is now implemented, promote the program to a run-and-compare test", tc.file, got, tc.want)
			}
		})
	}
}

func TestSprint151Implemented(t *testing.T) {
	for _, file := range []string{
		"exprstmt_bare_typeswitch.go",
		"exprstmt_paren_call.go",
		"type_indexed_call.go",
		"type_indexed_call_ident.go",
	} {
		t.Run(file, func(t *testing.T) {
			path := filepath.Join("testdata", "sprint151", file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			goOut, err := exec.Command("go", "run", path).Output()
			if err != nil {
				t.Fatalf("go run: %v", err)
			}
			program, err := gosource.Load([]gosource.Source{{Name: file, Data: data}}, gosource.Options{RunMain: true})
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.StdIO(nil, &stdout, &stderr))
			if err != nil {
				t.Fatal(err)
			}
			if err := runner.Run(context.Background(), program.File); err != nil || stderr.Len() > 0 {
				t.Fatalf("interpreter: err=%v stderr=%q", err, stderr.String())
			}
			if !bytes.Equal(stdout.Bytes(), goOut) {
				t.Fatalf("stdout differs from go run\ninterpreter: %q\ngo run:      %q", stdout.Bytes(), goOut)
			}
		})
	}
}
