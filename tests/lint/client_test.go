package lint

import (
	"strings"
	"testing"

	"deck2/internal/lint"
)

// TestTheClientHasOneTruth: the client calls commands only through its live read, the screen and the press, and its
// words are deck's dictionary by their English text.
func TestTheClientHasOneTruth(t *testing.T) {
	vs, err := lint.Client("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		t.Error(v.String())
	}
}

func TestTheClientCheckCatchesItsOwnWays(t *testing.T) {
	vs, err := lint.Client("testdata/client")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range vs {
		got = append(got, v.String())
	}
	want := []string{
		"web/src/blocks/a.tsx:1: a command called outside the live read, the screen and the press: read it live or press a button of the server",
		"web/src/blocks/a.tsx:2: words of the client's own: deck's are in internal/words",
		"web/src/blocks/a.tsx:3: a key of words that is not its English text",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
