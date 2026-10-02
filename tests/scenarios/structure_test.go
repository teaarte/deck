package scenarios

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deck2/internal/docs"
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

func TestLegacyDescriptionBecomesThePrompt(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("ideas", ideasV1)
	var projects []struct{ Description string }
	e.projectsOf(&projects)
	var prompt struct{ Text string }
	e.must("deck.document_get", map[string]any{"project": "ideas", "name": "prompt.md"}, &prompt)
	if len(projects) != 1 || projects[0].Description != "Ideas of the day" || prompt.Text != "Ideas of the day" {
		t.Fatalf("the v1 description must move into the prompt: %+v %q", projects, prompt.Text)
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
		return q.SetHealth(ctx, db.SetHealthParams{Subject: "command:theirs.collect", Status: "error", Error: "their secret path"})
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

func TestStoredV2DescriptionMovesToThePromptOnStart(t *testing.T) {
	e := newEnv(t, nil)
	p := e.createProject("ideas", ideasManifest)
	v2 := strings.Replace(ideasManifest, `"version": 5,`, `"version": 2, "description": "Ideas of the day",`, 1)
	ctx := context.Background()
	err := e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, _, err := docs.Save(ctx, q, docs.Ref{Scope: docs.Scope{Workspace: 1, Project: p}, Name: docs.ManifestName}, docs.KindManifest, v2, "owner", "stored by an older deck")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.App.UpgradeManifests(ctx); err != nil {
		t.Fatal(err)
	}
	var projects []struct {
		Description   string
		ManifestError string `json:"manifest_error"`
	}
	e.projectsOf(&projects)
	if len(projects) != 1 || projects[0].ManifestError != "" || projects[0].Description != "Ideas of the day" {
		t.Fatalf("a v2 manifest with a description must upgrade and move it into the prompt: %+v", projects)
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
	if _, err := os.Stat(filepath.Join(home, "deck.db")); err != nil {
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
