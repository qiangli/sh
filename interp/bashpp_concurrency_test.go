package interp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/expand"
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

func TestBashPPFailureArbitratesByLaunchOrdinal(t *testing.T) {
	for range 120 {
		out, err := runBashPPConcurrency(t, `
func seven() { return 7; }
func nine() { return 9; }
func main() { go seven(); go nine(); }
main()
`)
		if err == nil || !strings.Contains(out, "exit status 7") {
			t.Fatalf("out=%q err=%v", out, err)
		}
	}
	for range 120 {
		out, err := runBashPPConcurrency(t, `
func one() { false; }
func nine() { return 9; }
func main() { go one(); go nine(); }
main()
`)
		if err == nil || !strings.Contains(out, "exit status 1") {
			t.Fatalf("code-1 concrete failure was displaced: out=%q err=%v", out, err)
		}
	}
}

func TestBashPPFastFailureRefusesLaterLaunchSideEffects(t *testing.T) {
	out, err := runBashPPConcurrency(t, `
func fail() { return 7; }
func later(ch) { ch <- launched; }
func main() {
 ch := make(chan string, 1)
 go fail()
 go later(ch)
 value := <-ch
 echo "$value"
}
main()
`)
	if err == nil || strings.Contains(out, "launched\n") || !strings.Contains(out, "exit status 7") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestBashPPFastFailureCancelsOwnerReceive(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		out, err := runBashPPConcurrency(t, `
func fail() { return 7; }
func main() {
 ch := make(chan string)
 go fail()
 never := <-ch
 echo "$never"
}
main()
`)
		if err == nil || !strings.Contains(out, "exit status 7") {
			t.Errorf("out=%q err=%v", out, err)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("owner receive remained blocked after task failure")
	}
}

func TestBashPPEOFStopsSuccessfulBlockedTask(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		out, err := runBashPPConcurrency(t, `
func blocked(ch) { never := <-ch; echo "$never"; }
func main() { ch := make(chan string); go blocked(ch); }
main()
`)
		if err != nil || out != "" {
			t.Errorf("out=%q err=%v", out, err)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("File completion did not cancel and join blocked task")
	}
}

func TestCloneBashPPVariableCopiesMaps(t *testing.T) {
	orig := expand.Variable{
		ListMap: map[int]string{3: "three"},
		ListSet: map[int]bool{3: true},
		Map:     map[string]string{"key": "value"},
	}
	cloned := cloneBashPPVariable(orig)
	if cloned.ListMap[3] != "three" || !cloned.ListSet[3] || cloned.Map["key"] != "value" {
		t.Fatalf("map content lost: %#v", cloned)
	}
	cloned.ListMap[3] = "changed"
	cloned.ListSet[4] = true
	cloned.Map["key"] = "changed"
	if orig.ListMap[3] != "three" || orig.ListSet[4] || orig.Map["key"] != "value" {
		t.Fatalf("clone aliases source: orig=%#v cloned=%#v", orig, cloned)
	}
}

func TestBashPPChannelCloseCancelsRegisteredSend(t *testing.T) {
	for range 200 {
		ch := newBashPPChannel("string", 0)
		registered := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			if !ch.beginSend() {
				t.Error("fresh channel refused send registration")
				return
			}
			close(registered)
			select {
			case ch.ch <- "never":
				t.Error("unbuffered send unexpectedly completed")
			case <-ch.closing:
			}
			ch.endSend()
		}()
		<-registered
		if !ch.close() {
			t.Fatal("fresh channel refused close")
		}
		<-done
		if ch.beginSend() {
			t.Fatal("closed channel accepted send registration")
		}
	}
}

func TestBashPPTaskExitTrapRunsOnceOnSiblingCancellation(t *testing.T) {
	out, err := runBashPPConcurrency(t, `
func blocked(ch) { value := <-ch; echo "$value"; }
func fail() { return 7; }
func main() {
 trap 'echo task-exit' EXIT
 ch := make(chan string)
 go blocked(ch)
 go fail()
}
main()
`)
	if err == nil || strings.Count(out, "task-exit\n") != 3 || !strings.Contains(out, "exit status 7") {
		// Both launched tasks run their private snapshot, then the owner runs its
		// own EXIT trap at the File boundary.
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestBashPPUnbufferedParentTaskRendezvous(t *testing.T) {
	out, err := runBashPPConcurrency(t, `
func send(ch) { ch <- from-task; }
func recv(ch, ack) { value := <-ch; ack <- "$value-from-task"; }
func main() {
 from := make(chan string)
 go send(from)
 value := <-from
 echo "$value"
 to := make(chan string)
 ack := make(chan string)
 go recv(to, ack)
 to <- from-parent
 result := <-ack
 echo "$result"
}
main()
`)
	if err != nil || out != "from-task\nfrom-parent-from-task\n" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestBashPPGoRequiresFileOwner(t *testing.T) {
	r, err := New(Lang(syntax.LangBashPP))
	if err != nil {
		t.Fatal(err)
	}
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader("func f() { return; }\ngo f()\n"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background(), f.Stmts[1]); err == nil {
		t.Fatal("go escaped a Stmt Run without a File owner")
	}
}

func TestBashPPTaskSnapshotMutableObjectAndSubshellLevel(t *testing.T) {
	out, err := runBashPPConcurrency(t, `
arr=(80 443)
func mutate(ch) {
 arr[0]=8080
 ch <- "$BASH_SUBSHELL:${arr[0]}"
}
func main() {
 ch := make(chan string, 1)
 go mutate(ch)
 result := <-ch
 echo "$result"
 echo "${arr[0]}"
}
main()
`)
	if err != nil || out != "0:8080\n80\n" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestBashPPTaskDescriptorCloseIsPrivateAndOffsetShared(t *testing.T) {
	path := t.TempDir() + "/values"
	if err := os.WriteFile(path, []byte("first\nsecond\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := `
exec 8<` + path + `
func first(ch) { read -u 8 value; ch <- "$value"; exec 8<&-; }
func main() {
 ch := make(chan string)
 go first(ch)
 firstValue := <-ch
 echo "$firstValue"
 read -u 8 value
 echo "$value"
}
main()
`
	out, err := runBashPPConcurrency(t, src)
	if err != nil || out != "first\nsecond\n" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestBashPPTaskSignalsStayLocal(t *testing.T) {
	out, err := runBashPPConcurrency(t, `
func signals(ch) {
 kill -s TERM $$
 ch <- unreachable
}
func main() { ch := make(chan string); go signals(ch); }
main()
`)
	if err == nil || !strings.Contains(out, "process signals are unavailable inside a Bash++ task") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestBashPPTaskExecTerminatesOnlyTask(t *testing.T) {
	out, err := runBashPPConcurrency(t, `
func replace() { exec /bin/echo task-exec; echo unreachable; }
func main() { go replace(); echo owner-alive; }
main()
`)
	if err != nil || !strings.Contains(out, "owner-alive\n") || strings.Contains(out, "unreachable") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestBashPPChannelNumericWidthsAndNamedTypes(t *testing.T) {
	out, err := runBashPPConcurrency(t, `
type Small int8
type Alias = uint8
type Color enum { Red; Green }
func main() {
 a := make(chan Small, 1); a <- 128
 b := make(chan Alias, 1); b <- -1
 c := make(chan Color, 1); c <- Blue
}
main()
`)
	if err == nil || !strings.Contains(out, `cannot send "128" as Small`) {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestBashPPStaleChannelCapabilityIsInvalid(t *testing.T) {
	var out strings.Builder
	r, err := New(Lang(syntax.LangBashPP), StdIO(nil, &out, &out))
	if err != nil {
		t.Fatal(err)
	}
	parse := func(src string) *syntax.File {
		f, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), "stale.bpp")
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	if err := r.Run(context.Background(), parse("func setup() { ch := make(chan string, 1); SAVED=$ch; }\nsetup()\n")); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background(), parse("func stale() { SAVED <- stale; }\nstale()\n")); err == nil ||
		!strings.Contains(out.String(), "not a channel in this task group") {
		t.Fatalf("out=%q err=%v", out.String(), err)
	}
}

func TestBashPPTaskSnapshotRefusesUnsupportedObject(t *testing.T) {
	var out strings.Builder
	r, err := New(Lang(syntax.LangBashPP), StdIO(nil, &out, &out))
	if err != nil {
		t.Fatal(err)
	}
	r.Reset()
	if err := r.writeEnv.Set("OPAQUE", expand.Variable{Set: true, Kind: expand.Object, Obj: make(chan int)}); err != nil {
		t.Fatal(err)
	}
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader("func f() { return; }\ngo f()\n"), "object.bpp")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background(), f); err == nil || !strings.Contains(out.String(), "unsupported mutable Bash++ object type") {
		t.Fatalf("out=%q err=%v", out.String(), err)
	}
}

func TestBashPPTaskObjectCloneRejectsCycle(t *testing.T) {
	object := map[string]any{}
	object["self"] = object
	if _, err := newBashPPObjectCloner().clone(object); err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("cycle clone error = %v", err)
	}
}

func TestBashPPSelectReleasesSendBeforeArmAndOnError(t *testing.T) {
	for _, src := range []string{
		`func main() { ch := make(chan string, 1); select { case ch <- ok: close(ch); } }; main()`,
		`func main() { ch := make(chan string); select { case ch <- ok: echo no; case missing <- bad: echo no; }; close(ch); }; main()`,
	} {
		done := make(chan struct{})
		go func() { _, _ = runBashPPConcurrency(t, src); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("select leaked a send registration")
		}
	}
}

func TestBashPPChannelTypeExistenceAndReceiveIdentity(t *testing.T) {
	out, err := runBashPPConcurrency(t, `func main() { ch := make(chan Missing); }; main()`)
	if err == nil || !strings.Contains(out, "undefined element type: Missing") {
		t.Fatalf("out=%q err=%v", out, err)
	}

	out, err = runBashPPConcurrency(t, `
type Small int8
func (v Small) Show() { echo "typed:$v"; }
func main() { ch := make(chan Small, 1); ch <- 7; value := <-ch; value.Show(); }
main()
`)
	if err != nil || out != "typed:7\n" {
		t.Fatalf("named receive identity: out=%q err=%v", out, err)
	}

	r := &Runner{bashPPScope: newBashPPScope(nil), bashPPTypes: map[string]bashPPType{
		"Named": {underlying: "int8"},
		"Alias": {underlying: "uint8", alias: true},
		"Color": {underlying: "enum", members: []string{"Red", "Green"}},
	}}
	for _, tc := range []struct {
		typ, good, bad, identity string
	}{
		{"Named", "127", "128", "Named"},
		{"Alias", "255", "-1", ""},
		{"Color", "Red", "Blue", "Color"},
	} {
		if !r.bashPPValueFits(tc.typ, tc.good) || r.bashPPValueFits(tc.typ, tc.bad) {
			t.Errorf("independent %s validation failed", tc.typ)
		}
		if err := r.bashPPScope.declare(tc.typ, expand.Variable{Set: true}, false); err != nil {
			t.Fatal(err)
		}
		r.bashPPSetReceivedType(tc.typ, tc.typ)
		if got := r.bashPPScope.lookup(tc.typ).typeName; got != tc.identity {
			t.Errorf("received %s identity = %q, want %q", tc.typ, got, tc.identity)
		}
	}
}

func TestBashPPKnownCapabilityCannotExfiltrateOrCrossLaterExec(t *testing.T) {
	out, _ := runBashPPConcurrency(t, `
func main() {
 ch := make(chan string, 1)
	 ( echo "prefix-${ch}-suffix" )
	 echo "$(echo prefix-${ch}-suffix)"
	 echo "$ch" | /bin/cat
}

main()
`)
	if strings.Count(out, "channel handles cannot cross a shell-copy boundary") < 1 {
		t.Fatalf("shell-copy exfiltration was not rejected: %q", out)
	}

	var later strings.Builder
	r, _ := New(Lang(syntax.LangBashPP), StdIO(nil, &later, &later))
	parse := func(src string) *syntax.File {
		f, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), "stale-exec.bpp")
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	if err := r.Run(context.Background(), parse(`func setup() { ch := make(chan string, 1); export SAVED=$ch; }; setup()`)); err == nil ||
		!strings.Contains(later.String(), "channel capabilities cannot be exported") {
		t.Fatalf("capability export was not rejected: out=%q err=%v", later.String(), err)
	}
	if err := r.Run(context.Background(), parse(`func save() { ch := make(chan string, 1); SAVED=$ch; }; save()`)); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background(), parse(`func stale() { SAVED <- nope; }; stale()`)); err == nil ||
		!strings.Contains(later.String(), "not a channel in this task group") {
		t.Fatalf("stale capability gained authority: out=%q err=%v", later.String(), err)
	}
	if err := r.Run(context.Background(), parse(`/bin/echo "prefix-${SAVED}-suffix"`)); err == nil ||
		!strings.Contains(later.String(), "channel handles cannot cross an exec boundary") {
		t.Fatalf("stale capability crossed exec: out=%q err=%v", later.String(), err)
	}
	innocent, err := runBashPPConcurrency(t, `/bin/echo chan@bashpp:not-issued`)
	if err != nil || innocent != "chan@bashpp:not-issued\n" {
		t.Fatalf("innocent prefix rejected: out=%q err=%v", innocent, err)
	}
}

func TestBashPPCanceledTaskDoesNotPromoteTransientFailure(t *testing.T) {
	for range 120 {
		out, err := runBashPPConcurrency(t, `
func transient(ch) { false; value := <-ch; echo "$value"; }
func seven() { return 7; }
func main() { ch := make(chan string); go transient(ch); go seven(); }
main()
`)
		if err == nil || !strings.Contains(out, "exit status 7") || strings.Contains(out, "exit status 1") {
			t.Fatalf("out=%q err=%v", out, err)
		}
	}
}

func TestBashPPUnseededTaskGetsPrivateRNG(t *testing.T) {
	r, err := New(Lang(syntax.LangBashPP))
	if err != nil {
		t.Fatal(err)
	}
	r.Reset()
	r.bashPPConcurrent = newBashPPConcurrent(context.Background())
	defer r.bashPPConcurrent.cancel()
	a, err := r.bashPPTaskSnapshot(0)
	if err != nil {
		t.Fatal(err)
	}
	defer a.closeBashPPTaskResources()
	b, err := r.bashPPTaskSnapshot(1)
	if err != nil {
		t.Fatal(err)
	}
	defer b.closeBashPPTaskResources()
	if !a.randomSeeded || !b.randomSeeded || a.randomSeed == b.randomSeed {
		t.Fatalf("task RNGs not private: a=%d/%v b=%d/%v", a.randomSeed, a.randomSeeded, b.randomSeed, b.randomSeeded)
	}
}

func TestBashPPTypedChannelCapabilityPropagationAndForgery(t *testing.T) {
	out, err := runBashPPConcurrency(t, `
func relay(ch) { ch <- function; }
func namedRelay(ch) { ch <- named; }
func defaultRelay(ch string = channel) { ch <- default; }
func main() {
 ch := make(chan string, 5)
	channel := ch
	copy := ch
	copy <- direct
	var assigned string
	assigned=ch
	assigned <- assigned
	relay(ch)
 namedRelay(ch: ch)
 defaultRelay()
	first := <-ch
	second := <-ch
	third := <-ch
	fourth := <-ch
	fifth := <-ch
 echo "$first"
 echo "$second"
 echo "$third"
	echo "$fourth"
	echo "$fifth"
 forged := "$ch"
 forged <- denied
}
main()
`)
	if err == nil || !strings.Contains(out, "direct\n") || !strings.Contains(out, "assigned\n") || !strings.Contains(out, "function\n") ||
		!strings.Contains(out, "named\n") || !strings.Contains(out, "default\n") ||
		!strings.Contains(out, "forged is not a channel in this task group") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestBashPPTypeAliasChainsAndInvalidUnderlying(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`type Broken Missing`, "undefined type: Missing"},
		{`type Loop Loop`, "cyclic type declaration: Loop"},
	} {
		out, err := runBashPPConcurrency(t, tc.src)
		if err == nil || !strings.Contains(out, tc.want) {
			t.Errorf("src=%q out=%q err=%v", tc.src, out, err)
		}
	}
	r := &Runner{bashPPScope: newBashPPScope(nil), bashPPTypes: map[string]bashPPType{
		"Base":  {underlying: "int8"},
		"Alias": {underlying: "Base", alias: true},
		"Chain": {underlying: "Alias", alias: true},
	}}
	_ = r.bashPPScope.declare("value", expand.Variable{Set: true}, false)
	r.bashPPSetReceivedType("value", "Chain")
	if got := r.bashPPScope.lookup("value").typeName; got != "Base" {
		t.Fatalf("alias chain identity=%q", got)
	}
	if !r.bashPPValueFits("Chain", "127") || r.bashPPValueFits("Chain", "128") {
		t.Fatal("alias chain validation did not reach underlying type")
	}
}
