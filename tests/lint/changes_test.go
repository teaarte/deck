package lint

import (
	"fmt"
	"os"
	"strings"
	"testing"

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
