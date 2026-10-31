package scenarios

// Contract tests of the Claude Code adapter on recorded streams.

import (
	"context"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRateLimitWarningDoesNotPause(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "ratelimit_warning.jsonl")
	e := harness.New(t, harness.WithCLI(c))
	p := e.Project("plain", harness.PlainManifest)
	if got := e.WaitState(e.Run(p, "work"), "done", "error"); got.State != "done" {
		t.Fatalf("a warning about the limit must not stop the turn, got %+v", got)
	}
	res, err := c.Adapter(t.TempDir()).Run(context.Background(), provider.Request{Cwd: t.TempDir(), Prompt: "x", SessionID: "s"})
	if err != nil || res.Outcome != provider.Done {
		t.Fatalf("allowed_warning and a rejected overage are not a limit: %+v %v", res, err)
	}
}

func TestRateLimitIsOnlyAnExplicitRefusal(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(1, "ratelimit_rejected.jsonl")
	b, _ := os.ReadFile(harness.Testdata("claude/ratelimit_rejected.jsonl"))
	ls := strings.Split(strings.TrimSpace(string(b)), "\n")
	b, _ = os.ReadFile(harness.Testdata("claude/ok.jsonl"))
	ok := strings.Split(strings.TrimSpace(string(b)), "\n")
	c.StreamText(2, strings.Join([]string{ls[0], ls[1], ok[1], ok[3]}, "\n")+"\n")
	a := c.Adapter(t.TempDir())
	res, err := a.Run(context.Background(), provider.Request{Cwd: t.TempDir(), Prompt: "x", SessionID: "s"})
	if err != nil || res.Outcome != provider.Limited || res.ResetsAt != 1790379000 {
		t.Fatalf("a rejected rate limit event is a limit: %+v %v", res, err)
	}
	res, err = a.Run(context.Background(), provider.Request{Cwd: t.TempDir(), Prompt: "x", SessionID: "s"})
	if err != nil || res.Outcome == provider.Limited {
		t.Fatalf("a later model answer lifts the refusal: %+v %v", res, err)
	}
}

func TestSubAgentEventsAreNotProcessEvents(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "subagent.jsonl")
	var events []provider.Event
	res, err := c.Adapter(t.TempDir()).Run(context.Background(), provider.Request{Cwd: t.TempDir(), Prompt: "x", SessionID: "s",
		OnEvent: func(ev provider.Event) { events = append(events, ev) }})
	if err != nil || res.Text != "done" {
		t.Fatalf("want the result of the main agent, got %+v %v", res, err)
	}
	var texts []string
	for _, ev := range events {
		if ev.Kind == provider.EventTool {
			texts = append(texts, ev.Kind+":"+ev.Title)
			continue
		}
		texts = append(texts, ev.Kind+":"+ev.Text)
	}
	got := strings.Join(texts, ",")
	call := slices.IndexFunc(events, func(ev provider.Event) bool { return ev.Kind == provider.EventTool })
	update := slices.IndexFunc(events, func(ev provider.Event) bool { return ev.Kind == provider.EventToolUpdate })
	if call < 0 || update < call || events[call].ID == "" || events[update].ID != events[call].ID || events[update].Status != "completed" ||
		!strings.Contains(events[call].Text, "Say hi") || !strings.Contains(events[update].Text, "hi") {
		t.Fatalf("a tool call and its result share the id of the call: %+v", events)
	}
	if strings.Contains(got, "sub-agent") || !strings.Contains(got, "tool:Agent") || !strings.Contains(got, "message:done") {
		t.Fatalf("only events of the main agent count: %s", got)
	}
}

func TestCodeRunSeesRepoInstructionsOnly(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "ok.jsonl")
	outer := t.TempDir()
	repo := filepath.Join(outer, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	repoSettings := `{"env": {"ANTHROPIC_EXTRA_KEY": "sk-repo", "GOFLAGS": "-mod=mod"}, "apiKeyHelper": "echo sk-repo"}`
	if err := os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), []byte(repoSettings), 0o644); err != nil {
		t.Fatal(err)
	}
	a := c.Adapter(t.TempDir())
	if _, err := a.Run(context.Background(), provider.Request{Cwd: repo, Repo: true, Prompt: "x", SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	args := c.Call(1).Args
	if src, _ := harness.Flag(args, "--setting-sources"); src != "project" {
		t.Fatalf("a code run loads the repo settings, got %q", src)
	}
	var s struct {
		Excludes    []string          `json:"claudeMdExcludes"`
		Env         map[string]string `json:"env"`
		Permissions struct{ Deny []string }
	}
	if err := json.Unmarshal([]byte(c.Call(1).Settings), &s); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(outer)
	if v, ok := s.Env["ANTHROPIC_EXTRA_KEY"]; !ok || v != "" {
		t.Fatalf("an API variable from the repo settings must be blanked out: %v", s.Env)
	}
	if _, ok := s.Env["GOFLAGS"]; ok {
		t.Fatalf("other repo variables stay: %v", s.Env)
	}
	if !slices.Contains(s.Permissions.Deny, "Write(/"+filepath.Join(real, "repo", ".claude", "settings*.json")+")") {
		t.Fatalf("the agent must not write the repo settings: %v", s.Permissions.Deny)
	}
	want := map[string]bool{filepath.Join(outer, "CLAUDE.md"): false, filepath.Join(real, "CLAUDE.md"): false,
		filepath.Join(real, ".claude", "CLAUDE.md"): false, "/CLAUDE.md": false}
	for _, x := range s.Excludes {
		if strings.HasPrefix(x, repo+string(filepath.Separator)) || strings.HasPrefix(x, filepath.Join(real, "repo")+string(filepath.Separator)) {
			t.Fatalf("instructions of the repo itself must not be excluded: %s", x)
		}
		if _, ok := want[x]; ok {
			want[x] = true
		}
	}
	for x, seen := range want {
		if !seen {
			t.Errorf("%s must be excluded", x)
		}
	}
}

func TestResumedClaudeCodeTurnCountsOnlyItsOwnTokens(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(1, "selfcheck.jsonl")
	c.Stream(2, "selfcheck_resume.jsonl")
	a := c.Adapter(t.TempDir())
	var got []provider.Usage
	req := provider.Request{Cwd: t.TempDir(), Prompt: "x", SessionID: "s", OnUsage: func(u provider.Usage) { got = append(got, u) }}
	if _, err := a.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	req.Resume = true
	if _, err := a.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Output != 399 || got[0].SessionCostUSD == nil || got[1].Input != 2 || got[1].Output != 243 ||
		got[1].CacheRead != 18216 || got[1].SessionCostUSD == nil || got[1].CostUSD != nil || got[1].Model != "claude-opus-5-5[1m]" {
		t.Fatalf("the model usage of a result adds up the session, so a resumed turn counts its own usage: %+v", got)
	}
}

func TestTheAnswerOfAClaudeCodeTurnIsTheLastResultOfItsSession(t *testing.T) {
	t.Parallel()
	for fixture, want := range map[string]string{"background_agents.jsonl": "gamma", "two_results.jsonl": "ok"} {
		c := harness.NewCLI(t)
		c.Stream(0, fixture)
		res, err := c.Adapter(t.TempDir()).Run(context.Background(), provider.Request{Cwd: t.TempDir(), Prompt: "x", SessionID: "s"})
		if err != nil || res.Outcome != provider.Done || res.Text != want {
			t.Fatalf("%s: background agents wake the session for more results, and the last one is the answer: %+v %v", fixture, res, err)
		}
	}
}

// TestAWokenClaudeCodeSessionMayWorkInSilence: once a background task wakes the session, it works as long as it
// needs; the CLI is stopped only for silence after a result.
func TestAWokenClaudeCodeSessionMayWorkInSilence(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	b, _ := os.ReadFile(harness.Testdata("claude/background_agents.jsonl"))
	ls := strings.Split(strings.TrimSpace(string(b)), "\n")
	last := slices.IndexFunc(ls, func(l string) bool { return strings.Contains(l, `"result": "beta"`) })
	c.StreamText(1, strings.Join(ls[:last+2], "\n")+"\n")
	rest := filepath.Join(c.Dir, "rest")
	if err := os.WriteFile(rest, []byte(strings.Join(ls[last+2:], "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.After(`sleep 1; sed "s/{{session}}/$session/g" '` + rest + `'` + "\n")
	res, err := c.Adapter(t.TempDir()).Run(context.Background(), provider.Request{Cwd: t.TempDir(), Prompt: "x", SessionID: "s"})
	if err != nil || res.Text != "gamma" {
		t.Fatalf("the woken session is not cut off while it works: %+v %v", res, err)
	}
}

// TestASelfCheckCutByItsCeilingIsSlowNotABrokenAdapter: a CLI that gives no answer in time, as on a machine without
// network, is slow, not broken.
func TestASelfCheckCutByItsCeilingIsSlowNotABrokenAdapter(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	if err := os.WriteFile(filepath.Join(c.Dir, "before"), []byte("sleep 30\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got := c.Adapter(t.TempDir()).SelfCheck(ctx)
	if !got.Slow || got.Err == nil || !strings.Contains(got.Err.Error(), "without network") {
		t.Fatalf("a self-check cut by its ceiling is slow and says the network may be down: %+v", got)
	}
}
