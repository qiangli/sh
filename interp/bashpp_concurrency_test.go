package interp

import (
	"context"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func runBashPPConcurrency(t *testing.T, src string) (string, error) {
	t.Helper()
	var out strings.Builder
	r, err := New(Lang(syntax.LangBashPP), StdIO(nil, &out, &out))
	if err != nil {
		t.Fatal(err)
	}
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), "concurrency.bpp")
	if err != nil {
		t.Fatal(err)
	}
	err = r.Run(context.Background(), f)
	return out.String(), err
}

func TestBashPPChannelsAndGo(t *testing.T) {
	out, err := runBashPPConcurrency(t, `
func worker(ch) {
 ch <- 1
 ch <- 2
 close(ch)
}
func main() {
 ch := make(chan int)
 go worker(ch)
 for v := range ch {
  echo $v
 }
}
main()
`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "1\n2\n" {
		t.Fatalf("out=%q", out)
	}
}

func TestBashPPClosedTwoValueReceive(t *testing.T) {
	out, err := runBashPPConcurrency(t, `
func main() {
 ch := make(chan string, 1)
 ch <- yes
 close(ch)
 first, open := <-ch
 echo "$first:$open"
 second, still := <-ch
 echo "$second:$still"
}
main()
`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "yes:true\n:false\n" {
		t.Fatalf("out=%q", out)
	}
}

func TestBashPPChannelRefusesSubshell(t *testing.T) {
	out, _ := runBashPPConcurrency(t, `
func main() {
	ch := make(chan int)
	( ch <- one )
}
main()
`)
	if !strings.Contains(out, "channel ch cannot cross a shell-copy boundary") {
		t.Fatalf("out=%q", out)
	}
}

func TestBashPPTypedChannelAndCapacityBounds(t *testing.T) {
	out, _ := runBashPPConcurrency(t, `
func main() {
 bad := make(chan int, 65537)
 good := make(chan int, 1)
 good <- nope
}
main()
`)
	if !strings.Contains(out, "capacity must be between 0 and 65536") ||
		!strings.Contains(out, `cannot send "nope" as int channel value`) {
		t.Fatalf("out=%q", out)
	}
}

func TestBashPPSelectReceiveDeclDefaultAndClosed(t *testing.T) {
	out, err := runBashPPConcurrency(t, `
func main() {
 ch := make(chan string, 1)
 ch <- ready
 select {
 case v, ok := <-ch:
  echo "$v:$ok"
 default:
  echo missed
 }
 close(ch)
 select {
 case v, ok := <-ch:
  echo "$v:$ok"
 }
 empty := make(chan string)
 select {
 case <-empty:
  echo impossible
 default:
  echo default
 }
}
main()
`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "ready:true\n:false\ndefault\n" {
		t.Fatalf("out=%q", out)
	}
}

func TestBashPPTaskFailureCancelsBlockedSibling(t *testing.T) {
	out, err := runBashPPConcurrency(t, `
func blocked(ch) { v := <-ch; echo $v; }
func fail() { false; }
func main() {
 ch := make(chan string)
 go blocked(ch)
 go fail()
}
main()
`)
	if err == nil {
		t.Fatal("expected task failure")
	}
	if !strings.Contains(out, "bash++: task failed: exit status 1") {
		t.Fatalf("out=%q", out)
	}
}

func TestBashPPNestedTaskRegistration(t *testing.T) {
	out, err := runBashPPConcurrency(t, `
func leaf(ch) { ch <- nested; close(ch); }
func parent(ch) { go leaf(ch); }
func main() {
 ch := make(chan string)
 go parent(ch)
 for v := range ch { echo $v; }
}
main()
`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nested") {
		t.Fatalf("out=%q", out)
	}
}

func TestBashPPChannelHandleRefusesExec(t *testing.T) {
	out, _ := runBashPPConcurrency(t, `
func main() {
 ch := make(chan string)
 /bin/echo "$ch"
}
main()
`)
	if !strings.Contains(out, "channel handles cannot cross an exec boundary") {
		t.Fatalf("out=%q", out)
	}
}

func TestBashPPConcurrentTaskOutput(t *testing.T) {
	_, err := runBashPPConcurrency(t, `
func say(v) {
 echo $v
}
func main() {
 go say(one)
 go say(two)
}
main()
`)
	if err != nil {
		t.Fatal(err)
	}
}
