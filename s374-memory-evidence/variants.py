from pathlib import Path
root=Path.home()/'s374/memory';src=Path.home()/'sdk/go1.27.1/test';out=root/'variants';out.mkdir(exist_ok=True)
for n in (100,400,1000):
 s=(src/'fixedbugs/issue80188.go').read_text().replace('for range 10000','for range 10').replace('for range 4','for range 1').replace('for x := range 10000',f'for x := range {n}')
 (out/f'80188-{n}.go').write_text(s)
for n in (16384,65536,262144):
 s=(src/'fixedbugs/issue15277.go').read_text().replace('10 << 20',str(n)).replace('9<<20',str(n*9//10)).replace('1<<20',str(n//10))
 (out/f'15277-{n}.go').write_text(s)
for n in (1000,4000,16000):
 s=(src/'fixedbugs/issue20780b.go').read_text().replace('2e6',str(n))
 (out/f'20780b-{n}.go').write_text(s)
for n in (4,5,6):
 s=(src/'peano.go').read_text().replace('max := 9',f'max := {n}')
 (out/f'peano-{n}.go').write_text(s)
for n in (20,100,300):
 s=(src/'fixedbugs/issue78081.go').read_text().replace('i < 16','i < 1').replace('range 100000',f'range {n}').replace('useStack(64)','useStack(8)')
 (out/f'78081-{n}.go').write_text(s)
for n in (1,2,3):
 s=(src/'rangegen.go').read_text().replace('max = 5',f'max = {n}')
 (out/f'rangegen-{n}.go').write_text(s)
for n in (100,500,2000):
 s=(src/'fixedbugs/issue39541.go').read_text().replace('10000',str(n)).replace('i < 100','i < 1')
 (out/f'39541-{n}.go').write_text(s)
for p in out.glob('*.go'):
 s=p.read_text();name=p.name
 if name.startswith('80188-'):
  n=int(p.stem.split('-')[1]);s=s.replace('all = append(all, f(x))',f'all = append(all, f(x)); if x == {n//2-1} || x == {n-1} {{ println("MEMPOINT") }}')
 elif name.startswith('15277-'):
  s=s.replace('runtime.KeepAlive(x)','println("MEMPOINT"); runtime.KeepAlive(x)',1).replace('x = nil','x = nil; println("MEMPOINT")')
 elif name.startswith('20780b-'):
  s=s.replace('g(7, f(7))','g(7, f(7)); println("MEMPOINT")').replace('h(f(0), x1, f(2), x3, f(4))','h(f(0), x1, f(2), x3, f(4)); println("MEMPOINT")')
 elif name.startswith('peano-'):
  s=s.replace('for i := 0; i <= max; i++ {','for i := 0; i <= max; i++ { println("MEMPOINT")')
 elif name.startswith('78081-'):
  n=int(p.stem.split('-')[1]);s=s.replace(f'for range {n} {{',f'for j := range {n} {{').replace('f(&b)',f'f(&b); if j == {n//2-1} || j == {n-1} {{ println("MEMPOINT") }}')
 elif name.startswith('rangegen-'):
  s=s.replace('for i := 1; i <= max; i++ {','for i := 1; i <= max; i++ { println("MEMPOINT")')
  s=s.replace('flush(false)','flush(false)')
  s=s.replace('flush(true)','println("MEMPOINT"); flush(true)')
 elif name.startswith('39541-'):
  n=int(p.stem.split('-')[1]);s=s.replace('f()\n',f'f(); if j == {n//2-1} || j == {n-1} {{ println("MEMPOINT") }}\n')
 p.write_text(s)
