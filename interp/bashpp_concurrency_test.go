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
 ch <- one
 ch <- two
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
	if out != "one\ntwo\n" {
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
	if !strings.Contains(out, "Channel ch cannot cross a subshell boundary") {
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
