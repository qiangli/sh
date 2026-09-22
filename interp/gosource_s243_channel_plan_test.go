//go:build full

package interp_test

// Sprint: #243; Story: #674; Story-ID: 63073886bfce
import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestS243ChannelDomainPlanningThreeModes(t *testing.T) {
	for name, source := range map[string]string{
		"connected-local-types": `package main
import "fmt"
type payload struct{ n int }
func main(){ requests:=make(chan int,1); data:=make(chan payload,1); requests<-1; data<-payload{7}; select { case <-requests: fmt.Println((<-data).n); case data<-payload{9}: panic("losing send") }; close(requests); close(data) }`,
		"native-origin-keeps-domain-native": `package main
import("fmt";"time")
func main(){ c:=make(chan int,1); c<-7; select { case v:=<-c: fmt.Println(v); case <-time.After(time.Hour): panic("timer") } }`,
		"close-nil-cancel-and-nonselected-sender": `package main
import "fmt"
func main(){ c:=make(chan int,1); var stop chan struct{}; c<-4; select { case v:=<-c: fmt.Println(v); case c<-9: panic("losing sender"); case <-stop: panic("nil cancellation arm") }; close(c); _,ok:=<-c; fmt.Println(ok) }`,
	} {
		t.Run(name, func(t *testing.T) { typedSendThreeModes(t, source) })
	}
}

func TestS243OriginalPowserChannelDomains(t *testing.T) {
	for _, name := range []string{"powser1.go", "powser2.go"} {
		source, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "test", "chan", name))
		if err != nil {
			t.Fatal(err)
		}
		t.Run(name, func(t *testing.T) { typedSendThreeModes(t, string(source)) })
	}
}
