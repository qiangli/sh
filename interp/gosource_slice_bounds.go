package interp

import (
	"fmt"
	"mvdan.cc/sh/v3/syntax"
)

// A checked Go slice expression faults at runtime, so its bounds failure
// participates in panic/recover instead of becoming a shell diagnostic.
func (r *Runner) goSourceSliceBoundsPanic(expr *syntax.BashPPSliceExpr, low, high, max, limit, capacity int, slice bool) error {
	unit := "length"
	if slice {
		unit = "capacity"
	}
	var message string
	if expr.SecondColon.IsValid() {
		switch {
		case max < 0:
			message = fmt.Sprintf("[::%d]", max)
		case max > capacity:
			message = fmt.Sprintf("[::%d] with %s %d", max, unit, capacity)
		case high < 0:
			message = fmt.Sprintf("[:%d:]", high)
		case high > max:
			message = fmt.Sprintf("[:%d:%d]", high, max)
		case low < 0:
			message = fmt.Sprintf("[%d::]", low)
		default:
			message = fmt.Sprintf("[%d:%d:]", low, high)
		}
	} else {
		switch {
		case high < 0:
			message = fmt.Sprintf("[:%d]", high)
		case high > limit:
			message = fmt.Sprintf("[:%d] with %s %d", high, unit, limit)
		case low < 0:
			message = fmt.Sprintf("[%d:]", low)
		default:
			message = fmt.Sprintf("[%d:%d]", low, high)
		}
	}
	r.goSourceRuntimePanic("runtime error: slice bounds out of range " + message)
	return errBashPPScalarInterrupted
}
