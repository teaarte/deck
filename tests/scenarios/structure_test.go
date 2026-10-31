package scenarios

import (
	"context"
	"deck2/internal/app"
	deckhome "deck2/internal/home"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/tests/harness"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
	e.Project("mine", ideasManifest)
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
	e.Project("theirs", ideasManifest)
	if code, out := e.Call("deck.manifest_get", map[string]any{"project": "theirs"}); code != 200 || !strings.Contains(string(out), "collect") {
		t.Fatalf("a name finds the project of the caller's workspace: %d %s", code, out)
	}
}

func TestCoreDocumentNamesKeepTheirKind(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("ideas", ideasManifest)
	for _, name := range []string{"prompt.md", "manifest.json"} {
		if code, _ := e.Call("deck.document_save", map[string]any{"project": "ideas", "name": name, "kind": "artifact", "text": "Injected"}); code != 422 {
			t.Errorf("%s must be reserved for its kind, got %d", name, code)
		}
	}
	e.Must("deck.document_save", map[string]any{"project": "ideas", "name": "prompt.md", "kind": "prompt", "text": "Real prompt"}, nil)
}

func TestAHomeWithAQuestionMarkOrAHashKeepsTheDatabaseInside(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "a?b#c%d")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	e.Project("mine", ideasManifest)
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

// TestDeletingAProjectKeepsTheHealthOfItsNamesakeElsewhere: health rows are keyed by workspace and project, so a
// project of the same name in another workspace keeps its own.
func TestDeletingAProjectKeepsTheHealthOfItsNamesakeElsewhere(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	ctx := context.Background()
	mine := e.Project("x", harness.PlainManifest)
	if _, err := e.App.Store.DB.ExecContext(ctx, `INSERT INTO workspace (id, name) VALUES (2, 'other')`); err != nil {
		t.Fatal(err)
	}
	err := e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		theirs, err := q.CreateProject(ctx, db.CreateProjectParams{WorkspaceID: 2, Name: "x", Status: "enabled"})
		if err != nil {
			return err
		}
		for _, h := range []db.SetHealthParams{{WorkspaceID: store.ID(1), ProjectID: store.ID(mine), Subject: "tool:x.get", Status: "error"},
			{WorkspaceID: store.ID(2), ProjectID: store.ID(theirs.ID), Subject: "tool:x.get", Status: "error"}} {
			if _, err := q.SetHealth(ctx, h); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	e.Must("deck.project_delete", map[string]any{"project": "x"}, nil)
	var n int
	_ = e.App.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM health WHERE subject = 'tool:x.get' AND workspace_id = 2`).Scan(&n)
	if n != 1 {
		t.Fatalf("the namesake in the other workspace keeps its health: %d", n)
	}
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
	e.Project("mine", ideasManifest)
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

// TestATokenInTheAddressOpensOnlyReads: the token in an address, which links and logs may carry, opens the page, its
// events and its files, but no command; a command takes the token in its header.
func TestATokenInTheAddressOpensOnlyReads(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	r, err := http.Post(e.URL+"/api/commands/deck.projects_list?token="+e.OwnerToken(), "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a command with the token only in its address is refused: %d", r.StatusCode)
	}
	g, err := http.Get(e.URL + "/api/events?token=" + e.OwnerToken())
	if err != nil {
		t.Fatal(err)
	}
	g.Body.Close()
	if g.StatusCode != http.StatusOK {
		t.Fatalf("the events of the page open with the token in the address: %d", g.StatusCode)
	}
}
