package polyglot

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os/exec"
)

// StreamSignature reports an analyzed export's streaming adapter. A missing
// signature is not guessed from an arbitrary callable or its result.
func (m *Module) StreamSignature(name string) (Signature, bool) {
	for _, export := range m.plan.Exports {
		if export.Name == name && export.Signature.Iterator != "" {
			return export.Signature, true
		}
	}
	return Signature{}, false
}

// StreamCommand constructs, but does not start, the real child used by a
// generated iterator/filter adapter. The caller owns its existing bounded
// process substrate and job-group lifecycle; this package adds no process
// supervisor. Iterator stdout is NDJSON; filter stdout is raw text. The
// protocol request is a separate argv value, so stdin remains a real pipe on
// Windows too (no inherited fd-number assumptions).
func (m *Module) StreamCommand(name string, args []any, filter bool) (*exec.Cmd, error) {
	p, ok := m.runtime.(Python)
	if !ok {
		return nil, errors.New("polyglot: streaming runtime is not Python")
	}
	sig, ok := m.StreamSignature(name)
	if !ok || filter && !sig.Filter {
		return nil, errors.New("polyglot: function has no matching iterator/filter annotation")
	}
	request, err := json.Marshal(map[string]any{"source": m.plan.Source, "name": name, "args": args, "filter": filter})
	if err != nil {
		return nil, err
	}
	argv := p.pythonArguments(pythonStreamWorker, true)
	argv = append(argv, base64.StdEncoding.EncodeToString(request))
	executable, argv := workerExecArgs(p.executable(), argv)
	cmd := exec.Command(executable, argv...)
	p.configure(cmd)
	return cmd, nil
}

const pythonStreamWorker = `
import sys, base64, json
request=json.loads(base64.b64decode(sys.argv.pop()))
` + pythonPathBootstrap + `
import contextlib, traceback
output=sys.stdout
ns={'__name__':'__bashpp__'}
iterator=None
try:
    with contextlib.redirect_stdout(sys.stderr):
        exec(compile('from __future__ import annotations\n'+request['source'],'<bash++ python stream>','exec'),ns,ns)
        args=request['args'] or []
        if request['filter']: args=[sys.stdin]+args
        iterator=iter(ns[request['name']](*args))
        for value in iterator:
            if request['filter']:
                if not isinstance(value,str): raise TypeError('TextIO filter must yield str')
                output.write(value)
            else:
                output.write(json.dumps(value,separators=(',',':'))+'\n')
            output.flush()
except BrokenPipeError:
    sys.exit(141)
except BaseException:
    traceback.print_exc(file=sys.stderr)
    sys.exit(1)
finally:
    close=getattr(iterator,'close',None)
    if close is not None: close()
`
