package modules

import (
	"context"
	"encoding/json"
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
	"deck2/tests/harness"
)

// trialAgent tries an external built-in tool and an external command, and starts a child command, until
// quiet is set.
func trialAgent(quiet *atomic.Bool) harness.Script {
	return func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() != "work" || quiet.Load() {
			return provider.Result{Structured: json.RawMessage(`{}`)}
		}
		return tryExternal(ctx, c)
	}
}

func tryExternal(ctx context.Context, c *harness.Call) provider.Result {
	d := c.Approve(ctx, "Bash", map[string]any{"command": "curl https://example.com"})
	send, _ := c.Tool(ctx, "notes__send", map[string]any{})
	child, _ := c.Tool(ctx, "notes__child", map[string]any{})
	out, _ := json.Marshal(map[string]any{"bash": d["message"], "send": send, "child": child})
	return provider.Result{Structured: out}
}

func TestDraftRunsOnlyTrialsAndNoTriggersUntilEnabled(t *testing.T) {
	t.Parallel()
	var quiet atomic.Bool
	e := harness.New(t, harness.WithAgent(trialAgent(&quiet)), harness.WithClock())
	_, tok := e.Token("terminal", "deck.*", "notes.*")
	code, out := e.As(tok).Call("deck.project_create", map[string]any{"name": "notes", "manifest": harness.DraftManifest})
	if code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	if p := e.ProjectRow("notes"); p.Status != store.ProjectDraft || !slices.Equal(p.Rights, []string{"its programs reach example.com and may send its data there"}) {
		t.Fatalf("a project the master creates is a draft that shows the rights it asks for: %+v", p)
	}

	e.Clock.Add(2 * time.Hour)
	e.Tick()
	e.Settle()
	if n := len(e.Procs()); n != 0 {
		t.Fatalf("a draft runs on none of its triggers, got %d processes", n)
	}

	var run struct{ Process int64 }
	e.Must("notes.work", map[string]any{}, &run)
	got := e.WaitState(run.Process, "done")
	var res struct{ Bash, Send, Child string }
	_ = json.Unmarshal(got.Result, &res)
	if res.Bash != gateway.TrialRefusal || !strings.Contains(res.Send, `unknown tool "notes__send"`) {
		t.Fatalf("a run in a draft declines everything external without asking: %+v", res)
	}
	var asks []harness.Ask
	e.Must("deck.asks_list", nil, &asks)
	if len(asks) != 0 {
		t.Fatalf("a trial asks the owner nothing: %+v", asks)
	}
	var child struct{ Process int64 }
	_ = json.Unmarshal([]byte(res.Child), &child)
	for _, id := range []int64{run.Process, child.Process} {
		if p := trial(e, id); !p {
			t.Fatalf("process %d of a draft and its children are trial runs", id)
		}
	}
	e.Until("the after trigger of a trial run in the draft", func() bool {
		for _, p := range e.Procs() {
			if p.Parent == run.Process && p.Trigger != 0 {
				return trial(e, p.ID)
			}
		}
		return false
	})
	e.Settle()

	e.Clock.Add(24 * time.Hour)
	shown := e.ProjectRow("notes")
	if code, out := e.As(tok).Call("deck.project_enable", map[string]any{"project": "notes", "revision": shown.Revision}); code != 403 {
		t.Fatalf("only the owner enables a draft: %d %s", code, out)
	}
	wider := strings.Replace(harness.DraftManifest, `"what": ["example.com"], "why": "reads its pages"`, `"what": ["example.com", "example.org"], "why": "reads its pages"`, 1)
	if code, out := e.As(tok).Call("deck.manifest_save", map[string]any{"project": "notes", "manifest": wider}); code != 200 {
		t.Fatalf("manifest_save: %d %s", code, out)
	}
	if code, out := e.Call("deck.project_enable", map[string]any{"project": "notes", "revision": shown.Revision}); code != 409 || !strings.Contains(string(out), "look at its rights again") {
		t.Fatalf("enabling is consent to the rights the owner saw, not to a later revision: %d %s", code, out)
	}
	var en struct {
		Status string
		Rights []string
	}
	e.Must("deck.project_enable", map[string]any{"project": "notes", "revision": e.ProjectRow("notes").Revision}, &en)
	if en.Status != store.ProjectEnabled || !slices.Equal(en.Rights, []string{"it acts in your name: send"}) {
		t.Fatalf("enable: %+v", en)
	}
	if code, _ := e.Call("deck.project_enable", map[string]any{"project": "notes", "revision": e.ProjectRow("notes").Revision}); code != 409 {
		t.Fatalf("enabling twice is refused: %d", code)
	}
	quiet.Store(true)
	before := len(e.Procs())
	e.Tick()
	e.Settle()
	if n := len(e.Procs()); n != before {
		t.Fatalf("the schedule missed while a draft does not catch up: %d -> %d", before, n)
	}
	e.Clock.Add(24 * time.Hour)
	e.Tick()
	e.Until("the scheduled run and its after trigger", func() bool { return len(e.Procs()) == before+2 })
	e.Settle()
	for _, p := range e.Procs()[:2] {
		if trial(e, p.ID) || p.State != "done" {
			t.Fatalf("runs of an enabled project are real: %+v", p)
		}
	}

	quiet.Store(false)
	var tried struct{ Process int64 }
	e.Must("deck.command_try", map[string]any{"live": true, "command": "notes.work"}, &tried)
	_ = json.Unmarshal(e.WaitState(tried.Process, "done").Result, &res)
	_ = json.Unmarshal([]byte(res.Child), &child)
	if res.Bash != gateway.TrialRefusal || child.Process == 0 || !trial(e, child.Process) {
		t.Fatalf("a trial in an enabled project declines external calls and its children are trials too: %+v", res)
	}
	e.Until("the after trigger of the trial", func() bool {
		for _, p := range e.Procs() {
			if p.Parent == tried.Process && p.Trigger != 0 {
				return trial(e, p.ID)
			}
		}
		return false
	})
	var try struct{ Process int64 }
	if code, out := e.As(tok).Call("deck.command_try", map[string]any{"live": true, "command": "notes.send"}); code != 200 || json.Unmarshal(out, &try) != nil {
		t.Fatalf("a trial of an external command is allowed, because nothing leaves deck: %d %s", code, out)
	}
	e.WaitState(try.Process, "done")
	if !trial(e, try.Process) {
		t.Fatal("command_try starts a trial run in an enabled project")
	}
	var held struct{ Waiting int64 }
	if code, out := e.As(tok).Call("notes.send", map[string]any{"retry_key": "x"}); code != 200 || json.Unmarshal(out, &held) != nil || held.Waiting == 0 {
		t.Fatalf("an external command of the master outside the allowed list waits for the owner: %d %s", code, out)
	}
	var params string
	var calls []struct{ Tool, Class, Params string }
	e.Must("deck.calls_list", map[string]any{"outcome": "waiting"}, &calls)
	for _, c := range calls {
		if c.Tool == "notes.send" && c.Class == "external" {
			params = c.Params
		}
	}
	if params != `{"retry_key":"x"}` {
		t.Fatalf("the journal keeps the parameters of a call that is not internal: %q", params)
	}
}

func trial(e *harness.Env, id int64) bool {
	var p struct{ Trial bool }
	e.Must("deck.process_get", map[string]any{"process": id}, &p)
	return p.Trial
}

func TestChangesToADraftApplyAtOnce(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	_, tok := e.Token("terminal", "deck.*")
	if code, out := e.As(tok).Call("deck.project_create", map[string]any{"name": "notes", "manifest": harness.PlainManifest}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	wider := strings.Replace(harness.PlainManifest, `"screens": []`, `"screens": [], "rights": [{"right": "network", "what": ["example.com"], "why": "reads it"}]`, 1)
	code, out := e.As(tok).Call("deck.manifest_save", map[string]any{"project": "notes", "manifest": wider})
	if code != 200 || !strings.Contains(string(out), `"revision":2`) {
		t.Fatalf("in a draft rights are part of what the owner enables, so the change applies: %d %s", code, out)
	}
	var feed []any
	e.Must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) != 0 {
		t.Fatalf("changes to a draft are not done without the owner: %v", feed)
	}
	if p := e.ProjectRow("notes"); !slices.Equal(p.Rights, []string{"its programs reach example.com and may send its data there"}) {
		t.Fatalf("the draft shows the rights enabling it grants: %+v", p)
	}
}

func TestEnablingADraftDropsAScheduledRunAlreadyPicked(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok), harness.WithClock())
	_, tok := e.Token("terminal", "deck.*")
	m := harness.TestManifest([]string{harness.AgentCommand("work", "")}, []string{`{"command": "work", "cron": "0 9 * * *"}`})
	if code, out := e.As(tok).Call("deck.project_create", map[string]any{"name": "late", "manifest": m}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	now := e.Clock.Add(2 * time.Hour)
	due, err := e.App.Store.Q.DueTriggers(context.Background(), store.Time(now))
	if err != nil || len(due) != 1 {
		t.Fatalf("the missed run is due: %v %v", due, err)
	}
	e.Must("deck.project_enable", map[string]any{"project": "late", "revision": e.ProjectRow("late").Revision}, nil)
	if _, err := e.App.Triggers.Fire(context.Background(), due[0], map[string]any{}); err == nil || !strings.Contains(err.Error(), "moved") {
		t.Fatalf("a firing picked before enable does not run after it: %v", err)
	}
	if n := len(e.Procs()); n != 0 {
		t.Fatalf("no process for the missed draft schedule: %d", n)
	}
}

func TestCommandRunsTheRevisionItWasAuthorizedAgainst(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	e.Project("nums", harness.CalcManifest)
	cmd, err := e.App.Registry.Lookup(context.Background(), 1, "nums.calc")
	if err != nil {
		t.Fatal(err)
	}
	e.Must("deck.manifest_save", map[string]any{"project": "nums",
		"manifest": strings.NewReplacer(`"executor": {`, `"callers": ["owner"], "executor": {`, "OLD PROMPT", "NEW PROMPT").Replace(harness.CalcManifest)}, nil)
	out, err := cmd.Handler(context.Background(), registry.Actor{Kind: registry.Agent, Workspace: 1, ProjectID: e.ProjectID("nums")}, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	e.WaitState(out.(process.Started).Process, "done")
	if calls := e.Fake.Calls(); len(calls) != 1 || !strings.Contains(calls[0].Prompt, "OLD PROMPT") || strings.Contains(calls[0].Prompt, "NEW PROMPT") {
		t.Fatalf("the process must run the command of the revision the call was checked against: %+v", calls)
	}
}

// TestAScriptOfADraftReachesWhatTheOwnerGrantsIt: before the owner enables a draft its scripts reach no host it
// declares, the package registries too, until the owner grants one on its card.
func TestAScriptOfADraftReachesWhatTheOwnerGrantsIt(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	_, tok := e.Token("terminal", "deck.*", "pkgs.*")
	if code, out := e.As(tok).Call("deck.project_create", map[string]any{"name": "pkgs",
		"manifest": harness.ScriptManifest("run", "", `, "rights": [{"right": "network", "what": ["pypi.org", "example.com"], "why": "reads it"}]`)}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	e.SaveScript("pkgs", "scripts/main.py", harness.PythonLib+`
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
	try := func() (got struct{ Registry, Site string }) {
		var run struct{ Process int64 }
		e.Must("pkgs.run", map[string]any{}, &run)
		p := e.WaitState(run.Process, "done", "error")
		_ = json.Unmarshal(p.Result, &got)
		return got
	}
	refused := func(s string) bool {
		return strings.Contains(s, "is not reachable from this program") || strings.Contains(s, "is not a host this module holds the network of")
	}
	if got := try(); !refused(got.Registry) || !refused(got.Site) {
		t.Fatalf("a draft's script reaches nothing it declares before the owner grants it: %+v", got)
	}
	e.Grant("pkgs", "network", "pypi.org")
	if got := try(); refused(got.Registry) || !refused(got.Site) {
		t.Fatalf("a draft's script reaches the host the owner granted, not the others: %+v", got)
	}
}

// TestADraftSaysWhatAsksTheFirstTime: before the owner enables a draft, its row names what it sends in the owner's
// name, which waits for the owner the first time.
func TestADraftSaysWhatAsksTheFirstTime(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	_, tok := e.Token("terminal", "deck.*")
	if code, out := e.As(tok).Call("deck.project_create", map[string]any{"name": "notes", "manifest": harness.PostManifest("http://127.0.0.1:1", "")}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	if p := e.ProjectRow("notes"); !slices.Equal(p.Asks, []string{"post"}) {
		t.Fatalf("the draft names the tool that sends, which asks the first time: %+v", p)
	}
	var items []harness.ListItem
	_ = json.Unmarshal(e.SettingsBlock("Modules", nil)["items"], &items)
	if !slices.ContainsFunc(items, func(it harness.ListItem) bool { return strings.Contains(it.Meta, "asks you the first time: post") }) {
		t.Fatalf("the Projects tab says it before enabling: %+v", items)
	}
}

// TestTheScheduleShowsTheModelARunStartsOn: the provider a module suggests is its author's wish, no right: its run
// starts on it, and the schedule shows it.
func TestTheScheduleShowsTheModelARunStartsOn(t *testing.T) {
	t.Parallel()
	main, other := &harness.Fake{Script: harness.Ok}, &harness.Fake{Script: harness.Ok, ProviderName: "other"}
	e := harness.New(t, harness.WithProvider(main, other))
	m := harness.TestManifest([]string{harness.AgentCommand("work", "")}, []string{`{"command": "work", "cron": "0 9 * * *"}`})
	_, tok := e.Token("terminal", "deck.*")
	if code, raw := e.As(tok).Call("deck.project_create", map[string]any{"name": "sketch", "manifest": m}); code != 200 {
		t.Fatalf("create: %d %s", code, raw)
	}
	suggests := strings.Replace(m, `"executor": {"type": "agent"`, `"provider": "other", "executor": {"type": "agent"`, 1)
	if code, raw := e.As(tok).Call("deck.manifest_save", map[string]any{"project": "sketch", "manifest": suggests}); code != 200 {
		t.Fatalf("save: %d %s", code, raw)
	}
	var ts []struct{ Project, Model string }
	e.Must("deck.triggers_list", map[string]any{"project": "sketch"}, &ts)
	var try struct{ Process int64 }
	e.Must("deck.command_try", map[string]any{"command": "sketch.work"}, &try)
	e.WaitState(try.Process, "done")
	if len(ts) != 1 || ts[0].Model != "other" || len(main.Calls()) != 0 || len(other.Calls()) != 1 {
		t.Fatalf("the schedule shows the model the run starts on: %+v, %d on fake, %d on other", ts, len(main.Calls()), len(other.Calls()))
	}
}
