package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
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
			out, bad := c.Tool(ctx, "skill_propose", map[string]any{"skill": "skills/memory/SKILL.md", "text": "x", "reason": "lesson a"})
			said.Store("alpha", fmt.Sprint(bad, " ", out))
			return provider.Result{Text: "ok"}
		}
		in, ok := propose.LoadAndDelete("in")
		if !ok {
			return provider.Result{Text: "nothing to propose"}
		}
		out, bad := c.Tool(ctx, "skill_propose", in)
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

	if got := ask(map[string]any{"skill": "skills/memory/SKILL.md", "text": "no frontmatter", "reason": reason}); !strings.HasPrefix(got, "true ") || !strings.Contains(got, "frontmatter") {
		t.Fatalf("a text that fails the checks of a skill is refused at the call: %s", got)
	}
	if got := ask(map[string]any{"skill": "skills/nope/SKILL.md", "text": next, "reason": reason}); !strings.HasPrefix(got, "true ") || !strings.Contains(got, "no skill file") {
		t.Fatalf("a skill that is not there is refused: %s", got)
	}
	if as := waiting(); len(as) != 0 {
		t.Fatalf("a refused proposal waits for no one: %+v", as)
	}

	good := map[string]any{"skill": "skills/memory/SKILL.md", "text": next, "reason": reason}
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

	later := map[string]any{"skill": "skills/memory/SKILL.md", "text": next + "- One more.\n", "reason": reason}
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
	var dispatch struct{ Text string }
	e.Must("deck.document_get", map[string]any{"project": "master", "name": "skills/dispatch/SKILL.md"}, &dispatch)
	var done struct{ Changed bool }
	e.Must("deck.skill_propose", map[string]any{"project": "master", "skill": "skills/dispatch/SKILL.md", "text": dispatch.Text + "\n- More.\n", "reason": "lesson b"}, &done)
	e.Must("deck.document_get", map[string]any{"project": "master", "name": "skills/dispatch/SKILL.md"}, &dispatch)
	if !done.Changed || !strings.HasSuffix(dispatch.Text, "- More.\n") {
		t.Fatalf("the owner's own proposal of a skill of the master changes it at once: %+v", done)
	}
}
