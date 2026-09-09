package interp

// Sprint: #118; Story: #56; Story-ID: 3ef468f4e831
import (
	"fmt"
	"mvdan.cc/sh/v3/syntax"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"
)

// GoSourceTest identifies an original test declaration, not a source-file count.
type GoSourceTest struct {
	Name, Source string
	Line, Column uint
}

// Tests discovers every top-level Test function in the original typed AST.
// Registration does not execute any body and does not count dynamic subtests.
func (s *GoSourceTestingSession) Tests() ([]GoSourceTest, error) {
	if s == nil || s.closed || s.runner.goSourceTesting != s {
		return nil, fmt.Errorf("gosource: testing session is closed or reset")
	}
	var tests []GoSourceTest
	for _, stmt := range s.program.File.Stmts {
		decl, ok := stmt.Cmd.(*syntax.BashPPFuncDecl)
		if !ok || decl.Receiver != nil || decl.Name == nil {
			continue
		}
		name := decl.Name.Value
		if !strings.HasPrefix(name, "Test") {
			continue
		}
		tail := strings.TrimPrefix(name, "Test")
		if tail != "" {
			first, _ := utf8.DecodeRuneInString(tail)
			if unicode.IsLower(first) {
				continue
			}
		}
		source, _, ok := s.program.SourceAt(decl.Pos())
		if !ok {
			return nil, fmt.Errorf("gosource: test %s lacks original source identity", name)
		}
		tests = append(tests, GoSourceTest{Name: name, Source: source, Line: decl.Pos().Line(), Column: decl.Pos().Col()})
	}
	return tests, nil
}

// Reflection only adapts testing's concrete callback type; it never executes
// original test code. testing remains owned by the host harness, whose Run and
// Cleanup implementations decide the callback goroutine and cleanup order.
func (r *Runner) bashPPTestingCallback(handle *goSourceTestingHandle, method string, args []any) error {
	native := reflect.ValueOf(handle.target).MethodByName(method)
	if !native.IsValid() {
		return fmt.Errorf("gosource: scheduler does not provide testing.%s", method)
	}
	if method == "Run" {
		if len(args) != 2 {
			return fmt.Errorf("gosource: testing.Run requires a name and callback")
		}
		name, ok := args[0].(string)
		if !ok {
			return fmt.Errorf("gosource: testing.Run name must be a string")
		}
		fn, ok := args[1].(*bashPPFunc)
		if !ok {
			return fmt.Errorf("gosource: testing.Run callback must be interpreted")
		}
		if native.Type().NumIn() != 2 || native.Type().In(1).Kind() != reflect.Func || native.Type().In(1).NumIn() != 1 {
			return fmt.Errorf("gosource: invalid scheduler Run signature")
		}
		ctx := r.ectx
		callback := reflect.MakeFunc(native.Type().In(1), func(values []reflect.Value) []reflect.Value {
			target, ok := values[0].Interface().(GoSourceTestingT)
			if !ok {
				panic("gosource: scheduler supplied invalid test capability")
			}
			if err := handle.session.runFunction(ctx, name, fn, target, true, nil); err != nil {
				target.Errorf("interpreted callback: %v", err)
			}
			return nil
		})
		native.Call([]reflect.Value{reflect.ValueOf(name), callback})
		return nil
	}
	if len(args) != 1 {
		return fmt.Errorf("gosource: testing.Cleanup requires a callback")
	}
	fn, ok := args[0].(*bashPPFunc)
	if !ok {
		return fmt.Errorf("gosource: testing.Cleanup callback must be interpreted")
	}
	if native.Type().NumIn() != 1 || native.Type().In(0).Kind() != reflect.Func || native.Type().In(0).NumIn() != 0 {
		return fmt.Errorf("gosource: invalid scheduler Cleanup signature")
	}
	// The root callback's signal scope ends before Cleanup. The package session
	// lifetime remains valid until the harness closes the session.
	ctx := handle.session.context
	callback := reflect.MakeFunc(native.Type().In(0), func(_ []reflect.Value) []reflect.Value {
		if err := handle.session.runFunction(ctx, "cleanup", fn, handle.target, true, handle); err != nil {
			handle.target.Errorf("interpreted cleanup: %v", err)
		}
		return nil
	})
	native.Call([]reflect.Value{callback})
	return nil
}
