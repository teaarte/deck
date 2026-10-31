package browser

import (
	"deck2/tests/harness"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// fitsItsCard is what of the cards of the tiles goes out of its card, or wraps a number or a button: empty when
// nothing does.
const fitsItsCard = `(() => {
  const out = [];
  for (const card of document.querySelectorAll('[data-cell]')) {
    const c = card.getBoundingClientRect();
    for (const el of card.querySelectorAll('[data-widget] *, [data-widget-head] *')) {
      const r = el.getBoundingClientRect();
      if (!r.width || !r.height || getComputedStyle(el).visibility === 'hidden' || el.closest('[hidden]')) continue;
      if (r.left < c.left - 1 || r.right > c.right + 1 || r.top < c.top - 1 || r.bottom > c.bottom + 1) {
        out.push(card.dataset.cell + ': ' + el.tagName + ' ' + (el.innerText || '').slice(0, 30).replace(/\n/g, ' ') + ' out');
        break;
      }
    }
    for (const el of card.querySelectorAll('[data-stat], button')) {
      const r = el.getBoundingClientRect();
      if (!r.width) continue;
      const line = parseFloat(getComputedStyle(el).lineHeight) || parseFloat(getComputedStyle(el).fontSize) * 1.3;
      if (el.matches('[data-stat]') && r.height > line * 1.5) out.push(card.dataset.cell + ': number wraps ' + el.innerText.replace(/\n/g, ' '));
      if (el.scrollWidth > el.clientWidth + 1) out.push(card.dataset.cell + ': cut ' + el.innerText.replace(/\n/g, ' '));
    }
  }
  return out.join('; ');
})()`

// TestRealBrowserWidgetsFitTheirCards: on a computer and a phone, cards of every size show only what they hold: no
// line, number or button goes out of its card or wraps; Today of a low card keeps its last line whole.
func TestRealBrowserWidgetsFitTheirCards(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, nil)
	sizes := [][2]int{{1, 1}, {2, 1}, {2, 2}, {3, 2}, {1, 2}, {3, 3}}
	at := time.Now().Add(-39 * time.Second).UTC().Format(time.RFC3339)
	var recs []map[string]any
	for i := range 12 {
		recs = append(recs, map[string]any{"name": fmt.Sprintf("A rather long name of item number %d that goes on", i), "n": i, "at": at})
	}
	for i := range sizes {
		name := fmt.Sprintf("lab%d", i)
		e.Project(name, harness.WidgetsOfWork)
		e.Must("deck.records_upsert", map[string]any{"project": name, "collection": "items", "records": harness.Jsonl(recs...)}, nil)
	}
	b := newPlaywright(t)
	for _, phone := range []bool{false, true} {
		width, height, look := 1400, 1100, "computer"
		if phone {
			width, height, look = 390, 844, "phone"
		}
		b.call("browser_resize", map[string]any{"width": width, "height": height})
		b.Open(e, "")
		b.until("the cards", `document.querySelector('[data-cell="today"] [data-widget]') && document.querySelector('[data-cols]')`)
		cols := colsOf(b)
		device := b.eval(`localStorage.getItem('deck-device')`)
		today := int64(2)
		if phone {
			today = 1
		}
		cards := []harness.DeskCard{{ID: "today", X: 0, Y: 0, W: int64(min(2, cols)), H: today}}
		x, y, row := int64(min(2, cols)), int64(0), today
		for i, s := range sizes {
			w, h := int64(min(s[0], cols)), int64(s[1])
			if x+w > int64(cols) {
				x, y, row = 0, y+row, 0
			}
			cards = append(cards, harness.DeskCard{ID: fmt.Sprintf("lab%d", i), X: x, Y: y, W: w, H: h})
			x += w
			row = max(row, h)
		}
		e.Place(device, cols, cards...)
		b.open(e.URL + "/?reload=" + look)
		b.until("every card with its view", fmt.Sprintf(`document.querySelectorAll('[data-cell] [data-widget] [data-block]').length >= %d`, len(sizes)+1))
		time.Sleep(time.Second)
		b.shot("widgets-" + look)
		if got := b.eval(fitsItsCard); got != "" {
			t.Errorf("on a %s, cards hold what they show: %s", look, got)
		}
		if got := b.eval(`(() => { const d = document.querySelector('[data-card-deki] img')?.getBoundingClientRect(); if (!d) return 'no Deki';
			return [...document.querySelectorAll('[data-cell="today"] [data-item]')].filter((l) => { const r = l.getBoundingClientRect();
				return r.right > d.left && r.left < d.right && r.bottom > d.top && r.top < d.bottom }).map((l) => l.innerText).join('; ') })()`); got != "" {
			t.Errorf("on a %s, Deki stands clear of Today's lines: %s", look, got)
		}
		if text := b.eval(`document.querySelector('[data-cell="today"]').innerText`); !strings.Contains(text, "Сегодня") {
			t.Errorf("Today in Russian on a %s: %q", look, text)
		}
	}
}
