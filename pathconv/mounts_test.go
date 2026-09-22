// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package pathconv

import (
	"errors"
	"reflect"
	"runtime"
	"testing"
)

// testMounts is the table the bash 5.3 harness lays out on Windows:
// BASHY_ROOT=D:\w\root with usr\bin (no root\bin), etc, and TEMP on C:.
func testMounts(t *testing.T, extra ...Mount) *Mounts {
	t.Helper()
	old := dirExists
	dirExists = func(string) bool { return false }
	defer func() { dirExists = old }()
	return NewMounts(`D:\w\root`, extra, `C:\Users\me\AppData\Local\Temp`)
}

func TestMountsToOSFromOSRoundTrip(t *testing.T) {
	m := testMounts(t)

	tests := []struct {
		posix  string
		native string
	}{
		{`/`, `D:\w\root`},
		{`/usr`, `D:\w\root\usr`},
		{`/usr/bin/printf`, `D:\w\root\usr\bin\printf`},
		{`/bin`, `D:\w\root\usr\bin`},
		{`/bin/sh`, `D:\w\root\usr\bin\sh`},
		{`/etc/passwd`, `D:\w\root\etc\passwd`},
		{`/tmp`, `C:\Users\me\AppData\Local\Temp`},
		{`/tmp/x`, `C:\Users\me\AppData\Local\Temp\x`},
		{`/dev/null`, `NUL`},
		{`/home/x`, `D:\w\root\home\x`},
	}
	for _, tt := range tests {
		got, ok := m.ToOS(tt.posix)
		if !ok || got != tt.native {
			t.Errorf("Mounts.ToOS(%q) = (%q, %v), want (%q, true)", tt.posix, got, ok, tt.native)
		}
		if got := ToOSMountsMode(m, `C:\work`, tt.posix, true); got != tt.native {
			t.Errorf("ToOSMountsMode(%q) = %q, want %q", tt.posix, got, tt.native)
		}
		wantBack := tt.posix
		if tt.posix == "/bin" || tt.posix == "/bin/sh" {
			// /bin is an alias of /usr/bin on disk: like a merged-usr
			// symlink it resolves forward, and the physical spelling is
			// /usr/bin (what `cd /bin; pwd -P` prints on Linux).
			wantBack = "/usr" + tt.posix
		}
		back, ok := m.FromOS(tt.native)
		if !ok || back != wantBack {
			t.Errorf("Mounts.FromOS(%q) = (%q, %v), want (%q, true)", tt.native, back, ok, wantBack)
		}
		if got := FromOSMountsMode(m, tt.native, true); got != wantBack {
			t.Errorf("FromOSMountsMode(%q) = %q, want %q", tt.native, got, wantBack)
		}
	}
	// Relative and non-POSIX inputs never hit the table.
	for _, p := range []string{`rel`, `C:\x`, ``, `usr/bin`} {
		if got, ok := m.ToOS(p); ok {
			t.Errorf("Mounts.ToOS(%q) = %q, want no match", p, got)
		}
	}
	if got, ok := m.FromOS(`E:\elsewhere`); ok {
		t.Errorf("Mounts.FromOS(E:\\elsewhere) = %q, want no match", got)
	}
	var nilMounts *Mounts
	if _, ok := nilMounts.ToOS(`/bin`); ok {
		t.Error("nil Mounts.ToOS matched")
	}
	if _, ok := nilMounts.FromOS(`D:\w\root`); ok {
		t.Error("nil Mounts.FromOS matched")
	}
}

func TestMountsFromOSNativeSpelling(t *testing.T) {
	m := testMounts(t)

	tests := []struct {
		native string
		want   string
	}{
		{`d:/W/ROOT/usr`, `/usr`},         // case- and separator-insensitive
		{`D:\w\root\`, `/`},               // trailing separator
		{`D:\w\root\usr\bin`, `/usr/bin`}, // the /bin alias never maps back
		{`D:\w\root\usr\bin\sh.exe`, `/usr/bin/sh.exe`},
		{`D:\w\root\etc\passwd`, `/etc/passwd`},                       // longest native prefix
		{`D:\w\rootx\usr`, `/d/w/rootx/usr`},                          // not a component boundary
		{`nul`, `/dev/null`},                                          // device, case-insensitive
		{"C:\\Users\\me\\AppData\\Local\\Temp\\a\uf03ab", `/tmp/a:b`}, // decoded
		{`C:\`, `/c`},
		{`E:\x\`, `/e/x`},
	}
	for _, tt := range tests {
		if got := FromOSMountsMode(m, tt.native, true); got != tt.want {
			t.Errorf("FromOSMountsMode(%q) = %q, want %q", tt.native, got, tt.want)
		}
	}
}

func TestToOSMountsModePrecedence(t *testing.T) {
	m := testMounts(t, Mount{Posix: "/opt", Native: `E:\opt`}, Mount{Posix: "/opt/deep/", Native: `E:\deep`})

	tests := []struct {
		name string
		path string
		want string
	}{
		{"unc passthrough", `//./pipe/x`, `//./pipe/x`},
		{"device passthrough", `\\.\pipe\x`, `\\.\pipe\x`},
		{"native drive", `C:\x`, `C:\x`},
		{"native drive forward", `c:/x/y`, `c:\x\y`},
		{"dev null", `/dev/null`, `NUL`},
		{"explicit mount", `/opt/x`, `E:\opt\x`},
		{"longest explicit mount wins", `/opt/deep/x`, `E:\deep\x`},
		{"explicit mount is a component", `/optx`, `D:\w\root\optx`},
		{"msys drive beats root", `/c/Users/x`, `C:\Users\x`},
		{"msys bare drive beats root", `/c`, `C:\`},
		{"wsl mount beats root", `/mnt/d/x`, `D:\x`},
		{"root mount", `/foo/bar`, `D:\w\root\foo\bar`},
		{"root itself", `/`, `D:\w\root`},
		{"relative untouched", `bin/tool`, `bin/tool`},
		{"drive-relative backslash", `\foo`, `C:\foo`},
	}
	for _, tt := range tests {
		if got := ToOSMountsMode(m, `C:\work`, tt.path, true); got != tt.want {
			t.Errorf("%s: ToOSMountsMode(%q) = %q, want %q", tt.name, tt.path, got, tt.want)
		}
	}
	// A root of the form C:\ keeps its separator.
	root := NewMounts(`C:\`, nil, "")
	if got, _ := root.ToOS(`/x`); got != `C:\x` {
		t.Errorf("ToOS under C:\\ root = %q, want C:\\x", got)
	}
	if got, _ := root.ToOS(`/`); got != `C:\` {
		t.Errorf("ToOS of / under C:\\ root = %q, want C:\\", got)
	}
	if got := FromOSMountsMode(root, `C:\x`, true); got != `/x` {
		t.Errorf("FromOS under C:\\ root = %q, want /x", got)
	}
}

func TestNewMountsBinDir(t *testing.T) {
	old := dirExists
	defer func() { dirExists = old }()

	dirExists = func(p string) bool { return p == `D:\w\root\bin` }
	m := NewMounts(`D:\w\root\`, nil, "")
	if got, _ := m.ToOS(`/bin/sh`); got != `D:\w\root\bin\sh` {
		t.Errorf("/bin with root\\bin present = %q, want D:\\w\\root\\bin\\sh", got)
	}
	if got, _ := m.FromOS(`D:\w\root\bin\sh`); got != `/bin/sh` {
		t.Errorf("a real root\\bin maps back = %q, want /bin/sh", got)
	}
	if got, _ := m.FromOS(`D:\w\root\usr\bin\sh`); got != `/usr/bin/sh` {
		t.Errorf("usr\\bin with a real root\\bin = %q, want /usr/bin/sh", got)
	}
	if got, _ := m.ToOS(`/tmp`); got != `D:\w\root\tmp` {
		t.Errorf("/tmp without a temp dir falls to root = %q", got)
	}

	// An explicit entry overrides a built-in one, including the root.
	m = NewMounts(`D:\w\root`, []Mount{{Posix: "/bin/", Native: `E:\bin`}, {Posix: "/", Native: `E:\`}}, "")
	if got, _ := m.ToOS(`/bin/x`); got != `E:\bin\x` {
		t.Errorf("override /bin = %q", got)
	}
	if got, _ := m.ToOS(`/x`); got != `E:\x` || m.Root != `E:\` {
		t.Errorf("override / = %q, Root %q", got, m.Root)
	}
	// Entries that are not absolute POSIX paths are ignored.
	m = NewMounts("", []Mount{{Posix: "rel", Native: `E:\x`}, {Posix: "/ok", Native: ""}}, "")
	if _, ok := m.ToOS(`/ok`); ok {
		t.Error("empty native mount was installed")
	}
	if _, ok := m.ToOS(`/rel`); ok {
		t.Error("relative posix mount was installed")
	}
	if _, ok := m.ToOS(`/x`); ok {
		t.Error("root installed without a root directory")
	}
}

var errNotResolved = errors.New("not resolved")

func TestDiscover(t *testing.T) {
	oldExists, oldEval, oldTemp := dirExists, evalSymlinks, TempDir
	dirExists = func(string) bool { return false }
	evalSymlinks = func(p string) (string, error) {
		if p == `D:\RUNNER~1\root` {
			return `D:\runneradmin\root`, nil
		}
		return "", errNotResolved
	}
	TempDir = func() string { return `C:\Temp` }
	defer func() { dirExists, evalSymlinks, TempDir = oldExists, oldEval, oldTemp }()

	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	if m := Discover(getenv); m != nil {
		t.Fatalf("Discover without BASHY_ROOT = %+v, want nil", m)
	}

	env["BASHY_ROOT"] = `D:\RUNNER~1\root`
	m := Discover(getenv)
	if m == nil || m.Root != `D:\runneradmin\root` {
		t.Fatalf("Discover root = %+v, want canonical D:\\runneradmin\\root", m)
	}
	if got, _ := m.ToOS(`/usr/bin/printf`); got != `D:\runneradmin\root\usr\bin\printf` {
		t.Errorf("/usr/bin/printf = %q", got)
	}
	if got, _ := m.ToOS(`/tmp/x`); got != `C:\Temp\x` {
		t.Errorf("/tmp/x = %q", got)
	}
	if got, _ := m.ToOS(`/dev/null`); got != `NUL` {
		t.Errorf("/dev/null = %q", got)
	}

	env["BASHY_MOUNTS"] = `E:\opt=/opt; F:\tmp = /tmp ;bad;=/x;E:\y=rel`
	m = Discover(getenv)
	if got, _ := m.ToOS(`/opt/x`); got != `E:\opt\x` {
		t.Errorf("BASHY_MOUNTS /opt = %q", got)
	}
	if got, _ := m.ToOS(`/tmp/x`); got != `F:\tmp\x` {
		t.Errorf("BASHY_MOUNTS /tmp override = %q", got)
	}
	if got, _ := m.ToOS(`/x`); got != `D:\runneradmin\root\x` {
		t.Errorf("malformed BASHY_MOUNTS entry changed /x = %q", got)
	}
}

func TestCurrentMountsOffWindows(t *testing.T) {
	// Off Windows the process-wide table is always nil, so Unix conversions
	// cannot change; SetMounts must still be safe to call.
	old := currentMounts.Load()
	defer SetMounts(old)
	SetMounts(testMounts(t))
	if got := CurrentMounts(); got != nil && runtime.GOOS != "windows" {
		t.Errorf("CurrentMounts off Windows = %+v, want nil", got)
	}
}

func TestSplitPathList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		v       string
		windows bool
		want    []string
	}{
		{"/bin:/usr/bin", true, []string{"/bin", "/usr/bin"}},
		{`C:\a;D:\b`, true, []string{`C:\a`, `D:\b`}},
		{`c:/x:/bin`, true, []string{`c:/x`, `/bin`}},
		{`C:\Go\bin`, true, []string{`C:\Go\bin`}},
		{`/bin:C:\a:/usr/bin`, true, []string{`/bin`, `C:\a`, `/usr/bin`}},
		{`;C:\bin`, true, []string{``, `C:\bin`}},
		{`a:b`, true, []string{`a:b`}},
		{``, true, []string{}},
		{"/bin:/usr/bin", false, []string{"/bin", "/usr/bin"}},
		{`C:\a;D:\b`, false, []string{`C`, `\a;D`, `\b`}}, // off Windows ':' is the only separator
		{``, false, []string{}},
	}
	for _, tt := range tests {
		if got := SplitPathList(tt.v, tt.windows); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SplitPathList(%q, %v) = %q, want %q", tt.v, tt.windows, got, tt.want)
		}
	}
}

func TestNativePathListMounts(t *testing.T) {
	m := testMounts(t)

	tests := []struct {
		value string
		want  string
	}{
		{`/bin:/usr/bin`, `D:\w\root\usr\bin;D:\w\root\usr\bin`},
		{`/c/a:/usr/bin`, `C:\a;D:\w\root\usr\bin`},
		{`C:\Go\bin:/bin`, `C:\Go\bin;D:\w\root\usr\bin`},
		{`C:\a;/tmp`, `C:\a;C:\Users\me\AppData\Local\Temp`},
		{`/foo`, `D:\w\root\foo`},
		{`/tmp/a:b`, `C:\Users\me\AppData\Local\Temp\a;b`}, // a list, not a name with a colon
		{``, ``},
	}
	for _, tt := range tests {
		if got := NativePathListMounts(m, tt.value); got != tt.want {
			t.Errorf("NativePathListMounts(%q) = %q, want %q", tt.value, got, tt.want)
		}
	}
	// A nil table is the plain conversion: TMPDIR=/tmp stays as it is for
	// a child, a bare /foo too.
	for _, v := range []string{`/tmp`, `/foo`, `/bin`} {
		if got := NativePathMounts(nil, v); got != v {
			t.Errorf("NativePathMounts(nil, %q) = %q, want unchanged", v, got)
		}
	}
	if got := NativePathMounts(m, `/tmp`); got != `C:\Users\me\AppData\Local\Temp` {
		t.Errorf("NativePathMounts(/tmp) = %q", got)
	}
	if got := NativePathMounts(m, `/tmp/a?b`); got != "C:\\Users\\me\\AppData\\Local\\Temp\\a\uf03fb" {
		t.Errorf("NativePathMounts encodes = %q", got)
	}
}

func TestSpecialCharsRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want string
	}{
		{`a:b`, "a\uf03ab"},
		{`a*b?c"d<e>f|g`, "a\uf02ab\uf03fc\uf022d\uf03ce\uf03ef\uf07cg"},
		{`C:\x:y`, "C:\\x\uf03ay"},       // drive colon untouched
		{`c:/x?`, "c:/x\uf03f"},          // forward-slash drive form too
		{`C:`, `C:`},                     // bare drive
		{`\\.\pipe\a:b`, `\\.\pipe\a:b`}, // device prefix untouched
		{`//server/share/a:b`, `//server/share/a:b`},
		{`plain/path`, `plain/path`},
		{``, ``},
		{"ünï:cödé", "ünï\uf03acödé"},
	}
	for _, tt := range tests {
		got := EncodeSpecialMode(tt.path, true)
		if got != tt.want {
			t.Errorf("EncodeSpecialMode(%q) = %q, want %q", tt.path, got, tt.want)
		}
		if back := DecodeSpecialMode(got, true); back != tt.path {
			t.Errorf("DecodeSpecialMode(%q) = %q, want %q", got, back, tt.path)
		}
		if got := EncodeSpecialMode(tt.path, false); got != tt.path {
			t.Errorf("EncodeSpecialMode(%q, posix) = %q, want unchanged", tt.path, got)
		}
	}
	if got := DecodeSpecialMode("x\uf03ay", false); got != "x\uf03ay" {
		t.Errorf("DecodeSpecialMode posix = %q, want unchanged", got)
	}
}

func TestShellRelativeBackslashIsFilenameCharacter(t *testing.T) {
	got := EncodeShellRelativeMode(`a\*b`, true)
	want := "a\uf05c\uf02ab"
	if got != want {
		t.Fatalf("EncodeShellRelativeMode = %q, want %q", got, want)
	}
	if back := DecodeSpecialMode(got, true); back != `a\*b` {
		t.Fatalf("DecodeSpecialMode = %q", back)
	}
	if got := JoinAbsMode(`C:\work`, `a\*b`, true); got != "C:\\work\\a\uf05c\uf02ab" {
		t.Fatalf("JoinAbsMode = %q", got)
	}
	m := NewMounts(`C:\root`, nil, `C:\Temp`)
	if got := ToOSMountsMode(m, `C:\work`, `/tmp/a\*b`, true); got != "C:\\Temp\\a\uf05c\uf02ab" {
		t.Fatalf("ToOSMountsMode /tmp = %q", got)
	}
	if got := ToOSMountsMode(m, `C:\work`, `/dir/a\*b`, true); got != "C:\\root\\dir\\a\uf05c\uf02ab" {
		t.Fatalf("ToOSMountsMode mounted = %q", got)
	}
}

func TestToOSModeEncodesSpecialChars(t *testing.T) {
	// Not parallel: pins the TempDir hook.
	oldTempDir := TempDir
	TempDir = func() string { return `C:\Temp` }
	defer func() { TempDir = oldTempDir }()

	tests := []struct {
		dir  string
		path string
		want string
	}{
		{`C:\work`, `a:b`, "a\uf03ab"},
		{`C:\work`, `sub/a?b`, "sub/a\uf03fb"},
		{`C:\work`, `/tmp/a:b`, "C:\\Temp\\a\uf03ab"},
		{`C:\work`, `/c/x/a*b`, "C:\\x\\a\uf02ab"},
		{`D:\work`, `/x:y`, "D:\\x\uf03ay"},
		{`C:\work`, `C:\x|y`, "C:\\x\uf07cy"},
		{`C:\work`, `C:`, `C:`},
	}
	for _, tt := range tests {
		if got := ToOSMode(tt.dir, tt.path, true); got != tt.want {
			t.Errorf("ToOSMode(%q, %q) = %q, want %q", tt.dir, tt.path, got, tt.want)
		}
	}
	if got := JoinAbsMode(`C:\work`, `a:b`, true); got != "C:\\work\\a\uf03ab" {
		t.Errorf("JoinAbsMode relative a:b = %q", got)
	}
	if got := FromOSMode("C:\\work\\a\uf03ab", true); got != `/c/work/a:b` {
		t.Errorf("FromOSMode decodes = %q", got)
	}
}
