//go:build full

package interp_test

import "testing"

func TestS557ResidentMethodCapture(t *testing.T) {
	typedSendThreeModes(t, `package main
import("fmt"; "sync")
func main() {
 mu:=new(sync.Mutex)
 mu.Lock()
 original:=mu
 unlock:=mu.Unlock
 mu=new(sync.Mutex)
 unlock()
 if !original.TryLock() { panic("method lost original receiver") }
 original.Unlock()
 if !mu.TryLock() { panic("method changed new receiver") }
 mu.Unlock()
 wg:=new(sync.WaitGroup)
 add,done,wait:=wg.Add,wg.Done,wg.Wait
 wg=new(sync.WaitGroup)
 add(1)
 go done()
 wait()
 wg.Wait()
 fmt.Println("captured")
}`)
}
