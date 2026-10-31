package chat

import (
	"context"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestAChainAskedInTheChatCountsAsTheOwnersButton: what the master starts counts as the owner's start; a model
// command an agent calls of its project is within its step, an agent command the next step.
func TestAChainAskedInTheChatCountsAsTheOwnersButton(t *testing.T) {
	t.Parallel()
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", harness.ChatText("a still")
		},
	})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch c.Command() {
		case "direct":
			c.Tool(ctx, "studio__media", map[string]any{"scene": "a knight"})
			c.Tool(ctx, "studio__board", map[string]any{})
			return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
		case "board":
			return provider.Result{Structured: json.RawMessage(`{"n": 2}`)}
		}
		if out, bad := c.Tool(ctx, "studio__direct", map[string]any{}); bad {
			return provider.Result{Text: "refused: " + out}
		}
		return provider.Result{Text: "Started the video."}
	}))

	e.Must("deck.connection_set", map[string]any{"name": "groq", "api": "openai", "url": api.URL + "/v1", "key": "gsk-1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "groq", "model": "still", "name": "still", "kind": "text", "free": true}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"still"}}, nil)
	media := `{"name": "media", "title": "Media", "description": "Makes a still.", "class": "internal",
	  "input": {"type": "object", "properties": {"scene": {"type": "string"}}},
	  "executor": {"type": "model", "kind": "text", "prompt": "Describe a still."}}`
	e.Project("studio", harness.TestManifest([]string{harness.AgentCommand("direct", ""), harness.AgentCommand("board", ""), media}, nil))
	topic := e.Say(0, "make the video")
	if said := e.MasterSaid(topic, 1); said[0].Text != "Started the video." {
		t.Fatalf("the master starts the command: %+v", said)
	}
	depth := map[string]int64{}
	e.Until("the steps of the chain", func() bool {
		for _, p := range e.Procs() {
			if p.Project == "studio" && (p.State == "done" || p.State == "error") {
				depth[p.Command] = p.Depth
			}
		}
		return len(depth) == 3
	})
	if depth["direct"] != 1 || depth["media"] != 1 || depth["board"] != 2 {
		t.Fatalf("a start of the master is the owner's, a model command is within the step, an agent one the next step: %v", depth)
	}
	for _, n := range e.Notes() {
		if strings.Contains(n.Text, "deck stopped a chain") {
			t.Fatalf("no chain was stopped: %+v", n)
		}
	}
}

// TestTheMasterReadsTheFieldsOfAFormOnlyTheOwnerFillsIn: command_describe gives the master the schema of a
// command closed to it, and a call that misses a field says which one in plain words.
func TestTheMasterReadsTheFieldsOfAFormOnlyTheOwnerFillsIn(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	_, tok := e.Token("helper", "deck.command_describe", "deck.connection_set")
	code, out := e.As(tok).Call("deck.command_describe", map[string]any{"command": "deck.connection_set"})
	var spec struct {
		Name    string
		Callers []string
		Input   struct {
			Properties map[string]struct{ Enum []string }
			Required   []string
		}
	}
	if code != 200 || json.Unmarshal(out, &spec) != nil {
		t.Fatalf("command_describe: %d %s", code, out)
	}
	if spec.Name != "deck.connection_set" || len(spec.Callers) != 1 || spec.Callers[0] != "owner" ||
		strings.Join(spec.Input.Properties["api"].Enum, ",") != "openai,google,anthropic,raw" {
		t.Fatalf("the master sees the fields of the connection form and the APIs to pick: %s", out)
	}
	if code, out := e.As(tok).Call("deck.connection_set", map[string]any{"name": "x", "api": "openai", "url": "https://x.example/v1"}); code != 403 {
		t.Fatalf("describing a command does not open it: %d %s", code, out)
	}
	e.Refused("deck.model_set", map[string]any{"connection": "x"}, 422,
		"input of deck.model_set: missing property 'model'; deck.command_describe gives its parameters")
	e.Refused("deck.model_set", map[string]any{"connection": "x", "model": "m", "agent": "foo"}, 422,
		"agent: value must be 'goose'")
}

// TestTheChangesOfARunAreOneLine: what a run of a module deleted is one line of done without you with how many, in
// the owner's words, and its undo writes every record back at once; another run is another line.
func TestTheChangesOfARunAreOneLine(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		list, _ := c.Tool(ctx, "records_list", map[string]any{"project": "notes", "collection": "ideas"})
		var recs []struct{ ID int64 }
		_ = json.Unmarshal([]byte(list), &recs)
		for _, r := range recs[:min(3, len(recs))] {
			c.Tool(ctx, "record_delete", map[string]any{"record": r.ID})
		}
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}))

	e.Project("notes", harness.IdeasManifest)
	for i := range 4 {
		e.Must("deck.records_upsert", map[string]any{"project": "notes", "collection": "ideas", "records": harness.Jsonl(map[string]any{"title": fmt.Sprint("idea ", i), "score": i})}, nil)
	}
	e.WaitState(e.Start("notes", "collect", nil), "done")
	e.WaitState(e.Start("notes", "collect", nil), "done")
	type line struct {
		ID      int64
		Text    string
		CanUndo bool `json:"can_undo"`
	}
	var feed []line
	e.Must("deck.feed_list", map[string]any{"kind": "change", "lang": "ru"}, &feed)
	if len(feed) != 2 || feed[1].Text != "удалено записей из ideas: 3" || !feed[1].CanUndo || !strings.HasPrefix(feed[0].Text, "удалено idea") {
		t.Fatalf("a run is one line with how many in the owner's words: %+v", feed)
	}
	e.Must("deck.feed_undo", map[string]any{"entry": feed[1].ID}, nil)
	var recs []struct{ ID int64 }
	e.Must("deck.records_list", map[string]any{"project": "notes", "collection": "ideas"}, &recs)
	if len(recs) != 3 {
		t.Fatalf("the undo of the line writes back all it deleted: %+v", recs)
	}
	e.Must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if feed[1].CanUndo || !feed[0].CanUndo {
		t.Fatalf("the line is undone, the other one not: %+v", feed)
	}
}

// TestAManifestCheckOfAProjectAloneChecksItsCurrentManifest: manifest_check with only a project checks the manifest
// in force; data, tickers, numbers and signs want no translation, and words its filters and expressions show are used.
func TestAManifestCheckOfAProjectAloneChecksItsCurrentManifest(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	m := strings.Replace(harness.IdeasManifest, `"screens": [{
    "name": "main", "title": "Ideas",`, `"i18n": {"ru": {"Ideas": "Идеи", "Best ideas": "Лучшие идеи", "Idea": "Идея", "Score": "Оценка", "Again": "Ещё раз",
      "Collect ideas": "Собрать идеи", "Writes ideas": "Пишет идеи", "Short ideas": "Короткие идеи", "title": "название", "score": "оценка",
      "topic": "тема", "written": "записано", "fresh": "свежая", "high": "высокая", "Level": "Уровень", "rare": "редко"}},
  "screens": [{
    "name": "main", "title": "Ideas", "params": {"level": "string", "topic": "string"},
    "side": [{"type": "filter", "title": "Level", "param": "level", "options": ["high", "COIN"]},
      {"type": "filter", "param": "topic", "source": {"collection": "ideas"}, "field": "topic"}],`, 1)
	m = strings.Replace(m, `{"label": "Score", "bind": "score"}`, `{"label": "Score", "bind": "score > 8 ? 'fresh' : '$0'", "format": "tag"}, {"label": "R:R", "bind": "score"}`, 1)
	e.Project("notes", m)
	e.Must("deck.records_upsert", map[string]any{"project": "notes", "collection": "ideas", "records": harness.Jsonl(map[string]any{"title": "a", "score": 9, "topic": "Биткоин"}, map[string]any{"title": "b", "score": 1, "topic": "ETH"}, map[string]any{"title": "c", "score": 2, "topic": "rare"})}, nil)
	var c struct {
		Ok           bool
		Refused      []string
		Unused       []string `json:"unused_i18n"`
		Untranslated []string
	}
	e.Must("deck.manifest_check", map[string]any{"project": "notes"}, &c)
	if !c.Ok || len(c.Untranslated) != 0 || !slices.Equal(c.Unused, []string{}) {
		t.Fatalf("a check of the current manifest says nothing false of its words: %+v", c)
	}
}

// TestNoSchemaAnAgentSeesHasAllows: the field a press of the owner gives the calls its button allows in is in no input
// schema of a command, and only the owner's call gives it.
func TestNoSchemaAnAgentSeesHasAllows(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("notes", harness.IdeasManifest)
	var d struct{ Input json.RawMessage }
	e.Must("deck.command_describe", map[string]any{"command": "notes.collect"}, &d)
	if strings.Contains(string(d.Input), "allows") || !strings.Contains(string(d.Input), "retry_key") {
		t.Fatalf("the schema of a command does not name allows: %s", d.Input)
	}
	_, tok := e.Token("terminal", "notes.*")
	if code, out := e.As(tok).Call("notes.collect", map[string]any{"allows": "[]"}); code != 422 || !strings.Contains(string(out), "allows") {
		t.Fatalf("allows from another than the owner is refused: %d %s", code, out)
	}
	if code, out := e.Call("notes.collect", map[string]any{"allows": `[{"command": "deck.notify", "params": {}}]`}); code != 200 {
		t.Fatalf("the owner's call gives allows: %d %s", code, out)
	}
}

// TestAManifestTheMasterChangedTwiceInARunGoesBackOnce: two saves of a manifest in one reply of the master are one
// line, undone to the manifest before the first; one the owner changed between them is not undone over their change.
func TestAManifestTheMasterChangedTwiceInARunGoesBackOnce(t *testing.T) {
	t.Parallel()
	plans, mid, on := make(chan bool, 1), make(chan bool), make(chan bool)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		var owner bool
		select {
		case owner = <-plans:
		default:
			return provider.Result{Text: "ok"}
		}
		for _, edit := range [][2]string{{`"title": "Work"`, `"title": "Work hard"`}, {`"title": "Work hard"`, `"title": "Work harder"`}} {
			c.Tool(ctx, "manifest_save", map[string]any{"project": "notes", "old": []string{edit[0]}, "new": []string{edit[1]}})
			if owner && edit[1] == `"title": "Work hard"` {
				mid <- true
				<-on
			}
		}
		return provider.Result{Text: "Saved."}
	}))

	e.Project("notes", harness.PlainManifest)
	var feed []struct {
		ID   int64
		Text string
	}
	title := func() string {
		var cur struct{ Manifest string }
		e.Must("deck.manifest_get", map[string]any{"project": "notes"}, &cur)
		var m struct{ Commands []struct{ Title string } }
		_ = json.Unmarshal([]byte(cur.Manifest), &m)
		return m.Commands[0].Title
	}

	plans <- false
	e.MasterSaid(e.Say(0, "retitle the work"), 1)
	e.Must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) != 1 || feed[0].Text != "changed the manifest of notes ×2" {
		t.Fatalf("two saves of a run are one line: %+v", feed)
	}
	e.Must("deck.feed_undo", map[string]any{"entry": feed[0].ID}, nil)
	if got := title(); got != "Work" {
		t.Fatalf("the undo goes back to before the first save: %q", got)
	}

	plans <- true
	topic := e.Say(0, "retitle the work again")
	<-mid
	e.Must("deck.manifest_save", map[string]any{"project": "notes", "old": []string{`"description": "Does work"`}, "new": []string{`"description": "Does the owner's work"`}}, nil)
	on <- true
	e.MasterSaid(topic, 1)
	e.Must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	e.Refused("deck.feed_undo", map[string]any{"entry": feed[0].ID}, 409, "changed after")
}

func TestReviewRunsOnItsSchedule(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.MasterScript), harness.WithClock())
	e.MasterAwake()
	e.Project("work", harness.PlainManifest)
	e.Clock.Add(2*time.Hour + 29*time.Minute)
	e.Tick()
	if n := len(e.RunsOf("master", "review")); n != 0 {
		t.Fatalf("not before 10:30: %d", n)
	}
	e.Clock.Add(2 * time.Minute)
	e.Tick()
	e.Until("the review", func() bool { rs := e.RunsOf("master", "review"); return len(rs) == 1 && rs[0].State == "done" })
	e.Until("the note of the review", func() bool {
		for _, n := range e.Notes() {
			if strings.Contains(n.Text, "The queue has not moved") {
				return true
			}
		}
		return false
	})
	var ts []struct{ Command, Spec string }
	e.Must("deck.triggers_list", map[string]any{"project": "master"}, &ts)
	found := false
	for _, tr := range ts {
		found = found || tr.Command == "review" && tr.Spec == "30 10 * * *"
	}
	if !found {
		t.Fatalf("the review is a trigger of the schedule screen: %+v", ts)
	}
}

func TestAnErrorReachesTheMasterOncePerDay(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.MasterScript), harness.WithClock())
	e.MasterAwake()
	p := e.Project("work", harness.PlainManifest)
	failed := e.WaitState(e.Run(p, "work"), "error")
	e.Until("on_error", func() bool { return len(e.RunsOf("master", "on_error")) == 1 })
	in := e.App.Store.Q
	got, _ := in.GetProcess(context.Background(), e.RunsOf("master", "on_error")[0].ID)
	if !strings.Contains(got.Input, `"process":`) || !strings.Contains(got.Input, strings.TrimSpace(harness.Itoa(failed.ID))) {
		t.Fatalf("the master gets the failed process: %s", got.Input)
	}
	e.WaitState(e.Run(p, "work"), "error")
	e.App.Runner.Wait()
	if n := len(e.RunsOf("master", "on_error")); n != 1 {
		t.Fatalf("once a day per command: %d", n)
	}
	e.Clock.Add(25 * time.Hour)
	e.WaitState(e.Run(p, "work"), "error")
	e.Until("the next day", func() bool { return len(e.RunsOf("master", "on_error")) == 2 })
}

func TestMasterOwnErrorsDoNotReachItself(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Outcome: provider.Failed, Text: "the model is overloaded"}
	}))

	e.MasterAwake()
	var r struct{ Process int64 }
	e.Must("master.review", map[string]any{}, &r)
	e.WaitState(r.Process, "error")
	e.App.Runner.Wait()
	if n := len(e.RunsOf("master", "on_error")); n != 0 {
		t.Fatalf("the master's own failure does not go to itself: %d", n)
	}
	if n := e.WaitNote("review failed:"); !strings.Contains(n.Text, "the model is overloaded") {
		t.Fatalf("the owner hears of it in the feed instead: %+v", n)
	}
}

// TestReviewCallsOnlyWhatItsCommandLists: the daily review reads and tells the owner; changing a project is not among
// its tools, and a call by name is refused.
func TestReviewCallsOnlyWhatItsCommandLists(t *testing.T) {
	t.Parallel()
	var tools []string
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		tools = c.Req.AllowedTools
		out, bad := c.Tool(ctx, "manifest_save", map[string]any{"project": "work", "manifest": harness.PlainManifest})
		return provider.Result{Text: fmt.Sprintf("%v %s", bad, out)}
	}))

	e.MasterAwake()
	e.Project("work", harness.PlainManifest)
	var r struct{ Process int64 }
	e.Must("master.review", map[string]any{}, &r)
	got := e.WaitState(r.Process, "done", "error")
	if !strings.HasPrefix(string(got.Result), `"true`) || !slices.Contains(tools, "mcp__deck__notify") || !slices.Contains(tools, "mcp__deck__health_list") || slices.Contains(tools, "mcp__deck__suggest") ||
		slices.Contains(tools, "mcp__deck__manifest_save") || slices.Contains(tools, "mcp__deck__record_delete") {
		t.Fatalf("the review gets the tools it lists and no other: %s %v", got.Result, tools)
	}
}

// TestADeletedProjectTakesItsErrorDayWithIt: a new project under the name of a deleted one does not inherit its
// day of silence towards the master.
func TestADeletedProjectTakesItsErrorDayWithIt(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.MasterScript), harness.WithClock())
	e.MasterAwake()
	e.WaitState(e.Run(e.Project("work", harness.PlainManifest), "work"), "error")
	e.Until("on_error", func() bool { rs := e.RunsOf("master", "on_error"); return len(rs) == 1 && rs[0].State == "done" })
	e.Must("deck.project_delete", map[string]any{"project": "work"}, nil)
	e.WaitState(e.Run(e.Project("work", harness.PlainManifest), "work"), "error")
	e.Until("on_error of the new project", func() bool { return len(e.RunsOf("master", "on_error")) == 2 })
}

// TestABlockThatFailsOnAScreenReachesTheMaster: a block of a module screen that fails on the records there are
// is in the health of the module and goes to the master once per block and revision of the manifest.
func TestABlockThatFailsOnAScreenReachesTheMaster(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.MasterScript))
	e.MasterAwake()
	broken := strings.Replace(harness.WebNotes, `"columns": [{"label": "Title", "bind": "title"}`, `"columns": [{"label": "Title", "bind": "title"}, {"label": "Score", "bind": "score > 0 ? score : nil"}`, 1)
	if broken == harness.WebNotes {
		t.Fatal("fixture replacement did not apply")
	}
	for _, name := range []string{"web", "web2"} {
		e.Project(name, broken)
		e.Must("deck.document_save", map[string]any{"project": name, "name": "custom/chart.html", "kind": "custom", "text": "<p>x</p>"}, nil)
		e.Must("deck.records_upsert", map[string]any{"project": name, "collection": "notes", "records": harness.Jsonl(map[string]any{"title": "b"})}, nil)
	}
	e.ScreenMessages("web", "main", nil)
	e.Until("on_error", func() bool { return len(e.RunsOf("master", "on_error")) == 1 })
	got, _ := e.App.Store.Q.GetProcess(context.Background(), e.RunsOf("master", "on_error")[0].ID)
	if !strings.Contains(got.Input, `"screen":"main"`) || !strings.Contains(got.Input, `"project":"web"`) || !strings.Contains(got.Input, "Score") {
		t.Fatalf("the master gets the screen, the block and the error: %s", got.Input)
	}
	e.ScreenMessages("web2", "main", nil)
	e.WaitHealth("screen:web2.main", "error")
	hs := e.Health("web")
	found := false
	for _, h := range hs {
		found = found || h.Subject == "screen:web.main" && h.Status == "error"
	}
	if !found {
		t.Fatalf("the health of the module names the screen: %+v", hs)
	}
	e.ScreenMessages("web", "main", nil)
	if n := len(e.RunsOf("master", "on_error")); n != 2 {
		t.Fatalf("once per block and revision of each module: %d", n)
	}
	e.Must("deck.manifest_save", map[string]any{"project": "web", "manifest": harness.WebNotes}, nil)
	hs = e.Health("web")
	for _, h := range hs {
		if h.Subject == "screen:web.main" {
			t.Fatalf("a new revision starts the screen over: %+v", hs)
		}
	}
}

// TestABlockThatFailsIsInTheHealthOfItsModule: a failing block is in the health of its module, which a read of other
// blocks after an event does not clear.
func TestABlockThatFailsIsInTheHealthOfItsModule(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.MasterScript))
	e.MasterAwake()
	broken := strings.Replace(harness.WebNotes, `"columns": [{"label": "Title", "bind": "title"}`, `"columns": [{"label": "Title", "bind": "title"}, {"label": "Score", "bind": "score > 0 ? score : nil"}`, 1)
	e.Project("web", broken)
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>x</p>"}, nil)
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": harness.Jsonl(map[string]any{"title": "b"})}, nil)
	e.ScreenMessages("web", "main", nil)
	e.ScreenMessages("web", "main", map[string]any{"topic": "asks"})
	hs := e.Health("web")
	if !slices.ContainsFunc(hs, func(h harness.HealthRow) bool {
		return h.Subject == "screen:web.main" && h.Status == "error"
	}) {
		t.Fatalf("the health of the module names the screen, and a read of some of its blocks keeps it: %+v", hs)
	}
}

// TestADocumentDeckNoLongerShipsLeavesTheMaster: a document deck wrote into project master that it ships no more
// goes on the next start; one the owner changed stays his.
func TestADocumentDeckNoLongerShipsLeavesTheMaster(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	for _, q := range []string{
		`INSERT INTO document (workspace_id, project_id, kind, name, current) SELECT 1, id, 'skill', 'skills/old/SKILL.md', 1 FROM project WHERE name = 'master'`,
		`INSERT INTO document_revision (document_id, n, text, author) SELECT id, 1, 'old', 'deck' FROM document WHERE name = 'skills/old/SKILL.md'`,
		`INSERT INTO document (workspace_id, project_id, kind, name, current) SELECT 1, id, 'skill', 'skills/kept/SKILL.md', 2 FROM project WHERE name = 'master'`,
		`INSERT INTO document_revision (document_id, n, text, author) SELECT id, 1, 'kept', 'deck' FROM document WHERE name = 'skills/kept/SKILL.md'`,
		`INSERT INTO document_revision (document_id, n, text, author) SELECT id, 2, 'mine', 'owner' FROM document WHERE name = 'skills/kept/SKILL.md'`,
	} {
		// Skills an older deck wrote into the master, past the checks of a save.
		e.SQL(q)
	}
	_ = e.App.Close()
	e = harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	var ds []struct {
		Name    string
		Deleted bool
	}
	e.Must("deck.documents_list", map[string]any{"project": "master", "with_deleted": true}, &ds)
	deleted := map[string]bool{}
	for _, d := range ds {
		deleted[d.Name] = d.Deleted
	}
	for name, gone := range map[string]bool{"skills/old/SKILL.md": true, "skills/kept/SKILL.md": false, "prompts/reply.md": false} {
		if was, ok := deleted[name]; !ok || was != gone {
			t.Fatalf("%s: deleted %v, want %v (listed %v)", name, was, gone, ok)
		}
	}
}

// TestOnErrorCallsOnlyWhatItsCommandLists: the agent that reads the output of failed processes reads and tells the
// owner; deleting a project is not among its tools, and a call by name is refused with the project left in place.
func TestOnErrorCallsOnlyWhatItsCommandLists(t *testing.T) {
	t.Parallel()
	var tools []string
	var listed, deleted string
	var listedBad, deletedBad bool
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		tools = c.Req.AllowedTools
		listed, listedBad = c.Tool(ctx, "projects_list", map[string]any{})
		deleted, deletedBad = c.Tool(ctx, "project_delete", map[string]any{"project": "work"})
		return provider.Result{Text: "looked"}
	}))

	e.MasterAwake()
	e.Project("work", harness.PlainManifest)
	var r struct{ Process int64 }
	e.Must("master.on_error", map[string]any{"project": "work", "screen": "main", "error": "b0: broken"}, &r)
	e.WaitState(r.Process, "done")
	if listedBad || !strings.Contains(listed, `"work"`) || !slices.Contains(tools, "mcp__deck__projects_list") {
		t.Fatalf("on_error calls what its command lists: %v %s %v", listedBad, listed, tools)
	}
	if !deletedBad || slices.Contains(tools, "mcp__deck__project_delete") {
		t.Fatalf("on_error may not delete a project: %v %s %v", deletedBad, deleted, tools)
	}
	var ps []struct{ Name string }
	e.Must("deck.projects_list", nil, &ps)
	if !slices.ContainsFunc(ps, func(p struct{ Name string }) bool { return p.Name == "work" }) {
		t.Fatalf("the project stays: %+v", ps)
	}
}
