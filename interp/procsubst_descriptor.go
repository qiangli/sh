package interp

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// Darwin process substitutions use anonymous pipes. Each shell copy owns its
// published descriptors; a statement retains its endpoints until all commands
// using the expanded paths have returned, as with Bash's /dev/fd implementation.
type procSubstDescriptor struct {
	file *os.File
	read bool
}

func (r *Runner) procSubstDescriptor(path string) *procSubstDescriptor {
	if len(r.procSubstDescriptors) == 0 || !strings.HasPrefix(path, "/dev/fd/") {
		return nil
	}
	fd, err := strconv.Atoi(strings.TrimPrefix(path, "/dev/fd/"))
	if err != nil || strconv.Itoa(fd) != strings.TrimPrefix(path, "/dev/fd/") {
		return nil
	}
	return r.procSubstDescriptors[fd]
}
func (r *Runner) openProcSubstDescriptor(path string, flags int) (*os.File, bool, error) {
	fd, owned := r.ownedProcSubstDescriptorFD(path)
	if !owned {
		return nil, false, nil
	}
	if r.fdClosedTable[fd] {
		return nil, true, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
	}
	read, write, ok := r.fdCaps(fd)
	var source *os.File
	if flags&3 == os.O_RDONLY {
		source = read
	} else if flags&3 == os.O_WRONLY {
		source, _ = write.(*os.File)
	}
	if !ok || source == nil {
		return nil, true, &os.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
	}

	f, _, err := dupPipeFd(source)
	return f, true, err
}
func (r *Runner) procSubstScope() func() {
	previous := maps.Clone(r.procSubstDescriptors)
	return func() {
		for fd, p := range r.procSubstDescriptors {
			if previous[fd] != p {
				_ = p.file.Close()
				if r.fdTable[fd] == nil && r.fdWriteTable[fd] == nil {
					if r.fdClosedTable == nil {
						r.fdClosedTable = make(map[int]bool)
					}
					r.fdClosedTable[fd] = true
				}
			}
		}
		r.procSubstDescriptors = previous
	}
}
func (r *Runner) cloneProcSubstDescriptors(child *Runner) map[int]*procSubstDescriptor {
	if len(r.procSubstDescriptors) == 0 {
		return nil
	}
	result := make(map[int]*procSubstDescriptor, len(r.procSubstDescriptors))
	for fd, p := range r.procSubstDescriptors {
		if !fileOpen(p.file) {
			continue
		}
		f, _, err := dupPipeFd(p.file)
		if err != nil {
			for _, p := range result {
				_ = p.file.Close()
			}
			child.exit.fatal(err)
			return nil
		}
		result[fd] = &procSubstDescriptor{file: f, read: p.read}
	}
	return result
}
func (r *Runner) excludeProcSubstDescriptor(path string) {
	if p := r.procSubstDescriptor(path); p != nil {
		_ = p.file.Close()
		fd, _ := strconv.Atoi(strings.TrimPrefix(path, "/dev/fd/"))
		delete(r.procSubstDescriptors, fd)
	}
}
func (r *Runner) closeProcSubstDescriptors() {
	for _, p := range r.procSubstDescriptors {
		_ = p.file.Close()
	}
	r.procSubstDescriptors = nil
}

func (r *Runner) closeSubshellFiles() {
	r.closeProcSubstDescriptors()
	r.closeProcSubstIO()
	r.closeDirFile()
}
func (r *Runner) reconcileProcSubstDescriptors() {
outer:
	for fd, p := range r.procSubstDescriptors {
		for _, scope := range r.redirScopes {
			if scope.procSubstRestoreFD != nil && scope.procSubstRestoreFD(fd) {
				continue outer
			}
		}

		if r.fdClosedTable[fd] || r.fdTable[fd] != nil && r.fdTable[fd] != p.file || r.fdWriteTable[fd] != nil && r.fdWriteTable[fd] != p.file {
			_ = p.file.Close()
		}
	}
}

func (r *Runner) ownedProcSubstDescriptorFD(path string) (int, bool) {
	if runtime.GOOS != "darwin" || !strings.HasPrefix(path, "/dev/fd/") {
		return 0, false
	}
	fd, err := strconv.Atoi(strings.TrimPrefix(path, "/dev/fd/"))
	return fd, err == nil && strconv.Itoa(fd) == strings.TrimPrefix(path, "/dev/fd/") && r.procSubstDescriptorFDs[fd]
}

func (r *Runner) prepareOwnedChildDescriptors(cmd *exec.Cmd) (func(), error) {
	var duplicates []*os.File
	cleanup := func() {
		for _, f := range duplicates {
			_ = f.Close()
		}
	}
	if runtime.GOOS != "darwin" || len(r.procSubstDescriptorFDs) == 0 {
		return cleanup, nil
	}
	fds := make([]int, 0, len(r.procSubstDescriptorFDs))
	for fd := range r.procSubstDescriptorFDs {
		if fd >= 3 {
			fds = append(fds, fd)
		}
	}
	slices.Sort(fds)
	planned := slices.Clone(cmd.ExtraFiles)
	var closed *os.File
	for _, fd := range fds {
		if fd >= 1<<20 {
			cleanup()
			return func() {}, fmt.Errorf("owned descriptor %d exceeds handoff bound", fd)
		}
		read, write, ok := r.fdCaps(fd)
		source := read
		if source == nil {
			source, _ = write.(*os.File)
		}
		if ok && source == nil {
			cleanup()
			return func() {}, fmt.Errorf("owned descriptor %d is not an OS file", fd)
		}
		slot := fd - 3
		if slot < len(planned) && fileOpen(planned[slot]) {
			if source == planned[slot] {
				continue
			}
			cleanup()
			return func() {}, fmt.Errorf("owned descriptor %d conflicts with child ExtraFiles", fd)
		}
		for len(planned) <= slot {
			planned = append(planned, nil)
		}
		if !ok || !fileOpen(source) {
			if closed == nil {
				var err error
				closed, err = os.Open(os.DevNull)
				if err != nil {
					cleanup()
					return func() {}, err
				}
				_ = closed.Close()
			}
			planned[slot] = closed
			continue
		}
		duplicate, _, err := dupPipeFd(source)
		if err != nil {
			cleanup()
			return func() {}, err
		}
		duplicates = append(duplicates, duplicate)
		planned[slot] = duplicate
	}
	cmd.ExtraFiles = planned
	// This reserved key is an existing child-shell descriptor declaration.
	// Reconcile owned slots only; an absent key stays absent (including env -i).
	env := slices.Clone(cmd.Env)
	if cmd.Env == nil {
		env = cmd.Environ()
	}
	for i, entry := range env {
		value, exists := strings.CutPrefix(entry, BashyInheritedFdsEnv+"=")
		if !exists {
			continue
		}
		var entries []string
		for _, part := range strings.Split(value, ",") {
			fd, err := strconv.Atoi(part)
			if part != "" && (err != nil || !r.procSubstDescriptorFDs[fd]) {
				entries = append(entries, part)
			}
		}
		for _, fd := range fds {
			if fileOpen(planned[fd-3]) {
				entries = append(entries, strconv.Itoa(fd))
			}
		}
		env[i] = BashyInheritedFdsEnv + "=" + strings.Join(entries, ",")
		cmd.Env = env
	}
	return cleanup, nil
}

// cloneProcSubstIO snapshots only the Darwin process-substitution lineage.
// It reuses the task descriptor-copy primitive without adopting task semantics.
// Descendants snapshot their current bindings before launch, so a background
// child never borrows a descriptor that its producer will close on return.
func (r *Runner) cloneProcSubstIO() error {
	dups := make(map[*os.File]*os.File)
	var owned []*os.File
	dup := func(f *os.File) (*os.File, error) {
		if f == nil || !fileOpen(f) {
			return f, nil
		}
		if copy := dups[f]; copy != nil {
			return copy, nil
		}
		copy, distinct, err := dupPipeFd(f)
		if err != nil {
			return nil, err
		}
		if !distinct {
			return nil, fmt.Errorf("cannot duplicate process-substitution descriptor")
		}
		dups[f] = copy
		owned = append(owned, copy)
		return copy, nil
	}
	finish := false
	defer func() {
		if !finish {
			for _, f := range owned {
				_ = f.Close()
			}
		}
	}()
	var err error
	if r.stdin, err = dup(r.stdin); err != nil {
		return err
	}
	if r.origStdin, err = dup(r.origStdin); err != nil {
		return err
	}
	if r.stdout, err = bashPPDupWriter(r.stdout, r, dup); err != nil {
		return err
	}
	if r.origStdout, err = bashPPDupWriter(r.origStdout, r, dup); err != nil {
		return err
	}
	if r.stderr, err = bashPPDupWriter(r.stderr, r, dup); err != nil {
		return err
	}
	if r.origStderr, err = bashPPDupWriter(r.origStderr, r, dup); err != nil {
		return err
	}
	for fd, f := range r.fdTable {
		if r.fdTable[fd], err = dup(f); err != nil {
			return err
		}
	}
	for fd, w := range r.fdWriteTable {
		if r.fdWriteTable[fd], err = bashPPDupWriter(w, r, dup); err != nil {
			return err
		}
	}
	r.procSubstOwnIO = true
	r.procSubstIOFiles = append(r.procSubstIOFiles, owned...)
	finish = true
	return nil
}
func (r *Runner) closeProcSubstIO() {
	for _, f := range r.procSubstIOFiles {
		_ = f.Close()
	}
	r.procSubstIOFiles = nil
}
