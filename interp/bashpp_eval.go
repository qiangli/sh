// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Bash++ P2 evaluator capability policy.
//
// The design of record makes this a PRECONDITION of choosing an evaluator, not
// a consequence of it: "decide the fallback for cgo, compiled-only and
// unsupported-feature packages before an evaluator is selected, or the choice
// is made by whatever the prototype happened to run."  This file is that
// decision, expressed as code so it is executable rather than prose.  The
// narrative record, including the measured evidence that retired the Yaegi
// time-box, is docs/bashpp-p2-evaluator-decision.md.
//
// TWO RULES GOVERN EVERYTHING BELOW.
//
//  1. FAIL CLOSED.  An unclassified package is refused, never executed.  The
//     zero value of [bashPPCapability] is capUnknown and its policy is
//     policyRefuse, so a class someone forgets to wire up declines to run
//     instead of quietly reaching the toolchain.
//
//  2. NO SILENT SEMANTIC FALLBACK.  Whenever execution moves from the
//     in-process interpreted evaluator to the reviewed native toolchain, the
//     transition is announced on stderr.  A fallback nobody can observe is
//     indistinguishable from the evaluator having been chosen by accident,
//     which is precisely the failure the design warned about.

// bashPPCapability classifies an import path by what an evaluator can actually
// do with it. Classification is derived from `go list -json` facts, never from
// the import path's spelling.
type bashPPCapability int

const (
	// capUnknown is the zero value and is deliberately first: an
	// unclassified package must refuse, not execute.
	capUnknown bashPPCapability = iota
	// capInterpreted is a reviewed pure-Go standard-library package, which
	// the in-process evaluator may run without any toolchain.
	capInterpreted
	// capNativeOnly is pure Go and buildable, but outside the in-process
	// evaluator's reviewed inventory: local-module, workspace, vendor and
	// GOPATH packages land here.
	capNativeOnly
	// capCgo requires cgo.
	capCgo
	// capCompiledOnly has no Go source available to interpret or rebuild.
	capCompiledOnly
	// capNotBuildable is `package main`, a package with no buildable Go
	// files, or one excluded by build constraints on this platform.
	capNotBuildable
	// capUnreviewedStdlib is a standard-library package outside the
	// reviewed Go 1.27 inventory. It is refused rather than run: the
	// reviewed inventory is an allow-list, and quietly widening it to
	// "anything go list calls standard" would defeat the review.
	capUnreviewedStdlib
	// capMissing could not be resolved at all.
	capMissing
)

// bashPPPolicy is what the runner does with a classified package.
type bashPPPolicy int

const (
	// policyRefuse is the zero value, so the default for anything
	// unclassified is to decline. See rule 1 above.
	policyRefuse bashPPPolicy = iota
	// policyInterpret runs the call in-process, with no toolchain.
	policyInterpret
	// policyFallbackExplicit runs the call on the reviewed native
	// toolchain, after announcing the transition. See rule 2 above.
	policyFallbackExplicit
)

// bashPPPolicyFor is the single decision table. It is a pure function of the
// capability class so the policy can be tested without a toolchain, a module,
// or a network.
func bashPPPolicyFor(c bashPPCapability) bashPPPolicy {
	switch c {
	case capInterpreted:
		return policyInterpret
	case capNativeOnly:
		// Pure Go and buildable, just not in the in-process inventory.
		// The reviewed toolchain is a policy-approved fallback for it,
		// and the transition is announced.
		return policyFallbackExplicit
	case capCgo, capCompiledOnly, capNotBuildable, capUnreviewedStdlib, capMissing, capUnknown:
		return policyRefuse
	}
	return policyRefuse
}

// refusal explains a refusal in the shell's own voice. Every string here is
// user-facing, so each says what was refused and why, never merely that
// something failed.
func (c bashPPCapability) refusal() string {
	switch c {
	case capCgo:
		// bashy is a pure-Go program (see CLAUDE.md, "Pure Go only").
		// Building a cgo package would require a C toolchain the shell
		// does not promise and cannot review, so this refuses rather
		// than inheriting a dependency the project has ruled out.
		return "package requires cgo, which this pure-Go shell does not provide"
	case capCompiledOnly:
		return "package has no Go source available to interpret or build"
	case capNotBuildable:
		return "package is not importable: it is package main, has no buildable Go files, or is excluded on this platform"
	case capUnreviewedStdlib:
		return "package is not in the reviewed Go standard library"
	case capMissing:
		return "package could not be resolved"
	case capUnknown:
		return "package could not be classified"
	}
	return "package is not supported by any available evaluator"
}

// bashPPPackageFacts is the subset of `go list -json` this policy reads.
type bashPPPackageFacts struct {
	Name           string
	Standard       bool
	Dir            string
	GoFiles        []string
	CgoFiles       []string
	IgnoredGoFiles []string
	Incomplete     bool
	Error          *struct{ Err string }
}

// classifyBashPPPackage maps go list facts onto a capability class.
//
// Order matters and is not arbitrary. Unresolvable and unbuildable are checked
// before cgo, because a package that cannot be imported at all should say so
// rather than complain about cgo it was never going to reach.
func classifyBashPPPackage(f bashPPPackageFacts, path string) bashPPCapability {
	// A package whose every file is excluded by build constraints reports a
	// go list Error ("build constraints exclude all Go files"), but calling
	// that "could not be resolved" would be wrong and unhelpful: the package
	// exists and simply does not build on this host. Check the platform shape
	// before falling through to the generic resolution failure.
	if len(f.GoFiles) == 0 && len(f.CgoFiles) == 0 && len(f.IgnoredGoFiles) > 0 {
		return capNotBuildable
	}
	if f.Error != nil || f.Incomplete {
		return capMissing
	}
	// Go itself refuses to import package main. Matching that exactly is
	// mandatory: the measured Yaegi prototype ACCEPTED such an import, and
	// silently diverging from Go here is the kind of defect this policy
	// exists to keep out.
	if f.Name == "main" {
		return capNotBuildable
	}
	if len(f.GoFiles) == 0 && len(f.CgoFiles) == 0 {
		if len(f.IgnoredGoFiles) > 0 {
			// Source exists but every file is excluded by build
			// constraints: a platform mismatch, not a missing package.
			return capNotBuildable
		}
		if f.Dir == "" {
			return capCompiledOnly
		}
		return capNotBuildable
	}
	if len(f.CgoFiles) > 0 {
		return capCgo
	}
	if f.Standard {
		if syntax.BashPPStdlibImportAllowed(path) {
			return capInterpreted
		}
		return capUnreviewedStdlib
	}
	return capNativeOnly
}

// bashPPGoListFacts runs `go list -json` for one import path.
func bashPPGoListFacts(ctx context.Context, req bashPPEvalRequest, path string) (bashPPPackageFacts, error) {
	cmd := exec.CommandContext(ctx, req.Go, "list", "-e", "-json", path)
	cmd.Dir, cmd.Env = req.Dir, req.Env
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return bashPPPackageFacts{}, ctxErr
		}
		return bashPPPackageFacts{}, fmt.Errorf("bash++ import %q: go list: %w", path, err)
	}
	var facts bashPPPackageFacts
	if err := json.Unmarshal(out.Bytes(), &facts); err != nil {
		return bashPPPackageFacts{}, fmt.Errorf("bash++ import %q: go list: %w", path, err)
	}
	return facts, nil
}

// bashPPInterpreter is the seam an in-process, toolchain-free evaluator plugs
// into. It is intentionally NARROWER than [bashPPEvaluator]: an interpreter
// only ever executes a call whose package already passed import-time policy,
// so it has no Resolve of its own.
//
// It is currently unimplemented, and that is a recorded outcome rather than an
// oversight. The Yaegi time-box the design authorised was run and expired on
// measured evidence: see docs/P2-EVALUATOR-BLOCKERS.md. Keeping the seam here,
// with no implementation behind it, is what makes the absence visible and
// keeps the eventual adapter a package-private file rather than an API change.
type bashPPInterpreter interface {
	// Call runs a selector call in-process. It returns
	// errBashPPNotInterpretable, and must have written nothing to
	// req.Stdout, when it cannot run this particular call.
	Call(context.Context, bashPPEvalRequest) error
}

// errBashPPNotInterpretable is how an interpreter declines a call it cannot
// run, as distinct from a call that ran and failed. The two must not be
// conflated: declining is dispatched onward, whereas a genuine failure is the
// program's result and is returned unchanged.
var errBashPPNotInterpretable = errors.New("bash++: call is not available to the in-process evaluator")

// policyBashPPEvaluator is the evaluator the runner uses by default. It owns
// the capability decision and delegates execution: to the in-process
// interpreter where one exists and the policy allows it, and otherwise to the
// reviewed native toolchain, announced.
//
// It holds NO per-session mutable state. That is deliberate and load-bearing:
// the capability decision is taken at import time and the import registry is
// already per-runner and cloned into subshells, so a subshell can never
// observe or corrupt a sibling's classification. Caching classifications on
// the evaluator would have reintroduced exactly the shared state the session
// isolation tests forbid, because bashPPToolchain.eval is an interface value
// shared by every subshell.
type policyBashPPEvaluator struct {
	// interpreted is nil while no in-process evaluator is adopted.
	interpreted bashPPInterpreter
	native      bashPPEvaluator
}

func newPolicyBashPPEvaluator() *policyBashPPEvaluator {
	return &policyBashPPEvaluator{native: nativeBashPPEvaluator{}}
}

// Resolve classifies the package and applies the policy.
//
// DIAGNOSTIC TIMING. Every class that can be decided from package facts is
// decided HERE, at `import`, not deferred to the first call. A refused package
// must fail on the line that named it: an import that appears to succeed and
// then fails at an unrelated call site later reports the error in the wrong
// place, and under `set -e` it aborts at the wrong statement. The registry is
// only ever populated with packages that passed policy.
func (e *policyBashPPEvaluator) Resolve(ctx context.Context, req bashPPEvalRequest, path string) (string, error) {
	facts, err := bashPPGoListFacts(ctx, req, path)
	if err != nil {
		return "", err
	}
	capability := classifyBashPPPackage(facts, path)
	switch bashPPPolicyFor(capability) {
	case policyRefuse:
		return "", fmt.Errorf("bash++ import %q: %s", path, capability.refusal())
	case policyFallbackExplicit:
		e.announce(req, fmt.Sprintf("import %q", path))
	}
	if !syntax.ValidName(facts.Name) {
		return "", fmt.Errorf("bash++ import %q: invalid package name %q", path, facts.Name)
	}
	if err := validateBashPPImportVisibility(req.Dir, facts.Dir, path); err != nil {
		return "", err
	}
	return facts.Name, nil
}

// announce writes the mandatory no-silent-fallback notice. Rule 2.
//
// It is silent while no interpreter is adopted, and that is the rule applied
// correctly rather than an exemption from it. Rule 2 exists to make a
// per-call SEMANTIC DOWNGRADE visible: this call would have been interpreted,
// and was not. With no interpreter adopted there is no downgrade to report —
// the reviewed toolchain is the engine the policy selects for everything, a
// fact that belongs in docs/bashpp-p2-evaluator-decision.md and not on stderr
// before every line of every script. Announcing unconditionally would also
// change Classic-visible output for programs whose behavior did not change.
func (e *policyBashPPEvaluator) announce(req bashPPEvalRequest, what string) {
	if e.interpreted == nil || req.Stderr == nil {
		return
	}
	fmt.Fprintf(req.Stderr, "bash++: %s: not available to the in-process evaluator; using the reviewed Go toolchain\n", what)
}

// Call executes a selector call, preferring the in-process interpreter.
//
// A package whose specific symbol the interpreter declines is still a semantic
// fallback, so it is announced too: the package passed policy, but this call
// is not one the interpreter can run.
func (e *policyBashPPEvaluator) Call(ctx context.Context, req bashPPEvalRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.interpreted != nil {
		switch err := e.interpreted.Call(ctx, req); {
		case err == nil:
			return nil
		case errors.Is(err, errBashPPNotInterpretable):
			e.announce(req, strings.Join(req.Selector, "."))
		default:
			// A real failure from a call that actually ran is the
			// program's result. Retrying it natively would run the
			// user's code a second time.
			return err
		}
	}
	return e.native.Call(ctx, req)
}
