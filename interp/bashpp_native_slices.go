package interp

// Sprint: #118; Story: #54; Story-ID: c3a60493cde9
import (
	"cmp"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// A captured slice header retains the original backing storage even if the
// source binding changes before a deferred call. No original expression or
// Runner is captured here.
type bashPPNativeSlice struct {
	view []any
	meta *bashPPCollectionMeta
	typ  syntax.BashPPTypeExpr
}
type bashPPNativeSliceBuffer struct {
	Index  int               `json:"index"`
	Length int               `json:"length"`
	Value  bashPPBridgeValue `json:"value"`
}

func nativeSliceCallable(req bashPPEvalRequest, q bashPPBridgeRequest) string {
	if q.Receiver != nil {
		if q.Selector == "" {
			return q.Receiver.Callable
		}
		return q.Receiver.NativeType + "." + q.Selector
	}
	alias, name, ok := strings.Cut(q.Selector, ".")
	if !ok {
		return ""
	}
	return req.Imports[alias] + "." + name
}
func nativeReaderType(name string) bool {
	switch name {
	case "*strings.Reader", "*bytes.Reader", "*bytes.Buffer", "*bufio.Reader", "*os.File":
		return true
	}
	return false
}
func nativeSliceReadIndex(req bashPPEvalRequest, q bashPPBridgeRequest) int {
	name := nativeSliceCallable(req, q)
	for _, method := range []string{".Read", ".ReadAt"} {
		if owner, ok := strings.CutSuffix(name, method); ok && nativeReaderType(owner) {
			return 0
		}
	}
	if (name == "io.ReadFull" || name == "io.ReadAtLeast") && len(q.Args) > 0 && q.Args[0].Kind == "handle" && nativeReaderType(q.Args[0].NativeType) {
		return 1
	}
	return -1
}
func nativeSliceReadOnly(name string) bool {
	switch name {
	case "fmt.Print", "fmt.Println", "fmt.Printf", "fmt.Sprint", "fmt.Sprintln", "fmt.Sprintf", "fmt.Errorf", "fmt.Fprint", "fmt.Fprintln", "fmt.Fprintf",
		"bytes.Equal", "bytes.Compare", "bytes.Contains", "bytes.Count", "bytes.HasPrefix", "bytes.HasSuffix", "bytes.Index", "bytes.IndexByte", "bytes.IndexAny", "bytes.LastIndex", "bytes.LastIndexByte", "bytes.LastIndexAny", "bytes.Clone",
		"strings.Join", "os.WriteFile", "syscall.Exec",
		// slices.Equal only reads both transported slices to answer a bool; it
		// retains neither. The generic function is not a reflectable dependency
		// symbol, so nativeSliceGenericHelper computes the result interpreter-side.
		"slices.Equal", "maps.Equal",
		"*crypto/internal/fips140/sha256.Digest.Write", "*crypto/sha256.digest.Write", "crypto/sha256.Sum256", "crypto/sha1.Sum", "crypto/md5.Sum",
		"*bytes.Buffer.Write", "*bufio.Writer.Write", "*os.File.Write", "*net.TCPConn.Write", "*net.UnixConn.Write",
		// Byte-slice emitters that read the transported storage and hand back a
		// freshly allocated string/[]byte; the original slice is never retained.
		"*encoding/base64.Encoding.EncodeToString", "encoding/base64.StdEncoding.EncodeToString",
		"encoding/hex.EncodeToString", "encoding/hex.Dump",
		"regexp.Match", "*regexp.Regexp.Match", "*regexp.Regexp.Find", "*regexp.Regexp.FindAll", "*regexp.Regexp.FindIndex", "*regexp.Regexp.FindSubmatch", "*regexp.Regexp.ReplaceAll", "*regexp.Regexp.ReplaceAllFunc",
		"slices.IsSorted",
		// Structural value emitters — the marshalers walk the transported value
		// tree and allocate their own output. Element storage is read only.
		"encoding/json.Marshal", "encoding/json.MarshalIndent", "encoding/json/v2.Marshal",
		"encoding/xml.Marshal", "encoding/xml.MarshalIndent",
		// The structural decoders read the transported input bytes once and
		// build their own result; the input storage is neither retained nor
		// written. Their output travels through the pointer writeback.
		"encoding/json.Unmarshal", "encoding/json/v2.Unmarshal", "encoding/xml.Unmarshal":
		return true
	}
	return false
}

// nativeSliceMutatingIndex reports the argument that an in-place collection
// operation reorders or rewrites through the transported backing storage, or
// -1 when the callable does not mutate a slice argument. The dependency sorts
// its own decoded copy of the elements; the writeback protocol then copies the
// reordered elements back into the interpreter's original backing array so
// aliases observe the change, exactly as native Go would. The concrete
// sort.Ints/Strings/Float64s sorters cross to the dependency as reflected
// symbols; the generic slices.Sort is not a reflectable symbol, so it shares
// this mutation writeback but has its reordering computed interpreter-side by
// nativeSliceGenericHelper before the request would reach the dependency.
func nativeSliceMutatingIndex(name string) int {
	switch name {
	case "sort.Ints", "sort.Strings", "sort.Float64s", "slices.Sort",
		// slices.SortFunc/SortStableFunc reorder in place through the original
		// comparison callback; goSourceSlicesSortFunc computes the ordering
		// interpreter-side and rides this same writeback.
		"slices.SortFunc", "slices.SortStableFunc":
		return 0
	}
	return -1
}

func prepareNativeSliceBuffers(req bashPPEvalRequest, q *bashPPBridgeRequest) error {
	if q.Op != "call" {
		return nil
	}
	q.SliceBuffers = nil
	q.sliceTargets = nil
	hasSlice := false
	var refresh func(*bashPPBridgeValue) error
	refresh = func(v *bashPPBridgeValue) error {
		if capture := v.sliceView; capture != nil {
			hasSlice = true
			if req.CallbackOwner == nil {
				return fmt.Errorf("gosource: original slice has no request owner")
			}
			current, err := req.CallbackOwner.bashPPBridgeCollection(capture.view, capture.meta, capture.typ)
			if err != nil {
				return err
			}
			*v = current
		}
		for i := range v.Elements {
			if err := refresh(&v.Elements[i]); err != nil {
				return err
			}
		}
		for name, field := range v.Fields {
			if err := refresh(&field); err != nil {
				return err
			}
			v.Fields[name] = field
		}
		for i := range v.Entries {
			if err := refresh(&v.Entries[i].Key); err != nil {
				return err
			}
			if err := refresh(&v.Entries[i].Value); err != nil {
				return err
			}
		}
		return nil
	}
	for i := range q.Args {
		if err := refresh(&q.Args[i]); err != nil {
			return err
		}
	}
	if !hasSlice {
		return nil
	}
	// The refusal below guards a dependency that would RETAIN or MUTATE a
	// decoded copy of interpreter storage while an original callback runs. Two
	// kinds of callable are outside that hazard and are admitted with their
	// callbacks: one this Runner answers itself, which never sends the storage
	// anywhere, and a read-only emitter, which walks the transported tree once
	// and allocates its own output. The latter is the same footing fmt's
	// formatting entries already stand on, and they are in that set.
	if callable := nativeSliceCallable(req, *q); requestHasCallbacks(req, *q) &&
		!goSourceInterpretedCallable(callable) && !nativeSliceReadOnly(callable) {
		return fmt.Errorf("gosource: original callback with copied slice references is unsupported")
	}
	if nativeSliceCallable(req, *q) == "*text/template.Template.Execute" {
		// A configured template may call arbitrary registered functions. Permit
		// only inspected function-free trees over primitive string slices.
		if q.Receiver == nil || q.Selector != "Execute" || len(q.Args) != 2 || q.Args[1].Type != "[]string" || q.Args[1].Kind != "slice" {
			return fmt.Errorf("gosource: template slice transport requires direct primitive string data")
		}
		for _, element := range q.Args[1].Elements {
			if element.Kind != "string" || element.Type != "string" {
				return fmt.Errorf("gosource: template slice elements must be primitive strings")
			}
		}
		values, err := req.CallbackOwner.bashPPNativeRequest(req.CallbackOwner.ectx, req, bashPPBridgeRequest{Op: "template-readonly", Receiver: q.Receiver})
		if err != nil {
			return err
		}
		if len(values) != 1 || values[0].Kind != "bool" || values[0].Text != "true" {
			return fmt.Errorf("gosource: template with function or template callbacks cannot receive original slice storage")
		}
		return nil
	}
	name := nativeSliceCallable(req, *q)
	if mut := nativeSliceMutatingIndex(name); mut >= 0 {
		return prepareNativeSliceMutation(req, q, mut)
	}
	if nativePointerWritebackAllowed(req, *q) || nativeRetainedPointerMutator(name) {
		return nil
	}
	index := nativeSliceReadIndex(req, *q)
	if index < 0 {
		// An interpreter-computed helper that does not mutate reads the
		// transported elements and allocates its own answer.
		if nativeSliceReadOnly(name) || goSourceInterpretedCallable(name) {
			return nil
		}
		return fmt.Errorf("gosource: native slice retention or mutation is unsupported for %s", name)
	}
	if index >= len(q.Args) || q.Args[index].sliceView == nil {
		return fmt.Errorf("gosource: native Read requires a direct original byte slice")
	}
	target := q.Args[index].sliceView
	collection, ok := req.CallbackOwner.bashPPUnderlyingType(target.typ).(*syntax.BashPPCollectionType)
	if !ok || collection.Kind != "slice" {
		return fmt.Errorf("gosource: native Read buffer has no slice identity")
	}
	element := bashPPTypeText(req.CallbackOwner.bashPPUnderlyingType(collection.Element))
	if element != "byte" && element != "uint8" {
		return fmt.Errorf("gosource: native Read requires a byte slice")
	}
	full, err := req.CallbackOwner.bashPPBridgeCollection(target.view[:cap(target.view)], target.meta, target.typ)
	if err != nil {
		return err
	}
	// Capacity outside the visible length may still use the collection layer's
	// lazy nil slots. For byte storage those slots are Go's zero bytes.
	for i := range full.Elements {
		if full.Elements[i].Kind == "nil" {
			full.Elements[i] = bashPPBridgeValue{Kind: "uint", Type: "uint8", Text: "0"}
		}
	}
	q.SliceBuffers = []bashPPNativeSliceBuffer{{Index: index, Length: len(target.view), Value: full}}
	q.sliceTargets = []*bashPPNativeSlice{target}
	q.sliceMutating = []bool{false}
	q.sliceElem = []syntax.BashPPTypeExpr{collection.Element}
	return nil
}

// prepareNativeSliceMutation sets up the transport for a concrete in-place
// mutator (sort.Ints/Strings/Float64s). Only the visible length crosses the
// boundary; the dependency reorders its decoded copy and the writeback copies
// the reordered elements back into the interpreter's original backing array so
// aliasing slices observe the same reordering, as native Go does.
func prepareNativeSliceMutation(req bashPPEvalRequest, q *bashPPBridgeRequest, index int) error {
	if requestHasCallbacks(req, *q) && !goSourceInterpretedCallable(nativeSliceCallable(req, *q)) {
		return fmt.Errorf("gosource: %s cannot mutate a slice carrying original callbacks", nativeSliceCallable(req, *q))
	}
	if index >= len(q.Args) || q.Args[index].sliceView == nil {
		return fmt.Errorf("gosource: %s requires a direct original slice", nativeSliceCallable(req, *q))
	}
	target := q.Args[index].sliceView
	collection, ok := req.CallbackOwner.bashPPUnderlyingType(target.typ).(*syntax.BashPPCollectionType)
	if !ok || collection.Kind != "slice" {
		return fmt.Errorf("gosource: native mutation buffer has no slice identity")
	}
	visible, err := req.CallbackOwner.bashPPBridgeCollection(target.view, target.meta, target.typ)
	if err != nil {
		return err
	}
	if visible.Kind != "slice" {
		return fmt.Errorf("gosource: native mutation buffer is not a slice")
	}
	q.SliceBuffers = []bashPPNativeSliceBuffer{{Index: index, Length: len(target.view), Value: visible}}
	q.sliceTargets = []*bashPPNativeSlice{target}
	q.sliceMutating = []bool{true}
	q.sliceElem = []syntax.BashPPTypeExpr{collection.Element}
	return nil
}

func applyNativeSliceBuffers(runner *Runner, q bashPPBridgeRequest, reply bashPPBridgeResponse) error {
	if len(reply.SliceUpdates) == 0 && reply.Error != "" {
		return nil
	} // rejected before invoking the dependency
	if len(reply.SliceUpdates) != len(q.SliceBuffers) {
		return fmt.Errorf("gosource: native slice writeback count mismatch")
	}
	type sliceWriteback struct {
		values   []any
		metas    []*bashPPCollectionMeta
		length   int  // visible length written; the byte path writes cap
		mutating bool // reorder the visible length vs fill up to capacity
	}
	updates := make([]sliceWriteback, len(reply.SliceUpdates))
	for i, wire := range reply.SliceUpdates {
		target := q.sliceTargets[i]
		mutating := i < len(q.sliceMutating) && q.sliceMutating[i]
		want := cap(target.view)
		if mutating {
			want = len(target.view)
		}
		if wire.Index != q.SliceBuffers[i].Index || wire.Length != len(target.view) || wire.Value.Kind != "slice" || len(wire.Value.Elements) != want {
			return fmt.Errorf("gosource: invalid native slice writeback shape")
		}
		values := make([]any, len(wire.Value.Elements))
		metas := make([]*bashPPCollectionMeta, len(wire.Value.Elements))
		if mutating {
			// Rebuild the reordered elements at the slice's declared element
			// type. Structured metadata (per-element identity) rides along so a
			// mutated element remains a full interpreter value.
			for j, v := range wire.Value.Elements {
				value, meta, err := runner.bashPPBridgeContents(v, q.sliceElem[i])
				if err != nil {
					return fmt.Errorf("gosource: invalid native slice writeback: %w", err)
				}
				values[j], metas[j] = value, meta
			}
		} else {
			for j, v := range wire.Value.Elements {
				n, err := strconv.ParseUint(v.Text, 10, 8)
				if err != nil || (v.Kind != "uint" && v.Kind != "int") {
					return fmt.Errorf("gosource: invalid byte in native slice writeback")
				}
				values[j] = int(n)
			}
		}
		updates[i] = sliceWriteback{values: values, metas: metas, length: want, mutating: mutating}
	}
	for i, update := range updates {
		target := q.sliceTargets[i]
		copy(target.view[:update.length], update.values)
		if update.mutating && target.meta != nil && len(target.meta.sequence) >= update.length {
			for j := 0; j < update.length; j++ {
				target.meta.sequence[j] = update.metas[j]
			}
		}
	}
	return nil
}

// nativeSliceGenericHelper answers the two generic slices helpers the dependency
// worker cannot invoke: an uninstantiated generic function is not a reflectable
// imported symbol, so slices.Equal/slices.Sort never resolve there. Both operate
// over primitive element values that already crossed the collection transport, so
// the interpreter computes them directly. slices.Equal reads both transported
// slices and answers a bool (the read-only path); slices.Sort reorders the
// visible elements and rides the existing mutation writeback, so every aliasing
// header observes the reordering exactly as the concrete sort.Strings path does.
// handled is false for every other callable, leaving the normal dependency
// dispatch untouched.
func (r *Runner) nativeSliceGenericHelper(ctx context.Context, req bashPPEvalRequest, q *bashPPBridgeRequest) (values []bashPPBridgeValue, handled bool, err error) {
	if q.Op != "call" {
		return nil, false, nil
	}
	switch nativeSliceCallable(req, *q) {
	case "slices.Collect":
		values, err := r.goSourceSlicesCollect(ctx, req, q.Args)
		return values, true, err
	case "slices.Equal":
		if len(q.Args) != 2 {
			return nil, false, fmt.Errorf("gosource: slices.Equal requires two slices")
		}
		equal, err := nativeSlicePrimitiveEqual(q.Args[0], q.Args[1])
		if err != nil {
			return nil, false, err
		}
		return []bashPPBridgeValue{{Kind: "bool", Type: "bool", Text: strconv.FormatBool(equal)}}, true, nil
	case "maps.Equal":
		if len(q.Args) != 2 {
			return nil, true, fmt.Errorf("gosource: maps.Equal requires two maps")
		}
		equal, err := nativeMapPrimitiveEqual(q.Args[0], q.Args[1])
		if err != nil {
			return nil, true, err
		}
		return []bashPPBridgeValue{{Kind: "bool", Type: "bool", Text: strconv.FormatBool(equal)}}, true, nil
	case "slices.Sort":
		if len(q.SliceBuffers) != 1 {
			return nil, false, fmt.Errorf("gosource: slices.Sort requires a direct original slice")
		}
		sorted, err := nativeSliceSorted(q.SliceBuffers[0].Value.Elements)
		if err != nil {
			return nil, false, err
		}
		reply := bashPPBridgeResponse{SliceUpdates: []bashPPNativeSliceBuffer{{
			Index:  q.SliceBuffers[0].Index,
			Length: q.SliceBuffers[0].Length,
			Value:  bashPPBridgeValue{Kind: "slice", Elements: sorted},
		}}}
		if err := applyNativeSliceBuffers(r, *q, reply); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	case "slices.IsSorted":
		if len(q.Args) != 1 {
			return nil, false, fmt.Errorf("gosource: slices.IsSorted requires one slice")
		}
		elements, err := nativeSliceElements(q.Args[0])
		if err != nil {
			return nil, false, err
		}
		sorted, err := nativeSliceIsSorted(elements)
		if err != nil {
			return nil, false, err
		}
		return []bashPPBridgeValue{{Kind: "bool", Type: "bool", Text: strconv.FormatBool(sorted)}}, true, nil
	}
	// The generic callback-taking helpers share this interpreter-side dispatch:
	// slices.SortFunc and friends are uninstantiated generic functions too.
	return r.goSourceGenericCallbackHelper(ctx, req, q)
}

func nativeMapPrimitiveEqual(a, b bashPPBridgeValue) (bool, error) {
	if a.Kind == "nil" && b.Kind == "nil" {
		return true, nil
	}
	if a.Kind != "map" || b.Kind != "map" {
		return false, fmt.Errorf("gosource: maps.Equal requires map operands, got %s and %s", a.Kind, b.Kind)
	}
	if len(a.Entries) != len(b.Entries) {
		return false, nil
	}
	for _, left := range a.Entries {
		found := false
		for _, right := range b.Entries {
			keysEqual, err := nativeSliceScalarEqual(left.Key, right.Key)
			if err != nil {
				return false, err
			}
			if !keysEqual {
				continue
			}
			valuesEqual, err := nativeSliceScalarEqual(left.Value, right.Value)
			if err != nil {
				return false, err
			}
			if !valuesEqual {
				return false, nil
			}
			found = true
			break
		}
		if !found {
			return false, nil
		}
	}
	return true, nil
}

func (r *Runner) goSourceSlicesCollect(ctx context.Context, req bashPPEvalRequest, args []bashPPBridgeValue) ([]bashPPBridgeValue, error) {
	if len(args) != 1 || args[0].Kind != "callback" || args[0].Session != req.Bridge.id {
		return nil, fmt.Errorf("gosource: slices.Collect requires one current iterator callback")
	}
	req.Bridge.mu.Lock()
	iterator := req.Bridge.functions[args[0].Handle]
	req.Bridge.mu.Unlock()
	yieldType, err := r.goSourceIteratorYield(iterator)
	if err != nil {
		return nil, fmt.Errorf("gosource: slices.Collect: %w", err)
	}
	yieldParams := bashppParams(yieldType.Params)
	if len(yieldParams) != 1 {
		return nil, fmt.Errorf("gosource: slices.Collect iterator must yield one value")
	}
	var collected []bashPPBridgeValue
	yield := &bashPPFunc{
		collectYield: &collected,
		lit:          &syntax.BashPPFuncLit{Params: yieldType.Params, Results: yieldType.Results},
		scope:        r.bashPPScope,
	}
	vr := r.bashPPStoreFunc(yield)
	savedResults, savedCalls := r.bashPPResultCells, r.bashPPCallCells
	defer func() { r.bashPPResultCells, r.bashPPCallCells = savedResults, savedCalls }()
	r.bashPPCallCells = []*bashPPCell{{vr: vr, declType: yieldType}}
	failure := r.bashPPShortFailureSeq
	r.bashPPInvoke(ctx, iterator, []string{vr.Str})
	if r.bashPPPanicking() || r.exit.exiting || r.exit.fatalExit || r.exit.err != nil || r.bashPPShortFailureSeq != failure {
		return nil, errBashPPScalarInterrupted
	}
	return []bashPPBridgeValue{{Kind: "slice", Type: "[]" + bashPPTypeText(yieldParams[0].typ), Elements: collected}}, nil
}

// nativeSliceElements returns the element values of a transported slice, treating
// a nil slice as empty so slices.Equal(nil, []T{}) is true as native Go reports.
func nativeSliceElements(v bashPPBridgeValue) ([]bashPPBridgeValue, error) {
	switch v.Kind {
	case "slice":
		return v.Elements, nil
	case "nil":
		return nil, nil
	}
	return nil, fmt.Errorf("gosource: slices.Equal requires slice operands, got %s", v.Kind)
}

// nativeSlicePrimitiveEqual compares two transported slices element by element
// over the primitive element kinds. Non-primitive elements are rejected rather
// than silently mishandled, keeping the helper fail-closed.
func nativeSlicePrimitiveEqual(a, b bashPPBridgeValue) (bool, error) {
	left, err := nativeSliceElements(a)
	if err != nil {
		return false, err
	}
	right, err := nativeSliceElements(b)
	if err != nil {
		return false, err
	}
	if len(left) != len(right) {
		return false, nil
	}
	for i := range left {
		equal, err := nativeSliceScalarEqual(left[i], right[i])
		if err != nil {
			return false, err
		}
		if !equal {
			return false, nil
		}
	}
	return true, nil
}

// nativeSliceScalarEqual compares primitive elements with Go's == semantics.
// In particular, NaN never equals itself while signed zero values compare equal.
func nativeSliceScalarEqual(a, b bashPPBridgeValue) (bool, error) {
	if a.Kind != b.Kind {
		return false, fmt.Errorf("gosource: slices.Equal requires uniform element kinds, got %s and %s", a.Kind, b.Kind)
	}
	switch a.Kind {
	case "string":
		return a.Text == b.Text, nil
	case "bool":
		return a.Text == b.Text, nil
	case "int":
		ai, err := strconv.ParseInt(a.Text, 10, 64)
		if err != nil {
			return false, fmt.Errorf("gosource: invalid integer element %q", a.Text)
		}
		bi, err := strconv.ParseInt(b.Text, 10, 64)
		if err != nil {
			return false, fmt.Errorf("gosource: invalid integer element %q", b.Text)
		}
		return ai == bi, nil
	case "uint":
		au, err := strconv.ParseUint(a.Text, 10, 64)
		if err != nil {
			return false, fmt.Errorf("gosource: invalid unsigned element %q", a.Text)
		}
		bu, err := strconv.ParseUint(b.Text, 10, 64)
		if err != nil {
			return false, fmt.Errorf("gosource: invalid unsigned element %q", b.Text)
		}
		return au == bu, nil
	case "float":
		af, err := strconv.ParseFloat(a.Text, 64)
		if err != nil {
			return false, fmt.Errorf("gosource: invalid float element %q", a.Text)
		}
		bf, err := strconv.ParseFloat(b.Text, 64)
		if err != nil {
			return false, fmt.Errorf("gosource: invalid float element %q", b.Text)
		}
		return af == bf, nil
	}
	return false, fmt.Errorf("gosource: slices.Equal requires primitive elements, got %s", a.Kind)
}

// nativeSliceSorted returns the elements ordered like slices.Sort over an ordered
// primitive element type. The input order is preserved among equal elements; that
// choice is unobservable by value, which is all the mutation writeback restores.
func nativeSliceSorted(elems []bashPPBridgeValue) ([]bashPPBridgeValue, error) {
	out := append([]bashPPBridgeValue(nil), elems...)
	var sortErr error
	sort.SliceStable(out, func(i, j int) bool {
		if sortErr != nil {
			return false
		}
		less, err := nativeSliceElementLess(out[i], out[j])
		if err != nil {
			sortErr = err
			return false
		}
		return less
	})
	if sortErr != nil {
		return nil, sortErr
	}
	return out, nil
}

func nativeSliceIsSorted(elems []bashPPBridgeValue) (bool, error) {
	for i := 1; i < len(elems); i++ {
		less, err := nativeSliceElementLess(elems[i], elems[i-1])
		if err != nil {
			return false, err
		}
		if less {
			return false, nil
		}
	}
	return true, nil
}

// nativeSliceElementLess orders two primitive elements as cmp.Ordered would:
// strings lexically and numbers by value. bool is not ordered and is rejected,
// as slices.Sort's cmp.Ordered constraint requires.
func nativeSliceElementLess(a, b bashPPBridgeValue) (bool, error) {
	if a.Kind != b.Kind {
		return false, fmt.Errorf("gosource: slices.Sort requires uniform element kinds, got %s and %s", a.Kind, b.Kind)
	}
	switch a.Kind {
	case "string":
		return a.Text < b.Text, nil
	case "int":
		ai, err := strconv.ParseInt(a.Text, 10, 64)
		if err != nil {
			return false, fmt.Errorf("gosource: invalid integer element %q", a.Text)
		}
		bi, err := strconv.ParseInt(b.Text, 10, 64)
		if err != nil {
			return false, fmt.Errorf("gosource: invalid integer element %q", b.Text)
		}
		return ai < bi, nil
	case "uint":
		au, err := strconv.ParseUint(a.Text, 10, 64)
		if err != nil {
			return false, fmt.Errorf("gosource: invalid unsigned element %q", a.Text)
		}
		bu, err := strconv.ParseUint(b.Text, 10, 64)
		if err != nil {
			return false, fmt.Errorf("gosource: invalid unsigned element %q", b.Text)
		}
		return au < bu, nil
	case "float":
		af, err := strconv.ParseFloat(a.Text, 64)
		if err != nil {
			return false, fmt.Errorf("gosource: invalid float element %q", a.Text)
		}
		bf, err := strconv.ParseFloat(b.Text, 64)
		if err != nil {
			return false, fmt.Errorf("gosource: invalid float element %q", b.Text)
		}
		return cmp.Less(af, bf), nil
	}
	return false, fmt.Errorf("gosource: slices.Sort requires ordered primitive elements, got %s", a.Kind)
}
