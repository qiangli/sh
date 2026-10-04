package interp

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"go/ast"
	"go/parser"
	"go/token"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

// The table is deliberately made of linked, typed symbols. A Go import alias
// is authenticated through req.Imports before a name is looked up here.
var fastStrconvSymbols = map[string]reflect.Value{
	"Itoa":               reflect.ValueOf(strconv.Itoa),
	"FormatBool":         reflect.ValueOf(strconv.FormatBool),
	"FormatInt":          reflect.ValueOf(strconv.FormatInt),
	"FormatUint":         reflect.ValueOf(strconv.FormatUint),
	"FormatFloat":        reflect.ValueOf(strconv.FormatFloat),
	"FormatComplex":      reflect.ValueOf(strconv.FormatComplex),
	"Quote":              reflect.ValueOf(strconv.Quote),
	"QuoteToASCII":       reflect.ValueOf(strconv.QuoteToASCII),
	"QuoteToGraphic":     reflect.ValueOf(strconv.QuoteToGraphic),
	"QuoteRune":          reflect.ValueOf(strconv.QuoteRune),
	"QuoteRuneToASCII":   reflect.ValueOf(strconv.QuoteRuneToASCII),
	"QuoteRuneToGraphic": reflect.ValueOf(strconv.QuoteRuneToGraphic),
	"CanBackquote":       reflect.ValueOf(strconv.CanBackquote),
}

// Each entry is a method of one session-owned generator, except Seed, which
// is handled below. Keep this set in sync with the source-level ownership
// proof; an uncovered package-level operation disables the whole package.
var fastRandMethods = map[string]string{
	"Int": "Int", "Int31": "Int31", "Int31n": "Int31n",
	"Int63": "Int63", "Int63n": "Int63n", "Intn": "Intn",
	"Uint": "Uint", "Uint32": "Uint32", "Uint64": "Uint64",
	"Float32": "Float32", "Float64": "Float64",
	"NormFloat64": "NormFloat64", "ExpFloat64": "ExpFloat64",
}

// goSourceFastStdlibCall returns handled=false for every shape outside the
// small value-only contract. Fallback then follows the ordinary worker path.
func goSourceFastStdlibCall(ctx context.Context, req bashPPEvalRequest, q bashPPBridgeRequest) (values []bashPPBridgeValue, handled bool, err error) {
	if req.disableFastStdlib || q.Op != "call" || q.Receiver != nil || q.Instance != "" || q.Spread || len(q.Transfers) != 0 || len(q.SliceBuffers) != 0 || q.FormatOnly != "" {
		return nil, false, nil
	}
	alias, name, ok := strings.Cut(q.Selector, ".")
	if !ok || alias == "" || name == "" {
		return nil, false, nil
	}
	path := req.Imports[alias]
	var fn reflect.Value
	switch path {
	case "strconv":
		fn = fastStrconvSymbols[name]
	case "math/rand":
		if req.Bridge == nil || !req.Bridge.fastRandAllowed(req, alias) {
			return nil, false, nil
		}
		if name != "Seed" && fastRandMethods[name] == "" {
			return nil, false, nil
		}
	default:
		return nil, false, nil
	}
	if path == "strconv" && !fn.IsValid() {
		return nil, false, nil
	}
	for _, arg := range q.Args {
		if !fastPlainBridgeValue(arg) {
			return nil, false, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	if err := validateLocalTransport(req, q); err != nil {
		return nil, true, err
	}
	if path == "math/rand" {
		req.Bridge.fastRandMu.Lock()
		defer req.Bridge.fastRandMu.Unlock()
		if req.Bridge.fastRand == nil {
			var seed [8]byte
			if fastRandDebugSetting(req, "randautoseed") == "0" {
				binary.LittleEndian.PutUint64(seed[:], 1)
			} else {
				if _, err := crand.Read(seed[:]); err != nil {
					return nil, true, err
				}
			}
			req.Bridge.fastRand = rand.New(rand.NewSource(int64(binary.LittleEndian.Uint64(seed[:]))))
		}
		if name == "Seed" {
			fn = reflect.ValueOf(req.Bridge.fastRand.Seed)
		} else {
			fn = reflect.ValueOf(req.Bridge.fastRand).MethodByName(fastRandMethods[name])
		}
	}
	if !fn.IsValid() {
		return nil, false, nil
	}
	sig := fn.Type()
	if sig.IsVariadic() || sig.NumIn() != len(q.Args) {
		return nil, false, nil
	}
	args := make([]reflect.Value, len(q.Args))
	for i, arg := range q.Args {
		v, ok := fastDecodeScalar(arg, sig.In(i))
		if !ok {
			return nil, false, nil
		}
		args[i] = v
	}
	for i := 0; i < sig.NumOut(); i++ {
		if !fastPlainType(sig.Out(i)) {
			return nil, false, nil
		}
	}
	if path == "math/rand" && name == "Seed" && !fastRandSeedActive(req) {
		// Since Go 1.24 the package-level Seed is a no-op by default.
		return nil, true, nil
	}
	defer func() {
		if recover() != nil {
			// Preserve the worker's panic translation and interpreter unwind.
			// The admitted functions validate before changing observable state.
			values, handled, err = nil, false, nil
		}
	}()
	handled = true
	results := fn.Call(args)
	values = make([]bashPPBridgeValue, len(results))
	for i, result := range results {
		values[i] = fastEncodeScalar(result)
	}
	return values, handled, nil
}

func fastPlainBridgeValue(v bashPPBridgeValue) bool {
	if v.Origin != 0 || v.Storage != 0 || v.Handle != 0 || v.Session != "" || v.Callbacks || v.sliceView != nil || v.localReflect != nil || v.localCell != nil || v.Interface != "" || len(v.Elements) != 0 || len(v.Fields) != 0 || len(v.Entries) != 0 || v.Within != nil {
		return false
	}
	switch v.Kind {
	case "bool", "int", "uint", "float", "complex", "string":
		return true
	}
	return false
}

func fastPlainType(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128, reflect.String:
		return true
	}
	return false
}

func fastDecodeScalar(v bashPPBridgeValue, target reflect.Type) (reflect.Value, bool) {
	if !fastPlainType(target) || v.Type != "" && v.Type != target.String() {
		return reflect.Value{}, false
	}
	r := reflect.New(target).Elem()
	var err error
	switch target.Kind() {
	case reflect.Bool:
		var x bool
		x, err = strconv.ParseBool(v.Text)
		if v.Kind != "bool" {
			return reflect.Value{}, false
		}
		r.SetBool(x)
	case reflect.String:
		if v.Kind != "string" {
			return reflect.Value{}, false
		}
		r.SetString(v.stringText())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var x int64
		x, err = strconv.ParseInt(v.Text, 10, 64)
		if v.Kind != "int" || err == nil && r.OverflowInt(x) {
			return reflect.Value{}, false
		}
		r.SetInt(x)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		var x uint64
		x, err = strconv.ParseUint(v.Text, 10, 64)
		if v.Kind != "uint" || err == nil && r.OverflowUint(x) {
			return reflect.Value{}, false
		}
		r.SetUint(x)
	case reflect.Float32, reflect.Float64:
		var x float64
		x, err = strconv.ParseFloat(v.Text, target.Bits())
		if v.Kind != "float" {
			return reflect.Value{}, false
		}
		r.SetFloat(x)
	case reflect.Complex64, reflect.Complex128:
		var x complex128
		x, err = strconv.ParseComplex(v.Text, target.Bits())
		if v.Kind != "complex" {
			return reflect.Value{}, false
		}
		r.SetComplex(x)
	}
	return r, err == nil
}

func fastEncodeScalar(v reflect.Value) bashPPBridgeValue {
	out := bashPPBridgeValue{Type: v.Type().String(), NativeType: v.Type().String()}
	switch v.Kind() {
	case reflect.String:
		out.Kind, out.Text = "string", v.String()
		out.Bytes = []byte(out.Text)
	case reflect.Bool:
		out.Kind, out.Text = "bool", strconv.FormatBool(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		out.Kind, out.Text = "int", strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		out.Kind, out.Text = "uint", strconv.FormatUint(v.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		out.Kind, out.Text = "float", strconv.FormatFloat(v.Float(), 'g', -1, v.Type().Bits())
	case reflect.Complex64, reflect.Complex128:
		out.Kind, out.Text = "complex", strconv.FormatComplex(v.Complex(), 'g', -1, v.Type().Bits())
	}
	return out
}

// Only direct calls to covered top-level functions may share the session's
// interpreter-owned random state. An unreadable or composite source refuses
// the optimization. A selector used as a function value also refuses it.
func fastRandSourceProof(req bashPPEvalRequest, alias string) bool {
	if len(req.RootFiles) > 1 {
		return false
	}
	if len(req.CompanionFiles) != 0 || len(req.NativeFuncs) != 0 || len(req.MappedCompanions) != 0 || len(req.CgoPackages) != 0 {
		return false
	}
	// Other linked dependency code could call math/rand's global functions
	// while this program's calls use the local generator. Admit only packages
	// whose entry points do not do that.
	for _, path := range req.Imports {
		switch path {
		case "math/rand", "strconv", "fmt", "bytes", "strings":
		default:
			return false
		}
	}
	if req.SourceFile == "" {
		return false
	}
	data, err := os.ReadFile(req.SourceFile)
	if err != nil {
		return false
	}
	file, err := parser.ParseFile(token.NewFileSet(), req.SourceFile, data, 0)
	if err != nil {
		return false
	}
	approved := make(map[*ast.SelectorExpr]bool)
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == alias && (sel.Sel.Name == "Seed" || fastRandMethods[sel.Sel.Name] != "") {
					approved[sel] = true
				}
			}
		}
		return true
	})
	valid := true
	ast.Inspect(file, func(n ast.Node) bool {
		// A local declaration with the import's spelling can redirect one of
		// the selectors to a different value. Refuse the package as a unit.
		switch decl := n.(type) {
		case *ast.AssignStmt:
			if decl.Tok == token.DEFINE {
				for _, lhs := range decl.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Name == alias {
						valid = false
					}
				}
			}
		case *ast.ValueSpec:
			for _, id := range decl.Names {
				if id.Name == alias {
					valid = false
				}
			}
		case *ast.Field:
			for _, id := range decl.Names {
				if id.Name == alias {
					valid = false
				}
			}
		case *ast.RangeStmt:
			if decl.Tok == token.DEFINE {
				for _, item := range []ast.Expr{decl.Key, decl.Value} {
					if id, ok := item.(*ast.Ident); ok && id.Name == alias {
						valid = false
					}
				}
			}
		}
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == alias && !approved[sel] {
				valid = false
			}
		}
		return valid
	})
	return valid
}

func (s *bashPPNativeSession) fastRandAllowed(req bashPPEvalRequest, alias string) bool {
	s.fastRandMu.Lock()
	defer s.fastRandMu.Unlock()
	if s.fastRandProofSeen == nil {
		s.fastRandProofSeen = make(map[string]bool)
		s.fastRandProof = make(map[string]bool)
	}
	key := req.SourceFile + "\x00" + alias
	if !s.fastRandProofSeen[key] {
		s.fastRandProof[key] = fastRandSourceProof(req, alias)
		s.fastRandProofSeen[key] = true
	}
	return s.fastRandProof[key]
}

func fastRandSeedActive(req bashPPEvalRequest) bool {
	return fastRandDebugSetting(req, "randseednop") == "0"
}

func fastRandDebugSetting(req bashPPEvalRequest, name string) string {
	debug := os.Getenv("GODEBUG")
	for _, pair := range req.Env {
		if value, ok := strings.CutPrefix(pair, "GODEBUG="); ok {
			debug = value
		}
	}
	// The original program's default GODEBUG (its module's go directive,
	// godebug lines and //go:debug directives) applies beneath the
	// environment, exactly as in a native build and as the worker link
	// replays it; see bashPPDefaultGODEBUG.
	debug = fastProgramDefaultGODEBUG(req) + "," + debug
	value := ""
	for _, setting := range strings.Split(debug, ",") {
		if selected, ok := strings.CutPrefix(setting, name+"="); ok {
			value = selected
		}
	}
	return value
}

// fastDefaultGODEBUG caches the default GODEBUG of each original program
// directory: establishing it runs the go command once.
var fastDefaultGODEBUG sync.Map

func fastProgramDefaultGODEBUG(req bashPPEvalRequest) string {
	key := req.SourceDir
	if key == "" {
		key = bashPPModuleRequest(req).Dir
	}
	if cached, ok := fastDefaultGODEBUG.Load(key); ok {
		return cached.(string)
	}
	value := bashPPDefaultGODEBUG(context.Background(), req)
	fastDefaultGODEBUG.Store(key, value)
	return value
}
