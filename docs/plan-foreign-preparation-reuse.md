# Foreign preparation across lower inference passes

`lower.Compile` can emit twice: the first pass type-checks package globals and
the second emits their inferred types. Native `polyglot.Prepare` analyzes and
compiles foreign fence source, so repeating it can dominate a bounded compile.

Keep one private `preparedForeign` value on the stack of a `Compile` call and
pass it through both recursive paths (flat and library output). Cache only a
successful native plan. Each pass still reconstructs imports, reserved names,
diagnostics, runtime configuration, and its own emitter. A cache hit requires
the same blocks, source path, source directory, process environment, selected
foreign environment plans, language registry revision, and actual bytes of each
selected provider executable, resolution file, and C++ module source. An
unreadable file disables reuse so a fresh preparation supplies its own error.
File snapshots bracket the first preparation, preventing a file changed during
analysis from being stored under its later bytes.
Runner fences always
run their user-provided analyzer again. A canceled context cannot take a hit.
The saved environment plans are cloned, including C++ module units and their
dependencies; C# assembly graph bytes and C++ source asset fingerprints are
compared through the newly discovered plans before reuse. Same-path file edits
cannot retain a plan prepared for old provider or module bytes.

This is bounded to one prepared plan set per compilation. No package-level or
disk cache survives a call. Re-registering a provider increments the registry
revision, so a change between inference passes forces a new preparation. A
failed preparation is never stored.

Reuse admission is limited to units containing only C/C++ blocks, with no custom runner. Other-language and mixed units prepare afresh. Provider executables, resolution metadata and declared module source bytes are compared before and after preparation and before reuse. This does not claim to rehash transitive SDK/system headers: within one compilation the first successful preparation is the unit snapshot. Acceptance uses an authenticated immutable SDK tree; mutation of undeclared external headers during that compilation is outside this bounded identity check. No cross-compilation cache is introduced.
