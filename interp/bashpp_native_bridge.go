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
	ReaderBuffer []byte              `json:"reader_buffer,omitempty"`
	ReaderLength int                 `json:"reader_length,omitempty"`
	CallArgs     []bashPPBridgeValue `json:"call_args,omitempty"`
	sliceView    *bashPPNativeSlice  // host-only original backing view

	// Callable is derived by the interpreter from authenticated native type or
	// import metadata; the dependency worker cannot set callback policy itself.
	Callable   string                       `json:"-"`
	NativeType string                       `json:"native_type,omitempty"`
	Callbacks  bool                         `json:"callbacks,omitempty"`
	Function   bool                         `json:"function,omitempty"`
	Origin     uint64                       `json:"origin,omitempty"`
	Interface  string                       `json:"interface,omitempty"`
	Session    string                       `json:"session,omitempty"`
	Kind       string                       `json:"kind"`
	Type       string                       `json:"type,omitempty"`
	Text       string                       `json:"text,omitempty"`
	Handle     uint64                       `json:"handle,omitempty"`
	Elements   []bashPPBridgeValue          `json:"elements,omitempty"`
	Fields     map[string]bashPPBridgeValue `json:"fields,omitempty"`
	Entries    []bashPPBridgeEntry          `json:"entries,omitempty"`
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
	sliceElem  []syntax.BashPPTypeExpr
	ID         uint64              `json:"id"`
	Op         string              `json:"op"`
	Selector   string              `json:"selector"`
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
	PtrUpdates   []bashPPBridgeValue       `json:"ptr_updates,omitempty"`

	Panic *bashPPBridgeValue `json:"panic,omitempty"`
	ID    uint64             `json:"id"`
	// Op, Selector and Receiver are set only when the dependency is asking the
	// interpreter to run an original method body it must not compile itself.
	Op       string              `json:"op,omitempty"`
	Selector string              `json:"selector,omitempty"`
	Receiver *bashPPBridgeValue  `json:"receiver,omitempty"`
	Values   []bashPPBridgeValue `json:"values,omitempty"`
	Error    string              `json:"error,omitempty"`
}
type bashPPNativeSession struct {
	functions       map[uint64]*bashPPFunc
	functionNext    uint64
	callbackGate    chan struct{}
	activeCallbacks chan bashPPBridgeResponse
	callbackOwner   *Runner
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
	imports             string
	locals              string
	embeds              string
	id                  string
}

func (r *Runner) closeGoSourceBridge() {
	r.bashPPTools.nativeTypes = nil
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
		return nil
	}
	source, err := bashPPNativeSource(ctx, bashPPModuleRequest(req))
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	secret := make([]byte, 24)
	if _, err = rand.Read(secret); err != nil {
		return err
	}
	auth := hex.EncodeToString(secret)
	s.id = auth[:16]
	source = strings.Replace(source, "//CONNECTION", "const bridgeAddress = "+strconv.Quote(listener.Addr().String())+"\nconst bridgeAuth = "+strconv.Quote(auth), 1)
	scratchEnv := req.RuntimeEnv
	if scratchEnv == nil {
		scratchEnv = req.Env
	}
	sourceDir := bashPPModuleRequest(req).Dir
	policy := bashPPScratchIsolated
	if len(req.EmbedDecls) > 0 {
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
	build := exec.CommandContext(ctx, req.Go, "build", "-p", "2", "-overlay="+file.overlay, "-o", binary, file.buildPath)
	build.Dir, build.Env = bashPPModuleRequest(req).Dir, setEnvString(req.Env, "CGO_ENABLED", "0")
	var diagnostics bytes.Buffer
	build.Stdout, build.Stderr = &diagnostics, &diagnostics
	if err = build.Run(); err != nil {
		cleanup()
		return fmt.Errorf("gosource: build dependency bridge: %w: %s", err, diagnostics.String())
	}
	cmd := exec.CommandContext(ctx, binary)
	// Bootstrap data is compiled into this ephemeral dependency-only helper.
	// It must not pollute os.Args or os.Environ, including dependency init.
	cmd.Args = append([]string(nil), req.Argv...)
	cmd.Dir, cmd.Env = req.Dir, req.RuntimeEnv
	cmd.Stdin, cmd.Stdout, cmd.Stderr = req.Stdin, req.Stdout, req.Stderr
	bashPPNativeProcessGroup(cmd)
	cmd.Cancel = func() error {
		s.mu.Lock()
		s.processCancellation = ctx.Err()
		s.mu.Unlock()
		bashPPNativeKill(cmd)
		return nil
	}
	if err = cmd.Start(); err != nil {
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
					s.serveCallback(ctx, nil, reply)
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
	if err := validateLocalTransport(req, q); err != nil {
		return nil, err
	}
	if retainedFunctionCallback(req, q) {
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
			s.serveCallback(ctx, req.CallbackOwner, callback)
			if owner := req.CallbackOwner; owner != nil {
				if owner.exit.err != nil {
					return nil, owner.exit.err
				}
				if owner.exit.exiting {
					return nil, &bashPPNativeExit{status: int(owner.exit.code)}
				}
			}
		case reply := <-wait:
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
				return nil, errors.New(reply.Error)
			}
			for i := range reply.Values {
				if reply.Values[i].Kind == "handle" {
					reply.Values[i].Session = s.id
					// Only a result of a request that actually CARRIED an
					// original callback may retain one. A request merely
					// parked as a callback server for the session — every
					// request once a retained handler is registered — hands
					// its callbacks to nothing and marks nothing.
					if requestHasCallbacks(req, q) && !synchronousFunctionCallback(req, q) {
						reply.Values[i].Callbacks = true
					}
				}
			}
			return reply.Values, nil
		case <-ctx.Done():
			s.closeCanceled(ctx.Err())
			return nil, ctx.Err()
		case <-s.done:
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
	importAliases := map[string]string{}
	for i, path := range ordered {
		blankOnly := true
		for _, alias := range paths[path] {
			if !strings.HasPrefix(alias, "_:") {
				blankOnly = false
			}
		}
		if blankOnly {
			fmt.Fprintf(&imports, "_ %q\n", path)
			continue
		}
		pkg, err := imp.Import(path)
		if err != nil {
			return "", err
		}
		alias := fmt.Sprintf("bpppkg%d", i)
		for _, original := range paths[path] {
			importAliases[original] = alias
		}
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
					fmt.Fprintf(&typeEntries, "%q: reflect.TypeFor[%s.%s](),\n", key+"."+name, alias, name)
				}
				used = true
			case *types.Func:
				if sig, ok := obj.Type().(*types.Signature); ok && sig.TypeParams().Len() > 0 {
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
		// Both spellings resolve: the original program's own name, and the
		// package-qualified identity Go's %T prints for it.
		fmt.Fprintf(&typeEntries, "%q: reflect.TypeFor[%s](),\n%q: reflect.TypeFor[%s](),\n", local.Name, local.Name, "main."+local.Name, local.Name)
		if local.WireType != "" {
			fmt.Fprintf(&typeEntries, "%q: reflect.TypeFor[%s](),\n", local.WireType, local.Name)
		}
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
	return source, nil
}

func bashPPEmbedIdentity(decls []bashPPEmbedDecl) string {
	data, _ := json.Marshal(decls)
	return string(data)
}

func bashPPBridgeLiteral(text string) (bashPPBridgeValue, error) {
	if text == "nil" {
		return bashPPBridgeValue{Kind: "nil"}, nil
	}
	if str, err := strconv.Unquote(text); err == nil {
		return bashPPBridgeValue{Kind: "string", Text: str}, nil
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
