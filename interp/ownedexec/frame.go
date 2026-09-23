// Package ownedexec carries arguments that the native process launcher cannot
// represent to another Bashy-owned executable. It is deliberately inert for
// every other executable.
package ownedexec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
)

const Marker = "BASHY_OWNED_EXEC_FRAME"
const Sentinel = "--bashy-owned-exec-frame-v1"
const maxFrame = 2 << 20
const magic = "BASHYAE1"

type Frame struct {
	Args []string
	Env  []string // Entries omitted from the native child environment.
}

func Write(w io.Writer, f Frame) error {
	var body []byte
	appendStrings := func(ss []string) error {
		if len(ss) > maxFrame/4 {
			return errors.New("owned exec: too many strings")
		}
		body = binary.LittleEndian.AppendUint32(body, uint32(len(ss)))
		for _, s := range ss {
			if strings.IndexByte(s, 0) >= 0 {
				return errors.New("owned exec: NUL in string")
			}
			if len(s) > maxFrame || len(body)+4+len(s) > maxFrame {
				return errors.New("owned exec: payload too large")
			}
			body = binary.LittleEndian.AppendUint32(body, uint32(len(s)))
			body = append(body, s...)
		}
		return nil
	}
	if err := appendStrings(f.Args); err != nil {
		return err
	}
	if err := appendStrings(f.Env); err != nil {
		return err
	}
	if len(body) > maxFrame {
		return errors.New("owned exec: payload too large")
	}
	if _, err := io.WriteString(w, magic); err != nil {
		return err
	}
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(len(body)))
	if _, err := w.Write(n[:]); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

func Read(r io.Reader) (Frame, error) {
	var h [12]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return Frame{}, err
	}
	if string(h[:8]) != magic {
		return Frame{}, errors.New("owned exec: invalid frame")
	}
	n := binary.LittleEndian.Uint32(h[8:])
	if n > maxFrame {
		return Frame{}, errors.New("owned exec: payload too large")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return Frame{}, err
	}
	readStrings := func() ([]string, error) {
		if len(b) < 4 {
			return nil, io.ErrUnexpectedEOF
		}
		count := binary.LittleEndian.Uint32(b[:4])
		b = b[4:]
		if count > maxFrame/4 {
			return nil, errors.New("owned exec: too many strings")
		}
		out := make([]string, 0, count)
		for i := uint32(0); i < count; i++ {
			if len(b) < 4 {
				return nil, io.ErrUnexpectedEOF
			}
			n := binary.LittleEndian.Uint32(b[:4])
			b = b[4:]
			if n > uint32(len(b)) {
				return nil, io.ErrUnexpectedEOF
			}
			s := string(b[:n])
			b = b[n:]
			if strings.IndexByte(s, 0) >= 0 {
				return nil, errors.New("owned exec: NUL in string")
			}
			out = append(out, s)
		}
		return out, nil
	}
	args, err := readStrings()
	if err != nil {
		return Frame{}, err
	}
	env, err := readStrings()
	if err != nil {
		return Frame{}, err
	}
	if len(b) != 0 || len(args) == 0 {
		return Frame{}, errors.New("owned exec: invalid frame contents")
	}
	return Frame{Args: args, Env: env}, nil
}

// Adopt restores a frame before a Bashy-owned command parses its arguments.
// It is a no-op for ordinary process starts. The parent supplies only an
// inherited descriptor number or handle, never a filesystem pathname.
func Adopt() error {
	if len(os.Args) != 2 || os.Args[1] != Sentinel {
		return nil
	}
	value, ok := os.LookupEnv(Marker)
	if !ok {
		return errors.New("owned exec: missing descriptor")
	}
	_ = os.Unsetenv(Marker)
	fd, err := strconv.ParseUint(value, 10, 64)
	if err != nil || fd == 0 {
		return errors.New("owned exec: invalid descriptor")
	}
	f := os.NewFile(uintptr(fd), "bashy-owned-exec-frame")
	if f == nil {
		return errors.New("owned exec: missing descriptor")
	}
	frame, err := Read(f)
	_ = f.Close()
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		os.Clearenv()
	}
	for _, entry := range frame.Env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return fmt.Errorf("owned exec: invalid environment entry")
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	os.Args = frame.Args
	return nil
}
