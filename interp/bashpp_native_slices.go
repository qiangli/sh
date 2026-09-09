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
		"strings.Join", "os.WriteFile", "crypto/sha256.Sum256", "crypto/sha1.Sum", "crypto/md5.Sum",
		"*bytes.Buffer.Write", "*bufio.Writer.Write", "*os.File.Write", "*net.TCPConn.Write", "*net.UnixConn.Write":
		return true
	}
	return false
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
	index := nativeSliceReadIndex(req, *q)
	if index < 0 {
		if nativeSliceReadOnly(nativeSliceCallable(req, *q)) {
			return nil
		}
		return fmt.Errorf("gosource: native slice retention or mutation is unsupported for %s", nativeSliceCallable(req, *q))
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
	return nil
}

func applyNativeSliceBuffers(q bashPPBridgeRequest, reply bashPPBridgeResponse) error {
	if len(reply.SliceUpdates) == 0 && reply.Error != "" {
		return nil
	} // rejected before invoking the dependency
	if len(reply.SliceUpdates) != len(q.SliceBuffers) {
		return fmt.Errorf("gosource: native slice writeback count mismatch")
	}
	updates := make([][]any, len(reply.SliceUpdates))
	for i, wire := range reply.SliceUpdates {
		target := q.sliceTargets[i]
		if wire.Index != q.SliceBuffers[i].Index || wire.Length != len(target.view) || wire.Value.Kind != "slice" || len(wire.Value.Elements) != cap(target.view) {
			return fmt.Errorf("gosource: invalid native slice writeback shape")
		}
		values := make([]any, len(wire.Value.Elements))
		for j, v := range wire.Value.Elements {
			n, err := strconv.ParseUint(v.Text, 10, 8)
			if err != nil || (v.Kind != "uint" && v.Kind != "int") {
				return fmt.Errorf("gosource: invalid byte in native slice writeback")
			}
			values[j] = int(n)
		}
		updates[i] = values
	}
	for i, values := range updates {
		copy(q.sliceTargets[i].view[:cap(q.sliceTargets[i].view)], values)
	}
	return nil
}
