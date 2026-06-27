# POSIX conformance exec blockers

This document details the blockers preventing full POSIX conformance of the `exec` builtin command in the pure-Go shell interpreter. 

Specifically, the "process ID of executed process" case (e.g. `exec sh -c "[ \$\$ -eq <pid> ]"`, expecting the `$$` variable to remain unchanged across `exec`) cannot be fully matched. Because this Go-based shell engine runs subshells and commands as goroutines or child processes via the `os/exec` package rather than using real `fork`/`exec` syscalls to replace the current running shell process, the PID identity of the replaced process cannot be preserved. Attempting to preserve the PID across shell execution is thus limited by the runtime environment and pure-Go implementation architecture.
