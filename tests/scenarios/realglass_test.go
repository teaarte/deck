package scenarios

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

// worstContrast is the lowest contrast of the text on the menu island and the tabs over the plain canvas and over
// the strokes of its pattern under the glass.
const worstContrast = `(() => {
  const c = document.createElement('canvas'); c.width = c.height = 1;
  const x = c.getContext('2d', { willReadFrequently: true });
  const paint = (colors) => { x.clearRect(0, 0, 1, 1); for (const col of colors) { x.fillStyle = '#010203'; x.fillStyle = col;
    if (x.fillStyle === '#010203') throw new Error('no color: ' + col); x.fillRect(0, 0, 1, 1); }
    return [...x.getImageData(0, 0, 1, 1).data].slice(0, 3); };
  const lum = (p) => { const [r, g, b] = p.map((v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; });
    return 0.2126 * r + 0.7152 * g + 0.0722 * b; };
  const ratio = (a, b) => { const [h, l] = [lum(a), lum(b)].sort((p, q) => q - p); return (h + 0.05) / (l + 0.05); };
  const canvas = getComputedStyle(document.querySelector('[data-backdrop]')).backgroundColor;
  const stroke = document.querySelector('[data-backdrop] > div');
  const spots = [[canvas], stroke ? [canvas, getComputedStyle(stroke).backgroundColor] : [canvas]];
  let worst = 99;
  for (const [sel, text] of [['[data-nav]', '[data-nav] [data-system]'], ['[data-nav]', '[data-nav] a'], ['[data-nav]', '[data-nav] a[data-active]'],
    ['.mantine-Tabs-list', '[role=tab]'], ['.mantine-Tabs-list', '[role=tab][data-active]']]) {
    const surface = document.querySelector(sel), el = document.querySelector(text);
    if (!surface || !el) continue;
    const fills = [];
    for (let n = el; n && n !== surface; n = n.parentElement) fills.unshift(getComputedStyle(n).backgroundColor);
    for (const spot of spots) {
      const under = [...spot, getComputedStyle(surface).backgroundColor, ...fills];
      worst = Math.min(worst, ratio(paint([...under, getComputedStyle(el).color]), paint(under)));
    }
  }
  return worst.toFixed(2);
})()`

// TestRealBrowserGlass picks the glass theme in Settings → Interface and checks where the glass is and is not:
// DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowser -v.
func TestRealBrowserGlass(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, webNotesAgent())
	e.createProject("web", webNotes)
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": themeProbe}, nil)
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes",
		"records": jsonl(map[string]any{"title": "alpha", "source": "mail"}, map[string]any{"title": "beta", "source": "web"})}, nil)

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1280, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/deck/settings")
	b.until("the appearance tab with juce for a new device", `document.querySelector('[data-look="theme"] input[value="juce"]')?.checked`)
	style := func(sel, prop string) string {
		return b.eval(`getComputedStyle(document.querySelector('` + sel + `')).` + prop)
	}
	if got := style("[data-nav]", "backdropFilter"); got != "none" {
		t.Fatalf("juce has no glass: %s", got)
	}
	b.eval(`(document.querySelector('[data-look="theme"] input[value="glass"]').click(), '')`)
	b.eval(`(document.querySelector('[data-look="scheme"] input[value="light"]').click(), '')`)
	b.until("the light glass theme", `document.documentElement.dataset.deckTheme === 'glass' && document.documentElement.dataset.mantineColorScheme === 'light'`)
	b.eval(`([...document.querySelectorAll('[role=tab]')].find((x) => x.innerText.trim() === 'Interface').click(), '')`)
	b.until("the interface tab", `document.querySelector('[data-appearance]').offsetParent`)
	b.shot("glass-settings")
	if got := style(".mantine-Tabs-list", "backdropFilter"); got == "none" {
		t.Fatalf("the tabs are glass: %s", got)
	}
	if got, _ := strconv.ParseFloat(b.eval(worstContrast), 64); got < 4.5 {
		t.Fatalf("the text of the tabs on the glass is AA: %.2f", got)
	}

	b.open(e.URL + "/#/p/web/main")
	b.until("the table and the custom block", `document.querySelector('[data-block] table') && document.querySelector('[data-custom]')`)
	if got := b.eval(`document.documentElement.dataset.deckTheme + ' ' + JSON.parse(localStorage.getItem('deck-look')).theme`); got != "glass glass" {
		t.Fatalf("the theme is kept on the device: %s", got)
	}
	if got := style("[data-nav]", "backdropFilter"); got == "none" {
		t.Fatalf("the menu island is glass: %s", got)
	}
	table := `[...document.querySelectorAll('[data-block]')].find((x) => x.querySelector('table'))`
	if got := b.eval(`(() => { const s = getComputedStyle(` + table + `); return s.backdropFilter + ' ' + s.backgroundColor })()`); got != "none rgb(255, 255, 255)" {
		t.Fatalf("a block with a table stays solid: %s", got)
	}
	e.waitFor("the custom block to report its theme", func() bool { return len(e.themesSeen()) > 0 })
	if got, body := e.themesSeen()[0], b.eval(`getComputedStyle(document.documentElement).getPropertyValue('--mantine-color-body').trim()`); got.Bg != body {
		t.Fatalf("a custom block gets the solid body of the host as its background: %+v, want %s", got, body)
	}
	if got, _ := strconv.ParseFloat(b.eval(worstContrast), 64); got < 4.5 {
		t.Fatalf("the text on the light glass is AA: %.2f", got)
	} else {
		t.Logf("the lowest contrast on the light glass: %.2f", got)
	}
	b.shot("glass-light")

	b.pick(`[data-filter="source"]`, "web")
	b.until("the glass dropdown of a select", `document.querySelector('.mantine-Select-dropdown')`)
	if got := style(".mantine-Select-dropdown", "backdropFilter"); got == "none" {
		t.Fatalf("the dropdown of a select is glass: %s", got)
	}

	b.open(e.URL + "/#/deck/settings")
	b.until("the appearance tab", `document.querySelector('[data-look="scheme"]')`)
	b.eval(`(document.querySelector('[data-look="scheme"] input[value="dark"]').click(), '')`)
	b.until("the dark scheme", `document.documentElement.dataset.mantineColorScheme === 'dark'`)
	b.open(e.URL + "/#/p/web/main")
	b.until("the table", `document.querySelector('[data-block] table')`)
	if got := style("[data-nav]", "backdropFilter"); got == "none" {
		t.Fatalf("the dark menu island is glass: %s", got)
	}
	if got := b.eval(`(() => { const s = getComputedStyle(` + table + `); return s.backdropFilter + ' ' + /rgba|\/ 0?\./.test(s.backgroundColor) })()`); got != "none false" {
		t.Fatalf("a dark block with a table stays solid: %s", got)
	}
	if got, _ := strconv.ParseFloat(b.eval(worstContrast), 64); got < 4.5 {
		t.Fatalf("the text on the dark glass is AA: %.2f", got)
	} else {
		t.Logf("the lowest contrast on the dark glass: %.2f", got)
	}
	b.shot("glass-dark")

	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.open(e.URL + "/#/p/web/main")
	b.until("the table on a phone", `document.querySelector('[data-block] table')`)
	if got := b.eval(`String(document.documentElement.scrollWidth <= innerWidth + 1)`); got != "true" {
		t.Fatalf("the glass screen fits the width of a phone: %s", got)
	}
	b.shot("glass-phone")
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { const s = await page.context().newCDPSession(page);
		await s.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 5 }); }`})
	b.until("a touch screen", `matchMedia('(pointer: coarse)').matches`)
	if got := b.eval(`[...document.querySelectorAll('input:not([type=hidden]), textarea')].filter((i) => i.offsetParent && parseFloat(getComputedStyle(i).fontSize) < 16).map((i) => i.outerHTML.slice(0, 80)).join(' ')`); got != "" {
		t.Fatalf("fields on a touch screen take 16px, so Safari does not zoom into them: %s", got)
	}
	b.eval(`(localStorage.setItem('deck-look', JSON.stringify({ ...JSON.parse(localStorage.getItem('deck-look')), font: 'large' })), location.reload(), '')`)
	b.until("large fields on a touch screen", `document.querySelector('input[data-field="title"]') && matchMedia('(pointer: coarse)').matches`)
	if got := b.eval(`getComputedStyle(document.querySelector('input[data-field="title"]')).fontSize`); got == "16px" {
		t.Fatalf("a field larger than 16px keeps its size on a touch screen: %s", got)
	}
	if got := style("[data-phone-bar]", "backdropFilter"); got == "none" {
		t.Fatalf("the bar of a phone is glass: %s", got)
	}
	b.eval(`(document.querySelector('[data-nav-open]').click(), '')`)
	b.until("the menu on a phone", `document.querySelector('[data-nav-phone] [data-system]')?.getBoundingClientRect().left >= 0`)
	b.shot("glass-phone-menu")

	media := func(name, value string) {
		feature, _ := json.Marshal(map[string]any{"features": []map[string]string{{"name": name, "value": value}}})
		b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { const s = await page.context().newCDPSession(page);
			await s.send('Emulation.setEmulatedMedia', ` + string(feature) + `); }`})
	}
	b.call("browser_resize", map[string]any{"width": 1280, "height": 900})
	media("prefers-reduced-transparency", "reduce")
	b.open(e.URL + "/#/p/web/main")
	b.until("no glass with reduced transparency", `matchMedia('(prefers-reduced-transparency: reduce)').matches && document.querySelector('[data-nav]') && getComputedStyle(document.querySelector('[data-nav]')).backdropFilter === 'none'`)
	if got := style("[data-nav]", "backgroundColor"); got != "rgb(34, 34, 40)" {
		t.Fatalf("the menu island is solid with reduced transparency: %s", got)
	}
	b.shot("glass-reduced")
	media("prefers-contrast", "more")
	b.until("more contrast", `matchMedia('(prefers-contrast: more)').matches && getComputedStyle(document.querySelector('[data-nav]')).backdropFilter === 'none'`)
	if got := style("[data-nav]", "backgroundColor") + " " + style("[data-nav]", "boxShadow"); got != "rgb(34, 34, 40) rgb(230, 230, 236) 0px 0px 0px 1px" {
		t.Fatalf("the menu island is solid with a plain edge for more contrast: %s", got)
	}
	media("prefers-contrast", "no-preference")

	b.open(e.URL + "/#/deck/settings")
	b.until("the appearance tab", `document.querySelector('[data-look="theme"]')`)
	b.eval(`(document.querySelector('[data-look="theme"] input[value="default"]').click(), '')`)
	b.until("the default theme back", `document.documentElement.dataset.deckTheme === 'default' && getComputedStyle(document.querySelector('[data-nav]')).backdropFilter === 'none'`)
}
