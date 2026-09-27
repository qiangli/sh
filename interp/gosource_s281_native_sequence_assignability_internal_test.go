//go:build full

package interp

// Sprint: #281; Story: #809; Story-ID: fac7e14af4a8

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/syntax"
)

// TestS281NativeSequenceMaterializationChecksSourceType keeps the sequence
// materializer behind the dependency helper's authenticated assignability
// check. Element-by-element conversion cannot reject an incompatible empty
// slice, and cannot distinguish arrays whose element types match but lengths
// differ.
func TestS281NativeSequenceMaterializationChecksSourceType(t *testing.T) {
	var r *Runner
	var checks []error
	probed := false
	writer := callbackProbeWriter(func(p []byte) (int, error) {
		if !strings.Contains(string(p), "probe") {
			return len(p), nil
		}
		probed = true
		stringType := &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "string"}}
		uint64Type := &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "uint64"}}
		byteType := &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "byte"}}
		pairOfStrings := &syntax.BashPPCollectionType{Kind: "array", Length: &syntax.Lit{Value: "2"}, Element: stringType}
		pairOfUint64 := &syntax.BashPPCollectionType{Kind: "array", Length: &syntax.Lit{Value: "2"}, Element: uint64Type}
		tests := []struct {
			name     string
			cell     string
			expected syntax.BashPPTypeExpr
			wantErr  bool
		}{
			{"non-empty compatible", "ranges", &syntax.BashPPCollectionType{Kind: "slice", Element: pairOfUint64}, false},
			{"empty compatible", "empty", &syntax.BashPPCollectionType{Kind: "slice", Element: pairOfUint64}, false},
			{"array compatible", "array", &syntax.BashPPCollectionType{Kind: "array", Length: &syntax.Lit{Value: "32"}, Element: byteType}, false},
			{"non-empty element mismatch", "ranges", &syntax.BashPPCollectionType{Kind: "slice", Element: pairOfStrings}, true},
			{"empty element mismatch", "empty", &syntax.BashPPCollectionType{Kind: "slice", Element: pairOfStrings}, true},
			{"array length mismatch", "array", &syntax.BashPPCollectionType{Kind: "array", Length: &syntax.Lit{Value: "31"}, Element: byteType}, true},
		}
		for _, test := range tests {
			native := r.bashPPNativeCellValue(test.cell)
			if native == nil || native.Kind != "handle" {
				checks = append(checks, fmt.Errorf("%s: missing native handle: %#v", test.name, native))
				continue
			}
			_, _, handled, err := r.goSourceNativeSequenceContents(native, test.expected)
			if !handled || test.wantErr != (err != nil) || (test.wantErr && !strings.Contains(err.Error(), "cannot use native")) {
				checks = append(checks, fmt.Errorf("%s: handled=%v err=%v", test.name, handled, err))
			}
		}
		return len(p), nil
	})
	var err error
	r, err = New(Lang(syntax.LangBashPP), Dir(t.TempDir()), StdIO(nil, io.Discard, writer))
	if err != nil {
		t.Fatal(err)
	}
	source := `package main
import (
	"debug/dwarf"
	"fmt"
	"io"
	"reflect"
)
func main() {
	empty, emptyErr := new(dwarf.Data).Ranges(&dwarf.Entry{Field: nil})
	ranges, rangesErr := new(dwarf.Data).Ranges(&dwarf.Entry{Field: []dwarf.Field{
		{Attr: dwarf.AttrLowpc, Val: uint64(10)},
		{Attr: dwarf.AttrHighpc, Class: dwarf.ClassAddress, Val: uint64(20)},
	}})
	array := reflect.New(reflect.ArrayOf(32, reflect.TypeOf(byte(0)))).Elem().Interface().([32]byte)
	println("probe")
	fmt.Fprintf(io.Discard, "%v %v %v %v %v", empty, emptyErr, ranges, rangesErr, array)
}`
	p, err := gosource.Parse(strings.NewReader(source), filepath.Join(r.Dir, "original.go"), gosource.Options{RunMain: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Run(context.Background(), p.File); err != nil {
		t.Fatal(err)
	}
	if !probed {
		t.Fatal("native sequence probe did not run")
	}
	for _, err := range checks {
		t.Error(err)
	}
}
