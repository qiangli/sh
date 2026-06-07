// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"mvdan.cc/sh/v3/pattern"
	"mvdan.cc/sh/v3/syntax"
)

// stripBackslashEscapes removes a single backslash before any
// character, mirroring bash 5.3's quote-removal pass on the replacement
// in ${var/pat/repl}: `\X` → `X` for any X, `\\` → `\`. A trailing
// backslash is kept as-is.
func stripBackslashEscapes(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			b.WriteByte(s[i+1])
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func stripParamExpLitEscapes(s string, stripSingle bool) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '"', '\\', '$', '`':
				b.WriteByte(s[i+1])
				i++
				continue
			case '\'':
				if stripSingle {
					b.WriteByte(s[i+1])
					i++
					continue
				}
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func (cfg *Config) literalParamExpWord(word *syntax.Word, innerDoubleQuoted bool) (string, error) {
	if word == nil {
		return "", nil
	}
	var sb strings.Builder
	for _, part := range word.Parts {
		switch part := part.(type) {
		case *syntax.SglQuoted:
			if cfg.insideDoubleQuote {
				val, err := cfg.literalParamExpQuotedText(part.Value)
				if err != nil {
					return "", err
				}
				sb.WriteByte('\'')
				sb.WriteString(val)
				sb.WriteByte('\'')
			} else {
				sb.WriteString(part.Value)
			}
		case *syntax.DblQuoted:
			val, err := cfg.literalParamExpWord(&syntax.Word{Parts: part.Parts}, true)
			if err != nil {
				return "", err
			}
			sb.WriteString(val)
		case *syntax.Lit:
			if innerDoubleQuoted {
				if cfg.insideDoubleQuote {
					sb.WriteString(stripBackslashEscapes(part.Value))
				} else {
					sb.WriteString(stripParamExpLitEscapes(part.Value, false))
				}
			} else if !cfg.insideDoubleQuote {
				sb.WriteString(stripBackslashEscapes(part.Value))
			} else {
				sb.WriteString(stripParamExpLitEscapes(part.Value, false))
			}
		default:
			val, err := Literal(cfg, &syntax.Word{Parts: []syntax.WordPart{part}})
			if err != nil {
				return "", err
			}
			sb.WriteString(val)
		}
	}
	return sb.String(), nil
}

func (cfg *Config) literalParamExpQuotedText(text string) (string, error) {
	if !strings.ContainsAny(text, "$`\\") {
		return text, nil
	}
	word, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Document(strings.NewReader(text))
	if err != nil && err != io.EOF {
		return "", err
	}
	return Literal(cfg, word)
}

func paramExpWordSingleQuotesOnly(word *syntax.Word) bool {
	if word == nil || len(word.Parts) == 0 {
		return false
	}
	for _, part := range word.Parts {
		sq, ok := part.(*syntax.SglQuoted)
		if !ok || sq.Dollar {
			return false
		}
	}
	return true
}

func nodeLit(node syntax.Node) string {
	if word, ok := node.(*syntax.Word); ok {
		return word.Lit()
	}
	return ""
}

// UnsetParameterError is returned when a parameter expansion encounters an
// unset variable and [Config.NoUnset] has been set.
type UnsetParameterError struct {
	Node    *syntax.ParamExp
	Message string
}

func (u UnsetParameterError) Error() string {
	return fmt.Sprintf("%s: %s", u.Node.Param.Value, u.Message)
}

// BadSubstitutionError is returned for malformed parameter expansions which
// bash accepts at parse time but rejects during expansion.
type BadSubstitutionError struct {
	Node *syntax.ParamExp
}

func (b BadSubstitutionError) Error() string { return "bad substitution" }

func bashLengthSliceExpr(pe *syntax.ParamExp, expr syntax.ArithmExpr) syntax.ArithmExpr {
	if pe.Param == nil || pe.Param.Value != "#" || expr == nil {
		return expr
	}
	return &syntax.Word{Parts: []syntax.WordPart{
		&syntax.Lit{ValuePos: pe.Param.Pos(), Value: "#: " + nodeLit(expr)},
	}}
}

func overridingUnset(pe *syntax.ParamExp) bool {
	if pe.Exp == nil {
		return false
	}
	switch pe.Exp.Op {
	case syntax.AlternateUnset, syntax.AlternateUnsetOrNull,
		syntax.DefaultUnset, syntax.DefaultUnsetOrNull,
		syntax.ErrorUnset, syntax.ErrorUnsetOrNull,
		syntax.AssignUnset, syntax.AssignUnsetOrNull:
		return true
	}
	return false
}

func bashAlternateCommandSubstEOF(word *syntax.Word) bool {
	if word == nil {
		return false
	}
	for _, part := range word.Parts {
		sq, ok := part.(*syntax.SglQuoted)
		if !ok {
			continue
		}
		idx := strings.Index(sq.Value, "$(")
		if idx >= 0 && !strings.Contains(sq.Value[idx+2:], ")") {
			return true
		}
	}
	return false
}

// bashAssocBucket computes the bucket index bash 5.3 stores an
// associative-array key in — FNV-1 with the historical initial
// value (2166136261) and prime (16777619), modulo 1024 (bash's
// ASSOC_HASH_BUCKETS). Iterating bucket-ascending matches bash's
// `${arr[@]}` order on assoc arrays.
func bashAssocBucket(s string) uint32 {
	i := uint32(2166136261)
	for _, c := range []byte(s) {
		i = i * 16777619
		i = i ^ uint32(c)
	}
	return i % 1024
}

// AssocKeysInBashOrder returns the keys of an associative-array
// map sorted by bash 5.3's hash-table iteration order so callers
// (e.g. `declare -p` printers in interp) can produce bash-shaped
// output without duplicating the hash math. Collisions break ties
// lexically.
func AssocKeysInBashOrder(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortStableFunc(keys, func(a, b string) int {
		ba := bashAssocBucket(a)
		bb := bashAssocBucket(b)
		if ba != bb {
			return int(ba) - int(bb)
		}
		return strings.Compare(a, b)
	})
	return keys
}

// applyParamMods applies the trailing modifier portion of a parameter
// expansion (Slice, Repl, Exp) to a precomputed string value. Used
// by the indirect-expansion path (`${!x//c/x}`) where the value
// comes from a name lookup but the substitution should still apply.
func applyParamMods(cfg *Config, pe *syntax.ParamExp, str string) (string, error) {
	if pe.Repl == nil {
		return str, nil
	}
	orig, err := Pattern(cfg, pe.Repl.Orig)
	if err != nil {
		return "", err
	}
	if orig == "" {
		return str, nil
	}
	var with string
	if pe.Repl.With != nil {
		var withSb strings.Builder
		for _, part := range pe.Repl.With.Parts {
			if lit, ok := part.(*syntax.Lit); ok {
				withSb.WriteString(stripBackslashEscapes(lit.Value))
				continue
			}
			s, lerr := Literal(cfg, &syntax.Word{Parts: []syntax.WordPart{part}})
			if lerr != nil {
				return "", lerr
			}
			withSb.WriteString(s)
		}
		with = withSb.String()
	}
	n := 1
	if pe.Repl.All {
		n = -1
	}
	locs := findAllIndex(orig, str, n)
	var sb strings.Builder
	last := 0
	for _, loc := range locs {
		sb.WriteString(str[last:loc[0]])
		sb.WriteString(with)
		last = loc[1]
	}
	sb.WriteString(str[last:])
	return sb.String(), nil
}

func (cfg *Config) paramExp(pe *syntax.ParamExp) (string, error) {
	oldParam := cfg.curParam
	cfg.curParam = pe
	defer func() { cfg.curParam = oldParam }()

	name := pe.Param.Value
	index := pe.Index
	switch name {
	case "@", "*":
		index = &syntax.Word{Parts: []syntax.WordPart{
			&syntax.Lit{Value: name},
		}}
	}
	var vr Variable
	switch name {
	case "LINENO":
		// This is the only parameter expansion that the environment
		// interface cannot satisfy. When the interpreter has set
		// [Config.OverrideLineno] (e.g. while expanding a trap
		// body), use it instead of the actual parser position.
		var line uint64
		if cfg.OverrideLineno > 0 {
			line = uint64(cfg.OverrideLineno)
		} else {
			line = uint64(cfg.curParam.Pos().Line())
		}
		vr = Variable{Set: true, Kind: String, Str: strconv.FormatUint(line, 10)}
	default:
		vr = cfg.Env.Get(name)
	}
	orig := vr
	if n, v := vr.Resolve(cfg.Env); n != "" {
		name, vr = n, v
	}
	if cfg.NoUnset && !vr.IsSet() && !overridingUnset(pe) {
		return "", UnsetParameterError{
			Node:    pe,
			Message: "unbound variable",
		}
	}

	var sliceOffset, sliceLen int
	if pe.Slice != nil {
		var err error
		if pe.Slice.Offset != nil {
			if pe.Param != nil && pe.Param.Value == "#" && nodeLit(pe.Slice.Offset) == "%" {
				return "", &ArithmError{
					Expr: bashLengthSliceExpr(pe, pe.Slice.Offset),
					Err:  fmt.Errorf("arithmetic syntax error: operand expected (error token is %q)", "%"),
				}
			}
			sliceOffset, err = Arithm(cfg, pe.Slice.Offset)
			if err != nil {
				if arithErr, ok := err.(*ArithmError); ok {
					arithErr.Expr = bashLengthSliceExpr(pe, arithErr.Expr)
				}
				return "", err
			}
		}
		if pe.Slice.Length != nil {
			sliceLen, err = Arithm(cfg, pe.Slice.Length)
			if err != nil {
				return "", err
			}
		}
	}

	var (
		str   string
		elems []string

		indexAllElements bool // true if var has been accessed with * or @ index
		callVarInd       = true
	)

	switch nodeLit(index) {
	case "@", "*":
		switch vr.Kind {
		case Unknown:
			elems = nil
			indexAllElements = true
		case Indexed:
			indexAllElements = true
			callVarInd = false
			elems = cfg.sliceElems(pe, vr.List, name == "@" || name == "*")
			str = strings.Join(elems, " ")
		case Associative:
			indexAllElements = true
			callVarInd = false
			// Bash iterates assoc-array values in bash-bucket
			// order (see AssocKeysInBashOrder).
			keys := AssocKeysInBashOrder(vr.Map)
			elems = make([]string, len(keys))
			for i, k := range keys {
				elems[i] = vr.Map[k]
			}
			str = strings.Join(elems, " ")
		}
	}
	if callVarInd {
		var err error
		str, err = cfg.varInd(vr, index)
		if err != nil {
			return "", err
		}
	}
	if !indexAllElements {
		elems = []string{str}
	}

	switch {
	case pe.Names != 0 && !pe.Excl:
		// `${name*}` without `!` — bash 5.3 parses this but emits
		// `bad substitution` at expansion time.
		return "", BadSubstitutionError{Node: pe}
	case pe.Length:
		n := len(elems)
		switch nodeLit(index) {
		case "@", "*":
		default:
			n = utf8.RuneCountInString(str)
		}
		str = strconv.Itoa(n)
	case pe.Excl:
		var strs []string
		applyMod := false
		switch {
		case pe.Names != 0:
			strs = cfg.namesByPrefix(pe.Param.Value)
		case orig.Kind == NameRef:
			strs = append(strs, orig.Str)
		case pe.Index != nil && vr.Kind == Indexed:
			for i, e := range vr.List {
				if e != "" {
					strs = append(strs, strconv.Itoa(i))
				}
			}
		case pe.Index != nil && vr.Kind == Associative:
			strs = slices.AppendSeq(strs, maps.Keys(vr.Map))
		case !vr.IsSet():
			// Bash 5.3 includes the variable name in the message
			// (`./file: line N: foo: invalid indirect expansion`).
			return "", fmt.Errorf("%s: invalid indirect expansion", name)
		case str == "":
			return "", nil
		default:
			vr = cfg.Env.Get(str)
			strs = append(strs, vr.String())
			// `${!x//c/x}` — apply trailing modifiers to the
			// dereferenced value (bash 5.3).
			applyMod = true
		}
		slices.Sort(strs)
		str = strings.Join(strs, " ")
		if applyMod {
			if mod, err := applyParamMods(cfg, pe, str); err != nil {
				return "", err
			} else {
				str = mod
			}
		}
	case pe.Width:
		// mksh's ${%var}: character width of the string value.
		str = strconv.Itoa(utf8.RuneCountInString(str))
	case pe.IsSet:
		// Zsh's ${+var}: 1 if set, 0 if unset.
		if vr.IsSet() {
			str = "1"
		} else {
			str = "0"
		}
	case pe.Slice != nil:
		if callVarInd {
			slicePos := func(n int) int {
				if n < 0 {
					n = len(str) + n
					if n < 0 {
						n = len(str)
					}
				} else if n > len(str) {
					n = len(str)
				}
				return n
			}
			if pe.Slice.Offset != nil {
				str = str[slicePos(sliceOffset):]
			}
			if pe.Slice.Length != nil {
				str = str[:slicePos(sliceLen)]
			}
		} // else, elems are already sliced
	case pe.Repl != nil:
		orig, err := Pattern(cfg, pe.Repl.Orig)
		if err != nil {
			return "", err
		}
		if orig == "" {
			break // nothing to replace
		}
		// Bash 5.3 applies quote-removal to the replacement string:
		// a backslash in the *unquoted* portion escapes the next
		// character (`\'` → `'`, `\\` → `\`, `\&` → `&`). Already-
		// quoted parts (DblQuoted, SglQuoted) keep their text as
		// Literal returned it, since they've already been through
		// the relevant per-context backslash handling. So we walk
		// the word parts: strip backslashes only on the bare Lit
		// pieces, concatenate the rest as Literal would have.
		var with string
		if pe.Repl.With != nil {
			var withSb strings.Builder
			for _, part := range pe.Repl.With.Parts {
				if lit, ok := part.(*syntax.Lit); ok {
					withSb.WriteString(stripBackslashEscapes(lit.Value))
					continue
				}
				s, lerr := Literal(cfg, &syntax.Word{Parts: []syntax.WordPart{part}})
				if lerr != nil {
					return "", lerr
				}
				withSb.WriteString(s)
			}
			with = withSb.String()
		}
		n := 1
		if pe.Repl.All {
			n = -1
		}
		out := slices.Clone(elems)
		for i, elem := range out {
			locs := findAllIndex(orig, elem, n)
			sb := cfg.strBuilder()
			last := 0
			for _, loc := range locs {
				sb.WriteString(elem[last:loc[0]])
				sb.WriteString(with)
				last = loc[1]
			}
			sb.WriteString(elem[last:])
			out[i] = sb.String()
		}
		str = strings.Join(out, " ")
	case pe.Exp != nil:
		// Bash 5.3 keeps `$'…'` ANSI-C sequences literal inside the
		// substitute text of a default-value parameter expansion
		// (`${var-DEFAULT}`, `${var+ALT}`, `${var=ASSIGN}`,
		// `${var?ERR}`) when the whole thing is evaluated for a
		// heredoc body. Other operations (pattern strip, replace,
		// substring, case-fold) still decode `$'…'` normally.
		op := pe.Exp.Op
		isDefaultLike := op == syntax.AlternateUnset || op == syntax.AlternateUnsetOrNull ||
			op == syntax.DefaultUnset || op == syntax.DefaultUnsetOrNull ||
			op == syntax.AssignUnset || op == syntax.AssignUnsetOrNull ||
			op == syntax.ErrorUnset || op == syntax.ErrorUnsetOrNull
		isPatternOp := op == syntax.RemSmallPrefix || op == syntax.RemLargePrefix ||
			op == syntax.RemSmallSuffix || op == syntax.RemLargeSuffix
		var arg string
		var err error
		switch {
		case cfg.inHeredocBody && isDefaultLike:
			arg, err = literalKeepAnsiC(cfg, pe.Exp.Word)
		case isPatternOp:
			// `${var%PAT}` family: quoted segments of PAT are
			// literal characters, not glob metacharacters.
			// `${P%"*"}` removes a literal `*`, not the longest
			// possible match.
			arg, err = Pattern(cfg, pe.Exp.Word)
		case op == syntax.AlternateUnset || op == syntax.AlternateUnsetOrNull:
			arg, err = cfg.literalParamExpWord(pe.Exp.Word, false)
		case cfg.insideDoubleQuote && (op == syntax.DefaultUnset || op == syntax.DefaultUnsetOrNull) &&
			paramExpWordSingleQuotesOnly(pe.Exp.Word):
			arg, err = cfg.literalParamExpWord(pe.Exp.Word, false)
		default:
			arg, err = Literal(cfg, pe.Exp.Word)
		}
		if err != nil {
			if (op == syntax.AlternateUnset || op == syntax.AlternateUnsetOrNull) &&
				vr.IsSet() && bashAlternateCommandSubstEOF(pe.Exp.Word) {
				return "", fmt.Errorf("command substitution: line %d: unexpected EOF while looking for matching `)'", pe.Pos().Line()+2)
			}
			return "", err
		}
		switch op {
		case syntax.AlternateUnsetOrNull:
			if str == "" {
				break
			}
			fallthrough
		case syntax.AlternateUnset:
			if vr.IsSet() {
				if bashAlternateCommandSubstEOF(pe.Exp.Word) {
					return "", fmt.Errorf("command substitution: line %d: unexpected EOF while looking for matching `)'", pe.Pos().Line()+2)
				}
				str = arg
			}
		case syntax.DefaultUnset:
			if vr.IsSet() {
				break
			}
			fallthrough
		case syntax.DefaultUnsetOrNull:
			if str == "" {
				str = arg
			}
		case syntax.ErrorUnset:
			if vr.IsSet() {
				break
			}
			fallthrough
		case syntax.ErrorUnsetOrNull:
			if str == "" {
				return "", UnsetParameterError{
					Node:    pe,
					Message: arg,
				}
			}
		case syntax.AssignUnset:
			if vr.IsSet() {
				break
			}
			fallthrough
		case syntax.AssignUnsetOrNull:
			if str == "" {
				if err := cfg.envSet(name, arg); err != nil {
					return "", err
				}
				str = arg
			}
		case syntax.RemSmallPrefix, syntax.RemLargePrefix,
			syntax.RemSmallSuffix, syntax.RemLargeSuffix:
			suffix := op == syntax.RemSmallSuffix || op == syntax.RemLargeSuffix
			small := op == syntax.RemSmallPrefix || op == syntax.RemSmallSuffix
			out := slices.Clone(elems)
			for i, elem := range out {
				out[i] = removePattern(elem, arg, suffix, small)
			}
			str = strings.Join(out, " ")
		case syntax.UpperFirst, syntax.UpperAll,
			syntax.LowerFirst, syntax.LowerAll,
			syntax.CaseToggleFirst, syntax.CaseToggleAll:

			caseFunc := unicode.ToLower
			if op == syntax.UpperFirst || op == syntax.UpperAll {
				caseFunc = unicode.ToUpper
			} else if op == syntax.CaseToggleFirst || op == syntax.CaseToggleAll {
				caseFunc = func(r rune) rune {
					if unicode.IsUpper(r) {
						return unicode.ToLower(r)
					}
					return unicode.ToUpper(r)
				}
			}
			all := op == syntax.UpperAll || op == syntax.LowerAll || op == syntax.CaseToggleAll

			// empty string means '?'; nothing to do there
			expr, err := pattern.Regexp(arg, 0)
			if err != nil {
				return str, nil
			}
			rx, err := regexp.Compile(expr)
			if err != nil {
				return str, nil
			}

			// Casemod operates on a copy — `elems` may alias the
			// underlying array's List slice, and bash does not
			// mutate the variable when an expansion-time casemod
			// is applied.
			out := make([]string, len(elems))
			for i, elem := range elems {
				rs := []rune(elem)
				if all {
					for ri, r := range rs {
						if rx.MatchString(string(r)) {
							rs[ri] = caseFunc(r)
						}
					}
				} else if len(rs) > 0 && rx.MatchString(string(rs[0])) {
					// Single ^ / , / ~: only the first char of
					// each element is tested; no fall-through
					// to subsequent runes.
					rs[0] = caseFunc(rs[0])
				}
				out[i] = string(rs)
			}
			str = strings.Join(out, " ")
		case syntax.OtherParamOps:
			switch arg {
			case "Q":
				str, err = syntax.Quote(str, syntax.LangBash)
				if err != nil {
					// Is this even possible? If a user runs into this panic,
					// it's most likely a bug we need to fix.
					panic(err)
				}
			case "E":
				tail := str
				var rns []rune
				for tail != "" {
					var rn rune
					rn, _, tail, _ = strconv.UnquoteChar(tail, 0)
					rns = append(rns, rn)
				}
				str = string(rns)
			case "a":
				// ${var@a} returns variable attribute flags.
				// We use orig (before nameref resolve) for the attributes.
				str = orig.Flags()
			case "A":
				// ${var@A} returns a declare statement that recreates the variable.
				flags := orig.Flags()
				quoted, err := syntax.Quote(str, syntax.LangBash)
				if err != nil {
					return "", err
				}
				if flags == "" {
					str = fmt.Sprintf("%s=%s", name, quoted)
				} else {
					str = fmt.Sprintf("declare -%s %s=%s", flags, name, quoted)
				}
			case "U":
				str = strings.ToUpper(str)
			case "u":
				if str != "" {
					r, size := utf8.DecodeRuneInString(str)
					str = string(unicode.ToUpper(r)) + str[size:]
				}
			case "L":
				str = strings.ToLower(str)
			case "K":
				str = cfg.paramAtK(vr, name)
			case "k":
				str = cfg.paramAtK(vr, name)
			case "P":
				str = cfg.expandPrompt(str)
			default:
				return "", fmt.Errorf("unexpected @%s param expansion", arg)
			}
		}
	}
	return str, nil
}

// paramAtK implements ${var@K} and ${var@k}, producing quoted key-value pairs
// for arrays. For indexed arrays: "0 val0 1 val1 ...".
// For associative arrays: "key1 val1 key2 val2 ...".
// For plain strings, returns the value unchanged.
func (cfg *Config) paramAtK(vr Variable, name string) string {
	switch vr.Kind {
	case Indexed:
		var parts []string
		for i, v := range vr.List {
			if v != "" {
				quoted, err := syntax.Quote(v, syntax.LangBash)
				if err != nil {
					quoted = v
				}
				parts = append(parts, strconv.Itoa(i)+" "+quoted)
			}
		}
		return strings.Join(parts, " ")
	case Associative:
		keys := slices.Sorted(maps.Keys(vr.Map))
		var parts []string
		for _, k := range keys {
			v := vr.Map[k]
			quotedK, err := syntax.Quote(k, syntax.LangBash)
			if err != nil {
				quotedK = k
			}
			quotedV, err := syntax.Quote(v, syntax.LangBash)
			if err != nil {
				quotedV = v
			}
			parts = append(parts, quotedK+" "+quotedV)
		}
		return strings.Join(parts, " ")
	default:
		return vr.String()
	}
}

// expandPrompt implements ${var@P} by delegating to [Config.PromptExpand].
// If PromptExpand is not set, it performs basic prompt escape expansion.
func (cfg *Config) expandPrompt(s string) string {
	if cfg.PromptExpand != nil {
		return cfg.PromptExpand(s)
	}
	return defaultPromptExpand(s)
}

// defaultPromptExpand handles a subset of Bash prompt escape sequences.
func defaultPromptExpand(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s) {
			b.WriteByte('\\')
			break
		}
		switch s[i] {
		case 'a':
			b.WriteByte('\a')
		case 'e':
			b.WriteByte('\x1b')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case '\\':
			b.WriteByte('\\')
		case '[', ']':
			// Non-printing sequence markers; ignore.
		default:
			// For unrecognized sequences, preserve them.
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func removePattern(str, pat string, fromEnd, shortest bool) string {
	var mode pattern.Mode
	if shortest {
		mode |= pattern.Shortest
	}
	expr, err := pattern.Regexp(pat, mode)
	if err != nil {
		return str
	}
	switch {
	case fromEnd && shortest:
		// use .* to get the right-most shortest match
		expr = ".*(" + expr + ")$"
	case fromEnd:
		// simple suffix
		expr = "(" + expr + ")$"
	default:
		// simple prefix
		expr = "^(" + expr + ")"
	}
	// no need to check error as Translate returns one
	rx := regexp.MustCompile(expr)
	if loc := rx.FindStringSubmatchIndex(str); loc != nil {
		// remove the original pattern (the submatch)
		str = str[:loc[2]] + str[loc[3]:]
	}
	return str
}

func (cfg *Config) varInd(vr Variable, idx syntax.ArithmExpr) (string, error) {
	if idx == nil {
		return vr.String(), nil
	}
	switch vr.Kind {
	case String:
		n, err := Arithm(cfg, idx)
		if err != nil {
			return "", err
		}
		if n == 0 {
			return vr.Str, nil
		}
	case Indexed:
		switch nodeLit(idx) {
		case "*", "@":
			return strings.Join(vr.List, " "), nil
		}
		i, err := Arithm(cfg, idx)
		if err != nil {
			return "", err
		}
		if i < 0 {
			return "", fmt.Errorf("negative array index")
		}
		if i < len(vr.List) {
			return vr.List[i], nil
		}
	case Associative:
		switch lit := nodeLit(idx); lit {
		case "@", "*":
			strs := slices.Sorted(maps.Values(vr.Map))
			if lit == "*" {
				return cfg.ifsJoin(strs), nil
			}
			return strings.Join(strs, " "), nil
		}
		val, err := Literal(cfg, idx.(*syntax.Word))
		if err != nil {
			return "", err
		}
		return vr.Map[val], nil
	}
	return "", nil
}

func (cfg *Config) namesByPrefix(prefix string) []string {
	var names []string
	for name := range cfg.Env.Each {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	return names
}
