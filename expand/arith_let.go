package expand

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

func (cfg *Config) arithmAssocKey(word *syntax.Word) (string, error) {
	if key, ok := cfg.arithmRawAssocKeys[word]; ok {
		return key, nil
	}
	return Literal(cfg, word)
}

// protectLetAssocKeys separates expanded associative keys from arithmetic
// syntax. With assoc_expand_once, Bash reads the key through the first closing
// bracket; quotes, dollar signs and opening brackets are data. Only the normal
// arithmetic parser interprets the remaining expression. Offsets refer to the
// rewritten input, including its opening ((, and identify individual AST nodes.
func (cfg *Config) protectLetAssocKeys(text string) (string, map[uint]letAssocKey) {
	if !cfg.LetArithmetic || !cfg.AssocExpandOnce {
		return text, nil
	}
	var out strings.Builder
	var keys map[uint]letAssocKey
	depth := 0
	for i := 0; i < len(text); {
		if text[i] == '\'' || text[i] == '"' {
			start, quote := i, text[i]
			i++
			for i < len(text) {
				c := text[i]
				i++
				if c == '\\' && i < len(text) {
					i++
				} else if c == quote {
					break
				}
			}
			out.WriteString(text[start:i])
			continue
		}
		if !arithmNameStart(text[i]) {
			if text[i] == '[' {
				depth++
			} else if text[i] == ']' && depth > 0 {
				depth--
			}
			out.WriteByte(text[i])
			i++
			continue
		}
		start := i
		i++
		for i < len(text) && arithmNameContinue(text[i]) {
			i++
		}
		name := text[start:i]
		out.WriteString(name)
		vr := cfg.Env.Get(name)
		if n, resolved := vr.Resolve(cfg.Env); n != "" {
			vr = resolved
		}
		if depth != 0 || vr.Kind != Associative || i == len(text) || text[i] != '[' {
			continue
		}
		end := strings.IndexByte(text[i+1:], ']')
		if end < 0 {
			continue
		}
		end += i + 1
		// Empty keys must follow the existing error path, never become index zero.
		if end == i+1 {
			continue
		}
		out.WriteByte('[')
		if keys == nil {
			keys = make(map[uint]letAssocKey)
		}
		keys[uint(out.Len()+2)] = letAssocKey{name: name, value: text[i+1 : end]}
		out.WriteString("0]")
		i = end + 1
	}
	if len(keys) == 0 {
		return text, nil
	}
	return out.String(), keys
}

// Dollar operands outside subscripts have already undergone shell expansion.
// Subscripts have their own expansion rules and must be left to that evaluator.
func letDollarArithmToken(text string) string {
	depth := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '$':
			if depth == 0 {
				return literalDollarArithmToken(text[i:])
			}
		}
	}
	return ""
}

// A placeholder must bind to exactly one expected associative index. Never
// evaluate the numeric placeholder if parsing changed its structural meaning.
type letAssocKey struct{ name, value string }

func bindLetAssocKeys(expr syntax.ArithmExpr, keys map[uint]letAssocKey, previous map[*syntax.Word]string) (map[*syntax.Word]string, error) {
	bound := make(map[*syntax.Word]string, len(previous)+len(keys))
	for word, key := range previous {
		bound[word] = key
	}
	matched := make(map[uint]bool, len(keys))
	invalid := false
	syntax.Walk(expr, func(node syntax.Node) bool {
		pe, ok := node.(*syntax.ParamExp)
		if !ok || pe.Index == nil {
			return true
		}
		offset := pe.Index.Pos().Offset()
		key, ok := keys[offset]
		if !ok {
			return true
		}
		word, ok := pe.Index.(*syntax.Word)
		if !ok || pe.Param == nil || pe.Param.Value != key.name || word.Lit() != "0" || matched[offset] {
			invalid = true
			return false
		}
		matched[offset] = true
		bound[word] = key.value
		return true
	})
	if invalid || len(matched) != len(keys) {
		return nil, fmt.Errorf("invalid expanded associative subscript")
	}
	return bound, nil
}
