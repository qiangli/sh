// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-quicktest/qt"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/internal"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// runnerRunTimeout is the context timeout used by any tests calling [Runner.Run].
// The timeout saves us from hangs or burning too much CPU if there are bugs.
// All the test cases are designed to be inexpensive and stop in a very short
// amount of time, so 5s should be plenty even for busy machines.
const runnerRunTimeout = 5 * time.Second

// Some program which should be in $PATH. Needs to run before runTests is
// initialized (so an init function wouldn't work), because runTest uses it.
var pathProg = func() string {
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return "sh"
}()

func parse(tb testing.TB, parser *syntax.Parser, src string) *syntax.File {
	if parser == nil {
		parser = syntax.NewParser()
	}
	file, err := parser.Parse(strings.NewReader(src), "")
	if err != nil {
		tb.Fatal(err)
	}
	return file
}

func BenchmarkRun(b *testing.B) {
	b.ReportAllocs()

	src := `
echo a b c d
echo ./$foo/etc $(echo foo bar)
foo="bar"
x=y :
fn() {
	local a=b
	for i in 1 2 3; do
		echo $i | cat
	done
}
[[ $foo == bar ]] && fn
echo a{b,c}d *.go
let i=(2 + 3)
`
	file := parse(b, nil, src)
	r, _ := interp.New()
	ctx := context.Background()

	for b.Loop() {
		r.Reset()
		if err := r.Run(ctx, file); err != nil {
			b.Fatal(err)
		}
	}
}

var hasBash53 bool

func TestMain(m *testing.M) {
	if os.Getenv("GOSH_PROG") != "" {
		switch os.Getenv("GOSH_CMD") {
		case "exit_0":
			os.Exit(0)
		case "exit_5":
			os.Exit(5)
		case "print_ok":
			fmt.Printf("exec ok\n")
			os.Exit(0)
		case "print_fail":
			fmt.Printf("exec fail\n")
			os.Exit(1)
		case "pid_and_hang":
			fmt.Println(os.Getpid())
			time.Sleep(time.Hour)
			os.Exit(0)
		case "foo_null_bar":
			fmt.Println("foo\x00bar")
			os.Exit(0)
		case "lookpath":
			_, err := exec.LookPath(pathProg)
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			fmt.Printf("%s found\n", pathProg)
			os.Exit(0)
		}
		r := strings.NewReader(os.Args[1])
		file, err := syntax.NewParser().Parse(r, "")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		runner, _ := interp.New(
			interp.StdIO(os.Stdin, os.Stdout, os.Stderr),
			interp.ExecHandlers(testExecHandler),
		)
		ctx := context.Background()
		if err := runner.Run(ctx, file); err != nil {
			var es interp.ExitStatus
			if errors.As(err, &es) {
				os.Exit(int(es))
			}

			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	prog, err := os.Executable()
	if err != nil {
		panic(err)
	}
	os.Setenv("GOSH_PROG", prog)

	internal.TestMainSetup()

	hasBash53 = checkBash()

	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	os.Setenv("GO_TEST_DIR", wd)

	os.Setenv("INTERP_GLOBAL", "value")
	os.Setenv("MULTILINE_INTERP_GLOBAL", "\nwith\nnewlines\n\n")

	// Double check that env vars on Windows are case insensitive.
	if runtime.GOOS == "windows" {
		os.Setenv("mixedCase_INTERP_GLOBAL", "value")
	} else {
		os.Setenv("MIXEDCASE_INTERP_GLOBAL", "value")
	}

	os.Setenv("PATH_PROG", pathProg)

	// To print env vars. Only a builtin on Windows.
	if runtime.GOOS == "windows" {
		os.Setenv("ENV_PROG", "cmd /c set")
	} else {
		os.Setenv("ENV_PROG", "env")
	}

	m.Run()
}

func checkBash() bool {
	out, err := exec.Command("bash", "-c", "echo -n $BASH_VERSION").Output()
	if err != nil {
		return false
	}
	return strings.HasPrefix(string(out), "5.3")
}

// concBuffer wraps a [bytes.Buffer] in a mutex so that concurrent writes
// to it don't upset the race detector.
type concBuffer struct {
	buf bytes.Buffer
	sync.Mutex
}

func (c *concBuffer) Write(p []byte) (int, error) {
	c.Lock()
	n, err := c.buf.Write(p)
	c.Unlock()
	return n, err
}

func (c *concBuffer) WriteString(s string) (int, error) {
	c.Lock()
	n, err := c.buf.WriteString(s)
	c.Unlock()
	return n, err
}

func (c *concBuffer) String() string {
	c.Lock()
	s := c.buf.String()
	c.Unlock()
	return s
}

func (c *concBuffer) Reset() {
	c.Lock()
	c.buf.Reset()
	c.Unlock()
}

type runTest struct {
	in, want string
}

var runTests = []runTest{
	// no-op programs
	{"", ""},
	{"true", ""},
	{":", ""},
	{"exit", ""},
	{"exit 0", ""},
	{"{ :; }", ""},
	{"(:)", ""},

	// exit status codes
	{"exit 1", "exit status 1"},
	{"exit -1", "exit status 255"},
	{"exit 300", "exit status 44"},
	{"false", "exit status 1"},
	{"false foo", "exit status 1"},
	{"! false", ""},
	{"true foo", ""},
	{": foo", ""},
	{"! true", "exit status 1"},
	{"false; true", ""},
	{"false; exit", "exit status 1"},
	{"exit; echo foo", ""},
	{"exit 0; echo foo", ""},
	{"printf", "usage: printf [-v var] format [arguments]\nexit status 2 #JUSTERR"},
	{"break", "break: only meaningful in a `for', `while', or `until' loop\n #JUSTERR"},
	{"continue", "continue: only meaningful in a `for', `while', or `until' loop\n #JUSTERR"},
	{"cd a b", "cd: too many arguments\nexit status 1 #JUSTERR"},
	{"shift a", "shift: a: numeric argument required\nexit status 2 #JUSTERR"},
	{"shift 1 2", "shift: too many arguments\nexit status 2 #JUSTERR"},
	{"shift -1", "shift: -1: shift count out of range\nexit status 1 #JUSTERR"},
	{"shift -- -4", "shift: -4: shift count out of range\nexit status 1 #JUSTERR"},
	{"shopt -s shift_verbose; shift -1", "shift: -1: shift count out of range\nexit status 1 #JUSTERR"},
	{
		"shouldnotexist",
		"\"shouldnotexist\": executable file not found in $PATH\nexit status 127 #JUSTERR",
	},
	{
		"for i in 1; do continue a; done",
		"continue: a: numeric argument required\nexit status 128 #JUSTERR",
	},
	{
		"for i in 1; do break a; done",
		"break: a: numeric argument required\nexit status 128 #JUSTERR",
	},
	{
		"for i in 1; do break 1 2; done",
		"break: too many arguments\nexit status 2 #JUSTERR",
	},
	{
		"for i in 1; do continue 1 2; done",
		"continue: too many arguments\nexit status 2 #JUSTERR",
	},
	{"false; a=b", ""},
	{"false; false &", ""},
	{
		"GOSH_CMD=exit_0 $GOSH_PROG; echo next",
		"next\n",
	},
	{
		"GOSH_CMD=exit_5 $GOSH_PROG; echo next",
		"next\n",
	},
	{
		"! GOSH_CMD=exit_0 $GOSH_PROG",
		"exit status 1",
	},
	{
		"! GOSH_CMD=exit_5 $GOSH_PROG",
		"",
	},

	// we don't need to follow bash error strings
	{"exit a", "exit: a: numeric argument required\nexit status 2 #JUSTERR"},
	{"exit 1 2", "exit: too many arguments\nexit status 2 #JUSTERR"},
	{"f() { return a; }; f", "return: a: numeric argument required\nexit status 2 #JUSTERR"},
	{"f() { return a; echo bad; }; f; echo after:$?", "return: a: numeric argument required\nafter:2\n #JUSTERR"},
	{"return 1 2", "return: too many arguments\nexit status 2 #JUSTERR"},

	// echo
	{"echo", "\n"},
	{"echo a b c", "a b c\n"},
	{"echo -n foo", "foo"},
	{`echo -e '\t'`, "\t\n"},
	{`echo -E '\t'`, "\\t\n"},
	{`echo -e 'before\x00after'`, "before\x00after\n"},
	{`echo -e '\x'`, "\\x\n"},
	{"echo -x foo", "-x foo\n"},
	{"echo -e -x -e foo", "-x -e foo\n"},

	// printf
	{"printf foo", "foo"},
	{"printf %%", "%"},
	{"printf %", "printf: `%': missing format character\nexit status 1 #JUSTERR"},
	{"printf %; echo foo", "printf: `%': missing format character\nfoo\n #IGNORE"},

	// printf -v: assign formatted output to the named variable instead
	// of writing to stdout.
	{"printf -v out 'x=%d' 7; echo $out", "x=7\n"},
	{"printf -v out '%s\\n' hi; echo \"$out\" | wc -c | tr -d ' '", "4\n"},
	{"declare -A A; printf -v 'A[ ]' '%s' X; declare -p A", "declare -A A=([\" \"]=\"X\" )\n"},
	{"printf -v 1bad 'x'", "printf: \"1bad\": not a valid identifier\nexit status 1 #JUSTERR"},
	{"printf -v", "printf: -v: option requires an argument\nexit status 2 #JUSTERR"},

	// printf %b: interpret backslash escapes inside the *argument*
	// (not the format string), so `printf '%b\n' 'a\tb'` outputs a
	// literal tab.
	{`printf '%b' 'foo\nbar'`, "foo\nbar"},
	{`printf '%b\n' 'a\tb'`, "a\tb\n"},
	{`printf '\0007'`, "\x007"},
	{`printf '%b' '\0007'`, "\a"},
	{`printf '\0200'`, "\x100"},
	{`printf '%b' '\0200'`, "\x80"},

	// printf %(fmt)T: strftime-style datetime. Year and Unix-time
	// specifiers are timezone-stable for fixed timestamps.
	{`printf '%(%Y)T\n' 1700000000`, "2023\n"},
	{`printf '%(%s)T\n' 1700000000`, "1700000000\n"},
	{`TZ=EST5EDT,M3.2.0/2,M11.1.0/2 printf '%()T %(%x %X)T\n' 1275250155 1275246555`, "16:09:15 05/30/10 15:09:15\n"},
	{`TZ=EST5EDT,M3.2.0/2,M11.1.0/2 printf '%-12.20(%H:%M:%S)T!\n' 1275250155`, "16:09:15    !\n"},
	{`TZ=EST5EDT,M3.2.0/2,M11.1.0/2 printf '%.50(%x (foo) %X)T\n' 1275250155`, "05/30/10 (foo) 16:09:15\n"},
	{`printf '%(abde)Z\n'`, "printf: warning: `Z': invalid time format specification\n%(abde)Z\nexit status 1 #JUSTERR"},
	{`printf '%(%%)T' 1700000000`, "%"},
	// Unknown specifier passes through verbatim, matching bash.
	{`printf '%(%q)T\n' 1700000000`, "%q\n"},

	// printf -- ends option parsing so a format starting with - works.
	{`printf -- '-x: %s\n' world`, "-x: world\n"},

	// bash 5.3 funsub ${ cmd; }: runs body in caller's scope (no
	// subshell), captures stdout. Distinct from $(...) which subshells.
	{`v=${ echo hi; }; echo "$v"`, "hi\n"},
	{`v=${ echo a; echo b; }; echo "[$v]"`, "[a\nb]\n"},
	// Same process and caller scope for ordinary assignments. Note the
	// contrast with $(...) on the line after, which subshells.
	{`x=before; v=${ x=after; echo cap; }; echo "$v $x"`, "cap after\n"},
	{`x=before; v=$(x=after; echo cap); echo "$v $x"`, "cap before\n"},
	// Fresh variables assigned inside the funsub also persist.
	{`v=${ newvar=hello; echo cap; }; echo "v=$v newvar=${newvar-unset}"`, "v=cap newvar=hello\n"},
	// `local` inside the funsub body is legal (the body is function-scoped).
	{`x=outer; v=${ local x=inner; echo "in=$x"; }; echo "$v out=$x"`, "in=inner out=outer\n"},
	// Multiple unrelated assignments in the body leak unless local.
	{`a=1; b=2; v=${ a=99; b=99; c=99; echo cap; }; echo "$v $a $b ${c-u}"`, "cap 99 99 99\n"},
	// bash 5.3 valsub ${|cmd;}: stdout passes through; the expansion
	// value comes from REPLY.
	{`v=${| echo hello; REPLY=value; }; echo "v=$v REPLY=$REPLY"`, "hello\nv=value REPLY=\n"},
	{`v=${| REPLY=$'a\n\n'; }; printf '<%s>\n' "$v"`, "<a\n\n>\n"},
	// `return` inside funsub is local to the body (like a function);
	// `exit` propagates out (kills the shell). Mirrors bash 5.3.
	{`v=${ echo a; return; echo b; }; echo "[$v]"`, "[a]\n"},
	{`v=${ echo a; exit 2; echo b; }; echo never`, "exit status 2"},
	{`set -e; v=${ echo a; false; echo b; }; echo "[$v]"`, "[a\nb]\n"},
	{`set -e -o posix; v=${ echo a; false; echo b; }; echo "[$v]"`, "exit status 1"},
	// `exit 0` from a funsub also propagates — without preserving the
	// funsub's exit status as lastExpandExit, the assignment path's
	// "restore lastExpandExit on success" recovery would silently
	// clear the exiting flag.
	{`v=${ exit 0; }; echo never`, ""},
	// `break` inside funsub breaks the enclosing loop.
	{`for i in 1 2 3; do v=${ break; }; echo "i=$i"; done; echo done`, "done\n"},
	// positional-parameter changes (shift, set --) are NOT variable
	// assignments per bash 5.3 funsub spec, so they DO leak from a funsub
	// body. Wrapping the body in (...) subshells the call and isolates
	// them. Mirrors bash 5.3 comsub2.tests lines 117-130.
	{`set -- 1 2; : "${ shift; }"; echo "$@"`, "2\n"},
	{`set -- 1 2; : "${| shift; }"; echo "$@"`, "2\n"},
	{`set -- 1 2; : "${ ( shift ); }"; echo "$@"`, "1 2\n"},

	// runner-state introspection builtin emits JSON; check it round-trips
	// by extracting a known key via grep -q.
	{`f(){ :; }; runner-state funcs | grep -q '"funcs":\[.*"f"' && echo ok`, "ok\n"},
	{`runner-state bogus`, "runner-state: unknown section \"bogus\" (try: vars opts traps fds funcs callstack all)\nexit status 2 #JUSTERR"},
	{"printf %1", "printf: `%1': missing format character\nexit status 1 #JUSTERR"},
	{"printf %+", "printf: `%+': missing format character\nexit status 1 #JUSTERR"},
	{"printf %B foo", "printf: `B': invalid format character\nexit status 1 #JUSTERR"},
	{"printf 'ab%Mcd\n'; printf '%y' 0", "printf: `M': invalid format character\nabprintf: `y': invalid format character\nexit status 1 #JUSTERR"},
	{"printf %12-s foo", "printf: `-': invalid format character\nexit status 1 #JUSTERR"},
	{"printf ' %s \n' bar", " bar \n"},
	{"printf '\\A'", "\\A"},
	{"printf %s foo", "foo"},
	{"printf %s", ""},
	{"printf %d,%i 3 4", "3,4"},
	{"printf %d", "0"},
	{"printf '%d' ''", "printf: : invalid number\n0exit status 1 #JUSTERR"},
	{"printf '%d' 2>/dev/null", "0"},
	{"printf %d,%d 010 0x10", "8,16"},
	{"printf %c,%c,%c foo àa", "f,\xc3,\x00"}, // TODO: use a rune?
	{"printf '%2c\\n' 65", " 6\n"},
	{"printf '%-2c--\\n' 65", "6 --\n"},
	{"printf %3s a", "  a"},
	{"printf '%#q\\n' no-quotes-needed 'quotes;needed'", "'no-quotes-needed'\n'quotes;needed'\n"},
	{"printf -v out '%#q\\n' \"a'b\"; printf '%s' \"$out\"", "'a'\\''b'\n"},
	{"printf %3i 1", "  1"},
	{"printf %+i%+d 1 -3", "+1-3"},
	{"printf 'x%-+10.0fx\\n' 123", "x+123      x\n"},
	{"printf 'x%-+10.0dx\\n' 123", "x+123      x\n"},
	{"printf 'x%+010.0xx\\n' 123", "x        7bx\n"},
	{"printf '%.2ls\\n' 'ಇಳಿಕೆಗಳು'", "ಇಳ\n"},
	{"printf '%4.2lc---\\n' 'ಇ'", "   ಇ---\n"},
	{"printf '%S %C\\n' 'ಇಳ' 'ಇ'", "ಇಳ ಇ\n"},
	{"printf %-5x 10", "a    "},
	{"printf '[%*s]\\n' 9223372036854775825 X 2>/dev/null", "[X]\nexit status 1"},
	{"printf '[%.*s]\\n' 9223372036854775825 X 2>/dev/null", "[X]\nexit status 1"},
	{"printf '%.9223372036854775825s\\n' XY", "XY\n"},
	{"printf '%.9223372036854775825Q\\n' XY 2>/dev/null", "XY\nexit status 1"},
	{"printf %02x 1", "01"},
	{"printf 'a% 5s' a", "a    a"},
	{"printf 'nofmt' 1 2 3", "nofmt"},
	{"printf '%d_' 1 2 3", "1_2_3_"},
	{"printf '%02d %02d\n' 1 2 3", "01 02\n03 00\n"},
	{`printf '0%s1' 'a\bc'`, `0a\bc1`},
	{`printf '0%b1' 'a\bc'`, "0a\bc1"},
	{"printf 'a%bc'", "ac"},
	{"printf 'before\\x00after'", "before\x00after"},

	// printf escape sequences at end of format string (must not panic)
	{"printf '\\0'", "\x00"},
	{"printf '\\01'", "\x01"},
	{"printf '\\x'", "printf: missing hex digit for \\x\n\\xexit status 1"},
	{"printf 'a\\0'", "a\x00"},
	{"printf '\\\\'", "\\"},

	// words and quotes
	{"echo  foo ", "foo\n"},
	{"echo ' foo '", " foo \n"},
	{`echo " foo "`, " foo \n"},
	{`echo a'b'c"d"e`, "abcde\n"},
	{`a=" b c "; echo $a`, "b c\n"},
	{`a=" b c "; echo "$a"`, " b c \n"},
	{`a=" b c "; echo foo${a}bar`, "foo b c bar\n"},
	{`a="b    c"; echo foo${a}bar`, "foob cbar\n"},
	{`echo "$(echo ' b c ')"`, " b c \n"},
	{"echo ''", "\n"},
	{`$(echo)`, ""},
	{`printf '<%s>\n' $(printf hello)`, "<hello>\n"},
	{`printf '<%s>\n' $(printf 'a b')`, "<a>\n<b>\n"},
	{`set -- $(printf 'foo bar'); echo $#:$1,$2`, "2:foo,bar\n"},
	{`echo -n '\\'`, `\\`},
	{`echo -n "\\"`, `\`},
	{`set -- a b c; x="$@"; echo "$x"`, "a b c\n"},
	{`set -- b c; echo a"$@"d`, "ab cd\n"},
	{`count() { echo $#; }; set --; count "$@"`, "0\n"},
	{`count() { echo $#; }; set -- ""; count "$@"`, "1\n"},
	{`count() { echo $#; }; set -- ""; shift; count "$@"`, "0\n"},
	{`count() { echo $#; }; a=(); count "${a[@]}"`, "0\n"},
	{`count() { echo $#; }; count "${unset_var[@]}"`, "0\n"},
	{`count() { echo $#; }; a=(""); count "${a[@]}"`, "1\n"},
	{`echo $1 $3; set -- a b c; echo $1 $3`, "\na c\n"},
	{`[[ $0 == "bash" || $0 == "gosh" || $0 == "bashy" ]]`, ""},

	// dollar quotes
	{`echo $'foo\nbar'`, "foo\nbar\n"},
	{`echo $'\r\t\\'`, "\r\t\\\n"},
	{`echo $"foo\nbar"`, "foo\\nbar\n"},
	{`echo $'%s'`, "%s\n"},
	{`a=$'\r\t\\'; echo "$a"`, "\r\t\\\n"},
	{`a=$"foo\nbar"; echo "$a"`, "foo\\nbar\n"},
	{`echo $'\a\b\e\E\f\v'`, "\a\b\x1b\x1b\f\v\n"},
	{`echo $'\\\'\"\?'`, "\\'\"?\n"},
	{`echo $'\1\45\12345\777\9'`, "\x01%S45\xff\\9\n"},
	{`echo $'\x\xf\x09\xAB'`, "\\x\x0f\x09\xab\n"},
	{`echo $'\u\uf\u09\uABCD\u00051234'`, "\\u\u000f\u0009\uabcd\u00051234\n"},
	{`echo $'\U\Uf\U09\UABCD\U00051234'`, "\\U\u000f\u0009\uabcd\U00051234\n"},
	{
		"echo 'before\x00after'",
		"beforeafter\n",
	},
	{
		"echo \"before\x00after\"",
		"beforeafter\n",
	},
	{
		"echo $'before\x00after'",
		"beforeafter\n",
	},
	{
		"echo $'before\\x00after'",
		"before\n",
	},
	{
		"echo $'before\\xafter'",
		"before\xafter\n",
	},
	{
		"a='before\x00after'; eval \"echo -n ${a} ${a@Q}\";",
		"beforeafter beforeafter",
	},
	{
		"a=$'before\\x00after'; eval \"echo -n ${a} ${a@Q}\";",
		"before before",
	},
	{
		"i\x00f true; then echo before\x00; \x00fi",
		"before\n",
	},
	{
		"echo $(GOSH_CMD=foo_null_bar $GOSH_PROG)",
		"foobar\n #IGNORE",
	},
	// See the TODO where foo_NULL_BAR is set.
	// {
	// 	"echo $foo_NULL_BAR \"${foo_NULL_BAR}\"",
	// 	"foo\n",
	// },

	// escaped chars
	{"echo a\\b", "ab\n"},
	{"echo a\\ b", "a b\n"},
	{"echo \\$a", "$a\n"},
	{"echo \"a\\b\"", "a\\b\n"},
	{"echo 'a\\b'", "a\\b\n"},
	{"echo \"a\\\nb\"", "ab\n"},
	{"echo 'a\\\nb'", "a\\\nb\n"},
	{`echo "\""`, "\"\n"},
	{`echo \\`, "\\\n"},
	{`echo \\\\`, "\\\\\n"},
	{`echo \`, "\\\n"},

	// escape characters in double quote literal
	{`echo "\\"`, "\\\n"},     // special character is preserved
	{`echo "\b"`, "\\b\n"},    // non-special character has both characters preserved
	{`echo "\\\\"`, "\\\\\n"}, // sequential backslashes (escape characters repeated sequentially)

	// vars
	{"foo=bar; echo $foo", "bar\n"},
	{"foo=bar foo=etc; echo $foo", "etc\n"},
	{"foo=bar; foo=etc; echo $foo", "etc\n"},
	{"foo=bar; foo=; echo $foo", "\n"},
	{"unset foo; echo $foo", "\n"},
	{"foo=bar; unset foo; echo $foo", "\n"},
	{"echo $INTERP_GLOBAL", "value\n"},
	{"INTERP_GLOBAL=; echo $INTERP_GLOBAL", "\n"},
	{"unset INTERP_GLOBAL; echo $INTERP_GLOBAL", "\n"},
	{"echo $MIXEDCASE_INTERP_GLOBAL", "value\n"},
	{"foo=bar; foo=x true; echo $foo", "bar\n"},
	{"foo=bar; foo=x true; echo $foo", "bar\n"},
	{"foo=bar; $ENV_PROG | grep '^foo='", "exit status 1"},
	{"foo=bar $ENV_PROG | grep '^foo='", "foo=bar\n"},
	{"foo=a foo=b $ENV_PROG | grep '^foo='", "foo=b\n"},
	{"$ENV_PROG | grep -i '^interp_global='", "INTERP_GLOBAL=value\n"},
	{"INTERP_GLOBAL=new; $ENV_PROG | grep -i '^interp_global='", "INTERP_GLOBAL=new\n"},
	{"INTERP_GLOBAL=; $ENV_PROG | grep -i '^interp_global='", "INTERP_GLOBAL=\n"},
	{"unset INTERP_GLOBAL; $ENV_PROG | grep -i '^interp_global='", "exit status 1"},
	{"a=b; a+=c x+=y; echo $a $x", "bc y\n"},
	{`a=" x  y"; b=$a c="$a"; echo $b; echo $c`, "x y\nx y\n"},
	{`a=" x  y"; b=$a c="$a"; echo "$b"; echo "$c"`, " x  y\n x  y\n"},
	{`arr=("foo" "bar" "lala" "foobar"); echo ${arr[@]:2}; echo ${arr[*]:2}`, "lala foobar\nlala foobar\n"},
	{`arr=("foo" "bar" "lala" "foobar"); echo ${arr[@]:2:4}; echo ${arr[*]:1:4}`, "lala foobar\nbar lala foobar\n"},
	{`arr=("foo" "bar"); echo ${arr[@]}; echo ${arr[*]}`, "foo bar\nfoo bar\n"},
	{`arr=("foo"); echo ${arr[@]:99}`, "\n"},
	{`echo ${arr[@]:1:99}; echo ${arr[*]:1:99}`, "\n\n"},
	{`arr=(0 1 2 3 4 5 6 7 8 9 0 a b c d e f g h); echo ${arr[@]:3:4}`, "3 4 5 6\n"},
	{`v=ಇಳಿಕೆಗಳು; printf '<%s> <%s>\n' "${v:0:2}" "${v:0:1}"`, "<ಇಳ> <ಇ>\n"},

	// quoted array slicing
	{`a=(1 2 3 4 5); echo "${a[@]:2:2}"`, "3 4\n"},
	{`a=(1 2 3 4 5); echo "${a[*]:2:2}"`, "3 4\n"},
	{`a=(1 2 3 4 5); b=("${a[@]:2:2}"); echo ${#b[@]}`, "2\n"},
	{`a=(1 2 3 4 5); echo "${a[@]:3}"`, "4 5\n"},
	{`a=(1 2 3 4 5); echo "${a[@]: -2}"`, "4 5\n"},
	{`a=(1 2 3 4 5); echo "${a[@]: -99}"`, "\n"},

	// positional parameter slicing (1-based offset, $0 at offset 0)
	{`f() { echo "${@:2:2}"; }; f a b c d e`, "b c\n"},
	{`f() { echo ${@:2:2}; }; f a b c d e`, "b c\n"},
	{`f() { echo "${@:1}"; }; f a b c`, "a b c\n"},
	{`f() { echo "${*:2:2}"; }; f a b c d e`, "b c\n"},
	{`f() { echo "${@: -2}"; }; f a b c d e`, "d e\n"},
	{`f() { echo "${@: -3:2}"; }; f a b c d e`, "c d\n"},
	{`f() { echo "${@:1:0}"; }; f a b c`, "\n"},
	{`f() { echo "${@:99}"; }; f a b c`, "\n"},
	{`set -- a b c; v=("${@:0:2}"); echo "${#v[@]}"`, "2\n"},
	{`f() { for x in "${@:2:2}"; do echo "$x"; done; }; f a b c d e`, "b\nc\n"},
	{`set --; v=("${@:0}"); echo "${#v[@]}"`, "1\n"},
	{`f() { echo "${@: -10}"; }; f a b c`, "\n"},

	{`echo ${foo[@]}; echo ${foo[*]}`, "\n\n"},
	// TODO: reenable once we figure out the broken pipe error
	//{`$ENV_PROG | while read line; do if test -z "$line"; then echo empty; fi; break; done`, ""}, // never begin with an empty element

	// inline variables have special scoping
	{
		"f() { echo $inline; inline=bar true; echo $inline; }; inline=foo f",
		"foo\nfoo\n",
	},
	{"v=x; read v <<< 'y'; echo $v", "y\n"},
	{"v=x; v=inline read v <<< 'y'; echo $v", "x\n"},
	{"v=x; v=inline unset v; echo $v", "x\n"},
	{"v=x; echo 'v=y' >f; v=inline source ./f; echo $v", "x\n"},
	{"declare -n v=v2; v=inline true; echo $v $v2", "\n"},
	{"f() { echo $v; }; v=x; v=y f; f", "y\nx\n"},
	{"f() { echo $v; }; v=x; v+=y f; f", "xy\nx\n"},
	{"f() { echo $v; }; declare -n v=v2; v2=x; v=y f; f", "y\nx\n"},
	{"f() { echo ${v[@]}; }; v=(e1 e2); v=y f; f", "y\ne1 e2\n"},

	// special vars
	{"echo $?; false; echo $?", "0\n1\n"},
	{"for i in 1 2; do\necho $LINENO\necho $LINENO\ndone", "2\n3\n2\n3\n"},
	{"[[ -n $$ && $$ -gt 0 ]]", ""},
	{"[[ $$ -eq $PPID ]]", "exit status 1"},
	// [[ ]] && / || must short-circuit so the rhs (and its expansions) is
	// never evaluated when the lhs settles the result. Mirrors bash's
	// cond.tests `[[ -n $TDIR || $HOME -ef ${H*} ]]` — the unevaluatable
	// ${H*} on the rhs must never run.
	{`TDIR=set; [[ -n $TDIR || $(echo SIDE >&2; echo x) ]] && echo ok 2>&1`, "ok\n"},
	{`unset TDIR; [[ -n $TDIR && $(echo SIDE >&2; echo x) ]] 2>&1 || echo no`, "no\n"},
	{"[[ $RANDOM -eq $RANDOM ]]", "exit status 1"},   // 1 in 32k chance of a collision, 0.003%
	{"[[ $SRANDOM -eq $SRANDOM ]]", "exit status 1"}, // 1 in 2**32 chance of a collision,
	{"RANDOM=42; echo $RANDOM $RANDOM", "17772 26794\n"},
	{"RANDOM=42; echo $RANDOM ${ echo $RANDOM; }", "17772 26794\n"},

	// Ensure that we consistently use 64 bits even on 32-bit platforms.
	// Bash doesn't do this, but we do, for portability and consistency.
	{"[[ 1000000000123 -lt 100 ]]", "exit status 1"},
	{"[[ 1000000000123 -eq 1000000000456 ]]", "exit status 1"},
	{"[[ 1000000000123 < 100 ]]", "exit status 1"},
	{"((1000000000123 == 1000000000456))", "exit status 1"},
	{"(( array[0]++ )); echo ${array[0]}; (( array[0] ++ )); echo ${array[0]}", "1\n2\n"},
	{"(( ++array[1] )); echo ${array[1]}", "1\n"},
	{"v=4; DIND=20; (( dice[DIND/v]+=2 )); echo ${dice[5]}", "2\n"},

	// var manipulation
	{"echo ${#a} ${#a[@]}", "0 0\n"},
	{"a=bar; echo ${#a} ${#a[@]}", "3 1\n"},
	{"a=世界; echo ${#a}", "2\n"},
	{"a=(a bcd); echo ${#a} ${#a[@]} ${#a[*]} ${#a[1]}", "1 2 2 3\n"},
	{
		"a=($(echo a bcd)); echo ${#a} ${#a[@]} ${#a[*]} ${#a[1]}",
		"1 2 2 3\n",
	},
	{
		"a=([0]=$(echo a b) $(echo c d)); echo ${#a} ${#a[@]} ${#a[*]} ${#a[0]}",
		"3 3 3 3\n",
	},
	{"set -- a bc; echo ${#@} ${#*} $#", "2 2 2\n"},
	{
		"echo ${!a}; echo more",
		"a: invalid indirect expansion\nexit status 1 #JUSTERR",
	},
	{
		"a=b; echo ${!a}; b=c; echo ${!a}",
		"\nc\n",
	},
	{
		"set -- a b c d e f g h; z=abcdefghijklmnop; echo ${!9:-$z}",
		"abcdefghijklmnop\n",
	},
	{
		"a=foo_very_long; echo ${a:1}; echo ${a: -1}; echo ${a: -10}; echo ${a:5}",
		"oo_very_long\ng\n_very_long\nery_long\n",
	},
	{
		"a=foo_very_long; echo ${a::2}; echo ${a::-1}; echo ${a: -10}; echo ${a::5}",
		"fo\nfoo_very_lon\n_very_long\nfoo_v\n",
	},
	{
		"a=abc; echo ${a:1:1}",
		"b\n",
	},
	{
		"a=foo; echo ${a/no/x} ${a/o/i} ${a//o/i} ${a/fo/}",
		"foo fio fii o\n",
	},
	{
		"a=foo; echo ${a/*/xx} ${a//?/na} ${a/o*}",
		"xx nanana f\n",
	},
	{
		"a=12345; echo ${a//[42]} ${a//[^42]} ${a//[!42]}",
		"135 24 24\n",
	},
	{"a=0123456789; echo ${a//[1-35-8]}", "049\n"},
	{"a=]abc]; echo ${a//[]b]}", "ac\n"},
	{"a=-abc-; echo ${a//[-b]}", "ac\n"},
	{`a='x\y'; echo ${a//\\}`, "xy\n"},
	{"a=']'; echo ${a//[}", "]\n"},
	{"a=']'; echo ${a//[]}", "]\n"},
	{"a=']'; echo ${a//[]]}", "\n"},
	{"a='['; echo ${a//[[]}", "\n"},
	{"a=']'; echo ${a//[xy}", "]\n"},
	{"a='abc123'; echo ${a//[[:digit:]]}", "abc\n"},
	{"a='[[:wrong:]]'; echo ${a//[[:wrong:]]}", "[[:wrong:]]\n"},
	{"a='[[:wrong:]]'; echo ${a//[[:}", "[[:wrong:]]\n"},
	{"a='abcx1y'; echo ${a//x[[:digit:]]y}", "abc\n"},
	{`a=xyz; echo "${a/y/a  b}"`, "xa  bz\n"},
	{"a='foo/bar'; echo ${a//o*a/}", "fr\n"},
	{"a=foobar; echo ${a//a/} ${a///b} ${a///}", "foobr foobar foobar\n"},
	{
		"echo ${a:-b}; echo $a; a=; echo ${a:-b}; a=c; echo ${a:-b}",
		"b\n\nb\nc\n",
	},
	{
		"echo ${#:-never} ${?:-never} ${LINENO:-never}",
		"0 0 1\n",
	},
	{
		"echo ${1-one} ${2-two} ${3-three}",
		"one two three\n",
	},
	{
		"set -u; echo ${1}",
		"1: unbound variable\nexit status 1 #JUSTERR",
	},
	{
		"echo ${a-b}; echo $a; a=; echo ${a-b}; a=c; echo ${a-b}",
		"b\n\n\nc\n",
	},
	{
		"echo ${a:=b}; echo $a; a=; echo ${a:=b}; a=c; echo ${a:=b}",
		"b\nb\nb\nc\n",
	},
	{
		"echo ${a=b}; echo $a; a=; echo ${a=b}; a=c; echo ${a=b}",
		"b\nb\n\nc\n",
	},
	{
		"echo ${a:+b}; echo $a; a=; echo ${a:+b}; a=c; echo ${a:+b}",
		"\n\n\nb\n",
	},
	{
		"echo ${a+b}; echo $a; a=; echo ${a+b}; a=c; echo ${a+b}",
		"\n\nb\nb\n",
	},
	{
		"a=b; echo ${a:?err1}; a=; echo ${a:?err2}; unset a; echo ${a:?err3}",
		"b\na: err2\nexit status 1 #JUSTERR",
	},
	{
		"a=b; echo ${a?err1}; a=; echo ${a?err2}; unset a; echo ${a?err3}",
		"b\n\na: err3\nexit status 1 #JUSTERR",
	},
	{
		"echo ${a:?%s}",
		"a: %s\nexit status 1 #JUSTERR",
	},
	{
		"x=aaabccc; echo ${x#*a}; echo ${x##*a}",
		"aabccc\nbccc\n",
	},
	{
		"shopt -s extglob; x=000987; echo ${x##*(0)}",
		"987\n",
	},
	{
		"shopt -s extglob; x=abcdef; echo ${x#+(a|abc)}; echo ${x##+(a|abc)}",
		"bcdef\ndef\n",
	},
	{
		"shopt -s extglob; TEST='a , b'; echo ${TEST//*([[:space:]]),*([[:space:]])/,}",
		"a,b\n",
	},
	{
		"x=(__a _b c_); echo ${x[@]#_}",
		"_a b c_\n",
	},
	{
		"shopt -s extglob; x=(000987 00123); printf '<%s>\\n' \"${x[@]##*(0)}\"",
		"<987>\n<123>\n",
	},
	{
		"x=(a__ b_ _c); echo ${x[@]%%_}",
		"a_ b _c\n",
	},
	{
		"x=aaabccc; echo ${x%c*}; echo ${x%%c*}",
		"aaabcc\naaab\n",
	},
	{
		"x=aaabccc; echo ${x%%[bc}",
		"aaabccc\n",
	},
	{
		"a='àÉñ bAr'; echo ${a^}; echo ${a^^}",
		"ÀÉñ bAr\nÀÉÑ BAR\n",
	},
	{
		"a='àÉñ bAr'; echo ${a,}; echo ${a,,}",
		"àÉñ bAr\nàéñ bar\n",
	},
	{
		"a='àÉñ bAr'; echo ${a^?}; echo ${a^^[br]}",
		"ÀÉñ bAr\nàÉñ BAR\n",
	},
	{
		"a='àÉñ bAr'; echo ${a,?}; echo ${a,,[br]}",
		"àÉñ bAr\nàÉñ bAr\n",
	},
	{
		"a=(àÉñ bAr); echo ${a[@]^}; echo ${a[*],,}",
		"ÀÉñ BAr\nàéñ bar\n",
	},
	{
		"INTERP_X_1=a INTERP_X_2=b; echo ${!INTERP_X_*}",
		"INTERP_X_1 INTERP_X_2\n",
	},
	{
		"INTERP_X_2=b INTERP_X_1=a; echo ${!INTERP_*}",
		"INTERP_GLOBAL INTERP_X_1 INTERP_X_2\n",
	},
	{
		`INTERP_X_2=b INTERP_X_1=a; set -- ${!INTERP_*}; echo $#`,
		"3\n",
	},
	{
		`INTERP_X_2=b INTERP_X_1=a; set -- "${!INTERP_*}"; echo $#`,
		"1\n",
	},
	{
		`INTERP_X_2=b INTERP_X_1=a; set -- ${!INTERP_@}; echo $#`,
		"3\n",
	},
	{
		`INTERP_X_2=b INTERP_X_1=a; set -- "${!INTERP_@}"; echo $#`,
		"3\n",
	},
	{
		`a='b  c'; eval "echo -n ${a} ${a@Q}"`,
		`b c b  c`,
	},
	{
		`a='"\n'; printf "%s %s" "${a}" "${a@E}"`,
		"\"\\n \"\n",
	},

	// ${var@a} and ${var@A}
	{
		`a=foo; echo "<${a@a}>"`,
		"<>\n",
	},
	{
		`declare -a arr=(1 2 3); echo "${arr@a}"`,
		"a\n",
	},
	{
		`declare -A map=([k]=v); echo "${map@a}"`,
		"A\n",
	},
	{
		`export e=1; echo "${e@a}"`,
		"x\n",
	},
	{
		`readonly ro=1; echo "${ro@a}"`,
		"r\n",
	},
	{
		`declare -a arr=(1); export arr; echo "${arr@a}"`,
		"ax\n",
	},
	{
		`a=hello; echo "${a@A}"`,
		"a=hello\n #IGNORE bash always single-quotes",
	},
	{
		`export e=1; echo "${e@A}"`,
		"declare -x e=1\n #IGNORE bash always single-quotes",
	},
	{
		`a=Hello; echo "${a@U}"`,
		"HELLO\n",
	},
	{
		`a=hello; echo "${a@u}"`,
		"Hello\n",
	},
	{
		`a=HELLO; echo "${a@L}"`,
		"hello\n",
	},
	{
		`a=foo; echo "<${a@K}><${a@k}>"`,
		"<foo><foo>\n #IGNORE not implemented; must not panic",
	},
	{
		"declare a; a+=(b); echo ${a[@]} ${#a[@]}",
		"b 1\n",
	},
	{
		`a=""; a+=(b); echo ${a[@]} ${#a[@]}`,
		"b 2\n",
	},
	{
		"f() { local a; a=bad; a=good; echo $a; }; f",
		"good\n",
	},
	{
		`declare x; [[ -v x ]] && echo set || echo unset`,
		"unset\n",
	},
	{
		`declare x=; [[ -v x ]] && echo set || echo unset`,
		"set\n",
	},
	{
		`declare -a x; [[ -v x ]] && echo set || echo unset`,
		"unset\n",
	},
	{
		`declare -A x; [[ -v x ]] && echo set || echo unset`,
		"unset\n",
	},
	{
		`declare -a a; a[1]=1; [[ -v a ]] && echo set || echo unset; [[ -v a[@] ]] && echo set || echo unset`,
		"unset\nset\n",
	},
	{
		`declare -A A; A[a]=1; [[ -v A ]] && echo set || echo unset; [[ -v A[@] ]] && echo at || echo no-at; A[@]=2; [[ -v A[@] ]] && echo at || echo no-at`,
		"unset\nno-at\nat\n",
	},
	{
		`scalar=; [[ -v scalar[@] ]] && echo set || echo unset`,
		"set\n",
	},
	{
		`declare -r -x x; [[ -v x ]] && echo set || echo unset`,
		"unset\n",
	},
	{
		`declare -n x; [[ -v x ]] && echo set || echo unset`,
		"unset\n",
	},

	// declare -f and declare -p
	{
		`f() { echo hello; }; declare -f f`,
		"f () \n{ \n    echo hello\n}\n",
	},
	{
		`f() { echo hello; }; declare -f -p f`,
		"f () \n{ \n    echo hello\n}\n",
	},
	{
		`declare -f nonexistent 2>/dev/null; echo "exit: $?"`,
		"exit: 1\n",
	},
	{
		`declare -f -p nonexistent 2>/dev/null; echo "exit: $?"`,
		"exit: 1\n",
	},
	{
		`f() { :; }; declare -f -a f; declare -f -i f g`,
		"declare: -a: invalid option\ndeclare: -i: invalid option\nexit status 2 #JUSTERR",
	},
	{
		`f() { :; }; readonly -f f; declare -f +r f`,
		"declare: f: readonly function\nexit status 1 #JUSTERR",
	},
	{
		`f() { :; }; declare -fr f; declare -F -r`,
		"declare -fr f\n",
	},
	{
		`declare -f f='() { :; }'`,
		"declare: cannot use `-f' to make functions\nexit status 1 #JUSTERR",
	},
	{
		`x=1; export -f x`,
		"export: x: not a function\nexit status 1 #JUSTERR",
	},
	{
		`f() { echo hello; }; declare -f f >/dev/null && echo "f exists"`,
		"f exists\n",
	},
	{
		`a=hello; declare -p a`,
		"declare -- a=\"hello\"\n",
	},
	{
		`declare a; declare -p a`,
		"declare -- a\n",
	},
	{
		`declare -a arr=(1 2 3); declare -p arr`,
		"declare -a arr=([0]=\"1\" [1]=\"2\" [2]=\"3\")\n",
	},
	{
		`declare -a arr; declare -p arr; arr=(); declare -p arr`,
		"declare -a arr\ndeclare -a arr=()\n",
	},
	{
		`declare -a | grep -E '^(declare -a BASH_ARGC|declare -a BASH_ARGV|declare -a FUNCNAME)'`,
		"declare -a BASH_ARGC=()\ndeclare -a BASH_ARGV=()\ndeclare -a FUNCNAME\n",
	},
	{
		`declare -A assoc; declare -p assoc; assoc=(); declare -p assoc`,
		"declare -A assoc\ndeclare -A assoc=()\n",
	},
	{
		`declare -A assoc=([foo]=bar); declare -p assoc`,
		"declare -A assoc=([foo]=\"bar\" )\n",
	},
	{
		`declare -A assoc; key='x],b[$(echo uname >&2)'; (( assoc[$key]++ )); assoc[!]=bang; assoc[%]=pct; declare -p assoc`,
		"declare -A assoc=([%]=\"pct\" [\"!\"]=\"bang\" [\"x],b[\\$(echo uname >&2)\"]=\"1\" )\n",
	},
	{
		`declare -ai arr=(1+1); declare -p arr`,
		"declare -ai arr=([0]=\"2\")\n",
	},
	{
		`readonly -a arr=(); declare -p arr`,
		"declare -ar arr=()\n",
	},
	{
		`declare -r c[100]; declare -p c`,
		"declare -ar c\n",
	},
	{
		`readonly a[5]`,
		"readonly: `a[5]': not a valid identifier\nexit status 1 #JUSTERR",
	},
	{
		`export e=1; declare -p e`,
		"declare -x e=\"1\"\n",
	},
	{
		`readonly c=immutable; declare -p c`,
		"declare -r c=\"immutable\"\n",
	},
	{
		`declare -p nonexistent 2>/dev/null; echo "exit: $?"`,
		"exit: 1\n",
	},

	// if
	{
		"if true; then echo foo; fi",
		"foo\n",
	},
	{
		"if false; then echo foo; fi",
		"",
	},
	{
		"if GOSH_CMD=print_fail $GOSH_PROG; then echo foo; fi",
		"exec fail\n",
	},
	{
		"if true; then echo foo; else echo bar; fi",
		"foo\n",
	},
	{
		"if false; then echo foo; else echo bar; fi",
		"bar\n",
	},
	{
		"if true; then false; fi",
		"exit status 1",
	},
	{
		"if false; then :; else false; fi",
		"exit status 1",
	},
	{
		"if false; then :; elif true; then echo foo; fi",
		"foo\n",
	},
	{
		"if false; then :; elif false; then :; elif true; then echo foo; fi",
		"foo\n",
	},
	{
		"if false; then :; elif false; then :; else echo foo; fi",
		"foo\n",
	},

	// while
	{
		"while false; do echo foo; done",
		"",
	},
	{
		"while GOSH_CMD=print_fail $GOSH_PROG; do echo foo; done",
		"exec fail\n",
	},
	{
		"while true; do exit 1; done",
		"exit status 1",
	},
	{
		"while true; do break; done",
		"",
	},
	{
		"while true; do while true; do break 2; done; done",
		"",
	},

	// until
	{
		"until true; do echo foo; done",
		"",
	},
	{
		"until false; do exit 1; done",
		"exit status 1",
	},
	{
		"until false; do break; done",
		"",
	},

	// for
	{
		"for i in 1 2 3; do echo $i; done",
		"1\n2\n3\n",
	},
	{
		"for i in 1 2 3; do echo $i; exit; done",
		"1\n",
	},
	{
		"for i in 1 2 3; do echo $i; false; done",
		"1\n2\n3\nexit status 1",
	},
	{
		"for i in 1 2 3; do echo $i; break; done",
		"1\n",
	},
	{
		"for i in 1 2 3; do echo $i; continue; echo foo; done",
		"1\n2\n3\n",
	},
	{
		"for i in 1 2; do for j in a b; do echo $i $j; continue 2; done; done",
		"1 a\n2 a\n",
	},
	{
		"for i in 1 2 3; do continue -1; done",
		"continue: -1: loop count out of range\nexit status 1 #JUSTERR",
	},
	{
		"for i in 1 2 3; do continue 0; done",
		"continue: 0: loop count out of range\nexit status 1 #JUSTERR",
	},
	{
		"for i in 1 2 3; do continue -- -5; done",
		"continue: -5: loop count out of range\nexit status 1 #JUSTERR",
	},
	{
		"for ((i=0; i<3; i++)); do echo $i; done",
		"0\n1\n2\n",
	},
	// for, with missing Init, Cond, Post
	{
		"i=0; for ((; i<3; i++)); do echo $i; done",
		"0\n1\n2\n",
	},
	{
		"for ((i=0;; i++)); do if [ $i -ge 3 ]; then break; fi; echo $i; done",
		"0\n1\n2\n",
	},
	{
		"for ((i=0; i<3;)); do echo $i; i=$((i+1)); done",
		"0\n1\n2\n",
	},
	{
		"i=0; for ((;;)); do if [ $i -ge 3 ]; then break; fi; echo $i; i=$((i+1)); done",
		"0\n1\n2\n",
	},
	// TODO: uncomment once expandEnv.Set starts returning errors
	// {
	// 	"readonly i; for ((i=0; i<3; i++)); do echo $i; done",
	// 	"0\n1\n2\n",
	// },
	{
		"for ((i=5; i>0; i--)); do echo $i; break; done",
		"5\n",
	},
	{
		"for i in 1 2; do for j in a b; do echo $i $j; done; break; done",
		"1 a\n1 b\n",
	},
	{
		"for i in 1 2 3; do :; done; echo $i",
		"3\n",
	},
	{
		"for ((i=0; i<3; i++)); do :; done; echo $i",
		"3\n",
	},
	{
		"set -- a 'b c'; for i in; do echo $i; done",
		"",
	},
	{
		"set -- a 'b c'; for i; do echo $i; done",
		"a\nb c\n",
	},

	// block
	{
		"{ echo foo; }",
		"foo\n",
	},
	{
		"{ false; }",
		"exit status 1",
	},

	// subshell
	{
		"(echo foo)",
		"foo\n",
	},
	{
		"(false)",
		"exit status 1",
	},
	{
		"(exit 1)",
		"exit status 1",
	},
	{
		"(false); echo foo",
		"foo\n",
	},
	{
		"(exit 0); echo foo",
		"foo\n",
	},
	{
		"(exit 1); echo foo",
		"foo\n",
	},
	{
		"(foo=bar; echo $foo); echo $foo",
		"bar\n\n",
	},
	{
		"(echo() { printf 'bar\n'; }; echo); echo",
		"bar\n\n",
	},
	{
		"unset INTERP_GLOBAL & echo $INTERP_GLOBAL",
		"value\n",
	},
	{
		"(fn() { :; }) & pwd >/dev/null",
		"",
	},
	{
		"x[0]=x; (echo ${x[0]}; x[0]=y; echo ${x[0]}); echo ${x[0]}",
		"x\ny\nx\n",
	},
	{
		`x[3]=x; (x[3]=y); echo ${x[3]}`,
		"x\n",
	},
	{
		"shopt -s expand_aliases; alias f='echo x'\nf\n(f\nalias f='echo y'\neval f\n)\nf\n",
		"x\nx\ny\nx\n",
	},
	{
		"set -- a; echo $1; (echo $1; set -- b; echo $1); echo $1",
		"a\na\nb\na\n",
	},
	{"false; ( echo $? )", "1\n"},

	// cd/pwd
	{"[[ fo~ == 'fo~' ]]", ""},
	{`[[ 'ab\c' == *\\* ]]`, ""},
	{`[[ foo/bar == foo* ]]`, ""},
	{"[[ a == [ab ]]", "exit status 1"},
	{`HOME='/*'; echo ~; echo "$HOME"`, "/*\n/*\n"},
	{`test -d ~`, ""},
	{
		`for flag in b c d e f g h k L p r s S u w x; do test -$flag ""; echo -n "$flag$? "; done`,
		`b1 c1 d1 e1 f1 g1 h1 k1 L1 p1 r1 s1 S1 u1 w1 x1 `,
	},
	{`foo=~; test -d $foo`, ""},
	{`foo=~; test -d "$foo"`, ""},
	{`foo='~'; test -d $foo`, "exit status 1"},
	{`foo='~'; [ $foo == '~' ]`, ""},
	{
		`[[ ~ == "$HOME" ]] && [[ ~/foo == "$HOME/foo" ]]`,
		"",
	},
	{
		`HOME=$PWD/home; mkdir home; touch home/f; [[ -e ~/f ]]`,
		"",
	},
	{
		`HOME=$PWD/home; mkdir home; touch home/f; [[ ~/f -ef $HOME/f ]]`,
		"",
	},
	{
		"[[ ~noexist == '~noexist' ]]",
		"",
	},
	{
		`w="$HOME"; cd; [[ $PWD == "$w" ]]`,
		"",
	},
	{
		`cd ''`,
		"cd: empty directory path\nexit status 1 #JUSTERR",
	},
	{
		`HOME=/foo; echo $HOME`,
		"/foo\n",
	},
	{
		"cd noexist",
		"cd: noexist: No such file or directory\nexit status 1 #JUSTERR",
	},
	{
		"mkdir -p a/b && cd a && cd b && cd ../..",
		"",
	},
	{
		">a && cd a",
		"cd: a: Not a directory\nexit status 1 #JUSTERR",
	},
	{
		`payload=$'\065\247\100\063\231\053\306\123\070\237\242\352\263'; cd "$payload"`,
		"cd: $'5\\247@3\\231+\\306S8\\237\\242\\352\\263': No such file or directory\nexit status 1 #JUSTERR",
	},
	{
		`[ "!" != "!" ]; echo bang:$?; [ "(" != "(" ]; echo paren:$?`,
		"bang:1\nparen:1\n",
	},
	{
		`[[ $PWD == "$(pwd)" ]]`,
		"",
	},
	{
		"PWD=changed; [[ $PWD == changed ]]",
		"",
	},
	{
		"PWD=changed; mkdir a; cd a; [[ $PWD == changed ]]",
		"exit status 1",
	},
	{
		`mkdir %s; old="$PWD"; cd %s; [[ $old == "$PWD" ]]`,
		"exit status 1",
	},
	{
		`old="$PWD"; mkdir a; cd a; cd ..; [[ $old == "$PWD" ]]`,
		"",
	},
	{
		`[[ $PWD == "$OLDPWD" ]]`,
		"exit status 1",
	},
	{
		`old="$PWD"; mkdir a; cd a; [[ $old == "$OLDPWD" ]]`,
		"",
	},
	{
		`old="$PWD"; mkdir a; mkdir parent parent/d; CDPATH=parent; cd d >/dev/null; [[ "$PWD" = "$old/parent/d" ]]; echo path:$?; [[ "$OLDPWD" = "$old" ]]; echo old:$?`,
		"path:0\nold:0\n",
	},
	{
		`old="$PWD"; mkdir d; CDPATH=.; cd d; [[ "$PWD" = "$old/d" ]]; echo path:$?`,
		"path:0\n",
	},
	{
		`mkdir a; ln -s a b; [[ $(cd a && pwd) == "$(cd b && pwd)" ]]; echo $?`,
		"1\n",
	},
	{
		`pwd -a`,
		"pwd: -a: invalid option\nexit status 2 #JUSTERR",
	},
	{
		`pwd -L -P -a`,
		"pwd: -a: invalid option\nexit status 2 #JUSTERR",
	},
	{
		`mkdir a; ln -s a b; [[ "$(cd a && pwd -P)" == "$(cd b && pwd -P)" ]]`,
		"",
	},
	{
		`mkdir a; ln -s a b; [[ "$(cd a && pwd -P)" == "$(cd b && pwd -L)" ]]; echo $?`,
		"1\n",
	},
	{
		`orig="$PWD"; mkdir a; cd a; cd - >/dev/null; [[ "$PWD" == "$orig" ]]`,
		"",
	},
	{
		`orig="$PWD"; mkdir a; cd a; [[ $(cd -) == "$orig" ]]`,
		"",
	},
	{
		`OLDPWD=/tmp/bashy-does-not-exist; cd -; echo status:$?`,
		"cd: /tmp/bashy-does-not-exist: No such file or directory\nstatus:1\n #JUSTERR",
	},
	{
		`readonly PWD; cd /; echo status:$?`,
		"PWD: readonly variable\nstatus:1\n #JUSTERR",
	},
	{
		`readonly OLDPWD; cd /; echo status:$?`,
		"OLDPWD: readonly variable\nstatus:1\n #JUSTERR",
	},

	// dirs/pushd/popd
	{"set -- $(dirs); echo $# ${#DIRSTACK[@]}", "1 1\n"},
	{"dirs -c; set -- $(dirs); echo $# ${#DIRSTACK[@]}", "1 1\n"},
	{"pushd", "pushd: no other directory\nexit status 1 #JUSTERR"},
	{"pushd -n", ""},
	{"pushd foo bar", "pushd: too many arguments\nexit status 2 #JUSTERR"},
	{"pushd does-not-exist; set -- $(dirs); echo $#", "pushd: does-not-exist: No such file or directory\n1\n #IGNORE"},
	{"mkdir a; pushd a >/dev/null; set -- $(dirs); echo $#", "2\n"},
	{"mkdir a; set -- $(pushd a); echo $#", "2\n"},
	{
		`mkdir a; pushd a >/dev/null; set -- $(dirs); [[ $1 == "$HOME" ]]`,
		"exit status 1",
	},
	{
		`mkdir a; pushd a >/dev/null; [[ ${DIRSTACK[0]} == "$HOME" ]]`,
		"exit status 1",
	},
	{
		`old=$(dirs); mkdir a; pushd a >/dev/null; pushd >/dev/null; set -- $(dirs); [[ $1 == "$old" ]]`,
		"",
	},
	{
		`old=$(dirs); mkdir a; pushd a >/dev/null; pushd -n >/dev/null; set -- $(dirs); [[ $1 == "$old" ]]`,
		"exit status 1",
	},
	{
		"mkdir a; pushd a >/dev/null; pushd >/dev/null; rm -r a; pushd",
		"pushd: ABS_PATH_A: No such file or directory\nexit status 1 #JUSTERR",
	},
	{
		`old=$(dirs); mkdir a; pushd -n a >/dev/null; set -- $(dirs); [[ $1 == "$old" ]]`,
		"",
	},
	{
		`old=$(dirs); mkdir a; pushd -n a >/dev/null; pushd >/dev/null; set -- $(dirs); [[ $1 == "$old" ]]`,
		"exit status 1",
	},
	{"popd", "popd: directory stack empty\nexit status 1 #JUSTERR"},
	{"popd -n", "popd: directory stack empty\nexit status 1 #JUSTERR"},
	{"popd foo", "popd: foo: invalid argument\npopd: usage: popd [-n] [+N | -N]\nexit status 2 #JUSTERR"},
	{"old=$(dirs); mkdir a; pushd a >/dev/null; set -- $(popd); echo $#", "1\n"},
	{
		`old=$(dirs); mkdir a; pushd a >/dev/null; popd >/dev/null; [[ $(dirs) == "$old" ]]`,
		"",
	},
	{"old=$(dirs); mkdir a; pushd a >/dev/null; set -- $(popd -n); echo $#", "1\n"},
	{
		`old=$(dirs); mkdir a; pushd a >/dev/null; popd -n >/dev/null; [[ $(dirs) == "$old" ]]`,
		"exit status 1",
	},
	{
		`root=$PWD; mkdir a b; pushd "$root/a" >/dev/null; pushd "$root/b" >/dev/null; pushd +1 >/dev/null; [[ ${DIRSTACK[0]} == */a ]]`,
		"",
	},
	{
		`root=$PWD; mkdir a b c; pushd "$root/a" >/dev/null; pushd "$root/b" >/dev/null; pushd "$root/c" >/dev/null; popd +2 >/dev/null; [[ ${DIRSTACK[0]} == */c && ${DIRSTACK[1]} == */b && ${DIRSTACK[2]} == "$root" ]]`,
		"",
	},
	{
		`root=$PWD; mkdir a b; pushd "$root/a" >/dev/null; pushd "$root/b" >/dev/null; DIRSTACK[1]=$root; [[ $(dirs) == "$root/b $root $root" ]]`,
		"",
	},
	{
		"mkdir a; pushd a >/dev/null; pushd >/dev/null; rm -r a; popd",
		"popd: ABS_PATH_A: No such file or directory\nexit status 1 #JUSTERR",
	},

	// binary cmd
	{
		"true && echo foo || echo bar",
		"foo\n",
	},
	{
		"false && echo foo || echo bar",
		"bar\n",
	},

	// func
	{
		"foo() { echo bar; }; foo",
		"bar\n",
	},
	{
		"foo() { echo $1; }; foo",
		"\n",
	},
	{
		"foo() { echo $1; }; foo a b",
		"a\n",
	},
	{
		"foo() { echo $1; bar c d; echo $2; }; bar() { echo $2; }; foo a b",
		"a\nd\nb\n",
	},
	{
		`foo() { echo $#; }; foo; foo 1 2 3; foo "a b"; echo $#`,
		"0\n3\n1\n0\n",
	},
	{
		`foo() { echo "<${*-x}> <${@-x}>"; }; foo; foo ""; foo "" ""`,
		"<x> <x>\n<> <>\n< > < >\n",
	},
	{
		`foo() { echo "${!@}-${!*}"; }; foo`,
		"-\n",
	},
	{
		`foo() { for a in $*; do echo "$a"; done }; foo 'a  1' 'b  2'`,
		"a\n1\nb\n2\n",
	},
	{
		`foo() { for a in "$*"; do echo "$a"; done }; foo 'a  1' 'b  2'`,
		"a  1 b  2\n",
	},
	{
		`foo() { for a in "foo$*"; do echo "$a"; done }; foo 'a  1' 'b  2'`,
		"fooa  1 b  2\n",
	},
	{
		`foo() { for a in $@; do echo "$a"; done }; foo 'a  1' 'b  2'`,
		"a\n1\nb\n2\n",
	},
	{
		`foo() { for a in "$@"; do echo "$a"; done }; foo 'a  1' 'b  2'`,
		"a  1\nb  2\n",
	},

	// alias (note the input newlines)
	{
		"alias foo; alias foo=echo; alias foo; alias foo=; alias foo",
		"alias: foo: not found\nalias foo='echo'\nalias foo=''\n #IGNORE",
	},
	{
		"shopt -s expand_aliases; alias foo=echo\nfoo foo; foo bar",
		"foo\nbar\n",
	},
	{
		"shopt -s expand_aliases; alias true=echo\ntrue foo; unalias true\ntrue bar",
		"foo\n",
	},
	{
		"shopt -s expand_aliases; alias echo='echo a'\necho b c",
		"a b c\n",
	},
	{
		"shopt -s expand_aliases; alias foo='echo '\nfoo foo; foo bar",
		"echo\nbar\n",
	},
	{
		"shopt -s expand_aliases; alias foo=\"echo 'Error:\"\neval \"foo bar'\"",
		"Error: bar\n",
	},
	{
		"shopt -s expand_aliases; alias comment=#\ncomment text after\necho ok",
		"ok\n",
	},

	// case
	{
		"case b in x) echo foo ;; a|b) echo bar ;; esac",
		"bar\n",
	},
	{
		"case b in x) echo foo ;; y|z) echo bar ;; esac",
		"",
	},
	{
		"case foo in bar) echo foo ;; *) echo bar ;; esac",
		"bar\n",
	},
	{
		"case foo in *o*) echo bar ;; esac",
		"bar\n",
	},
	{
		"case foo in '*') echo x ;; f*) echo y ;; esac",
		"y\n",
	},
	{
		"euro=$'\\342\\202\\254'; b=$'\\202'; case $euro in *$b*) echo bytematch ;; *) echo mbchar ;; esac",
		"bytematch\n",
	},

	// exec
	{
		"$GOSH_PROG 'echo foo'",
		"foo\n",
	},
	{
		"$GOSH_PROG 'echo foo >&2' >/dev/null",
		"foo\n",
	},
	{
		"echo foo | $GOSH_PROG 'cat >&2' >/dev/null",
		"foo\n",
	},
	{
		"$GOSH_PROG 'exit 1'",
		"exit status 1",
	},
	{
		"exec >/dev/null; echo foo",
		"",
	},

	// return
	{"return", "return: can only `return' from a function or sourced script\nexit status 1 #JUSTERR"},
	{"f() { return; }; f", ""},
	{"f() { return 2; }; f", "exit status 2"},
	{"f() { echo foo; return; echo bar; }; f", "foo\n"},
	{"f1() { :; }; f2() { f1; return; }; f2", ""},
	{"echo 'return' >a; source ./a", ""},
	{"echo 'return' >a; source ./a; return", "return: can only `return' from a function or sourced script\nexit status 1 #JUSTERR"},
	{"echo 'return 2' >a; source ./a", "exit status 2"},
	{"echo 'echo foo; return; echo bar' >a; source ./a", "foo\n"},

	// command
	{"command", ""},
	{"command -o echo", "command: invalid option \"-o\"\nexit status 2 #JUSTERR"},
	{"command -vo echo", "command: invalid option \"-o\"\nexit status 2 #JUSTERR"},
	{"echo() { :; }; echo foo", ""},
	{"echo() { :; }; command echo foo", "foo\n"},
	{"command -v does-not-exist", "exit status 1"},
	{"foo() { :; }; command -v foo", "foo\n"},
	{"foo() { :; }; command -v does-not-exist foo", "foo\n"},
	{"command -v echo", "echo\n"},
	{"[[ $(command -v $PATH_PROG) == $PATH_PROG ]]", "exit status 1"},

	// cmd substitution
	{
		"echo foo $(printf bar)",
		"foo bar\n",
	},
	{
		"echo foo $(echo bar)",
		"foo bar\n",
	},
	{
		"$(echo echo foo bar)",
		"foo bar\n",
	},
	{
		"for i in 1 $(echo 2 3) 4; do echo $i; done",
		"1\n2\n3\n4\n",
	},
	{
		"echo 1$(echo 2 3)4",
		"12 34\n",
	},
	{
		`mkdir d; [[ $(cd d && pwd) == "$(pwd)" ]]`,
		"exit status 1",
	},
	{
		"a=sub true & { a=main $ENV_PROG | grep '^a='; }",
		"a=main\n",
	},
	{
		"echo foo >f; echo $(cat f); echo $(<f)",
		"foo\nfoo\n",
	},
	{
		"echo foo >f; echo $(<f*)",
		"foo\n",
	},
	{
		"echo foo >f; echo $(<f; echo bar)",
		"bar\n",
	},
	{
		"$(false); echo $?; $(exit 3); echo $?; $(true); echo $?",
		"1\n3\n0\n",
	},
	{
		"foo=$(false); echo $?; echo foo $(false); echo $?",
		"1\nfoo\n0\n",
	},
	{
		"$(false) $(true); echo $?; $(true) $(false); echo $?",
		"0\n1\n",
	},
	{
		"foo=$(false) $(true); echo $?; foo=$(true) $(false); echo $?",
		"1\n0\n",
	},

	// pipes
	{
		"echo foo | sed 's/o/a/g'",
		"faa\n",
	},
	{
		"echo foo | false | true",
		"",
	},
	{
		"true $(true) | true", // used to panic
		"",
	},
	{
		// The first command in the block used to consume stdin, even
		// though it shouldn't be. We just want to run any arbitrary
		// non-builtin program that doesn't consume stdin.
		"echo foo | { $ENV_PROG >/dev/null; cat; }",
		"foo\n",
	},

	// redirects
	{
		"echo foo >&1 | sed 's/o/a/g'",
		"faa\n",
	},
	{
		"echo foo >&2 | sed 's/o/a/g'",
		"foo\n",
	},
	{
		// TODO: why does bash need a block here?
		"{ echo foo >&2; } |& sed 's/o/a/g'",
		"faa\n",
	},
	{
		"echo foo >/dev/null; echo bar",
		"bar\n",
	},
	{
		">a; echo foo >>b; wc -c <a >>b; cat b | tr -d ' '",
		"foo\n0\n",
	},
	{
		"echo foo >a; <a",
		"",
	},
	{
		"echo foo >a; mkdir b; cd b; cat <../a",
		"foo\n",
	},
	{
		"echo foo >a; wc -c <a | tr -d ' '",
		"4\n",
	},
	{
		"echo foo >>a; echo bar &>>a; wc -c <a | tr -d ' '",
		"8\n",
	},
	{
		"{ echo a; echo b >&2; } &>/dev/null",
		"",
	},
	{
		"exec 3>&1 4>&2; exec >&a; echo out; echo err >&2; exec 1>&3 2>&4; cat a",
		"out\nerr\n",
	},
	{
		// >| force-overwrite; equivalent to > when noclobber is unset.
		"echo foo >| a; cat a",
		"foo\n",
	},
	{
		// >| overwrites an existing file.
		"echo foo >a; echo bar >| a; cat a",
		"bar\n",
	},
	{
		"echo foo >a; set -C; echo bar >a; cat a",
		"a: cannot overwrite existing file\nfoo\n",
	},
	{
		"echo foo >a; set -C; echo bar >| a; cat a",
		"bar\n",
	},
	{
		": 2>/dev/null <$((foo+=42)); echo $foo",
		"42\n",
	},
	{
		"echo foo >a; exec 3<a; echo bad 2>/dev/null >&3; echo ok",
		"ok\n",
	},
	{
		// <> opens for read-write; the file must be readable as stdin.
		"echo foo >a; cat <>a",
		"foo\n",
	},
	{
		// <> creates the target file if it does not exist.
		"cat <>missing; ls missing",
		"missing\n",
	},
	{
		"sed 's/o/a/g' <<EOF\nfoo$foo\nEOF",
		"faa\n",
	},
	{
		"sed 's/o/a/g' <<'EOF'\nfoo$foo\nEOF",
		"faa$faa\n",
	},
	{
		"sed 's/o/a/g' <<EOF\n\tfoo\nEOF",
		"\tfaa\n",
	},
	{
		"sed 's/o/a/g' <<EOF\nfoo\nEOF",
		"faa\n",
	},
	{
		"cat <<EOF\n~/foo\nEOF",
		"~/foo\n",
	},
	{
		"sed 's/o/a/g' <<<foo$foo",
		"faa\n",
	},
	{
		"cat <<-EOF\n\tfoo\nEOF",
		"foo\n",
	},
	// Empty heredoc delimiter — terminated by an empty line. Both
	// quoted forms (`<<''` and `<<""`) and the tab-stripping `<<-''`
	// variant should round-trip body content.
	{"cat <<''\nhi\n\n", "hi\n"},
	{"cat <<\"\"\nhi\n\n", "hi\n"},
	{"cat <<-''\n\tindented\n\n", "indented\n"},
	{
		"cat <<-EOF\n\tfoo\n\nEOF",
		"foo\n\n",
	},
	{
		"cat <<EOF\nfoo\\\nbar\nEOF",
		"foobar\n",
	},
	{
		"cat <<'EOF'\nfoo\\\nbar\nEOF",
		"foo\\\nbar\n",
	},
	{
		"cat <<EOF\nfoo\\\"bar\\baz\nEOF",
		"foo\\\"bar\\baz\n",
	},
	{
		"cat <<EOF\n \\\\ \\$ \\` \nEOF",
		" \\ $ ` \n",
	},
	{
		"cat <<'PY'\nhe said “smart” quotes\nPY",
		"he said “smart” quotes\n",
	},
	{
		"cat <<PY\nhe said “smart” quotes\nPY",
		"he said “smart” quotes\n",
	},
	{
		"cat <<'PY'\nprint(f“key: {v}”)\nPY",
		"print(f“key: {v}”)\n",
	},
	{
		"mkdir a; echo foo >a |& grep -q 'is a directory'",
		" #IGNORE bash prints a warning",
	},
	{
		"echo foo 1>&1 | sed 's/o/a/g'",
		"faa\n",
	},
	{
		"echo foo 2>&2 |& sed 's/o/a/g'",
		"faa\n",
	},
	{
		"printf 2>&1 | sed 's/.*usage.*/foo/'",
		"foo\n",
	},
	{
		"mkdir a && cd a && echo foo >b && cd .. && cat a/b",
		"foo\n",
	},
	{
		"echo foo 2>&-; :",
		"foo\n",
	},
	{
		// `>&-` closes stdout or stderr. Note that any writes result in errors.
		"echo foo >&- 2>&-; :",
		"",
	},
	{
		"echo foo | sed $(read line 2>/dev/null; echo 's/o/a/g')",
		"",
	},
	{
		// `<&-` closes stdin, to e.g. ensure that a subshell does not consume
		// the standard input shared with the parent shell.
		// Note that any reads result in errors.
		"echo foo | sed $(exec <&-; read line 2>/dev/null; echo 's/o/a/g')",
		"faa\n",
	},
	{
		// Concurrent pipe commands used to cause races when modifying the environment.
		"a=1 b=2 c=3 d=4 e=5 : | a=1 b=2 c=3 d=4 e=5 : | a=1 b=2 c=3 d=4 e=5 : | a=1 b=2 c=3 d=4 e=5 :",
		"",
	},

	// background/wait
	{"wait", ""},
	{"wait foo", "wait: pid foo is not a child of this shell\nexit status 1 #JUSTERR"},

	// disown — no-op (no job table to remove from, no SIGHUP to dodge)
	{"disown", ""},
	{"disown -a", ""},
	{"disown -h -r", ""},
	{"disown 12345", ""},
	{"disown %1", ""},
	{"true & disown; echo done", "done\n"},
	{"set -e; disown -a; echo ok", "ok\n"},
	{"disown -z", "disown: invalid option \"-z\"\nexit status 2 #JUSTERR"},

	// kill — argv parsing / -l listing / error paths
	{"kill", "kill: usage: kill [-s sigspec | -n signum | -sigspec] pid | jobspec ... or kill -l [sigspec]\nexit status 2 #JUSTERR"},
	{"kill foo", "kill: `foo': not a pid or valid job spec\nexit status 1 #JUSTERR"},
	{"kill %1", "kill: %1: no job control in this shell\nexit status 1 #JUSTERR"},
	{"kill -s NOSIG 1", "kill: NOSIG: invalid signal specification\nexit status 1 #JUSTERR"},
	{"kill -NOSIG 1", "kill: NOSIG: invalid signal specification\nexit status 1 #JUSTERR"},
	{"kill -s", "kill: -s: option requires an argument\nexit status 2 #JUSTERR"},
	{"kill -INT ''", "kill: `': not a pid or valid job spec\nexit status 1 #JUSTERR"},
	{"kill -INT", "kill: usage: kill [-s sigspec | -n signum | -sigspec] pid | jobspec ... or kill -l [sigspec]\nexit status 2 #JUSTERR"},
	{"kill -l BAD", "kill: BAD: invalid signal specification\nexit status 1 #JUSTERR"},
	{"kill -HUP @12", "kill: `@12': not a pid or valid job spec\nexit status 1 #JUSTERR"},
	{"kill -l TERM", "15\n"},
	{"kill -l SIGTERM", "15\n"},
	{"kill -l 15", "TERM\n"},
	{"kill -l 129", "HUP\n"},
	{"kill -l | head -1", " 1) SIGHUP        2) SIGINT        3) SIGQUIT       4) SIGILL        5) SIGTRAP      \n"},
	{"diff <(kill -l) <(trap -l)", ""},
	{"kill -l KILL INT", "9\n2\n"},

	// setsid / nohup — usage / lookup errors. Real-subprocess delivery is
	// covered in builtin_proc_test.go (unix-only).
	{"setsid", "setsid: usage: setsid [-f] [-w] [-c] <program> [args...]\nexit status 2 #JUSTERR"},
	{"setsid -z foo", "setsid: invalid option: \"-z\"\nexit status 2 #JUSTERR"},
	{"setsid nonexistent-binary-xyz", "setsid: \"nonexistent-binary-xyz\": executable file not found in $PATH\nexit status 127 #JUSTERR"},
	{"nohup", "nohup: usage: nohup <program> [args...]\nexit status 125 #JUSTERR"},
	{"nohup nonexistent-binary-xyz", "nohup: \"nonexistent-binary-xyz\": executable file not found in $PATH\nexit status 127 #JUSTERR"},

	{"{ true; } & wait", ""},
	{"{ false; } & wait", ""},
	{"{ sleep 0.01; true; } & wait", ""},
	{"{ sleep 0.01; false; } & wait", ""},
	{
		"{ echo foo; } & wait; echo bar",
		"foo\nbar\n",
	},
	{
		"{ echo foo & wait; } & wait; echo bar",
		"foo\nbar\n",
	},
	{`mkdir d; old=$PWD; cd d & wait; [[ $old == "$PWD" ]]`, ""},
	{
		"f() { echo 1; }; { sleep 0.01; f; } & f() { echo 2; }; wait",
		"1\n",
	},
	{"[[ -n $! ]]", "exit status 1"},
	{"true & [[ -n $! ]]", ""},
	{"true & true;  [[ -n $! ]]", ""},
	{"true & pid=$!; wait $pid", ""},
	{"false & pid=$!; wait $pid", "exit status 1"},
	{"{ sleep 0.01; true; } & pid=$!; wait $pid", ""},
	{"{ sleep 0.01; false; } & pid=$!; wait $pid", "exit status 1"},
	{"(true) & ok=$!; (false) & fail=$!; wait $ok $fail", "exit status 1"},
	{"(true) & ok=$!; (false) & ignore=$!; wait $ok", ""},
	{"echo foo | true | false & wait $!", "exit status 1"},
	{"echo foo | false | true & wait $!", ""},
	{"f() { false & true; }; f; wait $!", "exit status 1"},
	// The parent and child shells should not cause data races when setting env vars.
	// Note that we can't use `echo $var`, as it seems to write newlines separately,
	// which can cause them to get mixed up between concurrent subshells.
	{
		"{ for n in {0..9}; do { echo -n $n$'\n'; } & done; wait; } | sort",
		"0\n1\n2\n3\n4\n5\n6\n7\n8\n9\n",
	},
	{
		"outer=val; for n in {0..9}; do { echo -n $outer$'\n'; } & outer=val; done; wait",
		"val\nval\nval\nval\nval\nval\nval\nval\nval\nval\n",
	},
	{
		"for n in {0..9}; do { inner=val; } & echo $inner; done",
		"\n\n\n\n\n\n\n\n\n\n",
	},
	{
		"exit 2 & bg1=$!; exit 0 & bg2=$!; wait $bg1 $bg2; echo $?",
		"0\n",
	},
	{
		"exit 2 & bg1=$!; exit 4 & bg2=$!; wait $bg1 $bg2; echo $?",
		"4\n",
	},

	// bash test
	{
		"[[ a ]]",
		"",
	},
	{
		"[[ '' ]]",
		"exit status 1",
	},
	{
		"[[ '' ]]; [[ a ]]",
		"",
	},
	{
		"[[ ! (a == b) ]]",
		"",
	},
	{
		"[[ a != b ]]",
		"",
	},
	{
		"[[ a && '' ]]",
		"exit status 1",
	},
	{
		"[[ a || '' ]]",
		"",
	},
	{
		"[[ a > 3 ]]",
		"",
	},
	{
		"[[ a < 3 ]]",
		"exit status 1",
	},
	{
		"[[ 3 == 03 ]]",
		"exit status 1",
	},
	{
		"[[ a -eq b ]]",
		"",
	},
	{
		"[[ 3 -eq 03 ]]",
		"",
	},
	{
		"[[ 3 -ne 4 ]]",
		"",
	},
	{
		"[[ 3 -le 4 ]]",
		"",
	},
	{
		"[[ 3 -ge 4 ]]",
		"exit status 1",
	},
	{
		"[[ 3 -ge 3 ]]",
		"",
	},
	{
		"[[ 3 -lt 4 ]]",
		"",
	},
	{
		"[[ ' 3' -lt '4 ' ]]",
		"",
	},
	{
		"[[ 3 -gt 4 ]]",
		"exit status 1",
	},
	{
		"[[ 3 -gt 3 ]]",
		"exit status 1",
	},
	{
		"[[ a -nt a || a -ot a ]]",
		"exit status 1",
	},
	{
		"touch -t 202111050000.30 a b; [[ a -nt b || a -ot b ]]",
		"exit status 1",
	},
	{
		"touch -t 202111050200.00 a; touch -t 202111060100.00 b; [[ a -nt b ]]",
		"exit status 1",
	},
	{
		"touch -t 202111050000.00 a; touch -t 202111060000.00 b; [[ a -ot b ]]",
		"",
	},
	{
		"[[ a -ef b ]]",
		"exit status 1",
	},
	{
		">a >b; [[ a -ef b ]]",
		"exit status 1",
	},
	{
		">a; [[ a -ef a ]]",
		"",
	},
	{
		">a; ln a b; [[ a -ef b ]]",
		"",
	},
	{
		">a; ln -s a b; [[ a -ef b ]]",
		"",
	},
	{
		"[[ -z 'foo' || -n '' ]]",
		"exit status 1",
	},
	{
		"[[ -z '' && -n 'foo' ]]",
		"",
	},
	{
		"a=x b=''; [[ -v a && -v b && ! -v c ]]",
		"",
	},
	{
		"[[ abc == *b* ]]",
		"",
	},
	{
		"[[ abc != *b* ]]",
		"exit status 1",
	},
	{
		"[[ *b = '*b' ]]",
		"",
	},
	{
		"[[ ab == a. ]]",
		"exit status 1",
	},
	{
		`x='*b*'; [[ abc == $x ]]`,
		"",
	},
	{
		`x='*b*'; [[ abc == "$x" ]]`,
		"exit status 1",
	},
	{
		`[[ abc == \a\bc ]]`,
		"",
	},
	{
		"[[ abc != *b'*' ]]",
		"",
	},
	{
		"[[ a =~ b ]]",
		"exit status 1",
	},
	{
		"[[ foo =~ foo && foo =~ .* && foo =~ f.o ]]",
		"",
	},
	{
		"[[ foo =~ oo ]] && echo foo; [[ foo =~ ^oo$ ]] && echo bar || true",
		"foo\n",
	},
	{
		`[[ x =~ \x ]]; echo $?; [[ a-b =~ a\-b ]]; echo $?; c=$'\001'; [[ $c =~ \$c ]]; echo $?`,
		"0\n0\n1\n",
	},
	{
		`[[ dog =~ [[=d=]].. ]] && echo ok1; [[ dog =~ [[.d.][.D.]]o. ]] && echo ok2; [[ dog =~ ([[.d.][.D.]])o(.) ]] && echo "${BASH_REMATCH[1]} ${BASH_REMATCH[2]}"`,
		"ok1\nok2\nd g\n",
	},
	{
		`[[ ']' =~ [']'] ]] && echo rb; [[ a =~ ['a]'] ]] || echo no; [[ a] =~ ['a]'] ]] && echo lit`,
		"rb\nno\nlit\n",
	},
	{
		"[[ a =~ [ ]]",
		"[[: error parsing regexp: missing closing ]: `[`\nexit status 2 #JUSTERR",
	},
	{
		"[[ a__b__c =~ _*(b_*) ]]; echo ${BASH_REMATCH[0]}; echo ${BASH_REMATCH[1]}",
		"__b__\nb__\n",
	},
	{
		"[[ -e a ]] && echo x; >a; [[ -e a ]] && echo y",
		"y\n",
	},
	{
		"ln -s b a; [[ -e a ]] && echo x; >b; [[ -e a ]] && echo y",
		"y\n",
	},
	{
		"[[ -f a ]] && echo x; >a; [[ -f a ]] && echo y",
		"y\n",
	},
	{
		"[[ -e a ]] && echo x; mkdir a; [[ -e a ]] && echo y",
		"y\n",
	},
	{
		"[[ -d a ]] && echo x; mkdir a; [[ -d a ]] && echo y",
		"y\n",
	},
	{
		"[[ -r a ]] && echo x; >a; [[ -r a ]] && echo y",
		"y\n",
	},
	{
		"[[ -w a ]] && echo x; >a; [[ -w a ]] && echo y",
		"y\n",
	},
	{
		"[[ -s a ]] && echo x; echo body >a; [[ -s a ]] && echo y",
		"y\n",
	},
	{
		"[[ -L a ]] && echo x; ln -s b a; [[ -L a ]] && echo y;",
		"y\n",
	},
	{
		"[[ \"multiline\ntext\" == *text* ]] && echo x; [[ \"multiline\ntext\" == *multiline* ]] && echo y",
		"x\ny\n",
	},
	// * should match a newline
	{
		"[[ \"multiline\ntext\" == multiline*text ]] && echo x",
		"x\n",
	},
	{
		"[[ \"multiline\ntext\" == text ]]",
		"exit status 1",
	},
	{
		`case $'a\nb' in a*b) echo match ;; esac`,
		"match\n",
	},
	{
		`a=$'a\nb'; echo "${a/a*b/sub}"`,
		"sub\n",
	},
	{
		"mkdir a; cd a; test -f b && echo x; >b; test -f b && echo y",
		"y\n",
	},
	{
		">a; [[ -b a ]] && echo block; [[ -c a ]] && echo char; true",
		"",
	},
	{
		"[[ -e /dev/sda ]] || { echo block; exit; }; [[ -b /dev/sda ]] && echo block; [[ -c /dev/sda ]] && echo char; true",
		"block\n",
	},
	{
		"[[ -e /dev/nvme0n1 ]] || { echo block; exit; }; [[ -b /dev/nvme0n1 ]] && echo block; [[ -c /dev/nvme0n1 ]] && echo char; true",
		"block\n",
	},
	{
		"[[ -e /dev/tty ]] || { echo char; exit; }; [[ -b /dev/tty ]] && echo block; [[ -c /dev/tty ]] && echo char; true",
		"char\n",
	},
	{"[[ -t 1 ]]", "exit status 1"},
	{"[[ -t 1234 ]]", "exit status 1"},
	{"[[ -o wrong ]]", "exit status 1"},
	{"[[ -o errexit ]]", "exit status 1"},
	{"set -e; [[ -o errexit ]]", ""},
	{"[[ -o noglob ]]", "exit status 1"},
	{"set -f; [[ -o noglob ]]", ""},
	{"[[ -o allexport ]]", "exit status 1"},
	{"set -a; [[ -o allexport ]]", ""},
	{"[[ -o nounset ]]", "exit status 1"},
	{"set -u; [[ -o nounset ]]", ""},
	{"[[ -o noexec ]]", "exit status 1"},
	{"set -n; [[ -o noexec ]]", ""}, // actually does nothing, but oh well
	{"[[ -o pipefail ]]", "exit status 1"},
	{"set -o pipefail; [[ -o pipefail ]]", ""},
	// TODO: we don't implement precedence of && over ||.
	// {"[[ a == x && b == x || c == c ]]", ""},
	{"[[ (a == x && b == x) || c == c ]]", ""},
	{"[[ a == x && (b == x || c == c) ]]", "exit status 1"},

	// classic test
	{
		"[",
		"[: missing `]'\nexit status 2 #JUSTERR",
	},
	{
		"[ a",
		"[: missing `]'\nexit status 2 #JUSTERR",
	},
	{
		"[ a b c ]",
		"[: b: binary operator expected\nexit status 2 #JUSTERR",
	},
	{
		"[ a -a ]",
		"[: argument expected\nexit status 2 #JUSTERR",
	},
	{"[ a ]", ""},
	{"[ -n ]", ""},
	{"[ '-n' ]", ""},
	{"[ -z ]", ""},
	{"[ ! ]", ""},
	{"[ a != b ]", ""},
	{"[ ! a '==' a ]", "exit status 1"},
	{"[ a -a 0 -gt 1 ]", "exit status 1"},
	{"[ 0 -gt 1 -o 1 -gt 0 ]", ""},
	{"[ 3 -gt 4 ]", "exit status 1"},
	{"[ 3 -lt 4 ]", ""},
	{"[ ' 3' -lt '4 ' ]", ""},
	{
		"[ -e a ] && echo x; >a; [ -e a ] && echo y",
		"y\n",
	},
	{
		"test 3 -gt 4",
		"exit status 1",
	},
	{
		"test 3 -lt 4",
		"",
	},
	{
		"test 3 -lt",
		"test: syntax error: `-lt' unexpected\nexit status 2 #JUSTERR",
	},
	{
		"touch -t 202111050000.00 a; touch -t 202111060000.00 b; [ a -nt b ]",
		"exit status 1",
	},
	{
		"touch -t 202111050000.00 a; touch -t 202111060000.00 b; [ a -ot b ]",
		"",
	},
	{
		">a; [ a -ef a ]",
		"",
	},
	{"[ 3 -eq 04 ]", "exit status 1"},
	{"[ 3 -eq 03 ]", ""},
	{"[ 3 -ne 03 ]", "exit status 1"},
	{"[ 3 -le 4 ]", ""},
	{"[ 3 -ge 4 ]", "exit status 1"},
	{
		"[ -d a ] && echo x; mkdir a; [ -d a ] && echo y",
		"y\n",
	},
	{
		"[ -r a ] && echo x; >a; [ -r a ] && echo y",
		"y\n",
	},
	{
		"[ -w a ] && echo x; >a; [ -w a ] && echo y",
		"y\n",
	},
	{
		// A directory is readable, writable, and executable.
		"mkdir d; [ -r d ] && echo r; [ -w d ] && echo w; [ -x d ] && echo x",
		"r\nw\nx\n",
	},
	{
		"test -N a",
		"exit status 1",
	},
	{
		"test -? a",
		// TODO: this error message should refer to `-?`
		"test: -?: unary operator expected\nexit status 2 #JUSTERR",
	},
	{
		"[ -s a ] && echo x; echo body >a; [ -s a ] && echo y",
		"y\n",
	},
	{
		"[ -L a ] && echo x; ln -s b a; [ -L a ] && echo y;",
		"y\n",
	},
	{
		">a; [ -b a ] && echo block; [ -c a ] && echo char; true",
		"",
	},
	{"[ -t 1 ]", "exit status 1"},
	{"[ -t 1234 ]", "exit status 1"},
	{"[ -o wrong ]", "exit status 1"},
	{"[ -o errexit ]", "exit status 1"},
	{"set -e; [ -o errexit ]", ""},
	{"a=x b=''; [ -v a -a -v b -a ! -v c ]", ""},
	{"[ a = a ]", ""},
	{"[ a != a ]", "exit status 1"},
	{"[ abc = ab* ]", "exit status 1"},
	{"[ abc != ab* ]", ""},
	// TODO: we don't implement precedence of -a over -o.
	// {"[ a = x -a b = x -o c = c ]", ""},
	{`[ \( a = x -a b = x \) -o c = c ]`, ""},
	{`[ a = x -a \( b = x -o c = c \) ]`, "exit status 1"},

	// arithm
	{
		"echo $((1 == +1))",
		"1\n",
	},
	{"echo $((4 ? 1 : 0))", "1\n"},
	{"A='3 + 5'; echo $((4 ? : $A)); echo after", "expression expected\nafter\n"},
	{"echo $((1 ? 20)); echo after", "`:' expected for conditional expression\nafter\n"},
	{"echo $((4 ? 20 :)); echo after", "expression expected\nafter\n"},
	{"echo $((2**-1)); echo after", "exponent less than 0\nafter\n"},
	{"v=-9223372036854775808; echo $((v)); echo $((v / -1)); echo $((v * -1)); echo $((-v))", "-9223372036854775808\n-9223372036854775808\n-9223372036854775808\n-9223372036854775808\n"},
	{"A='4 + '; echo $(((4 + A) + 4)); echo after", "arithmetic syntax error: operand expected (error token is \"+ \")\nafter\n"},
	{"echo $((++7)); echo $((--7))", "7\n7\n"},
	{"((++)); echo $?", "arithmetic syntax error: operand expected (error token is \"+ \")\n1\n"},
	{"echo $((+++7)); echo $((++ + 7)); echo $((---7)); echo $((-- - 7))", "7\n7\n-7\n-7\n"},
	{"a=1; echo $((4+++a)); echo $a; a=1; echo $((4---a)); echo $a", "6\n2\n4\n0\n"},
	{"readonly xx=5; echo $((xx=5)); echo $?", "xx: readonly variable\n1\n"},
	{"x=1; ((x=2, y=x)); echo $x $y", "2 2\n"},
	{"x=(456 123); (( x[1] < x && (x=x[1], x[1]=$x) )); echo ${x[@]}", "123 456\n"},
	{"x=(456 123); (( x[1] < x[0] && (x[0]=x[1], x[1]=$x) )); echo ${x[@]}", "123 456\n"},
	{"n=0; (( a[n]=++n )); echo $n ${a[@]}", "1 1\n"},
	{"n=0 a='(a[n]=++n)<7&&a[0]'; ((a[0])); echo ${a[@]:1}", "1 2 3 4 5 6 7\n"},
	{"set -u; echo $((a > 4)); echo after", "a: unbound variable\nexit status 1 #JUSTERR"},
	{"a=b b=a; echo $((a + 7)); echo after", "b: expression recursion level exceeded (error token is \"b\")\nafter\n"},
	{"x=8; echo $((--x++)); echo after", "++: assignment requires lvalue (error token is \"++ \")\nafter\n"},
	{"HOME=/usr/homes/chet; echo \"${HOME:`echo }`}\"; echo after", "arithmetic syntax error: operand expected (error token is \"}\")\nafter\n"},
	{"set -- a b c d op; echo ${!#}; v=bad-var; echo ${!v}; echo after", "op\nbad-var: invalid variable name\nafter\n"},
	{"set -- a 'b c' d; foo=@; printf '<%s>\\n' ${!foo}; printf 'Q<%s>\\n' \"${!foo}\"", "<a>\n<b>\n<c>\n<d>\nQ<a>\nQ<b c>\nQ<d>\n"},
	{"set -- a b c d e; echo ${6=arg6}; echo after", "$6: cannot assign in this way\nafter\n"},
	{"set -u; echo $9", "$9: unbound variable\nexit status 1 #JUSTERR"},
	{"set -u; echo ${9}", "9: unbound variable\nexit status 1 #JUSTERR"},
	{"v=abcde; echo ${v/#a/ab}; echo ${v/%?/last}; av=(abcd efgh); echo ${av[1]/#?/xx}; echo ${av[1]/%??/za}", "abbcde\nabcdlast\nxxfgh\nefza\n"},
	{"av=(abcd efgh ijkl); printf '<%s>\\n' ${av[@]/%??/xx}; set -- abcd efgh ijkl; printf 'P<%s>\\n' ${@/#??/za}", "<abxx>\n<efxx>\n<ijxx>\nP<zacd>\nP<zagh>\nP<zakl>\n"},
	{"_QUANTITY= _QUOTA= _QUOTE= _QUILL= _QUEST= _QUART=; IFS=-; printf '<%s>\\n' \"${!_Q*}\"; printf '<%s>\\n' \"${!_Q@}\"", "<_QUANTITY-_QUART-_QUEST-_QUILL-_QUOTA-_QUOTE>\n<_QUANTITY>\n<_QUART>\n<_QUEST>\n<_QUILL>\n<_QUOTA>\n<_QUOTE>\n"},
	{"_Q=1; echo \"${!_Q* }\"; echo after", "bad substitution\nafter\n"},
	{"set -- a b; echo ${!1*}; echo ${!@*}; echo after", "bad substitution\nbad substitution\nafter\n"},
	{"arrayA=(A B C); xx='arrayA[*]'; arrayB=( ${!xx} ); echo \"${#arrayB[*]}:${arrayB[0]}:${arrayB[1]}:${arrayB[2]}\"; arrayB=( \"${!xx}\" ); echo \"${#arrayB[*]}:${arrayB[0]}:${arrayB[1]}:${arrayB[2]}\"; xx='arrayA[@]'; arrayB=( ${!xx} ); echo \"${#arrayB[*]}:${arrayB[0]}:${arrayB[1]}:${arrayB[2]}\"; arrayB=( \"${!xx}\" ); echo \"${#arrayB[*]}:${arrayB[0]}:${arrayB[1]}:${arrayB[2]}\"", "3:A:B:C\n1:A B C::\n3:A:B:C\n3:A:B:C\n"},
	// Assignment binds lower than the ternary false branch in bash:
	// these parse like `(cond ? a : a) += 5`, which is not an lvalue.
	{"a=10; echo $((0 ? a : a+=5)); echo $a", "attempted assignment to non-variable\n10\n"},
	{"a=10; echo $((1 ? a*=2 : a+=5)); echo $a", "attempted assignment to non-variable\n10\n"},
	{"_ENV=oops; x=${_ENV[(_$-=0)+(_=1)-_${-%%*i*}]}; echo ${x:-unset}", "unset\n"},
	{
		"echo $((!0))",
		"1\n",
	},
	{
		"echo $((!3))",
		"0\n",
	},
	{
		"echo $((~0))",
		"-1\n",
	},
	{
		"echo $((~3))",
		"-4\n",
	},
	{
		"echo $((1 + 2 - 3))",
		"0\n",
	},
	{
		"echo $((-1 * 6 / 2))",
		"-3\n",
	},
	{
		"a=2; echo $(( a + $a + c ))",
		"4\n",
	},
	{
		"a=b; b=c; c=5; echo $((a % 3))",
		"2\n",
	},
	{
		"echo $((2 > 2 || 2 < 2))",
		"0\n",
	},
	{
		"echo $((2 >= 2 && 2 <= 2))",
		"1\n",
	},
	{
		"echo $(((1 & 2) != (1 | 2)))",
		"1\n",
	},
	{
		"echo $a; echo $((a = 3 ^ 2)); echo $a",
		"\n1\n1\n",
	},
	{
		"echo $((a += 1, a *= 2, a <<= 2, a >> 1))",
		"4\n",
	},
	{
		"echo $((a -= 10, a /= 2, a >>= 1, a << 1))",
		"-6\n",
	},
	{
		"echo $((a |= 3, a &= 1, a ^= 8, a %= 5, a))",
		"4\n",
	},
	{
		"echo $((a = 3, ++a, a--))",
		"4\n",
	},
	{
		"echo $((2 ** 3)) $((1234 ** 4567))",
		"8 0\n",
	},
	{
		"echo $((1 ? 2 : 3)) $((0 ? 2 : 3))",
		"2 3\n",
	},
	{
		"echo $((255+1))",
		"256\n",
	},
	{
		"echo $((0xff+1))",
		"256\n",
	},
	{
		"echo $((0377+1))",
		"256\n",
	},
	{
		"echo $((10#255+1))",
		"256\n",
	},
	{
		"echo $((16#ff+1))",
		"256\n",
	},
	{
		"echo $((2#11111111+1))",
		"256\n",
	},
	// TODO: Enable this test once integer bit widths are
	// handled in a consistent manner throughout the library.
	//{
	//	"echo $((16#badc0ffee+1))",
	//	"50159747055\n",
	//},
	{
		"echo $((16#cafe+1))",
		"51967\n",
	},
	{
		"echo $((nope+1))",
		"1\n", // Yes, this is what bash does.
	},
	{
		"((1))",
		"",
	},
	{
		"((3 == 4))",
		"exit status 1",
	},
	{
		"let i=(3+4); let i++; echo $i; let i--; echo $i",
		"8\n7\n",
	},
	{
		"let; echo $?",
		"let: expression expected\n1\n",
	},
	{
		"let 3==4",
		"exit status 1",
	},
	{
		"a=1; let a++; echo $a",
		"2\n",
	},
	{
		"a=$((1 + 2)); echo $a",
		"3\n",
	},
	{
		"x=3; echo $(($x)) $((x))",
		"3 3\n",
	},
	{
		"set -- 1; echo $(($@))",
		"1\n",
	},
	{
		"a=b b=a; echo $(($a))",
		"a: expression recursion level exceeded (error token is \"a\")\n",
	},
	{
		"let x=3; let 3/0; ((3/0)); echo $((x/y)); let x/=0",
		"division by zero\ndivision by zero\ndivision by zero\ndivision by zero\nexit status 1 #JUSTERR",
	},
	{
		"let x=3; let 3%0; ((3%0)); echo $((x%y)); let x%=0",
		"division by zero\ndivision by zero\ndivision by zero\ndivision by zero\nexit status 1 #JUSTERR",
	},
	{
		"let x=' 3'; echo $x",
		"3\n",
	},
	{
		"x=' 3'; let x++; echo \"$x\"",
		"4\n",
	},
	// let with multiple expressions: each evaluates left to right and
	// later ones can reference earlier ones. Exit code is from the last.
	{"let a=10 b=20 c=a+b; echo \"$a $b $c\"", "10 20 30\n"},
	{"let 1 0; echo $?", "1\n"}, // last expr (0) → exit 1
	{"let 0 1; echo $?", "0\n"}, // last expr (1) → exit 0

	// select loop: prints menu to stderr, reads reply from stdin
	// (which we feed via heredoc), sets var to items[N-1], REPLY to
	// the raw input, then runs the body. Empty/invalid input is
	// handled separately below.
	{
		"PS3='> '; select x in a b c; do echo \"x=$x R=$REPLY\"; break; done <<<2",
		"1) a\n2) b\n3) c\n> x=b R=2\n",
	},
	// Invalid reply (out-of-range number) sets var to empty, REPLY
	// to the raw input, body runs.
	{
		"PS3='> '; select x in a b c; do echo \"x=$x R=$REPLY\"; break; done <<<99",
		"1) a\n2) b\n3) c\n> x= R=99\n",
	},
	// EOF on stdin exits the loop with status 1.
	{
		"PS3='> '; select x in a b c; do echo body; done </dev/null; echo end=$?",
		"1) a\n2) b\n3) c\n> end=1\n",
	},

	// set/shift
	{
		"echo $#; set foo bar; echo $#",
		"0\n2\n",
	},
	{
		"shift; set a b c; shift; echo $@",
		"b c\n",
	},
	{
		"shift 2; set a b c; shift 2; echo $@",
		"c\n",
	},
	{
		`echo $#; set '' ""; echo $#`,
		"0\n2\n",
	},
	{
		"set -- a b; echo $#",
		"2\n",
	},
	{
		"set -U",
		"set: -U: invalid option\nset: usage: set [-abefhkmnptuvxBCEHPT] [-o option-name] [--] [-] [arg ...]\nexit status 2 #JUSTERR",
	},
	{
		"set -o trackall",
		"set: trackall: invalid option name\nexit status 2 #JUSTERR",
	},
	{
		"set -e; false; echo foo",
		"exit status 1",
	},
	{
		"set -e; shouldnotexist; echo foo",
		"\"shouldnotexist\": executable file not found in $PATH\nexit status 127 #JUSTERR",
	},
	{
		"set -e; set +e; false; echo foo",
		"foo\n",
	},
	{
		"set -e; ! false; echo foo",
		"foo\n",
	},
	{
		"set -e; ! true; echo foo",
		"foo\n",
	},
	{
		"set -e; if false; then echo never; fi; echo foo",
		"foo\n",
	},
	{
		"set -e; while false; do echo never; done; echo foo",
		"foo\n",
	},
	{
		"set -e; false || true; echo foo",
		"foo\n",
	},
	{
		"set -e; false && true; echo foo",
		"foo\n",
	},
	{
		"set -e; true && false; echo foo",
		"exit status 1",
	},
	{
		"false | :",
		"",
	},
	{
		// Important that we don't print in these, as otherwise we get "broken pipe" errors.
		"GOSH_CMD=exit_5 $GOSH_PROG | GOSH_CMD=exit_0 $GOSH_PROG",
		"",
	},
	{
		"set -o pipefail; false | :",
		"exit status 1",
	},
	{
		"set -o pipefail; GOSH_CMD=exit_5 $GOSH_PROG | GOSH_CMD=exit_0 $GOSH_PROG",
		"exit status 5",
	},
	{
		"set -o pipefail; true | false | true | :",
		"exit status 1",
	},
	{
		"set -o pipefail; set -M 2>/dev/null | false",
		"exit status 1",
	},
	{
		"set -o pipefail; false | :; echo next",
		"next\n",
	},
	{
		"set -o pipefail; exit 0 | :; echo next",
		"next\n",
	},
	{
		"set -o pipefail; exit 1 | :; echo next",
		"next\n",
	},
	{
		"set -e -o pipefail; false | :; echo next",
		"exit status 1",
	},
	{
		"exit 0 && true; echo foo",
		"",
	},
	{
		"exit 1 && true; echo foo",
		"exit status 1",
	},
	{
		"set -f; >a.x; echo *.x;",
		"*.x\n",
	},
	{
		"set -f; set +f; >a.x; echo *.x;",
		"a.x\n",
	},
	{
		"set -a; foo=bar; $ENV_PROG | grep ^foo=",
		"foo=bar\n",
	},
	{
		"set -a; foo=(b a r); $ENV_PROG | grep ^foo=",
		"exit status 1",
	},
	{
		"foo=bar; set -a; $ENV_PROG | grep ^foo=",
		"exit status 1",
	},
	{
		"a=b; echo $a; set -u; echo $a",
		"b\nb\n",
	},
	{
		"echo $a; set -u; echo $a; echo extra",
		"\na: unbound variable\nexit status 1 #JUSTERR",
	},
	{
		"foo=bar; set -u; echo ${foo/bar/}",
		"\n",
	},
	{
		"foo=bar; set -u; echo ${foo#bar}",
		"\n",
	},
	{
		"set -u; echo ${foo/bar/}",
		"foo: unbound variable\nexit status 1 #JUSTERR",
	},
	{
		"set -u; echo ${foo#bar}",
		"foo: unbound variable\nexit status 1 #JUSTERR",
	},
	// TODO: detect this case as unset
	// {
	// 	"set -u; foo=(bar); echo $foo; echo ${foo[3]}",
	// 	"bar\nfoo: unbound variable\nexit status 1 #JUSTERR",
	// },
	{
		"set -u; foo=(bar); echo ${foo[3]}",
		"foo[3]: unbound variable\nexit status 1 #JUSTERR",
	},
	{
		"set -u; echo ${narray[4]}",
		"narray[4]: unbound variable\nexit status 1 #JUSTERR",
	},
	{
		"set -u; echo ${narray[bar]}",
		"bar: unbound variable\nexit status 1 #JUSTERR",
	},
	{
		"set -u; foo=(''); echo ${foo[0]}",
		"\n",
	},
	{
		"set -u; echo ${#foo}",
		"foo: unbound variable\nexit status 1 #JUSTERR",
	},
	{
		"set -u; echo ${foo+bar}",
		"\n",
	},
	{
		"set -u; echo ${foo:+bar}",
		"\n",
	},
	{
		"set -u; echo ${foo-bar}",
		"bar\n",
	},
	{
		"set -u; echo ${foo:-bar}",
		"bar\n",
	},
	{
		"set -u; echo ${foo=bar}",
		"bar\n",
	},
	{
		"set -u; echo ${foo:=bar}",
		"bar\n",
	},
	{
		"set -u; echo ${foo?bar}",
		"foo: bar\nexit status 1 #JUSTERR",
	},
	{
		"set -u; echo ${foo:?bar}",
		"foo: bar\nexit status 1 #JUSTERR",
	},
	{
		"set -ue; set -ueo pipefail",
		"",
	},
	{"set -n; echo foo", ""},
	{"set -n; [ wrong", ""},
	{"set -n; set +n; echo foo", ""},
	{
		"set -o foobar",
		"set: foobar: invalid option name\nexit status 2 #JUSTERR",
	},
	{"set -o noexec; echo foo", ""},
	{"set +o noexec; echo foo", "foo\n"},
	{"set -e; set -o | grep -E 'errexit|noexec' | wc -l | tr -d ' '", "2\n"},
	{"set -e; set -o | grep -E 'errexit|noexec' | grep 'on$' | wc -l | tr -d ' '", "1\n"},
	{
		// `set -a; set +o`: confirm the explicitly-set allexport
		// is reflected as `set -o allexport`. The no-op aliases
		// (history, monitor, …) interleave but we just look for
		// the line we care about.
		"set -a; set +o | grep '^set -o allexport$'",
		"set -o allexport\n",
	},
	{`set - foobar; echo $@; set -; echo $@`, "foobar\nfoobar\n"},

	// unset
	{
		"a=1; echo $a; unset a; echo $a",
		"1\n\n",
	},
	{
		"notinpath() { echo func; }; notinpath; unset -f notinpath; notinpath",
		"func\n\"notinpath\": executable file not found in $PATH\nexit status 127 #JUSTERR",
	},
	{
		"a=1; a() { echo func; }; unset -f a; echo $a",
		"1\n",
	},
	{
		"a=1; a() { echo func; }; unset -v a; a; echo $a",
		"func\n\n",
	},
	{
		"unset -f -v SHELL",
		"unset: cannot simultaneously unset a function and a variable\nexit status 1 #JUSTERR",
	},
	{
		"notinpath=1; notinpath() { echo func; }; notinpath; echo $notinpath; unset notinpath; notinpath; echo $notinpath; unset notinpath; notinpath",
		"func\n1\nfunc\n\n\"notinpath\": executable file not found in $PATH\nexit status 127 #JUSTERR",
	},
	{
		"unset PATH; [[ $PATH == '' ]]",
		"",
	},
	{
		"readonly a=1; echo $a; unset a; echo $a",
		"1\nunset: a: cannot unset: readonly variable\n1\n #IGNORE bash prints a warning",
	},
	{
		"f() { local a=1; echo $a; unset a; echo $a; }; f",
		"1\n\n",
	},
	{
		`a=b eval 'echo $a; unset a; echo $a'`,
		"b\n\n",
	},
	{
		`$(unset INTERP_GLOBAL); echo $INTERP_GLOBAL; unset INTERP_GLOBAL; echo $INTERP_GLOBAL`,
		"value\n\n",
	},
	{
		`x=orig; f() { local x=local; unset x; x=still_local; }; f; echo $x`,
		"orig\n",
	},
	{
		`x=orig; f() { local x=local; unset x; [[ -v x ]] && echo set || echo unset; }; f`,
		"unset\n",
	},
	{
		`PS3="pick one: "; select opt in foo bar baz; do echo "Selected $opt"; break; done <<< 3`,
		"1) foo\n2) bar\n3) baz\npick one: Selected baz\n",
	},
	{
		`(set -o posix; for invalid-name in a; do echo body; done; echo after); echo outer:$?`,
		"`invalid-name': not a valid identifier\nouter:2\n",
	},
	{
		`bad_select() { select $1 in a b c; do echo $REPLY; done; }; bad_select "a b"; echo status:$?`,
		"`$1': not a valid identifier\nstatus:1\n",
	},
	{
		`opts=(foo bar baz); select opt in ${opts[@]}; do echo "Selected $opt"; break; done <<< 99`,
		"1) foo\n2) bar\n3) baz\n#? Selected \n",
	},
	{
		`select opt in foo; do
	case $opt in
	foo) echo "option 1"; break;;
	*) echo "invalid option $REPLY"; break;;
	esac
done <<< 2`,
		"1) foo\n#? invalid option 2\n",
	},

	// shopt
	{"set -e; shopt -o | grep -E '^(errexit|noexec)' | wc -l | tr -d ' '", "2\n"},
	{"set -e; shopt -o | grep -E '^(errexit|noexec)' | grep 'on$' | wc -l | tr -d ' '", "1\n"},
	{"set -e; shopt | grep -E '^(errexit|noexec)' | wc -l | tr -d ' '", "0\n"},
	{"shopt -s -o noexec; echo foo", ""},
	{"shopt -so noexec; echo foo", ""},
	{"shopt -u -o noexec; echo foo", "foo\n"},
	{
		"shopt -s -u checkhash",
		"shopt: cannot set and unset shell options simultaneously\nexit status 1 #JUSTERR",
	},
	{"shopt -u globstar; shopt globstar | grep 'off$' | wc -l | tr -d ' '", "1\n"},
	{"shopt -s globstar; shopt globstar | grep 'off$' | wc -l | tr -d ' '", "0\n"},
	{"shopt extglob | grep 'off' | wc -l | tr -d ' '", "1\n"},
	{
		"shopt inherit_errexit",
		"inherit_errexit     \toff\nexit status 1",
	},
	{
		"shopt -o -s pipefail; shopt -o pipefail | grep -q 'on$'",
		"",
	},
	{
		"shopt -o -u pipefail; shopt -o pipefail | grep -q 'on$'",
		"exit status 1",
	},
	{
		"shopt pipefail",
		"shopt: pipefail: invalid shell option name\nexit status 1 #JUSTERR",
	},
	{
		"shopt -s pipefail",
		"shopt: pipefail: invalid shell option name\nexit status 1 #JUSTERR",
	},
	{
		"shopt -o -s extglob",
		"shopt: extglob: invalid option name\nexit status 1 #JUSTERR",
	},
	{
		"shopt -s login_shell",
		"",
	},
	{
		"shopt -s interactive_comments",
		"",
	},
	{
		"shopt -s nosuchname",
		"shopt: nosuchname: invalid shell option name\nexit status 1 #JUSTERR",
	},
	{
		"shopt -o -s nosuchname",
		"shopt: nosuchname: invalid option name\nexit status 1 #JUSTERR",
	},
	{
		"touch a .b ..c; shopt -u dotglob; echo *",
		"a\n",
	},
	{
		"touch a .b ..c; shopt -s dotglob; echo *",
		"..c .b a\n",
	},
	{
		"mkdir sub .sub2; touch {sub,.sub2}/{a,.b}; shopt -s globstar; shopt -u dotglob; echo **/* | sed 's@\\\\@/@g'",
		"sub sub/a\n",
	},
	{
		"mkdir sub .sub2; touch {sub,.sub2}/{a,.b}; shopt -s globstar; shopt -s dotglob; echo **/* | sed 's@\\\\@/@g'",
		".sub2 .sub2/.b .sub2/a sub sub/.b sub/a\n",
	},
	{
		// Beware that macOS file systems are by default case-preserving but
		// case-insensitive, so e.g. "touch x X" creates only one file.
		"touch a ab Ac Ad; shopt -u nocaseglob; echo a*",
		"a ab\n",
	},
	{
		"touch a ab Ac Ad; shopt -s nocaseglob; echo a*",
		"Ac Ad a ab\n",
	},
	{
		"touch a ab abB Ac Ad; shopt -u nocaseglob; echo *b",
		"ab\n",
	},
	{
		"touch a ab abB Ac Ad; shopt -s nocaseglob; echo *b",
		"ab abB\n",
	},
	{
		// `shopt -p` lists every shopt in reusable form.
		// We don't pin the full output; just check exit 0.
		"shopt -p > /dev/null && echo ok",
		"ok\n",
	},
	{
		// `shopt -q NAME` returns 0 if set, 1 if not, no output.
		"shopt -q extglob; echo $?; shopt -s extglob; shopt -q extglob; echo $?",
		"1\n0\n",
	},

	// IFS
	{`echo -n "$IFS"`, " \t\n"},
	{`a="x:y:z"; IFS=:; echo $a`, "x y z\n"},
	{`a=(x y z); IFS=-; echo ${a[*]}`, "x y z\n"},
	{`a=(x y z); IFS=-; echo ${a[@]}`, "x y z\n"},
	{`a=(x y z); IFS=-; echo "${a[*]}"`, "x-y-z\n"},
	{`a=(x y z); IFS=-; echo "${a[@]}"`, "x y z\n"},
	{`a="  x y z"; IFS=; echo $a`, "  x y z\n"},
	{`a=(x y z); IFS=; echo "${a[*]}"`, "xyz\n"},
	{`a=(x y z); IFS=-; echo "${!a[@]}"`, "0 1 2\n"},
	{`set -- x y z; IFS=-; echo $*`, "x y z\n"},
	{`set -- x y z; IFS=-; echo "$*"`, "x-y-z\n"},
	{`set -- a b; IFS=é; echo "$*"`, "aéb\n"},
	{`set -- "x y" z; unset IFS; printf '<%s>\n' $@`, "<x y>\n<z>\n"},
	{`set -- "x y" z; IFS=; printf '<%s>\n' $@`, "<x y>\n<z>\n"},
	{`set -- x y z; IFS=; echo $*`, "x y z\n"},
	{`set -- x y z; IFS=; echo "$*"`, "xyz\n"},
	{`set -- x y z; IFS=; unset v; printf '<%s>\n' ${v-$@}`, "<x>\n<y>\n<z>\n"},
	{`set -- x y z; IFS=; unset v; printf '<%s>\n' ${v-"$@"}`, "<x>\n<y>\n<z>\n"},
	{`set -- x y z; IFS=; unset v; printf '<%s>\n' ${v-$*}`, "<x>\n<y>\n<z>\n"},
	{`set -- x y z; IFS=; unset v; printf '<%s>\n' ${v-"$*"}`, "<xyz>\n"},
	{`set -- x y; unset v; printf '<%s>\n' ${v="$*"}; printf '[%s]\n' "$v"`, "<x>\n<y>\n[x y]\n"},
	{`set -o posix; set -- " abc" "def ghi" "jkl "; IFS=; unset v; printf '<%s>\n' ${v-$@}`, "< abc>\n<def ghi>\n<jkl >\n"},
	{`set -o posix; set -- " abc" "def ghi" "jkl "; IFS=:; unset v; printf '<%s>\n' ${v-$@}`, "< abc def ghi jkl >\n"},
	{`set -o posix; set -- " abc" "def ghi" "jkl "; unset IFS v; printf '<%s>\n' ${v-$@}`, "< abc def ghi jkl >\n"},

	// builtin
	{"builtin", ""},
	{"builtin noexist", "builtin: noexist: not a shell builtin\nexit status 1 #JUSTERR"},
	{
		"builtin -x",
		"builtin: -x: invalid option\nbuiltin: usage: builtin [shell-builtin [arg ...]]\nexit status 2 #JUSTERR",
	},
	{"builtin echo foo", "foo\n"},
	{
		"echo() { printf 'bar\n'; }; echo foo; builtin echo foo",
		"bar\nfoo\n",
	},

	// type
	{"type", ""},
	{"type for", "for is a shell keyword\n"},
	{"type echo", "echo is a shell builtin\n"},
	{"echo() { :; }; type echo | grep 'is a function'", "echo is a function\n"},
	{"type $PATH_PROG | grep -q -E ' is (/|[A-Z]:)'", ""},
	{"type noexist", "type: noexist: not found\nexit status 1 #JUSTERR"},
	{"PATH=/; type $PATH_PROG", "type: " + pathProg + ": not found\nexit status 1 #JUSTERR"},
	{"shopt -s expand_aliases; alias interp_foo='bar baz'\ntype interp_foo", "interp_foo is aliased to `bar baz'\n"},
	{"alias interp_foo='bar baz'\ntype interp_foo", "type: interp_foo: not found\nexit status 1 #JUSTERR"},
	{"type -p $PATH_PROG | grep -q -E '^(/|[A-Z]:)'", ""},
	{"PATH=/; type -p $PATH_PROG", "exit status 1"},
	// TODO: type -P should force PATH lookup even for builtins, unlike type -p.
	{"type -P $PATH_PROG | grep -q -E '^(/|[A-Z]:)'", ""},
	{"PATH=/; type -P $PATH_PROG", "exit status 1"},
	{"shopt -s expand_aliases; alias interp_foo='bar'; type -t interp_foo", "alias\n"},
	{"type -t case", "keyword\n"},
	{"interp_foo(){ :; }; type -t interp_foo", "function\n"},
	{"type -t type", "builtin\n"},
	{"type -t $PATH_PROG", "file\n"},
	{"type -t inexisting_dfgsdgfds", "exit status 1"},

	// type -a: show all matches in priority order. echo is both a
	// builtin and a file in PATH (via the test exec handler).
	{"type -a -t echo", "builtin\nfile\n"},
	{"interp_myfn(){ :; }; type -a -t interp_myfn", "function\n"},
	{"interp_myfn(){ :; }; type -a interp_myfn echo | head -2", "interp_myfn is a function\ninterp_myfn () \n"},
	{"interp_myfn(){ echo foo |& cat; }; type interp_myfn", "interp_myfn is a function\ninterp_myfn () \n{ \n    echo foo 2>&1 | cat\n}\n"},
	{`set -o posix
swap32_posix()
{
	local arg
	for arg in "$@"; do
		echo $((
			($arg & 4278190080) >> 24 |
			($arg & 16711680) >> 8 |
			($arg & 65280) << 8 |
			($arg & 255) << 24
		))
	done
}
type swap32_posix`, "swap32_posix is a function\nswap32_posix () \n{ \n    local arg;\n    for arg in \"$@\";\n    do\n        echo $((\n                        ($arg & 4278190080) >> 24 |\n                        ($arg & 16711680) >> 8 |\n                        ($arg & 65280) << 8 |\n                        ($arg & 255) << 24\n                ));\n    done\n}\n"},

	// type -f: skip function lookup, fall through to builtin/file.
	{"echo(){ :; }; type -t echo", "function\n"},
	{"echo(){ :; }; type -t -f echo", "builtin\n"},

	// command -V: verbose form, mirrors `type` default output.
	{"command -V echo", "echo is a shell builtin\n"},
	{"command -V noexist", "command: noexist: not found\nexit status 1 #JUSTERR"},

	// hash
	{"hash $PATH_PROG", ""},
	{"set +o hashall; hash -p /bin/sh sh", "hash: hashing disabled\nexit status 1 #JUSTERR"},
	{"hash -v", "hash: -v: invalid option\nhash: usage: hash [-lr] [-p pathname] [-dt] [name ...]\nexit status 1 #JUSTERR"},
	{"hash -d", "hash: -d: option requires an argument\nexit status 2 #JUSTERR"},

	// trap
	{"trap 'echo at_exit' EXIT; true", "at_exit\n"},
	{"trap 'echo on_err' ERR; false; echo FAIL", "on_err\nFAIL\n"},
	{"trap 'echo on_err' ERR; false || true; echo OK", "OK\n"},
	{"trap 'echo at_exit' EXIT; trap - EXIT; echo OK", "OK\n"},
	{"set -e; trap 'echo A' ERR EXIT; false; echo FAIL", "A\nA\nexit status 1"},
	{"trap 'foobar' UNKNOWN", "trap: UNKNOWN: invalid signal specification\nexit status 2 #JUSTERR"},
	{"trap -p NOSIG", "trap: NOSIG: invalid signal specification\nexit status 1 #JUSTERR"},
	{
		"trap -s",
		"trap: -s: invalid option\ntrap: usage: trap [-Plp] [[action] signal_spec ...]\nexit status 2 #JUSTERR",
	},
	{"set -T; f(){\n:\n}\ntrap 'echo return:$LINENO' RETURN; f", "return:1\n"},
	// TODO: our builtin appears to not receive the piped bytes?
	// {"trap 'echo on_err' ERR; trap | grep -q '.*echo on_err.*'", "trap -- \"echo on_err\" ERR\n"},
	{"trap 'false' ERR EXIT; false", "exit status 1"},

	// eval
	{"eval", ""},
	{"eval ''", ""},
	{
		"eval -i 'echo foo'",
		"eval: -i: invalid option\neval: usage: eval [arg ...]\nexit status 2 #JUSTERR",
	},
	{"eval -- echo foo", "foo\n"},
	{"eval echo foo", "foo\n"},
	{"eval 'echo foo'", "foo\n"},
	{"eval 'exit 1'", "exit status 1"},
	{"eval '(x'", "eval: 1:1: reached EOF without matching `(` with `)`\nexit status 1 #JUSTERR"},
	{"set a b; eval 'echo $@'", "a b\n"},
	{"eval 'a=foo'; echo $a", "foo\n"},
	{`a=b eval "echo $a"`, "\n"},
	{`a=b eval 'echo $a'`, "b\n"},
	{`eval 'echo "\$a"'`, "$a\n"},
	{`a=b eval 'x=y eval "echo \$a \$x"'`, "b y\n"},
	{`a=b eval 'a=y eval "echo $a \$a"'`, "b y\n"},
	{"a=b eval '(echo $a)'", "b\n"},

	// source
	{
		"source",
		"source: filename argument required\nsource: usage: source [-p path] filename [arguments]\nexit status 2 #JUSTERR",
	},
	{
		"echo 'echo foo' >a; source ./a; . ./a",
		"foo\nfoo\n",
	},
	{
		"echo 'echo $@' >a; source ./a; source ./a b c; echo $@",
		"\nb c\n\n",
	},
	{
		"echo 'foo=bar' >a; source ./a; echo $foo",
		"bar\n",
	},

	// source from PATH
	{
		"mkdir test; echo 'echo foo' >test/a; PATH=$PWD/test source a; . test/a",
		"foo\nfoo\n",
	},

	// source with set and shift
	{
		"echo 'set -- d e f' >a; source ./a; echo $@",
		"d e f\n",
	},
	{
		"echo 'echo $@' >a; set -- b c; source ./a; echo $@",
		"b c\nb c\n",
	},
	{
		"echo 'echo $@' >a; set -- b c; source ./a d e; echo $@",
		"d e\nb c\n",
	},
	{
		"echo 'shift; echo $@' >a; set -- b c; source ./a d e; echo $@",
		"e\nb c\n",
	},
	{
		"echo 'shift' >a; set -- b c; source ./a; echo $@",
		"c\n",
	},
	{
		"echo 'shift; set -- $@' >a; set -- b c; source ./a d e; echo $@",
		"e\n",
	},
	{
		"echo 'set -- g f'>b; echo 'set -- d e f; echo $@; source ./b;' >a; source ./a; echo $@",
		"d e f\ng f\n",
	},
	{
		"echo 'set -- g f'>b; echo 'echo $@; set -- d e f; source ./b;' >a; source ./a b c; echo $@",
		"b c\ng f\n",
	},
	{
		"echo 'shift; echo $@' >b; echo 'shift; echo $@; source ./b' >a; source ./a b c d; echo $@",
		"c d\nd\n\n",
	},
	{
		"echo 'set -- b c d' >b; echo 'source ./b' >a; set -- a; source ./a; echo $@",
		"b c d\n",
	},
	{
		"echo 'echo $@' >b; echo 'set -- b c d; source ./b' >a; set -- a; source ./a; echo $@",
		"b c d\nb c d\n",
	},
	{
		"echo 'shift; echo $@' >b; echo 'shift; echo $@; source ./b c d' >a; set -- a b; source ./a; echo $@",
		"b\nd\nb\n",
	},
	{
		"echo 'set -- a b c' >b; echo 'echo $@; source ./b; echo $@' >a; source ./a; echo $@",
		"\na b c\na b c\n",
	},

	// indexed arrays
	{
		"a=foo; echo ${a[0]} ${a[@]} ${a[x]}; echo ${a[1]}",
		"foo foo foo\n\n",
	},
	{
		"a=(); echo ${a[0]} ${a[@]} ${a[x]} ${a[1]}",
		"\n",
	},
	{
		"a=(b c); echo $a; echo ${a[0]}; echo ${a[1]}; echo ${a[x]}",
		"b\nb\nc\nb\n",
	},
	{
		"a=(b c); echo ${a[@]}; echo ${a[*]}",
		"b c\nb c\n",
	},
	{
		"a=(1 2 3); echo ${a[2-1]}; echo $((a[1+1]))",
		"2\n3\n",
	},
	{
		"a=(1 2) x=(); a+=b x+=c; echo ${a[@]}; echo ${x[@]}",
		"1b 2\nc\n",
	},
	{
		"a=(1 2) x=(); a+=(b c) x+=(d e); echo ${a[@]}; echo ${x[@]}",
		"1 2 b c\nd e\n",
	},
	{
		"a=bbb; a+=(c d); echo ${a[@]}",
		"bbb c d\n",
	},
	{
		`a=('a  1' 'b  2'); for e in ${a[@]}; do echo "$e"; done`,
		"a\n1\nb\n2\n",
	},
	{
		`a=('a  1' 'b  2'); for e in "${a[*]}"; do echo "$e"; done`,
		"a  1 b  2\n",
	},
	{
		`a=('a  1' 'b  2'); for e in "${a[@]}"; do echo "$e"; done`,
		"a  1\nb  2\n",
	},
	{
		`declare -a a; a[0]='a  1'; a[1]='b  2'; for e in "${a[@]}"; do echo "$e"; done`,
		"a  1\nb  2\n",
	},
	{
		`a=([1]=y [0]=x); echo ${a[0]}`,
		"x\n",
	},
	{
		`a=(y); a[2]=x; echo ${a[2]}`,
		"x\n",
	},
	{
		`a="y"; a[2]=x; echo ${a[2]}`,
		"x\n",
	},
	{
		`declare -a a=(x y); echo ${a[1]}`,
		"y\n",
	},
	{
		`a=b; echo "${a[@]}"`,
		"b\n",
	},
	{
		`a=(b); echo ${a[3]}`,
		"\n",
	},
	{
		`a=(b); echo ${a[-2]}`,
		"negative array index\n #JUSTERR",
	},
	{
		`a=abcde; declare -a a; a[5]="hello world"; a[4+5/2]="test expression"; declare a["7 + 8"]="test 2"; a[7 + 8]="test 2"; declare -p a; echo "${#a[@]}"; printf '%s\n' "${!a[@]}"`,
		"declare -a a=([0]=\"abcde\" [5]=\"hello world\" [6]=\"test expression\" [15]=\"test 2\")\n4\n0\n5\n6\n15\n",
	},
	{
		`a=(x "" y); unset 'a[0]'; a[5]=z; declare -p a; echo "${#a[@]}"; printf '<%s>\n' "${a[@]}"`,
		"declare -a a=([1]=\"\" [2]=\"y\" [5]=\"z\")\n3\n<>\n<y>\n<z>\n",
	},
	{
		`a=([0]=' x ' [1]=' y '); for v in "${a[@]}"; do echo "$v"; done`,
		" x \n y \n",
	},
	{
		`a=([0]=' x ' [1]=' y '); for v in "${a[*]}"; do echo "$v"; done`,
		" x   y \n",
	},
	{
		`a=([0]=' x ' [1]=' y '); for v in "${!a[@]}"; do echo "$v"; done`,
		"0\n1\n",
	},
	{
		`a=([0]=' x ' [1]=' y '); for v in "${!a[*]}"; do echo "$v"; done`,
		"0 1\n",
	},

	// associative arrays
	{
		`a=foo; echo ${a[""]} ${a["x"]}`,
		"foo foo\n",
	},
	{
		`declare -A a=(); echo ${a[0]} ${a[@]} ${a[1]} ${a["x"]}`,
		"\n",
	},
	{
		`declare -A a; a=4; declare -p a`,
		"declare -A a=([0]=\"4\" )\n",
	},
	{
		`declare -A a; a=([one]=1 [two]=2); declare -p a`,
		"declare -A a=([two]=\"2\" [one]=\"1\" )\n",
	},
	{
		`declare -A a; a[hello world]=flip; printf '<%s>\n' "${a[hello world]}"; a=([six]=6 [foo bar]="qux qix"); printf '<%s>\n' "${a[foo bar]}"; declare -p a`,
		"<flip>\n<qux qix>\ndeclare -A a=([six]=\"6\" [\"foo bar\"]=\"qux qix\" )\n",
	},
	{
		`touch afo; declare -A a=([foo]=one [bar]=two); unset a[foo]; declare -p a; declare -A c; c[foo]=one; c[bar]=two; unset c[foo]; declare -p c; b=(zero one); unset b[0]; declare -p b`,
		"declare -A a=([bar]=\"two\" )\ndeclare -A c=([bar]=\"two\" )\ndeclare -a b=([1]=\"one\")\n",
	},
	{
		`flix=9; declare -Ai a=([zero]=1+4 [one]=3+7 [foo bar]=flix); a[foo bar]+=7; declare -p a`,
		"declare -Ai a=([one]=\"10\" [\"foo bar\"]=\"16\" [zero]=\"5\" )\n",
	},
	{
		`declare -A a; a=([zero]=0 four [one]=1); declare -p a`,
		"a: four: must use subscript when assigning associative array\ndeclare -A a=([one]=\"1\" [zero]=\"0\" )\n #JUSTERR",
	},
	{
		`flix=9; declare -A a=([s*]=6 [foo bar]=flix); declare -p a; printf '<%s>\n' "${a[foo bar]}"; b=([1+2]=three); declare -p b`,
		"declare -A a=([\"s*\"]=\"6\" [\"foo bar\"]=\"flix\" )\n<flix>\ndeclare -a b=([3]=\"three\")\n",
	},
	{
		`declare -A a=([foo]=bar); unset empty; echo ${#a[$empty]}; echo ${#a[missing]}`,
		"[$empty]: bad array subscript\n0\n #JUSTERR",
	},
	{
		`declare -a AA; unset 'AA[-2]'`,
		"unset: [-2]: bad array subscript\nexit status 1 #JUSTERR",
	},
	{
		`declare -A a=([one]=a [*]=12 [hello world]=flip [box]="multiple words"); printf '<%s>\n' "${a[@]}"; printf '<%s>\n' "${!a[@]}"`,
		"<multiple words>\n<12>\n<flip>\n<a>\n<box>\n<*>\n<hello world>\n<one>\n",
	},
	{
		`declare -A a; a[bar\"bie]=doll; a[bar\'bie]=toy; a[bar\$bie]=cash; printf '<%s>\n' "${!a[@]}"; declare -p a`,
		"<bar'bie>\n<bar$bie>\n<bar\"bie>\ndeclare -A a=([\"bar'bie\"]=\"toy\" [\"bar\\$bie\"]=\"cash\" [\"bar\\\"bie\"]=\"doll\" )\n",
	},
	{
		`declare -A a; declare +A a; declare -a b; declare +a b`,
		"declare: a: cannot destroy array variables in this way\ndeclare: b: cannot destroy array variables in this way\nexit status 1 #JUSTERR",
	},
	{
		`declare -A a=([0]=zero [x]=ex); echo "$a"; echo "${a:1:2}"`,
		"zero\ner\n",
	},
	{
		`declare -A a=([x]=b [y]=c); echo $a; echo ${a[0]}; echo ${a["x"]}; echo ${a["_"]}`,
		"\n\nb\n\n",
	},
	{
		`declare -Ag a=([x]=y); echo ${a["x"]}`,
		"y\n",
	},
	{
		`declare -A a=([x]=b [y]=c); for e in ${a[@]}; do echo $e; done | sort`,
		"b\nc\n",
	},
	{
		`declare -A a=([y]=b [x]=c); for e in ${a[*]}; do echo $e; done | sort`,
		"b\nc\n",
	},
	{
		`declare -A a=([x]=a); a["y"]=d; a["x"]=c; for e in ${a[@]}; do echo $e; done | sort`,
		"c\nd\n",
	},
	{
		`declare -A a=([x]=a); a[y]=d; a[x]=c; for e in ${a[@]}; do echo $e; done | sort`,
		"c\nd\n",
	},
	{
		// cheating a little; bash just did a=c
		`a=(["x"]=b ["y"]=c); echo ${a["y"]}`,
		"c\n",
	},
	{
		`declare -A a=(['x']=b); echo ${a['x']} ${a[$'x']} ${a[$"x"]}`,
		"b b b\n",
	},
	{
		`arr=([0x003d]==); echo ${arr[61]}`,
		"=\n",
	},
	{
		`arr=([0x0020]=\  [0x0021]=\! [0x005c]=\\); printf '<%s>\n' "${arr[32]}" "${arr[33]}" "${arr[92]}"`,
		"< >\n<!>\n<\\>\n",
	},
	{`printf '<%s>\n' $'\Uffffffff'`, "<>\n"},
	{
		`a=(['x']=b); echo ${a['y']}`,
		"\n #IGNORE bash requires -A",
	},
	{
		`declare -A a=(['a  1']=' x ' ['b  2']=' y '); for v in "${a[@]}"; do echo "$v"; done | sort`,
		" x \n y \n",
	},
	{
		`declare -A a=(['a  1']=' x ' ['b  2']=' y '); for v in "${a[*]}"; do echo "$v"; done`,
		" x   y \n",
	},
	{
		`declare -A a=(['a  1']=' x ' ['b  2']=' y '); for v in "${!a[@]}"; do echo "$v"; done | sort`,
		"a  1\nb  2\n",
	},
	{
		`declare -A a=(['a  1']=' x ' ['b  2']=' y '); for v in "${!a[*]}"; do echo "$v"; done`,
		"a  1 b  2\n",
	},
	{
		`declare -A a; a[a]=x; a[b]=y; for v in "${!a[@]}"; do echo "$v"; done | sort`,
		"a\nb\n",
	},
	{
		`declare -A a; a[a]=x; a[b]=y; declare -A a; for v in "${!a[@]}"; do echo "$v"; done | sort`,
		"a\nb\n",
	},
	// weird assignments
	{"a=b; a=(c d); echo ${a[@]}", "c d\n"},
	{"a=(b c); a=d; echo ${a[@]}", "d c\n"},
	{"declare -A a=([x]=b [y]=c); a=d; for e in ${a[@]}; do echo $e; done | sort", "b\nc\nd\n"},
	{"i=3; a=b; a[i]=x; echo ${a[@]}", "b x\n"},
	{"i=3; declare a=(b); a[i]=x; echo ${!a[@]}", "0 3\n"},
	{"i=3; declare -A a=(['x']=b); a[i]=x; for e in ${!a[@]}; do echo $e; done | sort", "i\nx\n"},

	// declare
	{"declare -B foo", "declare: -B: invalid option\ndeclare: usage: declare [-aAfFgiIlnrtux] [name[=value] ...] or declare -p [-aAfFilnrtux] [name ...]\nexit status 2 #JUSTERR"},
	{"declare -- -z", "declare: invalid name \"-z\"\nexit status 1 #JUSTERR"},
	{"a=b; declare a; echo $a; declare a=; echo $a", "b\n\n"},
	{"a=b; declare a; echo $a", "b\n"},
	{
		"declare a=b c=(1 2); echo $a; echo ${c[@]}",
		"b\n1 2\n",
	},
	{"a=x; declare $a; echo $a $x", "x\n"},
	{"a=x=y; declare $a; echo $a $x", "x=y y\n"},
	{"a='x=(y)'; declare $a; echo $a $x", "x=(y) (y)\n"},
	{"a='x=b y=c'; declare $a; echo $x $y", "b c\n"},
	{"declare =bar", "declare: invalid name \"\"\nexit status 1 #JUSTERR"},
	{"declare $unset=$unset", "declare: invalid name \"\"\nexit status 1 #JUSTERR"},

	// export
	{"declare foo=bar; $ENV_PROG | grep '^foo='", "exit status 1"},
	{"declare -x foo=bar; $ENV_PROG | grep '^foo='", "foo=bar\n"},
	{"export foo=bar; $ENV_PROG | grep '^foo='", "foo=bar\n"},
	{"foo=bar; export foo; $ENV_PROG | grep '^foo='", "foo=bar\n"},
	{"export foo=bar; foo=baz; $ENV_PROG | grep '^foo='", "foo=baz\n"},
	{"export foo=bar; readonly foo=baz; $ENV_PROG | grep '^foo='", "foo=baz\n"},
	{"export foo=(1 2); $ENV_PROG | grep '^foo='", "exit status 1"},
	{"declare -A foo=([a]=b); export foo; $ENV_PROG | grep '^foo='", "exit status 1"},
	{"export foo=(b c); foo=x; $ENV_PROG | grep '^foo='", "exit status 1"},
	{"foo() { bar=foo; export bar; }; foo; $ENV_PROG | grep ^bar=", "bar=foo\n"},
	{"foo() { export bar; }; bar=foo; foo; $ENV_PROG | grep ^bar=", "bar=foo\n"},
	{"foo() { export bar; }; foo; bar=foo; $ENV_PROG | grep ^bar=", "bar=foo\n"},
	{"foo() { export bar=foo; }; foo; readonly bar; $ENV_PROG | grep ^bar=", "bar=foo\n"},

	// local
	{
		"local a=b",
		"local: can only be used in a function\nexit status 1 #JUSTERR",
	},
	{
		"local a=b 2>/dev/null; echo $a",
		"\n",
	},
	{
		"{ local a=b; }",
		"local: can only be used in a function\nexit status 1 #JUSTERR",
	},
	{
		"echo 'local a=b' >a; source ./a",
		"local: can only be used in a function\nexit status 1 #JUSTERR",
	},
	{
		"echo 'local a=b' >a; f() { source ./a; }; f; echo $a",
		"\n",
	},
	{
		"f() { local a=b; }; f; echo $a",
		"\n",
	},
	{
		"a=x; f() { local a=b; }; f; echo $a",
		"x\n",
	},
	{
		"a=x; f() { echo $a; local a=b; echo $a; }; f",
		"x\nb\n",
	},
	{
		"f1() { local a=b; }; f2() { f1; echo $a; }; f2",
		"\n",
	},
	{
		"f() { a=1; declare b=2; export c=3; readonly d=4; declare -g e=5; }; f; echo $a $b $c $d $e",
		"1 3 4 5\n",
	},
	{
		`f() { local x; [[ -v x ]] && echo set || echo unset; }; f`,
		"unset\n",
	},
	{
		`f() { local x=; [[ -v x ]] && echo set || echo unset; }; f`,
		"set\n",
	},
	{
		`export x=before; f() { local x; export x=after; $ENV_PROG | grep '^x='; }; f; echo $x`,
		"x=after\nbefore\n",
	},
	{
		"getx() { echo $X; }; f() { local X=Y; getx; echo $X; }; f",
		"Y\nY\n",
	},
	{
		"setx() { X=Y; }; f() { local X; setx; echo $X; }; f",
		"Y\n",
	},
	{
		"setx() { local X=Y; }; f() { local X; setx; echo $X; }; f",
		"\n",
	},
	{
		"setx() { declare X=Y; }; f() { local X; setx; echo $X; }; f",
		"\n",
	},
	{
		"setx() { X=Y :; }; f() { local X; setx; echo $X; }; f",
		"\n",
	},

	// unset global from inside function
	{"f() { unset foo; echo $foo; }; foo=bar; f", "\n"},
	{"f() { unset foo; }; foo=bar; f; echo $foo", "\n"},

	// name references
	{"declare -n foo=bar; bar=etc; [[ -R foo ]]", ""},
	{"declare -n foo=bar; bar=etc; [ -R foo ]", ""},
	{"nameref foo=bar; bar=etc; [[ -R foo ]]", " #IGNORE"},
	{`arr=(a b); f(){ local -n A=arr; printf '<%s>\n' "${!A[@]}"; }; f`, "<0>\n<1>\n"},
	{`arr=([0x03A8]=x); f(){ local -n A=${1:?}; printf '<%s>\n' "${!A[@]}"; }; f arr`, "<936>\n"},
	{"declare foo=bar; bar=etc; [[ -R foo ]]", "exit status 1"},
	{
		"declare -n foo=bar; bar=etc; echo $foo; bar=zzz; echo $foo",
		"etc\nzzz\n",
	},
	{
		"declare -n foo=bar; bar=(x y); echo ${foo[1]}; bar=(a b); echo ${foo[1]}",
		"y\nb\n",
	},
	{
		"declare -n foo=bar; bar=etc; echo $foo; unset bar; echo $foo",
		"etc\n\n",
	},
	{
		"declare -n a1=a2 a2=a3 a3=a4; a4=x; echo $a1 $a3",
		"x x\n",
	},
	{
		"declare -n foo=bar bar=foo; echo $foo",
		"\n #IGNORE",
	},
	{
		"declare -n foo=bar; echo $foo",
		"\n",
	},
	{
		"declare -n foo=bar; echo ${!foo}",
		"bar\n",
	},
	{
		"declare -n foo=bar; bar=etc; echo $foo; echo ${!foo}",
		"etc\nbar\n",
	},
	{
		"declare -n foo=bar; bar=etc; foo=value; echo $foo; echo $bar",
		"value\nvalue\n",
	},
	{
		"declare -n foo=bar; foo=value; echo $foo; echo $bar",
		"value\nvalue\n",
	},
	{
		"declare -n foo=bar; declare foo=value; echo $foo; echo $bar",
		"value\nvalue\n",
	},
	{
		"declare -n foo=bar bar=baz; foo=value; echo $foo; echo $bar; echo $baz",
		"value\nvalue\nvalue\n",
	},
	{
		"declare -n foo=bar; set -u; echo ${foo}",
		"foo: unbound variable\nexit status 1 #JUSTERR",
	},
	{
		"declare -n foo=bar; set -u; echo ${foo:=value}; echo $foo; echo $bar",
		"value\nvalue\nvalue\n",
	},
	{
		"declare -n foo=bar; foo=value $ENV_PROG | grep '^bar='",
		"bar=value\n",
	},
	{
		"echo ${!@}-${!*}; set -- foo; echo ${!@}-${!*}-${!1}; foo=value; echo ${!@}-${!*}-${!1}",
		"-\n--\nvalue-value-value\n",
	},
	{
		"declare -n ref=arr; ref+=(x y); echo ${ref[@]} ${arr[@]}",
		"x y x y\n",
	},

	// read-only vars
	{"declare -r foo=bar; echo $foo", "bar\n"},
	{"readonly foo=bar; echo $foo", "bar\n"},
	{
		"readonly -x foo",
		"readonly: -x: invalid option\nreadonly: usage: readonly [-aAf] [name[=value] ...] or readonly -p\nexit status 2 #JUSTERR",
	},
	{"readonly foo=bar; export foo; echo $foo", "bar\n"},
	{"readonly foo=bar; readonly bar=foo; export foo bar; echo $bar", "foo\n"},
	{
		"a=b; a=c; echo $a; readonly a; a=d",
		"c\na: readonly variable\nexit status 1 #JUSTERR",
	},
	{
		"declare -r foo=bar; foo=etc",
		"foo: readonly variable\nexit status 1 #JUSTERR",
	},
	{
		"VAR=4; readonly VAR; VAR=7 :; echo $VAR",
		"VAR: readonly variable\n4\n #JUSTERR",
	},
	{
		"declare -r foo=bar; export foo=",
		"foo: readonly variable\nexit status 1 #JUSTERR",
	},
	{
		"readonly foo=bar; foo=etc",
		"foo: readonly variable\nexit status 1 #JUSTERR",
	},
	{
		"foo() { bar=foo; readonly bar; }; foo; bar=bar",
		"bar: readonly variable\nexit status 1 #JUSTERR",
	},
	{
		"foo() { readonly bar; }; foo; bar=foo",
		"bar: readonly variable\nexit status 1 #JUSTERR",
	},
	{
		"foo() { readonly bar=foo; }; foo; export bar; $ENV_PROG | grep '^bar='",
		"bar=foo\n",
	},

	// multiple var modes at once
	{
		"declare -r -x foo=bar; $ENV_PROG | grep '^foo='",
		"foo=bar\n",
	},
	{
		"declare -r -x foo=bar; foo=x",
		"foo: readonly variable\nexit status 1 #JUSTERR",
	},

	// globbing
	{"echo .", ".\n"},
	{"echo ..", "..\n"},
	{"echo ./.", "./.\n"},
	{
		">a.x >b.x >c.x; echo *.x; rm a.x b.x c.x",
		"a.x b.x c.x\n",
	},
	{
		`>a.x; echo '*.x' "*.x"; rm a.x`,
		"*.x *.x\n",
	},
	{
		`>a.x >b.y; echo *'.'x; rm a.x`,
		"a.x\n",
	},
	{
		`>a.x; echo *'.x' "a."* '*'.x; rm a.x`,
		"a.x a.x *.x\n",
	},
	{
		"echo *.x; echo foo *.y bar",
		"*.x\nfoo *.y bar\n",
	},
	{
		`>a.x >b.x >c.x; a=*.x; echo $a; echo "$a"`,
		"a.x b.x c.x\n*.x\n",
	},
	{
		`>a.x >b.x >c.x; a=(*.x); echo "${a[@]}"; echo ${a[1]}`,
		"a.x b.x c.x\nb.x\n",
	},
	{
		"mkdir a; >a/b.x; echo */*.x | sed 's@\\\\@/@g'; cd a; echo *.x",
		"a/b.x\nb.x\n",
	},
	{
		"mkdir -p a/b/c; echo a/* | sed 's@\\\\@/@g'",
		"a/b\n",
	},
	{
		">.hidden >a; echo *; echo .h*; rm .hidden a",
		"a\n.hidden\n",
	},
	{
		`mkdir d; >d/.hidden >d/a; set -- "$(echo d/*)" "$(echo d/.h*)"; echo ${#1} ${#2}; rm -r d`,
		"3 9\n",
	},
	{
		"mkdir -p a/b/c; echo a/** | sed 's@\\\\@/@g'",
		"a/b\n",
	},
	{
		"shopt -s globstar; mkdir -p a/b/c; echo a/** | sed 's@\\\\@/@g'",
		"a/ a/b a/b/c\n",
	},
	{
		"shopt -s globstar; mkdir -p a/b/c; echo **/c | sed 's@\\\\@/@g'",
		"a/b/c\n",
	},
	{
		"shopt -s globstar; mkdir -p a/b; touch c; echo ** | sed 's@\\\\@/@g'",
		"a a/b c\n",
	},
	{
		"shopt -s globstar; mkdir -p a/b; touch c; echo **/ | sed 's@\\\\@/@g'",
		"a/ a/b/\n",
	},
	{
		"shopt -s globstar; mkdir -p a/b/c a/d; echo ** | sed 's@\\\\@/@g'",
		"a a/b a/b/c a/d\n",
	},
	{
		"shopt -s globstar; mkdir -p a.x a/b.x a/b/c.x; echo **.x ./**.x | sed 's@\\\\@/@g'",
		"a.x ./a.x\n",
	},
	{
		"shopt -s globstar; mkdir -p a/b; touch a/b/c; echo **/* | sed 's@\\\\@/@g'",
		"a a/b a/b/c\n",
	},
	{
		"shopt -s globstar; mkdir -p b; touch x2 a b/c d x1; echo **/* | sed 's@\\\\@/@g'",
		"a b b/c d x1 x2\n",
	},
	{
		"mkdir foo; touch foo/bar; echo */bar */bar/ | sed 's@\\\\@/@g'",
		"foo/bar */bar/\n",
	},
	{
		"shopt -s nullglob; touch existing-1; echo missing-* existing-*",
		"existing-1\n",
	},
	{
		"touch ŀfoo; echo ŀ*",
		"ŀfoo\n",
	},

	// Extended globbing via the extglob option.
	// Note how extglob affects Bash's own line-by-line parsing, so we set the option before a newline.
	{
		"shopt -s extglob\necho invalid-?([)",
		"invalid-?([)\n",
	},
	{
		"shopt -s extglob\necho +()c @()x",
		"+()c @()x\n",
	},
	{
		"touch az a1z a12z a123z; echo a?([0-9])z",
		"extended globbing operator used without the \"extglob\" option set\n #JUSTERR",
	},
	{
		"shopt -s extglob\ntouch az a1z a12z a123z; echo a?([0-9])z",
		"a1z az\n",
	},
	{
		"shopt -s extglob\ntouch az a1z a12z a123z; echo a*([0-9])z",
		"a123z a12z a1z az\n",
	},
	{
		"shopt -s extglob\ntouch az a1z a12z a123z; echo a+([0-9])z",
		"a123z a12z a1z\n",
	},
	{
		"shopt -s extglob\ntouch abd acd aed; echo a+(b|c)d",
		"abd acd\n",
	},
	{
		"shopt -s extglob\ntouch ab abcdef abef abcfef; echo ab**(e|f)",
		"ab abcdef abcfef abef\n",
	},
	{
		"shopt -s extglob\ncase '*(a|b[)' in *(a|b[)) echo yes;; *) echo no;; esac",
		"yes\n",
	},
	{
		"shopt -s extglob\ntouch a ab ba; echo a*!(x)",
		"a ab\n",
	},
	{
		"shopt -s extglob\ntouch a b c .x .y .z; echo !(f); echo !(f)!(f)",
		"a b c\na b c\n",
	},
	{
		"shopt -s extglob; GLOBIGNORE='+([^[:alnum:]]):@([-.,:; _]):[![:alnum:]]'; touch ';' '++'; echo *",
		"*\n",
	},
	{
		"shopt -s extglob; shopt -u globskipdots; touch .a .foo a.log; echo .*; echo @(.*); echo .?; echo @(.?); echo '---'; echo *(.)",
		". .. .a .foo\n. .. .a .foo\n.. .a\n.. .a\n---\n*(.)\n",
	},
	{
		"shopt -s extglob\ntouch az a1z a12z a123z; echo a@([0-9])z",
		"a1z\n",
	},
	{
		"mkdir -p ab/cd; touch ab/cd/efg; GLOBIGNORE='ab/cd/efg'; echo */*/efg",
		"*/*/efg\n",
	},
	{
		"enable -f ./strmatch.so strmatch; strmatch 'ab[/]ef' 'ab[/]ef'; echo $?; strmatch 'ab/ef' 'ab[/]ef'; echo $?",
		"0\n1\n",
	},
	{
		"shopt -s nullglob extglob\nprintf '<%s>\\n' @(missing) after",
		"<after>\n",
	},
	{
		"shopt -s extglob\ntouch a{1..9}0z; echo a+(0|[1-2]|8)z",
		"a10z a20z a80z\n",
	},
	{
		"shopt -s extglob\ntouch az a1z a12z a123z; echo a!([0-9])z",
		"a123z a12z az\n",
	},
	// !(pattern) extglob negation in case and [[ ]] matching
	{
		"shopt -s extglob\ncase \"bar\" in !(foo)) echo match;; esac",
		"match\n",
	},
	{
		"shopt -s extglob\ncase \"foo\" in !(foo)) echo match;; esac",
		"",
	},
	{
		"shopt -s extglob\ncase \"\" in !(foo)) echo match;; esac",
		"match\n",
	},
	{
		"shopt -s extglob\ncase \"baz\" in !(foo|bar)) echo match;; esac",
		"match\n",
	},
	{
		"shopt -s extglob\ncase \"file.tar.gz\" in !(*.sig)) echo match;; esac",
		"match\n",
	},
	{
		"shopt -s extglob\ncase \"file.sig\" in !(*.sig)) echo match;; esac",
		"",
	},
	{
		"shopt -s extglob\ncase \"foo_xxx_baz\" in foo_!(bar)_baz) echo match;; esac",
		"match\n",
	},
	{
		"shopt -s extglob\ncase \"foo_bar_baz\" in foo_!(bar)_baz) echo match;; esac",
		"",
	},
	{
		"shopt -s extglob\n[[ \"bar\" == !(foo) ]] && echo match",
		"match\n",
	},
	// Unsupported: multiple groups, glob prefix, or glob suffix.
	{
		"shopt -s extglob\ncase \"xabab\" in *a!(b)) echo match;; esac",
		" #IGNORE glob prefix not supported",
	},
	{
		"shopt -s extglob\ncase \"baz\" in !(foo)!(bar)) echo match;; esac",
		"match\n",
	},
	{
		"shopt -s extglob\ncase \".bar\" in .*!(foo)) echo match;; esac",
		" #IGNORE glob prefix not supported",
	},
	{
		"shopt -s extglob\ncase \".foo\" in .*!(foo)) echo match;; esac",
		" #IGNORE glob prefix not supported",
	},
	{
		"shopt -s extglob\ncase \"bar\" in .*!(foo)) echo match;; esac",
		" #IGNORE glob prefix not supported",
	},
	{
		// Extended pattern matching is always available outside of pathname expansions (globbing).
		"[[ a123z == a@([0-9])z ]]; echo $?; [[ a123z == a+([0-9])z ]]; echo $?",
		"1\n0\n",
	},
	// Ensure that setting nullglob does not return invalid globs as null
	// strings.
	{
		"shopt -s nullglob; [ -n butter ] && echo bubbles",
		"bubbles\n",
	},
	{
		"cat <<EOF\n{foo,bar}\nEOF",
		"{foo,bar}\n",
	},
	{
		"cat <<EOF\n*.go\nEOF",
		"*.go\n",
	},
	{
		"mkdir -p a/b a/c; echo ./a/* | sed 's@\\\\@/@g'",
		"./a/b ./a/c\n",
	},
	{
		"mkdir -p a/b a/c d; cd d; echo ../a/* | sed 's@\\\\@/@g'",
		"../a/b ../a/c\n",
	},
	{
		"mkdir x-d1 x-d2; >x-f; echo x-*/ | sed 's@\\\\@/@g'",
		"x-d1/ x-d2/\n",
	},
	{
		"mkdir x-d1 x-d2; >x-f; echo ././x-*/// | sed 's@\\\\@/@g'",
		"././x-d1/ ././x-d2/\n",
	},
	{
		"mkdir -p x-d1/a x-d2/b; >x-f; echo x-*/* | sed 's@\\\\@/@g'",
		"x-d1/a x-d2/b\n",
	},
	{
		"mkdir -p foo/bar; ln -s foo sym; echo sy*/; echo sym/b*",
		"sym/\nsym/bar\n",
	},
	{
		">foo; ln -s foo sym; echo sy*; echo sy*/",
		"sym\nsy*/\n",
	},
	{
		"mkdir x-d; >x-f; test -d $PWD/x-*/",
		"",
	},
	{
		"mkdir dir; >dir/x-f; ln -s dir sym; cd sym; test -f $PWD/x-*",
		"",
	},

	// brace expansion; there are also some tests in the expand package
	{"echo a}b", "a}b\n"},
	{"echo {a,b{c,d}", "{a,bc {a,bd\n"},
	{"echo a{b}", "a{b}\n"},
	{"echo a{à,世界}", "aà a世界\n"},
	{"echo a{b,c}d{e,f}g", "abdeg abdfg acdeg acdfg\n"},
	{"echo a{b{x,y},c}d", "abxd abyd acd\n"},
	{"echo a{1..", "a{1..\n"},
	{"echo a{1..2}b{4..5}c", "a1b4c a1b5c a2b4c a2b5c\n"},
	{"echo a{c..f}", "ac ad ae af\n"},
	{"echo a{4..1..1}", "a4 a3 a2 a1\n"},
	{"b=c; echo ${b}a{4..1..1}", "ca4 ca3 ca2 ca1\n"},
	{"b=c; echo a{1,2}$b", "a1c a2c\n"},
	{"echo a{1,2}'bc'", "a1bc a2bc\n"},
	{`echo a\{1,2}b`, "a{1,2}b\n"},
	{`echo a{1,2\`, "a{1,2\\\n"},
	{`echo a{1,2\}b`, "a{1,2}b\n"},
	{`echo a{1\,2,3}b`, "a1,2b a3b\n"},
	{`echo a{1\}2,3}b`, "a1}2b a3b\n"},
	{`echo a{1\..2}b`, "a{1..2}b\n"},
	{`echo \{\{iriname\}\}`, "{{iriname}}\n"},
	{
		"echo {1..100000}",
		"brace expansion would exceed 16384 elements\n #IGNORE bash has no defensive limit below MaxInt",
	},
	{
		"echo a{0..9999999999}b",
		"brace expansion would exceed 16384 elements\n #JUSTERR bash errors with a different message",
	},

	// brace expansion in declarations
	{"declare {A,B}_VAR=1; echo $A_VAR $B_VAR", "1 1\n"},
	{"declare {x,y}=val; echo $x $y", "val val\n"},
	{"declare -x RUN_{VERY_,}EXPENSIVE_TESTS=yes; echo $RUN_EXPENSIVE_TESTS", "yes\n"},
	{"declare {A,B}_VAR; A_VAR=1; B_VAR=2; echo $A_VAR $B_VAR", "1 2\n"},
	{"declare {foo=x,bar=y}; echo $foo $bar", "x y\n"},
	{`declare foo{bar=baz`, "declare: invalid name \"foo{bar\"\nexit status 1 #JUSTERR"},
	{"{a,b}=value", "\"a=value\": executable file not found in $PATH\nexit status 127 #JUSTERR"},

	// tilde expansion
	{
		"[[ '~/foo' == ~/foo ]] || [[ ~/foo == '~/foo' ]]",
		"exit status 1",
	},
	{
		"case '~/foo' in ~/foo) echo match ;; esac",
		"",
	},
	{
		"a=~/foo; [[ $a == '~/foo' ]]",
		"exit status 1",
	},
	{
		`a=$(echo "~/foo"); [[ $a == '~/foo' ]]`,
		"",
	},
	{
		`HOME=/foo; rel=/bar; echo ~/bar ~/'bar' ~/"bar" ~/$rel ~/"$rel"`,
		"/foo/bar /foo/bar /foo/bar /foo//bar /foo//bar\n",
	},
	{
		`HOME=/foo; rel=/bar; echo ~'/bar' ~"/bar" ~$rel ~"/$rel"`,
		"~/bar ~/bar ~/bar ~//bar\n",
	},
	{
		`HOME=/foo; echo ~ ~/ ~/'' ~'' ~""`,
		"/foo /foo/ /foo/ ~ ~\n",
	},

	// /dev/null
	{"echo foo >/dev/null", ""},
	{"cat </dev/null", ""},

	// time - real would be slow and flaky; see TestElapsedString
	{"{ time; } |& wc | tr -s ' '", " 4 6 42\n"},
	{"{ time echo -n; } |& wc | tr -s ' '", " 4 6 42\n"},
	{"{ time -p; } |& wc | tr -s ' '", " 3 6 29\n"},
	{"{ time -p echo -n; } |& wc | tr -s ' '", " 3 6 29\n"},

	// unsupported builtins — remaining genuinely-unsupported names route to
	// per-name hints via the default arm. Exhaustive coverage lives in
	// TestUnsupportedHints (unexported_test.go); these spot-check one to
	// catch a regression in the dispatcher's default arm.
	{
		"newgrp staff",
		"newgrp: not supported in this shell — group switching is not supported; switch groups in the parent process (e.g. with sudo -g)\nexit status 2 #JUSTERR",
	},

	// coproc: pipes are real, fd numbers are real, and `<&${CO[0]}` /
	// `>&${CO[1]}` go through fdTable. Without the fdTable lookup the
	// redirect layer would reject any numeric fd >= 3 with "bad fd number".
	// See docs/plan-punted-builtins.md for the numbered-fd refactor scope.
	{
		"coproc CO { read line; echo got=$line; }; echo hi >&${CO[1]}; read out <&${CO[0]}; echo $out",
		"got=hi\n",
	},

	// numbered fds (Phase 2): persistent assignment via exec, scoped via
	// plain redirect, dup forms with N >= 3 on either side. See
	// docs/plan-punted-builtins.md.
	{"echo a >&3", "3: Bad file descriptor\nexit status 1 #JUSTERR"},
	{"exec 3>f; echo a >&3; echo b >&3; cat f", "a\nb\n"},
	{"echo data >f; exec 3<f; read line <&3; echo got=$line", "got=data\n"},
	// scoped 3>f: echo writes to stdout (not fd 3); file f is created empty.
	{"echo a 3>f; cat f", "a\n"},

	// {var}> named-fd allocator (Phase 2): picks a fresh fd >= 10,
	// stores it in $var so the script can refer to it via `>&$var`.
	{"exec {fd}>f; echo fd=$fd; echo hi >&$fd; exec {fd}>&-; cat f", "fd=10\nhi\n"},
	{"exec {a}>f; exec {b}>g; echo $a $b", "10 11\n"},
	{"echo data >f; exec {fd}<f; read line <&$fd; echo got=$line", "got=data\n"},

	// fg: in-shell builtin waits on the bgProc.done channel and propagates
	// the captured exit status. Without a controlling TTY we don't try to
	// reattach stdio; see docs/plan-punted-builtins.md.
	{"fg", "fg: no job control\nexit status 1 #JUSTERR"},
	{"bg", "bg: no job control\nexit status 1 #JUSTERR"},
	{"fg %99", "fg: %99: no such job\nexit status 1 #JUSTERR"},
	{"(echo done) & fg", "done\n"},
	{"(exit 7) & fg; echo after=$?", "after=7\n"},

	// umask: per-Runner virtual mask. Reading is 4-digit octal; setting
	// updates only the runner field, not the process.
	{"umask 077; umask", "0077\n"},
	{"umask 022; umask", "0022\n"},
	{"umask 022; umask u=r+w; umask -S", "u=rw,g=rx,o=rx\n"},
	{"umask 022; umask u+w=r+x; umask -S", "u=rx,g=rx,o=rx\n"},
	{"umask 022; umask g+u,o+rwx-u; umask -S", "u=rwx,g=rwx,o=\n"},
	{"umask 022; umask +xwr; umask -S", "u=rwx,g=rwx,o=rwx\n"},
	{"umask 999", "umask: 999: octal number out of range\nexit status 1"},
	{"umask -i", "umask: -i: invalid option\numask: usage: umask [-p] [-S] [mode]\nexit status 2 #JUSTERR"},

	// logout from a non-login shell errors with the bash-compatible
	// message. The login-shell success path is covered in
	// TestRunnerLoginShell.
	{
		"logout",
		"logout: not login shell: use `exit'\nexit status 1 #JUSTERR",
	},

	// exec
	{"exec", ""},
	{
		"exec builtin echo foo",
		"\"builtin\": executable file not found in $PATH\nexit status 127 #JUSTERR",
	},
	{
		"exec $GOSH_PROG 'echo foo'; echo bar",
		"foo\n",
	},

	// read
	{
		"read </dev/null",
		"exit status 1",
	},
	{
		"read 1</dev/null",
		"exit status 1",
	},
	{
		"read -X",
		"read: invalid option \"-X\"\nexit status 2 #JUSTERR",
	},
	{
		"read -rX",
		"read: invalid option \"-X\"\nexit status 2 #JUSTERR",
	},
	{
		"read 0ab",
		"read: `0ab': not a valid identifier\nexit status 2 #JUSTERR",
	},
	{
		"read <<< foo; echo $REPLY",
		"foo\n",
	},
	{
		"read <<<'  a  b  c  '; echo \"$REPLY\"",
		"  a  b  c  \n",
	},
	{
		"read <<< 'y\nn\n'; echo $REPLY",
		"y\n",
	},
	{
		"read a_0 <<< foo; echo $a_0",
		"foo\n",
	},
	{
		"read 'a[2]' <<< foo; declare -p a",
		"declare -a a=([2]=\"foo\")\n",
	},
	{
		"declare -A A; read 'A[*]' <<< foo; declare -p A",
		"declare -A A=([\"*\"]=\"foo\" )\n",
	},
	{
		"read a b <<< 'foo  bar  baz  '; echo \"$a\"; echo \"$b\"",
		"foo\nbar  baz\n",
	},
	{
		"while read a; do echo $a; done <<< 'a\nb\nc'",
		"a\nb\nc\n",
	},
	{
		"while read a b; do echo -e \"$a\n$b\"; done <<< '1 2\n3'",
		"1\n2\n3\n\n",
	},
	{
		`read a <<< '\\'; echo "$a"`,
		"\\\n",
	},
	{
		`read a <<< '\a\b\c'; echo "$a"`,
		"abc\n",
	},
	{
		"read -r a b <<< '1\\\t2'; echo $a; echo $b;",
		"1\\\n2\n",
	},
	{
		"echo line\\\ncontinuation | while read a; do echo $a; done",
		"linecontinuation\n",
	},
	{
		"while read a; do echo $a; GOSH_CMD=print_ok $GOSH_PROG; done <<< 'a\nb\nc'",
		"a\nexec ok\nb\nexec ok\nc\nexec ok\n",
	},
	{
		"while read a; do echo $a; GOSH_CMD=print_ok $GOSH_PROG; done <<EOF\na\nb\nc\nEOF",
		"a\nexec ok\nb\nexec ok\nc\nexec ok\n",
	},
	{
		"echo file1 >f; echo file2 >>f; while read a; do echo $a; done <f",
		"file1\nfile2\n",
	},
	{
		"read -u 42 a",
		"read: 42: invalid file descriptor: Bad file descriptor\nexit status 2 #JUSTERR",
	},
	// TODO: our final exit status here isn't right.
	// {
	// 	"while read a; do echo $a; GOSH_CMD=print_fail $GOSH_PROG; done <<< 'a\nb\nc'",
	// 	"a\nexec fail\nb\nexec fail\nc\nexec fail\nexit status 1",
	// },
	{
		`read -r a <<< '\\'; echo "$a"`,
		"\\\\\n",
	},
	{
		"read -r a <<< '\\a\\b\\c'; echo $a",
		"\\a\\b\\c\n",
	},
	{
		"IFS=: read a b c <<< '1:2:3'; echo $a; echo $b; echo $c",
		"1\n2\n3\n",
	},
	{
		"IFS=: read a b c <<< '1\\:2:3'; echo \"$a\"; echo $b; echo $c",
		"1:2\n3\n\n",
	},
	{
		"read -n 5 a <<< 'абвгдежзиклмноп '; echo -$a- ${#a}",
		"-абвгд- 5\n",
	},
	{
		"read -p",
		"read: -p: option requires an argument\nexit status 2 #JUSTERR",
	},
	{
		"read -X -p",
		"read: invalid option \"-X\"\nexit status 2 #JUSTERR",
	},
	{
		"read -p 'Display me as a prompt. Continue? (y/n) ' choice <<< 'y'; echo $choice",
		"Display me as a prompt. Continue? (y/n) y\n #IGNORE bash requires a terminal",
	},
	{
		"read -r -p 'Prompt and raw flag together: ' a <<< '\\a\\b\\c'; echo $a",
		"Prompt and raw flag together: \\a\\b\\c\n #IGNORE bash requires a terminal",
	},

	// read -t TIMEOUT
	{
		"read -t",
		"read: -t: option requires an argument\nexit status 2 #JUSTERR",
	},
	{
		"read -t bogus x",
		"read: bogus: invalid timeout specification\nexit status 1 #JUSTERR",
	},
	{
		"read -t -1 x",
		"read: -1: invalid timeout specification\nexit status 1 #JUSTERR",
	},
	{
		"read -t 5 x <<< hello; echo $x",
		"hello\n",
	},
	{
		"read -t 5 x </dev/null; echo $?",
		"1\n",
	},
	{
		"read -t 0.05 x < <(sleep 0.2); echo $?",
		"142\n",
	},

	// read -a
	{
		`echo "1 2 3" | { read -a arr; echo "${arr[0]} ${arr[1]} ${arr[2]}"; }`,
		"1 2 3\n",
	},
	{
		`echo "a b c" | { read -a arr; echo "${#arr[@]}"; }`,
		"3\n",
	},
	{
		`echo "" | { read -a arr; echo "${#arr[@]}"; }`,
		"0\n",
	},
	{
		`echo 'a\tb' | { read -ra arr; echo "${#arr[@]} ${arr[0]}"; }`,
		"1 a\\tb\n",
	},
	{
		`a=a; echo | (read a; echo -n "$a")`,
		"",
	},
	{
		`a=b; read a < /dev/null; echo -n "$a"`,
		"",
	},
	{
		"a=c; echo x | (read a; echo -n $a)",
		"x",
	},
	{
		"a=d; echo -n y | (read a; echo -n $a)",
		"y",
	},

	// getopts
	{
		"getopts",
		"getopts: usage: getopts optstring name [arg ...]\nexit status 2",
	},
	{
		"getopts a a:b",
		"getopts: `a:b': not a valid identifier\nexit status 2 #JUSTERR",
	},
	{
		"getopts abc opt -a; echo $opt; $optarg",
		"a\n",
	},
	{
		"getopts abc opt -z",
		"bashy: illegal option -- z\n #IGNORE",
	},
	{
		"getopts a: opt -a",
		"bashy: option requires an argument -- a\n #IGNORE",
	},
	{
		"getopts :abc opt -z; echo $opt; echo $OPTARG",
		"?\nz\n",
	},
	{
		"getopts :a: opt -a; echo $opt; echo $OPTARG",
		":\na\n",
	},
	{
		"getopts abc opt foo -a; echo $opt; echo $OPTIND",
		"?\n1\n",
	},
	{
		"getopts abc opt -a foo; echo $opt; echo $OPTIND",
		"a\n2\n",
	},
	{
		"OPTIND=3; getopts abc opt -a -b -c; echo $opt;",
		"c\n",
	},
	{
		"OPTIND=100; getopts abc opt -a -b -c; echo $opt;",
		"?\n",
	},
	{
		"OPTIND=foo; getopts abc opt -a -b -c; echo $opt;",
		"a\n",
	},
	{
		"while getopts ab:c opt -c -b arg -a foo; do echo $opt $OPTARG $OPTIND; done",
		"c 2\nb arg 4\na 5\n",
	},
	{
		"while getopts abc opt -ba -c foo; do echo $opt $OPTARG $OPTIND; done",
		"b 1\na 2\nc 3\n",
	},
	{
		"a() { while getopts abc: opt; do echo $opt $OPTARG; done }; a -a -b -c arg",
		"a\nb\nc arg\n",
	},
	// mapfile
	{
		"mapfile <<EOF\na\nb\nc\nEOF\n" + `for x in "${MAPFILE[@]}"; do echo "$x"; done`,
		"a\n\nb\n\nc\n\n",
	},
	{
		"mapfile -t <<EOF\na\nb\nc\nEOF\n" + `for x in "${MAPFILE[@]}"; do echo "$x"; done`,
		"a\nb\nc\n",
	},
	{
		"mapfile -u 42 A",
		"mapfile: 42: invalid file descriptor: Bad file descriptor\nexit status 2 #JUSTERR",
	},
	{
		"mapfile ''",
		"mapfile: empty array variable name\nexit status 1 #JUSTERR",
	},
	{
		"mapfile -t -d b <<EOF\nabc\nEOF\n" + `for x in "${MAPFILE[@]}"; do echo "$x"; done`,
		"a\nc\n\n",
	},
	{
		"mapfile -t butter <<EOF\na\nb\nc\nEOF\n" + `for x in "${butter[@]}"; do echo "$x"; done`,
		"a\nb\nc\n",
	},
}

var runTestsUnix = []runTest{
	{"[[ -n $PPID && $PPID -ge 0 ]]", ""}, // can be 0 if running as the init process

	// exec -a NAME CMD overrides argv[0] of the spawned process; the
	// shell does not return (so "unreached" is never printed).
	{
		`exec -a renamed /bin/sh -c 'printf "%s\n" "$0"'; echo unreached`,
		"renamed\n",
	},
	{
		`FOO=BAR; export FOO; exec -c /bin/sh -c 'printf "%s\n" "${FOO-unset}"'; echo unreached`,
		"unset\n",
	},
	{
		`exec -a`,
		"exec: -a: option requires an argument\nexit status 2 #JUSTERR",
	},
	{
		`exec -a foo`,
		"exec: -a requires a command to execute\nexit status 2 #JUSTERR",
	},
	{
		`enable -d notbuiltin`,
		"enable: notbuiltin: not a shell builtin\nexit status 1 #JUSTERR",
	},
	{
		`enable -d test`,
		"enable: test: not dynamically loaded\nexit status 1 #JUSTERR",
	},
	{
		`enable -f ./strmatch.so strmatch; enable -d strmatch`,
		"",
	},
	{
		`shopt -s expand_aliases; alias let='let --'; let '1 == 1'`,
		"",
	},
	{
		`alias '\$'=xx`,
		"alias: `\\$': invalid alias name\nexit status 1 #JUSTERR",
	},
	{
		`BASH_ALIASES['\$']=xx`,
		"`\\$': invalid alias name\nexit status 1 #JUSTERR",
	},
	{
		`exec -z echo hi`,
		"exec: invalid option \"-z\"\nexit status 2 #JUSTERR",
	},
	{
		// no root user on windows
		"[[ ~root == '~root' ]]",
		"exit status 1",
	},

	// windows does not support paths with '*'
	{
		"mkdir -p '*/a.z' 'b/a.z'; cd '*'; set -- *.z; echo $#",
		"1\n",
	},
	{
		"mkdir -p 'a-*/d'; test -d $PWD/a-*/*",
		"",
	},

	// no fifos on windows
	{
		"[ -p a ] && echo x; mkfifo a; [ -p a ] && echo y",
		"y\n",
	},
	{
		"[[ -p a ]] && echo x; mkfifo a; [[ -p a ]] && echo y",
		"y\n",
	},

	{"sh() { :; }; sh -c 'echo foo'", ""},
	{"sh() { :; }; command sh -c 'echo foo'", "foo\n"},

	// chmod is practically useless on Windows
	{
		"[ -x a ] && echo x; >a; chmod 0755 a; [ -x a ] && echo y",
		"y\n",
	},
	{
		"[[ -x a ]] && echo x; >a; chmod 0755 a; [[ -x a ]] && echo y",
		"y\n",
	},
	{
		">a; [ -k a ] && echo x; chmod +t a; [ -k a ] && echo y",
		"y\n",
	},
	{
		">a; [ -u a ] && echo x; chmod u+s a; [ -u a ] && echo y",
		"y\n",
	},
	{
		">a; [ -g a ] && echo x; chmod g+s a; [ -g a ] && echo y",
		"y\n",
	},
	{
		">a; [[ -k a ]] && echo x; chmod +t a; [[ -k a ]] && echo y",
		"y\n",
	},
	{
		">a; [[ -u a ]] && echo x; chmod u+s a; [[ -u a ]] && echo y",
		"y\n",
	},
	{
		">a; [[ -g a ]] && echo x; chmod g+s a; [[ -g a ]] && echo y",
		"y\n",
	},
	{
		`mkdir a; chmod 0100 a; cd a`,
		"",
	},
	// Note that these will succeed if we're root.
	{
		`mkdir a; chmod 0000 a; cd a`,
		"cd: a: Permission denied\nexit status 1 #JUSTERR",
	},
	{
		`mkdir a; chmod 0222 a; cd a`,
		"cd: a: Permission denied\nexit status 1 #JUSTERR",
	},
	{
		`mkdir a; chmod 0444 a; cd a`,
		"cd: a: Permission denied\nexit status 1 #JUSTERR",
	},
	{
		`mkdir a; chmod 0010 a; cd a`,
		"cd: a: Permission denied\nexit status 1 #JUSTERR",
	},
	{
		`mkdir a; chmod 0001 a; cd a`,
		"cd: a: Permission denied\nexit status 1 #JUSTERR",
	},
	{
		`unset UID`,
		"unset: UID: cannot unset: readonly variable\nexit status 1 #IGNORE",
	},
	{
		`unset -v BASH_LINENO BASH_SOURCE`,
		"unset: BASH_LINENO: cannot unset\nunset: BASH_SOURCE: cannot unset\nexit status 1 #JUSTERR",
	},
	{
		`test -n "$EUID" && echo OK`,
		"OK\n",
	},
	{
		`set EUID=newvalue; test EUID != newvalue && echo OK || echo EUID=$EUID`,
		"OK\n",
	},
	{
		`unset EUID`,
		"unset: EUID: cannot unset: readonly variable\nexit status 1 #IGNORE",
	},
	// GID is not set in bash
	{
		`unset GID`,
		"unset: GID: cannot unset: readonly variable\nexit status 1 #IGNORE",
	},
	{
		`[[ -z $GID ]] && echo "GID not set"`,
		"exit status 1 #JUSTERR #IGNORE",
	},

	// Unix-y PATH
	{
		"PATH=; bash -c 'echo foo'",
		"\"bash\": executable file not found in $PATH\nexit status 127 #JUSTERR",
	},
	{
		"cd /; sure/is/missing",
		"stat /sure/is/missing: no such file or directory\nexit status 127 #JUSTERR",
	},
	{
		"echo '#!/bin/sh\necho b' >a; chmod 0755 a; PATH=; a",
		"b\n",
	},
	{
		"mkdir c; cd c; echo '#!/bin/sh\necho b' >a; chmod 0755 a; PATH=; a",
		"b\n",
	},
	{
		"mkdir c; echo '#!/bin/sh\necho b' >c/a; chmod 0755 c/a; c/a",
		"b\n",
	},
	{
		"GOSH_CMD=lookpath $GOSH_PROG",
		"sh found\n",
	},

	// error strings which are too different on Windows
	{
		"echo foo >/shouldnotexist/file",
		"open /shouldnotexist/file: no such file or directory\nexit status 1 #JUSTERR",
	},
	{
		"set -e; echo foo >/shouldnotexist/file; echo foo",
		"open /shouldnotexist/file: no such file or directory\nexit status 1 #JUSTERR",
	},

	// process substitution; named pipes (fifos) are a TODO for windows
	{
		"sed 's/o/e/g' <(echo foo bar)",
		"fee bar\n",
	},
	{
		"cat <(echo foo) <(echo bar) <(echo baz)",
		"foo\nbar\nbaz\n",
	},
	{
		"cat <(cat <(echo nested))",
		"nested\n",
	},
	{
		"cat ${foo:-<(echo a)}",
		"a\n",
	},
	{
		// The tests here use "wait" because otherwise the parent may finish before
		// the subprocess has had time to process the input and print the result.
		"echo foo bar > >(sed 's/o/e/g'); wait",
		"fee bar\n",
	},
	{
		"echo foo bar | tee >(sed 's/o/e/g') >/dev/null; wait",
		"fee bar\n",
	},
	{
		"echo nested > >(cat > >(cat); wait); wait",
		"nested\n",
	},
	{
		"cat <(exit 0); wait $!; echo $?",
		"0\n",
	},
	{
		"cat <(exit 5); wait $!; echo $?",
		"5\n",
	},
	{
		// The reader here does not consume the named pipe.
		"test -e <(echo foo)",
		"",
	},
	// echo trace
	{
		`set -x; animals=("dog", "cat", "otter"); echo "hello ${animals[*]}"`,
		`+ animals=(dog, cat, otter)
+ echo 'hello dog, cat, otter'
hello dog, cat, otter
`,
	},
	{
		`set -x; s="always print a decimal point for %e, %E, %f, %F, %g and %G; do not remove trailing zeros for %g and %G"; echo "$s"`,
		`+ s='always print a decimal point for %e, %E, %f, %F, %g and %G; do not remove trailing zeros for %g and %G'
+ echo 'always print a decimal point for %e, %E, %f, %F, %g and %G; do not remove trailing zeros for %g and %G'
always print a decimal point for %e, %E, %f, %F, %g and %G; do not remove trailing zeros for %g and %G
`,
	},
	{
		`set -x
x=without; echo "$x"
x="double quote"; echo "$x"
x='single quote'; echo "$x"`,
		`+ x=without
+ echo without
without
+ x='double quote'
+ echo 'double quote'
double quote
+ x='single quote'
+ echo 'single quote'
single quote
`,
	},
	// for trace
	{
		`set -x
exec >/dev/null
echo "trace should go to stderr"`,
		`+ exec
+ echo 'trace should go to stderr'
`,
	},
	{
		`set -x
animals=(dog, cat, otter)
for i in ${animals[@]}
do
   echo "hello ${i}"
done
`,
		`+ animals=(dog, cat, otter)
+ for i in ${animals[@]}
+ echo 'hello dog,'
hello dog,
+ for i in ${animals[@]}
+ echo 'hello cat,'
hello cat,
+ for i in ${animals[@]}
+ echo 'hello otter'
hello otter
`,
	},
	{
		`set -x
loop() {
    for i do
        echo "something with $i"
    done
}
loop 1 2 3`,
		`+ loop 1 2 3
+ for i in "$@"
+ echo 'something with 1'
something with 1
+ for i in "$@"
+ echo 'something with 2'
something with 2
+ for i in "$@"
+ echo 'something with 3'
something with 3
`,
	},
	{
		`set -x; animals=(dog, cat, otter); for i in ${animals[@]}; do echo "hello ${i}"; done`,
		`+ animals=(dog, cat, otter)
+ for i in ${animals[@]}
+ echo 'hello dog,'
hello dog,
+ for i in ${animals[@]}
+ echo 'hello cat,'
hello cat,
+ for i in ${animals[@]}
+ echo 'hello otter'
hello otter
`,
	},
	{
		`set -x; a=x"y"$z b=(foo bar $none '')`,
		"+ a=xy\n+ b=(foo bar $none '')\n",
	},
	{
		`set -x; for i in a b; do echo $i; done`,
		`+ for i in a b
+ echo a
a
+ for i in a b
+ echo b
b
`,
	},
	{
		`set -x; for i in $none_a $none_b; do echo $i; done`,
		``,
	},
	// case trace
	{
		`set -x; pet=dog; case $pet in 'dog') echo "barks";; *) echo "unknown";; esac`,
		`+ pet=dog
+ case $pet in
+ echo barks
barks
`,
	},
	{
		`set -x
pet="dog"
case $pet in
  dog)
    echo "barks"
    ;;
  *)
    echo "unknown"
    ;;
esac`,
		`+ pet=dog
+ case $pet in
+ echo barks
barks
`,
	},
	// arithmetic
	{
		`set -x
a=$(( 4 + 5 )); echo $a
a=$((3+5)); echo $a`,
		`+ a=9
+ echo 9
9
+ a=8
+ echo 8
8
`,
	},
	{
		`set -x;
let a=5+4; echo $a
let "a = 5 + 4"; echo $a
let a++; echo $a`,
		`+ let a=5+4
+ echo 9
9
+ let 'a = 5 + 4'
+ echo 9
9
+ let a++
+ echo 10
10
`,
	},
	// functions
	{
		`set -x; function with_function () { echo 'hello, world'; }; with_function`,
		`+ with_function
+ echo 'hello, world'
hello, world
`,
	},
	{
		`set -x; without_function () { echo 'hello, world'; }; without_function`,
		`+ without_function
+ echo 'hello, world'
hello, world
`,
	},
	{
		// globbing wildcard as function name
		`@() { echo "$@"; }; @ lala; function +() { echo "$@"; }; + foo`,
		"lala\nfoo\n",
	},
	{
		`      @() { echo "$@"; }; @ lala;`,
		"lala\n",
	},
	{
		// globbing wildcard as function name but with space after the name
		`+ () { echo "$@"; }; + foo; @ () { echo "$@"; }; @ lala; ? () { echo "$@"; }; ? bar`,
		"foo\nlala\nbar\n",
	},
	// mapfile, no process substitution yet on Windows
	{
		`mapfile -t -d "" < <(printf "a\0b\n"); for x in "${MAPFILE[@]}"; do echo "$x"; done`,
		"a\nb\n\n",
	},
	// Windows does not support having a `\n` in a filename
	{
		`> $'bar\nbaz'; echo bar*baz`,
		"bar\nbaz\n",
	},
}

var runTestsWindows = []runTest{
	{"[[ -n $PPID || $PPID -gt 0 ]]", ""}, // os.Getppid can be 0 on windows
	{"cmd() { :; }; cmd /c 'echo foo'", ""},
	{"cmd() { :; }; command cmd /c 'echo foo'", "foo\r\n"},
	{
		"GOSH_CMD=lookpath $GOSH_PROG",
		"cmd found\n",
	},
	{
		"localCase=camel; LocalCase=pascal; echo $localcase",
		"pascal\n",
	},
	{
		// Matching the env var name set as a global
		// in a case sensitive way.
		"$ENV_PROG | grep -i '^mixedCase_interp'",
		"mixedCase_INTERP_GLOBAL=value\n",
	},
	{
		// Overwriting the env var set as a global
		// in a case insensitive way.
		"MIXEDCASE_interp_global=replaced; echo $MIXEDCASE_interp_GLOBAL",
		"replaced\n",
	},
	{
		"MIXEDCASE_interp_global=replaced; $ENV_PROG | grep -i '^mixedcase_interp'",
		"MIXEDCASE_interp_global=replaced\n",
	},
}

// These tests are specific to 64-bit architectures, and that's fine. We don't
// need to add explicit versions for 32-bit.
var runTests64bit = []runTest{
	{"printf %i,%u -3 -3", "-3,18446744073709551613"},
	{"printf %o -3", "1777777777777777777775"},
	{"printf %x -3", "fffffffffffffffd"},
}

func init() {
	if runtime.GOOS == "windows" {
		runTests = append(runTests, runTestsWindows...)
	} else { // Unix-y
		runTests = append(runTests, runTestsUnix...)
	}
	if bits.UintSize == 64 {
		runTests = append(runTests, runTests64bit...)
	}
}

// ln -s: wine doesn't implement symlinks; see https://bugs.winehq.org/show_bug.cgi?id=44948
// process substitutions are not supported on Windows
var skipOnWindows = regexp.MustCompile(`ln -s|<\(`)

// process substitutions seemflaky on mac; see https://github.com/mvdan/sh/issues/576
var skipOnMac = regexp.MustCompile(`>\(|<\(`)

func skipIfUnsupported(tb testing.TB, src string) {
	switch {
	case runtime.GOOS == "windows" && skipOnWindows.MatchString(src):
		tb.Skipf("skipping non-portable test on windows")
	case runtime.GOOS == "darwin" && skipOnMac.MatchString(src):
		tb.Skipf("skipping non-portable test on mac")
	case strings.Contains(src, "chmod u+s") && !supportsSetIDMode(os.ModeSetuid):
		tb.Skipf("skipping setuid mode test on filesystem without setuid support")
	case strings.Contains(src, "chmod g+s") && !supportsSetIDMode(os.ModeSetgid):
		tb.Skipf("skipping setgid mode test on filesystem without setgid support")
	}
}

func supportsSetIDMode(bit os.FileMode) bool {
	dir, err := os.MkdirTemp("", "sh-setid-test-*")
	if err != nil {
		return false
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "a")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		return false
	}
	if err := os.Chmod(path, 0o644|bit); err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode()&bit != 0
}

func TestRunnerRun(t *testing.T) {
	t.Parallel()

	p := syntax.NewParser()
	for _, c := range runTests {
		t.Run("", func(t *testing.T) {
			skipIfUnsupported(t, c.in)
			t.Logf("input: %q", c.in)

			// Parse first, as we reuse a single parser.
			file := parse(t, p, c.in)

			t.Parallel()

			tdir := t.TempDir()
			var cb concBuffer
			r, err := interp.New(interp.Dir(tdir), interp.StdIO(nil, &cb, &cb),
				// TODO: why does this make some tests hang?
				// interp.Env(expand.ListEnviron(append(os.Environ(),
				// 	"foo_NULL_BAR=foo\x00bar")...)),
				interp.ExecHandlers(testExecHandler),
			)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
			defer cancel()
			if err := r.Run(ctx, file); err != nil {
				cb.WriteString(err.Error())
			}

			// Some builtins like "pushd" can show absolute paths as part of error messages.
			// Allow a very simple search-and-replace for the equivalent to "$PWD/a".
			want := strings.ReplaceAll(c.want, "ABS_PATH_A", filepath.Join(tdir, "a"))

			if i := strings.Index(want, " #"); i >= 0 {
				want = want[:i]
			}
			if got := cb.String(); got != want {
				if len(got) > 200 {
					got = "…" + got[len(got)-200:]
				}
				t.Fatalf("wrong output in %q:\nwant: %q\ngot:  %q",
					c.in, want, got)
			}
		})
	}
}

func TestBashCompatPosixSpecialBuiltinFuncDeclInSubshell(t *testing.T) {
	src := "( set -o posix\n" +
		"break()\n" +
		"{\n" +
		"echo hi\n" +
		"}\n" +
		"echo after\n" +
		")\n"
	file, err := syntax.NewParser().Parse(strings.NewReader(src), "./func5.sub")
	qt.Assert(t, qt.IsNil(err))

	var cb bytes.Buffer
	r, err := interp.New(interp.StdIO(nil, &cb, &cb), interp.WithBashCompatErrors(true))
	qt.Assert(t, qt.IsNil(err))

	err = r.Run(context.Background(), file)
	qt.Assert(t, qt.ErrorMatches(err, "exit status 1"))
	qt.Assert(t, qt.Equals(cb.String(), "./func5.sub: line 7: `break': is a special builtin\n"))
}

func TestBashCompatMalformedLengthSubstitution(t *testing.T) {
	src := "echo ${#:}\n" +
		"echo ${#/}\n" +
		"echo ${#%}\n" +
		"echo ${#=}\n" +
		"echo ${#+}\n" +
		"echo ${#1xyz}\n" +
		"echo ${#:%}\n"
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), "./more-exp.tests")
	qt.Assert(t, qt.IsNil(err))

	var cb bytes.Buffer
	r, err := interp.New(
		interp.StdIO(nil, &cb, &cb),
		interp.WithBashCompatErrors(true),
		interp.WithBashSource([]byte(src)),
	)
	qt.Assert(t, qt.IsNil(err))

	err = r.Run(context.Background(), file)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(cb.String(),
		"./more-exp.tests: line 1: ${#:}: bad substitution\n"+
			"./more-exp.tests: line 2: ${#/}: bad substitution\n"+
			"./more-exp.tests: line 3: ${#%}: bad substitution\n"+
			"./more-exp.tests: line 4: ${#=}: bad substitution\n"+
			"./more-exp.tests: line 5: ${#+}: bad substitution\n"+
			"./more-exp.tests: line 6: ${#1xyz}: bad substitution\n"+
			"./more-exp.tests: line 7: #: %: arithmetic syntax error: operand expected (error token is \"%\")\n"))
}

func TestBashCompatExecInvalidOptionUsage(t *testing.T) {
	src := "exec -1</dev/null\n"
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), "./redir.tests")
	qt.Assert(t, qt.IsNil(err))

	var cb bytes.Buffer
	r, err := interp.New(
		interp.StdIO(nil, &cb, &cb),
		interp.WithBashCompatErrors(true),
		interp.WithBashSource([]byte(src)),
	)
	qt.Assert(t, qt.IsNil(err))

	err = r.Run(context.Background(), file)
	qt.Assert(t, qt.ErrorMatches(err, "exit status 2"))
	qt.Assert(t, qt.Equals(cb.String(),
		"./redir.tests: line 1: exec: -1: invalid option\n"+
			"exec: usage: exec [-cl] [-a name] [command [argument ...]] [redirection ...]\n"))
}

func readLines(hc interp.HandlerContext) ([][]byte, error) {
	bs, err := io.ReadAll(hc.Stdin)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "windows" {
		bs = bytes.ReplaceAll(bs, []byte("\r\n"), []byte("\n"))
	}
	bs = bytes.TrimSuffix(bs, []byte("\n"))
	return bytes.Split(bs, []byte("\n")), nil
}

func absPath(dir, path string) string {
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return filepath.Clean(path) // TODO: this clean is likely unnecessary
}

var testBuiltinsMap = map[string]func(interp.HandlerContext, []string) error{
	"cat": func(hc interp.HandlerContext, args []string) error {
		if len(args) == 0 {
			if hc.Stdin == nil || hc.Stdout == nil {
				return nil
			}
			_, err := io.Copy(hc.Stdout, hc.Stdin)
			return err
		}
		for _, arg := range args {
			path := absPath(hc.Dir, arg)
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			_, err = io.Copy(hc.Stdout, f)
			f.Close()
			if err != nil {
				return err
			}
		}
		return nil
	},
	"wc": func(hc interp.HandlerContext, args []string) error {
		bs, err := io.ReadAll(hc.Stdin)
		if err != nil {
			return err
		}
		if len(args) == 0 {
			fmt.Fprintf(hc.Stdout, "%7d", bytes.Count(bs, []byte("\n")))
			fmt.Fprintf(hc.Stdout, "%8d", len(bytes.Fields(bs)))
			fmt.Fprintf(hc.Stdout, "%8d\n", len(bs))
		} else if args[0] == "-c" {
			fmt.Fprintln(hc.Stdout, len(bs))
		} else if args[0] == "-l" {
			fmt.Fprintln(hc.Stdout, bytes.Count(bs, []byte("\n")))
		}
		return nil
	},
	"head": func(hc interp.HandlerContext, args []string) error {
		limit := 10
		if len(args) == 1 && strings.HasPrefix(args[0], "-") {
			if _, err := fmt.Sscanf(args[0], "-%d", &limit); err != nil {
				return err
			}
		} else if len(args) != 0 {
			return fmt.Errorf("unexpected arg: %q", args[0])
		}
		lines, err := readLines(hc)
		if err != nil {
			return err
		}
		for i, line := range lines {
			if i >= limit {
				break
			}
			fmt.Fprintf(hc.Stdout, "%s\n", line)
		}
		return nil
	},
	"sh": func(hc interp.HandlerContext, args []string) error {
		if len(args) != 2 || args[0] != "-c" {
			return fmt.Errorf("unexpected args: %q", args)
		}
		file, err := syntax.NewParser().Parse(strings.NewReader(args[1]), "")
		if err != nil {
			return err
		}
		r, err := interp.New(
			interp.Dir(hc.Dir),
			interp.StdIO(hc.Stdin, hc.Stdout, hc.Stderr),
		)
		if err != nil {
			return err
		}
		return r.Run(context.Background(), file)
	},
	"tr": func(hc interp.HandlerContext, args []string) error {
		if len(args) != 2 || len(args[1]) != 1 {
			return fmt.Errorf("usage: tr [-s -d] [character]")
		}
		squeeze := args[0] == "-s"
		char := args[1][0]
		bs, err := io.ReadAll(hc.Stdin)
		if err != nil {
			return err
		}
		for {
			i := bytes.IndexByte(bs, char)
			if i < 0 {
				hc.Stdout.Write(bs) // remaining
				break
			}
			hc.Stdout.Write(bs[:i]) // up to char
			bs = bs[i+1:]

			bs = bytes.TrimLeft(bs, string(char)) // remove repeats
			if squeeze {
				hc.Stdout.Write([]byte{char})
			}
		}
		return nil
	},
	"sort": func(hc interp.HandlerContext, args []string) error {
		lines, err := readLines(hc)
		if err != nil {
			return err
		}
		slices.SortFunc(lines, bytes.Compare)
		for _, line := range lines {
			fmt.Fprintf(hc.Stdout, "%s\n", line)
		}
		return nil
	},
	"grep": func(hc interp.HandlerContext, args []string) error {
		var rx *regexp.Regexp
		quiet := false
		caseInsensitive := false
		for _, arg := range args {
			if arg == "-q" {
				quiet = true
			} else if arg == "-i" {
				caseInsensitive = true
			} else if arg == "-E" {
			} else if rx == nil {
				if caseInsensitive {
					arg = "(?i)" + arg
				}
				rx = regexp.MustCompile(arg)
			} else {
				return fmt.Errorf("unexpected arg: %q", arg)
			}
		}
		lines, err := readLines(hc)
		if err != nil {
			return err
		}
		anyMatch := false
		for _, line := range lines {
			if rx.Match(line) {
				if quiet {
					return nil
				}
				anyMatch = true
				fmt.Fprintf(hc.Stdout, "%s\n", line)
			}
		}
		if !anyMatch {
			return interp.ExitStatus(1)
		}
		return nil
	},
	"sed": func(hc interp.HandlerContext, args []string) error {
		f := hc.Stdin
		switch len(args) {
		case 1:
		case 2:
			var err error
			f, err = os.Open(absPath(hc.Dir, args[1]))
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("usage: sed pattern [file]")
		}
		expr := args[0]
		if expr == "" || expr[0] != 's' {
			return fmt.Errorf("unimplemented")
		}
		sep := expr[1]
		expr = expr[2:]
		from := expr[:strings.IndexByte(expr, sep)]
		expr = expr[len(from)+1:]
		to := expr[:strings.IndexByte(expr, sep)]
		bs, err := io.ReadAll(f)
		if err != nil {
			return err
		}
		rx := regexp.MustCompile(from)
		bs = rx.ReplaceAllLiteral(bs, []byte(to))
		_, err = hc.Stdout.Write(bs)
		return err
	},
	"mkdir": func(hc interp.HandlerContext, args []string) error {
		for _, arg := range args {
			if arg == "-p" {
				continue
			}
			path := absPath(hc.Dir, arg)
			if err := os.MkdirAll(path, 0o777); err != nil {
				return err
			}
		}
		return nil
	},
	"rm": func(hc interp.HandlerContext, args []string) error {
		for _, arg := range args {
			if arg == "-r" {
				continue
			}
			path := absPath(hc.Dir, arg)
			if err := os.RemoveAll(path); err != nil {
				return err
			}
		}
		return nil
	},
	"ln": func(hc interp.HandlerContext, args []string) error {
		symbolic := args[0] == "-s"
		if symbolic {
			args = args[1:]
		}
		oldname := absPath(hc.Dir, args[0])
		newname := absPath(hc.Dir, args[1])
		if symbolic {
			return os.Symlink(oldname, newname)
		}
		return os.Link(oldname, newname)
	},
	"touch": func(hc interp.HandlerContext, args []string) error {
		filenames := args // create all arguments as filenames

		newTime := time.Now()
		if args[0] == "-t" {
			if len(args) < 3 {
				return fmt.Errorf("usage: touch [-t [[CC]YY]MMDDhhmm[.SS]] file")
			}
			filenames = args[2:] // treat the rest of the args as filenames

			arg := args[1]
			if len(arg) > 15 {
				return fmt.Errorf("usage: touch [-t [[CC]YY]MMDDhhmm[.SS]] file")
			}
			s, err := time.Parse("200601021504.05", arg)
			if err != nil {
				return err
			}
			newTime = s
		}

		for _, arg := range filenames {
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("usage: touch [-t [[CC]YY]MMDDhhmm[.SS]] file")
			}
			path := absPath(hc.Dir, arg)
			// create the file if it does not exist
			f, err := os.OpenFile(path, os.O_CREATE, 0o666)
			if err != nil {
				return err
			}
			f.Close()
			// change the modification and access time
			if err := os.Chtimes(path, newTime, newTime); err != nil {
				return err
			}
		}
		return nil
	},
	"sleep": func(hc interp.HandlerContext, args []string) error {
		for _, arg := range args {
			// assume and default unit to be in seconds
			d, err := time.ParseDuration(fmt.Sprintf("%ss", arg))
			if err != nil {
				return err
			}
			time.Sleep(d)
		}
		return nil
	},
}

func testExecHandler(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		if fn := testBuiltinsMap[args[0]]; fn != nil {
			return fn(interp.HandlerCtx(ctx), args[1:])
		}
		return next(ctx, args)
	}
}

// Same as the syntax package.
var requireShells = os.Getenv("REQUIRE_SHELLS") == "1"

func TestRunnerRunConfirm(t *testing.T) {
	if testing.Short() {
		t.Skip("calling bash is slow")
	}
	if !hasBash53 {
		if requireShells {
			t.Fatal("bash 5.3 required to run")
		} else {
			t.Skip("bash 5.3 required to run")
		}
	}
	t.Parallel()

	if runtime.GOOS == "windows" {
		// For example, it seems to treat environment variables as
		// case-sensitive, which isn't how Windows works.
		t.Skip("bash on Windows emulates Unix-y behavior")
	}
	for _, c := range runTests {
		t.Run("", func(t *testing.T) {
			if strings.Contains(c.want, " #IGNORE") {
				return
			}
			skipIfUnsupported(t, c.in)
			t.Parallel()
			tdir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash")
			cmd.Dir = tdir
			cmd.Stdin = strings.NewReader(c.in)
			out, err := cmd.CombinedOutput()
			if strings.Contains(c.want, " #JUSTERR") {
				// bash sometimes exits with status code 0 and
				// stderr "bash: ..." for an error
				fauxErr := bytes.HasPrefix(out, []byte("bash:"))
				if err == nil && !fauxErr {
					t.Fatalf("wanted bash to error in %q", c.in)
				}
				return
			}
			got := string(out)
			if err != nil {
				got += err.Error()
			}
			if got != c.want {
				t.Fatalf("wrong bash output in %q:\nwant: %q\ngot:  %q",
					c.in, c.want, got)
			}
		})
	}
}

func TestRunnerOpts(t *testing.T) {
	t.Parallel()

	withPath := func(strs ...string) func(*interp.Runner) error {
		prefix := []string{
			"PATH=" + os.Getenv("PATH"),
			"ENV_PROG=" + os.Getenv("ENV_PROG"),
		}
		return interp.Env(expand.ListEnviron(append(prefix, strs...)...))
	}
	opts := func(list ...interp.RunnerOption) []interp.RunnerOption {
		return list
	}
	cases := []struct {
		opts     []interp.RunnerOption
		in, want string
	}{
		{
			nil,
			"$ENV_PROG | grep -i '^interp_global='",
			"INTERP_GLOBAL=value\n",
		},
		{
			opts(withPath()),
			"$ENV_PROG | grep -i '^interp_global='",
			"exit status 1",
		},
		{
			opts(withPath("INTERP_GLOBAL=bar")),
			"$ENV_PROG | grep -i '^interp_global='",
			"INTERP_GLOBAL=bar\n",
		},
		{
			opts(withPath("a=b")),
			"echo $a",
			"b\n",
		},
		{
			opts(withPath("A=b")),
			"$ENV_PROG | grep '^A='; echo $A",
			"A=b\nb\n",
		},
		{
			opts(withPath("A=b", "A=c")),
			"$ENV_PROG | grep '^A='; echo $A",
			"A=c\nc\n",
		},
		{
			opts(withPath("HOME=")),
			"echo $HOME",
			"\n",
		},
		{
			opts(withPath("PWD=foo")),
			"[[ $PWD == foo ]]",
			"exit status 1",
		},
		{
			opts(interp.Params("foo")),
			"echo $@",
			"foo\n",
		},
		{
			opts(interp.Params("-u", "--", "foo")),
			"echo $@; echo $unset",
			"foo\nunset: unbound variable\nexit status 1",
		},
		{
			opts(interp.Params("-u", "--", "foo")),
			"echo $@; echo ${unset:-default}",
			"foo\ndefault\n",
		},
		{
			opts(interp.Params("foo")),
			"set >/dev/null; echo $@",
			"foo\n",
		},
		{
			opts(interp.Params("foo")),
			"set -e; echo $@",
			"foo\n",
		},
		{
			opts(interp.Params("foo")),
			"set --; echo $@",
			"\n",
		},
		{
			opts(interp.Params("foo")),
			"set bar; echo $@",
			"bar\n",
		},
		{
			opts(interp.Env(expand.FuncEnviron(func(name string) string {
				if name == "foo" {
					return "bar"
				}
				return ""
			}))),
			"(echo $foo); echo x | echo $foo",
			"bar\nbar\n",
		},
	}
	p := syntax.NewParser()
	for _, c := range cases {
		t.Run("", func(t *testing.T) {
			skipIfUnsupported(t, c.in)
			file := parse(t, p, c.in)
			var cb concBuffer
			r, err := interp.New(append(c.opts,
				interp.StdIO(nil, &cb, &cb),
				interp.ExecHandlers(testExecHandler),
			)...)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
			defer cancel()
			if err := r.Run(ctx, file); err != nil {
				cb.WriteString(err.Error())
			}
			if got := cb.String(); got != c.want {
				t.Fatalf("wrong output in %q:\nwant: %q\ngot:  %q",
					c.in, c.want, got)
			}
		})
	}
}

func TestRunnerContext(t *testing.T) {
	t.Parallel()

	cases := []string{
		"",
		"while true; do true; done",
		"until false; do true; done",
		"sleep 1000",
		"while true; do true; done & wait",
		"sleep 1000 & wait",
		"(while true; do true; done)",
		"$(while true; do true; done)",
		"while true; do true; done | while true; do true; done",
	}
	p := syntax.NewParser()
	for _, in := range cases {
		t.Run("", func(t *testing.T) {
			file := parse(t, p, in)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			r, _ := interp.New()
			errChan := make(chan error)
			go func() {
				errChan <- r.Run(ctx, file)
			}()

			timeout := 500 * time.Millisecond
			select {
			case err := <-errChan:
				if err != nil && err != ctx.Err() {
					t.Fatal("Runner did not use ctx.Err()")
				}
			case <-time.After(timeout):
				t.Fatalf("program was not killed in %s", timeout)
			}
		})
	}
}

func TestCancelBlockedStdinRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		// TODO: Why is this? The [os.File.SetReadDeadline] docs seem to imply that it should work
		// across all major platforms, and the file polling  implementation seems to be
		// for all posix platforms including Windows.
		// Our previous logic and tests with muesli/cancelreader did not test an os.Pipe
		// on Windows either, so skipping here is not any worse.
		t.Skip("os.Pipe on windows appears to not support cancellable reads")
	}
	t.Parallel()

	p := syntax.NewParser()
	file := parse(t, p, "read x")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	// Make the linter happy, even though we deliberately wait for the timeout.
	defer cancel()

	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("Error calling os.Pipe: %v", err)
	}
	defer func() {
		stdinWrite.Close()
		stdinRead.Close()
	}()
	r, _ := interp.New(interp.StdIO(stdinRead, nil, nil))
	now := time.Now()
	errChan := make(chan error)
	go func() {
		errChan <- r.Run(ctx, file)
	}()

	timeout := 500 * time.Millisecond
	select {
	case err := <-errChan:
		if err == nil || err.Error() != "exit status 1" || ctx.Err() != context.DeadlineExceeded {
			t.Fatalf("'read x' did not timeout correctly; err: %v, ctx.Err(): %v; dur: %v",
				err, ctx.Err(), time.Since(now))
		}
	case <-time.After(timeout):
		t.Fatalf("program was not killed in %s", timeout)
	}
}

func TestRunnerAltNodes(t *testing.T) {
	t.Parallel()

	in := "echo foo"
	file := parse(t, nil, in)
	want := "foo\n"
	nodes := []syntax.Node{
		file,
		file.Stmts[0],
		file.Stmts[0].Cmd,
	}
	for _, node := range nodes {
		var cb concBuffer
		r, _ := interp.New(interp.StdIO(nil, &cb, &cb))
		ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
		defer cancel()
		if err := r.Run(ctx, node); err != nil {
			cb.WriteString(err.Error())
		}
		if got := cb.String(); got != want {
			t.Fatalf("wrong output in %q:\nwant: %q\ngot:  %q",
				in, want, got)
		}
	}
}

func TestRunnerDir(t *testing.T) {
	t.Parallel()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("Missing", func(t *testing.T) {
		_, err := interp.New(interp.Dir("missing"))
		if err == nil {
			t.Fatal("expected New to error when Dir is missing")
		}
	})
	t.Run("NotDir", func(t *testing.T) {
		_, err := interp.New(interp.Dir("interp_test.go"))
		if err == nil {
			t.Fatal("expected New to error when Dir is not a dir")
		}
	})
	t.Run("NotDirAbs", func(t *testing.T) {
		_, err := interp.New(interp.Dir(filepath.Join(wd, "interp_test.go")))
		if err == nil {
			t.Fatal("expected New to error when Dir is not a dir")
		}
	})
	t.Run("Relative", func(t *testing.T) {
		// On Windows, it's impossible to make a relative path from one
		// drive to another. Use the parent directory, as that's for
		// sure in the same drive as the current directory.
		rel := ".." + string(filepath.Separator)
		r, err := interp.New(interp.Dir(rel))
		if err != nil {
			t.Fatal(err)
		}
		if !filepath.IsAbs(r.Dir) {
			t.Errorf("Runner.Dir is not absolute")
		}
	})
	// Ensure that we treat symlinks and short paths properly, especially
	// with Dir and globbing.
	t.Run("SymlinkOrShortPath", func(t *testing.T) {
		tdir := t.TempDir()

		realDir := filepath.Join(tdir, "real-long-dir-name")
		realFile := filepath.Join(realDir, "realfile")

		if err := os.Mkdir(realDir, 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(realFile, []byte(""), 0o666); err != nil {
			t.Fatal(err)
		}

		var altDir string
		if runtime.GOOS == "windows" {
			short, err := shortPathName(realDir)
			if err != nil {
				t.Fatal(err)
			}
			altDir = short
			// We replace tdir later, and it might have been shortened.
			tdir = filepath.Dir(altDir)
		} else {
			altDir = filepath.Join(tdir, "symlink")
			if err := os.Symlink(realDir, altDir); err != nil {
				t.Fatal(err)
			}
		}

		var b bytes.Buffer
		r, err := interp.New(interp.Dir(altDir), interp.StdIO(nil, &b, &b))
		if err != nil {
			t.Fatal(err)
		}
		file := parse(t, nil, "echo $PWD $PWD/*")
		ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
		defer cancel()
		if err := r.Run(ctx, file); err != nil {
			t.Fatal(err)
		}
		got := b.String()
		got = strings.ReplaceAll(got, tdir, "")
		got = strings.TrimSpace(got)
		want := `/symlink /symlink/realfile`
		if runtime.GOOS == "windows" {
			want = `\\REAL.{4} \\REAL.{4}\\realfile`
		}
		if !regexp.MustCompile(want).MatchString(got) {
			t.Fatalf("\nwant regexp: %q\ngot: %q", want, got)
		}
	})
}

func TestRunnerIncremental(t *testing.T) {
	t.Parallel()

	file := parse(t, nil, "echo foo; false; echo bar; exit 0; echo baz")
	want := "foo\nbar\n"
	var b bytes.Buffer
	r, _ := interp.New(interp.StdIO(nil, &b, &b))
	ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
	defer cancel()
	for _, stmt := range file.Stmts {
		err := r.Run(ctx, stmt)
		if !errors.As(err, new(interp.ExitStatus)) && err != nil {
			// Keep track of unexpected errors.
			b.WriteString(err.Error())
		}
		if r.Exited() {
			break
		}
	}
	if got := b.String(); got != want {
		t.Fatalf("\nwant: %q\ngot:  %q", want, got)
	}
}

func TestRunnerResetFields(t *testing.T) {
	t.Parallel()

	tdir := t.TempDir()
	logPath := filepath.Join(tdir, "log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	r, _ := interp.New(
		interp.Params("-f", "--", "first", tdir, logPath),
		interp.Dir(tdir),
		interp.ExecHandlers(testExecHandler),
	)
	// Check that using option funcs and Runner fields directly is still
	// kept by Reset.
	interp.StdIO(nil, logFile, os.Stderr)(r)
	r.Env = expand.ListEnviron(append(os.Environ(), "GLOBAL=foo")...)

	file := parse(t, nil, `
# Params set 3 arguments
[[ $# -eq 3 ]] || exit 10
[[ $1 == "first" ]] || exit 11

# Params set the -f option (noglob)
[[ -o noglob ]] || exit 12

# $PWD was set via Dir, and should be equal to $2
[[ "$PWD" == "$2" ]] || exit 13

# stdout should go into the log file, which is at $3
echo line1
echo line2
[[ "$(wc -l <$3)" == "2" ]] || exit 14

# $GLOBAL was set directly via the Env field
[[ "$GLOBAL" == "foo" ]] || exit 15

# Change all of the above within the script. Reset should undo this.
set +f -- newargs
cd
exec >/dev/null 2>/dev/null
GLOBAL=
export GLOBAL=
`)
	ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
	defer cancel()
	for i := range 3 {
		if err := r.Run(ctx, file); err != nil {
			t.Fatalf("run number %d: %v", i, err)
		}
		r.Reset()
		// empty the log file too
		logFile.Truncate(0)
		logFile.Seek(0, io.SeekStart)
	}
}

func TestRunnerManyResets(t *testing.T) {
	t.Parallel()
	r, _ := interp.New()
	for range 5 {
		r.Reset()
	}
}

func TestRunnerAuditHandler(t *testing.T) {
	t.Parallel()
	src := `f(){ :; }; f; echo hi; ls /noexist 2>/dev/null; true`
	file, err := syntax.NewParser().Parse(strings.NewReader(src), "")
	if err != nil {
		t.Fatal(err)
	}
	var events []interp.AuditEvent
	hits := func(e interp.AuditEvent) { events = append(events, e) }
	var out bytes.Buffer
	r, err := interp.New(
		interp.StdIO(nil, &out, &out),
		interp.WithAuditHandler(hits),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
	defer cancel()
	if err := r.Run(ctx, file); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var sawLs, sawEcho, sawTrue bool
	for _, e := range events {
		if len(e.Args) > 0 && e.Args[0] == "ls" {
			sawLs = true
			if e.Kind != "exec" || e.IsBuiltin {
				t.Fatalf("ls event = %+v; want exec/non-builtin", e)
			}
		}
		if len(e.Args) > 0 && e.Args[0] == "echo" {
			sawEcho = true
			if e.Kind != "builtin" || !e.IsBuiltin {
				t.Fatalf("echo event = %+v; want builtin", e)
			}
			if e.EnvDigest == "" || e.CallStackHash == "" {
				t.Fatalf("echo event missing digests: %+v", e)
			}
		}
		if len(e.Args) > 0 && e.Args[0] == "true" {
			sawTrue = true
		}
	}
	if !sawLs || !sawEcho || !sawTrue {
		t.Fatalf("expected audit events for ls/echo/true, got events: %+v", events)
	}
}

func TestRunnerAuditLog(t *testing.T) {
	t.Parallel()
	file, err := syntax.NewParser().Parse(strings.NewReader("echo hi"), "")
	if err != nil {
		t.Fatal(err)
	}
	var out, log bytes.Buffer
	r, err := interp.New(
		interp.StdIO(nil, &out, &out),
		interp.WithDeterministic(1),
		interp.WithAuditLog(&log),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
	defer cancel()
	if err := r.Run(ctx, file); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var ev interp.AuditEvent
	if err := json.Unmarshal(bytes.TrimSpace(log.Bytes()), &ev); err != nil {
		t.Fatalf("audit log json: %v\n%s", err, log.String())
	}
	if got, want := ev.Kind, "builtin"; got != want {
		t.Fatalf("kind = %q, want %q", got, want)
	}
	if got, want := strings.Join(ev.Args, " "), "echo hi"; got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func TestRunnerJsonBuiltins(t *testing.T) {
	t.Parallel()
	src := `
foo=bar
arr=(zero one)
declare -A assoc=([k]=v)
f(){ echo ok; }
set --json
declare --json -p foo
declare --json -p arr
declare --json -p assoc
declare --json -F f
`
	file, err := syntax.NewParser().Parse(strings.NewReader(src), "")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	r, err := interp.New(interp.StdIO(nil, &out, &out))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
	defer cancel()
	if err := r.Run(ctx, file); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	dec := json.NewDecoder(strings.NewReader(out.String()))
	var setObj struct {
		Variables []map[string]any `json:"variables"`
	}
	if err := dec.Decode(&setObj); err != nil {
		t.Fatalf("set json: %v\n%s", err, out.String())
	}
	if len(setObj.Variables) == 0 {
		t.Fatalf("set json has no variables")
	}
	var foo, arr, assoc, fn map[string]any
	for _, dst := range []*map[string]any{&foo, &arr, &assoc, &fn} {
		if err := dec.Decode(dst); err != nil {
			t.Fatalf("decode json line: %v\n%s", err, out.String())
		}
	}
	if got, want := foo["name"], "foo"; got != want {
		t.Fatalf("foo name = %v, want %q", got, want)
	}
	if got, want := foo["kind"], "string"; got != want {
		t.Fatalf("foo kind = %v, want %q", got, want)
	}
	if got, want := foo["value"], "bar"; got != want {
		t.Fatalf("foo value = %v, want %q", got, want)
	}
	if got, want := arr["kind"], "indexed"; got != want {
		t.Fatalf("arr kind = %v, want %q", got, want)
	}
	if got, want := assoc["kind"], "associative"; got != want {
		t.Fatalf("assoc kind = %v, want %q", got, want)
	}
	if got, want := fn["name"], "f"; got != want {
		t.Fatalf("function name = %v, want %q", got, want)
	}
}

func TestRunnerStructuredErrors(t *testing.T) {
	t.Parallel()
	src := "f(){ return 1 2; }\nf\nmissing-command\n"
	file, err := syntax.NewParser().Parse(strings.NewReader(src), "script.sh")
	if err != nil {
		t.Fatal(err)
	}
	var events []interp.ErrorEvent
	var out bytes.Buffer
	r, err := interp.New(
		interp.StdIO(nil, &out, &out),
		interp.WithBashCompatErrors(true),
		interp.WithStructuredErrors(func(e interp.ErrorEvent) {
			events = append(events, e)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
	defer cancel()
	err = r.Run(ctx, file)
	if status, ok := interp.IsExitStatus(err); !ok || status != 127 {
		t.Fatalf("Run status = %v, %v; want 127", status, err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v; want 2 events", events)
	}
	if got, want := events[0].Kind, "builtin"; got != want {
		t.Fatalf("first kind = %q, want %q", got, want)
	}
	if got, want := events[0].Command, "return"; got != want {
		t.Fatalf("first command = %q, want %q", got, want)
	}
	if got, want := events[0].Function, "f"; got != want {
		t.Fatalf("first function = %q, want %q", got, want)
	}
	if got, want := events[0].ExitStatus, uint8(2); got != want {
		t.Fatalf("first exit status = %d, want %d", got, want)
	}
	if got, want := events[0].Message, "script.sh: line 1: return: too many arguments"; got != want {
		t.Fatalf("first message = %q, want %q", got, want)
	}
	if got, want := events[0].Pos.Line(), uint(1); got != want {
		t.Fatalf("first line = %d, want %d", got, want)
	}

	if got, want := events[1].Kind, "exec"; got != want {
		t.Fatalf("second kind = %q, want %q", got, want)
	}
	if got, want := events[1].Command, "missing-command"; got != want {
		t.Fatalf("second command = %q, want %q", got, want)
	}
	if got, want := events[1].ExitStatus, uint8(127); got != want {
		t.Fatalf("second exit status = %d, want %d", got, want)
	}
	if got, want := events[1].Message, "script.sh: line 3: missing-command: command not found"; got != want {
		t.Fatalf("second message = %q, want %q", got, want)
	}
}

func TestRunnerDeterministic(t *testing.T) {
	t.Parallel()
	run := func(seed int64) string {
		src := `echo $RANDOM $RANDOM $$ $SECONDS`
		file, _ := syntax.NewParser().Parse(strings.NewReader(src), "")
		var out bytes.Buffer
		r, _ := interp.New(
			interp.StdIO(nil, &out, &out),
			interp.WithDeterministic(seed),
		)
		ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
		defer cancel()
		if err := r.Run(ctx, file); err != nil {
			t.Fatalf("Run: %v", err)
		}
		return out.String()
	}
	a := run(42)
	b := run(42)
	if a != b {
		t.Fatalf("deterministic runs disagree: %q vs %q", a, b)
	}
	c := run(7)
	if a == c {
		t.Fatalf("different seeds produced same stream: %q", a)
	}
}

func TestRunnerDeterministicEnvAndSetOption(t *testing.T) {
	t.Parallel()
	src := `echo $RANDOM $$ $SECONDS; set -o deterministic; echo $RANDOM $$ $SECONDS`
	file, _ := syntax.NewParser().Parse(strings.NewReader(src), "")
	run := func() string {
		var out bytes.Buffer
		r, err := interp.New(
			interp.Env(expand.ListEnviron("BASHY_DETERMINISTIC=9")),
			interp.StdIO(nil, &out, &out),
		)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
		defer cancel()
		if err := r.Run(ctx, file); err != nil {
			t.Fatalf("Run: %v", err)
		}
		return out.String()
	}
	if a, b := run(), run(); a != b {
		t.Fatalf("deterministic env runs disagree: %q vs %q", a, b)
	}
}

func TestRunnerLoginShell(t *testing.T) {
	t.Parallel()

	// With WithLoginShell, `logout` should exit cleanly with the
	// caller-provided code.
	file, err := syntax.NewParser().Parse(strings.NewReader("logout 7"), "")
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	r, err := interp.New(interp.StdIO(nil, &b, &b), interp.WithLoginShell(true))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
	defer cancel()
	runErr := r.Run(ctx, file)
	if runErr == nil {
		t.Fatalf("expected exit status from `logout 7`, got nil")
	}
	var status interp.ExitStatus
	if !errors.As(runErr, &status) || uint8(status) != 7 {
		t.Fatalf("want exit status 7, got %v", runErr)
	}
	if got := b.String(); got != "" {
		t.Fatalf("unexpected stderr/stdout: %q", got)
	}
}

func TestRunnerFilename(t *testing.T) {
	t.Parallel()

	want := "f.sh\n"
	file, _ := syntax.NewParser().Parse(strings.NewReader("echo $0"), "f.sh")
	var b bytes.Buffer
	r, _ := interp.New(interp.StdIO(nil, &b, &b))
	ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
	defer cancel()
	if err := r.Run(ctx, file); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != want {
		t.Fatalf("\nwant: %q\ngot:  %q", want, got)
	}
}

func TestRunnerEnvNoModify(t *testing.T) {
	t.Parallel()

	env := expand.ListEnviron("one=1", "two=2")
	file := parse(t, nil, `echo -n "$one $two; "; one=x; unset two`)

	var b bytes.Buffer
	r, _ := interp.New(interp.Env(env), interp.StdIO(nil, &b, &b))
	ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
	defer cancel()
	for range 3 {
		r.Reset()
		err := r.Run(ctx, file)
		if err != nil {
			t.Fatal(err)
		}
	}

	want := "1 2; 1 2; 1 2; "
	if got := b.String(); got != want {
		t.Fatalf("\nwant: %q\ngot:  %q", want, got)
	}
}

func TestMalformedPathOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Skipping windows test on non-windows GOOS")
	}
	tdir := t.TempDir()
	t.Parallel()

	path := filepath.Join(tdir, "test.cmd")
	script := []byte("@echo foo")
	if err := os.WriteFile(path, script, 0o777); err != nil {
		t.Fatal(err)
	}

	// set PATH to c:\tmp\dir instead of C:\tmp\dir
	volume := filepath.VolumeName(tdir)
	pathList := strings.ToLower(volume) + tdir[len(volume):]

	file := parse(t, nil, "test.cmd")
	var cb concBuffer
	r, _ := interp.New(interp.Env(expand.ListEnviron("PATH="+pathList)), interp.StdIO(nil, &cb, &cb))
	ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
	defer cancel()
	if err := r.Run(ctx, file); err != nil {
		t.Fatal(err)
	}
	want := "foo\r\n"
	if got := cb.String(); got != want {
		t.Fatalf("wrong output:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestReadShouldNotPanicWithNilStdin(t *testing.T) {
	t.Parallel()

	r, err := interp.New()
	if err != nil {
		t.Fatal(err)
	}

	f := parse(t, nil, "read foobar")
	ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
	defer cancel()
	if err := r.Run(ctx, f); err == nil {
		t.Fatal("it should have returned an error")
	}
}

func TestRunnerVars(t *testing.T) {
	t.Parallel()

	r, err := interp.New()
	if err != nil {
		t.Fatal(err)
	}

	f := parse(t, nil, "foo=updated; BAR=new")
	ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
	defer cancel()
	if err := r.Run(ctx, f); err != nil {
		t.Fatal(err)
	}

	if want, got := "updated", r.Vars["foo"].String(); got != want {
		t.Fatalf("wrong output:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestRunnerSubshell(t *testing.T) {
	t.Parallel()

	r1, err := interp.New()
	if err != nil {
		t.Fatal(err)
	}

	r2 := r1.Subshell()
	f1 := parse(t, nil, "PARENT=foo")
	f2 := parse(t, nil, "CHILD=bar")

	ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
	defer cancel()
	if err := r1.Run(ctx, f1); err != nil {
		t.Fatal(err)
	}
	if err := r2.Run(ctx, f2); err != nil {
		t.Fatal(err)
	}

	if want, got := "foo", r1.Vars["PARENT"].String(); got != want {
		t.Fatalf("wrong output:\nwant: %q\ngot:  %q", want, got)
	}
	if want, got := "bar", r2.Vars["CHILD"].String(); got != want {
		t.Fatalf("wrong output:\nwant: %q\ngot:  %q", want, got)
	}

	r3 := r2.Subshell()
	f3 := parse(t, nil, "CHILD=modified")
	if err := r3.Run(ctx, f3); err != nil {
		t.Fatal(err)
	}
	if want, got := "bar", r2.Vars["CHILD"].String(); got != want {
		t.Fatalf("wrong output:\nwant: %q\ngot:  %q", want, got)
	}
	if want, got := "modified", r3.Vars["CHILD"].String(); got != want {
		t.Fatalf("wrong output:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestRunnerNonFileStdin(t *testing.T) {
	t.Parallel()

	var cb concBuffer
	r, err := interp.New(interp.StdIO(strings.NewReader("a\nb\nc\n"), &cb, &cb))
	if err != nil {
		t.Fatal(err)
	}
	file := parse(t, nil, "while read a; do echo $a; GOSH_CMD=print_ok $GOSH_PROG; done")
	ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
	defer cancel()
	if err := r.Run(ctx, file); err != nil {
		cb.WriteString(err.Error())
	}
	// TODO: just like with heredocs, the first print_ok call consumes all stdin.
	qt.Assert(t, qt.Equals(cb.String(), "a\nexec ok\nb\nexec ok\nc\nexec ok\n"))
}
