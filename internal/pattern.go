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

// extNegatedMatcher handles !(pattern-list) extglob negation.
// Supports a single !(...) group; the prefix must be fixed text,
// but the suffix can be an arbitrary glob (e.g. `!(foo)*`,
// `!(foo)bar*`). The negation matches when *some* split of the
// remaining string after the prefix can satisfy both halves: the
// first half doesn't match the inner pattern, the second half
// matches the glob suffix.
func extNegatedMatcher(pat string, groups []pattern.NegExtGlobGroup) (func(string) bool, error) {
	if len(groups) != 1 {
		return nil, fmt.Errorf("multiple extglob !(...) groups are not supported yet")
	}
	g := groups[0]
	prefix := pat[:g.Start]
	suffix := pat[g.End:]

	if pattern.HasMeta(prefix, 0) {
		return nil, fmt.Errorf("extglob !(...) is only supported with a fixed prefix")
	}

	// Use @(inner) to compile the pattern list, then negate the match.
	inner := pat[g.Start+len("!(") : g.End-len(")")]
	innerExpr, err := pattern.Regexp("@("+inner+")", pattern.EntireString|pattern.ExtendedOperators)
	if err != nil {
		return nil, err
	}
	innerRx, err := regexp.Compile(innerExpr)
	if err != nil {
		return nil, err
	}

	// Suffix may itself be a glob. Compile it (entire-string match).
	suffixExpr, err := pattern.Regexp(suffix, pattern.EntireString|pattern.ExtendedOperators)
	if err != nil {
		return nil, err
	}
	suffixRx, err := regexp.Compile(suffixExpr)
	if err != nil {
		return nil, err
	}

	return func(name string) bool {
		if !strings.HasPrefix(name, prefix) {
			return false
		}
		rest := name[len(prefix):]
		// Try every split of `rest` into negPart + suffPart such
		// that negPart does NOT match the inner pattern and
		// suffPart matches the suffix glob.
		for split := 0; split <= len(rest); split++ {
			negPart := rest[:split]
			suffPart := rest[split:]
			if innerRx.MatchString(negPart) {
				continue
			}
			if suffixRx.MatchString(suffPart) {
				return true
			}
		}
		return false
	}, nil
}
