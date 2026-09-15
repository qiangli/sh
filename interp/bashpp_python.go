package interp

import (
	"fmt"
	"go/constant"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/polyglot"
	"mvdan.cc/sh/v3/syntax"
)

func (r *Runner) bashPPPythonValue(expr syntax.BashPPExpr) (any, bool, error) {
	switch x := expr.(type) {
	case *syntax.BashPPIdent:
		if r.bashPPScope == nil {
			return nil, false, nil
		}
		cell := r.bashPPScope.lookup(x.Name.Value)
		if cell == nil || cell.vr.Kind != expand.Object {
			return nil, false, nil
		}
		handle, ok := cell.vr.Obj.(*polyglot.Handle)
		return handle, ok, nil
	case *syntax.BashPPSelectorExpr:
		base, handled, err := r.bashPPPythonValue(x.X)
		if err != nil || !handled {
			return nil, handled, err
		}
		handle, ok := base.(*polyglot.Handle)
		if !ok {
			return nil, true, fmt.Errorf("Python selector parent is not an object")
		}
		result, err := handle.GetAttr(r.ectx, x.Sel.Value)
		if result.Stdout != "" {
			fmt.Fprint(r.stdout, result.Stdout)
		}
		if result.Stderr != "" {
			fmt.Fprint(r.stderr, result.Stderr)
		}
		return result.Value, true, err
	}
	return nil, false, nil
}

func bashPPPythonScalar(value any) (bashPPScalar, error) {
	switch value := value.(type) {
	case string:
		return bashPPScalar{value: constant.MakeString(value), runtime: true}, nil
	case bool:
		return bashPPScalar{value: constant.MakeBool(value), runtime: true}, nil
	case int64:
		return bashPPScalar{value: constant.MakeInt64(value), runtime: true}, nil
	case float64:
		return bashPPScalar{value: constant.MakeFloat64(value), runtime: true}, nil
	}
	return bashPPScalar{}, fmt.Errorf("Python value %T is not scalar", value)
}
