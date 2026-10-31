package scenarios

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
)

// masterScript plays the master: the review tells the owner in the feed, on_error says what it got; failing makes
// the work command of the other projects fail.
func masterScript(ctx context.Context, c *Call) provider.Result {
	switch c.command() {
	case "review":
		if out, bad := c.Tool(ctx, "notify", map[string]any{"project": "work", "text": "The queue has not moved for three days; say \"unstick it\" to have it done."}); bad {
			return provider.Result{Text: "refused: " + out}
		}
		return provider.Result{Text: "one note"}
	case "on_error":
		return provider.Result{Text: "looked"}
	case "work":
		return provider.Result{Outcome: provider.Failed, Text: "the site is down"}
	}
	return provider.Result{Text: "ok"}
}

func (e *Env) runsOf(project, command string) []Proc {
	e.T.Helper()
	var out []Proc
	for _, p := range e.Procs() {
		if p.Project == project && p.Command == command {
			out = append(out, p)
		}
	}
	return out
}

func (e *Env) masterAwake() {
	e.T.Helper()
	e.Must("deck.project_pause", map[string]any{"project": "master", "paused": false}, nil)
	e.UseModels("master.on_error")
}

func TestReviewRunsOnItsSchedule(t *testing.T) {
	t.Parallel()
	e := New(t, WithAgent(masterScript), WithClock())
	e.masterAwake()
	e.Project("work", plainManifest)
	e.Clock.Add(2*time.Hour + 29*time.Minute)
	e.tick()
	if n := len(e.runsOf("master", "review")); n != 0 {
		t.Fatalf("not before 10:30: %d", n)
	}
	e.Clock.Add(2 * time.Minute)
	e.tick()
	e.Until("the review", func() bool { rs := e.runsOf("master", "review"); return len(rs) == 1 && rs[0].State == "done" })
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
	e := New(t, WithAgent(masterScript), WithClock())
	e.masterAwake()
	p := e.Project("work", plainManifest)
	failed := e.WaitState(e.Run(p, "work"), "error")
	e.Until("on_error", func() bool { return len(e.runsOf("master", "on_error")) == 1 })
	in := e.App.Store.Q
	got, _ := in.GetProcess(context.Background(), e.runsOf("master", "on_error")[0].ID)
	if !strings.Contains(got.Input, `"process":`) || !strings.Contains(got.Input, strings.TrimSpace(itoa(failed.ID))) {
		t.Fatalf("the master gets the failed process: %s", got.Input)
	}
	e.WaitState(e.Run(p, "work"), "error")
	e.App.Runner.Wait()
	if n := len(e.runsOf("master", "on_error")); n != 1 {
		t.Fatalf("once a day per command: %d", n)
	}
	e.Clock.Add(25 * time.Hour)
	e.WaitState(e.Run(p, "work"), "error")
	e.Until("the next day", func() bool { return len(e.runsOf("master", "on_error")) == 2 })
}

func TestMasterOwnErrorsDoNotReachItself(t *testing.T) {
	t.Parallel()
	e := New(t, WithAgent(func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Outcome: provider.Failed, Text: "the model is overloaded"}
	}))

	e.masterAwake()
	var r struct{ Process int64 }
	e.Must("master.review", map[string]any{}, &r)
	e.WaitState(r.Process, "error")
	e.App.Runner.Wait()
	if n := len(e.runsOf("master", "on_error")); n != 0 {
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
	e := New(t, WithAgent(func(ctx context.Context, c *Call) provider.Result {
		tools = c.Req.AllowedTools
		out, bad := c.Tool(ctx, "manifest_save", map[string]any{"project": "work", "manifest": plainManifest})
		return provider.Result{Text: fmt.Sprintf("%v %s", bad, out)}
	}))

	e.masterAwake()
	e.Project("work", plainManifest)
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
	e := New(t, WithAgent(masterScript), WithClock())
	e.masterAwake()
	e.WaitState(e.Run(e.Project("work", plainManifest), "work"), "error")
	e.Until("on_error", func() bool { rs := e.runsOf("master", "on_error"); return len(rs) == 1 && rs[0].State == "done" })
	e.Must("deck.project_delete", map[string]any{"project": "work"}, nil)
	e.WaitState(e.Run(e.Project("work", plainManifest), "work"), "error")
	e.Until("on_error of the new project", func() bool { return len(e.runsOf("master", "on_error")) == 2 })
}

// TestABlockThatFailsOnAScreenReachesTheMaster: a block of a module screen that fails on the records there are
// is in the health of the module and goes to the master once per block and revision of the manifest.
func TestABlockThatFailsOnAScreenReachesTheMaster(t *testing.T) {
	t.Parallel()
	e := New(t, WithAgent(masterScript))
	e.masterAwake()
	broken := strings.Replace(webNotes, `"columns": [{"label": "Title", "bind": "title"}`, `"columns": [{"label": "Title", "bind": "title"}, {"label": "Score", "bind": "score > 0 ? score : nil"}`, 1)
	if broken == webNotes {
		t.Fatal("fixture replacement did not apply")
	}
	for _, name := range []string{"web", "web2"} {
		e.Project(name, broken)
		e.Must("deck.document_save", map[string]any{"project": name, "name": "custom/chart.html", "kind": "custom", "text": "<p>x</p>"}, nil)
		e.Must("deck.records_upsert", map[string]any{"project": name, "collection": "notes", "records": jsonl(map[string]any{"title": "b"})}, nil)
	}
	e.screenMessages("web", "main", nil)
	e.Until("on_error", func() bool { return len(e.runsOf("master", "on_error")) == 1 })
	got, _ := e.App.Store.Q.GetProcess(context.Background(), e.runsOf("master", "on_error")[0].ID)
	if !strings.Contains(got.Input, `"screen":"main"`) || !strings.Contains(got.Input, `"project":"web"`) || !strings.Contains(got.Input, "Score") {
		t.Fatalf("the master gets the screen, the block and the error: %s", got.Input)
	}
	e.screenMessages("web2", "main", nil)
	e.WaitHealth("screen:web2.main", "error")
	hs := e.Health("web")
	found := false
	for _, h := range hs {
		found = found || h.Subject == "screen:web.main" && h.Status == "error"
	}
	if !found {
		t.Fatalf("the health of the module names the screen: %+v", hs)
	}
	e.screenMessages("web", "main", nil)
	if n := len(e.runsOf("master", "on_error")); n != 2 {
		t.Fatalf("once per block and revision of each module: %d", n)
	}
	e.Must("deck.manifest_save", map[string]any{"project": "web", "manifest": webNotes}, nil)
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
	e := New(t, WithAgent(masterScript))
	e.masterAwake()
	broken := strings.Replace(webNotes, `"columns": [{"label": "Title", "bind": "title"}`, `"columns": [{"label": "Title", "bind": "title"}, {"label": "Score", "bind": "score > 0 ? score : nil"}`, 1)
	e.Project("web", broken)
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>x</p>"}, nil)
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": jsonl(map[string]any{"title": "b"})}, nil)
	e.screenMessages("web", "main", nil)
	e.screenMessages("web", "main", map[string]any{"topic": "asks"})
	hs := e.Health("web")
	if !slices.ContainsFunc(hs, func(h HealthRow) bool {
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
	e := New(t, WithHome(home), WithProvider(&Fake{}))
	for _, q := range []string{
		`INSERT INTO document (workspace_id, project_id, kind, name, current) SELECT 1, id, 'skill', 'skills/old/SKILL.md', 1 FROM project WHERE name = 'master'`,
		`INSERT INTO document_revision (document_id, n, text, author) SELECT id, 1, 'old', 'deck' FROM document WHERE name = 'skills/old/SKILL.md'`,
		`INSERT INTO document (workspace_id, project_id, kind, name, current) SELECT 1, id, 'skill', 'skills/kept/SKILL.md', 2 FROM project WHERE name = 'master'`,
		`INSERT INTO document_revision (document_id, n, text, author) SELECT id, 1, 'kept', 'deck' FROM document WHERE name = 'skills/kept/SKILL.md'`,
		`INSERT INTO document_revision (document_id, n, text, author) SELECT id, 2, 'mine', 'owner' FROM document WHERE name = 'skills/kept/SKILL.md'`,
	} {
		if _, err := e.App.Store.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = e.App.Close()
	e = New(t, WithHome(home), WithProvider(&Fake{}))
	for name, gone := range map[string]bool{"skills/old/SKILL.md": true, "skills/kept/SKILL.md": false, "prompts/reply.md": false} {
		var deleted sql.NullString
		if err := e.App.Store.DB.QueryRow(`SELECT deleted_at FROM document WHERE name = ? AND project_id = (SELECT id FROM project WHERE name = 'master')`, name).Scan(&deleted); err != nil || deleted.Valid != gone {
			t.Fatalf("%s: deleted %v, want %v (%v)", name, deleted.Valid, gone, err)
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
	e := New(t, WithAgent(func(ctx context.Context, c *Call) provider.Result {
		tools = c.Req.AllowedTools
		listed, listedBad = c.Tool(ctx, "projects_list", map[string]any{})
		deleted, deletedBad = c.Tool(ctx, "project_delete", map[string]any{"project": "work"})
		return provider.Result{Text: "looked"}
	}))

	e.masterAwake()
	e.Project("work", plainManifest)
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
