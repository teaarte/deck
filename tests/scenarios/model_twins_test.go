package scenarios

import (
	"path/filepath"
	"testing"

	"deck2/internal/store"
)

// TestTwinsOfAModelMergeIntoTheFirst: models of one connection with the same API name, kind and options become the
// first of them on upgrade, which takes their agent, assignments, chat topics, processes and sessions.
func TestTwinsOfAModelMergeIntoTheFirst(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "deck.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO connection (workspace_id, name, url) VALUES (1, 'google', 'http://127.0.0.1:9')`,
		`INSERT INTO model (workspace_id, connection_id, name, model, options) SELECT 1, id, 'google/flash', 'flash', '{}' FROM connection WHERE name = 'google'`,
		`INSERT INTO model (workspace_id, connection_id, name, model, options, agent) SELECT 1, id, 'google-flash', 'flash', '{ }', 'goose' FROM connection WHERE name = 'google'`,
		`INSERT INTO model (workspace_id, connection_id, name, model, options) SELECT 1, id, 'flash-ru', 'flash', '{"language": "ru"}' FROM connection WHERE name = 'google'`,
		`INSERT INTO assignment (workspace_id, role, models) VALUES (1, 'agent', '["google-flash", "flash-ru", "google/flash"]')`,
		`INSERT INTO chat_topic (workspace_id, title, model) VALUES (1, 'talk', 'google-flash')`,
		`INSERT INTO chat_session (topic_id, provider, session_id) SELECT id, 'google-flash', 's1' FROM chat_topic`,
		`INSERT INTO project (workspace_id, name) VALUES (1, 'work')`,
		`INSERT INTO process (project_id, command, state, provider) SELECT id, 'run', 'paused', 'google-flash' FROM project WHERE name = 'work'`,
		`DELETE FROM goose_db_version WHERE version_id >= 46`,
	} {
		if _, err := st.DB.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	st.Close()
	if st, err = store.Open(path); err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var names, agent, models, topic, session, provider string
	for _, r := range []struct {
		q   string
		out *string
	}{
		{`SELECT group_concat(name, ',') FROM (SELECT name FROM model WHERE model = 'flash' ORDER BY id)`, &names},
		{`SELECT agent FROM model WHERE name = 'google/flash'`, &agent},
		{`SELECT models FROM assignment WHERE role = 'agent'`, &models},
		{`SELECT model FROM chat_topic`, &topic},
		{`SELECT provider FROM chat_session`, &session},
		{`SELECT provider FROM process`, &provider},
	} {
		if err := st.DB.QueryRow(r.q).Scan(r.out); err != nil {
			t.Fatalf("%s: %v", r.q, err)
		}
	}
	if names != "google/flash,flash-ru" || agent != "goose" || models != `["google/flash","flash-ru"]` ||
		topic != "google/flash" || session != "google/flash" || provider != "google/flash" {
		t.Fatalf("a twin merges into the first, one with its own options stays: %s %s %s %s %s %s", names, agent, models, topic, session, provider)
	}
}
