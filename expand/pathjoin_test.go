package expand

import "testing"

// Glob matches are shell words: joined with `/` on every platform, never
// with the OS separator (bash prints `foo/bar` for `f*/bar` on Windows too).
func TestPathJoin2KeepsSlash(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{"", "x", "x"},
		{"foo", "bar", "foo/bar"},
		{"foo/", "bar", "foo/bar"},
		{`C:\`, "bar", `C:\bar`},
		{"C:/", "bar", "C:/bar"},
		{"/", "etc", "/etc"},
		{"lib/glob", "glob.o", "lib/glob/glob.o"},
	}
	for _, c := range cases {
		if got := pathJoin2(c.a, c.b); got != c.want {
			t.Errorf("pathJoin2(%q, %q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}
