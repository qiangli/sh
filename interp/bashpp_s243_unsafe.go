package interp

import (
	"fmt"

	"mvdan.cc/sh/v3/syntax"
)

// goSourceUnsafeBlankView validates the only byte view the interpreter can
// presently represent without inventing bytes: a struct made entirely of
// blank fields. Go gives those fields real layout but comparisons ignore their
// stored bytes, so a correctly sized source may be presented as the target's
// zero value while the pointer continues to retain the original allocation.
func (r *Runner) goSourceUnsafeBlankView(source, target syntax.BashPPTypeExpr) error {
	fields, _, ok := r.bashPPStructFields(target)
	if !ok {
		return fmt.Errorf("BASHPP-EUNSAFE-VIEW: target %s is not a supported blank-field struct", bashPPTypeText(target))
	}
	for _, field := range bashPPFlatFields(fields) {
		if field.name != "_" {
			return fmt.Errorf("BASHPP-EUNSAFE-VIEW: target %s contains observable field %s", bashPPTypeText(target), field.name)
		}
	}
	sourceLayout, sourceOK := r.goSourceLayoutType(source, map[string]bool{})
	targetLayout, targetOK := r.goSourceLayoutType(target, map[string]bool{})
	if !sourceOK || !targetOK || goSourceAMD64Sizes.Sizeof(sourceLayout) != goSourceAMD64Sizes.Sizeof(targetLayout) || goSourceAMD64Sizes.Alignof(sourceLayout) != goSourceAMD64Sizes.Alignof(targetLayout) {
		return fmt.Errorf("BASHPP-EUNSAFE-LAYOUT: %s and %s do not have the same size and alignment", bashPPTypeText(source), bashPPTypeText(target))
	}
	return nil
}

func bashPPUnsafeBlankZero(typ syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta) {
	return map[string]any{}, &bashPPCollectionMeta{kind: "struct", typ: typ, mapping: map[string]*bashPPCollectionMeta{}}
}
