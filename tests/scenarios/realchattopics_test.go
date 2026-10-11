package scenarios

import (
	"context"
	"os"
	"testing"

	"deck2/internal/provider"
)

// TestRealBrowserChatTopicsFollowTheModuleFilter: the module picked above the chat narrows its topics, without a
// module too: DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowserChatTopicsFollowTheModuleFilter -v.
func TestRealBrowserChatTopicsFollowTheModuleFilter(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result { return provider.Result{Text: "ok"} })
	e.createProject("web", webNotes)
	e.masterSaid(e.say(0, "about web", map[string]any{"screen": `{"project":"web","screen":"main"}`}), 1)
	e.masterSaid(e.say(0, "about master", map[string]any{"screen": `{"project":"master","screen":"documents"}`}), 1)
	e.masterSaid(e.say(0, "about nothing"), 1)

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/deck/cockpit")
	b.until("the cockpit", `document.querySelector('[data-chat-toggle]')`)
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the module filter", `document.querySelector('[data-topic-project]')`)
	topics := func() string {
		return `(() => { document.querySelector('[data-topics]').click(); const id = document.querySelector('[data-topics]').getAttribute('aria-controls');
			const box = id && document.getElementById(id); return [...(box || document).querySelectorAll('[role=option]')].map((o) => o.innerText.trim()).sort().join('|') })()`
	}
	b.until("all topics", topics()+` === 'about master|about nothing|about web'`)
	b.eval(`(document.body.click(), '')`)
	scope := func(label string) {
		b.eval(`(document.querySelector('[data-topic-project]').click(), '')`)
		b.until("the module "+label, `[...document.querySelectorAll('[data-topic-projects] [role=menuitem]')].some((x) => x.innerText.trim() === '`+label+`')`)
		b.eval(`([...document.querySelectorAll('[data-topic-projects] [role=menuitem]')].find((x) => x.innerText.trim() === '` + label + `').click(), '')`)
	}
	scope("web")
	b.until("the last topic of web open", `document.querySelector('[data-topics]').value === 'about web'`)
	b.until("only the topics of web", topics()+` === 'about web'`)
	b.eval(`(document.body.click(), '')`)
	scope("Without a module")
	b.until("only the topics without a module", topics()+` === 'about nothing'`)
	b.eval(`(document.querySelector('[data-topic-project]').click(), '')`)
	b.until("the modules opened last on top", `[...document.querySelectorAll('[data-topic-projects] [role=menuitem]')].map((x) => x.innerText.trim()).slice(1, 3).join('|') === 'Without a module|web'`)
	b.eval(`([...document.querySelectorAll('[data-topic-projects] [role=menuitem]')].find((x) => x.innerText.trim() === 'All modules').click(), '')`)
	options := `(() => { document.querySelector('[data-topics]').click(); const id = document.querySelector('[data-topics]').getAttribute('aria-controls');
		const box = id && document.getElementById(id); return [...(box || document).querySelectorAll('[role=option]')].map((o) => o.innerText.trim()).join('|') })()`
	b.until("the topics newest first", options+` === 'about nothing|about master|about web'`)
	b.eval(`([...document.querySelectorAll('[role=option]')].find((o) => o.innerText.trim() === 'about web').click(), '')`)
	b.until("the topic picked", `document.querySelector('[data-topics]').value === 'about web'`)
	b.until("the open topic first", options+` === 'about web|about nothing|about master'`)
	b.eval(`(document.body.click(), '')`)

	b.until("the model of the topic, the master's own", `document.querySelector('[data-topic-model]')?.dataset.topicModel === 'fake' && document.querySelector('[data-topic-model]').dataset.variant === 'subtle'`)
	b.eval(`(document.querySelector('[data-topic-model]').click(), '')`)
	b.until("the models to pick, the master's own once", `document.querySelector('[data-topic-models] [data-model="quiet"]') && !document.querySelector('[data-topic-models] [data-model="fake"]')`)
	b.eval(`(document.querySelector('[data-topic-models] [data-model="quiet"]').click(), '')`)
	b.until("the topic keeps the model picked", `document.querySelector('[data-topic-model]').dataset.variant === 'light'`)

	b.type_(`document.querySelector('[data-chat-input]')`, "remember this line")
	b.eval(`(document.querySelector('[data-chat-send]').click(), '')`)
	b.until("the input empty after sending", `document.querySelector('[data-chat-input]').value === ''`)
	key := func(k string) { b.eval(`(document.querySelector('[data-chat-input]').dispatchEvent(new KeyboardEvent('keydown', { key: '` + k + `', bubbles: true })), '')`) }
	key("ArrowUp")
	b.until("arrow up brings back the message sent", `document.querySelector('[data-chat-input]').value === 'remember this line'`)
	key("ArrowDown")
	b.until("arrow down goes back to an empty input", `document.querySelector('[data-chat-input]').value === ''`)
}
