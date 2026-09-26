package interp

// Sprint: #165; Story: #99; Story-ID: ca559d7ee23d
//
// Cyclic pointees cross the bridge finitely.
//
// The transport walks a pointer as the pointee it addresses, and a pointee's
// fields as the values they hold. A value graph with a cycle — a doubly
// linked ring whose sentinel's next and prev address the sentinel itself
// (container/list's shape; typeparam/list2.go hands such an element to
// fmt's %p) — made that walk unbounded: the native stack overflowed at the
// 1 GB limit in seconds. Go's own encoders print a nested pointer at depth
// as its address; the bridge's equivalent is a BACK-REFERENCE: a pointer
// whose origin is already on the current walk's path crosses as
// `{Kind:"pointer", Type, Origin}` with no pointee, and the worker binds
// it to the storage it registered for that origin before decoding the
// pointee (pre-order registration in bashpp_native_worker.go.txt). The
// worker's structural encoder answers the same shape for a pointer already
// on its encoding path, and the interpreter rebuilds it as the original
// pointer that origin names. Nothing is normalised: a back-reference to an
// origin the session never registered fails closed on either side.

import (
	"fmt"
	"reflect"
	"strconv"

	"mvdan.cc/sh/v3/syntax"
)

// bashPPTransportOrigin registers ptr with the session's origin table (or
// finds its existing origin) BEFORE its pointee is transported, so a nested
// reference back to ptr can be spelled by origin.
func bashPPTransportOrigin(session *bashPPNativeSession, ptr *bashPPPointer) uint64 {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.origins == nil {
		session.origins = map[uint64]*bashPPPointer{}
	}
	// The index answers the storage identity (target cell + path) in O(1).
	// Registered paths are never rewritten in place, so a hit is verified
	// exactly as the former table scan compared, and a miss means no
	// registered pointer names that storage.
	key := bashPPOriginKeyOf(ptr)
	if session.originIndex == nil || session.originIndexed != len(session.origins) {
		session.reindexOriginsLocked()
	}
	if id, ok := session.originIndex[key]; ok {
		if existing := session.origins[id]; existing != nil && existing.target == ptr.target && reflect.DeepEqual(existing.path, ptr.path) {
			return id
		}
	}
	session.originNext++
	session.origins[session.originNext] = ptr
	session.originIndex[key] = session.originNext
	session.originIndexed = len(session.origins)
	return session.originNext
}

// bashPPTransportSliceOrigin gives an interpreter slice backing array a stable
// session-local identity. A reslice starting at the same element retains the
// identity; a replacement header naming a different array does not. Keeping
// the view in the session prevents address reuse while a native reflect.Value
// can still retain an element selected from that backing array.
func bashPPTransportSliceOrigin(session *bashPPNativeSession, view []any) uint64 {
	if session == nil || cap(view) == 0 {
		return 0
	}
	data := reflect.ValueOf(view).Pointer()
	if data == 0 {
		return 0
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if id := session.sliceOriginIndex[data]; id != 0 {
		return id
	}
	if session.sliceOriginIndex == nil {
		session.sliceOriginIndex = make(map[uintptr]uint64)
		session.sliceOriginKeep = make(map[uint64][]any)
	}
	session.sliceOriginNext++
	id := session.sliceOriginNext
	session.sliceOriginIndex[data] = id
	session.sliceOriginKeep[id] = view[:cap(view)]
	return id
}

// bashPPOriginKey is the storage identity a transport origin names: the root
// cell and an exact spelling of the step path. A nil path and an empty path
// spell differently, as reflect.DeepEqual distinguishes them.
type bashPPOriginKey struct {
	target *bashPPCell
	path   string
}

func bashPPOriginKeyOf(ptr *bashPPPointer) bashPPOriginKey {
	if ptr.path == nil {
		return bashPPOriginKey{target: ptr.target}
	}
	b := make([]byte, 0, 1+len(ptr.path)*8)
	b = append(b, '[')
	for _, step := range ptr.path {
		// Length-prefixed field names keep arbitrary names unambiguous.
		b = strconv.AppendInt(b, int64(len(step.field)), 10)
		b = append(b, ':')
		b = append(b, step.field...)
		b = append(b, ',')
		b = strconv.AppendInt(b, int64(step.index), 10)
		if step.deref {
			b = append(b, '*')
		}
		b = append(b, ';')
	}
	return bashPPOriginKey{target: ptr.target, path: string(b)}
}

// reindexOriginsLocked rebuilds the identity index from the origin table.
// It keeps the lowest origin for a duplicated identity, which is the only
// one the table registration itself could have produced.
func (s *bashPPNativeSession) reindexOriginsLocked() {
	s.originIndex = make(map[bashPPOriginKey]uint64, len(s.origins))
	for id, ptr := range s.origins {
		if ptr == nil {
			continue
		}
		key := bashPPOriginKeyOf(ptr)
		if prev, ok := s.originIndex[key]; !ok || id < prev {
			s.originIndex[key] = id
		}
	}
	s.originIndexed = len(s.origins)
}

// bashPPTransportEnter marks origin as being transported on this runner's
// current walk. It answers true when origin is already on the path — the
// caller then sends a back-reference — and otherwise a leave function that
// unmarks it once its pointee has crossed.
func (r *Runner) bashPPTransportEnter(origin uint64) (onPath bool, leave func()) {
	if r.bashPPTransportPath == nil {
		r.bashPPTransportPath = map[uint64]bool{}
	}
	if r.bashPPTransportPath[origin] {
		return true, nil
	}
	r.bashPPTransportPath[origin] = true
	return false, func() {
		delete(r.bashPPTransportPath, origin)
		if len(r.bashPPTransportPath) == 0 {
			r.bashPPTransportPath = nil
		}
	}
}

// bashPPTransportBackReference is the finite spelling of a pointer already
// on the transport path: its type from the pointee's declared type, its
// origin, no pointee.
func bashPPTransportBackReference(session *bashPPNativeSession, origin uint64, meta *bashPPCollectionMeta, typ syntax.BashPPTypeExpr) bashPPBridgeValue {
	if meta != nil && meta.typ != nil {
		typ = meta.typ
	}
	return bashPPBridgeValue{Origin: origin, Session: session.id, Kind: "pointer", Type: "*" + bashPPBridgeTypeText(typ)}
}

// bashPPBridgeBackReference answers whether v is a back-reference the
// dependency sent (a pointer with an origin and no pointee).
func bashPPBridgeBackReference(v bashPPBridgeValue) bool {
	return v.Kind == "pointer" && len(v.Elements) == 0
}

// bashPPBridgeBackReferencePointer resolves a back-reference the dependency
// sent to the original pointer its origin names; an origin this session
// never registered fails closed.
func (r *Runner) bashPPBridgeBackReferencePointer(v bashPPBridgeValue) (*bashPPPointer, error) {
	session := r.bashPPTools.bridge
	if session == nil || v.Origin == 0 || v.Session != session.id {
		return nil, fmt.Errorf("gosource: pointer back-reference names no origin of this dependency session")
	}
	session.mu.Lock()
	ptr := session.origins[v.Origin]
	session.mu.Unlock()
	if ptr == nil {
		return nil, fmt.Errorf("gosource: pointer back-reference names an unknown origin")
	}
	return ptr, nil
}

// bashPPBridgeOriginPointer resolves a pointer-typed value the dependency
// sent to the original pointer its origin names. A back-reference (no
// pointee) MUST resolve; a flattened pointee carrying an origin resolves when
// the origin is this session's, and otherwise stays the copy-by-shape the
// caller rebuilds.
func (r *Runner) bashPPBridgeOriginPointer(v bashPPBridgeValue) (*bashPPPointer, bool, error) {
	if bashPPBridgeBackReference(v) {
		ptr, err := r.bashPPBridgeBackReferencePointer(v)
		return ptr, err == nil, err
	}
	session := r.bashPPTools.bridge
	if session == nil || v.Origin == 0 || v.Session != session.id {
		return nil, false, nil
	}
	session.mu.Lock()
	ptr := session.origins[v.Origin]
	session.mu.Unlock()
	return ptr, ptr != nil, nil
}
