//go:build windows

package interp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func preparePlatformExec(dir, execPath, diagnosticPath string, args []string) (string, []string, string) {
	data, err := os.ReadFile(diagnosticPath)
	if err != nil {
		return execPath, args, ""
	}
	interp, optarg, ok := parseShebang(data)
	if !ok {
		return execPath, args, ""
	}
	resolved := interp
	if shellPathAbs(interp) {
		resolved = shellPathToOS(dir, interp)
	} else if found, err := exec.LookPath(interp); err == nil {
		resolved = found
	}
	if _, err := os.Stat(resolved); err != nil {
		if _, extErr := os.Stat(resolved + ".exe"); extErr == nil {
			resolved += ".exe"
		} else if base := strings.TrimSuffix(strings.ToLower(filepath.Base(interp)), ".exe"); base == "bash" || base == "sh" {
			if self, selfErr := os.Executable(); selfErr == nil {
				resolved = self
			} else {
				return execPath, args, interp
			}
		} else {
			return execPath, args, interp
		}
	}
	if self, err := os.Executable(); err == nil {
		if a, aerr := os.Stat(resolved); aerr == nil {
			if b, berr := os.Stat(self); berr == nil && os.SameFile(a, b) {
				resolved = self
			}
		}
	}
	return resolved, shebangArgs(resolved, optarg, execPath, args), ""
}
