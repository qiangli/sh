# Sprint 79 Issue 7 interface audit

1. Map the POSIX.1-2016 Issue 7 clauses for `alias`, `echo`, `false`,
   `true`, and `unalias` onto the strict POSIX runner used by Bashy's `sh`.
2. Add one stable, command-named test per interface, including every
   applicable option, operand, standard stream, environment, effect, error,
   and status branch. Keep `echo -n`/backslash behavior explicitly marked as
   implementation-defined and XSI escape behavior explicitly conditional.
3. Fix only failures confirmed on that path, preserving default Bash behavior.
4. Repeat focused tests, then run race, vet, cross-build, broader package, and
   whitespace gates before committing and verifying a clean branch.
