package interp_test

// Sprint: #118; Story: #64; Story-ID: 45be321bddfb
//
// Focused regressions for the general callback/signature/copied-slice bridge.
// Each differential case runs unchanged original bytes both on the interpreter
// and as a real Go build and requires the two to agree; the boundary cases
// require an explicit refusal rather than a silently wrong answer.
import (
	"strings"
	"testing"
)

// TestGoSourceCallbackSignatures covers what a callback may now declare. A
// value-semantics aggregate is copied by Go itself at the call, so a copied
// transport is faithful; a reference-bearing parameter is not and stays out.
func TestGoSourceCallbackSignatures(t *testing.T) {
	for name, source := range map[string]string{
		"struct parameters": `package main
import ("cmp";"fmt";"slices")
type person struct{name string;age int}
func main(){
	people:=[]person{{"Jax",37},{"TJ",25},{"Alex",72}}
	slices.SortFunc(people,func(a,b person)int{return cmp.Compare(a.age,b.age)})
	fmt.Println(people)
}`,
		"nested struct parameters": `package main
import ("cmp";"fmt";"slices")
type at struct{hour,minute int}
type event struct{name string;when at}
func main(){
	events:=[]event{{"late",at{9,30}},{"early",at{9,5}},{"first",at{8,0}}}
	slices.SortFunc(events,func(a,b event)int{
		if c:=cmp.Compare(a.when.hour,b.when.hour);c!=0{return c}
		return cmp.Compare(a.when.minute,b.when.minute)
	})
	fmt.Println(events)
}`,
		"array parameters": `package main
import ("cmp";"fmt";"slices")
func main(){
	rows:=[][2]int{{3,1},{1,9},{2,4}}
	slices.SortFunc(rows,func(a,b [2]int)int{return cmp.Compare(a[0],b[0])})
	fmt.Println(rows)
}`,
		"named scalar parameters": `package main
import ("cmp";"fmt";"slices")
type celsius float64
func main(){
	values:=[]celsius{3.5,-1.25,2}
	slices.SortFunc(values,func(a,b celsius)int{return cmp.Compare(a,b)})
	fmt.Println(values)
}`,
	} {
		t.Run(name, func(t *testing.T) { differGoSource(t, source, nil, "") })
	}
}

// A callback parameter carrying a reference the caller still shares cannot be
// copied faithfully, so it is refused before the callback or the surrounding
// call has any effect.
func TestGoSourceCallbackSignatureBoundary(t *testing.T) {
	const source = `package main
import ("fmt";"slices")
type bag struct{items []int}
func main(){
	bags:=[]bag{{[]int{2}},{[]int{1}}}
	slices.SortFunc(bags,func(a,b bag)int{a.items[0]=0;return len(a.items)-len(b.items)})
	fmt.Println(bags)
}`
	got := runGoSourceRunnerError(t, source)
	if !strings.Contains(got, "requires value-semantics parameters") {
		t.Fatalf("missing signature boundary: %q", got)
	}
	if strings.Contains(got, "[{[0]}") {
		t.Fatalf("the refused callback still ran: %q", got)
	}
}

// TestGoSourceInterpretedGenericHelpers pins the generic callable set the
// dependency cannot reflect and this Runner therefore answers itself.
func TestGoSourceInterpretedGenericHelpers(t *testing.T) {
	for name, source := range map[string]string{
		"cmp compare and less": `package main
import ("cmp";"fmt")
func main(){
	fmt.Println(cmp.Compare(3,5),cmp.Compare(5,3),cmp.Compare(4,4))
	fmt.Println(cmp.Compare("b","a"),cmp.Compare("a","b"))
	fmt.Println(cmp.Less(1.5,2.5),cmp.Less(2.5,1.5))
}`,
		"sort func by length": `package main
import ("cmp";"fmt";"slices")
func main(){
	fruits:=[]string{"peach","banana","kiwi"}
	lenCmp:=func(a,b string)int{return cmp.Compare(len(a),len(b))}
	slices.SortFunc(fruits,lenCmp)
	fmt.Println(fruits)
}`,
		"sort stable func": `package main
import ("cmp";"fmt";"slices")
func main(){
	words:=[]string{"bb","a","cc","d"}
	slices.SortStableFunc(words,func(a,b string)int{return cmp.Compare(len(a),len(b))})
	fmt.Println(words)
}`,
		"index and contains func": `package main
import ("fmt";"slices")
func main(){
	values:=[]int{4,8,15,16}
	fmt.Println(slices.IndexFunc(values,func(n int)bool{return n>10}))
	fmt.Println(slices.IndexFunc(values,func(n int)bool{return n>100}))
	fmt.Println(slices.ContainsFunc(values,func(n int)bool{return n%2==1}))
	fmt.Println(slices.ContainsFunc(values,func(n int)bool{return n==8}))
}`,
		// The reordering must reach the original backing array, not a copy, so
		// a header aliasing the same storage observes it.
		"mutation reaches aliasing headers": `package main
import ("cmp";"fmt";"slices")
func main(){
	values:=[]int{3,1,2}
	alias:=values[:2]
	slices.SortFunc(values,func(a,b int)int{return cmp.Compare(a,b)})
	fmt.Println(values,alias)
}`,
		// A callback that consults captured interpreter state, and mutates it,
		// keeps doing so while the sort is in flight.
		"callback captures original state": `package main
import ("cmp";"fmt";"slices")
func main(){
	calls:=0
	values:=[]string{"cc","a","bbb"}
	slices.SortFunc(values,func(a,b string)int{calls++;return cmp.Compare(len(a),len(b))})
	fmt.Println(values,calls>0)
}`,
	} {
		t.Run(name, func(t *testing.T) { differGoSource(t, source, nil, "") })
	}
}

// TestGoSourceReadOnlyEmitterWithCallback pins the copied-slice relaxation. A
// structural emitter walks the transported tree once and allocates its own
// output, so an original mirror method may ride along with slice storage.
func TestGoSourceReadOnlyEmitterWithCallback(t *testing.T) {
	for name, source := range map[string]string{
		"xml marshal with stringer": `package main
import ("encoding/xml";"fmt")
type Plant struct{Id int ` + "`xml:\"id,attr\"`" + `;Origin []string ` + "`xml:\"origin\"`" + `}
func (p Plant) String() string{return fmt.Sprintf("Plant id=%v origin=%v",p.Id,p.Origin)}
func main(){
	p:=&Plant{Id:7,Origin:[]string{"a","b"}}
	out,_:=xml.MarshalIndent(p," ","  ")
	fmt.Println(string(out))
	fmt.Println(*p)
}`,
		"json marshal with stringer": `package main
import ("encoding/json";"fmt")
type Plant struct{Id int;Origin []string}
func (p Plant) String() string{return fmt.Sprintf("Plant %v %v",p.Id,p.Origin)}
func main(){
	p:=Plant{Id:7,Origin:[]string{"a","b"}}
	out,_:=json.Marshal(p)
	fmt.Println(string(out))
	fmt.Println(p)
}`,
	} {
		t.Run(name, func(t *testing.T) { differGoSource(t, source, nil, "") })
	}
}

// TestGoSourceCopiedReceiverWrite pins the replacement for the old blanket
// refusal: a value-receiver method on reference-bearing storage now runs, and
// a write THROUGH that shared storage is reported rather than dropped.
func TestGoSourceCopiedReceiverWrite(t *testing.T) {
	const source = `package main
import "fmt"
type bag struct{items []int}
func (b bag) String() string{b.items[0]=99;return "bag"}
func main(){
	b:=bag{items:[]int{1}}
	fmt.Println(b)
	fmt.Println(b.items[0])
}`
	got := runGoSourceRunnerError(t, source)
	if !strings.Contains(got, "wrote through reference storage of a copied receiver") {
		t.Fatalf("a dropped effect was not reported: %q", got)
	}
}

// TestGoSourceNativePointerWriteback pins the general out-parameter path: a
// dependency decoder writes a structural value through a pointer to an
// original variable, and only what it actually changed crosses back.
func TestGoSourceNativePointerWriteback(t *testing.T) {
	for name, source := range map[string]string{
		"json decodes into an original struct": `package main
import ("encoding/json";"fmt")
type Plant struct{Id int;Name string;Origin []string}
func main(){
	var p Plant
	if err:=json.Unmarshal([]byte(` + "`" + `{"Id":3,"Name":"x","Origin":["a","b"]}` + "`" + `),&p);err!=nil{panic(err)}
	fmt.Println(p.Id,p.Name,p.Origin)
}`,
		"json decodes into an original map": `package main
import ("encoding/json";"fmt")
func main(){
	var m map[string]int
	if err:=json.Unmarshal([]byte(` + "`" + `{"a":1}` + "`" + `),&m);err!=nil{panic(err)}
	fmt.Println(m["a"],len(m))
}`,
		// A later unrelated dependency call must not revert original state the
		// dependency never touched.
		"unchanged pointees are not reverted": `package main
import ("encoding/json";"fmt";"strings")
func main(){
	var n int
	if err:=json.Unmarshal([]byte("41"),&n);err!=nil{panic(err)}
	n = n + 1
	fmt.Println(strings.ToUpper("done"),n)
}`,
	} {
		t.Run(name, func(t *testing.T) { differGoSource(t, source, nil, "") })
	}
}

// TestGoSourceNativeMapRange pins ranging over a value the dependency owns:
// the keys come from the dependency's own map and each element is read back
// through the same handle, keeping structured elements as handles.
func TestGoSourceNativeMapRange(t *testing.T) {
	const source = `package main
import ("fmt";"net/url";"sort")
func main(){
	values,err:=url.ParseQuery("a=1&b=2&b=3")
	if err!=nil{panic(err)}
	var lines []string
	for name,items:=range values{
		for _,item:=range items{
			lines=append(lines,fmt.Sprintf("%v=%v",name,item))
		}
	}
	sort.Strings(lines)
	fmt.Println(lines)
}`
	differGoSource(t, source, nil, "")
}

// TestGoSourceRetainedCallbackPolicy pins that admitting retained HTTP handler
// registration did not open every retaining API: an unreviewed one is still
// refused before the dependency can keep the original function.
func TestGoSourceRetainedCallbackPolicy(t *testing.T) {
	const source = `package main
import ("fmt";"os";"path/filepath";"io/fs")
func main(){
	filepath.WalkDir(".",func(path string,d fs.DirEntry,err error)error{fmt.Println(path);return nil})
	os.Exit(0)
}`
	got := runGoSourceRunnerError(t, source)
	if !strings.Contains(got, "callback") {
		t.Fatalf("an unreviewed retaining API was admitted: %q", got)
	}
}
