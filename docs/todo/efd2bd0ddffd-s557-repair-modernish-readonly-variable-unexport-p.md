---
id: efd2bd0ddffd
kind: bug
title: S557 repair Modernish readonly variable unexport probe
seq: 182
status: assigned
priority: p1
created: 2026-11-02T01:10:42.019905Z
assignee: s557-manager
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
---

Frozen candidate58a2442e Modernish run41370662210 core initialization succeeds but tests stop in lib/modernish/mdl/var/unexport.mm line132: declare -rx _Msh_test="ok", then unset readonly failure; zero Bashy tests measured. Inspect pinned source read-only; authored GNU5.3 differential reproductions before isolated narrow runtime correction. Preserve readonly value protection and export semantics, no foreign-suite execution locally, no public candidate mutation/push; root reviews and owns hosted acceptance.


## Isolated repair evidence (internal helper, s557-manager)

Pinned Modernish cde49fa37f93e943b9cb9b9536ce83836c4864ae probes an
unsupported long-option typeset form before its supported Bash -g/+x fallback.
The command-form declaration implementation ignored unknown option characters,
then interpreted letters in the long option as export, print, and readonly.
An authored GNU Bash 5.3.15 comparison reproduced the retained failure before
production edits: the probe became readonly/exported, its value changed, and
cleanup failed; GNU returned status2 without changing the variable. The same
pre-fix selection also exposed failure to clear export in the valid -g/+x path
and silently accepted unknown short options (focused test failed in0.470s).

The command-form fallback now rejects unknown options before interpreting their
remaining letters; supports -g global lookup/storage and +x export removal;
honors -- and operand boundaries; and preserves readonly assignment protection.
Attribute-only changes retain the existing value via KeepValue. The two export
attribute masks match GNU: +x clears export even when -x also appears. Existing
unsupported valid declaration attributes are not expanded by this repair.
Indexed/name-reference operand paths retain their prior validation; the new
identifier check is confined to flag-shaped arguments after option parsing.

Eighteen authored cases passed unconditional assertions and GNU5.3.15 comparison
(0.607s together with existing TestDeclare2Fidelity): exact probe/fallback,
unknown short/long flags, --, export mask ordering, literal values and embedded
'=', readonly attribute/value distinction, global/local and readonly-shadow
scope, existing nameref target, inline export, and options after first operand.
Twelve selected existing TestRunnerRun declaration/inline/nameref/readonly rows
and the dedicated readonly-prefix/errexit guards passed (0.390s). The existing
Declare2 fidelity table also passed separately (0.446s). No vendor suite or
product build ran locally; the frozen public candidate was not changed.

Modernish run41370662210 remains INIT BLOCKED with0Bashy tests measured despite
workflow success. Acceptance requires a new hosted run on an integrated candidate;
this isolated patch does not change that recorded result. Root independently
reviewed the narrowed production diff before this commit.
