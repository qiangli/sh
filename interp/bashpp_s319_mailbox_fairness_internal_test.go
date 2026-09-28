package interp

// Sprint: #319; Story: #1088; Story-ID: 00446a7bd51a

import (
	"runtime"
	"sync"
	"sync/atomic"
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

	var spin uint32
	for range 16 {
		bashPPMailboxWaitYield(&spin)
		select {
		case <-ran:
			return
		default:
		}
	}
	t.Fatal("empty mailbox polls did not yield to runnable callback service")
}

// A testing.M callback request stays live while all parallel test callbacks
// execute. Model that owner plus the runnable callback frames on one processor:
// an idle owner which only calls Gosched remains runnable and is scheduled once
// for every callback step. After a short responsive window, idle polling must
// back off so callback work, the control reader, and timers own the processor.
func TestS319CallbackMailboxPollBacksOffUnderParallelCallbacks(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })

	const callbacks = 16
	run := func(wait func(*uint32)) uint64 {
		start := make(chan struct{})
		stop := make(chan struct{})
		var callbackReady sync.WaitGroup
		var callbackDone sync.WaitGroup
		var pollerDone sync.WaitGroup
		var polls atomic.Uint64
		callbackReady.Add(callbacks)
		callbackDone.Add(callbacks)
		for range callbacks {
			go func() {
				defer callbackDone.Done()
				callbackReady.Done()
				<-start
				for range 256 {
					runtime.Gosched()
				}
			}()
		}
		pollerDone.Add(1)
		go func() {
			defer pollerDone.Done()
			var spin uint32
			<-start
			for {
				select {
				case <-stop:
					return
				default:
					polls.Add(1)
					wait(&spin)
				}
			}
		}()
		callbackReady.Wait()
		close(start)
		callbackDone.Wait()
		close(stop)
		pollerDone.Wait()
		return polls.Load()
	}

	// This is the behavior of 4334607f: every idle poll only yielded and
	// therefore competed with each unit of useful callback work.
	if red := run(func(*uint32) { runtime.Gosched() }); red <= 192 {
		t.Fatalf("Gosched-only control made %d polls; contention reproduction did not turn red", red)
	}
	if got := run(bashPPMailboxWaitYield); got > 192 {
		t.Fatalf("idle mailbox polls = %d, want <= 192 while parallel callbacks run", got)
	}
}
