package owner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/health"
	deckhome "deck2/internal/home"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/tests/harness"
)

func TestQueryOutsideTheTransactionFailsLoudly(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	var recovered any
	// The store itself: the shared queries inside a transaction.
	_ = e.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		defer func() { recovered = recover() }()
		_, _ = e.App.Store.Q.ListProjects(ctx)
		return nil
	})
	if recovered == nil || !strings.Contains(recovered.(string), "inside a transaction") {
		t.Fatalf("a query through Store.Q inside a transaction must panic, got %v", recovered)
	}
}

func TestOtherWorkspacesAreInvisible(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("mine", harness.IdeasManifest)
	// A second workspace with its projects, one named as ours; deck makes no workspace but the owner's.
	e.SQL(`INSERT INTO workspace (id, name) VALUES (2, 'other');
		INSERT INTO project (workspace_id, name, status) VALUES (2, 'theirs', 'enabled'), (2, 'mine', 'enabled')`)
	var projects []struct{ Name string }
	e.ProjectsOf(&projects)
	if len(projects) != 1 || projects[0].Name != "mine" {
		t.Fatalf("projects of another workspace must not be listed: %+v", projects)
	}
	if code, _ := e.Call("deck.manifest_get", map[string]any{"project": "theirs"}); code != 404 {
		t.Fatalf("a project of another workspace must look missing, got %d", code)
	}
	if code, _ := e.Call("theirs.collect", nil); code != 404 {
		t.Fatalf("commands of another workspace must look missing, got %d", code)
	}
	// A failing check of another workspace.
	e.SQL(`INSERT INTO health (workspace_id, subject, status, error) VALUES (2, 'command:theirs.collect', 'error', 'their secret path')`)
	for _, h := range e.Health() {
		if strings.Contains(h.Subject, "theirs") {
			t.Fatalf("health of another workspace's commands must not be listed: %+v", h)
		}
	}
	e.Project("theirs", harness.IdeasManifest)
	if code, out := e.Call("deck.manifest_get", map[string]any{"project": "theirs"}); code != 200 || !strings.Contains(string(out), "collect") {
		t.Fatalf("a name finds the project of the caller's workspace: %d %s", code, out)
	}
}

func TestAHomeWithAQuestionMarkOrAHashKeepsTheDatabaseInside(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "a?b#c%d")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	e.Project("mine", harness.IdeasManifest)
	if _, err := os.Stat(filepath.Join(deckhome.Layout(home).State(), "deck.db")); err != nil {
		t.Fatalf("the database lives in the home of deck: %v", err)
	}
}

// TestAWriteOutsideATransactionFailsLoudly: every write runs in Store.Tx; one through the shared queries panics.
func TestAWriteOutsideATransactionFailsLoudly(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	defer func() {
		if p := recover(); p == nil || !strings.Contains(fmt.Sprint(p), "outside a transaction") {
			t.Fatalf("a write through Store.Q panics: %v", p)
		}
	}()
	// The store itself: a write through the shared queries.
	_, _ = e.App.Store.Q.SetHealth(context.Background(), db.SetHealthParams{Subject: "x", Status: "ok"})
}

// TestADeckOfAnOlderLayoutDoesNotStart: a folder with deck's files outside state/ and work/ is refused, so nothing of
// it lies open next to the folders agents work in.
func TestADeckOfAnOlderLayoutDoesNotStart(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "deck.db"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.New(app.Config{Home: home}, &harness.Fake{}); err == nil || !strings.Contains(err.Error(), "older layout (deck.db)") {
		t.Fatalf("deck does not start on a folder of the older layout: %v", err)
	}
}

// TestAReadWhileATransactionIsOpenDoesNotWait: reads go past the one writer, so a long transaction (a snapshot,
// a manifest saved) holds no screen, and a read of another context inside a transaction does not freeze deck.
func TestAReadWhileATransactionIsOpenDoesNotWait(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("mine", harness.IdeasManifest)
	hold, held := make(chan struct{}), make(chan struct{})
	go func() {
		// The store itself: a transaction that stays open.
		_ = e.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
			close(held)
			<-hold
			return nil
		})
	}()
	<-held
	defer close(hold)
	done := make(chan int, 1)
	go func() {
		code, _ := e.Call("deck.projects_list", nil)
		done <- code
	}()
	if code := harness.Receive(t, "a read while a transaction is open", done); code != 200 {
		t.Fatalf("projects_list: %d", code)
	}
}

// TestATransactionOfAnotherContextInsideATransactionFailsInsteadOfFreezing: a write that waits for the base past
// its deadline is an error with the stack in the log, not deck frozen without a line.
func TestATransactionOfAnotherContextInsideATransactionFailsInsteadOfFreezing(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.App.Store.Wait = time.Second
	done := make(chan error, 1)
	go func() {
		// The store itself: a transaction inside a transaction.
		done <- e.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
			return e.App.Store.Tx(context.Background(), func(context.Context, *db.Queries) error { return nil })
		})
	}()
	if err := harness.Receive(t, "a transaction inside a transaction to fail", done); err == nil || !strings.Contains(err.Error(), "waited") {
		t.Fatalf("a nested transaction fails with why: %v", err)
	}
}

// TestATableWithoutATopicKeepsDeckFromStarting: a table of the base whose writes would publish nothing and that is
// not marked silent keeps deck from starting, so no new table goes unseen by the screens that follow it.
func TestATableWithoutATopicKeepsDeckFromStarting(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	// A table a newer deck would add without a topic.
	e.SQL(`CREATE TABLE stray (id INTEGER PRIMARY KEY, project_id INTEGER)`)
	_ = e.App.Close()
	if _, err := app.New(app.Config{Home: home}, &harness.Fake{}); err == nil || !strings.Contains(err.Error(), "table stray publishes no topic") {
		t.Fatalf("deck does not start with a table it does not know: %v", err)
	}
}

// TestNewLeavesNoServiceHalfBuilt: after New every service of deck has each dependency it names, so none is
// filled in later by hand.
func TestNewLeavesNoServiceHalfBuilt(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	app := reflect.ValueOf(e.App).Elem()
	var missing []string
	for i := range app.NumField() {
		f, name := app.Field(i), app.Type().Field(i).Name
		if !app.Type().Field(i).IsExported() {
			continue
		}
		if f.Kind() == reflect.Pointer && f.IsNil() {
			missing = append(missing, name)
			continue
		}
		if f.Kind() != reflect.Struct || !strings.HasPrefix(f.Type().PkgPath(), "deck2/internal/app/") {
			continue
		}
		s := f
		for j := range s.NumField() {
			sf := s.Type().Field(j)
			if !sf.IsExported() {
				continue
			}
			switch v := s.Field(j); v.Kind() {
			case reflect.Func, reflect.Pointer, reflect.Interface:
				if v.IsNil() {
					missing = append(missing, name+"."+sf.Name)
				}
			}
		}
	}
	if len(missing) > 0 {
		t.Fatalf("New leaves dependencies of services unset: %v", missing)
	}
}

// TestTheTimesDeckWritesAreOfItsClock: a note read, a failing check and its reading, and a revoked token carry the
// time of deck's clock, not that of the base.
func TestTheTimesDeckWritesAreOfItsClock(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock())
	at := store.Time(e.Clock.Add(time.Hour))
	// A note of the master for the owner to read.
	e.SQL(`INSERT INTO feed (workspace_id, project_id, kind, author, text, created_at)
		SELECT workspace_id, id, 'note', 'deck', 'by the clock', ? FROM project WHERE builtin = 1`, at)
	var note int64
	// The note just written.
	if err := e.SQLRow(`SELECT id FROM feed WHERE text = 'by the clock'`).Scan(&note); err != nil {
		t.Fatal(err)
	}
	e.Must("deck.feed_mark_read", map[string]any{"entry": note}, nil)
	if err := health.Write(t.Context(), e.App.Store, health.Of{}, "tool:web.fetch", health.Error, "the server exited: 1"); err != nil {
		t.Fatal(err)
	}
	e.Must("deck.health_mark_seen", map[string]any{"subject": "tool:web.fetch"}, nil)
	token, _ := e.Token("terminal", "deck.projects_list")
	e.Must("deck.token_revoke", map[string]any{"token": token}, nil)
	var read, updated, seen, revoked string
	// The times deck wrote.
	err := e.SQLRow(`SELECT (SELECT read_at FROM feed WHERE id = ?), (SELECT updated_at FROM health WHERE subject = 'tool:web.fetch'),
		(SELECT seen_at FROM health WHERE subject = 'tool:web.fetch'), (SELECT revoked_at FROM automation_token WHERE id = ?)`, note, token).
		Scan(&read, &updated, &seen, &revoked)
	if err != nil {
		t.Fatal(err)
	}
	if read != at || updated != at || seen != at || revoked != at {
		t.Fatalf("the times are of deck's clock %s: read %s, updated %s, seen %s, revoked %s", at, read, updated, seen, revoked)
	}
}

// TestTheMarksOfAnOlderDeckBeginWithTheirDomain: a mark an older deck kept comes through the update under the name of
// its domain, with its value and project.
func TestTheMarksOfAnOlderDeckBeginWithTheirDomain(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "deck.db")
	st, err := store.Open(path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	old := map[string]string{"moved:secrets": "secrets:moved", "moved:sessions": "providers:sessions_moved",
		"master_error:1.work.task_failed": "processes:master_error:1.work.task_failed", "screen_error:1.screen:main:b0@3": "processes:screen_error:1.screen:main:b0@3",
		"incident:7.running": "processes:incident:7.running", "manifest_problems:1": "project:manifest_problems:1",
		"push:2:ask": "feed:push:2:ask"}
	// The marks under the names of an older deck, and the base before the update.
	for _, q := range []string{`INSERT INTO project (workspace_id, name) VALUES (1, 'alpha')`, `DELETE FROM goose_db_version WHERE version_id >= 76`, `ALTER TABLE chat_topic DROP COLUMN lang; ALTER TABLE process RENAME COLUMN wait_until_ms TO pause_until_ms; ALTER TABLE process DROP COLUMN wait_reason; ALTER TABLE process DROP COLUMN wait_on; ALTER TABLE process DROP COLUMN wait_silence; DROP TABLE right_grant; ALTER TABLE project ADD COLUMN granted_revision INTEGER; ALTER TABLE mcp_tool ADD COLUMN approved TEXT NOT NULL DEFAULT ''`} {
		if _, err := st.DB.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for name := range old {
		// A mark of the project, its value its old name.
		if _, err := st.DB.Exec(`INSERT INTO mark (workspace_id, project_id, name, value) SELECT 1, id, ?, ? FROM project WHERE name = 'alpha'`, name, name); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()
	if st, err = store.Open(path, time.Now); err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// The marks after the update.
	rows, err := st.DB.Query(`SELECT m.name, m.value, j.name FROM mark m JOIN project j ON j.id = m.project_id WHERE m.workspace_id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var name, value, project string
		if err := rows.Scan(&name, &value, &project); err != nil {
			t.Fatal(err)
		}
		if project != "alpha" || old[value] != name {
			t.Fatalf("the mark %s keeps its value and project under the name of its domain: %s of %s", value, name, project)
		}
		got[name] = value
	}
	if len(got) != len(old) {
		t.Fatalf("every mark comes through: %v", got)
	}
}
