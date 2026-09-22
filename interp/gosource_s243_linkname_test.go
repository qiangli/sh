//go:build full

package interp_test

// Sprint: #243; Story: #674; Story-ID: 63073886bfce

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func TestS243LinknameStorageOriginal(t *testing.T) {
	dir := filepath.Join(runtime.GOROOT(), "test", "fixedbugs", "issue42401.dir")
	read := func(name string) gosource.Source {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return gosource.Source{Name: name, Data: data}
	}
	out, stderr := runGoSourcePackageSet(t, "issue42401", string(read("b.go").Data), []gosource.PackageSpec{
		{Path: "test/a", Sources: []gosource.Source{read("a.go")}},
	})
	if out != "" || stderr != "" {
		t.Fatalf("original output stdout=%q stderr=%q", out, stderr)
	}
}

func TestS243LinknameStorageControls(t *testing.T) {
	src := func(name, text string) gosource.Source { return gosource.Source{Name: name, Data: []byte(text)} }
	dep := src("a.go", `package a
var S = mark()
var Other = "separate"
var initializations int
func mark() string { initializations++; return "initial" }
func Get() (string,string,int) { return S,Other,initializations }
func Set(v string) { S=v }
`)
	main := `package main
import ("fmt"; _ "unsafe"; "./a")
//go:linkname linked test/a.S
var linked string
var S = "main"
func main(){
 v,o,n:=a.Get(); fmt.Println(v,o,n,linked,S)
 linked="from-main"; v,o,n=a.Get(); fmt.Println(v,o,n,linked,S)
 a.Set("from-a"); v,o,n=a.Get(); fmt.Println(v,o,n,linked,S)
}`
	out, stderr := runGoSourcePackageSet(t, "linkname-controls", main, []gosource.PackageSpec{{Path: "test/a", Sources: []gosource.Source{dep}}})
	want := "initial separate 1 initial main\nfrom-main separate 1 from-main main\nfrom-a separate 1 from-a main\n"
	if out != want || stderr != "" {
		t.Fatalf("controls stdout=%q want=%q stderr=%q", out, want, stderr)
	}
}

func TestS243LinknameMissingTargetRefuses(t *testing.T) {
	main := gosource.Source{Name: "main.go", Data: []byte(`package main
import _ "unsafe"
//go:linkname linked missing/package.value
var linked string
func main(){ _ = linked }
`)}
	program, err := gosource.Load([]gosource.Source{main}, gosource.Options{RunMain: true})
	if err != nil {
		// Newer frontend authority may reject this before execution.
		return
	}
	var stdout, stderr bytes.Buffer
	runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.StdIO(nil, &stdout, &stderr))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = runner.Run(ctx, program.File)
	if err == nil {
		t.Fatalf("missing target executed successfully: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "go:linkname target") || !strings.Contains(stderr.String(), "is not a linked package variable") {
		t.Fatalf("missing target was not truthfully refused: program=%v stderr=%q", program != nil, stderr.String())
	}
}
