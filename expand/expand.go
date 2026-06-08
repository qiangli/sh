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
	"maps"
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

	// DotGlob corresponds to the shell option which allows filenames beginning
	// with a dot to be matched by a pattern which does not begin with a dot.
	DotGlob bool

	// NoCaseGlob corresponds to the shell option which causes case-insensitive
	// pattern matching in pathname expansion.
	NoCaseGlob bool

	// NullGlob corresponds to the shell option which allows globbing
	// patterns which match nothing to result in zero fields.
	NullGlob bool

	// NoUnset corresponds to the shell option which treats unset variables
	// as errors.
	NoUnset bool

	// ExtGlob corresponds to the shell option which allows using extended
	// pattern matching features when performing pathname expansion (globbing).
	ExtGlob bool

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

	bufferAlloc strings.Builder
	fieldAlloc  [4]fieldPart
	fieldsAlloc [4][]fieldPart

	ifs string
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
	if vr := cfg.Env.Get("IFS"); vr.IsSet() {
		cfg.ifs = vr.String()
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
		sep = cfg.ifs[:1]
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
	vr.Set = true
	switch vr.Kind {
	case Indexed:
		if len(vr.List) == 0 {
			vr.List = []string{value}
		} else {
			vr.List[0] = value
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

// literalKeepAnsiC is like [Literal] but leaves `$'…'` ANSI-C
// quoting literal (no decoding). Used by the parameter-expansion
// default-value path inside heredoc bodies — bash 5.3 preserves
// `$'\01'` as five literal characters in that position.
func literalKeepAnsiC(cfg *Config, word *syntax.Word) (string, error) {
	if word == nil {
		return "", nil
	}
	cfg = prepareConfig(cfg)
	prev := cfg.literalAnsiC
	cfg.literalAnsiC = true
	defer func() { cfg.literalAnsiC = prev }()
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
					sb.WriteRune(rune(n))
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
				sb.WriteRune(rune(n))
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
	_, err := formatIntoMode(sb, s, nil, cfg.StartTime, true, nil, nil)
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

	consumed, err := formatIntoMode(sb, format, args, cfg.StartTime, false, cfg.OnFormatWarning, cfg.OnPercentN)
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
		case 'F':
			fmt.Fprintf(&sb, "%04d-%02d-%02d", t.Year(), int(t.Month()), t.Day())
		case 'D':
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
	return formatIntoMode(sb, format, args, startTime, false, warn, nil)
}

// formatIntoMode is the inner worker for [Format]. percentB switches the
// escape table to bash's `%b` interpretation: `\"`, `\'`, `\?` are
// preserved with their backslash (bash only honors those escapes in
// format strings, not in `%b` arg). onPercentN is invoked by `%n` to
// store the byte count into the variable named by the next arg.
func formatIntoMode(sb *strings.Builder, format string, args []string, startTime time.Time, percentB bool, warn func(string), onPercentN func(string, int) error) (int, error) {
	inPercentB := percentB
	var fmts []byte
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
				// bash printf: `\nnn` is 1-3 octal digits. A leading
				// `\0` is also accepted as a marker followed by up
				// to 3 more octal digits (so `\0200` reads 200 octal
				// = 0x80, NOT \020 + literal "0"). Match that.
				if c == '0' && i+1 < len(format) {
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
						sb.WriteRune(rune(n))
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
				if len(fmts) > 1 {
					return 0, fmt.Errorf("printf: `%c': invalid format character", c)
				}
				end := strings.IndexByte(format[i+1:], ')')
				if end < 0 {
					return 0, fmt.Errorf("printf: missing matching `)' in format")
				}
				strFmt := format[i+1 : i+1+end]
				nextIdx := i + 1 + end + 1
				if nextIdx >= len(format) || format[nextIdx] != 'T' {
					return 0, fmt.Errorf("printf: %%(...) must be followed by `T'")
				}
				var t time.Time
				if len(args) > 0 {
					arg := args[0]
					args = args[1:]
					switch arg {
					case "-1", "":
						t = time.Now()
					case "-2":
						if !startTime.IsZero() {
							t = startTime
						} else {
							t = time.Now()
						}
					default:
						n, err := strconv.ParseInt(arg, 10, 64)
						if err != nil {
							return 0, fmt.Errorf("printf: %s: invalid number", arg)
						}
						t = time.Unix(n, 0)
					}
				} else {
					t = time.Now()
				}
				sb.WriteString(strftime(strFmt, t))
				i = nextIdx // skip past the )T
				fmts = nil
			case 'c':
				var b byte
				if len(args) > 0 {
					arg := ""
					arg, args = args[0], args[1:]
					if len(arg) > 0 {
						b = arg[0]
					}
				}
				sb.WriteByte(b)
				fmts = nil
			case '+', '-', ' ', '#', '\'':
				if len(fmts) > 1 {
					return 0, fmt.Errorf("printf: `%c': invalid format character", c)
				}
				fmts = append(fmts, c)
			case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '.':
				fmts = append(fmts, c)
			case 'l', 'h', 'L', 'j', 'z', 't':
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
				width, err := strconv.ParseInt(strings.TrimSpace(wArg), 10, 64)
				if err != nil {
					return 0, fmt.Errorf("printf: %s: invalid number", wArg)
				}
				fmts = append(fmts, []byte(strconv.FormatInt(width, 10))...)
			case 'q', 'Q':
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
				// bash's `printf %q` strategy:
				//   - All chars printable AND no shell-special:
				//     no quoting.
				//   - All chars printable, some shell-special:
				//     backslash-escape each special char.
				//   - Any non-printable / invalid-UTF-8 byte:
				//     fall back to syntax.Quote (`$'…'` form).
				quoted := bashPrintfQuote(arg)
				if quoted == "" && arg != "" {
					// fallback: control chars present, use $'…'
					q, qerr := syntax.Quote(arg, syntax.LangBash)
					if qerr != nil {
						q = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
					}
					quoted = q
				}
				if arg == "" {
					quoted = "''"
				}
				// Honor the width/precision specifier (`%-10q`,
				// `%10q`, `%.5q`, …) by passing the quoted result
				// through fmt.Sprintf with `%s` semantics. For `%Q`
				// we already trimmed the unquoted arg, so strip the
				// precision from the verb to avoid Go re-trimming.
				if len(fmts) > 1 {
					verb := string(fmts) + "s"
					if stripPrec && c == 'Q' {
						dot := bytes.IndexByte(fmts, '.')
						verb = string(fmts[:dot]) + "s"
					}
					sb.WriteString(fmt.Sprintf(verb, quoted))
				} else {
					sb.WriteString(quoted)
				}
				fmts = nil
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
				continue
			case 's', 'b', 'd', 'i', 'u', 'o', 'x', 'X', 'f', 'F', 'e', 'E', 'g', 'G':
				arg := ""
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
					_, err := formatIntoMode(&bsb, arg, nil, startTime, true, warn, nil)
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
					fmts = append(fmts, c)
					fmt.Fprintf(sb, string(fmts), farg)
				}
				fmts = nil
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
		if pattern.HasMeta(part.val, 0) {
			glob = true
		}
	}
	if glob { // only copy the string if it will be used
		escaped = sb.String()
	}
	return escaped, glob
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
			prevQuote := cfg.insideDoubleQuote
			if ql == quoteDouble || ql == quoteHeredoc {
				cfg.insideDoubleQuote = true
			}
			val, err := cfg.paramExp(wp)
			cfg.insideDoubleQuote = prevQuote
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
			if strings.Contains(s, "\\") {
				sb := cfg.strBuilder()
				for i := 0; i < len(s); i++ {
					b := s[i]
					if b == '\\' {
						if i++; i >= len(s) {
							sb.WriteByte(b)
							break
						}
						b = s[i]
					}
					sb.WriteByte(b)
				}
				s = sb.String()
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
			curField = append(curField, fieldPart{val: s})
		case *syntax.SglQuoted:
			allowEmpty = true
			fp := fieldPart{quote: quoteSingle, val: wp.Value}
			if wp.Dollar {
				fp.val = ansiCEscape(fp.val)
				fp.val, _, _ = strings.Cut(fp.val, "\x00") // cut the string if format included \x00
			}
			curField = append(curField, fp)
		case *syntax.DblQuoted:
			if len(wp.Parts) == 1 {
				pe, _ := wp.Parts[0].(*syntax.ParamExp)
				if elems := cfg.quotedElemFields(pe); elems != nil {
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
			wfield, err := cfg.wordField(wp.Parts, quoteDouble)
			if err != nil {
				return nil, err
			}
			for _, part := range wfield {
				part.quote = quoteDouble
				curField = append(curField, part)
			}
		case *syntax.ParamExp:
			// `${var-"$@"}` (unquoted) preserves the field
			// structure of "$@" when the default fires. Detect
			// that special case before falling through to the
			// generic single-string paramExp path. We restrict
			// the recovery to `"$@"` defaults — `"$*"` always
			// joins to a single string, so the regular path is
			// already correct for it.
			if isSubstWithQuotedAt(wp) {
				if elems := cfg.quotedElemFields(wp); elems != nil {
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
		syntax.DefaultUnset, syntax.DefaultUnsetOrNull:
	default:
		return nil, false, nil
	}
	if len(pe.Exp.Word.Parts) != 1 {
		return nil, false, nil
	}
	switch pe.Exp.Word.Parts[0].(type) {
	case *syntax.SglQuoted, *syntax.DblQuoted:
	default:
		return nil, false, nil
	}
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
		return nil, false, nil
	}
	fields, err := cfg.wordFields(pe.Exp.Word.Parts)
	if err != nil {
		return nil, false, err
	}
	for i, field := range fields {
		if len(field) == 0 {
			fields[i] = []fieldPart{{quote: quoteSingle, val: ""}}
		}
	}
	return fields, true, nil
}

// quotedElemFields returns the list of elements resulting from a quoted
// parameter expansion that should be treated especially, like "${foo[@]}".
func (cfg *Config) quotedElemFields(pe *syntax.ParamExp) []string {
	if pe == nil || pe.Length || pe.Width || pe.IsSet {
		return nil
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
			switch inner := pe.Exp.Word.Parts[0].(type) {
			case *syntax.ParamExp:
				innerPE = inner
			case *syntax.DblQuoted:
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
					if e := cfg.quotedElemFields(innerPE); e != nil {
						return e
					}
				}
			}
		}
	}
	if elems := cfg.quotedReplElemFields(pe); elems != nil {
		return elems
	}
	if elems := cfg.quotedRemoveElemFields(pe); elems != nil {
		return elems
	}
	// Casemod operators need per-element processing by the full
	// paramExp path, so return nil here and let the caller fall back
	// to it.
	if pe.Exp != nil || pe.Repl != nil {
		return nil
	}
	name := pe.Param.Value
	if pe.Excl {
		switch pe.Names {
		case syntax.NamesPrefixWords: // "${!prefix@}"
			return cfg.namesByPrefix(pe.Param.Value)
		case syntax.NamesPrefix: // "${!prefix*}"
			return nil
		}
		switch nodeLit(pe.Index) {
		case "@": // "${!name[@]}"
			switch vr := cfg.Env.Get(name); vr.Kind {
			case Indexed:
				// TODO: if an indexed array only has elements 0 and 10,
				// we should not return all indices in between those.
				keys := make([]string, 0, len(vr.List))
				for key := range vr.List {
					keys = append(keys, strconv.Itoa(key))
				}
				return keys
			case Associative:
				return slices.Collect(maps.Keys(vr.Map))
			}
		}
		vr := cfg.Env.Get(name)
		switch target := vr.String(); target {
		case "@":
			return cfg.quotedAllElemValues(&syntax.ParamExp{Param: &syntax.Lit{Value: "@"}})
		case "*":
			return cfg.quotedAllElemValues(&syntax.ParamExp{Param: &syntax.Lit{Value: "*"}})
		}
		return nil
	}
	switch name {
	case "*": // "${*}" or "${*:offset:length}"
		return []string{cfg.ifsJoin(cfg.sliceElems(pe, cfg.Env.Get(name).List, true))}
	case "@": // "${@}" or "${@:offset:length}"
		return cfg.sliceElems(pe, cfg.Env.Get(name).List, true)
	}
	switch nodeLit(pe.Index) {
	case "@": // "${name[@]}"
		vr := cfg.Env.Get(name)
		switch vr.Kind {
		case Indexed:
			return cfg.sliceElems(pe, vr.List, false)
		case Associative:
			return slices.Collect(maps.Values(vr.Map))
		case Unknown:
			if !vr.IsSet() {
				// An unset variable expanded as "${name[@]}" produces
				// zero fields, just like an empty array.
				return []string{}
			}
		}
	case "*": // "${name[*]}"
		if vr := cfg.Env.Get(name); vr.Kind == Indexed {
			return []string{cfg.ifsJoin(cfg.sliceElems(pe, vr.List, false))}
		}
	}
	return nil
}

func (cfg *Config) quotedAllElemValues(pe *syntax.ParamExp) []string {
	name := pe.Param.Value
	switch name {
	case "*":
		return []string{cfg.ifsJoin(cfg.sliceElems(pe, cfg.Env.Get(name).List, true))}
	case "@":
		return cfg.sliceElems(pe, cfg.Env.Get(name).List, true)
	default:
		switch nodeLit(pe.Index) {
		case "@":
			vr := cfg.Env.Get(name)
			switch vr.Kind {
			case Indexed:
				return cfg.sliceElems(pe, vr.List, false)
			case Associative:
				keys := AssocKeysInBashOrder(vr.Map)
				elems := make([]string, len(keys))
				for i, k := range keys {
					elems[i] = vr.Map[k]
				}
				return elems
			}
		case "*":
			if vr := cfg.Env.Get(name); vr.Kind == Indexed {
				return []string{cfg.ifsJoin(cfg.sliceElems(pe, vr.List, false))}
			}
		}
	}
	return nil
}

func (cfg *Config) quotedReplElemFields(pe *syntax.ParamExp) []string {
	if pe == nil || pe.Repl == nil || pe.Length || pe.Width || pe.IsSet || pe.Excl {
		return nil
	}
	elems := cfg.quotedAllElemValues(pe)
	if elems == nil {
		return nil
	}
	orig, replAnchoredStart, replAnchoredEnd, err := replPattern(cfg, pe.Repl.Orig)
	if err != nil || orig == "" {
		return nil
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
				return nil
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
		locs := findReplIndex(orig, elem, n, replAnchoredStart, replAnchoredEnd)
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
	return out
}

func (cfg *Config) quotedRemoveElemFields(pe *syntax.ParamExp) []string {
	if pe == nil || pe.Exp == nil || pe.Length || pe.Width || pe.IsSet || pe.Excl {
		return nil
	}
	op := pe.Exp.Op
	isPatternOp := op == syntax.RemSmallPrefix || op == syntax.RemLargePrefix ||
		op == syntax.RemSmallSuffix || op == syntax.RemLargeSuffix
	if !isPatternOp {
		return nil
	}
	elems := cfg.quotedAllElemValues(pe)
	if elems == nil {
		return nil
	}
	arg, err := Pattern(cfg, pe.Exp.Word)
	if err != nil {
		return nil
	}
	suffix := op == syntax.RemSmallSuffix || op == syntax.RemLargeSuffix
	small := op == syntax.RemSmallPrefix || op == syntax.RemSmallSuffix
	out := make([]string, len(elems))
	for i, elem := range elems {
		out[i] = removePattern(elem, arg, suffix, small)
	}
	return out
}

// sliceElems applies ${var:offset:length} slicing to a list of elements.
// When positional is true, $0 is prepended to the list before slicing.
// In bash, positional parameter offsets ($@ and $*) are 1-based and
// offset 0 includes $0 (the shell or script name). Negative offsets
// count from $# + 1, so $0 is reachable via large enough negative values.
func (cfg *Config) sliceElems(pe *syntax.ParamExp, elems []string, positional bool) []string {
	if pe.Slice == nil {
		return elems
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
			return elems
		}
		elems = elems[slicePos(offset):]
	}
	if pe.Slice.Length != nil {
		length, err := Arithm(cfg, pe.Slice.Length)
		if err != nil {
			return elems
		}
		elems = elems[:slicePos(length)]
	}
	return elems
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

func findAllIndex(pat, name string, n int) [][]int {
	expr, err := pattern.Regexp(pat, 0)
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
		case !pattern.HasMeta(part, 0):
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
	return matches, nil
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
