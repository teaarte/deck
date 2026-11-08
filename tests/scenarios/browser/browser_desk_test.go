package browser

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"deck2/tests/harness"
)

// TestRealBrowserTheDesk: a long press opens the placing, a card moves and grows by its corner and stays so after a
// reload; the open chat narrows the grid unsaved; a roomy card offers Deki's view; the phone fits its own.
func TestRealBrowserTheDesk(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	for _, name := range []string{"alpha", "beta"} {
		e.Project(name, harness.PlainManifest)
	}
	e.Project("shelf", harness.WidgetsManifest)
	e.Must("deck.record_upsert", map[string]any{"project": "shelf", "collection": "items", "records": harness.Jsonl(map[string]any{"name": "a", "n": 1})}, nil)
	b := Open(t)
	cell := func(id string) string {
		return `(() => { const r = document.querySelector('[data-cell="` + id + `"]').getBoundingClientRect(); return [Math.round(r.left), Math.round(r.top), Math.round(r.width), Math.round(r.height)].join(' ') })()`
	}
	b.call("browser_resize", map[string]any{"width": 1400, "height": 1000})
	b.Open(e, "")
	b.until("the cards in cells, Today first", `document.querySelector('[data-cell="today"]') && document.querySelector('[data-cell="beta"]') && document.querySelector('[data-cell="shelf"] [data-widget] [data-block]')`)
	before := b.eval(cell("beta"))
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { const r = await page.locator('[data-cell="alpha"]').boundingBox();
		await page.mouse.move(r.x + r.width / 2, r.y + r.height / 2); await page.mouse.down(); await page.waitForTimeout(800); await page.mouse.up(); }`})
	b.until("the placing open by a long press", `document.querySelector('[data-apps][data-placing]') && document.querySelector('[data-placing-done]')`)
	b.shot("desk-placing")
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { const r = await page.locator('[data-cell="beta"]').boundingBox();
		const room = await page.locator('[data-cols]').boundingBox();
		await page.mouse.move(r.x + r.width / 2, r.y + r.height / 2); await page.mouse.down();
		await page.mouse.move(r.x + r.width / 2 + 10, r.y + r.height * 2.6, { steps: 8 }); await page.mouse.move(room.x + r.width / 2, r.y + r.height * 2.6, { steps: 8 }); await page.mouse.up(); }`})
	e.Until("the moved card kept", func() bool {
		v := desk(b, colsOf(b))
		for _, c := range v.Cards {
			if c.ID == "beta" && c.Y >= 2 {
				return v.Own
			}
		}
		return false
	})
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { const h = await page.locator('[data-cell="beta"] .react-resizable-handle').boundingBox();
		await page.mouse.move(h.x + h.width / 2, h.y + h.height / 2); await page.mouse.down();
		await page.mouse.move(h.x + 260, h.y + 40, { steps: 10 }); await page.mouse.up(); }`})
	e.Until("the grown card kept", func() bool {
		for _, c := range desk(b, colsOf(b)).Cards {
			if c.ID == "beta" && c.W >= 2 {
				return true
			}
		}
		return false
	})
	b.until("a roomy card offers Deki's view", `document.querySelector('[data-cell="beta"] [data-widget-draw]')`)
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { const c = await page.locator('[data-cell="shelf"]').boundingBox();
		const h = await page.locator('[data-cell="shelf"] .react-resizable-handle').boundingBox();
		await page.mouse.move(h.x + h.width / 2, h.y + h.height / 2); await page.mouse.down();
		await page.mouse.move(h.x + c.width * 1.1, h.y + c.height * 1.1, { steps: 10 }); await page.mouse.up(); }`})
	b.until("a grown card shows its larger view", `document.querySelector('[data-cell="shelf"] [data-widget="2x2"] [data-item]')`)
	b.eval(`(document.querySelector('[data-placing-done]').click(), '')`)
	b.until("the placing closed", `!document.querySelector('[data-apps][data-placing]')`)
	moved := b.eval(cell("beta"))
	if moved == before {
		t.Fatalf("the card moved: %s", moved)
	}
	b.open(b.base + "/?reload=1")
	b.until("the card where it was put after a reload", cell("beta")+` === '`+moved+`'`)
	b.shot("desk")
	cols := colsOf(b)
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the chat open narrows the grid", fmt.Sprintf(`Number(document.querySelector('[data-cols]').dataset.cols) < %d`, cols))
	b.shot("desk-chat")
	if got := b.eval(pencilOverChat); got != "" {
		t.Fatalf("the pencil of the placing lies over the open chat: %s", got)
	}
	b.eval(`(document.querySelector('[data-chat-close]').click(), '')`)
	b.until("the grid as it was once the chat closes", cell("beta")+` === '`+moved+`'`)
	if v := desk(b, cols-1); v.Own && v.Columns == int64(cols-1) {
		t.Fatalf("a narrower grid of the open chat is not saved: %+v", v)
	}

	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.open(b.base + "/?reload=2")
	b.until("the cards on a phone", `document.querySelector('[data-phone-tabs]') && document.querySelector('[data-cell="beta"]')`)
	if got := b.eval(`String(document.documentElement.scrollWidth <= innerWidth + 1)`); got != "true" {
		t.Fatalf("the cards fit the phone: %s", got)
	}
	b.shot("desk-phone")
	b.eval(`(document.querySelector('[data-phone-tabs] [data-chat-toggle]').click(), '')`)
	b.until("the chat open on the phone", `Math.abs(document.querySelector('[data-chat-phone] [data-chat-close]')?.closest('.mantine-Drawer-content')?.getBoundingClientRect().left ?? 99) < 1`)
	b.shot("desk-chat-390")
	if got := b.eval(pencilOverChat); got != "" {
		t.Fatalf("the pencil of the placing lies over the open chat on a phone: %s", got)
	}
	b.eval(`(document.querySelector('[data-chat-close]').click(), '')`)
	if !strings.Contains(b.eval(`document.querySelector('[data-cell="today"]').innerText`), "Today") {
		t.Fatal("Today stays on the phone")
	}
}

// pencilOverChat is empty unless the pencil of the placing lies over the open chat: where they meet, the chat is on top.
const pencilOverChat = `(() => {
	const p = document.querySelector('[data-placing-toggle]').getBoundingClientRect();
	const chat = [...document.querySelectorAll('[data-chat-close]')].map((c) => c.closest('[data-chat-panel], .mantine-Drawer-content')).find((c) => c && c.getBoundingClientRect().width > 0);
	if (!chat) return 'no open chat';
	const c = chat.getBoundingClientRect();
	const l = Math.max(p.left, c.left), r = Math.min(p.right, c.right), t = Math.max(p.top, c.top), b = Math.min(p.bottom, c.bottom);
	if (l >= r || t >= b) return '';
	const top = document.elementFromPoint((l + r) / 2, (t + b) / 2);
	return top && chat.contains(top) ? '' : 'pencil ' + [p.left, p.top, p.width, p.height].map(Math.round).join(' ') + ' over chat ' + [c.left, c.top, c.width, c.height].map(Math.round).join(' ');
})()`

// pageCall calls a command of deck from the page, signed in as the client is, and gives what it answered.
func pageCall(b *playwright, command string, input any) string {
	b.t.Helper()
	in, _ := json.Marshal(input)
	return b.eval(fmt.Sprintf(`fetch('/api/commands/%s', { method: 'POST', body: %q }).then((r) => r.text())`, command, in))
}

// desk is the layout of the tiles deck keeps for the browser's own device.
func desk(b *playwright, cols int) (v struct {
	Columns int64
	Own     bool
	Cards   []harness.DeskCard
}) {
	b.t.Helper()
	out := pageCall(b, "deck.layout_get", map[string]any{"columns": cols})
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		b.t.Fatalf("the layout of the page's device: %s", out)
	}
	return v
}

// place places the tiles of the browser's own device for its columns.
func place(b *playwright, cols int, cards ...harness.DeskCard) {
	b.t.Helper()
	raw, _ := json.Marshal(cards)
	if out := pageCall(b, "deck.layout_set", map[string]any{"columns": cols, "cards": string(raw)}); strings.HasPrefix(out, "deck:") {
		b.t.Fatalf("the page places its tiles: %s", out)
	}
}

// colsOf is the number of columns of the tiles the browser shows.
func colsOf(b *playwright) int {
	var n int
	fmt.Sscan(b.eval(`document.querySelector('[data-cols]').dataset.cols`), &n)
	return n
}

// TestRealBrowserTenWidgetsStayQuick: ten cards with their views draw at once, and a change of one module reads
// and draws again only its card.
func TestRealBrowserTenWidgetsStayQuick(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	for i := range 10 {
		name := fmt.Sprintf("shelf%d", i)
		e.Project(name, harness.WidgetsManifest)
		e.Must("deck.record_upsert", map[string]any{"project": name, "collection": "items", "records": harness.Jsonl(map[string]any{"name": "a", "n": 1})}, nil)
	}
	b := Open(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 1000})
	b.Open(e, "#/deck/schedule")
	b.until("deck open", `document.querySelector('[data-block]')`)
	b.eval(`(window.t1 = 0, window.t0 = performance.now(), location.hash = '#/',
		(function wait() { if (document.querySelectorAll('[data-widget] [data-block]').length >= 11) window.t1 = performance.now(); else requestAnimationFrame(wait) })(), '')`)
	b.until("ten views and Today drawn", `window.t1 > 0`)
	ms := b.eval(`String(Math.round(window.t1 - window.t0))`)
	t.Logf("ten views drawn in %s ms", ms)
	var n int
	fmt.Sscan(ms, &n)
	if n > 3000 {
		t.Errorf("ten views take %d ms to draw", n)
	}
	b.eval(`(window.deckDraws = {}, '')`)
	e.Must("deck.record_upsert", map[string]any{"project": "shelf3", "collection": "items", "records": harness.Jsonl(map[string]any{"name": "b", "n": 2})}, nil)
	b.until("the changed card read again", `[...document.querySelectorAll('[data-module="shelf3"] [data-widget]')].some((x) => x.innerText.includes('2'))`)
	time.Sleep(800 * time.Millisecond)
	var got map[string]float64
	_ = json.Unmarshal([]byte(b.eval(`JSON.stringify(window.deckDraws)`)), &got)
	t.Logf("draws after a change of one module: %v", got)
	if got["screen"] > 4 || got["shell"] > 0 {
		t.Errorf("a change of one module draws %v screens and the frame %v times", got["screen"], got["shell"])
	}
}
