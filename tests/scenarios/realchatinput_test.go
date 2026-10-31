package scenarios

import (
	"context"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestRealBrowserChatInput: pasted and dropped files attach as the plus does, the field has no focus ring, the turn is
// one line that opens in place, and a message tells its model, cost and time on hover or a press.
func TestRealBrowserChatInput(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if strings.Contains(c.Req.Prompt, "think long") {
			c.Req.OnEvent(provider.Event{Kind: provider.EventMessage, Text: "Reading the list"})
			c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t2", Title: "records_list", Text: "{}"})
			c.Req.OnEvent(provider.Event{Kind: provider.EventMessage, Text: "Weighing the shops"})
			c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t3", Title: "Bash", Text: "{}"})
			<-ctx.Done()
			return provider.Result{}
		}
		return provider.Result{Text: "Put it into groceries."}
	}))

	e.Must("deck.theme_save", map[string]any{"name": "ringed", "text": `{"version": 1, "title": {"en": "Ringed"}, "mood": "flat", "radius": "md",
		"css": ".field:focus { box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 22%, transparent); }"}`}, nil)
	e.Must("deck.theme_use", map[string]any{"name": "ringed"}, nil)
	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.Open(e, "#/deck/cockpit")
	b.until("the cockpit", `document.querySelector('[data-chat-toggle]')`)
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the chat", `document.querySelector('[data-chat-input]')`)

	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { await page.locator('[data-chat-input]').click(); await page.keyboard.type('a'); }`})
	b.until("the field in focus without a ring", `(() => { const i = document.querySelector('[data-chat-input]'); const st = getComputedStyle(i);
		return document.activeElement === i && st.boxShadow === 'none' && st.outlineStyle === 'none' })()`)
	b.shot("input-focus-pc")
	b.eval(pasteFiles)
	b.until("the pasted picture and file over the field", `document.querySelector('[data-attached-picture] img')?.naturalWidth === 16 &&
		document.querySelector('[data-attached-file]')?.textContent.includes('list.txt') && document.querySelector('[data-chat-input]').value === 'a'`)
	b.eval(`(() => { const dt = new DataTransfer(); dt.setData('text/html', '<table><tr><td>1</td></tr></table>'); dt.setData('text/plain', '1');
		dt.items.add(new File(['x'], 'cells.png', { type: 'image/png' }));
		document.querySelector('[data-chat-input]').dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true })); return '' })()`)
	b.eval(`(() => { const dt = new DataTransfer(); dt.items.add(new File(['eggs'], 'dropped.txt', { type: 'text/plain' }));
		document.querySelector('[data-composer]').dispatchEvent(new DragEvent('drop', { dataTransfer: dt, bubbles: true, cancelable: true })); return '' })()`)
	b.until("the dropped file beside them, a copy of cells from an app kept as its text", `[...document.querySelectorAll('[data-attached-file]')].some((f) => f.textContent.includes('dropped.txt')) &&
		document.querySelectorAll('[data-attached] > *').length === 3`)
	b.shot("input-paste-pc")
	b.type_(`document.querySelector('[data-chat-input]')`, "we are out of milk")
	b.eval(`(document.querySelector('[data-chat-send]').click(), '')`)
	b.until("the reply", `[...document.querySelectorAll('[data-message="master"]')].some((m) => m.innerText.includes('groceries'))`)
	ms := e.ChatMessages(1)
	var names []string
	for _, f := range ms[0].Files {
		names = append(names, f.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"dropped.txt", "image.png", "list.txt"}) {
		t.Fatalf("the pasted and dropped files went with the message: %v", names)
	}

	hover := func(sel string) {
		b.call("browser_run_code_unsafe", map[string]any{"code": fmt.Sprintf(`async (page) => { await page.locator(%q).last().hover(); }`, sel)})
	}
	tools := func(sel string) string {
		return fmt.Sprintf(`[...document.querySelectorAll(%q)].pop()?.querySelector(':scope > [data-line-tools]')`, sel)
	}
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { await page.mouse.move(5, 890); }`})
	b.until("how a message went hidden without hover", `getComputedStyle(`+tools(`[data-message="master"]`)+`).opacity === '0'`)
	hover(`[data-message="master"]`)
	b.until("the model, the time it took and when, on hover", `(() => { const m = `+tools(`[data-message="master"]`)+`; return getComputedStyle(m).opacity === '1' &&
		/^fake · .* · \d{1,2}:\d\d/.test(m.querySelector('[data-work-meta]').innerText) })()`)
	b.shot("input-hover-pc")
	hover(`[data-message="owner"]`)
	b.until("when the owner wrote it, on hover", `(() => { const m = `+tools(`[data-message="owner"]`)+`; return getComputedStyle(m).opacity === '1' &&
		/^\d{1,2}:\d\d/.test(m.innerText) })()`)
	b.shot("input-hover-owner-pc")

	b.type_(`document.querySelector('[data-chat-input]')`, "think long about it")
	b.eval(`(document.querySelector('[data-chat-send]').click(), '')`)
	b.until("the turn in one line: what it does, a timer, the model and Stop, its steps folded", oneLineTurn)
	b.shot("input-turn-pc")
	b.eval(`(document.querySelector('[data-turn-fold]').click(), '')`)
	b.until("the steps and the calls under the line, as wide as it and not moved right", openedTurn)
	b.shot("input-turn-open-pc")
	b.eval(`(document.querySelector('[data-turn-fold]').click(), '')`)
	b.until("the turn folded again", `!document.querySelector('[data-turn-open]') && document.querySelector('[data-turn-fold]').getAttribute('aria-expanded') === 'false'`)

	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.until("the chat on the phone", `document.querySelector('[data-chat-input]')?.getBoundingClientRect().width > 300 && document.querySelector('[data-turn="running"]')`)
	b.until("the turn in one line on the phone", oneLineTurn)
	b.shot("input-turn-phone")
	b.eval(`(document.querySelector('[data-turn-fold]').click(), '')`)
	b.until("the steps and the calls on the phone, not moved right", openedTurn)
	b.shot("input-turn-open-phone")
	b.eval(`(document.querySelector('[data-turn-fold]').click(), '')`)
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { await page.mouse.move(5, 5); }`})
	b.eval(`([...document.querySelectorAll('[data-message="owner"] [data-bubble]')][0].click(), '')`)
	b.until("on a phone a press shows when the message was written", `(() => { const m = document.querySelector('[data-message="owner"]');
		return m.dataset.shown !== undefined && getComputedStyle(m.querySelector(':scope > [data-line-tools]')).opacity === '1' })()`)
	b.shot("input-press-phone")
}

// pasteFiles pastes a picture and a text file into the chat input as a paste from the clipboard does.
const pasteFiles = `(() => { const i = document.querySelector('[data-chat-input]'); const c = document.createElement('canvas'); c.width = c.height = 16;
	const g = c.getContext('2d'); g.fillStyle = '#c33'; g.fillRect(0, 0, 16, 16);
	const bin = atob(c.toDataURL('image/png').split(',')[1]); const bytes = Uint8Array.from(bin, (x) => x.charCodeAt(0));
	const dt = new DataTransfer(); dt.items.add(new File([bytes], 'image.png', { type: 'image/png' })); dt.items.add(new File(['milk, oats'], 'list.txt', { type: 'text/plain' }));
	i.focus(); i.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true })); return '' })()`

// oneLineTurn holds when the running turn is one line of Deki, what it does, the timer, the model, the fold and
// Stop, with nothing under it.
const oneLineTurn = `(() => { const t = document.querySelector('[data-turn="running"]'); const h = t?.querySelector('[data-turn-head]'); if (!h) return false;
	const r = h.getBoundingClientRect(); const parts = ['[data-pose]', '[data-turn-now]', '[data-turn-timer]', '[data-turn-model]', '[data-verb="stop"]'].map((q) => h.querySelector(q));
	return parts.every((p) => p && p.getBoundingClientRect().width > 0 && p.getBoundingClientRect().top >= r.top - 1 && p.getBoundingClientRect().bottom <= r.bottom + 1) &&
		r.height < 44 && h.querySelector('[data-turn-now]').innerText === 'runs a command' && h.querySelector('[data-turn-model]').innerText === 'fake' &&
		h.querySelector('[data-turn-fold]').getAttribute('aria-expanded') === 'false' && !t.querySelector('[data-turn-step]') && !t.querySelector('[data-turn-open]') &&
		t.getBoundingClientRect().height < r.height + 2 })()`

// openedTurn holds when the turn shows its two steps and its calls under its line, starting at most a few pixels
// right of the line and ending within it.
const openedTurn = `(() => { const t = document.querySelector('[data-turn="running"]'); const h = t?.querySelector('[data-turn-head]'); const body = t?.querySelector('[data-turn-open]');
	if (!body) return false; const r = h.getBoundingClientRect();
	const inner = [...body.querySelectorAll('[data-turn-step], [data-turn-line]')];
	return h.querySelector('[data-turn-fold]').getAttribute('aria-expanded') === 'true' && body.querySelectorAll('[data-turn-step]').length === 2 && inner.some((l) => l.innerText.includes('records_list')) &&
		[body, ...body.querySelectorAll('*')].every((x) => { const q = x.getBoundingClientRect(); return q.width === 0 || (q.left >= r.left - 1 && q.right <= r.right + 1) }) &&
		inner.every((l) => l.getBoundingClientRect().left - r.left <= 20) })()`
