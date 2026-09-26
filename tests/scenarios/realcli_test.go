package scenarios

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"deck2/internal/api"
	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/internal/provider/claudecode"
	"deck2/internal/store/db"
	"deck2/web"
)

const realManifest = `{
  "version": 4,
  "collections": [{
    "name": "ideas", "description": "Short ideas", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "score": {"type": "integer"}, "topic": {"type": "string"}},
               "required": ["title", "score", "topic"]}
  }],
  "commands": [{
    "name": "collect", "title": "Collect ideas", "description": "Writes ideas", "class": "internal",
    "executor": {"type": "agent", "prompt": "First ask the owner with the AskUserQuestion tool which topic to use, with exactly two options: go and rust. Then write two short project ideas about the chosen topic into the ideas collection using the records_upsert tool (score 1-10, topic = the chosen topic). Do not use any other tools. Finish with the number of records written."},
    "result": {"type": "object", "properties": {"written": {"type": "integer"}}, "required": ["written"]}
  }],
  "triggers": [],
  "screens": [{"name": "main", "title": "Ideas", "blocks": [{"type": "table", "title": "Ideas",
    "source": {"collection": "ideas"}, "columns": [{"label": "Idea", "bind": "title"}, {"label": "Topic", "bind": "topic"}]}]}]
}`

// TestRealClaudeCodeQuestionResume runs the stage 0 acceptance scenario on the installed Claude Code CLI.
func TestRealClaudeCodeQuestionResume(t *testing.T) {
	if os.Getenv("DECK_REAL_CLI") == "" {
		t.Skip("set DECK_REAL_CLI=1 to run against the real Claude Code CLI")
	}
	ts := httptest.NewUnstartedServer(nil)
	base := "http://" + ts.Listener.Addr().String()
	home := t.TempDir()
	a, err := app.New(app.Config{Home: home, MCPBase: base, QuestionWait: 5 * time.Second, ConfirmWait: 5 * time.Second}, claudecode.New(filepath.Join(home, "mcp")))
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = api.New(a, token, web.FS()).Handler()
	ts.Start()
	t.Cleanup(func() { _ = a.Close(); ts.Close() })
	e := &Env{T: t, App: a, URL: base, names: map[int64]string{}}

	p := e.createProject("ideas", realManifest)
	id := e.run(p, "collect")
	waitLong := func(states ...string) proc {
		deadline := time.Now().Add(4 * time.Minute)
		for time.Now().Before(deadline) {
			got := e.process(id)
			for _, s := range states {
				if got.State == s {
					return got
				}
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Fatalf("process did not reach %v: %+v", states, e.process(id))
		return proc{}
	}
	stopped := waitLong("waiting_answer", "done", "error")
	if stopped.State != "waiting_answer" {
		t.Fatalf("agent should stop waiting for the owner, got %+v", stopped)
	}
	a1 := e.waitAsk()
	if a1.Kind != "question" {
		t.Fatalf("unexpected ask %+v", a1)
	}
	t.Logf("question: %s", a1.Text)
	e.answer(a1.ID, "rust")
	got := waitLong("done", "error")
	t.Logf("result: %+v", got)
	if got.State != "done" || got.NotBySchema {
		t.Fatalf("want done by schema, got %+v", got)
	}
	recs, _ := a.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "ideas"})
	if len(recs) < 2 {
		t.Fatalf("want at least 2 records, got %d", len(recs))
	}
	for _, r := range recs {
		if !strings.Contains(strings.ToLower(r.Data), `"topic":"rust"`) {
			t.Fatalf("record ignores the owner's answer: %s", r.Data)
		}
		t.Logf("record: %s", r.Data)
	}
	if rows := e.screenRows(p, "main"); len(rows) != len(recs) {
		t.Fatalf("screen shows %d rows, want %d", len(rows), len(recs))
	}
}

const bashManifest = `{
  "version": 4,
  "collections": [],
  "commands": [{
    "name": "mark", "title": "Mark", "description": "Writes a marker file with Bash", "class": "internal",
    "executor": {"type": "agent", "prompt": "Use the Bash tool to run exactly this command: echo deck-ok > marker.txt\nIf it is refused, do not try any other way; just report the refusal and stop."}
  }],
  "triggers": [],
  "screens": []
}`

// TestRealClaudeCodePermission checks that a Bash permission request reaches the owner and that allow and
// deny with a reason both work on the real CLI.
func TestRealClaudeCodePermission(t *testing.T) {
	if os.Getenv("DECK_REAL_CLI") == "" {
		t.Skip("set DECK_REAL_CLI=1 to run against the real Claude Code CLI")
	}
	home := t.TempDir()
	ts := httptest.NewUnstartedServer(nil)
	base := "http://" + ts.Listener.Addr().String()
	a, err := app.New(app.Config{Home: home, MCPBase: base, QuestionWait: 2 * time.Minute, ConfirmWait: 2 * time.Minute}, claudecode.New(filepath.Join(home, "mcp")))
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = api.New(a, token, web.FS()).Handler()
	ts.Start()
	t.Cleanup(func() { _ = a.Close(); ts.Close() })
	e := &Env{T: t, App: a, URL: base, names: map[int64]string{}}
	p := e.createProject("perm", bashManifest)

	for _, tc := range []struct{ answer, wantFile string }{{"allow", "deck-ok"}, {"deny:not today", ""}} {
		id := e.run(p, "mark")
		deadline := time.Now().Add(3 * time.Minute)
		var ask askView
		for time.Now().Before(deadline) {
			_, out := e.call("deck.asks_list", nil)
			var as []askView
			_ = json.Unmarshal(out, &as)
			if len(as) > 0 {
				ask = as[0]
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
		if ask.Kind != "confirm" || !strings.Contains(ask.Text, "marker.txt") {
			t.Fatalf("want a Bash confirmation, got %+v (process %+v)", ask, e.process(id))
		}
		t.Logf("confirm: %s", ask.Text)
		e.answer(ask.ID, tc.answer)
		for time.Now().Before(deadline) && e.process(id).State == "running" {
			time.Sleep(300 * time.Millisecond)
		}
		got := e.process(id)
		b, _ := os.ReadFile(filepath.Join(home, "runs", itoa(id), "work", "marker.txt"))
		t.Logf("%s -> state %s, file %q, result %q", tc.answer, got.State, strings.TrimSpace(string(b)), got.Result)
		if got.State != "done" || strings.TrimSpace(string(b)) != tc.wantFile {
			t.Fatalf("answer %q: state %s, file %q", tc.answer, got.State, b)
		}
	}
}

func realCLI(t *testing.T) {
	if os.Getenv("DECK_REAL_CLI") == "" {
		t.Skip("set DECK_REAL_CLI=1 to run against the real Claude Code CLI")
	}
}

// TestRealClaudeCodeKeepsSubscription checks that API variables of deck's environment do not reach the CLI.
func TestRealClaudeCodeKeepsSubscription(t *testing.T) {
	realCLI(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-invalid")
	t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:1")
	sid := uuid.NewString()
	res, err := claudecode.New(t.TempDir()).Run(context.Background(), provider.Request{Cwd: t.TempDir(),
		Prompt: "Reply with just: ok", SessionID: sid, MCPURL: "http://127.0.0.1:1/mcp/none"})
	t.Logf("result: %+v", res)
	if err != nil || res.IsError || !strings.Contains(strings.ToLower(res.Text), "ok") {
		t.Fatalf("want a successful turn of session %s on the subscription: %+v %v", sid, res, err)
	}
}

// TestRealClaudeCodeRepoInstructions checks that a code run sees the instructions of the repo and its
// subfolders but not of the folders above; the control run without excludes must see them all.
func TestRealClaudeCodeRepoInstructions(t *testing.T) {
	realCLI(t)
	outer := t.TempDir()
	repo := filepath.Join(outer, "repo")
	files := map[string]string{
		"CLAUDE.md":                  "Marker word: OUTERMARK.",
		".claude/CLAUDE.md":          "Marker word: OUTERDOTMARK.",
		".claude/rules/extra.md":     "Marker word: OUTERRULEMARK.",
		"repo/CLAUDE.md":             "Marker word: REPOMARK.",
		"repo/sub/CLAUDE.md":         "Marker word: SUBMARK.",
		"repo/.claude/settings.json": `{"env": {"ANTHROPIC_API_KEY": "sk-ant-repo", "ANTHROPIC_BASE_URL": "http://127.0.0.1:1"}, "apiKeyHelper": "echo sk-ant-repo"}`,
		"repo/sub/notes.txt":         "Nothing here.",
	}
	for name, text := range files {
		p := filepath.Join(outer, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	prompt := "Read the file sub/notes.txt with the Read tool. Then try to create .claude/settings.local.json with the content {} using the Write tool. Then reply with only the marker words (ALL CAPS words ending in MARK) " +
		"that appear anywhere in your instructions or context, separated by spaces. Do not guess and do not search for files."
	a := claudecode.New(t.TempDir())
	res, err := a.Run(context.Background(), provider.Request{Cwd: repo, Repo: true, Prompt: prompt, SessionID: uuid.NewString(),
		MCPURL: "http://127.0.0.1:1/mcp/none", AllowedTools: []string{"Read", "Write"}})
	if err != nil || res.IsError {
		t.Fatalf("the repo settings must not move the code run off the subscription: %+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".claude", "settings.local.json")); err == nil {
		t.Fatal("the agent must not write the repo settings")
	}
	t.Logf("code run sees: %s", res.Text)
	for _, w := range []string{"REPOMARK", "SUBMARK"} {
		if !strings.Contains(res.Text, w) {
			t.Errorf("the code run must see %s", w)
		}
	}
	for _, w := range []string{"OUTERMARK", "OUTERDOTMARK", "OUTERRULEMARK"} {
		if strings.Contains(res.Text, w) {
			t.Errorf("the code run must not see %s", w)
		}
	}

	cmd := exec.Command("claude", "-p", "--output-format", "text", "--setting-sources", "project", "--strict-mcp-config",
		"--allowedTools", "Read", "--settings", `{"apiKeyHelper": "", "env": {"ANTHROPIC_API_KEY": "", "ANTHROPIC_BASE_URL": ""}}`)
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader(prompt)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("control run: %v", err)
	}
	t.Logf("control run sees: %s", out)
	if !strings.Contains(string(out), "OUTERMARK") {
		t.Fatalf("the control run without excludes must see the folder above, or the test proves nothing")
	}
}

// TestRealClaudeCodeSeesSkills checks that skills given as a plugin reach a session with no setting sources.
func TestRealClaudeCodeSeesSkills(t *testing.T) {
	realCLI(t)
	skill := "---\nname: probe\ndescription: Knows the probe word. Use it when asked for the probe word.\n---\nThe probe word is PERSIMMON.\n"
	prompt := "What is the probe word? Use your skills if you need to. Reply with only the word."
	run := func(skills map[string]string) provider.Result {
		res, err := claudecode.New(t.TempDir()).Run(context.Background(), provider.Request{Cwd: t.TempDir(), Prompt: prompt,
			SessionID: uuid.NewString(), MCPURL: "http://127.0.0.1:1/mcp/none", Skills: skills, AllowedTools: []string{"Skill"}})
		if err != nil || res.IsError {
			t.Fatalf("turn: %+v %v", res, err)
		}
		return res
	}
	if res := run(map[string]string{"probe/SKILL.md": skill}); !strings.Contains(res.Text, "PERSIMMON") {
		t.Fatalf("the session must see the skill: %q", res.Text)
	}
	if res := run(nil); strings.Contains(res.Text, "PERSIMMON") {
		t.Fatalf("without the skill the word is unknown, or the test proves nothing: %q", res.Text)
	}
}

// TestRealClaudeCodeKnowsFakeFlags checks that every flag the fake CLI accepts exists in the real one.
func TestRealClaudeCodeKnowsFakeFlags(t *testing.T) {
	realCLI(t)
	help, err := exec.Command("claude", "--help").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile("testdata/claude/fake.sh")
	flags := regexp.MustCompile(`--[a-zA-Z-]+`).FindAllString(string(b), -1)
	if len(flags) < 10 {
		t.Fatalf("found only %v in the fake", flags)
	}
	for _, f := range flags {
		if !regexp.MustCompile(`(?m)(^|[\s,])` + regexp.QuoteMeta(f) + `\b`).Match(help) {
			t.Errorf("claude --help has no %s", f)
		}
	}
}

// TestRealClaudeCodeSelfCheck runs the adapter's self-check on the installed CLI.
func TestRealClaudeCodeSelfCheck(t *testing.T) {
	if os.Getenv("DECK_REAL_CLI") == "" {
		t.Skip("set DECK_REAL_CLI=1 to run against the real Claude Code CLI")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := claudecode.New(t.TempDir()).SelfCheck(ctx)
	if c.Err != nil || c.Version == "" {
		t.Fatalf("the self-check of the real CLI is green: %+v", c)
	}
	t.Logf("self-check of %s is green", c.Version)
}
