package shellexec

import (
	"context"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/lower/shellrt"
	"mvdan.cc/sh/v3/polyglot"
)

func NewForeignIterator(module *polyglot.Module, name string, args []any) *shellrt.ForeignIterator {
	signature, _ := module.StreamSignature(name)
	return &shellrt.ForeignIterator{Element: signature.Iterator, Decode: module.DecodeIteratorFrame, Start: func(ctx context.Context) (shellrt.IteratorProcess, error) {
		return interp.StartForeignIterator(ctx, module, name, args, shellrt.Stdout, shellrt.Stderr)
	}}
}
