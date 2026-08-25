// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

type issue7CommandResult struct {
	stdout string
	stderr string
	status uint8
}

// runIssue7Command follows Bashy's sh execution path: POSIX parsing and mode,
// plus the strict-POSIX semantic switch used by that host. The executable
// marker files satisfy strict mode's required PATH lookup for regular
// builtins without ever executing an external command.
func runIssue7Command(t *testing.T, src string, xsiEcho bool) issue7CommandResult {
	t.Helper()
	pathDir := t.TempDir()
	for _, name := range []string{"echo", "false", "printf", "true"} {
		path := filepath.Join(pathDir, name)
		if runtime.GOOS == "windows" {
			path += ".EXE"
		}
		if err := os.WriteFile(path, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangPOSIX)).Parse(strings.NewReader(src), "")
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	var stdout, stderr bytes.Buffer
	r, err := New(
		WithPosixMode(true),
		WithStrictPosix(true),
		StdIO(strings.NewReader("stdin sentinel\n"), &stdout, &stderr),
		Env(expand.ListEnviron(
			"PATH="+pathDir,
			"PATHEXT=.EXE",
			"LANG=C",
			"LC_ALL=C",
			"LC_CTYPE=C",
			"LC_MESSAGES=C",
			"NLSPATH=",
		)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if xsiEcho {
		opt, supported := r.bashOptByName("xpg_echo")
		if !supported || opt == nil {
			t.Fatal("xpg_echo option is unavailable")
		}
		*opt = true
	}
	err = r.Run(context.Background(), file)
	var status uint8
	if err != nil {
		var ok bool
		status, ok = IsExitStatus(err)
		if !ok {
			t.Fatalf("run %q: %v", src, err)
		}
	}
	return issue7CommandResult{stdout.String(), stderr.String(), status}
}

func TestAliasIssue7Interface(t *testing.T) {
	t.Run("no_operands_empty_table", func(t *testing.T) {
		got := runIssue7Command(t, "alias", false)
		if got != (issue7CommandResult{}) {
			t.Fatalf("alias: got %#v, want silent success", got)
		}
	})

	t.Run("define_redefine_query_and_subshell_effect", func(t *testing.T) {
		got := runIssue7Command(t, "alias mark='echo first'\nmark\nalias mark='echo second'\n(mark)\nalias mark", false)
		want := "first\nsecond\nmark='echo second'\n"
		if got.stdout != want || got.stderr != "" || got.status != 0 {
			t.Fatalf("alias lifecycle: got %#v, want stdout %q and success", got, want)
		}
	})

	t.Run("portable_special_names_and_trailing_blank_substitution", func(t *testing.T) {
		got := runIssue7Command(t, "alias '1=echo digit' '@=echo at' 'comma,=echo comma' 'lead=command ' 'nextcmd=echo chained'\n1\n@\ncomma,\nlead nextcmd", false)
		want := "digit\nat\ncomma\nchained\n"
		if got.stdout != want || got.stderr != "" || got.status != 0 {
			t.Fatalf("portable alias names: got %#v, want stdout %q and success", got, want)
		}
	})

	t.Run("no_operands_displays_all_definitions", func(t *testing.T) {
		got := runIssue7Command(t, "alias beta=two alpha=one\nalias", false)
		want := "alpha='one'\nbeta='two'\n"
		if got.stdout != want || got.stderr != "" || got.status != 0 {
			t.Fatalf("all aliases: got %#v, want stdout %q and success", got, want)
		}
	})

	t.Run("display_is_quoted_for_reinput", func(t *testing.T) {
		got := runIssue7Command(t, "alias quote=\"echo 'reinput ok'\"\nsaved=$(alias quote)\nunalias quote\neval \"alias $saved\"\nquote", false)
		if got.stdout != "reinput ok\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("reinput: got %#v", got)
		}
	})

	t.Run("missing_name_is_diagnostic_failure_after_other_output", func(t *testing.T) {
		got := runIssue7Command(t, "alias known=value\nalias known missing", false)
		if got.stdout != "known='value'\n" || got.stderr == "" || got.status == 0 {
			t.Fatalf("missing alias: got %#v", got)
		}
	})

	t.Run("subshell_changes_do_not_escape", func(t *testing.T) {
		got := runIssue7Command(t, "alias keep='echo outer'\n(unalias keep)\nkeep", false)
		if got.stdout != "outer\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("subshell isolation: got %#v", got)
		}
	})

	t.Run("definitions_do_not_cross_shell_invocations", func(t *testing.T) {
		first := runIssue7Command(t, "alias private=value", false)
		second := runIssue7Command(t, "alias private", false)
		if first != (issue7CommandResult{}) || second.stdout != "" || second.stderr == "" || second.status == 0 {
			t.Fatalf("invocation isolation: first=%#v second=%#v", first, second)
		}
	})

	t.Run("standard_output_error_is_failure", func(t *testing.T) {
		got := runIssue7Command(t, "alias shown=value\nalias shown >&-", false)
		if got.stdout != "" || !strings.Contains(got.stderr, "write error") || got.status == 0 {
			t.Fatalf("closed stdout: got %#v", got)
		}
	})

	t.Run("standard_input_is_not_used", func(t *testing.T) {
		got := runIssue7Command(t, "alias\nIFS= read -r line\nprintf '<%s>\\n' \"$line\"", false)
		if got.stdout != "<stdin sentinel>\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("stdin preservation: got %#v", got)
		}
	})
}

func TestEchoIssue7Interface(t *testing.T) {
	t.Run("no_operands_writes_newline", func(t *testing.T) {
		got := runIssue7Command(t, "echo", false)
		if got.stdout != "\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("echo: got %#v", got)
		}
	})

	t.Run("operands_single_space_and_option_terminator_is_string", func(t *testing.T) {
		got := runIssue7Command(t, "echo one '' three\necho --", false)
		if got.stdout != "one  three\n--\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("echo operands: got %#v", got)
		}
	})

	t.Run("no_options_dash_e_and_dash_E_are_operands", func(t *testing.T) {
		got := runIssue7Command(t, "echo -e plain\necho -E plain\necho -ne plain", false)
		want := "-e plain\n-E plain\n-ne plain\n"
		if got.stdout != want || got.stderr != "" || got.status != 0 {
			t.Fatalf("echo option-like operands: got %#v, want stdout %q", got, want)
		}
	})

	t.Run("implementation_defined_regions_choose_bsd_dash_n_and_literal_backslash", func(t *testing.T) {
		got := runIssue7Command(t, "echo -n prompt\necho 'a\\tb'", false)
		if got.stdout != "prompta\\tb\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("implementation-defined echo behavior: got %#v", got)
		}
	})

	t.Run("conditional_XSI_escape_mode", func(t *testing.T) {
		got := runIssue7Command(t, "echo '\\a\\b\\f\\n\\r\\t\\v\\\\\\0101'\necho 'before\\cafter' ignored", true)
		want := "\a\b\f\n\r\t\v\\A\nbefore"
		if got.stdout != want || got.stderr != "" || got.status != 0 {
			t.Fatalf("XSI echo mode: got %#v, want stdout %q", got, want)
		}
	})

	t.Run("conditional_XSI_dash_n_is_string", func(t *testing.T) {
		got := runIssue7Command(t, "echo -n xsi", true)
		if got.stdout != "-n xsi\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("XSI -n operand: got %#v", got)
		}
	})

	t.Run("standard_output_error_is_diagnostic_failure", func(t *testing.T) {
		got := runIssue7Command(t, "echo value >&-", false)
		if got.stdout != "" || !strings.Contains(got.stderr, "write error") || got.status == 0 {
			t.Fatalf("closed stdout: got %#v", got)
		}
	})

	t.Run("standard_input_is_not_used", func(t *testing.T) {
		got := runIssue7Command(t, "echo value\nIFS= read -r line\nprintf '<%s>\\n' \"$line\"", false)
		if got.stdout != "value\n<stdin sentinel>\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("stdin preservation: got %#v", got)
		}
	})
}

func TestFalseIssue7Interface(t *testing.T) {
	t.Run("silent_nonzero_status", func(t *testing.T) {
		got := runIssue7Command(t, "false", false)
		if got.stdout != "" || got.stderr != "" || got.status == 0 {
			t.Fatalf("false: got %#v, want silent non-zero status", got)
		}
	})
	t.Run("standard_input_and_environment_are_unused", func(t *testing.T) {
		got := runIssue7Command(t, "false\nIFS= read -r line\nprintf '<%s>\\n' \"$line\"", false)
		if got.stdout != "<stdin sentinel>\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("false stdin preservation: got %#v", got)
		}
	})
}

func TestTrueIssue7Interface(t *testing.T) {
	t.Run("silent_zero_status", func(t *testing.T) {
		got := runIssue7Command(t, "true", false)
		if got.stdout != "" || got.stderr != "" || got.status != 0 {
			t.Fatalf("true: got %#v, want silent zero status", got)
		}
	})
	t.Run("standard_input_and_environment_are_unused", func(t *testing.T) {
		got := runIssue7Command(t, "true\nIFS= read -r line\nprintf '<%s>\\n' \"$line\"", false)
		if got.stdout != "<stdin sentinel>\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("true stdin preservation: got %#v", got)
		}
	})
}

func TestUnaliasIssue7Interface(t *testing.T) {
	t.Run("remove_each_operand", func(t *testing.T) {
		got := runIssue7Command(t, "alias one=1 two=2\nunalias one two\nalias", false)
		if got != (issue7CommandResult{}) {
			t.Fatalf("unalias operands: got %#v, want empty table and success", got)
		}
	})

	t.Run("dash_a_removes_all", func(t *testing.T) {
		got := runIssue7Command(t, "alias one=1 two=2\nunalias -a\nalias", false)
		if got != (issue7CommandResult{}) {
			t.Fatalf("unalias -a: got %#v, want empty table and success", got)
		}
	})

	t.Run("option_terminator", func(t *testing.T) {
		got := runIssue7Command(t, "alias -- -dash=value\nunalias -- -dash\nalias", false)
		if got != (issue7CommandResult{}) {
			t.Fatalf("unalias --: got %#v, want empty table and success", got)
		}
	})

	t.Run("missing_operand_is_diagnostic_failure_and_other_names_are_removed", func(t *testing.T) {
		got := runIssue7Command(t, "alias gone=1 kept=2\nunalias gone missing\nprintf 'status=%s\\n' $?\nalias", false)
		want := "status=1\nkept='2'\n"
		if got.stdout != want || got.stderr == "" || got.status != 0 {
			t.Fatalf("missing unalias name: got %#v, want stdout %q", got, want)
		}
	})

	t.Run("required_name_operand", func(t *testing.T) {
		got := runIssue7Command(t, "unalias", false)
		if got.stdout != "" || got.stderr == "" || got.status == 0 {
			t.Fatalf("unalias without name: got %#v", got)
		}
	})

	t.Run("unrecognized_option", func(t *testing.T) {
		got := runIssue7Command(t, "unalias -z", false)
		if got.stdout != "" || got.stderr == "" || got.status == 0 {
			t.Fatalf("unalias invalid option: got %#v", got)
		}
	})

	t.Run("standard_input_is_not_used", func(t *testing.T) {
		got := runIssue7Command(t, "alias gone=1\nunalias gone\nIFS= read -r line\nprintf '<%s>\\n' \"$line\"", false)
		if got.stdout != "<stdin sentinel>\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("stdin preservation: got %#v", got)
		}
	})
}
