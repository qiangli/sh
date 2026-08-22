# Runner-backed document expansion

1. Add a focused public-API test proving expansion uses the runner's live
   variables and command-substitution machinery.
2. Expose one synchronous, no-field-splitting expansion method on `Runner`
   using the same expansion configuration as interpreted commands.
3. Run focused, race, full, 32-bit, and cross-platform build gates before the
   downstream Bashy startup fix consumes the API.
