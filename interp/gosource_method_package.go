package interp

// Sprint: #153; Story: S153.4; Story-ID: e58cccba74f8
import (
	"strings"
	"unicode"
	"unicode/utf8"

	"mvdan.cc/sh/v3/syntax"
)

// AN UNEXPORTED METHOD NAME BELONGS TO ITS PACKAGE.
//
// Go qualifies an unexported method name by the package that declares it:
// main's `interface{ private() }` asks for main.private, and a type whose
// only `private` was promoted from another package's struct does not have
// it. The Go front end links an explicit package into the program by
// renaming its package-level declarations with a hygiene marker,
// `__gosource_pkg_<N>_<Name>`, and that marker is the package identity the
// runtime has: a method's package is its receiver type's, an interface
// method's is that of the declared interface it is spelled in.
//
// An interface literal that is not a declaration — `var x interface{ m() }`
// — carries no marker and is read as the program package's own; a linked
// package spelling such a literal with an unexported method is not told
// apart, which is the one case this identity does not cover.

const goSourceLinkedPackageMarker = "__gosource_pkg_"

// goSourceLinkedPackage reports the linked-package tag of a declared name,
// or "" for the program's own package.
func goSourceLinkedPackage(name string) string {
	if !strings.HasPrefix(name, goSourceLinkedPackageMarker) {
		return ""
	}
	rest := name[len(goSourceLinkedPackageMarker):]
	i := strings.IndexByte(rest, '_')
	if i < 0 {
		return ""
	}
	return rest[:i]
}

// goSourceUnexportedName reports whether a method name is qualified by its
// package: one that does not begin with an upper-case letter.
func goSourceUnexportedName(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return r != utf8.RuneError && !unicode.IsUpper(r)
}

// goSourceInterfacePackage reports the package of a declared interface type
// by finding the declaration whose type is this very literal.
func (r *Runner) goSourceInterfacePackage(iface *syntax.BashPPInterfaceType) string {
	if !r.bashPPGoSource || iface == nil {
		return ""
	}
	for name, decl := range r.bashPPTypes {
		if decl.typeExpr == iface {
			return goSourceLinkedPackage(name)
		}
	}
	return ""
}

// goSourceMethodSpecPackage reports the package of an interface method spec
// by the declared interface that spells it directly.
func (r *Runner) goSourceMethodSpecPackage(spec *syntax.BashPPMethodSpec) string {
	if !r.bashPPGoSource || spec == nil {
		return ""
	}
	for name, decl := range r.bashPPTypes {
		iface, ok := decl.typeExpr.(*syntax.BashPPInterfaceType)
		if !ok {
			continue
		}
		for _, elem := range bashPPInterfaceElems(iface) {
			if elem.Method == spec {
				return goSourceLinkedPackage(name)
			}
		}
	}
	return ""
}

// goSourceMethodPackage reports the package of the method a selection
// resolved: a declared method's receiver type, or the declared interface
// an embedded interface's method is spelled in.
func (r *Runner) goSourceMethodPackage(sel bashPPSelection) string {
	if sel.method != nil && sel.method.decl != nil && sel.method.decl.Receiver != nil && sel.method.decl.Receiver.RecvType != nil {
		return goSourceLinkedPackage(sel.method.decl.Receiver.RecvType.Value)
	}
	return r.goSourceMethodSpecPackage(sel.interfaceSpec)
}
