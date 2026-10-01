package scenarios

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider"
)

// TestRealBrowserMasterChat talks to the master from the side panel in the real browser: a topic and its reply,
// a question answered in the feed, a turn stopped, and the panel on the whole screen of a phone.
func TestRealBrowserMasterChat(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		switch {
		case strings.Contains(c.Req.Prompt, "which list"):
			c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t1", Title: "cards_list", Text: "{}"})
			d := c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []any{map[string]any{"question": "Which list?",
				"options": []any{map[string]any{"label": "shop"}, map[string]any{"label": "market"}}}}})
			answers, _ := d["updatedInput"].(map[string]any)["answers"].(map[string]any)
			return provider.Result{Text: fmt.Sprint("Picked ", answers["Which list?"])}
		case strings.Contains(c.Req.Prompt, "think long"):
			c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t2", Title: "records_list", Text: "{}"})
			<-ctx.Done()
			return provider.Result{}
		}
		return provider.Result{Text: "Put it into **groceries** as card #1."}
	}, func(c *app.Config) { c.QuestionWait = 2 * time.Minute })
	e.must("deck.project_create", map[string]any{"name": "groceries", "prompt": "# Groceries: the shopping list.\n"}, nil)

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/p/groceries/board")
	b.until("the board", `document.querySelector('[data-lane]')`)
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the chat beside the screen", `document.querySelector('[data-chat-panel] [data-chat-input]') && document.querySelector('[data-lane]')`)
	say := func(text string) {
		b.type_(`document.querySelector('[data-chat-input]')`, text)
		b.eval(`(document.querySelector('[data-chat-send]').click(), '')`)
	}
	say("we are out of milk")
	b.until("the master's reply in the feed", `[...document.querySelectorAll('[data-message="master"]')].some((m) => m.querySelector('strong')?.innerText === 'groceries')`)
	b.shot("chat-reply")
	ms := e.chatMessages(1)
	if len(ms) != 2 || !strings.Contains(string(ms[0].Screen), `"project":"groceries"`) || !strings.Contains(string(ms[0].Screen), `"screen":"board"`) {
		t.Fatalf("the message carries the screen the owner is on: %+v", ms)
	}

	say("which list then?")
	b.until("the question in the feed", `document.querySelector('[data-question]')?.innerText.includes('Which list?')`)
	b.until("the turn with its calls", `document.querySelector('[data-turn] [data-turn-line]')?.innerText.includes('cards_list')`)
	b.eval(`([...document.querySelectorAll('[data-question] input[type=checkbox], [data-question] input[type=radio]')].find((i) => i.value === 'market').click(), '')`)
	b.eval(`([...document.querySelectorAll('[data-question] button')].find((x) => !x.disabled && x.innerText.trim() !== '').click(), '')`)
	b.until("the reply after the answer", `[...document.querySelectorAll('[data-message="master"]')].some((m) => m.innerText.includes('Picked market'))`)
	b.until("the question gone", `!document.querySelector('[data-question]')`)

	say("think long about it")
	b.until("the running turn with stop", `document.querySelector('[data-turn="running"] [data-verb="stop"]')`)
	b.shot("chat-turn")
	b.eval(`(document.querySelector('[data-turn] [data-verb="stop"]').click(), '')`)
	b.until("the stopped reply", `document.querySelector('[data-reply-state="stopped"]') && !document.querySelector('[data-turn]')`)

	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.open(e.URL + "/#/deck/cockpit")
	b.until("the chat on the whole phone screen", `(() => { const i = document.querySelector('[data-chat-input]'); if (!i) return false;
		const r = i.getBoundingClientRect(); return r.width > 300 && !document.querySelector('[data-chat-panel]') })()`)
	b.until("the newest topic open on the phone", `document.querySelector('[data-message="master"]')`)
	b.eval(`(document.querySelector('[data-new-topic]').click(), '')`)
	b.until("a new topic", `!document.querySelector('[data-message]')`)
	say("from the phone")
	b.until("the reply on the phone", `[...document.querySelectorAll('[data-message="master"]')].some((m) => m.innerText.includes('groceries'))`)
	b.shot("chat-phone")
	var ts []chatTopic
	e.must("deck.chat_topics", nil, &ts)
	if len(ts) != 2 || ts[0].Title != "from the phone" {
		t.Fatalf("the phone opened a topic of its own: %+v", ts)
	}
}
