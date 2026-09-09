// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

// Sprint: #118; Story: #54; Story-ID: c3a60493cde9
import (
	"mvdan.cc/sh/v3/syntax"
)

// bashPPResolveNativeEmbedded finds the embedded field path that promotes an
// imported type's method to root. It is the dependency-owned half of
// [Runner.bashPPResolveSelection]: that BFS can only match methods declared in
// this program, so a struct embedding `sync.Mutex` resolves nothing for `Lock`.
//
// Go's depth and ambiguity rules are shared, not re-invented. Levels are walked
// breadth-first; the shallowest level holding any candidate decides, and a
// level is usable only when exactly one candidate sits at it. A local field or
// method of the same name at that level therefore shadows or collides exactly
// as it would in Go, and this helper reports nothing so the interpreted paths
// keep ownership of the diagnostic.
func (r *Runner) bashPPResolveNativeEmbedded(root syntax.BashPPTypeExpr, name string) ([]bashPPEmbedEdge, bool) {
	if !r.bashPPGoSource || root == nil || name == "" {
		return nil, false
	}
	rootPointer := false
	if pointer, ok := root.(*syntax.BashPPPointerType); ok {
		rootPointer, root = true, pointer.Element
	}
	key := bashPPTypeText(root)
	level := []bashPPSelectionNode{{typ: root, indirect: rootPointer, ancestors: map[string]bool{key: true}}}
	for len(level) > 0 {
		var native [][]bashPPEmbedEdge
		local := 0
		var next []bashPPSelectionNode
		for _, node := range level {
			if r.bashPPNativeType(node.typ) {
				// Depth 0 is an ordinary native receiver, already routed by the
				// direct paths; it still counts as the winning candidate here so
				// a deeper embedded match cannot outrank it.
				native = append(native, append([]bashPPEmbedEdge(nil), node.edges...))
			}
			fields, _, isStruct := r.bashPPStructFields(node.typ)
			if isStruct {
				for _, field := range fields {
					for _, fieldName := range bashPPDeclaredFieldNames(field) {
						if fieldName == name {
							local++
						}
					}
				}
			}
			if owner, _, ok := r.bashPPMethodOwner(node.typ); ok && r.bashPPMethods[owner][name] != nil {
				local++
			}
			if iface, ok := r.bashPPInterfaceType(node.typ); ok {
				set, err := r.bashPPInterfaceMethodSet(bashPPTypeText(node.typ), iface, make(map[string]bool))
				if err == nil {
					if _, found := set.byName[name]; found {
						local++
					}
				}
			}
			if !isStruct {
				continue
			}
			for _, field := range fields {
				fieldName, ok := bashPPEmbeddedFieldName(field)
				if !ok {
					continue
				}
				child, pointer, invalid, _ := r.bashPPEmbeddedTarget(field.FieldTypeExpr)
				if invalid || child == nil {
					continue
				}
				childKey := bashPPTypeText(child)
				if node.ancestors[childKey] {
					continue
				}
				ancestors := make(map[string]bool, len(node.ancestors)+1)
				for ancestor := range node.ancestors {
					ancestors[ancestor] = true
				}
				ancestors[childKey] = true
				edges := append(append([]bashPPEmbedEdge(nil), node.edges...), bashPPEmbedEdge{name: fieldName, pointer: pointer})
				next = append(next, bashPPSelectionNode{typ: child, edges: edges, indirect: node.indirect || pointer, ancestors: ancestors})
			}
		}
		if local+len(native) > 0 {
			if local > 0 || len(native) != 1 {
				return nil, false
			}
			return native[0], true
		}
		level = next
	}
	return nil, false
}

// bashPPPromotedNativeReceiver reads the embedded dependency-owned receiver
// that carries method, for a receiver expression that is itself a local
// identifier or field chain. The embedded field is read out of the root's live
// storage rather than reconstructed, so the returned handle is the same value
// the explicit `x.Mutex.Lock()` spelling would reach: pointer identity, and
// with it lock state, is preserved.
//
// Only identifiers and field selectors are walked, so this evaluates no index,
// call or user expression and cannot repeat an operand's effects — the caller
// may use it as a predicate and then again to bind, as the direct native paths
// already do with [Runner.bashPPNativeLocalField].
func (r *Runner) bashPPPromotedNativeReceiver(expr syntax.BashPPExpr, method string) *bashPPBridgeValue {
	if !r.bashPPGoSource || method == "" {
		return nil
	}
	if paren, ok := expr.(*syntax.BashPPParenExpr); ok {
		return r.bashPPPromotedNativeReceiver(paren.X, method)
	}
	value, meta, ok := r.bashPPNativeLocalBase(expr)
	if !ok || meta == nil || meta.kind != "struct" {
		return nil
	}
	if _, already := value.(*bashPPBridgeValue); already {
		// A wholly dependency-owned receiver is the direct path's business.
		return nil
	}
	edges, ok := r.bashPPResolveNativeEmbedded(meta.typ, method)
	if !ok || len(edges) == 0 {
		return nil
	}
	embedded, _, err := bashPPReadSelection(value, meta, edges)
	if err != nil {
		return nil
	}
	native, _ := embedded.(*bashPPBridgeValue)
	return native
}
