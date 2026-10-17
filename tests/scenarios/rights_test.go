package scenarios

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"deck2/internal/provider"
)

// studioModule has one agent command that edits the code of the module.
const studioModule = `{"version": 12,
  "writes": ["code/"],
  "commands": [
    {"name": "build", "title": "Build", "description": "Edits the code.", "class": "internal",
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Edit the code."}}]}`

// TestAnAgentWritesTheDocumentsItsManifestNames: an agent saves a document writes names and changes it without its
// kind, is refused another with what to add, knows both from its prompt, and a wider writes waits for the owner.
func TestAnAgentWritesTheDocumentsItsManifestNames(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	outs := map[string]string{}
	var prompt string
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		save := func(key string, args map[string]any) {
			out, bad := c.Tool(ctx, "document_save", args)
			if bad {
				out = "refused: " + out
			}
			mu.Lock()
			outs[key] = out
			mu.Unlock()
		}
		mu.Lock()
		prompt = c.Req.Prompt
		mu.Unlock()
		save("new", map[string]any{"name": "code/scene.jsx", "kind": "doc", "text": "export default 1"})
		save("again", map[string]any{"name": "code/scene.jsx", "old": []string{"1"}, "new": []string{"2"}})
		save("other", map[string]any{"name": "notes.md", "kind": "doc", "text": "hi"})
		save("unknown", map[string]any{"name": "code/none.jsx", "text": "x"})
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	e.createProject("studio", studioModule)
	e.waitState(e.start("studio", "build", nil), "done")
	mu.Lock()
	defer mu.Unlock()
	if strings.HasPrefix(outs["new"], "refused") || strings.HasPrefix(outs["again"], "refused") {
		t.Fatalf("the agent writes a document under writes and changes it without its kind: %v", outs)
	}
	if !strings.Contains(outs["other"], "refused") || !strings.Contains(outs["other"], "writes in its manifest") {
		t.Fatalf("another document is refused with what to add: %s", outs["other"])
	}
	if !strings.Contains(outs["unknown"], "give its kind to create it") {
		t.Fatalf("a new document without a kind is refused: %s", outs["unknown"])
	}
	if !strings.Contains(prompt, "these documents of the project: code/") || !strings.Contains(prompt, "do not start the work") {
		t.Fatalf("the agent knows what it may write before it works: %s", prompt)
	}
	if !strings.Contains(prompt, "do not call deck's HTTP API") {
		t.Fatalf("the agent is told to reach deck's data through its tools: %s", prompt)
	}
	var doc struct{ Text string }
	e.must("deck.document_get", map[string]any{"project": "studio", "name": "code/scene.jsx"}, &doc)
	if doc.Text != "export default 2" {
		t.Fatalf("the change is saved: %q", doc.Text)
	}
	var feed []struct{ Text string }
	e.must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if !slices.ContainsFunc(feed, func(f struct{ Text string }) bool { return strings.Contains(f.Text, "changed document code/scene.jsx") }) {
		t.Fatalf("the owner sees the change with an undo: %+v", feed)
	}

	_, tok := e.token("terminal", "deck.*")
	var w struct {
		Waiting int64
		Widens  []string
	}
	wider := strings.Replace(studioModule, `"writes": ["code/"]`, `"writes": ["code/", "personas/style.md"]`, 1)
	code, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "studio", "manifest": wider})
	if code != 200 || json.Unmarshal(out, &w) != nil || w.Waiting == 0 || !slices.Equal(w.Widens, []string{"writes document personas/style.md"}) {
		t.Fatalf("a wider writes waits for the owner's button: %d %s", code, out)
	}
	narrower := strings.Replace(studioModule, `"writes": ["code/"]`, `"writes": ["code/scene.jsx"]`, 1)
	if code, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "studio", "manifest": narrower}); code != 200 || strings.Contains(string(out), "waiting") {
		t.Fatalf("a narrower writes needs no button: %d %s", code, out)
	}
}

// TestAnAgentDeletesTheRecordsOfItsOwnProject: an agent of a module deletes a record of its project, which the owner
// may undo, and not a record of another project.
func TestAnAgentDeletesTheRecordsOfItsOwnProject(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	outs := map[string]string{}
	var own, other int64
	e := clipsEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "tally" {
			for key, id := range map[string]int64{"own": own, "other": other} {
				out, bad := c.Tool(ctx, "record_delete", map[string]any{"record": id})
				if bad {
					out = "refused: " + out
				}
				mu.Lock()
				outs[key] = out
				mu.Unlock()
			}
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	e.createProject("reels", clipsModule)
	own = e.clip("a")
	e.must("deck.records_upsert", map[string]any{"project": "reels", "collection": "clips", "records": jsonl(map[string]any{"slug": "b"})}, nil)
	var reels []struct{ ID int64 }
	e.must("deck.records_list", map[string]any{"project": "reels", "collection": "clips"}, &reels)
	other = reels[0].ID
	e.waitState(e.start("clips", "tally", nil), "done")
	mu.Lock()
	defer mu.Unlock()
	if strings.HasPrefix(outs["own"], "refused") {
		t.Fatalf("an agent deletes a record of its own project: %s", outs["own"])
	}
	if !strings.HasPrefix(outs["other"], "refused") {
		t.Fatalf("an agent does not delete a record of another project: %s", outs["other"])
	}
	var feed []struct {
		Text    string
		Actions []map[string]any
	}
	e.must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) == 0 || !strings.Contains(feed[0].Text, "deleted") {
		t.Fatalf("the owner sees the deletion with an undo: %+v", feed)
	}
}

const probeModule = `{"version": 12,
  "commands": [{"name": "probe", "title": "Probe", "description": "Reads settings.", "class": "internal",
    "result": {"type": "object"}, "executor": {"type": "script", "script": "probe"}}],
  "scripts": [{"name": "probe", "file": "scripts/probe.py", "runtime": "python"}]}`

// probeScript calls a command of the owner side and fails on the bare status, as a script that reads no body does.
const probeScript = `import json, os, urllib.request
req = urllib.request.Request(os.environ["DECK_URL"] + "/commands/deck.settings_list", data=b"{}",
    headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"})
urllib.request.urlopen(req, timeout=30)
print("{}")
`

// TestAScriptFailedByARefusalSaysWhatDeckRefused: a script that dies on a bare 403 fails with what deck refused and
// who may call it, as a failure of rights.
func TestAScriptFailedByARefusalSaysWhatDeckRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("probe", probeModule)
	e.saveScript("probe", "scripts/probe.py", probeScript)
	id := e.start("probe", "probe", nil)
	e.waitScript(id, "done", "error")
	f := e.fault(id)
	if f.ErrorType != "rights.denied" || !strings.Contains(f.Error, "deck refused its calls: deck.settings_list") ||
		!strings.Contains(f.Error, "cannot be called by agent (script); only owner, master call it") {
		t.Fatalf("the failure names the refusal: %+v", f)
	}
}
