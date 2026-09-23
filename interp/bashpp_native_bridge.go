package interp

// Sprint: #118; Story: #50; Story-ID: cf81e4868348
// The persistent child runs imported dependency operations only. Its source is
// a fixed protocol template plus a Go-type-checked export table, never a tested
// program body or expression. There is no second source-language interpreter.
import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/constant"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"mvdan.cc/sh/v3/syntax"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed bashpp_native_worker.go.txt
var bashPPNativeWorker string

type bashPPBridgeValue struct {
	// deferredNativeComposite is return-boundary provenance minted only while
	// evaluating the mirrored method frame's own return expression. The worker
	// reconstructs the value at the declared reflect result type; the marker is
	// host-only and is never trusted from the wire.
	deferredNativeComposite bool
	ReaderBuffer            []byte              `json:"reader_buffer,omitempty"`
	ReaderLength            int                 `json:"reader_length,omitempty"`
	CallArgs                []bashPPBridgeValue `json:"call_args,omitempty"`
	sliceView               *bashPPNativeSlice  // host-only original backing view

	// reflectCopy marks a handle derived from an admitted reflect.ValueOf
	// copy (reflectedValueCopy). Host-only: it is set on replies by the
	// session itself and never trusted from the wire.
	reflectCopy bool

	// Callable is derived by the interpreter from authenticated native type or
	// import metadata; the dependency worker cannot set callback policy itself.
	Callable     string                       `json:"-"`
	NativeType   string                       `json:"native_type,omitempty"`
	NativeTypeID uint64                       `json:"native_type_id,omitempty"`
	Callbacks    bool                         `json:"callbacks,omitempty"`
	Function     bool                         `json:"function,omitempty"`
	Origin       uint64                       `json:"origin,omitempty"`
	Interface    string                       `json:"interface,omitempty"`
	Session      string                       `json:"session,omitempty"`
	Kind         string                       `json:"kind"`
	Type         string                       `json:"type,omitempty"`
	Text         string                       `json:"text,omitempty"`
	Bytes        []byte                       `json:"bytes,omitempty"`
	Handle       uint64                       `json:"handle,omitempty"`
	Elements     []bashPPBridgeValue          `json:"elements,omitempty"`
	Fields       map[string]bashPPBridgeValue `json:"fields,omitempty"`
	Entries      []bashPPBridgeEntry          `json:"entries,omitempty"`
}
type bashPPBridgeEntry struct {
	Key   bashPPBridgeValue `json:"key"`
	Value bashPPBridgeValue `json:"value"`
}
type bashPPBridgeRequest struct {
	SliceBuffers []bashPPNativeSliceBuffer `json:"slice_buffers,omitempty"`
	sliceTargets []*bashPPNativeSlice
	// sliceMutating[i] distinguishes a full in-place mutation writeback (sort)
	// from the byte-read writeback: a read fills a caller buffer up to its
	// capacity, while a mutation reorders exactly the visible length. Host-only.
	sliceMutating []bool
	// sliceElem[i] is the declared element type of sliceTargets[i], used to
	// rebuild the writeback elements as interpreter values. Host-only.
	sliceElem []syntax.BashPPTypeExpr
	// sliceReconcile[i] marks a buffer whose class is decided by the observed
	// call behaviour: elements returned unchanged are a read-only consumer and
	// nothing is written back; changed elements are an in-place mutation and
	// are written back over the visible length. Host-only.
	sliceReconcile []bool
	// Transfers lists the argument indexes whose original slice is handed to
	// the dependency as the storage of record rather than as a copy; the
	// worker answers each with a handle on that same slice (Transferred) and
	// argCells[i] is the interpreter binding the transfer rebinds to it. See
	// bashpp_native_transfer.go. argCells, transferProof and sourceProgram are host-only.
	Transfers     []int `json:"transfers,omitempty"`
	argCells      []*bashPPCell
	transferProof []bool
	// coherence, when set, re-reads the storage a read-only request copied
	// after every callback it serves. See bashpp_native_coherence.go.
	coherence     *goSourceCopyCoherence
	sourceProgram bool   // the call site is in the program package itself
	ID            uint64 `json:"id"`
	Op            string `json:"op"`
	PanicOnFault  bool   `json:"panic_on_fault,omitempty"`
	Selector      string `json:"selector"`
	// Instance is the type-argument suffix of an instantiated imported
	// generic function; the helper resolves Selector+Instance.
	Instance   string              `json:"instance,omitempty"`
	Receiver   *bashPPBridgeValue  `json:"receiver,omitempty"`
	Args       []bashPPBridgeValue `json:"args,omitempty"`
	Spread     bool                `json:"spread,omitempty"`
	SourceFile string              `json:"source_file,omitempty"`
	SourceLine int                 `json:"source_line,omitempty"`
	LogPrint   string              `json:"log_print,omitempty"`
	// Values and Error answer a callback the dependency raised; they are set
	// only when Op is "callback-reply". Sprint #118 Story #54 (c3a60493cde9).
	Values []bashPPBridgeValue `json:"values,omitempty"`
	Error  string              `json:"error,omitempty"`
}
type bashPPBridgeResponse struct {
	SliceUpdates []bashPPNativeSliceBuffer `json:"slice_updates,omitempty"`
	// Transferred answers each requested transfer with a handle on the
	// decoded slice the dependency received, in Transfers order.
	Transferred []bashPPNativeSliceBuffer `json:"transferred,omitempty"`
	PtrUpdates  []bashPPBridgeValue       `json:"ptr_updates,omitempty"`

	Panic *bashPPBridgeValue `json:"panic,omitempty"`
	// PanicAddr carries runtime.Error values produced by SetPanicOnFault. It
	// is meaningful only when Error is a worker-recovered native dependency
	// panic.
	PanicAddr *uint64 `json:"panic_addr,omitempty"`
	ID        uint64  `json:"id"`
	// Op, Selector and Receiver are set only when the dependency is asking the
	// interpreter to run an original method body it must not compile itself.
	Op       string              `json:"op,omitempty"`
	Selector string              `json:"selector,omitempty"`
	Receiver *bashPPBridgeValue  `json:"receiver,omitempty"`
	Values   []bashPPBridgeValue `json:"values,omitempty"`
	Error    string              `json:"error,omitempty"`
}
type bashPPNativeSession struct {
	// Type facts are authenticated on this connection; no native values are cached.
	handleTypes         map[uint64]uint64
	interfaceAdmissions map[goSourceNativeAdmissionKey]bool
	functions           map[uint64]*bashPPFunc
	functionNext        uint64
	callbackGate        chan struct{}
	activeCallbacks     chan bashPPBridgeResponse
	callbackOwner       *Runner
	// retained records that this session was handed an original callback it
	// keeps past the handing-over call. Every later request then parks as a
	// callback server; see requestCallbackCapable.
	retained            bool
	origins             map[uint64]*bashPPPointer
	originNext          uint64
	start               sync.Mutex
	write               sync.Mutex
	mu                  sync.Mutex
	next                atomic.Uint64
	conn                net.Conn
	cmd                 *exec.Cmd
	pending             map[uint64]chan bashPPBridgeResponse
	done                chan struct{}
	waitErr             error
	forwardedSignal     int   // protected by mu; last parent signal delivered to cmd
	closeCancellation   error // protected by mu; set only by the closer of conn
	processCancellation error // protected by mu; command context requested kill
	closeOnce           sync.Once
	cleanup             func()
	stopSignals         func()
	drains              []*bashPPNativeOutputDrain
	imports             string
	locals              string
	embeds              string
	companions          string
	cgo                 string
	instances           string
	id                  string
}

func (r *Runner) closeGoSourceBridge() {
	r.bashPPTools.nativeTypes = nil
	r.bashPPTools.requestEnv = nil
	r.bashPPTools.runtimeEnv = nil
	if session := r.bashPPTools.bridge; session != nil {
		session.close()
		r.bashPPTools.bridge = nil
	}
}
func (s *bashPPNativeSession) close() { s.closeCanceled(nil) }

func (s *bashPPNativeSession) closeCanceled(cause error) {
	s.closeOnce.Do(func() {
		if s.conn != nil {
			s.write.Lock()
			processExited := false
			select {
			case <-s.done:
				processExited = true
			default:
			}
			_ = json.NewEncoder(s.conn).Encode(bashPPBridgeRequest{Op: "close"})
			if err := s.conn.Close(); err == nil && cause != nil && !processExited {
				s.mu.Lock()
				s.closeCancellation = cause
				s.mu.Unlock()
			}
			s.write.Unlock()
		}
		if s.cmd != nil && s.cmd.Process != nil {
			select {
			case <-s.done:
			case <-time.After(time.Second):
				bashPPNativeKill(s.cmd)
				<-s.done
			}
		}
		if s.stopSignals != nil {
			s.stopSignals()
		}
		s.closeDrains()
		if s.cmd != nil && s.cmd.Process != nil {
			// The child is gone by now, so the copiers can reach EOF; waiting
			// here makes the program's final output visible before the session
			// reports closed.
			s.waitDrains()
		}
		if s.cleanup != nil {
			s.cleanup()
		}
	})
}
func bridgeImportIdentity(imports map[string]string) string {
	data, _ := json.Marshal(imports)
	return string(data)
}
func (s *bashPPNativeSession) begin(ctx context.Context, req bashPPEvalRequest) error {
	s.start.Lock()
	defer s.start.Unlock()
	if s.conn != nil {
		if s.imports != bridgeImportIdentity(req.Imports) {
			return errors.New("gosource: imports changed after native dependency initialization")
		}
		if s.locals != bashPPLocalTypeIdentity(req.LocalTypes) {
			return errors.New("gosource: local types changed after native dependency initialization")
		}
		if s.embeds != bashPPEmbedIdentity(req.EmbedDecls) {
			return errors.New("gosource: embed declarations changed after native dependency initialization")
		}
		if s.companions != bashPPNativeCompanionIdentity(req.CompanionFiles, req.NativeFuncs, req.CompanionTrampolines, req.CompanionUnmappedFrames) {
			return errors.New("gosource: native companions changed after native dependency initialization")
		}
		if s.cgo != bashPPCgoIdentity(req.CgoPackages) {
			return errors.New("gosource: cgo packages changed after native dependency initialization")
		}
		if s.instances != bashPPImportedInstanceIdentity(req.Instances) {
			return errors.New("gosource: imported instantiations changed after native dependency initialization")
		}
		return nil
	}
	source, err := bashPPNativeSource(ctx, bashPPModuleRequest(req))
	if err != nil {
		return err
	}
	listener, bridgeNetwork, cleanupControl, err := bashPPNativeControlListener()
	if err != nil {
		return err
	}
	defer listener.Close()
	defer cleanupControl()
	secret := make([]byte, 24)
	if _, err = rand.Read(secret); err != nil {
		return err
	}
	auth := hex.EncodeToString(secret)
	s.id = auth[:16]
	source = strings.Replace(source, "//CONNECTION", "const bridgeNetwork = "+strconv.Quote(bridgeNetwork)+"\nconst bridgeAddress = "+strconv.Quote(listener.Addr().String())+"\nconst bridgeAuth = "+strconv.Quote(auth), 1)
	scratchEnv := req.RuntimeEnv
	if scratchEnv == nil {
		scratchEnv = req.Env
	}
	sourceDir := bashPPModuleRequest(req).Dir
	policy := bashPPScratchIsolated
	if len(req.EmbedDecls) > 0 || len(req.CompanionFiles) > 0 {
		sourceDir = req.SourceDir
		policy = bashPPScratchSourceRoot
	}
	file, err := bashPPImportTempSource(sourceDir, "bashpp-session-*.go", scratchEnv, policy)
	if err != nil {
		return err
	}
	cleanup := file.cleanup
	s.cleanup = cleanup
	if _, err = file.WriteString(source); err != nil {
		file.Close()
		cleanup()
		return err
	}
	if err = file.Close(); err != nil {
		cleanup()
		return err
	}
	binary := file.Name() + ".bin"
	buildEnv := setEnvString(req.Env, "CGO_ENABLED", "0")
	if len(req.CgoPackages) > 0 {
		buildEnv = setEnvString(req.Env, "CGO_ENABLED", "1")
	}
	if len(req.CgoPackages) > 0 {
		// cmd/cgo changes into the logical source directory. Give the overlay
		// files unique names in the existing module directory so that directory
		// exists, while all bytes and build artifacts remain in private scratch.
		cgoMain := filepath.Join(bashPPModuleRequest(req).Dir, filepath.Base(file.buildPath))
		if _, statErr := os.Lstat(cgoMain); !os.IsNotExist(statErr) {
			cleanup()
			return fmt.Errorf("gosource: cgo helper overlay would mask an existing source path")
		}
		if err = file.remap(cgoMain); err != nil {
			cleanup()
			return err
		}
		wrappers, wrapperErr := bashPPCgoWrapperSources(ctx, req)
		if wrapperErr != nil {
			cleanup()
			return wrapperErr
		}
		paths := []string{file.buildPath}
		for i, wrapper := range wrappers {
			path, addErr := file.addSource(fmt.Sprintf("bashpp-cgo-%d.go", i), wrapper)
			if addErr != nil {
				cleanup()
				return addErr
			}
			paths = append(paths, path)
		}
		args := append([]string{"build", "-p", "2", "-overlay=" + file.overlay, "-o", binary}, paths...)
		build := exec.CommandContext(ctx, req.Go, args...)
		build.Dir, build.Env = bashPPModuleRequest(req).Dir, buildEnv
		var diagnostics bytes.Buffer
		build.Stdout, build.Stderr = &diagnostics, &diagnostics
		if err = build.Run(); err != nil {
			cleanup()
			return fmt.Errorf("gosource: build cgo dependency bridge: %w: %s", err, diagnostics.String())
		}
	} else if len(req.CompanionFiles) > 0 {
		if req.SourceFile == "" {
			cleanup()
			return fmt.Errorf("gosource: native companions require an original source file")
		}
		// The companion build reads the original package directory, since that
		// is where its assembly lives; every original Go file in it is overlaid
		// away first, so the compiler sees only the generated helper.
		stubs, err := bashPPCompanionRootStubs(req.SourceDir, req.RootFiles)
		if err != nil {
			cleanup()
			return err
		}
		if err = file.overlayRoot(stubs); err != nil {
			cleanup()
			return err
		}
		if err = bashPPCompanionOverlayComplete(file.sourceDir, file.replace); err != nil {
			cleanup()
			return err
		}
		// The build runs in the directory the overlay is keyed by, and says so
		// in its own environment: cmd/go matches overlay paths against the
		// working directory it resolves, and it resolves that one through
		// $PWD. A key spelled differently is not an error, it is simply not a
		// key -- and the build would then quietly compile the original
		// package. Hence both the explicit PWD and the check below.
		companionEnv := setEnvString(buildEnv, "PWD", file.sourceDir)
		if err = bashPPCompanionBuildIsolated(ctx, req.Go, file, companionEnv); err != nil {
			cleanup()
			return err
		}
		build := exec.CommandContext(ctx, req.Go, "build", "-p", "2", "-overlay="+file.overlay, "-o", binary, ".")
		build.Dir, build.Env = file.sourceDir, companionEnv
		var diagnostics bytes.Buffer
		build.Stdout, build.Stderr = &diagnostics, &diagnostics
		if err = build.Run(); err != nil {
			cleanup()
			return fmt.Errorf("gosource: build dependency bridge: %w: %s", err, diagnostics.String())
		}
	} else if policy == bashPPScratchSourceRoot {
		// go:embed patterns resolve against the worker's logical location;
		// only cmd/go's overlay gives the worker one inside the source root.
		build := exec.CommandContext(ctx, req.Go, "build", "-p", "2", "-overlay="+file.overlay, "-o", binary, file.buildPath)
		build.Dir, build.Env = bashPPModuleRequest(req).Dir, buildEnv
		var diagnostics bytes.Buffer
		build.Stdout, build.Stderr = &diagnostics, &diagnostics
		if err = build.Run(); err != nil {
			cleanup()
			return fmt.Errorf("gosource: build dependency bridge: %w: %s", err, diagnostics.String())
		}
	} else if err = bashPPBuildWorkerImportcfg(ctx, req.Go, bashPPModuleRequest(req).Dir, buildEnv, filepath.Dir(file.Name()), file.Name(), binary); err != nil {
		// The importcfg route: the worker's imports were decided at the
		// check (identity-keyed, D8); cmd/go's directory rule does not
		// re-decide them. See bashpp_sprint165_runtime2_worker_build.go.
		cleanup()
		return err
	}
	cmd := exec.CommandContext(ctx, binary)
	// Bootstrap data is compiled into this ephemeral dependency-only helper.
	// It must not pollute os.Args or os.Environ, including dependency init.
	cmd.Args = append([]string(nil), req.Argv...)
	cmd.Dir, cmd.Env = req.Dir, req.RuntimeEnv
	cmd.Stdin, cmd.Stdout, cmd.Stderr = req.Stdin, req.Stdout, req.Stderr
	// A non-file writer receives child output through a pipe this session owns,
	// so each answered request can drain its output before the interpreter's
	// next direct write; see bashpp_native_output.go. One writer given for both
	// streams shares one pipe, exactly as os/exec would share one descriptor.
	if _, isFile := req.Stdout.(*os.File); !isFile && req.Stdout != nil {
		drain, err := newBashPPNativeOutputDrain(req.Stdout)
		if err != nil {
			cleanup()
			return err
		}
		s.drains = append(s.drains, drain)
		cmd.Stdout = drain.write
	}
	if _, isFile := req.Stderr.(*os.File); !isFile && req.Stderr != nil {
		if bashPPSameWriter(req.Stderr, req.Stdout) {
			cmd.Stderr = cmd.Stdout
		} else {
			drain, err := newBashPPNativeOutputDrain(req.Stderr)
			if err != nil {
				s.closeDrains()
				cleanup()
				return err
			}
			s.drains = append(s.drains, drain)
			cmd.Stderr = drain.write
		}
	}
	bashPPNativeProcessGroup(cmd)
	cmd.Cancel = func() error {
		s.mu.Lock()
		s.processCancellation = ctx.Err()
		s.mu.Unlock()
		bashPPNativeKill(cmd)
		return nil
	}
	if err = cmd.Start(); err != nil {
		s.closeDrains()
		cleanup()
		return err
	}
	// The helper owns the native standard-library state of the interpreted Go
	// program. Keep it in an isolated process group for cleanup, but proxy the
	// program process's catchable signals as an exec replacement would.
	s.stopSignals = forwardExecReplacementSignalsWithReport(cmd.Process.Pid, func(sig int) {
		s.mu.Lock()
		s.forwardedSignal = sig
		s.mu.Unlock()
	})
	s.cmd = cmd
	s.done = make(chan struct{})
	s.pending = make(map[uint64]chan bashPPBridgeResponse)
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.waitErr = err
		conn := s.conn
		s.mu.Unlock()
		close(s.done)
		// The child's descriptors are gone; retiring the host write ends lets
		// each copier drain to EOF and flush the program's final output.
		s.closeDrains()
		_ = listener.Close()
		if conn != nil {
			// Serialize local close with writes and cancellation provenance.
			s.write.Lock()
			_ = conn.Close()
			s.write.Unlock()
		}
	}()
	if tcp, ok := listener.(*net.TCPListener); ok {
		_ = tcp.SetDeadline(time.Now().Add(10 * time.Second))
	}
	conn, err := listener.Accept()
	if err != nil {
		s.close()
		return fmt.Errorf("gosource: bridge handshake: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	decoder := json.NewDecoder(conn)
	var got string
	if err = decoder.Decode(&got); err != nil || got != auth {
		conn.Close()
		s.close()
		return errors.New("gosource: unauthenticated dependency bridge")
	}
	// The process has loaded its executable and authenticated its connection.
	// Removing build inputs now also prevents abrupt host termination from
	// leaving helper effects in the caller's TMPDIR. Close retries cleanup on
	// systems which cannot unlink an executable while it is running.
	cleanup()
	_ = conn.SetDeadline(time.Time{})
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()
	s.imports = bridgeImportIdentity(req.Imports)
	s.locals = bashPPLocalTypeIdentity(req.LocalTypes)
	s.embeds = bashPPEmbedIdentity(req.EmbedDecls)
	s.companions = bashPPNativeCompanionIdentity(req.CompanionFiles, req.NativeFuncs, req.CompanionTrampolines, req.CompanionUnmappedFrames)
	s.cgo = bashPPCgoIdentity(req.CgoPackages)
	s.instances = bashPPImportedInstanceIdentity(req.Instances)
	go func() {
		for {
			var reply bashPPBridgeResponse
			if err := decoder.Decode(&reply); err != nil {
				return
			}
			if reply.Op == "callback" {
				// The waiting request executes its own callback; this reader
				// remains available for nested dependency replies.
				s.mu.Lock()
				callbacks := s.activeCallbacks
				s.mu.Unlock()
				if callbacks != nil {
					select {
					case callbacks <- reply:
					case <-s.done:
						return
					}
				} else {
					s.serveCallback(ctx, nil, reply, nil)
				}
				continue
			}
			s.mu.Lock()
			wait := s.pending[reply.ID]
			s.mu.Unlock()
			if wait != nil {
				wait <- reply
			}
		}
	}()
	return nil
}
func (s *bashPPNativeSession) request(ctx context.Context, req bashPPEvalRequest, q bashPPBridgeRequest) ([]bashPPBridgeValue, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The package sort ordering entry points run over the interpreter's own
	// storage when they carry original callbacks, so the callbacks and the
	// algorithm share one backing array (bashpp_native_shared_order.go).
	if req.CallbackOwner != nil {
		if values, handled, err := req.CallbackOwner.goSourceSharedOrdering(ctx, req, &q); handled || err != nil {
			return values, err
		}
	}
	if err := prepareNativeSliceBuffers(req, &q); err != nil {
		return nil, err
	}
	// A generic slices helper (slices.Equal/slices.Sort) is not a reflectable
	// dependency symbol, so it is answered interpreter-side over the values that
	// already crossed the collection transport, before the dependency dispatch.
	if req.CallbackOwner != nil {
		if values, handled, err := req.CallbackOwner.nativeSliceGenericHelper(ctx, req, &q); handled || err != nil {
			return values, err
		}
	}
	bashPPReflectTypeOnly(req, &q)
	if err := validateLocalTransport(req, q); err != nil {
		return nil, err
	}
	q.PanicOnFault = req.PanicOnFault
	// A transfer hands the dependency slices whose elements carry original
	// callbacks; the callee may keep them past this call exactly as a
	// registration API does, so the session serves callbacks from now on.
	if retainedFunctionCallback(req, q) || len(q.Transfers) > 0 && requestHasCallbacks(req, q) {
		s.markRetainedCallbacks()
	}
	release, callbacks, err := s.enterCallbacks(ctx, req, q)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := s.begin(ctx, req); err != nil {
		return nil, err
	}
	var check func(bashPPBridgeValue) error
	check = func(v bashPPBridgeValue) error {
		if (v.Kind == "handle" || v.Kind == "callback" || v.Origin != 0) && v.Session != s.id {
			return errors.New("gosource: native handle belongs to another dependency session")
		}
		for _, e := range v.Elements {
			if err := check(e); err != nil {
				return err
			}
		}
		for _, e := range v.Fields {
			if err := check(e); err != nil {
				return err
			}
		}
		for _, e := range v.Entries {
			if err := check(e.Key); err != nil {
				return err
			}
			if err := check(e.Value); err != nil {
				return err
			}
		}
		return nil
	}
	for _, arg := range q.Args {
		if err := check(arg); err != nil {
			return nil, err
		}
	}
	if q.Receiver != nil {
		if err := check(*q.Receiver); err != nil {
			return nil, err
		}
	}
	q.ID = s.next.Add(1)
	wait := make(chan bashPPBridgeResponse, 1)
	s.mu.Lock()
	s.pending[q.ID] = wait
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, q.ID); s.mu.Unlock() }()
	s.write.Lock()
	err = json.NewEncoder(s.conn).Encode(q)
	s.write.Unlock()
	if err != nil {
		return nil, s.closedWriteError(ctx, err)
	}
	for {
		select {
		case callback := <-callbacks:
			// The callback body may write to the caller's streams directly;
			// child output raised before the callback must land first.
			s.drainOutputs()
			s.serveCallback(ctx, req.CallbackOwner, callback, q.coherence)
			if owner := req.CallbackOwner; owner != nil {
				if owner.exit.err != nil {
					return nil, owner.exit.err
				}
				if owner.exit.exiting {
					return nil, &bashPPNativeExit{status: int(owner.exit.code)}
				}
			}
		case reply := <-wait:
			// The reply crossed the control channel after the dependency's own
			// writes; the barrier keeps the next interpreted statement behind them.
			s.drainOutputs()
			// A written-back element may nest native values this session
			// still owns — a reflect.Type inside a reflect.StructField, a
			// reflect.Value the worker re-minted a handle for. They arrived
			// on its own authenticated connection, so they carry its
			// identity, exactly as a pointer writeback's pointee does; a
			// handle from any other session still fails closed downstream.
			for i := range reply.SliceUpdates {
				s.bashPPAuthenticateCallbackValue(&reply.SliceUpdates[i].Value)
			}
			if err := applyNativeSliceBuffers(req.CallbackOwner, q, reply); err != nil {
				return nil, err
			}
			// The worker reports only pointees whose structural snapshot changed,
			// so applying every reported update preserves retained-pointer writes
			// without replaying stale native copies over interpreter state.
			if err := s.applyNativePointerUpdates(req, reply); err != nil {
				return nil, err
			}
			if reply.Panic != nil {
				return nil, &bashPPCallbackPanic{value: reply.Panic.Text}
			}
			if reply.Error != "" {
				s.mu.Lock()
				forwarded := s.forwardedSignal > 0
				s.mu.Unlock()
				// A callback may already have serialized the private unwind token
				// before the signal closes the helper. Reattach its identity only
				// when this session actually forwarded that program signal.
				if forwarded && strings.HasSuffix(reply.Error, errBashPPScalarInterrupted.Error()) {
					return nil, errBashPPScalarInterrupted
				}
				if message, ok := goSourceNativeRuntimePanic(errors.New(reply.Error)); ok && reply.PanicAddr != nil {
					return nil, &bashPPNativeFaultPanic{text: message, addr: *reply.PanicAddr}
				}
				return nil, errors.New(reply.Error)
			}
			// The dependency now holds each transferred slice; rebind the
			// interpreter's supplying variable to that same storage.
			if err := s.applyNativeSliceTransfers(q, reply); err != nil {
				return nil, err
			}
			derivedCopy := reflectedCopyDerived(req, q)
			for i := range reply.Values {
				bashPPMarkReflectCopy(&reply.Values[i], derivedCopy)
				if reply.Values[i].Kind == "handle" {
					reply.Values[i].Session = s.id
					s.rememberNativeHandleType(reply.Values[i])
					if reply.Values[i].Origin != 0 && reply.Values[i].Function {
						reply.Values[i].Callbacks = true
					}
					// Only a result of a request that actually CARRIED an
					// original callback may retain one. A request merely
					// parked as a callback server for the session — every
					// request once a retained handler is registered — hands
					// its callbacks to nothing and marks nothing.
					if requestHasCallbacks(req, q) && !synchronousFunctionCallback(req, q) && !bashPPTypeDescriptorResult(req, q) && !reflectedMethodValueOf(req, q) {
						reply.Values[i].Callbacks = true
					}
				}
			}
			if owner := req.CallbackOwner; owner != nil && bashPPSetPanicOnFaultRequest(req, q) {
				owner.bashPPTools.panicOnFault = q.Args[0].Text == "true"
			}
			return reply.Values, nil
		case <-ctx.Done():
			s.closeCanceled(ctx.Err())
			return nil, ctx.Err()
		case <-s.done:
			s.waitDrains()
			s.mu.Lock()
			err := s.waitErr
			s.mu.Unlock()
			if canceled := s.canceledTermination(ctx, err); canceled != nil {
				return nil, canceled
			}
			return nil, s.programExitError(err)
		}
	}
}

func (s *bashPPNativeSession) programExitError(err error) error {
	if err == nil {
		// A clean exit is the original program terminating itself, for example
		// os.Exit(0) or a successful syscall.Exec replacement.
		return &bashPPNativeExit{}
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if exit.ExitCode() >= 0 {
			return &bashPPNativeExit{status: exit.ExitCode(), err: err}
		}
		s.mu.Lock()
		forwardedSignal := s.forwardedSignal
		s.mu.Unlock()
		if forwardedSignal > 0 {
			return &bashPPNativeExit{status: 128 + forwardedSignal, err: err, forwarded: true}
		}
	}
	return fmt.Errorf("gosource: dependency process exited: %w", err)
}
func bashPPNativeSource(ctx context.Context, req bashPPEvalRequest) (string, error) {
	// Import package export data through the same reviewed SDK and module context.
	// go doc -json is not an API and is deliberately not used.
	lookup := func(path string) (io.ReadCloser, error) {
		cmd := exec.CommandContext(ctx, req.Go, "list", "-export", "-f", "{{.Export}}", path)
		cmd.Dir, cmd.Env = req.Dir, req.Env
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("gosource: export %s: %w", path, err)
		}
		return os.Open(strings.TrimSpace(string(out)))
	}
	imp := importer.ForCompiler(token.NewFileSet(), "gc", lookup)
	paths := map[string][]string{}
	for alias, path := range req.Imports {
		paths[path] = append(paths[path], alias)
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	var imports, symbols, typeEntries strings.Builder
	var companionSymbols, unmappedFrames, forcing strings.Builder
	importAliases := map[string]string{}
	materialised := map[string]bool{}
	for _, local := range req.LocalTypes {
		materialised[local.Name] = true
	}
	// Every package's helper alias is settled before any symbol is
	// emitted: an instantiation registered under one package names types
	// of others (`errors.AsType[*fs.PathError]`), whichever order the
	// packages come in.
	blankOnly := map[string]bool{}
	for i, path := range ordered {
		if path == "C" {
			continue
		}
		blankOnly[path] = true
		for _, alias := range paths[path] {
			if !strings.HasPrefix(alias, "_:") {
				blankOnly[path] = false
			}
		}
		if blankOnly[path] {
			continue
		}
		for _, original := range paths[path] {
			importAliases[original] = fmt.Sprintf("bpppkg%d", i)
		}
	}
	for i, path := range ordered {
		if path == "C" {
			continue
		}
		if blankOnly[path] {
			fmt.Fprintf(&imports, "_ %q\n", path)
			continue
		}
		pkg, err := imp.Import(path)
		if err != nil {
			return "", err
		}
		alias := fmt.Sprintf("bpppkg%d", i)
		fmt.Fprintf(&imports, "%s %q\n", alias, path)
		used := false
		for _, name := range pkg.Scope().Names() {
			obj := pkg.Scope().Lookup(name)
			if !obj.Exported() {
				continue
			}
			keyNames := append([]string(nil), paths[path]...)
			switch obj := obj.(type) {
			case *types.TypeName:
				if alias, ok := obj.Type().(*types.Alias); ok && alias.TypeParams().Len() > 0 {
					continue
				}
				if iface, ok := obj.Type().Underlying().(*types.Interface); ok && !iface.IsMethodSet() {
					continue
				}
				if named, ok := obj.Type().(*types.Named); ok && named.TypeParams().Len() > 0 {
					continue
				}
				// A package bound under its own name (`import "strings"`
				// from Go source) is keyed once: alias and path coincide.
				keyed := map[string]bool{}
				for _, key := range append(keyNames, path) {
					if strings.HasPrefix(key, "_:") || strings.HasPrefix(key, ".:") || keyed[key] {
						continue
					}
					keyed[key] = true
					// TypeOf((*T)(nil)).Elem() is TypeFor's own definition,
					// spelled without a type argument: a type only the
					// compiler knows to be unallocatable (a struct embedding
					// internal/runtime/sys.NotInHeap) cannot instantiate
					// TypeFor but is an ordinary pointee.
					fmt.Fprintf(&typeEntries, "%q: reflect.TypeOf((*%s.%s)(nil)).Elem(),\n", key+"."+name, alias, name)
				}
				used = true
			case *types.Func:
				if sig, ok := obj.Type().(*types.Signature); ok && sig.TypeParams().Len() > 0 {
					// A generic function is a value only once instantiated:
					// the instantiations the program reaches are registered
					// under their instantiated spelling.
					emitted, err := bashPPImportedInstanceSymbols(&symbols, req.Instances, keyNames, name, alias, sig.TypeParams().Len(), importAliases, materialised)
					if err != nil {
						return "", err
					}
					used = used || emitted
					continue
				}
				for _, key := range keyNames {
					if strings.HasPrefix(key, "_:") {
						continue
					}
					symbol := key + "." + name
					if strings.HasPrefix(key, ".:") {
						symbol = name
					}
					fmt.Fprintf(&symbols, "%q: reflect.ValueOf(%s.%s),\n", symbol, alias, name)
					if bashPPForcesCollection(path, name) {
						fmt.Fprintf(&forcing, "%q: true,\n", symbol)
					}
				}
				used = true
			case *types.Var:
				for _, key := range keyNames {
					if strings.HasPrefix(key, "_:") {
						continue
					}
					symbol := key + "." + name
					if strings.HasPrefix(key, ".:") {
						symbol = name
					}
					fmt.Fprintf(&symbols, "%q: reflect.ValueOf(&%s.%s).Elem(),\n", symbol, alias, name)
				}
				used = true
			case *types.Const:
				expression := alias + "." + name
				if basic, ok := obj.Type().(*types.Basic); ok && basic.Info()&types.IsUntyped != 0 {
					switch obj.Val().Kind() {
					case constant.Int:
						if _, ok := constant.Int64Val(obj.Val()); ok {
							expression = "int64(" + expression + ")"
						} else if _, ok := constant.Uint64Val(obj.Val()); ok {
							expression = "uint64(" + expression + ")"
						} else {
							continue
						}
					case constant.Complex:
						continue
					}
				}
				for _, key := range keyNames {
					if strings.HasPrefix(key, "_:") {
						continue
					}
					symbol := key + "." + name
					if strings.HasPrefix(key, ".:") {
						symbol = name
					}
					fmt.Fprintf(&symbols, "%q: reflect.ValueOf(%s),\n", symbol, expression)
				}
				used = true
			}
		}
		if !used {
			fmt.Fprintf(&symbols, "// blank dependency %s\n", path)
			text := imports.String()
			imports.Reset()
			imports.WriteString(strings.Replace(text, alias+" ", "_ ", 1))
		}
	}
	var embeds strings.Builder
	for i, embed := range req.EmbedDecls {
		typ, err := bashPPNativeTypeImports(embed.Type, importAliases)
		if err != nil {
			return "", err
		}
		name := fmt.Sprintf("__bashpp_embed_%d", i)
		for _, directive := range embed.Directives {
			fmt.Fprintf(&embeds, "//%s\n", directive)
		}
		fmt.Fprintf(&embeds, "var %s %s\n", name, typ)
		fmt.Fprintf(&symbols, "%q: reflect.ValueOf(&%s).Elem(),\n", bashPPEmbedSymbolPrefix+embed.Name, name)
	}
	// Original locally declared types become real declarations here, so the
	// dependency sees the original field names, field types and defined type
	// name. Only the declaration crosses over: a mirrored String/Error body is
	// a fixed callback into the interpreter, never compiled original code.
	var locals strings.Builder
	localTypes := append([]bashPPLocalType(nil), req.LocalTypes...)
	for i := range localTypes {
		mapped, err := bashPPNativeTypeImports(localTypes[i].Decl, importAliases)
		if err != nil {
			return "", err
		}
		localTypes[i].Decl = mapped
		if localTypes[i].GenericDecl != "" {
			mapped, err = bashPPNativeDeclImports(localTypes[i].GenericDecl, importAliases)
			if err != nil {
				return "", err
			}
			localTypes[i].GenericDecl = mapped
		}
		if localTypes[i].PublicType != "" {
			mapped, err = bashPPNativeTypeImports(localTypes[i].PublicType, importAliases)
			if err != nil {
				return "", err
			}
			localTypes[i].PublicType = mapped
		}
		// Mirrored signatures name imported types the same way the original
		// program did; the helper knows them only by its own generated aliases.
		methods := append([]bashPPLocalMethod(nil), localTypes[i].Methods...)
		rewrite := func(list []string) ([]string, error) {
			out := make([]string, len(list))
			for k, text := range list {
				mapped, err := bashPPNativeTypeImports(text, importAliases)
				if err != nil {
					return nil, err
				}
				out[k] = mapped
			}
			return out, nil
		}
		for j := range methods {
			// Copy rather than rewrite in place: the caller's descriptors are
			// the session identity key and must keep the original spellings.
			if methods[j].Params, err = rewrite(methods[j].Params); err != nil {
				return "", err
			}
			if methods[j].Results, err = rewrite(methods[j].Results); err != nil {
				return "", err
			}
		}
		localTypes[i].Methods = methods
	}
	for _, local := range localTypes {
		locals.WriteString(bashPPLocalTypeGo(local))
		// Aliases inherit their target's identity; registering an alias here would
		// overwrite the metadata of that target's exact reflect.Type.
		if id := local.Identity; id != nil && !local.Alias {
			fmt.Fprintf(&locals, "func init(){originalTypeIdentities[reflect.TypeFor[%s]()] = originalTypeIdentity{Name:%q, PackageName:%q, PackagePath:%q}}\n", local.Name, id.Name, id.PackageName, id.PackagePath)
		}

		// Both spellings resolve: the original program's own name, and the
		// package-qualified identity Go's %T prints for it.
		fmt.Fprintf(&typeEntries, "%q: reflect.TypeFor[%s](),\n%q: reflect.TypeFor[%s](),\n", local.Name, local.Name, "main."+local.Name, local.Name)
		if local.WireType != "" {
			fmt.Fprintf(&typeEntries, "%q: reflect.TypeFor[%s](),\n", local.WireType, local.Name)
		}
	}
	for _, fn := range req.NativeFuncs {
		if !syntax.BashPPValidIdent(fn.Name) {
			return "", fmt.Errorf("gosource: invalid native companion function %q", fn.Name)
		}
		fmt.Fprintf(&companionSymbols, "%q: true,\n", fn.Name)
		params, results, err := bashPPNativeFuncSignatureImports(fn.Params, fn.Results, importAliases)
		if err != nil {
			return "", err
		}
		if results == "" {
			fmt.Fprintf(&locals, "func %s(%s)\n", fn.Name, params)
		} else {
			fmt.Fprintf(&locals, "func %s(%s)(%s)\n", fn.Name, params, results)
		}
		fmt.Fprintf(&symbols, "%q: reflect.ValueOf(%s),\n", fn.Name, fn.Name)
	}
	// A companion object may call a package function whose body is interpreted.
	// The trampoline is what makes the companion link; it compiles no original
	// statement, it only carries the call back to the interpreter that owns it.
	for _, fn := range req.CompanionTrampolines {
		if !syntax.BashPPValidIdent(fn.Name) {
			return "", fmt.Errorf("gosource: invalid assembly companion trampoline %q", fn.Name)
		}
		params, err := bashPPNativeTypeTexts(fn.Params, importAliases)
		if err != nil {
			return "", err
		}
		results, err := bashPPNativeTypeTexts(fn.Results, importAliases)
		if err != nil {
			return "", err
		}
		locals.WriteString(bashPPCompanionTrampolineGo(fn.Name, params, results))
	}
	for pi, pkg := range req.CgoPackages {
		for _, symbol := range pkg.Symbols {
			if symbol.Kind != "func" {
				return "", fmt.Errorf("gosource: cgo package %q selector C.%s has unsupported kind %q", pkg.Path, symbol.Name, symbol.Kind)
			}
			fmt.Fprintf(&symbols, "%q: reflect.ValueOf(__bashpp_cgo_%d_%s),\n", pkg.Alias+"."+symbol.Name, pi, symbol.Name)
		}
	}
	// The frames the runtime has no pointer map for. Their names are reported
	// back in the refusal a forced collection gets while one of them is live.
	for _, name := range req.CompanionUnmappedFrames {
		fmt.Fprintf(&unmappedFrames, "%q,\n", name)
	}
	codecs, err := bashPPLocalCodecsGo(localTypes)
	if err != nil {
		return "", err
	}
	locals.WriteString(codecs)
	source := strings.Replace(bashPPNativeWorker, "//IMPORTS", imports.String(), 1)
	source = strings.Replace(source, "//EMBEDS", embeds.String(), 1)
	source = strings.Replace(source, "//SYMBOLS", symbols.String(), 1)
	source = strings.Replace(source, "//TYPES", typeEntries.String(), 1)
	source = strings.Replace(source, "//LOCALTYPES", locals.String(), 1)
	source = strings.Replace(source, "//COMPANIONSYMBOLS", companionSymbols.String(), 1)
	source = strings.Replace(source, "//UNMAPPEDFRAMES", unmappedFrames.String(), 1)
	source = strings.Replace(source, "//FORCESGC", forcing.String(), 1)
	return source, nil
}

func bashPPEmbedIdentity(decls []bashPPEmbedDecl) string {
	data, _ := json.Marshal(decls)
	return string(data)
}

func bashPPNativeCompanionIdentity(files []string, funcs []bashPPNativeFuncDecl, trampolines []bashPPCompanionTrampoline, unmapped []string) string {
	data, _ := json.Marshal(struct {
		Files          []string
		Funcs          []bashPPNativeFuncDecl
		Trampolines    []bashPPCompanionTrampoline
		UnmappedFrames []string
	}{files, funcs, trampolines, unmapped})
	return string(data)
}

// bashPPNativeTypeTexts maps declared type spellings into the helper's own
// import aliases, one entry per declared value.
func bashPPNativeTypeTexts(texts []string, aliases map[string]string) ([]string, error) {
	out := make([]string, len(texts))
	for i, text := range texts {
		mapped, err := bashPPNativeTypeImports(text, aliases)
		if err != nil {
			return nil, err
		}
		out[i] = mapped
	}
	return out, nil

}

func bashPPCgoIdentity(packages []syntax.CgoPackage) string {
	data, _ := json.Marshal(packages)
	return string(data)
}

func bashPPNativeFuncSignatureImports(params, results string, aliases map[string]string) (string, string, error) {
	source := "func _(" + params + ")"
	if results != "" {
		source += "(" + results + ")"
	}
	decl, err := bashPPNativeDeclImports(source, aliases)
	if err != nil {
		return "", "", err
	}
	decl = strings.TrimSpace(decl)
	open := strings.IndexByte(decl, '(')
	if open < 0 {
		return "", "", fmt.Errorf("gosource: malformed native companion signature %q", decl)
	}
	close := strings.IndexByte(decl[open+1:], ')')
	if close < 0 {
		return "", "", fmt.Errorf("gosource: malformed native companion signature %q", decl)
	}
	close += open + 1
	params = decl[open+1 : close]
	results = strings.TrimSpace(decl[close+1:])
	if strings.HasPrefix(results, "(") && strings.HasSuffix(results, ")") {
		results = strings.TrimSuffix(strings.TrimPrefix(results, "("), ")")
	}
	return params, results, nil
}

func bashPPBridgeLiteral(text string) (bashPPBridgeValue, error) {
	if text == "nil" {
		return bashPPBridgeValue{Kind: "nil"}, nil
	}
	if str, err := strconv.Unquote(text); err == nil {
		return bashPPBridgeValue{Kind: "string", Text: str, Bytes: []byte(str)}, nil
	}
	if text == "true" || text == "false" {
		return bashPPBridgeValue{Kind: "bool", Text: text}, nil
	}
	for _, kind := range []token.Token{token.INT, token.FLOAT} {
		v := constant.MakeFromLiteral(text, kind, 0)
		if v.Kind() == constant.Unknown {
			continue
		}
		if kind == token.INT {
			return bashPPBridgeValue{Kind: "int", Text: v.ExactString()}, nil
		}
		number, _ := constant.Float64Val(v)
		return bashPPBridgeValue{Kind: "float", Text: strconv.FormatFloat(number, 'g', -1, 64)}, nil
	}
	return bashPPBridgeValue{}, fmt.Errorf("gosource: bridge accepts evaluated values, not argument source %q", text)
}
func (s *bashPPNativeSession) legacy(ctx context.Context, req bashPPEvalRequest) ([]bashPPBridgeValue, error) {
	args := make([]bashPPBridgeValue, len(req.Args))
	for i, text := range req.Args {
		v, err := bashPPBridgeLiteral(text)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	return s.request(ctx, req, bashPPBridgeRequest{Op: "call", Selector: strings.Join(req.Selector, "."), Args: args})
}

// GoSourceModuleDir binds dependency resolution to the original source module
// while Dir remains the executing program's cwd. The option is inert for shell
// source; callers should set it when Go source and runtime assets are separated.
func GoSourceModuleDir(dir string) RunnerOption {
	return func(r *Runner) error {
		absolute, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		info, err := os.Stat(absolute)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("gosource: module context is not a directory: %s", dir)
		}
		r.bashPPTools.moduleDir = absolute
		return nil
	}
}
func bashPPModuleRequest(req bashPPEvalRequest) bashPPEvalRequest {
	if req.ModuleDir != "" {
		req.Dir = req.ModuleDir
	}
	return req
}
func (r *Runner) bashPPStartGoSourceBridge(ctx context.Context) error {
	if !r.bashPPGoSource || len(r.bashPPImports) == 0 {
		return nil
	}
	req, err := r.bashPPEvalRequest()
	if err != nil {
		return err
	}
	return req.Bridge.begin(ctx, req)
}
func (r *Runner) bashPPBridgeRegisterScalarTypes(ctx context.Context, req bashPPEvalRequest, path, alias string) error {
	if alias == "_" {
		return nil
	}
	req = bashPPModuleRequest(req)
	lookup := func(path string) (io.ReadCloser, error) {
		cmd := exec.CommandContext(ctx, req.Go, "list", "-export", "-f", "{{.Export}}", path)
		cmd.Dir, cmd.Env = req.Dir, req.Env
		out, err := cmd.Output()
		if err != nil {
			return nil, err
		}
		return os.Open(strings.TrimSpace(string(out)))
	}
	pkg, err := importer.ForCompiler(token.NewFileSet(), "gc", lookup).Import(path)
	if err != nil {
		return err
	}
	if r.bashPPTypes == nil {
		r.bashPPTypes = map[string]bashPPType{}
	}
	// Copy on write: public subshells and interpreted tasks may share the
	// previous immutable export metadata without sharing import mutations.
	nativeTypes := make(map[string]types.Type, len(r.bashPPTools.nativeTypes)+pkg.Scope().Len())
	for name, typ := range r.bashPPTools.nativeTypes {
		nativeTypes[name] = typ
	}
	for _, name := range pkg.Scope().Names() {
		if object, ok := pkg.Scope().Lookup(name).(*types.TypeName); ok && object.Exported() {
			nativeTypes[path+"."+name] = object.Type()
		}
	}
	r.bashPPTools.nativeTypes = nativeTypes
	for _, name := range pkg.Scope().Names() {
		object, ok := pkg.Scope().Lookup(name).(*types.TypeName)
		if !ok || !object.Exported() {
			continue
		}
		basic, ok := object.Type().Underlying().(*types.Basic)
		if !ok {
			continue
		}
		key := alias + "." + name
		if alias == "." {
			key = name
		}
		canonical := path + "." + name
		if named, ok := types.Unalias(object.Type()).(*types.Named); ok && named.Obj().Pkg() != nil {
			canonical = named.Obj().Pkg().Path() + "." + named.Obj().Name()
		}
		r.bashPPTypes[canonical] = bashPPType{underlying: basic.Name(), typeExpr: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: basic.Name()}}}
		for _, binding := range []string{key, path + "." + name} {
			if binding != canonical {
				r.bashPPTypes[binding] = bashPPType{underlying: basic.Name(), alias: true, typeExpr: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: canonical}}}
			}
		}
	}
	return nil
}

// A sibling may have passed its initial context check before EOF cancellation
// closed the shared connection. Only that causally identified local close is
// cancellation; ordinary network failures and live-context requests stay errors.
func (s *bashPPNativeSession) closedWriteError(ctx context.Context, err error) error {
	if ctx.Err() == nil || !errors.Is(err, net.ErrClosed) {
		return err
	}
	s.mu.Lock()
	canceled := s.closeCancellation != nil || s.processCanceledLocked(s.waitErr)
	s.mu.Unlock()
	if canceled {
		return ctx.Err()
	}
	return err
}

// A signaled dependency process is cancellation only when its command context
// requested that kill. An ordinary exit status remains the program's own exit.
func (s *bashPPNativeSession) processCanceledLocked(err error) bool {
	var exit *exec.ExitError
	return s.processCancellation != nil && errors.As(err, &exit) && exit.ExitCode() < 0
}
func (s *bashPPNativeSession) canceledTermination(ctx context.Context, err error) error {
	if ctx.Err() == nil {
		return nil
	}
	// A close response can terminate the helper before the closer records
	// its provenance. Wait for that close transaction before inspecting it.
	s.write.Lock()
	defer s.write.Unlock()
	s.mu.Lock()
	canceled := s.processCanceledLocked(err)
	if s.closeCancellation != nil {
		var exit *exec.ExitError
		canceled = canceled || err == nil || errors.As(err, &exit) && exit.ExitCode() < 0
	}
	s.mu.Unlock()
	if canceled {
		return ctx.Err()
	}
	return nil
}

func (s *bashPPNativeSession) markRetainedCallbacks() {
	s.mu.Lock()
	s.retained = true
	s.mu.Unlock()
}

func (s *bashPPNativeSession) retainedCallbacks() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retained
}

type bashPPNativeFaultPanic struct {
	text string
	addr uint64
}

func (p *bashPPNativeFaultPanic) Error() string { return p.text }

func bashPPSetPanicOnFaultRequest(req bashPPEvalRequest, q bashPPBridgeRequest) bool {
	if q.Op != "call" || len(q.Args) != 1 || q.Args[0].Kind != "bool" {
		return false
	}
	pkg, name, ok := strings.Cut(q.Selector, ".")
	return ok && name == "SetPanicOnFault" && req.Imports[pkg] == "runtime/debug"
}

// bashPPForcesCollection reports an imported function that starts a collection
// cycle whatever the pacing says. While an unmapped companion frame is live the
// helper holds collection off, and these are the calls that would defeat that;
// the helper serialises them against that window rather than crashing in it.
func bashPPForcesCollection(path, name string) bool {
	switch path {
	case "runtime":
		return name == "GC"
	case "runtime/debug":
		return name == "FreeOSMemory" || name == "SetGCPercent" || name == "SetMemoryLimit"
	}
	return false
}
