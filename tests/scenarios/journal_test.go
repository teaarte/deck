package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deck2/internal/provider"
)

// TestJournalShowsTheCallsOfTheGatewayTheSpendFilesAndSnapshots: the journal screen reads the gateway journal
// with its outcomes, the tokens by command and provider, the files and the snapshots; it only shows them.
func TestJournalShowsTheCallsOfTheGatewayTheSpendFilesAndSnapshots(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "tell" {
			c.Tool(ctx, "web__publish", map[string]any{})
			c.Req.OnUsage(provider.Usage{RequestModel: "big", Model: "big", Input: 1200, Output: 300})
			c.Req.OnUsage(provider.Usage{RequestModel: "big", Model: "big", Input: 800, Output: 100})
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	check := newCatalogCheck(t)
	e.createProject("web", boardWebManifest)
	e.createProject("quiet", boardWebManifest)
	e.waitState(e.start("web", "tell", nil), "waiting_answer")
	scan := e.upload("web", "scan.txt", "text/plain", "a page")
	e.waitState(e.start("web", "score", map[string]any{"title": "x", "scan": fmt.Sprint(scan)}), "done")
	e.must("deck.snapshot_create", nil, nil)
	if err := os.WriteFile(filepath.Join(e.App.Cfg.Home, "backups", "deck-20200101T000000.tar.gz"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	var calls []struct {
		Tool, Actor, Project, Outcome string
	}
	e.must("deck.calls_list", map[string]any{"project": "web"}, &calls)
	waiting := false
	for _, c := range calls {
		waiting = waiting || c.Tool == "web.publish" && c.Outcome == "waiting" && c.Project == "web" && strings.HasPrefix(c.Actor, "agent:")
	}
	if !waiting {
		t.Fatalf("the journal has the external call that waits for the owner: %+v", calls)
	}
	e.answer(e.waitAsk().ID, "deny")
	e.must("deck.calls_list", map[string]any{"project": "web"}, &calls)
	outcomes := map[string]bool{}
	for _, c := range calls {
		outcomes[c.Outcome] = true
	}
	if !outcomes["rejected"] || !outcomes["waiting"] {
		t.Fatalf("the journal keeps each outcome of a call: %+v", calls)
	}
	var rejected []map[string]any
	e.must("deck.calls_list", map[string]any{"outcome": "rejected"}, &rejected)
	if len(rejected) != 1 || rejected[0]["outcome"] != "rejected" || rejected[0]["_actions"] != nil {
		t.Fatalf("the journal narrows by outcome and gives only its own fields: %+v", rejected)
	}
	var none []any
	e.must("deck.calls_list", map[string]any{"project": "quiet"}, &none)
	if len(none) != 0 {
		t.Fatalf("the journal of a project has only its calls: %+v", none)
	}

	msgs := e.screenMessages("", "journal", nil)
	check.messages(t, msgs)
	data := e.screenData("", "journal", nil)
	rows := blockData[[]screenRow](t, data["b0-0-0"]["rows"])
	if len(rows) == 0 || !strings.Contains(string(data["b0-0-0"]["filters"]), `"waiting"`) {
		t.Fatalf("the calls tab lists the calls with their outcomes to narrow by: %+v %s", rows, data["b0-0-0"]["filters"])
	}
	picked := e.screenData("", "journal", map[string]any{"filter": `{"b0-0-0": {"outcome": "waiting"}}`})["b0-0-0"]
	if len(blockData[[]screenRow](t, picked["rows"])) == 0 || len(blockData[[]screenRow](t, picked["rows"])) == len(rows) {
		t.Fatalf("the outcome filter narrows the calls: %d of %d", len(blockData[[]screenRow](t, picked["rows"])), len(rows))
	}
	for _, r := range blockData[[]screenRow](t, picked["rows"]) {
		if r.Cells[5] != "waiting" {
			t.Fatalf("the outcome filter keeps one outcome: %+v", r)
		}
		if verb(t, r.Actions, "open")["screen"] != "process" {
			t.Fatalf("a call opens its process: %+v", r.Actions)
		}
	}

	spend := blockData[[]screenRow](t, data["b0-1-0"]["rows"])
	if len(spend) != 1 || spend[0].Cells[1] != "tell" || spend[0].Cells[3] != "big" || spend[0].Cells[4] != float64(2) ||
		spend[0].Cells[5] != float64(2000) || spend[0].Cells[6] != float64(400) {
		t.Fatalf("spend sums the tokens of a command on a provider and model: %+v", spend)
	}
	files := blockData[[]screenRow](t, data["b0-2-0"]["rows"])
	if len(files) != 1 || files[0].Cells[0] != "scan.txt" || files[0].Cells[1] != "web" || !strings.HasPrefix(files[0].Cells[4].(string), "process #") {
		t.Fatalf("files lists the files of the workspace with what they belong to: %+v", files)
	}
	snaps := blockData[[]screenRow](t, data["b0-3-0"]["rows"])
	if len(snaps) != 2 || !strings.HasPrefix(snaps[0].Cells[0].(string), "deck-2026") || snaps[1].Cells[1] != "2020-01-01T00:00:00Z" {
		t.Fatalf("snapshots lists what backups holds: %+v", snaps)
	}
	for _, r := range append(snaps, spend...) {
		if len(r.Actions) != 0 {
			t.Fatalf("the journal shows and restores nothing; only a file downloads: %+v", r)
		}
	}
	events := e.sse(t.Context())
	e.must("deck.snapshot_create", nil, nil)
	waitTopic(t, events, "snapshots")
}
