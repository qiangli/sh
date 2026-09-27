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
