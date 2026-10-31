package scenarios

import (
	"bytes"
	"crypto/sha256"
	"deck2/tests/harness"
	"encoding/hex"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRealBrowserCatalog draws the blocks of a forecast module in the real browser of Playwright MCP:
// DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowser -v.
func TestRealBrowserCatalog(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := harness.New(t)
	e.Project("btc", forecastManifest)
	made := time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339)
	e.Must("deck.records_upsert", map[string]any{"project": "btc", "collection": "prices",
		"records": harness.Jsonl(map[string]any{"at": "2026-09-30T10:00:00Z", "price": 60100}, map[string]any{"at": "2026-10-01T10:00:00Z", "price": 61000.5},
			map[string]any{"at": "2026-10-02T10:00:00Z", "price": 62340.25, "forecast": 63000})}, nil)
	e.Must("deck.records_upsert", map[string]any{"project": "btc", "collection": "forecasts",
		"records": harness.Jsonl(map[string]any{"at": made, "horizon": "7d", "change": 0.042, "confidence": 0.6, "status": "open", "source": "https://example.com/a"},
			map[string]any{"at": "2026-09-01T09:00:00Z", "horizon": "1d", "change": -0.01, "confidence": 0.3, "status": "missed"})}, nil)

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1280, "height": 900})
	b.Open(e, "#/p/btc/main")
	b.until("the forecast screen", `document.querySelector('[data-stat]') && document.querySelector('[data-format="relative"]')`)
	b.until("the rows that open under their line", `document.querySelector('[data-row-toggle]')`)
	b.eval(`(document.querySelectorAll('[data-row-toggle]').forEach((x) => x.click()), '')`)
	for what, cond := range map[string]string{
		"money in the locale of the browser":  `document.querySelector('[data-stat]').innerText === new Intl.NumberFormat(navigator.language, { style: 'currency', currency: 'USD' }).format(62340.25)`,
		"a percent of a fraction":             `document.querySelector('[data-format="percent"]').innerText === new Intl.NumberFormat(navigator.language, { style: 'percent', maximumFractionDigits: 1 }).format(0.042)`,
		"a time some minutes ago":             `document.querySelector('[data-format="relative"]').innerText === new Intl.RelativeTimeFormat(navigator.language, { numeric: 'auto' }).format(-5, 'minute')`,
		"a tag":                               `document.querySelector('[data-format="tag"]').innerText.toLowerCase() === 'open'`,
		"a link":                              `document.querySelector('a[href="https://example.com/a"]')`,
		"a stat in the color of its tone":     `getComputedStyle(document.querySelector('[data-stat] [data-tone="ok"]')).color !== getComputedStyle(document.querySelector('[data-stat]')).color`,
		"a fall in another color than a rise": `getComputedStyle(document.querySelector('td [data-tone="error"]')).color !== getComputedStyle(document.querySelector('td [data-tone="ok"]')).color`,
		"a warn tag":                          `document.querySelector('[data-format="tag"][data-tone="warn"]')`,
		"a muted line":                        `getComputedStyle(document.querySelector('[data-item][data-tone="muted"]')).opacity === '0.6'`,
	} {
		b.until(what, cond)
	}
	b.shot("catalog-format")
	b.until("the price chart with its two series", `document.querySelectorAll('[data-chart-block] .recharts-line').length === 2 && document.querySelector('[data-chart-block] .recharts-line path')`)
	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.until("the chart fitted to a phone", `document.querySelector('[data-chart-block] svg').getBoundingClientRect().width <= window.innerWidth && document.querySelector('[data-chart-block] svg').getBoundingClientRect().width > 200`)
	b.shot("catalog-phone")
	if got := b.eval(`String(document.documentElement.scrollWidth <= window.innerWidth)`); got != "true" {
		t.Fatalf("the screen of a phone does not scroll sideways: %s", b.eval(`document.documentElement.scrollWidth + ' > ' + window.innerWidth`))
	}
	b.call("browser_resize", map[string]any{"width": 1280, "height": 900})
	b.eval(`[...document.querySelectorAll('td')].find(x => x.innerText === '7d').click(), ''`)
	b.until("the forecast page opened by a click on its row", `location.hash.startsWith('#/p/btc/forecast/') && document.body.innerText.includes('Still open')`)
	b.eval(`history.back(), ''`)
	b.until("the screen again", `document.querySelector('[data-opens]')`)
	b.eval(`(document.querySelectorAll('[data-row-toggle]').forEach((x) => x.click()), '')`)
	b.until("the link in its opened row", `document.querySelector('a[href="https://example.com/a"]')`)
	b.eval(`document.querySelector('a[href="https://example.com/a"]').addEventListener('click', (e) => e.preventDefault()), ''`)
	b.eval(`document.querySelector('a[href="https://example.com/a"]').click(), ''`)
	time.Sleep(500 * time.Millisecond)
	if h := b.eval(`location.hash`); h != "#/p/btc/main" {
		t.Fatalf("a link in a row opens the link, not the row: %s", h)
	}

	e.Project("fresh", strings.ReplaceAll(forecastManifest, `"title": "BTC"`, `"title": "Fresh"`))
	b.Open(e, "#/p/fresh/main")
	b.until("the words of empty blocks", `[...document.querySelectorAll('[data-empty]')].map(x => x.innerText).join('|') === 'No prices yet|No forecasts yet|Nothing here|No prices yet'`)
	b.shot("catalog-empty")
	if b.eval(`String(document.body.innerText.includes('Missed'))`) != "false" {
		t.Fatal("a block whose when does not hold is not drawn")
	}
	e.Must("deck.records_upsert", map[string]any{"project": "fresh", "collection": "forecasts",
		"records": harness.Jsonl(map[string]any{"at": "2026-09-01T09:00:00Z", "horizon": "1d", "change": -0.01, "status": "missed"})}, nil)
	b.until("the block its when now holds for, without a reload", `[...document.querySelectorAll('[data-block] h3')].some(x => x.innerText === 'Missed')`)
	e.Must("deck.records_upsert", map[string]any{"project": "fresh", "collection": "forecasts", "records": harness.Jsonl(map[string]any{"at": "3", "horizon": "2d"})}, nil)
	b.until("a time that is not one shown as it is", `[...document.querySelectorAll('[data-format="relative"]')].some(x => x.innerText === '3')`)
}

// TestRealBrowserMedia shows a picture, a PDF and another file of a module in the real browser of Playwright MCP;
// a download gives the same file.
func TestRealBrowserMedia(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := harness.New(t)
	id := e.Project("btc", forecastManifest)
	var pic bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{30, 120, 220, 255}}, image.Point{}, draw.Src)
	_ = png.Encode(&pic, img)
	scan := e.Upload("btc", "scan.png", "image/png", pic.String())
	pdf := "%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj 2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj " +
		"3 0 obj<</Type/Page/MediaBox[0 0 200 100]/Parent 2 0 R>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF"
	data := e.App.Home.Data(id)
	_ = os.MkdirAll(data, 0o700)
	_ = os.WriteFile(filepath.Join(data, "report.pdf"), []byte(pdf), 0o600)
	_ = os.WriteFile(filepath.Join(data, "notes.csv"), []byte("a,b\n1,2\n"), 0o600)
	_ = os.WriteFile(filepath.Join(data, "logo.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), 0o600)
	e.Must("deck.records_upsert", map[string]any{"project": "btc", "collection": "attachments", "records": harness.Jsonl(
		map[string]any{"title": "a scan", "file": scan}, map[string]any{"title": "b report", "file": "report.pdf"},
		map[string]any{"title": "c notes", "file": "notes.csv"}, map[string]any{"title": "d gone", "file": "gone.png"},
		map[string]any{"title": "e logo", "file": "logo.svg"})}, nil)

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1280, "height": 900})
	b.Open(e, "#/p/btc/files")
	b.eval(`(localStorage.removeItem('deck-files-view'), '')`)
	b.until("the files as tiles", `document.querySelectorAll('[data-tile]').length === 5`)
	b.until("the picture as its thumbnail", `document.querySelector('[data-thumb="image"]')?.naturalWidth === 64`)
	b.until("an SVG as the badge of its type, never a picture", `(() => { const t = [...document.querySelectorAll('[data-tile]')].find((x) => x.innerText.includes('e logo')); return t && !t.querySelector('img') && t.querySelector('[data-type-icon]') })()`)
	b.until("the error of a missing file on its tile", `document.querySelector('[data-media="error"]')?.innerText.includes('gone.png')`)
	b.shot("media-tiles")
	b.eval(`([...document.querySelectorAll('[data-tile]')].find((x) => x.innerText.includes('b report')).click(), '')`)
	b.until("the PDF opened in the lightbox", `!!(document.querySelector('[data-lightbox] [data-media="pdf"] canvas[data-page="1"]')?.width > 100 || document.querySelector('[data-lightbox] [data-media="pdf-native"]'))`)
	b.eval(`(document.querySelector('[data-lightbox-next]').click(), '')`)
	b.until("the next file, a text, from its start", `document.querySelector('[data-lightbox] [data-media="text"]')?.innerText.includes('a,b')`)
	b.shot("media-lightbox")
	b.eval(`(document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })), document.querySelector('[data-lightbox] button.mantine-Modal-close')?.click(), '')`)
	b.eval(`([...document.querySelectorAll('[data-files-mode] input')].find((x) => x.value === 'list').click(), '')`)
	b.until("the list with the size of each file", `[...document.querySelectorAll('[data-row]')].some((x) => x.innerText.includes('c notes') && x.innerText.includes('8'))`)
	b.shot("media-list")
	if logs := b.call("browser_console_messages", map[string]any{}); strings.Contains(logs, "Content Security Policy") {
		t.Fatalf("the PDF is drawn, not blocked: %s", logs)
	}
	b.eval(`(() => { window.saved = []; const make = URL.createObjectURL; URL.createObjectURL = (x) => { window.saved.push(x); return make(x) };
		HTMLAnchorElement.prototype.click = function () { window.savedName = this.download }; return '' })()`)
	b.eval(`([...document.querySelectorAll('[data-row]')].find((x) => x.innerText.includes('a scan')).querySelector('button').click(), '')`)
	b.until("the picture in full", `document.querySelector('[data-lightbox] [data-media-full]')`)
	b.eval(`(document.querySelector('[data-lightbox] [data-download]').click(), '')`)
	b.until("the download", `window.savedName === 'scan.png'`)
	sum := sha256.Sum256(pic.Bytes())
	got := b.eval(`(async () => { const x = window.saved.at(-1); const h = await crypto.subtle.digest('SHA-256', await x.arrayBuffer());
		return [...new Uint8Array(h)].map(v => v.toString(16).padStart(2, '0')).join('') })()`)
	if got != hex.EncodeToString(sum[:]) {
		t.Fatalf("the download is the same file: %s", got)
	}
}
