# Unsafe struct overlay design

## Decision

Do not implement the `(*S2)(unsafe.Pointer(&x)).padding = 88` write as a
local selector-assignment fix. Reusing the cross-width slice encoder and
decoder is possible only after adding persistent byte storage to interpreted
allocations. That change crosses the value-copy, pointer-alias, task-clone,
and concurrent-access boundaries, so it is neither contained nor sound as a
fixture-sized change.

The exact standalone differential test was run before making this decision.
Native Go printed:

```
1 3 true
2 4 true
88
99
```

The interpreter stopped at the first write with:

```
BASHPP-ESELECTOR-ASSIGN: target is not a structured value
```

The reproducer used two values of these types and read the padding fields back
after the writes, so merely ignoring a write to source padding cannot satisfy
it:

```go
type S struct {
	A int8
	B int16
}

type S2 struct {
	A       int8
	padding int8
	B       int16
}

x := S{A: 1, B: 3}
(*S2)(unsafe.Pointer(&x)).padding = 88
fmt.Println(x.A, x.B, x == S{A: 1, B: 3})
fmt.Println((*S2)(unsafe.Pointer(&x)).padding)
```

## Why the existing byte view is insufficient

`goSourceUnsafeByteLayout` intentionally rejects a struct with internal or
trailing padding. Interpreter struct storage is a `map[string]any` containing
declared fields only, so `goSourceUnsafeEncode` has no byte to encode at the
padding offset and `goSourceUnsafeDecode` has nowhere to retain a byte decoded
there.

The cross-width slice view avoids this problem by accepting only layouts in
which every byte belongs to an observable value. It then moves ownership of
the complete source slots into a new backing and marks the old slots as moved.
A struct overlay cannot use that ownership transfer: ordinary reads of `x.A`,
`x.B`, and `x == S{...}` must remain valid while repeated `*S2` views observe
the padding writes.

A transient decoded `S2` map is also insufficient. Selector assignment would
mutate only that map. The next conversion constructs another pointer value,
so the byte `88` must belong to the allocation named by `&x`, not to one
conversion expression or pointer wrapper.

## Required general mechanism

1. Give every interpreter-owned addressable allocation a canonical raw-byte
   companion. It must retain padding and pointer-word side data and identify
   nested addressable subobjects by stable offsets.
2. Extend unsafe layouts to calculate aligned field offsets and total struct
   size, including internal and trailing padding. Use the target Go
   architecture's `go/types.Sizes`, not host assumptions scattered through
   selector code.
3. On the first unsafe view, encode the typed value into that allocation image
   without overwriting retained padding. A view read decodes from the image.
4. A view write must update the image transactionally, decode all observable
   source fields back into typed storage, and retain unobservable bytes for
   later views. Typed field writes must update the same image or invalidate and
   regenerate only the observable byte ranges without losing padding.
5. Record the byte offset at which a retyped pointer's view begins. Field and
   array addressing through the view then extends that byte offset rather than
   appending target-field names to the source struct's map path.
6. Preserve Go value semantics: struct and array assignment copy the byte
   companion; pointer aliases share it; captured variables shared with a task
   keep one companion; snapshots and values passed by value receive independent
   companions. These rules must be integrated with `bashPPCloner`,
   `bashPPCopyArrayValue`, pointer cloning, interface boxing, calls, returns,
   sends, and native boundaries.
7. Protect typed storage and its byte companion with one synchronization
   discipline. Publishing decoded fields separately from padding would permit
   mixed-version reads that no Go execution performs.
8. Continue refusing byte ranges that partially overlap a live pointer word,
   and preserve the existing pointer side table for exact pointer-word views.
   Architecture support should be explicit and tested.

## Acceptance tests for that mechanism

- Direct padding writes and reads through fresh conversions, including the
  `cmd/compile/internal/test` `TestCompareSkip` case.
- Overlay writes to observable source fields and typed writes observed through
  an existing overlay pointer.
- Nested structs, arrays of padded structs, pointer arithmetic to elements,
  and two differently typed aliases of one allocation.
- Struct assignment before and after creating a view, function arguments and
  returns, interface boxing, channel sends, and goroutine capture semantics.
- Internal and trailing padding, zero-sized fields, pointer-containing
  layouts, invalid bool/float bit patterns, nil pointers, and unsupported
  architectures.
- Race-focused tests confirming that a view never observes typed fields from
  one version beside padding from another.

Until this allocation-level representation exists, accepting only a write to
a padding-shaped field would discard observable state and special-case the
current fixture. The existing refusal is safer.
