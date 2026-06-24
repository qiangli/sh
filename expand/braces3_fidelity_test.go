// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"reflect"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func TestBraces3Fidelity(t *testing.T) {
	t.Parallel()

	t.Run("ParamElemWithSuffix", func(t *testing.T) {
		word := parseCallArg(t, "echo {$a,b}_{c,d}", 1)
		got, err := Fields(&Config{Env: ListEnviron("a=A")}, word)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"b_c", "b_d"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("want %q, got %q", want, got)
		}
	})

	t.Run("EmptyListElemsElideBareWords", func(t *testing.T) {
		word := parseCallArg(t, `printf '%s\n' {X,,Y,}`, 2)
		got, err := Fields(nil, word)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"X", "Y"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("want %q, got %q", want, got)
		}
	})

	t.Run("BraceResultLeadingTilde", func(t *testing.T) {
		word := parseCallArg(t, "echo {foo~,~}/bar", 1)
		got, err := Fields(&Config{Env: ListEnviron("HOME=/home/bob")}, word)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"foo~/bar", "/home/bob/bar"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("want %q, got %q", want, got)
		}
	})

	t.Run("PrefixTildeBraceResults", func(t *testing.T) {
		word := parseCallArg(t, "echo ~{/src,root}", 1)
		got, err := Fields(&Config{Env: ListEnviron("HOME=/home/bob", "HOME root=/root")}, word)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"/home/bob/src", "/root"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("want %q, got %q", want, got)
		}
	})

	t.Run("BacktickCharRangeDocumentsParserGap", func(t *testing.T) {
		word := &syntax.Word{Parts: []syntax.WordPart{lit("-{z..A}-")}}
		syntax.SplitBraces(word)
		var got []*syntax.Word
		for w, err := range BracesSeq(nil, word) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, w)
		}
		if len(got) != int('z'-'A'+1) {
			t.Fatalf("expected z..A engine expansion, got %d words", len(got))
		}
	})
}
