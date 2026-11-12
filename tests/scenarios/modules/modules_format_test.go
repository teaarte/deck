package modules

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"deck2/internal/manifest"
	"deck2/tests/harness"
)

// TestAStoredManifestOfFormat14ReadsInFormat15: a stored manifest of format 14 loses no_network and local_browser; a
// module whose agents on a login no longer reach its domains, as format 17 has it, says so once.
func TestAStoredManifestOfFormat14ReadsInFormat15(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithHome(t.TempDir()))
	old := func(requires string) string {
		m := harness.ManifestVersion(harness.IdeasManifest, "14")
		m = strings.Replace(m, `"version": 14`, `"version": 14, "no_network": true`, 1)
		if strings.Contains(requires, "images") {
			m = strings.Replace(m, `"version": 14`, `"version": 14, "domains": {"read": ["data.example.org"]}`, 1)
		}
		return strings.Replace(m, `"executor": {`, `"requires": `+requires+`, "executor": {`, 1)
	}
	for name, requires := range map[string]string{"ideas": `["images", "local_browser"]`, "notes": `["local_browser"]`} {
		e.Project(name, harness.IdeasManifest)
		// The manifest of format 14 an older deck kept.
		e.PlantRevision(name, "manifest.json", manifest.Canonical(old(requires)))
	}
	// A base an older deck left: the manifests of format 14.
	e.SQL(`DELETE FROM goose_db_version WHERE version_id >= 56`)
	// Nor kept its models the module that added them.
	e.SQL(`ALTER TABLE model DROP COLUMN project_id`)
	// Nor had its messages of the master blocks.
	e.SQL(`ALTER TABLE chat_message DROP COLUMN block; ALTER TABLE chat_message DROP COLUMN answer; ALTER TABLE chat_message DROP COLUMN answered_at; ALTER TABLE model DROP COLUMN tools; ALTER TABLE chat_topic DROP COLUMN lang; ALTER TABLE process RENAME COLUMN wait_until_ms TO pause_until_ms; ALTER TABLE process DROP COLUMN wait_reason; ALTER TABLE process DROP COLUMN wait_on; ALTER TABLE process DROP COLUMN wait_silence; DROP TABLE right_grant; ALTER TABLE project ADD COLUMN granted_revision INTEGER; ALTER TABLE mcp_tool ADD COLUMN approved TEXT NOT NULL DEFAULT ''`)
	// Nor had its connections a node.
	e.SQL(`ALTER TABLE connection DROP COLUMN node_id`)
	// Nor had it the devices of the owner.
	for _, q := range harness.BeforeDevices {
		e.SQL(q)
	}
	// Nor had it the column of a result handed in, which a later migration adds again.
	e.SQL(`ALTER TABLE process DROP COLUMN submitted_at`)

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
		if strings.Contains(f.Text, "no longer reach the network from their shells") {
			told++
			if !strings.Contains(f.Text, "The agents of ideas") || !strings.Contains(f.Text, "data.example.org") || strings.Contains(f.Text, "had no_network") {
				t.Fatalf("the module whose agents no longer reach its domains is told: %s", f.Text)
			}
		}
	}
	if told != 1 {
		t.Fatalf("one note tells the modules whose agents lost their network, told %d times: %+v", told, feed)
	}
}

// TestAStoredManifestOfFormat15ReadsInFormat16: a command runs one at a time in format 16 unless it says runs many,
// so a stored command without runs whose real runs went side by side gets runs many, and only such a one.
func TestAStoredManifestOfFormat15ReadsInFormat16(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithHome(t.TempDir()))
	one := strings.Replace(harness.AgentCommand("kept", ""), `"executor": {`, `"runs": "one", "executor": {`, 1)
	m := harness.TestManifest([]string{harness.AgentCommand("beside", ""), harness.AgentCommand("after", ""), one}, nil)
	p := e.Project("jobs", m)
	// The manifest of format 15 an older deck kept.
	e.PlantRevision("jobs", "manifest.json", manifest.Canonical(harness.ManifestVersion(m, "15")))
	// The runs an older deck kept: beside overlapped, after ran one after another, and only its trials overlapped.
	e.SQL(`INSERT INTO process (project_id, command, state, trial, started_at, finished_at) VALUES
		(?, 'beside', 'done', 0, '2026-10-01T10:00:00.000Z', '2026-10-01T10:30:00.000Z'),
		(?, 'beside', 'done', 0, '2026-10-01T10:10:00.000Z', '2026-10-01T10:20:00.000Z'),
		(?, 'after', 'done', 0, '2026-10-01T10:00:00.000Z', '2026-10-01T10:05:00.000Z'),
		(?, 'after', 'done', 0, '2026-10-01T10:06:00.000Z', '2026-10-01T10:07:00.000Z'),
		(?, 'after', 'done', 1, '2026-10-01T11:00:00.000Z', '2026-10-01T11:30:00.000Z'),
		(?, 'after', 'done', 1, '2026-10-01T11:10:00.000Z', '2026-10-01T11:20:00.000Z'),
		(?, 'kept', 'done', 0, '2026-10-01T10:00:00.000Z', '2026-10-01T10:30:00.000Z'),
		(?, 'kept', 'done', 0, '2026-10-01T10:10:00.000Z', '2026-10-01T10:40:00.000Z')`, p, p, p, p, p, p, p, p)
	// A base an older deck left: the manifests of format 15.
	e.SQL(`DELETE FROM goose_db_version WHERE version_id >= 58`)
	// Nor kept its models the module that added them.
	e.SQL(`ALTER TABLE model DROP COLUMN project_id`)
	// Nor had its messages of the master blocks.
	e.SQL(`ALTER TABLE chat_message DROP COLUMN block; ALTER TABLE chat_message DROP COLUMN answer; ALTER TABLE chat_message DROP COLUMN answered_at; ALTER TABLE model DROP COLUMN tools; ALTER TABLE chat_topic DROP COLUMN lang; ALTER TABLE process RENAME COLUMN wait_until_ms TO pause_until_ms; ALTER TABLE process DROP COLUMN wait_reason; ALTER TABLE process DROP COLUMN wait_on; ALTER TABLE process DROP COLUMN wait_silence; DROP TABLE right_grant; ALTER TABLE project ADD COLUMN granted_revision INTEGER; ALTER TABLE mcp_tool ADD COLUMN approved TEXT NOT NULL DEFAULT ''`)
	// Nor had its connections a node.
	e.SQL(`ALTER TABLE connection DROP COLUMN node_id`)
	// Nor had it the devices of the owner.
	for _, q := range harness.BeforeDevices {
		e.SQL(q)
	}

	e = e.Restart()
	var doc struct{ Text string }
	e.Must("deck.document_get", map[string]any{"project": "jobs", "name": "manifest.json"}, &doc)
	var got struct {
		Version  int `json:"version"`
		Commands []struct {
			Name string `json:"name"`
			Runs string `json:"runs"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(doc.Text), &got); err != nil || got.Version != manifest.FormatVersion || len(got.Commands) != 3 {
		t.Fatalf("the manifest is of format 16: %v %s", err, doc.Text)
	}
	runs := map[string]string{}
	for _, c := range got.Commands {
		runs[c.Name] = c.Runs
	}
	if runs["beside"] != "many" || runs["after"] != "" || runs["kept"] != "one" {
		t.Fatalf("only the command whose real runs went side by side runs many: %v", runs)
	}
	if doc.Text != manifest.Canonical(doc.Text) {
		t.Fatalf("the manifest keeps the one stored form: %s", doc.Text)
	}
	e.Must("deck.manifest_check", map[string]any{"project": "jobs"}, nil)
}
