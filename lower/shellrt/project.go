// Copyright (c) 2026, the bash++ authors
// See LICENSE for licensing information

package shellrt

import (
	"encoding"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// This file is linked into every generated program, so it stays on the
// standard library. It is a faithful port of the interpreter's object coercion
// in expand/object.go, not an independent design; the two are held together by
// TestProjectMatchesInterpreterCoercion, which diffs this package's output
// against expand.ObjectString over a shared table. The dependency direction is
// test-only on purpose: the generated program must not pull in expand's
// transitive module graph.

// Kind is the projection the compiler selected for a value. The compiler
// decides it from the binding's declared provenance, never from the value's
// runtime shape: an imported package call's result is KindObject even when it
// happens to be a string, and a field selected out of a rich value is
// KindScalar even when the field itself is a plain scalar.
//
// Passing the kind explicitly is what keeps a plain shell string from being
// relabelled as JSON because of what it happens to contain.
type Kind int

const (
	// KindScalar renders a native scalar as the plain shell text the
	// interpreter produces for it. No quoting, no JSON.
	KindScalar Kind = iota
	// KindObject renders a rich root through the shell's object coercion,
	// which is JSON.
	KindObject
)

// InvalidObject is the one fixed marker every unsafely coercible value
// collapses to. It matches the interpreter's marker exactly.
const InvalidObject = "<invalid object>"

// UnsupportedScalar is the visible marker returned when a value reaches the
// runtime scalar path that the runtime cannot faithfully render. It is
// deliberately not a silent best effort: the only values that land here are
// ones whose faithful spelling was already lost before the runtime saw them.
const UnsupportedScalar = "<unsupported scalar projection>"

const (
	maxObjectDepth     = 100
	maxObjectJSONBytes = 64 << 20
	maxObjectVisits    = 1 << 20
)

var (
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	stringerType      = reflect.TypeFor[fmt.Stringer]()
	errorType         = reflect.TypeFor[error]()
	jsonNumberType    = reflect.TypeFor[json.Number]()
	isZeroerType      = reflect.TypeFor[interface{ IsZero() bool }]()
)

// Project renders value as the shell text for the given kind.
//
// Project only reads value. It never assigns through a pointer, never writes a
// map or slice element, and never calls a caller-defined method on the graph,
// so projecting a readonly binding cannot disturb its identity and projecting
// the same value twice always yields the same bytes. The object encoding is
// deterministic: encoding/json sorts map keys and keeps struct field
// declaration order.
//
// Values that cannot be safely coerced — channels, functions and other callable
// capabilities, cyclic graphs, and values carrying MarshalJSON/MarshalText/
// String/Error methods — do not leak a marshaling handle or run caller code.
// They collapse to InvalidObject.
func Project(value any, kind Kind) string {
	switch kind {
	case KindObject:
		return objectText(value)
	default:
		return scalarText(value)
	}
}

func objectText(value any) string {
	if value == nil {
		return "null"
	}
	if err := preflight(reflect.ValueOf(value), 0, map[visit]bool{}, new(int)); err != nil {
		return InvalidObject
	}
	b, err := json.Marshal(value)
	if err != nil || len(b) > maxObjectJSONBytes {
		return InvalidObject
	}
	return string(b)
}

type visit struct {
	typ reflect.Type
	ptr uintptr
}

// preflight refuses everything the interpreter refuses, before any marshaling
// begins. Refusing first is what keeps a caller-defined method from running and
// keeps a capability from reaching the encoder at all.
func preflight(v reflect.Value, depth int, seen map[visit]bool, visits *int) error {
	*visits++
	if *visits > maxObjectVisits {
		return fmt.Errorf("object graph exceeds maximum traversal work")
	}
	if depth > maxObjectDepth {
		return fmt.Errorf("object graph exceeds maximum depth")
	}
	if !v.IsValid() {
		return nil
	}
	if hasCallerMethod(v.Type()) {
		return fmt.Errorf("reachable type %v has caller-defined coercion method", v.Type())
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		if v.Kind() == reflect.Pointer {
			vis := visit{typ: v.Type(), ptr: v.Pointer()}
			if seen[vis] {
				return fmt.Errorf("object graph contains a cycle")
			}
			seen[vis] = true
			defer delete(seen, vis)
		}
		return preflight(v.Elem(), depth+1, seen, visits)
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.String:
		return nil
	case reflect.Float32, reflect.Float64:
		if f := v.Float(); math.IsInf(f, 0) || math.IsNaN(f) {
			return fmt.Errorf("non-finite float cannot be used as a JSON object value")
		}
		return nil
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := preflight(v.Index(i), depth+1, seen, visits); err != nil {
				return err
			}
		}
		return nil
	case reflect.Slice:
		if v.IsNil() {
			return nil
		}
		vis := visit{typ: v.Type(), ptr: v.Pointer()}
		if seen[vis] {
			return fmt.Errorf("object graph contains a cycle")
		}
		seen[vis] = true
		defer delete(seen, vis)
		if isJSONByteSlice(v.Type()) {
			// encoding/json emits a base64 string; there is nothing to walk.
			return nil
		}
		for i := 0; i < v.Len(); i++ {
			if err := preflight(v.Index(i), depth+1, seen, visits); err != nil {
				return err
			}
		}
		return nil
	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		if !validJSONMapKey(v.Type().Key()) {
			return fmt.Errorf("map key type %v is not supported by JSON objects", v.Type().Key())
		}
		// Maps do not expose Pointer; UnsafePointer is the supported reflect
		// identity. It is used only for cycle detection.
		vis := visit{typ: v.Type(), ptr: uintptr(v.UnsafePointer())}
		if seen[vis] {
			return fmt.Errorf("object graph contains a cycle")
		}
		seen[vis] = true
		defer delete(seen, vis)
		iter := v.MapRange()
		for iter.Next() {
			if err := preflight(iter.Key(), depth+1, seen, visits); err != nil {
				return err
			}
			if err := preflight(iter.Value(), depth+1, seen, visits); err != nil {
				return err
			}
		}
		return nil
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			field := t.Field(i)
			omitZero, reachable := fieldInfo(field)
			if !reachable {
				continue
			}
			if omitZero && hasIsZeroMethod(field.Type) {
				return fmt.Errorf("reachable type %v has caller-defined IsZero method used by omitzero", field.Type)
			}
			if err := preflight(v.Field(i), depth+1, seen, visits); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported JSON object kind %v", v.Kind())
	}
}

// fieldInfo reports whether a struct field is reachable by encoding/json and
// whether it carries the omitzero option.
func fieldInfo(field reflect.StructField) (omitZero, reachable bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return false, false
	}
	if !field.IsExported() && !field.Anonymous {
		return false, false
	}
	_, options, _ := strings.Cut(tag, ",")
	for options != "" {
		var option string
		option, options, _ = strings.Cut(options, ",")
		if option == "omitzero" {
			omitZero = true
		}
	}
	return omitZero, true
}

func hasIsZeroMethod(t reflect.Type) bool {
	return t.Implements(isZeroerType) ||
		(t.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(isZeroerType))
}

func isJSONByteSlice(t reflect.Type) bool {
	if t.Kind() != reflect.Slice || t.Elem().Kind() != reflect.Uint8 {
		return false
	}
	ptrElem := reflect.PointerTo(t.Elem())
	return !ptrElem.Implements(jsonMarshalerType) && !ptrElem.Implements(textMarshalerType)
}

func hasCallerMethod(t reflect.Type) bool {
	if t == jsonNumberType {
		// encoding/json handles json.Number as a scalar without calling String.
		return false
	}
	if implementsUnsafeMethod(t) {
		return true
	}
	return t.Kind() != reflect.Pointer && implementsUnsafeMethod(reflect.PointerTo(t))
}

func implementsUnsafeMethod(t reflect.Type) bool {
	return t.Implements(jsonMarshalerType) ||
		t.Implements(textMarshalerType) ||
		t.Implements(stringerType) ||
		t.Implements(errorType)
}

func validJSONMapKey(t reflect.Type) bool {
	if hasCallerMethod(t) {
		return false
	}
	switch t.Kind() {
	case reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	default:
		return false
	}
}

// scalarText renders the native scalars the compiler is allowed to route
// through KindScalar. Floating values are refused rather than guessed: the
// interpreter spells an untyped 1.5 as the exact rational 3/2, and a float64
// alone no longer carries the source provenance needed to recover that. The
// compiler must project floating scalars from their retained literal instead.
func scalarText(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.FormatInt(int64(v), 10)
	case int8:
		return strconv.FormatInt(int64(v), 10)
	case int16:
		return strconv.FormatInt(int64(v), 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	case uint8:
		return strconv.FormatUint(uint64(v), 10)
	case uint16:
		return strconv.FormatUint(uint64(v), 10)
	case uint32:
		return strconv.FormatUint(uint64(v), 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case uintptr:
		return strconv.FormatUint(uint64(v), 10)
	default:
		return UnsupportedScalar
	}
}
