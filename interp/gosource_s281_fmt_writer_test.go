//go:build full

package interp

// Sprint: #281; Story: #809; Story-ID: fac7e14af4a8

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/syntax"
)

func TestGoSourceS281LocalFmtWriterStaysInInterpreter(t *testing.T) {
	var mu sync.Mutex
	steps := map[string]int{}
	goSourceReflectTrace = func(step string) {
		mu.Lock()
		steps[step]++
		mu.Unlock()
	}
	defer func() { goSourceReflectTrace = nil }()

	source := `package main

import "fmt"

type sink struct{ data string }

func (s *sink) Write(p []byte) (int, error) {
	s.data += string(p)
	return len(p), nil
}

func main() {
	var s sink
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&s, "%6d  ", i)
		fmt.Fprint(&s, ".  ")
		fmt.Fprintln(&s, "Name:", "node")
	}
	fmt.Println(len(s.data) > 0)
}
`
	var out, errout bytes.Buffer
	r, err := New(Lang(syntax.LangBashPP), Dir(t.TempDir()), StdIO(nil, &out, &errout))
	if err != nil {
		t.Fatal(err)
	}
	p, err := gosource.Parse(strings.NewReader(source), filepath.Join(r.Dir, "fmtwriter.go"), gosource.Options{RunMain: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := r.Run(ctx, p.File); err != nil {
		t.Fatalf("run: %v\nstderr=%q", err, errout.String())
	}
	if out.String() != "true\n" || errout.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", out.String(), errout.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if got := steps["fmt.Fprintf"] + steps["fmt.Fprint"] + steps["fmt.Fprintln"]; got != 0 {
		t.Fatalf("local fmt writer crossed to dependency helper %d times; steps=%v", got, steps)
	}
}
