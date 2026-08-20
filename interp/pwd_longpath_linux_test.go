//go:build linux

package interp_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
	"golang.org/x/sys/unix"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func TestPwdBeyondPathMax(t *testing.T) {
	root := t.TempDir()
	const component = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const depth = 60

	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	qt.Assert(t, qt.IsNil(err))
	defer func() { _ = unix.Close(fd) }()
	for range depth {
		if err := unix.Mkdirat(fd, component, 0o755); err != nil {
			t.Fatal(err)
		}
		next, err := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.IsNil(unix.Close(fd)))
		fd = next
	}
	qt.Assert(t, qt.IsNil(unix.Mkdirat(fd, "target", 0o755)))
	qt.Assert(t, qt.IsNil(unix.Symlinkat("target", fd, "link")))
	fileFD, err := unix.Openat(fd, "regular", unix.O_CREAT|unix.O_WRONLY|unix.O_CLOEXEC, 0o644)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsNil(unix.Close(fileFD)))

	want := root + strings.Repeat(string(filepath.Separator)+component, depth)
	wantTarget := want + string(filepath.Separator) + "target"
	info, err := interp.DefaultStatHandler()(context.Background(), want+string(filepath.Separator)+"regular", true)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(info.Mode().IsRegular()))
	src := fmt.Sprintf(`
i=0
while [ "$i" -lt %d ]; do
	cd %s || exit 70
	i=$((i + 1))
done
pwd -L
pwd -P
printf 'PWD=%%s\n' "$PWD"
cd .. || exit 71
cd %s || exit 72
cd -P link || exit 73
pwd -L
pwd -P
printf 'PWD=%%s\n' "$PWD"
`, depth, component, component)
	file, err := syntax.NewParser(syntax.Variant(syntax.LangPOSIX)).Parse(strings.NewReader(src), "")
	qt.Assert(t, qt.IsNil(err))
	var output bytes.Buffer
	runner, err := interp.New(interp.Dir(root), interp.StdIO(nil, &output, &output), interp.WithPosixMode(true))
	qt.Assert(t, qt.IsNil(err))
	err = runner.Run(context.Background(), file)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("output: %s", output.String()))
	qt.Assert(t, qt.Equals(output.String(), want+"\n"+want+"\nPWD="+want+"\n"+wantTarget+"\n"+wantTarget+"\nPWD="+wantTarget+"\n"))
	_, err = os.Stat(want)
	qt.Assert(t, qt.ErrorMatches(err, `.*file name too long.*`))
}
