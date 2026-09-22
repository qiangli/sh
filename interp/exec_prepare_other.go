//go:build !windows

package interp

func preparePlatformExec(_ string, execPath, _ string, args []string) (string, []string, string) {
	return execPath, args, ""
}
