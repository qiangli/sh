# Dry-run file interception and Bash++ task opens

The product installs a dry-run open handler for runtime `set -o dryrun`
changes. Treating this always-installed wrapper as the active normal handler
blocks ordinary Bash++ task redirection, whose cancellation and FIFO protocol
requires native opens.

Implement a `DryRunOpenHandler` override selected only while the runner's
existing dry-run option is active. Keep the normal open handler independent;
its custom-handler classification must survive an inactive or removed override.
Use the same active-handler decision for ordinary opens and task/FIFO routing.
Copy the override and current/original dry-run option state through shell copies
and Reset. Active custom handlers, including the dry-run override, remain
refused inside tasks before any handler call or file mutation.

Verify native task writes, initial/runtime/task-local option changes, shell and
public subshells, command substitution, pipelines, Reset, custom-handler refusal,
native FIFO rendezvous and cancellation guards. The product wires this override
and checks the installed awd task boundary with explicit completion channels;
EOF cancellation is not a substitute for task completion.
