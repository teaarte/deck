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

	"deck2/internal/gateway"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
)

const draftManifest = `{
  "version": 13,
  "commands": [
    {"name": "work", "title": "Work", "description": "Does work.", "class": "internal", "input": {"type": "object", "properties": {}},
     "executor": {"type": "agent", "prompt": "Do the work."}, "result": {"type": "object"}},
    {"name": "child", "title": "Child", "description": "Helps.", "class": "internal", "input": {"type": "object", "properties": {"process": {"type": "integer", "format": "process"}}},
     "executor": {"type": "agent", "prompt": "Help."}, "result": {"type": "object"}},
    {"name": "send", "title": "Send", "description": "Sends it out.", "class": "external",
     "executor": {"type": "agent", "prompt": "Send."}, "result": {"type": "object"}}
  ],
  "triggers": [{"command": "work", "cron": "0 9 * * *"}, {"command": "child", "after": "work"}],
  "domains": {"read": ["example.com"]}
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
	out, _ := json.Marshal(map[string]any{"bash": d["message"], "send": send, "child": child})
	return provider.Result{Structured: out}
}

type projectV struct {
	Name     string
	Status   string
	Rights   []string
	Asks     []string
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
	t.Parallel()
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

	e.Clock.Add(2 * time.Hour)
	e.tick()
	e.settle()
	if n := len(e.processes()); n != 0 {
		t.Fatalf("a draft runs on none of its triggers, got %d processes", n)
	}

	var run struct{ Process int64 }
	e.must("notes.work", map[string]any{}, &run)
	got := e.waitState(run.Process, "done")
	var res struct{ Bash, Send, Child string }
	_ = json.Unmarshal(got.Result, &res)
	if res.Bash != gateway.TrialRefusal || !strings.Contains(res.Send, `unknown tool "notes__send"`) {
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

func TestChangesToADraftApplyAtOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	_, tok := e.token("terminal", "deck.*")
	if code, out := e.callWith(tok, "deck.project_create", map[string]any{"name": "notes", "manifest": plainManifest}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	wider := strings.Replace(plainManifest, `"screens": []`, `"screens": [], "domains": {"read": ["example.com"]}`, 1)
	code, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "manifest": wider})
	if code != 200 || !strings.Contains(string(out), `"revision":2`) {
		t.Fatalf("in a draft rights are part of what the owner enables, so the change applies: %d %s", code, out)
	}
	var feed []any
	e.must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) != 0 {
		t.Fatalf("changes to a draft are not done without the owner: %v", feed)
	}
	if p := e.project("notes"); !slices.Equal(p.Rights, []string{"read domain: example.com"}) {
		t.Fatalf("the draft shows the rights enabling it grants: %+v", p)
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
	t.Parallel()
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
	if _, err := e.App.Triggers.Fire(context.Background(), due[0], map[string]any{}); err == nil || !strings.Contains(err.Error(), "moved") {
		t.Fatalf("a firing picked before enable does not run after it: %v", err)
	}
	if n := len(e.processes()); n != 0 {
		t.Fatalf("no process for the missed draft schedule: %d", n)
	}
}

func TestCommandRunsTheRevisionItWasAuthorizedAgainst(t *testing.T) {
	t.Parallel()
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
	e.waitState(out.(process.Started).Process, "done")
	if calls := e.Fake.Calls(); len(calls) != 1 || !strings.Contains(calls[0].Prompt, "OLD PROMPT") || strings.Contains(calls[0].Prompt, "NEW PROMPT") {
		t.Fatalf("the process must run the command of the revision the call was checked against: %+v", calls)
	}
}

// TestAScriptOfADraftInstallsItsPackages: before the owner enables a draft, its scripts reach the package
// registries its domains name and nothing else there.
func TestAScriptOfADraftInstallsItsPackages(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	_, tok := e.token("terminal", "deck.*", "pkgs.*")
	if code, out := e.callWith(tok, "deck.project_create", map[string]any{"name": "pkgs",
		"manifest": scriptManifest("run", "", `, "domains": {"read": ["pypi.org", "example.com"]}`)}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	e.saveScript("pkgs", "scripts/main.py", pythonLib+`
def why(url):
    proxy = os.environ["HTTP_PROXY"]
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({"http": proxy, "https": proxy}))
    try:
        with opener.open(url, timeout=10) as r:
            return "reached"
    except urllib.error.HTTPError as e:
        return e.read().decode()
    except Exception as e:
        return str(e)
print(json.dumps({"registry": why("http://pypi.org/simple/"), "site": why("http://example.com/")}))
`)
	var run struct{ Process int64 }
	e.must("pkgs.run", map[string]any{}, &run)
	p := e.waitScript(run.Process, "done", "error")
	var got struct{ Registry, Site string }
	_ = json.Unmarshal(p.Result, &got)
	if strings.Contains(got.Registry, "not in the domains") || !strings.Contains(got.Site, "not in the domains") {
		t.Fatalf("a draft's script reaches the registries it names, not its other domains: %s", p.Result)
	}
}

// TestADraftSaysWhichToolsWillAskEachTime: before the owner enables a draft, its row names the tools and commands
// that will still wait for the owner on every call.
func TestADraftSaysWhichToolsWillAskEachTime(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	_, tok := e.token("terminal", "deck.*")
	m := strings.Replace(postManifest("http://127.0.0.1:1", ""), `"class": "internal"`, `"class": "reversible"`, 1)
	if code, out := e.callWith(tok, "deck.project_create", map[string]any{"name": "notes", "manifest": m}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	if p := e.project("notes"); !slices.Equal(p.Asks, []string{"post"}) {
		t.Fatalf("the draft names the external tool, which asks every time, and not its reversible command: %+v", p)
	}
	var items []listItem
	_ = json.Unmarshal(e.settingsBlock("Modules", nil)["items"], &items)
	if !slices.ContainsFunc(items, func(it listItem) bool { return strings.Contains(it.Meta, "asks you each time: post") }) {
		t.Fatalf("the Projects tab says it before enabling: %+v", items)
	}
}

// TestTheProjectShowsAgentsReachTheWholeNetwork: the read domains do not hold an agent on Claude Code or Codex, so
// the project says its agents reach the whole network, unless it has no_network or no agent.
func TestTheProjectShowsAgentsReachTheWholeNetwork(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("open", `{"version": 13, "domains": {"read": ["example.com"]}, "commands": [`+agentCommand("work", "")+`]}`)
	e.createProject("closed", `{"version": 13, "no_network": true, "commands": [`+agentCommand("work", "")+`]}`)
	e.createProject("empty", `{"version": 13}`)
	var ps []struct{ Name, Network string }
	e.must("deck.projects_list", map[string]any{}, &ps)
	for _, p := range ps {
		if open := p.Name == "open"; p.Name != "master" && open != strings.Contains(p.Network, "whole network") {
			t.Fatalf("%s: its agents reach the whole network only with network: %q", p.Name, p.Network)
		}
	}
	if meta := e.screenData("open", "settings", nil); !strings.Contains(fmt.Sprint(meta), "whole network") {
		t.Fatalf("the settings of the project show it: %v", meta)
	}
}

// TestTheScheduleShowsTheModelARunStartsOn: the provider a draft suggests before the owner grants it is not what its
// run starts on, so the schedule does not show it either.
func TestTheScheduleShowsTheModelARunStartsOn(t *testing.T) {
	t.Parallel()
	main, other := &Fake{script: ok}, &Fake{script: ok, name: "other"}
	e := newEnvBuiltin(t, t.TempDir(), []provider.Provider{main, other})
	m := testManifest([]string{agentCommand("work", "")}, []string{`{"command": "work", "cron": "0 9 * * *"}`})
	_, tok := e.token("terminal", "deck.*")
	if code, raw := e.callWith(tok, "deck.project_create", map[string]any{"name": "sketch", "manifest": m}); code != 200 {
		t.Fatalf("create: %d %s", code, raw)
	}
	suggests := strings.Replace(m, `"executor": {"type": "agent"`, `"provider": "other", "executor": {"type": "agent"`, 1)
	if code, raw := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "sketch", "manifest": suggests}); code != 200 {
		t.Fatalf("save: %d %s", code, raw)
	}
	var ts []struct{ Project, Model string }
	e.must("deck.triggers_list", map[string]any{"project": "sketch"}, &ts)
	var try struct{ Process int64 }
	e.must("deck.command_try", map[string]any{"command": "sketch.work"}, &try)
	e.waitState(try.Process, "done")
	if len(ts) != 1 || ts[0].Model != "fake" || len(main.Calls()) != 1 || len(other.Calls()) != 0 {
		t.Fatalf("the schedule shows the model the run starts on: %+v, %d on fake, %d on other", ts, len(main.Calls()), len(other.Calls()))
	}
}
