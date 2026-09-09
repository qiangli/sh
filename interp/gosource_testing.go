package interp

// Sprint: #118; Story: #56; Story-ID: 3ef468f4e831
import (
	"context"
	"errors"
	"fmt"
	"go/constant"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/syntax"
)

// GoSourceTestingT is the scheduler capability passed to an interpreted test.
// A real *testing.T implements it. The interpreter deliberately does not import
// testing: the dedicated host harness owns testing.Main and the test goroutine.
type GoSourceTestingT interface {
	Errorf(string, ...any)
	Logf(string, ...any)
	Fail()
	FailNow()
	SkipNow()
}

// GoSourceTestingSession owns one loaded, unchanged Go test package. Its
// functions execute through the ordinary interpreter; native dependency calls
// retain their separate bridge. One session and its Runner are serial resources.
// Close it before using the Runner for another program. Reset invalidates it.
type GoSourceTestingSession struct {
	runner  *Runner
	context context.Context
	program *gosource.Program
	loading bool
	active  *goSourceTestingHandle
	closed  bool
}

type goSourceTestingHandle struct {
	session *GoSourceTestingSession
	target  GoSourceTestingT
	active  bool
}

type goSourceTestingExit struct {
	handle *goSourceTestingHandle
	skip   bool
}

// LoadGoSourceTests initializes a non-main package without rewriting its source
// or adding a main function. The caller must supply every applicable original
// package companion to gosource.Load with RunMain false. Only its original init
// functions are invoked here; test registration is an explicit later operation.
func (r *Runner) LoadGoSourceTests(ctx context.Context, program *gosource.Program) (*GoSourceTestingSession, error) {
	if program == nil || program.File == nil || !program.File.GoSource || program.Package == "main" {
		return nil, fmt.Errorf("gosource: testing requires a loaded non-main Go package")
	}
	if r.Dialect() != syntax.LangBashPP {
		return nil, fmt.Errorf("gosource: testing requires a Bash++ runner")
	}
	if r.goSourceTesting != nil {
		return nil, fmt.Errorf("gosource: runner already owns a testing session")
	}
	r.Reset()
	session := &GoSourceTestingSession{runner: r, context: ctx, program: program, loading: true}
	r.goSourceTesting = session
	file := *program.File
	file.Stmts = append([]*syntax.Stmt(nil), program.File.Stmts...)
	for _, name := range program.InitFunctions {
		pos := file.Pos()
		file.Stmts = append(file.Stmts, &syntax.Stmt{Cmd: &syntax.BashPPCall{Fun: []*syntax.Lit{{Value: name, ValuePos: pos, ValueEnd: pos}}, Lparen: pos, Rparen: pos}})
	}
	if err := r.Run(ctx, &file); err != nil {
		session.Close()
		return nil, err
	}
	session.loading = false
	return session, nil
}

// Close releases this package's dependency process. It is idempotent and does
// not close another session that may have replaced this one after Reset.
func (s *GoSourceTestingSession) Close() {
	if s == nil || s.closed {
		return
	}
	s.closed = true
	if s.runner.goSourceTesting == s {
		s.runner.closeGoSourceBridge()
		s.runner.goSourceTesting = nil
	}
}

// Run invokes one original func(*testing.T) body in the caller's testing
// goroutine. Generated testing.InternalTest callbacks can call this method
// directly; no original test body is compiled into their native harness.
// FailNow and SkipNow unwind interpreted defers before reaching the scheduler.
func (s *GoSourceTestingSession) Run(ctx context.Context, name string, target GoSourceTestingT) error {
	if s == nil || s.closed || s.runner.goSourceTesting != s {
		return fmt.Errorf("gosource: testing session is closed or reset")
	}
	return s.runFunction(ctx, name, s.runner.bashPPFuncs[name], target, false, nil)
}
func (s *GoSourceTestingSession) runFunction(ctx context.Context, name string, fn *bashPPFunc, target GoSourceTestingT, nested bool, cleanup *goSourceTestingHandle) (err error) {
	if s == nil || s.closed || s.runner.goSourceTesting != s {
		return fmt.Errorf("gosource: testing session is closed or reset")
	}
	if target == nil {
		return fmt.Errorf("gosource: nil testing scheduler capability")
	}
	if s.active != nil && !nested {
		return fmt.Errorf("gosource: overlapping test callbacks are not supported")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r := s.runner
	if !nested {
		var finishSignalRun func()
		ctx, finishSignalRun = r.beginAsyncSignalRun(ctx)
		defer finishSignalRun()
	}
	previousTaskPolicy := r.bashPPHostedTask
	r.bashPPHostedTask = taskPolicy(ctx)
	defer func() { r.bashPPHostedTask = previousTaskPolicy }()
	defer r.withDeclarations(ctx)()
	if fn == nil {
		return fmt.Errorf("gosource: test function %q is not loaded", name)
	}
	params := bashppParams(fn.params())
	expectedParams := 1
	if cleanup != nil {
		expectedParams = 0
	}
	if len(params) != expectedParams || len(fn.results()) != 0 {
		return fmt.Errorf("gosource: invalid testing callback signature for %s", name)
	}
	if cleanup == nil {
		declared := params[0].declared
		alias, typ, ok := strings.Cut(strings.TrimPrefix(declared, "*"), ".")
		if !strings.HasPrefix(declared, "*") || !ok || typ != "T" || r.bashPPImports[alias] != "testing" {
			return fmt.Errorf("gosource: %s must receive the imported *testing.T, got %s", name, declared)
		}
	}
	previousHandle := s.active
	handle := cleanup
	if handle == nil {
		handle = &goSourceTestingHandle{session: s, target: target}
	}
	previousActive := handle.active
	handle.active = true
	s.active = handle
	previousExit, previousExpandExit := r.exit, r.expandRunExit
	previousGo, previousFile, previousContext := r.bashPPGoSource, r.bashPPGoSourceFile, r.ectx
	r.bashPPGoSource, r.bashPPGoSourceFile = true, s.program.File
	r.fillExpandConfig(ctx)
	r.exit = exitStatus{}
	r.expandRunExit = exitStatus{}
	defer func() {
		handle.active = previousActive
		s.active = previousHandle
		r.exit, r.expandRunExit = previousExit, previousExpandExit
		r.bashPPGoSource, r.bashPPGoSourceFile = previousGo, previousFile
		r.fillExpandConfig(previousContext)
		if v := recover(); v != nil {
			control, ok := v.(goSourceTestingExit)
			if !ok || control.handle != handle {
				panic(v)
			}
			if control.skip {
				target.SkipNow()
			} else {
				target.FailNow()
			}
		}
	}()
	var args []string
	if cleanup == nil {
		cell := &bashPPCell{vr: expand.NewObject(handle), declType: params[0].typ}
		var ok bool
		args, ok = r.bashPPBindCall(fn, []string{"testing.T@host"}, nil, []*bashPPCell{cell}, nil, 1)
		if !ok {
			return fmt.Errorf("gosource: cannot bind test callback %s", name)
		}
	}
	r.bashPPInvoke(ctx, fn, args)
	if r.exit.err != nil {
		return r.exit.err
	}
	if r.exit.code != 0 {
		return ExitStatus(r.exit.code)
	}
	return ctx.Err()
}

// bashPPTestingCall handles only the host testing capability. All ordinary
// Bash calls and imported dependency calls continue through their usual paths.
func (r *Runner) bashPPTestingCall(call *syntax.BashPPCall) bool {
	invoke, handled := r.bashPPTestingCapture(call)
	if invoke != nil {
		invoke()
	}
	return handled
}

func (r *Runner) bashPPTestingCapture(call *syntax.BashPPCall) (func(), bool) {
	if !r.bashPPGoSource || r.goSourceTesting == nil || len(call.Fun) != 2 || r.bashPPScope == nil {
		return nil, false
	}
	cell := r.bashPPScope.lookup(call.Fun[0].Value)
	if cell == nil {
		return nil, false
	}
	handle, ok := cell.vr.Obj.(*goSourceTestingHandle)
	if !ok {
		return nil, false
	}
	fail := func(err error) (func(), bool) { r.exit.fatal(err); return nil, true }
	if !handle.active || handle.session != r.goSourceTesting || handle.session.active != handle {
		return fail(errors.New("gosource: stale testing.T capability"))
	}
	var args []any
	for _, expr := range call.ArgExprs {
		if literal, ok := expr.(*syntax.BashPPFuncLit); ok {
			fn, _ := r.bashPPMakeClosure(literal)
			args = append(args, fn)
			continue
		}
		value, err := r.bashPPEvalScalarExpr(expr)
		if err != nil {
			return fail(err)
		}
		switch value.value.Kind() {
		case constant.String:
			args = append(args, constant.StringVal(value.value))
		case constant.Bool:
			args = append(args, constant.BoolVal(value.value))
		case constant.Int:
			n, exact := constant.Int64Val(value.value)
			if !exact {
				return fail(errors.New("gosource: testing argument is outside int64"))
			}
			args = append(args, int(n))
		case constant.Float:
			n, _ := constant.Float64Val(value.value)
			args = append(args, n)
		default:
			return fail(errors.New("gosource: testing method requires scalar arguments in this driver slice"))
		}
	}
	method := call.Fun[1].Value
	return func() { r.bashPPTestingInvoke(handle, method, args) }, true
}

func (r *Runner) bashPPTestingInvoke(handle *goSourceTestingHandle, method string, args []any) {
	fail := func(err error) { r.exit.fatal(err) }
	if !handle.active || handle.session != r.goSourceTesting || handle.session.active != handle {
		fail(errors.New("gosource: stale testing.T capability"))
		return
	}
	message := func() (string, error) {
		if len(args) == 0 {
			return "", errors.New("gosource: formatted testing call requires a format")
		}
		format, ok := args[0].(string)
		if !ok {
			return "", errors.New("gosource: testing format is not a string")
		}
		return fmt.Sprintf(format, args[1:]...), nil
	}
	switch method {
	case "Run", "Cleanup":
		if err := r.bashPPTestingCallback(handle, method, args); err != nil {
			fail(err)
		}
		return
	case "Helper":
		if len(args) != 0 {
			fail(errors.New("gosource: testing.Helper takes no arguments"))
		}
		// A scheduler helper mark cannot describe an interpreted stack. Retain
		// source positions in interpreter errors; no native helper frame is claimed.
		return
	case "Errorf", "Logf", "Fatalf", "Skipf":
		text, err := message()
		if err != nil {
			fail(err)
			return
		}
		if method == "Errorf" || method == "Fatalf" {
			handle.target.Errorf("%s", text)
		} else {
			handle.target.Logf("%s", text)
		}
		if method == "Fatalf" || method == "Skipf" {
			panic(goSourceTestingExit{handle: handle, skip: method == "Skipf"})
		}
	case "Error", "Log", "Fatal", "Skip":
		text := fmt.Sprintln(args...)
		if method == "Error" || method == "Fatal" {
			handle.target.Errorf("%s", text)
		} else {
			handle.target.Logf("%s", text)
		}
		if method == "Fatal" || method == "Skip" {
			panic(goSourceTestingExit{handle: handle, skip: method == "Skip"})
		}
	case "Fail", "FailNow", "SkipNow":
		if len(args) != 0 {
			fail(errors.New("gosource: testing control method takes no arguments"))
			return
		}
		if method != "SkipNow" {
			handle.target.Fail()
		}
		if method != "Fail" {
			panic(goSourceTestingExit{handle: handle, skip: method == "SkipNow"})
		}
	default:
		fail(fmt.Errorf("gosource: testing.T.%s is not implemented by this driver slice", method))
		return
	}
}

// A private control panic crosses every interpreted frame without converting
// FailNow into shell exit (which would discard Go defers). It is consumed only
// by the owning scheduler callback after each frame's interpreted defers run.
func (r *Runner) bashPPTestingUnwind(ctx context.Context, mark int) {
	if v := recover(); v != nil {
		if _, ok := v.(goSourceTestingExit); ok {
			r.bashPPRunDefers(ctx, mark)
		}
		panic(v)
	}
}

// Catch only scheduler control while running one cleanup, allowing the rest of
// the frame's defers to run even if this cleanup itself calls FailNow/SkipNow.
func bashPPTestingCatch(call func()) (control *goSourceTestingExit) {
	defer func() {
		if v := recover(); v != nil {
			if exit, ok := v.(goSourceTestingExit); ok {
				control = &exit
			} else {
				panic(v)
			}
		}
	}()
	call()
	return nil
}
