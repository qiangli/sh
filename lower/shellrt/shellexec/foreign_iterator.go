package shellexec

import (
	"context"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/lower/shellrt"
	"mvdan.cc/sh/v3/polyglot"
)

func NewForeignIterator(module *polyglot.Module, name string, args []any) *shellrt.ForeignIterator {
	signature, _ := module.StreamSignature(name)
	return &shellrt.ForeignIterator{
		Element: signature.Iterator,
		Decode: func(ctx context.Context, line, element string) (any, string, string, error) {
			frame, err := module.DecodeIteratorFrame(ctx, line, element)
			return frame.Value, frame.Stdout, frame.Stderr, err
		},
		Start: func(ctx context.Context) (shellrt.IteratorProcess, error) {
			return interp.StartForeignIterator(ctx, module, name, args, shellrt.Stdout, shellrt.Stderr)
		},
	}
}
