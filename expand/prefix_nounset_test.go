package expand

import (
	"errors"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func TestPrefixNounsetErrorBoundaries(t *testing.T) {
	for _, tc := range []struct {
		src string
		bad bool
	}{
		{`${missing}`, false}, {`${!missing}`, false}, {`${missing*}`, true}, {`${!1*}`, true},
	} {
		t.Run(tc.src, func(t *testing.T) {
			word, err := syntax.NewParser().Document(strings.NewReader(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Literal(&Config{Env: testEnv{}, NoUnset: true}, word)
			if tc.bad {
				var target BadSubstitutionError
				if !errors.As(err, &target) {
					t.Fatalf("want bad substitution, got %T: %v", err, err)
				}
			} else {
				var target UnsetParameterError
				if !errors.As(err, &target) {
					t.Fatalf("want unset error, got %T: %v", err, err)
				}
			}
		})
	}
}
