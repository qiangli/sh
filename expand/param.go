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

	"mvdan.cc/sh/v3/internal"
	"mvdan.cc/sh/v3/pattern"
	"mvdan.cc/sh/v3/syntax"
)

func indexedNegativeOffset(vr Variable, n int) int {
	indexes := vr.IndexedIndexes()
	if len(indexes) == 0 {
		return n
	}
	return indexes[len(indexes)-1] + 1 + n
}

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
			case '"', '\\', '$', '`', '}':
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

func paramExpWordHasBackslashLit(word *syntax.Word) bool {
	if word == nil {
		return false
	}
	for _, part := range word.Parts {
		switch part := part.(type) {
		case *syntax.Lit:
			if strings.ContainsRune(part.Value, '\\') {
				return true
			}
		case *syntax.DblQuoted:
			if paramExpWordHasBackslashLit(&syntax.Word{Parts: part.Parts}) {
				return true
			}
		}
	}
	return false
}

func paramExpWordHasSingleQuotedLeadingDouble(word *syntax.Word) bool {
	if word == nil {
		return false
	}
	for _, part := range word.Parts {
		sq, ok := part.(*syntax.SglQuoted)
		if ok && !sq.Dollar && strings.HasPrefix(sq.Value, `"`) {
			return true
		}
	}
	return false
}

func (cfg *Config) literalParamExpDoubleQuotedDefault(word *syntax.Word) (string, error) {
	var sb strings.Builder
	for _, part := range word.Parts {
		switch part := part.(type) {
		case *syntax.SglQuoted:
			if !part.Dollar && strings.HasPrefix(part.Value, `"`) {
				sb.WriteByte('\'')
				sb.WriteString(strings.TrimPrefix(part.Value, `"`))
				sb.WriteByte('\'')
				continue
			}
		}
		val, err := cfg.literalParamExpWord(&syntax.Word{Parts: []syntax.WordPart{part}}, false)
		if err != nil {
			return "", err
		}
		sb.WriteString(val)
	}
	return sb.String(), nil
}

func paramExpDefaultTriggers(op syntax.ParExpOperator, vr Variable, str string) bool {
	switch op {
	case syntax.DefaultUnset:
		return !vr.IsSet()
	case syntax.DefaultUnsetOrNull:
		return !vr.IsSet() || str == ""
	}
	return false
}

func indirectDefaultOp(op syntax.ParExpOperator) bool {
	switch op {
	case syntax.AlternateUnset, syntax.AlternateUnsetOrNull,
		syntax.DefaultUnset, syntax.DefaultUnsetOrNull,
		syntax.ErrorUnset, syntax.ErrorUnsetOrNull:
		return true
	}
	return false
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
	Name    string
	Message string
}

func (u UnsetParameterError) Error() string {
	name := u.Name
	if name == "" && u.Node != nil && u.Node.Param != nil {
		name = u.Node.Param.Value
		if u.Node.Short {
			numeric := true
			for i := 0; i < len(name); i++ {
				if name[i] < '0' || name[i] > '9' {
					numeric = false
					break
				}
			}
			if numeric && name != "" {
				name = "$" + name
			}
		}
	}
	return fmt.Sprintf("%s: %s", name, u.Message)
}

// BadSubstitutionError is returned for malformed parameter expansions which
// bash accepts at parse time but rejects during expansion.
type BadSubstitutionError struct {
	Node *syntax.ParamExp
}

func (b BadSubstitutionError) Error() string { return "bad substitution" }

// IndirectExpansionError is returned when an indirect expansion ${!var}
// fails because var itself is unset. Bash treats the failure as fatal
// unless a default-style operator (:- and friends) follows the
// indirection, in which case the command fails with $? = 1 and
// execution continues (errors6.sub lines 50-51).
type IndirectExpansionError struct {
	Name     string
	NonFatal bool
}

func (i IndirectExpansionError) Error() string {
	return i.Name + ": invalid indirect expansion"
}

func bashLengthSliceExpr(pe *syntax.ParamExp, expr syntax.ArithmExpr) syntax.ArithmExpr {
	if pe.Param == nil || pe.Param.Value != "#" || expr == nil {
		return expr
	}
	return &syntax.Word{Parts: []syntax.WordPart{
		&syntax.Lit{ValuePos: pe.Param.Pos(), Value: "#: " + nodeLit(expr)},
	}}
}

func bashParamSliceExpr(pe *syntax.ParamExp, expr syntax.ArithmExpr) syntax.ArithmExpr {
	if pe.Param == nil || expr == nil {
		return expr
	}
	return &syntax.Word{Parts: []syntax.WordPart{
		&syntax.Lit{ValuePos: pe.Param.Pos(), Value: pe.Param.Value + ": " + nodeLit(expr)},
	}}
}

func bashParamSliceText(pe *syntax.ParamExp, err error) string {
	if pe.Param == nil {
		return ""
	}
	msg := err.Error()
	const prefix = "error token is \""
	start := strings.Index(msg, prefix)
	if start < 0 {
		return ""
	}
	start += len(prefix)
	end := strings.IndexByte(msg[start:], '"')
	if end < 0 {
		return ""
	}
	return pe.Param.Value + ": " + msg[start:start+end]
}

func validIndirectName(name string) bool {
	switch name {
	case "#", "@", "*", "?", "-", "$", "!", "0":
		return true
	}
	if syntax.ValidName(name) {
		return true
	}
	if _, _, ok := splitIndirectArrayRef(name); ok {
		return true
	}
	for i := 0; i < len(name); i++ {
		if name[i] < '0' || name[i] > '9' {
			return false
		}
	}
	return name != ""
}

func splitIndirectArrayRef(ref string) (base string, index *syntax.Word, ok bool) {
	base, rest, ok := strings.Cut(ref, "[")
	if !ok || !strings.HasSuffix(rest, "]") || !syntax.ValidName(base) {
		return "", nil, false
	}
	idx := strings.TrimSuffix(rest, "]")
	if idx == "" {
		return "", nil, false
	}
	return base, &syntax.Word{Parts: []syntax.WordPart{
		&syntax.Lit{Value: idx},
	}}, true
}

func cannotAssignParam(name string) bool {
	switch name {
	case "@", "*", "#", "?", "-", "$", "!", "0":
		return true
	}
	for i := 0; i < len(name); i++ {
		if name[i] < '0' || name[i] > '9' {
			return false
		}
	}
	return name != ""
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
	orig, replAnchoredStart, replAnchoredEnd, err := replPattern(cfg, pe.Repl.Orig, pe.Repl.All)
	if err != nil {
		return "", err
	}
	if orig == "" && !replAnchoredStart && !replAnchoredEnd {
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
	locs := cfg.findReplIndex(orig, str, n, replAnchoredStart, replAnchoredEnd)
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

func replPattern(cfg *Config, word *syntax.Word, all bool) (pat string, start, end bool, err error) {
	pat, err = Pattern(cfg, word)
	if err != nil || word == nil || len(word.Parts) == 0 {
		return pat, false, false, err
	}
	lit, ok := word.Parts[0].(*syntax.Lit)
	if !ok || lit.Value == "" {
		return pat, false, false, nil
	}
	if !all {
		switch lit.Value[0] {
		case '#':
			return strings.TrimPrefix(pat, "#"), true, false, nil
		case '%':
			return strings.TrimPrefix(pat, "%"), false, true, nil
		}
	}
	return pat, false, false, nil
}

func (cfg *Config) findReplIndex(pat, name string, n int, start, end bool) [][]int {
	if pat == "" {
		if start {
			return [][]int{{0, 0}}
		}
		if end {
			return [][]int{{len(name), len(name)}}
		}
		return nil
	}
	if !start && !end {
		return cfg.findAllIndex(pat, name, n)
	}
	var mode pattern.Mode
	if cfg.ExtGlob {
		mode |= pattern.ExtendedOperators
	}
	expr, err := pattern.Regexp(pat, mode)
	if err != nil {
		return nil
	}
	switch {
	case start:
		expr = "^(" + expr + ")"
	case end:
		expr = "(" + expr + ")$"
	}
	rx, err := regexp.Compile(expr)
	if err != nil {
		return nil
	}
	if loc := rx.FindStringSubmatchIndex(name); loc != nil && len(loc) >= 4 {
		return [][]int{{loc[2], loc[3]}}
	}
	return nil
}

func (cfg *Config) paramExp(pe *syntax.ParamExp) (string, error) {
	oldParam := cfg.curParam
	cfg.curParam = pe
	defer func() { cfg.curParam = oldParam }()

	if pe.BadSubst != nil {
		return "", BadSubstitutionError{Node: pe}
	}

	name := pe.Param.Value
	index := pe.Index
	if name == "!" && pe.Exp != nil && pe.Exp.Op == syntax.RemSmallPrefix && pe.Exp.Word == nil {
		return cfg.Env.Get(cfg.Env.Get("#").String()).String(), nil
	}
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
	if vr.Kind == NameRef && index == nil {
		if base, idx, ok := nameRefArrayTarget(vr.Str); ok {
			name = base
			vr = cfg.Env.Get(base)
			index = &syntax.Word{Parts: []syntax.WordPart{
				&syntax.Lit{Value: idx},
			}}
		}
	}
	if n, v := vr.Resolve(cfg.Env); n != "" {
		name, vr = n, v
	}
	if cfg.NoUnset && !vr.IsSet() && !overridingUnset(pe) {
		if pe.Index != nil {
			idxText := nodeLit(pe.Index)
			if vr.Kind != Associative {
				i, err := Arithm(cfg, pe.Index)
				if err != nil {
					return "", err
				}
				if idxText == "" {
					idxText = strconv.Itoa(i)
				}
			}
			if idxText == "" && vr.Kind == Indexed {
				if i, err := Arithm(cfg, pe.Index); err == nil {
					idxText = strconv.Itoa(i)
				}
			}
			if idxText != "" {
				return "", UnsetParameterError{
					Name:    fmt.Sprintf("%s[%s]", name, idxText),
					Message: "unbound variable",
				}
			}
		}
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
				} else {
					err = &ArithmError{
						Expr: bashParamSliceExpr(pe, pe.Slice.Offset),
						Text: bashParamSliceText(pe, err),
						Err:  err,
					}
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
		allOp := nodeLit(index)
		switch vr.Kind {
		case Unknown:
			elems = nil
			indexAllElements = true
		case Indexed:
			indexAllElements = true
			callVarInd = false
			var err error
			elems, err = cfg.sliceIndexedElems(pe, vr, name == "@" || name == "*")
			if err != nil {
				return "", err
			}
			if allOp == "*" && name != "@" && (name != "*" || cfg.ifs != "") {
				str = cfg.ifsJoin(elems)
			} else {
				str = strings.Join(elems, " ")
			}
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
			if allOp == "*" && name != "@" && (name != "*" || cfg.ifs != "") {
				str = cfg.ifsJoin(elems)
			} else {
				str = strings.Join(elems, " ")
			}
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
		if pe.Exp != nil && !indirectDefaultOp(pe.Exp.Op) {
			return "", BadSubstitutionError{Node: pe}
		}
		var strs []string
		applyMod := false
		sortStrs := false
		switch {
		case pe.Names != 0:
			if !syntax.ValidName(pe.Param.Value) {
				return "", BadSubstitutionError{Node: pe}
			}
			strs = cfg.namesByPrefix(pe.Param.Value)
			sortStrs = true
		case pe.Index != nil && vr.Kind == Indexed:
			for _, i := range vr.IndexedIndexes() {
				strs = append(strs, strconv.Itoa(i))
			}
		case pe.Index != nil && vr.Kind == Associative:
			strs = AssocKeysInBashOrder(vr.Map)
		case orig.Kind == NameRef:
			strs = append(strs, orig.Str)
		case (name == "@" || name == "*") && !vr.IsSet():
			return "", nil
		case !vr.IsSet():
			// Bash 5.3 includes the variable name in the message
			// (`./file: line N: foo: invalid indirect expansion`).
			if pe.Exp != nil && indirectDefaultOp(pe.Exp.Op) {
				if !syntax.ValidName(name) {
					// Unset positional/special parameter: the
					// default-style operator simply applies.
					break
				}
				return "", IndirectExpansionError{Name: name, NonFatal: true}
			}
			return "", IndirectExpansionError{Name: name}
		case str == "" && pe.Exp != nil && indirectDefaultOp(pe.Exp.Op):
			break
		case str == "":
			return "", nil
		default:
			if !validIndirectName(str) {
				return "", fmt.Errorf("%s: invalid variable name", str)
			}
			if base, idx, ok := splitIndirectArrayRef(str); ok {
				vr = cfg.Env.Get(base)
				val, err := cfg.varInd(vr, idx)
				if err != nil {
					return "", err
				}
				strs = append(strs, val)
				applyMod = true
				break
			}
			vr = cfg.Env.Get(str)
			switch str {
			case "@":
				strs = append(strs, vr.List...)
			case "*":
				strs = append(strs, cfg.ifsJoin(vr.List))
			default:
				strs = append(strs, vr.String())
			}
			// `${!x//c/x}` — apply trailing modifiers to the
			// dereferenced value (bash 5.3).
			applyMod = true
		}
		if sortStrs {
			slices.Sort(strs)
		}
		str = strings.Join(strs, " ")
		if pe.Exp != nil && indirectDefaultOp(pe.Exp.Op) {
			arg, err := Literal(cfg, pe.Exp.Word)
			if err != nil {
				return "", err
			}
			switch pe.Exp.Op {
			case syntax.AlternateUnset:
				if vr.IsSet() {
					str = arg
				} else {
					str = ""
				}
			case syntax.AlternateUnsetOrNull:
				if vr.IsSet() && str != "" {
					str = arg
				} else {
					str = ""
				}
			case syntax.DefaultUnset:
				if !vr.IsSet() {
					str = arg
				}
			case syntax.DefaultUnsetOrNull:
				if !vr.IsSet() || str == "" {
					str = arg
				}
			case syntax.ErrorUnset:
				if !vr.IsSet() {
					if arg == "" {
						arg = "parameter not set"
					}
					return "", UnsetParameterError{Node: pe, Message: arg}
				}
			case syntax.ErrorUnsetOrNull:
				if !vr.IsSet() || str == "" {
					if arg == "" {
						arg = "parameter null or not set"
					}
					return "", UnsetParameterError{Node: pe, Message: arg}
				}
			}
		}
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
			runes := []rune(str)
			start, end := 0, len(runes)
			slicePos := func(n int) int {
				if n < 0 {
					n = len(runes) + n
					if n < 0 {
						n = len(runes)
					}
				} else if n > len(runes) {
					n = len(runes)
				}
				return n
			}
			if pe.Slice.Offset != nil {
				start = slicePos(sliceOffset)
			}
			if pe.Slice.Length != nil {
				if sliceLen < 0 {
					end = slicePos(sliceLen)
					if start > 0 && end < len(runes) {
						end++
					}
				} else if start+sliceLen < end {
					end = start + sliceLen
				}
				if end < start {
					end = start
				}
			}
			str = string(runes[start:end])
		} // else, elems are already sliced
	case pe.Repl != nil:
		orig, replAnchoredStart, replAnchoredEnd, err := replPattern(cfg, pe.Repl.Orig, pe.Repl.All)
		if err != nil {
			return "", err
		}
		if orig == "" && !replAnchoredStart && !replAnchoredEnd {
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
		replaceOne := func(elem string) string {
			locs := cfg.findReplIndex(orig, elem, n, replAnchoredStart, replAnchoredEnd)
			sb := cfg.strBuilder()
			last := 0
			for _, loc := range locs {
				sb.WriteString(elem[last:loc[0]])
				sb.WriteString(with)
				last = loc[1]
			}
			sb.WriteString(elem[last:])
			return sb.String()
		}
		if indexAllElements && nodeLit(index) == "*" && !replAnchoredStart && !replAnchoredEnd {
			target := str
			if name == "*" {
				target = cfg.ifsJoin(elems)
			}
			str = replaceOne(target)
			break
		}
		out := slices.Clone(elems)
		for i, elem := range out {
			out[i] = replaceOne(elem)
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
		case op == syntax.AssignUnset || op == syntax.AssignUnsetOrNull:
			if cfg.ifs == "" && paramExpWordHasAtOrStar(pe.Exp.Word) {
				if assignVal, ok := cfg.simpleAtStarNullIFSAssign(pe.Exp.Word); ok {
					arg = assignVal
				} else if cfg.insideDoubleQuote {
					arg, err = cfg.literalParamExpWord(pe.Exp.Word, false)
				} else {
					arg, err = LiteralWithQuoteRemoval(cfg, pe.Exp.Word)
				}
			} else if cfg.insideDoubleQuote {
				arg, err = cfg.literalParamExpWord(pe.Exp.Word, false)
			} else {
				arg, err = LiteralWithQuoteRemoval(cfg, pe.Exp.Word)
			}
		case cfg.insideDoubleQuote && paramExpDefaultTriggers(op, vr, str) &&
			(op == syntax.DefaultUnset || op == syntax.DefaultUnsetOrNull) &&
			paramExpWordHasSingleQuotedLeadingDouble(pe.Exp.Word):
			arg, err = cfg.literalParamExpDoubleQuotedDefault(pe.Exp.Word)
		case cfg.insideDoubleQuote && paramExpDefaultTriggers(op, vr, str) &&
			(op == syntax.DefaultUnset || op == syntax.DefaultUnsetOrNull) &&
			(paramExpWordSingleQuotesOnly(pe.Exp.Word) || paramExpWordHasBackslashLit(pe.Exp.Word)):
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
		starAggregateNull := false
		if name == "*" {
			starAggregateNull = len(vr.List) > 0
			for _, elem := range vr.List {
				if elem != "" {
					starAggregateNull = false
					break
				}
			}
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
			if str == "" || starAggregateNull {
				str = arg
			}
		case syntax.ErrorUnset:
			if vr.IsSet() {
				break
			}
			if arg == "" {
				arg = "parameter not set"
			}
			fallthrough
		case syntax.ErrorUnsetOrNull:
			if str == "" {
				if arg == "" {
					arg = "parameter null or not set"
				}
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
				if cannotAssignParam(name) {
					return "", fmt.Errorf("$%s: cannot assign in this way", name)
				}
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
				out[i] = cfg.removePattern(elem, arg, suffix, small)
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
				quoted := bashSingleQuote(str)
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
		for _, i := range vr.IndexedIndexes() {
			v := vr.List[i]
			quoted, err := syntax.Quote(v, syntax.LangBash)
			if err != nil {
				quoted = v
			}
			parts = append(parts, strconv.Itoa(i)+" "+quoted)
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

func (cfg *Config) removePattern(str, pat string, fromEnd, shortest bool) string {
	mode := pattern.EntireString
	if cfg.ExtGlob {
		mode |= pattern.ExtendedOperators
	}
	match := func(s string) bool {
		return false
	}
	splitPoints := removePatternSplitPoints
	if !utf8.ValidString(pat) {
		match = func(s string) bool {
			return internal.BytePatternMatch([]byte(pat), []byte(s))
		}
		splitPoints = removePatternByteSplitPoints
	} else {
		matcher, err := internal.ExtendedPatternMatcher(pat, mode)
		if err != nil {
			return str
		}
		match = func(s string) bool {
			return matcher(s)
		}
	}
	if !fromEnd {
		if shortest {
			for _, i := range splitPoints(str) {
				if match(str[:i]) {
					return str[i:]
				}
			}
		} else {
			for _, i := range slices.Backward(splitPoints(str)) {
				if match(str[:i]) {
					return str[i:]
				}
			}
		}
		return str
	}

	if shortest {
		for _, i := range slices.Backward(splitPoints(str)) {
			if match(str[i:]) {
				return str[:i]
			}
		}
	} else {
		for _, i := range splitPoints(str) {
			if match(str[i:]) {
				return str[:i]
			}
		}
	}
	return str
}

func removePatternSplitPoints(s string) []int {
	points := make([]int, 0, utf8.RuneCountInString(s)+1)
	points = append(points, 0)
	for i := range s {
		if i > 0 {
			points = append(points, i)
		}
	}
	points = append(points, len(s))
	return points
}

func removePatternByteSplitPoints(s string) []int {
	points := make([]int, len(s)+1)
	for i := range points {
		points[i] = i
	}
	return points
}

func (cfg *Config) varInd(vr Variable, idx syntax.ArithmExpr) (string, error) {
	if idx == nil {
		if vr.Kind == Associative {
			return vr.Map["0"], nil
		}
		return vr.String(), nil
	}
	switch vr.Kind {
	case String:
		switch nodeLit(idx) {
		case "*", "@":
			return vr.Str, nil
		}
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
			return strings.Join(vr.IndexedValues(), " "), nil
		}
		if text, ok := singleQuotedWhitespaceIndex(idx); ok {
			return "", &ArithmError{
				Text: "'" + text + "'",
				Err:  fmt.Errorf("arithmetic syntax error: operand expected (error token is %q)", "'"+text+"'"),
			}
		}
		if start, end, ok := indexedBraceRange(idx); ok {
			var vals []string
			step := 1
			if start > end {
				step = -1
			}
			for i := start; ; i += step {
				if vr.IndexedSet(i) {
					vals = append(vals, vr.List[i])
				}
				if i == end {
					break
				}
			}
			return strings.Join(vals, " "), nil
		}
		i, err := Arithm(cfg, indexedQuotedLiteralIndex(idx))
		if err != nil {
			return "", err
		}
		if i < 0 {
			orig := i
			i = indexedNegativeOffset(vr, i)
			if i < 0 && cfg.curParam.Length {
				idxText := subscriptText(idx)
				if idxText == "" {
					idxText = strconv.Itoa(orig)
				}
				return "", fmt.Errorf("[%s]: bad array subscript", idxText)
			}
			if i < 0 {
				name := ""
				if cfg.curParam.Param != nil {
					name = cfg.curParam.Param.Value
				}
				return "", fmt.Errorf("%s: bad array subscript", name)
			}
		}
		if vr.IndexedSet(i) {
			return vr.List[i], nil
		}
		if cfg.NoUnset {
			idxText := nodeLit(idx)
			if idxText == "" {
				idxText = strconv.Itoa(i)
			}
			return "", UnsetParameterError{
				Name:    fmt.Sprintf("%s[%s]", cfg.curParam.Param.Value, idxText),
				Message: "unbound variable",
			}
		}
	case Associative:
		switch lit := nodeLit(idx); lit {
		case "@", "*":
			keys := AssocKeysInBashOrder(vr.Map)
			strs := make([]string, len(keys))
			for i, k := range keys {
				strs[i] = vr.Map[k]
			}
			if lit == "*" {
				return cfg.ifsJoin(strs), nil
			}
			return strings.Join(strs, " "), nil
		}
		val, err := Literal(cfg, idx.(*syntax.Word))
		if err != nil {
			return "", err
		}
		if val == "" {
			return "", fmt.Errorf("[%s]: bad array subscript", subscriptText(idx))
		}
		return vr.Map[val], nil
	}
	return "", nil
}

func singleQuotedWhitespaceIndex(idx syntax.ArithmExpr) (string, bool) {
	word, ok := idx.(*syntax.Word)
	if !ok || len(word.Parts) != 1 {
		return "", false
	}
	sq, ok := word.Parts[0].(*syntax.SglQuoted)
	if !ok || strings.TrimSpace(sq.Value) != "" {
		return "", false
	}
	return sq.Value, true
}

func indexedBraceRange(idx syntax.ArithmExpr) (int, int, bool) {
	word, ok := idx.(*syntax.Word)
	if !ok || len(word.Parts) != 1 {
		return 0, 0, false
	}
	dq, ok := word.Parts[0].(*syntax.DblQuoted)
	if !ok || len(dq.Parts) != 1 {
		return 0, 0, false
	}
	lit, ok := dq.Parts[0].(*syntax.Lit)
	if !ok || !strings.HasPrefix(lit.Value, "{") || !strings.HasSuffix(lit.Value, "}") {
		return 0, 0, false
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(lit.Value, "{"), "}")
	parts := strings.Split(inner, "..")
	if len(parts) != 2 {
		return 0, 0, false
	}
	start, err1 := strconv.Atoi(parts[0])
	end, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return start, end, true
}

func indexedQuotedLiteralIndex(idx syntax.ArithmExpr) syntax.ArithmExpr {
	word, ok := idx.(*syntax.Word)
	if !ok || len(word.Parts) != 1 {
		return idx
	}
	dq, ok := word.Parts[0].(*syntax.DblQuoted)
	if !ok || len(dq.Parts) != 1 {
		return idx
	}
	lit, ok := dq.Parts[0].(*syntax.Lit)
	if !ok {
		return idx
	}
	return &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: strings.TrimSpace(lit.Value)}}}
}

func subscriptText(idx syntax.ArithmExpr) string {
	if lit := nodeLit(idx); lit != "" {
		return lit
	}
	var b strings.Builder
	syntax.NewPrinter().Print(&b, idx)
	return b.String()
}

func nameRefArrayTarget(s string) (name, idx string, ok bool) {
	lb := strings.IndexByte(s, '[')
	if lb <= 0 || !strings.HasSuffix(s, "]") {
		return "", "", false
	}
	name = s[:lb]
	if !syntax.ValidName(name) {
		return "", "", false
	}
	return name, s[lb+1 : len(s)-1], true
}

func (cfg *Config) namesByPrefix(prefix string) []string {
	var names []string
	for name := range cfg.Env.Each {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
