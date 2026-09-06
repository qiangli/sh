// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/iotest"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func TestBashPPInterfaceAssignmentAssertionsAndTypeSwitch(t *testing.T) {
	const src = `type Count int
func (v Count) Show(prefix string) { echo "$prefix:$v"; }
type Shower interface { Show string }
func main() {
	var v Count = 7
	var i Shower = v
	x, ok := i.(Count)
	echo "assert:$x:$ok"
	y := i.(Count)
	y.Show(method)
	var nilI Shower
	switch n := nilI.(type) {
	case nil:
		echo nil-interface
	default:
		echo "$n"
	}
	var p *Count = nil
	var pi Shower = p
	switch q := pi.(type) {
	case nil:
		echo wrong
	default:
		echo typed-nil-pointer:${q-unset}
	}
	switch z := i.(type) {
	case nil:
		echo wrong
	case Count:
		echo "type:$z"
	default:
		echo wrong
	}
	snap(i)
}
func snap(i Shower) {
	a, ok := i.(Count)
	echo "subshell:$a:$ok"
}
main()
`
	for _, bytewise := range []bool{false, true} {
		var rd interface{ Read([]byte) (int, error) } = strings.NewReader(src)
		if bytewise {
			rd = iotest.OneByteReader(strings.NewReader(src))
		}
		f, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(rd, "iface.bpp")
		if err != nil {
			t.Fatalf("bytewise=%v: parse: %v", bytewise, err)
		}
		var out strings.Builder
		r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
		err = r.Run(context.Background(), f)
		if err != nil {
			t.Fatalf("bytewise=%v: err=%v output=%q", bytewise, err, out.String())
		}
		want := "assert:7:true\nmethod:7\nnil-interface\ntyped-nil-pointer:\ntype:7\nsubshell:7:true\n"
		if out.String() != want {
			t.Fatalf("bytewise=%v: output = %q, want %q", bytewise, out.String(), want)
		}
	}
}

func TestBashPPInterfaceDiagnostics(t *testing.T) {
	tests := []struct{ name, src, want string }{
		{"duplicate", "type I interface { M string M string }\n", "BASHPP-EINTERFACE-DUPLICATE: interface I declares method M more than once\n"},
		{"missing", "type T int\nfunc (v T) N(s string) { }\ntype I interface { M string }\nfunc main() { var v T = 1; var i I = v }\nmain()\n", "BASHPP-EINTERFACE-MISSING: T does not implement interface (missing method M)\n"},
		{"wrong signature", "type T int\nfunc (v T) M(n int) { }\ntype I interface { M string }\nfunc main() { var v T = 1; var i I = v }\nmain()\n", "BASHPP-EINTERFACE-SIGNATURE: T method M has wrong signature\n"},
		{"assert fail", "type T int\nfunc (v T) M(s string) { }\ntype U int\nfunc (v U) M(s string) { }\ntype I interface { M string }\nfunc main() { var v T = 1; var i I = v; x := i.(U); echo $x }\nmain()\n", "BASHPP-EASSERT-FAIL: interface value has dynamic type T, not U\n\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(tc.src), "badiface.bpp")
			if err != nil {
				t.Fatal(err)
			}
			var out strings.Builder
			r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
			err = r.Run(context.Background(), f)
			var status interp.ExitStatus
			if tc.name == "assert fail" {
				if err != nil || out.String() != tc.want {
					t.Fatalf("err/output = %v/%q, want nil/%q", err, out.String(), tc.want)
				}
				return
			}
			if !errors.As(err, &status) || status != 2 || out.String() != tc.want {
				t.Fatalf("err/output = %v/%q, want status 2/%q", err, out.String(), tc.want)
			}
		})
	}
}
