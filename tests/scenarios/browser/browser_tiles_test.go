package browser

import (
	"bytes"
	"deck2/tests/harness"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRealBrowserTheTilesStandOnTheWayBack: back from a module the tiles stand as they were at once, nothing in them
// moves or comes in; a card of another size settles softly.
func TestRealBrowserTheTilesStandOnTheWayBack(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := harness.New(t, harness.WithAgent(harness.Ok))
	for i := range 3 {
		name := fmt.Sprintf("lab%d", i)
		e.Project(name, strings.Replace(harness.WidgetsOfWork, `"screens": [`, `"screens": [{"name": "main", "title": "Main", "blocks": [{"type": "table", "source": {"collection": "items"}, "columns": [{"label": "Name", "bind": "name"}]}]}, `, 1))
		e.Must("deck.records_upsert", map[string]any{"project": name, "collection": "items", "records": harness.Jsonl(map[string]any{"name": "one", "n": 1})}, nil)
	}
	b := newPlaywright(t)
	// back goes back to the tiles and watches them for a second: they stand in the first frames the address is theirs,
	// then no card moves and nothing in them plays but Deki at rest; it says what went otherwise.
	const back = `new Promise((done) => {
		const at = () => [...document.querySelectorAll('[data-apps] [data-cell]')].map((c) => { const r = c.getBoundingClientRect(); return c.dataset.cell + ':' + Math.round(r.x) + ',' + Math.round(r.y) + ',' + Math.round(r.width); }).join(' ');
		const playing = () => document.getAnimations().filter((a) => a.playState === 'running' && a.effect?.target instanceof Element && a.effect.target.closest('[data-apps]') && !a.effect.target.closest('[data-pose]'))
			.map((a) => a.animationName || a.transitionProperty || 'animation');
		const why = new Set(); let since = -1, first = '';
		const t0 = performance.now();
		const step = () => {
			const now = at();
			if (location.hash === '#/' || location.hash === '') {
				since++;
				if (since > 3 && !first) why.add('no tiles in the first frames');
				if (now && !first) first = now;
				if (first && now !== first) why.add('moved: ' + now);
				if (first) playing().forEach((a) => why.add('plays ' + a));
			}
			if (performance.now() - t0 < 1000) requestAnimationFrame(step); else done(why.size ? JSON.stringify([...why].slice(0, 8)) : 'true');
		};
		history.back();
		requestAnimationFrame(step);
	})`
	for _, size := range []struct{ w, h int }{{1400, 1000}, {390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.w, "height": size.h})
		b.Open(e, "#/")
		b.until("the cards", `document.querySelector('[data-cell="today"] [data-widget]') && document.querySelector('[data-cols]')`)
		cols := int64(colsOf(b))
		// The owner's own layout: the modules first, Today after them.
		e.Place(b.eval(`localStorage.getItem('deck-device')`), int(cols), harness.DeskCard{ID: "lab2", X: 0, Y: 0, W: 1, H: 1}, harness.DeskCard{ID: "lab1", X: 0, Y: 1, W: 1, H: 1},
			harness.DeskCard{ID: "lab0", X: 0, Y: 2, W: 1, H: 1}, harness.DeskCard{ID: "today", X: 0, Y: 3, W: min(2, cols), H: 2})
		b.open(e.URL + "/?reload=" + fmt.Sprint(size.w) + "#/")
		b.until("the cards with their views where the owner put them", `document.querySelectorAll('[data-cell] [data-widget] [data-screen]').length >= 4 &&
			document.querySelector('[data-cell="lab2"]').getBoundingClientRect().y < document.querySelector('[data-cell="today"]').getBoundingClientRect().y`)
		b.eval(`(location.hash = '#/p/lab1/main', '')`)
		b.until("the screen of the module", `location.hash.includes('lab1') && document.querySelector('[data-screen-view] [data-screen]') && !document.querySelector('[data-apps]')`)
		if got := b.eval(back); got != "true" {
			t.Fatalf("the tiles stand on the way back at %dx%d: %s", size.w, size.h, got)
		}
		b.shot(fmt.Sprintf("tiles-back-%d", size.w))
	}
}

// TestRealBrowserThemeStylesTheCards: the cards wear a theme of white cards, a meadow and its own grid on a desktop and a
// phone: DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowserThemeStylesTheCards -v.
func TestRealBrowserThemeStylesTheCards(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Project("web", harness.WebNotes)
	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.Open(e, "#/deck/apps")
	b.until("the cockpit's card in the accent", `(() => { const c = document.querySelector('[data-app-tile][data-big]'); return c && getComputedStyle(c).backgroundImage.includes('gradient') })()`)
	b.shot("tiles-juce")

	cards := `{"version": 1, "title": {"en": "Cards"}, "mood": "m", "schemes": ["light"],
  "tokens": {"cockpit": "linear-gradient(180deg, #ffffff, #f2f6ee)", "tile": "#ffffff", "tile-text": "#0e1116", "tile-shade": "none",
    "tile-icon": "#faece7", "tile-min": "240px", "tile-height": "200px", "tile-gap": "24px",
    "meadow": "radial-gradient(70% 100% at 22% 100%, #b7d79a 0%, transparent 75%)"},
  "parts": {"tile": {"radius": "24px", "shadow": "var(--ring), var(--shadow)", "background": "#ffffff"}}}`
	e.Must("deck.theme_save", map[string]any{"name": "cards", "text": cards}, nil)
	e.Must("deck.theme_use", map[string]any{"name": "cards"}, nil)
	for _, size := range []struct {
		name          string
		width, height int
	}{{"desktop", 1400, 900}, {"phone", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.width, "height": size.height})
		b.open(e.URL + "/#/deck/apps")
		b.until("the cards theme worn", `document.documentElement.dataset.deckTheme === 'cards' && document.querySelector('[data-app-tile]:not([data-big])')`)
		got := b.eval(`(() => { const c = document.querySelector('[data-app-tile]:not([data-big])'), big = document.querySelector('[data-app-tile][data-big]'), s = getComputedStyle(c);
			const cells = [...document.querySelectorAll('[data-cell]')].map((x) => x.getBoundingClientRect()), meadow = document.querySelector('[data-meadow]');
			const gaps = cells.flatMap((a) => cells.flatMap((b) => Math.abs(b.left - a.left) < 1 && b.top > a.bottom ? [b.top - a.bottom]
				: Math.abs(b.top - a.top) < 1 && b.left > a.right ? [b.left - a.right] : [])).map(Math.round);
			return [s.backgroundColor, s.color, s.borderTopLeftRadius, getComputedStyle(big).backgroundImage.includes('rgb(242, 246, 238)'), Math.min(...gaps) + 'px',
				getComputedStyle(c.querySelector('[data-app-tile-icon]')).backgroundColor, !!meadow && getComputedStyle(meadow).backgroundImage.includes('radial-gradient')].join(' ') })()`)
		gap := map[string]string{"desktop": "24px", "phone": "12px"}[size.name]
		if want := "rgb(255, 255, 255) rgb(14, 17, 22) 24px true " + gap + " rgb(250, 236, 231) true"; got != want {
			t.Fatalf("the cards wear their theme on a %s: %s, want %s", size.name, got, want)
		}
		if size.name == "desktop" {
			if h := b.eval(`String(Math.round(document.querySelector('[data-app-tile]:not([data-big])').getBoundingClientRect().height))`); h != "200" {
				t.Fatalf("a card is as high as its theme says: %s", h)
			}
		}
		b.shot("tiles-cards-" + size.name)
	}
	if !strings.Contains(b.eval(`document.getElementById('deck-theme')?.textContent ?? ''`), "--deck-tile-min: 240px") {
		t.Fatal("the grid's least width comes from the theme")
	}
}

// labManifest is a module of a price by the hour: its widgets put numbers over a chart, its screen charts a week and
// lays its tasks on a board, and a table has its one button in its head.
var labManifest = harness.Manifest(`{"title": "Laboratory",
  "collections": [
    {"name": "prices", "description": "The price by the hour.", "key": "at", "display": "at",
      "schema": {"type": "object", "properties": {"at": {"type": "string"}, "price": {"type": "number"}}, "required": ["at"]}},
    {"name": "tasks", "description": "Tasks.", "key": "title", "display": "title",
      "schema": {"type": "object", "properties": {"title": {"type": "string"}, "order": {"type": "integer"}, "status": {"type": "string", "enum": ["todo", "doing", "done"]}}}}],
  "commands": [`) + harness.AgentCommand("collect", "") + `, ` + harness.AgentCommand("move", `{"type": "object", "properties": {"id": {"type": "integer"}, "status": {"type": "string", "enum": ["todo", "doing", "done"]}}}`) + `],
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
		id := e.Upload("master", "cockpit.png", "image/png", console())
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
			e.Place(device, 1, harness.DeskCard{ID: "today", X: 0, Y: 0, W: 1, H: 1}, harness.DeskCard{ID: "lab", X: 0, Y: 1, W: 1, H: 2}, harness.DeskCard{ID: "lab2", X: 0, Y: 3, W: 1, H: 1})
		} else {
			e.Place(device, cols, harness.DeskCard{ID: "today", X: 0, Y: 0, W: 2, H: 1}, harness.DeskCard{ID: "lab", X: 2, Y: 0, W: 2, H: 2}, harness.DeskCard{ID: "lab2", X: 0, Y: 1, W: 2, H: 1})
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
