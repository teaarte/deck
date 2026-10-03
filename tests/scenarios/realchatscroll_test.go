package scenarios

import (
	"context"
	"os"
	"strings"
	"testing"

	"deck2/internal/provider"
)

// TestRealBrowserChatStaysOnTheLastReply: the feed of the master opens, reloads and grows at its last reply:
// DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowserChatStaysOnTheLastReply -v.
func TestRealBrowserChatStaysOnTheLastReply(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Text: "## Done\n\n" + strings.Repeat("A long line of the reply that takes room in the feed. ", 12)}
	})
	e.must("deck.project_create", map[string]any{"name": "groceries", "prompt": "# Groceries: the shopping list.\n"}, nil)
	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 800})
	b.open(e.URL + "/?token=" + token + "#/deck/cockpit")
	b.until("the cockpit", `document.querySelector('[data-chat-toggle]')`)
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the chat", `document.querySelector('[data-chat-input]')`)
	for i := range 6 {
		b.type_(`document.querySelector('[data-chat-input]')`, "message "+string(rune('a'+i)))
		b.eval(`(document.querySelector('[data-chat-send]').click(), '')`)
		b.until("the reply", `document.querySelectorAll('[data-message="master"]').length === `+string(rune('1'+i)))
	}
	bottom := `(() => { const v = document.querySelector('[data-feed] .mantine-ScrollArea-viewport') || document.querySelector('[data-feed]');
		return v.scrollHeight > v.clientHeight + 100 && v.scrollHeight - v.scrollTop - v.clientHeight < 8 })()`
	b.until("the feed at its last reply", bottom)
	e.must("deck.project_create", map[string]any{"name": "other", "prompt": "# Other.\n"}, nil)
	b.until("the feed at its last reply after an event", bottom)
	b.open(e.URL + "/#/p/groceries/board")
	b.until("the feed at its last reply after a move", bottom)
	b.eval(`(location.reload(), '')`)
	b.until("the chat after a reload", `document.querySelectorAll('[data-message="master"]').length === 6`)
	b.until("the feed at its last reply after a reload", bottom)
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { const box = await page.locator('[data-feed]').boundingBox();
		await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2); await page.mouse.wheel(0, -5000); }`})
	b.until("the owner up in the feed", `document.querySelector('[data-feed] .mantine-ScrollArea-viewport').scrollTop < 100`)
	b.type_(`document.querySelector('[data-chat-input]')`, "one more")
	b.eval(`(document.querySelector('[data-chat-send]').click(), '')`)
	b.until("the feed at the reply to a message the owner sent from above", `document.querySelectorAll('[data-message="master"]').length === 7 && `+bottom)
}
