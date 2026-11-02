package interp

import (
	"go/constant"
	"sync"
	"testing"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Taking an address is not a guest read of the pointee. The interpreter's
// inferred-type lookup must not race with a task writing through that address.
func TestBashPPAddressGuardsEscapingPointee(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatal(err)
	}
	cell := &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: "0"}, scalarKind: constant.Int}
	r.bashPPScope = newBashPPScope(nil)
	r.bashPPScope.entries["n"] = cell
	expr := &syntax.BashPPIdent{Name: &syntax.Lit{Value: "n"}}
	ptr, err := r.bashPPAddress(expr)
	if err != nil {
		t.Fatal(err)
	}
	if ptr.target != cell || cell.guard == nil {
		t.Fatal("address did not preserve and guard original storage before escape")
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		writer := &Runner{}
		for i := 0; i < 1000; i++ {
			if err := writer.bashPPWriteUpdatePointer(ptr, i, constant.Int); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	defer wg.Wait()
	for i := 0; i < 1000; i++ {
		again, err := r.bashPPAddress(expr)
		if err != nil {
			t.Fatal(err)
		}
		if again.target != cell {
			t.Fatal("address copied pointee storage")
		}
		typ, ok := again.elem.(*syntax.BashPPNamedType)
		if !ok || typ.Name.Value != "int" {
			t.Fatalf("address lost inferred int type: %#v", again.elem)
		}
	}
}
