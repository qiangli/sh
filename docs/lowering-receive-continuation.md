# Receive declaration continuation

Native receive declarations capture the value, closed-channel indicator, and operation error together. Failed receives leave source bindings absent for interpolation. Owner functions can finish the current sequence while Program.ReceiveFailure defers cancellation for task-failure arbitration. A canceled task context instead unwinds immediately, so a blocked sibling cannot execute later output.

Select send operands use the same source-word conversion as ordinary sends, including bare string operands. Runtime channel errors and provider errors are handled by the separate Program operation repair.

The compiled entry acceptance matrix exercises owner receive output, sibling cancellation, exit traps, closed sends, ready/default select, and range returns at GOMAXPROCS 1, 2, and 4. The runtime releases task launch admission at a real interpreter blocking boundary or task termination. Immediate ready/default operations therefore complete before the next launch, and task failure remains visible during shutdown.
