# Receive declaration continuation

Native receive declarations capture the value, closed-channel indicator, and operation error together. Failed receives leave source bindings absent for interpolation. Owner functions can finish the current sequence while Program.ReceiveFailure defers cancellation for task-failure arbitration. A canceled task context instead unwinds immediately, so a blocked sibling cannot execute later output.

Select send operands use the same source-word conversion as ordinary sends, including bare string operands. Runtime channel errors and provider errors are handled by the separate Program operation repair.

The compiled entry acceptance matrix exercises owner receive output, sibling cancellation, exit traps, closed sends, ready/default select, and range returns at GOMAXPROCS 1, 2, and 4. The integration currently retains a task scheduling failure at GOMAXPROCS 4: some immediate select failures allow the next task to start, and a shell task canceled during startup can lose its failure status. This dispatcher change does not certify that runtime scheduling behavior.
