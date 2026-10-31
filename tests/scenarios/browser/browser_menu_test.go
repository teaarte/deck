package browser

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestRealBrowserCockpitHistory: History stands in the head of Today and keeps its place while what runs and what
// waits come in: DECK_REAL_BROWSER=1 go test ./tests/scenarios/browser -run TestRealBrowserCockpitHistory -v.
func TestRealBrowserCockpitHistory(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "score" {
			select {
			case <-hold:
			case <-ctx.Done():
			}
		}
		return harness.WebNotesAgent()(ctx, c)
	}))

	e.Project("web", harness.WebNotes)
	b := Open(t)
	at := `JSON.stringify(document.querySelector('[data-screen-head] [data-dialog-open="changes"]')?.getBoundingClientRect())`
	for _, size := range []struct {
		name          string
		width, height int
	}{{"desktop", 1400, 900}, {"phone", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.width, "height": size.height})
		b.Open(e, "#/deck/cockpit")
		b.until("History in the head of Today", `document.querySelector('[data-screen-head] [data-dialog-open="changes"]') && !document.querySelector('[data-grid] [data-dialog-open]')`)
		before := b.eval(at)
		b.shot("cockpit-history-" + size.name)
		id := e.Start("web", "score", map[string]any{"title": "x"})
		e.WaitState(id, "running")
		b.until("the running chip on Today", `document.querySelector('[data-chips] [data-chip]')`)
		if after := b.eval(at); after != before {
			t.Fatalf("History keeps its place on a %s when a list comes in: %s, then %s", size.name, before, after)
		}
		b.shot("cockpit-history-" + size.name + "-running")
		e.Must("deck.process_stop", map[string]any{"process": id}, nil)
		_, tok := e.Token("ask-"+size.name, "deck.model_set")
		if code, out := e.As(tok).Call("deck.model_set", map[string]any{"connection": "none", "model": size.name}); code != 200 || !strings.Contains(string(out), `"waiting"`) {
			t.Fatalf("a token's model waits for the owner: %d %s", code, out)
		}
		b.until("the decision on Today", `document.querySelector('[data-focus-card]')`)
		if after := b.eval(at); after != before {
			t.Fatalf("History keeps its place on a %s when a decision comes in: %s, then %s", size.name, before, after)
		}
		b.shot("cockpit-decision-" + size.name)
		b.eval(`(document.querySelector('[data-screen-head] [data-dialog-open="changes"]').click(), '')`)
		b.until("the history open", `document.querySelector('[data-dialog="changes"]') && location.hash.includes('dialog=changes')`)
	}
}

// TestRealBrowserCards: deck opens on its tiles, Today first, a module with its cover and no icon, one without with
// its icon, no menu on a tile; a tile opens its module and the way home goes back.
func TestRealBrowserCards(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Project("notes", harness.NotesManifest)
	e.Project("plain", harness.PlainManifest)
	png := e.Upload("notes", "cover.png", "image/png", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\xcf\xc0\xf0\x1f\x00\x05\x00\x01\xff\x89\x99=\x1d\x00\x00\x00\x00IEND\xaeB`\x82")
	e.Must("deck.project_cover_set", map[string]any{"project": "notes", "file": strconv.FormatInt(png, 10)}, nil)
	b := Open(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.Open(e, "")
	b.until("the tiles, Today first, then the modules, one with its cover faded in", `(() => { const c = [...document.querySelectorAll('[data-app]')].map((a) => a.dataset.app);
		const notes = document.querySelector('[data-app=notes]')?.closest('[data-app-tile]');
		return c[0] === 'cockpit' && c.includes('notes') && c.includes('plain') && notes.dataset.cover === 'true' && notes.querySelector('[data-cover-img][data-shown]') })()`)
	if got := b.eval(`(() => { const notes = document.querySelector('[data-module=notes]'), plain = document.querySelector('[data-module=plain]');
		return [!notes.querySelector('[data-app-tile-icon]'), !!plain.querySelector('[data-app-tile-icon]'), !document.querySelector('[data-card-places], [data-card-more]'),
			!document.querySelector('[data-new-module]'), document.querySelector('[data-app-tile][data-big]').getBoundingClientRect().width > 1.9 * plain.getBoundingClientRect().width].join(' ') })()`); got != "true true true true true" {
		t.Fatalf("a tile with a cover has no icon, one without has, no menu on a tile, no tile that only opens the chat, Today on two columns: %s", got)
	}
	b.shot("cards")
	b.until("Deki on Today's tile in the pose of the pulse", `document.querySelector('[data-app=cockpit] ~ [data-card-deki] [data-deki]')`)
	b.eval(`(document.querySelector('[data-card-deki]').click(), '')`)
	b.until("Deki opens the chat with the master", `document.querySelector('[data-chat-close]') && (location.hash === '' || location.hash === '#/')`)
	b.eval(`(document.querySelector('[data-chat-close]').click(), '')`)
	b.eval(`(document.querySelector('[data-app=plain]').click(), '')`)
	b.until("a tile opens its module", `location.hash.startsWith('#/p/plain/') && document.querySelector('[data-apps-home]')`)
	b.until("the module's screens are tabs", `document.querySelector('[data-places] a[aria-current=page]') && document.querySelector('[data-places] a[href="#/p/plain/settings"]')`)
	b.eval(`(document.querySelector('[data-places] a[href="#/p/plain/settings"]').click(), '')`)
	b.until("its settings", `location.hash === '#/p/plain/settings' && document.querySelector('[data-places] a[aria-current=page]').getAttribute('href') === '#/p/plain/settings'`)
	b.until("a module offers to draw its cover for this theme", `document.querySelector('[data-dialog-open="draw-cover"]')`)
	b.eval(`(document.querySelector('[data-dialog-open="draw-cover"]').click(), '')`)
	b.until("the dialog to draw the cover with a wish and a model", `document.querySelector('.mantine-Modal-content [data-field="wish"]') && document.querySelector('.mantine-Modal-content [data-field="model"]') && !document.querySelector('.mantine-Modal-content [data-field="project"]')`)
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { await page.locator('.mantine-Modal-content [data-field="wish"]').fill('a paper boat'); await page.locator('.mantine-Modal-content [data-submit]').click(); }`})
	b.until("the master is told in the chat", `document.querySelector('[data-chat-panel]')?.innerText.includes('a paper boat')`)
	b.eval(`(document.querySelector('[data-chat-close]').click(), '')`)
	dark := e.Upload("plain", "dark.png", "image/png", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\xcf\xc0\xf0\x1f\x00\x05\x00\x01\xff\x89\x99=\x1d\x00\x00\x00\x00IEND\xaeB`\x82")
	e.Must("deck.project_cover_set", map[string]any{"project": "plain", "file": strconv.FormatInt(dark, 10), "scheme": "dark"}, nil)
	b.until("the cover of the module in its settings", `[...document.querySelectorAll('[data-block]')].some((x) => x.innerText.startsWith('Covers') && x.querySelector('img'))`)
	b.eval(`(document.querySelector('[data-apps-home]').click(), '')`)
	b.until("the way home", `document.querySelector('[data-apps]')`)
	b.until("a tile with only a dark cover shows it in the light scheme too", `document.querySelector('[data-app=plain]').closest('[data-app-tile]').dataset.cover === 'true'`)

	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.until("the tiles on a phone", `document.querySelector('[data-phone-tabs]') && document.querySelector('[data-apps]') && !document.querySelector('[data-apps-home]')`)
	b.shot("cards-phone")
}
