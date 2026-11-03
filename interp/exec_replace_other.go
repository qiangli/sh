// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

//go:build unix && (!linux || cgo)

package interp

import "context"

func execReplace(ctx context.Context, path string, args, env []string, stdin, stdout, stderr any, owned *ownedExecHandoff) (bool, error) {
	return execReplaceLegacy(ctx, path, args, env, stdin, stdout, stderr, owned)
}
