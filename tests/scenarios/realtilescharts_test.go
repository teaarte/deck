package scenarios

import (
	"bytes"
	"deck2/tests/harness"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"
)

// labManifest is a module of a price by the hour: its widgets put numbers over a chart, its screen charts a week and
// lays its tasks on a board, and a table has its one button in its head.
var labManifest = `{"version": 13, "title": "Laboratory",
  "collections": [
    {"name": "prices", "description": "The price by the hour.", "key": "at", "display": "at",
      "schema": {"type": "object", "properties": {"at": {"type": "string"}, "price": {"type": "number"}}, "required": ["at"]}},
    {"name": "tasks", "description": "Tasks.", "key": "title", "display": "title",
      "schema": {"type": "object", "properties": {"title": {"type": "string"}, "order": {"type": "integer"}, "status": {"type": "string", "enum": ["todo", "doing", "done"]}}}}],
  "commands": [` + harness.AgentCommand("collect", "") + `, ` + harness.AgentCommand("move", `{"type": "object", "properties": {"id": {"type": "integer"}, "status": {"type": "string", "enum": ["todo", "doing", "done"]}}}`) + `],
  "triggers": [{"command": "collect", "cron": "0 * * * *"}],
  "screens": [
    {"name": "main", "title": "Main", "blocks": [
      {"type": "table", "title": "Prices", "source": {"collection": "prices", "sort": "-at", "limit": 5}, "columns": [{"label": "At", "bind": "at"}, {"label": "Price", "bind": "price"}],
        "head": [{"label": "Collect now", "command": "collect", "primary": true}]},
      {"type": "chart", "title": "BTC, 7 days", "span": "full", "source": {"collection": "prices", "sort": "at"}, "x": "at", "series": [{"label": "Price", "y": "price"}], "format": "compact"},
      {"type": "chart", "title": "BTC in dollars", "source": {"collection": "prices", "sort": "at"}, "x": "at", "series": [{"label": "Price", "y": "price"}], "format": "money", "currency": "USD"}]},
    {"name": "board", "title": "Board", "blocks": [
      {"type": "list", "title": "Tasks", "board": "status", "source": {"collection": "tasks", "sort": "order"}, "item": {"title": "title", "tone": "status == 'done' ? 'muted' : ''"},
        "actions": [{"label": "Open", "screen": "task", "primary": true}, {"label": "Move", "command": "move", "params": {"id": "=_id"}, "input": "status"}]}]},
    {"name": "task", "title": "Task", "record": "tasks", "blocks": [{"type": "card", "fields": [{"label": "Title", "bind": "title"}]}]}],
  "menu": ["main", "board"],
  "widgets": [
    {"min": "1x1", "blocks": [{"type": "stat", "title": "Price", "source": {"collection": "prices", "sort": "-at", "limit": 1}, "value": "rows[0].price", "format": "money", "currency": "USD"}]},
    {"min": "2x1", "blocks": [
      {"type": "stat", "title": "Price", "source": {"collection": "prices", "sort": "-at", "limit": 1}, "value": "rows[0].price", "format": "money", "currency": "USD"},
      {"type": "chart", "source": {"collection": "prices", "sort": "at"}, "x": "at", "series": [{"label": "Price", "y": "price"}], "format": "compact"},
      {"type": "stat", "title": "Open trades", "source": {"collection": "tasks", "where": "status != 'done'"}}]},
    {"min": "2x2", "blocks": [
      {"type": "stat", "title": "Price", "source": {"collection": "prices", "sort": "-at", "limit": 1}, "value": "rows[0].price", "format": "money", "currency": "USD"},
      {"type": "stat", "title": "Forecast", "source": {"collection": "prices", "sort": "-at", "limit": 1}, "value": "rows[0].price * 1.01", "format": "money", "currency": "USD"},
      {"type": "chart", "title": "Week", "source": {"collection": "prices", "sort": "at"}, "x": "at", "series": [{"label": "Price", "y": "price"}], "format": "compact"},
      {"type": "stat", "title": "Open trades", "source": {"collection": "tasks", "where": "status != 'done'"}}]}
  ]}`

// console is a picture of Today as the owner's theme draws it: calm on the left, a light device on the right.
func console() string {
	img := image.NewRGBA(image.Rect(0, 0, 1200, 500))
	for y := range 500 {
		for x := range 1200 {
			c := color.RGBA{214, 92, 40, 255}
			if dx, dy := float64(x-860)/300, float64(y-260)/170; dx*dx+dy*dy < 1 {
				c = color.RGBA{236, 232, 224, 255}
				if (x/40+y/40)%2 == 0 {
					c = color.RGBA{60, 58, 54, 255}
				}
			}
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.String()
}

// TestRealBrowserTilesAndCharts: cards take the view of their size with the chart grown and what has no room said;
// Today reads over its picture; axes cut and repeat no label; the board's accent is at work; a table has a head.
func TestRealBrowserTilesAndCharts(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, nil)
	e.Project("lab", labManifest)
	start := time.Now().UTC().Truncate(time.Hour).Add(-7 * 24 * time.Hour)
	var prices []map[string]any
	for h := range 7 * 24 {
		prices = append(prices, map[string]any{"at": start.Add(time.Duration(h) * time.Hour).Format(time.RFC3339), "price": 80000 + 1200*float64(h%37)/37 + float64(h)*3})
	}
	e.Must("deck.records_upsert", map[string]any{"project": "lab", "collection": "prices", "records": harness.Jsonl(prices...)}, nil)
	e.Must("deck.records_upsert", map[string]any{"project": "lab", "collection": "tasks", "records": harness.Jsonl(
		map[string]any{"title": "Finished long ago", "order": 1, "status": "done"},
		map[string]any{"title": "Next to do", "order": 2, "status": "todo"},
		map[string]any{"title": "Doing now", "order": 3, "status": "doing"})}, nil)
	e.Project("lab2", labManifest)
	e.Must("deck.records_upsert", map[string]any{"project": "lab2", "collection": "prices", "records": harness.Jsonl(prices...)}, nil)
	e.Must("deck.theme_save", map[string]any{"name": "pult", "text": `{"version": 1, "title": {"en": "Pult"}, "mood": "m", "accent": "orange"}`}, nil)
	for _, scheme := range []string{"light", "dark"} {
		id := upload(e, "master", "cockpit.png", "image/png", console())
		e.Must("deck.theme_image", map[string]any{"theme": "pult", "slot": "cockpit", "scheme": scheme, "file": fmt.Sprint(id)}, nil)
	}
	e.Must("deck.theme_use", map[string]any{"name": "pult"}, nil)
	b := newPlaywright(t)
	for _, phone := range []bool{false, true} {
		width, height, look := 1280, 900, "pc"
		if phone {
			width, height, look = 390, 844, "phone"
		}
		b.call("browser_resize", map[string]any{"width": width, "height": height})
		b.Open(e, "#/")
		b.until("the cards", `document.querySelector('[data-cell="today"] [data-widget]') && document.querySelector('[data-cols]')`)
		cols := colsOf(b)
		device := b.eval(`localStorage.getItem('deck-device')`)
		if phone {
			if cols != 2 {
				t.Fatalf("a phone has two columns of cells: %d", cols)
			}
			// The phone placed its cards when its grid had one column: they go across both now.
			place(e, device, 1, harness.DeskCard{ID: "today", X: 0, Y: 0, W: 1, H: 1}, harness.DeskCard{ID: "lab", X: 0, Y: 1, W: 1, H: 2}, harness.DeskCard{ID: "lab2", X: 0, Y: 3, W: 1, H: 1})
		} else {
			place(e, device, cols, harness.DeskCard{ID: "today", X: 0, Y: 0, W: 2, H: 1}, harness.DeskCard{ID: "lab", X: 2, Y: 0, W: 2, H: 2}, harness.DeskCard{ID: "lab2", X: 0, Y: 1, W: 2, H: 1})
		}
		b.open(e.URL + "/?reload=" + look + "#/")
		b.until("the view of each size", `document.querySelector('[data-cell="lab"] [data-widget="2x2"]') && document.querySelector('[data-cell="lab2"] [data-widget="2x1"]') &&
			document.querySelectorAll('[data-cell] [data-widget] [data-chart-block] svg').length >= 2`)
		b.until("the 2x2 view whole, its chart grown to the stat under it", `(() => { const w = document.querySelector('[data-cell="lab"] [data-widget]');
			const cells = [...w.querySelector('[data-grid]').children]; const g = w.querySelector('[data-grid]').getBoundingClientRect();
			const chart = cells.find((c) => c.querySelector('[data-chart-block]')).getBoundingClientRect();
			const last = cells[cells.length - 1].getBoundingClientRect();
			return cells.length === 4 && cells.every((c) => c.getBoundingClientRect().left < g.right - 1) && !w.querySelector('[data-widget-left-out]') &&
				chart.height > 3 * w.clientHeight / Number(getComputedStyle(w).getPropertyValue('--thirds')) && g.bottom - last.bottom < 8 })()`)
		b.until("the 2x1 view: the stat and the chart, the block it has no room for said in its head", `(() => { const w = document.querySelector('[data-cell="lab2"] [data-widget]');
			const g = w.querySelector('[data-grid]'); const r = g.getBoundingClientRect(); const cells = [...g.children].map((c) => [c, c.getBoundingClientRect()]);
			return cells[0][1].left === r.left && cells[1][1].left === r.left && cells[1][0].querySelector('[data-chart-block]') && cells[2][1].left >= r.right - 1 && cells[2][0].inert &&
				w.querySelector('[data-widget-left-out]')?.getAttribute('aria-label').includes('Open trades') })()`)
		b.until("Today's lines on a veil over the picture", `(() => { const ls = [...document.querySelectorAll('[data-cell="today"] [data-item]')];
			return ls.length > 0 && ls.every((l) => /^(rgba\(0, 0, 0, 0.6\)|color\(srgb 0 0 0 \/ 0.6\))$/.test(getComputedStyle(l).backgroundColor)) })()`)
		time.Sleep(time.Second)
		if got := b.eval(axesFit); got != "" {
			t.Errorf("on a %s, the axes of the cards cut no label and name each date once: %s", look, got)
		}
		b.shot("tiles-" + look)
		b.open(e.URL + "/?reload=" + look + "#/p/lab/main")
		b.until("the charts", `document.querySelectorAll('[data-chart-block] svg .recharts-yAxis').length >= 2`)
		b.until("the button of the table in its head, no card of its own", `[...document.querySelectorAll('[data-block]')].some((x) => x.innerText.startsWith('Prices') && x.querySelector('button')?.innerText === 'Collect now') &&
			!document.querySelector('[data-block] > [data-actions]:only-child')`)
		time.Sleep(time.Second)
		if got := b.eval(axesFit); got != "" {
			t.Errorf("on a %s, the axes of the screen cut no label and name each date once: %s", look, got)
		}
		b.shot("main-" + look)
		b.open(e.URL + "/?reload=" + look + "#/p/lab/board")
		b.until("the board", `document.querySelectorAll('[data-lane] [data-card]').length === 3`)
		b.until("the accent of the board on the first card at work, none on Done", `document.querySelectorAll('[data-board] [data-verb][data-variant="filled"], [data-board] button[data-variant="filled"]').length === 1 &&
			document.querySelector('[data-lane="todo"] button[data-variant="filled"]') && !document.querySelector('[data-lane="done"] button[data-variant="filled"]')`)
		b.shot("board-" + look)
	}
}

// axesFit is what of the charts on the page cuts a label of its y axis or says a date of its x axis twice.
const axesFit = `(() => {
  const out = [];
  for (const svg of document.querySelectorAll('[data-chart-block] svg.recharts-surface')) {
    const box = svg.getBoundingClientRect();
    for (const t of svg.querySelectorAll('.recharts-yAxis .recharts-cartesian-axis-tick text')) {
      if (t.getBoundingClientRect().left < box.left - 0.5) out.push('cut ' + t.textContent);
    }
    const xs = [...svg.querySelectorAll('.recharts-xAxis .recharts-cartesian-axis-tick text')].map((t) => t.textContent);
    if (new Set(xs).size !== xs.length) out.push('twice ' + xs.join(','));
  }
  return out.join('; ');
})()`
