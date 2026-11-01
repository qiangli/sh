package syntax

import (
	"errors"
	"strings"
	"testing"
)

func TestCommandWordObserverBoundaries(t *testing.T) {
	src := "# guard\n'guard'; \\guard; printf guard; x=guard; cat <<'END'\nguard\nEND\nx=$(guard); `guard`; f() { guard; }; >out guard"
	var positions []int
	p := NewParser(CommandWordObserver(func(word string, pos Pos) error {
		if word == "guard" {
			positions = append(positions, int(pos.Offset()))
		}
		return nil
	}))
	if _, err := p.Parse(strings.NewReader(src), ""); err != nil {
		t.Fatal(err)
	}
	// The observer can revisit a command, but it must never report data words.
	want1, want2 := strings.Index(src, "guard; };"), strings.LastIndex(src, "guard")
	seen1, seen2 := false, false
	for _, pos := range positions {
		switch pos {
		case want1:
			seen1 = true
		case want2:
			seen2 = true
		default:
			t.Fatalf("observed non-command guard at %d", pos)
		}
	}
	if !seen1 || !seen2 {
		t.Fatalf("missing command positions: %v", positions)
	}
}

func TestCommandWordObserverStopsBeforeGrammarError(t *testing.T) {
	sentinel := errors.New("expand command")
	p := NewParser(CommandWordObserver(func(word string, _ Pos) error {
		if word == "forever" {
			return sentinel
		}
		return nil
	}))
	if _, err := p.Parse(strings.NewReader("f() { forever do :; done; }"), ""); err != sentinel {
		t.Fatalf("got %v, want observer error", err)
	}
	CommandWordObserver(nil)(p)
	if _, err := p.Parse(strings.NewReader("f() { forever do :; done; }"), ""); err == nil {
		t.Fatal("ordinary parser unexpectedly accepts alias grammar")
	}
}
