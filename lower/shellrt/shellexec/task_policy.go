package shellexec

import (
	"context"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/lower/shellrt"
	"mvdan.cc/sh/v3/syntax"
)

func (sh *shell) taskPolicyContext(ctx context.Context) context.Context {
	if sh.cfg.lang == syntax.LangBashPP && shellrt.InTask(ctx) {
		return interp.WithTaskPolicy(ctx)
	}
	return ctx
}
