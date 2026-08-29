# Bash 5.3 Darwin regression repair

## Scope

- Keep a builtin writer's kernel-generated `SIGPIPE` inside its simulated
  pipeline process on Darwin, just as the Linux implementation already does.
- Treat a descriptor explicitly closed by the shell as absent when resolving
  `/dev/fd/N`, even if the embedding Go process still owns the inherited host
  descriptor.

## Implementation

1. Add a Darwin `writePipelineOutput` implementation that temporarily applies
   `F_SETNOSIGPIPE` to the pipeline descriptor, converting the generated signal
   to the `EPIPE` that the simulated child-status path already owns.
2. Consult `fdClosedTable` before virtual descriptor lookup can fall through
   to the host filesystem.
3. Add focused regressions for a nested pipeline whose non-final writer gets
   `SIGPIPE` and for a shell-closed descriptor backed by a still-live host fd.

## Gates

- `go test ./interp`
- On novidesign with Bashy `72c3feb`, this candidate, and Go 1.26.5:
  `make test-bash TESTS="set-e test trap"`
