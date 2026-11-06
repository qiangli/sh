# S562 B3 batch 3 — Go module-internal functions and Python handle proposal

Story #3040 / 6a4f4591bc8e.

## Go delivery

Keep unsupported exported Go functions in the original compiled module, but
separate their diagnostic records from Plan.Exports into
Plan.UnavailableExports. Record generic type parameters, variadic parameters,
unsupported parameter/result types, and multiple value results. Go compilation
still checks the whole module; malformed declarations, duplicate exports,
reserved names, methods, and non-import package declarations retain their
existing rejection.

The interpreter resolves a known unavailable name to its explicit bridge
diagnostic before argument/result checking. Lowering rejects the same call
before emitting wrappers. The Go worker also rejects direct protocol calls by
name and reason. No callable exports is valid, including a module containing
only private functions. Lowering requests output imports only when adapters
need them.

Regression coverage: Go by Example's SlicesIndex signature called from Main,
variadic and unsupported parameter/result functions, multiple value results,
direct and qualified Bash# calls, interpreted/native successful execution,
zero-callable modules, malformed modules and duplicate unavailable names.
No Python implementation changes are part of this delivery.

## Python: existing machinery

* polyglot/polyglot.go already stores opaque Handle records: module ownership,
  generation, ID, Type, Repr, Callable. The Python worker retains actual objects
  in its handles table and returns structured handle records. GetAttr, Call,
  CallAttr, Release, stale-generation checks and recursive value encoding exist.
* interp/bashpp_func.go already recognizes an expand.Object carrying a
  *polyglot.Handle and dispatches selectors through CallAttr. Otherwise a local
  value goes through the Go-style typed selector graph, which reports
  BASHPP-ESELECTOR-TYPE when the local has no type.
* interp/bashpp_polyglot.go preserves a returned handle for direct Python
  imports. A dynamic fence export instead returns text plus error; its result
  does not get an object cell. The typed error-opt-in path preserves only
  explicitly object/handle-typed results. Thus the reported make/read example
  loses the carrier before reaching the existing handle selector dispatch.
* lower/polyglot.go has pythonValues, pythonCallKind, handle access helpers and
  CallAttr generation. findPythonValues only considers single-LHS declarations
  and direct-import/previously marked Python values. It does not propagate a
  handle-capable result from a dynamic fence's two-result binding.
* polyglot/iterator.go and the Python worker support iter_open/iter_next/
  iter_close, EOF, cleanup and generation checks. Signature-based streaming
  exports feed interp/bashpp_foreign_iterator.go and lower/foreign_iterator.go.
  This is distinct from an arbitrary returned object implementing __next__.
* Generic host callback tables, reverse requests and bounded re-entry exist in
  polyglot/polyglot.go. Python's $callback decoder currently constructs
  PipelineInput, a special stdin stream reader, not a general callable proxy.
  The Python language row does not enable the general callback binding used
  by Rust. Method support alone cannot enable callbacks.

Local probe (existing code, no Python fix applied):

~~~~text
~~~python as py
import io
def make():
    return io.StringIO("read")
~~~
h, err := py.make()
v, err := h.read()
~~~~

The interpreter reports `BASHPP-ESELECTOR-TYPE: local h has no typed selector
path`, followed by an assignment mismatch. The handle's runtime Type is
descriptive metadata, not a Bash# structured type. The probe was temporary;
this proposal does not add an expected-failure test to the gate.

## Smallest proposed change — pending manager review

1. Preserve a runtime *polyglot.Handle in the value result cell of a dynamic
   Python fence call, including the error binding's position. Do not infer
   authority from Type/Repr or turn every string into a handle.
2. Reuse existing handle selector dispatch. Add an explicit two-result
   value/error form for handle method calls (direct imports currently expose
   one value); keep existing one-value behavior. Classify Python exceptions as
   domain errors while retaining transport/cancellation failures as failures.
   Preserve handles returned by methods for chained calls.
3. Extend lower's value provenance to mark the first result of dynamic Python
   fence calls and method calls, including two-LHS bindings and assignments.
   The mark means “may carry a Python value”, not “is always an object”.
   At runtime check *polyglot.Handle before CallAttr. Emit shared helpers when
   fences need them, not just when direct imports exist.
4. Preserve typed local selector precedence, module ownership, generations,
   release behavior and output ordering. Do not introduce method discovery,
   reflection schemas or a new worker protocol for ordinary method calls.

Expected files: interp/bashpp_polyglot.go (result cells), interp/bashpp_func.go
(method result/error contract), lower/polyglot.go (provenance/helpers), and
possibly lower/callables_projection.go and lower/callables_result_presence.go
(result projection/arity). Tests in interp/bashpp_polyglot_test.go,
lower/polyglot_test.go / polyglot_errors_test.go, and polyglot/polyglot_test.go
for ownership/lifetime controls if existing coverage is insufficient.

Estimate: 150–250 production lines and 200–350 test lines, about 12–18
cases shared across interpreted/lowered execution. Cases: make/read,
method-returned handle, scalar method result, exception opt-in, one-value
compatibility, two-result reassignment, missing/noncallable attribute, stale
handle, cross-module handle, stdout/stderr, direct-import compatibility and
ordinary typed-selector non-regression. This is an estimate, not a commitment
to a line quota.

Risks: result-cell/text divergence, incorrect error arity, loss of provenance
through assignments or containers, local-name shadowing, non-object dynamic
returns, worker lifetime, and helper emission in fence-only compiled units.

## Iterators and callbacks are separate decisions

Calling h.__next__() follows mechanically from handle methods, but normal EOF
would remain StopIteration, a Python exception. A Bash# next(h) operation with
an explicit done flag or range/stream consumption does not follow. Prefer
adapting an authenticated handle into the existing Iterator lifecycle with a
small worker operation to open an iterator from that handle. Decide EOF shape
and early-break cleanup first. Rough additional cost: 100–180 production and
150–250 test lines across polyglot/iterator.go, the Python worker, interpreter
and lower iterator adapters; 8–12 cases.

General callbacks require a distinct callable proxy in Python, separate from
PipelineInput, plus Python callback registration/argument conversion in both
engines. Decide call-scoped versus retained callbacks, nested calls, exceptions,
cancellation and callback-returned handles before enabling them. Existing host
machinery can be reused, but this is not implied by method calls. Rough
additional cost: 250–450 production and 300–500 test lines, 15–25 cases across
polyglot/polyglot.go, polyglot/languages.go, interpreter/lower callback adapters.
Retained callbacks would need a further explicit lifetime design.

## Validation

* Behavioral red confirmed against the original Go analyzer: the interpreter
  rejects SlicesIndex, Variadic and Unsupported; lowering rejects all five
  regression shapes before Main can run. The original analyzer was restored
  through an overlay for the interpreter red check.
* Green: TestBashPPGoModuleInternalFunctions; TestGoModuleInternalDeclarations;
  TestGoFenceModuleInternalFunctions, including bare/qualified statement and
  assignment diagnostics, native/interpreted execution and zero exports.
* Requested selections: go test ./polyglot/... PASS (19.556s);
  go test -tags full ./interp -run 'BashPP|Foreign|Polyglot|Fence|Island'
  -count=1 PASS on retry (266.274s);
  go test -tags full ./lower -run 'Polyglot|Fence|Go' -count=1 PASS (296.867s).
* First interpreter selection failed in
  TestSprint162BashPPFIFOAfterTask/procsub-external-control: expected output
  arrived, but its five-second context expired. The isolated case then passed
  (0.489s package run), followed by the full successful selection above.
  No FIFO implementation, test or timeout was changed.
* Focused syntax fence selection PASS; git diff --check clean.
