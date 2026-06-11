package interp

// Bash-compatible command history for the `history` and `fc` builtins.
//
// Bash records history at the *reader* level: every line the parser
// consumes while `set -o history` is on becomes a history entry (joined
// with `; `/` `/newline separators for multi-line commands, comments
// included), filtered through HISTCONTROL/HISTIGNORE and stifled to
// HISTSIZE. This interpreter executes a fully-parsed AST instead of
// reading lines, so we emulate the reader: when history is enabled we
// re-parse the script source (keeping comments) into a line-ordered
// "timeline" of would-be history entries, and every builtin dispatch
// advances a cursor through that timeline up to the builtin's source
// line. This matches bash's observable behavior for scripts because all
// lines up to the currently-executing command have been "read".
//
// The state is a process-wide singleton rather than a Runner field so
// that subshells (which are separate Runner copies in this interpreter)
// share the history list the way bash subshells inherit it. It only
// activates once a script turns on `set -o history`, so unrelated
// Runners in the same process are unaffected.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"mvdan.cc/sh/v3/pattern"
	"mvdan.cc/sh/v3/syntax"
)

// histGroup is one would-be history entry from the source timeline.
type histGroup struct {
	startLine int
	text      string
}

type histState struct {
	mu     sync.Mutex
	active atomic.Bool // fast-path gate for histSync

	enabled bool // `set -o history`
	expand  bool // `set -H` / `set -o histexpand`

	srcName  string
	timeline []histGroup
	next     int // next unconsumed timeline group

	list []string
	base int // history number of list[0] (bash history_base)

	linesThisSession int // entries added this session (for history -a)
	linesInFile      int // file lines already consumed (for history -n)
	loaded           bool

	lastAdded  bool // bash hist_last_line_added
	lastPushed bool // bash hist_last_line_pushed
}

var shellHist = &histState{base: 1}

// histReset restores the singleton to its initial state. Used by tests.
func histReset() {
	h := shellHist
	h.mu.Lock()
	defer h.mu.Unlock()
	h.active.Store(false)
	h.enabled, h.expand = false, false
	h.srcName, h.timeline, h.next = "", nil, 0
	h.list, h.base = nil, 1
	h.linesThisSession, h.linesInFile, h.loaded = 0, 0, false
	h.lastAdded, h.lastPushed = false, false
}

// histSync advances the reader-emulation cursor up to the given source
// position, recording every timeline entry bash's reader would have
// consumed by now. Called at the top of every builtin dispatch.
func (r *Runner) histSync(ctx context.Context, pos syntax.Pos) {
	h := shellHist
	if !h.active.Load() {
		return
	}
	line := int(pos.Line())
	if line <= 0 {
		return
	}
	h.mu.Lock()
	var expanded []string
	if h.enabled {
		for h.next < len(h.timeline) && h.timeline[h.next].startLine <= line {
			g := h.timeline[h.next]
			h.next++
			text := g.text
			// The `!` event char triggers history expansion unless
			// followed by a blank, `=` or `(` (so `! cmd` negation
			// pipelines are left alone), and only once `set -H` is on.
			if h.expand && len(text) > 1 && text[0] == '!' &&
				!strings.ContainsRune(" \t=(", rune(text[1])) {
				exp, ok := h.expandDesignator(text)
				if !ok {
					h.lastAdded = false
					continue
				}
				expanded = append(expanded, exp)
				text = exp
			}
			h.addLine(r, text)
		}
	}
	h.mu.Unlock()
	// Bash echoes history-expanded lines to stderr as it reads them, and
	// then executes the expansion. The original `!...` statement already
	// failed in the runner (the AST interpreter has no reader-level
	// expansion), so running the expansion here is the closest match.
	for _, e := range expanded {
		r.errf("%s\n", e)
		r.histRunString(ctx, e)
	}
}

// histSetEnabled flips `set -o history` state. On the off→on transition
// it builds the source timeline and, like bash's set.def, loads HISTFILE
// if no history lines have been added this session.
func (r *Runner) histSetEnabled(on bool, pos syntax.Pos) {
	h := shellHist
	h.mu.Lock()
	defer h.mu.Unlock()
	h.enabled = on
	if !on {
		return
	}
	h.active.Store(true)
	if h.timeline == nil && r.filename != "" {
		if src, err := os.ReadFile(r.absPath(r.filename)); err == nil {
			h.srcName = r.filename
			h.timeline = buildHistTimeline(src)
			// Skip everything up to and including the enabling line:
			// those lines were read while history was still off.
			for h.next < len(h.timeline) && h.timeline[h.next].startLine <= int(pos.Line()) {
				h.next++
			}
		}
	}
	if !h.loaded && h.linesThisSession == 0 {
		h.loaded = true
		hf := r.envGet("HISTFILE")
		if hf == "" {
			if home, err := os.UserHomeDir(); err == nil {
				hf = filepath.Join(home, ".bashy_history")
			}
		}
		if hf != "" {
			if entries, nlines, err := readHistFile(r.absPath(hf)); err == nil {
				for _, e := range entries {
					h.appendEntry(r, e)
				}
				h.linesInFile = nlines
			}
		}
	}
}

func (r *Runner) histSetExpand(on bool) {
	h := shellHist
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expand = on
	if on {
		h.active.Store(true)
	}
}

// addLine records one reader line, applying HISTCONTROL and HISTIGNORE,
// and updates hist_last_line_added.
func (h *histState) addLine(r *Runner, text string) {
	h.lastPushed = false
	if strings.TrimSpace(text) == "" {
		return
	}
	ignoreSpace, ignoreDups, eraseDups := false, false, false
	for _, w := range strings.Split(r.envGet("HISTCONTROL"), ":") {
		switch w {
		case "ignorespace":
			ignoreSpace = true
		case "ignoredups":
			ignoreDups = true
		case "ignoreboth":
			ignoreSpace, ignoreDups = true, true
		case "erasedups":
			eraseDups = true
		}
	}
	if ignoreSpace && (text[0] == ' ' || text[0] == '\t') {
		h.lastAdded = false
		return
	}
	if ignoreDups && len(h.list) > 0 && h.list[len(h.list)-1] == text {
		h.lastAdded = false
		return
	}
	if h.histIgnored(r, text) {
		h.lastAdded = false
		return
	}
	if eraseDups {
		kept := h.list[:0]
		for _, e := range h.list {
			if e != text {
				kept = append(kept, e)
			}
		}
		h.list = kept
	}
	h.appendEntry(r, text)
	h.lastAdded = true
	h.linesThisSession++
}

// histIgnored reports whether HISTIGNORE filters out the line. The `&`
// pattern matches the previous history entry.
func (h *histState) histIgnored(r *Runner, text string) bool {
	hi := r.envGet("HISTIGNORE")
	if hi == "" {
		return false
	}
	for _, pat := range strings.Split(hi, ":") {
		if pat == "" {
			continue
		}
		if pat == "&" {
			if len(h.list) > 0 && h.list[len(h.list)-1] == text {
				return true
			}
			continue
		}
		expr, err := pattern.Regexp(pat, pattern.EntireString)
		if err != nil {
			continue
		}
		if rx, err := regexp.Compile(expr); err == nil && rx.MatchString(text) {
			return true
		}
	}
	return false
}

// appendEntry adds an entry and stifles the list to HISTSIZE, advancing
// the base number for entries dropped off the front (bash history_base).
func (h *histState) appendEntry(r *Runner, text string) {
	h.list = append(h.list, text)
	if size, ok := h.histSize(r); ok {
		if size <= 0 {
			h.base += len(h.list)
			h.list = nil
			return
		}
		for len(h.list) > size {
			h.list = h.list[1:]
			h.base++
		}
	}
}

func (h *histState) histSize(r *Runner) (int, bool) {
	vr := r.lookupVar("HISTSIZE")
	if !vr.IsSet() {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(vr.String()))
	if err != nil || n < 0 {
		return 0, false // bash treats invalid/negative as unlimited
	}
	return n, true
}

// expandDesignator implements the small subset of history expansion the
// builtins need: `!!`, `!-n`, `!n` and `!prefix`, optionally followed by
// more text on the line.
func (h *histState) expandDesignator(line string) (string, bool) {
	rest := line[1:]
	var desig, tail string
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		desig, tail = rest[:i], rest[i:]
	} else {
		desig = rest
	}
	var entry string
	switch {
	case desig == "!":
		if len(h.list) == 0 {
			return "", false
		}
		entry = h.list[len(h.list)-1]
	case strings.HasPrefix(desig, "-"):
		n, err := strconv.Atoi(desig)
		if err != nil || -n > len(h.list) || n == 0 {
			return "", false
		}
		entry = h.list[len(h.list)+n]
	default:
		if n, err := strconv.Atoi(desig); err == nil {
			idx := n - h.base
			if idx < 0 || idx >= len(h.list) {
				return "", false
			}
			entry = h.list[idx]
			break
		}
		for j := len(h.list) - 1; j >= 0; j-- {
			if strings.HasPrefix(h.list[j], desig) {
				entry = h.list[j]
				break
			}
		}
		if entry == "" {
			return "", false
		}
	}
	return entry + tail, true
}

// readHistFile reads a history file the way bash's read_history does:
// `#<digits>` lines are timestamps delimiting (possibly multi-line)
// entries; without timestamps every line is one entry. Returns the
// entries and the number of file lines consumed.
func readHistFile(path string) ([]string, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(data) == 0 {
		return nil, 0, nil
	}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil, strings.Count(string(data), "\n"), nil
	}
	lines := strings.Split(text, "\n")
	isTimestamp := func(s string) bool {
		return len(s) > 1 && s[0] == '#' && s[1] >= '0' && s[1] <= '9'
	}
	var entries []string
	var cur []string
	timestamped := false
	flush := func() {
		// Leading blank lines are removed; interior and trailing
		// blanks are preserved.
		for len(cur) > 0 && strings.TrimSpace(cur[0]) == "" {
			cur = cur[1:]
		}
		if len(cur) > 0 {
			entries = append(entries, strings.Join(cur, "\n"))
		}
		cur = nil
	}
	for _, ln := range lines {
		if isTimestamp(ln) {
			timestamped = true
			flush()
			continue
		}
		if timestamped {
			cur = append(cur, ln)
		} else if ln != "" {
			entries = append(entries, ln)
		}
	}
	flush()
	return entries, len(lines), nil
}

func writeHistFile(path string, entries []string) error {
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e)
		sb.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(sb.String()), 0o600)
}

func appendHistFile(path string, entries []string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, e := range entries {
		if _, err := fmt.Fprintf(f, "%s\n", e); err != nil {
			return err
		}
	}
	return nil
}

// ---- timeline construction ----

// buildHistTimeline parses the script source (keeping comments) and
// produces the ordered list of history entries bash's reader would
// record, with their starting source lines.
func buildHistTimeline(src []byte) []histGroup {
	parser := syntax.NewParser(syntax.KeepComments(true), syntax.Variant(syntax.LangBash))
	f, err := parser.Parse(strings.NewReader(string(src)), "")
	if err != nil {
		return nil
	}
	srcLines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")

	type span struct {
		start, end int
		hdoc       bool
	}
	var spans []span
	quotedBreak := map[int]bool{} // line L: break between L and L+1 is inside quotes

	scanQuotes := func(node syntax.Node) {
		syntax.Walk(node, func(n syntax.Node) bool {
			if n == nil {
				return true
			}
			var p, e syntax.Pos
			switch q := n.(type) {
			case *syntax.SglQuoted:
				p, e = q.Pos(), q.End()
			case *syntax.DblQuoted:
				p, e = q.Pos(), q.End()
			default:
				return true
			}
			if p.IsValid() && e.IsValid() {
				for l := int(p.Line()); l < int(e.Line()); l++ {
					quotedBreak[l] = true
				}
			}
			return true
		})
	}

	for _, st := range f.Stmts {
		start, end := int(st.Pos().Line()), int(st.End().Line())
		hdoc := false
		syntax.Walk(st, func(n syntax.Node) bool {
			if n == nil {
				return true
			}
			if e := n.End(); e.IsValid() && int(e.Line()) > end {
				end = int(e.Line())
			}
			if rd, ok := n.(*syntax.Redirect); ok &&
				(rd.Op == syntax.Hdoc || rd.Op == syntax.DashHdoc) {
				hdoc = true
			}
			return true
		})
		scanQuotes(st)
		spans = append(spans, span{start, end, hdoc})
		for _, c := range st.Comments {
			spans = append(spans, span{int(c.Pos().Line()), int(c.End().Line()), false})
		}
	}
	for _, c := range f.Last {
		spans = append(spans, span{int(c.Pos().Line()), int(c.End().Line()), false})
	}
	if len(spans) == 0 {
		return nil
	}
	// Sort by start line and merge spans sharing lines: bash's reader is
	// line-oriented, so `echo a; echo b` on one line is one entry.
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j].start < spans[j-1].start; j-- {
			spans[j], spans[j-1] = spans[j-1], spans[j]
		}
	}
	var merged []span
	for _, s := range spans {
		if n := len(merged); n > 0 && s.start <= merged[n-1].end {
			if s.end > merged[n-1].end {
				merged[n-1].end = s.end
			}
			merged[n-1].hdoc = merged[n-1].hdoc || s.hdoc
			continue
		}
		merged = append(merged, s)
	}

	var groups []histGroup
	for _, s := range merged {
		if s.start < 1 || s.start > len(srcLines) {
			continue
		}
		end := min(s.end, len(srcLines))
		lines := srcLines[s.start-1 : end]
		text := joinHistLines(lines, s.hdoc, s.start, quotedBreak)
		if strings.TrimSpace(text) == "" {
			continue
		}
		groups = append(groups, histGroup{startLine: s.start, text: text})
	}
	return groups
}

// joinHistLines reproduces bash_add_history's joining of the source
// lines of one command into a single history entry.
func joinHistLines(lines []string, hdoc bool, startLine int, quotedBreak map[int]bool) string {
	if len(lines) == 1 {
		return lines[0]
	}
	if hdoc {
		// Here-document lines keep their newlines, including the
		// trailing one after the delimiter.
		return strings.Join(lines, "\n") + "\n"
	}
	acc := lines[0]
	for i, ln := range lines[1:] {
		boundary := startLine + i // line number before this one
		if quotedBreak[boundary] {
			acc += "\n" + ln
			continue
		}
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if t := strings.TrimSuffix(acc, "\\"); t != acc && !strings.HasSuffix(t, "\\") {
			// Escaped newline: the quoted newline disappears.
			acc = t + ln
			continue
		}
		if histNoSemiAfter(acc) {
			acc += " " + ln
		} else {
			acc += "; " + ln
		}
	}
	return acc
}

// histNoSemiAfter mirrors parse.y's no_semi_successors: after these
// trailing tokens bash joins continuation lines with a space, not `; `.
func histNoSemiAfter(acc string) bool {
	t := strings.TrimRight(acc, " \t")
	if t == "" {
		return true
	}
	for _, suf := range []string{"&&", "||", ";;&", ";;", ";&"} {
		if strings.HasSuffix(t, suf) {
			return true
		}
	}
	switch t[len(t)-1] {
	case ';', '&', '|', '{', '(', ')':
		return true
	}
	fields := strings.Fields(t)
	switch fields[len(fields)-1] {
	case "case", "do", "else", "if", "then", "until", "while", "in":
		return true
	}
	return false
}

// ---- the history builtin ----

func (r *Runner) historyBuiltin(pos syntax.Pos, args []string) (exit exitStatus) {
	failf := func(code uint8, format string, a ...any) exitStatus {
		r.errf("%s"+format, append([]any{r.bashErrPrefix(pos)}, a...)...)
		exit.code = code
		return exit
	}
	usage := func(flag string) exitStatus {
		r.errf("%shistory: %s: invalid option\n", r.bashErrPrefix(pos), flag)
		r.errf("history: usage: %s\n", bashUsage["history"])
		exit.code = 2
		return exit
	}
	var aFlag, cFlag, nFlag, pFlag, rFlag, sFlag, wFlag, dFlag bool
	var deleteArg string
	i := 0
parseOpts:
	for ; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			i++
			break
		}
		if len(a) < 2 || a[0] != '-' {
			break
		}
		for j := 1; j < len(a); j++ {
			switch a[j] {
			case 'a':
				aFlag = true
			case 'c':
				cFlag = true
			case 'n':
				nFlag = true
			case 'p':
				pFlag = true
			case 'r':
				rFlag = true
			case 's':
				sFlag = true
			case 'w':
				wFlag = true
			case 'd':
				dFlag = true
				if j+1 < len(a) {
					deleteArg = a[j+1:]
				} else if i+1 < len(args) {
					i++
					deleteArg = args[i]
				} else {
					return failf(2, "history: -d: option requires an argument\n")
				}
				continue parseOpts
			default:
				return usage(a[:1] + string(a[j]))
			}
		}
	}
	args = args[i:]

	nrw := 0
	for _, f := range []bool{aFlag, nFlag, rFlag, wFlag} {
		if f {
			nrw++
		}
	}
	if nrw > 1 {
		return failf(1, "history: cannot use more than one of -anrw\n")
	}

	h := shellHist
	h.mu.Lock()
	unlocked := false
	defer func() {
		if !unlocked {
			h.mu.Unlock()
		}
	}()

	if cFlag {
		h.list = nil
		h.base = 1
		h.linesThisSession = 0
		h.lastAdded = false
		if len(args) == 0 && !sFlag && !dFlag && nrw == 0 {
			return exit
		}
	}

	switch {
	case sFlag:
		if len(args) > 0 {
			// bash push_history: drop the `history -s` line itself if
			// the reader just added it, then add the argument string.
			if h.lastAdded && !h.lastPushed && len(h.list) > 0 {
				h.list = h.list[:len(h.list)-1]
			}
			h.addLine(r, strings.Join(args, " "))
			h.lastAdded = false
			h.lastPushed = true
		}
		return exit
	case pFlag:
		if h.lastAdded && !h.lastPushed && len(h.list) > 0 {
			h.list = h.list[:len(h.list)-1]
			h.lastAdded = false
		}
		for _, a := range args {
			if strings.HasPrefix(a, "!") && len(a) > 1 {
				exp, ok := h.expandDesignator(a)
				if !ok {
					h.mu.Unlock()
					unlocked = true
					return failf(1, "history: %s: history expansion failed\n", a)
				}
				r.outf("%s\n", exp)
			} else {
				r.outf("%s\n", a)
			}
		}
		return exit
	case dFlag:
		erange := func(tok string) exitStatus {
			h.mu.Unlock()
			unlocked = true
			return failf(1, "history: %s: history position out of range\n", tok)
		}
		rest := deleteArg
		if strings.HasPrefix(rest, "-") {
			rest = rest[1:]
		}
		if dash := strings.Index(rest, "-"); dash >= 0 {
			// Range form: start-end.
			startTok := deleteArg[:len(deleteArg)-len(rest)+dash]
			endTok := rest[dash+1:]
			start, err1 := strconv.Atoi(startTok)
			end, err2 := strconv.Atoi(endTok)
			if err1 != nil || err2 != nil || endTok == "" {
				return erange(deleteArg)
			}
			if strings.HasPrefix(startTok, "-") && start < 0 {
				start += len(h.list)
			} else if start > 0 {
				start -= h.base
			}
			if start < 0 || start >= len(h.list) {
				return erange(startTok)
			}
			if strings.HasPrefix(endTok, "-") && end < 0 {
				end += len(h.list)
			} else if end > 0 {
				end -= h.base
			}
			if end < 0 || end >= len(h.list) {
				return erange(endTok)
			}
			if start <= end {
				h.list = append(h.list[:start], h.list[end+1:]...)
			}
			return exit
		}
		n, err := strconv.Atoi(deleteArg)
		if err != nil {
			h.mu.Unlock()
			unlocked = true
			return failf(1, "history: %s: invalid number\n", deleteArg)
		}
		var idx int
		if strings.HasPrefix(deleteArg, "-") && n < 0 {
			idx = len(h.list) + n
			if idx < 0 {
				return erange(deleteArg)
			}
		} else if n < h.base || n >= h.base+len(h.list) {
			return erange(deleteArg)
		} else {
			idx = n - h.base
		}
		if idx >= len(h.list) {
			return erange(deleteArg)
		}
		h.list = append(h.list[:idx], h.list[idx+1:]...)
		return exit
	case nrw > 0:
		filename := ""
		if len(args) > 0 {
			filename = args[0]
		} else {
			filename = r.envGet("HISTFILE")
		}
		if filename == "" {
			h.mu.Unlock()
			unlocked = true
			if len(args) > 0 {
				return failf(1, "history: empty filename\n")
			}
			return failf(1, "history: HISTFILE: parameter null or not set\n")
		}
		path := r.absPath(filename)
		var err error
		switch {
		case aFlag:
			if h.linesThisSession > 0 {
				n := min(h.linesThisSession, len(h.list))
				err = appendHistFile(path, h.list[len(h.list)-n:])
				if err == nil {
					h.linesInFile += n
					h.linesThisSession = 0
				}
			} else if _, serr := os.Stat(path); os.IsNotExist(serr) {
				err = writeHistFile(path, nil)
			}
		case wFlag:
			err = writeHistFile(path, h.list)
		case rFlag:
			var entries []string
			var nlines int
			entries, nlines, err = readHistFile(path)
			if err == nil {
				for _, e := range entries {
					h.appendEntry(r, e)
				}
				h.linesInFile = nlines
			}
		case nFlag:
			var entries []string
			var nlines int
			entries, nlines, err = readHistFileFrom(path, h.linesInFile)
			if err == nil {
				for _, e := range entries {
					h.appendEntry(r, e)
				}
				h.linesInFile = nlines
				h.linesThisSession += len(entries)
			}
		}
		if err != nil {
			h.mu.Unlock()
			unlocked = true
			return failf(1, "history: %s: %v\n", filename, err)
		}
		return exit
	case cFlag:
		return exit
	}

	// Plain listing, optionally limited to the last N entries.
	limit := len(h.list)
	if len(args) > 1 {
		h.mu.Unlock()
		unlocked = true
		return failf(2, "history: too many arguments\n")
	}
	if len(args) == 1 {
		n, err := strconv.Atoi(args[0])
		if err != nil {
			h.mu.Unlock()
			unlocked = true
			return failf(2, "history: %s: numeric argument required\n", args[0])
		}
		if n < 0 {
			n = -n
		}
		limit = min(n, len(h.list))
	}
	for idx := len(h.list) - limit; idx < len(h.list); idx++ {
		r.outf("%5d  %s\n", h.base+idx, h.list[idx])
	}
	return exit
}

// readHistFileFrom reads only the file lines past the first `from`
// lines, for `history -n`.
func readHistFileFrom(path string, from int) ([]string, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil, 0, nil
	}
	lines := strings.Split(text, "\n")
	if from >= len(lines) {
		return nil, len(lines), nil
	}
	rest := strings.Join(lines[from:], "\n") + "\n"
	tmp := []byte(rest)
	// Reuse readHistFile's parsing on the remaining lines.
	f, err := os.CreateTemp("", "bashy-histn")
	if err != nil {
		return nil, 0, err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(tmp); err != nil {
		f.Close()
		return nil, 0, err
	}
	f.Close()
	entries, _, err := readHistFile(f.Name())
	return entries, len(lines), err
}

// ---- the fc builtin ----

const histInvalid = -1 << 30
const histNotFound = histInvalid + 2

func (r *Runner) fcBuiltin(ctx context.Context, pos syntax.Pos, args []string) (exit exitStatus) {
	failf := func(code uint8, format string, a ...any) exitStatus {
		r.errf("%s"+format, append([]any{r.bashErrPrefix(pos)}, a...)...)
		exit.code = code
		return exit
	}
	fcIsNumber := func(s string) bool {
		s = strings.TrimPrefix(s, "-")
		if s == "" {
			return false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}

	numbering, reverse, listing, execute := true, false, false, false
	ename := ""
	i := 0
parseOpts:
	for ; i < len(args); i++ {
		a := args[i]
		if fcIsNumber(a) {
			break
		}
		if a == "--" {
			i++
			break
		}
		if len(a) < 2 || a[0] != '-' {
			break
		}
		for j := 1; j < len(a); j++ {
			switch a[j] {
			case 'n':
				numbering = false
			case 'l':
				listing = true
			case 'r':
				reverse = true
			case 's':
				execute = true
			case 'e':
				if j+1 < len(a) {
					ename = a[j+1:]
				} else if i+1 < len(args) {
					i++
					ename = args[i]
				} else {
					return failf(2, "fc: -e: option requires an argument\n")
				}
				continue parseOpts
			default:
				r.errf("%sfc: -%c: invalid option\n", r.bashErrPrefix(pos), a[j])
				r.errf("fc: usage: %s\n", bashUsage["fc"])
				exit.code = 2
				return exit
			}
		}
	}
	specs := args[i:]

	if ename == "-" {
		execute = true
		ename = ""
	}

	h := shellHist
	h.mu.Lock()

	rh := 0
	if h.enabled {
		rh = 1
	}
	la := 0
	if h.lastAdded {
		la = 1
	}

	// gethnum translates one history spec into a 0-based list index,
	// following fc.def's fc_gethnum.
	lastHist := len(h.list) - rh - la
	if lastHist < 0 {
		lastHist = 0
	}
	realLast := len(h.list) - 1
	gethnum := func(spec string, first bool) int {
		idx := lastHist
		if spec == "" {
			return idx
		}
		s := spec
		sign := 1
		if strings.HasPrefix(s, "-") {
			sign = -1
			s = s[1:]
		}
		if s != "" && s[0] >= '0' && s[0] <= '9' {
			end := 0
			for end < len(s) && s[end] >= '0' && s[end] <= '9' {
				end++
			}
			n, _ := strconv.Atoi(s[:end])
			n *= sign
			if n < 0 {
				n += idx + 1
				if n < 0 {
					n = 0
				}
				return n
			}
			if n == 0 {
				if sign == -1 {
					if listing {
						return realLast
					}
					return histInvalid
				}
				return idx
			}
			n -= h.base
			if n < 0 || n >= idx {
				if first {
					return 0
				}
				return idx
			}
			return n
		}
		for j := idx; j >= 0 && j < len(h.list); j-- {
			if strings.HasPrefix(h.list[j], spec) {
				return j
			}
		}
		return histNotFound
	}

	if execute {
		// fc -s [pat=rep ...] [command]: re-execute with substitutions.
		var subs [][2]string
		for len(specs) > 0 {
			eq := strings.Index(specs[0], "=")
			if eq < 0 {
				break
			}
			subs = append(subs, [2]string{specs[0][:eq], specs[0][eq+1:]})
			specs = specs[1:]
		}
		spec := ""
		if len(specs) > 0 {
			spec = specs[0]
		}
		idx := gethnum(spec, false)
		if idx < 0 || idx >= len(h.list) {
			h.mu.Unlock()
			return failf(1, "fc: no command found\n")
		}
		command := h.list[idx]
		for _, s := range subs {
			command = strings.ReplaceAll(command, s[0], s[1])
		}
		// Replace the recorded `fc -s` line with the re-executed
		// command, so consecutive substitutions chain.
		if h.lastAdded && !h.lastPushed && len(h.list) > 0 {
			h.list = h.list[:len(h.list)-1]
		}
		h.addLine(r, command)
		h.mu.Unlock()
		r.errf("%s\n", command)
		return r.histRunString(ctx, command)
	}

	if len(h.list) == 0 {
		h.mu.Unlock()
		return exit
	}

	var histbeg, histend int
	if len(specs) > 0 {
		histbeg = gethnum(specs[0], true)
		if len(specs) > 1 {
			histend = gethnum(specs[1], false)
		} else if histbeg == realLast {
			histend = histbeg
			if listing {
				histend = realLast
			}
		} else {
			histend = histbeg
			if listing {
				histend = lastHist
			}
		}
	} else if listing {
		histend = lastHist
		histbeg = max(histend-15, 0)
	} else {
		histbeg, histend = lastHist, lastHist
	}

	if histbeg == histInvalid || histend == histInvalid {
		h.mu.Unlock()
		return failf(1, "fc: history specification out of range\n")
	}
	if histbeg == histNotFound || histend == histNotFound {
		h.mu.Unlock()
		return failf(1, "fc: no command found\n")
	}
	histbeg, histend = max(histbeg, 0), max(histend, 0)

	if !listing && h.lastAdded && !h.lastPushed && len(h.list) > 0 {
		// "When not listing, the fc command that caused the editing
		// shall not be entered into the history list."
		h.list = h.list[:len(h.list)-1]
		if histend >= lastHist {
			histend = lastHist
		} else if histbeg >= lastHist {
			histbeg = lastHist
		}
		h.lastAdded = false
	}

	if histend < histbeg {
		histbeg, histend = histend, histbeg
		reverse = true
	}
	histend = min(histend, len(h.list)-1)
	histbeg = min(histbeg, len(h.list)-1)
	if histbeg < 0 || histend < 0 {
		h.mu.Unlock()
		return exit
	}

	if listing {
		idxs := make([]int, 0, histend-histbeg+1)
		for j := histbeg; j <= histend; j++ {
			idxs = append(idxs, j)
		}
		if reverse {
			for l, rgt := 0, len(idxs)-1; l < rgt; l, rgt = l+1, rgt-1 {
				idxs[l], idxs[rgt] = idxs[rgt], idxs[l]
			}
		}
		base := h.base
		entries := make([]string, len(idxs))
		for k, j := range idxs {
			entries[k] = h.list[j]
		}
		h.mu.Unlock()
		for k, j := range idxs {
			if numbering {
				r.outf("%d\t %s\n", base+j, entries[k])
			} else {
				r.outf("\t %s\n", entries[k])
			}
		}
		return exit
	}

	// Edit-and-rerun: write the range to a temp file, run the editor on
	// it, then echo and execute the (possibly edited) commands.
	selected := make([]string, 0, histend-histbeg+1)
	for j := histbeg; j <= histend; j++ {
		selected = append(selected, h.list[j])
	}
	h.mu.Unlock()

	tmp, err := os.CreateTemp("", "bashy-fc")
	if err != nil {
		return failf(1, "fc: cannot open temp file: %v\n", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	for _, e := range selected {
		fmt.Fprintf(tmp, "%s\n", e)
	}
	tmp.Close()

	editor := ename
	if editor == "" {
		if editor = r.envGet("FCEDIT"); editor == "" {
			if editor = r.envGet("EDITOR"); editor == "" {
				editor = "vi"
			}
		}
	}
	if st := r.histRunString(ctx, editor+" "+tmpName); st.code != 0 {
		exit.code = 1
		return exit
	}

	edited, err := os.ReadFile(tmpName)
	if err != nil {
		exit.code = 1
		return exit
	}
	content := strings.TrimSuffix(string(edited), "\n")
	if content == "" {
		return exit
	}
	for _, ln := range strings.Split(content, "\n") {
		r.errf("%s\n", ln)
	}
	h.mu.Lock()
	h.addLine(r, content)
	h.mu.Unlock()
	return r.histRunString(ctx, content)
}

// histRunString parses and runs a command string, returning its exit
// status. Used by fc to re-execute history entries and editors.
func (r *Runner) histRunString(ctx context.Context, src string) exitStatus {
	p := syntax.NewParser(syntax.Variant(syntax.LangBash))
	file, err := p.Parse(strings.NewReader(src), "")
	if err != nil {
		r.errf("fc: parse error: %v\n", err)
		return exitStatus{code: 1}
	}
	r.stmts(ctx, file.Stmts)
	return r.exit
}
