package scenarios

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"deck2/internal/provider"
	"deck2/internal/provider/claudecode"
	"deck2/internal/store/db"
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
	realCLI(t)
	home := t.TempDir()
	e := newEnvWith(t, home, claudecode.New(home))

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
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "ideas"})
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

// TestRealClaudeCodeRepoInstructions: an agent command in a repo folder sees the instructions of the repo and its
// subfolders, not of the folders above; the control run without excludes sees them all.
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
	home := t.TempDir()
	e := newEnvWith(t, home, claudecode.New(home))
	cmd, _ := json.Marshal(map[string]any{"name": "markers", "title": "Markers", "description": "Names the marker words.", "class": "internal",
		"result":   map[string]any{"type": "object", "required": []string{"markers"}, "properties": map[string]any{"markers": map[string]any{"type": "string"}}},
		"executor": map[string]any{"type": "agent", "folder": true, "prompt": prompt + " The result is those words in markers."}})
	p := e.createProject("code", `{"version": 5, "commands": [`+string(cmd)+`]}`)
	e.must("deck.folder_add", map[string]any{"project": "code", "path": repo}, nil)
	id := e.run(p, "markers")
	got := e.pollState(id, 5*time.Minute, time.Second, "done", "error")
	if got.State != "done" {
		t.Fatalf("the repo settings must not move the agent off the subscription: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(repo, ".claude", "settings.local.json")); err == nil {
		t.Fatal("the agent must not write the repo settings")
	}
	seen := string(got.Result)
	t.Logf("the agent in the folder sees: %s", seen)
	for _, w := range []string{"REPOMARK", "SUBMARK"} {
		if !strings.Contains(seen, w) {
			t.Errorf("the agent must see %s", w)
		}
	}
	for _, w := range []string{"OUTERMARK", "OUTERDOTMARK", "OUTERRULEMARK"} {
		if strings.Contains(seen, w) {
			t.Errorf("the agent must not see %s", w)
		}
	}

	control := exec.Command("claude", "-p", "--output-format", "text", "--setting-sources", "project", "--strict-mcp-config",
		"--allowedTools", "Read", "--settings", `{"apiKeyHelper": "", "env": {"ANTHROPIC_API_KEY": "", "ANTHROPIC_BASE_URL": ""}}`)
	control.Dir = repo
	control.Stdin = strings.NewReader(prompt)
	out, err := control.Output()
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
