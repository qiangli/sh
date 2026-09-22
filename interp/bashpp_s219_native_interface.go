// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

// Sprint: #219; Story: #462; Story-ID: e348d2c13248
//
// Interface admission of a dependency-owned handle. bashPPImplements reads
// a type's method set from the original declarations, and a handle the
// dependency owns — the *errors.errorString behind errors.New, the
// *bufio.Writer behind bufio.NewWriter — declares nothing there: its
// methods are real but have no original body, so every such assignment
// was refused as "missing method". The dependency's own assignability
// answer is the authority for such a value; a refusal is then diagnosed
// per method from the handle's own member metadata so a missing method
// and a wrong signature keep their distinct reasons.

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// bashPPImplementsCell is bashPPImplements for a dynamic value with a cell:
// a dependency-owned handle is admitted on the dependency's authenticated
// answer, everything else on the original method set.
func (r *Runner) bashPPImplementsCell(cell *bashPPCell, dynamic syntax.BashPPTypeExpr, iface *syntax.BashPPInterfaceType) error {
	if claimed, err := r.goSourceNativeImplements(cell, dynamic, iface); claimed {
		return err
	}
	return r.bashPPImplements(dynamic, iface)
}

// goSourceNativeImplements asks the dependency whether the handle a cell
// holds is assignable to iface. It reports claimed=false for a cell the
// dependency does not own, for an interface with no methods (every value
// implements it), and for a dynamic type the interpreter declares itself,
// whose method set is the original one. The handle is only read; it is
// never materialised or copied.
func (r *Runner) goSourceNativeImplements(cell *bashPPCell, dynamic syntax.BashPPTypeExpr, iface *syntax.BashPPInterfaceType) (claimed bool, err error) {
	if !r.bashPPGoSource || cell == nil || iface == nil {
		return false, nil
	}
	for cell != nil && cell.interfaceValue != nil && !cell.interfaceValue.nilIface {
		cell = cell.interfaceValue.cell
	}
	if !r.goSourceDependencyOwnedCell(cell) {
		return false, nil
	}
	if owner, _ := bashPPInterfaceMethodOwner(dynamic); owner != "" {
		if _, declared := r.bashPPTypes[owner]; declared {
			return false, nil
		}
	}
	expected, err := r.bashPPInterfaceMethodSet("interface", iface, make(map[string]bool))
	if err != nil {
		return true, err
	}
	if len(expected.order) == 0 {
		return false, nil
	}
	value, err := r.bashPPBridgeCell(cell)
	if err != nil {
		return true, err
	}
	value.Interface = ""
	// A diagnostic spells the dynamic type as the interpreter knows it, or
	// as the dependency reports the handle when the cell was inferred.
	spelled := bashPPStripLocalPackage(value.Type)
	if dynamic != nil {
		spelled = bashPPTypeText(dynamic)
	}
	answer, err := r.bashPPNativeTypeRequest("assignable", iface, value)
	switch {
	case err == nil && answer.Kind == "bool" && answer.Text == "true":
		return true, nil
	case err == nil && answer.Kind == "bool":
		// Refused: name the method that is missing or wrong.
		if err := r.goSourceNativeMethodSetMismatch(value, spelled, expected); err != nil {
			return true, err
		}
		return true, fmt.Errorf("BASHPP-EINTERFACE-MISSING: %s does not implement interface", spelled)
	case err != nil && strings.Contains(err.Error(), "unregistered bridge type"):
		// An interface the helper could not materialise — one whose
		// signature spells a type it does not mirror — is still checked
		// method by method against the handle's own member metadata.
		return true, r.goSourceNativeMethodSetMismatch(value, spelled, expected)
	case err != nil:
		return true, err
	}
	return true, fmt.Errorf("gosource: assignability of %s answered %s", spelled, answer.Kind)
}

// goSourceNativeMethodSetMismatch checks each expected method against the
// handle's members as the dependency reports them: a member the dependency
// does not have is missing, a member whose reported Go type is not the
// expected signature is wrong. It returns nil when every method matches.
func (r *Runner) goSourceNativeMethodSetMismatch(value bashPPBridgeValue, spelled string, expected *bashPPInterfaceMethods) error {
	for _, name := range expected.order {
		method := expected.byName[name]
		// reflect never reports an unexported method, and a dependency type
		// cannot declare one in the interface's package anyway.
		if goSourceUnexportedName(name) {
			return fmt.Errorf("BASHPP-EINTERFACE-MISSING: %s does not implement interface (missing method %s)", spelled, name)
		}
		member, err := r.bashPPNativeAccess(r.ectx, "member", value, name)
		if err != nil || member.Kind != "handle" || !(member.Function || strings.HasPrefix(member.Type, "func(")) {
			return fmt.Errorf("BASHPP-EINTERFACE-MISSING: %s does not implement interface (missing method %s)", spelled, name)
		}
		if !r.goSourceNativeSignatureMatches(member.Type, method) {
			return fmt.Errorf("BASHPP-EINTERFACE-SIGNATURE: %s method %s has wrong signature", spelled, name)
		}
	}
	return nil
}

// goSourceNativeSignatureMatches compares a method type the dependency
// spelled — reflect's "func([]uint8) (int, error)", with the program's own
// types qualified "main." — against an interface method specification.
func (r *Runner) goSourceNativeSignatureMatches(text string, method bashPPInterfaceMethod) bool {
	typ, ok := syntax.BashPPTypeExprFromText(bashPPStripLocalPackage(text)).(*syntax.BashPPFuncType)
	if !ok {
		return false
	}
	if bashPPFieldsSignature(typ.Params)+"->"+bashPPFieldsSignature(typ.Results) == method.sig {
		return true
	}
	return r.bashPPAliasedSignatureEqual(typ.Params, typ.Results, method.spec.Params, method.spec.Results)
}
