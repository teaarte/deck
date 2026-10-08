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
	b.until("only the topics of web", topics()+` === 'about web'`)
	b.eval(`(document.body.click(), '')`)
	scope("Without a module")
	b.until("only the topics without a module", topics()+` === 'about nothing'`)
}
