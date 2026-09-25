package interp

// Sprint: #281; Story: #809; Story-ID: fac7e14af4a8

import (
	"bytes"
	"context"
	"fmt"
	"go/constant"
	"strconv"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

func (r *Runner) goSourceLocalFmtWriterCall(ctx context.Context, call *syntax.BashPPCall) ([]bashPPBridgeValue, bool, error) {
	if !r.bashPPGoSource || call == nil || call.Ellipsis.IsValid() || len(call.Fun) != 2 ||
		r.bashPPImports[call.Fun[0].Value] != "fmt" || len(call.ArgExprs) == 0 {
		return nil, false, nil
	}
	name := call.Fun[1].Value
	if name != "Fprint" && name != "Fprintln" && name != "Fprintf" {
		return nil, false, nil
	}
	writer, err := r.bashPPBridgeExpr(call.ArgExprs[0])
	if err != nil {
		return nil, true, err
	}
	if writer.Kind != "pointer" && writer.Kind != "struct" || writer.Type == "" {
		return nil, false, nil
	}
	typeName := bashPPLocalTypeName(writer.Type)
	typeName = trimLeadingStars(typeName)
	fn := r.bashPPMethods[typeName]["Write"]
	if fn == nil || fn.decl == nil || len(fn.params()) != 1 {
		return nil, false, nil
	}
	paramType := fn.params()[0].FieldTypeExpr
	if bashPPTypeText(paramType) != "[]byte" && bashPPTypeText(paramType) != "[]uint8" {
		return nil, false, nil
	}

	values := make([]any, 0, len(call.ArgExprs)-1)
	for _, expr := range call.ArgExprs[1:] {
		value, err := r.bashPPBridgeExpr(expr)
		if err != nil {
			return nil, true, err
		}
		plain, ok := goSourceFmtPlainValue(value)
		if !ok {
			return nil, false, nil
		}
		values = append(values, plain)
	}
	var buf bytes.Buffer
	switch name {
	case "Fprint":
		_, _ = fmt.Fprint(&buf, values...)
	case "Fprintln":
		_, _ = fmt.Fprintln(&buf, values...)
	case "Fprintf":
		if len(values) == 0 {
			return nil, false, nil
		}
		format, ok := values[0].(string)
		if !ok {
			return nil, false, nil
		}
		_, _ = fmt.Fprintf(&buf, format, values[1:]...)
	}
	written, err := r.goSourceCallLocalWrite(ctx, typeName, writer, buf.Bytes(), paramType)
	if err != nil {
		return nil, true, err
	}
	return []bashPPBridgeValue{
		{Kind: "int", Type: "int", Text: strconv.Itoa(written)},
		{Kind: "nil", Type: "error"},
	}, true, nil
}

func trimLeadingStars(s string) string {
	for len(s) > 0 && s[0] == '*' {
		s = s[1:]
	}
	return s
}

func goSourceFmtPlainValue(value bashPPBridgeValue) (any, bool) {
	if value.Callbacks || value.Function || value.Kind == "callback" || value.Kind == "handle" ||
		value.Kind == "pointer" || value.Kind == "struct" || value.Kind == "slice" ||
		value.Kind == "map" || value.Kind == "interface" {
		return nil, false
	}
	if !goSourceFmtBuiltinScalarType(value.Type) {
		return nil, false
	}
	scalar, err := value.scalar()
	if err != nil {
		if value.Kind == "nil" {
			return nil, true
		}
		return nil, false
	}
	if scalar.hasNonFinite {
		return scalar.nonFinite, true
	}
	if scalar.hasNonFiniteComplex {
		return scalar.nonFiniteComplex, true
	}
	switch scalar.value.Kind() {
	case constant.String:
		return constant.StringVal(scalar.value), true
	case constant.Bool:
		return constant.BoolVal(scalar.value), true
	case constant.Int:
		if value.Kind == "uint" {
			if n, err := strconv.ParseUint(value.Text, 10, 64); err == nil {
				return n, true
			}
			return nil, false
		}
		if n, err := strconv.ParseInt(value.Text, 10, 64); err == nil {
			return n, true
		}
		return nil, false
	case constant.Float:
		n, ok := constant.Float64Val(scalar.value)
		return n, ok
	case constant.Complex:
		return bashPPComplexNumber(scalar.value), true
	}
	return nil, false
}

func goSourceFmtBuiltinScalarType(typ string) bool {
	switch typ {
	case "", "bool", "string",
		"int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"byte", "rune",
		"float32", "float64",
		"complex64", "complex128",
		"error":
		return true
	}
	return false
}

func (r *Runner) goSourceCallLocalWrite(ctx context.Context, typeName string, recv bashPPBridgeValue, data []byte, paramType syntax.BashPPTypeExpr) (int, error) {
	cell, err := r.goSourceLocalWriterCell(recv, typeName)
	if err != nil {
		return 0, err
	}
	bound, ok := r.bashPPBindMethodReceiver(cell, "Write", true, recv.Origin == 0)
	if !ok {
		return 0, fmt.Errorf("gosource: cannot bind original method %s.Write", typeName)
	}
	args := make([]any, len(data))
	metas := make([]*bashPPCollectionMeta, len(data))
	for i, b := range data {
		args[i] = int(b)
	}
	arg := &bashPPCell{declType: paramType}
	bashPPStoreCellValue(arg, args, &bashPPCollectionMeta{kind: "slice", typ: paramType, sequence: metas})

	savedResults, savedChannels := r.bashPPResultCells, r.bashPPCallChannels
	savedInterfaces, savedCells := r.bashPPCallInterfaces, r.bashPPCallCells
	defer func() {
		r.bashPPResultCells, r.bashPPCallChannels = savedResults, savedChannels
		r.bashPPCallInterfaces, r.bashPPCallCells = savedInterfaces, savedCells
	}()
	r.bashPPCallChannels, r.bashPPCallInterfaces, r.bashPPCallCells = nil, nil, []*bashPPCell{arg}
	results := r.bashPPInvoke(ctx, bound, []string{""})
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if r.exit.err != nil {
		return 0, r.exit.err
	}
	if r.exit.exiting || r.exit.fatalExit || r.exit.code != 0 || len(results) != 2 || len(r.bashPPResultCells) != 2 {
		return 0, fmt.Errorf("gosource: original %s.Write failed (status %d)", typeName, r.exit.code)
	}
	if !goSourceNilResult(r.bashPPResultCells[1]) {
		return 0, fmt.Errorf("gosource: original %s.Write returned an error", typeName)
	}
	n, err := strconv.Atoi(results[0])
	if err != nil {
		return 0, fmt.Errorf("gosource: original %s.Write returned invalid count", typeName)
	}
	if n != len(data) {
		return 0, fmt.Errorf("gosource: original %s.Write returned short count", typeName)
	}
	return n, nil
}

func (r *Runner) goSourceLocalWriterCell(recv bashPPBridgeValue, typeName string) (*bashPPCell, error) {
	named := &syntax.BashPPNamedType{Name: &syntax.Lit{Value: typeName}}
	if recv.Origin != 0 {
		session := r.bashPPTools.bridge
		if session == nil {
			return nil, fmt.Errorf("gosource: callback receiver identity expired")
		}
		session.mu.Lock()
		ptr := session.origins[recv.Origin]
		session.mu.Unlock()
		if ptr == nil {
			return nil, fmt.Errorf("gosource: callback receiver identity expired")
		}
		return bashPPPointerCell(ptr), nil
	}
	value, meta, err := r.bashPPBridgeContents(recv, named)
	if err != nil {
		return nil, err
	}
	cell := &bashPPCell{declType: named, typeName: typeName}
	bashPPStoreCellValue(cell, value, meta)
	return cell, nil
}

func goSourceNilResult(cell *bashPPCell) bool {
	return cell != nil && (cell.interfaceValue != nil && cell.interfaceValue.nilIface ||
		cell.vr.Kind == expand.String && cell.vr.Str == "" && cell.vr.Obj == nil)
}
