package interp_test

import (
	"strings"
	"testing"

	"mvdan.cc/sh/v3/gosource"
)

func TestGoSourceBlankTypeDeclarations(t *testing.T) {
	for name, source := range map[string]string{
		"single": `package main
import "fmt"
type _ struct { Value int }
func main() { fmt.Println("ok") }
`,
		"alias": `package main
import "fmt"
type Named struct { Value int }
type _ = Named
func main() { fmt.Println(Named{7}) }
`,
		"repeated_and_nested": `package main
import ("fmt"; "reflect")
type _ struct { Inner struct { Value int }; _ int }
type _ int
type Named struct { _ int; Value int }
var calls int
func touch() int { calls++; return calls }
var _ = touch()
func main() {
 type _ = string
 _ = touch()
 v := Named{touch(), 9}
 a := struct { Value int }{11}
 fmt.Println(calls, v.Value, reflect.ValueOf(v).NumField(), reflect.ValueOf(v).Field(0).Int(), a)
}
`,
	} {
		t.Run(name, func(t *testing.T) { differGoSource(t, source, nil, "") })
	}
}

func TestGoSourceBlankTypeNamesRemainUnusable(t *testing.T) {
	for _, source := range []string{
		"package main\ntype _ int\nfunc main(){var x _;_ = x}",
		"package main\ntype Named int\ntype Named string\nfunc main(){}",
	} {
		if _, err := gosource.Parse(strings.NewReader(source), "invalid.go", gosource.Options{RunMain: true}); err == nil {
			t.Fatalf("accepted invalid declaration: %s", source)
		}
	}
}
