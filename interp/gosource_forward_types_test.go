package interp_test

// Sprint: #118; Story: #53; Story-ID: 99bd1de0093b
import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// forwardTypeOriginal returns Tour bytes after checking them against the digest
// recorded here, so a test can never be made to pass by editing its input.
func forwardTypeOriginal(t *testing.T, name, digest string) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("testdata/gosource-type-registry", name))
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(source)); got != digest {
		t.Fatalf("original bytes changed: %s", got)
	}
	return string(source)
}

// The Tour's crawler exercise declares `type fakeFetcher map[string]*fakeResult`
// above `type fakeResult struct{...}`. Package scope covers the whole file in
// Go, so this is ordinary source that the runtime rejected as
// `undefined type: fakeResult` while it registered declarations in the order it
// executed them.
func TestGoSourceOriginalForwardTypeThreeModes(t *testing.T) {
	source := forwardTypeOriginal(t, "exercise-web-crawler.go.txt",
		"bd6226dcae3b663a3aa1cbf502d840357ac0efb6de46e7a66612ca2834cf5e37")
	typedSendThreeModes(t, source)
}

// Two same-cluster originals still fail, for reasons that are not declaration
// registration. Pinning the diagnostic keeps each gap visible and keeps it from
// silently turning back into a registration failure.
func TestGoSourceOriginalForwardTypeRemainingGaps(t *testing.T) {
	for _, tc := range []struct{ name, digest, want string }{
		// Embeds sync.Mutex in an anonymous struct; the embedded imported
		// method set is what is missing, not the type's registration.
		{"webcrawler.go.txt", "490f194b0e0610dde7196a8cb2ebec643586a6f05f94a1aabe7a29fb526b8882", "has no method Lock"},
		// MyError carries a time.Time field, which the dependency helper cannot
		// materialise, so no codec is emitted for the local type at all.
		{"methods-errors.go.txt", "a0825fe6310af87e48e7f46b5290814df190d1e9533d0132fd663d6bb7f3236e", `unregistered bridge type "*MyError"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := forwardTypeOriginal(t, tc.name, tc.digest)
			program, err := gosource.Parse(strings.NewReader(source), tc.name, gosource.Options{RunMain: true})
			if err != nil {
				t.Fatalf("gosource rejected an original: %v", err)
			}
			var out, errout bytes.Buffer
			runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.Dir(t.TempDir()), interp.StdIO(nil, &out, &errout))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			err = runner.Run(ctx, program.File)
			if err == nil {
				t.Fatalf("documented gap now passes; promote %s to a three-mode case", tc.name)
			}
			diagnostic := err.Error() + errout.String()
			if strings.Contains(diagnostic, "undefined type") {
				t.Fatalf("declaration registration regressed: %s", diagnostic)
			}
			if !strings.Contains(diagnostic, tc.want) {
				t.Fatalf("gap changed: want %q, got %q", tc.want, diagnostic)
			}
		})
	}
}

// Forward and recursive references across every position a named type can hold
// in a declaration, each checked against real Go.
func TestGoSourceForwardTypeDeclarationsThreeModes(t *testing.T) {
	for name, source := range map[string]string{
		"map_element_forward":   `package main;import "fmt";type table map[string]*row;type row struct{n int};func main(){t:=table{"a":&row{1}};fmt.Println(t["a"].n)}`,
		"slice_element_forward": `package main;import "fmt";type bag []leaf;type leaf struct{n int};func main(){b:=bag{{1},{2}};fmt.Println(len(b),b[1].n)}`,
		"struct_field_forward":  `package main;import "fmt";type outer struct{in *inner};type inner struct{n int};func main(){o:=outer{&inner{3}};fmt.Println(o.in.n)}`,
		// The chan declaration is what must register; a named channel type has
		// its own unrelated runtime gaps, so nothing here constructs one.
		"chan_element_forward": `package main;import "fmt";type pipe chan *item;type item struct{n int};func main(){fmt.Println(item{4}.n)}`,
		"alias_forward":        `package main;import "fmt";type ref = *cell;type cell struct{n int};func main(){var r ref=&cell{5};fmt.Println(r.n)}`,
		// Neither declaration can be moved ahead of the other, so ordering
		// declarations by dependency could not have fixed this.
		"mutual_pointer_recursion": `package main;import "fmt";type a struct{b *b};type b struct{a *a};func main(){x:=&a{};y:=&b{a:x};x.b=y;fmt.Println(x.b.a==x)}`,
		"self_pointer_recursion":   `package main;import "fmt";type node struct{next *node;n int};func main(){n2:=&node{n:2};n1:=&node{next:n2,n:1};fmt.Println(n1.n,n1.next.n)}`,
		"generic_self_reference":   `package main;import "fmt";type List[T any] struct{next *List[T];val T};func main(){l:=&List[int]{val:1};l.next=&List[int]{val:2};fmt.Println(l.val,l.next.val)}`,
		"method_on_forward_type":   `package main;import "fmt";type holder map[string]*payload;func (h holder) get(k string)int{return h[k].n};type payload struct{n int};func main(){fmt.Println(holder{"k":&payload{6}}.get("k"))}`,
		"const_uses_forward_type":  `package main;import "fmt";const limit size=7;type size int;func main(){fmt.Println(limit)}`,
		// Package initialization still runs in dependency order; only the type
		// registry is populated ahead of the statements.
		"initializer_order": `package main;import "fmt";var second=first+1;var first=1;type later struct{n int};var third=&later{second};func main(){fmt.Println(first,second,third.n)}`,
	} {
		t.Run(name, func(t *testing.T) { typedSendThreeModes(t, source) })
	}
}

// Pre-registration must widen nothing but package scope. Each of these is
// rejected for a reason that survives it. Rejection may come from the type
// checker or from the runtime; what matters is that neither name is quietly
// resolved to something the source did not mean.
func TestGoSourceForwardTypeRegistryRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		source string
		want   []string
	}{
		// A type declared inside a function body is not a package declaration,
		// so pre-registration must not hoist it. The runtime has no lexical
		// type namespace, so the reused spelling is refused outright rather
		// than conflated with the package type.
		"local_type_not_hoisted": {
			`package main;import "fmt";type seen struct{n int};func main(){fmt.Println(seen{1});type seen struct{s string};fmt.Println(seen{"x"})}`,
			[]string{"unregistered bridge type", "redeclared"},
		},
		// Infinite value layouts, now reachable because both names resolve.
		"mutual_value_recursion": {
			`package main;type a struct{b b};type b struct{a a};func main(){}`,
			[]string{"cyclic type declaration", "invalid recursive type"},
		},
		"self_value_recursion": {
			`package main;type loop struct{next loop};func main(){}`,
			[]string{"cyclic type declaration", "invalid recursive type"},
		},
		// A named constraint is still enforced; only `any` is free.
		"named_constraint_violated": {
			`package main;import "fmt";type num interface{~int};func sum[T num](xs []T)T{var t T;for _,x:=range xs{t+=x};return t};func main(){fmt.Println(sum([]string{"a"}))}`,
			[]string{"does not satisfy", "num"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			accept := func(diagnostic string) {
				t.Helper()
				for _, want := range tc.want {
					if strings.Contains(diagnostic, want) {
						return
					}
				}
				t.Fatalf("want one of %q, got %q", tc.want, diagnostic)
			}
			program, err := gosource.Parse(strings.NewReader(tc.source), name+".go", gosource.Options{RunMain: true})
			if err != nil {
				accept(err.Error())
				return
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
				t.Fatalf("accepted: %q", out.String())
			}
			accept(err.Error() + errout.String())
		})
	}
}

// Registration is a gosource-only rule. Classic Bash++ has no package scope, so
// a name must still be declared before it is used.
func TestBashPPClassicTypesStaySequential(t *testing.T) {
	for name, tc := range map[string]struct{ source, want string }{
		"forward_reference_still_undefined": {"type Fetcher map[string]*Result\ntype Result struct { n int }\n", "undefined type: Result"},
		"undefined_name_still_undefined":    {"type Broken Missing\n", "undefined type: Missing"},
		"redeclaration_still_refused":       {"type Dup int\ntype Dup int\n", "type Dup redeclared in this session"},
	} {
		t.Run(name, func(t *testing.T) {
			var out strings.Builder
			runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.StdIO(nil, &out, &out))
			if err != nil {
				t.Fatal(err)
			}
			file, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(tc.source), "classic.bpp")
			if err != nil {
				t.Fatal(err)
			}
			_ = runner.Run(context.Background(), file)
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("classic Bash++ did not report %q: %q", tc.want, out.String())
			}
		})
	}
}
