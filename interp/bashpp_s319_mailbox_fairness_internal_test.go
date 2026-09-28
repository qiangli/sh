package interp

// Sprint: #319; Story: #1088; Story-ID: 00446a7bd51a

import (
	"runtime"
	"testing"
)

// A callback-capable request polls the shared-memory mailbox while native
// testing runs callback bodies in independent goroutines. The empty-mailbox
// path must yield the processor so those bodies can produce the next request
// or reply; a tight poll otherwise competes with the work it is serving.
func TestS319CallbackMailboxPollYieldsToParallelCallback(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })

	ready := make(chan struct{})
	release := make(chan struct{})
	ran := make(chan struct{}, 1)
	go func() {
		close(ready)
		<-release
		ran <- struct{}{}
	}()
	<-ready
	close(release)

	bashPPMailboxYield()
	select {
	case <-ran:
	default:
		t.Fatal("empty mailbox poll did not yield to runnable callback service")
	}
}
