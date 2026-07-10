// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

// Package interp implements an interpreter to execute shell programs
// parsed by the [syntax] package as either [syntax.LangBash]
// or [syntax.LangPOSIX], behaving like Bash as a result.
//
// The interpreter currently aims to behave like a non-interactive shell,
// which is how most shells run scripts, and is more useful to machines.
// In the future, it may gain an option to behave like an interactive shell.
package interp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	mathrand "math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

const BashyInheritedFdsEnv = "BASHY_INHERITED_FDS"

// BashyHardIgnoreEnv carries, across an exec of our own shell binary, the set
// of signals (comma-separated bash names) that the parent shell had set to
// SIG_IGN via `trap ” SIG`. The child treats them as ignored-on-entry, i.e.
// hard-ignored: a `trap` on them is a silent no-op and they list as
// `trap -- ” SIG`, matching bash's SIG_HARD_IGNORE handling (trap.c). It is an
// internal channel -- filtered out of the environment passed to grandchildren
// and unset from the child's own variable scope so scripts never observe it.
const BashyHardIgnoreEnv = "BASHY_HARD_IGNORE"

// A Runner interprets shell programs. It can be reused, but it is not safe for
// concurrent use. Use [New] to build a new Runner.
//
// Note that writes to Stdout and Stderr may be concurrent if background
// commands are used. If you plan on using an [io.Writer] implementation that
// isn't safe for concurrent use, consider a workaround like hiding writes
// behind a mutex.
//
// Runner's exported fields are meant to be configured via [RunnerOption];
// once a Runner has been created, the fields should be treated as read-only.
type Runner struct {
	// Env specifies the initial environment for the interpreter, which must
	// not be nil. It can only be set via [Env].
	//
	// If it includes a TMPDIR variable describing an absolute directory,
	// it is used as the directory in which to create temporary files needed
	// for the interpreter's use, such as named pipes for process substitutions.
	// Otherwise, [os.TempDir] is used.
	Env expand.Environ

	// writeEnv overlays [Runner.Env] so that we can write environment variables
	// as an overlay.
	writeEnv expand.WriteEnviron

	// Dir specifies the working directory of the command, which must be an
	// absolute path. It can only be set via [Dir].
	Dir string

	// tempDir is either $TMPDIR from [Runner.Env], or [os.TempDir].
	tempDir string

	// Params are the current shell parameters, e.g. from running a shell
	// file or calling a function. Accessible via the $@/$* family of vars.
	// It can only be set via [Params].
	Params []string

	// Separate maps - note that bash allows a name to be both a var and a
	// func simultaneously.
	// Vars is mostly superseded by Env at this point.
	// TODO(v4): remove these

	Vars  map[string]expand.Variable
	Funcs map[string]*syntax.Stmt

	// funcSources records the script name active when a function was
	// defined. Bash reports runtime diagnostics in a function body against
	// the definition source, not necessarily the caller's source.
	funcSources map[string]string

	alias map[string]alias

	// callHandler is a function allowing to replace a simple command's
	// arguments. It may be nil.
	callHandler CallHandlerFunc

	// execHandler is responsible for executing programs. It must not be nil.
	execHandler ExecHandlerFunc

	// execMiddlewares grows with calls to [ExecHandlers],
	// and is used to construct execHandler when Reset is first called.
	// The slice is needed to preserve the relative order of middlewares.
	execMiddlewares []func(ExecHandlerFunc) ExecHandlerFunc

	// openHandler is a function responsible for opening files. It must not be nil.
	openHandler OpenHandlerFunc

	// readDirHandler is a function responsible for reading directories during
	// glob expansion. It must be non-nil.
	readDirHandler ReadDirHandlerFunc2

	// statHandler is a function responsible for getting file stat. It must be non-nil.
	statHandler StatHandlerFunc

	stdin            *os.File // e.g. the read end of a pipe
	stdinTTYFallback bool
	stdinDevTTY      bool
	stdout           io.Writer
	stderr           io.Writer

	ecfg *expand.Config
	ectx context.Context // just so that Runner.Subshell can use it again

	// didReset remembers whether the runner has ever been reset. This is
	// used so that Reset is automatically called when running any program
	// or node for the first time on a Runner.
	didReset bool

	usedNew bool

	filename            string // only if Node was a File, or set for incremental runs
	incrementalFilename string
	interactiveShell    bool
	mirrorUmask         bool
	commandString       bool
	standardInput       bool

	// curStmtPos is the position of the currently executing top-level
	// statement, updated at the top of stmtSync. Error sites that have
	// no other pos to hand (setVar/readonly, builtin-internal failures
	// reached via paths that don't carry a Pos) use it to drive
	// [Runner.bashErrPrefix] so the `<file>: line N:` prefix lands.
	curStmtPos syntax.Pos
	curStmtEnd syntax.Pos

	// discardNextStmt keeps one following top-level statement skipped
	// for bash arithmetic-expansion errors that abort the rest of a
	// physical line rather than only the current simple command.
	discardNextStmt bool

	// discardRestOfLine, when non-zero, names a source line whose
	// remaining top-level statements bash skips. A standalone assignment
	// to a readonly variable (`RO=z; echo skipped`) aborts the rest of
	// its physical line but not subsequent lines, so the runner skips
	// every following top-level statement sharing this line number.
	discardRestOfLine uint

	// enclosingSubshellEnd is set while executing statements inside a
	// foreground subshell. Bash 5.3 reports some fatal declaration errors
	// at the closing ")" rather than at the inner declaration.
	enclosingSubshellEnd syntax.Pos

	// setVarFromBuiltin is non-empty while a declare-family builtin
	// (declare/typeset/local/readonly/export) is parsing a string
	// argument like `readonly -a 'name=value'` and assigning into the
	// variable space. When set, [Runner.setVar]'s error message uses
	// the builtin's name (e.g. "readonly:") instead of the enclosing
	// function name. Bash 5.3 attributes array-conversion-from-string
	// errors to the builtin and syntax-level assignment errors to the
	// enclosing function.
	setVarFromBuiltin string

	// setVarStringParsed is true while a declare-family builtin is
	// processing a string-form arg (`readonly 'name=value'`), even
	// when no array flag is in use. Bash 5.3 suppresses the function-
	// name attribution for these — the error gets no extra prefix
	// beyond `<file>: line N: <var>:`.
	setVarStringParsed bool

	// setVarArrayLiteral is true while a declare-family builtin is
	// processing a syntax-level array-literal assignment
	// (`readonly -a a=(1 2 3)`). Bash 5.3 attributes a failure here
	// to the enclosing function; scalar syntax-level assignments
	// (`readonly r='(5)'`) get no extra prefix.
	setVarArrayLiteral bool

	// declAssignContext is true while a declare-family clause is
	// processing its assignments. Some bash semantics differ between
	// declare-context (`declare a=v`, `readonly a=v`) and an inline
	// prefix-assignment (`a=v cmd`), notably preserving an existing
	// variable's array kind on scalar assignment.
	declAssignContext bool

	// exportedFuncs tracks function names marked for export via
	// `export -f <name>`. When the runner spawns a child process,
	// each entry becomes a `BASH_FUNC_<name>%%=() { … }` env var
	// so the child can re-import the function on startup. Bash's
	// way of propagating shell functions across `exec`.
	exportedFuncs map[string]bool

	// readonlyFuncs tracks function names marked read-only via
	// `readonly -f <name>`. Subsequent redefinition or unset of
	// the function is rejected, and `declare -fr` / `readonly -f`
	// lists only these names.
	readonlyFuncs map[string]bool

	// funcTrace tracks function names given the trace attribute via
	// `declare -ft <name>` (or `declare -tf`). A traced function
	// inherits the DEBUG and RETURN traps even without the global
	// `set -T` (functrace) option — see [Runner.shouldFireDebugTrap].
	funcTrace map[string]bool

	// inlineLeakFromFunc records variable names whose inline
	// assignments leaked out of a function-scoped special-builtin
	// invocation (`var=N return …`). The outer inline-restore
	// loop checks this map and skips restoring affected names
	// so the leaked value survives. Cleared by callers after
	// they handle their own restore.
	inlineLeakFromFunc map[string]bool

	// tempEnv records variable names bound by inline assignments for
	// the in-flight call (`a=4 foo`). Bash lets `local a` (no value)
	// inside the callee inherit the temporary-environment value even
	// without shopt localvar_inherit; setVar consults this set to
	// keep that inherit while plain exported globals stay uninherited.
	// Saved/restored around each call so nested calls see the outer
	// binding (the temp env spans the whole call stack).
	tempEnv map[string]bool

	// argv0 is bash's $0 / $BASH_ARGV0 — initialized from filename
	// but separately settable by user code. Error-message prefixes
	// continue to use filename so they stay stable across user
	// reassignments of BASH_ARGV0.
	argv0 string

	// origArgv0 preserves an explicitly configured argv0 (see [WithArgv0])
	// across [Runner.Reset], which otherwise zeroes the live argv0.
	origArgv0 string

	// >0 to break or continue out of N enclosing loops
	breakEnclosing, contnEnclosing int

	inLoop        bool
	inFunc        bool
	inSource      bool
	inTimeClause  bool       // suppress inner `time` keyword's output
	handlingTrap  bool       // whether we're currently in a trap callback
	trapEntryExit exitStatus // exit status upon entering the current trap
	xtraceLevel   int        // xtrace indirection depth (trap handlers add one)

	// track if a sourced script set positional parameters
	sourceSetParams bool

	// noErrExit prevents failing commands from triggering [optErrExit],
	// such as the condition in a [syntax.IfClause].
	noErrExit bool

	// The current and last exit statuses. They can only be different if
	// the interpreter is in the middle of running a statement. In that
	// scenario, 'exit' is the status for the current statement being run,
	// and 'lastExit' corresponds to the previous statement that was run.
	exit     exitStatus
	lastExit exitStatus

	lastExpandExit     exitStatus // used to surface exit statuses while expanding fields
	lastExpandCmdSubst bool       // whether lastExpandExit came from a command substitution
	expandRunExit      exitStatus // expansion failures which affect Run but not $?

	// outErr records the most recent error from r.out/r.outf (a builtin's
	// standard-output write). Bash fails an output builtin whose stdout write
	// hits a closed fd (`echo >&-`) with a non-zero exit; the builtin
	// dispatcher inspects this after running each builtin.
	outErr error

	// stdinClosed marks fd 0 as explicitly closed (`cmd <&-`). r.stdin is
	// nil in that case (as for "no stdin"), but a closed fd must make a
	// spawned command's read fail with EBADF rather than read an empty
	// /dev/null, so the exec handler synthesizes a genuinely closed
	// descriptor. Saved/restored around each statement's redirects.
	stdinClosed bool

	// lastArithErr captures the most recent error from r.arithm so
	// callers (notably the C-style for-loop) can detect arithmetic
	// failures and abort, matching bash 5.3 — a runtime arith error
	// inside `for ((init; cond; post))` terminates the loop. The
	// runner clears it before each arithm call site that cares.
	lastArithErr error

	// bashSource holds the original script bytes for bash-compatible
	// diagnostics that must preserve source spelling rather than printer
	// output, notably arithmetic errors.
	bashSource []byte

	// stdinSource tracks source bytes that originally came from fd 0.
	// bashy buffers stdin before parsing; reads from sourced scripts and
	// child commands must still consume the same logical input stream.
	stdinSourceActive     bool
	stdinSourceOffset     int
	stdinSourceBaseOffset int
	// stdinRedirected is set while a command's fd 0 is bound to an explicit
	// input redirect (`<`, `<<`, `<<<`). It suppresses the stdin-source reader
	// above, so a heredoc/here-string/file redirect wins over the
	// "stdin-script commands consume subsequent lines" feature — matching bash,
	// where `cat <<E` reads the heredoc body, not later script lines. Saved and
	// restored around each statement's redirects like r.stdin.
	stdinRedirected bool

	// aliasLineOverride is non-zero while expanding a multi-stmt
	// alias body. bashErrPrefix prefers it over the AST stmt's own
	// Pos().Line() so runtime errors from inside an alias body
	// (`command not found`, etc.) report the call site's line in
	// the source script rather than the body-relative line in the
	// alias-body parse.
	aliasLineOverride int
	aliasReparseDepth int

	// aliasBase and aliasDefOverride implement bash's alias-definition
	// *timing*: an alias is expanded only for input read after the line
	// that defined it (commands on the same parse unit / same line as the
	// definition are not affected). The runner walks a pre-parsed AST
	// rather than reading line-by-line, so timing is reconstructed from
	// effective line numbers:
	//
	//   defLine(alias) = aliasDefOverride>0 ? aliasDefOverride : aliasBase+pos.Line()
	//   useLine(cmd)   = aliasBase + cmd.Pos().Line()
	//   expand iff useLine > defLine
	//
	// aliasBase shifts a freshly re-parsed unit (eval/source bodies,
	// successive interactive input reads) past everything read before it,
	// so its tokens see all already-defined aliases while still honoring
	// internal line ordering. aliasDefOverride pins aliases *defined*
	// inside such a re-parsed unit to the outer call's line, matching
	// bash: `eval 'alias g=...'` makes g visible on the next outer line
	// but not on the same one.
	aliasBase        int
	aliasDefOverride int

	// funsubLineOffset is applied to bash-style diagnostic line
	// numbers while executing a multi-line `${ ...; }` funsub body.
	// Bash 5.3 reports runtime command diagnostics one line later
	// than the physical AST position in that form.
	funsubLineOffset int

	// assignNamerefName, when non-empty, overrides the variable name
	// used in a readonly-variable diagnostic from an array or
	// indexed-element assignment made through a nameref. Bash is
	// asymmetric here: a scalar nameref assignment (`r=5`) reports the
	// resolved target name, but an array append or element store
	// (`r+=(4)`, `r[0]=9`) reports the nameref name as written. It is
	// set narrowly around the assignment and cleared immediately after.
	assignNamerefName string

	// evalLineOffset is added to bash-style diagnostic line numbers
	// while executing the body of an `eval` (parsed from a string).
	// Bash keeps eval'd code anchored to the absolute script line
	// where it physically sits, so an `eval` on line N reports a
	// runtime error in its (line-1-based) body at N+line-1. It is
	// reset to zero across a function-body call, since a function
	// invoked from eval reports its own definition line, not the
	// eval call's. See the eval builtin and the function dispatch.
	// evalExec is non-zero whenever such a body is running, which also
	// redirects a few builtin-attributed diagnostics (array-conversion)
	// to `eval`.
	evalLineOffset int
	evalExec       int

	// bgProcs holds all background shells spawned by this runner.
	// Their PIDs are 1-indexed, from 1 to len(bgProcs), with a "g" prefix
	// to distinguish them from real PIDs on the host operating system.
	//
	// Note that each shell only tracks its direct children;
	// subshells do not share nor inherit the background PIDs they can wait for.
	bgProcs []*bgProc

	// inheritedBang is the caller's most recent background job, visible
	// only through $! in subshells. It is intentionally not waitable by
	// this runner; bash lets a subshell expand $! from its parent but
	// still reports "not a child of this shell" for wait $!.
	inheritedBang *bgProc

	// jobsReadOnly is set for command substitutions that may display the
	// parent's job table via `jobs` but may not manipulate those jobs with
	// `fg` or `bg`.
	jobsReadOnly bool

	// preferredJobID records a job Bash has explicitly made current, such
	// as a stopped job continued with `kill -CONT %N` or `bg %N`.
	preferredJobID int

	// doneBgPids keeps exit statuses for completed background jobs after
	// they leave bgProcs, so a later `wait <pid>` can still report the
	// saved status like bash's bgpids table.
	doneBgPids map[int64]exitStatus

	// coprocSeq counts coprocs started by this runner; it seeds the
	// synthetic, unique [bgProc.coprocPid] reported in `<NAME>_PID`.
	coprocSeq int64

	// coprocFds maps a live coproc pipe fd to the array variable element
	// it backs, so that closing the fd (e.g. `exec {fd}<&${COPROC[0]}-`)
	// rewrites that element to "-1", the way bash marks a closed coproc
	// descriptor. Entries are removed when the fd is closed or reaped.
	coprocFds map[int]coprocFdRef

	// coprocReg resolves a coproc's synthetic `<NAME>_PID` back to its
	// bgProc. It is shared by pointer with subshells (unlike bgProcs,
	// which subshells do not inherit) so that `kill $COPROC_PID` works
	// from a background subshell — bash's coproc pid is a real OS pid that
	// any process can signal, and this is how we reach the real child of a
	// coprocess run as a goroutine. Lazily created by the first coproc.
	coprocReg *coprocReg

	// coprocReapedFds records coproc pipe fds released by reapCoproc during
	// the current statement. stmtSync snapshots/restores fdTable around a
	// redirected statement; reaping a coprocess (e.g. via `wait $PID >f`)
	// closes its fds, and that removal must survive the restore rather than
	// being undone. Reset at the start of each stmtSync.
	coprocReapedFds map[int]bool

	opts runnerOpts

	origDir          string
	origParams       []string
	origOpts         runnerOpts
	origNoOpSetState map[string]bool
	origStdin        *os.File
	origStdout       io.Writer
	origStderr       io.Writer

	// Most scripts don't use pushd/popd, so make space for the initial PWD
	// without requiring an extra allocation.
	dirStack     []string
	dirBootstrap [1]string

	optState getopts

	// keepRedirs is used so that "exec" can make any redirections
	// apply to the current shell, and not just the command.
	keepRedirs bool

	// lateRedirs holds a simple command's redirections when they must be
	// applied AFTER its words are expanded rather than before — the POSIX
	// order (expand words, then perform redirections). stmtSync stashes them
	// here for a CallExpr whose arguments contain a command substitution, and
	// the CallExpr handler in cmd() applies them once the fields are expanded
	// (so e.g. `echo $(cat f) > f` reads f before the redirect truncates it).
	// lateRedirClosers collects the opened closers for stmtSync to close.
	lateRedirs       []*syntax.Redirect
	lateRedirClosers []io.Closer

	// trapCallbacks maps signal/pseudo-signal names to trap handler code.
	// Supported keys: EXIT, ERR, DEBUG, RETURN, and signal names like INT, TERM, etc.
	trapCallbacks map[string]string

	// inheritedExitTrap is set in subshells where EXIT is visible to
	// `trap -p`, but reset for execution until the subshell sets it.
	inheritedExitTrap bool

	// asyncList marks a runner executing a POSIX asynchronous list (`cmd &`).
	// Such runners get SIGINT/SIGQUIT ignored by default while job control is
	// disabled; external commands launched from them must inherit that state.
	asyncList bool

	// callStack tracks function call frames for caller/BASH_SOURCE/BASH_LINENO/FUNCNAME.
	callStack []callFrame

	// exitTrapCallStack preserves the function stack for an EXIT trap
	// triggered by `exit` from inside a function.
	exitTrapCallStack []callFrame

	// localOptStack tracks function frames that used `local -`,
	// which asks bash to restore shell option state on return.
	localOptStack []localOptFrame

	// cmdHashTable caches resolved command paths and lookup counts for the hash builtin.
	cmdHashTable map[string]cmdHashEntry

	// disabledBuiltins tracks builtins disabled via "enable -n".
	disabledBuiltins map[string]bool

	// dynamicBuiltins tracks fake dynamically-loaded builtins accepted
	// via "enable -f" for Bash compatibility tests.
	dynamicBuiltins map[string]bool

	// completionSpecs stores programmable-completion specs registered
	// via the complete builtin.
	completionSpecs map[string]completionSpec

	// noOpSetState tracks the on/off state of accept-and-ignore
	// `set -o` options (history, monitor, privileged, etc.). The
	// runner doesn't otherwise act on these flags, but it must
	// remember the value so `set -o` listings echo back what the
	// script most recently asserted.
	noOpSetState map[string]bool

	// bypassNoExec is set when a POSIX special builtin (e.g. `set`)
	// executes under `set -n` (noexec). Special builtins must execute
	// even when noexec is on, so they can toggle the option back off.
	bypassNoExec bool

	// settingIgnoreEOFOption is true while set/shopt is synchronizing
	// IGNOREEOF from the ignoreeof option, not from a user assignment.
	settingIgnoreEOFOption bool

	// bgPidCallback, when non-nil, is invoked with the OS PID of every
	// real process this runner spawns from a backgrounded statement
	// (`foo &`). Set via [WithBgPidCallback]. Outpost uses this to
	// publish detached PIDs to its job-control registry — the in-shell
	// `fg`/`bg`/`jobs` builtins are unimplemented because subshells
	// here are goroutines, not OS processes.
	bgPidCallback func(pid int)

	// pipeStatus tracks exit codes from the last pipeline for PIPESTATUS.
	pipeStatus []string

	// promptExpand is called by ${var@P} to expand prompt escape sequences.
	// If nil, a default basic expansion is used.
	promptExpand func(string) string

	// startTime records when the shell was created, for the SECONDS variable.
	startTime time.Time

	// subshellLevel tracks the nesting depth of subshells, for BASH_SUBSHELL.
	subshellLevel int

	// umask is this Runner's virtual file-creation mask. It is applied at
	// [Runner.open] when O_CREATE is set in the flags, never via
	// [syscall.Umask] (which would clobber other Runners in the same
	// process). A custom [OpenHandler] that bypasses [Runner.open]
	// won't see the mask applied.
	umask int

	// loginShell, when true, allows the `logout` builtin to exit the shell.
	// Set via [WithLoginShell]. Bash refuses `logout` from a non-login
	// shell with "not login shell: use 'exit'".
	loginShell bool

	// bashCompatErrors, when true, prefixes builtin error messages with
	// `<filename>: line <N>:` and uses bash's argument-first/no-quote
	// wording. Set via [WithBashCompatErrors] by [cmd/bashy] so the bash
	// 5.3 test suite output matches. Default off so library callers
	// (and the legacy TestRunnerRun tests) keep the old "<name>: <msg>
	// <arg>" wording without the line prefix.
	bashCompatErrors bool
	// dryRun is the bashy-only `set -o dryrun` option's state, readable by
	// handlers via [HandlerContext.DryRun]. dryRunOpt gates whether the option
	// is recognized at all — only hosts that pass [EnableDryRunOption] (i.e.
	// bashy, and not under --posix) accept `set -o dryrun`; everywhere else
	// (the pure bash drop-in, gosh) it errors like any unknown option.
	dryRun    bool
	dryRunOpt bool
	// orig* snapshot the construction-time dry-run state so Reset (which rebuilds
	// the Runner) restores it — dryRunOpt persists; dryRun returns to its initial.
	origDryRun    bool
	origDryRunOpt bool
	// handlingDebugTrap prevents DEBUG traps from recursively firing
	// for commands inside the DEBUG trap itself while still allowing
	// DEBUG to run inside other trap handlers such as RETURN.
	handlingDebugTrap bool

	// auditHandler, when non-nil, is invoked just before the runner
	// hands a simple command off to execHandler. It receives an
	// [AuditEvent] describing the command, its arguments, and the
	// source position. Used by agentic harnesses to log/observe what
	// the shell is about to run.
	auditHandler func(AuditEvent)
	auditLog     io.Writer

	// structuredErrorHandler, when non-nil, is invoked for known
	// user-facing diagnostics as they are emitted to stderr. It is an
	// additive observability hook; stderr output and exit semantics are
	// unchanged when it is set.
	structuredErrorHandler func(ErrorEvent)

	// deterministic toggles deterministic-mode behaviour: a seeded
	// PRNG for $RANDOM, frozen $SECONDS / $EPOCHSECONDS, and stable
	// $$ when [deterministicSeed] is set. Embedders enable it via
	// [WithDeterministic] for reproducible agent runs.
	deterministic     bool
	deterministicSeed int64
	deterministicRng  *mathrand.PCG

	// randomSeeded tracks whether $RANDOM has been explicitly assigned.
	// Once set, reads follow bash's own Park-Miller generator so
	// `RANDOM=42; echo $RANDOM $RANDOM` is reproducible.
	randomSeeded bool
	randomSeed   uint32

	// fdTable holds non-stdio file descriptors keyed by OS fd number.
	// 0/1/2 stay in stdin/stdout/stderr; everything else (coproc pipe
	// ends, future `exec N<file` targets) lives here. Lookups happen
	// when a script uses `<&N` / `>&N` with N >= 3. The map is shared
	// with subshells (fds are inherited in bash), but mutations in a
	// subshell do not leak back to the parent because the map itself
	// is cloned by [Runner.subshell].
	fdTable map[int]*os.File
	// fdReadTable marks fdTable entries that are valid input sources.
	fdReadTable map[int]bool

	// fdWriteTable holds write-only descriptors whose target is not a
	// real file, such as stdout captured by command substitution. Those
	// cannot be represented in fdTable but bash still allows `exec 4>&1`
	// and later `echo >&4` within the same shell/subshell.
	fdWriteTable map[int]io.Writer
	// fdClosedTable records inherited non-stdio descriptors explicitly
	// closed by the shell. This keeps lazy inherited-fd lookup from
	// resurrecting a descriptor the script has logically closed.
	fdClosedTable map[int]bool

	// stmtTraceOutput snapshots xtrace output before a simple command's
	// own redirections are applied. Bash traces `cmd 2>file` to the
	// shell's current xtrace sink, not to file.
	stmtTraceOutput io.Writer

	// redirMoveCloseFds tracks source fds closed by N>&M- or N<&M- while
	// applying a statement's redirects. Bash keeps those source closes
	// after the statement's redirection frame is restored.
	redirMoveCloseFds map[int]bool

	inheritedFds map[int]bool

	// ulimitOverride records pseudo-set values from `ulimit -X N`
	// so the next `ulimit -X` read returns them. We don't actually
	// call setrlimit (would affect the whole process and need
	// permissions); the map is purely cosmetic.
	ulimitOverride map[string]string

	// testIntErr is set non-empty when an integer test operator
	// (`-eq`, `-lt`, …) saw a non-integer operand; the test
	// builtin uses this to mirror bash's "integer expected" error
	// and exit 2 after binTest returns. Cleared on next test
	// builtin entry.
	testIntErr string

	// testArithErr captures an arithmetic-syntax error encountered
	// while parsing a [[ ]] arithmetic operand (`[[ 7 -eq 4+ ]]`).
	// It is non-empty alongside the offending operand in
	// [Runner.testIntErr]; TestClause prints the bash 5.3
	// arithmetic-syntax-error wording instead of "integer expected"
	// when this is set, and the exit status is 1, not 2.
	testArithErr string

	// Signal-trap delivery (see signal.go). Lazily initialized the first
	// time `trap` registers a handler for a real OS signal. A goroutine
	// fed by signal.Notify records received signals in pendingSig; the
	// command-execution loop drains them at statement boundaries and runs
	// the corresponding trap handlers, letting their control flow
	// (return/exit/break/continue) unwind normally. These are deliberately
	// NOT inherited by subshells: a subshell that self-signals must send a
	// real OS signal so the parent's Notify catches it.
	sigMu         sync.Mutex
	sigCh         chan os.Signal
	pendingSig    map[string]int       // signal name -> pending count, guarded by sigMu
	sigNotify     map[string]os.Signal // signal name -> os.Signal under signal.Notify
	sigIgnored    map[string]bool      // signal name -> set to real SIG_IGN via `trap '' SIG`
	sigWake       chan struct{}        // wakes a blocked wait when a signal arrives
	hasPendingSig atomic.Bool          // fast-path: any pending signal?

	// sigParent links a foreground subshell back to the runner it runs
	// inline within (nil for the top-level shell and for background/async
	// subshells, which run in their own goroutine). A foreground subshell's
	// $$ is the parent shell's PID, so a self-directed `kill -SIG $$` whose
	// trap the parent owns must be delivered synchronously into the parent's
	// pending queue rather than via a racy OS signal — see
	// [Runner.owningSignalRunner] and the kill builtin.
	sigParent *Runner

	// chldTrapActive mirrors whether a non-empty SIGCHLD trap is currently
	// installed. Read from background-job goroutines and the exec handler to
	// decide whether to queue a CHLD trap per reaped child, so it's an atomic
	// rather than a guarded map read.
	chldTrapActive atomic.Bool

	// startupIgnored is the set of signals that were SIG_IGN when this shell
	// process started — inherited from a parent shell that ran `trap '' SIG`
	// before exec'ing us (carried in via BashyHardIgnoreEnv). Bash flags these
	// as SIG_HARD_IGNORE: a `trap` that targets them is a silent no-op and they
	// list as `trap -- '' SIG`. Computed once at first Reset and shared (read
	// only) with subshell clones.
	startupIgnored map[string]bool

	// inSignalTrap and friends implement POSIX interp 1602: a `return`
	// with no argument executed directly in a signal-trap action yields
	// the $? in effect when the trap was invoked (signalTrapExit), not the
	// trap's own last command. A `return` inside a function *called* from
	// the action uses normal semantics — distinguished by comparing the
	// call-stack depth against signalTrapDepth (the depth at trap entry).
	inSignalTrap    bool
	signalTrapDepth int
	signalTrapExit  uint8
}

// exitStatus holds the state of the shell after running one command.
// Beyond the exit status code, it also holds whether the shell should return or exit,
// as well as any Go error values that should be given back to the user.
//
// TODO(v4): consider replacing ExitStatus with a struct like this,
// so that an [ExecHandlerFunc] can e.g. mimic `exit 0` or fatal errors
// with specific exit codes.
type exitStatus struct {
	// code is the exit status code.
	// When code is zero, err must be nil.
	code uint8

	// TODO: consider an enum, as only one of these should be set at a time
	returning     bool // whether the current function `return`ed
	exiting       bool // whether the current shell is exiting
	fatalExit     bool // whether the current shell is exiting due to a fatal error; err below must not be nil
	noNegate      bool // whether a surrounding `!` must not invert this status
	errexitExempt bool // whether this failure inherited an errexit exemption

	// discarding qualifies exiting: a variable-assignment error in a
	// non-interactive non-POSIX shell aborts the current top-level
	// command (bash's DISCARD longjmp) rather than the whole shell.
	// It unwinds via the exiting machinery and is converted back to a
	// plain non-zero status at the end of [Runner.Run]; whenever
	// exiting is cleared (subshell boundaries), discarding must be
	// cleared with it.
	discarding bool

	// err holds the error information for a non-zero exit status code or fatal error.
	// Used so that running a single statement with a custom handler
	// which returns a non-fatal Go error, such as a Go error wrapping [NewExitStatus],
	// can be returned by [Runner.Run] without being lost entirely.
	err error
}

// clear sets the exit status code and error to zero, as long as the exit status
// was not set by `return`, `exit`, or a fatal error.
func (e *exitStatus) clear() {
	if e.returning || e.exiting || e.fatalExit {
		return
	}
	e.code = 0
	e.err = nil
}

func (e *exitStatus) ok() bool { return e.code == 0 }

// oneIf sets the exit status code to 1 if b is true.
// Note that it assumes the exit status hasn't been set yet,
// meaning that [exitStatus.code] and [exitStatus.err] are zero values.
func (e *exitStatus) oneIf(b bool) {
	if b {
		e.code = 1
	}
}

func (e *exitStatus) fatal(err error) {
	if e.fatalExit || err == nil {
		return
	}
	e.exiting = true
	e.fatalExit = true
	e.err = err
	if e.code == 0 {
		e.code = 1
	}
}

func (e *exitStatus) fromHandlerError(err error) {
	if err == nil {
		return
	}
	var exit errBuiltinExitStatus
	var es ExitStatus
	if errors.As(err, &exit) {
		*e = exitStatus(exit)
	} else if errors.As(err, &es) {
		e.err = err
		e.code = uint8(es)
	} else {
		e.fatal(err) // handler's custom fatal error
	}
}

// callFrame records a function call for the call stack.
type callFrame struct {
	line         uint
	source       string
	callerSource string
	funcName     string
	args         []string
	// bodyLine is the line of the function body's opening token.
	// Bash's parser stamps for/select commands inside a function with
	// this line rather than their own, and runtime diagnostics like
	// "not a valid identifier" surface it.
	bodyLine uint
	// debugTrace reports whether the DEBUG/RETURN traps fire inside
	// this function frame. It is true when the global functrace
	// option is set or the function carries the trace attribute
	// (`declare -ft`), and is also turned on if a `trap ... DEBUG`
	// command runs within the frame (which activates the trap for the
	// remainder of that function, as bash does).
	debugTrace bool
}

type bgProc struct {
	// closed when the background process finishes,
	// after which point the result fields below are set.
	done chan struct{}

	exit *exitStatus

	cmd string

	cancel context.CancelFunc

	killedSignal atomic.Int32

	state atomic.Int32

	ignoreNextStop atomic.Int32

	ignoreNextContinue atomic.Int32

	// stopSignal is the SIG-prefixed name (e.g. "SIGTSTP", "SIGSTOP") of
	// the signal that last stopped this job. Recorded so `jobs` in POSIX
	// mode can print "Stopped(SIGTSTP)" matching bash's printable_job_status.
	// Empty until the job is stopped, and stays empty on platforms without a
	// job-control wait path. Guarded by stopSignalMu because the unix
	// job-control waiter writes it from the bg goroutine while `jobs` reads it.
	stopSignalMu sync.Mutex
	stopSignal   string

	// jobID is the stable job-table number reported by `jobs`, `%N` job
	// specs and `$!`'s "g<N>" sentinel. It is assigned the lowest free
	// slot when a `&` background job is created and never changes while
	// the job lives, so removing a finished job does not renumber the
	// survivors (mirroring bash's job-table slots). Zero for coproc and
	// process-substitution bgProcs, which are not listed as jobs.
	jobID int

	// pid is the OS PID of the last real external process this
	// backgrounded statement spawned. Zero until set, and stays zero for
	// pure-builtin or pure-goroutine subshells (e.g. `(true) &`,
	// `(echo hi) &`). Updated atomically because the exec handler
	// (DefaultExecHandler / runDetachedExec) writes it from the
	// background goroutine while the parent shell reads it via `$!`.
	pid atomic.Int64

	// pidReady is closed once the bg goroutine has either set pid via a
	// real exec.Start, or finished running entirely without ever doing
	// so. `$!` waits on this channel so it can return a real PID instead
	// of the "g<N>" sentinel whenever one is actually available — the
	// usual `PID=$!; kill $PID` idiom relies on this.
	pidReady chan struct{}

	// publishPidToBang controls whether the first real exec PID should be
	// exposed through $!. Simple external-command backgrounds want that;
	// compound shell jobs keep the synthetic g<N> handle so signals target
	// the whole in-process job, not one inner child.
	publishPidToBang bool

	// pids records every OS PID published by this background statement.
	// Compound commands can exec more than once after `$!` has already
	// captured the first pid; wait must still map that pid to this job.
	pidsMu sync.Mutex
	pids   []int64

	// pidCallback is the runner's WithBgPidCallback hook copied in at
	// spawn time (so publishBgPid doesn't need to reach for the Runner).
	// nil means "no embedder cares". Invoked synchronously from
	// publishBgPid with the OS PID — embedders that want async fan-out
	// should hand off to a goroutine themselves.
	pidCallback func(pid int)

	// jobControl records whether monitor mode (`set -m`) was active when
	// this job was backgrounded. `fg`/`bg` refuse a job that was not
	// started under job control, even if monitor mode is later enabled.
	jobControl bool

	// coprocReadonly names a readonly variable that a `coproc` failed to
	// bind its fd array to. Bash defers unsetting the coproc variable
	// until the coprocess is reaped, so reaping a readonly one reports
	// `<var>: cannot unset: readonly variable` (see Runner.reapCoproc).
	// Empty for non-coproc background jobs and writable coproc variables.
	coprocReadonly string

	// coprocReadonlyPos is the position of the `coproc` clause, used to
	// drive the `cannot unset` diagnostic's line number at reap time.
	coprocReadonlyPos syntax.Pos

	// coprocPidVar names the `<NAME>_PID` variable a `coproc` is
	// responsible for. Bash's coproc_unsetvars unbinds it (without
	// following namerefs and ignoring the readonly attribute) when the
	// coprocess is reaped, so reaping force-removes it here. Empty for
	// non-coproc background jobs and for coprocs whose name was invalid.
	coprocPidVar string

	// coprocPid is the synthetic, runner-unique integer reported in the
	// `<NAME>_PID` variable so scripts can `wait $COPROC_PID` /
	// `kill $COPROC_PID`. Real bash hands out the coprocess subshell's OS
	// PID; this runner runs the coprocess as a goroutine with no PID of
	// its own, so we mint a stable integer here and resolve it back to
	// this bgProc in wait/kill. Zero for non-coproc background jobs.
	coprocPid int64

	// coprocReadFd / coprocWriteFd are the logical fd numbers bound to the
	// coproc's `${NAME[0]}` / `${NAME[1]}` array. They are released from
	// the runner's fd tables when the coprocess is reaped so a later
	// coproc reuses the same high fds, matching bash. Zero when unset.
	coprocReadFd  int
	coprocWriteFd int

	// coprocReadFile / coprocWriteFile are the parent-side pipe ends to
	// close on reap (mirroring bash closing c_rfd / c_wfd in
	// coproc_dispose). nil for non-coproc background jobs.
	coprocReadFile  *os.File
	coprocWriteFile *os.File
}

type jobState int32

const (
	jobRunning jobState = iota
	jobStopped
	jobDead
)

func (bg *bgProc) setState(state jobState) {
	bg.state.Store(int32(state))
}

func (bg *bgProc) jobState() jobState {
	return jobState(bg.state.Load())
}

// setStopSignal records the SIG-prefixed name of the signal that stopped
// this job, for POSIX-mode `jobs` reporting.
func (bg *bgProc) setStopSignal(name string) {
	bg.stopSignalMu.Lock()
	bg.stopSignal = name
	bg.stopSignalMu.Unlock()
}

// getStopSignal returns the SIG-prefixed name of the signal that last
// stopped this job, or "" if it has never been stopped.
func (bg *bgProc) getStopSignal() string {
	bg.stopSignalMu.Lock()
	defer bg.stopSignalMu.Unlock()
	return bg.stopSignal
}

// coprocFdRef identifies which coproc array element a live pipe fd
// backs, so closing the fd can rewrite that element to "-1".
type coprocFdRef struct {
	varName string
	idx     int
}

// coprocReg maps a coproc's synthetic `<NAME>_PID` to its bgProc so that
// `wait`/`kill` can resolve it — including from a background subshell,
// which shares this registry by pointer. Safe for concurrent use.
type coprocReg struct {
	mu    sync.Mutex
	byPid map[int64]*bgProc
}

func (c *coprocReg) add(pid int64, bg *bgProc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byPid == nil {
		c.byPid = make(map[int64]*bgProc)
	}
	c.byPid[pid] = bg
}

func (c *coprocReg) lookup(pid int64) *bgProc {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byPid[pid]
}

func (c *coprocReg) remove(pid int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byPid, pid)
}

// bgProcCtxKey is the context-value key under which the bg goroutine
// stashes a pointer to its own bgProc. The default exec handler and the
// nohup/setsid builtins read this back so they can publish the OS PID
// of the process they just spawned. Nil means "not running in a
// backgrounded subshell" — exec handlers in that case skip the publish
// path entirely.
type bgProcCtxKey struct{}

// bgNonPrimaryPidCtxKey marks background pipeline elements whose child PIDs
// belong to the job for wait/cleanup purposes, but must not replace $!.
type bgNonPrimaryPidCtxKey struct{}

// publishBgPid is what exec handlers call after a successful
// exec.Start. Sets the running goroutine's bgProc.pid and records the
// published pid for later `wait $!` resolution. Safe no-op when not in a
// backgrounded context.
func publishBgPid(ctx context.Context, pid int) {
	bg, _ := ctx.Value(bgProcCtxKey{}).(*bgProc)
	if bg == nil {
		return
	}
	pid64 := int64(pid)
	bg.pidsMu.Lock()
	bg.pids = append(bg.pids, pid64)
	bg.pidsMu.Unlock()
	if nonPrimary, _ := ctx.Value(bgNonPrimaryPidCtxKey{}).(bool); nonPrimary || !bg.publishPidToBang {
		return
	}
	bg.pid.Store(pid64)
	select {
	case <-bg.pidReady:
		// already closed by an earlier exec or by goroutine exit
	default:
		close(bg.pidReady)
	}
	if bg.pidCallback != nil {
		bg.pidCallback(pid)
	}
}

type alias struct {
	// text is the literal alias body exactly as supplied (the part
	// after `=`, including any trailing whitespace). Bash stores and
	// displays alias bodies verbatim, so this is what `alias`/`alias -p`,
	// `type`, and ${BASH_ALIASES[...]} render — args/file/raw below are
	// the re-parsed forms used only at expansion time.
	text string
	// raw is set for bodies Bash accepts as text even though they are
	// not standalone parseable shell words.
	raw string
	// args is set for the common "simple call" alias case
	// (`alias l='ls -la'`); inlined into the surrounding CallExpr's
	// arg list at expansion time.
	args []*syntax.Word
	// file is set for aliases whose body parses to multiple
	// statements or non-CallExpr commands (e.g. with embedded
	// newlines: `alias foo=$'echo a\necho b'`). At expansion time the
	// surrounding command is replaced by running the parsed
	// statements; args is nil in this case.
	file  *syntax.File
	blank bool
	// defLine is the effective input line at which this alias became
	// defined (see [Runner.aliasDefLine]). Bash expands an alias only on
	// input read *after* the line that defined it, so a use is expanded
	// only when its effective line is strictly greater than defLine. Zero
	// means "no position recorded" (e.g. an alias installed on a
	// programmatically-built AST), in which case the timing gate is
	// skipped and the alias always expands.
	defLine int
}

// aliasDefLine returns the effective line to record for an alias defined
// at AST line astLine, honoring the current alias-timing scope. See the
// aliasBase / aliasDefOverride fields on [Runner].
func (r *Runner) aliasDefLine(astLine int) int {
	if r.aliasDefOverride > 0 {
		return r.aliasDefOverride
	}
	return r.aliasBase + astLine
}

// aliasUseLine returns the effective line of a command at AST line astLine
// for alias-timing comparisons.
func (r *Runner) aliasUseLine(astLine int) int {
	return r.aliasBase + astLine
}

// withAliasReparse runs fn while the alias-timing scope is set for a
// freshly re-parsed unit (eval/source body) whose tokens were read just
// after the outer command at effective line outerLine. Inside fn, uses see
// every alias defined up to outerLine and aliases defined are pinned to
// outerLine for outer visibility. The scope is restored afterward.
func (r *Runner) withAliasReparse(outerLine int, fn func()) {
	prevBase, prevOverride := r.aliasBase, r.aliasDefOverride
	r.aliasBase = outerLine
	r.aliasDefOverride = outerLine
	defer func() {
		r.aliasBase, r.aliasDefOverride = prevBase, prevOverride
	}()
	fn()
}

func (r *Runner) trapAliasReparseLine() int {
	line := r.aliasUseLine(int(r.curStmtPos.Line()))
	for _, als := range r.alias {
		if als.defLine >= line {
			line = als.defLine + 1
		}
	}
	if line < 1 {
		line = 1
	}
	return line
}

// AdvanceAliasInput marks the start of a newly-read unit of interactive
// input spanning lineCount lines. Interactive front-ends parse each input
// chunk independently (line numbers restart at 1), so this advances the
// alias-timing base past the previous chunk, letting aliases defined on an
// earlier prompt expand on later ones while a definition and use typed on
// the same line still do not expand. Safe to call between input reads.
func (r *Runner) AdvanceAliasInput(lineCount int) {
	if lineCount < 1 {
		lineCount = 1
	}
	r.aliasBase += lineCount
}

// ConsumedSourceOffset reports how far into the original bash source the
// runner has consumed input while executing parsed statements.
func (r *Runner) ConsumedSourceOffset() int {
	return r.stdinSourceOffset
}

// RunAliasExpandedSourceLine expands aliases on one physical bash source line
// and runs it if the expanded line parses successfully. It is used by callers
// that parse incrementally and need bash's POSIX command-substitution rule:
// aliases are expanded while parsing $(...), so an alias body can provide the
// closing ')'.
func (r *Runner) RunAliasExpandedSourceLine(ctx context.Context, line int) bool {
	if line <= 0 || !r.opts[optExpandAliases] || len(r.bashSource) == 0 {
		return false
	}
	pos := syntax.NewPos(uint(r.sourceLineEndOffset(uint(line-1))), uint(line), 1)
	if file, ok := r.aliasReparsePhysicalLine(pos, r.aliasUseLine(line)); ok {
		r.runAliasReparseFile(ctx, pos, r.aliasUseLine(line), file)
		return true
	}
	return false
}

// New creates a new Runner, applying a number of options. If applying any of
// the options results in an error, it is returned.
//
// Any unset options fall back to their defaults. For example, not supplying the
// environment falls back to the process's environment, and not supplying the
// standard output writer means that the output will be discarded.
func New(opts ...RunnerOption) (*Runner, error) {
	r := &Runner{
		usedNew:        true,
		openHandler:    DefaultOpenHandler(),
		readDirHandler: DefaultReadDirHandler2(),
		statHandler:    DefaultStatHandler(),
	}
	r.dirStack = r.dirBootstrap[:0]
	// turn "on" the default Bash options
	for i, opt := range bashOptsTable {
		r.opts[len(posixOptsTable)+i] = opt.defaultState
	}

	for _, opt := range opts {
		if err := opt(r); err != nil {
			return nil, err
		}
	}

	// Set the default fallbacks, if necessary.
	if r.Env == nil {
		Env(nil)(r)
	}
	if r.Dir == "" {
		if err := Dir("")(r); err != nil {
			return nil, err
		}
	}
	if r.stdout == nil || r.stderr == nil {
		StdIO(r.stdin, r.stdout, r.stderr)(r)
	}
	if !r.deterministic {
		if value := r.Env.Get("BASHY_DETERMINISTIC").String(); value != "" {
			seed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				seed = 0
			}
			WithDeterministic(seed)(r)
		}
	}
	r.importExportedFuncs()
	if r.auditLog == nil {
		if path := r.Env.Get("BASHY_AUDIT_LOG").String(); path != "" {
			f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o666)
			if err != nil {
				return nil, err
			}
			r.auditLog = f
		}
	}
	return r, nil
}

func (r *Runner) importExportedFuncs() {
	r.Env.Each(func(name string, vr expand.Variable) bool {
		if !vr.IsSet() || vr.Kind != expand.String {
			return true
		}
		fname, ok := strings.CutPrefix(name, "BASH_FUNC_")
		if !ok {
			return true
		}
		fname, ok = strings.CutSuffix(fname, "%%")
		if !ok || !validExportedFuncName(fname) || !validBashFuncName(fname) {
			return true
		}
		body := vr.Str
		if !strings.HasPrefix(body, "()") {
			return true
		}
		src := fname + " " + body
		file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), "")
		if err != nil || len(file.Stmts) != 1 {
			return true
		}
		fd, ok := file.Stmts[0].Cmd.(*syntax.FuncDecl)
		if !ok || fd.Name.Value != fname {
			return true
		}
		if r.Funcs == nil {
			r.Funcs = make(map[string]*syntax.Stmt)
		}
		r.Funcs[fname] = fd.Body
		if r.funcSources == nil {
			r.funcSources = make(map[string]string)
		}
		r.funcSources[fname] = r.filename
		return true
	})
}

// RunnerOption can be passed to [New] to alter a [Runner]'s behaviour.
// It can also be applied directly on an existing Runner,
// such as interp.Params("-e")(runner).
// Note that options cannot be applied once Run or Reset have been called.
type RunnerOption func(*Runner) error

// TODO: enforce the rule above via didReset.

// Env sets the interpreter's environment. If nil, a copy of the current
// process's environment is used.
func Env(env expand.Environ) RunnerOption {
	return func(r *Runner) error {
		if env == nil {
			env = expand.ListEnviron(os.Environ()...)
		}
		r.Env = env
		return nil
	}
}

// Dir sets the interpreter's working directory. If empty, the process's current
// directory is used.
func Dir(path string) RunnerOption {
	return func(r *Runner) error {
		if path == "" {
			path, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("could not get current dir: %w", err)
			}
			r.Dir = path
			return nil
		}
		path, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("could not get absolute dir: %w", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("could not stat: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", path)
		}
		r.Dir = path
		return nil
	}
}

// PromptExpand sets a function to expand prompt escape sequences for ${var@P}.
// If not set, a basic default expansion is used.
func PromptExpand(fn func(string) string) RunnerOption {
	return func(r *Runner) error {
		r.promptExpand = fn
		return nil
	}
}

// Interactive configures the interpreter to behave like an interactive shell,
// akin to Bash. Currently, this only enables the expansion of aliases,
// but later on it should also change other behavior.
// MirrorUmask makes the `umask` builtin also set the process-wide umask via
// [syscall.Umask], so external commands (e.g. `mkdir`) honour the shell's
// umask. This is OFF by default because the library supports many Runners per
// process and the process umask is global; a standalone shell binary running
// ONE Runner per process (e.g. the bashy drop-in) enables it for POSIX
// fidelity. Subshells do not propagate it, so a goroutine subshell can't
// clobber the parent's process umask.
func MirrorUmask(enabled bool) RunnerOption {
	return func(r *Runner) error {
		r.mirrorUmask = enabled
		return nil
	}
}

func Interactive(enabled bool) RunnerOption {
	return func(r *Runner) error {
		r.interactiveShell = enabled
		r.opts[optExpandAliases] = enabled
		return nil
	}
}

// CommandString marks the runner as executing a command supplied via
// Bash's `-c` mode. This affects dynamic shell state such as `$-`.
func CommandString(enabled bool) RunnerOption {
	return func(r *Runner) error {
		r.commandString = enabled
		return nil
	}
}

// StandardInput marks the runner as executing commands read from standard
// input. This affects dynamic shell state such as `$-`.
func StandardInput(enabled bool) RunnerOption {
	return func(r *Runner) error {
		r.standardInput = enabled
		return nil
	}
}

// Params populates the shell options and parameters. For example, Params("-e",
// "--", "foo") will set the "-e" option and the parameters ["foo"], and
// Params("+e") will unset the "-e" option and leave the parameters untouched.
//
// This is similar to what the interpreter's "set" builtin does.
func Params(args ...string) RunnerOption {
	return func(r *Runner) error {
		fp := flagParser{remaining: args}
		for fp.more() {
			flag := fp.flag()
			if flag == "-" {
				// POSIX: a lone `-` ends option processing and, as a
				// special case, turns off the -x and -v options. Remaining
				// args become positional parameters (unchanged if none).
				r.opts[optXTrace] = false
				if r.noOpSetState == nil {
					r.noOpSetState = make(map[string]bool)
				}
				r.noOpSetState["verbose"] = false
				if args := fp.args(); len(args) > 0 {
					r.Params = args
				}
				return nil
			}
			if flag == "+" {
				// A lone `+` ends option processing like `-` (without the
				// legacy -v/-x reset): remaining args become positional
				// parameters, and with none the parameters are unchanged.
				// Without this, flag[1] below panics on the one-char "+"
				// (`set + -` crashed with index out of range).
				if args := fp.args(); len(args) > 0 {
					r.Params = args
				}
				return nil
			}
			enable := flag[0] == '-'
			if flag[1] != 'o' {
				if flag[1] == 'O' {
					value := fp.value()
					opt, supported := r.bashOptByName(value)
					if opt == nil || !supported {
						return fmt.Errorf("invalid option: %q", value)
					}
					*opt = enable
					continue
				}
				if flag[1] == 'B' {
					if r.noOpSetState == nil {
						r.noOpSetState = make(map[string]bool)
					}
					r.noOpSetState["braceexpand"] = enable
					continue
				}
				if flag == "+r" {
					return fmt.Errorf("+r: invalid option")
				}
				opt := r.posixOptByFlag(flag[1])
				if opt == nil {
					// Accept-and-ignore single-letter options
					// that bash supports but we don't model. The
					// on/off state is still tracked (in
					// noOpSetState) so it surfaces in $-.
					if name, ok := noOpSetFlagToName[flag[1]]; ok {
						if r.noOpSetState == nil {
							r.noOpSetState = make(map[string]bool)
						}
						r.noOpSetState[name] = enable
						continue
					}
					return fmt.Errorf("invalid option: %q", flag)
				}
				*opt = enable
				if opt == &r.opts[optRestricted] && enable {
					r.markRestrictedVarsReadonly()
				}
				continue
			}
			value := fp.value()
			if value == "deterministic" {
				r.deterministic = enable
				if enable {
					r.deterministicRng = mathrand.NewPCG(uint64(r.deterministicSeed), uint64(r.deterministicSeed)^0x9E3779B97F4A7C15)
				}
				continue
			}
			if value == "" && enable {
				// `set -o` (no name): bash lists all options
				// including no-op aliases (history, hashall,
				// emacs, etc.) sorted alphabetically. Build the
				// combined list and emit.
				type oentry struct {
					name    string
					enabled bool
				}
				var list []oentry
				for i, opt := range &posixOptsTable {
					if opt.name == "restricted" {
						continue
					}
					list = append(list, oentry{opt.name, r.opts[i]})
				}
				for n, on := range noOpSetOptions {
					if v, ok := r.noOpSetState[n]; ok {
						on = v
					}
					list = append(list, oentry{n, on})
				}
				sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })
				for _, e := range list {
					r.printOptLine(e.name, e.enabled, true)
				}
				continue
			}
			if value == "" && !enable {
				// `set +o` (no name): same set, reusable form.
				type oentry struct {
					name    string
					enabled bool
				}
				var list []oentry
				for i, opt := range &posixOptsTable {
					if opt.name == "restricted" {
						continue
					}
					list = append(list, oentry{opt.name, r.opts[i]})
				}
				for n, on := range noOpSetOptions {
					if v, ok := r.noOpSetState[n]; ok {
						on = v
					}
					list = append(list, oentry{n, on})
				}
				sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })
				for _, e := range list {
					setFlag := "+o"
					if e.enabled {
						setFlag = "-o"
					}
					r.outf("set %s %s\n", setFlag, e.name)
				}
				continue
			}
			opt := r.posixOptByName(value)
			if opt == nil {
				noOpName := value
				// Map single-letter flags to no-op option names for
				// accept-and-ignore options (e.g. `set -o b` → notify).
				if len(value) == 1 {
					if mapped, ok := noOpSetFlagToName[value[0]]; ok {
						noOpName = mapped
					}
				}
				if _, ok := noOpSetOptions[noOpName]; ok {
					// accept-and-ignore: remember the toggle so
					// subsequent `set -o` listings echo back what
					// the script asserted, but don't otherwise act
					// on the flag.
					if r.noOpSetState == nil {
						r.noOpSetState = make(map[string]bool)
					}
					r.noOpSetState[noOpName] = enable
					if noOpName == "ignoreeof" {
						r.setIgnoreEOFOption(enable)
					}
					continue
				}
				return fmt.Errorf("invalid option: %q", value)
			}
			if value == "restricted" && !enable {
				return fmt.Errorf("restricted: invalid option name")
			}
			*opt = enable
			if value == "restricted" && enable {
				r.markRestrictedVarsReadonly()
			}
			if value == "posix" {
				r.setPosixMode(enable)
			}
		}
		if args := fp.args(); args != nil {
			// If "--" wasn't given and there were zero arguments,
			// we don't want to override the current parameters.
			r.Params = args

			// Record whether a sourced script sets the parameters.
			if r.inSource {
				r.sourceSetParams = true
			}
		}
		return nil
	}
}

// CallHandler sets the call handler. See [CallHandlerFunc] for more info.
func CallHandler(f CallHandlerFunc) RunnerOption {
	return func(r *Runner) error {
		r.callHandler = f
		return nil
	}
}

// ExecHandler sets one command execution handler,
// which replaces [DefaultExecHandler](2 * time.Second).
//
// Deprecated: use [ExecHandlers] instead, which allows chaining handlers more easily
// like middleware functions.
func ExecHandler(f ExecHandlerFunc) RunnerOption {
	return func(r *Runner) error {
		r.execHandler = f
		return nil
	}
}

// ExecHandlers appends middlewares to handle command execution.
// The middlewares are chained from first to last, and the first is called by the runner.
// Each middleware is expected to call the "next" middleware at most once.
//
// For example, a middleware may implement only some commands.
// For those commands, it can run its logic and avoid calling "next".
// For any other commands, it can call "next" with the original parameters.
//
// Another common example is a middleware which always calls "next",
// but runs custom logic either before or after that call.
// For instance, a middleware could change the arguments to the "next" call,
// or it could print log lines before or after the call to "next".
//
// The last exec handler is always [DefaultExecHandler](2 * time.Second).
func ExecHandlers(middlewares ...func(next ExecHandlerFunc) ExecHandlerFunc) RunnerOption {
	return func(r *Runner) error {
		r.execMiddlewares = append(r.execMiddlewares, middlewares...)
		return nil
	}
}

// TODO: consider porting the middleware API in [ExecHandlers] to [OpenHandler],
// [ReadDirHandler2], and [StatHandler].

// TODO(v4): now that [ExecHandlers] allows calling a next handler with changed
// arguments, one of the two advantages of [CallHandler] is gone. The other is the
// ability to work with builtins; if we make [ExecHandlers] work with builtins, we
// could join both APIs.

// OpenHandler sets file open handler. See [OpenHandlerFunc] for more info.
func OpenHandler(f OpenHandlerFunc) RunnerOption {
	return func(r *Runner) error {
		r.openHandler = f
		return nil
	}
}

// EnableDryRunOption turns on the non-POSIX `set -o dryrun` shell option,
// initialized to enabled. When on, `set -o dryrun` / `set +o dryrun` toggle the
// runner's dry-run flag, which exec/open handlers read via [HandlerContext.DryRun]
// to print-and-skip instead of executing. Hosts that do not pass this option
// (the pure bash drop-in, gosh, anything under --posix) reject `set -o dryrun`
// as an unknown option, exactly like Bash — so bash conformance is unaffected.
// The option is deliberately kept out of `set -o` listings and SHELLOPTS.
func EnableDryRunOption(enabled bool) RunnerOption {
	return func(r *Runner) error {
		r.dryRunOpt = true
		r.dryRun = enabled
		return nil
	}
}

// WithDisabledBuiltins marks the named builtins as disabled at runner
// construction, exactly as `enable -n <name>` would at runtime: they fall
// through to PATH lookup and `type`/`command -v` report them as external (or
// not-found). This is the programmatic form of `enable -n` and is how a strict
// drop-in suppresses fork-only builtins it adds for embedders. For example the
// pure `bash` binary disables the fork's `nohup`/`setsid` so they resolve to
// the real external commands like bash 5.3 (and, where absent — e.g. setsid on
// macOS — report "not found", matching bash on that platform), while the
// AgentOS / matrix-shell embedders keep them as builtins for in-process detach.
func WithDisabledBuiltins(names ...string) RunnerOption {
	return func(r *Runner) error {
		if len(names) == 0 {
			return nil
		}
		if r.disabledBuiltins == nil {
			r.disabledBuiltins = make(map[string]bool)
		}
		for _, n := range names {
			r.disabledBuiltins[n] = true
		}
		return nil
	}
}

// ReadDirHandler sets the read directory handler. See [ReadDirHandlerFunc] for more info.
//
// Deprecated: use [ReadDirHandler2].
func ReadDirHandler(f ReadDirHandlerFunc) RunnerOption {
	return func(r *Runner) error {
		r.readDirHandler = func(ctx context.Context, path string) ([]fs.DirEntry, error) {
			infos, err := f(ctx, path)
			if err != nil {
				return nil, err
			}
			entries := make([]fs.DirEntry, len(infos))
			for i, info := range infos {
				entries[i] = fs.FileInfoToDirEntry(info)
			}
			return entries, nil
		}
		return nil
	}
}

// ReadDirHandler2 sets the read directory handler. See [ReadDirHandlerFunc2] for more info.
func ReadDirHandler2(f ReadDirHandlerFunc2) RunnerOption {
	return func(r *Runner) error {
		r.readDirHandler = f
		return nil
	}
}

// StatHandler sets the stat handler. See [StatHandlerFunc] for more info.
func StatHandler(f StatHandlerFunc) RunnerOption {
	return func(r *Runner) error {
		r.statHandler = f
		return nil
	}
}

func stdinFile(r io.Reader) (*os.File, error) {
	switch r := r.(type) {
	case *os.File:
		return r, nil
	case nil:
		return nil, nil
	default:
		pr, pw, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		go func() {
			io.Copy(pw, r)
			pw.Close()
		}()
		return pr, nil
	}
}

// StdIO configures an interpreter's standard input, standard output, and
// standard error. If out or err are nil, they default to a writer that discards
// the output.
//
// Note that providing a non-nil standard input other than [*os.File] will require
// an [os.Pipe] and spawning a goroutine to copy into it,
// as an [os.File] is the only way to share a reader with subprocesses.
// This may cause the interpreter to consume the entire reader.
// See [os/exec.Cmd.Stdin].
//
// When providing an [*os.File] as standard input, consider using an [os.Pipe]
// as it has the best chance to support cancellable reads via [os.File.SetReadDeadline],
// so that cancelling the runner's context can stop a blocked standard input read.
func StdIO(in io.Reader, out, err io.Writer) RunnerOption {
	return func(r *Runner) error {
		stdin, _err := stdinFile(in)
		if _err != nil {
			return _err
		}
		r.stdin = stdin
		if out == nil {
			out = io.Discard
		}
		r.stdout = out
		if err == nil {
			err = io.Discard
		}
		r.stderr = err
		return nil
	}
}

// WithBgPidCallback registers a callback invoked with the OS PID of each
// real external process this runner spawns from a backgrounded statement
// (`foo &`). Fires from inside [publishBgPid] right after the PID is
// stored on the bgProc. Not invoked for pure-builtin or pure-goroutine
// backgrounded subshells (e.g. `(true) &`), because there is no kernel
// PID to report.
//
// The callback runs synchronously on the background goroutine. Keep it
// cheap; hand off to your own goroutine if you need to do non-trivial
// work (file I/O, network, etc.).
//
// Use case: an embedder (e.g. the outpost agent) that wants to surface
// detached jobs to an external job-control CLI. The in-shell `fg`/`bg`/
// `jobs` builtins are unimplemented because subshells in this
// interpreter are goroutines, not OS processes — pushing the job
// registry out to a process that can use real OS primitives is the
// supported path.
func WithBgPidCallback(fn func(pid int)) RunnerOption {
	return func(r *Runner) error {
		r.bgPidCallback = fn
		return nil
	}
}

// WithLoginShell marks this runner as a login shell. Without it, the
// `logout` builtin refuses with bash's "not login shell" error. cmd/bashy's
// interactive mode and embedders that own the session lifetime (e.g.
// outpost's SSH attach) should opt in.
func WithLoginShell(login bool) RunnerOption {
	return func(r *Runner) error {
		r.loginShell = login
		return nil
	}
}

// WithBashCompatErrors switches builtin error messages to bash 5.3
// wording. With this flag set, builtins prefix user-fault errors with
// `<filename>: line <N>:` (using [Runner]'s filename or "bashy" when
// running -c), put the offending argument before the message, and drop
// Go's `%q` quoting. cmd/bashy enables this so the upstream bash test
// suite output matches; library callers using [interp.New] directly
// see the legacy mvdan/sh wording by default.
func WithBashCompatErrors(on bool) RunnerOption {
	return func(r *Runner) error {
		r.bashCompatErrors = on
		return nil
	}
}

// WithBashSource provides the original script bytes for bash-compatible
// diagnostics that need source-preserved snippets. The runner copies src so
// callers may reuse their buffer after applying the option.
func WithBashSource(src []byte) RunnerOption {
	return func(r *Runner) error {
		r.bashSource = append(r.bashSource[:0], src...)
		return nil
	}
}

// WithIncrementalFilename sets the filename used for bash-compatible
// diagnostics when [Runner.Run] is called incrementally with a [*syntax.Stmt]
// or [syntax.Command] instead of a full [*syntax.File].
func WithIncrementalFilename(name string) RunnerOption {
	return func(r *Runner) error {
		r.incrementalFilename = name
		return nil
	}
}

// WithArgv0 sets the shell's $0 (and initial $BASH_ARGV0) explicitly,
// independent of the filename used for error-message prefixes. A standalone
// shell uses this for the `-s` / stdin / interactive invocation forms, where
// bash reports $0 as the name the shell was invoked with (argv[0]) even though
// no script file is being run. The value survives [Runner.Reset]; user code can
// still override it at runtime by assigning to $BASH_ARGV0.
func WithArgv0(name string) RunnerOption {
	return func(r *Runner) error {
		r.argv0 = name
		r.origArgv0 = name
		return nil
	}
}

func WithInheritedFds(fds []int) RunnerOption {
	return func(r *Runner) error {
		if len(fds) == 0 {
			r.inheritedFds = nil
			return nil
		}
		r.inheritedFds = make(map[int]bool, len(fds))
		for _, fd := range fds {
			if fd >= 3 {
				r.inheritedFds[fd] = true
			}
		}
		return nil
	}
}

// AuditEvent is delivered to the [WithAuditHandler] callback just
// before the runner dispatches a simple command to either a shell
// builtin or [ExecHandlerFunc].
type AuditEvent struct {
	// Kind is "builtin" or "exec".
	Kind string
	// Args is the resolved command and its arguments, post expansion.
	Args []string
	// Pos is the source position of the command in the script.
	Pos syntax.Pos
	// When records when the command was observed. In deterministic
	// mode, this is the runner's frozen start time.
	When time.Time
	// Filename is [Runner.filename] (the parsed script's name) or
	// empty if the runner was driven by -c / a Node value.
	Filename string
	// CallStackHash is a SHA-256 digest of the active shell function
	// stack at the observation point.
	CallStackHash string
	// EnvDigest is a SHA-256 digest of the current shell variable
	// environment.
	EnvDigest string
	// IsBuiltin is true if Args[0] is a shell builtin. Embedders that
	// want a record of every "command" — builtin or not — typically
	// keep these; harnesses that only care about external launches
	// can filter them out.
	IsBuiltin bool
}

// WithAuditHandler registers a callback invoked once per simple
// command immediately before [ExecHandlerFunc] runs. Use it to
// build a record of what the shell is about to execute (replay
// logs, capability checking, observability). The callback runs
// synchronously; return quickly. Returning is non-fatal — there
// is no way to veto the command from the audit callback. Use a
// custom [ExecHandlerFunc] for that.
func WithAuditHandler(fn func(AuditEvent)) RunnerOption {
	return func(r *Runner) error {
		r.auditHandler = fn
		return nil
	}
}

// WithAuditLog writes each [AuditEvent] as one JSON object per line.
// It is additive with [WithAuditHandler]; both receive the same event.
func WithAuditLog(w io.Writer) RunnerOption {
	return func(r *Runner) error {
		r.auditLog = w
		return nil
	}
}

// ErrorEvent is delivered to the [WithStructuredErrors] callback when
// the runner emits a known user-facing diagnostic.
type ErrorEvent struct {
	// Kind classifies the diagnostic at a coarse level, such as
	// "builtin", "expand", "exec", or "io".
	Kind string
	// Severity is "error" for diagnostics that affect the command's
	// status. Future warning-only sites may use "warning".
	Severity string
	// Message is the exact diagnostic text written to stderr, minus a
	// trailing newline.
	Message string
	// Pos is the source position associated with the diagnostic.
	Pos syntax.Pos
	// Filename is [Runner.filename] (the parsed script's name) or
	// empty if the runner was driven by -c / a Node value.
	Filename string
	// Function is the innermost shell function active when the
	// diagnostic was produced, if any.
	Function string
	// Command is the command or builtin name associated with the
	// diagnostic, if known.
	Command string
	// ExitStatus is the status assigned by the diagnostic site, when
	// that status is known.
	ExitStatus uint8
}

// WithStructuredErrors registers a callback invoked when the runner
// emits known diagnostics. It gives embedders a typed stream for
// observability and audit trails without parsing bash-shaped stderr.
// The callback runs synchronously; keep it cheap.
func WithStructuredErrors(fn func(ErrorEvent)) RunnerOption {
	return func(r *Runner) error {
		r.structuredErrorHandler = fn
		return nil
	}
}

// WithDeterministic enables deterministic-mode runs targeted at
// agentic harnesses that need reproducible output. When on:
//   - $RANDOM uses a per-runner PRNG seeded from `seed` (or 0 if
//     the caller passed no seed) rather than [crypto/rand].
//   - $SECONDS and $EPOCHSECONDS return a fixed value derived from
//     the runner's start time rather than wall-clock progression.
//   - $$ (PID) is stable: the seed value modulo 2^15 instead of
//     [os.Getpid].
//
// External commands still see the host wall clock and real PIDs;
// determinism is bounded to what the shell itself emits. Embedders
// that need stronger isolation should combine this with a custom
// [ExecHandler]/[OpenHandler] and an [AuditHandler].
func WithDeterministic(seed int64) RunnerOption {
	return func(r *Runner) error {
		r.deterministic = true
		r.deterministicSeed = seed
		// PCG seeded from (seed, seed^0x9E3779B97F4A7C15) — the lo
		// half is the user seed, hi is a hash to avoid weak streams
		// when the user passed 0.
		r.deterministicRng = mathrand.NewPCG(uint64(seed), uint64(seed)^0x9E3779B97F4A7C15)
		return nil
	}
}

// WithPosixMode sets the runner's POSIX mode.
func WithPosixMode(enabled bool) RunnerOption {
	return func(r *Runner) error {
		r.setPosixMode(enabled)
		return nil
	}
}

func (r *Runner) posixOptByName(name string) *bool {
	// Bashy-only `set -o dryrun`, recognized only when EnableDryRunOption was
	// passed (never in the pure bash drop-in or under --posix). Kept out of
	// posixOptsTable so it never appears in `set -o` listings or SHELLOPTS.
	if name == "dryrun" && r.dryRunOpt {
		return &r.dryRun
	}
	for i, opt := range &posixOptsTable {
		if opt.name == name {
			return &r.opts[i]
		}
	}
	// POSIX allows set -o <name> where name is the single-letter abbreviation
	// as well as the long option name (e.g. `set -o a` == `set -o allexport`).
	if len(name) == 1 {
		return r.posixOptByFlag(name[0])
	}
	return nil
}

func (r *Runner) setPosixMode(enabled bool) {
	r.opts[optPosix] = enabled
	if r.ecfg != nil {
		r.ecfg.Posix = enabled
	}
	if enabled {
		r.opts[optExpandAliases] = true
		// POSIX mode auto-sets POSIXLY_CORRECT=y if not already set.
		if r.writeEnv != nil && !r.writeEnv.Get("POSIXLY_CORRECT").IsSet() {
			r.writeEnv.Set("POSIXLY_CORRECT", expand.Variable{
				Set: true, Exported: true, Kind: expand.String, Str: "y",
			})
		}
	}
}

// LangVariant returns the parser language implied by the runner's
// current shell options.
func (r *Runner) LangVariant() syntax.LangVariant {
	if r.opts[optPosix] {
		return syntax.LangPOSIX
	}
	return syntax.LangBash
}

// VimMode reports whether the vi line-editing mode is active (`set -o vi`).
// Bash defaults to emacs editing; `set -o vi` switches to vi. The interactive
// line editor consults this between prompts so a runtime `set -o vi`/`set +o
// vi` toggle takes effect. Reading the absent/nil map key yields false (emacs).
func (r *Runner) VimMode() bool {
	return r.noOpSetState["vi"]
}

// LiveVar returns the current value of the named variable, resolved through
// the active scope (locals, globals, and the writable environment overlay).
// Unlike the read-only [Runner.Env] — which holds only the initial
// environment — it reflects assignments made while the runner executes, such
// as an interactive `PS1=...`. Returns the zero [expand.Variable] (unset) when
// the name is not declared. Intended for embedders that must read live shell
// state between runs, e.g. recomputing the prompt each interactive line.
func (r *Runner) LiveVar(name string) expand.Variable {
	// Before the first Run, Reset has not built the writable overlay yet;
	// fall back to the initial environment so callers (e.g. the interactive
	// prompt, shown before the first command) don't dereference a nil overlay.
	if r.writeEnv == nil {
		if r.Env == nil {
			return expand.Variable{}
		}
		return r.Env.Get(name)
	}
	return r.lookupVar(name)
}

func (r *Runner) posixOptByFlag(flag byte) *bool {
	for i, opt := range &posixOptsTable {
		if opt.flag == flag {
			return &r.opts[i]
		}
	}
	return nil
}

func (r *Runner) bashOptByName(name string) (status *bool, supported bool) {
	for i, opt := range bashOptsTable {
		if opt.name == name {
			index := len(posixOptsTable) + i
			return &r.opts[index], opt.supported
		}
	}
	return nil, false
}

// runnerOpts contains all POSIX Shell and Bash options as one contiguous table.
type runnerOpts [len(posixOptsTable) + len(bashOptsTable)]bool

type localOptFrame struct {
	active       bool
	opts         runnerOpts
	noOpSetState map[string]bool
}

func (r *Runner) markLocalOpts() {
	if n := len(r.localOptStack); n > 0 {
		r.localOptStack[n-1].active = true
	}
}

func (r *Runner) localOptsActive() bool {
	return len(r.localOptStack) > 0 && r.localOptStack[len(r.localOptStack)-1].active
}

type posixOpt struct {
	flag byte   // one-character flag form for this option; a space if none exists
	name string // full name of the option
}

type bashOpt struct {
	name         string
	defaultState bool // Bash's default value for this option
	supported    bool // whether we support the option's non-default state
}

var posixOptsTable = [...]posixOpt{
	// sorted alphabetically by name
	{'a', "allexport"},
	{'e', "errexit"},
	{'n', "noexec"},
	{'C', "noclobber"},
	{'f', "noglob"},
	{'u', "nounset"},
	{'x', "xtrace"},
	{' ', "pipefail"},
	{' ', "posix"},
	{'r', "restricted"},
	{'k', "keyword"},
}

// noOpSetOptions are option names that `set -o NAME` / `set -H` etc. can
// pass but which we silently accept without any runtime effect. This is
// the long-name set; the short-flag set is in
// [posixOptByFlag] / [Params].
// noOpSetOptions maps each `set -o NAME` we accept-and-ignore to its
// default state in bash 5.3 (true = on, false = off). The state isn't
// tracked per-runner; the value just lets the listing form match bash.
var noOpSetOptions = map[string]bool{
	"history":              true, // on by default
	"histexpand":           true, // alias for history
	"hashall":              true, // bash's listing uses "hashall", not "hashcmds"
	"verbose":              false,
	"monitor":              true, // job control on in bash by default
	"vi":                   false,
	"emacs":                true, // bash defaults emacs editor on
	"interactive-comments": true,
	"ignoreeof":            false,
	"physical":             false,
	"privileged":           false,
	"functrace":            false,
	"braceexpand":          true, // always on
	"notify":               false,
	"onecmd":               false,
	"errtrace":             false, // set -E, listed by bash
	"nolog":                false, // listed by bash
}

// noOpSetFlagToName maps single-letter option flags to their
// accept-and-ignore long-name counterparts. Used by both the
// short-flag path (`set -b`) and the long-name path (`set -o b`).
var noOpSetFlagToName = map[byte]string{
	'b': "notify",
	'v': "verbose",
	'h': "hashall",
	'H': "histexpand",
	'B': "braceexpand",
	'P': "physical",
	'p': "privileged",
	't': "onecmd",
	'E': "errtrace",
	'T': "functrace",
	'm': "monitor",
}

var bashOptsTable = [...]bashOpt{
	// IMPORTANT: the first six entries — dotglob, expand_aliases,
	// extglob, globstar, nocaseglob, nullglob — are referenced by
	// the optDotGlob…optNullGlob const constants below in that
	// exact order. Don't reorder them. The rest is sorted
	// alphabetically and printed sorted by `shopt`/`shopt -p`.
	{name: "dotglob", supported: true},
	{name: "expand_aliases", supported: true},
	{name: "extglob", supported: true},
	{name: "globstar", supported: true},
	{name: "nocaseglob", supported: true},
	{name: "nullglob", supported: true},
	// Everything below is in alphabetical order.
	{name: "array_expand_once"},
	{name: "assoc_expand_once"},
	{name: "autocd", supported: true},
	{name: "bash_source_fullpath"},
	{name: "cdable_vars"},
	{name: "cdspell"},
	{name: "checkhash", supported: true},
	{name: "checkjobs"},
	{name: "checkwinsize", defaultState: true, supported: true},
	{name: "cmdhist", defaultState: true, supported: true},
	{name: "compat31"},
	{name: "compat32"},
	{name: "compat40"},
	{name: "compat41"},
	{name: "compat42"},
	{name: "compat43"},
	{name: "compat44"},
	{name: "complete_fullquote", defaultState: true, supported: true},
	{name: "direxpand"},
	{name: "dirspell"},
	{name: "execfail"},
	{name: "extdebug", supported: true},
	{name: "extquote", defaultState: true, supported: true},
	{name: "failglob", supported: true},
	{name: "force_fignore", defaultState: true, supported: true},
	{name: "globasciiranges", defaultState: true, supported: true},
	{name: "globskipdots", defaultState: true, supported: true},
	{name: "gnu_errfmt"},
	{name: "histappend"},
	{name: "histreedit"},
	{name: "histverify"},
	{name: "hostcomplete", defaultState: true, supported: true},
	{name: "huponexit", supported: true},
	{name: "inherit_errexit", supported: true},
	{name: "interactive_comments", defaultState: true, supported: true},
	{name: "lastpipe", supported: true},
	{name: "lithist"},
	{name: "localvar_inherit"},
	{name: "localvar_unset"},
	{name: "login_shell"},
	{name: "mailwarn"},
	{name: "no_empty_cmd_completion"},
	{name: "nocasematch", supported: true},
	{name: "noexpand_translation"},
	{name: "patsub_replacement", defaultState: true, supported: true},
	{name: "progcomp", defaultState: true, supported: true},
	{name: "progcomp_alias"},
	{name: "promptvars", defaultState: true, supported: true},
	{name: "restricted_shell"},
	{name: "shift_verbose"},
	{name: "sourcepath", defaultState: true, supported: true},
	{name: "varredir_close"},
	{name: "xpg_echo", supported: true},
}

// To access the shell options arrays without a linear search when we
// know which option we're after at compile time. First come the shell options,
// then the bash options.
const (
	// These correspond to indexes in [shellOptsTable]
	optAllExport = iota
	optErrExit
	optNoExec
	optNoClobber
	optNoGlob
	optNoUnset
	optXTrace
	optPipeFail
	optPosix
	optRestricted
	optKeyword

	// These correspond to indexes (offset by the above seven items) of
	// supported options in [bashOptsTable]
	optDotGlob
	optExpandAliases
	optExtGlob
	optGlobStar
	optNoCaseGlob
	optNullGlob
)

// Reset returns a runner to its initial state, right before the first call to
// Run or Reset.
//
// Typically, this function only needs to be called if a runner is reused to run
// multiple programs non-incrementally. Not calling Reset between each run will
// mean that the shell state will be kept, including variables, options, and the
// current directory.
func (r *Runner) Reset() {
	if !r.usedNew {
		panic("use interp.New to construct a Runner")
	}
	if !r.didReset {
		r.origDir = r.Dir
		r.origParams = r.Params
		r.origOpts = r.opts
		r.origDryRun = r.dryRun
		r.origDryRunOpt = r.dryRunOpt
		r.origNoOpSetState = maps.Clone(r.noOpSetState)
		r.origStdin = r.stdin
		r.origStdout = r.stdout
		r.origStderr = r.stderr

		if r.execHandler != nil && len(r.execMiddlewares) > 0 {
			panic("interp.ExecHandler should be replaced with interp.ExecHandlers, not mixed")
		}
		if r.execHandler == nil {
			r.execHandler = DefaultExecHandler(2 * time.Second)
		}
		// Middlewares are chained from first to last, and each can call the
		// next in the chain, so we need to construct the chain backwards.
		for _, mw := range slices.Backward(r.execMiddlewares) {
			r.execHandler = mw(r.execHandler)
		}
		// Fill tempDir; only need to do this once given that Env will not change.
		if dir := r.Env.Get("TMPDIR").String(); filepath.IsAbs(dir) {
			r.tempDir = dir
		} else {
			r.tempDir = os.TempDir()
		}
		// Clean it as we will later do a string prefix match.
		r.tempDir = filepath.Clean(r.tempDir)
		// Snapshot the process umask once at first Reset. The builtin
		// updates only r.umask afterwards; the process value is never
		// mutated by this runner.
		r.umask = processUmask()
		// Snapshot, once, the signals that were ignored on shell entry. The
		// environment bridge covers self re-execs; the OS disposition snapshot
		// covers a real parent that exec'd us with SIG_IGN inherited.
		r.startupIgnored = startupIgnoredSignals(r.Env.Get(BashyHardIgnoreEnv).String())
	}
	// reset the internal state
	*r = Runner{
		Env:            r.Env,
		tempDir:        r.tempDir,
		callHandler:    r.callHandler,
		execHandler:    r.execHandler,
		openHandler:    r.openHandler,
		readDirHandler: r.readDirHandler,
		statHandler:    r.statHandler,
		bgPidCallback:  r.bgPidCallback,

		// These can be set by functions like [Dir] or [Params], but
		// builtins can overwrite them; reset the fields to whatever the
		// constructor set up.
		Dir:          r.origDir,
		Params:       r.origParams,
		opts:         r.origOpts,
		dryRun:       r.origDryRun,
		dryRunOpt:    r.origDryRunOpt,
		noOpSetState: maps.Clone(r.origNoOpSetState),
		stdin:        r.origStdin,
		stdout:       r.origStdout,
		stderr:       r.origStderr,

		// Restore an explicitly configured $0 (see [WithArgv0]); a plain
		// runner leaves both empty so the filename-based default applies.
		argv0:     r.origArgv0,
		origArgv0: r.origArgv0,

		origDir:          r.origDir,
		origParams:       r.origParams,
		origOpts:         r.origOpts,
		origDryRun:       r.origDryRun,
		origDryRunOpt:    r.origDryRunOpt,
		origNoOpSetState: maps.Clone(r.origNoOpSetState),
		origStdin:        r.origStdin,
		origStdout:       r.origStdout,
		origStderr:       r.origStderr,

		// emptied below, to reuse the space
		Vars: r.Vars,

		// Preserve user-registered functions across Reset; bash's
		// `BASH_FUNC_*` env imports run at construction time and the
		// resulting functions are part of the initial shell state,
		// not per-Run scratch state.
		Funcs:       r.Funcs,
		funcSources: r.funcSources,

		// disabledBuiltins (`enable -n`, or WithDisabledBuiltins at
		// construction) is part of the shell's persistent state, not per-Run
		// scratch — preserve it across Reset like funcs/aliases.
		disabledBuiltins: r.disabledBuiltins,

		dirStack: r.dirStack[:0],
		usedNew:  r.usedNew,

		promptExpand:           r.promptExpand,
		startTime:              r.startTime,
		subshellLevel:          r.subshellLevel,
		umask:                  r.umask,
		startupIgnored:         r.startupIgnored,
		inheritedExitTrap:      r.inheritedExitTrap,
		loginShell:             r.loginShell,
		bashCompatErrors:       r.bashCompatErrors,
		bashSource:             slices.Clone(r.bashSource),
		auditHandler:           r.auditHandler,
		auditLog:               r.auditLog,
		structuredErrorHandler: r.structuredErrorHandler,
		deterministic:          r.deterministic,
		deterministicSeed:      r.deterministicSeed,
		deterministicRng:       r.deterministicRng,
		interactiveShell:       r.interactiveShell,
		commandString:          r.commandString,
		standardInput:          r.standardInput,
		mirrorUmask:            r.mirrorUmask,
		inheritedFds:           maps.Clone(r.inheritedFds),
		// fdTable is intentionally not preserved across Reset; a reset
		// runner starts with no inherited non-stdio fds.
	}
	// Ensure we stop referencing any pointers before we reuse bgProcs.
	clear(r.bgProcs)
	r.bgProcs = r.bgProcs[:0]

	if r.Vars == nil {
		r.Vars = make(map[string]expand.Variable)
	} else {
		clear(r.Vars)
	}
	if r.deterministic {
		r.deterministicRng = mathrand.NewPCG(uint64(r.deterministicSeed), uint64(r.deterministicSeed)^0x9E3779B97F4A7C15)
	}
	// TODO(v4): Use the supplied Env directly if it implements enough methods.
	r.writeEnv = &overlayEnviron{parent: r.Env}
	// The hard-ignore bridge variable is internal: consume it (already
	// snapshotted into startupIgnored) and hide it from the script's scope.
	if r.writeEnv.Get(BashyHardIgnoreEnv).IsSet() {
		r.delVar(BashyHardIgnoreEnv)
	}
	if !r.writeEnv.Get("HOME").IsSet() {
		home, _ := os.UserHomeDir()
		r.setVarString("HOME", shellPathFromOS(home))
	}
	if !r.writeEnv.Get("UID").IsSet() {
		r.setVar("UID", expand.Variable{
			Set:      true,
			Kind:     expand.String,
			ReadOnly: true,
			Str:      strconv.Itoa(os.Getuid()),
		})
	}
	if !r.writeEnv.Get("EUID").IsSet() {
		r.setVar("EUID", expand.Variable{
			Set:      true,
			Kind:     expand.String,
			ReadOnly: true,
			Str:      strconv.Itoa(os.Geteuid()),
		})
	}
	if !r.writeEnv.Get("GID").IsSet() {
		r.setVar("GID", expand.Variable{
			Set:      true,
			Kind:     expand.String,
			ReadOnly: true,
			Str:      strconv.Itoa(os.Getgid()),
		})
	}
	r.setVarString("PWD", shellPathFromOS(r.Dir))
	r.setVarString("IFS", " \t\n")
	r.setVarString("OPTIND", "1")
	if r.opts[optPosix] && !r.writeEnv.Get("POSIXLY_CORRECT").IsSet() {
		r.writeEnv.Set("POSIXLY_CORRECT", expand.Variable{
			Set: true, Exported: true, Kind: expand.String, Str: "y",
		})
	}
	if r.startTime.IsZero() {
		r.startTime = time.Now()
	}

	r.dirStack = append(r.dirStack, r.Dir)

	// A restricted shell (set via `-r`/`--restricted`/`rbash` before the
	// first Reset) freezes PATH, SHELL, ENV, and BASH_ENV. The opts were
	// captured during New, but writeEnv only exists now, so apply it here.
	if r.opts[optRestricted] {
		r.markRestrictedVarsReadonly()
	}

	// `-o ignoreeof` set via [Params] at construction is recorded in
	// noOpSetState but its IGNOREEOF variable was deferred (writeEnv did not
	// exist yet); apply it now that the variable environment is ready.
	if r.noOpSetState["ignoreeof"] {
		r.setIgnoreEOFOption(true)
	}

	r.didReset = true
}

// ExitStatus is a non-zero status code resulting from running a shell node.
type ExitStatus uint8

func (s ExitStatus) Error() string { return fmt.Sprintf("exit status %d", s) }

// NewExitStatus creates an error which contains the specified exit status code.
//
// Deprecated: use [ExitStatus] directly.
//
//go:fix inline
func NewExitStatus(status uint8) error {
	return ExitStatus(status)
}

// IsExitStatus checks whether error contains an exit status and returns it.
//
// Deprecated: use [errors.As] with [ExitStatus] directly.
//
//go:fix inline
func IsExitStatus(err error) (status uint8, ok bool) {
	var es ExitStatus
	if errors.As(err, &es) {
		return uint8(es), true
	}
	return 0, false
}

// SetLastExitStatus sets the shell status exposed via $? without running a
// command. It is intended for embedders that recover from external parse or
// scheduling errors and need the next shell statement to observe that status.
func (r *Runner) SetLastExitStatus(status uint8) {
	r.lastExit = exitStatus{code: status}
}

// Run interprets a node, which can be a [*File], [*Stmt], or [Command]. If a non-nil
// error is returned, it will typically contain a command's exit status, which
// can be retrieved with [IsExitStatus].
//
// Run can be called multiple times synchronously to interpret programs
// incrementally. To reuse a [Runner] without keeping the internal shell state,
// call Reset.
//
// Calling Run on an entire [*File] implies an exit, meaning that an exit trap may
// run.
func (r *Runner) Run(ctx context.Context, node syntax.Node) error {
	if !r.didReset {
		r.Reset()
	}
	r.fillExpandConfig(ctx)
	r.exit = exitStatus{}
	r.expandRunExit = exitStatus{}
	r.filename = r.incrementalFilename
	runExitTrap := false
	switch node := node.(type) {
	case *syntax.File:
		r.filename = node.Name
		if !r.commandString && node.Name == "" && len(r.bashSource) > 0 {
			r.stdinSourceActive = true
		}
		runExitTrap = true
		for _, stmt := range node.Stmts {
			if r.stdinSourceActive && int(stmt.Pos().Offset()) < r.stdinSourceOffset {
				continue
			}
			// Skip the tail of a physical line aborted by a readonly
			// assignment error; resume once the source line changes.
			if r.discardRestOfLine != 0 {
				if stmt.Pos().Line() == r.discardRestOfLine {
					continue
				}
				r.discardRestOfLine = 0
				r.exit.discarding = false
				r.exit.exiting = false
			}
			r.verboseStmt(stmt)
			r.stmt(ctx, stmt)
			// A DISCARD only aborts the top-level command it
			// occurred in; the next one still runs.
			if r.exit.discarding {
				if r.discardNextStmt {
					r.discardNextStmt = false
					continue
				}
				if r.discardRestOfLine != 0 {
					continue
				}
				r.exit.discarding = false
				r.exit.exiting = false
			}
		}
	case *syntax.Stmt:
		if !r.commandString && r.incrementalFilename == "" && len(r.bashSource) > 0 {
			r.stdinSourceActive = true
		}
		if r.stdinSourceActive && int(node.Pos().Offset()) < r.stdinSourceOffset {
			break
		}
		r.verboseStmt(node)
		r.stmt(ctx, node)
	case syntax.Command:
		if !r.commandString && r.incrementalFilename == "" && len(r.bashSource) > 0 {
			r.stdinSourceActive = true
		}
		r.cmd(ctx, node)
	default:
		return fmt.Errorf("node can only be File, Stmt, or Command: %T", node)
	}
	// A DISCARDed top-level command only aborts itself; the shell (and
	// any caller driving Run statement-by-statement) keeps going.
	r.discardRestOfLine = 0
	if r.exit.discarding {
		r.exit.discarding = false
		r.exit.exiting = false
	}
	if runExitTrap {
		oldCallStack := r.callStack
		if r.exitTrapCallStack != nil {
			r.callStack = r.exitTrapCallStack
		}
		r.trapCallback(ctx, r.trapCallbacks["EXIT"], "exit")
		r.callStack = oldCallStack
		r.exitTrapCallStack = nil
	}
	maps.Insert(r.Vars, r.writeEnv.Each)
	// Return the first of: a fatal error, a non-fatal handler error, or the exit code.
	if err := r.exit.err; err != nil {
		if r.exit.code == 0 {
			// This should never happen; too much code relies on checking [exitStatus.code]
			// to see if the last command succeeded or failed. [exitStatus.err] should only be
			// additional information, so fail loudly if the invariant is broken.
			panic("ended up with a non-nil exitStatus.err but a zero exitStatus.code")
		}
		return err
	}
	if code := r.exit.code; code != 0 {
		return ExitStatus(code)
	}
	if code := r.expandRunExit.code; code != 0 {
		return ExitStatus(code)
	}
	return nil
}

func (r *Runner) verboseStmt(stmt *syntax.Stmt) {
	if stmt == nil || !r.noOpSetState["verbose"] || len(r.bashSource) == 0 {
		return
	}
	src := r.sourceTextRange(stmt.Pos(), stmt.End(), false)
	if src == "" {
		return
	}
	r.errf("%s", src)
	if !strings.HasSuffix(src, "\n") {
		r.errf("\n")
	}
}

// Exited reports whether the last Run call should exit an entire shell. This
// can be triggered by the "exit" built-in command, for example.
//
// Note that this state is overwritten at every Run call, so it should be
// checked immediately after each Run call.
func (r *Runner) Exited() bool {
	return r.exit.exiting
}

// StdinFile returns the file currently used as the runner's standard input.
// It can change after a persistent redirection such as `exec 0<file`.
func (r *Runner) StdinFile() *os.File {
	return r.stdin
}

// Subshell makes a copy of the given [Runner], suitable for use concurrently
// with the original. The copy will have the same environment, including
// variables and functions, but they can all be modified without affecting the
// original.
//
// Subshell is not safe to use concurrently with [Run]. Orchestrating this is
// left up to the caller; no locking is performed.
//
// To replace e.g. stdin/out/err, do [StdIO](r.stdin, r.stdout, r.stderr)(r) on
// the copy.
func (r *Runner) Subshell() *Runner {
	return r.subshell(true)
}

// subshell is like [Runner.subshell], but allows skipping some allocations and copies
// when creating subshells which will not be used concurrently with the parent shell.
// TODO(v4): we should expose this, e.g. SubshellForeground and SubshellBackground.
func (r *Runner) subshell(background bool) *Runner {
	if !r.didReset {
		r.Reset()
	}
	// Keep in sync with the Runner type. Manually copy fields, to not copy
	// sensitive ones like [errgroup.Group], and to do deep copies of slices.
	r2 := &Runner{
		Dir:                  r.Dir,
		tempDir:              r.tempDir,
		Params:               r.Params,
		callHandler:          r.callHandler,
		execHandler:          r.execHandler,
		openHandler:          r.openHandler,
		readDirHandler:       r.readDirHandler,
		statHandler:          r.statHandler,
		stdin:                r.stdin,
		stdinTTYFallback:     r.stdinTTYFallback,
		stdinDevTTY:          r.stdinDevTTY,
		stdout:               r.stdout,
		stderr:               r.stderr,
		filename:             r.filename,
		curStmtPos:           r.curStmtPos,
		aliasReparseDepth:    r.aliasReparseDepth,
		aliasBase:            r.aliasBase,
		aliasDefOverride:     r.aliasDefOverride,
		enclosingSubshellEnd: r.enclosingSubshellEnd,
		opts:                 r.opts,
		noOpSetState:         maps.Clone(r.noOpSetState),
		tempEnv:              maps.Clone(r.tempEnv),
		usedNew:              r.usedNew,
		exit:                 r.exit,
		lastExit:             r.lastExit,
		bgPidCallback:        r.bgPidCallback,
		inheritedBang:        r.lastBangProc(),
		cmdHashTable:         maps.Clone(r.cmdHashTable),

		origStdout: r.origStdout, // used for process substitutions

		promptExpand:           r.promptExpand,
		startTime:              r.startTime,
		subshellLevel:          r.subshellLevel + 1,
		umask:                  r.umask,
		startupIgnored:         r.startupIgnored,
		loginShell:             r.loginShell,
		bashCompatErrors:       r.bashCompatErrors,
		auditHandler:           r.auditHandler,
		auditLog:               r.auditLog,
		structuredErrorHandler: r.structuredErrorHandler,
		deterministic:          r.deterministic,
		deterministicSeed:      r.deterministicSeed,
		deterministicRng:       r.deterministicRng,
		randomSeeded:           r.randomSeeded,
		randomSeed:             r.randomSeed,
		interactiveShell:       r.interactiveShell,
		commandString:          r.commandString,
		standardInput:          r.standardInput,
		// Subshells inherit open fds the way bash does. Clone the map so
		// child mutations (close, dup) don't leak back to the parent;
		// the underlying *os.File handles are shared (single OS fd).
		fdTable:       maps.Clone(r.fdTable),
		fdReadTable:   maps.Clone(r.fdReadTable),
		fdWriteTable:  maps.Clone(r.fdWriteTable),
		fdClosedTable: maps.Clone(r.fdClosedTable),
		inheritedFds:  maps.Clone(r.inheritedFds),

		// Shared by pointer (not cloned): a background subshell must be
		// able to resolve `$COPROC_PID` to the parent's coprocess so that
		// `kill $COPROC_PID` reaches the real child.
		coprocReg: r.coprocReg,

		ulimitOverride: maps.Clone(r.ulimitOverride),
	}
	r2.writeEnv = newOverlayEnviron(r.writeEnv, background)
	// Funcs are copied, since they might be modified.
	r2.Funcs = maps.Clone(r.Funcs)
	r2.funcSources = maps.Clone(r.funcSources)
	r2.Vars = make(map[string]expand.Variable)
	r2.alias = maps.Clone(r.alias)
	r2.trapCallbacks = maps.Clone(r.trapCallbacks)
	r2.inheritedExitTrap = r.trapCallbacks["EXIT"] != ""
	// Subshells inherit the ERR trap only with errtrace (set -E), and
	// the DEBUG/RETURN traps only with functrace (set -T); otherwise
	// they reset to their default disposition. This keeps the ERR trap
	// from firing for failed commands inside `$(...)`, pipeline
	// elements, and `( ... )` (bash trap2.sub/trap3.sub).
	if !r.errtraceEnabled() {
		delete(r2.trapCallbacks, "ERR")
	}
	if !r.functraceEnabled() {
		delete(r2.trapCallbacks, "DEBUG")
		delete(r2.trapCallbacks, "RETURN")
	}
	r2.disabledBuiltins = maps.Clone(r.disabledBuiltins)
	r2.readonlyFuncs = maps.Clone(r.readonlyFuncs)
	r2.funcTrace = maps.Clone(r.funcTrace)
	r2.exportedFuncs = maps.Clone(r.exportedFuncs)
	// Subshells inherit "we're inside a function" so that `return`
	// in `$(... return ...)` aborts only the subshell rather than
	// erroring with "can only be done from a func or sourced
	// script". Bash distinguishes these — see comsub.tests.
	r2.inFunc = r.inFunc
	r2.callStack = append([]callFrame(nil), r.callStack...)
	// Errexit-inhibition flags travel through — `(false; echo A)`
	// as the LHS of `&&` runs both statements because the outer
	// `&&` already suppresses errexit for the subshell body.
	r2.noErrExit = r.noErrExit

	r2.dirStack = append(r2.dirBootstrap[:0], r.dirStack...)
	// A foreground subshell runs inline in the parent's goroutine, so it can
	// reach back into the parent's pending-signal queue. Background/async
	// subshells run concurrently and must keep using a real OS signal, so
	// they leave sigParent nil.
	if !background {
		r2.sigParent = r
	}
	r2.fillExpandConfig(r.ectx)
	r2.didReset = true
	return r2
}
