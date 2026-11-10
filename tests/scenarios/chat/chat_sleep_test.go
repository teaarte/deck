package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"deck2/internal/fault"
	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestSleepTurnsAFailedEpisodeIntoALessonWithItsSource: a run of a module fails; the master's sleep reads the
// episodes and writes a lesson of that command with the failed process as its source, which its next run gets.
func TestSleepTurnsAFailedEpisodeIntoALessonWithItsSource(t *testing.T) {
	t.Parallel()
	var failed sync.Map
	var said sync.Map
	var prompts sync.Map
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch c.Command() {
		case "collect":
			pid := processOf.FindStringSubmatch(c.Req.Prompt)[1]
			if _, first := failed.LoadOrStore("collect", pid); !first {
				return provider.Fail(fault.TaskFailed, "the site answered 503")
			}
			prompts.Store("collect", c.Req.Prompt)
			return harness.Ok(ctx, c)
		case "sleep":
			out, bad := c.Tool(ctx, "episodes_list", map[string]any{"since": "2020-01-01T00:00:00Z"})
			said.Store("episodes", fmt.Sprint(bad, " ", out))
			pid, _ := failed.Load("collect")
			out, bad = c.Tool(ctx, "document_save", map[string]any{"project": "shop", "name": "memory/site-503.md", "kind": "memory",
				"text": lesson("site-503", fmt.Sprint("process:", pid), "collect", "", "SITE-503 the site answers 503 at night; run it again later")})
			said.Store("save", fmt.Sprint(bad, " ", out))
			return provider.Result{Text: "one lesson written"}
		}
		return provider.Result{Text: "ok"}
	}))
	p := e.Project("shop", harness.TestManifest([]string{harness.AgentCommand("collect", "")}, nil))
	first := e.Run(p, "collect")
	if got := e.WaitState(first, "error"); got.State != "error" {
		t.Fatalf("the first run fails: %+v", got)
	}
	var r struct{ Process int64 }
	e.Must("master.sleep", map[string]any{}, &r)
	e.WaitState(r.Process, "done")
	v, _ := said.Load("episodes")
	ep, ok := strings.CutPrefix(fmt.Sprint(v), "false ")
	type episode struct {
		ID                      int64
		Project, Command, State string
		ErrorType               string `json:"error_type"`
	}
	var episodes struct{ Processes []episode }
	if !ok || json.Unmarshal([]byte(ep), &episodes) != nil || !slices.ContainsFunc(episodes.Processes, func(p episode) bool {
		return p.ID == first && p.Project == "shop" && p.Command == "collect" && p.State == "error" && p.ErrorType == "task.failed"
	}) {
		t.Fatalf("sleep reads the failed run among the episodes: %v", v)
	}
	if v, _ := said.Load("save"); !strings.HasPrefix(fmt.Sprint(v), "false ") {
		t.Fatalf("sleep writes a lesson in the project of the failed command: %v", v)
	}
	var got struct{ Text string }
	e.Must("deck.document_get", map[string]any{"project": "shop", "name": "memory/site-503.md"}, &got)
	if !strings.Contains(got.Text, fmt.Sprintf("source: process:%d", first)) || !strings.Contains(got.Text, "command: collect") {
		t.Fatalf("the lesson names the failed process and its command: %q", got.Text)
	}
	e.WaitState(e.Run(p, "collect"), "done")
	if v, _ := prompts.Load("collect"); !strings.Contains(fmt.Sprint(v), "SITE-503") {
		t.Fatalf("the next run of the command gets the lesson:\n%v", v)
	}
}

// TestSleepStartsOnAFreeModelFirst: sleep suggests the free models; with one it starts there, without one on the
// first model as any command.
func TestSleepStartsOnAFreeModelFirst(t *testing.T) {
	t.Parallel()
	idle := func(context.Context, *harness.Call) provider.Result { return provider.Result{Text: "ok"} }
	main, backup := &harness.Fake{Script: idle}, &harness.Fake{ProviderName: "backup", Script: idle}
	e := harness.New(t, harness.WithProvider(main, backup))
	e.MasterAwake()
	sleep := func() string {
		var r struct{ Process int64 }
		e.Must("master.sleep", map[string]any{}, &r)
		e.WaitState(r.Process, "done")
		var got struct{ Provider string }
		e.Must("deck.process_get", map[string]any{"process": r.Process}, &got)
		return got.Provider
	}
	if got := sleep(); got != "fake" {
		t.Fatalf("without a free model sleep starts on the first one: %s", got)
	}
	// The backup model marked free, as the owner marks a model of a connection that costs nothing.
	e.SQL(`INSERT INTO connection (workspace_id, name, url) VALUES (1, 'gratis', 'http://localhost:1/v1');
		INSERT INTO model (workspace_id, connection_id, name, model, free) SELECT 1, id, 'backup', 'm', 1 FROM connection WHERE name = 'gratis'`)
	if got := sleep(); got != "backup" {
		t.Fatalf("a free model goes first: %s", got)
	}
	e.UseModels("master.sleep", "fake")
	if got := sleep(); got != "fake" {
		t.Fatalf("the owner's assignment of sleep stands over the free one: %s", got)
	}
}

// oldMemory is the weekly cleanup of the master before sleep.
const oldMemory = `{"name": "memory", "title": "Clean up memory", "description": "Merges, shortens and deletes facts of the memory of the owner and of each project by the memory skill.", "class": "internal", "callers": ["owner", "trigger"], "provider": "claude-code", "executor": {"type": "agent", "prompt_file": "prompts/memory.md", "skills": ["memory"], "calls": ["deck.projects_list", "deck.documents_list", "deck.document_get", "deck.document_save", "deck.document_delete"]}}`

// TestADeckBeforeSleepWakesUpWithIt: a deck whose owner edited the master, paused its weekly cleanup and gave it a
// model comes back with sleep in its place, paused, on that model, and the owner's other edit kept.
func TestADeckBeforeSleepWakesUpWithIt(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	var cur struct{ Text string }
	e.Must("deck.document_get", map[string]any{"project": "master", "name": "manifest.json"}, &cur)
	var m map[string]any
	if err := json.Unmarshal([]byte(cur.Text), &m); err != nil {
		t.Fatal(err)
	}
	var memory any
	_ = json.Unmarshal([]byte(oldMemory), &memory)
	for i, c := range m["commands"].([]any) {
		if c.(map[string]any)["name"] == "sleep" {
			m["commands"].([]any)[i] = memory
		}
	}
	for _, tr := range m["triggers"].([]any) {
		if tr := tr.(map[string]any); tr["command"] == "sleep" {
			tr["command"], tr["cron"] = "memory", "0 10 * * 1"
		}
	}
	old, _ := json.MarshalIndent(m, "", "  ")
	// The prompt of the weekly cleanup of a deck before sleep.
	e.PlantRevision("master", "prompts/memory.md", "Clean up the memory.")
	// The manifest of the master that deck shipped before sleep.
	e.PlantRevision("master", "manifest.json", string(old))
	edited := strings.Replace(string(old), `"title": "Daily review"`, `"title": "Morning review"`, 1)
	// The owner's edit of it.
	e.PlantRevision("master", "manifest.json", edited)
	// Its author is the owner.
	e.SQL(`UPDATE document_revision SET author = 'owner' WHERE id = (SELECT max(r.id) FROM document_revision r
		JOIN document d ON d.id = r.document_id JOIN project p ON p.id = d.project_id WHERE p.builtin = 1 AND d.name = 'manifest.json')`)
	// The trigger of the weekly cleanup.
	e.SQL(`UPDATE "trigger" SET command = 'memory', spec = '0 10 * * 1' WHERE command = 'sleep'`)
	var ts []struct {
		ID            int64
		Command, Spec string
		Paused        bool `json:"own_paused"`
	}
	e.Must("deck.triggers_list", map[string]any{"project": "master"}, &ts)
	for _, tr := range ts {
		if tr.Command == "memory" {
			e.Must("deck.trigger_pause", map[string]any{"trigger": tr.ID, "paused": true}, nil)
		}
	}
	e.UseModels("master.memory", "fake")
	// A base of a deck before sleep.
	e.SQL(`DELETE FROM goose_db_version WHERE version_id >= 67`)
	// Nor kept its models the module that added them.
	e.SQL(`ALTER TABLE model DROP COLUMN project_id`)
	// Nor had its messages of the master blocks.
	e.SQL(`ALTER TABLE chat_message DROP COLUMN block; ALTER TABLE chat_message DROP COLUMN answer; ALTER TABLE chat_message DROP COLUMN answered_at; ALTER TABLE model DROP COLUMN tools`)
	e2 := e.Restart()
	e2.Must("deck.triggers_list", map[string]any{"project": "master"}, &ts)
	var sleeps int
	for _, tr := range ts {
		if tr.Command == "memory" {
			t.Fatalf("the weekly cleanup is gone: %+v", ts)
		}
		if tr.Command == "sleep" && tr.Spec == "30 3 * * *" && tr.Paused {
			sleeps++
		}
	}
	if sleeps != 1 {
		t.Fatalf("one nightly sleep, paused as the cleanup was: %+v", ts)
	}
	var ms harness.ModelsOut
	e2.Must("deck.models_list", map[string]any{"project": "master"}, &ms)
	var moved bool
	for _, a := range ms.Assignments {
		moved = moved || a.Project == "master" && a.Command == "sleep" && len(a.Models) == 1 && a.Models[0] == "fake"
	}
	if !moved {
		t.Fatalf("the model of the cleanup goes to sleep: %+v", ms.Assignments)
	}
	e2.Must("deck.document_get", map[string]any{"project": "master", "name": "manifest.json"}, &cur)
	if !strings.Contains(cur.Text, `"name": "sleep"`) || strings.Contains(cur.Text, `"name": "memory"`) ||
		!strings.Contains(cur.Text, `"title": "Morning review"`) || !strings.Contains(cur.Text, `"prompt_file": "prompts/sleep.md"`) {
		t.Fatalf("the manifest has sleep and the owner's edit:\n%s", cur.Text)
	}
	e2.Must("deck.document_get", map[string]any{"project": "master", "name": "prompts/sleep.md"}, nil)
}

// TestMemoryMapCountsFactsLessonsAndRepeats: repeats are failures of real runs that nothing took on; a trial and
// a run handed on to the next model are none.
func TestMemoryMapCountsFactsLessonsAndRepeats(t *testing.T) {
	t.Parallel()
	main := &harness.Fake{Script: func(context.Context, *harness.Call) provider.Result {
		return provider.Fail(fault.ModelUnavailable, "the model is down")
	}}
	backup := &harness.Fake{ProviderName: "backup", Script: func(context.Context, *harness.Call) provider.Result {
		return provider.Fail(fault.TaskFailed, "broken")
	}}
	e := harness.New(t, harness.WithProvider(main, backup))
	p := e.Project("shop", harness.TestManifest([]string{harness.AgentCommand("collect", ""), harness.AgentCommand("report", "")}, nil))
	e.UseModels("shop.collect", "fake", "backup")
	e.UseModels("shop.report", "backup")
	e.Must("deck.document_save", map[string]any{"project": "shop", "name": "memory/tone.md", "kind": "memory",
		"text": harness.Fact("tone", "feedback", "Plain reports")}, nil)
	e.Must("deck.document_save", map[string]any{"project": "shop", "name": "memory/site.md", "kind": "memory",
		"text": lesson("site", "process:1", "collect", "", "Wait for the site")}, nil)
	e.Must("deck.document_save", map[string]any{"project": "shop", "name": "memory/MEMORY.md", "kind": "memory",
		"text": "- [Tone](tone.md) — reports\n\n## Lessons\n- [Site](site.md) — collect\n"}, nil)
	count := func(errorType string) (n int) {
		// The failures of collect by type, handed on or not, which no read counts so.
		_ = e.SQLRow(`SELECT count(*) FROM process WHERE command = 'collect' AND state = 'error' AND error_type = ?`, errorType).Scan(&n)
		return n
	}
	for i := 1; i <= 3; i++ {
		e.Run(p, "collect")
		e.Until("the work of the run fails", func() bool { return count("task.failed") == i })
	}
	if n := count("model.unavailable"); n < 2 {
		t.Fatalf("runs of collect were handed on from the main model: %d", n)
	}
	e.WaitState(e.Run(p, "report"), "error")
	for range 2 {
		var tried struct{ Process int64 }
		e.Must("deck.command_try", map[string]any{"live": true, "command": "shop.report"}, &tried)
		e.WaitState(tried.Process, "error")
	}
	var o struct {
		Projects []struct {
			Project        string
			Facts, Lessons int
			IndexLines     int `json:"index_lines"`
			IndexLimit     int `json:"index_limit"`
			Changed        string
		}
		Repeats []struct {
			Project, Command  string
			ErrorType         string `json:"error_type"`
			Failures, Repeats int
		}
	}
	e.Must("deck.memory_map", nil, &o)
	var shop bool
	for _, row := range o.Projects {
		if row.Project == "shop" {
			shop = row.Facts == 1 && row.Lessons == 1 && row.IndexLines == 4 && row.IndexLimit == 150 && row.Changed != ""
		}
	}
	if !shop {
		t.Fatalf("the memory of the project is counted: %+v", o.Projects)
	}
	if len(o.Repeats) != 1 || o.Repeats[0].Command != "collect" || o.Repeats[0].ErrorType != "task.failed" ||
		o.Repeats[0].Failures != 3 || o.Repeats[0].Repeats != 2 {
		t.Fatalf("a command that failed the same way three times repeats twice, trials and runs handed on aside: %+v", o.Repeats)
	}
}
