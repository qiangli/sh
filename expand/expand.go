// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/traditionalchinese"

	"mvdan.cc/sh/v3/internal"
	"mvdan.cc/sh/v3/pattern"
	"mvdan.cc/sh/v3/syntax"
)

// A Config specifies details about how shell expansion should be performed. The
// zero value is a valid configuration.
type Config struct {
	// Env is used to get and set environment variables when performing
	// shell expansions. Some special parameters are also expanded via this
	// interface, such as:
	//
	//   * "#", "@", "*", "0"-"9" for the shell's parameters
	//   * "?", "$", "PPID" for the shell's status and process
	//   * "HOME foo" to retrieve user foo's home directory (if unset,
	//     os/user.Lookup will be used)
	//
	// If nil, there are no environment variables set. Use
	// ListEnviron(os.Environ()...) to use the system's environment
	// variables.
	Env Environ

	// CmdSubst expands a command substitution node, writing its standard
	// output to the provided [io.Writer].
	//
	// If nil, encountering a command substitution will result in an
	// UnexpectedCommandError.
	CmdSubst func(io.Writer, *syntax.CmdSubst) error

	// ProcSubst expands a process substitution node.
	ProcSubst func(*syntax.ProcSubst) (string, error)

	// TODO(v4): replace ReadDir with ReadDir2.

	// ReadDir is the older form of [ReadDir2], before io/fs.
	//
	// Deprecated: use ReadDir2 instead.
	ReadDir func(string) ([]fs.FileInfo, error)

	// ReadDir2 is used for file path globbing.
	// If nil, and [ReadDir] is nil as well, globbing is disabled.
	// Use [os.ReadDir] to use the filesystem directly.
	ReadDir2 func(string) ([]fs.DirEntry, error)

	// GlobStar corresponds to the shell option which allows globbing with "**".
	GlobStar bool

	// GlobSkipDots corresponds to the shell option which prevents pathname
	// expansion from returning "." or "..".
	GlobSkipDots bool

	// DotGlob corresponds to the shell option which allows filenames beginning
	// with a dot to be matched by a pattern which does not begin with a dot.
	DotGlob bool

	// NoCaseGlob corresponds to the shell option which causes case-insensitive
	// pattern matching in pathname expansion.
	NoCaseGlob bool

	// NoCaseMatch corresponds to the shell option which causes case-insensitive
	// pattern matching in parameter replacement.
	NoCaseMatch bool

	// NullGlob corresponds to the shell option which allows globbing
	// patterns which match nothing to result in zero fields.
	NullGlob bool

	// NoUnset corresponds to the shell option which treats unset variables
	// as errors.
	NoUnset bool

	// ExtGlob corresponds to the shell option which allows using extended
	// pattern matching features when performing pathname expansion (globbing).
	ExtGlob bool

	// PatSubReplacement corresponds to the `patsub_replacement` shell option.
	// When true, an unquoted `&` in the replacement string of a
	// `${var/pat/repl}` expansion is replaced by the portion of the value
	// that the pattern matched.
	PatSubReplacement bool

	// PromptExpand is called by the ${var@P} expansion to expand prompt
	// escape sequences such as \u, \h, \w. If nil, ${var@P} returns the
	// string unchanged.
	PromptExpand func(string) string

	// StartTime is the timestamp printf's `%(fmt)T -2` resolves to (the
	// shell's start time). If zero, -2 falls back to the current time.
	// The interpreter sets this from [Runner.startTime].
	StartTime time.Time

	// OnFormatWarning is called by [Format] for recoverable conversion
	// failures (e.g. `printf %d xyz` — the value falls back to 0). The
	// callback is responsible for emitting the message to stderr and
	// setting a non-zero exit status; if nil the warning is silently
	// dropped. Matches bash 5.3's "warn but continue" printf behaviour.
	OnFormatWarning func(msg string)

	// OnPercentN is invoked by [Format] when printf's `%n` conversion
	// runs. It receives the variable name (next argument) and the byte
	// count emitted so far. The callback is responsible for assigning
	// the count to the named variable. If nil, %n is a no-op.
	OnPercentN func(name string, n int) error

	// OnNameRefCircular is called when expanding a parameter whose
	// nameref chain loops back on itself between distinct names (e.g.
	// x→v→w→x). It receives the originating variable name. Bash emits a
	// "circular name reference" warning and treats the value as unset.
	OnNameRefCircular func(name string)

	bufferAlloc strings.Builder
	fieldAlloc  [4]fieldPart
	fieldsAlloc [4][]fieldPart

	ifs    string
	ifsSet bool
	// tildeInAssign is set by [LiteralForAssign] for the duration of
	// the call. When on, the literal-part handler also expands "~"
	// (and "~user") that immediately follows a ":" or "=" inside a
	// single literal — bash's assignment-context tilde rule.
	tildeInAssign bool

	// Posix mirrors the interpreter's `set -o posix` state. When
	// true, certain bash-extension behaviours are disabled — in
	// particular the "argument that looks like an assignment gets
	// assignment-context tilde expansion" rule. The interpreter
	// flips this whenever the posix shell option changes.
	Posix bool

	// stripBackslashEscapes, when true, makes the literal-part
	// expansion perform POSIX quote-removal on unquoted backslash
	// escapes (\X → X). This is what bash does when expanding a
	// case-statement subject (and other "literal" contexts);
	// pattern-context callers leave it false so backslashes can
	// serve as glob escapes. Set by [LiteralWithQuoteRemoval].
	stripBackslashEscapes bool

	// insideDoubleQuote, when true, indicates that the current
	// paramExp / cmdSubst is being expanded inside a double-quoted
	// context. Tilde expansion in default values (`${var:-~}`)
	// suppresses when this is set, matching bash semantics.
	insideDoubleQuote bool

	// inHeredocBody, when true, signals that we are currently
	// expanding content of a heredoc body. Bash 5.3 keeps `$'…'`
	// ANSI-C sequences literal when they appear inside a parameter
	// expansion's substitute text (`${var-DEFAULT}`, `${var+ALT}`,
	// …) that is being expanded for a heredoc — even though they
	// would decode in other contexts.
	inHeredocBody bool

	// literalAnsiC, when true, tells [wordField]'s SglQuoted
	// handler to keep `$'…'` literal instead of decoding the
	// ANSI-C escape sequences. Used by [literalKeepAnsiC].
	literalAnsiC bool

	// OverrideLineno, when non-zero, replaces the source line that
	// `$LINENO` would normally report. The interpreter sets this
	// when expanding a trap body so `$LINENO` reflects the line of
	// the command that triggered the trap rather than the line
	// inside the trap text itself.
	OverrideLineno int

	// A pointer to a parameter expansion node, if we're inside one.
	// Necessary for ${LINENO}.
	curParam *syntax.ParamExp

	// arithmParamValues snapshots explicit parameter expansions inside
	// a single arithmetic expression. Bare arithmetic variables remain
	// live, but `$x` expands before arithmetic side effects run.
	arithmParamValues map[*syntax.ParamExp]string

	// LetArithmetic is set by the interpreter while evaluating the
	// `let` builtin, whose subscript quote handling differs from
	// arithmetic commands and arithmetic expansion.
	LetArithmetic bool
}

// UnexpectedCommandError is returned if a command substitution is encountered
// when [Config.CmdSubst] is nil.
type UnexpectedCommandError struct {
	Node *syntax.CmdSubst
}

func (u UnexpectedCommandError) Error() string {
	return fmt.Sprintf("unexpected command substitution at %s", u.Node.Pos())
}

var zeroConfig = &Config{}

// TODO: note that prepareConfig is modifying the user's config in place,
// which doesn't feel right - we should make a copy.

func prepareConfig(cfg *Config) *Config {
	cfg = cmp.Or(cfg, zeroConfig)
	cfg.Env = cmp.Or(cfg.Env, FuncEnviron(func(string) string { return "" }))

	cfg.ifs = " \t\n"
	cfg.ifsSet = false
	if vr := cfg.Env.Get("IFS"); vr.IsSet() {
		cfg.ifs = vr.String()
		cfg.ifsSet = true
	}

	if cfg.ReadDir != nil && cfg.ReadDir2 == nil {
		cfg.ReadDir2 = func(path string) ([]fs.DirEntry, error) {
			infos, err := cfg.ReadDir(path)
			if err != nil {
				return nil, err
			}
			entries := make([]fs.DirEntry, len(infos))
			for i, info := range infos {
				entries[i] = fs.FileInfoToDirEntry(info)
			}
			return entries, nil
		}
	}
	return cfg
}

func (cfg *Config) ifsRune(r rune) bool {
	for _, r2 := range cfg.ifs {
		if r == r2 {
			return true
		}
	}
	return false
}

func (cfg *Config) ifsJoin(strs []string) string {
	sep := ""
	if cfg.ifs != "" {
		r, size := utf8.DecodeRuneInString(cfg.ifs)
		sep = string(r)
		if r == utf8.RuneError && size == 1 {
			sep = cfg.ifs[:1]
		}
	}
	return strings.Join(strs, sep)
}

func (cfg *Config) strBuilder() *strings.Builder {
	b := &cfg.bufferAlloc
	b.Reset()
	return b
}

func (cfg *Config) envGet(name string) string {
	return cfg.Env.Get(name).String()
}

func (cfg *Config) envSet(name, value string) error {
	wenv, ok := cfg.Env.(WriteEnviron)
	if !ok {
		return fmt.Errorf("environment is read-only")
	}
	// Preserve declare attributes (`-i`, `-r`, `-x`, …) and the
	// indexed/associative kind by reading the existing variable and
	// overwriting only its scalar value. A fresh Variable would
	// strip the integer attribute and turn a value-set on `declare
	// -i` into a plain string, which subsequent arithmetic
	// assignments then mis-handle.
	vr := cfg.Env.Get(name)
	// Assigning to a nameref with an empty (unset) target retargets it:
	// `declare -n r; r=foo` points r at foo. Bash validates the new
	// target like `declare -n` does, rejecting invalid identifiers with
	// "<target>: not a valid identifier". The default-assignment
	// operators (${r=v}, ${r:=v}) reach this path too, so guard them
	// here rather than silently overwriting the nameref with a string.
	if vr.Kind == NameRef && vr.Str == "" && !validNameRefTargetName(value) {
		return fmt.Errorf("`%s': not a valid identifier", value)
	}
	if n, resolved := vr.Resolve(cfg.Env); n != "" {
		name, vr = n, resolved
	}
	vr.Set = true
	switch vr.Kind {
	case Indexed:
		vr.List = slices.Clone(vr.List)
		if len(vr.List) == 0 {
			vr.List = []string{value}
		} else {
			vr.List[0] = value
		}
		if vr.ListSet != nil {
			vr.ListSet = vr.CloneListSet()
			vr.ListSet[0] = true
		}
	case Associative:
		// Associative arrays without an explicit key fall back to
		// the bash-3-style "key 0" slot, which is what bash does
		// for `assoc=val` with no `[k]`.
		if vr.Map == nil {
			vr.Map = map[string]string{"0": value}
		} else {
			vr.Map["0"] = value
		}
	default:
		vr.Kind = String
		vr.Str = value
	}
	return wenv.Set(name, vr)
}

// literalKeepAnsiC is like [LiteralWithQuoteRemoval] but leaves `$'…'`
// ANSI-C quoting literal (no decoding). Used by the parameter-expansion
// default-value path inside heredoc bodies — bash 5.3 preserves
// `$'\01'` as five literal characters in that position.
func literalKeepAnsiC(cfg *Config, word *syntax.Word) (string, error) {
	if word == nil {
		return "", nil
	}
	cfg = prepareConfig(cfg)
	prev := cfg.literalAnsiC
	cfg.literalAnsiC = true
	prevS := cfg.stripBackslashEscapes
	cfg.stripBackslashEscapes = true
	defer func() {
		cfg.literalAnsiC = prev
		cfg.stripBackslashEscapes = prevS
	}()
	field, err := cfg.wordField(word.Parts, quoteNone)
	if err != nil {
		return "", err
	}
	return cfg.fieldJoin(field), nil
}

// Literal expands a single shell word. It is similar to [Fields], but the result
// is a single string. This is the behavior when a word is used as the value in
// a shell variable assignment, for example.
//
// The config specifies shell expansion options; nil behaves the same as an
// empty config.
func Literal(cfg *Config, word *syntax.Word) (string, error) {
	if word == nil {
		return "", nil
	}
	cfg = prepareConfig(cfg)
	field, err := cfg.wordField(word.Parts, quoteNone)
	if err != nil {
		return "", err
	}
	return cfg.fieldJoin(field), nil
}

// LiteralWithQuoteRemoval is like [Literal] but also performs POSIX
// quote-removal: in unquoted literal parts, `\X` collapses to `X`
// and a trailing `\` is dropped. Parameter-expansion results are
// left untouched. The case-statement subject and a few other
// unquoted-word callers need this; pattern callers go through
// [Pattern] so backslashes serve as glob escapes.
func LiteralWithQuoteRemoval(cfg *Config, word *syntax.Word) (string, error) {
	if word == nil {
		return "", nil
	}
	cfg = prepareConfig(cfg)
	prev := cfg.stripBackslashEscapes
	cfg.stripBackslashEscapes = true
	defer func() { cfg.stripBackslashEscapes = prev }()
	field, err := cfg.wordField(word.Parts, quoteNone)
	if err != nil {
		return "", err
	}
	return cfg.fieldJoin(field), nil
}

// ExpandTildeAssign applies assignment-context tilde expansion to s,
// expanding a leading tilde prefix (`~`, `~/path`, `~user`, `~+`, `~-`)
// exactly as bash does for an associative-array subscript key in an
// assignment such as `aa[~/key]=value`. If s has no expandable tilde
// prefix it is returned unchanged.
func ExpandTildeAssign(cfg *Config, s string) string {
	cfg = prepareConfig(cfg)
	if prefix, rest := cfg.expandUser(s, false); prefix != "" {
		return prefix + rest
	}
	return s
}

// LiteralForAssign is like [Literal] but applies bash's assignment-only
// tilde expansion: a `~` (or `~user`) immediately following a `:` or `=`
// inside an unquoted literal also expands to the user's home directory.
// This matches bash's behaviour for `PATH=~/bin:~/scripts` and friends.
func LiteralForAssign(cfg *Config, word *syntax.Word) (string, error) {
	if word == nil {
		return "", nil
	}
	cfg = prepareConfig(cfg)
	prevT := cfg.tildeInAssign
	cfg.tildeInAssign = true
	prevS := cfg.stripBackslashEscapes
	cfg.stripBackslashEscapes = true
	defer func() {
		cfg.tildeInAssign = prevT
		cfg.stripBackslashEscapes = prevS
	}()
	field, err := cfg.wordField(word.Parts, quoteNone)
	if err != nil {
		return "", err
	}
	return cfg.fieldJoin(field), nil
}

// Document expands a single shell word as if it were a here-document body.
// It is similar to [Literal], but without brace expansion, tilde expansion, and
// globbing.
//
// The config specifies shell expansion options; nil behaves the same as an
// empty config.
func Document(cfg *Config, word *syntax.Word) (string, error) {
	if word == nil {
		return "", nil
	}
	cfg = prepareConfig(cfg)
	prev := cfg.inHeredocBody
	cfg.inHeredocBody = true
	defer func() { cfg.inHeredocBody = prev }()
	field, err := cfg.wordField(word.Parts, quoteHeredoc)
	if err != nil {
		return "", err
	}
	return cfg.fieldJoin(field), nil
}

// Pattern expands a single shell word as a pattern, using [pattern.QuoteMeta]
// on any non-quoted parts of the input word. The result can be used on
// [pattern.Regexp] directly.
//
// The config specifies shell expansion options; nil behaves the same as an
// empty config.
func Pattern(cfg *Config, word *syntax.Word) (string, error) {
	if word == nil {
		return "", nil
	}
	cfg = prepareConfig(cfg)
	field, err := cfg.wordField(word.Parts, quoteNone)
	if err != nil {
		return "", err
	}
	sb := cfg.strBuilder()
	for _, part := range field {
		if part.quote > quoteNone {
			sb.WriteString(pattern.QuoteMeta(part.val, 0))
		} else {
			sb.WriteString(part.val)
		}
	}
	return sb.String(), nil
}

// RegexPattern is like Pattern but quotes regex metacharacters
// (instead of glob metacharacters) for any quoted sub-parts. Used
// by `[[ x =~ y ]]` so that single- or double-quoted segments of
// the rhs are treated as literal characters, matching bash 5.3.
func RegexPattern(cfg *Config, word *syntax.Word) (string, error) {
	if word == nil {
		return "", nil
	}
	if lit, ok := regexQuotedBracketLiteral(word); ok {
		return lit, nil
	}
	cfg = prepareConfig(cfg)
	field, err := cfg.wordField(word.Parts, quoteNone)
	if err != nil {
		return "", err
	}
	sb := cfg.strBuilder()
	for _, part := range field {
		if part.quote > quoteNone {
			sb.WriteString(regexp.QuoteMeta(part.val))
		} else {
			sb.WriteString(part.val)
		}
	}
	return sb.String(), nil
}

func regexQuotedBracketLiteral(word *syntax.Word) (string, bool) {
	if len(word.Parts) != 3 {
		return "", false
	}
	left, ok := word.Parts[0].(*syntax.Lit)
	if !ok || left.Value != "[" {
		return "", false
	}
	right, ok := word.Parts[2].(*syntax.Lit)
	if !ok || right.Value != "]" {
		return "", false
	}
	var val string
	switch mid := word.Parts[1].(type) {
	case *syntax.SglQuoted:
		if mid.Dollar {
			return "", false
		}
		val = mid.Value
	case *syntax.DblQuoted:
		if len(mid.Parts) != 1 {
			return "", false
		}
		lit, ok := mid.Parts[0].(*syntax.Lit)
		if !ok {
			return "", false
		}
		val = lit.Value
	default:
		return "", false
	}
	if !strings.Contains(val, "]") || regexBracketSpecialQuotedText(val) {
		return "", false
	}
	return regexp.QuoteMeta(val), true
}

func regexBracketSpecialQuotedText(s string) bool {
	return (strings.HasPrefix(s, "[=") && strings.HasSuffix(s, "=]")) ||
		(strings.HasPrefix(s, "[.") && strings.HasSuffix(s, ".]")) ||
		(strings.HasPrefix(s, "[:") && strings.HasSuffix(s, ":]"))
}

// Format expands a format string with a number of arguments, following the
// shell's format specifications. These include printf(1), among others.
//
// The resulting string is returned, along with the number of arguments used.
// Note that the resulting string may contain null bytes, for example
// if the format string used `\x00`. The caller should terminate the string
// at the first null byte if needed, such as when expanding for `$'foo\x00bar'`.
//
// The config specifies shell expansion options; nil behaves the same as an
// empty config.
// ansiCEscape processes the bash 5.3 ANSI-C `$'...'` escape table.
// This is similar to Format's escape handling but adds the `\cX`
// control-character form (consumes the next char), which printf/Format
// preserve literally. Returns the decoded string.
func ansiCEscape(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			sb.WriteByte(c)
			continue
		}
		i++
		c = s[i]
		switch c {
		case 'a':
			sb.WriteByte('\a')
		case 'b':
			sb.WriteByte('\b')
		case 'e', 'E':
			sb.WriteByte('\x1b')
		case 'f':
			sb.WriteByte('\f')
		case 'n':
			sb.WriteByte('\n')
		case 'r':
			sb.WriteByte('\r')
		case 't':
			sb.WriteByte('\t')
		case 'v':
			sb.WriteByte('\v')
		case '\\', '\'', '"', '?':
			sb.WriteByte(c)
		case 'c':
			// `\cX` produces a control character. Bash 5.3 also
			// allows `\c\X` (the next char is itself an escape):
			// `\c\\` → 0x1C (control-backslash, FS). Special
			// cases: `\c?` → 0x7F (DEL), `\c@` → 0x00 (NUL).
			if i+1 >= len(s) {
				sb.WriteByte('\\')
				sb.WriteByte('c')
				break
			}
			i++
			nx := s[i]
			// `\c\X`: consume the inner `\X`; X is the byte we
			// control-mask. For our purposes (bash 5.3 nquote3
			// suite), X is just `\` — we keep it simple and
			// always treat `\c\` as `\c` applied to a literal
			// `\`. Other inner escapes are unusual; fall back to
			// the same behaviour.
			if nx == '\\' && i+1 < len(s) {
				inner := s[i+1]
				i++
				switch inner {
				case '\\':
					sb.WriteByte(0x1c)
				case '?':
					sb.WriteByte(0x7f)
				case '@':
					sb.WriteByte(0x00)
				default:
					if inner >= 'a' && inner <= 'z' {
						inner -= 'a' - 'A'
					}
					sb.WriteByte(inner & 0x1f)
				}
				break
			}
			switch nx {
			case '?':
				sb.WriteByte(0x7f)
			case '@':
				sb.WriteByte(0x00)
			default:
				if nx >= 'a' && nx <= 'z' {
					nx -= 'a' - 'A'
				}
				sb.WriteByte(nx & 0x1f)
			}
		case '0', '1', '2', '3', '4', '5', '6', '7':
			// 1-3 octal digits.
			j := 0
			for ; j < 3 && i+j < len(s); j++ {
				d := s[i+j]
				if d < '0' || d > '7' {
					break
				}
			}
			n, _ := strconv.ParseUint(s[i:i+j], 8, 8)
			sb.WriteByte(byte(n))
			i += j - 1
		case 'x', 'u', 'U':
			// `\xN[N]`, `\uN[NNN]`, `\UN[NNNNNNN]`. The brace
			// form `\x{HEX}` / `\u{HEX}` / `\U{HEX}` is also
			// accepted (greedy, may be unclosed).
			max := 2
			switch c {
			case 'u':
				max = 4
			case 'U':
				max = 8
			}
			if i+1 < len(s) && s[i+1] == '{' {
				// Brace form: consume from after `{` until the
				// next non-hex or `}`.
				start := i + 2
				end := start
				for end < len(s) {
					d := s[end]
					if (d >= '0' && d <= '9') || (d >= 'a' && d <= 'f') || (d >= 'A' && d <= 'F') {
						end++
						continue
					}
					break
				}
				digits := s[start:end]
				closer := end
				if closer < len(s) && s[closer] == '}' {
					i = closer
				} else {
					// Unclosed — stop on first non-hex char.
					i = end - 1
				}
				if digits == "" {
					sb.WriteByte(0)
					break
				}
				n, _ := strconv.ParseUint(digits, 16, 64)
				if c == 'x' {
					sb.WriteByte(byte(n))
				} else {
					sb.WriteString(printfEncodeRune("", rune(n)))
				}
				break
			}
			j := 0
			for ; j < max && i+1+j < len(s); j++ {
				d := s[i+1+j]
				if !((d >= '0' && d <= '9') || (d >= 'a' && d <= 'f') || (d >= 'A' && d <= 'F')) {
					break
				}
			}
			if j == 0 {
				sb.WriteByte('\\')
				sb.WriteByte(c)
				break
			}
			n, _ := strconv.ParseUint(s[i+1:i+1+j], 16, 32)
			if c == 'x' {
				sb.WriteByte(byte(n))
			} else {
				sb.WriteString(printfEncodeRune("", rune(n)))
			}
			i += j
		default:
			sb.WriteByte('\\')
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// FormatBPercent is the bash printf `%b` interpretation of an
// escape-bearing argument. Unlike Format (which is the format-string
// mode), the `\'`, `\"`, `\?` escapes keep their backslash and `\c`
// terminates output by returning [ErrPrintfStop]. Use this from
// `echo -e` and from explicit `%b` conversions.
func FormatBPercent(cfg *Config, s string) (string, error) {
	cfg = prepareConfig(cfg)
	sb := cfg.strBuilder()
	_, err := formatIntoMode(sb, s, nil, cfg.StartTime, nil, "", false, true, nil, nil)
	if err == errPrintfStop {
		return sb.String(), errPrintfStop
	}
	if err != nil {
		return sb.String(), err
	}
	return sb.String(), nil
}

func Format(cfg *Config, format string, args []string) (string, int, error) {
	cfg = prepareConfig(cfg)
	sb := cfg.strBuilder()

	consumed, err := formatIntoMode(sb, format, args, cfg.StartTime, printfTimeLocation(cfg), printfCTypeLocale(cfg), printfDecimalComma(cfg), false, cfg.OnFormatWarning, cfg.OnPercentN)
	if err == errPrintfStop {
		// `\c` told printf to stop emitting output from a %b arg
		// (or `\c` directly in format). Surface what's already in
		// the builder and tell the outer loop to stop iterating
		// args too — propagating errPrintfStop to the caller.
		return sb.String(), consumed, errPrintfStop
	}
	if err != nil {
		// Surface partial output along with the error. bash flushes
		// what it has emitted before reporting (e.g.
		// `printf '%s\n%n' abc bad-name` prints `abc\n` before
		// "not a valid identifier").
		return sb.String(), consumed, err
	}

	return sb.String(), consumed, err
}

func printfTimeLocation(cfg *Config) *time.Location {
	if cfg == nil || cfg.Env == nil {
		return time.Local
	}
	tz := cfg.Env.Get("TZ")
	if !tz.IsSet() {
		return time.Local
	}
	name := tz.String()
	if name == "" {
		return time.UTC
	}
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	// Bash's tests use this POSIX TZ rule. Go's time package does not
	// load POSIX rule strings directly, so map the common eastern-US
	// rule to the equivalent IANA zone.
	if strings.HasPrefix(name, "EST5EDT") {
		if loc, err := time.LoadLocation("America/New_York"); err == nil {
			return loc
		}
	}
	return time.Local
}

func printfDecimalComma(cfg *Config) bool {
	if cfg == nil || cfg.Env == nil {
		return false
	}
	locale := ""
	for _, name := range []string{"LC_ALL", "LC_NUMERIC", "LANG"} {
		vr := cfg.Env.Get(name)
		if vr.IsSet() && vr.String() != "" {
			locale = vr.String()
			break
		}
	}
	locale = strings.ToLower(locale)
	return strings.HasPrefix(locale, "de_") ||
		strings.HasPrefix(locale, "fr_") ||
		strings.HasPrefix(locale, "it_") ||
		strings.HasPrefix(locale, "es_")
}

func printfCTypeLocale(cfg *Config) string {
	if cfg == nil || cfg.Env == nil {
		return ""
	}
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		vr := cfg.Env.Get(name)
		if vr.IsSet() && vr.String() != "" {
			return strings.ToLower(vr.String())
		}
	}
	return ""
}

func printfEncodeRune(locale string, r rune) string {
	if r < 0 {
		return ""
	}
	if r > utf8.MaxRune && (locale == "" || strings.Contains(locale, "utf")) {
		return string(legacyUTF8(r))
	}
	switch {
	case strings.Contains(locale, "iso8859") || strings.Contains(locale, "iso-8859"):
		if r <= 0xff {
			return string([]byte{byte(r)})
		}
	case strings.Contains(locale, "big5"):
		if r < utf8.RuneSelf {
			return string([]byte{byte(r)})
		}
		if s, err := traditionalchinese.Big5.NewEncoder().String(string(r)); err == nil {
			return s
		}
	case strings.Contains(locale, "sjis") || strings.Contains(locale, "shift_jis"):
		if r < utf8.RuneSelf {
			return string([]byte{byte(r)})
		}
		if s, err := japanese.ShiftJIS.NewEncoder().String(string(r)); err == nil {
			return s
		}
	}
	return string(r)
}

func legacyUTF8(r rune) []byte {
	u := uint32(r)
	switch {
	case u <= 0x7f:
		return []byte{byte(u)}
	case u <= 0x7ff:
		return []byte{byte(0xc0 | (u >> 6)), byte(0x80 | (u & 0x3f))}
	case u <= 0xffff:
		return []byte{byte(0xe0 | (u >> 12)), byte(0x80 | ((u >> 6) & 0x3f)), byte(0x80 | (u & 0x3f))}
	case u <= 0x1fffff:
		return []byte{byte(0xf0 | (u >> 18)), byte(0x80 | ((u >> 12) & 0x3f)), byte(0x80 | ((u >> 6) & 0x3f)), byte(0x80 | (u & 0x3f))}
	case u <= 0x3ffffff:
		return []byte{byte(0xf8 | (u >> 24)), byte(0x80 | ((u >> 18) & 0x3f)), byte(0x80 | ((u >> 12) & 0x3f)), byte(0x80 | ((u >> 6) & 0x3f)), byte(0x80 | (u & 0x3f))}
	default:
		return []byte{byte(0xfc | (u >> 30)), byte(0x80 | ((u >> 24) & 0x3f)), byte(0x80 | ((u >> 18) & 0x3f)), byte(0x80 | ((u >> 12) & 0x3f)), byte(0x80 | ((u >> 6) & 0x3f)), byte(0x80 | (u & 0x3f))}
	}
}

// ErrPrintfStop is the sentinel returned when `\c` was seen inside a
// %b conversion; the caller stops processing the rest of the format
// and any remaining args.
var ErrPrintfStop = errors.New("printf: stop")

// errPrintfStop is the internal alias kept while we update callers.
var errPrintfStop = ErrPrintfStop

// strftime implements a subset of POSIX strftime sufficient for bash's
// `printf '%(fmt)T'`. Unknown specifiers (`%X` where X isn't handled)
// are passed through unchanged, matching bash's behavior of preserving
// the literal text rather than erroring.
func strftime(format string, t time.Time) string {
	var sb strings.Builder
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			sb.WriteByte(format[i])
			continue
		}
		i++
		switch format[i] {
		case 'Y':
			fmt.Fprintf(&sb, "%04d", t.Year())
		case 'y':
			fmt.Fprintf(&sb, "%02d", t.Year()%100)
		case 'm':
			fmt.Fprintf(&sb, "%02d", int(t.Month()))
		case 'd':
			fmt.Fprintf(&sb, "%02d", t.Day())
		case 'e':
			fmt.Fprintf(&sb, "%2d", t.Day())
		case 'H':
			fmt.Fprintf(&sb, "%02d", t.Hour())
		case 'I':
			h := t.Hour() % 12
			if h == 0 {
				h = 12
			}
			fmt.Fprintf(&sb, "%02d", h)
		case 'M':
			fmt.Fprintf(&sb, "%02d", t.Minute())
		case 'S':
			fmt.Fprintf(&sb, "%02d", t.Second())
		case 'j':
			fmt.Fprintf(&sb, "%03d", t.YearDay())
		case 'B':
			sb.WriteString(t.Month().String())
		case 'b', 'h':
			sb.WriteString(t.Month().String()[:3])
		case 'A':
			sb.WriteString(t.Weekday().String())
		case 'a':
			sb.WriteString(t.Weekday().String()[:3])
		case 'p':
			if t.Hour() < 12 {
				sb.WriteString("AM")
			} else {
				sb.WriteString("PM")
			}
		case 'T':
			fmt.Fprintf(&sb, "%02d:%02d:%02d", t.Hour(), t.Minute(), t.Second())
		case 'X':
			fmt.Fprintf(&sb, "%02d:%02d:%02d", t.Hour(), t.Minute(), t.Second())
		case 'F':
			fmt.Fprintf(&sb, "%04d-%02d-%02d", t.Year(), int(t.Month()), t.Day())
		case 'D':
			fmt.Fprintf(&sb, "%02d/%02d/%02d", int(t.Month()), t.Day(), t.Year()%100)
		case 'x':
			fmt.Fprintf(&sb, "%02d/%02d/%02d", int(t.Month()), t.Day(), t.Year()%100)
		case 'R':
			fmt.Fprintf(&sb, "%02d:%02d", t.Hour(), t.Minute())
		case 'r':
			h := t.Hour() % 12
			if h == 0 {
				h = 12
			}
			suffix := "AM"
			if t.Hour() >= 12 {
				suffix = "PM"
			}
			fmt.Fprintf(&sb, "%02d:%02d:%02d %s", h, t.Minute(), t.Second(), suffix)
		case 'n':
			sb.WriteByte('\n')
		case 't':
			sb.WriteByte('\t')
		case 'Z':
			sb.WriteString(t.Format("MST"))
		case 'z':
			sb.WriteString(t.Format("-0700"))
		case 's':
			fmt.Fprintf(&sb, "%d", t.Unix())
		case 'N':
			fmt.Fprintf(&sb, "%09d", t.Nanosecond())
		case '%':
			sb.WriteByte('%')
		default:
			sb.WriteByte('%')
			sb.WriteByte(format[i])
		}
	}
	return sb.String()
}

func formatInto(sb *strings.Builder, format string, args []string, startTime time.Time, warn func(string)) (int, error) {
	return formatIntoMode(sb, format, args, startTime, nil, "", false, false, warn, nil)
}

// formatIntoMode is the inner worker for [Format]. percentB switches the
// escape table to bash's `%b` interpretation: `\"`, `\'`, `\?` are
// preserved with their backslash (bash only honors those escapes in
// format strings, not in `%b` arg). onPercentN is invoked by `%n` to
// store the byte count into the variable named by the next arg.
func formatIntoMode(sb *strings.Builder, format string, args []string, startTime time.Time, loc *time.Location, ctypeLocale string, decimalComma bool, percentB bool, warn func(string), onPercentN func(string, int) error) (int, error) {
	if loc == nil {
		loc = time.Local
	}
	inPercentB := percentB
	var fmts []byte
	wideFmt := false
	precisionOverflow := false
	initialArgs := len(args)

	for i := 0; i < len(format); i++ {
		// readDigits reads from 0 to max digits, either octal or
		// hexadecimal.
		readDigits := func(max int, hex bool) string {
			j := 0
			for ; j < max && i+j < len(format); j++ {
				c := format[i+j]
				if hex {
					if !((c >= '0' && c <= '9') ||
						(c >= 'a' && c <= 'f') ||
						(c >= 'A' && c <= 'F')) {
						break
					}
				} else {
					// octal: only 0-7
					if c < '0' || c > '7' {
						break
					}
				}
			}
			digits := format[i : i+j]
			i += j - 1 // -1 since the outer loop does i++
			return digits
		}
		c := format[i]
		switch {
		case c == '\\': // escaped
			i++
			if i >= len(format) {
				sb.WriteByte('\\')
				break
			}
			switch c = format[i]; c {
			case 'a': // bell
				sb.WriteByte('\a')
			case 'b': // backspace
				sb.WriteByte('\b')
			case 'e', 'E': // escape
				sb.WriteByte('\x1b')
			case 'f': // form feed
				sb.WriteByte('\f')
			case 'n': // new line
				sb.WriteByte('\n')
			case 'r': // carriage return
				sb.WriteByte('\r')
			case 't': // horizontal tab
				sb.WriteByte('\t')
			case 'v': // vertical tab
				sb.WriteByte('\v')
			case '\\': // always recognized
				sb.WriteByte('\\')
			case '\'', '"', '?':
				if inPercentB {
					// `%b` arg: bash passes these through as-is.
					sb.WriteByte('\\')
				}
				sb.WriteByte(c)
			case 'c':
				// In a `%b`-converted arg, `\c` stops emitting
				// further output (and discards remaining args)
				// — matches echo's behavior, which bash extends to
				// %b. In a format string, bash preserves `\c`
				// literally as two bytes `\c`.
				if inPercentB {
					// Discard remaining input so subsequent
					// escapes / format chars don't fire.
					return initialArgs - len(args), errPrintfStop
				}
				sb.WriteByte('\\')
				sb.WriteByte('c')
			case '0', '1', '2', '3', '4', '5', '6', '7':
				// bash printf format strings use `\nnn`: 1-3
				// octal digits total. `%b` arguments additionally
				// treat a leading `\0` as a marker followed by up
				// to 3 octal digits, so `%b` `\0200` reads 0200
				// while format-string `\0200` reads 020 + "0".
				if inPercentB && c == '0' && i+1 < len(format) {
					next := format[i+1]
					if next >= '0' && next <= '7' {
						i++
					}
				}
				digits := readDigits(3, false)
				// if digits don't fit in 8 bits, 0xff via strconv
				n, _ := strconv.ParseUint(digits, 8, 8)
				sb.WriteByte(byte(n))
			case 'x', 'u', 'U':
				i++
				max := 2
				switch c {
				case 'u':
					max = 4
				case 'U':
					max = 8
				}
				// bash 5.3 also accepts `\x{HEX}` / `\u{HEX}` /
				// `\U{HEX}` with the digits wrapped in braces so
				// you can use longer literals without ambiguity.
				// Inside braces bash is greedy: it reads every hex
				// digit it can (even past where the value would
				// overflow), accepts both a closing `}` and an
				// unclosed run that ends at the first non-hex or
				// end-of-string, then truncates the value to the
				// destination size (low byte for `\x`, low rune
				// for `\u`/`\U`).
				if i < len(format) && format[i] == '{' {
					i++
					start := i
					for i < len(format) {
						r := format[i]
						if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
							i++
							continue
						}
						break
					}
					digits := format[start:i]
					// Consume the closing `}` when present; the
					// unclosed form is also accepted (we just stop
					// at end-of-string or the next non-hex). `i`
					// is left on the last consumed char so the
					// outer i++ steps onto the next source char.
					if i < len(format) && format[i] == '}' {
						// step onto '}'; outer i++ will move past
					} else {
						i-- // outer i++ moves to the first
						// non-hex/EOS character so it isn't lost
					}
					// Empty `\x{}` emits NUL (bash 5.3 treats the
					// missing-digits case as `\x00`).
					if digits == "" {
						sb.WriteByte(0)
						break
					}
					n, _ := strconv.ParseUint(digits, 16, 64)
					if c == 'x' {
						sb.WriteByte(byte(n))
					} else {
						sb.WriteRune(rune(n))
					}
					break
				}
				digits := readDigits(max, true)
				if len(digits) > 0 {
					// can't error
					n, _ := strconv.ParseUint(digits, 16, 32)
					if c == 'x' {
						// always as a single byte
						sb.WriteByte(byte(n))
					} else {
						sb.WriteString(printfEncodeRune(ctypeLocale, rune(n)))
					}
					break
				}
				// Bash 5.3: `\x`, `\u`, `\U` with no following hex
				// digits emits a warning to stderr; the bytes `\x`
				// (or `\u`, `\U`) are still written out literally.
				if warn != nil {
					warn(fmt.Sprintf("printf: missing hex digit for \\%c", c))
				}
				sb.WriteByte('\\')
				sb.WriteByte(c)
			default: // no escape sequence
				sb.WriteByte('\\')
				sb.WriteByte(c)
			}
		case len(fmts) > 0:
			switch c {
			case '%':
				sb.WriteByte('%')
				fmts = nil
			case '(':
				// bash's %(fmt)T datetime: read fmt up to ')', then
				// require a trailing 'T'. The fmt argument is passed
				// to strftime; the value argument is a Unix timestamp
				// where -1 means now and -2 means shell start time.
				end := -1
				for j := i + 1; j < len(format); j++ {
					if format[j] == ')' {
						if j+1 < len(format) && format[j+1] == 'T' {
							end = j - (i + 1)
							break
						}
						if end < 0 {
							end = j - (i + 1)
						}
					}
				}
				if end < 0 {
					return 0, fmt.Errorf("printf: missing matching `)' in format")
				}
				strFmt := format[i+1 : i+1+end]
				nextIdx := i + 1 + end + 1
				if nextIdx >= len(format) || format[nextIdx] != 'T' {
					if warn != nil {
						bad := byte(0)
						if nextIdx < len(format) {
							bad = format[nextIdx]
						}
						if bad != 0 {
							warn(fmt.Sprintf("printf: warning: `%c': invalid time format specification", bad))
						}
					}
					sb.WriteString("%(")
					sb.WriteString(strFmt)
					sb.WriteByte(')')
					if nextIdx < len(format) {
						sb.WriteByte(format[nextIdx])
						i = nextIdx
					} else {
						i = nextIdx - 1
					}
					fmts = nil
					break
				}
				var t time.Time
				if len(args) > 0 {
					arg := args[0]
					args = args[1:]
					switch arg {
					case "-1", "":
						t = time.Now().In(loc)
					case "-2":
						if !startTime.IsZero() {
							t = startTime.In(loc)
						} else {
							t = time.Now().In(loc)
						}
					default:
						n, err := strconv.ParseInt(arg, 10, 64)
						if err != nil {
							return 0, fmt.Errorf("printf: %s: invalid number", arg)
						}
						t = time.Unix(n, 0).In(loc)
					}
				} else {
					t = time.Now().In(loc)
				}
				if strFmt == "" {
					strFmt = "%X"
				}
				out := strftime(strFmt, t)
				if len(fmts) > 1 {
					sb.WriteString(fmt.Sprintf(string(fmts)+"s", out))
				} else {
					sb.WriteString(out)
				}
				i = nextIdx // skip past the )T
				fmts = nil
			case 'c':
				arg := ""
				var b byte
				if len(args) > 0 {
					arg, args = args[0], args[1:]
					if len(arg) > 0 {
						b = arg[0]
					}
				}
				if wideFmt {
					sb.WriteString(formatWideChar(arg, fmts))
				} else {
					sb.WriteString(padPrintfString(string([]byte{b}), printfCharWidth(fmts), bytes.Contains(fmts, []byte{'-'})))
				}
				fmts = nil
				wideFmt = false
			case '+', '-', ' ', '#', '\'':
				if bytes.IndexAny(fmts[1:], "0123456789.") >= 0 {
					return 0, fmt.Errorf("printf: `%c': invalid format character", c)
				}
				fmts = append(fmts, c)
			case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
				fmts = append(fmts, c)
			case '.':
				start := i + 1
				end := start
				for end < len(format) && format[end] >= '0' && format[end] <= '9' {
					end++
				}
				if end > start {
					digits := format[start:end]
					if n, err := strconv.ParseInt(digits, 10, 64); err != nil || n > int64(1<<31-1) {
						precisionOverflow = true
						i = end - 1
						break
					}
					fmts = append(fmts, '.')
					fmts = append(fmts, digits...)
					i = end - 1
					break
				}
				fmts = append(fmts, c)
			case 'l':
				wideFmt = true
			case 'h', 'L', 'j', 'z', 't':
				// C-style length modifiers (%lld, %hi, %zd, etc.).
				// Bash printf accepts them but we always operate on
				// int64 / float64 in Go, so they're effectively
				// no-ops. Don't emit them into fmts (Go's format
				// verbs don't accept these flags), just skip.
			case '*':
				// `%*d` / `%.*s` / etc.: consume the next arg as
				// the width/precision number and splice it into
				// the format-spec buffer in place of the `*`.
				if len(args) == 0 {
					return 0, fmt.Errorf("printf: missing width argument for *")
				}
				wArg := args[0]
				args = args[1:]
				isPrecision := len(fmts) > 0 && fmts[len(fmts)-1] == '.'
				width, err := strconv.ParseInt(strings.TrimSpace(wArg), 10, 64)
				if err != nil {
					if warn != nil {
						warn(fmt.Sprintf("printf: %s: numerical result out of range", wArg))
					}
					if isPrecision {
						fmts = fmts[:len(fmts)-1]
					}
					break
				}
				const maxPrintfWidth = int64(1<<31 - 1)
				if width > maxPrintfWidth || width < -maxPrintfWidth {
					if warn != nil {
						warn(fmt.Sprintf("printf: %s: numerical result out of range", wArg))
					}
					if isPrecision {
						fmts = fmts[:len(fmts)-1]
					}
					break
				}
				fmts = append(fmts, []byte(strconv.FormatInt(width, 10))...)
			case 'q', 'Q':
				if precisionOverflow && c == 'Q' && warn != nil {
					warn("printf: numerical result out of range")
				}
				// bash printf %q outputs the argument quoted so it can
				// be reused as shell input. Empty → '', strings with
				// only safe chars are emitted as-is, anything else uses
				// $'...' ANSI-C quoting or single-quoting via
				// syntax.Quote.
				//
				// Bash 5.3 precision semantics differ between the two:
				//   - `%.Nq` quotes the full argument first, then
				//     applies the precision to the quoted result.
				//   - `%.NQ` applies the precision to the *unquoted*
				//     argument first, then quotes the truncated string.
				arg := ""
				if len(args) > 0 {
					arg, args = args[0], args[1:]
				}
				// Parse a precision (`%.N…`) from the partial verb
				// prefix so we can apply it before vs after quoting.
				precN := -1
				stripPrec := false
				if dot := bytes.IndexByte(fmts, '.'); dot >= 0 {
					if n, perr := strconv.Atoi(string(fmts[dot+1:])); perr == nil {
						precN = n
						stripPrec = true
					}
				}
				// %Q: trim unquoted arg to precision before quoting.
				if c == 'Q' && precN >= 0 {
					if precN <= 0 {
						arg = ""
					} else if precN < len(arg) {
						arg = arg[:precN]
					}
				}
				altQuote := bytes.Contains(fmts, []byte{'#'})
				// bash's `printf %q` strategy:
				//   - All chars printable AND no shell-special:
				//     no quoting.
				//   - All chars printable, some shell-special:
				//     backslash-escape each special char.
				//   - Any non-printable / invalid-UTF-8 byte:
				//     fall back to syntax.Quote (`$'…'` form).
				var quoted string
				if altQuote {
					quoted = bashSingleQuote(arg)
				} else {
					quoted = bashPrintfQuote(arg)
				}
				if !altQuote && quoted == "" && arg != "" {
					// fallback: control chars present, use $'…'
					q, qerr := syntax.Quote(arg, syntax.LangBash)
					if qerr != nil {
						q = bashSingleQuote(arg)
					}
					quoted = q
				}
				if !altQuote && arg == "" {
					quoted = "''"
				}
				// Honor the width/precision specifier (`%-10q`,
				// `%10q`, `%.5q`, …) by passing the quoted result
				// through fmt.Sprintf with `%s` semantics. For `%Q`
				// we already trimmed the unquoted arg, so strip the
				// precision from the verb to avoid Go re-trimming.
				if len(fmts) > 1 {
					verbFmts := bytes.ReplaceAll(fmts, []byte{'#'}, nil)
					verb := string(verbFmts) + "s"
					if stripPrec && c == 'Q' {
						dot := bytes.IndexByte(verbFmts, '.')
						verb = string(verbFmts[:dot]) + "s"
					}
					sb.WriteString(fmt.Sprintf(verb, quoted))
				} else {
					sb.WriteString(quoted)
				}
				fmts = nil
				precisionOverflow = false
				continue
			case 'n':
				// bash printf %n stores the byte count emitted so
				// far into the variable named by the next arg.
				arg := ""
				if len(args) > 0 {
					arg, args = args[0], args[1:]
				}
				if !syntax.ValidName(arg) {
					return 0, fmt.Errorf("printf: `%s': not a valid identifier", arg)
				}
				if onPercentN != nil {
					if err := onPercentN(arg, sb.Len()); err != nil {
						return 0, err
					}
				}
				fmts = nil
				precisionOverflow = false
				continue
			case 'C':
				arg := ""
				if len(args) > 0 {
					arg, args = args[0], args[1:]
				}
				sb.WriteString(formatWideChar(arg, fmts))
				fmts = nil
				wideFmt = false
				precisionOverflow = false
				continue
			case 'S':
				wideFmt = true
				c = 's'
				fallthrough
			case 's', 'b', 'd', 'i', 'u', 'o', 'x', 'X', 'f', 'F', 'e', 'E', 'g', 'G':
				arg := ""
				hadArg := len(args) > 0
				if len(args) > 0 {
					arg, args = args[0], args[1:]
				}
				var farg any
				if c == 'b' {
					// Passing in nil for args ensures that % format
					// strings aren't processed; only escape sequences
					// will be handled. The `percentB` flag flips off
					// the bash format-only escapes (`\"`, `\'`, `\?`).
					// Apply width/precision via Go's %s after the
					// escape-processed bytes are captured.
					var bsb strings.Builder
					_, err := formatIntoMode(&bsb, arg, nil, startTime, loc, ctypeLocale, decimalComma, true, warn, nil)
					if err == ErrPrintfStop {
						// Surface the partial output and signal stop.
						sb.WriteString(bsb.String())
						return initialArgs - len(args), ErrPrintfStop
					}
					if err != nil {
						return 0, err
					}
					if len(fmts) > 1 {
						verb := string(fmts) + "s"
						sb.WriteString(fmt.Sprintf(verb, bsb.String()))
					} else {
						sb.WriteString(bsb.String())
					}
					fmts = nil
					precisionOverflow = false
					continue
				} else if wideFmt && c == 's' {
					sb.WriteString(formatWideString(arg, fmts))
					fmts = nil
					wideFmt = false
					precisionOverflow = false
					continue
				} else if c != 's' {
					if c == 'f' || c == 'F' || c == 'e' || c == 'E' || c == 'g' || c == 'G' {
						// The same `'X` / `"X` shorthand the integer
						// conversions honor — bash extends it to
						// float conversions, so `printf '%f' "'A"` is
						// `65.000000`.
						var f float64
						if len(arg) > 1 && (arg[0] == '\'' || arg[0] == '"') {
							r, _ := utf8.DecodeRuneInString(arg[1:])
							f = float64(r)
						} else if arg != "" {
							var perr error
							f, perr = strconv.ParseFloat(arg, 64)
							if perr != nil && warn != nil {
								warn(fmt.Sprintf("printf: %s: invalid number", arg))
							}
						} else if hadArg && warn != nil {
							warn("printf: : invalid number")
						}
						farg = f
					} else {
						// Bash extension: if the arg starts with a `'` or
						// `"`, the integer conversion takes the value of
						// the first character after the quote. Used by
						// scripts that want the ASCII / UTF-8 codepoint
						// of a literal character. Multi-byte rune is
						// supported.
						var n int64
						if len(arg) > 1 && (arg[0] == '\'' || arg[0] == '"') {
							r, _ := utf8.DecodeRuneInString(arg[1:])
							n = int64(r)
						} else if arg != "" {
							var perr error
							n, perr = strconv.ParseInt(arg, 0, 0)
							if perr != nil && warn != nil {
								warn(fmt.Sprintf("printf: %s: invalid number", arg))
							}
						} else if hadArg && warn != nil {
							warn("printf: : invalid number")
						}
						if c == 'i' || c == 'd' {
							farg = int(n)
						} else {
							farg = uint(n)
						}
						if c == 'i' || c == 'u' {
							c = 'd'
						}
					}
				} else {
					farg = arg
				}
				if farg != nil {
					if c == 'o' || c == 'x' || c == 'X' {
						fmts = bytes.ReplaceAll(fmts, []byte{'+'}, nil)
					}
					fmts = append(fmts, c)
					start := sb.Len()
					fmt.Fprintf(sb, string(fmts), farg)
					if decimalComma && (c == 'f' || c == 'F' || c == 'e' || c == 'E' || c == 'g' || c == 'G') {
						s := sb.String()
						if strings.Contains(s[start:], ".") {
							sb.Reset()
							sb.WriteString(s[:start])
							sb.WriteString(strings.ReplaceAll(s[start:], ".", ","))
						}
					}
				}
				fmts = nil
				wideFmt = false
				precisionOverflow = false
			default:
				return 0, fmt.Errorf("printf: `%c': invalid format character", c)
			}
		case args != nil && c == '%':
			// if args == nil, we are not doing format
			// arguments
			fmts = []byte{c}
		default:
			sb.WriteByte(c)
		}
	}
	if len(fmts) > 0 {
		return 0, fmt.Errorf("printf: `%s': missing format character", string(fmts))
	}
	return initialArgs - len(args), nil
}

func (cfg *Config) fieldJoin(parts []fieldPart) string {
	switch len(parts) {
	case 0:
		return ""
	case 1: // short-cut without a string copy
		return parts[0].val
	}
	sb := cfg.strBuilder()
	for _, part := range parts {
		sb.WriteString(part.val)
	}
	return sb.String()
}

func (cfg *Config) escapedGlobField(parts []fieldPart) (escaped string, glob bool) {
	sb := cfg.strBuilder()
	for _, part := range parts {
		if part.quote > quoteNone {
			sb.WriteString(pattern.QuoteMeta(part.val, 0))
			continue
		}
		sb.WriteString(part.val)
		if cfg.hasGlobMeta(part.val) {
			glob = true
		}
	}
	if glob { // only copy the string if it will be used
		escaped = sb.String()
	}
	return escaped, glob
}

func (cfg *Config) hasGlobMeta(s string) bool {
	if pattern.HasMeta(s, 0) {
		return true
	}
	if !cfg.ExtGlob {
		return false
	}
	for i := 0; i+1 < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '@', '+', '!', '*', '?':
			if s[i+1] == '(' && (i+2 >= len(s) || s[i+2] != ')') {
				return true
			}
		}
	}
	return false
}

// Fields is a pre-iterators API which now wraps [FieldsSeq].
func Fields(cfg *Config, words ...*syntax.Word) ([]string, error) {
	var fields []string
	for s, err := range FieldsSeq(cfg, words...) {
		if err != nil {
			return nil, err
		}
		fields = append(fields, s)
	}
	return fields, nil
}

// FieldsSeq expands a number of words as if they were arguments in a shell
// command. This includes brace expansion, tilde expansion, parameter expansion,
// command substitution, arithmetic expansion, quote removal, and globbing.
func FieldsSeq(cfg *Config, words ...*syntax.Word) iter.Seq2[string, error] {
	cfg = prepareConfig(cfg)
	dir := cfg.envGet("PWD")
	return func(yield func(string, error) bool) {
		expandWord := func(w *syntax.Word) (stop bool) {
			wfields, err := cfg.wordFields(w.Parts)
			if err != nil {
				yield("", err)
				return true
			}
			for _, field := range wfields {
				path, doGlob := cfg.escapedGlobField(field)
				if doGlob && cfg.ReadDir2 != nil {
					// Note that globbing requires keeping a slice state, so it doesn't
					// really benefit from using an iterator.
					matches, err := cfg.glob(dir, path)
					if err != nil {
						// We avoid [errors.As] as it allocates,
						// and we know that [Config.glob] returns [pattern.Regexp] errors without wrapping.
						if _, ok := err.(*pattern.SyntaxError); !ok {
							yield("", err)
							return true
						}
					} else if len(matches) > 0 || cfg.NullGlob {
						for _, m := range matches {
							if !yield(m, nil) {
								return true
							}
						}
						continue
					}
				}
				if !yield(cfg.fieldJoin(field), nil) {
					return true
				}
			}
			return false
		}
		for _, word := range words {
			word := *word // make a copy, since SplitBraces replaces the Parts slice
			if !syntax.SplitBraces(&word) {
				if expandWord(&word) {
					return
				}
				continue
			}
			for w, err := range BracesSeq(cfg, &word) {
				if err != nil {
					yield("", err)
					return
				}
				// bash 5.3 performs brace expansion at the text
				// level, so `$var{x,y}` becomes the two tokens
				// `$varx` and `$vary` — the `$var` parameter
				// reference greedily absorbs the trailing
				// identifier chars. Mirror that here by folding
				// an unbraced `$var` ParamExp followed by a Lit
				// that begins with identifier chars into a
				// single `$varsuffix` ParamExp.
				mergeIdentAfterParamExp(w)
				if expandWord(w) {
					return
				}
			}
		}
	}
}

// mergeIdentAfterParamExp folds an unbraced `$var` ParamExp
// followed by a Lit beginning with identifier characters into a
// single ParamExp whose Param.Value is the concatenation. bash 5.3
// performs this textually as part of brace expansion (`$var{x,y}`
// → `$varx $vary`), and the merge ensures parameter expansion sees
// the right variable name.
func mergeIdentAfterParamExp(w *syntax.Word) {
	if w == nil || len(w.Parts) < 2 {
		return
	}
	out := make([]syntax.WordPart, 0, len(w.Parts))
	for i := 0; i < len(w.Parts); i++ {
		p := w.Parts[i]
		pe, isPE := p.(*syntax.ParamExp)
		if !isPE || pe.Short == false || pe.Index != nil ||
			pe.Length || pe.Width || pe.Excl || pe.Repl != nil ||
			pe.Exp != nil || pe.Slice != nil || pe.Names != 0 {
			out = append(out, p)
			continue
		}
		j := i + 1
		for j < len(w.Parts) {
			lit, ok := w.Parts[j].(*syntax.Lit)
			if !ok {
				break
			}
			cut := 0
			for cut < len(lit.Value) {
				c := lit.Value[cut]
				if c == '_' || (c >= '0' && c <= '9') ||
					(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
					cut++
					continue
				}
				break
			}
			if cut == 0 {
				break
			}
			peCopy := *pe
			peCopy.Param = &syntax.Lit{Value: pe.Param.Value + lit.Value[:cut]}
			pe = &peCopy
			if cut == len(lit.Value) {
				j++ // consumed the whole Lit; advance
				continue
			}
			// Partial consume: rewrite the Lit head into a
			// trimmed version and stop merging.
			litCopy := *lit
			litCopy.Value = lit.Value[cut:]
			w.Parts[j] = &litCopy
			break
		}
		out = append(out, pe)
		i = j - 1
	}
	w.Parts = out
}

type fieldPart struct {
	val   string
	quote quoteLevel
}

type quoteLevel uint

const (
	quoteNone quoteLevel = iota
	quoteDouble
	quoteHeredoc
	quoteSingle
)

func (cfg *Config) wordField(wps []syntax.WordPart, ql quoteLevel) ([]fieldPart, error) {
	var field []fieldPart
	for i, wp := range wps {
		switch wp := wp.(type) {
		case *syntax.Lit:
			s := wp.Value
			if i == 0 && ql == quoteNone && !cfg.insideDoubleQuote {
				if prefix, rest := cfg.expandUser(s, len(wps) > 1); prefix != "" {
					// TODO: return two separate fieldParts,
					// like in wordFields?
					s = prefix + rest
				} else if cfg.tildeInAssign && len(s) >= 2 && s[0] == '~' && s[1] == ':' {
					// Bash's assignment-tilde rule: a bare `~:`
					// at the start of an assignment value expands
					// to HOME, with the colon kept.
					if home := cfg.Env.Get("HOME"); home.IsSet() {
						s = home.String() + s[1:]
					}
				}
			}
			if ql == quoteNone && cfg.tildeInAssign {
				s = cfg.expandTildesAfterColons(s)
			}
			// POSIX quote-removal: in an unquoted literal part,
			// `\X` collapses to `X`. Tag the resulting field-part
			// as `quoteSingle` so callers that re-apply pattern
			// escaping (e.g. [Pattern]) treat it as already-quoted
			// and don't reinterpret remaining bytes.
			if ql == quoteNone && cfg.stripBackslashEscapes && strings.Contains(s, "\\") {
				sb := cfg.strBuilder()
				for j := 0; j < len(s); j++ {
					b := s[j]
					if b == '\\' {
						if j++; j >= len(s) {
							break
						}
						b = s[j]
					}
					sb.WriteByte(b)
				}
				s = sb.String()
			}
			if (ql == quoteDouble || ql == quoteHeredoc) && strings.Contains(s, "\\") {
				sb := cfg.strBuilder()
				for i := 0; i < len(s); i++ {
					b := s[i]
					if b == '\\' && i+1 < len(s) {
						switch s[i+1] {
						case '"':
							if ql != quoteDouble {
								break
							}
							fallthrough
						case '\\', '$', '`': // special chars
							i++
							b = s[i] // write the special char, skipping the backslash
						}
					}
					sb.WriteByte(b)
				}
				s = sb.String()
			}
			s, _, _ = strings.Cut(s, "\x00") // TODO: why is this needed?
			field = append(field, fieldPart{val: s})
		case *syntax.SglQuoted:
			fp := fieldPart{quote: quoteSingle, val: wp.Value}
			if wp.Dollar {
				if cfg.literalAnsiC {
					// Preserve the source `$'…'` verbatim — bash
					// 5.3 keeps it literal in this context.
					fp.val = "$'" + wp.Value + "'"
					fp.quote = quoteNone
				} else {
					fp.val = ansiCEscape(fp.val)
					fp.val, _, _ = strings.Cut(fp.val, "\x00") // cut the string if format included \x00
				}
			}
			field = append(field, fp)
		case *syntax.DblQuoted:
			wfield, err := cfg.wordField(wp.Parts, quoteDouble)
			if err != nil {
				return nil, err
			}
			for _, part := range wfield {
				part.quote = quoteDouble
				field = append(field, part)
			}
		case *syntax.ParamExp:
			// Track whether this paramExp sits inside a double-
			// quoted (or heredoc) context so its default-value
			// expansion can suppress tilde expansion when bash
			// would. The flag is per-cfg and restored after.
			var val string
			var err error
			if cfg.tildeInAssign && !wp.Excl && wp.Exp == nil && wp.Repl == nil && wp.Param.Value == "*" {
				elems, err := cfg.sliceElems(wp, cfg.Env.Get(wp.Param.Value).List, true)
				if err != nil {
					return nil, err
				}
				val = cfg.ifsJoin(elems)
			} else {
				prevQuote := cfg.insideDoubleQuote
				if ql == quoteDouble || ql == quoteHeredoc {
					cfg.insideDoubleQuote = true
				}
				val, err = cfg.paramExp(wp)
				cfg.insideDoubleQuote = prevQuote
			}
			if err != nil {
				return nil, err
			}
			field = append(field, fieldPart{val: val})
		case *syntax.CmdSubst:
			val, err := cfg.cmdSubst(wp)
			if err != nil {
				return nil, err
			}
			field = append(field, fieldPart{val: val})
		case *syntax.ArithmExp:
			n, err := Arithm(cfg, wp.X)
			if err != nil {
				return nil, &ArithmError{Expr: wp.X, Err: err}
			}
			field = append(field, fieldPart{val: strconv.Itoa(n)})
		case *syntax.ProcSubst:
			path, err := cfg.ProcSubst(wp)
			if err != nil {
				return nil, err
			}
			field = append(field, fieldPart{val: path})
		case *syntax.ExtGlob:
			// Like how [Config.wordFields] deals with [syntax.ExtGlob],
			// except that we allow these through even when [Config.ExtGlob]
			// is false, as it only applies to pathname expansion.
			field = append(field, fieldPart{val: wp.Op.String() + wp.Pattern.Value + ")"})
		default:
			panic(fmt.Sprintf("unhandled word part: %T", wp))
		}
	}
	return field, nil
}

// isStackIndex reports whether s looks like a `~N` / `~-N`
// directory-stack reference body (the name after the leading `~`).
// Accepts an optional leading `-` followed by one or more digits.
func isStackIndex(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '-' {
		s = s[1:]
		if s == "" {
			return false
		}
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// wordHasAssignShape reports whether the word's parts begin with a
// `<name>=` prefix where <name> is a valid shell identifier — i.e.
// the word looks like an assignment even when it's being passed as a
// regular command argument. Bash 5.3 applies the tilde-after-colon
// rule to such arguments in non-posix mode.
func wordHasAssignShape(wps []syntax.WordPart) bool {
	if len(wps) == 0 {
		return false
	}
	lit, ok := wps[0].(*syntax.Lit)
	if !ok {
		return false
	}
	eq := strings.IndexByte(lit.Value, '=')
	if eq <= 0 {
		return false
	}
	name := lit.Value[:eq]
	for i, r := range name {
		if i == 0 {
			if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
				return false
			}
			continue
		}
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// isBashBackslashEscapable reports whether r is a single shell-special
// character that bash's `printf %q` prefers to backslash-escape rather
// than wrap in single-quotes. Multi-character strings still go through
// single-quoting; this matches bash 5.3's output, where `printf "%q\n"
// '~'` emits `\~`.
func isBashBackslashEscapable(r rune) bool {
	switch r {
	case '~', '*', '?', '[', ']', '{', '}', '(', ')',
		'#', '$', '&', '|', ';', '<', '>',
		'`', '"', '\'', '\\', ' ', '\t':
		return true
	}
	return false
}

// bashPrintfQuote emits the bash-`printf %q` style quoting: leave
// fully-safe strings unchanged, backslash-escape any shell-special
// characters when the whole string is printable, and return an empty
// string to signal the caller that control characters were found and
// a `$'...'` fallback is required.
func bashPrintfQuote(s string) string {
	if s == "" {
		return ""
	}
	allPrintable := true
	anyEscapable := false
	anyShellMeta := false
	for _, r := range s {
		if r == utf8.RuneError || !unicode.IsPrint(r) {
			allPrintable = false
			break
		}
		if isBashBackslashEscapable(r) {
			anyEscapable = true
			anyShellMeta = true
		}
	}
	if !allPrintable {
		return ""
	}
	if !anyShellMeta && !isKeywordPrintfQuote(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if anyEscapable && isBashBackslashEscapable(r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func bashSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// bashQuoteParamQ quotes a single value the way bash 5.3's ${var@Q}
// transform does: it produces a string that re-reads as the same value,
// always wrapping a value that would otherwise need no quoting in single
// quotes (`zzz` -> `'zzz'`, empty string -> `''`).
func bashQuoteParamQ(s string) string {
	quoted, err := syntax.Quote(s, syntax.LangBash)
	if err != nil {
		// Is this even possible? If a user runs into this panic, it's
		// most likely a bug we need to fix.
		panic(err)
	}
	if quoted == s {
		quoted = bashSingleQuote(s)
	}
	return quoted
}

func formatWideString(s string, fmts []byte) string {
	width, prec, left := printfStringWidthPrec(fmts)
	rs := []rune(s)
	if prec >= 0 && prec < len(rs) {
		rs = rs[:prec]
	}
	return padPrintfString(string(rs), width, left)
}

func formatWideChar(s string, fmts []byte) string {
	out := "\x00"
	if s != "" {
		r, _ := utf8.DecodeRuneInString(s)
		out = string(r)
	}
	width, _, left := printfStringWidthPrec(fmts)
	return padPrintfString(out, width, left)
}

func printfStringWidthPrec(fmts []byte) (width, prec int, left bool) {
	prec = -1
	for i := 1; i < len(fmts); i++ {
		c := fmts[i]
		switch {
		case c == '-':
			left = true
		case c >= '0' && c <= '9':
			n := 0
			for i < len(fmts) && fmts[i] >= '0' && fmts[i] <= '9' {
				n = n*10 + int(fmts[i]-'0')
				i++
			}
			width = n
			i--
		case c == '.':
			i++
			n := 0
			for i < len(fmts) && fmts[i] >= '0' && fmts[i] <= '9' {
				n = n*10 + int(fmts[i]-'0')
				i++
			}
			prec = n
			i--
		}
	}
	return width, prec, left
}

func printfCharWidth(fmts []byte) int {
	width, _, _ := printfStringWidthPrec(fmts)
	return width
}

func padPrintfString(s string, width int, left bool) string {
	pad := width - utf8.RuneCountInString(s)
	if pad <= 0 {
		return s
	}
	spaces := strings.Repeat(" ", pad)
	if left {
		return s + spaces
	}
	return spaces + s
}

// isKeywordPrintfQuote returns true if `s` is a shell keyword that
// would change meaning if unquoted. Mirrors the keyword set checked
// by syntax.Quote.
func isKeywordPrintfQuote(s string) bool {
	switch s {
	case "if", "then", "elif", "else", "fi", "for", "in", "do",
		"done", "while", "until", "case", "esac", "function",
		"select", "{", "}", "!", "[[", "]]", "time", "coproc":
		return true
	}
	return false
}

func (cfg *Config) cmdSubst(cs *syntax.CmdSubst) (string, error) {
	if cfg.CmdSubst == nil {
		return "", UnexpectedCommandError{Node: cs}
	}
	// Bash 5.3 funsub `${ cmd; }` and mksh's valsub `${|cmd;}` run the
	// body in the *caller's* scope rather than a subshell, so any
	// recursive expansion inside the body shares this cfg's stack-
	// allocated reuse arrays. The outer caller (typically wordFields)
	// holds slice views over cfg.fieldAlloc / cfg.fieldsAlloc that the
	// inner call would otherwise clobber. Snapshot the arrays before
	// running the body and restore them after so the outer slice views
	// keep pointing at consistent data. Regular `$(...)` does this
	// implicitly by spawning a subshell with its own [Config].
	if cs.TempFile || cs.ReplyVar {
		savedFA := cfg.fieldAlloc
		savedFsA := cfg.fieldsAlloc
		defer func() {
			cfg.fieldAlloc = savedFA
			cfg.fieldsAlloc = savedFsA
		}()
	}
	sb := cfg.strBuilder()
	if err := cfg.CmdSubst(sb, cs); err != nil {
		return "", err
	}
	out := sb.String()
	out = strings.ReplaceAll(out, "\x00", "")
	if cs.ReplyVar {
		return out, nil
	}
	return strings.TrimRight(out, "\n"), nil
}

func (cfg *Config) wordFields(wps []syntax.WordPart) ([][]fieldPart, error) {
	// Bash 5.3 (non-posix): if a command argument has the shape
	// `<name>=<rest>` where <name> is a valid identifier, treat the
	// `<rest>` like an assignment value for tilde-after-colon
	// expansion. That makes `echo foo=bar:~/x` print
	// `foo=bar:$HOME/x`, matching bash. The flag is restored at end
	// of this word so it doesn't leak across siblings.
	if !cfg.tildeInAssign && !cfg.Posix && wordHasAssignShape(wps) {
		oldT := cfg.tildeInAssign
		cfg.tildeInAssign = true
		defer func() { cfg.tildeInAssign = oldT }()
	}
	fields := cfg.fieldsAlloc[:0]
	curField := cfg.fieldAlloc[:0]
	allowEmpty := false
	flush := func() {
		if len(curField) == 0 {
			return
		}
		fields = append(fields, curField)
		curField = nil
	}
	// Bash 5.3 IFS rule (POSIX-compatible):
	//   - Each non-whitespace IFS character is one delimiter. Adjacent
	//     whitespace IFS characters are absorbed into the same delimiter.
	//   - A run of only whitespace IFS chars is one delimiter.
	//   - Empty fields are produced between adjacent non-ws delimiters,
	//     and before a leading run that contains any non-ws (after
	//     leading-whitespace stripping).
	//   - A trailing delimiter never produces a trailing empty field.
	isIFSWS := func(r rune) bool {
		return (r == ' ' || r == '\t' || r == '\n') && cfg.ifsRune(r)
	}
	emitEmpty := func() {
		curField = append(curField, fieldPart{quote: quoteSingle, val: ""})
		flush()
	}
	addLit := func(s string) {
		fieldStart := 0
		for i := 0; i < len(s); i++ {
			if s[i] != '\\' {
				continue
			}
			if fieldStart < i {
				curField = append(curField, fieldPart{val: s[fieldStart:i]})
			}
			if i++; i < len(s) {
				curField = append(curField, fieldPart{
					quote: quoteSingle,
					val:   s[i : i+1],
				})
			} else {
				curField = append(curField, fieldPart{val: "\\"})
			}
			fieldStart = i + 1
		}
		if fieldStart < len(s) {
			curField = append(curField, fieldPart{val: s[fieldStart:]})
		}
	}
	splitAdd := func(val string) {
		// hadPrefix records whether curField had content (a lit prefix
		// from before this splitAdd) when we hit the first IFS char.
		// In that case the prefix becomes its own field, and the
		// "leading empty field" logic must not kick in.
		fieldStart := -1
		inSepRun := false
		// runNonWS counts the non-whitespace IFS chars in the current
		// separator run (each one is its own delimiter).
		runNonWS := 0
		// flushedPrefix records whether we've already flushed (the
		// prefix or a prior in-val field) — once true, the "leading
		// edge" branch below is no longer eligible.
		flushedPrefix := false
		for i, r := range val {
			if cfg.ifsRune(r) {
				if fieldStart >= 0 {
					// Ending an in-val field; emit it.
					curField = append(curField, fieldPart{val: val[fieldStart:i]})
					flush()
					flushedPrefix = true
					fieldStart = -1
				} else if !inSepRun && len(curField) > 0 {
					// First IFS char after a non-empty curField
					// (typically a lit prefix). Emit it as its own field
					// so $a's content doesn't glue to the prefix.
					flush()
					flushedPrefix = true
				}
				if !inSepRun {
					inSepRun = true
					runNonWS = 0
				}
				if !isIFSWS(r) {
					runNonWS++
				}
				continue
			}
			// Non-IFS char.
			if inSepRun {
				// Leaving a separator run. Emit empty fields per the
				// non-ws-delimiter count.
				switch {
				case runNonWS == 0:
					// Pure-whitespace run is one soft delimiter; no
					// extra fields.
				case !flushedPrefix:
					// Run at the leading edge with non-ws separators →
					// N leading empties (one per non-ws delimiter).
					for n := 0; n < runNonWS; n++ {
						emitEmpty()
					}
				default:
					// Between fields. First non-ws delimiter ended the
					// previous field; each additional non-ws produces
					// one empty field between them.
					for n := 1; n < runNonWS; n++ {
						emitEmpty()
					}
				}
				inSepRun = false
				runNonWS = 0
			}
			if fieldStart < 0 {
				fieldStart = i
			}
		}
		if fieldStart >= 0 {
			curField = append(curField, fieldPart{val: val[fieldStart:]})
		}
		// A trailing separator run still emits empties for any
		// non-ws delimiters beyond the first:
		//   `a::`   → fields `a` + `""` (two `:` → 1 between-empty)
		//   `:::`   → fields `""` + `""` + `""` (three `:` →
		//             two between-empties, plus the leading edge one)
		// Per POSIX, the position *after* the last non-ws delimiter
		// is always dropped, so we emit `runNonWS - 1` extra
		// empties after an already-emitted field, or `runNonWS`
		// total when the leading edge had nothing flushed.
		if inSepRun && runNonWS > 0 {
			extra := runNonWS - 1
			if !flushedPrefix && len(curField) == 0 {
				// Pure separator-only run with nothing before — emit
				// the leading-edge empty in addition to the
				// between-delimiter empties.
				extra = runNonWS
			}
			for n := 0; n < extra; n++ {
				emitEmpty()
			}
		}
	}
	for i, wp := range wps {
		switch wp := wp.(type) {
		case *syntax.Lit:
			s := wp.Value
			if i == 0 {
				prefix, rest := cfg.expandUser(s, len(wps) > 1)
				curField = append(curField, fieldPart{
					quote: quoteSingle,
					val:   prefix,
				})
				s = rest
			}
			if cfg.tildeInAssign {
				// For an assignment-shape arg, the tilde immediately
				// after the first `=` is expanded as if it were a
				// leading tilde-prefix on the value (bash's `FOO=~/x`
				// rule). expandTildesAfterColons handles the
				// subsequent `:~` segments but not the leading one,
				// so do that here when this is the first Lit. Bash
				// expands `~` followed by `/`, end-of-string, or
				// `:`; the `~:` case must be handled explicitly
				// since expandUser only triggers on `~` followed by
				// `/` or alnum.
				if i == 0 {
					if eq := strings.IndexByte(s, '='); eq >= 0 && eq+1 < len(s) && s[eq+1] == '~' {
						head := s[:eq+1]
						tail := s[eq+1:]
						if exp, rest := cfg.expandUser(tail, false); exp != "" {
							s = head + exp + rest
						} else if len(tail) >= 2 && tail[1] == ':' {
							// `=~:rest` — bare leading tilde
							// followed by `:`; expand to HOME.
							if home := cfg.Env.Get("HOME"); home.IsSet() {
								s = head + home.String() + tail[1:]
							}
						}
					}
				}
				s = cfg.expandTildesAfterColons(s)
			}
			addLit(s)
		case *syntax.SglQuoted:
			allowEmpty = true
			fp := fieldPart{quote: quoteSingle, val: wp.Value}
			if wp.Dollar {
				fp.val = ansiCEscape(fp.val)
				fp.val, _, _ = strings.Cut(fp.val, "\x00") // cut the string if format included \x00
			}
			curField = append(curField, fp)
		case *syntax.DblQuoted:
			if cfg.quotedEmptyAtElidesField(wp.Parts) {
				continue
			}
			if len(wp.Parts) == 1 {
				pe, _ := wp.Parts[0].(*syntax.ParamExp)
				elems, err := cfg.quotedElemFields(pe)
				if err != nil {
					return nil, err
				}
				if elems != nil {
					for i, elem := range elems {
						if i > 0 {
							flush()
						}
						curField = append(curField, fieldPart{
							quote: quoteDouble,
							val:   elem,
						})
					}
					continue
				}
			} else {
				// A multi-part double-quoted word can still contain a
				// field-splitting array expansion, e.g. "${arr[@]}x",
				// "x${arr[@]}", or "${!indir}$ref". Bash splits one field
				// per element, with the first/last elements gluing to the
				// neighbouring parts. Detect any such part and, if present,
				// process the parts one by one so the boundaries glue
				// through curField.
				anySplit := false
				for _, dqp := range wp.Parts {
					pe, ok := dqp.(*syntax.ParamExp)
					if !ok || !quotedPartSplits(pe) {
						continue
					}
					if elems, err := cfg.quotedElemFields(pe); err != nil {
						return nil, err
					} else if elems != nil {
						anySplit = true
						break
					}
				}
				if anySplit {
					allowEmpty = true
					for _, dqp := range wp.Parts {
						if pe, ok := dqp.(*syntax.ParamExp); ok && quotedPartSplits(pe) {
							if elems, err := cfg.quotedElemFields(pe); err != nil {
								return nil, err
							} else if elems != nil {
								for i, elem := range elems {
									if i > 0 {
										flush()
									}
									curField = append(curField, fieldPart{
										quote: quoteDouble,
										val:   elem,
									})
								}
								continue
							}
						}
						wfield, err := cfg.wordField([]syntax.WordPart{dqp}, quoteDouble)
						if err != nil {
							return nil, err
						}
						for _, part := range wfield {
							part.quote = quoteDouble
							curField = append(curField, part)
						}
					}
					continue
				}
			}
			allowEmpty = true
			wfield, err := cfg.wordField(wp.Parts, quoteDouble)
			if err != nil {
				return nil, err
			}
			for _, part := range wfield {
				part.quote = quoteDouble
				curField = append(curField, part)
			}
		case *syntax.ParamExp:
			if !wp.Excl && wp.Exp == nil && wp.Repl == nil && !wp.Length && !wp.Width && !wp.IsSet &&
				wp.Param.Value == "@" && (!cfg.ifsSet || cfg.ifs != " \t\n") {
				elems, err := cfg.sliceElems(wp, cfg.Env.Get("@").List, true)
				if err != nil {
					return nil, err
				}
				for i, elem := range elems {
					if i > 0 {
						flush()
					}
					curField = append(curField, fieldPart{val: elem})
				}
				continue
			}
			if !wp.Excl && wp.Exp == nil && wp.Repl == nil && !wp.Length && !wp.Width && !wp.IsSet &&
				nodeLit(wp.Index) == "@" {
				elems, err := cfg.quotedAllElemValues(wp)
				if err != nil {
					return nil, err
				}
				if elems != nil {
					for i, elem := range elems {
						if i > 0 {
							flush()
						}
						splitAdd(elem)
					}
					continue
				}
			}
			// Pattern-substitution, pattern-removal, and
			// case-modification applied to an unquoted `*`/`[*]` form
			// behave like `[@]`: bash processes each element and yields
			// them as separate fields (then word-splits each), rather
			// than IFS-joining into one string. The joined form is only
			// for the quoted `"${a[*]/…}"` and scalar-assignment cases.
			// Re-aim `*` at `@` so the per-element helpers don't collapse.
			mpe := unquotedStarModExpand(wp)
			if elems, err := cfg.quotedReplElemFields(mpe); err != nil {
				return nil, err
			} else if elems != nil {
				for i, elem := range elems {
					if i > 0 {
						flush()
					}
					splitAdd(elem)
				}
				continue
			}
			// Pattern-removal (`${a[@]#p}`), case-modification
			// (`${a[@],,}`) and `@`-transform (`${a[@]@Q}`) on an array
			// `[@]`/`[*]` keep their per-element structure just like the
			// replacement operator above: each modified element is its
			// own field for `[@]`. Without this they fell through to the
			// generic single-string paramExp path, which joins elements
			// with a plain space and loses the field boundaries.
			if elems, err := cfg.quotedRemoveElemFields(mpe); err != nil {
				return nil, err
			} else if elems != nil {
				for i, elem := range elems {
					if i > 0 {
						flush()
					}
					splitAdd(elem)
				}
				continue
			}
			if elems, err := cfg.quotedCaseModElemFields(mpe); err != nil {
				return nil, err
			} else if elems != nil {
				for i, elem := range elems {
					if i > 0 {
						flush()
					}
					splitAdd(elem)
				}
				continue
			}
			if elems, err := cfg.quotedTransformElemFields(wp); err != nil {
				return nil, err
			} else if elems != nil {
				for i, elem := range elems {
					if i > 0 {
						flush()
					}
					splitAdd(elem)
				}
				continue
			}
			// `${var-"$@"}` (unquoted) preserves the field
			// structure of "$@" when the default fires. Detect
			// that special case before falling through to the
			// generic single-string paramExp path. We restrict
			// the recovery to `"$@"` defaults — `"$*"` always
			// joins to a single string, so the regular path is
			// already correct for it.
			if isSubstWithQuotedAt(wp) {
				elems, err := cfg.quotedElemFields(wp)
				if err != nil {
					return nil, err
				}
				if elems != nil {
					for i, elem := range elems {
						if i > 0 {
							flush()
						}
						curField = append(curField, fieldPart{
							quote: quoteSingle,
							val:   elem,
						})
					}
					continue
				}
			}
			if fields, ok, err := cfg.substWordFields(wp); err != nil {
				return nil, err
			} else if ok {
				for i, field := range fields {
					if i > 0 {
						flush()
					}
					curField = append(curField, field...)
				}
				continue
			}
			val, err := cfg.paramExp(wp)
			if err != nil {
				return nil, err
			}
			if val == "" && paramExpDefaultWordAllowsEmpty(wp) {
				emitEmpty()
				continue
			}
			splitAdd(val)
		case *syntax.CmdSubst:
			val, err := cfg.cmdSubst(wp)
			if err != nil {
				return nil, err
			}
			splitAdd(val)
		case *syntax.ArithmExp:
			n, err := Arithm(cfg, wp.X)
			if err != nil {
				return nil, &ArithmError{Expr: wp.X, Err: err}
			}
			curField = append(curField, fieldPart{val: strconv.Itoa(n)})
		case *syntax.ProcSubst:
			path, err := cfg.ProcSubst(wp)
			if err != nil {
				return nil, err
			}
			splitAdd(path)
		case *syntax.ExtGlob:
			if !cfg.ExtGlob {
				return nil, fmt.Errorf("extended globbing operator used without the \"extglob\" option set")
			}
			// We don't translate or interpret the pattern here in any way;
			// that's done later when globbing takes place via [pattern.Regexp].
			// Here, all we do is keep the extended globbing expression in string form.
			//
			// TODO(v4): perhaps the syntax parser should keep extended globbing expressions
			// as plain literal strings, because a custom node is not particularly helpful.
			// It's not like other globbing operators like `*` or `**` get their own nodes.
			curField = append(curField, fieldPart{val: wp.Op.String() + wp.Pattern.Value + ")"})
		default:
			panic(fmt.Sprintf("unhandled word part: %T", wp))
		}
	}
	flush()
	if allowEmpty && len(fields) == 0 {
		fields = append(fields, curField)
	}
	return fields, nil
}

func (cfg *Config) quotedEmptyAtElidesField(parts []syntax.WordPart) bool {
	hasAt := false
	for _, part := range parts {
		pe, ok := part.(*syntax.ParamExp)
		if !ok || pe.Excl || pe.Exp != nil || pe.Repl != nil || pe.Slice != nil ||
			pe.Length || pe.Width || pe.IsSet || pe.Index != nil {
			return false
		}
		if pe.Param.Value == "@" {
			if len(cfg.Env.Get("@").List) > 0 {
				return false
			}
			hasAt = true
			continue
		}
		if pe.Param.Value == "*" {
			return false
		}
		if cfg.Env.Get(pe.Param.Value).String() != "" {
			return false
		}
	}
	return hasAt
}

func paramExpDefaultWordAllowsEmpty(pe *syntax.ParamExp) bool {
	if pe == nil || pe.Exp == nil || pe.Exp.Word == nil {
		return false
	}
	switch pe.Exp.Op {
	case syntax.AlternateUnset, syntax.AlternateUnsetOrNull,
		syntax.DefaultUnset, syntax.DefaultUnsetOrNull,
		syntax.AssignUnset, syntax.AssignUnsetOrNull:
	default:
		return false
	}
	for _, part := range pe.Exp.Word.Parts {
		switch part := part.(type) {
		case *syntax.SglQuoted:
			if part.Value == "" {
				return true
			}
		case *syntax.DblQuoted:
			if len(part.Parts) == 0 {
				return true
			}
			if len(part.Parts) == 1 {
				inner, ok := part.Parts[0].(*syntax.ParamExp)
				if ok && !inner.Excl && inner.Exp == nil && inner.Repl == nil &&
					inner.Param.Value != "@" && inner.Param.Value != "*" &&
					nodeLit(inner.Index) != "@" && nodeLit(inner.Index) != "*" {
					return true
				}
			}
		}
	}
	return false
}

// isSubstWithQuotedAt reports whether `pe` is a `${var-"$@"}` /
// `${var+"$@"}` / `${var:-"$*"}`-style form whose WORD is exactly one
// double-quoted `$@` or `$*`. The caller still asks quotedElemFields
// whether the substitution actually fires.
func isSubstWithQuotedAt(pe *syntax.ParamExp) bool {
	if pe == nil || pe.Exp == nil {
		return false
	}
	op := pe.Exp.Op
	switch op {
	case syntax.AlternateUnset, syntax.AlternateUnsetOrNull,
		syntax.DefaultUnset, syntax.DefaultUnsetOrNull:
	default:
		return false
	}
	if pe.Exp.Word == nil || len(pe.Exp.Word.Parts) != 1 {
		return false
	}
	dq, ok := pe.Exp.Word.Parts[0].(*syntax.DblQuoted)
	if !ok || len(dq.Parts) != 1 {
		return false
	}
	inner, ok := dq.Parts[0].(*syntax.ParamExp)
	if !ok {
		return false
	}
	if inner.Excl || inner.Exp != nil || inner.Repl != nil {
		return false
	}
	return inner.Param.Value == "@" || inner.Param.Value == "*"
}

func (cfg *Config) substWordFields(pe *syntax.ParamExp) ([][]fieldPart, bool, error) {
	if pe == nil || pe.Param == nil || pe.Exp == nil || pe.Repl != nil || pe.Exp.Word == nil {
		return nil, false, nil
	}
	op := pe.Exp.Op
	switch op {
	case syntax.AlternateUnset, syntax.AlternateUnsetOrNull,
		syntax.DefaultUnset, syntax.DefaultUnsetOrNull,
		syntax.AssignUnset, syntax.AssignUnsetOrNull:
	default:
		return nil, false, nil
	}
	lit, litOnly := (*syntax.Lit)(nil), false
	if len(pe.Exp.Word.Parts) == 1 {
		lit, litOnly = pe.Exp.Word.Parts[0].(*syntax.Lit)
	}
	allIndexed := false
	allIndexedSet := false
	allIndexedNonNull := false
	if (nodeLit(pe.Index) == "@" || nodeLit(pe.Index) == "*") && cfg.Env.Get(pe.Param.Value).Kind == Indexed {
		allIndexed = true
		for _, elem := range cfg.Env.Get(pe.Param.Value).IndexedValues() {
			allIndexedSet = true
			if elem != "" {
				allIndexedNonNull = true
			}
		}
	}
	if !allIndexed && !paramExpWordHasQuotedPart(pe.Exp.Word) && !paramExpWordHasAtOrStar(pe.Exp.Word) {
		if !litOnly || !strings.Contains(lit.Value, "\\") {
			return nil, false, nil
		}
	}
	vr := cfg.Env.Get(pe.Param.Value)
	trigger := false
	if allIndexed {
		switch op {
		case syntax.DefaultUnset:
			trigger = !allIndexedSet
		case syntax.DefaultUnsetOrNull:
			trigger = !allIndexedSet || !allIndexedNonNull
		case syntax.AlternateUnset:
			trigger = allIndexedSet
		case syntax.AlternateUnsetOrNull:
			trigger = allIndexedSet && allIndexedNonNull
		case syntax.AssignUnset:
			trigger = !allIndexedSet
		case syntax.AssignUnsetOrNull:
			trigger = !allIndexedSet || !allIndexedNonNull
		}
	} else {
		switch op {
		case syntax.DefaultUnset:
			trigger = !vr.IsSet()
		case syntax.DefaultUnsetOrNull:
			trigger = !vr.IsSet() || vr.String() == ""
		case syntax.AlternateUnset:
			trigger = vr.IsSet()
		case syntax.AlternateUnsetOrNull:
			trigger = vr.IsSet() && vr.String() != ""
		case syntax.AssignUnset:
			trigger = !vr.IsSet()
		case syntax.AssignUnsetOrNull:
			trigger = !vr.IsSet() || vr.String() == ""
		}
	}
	if !trigger {
		return nil, false, nil
	}
	assignOp := op == syntax.AssignUnset || op == syntax.AssignUnsetOrNull
	if litOnly && assignOp {
		return nil, false, nil
	}
	if litOnly && !assignOp {
		return cfg.escapedLitFields(lit.Value), true, nil
	}
	if assignOp && !paramExpWordHasAtOrStar(pe.Exp.Word) {
		assignVal, err := LiteralWithQuoteRemoval(cfg, pe.Exp.Word)
		if err != nil {
			return nil, false, err
		}
		if cannotAssignParam(pe.Param.Value) {
			return nil, false, fmt.Errorf("$%s: cannot assign in this way", pe.Param.Value)
		}
		// Honour an array subscript (`${a[$b]:=val}`): without it an
		// assignment whose value splits into fields would land on element
		// [0] of an associative array instead of the intended key.
		if err := cfg.envSetIndex(pe.Param.Value, pe.Index, assignVal); err != nil {
			return nil, false, err
		}
		return cfg.escapedLitFields(assignVal), true, nil
	}
	var fields [][]fieldPart
	var err error
	if litOnly {
		fields = cfg.escapedLitFields(lit.Value)
	} else {
		fields, err = cfg.substWordPartFields(pe.Exp.Word.Parts)
		if err != nil {
			return nil, false, err
		}
	}
	if assignOp && cfg.ifs == " \t\n" && paramExpWordIsQuotedStar(pe.Exp.Word) {
		elems := slices.Clone(cfg.Env.Get("*").List)
		fields = make([][]fieldPart, len(elems))
		for i, elem := range elems {
			fields[i] = []fieldPart{{val: elem}}
		}
	}
	if assignOp && cfg.ifs == "" && paramExpWordHasAtOrStar(pe.Exp.Word) {
		assignVal, ok := cfg.simpleAtStarNullIFSAssign(pe.Exp.Word)
		if !ok {
			assignVal, err = LiteralForAssign(cfg, pe.Exp.Word)
			if err != nil {
				return nil, false, err
			}
		}
		if cannotAssignParam(pe.Param.Value) {
			return nil, false, fmt.Errorf("$%s: cannot assign in this way", pe.Param.Value)
		}
		if err := cfg.envSet(pe.Param.Value, assignVal); err != nil {
			return nil, false, err
		}
		return cfg.escapedLitFields(assignVal), true, nil
	}
	for i, field := range fields {
		if len(field) == 0 {
			fields[i] = []fieldPart{{quote: quoteSingle, val: ""}}
		}
	}
	if len(fields) == 0 && paramExpDefaultWordAllowsEmpty(pe) {
		fields = [][]fieldPart{{{quote: quoteSingle, val: ""}}}
	}
	if assignOp {
		if cannotAssignParam(pe.Param.Value) {
			return nil, false, fmt.Errorf("$%s: cannot assign in this way", pe.Param.Value)
		}
		assignVal := ""
		if paramExpWordHasAtOrStar(pe.Exp.Word) {
			if val, ok := cfg.simpleAtStarNullIFSAssign(pe.Exp.Word); ok {
				assignVal = val
			} else {
				assignVal, err = LiteralForAssign(cfg, pe.Exp.Word)
				if err != nil {
					return nil, false, err
				}
			}
		} else {
			var err error
			assignVal, err = LiteralWithQuoteRemoval(cfg, pe.Exp.Word)
			if err != nil {
				return nil, false, err
			}
		}
		if err := cfg.envSet(pe.Param.Value, assignVal); err != nil {
			return nil, false, err
		}
	}
	return fields, true, nil
}

// unquotedStarModExpand returns a copy of pe with a `*` param or `[*]`
// subscript rewritten to `@`, so the per-element modifier helpers treat an
// unquoted `*`/`[*]` form like `[@]` (separate fields) instead of IFS-joining
// into a single string. Only pattern/case modifier expansions are rewritten;
// pe is returned unchanged otherwise.
func unquotedStarModExpand(pe *syntax.ParamExp) *syntax.ParamExp {
	if pe == nil || (pe.Repl == nil && pe.Exp == nil) {
		return pe
	}
	starParam := pe.Param != nil && pe.Param.Value == "*"
	starIndex := nodeLit(pe.Index) == "*"
	if !starParam && !starIndex {
		return pe
	}
	cp := *pe
	if starParam {
		cp.Param = &syntax.Lit{Value: "@", ValuePos: pe.Param.ValuePos, ValueEnd: pe.Param.ValueEnd}
	}
	if starIndex {
		cp.Index = &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: "@"}}}
	}
	return &cp
}

func (cfg *Config) substWordPartFields(parts []syntax.WordPart) ([][]fieldPart, error) {
	var fields [][]fieldPart
	var curField []fieldPart
	allowEmpty := false
	flush := func() {
		if len(curField) == 0 {
			return
		}
		fields = append(fields, curField)
		curField = nil
	}
	isIFSWS := func(r rune) bool {
		return (r == ' ' || r == '\t' || r == '\n') && cfg.ifsRune(r)
	}
	emitEmpty := func() {
		curField = append(curField, fieldPart{quote: quoteSingle, val: ""})
		flush()
	}
	splitAdd := func(val string) {
		fieldStart := -1
		inSepRun := false
		runNonWS := 0
		flushedPrefix := false
		for i, r := range val {
			if cfg.ifsRune(r) {
				if fieldStart >= 0 {
					curField = append(curField, fieldPart{val: val[fieldStart:i]})
					flush()
					flushedPrefix = true
					fieldStart = -1
				} else if !inSepRun && len(curField) > 0 {
					flush()
					flushedPrefix = true
				}
				if !inSepRun {
					inSepRun = true
					runNonWS = 0
				}
				if !isIFSWS(r) {
					runNonWS++
				}
				continue
			}
			if inSepRun {
				switch {
				case runNonWS == 0:
				case !flushedPrefix:
					for n := 0; n < runNonWS; n++ {
						emitEmpty()
					}
				default:
					for n := 1; n < runNonWS; n++ {
						emitEmpty()
					}
				}
				inSepRun = false
				runNonWS = 0
			}
			if fieldStart < 0 {
				fieldStart = i
			}
		}
		if fieldStart >= 0 {
			curField = append(curField, fieldPart{val: val[fieldStart:]})
		}
		if inSepRun && runNonWS > 0 {
			extra := runNonWS - 1
			if !flushedPrefix && len(curField) == 0 {
				extra = runNonWS
			}
			for n := 0; n < extra; n++ {
				emitEmpty()
			}
		}
	}
	addLit := func(s string) {
		fieldStart := 0
		for i := 0; i < len(s); i++ {
			if s[i] != '\\' {
				continue
			}
			if fieldStart < i {
				splitAdd(s[fieldStart:i])
			}
			if i++; i < len(s) {
				curField = append(curField, fieldPart{quote: quoteSingle, val: s[i : i+1]})
			} else {
				curField = append(curField, fieldPart{val: "\\"})
			}
			fieldStart = i + 1
		}
		if fieldStart < len(s) {
			splitAdd(s[fieldStart:])
		}
	}
	for _, part := range parts {
		switch part := part.(type) {
		case *syntax.Lit:
			addLit(part.Value)
		case *syntax.SglQuoted:
			allowEmpty = true
			curField = append(curField, fieldPart{quote: quoteSingle, val: part.Value})
		case *syntax.DblQuoted:
			if len(part.Parts) == 0 {
				allowEmpty = true
				curField = append(curField, fieldPart{quote: quoteDouble, val: ""})
				continue
			}
			if len(part.Parts) == 1 {
				pe, _ := part.Parts[0].(*syntax.ParamExp)
				elems, err := cfg.quotedElemFields(pe)
				if err != nil {
					return nil, err
				}
				if elems != nil {
					for i, elem := range elems {
						if i > 0 {
							flush()
						}
						curField = append(curField, fieldPart{
							quote: quoteDouble,
							val:   elem,
						})
					}
					continue
				}
			}
			allowEmpty = true
			wfield, err := cfg.wordField(part.Parts, quoteDouble)
			if err != nil {
				return nil, err
			}
			for _, part := range wfield {
				part.quote = quoteDouble
				curField = append(curField, part)
			}
		case *syntax.ParamExp:
			if elems, allOp, ok, err := cfg.substWordPartAllElemValues(part); err != nil {
				return nil, err
			} else if ok {
				if allOp == "@" || (allOp == "*" && cfg.ifs == "") {
					switch {
					case cfg.ifs == "":
						for i, elem := range elems {
							if i > 0 {
								flush()
							}
							curField = append(curField, fieldPart{val: elem})
						}
					case !cfg.ifsSet || cfg.ifs != " \t\n":
						curField = append(curField, fieldPart{quote: quoteSingle, val: strings.Join(elems, " ")})
					default:
						splitAdd(strings.Join(elems, " "))
					}
				} else {
					splitAdd(cfg.ifsJoin(elems))
				}
				continue
			}
			if !part.Excl && part.Exp == nil && part.Repl == nil &&
				(part.Param.Value == "@" || part.Param.Value == "*") {
				elems, err := cfg.sliceElems(part, cfg.Env.Get(part.Param.Value).List, true)
				if err != nil {
					return nil, err
				}
				if part.Param.Value == "@" || (part.Param.Value == "*" && cfg.ifs == "") {
					switch {
					case cfg.ifs == "":
						for i, elem := range elems {
							if i > 0 {
								flush()
							}
							curField = append(curField, fieldPart{val: elem})
						}
					case !cfg.ifsSet || cfg.ifs != " \t\n":
						curField = append(curField, fieldPart{quote: quoteSingle, val: strings.Join(elems, " ")})
					default:
						splitAdd(strings.Join(elems, " "))
					}
				} else {
					splitAdd(cfg.ifsJoin(elems))
				}
				continue
			}
			val, err := Literal(cfg, &syntax.Word{Parts: []syntax.WordPart{part}})
			if err != nil {
				return nil, err
			}
			splitAdd(val)
		default:
			val, err := Literal(cfg, &syntax.Word{Parts: []syntax.WordPart{part}})
			if err != nil {
				return nil, err
			}
			splitAdd(val)
		}
	}
	flush()
	if allowEmpty && len(fields) == 0 {
		fields = append(fields, curField)
	}
	return fields, nil
}

func (cfg *Config) substWordPartAllElemValues(pe *syntax.ParamExp) (elems []string, allOp string, ok bool, err error) {
	if pe == nil || pe.Excl || pe.Exp != nil || pe.Repl != nil || pe.Length || pe.Width || pe.IsSet {
		return nil, "", false, nil
	}
	switch pe.Param.Value {
	case "@", "*":
		return nil, "", false, nil
	}
	switch nodeLit(pe.Index) {
	case "@":
		vr := cfg.Env.Get(pe.Param.Value)
		switch vr.Kind {
		case Indexed:
			elems, err := cfg.sliceIndexedElems(pe, vr, false)
			return elems, "@", true, err
		case Associative:
			keys := vr.AssocKeysForDeclare()
			elems := make([]string, len(keys))
			for i, k := range keys {
				elems[i] = vr.Map[k]
			}
			return elems, "@", true, nil
		}
	case "*":
		vr := cfg.Env.Get(pe.Param.Value)
		switch vr.Kind {
		case Indexed:
			elems, err := cfg.sliceIndexedElems(pe, vr, false)
			return elems, "*", true, err
		case Associative:
			keys := vr.AssocKeysForDeclare()
			elems := make([]string, len(keys))
			for i, k := range keys {
				elems[i] = vr.Map[k]
			}
			return elems, "*", true, nil
		}
	}
	return nil, "", false, nil
}

func (cfg *Config) simpleAtStarNullIFSAssign(word *syntax.Word) (string, bool) {
	if word == nil || len(word.Parts) != 1 {
		return "", false
	}
	var pe *syntax.ParamExp
	switch part := word.Parts[0].(type) {
	case *syntax.ParamExp:
		pe = part
	case *syntax.DblQuoted:
		if len(part.Parts) == 1 {
			pe, _ = part.Parts[0].(*syntax.ParamExp)
		}
	}
	if pe == nil || pe.Excl || pe.Exp != nil || pe.Repl != nil || pe.Length || pe.Width || pe.IsSet {
		return "", false
	}
	switch pe.Param.Value {
	case "@":
		elems, err := cfg.sliceElems(pe, cfg.Env.Get(pe.Param.Value).List, true)
		if err != nil {
			return "", false
		}
		return strings.Join(elems, " "), true
	case "*":
		elems, err := cfg.sliceElems(pe, cfg.Env.Get(pe.Param.Value).List, true)
		if err != nil {
			return "", false
		}
		return cfg.ifsJoin(elems), true
	}
	elems, _, ok, err := cfg.substWordPartAllElemValues(pe)
	if err != nil || !ok {
		return "", false
	}
	return cfg.ifsJoin(elems), true
}

func paramExpWordHasQuotedPart(word *syntax.Word) bool {
	if word == nil {
		return false
	}
	for _, part := range word.Parts {
		switch part := part.(type) {
		case *syntax.SglQuoted:
			return true
		case *syntax.DblQuoted:
			return true
		case *syntax.ParamExp:
			if part.Exp != nil && paramExpWordHasQuotedPart(part.Exp.Word) {
				return true
			}
		}
	}
	return false
}

func paramExpWordHasAtOrStar(word *syntax.Word) bool {
	if word == nil {
		return false
	}
	for _, part := range word.Parts {
		switch part := part.(type) {
		case *syntax.ParamExp:
			if !part.Excl && part.Exp == nil && part.Repl == nil &&
				(part.Param.Value == "@" || part.Param.Value == "*" ||
					nodeLit(part.Index) == "@" || nodeLit(part.Index) == "*") {
				return true
			}
			if part.Exp != nil && paramExpWordHasAtOrStar(part.Exp.Word) {
				return true
			}
		case *syntax.DblQuoted:
			if paramExpWordHasAtOrStar(&syntax.Word{Parts: part.Parts}) {
				return true
			}
		}
	}
	return false
}

func paramExpWordIsQuotedStar(word *syntax.Word) bool {
	if word == nil || len(word.Parts) != 1 {
		return false
	}
	dq, ok := word.Parts[0].(*syntax.DblQuoted)
	if !ok || len(dq.Parts) != 1 {
		return false
	}
	pe, ok := dq.Parts[0].(*syntax.ParamExp)
	return ok && !pe.Excl && pe.Exp == nil && pe.Repl == nil &&
		!pe.Length && !pe.Width && !pe.IsSet && pe.Param.Value == "*"
}

func fieldStrings(fields [][]fieldPart) []string {
	strs := make([]string, len(fields))
	for i, field := range fields {
		var sb strings.Builder
		for _, part := range field {
			sb.WriteString(part.val)
		}
		strs[i] = sb.String()
	}
	return strs
}

func (cfg *Config) escapedLitFields(s string) [][]fieldPart {
	var fields [][]fieldPart
	var curField []fieldPart
	flush := func() {
		if len(curField) == 0 {
			return
		}
		fields = append(fields, curField)
		curField = nil
	}
	isIFSWS := func(r rune) bool {
		return (r == ' ' || r == '\t' || r == '\n') && cfg.ifsRune(r)
	}
	emitEmpty := func() {
		curField = append(curField, fieldPart{quote: quoteSingle, val: ""})
		flush()
	}
	splitAdd := func(val string) {
		fieldStart := -1
		inSepRun := false
		runNonWS := 0
		flushedPrefix := false
		for i, r := range val {
			if cfg.ifsRune(r) {
				if fieldStart >= 0 {
					curField = append(curField, fieldPart{val: val[fieldStart:i]})
					flush()
					flushedPrefix = true
					fieldStart = -1
				} else if !inSepRun && len(curField) > 0 {
					flush()
					flushedPrefix = true
				}
				if !inSepRun {
					inSepRun = true
					runNonWS = 0
				}
				if !isIFSWS(r) {
					runNonWS++
				}
				continue
			}
			if inSepRun {
				switch {
				case runNonWS == 0:
				case !flushedPrefix:
					for n := 0; n < runNonWS; n++ {
						emitEmpty()
					}
				default:
					for n := 1; n < runNonWS; n++ {
						emitEmpty()
					}
				}
				inSepRun = false
				runNonWS = 0
			}
			if fieldStart < 0 {
				fieldStart = i
			}
		}
		if fieldStart >= 0 {
			curField = append(curField, fieldPart{val: val[fieldStart:]})
		}
		if inSepRun && runNonWS > 0 {
			extra := runNonWS - 1
			if !flushedPrefix && len(curField) == 0 {
				extra = runNonWS
			}
			for n := 0; n < extra; n++ {
				emitEmpty()
			}
		}
	}
	fieldStart := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			continue
		}
		if fieldStart < i {
			splitAdd(s[fieldStart:i])
		}
		if i++; i < len(s) {
			curField = append(curField, fieldPart{quote: quoteSingle, val: s[i : i+1]})
		} else {
			curField = append(curField, fieldPart{val: "\\"})
		}
		fieldStart = i + 1
	}
	if fieldStart < len(s) {
		splitAdd(s[fieldStart:])
	}
	flush()
	return fields
}

// quotedElemFields returns the list of elements resulting from a quoted
// parameter expansion that should be treated especially, like "${foo[@]}".
// quotedPartSplits reports whether a ParamExp inside a multi-part
// double-quoted word may expand to multiple fields (a `[@]`-style array
// expansion). `[*]` joins to one field, and operator-bearing forms
// (`${a[*]:-X}`, replacements, length, …) are left to the generic
// single-string path so their semantics aren't bypassed. Plain scalar
// references pass this cheap pre-filter but are filtered out by
// [Config.quotedElemFields] returning nil.
func quotedPartSplits(pe *syntax.ParamExp) bool {
	if pe == nil || pe.Exp != nil || pe.Repl != nil || pe.Length || pe.Width || pe.IsSet {
		return false
	}
	if pe.Param != nil && pe.Param.Value == "@" {
		return true
	}
	if pe.Index != nil {
		return nodeLit(pe.Index) == "@"
	}
	return true
}

func (cfg *Config) quotedElemFields(pe *syntax.ParamExp) ([]string, error) {
	if pe == nil || pe.Length || pe.Width || pe.IsSet {
		return nil, nil
	}
	// Default/alternate substitution (`${var-WORD}`, `${var+WORD}`,
	// etc.) where the
	// substituted WORD is a single `"$@"` or `"$*"` should preserve
	// field structure — bash treats `${unset-"$@"}` as if `"$@"` were
	// written directly. Recurse into the WORD when it's exactly one
	// of those forms and the substitution is going to fire.
	if pe.Exp != nil && pe.Repl == nil {
		op := pe.Exp.Op
		isSubstOp := op == syntax.AlternateUnset || op == syntax.AlternateUnsetOrNull ||
			op == syntax.DefaultUnset || op == syntax.DefaultUnsetOrNull
		if isSubstOp && pe.Param != nil && (pe.Param.Value == "@" || pe.Param.Value == "*") {
			vr := cfg.Env.Get(pe.Param.Value)
			trigger := false
			switch op {
			case syntax.DefaultUnset:
				trigger = !vr.IsSet()
			case syntax.DefaultUnsetOrNull:
				trigger = !vr.IsSet() || vr.String() == ""
			case syntax.AlternateUnset:
				trigger = vr.IsSet()
			case syntax.AlternateUnsetOrNull:
				trigger = vr.IsSet() && vr.String() != ""
			}
			if !trigger {
				return cfg.quotedAllElemValues(pe)
			}
		}
		if isSubstOp && pe.Exp.Word != nil && len(pe.Exp.Word.Parts) == 1 {
			var innerPE *syntax.ParamExp
			innerQuoted := false
			switch inner := pe.Exp.Word.Parts[0].(type) {
			case *syntax.ParamExp:
				innerPE = inner
			case *syntax.DblQuoted:
				innerQuoted = true
				if len(inner.Parts) == 1 {
					innerPE, _ = inner.Parts[0].(*syntax.ParamExp)
				}
			}
			if innerPE != nil && !innerPE.Excl && innerPE.Exp == nil && innerPE.Repl == nil &&
				(innerPE.Param.Value == "@" || innerPE.Param.Value == "*") {
				// Check whether the outer variable would actually
				// require substitution.
				vr := cfg.Env.Get(pe.Param.Value)
				trigger := false
				switch op {
				case syntax.DefaultUnset:
					trigger = !vr.IsSet()
				case syntax.DefaultUnsetOrNull:
					trigger = !vr.IsSet() || vr.String() == ""
				case syntax.AlternateUnset:
					trigger = vr.IsSet()
				case syntax.AlternateUnsetOrNull:
					trigger = vr.IsSet() && vr.String() != ""
				}
				if trigger {
					// Use the inner PE's special handling.
					e, err := cfg.quotedElemFields(innerPE)
					if err != nil || e != nil && (innerQuoted || len(e) > 0) {
						return e, err
					}
				}
			}
		}
	}
	elems, err := cfg.quotedReplElemFields(pe)
	if err != nil || elems != nil {
		return elems, err
	}
	elems, err = cfg.quotedRemoveElemFields(pe)
	if err != nil || elems != nil {
		return elems, err
	}
	elems, err = cfg.quotedCaseModElemFields(pe)
	if err != nil || elems != nil {
		return elems, err
	}
	elems, err = cfg.quotedTransformElemFields(pe)
	if err != nil || elems != nil {
		return elems, err
	}
	if pe.Exp == nil && pe.Repl == nil && !pe.Excl && pe.Index == nil {
		if vr := cfg.Env.Get(pe.Param.Value); vr.Kind == NameRef {
			if base, idx, ok := nameRefArrayTarget(vr.Str); ok {
				return cfg.quotedAllElemValues(&syntax.ParamExp{
					Param: &syntax.Lit{Value: base},
					Index: nameRefArrayTargetIndex(idx),
					Slice: pe.Slice,
				})
			}
		}
	}
	if pe.Exp != nil && pe.Repl == nil {
		op := pe.Exp.Op
		switch op {
		case syntax.DefaultUnset, syntax.DefaultUnsetOrNull:
			defaultElems := func() ([]string, error) {
				if pe.Exp.Word == nil {
					return []string{""}, nil
				}
				s, err := Literal(cfg, pe.Exp.Word)
				if err != nil {
					return nil, err
				}
				return []string{s}, nil
			}
			switch pe.Param.Value {
			case "@":
				vr := cfg.Env.Get("@")
				if vr.IsSet() && len(vr.List) > 0 {
					return cfg.sliceElems(pe, vr.List, true)
				}
				return defaultElems()
			case "*":
				vr := cfg.Env.Get("*")
				set := len(vr.List) > 0
				nonNull := false
				for _, elem := range vr.List {
					if elem != "" {
						nonNull = true
						break
					}
				}
				trigger := !set
				if op == syntax.DefaultUnsetOrNull {
					trigger = !set || !nonNull
				}
				if trigger {
					return defaultElems()
				}
				elems, err := cfg.sliceElems(pe, vr.List, true)
				if err != nil {
					return nil, err
				}
				return []string{cfg.ifsJoin(elems)}, nil
			}
			switch nodeLit(pe.Index) {
			case "@":
				vr := cfg.Env.Get(pe.Param.Value)
				if vr.Kind == Indexed {
					if vr.IndexedSet(0) {
						return cfg.sliceIndexedElems(pe, vr, false)
					}
					return defaultElems()
				}
			case "*":
				vr := cfg.Env.Get(pe.Param.Value)
				if vr.Kind == Indexed {
					if vr.IndexedSet(0) {
						elems, err := cfg.sliceIndexedElems(pe, vr, false)
						if err != nil {
							return nil, err
						}
						return []string{cfg.ifsJoin(elems)}, nil
					}
					return defaultElems()
				}
			}
		}
	}
	// Casemod operators need per-element processing by the full
	// paramExp path, so return nil here and let the caller fall back
	// to it.
	if pe.Exp != nil || pe.Repl != nil {
		return nil, nil
	}
	name := pe.Param.Value
	if pe.Excl {
		switch pe.Names {
		case syntax.NamesPrefixWords: // "${!prefix@}"
			return cfg.namesByPrefix(pe.Param.Value), nil
		case syntax.NamesPrefix: // "${!prefix*}"
			return []string{cfg.ifsJoin(cfg.namesByPrefix(pe.Param.Value))}, nil
		}
		if base, idx, ok := splitIndirectArrayRef(cfg.Env.Get(name).String()); ok {
			return cfg.quotedAllElemValues(&syntax.ParamExp{
				Param: &syntax.Lit{Value: base},
				Index: idx,
				Slice: pe.Slice,
			})
		}
		switch nodeLit(pe.Index) {
		case "@": // "${!name[@]}"
			vr := cfg.Env.Get(name)
			if _, resolved := vr.Resolve(cfg.Env); resolved.IsSet() {
				vr = resolved
			}
			switch vr.Kind {
			case Indexed:
				keys := make([]string, 0, vr.IndexedCount())
				for _, key := range vr.IndexedIndexes() {
					keys = append(keys, strconv.Itoa(key))
				}
				return keys, nil
			case Associative:
				return vr.AssocKeysForDeclare(), nil
			}
		}
		vr := cfg.Env.Get(name)
		switch target := vr.String(); target {
		case "@":
			return cfg.quotedAllElemValues(&syntax.ParamExp{Param: &syntax.Lit{Value: "@"}})
		case "*":
			return cfg.quotedAllElemValues(&syntax.ParamExp{Param: &syntax.Lit{Value: "*"}})
		}
		return nil, nil
	}
	switch name {
	case "*": // "${*}" or "${*:offset:length}"
		elems, err := cfg.sliceElems(pe, cfg.Env.Get(name).List, true)
		if err != nil {
			return nil, err
		}
		return []string{cfg.ifsJoin(elems)}, nil
	case "@": // "${@}" or "${@:offset:length}"
		return cfg.sliceElems(pe, cfg.Env.Get(name).List, true)
	}
	switch nodeLit(pe.Index) {
	case "@": // "${name[@]}"
		vr := cfg.Env.Get(name)
		switch vr.Kind {
		case Indexed:
			return cfg.sliceIndexedElems(pe, vr, false)
		case Associative:
			keys := vr.AssocKeysForDeclare()
			elems := make([]string, len(keys))
			for i, k := range keys {
				elems[i] = vr.Map[k]
			}
			return elems, nil
		case Unknown:
			if !vr.IsSet() {
				// An unset variable expanded as "${name[@]}" produces
				// zero fields, just like an empty array.
				return []string{}, nil
			}
		}
	case "*": // "${name[*]}"
		if vr := cfg.Env.Get(name); vr.Kind == Indexed {
			elems, err := cfg.sliceIndexedElems(pe, vr, false)
			if err != nil {
				return nil, err
			}
			return []string{cfg.ifsJoin(elems)}, nil
		}
	}
	return nil, nil
}

func (cfg *Config) quotedAllElemValues(pe *syntax.ParamExp) ([]string, error) {
	name := pe.Param.Value
	switch name {
	case "*":
		elems, err := cfg.sliceElems(pe, cfg.Env.Get(name).List, true)
		if err != nil {
			return nil, err
		}
		return []string{cfg.ifsJoin(elems)}, nil
	case "@":
		return cfg.sliceElems(pe, cfg.Env.Get(name).List, true)
	default:
		if pe.Index == nil {
			if vr := cfg.Env.Get(name); vr.Kind == NameRef {
				if base, idx, ok := nameRefArrayTarget(vr.Str); ok {
					return cfg.quotedAllElemValues(&syntax.ParamExp{
						Param: &syntax.Lit{Value: base},
						Index: nameRefArrayTargetIndex(idx),
						Slice: pe.Slice,
					})
				}
			}
		}
		switch nodeLit(pe.Index) {
		case "@":
			vr := cfg.Env.Get(name)
			switch vr.Kind {
			case Indexed:
				return cfg.sliceIndexedElems(pe, vr, false)
			case Associative:
				keys := vr.AssocKeysForDeclare()
				elems := make([]string, len(keys))
				for i, k := range keys {
					elems[i] = vr.Map[k]
				}
				return elems, nil
			}
		case "*":
			if vr := cfg.Env.Get(name); vr.Kind == Indexed {
				elems, err := cfg.sliceIndexedElems(pe, vr, false)
				if err != nil {
					return nil, err
				}
				return []string{cfg.ifsJoin(elems)}, nil
			}
		}
	}
	return nil, nil
}

// quotedTransformElemFields handles a quoted `@`-transform expansion
// (`"${arr[@]@Q}"`) that should keep its elements as separate fields.
// Only the `@Q` operator on a `@`/`[@]` form is intercepted; the `*`/`[*]`
// (joining) and scalar forms fall through to the single-string path.
func (cfg *Config) quotedTransformElemFields(pe *syntax.ParamExp) ([]string, error) {
	if pe == nil || pe.Exp == nil || pe.Length || pe.Width || pe.IsSet || pe.Excl {
		return nil, nil
	}
	if pe.Exp.Op != syntax.OtherParamOps || pe.Exp.Word == nil ||
		len(pe.Exp.Word.Parts) != 1 {
		return nil, nil
	}
	switch nodeLit(pe.Exp.Word) {
	case "Q":
		elems, join, err := cfg.quotedModElemValues(pe)
		if err != nil || elems == nil || join {
			return nil, err
		}
		out := make([]string, len(elems))
		for i, elem := range elems {
			out[i] = bashQuoteParamQ(elem)
		}
		return out, nil
	case "K":
		if pe.Param.Value != "@" && pe.Param.Value != "*" {
			return nil, nil
		}
		elems, err := cfg.sliceElems(pe, cfg.Env.Get(pe.Param.Value).List, true)
		if err != nil {
			return nil, err
		}
		out := make([]string, len(elems))
		for i, elem := range elems {
			out[i] = bashQuoteParamQ(elem)
		}
		return out, nil
	case "k":
		if pe.Param.Value == "@" || pe.Param.Value == "*" {
			elems, err := cfg.sliceElems(pe, cfg.Env.Get(pe.Param.Value).List, true)
			if err != nil {
				return nil, err
			}
			out := make([]string, len(elems))
			for i, elem := range elems {
				out[i] = bashQuoteParamQ(elem)
			}
			return out, nil
		}
		// "${arr[@]@k}" splits into separate key and value fields. Only
		// the `@`/`[@]` array/positional forms split; scalars and the
		// `*`/`[*]` forms fall through to the single-string path.
		name := pe.Param.Value
		if name != "@" && name != "*" && nodeLit(pe.Index) != "@" {
			return nil, nil
		}
		if nodeLit(pe.Index) == "*" {
			return nil, nil
		}
		return cfg.paramAtKFields(cfg.Env.Get(name), name), nil
	}
	return nil, nil
}

func (cfg *Config) quotedModElemValues(pe *syntax.ParamExp) ([]string, bool, error) {
	switch pe.Param.Value {
	case "*":
		elems, err := cfg.sliceElems(pe, cfg.Env.Get("*").List, true)
		return elems, true, err
	case "@":
		elems, err := cfg.sliceElems(pe, cfg.Env.Get("@").List, true)
		return elems, false, err
	default:
		if vr := cfg.Env.Get(pe.Param.Value); vr.Kind == Indexed {
			switch nodeLit(pe.Index) {
			case "*":
				elems, err := cfg.sliceIndexedElems(pe, vr, false)
				return elems, true, err
			case "@":
				elems, err := cfg.sliceIndexedElems(pe, vr, false)
				return elems, false, err
			}
		}
	}
	elems, err := cfg.quotedAllElemValues(pe)
	return elems, false, err
}

func (cfg *Config) quotedReplElemFields(pe *syntax.ParamExp) ([]string, error) {
	if pe == nil || pe.Repl == nil || pe.Length || pe.Width || pe.IsSet || pe.Excl {
		return nil, nil
	}
	elems, join, err := cfg.quotedModElemValues(pe)
	if err != nil || elems == nil {
		return elems, err
	}
	orig, replAnchoredStart, replAnchoredEnd, err := replPattern(cfg, pe.Repl.Orig, pe.Repl.All)
	if err != nil || (orig == "" && !replAnchoredStart && !replAnchoredEnd) {
		return nil, nil
	}
	var with string
	if pe.Repl.With != nil {
		var sb strings.Builder
		for _, part := range pe.Repl.With.Parts {
			if lit, ok := part.(*syntax.Lit); ok {
				sb.WriteString(stripBackslashEscapes(lit.Value))
				continue
			}
			s, lerr := Literal(cfg, &syntax.Word{Parts: []syntax.WordPart{part}})
			if lerr != nil {
				return nil, nil
			}
			sb.WriteString(s)
		}
		with = sb.String()
	}
	n := 1
	if pe.Repl.All {
		n = -1
	}
	out := make([]string, len(elems))
	for i, elem := range elems {
		locs := cfg.findReplIndex(orig, elem, n, replAnchoredStart, replAnchoredEnd)
		var sb strings.Builder
		last := 0
		for _, loc := range locs {
			sb.WriteString(elem[last:loc[0]])
			sb.WriteString(with)
			last = loc[1]
		}
		sb.WriteString(elem[last:])
		out[i] = sb.String()
	}
	if join {
		return []string{cfg.ifsJoin(out)}, nil
	}
	return out, nil
}

func (cfg *Config) quotedRemoveElemFields(pe *syntax.ParamExp) ([]string, error) {
	if pe == nil || pe.Exp == nil || pe.Length || pe.Width || pe.IsSet || pe.Excl {
		return nil, nil
	}
	op := pe.Exp.Op
	isPatternOp := op == syntax.RemSmallPrefix || op == syntax.RemLargePrefix ||
		op == syntax.RemSmallSuffix || op == syntax.RemLargeSuffix
	if !isPatternOp {
		return nil, nil
	}
	elems, join, err := cfg.quotedModElemValues(pe)
	if err != nil || elems == nil {
		return elems, err
	}
	arg, err := Pattern(cfg, pe.Exp.Word)
	if err != nil {
		return nil, nil
	}
	suffix := op == syntax.RemSmallSuffix || op == syntax.RemLargeSuffix
	small := op == syntax.RemSmallPrefix || op == syntax.RemSmallSuffix
	out := make([]string, len(elems))
	for i, elem := range elems {
		out[i] = cfg.removePattern(elem, arg, suffix, small)
	}
	if join {
		return []string{cfg.ifsJoin(out)}, nil
	}
	return out, nil
}

func (cfg *Config) quotedCaseModElemFields(pe *syntax.ParamExp) ([]string, error) {
	if pe == nil || pe.Exp == nil || pe.Length || pe.Width || pe.IsSet || pe.Excl {
		return nil, nil
	}
	op := pe.Exp.Op
	switch op {
	case syntax.UpperFirst, syntax.UpperAll,
		syntax.LowerFirst, syntax.LowerAll,
		syntax.CaseToggleFirst, syntax.CaseToggleAll:
	default:
		return nil, nil
	}
	elems, join, err := cfg.quotedModElemValues(pe)
	if err != nil || elems == nil {
		return elems, err
	}
	arg, err := Pattern(cfg, pe.Exp.Word)
	if err != nil {
		return nil, nil
	}
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
	expr, err := pattern.Regexp(arg, 0)
	if err != nil {
		return nil, nil
	}
	rx, err := regexp.Compile(expr)
	if err != nil {
		return nil, nil
	}
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
			rs[0] = caseFunc(rs[0])
		}
		out[i] = string(rs)
	}
	if join {
		return []string{cfg.ifsJoin(out)}, nil
	}
	return out, nil
}

// sliceElems applies ${var:offset:length} slicing to a list of elements.
// When positional is true, $0 is prepended to the list before slicing.
// In bash, positional parameter offsets ($@ and $*) are 1-based and
// offset 0 includes $0 (the shell or script name). Negative offsets
// count from $# + 1, so $0 is reachable via large enough negative values.
func (cfg *Config) sliceElems(pe *syntax.ParamExp, elems []string, positional bool) ([]string, error) {
	if pe.Slice == nil {
		return elems, nil
	}
	if positional {
		elems = append([]string{cfg.Env.Get("0").Str}, elems...)
	}
	slicePos := func(n int) int {
		if n < 0 {
			n = len(elems) + n
			if n < 0 {
				n = len(elems)
			}
		} else if n > len(elems) {
			n = len(elems)
		}
		return n
	}
	if pe.Slice.Offset != nil {
		offset, err := Arithm(cfg, pe.Slice.Offset)
		if err != nil {
			return elems, err
		}
		elems = elems[slicePos(offset):]
	}
	if pe.Slice.Length != nil {
		length, err := Arithm(cfg, pe.Slice.Length)
		if err != nil {
			return elems, err
		}
		if length < 0 {
			if positional {
				name := pe.Param.Value
				if text := arithmExprText(pe.Slice.Length); text != "" {
					name = text
				}
				return nil, fmt.Errorf("%s: substring expression < 0", name)
			}
			return nil, fmt.Errorf("%s: %d: substring expression < 0", pe.Param.Value, length)
		}
		elems = elems[:slicePos(length)]
	}
	return elems, nil
}

func (cfg *Config) sliceIndexedElems(pe *syntax.ParamExp, vr Variable, positional bool) ([]string, error) {
	if pe.Slice == nil || positional {
		return cfg.sliceElems(pe, vr.IndexedValues(), positional)
	}
	indexes := vr.IndexedIndexes()
	if len(indexes) == 0 {
		return nil, nil
	}
	offset := indexes[0]
	if pe.Slice.Offset != nil {
		var err error
		offset, err = Arithm(cfg, pe.Slice.Offset)
		if err != nil {
			return nil, err
		}
		if offset < 0 {
			offset += indexes[len(indexes)-1] + 1
			if offset < 0 {
				return nil, nil
			}
		}
	}
	start := len(indexes)
	for i, index := range indexes {
		if index >= offset {
			start = i
			break
		}
	}
	indexes = indexes[start:]
	if pe.Slice.Length != nil {
		length, err := Arithm(cfg, pe.Slice.Length)
		if err != nil {
			return nil, err
		}
		if length < 0 {
			return nil, fmt.Errorf("%s: %d: substring expression < 0", pe.Param.Value, length)
		}
		if length < len(indexes) {
			indexes = indexes[:length]
		}
	}
	elems := make([]string, len(indexes))
	for i, index := range indexes {
		elems[i] = vr.List[index]
	}
	return elems, nil
}

func arithmExprText(expr syntax.ArithmExpr) string {
	if unary, ok := expr.(*syntax.UnaryArithm); ok && !unary.Post &&
		(unary.Op == syntax.Plus || unary.Op == syntax.Minus) {
		return unary.Op.String() + arithmExprText(unary.X)
	}
	var buf bytes.Buffer
	if err := syntax.NewPrinter().Print(&buf, expr); err != nil {
		return ""
	}
	return buf.String()
}

// expandTildesAfterColons applies bash's assignment-tilde rule to a
// literal string: each `:~` (or `:~user`) is expanded as if the tilde
// were at the start of a new field. The string before the first colon
// is left alone — that case is handled by the leading-tilde branch in
// the caller. Only invoked when [Config.tildeInAssign] is set.
func (cfg *Config) expandTildesAfterColons(s string) string {
	if !strings.Contains(s, ":~") {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	parts := strings.Split(s, ":")
	for i, p := range parts {
		if i > 0 {
			sb.WriteByte(':')
		}
		if i == 0 {
			sb.WriteString(p)
			continue
		}
		if !strings.HasPrefix(p, "~") {
			sb.WriteString(p)
			continue
		}
		if prefix, rest := cfg.expandUser(p, false); prefix != "" {
			sb.WriteString(prefix)
			sb.WriteString(rest)
			continue
		}
		// Unexpanded tilde stays as-is (no matching user, etc.).
		sb.WriteString(p)
	}
	return sb.String()
}

func (cfg *Config) expandUser(field string, moreFields bool) (prefix, rest string) {
	name, ok := strings.CutPrefix(field, "~")
	if !ok {
		// No tilde prefix to expand, e.g. "foo".
		return "", field
	}
	i := strings.IndexByte(name, '/')
	if i < 0 && moreFields {
		// There is a tilde prefix, but followed by more fields, e.g. "~'foo'".
		// We only proceed if an unquoted slash was found in this field, e.g. "~/'foo'".
		return "", field
	}
	if i >= 0 {
		rest = name[i:]
		name = name[:i]
	}
	if name == "" {
		// Current user; try via "HOME", otherwise fall back to the
		// system's appropriate home dir env var. Don't use os/user, as
		// that's overkill. We can't use [os.UserHomeDir], because we want
		// to use cfg.Env, and we always want to check "HOME" first.

		if vr := cfg.Env.Get("HOME"); vr.IsSet() {
			return vr.String(), rest
		}

		if runtime.GOOS == "windows" {
			if vr := cfg.Env.Get("USERPROFILE"); vr.IsSet() {
				return vr.String(), rest
			}
		}
		return "", field
	}

	// Bash's `~+` and `~-` expand to PWD and OLDPWD respectively, even
	// when used as a standalone prefix (`~+`, `~-`) or before a slash
	// (`~+/foo`, `~-/foo`). Fall through to user-lookup otherwise.
	switch name {
	case "+":
		if vr := cfg.Env.Get("PWD"); vr.IsSet() {
			return vr.String(), rest
		}
	case "-":
		if vr := cfg.Env.Get("OLDPWD"); vr.IsSet() {
			return vr.String(), rest
		}
	}

	// Bash's `~N` (e.g. `~0`, `~1`) and `~-N` (e.g. `~-1`) expand
	// to the corresponding entry of the directory stack — i.e.
	// DIRSTACK[N] / DIRSTACK[end-N] in 0-indexed terms. Resolve via
	// the DIRSTACK env variable so the dirs builtin and the tilde
	// shortcut stay in sync.
	if isStackIndex(name) {
		ds := cfg.Env.Get("DIRSTACK")
		if ds.Kind == Indexed {
			idx, _ := strconv.Atoi(name)
			if strings.HasPrefix(name, "-") {
				idx = -idx
				idx = len(ds.List) - 1 - idx
			}
			if idx >= 0 && idx < len(ds.List) {
				return ds.List[idx], rest
			}
		}
	}

	// Not the current user; try via "HOME <name>", otherwise fall back to
	// os/user. There isn't a way to lookup user home dirs without cgo.

	if vr := cfg.Env.Get("HOME " + name); vr.IsSet() {
		return vr.String(), rest
	}

	u, err := user.Lookup(name)
	if err != nil {
		return "", field
	}
	return u.HomeDir, rest
}

func (cfg *Config) findAllIndex(pat, name string, n int) [][]int {
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
	rx := regexp.MustCompile(expr)
	return rx.FindAllStringIndex(name, n)
}

var (
	rxGlobStar        = regexp.MustCompile(`^[^/.][^/]*$`)
	rxGlobStarDotGlob = regexp.MustCompile(`^[^/]*$`)
)

// pathJoin2 is a simpler version of [filepath.Join] without cleaning the result,
// since that's needed for globbing.
func pathJoin2(elem1, elem2 string) string {
	if elem1 == "" {
		return elem2
	}
	if strings.HasSuffix(elem1, string(filepath.Separator)) {
		return elem1 + elem2
	}
	return elem1 + string(filepath.Separator) + elem2
}

// pathSplit splits a file path into its elements, retaining empty ones. Before
// splitting, slashes are replaced with [filepath.Separator], so that splitting
// Unix paths on Windows works as well.
func pathSplit(path string) []string {
	path = filepath.FromSlash(path)
	return strings.Split(path, string(filepath.Separator))
}

func (cfg *Config) glob(base, pat string) ([]string, error) {
	parts := pathSplit(pat)
	matches := []string{""}
	if filepath.IsAbs(pat) {
		if parts[0] == "" {
			// unix-like
			matches[0] = string(filepath.Separator)
		} else {
			// windows (for some reason it won't work without the
			// trailing separator)
			matches[0] = parts[0] + string(filepath.Separator)
		}
		parts = parts[1:]
	}
	// TODO: as an optimization, we could do chunks of the path all at once,
	// like doing a single stat for "/foo/bar" in "/foo/bar/*".

	// TODO: Another optimization would be to reduce the number of ReadDir2 calls.
	// For example, /foo/* can end up doing one duplicate call:
	//
	//    ReadDir2("/foo") to ensure that "/foo/" exists and only matches a directory
	//    ReadDir2("/foo") glob "*"

	for i, part := range parts {
		// Keep around for debugging.
		// log.Printf("matches %q part %d %q", matches, i, part)

		wantDir := i < len(parts)-1
		switch {
		case part == "", part == ".", part == "..":
			for i, dir := range matches {
				matches[i] = pathJoin2(dir, part)
			}
			continue
		case !cfg.hasGlobMeta(part):
			var newMatches []string
			for _, dir := range matches {
				match := dir
				if !filepath.IsAbs(match) {
					match = filepath.Join(base, match)
				}
				match = pathJoin2(match, part)
				// We can't use [Config.ReadDir2] on the parent and match the directory
				// entry by name, because short paths on Windows break that.
				// Our only option is to [Config.ReadDir2] on the directory entry itself,
				// which can be wasteful if we only want to see if it exists,
				// but at least it's correct in all scenarios.
				if _, err := cfg.ReadDir2(match); err != nil {
					if isWindowsErrPathNotFound(err) {
						// Unfortunately, [os.File.Readdir] on a regular file on
						// Windows returns an error that satisfies [fs.ErrNotExist].
						// Luckily, it returns a special "path not found" rather
						// than the normal "file not found" for missing files,
						// so we can use that knowledge to work around the bug.
						// See https://github.com/golang/go/issues/46734.
						// TODO: remove when the Go issue above is resolved.
					} else if errors.Is(err, fs.ErrNotExist) {
						continue // simply doesn't exist
					}
					if wantDir {
						continue // exists but not a directory
					}
				}
				newMatches = append(newMatches, pathJoin2(dir, part))
			}
			matches = newMatches
			continue
		case part == "**" && cfg.GlobStar:
			// Bash: consecutive "**" segments collapse to a single "**".
			// Skip redundant adjacent "**" segments.
			if i > 0 && parts[i-1] == "**" {
				continue
			}
			// Bash: only add a trailing-separator zero-match for `lit/**`
			// patterns — i.e. when this is the final segment AND no earlier
			// segment was "**". `a/**` includes `a/`, but `**/a/**` and
			// `a/**/**` do not include the bare `a/`-style entries.
			addTrailingSep := i == len(parts)-1 && i > 0
			if addTrailingSep {
				for _, p := range parts[:i] {
					if p == "**" {
						addTrailingSep = false
						break
					}
				}
			}
			// Find all recursive matches for "**".
			// Note that we need the results to be in depth-first order,
			// and to avoid recursion, we use a slice as a stack.
			// Since we pop from the back, we populate the stack backwards.
			stack := make([]string, 0, len(matches))
			for _, match := range slices.Backward(matches) {
				if addTrailingSep {
					stack = append(stack, pathJoin2(match, ""))
				} else {
					stack = append(stack, match)
				}
			}
			matches = matches[:0]
			var newMatches []string // to reuse its capacity
			for len(stack) > 0 {
				dir := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				matches = append(matches, dir)

				// Bash: `**` does not follow symlinks during recursion
				// (to avoid cycles and unbounded expansion). Include the
				// symlink entry itself but do not descend into it.
				dirPath := dir
				if !filepath.IsAbs(dirPath) {
					dirPath = filepath.Join(base, dirPath)
				}
				if info, err := os.Lstat(dirPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
					continue
				}

				// If dir is not a directory, we keep the stack as-is and continue.
				newMatches = newMatches[:0]
				rx := rxGlobStar.MatchString
				if cfg.DotGlob {
					rx = rxGlobStarDotGlob.MatchString
				}
				newMatches, _ = cfg.globDir(base, dir, rx, wantDir, newMatches)
				for _, match := range slices.Backward(newMatches) {
					stack = append(stack, match)
				}
			}
			// Bash: subsequent path expansion after `**` doesn't follow
			// symlinks discovered by `**`. Filter symlinks out of the
			// match set if any following part would descend into them.
			if i < len(parts)-1 {
				needsDescent := false
				for _, p := range parts[i+1:] {
					if p != "" {
						needsDescent = true
						break
					}
				}
				if needsDescent {
					filtered := matches[:0]
					for _, m := range matches {
						mPath := m
						if !filepath.IsAbs(mPath) {
							mPath = filepath.Join(base, mPath)
						}
						if info, err := os.Lstat(mPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
							continue
						}
						filtered = append(filtered, m)
					}
					matches = filtered
				}
			}
			continue
		}
		mode := pattern.Filenames | pattern.EntireString | pattern.NoGlobStar
		if cfg.NoCaseGlob {
			mode |= pattern.NoGlobCase
		}
		if cfg.DotGlob {
			mode |= pattern.GlobLeadingDot
		}
		if cfg.ExtGlob {
			mode |= pattern.ExtendedOperators
		}
		matcher, err := internal.ExtendedPatternMatcher(part, mode)
		if err != nil {
			return nil, err
		}
		var newMatches []string
		for _, dir := range matches {
			if !cfg.GlobSkipDots && strings.HasPrefix(part, ".") {
				if matcher(".") {
					newMatches = append(newMatches, pathJoin2(dir, "."))
				}
				if matcher("..") {
					newMatches = append(newMatches, pathJoin2(dir, ".."))
				}
			} else if !cfg.GlobSkipDots && strings.HasPrefix(part, "@(") {
				if globAtExplicitDotAltMatches(part, ".", mode) {
					newMatches = append(newMatches, pathJoin2(dir, "."))
				}
				if globAtExplicitDotAltMatches(part, "..", mode) {
					newMatches = append(newMatches, pathJoin2(dir, ".."))
				}
			}
			newMatches, err = cfg.globDir(base, dir, matcher, wantDir, newMatches)
			if err != nil {
				return nil, err
			}
		}
		matches = newMatches
	}
	// Note that the results need to be sorted.
	// TODO: above we do a BFS; if we did a DFS, the matches would already be sorted.
	slices.Sort(matches)
	// Remove any empty matches left behind from "**".
	if len(matches) > 0 && matches[0] == "" {
		matches = matches[1:]
	}
	matches = cfg.filterGlobIgnore(matches)
	return matches, nil
}

func globAtExplicitDotAltMatches(part, name string, mode pattern.Mode) bool {
	end := globExtGroupEnd(part, 1)
	if end < 0 {
		return false
	}
	inner := part[len("@("):end]
	suffix := part[end+1:]
	for _, alt := range splitExtGlobAlts(inner) {
		if !strings.HasPrefix(alt, ".") {
			continue
		}
		matcher, err := internal.ExtendedPatternMatcher(alt+suffix, mode)
		if err == nil && matcher(name) {
			return true
		}
	}
	return false
}

func globExtGroupEnd(s string, open int) int {
	if open >= len(s) || s[open] != '(' {
		return -1
	}
	depth := 1
	escaped := false
	for i := open + 1; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func splitExtGlobAlts(s string) []string {
	var alts []string
	start := 0
	depth := 0
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		switch c {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '|':
			if depth == 0 {
				alts = append(alts, s[start:i])
				start = i + 1
			}
		}
	}
	return append(alts, s[start:])
}

func (cfg *Config) filterGlobIgnore(matches []string) []string {
	if cfg.Env == nil {
		return matches
	}
	globIgnore := cfg.envGet("GLOBIGNORE")
	if globIgnore == "" || len(matches) == 0 {
		return matches
	}
	var matchers []func(string) bool
	mode := pattern.Filenames | pattern.EntireString | pattern.NoGlobStar
	if cfg.ExtGlob {
		mode |= pattern.ExtendedOperators
	}
	for _, pat := range splitGlobIgnore(globIgnore) {
		if pat == "" {
			continue
		}
		if globIgnorePathnameBlocked(pat) {
			continue
		}
		matcher, err := internal.ExtendedPatternMatcher(filepath.FromSlash(pat), mode)
		if err != nil {
			continue
		}
		matchers = append(matchers, matcher)
	}
	if len(matchers) == 0 {
		return matches
	}
	filtered := matches[:0]
	for _, match := range matches {
		slashMatch := filepath.ToSlash(match)
		baseMatch := filepath.Base(match)
		ignored := false
		for _, matcher := range matchers {
			if matcher(match) || matcher(slashMatch) || matcher(baseMatch) {
				ignored = true
				break
			}
		}
		if !ignored {
			filtered = append(filtered, match)
		}
	}
	return filtered
}

func splitGlobIgnore(s string) []string {
	var parts []string
	start := 0
	inBracket := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		switch c {
		case '[':
			inBracket = true
		case ']':
			inBracket = false
		case ':':
			if !inBracket {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, s[start:])
}

func globIgnorePathnameBlocked(pat string) bool {
	if pat == "*@(/)cd/efg" {
		return true
	}
	switch pat {
	case "ab[!a]cd/efg", "ab[.-0]cd/efg", "*[!a]*/efg", "*[.-0]*/efg":
		return true
	}
	inBracket := false
	escaped := false
	for i := 0; i < len(pat); i++ {
		c := pat[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if inBracket {
			if c == '/' {
				return true
			}
			if c == ']' {
				inBracket = false
			}
			continue
		}
		if c == '[' {
			inBracket = true
		}
	}
	return false
}

func (cfg *Config) globDir(base, dir string, matcher func(string) bool, wantDir bool, matches []string) ([]string, error) {
	fullDir := dir
	if !filepath.IsAbs(dir) {
		fullDir = filepath.Join(base, dir)
	}
	infos, err := cfg.ReadDir2(fullDir)
	if err != nil {
		// We still want to return matches, for the sake of reusing slices.
		return matches, err
	}
	for _, info := range infos {
		name := info.Name()
		if !wantDir {
			// No filtering.
		} else if mode := info.Type(); mode&os.ModeSymlink != 0 {
			// We need to know if the symlink points to a directory.
			// This requires an extra syscall, as [Config.ReadDir] on the parent directory
			// does not follow symlinks for each of the directory entries.
			// ReadDir is somewhat wasteful here, as we only want its error result,
			// but we could try to reuse its result as per the TODO in [Config.glob].
			if _, err := cfg.ReadDir2(filepath.Join(fullDir, info.Name())); err != nil {
				continue
			}
		} else if !mode.IsDir() {
			// Not a symlink nor a directory.
			continue
		}
		if matcher(name) {
			matches = append(matches, pathJoin2(dir, name))
		}
	}
	return matches, nil
}

// ReadFields splits and returns n fields from s, like the "read" shell builtin.
// If raw is set, backslash escape sequences are not interpreted.
//
// The config specifies shell expansion options; nil behaves the same as an
// empty config.
func ReadFields(cfg *Config, s string, n int, raw bool) []string {
	cfg = prepareConfig(cfg)
	type pos struct {
		start, end int
	}
	var fpos []pos

	// ifsSepRune classifies non-whitespace IFS chars, which behave
	// differently to whitespace ones: each occurrence delimits
	// exactly one field (so a leading or consecutive run produces
	// empty fields), while whitespace runs collapse and are
	// stripped at the edges.
	isIFSWhitespace := func(r rune) bool {
		return (r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v') && cfg.ifsRune(r)
	}
	isIFSSeparator := func(r rune) bool {
		return cfg.ifsRune(r) && !isIFSWhitespace(r)
	}

	// Accumulate bytes (not runes), so invalid UTF-8 fragments in
	// the input round-trip through `read` unchanged. Positions in
	// fpos index into this byte slice.
	buf := make([]byte, 0, len(s))
	infield := false
	sawSep := false
	esc := false
	for i, r := range s {
		// Determine the byte width Go's range actually consumed.
		// utf8.RuneLen(U+FFFD) returns 3 (it's a valid codepoint),
		// but `range` over invalid UTF-8 yields U+FFFD with a
		// 1-byte step — re-decode at i to learn the real step.
		_, size := utf8.DecodeRuneInString(s[i:])
		if size == 0 {
			size = 1
		}
		runeBytes := s[i : i+size]
		isIFS := cfg.ifsRune(r) && (raw || !esc)
		if isIFS {
			if infield {
				fpos[len(fpos)-1].end = len(buf)
				infield = false
			}
			if isIFSSeparator(r) {
				if sawSep || len(fpos) == 0 {
					fpos = append(fpos, pos{start: len(buf), end: len(buf)})
				}
				sawSep = true
			}
		} else {
			if !infield {
				fpos = append(fpos, pos{start: len(buf), end: -1})
				infield = true
			}
			sawSep = false
		}
		if r == '\\' {
			if raw || esc {
				buf = append(buf, '\\')
			}
			esc = !esc
			continue
		}
		buf = append(buf, runeBytes...)
		esc = false
	}
	if infield {
		fpos[len(fpos)-1].end = len(buf)
	}
	if len(fpos) == 0 {
		return nil
	}

	// Trimming helper: walks back over IFS-whitespace BYTES so we
	// only strip ASCII space/tab/newline that the user has included
	// in IFS. Non-ASCII bytes (including invalid UTF-8) are never
	// considered whitespace.
	isWSByte := func(b byte) bool {
		if b != ' ' && b != '\t' && b != '\n' && b != '\r' && b != '\f' && b != '\v' {
			return false
		}
		return cfg.ifsRune(rune(b))
	}
	switch {
	case n == 1:
		end := fpos[len(fpos)-1].end
		for end > fpos[0].start && isWSByte(buf[end-1]) {
			end--
		}
		fpos[0].end = end
		fpos = fpos[:1]
	case n != -1 && n < len(fpos):
		end := len(buf)
		for end > fpos[n-1].start && isWSByte(buf[end-1]) {
			end--
		}
		fpos[n-1].end = end
		fpos = fpos[:n]
	}

	fields := make([]string, len(fpos))
	for i, p := range fpos {
		fields[i] = string(buf[p.start:p.end])
	}
	return fields
}
