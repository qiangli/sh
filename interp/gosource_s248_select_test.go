//go:build full

package interp_test

// Sprint: #248; Story: #700; Story-ID: 14e8b88629e0

import (
	"strings"
	"testing"
)

// TestS248NilNativeChannelSelectArm: context.Background().Done() is a nil
// dependency channel. A nil channel never communicates, so its arm is
// disabled rather than forcing arbitration against the interpreter-owned arm.
func TestS248NilNativeChannelSelectArm(t *testing.T) {
	const source = `package main

import (
	"context"
	"fmt"
)

type pair struct{ a, b int }

func main() {
	ctx := context.Background()
	values := make(chan pair)
	go func() {
		for i := 0; i < 3; i++ {
			values <- pair{i, i * i}
		}
		close(values)
	}()
	for {
		select {
		case <-ctx.Done():
			panic("nil channel arm fired")
		case p, ok := <-values:
			if !ok {
				fmt.Println("closed")
				return
			}
			fmt.Println(p.a, p.b)
		}
	}
}
`
	out, stderr, err := runGoSource(t, "s248-nil-native-arm", source)
	if err != nil || stderr != "" || out != "0 0\n1 1\n2 4\nclosed\n" {
		t.Fatalf("run=%v stdout=%q stderr=%q", err, out, stderr)
	}
}

// A live dependency channel still cannot share one atomic select with an
// interpreter-owned channel: that shape keeps its prompt refusal.
func TestS248LiveNativeChannelMixedSelectRefusal(t *testing.T) {
	const source = `package main

import (
	"context"
	"fmt"
)

type pair struct{ a, b int }

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	values := make(chan pair, 1)
	values <- pair{1, 2}
	select {
	case <-ctx.Done():
		fmt.Println("canceled")
	case p := <-values:
		fmt.Println(p.a, p.b)
	}
}
`
	got := runGoSourceRunnerError(t, source)
	if !strings.Contains(got, "mixed native/interpreted channel select requires atomic arbitration") {
		t.Fatalf("live mixed select was not refused: %q", got)
	}
}
