package interp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

// Sprint: #281; Story: #810; Story-ID: 48c1146a3ab0
func TestDependencyFunctionCallbackLifetimeProof(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   bool
	}{
		{
			name: "parser stack local callback",
			source: `package dep
type ErrorHandler func(error)
type source struct { errh ErrorHandler }
func (s *source) init(errh ErrorHandler) { s.errh = errh }
func (s *source) fail(err error) { if s.errh != nil { s.errh(err) } }
type scanner struct { source }
func (s *scanner) init(errh ErrorHandler) { s.source.init(errh) }
func (s *scanner) next() { s.fail(nil) }
type parser struct { scanner; errh ErrorHandler }
func (p *parser) init(errh ErrorHandler) {
	p.errh = errh
	p.scanner.init(func(err error) { p.errorAt(err) })
}
func (p *parser) errorAt(err error) { if p.errh != nil { p.errh(err) } }
func Parse(errh ErrorHandler) { var p parser; p.init(errh); p.next() }
`,
			want: true,
		},
		{
			name: "global retention",
			source: `package dep
type ErrorHandler func(error)
var retained ErrorHandler
func Parse(errh ErrorHandler) { retained = errh }
`,
		},
		{
			name: "goroutine",
			source: `package dep
type ErrorHandler func(error)
func Parse(errh ErrorHandler) { go errh(nil) }
`,
		},
		{
			name: "local container escaped before storage",
			source: `package dep
type ErrorHandler func(error)
type holder struct { callback ErrorHandler }
var saved *holder
func keep(h *holder) { saved = h }
func Parse(errh ErrorHandler) { var h holder; keep(&h); h.callback = errh }
`,
		},
		{
			name: "deferred callback runs before return",
			source: `package dep
type ErrorHandler func(error)
func Parse(errh ErrorHandler) { defer errh(nil) }
`,
			want: true,
		},
		{
			name: "unknown callee",
			source: `package dep
type ErrorHandler func(error)
func Parse(errh ErrorHandler) { external(errh) }
`,
		},
		{
			name: "conditional retention",
			source: `package dep
var saved func()
func Parse(cb func(), choose bool) { var f func(); if choose { f = cb }; saved = f }
`,
		},
		{
			name: "loop retention",
			source: `package dep
var saved func()
func Parse(cb func(), choose bool) { var f func(); for choose { f = cb; break }; saved = f }
`,
		},
		{
			name: "block retention",
			source: `package dep
var saved func()
func Parse(cb func()) { var f func(); { f = cb }; saved = f }
`,
		},
		{
			name: "switch retention",
			source: `package dep
var saved func()
func Parse(cb func(), choose bool) { var f func(); switch { case choose: f = cb }; saved = f }
`,
		},
		{
			name: "pointer alias branch clearing cannot sanitize",
			source: `package dep
type holder struct { f func() }
var saved *holder
func Parse(cb func(), choose bool) { h := &holder{}; alias := h; h.f = cb; if choose { alias.f = nil }; saved = h }
`,
		},
		{
			name: "named callback result naked return",
			source: `package dep
func Parse(cb func()) (out func()) { out = cb; return }
`,
		},
		{
			name: "conditional returned closure",
			source: `package dep
var saved func()
func wrap(cb func(), choose bool) func() { if choose { return func() { cb() } }; return nil }
func Parse(cb func(), choose bool) { saved = wrap(cb, choose) }
`,
		},
		{
			name: "async method on tainted receiver",
			source: `package dep
type holder struct { f func() }
func (h *holder) run() { h.f() }
func Parse(cb func()) { h := &holder{f: cb}; go h.run() }
`,
		},
		{
			name: "nested deferred local factory receiver",
			source: `package dep
type holder struct { f func() }
func factory(h *holder) func() { return h.f }
func Parse(cb func()) { h := &holder{f: cb}; defer factory(h)() }
`,
			want: true,
		},
		{
			name: "deferred closure actual retention",
			source: `package dep
var saved func()
func Parse(cb func()) { defer func() { saved = cb }() }
`,
		},
		{
			name: "deferred closure parameter actual retention",
			source: `package dep
var saved func()
func Parse(cb func()) { defer func(f func()) { saved = f }(cb) }
`,
		},
		{
			name: "deferred closure parameter synchronous call",
			source: `package dep
func Parse(cb func()) { defer func(f func()) { f() }(cb) }
`,
			want: true,
		},
		{
			name: "deferred unknown callee",
			source: `package dep
func Parse(cb func()) { defer external(cb) }
`,
		},
		{
			name: "deferred factory actual retention",
			source: `package dep
var saved func()
func factory(cb func()) func() { saved = cb; return func() {} }
func Parse(cb func()) { defer factory(cb)() }
`,
		},
		{
			name: "deferred late variable retention",
			source: `package dep
var saved func()
func Parse(cb func()) { var f func(); defer func() { saved = f }(); f = cb }
`,
		},
		{
			name: "deferred late field retention",
			source: `package dep
var saved func()
func Parse(cb func()) { h := &struct{ f func() }{}; defer func() { saved = h.f }(); h.f = cb }
`,
		},
		{
			name: "deferred lifo mutation retention",
			source: `package dep
var saved func()
func Parse(cb func()) { var f func(); defer func() { saved = f }(); defer func() { f = cb }() }
`,
		},
		{
			name: "variadic callback retention",
			source: `package dep
var saved func()
func keep(prefix string, callbacks ...func()) { saved = callbacks[0] }
func Parse(cb func()) { keep("callback", cb) }
`,
		},
		{
			name: "variadic synchronous callback",
			source: `package dep
func invoke(prefix string, callbacks ...func()) { callbacks[0]() }
func Parse(cb func()) { invoke("callback", cb) }
`,
			want: true,
		},
		{
			name: "unnamed callback parameter",
			source: `package dep
func discard(func()) {}
func Parse(cb func()) { discard(cb) }
`,
			want: true,
		},
		{
			name: "grouped callback parameters",
			source: `package dep
func invoke(first, second func()) { first(); second() }
func Parse(cb func()) { invoke(cb, func() {}) }
`,
			want: true,
		},
		{
			name: "grouped callback retention",
			source: `package dep
var saved func()
func keep(first, second func()) { saved = second }
func Parse(cb func()) { keep(func() {}, cb) }
`,
		},
		{
			name: "syntax parser trace-shaped defer",
			source: `package dep
const trace = false
type source struct { errh func(error) }
func (s *source) init(errh func(error)) { s.errh = errh }
type scanner struct { source }
type parser struct { scanner }
func (p *parser) init(cb func(error)) { p.scanner.source.init(cb) }
func (p *parser) trace(string) func() { _ = p.scanner.source.errh; return func() { _ = p } }
func (p *parser) parse() { if trace { defer p.trace("file")() } }
func Parse(cb func(error)) { var p parser; p.init(cb); p.parse() }
`,
			want: true,
		},
		{
			name: "clean function field on callback-bearing receiver",
			source: `package dep
type Handler func(int)
type source struct { callback func() }
func (s *source) pos() int { return 1 }
type parser struct { source; handler Handler }
func (p *parser) clear() { p.handler(p.pos()) }
func Parse(cb func(), handler Handler) { var p parser; p.callback = cb; p.handler = handler; p.clear() }
`,
			want: true,
		},
		{
			name: "function field cannot receive callback",
			source: `package dep
type Handler func(func())
type parser struct { callback func(); handler Handler }
func (p *parser) pass() { p.handler(p.callback) }
func Parse(cb func(), handler Handler) { var p parser; p.callback = cb; p.handler = handler; p.pass() }
`,
		},
		{
			name: "unknown field on callback-bearing receiver",
			source: `package dep
type parser struct { callback func() }
func (p *parser) pos() int { return 1 }
func (p *parser) call() { p.unknown(p.pos()) }
func Parse(cb func()) { var p parser; p.callback = cb; p.call() }
`,
		},
		{
			name: "recursive callback substitution is unproved",
			source: `package dep
func walk(cb func()) { next := func() { cb() }; walk(next) }
func Parse(cb func()) { walk(cb) }
`,
		},
		{
			name: "recursive receiver field mutation is unproved",
			source: `package dep
var saved func()
type holder struct{ f func() }
func (h *holder) run(cb func(), choose bool) {
	if h.f != nil {
		saved = h.f
		return
	}
	h.f = cb
	if choose {
		h.run(cb, false)
	}
}
func Parse(cb func(), choose bool) {
	h := &holder{}
	h.run(cb, choose)
}
`,
		},
		{
			name: "recursive actual callback field mutation is unproved",
			source: `package dep
var saved func()
type holder struct{ f func() }
func mutate(h *holder, cb func(), choose bool) {
	h.f = cb
	if choose {
		mutate(h, cb, false)
	}
}
func inspect(h *holder) {
	if h.f != nil {
		saved = h.f
	}
}
func Parse(cb func(), choose bool) {
	h := &holder{}
	mutate(h, cb, choose)
	inspect(h)
}
`,
		},
		{
			name: "recursive captured environment mutation is unproved",
			source: `package dep
var saved func()
type holder struct{ f func() }
func run(invoke func(), h *holder, cb func(), choose bool) {
	h.f = cb
	if choose {
		run(invoke, h, cb, false)
		return
	}
	invoke()
}
func Parse(cb func(), choose bool) {
	h := &holder{}
	invoke := func() {
		if h.f != nil {
			saved = h.f
		}
	}
	run(invoke, h, cb, choose)
}
`,
		},
		{
			name: "recursive closure capture is unproved",
			source: `package dep
var saved func()
func run(invoke func(), cb func(), choose bool) {
	next := func() { invoke(); cb() }
	if choose {
		run(next, cb, false)
		return
	}
	next()
}
func Parse(cb func(), choose bool) {
	run(func(){}, cb, choose)
}
`,
		},
		{
			name: "alias topology can introduce callback reachability",
			source: `package dep
var saved func()
type H struct{ dst *H; f func() }
func Retain(cb func()) {
	h := &H{}
	other := &H{}
	h.dst = other
	other.f = cb
	saved = h.dst.f
}
func Parse(cb func()) { Retain(cb) }
`,
		},
		{
			name: "whole pointer overwrite preserves aliases",
			source: `package dep
var saved func()
type holder struct{ f func() }
func Retain(cb func()) {
	h := &holder{}
	alias := h
	*alias = holder{f: cb}
	saved = h.f
}
func Parse(cb func()) { Retain(cb) }
`,
		},
		{
			name: "slice element alias later taint",
			source: `package dep
var saved func()
type holder struct{ f func() }
func Retain(cb func()) {
	h := &holder{}
	xs := []*holder{h}
	h.f = cb
	saved = xs[0].f
}
func Parse(cb func()) { Retain(cb) }
`,
		},
		{
			name: "map element alias later taint",
			source: `package dep
var saved func()
type holder struct{ f func() }
func Retain(cb func()) {
	h := &holder{}
	xs := map[int]*holder{0: h}
	h.f = cb
	saved = xs[0].f
}
func Parse(cb func()) { Retain(cb) }
`,
		},
		{
			name: "escaped aggregate preserves reachable element",
			source: `package dep
var saved []*holder
type holder struct{ f func() }
func keep(xs []*holder) { saved = xs }
func Retain(cb func()) {
	h := &holder{}
	xs := []*holder{h}
	keep(xs)
	h.f = cb
}
func Parse(cb func()) { Retain(cb) }
`,
		},
		{
			name: "escaped clean child cannot later receive callback",
			source: `package dep
var saved *child
type child struct{ f func() }
type holder struct{ child *child }
func keep(c *child) { saved = c }
func (h *holder) walk(cb func(), choose bool) {
	keep(h.child)
	if choose {
		h.walk(cb, false)
		return
	}
	h.child.f = cb
}
func Parse(cb func(), choose bool) {
	h := &holder{}
	h.walk(cb, choose)
}
`,
		},
		{
			name: "stable recursive synchronous callback",
			source: `package dep
func walk(cb func(), n int) { if n > 0 { walk(cb, n-1) } else { cb() } }
func Parse(cb func()) { walk(cb, 1) }
`,
			want: true,
		},
		{
			name: "value returning closure synchronously invokes callback",
			source: `package dep
func list(each func() bool) { _ = each() }
func Parse(cb func()) { list(func() bool { cb(); return true }) }
`,
			want: true,
		},
		{
			name: "value returning closure still cannot return callback",
			source: `package dep
var saved func()
func list(each func() func()) { saved = each() }
func Parse(cb func()) { list(func() func() { return cb }) }
`,
		},
		{
			name: "lexical local shadows global",
			source: `package dep
type ErrorHandler func(error)
var retained ErrorHandler
func Parse(errh ErrorHandler) { var retained ErrorHandler; retained = errh; retained(nil) }
`,
			want: true,
		},
		{
			name: "nested short declaration shadows outer",
			source: `package dep
func Parse(cb func()) { var f func(); { f := cb; f() }; _ = f }
`,
			want: true,
		},
		{
			name: "conditional synchronous callback",
			source: `package dep
func Parse(cb func(), choose bool) { if choose { cb() } }
`,
			want: true,
		},
		{
			name: "loop synchronous callback",
			source: `package dep
func Parse(cb func(), choose bool) { for choose { cb(); choose = false } }
`,
			want: true,
		},
		{
			name: "completed frame later object taint",
			source: `package dep
var saved func()
type holder struct { f func() }
func inspect(h *holder) { if h.f != nil { saved = h.f } }
func Parse(cb func()) { h := &holder{f: func(){}}; inspect(h); h.f = cb; inspect(h) }
`,
		},
		{
			name: "completed frame later closure capture",
			source: `package dep
var saved func()
func inspect(f func()) { if f != nil { saved = f } }
func Parse(cb func()) { inspect(func(){}); inspect(func(){ cb() }) }
`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "dep.go", test.source, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			got := newDependencyCallbackProof([]*ast.File{file}).prove("Parse", []int{0})
			if got != test.want {
				t.Fatalf("proof=%v, want %v", got, test.want)
			}
		})
	}
}

func TestDependencyCallbackProofCompilerSyntaxSource(t *testing.T) {
	dir := filepath.Join(runtime.GOROOT(), "src", "cmd", "compile", "internal", "syntax")
	selected := []string{
		"syntax.go",
		"parser.go",
		"scanner.go",
		"source.go",
		"branches.go",
		"tokens.go",
	}
	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(selected))
	for _, name := range selected {
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		files = append(files, file)
	}
	for _, test := range []struct {
		name string
		fn   string
		args []int
	}{
		{name: "Parse", fn: "Parse", args: []int{2}},
		{name: "ParseFile", fn: "ParseFile", args: []int{1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			proof := newDependencyCallbackProof(files)
			proof.fset = fset
			if !proof.prove(test.fn, test.args) {
				t.Fatalf("%s callback proof refused: %s", test.fn, proof.reason)
			}
		})
	}
}
