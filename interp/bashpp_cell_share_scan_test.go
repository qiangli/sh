// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The structural half of the aliased-cell discipline on the READER side.
//
// A cell taken straight out of [bashPPScope.lookup] is exactly the storage an
// interpreted `go` statement aliases: [bashPPCloner.cloneCell] hands the task
// the same *bashPPCell the launcher's scope holds. Reading several of its
// fields without [bashPPCell.view] is therefore a host data race, and for the
// interface-typed fields it is worse than an unspecified value — a torn
// interface word is a pointer into unrelated memory, which is how Sprint 281
// T4c's probe corpus ended up reporting races inside syntax nodes.
//
// T4c fixed every such reader its corpus reached. The rest are listed below
// and still to be audited. The point of the scan is the RATCHET: a new
// function cannot read a looked-up cell's fields directly without either
// taking a view or being added here on purpose.
//
// The scan is deliberately syntactic and deliberately narrow:
//   - the guarded field set is read out of (*bashPPCell).storeFields, so it
//     cannot drift from the fields a writer publishes as a bundle;
//   - only identifiers assigned from a `lookup(…)` call count, because those
//     are the ones that certainly name storage a task can alias;
//   - an identifier anywhere assigned from `.view()` is clean, which is the
//     fix this lane applied;
//   - a store is not a read: writers answer to the guard, not to this scan.
var bashPPCellDirectReaders = []string{
	"Get",
	"bashPPApplyMapUpdate",
	"bashPPApplySliceUpdate",
	"bashPPAssign",
	"bashPPBindBuiltinResult",
	"bashPPBooleanExprShape",
	"bashPPBuiltinAssign",
	"bashPPCallbackFunctionCapture",
	"bashPPChannelOperation",
	"bashPPCollectionAssign",
	"bashPPCollectionOperand",
	"bashPPComplexShortDecl",
	"bashPPConstantScalarExpr",
	"bashPPEvalConstIntExpr",
	"bashPPEvalTypedValue",
	"bashPPForAssign",
	"bashPPGoArgWord",
	"bashPPGoSourceTaskFunc",
	"bashPPIncDec",
	"bashPPInvoke",
	"bashPPLookupSelectorFunc",
	"bashPPMakeInterfaceValue",
	"bashPPMixedImportArg",
	"bashPPNativeArgCells",
	"bashPPNilFuncCall",
	"bashPPRangeScalarValue",
	"bashPPReflectValueReceiver",
	"bashPPResolveWord",
	"bashPPShortDecl",
	"bashPPSprint165StoredBridgeScalar",
	"bashPPSwitchConstantExpr",
	"bashPPTestingArguments",
	"delVar",
	"goSourceMethodExprType",
	"goSourceNativeAssignCall",
	"goSourceParallelDecl",
	"goSourcePrintReferenceKind",
	"goSourcePrintReferenceOperand",
	"goSourceReceiveAssign",
	"goSourceRecoverAssign",
	"goSourceUnsafeIntegerOperand",
	"goSourceValueSwitchTag",
	"goSourceWaitGroupNamed",
	"setVar",
}

func bashPPCellPtrType(e ast.Expr) bool {
	star, ok := e.(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := star.X.(*ast.Ident)
	return ok && id.Name == "bashPPCell"
}

// bashPPGuardedCellFields is the field set (*bashPPCell).storeFields publishes.
func bashPPGuardedCellFields(t *testing.T, pkg *ast.Package) map[string]bool {
	t.Helper()
	guarded := map[string]bool{}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "storeFields" || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				assign, ok := n.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for _, lhs := range assign.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok {
						guarded[sel.Sel.Name] = true
					}
				}
				return true
			})
		}
	}
	if len(guarded) < 10 {
		t.Fatalf("found %d guarded fields in (*bashPPCell).storeFields; the scan cannot have read it", len(guarded))
	}
	return guarded
}

func TestBashPPCellReadersTakeAView(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return strings.HasSuffix(fi.Name(), ".go") && !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg := pkgs["interp"]
	if pkg == nil {
		t.Fatal("package interp not parsed")
	}
	guarded := bashPPGuardedCellFields(t, pkg)

	found := map[string][]string{}
	for name, file := range pkg.Files {
		if strings.HasSuffix(name, "bashpp_cell_share.go") {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			looked, viewed := map[string]bool{}, map[string]bool{}
			writes := map[ast.Node]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.UnaryExpr:
					// Taking an address is a handle to write through.
					if x.Op == token.AND {
						writes[x.X] = true
					}
				case *ast.IncDecStmt:
					writes[x.X] = true
				case *ast.AssignStmt:
					for _, lhs := range x.Lhs {
						writes[lhs] = true
					}
					if len(x.Lhs) != len(x.Rhs) {
						return true
					}
					for i, lhs := range x.Lhs {
						id, ok := lhs.(*ast.Ident)
						if !ok {
							continue
						}
						call, ok := x.Rhs[i].(*ast.CallExpr)
						if !ok {
							continue
						}
						sel, ok := call.Fun.(*ast.SelectorExpr)
						if !ok {
							continue
						}
						switch sel.Sel.Name {
						case "view":
							viewed[id.Name] = true
						case "lookup":
							looked[id.Name] = true
						}
					}
				}
				return true
			})
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || writes[ast.Node(sel)] || !guarded[sel.Sel.Name] {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok || !looked[id.Name] || viewed[id.Name] {
					return true
				}
				found[fn.Name.Name] = append(found[fn.Name.Name], fset.Position(sel.Pos()).String())
				return true
			})
		}
	}

	var added []string
	for name, where := range found {
		if !slices.Contains(bashPPCellDirectReaders, name) {
			added = append(added, name+" at "+where[0])
		}
	}
	sort.Strings(added)
	for _, where := range added {
		t.Errorf("READER-DIRECT %s: takes a looked-up cell's guarded fields straight off the cell. "+
			"Snapshot it with cell.view() (or viewVar/viewInterface for a single field), as "+
			"bashpp_cell_share.go describes. If the cell genuinely cannot be aliased into a task, "+
			"add the function to bashPPCellDirectReaders.", where)
	}

	var gone []string
	for _, name := range bashPPCellDirectReaders {
		if found[name] == nil {
			gone = append(gone, name)
		}
	}
	sort.Strings(gone)
	for _, name := range gone {
		t.Errorf("READER-STALE %s: no longer reads a looked-up cell directly; drop it from "+
			"bashPPCellDirectReaders so the list keeps naming real work", name)
	}
}
