package browser

import (
	"context"
	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"fmt"
	"strings"
	"testing"
	"time"
)

var longQuestion = "The groceries module keeps two lists, and the shop list has not been opened for three weeks. " +
	strings.Repeat("Merging changes the screens and the widget of the module, and the old items get the tag of their list. ", 6) +
	"Should I merge them into one list, keep both as they are, or move the old items somewhere else?"

// TestRealBrowserMasterQuestions: a long question with options described and two questions one by one, the second of
// many answers, fit in the chat above the waiting turn on a computer and a phone, and each answer reaches the master.
func TestRealBrowserMasterQuestions(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	opt := func(label, description string) map[string]any {
		return map[string]any{"label": label, "description": description}
	}
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		c.Req.OnEvent(provider.Event{Kind: provider.EventMessage, Text: "Reading the lists"})
		c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t1", Title: "records_list", Text: "{}"})
		c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t2", Title: "document_read", Text: "{}"})
		var questions []any
		switch {
		case strings.Contains(c.Req.Prompt, "long one"):
			questions = []any{map[string]any{"question": longQuestion, "header": "Lists", "options": []any{
				opt("Merge", "One list with a tag of where each item came from; the screens change."),
				opt("Keep both", "Nothing changes; the shop list stays as it is."),
				opt("Archive the old", "Items older than a month go to an archive list nobody sees."),
				opt("Delete the shop list", "The shop list and its items are gone for good."),
			}}}
		case strings.Contains(c.Req.Prompt, "two at once"):
			questions = []any{
				map[string]any{"question": "Which list do new items go to?", "header": "List", "options": []any{
					opt("shop", "The list of the supermarket."), opt("market", "The list of the Saturday market.")}},
				map[string]any{"question": "What should each item show?", "header": "Fields", "multiSelect": true, "options": []any{
					opt("price", "The last price paid, from the receipts."), opt("shop", "Where it was bought last."),
					opt("photo", "A small photo of the package.")}},
			}
		default:
			return provider.Result{Text: "Done."}
		}
		d := c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": questions})
		answers, _ := d["updatedInput"].(map[string]any)["answers"].(map[string]any)
		var got []string
		for _, q := range questions {
			got = append(got, fmt.Sprint(answers[q.(map[string]any)["question"].(string)]))
		}
		return provider.Result{Text: "Answers: " + strings.Join(got, " | ")}
	}), harness.WithConfig(

		func(c *app.Config) { c.QuestionWait = 5 * time.Minute; c.ConfirmTTL = time.Hour }))

	e.Must("deck.project_create", map[string]any{"name": "groceries", "prompt": "# Groceries: the shopping list.\n", "manifest": harness.TasksManifest}, nil)

	b := Open(t)
	say := func(text string) {
		b.type_(`document.querySelector('[data-chat-input]')`, text)
		b.eval(`(document.querySelector('[data-chat-send]').click(), '')`)
	}
	pick := func(label string) {
		b.eval(fmt.Sprintf(`(q().querySelector('[data-option=%q]').click(), '')`, label))
	}
	send := func() {
		b.eval(`([...q().querySelectorAll('button')].filter((x) => !x.disabled && x.innerText.trim() !== '').pop().click(), '')`)
	}
	// inside says the whole question fits in the feed of the chat, above the turn.
	inside := `(() => { const q0 = q(), t = document.querySelector('[data-turn]'); if (!q0 || !t) return false;
		const box = (document.querySelector('[data-chat-panel]') ?? document.querySelector('[data-chat-phone] .mantine-Drawer-content')).getBoundingClientRect();
		q0.scrollIntoView({block: 'end'}); const r = q0.getBoundingClientRect();
		return r.left >= box.left - 1 && r.right <= box.right + 1 && r.bottom <= t.getBoundingClientRect().top + 1 &&
			r.height <= document.querySelector('[data-chat-panel] [data-feed], [data-chat-phone] [data-feed]').getBoundingClientRect().height })()`
	for _, look := range []struct {
		name   string
		w, h   int
		screen string
	}{{"pc", 1400, 900, "#/p/groceries/tasks"}, {"phone", 390, 844, "#/deck/cockpit"}} {
		b.call("browser_resize", map[string]any{"width": look.w, "height": look.h})
		b.Open(e, look.screen)
		if look.name == "pc" {
			b.until("the tasks", `document.querySelector('[data-block]')`)
			b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
		}
		b.until("the chat input", `document.querySelector('[data-chat-input]')?.offsetParent`)
		b.eval(`(window.q = () => document.querySelector('[data-chat-panel] [data-question], [data-chat-phone] [data-question]'), '')`)
		if look.name == "phone" {
			b.eval(`(document.querySelector('[data-new-topic]').click(), '')`)
			b.until("a new topic", `!document.querySelector('[data-message]')`)
		}

		say("ask me the long one")
		b.until("the long question in the feed", `q()?.innerText.includes('three weeks')`)
		b.until("each option with what it means", `q().querySelectorAll('[data-option] [data-option-description]').length === 4`)
		b.until("the turn waits for the owner", `document.querySelector('[data-turn] [data-turn-now]')?.innerText === 'waits for you' && document.querySelector('[data-turn] [data-pose]')?.dataset.pose === 'wait'`)
		b.until("the question within the chat, above the turn", inside)
		if look.name == "phone" {
			b.until("a long question scrolls in its own box", `(() => { const x = q().querySelector('[data-question-body]'); return x.scrollHeight > x.clientHeight })()`)
		}
		b.shot("questions-long-" + look.name)
		pick("Keep both")
		b.until("the option picked", `q()?.querySelector('[data-option="Keep both"]')?.dataset.checked !== undefined`)
		send()
		b.until("the reply with the answer", `[...document.querySelectorAll('[data-message="master"]')].some((m) => m.innerText.includes('Answers: Keep both'))`)
		b.until("the question gone", `!q()`)

		say("ask me two at once")
		b.until("the first of two questions alone", `q()?.querySelector('[data-question-head]')?.innerText === '1 of 2 · List' && !q().innerText.includes('What should each item show?')`)
		b.until("next waits for a pick", `q()?.querySelector('[data-question-next]')?.disabled`)
		b.until("the question within the chat, above the turn", inside)
		b.shot("questions-two-" + look.name)
		pick("market")
		b.eval(`(q()?.querySelector('[data-question-next]').click(), '')`)
		b.until("the second question, of many answers", `q()?.querySelector('[data-question-head]')?.innerText === '2 of 2 · Fields · several may be picked' && q().innerText.includes('What should each item show?')`)
		pick("price")
		pick("photo")
		b.until("both picked", `q().querySelectorAll('[data-option][data-checked]').length === 2`)
		b.shot("questions-two-second-" + look.name)
		b.eval(`(q().querySelector('[data-question-back]').click(), '')`)
		b.until("back on the first with its pick", `q()?.querySelector('[data-option="market"]')?.dataset.checked !== undefined`)
		b.eval(`(q()?.querySelector('[data-question-next]').click(), '')`)
		send()
		b.until("the reply with both answers", `[...document.querySelectorAll('[data-message="master"]')].some((m) => m.innerText.includes('market') && m.innerText.includes('price') && m.innerText.includes('photo'))`)
		b.until("the questions gone", `!q()`)
	}
}
