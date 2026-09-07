# Native method and subshell integration

Execution units dispatch source methods through private Program-aware methods while retaining their public Go signatures. Bound method values retain the receiver and accept the invocation Program; interface dispatch uses the private capability adapter when available.

Native subshell bodies execute in a child Program with a separate shell session, channel scope, and readonly state. Generated code registers every captured native binding address before cloning the graph, preserving shared pointers and rebasing pointers to captured local bindings. Initializers and statements remain in source order.

Coverage includes same-source interpreter/artifact comparisons for pointer isolation, readonly subshell enforcement, concrete and interface marked methods, bound handles, and failed package-scope declarations. Typed output at script scope clears command status; typed output inside a source function preserves the function's outstanding diagnostic status.

Remaining boundaries include callable and opaque-resource graph captures, constant captures, and source functions accessing package globals from a subshell. Independent generic method parameters require the separate parser and method-emitter extension. These cases are not certified by this integration slice.
