package ownedexec

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestFrameRoundTripAndRejectsCorruption(t *testing.T) {
	want := Frame{Args: []string{"-printf", "", "🙂", strings.Repeat("x", 200000)}, Env: []string{"EMPTY=", "UNICODE=🙂"}}
	var b bytes.Buffer
	if err := Write(&b, want); err != nil {
		t.Fatal(err)
	}
	got, err := Read(&b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("frame changed: %#v", got)
	}
	for _, data := range [][]byte{nil, []byte("BASHYAE1"), []byte("NOTFRAME" + strings.Repeat("x", 20))} {
		if _, err := Read(bytes.NewReader(data)); err == nil {
			t.Fatalf("accepted damaged frame %q", data)
		}
	}
}
