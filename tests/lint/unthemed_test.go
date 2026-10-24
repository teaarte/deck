package lint

import (
	"strings"
	"testing"

	"deck2/internal/lint"
)

// TestTheClientTakesItsLookFromTheTheme: colors, radii and radius="…" of the client outside the theme's files are
// only those the ratchet still lists, and its counts only go down.
func TestTheClientTakesItsLookFromTheTheme(t *testing.T) {
	vs, err := lint.Unthemed("../..")
	if err != nil {
		t.Fatal(err)
	}
	r, err := lint.ReadRatchet("unthemed.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.Against(vs) {
		t.Error(f)
	}
	for _, clean := range []string{"web/src/app/", "web/src/apps/"} {
		for f := range r {
			if strings.HasPrefix(f, clean) {
				t.Errorf("%s is to be clean, not on the ratchet", f)
			}
		}
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
	r := lint.Ratchet{"web/src/app/a.module.css": 4, "web/src/gone.tsx": 1}
	said := strings.Join(r.Against(vs), "\n")
	for _, s := range []string{"a.module.css: 3 unthemed values left, lower the ratchet from 4", "b.tsx: 1 unthemed values; take them from the theme", "gone.tsx: clean, take it off the ratchet"} {
		if !strings.Contains(said, s) {
			t.Errorf("the ratchet says %q in:\n%s", s, said)
		}
	}
}
