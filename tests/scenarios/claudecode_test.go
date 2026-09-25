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
	"deck2/internal/provider/claudecode"
	"deck2/internal/store/db"
)

const plainManifest = `{
  "version": 4,
  "collections": [],
  "commands": [{
    "name": "work", "title": "Work", "description": "Does work", "class": "internal",
    "executor": {"type": "agent", "prompt": "Do the work."}
  }],
  "triggers": [],
  "screens": []
}`

// fakeCLI is a folder with the fake Claude Code CLI and the streams it replays.
type fakeCLI struct {
	t   *testing.T
	dir string
}

func newCLI(t *testing.T) *fakeCLI {
	t.Helper()
	dir := t.TempDir()
	b, err := os.ReadFile("testdata/claude/fake.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "claude"), b, 0o755); err != nil {
		t.Fatal(err)
	}
	return &fakeCLI{t: t, dir: dir}
}

// stream sets what call n replays; n = 0 sets it for every call without its own stream.
func (c *fakeCLI) stream(n int, fixture string) {
	var meta map[string]struct{ CLI, Date, Kind string }
	b, _ := os.ReadFile("testdata/claude/fixtures.json")
	if err := json.Unmarshal(b, &meta); err != nil {
		c.t.Fatal(err)
	}
	if m := meta[fixture]; m.CLI == "" || m.Date == "" || (m.Kind != "real" && m.Kind != "synthetic") {
		c.t.Fatalf("fixture %s needs its CLI version, date and kind in fixtures.json", fixture)
	}
	b, err := os.ReadFile(filepath.Join("testdata/claude", fixture))
	if err != nil {
		c.t.Fatal(err)
	}
	c.streamText(n, string(b))
}

func (c *fakeCLI) streamText(n int, text string) {
	name := "stream"
	if n > 0 {
		name += "." + strconv.Itoa(n)
	}
	if err := os.WriteFile(filepath.Join(c.dir, name), []byte(text), 0o644); err != nil {
		c.t.Fatal(err)
	}
}

// after sets shell code the fake runs once the stream is out; $c is the call folder, $n the call number.
func (c *fakeCLI) after(script string) {
	if err := os.WriteFile(filepath.Join(c.dir, "after"), []byte(script), 0o644); err != nil {
		c.t.Fatal(err)
	}
}

func (c *fakeCLI) skipStdin() {
	if err := os.WriteFile(filepath.Join(c.dir, "nostdin"), nil, 0o644); err != nil {
		c.t.Fatal(err)
	}
}

func (c *fakeCLI) adapter(home string) *claudecode.Adapter {
	return &claudecode.Adapter{Binary: filepath.Join(c.dir, "claude"), ConfigDir: filepath.Join(home, "mcp"), Linger: 300 * time.Millisecond}
}

type cliCall struct {
	settings     string
	settingsMode string
	args         []string
	env          map[string]string
	stdin        string
	cwd          string
	mcpMode      string
	mcp          string
}

func (c *fakeCLI) call(n int) cliCall {
	c.t.Helper()
	d := filepath.Join(c.dir, "calls", strconv.Itoa(n))
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(d, name))
		return string(b)
	}
	if _, err := os.Stat(d); err != nil {
		c.t.Fatalf("the CLI was not started %d times", n)
	}
	cc := cliCall{env: map[string]string{}, stdin: read("stdin"), cwd: strings.TrimSpace(read("cwd")),
		mcpMode: strings.TrimSpace(read("mcpmode")), mcp: read("mcpconfig"),
		settings: read("settings"), settingsMode: strings.TrimSpace(read("settingsmode"))}
	cc.args = strings.Split(strings.TrimSuffix(read("args"), "\n"), "\n")
	for _, l := range strings.Split(read("env"), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok && k != "" && !strings.ContainsAny(k, " \t") {
			cc.env[k] = v
		}
	}
	return cc
}

func (c *fakeCLI) calls() int {
	es, _ := os.ReadDir(filepath.Join(c.dir, "calls"))
	return len(es)
}

// flag returns the value after name in args and whether it was there.
func flag(args []string, name string) (string, bool) {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func cliEnv(t *testing.T, c *fakeCLI) (*Env, string) {
	home := t.TempDir()
	return newEnvWith(t, home, c.adapter(home)), home
}

func (e *Env) session(id int64) string {
	p, err := e.App.Store.Q.GetProcess(context.Background(), id)
	if err != nil {
		e.T.Fatal(err)
	}
	return p.SessionID
}

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

func TestResumeAfterEarlyCrashStartsTheSessionAgain(t *testing.T) {
	c := newCLI(t)
	c.after(`[ "$n" = 1 ] && sleep 300`)
	c.stream(2, "noconversation.jsonl")
	c.stream(3, "ok.jsonl")
	home := t.TempDir()
	e1 := newEnvWith(t, home, c.adapter(home))
	p := e1.createProject("plain", plainManifest)
	id := e1.run(p, "work")
	sid := e1.session(id)
	deadline := time.Now().Add(5 * time.Second)
	for c.calls() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	_ = e1.App.Close()

	e2 := newEnvWith(t, home, c.adapter(home))
	if err := e2.App.Runner.ResumeInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e2.waitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("want done, got %+v", got)
	}
	if v, ok := flag(c.call(2).args, "--resume"); !ok || v != sid {
		t.Fatalf("deck must first resume session %s: %v", sid, c.call(2).args)
	}
	third := c.call(3)
	if v, ok := flag(third.args, "--session-id"); !ok || v != sid || !strings.Contains(third.stdin, "Do the work.") {
		t.Fatalf("a session that never started must start again with its id and the full prompt: %v %q", third.args, third.stdin)
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

func TestLostLoginWaitsForOwnerAndRetries(t *testing.T) {
	c := newCLI(t)
	c.stream(1, "nologin.jsonl")
	c.stream(2, "ok.jsonl")
	e, _ := cliEnv(t, c)
	p := e.createProject("plain", plainManifest)
	id := e.run(p, "work")
	e.waitState(id, "needs_login")
	a := e.waitAsk()
	if a.Kind != "login" || !strings.Contains(a.Text, "Not logged in") || !strings.Contains(string(a.Payload), "new_session") {
		t.Fatalf("want a login ask with the choices, got %+v", a)
	}
	if h := e.health("provider:claude-code"); h["status"] != "needs_login" || !strings.Contains(h["error"].(string), "Not logged in") {
		t.Fatalf("the provider health must show the lost login: %v", h)
	}
	if code, _ := e.call("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "yes"}); code != 422 {
		t.Fatalf("a login ask takes only retry or new_session, got %d", code)
	}
	e.answer(a.ID, "retry")
	if got := e.waitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("want done, got %+v", got)
	}
	second := c.call(2)
	if v, ok := flag(second.args, "--resume"); !ok || v != e.session(id) || !strings.Contains(second.stdin, "logged in again") {
		t.Fatalf("retry must resume the same session: %v %q", second.args, second.stdin)
	}
	if h := e.health("provider:claude-code"); h["status"] != "ok" {
		t.Fatalf("a successful turn must clear the health: %v", h)
	}
}

func TestLostLoginNewSessionGetsReportOfWhatWasDone(t *testing.T) {
	c := newCLI(t)
	c.stream(1, "nologin.jsonl")
	c.stream(2, "ok.jsonl")
	e, _ := cliEnv(t, c)
	p := e.createProject("plain", plainManifest)
	id := e.run(p, "work")
	e.waitState(id, "needs_login")
	first := e.session(id)
	err := e.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		return q.LogToolCall(ctx, db.LogToolCallParams{ProcessID: id, ProjectID: p, Tool: "records_upsert", Class: "internal", Outcome: "done"})
	})
	if err != nil {
		t.Fatal(err)
	}
	e.answer(e.waitAsk().ID, "new_session")
	if got := e.waitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("want done, got %+v", got)
	}
	second := c.call(2)
	sid, ok := flag(second.args, "--session-id")
	if !ok || sid == first || sid != e.session(id) {
		t.Fatalf("new_session must start a new session deck assigns: %v, first %s", second.args, first)
	}
	if !strings.Contains(second.stdin, "Do the work.") || !strings.Contains(second.stdin, "- records_upsert: done") {
		t.Fatalf("the new session needs the task and the journal of the old one: %q", second.stdin)
	}
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

func waitCalls(t *testing.T, c *fakeCLI, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.calls() < n {
		if time.Now().After(deadline) {
			t.Fatalf("the CLI was started %d times, want %d", c.calls(), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRetryAfterLostLoginFinishesWhenTheSessionWasNeverSaved(t *testing.T) {
	c := newCLI(t)
	c.stream(1, "nologin.jsonl")
	c.stream(2, "noconversation.jsonl")
	c.stream(3, "ok.jsonl")
	e, _ := cliEnv(t, c)
	p := e.createProject("plain", plainManifest)
	id := e.run(p, "work")
	e.waitState(id, "needs_login")
	e.answer(e.waitAsk().ID, "retry")
	if got := e.waitState(id, "done", "error"); got.State != "done" || !strings.Contains(string(got.Result), "ok") {
		t.Fatalf("want done with the result, got %+v", got)
	}
}

func TestNewSessionReportSurvivesRestart(t *testing.T) {
	c := newCLI(t)
	c.stream(1, "nologin.jsonl")
	c.after(`[ "$n" = 2 ] && sleep 300`)
	c.stream(3, "noconversation.jsonl")
	c.stream(4, "ok.jsonl")
	home := t.TempDir()
	e1 := newEnvWith(t, home, c.adapter(home))
	p := e1.createProject("plain", plainManifest)
	id := e1.run(p, "work")
	e1.waitState(id, "needs_login")
	err := e1.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		return q.LogToolCall(ctx, db.LogToolCallParams{ProcessID: id, ProjectID: p, Tool: "records_upsert", Class: "internal", Outcome: "done"})
	})
	if err != nil {
		t.Fatal(err)
	}
	e1.answer(e1.waitAsk().ID, "new_session")
	waitCalls(t, c, 2)
	_ = e1.App.Close()

	e2 := newEnvWith(t, home, c.adapter(home))
	if err := e2.App.Runner.ResumeInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e2.waitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("want done, got %+v", got)
	}
	fourth := c.call(4)
	if sid, _ := flag(fourth.args, "--session-id"); sid != e2.session(id) || !strings.Contains(fourth.stdin, "- records_upsert: done") {
		t.Fatalf("the restarted new session must still get the report: %v %q", fourth.args, fourth.stdin)
	}
}

func TestResultSeenWhileDeckStopsIsNotTheOutcome(t *testing.T) {
	c := newCLI(t)
	c.stream(1, "ok.jsonl")
	c.after(`[ "$n" = 1 ] && sleep 300`)
	c.stream(2, "ok.jsonl")
	home := t.TempDir()
	slow := c.adapter(home)
	slow.Linger = time.Minute
	e1 := newEnvWith(t, home, slow)
	p := e1.createProject("plain", plainManifest)
	id := e1.run(p, "work")
	waitCalls(t, c, 1)
	time.Sleep(200 * time.Millisecond)
	_ = e1.App.Close()

	e2 := newEnvWith(t, home, c.adapter(home))
	if got := e2.process(id); got.State != "running" {
		t.Fatalf("a turn cut by the stop of deck must stay running, got %+v", got)
	}
	if err := e2.App.Runner.ResumeInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	e2.waitState(id, "done")
	if _, ok := flag(c.call(2).args, "--resume"); !ok {
		t.Fatalf("the process must continue its session after the start: %v", c.call(2).args)
	}
}
