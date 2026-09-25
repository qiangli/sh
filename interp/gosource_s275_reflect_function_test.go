//go:build full

package interp_test

import (
	"strings"
	"testing"
)

func TestS275ReflectedOriginalFunctionCall(t *testing.T) {
	src := `package main
import "reflect"
func f(next func() bool) { if next() { println("called") } }
func main() {
	next := reflect.MakeFunc(reflect.TypeOf((func() bool)(nil)), func(_ []reflect.Value) []reflect.Value {
		return []reflect.Value{reflect.ValueOf(true)}
	})
	reflect.ValueOf(f).Call([]reflect.Value{next})
}`
	_, stderr, err := runGoSource(t, "s275-reflect-function-call", src)
	if err != nil || stderr != "called\n" {
		t.Fatalf("reflected original call: err=%v stderr=%q", err, stderr)
	}
}

func TestS275ReflectedOriginalFunctionTypeAndCall(t *testing.T) {
	src := `package main
import "reflect"
func main() {
	var fn any = func(x int, s int) int { return x << s }
	v := reflect.ValueOf(fn)
	x := reflect.ValueOf(1).Convert(v.Type().In(0))
	v.Call([]reflect.Value{x, reflect.ValueOf(2)})
	println("called")
}`
	_, stderr, err := runGoSource(t, "s275-reflect-function-type-call", src)
	if err != nil || stderr != "called\n" {
		t.Fatalf("reflected original type and call: err=%v stderr=%q", err, stderr)
	}
}

func TestS275ReflectedOriginalFunctionRetainerRefused(t *testing.T) {
	src := `package main
import (
	"reflect"
	"time"
)
func f() { println("called") }
func main() {
	r := reflect.ValueOf(f)
	time.AfterFunc(time.Hour, r.Interface().(func()))
	println("after")
}`
	out, stderr, err := runGoSource(t, "s275-reflect-function-retainer", src)
	if err == nil || !strings.Contains(err.Error()+stderr, "only supports synchronous Call") || strings.Contains(out+stderr, "after") {
		t.Fatalf("retained reflected original call: out=%q stderr=%q err=%v", out, stderr, err)
	}
}

func TestS275ReflectedOriginalFunctionPointer(t *testing.T) {
	src := `package main
import (
	"reflect"
	"runtime"
)
func f(n int) int { return n }
func name(fn any) string { return runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name() }
func main() {
	lit := func() {}
	println(name(f), name(lit))
	pc := reflect.ValueOf(f).Pointer()
	fn := runtime.FuncForPC(pc)
	_, line := fn.FileLine(pc)
	println(line, fn.Entry() == pc, runtime.FuncForPC(pc+1) == fn, runtime.FuncForPC(pc+1) != fn)
	println(reflect.ValueOf(f).Pointer() == pc, reflect.ValueOf(lit).Pointer() == pc)
}`
	_, stderr, err := runGoSource(t, "s275-reflect-function-pointer", src)
	if want := "main.f main.main.func1\n6 true true false\ntrue false\n"; err != nil || stderr != want {
		t.Fatalf("reflected original pointer: err=%v stderr=%q want %q", err, stderr, want)
	}
}
