// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// bashPPMapKey is the evaluator's comparable representation of a Go map key.
// The payload remains JSON-shaped for shell interoperability, while this key
// preserves the Go type and equality identity that a string property cannot.
type bashPPMapKey struct {
	typ   string
	value string
	nonce uint64
}

type bashPPMapEntry struct {
	storage string
	key     any
	keyMeta *bashPPCollectionMeta
}

func (r *Runner) bashPPSprint165StoredBridgeScalar(expr syntax.BashPPExpr, expected syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta, bool, error) {
	id, ok := expr.(*syntax.BashPPIdent)
	if !ok || r.bashPPScope == nil {
		return nil, nil, false, nil
	}
	cell := r.bashPPScope.lookup(id.Name.Value)
	if cell == nil || cell.vr.Kind != expand.Object {
		return nil, nil, false, nil
	}
	native, ok := cell.vr.Obj.(*bashPPBridgeValue)
	if !ok || native == nil {
		return nil, nil, false, nil
	}
	switch native.Kind {
	case "string", "bool", "int", "uint", "float", "complex":
	default:
		return nil, nil, false, nil
	}
	return r.bashPPCollectionBridgeValue(cell.vr.Obj, expected)
}

func bashPPSprint165MapPart(text string) string {
	return strconv.Itoa(len(text)) + ":" + text
}

func bashPPSprint165PointerMapValue(pointer *bashPPPointer) string {
	if pointer == nil {
		return "nil"
	}
	var path strings.Builder
	for _, step := range pointer.path {
		path.WriteString(bashPPSprint165MapPart(step.field))
		path.WriteByte('/')
		path.WriteString(strconv.Itoa(step.index))
		path.WriteByte('/')
		path.WriteString(strconv.FormatBool(step.deref))
	}
	return fmt.Sprintf("%p/%s", pointer.target, path.String())
}

func (r *Runner) bashPPSprint165MapKey(value any, meta *bashPPCollectionMeta, typ syntax.BashPPTypeExpr) (bashPPMapKey, bool, error) {
	if meta != nil && meta.interfaceValue != nil {
		iface := meta.interfaceValue
		if iface.nilIface || iface.cell == nil {
			return bashPPMapKey{typ: "<nil>", value: "nil"}, false, nil
		}
		dynamic := iface.dynamic
		if !r.bashPPMapKeyType(dynamic) {
			r.bashPPRaise("runtime error: hash of unhashable type " + r.goSourceRuntimeTypeName(dynamic))
			return bashPPMapKey{}, false, errBashPPScalarInterrupted
		}
		cell := iface.cell
		var inner any
		var innerMeta *bashPPCollectionMeta
		switch {
		case cell.pointer:
			inner, innerMeta = cell.pointerValue, bashPPPointerMeta(cell.declType)
		case cell.vr.Kind == expand.Object:
			inner, innerMeta = cell.vr.Obj, bashPPCellMeta(cell)
		default:
			inner = bashPPBuiltinExactScalarValue(cell.vr.String(), r.bashPPScalarFromCell(cell))
		}
		key, nonreflexive, err := r.bashPPSprint165MapKey(inner, innerMeta, dynamic)
		key.typ = bashPPTypeText(dynamic)
		return key, nonreflexive, err
	}

	typeName := bashPPTypeText(typ)
	shape := r.bashPPUnderlyingType(typ)
	switch x := shape.(type) {
	case *syntax.BashPPNamedType:
		if native, ok := value.(*bashPPBridgeValue); ok && native != nil {
			decoded, _, err := bashPPBridgeScalarValue(*native)
			if err != nil {
				return bashPPMapKey{}, false, err
			}
			value = decoded
		}
		switch x.Name.Value {
		case "string":
			text, ok := value.(string)
			if !ok {
				return bashPPMapKey{}, false, fmt.Errorf("BASHPP-ECOLLECTION-KEY: %T is not %s", value, typeName)
			}
			return bashPPMapKey{typ: typeName, value: "s" + bashPPSprint165MapPart(text)}, false, nil
		case "bool":
			boolean, ok := value.(bool)
			if !ok {
				return bashPPMapKey{}, false, fmt.Errorf("BASHPP-ECOLLECTION-KEY: %T is not %s", value, typeName)
			}
			return bashPPMapKey{typ: typeName, value: "b" + strconv.FormatBool(boolean)}, false, nil
		case "float32", "float64":
			floating, ok := value.(float64)
			if !ok {
				if integer, integerOK := value.(int); integerOK {
					floating, ok = float64(integer), true
				}
			}
			if !ok {
				return bashPPMapKey{}, false, fmt.Errorf("BASHPP-ECOLLECTION-KEY: %T is not %s", value, typeName)
			}
			if x.Name.Value == "float32" {
				floating = float64(float32(floating))
			}
			if math.IsNaN(floating) {
				return bashPPMapKey{typ: typeName, value: "nan"}, true, nil
			}
			if floating == 0 {
				floating = 0
			}
			return bashPPMapKey{typ: typeName, value: "f" + strconv.FormatUint(math.Float64bits(floating), 16)}, false, nil
		case "complex64", "complex128":
			var complexValue complex128
			switch value := value.(type) {
			case complex128:
				complexValue = value
			case string:
				complexValue = bashPPComplexNumber(bashPPParseComplex(value))
			default:
				return bashPPMapKey{}, false, fmt.Errorf("BASHPP-ECOLLECTION-KEY: %T is not %s", value, typeName)
			}
			if x.Name.Value == "complex64" {
				complexValue = complex128(complex64(complexValue))
			}
			if math.IsNaN(real(complexValue)) || math.IsNaN(imag(complexValue)) {
				return bashPPMapKey{typ: typeName, value: "nan"}, true, nil
			}
			realPart, imagPart := real(complexValue), imag(complexValue)
			if realPart == 0 {
				realPart = 0
			}
			if imagPart == 0 {
				imagPart = 0
			}
			encoded := strconv.FormatUint(math.Float64bits(realPart), 16) + "/" + strconv.FormatUint(math.Float64bits(imagPart), 16)
			return bashPPMapKey{typ: typeName, value: "c" + encoded}, false, nil
		default:
			if bashPPIntegerType(x.Name.Value) {
				return bashPPMapKey{typ: typeName, value: "i" + fmt.Sprint(value)}, false, nil
			}
		}
	case *syntax.BashPPPointerType:
		pointer, ok := value.(*bashPPPointer)
		if !ok || pointer == nil {
			return bashPPMapKey{typ: typeName, value: "nil"}, false, nil
		}
		return bashPPMapKey{typ: typeName, value: bashPPSprint165PointerMapValue(pointer)}, false, nil
	case *syntax.BashPPChanType:
		if meta != nil && meta.channel != nil {
			return bashPPMapKey{typ: typeName, value: fmt.Sprintf("local:%p", meta.channel)}, false, nil
		}
		if native, ok := value.(*bashPPBridgeValue); ok && native != nil {
			return bashPPMapKey{typ: typeName, value: fmt.Sprintf("native:%s:%d", native.Session, native.Handle)}, false, nil
		}
		return bashPPMapKey{typ: typeName, value: "nil"}, false, nil
	case *syntax.BashPPCollectionType:
		if x.Kind != "array" {
			break
		}
		sequence, ok := value.([]any)
		if !ok {
			return bashPPMapKey{}, false, fmt.Errorf("BASHPP-ECOLLECTION-KEY: %T is not %s", value, typeName)
		}
		var encoded strings.Builder
		for i, item := range sequence {
			var child *bashPPCollectionMeta
			if meta != nil && i < len(meta.sequence) {
				child = meta.sequence[i]
			}
			part, nonreflexive, err := r.bashPPSprint165MapKey(item, child, x.Element)
			if err != nil {
				return bashPPMapKey{}, false, err
			}
			if nonreflexive {
				return bashPPMapKey{typ: typeName, value: "nan"}, true, nil
			}
			encoded.WriteString(bashPPSprint165MapPart(part.typ))
			encoded.WriteString(bashPPSprint165MapPart(part.value))
		}
		return bashPPMapKey{typ: typeName, value: "a" + encoded.String()}, false, nil
	case *syntax.BashPPStructType:
		mapping, ok := value.(map[string]any)
		if !ok {
			return bashPPMapKey{}, false, fmt.Errorf("BASHPP-ECOLLECTION-KEY: %T is not %s", value, typeName)
		}
		var encoded strings.Builder
		for _, field := range bashPPFlatFields(x.Fields) {
			var child *bashPPCollectionMeta
			if meta != nil {
				child = bashPPLayoutGet(meta.mapping, field.name)
			}
			fieldValue, _ := bashPPStorageGet(mapping, field.name)
			part, nonreflexive, err := r.bashPPSprint165MapKey(fieldValue, child, field.typ)
			if err != nil {
				return bashPPMapKey{}, false, err
			}
			if nonreflexive {
				return bashPPMapKey{typ: typeName, value: "nan"}, true, nil
			}
			encoded.WriteString(bashPPSprint165MapPart(field.name))
			encoded.WriteString(bashPPSprint165MapPart(part.typ))
			encoded.WriteString(bashPPSprint165MapPart(part.value))
		}
		return bashPPMapKey{typ: typeName, value: "r" + encoded.String()}, false, nil
	}
	return bashPPMapKey{}, false, fmt.Errorf("BASHPP-ECOLLECTION-KEY: unsupported map key type %s", typeName)
}

func (r *Runner) bashPPSprint165MapLookup(meta *bashPPCollectionMeta, value any, valueMeta *bashPPCollectionMeta, typ syntax.BashPPTypeExpr) (string, *bashPPMapEntry, bool, error) {
	key, nonreflexive, err := r.bashPPSprint165MapKey(value, valueMeta, typ)
	if err != nil || nonreflexive || meta == nil {
		return "", nil, false, err
	}
	bashPPStorageMu.RLock()
	entry, found := meta.mapKeys[key]
	bashPPStorageMu.RUnlock()
	if !found {
		return "", nil, false, nil
	}
	return entry.storage, entry, true, nil
}

func (r *Runner) bashPPSprint165MapDelete(mapping map[string]any, meta *bashPPCollectionMeta, value any, valueMeta *bashPPCollectionMeta, typ syntax.BashPPTypeExpr) error {
	key, nonreflexive, err := r.bashPPSprint165MapKey(value, valueMeta, typ)
	if err != nil || nonreflexive || meta == nil {
		return err
	}
	bashPPStorageMu.Lock()
	defer bashPPStorageMu.Unlock()
	entry := meta.mapKeys[key]
	if entry == nil {
		return nil
	}
	delete(mapping, entry.storage)
	delete(meta.mapping, entry.storage)
	delete(meta.mapKeys, key)
	return nil
}

func (r *Runner) bashPPSprint165MapStore(mapping map[string]any, meta *bashPPCollectionMeta, value any, valueMeta *bashPPCollectionMeta, typ syntax.BashPPTypeExpr, element any, child *bashPPCollectionMeta) (string, error) {
	if mapping == nil {
		if r.bashPPGoSource {
			r.goSourceRuntimePanic("assignment to entry in nil map")
			return "", errBashPPScalarInterrupted
		}
		return "", fmt.Errorf("BASHPP-ENIL-MAP: assignment to nil map")
	}
	key, nonreflexive, err := r.bashPPSprint165MapKey(value, valueMeta, typ)
	if err != nil {
		return "", err
	}
	bashPPStorageMu.Lock()
	defer bashPPStorageMu.Unlock()
	if meta.mapKeys == nil {
		meta.mapKeys = make(map[bashPPMapKey]*bashPPMapEntry)
	}
	if !nonreflexive {
		if entry := meta.mapKeys[key]; entry != nil {
			mapping[entry.storage], meta.mapping[entry.storage] = element, child
			return entry.storage, nil
		}
	} else {
		meta.mapNonce++
		key.nonce = meta.mapNonce
	}
	storage := fmt.Sprint(value)
	if _, exists := mapping[storage]; exists {
		for {
			meta.mapNonce++
			candidate := storage + "#map" + strconv.FormatUint(meta.mapNonce, 10)
			if _, exists := mapping[candidate]; !exists {
				storage = candidate
				break
			}
		}
	}
	entry := &bashPPMapEntry{storage: storage, key: value, keyMeta: valueMeta}
	meta.mapKeys[key] = entry
	mapping[storage], meta.mapping[storage] = element, child
	return storage, nil
}

func bashPPSprint165MapEntries(meta *bashPPCollectionMeta) []*bashPPMapEntry {
	if meta == nil {
		return nil
	}
	bashPPStorageMu.RLock()
	entries := make([]*bashPPMapEntry, 0, len(meta.mapKeys))
	for _, entry := range meta.mapKeys {
		entries = append(entries, entry)
	}
	bashPPStorageMu.RUnlock()
	sort.Slice(entries, func(i, j int) bool { return entries[i].storage < entries[j].storage })
	return entries
}

func bashPPSprint165MapHasTypedKeys(meta *bashPPCollectionMeta) bool {
	if meta == nil {
		return false
	}
	bashPPStorageMu.RLock()
	typed := meta.mapKeys != nil
	bashPPStorageMu.RUnlock()
	return typed
}

func bashPPSprint165MapEntryTable(meta *bashPPCollectionMeta) map[bashPPMapKey]*bashPPMapEntry {
	if meta == nil {
		return nil
	}
	bashPPStorageMu.RLock()
	if meta.mapKeys == nil {
		bashPPStorageMu.RUnlock()
		return nil
	}
	entries := make(map[bashPPMapKey]*bashPPMapEntry, len(meta.mapKeys))
	for key, entry := range meta.mapKeys {
		entries[key] = entry
	}
	bashPPStorageMu.RUnlock()
	return entries
}

func bashPPSprint165MapEntryValue(mapping map[string]any, meta *bashPPCollectionMeta, storage string) (any, *bashPPCollectionMeta, bool) {
	bashPPStorageMu.RLock()
	value, found := mapping[storage]
	var child *bashPPCollectionMeta
	if meta != nil && meta.mapping != nil {
		child = meta.mapping[storage]
	}
	bashPPStorageMu.RUnlock()
	return value, child, found
}

func bashPPSprint165MapEntryValueFound(mapping map[string]any, meta *bashPPCollectionMeta, storage string, found bool) (any, *bashPPCollectionMeta) {
	if !found {
		return nil, nil
	}
	value, child, _ := bashPPSprint165MapEntryValue(mapping, meta, storage)
	return value, child
}

func bashPPSprint165MapClear(mapping map[string]any, meta *bashPPCollectionMeta) {
	bashPPStorageMu.Lock()
	clear(mapping)
	if meta != nil {
		clear(meta.mapping)
		clear(meta.mapKeys)
	}
	bashPPStorageMu.Unlock()
}
