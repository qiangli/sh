//go:build unix

package interp

import (
	"os"
	"syscall"
)

func newBashPPCallbackMailbox() (*bashPPCallbackMailbox, error) {
	f, err := os.CreateTemp("", "bashpp-callback-mailbox-*")
	if err != nil {
		return nil, err
	}
	path := f.Name()
	fail := func() {
		_ = f.Close()
		_ = os.Remove(path)
	}
	if err = f.Chmod(0o600); err != nil {
		fail()
		return nil, err
	}
	if err = f.Truncate(bashPPMailboxSize); err != nil {
		fail()
		return nil, err
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, bashPPMailboxSize, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		fail()
		return nil, err
	}
	return &bashPPCallbackMailbox{data: data, path: path, cleanup: func() error {
		err := syscall.Munmap(data)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if removeErr := os.Remove(path); err == nil && !os.IsNotExist(removeErr) {
			err = removeErr
		}
		return err
	}}, nil
}
