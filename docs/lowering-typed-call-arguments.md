# Typed scalar call arguments

Calls carrying BashPPCall.ArgExprs lower those scalar nodes directly. Legacy Args remain available for ordinary shell-word call syntax and for source printing; they no longer determine the value of a committed scalar argument. The shared callArgument hook covers ordinary calls, immediate literals, task captures, default/named argument plans, deferred plans and method-expression dispatch. Method adapter helpers must use the same hook when their separate generic-method extension is integrated.

Compile runs CheckProfile before Bash# checking or native Go emission. Proven static source errors return their BASHPP diagnostic and no Result. Impossible interface assertions therefore use semantic rejection; possible assertions with a mismatched dynamic value still execute as runtime artifacts.

Same-source artifact tests cover nested arithmetic arguments, nested calls and lazy boolean effects. The complete lower suite passes with only the separately tracked task-scheduling acceptance excluded; no task cancellation behavior is certified by this scalar change.
