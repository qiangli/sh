//go:build full

package interp_test

// Sprint: #281; Story: #810; Story-ID: 48c1146a3ab0
//
// TestIntrinsics in cmd/compile/internal/ssagen ranges sys.Archs (a
// dependency-owned []*sys.Arch), passes each element to lookup, and embeds the
// pointer in an intrinsicKey map key. The test itself runs through the hosted
// testing callback, so this reproduction keeps every value boundary involved
// in that failure while using public imported pointers.

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func TestS281NativePointerRangeArgumentMapKey(t *testing.T) {
	const source = `package pointerkey

import (
	"testing"
	"unicode"
)

type key struct {
	arch *unicode.RangeTable
	name string
}

type builders map[key]int

var intrinsics builders

func (b builders) add(arch *unicode.RangeTable, value int) {
	b[key{arch, "same"}] = value
}

func (b builders) addForTables(value int, tables ...*unicode.RangeTable) {
	for _, table := range tables {
		b.add(table, value)
	}
}

func (b builders) lookup(arch *unicode.RangeTable) int {
	return b[key{arch, "same"}]
}

func initIntrinsics() {
	intrinsics = builders{}
	all := unicode.GraphicRanges[:]
	add := func(value int, tables ...*unicode.RangeTable) {
		intrinsics.addForTables(value, tables...)
	}
	add(11, all[0])
	add(22, all[1])
	add(33, all[0])
	var nilTable *unicode.RangeTable
	add(44, nilTable)
}

func TestPointerKeys(t *testing.T) {
	initIntrinsics()
	var first, second *unicode.RangeTable
	for i, arch := range unicode.GraphicRanges {
		if i == 0 {
			first = arch
		} else {
			second = arch
			break
		}
	}
	if first == nil || second == nil || first == second {
		t.Fatal("fixture needs two distinct live pointers")
	}
	if len(intrinsics) != 3 || intrinsics.lookup(first) != 33 || intrinsics.lookup(second) != 22 {
		t.Fatalf("identity/alias: len=%d first=%d second=%d", len(intrinsics), intrinsics.lookup(first), intrinsics.lookup(second))
	}

	var nilArch *unicode.RangeTable
	if len(intrinsics) != 3 || intrinsics.lookup(nilArch) != 44 || intrinsics.lookup(first) != 33 {
		t.Fatalf("nil/live: len=%d nil=%d first=%d", len(intrinsics), intrinsics.lookup(nilArch), intrinsics.lookup(first))
	}
}
`
	program, err := gosource.Load([]gosource.Source{{Name: "pointerkey_test.go", Data: []byte(source)}}, gosource.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.Dir(t.TempDir()), interp.StdIO(nil, &stdout, &stderr))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	session, err := runner.LoadGoSourceTests(ctx, program)
	if err != nil {
		t.Fatalf("load: %v; stderr=%s", err, stderr.String())
	}
	defer session.Close()
	if err := session.Run(ctx, "TestPointerKeys", t); err != nil {
		t.Fatalf("run: %v; stderr=%s", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "BASHPP-E") {
		t.Fatalf("interpreter diagnostic: %s", stderr.String())
	}
}
