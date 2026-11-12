package browser

import (
	"encoding/json"
	"testing"

	"deck2/tests/harness"
)

var rangeManifest = harness.Manifest(`{"title": "Shop", "icon": "coin",
  "commands": [
    {"name": "offer", "title": "Offer", "description": "Offers a price.",
      "input": {"type": "object", "required": ["price"], "properties": {
        "price": {"type": "integer", "title": "Price, $", "minimum": 0, "maximum": 1000, "multipleOf": 10, "examples": [50, 100, 200, 500, 1000]},
        "count": {"type": "integer", "title": "Count"},
        "lot": {"type": "integer", "title": "Lot", "minimum": 50, "maximum": 1050, "multipleOf": 100},
        "share": {"type": "number", "title": "Share", "minimum": 0, "exclusiveMaximum": 0.7}}},
      "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Offer it."}}],
  "screens": [{"name": "main", "title": "Shop", "blocks": [{"type": "form", "title": "Offer a price", "command": "offer"}]}],
  "menu": ["main"]
}`)

// TestRealBrowserRangeSlides: a number with both bounds slides, its exact value beside the label, its examples named
// marks that pull a finger near them but not the arrows; the value left goes into the call; one without bounds is a field.
func TestRealBrowserRangeSlides(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t)
	e.Project("shop", rangeManifest)
	b := Open(t)
	for _, size := range []struct {
		name string
		w, h int
	}{{"desktop", 1400, 900}, {"phone", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.w, "height": size.h})
		b.Open(e, "#/p/shop/main")
		b.until(size.name+": the price as a slider", `document.querySelector('[data-field="price"][data-range] [data-range-slider]')`)
		b.until(size.name+": the exact value beside its label", `(() => { const f = document.querySelector('[data-field="price"]');
			const l = f.querySelector('label').getBoundingClientRect(), v = f.querySelector('[data-range-value]').getBoundingClientRect();
			return Math.abs((l.top + l.bottom) / 2 - (v.top + v.bottom) / 2) < 8 && v.left > l.right })()`)
		b.until(size.name+": the marks named under the track, none over another", `(() => { const ls = [...document.querySelectorAll('[data-field="price"] .mantine-Slider-markLabel')];
			const r = ls.map((x) => x.getBoundingClientRect());
			return ls.length >= 3 && ls.some((x) => x.innerText.trim() === '200') && r.every((x, i) => i === 0 || x.left >= r[i - 1].right) })()`)
		b.until(size.name+": a number without bounds stays a field", `!document.querySelector('[data-field="count"] [data-range-slider]') && document.querySelector('input[data-field="count"], [data-field="count"] input')`)
		b.until(size.name+": a slider keeps to the grid deck checks, inside its bounds", `(() => {
			const lot = document.querySelector('[data-field="lot"] [role="slider"]'), share = document.querySelector('[data-field="share"] [role="slider"]');
			return lot.getAttribute('aria-valuemin') === '100' && lot.getAttribute('aria-valuemax') === '1000'
				&& Number(share.getAttribute('aria-valuemax')) === 0.699 && document.querySelector('[data-field="lot"] [data-empty]') })()`)
		b.until(size.name+": nothing goes past the edge", `document.documentElement.scrollWidth <= window.innerWidth + 1`)
		b.shot("range-" + size.name)
	}
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => {
  const track = await page.locator('[data-field="price"] .mantine-Slider-track').boundingBox();
  const thumb = await page.locator('[data-field="price"] .mantine-Slider-thumb').boundingBox();
  await page.mouse.move(thumb.x + thumb.width / 2, thumb.y + thumb.height / 2);
  await page.mouse.down();
  await page.mouse.move(track.x + track.width * 0.215, track.y + track.height / 2, { steps: 8 });
  await page.mouse.up();
}`})
	b.until("a finger near 200 is pulled to it", `document.querySelector('[data-field="price"] [data-range-value] input, [data-field="price"] input[data-range-value]').value === '200'`)
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => {
  await page.locator('[data-field="price"] .mantine-Slider-thumb').focus();
  await page.keyboard.press('ArrowRight');
}`})
	b.until("an arrow steps past the mark", `document.querySelector('[data-field="price"] [data-range-value] input, [data-field="price"] input[data-range-value]').value === '210'`)
	b.shot("range-moved")
	b.press(`[data-form="shop.offer"] [data-submit]`)
	var got struct{ Price int }
	e.Until("the call with the price left", func() bool {
		var ps []struct {
			ID      int64
			Command string
		}
		e.Must("deck.processes_list", map[string]any{"project": "shop"}, &ps)
		for _, p := range ps {
			var one struct{ Input json.RawMessage }
			if p.Command == "offer" {
				e.Must("deck.process_get", map[string]any{"process": p.ID}, &one)
				return json.Unmarshal(one.Input, &got) == nil
			}
		}
		return false
	})
	if got.Price != 210 {
		t.Fatalf("the call took price %d, not the 210 left on the slider", got.Price)
	}
}

// TestRealBrowserTodaySettingsInTheCorner: the settings of Today stand in the corner of its card with a press area of
// 32 px beside Deki in the head, on a desktop and a phone, and take no press while the cards are arranged.
func TestRealBrowserTodaySettingsInTheCorner(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t)
	b := Open(t)
	for _, size := range []struct {
		name string
		w, h int
	}{{"desktop", 1400, 900}, {"phone", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.w, "height": size.h})
		b.Open(e, "#/")
		b.until(size.name+": the settings of Today", `document.querySelector('[data-today-settings]')`)
		b.until(size.name+": a press area of 32 px in line with the title, its icon at the edge of the title's line, nothing over it", `(() => {
			const a = document.querySelector('[data-today-settings]').getBoundingClientRect();
			const line = document.querySelector('[data-today-settings]').parentElement.getBoundingClientRect();
			const icon = document.querySelector('[data-today-settings] svg').getBoundingClientRect();
			const top = document.elementFromPoint((icon.left + icon.right) / 2, (icon.top + icon.bottom) / 2);
			return a.width >= 32 && a.height >= 32 && Math.abs(icon.right - line.right) < 2 && top?.closest('[data-today-settings]') })()`)
		b.until(size.name+": Deki in the head stands beside the settings", `(() => { const d = document.querySelector('[data-card-deki][data-head]');
			if (!d) return false;
			const a = d.getBoundingClientRect(), g = document.querySelector('[data-today-settings]').getBoundingClientRect();
			return a.right <= g.left + 1 })()`)
		b.shot("today-" + size.name)
	}
	b.press(`[data-placing-toggle]`)
	b.until("the settings take no press while the cards are arranged", `getComputedStyle(document.querySelector('[data-today-settings]')).pointerEvents === 'none'`)
}
