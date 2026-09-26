// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

package pathconv

import (
	"reflect"
	"testing"
)

func TestNativeArgsIn(t *testing.T) {
	drives := DrivesOf("CD")
	in := []string{"-C", "/c/Users/x/repo", "--git-dir=/d/g", "/c", "/mnt/c/y", "/z/not-a-drive", "/help", "status", "-o/c/x", "https://a/b", "a/c/b"}
	want := []string{"-C", `C:\Users\x\repo`, `--git-dir=D:\g`, `C:\`, `C:\y`, "/z/not-a-drive", "/help", "status", "-o/c/x", "https://a/b", "a/c/b"}
	if got := NativeArgsIn(drives, in); !reflect.DeepEqual(got, want) {
		t.Fatalf("NativeArgsIn:\n got %q\nwant %q", got, want)
	}
	if in[1] != "/c/Users/x/repo" {
		t.Fatal("the input slice was modified")
	}
	same := []string{"status", "-s"}
	if got := NativeArgsIn(drives, same); &got[0] != &same[0] {
		t.Fatal("unchanged args should be returned as is")
	}
}
