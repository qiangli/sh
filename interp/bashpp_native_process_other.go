//go:build !unix

package interp

import "os/exec"

func bashPPNativeProcessGroup(cmd *exec.Cmd) {}
func bashPPNativeKill(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
