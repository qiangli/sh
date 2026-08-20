// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

func (r *Runner) ensureDirFile(path string) {
	if r.dirFile != nil {
		return
	}
	r.dirFile, _ = openRunnerDir(path)
}

func (r *Runner) replaceDirFile(path string) {
	next, _ := openRunnerDir(path)
	r.closeDirFile()
	r.dirFile = next
}

func (r *Runner) closeDirFile() {
	if r.dirFile != nil {
		_ = r.dirFile.Close()
		r.dirFile = nil
	}
}
