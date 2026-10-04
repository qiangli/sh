from pathlib import Path
root=Path.home()/'s374/memory';src=Path.home()/'sdk/go1.27.1/test';out=root/'finalcontrols';out.mkdir(exist_ok=True)
s=(src/'fixedbugs/issue39541.go').read_text().replace('10000','100000').replace('i < 100','i < 1').replace('f()\n','f(); if j == 49999 || j == 99999 { println("MEMPOINT") }\n')
(out/'39541-100000.go').write_text(s)
for n in (2,8):
 s=(src/'rangegen.go').read_text().replace('max = 5','max = 1').replace('func main()','func work()')
 s+=f'\nfunc main() {{for i:=0;i<{n};i++{{work();if i=={n//2-1} || i=={n-1} {{println("MEMPOINT")}}}}}}\n'
 (out/f'rangegen-repeat-{n}.go').write_text(s)
for keep in (True,False):
 s='''package main
import "runtime"
func arm(done chan bool) {
 x:=new([1024]byte)
 runtime.SetFinalizer(x,func(*[1024]byte){done<-true})
 KEEP
}
func main(){done:=make(chan bool,1);arm(done);for i:=0;i<3;i++{runtime.GC()};select{case <-done:println("finalized");default:println("NOT finalized")};println("MEMPOINT")}
'''.replace('KEEP','runtime.KeepAlive(x)' if keep else 'x[0]=1')
 (out/f'finalizer-transport-{keep}.go').write_text(s)
