package harness

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// LightEnv is deck with a working week behind it: modules with many records and files, runs and a long chat.
func LightEnv(t *testing.T) *Env {
	t.Helper()
	e := New(t)
	for _, name := range []string{"notes", "clips", "jobs"} {
		p := e.Project(name, strings.Replace(WebNotes, `"menu": ["main"]}`, `"menu": ["main"]}`, 1))
		var recs []map[string]any
		for i := range 200 {
			recs = append(recs, map[string]any{"title": fmt.Sprintf("%s note %03d", name, i), "source": "web", "note": strings.Repeat("words ", 20)})
		}
		e.Must("deck.records_upsert", map[string]any{"project": name, "collection": "notes", "records": Jsonl(recs...)}, nil)
		for i := range 60 {
			e.Upload(name, fmt.Sprintf("file-%02d.txt", i), "text/plain", strings.Repeat("x", 100))
		}
		for i := range 20 {
			if _, err := e.App.Store.DB.Exec(`INSERT INTO process (project_id, command, kind, state, started_at, finished_at) VALUES (?, 'score', 'agent', ?, ?, ?)`,
				p, []string{"done", "error"}[i%2], time.Now().Add(-time.Duration(i)*time.Hour).UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
		}
	}
	topic := e.Say(0, "hello")
	e.MasterSaid(topic, 1)
	return e
}
