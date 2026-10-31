package scenarios

import (
	"context"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/store/db"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestWideningChangeWaitsForTheOwnersButton(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Project("notes", harness.PlainManifest)
	_, tok := e.Token("terminal", "deck.*")
	wider := strings.Replace(harness.PlainManifest, `"screens": []`, `"screens": [], "domains": {"read": ["example.com"]}`, 1)

	var w struct {
		Revision int64
		Waiting  int64
		Widens   []string
	}
	code, out := e.As(tok).Call("deck.manifest_save", map[string]any{"project": "notes", "manifest": wider, "reason": "go faster"})
	if code != 200 || json.Unmarshal(out, &w) != nil || w.Waiting == 0 || w.Revision != 0 ||
		!slices.Equal(w.Widens, []string{"read domain: example.com"}) {
		t.Fatalf("a change that widens rights waits: %d %s", code, out)
	}
	if rev := e.ManifestRevision("notes"); rev != 1 {
		t.Fatalf("the manifest stays until the owner allows: revision %d", rev)
	}
	broken := strings.Replace(wider, `"screens": [],`, `"screens": [], "reads": [{"project": "nowhere"}],`, 1)
	if code, out := e.As(tok).Call("deck.manifest_save", map[string]any{"project": "notes", "manifest": broken}); code != 422 || strings.Contains(string(out), "waiting") {
		t.Fatalf("a widening change is checked in full before the owner is asked: %d %s", code, out)
	}
	if code, out := e.Call("deck.manifest_save", map[string]any{"project": "notes", "manifest": harness.PlainManifest, "if_revision": 7}); code != 409 {
		t.Fatalf("if_revision guards a save: %d %s", code, out)
	}
	a := e.WaitAsk()
	if a.Kind != "grant" || a.Text != "go faster" {
		t.Fatalf("the owner sees what the change asks for: %+v", a)
	}
	if code, _ := e.As(tok).Call("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"}); code != 403 {
		t.Fatalf("the master cannot allow its own change: %d", code)
	}
	e.Answer(a.ID, "allow")
	if rev := e.ManifestRevision("notes"); rev != 2 {
		t.Fatalf("allow applies the frozen change: revision %d", rev)
	}
	var feed []any
	e.Must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) != 0 {
		t.Fatalf("a change the owner allowed is not done without them: %v", feed)
	}

	code, out = e.As(tok).Call("deck.document_revert", map[string]any{"document": manifestDocument(e, "notes"), "revision": 1})
	if code != 200 || !strings.Contains(string(out), `"revision":3`) {
		t.Fatalf("a revert that narrows rights applies at once: %d %s", code, out)
	}
	code, out = e.As(tok).Call("deck.document_revert", map[string]any{"document": manifestDocument(e, "notes"), "revision": 2})
	if code != 200 || !strings.Contains(string(out), `"waiting"`) {
		t.Fatalf("a revert that widens rights waits too: %d %s", code, out)
	}
	stale := e.WaitAsk()
	e.Must("deck.manifest_save", map[string]any{"project": "notes", "manifest": strings.Replace(harness.PlainManifest, `"Does work"`, `"Does the work"`, 1), "reason": "owner"}, nil)
	if n := e.WaitNote("outdated"); !strings.Contains(n.Text, "notes") || !strings.Contains(n.Text, "revision 4") {
		t.Fatalf("the owner hears that a request for rights older than the manifest is outdated: %+v", n)
	}
	var calls []struct{ Tool, Outcome string }
	e.Must("deck.calls_list", map[string]any{"project": "notes"}, &calls)
	if !slices.ContainsFunc(calls, func(c struct{ Tool, Outcome string }) bool { return c.Outcome == "expired" }) {
		t.Fatalf("the journal says the outdated request expired: %+v", calls)
	}
	if code, out := e.Call("deck.ask_answer", map[string]any{"ask": stale.ID, "answer": "allow"}); code == 200 {
		t.Fatalf("an outdated request for rights is closed: %d %s", code, out)
	}
	if rev := e.ManifestRevision("notes"); rev != 4 {
		t.Fatalf("the outdated change is not applied: revision %d", rev)
	}
}

func TestWhatCountsAsWideningRights(t *testing.T) {
	t.Parallel()
	base := `{"version": 13, "commands": [{"name": "run", "title": "Run", "description": "Runs.", "class": "external",
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
			e := harness.New(t, harness.WithAgent(harness.Ok))
			e.Project("other", harness.PlainManifest)
			e.Project("notes", fmt.Sprintf(base, c.from))
			var cmd = strings.Replace(fmt.Sprintf(base, c.to), `"class": "external"`, `"class": "internal"`, 1)
			_, tok := e.Token("terminal", "deck.*")
			_, out := e.As(tok).Call("deck.manifest_save", map[string]any{"project": "notes", "manifest": fmt.Sprintf(base, c.to)})
			if !strings.Contains(string(out), c.want) {
				t.Fatalf("want %q, got %s", c.want, out)
			}
			_, out = e.As(tok).Call("deck.manifest_save", map[string]any{"project": "notes", "manifest": cmd})
			if !strings.Contains(string(out), "command run: class internal instead of external") {
				t.Fatalf("a milder command class widens too: %s", out)
			}
		})
	}
}

// TestCommandRightsWidenToo: dropping no_cloud, another provider and more callers of a command wait for the owner,
// so a private command does not go to the cloud without a yes.
func TestCommandRightsWidenToo(t *testing.T) {
	t.Parallel()
	cmd := `{"version": 13, "commands": [{"name": "run", "title": "Run", "description": "Runs.", "class": "internal", %s
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
			e := harness.New(t, harness.WithAgent(harness.Ok))
			e.Project("notes", from)
			_, tok := e.Token("terminal", "deck.*")
			_, out := e.As(tok).Call("deck.manifest_save", map[string]any{"project": "notes", "manifest": fmt.Sprintf(cmd, c.to)})
			var w struct {
				Waiting int64
				Widens  []string
			}
			if json.Unmarshal(out, &w) != nil || w.Waiting == 0 || !slices.Equal(w.Widens, []string{c.want}) {
				t.Fatalf("want only %q waiting for the owner, got %s", c.want, out)
			}
		})
	}
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Project("notes", from)
	_, tok := e.Token("terminal", "deck.*")
	if _, out := e.As(tok).Call("deck.manifest_save", map[string]any{"project": "notes", "manifest": renamed}); !strings.Contains(string(out), "command run2: new without no_cloud") {
		t.Fatalf("a renamed command does not drop no_cloud quietly: %s", out)
	}
}

func TestSecretsOfAuthWidenRightsLikeSecretsInHeaders(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Project("notes", `{"version": 13, "tools": {"http": [{"name": "own", "description": "d", "method": "GET",
	  "url": "https://service.example/x", "auth": {"type": "secret", "secret": "shared"}}]}}`)
	_, tok := e.Token("terminal", "deck.*")
	for _, c := range []struct{ auth, want string }{
		{`{"type": "secret", "secret": "github_token"}`, "secret: github_token"},
		{`{"type": "secret", "secret": "shared"}`, "secret: shared"},
		{`{"type": "oauth2", "auth_url": "https://a.example/auth", "token_url": "https://a.example/token", "client_id": "cid",
		   "client_secret": "csecret", "token": "tok"}`, "secret: csecret"},
	} {
		m := fmt.Sprintf(`{"version": 13, "tools": {"http": [{"name": "own", "description": "d", "method": "GET",
		  "url": "https://service.example/x", "auth": {"type": "secret", "secret": "shared"}},
		  {"name": "grab", "description": "d", "method": "GET", "url": "https://attacker.example/x", "auth": %s}]}}`, c.auth)
		var w struct {
			Waiting int64
			Widens  []string
		}
		code, out := e.As(tok).Call("deck.manifest_save", map[string]any{"project": "notes", "manifest": m})
		if code != 200 || json.Unmarshal(out, &w) != nil || w.Waiting == 0 || !slices.Contains(w.Widens, c.want) {
			t.Fatalf("a new tool that signs in with a secret waits for the owner's button: %d %s", code, out)
		}
	}
}

// TestAWideningWaitsForTheOwnerInEveryRight: a revision that widens rights is in force only with what the owner
// granted: a milder class waits for the owner's button, and a draft keeps the class of its granted revision.
func TestAWideningWaitsForTheOwnerInEveryRight(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("pub", harness.ScriptManifest("media", "", ""))
	_, tok := e.Token("terminal", "deck.*", "pub.*")
	milder := strings.Replace(harness.ScriptManifest("media", "", ""), `"class": "external",
     "executor": {"type": "agent", "prompt": "Publish."}`, `"class": "reversible",
     "executor": {"type": "agent", "prompt": "Publish."}`, 1)
	var w struct {
		Waiting int64
		Widens  []string
	}
	code, raw := e.As(tok).Call("deck.manifest_save", map[string]any{"project": "pub", "manifest": milder})
	if code != 200 || json.Unmarshal(raw, &w) != nil || w.Waiting == 0 || len(w.Widens) != 1 || !strings.Contains(w.Widens[0], "class reversible") {
		t.Fatalf("a milder class waits for the owner: %d %s", code, raw)
	}
	before := len(e.Procs())
	code, raw = e.As(tok).Call("pub.publish", map[string]any{})
	var held struct{ Waiting int64 }
	if code != 200 || json.Unmarshal(raw, &held) != nil || held.Waiting == 0 || len(e.Procs()) != before {
		t.Fatalf("until the owner grants it the command keeps its class and waits: %d %s", code, raw)
	}
	e.Refused("deck.ask_answer", map[string]any{"ask": w.Waiting, "answer": "retry"}, 422, "answer a grant with allow, deny")
	e.Answer(w.Waiting, "allow")
	e.Answer(held.Waiting, "deny")
	if code, raw = e.As(tok).Call("pub.publish", map[string]any{}); code != 200 || strings.Contains(string(raw), "waiting") {
		t.Fatalf("once granted the milder class runs at once: %d %s", code, raw)
	}

	if code, raw = e.As(tok).Call("deck.project_create", map[string]any{"name": "draft", "manifest": harness.ScriptManifest("media", "", "")}); code != 200 {
		t.Fatalf("create: %d %s", code, raw)
	}
	internal := strings.Replace(harness.ScriptManifest("media", "", ""), `"class": "external",
     "executor": {"type": "agent", "prompt": "Publish."}`, `"class": "internal",
     "executor": {"type": "agent", "prompt": "Publish."}`, 1)
	if code, raw = e.As(tok).Call("deck.manifest_save", map[string]any{"project": "draft", "manifest": internal}); code != 200 {
		t.Fatalf("a draft saves what it widens, to be granted when enabled: %d %s", code, raw)
	}
	for _, c := range commands(e) {
		if c.Name == "draft.publish" && c.Class != "external" {
			t.Fatalf("a draft's command keeps the class the owner granted until the owner enables it: %+v", c)
		}
	}
	wide := strings.Replace(internal, `"Publish."}`, `"Publish."}, "callers": ["owner", "agent"], "provider": "codex"`, 1)
	wide = strings.Replace(wide, `"runtime": "python"}]`, `"runtime": "python"}], "domains": {"read": ["example.com"]}, "models": ["openrouter"], "no_network": false,
  "tools": {"mcp": {"servers": {"svc": {"command": "/bin/cat", "env": {"KEY": "$secret.svc"}}, "open": {"command": "/bin/cat", "classes": {"x": "internal"}}}}}`, 1)
	if code, raw = e.As(tok).Call("deck.manifest_save", map[string]any{"project": "draft", "manifest": wide}); code != 200 {
		t.Fatalf("save: %d %s", code, raw)
	}
	ctx := context.Background()
	p, _ := e.App.Store.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: 1, Name: "draft"})
	cur, err := docs.Manifest(ctx, e.App.Store.Q, p)
	if err != nil {
		t.Fatal(err)
	}
	granted, _ := docs.ManifestAt(ctx, e.App.Store.Q, p.ID, p.GrantedRevision.Int64)
	inForce, _ := docs.InForce(ctx, e.App.Store.Q, p, cur)
	if w := manifest.Widens(granted, cur); len(w) < 6 {
		t.Fatalf("the saved revision widens class, callers, provider, domains, models and servers: %v", w)
	}
	for _, w := range manifest.Widens(granted, inForce) {
		t.Errorf("what is in force widens nothing the owner did not grant: %s", w)
	}
	if len(inForce.Tools.MCP.Servers) != 0 {
		t.Fatalf("a new server does not run before the owner grants it, with a secret or without: %+v", inForce.Tools.MCP.Servers)
	}
}

// TestARevisionNarrowedSinceAProcessStartedLeavesItTheGrantedCalls: a process resumed on its older revision gets the
// calls the owner granted since, not none; a pattern in the grant covers the names under it.
func TestARevisionNarrowedSinceAProcessStartedLeavesItTheGrantedCalls(t *testing.T) {
	t.Parallel()
	var outs []string
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		out, _ := c.Tool(ctx, "records_list", map[string]any{"collection": "items"})
		outs = append(outs, out)
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))

	m := harness.TestManifest([]string{harness.AgentCommand("work", "")}, nil)
	m = strings.Replace(m, `"version": 13,`, `"version": 13, "collections": [{"name": "items", "description": "An item.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}],`, 1)
	p := e.Project("calls", m)
	id := e.Run(p, "work")
	e.WaitState(id, "done")
	narrowed := strings.Replace(m, `"prompt": "Do work."}`, `"prompt": "Do work.", "calls": ["deck.*"]}`, 1)
	if narrowed == m {
		t.Fatal("fixture replacement did not apply")
	}
	e.Must("deck.manifest_save", map[string]any{"project": "calls", "manifest": narrowed}, nil)
	e.Must("deck.process_rework", map[string]any{"process": id, "remark": "once more"}, nil)
	e.WaitState(id, "done", "error")
	if len(outs) != 2 || outs[1] != outs[0] {
		t.Fatalf("the resumed process may call what the grant lists: %v", outs)
	}
}

// TestAnotherFolderOfACommandWidensRights: a command whose agent moves to another folder of the project, or into one,
// writes where the owner did not grant; dropping its folder narrows.
func TestAnotherFolderOfACommandWidensRights(t *testing.T) {
	t.Parallel()
	m := func(folder string) string {
		return `{"version": 13, "commands": [{"name": "run", "title": "Run", "description": "Runs.", "class": "external",
    "executor": {"type": "agent", ` + folder + `"prompt": "Run."}}]}`
	}
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Project("code", m(`"folder": "main", `))
	_, tok := e.Token("terminal", "deck.*")
	for _, c := range []struct{ to, want string }{
		{m(`"folder": "docs", `), "command run: works in folder docs"},
		{m(`"folder": true, `), "command run: works in the one folder of the project"},
		{strings.Replace(m(`"folder": "main", `), `]}`, `, {"name": "fix", "title": "Fix", "description": "Fixes.", "class": "external",
    "executor": {"type": "agent", "folder": "main", "prompt": "Fix."}}]}`, 1), "command fix: works in folder main"},
	} {
		if _, out := e.As(tok).Call("deck.manifest_save", map[string]any{"project": "code", "manifest": c.to}); !strings.Contains(string(out), c.want) {
			t.Fatalf("want %q, got %s", c.want, out)
		}
	}
	if _, out := e.As(tok).Call("deck.manifest_save", map[string]any{"project": "code", "manifest": m("")}); strings.Contains(string(out), "folder") {
		t.Fatalf("a command that leaves its folder widens nothing: %s", out)
	}
}

// TestAnotherExecutorOfACommandWidensRights: a command that runs another tool or another kind of executor reaches
// what the owner did not grant for it, as a new tool would.
func TestAnotherExecutorOfACommandWidensRights(t *testing.T) {
	t.Parallel()
	m := func(executor string) string {
		return `{"version": 13, "commands": [{"name": "digest", "title": "Digest", "description": "Sends the digest.", "class": "external",
    "executor": ` + executor + `}],
  "tools": {"http": [{"name": "send_digest", "description": "Sends", "method": "POST", "url": "https://a.example/digest", "class": "external"},
    {"name": "post_anything", "description": "Posts", "method": "POST", "url": "https://b.example/x", "class": "external"}]},
  "allowed_external": ["digest"], "domains": {"act": ["a.example", "b.example"]}}`
	}
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Project("mail", m(`{"type": "tool", "tool": "send_digest"}`))
	_, tok := e.Token("terminal", "deck.*")
	for _, c := range []struct{ to, want string }{
		{m(`{"type": "tool", "tool": "post_anything"}`), "command digest: runs tool post_anything instead of send_digest"},
		{m(`{"type": "agent", "prompt": "Send it."}`), "command digest: runs an agent instead of a tool"},
	} {
		if _, out := e.As(tok).Call("deck.manifest_save", map[string]any{"project": "mail", "manifest": c.to}); !strings.Contains(string(out), c.want) {
			t.Fatalf("want %q, got %s", c.want, out)
		}
	}
}

// TestADomainTheGrantCoversIsNoNewRight: a read domain under a granted "*" or a package registry is no new right,
// so the master saves it without the owner and a draft keeps its grant.
func TestADomainTheGrantCoversIsNoNewRight(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	base := `{"version": 13, "domains": {"read": [%s]}, "commands": [` + harness.AgentCommand("work", "") + `]}`
	e.Project("wide", fmt.Sprintf(base, `"*"`))
	e.Project("plain", fmt.Sprintf(base, ``))
	_, tok := e.Token("terminal", "deck.*")
	if code, raw := e.As(tok).Call("deck.project_create", map[string]any{"name": "sketch", "manifest": fmt.Sprintf(base, ``)}); code != 200 {
		t.Fatalf("create: %d %s", code, raw)
	}
	for _, s := range []struct{ project, read string }{{"wide", `"*", "example.com"`}, {"plain", `"pypi.org"`}, {"sketch", `"pypi.org"`}} {
		if code, raw := e.As(tok).Call("deck.manifest_save", map[string]any{"project": s.project, "manifest": fmt.Sprintf(base, s.read)}); code != 200 || strings.Contains(string(raw), "waiting") {
			t.Fatalf("%s: a domain the grant covers is saved without the owner: %d %s", s.project, code, raw)
		}
	}
	for _, addr := range []string{`"*", "[10.0.0.1]"`, `"*", "10.0.0.0/8"`} {
		if code, raw := e.As(tok).Call("deck.manifest_save", map[string]any{"project": "wide", "manifest": fmt.Sprintf(base, addr)}); code != 200 || !strings.Contains(string(raw), "waiting") {
			t.Fatalf("an address is not under \"*\": %s: %d %s", addr, code, raw)
		}
	}
	ctx := context.Background()
	p, _ := e.App.Store.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: 1, Name: "sketch"})
	if m, err := docs.Manifest(ctx, e.App.Store.Q, p); err != nil || p.GrantedRevision.Int64 != m.Revision {
		t.Fatalf("a draft that widens nothing keeps its grant on the new revision: %d, %v", p.GrantedRevision.Int64, err)
	}
}
