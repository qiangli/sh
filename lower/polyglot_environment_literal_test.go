package lower

import (
	"go/ast"
	"go/parser"
	"reflect"
	"strconv"
	"testing"

	"mvdan.cc/sh/v3/polyglot"
)

// TestEnvironmentLiteralCarriesEveryField pins that the lowered
// EnvironmentPlan literal reproduces every plan field. A dropped field is a
// launch plan the native binary silently loses: on an arm64 musl image the
// framework-dependent pwsh route resolves to Executable=dotnet with
// ExecutableArgs=[pwsh.dll], and without the argument the lowered binary ran
// bare dotnet ("load PowerShell module: EOF").
func TestEnvironmentLiteralCarriesEveryField(t *testing.T) {
	var plan polyglot.EnvironmentPlan
	v := reflect.ValueOf(&plan).Elem()
	want := map[string]string{}
	for i := 0; i < v.NumField(); i++ {
		f, name := v.Field(i), v.Type().Field(i).Name
		switch f.Kind() {
		case reflect.String:
			f.SetString("v-" + name)
			want[name] = strconv.Quote("v-" + name)
		case reflect.Slice:
			f.Set(reflect.ValueOf([]string{"a-" + name, "b-" + name}))
			want[name] = `[]string{"a-` + name + `", "b-` + name + `"}`
		default:
			t.Fatalf("EnvironmentPlan.%s: unhandled kind %s", name, f.Kind())
		}
	}
	plan.Executable = "/opt/bashy/bin/dotnet-runtime-musl/10.0.12/dotnet"
	want["Executable"] = strconv.Quote(plan.Executable)
	plan.ExecutableArgs = []string{"/opt/bashy/lib/pwsh/pwsh.dll"}
	want["ExecutableArgs"] = `[]string{"/opt/bashy/lib/pwsh/pwsh.dll"}`

	e := &emitter{prefix: "p_"}
	lit := e.environmentLiteral(&plan)
	expr, err := parser.ParseExpr(lit)
	if err != nil {
		t.Fatalf("literal does not parse: %v\n%s", err, lit)
	}
	comp := expr.(*ast.UnaryExpr).X.(*ast.CompositeLit)
	got := map[string]string{}
	for _, elt := range comp.Elts {
		kv := elt.(*ast.KeyValueExpr)
		got[kv.Key.(*ast.Ident).Name] = lit[kv.Value.Pos()-1 : kv.Value.End()-1]
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("EnvironmentPlan.%s: lowered %q, want %q", name, got[name], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("lowered %d fields, plan has %d", len(got), len(want))
	}
}
