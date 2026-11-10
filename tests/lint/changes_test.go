package lint

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"deck2/internal/lint"
	"deck2/internal/manifest"
)

// TestChangesNameTheFormat checks that master/changes.md, what the master reads after an update, tells of the
// current format of manifests: a new format comes with its line there.
func TestChangesNameTheFormat(t *testing.T) {
	b, err := os.ReadFile("../../master/changes.md")
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf(", format %d:", manifest.FormatVersion); !strings.Contains(string(b), want) {
		t.Fatalf("master/changes.md has no line of format %d (%q): write what changed for modules there",
			manifest.FormatVersion, want)
	}
}

// TestChangesGoInOrder: the master reads the lines after the last one it remembers, so no line is dated before the
// line above it.
func TestChangesGoInOrder(t *testing.T) {
	vs, err := lint.Changes("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		t.Error(v.String())
	}
}

func TestChangesCheckCatchesItsOwnWays(t *testing.T) {
	vs, err := lint.Changes("testdata/changes")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range vs {
		got = append(got, v.String())
	}
	want := []string{
		"master/changes.md:6: 2026-11-02 comes after 2026-11-03: a new line goes at the end with today's date",
		"master/changes.md:8: a line without its date: start it with - YYYY-MM-DD",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
