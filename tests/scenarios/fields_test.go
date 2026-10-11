package scenarios

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	deckhome "deck2/internal/home"
	"deck2/internal/registry"
)

func keysOf(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// TestAReadGivesOnlyTheFieldsAsked: a read with fields gives only those, of every row of a list, of the rows of
// the one list of an object, or of the object; without fields it gives all; records_list cuts the data of records.
func TestAReadGivesOnlyTheFieldsAsked(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	p := e.createProject("source", sourceManifest)
	e.must("deck.records_upsert", map[string]any{"project": "source", "collection": "items",
		"records": "{\"name\": \"a\", \"size\": 3}\n{\"name\": \"b\", \"size\": 5}"}, nil)

	var rows []map[string]any
	e.must("deck.records_list", map[string]any{"project": "source", "collection": "items", "fields": []string{"size", "nope"}}, &rows)
	if len(rows) != 2 || rows[0]["key"] != "a" || !slices.Equal(keysOf(rows[0]["data"].(map[string]any)), []string{"size"}) {
		t.Fatalf("records_list keeps its own fields, those of the data of each record: %v", rows)
	}
	e.must("deck.records_list", map[string]any{"project": "source", "collection": "items"}, &rows)
	if len(rows) != 2 || len(rows[0]) < 5 || len(rows[0]["data"].(map[string]any)) != 2 {
		t.Fatalf("records_list without fields gives whole rows: %v", rows)
	}

	master := registry.Actor{Kind: registry.Master, Workspace: 1}
	out, err := e.App.Registry.Invoke(t.Context(), master, "deck.projects_list", json.RawMessage(`{"fields": ["name"]}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	var projects []map[string]any
	if json.Unmarshal(raw, &projects) != nil || len(projects) < 2 || !slices.Equal(keysOf(projects[0]), []string{"name"}) {
		t.Fatalf("the master gets only the names of projects: %s", raw)
	}

	var m map[string]any
	e.must("deck.manifest_get", map[string]any{"project": "source", "fields": []string{"project", "revision"}}, &m)
	if !slices.Equal(keysOf(m), []string{"project", "revision"}) || m["project"] != "source" {
		t.Fatalf("manifest_get cuts the keys of its object: %v", m)
	}

	data := deckhome.Layout(e.App.Cfg.Home).Data(p)
	_ = os.MkdirAll(filepath.Join(data, "cache"), 0o700)
	_ = os.WriteFile(filepath.Join(data, "state.json"), []byte(`{}`), 0o600)
	e.must("deck.data_read", map[string]any{"project": "source", "fields": []string{"name"}}, &m)
	entries, _ := m["entries"].([]any)
	if len(entries) != 2 || !slices.Equal(keysOf(entries[0].(map[string]any)), []string{"name"}) || m["path"] == nil {
		t.Fatalf("data_read cuts the rows of its entries and keeps the rest: %v", m)
	}
	m = nil
	e.must("deck.data_read", map[string]any{"project": "source", "fields": []string{"name", "path"}}, &m)
	entries, _ = m["entries"].([]any)
	if len(entries) != 2 || !slices.Equal(keysOf(entries[0].(map[string]any)), []string{"name", "path"}) {
		t.Fatalf("a name of both the rows and the object cuts the rows: %v", m)
	}
	m = nil
	e.must("deck.data_read", map[string]any{"project": "source", "fields": []string{"path", "entries.name"}}, &m)
	entries, _ = m["entries"].([]any)
	if !slices.Equal(keysOf(m), []string{"entries", "path"}) || len(entries) != 2 || !slices.Equal(keysOf(entries[0].(map[string]any)), []string{"name"}) {
		t.Fatalf("a dotted name reaches into a field: %v", m)
	}

	var d struct {
		Input struct {
			Properties map[string]struct {
				Type  string
				Items struct{ Type string }
			}
			Required []string
		}
	}
	e.must("deck.command_describe", map[string]any{"command": "deck.health_list"}, &d)
	if f := d.Input.Properties["fields"]; f.Type != "array" || f.Items.Type != "string" || slices.Contains(d.Input.Required, "fields") {
		t.Fatalf("a read shows its optional fields: %+v", d.Input)
	}
	d.Input.Properties = nil
	e.must("deck.command_describe", map[string]any{"command": "deck.records_upsert"}, &d)
	if _, ok := d.Input.Properties["fields"]; ok {
		t.Fatalf("a command that writes takes no fields: %+v", d.Input)
	}
}
