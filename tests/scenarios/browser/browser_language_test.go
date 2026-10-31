package browser

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// english are the words in Latin letters of a part of the page, but for the names of modules, models and commands.
const english = `((el, names) => (el?.innerText ?? '').split(/[^A-Za-z]+/).filter((w) => w.length > 2 && !names.some((n) => n.toLowerCase().split(/[^a-z]+/).includes(w.toLowerCase()))).join(' '))`

// TestRealBrowserRussianTodayAndChat: in Russian, Today, its card and the chat have no English word of deck; the turn
// stands over the input, the input has no ring in focus, a button reached by the keyboard has one.
func TestRealBrowserRussianTodayAndChat(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	var from atomic.Int64
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch {
		case c.Command() == "work":
			return provider.Result{Text: "Посчитал: **3 записи**."}
		case strings.Contains(c.Req.Prompt, "подумай"):
			c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t1", Title: "records_list", Text: "{}"})
			<-ctx.Done()
			return provider.Result{}
		case strings.Contains(c.Req.Prompt, "посчитай"):
			r := harness.HandOver(ctx, c, &from)
			r.Text = strings.Replace(r.Text, "Handed it over.", "Передал в работу.", 1)
			return r
		}
		return provider.Result{Text: "Готово."}
	}))

	e.Must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, nil)
	e.Project("shelf", harness.WidgetsManifest)
	e.Must("deck.records_upsert", map[string]any{"project": "shelf", "collection": "items", "records": harness.Jsonl(map[string]any{"name": "a", "n": 1})}, nil)
	topic := e.Say(0, "посчитай записи")
	e.Told(topic, "done", "ru")
	e.Say(topic, "подумай ещё")
	names := `['deck', 'shelf', 'master', 'work', 'records_list', 'fake', 'USD']`

	b := Open(t)
	for _, phone := range []bool{false, true} {
		look := "computer"
		b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
		if phone {
			look = "phone"
			b.call("browser_resize", map[string]any{"width": 390, "height": 844})
		}
		b.Open(e, "&tab="+look+"#/")
		b.until("Today's card in Russian, with how long ago", `document.querySelector('[data-cell="today"] [data-since]') && document.querySelector('[data-cell="today"]').innerText.includes('Сегодня')`)
		time.Sleep(500 * time.Millisecond)
		if got := b.eval(english + `(document.querySelector('[data-cell="today"]'), ` + names + `)`); got != "" {
			t.Errorf("Today's card on a %s has English words: %s", look, got)
		}
		b.open(e.URL + "/#/deck/cockpit")
		b.until("Today with what runs", `document.querySelector('[data-block] [data-since]')`)
		time.Sleep(500 * time.Millisecond)
		b.shot("russian-today-" + look)
		if got := b.eval(english + `(document.querySelector('main') ?? document.body, ` + names + `)`); got != "" {
			t.Errorf("Today on a %s has English words: %s", look, got)
		}
	}

	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.open(e.URL + "/#/")
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the topic of the work in the chat", `document.querySelector('[data-chat-panel] [data-told="done"]')`)
	if got := b.eval(`document.querySelector('[data-told="done"] button').innerText`); got != "Открыть процесс" {
		t.Errorf("the button of the work's process in Russian: %q", got)
	}
	b.until("the running turn", `document.querySelector('[data-turn="running"] [data-turn-head]')`)
	b.shot("russian-chat")
	if got := b.eval(english + `(document.querySelector('[data-chat-panel]'), ` + names + `)`); got != "" {
		t.Errorf("the chat has English words: %s", got)
	}
	if got := b.eval(`(() => { const t = document.querySelector('[data-turn]').getBoundingClientRect(), c = document.querySelector('[data-composer]').getBoundingClientRect(),
		f = document.querySelector('[data-turn]').closest('[data-chat]').querySelector('[data-turn]').parentElement === document.querySelector('[data-composer]').parentElement;
		return String(f && t.bottom <= c.top + 1 && c.top - t.bottom < 24) })()`); got != "true" {
		t.Errorf("the turn stands right over the input: %s", got)
	}
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { await page.locator('[data-chat-input]').focus(); await page.keyboard.press('End'); }`})
	if got := b.eval(`(() => { const i = document.querySelector('[data-chat-input]'), c = document.querySelector('[data-composer]'); const s = getComputedStyle(i), k = getComputedStyle(c);
		return [document.activeElement === i, s.outlineStyle, k.boxShadow.includes('inset 0px 0px 0px 1px') ].join(' ') })()`); !strings.HasPrefix(got, "true none") {
		t.Errorf("the input in focus has no ring: %s", got)
	}
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { await page.locator('[data-chat-input]').focus(); await page.keyboard.press('Shift+Tab'); }`})
	if got := b.eval(`(() => { const a = document.activeElement; const s = getComputedStyle(a); return a.tagName + ' ' + (s.outlineStyle !== 'none' && parseFloat(s.outlineWidth) > 0) })()`); !strings.HasSuffix(got, "true") {
		t.Errorf("a button reached by the keyboard shows its ring: %s", got)
	}
}
