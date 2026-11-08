package chat

import (
	"context"
	"encoding/json"
	"fmt"
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
			out, bad := c.Tool(ctx, "episodes_list", map[string]any{})
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

// TestADeckBeforeSleepWakesUpWithIt: the master of a deck that cleaned memory weekly comes back with sleep alone.
func TestADeckBeforeSleepWakesUpWithIt(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	var cur struct{ Text string }
	e.Must("deck.document_get", map[string]any{"project": "master", "name": "manifest.json"}, &cur)
	var m map[string]any
	if err := json.Unmarshal([]byte(cur.Text), &m); err != nil {
		t.Fatal(err)
	}
	for _, c := range m["commands"].([]any) {
		if c := c.(map[string]any); c["name"] == "sleep" {
			c["name"], c["executor"].(map[string]any)["prompt_file"] = "memory", "prompts/memory.md"
		}
	}
	for _, tr := range m["triggers"].([]any) {
		if tr := tr.(map[string]any); tr["command"] == "sleep" {
			tr["command"], tr["cron"] = "memory", "0 10 * * 1"
		}
	}
	old, _ := json.Marshal(m)
	// The manifest of the master of a deck before sleep.
	e.PlantRevision("master", "manifest.json", string(old))
	// The prompt of its weekly cleanup.
	e.PlantRevision("master", "prompts/memory.md", "Clean up the memory.")
	// The trigger the weekly cleanup of a deck before had.
	e.SQL(`UPDATE "trigger" SET command = 'memory', spec = '0 10 * * 1' WHERE command = 'sleep'`)
	e2 := e.Restart()
	var ts []struct{ Command, Spec string }
	e2.Must("deck.triggers_list", map[string]any{"project": "master"}, &ts)
	var sleeps int
	for _, tr := range ts {
		if tr.Command == "memory" {
			t.Fatalf("the weekly cleanup is gone: %+v", ts)
		}
		if tr.Command == "sleep" && tr.Spec == "30 3 * * *" {
			sleeps++
		}
	}
	if sleeps != 1 {
		t.Fatalf("one nightly sleep: %+v", ts)
	}
	e2.Refused("deck.document_get", map[string]any{"project": "master", "name": "prompts/memory.md"}, 404, "no document")
	e2.Must("deck.document_get", map[string]any{"project": "master", "name": "prompts/sleep.md"}, nil)
}

func TestMemoryOverviewCountsFactsLessonsAndRepeats(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Fail(fault.TaskFailed, "broken")
	}))
	p := e.Project("shop", harness.TestManifest([]string{harness.AgentCommand("collect", "")}, nil))
	e.Must("deck.document_save", map[string]any{"project": "shop", "name": "memory/tone.md", "kind": "memory",
		"text": harness.Fact("tone", "feedback", "Plain reports")}, nil)
	e.Must("deck.document_save", map[string]any{"project": "shop", "name": "memory/site.md", "kind": "memory",
		"text": lesson("site", "process:1", "collect", "", "Wait for the site")}, nil)
	e.Must("deck.document_save", map[string]any{"project": "shop", "name": "memory/MEMORY.md", "kind": "memory",
		"text": "- [Tone](tone.md) — reports\n\n## Lessons\n- [Site](site.md) — collect\n"}, nil)
	for range 3 {
		e.WaitState(e.Run(p, "collect"), "error")
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
	e.Must("deck.memory_overview", nil, &o)
	var shop bool
	for _, row := range o.Projects {
		if row.Project == "shop" {
			shop = row.Facts == 1 && row.Lessons == 1 && row.IndexLines == 4 && row.IndexLimit == 150 && row.Changed != ""
		}
	}
	if !shop {
		t.Fatalf("the memory of the project is counted: %+v", o.Projects)
	}
	if len(o.Repeats) != 1 || o.Repeats[0].Command != "collect" || o.Repeats[0].Failures != 3 || o.Repeats[0].Repeats != 2 {
		t.Fatalf("a command that failed the same way three times repeats twice: %+v", o.Repeats)
	}
}
