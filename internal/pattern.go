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
