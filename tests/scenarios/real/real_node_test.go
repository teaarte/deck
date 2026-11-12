package real

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/provider/claudecode"
	"deck2/internal/provider/remote"
	"deck2/internal/store/db"
	"deck2/tests/harness"
)

// TestRealClaudeCodeOnANode runs the scenario of a question and its answer on the installed Claude Code of a node:
// the node's CLI on its own sign-in, deck's tools and the owner's answer through deck.
func TestRealClaudeCodeOnANode(t *testing.T) {
	realCLI(t)
	home := t.TempDir()
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(claudecode.New(home, nil)), harness.WithWait(4*time.Minute))
	n := e.PairNode("Mac")
	n.RunAgents(func(spec remote.Spec, nodeHome string, _ func(provider.ModelHooks) http.Handler) provider.Provider {
		return claudecode.New(nodeHome, nil)
	})
	realHomeAt(t, n.Home)
	n.Allow("claude-code")
	n.Start()
	p := e.Project("ideas", harness.OnLogin(realManifest))
	e.UseModels("ideas.collect", "claude-code@Mac")
	id := e.Run(p, "collect")
	if stopped := e.WaitState(id, "waiting:answer", "done", "error"); stopped.State != "waiting:answer" {
		t.Fatalf("the agent on the node stops waiting for the owner: %+v %+v", stopped, e.Fault(id))
	}
	a := e.WaitAsk()
	t.Logf("question: %s", a.Text)
	e.Answer(a.ID, "rust")
	got := e.WaitState(id, "done", "error")
	if got.State != "done" || got.NotBySchema {
		t.Fatalf("the agent on the node ends done by the schema: %+v %+v", got, e.Fault(id))
	}
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "ideas"})
	if len(recs) < 2 {
		t.Fatalf("the agent on the node writes through deck: %d records", len(recs))
	}
	for _, r := range recs {
		if !strings.Contains(strings.ToLower(r.Data), `"topic":"rust"`) {
			t.Fatalf("a record ignores the owner's answer: %s", r.Data)
		}
	}
	var proc struct{ Provider string }
	e.Must("deck.process_get", map[string]any{"process": id}, &proc)
	if proc.Provider != "claude-code@Mac" {
		t.Fatalf("the process ran on the node: %+v", proc)
	}
	if _, err := os.Stat(deckhome.Layout(home).Agent("claude", p)); err == nil {
		t.Fatal("deck's own CLI kept no home of the project: the turn ran on the node")
	}
}
