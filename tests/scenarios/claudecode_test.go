package scenarios

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"deck2/internal/provider"
)

func TestCLIGetsOnlyAllowlistedEnvironment(t *testing.T) {
	for k, v := range map[string]string{"ANTHROPIC_API_KEY": "sk-leak", "ANTHROPIC_BASE_URL": "http://leak", "CLAUDE_CODE_OAUTH_TOKEN": "leak",
		"CLAUDE_CODE_USE_BEDROCK": "leak", "DECK_HOME": "/leak", "AWS_SECRET_ACCESS_KEY": "leak", "LC_ALL": "C", "NODE_OPTIONS": "--no-warnings"} {
		t.Setenv(k, v)
	}
	c := newCLI(t)
	c.stream(0, "ok.jsonl")
	e, home := cliEnv(t, c)
	p := e.createProject("plain", plainManifest)
	id := e.run(p, "work")
	if got := e.waitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("want done, got %+v", got)
	}
	call := c.call(1)
	for k, v := range call.env {
		if strings.Contains(v, "leak") {
			t.Errorf("%s reached the CLI", k)
		}
	}
	for _, k := range []string{"PATH", "HOME", "LC_ALL", "NODE_OPTIONS", "MCP_TOOL_TIMEOUT"} {
		if _, ok := call.env[k]; !ok {
			t.Errorf("%s must reach the CLI", k)
		}
	}
	if sid, ok := flag(call.args, "--session-id"); !ok || sid != e.session(id) {
		t.Fatalf("the CLI must run the session deck assigned: %q vs %q", sid, e.session(id))
	}
	if _, ok := flag(call.args, "--resume"); ok {
		t.Fatal("a new process must not resume")
	}
	if src, ok := flag(call.args, "--setting-sources"); !ok || src != "" {
		t.Fatalf("a command agent must load no settings, got %q", src)
	}
	cfg, _ := flag(call.args, "--mcp-config")
	cwd, _ := filepath.EvalSymlinks(call.cwd)
	realCfg, _ := filepath.EvalSymlinks(filepath.Dir(cfg))
	if strings.HasPrefix(realCfg, cwd) || strings.HasPrefix(cfg, filepath.Join(home, "runs")) {
		t.Fatalf("the MCP config %s must be outside the agent folder %s", cfg, cwd)
	}
	if call.mcpMode != "-rw-------" || !strings.Contains(call.mcp, "/mcp/") {
		t.Fatalf("the MCP config must be 600 and point at the gateway: %s %s", call.mcpMode, call.mcp)
	}
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Fatalf("the MCP config must be removed after the turn: %v", err)
	}
	var set struct {
		Env          map[string]string
		APIKeyHelper *string `json:"apiKeyHelper"`
	}
	if err := json.Unmarshal([]byte(call.settings), &set); err != nil {
		t.Fatal(err)
	}
	if call.settingsMode != "-rw-------" || set.APIKeyHelper == nil || *set.APIKeyHelper != "" || set.Env["ANTHROPIC_API_KEY"] != "" ||
		set.Env["CLAUDE_CODE_USE_BEDROCK"] != "" {
		t.Fatalf("deck settings must blank out the key helper and API variables: %s %s", call.settingsMode, call.settings)
	}
	if f, _ := flag(call.args, "--settings"); f == "" || !strings.HasPrefix(f, filepath.Join(home, "mcp")) {
		t.Fatalf("settings must be a file of deck: %q", f)
	}
}

func TestLargeValuesAreNotArguments(t *testing.T) {
	c := newCLI(t)
	c.stream(0, "ok.jsonl")
	e, _ := cliEnv(t, c)
	desc := strings.Repeat("d", 200*1024)
	m := strings.Replace(ideasManifest, `"properties": {"written": {"type": "integer"}}`,
		`"properties": {"written": {"type": "integer", "description": "`+desc+`"}}`, 1)
	p := e.createProject("ideas", m)
	e.waitState(e.run(p, "collect"), "done", "error")
	call := c.call(1)
	for _, a := range call.args {
		if len(a) > 8*1024 {
			t.Fatalf("an argument of %d bytes: %.80s", len(a), a)
		}
	}
	if !strings.Contains(call.stdin, `"subtype":"initialize"`) || !strings.Contains(call.stdin, desc) || !strings.Contains(call.stdin, "Write three ideas") {
		t.Fatalf("the schema and the prompt must come by stdin: %.300s", call.stdin)
	}
}

func TestFakeCLIRejectsUnknownFlags(t *testing.T) {
	c := newCLI(t)
	out, err := exec.Command(filepath.Join(c.dir, "claude"), "-p", "--json-schema", "{}").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "unknown option '--json-schema'") {
		t.Fatalf("the fake must reject flags the adapter is not known to pass: %v %s", err, out)
	}
}

func TestCLIThatDoesNotExitAfterItsResultIsKilled(t *testing.T) {
	for name, after := range map[string]string{
		"hangs":        `sleep 300 & echo $! > "$c/child"; wait`,
		"leaves child": `sleep 300 >/dev/null 2>&1 & echo $! > "$c/child"`,
	} {
		t.Run(name, func(t *testing.T) {
			c := newCLI(t)
			c.stream(0, "ok.jsonl")
			c.after(after)
			e, _ := cliEnv(t, c)
			p := e.createProject("plain", plainManifest)
			start := time.Now()
			got := e.waitState(e.run(p, "work"), "done", "error")
			if got.State != "done" || !strings.Contains(string(got.Result), "ok") {
				t.Fatalf("the result must be kept, got %+v", got)
			}
			if d := time.Since(start); d > 4*time.Second {
				t.Fatalf("took %s", d)
			}
			b, _ := os.ReadFile(filepath.Join(c.dir, "calls", "1", "child"))
			pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for syscall.Kill(pid, 0) == nil {
				if time.Now().After(deadline) {
					t.Fatalf("child %d of the CLI is still alive", pid)
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}

func TestTurnSucceedsOnlyStrictly(t *testing.T) {
	big := strings.Repeat("x", 512*1024)
	for _, tc := range []struct {
		name, fixture, after string
		skipStdin            bool
		prompt               string
		want                 string
	}{
		{name: "second result", fixture: "two_results.jsonl", want: "2 results"},
		{name: "foreign session", fixture: "foreign_session.jsonl", want: "instead of"},
		{name: "error result", fixture: "error.jsonl", want: "API Error: 500"},
		{name: "no result", after: "exit 1", want: "without a result"},
		{name: "prompt not read", fixture: "ok.jsonl", skipStdin: true, prompt: big, want: "did not reach"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newCLI(t)
			if tc.fixture != "" {
				c.stream(0, tc.fixture)
			}
			if tc.after != "" {
				c.after(tc.after)
			}
			if tc.skipStdin {
				c.skipStdin()
			}
			m := plainManifest
			if tc.prompt != "" {
				m = strings.Replace(m, "Do the work.", tc.prompt, 1)
			}
			e, _ := cliEnv(t, c)
			p := e.createProject("plain", m)
			got := e.waitState(e.run(p, "work"), "done", "error")
			if got.State != "error" || !strings.Contains(got.Error, tc.want) {
				t.Fatalf("want error with %q, got %+v", tc.want, got)
			}
		})
	}
}

func (e *Env) health(subject string) map[string]any {
	var hs []map[string]any
	e.must("deck.health_list", nil, &hs)
	for _, h := range hs {
		if h["subject"] == subject {
			return h
		}
	}
	return nil
}

func TestRateLimitWarningDoesNotPause(t *testing.T) {
	c := newCLI(t)
	c.stream(0, "ratelimit_warning.jsonl")
	e, _ := cliEnv(t, c)
	p := e.createProject("plain", plainManifest)
	if got := e.waitState(e.run(p, "work"), "done", "error"); got.State != "done" {
		t.Fatalf("a warning about the limit must not stop the turn, got %+v", got)
	}
	res, err := c.adapter(t.TempDir()).Run(context.Background(), provider.Request{Cwd: t.TempDir(), Prompt: "x", SessionID: "s"})
	if err != nil || res.RateLimited || res.IsError {
		t.Fatalf("allowed_warning and a rejected overage are not a limit: %+v %v", res, err)
	}
}

func TestRateLimitIsOnlyAnExplicitRefusal(t *testing.T) {
	c := newCLI(t)
	c.stream(1, "ratelimit_rejected.jsonl")
	b, _ := os.ReadFile("testdata/claude/ratelimit_rejected.jsonl")
	ls := strings.Split(strings.TrimSpace(string(b)), "\n")
	b, _ = os.ReadFile("testdata/claude/ok.jsonl")
	ok := strings.Split(strings.TrimSpace(string(b)), "\n")
	c.streamText(2, strings.Join([]string{ls[0], ls[1], ok[1], ok[3]}, "\n")+"\n")
	a := c.adapter(t.TempDir())
	res, err := a.Run(context.Background(), provider.Request{Cwd: t.TempDir(), Prompt: "x", SessionID: "s"})
	if err != nil || !res.RateLimited || res.ResetsAt != 1790379000 {
		t.Fatalf("a rejected rate limit event is a limit: %+v %v", res, err)
	}
	res, err = a.Run(context.Background(), provider.Request{Cwd: t.TempDir(), Prompt: "x", SessionID: "s"})
	if err != nil || res.RateLimited {
		t.Fatalf("a later model answer lifts the refusal: %+v %v", res, err)
	}
}

func TestSubAgentEventsAreNotProcessEvents(t *testing.T) {
	c := newCLI(t)
	c.stream(0, "subagent.jsonl")
	var events []provider.Event
	res, err := c.adapter(t.TempDir()).Run(context.Background(), provider.Request{Cwd: t.TempDir(), Prompt: "x", SessionID: "s",
		OnEvent: func(ev provider.Event) { events = append(events, ev) }})
	if err != nil || res.Text != "done" {
		t.Fatalf("want the result of the main agent, got %+v %v", res, err)
	}
	var texts []string
	for _, ev := range events {
		texts = append(texts, ev.Kind+":"+ev.Text)
	}
	got := strings.Join(texts, ",")
	if strings.Contains(got, "sub-agent") || !strings.Contains(got, "tool:Agent") || !strings.Contains(got, "text:done") {
		t.Fatalf("only events of the main agent count: %s", got)
	}
}

func TestCodeRunSeesRepoInstructionsOnly(t *testing.T) {
	c := newCLI(t)
	c.stream(0, "ok.jsonl")
	outer := t.TempDir()
	repo := filepath.Join(outer, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	repoSettings := `{"env": {"ANTHROPIC_EXTRA_KEY": "sk-repo", "GOFLAGS": "-mod=mod"}, "apiKeyHelper": "echo sk-repo"}`
	if err := os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), []byte(repoSettings), 0o644); err != nil {
		t.Fatal(err)
	}
	a := c.adapter(t.TempDir())
	if _, err := a.Run(context.Background(), provider.Request{Cwd: repo, Repo: true, Prompt: "x", SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	args := c.call(1).args
	if src, _ := flag(args, "--setting-sources"); src != "project" {
		t.Fatalf("a code run loads the repo settings, got %q", src)
	}
	var s struct {
		Excludes    []string          `json:"claudeMdExcludes"`
		Env         map[string]string `json:"env"`
		Permissions struct{ Deny []string }
	}
	if err := json.Unmarshal([]byte(c.call(1).settings), &s); err != nil {
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

func TestPromptMentionsOnlyConnectedTools(t *testing.T) {
	for _, noQuestions := range []bool{false, true} {
		fake := &Fake{noQuestions: noQuestions, script: func(context.Context, *Call) provider.Result {
			return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
		}}
		e := newEnvWith(t, t.TempDir(), fake)
		e.Fake = fake
		p := e.createProject("ideas", ideasManifest)
		e.waitState(e.run(p, "collect"), "done")
		prompt := fake.Calls()[0].Prompt
		if strings.Contains(prompt, "AskUserQuestion") == noQuestions || !strings.Contains(prompt, "records_upsert") {
			t.Fatalf("question tool present=%v, prompt:\n%s", !noQuestions, prompt)
		}
	}
}
