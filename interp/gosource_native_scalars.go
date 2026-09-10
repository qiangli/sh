package interp

// Sprint: #118; Story: #67; Story-ID: 83b5cdc6fca6
import (
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// goSourceNativeScalarReceiver reports whether expr names a plain interpreter
// scalar that still stands for a dependency-owned defined type, and so carries
// that type's method set.
//
// [Runner.bashPPBindNativeValue] deliberately stores a scalar native result — a
// time.Duration, an os.FileMode — as an ordinary shell string instead of a
// session handle, so that arithmetic, comparison and printing keep working
// without a round trip. Only the storage is ordinary: the defined type stays on
// the cell, and a method set hangs off the type rather than off the storage.
// `diff.Hours()` is therefore the dependency's method on time.Duration;
// resolving it against interpreter field storage instead reports the receiver
// as having no such method at all.
func (r *Runner) goSourceNativeScalarReceiver(expr syntax.BashPPExpr) bool {
	switch x := expr.(type) {
	case *syntax.BashPPParenExpr:
		return r.goSourceNativeScalarReceiver(x.X)
	case *syntax.BashPPIdent:
		if r.bashPPScope == nil {
			return false
		}
		return r.goSourceNativeScalarCell(r.bashPPScope.lookup(x.Name.Value))
	}
	return false
}

// goSourceNativeScalarCell reports whether cell holds a scalar of an imported
// defined type. A handle, pointer, interface or channel cell already reaches
// the dependency by its own route and is not this case.
func (r *Runner) goSourceNativeScalarCell(cell *bashPPCell) bool {
	if !r.bashPPGoSource || cell == nil {
		return false
	}
	if cell.vr.Kind == expand.Object || cell.pointer || cell.interfaceValue != nil || cell.channel != nil {
		return false
	}
	name := cell.typeName
	if name == "" {
		if named, ok := bashPPSelectorCellType(cell).(*syntax.BashPPNamedType); ok {
			name = named.Name.Value
		}
	}
	return r.goSourceImportedTypeName(name)
}

// goSourceImportedTypeName reports whether name qualifies a type with an
// imported package.
//
// Both spellings reach here. Source-written types keep the program's own import
// alias, which gosource rewrites for hygiene, so the alias is the key of
// [Runner.bashPPImports]. A type name that came back from the dependency is the
// package-path identity reflect reports — the spelling %T prints — which is the
// map's value instead. Recognising only the alias would silently drop every
// value the dependency itself named.
func (r *Runner) goSourceImportedTypeName(name string) bool {
	bare := strings.TrimLeft(name, "*")
	dot := strings.LastIndex(bare, ".")
	if dot < 0 {
		return false
	}
	qualifier := bare[:dot]
	if r.bashPPImports[qualifier] != "" {
		return true
	}
	for _, path := range r.bashPPImports {
		if path == qualifier || goSourcePackageBase(path) == qualifier {
			return true
		}
	}
	return false
}

// goSourceCanonicalNativeType reduces one dependency-owned type name to the
// single spelling both sides of a comparison can be written in.
//
// Three spellings for one type reach the interpreter. The original program
// wrote `exec.ExitError`, which gosource rewrote to a hygienic import alias.
// The dependency reports a defined type as its package PATH plus name
// (`os/exec.ExitError`, what reflect's PkgPath gives) but a pointer type as
// Go's printed form (`*exec.ExitError`, the package NAME). Comparing any two of
// those as text says they are different types, which is how an assertion that
// must succeed came to report no match at all.
func (r *Runner) goSourceCanonicalNativeType(name string) string {
	pointers := len(name) - len(strings.TrimLeft(name, "*"))
	bare := name[pointers:]
	dot := strings.LastIndex(bare, ".")
	if dot < 0 {
		return name
	}
	qualifier, typeName := bare[:dot], bare[dot+1:]
	if path := r.bashPPImports[qualifier]; path != "" {
		qualifier = path
	}
	return strings.Repeat("*", pointers) + goSourcePackageBase(qualifier) + "." + typeName
}

// goSourceNativeTypeIdentical reports whether two names denote one imported
// type. It answers only for imported types; a local name is compared as text
// by the caller, exactly as before.
func (r *Runner) goSourceNativeTypeIdentical(left, right syntax.BashPPTypeExpr) bool {
	leftText, rightText := bashPPTypeText(left), bashPPTypeText(right)
	if !r.goSourceImportedTypeName(leftText) || !r.goSourceImportedTypeName(rightText) {
		return false
	}
	return r.goSourceCanonicalNativeType(leftText) == r.goSourceCanonicalNativeType(rightText)
}

func goSourcePackageBase(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}
