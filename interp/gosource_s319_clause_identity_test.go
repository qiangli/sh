//go:build full

package interp_test

// Sprint: #319; Story: #1087; Story-ID: 96cd7f0c400d

import (
	"strings"
	"testing"
)

// TestS319LinkedPackageTypeVerbIdentity reduces cmd/compile/internal/syntax's
// TestPos. Its typeOf helper formats a node with %T and trims "*syntax.", so
// the verb must print the identity the package declared. A linked package's
// types are flattened to __gosource_pkg_<N>_<Name> and mirrored in the
// dependency helper's package main; %T then reports that hygiene spelling
// rather than the source one. reflect.Type.String already projects the
// original identity, which the last column pins as the control.
//
// The clause rows extract a statically typed *CaseClause / *CommClause from a
// []*T body, as TestPos does; the statement and value rows show the mismatch
// does not depend on that shape.
func TestS319LinkedPackageTypeVerbIdentity(t *testing.T) {
	out, stderr := runGoSourceMultiPackage(t, "clause_identity", `package main
import "example/syntax"
func main() { syntax.Report() }
`, "example/syntax", "nodes.go", `package syntax
import (
	"fmt"
	"reflect"
	"strings"
)
type Node interface { aNode() }
type node struct{ pos int }
func (*node) aNode() {}
type Stmt interface { Node; aStmt() }
type stmt struct{ node }
func (stmt) aStmt() {}
type File struct { List []Stmt; node }
type SwitchStmt struct { Body []*CaseClause; stmt }
type SelectStmt struct { Body []*CommClause; stmt }
type CaseClause struct { Cases Node; node }
type CommClause struct { Comm Stmt; node }
type Pos struct{ line, col uint }
func parse() *File {
	f := new(File)
	s := new(SwitchStmt)
	s.Body = append(s.Body, new(CaseClause))
	f.List = append(f.List, s)
	c := new(SelectStmt)
	c.Body = append(c.Body, new(CommClause))
	f.List = append(f.List, c)
	return f
}
func typeOf(n any) string {
	const prefix = "*syntax."
	k := fmt.Sprintf("%T", n)
	return strings.TrimPrefix(k, prefix)
}
func report(want string, extract func(*File) any) {
	n := extract(parse())
	fmt.Printf("%s %s %s\n", want, typeOf(n), reflect.TypeOf(n).String())
}
func Report() {
	report("CaseClause", func(f *File) any { return Node(f.List[0].(*SwitchStmt).Body[0]) })
	report("CommClause", func(f *File) any { return Node(f.List[1].(*SelectStmt).Body[0]) })
	report("SwitchStmt", func(f *File) any { return Node(f.List[0]) })
	report("syntax.Pos", func(f *File) any { return Pos{1, 2} })
}
`)
	if stderr != "" {
		t.Fatalf("stderr=%q", stderr)
	}
	rows := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(rows) != 4 {
		t.Fatalf("stdout=%q, want 4 rows", out)
	}
	for _, row := range rows {
		fields := strings.Fields(row)
		if len(fields) != 3 {
			t.Fatalf("row %q, want 3 fields", row)
		}
		want, typ, reflected := fields[0], fields[1], fields[2]
		if control := "*syntax." + want; !strings.Contains(want, ".") && reflected != control {
			t.Errorf("control: reflect.Type.String = %s, want %s", reflected, control)
		}
		if typ != want {
			t.Errorf("type error: type = %s, want %s", typ, want)
		}
	}
}
