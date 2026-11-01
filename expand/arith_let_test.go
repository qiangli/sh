package expand

import (
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func TestLetAssocKeyBinding(t *testing.T) {
	expr, err := syntax.NewParser().Arithmetic(strings.NewReader("m[0]+m[0]"))
	if err != nil {
		t.Fatal(err)
	}
	indices := map[uint]letAssocKey{}
	syntax.Walk(expr, func(node syntax.Node) bool {
		if pe, ok := node.(*syntax.ParamExp); ok && pe.Index != nil {
			indices[pe.Index.Pos().Offset()] = letAssocKey{name: "m", value: "raw"}
		}
		return true
	})
	if len(indices) != 2 {
		t.Fatal(indices)
	}
	if bound, err := bindLetAssocKeys(expr, indices, nil); err != nil || len(bound) != 2 {
		t.Fatalf("binding=%v error=%v", bound, err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[uint]letAssocKey)
	}{
		{"unmatched", func(keys map[uint]letAssocKey) { keys[1000] = letAssocKey{name: "m", value: "lost"} }},
		{"wrong-name", func(keys map[uint]letAssocKey) {
			for offset := range keys {
				keys[offset] = letAssocKey{name: "other", value: "lost"}
				break
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keys := map[uint]letAssocKey{}
			for offset, key := range indices {
				keys[offset] = key
			}
			tc.mutate(keys)
			if _, err := bindLetAssocKeys(expr, keys, nil); err == nil {
				t.Fatal("unbound placeholder accepted")
			}
		})
	}
}

func TestLetDollarOperands(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"n += $v", "$v"}, {"m[$key]++", ""}, {"m[${key}]++", ""}, {"m[$(printf x)]++", ""}, {"m[0]+$v", "$v"},
	} {
		if got := letDollarArithmToken(tc.text); got != tc.want {
			t.Errorf("%q: got %q want %q", tc.text, got, tc.want)
		}
	}
}

func TestLetAssocKeyScope(t *testing.T) {
	env := testEnv{"m": {Kind: Associative, Set: true, Map: map[string]string{}}}
	for _, tc := range []struct {
		name, text string
		let, once  bool
		want       string
		count      int
	}{
		{"active", "++m[o'clock]", true, true, "++m[0]", 1},
		{"not-let", "++m[o'clock]", false, true, "++m[o'clock]", 0},
		{"option-unset", "++m[o'clock]", true, false, "++m[o'clock]", 0},
		{"nested-index", "a[m[o'clock]]=7", true, true, "a[m[o'clock]]=7", 0},
		{"empty", "m[]=7", true, true, "m[]=7", 0},
		{"extra-close", "m[x]]=7", true, true, "m[0]]=7", 1},
		{"unknown-array", "other[o'clock]=7", true, true, "other[o'clock]=7", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Env: env, LetArithmetic: tc.let, AssocExpandOnce: tc.once}
			got, keys := cfg.protectLetAssocKeys(tc.text)
			if got != tc.want || len(keys) != tc.count {
				t.Fatalf("text=%q keys=%v", got, keys)
			}
		})
	}
}
