package interp

import (
	"bytes"
	"context"
	"fmt"
	"mvdan.cc/sh/v3/syntax"
	"reflect"
	"strings"
	"testing"
)

func TestScanUnsetOperandSourcesBoundaries(t *testing.T) {
	cases := []struct {
		name, line string
		want       []bool
	}{
		{"bare", "unset -v first second", []bool{false, false}},
		{"quotes", "unset 'first' \"second\" third", []bool{true, true, false}},
		{"truncated-escape", "unset name\\", []bool{false}},
		{"truncated-double-quote-escape", "unset \"name\\", []bool{true}},
		{"truncated-single-quote", "unset 'name\\", []bool{true}},
		{"continued-leading", "unset -v \\\n  \"a[$key]\" bare", []bool{true, false}},
		{"continued-between", "unset bare \\\n 'a[$key]'", []bool{false, true}},
		{"continued-command", "un\\\nset \\\n 'a[$key]'", []bool{true}},
		{"continued-in-word", "unset na\\\nme \"next\"", []bool{false, true}},
		{"continued-in-quotes", "unset \"na\\\nme\" bare", []bool{true, false}},
		{"literal-newline-in-quotes", "unset 'a\nb' bare\nnext", []bool{true, false}},
		{"stop-at-newline", "unset bare\n'not-an-operand'", []bool{false}},
		{"continuation-only", "unset \\\n", nil},
		{"escaped-backslash-before-newline", "unset name\\\\\n'not-an-operand'", []bool{false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []bool
			for _, operand := range scanUnsetOperandSources([]byte(tc.line)) {
				got = append(got, operand.quoted)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("quote classification=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestUnsetContinuedOperands(t *testing.T) {
	cases := []struct{ name, script, want string }{
		{"scalar", "a=1; b=2\nunset -v \\\n a \\\n b\nprintf '<%s><%s>\\n' \"${a-unset}\" \"${b-unset}\"", "<unset><unset>\n"},
		{"quoted-array-first", "declare -A a\nkey='literal value'\na[$key]=keep\nx=one\nunset -v \\\n \"a[$key]\" x\nprintf '%s:<%s>\\n' \"${#a[@]}\" \"${x-unset}\"", "0:<unset>\n"},
		{"quoted-array-second", "declare -A a\nkey='literal value'\na[$key]=keep\nx=one\nunset -v x \\\n \"a[$key]\"\nprintf '%s:<%s>\\n' \"${#a[@]}\" \"${x-unset}\"", "0:<unset>\n"},
		{"continued-command", "a=1\nun\\\nset \\\n a\nprintf '<%s>\\n' \"${a-unset}\"", "<unset>\n"},
		{"continued-quoted-word", "a=1\nunset \"a\\\n\"\nprintf '<%s>\\n' \"${a-unset}\"", "<unset>\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := syntax.NewParser().Parse(strings.NewReader(tc.script), "unset-test")
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			r, err := New(StdIO(nil, &out, &out), WithBashSource([]byte(tc.script)))
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Run(context.Background(), f); err != nil {
				t.Fatalf("run:%v output:%s", err, &out)
			}
			if out.String() != tc.want {
				t.Fatalf("output=%q want=%q", out.String(), tc.want)
			}
		})
	}
}

// Even a large script tail must stay borrowed: an unset with no operands needs
// no result allocation, and scanning stops at its first unescaped newline.
func TestUnsetOperandSourcesDoesNotCopyScriptTail(t *testing.T) {
	for _, size := range []int{0, 4 << 20} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			source := append([]byte("unset\n"), bytes.Repeat([]byte("x"), size)...)
			r := &Runner{bashSource: source}
			allocations := testing.AllocsPerRun(20, func() {
				if got := r.unsetOperandSources(syntax.NewPos(0, 1, 1)); len(got) != 0 {
					panic("scanned beyond logical line")
				}
			})
			if allocations != 0 {
				t.Fatalf("irrelevant tail %d bytes: %.0f allocations per scan, want zero", size, allocations)
			}
		})
	}
}
