# Invocation result descriptors

The native `Ch` declaration keeps its source integer representation. A function
that returns a channel through that declared result records the channel in a
caller-owned `ResultFrame`. The caller passes the frame in a copied `Program`;
the callee takes it at entry and clears the body program's frame pointer before
nested calls. No last-result slot or serialized handle grants authority.

A tuple assignment validates all values and capabilities before committing
native destinations and their address-keyed sidecars. A subshell forks those
sidecars through the same snapshot address map as its values. A public native
wrapper rejects capability-only results that its Go signature cannot express.

The first compiler integration verifies the unchanged public named-rich-result
source through Compile and a source-removed artifact: pointer, struct and
integer-declared channel results produce `5:6:7`. Scalar carrier arguments,
forwarded result metadata and absent-result assignment handling require their
respective integration paths; runtime helper tests alone do not certify them.

Source call initializers now test result presence independently of the native
return value. A denied invocation leaves its target absent; forwarding an
absent scalar result preserves the original diagnostic. A shared sequential
short-declaration failure mark retains the source status after later output.
Named results are refreshed after source defers, so a recovered panic can still
return the final named value. The four public refused-agentic-result controls
verify complete output, diagnostics and status through source-removed artifacts.

Channel authority can also enter a native scalar carrier through a real shell
binding assignment or a source call's argument/default. The caller evaluates
arguments once in source order, retains channel identity in an explicit
argument frame, and binds it to the callee's actual parameter addresses.
Channel display strings are opaque text; interpolation does not copy authority.
The C3 artifact verifies direct, assigned, ordinary, named and default argument
paths, followed by exact refusal of a forged string, under the race detector.
