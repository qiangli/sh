from pathlib import Path
root=Path.home()/'s374/memory';src=Path.home()/'sdk/go1.27.1/test';out=root/'variants2';out.mkdir(exist_ok=True)
for n in (5000,20000):
 s=(src/'fixedbugs/issue39541.go').read_text().replace('10000',str(n)).replace('i < 100','i < 1').replace('f()\n',f'f(); if j == {n//2-1} || j == {n-1} {{ println("MEMPOINT") }}\n')
 (out/f'39541-{n}.go').write_text(s)
for n in (32768,131072):
 s=(src/'fixedbugs/issue20780b.go').read_text().replace('2e6',str(n)).replace('func h(x0, x1, x2, x3, x4 Big) {','func h(x0, x1, x2, x3, x4 Big) { println("MEMPOINT")').replace('g(7, f(7))','g(7, f(7)); println("MEMPOINT")')
 (out/f'20780b-{n}.go').write_text(s)
for n in (6,7):
 s=(src/'peano.go').read_text().replace('max := 9',f'max := {n}').replace('func count(x *Number) int {\n\tif is_zero(x) {','func count(x *Number) int {\n\tif is_zero(x) { println("MEMPOINT")')
 (out/f'peano-{n}.go').write_text(s)
for n in (300,3000):
 s=(src/'fixedbugs/issue78081.go').read_text().replace('i < 16','i < 1').replace('range 100000',f'j := range {n}').replace('useStack(64)','useStack(8)').replace('f(&b)',f'f(&b); if j == {n//2-1} || j == {n-1} {{ println("MEMPOINT") }}')
 (out/f'78081-{n}.go').write_text(s)
for calls in (4,16):
 for keep in (True,False):
  s='''package main
import "runtime"
type big [16384]byte
func f(x *big) {
KEEP
x=nil
runtime.GC()
}
func main(){
for i:=0;i<CALLS;i++ {f(new(big));if i==CALLS/2-1 || i==CALLS-1 {println("MEMPOINT")}}
}'''.replace('CALLS',str(calls)).replace('KEEP','runtime.KeepAlive(x)' if keep else 'if x[0]!=0 {panic("bad")}')
  (out/f'15277-churn-{keep}-{calls}.go').write_text(s)
