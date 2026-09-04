// Copyright (c) 2026, the bash++ authors.
// See LICENSE for licensing information.

package syntax

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-quicktest/qt"
)

// wrap puts a body inside a Bash++ func so the channel forms are inside a
// committed Go region, which is the only place they are recognized.
func bashppWrapFunc(body string) string { return "func f() {\n\t" + body + "\n}\n" }

// bashppRoundTrip asserts that src parses, prints back byte for byte, and
// reparses to the same printed bytes. The second print is what catches a node
// whose positions are plausible but whose printer drops or invents a token.
func bashppRoundTrip(t *testing.T, src string) *File {
	t.Helper()
	f, err := bashppParse(LangBashPP, src)
	qt.Assert(t, qt.IsNil(err))
	out, err := bashppPrint(f)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(out, src))
	again, err := bashppParse(LangBashPP, out)
	qt.Assert(t, qt.IsNil(err))
	out2, err := bashppPrint(again)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(out2, src))
	return f
}

// TestBashPPChanFormsExactAST pins the tree each P4 channel spelling builds,
// including the positions, because every one of these nodes is assembled from
// a redirect the shell parser produced rather than lexed directly. A node that
// looked right but pointed at the wrong column would print correctly and still
// give the interpreter and every diagnostic the wrong source span.
func TestBashPPChanFormsExactAST(t *testing.T) {
	t.Parallel()

	t.Run("make", func(t *testing.T) {
		f := bashppRoundTrip(t, bashppWrapFunc("results := make(chan int, 4)"))
		decl := f.Stmts[0].Cmd.(*BashPPFuncDecl).Body.Stmts[0].Cmd.(*BashPPShortDecl)
		qt.Assert(t, qt.Equals(len(decl.Lhs), 1))
		qt.Assert(t, qt.Equals(decl.Lhs[0].Value, "results"))
		qt.Assert(t, qt.Equals(decl.Class, ClassR))
		qt.Assert(t, qt.IsNotNil(decl.MakeChan))
		mk := decl.MakeChan
		qt.Assert(t, qt.Equals(mk.Make.Value, "make"))
		qt.Assert(t, qt.Equals(mk.ChanType.Elem.Value, "int"))
		qt.Assert(t, qt.Equals(mk.Capacity.Lit(), "4"))
		// `\tresults := make(chan int, 4)` starts at offset 11.
		qt.Assert(t, qt.Equals(mk.Make.Pos().Col(), uint(13)))
		qt.Assert(t, qt.Equals(mk.ChanType.Chan.Col(), uint(18)))
		qt.Assert(t, qt.Equals(mk.Rparen.Col(), uint(29)))
		qt.Assert(t, qt.Equals(decl.End(), posAddCol(mk.Rparen, 1)))
	})

	t.Run("make unbuffered", func(t *testing.T) {
		f := bashppRoundTrip(t, bashppWrapFunc("ready := make(chan bool)"))
		mk := f.Stmts[0].Cmd.(*BashPPFuncDecl).Body.Stmts[0].Cmd.(*BashPPShortDecl).MakeChan
		qt.Assert(t, qt.Equals(mk.ChanType.Elem.Value, "bool"))
		qt.Assert(t, qt.IsNil(mk.Capacity))
	})

	t.Run("make into shell reserved word", func(t *testing.T) {
		const src = "func f() {\n\tdone := make(chan bool)\n}\n"
		f := bashppRoundTrip(t, src)
		decl := f.Stmts[0].Cmd.(*BashPPFuncDecl).Body.Stmts[0].Cmd.(*BashPPShortDecl)
		qt.Assert(t, qt.Equals(decl.Lhs[0].Value, "done"))
		qt.Assert(t, qt.Equals(decl.MakeChan.ChanType.Elem.Value, "bool"))
	})

	t.Run("send", func(t *testing.T) {
		f := bashppRoundTrip(t, bashppWrapFunc("results <- 1"))
		send := f.Stmts[0].Cmd.(*BashPPFuncDecl).Body.Stmts[0].Cmd.(*BashPPSend)
		qt.Assert(t, qt.Equals(send.Chan.Lit(), "results"))
		qt.Assert(t, qt.Equals(send.Value.Lit(), "1"))
		qt.Assert(t, qt.Equals(send.Chan.Pos().Col(), uint(2)))
		qt.Assert(t, qt.Equals(send.Arrow.Col(), uint(10)))
		qt.Assert(t, qt.Equals(send.Value.Pos().Col(), uint(13)))
		qt.Assert(t, qt.Equals(send.End().Col(), uint(14)))
		// The redirect the shell parser built is consumed by the node.
		qt.Assert(t, qt.HasLen(f.Stmts[0].Cmd.(*BashPPFuncDecl).Body.Stmts[0].Redirs, 0))
	})

	t.Run("standalone receive", func(t *testing.T) {
		f := bashppRoundTrip(t, bashppWrapFunc("<-results"))
		stmt := f.Stmts[0].Cmd.(*BashPPFuncDecl).Body.Stmts[0]
		recv := stmt.Cmd.(*BashPPReceive)
		qt.Assert(t, qt.HasLen(stmt.Redirs, 0))
		qt.Assert(t, qt.Equals(recv.Arrow.Col(), uint(2)))
		qt.Assert(t, qt.Equals(recv.Chan.Lit(), "results"))
		// The channel literal is split back out of `-results` with the
		// columns it actually occupies, not the dash's.
		qt.Assert(t, qt.Equals(recv.Chan.Pos().Col(), uint(4)))
		qt.Assert(t, qt.Equals(recv.End().Col(), uint(11)))
	})

	t.Run("one-value receive", func(t *testing.T) {
		f := bashppRoundTrip(t, bashppWrapFunc("v := <-results"))
		decl := f.Stmts[0].Cmd.(*BashPPFuncDecl).Body.Stmts[0].Cmd.(*BashPPShortDecl)
		qt.Assert(t, qt.HasLen(decl.Lhs, 1))
		qt.Assert(t, qt.Equals(decl.Lhs[0].Value, "v"))
		qt.Assert(t, qt.HasLen(decl.Rhs, 0))
		qt.Assert(t, qt.IsNotNil(decl.Recv))
		qt.Assert(t, qt.Equals(decl.Recv.Chan.Lit(), "results"))
		qt.Assert(t, qt.Equals(decl.OpPos.Col(), uint(4)))
		qt.Assert(t, qt.Equals(decl.Recv.Arrow.Col(), uint(7)))
		qt.Assert(t, qt.Equals(decl.End(), decl.Recv.End()))
	})

	t.Run("two-value receive", func(t *testing.T) {
		f := bashppRoundTrip(t, bashppWrapFunc("v, ok := <-results"))
		decl := f.Stmts[0].Cmd.(*BashPPFuncDecl).Body.Stmts[0].Cmd.(*BashPPShortDecl)
		qt.Assert(t, qt.HasLen(decl.Lhs, 2))
		qt.Assert(t, qt.Equals(decl.Lhs[0].Value, "v"))
		qt.Assert(t, qt.Equals(decl.Lhs[1].Value, "ok"))
		// The comma is not part of the name it follows.
		qt.Assert(t, qt.Equals(decl.Lhs[0].End().Col(), uint(3)))
		qt.Assert(t, qt.Equals(decl.Lhs[1].Pos().Col(), uint(5)))
		qt.Assert(t, qt.IsNotNil(decl.Recv))
		qt.Assert(t, qt.Equals(decl.Recv.Chan.Lit(), "results"))
	})

	t.Run("close", func(t *testing.T) {
		f := bashppRoundTrip(t, bashppWrapFunc("close(results)"))
		cl := f.Stmts[0].Cmd.(*BashPPFuncDecl).Body.Stmts[0].Cmd.(*BashPPClose)
		qt.Assert(t, qt.Equals(cl.Kw.Value, "close"))
		qt.Assert(t, qt.Equals(cl.Chan.Lit(), "results"))
		qt.Assert(t, qt.Equals(cl.Pos().Col(), uint(2)))
	})

	t.Run("go call", func(t *testing.T) {
		f := bashppRoundTrip(t, "go worker(1, 2)\n")
		g := f.Stmts[0].Cmd.(*BashPPGo)
		qt.Assert(t, qt.Equals(g.Kw.Value, "go"))
		qt.Assert(t, qt.HasLen(g.Call.Fun, 1))
		qt.Assert(t, qt.Equals(g.Call.Fun[0].Value, "worker"))
		qt.Assert(t, qt.HasLen(g.Call.Args, 2))
		qt.Assert(t, qt.Equals(g.Pos().Col(), uint(1)))
		qt.Assert(t, qt.Equals(g.End().Col(), uint(16)))
	})

	t.Run("go func invocation", func(t *testing.T) {
		f := bashppRoundTrip(t, bashppWrapFunc("go func(x int) {\n\t\techo $x\n\t}(1)"))
		g := f.Stmts[0].Cmd.(*BashPPFuncDecl).Body.Stmts[0].Cmd.(*BashPPGo)
		qt.Assert(t, qt.HasLen(g.Call.Fun, 0))
		qt.Assert(t, qt.IsNotNil(g.Call.FuncLit))
		qt.Assert(t, qt.HasLen(g.Call.FuncLit.Params, 1))
		qt.Assert(t, qt.HasLen(g.Call.Args, 1))
	})

	t.Run("range", func(t *testing.T) {
		f := bashppRoundTrip(t, bashppWrapFunc("for v := range results {\n\t\techo $v\n\t}"))
		rng := f.Stmts[0].Cmd.(*BashPPFuncDecl).Body.Stmts[0].Cmd.(*BashPPRange)
		qt.Assert(t, qt.HasLen(rng.Names, 1))
		qt.Assert(t, qt.Equals(rng.Names[0].Value, "v"))
		qt.Assert(t, qt.Equals(rng.Chan.Lit(), "results"))
		qt.Assert(t, qt.Equals(rng.For.Col(), uint(2)))
		qt.Assert(t, qt.Equals(rng.Define.Col(), uint(8)))
		qt.Assert(t, qt.Equals(rng.Range.Col(), uint(11)))
		qt.Assert(t, qt.IsNotNil(rng.Body))
		qt.Assert(t, qt.Equals(rng.End(), rng.Body.End()))
	})

	t.Run("range without a name", func(t *testing.T) {
		f := bashppRoundTrip(t, bashppWrapFunc("for range results {\n\t\techo hi\n\t}"))
		rng := f.Stmts[0].Cmd.(*BashPPFuncDecl).Body.Stmts[0].Cmd.(*BashPPRange)
		qt.Assert(t, qt.HasLen(rng.Names, 0))
		qt.Assert(t, qt.IsFalse(rng.Define.IsValid()))
	})
}

// TestBashPPSelectArms covers the case/default surface, whose communication
// clauses reach the shared classifier through a different reader than the
// statement forms do. The point of the assertions is that they land on the
// SAME node types: if the two readers ever disagree, this is where it shows.
func TestBashPPSelectArms(t *testing.T) {
	t.Parallel()

	src := bashppWrapFunc("select {\n\tcase v := <-results:\n\t\techo $v\n\tcase <-done:\n\t\tbreak\n\tcase results <- 1:\n\t\techo sent\n\tdefault:\n\t\techo none\n\t}")
	f := bashppRoundTrip(t, src)
	sel := f.Stmts[0].Cmd.(*BashPPFuncDecl).Body.Stmts[0].Cmd.(*BashPPSelect)
	qt.Assert(t, qt.HasLen(sel.Cases, 4))

	recvDecl, ok := sel.Cases[0].Comm.(*BashPPShortDecl)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.Equals(recvDecl.Lhs[0].Value, "v"))
	qt.Assert(t, qt.Equals(recvDecl.Recv.Chan.Lit(), "results"))
	qt.Assert(t, qt.IsTrue(sel.Cases[0].Colon.IsValid()))
	qt.Assert(t, qt.HasLen(sel.Cases[0].Stmts, 1))

	recv, ok := sel.Cases[1].Comm.(*BashPPReceive)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.Equals(recv.Chan.Lit(), "done"))

	send, ok := sel.Cases[2].Comm.(*BashPPSend)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.Equals(send.Chan.Lit(), "results"))
	qt.Assert(t, qt.Equals(send.Value.Lit(), "1"))

	qt.Assert(t, qt.IsTrue(sel.Cases[3].Default))
	qt.Assert(t, qt.IsNil(sel.Cases[3].Comm))
	qt.Assert(t, qt.HasLen(sel.Cases[3].Stmts, 1))

	// The empty form still works and is still the Go production.
	empty := bashppRoundTrip(t, bashppWrapFunc("select {}"))
	sel = empty.Stmts[0].Cmd.(*BashPPFuncDecl).Body.Stmts[0].Cmd.(*BashPPSelect)
	qt.Assert(t, qt.HasLen(sel.Cases, 0))
}

// TestBashPPChanFormsWalkComplete asserts every new node is reachable from
// Walk. A node the visitor cannot see is invisible to shfmt's simplifier, to
// typedjson's encoder and to every consumer that traverses the tree, and the
// failure mode is silence rather than an error.
func TestBashPPChanFormsWalkComplete(t *testing.T) {
	t.Parallel()

	src := bashppWrapFunc("ch := make(chan int, 2)\n\tgo worker(1)\n\tch <- 1\n\tv, ok := <-ch\n\t<-ch\n\tclose(ch)\n\tfor x := range ch {\n\t\techo $x\n\t}\n\tselect {\n\tcase y := <-ch:\n\t\techo $y\n\tcase ch <- 2:\n\t\techo sent\n\tdefault:\n\t\techo none\n\t}")
	f, err := bashppParse(LangBashPP, src)
	qt.Assert(t, qt.IsNil(err))

	seen := map[string]int{}
	Walk(f, func(n Node) bool {
		switch n.(type) {
		case *BashPPMakeChan:
			seen["make"]++
		case *BashPPChanType:
			seen["chantype"]++
		case *BashPPGo:
			seen["go"]++
		case *BashPPSend:
			seen["send"]++
		case *BashPPReceive:
			seen["recv"]++
		case *BashPPClose:
			seen["close"]++
		case *BashPPRange:
			seen["range"]++
		case *BashPPSelect:
			seen["select"]++
		case *BashPPSelectCase:
			seen["selectcase"]++
		}
		return true
	})
	for _, want := range []struct {
		kind string
		n    int
	}{
		{"make", 1}, {"chantype", 1}, {"go", 1}, {"close", 1},
		{"range", 1}, {"select", 1}, {"selectcase", 3},
		{"send", 2}, // statement and select arm
		{"recv", 3}, // short decl, standalone, select arm
	} {
		qt.Check(t, qt.Equals(seen[want.kind], want.n), qt.Commentf("kind %q", want.kind))
	}
}

// TestBashPPChanFormsInertInBashAndPOSIX is the compatibility gate, one row
// per form. Nothing here may change what LangBash or LangPOSIX does — the
// channel forms are recognized only inside a Bash++ func body, which is itself
// a shape stock bash rejects, so a dialect that has no such body must see the
// bytes exactly as it always did.
func TestBashPPChanFormsInertInBashAndPOSIX(t *testing.T) {
	t.Parallel()

	// Each of these is legal bash today and must keep its shell meaning.
	shellShapes := []struct {
		name string
		src  string
	}{
		{"send is a redirect", "results <- 1\n"},
		{"receive is a redirect", "<-results\n"},
		{"short decl receive is a redirect", "v := <-results\n"},
		{"tuple receive is a redirect", "v, ok := <-results\n"},
		{"spaced dash redirect", "cat < -file\n"},
		{"process substitution", "diff <(f a) <(f b)\n"},
		{"here string", "cat <<< word\n"},
		{"heredoc", "cat <<EOF\nbody\nEOF\n"},
		{"go toolchain", "go build ./...\n"},
		{"for loop", "for i in a b; do echo $i; done\n"},
		{"select builtin", "select x in a b; do echo $x; done\n"},
		{"make command", "make install\n"},
		{"close command", "close some.file\n"},
	}
	for _, shape := range shellShapes {
		t.Run(shape.name, func(t *testing.T) {
			// LangBashPP must agree with LangBash byte for byte: same AST,
			// same node offsets, same printed output.
			if diff := bashppParseDiff(t, shape.src); diff != "" {
				t.Errorf("input %q: %s", shape.src, diff)
			}
			// And LangPOSIX must be untouched: it parses what it always
			// parsed, and never grows a Bash++ node.
			f, err := bashppParse(LangPOSIX, shape.src)
			if err != nil {
				return // a shape POSIX rejects on its own terms; still no Bash++ node
			}
			Walk(f, func(n Node) bool {
				if n == nil {
					return true
				}
				if strings.Contains(fmt.Sprintf("%T", n), "syntax.BashPP") {
					t.Errorf("LangPOSIX built %T for %q", n, shape.src)
				}
				return true
			})
		})
	}
}

// TestBashPPChanRecognizersRollBack is the gate that matters most, and the one
// the top-level inertness table cannot reach.
//
// Inside a Bash++ func body p.bashppFuncDepth is non-zero, so the `for` and
// `select` recognizers RUN on every shell loop written there. Each reads ahead
// through a parser transaction and must put back every byte it consumed when
// the shape turns out not to be the Go one. A recognizer that consumed a
// failed probe without rewinding would corrupt the statement that follows,
// which is precisely the defect that made the previous attempt at these forms
// unusable — so each case asserts the shell node AND that the statement after
// it survived intact.
func TestBashPPChanRecognizersRollBack(t *testing.T) {
	t.Parallel()

	shellInsideGo := []struct {
		name string
		body string
		want string // %T of the first body statement's command
	}{
		{"for-in loop", "for i in a b; do\n\t\techo $i\n\tdone", "*syntax.ForClause"},
		{"for-arithmetic loop", "for ((i = 0; i < 3; i++)); do\n\t\techo $i\n\tdone", "*syntax.ForClause"},
		{"for over positional params", "for i; do\n\t\techo $i\n\tdone", "*syntax.ForClause"},
		{"select builtin", "select x in a b; do\n\t\techo $x\n\tdone", "*syntax.ForClause"},
		{"plain redirect", "cat <in.txt", "*syntax.CallExpr"},
		// The space after `<` is what keeps this a redirect rather than a
		// receive, so the printer must not flatten it away on reprint.
		{"dash-leading redirect keeps its space", "cat < -in.txt", "*syntax.CallExpr"},
		{"redirect with fd", "cat 0<in.txt", "*syntax.CallExpr"},
		{"here string", "cat <<<word", "*syntax.CallExpr"},
		{"process substitution", "diff <(f a) <(f b)", "*syntax.CallExpr"},
		{"make as a command", "make install", "*syntax.CallExpr"},
		{"close as a command", "close some.file", "*syntax.CallExpr"},
	}
	for _, shape := range shellInsideGo {
		t.Run(shape.name, func(t *testing.T) {
			// A sentinel statement follows, so a recognizer that swallowed
			// input without rewinding shows up as a missing or mangled tail
			// rather than as a subtly different first node.
			src := bashppWrapFunc(shape.body + "\n\techo tail")
			f, err := bashppParse(LangBashPP, src)
			qt.Assert(t, qt.IsNil(err))
			stmts := f.Stmts[0].Cmd.(*BashPPFuncDecl).Body.Stmts
			qt.Assert(t, qt.HasLen(stmts, 2))
			qt.Assert(t, qt.Equals(fmt.Sprintf("%T", stmts[0].Cmd), shape.want))
			tail, ok := stmts[1].Cmd.(*CallExpr)
			qt.Assert(t, qt.IsTrue(ok))
			qt.Assert(t, qt.HasLen(tail.Args, 2))
			qt.Assert(t, qt.Equals(tail.Args[1].Lit(), "tail"))
			// Byte-for-byte reprint: a rollback that restored the tree but not
			// the positions would still print differently.
			out, err := bashppPrint(f)
			qt.Assert(t, qt.IsNil(err))
			qt.Assert(t, qt.Equals(out, src))
		})
	}
}

// TestBashPPChanMalformedTerminates covers inputs that open a Go-looking shape
// and then fail to complete one. Each must reach a verdict — an error or a
// shell parse — rather than reading forever. The whole reason the recognizers
// work off a completed parse or a bounded transaction is that this parser is
// streaming: an unbounded scan past a chunk boundary is how a previous attempt
// hung TestParseFiles/bash/OK/085-0 while consuming the machine's memory.
func TestBashPPChanMalformedTerminates(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		"select {",
		"select {\n\tcase",
		"select {\n\tcase <-ch",
		"select {\n\tcase <-ch:",
		"select {\n\tcase :\n\t}",
		"select {\n\tnonsense\n\t}",
		"for x := range",
		"for x := range ch",
		"for x := range ch {",
		"for x, y := range ch {\n\t}",
		"ch := make(chan",
		"ch := make(chan int",
		"ch := make(chan int,",
		"ch := make(chan)",
		"ch := make(chan int, 1, 2)",
		"ch <-",
		"<-",
		"v, ok := <-",
		"close()",
		"close(a, b)",
	} {
		t.Run(body, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				defer close(done)
				// Only termination is asserted: whether a given malformed
				// shape lands on a Go diagnostic or falls back to a shell one
				// is the recognizer's business, but it must land.
				_, _ = bashppParse(LangBashPP, bashppWrapFunc(body))
			}()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatalf("parsing %q did not terminate", body)
			}
		})
	}
}
