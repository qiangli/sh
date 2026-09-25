//go:build full

package interp_test

// Sprint: #281; Story: #809; Story-ID: fac7e14af4a8
//
// cmd/internal/testdir (Go 1.27's std lib test harness) pins
//
//	var findExecCmd = sync.OnceValue(func() (execCmd []string) { ... })
//
// and later calls findExecCmd() repeatedly. A callback result of []string is
// a slice built from pure value-semantics elements: unlike a []reflect.Value
// result, whose elements are already dependency handles
// (bashPPNativeHandleSlice), its elements are interpreter-owned storage that
// Go itself copies out wherever the slice is read — exactly what the general
// "value-semantics parameters and supported results" rule already admits for
// a struct or array of scalars (bashPPCallbackValueType). The bridge used to
// stop at struct/array and hard-refuse any slice result it did not
// specifically special-case (a nested [][]byte, or map[string]int for the Go
// Tour helpers), well before the request-scoped copied-results gate
// (copiedResultsConsumer) ever got a chance to admit it. bashPPCallbackValueSlice
// generalizes the existing bashPPNativeHandleSlice treatment to any slice
// whose elements are value-semantics, so it is rebuilt from copied element
// values on reply, gated the same way: only a consumer that copies results
// out and never retains them (sync.OnceFunc/OnceValue/OnceValues,
// reflect.MakeFunc) may observe it. These tests run the unchanged original
// source and compare stdout/stderr/status against a real Go build, so both
// the admission and the OnceValue "compute once, cache forever" semantics
// are pinned together.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/gosource"
)

func TestS281OnceValueValueSliceResult(t *testing.T) {
	for name, source := range map[string]string{
		"once_value_string_slice": `package main

import (
	"fmt"
	"sync"
)

var calls int

var findExecCmd = sync.OnceValue(func() (execCmd []string) {
	calls++
	return []string{"a", "b", "c"}
})

func main() {
	fmt.Println(findExecCmd())
	fmt.Println(findExecCmd())
	fmt.Println(calls)
}
`,
		"once_values_slice_and_scalar": `package main

import (
	"fmt"
	"sync"
)

var calls int

var get = sync.OnceValues(func() ([]int, error) {
	calls++
	return []int{1, 2, 3}, nil
})

func main() {
	a, errA := get()
	b, errB := get()
	fmt.Println(a, errA, b, errB, calls)
}
`,
		"once_value_struct_slice": `package main

import (
	"fmt"
	"sync"
)

type point struct{ X, Y int }

var get = sync.OnceValue(func() []point {
	return []point{{1, 2}, {3, 4}}
})

func main() {
	fmt.Println(get())
	fmt.Println(get())
}
`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "original.go")
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			oracle := runNativeOracle(t, dir, path, nil, "")
			got := runGoSourceRunner(t, dir, path, source, nil, "")
			if got.stdout != oracle.stdout || got.stderr != oracle.stderr || got.status != oracle.status {
				t.Fatalf("interp=%+v oracle=%+v", got, oracle)
			}
		})
	}
}

// A slice result stays admitted only for a reviewed copied-results consumer;
// a slice PARAMETER keeps the original refusal regardless of element type,
// because unlike a result it is not consumed once and copied out — an async
// or retained callback holding a copy could silently miss writes the
// original makes through its own backing array. bufio.Scanner.Split's own
// []byte parameter already pinned this before Sprint 281; this only confirms
// the new value-slice-result admission did not loosen it.
func TestS281ValueSliceParameterStillRefused(t *testing.T) {
	const source = `package main
import ("bufio";"strings")
func main(){s:=bufio.NewScanner(strings.NewReader(""));s.Split(func(b []byte,e bool)(int,[]byte,error){println("callback-ran");return 0,nil,nil});println("after")}`
	got := runGoSourceRunnerError(t, source)
	if !strings.Contains(got, "signature requires value-semantics parameters") {
		t.Fatalf("missing parameter boundary: %q", got)
	}
	if strings.Contains(got, "callback-ran") {
		t.Fatalf("unsupported body executed: %q", got)
	}
}

func TestS281TestingRunSynchronizesCopiedSliceCallback(t *testing.T) {
	driver := s249Source("_testmain.go", `package main

import (
	"fmt"
	"os"
	"testing"
	"testing/internal/testdeps"
)

type row struct {
	name string
	data []int
}

func TestCopiedSliceRow(t *testing.T) {
	rows := []row{{"first", []int{1, 2}}, {"second", []int{4, 5}}}
	for _, tt := range rows {
		t.Run(tt.name, func(t *testing.T) {
			tt.data[0] += len(tt.data)
			fmt.Println(tt.name, tt.data[0], rows[0].data[0], rows[1].data[0])
		})
	}
	fmt.Println("after", rows[0].data[0], rows[1].data[0])
}

var tests = []testing.InternalTest{{"TestCopiedSliceRow", TestCopiedSliceRow}}

func main() {
	m := testing.MainStart(testdeps.TestDeps{}, tests, nil, nil, nil)
	os.Exit(m.Run())
}
`)
	got, err := runS249PackageTestMain(t, []gosource.Source{driver}, nil)
	const want = "first 3 3 4\nsecond 6 3 6\nafter 3 6\nPASS\n"
	if err != nil || got.stdout != want || got.stderr != "" || got.status != 0 {
		t.Fatalf("run=%v outcome=%+v", err, got)
	}
}
