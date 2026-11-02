package real

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

	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/provider/claudecode"
	"deck2/internal/sandbox"
	"deck2/internal/store/db"
	"deck2/tests/harness"

	"github.com/google/uuid"
)

var realManifest = harness.Manifest(`{
  "collections": [{
    "name": "ideas", "description": "Short ideas", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "score": {"type": "integer"}, "topic": {"type": "string"}},
               "required": ["title", "score", "topic"]}
  }],
  "commands": [{
    "name": "collect", "title": "Collect ideas", "description": "Writes ideas", "class": "internal",
    "executor": {"type": "agent", "prompt": "First ask the owner with the AskUserQuestion tool which topic to use, with exactly two options: go and rust. Then write two short project ideas about the chosen topic into the ideas collection using the record_upsert tool (score 1-10, topic = the chosen topic). Do not use any other tools. Finish with the number of records written."},
    "result": {"type": "object", "properties": {"written": {"type": "integer"}}, "required": ["written"]}
  }],
  "triggers": [],
  "screens": [{"name": "main", "title": "Ideas", "blocks": [{"type": "table", "title": "Ideas",
    "source": {"collection": "ideas"}, "columns": [{"label": "Idea", "bind": "title"}, {"label": "Topic", "bind": "topic"}]}]}]
}`)

// TestRealClaudeCodeQuestionResume runs the stage 0 acceptance scenario on the installed Claude Code CLI.
func TestRealClaudeCodeQuestionResume(t *testing.T) {
	realCLI(t)
	home := realHome(t)
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(claudecode.New(home, nil)), harness.WithWait(4*time.Minute))

	p := e.Project("ideas", realManifest)
	id := e.Run(p, "collect")
	stopped := e.WaitState(id, "waiting_answer", "done", "error")
	if stopped.State != "waiting_answer" {
		t.Fatalf("agent should stop waiting for the owner, got %+v", stopped)
	}
	a1 := e.WaitAsk()
	if a1.Kind != "question" {
		t.Fatalf("unexpected ask %+v", a1)
	}
	t.Logf("question: %s", a1.Text)
	e.Answer(a1.ID, "rust")
	got := e.WaitState(id, "done", "error")
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
	if rows := e.ScreenRows(p, "main"); len(rows) != len(recs) {
		t.Fatalf("screen shows %d rows, want %d", len(rows), len(recs))
	}
}

func realCLI(t *testing.T) {
	harness.Needs(t, "DECK_REAL_CLI")
}

// realHome is a folder of deck for a test of the real CLI. In a box of its own the CLI signs in once per deck, so on
// Linux DECK_REAL_CLI names the folder of a login deck made (agents/claude/login), which every home of a test shares.
func realHome(t *testing.T) string {
	home := t.TempDir()
	if !sandbox.Homed() {
		return home
	}
	login := harness.Needs(t, "DECK_REAL_CLI")
	if !filepath.IsAbs(login) {
		t.Skip("on Linux DECK_REAL_CLI names the folder of a login of Claude Code that deck made")
	}
	if err := os.MkdirAll(filepath.Dir(deckhome.Layout(home).Login("claude")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(login, deckhome.Layout(home).Login("claude")); err != nil {
		t.Fatal(err)
	}
	return home
}

// TestRealClaudeCodeKeepsSubscription checks that API variables of deck's environment do not reach the CLI.
func TestRealClaudeCodeKeepsSubscription(t *testing.T) {
	realCLI(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-invalid")
	t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:1")
	sid := uuid.NewString()
	res, err := claudecode.New(realHome(t), nil).Run(context.Background(), provider.Request{Cwd: t.TempDir(),
		Prompt: "Reply with just: ok", SessionID: sid, MCPURL: "http://127.0.0.1:1/mcp/none"})
	t.Logf("result: %+v", res)
	if err != nil || res.Fault != nil || !strings.Contains(strings.ToLower(res.Text), "ok") {
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
	home := realHome(t)
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(claudecode.New(home, nil)))
	cmd, _ := json.Marshal(map[string]any{"name": "markers", "title": "Markers", "description": "Names the marker words.", "class": "internal",
		"result":   map[string]any{"type": "object", "required": []string{"markers"}, "properties": map[string]any{"markers": map[string]any{"type": "string"}}},
		"executor": map[string]any{"type": "agent", "folder": true, "prompt": prompt + " The result is those words in markers."}})
	p := e.Project("code", harness.Manifest(`{"commands": [`)+string(cmd)+`]}`)
	e.Must("deck.folder_add", map[string]any{"project": "code", "path": repo}, nil)
	id := e.Run(p, "markers")
	got := e.WaitState(id, "done", "error")
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
	if sandbox.Homed() {
		// The user of the machine has no login of its own; the control run takes deck's.
		control.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+deckhome.Layout(home).Login("claude"))
	}
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
		res, err := claudecode.New(realHome(t), nil).Run(context.Background(), provider.Request{Cwd: t.TempDir(), Prompt: prompt,
			SessionID: uuid.NewString(), MCPURL: "http://127.0.0.1:1/mcp/none", Skills: skills, AllowedTools: []string{"Skill"}})
		if err != nil || res.Fault != nil {
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
	b, _ := os.ReadFile(harness.Testdata("claude/fake.sh"))
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
	harness.Needs(t, "DECK_REAL_CLI")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := claudecode.New(realHome(t), nil).SelfCheck(ctx)
	if c.Err != nil || c.Version == "" {
		t.Fatalf("the self-check of the real CLI is green: %+v", c)
	}
	t.Logf("self-check of %s is green", c.Version)
}

// TestRealClaudeCodeProjectsRefreshTheOneLoginTogether: turns of several projects whose access ran out refresh the
// one login at once; the lock of the refresh is one for all homes, so every turn runs and the login lives on.
func TestRealClaudeCodeProjectsRefreshTheOneLoginTogether(t *testing.T) {
	realCLI(t)
	if !sandbox.Homed() {
		t.Skip("the homes of projects share one login on Linux")
	}
	home := realHome(t)
	file := filepath.Join(deckhome.Layout(home).Login("claude"), ".credentials.json")
	expire := func() int64 {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var login map[string]map[string]any
		if err := json.Unmarshal(raw, &login); err != nil {
			t.Fatal(err)
		}
		at, _ := login["claudeAiOauth"]["expiresAt"].(float64)
		return int64(at)
	}
	raw, _ := os.ReadFile(file)
	var login map[string]map[string]any
	_ = json.Unmarshal(raw, &login)
	login["claudeAiOauth"]["expiresAt"] = time.Now().Add(-time.Minute).UnixMilli()
	out, _ := json.Marshal(login)
	// In place, as the CLI writes it: the homes see the file of the login by its inode.
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write(out)
	if cerr := f.Close(); err != nil || cerr != nil {
		t.Fatal(err, cerr)
	}
	a := claudecode.New(home, nil).With("haiku", "haiku", "")
	type turn struct {
		res provider.Result
		err error
	}
	results := make(chan turn, 6)
	for project := int64(1); project <= 6; project++ {
		go func() {
			res, err := a.Run(context.Background(), provider.Request{Project: project, Cwd: t.TempDir(), Prompt: "Reply with just: ok",
				SessionID: uuid.NewString(), MCPURL: "http://127.0.0.1:1/mcp/none"})
			results <- turn{res, err}
		}()
	}
	for range 6 {
		if got := harness.Receive(t, "a turn", results); got.err != nil || got.res.Fault != nil || !strings.Contains(strings.ToLower(got.res.Text), "ok") {
			t.Fatalf("every turn runs on the one login: %+v %v", got.res, got.err)
		}
	}
	if expire() < time.Now().UnixMilli() {
		t.Fatalf("the login is refreshed and kept: %d", expire())
	}
}

// TestRealClaudeCodeResumesASessionStartedBeforeItsHome: a session of a turn that waits, started when the CLI kept it in
// the owner's own home, goes on in the home of its project once deck moved it there.
func TestRealClaudeCodeResumesASessionStartedBeforeItsHome(t *testing.T) {
	realCLI(t)
	if !sandbox.Homed() {
		t.Skip("the CLI has homes of projects on Linux")
	}
	home := realHome(t)
	a := claudecode.New(home, nil).With("haiku", "haiku", "")
	work, sid, word := t.TempDir(), uuid.NewString(), "PERSIMMON-"+uuid.NewString()[:4]
	if _, err := a.Run(context.Background(), provider.Request{Project: 9, Cwd: work, SessionID: sid, MCPURL: "http://127.0.0.1:1/mcp/none",
		Prompt: "Remember the word " + word + ". Reply with just: ok"}); err != nil {
		t.Fatal(err)
	}
	// The owner's own home of an older deck holds the session; the home of the project does not yet.
	user := t.TempDir()
	t.Setenv("HOME", user)
	kept := filepath.Join(deckhome.Layout(home).Agent("claude", 9), "projects")
	if err := os.Rename(kept, filepath.Join(user, ".claude", "projects")); err != nil {
		if err := os.MkdirAll(filepath.Join(user, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(kept, filepath.Join(user, ".claude", "projects")); err != nil {
			t.Fatal(err)
		}
	}
	if err := claudecode.New(home, nil).Move(10, sid); err != nil {
		t.Fatal(err)
	}
	res, err := a.Run(context.Background(), provider.Request{Project: 10, Cwd: work, SessionID: sid, Resume: true, MCPURL: "http://127.0.0.1:1/mcp/none",
		Prompt: "Which word did I ask you to remember? Reply with just the word."})
	if err != nil || res.Fault != nil || !strings.Contains(res.Text, word) {
		t.Fatalf("the moved session goes on with what it knew: %+v %v", res, err)
	}
}
