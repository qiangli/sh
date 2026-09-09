package interp_test

// Sprint: #118; Story: #52; Story-ID: d564bada90bb
import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGoSourceOriginalTypeRegistryThreeModes(t *testing.T) {
	for name, want := range map[string]string{
		"index.go.txt":          "398dd72a4b01efa279d571667bedbcccd1bbbceaeb77218635db48edf38fd983",
		"list.go.txt":           "ab72fc4fd321f507abde796abe0830876e0f8bcd8661d9ce4ac34c3da2f68060",
		"slice-literals.go.txt": "b085416ef12627bbd6034c78314d61df30a66e4b5d2b3b6cbfebd5befa27c897",
	} {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join("testdata/gosource-type-registry", name))
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprintf("%x", sha256.Sum256(source)) != want {
				t.Fatal("original bytes changed")
			}
			typedSendThreeModes(t, string(source))
		})
	}
}
func TestGoSourceTypeRegistryThreeModes(t *testing.T) {
	cases := map[string]string{
		"typed_alias_conversions":  `package main;import "fmt";type Byte=byte;func text()string{fmt.Println("text");return "hé"};func main(){var a []Byte=[]Byte(text());var b []uint8=[]uint8("hi");var c []int32=[]int32("hé");fmt.Printf("%T %v %s %T %v %s %T %v %s\n",a,a,string(a),b,b,string(b),c,c,string(c))}`,
		"alias_identity":           `package main;import "fmt";type Byte = byte;type Bytes = []Byte;func main(){xs:=Bytes("hi");fmt.Printf("%T %v %s\n",xs,xs,string(xs))}`,
		"entry_protocol_collision": `package main;import "fmt";type entry struct{N int};func main(){m:=map[string][]entry{"a":{{1}}};fmt.Printf("%T %v\n",m,m)}`,
		"local_point":              `package main;import "fmt";func main(){type point struct{X,Y int};p:=make([]point,0,3);fmt.Printf("%T %v\n",p,p[:3])}`,
		"anonymous_private_tag":    "package main;import \"fmt\";func main(){x:=struct{ n int `json:\"n\"` }{7};fmt.Printf(\"%T %v\\n\",x,x)}",
		"generic_explicit":         `package main;import "fmt";func First[T any](xs []T)T{return xs[0]};func main(){fmt.Println(First[int]([]int{7}),First[string]([]string{"yes"}))}`,
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) { typedSendThreeModes(t, source) })
	}
}

// These valid Go programs deliberately exceed the represented lexical type
// namespace. They must fail before a dependency can observe conflated types.
func TestGoSourceTypeRegistryRejectsAmbiguousScope(t *testing.T) {
	for name, source := range map[string]string{
		"local_local":   `package main;import "fmt";func one(){type point struct{X int};fmt.Println(point{1})};func two(){type point struct{X int};fmt.Println(point{2})};func main(){one();two()}`,
		"package_local": `package main;import "fmt";type point struct{X int};func main(){type point struct{X int};fmt.Println(point{1})}`,
	} {
		t.Run(name, func(t *testing.T) {
			program, err := gosource.Parse(strings.NewReader(source), "ambiguous.go", gosource.Options{RunMain: true})
			if err != nil {
				t.Fatal(err)
			}
			var out, errout bytes.Buffer
			runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.Dir(t.TempDir()), interp.StdIO(nil, &out, &errout))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err = runner.Run(ctx, program.File)
			if err == nil {
				t.Fatalf("ambiguous types accepted: %q", out.String())
			}
			diagnostic := err.Error() + errout.String()
			if !strings.Contains(diagnostic, "unregistered bridge type") && !strings.Contains(diagnostic, "redeclared") {
				t.Fatalf("unexpected failure: %v stderr=%q", err, errout.String())
			}
			if out.Len() != 0 {
				t.Fatalf("dependency observed ambiguous type: %q", out.String())
			}
		})
	}
}
