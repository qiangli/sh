// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"fmt"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// TODO(v4): the arithmetic APIs should return int64 for portability with 32-bit systems,
// even if Bash only supports native int sizes.

type ArithmError struct {
	Expr syntax.ArithmExpr
	Err  error
}

func (e *ArithmError) Error() string { return e.Err.Error() }
func (e *ArithmError) Unwrap() error { return e.Err }

type arithLvalue struct {
	name       string
	word       *syntax.Word
	index      syntax.ArithmExpr
	indexValue int
	indexSet   bool
}

// isAllDigits reports whether s is non-empty and consists entirely
// of ASCII digits (no sign, no separators).
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// containsArithOp reports whether s contains a character that would
// make it an arithmetic expression rather than a bare identifier or
// number literal. Used to decide whether the string form of an arith
// operand needs to be re-parsed (`let "jv *= 2"`).
func containsArithOp(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '+', '-', '*', '/', '%', '<', '>', '=', '!',
			'&', '|', '^', '~', '?', ':', '(', ')', ' ', '\t':
			return true
		}
	}
	return false
}

func Arithm(cfg *Config, expr syntax.ArithmExpr) (int, error) {
	switch expr := expr.(type) {
	case nil:
		return 0, nil
	case *syntax.Word:
		str, err := Literal(cfg, expr)
		if err != nil {
			return 0, err
		}
		// recursively fetch vars
		i := 0
		for syntax.ValidName(str) {
			val := cfg.envGet(str)
			if val == "" {
				break
			}
			if i++; i >= maxNameRefDepth {
				break
			}
			str = val
		}
		if token := literalDollarArithmToken(str); token != "" {
			return 0, fmt.Errorf("arithmetic syntax error: operand expected (error token is %q)", token)
		}
		// Bash re-parses the literal text of a Word-shaped arith
		// operand as an arithmetic expression when it contains
		// operators — `let "jv *= 2"` quotes the whole expression
		// in a Word, but we still need to evaluate it.
		if containsArithOp(str) {
			file, perr := syntax.NewParser().Parse(strings.NewReader("(("+str+"))"), "")
			if perr != nil {
				return 0, arithmParseError(str, perr)
			}
			if perr == nil && len(file.Stmts) == 1 {
				if ac, ok := file.Stmts[0].Cmd.(*syntax.ArithmCmd); ok && ac.X != nil {
					// Avoid infinite recursion when the re-parse
					// produces the same Word back.
					if _, isWord := ac.X.(*syntax.Word); !isWord {
						return Arithm(cfg, ac.X)
					}
				}
			}
		}
		n, err := atoiCheck(str)
		if err != nil {
			return 0, err
		}
		return int(n), nil
	case *syntax.ParenArithm:
		return Arithm(cfg, expr.X)
	case *syntax.UnaryArithm:
		switch expr.Op {
		case syntax.Inc, syntax.Dec:
			// Bash 5.3 distinguishes:
			//   - `7++` / `7--` (literal number) — treats as a
			//     parse-time syntax error: "arithmetic syntax
			//     error: operand expected (error token is "+ ")".
			//     The parser splits `++` into `+ +`; the trailing
			//     `+` is a unary operator missing its operand.
			//   - `--x++` (compound expression) — "assignment
			//     requires lvalue".
			//   - other non-name lvalues — "attempted assignment to
			//     non-variable".
			op := "++"
			tail := "+ "
			if expr.Op == syntax.Dec {
				op = "--"
				tail = "- "
			}
			lval, ok := arithLvalueFrom(expr.X)
			if !ok {
				return 0, fmt.Errorf("%s: assignment requires lvalue (error token is %q)", op, op)
			}
			if lval.name != "" && isAllDigits(lval.name) {
				return 0, fmt.Errorf("arithmetic syntax error: operand expected (error token is %q)", tail)
			}
			if !syntax.ValidName(lval.name) {
				return 0, fmt.Errorf("attempted assignment to non-variable (error token is %q)", op)
			}
			lval, err := cfg.resolveAritLvalue(lval)
			if err != nil {
				return 0, err
			}
			old, err := cfg.getAritLvalue(lval)
			if err != nil {
				return 0, err
			}
			val := old
			if expr.Op == syntax.Inc {
				val++
			} else {
				val--
			}
			if err := cfg.setAritLvalue(lval, val); err != nil {
				return 0, err
			}
			if expr.Post {
				return int(old), nil
			}
			return int(val), nil
		}
		val, err := Arithm(cfg, expr.X)
		if err != nil {
			return 0, err
		}
		switch expr.Op {
		case syntax.Not:
			return oneIf(val == 0), nil
		case syntax.BitNegation:
			return ^val, nil
		case syntax.Plus:
			return val, nil
		case syntax.Minus:
			return -val, nil
		default:
			return 0, fmt.Errorf("unsupported unary arithmetic operator: %q", expr.Op)
		}
	case *syntax.BinaryArithm:
		switch expr.Op {
		case syntax.Assgn, syntax.AddAssgn, syntax.SubAssgn,
			syntax.MulAssgn, syntax.QuoAssgn, syntax.RemAssgn,
			syntax.AndAssgn, syntax.OrAssgn, syntax.XorAssgn,
			syntax.ShlAssgn, syntax.ShrAssgn:
			return cfg.assgnArit(expr)
		case syntax.TernQuest: // TernColon can't happen here
			cond, err := Arithm(cfg, expr.X)
			if err != nil {
				return 0, err
			}
			b2 := expr.Y.(*syntax.BinaryArithm) // must have Op==TernColon
			if cond != 0 {
				return Arithm(cfg, b2.X)
			}
			return Arithm(cfg, b2.Y)
		case syntax.AndArit:
			left, err := Arithm(cfg, expr.X)
			if err != nil {
				return 0, err
			}
			if left == 0 {
				return 0, nil
			}
			right, err := Arithm(cfg, expr.Y)
			if err != nil {
				return 0, err
			}
			return oneIf(right != 0), nil
		case syntax.OrArit:
			left, err := Arithm(cfg, expr.X)
			if err != nil {
				return 0, err
			}
			if left != 0 {
				return 1, nil
			}
			right, err := Arithm(cfg, expr.Y)
			if err != nil {
				return 0, err
			}
			return oneIf(right != 0), nil
		case syntax.XorBool:
			left, err := Arithm(cfg, expr.X)
			if err != nil {
				return 0, err
			}
			right, err := Arithm(cfg, expr.Y)
			if err != nil {
				return 0, err
			}
			return oneIf((left != 0) != (right != 0)), nil
		}
		left, err := Arithm(cfg, expr.X)
		if err != nil {
			return 0, err
		}
		right, err := Arithm(cfg, expr.Y)
		if err != nil {
			return 0, err
		}
		return binArit(expr.Op, left, right)
	default:
		panic(fmt.Sprintf("unexpected arithm expr: %T", expr))
	}
}

func arithLvalueFrom(expr syntax.ArithmExpr) (arithLvalue, bool) {
	w, ok := expr.(*syntax.Word)
	if !ok {
		return arithLvalue{}, false
	}
	if len(w.Parts) == 1 {
		if pe, ok := w.Parts[0].(*syntax.ParamExp); ok && pe.Param != nil && pe.NestedParam == nil {
			return arithLvalue{name: pe.Param.Value, index: pe.Index}, true
		}
	}
	return arithLvalue{name: w.Lit(), word: w}, true
}

func (cfg *Config) resolveAritLvalueName(lval arithLvalue) (arithLvalue, error) {
	if lval.word == nil {
		return lval, nil
	}
	name, err := Literal(cfg, lval.word)
	if err != nil {
		return lval, err
	}
	lval.name = name
	return lval, nil
}

func (cfg *Config) getAritLvalue(lval arithLvalue) (int64, error) {
	if lval.index == nil {
		return atoi(cfg.envGet(lval.name)), nil
	}
	if !lval.indexSet {
		var err error
		lval, err = cfg.resolveAritLvalue(lval)
		if err != nil {
			return 0, err
		}
	}
	vr := cfg.Env.Get(lval.name)
	switch vr.Kind {
	case Indexed:
		if lval.indexValue < len(vr.List) {
			return atoi(vr.List[lval.indexValue]), nil
		}
	case String, NameRef:
		if lval.indexValue == 0 {
			return atoi(vr.Str), nil
		}
	}
	return 0, nil
}

func (cfg *Config) resolveAritLvalue(lval arithLvalue) (arithLvalue, error) {
	if lval.index == nil || lval.indexSet {
		return lval, nil
	}
	index, err := Arithm(cfg, lval.index)
	if err != nil {
		return lval, err
	}
	if index < 0 {
		return lval, fmt.Errorf("bad array subscript")
	}
	lval.indexValue = index
	lval.indexSet = true
	return lval, nil
}

func (cfg *Config) setAritLvalue(lval arithLvalue, val int64) error {
	if lval.index == nil {
		return cfg.envSet(lval.name, strconv.FormatInt(val, 10))
	}
	wenv, ok := cfg.Env.(WriteEnviron)
	if !ok {
		return fmt.Errorf("environment is read-only")
	}
	if !lval.indexSet {
		var err error
		lval, err = cfg.resolveAritLvalue(lval)
		if err != nil {
			return err
		}
	}
	vr := cfg.Env.Get(lval.name)
	if vr.Kind == String && vr.Str != "" {
		vr.List = []string{vr.Str}
	}
	vr.Kind = Indexed
	vr.Set = true
	if lval.indexValue >= len(vr.List) {
		vr.List = append(vr.List, make([]string, lval.indexValue-len(vr.List)+1)...)
	}
	vr.List[lval.indexValue] = strconv.FormatInt(val, 10)
	return wenv.Set(lval.name, vr)
}

func oneIf(b bool) int {
	if b {
		return 1
	}
	return 0
}

// atoi is like [strconv.ParseInt](s, BASE, 64), but it handles integer
// base prefixes according to bash-shell's rules, ignores errors, and
// trims whitespace.
//
// For more information about bash's integer base handling syntax,
// refer to the bash manual:
// https://www.man7.org/linux/man-pages/man1/bash.1.html
func atoi(s string) int64 {
	n, _ := atoiCheck(s)
	return n
}

func atoiCheck(s string) (int64, error) {
	orig := s
	s = strings.TrimSpace(s)
	base := int64(10)
	switch {
	case strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X"):
		base = 16
		s = s[2:]
	case strings.HasPrefix(s, "0"):
		base = 8
		s = s[1:]
		if strings.Contains(s, "#") {
			return 0, fmt.Errorf("invalid number (error token is %q)", orig)
		}
	default:
		baseStr, intStr, hasSep := strings.Cut(s, "#")
		if hasSep {
			var err error
			base, err = strconv.ParseInt(baseStr, 10, 8)
			if err != nil || base > 64 {
				return 0, fmt.Errorf("invalid arithmetic base (error token is %q)", orig)
			}
			if base < 2 {
				return 0, fmt.Errorf("invalid number (error token is %q)", orig)
			}
			if intStr == "" {
				return 0, fmt.Errorf("invalid integer constant (error token is %q)", orig)
			}
			if strings.Contains(intStr, "#") {
				return 0, fmt.Errorf("invalid number (error token is %q)", orig)
			}
			s = intStr
		}
	}
	// Bases 2-36 use Go's ParseInt (handles negative sign and case-
	// insensitive a-z). Bases 37-64 need bash's custom digit map:
	// 0-9 → 0-9, a-z → 10-35, A-Z → 36-61, @ → 62, _ → 63.
	if base <= 36 {
		n, err := strconv.ParseInt(s, int(base), 64)
		if err != nil && numericLiteralLike(orig) {
			return 0, fmt.Errorf("value too great for base (error token is %q)", orig)
		}
		return n, nil
	}
	n, ok := bashBaseAtoi(s, base)
	if !ok {
		return 0, fmt.Errorf("value too great for base (error token is %q)", orig)
	}
	return n, nil
}

func numericLiteralLike(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.Contains(s, "#") {
		return true
	}
	if len(s) > 1 && s[0] == '0' {
		for i := 1; i < len(s); i++ {
			if s[i] < '0' || s[i] > '7' {
				return true
			}
		}
	}
	return false
}

func literalDollarArithmToken(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] != '$' {
			continue
		}
		j := i + 1
		for j < len(s) {
			c := s[j]
			if c != '_' && (c < '0' || c > '9') &&
				(c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
				break
			}
			j++
		}
		if j == i+1 {
			return "$"
		}
		for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
			j++
		}
		return s[i:j]
	}
	return ""
}

func arithmParseError(s string, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "must be followed by an expression"):
		tok := strings.TrimSpace(s)
		if idx := strings.LastIndexAny(tok, "+-*/%<>=!&|^?:,"); idx >= 0 {
			tok = tok[idx:]
		}
		return fmt.Errorf("arithmetic syntax error: operand expected (error token is %q)", tok)
	case strings.Contains(msg, "without matching `((` with `))`"):
		tok := strings.TrimSpace(s)
		if fields := strings.Fields(tok); len(fields) > 0 {
			tok = fields[len(fields)-1]
		}
		return fmt.Errorf("missing `)' (error token is %q)", tok)
	default:
		return err
	}
}

// bashBaseAtoi parses an integer in bash's extended digit alphabet
// for bases 2-64. Returns 0 on any invalid digit; callers don't
// distinguish "invalid" from "zero" here, matching the silent
// behaviour of [atoi] for malformed input.
func bashBaseAtoi(s string, base int64) (int64, bool) {
	if len(s) == 0 {
		return 0, true
	}
	neg := false
	switch s[0] {
	case '+':
		s = s[1:]
	case '-':
		neg = true
		s = s[1:]
	}
	var n int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		var d int64
		switch {
		case c >= '0' && c <= '9':
			d = int64(c - '0')
		case c >= 'a' && c <= 'z':
			d = int64(c-'a') + 10
		case c >= 'A' && c <= 'Z':
			d = int64(c-'A') + 36
		case c == '@':
			d = 62
		case c == '_':
			d = 63
		default:
			return 0, false
		}
		if d >= base {
			return 0, false
		}
		n = n*base + d
	}
	if neg {
		n = -n
	}
	return n, true
}

func (cfg *Config) assgnArit(b *syntax.BinaryArithm) (int, error) {
	// Bash 5.3 accepts `7=4`, `(a)=4` at parse time and errors here
	// with "attempted assignment to non-variable". The arith parser
	// no longer rejects non-name lvalues so the for-loop tests can
	// reach this runtime path; surface the same wording bash uses.
	lval, ok := arithLvalueFrom(b.X)
	if !ok {
		return 0, fmt.Errorf("attempted assignment to non-variable")
	}
	lval, err := cfg.resolveAritLvalueName(lval)
	if err != nil {
		return 0, err
	}
	if !syntax.ValidName(lval.name) {
		return 0, fmt.Errorf("attempted assignment to non-variable")
	}
	if word, ok := b.Y.(*syntax.Word); ok && arithMissingRHS(word) {
		return 0, fmt.Errorf("arithmetic syntax error: operand expected (error token is %q)", b.Op.String())
	}
	lval, err = cfg.resolveAritLvalue(lval)
	if err != nil {
		return 0, err
	}
	val, err := cfg.getAritLvalue(lval)
	if err != nil {
		return 0, err
	}
	arg_, err := Arithm(cfg, b.Y)
	if err != nil {
		return 0, err
	}
	arg := int64(arg_)
	switch b.Op {
	case syntax.Assgn:
		val = arg
	case syntax.AddAssgn:
		val += arg
	case syntax.SubAssgn:
		val -= arg
	case syntax.MulAssgn:
		val *= arg
	case syntax.QuoAssgn:
		if arg == 0 {
			return 0, fmt.Errorf("division by zero")
		}
		val /= arg
	case syntax.RemAssgn:
		if arg == 0 {
			return 0, fmt.Errorf("division by zero")
		}
		val %= arg
	case syntax.AndAssgn:
		val &= arg
	case syntax.OrAssgn:
		val |= arg
	case syntax.XorAssgn:
		val ^= arg
	case syntax.ShlAssgn:
		val <<= uint(arg)
	case syntax.ShrAssgn:
		val >>= uint(arg)
	}
	if err := cfg.setAritLvalue(lval, val); err != nil {
		return 0, err
	}
	return int(val), nil
}

func arithMissingRHS(word *syntax.Word) bool {
	if len(word.Parts) != 1 {
		return false
	}
	lit, ok := word.Parts[0].(*syntax.Lit)
	return ok && lit.Value == "" && lit.Pos() == lit.End()
}

func intPow(a, b int) int {
	p := 1
	for b > 0 {
		if b&1 != 0 {
			p *= a
		}
		b >>= 1
		a *= a
	}
	return p
}

func binArit(op syntax.BinAritOperator, x, y int) (int, error) {
	switch op {
	case syntax.Add:
		return x + y, nil
	case syntax.Sub:
		return x - y, nil
	case syntax.Mul:
		return x * y, nil
	case syntax.Quo:
		if y == 0 {
			return 0, fmt.Errorf("division by zero")
		}
		return x / y, nil
	case syntax.Rem:
		if y == 0 {
			return 0, fmt.Errorf("division by zero")
		}
		return x % y, nil
	case syntax.Pow:
		return intPow(x, y), nil
	case syntax.Eql:
		return oneIf(x == y), nil
	case syntax.Gtr:
		return oneIf(x > y), nil
	case syntax.Lss:
		return oneIf(x < y), nil
	case syntax.Neq:
		return oneIf(x != y), nil
	case syntax.Leq:
		return oneIf(x <= y), nil
	case syntax.Geq:
		return oneIf(x >= y), nil
	case syntax.And:
		return x & y, nil
	case syntax.Or:
		return x | y, nil
	case syntax.Xor:
		return x ^ y, nil
	case syntax.Shr:
		return x >> uint(y), nil
	case syntax.Shl:
		return x << uint(y), nil
	case syntax.AndArit:
		return oneIf(x != 0 && y != 0), nil
	case syntax.OrArit:
		return oneIf(x != 0 || y != 0), nil
	case syntax.Comma:
		// x is executed but its result discarded
		return y, nil
	default:
		return 0, fmt.Errorf("unsupported binary arithmetic operator: %q", op)
	}
}
