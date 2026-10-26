package lint

import (
	"strings"
	"testing"

	"deck2/internal/lint"
)

// TestTheClientTakesItsLookFromTheTheme: the client has no color, radius or radius="…" of its own outside the
// theme's files; a theme changes every part of it.
func TestTheClientTakesItsLookFromTheTheme(t *testing.T) {
	vs, err := lint.Unthemed("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		t.Error(v.String())
	}
}

func TestTheUnthemedCheckCatchesValues(t *testing.T) {
	vs, err := lint.Unthemed("testdata/unthemed")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range vs {
		got = append(got, v.String())
	}
	want := []string{
		"web/src/app/a.module.css:2: a border-radius of its own, not of the theme",
		"web/src/app/a.module.css:5: a color of its own, not of the theme",
		"web/src/app/a.module.css:6: a color of its own, not of the theme",
		"web/src/app/b.tsx:1: a radius of its own, not of the theme",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
