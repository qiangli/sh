package man1_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPOSIXBuiltinManualPages(t *testing.T) {
	dir := "."
	master, err := os.ReadFile(filepath.Join(dir, "bashy-builtins.1"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(master)
	for _, section := range []string{".SH NAME", ".SH SYNOPSIS", ".SH DESCRIPTION", ".SH EXIT STATUS", ".SH STANDARDS"} {
		if !strings.Contains(body, section) {
			t.Errorf("master page is missing %s", section)
		}
	}
	for _, name := range []string{"alias", "bg", "cd", "command", "fc", "fg", "getopts", "jobs", "unalias"} {
		data, err := os.ReadFile(filepath.Join(dir, name+".1"))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if string(data) != ".so man1/bashy-builtins.1\n" {
			t.Errorf("%s does not resolve to the complete builtin manual", name)
		}
		if !strings.Contains(body, ".B "+name) && !strings.Contains(body, name+",") {
			t.Errorf("master page does not document %s", name)
		}
	}
}
