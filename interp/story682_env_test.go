// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import "testing"

// TestNativeExecEnvKeepsShellSpelling pins the story 682 policy: a child is
// handed the values the script set, byte for byte. Only PATH — which the
// exec lookup walks — and the BASHYENV opt-ins are converted.
func TestNativeExecEnvKeepsShellSpelling(t *testing.T) {
	t.Parallel()

	m := story682Mounts()
	// varenv.tests: HOME=/a/b/c /bin/echo $HOME prints /a/b/c, although
	// /a/b/c is a real directory under the virtual root.
	env := []string{
		"HOME=/a/b/c",
		"TEMP=/tmp",
		"TMPDIR=/tmp",
		"GOPATH=/c/go",
		"USERPROFILE=/c/Users/me",
		"SYSTEMROOT=/c/Windows",
	}
	got := nativeExecEnvMountsMode(m, append([]string(nil), env...), true)
	for i := range env {
		if got[i] != env[i] {
			t.Errorf("%q was converted to %q", env[i], got[i])
		}
	}
	// The two exceptions still apply.
	env = []string{"BASHYENV=GOPATH/p", "GOPATH=/c/go", "PATH=/bin:/c/Go/bin"}
	want := []string{"BASHYENV=GOPATH/p", `GOPATH=C:\go`, `PATH=C:\Temp\bash53-1\root\usr\bin;C:\Go\bin`}
	got = nativeExecEnvMountsMode(m, append([]string(nil), env...), true)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
