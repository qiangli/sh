# Typed Go float readback

Preserve float provenance when a Go-source result or declaration cell records
its type in `declType` rather than `scalarKind`. Its stored exact rational is
not a source literal: decode it with the existing typed float decoder before
falling back to untyped text. Keep Classic Bash++ readback and actual strings
unchanged; conversion continues to perform float32/float64 rounding.

Validate independent native/interpreted probes for tuple results, local copies,
generic collection values, float32 rounding and string rejection. Upstream
corpus comparison and delivery gates remain separate, with unchanged sources.
