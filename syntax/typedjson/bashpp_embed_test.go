// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package typedjson_test

import (
	"reflect"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
	"mvdan.cc/sh/v3/syntax/typedjson"
)

func TestBashPPEmbeddedStructTypedJSON(t *testing.T) {
	const src = "type Inner struct { N int }\ntype Outer struct { Inner; *Inner }\n"
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), "embed.bpp")
	if err != nil {
		t.Fatal(err)
	}
	var encoded strings.Builder
	if err := typedjson.Encode(&encoded, file); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded.String(), `"Embedded":true`) {
		t.Fatalf("encoded tree lacks embedded marker: %s", encoded.String())
	}
	decoded, err := typedjson.Decode(strings.NewReader(encoded.String()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, file) {
		t.Fatal("typed JSON changed embedded struct tree")
	}
}
