// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"fmt"
	"go/constant"

	"mvdan.cc/sh/v3/syntax"
)

// bashPPSprint162ComplexCollectionText recognises the interpreter's finite
// complex scalar carrier. Collections remain JSON-shaped, so complex values
// stay in their lossless string spelling and are reconstructed from the
// destination type when read.
func bashPPSprint162ComplexCollectionText(value any) bool {
	text, ok := value.(string)
	return ok && bashPPParseComplex(text).Kind() == constant.Complex
}

// bashPPSprint162CollectionBridgeScalar validates a scalar which crossed the
// dependency boundary in its transport wrapper. Non-finite floats cannot live
// in the collection's JSON-shaped scalar payload, so the wrapper remains the
// stored representation after its Go type and scalar spelling are validated.
func (r *Runner) bashPPSprint162CollectionBridgeScalar(value any, expected syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta, bool, error) {
	native, ok := value.(*bashPPBridgeValue)
	if !ok || native == nil {
		return nil, nil, false, nil
	}
	actual := bashPPBridgeDynamicType(native.Type)
	if native.Type == "" || !r.bashPPTypeAssignable(actual, expected) {
		return nil, nil, true, fmt.Errorf("BASHPP-ECOLLECTION-ELEMENT: cannot use %s as %s", bashPPTypeText(actual), bashPPTypeText(expected))
	}
	_, meta, err := bashPPBridgeScalarValue(*native)
	if err != nil {
		return nil, nil, true, fmt.Errorf("BASHPP-ECOLLECTION-ELEMENT: %v", err)
	}
	if meta != nil {
		return nil, nil, true, fmt.Errorf("BASHPP-ECOLLECTION-ELEMENT: dependency scalar carried collection metadata")
	}
	return native, &bashPPCollectionMeta{kind: "bridge-scalar", typ: expected}, true, nil
}
