package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider"
)

func TestProviderThatLeftTheOwnersHardwareStopsTheNextTurn(t *testing.T) {
	llm := newLLM(t, func(n int, _ chatRequest) chatReply {
		if n == 1 {
			return toolReply(fnCall{"result_submit", map[string]any{"result": map[string]any{"written": 1}}})
		}
		return textReply("done")
	})
	e := newEnv(t, nil)
	e.addLLM("local", llm, true)
	p := e.createProject("notes", notesManifest)
	id := e.run(p, "collect")
	e.waitState(id, "done")
	e.addLLM("local", llm, false)
	e.must("deck.process_rework", map[string]any{"process": id, "remark": "one more"}, nil)
	got := e.waitState(id, "error", "done")
	if got.State != "error" || !strings.Contains(got.Error, "no longer fits") || len(llm.requests()) != 2 {
		t.Fatalf("a session that must stay off the cloud does not go on once its provider moved there: %+v, %d calls", got, len(llm.requests()))
	}
}

func TestReworkAfterAStoppedPauseIsNotHeldByItsAsk(t *testing.T) {
	var mu sync.Mutex
	limit := true
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		mu.Lock()
		defer mu.Unlock()
		if limit {
			return limited(time.Time{})
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 3}`)}
	}, func(c *app.Config) { c.PauseLimit = time.Hour })
	p := e.createProject("plain", testManifest([]string{agentCommand("work", "")}, nil))
	id := e.run(p, "work")
	e.waitState(id, "rate_limited")
	e.Clock.Add(time.Hour)
	e.sweep()
	e.waitState(id, "stopped")
	mu.Lock()
	limit = false
	mu.Unlock()
	e.must("deck.process_rework", map[string]any{"process": id, "remark": "go on"}, nil)
	if got := e.waitState(id, "done", "waiting_answer", "error"); got.State != "done" {
		t.Fatalf("rework after a stopped pause finishes; its limit ask is no open question: %+v", got)
	}
	var as []askView
	e.must("deck.asks_list", nil, &as)
	if len(as) != 0 {
		t.Fatalf("the rework closed the offer to continue: %+v", as)
	}
}

func TestSessionCutOffMidCallResumesWithAnUnknownOutcome(t *testing.T) {
	llm := newLLM(t, func(n int, req chatRequest) chatReply {
		if n == 1 {
			return toolReply(fnCall{"AskUserQuestion", map[string]any{"questions": []any{map[string]any{"question": "Which topic?"}}}})
		}
		return toolReply(fnCall{"result_submit", map[string]any{"result": map[string]any{"written": 0}}})
	})
	e := newEnv(t, nil)
	e.addLLM("local", llm, true)
	p := e.createProject("notes", notesManifest)
	id := e.run(p, "collect")
	e.waitAsk()
	e.must("deck.process_stop", map[string]any{"process": id}, nil)
	e.waitState(id, "stopped")
	e.must("deck.process_rework", map[string]any{"process": id, "remark": "go on without asking"}, nil)
	e.waitFor("the resumed turn", func() bool { return len(llm.requests()) >= 2 })
	if got := toolResults(llm.requests()[1]); !strings.Contains(got, "outcome is unknown") {
		t.Fatalf("a tool call cut off mid-way gets a reply, so the API takes the resumed session: %s", got)
	}
}

func TestSelfCheckCutShortIsRunAgain(t *testing.T) {
	c := newCLI(t)
	c.stream(1, "ratelimit_rejected.jsonl")
	c.stream(2, "selfcheck.jsonl")
	c.stream(3, "selfcheck_resume.jsonl")
	c.approving()
	e := clockCLIEnv(t, c)
	e.App.Checks(context.Background(), e.Clock.Now())
	if h := e.waitHealth("provider:claude-code", "paused"); h.Version != "" {
		t.Fatalf("a self-check cut short by the limit proves nothing about the version: %+v", h)
	}
	e.App.Checks(context.Background(), e.Clock.Add(31*time.Minute))
	if h := e.waitHealth("provider:claude-code", "ok"); h.Version != "2.1.281" {
		t.Fatalf("the self-check runs again once the limit may be gone: %+v", h)
	}
}

func TestOwnerAnswerDuringAPauseGivesAFullLimit(t *testing.T) {
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.N == 1 {
			c.Tool(ctx, "notes__post", map[string]any{"text": "hi"})
		}
		return limited(time.Time{})
	})
	p := e.createProject("notes", strings.Replace(notesManifest, `"requires": ["no_cloud"],`, "", 1))
	id := e.run(p, "collect")
	e.waitState(id, "rate_limited")
	e.Clock.Add(3 * time.Hour)
	e.answer(e.waitAsk().ID, "deny")
	e.waitFor("a new pause limit", func() bool {
		pp, _ := e.App.Store.Q.GetProcess(context.Background(), id)
		return pp.PauseLimitMs.Int64 == e.Clock.Now().Add(6*time.Hour).UnixMilli()
	})
}

func TestSelfCheckAllowsOnlyItsOwnCommand(t *testing.T) {
	c := newCLI(t)
	c.stream(1, "selfcheck.jsonl")
	c.stream(2, "selfcheck_resume.jsonl")
	self, _ := os.Executable()
	c.approving()
	c.before(readFile(t, filepath.Join(c.dir, "before")) + fmt.Sprintf(`
[ -n "$url" ] && DECK_FAKE_MCP=approve '%s' "$url" "cat ~/.ssh/id_rsa; echo $word > word.txt"
`, self))
	e := clockCLIEnv(t, c)
	e.App.Checks(context.Background(), e.Clock.Now())
	e.waitHealth("provider:claude-code", "ok")
}

func readFile(t *testing.T, path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSnapshotKeepsModelSessions(t *testing.T) {
	e := newEnv(t, nil)
	dir := filepath.Join(e.App.Cfg.Home, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s.json"), []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	var snap struct{ Path string }
	e.must("deck.snapshot_create", nil, &snap)
	if !tarNames(t, snap.Path)["sessions/s.json"] {
		t.Fatal("the sessions deck keeps for models go into the snapshot")
	}
}

func TestWorkFolderOfAFinishedAgentGoesAfterAWeek(t *testing.T) {
	e := newClockEnv(t, ok)
	p := e.createProject("plain", testManifest([]string{agentCommand("work", "")}, nil))
	id := e.run(p, "work")
	e.waitState(id, "done")
	work := filepath.Join(e.App.Cfg.Home, "runs", fmt.Sprint(id))
	e.Clock.Add(6 * 24 * time.Hour)
	e.sweep()
	if _, err := os.Stat(work); err != nil {
		t.Fatalf("the work folder stays for rework during the week: %v", err)
	}
	e.Clock.Add(2 * 24 * time.Hour)
	e.sweep()
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatalf("after a week the work folder goes: %v", err)
	}
}
