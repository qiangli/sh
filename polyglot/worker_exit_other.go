//go:build !unix

package polyglot

import "os"

// workerDeath classifies how a worker ended. Off Unix kill's TerminateProcess
// is not distinguishable from the worker's own exit, so no death is reported.
func workerDeath(*os.ProcessState) workerExit { return workerExit{} }
