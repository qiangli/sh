//go:build !unix

package interp

func newBashPPCallbackMailbox() (*bashPPCallbackMailbox, error) { return nil, nil }
