package scenarios

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"deck2/internal/provider"
)

func browserManifest(domains string) string {
	return `{"version": 5,
  "commands": [{"name": "browse", "title": "Browse", "description": "Browses.", "class": "internal",
    "input": {"type": "object", "properties": {"steps": {"type": "string"}}},
    "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Browse."}}],
  "domains": ` + domains + `}`
}

type step struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
}

// steps is the input of the browse command: the gateway tools the fake agent calls in order.
func steps(s ...step) map[string]any {
	b, _ := json.Marshal(s)
	return map[string]any{"steps": string(b)}
}

// seqAgent calls the steps of its input on a fresh start and keeps every answer; files names files it
// writes into its results folder first.
type seqAgent struct {
	mu    sync.Mutex
	got   []toolCall
	tools []string
	files []string
}

func (s *seqAgent) script(ctx context.Context, c *Call) provider.Result {
	m := inputOf.FindStringSubmatch(c.Req.Prompt)
	if m == nil {
		return provider.Result{Structured: json.RawMessage(`{"resumed": true}`)}
	}
	var in struct{ Steps string }
	_ = json.Unmarshal([]byte(m[1]), &in)
	var list []step
	_ = json.Unmarshal([]byte(in.Steps), &list)
	for _, f := range s.files {
		_ = os.WriteFile(filepath.Join(c.Req.Cwd, "..", "results", f), []byte("file"), 0o600)
	}
	s.mu.Lock()
	s.tools = c.Req.AllowedTools
	s.mu.Unlock()
	for _, st := range list {
		out, isErr := c.Tool(ctx, st.Tool, st.Args)
		s.mu.Lock()
		s.got = append(s.got, toolCall{out, isErr})
		s.mu.Unlock()
	}
	return provider.Result{Structured: json.RawMessage(`{}`)}
}

func (s *seqAgent) last(n int) []toolCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]toolCall{}, s.got[len(s.got)-n:]...)
}

func nav(url string) step   { return step{"browser_navigate", map[string]any{"url": url}} }
func click(ref string) step { return step{"browser_click", map[string]any{"target": ref}} }

func TestBrowserReadsAnywhereListedAndActsOnlyOnActDomains(t *testing.T) {
	ag := &seqAgent{}
	e := newEnv(t, ag.script)
	e.createProject("web", browserManifest(`{"read": ["news.example"], "act": ["shop.example"]}`))

	e.waitState(e.start("web", "browse", steps(nav("https://shop.example/cart"), click("e2"))), "done")
	if got := ag.last(2); got[0].isErr || got[1].isErr || !slices.Contains(fakeLog(e.Browser), "click:https://shop.example/cart") {
		t.Fatalf("a click on an act domain runs at once: %+v %v", got, fakeLog(e.Browser))
	}
	if slices.Contains(ag.tools, "mcp__deck__browser_run_code_unsafe") || !slices.Contains(ag.tools, "mcp__deck__browser_click") {
		t.Fatalf("the agent gets the browser tools but never the one that runs code: %v", ag.tools)
	}
	if !slices.Contains(fakeLog(e.Browser), "start:headless") {
		t.Fatalf("agents use the browser headless: %v", fakeLog(e.Browser))
	}

	e.waitState(e.start("web", "browse", steps(nav("https://shop.example/pay"))), "done")
	_ = os.WriteFile(filepath.Join(e.Browser, "flip"), []byte("https://news.example/moved"), 0o600)
	e.waitState(e.start("web", "browse", steps(click("e9"))), "done")
	if got := ag.last(1)[0]; !got.isErr || !strings.Contains(got.out, "not on an act domain") || slices.Contains(fakeLog(e.Browser), "click:https://news.example/moved") {
		t.Fatalf("a click whose page changed after the check is not made: %+v", got)
	}

	e.waitState(e.start("web", "browse", steps(nav("https://evil.example/"))), "done")
	if got := ag.last(1)[0]; !got.isErr || !strings.Contains(got.out, "not on a read or act domain") {
		t.Fatalf("a page off the domains is not opened: %+v", got)
	}

	id := e.start("web", "browse", steps(nav("https://news.example/a"), click("e2")))
	e.waitState(id, "waiting_answer")
	if got := ag.last(1)[0]; !strings.Contains(got.out, `"waiting"`) || slices.Contains(fakeLog(e.Browser), "click:https://news.example/a") {
		t.Fatalf("a click on a read domain waits for the owner: %+v", got)
	}
	e.answer(e.waitAsk().ID, "allow")
	e.waitState(id, "done")
	if !slices.Contains(fakeLog(e.Browser), "click:https://news.example/a") {
		t.Fatalf("after yes deck clicks on the page it was asked on: %v", fakeLog(e.Browser))
	}

	id = e.start("web", "browse", steps(nav("https://news.example/b"), click("e3")))
	e.waitState(id, "waiting_answer")
	a := e.waitAsk()
	e.waitState(e.start("web", "browse", steps(nav("https://news.example/c"))), "done")
	if code, out := e.call("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"}); code == 200 || !strings.Contains(string(out), "changed") ||
		slices.Contains(fakeLog(e.Browser), "click:https://news.example/c") {
		t.Fatalf("a yes to a click on a page the browser left is refused: %d %s", code, out)
	}
}

func TestBrowserReadsAnySiteWithAStarAndSendsOnlyOwnFiles(t *testing.T) {
	ag := &seqAgent{files: []string{"cv.pdf"}}
	e := newEnv(t, ag.script)
	e.createProject("web", browserManifest(`{"read": ["*"], "act": ["jobs.example"]}`))
	e.waitState(e.start("web", "browse", steps(nav("https://anything.example/x"), nav("https://jobs.example/apply"),
		step{"browser_file_upload", map[string]any{"paths": []string{"/etc/passwd"}}},
		step{"browser_file_upload", map[string]any{"paths": []string{"results/cv.pdf"}}})), "done")
	got := ag.last(4)
	if got[0].isErr || got[1].isErr {
		t.Fatalf("\"*\" in read opens any site: %+v", got)
	}
	if !got[2].isErr || !strings.Contains(got[2].out, "not a file in the folder") || !slices.Contains(fakeLog(e.Browser), "upload:1") || got[3].isErr {
		t.Fatalf("an upload sends only files of the process's own folder: %+v %v", got, fakeLog(e.Browser))
	}
}

func TestExpiredSiteSessionWaitsForTheOwnersSignIn(t *testing.T) {
	ag := &seqAgent{}
	e := newEnv(t, ag.script)
	e.createProject("web", browserManifest(`{"read": ["mail.example"]}`))
	id := e.start("web", "browse", steps(nav("https://mail.example/inbox"),
		step{"browser_needs_login", map[string]any{"url": "https://mail.example/login", "why": "the inbox shows a sign-in form"}}))
	e.waitState(id, "waiting_answer")
	if h := e.waitHealth("site:mail.example", "needs_login"); !strings.Contains(h.Error, "sign-in form") {
		t.Fatalf("an expired session shows as needs login: %+v", h)
	}
	if a := e.waitAsk(); a.Kind != "sign_in" {
		t.Fatalf("the owner is asked to sign in: %+v", a)
	}
	e.must("deck.browser_login", map[string]any{"url": "https://mail.example/login"}, nil)
	if !slices.Contains(fakeLog(e.Browser), "start:headed") || !slices.Contains(fakeLog(e.Browser), "navigate:https://mail.example/login") {
		t.Fatalf("sign in opens a window of deck's browser at the page: %v", fakeLog(e.Browser))
	}
	e.must("deck.browser_login_done", map[string]any{"url": "https://mail.example/"}, nil)
	if p := e.waitState(id, "done"); !strings.Contains(string(p.Result), "resumed") {
		t.Fatalf("after the sign-in the process goes on: %+v", p)
	}
	e.waitHealth("site:mail.example", "ok")
}

func TestAHungBrowserToolFreesTheBrowserForOtherProjects(t *testing.T) {
	ag := &seqAgent{}
	e := newEnv(t, ag.script)
	e.App.Host.CallTimeout = 500 * time.Millisecond
	e.createProject("one", browserManifest(`{"read": ["*"]}`))
	e.createProject("two", browserManifest(`{"read": ["*"]}`))
	e.waitState(e.start("one", "browse", steps(nav("https://one.example/"))), "done")
	if err := os.WriteFile(filepath.Join(e.Browser, "hang"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	snap := step{"browser_snapshot", map[string]any{}}
	e.waitState(e.start("one", "browse", steps(snap)), "done")
	e.waitState(e.start("two", "browse", steps(nav("https://two.example/"), snap)), "done")
	got := ag.last(3)
	if !got[0].isErr || !strings.Contains(got[0].out, "did not answer") || got[1].isErr || got[2].isErr {
		t.Fatalf("a tool that hangs fails after its deadline and the browser works again for the next project: %+v", got)
	}
}

// TestStarInReadIsOnlyThePublicInternet: "*" does not open the owner's machine or network in the browser.
func TestStarInReadIsOnlyThePublicInternet(t *testing.T) {
	ag := &seqAgent{}
	e := newEnv(t, ag.script)
	e.createProject("web", browserManifest(`{"read": ["*", "127.0.0.1:8080"]}`))
	e.waitState(e.start("web", "browse", steps(nav("http://127.0.0.1:11434/"), nav("http://localhost:3000/"), nav("http://127.0.0.1:8080/"))), "done")
	if got := ag.last(3); !got[0].isErr || !got[1].isErr || got[2].isErr {
		t.Fatalf("a local page opens only when its host:port is named: %+v", got)
	}
}
