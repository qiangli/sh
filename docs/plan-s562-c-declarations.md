# S562 E4 C declaration repair

The C corpus has six missing-feature rows because export discovery rejects
every file-scope variable. Accept C variable declarations as private parts of
the translation unit while continuing to export only supported functions.
Keep C++ rejection and native per-call process lifetime unchanged.

Validate a scalar, static variable, array and bitfield struct through a native
call, and preserve the C++ unsupported-surface regression. Exercise the C fence
through the interpreter, including separate-call variable initialization.

The reported C `_Generic` gap does not reproduce through the baseline C17
analyzer, generated worker and call using the pinned compiler. Retain a
multiline macro regression. The interpreter reproduces the failure: the raw
fence reader writes the shell lexer's escaped-newline sentinel into the macro.
Restore backslash-newline while collecting each physical source line; verify
LF/CRLF input, streaming, source positions and print roundtripping. No compiler
flag change is justified by this evidence.

Run focused checks on the claimed test host, then have the corpus owner
regenerate and rerun the original seven rows. Keep original failures and
candidate-specific results; do not reduce coverage or grow timeouts.
