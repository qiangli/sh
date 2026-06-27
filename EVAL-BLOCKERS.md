# eval-p BLOCKERS

All eval-p yash test cases pass byte-for-byte against `gosh --posix`:

- evaluating no operands: PASS
- evaluating null operands: PASS
- evaluating some commands: PASS
- separator preceding operand: PASS
- operands are concatenated with spaces in-between: PASS
- exit status of evaluation: PASS
- effect on environment in evaluation: PASS

No blockers found; no code changes needed.
