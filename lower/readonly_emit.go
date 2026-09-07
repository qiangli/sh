package lower

import (
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// ReadonlyContext carries expressions, never a package-global state variable,
// a thread local or a goroutine-id lookup. State evaluates to the runtime's
// *ReadonlyState for the enclosing program or task; Abort names a func(error)
// that does not return, owned by the program/task failure boundary, which
// applies the language's diagnostic and exit-status contract.
type ReadonlyContext struct{ State, Abort string }

// ReadonlyMutation describes one guarded container update. Kind is the actual
// mutated container, not the root binding's own shape: it selects both the
// runtime's diagnostic wording and how the container expression is formed.
// Value is the already-emitted right-hand side; it is emitted exactly once.
// ValueType, when the compiler knows the target's element type, captures that
// value before the check. Without it the value cannot be captured at all: a
// bare temp would give an untyped constant its default type and change
// assignability, so `m["k"] = 1` into a map[string]float64 would stop compiling.
type ReadonlyMutation struct{ Kind, Value, ValueType string }

// ReadonlyBuiltin describes one guarded collection builtin call. Target is the
// emitted first argument and is captured exactly once; Args are the remaining
// arguments, emitted once each in source order. Root is the source spelling of
// the root binding and Address its actual binding address, defaulting to the
// root's own address. Path is positioned source text, never evaluated. Spread
// marks the `args...` form, whose element count is only known at runtime.
type ReadonlyBuiltin struct {
	Name, Root, Address, Target, Path string
	Args                              []string
	Spread                            bool
}

func (e *emitter) readonlyContext(n syntax.Node, c ReadonlyContext) error {
	// No default is possible here: a fallback would reintroduce exactly the
	// global state this contract forbids.
	if c.State == "" || c.Abort == "" {
		return e.fail(n, CodeBridge, "readonly lowering requires explicit runtime state and failure boundary expressions")
	}
	return nil
}

// readonlyGuard reports one runtime check. The error binding is scoped to the
// statement, so sibling guards in one block never collide. Abort must not
// return; a returning boundary would fall through into the update.
func (e *emitter) readonlyGuard(c ReadonlyContext, check string) string {
	name := e.prefix + "readonlyErr"
	return "if " + name + " := " + check + "; " + name + " != nil {\n" + c.Abort + "(" + name + ")\n}\n"
}

// readonlyDeclare marks each named binding by address. Marking records runtime
// locations, so the compiler must pass the binding itself and not a copy.
func (e *emitter) readonlyDeclare(n *syntax.DeclClause, c ReadonlyContext) (string, error) {
	if err := e.readonlyContext(n, c); err != nil {
		return "", err
	}
	if n.Variant == nil || n.Variant.Value != "readonly" {
		return "", e.fail(n, CodeUnsupported, "declaration variant is not readonly")
	}
	if len(n.Args) == 0 {
		return "", e.fail(n, CodeUnsupported, "readonly without names reports state rather than marking it")
	}
	var out strings.Builder
	for _, arg := range n.Args {
		if arg == nil || arg.Name == nil {
			return "", e.fail(n, CodeExpr, "readonly name is missing")
		}
		if arg.Value != nil || arg.Array != nil || arg.Index != nil || arg.Append {
			return "", e.fail(arg, CodeUnsupported, "readonly with an initializer needs the compiler's declaration path")
		}
		name := arg.Name.Value
		if !e.known(name) {
			return "", e.fail(arg, CodeUndefined, "undefined: "+name)
		}
		out.WriteString(e.readonlyGuard(c, c.State+".Mark("+strconv.Quote(name)+", &"+e.goName(name)+")"))
	}
	return strings.TrimSuffix(out.String(), "\n"), nil
}

// readonlyRebind guards the binding location itself. Rebinding a distinct alias
// is not mutation of the shared object, so the check takes the binding address
// and nothing else. The value is deliberately not captured into a temp: the
// check never reads it, it is already emitted exactly once by the assignment,
// and a temp would change untyped-constant assignability.
func (e *emitter) readonlyRebind(n syntax.Node, c ReadonlyContext, name, value string) (string, error) {
	if err := e.readonlyContext(n, c); err != nil {
		return "", err
	}
	if name == "" || value == "" {
		return "", e.fail(n, CodeExpr, "readonly rebinding needs a binding name and an emitted value")
	}
	if !e.known(name) {
		return "", e.fail(n, CodeUndefined, "undefined: "+name)
	}
	check := c.State + ".CheckAssign(&" + e.goName(name) + ")"
	return e.readonlyGuard(c, check) + e.goName(name) + " = " + value, nil
}

type readonlyStep struct {
	field string
	index syntax.BashPPExpr
}

// readonlyTarget is a mutation path decomposed once. Every index operand is
// captured into a temp in source order, so the guard and the update share one
// evaluation. Source is positioned path text with the root removed, matching
// the interpreter's diagnostics; it is never evaluated.
type readonlyTarget struct {
	root, setup, path, parent, last, source string
	field                                   bool
}

func (e *emitter) readonlyPath(x syntax.BashPPExpr) (*readonlyTarget, error) {
	var steps []readonlyStep
	current := x
	for {
		switch n := current.(type) {
		case *syntax.BashPPSelectorExpr:
			if n.Sel == nil {
				return nil, e.fail(n, CodeExpr, "mutation path field is missing")
			}
			steps = append(steps, readonlyStep{field: n.Sel.Value})
			current = n.X
		case *syntax.BashPPIndexExpr:
			if n.Index == nil {
				return nil, e.fail(n, CodeExpr, "mutation path index is missing")
			}
			steps = append(steps, readonlyStep{index: n.Index})
			current = n.X
		case *syntax.BashPPParenExpr:
			current = n.X
		case *syntax.BashPPIdent:
			if n.Name == nil {
				return nil, e.fail(n, CodeExpr, "mutation path root is missing")
			}
			if !e.known(n.Name.Value) {
				return nil, e.fail(n, CodeUndefined, "undefined: "+n.Name.Value)
			}
			target := &readonlyTarget{root: n.Name.Value, path: e.goName(n.Name.Value)}
			for i := len(steps) - 1; i >= 0; i-- {
				if err := e.readonlyStep(target, steps[i], len(steps)-i-1); err != nil {
					return nil, err
				}
			}
			return target, nil
		default:
			return nil, e.fail(x, CodeUnsupported, "mutation path is not a rooted field/index chain: "+nodeName(x))
		}
	}
}

func (e *emitter) readonlyStep(target *readonlyTarget, step readonlyStep, ordinal int) error {
	target.parent = target.path
	if step.index == nil {
		target.field, target.last = true, step.field
		target.path += "." + step.field
		target.source += "." + step.field
		return nil
	}
	value, err := e.expr(step.index)
	if err != nil {
		return err
	}
	// One capture per index keeps the guard and the update on a single
	// evaluation, in source order, even when an index has side effects.
	temp := e.prefix + "readonlyIndex" + strconv.Itoa(ordinal)
	target.setup += temp + " := " + value + "\n"
	target.field, target.last = false, temp
	target.path += "[" + temp + "]"
	target.source += "[" + readonlyExprText(step.index) + "]"
	return nil
}

// readonlyExprText reproduces the interpreter's positioned path spelling for
// the vocabulary a mutation path can contain. It is diagnostic text only.
func readonlyExprText(x syntax.BashPPExpr) string {
	switch n := x.(type) {
	case *syntax.BashPPBasicLit:
		if n.Value == nil {
			return "?"
		}
		return n.Value.Value
	case *syntax.BashPPIdent:
		if n.Name == nil {
			return "?"
		}
		return n.Name.Value
	case *syntax.BashPPSelectorExpr:
		if n.Sel == nil {
			return "?"
		}
		return readonlyExprText(n.X) + "." + n.Sel.Value
	case *syntax.BashPPIndexExpr:
		return readonlyExprText(n.X) + "[" + readonlyExprText(n.Index) + "]"
	case *syntax.BashPPParenExpr:
		return "(" + readonlyExprText(n.X) + ")"
	case *syntax.BashPPUnaryExpr:
		if n.Op == nil {
			return "?"
		}
		return n.Op.Value + readonlyExprText(n.X)
	case *syntax.BashPPBinaryExpr:
		if n.Op == nil {
			return "?"
		}
		return readonlyExprText(n.X) + " " + n.Op.Value + " " + readonlyExprText(n.Y)
	}
	return "?"
}

// readonlyMutation guards the actual container being changed. Field mutation
// checks the addressed location, so a public field reached through a pointer
// alias resolves to the marked object rather than to the local binding; map and
// slice mutation capture the parent container once and write through it, which
// preserves the deep alias identity the runtime records. A value container such
// as an array must be routed as "field", since writing through a captured copy
// would not reach the original.
func (e *emitter) readonlyMutation(target syntax.BashPPExpr, c ReadonlyContext, m ReadonlyMutation) (string, error) {
	if err := e.readonlyContext(target, c); err != nil {
		return "", err
	}
	switch m.Kind {
	case "field", "map", "slice":
	default:
		return "", e.fail(target, CodeType, "readonly mutation kind must be field, map or slice")
	}
	if m.Value == "" {
		return "", e.fail(target, CodeExpr, "readonly mutation needs an emitted value expression")
	}
	path, err := e.readonlyPath(target)
	if err != nil {
		return "", err
	}
	if path.source == "" {
		return "", e.fail(target, CodeExpr, "readonly mutation target is a whole binding; use the rebinding guard")
	}
	if path.field != (m.Kind == "field") {
		return "", e.fail(target, CodeType, "readonly mutation kind does not match the final path element")
	}
	setup := path.setup
	container, update := "&"+path.path, path.path
	if m.Kind != "field" {
		temp := e.prefix + "readonlyContainer"
		setup += temp + " := " + path.parent + "\n"
		container, update = temp, temp+"["+path.last+"]"
	}
	value := m.Value
	if m.ValueType != "" {
		temp := e.prefix + "readonlyValue"
		setup += "var " + temp + " " + m.ValueType + " = " + m.Value + "\n"
		value = temp
	}
	check := c.State + ".CheckMutation(" + strconv.Quote(path.root) + ", &" + e.goName(path.root) + ", " + container +
		", " + strconv.Quote(path.source) + ", " + strconv.Quote(m.Kind) + ")"
	return "{\n" + setup + e.readonlyGuard(c, check) + update + " = " + value + "\n}", nil
}

// readonlyBuiltin guards append, copy, delete and clear. The target is captured
// once so the checked container is the one the builtin then mutates; the call
// is returned as an expression so append keeps its value form. append is guarded
// only when the new elements fit in the existing capacity, because only then
// does it write into the readonly backing array; a growing append allocates and
// mutates nothing. The runtime's wording differs from the interpreter's
// "through <builtin>" sentence; see docs/lowering-readonly-emission.md for the
// requested CheckBuiltin entry point.
func (e *emitter) readonlyBuiltin(n syntax.Node, c ReadonlyContext, b ReadonlyBuiltin) (string, string, error) {
	if err := e.readonlyContext(n, c); err != nil {
		return "", "", err
	}
	extra := len(b.Args)
	switch b.Name {
	case "clear":
		if extra != 0 {
			return "", "", e.fail(n, CodeResult, "clear takes one collection")
		}
	case "delete":
		if extra != 1 {
			return "", "", e.fail(n, CodeResult, "delete takes a map and one key")
		}
	case "copy":
		if extra != 1 {
			return "", "", e.fail(n, CodeResult, "copy takes a destination and a source")
		}
	case "append":
	default:
		return "", "", e.fail(n, CodeUnsupported, "collection builtin is not guarded: "+b.Name)
	}
	if b.Spread && (b.Name != "append" || extra != 1) {
		return "", "", e.fail(n, CodeResult, "the spread form takes one spread argument after the slice")
	}
	if b.Target == "" || b.Root == "" {
		return "", "", e.fail(n, CodeExpr, "readonly builtin needs an emitted target and its root binding")
	}
	if !e.known(b.Root) {
		return "", "", e.fail(n, CodeUndefined, "undefined: "+b.Root)
	}
	address, path := b.Address, b.Path
	if address == "" {
		address = "&" + e.goName(b.Root)
	}
	if path == "" {
		path = b.Root
	}
	temp := e.prefix + "readonlyTarget"
	setup := temp + " := " + b.Target + "\n"
	arguments := append([]string{temp}, b.Args...)
	count := strconv.Itoa(extra)
	if b.Spread {
		// The spread operand is captured too: its length decides the guard and
		// the same value must reach the call.
		spread := e.prefix + "readonlySpread"
		setup += spread + " := " + b.Args[0] + "\n"
		arguments[1] = spread + "..."
		count = "len(" + spread + ")"
	}
	check := c.State + ".CheckMutation(" + strconv.Quote(b.Root) + ", " + address + ", " + temp +
		", " + strconv.Quote(path) + ", " + strconv.Quote(b.Name) + ")"
	guard := e.readonlyGuard(c, check)
	switch {
	case b.Name != "append":
	case b.Spread:
		guard = "if " + count + " > 0 && len(" + temp + ")+" + count + " <= cap(" + temp + ") {\n" + guard + "}\n"
	case extra == 0:
		// append(s) adds nothing and writes nothing.
		guard = ""
	default:
		guard = "if len(" + temp + ")+" + count + " <= cap(" + temp + ") {\n" + guard + "}\n"
	}
	return setup + guard, b.Name + "(" + strings.Join(arguments, ", ") + ")", nil
}
