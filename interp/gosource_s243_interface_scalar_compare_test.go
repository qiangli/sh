//go:build full

package interp_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestS243OriginalConvT2X(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "test", "convT2X.go"))
	if err != nil {
		t.Fatal(err)
	}
	typedSendThreeModes(t, string(source))
}

func TestS243InterfaceScalarComparison(t *testing.T) {
	typedSendThreeModes(t, `package main
import "fmt"
type N uint32
var order int
func scalar() uint32 {if order!=0{panic("scalar order")};order++;return 7}
func iface() any {if order!=1{panic("interface order")};order++;return uint32(7)}
func main(){
 c:=make(chan any,4); c<-uint32(7);c<-uint64(7);c<-N(7);c<-nil
 u:=uint32(7);v:=uint64(7);n:=N(7)
 a:=<-c; b:=<-c;d:=<-c;z:=<-c
 fmt.Println(a==u,u==a,a!=v,b==v,d==n,d!=u,z!=u)
 fmt.Println(scalar()==iface(),order)
 var p *int; var x any=p;fmt.Println(x==nil,x==p)
 func(){defer func(){fmt.Println(recover()!=nil)}();var y any=[]int{1};_ = y==y}()
}`)
}
