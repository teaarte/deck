package scenarios

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/store/db"
)

// storedFormats are the formats the revisions of the manifest of a project are stored in.
func (e *Env) storedFormats(project string) []int {
	e.T.Helper()
	rows, err := e.App.Store.DB.Query(`SELECT r.text FROM document_revision r JOIN document d ON d.id = r.document_id
		JOIN project p ON p.id = d.project_id WHERE d.kind = 'manifest' AND p.name = ? ORDER BY r.n`, project)
	if err != nil {
		e.T.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []int
	for rows.Next() {
		var text string
		var v struct{ Version int }
		if err := rows.Scan(&text); err != nil || json.Unmarshal([]byte(text), &v) != nil {
			e.T.Fatalf("a stored manifest: %v %s", err, text)
		}
		out = append(out, v.Version)
	}
	return out
}

// TestDeckStoresTheManifestsOfAnOlderFormatRaisedOnce: a revision an older deck stored in an older format is
// stored in the current one when deck starts, and the marks of read health go with it.
func TestDeckStoresTheManifestsOfAnOlderFormatRaisedOnce(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := newEnvWith(t, home, &Fake{})
	p := e.createProject("ideas", ideasManifest)
	older := []byte(strings.Replace(ideasManifest, `"version": 13,`, `"version": 4,`, 1))
	err := e.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		_, _, _, err := docs.Save(ctx, q, docs.Ref{Scope: docs.Scope{Workspace: 1, Project: p}, Name: docs.ManifestName}, docs.KindManifest, string(older), "deck", "an older deck")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`INSERT INTO mark (workspace_id, name, value) VALUES (1, 'health-seen:command:ideas.x', '1')`,
		`DELETE FROM goose_db_version WHERE version_id = 43`} {
		if _, err := e.App.Store.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if got := e.storedFormats("ideas"); len(got) != 2 || got[1] != 4 {
		t.Fatalf("the older deck left format 4: %v", got)
	}
	_ = e.App.Close()
	e = newEnvWith(t, home, &Fake{})
	if got := e.storedFormats("ideas"); len(got) != 2 || got[0] != manifest.FormatVersion || got[1] != manifest.FormatVersion {
		t.Fatalf("every revision is stored in format %d: %v", manifest.FormatVersion, got)
	}
	var marks int
	if err := e.App.Store.DB.QueryRow(`SELECT count(*) FROM mark WHERE name GLOB 'health-seen:*'`).Scan(&marks); err != nil || marks != 0 {
		t.Fatalf("the marks of read health go: %d %v", marks, err)
	}
	var cur struct {
		Manifest string
		Warnings []string
	}
	e.must("deck.manifest_get", map[string]any{"project": "ideas"}, &cur)
	if len(cur.Warnings) != 0 {
		t.Fatalf("the manifest reads as it is stored: %v", cur.Warnings)
	}
}

// TestAModuleOfAnOlderFormatImportsInTheCurrentOne: a .zip a deck of before exported is stored raised.
func TestAModuleOfAnOlderFormatImportsInTheCurrentOne(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("ideas/manifest.json")
	_, _ = w.Write([]byte(strings.Replace(ideasManifest, `"version": 13,`, `"version": 4,`, 1)))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	id := e.upload("", "ideas.zip", "application/zip", buf.String())
	e.must("deck.project_import", map[string]any{"file": strconv.FormatInt(id, 10), "name": "ideas"}, nil)
	if got := e.storedFormats("ideas"); len(got) != 1 || got[0] != manifest.FormatVersion {
		t.Fatalf("the import stores format %d: %v", manifest.FormatVersion, got)
	}
}
