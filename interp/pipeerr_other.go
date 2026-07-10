//go:build !unix

package interp

func isPlatformBrokenPipeWriteErr(error) bool {
	return false
}
