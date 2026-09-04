// Copyright (c) 2026, the bash++ authors
// See LICENSE for licensing information

package expand

import (
	"encoding"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"mvdan.cc/sh/v3/syntax"
)

const (
	invalidObjectString = "<invalid object>"
	maxObjectDepth      = 100
	maxObjectJSONBytes  = 64 << 20
	maxObjectVisits     = 1 << 20
)

var (
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	stringerType      = reflect.TypeFor[fmt.Stringer]()
	errorType         = reflect.TypeFor[error]()
	jsonNumberType    = reflect.TypeFor[json.Number]()
)

type invalidObject struct{}

func (invalidObject) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("invalid object")
}

// Objects are the bash++ dialect's one value kind which is not a string. The
// shell itself remains string-typed: an [Object] only stays an object while it
// is held by a variable, and the moment it is used as a word — interpolated,
// compared, or passed as an argument — it is coerced by [ObjectString].
//
// The coercion is JSON. That single choice is what makes objects safe to add to
// a POSIX shell: every consumer that predates them, from `echo` to an external
// binary, keeps seeing a plain string, and the only thing that changes is that
// the string is now structured. Serializing on the way out is what the OS
// boundary does; see [mvdan.cc/sh/v3/interp] and its exec environment.

// ObjectString renders val as the string the shell should see for it, which is
// its JSON encoding. Values which cannot be safely marshaled as JSON — channels,
// functions, cyclic structures, excessive depth, or values with marshaling,
// string, or error methods — return one fixed marker. The coercion stays total,
// but it never invokes caller-defined methods on invalid values.
//
// The encoding is deterministic: map keys are sorted by encoding/json, and
// struct fields keep their declaration order.
func ObjectString(val any) string {
	if val == nil {
		return "null"
	}
	state := newObjectPreflight()
	if err := preflightObject(reflect.ValueOf(val), 0, make(map[visit]bool), state); err != nil {
		return invalidObjectString
	}
	b, err := json.Marshal(val)
	if err != nil || len(b) > maxObjectJSONBytes {
		return invalidObjectString
	}
	return string(b)
}

// ValidObject reports whether val may be stored in an [Object] variable, which
// requires that it round-trips through JSON. Callers should use this to reject
// a bad value at the point of assignment, where an error can still be reported,
// rather than at expansion time where it cannot.
func ValidObject(val any) error {
	if val == nil {
		return nil // encodes as JSON null
	}
	state := newObjectPreflight()
	if err := preflightObject(reflect.ValueOf(val), 0, make(map[visit]bool), state); err != nil {
		return fmt.Errorf("value cannot be used as an object: %w", err)
	}
	b, err := json.Marshal(val)
	if err != nil {
		return fmt.Errorf("value cannot be used as an object: %w", err)
	}
	if len(b) > maxObjectJSONBytes {
		return fmt.Errorf("value cannot be used as an object: JSON encoding exceeds %d bytes", maxObjectJSONBytes)
	}
	return nil
}

// NewObject builds an [Object] variable holding val.
func NewObject(val any) Variable {
	if err := ValidObject(val); err != nil {
		val = invalidObject{}
	}
	return Variable{Set: true, Kind: Object, Obj: val}
}

type visit struct {
	typ reflect.Type
	ptr uintptr
}

type objectPreflight struct {
	visits       int
	encodedBytes int64
}

func newObjectPreflight() *objectPreflight {
	return &objectPreflight{}
}

func (p *objectPreflight) addBytes(n int64) error {
	if n < 0 || p.encodedBytes > int64(maxObjectJSONBytes)-n {
		return fmt.Errorf("object JSON encoding exceeds %d bytes", maxObjectJSONBytes)
	}
	p.encodedBytes += n
	return nil
}

func preflightObject(v reflect.Value, depth int, seen map[visit]bool, state *objectPreflight) error {
	state.visits++
	if state.visits > maxObjectVisits {
		return fmt.Errorf("object graph exceeds maximum traversal work %d", maxObjectVisits)
	}
	if depth > maxObjectDepth {
		return fmt.Errorf("object graph exceeds maximum depth %d", maxObjectDepth)
	}
	if !v.IsValid() {
		return nil
	}
	if typeHasCallerMethod(v.Type()) {
		return fmt.Errorf("reachable type %v has caller-defined coercion method", v.Type())
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return state.addBytes(4) // null
		}
		if v.Kind() == reflect.Pointer {
			vis := visit{typ: v.Type(), ptr: v.Pointer()}
			if seen[vis] {
				return fmt.Errorf("object graph contains a cycle")
			}
			seen[vis] = true
			defer delete(seen, vis)
		}
		return preflightObject(v.Elem(), depth+1, seen, state)
	case reflect.Bool:
		return state.addBytes(5) // false is the longer spelling
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return state.addBytes(int64(len(strconv.FormatInt(v.Int(), 10))))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return state.addBytes(int64(len(strconv.FormatUint(v.Uint(), 10))))
	case reflect.String:
		return state.addBytes(jsonStringSize(v.String()))
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return fmt.Errorf("non-finite float cannot be used as a JSON object value")
		}
		// The longest finite float spelling emitted by encoding/json is well
		// below this bound, including exponent and sign.
		return state.addBytes(32)
	case reflect.Array:
		if err := state.addBytes(collectionOverhead(v.Len())); err != nil {
			return err
		}
		for i := 0; i < v.Len(); i++ {
			if err := preflightObject(v.Index(i), depth+1, seen, state); err != nil {
				return err
			}
		}
		return nil
	case reflect.Slice:
		if v.IsNil() {
			return state.addBytes(4) // null
		}
		vis := visit{typ: v.Type(), ptr: v.Pointer()}
		if seen[vis] {
			return fmt.Errorf("object graph contains a cycle")
		}
		seen[vis] = true
		defer delete(seen, vis)
		if err := state.addBytes(collectionOverhead(v.Len())); err != nil {
			return err
		}
		for i := 0; i < v.Len(); i++ {
			if err := preflightObject(v.Index(i), depth+1, seen, state); err != nil {
				return err
			}
		}
		return nil
	case reflect.Map:
		if v.IsNil() {
			return state.addBytes(4) // null
		}
		if !validJSONMapKey(v.Type().Key()) {
			return fmt.Errorf("map key type %v is not supported by JSON objects", v.Type().Key())
		}
		// Maps do not expose Pointer, but UnsafePointer is the supported
		// reflect identity for map values. It is only used for cycle detection;
		// the original value is still passed to json.Marshal.
		vis := visit{typ: v.Type(), ptr: uintptr(v.UnsafePointer())}
		if seen[vis] {
			return fmt.Errorf("object graph contains a cycle")
		}
		seen[vis] = true
		defer delete(seen, vis)
		// Braces, commas, and one colon per entry. Map keys are JSON strings;
		// preflightObject accounts for their scalar spelling, and the extra two
		// bytes below safely cover quotes for integer keys (and overbound string
		// keys by two bytes).
		if err := state.addBytes(collectionOverhead(v.Len()) + 3*int64(v.Len())); err != nil {
			return err
		}
		iter := v.MapRange()
		for iter.Next() {
			if err := preflightObject(iter.Key(), depth+1, seen, state); err != nil {
				return err
			}
			if err := preflightObject(iter.Value(), depth+1, seen, state); err != nil {
				return err
			}
		}
		return nil
	case reflect.Struct:
		if err := state.addBytes(2); err != nil { // braces
			return err
		}
		t := v.Type()
		fields := 0
		for i := 0; i < v.NumField(); i++ {
			field := t.Field(i)
			name, reachable := objectJSONFieldName(field)
			if !reachable {
				continue
			}
			if fields > 0 {
				if err := state.addBytes(1); err != nil { // comma
					return err
				}
			}
			fields++
			nameBytes := jsonStringSize(name)
			if fallback := jsonStringSize(field.Name); fallback > nameBytes {
				nameBytes = fallback
			}
			if err := state.addBytes(nameBytes + 1); err != nil { // field name and colon
				return err
			}
			if err := preflightObject(v.Field(i), depth+1, seen, state); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported JSON object kind %v", v.Kind())
	}
}

func collectionOverhead(length int) int64 {
	if length == 0 {
		return 2
	}
	return int64(length) + 1 // delimiters and length-1 commas
}

func objectJSONFieldName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("json")
	name, _, _ := strings.Cut(tag, ",")
	if name == "-" {
		return "", false
	}
	if !field.IsExported() && !field.Anonymous {
		return "", false
	}
	if name == "" {
		name = field.Name
	}
	return name, true
}

func jsonStringSize(s string) int64 {
	size := int64(2) // quotes
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			i++
			switch c {
			case '\\', '"', '\b', '\f', '\n', '\r', '\t':
				size += 2
			case '<', '>', '&':
				size += 6 // encoding/json's default HTML escaping
			default:
				if c < 0x20 {
					size += 6
				} else {
					size++
				}
			}
			continue
		}
		r, width := utf8.DecodeRuneInString(s[i:])
		i += width
		if r == utf8.RuneError && width == 1 {
			size += 3 // replacement rune
		} else if r == '\u2028' || r == '\u2029' {
			size += 6
		} else {
			size += int64(width)
		}
	}
	return size
}

func typeHasCallerMethod(t reflect.Type) bool {
	if t == jsonNumberType {
		// The JSON decoder uses json.Number to preserve source spelling.
		// encoding/json handles it as a scalar without calling String.
		return false
	}
	if implementsUnsafeObjectMethod(t) {
		return true
	}
	if t.Kind() != reflect.Pointer && implementsUnsafeObjectMethod(reflect.PointerTo(t)) {
		return true
	}
	return false
}

func implementsUnsafeObjectMethod(t reflect.Type) bool {
	return t.Implements(jsonMarshalerType) ||
		t.Implements(textMarshalerType) ||
		t.Implements(stringerType) ||
		t.Implements(errorType)
}

func validJSONMapKey(t reflect.Type) bool {
	if typeHasCallerMethod(t) {
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

// ObjectsEnabled reports whether object-valued variables are available, which
// is true only under the bash++ dialect ([syntax.LangBashPP]). Every other
// variant, including the zero value [syntax.LangBash], returns false.
//
// This is the single source of truth for the gate. The interpreter consults it
// on the assignment path and refuses to create an [Object] when it is false, so
// that under any other dialect an object simply cannot come into existence and
// the rest of expansion never has to account for one.
func (cfg *Config) ObjectsEnabled() bool {
	return cfg.Lang == syntax.LangBashPP
}
