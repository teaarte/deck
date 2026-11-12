package browser

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// readingManifest is a demo module of articles to read: a side panel, a chart and a table, two widget views and a
// command whose agent asks the owner what goes into a digest.
var readingManifest = harness.Manifest(`{"title": "Reading list",
  "collections": [
    {"name": "articles", "description": "An article.", "key": "title", "display": "title",
      "schema": {"type": "object", "properties": {"title": {"type": "string"}, "source": {"type": "string"}, "status": {"type": "string"},
        "minutes": {"type": "integer"}}, "required": ["title"]}},
    {"name": "days", "description": "Articles read a day.", "key": "day",
      "schema": {"type": "object", "properties": {"day": {"type": "string"}, "read": {"type": "integer"}}, "required": ["day"]}}],
  "commands": [{"name": "share", "title": "Share", "description": "Sends the week's digest.", "result": {"type": "object"},
    "executor": {"type": "agent", "prompt": "Send the digest."}}],
  "screens": [{"name": "list", "title": "Reading list", "params": {"status": "string"},
    "side": [
      {"type": "filter", "title": "Status", "param": "status", "options": ["inbox", "reading", "done"]},
      {"type": "stat", "title": "To read", "source": {"collection": "articles", "where": "status != 'done'"}},
      {"type": "stat", "title": "Minutes left", "source": {"collection": "articles", "where": "status != 'done'"}, "value": "sum(rows, .minutes)"}],
    "blocks": [
      {"type": "chart", "kind": "bar", "title": "Read this week", "source": {"collection": "days", "sort": "day"}, "x": "day",
        "series": [{"label": "Articles", "y": "read"}]},
      {"type": "table", "title": "Articles", "source": {"collection": "articles", "sort": "title", "where": "param.status == nil || status == param.status"},
        "columns": [{"label": "Title", "bind": "title"}, {"label": "Source", "bind": "source"},
          {"label": "Status", "bind": "status", "format": "tag", "tone": "status == 'done' ? 'ok' : 'none'"}, {"label": "Minutes", "bind": "minutes"}]}]}],
  "widgets": [
    {"min": "1x1", "blocks": [{"type": "stat", "title": "To read", "source": {"collection": "articles", "where": "status != 'done'"}}]},
    {"min": "2x2", "blocks": [{"type": "stat", "title": "To read", "source": {"collection": "articles", "where": "status != 'done'"}},
      {"type": "list", "title": "Next", "source": {"collection": "articles", "where": "status == 'inbox'", "sort": "title"}, "item": {"title": "title", "meta": "source"}}]}],
  "menu": ["list"]}`)

// pricesManifest is a demo module that watches a price: a chart widget and a screen of the chart and a table.
var pricesManifest = harness.Manifest(`{"title": "Price watch",
  "collections": [{"name": "prices", "description": "A price.", "key": "day",
    "schema": {"type": "object", "properties": {"day": {"type": "string"}, "price": {"type": "number"}}, "required": ["day"]}}],
  "screens": [{"name": "main", "title": "Price watch", "blocks": [
    {"type": "chart", "title": "Price", "source": {"collection": "prices", "sort": "day"}, "x": "day", "series": [{"label": "Price", "y": "price"}],
      "format": "money", "currency": "USD"}]}],
  "widgets": [
    {"min": "1x1", "blocks": [{"type": "stat", "title": "Last", "source": {"collection": "prices", "sort": "-day", "limit": 1}, "value": "rows[0].price",
      "format": "money", "currency": "USD"}]},
    {"min": "2x1", "blocks": [{"type": "chart", "title": "Price", "source": {"collection": "prices", "sort": "day"}, "x": "day",
      "series": [{"label": "Price", "y": "price"}], "format": "money", "currency": "USD"}]}],
  "menu": ["main"]}`)

// mailerManifest is a demo module that comes as a draft: a schedule, a command that sends mail out and a site it reads.
var mailerManifest = harness.Manifest(`{"title": "Weekly mailer",
  "commands": [
    {"name": "collect", "title": "Collect", "description": "Collects the week's links.", "input": {"type": "object", "properties": {}},
     "executor": {"type": "agent", "prompt": "Collect the week's links."}, "result": {"type": "object"}},
    {"name": "send", "title": "Send", "description": "Mails the week's links to the owner.",
     "input": {"type": "object", "properties": {"process": {"type": "integer", "format": "process"}}},
     "executor": {"type": "agent", "prompt": "Mail the links."}, "result": {"type": "object"}}],
  "triggers": [{"command": "collect", "cron": "0 9 * * 1"}, {"command": "send", "after": "collect"}],
  "rights": [{"right": "network", "what": ["news.ycombinator.com"], "why": "reads it"},
    {"right": "send", "what": ["send"], "why": "mails the owner the week's links"}]}`)

// digestManifest is a demo module of the morning's news: a list widget.
var digestManifest = harness.Manifest(`{"title": "Morning digest",
  "collections": [{"name": "items", "description": "A story.", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "source": {"type": "string"}}, "required": ["title"]}}],
  "screens": [{"name": "main", "title": "Morning digest", "blocks": [
    {"type": "list", "title": "Today", "source": {"collection": "items", "sort": "title"}, "item": {"title": "title", "meta": "source"}}]}],
  "widgets": [{"min": "1x1", "blocks": [{"type": "list", "title": "Today", "source": {"collection": "items", "sort": "title"}, "item": {"title": "title"}}]}],
  "menu": ["main"]}`)

// showcaseAgent asks what goes into the digest on share and answers the owner's question in the chat with a chart under its text.
func showcaseAgent(ctx context.Context, c *harness.Call) provider.Result {
	if c.Req.Resume {
		return provider.Result{Structured: []byte(`{}`)}
	}
	if c.Command() == "share" {
		c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{{"header": "This week's digest", "question": "What goes into this week's digest?",
			"options": []map[string]any{{"label": "Everything read", "description": "five articles"}, {"label": "Only the best two"}, {"label": "Skip this week"}}}}})
		return provider.Result{Structured: []byte(`{}`)}
	}
	if strings.Contains(c.Req.Prompt, "How did my reading go") {
		if out, bad := c.Tool(ctx, "chat_block_show", map[string]any{"block": `{"type": "chart", "title": "Articles read", "kind": "bar",
  "x": ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"], "series": [{"label": "Articles", "values": [2, 1, 3, 0, 2, 4, 3]}]}`}); bad {
			return provider.Result{Text: "refused: " + out}
		}
		return provider.Result{Text: "Fifteen articles this week, most on Saturday. Four are still in the inbox, about 40 minutes."}
	}
	return provider.Result{Text: "ok"}
}

// TestRealBrowserShowcase: deck on demo modules as the README shows it: the tiles with live widgets, Today with one
// decision, the tiles on a phone, Deki answering with a chart beside a module screen, and a draft before enabling.
func TestRealBrowserShowcase(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t, harness.WithProvider(&harness.Fake{ProviderName: "claude-code", Script: showcaseAgent}))
	reading := e.Project("reading", readingManifest)
	e.Project("prices", pricesManifest)
	e.Project("digest", digestManifest)
	e.Must("deck.record_upsert", map[string]any{"project": "reading", "collection": "articles", "records": harness.Jsonl(
		map[string]any{"title": "How SQLite stays small", "source": "blog", "status": "inbox", "minutes": 12},
		map[string]any{"title": "Notes on sandboxing", "source": "paper", "status": "inbox", "minutes": 18},
		map[string]any{"title": "A tour of MCP", "source": "docs", "status": "reading", "minutes": 7},
		map[string]any{"title": "Writing less code", "source": "blog", "status": "inbox", "minutes": 4},
		map[string]any{"title": "Passkeys in practice", "source": "blog", "status": "done", "minutes": 9},
		map[string]any{"title": "Cron, revisited", "source": "newsletter", "status": "done", "minutes": 6})}, nil)
	var days []map[string]any
	for i, n := range []int{2, 1, 3, 0, 2, 4, 3} {
		days = append(days, map[string]any{"day": fmt.Sprintf("%d %s", i+1, []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}[i]), "read": n})
	}
	e.Must("deck.record_upsert", map[string]any{"project": "reading", "collection": "days", "records": harness.Jsonl(days...)}, nil)
	var prices []map[string]any
	for i, p := range []float64{41.2, 42.0, 41.5, 43.1, 44.8, 44.1, 45.6, 46.3, 45.9, 47.2} {
		prices = append(prices, map[string]any{"day": fmt.Sprintf("11-%02d", i+1), "price": p})
	}
	e.Must("deck.record_upsert", map[string]any{"project": "prices", "collection": "prices", "records": harness.Jsonl(prices...)}, nil)
	e.Must("deck.record_upsert", map[string]any{"project": "digest", "collection": "items", "records": harness.Jsonl(
		map[string]any{"title": "A new release of Go", "source": "news"}, map[string]any{"title": "Rain in the afternoon", "source": "weather"},
		map[string]any{"title": "Three talks worth watching", "source": "video"})}, nil)
	e.Run(reading, "share")
	e.Until("the question waits for the owner", func() bool { return len(e.CockpitItems()) == 1 })

	b := Open(t)
	b.call("browser_resize", map[string]any{"width": 1440, "height": 900})
	b.Open(e, "#/")
	b.until("the tiles", `document.querySelector('[data-cell="today"]') && document.querySelector('[data-cell="reading"] [data-widget]')`)
	place(b, colsOf(b), harness.DeskCard{ID: "today", X: 0, Y: 0, W: 2, H: 2}, harness.DeskCard{ID: "reading", X: 2, Y: 0, W: 2, H: 2},
		harness.DeskCard{ID: "prices", X: 4, Y: 0, W: 2, H: 1}, harness.DeskCard{ID: "digest", X: 4, Y: 1, W: 1, H: 1})
	b.open(b.base + "/?reload=1#/")
	b.until("the widgets in their places", `document.querySelector('[data-cell="reading"] [data-widget="2x2"] [data-item]') && document.querySelector('[data-cell="prices"] [data-widget="2x1"] svg')`)
	b.call("browser_resize", map[string]any{"width": 1440, "height": 540})
	b.until("the widgets kept in a lower window", `document.querySelector('[data-cell="prices"] [data-widget="2x1"] svg')`)
	b.shot("readme-tiles")

	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.open(b.base + "/?reload=2#/")
	b.until("the tiles on a phone", `document.querySelector('[data-phone-tabs]') && document.querySelector('[data-cell="reading"] [data-widget]')`)
	if got := b.eval(`String(document.documentElement.scrollWidth <= innerWidth + 1)`); got != "true" {
		t.Fatalf("the tiles fit the phone: %s", got)
	}
	b.call("browser_take_screenshot", map[string]any{"filename": "web-readme-phone.png"})

	b.call("browser_resize", map[string]any{"width": 1440, "height": 960})
	b.open(b.base + "/?reload=3#/deck/cockpit")
	b.until("one decision on Today", `document.querySelector('[data-focus]')?.dataset.left === '1'`)
	b.shot("readme-today")

	b.open(b.base + "/#/p/reading/list")
	b.until("the module screen with its panel, chart and table", `document.querySelector('[data-side]') && document.querySelector('[data-block] svg') && document.querySelectorAll('[data-grid] tbody tr').length === 6`)

	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the chat", `document.querySelector('[data-chat-input]')`)
	b.type_(`document.querySelector('[data-chat-input]')`, "How did my reading go this week?")
	b.eval(`(document.querySelector('[data-chat-send]').click(), '')`)
	b.until("Deki's answer with a chart under it", `[...document.querySelectorAll('[data-message="master"]')].some((m) => m.innerText.includes('Fifteen articles') && m.querySelector('[data-shown="chart"] svg'))`)
	b.shot("readme-chat")
	b.eval(`(document.querySelector('[data-chat-close]').click(), '')`)
	b.until("the chat closed", `!document.querySelector('[data-chat-input]')?.checkVisibility()`)

	_, tok := e.Token("import", "deck.*")
	if code, out := e.As(tok).Call("deck.project_create", map[string]any{"name": "mailer", "manifest": mailerManifest}); code != 200 {
		t.Fatalf("the mailer comes as a draft: %d %s", code, out)
	}
	b.call("browser_resize", map[string]any{"width": 1440, "height": 540})
	b.open(b.base + "/#/deck/settings?tab=Modules")
	b.until("the draft in the modules", itemOf("Weekly mailer", "draft"))
	b.eval(pressIn(itemOf("Weekly mailer", "draft"), "review"))
	b.until("the page of what the draft may do", `location.hash.startsWith('#/deck/draft') && document.querySelector('[data-verb="enable"]') && document.body.innerText.includes('What it may do once enabled')`)
	b.shot("readme-draft")
}
