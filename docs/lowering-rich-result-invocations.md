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
