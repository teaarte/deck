package scenarios

import (
	"context"
	"deck2/internal/app"
	deckhome "deck2/internal/home"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deck2/internal/store"
	"deck2/internal/store/db"
)

func TestQueryOutsideTheTransactionFailsLoudly(t *testing.T) {
	e := newEnv(t, nil)
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
	e := newEnv(t, nil)
	e.createProject("mine", ideasManifest)
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
	e.projectsOf(&projects)
	if len(projects) != 1 || projects[0].Name != "mine" {
		t.Fatalf("projects of another workspace must not be listed: %+v", projects)
	}
	if code, _ := e.call("deck.manifest_get", map[string]any{"project": "theirs"}); code != 404 {
		t.Fatalf("a project of another workspace must look missing, got %d", code)
	}
	if code, _ := e.call("theirs.collect", nil); code != 404 {
		t.Fatalf("commands of another workspace must look missing, got %d", code)
	}
	err := e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.SetHealth(ctx, db.SetHealthParams{WorkspaceID: store.ID(2), Subject: "command:theirs.collect", Status: "error", Error: "their secret path"})
	})
	if err != nil {
		t.Fatal(err)
	}
	var hs []healthRow
	e.must("deck.health_list", nil, &hs)
	for _, h := range hs {
		if strings.Contains(h.Subject, "theirs") {
			t.Fatalf("health of another workspace's commands must not be listed: %+v", h)
		}
	}
	e.createProject("theirs", ideasManifest)
	if code, out := e.call("deck.manifest_get", map[string]any{"project": "theirs"}); code != 200 || !strings.Contains(string(out), "collect") {
		t.Fatalf("a name finds the project of the caller's workspace: %d %s", code, out)
	}
}

func TestCoreDocumentNamesKeepTheirKind(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("ideas", ideasManifest)
	for _, name := range []string{"prompt.md", "manifest.json"} {
		if code, _ := e.call("deck.document_save", map[string]any{"project": "ideas", "name": name, "kind": "artifact", "text": "Injected"}); code != 422 {
			t.Errorf("%s must be reserved for its kind, got %d", name, code)
		}
	}
	e.must("deck.document_save", map[string]any{"project": "ideas", "name": "prompt.md", "kind": "prompt", "text": "Real prompt"}, nil)
}

func TestAHomeWithAQuestionMarkOrAHashKeepsTheDatabaseInside(t *testing.T) {
	home := filepath.Join(t.TempDir(), "a?b#c%d")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	e := newEnvWith(t, home, &Fake{})
	e.createProject("mine", ideasManifest)
	if _, err := os.Stat(filepath.Join(deckhome.Layout(home).State(), "deck.db")); err != nil {
		t.Fatalf("the database lives in the home of deck: %v", err)
	}
}

// TestAWriteOutsideATransactionFailsLoudly: every write runs in Store.Tx; one through the shared queries panics.
func TestAWriteOutsideATransactionFailsLoudly(t *testing.T) {
	e := newEnv(t, nil)
	defer func() {
		if p := recover(); p == nil || !strings.Contains(fmt.Sprint(p), "outside a transaction") {
			t.Fatalf("a write through Store.Q panics: %v", p)
		}
	}()
	_ = e.App.Store.Q.SetHealth(context.Background(), db.SetHealthParams{Subject: "x", Status: "ok"})
}

// TestDeletingAProjectKeepsTheHealthOfItsNamesakeElsewhere: health rows are keyed by workspace and project, so a
// project of the same name in another workspace keeps its own.
func TestDeletingAProjectKeepsTheHealthOfItsNamesakeElsewhere(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	mine := e.createProject("x", plainManifest)
	if _, err := e.App.Store.DB.ExecContext(ctx, `INSERT INTO workspace (id, name) VALUES (2, 'other')`); err != nil {
		t.Fatal(err)
	}
	err := e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		theirs, err := q.CreateProject(ctx, db.CreateProjectParams{WorkspaceID: 2, Name: "x", Status: "enabled"})
		if err != nil {
			return err
		}
		for _, h := range []db.SetHealthParams{{WorkspaceID: store.ID(1), ProjectID: store.ID(mine), Subject: "command:x.run", Status: "error"},
			{WorkspaceID: store.ID(2), ProjectID: store.ID(theirs.ID), Subject: "command:x.run", Status: "error"}} {
			if err := q.SetHealth(ctx, h); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	e.must("deck.project_delete", map[string]any{"project": "x"}, nil)
	var n int
	_ = e.App.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM health WHERE subject = 'command:x.run' AND workspace_id = 2`).Scan(&n)
	if n != 1 {
		t.Fatalf("the namesake in the other workspace keeps its health: %d", n)
	}
}

// TestADeckOfAnOlderLayoutDoesNotStart: a folder with deck's files outside state/ and work/ is refused, so nothing of
// it lies open next to the folders agents work in.
func TestADeckOfAnOlderLayoutDoesNotStart(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "deck.db"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.New(app.Config{Home: home}, &Fake{}); err == nil || !strings.Contains(err.Error(), "older layout (deck.db)") {
		t.Fatalf("deck does not start on a folder of the older layout: %v", err)
	}
}

// TestAReadWhileATransactionIsOpenDoesNotWait: reads go past the one writer, so a long transaction (a snapshot,
// a manifest saved) holds no screen, and a read of another context inside a transaction does not freeze deck.
func TestAReadWhileATransactionIsOpenDoesNotWait(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("mine", ideasManifest)
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
		code, _ := e.call("deck.projects_list", nil)
		done <- code
	}()
	select {
	case code := <-done:
		if code != 200 {
			t.Fatalf("projects_list: %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a read waits for an open transaction")
	}
}

// TestATransactionOfAnotherContextInsideATransactionFailsInsteadOfFreezing: a write that waits for the base past
// its deadline is an error with the stack in the log, not deck frozen without a line.
func TestATransactionOfAnotherContextInsideATransactionFailsInsteadOfFreezing(t *testing.T) {
	e := newEnv(t, nil)
	e.App.Store.Wait = time.Second
	done := make(chan error, 1)
	go func() {
		done <- e.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
			return e.App.Store.Tx(context.Background(), func(context.Context, *db.Queries) error { return nil })
		})
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "waited") {
			t.Fatalf("a nested transaction fails with why: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a transaction inside a transaction freezes deck")
	}
}
