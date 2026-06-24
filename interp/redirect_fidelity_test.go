// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp_test

import (
	"context"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func TestRunnerBash53RedirectCluster(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "dpl_in_and_dpl_out_equivalent_for_output_fd",
			in:   "echo one 1>&2\necho two 1<&2",
			want: "one\ntwo\n",
		},
		{
			name: "bare_less_amp_fd_duplication",
			in:   "echo foo51 > lessamp.txt\nexec 6< lessamp.txt\nread line <&6\necho \"[$line]\"",
			want: "[foo51]\n",
		},
		{
			name: "bare_redirection_resets_status",
			in:   "( exit 42 )\necho status=$?\n2>&1\necho status=$?",
			want: "status=42\nstatus=0\n",
		},
		{
			name: "two_ampersand_greater_parses_as_background",
			in:   "2&>1\necho status=$?",
			want: "status=127\n",
		},
		{
			name: "explicit_dpl_out_non_fd_word_opens_file",
			in:   "echo one 1>&redir-file\necho status=$?\ncat redir-file",
			want: "status=0\none\n",
		},
		{
			name: "fd_close_with_heredoc_restores_fd",
			in:   "exec 3> fd.txt\necho hello 3>&- << EOF\nEOF\necho world >&3\nexec 3>&-\ncat fd.txt",
			want: "hello\nworld\n",
		},
		{
			name: "closed_fd_self_dup_is_noop",
			in:   ": 3>&3\necho hello",
			want: "hello\n",
		},
		{
			name: "closed_fd_self_move_is_noop",
			in:   ": 3>&3-\necho hello",
			want: "hello\n",
		},
		{
			name: "write_fd_three_repeatedly",
			in:   "exec 3> fd3.txt\necho hello >&3\necho world >&3\nexec 3>&-\ncat fd3.txt",
			want: "hello\nworld\n",
		},
		{
			name: "write_fd_four_repeatedly",
			in:   "exec 4> fd4.txt\necho hello >&4\necho world >&4\nexec 4>&-\ncat fd4.txt",
			want: "hello\nworld\n",
		},
		{
			name: "exec_open_descriptor",
			in:   "exec 3>&1\necho hi 1>&3",
			want: "hi\n",
		},
		{
			name: "exec_open_multiple_descriptors",
			in:   "exec 3>&1\nexec 4>&1\necho three 1>&3\necho four 1>&4",
			want: "three\nfour\n",
		},
		{
			name: "heredoc_to_numbered_fd",
			in:   "read a 3<<EOF <&3\nabc\nEOF\necho [$a]",
			want: "[abc]\n",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			file, err := syntax.NewParser().Parse(strings.NewReader(tt.in), "")
			if err != nil {
				t.Fatal(err)
			}

			var cb concBuffer
			r, err := interp.New(
				interp.Dir(t.TempDir()),
				interp.StdIO(nil, &cb, &cb),
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

			if got := cb.String(); got != tt.want {
				t.Fatalf("wrong output in %q:\nwant: %q\ngot:  %q", tt.in, tt.want, got)
			}
		})
	}
}
