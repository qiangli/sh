// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

// Sprint: #118; Story: #52; Story-ID: d564bada90bb
//
// Original programs terminate themselves through their dependencies:
// os.Exit reports a status and syscall.Exec replaces the running image. Both
// end the process that owns the dependency session. The interpreter must
// report the program's own status rather than a generic bridge failure.

import (
	"context"
	"errors"
	"fmt"
)

// bashPPNativeExit reports that the dependency process terminated on its own
// rather than failing to answer a request. status is the process status; the
// wrapped error is retained so callers can still inspect the original cause.
type bashPPNativeExit struct {
	status int
	err    error
}

func (e *bashPPNativeExit) Error() string {
	return fmt.Sprintf("gosource: program exited with status %d", e.status)
}

func (e *bashPPNativeExit) Unwrap() error { return e.err }

// bashPPNativeExitStatus adopts a dependency-process termination as the
// program's own exit status. It reports whether err was such a termination.
func (r *Runner) bashPPNativeExitStatus(err error) bool {
	var exit *bashPPNativeExit
	if !errors.As(err, &exit) {
		return false
	}
	status := exit.status
	if status < 0 || status > 255 {
		return false
	}
	r.closeGoSourceBridge()
	r.exit = exitStatus{code: uint8(status), exiting: true}
	if status != 0 {
		// fatalExit keeps the status across the enclosing command and makes
		// every later exit.fatal on this unwind a no-op, so the program's own
		// status is not replaced by a bridge diagnostic.
		r.exit.fatalExit = true
		r.exit.err = ExitStatus(uint8(status))
	}
	return true
}

// bashPPNativeRequest issues one bridge request and converts a self-terminated
// dependency process into the program's exit status.
func (r *Runner) bashPPNativeRequest(ctx context.Context, req bashPPEvalRequest, q bashPPBridgeRequest) ([]bashPPBridgeValue, error) {
	values, err := req.Bridge.request(ctx, req, q)
	var callbackPanic *bashPPCallbackPanic
	if errors.As(err, &callbackPanic) {
		r.bashPPRaise(callbackPanic.value)
		return nil, errBashPPScalarInterrupted
	}
	if err != nil && r.bashPPNativeExitStatus(err) {
		if r.exit.code == 0 {
			// A successful self-termination has no diagnostic to report; the
			// interpreter simply stops with the program's own zero status.
			return nil, nil
		}
		return nil, errBashPPNativeExited
	}
	return values, err
}

// errBashPPNativeExited unwinds the interpreter after the program's status has
// already been recorded. It carries no diagnostic of its own.
var errBashPPNativeExited = errors.New("gosource: program exited")

type bashPPCallbackPanic struct{ value string }

func (p *bashPPCallbackPanic) Error() string { return "original callback panic: " + p.value }
