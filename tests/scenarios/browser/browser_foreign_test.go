package browser

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// TestRealBrowserForeignRenderer draws screens of deck with the stock renderer of @a2ui/react and deck's catalog alone,
// none of deck's host: the standard components pass its checks, a dialog reads its blocks, presses are A2UI actions.
func TestRealBrowserForeignRenderer(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	dir := filepath.Join(harness.Root, "web")
	out := t.TempDir()
	build := exec.Command(filepath.Join(dir, "node_modules/.bin/vite"), "build", "foreign", "--outDir", out, "--emptyOutDir")
	build.Dir = dir
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("the foreign page builds: %v\n%s", err, b)
	}
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()))
	e.Project("web", harness.RoutedManifest)
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": harness.Jsonl(map[string]any{"title": "alpha"})}, nil)
	deck, _ := url.Parse(e.URL)
	api, files := httputil.NewSingleHostReverseProxy(deck), http.FileServer(http.Dir(out))
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(page.Close)

	b := Open(t)
	b.call("browser_resize", map[string]any{"width": 1280, "height": 900})
	b.OpenVia(page.URL, e, "&project=web&screen=main")
	b.until("the screen drawn by the stock renderer", `document.querySelector('[data-foreign] [data-screen]') && !document.body.hasAttribute('data-foreign-error') && document.body.innerText.includes('alpha')`)
	b.until("the dialog as a stock Modal with a stock Button of a stock Text", `[...document.querySelectorAll('.a2ui-modal-trigger button')].some((x) => x.innerText === 'Score again')`)
	b.shot("foreign-screen")
	b.eval(`(document.querySelector('.a2ui-modal-trigger button').click(), '')`)
	b.until("the open dialog reads its form", `document.querySelector('.a2ui-modal-overlay [data-form="web.score"]') && document.querySelector('[data-foreign-actions]').innerText.includes('"name":"dialog"')`)
	b.shot("foreign-dialog")
	b.eval(`(document.querySelector('.a2ui-modal-close').click(), '')`)
	b.eval(`([...document.querySelectorAll('[data-foreign] button')].find((x) => x.innerText === 'Tell').click(), '')`)
	b.until("a press as an A2UI action named by its command with the button as its context",
		`JSON.parse(document.querySelector('[data-foreign-actions]').innerText).some((a) => a.name === 'web.tell' && a.context.command === 'web.tell' && a.sourceComponentId === 'b1')`)

	b.OpenVia(page.URL, e, "&screen=spend")
	b.until("deck's tabs as stock Tabs", `document.querySelectorAll('.a2ui-tab-button').length === 3 && !document.body.hasAttribute('data-foreign-error')`)
	b.eval(`(document.querySelectorAll('.a2ui-tab-button')[2].click(), '')`)
	b.until("the third tab with its chart", `document.querySelector('[data-chart-block]')`)
	b.shot("foreign-tabs")
}
