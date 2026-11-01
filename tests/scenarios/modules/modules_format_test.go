package modules

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"deck2/internal/manifest"
	"deck2/tests/harness"
)

// TestAStoredManifestOfFormat14ReadsInFormat15: the network of a module is only its rights in format 15, so a stored
// manifest of format 14 loses no_network and local_browser; a module whose agents now reach its domains says so once.
func TestAStoredManifestOfFormat14ReadsInFormat15(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithHome(t.TempDir()))
	old := func(requires string) string {
		m := harness.ManifestVersion(harness.IdeasManifest, "14")
		m = strings.Replace(m, `"version": 14`, `"version": 14, "no_network": true`, 1)
		if strings.Contains(requires, "images") {
			m = strings.Replace(m, `"version": 14`, `"version": 14, "domains": {"read": ["data.example.org"]}`, 1)
		}
		return strings.Replace(m, `"class": "internal",`, `"class": "internal", "requires": `+requires+`,`, 1)
	}
	for name, requires := range map[string]string{"ideas": `["images", "local_browser"]`, "notes": `["local_browser"]`} {
		e.Project(name, harness.IdeasManifest)
		// The manifest of format 14 an older deck kept.
		e.PlantRevision(name, "manifest.json", manifest.Canonical(old(requires)))
	}
	// A base an older deck left: the manifests of format 14.
	e.SQL(`ALTER TABLE process DROP COLUMN submitted_at`)
	e.SQL(`DELETE FROM goose_db_version WHERE version_id >= 56`)

	e = e.Restart()
	for name, want := range map[string][]string{"ideas": {"images"}, "notes": nil} {
		var doc struct{ Text string }
		e.Must("deck.document_get", map[string]any{"project": name, "name": "manifest.json"}, &doc)
		var m struct {
			Version   int             `json:"version"`
			NoNetwork json.RawMessage `json:"no_network"`
			Commands  []struct {
				Name     string   `json:"name"`
				Requires []string `json:"requires"`
			} `json:"commands"`
		}
		if err := json.Unmarshal([]byte(doc.Text), &m); err != nil || m.Version != manifest.FormatVersion || m.NoNetwork != nil ||
			len(m.Commands) != 1 || fmt.Sprint(m.Commands[0].Requires) != fmt.Sprint(want) || strings.Contains(doc.Text, "local_browser") {
			t.Fatalf("%s: the manifest is of format 15 without no_network and local_browser: %v %s", name, err, doc.Text)
		}
		if doc.Text != manifest.Canonical(doc.Text) || !strings.Contains(doc.Text, "\n  \"collections\"") {
			t.Fatalf("%s: the manifest keeps the one stored form and its order: %s", name, doc.Text)
		}
	}
	e.Must("deck.manifest_check", map[string]any{"project": "ideas"}, nil)
	var feed []struct{ Text string }
	e.Must("deck.feed_list", map[string]any{"kind": "note"}, &feed)
	told := 0
	for _, f := range feed {
		if strings.Contains(f.Text, "had no_network") {
			told++
			if !strings.Contains(f.Text, "ideas") || !strings.Contains(f.Text, "data.example.org") {
				t.Fatalf("the module whose agents now reach its domains is told: %s", f.Text)
			}
		}
	}
	if told != 1 {
		t.Fatalf("one module had no_network with domains, told %d times: %+v", told, feed)
	}
}
