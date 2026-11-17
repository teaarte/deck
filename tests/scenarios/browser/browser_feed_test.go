package browser

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestRealBrowserAPressAsksAndTheAnswerComesToTheFeed: a button of a module starts its agent, which asks the owner;
// the answer on Today reaches the agent, at once or by its resumed turn, whose note of it comes to the feed of Today.
func TestRealBrowserAPressAsksAndTheAnswerComesToTheFeed(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		answer := c.Req.Prompt
		if !strings.Contains(answer, "the owner has responded") {
			d := c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{
				{"question": "Which shelf?", "options": []map[string]any{{"label": "top"}, {"label": "bottom"}}}}})
			if d["behavior"] != "allow" {
				return provider.Result{Text: "asked"}
			}
			got, _ := json.Marshal(d["updatedInput"])
			answer = string(got)
		}
		shelf := "top"
		if strings.Contains(answer, `bottom`) {
			shelf = "bottom"
		}
		_, _ = c.Tool(ctx, "owner_notify", map[string]any{"text": "Put on the " + shelf + " shelf"})
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}))
	e.Project("shelf", harness.Manifest(`{"commands": [{"name": "sort", "title": "Sort", "description": "Asks where to put it.",
    "input": {"type": "object", "properties": {}}, "executor": {"type": "agent", "prompt": "Ask the owner which shelf."},
    "result": {"type": "object", "properties": {}}}],
  "screens": [{"name": "main", "title": "Shelf", "blocks": [{"type": "actions", "title": "Books", "actions": [{"label": "Sort", "command": "sort"}]}]}],
  "menu": ["main"]}`))

	b := Open(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.Open(e, "#/p/shelf/main")
	b.until("the Sort button", `[...document.querySelectorAll('button')].some((x) => x.innerText === 'Sort')`)
	b.eval(`([...document.querySelectorAll('button')].find((x) => x.innerText === 'Sort').click(), '')`)
	e.WaitAsk()
	b.open(b.base + "/#/deck/cockpit")
	card := `document.querySelector('[data-focus-card]')`
	b.until("the question of the run on Today", `document.querySelector('[data-kind]')?.innerText === 'Question' && `+card+`.innerText.includes('Which shelf?')`)
	b.shot("feed-question")
	b.eval(`(` + card + `.querySelector('[data-option="bottom"]').click(), '')`)
	b.until("Answer to unlock", `!([...`+card+`.querySelectorAll('button')].find((x) => x.innerText === 'Answer')?.disabled ?? true)`)
	b.eval(`([...` + card + `.querySelectorAll('button')].find((x) => x.innerText === 'Answer').click(), '')`)
	b.until("the note of the answer in the feed of Today", `[...document.querySelectorAll('[data-item]')].some((x) => x.innerText.includes('Put on the bottom shelf'))`)
	b.shot("feed-note")
}
