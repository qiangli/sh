import subprocess,time,os,sys,json,signal
label=sys.argv[1]; start=time.monotonic();peak=0;root_peak=0;reason='exit'
with open(label+'.out','w') as out,open(label+'.err','w') as err:
 p=subprocess.Popen(sys.argv[2:],stdout=out,stderr=err,start_new_session=True)
 while p.poll() is None:
  rows=subprocess.check_output(['/bin/ps','-axo','pid=,ppid=,rss='],text=True)
  rows=[tuple(map(int,r.split())) for r in rows.splitlines() if len(r.split())==3]
  ids={p.pid}
  for _ in range(20):
   more={pid for pid,ppid,rss in rows if ppid in ids}; old=len(ids);ids|=more
   if old==len(ids):break
  rss=sum(rss for pid,ppid,rss in rows if pid in ids);peak=max(peak,rss);root_peak=max(root_peak,next((rss for pid,ppid,rss in rows if pid==p.pid),0))
  if rss>3906250 or time.monotonic()-start>120:
   reason='RSS' if rss>3906250 else 'TIME'
   for pid in ids:
    try:os.kill(pid,signal.SIGKILL)
    except ProcessLookupError:pass
   break
  time.sleep(.05)
 rc=p.wait()
record=dict(label=label,rc=rc,reason=reason,seconds=round(time.monotonic()-start,3),peak_KiB=peak,root_peak_KiB=root_peak)
print(json.dumps(record),flush=True)
with open('results.jsonl','a') as f:f.write(json.dumps(record)+'\n')
sys.exit(0 if rc==0 else 1)
