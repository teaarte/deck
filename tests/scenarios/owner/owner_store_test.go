package owner

import (
	"context"
	"deck2/internal/app"
	deckhome "deck2/internal/home"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/tests/harness"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestQueryOutsideTheTransactionFailsLoudly(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	var recovered any
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
	ctx := context.Background()
	if _, err := e.App.Store.DB.ExecContext(ctx, `INSERT INTO workspace (id, name) VALUES (2, 'other')`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"theirs", "mine"} {
		if err := e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			_, err := q.CreateProject(ctx, db.CreateProjectParams{WorkspaceID: 2, Name: name, Status: "enabled"})
			return err
		}); err != nil {
			t.Fatalf("a project name is taken only in its own workspace: %v", err)
		}
	}
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
	err := e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.SetHealth(ctx, db.SetHealthParams{WorkspaceID: store.ID(2), Subject: "command:theirs.collect", Status: "error", Error: "their secret path"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
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
	if _, err := e.App.Store.DB.Exec(`CREATE TABLE stray (id INTEGER PRIMARY KEY, project_id INTEGER)`); err != nil {
		t.Fatal(err)
	}
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
