package browser

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// visibleText is the text of an element a person sees: folded details are not.
const visibleText = `((sel) => document.querySelector(sel)?.innerText ?? '')`

// TestRealBrowserARequestInWords: a request of four assignments reads on Today and in the chat, on a desktop and a
// phone, as what changes and why in the topic's language, the call only under its details.
func TestRealBrowserARequestInWords(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if !strings.Contains(c.Req.Prompt, "назначь модели") || strings.Contains(c.Req.Prompt, "This continues") {
			return provider.Result{Text: "ok"}
		}
		var places []string
		for _, cmd := range []string{"r1", "r2", "r3", "r4"} {
			places = append(places, fmt.Sprintf(`{"role": "text", "project": "films", "command": %q, "models": ["openrouter/qwen"]}`, cmd))
		}
		out, _ := c.Tool(ctx, "assignment_set", map[string]any{"assignments": "[" + strings.Join(places, ", ") + "]", "why": "Нужно для 4-го захода опыта."})
		return provider.Result{Text: "Попросил модели: " + out}
	}))
	e.Must("deck.setting_set", map[string]any{"name": "language", "value": "ru"}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen", "kind": "text", "call_usd": 0.01}, nil)
	e.Project("films", harness.TestManifest([]string{harness.AgentCommand("r1", ""), harness.AgentCommand("r2", ""),
		harness.AgentCommand("r3", ""), harness.AgentCommand("r4", "")}, nil))
	topic := e.Say(0, "назначь модели для четырёх команд films")
	e.WaitAsk()

	b := Open(t)
	for _, size := range []struct {
		name          string
		width, height int
	}{{"desktop", 1400, 900}, {"phone", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.width, "height": size.height})
		b.Open(e, "#/deck/cockpit")
		b.until("the request on Today", `document.querySelector('[data-focus]')?.innerText.includes('Нужно для 4-го захода опыта.')`)
		today := b.eval(visibleText + `('[data-focus]')`)
		if strings.Count(today, "→ openrouter/qwen") != 4 || !strings.Contains(today, "Назначить модели") || strings.ContainsAny(today, "{}") ||
			strings.Contains(today, "assignment_set") {
			t.Fatalf("Today on a %s says what changes and why in words, the call only under its details: %s", size.name, today)
		}
		b.shot("request-today-" + size.name)

		b.open(b.base + fmt.Sprintf("/#/deck/chat?topic=%d", topic))
		b.until("the request in the topic", `document.querySelector('[data-chat-ask] [data-change]')?.innerText.includes('→ openrouter/qwen')`)
		card := b.eval(visibleText + `('[data-chat-ask]')`)
		if strings.Count(card, "→ openrouter/qwen") != 4 || !strings.Contains(card, "Нужно для 4-го захода опыта.") || strings.ContainsAny(card, "{}") ||
			strings.Contains(card, "assignment_set") {
			t.Fatalf("the request in the chat on a %s says what changes and why in words: %s", size.name, card)
		}
		b.shot("request-chat-" + size.name)
	}
}
