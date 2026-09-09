package interp

// Sprint: #118; Story: #54; Story-ID: c3a60493cde9
import (
	"fmt"
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
		"strings.Join", "os.WriteFile", "syscall.Exec", "*flag.FlagSet.Parse",
		"*crypto/internal/fips140/sha256.Digest.Write", "*crypto/sha256.digest.Write", "crypto/sha256.Sum256", "crypto/sha1.Sum", "crypto/md5.Sum",
		"*bytes.Buffer.Write", "*bufio.Writer.Write", "*os.File.Write", "*net.TCPConn.Write", "*net.UnixConn.Write",
		// Byte-slice emitters that read the transported storage and hand back a
		// freshly allocated string/[]byte; the original slice is never retained.
		"*encoding/base64.Encoding.EncodeToString", "encoding/base64.StdEncoding.EncodeToString",
		"encoding/hex.EncodeToString", "encoding/hex.Dump",
		"regexp.Match", "*regexp.Regexp.Match", "*regexp.Regexp.Find", "*regexp.Regexp.FindAll", "*regexp.Regexp.FindIndex", "*regexp.Regexp.FindSubmatch", "*regexp.Regexp.ReplaceAll",
		// Structural value emitters — the marshalers walk the transported value
		// tree and allocate their own output. Element storage is read only.
		"encoding/json.Marshal", "encoding/json.MarshalIndent", "encoding/json/v2.Marshal",
		"encoding/xml.Marshal", "encoding/xml.MarshalIndent":
		return true
	}
	return false
}

// nativeSliceMutatingIndex reports the argument that an in-place collection
// operation reorders or rewrites through the transported backing storage, or
// -1 when the callable does not mutate a slice argument. The dependency sorts
// its own decoded copy of the elements; the writeback protocol then copies the
// reordered elements back into the interpreter's original backing array so
// aliases observe the change, exactly as native Go would. Only concrete
// (non-generic) mutators appear here: a generic helper such as slices.Sort
// cannot be reflected as an imported symbol and is resolved elsewhere.
func nativeSliceMutatingIndex(name string) int {
	switch name {
	case "sort.Ints", "sort.Strings", "sort.Float64s":
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
	if requestHasCallbacks(req, *q) {
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
	index := nativeSliceReadIndex(req, *q)
	if index < 0 {
		if nativeSliceReadOnly(name) {
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
	if requestHasCallbacks(req, *q) {
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
