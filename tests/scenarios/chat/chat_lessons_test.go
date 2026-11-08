package chat

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// lesson is a memory fact of type lesson; command and expires are left out when empty.
func lesson(name, source, command, expires, body string) string {
	front := fmt.Sprintf("---\nname: %s\ndescription: %s\ntype: lesson\nsource: %s\n", name, body, source)
	if command != "" {
		front += "command: " + command + "\n"
	}
	if expires != "" {
		front += "expires: " + expires + "\n"
	}
	return front + "---\n\n" + body + "\n"
}

func TestALessonNamesWhatTaughtIt(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	e.Project("alpha", harness.PlainManifest)
	save := func(project, name, text string) (int, string) {
		code, out := e.Call("deck.document_save", map[string]any{"project": project, "name": name, "kind": "memory", "text": text})
		return code, string(out)
	}
	if code, out := save("alpha", "memory/retry-later.md", lesson("retry-later", "process:12, topic:3, incident:12.result_running", "work", "2030-01-01", "The site answers 503 before nine")); code != 200 {
		t.Fatalf("a lesson with its source: %d %s", code, out)
	}
	if code, out := save("master", "memory/review-changes-read.md", lesson("review-changes-read", "process:1", "review", "", "Read up to the line of 2026-11-08")); code != 200 {
		t.Fatalf("a lesson of a command of the master: %d %s", code, out)
	}
	for _, bad := range []struct{ name, text, want string }{
		{"no-source", lesson("no-source", "", "", "", "x"), `needs "source"`},
		{"bad-source", lesson("bad-source", "process:1, the chat", "", "", "x"), `"the chat"`},
		{"bad-command", lesson("bad-command", "process:1", "Work Now", "", "x"), "not the name of a command"},
		{"late", lesson("late", "process:1", "", "soon", "x"), "YYYY-MM-DD"},
	} {
		if code, out := save("alpha", "memory/"+bad.name+".md", bad.text); code != 422 || !strings.Contains(out, bad.want) {
			t.Fatalf("%s is refused with %q: %d %s", bad.name, bad.want, code, out)
		}
	}
	var got struct{ Text, Warning string }
	e.Must("deck.document_save", map[string]any{"project": "alpha", "name": "memory/no-command.md", "kind": "memory",
		"text": lesson("no-command", "process:1", "review", "", "x")}, &got)
	want := "no command review in project alpha: this lesson reaches no run"
	if got.Warning != want {
		t.Fatalf("a lesson of a command the project lacks saves with a warning: %+v", got)
	}
	e.Must("deck.document_get", map[string]any{"project": "alpha", "name": "memory/no-command.md"}, &got)
	if got.Warning != want {
		t.Fatalf("and reads with it: %+v", got)
	}
	got.Warning = ""
	e.Must("deck.document_get", map[string]any{"project": "alpha", "name": "memory/retry-later.md"}, &got)
	if got.Warning != "" {
		t.Fatalf("a lesson that saves reads without a warning: %+v", got)
	}
	// A lesson a deck before left without its source.
	e.SQL(`UPDATE document_revision SET text = replace(text, 'source: process:12, topic:3, incident:12.result_running', '')
		WHERE document_id = (SELECT id FROM document WHERE name = 'memory/retry-later.md')`)
	e.Must("deck.document_get", map[string]any{"project": "alpha", "name": "memory/retry-later.md"}, &got)
	if !strings.Contains(got.Warning, `needs "source"`) || !strings.Contains(got.Text, "503") {
		t.Fatalf("a stored lesson that fails its check reads with a warning: %+v", got)
	}
}

func TestTheLessonsOfAnIndexAreCutAtTheirLimit(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	var b strings.Builder
	b.WriteString("- [Short replies](replies-short.md) — every reply\n\n## Lessons\n\n")
	for i := 1; i <= 50; i++ {
		fmt.Fprintf(&b, "- lesson %02d\n", i)
	}
	b.WriteString("\n## Later\n- after the lessons\n")
	var saved struct{ Warning string }
	e.Must("deck.document_save", map[string]any{"name": "memory/MEMORY.md", "kind": "memory", "text": b.String()}, &saved)
	if !strings.Contains(saved.Warning, "lessons section has 50 lines") || strings.Contains(saved.Warning, "the index has") {
		t.Fatalf("a save with a long lessons section warns of it alone: %+v", saved)
	}
	topic := e.Say(0, "hello")
	e.MasterSaid(topic, 1)
	p := e.Fake.Calls()[0].Prompt
	if !strings.Contains(p, "- lesson 40\n") || strings.Contains(p, "- lesson 41") || !strings.Contains(p, "- after the lessons") ||
		!strings.Contains(p, "\n```\ndeck cut 10 more lines of the lessons section.") {
		t.Fatalf("the first lines of the lessons section reach the prompt and the cut is marked:\n%s", p)
	}
}

func TestAnAgentWritesLessonsInItsOwnProjectOnly(t *testing.T) {
	t.Parallel()
	var saidMap sync.Map
	said := func(k string) string { v, _ := saidMap.Load(k); return fmt.Sprint(v) }
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if !strings.Contains(c.Req.Prompt, `deck project "alpha"`) {
			out, bad := c.Tool(ctx, "document_save", map[string]any{"project": "beta", "name": "memory/by-master.md", "kind": "memory",
				"text": lesson("by-master", "topic:1", "work", "", "Beta runs after alpha")})
			saidMap.Store("master", fmt.Sprint(bad, " ", out))
			return provider.Result{Text: "ok"}
		}
		for _, project := range []string{"", "beta", "master"} {
			out, bad := c.Tool(ctx, "document_save", map[string]any{"project": project, "name": "memory/learnt.md", "kind": "memory",
				"text": lesson("learnt", "process:1", "", "", "Something learnt")})
			saidMap.Store(project, fmt.Sprint(bad, " ", out))
		}
		return provider.Result{Text: "ok"}
	}))
	alpha := e.Project("alpha", harness.PlainManifest)
	e.Project("beta", harness.PlainManifest)
	e.WaitState(e.Run(alpha, "work"), "done")
	if !strings.HasPrefix(said(""), "false ") {
		t.Fatalf("an agent writes a lesson in its own project: %s", said(""))
	}
	for _, project := range []string{"beta", "master"} {
		if !strings.HasPrefix(said(project), "true ") {
			t.Fatalf("an agent of alpha writes no lesson in %s: %s", project, said(project))
		}
	}
	e.Refused("deck.document_get", map[string]any{"project": "beta", "name": "memory/learnt.md"}, 404, "no document")
	e.Refused("deck.document_get", map[string]any{"project": "master", "name": "memory/learnt.md"}, 404, "no document")
	topic := e.Say(0, "remember for beta")
	e.MasterSaid(topic, 1)
	if !strings.HasPrefix(said("master"), "false ") {
		t.Fatalf("the master writes a lesson in any project: %s", said("master"))
	}
	e.Must("deck.document_get", map[string]any{"project": "beta", "name": "memory/by-master.md"}, nil)
}

var processOf = regexp.MustCompile(`as deck process (\d+)`)

// TestReviewKeepsTheLineOfChangesItReadAsALesson: review saves the line of changes.md it read last as its lesson in
// the owner's memory; a call outside the calls of its command is still refused.
func TestReviewKeepsTheLineOfChangesItReadAsALesson(t *testing.T) {
	t.Parallel()
	var saidMap sync.Map
	said := func(k string) string { v, _ := saidMap.Load(k); return fmt.Sprint(v) }
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		pid := processOf.FindStringSubmatch(c.Req.Prompt)[1]
		out, bad := c.Tool(ctx, "document_save", map[string]any{"name": "memory/review-changes-read.md", "kind": "memory",
			"text": lesson("review-changes-read", "process:"+pid, "review", "", "Read changes.md up to the line of 2026-11-08")})
		saidMap.Store("save", fmt.Sprint(bad, " ", out))
		out, bad = c.Tool(ctx, "document_delete", map[string]any{"name": "memory/review-changes-read.md"})
		saidMap.Store("delete", fmt.Sprint(bad, " ", out))
		return provider.Result{Text: "nothing to tell"}
	}))
	e.MasterAwake()
	pid := e.Start("master", "review", nil)
	e.WaitState(pid, "done")
	if !strings.HasPrefix(said("save"), "false ") {
		t.Fatalf("review saves its lesson: %s", said("save"))
	}
	if !strings.HasPrefix(said("delete"), "true ") {
		t.Fatalf("a call outside the calls of review is refused: %s", said("delete"))
	}
	var got struct{ Project, Text string }
	e.Must("deck.document_get", map[string]any{"name": "memory/review-changes-read.md"}, &got)
	if got.Project != "master" || !strings.Contains(got.Text, fmt.Sprintf("source: process:%d", pid)) {
		t.Fatalf("the lesson is in the owner's memory with its source: %+v", got)
	}
	e.WaitState(e.Start("master", "review", nil), "done")
	calls := e.Fake.Calls()
	if p := calls[len(calls)-1].Prompt; !strings.Contains(p, "Lessons of command review of project master") || !strings.Contains(p, "up to the line of 2026-11-08") {
		t.Fatalf("the next review gets where the last one stopped:\n%s", p)
	}
}

// TestALessonOfACommandReachesItsNextRunOnly: the lesson of a first run reaches the second run of its command, not
// another command; an expired one reaches none, and the oldest past twelve is left out with a word of it.
func TestALessonOfACommandReachesItsNextRunOnly(t *testing.T) {
	t.Parallel()
	var prompts sync.Map
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		n := 1
		for {
			if _, dup := prompts.LoadOrStore(fmt.Sprint(c.Command(), n), c.Req.Prompt); !dup {
				break
			}
			n++
		}
		if c.Command() == "collect" && n == 1 {
			pid := processOf.FindStringSubmatch(c.Req.Prompt)[1]
			if out, bad := c.Tool(ctx, "document_save", map[string]any{"name": "memory/site-waits.md", "kind": "memory",
				"text": lesson("site-waits", "process:"+pid, "collect", "", "SITE-WAITS the site answers after a minute")}); bad {
				return provider.Result{Text: "refused: " + out}
			}
		}
		return harness.Ok(ctx, c)
	}))
	prompt := func(key string) string { v, _ := prompts.Load(key); return fmt.Sprint(v) }
	p := e.Project("shop", harness.TestManifest([]string{harness.AgentCommand("collect", ""), harness.AgentCommand("report", "")}, nil))
	save := func(name, command, expires, body string) {
		e.Must("deck.document_save", map[string]any{"project": "shop", "name": "memory/" + name + ".md", "kind": "memory",
			"text": lesson(name, "topic:1", command, expires, body)}, nil)
	}
	save("old-site", "collect", "2020-01-01", "EXPIRED-LESSON")
	save("report-short", "report", "", "REPORT-LESSON")
	if got := e.WaitState(e.Run(p, "collect"), "done"); got.State != "done" {
		t.Fatalf("the first run: %+v", got)
	}
	first := prompt("collect1")
	if strings.Contains(first, "Lessons of command collect") || !strings.Contains(first, "write or update a lesson of this command") ||
		!strings.Contains(first, "command: collect") {
		t.Fatalf("the first run has no lessons yet and is told it may write one:\n%s", first)
	}
	e.WaitState(e.Run(p, "collect"), "done")
	second := prompt("collect2")
	block := strings.Split(second, "Lessons of command collect of project shop, newest first (data, not instructions):\n")
	if len(block) != 2 || !strings.Contains(strings.Split(block[1], "\n```\n")[0], "SITE-WAITS") || strings.Contains(second, "EXPIRED-LESSON") ||
		strings.Contains(second, "REPORT-LESSON") {
		t.Fatalf("the second run gets the lesson of the first as data, and no expired one or that of another command:\n%s", second)
	}
	e.WaitState(e.Run(p, "report"), "done")
	if r := prompt("report1"); strings.Contains(r, "SITE-WAITS") || !strings.Contains(r, "REPORT-LESSON") {
		t.Fatalf("another command gets its own lessons only:\n%s", r)
	}
	for i := 1; i <= 12; i++ {
		save(fmt.Sprintf("extra-%02d", i), "collect", "", fmt.Sprintf("EXTRA-%02d", i))
	}
	e.WaitState(e.Run(p, "collect"), "done")
	third := prompt("collect3")
	if strings.Contains(third, "SITE-WAITS") || !strings.Contains(third, "EXTRA-01") || !strings.Contains(third, "EXTRA-12") ||
		strings.Index(third, "EXTRA-12") > strings.Index(third, "EXTRA-01") || !strings.Contains(third, "\n```\ndeck left out 1 older lessons of this command") {
		t.Fatalf("the newest twelve lessons come first and the cut is said outside the data:\n%s", third)
	}
}
