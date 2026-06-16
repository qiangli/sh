// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"fmt"
	"io"
	"maps"
	"path/filepath"
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

func indirectAtOp(op syntax.ParExpOperator) bool {
	return op == syntax.OtherParamOps
}

func transformNounsetUnset(vr Variable) bool {
	return !vr.IsSet() || vr.Kind == Indexed && vr.IndexedCount() == 0
}

func indirectParamModOp(op syntax.ParExpOperator) bool {
	switch op {
	case syntax.RemSmallPrefix, syntax.RemLargePrefix,
		syntax.RemSmallSuffix, syntax.RemLargeSuffix:
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

// arrayElemSet reports whether a specific array element is set.
// For indexed arrays, it checks the ListSet map. For associative arrays,
// it checks if the key exists in the Map. Returns true for scalar variables.
func arrayElemSet(vr Variable, idx syntax.ArithmExpr, cfg *Config) bool {
	if idx == nil {
		return vr.IsSet()
	}
	switch vr.Kind {
	case Associative:
		key, err := Literal(cfg, idx.(*syntax.Word))
		if err != nil {
			return false
		}
		_, ok := vr.Map[key]
		return ok
	case Indexed:
		i, err := Arithm(cfg, idx)
		if err != nil {
			return false
		}
		if i < 0 {
			// Handle negative index
			indexes := vr.IndexedIndexes()
			if len(indexes) == 0 {
				return false
			}
			i = indexes[len(indexes)-1] + 1 + i
			if i < 0 {
				return false
			}
		}
		return vr.IndexedSet(i)
	default:
		return vr.IsSet()
	}
}

// envSetIndex sets a variable value, handling array element assignment when
// idx is not nil. Used by parameter expansion assignment operators like ${a[i]=value}.
func (cfg *Config) envSetIndex(name string, idx syntax.ArithmExpr, value string) error {
	wenv, ok := cfg.Env.(WriteEnviron)
	if !ok {
		return fmt.Errorf("environment is read-only")
	}
	if idx == nil {
		return cfg.envSet(name, value)
	}

	vr := cfg.Env.Get(name)

	// Check for special indices
	switch nodeLit(idx) {
	case "@", "*":
		return fmt.Errorf("%s: cannot assign in this way", name)
	}

	if vr.Kind == Associative {
		// Associative array: evaluate index as string
		key, err := Literal(cfg, idx.(*syntax.Word))
		if err != nil {
			return err
		}
		if vr.Map == nil {
			vr.Map = make(map[string]string)
		} else {
			vr.Map = maps.Clone(vr.Map)
		}
		vr.Set = true
		vr.Kind = Associative
		vr.Map[key] = value
		return wenv.Set(name, vr)
	}

	// Indexed array: evaluate index as arithmetic
	i, err := Arithm(cfg, idx)
	if err != nil {
		return err
	}
	if i < 0 {
		// Handle negative index - convert to positive offset from end
		indexes := vr.IndexedIndexes()
		if len(indexes) == 0 {
			i = 0
		} else {
			i = indexes[len(indexes)-1] + 1 + i
			if i < 0 {
				return fmt.Errorf("%s: bad array subscript", name)
			}
		}
	}

	// Convert string to indexed array if needed
	if vr.Kind == String && vr.Str != "" {
		vr.List = []string{vr.Str}
		vr.ListSet = nil
	}

	vr.Kind = Indexed
	vr.Set = true
	vr.List = slices.Clone(vr.List)
	listSet := vr.CloneListSet()

	// Grow array if needed
	if i >= len(vr.List) {
		if listSet == nil {
			listSet = vr.DenseListSet()
		}
		vr.List = append(vr.List, make([]string, i-len(vr.List)+1)...)
	}

	vr.List[i] = value
	if listSet != nil {
		listSet[i] = true
		vr.ListSet = listSet
	}

	return wenv.Set(name, vr)
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

// applyParamMods applies the trailing modifier portion of a parameter
// expansion (Slice, Repl, Exp) to a precomputed string value. Used
// by the indirect-expansion path (`${!x//c/x}`) where the value
// comes from a name lookup but the substitution should still apply.
func applyParamMods(cfg *Config, pe *syntax.ParamExp, str string) (string, error) {
	out, err := applyParamModsElems(cfg, pe, []string{str})
	if err != nil {
		return "", err
	}
	if len(out) == 0 {
		return "", nil
	}
	return out[0], nil
}

func applyParamModsElems(cfg *Config, pe *syntax.ParamExp, elems []string) ([]string, error) {
	if pe.Repl == nil && (pe.Exp == nil || !indirectParamModOp(pe.Exp.Op)) {
		return elems, nil
	}
	out := slices.Clone(elems)
	if pe.Exp != nil && indirectParamModOp(pe.Exp.Op) {
		arg, err := Pattern(cfg, pe.Exp.Word)
		if err != nil {
			return nil, err
		}
		suffix := pe.Exp.Op == syntax.RemSmallSuffix || pe.Exp.Op == syntax.RemLargeSuffix
		small := pe.Exp.Op == syntax.RemSmallPrefix || pe.Exp.Op == syntax.RemSmallSuffix
		for i, elem := range out {
			out[i] = cfg.removePattern(elem, arg, suffix, small)
		}
	}
	if pe.Repl == nil {
		return out, nil
	}
	orig, replAnchoredStart, replAnchoredEnd, err := replPattern(cfg, pe.Repl.Orig, pe.Repl.All)
	if err != nil {
		return nil, err
	}
	if orig == "" && !replAnchoredStart && !replAnchoredEnd {
		return out, nil
	}
	segs, err := cfg.replTemplate(pe.Repl.With)
	if err != nil {
		return nil, err
	}
	matchSpecial := replHasMatch(segs)
	with := renderRepl(segs, "")
	n := 1
	if pe.Repl.All {
		n = -1
	}
	for i, elem := range out {
		locs := cfg.findReplIndex(orig, elem, n, replAnchoredStart, replAnchoredEnd)
		var sb strings.Builder
		last := 0
		for _, loc := range locs {
			sb.WriteString(elem[last:loc[0]])
			if matchSpecial {
				sb.WriteString(renderRepl(segs, elem[loc[0]:loc[1]]))
			} else {
				sb.WriteString(with)
			}
			last = loc[1]
		}
		sb.WriteString(elem[last:])
		out[i] = sb.String()
	}
	return out, nil
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
	if cfg.NoCaseMatch {
		mode |= pattern.NoGlobCase
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

// replSegment is one piece of a ${var/pat/repl} replacement template. When
// match is true the matched portion of the value is substituted here;
// otherwise lit is emitted verbatim.
type replSegment struct {
	lit   string
	match bool
}

// replTemplate builds the replacement template for the With word of a
// substitution. Backslash escapes and quoting are resolved up front; when
// patsub_replacement is enabled an unquoted `&` becomes a match placeholder.
// Tilde expansion is applied to a leading unquoted `~`.
func (cfg *Config) replTemplate(with *syntax.Word) ([]replSegment, error) {
	var segs []replSegment
	if with == nil {
		return segs, nil
	}
	emitLit := func(s string) {
		if s == "" {
			return
		}
		if n := len(segs); n > 0 && !segs[n-1].match {
			segs[n-1].lit += s
			return
		}
		segs = append(segs, replSegment{lit: s})
	}
	patsub := cfg.PatSubReplacement
	// scanUnquoted processes source/expanded text that is subject to
	// patsub `&` substitution and backslash escaping.
	scanUnquoted := func(s string, fromSource bool) {
		for i := 0; i < len(s); i++ {
			c := s[i]
			if c == '\\' && i+1 < len(s) {
				next := s[i+1]
				// In bare source text a backslash escapes any
				// character. In expanded variable content only `\&`
				// and `\\` are special; other backslashes are kept.
				if fromSource || next == '&' || next == '\\' {
					emitLit(string(next))
					i++
					continue
				}
				emitLit("\\")
				continue
			}
			if c == '&' && patsub {
				segs = append(segs, replSegment{match: true})
				continue
			}
			emitLit(string(c))
		}
	}
	for idx, part := range with.Parts {
		switch part := part.(type) {
		case *syntax.Lit:
			val := part.Value
			if idx == 0 {
				if prefix, rest := cfg.expandUser(val, len(with.Parts) > 1); prefix != "" {
					emitLit(prefix)
					val = rest
				}
			}
			scanUnquoted(val, true)
		case *syntax.ParamExp, *syntax.CmdSubst, *syntax.ArithmExp, *syntax.ProcSubst, *syntax.ExtGlob:
			s, err := Literal(cfg, &syntax.Word{Parts: []syntax.WordPart{part}})
			if err != nil {
				return nil, err
			}
			scanUnquoted(s, false)
		default:
			// Quoted parts (SglQuoted, DblQuoted): fully literal, no
			// `&` substitution or backslash processing here.
			s, err := Literal(cfg, &syntax.Word{Parts: []syntax.WordPart{part}})
			if err != nil {
				return nil, err
			}
			emitLit(s)
		}
	}
	return segs, nil
}

// renderRepl renders a replacement template, substituting matched for each
// match placeholder.
func renderRepl(segs []replSegment, matched string) string {
	if len(segs) == 0 {
		return ""
	}
	if len(segs) == 1 && !segs[0].match {
		return segs[0].lit
	}
	var sb strings.Builder
	for _, s := range segs {
		if s.match {
			sb.WriteString(matched)
		} else {
			sb.WriteString(s.lit)
		}
	}
	return sb.String()
}

// replHasMatch reports whether any segment substitutes the matched text.
func replHasMatch(segs []replSegment) bool {
	for _, s := range segs {
		if s.match {
			return true
		}
	}
	return false
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
	hadIndex := index != nil
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
			index = nameRefArrayTargetIndex(idx)
		}
	}
	if n, v, circular := vr.ResolveTracked(cfg.Env); n != "" {
		name, vr = n, v
		if circular && cfg.OnNameRefCircular != nil {
			cfg.OnNameRefCircular(pe.Param.Value)
		}
	}
	if cfg.NoUnset && !vr.IsSet() && !overridingUnset(pe) &&
		!(orig.Kind == NameRef && index != nil && vr.Kind == Indexed) &&
		nodeLit(index) != "@" && nodeLit(index) != "*" {
		if index != nil {
			idxText := nodeLit(index)
			if vr.Kind != Associative {
				i, err := Arithm(cfg, index)
				if err != nil {
					return "", err
				}
				if idxText == "" {
					idxText = strconv.Itoa(i)
				}
			}
			if idxText == "" && vr.Kind == Indexed {
				if i, err := Arithm(cfg, index); err == nil {
					idxText = strconv.Itoa(i)
				}
			}
			if idxText != "" {
				errName := fmt.Sprintf("%s[%s]", name, idxText)
				if orig.Kind == NameRef && !hadIndex {
					errName = pe.Param.Value
				}
				return "", UnsetParameterError{
					Name:    errName,
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
			// Bash iterates assoc-array values in its hash-table
			// order, the same order `declare -p` prints (honoring the
			// variable's bucket count via AssocKeysForDeclare).
			keys := vr.AssocKeysForDeclare()
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
		if pe.Exp != nil && !indirectDefaultOp(pe.Exp.Op) && !indirectAtOp(pe.Exp.Op) && !indirectParamModOp(pe.Exp.Op) {
			return "", BadSubstitutionError{Node: pe}
		}
		var strs []string
		indirectName := name
		indirectOrig := vr
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
			if pe.Exp != nil && indirectAtOp(pe.Exp.Op) {
				return "", fmt.Errorf("%s: invalid variable name", strings.Join(vr.IndexedValues(), " "))
			}
			for _, i := range vr.IndexedIndexes() {
				strs = append(strs, strconv.Itoa(i))
			}
		case pe.Index != nil && vr.Kind == Associative:
			if pe.Exp != nil && indirectAtOp(pe.Exp.Op) {
				keys := vr.AssocKeysForDeclare()
				vals := make([]string, len(keys))
				for i, key := range keys {
					vals[i] = vr.Map[key]
				}
				return "", fmt.Errorf("%s: invalid variable name", strings.Join(vals, " "))
			}
			strs = vr.AssocKeysForDeclare()
		case orig.Kind == NameRef && pe.Index != nil && nodeLit(pe.Index) != "@" && nodeLit(pe.Index) != "*":
			if vr.IsSet() {
				strs = append(strs, "")
			} else {
				return "", IndirectExpansionError{Name: fmt.Sprintf("%s[%s]", pe.Param.Value, nodeLit(pe.Index))}
			}
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
				indirectName = base
				indirectOrig = vr
				switch vr.Kind {
				case Indexed:
					switch nodeLit(idx) {
					case "@", "*":
						strs = append(strs, vr.IndexedValues()...)
						indexAllElements = true
					default:
						val, err := cfg.varInd(vr, idx)
						if err != nil {
							return "", err
						}
						strs = append(strs, val)
					}
				case Associative:
					switch nodeLit(idx) {
					case "@", "*":
						keys := vr.AssocKeysForDeclare()
						for _, key := range keys {
							strs = append(strs, vr.Map[key])
						}
						indexAllElements = true
					default:
						val, err := cfg.varInd(vr, idx)
						if err != nil {
							return "", err
						}
						strs = append(strs, val)
					}
				default:
					val, err := cfg.varInd(vr, idx)
					if err != nil {
						return "", err
					}
					strs = append(strs, val)
				}
				applyMod = true
				break
			}
			indirectName = str
			vr = cfg.Env.Get(str)
			indirectOrig = vr
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
		elems = strs
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
		if pe.Exp != nil && indirectAtOp(pe.Exp.Op) {
			if pe.Exp.Word == nil || len(pe.Exp.Word.Parts) != 1 {
				return "", BadSubstitutionError{Node: pe}
			}
			lit, ok := pe.Exp.Word.Parts[0].(*syntax.Lit)
			if !ok {
				return "", BadSubstitutionError{Node: pe}
			}
			switch lit.Value {
			case "Q":
				out := make([]string, len(elems))
				for i, elem := range elems {
					quoted, qerr := syntax.Quote(elem, syntax.LangBash)
					if qerr != nil {
						panic(qerr)
					}
					if quoted == elem {
						quoted = bashSingleQuote(elem)
					}
					out[i] = quoted
				}
				str = strings.Join(out, " ")
			case "a":
				if cfg.NoUnset && transformNounsetUnset(indirectOrig) {
					return "", UnsetParameterError{Name: "!" + name, Message: "unbound variable"}
				}
				str = indirectOrig.Flags()
			case "A":
				if cfg.NoUnset && transformNounsetUnset(indirectOrig) {
					return "", UnsetParameterError{Name: "!" + name, Message: "unbound variable"}
				}
				str = cfg.paramAtA(indirectOrig, indirectOrig, indirectName, str, indexAllElements)
			default:
				return "", BadSubstitutionError{Node: pe}
			}
		}
		if applyMod && (pe.Repl != nil || pe.Exp != nil && indirectParamModOp(pe.Exp.Op)) {
			if modElems, err := applyParamModsElems(cfg, pe, elems); err != nil {
				return "", err
			} else {
				elems = modElems
				str = strings.Join(elems, " ")
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
		// character (`\'` → `'`, `\\` → `\`, `\&` → `&`). With the
		// patsub_replacement option an unquoted `&` is replaced by
		// the matched text. [Config.replTemplate] resolves all of
		// this into a template rendered per match.
		segs, terr := cfg.replTemplate(pe.Repl.With)
		if terr != nil {
			return "", terr
		}
		matchSpecial := replHasMatch(segs)
		with := renderRepl(segs, "")
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
				if matchSpecial {
					sb.WriteString(renderRepl(segs, elem[loc[0]:loc[1]]))
				} else {
					sb.WriteString(with)
				}
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
			if arrayElemSet(vr, index, cfg) {
				break
			}
			fallthrough
		case syntax.AssignUnsetOrNull:
			if str == "" {
				if cannotAssignParam(name) {
					return "", fmt.Errorf("$%s: cannot assign in this way", name)
				}
				if err := cfg.envSetIndex(name, index, arg); err != nil {
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
			if pe.Exp.Word == nil || len(pe.Exp.Word.Parts) != 1 {
				return "", BadSubstitutionError{Node: pe}
			}
			if _, ok := pe.Exp.Word.Parts[0].(*syntax.Lit); !ok {
				return "", BadSubstitutionError{Node: pe}
			}
			switch arg {
			case "Q":
				// Bash 5.3's @Q on an unset variable (or unset array
				// element) expands to nothing, rather than quoting an
				// empty value to `''`. `@`/`*` and `[@]`/`[*]` forms are
				// "set" whenever any element exists, so they use IsSet
				// directly; a specific subscript checks that element.
				qSet := vr.IsSet()
				if name != "@" && name != "*" {
					if il := nodeLit(index); il != "@" && il != "*" {
						qSet = arrayElemSet(vr, index, cfg)
					}
				}
				if !qSet {
					str = ""
					break
				}
				// Bash 5.3 quotes each element of an array transform
				// separately (`${arr[@]@Q}` -> `'a' 'b'`).
				out := make([]string, len(elems))
				for i, elem := range elems {
					out[i] = bashQuoteParamQ(elem)
				}
				str = strings.Join(out, " ")
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
				if cfg.NoUnset && transformNounsetUnset(orig) {
					return "", UnsetParameterError{Name: name, Message: "unbound variable"}
				}
				// ${var@a} returns variable attribute flags.
				// We use orig (before nameref resolve) for the attributes.
				str = orig.Flags()
			case "A":
				if cfg.NoUnset && transformNounsetUnset(orig) {
					return "", UnsetParameterError{Name: name, Message: "unbound variable"}
				}
				// ${var@A} returns a declare statement that recreates the
				// variable. A variable that was never declared expands to
				// nothing (a declared-but-unset variable still prints its
				// `declare` line).
				if !vr.Declared() && name != "@" && name != "*" {
					str = ""
					break
				}
				// ${var@A} returns a declare statement that recreates the variable.
				if name == "@" || name == "*" {
					// Positional parameters reproduce as `set -- ...`.
					out := make([]string, len(elems))
					for i, elem := range elems {
						out[i] = bashSingleQuote(elem)
					}
					str = "set -- " + strings.Join(out, " ")
				} else {
					str = cfg.paramAtA(vr, orig, name, str, indexAllElements)
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
				return "", BadSubstitutionError{Node: pe}
			}
		}
	}
	return str, nil
}

// paramAtK implements ${var@K} and ${var@k}, producing quoted key-value pairs
// for arrays. For indexed arrays: "0 val0 1 val1 ...".
// For associative arrays: "key1 val1 key2 val2 ...".
// For plain strings, returns the value unchanged.
// paramAtK implements the string form of ${var@K} and ${var@k}. A scalar
// becomes a single-quoted value (`'string'`); positional parameters become
// the single-quoted values without keys (`'a b' 'c d'`); indexed and
// associative arrays become a sequence of `key "value"` pairs with the
// value in double quotes, in bash key order.
func (cfg *Config) paramAtK(vr Variable, name string) string {
	if name == "@" || name == "*" {
		// Positional parameters: quoted values, no keys.
		var parts []string
		for _, v := range vr.List {
			parts = append(parts, bashQuoteParamQ(v))
		}
		return strings.Join(parts, " ")
	}
	switch vr.Kind {
	case Indexed:
		var parts []string
		for _, i := range vr.IndexedIndexes() {
			parts = append(parts, strconv.Itoa(i)+" "+bashDeclareQuote(vr.List[i]))
		}
		return strings.Join(parts, " ")
	case Associative:
		var parts []string
		for _, k := range vr.AssocKeysForDeclare() {
			parts = append(parts, bashDeclareQuote(k)+" "+bashDeclareQuote(vr.Map[k]))
		}
		return strings.Join(parts, " ")
	default:
		return bashQuoteParamQ(vr.String())
	}
}

// paramAtKFields implements the field-splitting form of "${arr[@]@k}":
// each key and each (unquoted) value becomes a separate field.
func (cfg *Config) paramAtKFields(vr Variable, name string) []string {
	if name == "@" || name == "*" {
		return append([]string(nil), vr.List...)
	}
	switch vr.Kind {
	case Indexed:
		var out []string
		for _, i := range vr.IndexedIndexes() {
			out = append(out, strconv.Itoa(i), vr.List[i])
		}
		return out
	case Associative:
		var out []string
		for _, k := range vr.AssocKeysForDeclare() {
			out = append(out, k, vr.Map[k])
		}
		return out
	default:
		return []string{vr.String()}
	}
}

// paramAtA implements ${var@A}: a reusable declaration that recreates the
// variable. For arrays bash emits the full `declare -a NAME=([i]="v" ...)` /
// `declare -A NAME=([k]="v" ...)` form (double-quoted element values, in
// bash's hash-bucket key order); for scalars it emits the single-quoted
// `[declare -flags ]NAME='value'` form. `scalarStr` is the already-expanded
// scalar value, and `orig` carries the scalar's attribute flags.
func (cfg *Config) paramAtA(vr, orig Variable, name, scalarStr string, forceLiteral bool) string {
	switch vr.Kind {
	case Indexed, Associative:
		return declareArray(name, vr, forceLiteral)
	default:
		flags := orig.Flags()
		if !orig.IsSet() {
			// Declared but unset: bash prints just the attributes.
			if flags == "" {
				return name + "="
			}
			return "declare -" + flags + " " + name
		}
		quoted := bashSingleQuote(scalarStr)
		if flags == "" {
			return name + "=" + quoted
		}
		return "declare -" + flags + " " + name + "=" + quoted
	}
}

// declareArray formats an indexed or associative array as bash's ${arr@A}
// (and `declare -p`) reusable assignment.
//
// forceLiteral is true when the expansion used an explicit `[@]` / `[*]`
// subscript (`${arr[@]@A}`): bash then always emits the array literal for a
// *set* array, so an empty one yields `...NAME=()`. Without the subscript
// (`${arr@A}`) an array with no elements is reproduced as just the
// declaration (`declare -flags NAME`), matching bash treating an empty array
// as having no value. An unset-but-declared array never gets a value.
func declareArray(name string, vr Variable, forceLiteral bool) string {
	flags := vr.Flags()
	if flags == "" {
		flags = "-"
	}
	var b strings.Builder
	b.WriteString("declare -")
	b.WriteString(flags)
	b.WriteByte(' ')
	b.WriteString(name)
	switch vr.Kind {
	case Indexed:
		if forceLiteral {
			if !vr.IsSet() {
				return b.String()
			}
		} else if len(vr.IndexedIndexes()) == 0 {
			return b.String()
		}
		b.WriteString("=(")
		first := true
		for _, i := range vr.IndexedIndexes() {
			if !first {
				b.WriteByte(' ')
			}
			first = false
			fmt.Fprintf(&b, "[%d]=%s", i, bashDeclareQuote(vr.List[i]))
		}
		b.WriteByte(')')
	case Associative:
		if forceLiteral {
			if !vr.IsSet() {
				return b.String()
			}
		} else if len(vr.Map) == 0 {
			return b.String()
		}
		b.WriteString("=(")
		first := true
		for _, k := range vr.AssocKeysForDeclare() {
			if !first {
				b.WriteByte(' ')
			}
			first = false
			fmt.Fprintf(&b, "[%s]=%s", bashAssocKeyQuote(k), bashDeclareQuote(vr.Map[k]))
		}
		// Bash leaves a trailing space before `)` for associative arrays.
		if !first {
			b.WriteByte(' ')
		}
		b.WriteByte(')')
	}
	return b.String()
}

// bashDeclareQuote formats v the way bash's declare -p / ${arr@A} array
// literals do: double-quoted, escaping ", \, $ and `, falling back to ANSI-C
// $'...' for non-printable bytes. (Mirrors the interp helper of the same name;
// kept here so the expand layer needn't import interp.)
func bashDeclareQuote(v string) string {
	for i := 0; i < len(v); i++ {
		if c := v[i]; c < 0x20 || c == 0x7f {
			if q, err := syntax.Quote(v, syntax.LangBash); err == nil {
				return q
			}
			break
		}
	}
	var b strings.Builder
	b.Grow(len(v) + 2)
	b.WriteByte('"')
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == '"' || c == '\\' || c == '$' || c == '`' {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteByte('"')
	return b.String()
}

// bashAssocKeyQuote quotes an associative-array key for declare -p / ${arr@A}:
// bare when it contains only "safe" characters, double-quoted otherwise.
func bashAssocKeyQuote(s string) string {
	if s == "" {
		return bashDeclareQuote(s)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z',
			'A' <= c && c <= 'Z',
			'0' <= c && c <= '9',
			c == '_', c == '.', c == '%', c == '-':
			continue
		default:
			return bashDeclareQuote(s)
		}
	}
	return s
}

// expandPrompt implements ${var@P} by delegating to [Config.PromptExpand].
// If PromptExpand is not set, it performs basic prompt escape expansion.
func (cfg *Config) expandPrompt(s string) string {
	return cfg.defaultPromptExpand(s)
}

// defaultPromptExpand handles a subset of Bash prompt escape sequences.
func (cfg *Config) defaultPromptExpand(s string) string {
	s = cfg.expandPromptVars(s)
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '!' {
			b.WriteByte('1')
			continue
		}
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
		case 'h', 'H':
			host := cfg.envGet("HOSTNAME")
			if host == "" {
				host = cfg.envGet("HOST")
			}
			if s[i] == 'h' {
				if dot := strings.IndexByte(host, '.'); dot >= 0 {
					host = host[:dot]
				}
			}
			b.WriteString(host)
		case 'j':
			b.WriteByte('0')
		case 'e':
			b.WriteByte('\x1b')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 's':
			b.WriteString("bash")
		case 'v':
			b.WriteString("5.3")
		case 'V':
			b.WriteString("5.3.0")
		case 'w':
			b.WriteString(cfg.promptWorkingDir(false))
		case 'W':
			b.WriteString(cfg.promptWorkingDir(true))
		case '!':
			b.WriteByte('1')
		case '#':
			b.WriteByte('0')
		case '$':
			b.WriteByte('$')
		case '\\':
			b.WriteByte('\\')
		case '[':
			end := promptMarkerEnd(s, i+1)
			if end < 0 {
				break
			}
			inner := cfg.defaultPromptExpand(s[i+1 : end])
			switch inner {
			case "":
			case "\001":
				b.WriteByte('\001')
			default:
				b.WriteByte('\001')
				b.WriteString(inner)
				b.WriteByte('\002')
			}
			i = end + 1
		case ']':
		case '0', '1', '2', '3', '4', '5', '6', '7':
			end := i + 1
			for end < len(s) && end < i+3 && s[end] >= '0' && s[end] <= '7' {
				end++
			}
			val, _ := strconv.ParseUint(s[i:end], 8, 8)
			b.WriteByte(byte(val))
			i = end - 1
		default:
			// For unrecognized sequences, preserve them.
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func promptMarkerEnd(s string, start int) int {
	for i := start; i+1 < len(s); i++ {
		if s[i] == '\\' && s[i+1] == ']' {
			return i
		}
	}
	return -1
}

func (cfg *Config) expandPromptVars(s string) string {
	if !strings.ContainsAny(s, "$`") {
		return s
	}
	word, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Document(strings.NewReader(s))
	if err != nil && err != io.EOF {
		return s
	}
	out, err := Literal(cfg, word)
	if err != nil {
		return s
	}
	return out
}

func (cfg *Config) promptWorkingDir(base bool) string {
	pwd := cfg.envGet("PWD")
	if pwd == "" {
		pwd = "."
	}
	home := cfg.envGet("HOME")
	if home != "" && (pwd == home || strings.HasPrefix(pwd, home+"/")) {
		pwd = "~" + strings.TrimPrefix(pwd, home)
	}
	if !base {
		return pwd
	}
	switch pwd {
	case "", "/":
		return "/"
	case "~":
		return "~"
	}
	return filepath.Base(pwd)
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
			errName := cfg.curParam.Param.Value
			if cfg.curParam.Index != nil {
				errName = fmt.Sprintf("%s[%s]", errName, idxText)
			}
			return "", UnsetParameterError{
				Name:    errName,
				Message: "unbound variable",
			}
		}
	case Associative:
		switch lit := nodeLit(idx); lit {
		case "@", "*":
			keys := vr.AssocKeysForDeclare()
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

func nameRefArrayTargetIndex(idx string) syntax.ArithmExpr {
	src := "x[" + idx + "]=_"
	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	if file, err := parser.Parse(strings.NewReader(src), ""); err == nil && len(file.Stmts) == 1 {
		if call, ok := file.Stmts[0].Cmd.(*syntax.CallExpr); ok && len(call.Assigns) == 1 {
			if call.Assigns[0].Index != nil {
				return call.Assigns[0].Index
			}
		}
	}
	return &syntax.Word{Parts: []syntax.WordPart{
		&syntax.Lit{Value: idx},
	}}
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
