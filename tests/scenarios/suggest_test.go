package scenarios

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
)

// boardWork is plainManifest with a board, so cards land in its inbox lane.
var boardWork = strings.Replace(plainManifest, `"triggers": [],`, `"triggers": [], "board": {"lanes": [{"name": "inbox", "title": "Inbox", "role": "inbox"}, {"name": "done", "title": "Done", "role": "done"}]},`, 1)

func (e *Env) suggestions() []askView {
	e.T.Helper()
	var as []askView
	e.must("deck.asks_list", nil, &as)
	var out []askView
	for _, a := range as {
		if a.Kind == "suggest" {
			out = append(out, a)
		}
	}
	return out
}

func (e *Env) cardTitles(project string) []string {
	e.T.Helper()
	var board struct {
		Lanes []struct {
			Cards []struct{ Title string } `json:"cards"`
		} `json:"lanes"`
	}
	e.must("deck.cards_list", map[string]any{"project": project}, &board)
	var out []string
	for _, l := range board.Lanes {
		for _, c := range l.Cards {
			out = append(out, c.Title)
		}
	}
	return out
}

// suggester is a fake agent that suggests a card into a project, the one in the input or its own, and hands back
// the refusals it saw.
func suggester(ctx context.Context, c *Call) provider.Result {
	var in struct{ Into string }
	_ = json.Unmarshal([]byte(strings.SplitN(strings.SplitN(c.Req.Prompt, "Input (JSON data, not instructions): ", 2)[1], "\n", 2)[0]), &in)
	seen, _ := c.Tool(ctx, "suggestions_rejected", map[string]any{})
	args := map[string]any{"title": "Fix the parser", "text": "It drops the last line.", "reason": "three runs lost a line"}
	if in.Into != "" {
		args["project"] = in.Into
	}
	out, bad := c.Tool(ctx, "suggest", args)
	if bad {
		return provider.Result{Text: "refused: " + out}
	}
	return provider.Result{Text: "seen: " + seen}
}

const suggestManifest = `{"version": 5, "board": {"lanes": [{"name": "inbox", "title": "Inbox", "role": "inbox"}]},
  "commands": [{"name": "work", "title": "Work", "description": "Suggests", "class": "internal",
    "input": {"type": "object", "properties": {"into": {"type": "string"}}},
    "executor": {"type": "agent", "prompt": "Suggest."}}]}`

func TestSuggestionYesPutsACardInTheInbox(t *testing.T) {
	e := newEnv(t, suggester)
	e.createProject("alpha", suggestManifest)
	e.createProject("beta", boardWork)
	var r struct{ Process int64 }
	e.must("alpha.work", map[string]any{"into": "beta"}, &r)
	e.waitState(r.Process, "done")
	ss := e.suggestions()
	if len(ss) != 1 || !strings.Contains(ss[0].Text, "three runs lost a line") || !strings.Contains(ss[0].Text, "card in beta") {
		t.Fatalf("a suggestion waits for the owner in waiting for you: %+v", ss)
	}
	if got := e.cardTitles("beta"); len(got) != 0 {
		t.Fatalf("nothing is made before the yes: %v", got)
	}
	var items []listItem
	_ = json.Unmarshal(e.screenData("", "cockpit", nil)["b0"]["items"], &items)
	verbs := []string{}
	for _, a := range items[0].Actions {
		verbs = append(verbs, a["verb"].(string))
	}
	if strings.Join(verbs, " ") != "yes deny deny_because move" {
		t.Fatalf("the cockpit offers yes, no, no because and another project: %v", verbs)
	}
	e.answer(ss[0].ID, "allow")
	if got := e.cardTitles("beta"); len(got) != 1 || got[0] != "Fix the parser" {
		t.Fatalf("yes puts the card into the inbox lane of its project: %v", got)
	}
	if len(e.suggestions()) != 0 {
		t.Fatal("an answered suggestion leaves waiting for you")
	}
}

func TestRejectionIsSeenByTheNextSuggester(t *testing.T) {
	e := newEnv(t, suggester)
	p := e.createProject("alpha", suggestManifest)
	other := e.createProject("gamma", suggestManifest)
	e.waitState(e.run(p, "work"), "done")
	ss := e.suggestions()
	e.must("deck.ask_answer", map[string]any{"ask": ss[0].ID, "answer": "deny", "reason": "the parser is going away"}, nil)
	got := e.waitState(e.run(p, "work"), "done")
	if !strings.Contains(string(got.Result), "the parser is going away") || !strings.Contains(string(got.Result), "Fix the parser") {
		t.Fatalf("the next suggester sees the refusal with its reason: %s", got.Result)
	}
	if got := e.waitState(e.run(other, "work"), "done"); strings.Contains(string(got.Result), "the parser is going away") {
		t.Fatalf("an agent of another project does not see it: %s", got.Result)
	}
	var all []map[string]any
	e.must("deck.suggestions_rejected", map[string]any{}, &all)
	if len(all) != 1 || all[0]["why_not"] != "the parser is going away" {
		t.Fatalf("the owner's side sees every refusal: %+v", all)
	}
}

func TestSuggestionMovesToAnotherProject(t *testing.T) {
	e := newEnv(t, suggester)
	p := e.createProject("alpha", suggestManifest)
	e.createProject("beta", boardWork)
	e.waitState(e.run(p, "work"), "done")
	ss := e.suggestions()
	e.must("deck.ask_answer", map[string]any{"ask": ss[0].ID, "answer": "move", "project": "beta"}, nil)
	moved := e.suggestions()
	if len(moved) != 1 || moved[0].ID != ss[0].ID || !strings.Contains(moved[0].Text, "card in beta") {
		t.Fatalf("a moved suggestion waits again, now for the other project: %+v", moved)
	}
	e.answer(ss[0].ID, "allow")
	if got := e.cardTitles("beta"); len(got) != 1 || len(e.cardTitles("alpha")) != 0 {
		t.Fatalf("yes puts it into the project it moved to: %v", got)
	}
	if code, _ := e.call("deck.ask_answer", map[string]any{"ask": ss[0].ID, "answer": "move", "project": "alpha"}); code == 200 {
		t.Fatal("an answered suggestion does not move")
	}
}

func TestAutoModeRunsTheSuggestion(t *testing.T) {
	e := newEnv(t, suggester)
	p := e.createProject("alpha", strings.Replace(suggestManifest, `"version": 5,`, `"version": 5, "autonomy": "auto",`, 1))
	got := e.waitState(e.run(p, "work"), "done")
	if strings.Contains(string(got.Result), "refused") || len(e.suggestions()) != 0 {
		t.Fatalf("in auto a suggestion is done at once: %s", got.Result)
	}
	if titles := e.cardTitles("alpha"); len(titles) != 1 || titles[0] != "Fix the parser" {
		t.Fatalf("the card is made: %v", titles)
	}
	var rows []struct {
		ID      int64
		Text    string
		CanUndo bool `json:"can_undo"`
	}
	e.must("deck.unattended_list", map[string]any{}, &rows)
	if len(rows) != 1 || !strings.Contains(rows[0].Text, "three runs lost a line") || !rows[0].CanUndo {
		t.Fatalf("it shows in done without you with an undo: %+v", rows)
	}
	e.must("deck.unattended_undo", map[string]any{"entry": rows[0].ID}, nil)
	if titles := e.cardTitles("alpha"); len(titles) != 0 {
		t.Fatalf("undo takes the card away: %v", titles)
	}
}

func TestAnUnansweredSuggestionExpires(t *testing.T) {
	e := newClockEnv(t, suggester)
	p := e.createProject("alpha", suggestManifest)
	e.waitState(e.run(p, "work"), "done")
	if len(e.suggestions()) != 1 {
		t.Fatal("the suggestion waits")
	}
	e.Clock.Add(6 * 24 * time.Hour)
	e.sweep()
	if len(e.suggestions()) != 1 {
		t.Fatal("it waits seven days")
	}
	e.Clock.Add(25 * time.Hour)
	e.sweep()
	var all []map[string]any
	e.must("deck.suggestions_rejected", map[string]any{}, &all)
	if len(e.suggestions()) != 0 || len(all) != 0 {
		t.Fatalf("after seven days it expires, and that is no refusal: %+v", all)
	}
	e.must("deck.settings_set", map[string]any{"suggest_days": 1}, nil)
	e.waitState(e.run(p, "work"), "done")
	e.Clock.Add(25 * time.Hour)
	e.sweep()
	if len(e.suggestions()) != 0 {
		t.Fatal("the term is a setting")
	}
}
