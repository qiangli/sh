from pathlib import Path
root=Path.home()/'s374/memory';src=Path.home()/'sdk/go1.27.1/test';out=root/'controls';out.mkdir(exist_ok=True)
for n in (4,16):
 s=(src/'fixedbugs/issue20780b.go').read_text().replace('2e6','4096').replace('func main()','func work()')
 s+=f'\nfunc main() {{ for i:=0;i<{n};i++ {{work();if i=={n//2-1} || i=={n-1} {{println("MEMPOINT")}} }} }}\n'
 (out/f'20780b-repeat-{n}.go').write_text(s)
 s=(src/'peano.go').read_text().replace('max := 9','max := 6').replace('func main()','func work()')
 s+=f'\nfunc main() {{ for i:=0;i<{n};i++ {{work();if i=={n//2-1} || i=={n-1} {{println("MEMPOINT")}} }} }}\n'
 (out/f'peano-repeat-{n}.go').write_text(s)
