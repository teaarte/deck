package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deck2/internal/api"
	"deck2/internal/app"
	"deck2/internal/provider/codex"
	"deck2/web"
)

func realCodex(t *testing.T) {
	t.Helper()
	if os.Getenv("DECK_REAL_CODEX") != "1" {
		t.Skip("set DECK_REAL_CODEX=1 to run against the installed Codex")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex is not installed")
	}
}

// TestRealCodexSelfCheck runs the self-check of the Codex adapter on the installed CLI and the owner's login.
func TestRealCodexSelfCheck(t *testing.T) {
	realCodex(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	start := time.Now()
	c := codex.New(t.TempDir()).SelfCheck(ctx)
	t.Logf("codex %s: self-check in %s", c.Version, time.Since(start).Round(time.Second))
	if c.Err != nil || c.RateLimited || c.NeedsLogin {
		t.Fatalf("self-check: %+v", c)
	}
}

func codexManifest(name, url, extra, home string) string {
	return fmt.Sprintf(`{"version": 5%s,
  "commands": [{"name": "work", "title": "Work", "description": "Writes a file, searches the web and posts a note", "class": "internal",
    "result": {"type": "object", "required": ["title", "web", "curl", "token"], "properties": {"title": {"type": "string"}, "web": {"type": "boolean"}, "curl": {"type": "string"}, "token": {"type": "string"}}},
    "executor": {"type": "agent", "prompt": "Do these steps in order. 1. Run this shell command: echo deck-ok > marker.txt\n2. Use your web search to find the home page of the Go programming language and take its page title. If you have no web search or it fails, use an empty title and web false, and do not try any other way (no curl, no scripts).\n3. Call the %s__post tool of the deck MCP server with id a1 and text hi.\n4. Run this shell command once: curl -s -m 10 -o /dev/null -w '%%{http_code}' https://example.com and keep what it printed as curl, even if it failed. Do not ask for approval.\n5. Run this shell command once: cat %s/token ; keep what it printed as token, or empty when refused; do not try any other way and do not ask for approval.\nThe result: the title, whether the web worked, curl and token."}}],
  "tools": {"http": [{"name": "post", "description": "Posts a note", "method": "POST", "url": "%s/items/{id}",
    "body": {"text": "{text}"},
    "input": {"type": "object", "properties": {"id": {"type": "string"}, "text": {"type": "string"}}, "required": ["id", "text"]}}]}}`, extra, name, home, url)
}

// TestRealCodexAutonomy runs a command agent on the installed Codex: its file and the web need no yes, the external
// tool waits for one, the owner's token stays unread; without network there is no web and no network in the shell.
func TestRealCodexAutonomy(t *testing.T) {
	realCodex(t)
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{"ok": true}`) })
	home := t.TempDir()
	ts := httptest.NewUnstartedServer(nil)
	base := "http://" + ts.Listener.Addr().String()
	a, err := app.New(app.Config{Home: home, MCPBase: base, QuestionWait: 4 * time.Minute, ConfirmWait: 4 * time.Minute}, codex.New(home))
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = api.New(a, token, web.FS()).Handler()
	ts.Start()
	t.Cleanup(func() { _ = a.Close(); ts.Close() })
	e := &Env{T: t, App: a, URL: base, names: map[int64]string{}}

	owner := []byte("owner-token-" + fmt.Sprint(time.Now().UnixNano()))
	if err := os.WriteFile(filepath.Join(home, "token"), owner, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, extra string
		web         bool
	}{{"open", "", true}, {"closed", `, "no_network": true`, false}} {
		p := e.createProject(tc.name, codexManifest(tc.name, svc.URL, tc.extra, home))
		id := e.run(p, "work")
		var asks []askView
		confirmed := 0
		answered := map[int64]bool{}
		deadline := time.Now().Add(6 * time.Minute)
		for st := e.process(id).State; time.Now().Before(deadline) && st != "done" && st != "error"; st = e.process(id).State {
			_, out := e.call("deck.asks_list", nil)
			_ = json.Unmarshal(out, &asks)
			for _, ask := range asks {
				if answered[ask.ID] {
					continue
				}
				answered[ask.ID] = true
				if ask.Kind != "call" || !strings.Contains(ask.Text, tc.name+".post") {
					t.Fatalf("%s: only the external deck tool waits for the owner, got %+v", tc.name, ask)
				}
				t.Logf("%s: confirm: %s", tc.name, ask.Text)
				e.answer(ask.ID, "allow")
				confirmed++
			}
			time.Sleep(500 * time.Millisecond)
		}
		got := e.process(id)
		b, _ := os.ReadFile(filepath.Join(home, "runs", itoa(id), "work", "marker.txt"))
		t.Logf("%s: state %s, file %q, result %s, error %s", tc.name, got.State, strings.TrimSpace(string(b)), got.Result, got.Error)
		var res struct {
			Title, Curl, Token string
			Web                bool
		}
		_ = json.Unmarshal(got.Result, &res)
		if got.State != "done" || strings.TrimSpace(string(b)) != "deck-ok" || res.Web != tc.web || tc.web && res.Title == "" || confirmed != 1 ||
			res.Token != "" || strings.Contains(string(got.Result), strings.TrimSpace(string(owner))) || !tc.web && res.Curl == "200" {
			t.Fatalf("%s: want done with the file written and web %v: %+v", tc.name, tc.web, got)
		}
	}
	if n := len(svc.requests()); n != 2 {
		t.Fatalf("deck made the external call once per project after yes: %d", n)
	}
}

// TestRealCodexQuestion has a command agent on the installed Codex ask the owner through the ask tool of deck
// and carry the answer into its result.
func TestRealCodexQuestion(t *testing.T) {
	realCodex(t)
	home := t.TempDir()
	ts := httptest.NewUnstartedServer(nil)
	base := "http://" + ts.Listener.Addr().String()
	a, err := app.New(app.Config{Home: home, MCPBase: base, QuestionWait: 4 * time.Minute, ConfirmWait: 4 * time.Minute}, codex.New(home))
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = api.New(a, token, web.FS()).Handler()
	ts.Start()
	t.Cleanup(func() { _ = a.Close(); ts.Close() })
	e := &Env{T: t, App: a, URL: base, names: map[int64]string{}}
	p := e.createProject("ask", `{"version": 5,
  "commands": [{"name": "work", "title": "Work", "description": "Asks the owner", "class": "internal",
    "result": {"type": "object", "required": ["color"], "properties": {"color": {"type": "string"}}},
    "executor": {"type": "agent", "prompt": "Ask the owner which color the report should use, with the options red and blue. The result is the color the owner picked."}}]}`)
	id := e.run(p, "work")
	deadline := time.Now().Add(5 * time.Minute)
	asked := false
	for st := e.process(id).State; time.Now().Before(deadline) && st != "done" && st != "error"; st = e.process(id).State {
		var asks []askView
		_, out := e.call("deck.asks_list", nil)
		_ = json.Unmarshal(out, &asks)
		for _, ask := range asks {
			if ask.Kind != "question" || !strings.Contains(strings.ToLower(ask.Text), "color") {
				t.Fatalf("only the question waits for the owner, got %+v", ask)
			}
			t.Logf("question: %s", ask.Text)
			e.answer(ask.ID, "blue")
			asked = true
		}
		time.Sleep(500 * time.Millisecond)
	}
	got := e.process(id)
	t.Logf("state %s, result %s, error %s", got.State, got.Result, got.Error)
	if !asked || got.State != "done" || !strings.Contains(string(got.Result), `"blue"`) {
		t.Fatalf("want the owner's answer in the result: %+v", got)
	}
}
