# Import context and method argument integration

Compile uses Options.Dir as its import/build context. One module-aware importer resolves source imports and checks the emitted Go file, preserving package identity across ordinary, aliased, dot and runtime imports. Origin remains source identity only. Local modules, workspace replacements, module vendors, GOPATH and source-only GOPATH vendor layouts are exercised through both the interpreter and source-removed native artifacts.

A bare source word matching a function name remains literal text when the resolved destination parameter is string. Callable parameters continue to receive native function values. Method calls and expressions preserve explicit generic instantiation and evaluate parser-owned scalar arguments directly.

Pointer declarations initialized from a scalar create a typed native cell. Pointer method receivers retain separate shell projection provenance: their bound receiver exposes the pointee value, while an ordinary pointer variable still interpolates empty. Nil receivers interpolate empty without dereferencing. The complete public methods/dispatch.bpp fixture covers concrete calls, method values, pointer/value method expressions and deferred calls.
