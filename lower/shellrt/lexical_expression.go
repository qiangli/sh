package shellrt

import (
	"fmt"
	"go/constant"
	"go/token"
	"reflect"
)

// LexicalPrint observes a typed binding without narrowing shell scalar text.
func LexicalPrint(b *LexicalBindings, id, fallback string, site ValueSite) any {
	if slot := b.slot(id); slot != nil && *slot.present && !slot.raw.present && slot.raw.nativeScalar != nil && slot.raw.nativeScalar.Kind() == constant.Float {
		return slot.raw.nativeScalar.ExactString()
	}
	value, present, err := b.PrintValue(id, site)
	if err != nil {
		panic(ValueAbort{Err: err})
	}
	return BindingValue(present, value, fallback)
}
func LexicalPrintAddress[T any](b *LexicalBindings, address *T, fallback string, site ValueSite) any {
	if slot := b.slotAt(address); slot != nil {
		view := NewLexicalBindings()
		view.store.slots["value"] = slot
		return LexicalPrint(view, "value", fallback, site)
	}
	return *MustValue(CheckedPointer(address, site))
}

// LexicalOperand validates conversion but retains the original scalar width.
// Validation must happen before an operator; Print deliberately omits it.
func LexicalOperand[T any](b *LexicalBindings, id string, site ValueSite) constant.Value {
	_ = MustValue(Load[T](b, id, site))
	value, _, err := b.ScalarValue(id, site)
	return MustValue(value, err)
}
func LexicalOperandAddress[T any](b *LexicalBindings, address *T, site ValueSite) constant.Value {
	_ = MustValue(LoadAddress(b, address, site))
	if slot := b.slotAt(address); slot != nil {
		view := NewLexicalBindings()
		view.store.slots["value"] = slot
		value, _, err := view.ScalarValue("value", site)
		return MustValue(value, err)
	}
	return LexicalNative(*address)
}
func LexicalNative[T any](value T) constant.Value {
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Bool:
		return constant.MakeBool(v.Bool())
	case reflect.String:
		return constant.MakeString(v.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return constant.MakeInt64(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return constant.MakeUint64(v.Uint())
	case reflect.Float32, reflect.Float64:
		return constant.MakeFloat64(v.Float())
	}
	panic("compiler supplied non-scalar lexical operand")
}
func LexicalLiteral(text string, kind token.Token) constant.Value {
	return constant.MakeFromLiteral(text, kind, 0)
}

// LexicalBinary executes one compiler-selected scalar operator, then narrows
// its result to the native Go type. Operands remain in source evaluation order.
func LexicalBinary[T any](left, right constant.Value, op token.Token, site ValueSite) T {
	return LexicalEvaluate[T](left, right, op, site).Value
}

// LexicalEvaluated retains the exact scalar alongside its addressable native value.
type LexicalEvaluated[T any] struct {
	Value  T
	Scalar constant.Value
}

func LexicalEvaluate[T any](left, right constant.Value, op token.Token, site ValueSite) LexicalEvaluated[T] {
	defer func() {
		if failure := recover(); failure != nil {
			if _, ok := failure.(ValueAbort); ok {
				panic(failure)
			}
			code, message := "BASHPP-EEXPR-OPERAND", fmt.Sprintf("operator %s not defined on %s and %s", op, left.Kind(), right.Kind())
			if op == token.SHL || op == token.SHR {
				code, message = "BASHPP-EEXPR-SHIFT", "shift requires integer operands"
			}
			panic(ValueAbort{Err: &ValueError{Code: code, Message: message, Site: site}})
		}
	}()
	var result constant.Value
	switch op {
	case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
		result = constant.MakeBool(constant.Compare(left, op, right))
	case token.QUO, token.REM:
		if constant.Sign(right) == 0 {
			panic(ValueAbort{Err: &ValueError{Code: "BASHPP-EEXPR-DIVZERO", Message: "division by zero", Site: site}})
		}
		result = constant.BinaryOp(left, op, right)
	case token.SHL, token.SHR:
		if right.Kind() != constant.Int {
			panic(ValueAbort{Err: &ValueError{Code: "BASHPP-EEXPR-SHIFT", Message: "shift count must be an unsigned integer", Site: site}})
		}
		shift, ok := constant.Uint64Val(right)
		if !ok {
			panic(ValueAbort{Err: &ValueError{Code: "BASHPP-EEXPR-SHIFT", Message: "shift count must be an unsigned integer", Site: site}})
		}
		result = constant.Shift(left, op, uint(shift))
	default:
		result = constant.BinaryOp(left, op, right)
	}
	slot := &lexicalSlot{value: reflect.New(reflect.TypeFor[T]()).Elem(), raw: &lexicalRaw{}}
	value, err := lexicalNativeScalar(slot, result, site)
	return LexicalEvaluated[T]{Value: MustValue(valueInterface[T](value, err)), Scalar: result}
}
func valueInterface[T any](value reflect.Value, err error) (T, error) {
	if err != nil {
		var zero T
		return zero, err
	}
	return value.Interface().(T), nil
}

// LexicalExact carries a compiler-checked constant spelling, including the
// rational form produced by go/constant. It is never shell input conversion.
func LexicalExact(text string) constant.Value {
	for i, c := range text {
		if c == '/' {
			return constant.BinaryOp(constant.MakeFromLiteral(text[:i], token.INT, 0), token.QUO, constant.MakeFromLiteral(text[i+1:], token.INT, 0))
		}
	}
	value := constant.MakeFromLiteral(text, token.INT, 0)
	if value.Kind() != constant.Unknown {
		return value
	}
	return constant.MakeFromLiteral(text, token.FLOAT, 0)
}

// LexicalSourceType records the compiler's source name without replacing
// declaration attributes or raw spelling already carried by the slot.
func LexicalSourceType(b *LexicalBindings, id, name string) error {
	slot := b.slot(id)
	if slot == nil {
		return &LexicalWriteError{id, "unknown binding ID"}
	}
	slot.raw.info.SourceType = name
	return nil
}
