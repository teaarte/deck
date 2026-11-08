package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestASkillChangesOnlyByTheOwnersYes: the master's proposal of a skill waits for the owner with its reason and diff;
// a no keeps the skill, a yes saves it as the master's, an old yes does not apply, a module agent proposes nothing.
func TestASkillChangesOnlyByTheOwnersYes(t *testing.T) {
	t.Parallel()
	var propose, said sync.Map
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if strings.Contains(c.Req.Prompt, `deck project "alpha"`) {
			out, bad := c.Tool(ctx, "document_propose", map[string]any{"name": "skills/memory/SKILL.md", "text": "x", "reason": "lesson a"})
			said.Store("alpha", fmt.Sprint(bad, " ", out))
			return provider.Result{Text: "ok"}
		}
		in, ok := propose.LoadAndDelete("in")
		if !ok {
			return provider.Result{Text: "nothing to propose"}
		}
		out, bad := c.Tool(ctx, "document_propose", in)
		said.Store("master", fmt.Sprint(bad, " ", out))
		return provider.Result{Text: "proposed"}
	}))
	heard := func(k string) string { v, _ := said.Load(k); return fmt.Sprint(v) }
	type doc struct {
		Text      string
		Revision  int64
		Revisions []struct{ Author, Reason string }
	}
	read := func() doc {
		var d doc
		e.Must("deck.document_get", map[string]any{"name": "skills/memory/SKILL.md", "history": true}, &d)
		return d
	}
	before := read()
	next := before.Text + "\n- A lesson that repeats belongs in the skill.\n"
	reason := "the lessons repeat-one, repeat-two and repeat-three say the same"
	turn := 0
	ask := func(in map[string]any) string {
		if in = maps.Clone(in); in["if_revision"] == nil {
			in["if_revision"] = read().Revision
		}
		propose.Store("in", in)
		turn++
		e.MasterSaid(e.Say(0, fmt.Sprint("propose ", turn)), 1)
		return heard("master")
	}
	type asked struct {
		ID    int64
		Where string
	}
	waiting := func() []asked {
		var as []asked
		e.Must("deck.asks_list", nil, &as)
		return as
	}
	// of is the ask that waits for a proposal of a text with line, or 0.
	of := func(line string) int64 {
		for _, a := range waiting() {
			if strings.Contains(a.Where, line) {
				return a.ID
			}
		}
		return 0
	}

	if got := ask(map[string]any{"name": "skills/memory/SKILL.md", "text": "no frontmatter", "reason": reason}); !strings.HasPrefix(got, "true ") || !strings.Contains(got, "frontmatter") {
		t.Fatalf("a text that fails the checks of a skill is refused at the call: %s", got)
	}
	if got := ask(map[string]any{"name": "skills/nope/SKILL.md", "text": next, "reason": reason}); !strings.HasPrefix(got, "true ") || !strings.Contains(got, "no such document") {
		t.Fatalf("a skill that is not there is refused: %s", got)
	}
	if got := ask(map[string]any{"name": "skills/memory/SKILL.md", "text": next, "reason": reason, "if_revision": before.Revision - 1}); !strings.HasPrefix(got, "true ") || !strings.Contains(got, "read it again") {
		t.Fatalf("a text written from an older revision is refused: %s", got)
	}
	if got := ask(map[string]any{"name": "skills/memory/SKILL.md", "text": before.Text + strings.Repeat("- a long new line of the skill\n", 200), "reason": reason}); !strings.HasPrefix(got, "true ") || !strings.Contains(got, "smaller change") {
		t.Fatalf("a change longer than an ask shows is refused, not cut: %s", got)
	}
	if as := waiting(); len(as) != 0 {
		t.Fatalf("a refused proposal waits for no one: %+v", as)
	}

	good := map[string]any{"name": "skills/memory/SKILL.md", "text": next, "reason": reason}
	if got := ask(good); !strings.HasPrefix(got, "false ") || !strings.Contains(got, "waiting") {
		t.Fatalf("the master's proposal waits for the owner: %s", got)
	}
	as := waiting()
	if len(as) != 1 || !strings.Contains(as[0].Where, reason) || !strings.Contains(as[0].Where, "+ - A lesson that repeats belongs in the skill.") ||
		strings.Contains(as[0].Where, "\n  ---\n") {
		t.Fatalf("the owner sees the skill, why and what changes, not the whole text: %+v", as)
	}
	if d := read(); d.Text != before.Text || d.Revision != before.Revision {
		t.Fatalf("without a yes the skill is as it was: %d", d.Revision)
	}
	e.Answer(as[0].ID, "deny")
	if d := read(); d.Text != before.Text {
		t.Fatal("a no keeps the skill")
	}

	ask(good)
	as = waiting()
	e.Answer(as[0].ID, "allow")
	e.Until("the skill changed", func() bool { return read().Text == next })
	d := read()
	if d.Revisions[0].Author != "master" || d.Revisions[0].Reason != reason {
		t.Fatalf("the revision is the master's with the reason: %+v", d.Revisions[0])
	}
	if got := ask(good); !strings.HasPrefix(got, "false ") || strings.Contains(got, "waiting") || len(waiting()) != 0 {
		t.Fatalf("a proposal of the text the skill has is done without asking: %s", got)
	}

	later := map[string]any{"name": "skills/memory/SKILL.md", "text": next + "- One more.\n", "reason": reason}
	ask(later)
	id := e.Asks()[len(e.Asks())-1].ID
	if of("One more") != id {
		t.Fatalf("the later proposal waits: %+v", waiting())
	}
	e.Must("deck.document_save", map[string]any{"name": "skills/memory/SKILL.md", "text": next + "- The owner's own line.\n"}, nil)
	code, out := e.Call("deck.ask_answer", map[string]any{"ask": id, "answer": "allow"})
	if code != 409 || strings.Contains(read().Text, "One more") {
		t.Fatalf("a yes to a proposal of a skill changed since does not apply: %d %s", code, out)
	}
	e.Until("the old proposal closed", func() bool { return of("One more") == 0 })

	alpha := e.Project("alpha", harness.PlainManifest)
	e.WaitState(e.Run(alpha, "work"), "done")
	if got := heard("alpha"); !strings.HasPrefix(got, "true ") {
		t.Fatalf("an agent of a module does not propose a skill change: %s", got)
	}
	var raw json.RawMessage
	e.Must("deck.document_get", map[string]any{"name": "skills/memory/SKILL.md"}, &raw)
	if !strings.Contains(string(raw), "The owner's own line") {
		t.Fatalf("the skill stays the owner's: %s", raw)
	}
	var dispatch struct {
		Text     string
		Revision int64
	}
	e.Must("deck.document_get", map[string]any{"project": "master", "name": "skills/dispatch/SKILL.md"}, &dispatch)
	var done struct{ Changed bool }
	e.Must("deck.document_propose", map[string]any{"project": "master", "name": "skills/dispatch/SKILL.md", "text": dispatch.Text + "\n- More.\n", "reason": "lesson b", "if_revision": dispatch.Revision}, &done)
	e.Must("deck.document_get", map[string]any{"project": "master", "name": "skills/dispatch/SKILL.md"}, &dispatch)
	if !done.Changed || !strings.HasSuffix(dispatch.Text, "- More.\n") {
		t.Fatalf("the owner's own proposal of a skill of the master changes it at once: %+v", done)
	}
}

// TestAPressDoesNotStandForTheYesOfASkillChange: a button of the master that names a skill change makes its run wait
// for the owner's answer to the ask all the same.
func TestAPressDoesNotStandForTheYesOfASkillChange(t *testing.T) {
	t.Parallel()
	var said sync.Map
	var call atomic.Value
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() != "sleep" {
			return provider.Result{Text: "ok"}
		}
		out, bad := c.Tool(ctx, "document_propose", call.Load())
		said.Store("sleep", fmt.Sprint(bad, " ", out))
		return provider.Result{Text: "proposed"}
	}))
	var skill struct {
		Text     string
		Revision int64
	}
	e.Must("deck.document_get", map[string]any{"name": "skills/memory/SKILL.md"}, &skill)
	in := map[string]any{"name": "skills/memory/SKILL.md", "text": skill.Text + "\n- Pressed.\n", "reason": "lesson c", "if_revision": skill.Revision}
	call.Store(in)
	allowed := maps.Clone(in)
	allowed["if_revision"] = fmt.Sprint("=", skill.Revision)
	var m struct{ Manifest string }
	e.Must("deck.manifest_get", map[string]any{"project": "master"}, &m)
	var man map[string]any
	if err := json.Unmarshal([]byte(m.Manifest), &man); err != nil {
		t.Fatal(err)
	}
	man["screens"] = []any{map[string]any{"name": "skills", "title": "Skills", "blocks": []any{map[string]any{"type": "actions", "title": "Skill",
		"actions": []any{map[string]any{"label": "Apply", "command": "sleep",
			"allows": []any{map[string]any{"command": "deck.document_propose", "params": allowed}}}}}}}}
	e.Must("deck.manifest_save", map[string]any{"project": "master", "manifest": string(harness.MustJSON(man))}, nil)
	e.Must("deck.project_pause", map[string]any{"project": "master", "paused": false}, nil)
	apply := e.RowAction("master", "skills", "master.sleep", nil)
	if _, ok := apply["params"].(map[string]any)["allows"]; !ok {
		t.Fatalf("the button names the skill change: %+v", apply)
	}
	code, out := e.Press(apply)
	var run struct{ Process int64 }
	if err := json.Unmarshal(out, &run); code != 200 || err != nil {
		t.Fatalf("the press starts the run: %d %s", code, out)
	}
	e.Until("the run proposed", func() bool { _, ok := said.Load("sleep"); return ok })
	if got, _ := said.Load("sleep"); !strings.Contains(fmt.Sprint(got), "waiting") {
		t.Fatalf("the skill change waits for the owner's answer, press or not: %v", got)
	}
	e.Must("deck.document_get", map[string]any{"name": "skills/memory/SKILL.md"}, &skill)
	if strings.Contains(skill.Text, "Pressed.") {
		t.Fatal("the press did not change the skill")
	}
}

// TestAFactOfTheOwnerChangesOnlyByTheirYes: the master proposes a new text or the deletion of a fact of the owner with
// the quote it comes from, and the owner's yes makes it; a lesson and the index are not proposed.
func TestAFactOfTheOwnerChangesOnlyByTheirYes(t *testing.T) {
	t.Parallel()
	var propose, said sync.Map
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		in, ok := propose.LoadAndDelete("in")
		if !ok {
			return provider.Result{Text: "nothing to propose"}
		}
		out, bad := c.Tool(ctx, "document_propose", in)
		said.Store("master", fmt.Sprint(bad, " ", out))
		return provider.Result{Text: "proposed"}
	}))
	fact := func(typ, body string) string {
		return fmt.Sprintf("---\nname: replies\ndescription: %s\ntype: %s\n---\n\n%s\n", body, typ, body)
	}
	e.Must("deck.document_save", map[string]any{"name": "memory/replies.md", "kind": "memory", "text": fact("feedback", "Replies are short")}, nil)
	e.Must("deck.document_save", map[string]any{"name": "memory/site-waits.md", "kind": "memory",
		"text": lesson("site-waits", "process:1", "", "", "The site answers after a minute")}, nil)
	type doc struct {
		Text      string
		Revision  int64
		Revisions []struct{ Author, Reason string }
	}
	read := func(name string) (doc, int) {
		var d doc
		code, out := e.Call("deck.document_get", map[string]any{"name": name, "history": true})
		_ = json.Unmarshal(out, &d)
		return d, code
	}
	turn := 0
	ask := func(in map[string]any) string {
		d, _ := read(in["name"].(string))
		in["if_revision"] = d.Revision
		propose.Store("in", in)
		turn++
		e.MasterSaid(e.Say(0, fmt.Sprint("propose ", turn)), 1)
		v, _ := said.Load("master")
		return fmt.Sprint(v)
	}
	answer := func(want string) {
		as := e.Asks()
		if len(as) != 1 || !strings.Contains(as[0].Text, want) {
			t.Fatalf("one proposal waits for the owner: %+v", as)
		}
		e.Answer(as[0].ID, "allow")
	}
	quote := `the owner said in topic:1: "longer replies, with the reasons"`
	if got := ask(map[string]any{"name": "memory/site-waits.md", "text": lesson("site-waits", "process:1", "", "", "Two minutes"), "reason": quote}); !strings.Contains(got, "lessons are saved with document_save") {
		t.Fatalf("a lesson is not proposed, the master saves it: %s", got)
	}
	if got := ask(map[string]any{"name": "memory/MEMORY.md", "text": "- x\n", "reason": quote}); !strings.HasPrefix(got, "true ") {
		t.Fatalf("the index is not proposed: %s", got)
	}
	if got := ask(map[string]any{"name": "skills/memory/SKILL.md", "delete": true, "reason": quote}); !strings.Contains(got, "not deleted") {
		t.Fatalf("a skill is not deleted by a proposal: %s", got)
	}

	next := fact("feedback", "Replies are longer, with the reasons")
	if got := ask(map[string]any{"name": "memory/replies.md", "text": next, "reason": quote}); !strings.Contains(got, "waiting") {
		t.Fatalf("the proposal of a fact waits for the owner: %s", got)
	}
	if d, _ := read("memory/replies.md"); d.Text != fact("feedback", "Replies are short") {
		t.Fatalf("without a yes the fact is as it was: %s", d.Text)
	}
	answer("memory/replies.md")
	e.Until("the fact changed", func() bool { d, _ := read("memory/replies.md"); return d.Text == next })
	if d, _ := read("memory/replies.md"); d.Revisions[0].Author != "master" || d.Revisions[0].Reason != quote {
		t.Fatalf("the revision is the master's with the quote: %+v", d.Revisions[0])
	}

	if got := ask(map[string]any{"name": "memory/replies.md", "delete": true, "reason": quote}); !strings.Contains(got, "waiting") {
		t.Fatalf("the deletion of a fact waits for the owner: %s", got)
	}
	if _, code := read("memory/replies.md"); code != 200 {
		t.Fatalf("without a yes the fact stays: %d", code)
	}
	answer("memory/replies.md")
	e.Until("the fact deleted", func() bool { _, code := read("memory/replies.md"); return code == 404 })

	// The save checks the revision itself, past the gate, as on the owner's own call.
	e.Must("deck.document_save", map[string]any{"name": "memory/tone.md", "kind": "memory", "text": harness.Fact("tone", "user", "Plain words")}, nil)
	e.Must("deck.document_save", map[string]any{"name": "memory/tone.md", "text": harness.Fact("tone", "user", "Plain words, no jargon")}, nil)
	e.Refused("deck.document_propose", map[string]any{"name": "memory/tone.md", "text": harness.Fact("tone", "user", "Any words"), "reason": quote, "if_revision": 1},
		409, "read it again")

	if got := ask(map[string]any{"name": "memory/tone.md", "delete": true, "reason": quote}); !strings.Contains(got, "waiting") {
		t.Fatalf("the deletion waits for the owner: %s", got)
	}
	id := e.Asks()[0].ID
	e.Must("deck.document_delete", map[string]any{"name": "memory/tone.md"}, nil)
	e.Until("the proposal of a deletion done already closed", func() bool { return len(e.Asks()) == 0 })
	var answered string
	// How the ask closed, done or out of date, deck shows nowhere.
	if err := e.SQLRow(`SELECT answer FROM ask WHERE id = ?`, id).Scan(&answered); err != nil || answered != "allow" {
		t.Fatalf("a deletion done already closes as in force, not out of date: %q %v", answered, err)
	}
}
