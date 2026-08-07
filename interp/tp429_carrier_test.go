package interp_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// testCarrier2 re-execs for carrier mode like testCarrier in carrier_test.go
type testCarrier2 struct{}

type testCarrierProc2 struct {
	cmd   *exec.Cmd
	stdin io.Closer
}

func (c testCarrier2) StartCarrier(ctx context.Context) (interp.CarrierProcess, error) {
	cmd := exec.Command(os.Getenv("GOSH_PROG"))
	cmd.Env = append(os.Environ(), "GOSH_CMD=carrier")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &testCarrierProc2{cmd: cmd, stdin: stdin}, nil
}

func (p *testCarrierProc2) Pid() int { return p.cmd.Process.Pid }
func (p *testCarrierProc2) Wait() int {
	_ = p.cmd.Wait()
	return carrierExitSignal(p.cmd.ProcessState)
}
func (p *testCarrierProc2) Terminate() {
	p.stdin.Close()
	_ = p.cmd.Process.Kill()
}

func TestTP429CarrierWaitAfterSignal(t *testing.T) {
	c := testCarrier2{}
	src := `set +m
sleep 10 &
pid=$!
kill -TERM $pid
wait $pid
echo "rc=$?"`
	file, err := syntax.NewParser().Parse(strings.NewReader(src), "")
	if err != nil {
		t.Fatal(err)
	}
	var buf concBuffer
	r, err := interp.New(
		interp.WithJobCarrier(c),
		interp.StdIO(nil, &buf, &buf),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	runErr := r.Run(ctx, file)
	out := buf.String()
	t.Logf("output: %q, runErr: %v", out, runErr)

	// We expect rc=143 (128+15=SIGTERM)
	if !strings.Contains(out, "rc=143") {
		t.Errorf("expected rc=143 in output, got: %q", out)
	}
	_ = errors.New
	_ = fmt.Sprintf
}
