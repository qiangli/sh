package interp

// Sprint: #118; Story: #64; Story-ID: 45be321bddfb
//
// The general callback/signature/copied-slice bridge. Three limits used to be
// entangled here: a callback signature had to be scalar, a request carrying an
// original callback could not also carry original slice storage, and the
// generic ordering helpers had no reflectable dependency symbol at all.
//
// The rules implemented below are general, not per-callable:
//
//   - A callback parameter or result whose type has pure value semantics — a
//     scalar, or a struct/array built only from such types — is copied by Go
//     itself at the call boundary, so a copied transport observes exactly what
//     native Go observes. Reference-bearing shapes (slice, map, pointer, chan,
//     func, interface) stay refused, because a copy of those would silently
//     drop aliasing.
//
//   - The copied-slice refusal exists because a dependency holding a decoded
//     copy of interpreter storage may observe or lose writes an original
//     callback makes while the call is in flight. A callable computed entirely
//     interpreter-side never hands that storage to the dependency, so the
//     refusal does not apply to it; its callback and its slice are the same
//     Runner's own state, in one goroutine.
//
//   - slices.SortFunc/SortStableFunc/IndexFunc/ContainsFunc and cmp.Compare/Less
//     are uninstantiated generic functions, which are not reflectable imported
//     symbols. They are answered interpreter-side over the values that already
//     crossed the collection transport, exactly as slices.Sort/Equal are.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// goSourceInterpretedCallable reports a callable this Runner answers itself,
// without ever sending interpreter-owned storage to the dependency process.
// Only these are exempt from the copied-slice refusal.
func goSourceInterpretedCallable(name string) bool {
	switch name {
	case "slices.Sort", "slices.Equal", "slices.Collect",
		"slices.SortFunc", "slices.SortStableFunc", "slices.IndexFunc", "slices.ContainsFunc",
		"cmp.Compare", "cmp.Less":
		return true
	}
	return false
}

// bashPPCallbackValueType reports whether a callback parameter or result type
// has pure Go value semantics, so a transported copy is indistinguishable from
// the copy Go makes at the call itself.
func (r *Runner) bashPPCallbackValueType(typ syntax.BashPPTypeExpr) bool {
	return r.callbackValueType(typ, 0)
}

func (r *Runner) callbackValueType(typ syntax.BashPPTypeExpr, depth int) bool {
	if typ == nil || depth > 8 {
		return false
	}
	if r.bashPPCallbackScalarType(typ) {
		return true
	}
	switch shape := r.bashPPUnderlyingType(typ).(type) {
	case *syntax.BashPPStructType:
		for _, field := range bashPPFlatFields(shape.Fields) {
			if !r.callbackValueType(field.typ, depth+1) {
				return false
			}
		}
		return len(shape.Fields) > 0
	case *syntax.BashPPCollectionType:
		if shape.Kind == "array" {
			return r.callbackValueType(shape.Element, depth+1)
		}
	}
	return false
}

// bashPPCallbackNativeType reports a callback parameter or result type that
// names an imported dependency type. Such a value never leaves the dependency:
// it arrives as the session handle it already is, so nothing is copied and no
// aliasing can be lost. This is what lets an original http.HandlerFunc declare
// its real (http.ResponseWriter, *http.Request) signature.
func (r *Runner) bashPPCallbackNativeType(typ syntax.BashPPTypeExpr) bool {
	if typ == nil {
		return false
	}
	text := strings.TrimPrefix(bashPPTypeText(typ), "*")
	alias, name, ok := strings.Cut(text, ".")
	if !ok || name == "" || strings.Contains(name, ".") {
		return false
	}
	return r.bashPPImports[alias] != ""
}

// goSourceCallbackCell binds one transported value to a callback parameter at
// its declared type. A scalar keeps the direct text form the positional call
// protocol uses; a value-semantics aggregate is rebuilt through the ordinary
// bridge contents path, so the body reads the fields it declared.
func (r *Runner) goSourceCallbackCell(v bashPPBridgeValue, param bashPPParam) (*bashPPCell, string, error) {
	if scalar, err := v.scalar(); err == nil {
		// A constant.Value keeps a float exact as a rational, so 3.5 renders
		// as "7/2" — text no float parameter accepts. The transported decimal
		// is what the parameter's own type reads.
		text := bashPPScalarString(scalar.value)
		if v.Kind == "float" && v.Text != "" {
			text = v.Text
		}
		cell := goSourceNativeValueCell(v)
		cell.vr.Str = text
		cell.typeName, cell.declType = param.declared, param.typ
		return cell, text, nil
	}
	if r.bashPPCallbackNativeType(param.typ) {
		cell := goSourceNativeValueCell(v)
		cell.typeName, cell.declType = bashPPTypeText(param.typ), param.typ
		return cell, "", nil
	}
	if !r.bashPPCallbackValueType(param.typ) {
		return nil, "", fmt.Errorf("gosource: callback parameter of type %s requires shared-reference transport", bashPPTypeText(param.typ))
	}
	value, meta, err := r.bashPPBridgeContents(v, param.typ)
	if err != nil {
		return nil, "", err
	}
	cell := &bashPPCell{declType: param.typ, typeName: bashPPTypeText(param.typ)}
	bashPPStoreCellValue(cell, value, meta)
	return cell, "", nil
}

// goSourceCallInterpretedCallback invokes an original callback from an
// interpreter-computed helper. Unlike the dependency callback path it does not
// absorb a panic or a failed exit into a transported reply: there is no
// dependency frame between the caller and the callback, so an interrupted
// callback unwinds the original program the way native Go would.
func (r *Runner) goSourceCallInterpretedCallback(ctx context.Context, fn *bashPPFunc, args []bashPPBridgeValue) ([]*bashPPCell, error) {
	params := bashppParams(fn.params())
	if len(args) != len(params) {
		return nil, fmt.Errorf("gosource: original callback wants %d argument(s), got %d", len(params), len(args))
	}
	texts := make([]string, len(args))
	cells := make([]*bashPPCell, len(args))
	for i, arg := range args {
		cell, text, err := r.goSourceCallbackCell(arg, params[i])
		if err != nil {
			return nil, err
		}
		cells[i], texts[i] = cell, text
	}
	savedResults, savedCalls := r.bashPPResultCells, r.bashPPCallCells
	savedChannels, savedInterfaces := r.bashPPCallChannels, r.bashPPCallInterfaces
	defer func() {
		r.bashPPResultCells, r.bashPPCallCells = savedResults, savedCalls
		r.bashPPCallChannels, r.bashPPCallInterfaces = savedChannels, savedInterfaces
	}()
	r.bashPPCallChannels, r.bashPPCallInterfaces = nil, nil
	r.bashPPCallCells = cells
	failure := r.bashPPShortFailureSeq
	r.bashPPInvoke(ctx, fn, texts)
	if r.bashPPPanicking() || r.exit.exiting || r.exit.fatalExit || r.exit.err != nil || r.bashPPShortFailureSeq != failure {
		return nil, errBashPPScalarInterrupted
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	results := append([]*bashPPCell(nil), r.bashPPResultCells...)
	if len(results) != bashppResultCount(fn.results()) {
		return nil, fmt.Errorf("gosource: original callback result count mismatch")
	}
	return results, nil
}

// goSourceCallbackFunc resolves one transported callback argument back to the
// interpreted function it names, failing closed on a foreign session.
func goSourceCallbackFunc(req bashPPEvalRequest, v bashPPBridgeValue) (*bashPPFunc, error) {
	if v.Kind != "callback" || req.Bridge == nil || v.Session != req.Bridge.id {
		return nil, fmt.Errorf("gosource: a current original function callback is required")
	}
	req.Bridge.mu.Lock()
	fn := req.Bridge.functions[v.Handle]
	req.Bridge.mu.Unlock()
	if fn == nil {
		return nil, fmt.Errorf("gosource: original callback handle expired")
	}
	return fn, nil
}

// goSourceCallbackInt runs a comparison callback and reads its single int
// result, the shape cmp-style ordering functions require.
func (r *Runner) goSourceCallbackInt(ctx context.Context, fn *bashPPFunc, args []bashPPBridgeValue) (int, error) {
	results, err := r.goSourceCallInterpretedCallback(ctx, fn, args)
	if err != nil {
		return 0, err
	}
	if len(results) != 1 {
		return 0, fmt.Errorf("gosource: comparison callback must return one value")
	}
	value, err := r.bashPPBridgeCell(results[0])
	if err != nil {
		return 0, err
	}
	scalar, err := value.scalar()
	if err != nil {
		return 0, fmt.Errorf("gosource: comparison callback result: %w", err)
	}
	n, err := strconv.ParseInt(bashPPScalarString(scalar.value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("gosource: comparison callback returned %q, want an integer", bashPPScalarString(scalar.value))
	}
	return int(n), nil
}

// goSourceCallbackBool runs a predicate callback and reads its single bool
// result.
func (r *Runner) goSourceCallbackBool(ctx context.Context, fn *bashPPFunc, args []bashPPBridgeValue) (bool, error) {
	results, err := r.goSourceCallInterpretedCallback(ctx, fn, args)
	if err != nil {
		return false, err
	}
	if len(results) != 1 {
		return false, fmt.Errorf("gosource: predicate callback must return one value")
	}
	value, err := r.bashPPBridgeCell(results[0])
	if err != nil {
		return false, err
	}
	if value.Kind != "bool" {
		return false, fmt.Errorf("gosource: predicate callback must return bool, got %s", value.Kind)
	}
	return value.Text == "true", nil
}

// goSourceGenericCallbackHelper answers the generic callback-taking helpers
// interpreter-side. handled is false for every other callable, leaving ordinary
// dependency dispatch untouched.
func (r *Runner) goSourceGenericCallbackHelper(ctx context.Context, req bashPPEvalRequest, q *bashPPBridgeRequest) (values []bashPPBridgeValue, handled bool, err error) {
	name := nativeSliceCallable(req, *q)
	switch name {
	case "cmp.Compare", "cmp.Less":
		if len(q.Args) != 2 {
			return nil, true, fmt.Errorf("gosource: %s requires two operands", name)
		}
		n, err := goSourceOrderedCompare(q.Args[0], q.Args[1])
		if err != nil {
			return nil, true, err
		}
		if name == "cmp.Less" {
			return []bashPPBridgeValue{{Kind: "bool", Type: "bool", Text: strconv.FormatBool(n < 0)}}, true, nil
		}
		return []bashPPBridgeValue{{Kind: "int", Type: "int", Text: strconv.Itoa(n)}}, true, nil
	case "slices.SortFunc", "slices.SortStableFunc":
		return nil, true, r.goSourceSlicesSortFunc(ctx, req, q)
	case "slices.IndexFunc", "slices.ContainsFunc":
		return r.goSourceSlicesSearchFunc(ctx, req, q, name == "slices.ContainsFunc")
	}
	return nil, false, nil
}

// goSourceSlicesSortFunc reorders the visible elements with the original
// comparison callback and rides the existing mutation writeback, so every
// aliasing header observes the reordering as native Go's in-place sort does.
// Elements the callback reports equal keep their input order. slices.SortFunc
// leaves that order unspecified, so retaining it is one of the orders Go
// permits, and it is the one slices.SortStableFunc requires.
func (r *Runner) goSourceSlicesSortFunc(ctx context.Context, req bashPPEvalRequest, q *bashPPBridgeRequest) error {
	if len(q.SliceBuffers) != 1 {
		return fmt.Errorf("gosource: %s requires a direct original slice", nativeSliceCallable(req, *q))
	}
	if len(q.Args) != 2 {
		return fmt.Errorf("gosource: %s requires a slice and a comparison function", nativeSliceCallable(req, *q))
	}
	fn, err := goSourceCallbackFunc(req, q.Args[1])
	if err != nil {
		return err
	}
	sorted := append([]bashPPBridgeValue(nil), q.SliceBuffers[0].Value.Elements...)
	var callbackErr error
	sort.SliceStable(sorted, func(i, j int) bool {
		if callbackErr != nil {
			return false
		}
		n, err := r.goSourceCallbackInt(ctx, fn, []bashPPBridgeValue{sorted[i], sorted[j]})
		if err != nil {
			callbackErr = err
			return false
		}
		return n < 0
	})
	if callbackErr != nil {
		return callbackErr
	}
	reply := bashPPBridgeResponse{SliceUpdates: []bashPPNativeSliceBuffer{{
		Index:  q.SliceBuffers[0].Index,
		Length: q.SliceBuffers[0].Length,
		Value:  bashPPBridgeValue{Kind: "slice", Elements: sorted},
	}}}
	return applyNativeSliceBuffers(r, *q, reply)
}

// goSourceSlicesSearchFunc answers slices.IndexFunc/ContainsFunc. Both only
// read the transported elements, so no writeback is involved.
func (r *Runner) goSourceSlicesSearchFunc(ctx context.Context, req bashPPEvalRequest, q *bashPPBridgeRequest, contains bool) ([]bashPPBridgeValue, bool, error) {
	if len(q.Args) != 2 {
		return nil, true, fmt.Errorf("gosource: %s requires a slice and a predicate", nativeSliceCallable(req, *q))
	}
	fn, err := goSourceCallbackFunc(req, q.Args[1])
	if err != nil {
		return nil, true, err
	}
	elements, err := goSourceSequenceElements(q.Args[0])
	if err != nil {
		return nil, true, err
	}
	index := -1
	for i, element := range elements {
		match, err := r.goSourceCallbackBool(ctx, fn, []bashPPBridgeValue{element})
		if err != nil {
			return nil, true, err
		}
		if match {
			index = i
			break
		}
	}
	if contains {
		return []bashPPBridgeValue{{Kind: "bool", Type: "bool", Text: strconv.FormatBool(index >= 0)}}, true, nil
	}
	return []bashPPBridgeValue{{Kind: "int", Type: "int", Text: strconv.Itoa(index)}}, true, nil
}

// goSourceSequenceElements reads the transported elements of a slice or array,
// treating a nil slice as empty exactly as ranging over one does.
func goSourceSequenceElements(v bashPPBridgeValue) ([]bashPPBridgeValue, error) {
	switch v.Kind {
	case "slice", "array":
		return v.Elements, nil
	case "nil":
		return nil, nil
	}
	return nil, fmt.Errorf("gosource: a slice operand is required, got %s", v.Kind)
}

// goSourceOrderedCompare orders two transported operands as cmp.Compare does,
// including its NaN rule: a NaN sorts below every non-NaN value and equals
// itself. bool is not ordered and is rejected, as cmp.Ordered requires.
func goSourceOrderedCompare(a, b bashPPBridgeValue) (int, error) {
	if a.Kind != b.Kind && !(goSourceNumericKind(a.Kind) && goSourceNumericKind(b.Kind)) {
		return 0, fmt.Errorf("gosource: cmp requires operands of one ordered type, got %s and %s", a.Kind, b.Kind)
	}
	if a.Kind == "string" {
		return strings.Compare(a.Text, b.Text), nil
	}
	if !goSourceNumericKind(a.Kind) {
		return 0, fmt.Errorf("gosource: cmp requires ordered operands, got %s", a.Kind)
	}
	left, err := strconv.ParseFloat(a.Text, 64)
	if err != nil {
		return 0, fmt.Errorf("gosource: invalid ordered operand %q", a.Text)
	}
	right, err := strconv.ParseFloat(b.Text, 64)
	if err != nil {
		return 0, fmt.Errorf("gosource: invalid ordered operand %q", b.Text)
	}
	leftNaN, rightNaN := math.IsNaN(left), math.IsNaN(right)
	switch {
	case leftNaN && rightNaN:
		return 0, nil
	case leftNaN:
		return -1, nil
	case rightNaN:
		return 1, nil
	case left < right:
		return -1, nil
	case left > right:
		return 1, nil
	}
	return 0, nil
}

func goSourceNumericKind(kind string) bool {
	switch kind {
	case "int", "uint", "float":
		return true
	}
	return false
}

// goSourceReferenceDigest renders the storage a copied receiver shares with its
// caller: the contents of every slice and map reachable from it. A Go value
// receiver may freely reassign its own fields, including replacing a slice
// header, because the caller never sees that copy. What the caller does see is
// a write THROUGH one of those references, and that is what this digest
// changes. Comparing it before and after an original value-receiver callback
// turns a silently dropped effect into a reported one. Replacing a header is
// reported too: it is indistinguishable here, and refusing loudly is the side
// to err on.
func goSourceReferenceDigest(v bashPPBridgeValue) string {
	var out strings.Builder
	var walk func(bashPPBridgeValue)
	walk = func(v bashPPBridgeValue) {
		switch v.Kind {
		case "slice", "map":
			data, err := json.Marshal(v)
			if err != nil {
				out.WriteString("?")
				return
			}
			out.WriteString(v.Kind)
			out.Write(data)
			return
		}
		for _, element := range v.Elements {
			walk(element)
		}
		names := make([]string, 0, len(v.Fields))
		for name := range v.Fields {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			out.WriteString(name)
			walk(v.Fields[name])
		}
		for _, entry := range v.Entries {
			walk(entry.Value)
		}
	}
	walk(v)
	return out.String()
}

// goSourceCopiedReceiverDigest reads the shared-storage digest of a receiver
// the interpreter rebuilt from a transported copy. An unreadable receiver
// yields no digest and is simply not compared, which cannot mask a mutation:
// the same read fails identically before and after the body.
func (r *Runner) goSourceCopiedReceiverDigest(cell *bashPPCell) string {
	if cell == nil {
		return ""
	}
	value, err := r.bashPPBridgeCell(cell)
	if err != nil {
		return ""
	}
	return goSourceReferenceDigest(value)
}
