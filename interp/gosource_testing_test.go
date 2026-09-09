package interp_test

// Sprint: #118; Story: #56; Story-ID: 3ef468f4e831
import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/lower"
	"mvdan.cc/sh/v3/syntax"
)

func TestGoSourceTestingOriginalErrors(t *testing.T) {
	pins := map[string]string{
		"errors_test.go":  "7827d7e3ab7cc317a254ccf76158069b80c5b55b34ca45a3e6d799ad185f70ea",
		"example_test.go": "db75531e0575928a48a16d2c9769da59e719da091a31a4f03c3d5aeeca6be648",
		"join_test.go":    "09f9cc24acc7749d8f7197c6b90dc43223708c8f6412825e29ea3984bc6afdbc",
		"wrap_test.go":    "a098c0f752095df4614abf02925e52d2d35228e6ac272925dd4d9fd59f7e0cf6",
	}
	var sources []gosource.Source
	dir := filepath.Join(runtime.GOROOT(), "src", "errors")
	for name, digest := range pins {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != digest {
			t.Fatalf("requires pinned original Go1.27 errors source: %s", path)
		}
		sources = append(sources, gosource.Source{Name: path, Data: data})
	}
	program, err := gosource.Load(sources, gosource.Options{Importer: lower.NewModuleImporter(dir)})
	if err != nil {
		t.Fatal(err)
	}
	if program.Package != "errors_test" || len(program.Sources) != 4 {
		t.Fatalf("wrong original package: %#v", program.Sources)
	}
	var out, errs bytes.Buffer
	runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.Dir(t.TempDir()), interp.StdIO(nil, &out, &errs))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	session, err := runner.LoadGoSourceTests(ctx, program)
	if err != nil {
		t.Fatalf("load original package: %v; %s", err, errs.String())
	}
	defer session.Close()
	for _, name := range []string{"TestNewEqual", "TestErrorMethod"} {
		t.Run(name, func(t *testing.T) {
			if err := session.Run(ctx, name, t); err != nil {
				t.Fatalf("original body: %v; %s", err, errs.String())
			}
		})
	}
	for _, source := range sources {
		after, err := os.ReadFile(source.Name)
		if err != nil || !bytes.Equal(after, source.Data) {
			t.Fatalf("original source changed: %s", source.Name)
		}
	}
}

type testingRecorder struct {
	log                      []string
	failed, failNow, skipped bool
}

func (t *testingRecorder) Errorf(format string, args ...any) {
	t.failed = true
	t.log = append(t.log, fmt.Sprintf(format, args...))
}
func (t *testingRecorder) Logf(format string, args ...any) {
	t.log = append(t.log, fmt.Sprintf(format, args...))
}
func (t *testingRecorder) Fail()    { t.failed = true }
func (t *testingRecorder) FailNow() { t.failed = true; t.failNow = true }
func (t *testingRecorder) SkipNow() { t.skipped = true }

func TestGoSourceTestingControlAndLifecycle(t *testing.T) {
	// Authored driver fixtures exercise scheduler behavior; upstream bodies above
	// are loaded from the SDK, never rewritten to test this adapter.
	source := `package specimen
import "testing"
var initialized int
func init(){initialized=7}
func nested(t *testing.T){defer t.Log("nested defer");t.FailNow();t.Log("unreachable nested")}
func TestFatal(t *testing.T){defer t.Log("outer defer");nested(t);t.Log("unreachable outer")}
func TestSkip(t *testing.T){defer t.Log("skip defer");t.SkipNow();t.Log("unreachable skip")}
func TestInit(t *testing.T){if initialized!=7{t.Errorf("bad init: %d",initialized)}}
func TestDeferControl(t *testing.T){defer t.Log("last");defer t.FailNow();defer t.Log("first")}
func TestDeferCapture(t *testing.T){x:="before";defer t.Log(x);x="after"}
var cleanupOrder string
func TestNested(t *testing.T){
 t.Cleanup(func(){cleanupOrder += "outer;"})
 if !t.Run("child",func(t *testing.T){t.Cleanup(func(){cleanupOrder += "child;"});t.SkipNow();cleanupOrder += "unreachable;"}) {t.Error("skipped child Run returned false")}
 if cleanupOrder != "child;" {t.Errorf("child cleanup order: %s",cleanupOrder)}
}
func TestNestedFatal(t *testing.T){
 t.Cleanup(func(){t.Log("outer cleanup")})
 if t.Run("child",func(t *testing.T){defer t.Log("child defer");t.Cleanup(func(){t.Log("child cleanup")});t.Fatal("nested deliberate")}) {t.Error("failed child Run returned true")} else {t.Log("false result propagated")}
 t.Log("after child")
}
func TestAfterCleanup(t *testing.T){if cleanupOrder != "child;outer;"{t.Errorf("final cleanup order: %s",cleanupOrder)}}
func TestUnsupported(t *testing.T){t.Parallel()}
func TestFailure(t *testing.T){t.Errorf("deliberate failure %s","kept")}
`
	program, err := gosource.Parse(strings.NewReader(source), "driver_fixture.go", gosource.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var errs bytes.Buffer
	runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.Dir(t.TempDir()), interp.StdIO(nil, nil, &errs))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	session, err := runner.LoadGoSourceTests(ctx, program)
	if err != nil {
		t.Fatalf("load: %v %s", err, errs.String())
	}
	defer session.Close()

	if os.Getenv("BASHPP_TESTING_SCHEDULER_CHILD") == "1" {
		t.Run("FatalScheduler", func(t *testing.T) {
			if err := session.Run(ctx, "TestFatal", t); err != nil {
				t.Fatal(err)
			}
			t.Fatal("unreachable after FailNow")
		})
		t.Run("NestedFatalScheduler", func(t *testing.T) {
			if err := session.Run(ctx, "TestNestedFatal", t); err != nil {
				t.Fatal(err)
			}
		})
		t.Run("Reentry", func(t *testing.T) {
			if err := session.Run(ctx, "TestInit", t); err != nil {
				t.Fatal(err)
			}
		})
		return
	}
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGoSourceTestingControlAndLifecycle$", "-test.v")
	command.Env = append(os.Environ(), "BASHPP_TESTING_SCHEDULER_CHILD=1", "GOSH_PROG=")
	output, childErr := command.CombinedOutput()
	if childErr == nil || !bytes.Contains(output, []byte("--- FAIL: TestGoSourceTestingControlAndLifecycle/FatalScheduler")) || !bytes.Contains(output, []byte("--- PASS: TestGoSourceTestingControlAndLifecycle/Reentry")) || bytes.Contains(output, []byte("unreachable after FailNow")) {
		t.Fatalf("real FailNow scheduler/reentry: %v\n%s", childErr, output)
	}
	position := -1
	for _, marker := range []string{"nested deliberate", "child defer", "child cleanup", "false result propagated", "after child", "outer cleanup"} {
		found := bytes.Index(output, []byte(marker))
		if found <= position {
			t.Fatalf("nested fatal/cleanup ordering at %s: %s", marker, output)
		}
		position = found
	}
	t.Run("NestedScheduler", func(t *testing.T) {
		if err := session.Run(ctx, "TestNested", t); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("AfterCleanup", func(t *testing.T) {
		if err := session.Run(ctx, "TestAfterCleanup", t); err != nil {
			t.Fatal(err)
		}
	})
	afterSkip := false
	t.Run("RealSkip", func(t *testing.T) {
		if err := session.Run(ctx, "TestSkip", t); err != nil {
			t.Fatal(err)
		}
		afterSkip = true
	})
	if afterSkip {
		t.Fatal("SkipNow returned to test body")
	}
	for _, check := range []struct {
		name, log string
		fatal     bool
	}{
		{"TestDeferControl", "first\n|last\n", true},
		{"TestDeferCapture", "before\n", false},
	} {
		recorded := &testingRecorder{}
		if err := session.Run(ctx, check.name, recorded); err != nil || strings.Join(recorded.log, "|") != check.log || recorded.failNow != check.fatal {
			t.Fatalf("%s: %v %#v", check.name, err, recorded)
		}
	}
	fatal := &testingRecorder{}
	if err := session.Run(ctx, "TestFatal", fatal); err != nil {
		t.Fatalf("invoke fatal: %v; stderr=%s", err, errs.String())
	}
	if !fatal.failNow || strings.Join(fatal.log, "|") != "nested defer\n|outer defer\n" {
		t.Fatalf("fatal control: %#v", fatal)
	}
	skipped := &testingRecorder{}
	if err := session.Run(ctx, "TestSkip", skipped); err != nil {
		t.Fatal(err)
	}
	if !skipped.skipped || strings.Join(skipped.log, "|") != "skip defer\n" {
		t.Fatalf("skip control: %#v", skipped)
	}
	initialized := &testingRecorder{}
	if err := session.Run(ctx, "TestInit", initialized); err != nil || initialized.failed {
		t.Fatalf("init: %v %#v", err, initialized)
	}
	failed := &testingRecorder{}
	if err := session.Run(ctx, "TestFailure", failed); err != nil || !failed.failed {
		t.Fatalf("failure swallowed: %v %#v", err, failed)
	}
	if err := session.Run(ctx, "TestUnsupported", &testingRecorder{}); err == nil || !strings.Contains(err.Error(), "Parallel is not implemented") {
		t.Fatalf("unsupported testing method was accepted: %v", err)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if err := session.Run(cancelled, "TestInit", &testingRecorder{}); err != context.Canceled {
		t.Fatalf("cancelled callback: %v", err)
	}
	if err := runner.Run(ctx, program.File); err == nil {
		t.Fatal("ordinary Run stole active test session")
	}
	runner.Reset()
	if err := session.Run(ctx, "TestInit", &testingRecorder{}); err == nil {
		t.Fatal("Reset did not invalidate testing capability")
	}
}

func TestGoSourceTestingDiscoveryRejectsInvalid(t *testing.T) {
	for _, source := range []string{
		"package specimen; func TestBad() {}",
		"package specimen; import \"testing\"; func TestBad(t *testing.T) bool {return true}",
		"package specimen; import \"testing\"; func TestBad[T any](t *testing.T) {}",
		"package specimen; import \"testing\"; func TestBad(t *testing.B) {}",
	} {
		program, err := gosource.Parse(strings.NewReader(source), "invalid_registration.go", gosource.Options{})
		if err != nil {
			t.Fatal(err)
		}
		runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.Dir(t.TempDir()))
		if err != nil {
			t.Fatal(err)
		}
		session, err := runner.LoadGoSourceTests(context.Background(), program)
		if err != nil {
			t.Fatal(err)
		}
		_, err = session.Tests()
		session.Close()
		if err == nil {
			t.Fatalf("invalid Test registration accepted: %s", source)
		}
	}
}
