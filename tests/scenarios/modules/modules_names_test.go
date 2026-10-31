package modules_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestAModuleOfFormat13CallsDeckByTheNewNames: after the start its manifest, script and the owner's edit of the
// master's prompt name deck's commands anew, its agent calls them, and what deck ships merges with no conflict.
func TestAModuleOfFormat13CallsDeckByTheNewNames(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var told string
	var refused bool
	e := harness.New(t, harness.WithHome(t.TempDir()), harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		out, bad := c.Tool(ctx, "owner_notify", map[string]any{"text": "three ideas"})
		mu.Lock()
		told, refused = out, bad
		mu.Unlock()
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}))
	calls := func(name string) string {
		return strings.Replace(harness.IdeasManifest, `"prompt": "Write three ideas into the ideas collection."`,
			`"prompt": "Tell the owner.", "calls": ["deck.`+name+`", "deck.records_list"]`, 1)
	}
	p := e.Project("ideas", calls("owner_notify"))
	// The manifest of format 13 an older deck kept.
	e.PlantRevision("ideas", "manifest.json", harness.ManifestVersion(calls("notify"), "13"))
	// A script of before that calls deck by the old names.
	e.PlantRevision("ideas", "scripts/tell.py", `deck("notify", {"text": "x"}); deck("deck.process_log", {})`+"\n")
	const name = "prompts/on_error.md"
	raw, err := os.ReadFile(filepath.Join(harness.Root, "master", name))
	if err != nil {
		t.Fatal(err)
	}
	shipped := string(raw)
	if !strings.Contains(shipped, "`process_log_get`") || !strings.Contains(shipped, "`owner_notify`") {
		t.Fatal("the prompt of the master names no renamed command; the test edits them")
	}
	older := strings.NewReplacer("`process_log_get`", "`process_log`", "`owner_notify`", "`notify`").Replace(shipped)
	// The prompt an older deck shipped.
	e.PlantRevision("master", name, older)
	e.Must("deck.document_save", map[string]any{"project": "master", "name": name, "kind": "doc",
		"text": older + "\nThe owner's line names `notify` and deck.notify.\n"}, nil)
	// A base an older deck left: format 14 and its names not yet applied.
	e.SQL(`DELETE FROM goose_db_version WHERE version_id = 54`)

	e = e.Restart()
	var m struct{ Text string }
	e.Must("deck.document_get", map[string]any{"project": "ideas", "name": "manifest.json"}, &m)
	if !strings.Contains(m.Text, fmt.Sprintf(`"version": %d`, manifest.FormatVersion)) || !strings.Contains(m.Text, `"deck.owner_notify"`) || strings.Contains(m.Text, `"deck.notify"`) {
		t.Fatalf("the manifest is of format 14 and calls the new names: %.400s", m.Text)
	}
	var s struct{ Text string }
	e.Must("deck.document_get", map[string]any{"project": "ideas", "name": "scripts/tell.py"}, &s)
	if s.Text != `deck("owner_notify", {"text": "x"}); deck("deck.process_log_get", {})`+"\n" {
		t.Fatalf("the script calls the new names: %q", s.Text)
	}
	e.WaitState(e.Run(p, "collect"), "done")
	mu.Lock()
	defer mu.Unlock()
	if refused {
		t.Fatalf("the agent calls deck.owner_notify by its calls: %s", told)
	}
	var d struct{ Text string }
	e.Must("deck.document_get", map[string]any{"project": "master", "name": name}, &d)
	if d.Text != shipped+"\nThe owner's line names `owner_notify` and deck.owner_notify.\n" {
		t.Fatalf("the owner's edit names the new commands and merges with what deck ships: %.200q", d.Text)
	}
	var notes int
	// Notes to the master wait for its next turn; no read lists them.
	if err := e.SQLRow(`SELECT count(*) FROM feed WHERE for_master = 1 AND text LIKE '%' || ? || '%'`, name).Scan(&notes); err != nil || notes != 0 {
		t.Fatalf("a renamed command is no conflict for the master: %d notes, %v", notes, err)
	}
}
