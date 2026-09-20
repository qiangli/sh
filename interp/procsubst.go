// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import "os"

// procSubstPipe is the platform seam behind process substitution: a
// filesystem-visible rendezvous path whose other end the shell (or a child
// process) opens by name. On Unix it is a FIFO in the runner's temp dir; on
// Windows it is a \\.\pipe\ named pipe. Both open calls block until the
// consumer side opens the path, matching Bash's FIFO semantics.
type procSubstPipe interface {
	// path is the name substituted into the command line.
	path() string
	// openWriter opens the substitution's write end (`<(cmd)`).
	openWriter() (*os.File, error)
	// openReader opens the substitution's read end (`>(cmd)`).
	openReader() (*os.File, error)
	// cleanup releases the rendezvous once the substitution is done. It is
	// safe to call after the opened end has been closed.
	cleanup()
}
