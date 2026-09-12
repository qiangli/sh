// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/constant"
	"go/token"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

type bashPPBranchKind uint8

const (
	bashPPBranchNone bashPPBranchKind = iota
	bashPPBranchBreak
	bashPPBranchContinue
	bashPPBranchFallthrough
	bashPPBranchGoto
)

func (r *Runner) bashPPBranchStmt(branch *syntax.BashPPBranch) {
	switch branch.Kw.Value {
	case "break":
		r.bashPPBranch = bashPPBranchBreak
	case "continue":
		r.bashPPBranch = bashPPBranchContinue
	case "fallthrough":
		r.bashPPBranch = bashPPBranchFallthrough
	default:
		panic("invalid Bash++ branch " + branch.Kw.Value)
	}
	r.bashPPBranchDepth = int(branch.Depth)
	if r.bashPPBranchDepth < 1 {
		r.bashPPBranchDepth = 1
	}
	r.exit.clear()
}

func (r *Runner) bashPPBranchEscapesEligible() bool {
	if r.bashPPBranchDepth <= 1 {
		return false
	}
	r.bashPPBranchDepth--
	return true
}

func (r *Runner) bashPPClearBranch() {
	r.bashPPBranch = bashPPBranchNone
	r.bashPPBranchDepth = 0
	r.bashPPGotoLabel = ""
	r.exit.clear()
}

// bashPPGotoStmt raises a goto. It unwinds like a break through every
// enclosing statement until [Runner.stmts] finds the label in a statement
// list it is executing, then that list resumes at the label. Go forbids
// jumping into a block, over a variable declaration, or across a function
// boundary, and go/types has already rejected those programs, so the label is
// always in the current block or an enclosing one of the same function.
func (r *Runner) bashPPGotoStmt(g *syntax.BashPPGoto) {
	r.bashPPBranch = bashPPBranchGoto
	r.bashPPBranchDepth = 0
	r.bashPPGotoLabel = g.Label.Value
	r.exit.clear()
}

// bashPPGotoTarget reports the index of the statement labeled with the
// pending goto's label in stmts, or -1 when the goto must keep unwinding.
func (r *Runner) bashPPGotoTarget(stmts []*syntax.Stmt) int {
	for i, stmt := range stmts {
		if labeled, ok := stmt.Cmd.(*syntax.BashPPLabeled); ok && labeled.Label.Value == r.bashPPGotoLabel {
			return i
		}
	}
	return -1
}

// Evaluation of the Bash++ P1 ("Day-1") nodes.
//
// Like its counterpart in sh/syntax, this file is deliberately separate from
// runner.go: the evaluation can be written, reviewed and merged without
// touching a line the certification workstream owns. What runner.go owns is
// one `case` arm per node in the existing command type switch, each a single
// call into this file.
//
// Every P1 node is now reached from source: the parser claims the var/const
// declarations (sh/syntax/bashpp_decl.go), the `type` bodies and the `:=`
// short declarations (sh/syntax/bashpp_short.go), and the Go-form call, and
// runner.go dispatches each to its arm below. [Runner.bashPPIf] alone remains
// unreachable from source — brace-form `if` is a recorded Day-1 deferral (see
// sh/syntax/bashpp_braceif_decision.go) — and its runner arm exists only so a
// hand-built tree receives the owner's diagnostic rather than the generic
// "unhandled command node" fallback.
//
// ONE VALUE MODEL, NOT TWO. `:=` binds through the existing expand.Object
// machinery whenever the value is structured. Introducing a second
// representation for "a Go value in the shell" is the most likely way for this
// phase to do lasting damage: the object model already answers how a value
// crosses execve (as JSON), how it interpolates, and what happens under
// `set -o posix`, and a parallel model would have to answer all three again
// and would inevitably answer at least one differently.

// Every function below takes a ctx it may not use. That is deliberate: these
// are the bodies of the `case` arms in runner.go's command type switch, which
// is passed a ctx, and a uniform signature keeps each arm a single call. The
// arms that execute a body — the call path, via the typed-function machinery
// in bashpp_func.go and the eval toolchain — do use it.
//
// bashPPDeclare evaluates var, const and type declarations.
//
// The three share an implementation because the interpreter treats them
// identically apart from mutability. `const` additionally marks the variable
// read-only, which reuses the shell's existing readonly machinery rather than
// inventing a Bash++ notion of immutability — a const that could be reassigned
// by `declare` would be a lie, and the shell already knows how to refuse that.
func (r *Runner) bashPPDeclare(ctx context.Context, d *syntax.BashPPDecl) {
	if !r.objectsEnabled() {
		// Fail-safe: a Bash++ node can only be parsed under LangBashPP, so one
		// reaching a runner that has extensions off is a bug in the caller —
		// a dialect mismatch between parser and runner, or a hand-built tree —
		// not an input error, so it must be loud rather than silent.
		r.errf("bash++ declaration evaluated with extensions disabled\n")
		r.exit = exitStatus{code: 2}
		return
	}
	name := d.Name.Value
	typeInstalled, keepType := false, false
	defer func() {
		if typeInstalled && !keepType {
			delete(r.bashPPTypes, name)
		}
	}()
	if !syntax.BashPPValidIdent(name) {
		r.errf("invalid variable name: %q\n", name)
		r.exit = exitStatus{code: 2}
		return
	}
	if d.Site == syntax.StartTypeDecl {
		if err := bashPPValidateTypeParamDecls(d.TypeParams); err != nil {
			r.errf("%v\n", err)
			r.exit.code = 2
			return
		}
	}
	if r.bashPPScope == nil {
		// A runner which reached a Bash++ node without Reset having built the
		// outermost block; give it one rather than binding nowhere.
		r.bashPPScope = newBashPPScope(nil)
	}
	if r.goSourceChannelDeclaration(d) {
		return
	}
	if r.bashPPNativeDeclaration(d) {
		return
	}
	if d.Site == syntax.StartTypeDecl {
		if r.bashPPTypes == nil {
			r.bashPPTypes = make(map[string]bashPPType)
		}
		// A pre-registered package-level type is already in the registry by
		// design; only an entry this statement did not put there is a clash.
		preRegistered := r.bashPPGoSourceClaimType(name)
		if _, exists := r.bashPPTypes[name]; exists && !preRegistered {
			r.errf("%stype %s redeclared in this session\n", r.bashErrPrefix(d.Pos()), name)
			r.exit = exitStatus{code: 2}
			return
		}
		if d.DeclType == nil {
			r.errf("%stype %s has no underlying type\n", r.bashErrPrefix(d.Pos()), name)
			r.exit = exitStatus{code: 2}
			return
		}
		_, interfaceDeclaration := d.DeclTypeExpr.(*syntax.BashPPInterfaceType)
		// Make the declaration visible while validating its representation.
		// Recursive references can then be classified as either finite (behind
		// pointer/slice/map indirection) or infinite (direct/array/struct value
		// recursion), instead of being misreported as undefined.
		candidate := bashPPType{underlying: d.DeclType.Value, alias: d.Alias, typeParams: d.TypeParams, typeExpr: d.DeclTypeExpr, fields: d.StructFields}
		r.bashPPTypes[name] = candidate
		typeInstalled = true
		if d.DeclType.Value == "struct" {
			seenFields := make(map[string]bool)
			for _, field := range bashPPFlatFields(d.StructFields) {
				if seenFields[field.name] {
					r.errf("BASHPP-ESTRUCT-FIELD-DUPLICATE: field %q declared more than once\n", field.name)
					r.exit = exitStatus{code: 2}
					return
				}
				seenFields[field.name] = true
			}
		} else if d.DeclType.Value == "interface" || interfaceDeclaration {
			iface, ok := d.DeclTypeExpr.(*syntax.BashPPInterfaceType)
			if !ok {
				r.errf("BASHPP-EINTERFACE-TYPE: malformed interface declaration %s\n", name)
				r.exit = exitStatus{code: 2}
				return
			}
			if err := r.bashPPValidateInterfaceType(name, iface); err != nil {
				r.errf("%v\n", err)
				r.exit = exitStatus{code: 2}
				return
			}
		} else if d.DeclType.Value == "enum" {
			seen := make(map[string]bool, len(d.EnumMembers))
			for _, member := range d.EnumMembers {
				if !syntax.BashPPValidIdent(member.Value) {
					r.errf("BASHPP-EENUM-MEMBER: enum member %q must be an identifier\n", member.Value)
					r.exit = exitStatus{code: 2}
					return
				}
				if seen[member.Value] {
					r.errf("BASHPP-EENUM-DUPLICATE: enum %s declares member %q more than once\n", name, member.Value)
					r.exit = exitStatus{code: 2}
					return
				}
				seen[member.Value] = true
			}
		}
		if d.DeclType.Value != "interface" && !interfaceDeclaration && d.DeclType.Value != "enum" {
			if d.Alias && bashPPRecursiveGenericValue(d.DeclTypeExpr, name) {
				r.errf("%scyclic type declaration: %s\n", r.bashErrPrefix(d.Pos()), name)
				r.exit = exitStatus{code: 2}
				return
			}
			if err := r.bashPPValidateTypeRepresentation(d.DeclTypeExpr, make(map[string]bool), make(map[string]bool)); err != nil {
				if err == errBashPPTypeCycle {
					r.errf("%scyclic type declaration: %s\n", r.bashErrPrefix(d.Pos()), name)
				} else {
					r.errf("%s%v\n", r.bashErrPrefix(d.Pos()), err)
				}
				r.exit = exitStatus{code: 2}
				return
			}
		}
	}
	if !r.bashPPGoSource && (d.Site == syntax.StartVar || d.Site == syntax.StartConst) && d.DeclTypeExpr != nil {
		if named, ok := r.bashPPUnderlyingType(d.DeclTypeExpr).(*syntax.BashPPNamedType); ok &&
			(named.Name.Value == "complex64" || named.Name.Value == "complex128") {
			r.errf("%sBASHPP-ECOMPLEX-UNSUPPORTED: complex values are not supported by the Bash++ scalar carrier\n", r.bashErrPrefix(d.DeclTypeExpr.Pos()))
			r.exit = exitStatus{code: 2}
			return
		}
	}
	if d.Site == syntax.StartVar && d.DeclTypeExpr != nil {
		if err := r.bashPPValidateValueType(d.DeclTypeExpr, make(map[string]bool)); err != nil {
			r.errf("%s%v\n", r.bashErrPrefix(d.Pos()), err)
			r.exit = exitStatus{code: 2}
			return
		}
	}

	// DeclType is also the in-process identity source for P3-C receiver values.
	// The visible scalar stays in the ordinary shell variable below; the named
	// type and pointer bits are attached to its lexical cell after declaration.
	vr := r.bashPPValue(ctx, d.Init)
	if lit, ok := d.InitExpr.(*syntax.BashPPFuncLit); ok {
		_, vr = r.bashPPMakeClosure(lit)
	}
	if typed, handled, err := r.bashPPTypedScalarDeclValue(d); handled {
		if err != nil {
			if errors.Is(err, errBashPPScalarInterrupted) {
				return
			}
			pos := d.Pos()
			if d.InitExpr != nil {
				pos = d.InitExpr.Pos()
			}
			r.errf("%s%v\n", r.bashErrPrefix(pos), err)
			r.exit = exitStatus{code: 2}
			return
		}
		vr = typed
	}
	legacyPointerVR := vr
	legacyPointerInit := false
	var valueMeta *bashPPCollectionMeta
	var pointerValue *bashPPPointer
	if d.Site == syntax.StartVar && d.DeclTypeExpr != nil {
		if _, ok := r.bashPPInterfaceType(d.DeclTypeExpr); ok {
			if d.InitExpr != nil {
				iv, ifaceVR, err := r.bashPPMakeInterfaceValue(d.InitExpr, d.DeclTypeExpr)
				if err != nil {
					r.errf("%v\n", err)
					r.exit = exitStatus{code: 2}
					return
				}
				vr = ifaceVR
				defer func() {
					if cell := r.bashPPScope.lookup(name); cell != nil {
						cell.interfaceValue = iv
					}
				}()
			} else {
				vr = expand.Variable{Set: true, Kind: expand.String}
				defer func() {
					if cell := r.bashPPScope.lookup(name); cell != nil {
						cell.interfaceValue = &bashPPInterfaceValue{nilIface: true}
					}
				}()
			}
		} else if pointerType, ok := r.bashPPPointerType(d.DeclTypeExpr); ok {
			if len(d.Init) > 0 {
				value, _, err := r.bashPPEvalTypedValue(d.InitExpr, d.DeclTypeExpr)
				// Keep the established receiver construction surface (`var p
				// *Count = 9`) by treating a direct element value as an allocated
				// pointee. New code can spell the same operation as new(Count).
				if err != nil {
					elem, meta, elemErr := r.bashPPEvalTypedValue(d.InitExpr, pointerType.Element)
					if elemErr == nil {
						heap := &bashPPCell{declType: pointerType.Element}
						bashPPStoreCellValue(heap, elem, meta)
						value, err = &bashPPPointer{target: heap, elem: pointerType.Element}, nil
						legacyPointerInit = true
					}
				}
				if err != nil {
					r.errf("%v\n", err)
					r.exit = exitStatus{code: 2}
					return
				}
				pointerValue, _ = value.(*bashPPPointer)
			}
			vr = expand.Variable{Set: true, Kind: expand.String}
		}
		shape := r.bashPPUnderlyingType(d.DeclTypeExpr)
		_, _, isStruct := r.bashPPStructFields(d.DeclTypeExpr)
		_, isCollection := shape.(*syntax.BashPPCollectionType)
		if d.InitExpr != nil && (isStruct || isCollection) {
			value, meta, err := r.bashPPEvalTypedValue(d.InitExpr, d.DeclTypeExpr)
			if err != nil {
				r.errf("%v\n", err)
				r.exit = exitStatus{code: 2}
				return
			}
			if meta != nil {
				vr, valueMeta = expand.NewObject(value), meta
			}
		} else if isStruct {
			value, meta := r.bashPPZeroValue(d.DeclTypeExpr)
			vr, valueMeta = expand.NewObject(value), meta
		} else if collection, ok := shape.(*syntax.BashPPCollectionType); ok {
			value, meta := r.bashPPZeroValue(collection)
			if meta != nil {
				meta.typ = d.DeclTypeExpr
			}
			vr, valueMeta = expand.NewObject(value), meta
		}
	}
	// A declaration which shadows an exported shell variable inherits the
	// export, so the child process and the script agree on the value. It does
	// not export a name the shell was not already exporting: a Go declaration
	// is a local, and locals do not cross execve.
	if prev := r.writeEnv.Get(name); prev.Exported {
		vr.Exported = true
	}
	// `const` marks the cell readonly as well as refusing assignment through
	// the scope, so the shell's own readonly paths — `unset`, `declare -r` —
	// see it too; a const those could quietly reassign would be a lie.
	if err := r.bashPPScope.declare(name, vr, d.Site == syntax.StartConst); err != nil {
		r.errf("%s%v\n", r.bashErrPrefix(d.Pos()), err)
		r.exit = exitStatus{code: 2}
		return
	}
	if r.bashPPGoSource && d.Site == syntax.StartConst && d.InitExpr != nil {
		if value, err := r.bashPPEvalScalarExpr(d.InitExpr); err == nil {
			cell := r.bashPPScope.lookup(name)
			cell.exactScalar = value.value
			cell.scalarKind = value.value.Kind()
		}
	}
	if d.Site == syntax.StartTypeDecl {
		members := make([]string, len(d.EnumMembers))
		for i, member := range d.EnumMembers {
			members[i] = member.Value
		}
		r.bashPPTypes[name] = bashPPType{underlying: d.DeclType.Value, alias: d.Alias, typeParams: d.TypeParams, members: members, typeExpr: d.DeclTypeExpr, fields: d.StructFields}
		keepType = true
	}
	if (d.Site == syntax.StartVar || d.Site == syntax.StartConst) && d.DeclType != nil {
		_, pointer := r.bashPPPointerType(d.DeclTypeExpr)
		base := bashPPNamedTypeBase(d.DeclTypeExpr)
		cell := r.bashPPScope.lookup(name)
		cell.declType = d.DeclTypeExpr
		if _, named := r.bashPPTypes[base]; named {
			cell.typeName = base
		}
		if pointer {
			cell.pointer, cell.pointerValue = true, pointerValue
			cell.nilPointer = pointerValue == nil
			if legacyPointerInit {
				cell.vr = legacyPointerVR
			}
		}
		if valueMeta != nil {
			cell := r.bashPPScope.lookup(name)
			cell.object = &bashPPObjectIdentity{owner: name, collection: valueMeta}
			cell.valueMeta = valueMeta
			if named, ok := d.DeclTypeExpr.(*syntax.BashPPNamedType); ok {
				cell.typeName = named.Name.Value
			}
		}
	}
}

// bashPPTypedScalarDeclValue applies Go's zero-value, representability, and
// assignability rules to scalar declarations. Structured, pointer, and
// interface values retain their single-model paths below in bashPPDeclare.
func (r *Runner) bashPPTypedScalarDeclValue(d *syntax.BashPPDecl) (expand.Variable, bool, error) {
	if r.bashPPGoSource && d.Site == syntax.StartConst && d.DeclTypeExpr == nil {
		value, err := r.bashPPEvalScalarExpr(d.InitExpr)
		if err != nil {
			return expand.Variable{}, true, err
		}
		return expand.Variable{Set: true, Kind: expand.String, Str: bashPPScalarString(value.value)}, true, nil
	}
	if (d.Site != syntax.StartVar && d.Site != syntax.StartConst) || d.DeclTypeExpr == nil {
		return expand.Variable{}, false, nil
	}
	shape := r.bashPPUnderlyingType(d.DeclTypeExpr)
	named, ok := shape.(*syntax.BashPPNamedType)
	if !ok || !bashPPBuiltinType(named.Name.Value) || named.Name.Value == "error" {
		if d.Site == syntax.StartConst {
			return expand.Variable{}, true, fmt.Errorf("BASHPP-ECONST-TYPE: const initializer has unsupported type %s", bashPPTypeText(d.DeclTypeExpr))
		}
		return expand.Variable{}, false, nil
	}
	base := named.Name.Value
	if !r.bashPPGoSource && (base == "complex64" || base == "complex128") {
		return expand.Variable{}, true, fmt.Errorf("BASHPP-ECOMPLEX-UNSUPPORTED: complex constants are not supported by the Bash++ scalar carrier")
	}
	if d.InitExpr == nil {
		zero := "0"
		switch base {
		case "bool":
			zero = "false"
		case "string":
			zero = ""
		}
		return expand.Variable{Set: true, Kind: expand.String, Str: zero}, true, nil
	}
	if d.Site == syntax.StartConst && !r.bashPPConstantScalarExpr(d.InitExpr, base) {
		return expand.Variable{}, true, fmt.Errorf("BASHPP-ECONST-EXPR: const initializer is not a constant expression")
	}
	var value bashPPScalar
	var err error
	// Bash++ has always allowed an unquoted shell word as a string value
	// (`var token Token = hello`). Preserve that source surface for string
	// declarations while still treating a bound identifier as an expression.
	if ident, ok := d.InitExpr.(*syntax.BashPPIdent); ok && base == "string" && r.bashPPScope.lookup(ident.Name.Value) == nil {
		value = bashPPScalar{value: constant.MakeString(ident.Name.Value)}
	} else {
		value, err = r.bashPPEvalScalarExpr(d.InitExpr)
	}
	if err != nil {
		return expand.Variable{}, true, err
	}
	if value.typ != "" {
		actual := &syntax.BashPPNamedType{Name: &syntax.Lit{Value: value.typ}}
		if !r.bashPPTypeAssignable(actual, d.DeclTypeExpr) {
			return expand.Variable{}, true, fmt.Errorf("BASHPP-EASSIGN-TYPE: cannot use %s as %s in declaration", value.typ, bashPPTypeText(d.DeclTypeExpr))
		}
	} else if !bashPPUntypedScalarAssignable(base, value.value) {
		return expand.Variable{}, true, fmt.Errorf("BASHPP-EASSIGN-TYPE: cannot use %s constant as %s in declaration", value.value.Kind(), bashPPTypeText(d.DeclTypeExpr))
	}
	converted, err := r.bashPPConvertScalar(base, value)
	if err != nil {
		return expand.Variable{}, true, err
	}
	return expand.Variable{Set: true, Kind: expand.String, Str: bashPPScalarString(converted.value)}, true, nil
}

func (r *Runner) bashPPConstantScalarExpr(expr syntax.BashPPExpr, targetBase string) bool {
	switch x := expr.(type) {
	case *syntax.BashPPBasicLit:
		return true
	case *syntax.BashPPIdent:
		if x.Name.Value == "true" || x.Name.Value == "false" {
			return true
		}
		cell := r.bashPPScope.lookup(x.Name.Value)
		return cell != nil && cell.constant || cell == nil && targetBase == "string"
	case *syntax.BashPPParenExpr:
		return r.bashPPConstantScalarExpr(x.X, targetBase)
	case *syntax.BashPPUnaryExpr:
		return r.bashPPConstantScalarExpr(x.X, targetBase)
	case *syntax.BashPPBinaryExpr:
		return r.bashPPConstantScalarExpr(x.X, targetBase) && r.bashPPConstantScalarExpr(x.Y, targetBase)
	case *syntax.BashPPConvertExpr:
		return r.bashPPConstantScalarExpr(x.X, targetBase)
	}
	return false
}

func bashPPUntypedScalarAssignable(base string, value constant.Value) bool {
	switch base {
	case "complex64", "complex128":
		return value.Kind() == constant.Int || value.Kind() == constant.Float || value.Kind() == constant.Complex
	case "string":
		return value.Kind() == constant.String
	case "bool":
		return value.Kind() == constant.Bool
	case "float32", "float64":
		return value.Kind() == constant.Int || value.Kind() == constant.Float
	default:
		return bashPPIntegerType(base) && constant.ToInt(value).Kind() == constant.Int
	}
}

func bashPPNamedTypeBase(typ syntax.BashPPTypeExpr) string {
	switch x := typ.(type) {
	case *syntax.BashPPNamedType:
		return x.Name.Value
	case *syntax.BashPPPointerType:
		return bashPPNamedTypeBase(x.Element)
	}
	return ""
}

// bashPPValidatePackageInitOrder rejects the package-initialization cases this
// mixed shell/Go runtime cannot reorder soundly. Moving a declaration ahead of
// an intervening shell command would move observable I/O, so forward/cyclic
// directly referenced forward or self dependencies receive a positioned
// diagnostic instead of silently reading a shell zero value. Source-ordered
// dependencies retain their ordinary runtime behavior.
func (r *Runner) bashPPValidatePackageInitOrder(file *syntax.File) bool {
	type topDecl struct {
		index int
	}
	decls := make(map[string]topDecl)
	for index, stmt := range file.Stmts {
		if decl, ok := stmt.Cmd.(*syntax.BashPPDecl); ok && (decl.Site == syntax.StartVar || decl.Site == syntax.StartConst) {
			decls[decl.Name.Value] = topDecl{index: index}
		}
	}
	for index, stmt := range file.Stmts {
		if fn, ok := stmt.Cmd.(*syntax.BashPPFuncDecl); ok && fn.Receiver == nil && fn.Name.Value == "init" {
			r.errf("%sBASHPP-EINIT-FUNC: init functions are unsupported in the mixed shell execution model\n", r.bashErrPrefix(fn.Name.Pos()))
			r.exit = exitStatus{code: 2}
			return false
		}
		decl, ok := stmt.Cmd.(*syntax.BashPPDecl)
		if !ok || decl.InitExpr == nil {
			continue
		}
		valid := true
		syntax.Walk(decl.InitExpr, func(node syntax.Node) bool {
			if !valid {
				return false
			}
			ident, ok := node.(*syntax.BashPPIdent)
			if !ok {
				return true
			}
			dependency, declared := decls[ident.Name.Value]
			if declared && dependency.index >= index {
				r.errf("%sBASHPP-EINIT-ORDER: initializer for %s depends on %s before it is initialized; dependency reordering across shell statements is unsupported\n",
					r.bashErrPrefix(ident.Pos()), decl.Name.Value, ident.Name.Value)
				r.exit = exitStatus{code: 2}
				valid = false
				return false
			}
			return true
		})
		if !valid {
			return false
		}
	}
	return true
}

func bashPPTypeContainsTypeParam(typ syntax.BashPPTypeExpr) bool {
	switch x := typ.(type) {
	case *syntax.BashPPTypeParamType:
		return true
	case *syntax.BashPPNamedType:
		for _, arg := range x.TypeArgs {
			if bashPPTypeContainsTypeParam(arg.ArgType) {
				return true
			}
		}
	case *syntax.BashPPCollectionType:
		return bashPPTypeContainsTypeParam(x.Key) || bashPPTypeContainsTypeParam(x.Element)
	case *syntax.BashPPPointerType:
		return bashPPTypeContainsTypeParam(x.Element)
	case *syntax.BashPPStructType:
		for _, field := range x.Fields {
			if bashPPTypeContainsTypeParam(field.FieldTypeExpr) {
				return true
			}
		}
	case *syntax.BashPPInterfaceType:
		for _, elem := range x.Elems {
			if bashPPTypeContainsTypeParam(elem.Embedded) {
				return true
			}
		}
	case *syntax.BashPPUnionType:
		for _, term := range x.Terms {
			if bashPPTypeContainsTypeParam(term) {
				return true
			}
		}
	case *syntax.BashPPApproxType:
		return bashPPTypeContainsTypeParam(x.Term)
	}
	return false
}

func bashPPRecursiveGenericValue(typ syntax.BashPPTypeExpr, name string) bool {
	switch x := typ.(type) {
	case *syntax.BashPPNamedType:
		if x.Name.Value == name {
			return true
		}
		for _, arg := range x.TypeArgs {
			if bashPPRecursiveGenericValue(arg.ArgType, name) {
				return true
			}
		}
	case *syntax.BashPPCollectionType:
		return bashPPRecursiveGenericValue(x.Key, name) || bashPPRecursiveGenericValue(x.Element, name)
	case *syntax.BashPPPointerType:
		return bashPPRecursiveGenericValue(x.Element, name)
	case *syntax.BashPPStructType:
		for _, field := range x.Fields {
			if bashPPRecursiveGenericValue(field.FieldTypeExpr, name) {
				return true
			}
		}
	case *syntax.BashPPInterfaceType:
		for _, elem := range x.Elems {
			if bashPPRecursiveGenericValue(elem.Embedded, name) {
				return true
			}
		}
	case *syntax.BashPPUnionType:
		for _, term := range x.Terms {
			if bashPPRecursiveGenericValue(term, name) {
				return true
			}
		}
	case *syntax.BashPPApproxType:
		return bashPPRecursiveGenericValue(x.Term, name)
	}
	return false
}

// bashPPTypeTerminates validates the entire already-declared underlying chain.
// This is defensive as declarations normally establish the invariant one by
// one, but imported/retained registries must not smuggle a missing or cyclic
// alias into a new declaration.
func (r *Runner) bashPPTypeTerminates(name string, seen map[string]bool) bool {
	if bashPPBuiltinType(name) || name == "enum" {
		return true
	}
	if seen[name] {
		return false
	}
	typ, ok := r.bashPPTypes[name]
	if !ok {
		return false
	}
	seen[name] = true
	if typ.typeExpr != nil {
		if named, ok := typ.typeExpr.(*syntax.BashPPNamedType); ok {
			return r.bashPPTypeTerminates(named.Name.Value, seen)
		}
		return true
	}
	base := strings.TrimPrefix(typ.underlying, "*")
	if base == name {
		return strings.HasPrefix(typ.underlying, "*") && !typ.alias
	}
	return r.bashPPTypeTerminates(base, seen)
}

// bashPPShortDeclConversion recognizes `x := T(v)`. Only a builtin or defined
// type whose underlying type is scalar is claimed; a name that already denotes
// a callable or a binding, and a type with a structured underlying type, both
// stay on the paths that already own them.
func (r *Runner) bashPPShortDeclConversion(d *syntax.BashPPShortDecl) (*syntax.BashPPConvertExpr, bool) {
	if len(d.Lhs) != 1 {
		return nil, false
	}
	return r.bashPPConversionCall(d.Call)
}

// bashPPConversionCall recognizes a call-shaped Go conversion `T(v)`. See
// [Runner.bashPPShortDeclConversion] for what it deliberately leaves alone.
func (r *Runner) bashPPConversionCall(call *syntax.BashPPCall) (*syntax.BashPPConvertExpr, bool) {
	if call == nil || call.CalleeExpr != nil || len(call.Fun) != 1 {
		return nil, false
	}
	if len(call.ArgExprs) != 1 || call.ArgExprs[0] == nil || call.Ellipsis.IsValid() {
		return nil, false
	}
	name := call.Fun[0].Value
	if r.bashPPFuncs[name] != nil || !bashPPScalarTypeName(name) {
		return nil, false
	}
	if !bashPPBuiltinType(name) {
		decl, declared := r.bashPPTypes[name]
		if !declared {
			return nil, false
		}
		shape, ok := r.bashPPUnderlyingType(decl.typeExpr).(*syntax.BashPPNamedType)
		if !ok || !bashPPScalarTypeName(shape.Name.Value) || !bashPPBuiltinType(shape.Name.Value) {
			return nil, false
		}
	}
	return &syntax.BashPPConvertExpr{ConvType: call.Fun[0], Lparen: call.Lparen, Rparen: call.Rparen, X: call.ArgExprs[0]}, true
}

// bashPPConvertNamedScalar converts a scalar to a DEFINED type as well as to a
// builtin one. `MyFloat(3)` converts through MyFloat's underlying type and then
// keeps MyFloat as the result's type: that named identity is what carries the
// defined type's method set, so `MyFloat(3).Abs()` resolves where a bare
// float64 would not.
func (r *Runner) bashPPConvertNamedScalar(name string, x bashPPScalar) (bashPPScalar, error) {
	if bashPPBuiltinType(name) {
		return r.bashPPConvertScalar(name, x)
	}
	named := &syntax.BashPPNamedType{Name: &syntax.Lit{Value: name}}
	shape, ok := r.bashPPUnderlyingType(named).(*syntax.BashPPNamedType)
	if !ok || shape == named || !bashPPBuiltinType(shape.Name.Value) {
		return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-CONVERT: cannot convert %s to %s", x.value.Kind(), name)
	}
	converted, err := r.bashPPConvertScalar(shape.Name.Value, x)
	if err != nil {
		return bashPPScalar{}, err
	}
	converted.typ = name
	return converted, nil
}

// bashPPScalarTypeName rejects the two names [bashPPBuiltinType] admits that
// never denote a scalar conversion: `struct` is a shape, and `error` is an
// interface whose conversions are interface assignments.
func bashPPScalarTypeName(name string) bool {
	return name != "struct" && name != "error"
}

func bashPPBuiltinType(name string) bool {
	switch name {
	case "bool", "byte", "complex64", "complex128", "error", "float32", "float64",
		"int", "int8", "int16", "int32", "int64", "rune", "string",
		"struct", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
		return true
	}
	return false
}

// bashPPShortDecl evaluates `x := 42` and `x, y := f()`.
//
// The tuple form is bound positionally. A length mismatch is a hard error
// rather than a partial bind: binding what fits and leaving the rest at their
// previous values would leave the shell in a state no reader could predict
// from the source line.
func (r *Runner) bashPPShortDecl(ctx context.Context, d *syntax.BashPPShortDecl) {
	if !r.objectsEnabled() {
		r.errf("bash++ short declaration evaluated with extensions disabled\n")
		r.exit = exitStatus{code: 2}
		return
	}
	if r.bashPPScope == nil {
		r.bashPPScope = newBashPPScope(nil)
	}
	txn, ok := r.bashPPBeginShortDecl(d)
	if !ok {
		return
	}
	defer r.bashPPEndShortDecl(txn, d.Pos())
	if r.bashPPGoSource && len(d.RhsExprs) > 0 {
		r.goSourceParallelDecl(d)
		return
	}
	if r.goSourceMapCommaDecl(d) {
		return
	}
	if r.bashPPComplexShortDecl(d) {
		return
	}
	if r.bashPPGoSource && len(d.Lhs) == 1 {
		if method, ok := d.Expr.(*syntax.BashPPSelectorExpr); ok && method.MethodValue && !r.bashPPNativeExpr(method.X) {
			cell, err := r.goSourceLocalMethodValue(method)
			if err != nil {
				if !r.bashPPPanicking() {
					r.exit.fatal(err)
				}
				return
			}
			r.bashPPDeclareName(d.Lhs[0].Value, cell.vr)
			if target := r.bashPPScope.lookup(d.Lhs[0].Value); target != nil {
				*target = *cell
			}
			return
		}
	}
	// A named function value uses the same callable registry as a closure or
	// method value, retaining its declaration (including the agentic marker).
	if len(d.Lhs) == 1 {
		var sourceName string
		if ident, ok := d.Expr.(*syntax.BashPPIdent); ok {
			sourceName = ident.Name.Value
		}
		if len(d.Rhs) == 1 {
			sourceName = d.Rhs[0].Lit()
		}
		if sourceName != "" {
			if vr := r.lookupVar(sourceName); vr.IsSet() {
				if _, ok := r.bashPPClosure(vr.Str); ok {
					r.bashPPDeclareName(d.Lhs[0].Value, vr)
					return
				}
			} else if fn := r.bashPPFuncs[sourceName]; fn != nil {
				r.bashPPDeclareName(d.Lhs[0].Value, r.bashPPStoreFunc(fn))
				return
			}
		}
	}
	if d.Expr != nil {
		if call, ok := d.Expr.(*syntax.BashPPCall); ok {
			if cells, handled := r.goSourceErrorsAsTypeCells(call); handled {
				if len(d.Lhs) != len(cells) {
					r.errf("assignment mismatch: %d variable(s) but %d value(s)\n", len(d.Lhs), len(cells))
					r.exit = exitStatus{code: 2}
					return
				}
				for i, name := range d.Lhs {
					if name.Value == "_" {
						continue
					}
					r.bashPPDeclareName(name.Value, cells[i].vr)
					if target := r.bashPPScope.lookup(name.Value); target != nil {
						*target = *cells[i]
					}
				}
				return
			}
		}
		// A read rooted in a dependency-owned value binds a session handle;
		// see bashPPNativeShortDecl in bashpp_native_access.go.
		if r.bashPPNativeShortDecl(d) {
			return
		}
		if assert, ok := d.Expr.(*syntax.BashPPTypeAssertExpr); ok {
			if assert.TypeToken != nil {
				r.errf("BASHPP-EASSERT-TYPE: .(type) is only valid in a type switch\n")
				r.exit = exitStatus{code: 2}
				return
			}
			if len(d.Lhs) != 1 && len(d.Lhs) != 2 {
				r.errf("assignment mismatch: %d variable(s) but type assertion yields 1 or 2 value(s)\n", len(d.Lhs))
				r.exit = exitStatus{code: 2}
				return
			}
			values, source, err := r.bashPPTypeAssert(assert, len(d.Lhs) == 2)
			if err != nil {
				r.errf("%v\n", err)
				// A one-result failed assertion is a language-level panic in Go.
				// Keep it across the typed function boundary instead of allowing
				// the enclosing command invocation to clear a plain status code.
				r.exit = exitStatus{code: 2}
				r.exit.fatal(ExitStatus(2))
				return
			}
			if r.exit.code != 0 {
				return
			}
			r.bashPPDeclareName(d.Lhs[0].Value, expand.Variable{Set: true, Kind: expand.String, Str: values[0]})
			target := r.bashPPScope.lookup(d.Lhs[0].Value)
			if target != nil && source != nil {
				target.vr = source.vr
				target.typeName = source.typeName
				target.declType = source.declType
				target.pointer, target.nilPointer, target.pointerValue = source.pointer, source.nilPointer, source.pointerValue
				target.object, target.valueMeta = source.object, source.valueMeta
				target.interfaceValue = source.interfaceValue
			}
			if len(d.Lhs) == 2 {
				r.bashPPDeclareName(d.Lhs[1].Value, expand.Variable{Set: true, Kind: expand.String, Str: values[1]})
			}
			return
		}
		if len(d.Lhs) != 1 {
			r.errf("assignment mismatch: %d variable(s) but 1 value(s)\n", len(d.Lhs))
			r.exit = exitStatus{code: 2}
			return
		}
		if cell, handled, err := r.goSourceNilValueCell(d.Expr); handled {
			if err != nil {
				r.exit.fatal(err)
				return
			}
			r.bashPPDeclareName(d.Lhs[0].Value, cell.vr)
			if target := r.bashPPScope.lookup(d.Lhs[0].Value); target != nil {
				*target = *cell
			}
			return
		}
		if r.bashPPBindPointerExpr(d.Lhs[0].Value, d.Expr) {
			return
		}
		// `bs := []byte(s)`: a conversion whose target is a collection binds the
		// slice it produces rather than a scalar spelling of it; see
		// bashPPConvertCollectionCell in bashpp_collection_convert.go. Scalar
		// conversions report false and stay on the path below.
		if conv, ok := d.Expr.(*syntax.BashPPConvertExpr); ok && len(d.Lhs) == 1 {
			cell, handled, err := r.bashPPConvertCollectionCell(conv)
			if err != nil {
				r.errf("%s%v\n", r.bashErrPrefix(conv.Pos()), err)
				r.exit = exitStatus{code: 2}
				return
			}
			if handled {
				name := d.Lhs[0].Value
				r.bashPPDeclareName(name, cell.vr)
				target := r.bashPPScope.lookup(name)
				*target = *cell
				target.object = &bashPPObjectIdentity{owner: name, collection: cell.valueMeta}
				return
			}
		}
		if lit, ok := d.Expr.(*syntax.BashPPCompositeLit); ok {
			if len(d.Lhs) != 1 {
				r.errf("assignment mismatch: %d variable(s) but 1 value(s)\n", len(d.Lhs))
				r.exit = exitStatus{code: 2}
				return
			}
			value, meta, err := r.bashPPEvalComposite(lit, nil)
			if err != nil {
				// A composite element may reach the dependency — a channel, a
				// native constructor — and a cancelled task group aborts that
				// evaluation with the teardown sentinel rather than with a
				// diagnostic about the program. Reporting it would put a line
				// on stderr that the original Go never prints.
				if !errors.Is(err, errBashPPScalarInterrupted) {
					r.errf("%v\n", err)
				}
				r.exit = exitStatus{code: 2}
				return
			}
			name := d.Lhs[0].Value
			r.bashPPDeclareName(name, expand.NewObject(value))
			r.bashPPScope.lookup(name).object = &bashPPObjectIdentity{owner: name, collection: meta}
			r.bashPPScope.lookup(name).valueMeta = meta
			return
		}
		if _, ok := d.Expr.(*syntax.BashPPIndexExpr); ok {
			value, meta, err := r.bashPPReadExpr(d.Expr)
			if err != nil {
				r.errf("%v\n", err)
				r.exit = exitStatus{code: 2}
				return
			}
			name := d.Lhs[0].Value
			if meta != nil {
				value, meta = bashPPCopyArrayValue(value, meta)
				r.bashPPDeclareName(name, expand.NewObject(value))
				cell := r.bashPPScope.lookup(name)
				cell.object = &bashPPObjectIdentity{owner: name, collection: meta}
				if root, rootOK := bashPPCollectionRoot(d.Expr); rootOK {
					if source := r.bashPPScope.lookup(root); source != nil && source.object != nil {
						cell.object = source.object
					}
				}
				cell.valueMeta = meta
			} else {
				r.bashPPDeclareName(name, expand.Variable{Set: true, Kind: expand.String, Str: fmt.Sprint(value)})
			}
			return
		}
		if _, ok := d.Expr.(*syntax.BashPPSliceExpr); ok {
			value, meta, err := r.bashPPReadExpr(d.Expr)
			if err != nil {
				r.errf("%v\n", err)
				r.exit = exitStatus{code: 2}
				return
			}
			name := d.Lhs[0].Value
			r.bashPPDeclareName(name, expand.NewObject(value))
			cell := r.bashPPScope.lookup(name)
			cell.object = &bashPPObjectIdentity{owner: name, collection: meta}
			if root, rootOK := bashPPCollectionRoot(d.Expr); rootOK {
				if source := r.bashPPScope.lookup(root); source != nil && source.object != nil {
					cell.object = source.object
				}
			}
			cell.valueMeta = meta
			return
		}
		if _, ok := d.Expr.(*syntax.BashPPSelectorExpr); ok {
			value, meta, err := r.bashPPReadExpr(d.Expr)
			if err == nil {
				name := d.Lhs[0].Value
				if meta != nil && meta.interfaceValue != nil {
					r.bashPPDeclareName(name, expand.Variable{Set: true, Kind: expand.String})
					cell := r.bashPPScope.lookup(name)
					bashPPStoreCellValue(cell, value, meta)
					cell.declType = meta.typ
				} else if meta != nil {
					value, meta = bashPPCopyArrayValue(value, meta)
					r.bashPPDeclareName(name, expand.NewObject(value))
					cell := r.bashPPScope.lookup(name)
					cell.object = &bashPPObjectIdentity{owner: name, collection: meta}
					if root, rootOK := bashPPCollectionRoot(d.Expr); rootOK {
						if source := r.bashPPScope.lookup(root); source != nil && source.object != nil {
							cell.object = source.object
						}
					}
					cell.valueMeta = meta
				} else {
					r.bashPPDeclareName(name, expand.Variable{Set: true, Kind: expand.String, Str: fmt.Sprint(value)})
				}
				return
			}
			if strings.Contains(err.Error(), "BASHPP-ENIL-DEREF") || strings.Contains(err.Error(), "BASHPP-ESELECTOR-AMBIGUOUS") {
				r.errf("%v\n", err)
				r.exit = exitStatus{code: 2}
				return
			}
			// A selector which is not rooted in a structured value remains a
			// method value candidate and is handled below.
			if len(d.MethodValue) == 0 {
				r.errf("%v\n", err)
				r.exit = exitStatus{code: 2}
				return
			}
			r.bashPPShortDeclMethodValue(d)
			return
		}
		var source *bashPPCell
		if ident, ok := d.Expr.(*syntax.BashPPIdent); ok {
			source = r.bashPPScope.lookup(ident.Name.Value)
			if vr := r.lookupVar(ident.Name.Value); vr.IsSet() && vr.Kind == expand.Object {
				if source != nil && source.object != nil && bashPPValueMeta(bashPPCellMeta(source)) {
					value, meta := bashPPCopyArrayValue(vr.Obj, bashPPCellMeta(source))
					vr.Obj = value
					r.bashPPDeclareName(d.Lhs[0].Value, vr)
					target := r.bashPPScope.lookup(d.Lhs[0].Value)
					target.object = source.object
					target.valueMeta = meta
					target.typeName = source.typeName
					return
				}
				r.bashPPDeclareName(d.Lhs[0].Value, vr)
				target := r.bashPPScope.lookup(d.Lhs[0].Value)
				if source != nil && target != nil {
					target.object = source.object
					target.valueMeta = source.valueMeta
					target.channel, target.channelOwner = source.channel, source.channelOwner
					target.typeName = source.typeName
					target.declType = source.declType
					target.pointer, target.nilPointer, target.pointerValue = source.pointer, source.nilPointer, source.pointerValue
					target.interfaceValue = source.interfaceValue
				}
				return
			}
		}
		value, err := r.bashPPEvalScalarExpr(d.Expr)
		if err != nil {
			if errors.Is(err, errBashPPScalarInterrupted) {
				return
			}
			r.errf("%v\n", err)
			r.exit = exitStatus{code: 2}
			return
		}
		r.bashPPDeclareName(d.Lhs[0].Value, expand.Variable{Set: true, Kind: expand.String, Str: bashPPScalarString(value.value)})
		target := r.bashPPScope.lookup(d.Lhs[0].Value)
		if target != nil {
			target.scalarKind = value.value.Kind()
			target.typeName = value.typ
			if source != nil {
				target.object = source.object
				target.channel, target.channelOwner = source.channel, source.channelOwner
				if source.channel != nil {
					target.declType = source.declType
				}
			}
		}
		return
	}
	if d.MakeChan != nil {
		r.bashPPMakeChan(ctx, d)
		return
	}
	if d.Recv != nil {
		r.bashPPReceive(ctx, d.Recv, d.Lhs)
		return
	}
	// `greet := func(who string) { … }` binds the FUNCTION, not a call's
	// results, so it is answered before the call and tuple paths: there is one
	// value, it is the closure, and it captures the scope at this point.
	if d.FuncLit != nil {
		if len(d.Lhs) != 1 {
			r.errf("assignment mismatch: %d variable(s) but 1 value(s)\n", len(d.Lhs))
			r.exit = exitStatus{code: 2}
			return
		}
		name := d.Lhs[0].Value
		if !syntax.BashPPValidIdent(name) {
			r.errf("invalid variable name: %q\n", name)
			r.exit = exitStatus{code: 2}
			r.bashPPShortFailureSeq++
			return
		}
		fn, vr := r.bashPPMakeClosure(d.FuncLit)
		fn.bound = name
		r.bashPPDeclareName(name, vr)
		return
	}
	if len(d.MethodValue) > 0 {
		r.bashPPShortDeclMethodValue(d)
		return
	}
	// `x := f(1)` / `a, b := f()` binds a typed function's results. It is
	// handled before the tuple arity check below because a call's single text
	// Rhs never matches a multi-name left-hand side; the real arity check is
	// against the function's declared results, done inside.
	if d.Call != nil {
		if r.bashPPEnumConstruct(d) {
			return
		}
		if cells, handled := r.goSourceErrorsAsTypeCells(d.Call); handled {
			if len(d.Lhs) != len(cells) {
				r.errf("assignment mismatch: %d variable(s) but %d value(s)\n", len(d.Lhs), len(cells))
				r.exit = exitStatus{code: 2}
				return
			}
			for i, name := range d.Lhs {
				if name.Value == "_" {
					continue
				}
				r.bashPPDeclareName(name.Value, cells[i].vr)
				if target := r.bashPPScope.lookup(name.Value); target != nil {
					*target = *cells[i]
				}
			}
			return
		}
		// Native dependency handles resolve their own methods before local
		// language method lookup attempts to inspect interpreter type metadata.
		if r.bashPPBridgeShortDecl(ctx, d) {
			return
		}
		if fn, ok := r.bashPPLookupFunc(d.Call); ok {
			r.bashPPShortDeclCall(ctx, d, fn)
			return
		}
		// A Go conversion is spelled exactly like a call, so the Go front end
		// delivers `f := MyFloat(3)` as one. Evaluate it as the conversion it
		// is: the result then carries the defined type, and with it the method
		// set `f.Abs()` resolves against. Reaching the call path instead left
		// the value an untyped string with no selector path at all.
		if conv, ok := r.bashPPShortDeclConversion(d); ok {
			value, err := r.bashPPEvalScalarExpr(conv)
			if err != nil {
				if !errors.Is(err, errBashPPScalarInterrupted) {
					r.errf("%v\n", err)
					r.exit = exitStatus{code: 2}
				}
				return
			}
			r.bashPPDeclareName(d.Lhs[0].Value, expand.Variable{Set: true, Kind: expand.String, Str: bashPPScalarString(value.value)})
			if target := r.bashPPScope.lookup(d.Lhs[0].Value); target != nil {
				target.scalarKind = value.value.Kind()
				target.typeName = value.typ
			}
			return
		}
		// `err := recover()` is the spelling a recovering defer is written
		// with, so the predeclared functions bind their results here exactly
		// as a declared function's do.
		if name := bashPPPredeclaredCall(d.Call); name != "" {
			if bashPPValueBuiltin(name) {
				result, produced := r.bashPPRunValueBuiltin(name, d.Call)
				r.bashPPBindBuiltinResult(d, result, produced)
			} else {
				r.bashPPShortDeclPredeclared(d, name)
			}
			return
		}
		// The typed process boundary's inbound half: `r, err := run(...)`,
		// `out, err := capture(...)` (predeclared like panic/recover), then
		// the explicit `v, err := json.Decode(...)`, which must be answered
		// before the generic imported-selector delegation hands it to the
		// toolchain. See bashpp_capture.go.
		if r.bashPPShortDeclCapture(ctx, d) {
			return
		}
		if r.bashPPShortDeclDecode(ctx, d) {
			return
		}
		if r.bashPPShortDeclImported(ctx, d) {
			return
		}
	}
	if len(d.Lhs) != 1 && len(d.Lhs) != len(d.Rhs) {
		r.errf("assignment mismatch: %d variable(s) but %d value(s)\n",
			len(d.Lhs), len(d.Rhs))
		r.exit = exitStatus{code: 2}
		return
	}
	if len(d.Lhs) == 1 {
		name := d.Lhs[0].Value
		if !syntax.BashPPValidIdent(name) {
			r.errf("invalid variable name: %q\n", name)
			r.exit = exitStatus{code: 2}
			return
		}
		if len(d.Rhs) == 1 {
			sourceName := bashPPWordSource(d.Rhs[0])
			if syntax.BashPPValidIdent(sourceName) {
				source := r.bashPPScope.lookup(sourceName)
				if source != nil && source.vr.Kind == expand.Object {
					value, meta := source.vr.Obj, bashPPCellMeta(source)
					identity := source.object
					if bashPPValueMeta(meta) {
						value, meta = bashPPCopyArrayValue(value, meta)
						identity = source.object
					}
					vr := expand.NewObject(value)
					r.bashPPDeclareName(name, vr)
					cell := r.bashPPScope.lookup(name)
					if identity == nil {
						identity = &bashPPObjectIdentity{owner: name}
					}
					cell.object = identity
					cell.valueMeta = meta
					return
				}
				if source != nil && source.interfaceValue != nil {
					r.bashPPDeclareName(name, source.vr)
					r.bashPPScope.lookup(name).interfaceValue = source.interfaceValue
					return
				}
			}
		}
		vr := r.bashPPValueInRegion(ctx, d.Rhs, d.GoRegion)
		if r.exit.code != 0 {
			return
		}
		r.bashPPDeclareName(name, vr)
		if cell := r.bashPPScope.lookup(name); cell != nil && len(d.Rhs) == 1 {
			cell.scalarKind = bashPPWordScalarKind(d.Rhs[0], vr)
		}
		if len(d.Rhs) == 1 {
			if channel, owner := r.bashPPDirectChannel(d.Rhs[0]); channel != nil {
				cell := r.bashPPScope.lookup(name)
				cell.channel, cell.channelOwner = channel, owner
				if source := r.bashPPScope.lookup(bashPPWordSource(d.Rhs[0])); source != nil {
					cell.declType = source.declType
				}
			}
		}
		return
	}
	values := make([]expand.Variable, len(d.Rhs))
	for i := range d.Rhs {
		values[i] = r.bashPPValueInRegion(ctx, d.Rhs[i:i+1], d.GoRegion)
		if r.exit.code != 0 {
			return
		}
	}
	for i, lhs := range d.Lhs {
		r.bashPPDeclareName(lhs.Value, values[i])
		if r.exit.code != 0 {
			return
		}
		if cell := r.bashPPScope.lookup(lhs.Value); cell != nil {
			cell.scalarKind = bashPPWordScalarKind(d.Rhs[i], values[i])
		}
	}
}

func bashPPWordScalarKind(word *syntax.Word, value expand.Variable) constant.Kind {
	if word != nil && len(word.Parts) == 1 {
		switch word.Parts[0].(type) {
		case *syntax.SglQuoted, *syntax.DblQuoted:
			return constant.String
		case *syntax.Lit:
			return bashPPScalarFromString(value.Str).value.Kind()
		}
	}
	return constant.String
}

type bashPPShortDeclTxn struct {
	parent    *bashPPShortDeclTxn
	scope     *bashPPScope
	entries   map[string]*bashPPCell
	cells     map[*bashPPCell]bashPPCell
	newName   bool
	expected  int
	bound     map[string]bool
	positions map[string]syntax.Pos
	names     []string
	failed    bool
}

func (r *Runner) bashPPBeginShortDecl(d *syntax.BashPPShortDecl) (*bashPPShortDeclTxn, bool) {
	seen := make(map[string]bool, len(d.Lhs))
	txn := &bashPPShortDeclTxn{
		parent:    r.bashPPShortTxn,
		scope:     r.bashPPScope,
		entries:   make(map[string]*bashPPCell, len(r.bashPPScope.entries)),
		cells:     make(map[*bashPPCell]bashPPCell, len(r.bashPPScope.entries)),
		bound:     make(map[string]bool, len(d.Lhs)),
		positions: make(map[string]syntax.Pos, len(d.Lhs)),
	}
	for name, cell := range r.bashPPScope.entries {
		txn.entries[name] = cell
		txn.cells[cell] = *cell
	}
	for _, lhs := range d.Lhs {
		name := lhs.Value
		if !syntax.BashPPValidIdent(name) {
			r.errf("%sinvalid variable name: %q\n", r.bashErrPrefix(lhs.Pos()), name)
			r.exit = exitStatus{code: 2}
			return nil, false
		}
		if name == "_" {
			continue
		}
		txn.expected++
		if seen[name] {
			r.errf("%s%s repeated on left side of :=\n", r.bashErrPrefix(lhs.Pos()), name)
			r.exit = exitStatus{code: 2}
			r.bashPPShortFailureSeq++
			return nil, false
		}
		seen[name] = true
		txn.positions[name] = lhs.Pos()
		txn.names = append(txn.names, name)
		if _, exists := r.bashPPScope.entries[name]; !exists {
			txn.newName = true
		}
	}
	r.bashPPShortTxn = txn
	return txn, true
}

func (r *Runner) bashPPRollbackShortDecl(txn *bashPPShortDeclTxn) {
	for cell, before := range txn.cells {
		*cell = before
	}
	txn.scope.entries = txn.entries
}

func (r *Runner) bashPPEndShortDecl(txn *bashPPShortDeclTxn, pos syntax.Pos) {
	r.bashPPShortTxn = txn.parent
	incomplete := len(txn.bound) != txn.expected
	// A producer which diagnosed an ordinary binding failure may return before
	// touching any LHS. Mark that failure before an enclosing result-bearing
	// function can settle and clear status 2. Panic and hard-exit paths are
	// control transfers, not declaration diagnostics, and retain their own
	// unwind machinery.
	if incomplete && !txn.failed && r.exit.code == 2 && !r.exit.exiting && !r.exit.fatalExit && !r.bashPPPanicking() {
		txn.failed = true
	}
	if !txn.failed && len(txn.bound) == txn.expected {
		for _, name := range txn.names {
			if !txn.bound[name] {
				continue
			}
			cell, reused := txn.entries[name]
			if !reused {
				continue
			}
			before := txn.cells[cell]
			if err := r.bashPPValidateReusedShortValue(&before, cell); err != nil {
				r.errf("%s%v\n", r.bashErrPrefix(txn.positions[name]), err)
				txn.failed = true
				break
			}
			// := assigns an existing cell; it cannot replace that cell's
			// declared identity with metadata belonging to the producer.
			cell.declType, cell.typeName = before.declType, before.typeName
		}
	}
	if txn.failed || len(txn.bound) != txn.expected {
		r.bashPPRollbackShortDecl(txn)
		if txn.failed {
			r.exit = exitStatus{code: 2}
			r.bashPPShortFailureSeq++
		}
		return
	}
	if !txn.newName {
		r.bashPPRollbackShortDecl(txn)
		r.errf("%sBASHPP-ESHORT-NONEW: no new variables on left side of :=\n", r.bashErrPrefix(pos))
		r.exit = exitStatus{code: 2}
		r.bashPPShortFailureSeq++
	}
}

func (r *Runner) bashPPValidateReusedShortValue(target, candidate *bashPPCell) error {
	if target.declType == nil {
		return nil
	}
	if r.bashPPGoSource && candidate.vr.Kind == expand.Object {
		if native, ok := candidate.vr.Obj.(*bashPPBridgeValue); ok && native != nil {
			value, meta, err := r.goSourceNativeAssignedValue(*native, target.declType)
			if err != nil {
				return err
			}
			candidate.vr.Obj = value
			candidate.valueMeta = meta
			candidate.declType = target.declType
			return nil
		}
	}
	actual := candidate.declType
	if actual == nil && candidate.typeName != "" {
		actual = &syntax.BashPPNamedType{Name: &syntax.Lit{Value: candidate.typeName}}
		if candidate.pointer {
			actual = &syntax.BashPPPointerType{Element: actual}
		}
	}
	if actual == nil && candidate.valueMeta != nil {
		actual = candidate.valueMeta.typ
	}
	if actual != nil {
		if !r.bashPPTypeAssignable(actual, target.declType) {
			return fmt.Errorf("BASHPP-EASSIGN-TYPE: cannot assign %s to %s", bashPPTypeText(actual), bashPPTypeText(target.declType))
		}
		return nil
	}
	shape, ok := r.bashPPUnderlyingType(target.declType).(*syntax.BashPPNamedType)
	if !ok || !bashPPBuiltinType(shape.Name.Value) || candidate.vr.Kind != expand.String {
		return fmt.Errorf("BASHPP-EASSIGN-TYPE: untyped result is not assignable to %s", bashPPTypeText(target.declType))
	}
	value := bashPPScalarFromString(candidate.vr.Str)
	switch candidate.scalarKind {
	case constant.String:
		value = bashPPScalar{value: constant.MakeString(candidate.vr.Str)}
	case constant.Bool:
		value = bashPPScalar{value: constant.MakeBool(candidate.vr.Str == "true")}
	case constant.Int:
		value.value = constant.MakeFromLiteral(candidate.vr.Str, token.INT, 0)
	case constant.Complex:
		value.value = bashPPParseComplex(candidate.vr.Str)
	case constant.Float:
		value.value = constant.MakeFromLiteral(candidate.vr.Str, token.FLOAT, 0)
	}
	if !bashPPUntypedScalarAssignable(shape.Name.Value, value.value) {
		return fmt.Errorf("BASHPP-EASSIGN-TYPE: cannot assign %s to %s", value.value.Kind(), bashPPTypeText(target.declType))
	}
	converted, err := r.bashPPConvertScalar(shape.Name.Value, value)
	if err != nil {
		return err
	}
	candidate.vr.Str = bashPPScalarString(converted.value)
	return nil
}

func (r *Runner) bashPPShortDeclMethodValue(d *syntax.BashPPShortDecl) {
	if len(d.Lhs) != 1 {
		r.errf("assignment mismatch: %d variable(s) but 1 value(s)\n", len(d.Lhs))
		r.exit = exitStatus{code: 2}
		return
	}
	call := &syntax.BashPPCall{Fun: d.MethodValue}
	fn, ok := r.bashPPLookupFunc(call)
	if !ok {
		if r.exit.code == 0 {
			r.errf("bash++: selector %s.%s is not a method value\n", d.MethodValue[0].Value, d.MethodValue[len(d.MethodValue)-1].Value)
			r.exit.code = 2
		}
		return
	}
	vr := r.bashPPStoreFunc(fn)
	fn.bound = d.Lhs[0].Value
	r.bashPPDeclareName(d.Lhs[0].Value, vr)
}

func (r *Runner) bashPPDirectChannel(w *syntax.Word) (*bashPPChannel, *bashPPConcurrent) {
	if w == nil || len(w.Parts) != 1 || r.bashPPScope == nil {
		return nil, nil
	}
	lit, ok := w.Parts[0].(*syntax.Lit)
	if !ok || !syntax.BashPPValidIdent(lit.Value) {
		return nil, nil
	}
	cell := r.bashPPScope.lookup(lit.Value)
	if cell == nil {
		return nil, nil
	}
	return cell.channel, cell.channelOwner
}

// bashPPEnumConstruct evaluates `v := Color(Member)`. Enum values keep their
// member spelling in the ordinary shell cell while the lexical cell carries
// the named type, matching the existing named-scalar representation.
func (r *Runner) bashPPEnumConstruct(d *syntax.BashPPShortDecl) bool {
	if d.Call == nil || len(d.Call.Fun) != 1 || len(d.Lhs) != 1 {
		return false
	}
	name := d.Call.Fun[0].Value
	typ, ok := r.bashPPTypes[name]
	if !ok || typ.underlying != "enum" {
		return false
	}
	args := r.bashPPCallArgValues(d.Call)
	value := ""
	if len(args) == 1 {
		value = args[0]
	}
	valid := false
	for _, member := range typ.members {
		if member == value {
			valid = true
			break
		}
	}
	if !valid {
		r.errf("BASHPP-EENUM-VALUE: %s is not a member of %s\n", value, name)
		r.exit = exitStatus{code: 2}
		return true
	}
	r.bashPPDeclareName(d.Lhs[0].Value, expand.Variable{Set: true, Kind: expand.String, Str: value})
	if cell := r.bashPPScope.lookup(d.Lhs[0].Value); cell != nil {
		cell.typeName = name
	}
	return true
}

func (r *Runner) bashPPSwitch(ctx context.Context, sw *syntax.BashPPSwitch) {
	if !r.objectsEnabled() {
		r.errf("bash++ switch evaluated with extensions disabled\n")
		r.exit = exitStatus{code: 2}
		return
	}
	if r.bashPPScope == nil {
		r.bashPPScope = newBashPPScope(nil)
	}
	leaveSwitch := r.bashPPPushScope()
	defer leaveSwitch()
	r.exit.clear()
	if sw.Init != nil {
		if !sw.TypeSwitch {
			r.cmd(ctx, sw.Init)
		}
		if !r.exit.ok() {
			return
		}
	}
	if sw.TypeSwitch {
		r.bashPPTypeSwitch(ctx, sw)
		return
	}
	var tag bashPPScalar
	var err error
	if sw.Tag == nil {
		tag.value = constant.MakeBool(true)
	} else {
		tag, err = r.bashPPEvalScalarExpr(sw.Tag)
		if err != nil {
			if errors.Is(err, errBashPPScalarInterrupted) {
				return
			}
			r.errf("%v\n", err)
			r.exit = exitStatus{code: 2}
			return
		}
	}
	cases, err := r.bashPPValidateSwitchCases(sw, tag)
	if err != nil {
		if errors.Is(err, errBashPPScalarInterrupted) {
			return
		}
		r.errf("%v\n", err)
		r.exit = exitStatus{code: 2}
		return
	}
	selected := -1
	defaultArm := -1
	for armIndex, arm := range sw.Arms {
		if len(arm.Exprs) == 0 {
			defaultArm = armIndex
			continue
		}
		for _, candidate := range cases[armIndex] {
			match, compareErr := bashPPSwitchEqual(tag, candidate)
			if compareErr != nil {
				r.errf("%v\n", compareErr)
				r.exit = exitStatus{code: 2}
				return
			}
			if match {
				selected = armIndex
				break
			}
		}
		if selected == armIndex {
			break
		}
	}
	if selected < 0 {
		selected = defaultArm
	}
	if selected < 0 {
		return
	}
	for armIndex := selected; armIndex < len(sw.Arms); armIndex++ {
		leaveArm := r.bashPPPushScope()
		r.stmts(ctx, sw.Arms[armIndex].Stmts)
		leaveArm()
		switch r.bashPPBranch {
		case bashPPBranchBreak:
			if r.bashPPBranchEscapesEligible() {
				return
			}
			r.bashPPClearBranch()
			return
		case bashPPBranchFallthrough:
			r.bashPPClearBranch()
			continue
		default:
			return
		}
	}
}

// bashPPValidateSwitchCases is deliberately separate from arm selection.
// Every case expression is evaluated and type-checked in source order before
// an arm can run, while the returned values ensure selection never evaluates
// an expression a second time.
func (r *Runner) bashPPValidateSwitchCases(sw *syntax.BashPPSwitch, tag bashPPScalar) ([][]bashPPScalar, error) {
	cases := make([][]bashPPScalar, len(sw.Arms))
	var constants []bashPPScalar
	for armIndex, arm := range sw.Arms {
		for _, expr := range arm.Exprs {
			candidate, err := r.bashPPSwitchCaseScalar(tag, expr)
			if err != nil {
				return nil, err
			}
			if sw.Tag == nil && candidate.value.Kind() != constant.Bool {
				return nil, fmt.Errorf("BASHPP-ESWITCH-TYPE: tagless switch case must be boolean, got %s", candidate.value.Kind())
			}
			if err := bashPPSwitchComparable(tag, candidate); err != nil {
				return nil, err
			}
			if r.bashPPSwitchConstantExpr(tag, expr) {
				for _, previous := range constants {
					equal, err := bashPPSwitchEqual(previous, candidate)
					if err == nil && equal {
						return nil, fmt.Errorf("BASHPP-ESWITCH-DUPLICATE: duplicate case constant %s", bashPPSwitchConstantText(candidate))
					}
				}
				constants = append(constants, candidate)
			}
			cases[armIndex] = append(cases[armIndex], candidate)
		}
	}
	return cases, nil
}

func (r *Runner) bashPPSwitchConstantExpr(tag bashPPScalar, expr syntax.BashPPExpr) bool {
	switch x := expr.(type) {
	case *syntax.BashPPBasicLit:
		return true
	case *syntax.BashPPIdent:
		if x.Name.Value == "true" || x.Name.Value == "false" {
			return true
		}
		if tag.typ != "" {
			for _, member := range r.bashPPTypes[tag.typ].members {
				if x.Name.Value == member {
					return true
				}
			}
		}
	case *syntax.BashPPParenExpr:
		return r.bashPPSwitchConstantExpr(tag, x.X)
	case *syntax.BashPPUnaryExpr:
		return r.bashPPSwitchConstantExpr(tag, x.X)
	case *syntax.BashPPBinaryExpr:
		return r.bashPPSwitchConstantExpr(tag, x.X) && r.bashPPSwitchConstantExpr(tag, x.Y)
	case *syntax.BashPPConvertExpr:
		return r.bashPPSwitchConstantExpr(tag, x.X)
	}
	return false
}

func bashPPSwitchConstantText(value bashPPScalar) string {
	if value.value.Kind() == constant.String {
		return strconv.Quote(constant.StringVal(value.value))
	}
	return value.value.ExactString()
}

func (r *Runner) bashPPSwitchCaseScalar(tag bashPPScalar, expr syntax.BashPPExpr) (bashPPScalar, error) {
	if tag.typ != "" {
		if typ := r.bashPPTypes[tag.typ]; typ.underlying == "enum" {
			if ident, ok := expr.(*syntax.BashPPIdent); ok {
				for _, member := range typ.members {
					if ident.Name.Value == member {
						return bashPPScalar{value: constant.MakeString(member), typ: tag.typ}, nil
					}
				}
			}
		}
	}
	return r.bashPPEvalScalarExpr(expr)
}

func bashPPSwitchComparable(tag, candidate bashPPScalar) error {
	if tag.typ != "" && candidate.typ != "" && tag.typ != candidate.typ {
		return fmt.Errorf("BASHPP-ESWITCH-TYPE: case expression type %s does not match switch tag type %s", candidate.typ, tag.typ)
	}
	tagKind, candidateKind := tag.value.Kind(), candidate.value.Kind()
	numeric := func(kind constant.Kind) bool { return kind == constant.Int || kind == constant.Float }
	if tagKind != candidateKind && !(numeric(tagKind) && numeric(candidateKind)) {
		return fmt.Errorf("BASHPP-ESWITCH-TYPE: case expression type %s does not match switch tag type %s", candidateKind, tagKind)
	}
	return nil
}

func bashPPSwitchEqual(tag, candidate bashPPScalar) (bool, error) {
	if err := bashPPSwitchComparable(tag, candidate); err != nil {
		return false, err
	}
	return bashPPCompareScalar(tag.value, token.EQL, candidate.value)
}

// bashPPShortDeclPredeclared binds the results of a predeclared call to the
// names on the left of `:=`, preserving the status the call set — which for
// `recover` is how a script tells "recovered the empty string" from "there was
// nothing to recover".
func (r *Runner) bashPPShortDeclPredeclared(d *syntax.BashPPShortDecl, name string) {
	// A call that produced no values leaves nothing to bind — `panic(v)`
	// abandons the declaration along with the rest of the statement, and a
	// diagnosed call has already reported itself.
	results, ok := r.bashPPPredeclared(name, d.Call, r.bashPPCallArgValues(d.Call))
	if !ok {
		return
	}
	if len(d.Lhs) != len(results) {
		r.errf("assignment mismatch: %d variable(s) but %d value(s)\n",
			len(d.Lhs), len(results))
		r.exit = exitStatus{code: 2}
		return
	}
	status := r.exit
	for i, lhs := range d.Lhs {
		if !syntax.BashPPValidIdent(lhs.Value) {
			r.errf("invalid variable name: %q\n", lhs.Value)
			r.exit = exitStatus{code: 2}
			return
		}
		// recover's static type is `interface{}`, so `r := recover()` binds an
		// interface value, not a bare scalar. That is what lets `r != nil` hold
		// for a recovered payload — even the empty string — and `r == nil` hold
		// when there was nothing to recover. Status 0 from the call is the same
		// "recovered" signal `$?` carries; see [Runner.bashPPPredeclared].
		if name == "recover" && r.bashPPGoSource {
			r.bashPPDeclareRecoverInterface(lhs.Value, results[i], status.code == 0)
			continue
		}
		r.bashPPDeclareName(lhs.Value, expand.Variable{Set: true, Kind: expand.String, Str: results[i]})
	}
	r.exit = status
}

// bashPPDeclareRecoverInterface binds name to the interface value recover
// returns. A recovered payload becomes a non-nil interface whose dynamic type is
// string (the only shape a panic value carries in this engine), so it compares
// unequal to nil while still interpolating as its text. Nothing to recover binds
// the nil interface, which compares equal to nil.
func (r *Runner) bashPPDeclareRecoverInterface(name, value string, recovered bool) {
	vr := expand.Variable{Set: true, Kind: expand.String, Str: value}
	r.bashPPDeclareName(name, vr)
	cell := r.bashPPScope.lookup(name)
	if cell == nil {
		return
	}
	if !recovered {
		cell.interfaceValue = &bashPPInterfaceValue{nilIface: true}
		return
	}
	stringType := &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "string"}}
	inner := &bashPPCell{vr: vr, scalarKind: constant.MakeString(value).Kind(), declType: stringType, typeName: "string"}
	cell.interfaceValue = &bashPPInterfaceValue{dynamic: stringType, cell: inner}
	cell.scalarKind = inner.scalarKind
}

// bashPPDeclareName binds one name in the innermost block, reporting a
// redeclaration the way the keyword forms do. `:=` differs from `var` in Go by
// permitting a redeclaration when at least one name on the left is new, which
// is a rule about the whole left-hand side and so belongs to the site that has
// one; this is only the per-name half the two forms share.
func (r *Runner) bashPPDeclareName(name string, vr expand.Variable) {
	if name == "_" {
		return
	}
	if prev := r.writeEnv.Get(name); prev.Exported {
		vr.Exported = true
	}
	for txn := r.bashPPShortTxn; txn != nil; txn = txn.parent {
		if txn.scope != r.bashPPScope {
			continue
		}
		if cell, exists := txn.scope.entries[name]; exists {
			if cell.constant || cell.vr.ReadOnly {
				r.errf("%s%s: cannot assign to constant\n", r.bashErrPrefix(txn.positions[name]), name)
				r.exit = exitStatus{code: 2}
				txn.failed = true
				return
			}
			// The producer decorates a fresh candidate cell. The transaction
			// validates that candidate against the saved target identity and
			// restores the target identity only at commit.
			*cell = bashPPCell{vr: vr}
			txn.bound[name] = true
			return
		}
		_ = txn.scope.declare(name, vr, false)
		txn.bound[name] = true
		return
	}
	if err := r.bashPPScope.declare(name, vr, false); err != nil {
		r.errf("%v\n", err)
		r.exit = exitStatus{code: 2}
	}
}

// bashPPValue turns an unevaluated right-hand side into a variable.
//
// A scalar stays a STRING, deliberately. The design of record settles this:
// making `x := 42` produce an object would mean `echo $x` had to unwrap it,
// every arithmetic context had to unwrap it, and every external command had to
// be handed something. A shell variable holding "42" already behaves correctly
// in all three. Objects earn their keep for structured values, which have no
// faithful string form; they cost more than they return for scalars.
func (r *Runner) bashPPValue(ctx context.Context, words []*syntax.Word) expand.Variable {
	return r.bashPPValueInRegion(ctx, words, false)
}

func (r *Runner) bashPPValueInRegion(_ context.Context, words []*syntax.Word, goRegion bool) expand.Variable {
	if len(words) == 1 {
		// `x, y := <-c, <-c` reaches this site one operand at a time, and a Go
		// receive word is an operation rather than a literal. See
		// bashpp_chan_value.go.
		if vr, handled := r.bashPPGoReceiveWordValue(words[0]); handled {
			return vr
		}
	}
	switch len(words) {
	case 0:
		// A bare declaration: `var x int`. The zero value is the empty
		// string. Set is true all the same — a declared identifier holds its
		// zero value, it is not absent — so `${x-fallback}` yields the empty
		// string rather than the fallback, and execEnv does not treat the
		// binding as an unset name to be scrubbed from the child's
		// environment.
		return expand.Variable{Set: true, Kind: expand.String, Str: ""}
	case 1:
		return expand.Variable{Set: true, Kind: expand.String, Str: r.literal(words[0])}
	default:
		strs := make([]string, len(words))
		for i, w := range words {
			strs[i] = r.literal(w)
		}
		return expand.Variable{Set: true, Kind: expand.Indexed, List: strs}
	}
}

// bashPPCall evaluates a Go call in command position.
//
// A call resolves in order: a typed function declared in this session (P3-A),
// then an imported selector via the eval toolchain, and finally an honest
// diagnostic for a shape no phase implements. Every shape reaching this node
// is Class R — already a bash syntax error — so a diagnostic takes nothing
// away from any working script, which is exactly why a diagnostic is
// permitted here and forbidden on a Class E shape.
func (r *Runner) bashPPCall(ctx context.Context, c *syntax.BashPPCall) {
	if r.bashPPTestingCall(c) {
		return
	}
	// `wg.Go(f)` retains f past the call, so it can never be a synchronous
	// dependency callback. It is answered as its own bridge operation, on a
	// receiver the dependency authenticated as a sync.WaitGroup, before the
	// call would be prepared as a native request. See gosource_waitgroup.go.
	if r.goSourceWaitGroupGo(ctx, c) {
		return
	}
	if r.bashPPBridgeHandles(c) {
		if _, err := r.bashPPBridgeCall(ctx, c); err != nil && !r.bashPPPanicking() {
			r.exit.fatal(err)
		}
		return
	}
	if c.CalleeExpr != nil && !(r.bashPPGoSource && r.bashPPGoSourcePin != nil && r.bashPPGoSourcePin.call == c) {
		r.exit.fatal(fmt.Errorf("%sgosource: computed call runtime is not implemented", r.bashErrPrefix(c.Pos())))
		return
	}
	// A call to a typed function declared in this session runs the function.
	// It is checked before the external eval toolchain so a user's own `func`
	// always wins over a same-named tool binding.
	if fn, ok := r.bashPPLookupFunc(c); ok {
		args, ok := r.bashPPCallValues(c, fn)
		if !ok {
			return
		}
		r.bashPPInvoke(ctx, fn, args)
		return
	}
	if r.exit.code != 0 {
		return
	}
	// A Go region spells `close(ch)` as an ordinary call rather than as the
	// shell-recognized channel form. See bashpp_chan_value.go.
	if r.bashPPGoSourceChanCall(c) {
		return
	}
	// `panic` and `recover` are predeclared, so they answer only where the
	// session declared nothing of that name — the lookup above already had its
	// chance, exactly as a Go declaration shadows a predeclared identifier.
	// They are dialect state like every other extension, so POSIX mode and the
	// Classic dialect see the same "not implemented" diagnostic as any other
	// Go form, never a panic.
	if r.bashPPEnabled() && !r.PosixMode() {
		if name := bashPPPredeclaredCall(c); name != "" {
			if bashPPValueBuiltin(name) {
				r.bashPPRunValueBuiltin(name, c)
			} else {
				r.bashPPPredeclared(name, c, r.bashPPCallArgValues(c))
			}
			return
		}
		if r.bashPPCaptureCommandPosition(c) {
			return
		}
		if len(c.Fun) >= 1 {
			r.bashPPEvalSelector(ctx, c, nil)
			return
		}
	}
	name := ""
	for i, lit := range c.Fun {
		if i > 0 {
			name += "."
		}
		name += lit.Value
	}
	r.errf("bash++: %s(...) is recognized but not implemented in this phase\n", name)
	r.exit = exitStatus{code: 127}
}

func (r *Runner) bashPPCommandCall(ctx context.Context, c *syntax.BashPPCommandCall) {
	fn, ok := r.bashPPLookupFunc(c.Call)
	if !ok {
		if r.exit.code == 0 {
			r.errf("bash++: nested call is not a declared function\n")
			r.exit = exitStatus{code: 127}
		}
		return
	}
	args, ok := r.bashPPCallValues(c.Call, fn)
	if !ok {
		return
	}
	results := r.bashPPInvoke(ctx, fn, args)
	if r.exit.code != 0 || r.exit.exiting || r.exit.returning {
		return
	}
	words := append([]*syntax.Word(nil), c.Before...)
	for _, result := range results {
		words = append(words, &syntax.Word{Parts: []syntax.WordPart{&syntax.SglQuoted{Value: result}}})
	}
	r.cmd(ctx, &syntax.CallExpr{Args: words})
}

// bashPPEvalSelector dispatches a call through the import evaluator.
//
// values is nil for a direct call, whose arguments the evaluator receives as
// the Go source the script wrote. A DEFERRED call passes the values it captured
// when the defer ran instead, rendered back as Go literals: Go fixes a deferred
// call's arguments at the defer, so re-reading the script's words as the frame
// unwinds would hand the package whatever the variables hold by then.
func (r *Runner) bashPPEvalSelector(ctx context.Context, c *syntax.BashPPCall, values []string) {
	req, err := r.bashPPEvalRequest()
	if err == nil {
		req.Selector = make([]string, len(c.Fun))
		for i, lit := range c.Fun {
			req.Selector[i] = lit.Value
		}
		if values != nil {
			req.Args = make([]string, len(values))
			for i, value := range values {
				req.Args[i] = strconv.Quote(value)
			}
		} else if r.bashPPGoSource {
			req.Args, err = r.bashPPGoSourceArguments(c)
			if err != nil {
				r.exit.fatal(err)
				return
			}
		} else {
			req.Args = make([]string, len(c.Args))
			for i, arg := range c.Args {
				var b bytes.Buffer
				_ = syntax.NewPrinter().Print(&b, arg)
				req.Args[i] = b.String()
			}
		}
		err = r.bashPPTools.eval.Call(ctx, req)
	}
	if err != nil {
		r.exit.fatal(err)
	}
}

func (r *Runner) bashPPIf(ctx context.Context, i *syntax.BashPPIf) {
	if !r.objectsEnabled() {
		r.errf("bash++ if evaluated with extensions disabled\n")
		r.exit = exitStatus{code: 2}
		return
	}
	if r.bashPPScope == nil {
		r.bashPPScope = newBashPPScope(nil)
	}
	leave := r.bashPPPushScope()
	defer leave()
	r.exit.clear()
	if i.InitStmt != nil || i.Init != nil {
		if i.InitStmt != nil {
			r.cmd(ctx, i.InitStmt)
		} else {
			r.bashPPShortDecl(ctx, i.Init)
		}
		if r.bashPPPanicking() {
			return
		}
		// `if r := recover(); r != nil` is the canonical recover idiom, and a
		// recover that found nothing to recover reports status 1. That status is
		// the recover's ANSWER, not a failed init, so — like the errexit contexts
		// elsewhere — an errexit-exempt status does not abort the if; the
		// condition is still evaluated. A genuine error or exit still does.
		if !r.exit.ok() && !r.exit.errexitExempt {
			return
		}
		r.exit.clear()
	}
	cond, err := r.bashPPEvalScalarExpr(i.Cond)
	if err != nil {
		if errors.Is(err, errBashPPScalarInterrupted) {
			return
		}
		r.errf("%v\n", err)
		r.exit = exitStatus{code: 2}
		return
	}
	if cond.value.Kind() != constant.Bool {
		r.errf("BASHPP-EIF-COND: if condition must be boolean, got %s\n", cond.value.Kind())
		r.exit = exitStatus{code: 2}
		return
	}
	if constant.BoolVal(cond.value) {
		r.cmd(ctx, i.Then)
	} else if i.Else != nil {
		r.cmd(ctx, i.Else)
	}
}

func (r *Runner) bashPPFor(ctx context.Context, loop *syntax.BashPPFor) {
	if !r.objectsEnabled() {
		r.errf("bash++ for evaluated with extensions disabled\n")
		r.exit = exitStatus{code: 2}
		return
	}
	if r.bashPPScope == nil {
		r.bashPPScope = newBashPPScope(nil)
	}
	leave := r.bashPPPushScope()
	defer leave()
	r.exit.clear()
	if loop.Init != nil {
		r.cmd(ctx, loop.Init)
		if !r.exit.ok() {
			return
		}
	}
	var iterationNames []string
	if decl, ok := loop.Init.(*syntax.BashPPShortDecl); ok {
		for _, lhs := range decl.Lhs {
			iterationNames = append(iterationNames, lhs.Value)
		}
	}
	for !r.stop(ctx) {
		if loop.Cond != nil {
			cond, err := r.bashPPEvalScalarExpr(loop.Cond)
			if err != nil {
				if errors.Is(err, errBashPPScalarInterrupted) {
					return
				}
				r.errf("%v\n", err)
				r.exit = exitStatus{code: 2}
				return
			}
			if cond.value.Kind() != constant.Bool {
				r.errf("BASHPP-EFOR-COND: for condition must be boolean, got %s\n", cond.value.Kind())
				r.exit = exitStatus{code: 2}
				return
			}
			if !constant.BoolVal(cond.value) {
				r.exit.clear()
				return
			}
		}
		r.cmd(ctx, loop.Body)
		if r.exit.exiting || r.exit.returning || r.exit.fatalExit || r.loopControlPending() {
			return
		}
		switch r.bashPPBranch {
		case bashPPBranchBreak:
			if r.bashPPBranchEscapesEligible() {
				return
			}
			r.bashPPClearBranch()
			return
		case bashPPBranchContinue:
			if r.bashPPBranchEscapesEligible() {
				return
			}
			r.bashPPClearBranch()
		case bashPPBranchFallthrough:
			panic("validated fallthrough escaped to Bash++ for")
		case bashPPBranchGoto:
			return
		}
		// Go 1.27 creates the next iteration's variable after the body and
		// initializes it from this iteration's value before running post.
		// Replacing the cell here leaves closures attached to the cell they
		// observed while the loop itself proceeds with the fresh one.
		for _, name := range iterationNames {
			if old := r.bashPPScope.entries[name]; old != nil {
				copyCell := *old
				r.bashPPScope.entries[name] = &copyCell
			}
		}
		if loop.Post != nil {
			r.cmd(ctx, loop.Post)
			if !r.exit.ok() {
				return
			}
		}
	}
}

func (r *Runner) bashPPForAssign(assign *syntax.BashPPForAssign) {
	if !r.objectsEnabled() || r.bashPPScope == nil {
		r.errf("bash++ scalar assignment evaluated with extensions disabled\n")
		r.exit = exitStatus{code: 2}
		return
	}
	cell := r.bashPPScope.lookup(assign.Name.Value)
	if cell == nil {
		r.errf("BASHPP-EASSIGN-UNDEFINED: undefined: %s\n", assign.Name.Value)
		r.exit = exitStatus{code: 2}
		return
	}
	if cell.constant {
		r.errf("BASHPP-EASSIGN-CONST: cannot assign to constant %s\n", assign.Name.Value)
		r.exit = exitStatus{code: 2}
		return
	}
	if cell.vr.Kind == expand.Object {
		r.errf("BASHPP-EASSIGN-TYPE: %s is not a scalar\n", assign.Name.Value)
		r.exit = exitStatus{code: 2}
		return
	}
	value, err := r.bashPPEvalScalarExpr(assign.Expr)
	if err != nil {
		if errors.Is(err, errBashPPScalarInterrupted) {
			return
		}
		r.errf("%v\n", err)
		r.exit = exitStatus{code: 2}
		return
	}
	cell.vr.Set = true
	cell.vr.Kind = expand.String
	cell.vr.Str = bashPPScalarString(value.value)
	cell.vr.List, cell.vr.Map, cell.vr.ListMap, cell.vr.ListSet = nil, nil, nil, nil
	r.exit.clear()
}

func (r *Runner) bashPPIncDec(stmt *syntax.BashPPIncDec) {
	if stmt.TargetWord != nil {
		if stmt.Target == nil {
			r.bashPPUpdateError(stmt.Pos(), "FORM", "unsupported increment or decrement target")
			return
		}
		one := &syntax.BashPPBasicLit{Value: &syntax.Lit{ValuePos: stmt.Op.Pos(), ValueEnd: stmt.Op.End(), Value: "1"}, Kind: "INT"}
		op := "+"
		if stmt.Op.Value == "--" {
			op = "-"
		}
		r.bashPPApplyUpdate(stmt.Target, op, one, stmt.Op.Pos())
		return
	}
	if r.bashPPScope == nil {
		r.errf("bash++ inc-dec evaluated with extensions disabled\n")
		r.exit = exitStatus{code: 2}
		return
	}
	cell := r.bashPPScope.lookup(stmt.Name.Value)
	if cell == nil {
		r.errf("BASHPP-EINCDEC-UNDEFINED: undefined: %s\n", stmt.Name.Value)
		r.exit = exitStatus{code: 2}
		return
	}
	if cell.constant {
		r.errf("BASHPP-EINCDEC-CONST: cannot assign to constant %s\n", stmt.Name.Value)
		r.exit = exitStatus{code: 2}
		return
	}
	value := bashPPScalarFromString(cell.vr.String())
	if value.value.Kind() != constant.Int && value.value.Kind() != constant.Float {
		r.errf("BASHPP-EINCDEC-TYPE: operator %s not defined on %s\n", stmt.Op.Value, value.value.Kind())
		r.exit = exitStatus{code: 2}
		return
	}
	op := token.ADD
	if stmt.Op.Value == "--" {
		op = token.SUB
	}
	result, err := bashPPBinaryOp(value.value, op, constant.MakeInt64(1))
	if err != nil {
		r.errf("%v\n", err)
		r.exit = exitStatus{code: 2}
		return
	}
	cell.vr.Set, cell.vr.Kind, cell.vr.Str = true, expand.String, bashPPScalarString(result)
	r.exit.clear()
}

// bashPPUnsupported is the shared response for a recognized Go form belonging
// to a phase that has not landed.
//
// It exists so the Class rule has ONE implementation instead of being restated
// at each call site: a Class R form may be diagnosed, because bash rejects it
// anyway; a Class E form must never be, because a script relying on today's
// shell meaning would break. Returning false means "fall back to shell".
func (r *Runner) bashPPUnsupported(class syntax.SiteClass, form string) bool {
	if class == syntax.ClassE {
		return false
	}
	r.errf("bash++: %s is recognized but not supported in this phase\n", form)
	r.exit = exitStatus{code: 2}
	return true
}
