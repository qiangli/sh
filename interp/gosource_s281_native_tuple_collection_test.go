//go:build full

package interp_test

// Sprint: #281; Story: #809; Story-ID: fac7e14af4a8

import "testing"

// TestS281NativeTupleCollectionField compares the non-plain tuple assignment
// shape from debug/dwarf's readScope with native Go. The synthetic entry makes
// Data.Ranges return its literal low/high-PC pair before any optional DWARF
// section is read, so this needs no ELF or compiler fixture.
func TestS281NativeTupleCollectionField(t *testing.T) {
	const source = `package main
import (
	"debug/dwarf"
	"fmt"
)
type Ranges [][2]uint64
type Scope struct { ranges Ranges }
func main() {
	entry := &dwarf.Entry{Field: []dwarf.Field{
		{Attr: dwarf.AttrLowpc, Val: uint64(10)},
		{Attr: dwarf.AttrHighpc, Class: dwarf.ClassAddress, Val: uint64(20)},
	}}
	var scope Scope
	var err error
	scope.ranges, err = new(dwarf.Data).Ranges(entry)
	fmt.Println(err, len(scope.ranges), scope.ranges[0][0], scope.ranges[0][1])
}`
	differGoSource(t, source, nil, "")
}

// TestS281TupleCollectionAssignmentControls keeps the repair on the ordinary
// composite assignment path: local tuple values, nil and empty slices, arrays,
// and a later plain reassignment/range must retain their existing semantics.
func TestS281TupleCollectionAssignmentControls(t *testing.T) {
	const source = `package main
import (
	"crypto/sha256"
	"debug/dwarf"
	"fmt"
)
type Scope struct {
	ranges [][2]uint64
	sum [32]byte
}
func localNil() ([][2]uint64, error) { return nil, nil }
func localEmpty() ([][2]uint64, error) { return [][2]uint64{}, nil }
func localLiteral() ([][2]uint64, error) { return [][2]uint64{{1, 2}, {3, 4}}, nil }
func localArray() ([32]byte, error) { return sha256.Sum256([]byte("tuple")), nil }
func main() {
	var scope Scope
	var err error
	scope.ranges, err = localNil()
	fmt.Println(err, scope.ranges == nil, len(scope.ranges))
	scope.ranges, err = localEmpty()
	fmt.Println(err, scope.ranges == nil, len(scope.ranges))
	scope.ranges, err = localLiteral()
	fmt.Println(err, len(scope.ranges), scope.ranges[0], scope.ranges[1])
	scope.sum, err = localArray()
	fmt.Println(err, scope.sum[0], scope.sum[31])
	entry := &dwarf.Entry{Field: []dwarf.Field{
		{Attr: dwarf.AttrLowpc, Val: uint64(10)},
		{Attr: dwarf.AttrHighpc, Class: dwarf.ClassAddress, Val: uint64(20)},
	}}
	ranges, err := new(dwarf.Data).Ranges(entry)
	var reassigned [][2]uint64
	reassigned = ranges
	var total uint64
	for _, pair := range reassigned { total += pair[0] + pair[1] }
	fmt.Println(err, len(reassigned), total)
}`
	differGoSource(t, source, nil, "")
}

// TestS281NativeNilTupleCollectionField keeps a dependency-produced typed nil
// slice on the same tuple-to-field path. A synthetic empty DWARF entry returns
// without consulting an ELF section, so the control remains source-only.
func TestS281NativeNilTupleCollectionField(t *testing.T) {
	const source = `package main
import (
	"debug/dwarf"
	"fmt"
)
type Scope struct { ranges [][2]uint64 }
func main() {
	var scope Scope
	var err error
	scope.ranges, err = new(dwarf.Data).Ranges(&dwarf.Entry{Field: nil})
	fmt.Println(err, scope.ranges == nil, len(scope.ranges))
}`
	differGoSource(t, source, nil, "")
}

// TestS281NativeTupleCollectionRejectsIncompatibleTarget keeps comma-ok
// assertion failure behavior adjacent to the direct native-carrier controls
// in TestS281NativeSequenceMaterializationChecksSourceType.
func TestS281NativeTupleCollectionRejectsIncompatibleTarget(t *testing.T) {
	const source = `package main
import (
	"debug/dwarf"
	"fmt"
)
type Scope struct { ranges [][2]string }
func main() {
	entry := &dwarf.Entry{Field: []dwarf.Field{
		{Attr: dwarf.AttrLowpc, Val: uint64(10)},
		{Attr: dwarf.AttrHighpc, Class: dwarf.ClassAddress, Val: uint64(20)},
	}}
	ranges, err := new(dwarf.Data).Ranges(entry)
	var scope Scope
	var ok bool
	scope.ranges, ok = any(ranges).([][2]string)
	fmt.Println(err, ok, scope.ranges == nil, len(scope.ranges))
}`
	differGoSource(t, source, nil, "")
}
