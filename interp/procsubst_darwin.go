//go:build darwin

package interp

import (
	"fmt"
	"os"
	"strconv"
)

type procSubstDarwinPipe struct {
	name     string
	endpoint *os.File
}

// A Darwin FIFO open in progress can lose its peer when another goroutine
// forks, even with O_CLOEXEC. Anonymous pipes have no blocking open rendezvous;
// the existing ExtraFiles handoff preserves their /dev/fd names in children.
func (r *Runner) newProcSubstPipe(substWrites bool) (procSubstPipe, error) {
	read, write, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	published, endpoint := read, write
	if !substWrites {
		published, endpoint = write, read
	}
	limit := r.effectiveNofileLimit()
	available := func(fd int) bool {
		return r.fdTable[fd] == nil && r.fdWriteTable[fd] == nil && (!r.inheritedFds[fd] || r.fdClosedTable[fd]) && r.procSubstDescriptors[fd] == nil
	}
	fd := min(63, limit-1)
	for ; fd >= 3; fd-- {
		if available(fd) {
			break
		}
	}
	if fd < 3 {
		for fd = 64; fd < limit; fd++ {
			if available(fd) {
				break
			}
		}
	}
	if fd >= limit || fd < 3 {
		read.Close()
		write.Close()
		return nil, fmt.Errorf("process substitution exceeds open-files limit")
	}
	name := "/dev/fd/" + strconv.Itoa(fd)
	if r.procSubstDescriptors == nil {
		r.procSubstDescriptors = make(map[int]*procSubstDescriptor)
	}
	delete(r.fdClosedTable, fd)
	if r.procSubstDescriptorFDs == nil {
		r.procSubstDescriptorFDs = make(map[int]bool)
	}
	r.procSubstDescriptorFDs[fd] = true
	r.procSubstDescriptors[fd] = &procSubstDescriptor{file: published, read: substWrites}
	return &procSubstDarwinPipe{name: name, endpoint: endpoint}, nil
}
func (p *procSubstDarwinPipe) path() string                  { return p.name }
func (p *procSubstDarwinPipe) openWriter() (*os.File, error) { return p.endpoint, nil }
func (p *procSubstDarwinPipe) openReader() (*os.File, error) { return p.endpoint, nil }
func (p *procSubstDarwinPipe) cleanup()                      { _ = p.endpoint.Close() }

func (p *procSubstDarwinPipe) prepareSubshell(r *Runner) error {
	if r.procSubstOwnIO {
		return nil // The lineage clone already owns its current bindings.
	}
	return r.cloneProcSubstIO()
}
