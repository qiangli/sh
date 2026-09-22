package expand

import "testing"

// Glob matches are shell words: joined with `/` on every platform, never
// with the OS separator (bash prints `foo/bar` for `f*/bar` on Windows too).
func TestPathJoin2KeepsSlash(t *testing.T) {
	cases := []struct {
		a, b, want string
		windows    bool
	}{
		{"", "x", "x", false},
		{"foo", "bar", "foo/bar", false},
		{"foo/", "bar", "foo/bar", false},
		{"/", "etc", "/etc", false},
		{"lib/glob", "glob.o", "lib/glob/glob.o", true},
		{`C:\`, "bar", `C:\bar`, true},
		{"C:/", "bar", "C:/bar", true},
		{`dir\`, "x", `dir\/x`, false}, // a backslash is not a separator off Windows
	}
	for _, c := range cases {
		if got := pathJoin2Mode(c.a, c.b, c.windows); got != c.want {
			t.Errorf("pathJoin2Mode(%q, %q, windows=%v) = %q, want %q", c.a, c.b, c.windows, got, c.want)
		}
	}
}

// A glob pattern splits on `/` only: a backslash is the pattern's escape
// (`"*"*` reaches the globber as `\**`), never a separator, on Windows too.
func TestPathSplitKeepsBackslashEscapes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`\**`, []string{`\**`}},
		{"foo/bar", []string{"foo", "bar"}},
		{"/c/Users/*", []string{"", "c", "Users", "*"}},
	}
	for _, c := range cases {
		got := pathSplit(c.in)
		if len(got) != len(c.want) {
			t.Errorf("pathSplit(%q) = %q, want %q", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("pathSplit(%q) = %q, want %q", c.in, got, c.want)
				break
			}
		}
	}
	if !globPathAbs("/tmp/x") || !globPathAbs("/c/x") || globPathAbs("rel/x") {
		t.Error("globPathAbs: a leading slash is absolute in the shell's spelling; a relative element is not")
	}
}
