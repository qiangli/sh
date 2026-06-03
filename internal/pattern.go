// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package internal

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/pattern"
)

// ExtendedPatternMatcher returns a [regexp.Regexp.MatchString]-like function
// to support !(pattern-list) extended patterns where possible.
// It can be used instead of [pattern.Regexp] for narrow use cases.
func ExtendedPatternMatcher(pat string, mode pattern.Mode) (func(string) bool, error) {
	if mode&pattern.ExtendedOperators != 0 && mode&pattern.EntireString == 0 {
		// In the future we could try to support !(pattern) without matching
		// the entire input, ensuring we add enough test cases.
		panic("ExtendedOperators is only supported with EntireString")
	}

	// `*(!(p))` and `+(!(p))` patterns embed a negation inside an
	// extglob quantifier. Detect these specifically and build a
	// custom matcher — bash matches when the input can be split
	// into 1+ pieces (or 0+ for `*()`) that each fail to match p.
	if rep, ok := repeatedNegationMatcher(pat); ok {
		return rep, nil
	}

	// `@(<alt1>|<alt2>|…)` containing one or more `!(…)` alts
	// across the alternatives — recurse per-alternative and OR
	// the results.
	if alt, ok := atUnionWithNegationMatcher(pat, mode); ok {
		return alt, nil
	}

	// Extended pattern matching operators are always on outside of pathname expansion.
	expr, err := pattern.Regexp(pat, mode)
	if err != nil {
		// Handle !(pattern-list) negation: when Regexp returns NegExtglobError,
		// match the inner pattern and negate the result.
		var negErr *pattern.NegExtGlobError
		if !errors.As(err, &negErr) {
			return nil, err
		}
		return extNegatedMatcher(pat, negErr.Groups)
	}
	rx, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	return rx.MatchString, nil
}

// atUnionWithNegationMatcher handles `@(<alt>|<alt>|…)` patterns
// where at least one alternative contains a `!(…)` negation —
// the alternatives are compiled separately and OR'd together.
// Returns (matcher, true) if pat matches the shape AND every
// alt parses; (nil, false) otherwise.
func atUnionWithNegationMatcher(pat string, mode pattern.Mode) (func(string) bool, bool) {
	if !strings.HasPrefix(pat, "@(") || !strings.HasSuffix(pat, ")") {
		return nil, false
	}
	inner := pat[len("@("):len(pat)-len(")")]
	if !strings.Contains(inner, "!(") {
		return nil, false
	}
	// Split top-level `|` only — `!(a|b)` has nested `|` that we
	// must keep with its group.
	var alts []string
	depth := 0
	last := 0
	for i := 0; i < len(inner); i++ {
		switch inner[i] {
		case '(':
			depth++
		case ')':
			depth--
		case '|':
			if depth == 0 {
				alts = append(alts, inner[last:i])
				last = i + 1
			}
		}
	}
	alts = append(alts, inner[last:])
	matchers := make([]func(string) bool, len(alts))
	for i, a := range alts {
		m, err := ExtendedPatternMatcher(a, mode)
		if err != nil {
			return nil, false
		}
		matchers[i] = m
	}
	return func(name string) bool {
		for _, m := range matchers {
			if m(name) {
				return true
			}
		}
		return false
	}, true
}

// repeatedNegationMatcher handles `*(!(p))` / `+(!(p))` patterns.
// Returns (matcher, true) if pat exactly matches that shape;
// (nil, false) otherwise.
func repeatedNegationMatcher(pat string) (func(string) bool, bool) {
	var quantifier byte
	switch {
	case strings.HasPrefix(pat, "*(!(") && strings.HasSuffix(pat, "))"):
		quantifier = '*'
	case strings.HasPrefix(pat, "+(!(") && strings.HasSuffix(pat, "))"):
		quantifier = '+'
	default:
		return nil, false
	}
	inner := pat[len("*(!("):len(pat)-len("))")]
	// Reject if the inner pattern itself contains another `!(` —
	// the simple split logic below can't reason about that.
	if strings.Contains(inner, "!(") {
		return nil, false
	}
	innerExpr, err := pattern.Regexp("@("+inner+")", pattern.EntireString|pattern.ExtendedOperators)
	if err != nil {
		return nil, false
	}
	rx, err := regexp.Compile(innerExpr)
	if err != nil {
		return nil, false
	}
	// Try every partition of name into 1+ pieces (0+ for `*`)
	// where each piece does NOT match the inner pattern.
	var canPartition func(name string) bool
	canPartition = func(name string) bool {
		if name == "" {
			return true
		}
		for split := 1; split <= len(name); split++ {
			piece := name[:split]
			if rx.MatchString(piece) {
				continue
			}
			if canPartition(name[split:]) {
				return true
			}
		}
		return false
	}
	return func(name string) bool {
		if quantifier == '*' && name == "" {
			return true
		}
		if name == "" {
			return false
		}
		// At least one piece must not match the inner pattern.
		return canPartition(name)
	}, true
}

// extNegatedMatcher handles !(pattern-list) extglob negation,
// supporting one or more `!(...)` groups in sequence: the literal
// text in between groups must match exactly, and each group's
// inner pattern must NOT match the corresponding span of `name`.
// The (possibly globby) suffix after the last `!(...)` is
// compiled as its own entire-string glob. The matcher tries every
// valid split of `name` across the groups and returns true if any
// split satisfies all of them.
func extNegatedMatcher(pat string, groups []pattern.NegExtGlobGroup) (func(string) bool, error) {
	// Per-segment compilation. Each group contributes: (literalBefore,
	// innerRx). The trailing suffix (after the last group) is a single
	// glob regex.
	type segment struct {
		literal string
		innerRx *regexp.Regexp
	}
	segs := make([]segment, len(groups))
	cursor := 0
	for i, g := range groups {
		literal := pat[cursor:g.Start]
		if pattern.HasMeta(literal, 0) {
			return nil, fmt.Errorf("extglob !(...) is only supported with literal text between groups")
		}
		inner := pat[g.Start+len("!(") : g.End-len(")")]
		innerExpr, err := pattern.Regexp("@("+inner+")", pattern.EntireString|pattern.ExtendedOperators)
		if err != nil {
			return nil, err
		}
		rx, err := regexp.Compile(innerExpr)
		if err != nil {
			return nil, err
		}
		segs[i] = segment{literal: literal, innerRx: rx}
		cursor = g.End
	}
	suffix := pat[cursor:]
	suffixExpr, err := pattern.Regexp(suffix, pattern.EntireString|pattern.ExtendedOperators)
	if err != nil {
		return nil, err
	}
	suffixRx, err := regexp.Compile(suffixExpr)
	if err != nil {
		return nil, err
	}

	var match func(name string, i int) bool
	match = func(name string, i int) bool {
		if i == len(segs) {
			return suffixRx.MatchString(name)
		}
		seg := segs[i]
		if !strings.HasPrefix(name, seg.literal) {
			return false
		}
		rest := name[len(seg.literal):]
		// Try every split of `rest` into negPart + remainder.
		// Note: even negPart="" is allowed — `!(p)` matches the
		// empty string when p is non-empty.
		for split := 0; split <= len(rest); split++ {
			negPart := rest[:split]
			if seg.innerRx.MatchString(negPart) {
				continue
			}
			if match(rest[split:], i+1) {
				return true
			}
		}
		return false
	}

	return func(name string) bool { return match(name, 0) }, nil
}
