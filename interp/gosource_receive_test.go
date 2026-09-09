package interp_test

// Sprint: #118; Story: #51; Story-ID: 825f8083451e
import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func TestGoSourceComputedReceiveThreeModes(t *testing.T) {
	cases := map[string]string{
		"computed_direct_and_scalar": `package main
import "fmt"
func pick(c chan int)chan int{fmt.Println("channel");return c}
func main(){c:=make(chan int,2);c<-7;c<-8;x:=<-pick(c);fmt.Println(x,1+(<-pick(c)))}`,
		"closed_channel_zero_is_nil": `package main
import "fmt"
func main(){outer:=make(chan chan int);close(outer);inner:=<-outer;select{case <-inner:fmt.Println("UNREACHABLE");default:fmt.Println("nil")}}`,
		"computed_closed_pair": `package main
import "fmt"
func pick(c chan int)chan int{fmt.Println("channel");return c}
func main(){c:=make(chan int);close(c);x,ok:=<-pick(c);fmt.Println(x,ok)}`,
		"nil_receive_default": `package main
import "fmt"
func pick(c <-chan int)<-chan int{fmt.Println("channel");return c}
func main(){var c chan int;select{case x:=<-pick(c):fmt.Println("UNREACHABLE",x);default:fmt.Println("default")}}`,
		"all_select_operands_before_branch": `package main
import "fmt"
func pick(c chan int,s string)chan int{fmt.Println("channel",s);return c}
func value()int{fmt.Println("rhs");return 1}
func main(){c:=make(chan int,1);c<-7;var n chan int;select{case x:=<-pick(c,"first"):fmt.Println("selected",x);case <-pick(n,"second"):fmt.Println("UNREACHABLE recv");case pick(n,"third")<-value():fmt.Println("UNREACHABLE send")}}`,
		"operand_panic_skips_later": `package main
import "fmt"
func broken()chan int{fmt.Println("channel");panic("broken")}
func value()int{fmt.Println("UNREACHABLE rhs");return 1}
func main(){var c chan int;defer func(){v:=recover();fmt.Println(v)}();select{case <-broken():case c<-value():}}`,
		"nested_receive_operand": `package main
import "fmt"
func main(){outer:=make(chan chan int,1);inner:=make(chan int,1);inner<-9;outer<-inner;fmt.Println(<-(<-outer))}`,
		"structured_result": `package main
import "fmt"
type Item struct{N int}
func pick(c chan Item)chan Item{fmt.Println("channel");return c}
func main(){c:=make(chan Item,1);c<-Item{7};x:=<-pick(c);fmt.Printf("%T %v\n",x,x)}`,
		"computed_assignment": `package main
import "fmt"
func pick(c chan int)chan int{fmt.Println("channel");return c}
func main(){c:=make(chan int,1);c<-7;var x int;x=<-pick(c);fmt.Println(x)}`,
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) { typedSendThreeModes(t, source) })
	}
}

// This is a documented unsupported-storage guard, not a product success claim:
// the original valid Go program must fail before fabricating a channel from an
// aggregate's scalar carrier or consuming a different channel.
func TestGoSourceReceiveRejectsUnrepresentedChannelStorage(t *testing.T) {
	source := `package main
import "fmt"
func main(){c:=make(chan int,1);c<-7;cs:=[]chan int{c};fmt.Println(<-cs[0])}`
	program, err := gosource.Parse(strings.NewReader(source), "channel-storage.go", gosource.Options{RunMain: true})
	if err != nil {
		t.Fatal(err)
	}
	var out, errout bytes.Buffer
	runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.StdIO(nil, &out, &errout))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err = runner.Run(ctx, program.File)
	if status, ok := interp.IsExitStatus(err); !ok || status != 2 || !strings.Contains(errout.String(), "BASHPP-ECOLLECTION-ELEMENT") || out.Len() != 0 {
		t.Fatalf("unsupported storage must fail closed: %v stdout=%q stderr=%q", err, out.String(), errout.String())
	}
}
func TestGoSourceComputedReceiveCancellation(t *testing.T) {
	for name, body := range map[string]string{
		"direct_nil": `var c chan int;x:=<-pick(c);fmt.Println(x)`,
		"select_nil": `var c chan int;select{case <-pick(c):}`,
		"unbuffered": `c:=make(chan int);<-pick(c)`,
	} {
		t.Run(name, func(t *testing.T) {
			source := `package main
import "fmt"
func pick(c chan int)chan int{fmt.Println("operand");return c}
func main(){` + body + `;fmt.Println("UNREACHABLE")}`
			program, err := gosource.Parse(strings.NewReader(source), "cancel-receive.go", gosource.Options{RunMain: true})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			out := &sendReadyWriter{ready: make(chan struct{}), marker: "operand\n"}
			var errout bytes.Buffer
			runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.StdIO(nil, out, &errout))
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- runner.Run(ctx, program.File) }()
			select {
			case <-out.ready:
			case err := <-done:
				t.Fatalf("receive ended before operand: %v %q", err, errout.String())
			case <-ctx.Done():
				t.Fatal("operand did not execute")
			}
			select {
			case err := <-done:
				t.Fatalf("receive did not block: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			cancel()
			select {
			case err := <-done:
				if status, ok := interp.IsExitStatus(err); !ok || status != 1 || ctx.Err() != context.Canceled {
					t.Fatalf("cancel returned %v stderr=%q", err, errout.String())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cancelled receive did not join")
			}
			if out.String() != "operand\n" {
				t.Fatalf("operand count or post-receive execution: %q", out.String())
			}
		})
	}
}
