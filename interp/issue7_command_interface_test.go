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
// builtins without ever executing an external command; "utilx" is a
// non-builtin marker so the hash tests can exercise a genuine PATH search.
// extraEnv entries are appended to the fixed base environment.
func runIssue7Command(t *testing.T, src string, xsiEcho bool, extraEnv ...string) issue7CommandResult {
	t.Helper()
	pathDir := t.TempDir()
	for _, name := range []string{"echo", "false", "printf", "pwd", "true", "utilx"} {
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
	env := []string{
		"PATH=" + pathDir,
		"PATHEXT=.EXE",
		"LANG=C",
		"LC_ALL=C",
		"LC_CTYPE=C",
		"LC_MESSAGES=C",
		"NLSPATH=",
	}
	env = append(env, extraEnv...)
	r, err := New(
		WithPosixMode(true),
		WithStrictPosix(true),
		StdIO(strings.NewReader("stdin sentinel\n"), &stdout, &stderr),
		Env(expand.ListEnviron(env...)),
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

// issue7Dirs returns a symlink-free base directory plus two real
// subdirectories for the cd/pwd tests. The base is physically resolved so
// logical and physical paths only diverge where a test creates a symlink.
func issue7Dirs(t *testing.T) (base, dir1, dir2 string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir1 = filepath.Join(base, "dir1")
	dir2 = filepath.Join(base, "dir2")
	for _, dir := range []string{dir1, dir2} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return base, dir1, dir2
}

// issue7Symlink creates base/link pointing at target, skipping the test on
// platforms (or configurations) where symlinks are unavailable.
func issue7Symlink(t *testing.T, base, target string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink-based cd/pwd evidence requires POSIX symlinks")
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	return link
}

func TestCdIssue7Interface(t *testing.T) {
	t.Run("no_operand_uses_HOME", func(t *testing.T) {
		_, dir1, _ := issue7Dirs(t)
		got := runIssue7Command(t, "cd\nprintf '%s\\n' \"$PWD\"", false, "HOME="+dir1)
		if got.stdout != shellPathFromOS(dir1)+"\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("cd to HOME: got %#v", got)
		}
	})

	t.Run("no_operand_with_HOME_unset_is_diagnostic_failure", func(t *testing.T) {
		got := runIssue7Command(t, "unset HOME\ncd", false)
		if got.stdout != "" || !strings.Contains(got.stderr, "HOME not set") || got.status != 1 {
			t.Fatalf("cd without HOME: got %#v", got)
		}
	})

	t.Run("no_operand_with_empty_HOME_is_silent_noop", func(t *testing.T) {
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		got := runIssue7Command(t, "cd\nprintf '%s\\n' \"$PWD\"", false, "HOME=")
		if got.stdout != shellPathFromOS(wd)+"\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("cd with empty HOME: got %#v", got)
		}
	})

	t.Run("operand_updates_PWD_and_OLDPWD", func(t *testing.T) {
		_, dir1, dir2 := issue7Dirs(t)
		got := runIssue7Command(t, "cd \"$D1\"\ncd \"$D2\"\nprintf '%s\\n%s\\n' \"$PWD\" \"$OLDPWD\"",
			false, "D1="+dir1, "D2="+dir2)
		want := shellPathFromOS(dir2) + "\n" + shellPathFromOS(dir1) + "\n"
		if got.stdout != want || got.stderr != "" || got.status != 0 {
			t.Fatalf("cd operand: got %#v, want stdout %q", got, want)
		}
	})

	t.Run("dash_operand_returns_to_OLDPWD_and_prints_it", func(t *testing.T) {
		_, dir1, dir2 := issue7Dirs(t)
		got := runIssue7Command(t, "cd \"$D1\"\ncd \"$D2\"\ncd -\nprintf '%s\\n' \"$PWD\"",
			false, "D1="+dir1, "D2="+dir2)
		want := shellPathFromOS(dir1) + "\n" + shellPathFromOS(dir1) + "\n"
		if got.stdout != want || got.stderr != "" || got.status != 0 {
			t.Fatalf("cd -: got %#v, want stdout %q", got, want)
		}
	})

	t.Run("dash_operand_with_OLDPWD_unset_is_diagnostic_failure", func(t *testing.T) {
		got := runIssue7Command(t, "cd -", false)
		if got.stdout != "" || !strings.Contains(got.stderr, "OLDPWD not set") || got.status != 1 {
			t.Fatalf("cd - without OLDPWD: got %#v", got)
		}
	})

	t.Run("CDPATH_search_writes_new_directory_to_stdout", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("CDPATH is a colon-separated list; drive-letter paths cannot appear in it")
		}
		base, dir1, _ := issue7Dirs(t)
		got := runIssue7Command(t, "cd dir1\nprintf '%s\\n' \"$PWD\"", false, "CDPATH="+base)
		want := dir1 + "\n" + shellPathFromOS(dir1) + "\n"
		if got.stdout != want || got.stderr != "" || got.status != 0 {
			t.Fatalf("CDPATH cd: got %#v, want stdout %q", got, want)
		}
	})

	t.Run("null_directory_operand_is_diagnostic_failure", func(t *testing.T) {
		got := runIssue7Command(t, "cd ''", false)
		if got.stdout != "" || !strings.Contains(got.stderr, "null directory") || got.status != 1 {
			t.Fatalf("cd '': got %#v", got)
		}
	})

	t.Run("too_many_operands_is_usage_error", func(t *testing.T) {
		got := runIssue7Command(t, "cd one two", false)
		if got.stdout != "" || !strings.Contains(got.stderr, "too many arguments") || got.status != 2 {
			t.Fatalf("cd one two: got %#v", got)
		}
	})

	t.Run("failure_leaves_working_directory_unchanged", func(t *testing.T) {
		_, dir1, _ := issue7Dirs(t)
		got := runIssue7Command(t, "cd \"$D1\"\ncd \"$D1/missing\"\nst=$?\nprintf '%s %s\\n' \"$st\" \"$PWD\"",
			false, "D1="+dir1)
		want := "1 " + shellPathFromOS(dir1) + "\n"
		if got.stdout != want || got.stderr == "" || got.status != 0 {
			t.Fatalf("failed cd: got %#v, want stdout %q", got, want)
		}
	})

	t.Run("dash_L_default_keeps_logical_path_and_dotdot", func(t *testing.T) {
		base, dir1, _ := issue7Dirs(t)
		link := issue7Symlink(t, base, dir1)
		got := runIssue7Command(t, "cd \"$LINK\"\nprintf '%s\\n' \"$PWD\"\ncd ..\nprintf '%s\\n' \"$PWD\"",
			false, "LINK="+link)
		want := link + "\n" + base + "\n"
		if got.stdout != want || got.stderr != "" || got.status != 0 {
			t.Fatalf("logical cd: got %#v, want stdout %q", got, want)
		}
	})

	t.Run("dash_P_resolves_symlinks_in_PWD", func(t *testing.T) {
		base, dir1, _ := issue7Dirs(t)
		link := issue7Symlink(t, base, dir1)
		got := runIssue7Command(t, "cd -P \"$LINK\"\nprintf '%s\\n' \"$PWD\"", false, "LINK="+link)
		if got.stdout != dir1+"\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("physical cd: got %#v, want stdout %q", got, dir1+"\n")
		}
	})

	t.Run("dash_P_dotdot_follows_physical_parent_and_options_are_last_one_wins", func(t *testing.T) {
		base, dir1, _ := issue7Dirs(t)
		link := issue7Symlink(t, base, dir1)
		got := runIssue7Command(t,
			"cd \"$LINK\"\ncd -P ..\nprintf '%s\\n' \"$PWD\"\ncd -P -L \"$LINK\"\nprintf '%s\\n' \"$PWD\"",
			false, "LINK="+link)
		want := base + "\n" + link + "\n"
		if got.stdout != want || got.stderr != "" || got.status != 0 {
			t.Fatalf("physical dotdot / last-one-wins: got %#v, want stdout %q", got, want)
		}
	})

	t.Run("dash_output_write_error_fails_but_still_changes_directory", func(t *testing.T) {
		_, dir1, dir2 := issue7Dirs(t)
		src := "cd \"$D1\"\ncd \"$D2\"\ncd - >&-\nst=$?\nprintf '%s %s\\n' \"$st\" \"$PWD\""
		got := runIssue7Command(t, src, false, "D1="+dir1, "D2="+dir2)
		want := "1 " + shellPathFromOS(dir1) + "\n"
		if got.stdout != want || !strings.Contains(got.stderr, "cd: write error") || got.status != 0 {
			t.Fatalf("cd - write error: got %#v, want stdout %q", got, want)
		}
	})

	t.Run("standard_input_is_not_used", func(t *testing.T) {
		_, dir1, _ := issue7Dirs(t)
		got := runIssue7Command(t, "cd \"$D1\"\nIFS= read -r line\nprintf '<%s>\\n' \"$line\"",
			false, "D1="+dir1)
		if got.stdout != "<stdin sentinel>\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("stdin preservation: got %#v", got)
		}
	})
}

func TestCommandIssue7Interface(t *testing.T) {
	t.Run("no_operands_is_silent_success", func(t *testing.T) {
		got := runIssue7Command(t, "command\nprintf '%s\\n' \"$?\"", false)
		if got.stdout != "0\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("bare command: got %#v", got)
		}
	})

	t.Run("suppresses_function_lookup", func(t *testing.T) {
		_, dir1, dir2 := issue7Dirs(t)
		src := "cd() { printf 'fn %s\\n' \"$1\"; }\n" +
			"command cd \"$D1\"\nprintf '%s\\n' \"$PWD\"\n" +
			"cd \"$D2\"\nprintf '%s\\n' \"$PWD\""
		got := runIssue7Command(t, src, false, "D1="+dir1, "D2="+dir2)
		want := shellPathFromOS(dir1) + "\nfn " + dir2 + "\n" + shellPathFromOS(dir1) + "\n"
		if got.stdout != want || got.stderr != "" || got.status != 0 {
			t.Fatalf("function suppression: got %#v, want stdout %q", got, want)
		}
	})

	t.Run("special_builtin_loses_exit_on_error_property", func(t *testing.T) {
		src := "command eval '('\ncase \"$?\" in 0) printf 'zero\\n';; *) printf 'nonzero\\n';; esac\nprintf 'alive\\n'"
		got := runIssue7Command(t, src, false)
		if got.stdout != "nonzero\nalive\n" || got.stderr == "" || got.status != 0 {
			t.Fatalf("special builtin error survival: got %#v", got)
		}
	})

	t.Run("special_builtin_loses_assignment_persistence", func(t *testing.T) {
		src := "a=1 command :\nprintf '%s\\n' \"${a-unset}\"\nb=1 :\nprintf '%s\\n' \"${b-unset}\""
		got := runIssue7Command(t, src, false)
		if got.stdout != "unset\n1\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("assignment persistence: got %#v", got)
		}
	})

	t.Run("dash_v_regular_builtin_reports_pathname", func(t *testing.T) {
		src := "p=$(command -v true)\ncase \"$p\" in\ntrue) printf 'bare\\n';;\n*/true|*true.EXE) printf 'pathname\\n';;\n*) printf 'other\\n';;\nesac"
		got := runIssue7Command(t, src, false)
		if got.stdout != "pathname\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("command -v regular builtin: got %#v", got)
		}
	})

	t.Run("dash_v_intrinsic_special_and_keyword_report_name", func(t *testing.T) {
		got := runIssue7Command(t, "command -v cd\ncommand -v set\ncommand -v if", false)
		if got.stdout != "cd\nset\nif\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("command -v names: got %#v", got)
		}
	})

	t.Run("dash_v_alias_reports_reusable_definition", func(t *testing.T) {
		got := runIssue7Command(t, "alias ll='true -l'\ncommand -v ll", false)
		if got.stdout != "alias ll='true -l'\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("command -v alias: got %#v", got)
		}
	})

	t.Run("dash_v_not_found_is_silent_failure", func(t *testing.T) {
		got := runIssue7Command(t, "command -v no_such_i7", false)
		if got.stdout != "" || got.stderr != "" || got.status != 1 {
			t.Fatalf("command -v missing: got %#v", got)
		}
	})

	t.Run("dash_v_succeeds_when_any_name_is_found", func(t *testing.T) {
		src := "command -v true no_such_i7 >/dev/null\nprintf 's1=%s\\n' \"$?\"\n" +
			"command -v no_such_i7 true >/dev/null\nprintf 's2=%s\\n' \"$?\"\n" +
			"command -v no_such_i7 no_other_i7 >/dev/null\nprintf 's3=%s\\n' \"$?\""
		got := runIssue7Command(t, src, false)
		if got.stdout != "s1=0\ns2=0\ns3=1\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("command -v any-found status: got %#v", got)
		}
	})

	t.Run("dash_V_writes_description_with_pathname", func(t *testing.T) {
		got := runIssue7Command(t, "command -V true", false)
		if !strings.Contains(got.stdout, "true is a regular built-in at ") ||
			got.stderr != "" || got.status != 0 {
			t.Fatalf("command -V: got %#v", got)
		}
	})

	t.Run("dash_V_not_found_is_diagnostic_failure", func(t *testing.T) {
		got := runIssue7Command(t, "command -V no_such_i7", false)
		if got.stdout != "" || !strings.Contains(got.stderr, "not found") || got.status != 1 {
			t.Fatalf("command -V missing: got %#v", got)
		}
	})

	t.Run("dash_p_searches_standard_utilities_path", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the standard utilities path is a POSIX notion")
		}
		got := runIssue7Command(t, "command -p -v true", false)
		if got.stdout != "/bin/true\n" && got.stdout != "/usr/bin/true\n" {
			t.Fatalf("command -p -v: got %#v", got)
		}
		if got.stderr != "" || got.status != 0 {
			t.Fatalf("command -p -v: got %#v", got)
		}
	})

	t.Run("operand_not_found_exits_127", func(t *testing.T) {
		got := runIssue7Command(t, "probe() { printf 'fn\\n'; }\ncommand probe", false)
		if got.stdout != "" || got.stderr == "" || got.status != 127 {
			t.Fatalf("command missing operand: got %#v", got)
		}
	})

	t.Run("standard_input_is_not_used", func(t *testing.T) {
		got := runIssue7Command(t, "command :\nIFS= read -r line\nprintf '<%s>\\n' \"$line\"", false)
		if got.stdout != "<stdin sentinel>\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("stdin preservation: got %#v", got)
		}
	})
}

func TestGetoptsIssue7Interface(t *testing.T) {
	t.Run("OPTIND_is_initialized_to_one", func(t *testing.T) {
		got := runIssue7Command(t, "printf '%s\\n' \"$OPTIND\"", false)
		if got.stdout != "1\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("initial OPTIND: got %#v", got)
		}
	})

	t.Run("parses_options_and_option_arguments_from_args", func(t *testing.T) {
		src := "while getopts ab: opt -a -b val arg; do printf '%s %s\\n' \"$opt\" \"${OPTARG-unset}\"; done\n" +
			"printf 'end %s %s\\n' \"$opt\" \"$OPTIND\""
		got := runIssue7Command(t, src, false)
		want := "a unset\nb val\nend ? 4\n"
		if got.stdout != want || got.stderr != "" || got.status != 0 {
			t.Fatalf("getopts loop: got %#v, want stdout %q", got, want)
		}
	})

	t.Run("uses_positional_parameters_by_default", func(t *testing.T) {
		src := "set -- -a rest\ngetopts ab opt\nst=$?\nprintf '%s %s %s\\n' \"$st\" \"$opt\" \"$OPTIND\""
		got := runIssue7Command(t, src, false)
		if got.stdout != "0 a 2\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("getopts positional params: got %#v", got)
		}
	})

	t.Run("end_of_options_sets_name_to_question_mark_and_fails", func(t *testing.T) {
		src := "set -- operand\ngetopts ab opt\nst=$?\nprintf '%s %s %s\\n' \"$st\" \"$opt\" \"$OPTIND\""
		got := runIssue7Command(t, src, false)
		if got.stdout != "1 ? 1\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("getopts end of options: got %#v", got)
		}
	})

	t.Run("double_dash_terminates_and_is_consumed", func(t *testing.T) {
		src := "getopts ab opt -- x\nst=$?\nprintf '%s %s %s\\n' \"$st\" \"$opt\" \"$OPTIND\""
		got := runIssue7Command(t, src, false)
		if got.stdout != "1 ? 2\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("getopts --: got %#v", got)
		}
	})

	t.Run("verbose_unknown_option_diagnoses_and_unsets_OPTARG", func(t *testing.T) {
		src := "getopts ab opt -z\nst=$?\nprintf '%s %s %s\\n' \"$st\" \"$opt\" \"${OPTARG-unset}\""
		got := runIssue7Command(t, src, false)
		if got.stdout != "0 ? unset\n" || !strings.Contains(got.stderr, "illegal option -- z") || got.status != 0 {
			t.Fatalf("getopts verbose unknown: got %#v", got)
		}
	})

	t.Run("silent_unknown_option_sets_OPTARG_to_option_char", func(t *testing.T) {
		src := "getopts :ab opt -z\nst=$?\nprintf '%s %s %s\\n' \"$st\" \"$opt\" \"${OPTARG-unset}\""
		got := runIssue7Command(t, src, false)
		if got.stdout != "0 ? z\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("getopts silent unknown: got %#v", got)
		}
	})

	t.Run("verbose_missing_argument_diagnoses_with_question_mark", func(t *testing.T) {
		src := "getopts a: opt -a\nst=$?\nprintf '%s %s %s\\n' \"$st\" \"$opt\" \"${OPTARG-unset}\""
		got := runIssue7Command(t, src, false)
		if got.stdout != "0 ? unset\n" || !strings.Contains(got.stderr, "option requires an argument -- a") || got.status != 0 {
			t.Fatalf("getopts verbose missing arg: got %#v", got)
		}
	})

	t.Run("silent_missing_argument_sets_colon_and_OPTARG", func(t *testing.T) {
		src := "getopts :a: opt -a\nst=$?\nprintf '%s %s %s\\n' \"$st\" \"$opt\" \"${OPTARG-unset}\""
		got := runIssue7Command(t, src, false)
		if got.stdout != "0 : a\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("getopts silent missing arg: got %#v", got)
		}
	})

	t.Run("clustered_options_advance_within_one_argument", func(t *testing.T) {
		src := "getopts abc opt -abc\nprintf '%s %s\\n' \"$opt\" \"$OPTIND\"\n" +
			"getopts abc opt -abc\nprintf '%s %s\\n' \"$opt\" \"$OPTIND\"\n" +
			"getopts abc opt -abc\nprintf '%s %s\\n' \"$opt\" \"$OPTIND\"\n" +
			"getopts abc opt -abc\nst=$?\nprintf '%s %s %s\\n' \"$st\" \"$opt\" \"$OPTIND\""
		got := runIssue7Command(t, src, false)
		want := "a 1\nb 1\nc 2\n1 ? 2\n"
		if got.stdout != want || got.stderr != "" || got.status != 0 {
			t.Fatalf("getopts cluster: got %#v, want stdout %q", got, want)
		}
	})

	t.Run("attached_option_argument_is_recognized", func(t *testing.T) {
		src := "getopts a: opt -aVAL\nprintf '%s %s %s\\n' \"$opt\" \"$OPTARG\" \"$OPTIND\""
		got := runIssue7Command(t, src, false)
		if got.stdout != "a VAL 2\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("getopts attached arg: got %#v", got)
		}
	})

	t.Run("resetting_OPTIND_to_one_rescans", func(t *testing.T) {
		src := "set -- -a -b\ngetopts ab opt\ngetopts ab opt\nOPTIND=1\ngetopts ab opt\nst=$?\nprintf '%s %s %s\\n' \"$st\" \"$opt\" \"$OPTIND\""
		got := runIssue7Command(t, src, false)
		if got.stdout != "0 a 2\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("getopts rescan: got %#v", got)
		}
	})

	t.Run("invalid_name_operand_fails_but_advances_state", func(t *testing.T) {
		src := "set -- -a\ngetopts a 1bad\nst=$?\nprintf '%s %s\\n' \"$st\" \"$OPTIND\""
		got := runIssue7Command(t, src, false)
		if got.stdout != "1 2\n" || !strings.Contains(got.stderr, "not a valid identifier") || got.status != 0 {
			t.Fatalf("getopts bad name: got %#v", got)
		}
	})

	t.Run("missing_operands_are_usage_error", func(t *testing.T) {
		got := runIssue7Command(t, "getopts ab", false)
		if got.stdout != "" || !strings.Contains(got.stderr, "usage: getopts optstring name") || got.status != 2 {
			t.Fatalf("getopts usage: got %#v", got)
		}
	})
}

func TestHashIssue7Interface(t *testing.T) {
	t.Run("no_operands_with_empty_table_is_silent_success", func(t *testing.T) {
		got := runIssue7Command(t, "hash", false)
		if got != (issue7CommandResult{}) {
			t.Fatalf("hash: got %#v, want silent success", got)
		}
	})

	t.Run("operand_remembers_PATH_location_and_is_reported", func(t *testing.T) {
		src := "hash utilx\nst=$?\np=$(command -v utilx)\nh=$(hash -t utilx)\n" +
			"case \"$h\" in \"$p\") printf 'match %s\\n' \"$st\";; *) printf 'diff %s %s\\n' \"$h\" \"$p\";; esac\nhash"
		got := runIssue7Command(t, src, false)
		if !strings.HasPrefix(got.stdout, "match 0\nhits\tcommand\n") ||
			!strings.Contains(got.stdout, "utilx") || got.stderr != "" || got.status != 0 {
			t.Fatalf("hash operand: got %#v", got)
		}
	})

	t.Run("dash_r_forgets_all_remembered_locations", func(t *testing.T) {
		src := "hash utilx\nhash -r\nst=$?\nhash\nprintf 'r=%s\\n' \"$st\""
		got := runIssue7Command(t, src, false)
		if got.stdout != "r=0\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("hash -r: got %#v", got)
		}
	})

	t.Run("operand_not_found_is_diagnostic_failure", func(t *testing.T) {
		got := runIssue7Command(t, "hash no_such_i7", false)
		if got.stdout != "" || !strings.Contains(got.stderr, "not found") || got.status != 1 {
			t.Fatalf("hash missing: got %#v", got)
		}
	})

	t.Run("builtins_functions_and_slash_names_are_silently_skipped", func(t *testing.T) {
		src := "myfn() { :; }\nhash cd myfn ./no/such\nst=$?\nhash\nprintf 's=%s\\n' \"$st\""
		got := runIssue7Command(t, src, false)
		if got.stdout != "s=0\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("hash skip: got %#v", got)
		}
	})

	t.Run("dash_p_records_the_given_pathname", func(t *testing.T) {
		prog := filepath.Join(t.TempDir(), "myprog")
		if err := os.WriteFile(prog, nil, 0o755); err != nil {
			t.Fatal(err)
		}
		src := "hash -p \"$PROG\" alt\nh=$(hash -t alt)\n" +
			"case \"$h\" in \"$PROG\") printf 'match\\n';; *) printf 'diff %s\\n' \"$h\";; esac"
		got := runIssue7Command(t, src, false, "PROG="+prog)
		if got.stdout != "match\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("hash -p: got %#v", got)
		}
	})

	t.Run("dash_t_for_unhashed_name_is_diagnostic_failure", func(t *testing.T) {
		got := runIssue7Command(t, "hash -t utilx", false)
		if got.stdout != "" || !strings.Contains(got.stderr, "not found") || got.status != 1 {
			t.Fatalf("hash -t unhashed: got %#v", got)
		}
	})

	t.Run("invalid_option_is_usage_error", func(t *testing.T) {
		got := runIssue7Command(t, "hash -z", false)
		if got.stdout != "" || !strings.Contains(got.stderr, "invalid option") ||
			!strings.Contains(got.stderr, "usage:") || got.status != 2 {
			t.Fatalf("hash -z: got %#v", got)
		}
	})

	t.Run("subshell_changes_do_not_escape", func(t *testing.T) {
		src := "(hash utilx)\nhash -t utilx\nst=$?\nprintf 's=%s\\n' \"$st\""
		got := runIssue7Command(t, src, false)
		if got.stdout != "s=1\n" || !strings.Contains(got.stderr, "not found") || got.status != 0 {
			t.Fatalf("hash subshell isolation: got %#v", got)
		}
	})

	t.Run("standard_input_is_not_used", func(t *testing.T) {
		got := runIssue7Command(t, "hash utilx\nIFS= read -r line\nprintf '<%s>\\n' \"$line\"", false)
		if got.stdout != "<stdin sentinel>\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("stdin preservation: got %#v", got)
		}
	})
}

func TestPwdIssue7Interface(t *testing.T) {
	t.Run("writes_absolute_pathname_of_working_directory", func(t *testing.T) {
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		got := runIssue7Command(t, "pwd", false)
		if got.stdout != shellPathFromOS(wd)+"\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("pwd: got %#v, want stdout %q", got, shellPathFromOS(wd)+"\n")
		}
	})

	t.Run("dash_L_default_keeps_logical_path_dash_P_resolves_and_updates_PWD", func(t *testing.T) {
		base, dir1, _ := issue7Dirs(t)
		link := issue7Symlink(t, base, dir1)
		src := "cd \"$LINK\"\npwd\npwd -L\npwd -P\nprintf '%s\\n' \"$PWD\""
		got := runIssue7Command(t, src, false, "LINK="+link)
		want := link + "\n" + link + "\n" + dir1 + "\n" + dir1 + "\n"
		if got.stdout != want || got.stderr != "" || got.status != 0 {
			t.Fatalf("pwd modes: got %#v, want stdout %q", got, want)
		}
	})

	t.Run("last_of_repeated_L_P_options_wins", func(t *testing.T) {
		base, dir1, _ := issue7Dirs(t)
		link := issue7Symlink(t, base, dir1)
		got := runIssue7Command(t, "cd \"$LINK\"\npwd -P -L\npwd -L -P", false, "LINK="+link)
		want := link + "\n" + dir1 + "\n"
		if got.stdout != want || got.stderr != "" || got.status != 0 {
			t.Fatalf("pwd option precedence: got %#v, want stdout %q", got, want)
		}
	})

	t.Run("invalid_PWD_falls_back_to_physical_path", func(t *testing.T) {
		base, dir1, _ := issue7Dirs(t)
		link := issue7Symlink(t, base, dir1)
		got := runIssue7Command(t, "cd \"$LINK\"\nPWD=bogus\npwd", false, "LINK="+link)
		if got.stdout != dir1+"\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("pwd invalid PWD: got %#v, want stdout %q", got, dir1+"\n")
		}
	})

	t.Run("invalid_option_is_usage_error", func(t *testing.T) {
		got := runIssue7Command(t, "pwd -z", false)
		if got.stdout != "" || !strings.Contains(got.stderr, "invalid option") || got.status != 2 {
			t.Fatalf("pwd -z: got %#v", got)
		}
	})

	t.Run("operands_are_ignored_like_bash", func(t *testing.T) {
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		got := runIssue7Command(t, "pwd extra", false)
		if got.stdout != shellPathFromOS(wd)+"\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("pwd operand: got %#v", got)
		}
	})

	t.Run("standard_output_error_is_diagnostic_failure", func(t *testing.T) {
		got := runIssue7Command(t, "pwd >&-", false)
		if got.stdout != "" || !strings.Contains(got.stderr, "write error") || got.status == 0 {
			t.Fatalf("pwd closed stdout: got %#v", got)
		}
	})

	t.Run("standard_input_is_not_used", func(t *testing.T) {
		got := runIssue7Command(t, "pwd >/dev/null\nIFS= read -r line\nprintf '<%s>\\n' \"$line\"", false)
		if got.stdout != "<stdin sentinel>\n" || got.stderr != "" || got.status != 0 {
			t.Fatalf("stdin preservation: got %#v", got)
		}
	})
}
