package interp

import (
	"go/ast"
	"go/parser"
	"go/token"
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
			name: "deferred callback",
			source: `package dep
type ErrorHandler func(error)
func Parse(errh ErrorHandler) { defer errh(nil) }
`,
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
			name: "nested deferred factory receiver",
			source: `package dep
type holder struct { f func() }
func factory(h *holder) func() { return h.f }
func Parse(cb func()) { h := &holder{f: cb}; defer factory(h)() }
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
			name: "stable recursive synchronous callback",
			source: `package dep
func walk(cb func(), n int) { if n > 0 { walk(cb, n-1) } else { cb() } }
func Parse(cb func()) { walk(cb, 1) }
`,
			want: true,
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
